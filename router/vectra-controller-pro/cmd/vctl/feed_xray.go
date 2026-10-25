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
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/opkgstatus"
	"vectra-controller-pro/internal/supervisor"
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

// How the feed's xray-core goes in — always by itself, before vctl.
const (
	xrayKeep      = ""          // the package is the feed's, and the binary its own
	xrayInstall   = "install"   // none, or older: installed (an upgrade)
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
		return xrayInstall, nil
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
	return xrayInstall, nil
}

// fetchFeedXray downloads the feed's xray-core the plan puts in, checked
// against its signed sha256 and the room it needs, and gives the file and
// its removal; no file for a plan that installs none. Before the install's
// deadline: a slow link must not leave opkg too little of it — one killed
// halfway leaves a binary cut short, or none.
func fetchFeedXray(ctx context.Context, x *feedXray, how string) (string, func(), error) {
	if how == xrayKeep {
		return "", func() {}, nil
	}
	if err := roomForXray(x.Package); err != nil {
		return "", nil, err
	}
	xf := filepath.Join(os.TempDir(), xrayUpdateFile)
	done := func() { os.Remove(xf) }
	got, err := downloadFile(ctx, x.URL, xf)
	if err != nil {
		done()
		return "", nil, fmt.Errorf("download xray-core %s: %w", x.Package.Version, err)
	}
	if !strings.EqualFold(got, x.Package.SHA256) {
		done()
		return "", nil, fmt.Errorf("xray-core %s: checksum mismatch: got %s want %s", x.Package.Version, got, x.Package.SHA256)
	}
	return xf, done, nil
}

// controllerInstallTime bounds the self-update's opkg runs: long, as a
// deadline kills opkg where it is — a 32 MB xray unpacked on NAND takes its
// time, and a kill halfway leaves none.
const controllerInstallTime = 10 * time.Minute

// installControllerWithXray installs the feed's xray-core (xrayFile) as the
// plan says, by itself and first, then vctl's package (vctlFile), and returns
// what opkg said. First: vctl's Depends is met then by the package on the
// router, from no feed list (they are in RAM, gone after a reboot), and an
// xray that does not go in leaves vctl as it was. By itself: its
// --force-reinstall (or --force-downgrade) is never vctl's — opkg's
// reinstall removes the installed package first, and vctl's removal hands
// the router back — and it removes xray-core before it unpacks the new one,
// so one that fails after that is put back by a plain install. And with
// --force-space, roomForXray having measured first: opkg's own check counts
// xray-core's whole Installed-Size against the free flash, whatever the
// flash compresses (an AX3000T's UBIFS: 31 MB counted, 14 MB taken), and
// refuses on routers that have the room.
//
// Between the two, release (when there is one) restarts the xray that runs
// onto the new binary: the old one held the old binary's file, and its room
// on the flash comes back only when it exits — vctl's own opkg, which counts
// its Installed-Size as opkg does, then finds it.
func installControllerWithXray(ctx context.Context, how, xrayFile, vctlFile string, release func(context.Context)) ([]byte, error) {
	var out []byte
	if how != xrayKeep {
		args := []string{"--force-space"}
		if how == xrayReinstall || how == xrayDowngrade {
			args = append(args, "--force-"+how)
		}
		o, err := runControllerInstall(ctx, append(args, xrayFile)...)
		out = append(out, o...)
		if err != nil && how != xrayInstall && !xrayCoreInstalled() {
			again, err2 := runControllerInstall(ctx, "--force-space", xrayFile)
			out = append(out, again...)
			if err2 == nil && xrayCoreInstalled() {
				logging.L().Warn("xray-core went in again by a plain install after its --force-"+how+" failed", "err", err.Error())
				err = nil
			}
		}
		if err != nil {
			return out, fmt.Errorf("xray-core (vctl not installed): %w", err)
		}
		if release != nil {
			release(ctx)
		}
	}
	o, err := runControllerInstall(ctx, vctlFile)
	return append(out, o...), err
}

// xrayCoreInstalled: opkg's status has xray-core installed.
func xrayCoreInstalled() bool {
	status, err := os.ReadFile(opkgStatusFile)
	return err == nil && opkgstatus.Version(status, xrayPackageName) != ""
}

// The room a new xray-core needs, by its signed index entry: its download
// in /tmp beside vctl's, and the new binary on flash beside the old one —
// the xray that runs holds the old file until vctl restarts it, so the old
// one frees nothing meanwhile. On flash that compresses (UBIFS, JFFS2: the
// /overlay of a squashfs router) that is about the package's own Size:
// measured on an AX3000T's UBIFS, the 13.3 MB package took 14.1 MB, and the
// install's low point was 18.5 MB below the free space it started from — so
// 1.4 × Size there; on other flash its Installed-Size. Short of either,
// nothing is installed: opkg running out of room halfway leaves a binary cut
// short. overlayPath, mountsFile and xrayFreeBytes are stood in by tests.
var (
	overlayPath   = "/overlay"
	mountsFile    = "/proc/mounts"
	xrayFreeBytes = freeBytes
)

// flashCompresses: the filesystem mounted at path compresses what it stores.
func flashCompresses(path string) bool {
	b, err := os.ReadFile(mountsFile)
	if err != nil {
		return false
	}
	compresses := false
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) >= 3 && f[1] == path {
			compresses = f[2] == "ubifs" || f[2] == "jffs2"
		}
	}
	return compresses
}

func xrayFlashNeed(p feedverify.Package, flash string) int64 {
	if p.Size > 0 && flashCompresses(flash) {
		return p.Size * 14 / 10
	}
	return p.InstalledSize
}

const xrayRoomMargin = 4 << 20

func roomForXray(p feedverify.Package) error {
	flash := overlayPath
	if _, err := os.Stat(flash); err != nil {
		flash = "/"
	}
	for _, need := range []struct {
		path  string
		bytes int64
	}{{os.TempDir(), p.Size}, {flash, xrayFlashNeed(p, flash)}} {
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

// releaseOldXray restarts the xray vctl runs onto the binary just installed,
// and waits, up to 30 s, for the new process (installControllerWithXray).
// The old one's binary was the feed's predecessor, and every vctl from
// 0.7.0-r29 writes configs both take.
func (d *daemon) releaseOldXray(ctx context.Context) {
	if d.sup == nil || !d.supStarted {
		return
	}
	before := d.sup.Status().StartedAt
	if err := d.sup.Reload(d.supCtx); err != nil {
		logging.L().Warn("could not restart xray onto the new binary before vctl's install", "err", err.Error())
		return
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		if st := d.sup.Status(); st.State == supervisor.StateRunning && st.StartedAt.After(before) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
	logging.L().Warn("xray did not come back on the new binary within 30 s; vctl's install goes on")
}
