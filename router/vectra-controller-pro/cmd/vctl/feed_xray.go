package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"vectra-controller-pro/internal/feedverify"
	"vectra-controller-pro/internal/opkgstatus"
)

// One xray per vctl: the signed Vectra feed ships the xray-core the vctl in it
// depends on and is proven with (build-pro-feed.sh reads it from
// openwrt/Makefile, whose Depends names it as the minimum). The self-update —
// update_controller and Connect's automatic update — installs that one with
// vctl, from the same signed index. opkg alone would satisfy the Depends with
// the newest xray-core of any feed it has a list of, or find none (the lists
// are in RAM, gone after a reboot) and install nothing. And the xray on the
// router is the package's again where a binary was swapped in by hand: the
// four vctl routers ran a script-swapped 26.10.17 under an opkg record of
// 26.7.28 from 2026-10-21.

// xrayBinaryPath is the xray vctl runs (vctl-xray-wrapper execs it); tests
// point it elsewhere.
var xrayBinaryPath = "/usr/bin/xray"

const xrayPackageName = "xray-core"

// xrayUpdateFile is where the self-update downloads the feed's xray-core, in
// /tmp beside vctl's (controllerUpdateFile); gone however the update ends.
const xrayUpdateFile = "xray-core-update.ipk"

// feedXray is the signed feed's xray-core for this router, and where to get it.
type feedXray struct {
	Package feedverify.Package
	URL     string
}

// signedFeedXray is the feed's xray-core built for arch, nil when the feed
// has none. Like vctl's own package, only a plain file name in the feed's
// directory and a well-formed sha256 are taken.
func signedFeedXray(feed feedverify.Feed, pkgs []feedverify.Package, arch string) (*feedXray, error) {
	for _, p := range pkgs {
		if p.Name != xrayPackageName || p.Architecture != arch {
			continue
		}
		if !safeMaintenanceFilename.MatchString(p.Filename) || len(p.Filename) > 255 || strings.ContainsAny(p.Filename, "/\\?#%") {
			return nil, errors.New("the feed's xray-core has an invalid file name")
		}
		if h, err := hex.DecodeString(p.SHA256); err != nil || len(h) != 32 {
			return nil, errors.New("the feed's xray-core has an invalid checksum")
		}
		if !safePackageVersion.MatchString(p.Version) {
			return nil, errors.New("the feed's xray-core has an invalid version")
		}
		return &feedXray{Package: p, URL: strings.TrimRight(feed.URL, "/") + "/" + url.PathEscape(p.Filename)}, nil
	}
	return nil, nil
}

// How the feed's xray-core goes in.
const (
	xrayKeep      = ""          // the package is the feed's, and the binary its own
	xrayWith      = "with"      // none, or older: in one opkg with vctl, whose Depends needs it
	xrayReinstall = "reinstall" // the feed's version, another binary over it: --force-reinstall
	xrayDowngrade = "downgrade" // a newer one: --force-downgrade
)

// xrayPlan says how the feed's xray-core goes in, from opkg's record of
// xray-core and the version the binary itself says.
func xrayPlan(ctx context.Context, x *feedXray) (string, error) {
	if x == nil {
		return xrayKeep, nil
	}
	installed := ""
	if status, err := os.ReadFile(opkgStatusFile); err == nil {
		installed = opkgstatus.Version(status, xrayPackageName)
	}
	switch {
	case installed == "":
		return xrayWith, nil
	case installed == x.Package.Version:
		if xrayBinaryVersion(ctx) == opkgstatus.Upstream(installed) {
			return xrayKeep, nil
		}
		return xrayReinstall, nil
	}
	newer, err := newerMaintenanceVersion(ctx, installed, x.Package.Version)
	if err != nil {
		return "", fmt.Errorf("compare xray-core %s with the feed's %s: %w", installed, x.Package.Version, err)
	}
	if newer {
		return xrayDowngrade, nil
	}
	return xrayWith, nil
}

// installControllerWithXray installs the downloaded vctl package (vctlFile)
// with the feed's xray-core as the plan says, and returns what opkg said.
// --force-reinstall and --force-downgrade are only ever xray-core's, in an
// opkg of their own after vctl's: opkg's --force-reinstall removes the
// installed package first, and vctl's removal hands the router back.
func installControllerWithXray(ctx context.Context, x *feedXray, how, vctlFile string) ([]byte, error) {
	if how == xrayKeep {
		return runControllerInstall(ctx, vctlFile)
	}
	if err := roomForXray(x.Package); err != nil {
		return nil, err
	}
	xf := filepath.Join(os.TempDir(), xrayUpdateFile)
	defer os.Remove(xf)
	got, err := downloadFile(ctx, x.URL, xf)
	if err != nil {
		return nil, fmt.Errorf("download xray-core %s: %w", x.Package.Version, err)
	}
	if !strings.EqualFold(got, x.Package.SHA256) {
		return nil, fmt.Errorf("xray-core %s: checksum mismatch: got %s want %s", x.Package.Version, got, x.Package.SHA256)
	}
	if how == xrayWith {
		return runControllerInstall(ctx, xf, vctlFile)
	}
	out, err := runControllerInstall(ctx, vctlFile)
	if err != nil {
		return out, err
	}
	more, err := runControllerInstall(ctx, "--force-"+how, xf)
	return append(out, more...), err
}

// The room a new xray-core needs, by its signed index entry: its download
// in /tmp beside vctl's, and the whole binary unpacked on flash — the xray
// that runs holds the old file until vctl restarts it, so the old one frees
// nothing meanwhile. Short of either, nothing is installed: opkg running out
// of room halfway leaves a binary cut short. overlayPath and freeBytes are
// stood in by tests.
var (
	overlayPath   = "/overlay"
	xrayFreeBytes = freeBytes
)

const xrayRoomMargin = 4 << 20

func roomForXray(p feedverify.Package) error {
	flash := overlayPath
	if _, err := os.Stat(flash); err != nil {
		flash = "/"
	}
	for _, need := range []struct {
		path  string
		bytes int64
	}{{os.TempDir(), p.Size}, {flash, p.InstalledSize}} {
		free, ok := xrayFreeBytes(need.path)
		if !ok {
			return fmt.Errorf("room for xray-core %s on %s: cannot tell", p.Version, need.path)
		}
		if free < need.bytes+xrayRoomMargin {
			return fmt.Errorf("no room for xray-core %s: %d MiB free on %s, it needs %d MiB (nothing installed)",
				p.Version, free>>20, need.path, (need.bytes+xrayRoomMargin)>>20)
		}
	}
	return nil
}

// xrayBinaryVersion is what the binary says it is (`xray version`, its first
// line's second word), "" when it does not answer. Tests stand it in.
var xrayBinaryVersion = func(ctx context.Context) string {
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(c, xrayBinaryPath, "version").Output()
	if err != nil {
		return ""
	}
	first, _, _ := strings.Cut(string(out), "\n")
	if f := strings.Fields(first); len(f) >= 2 && f[0] == "Xray" {
		return f[1]
	}
	return ""
}
