package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"vectra-controller-pro/internal/feedverify"
)

// The panel's update_controller names a package by URL and sha256, and vctl
// installs it as root: whoever controls the panel — its database, an
// operator's stolen login — would be root on every router, and would have
// every router's VPN credentials with it. So vctl installs only a package the
// signed Vectra feed publishes, checked as opkg checks a feed before it trusts
// it: the feed the installer added, its index signed by a key in
// /etc/opkg/keys (the installer files the Vectra feed's key there), the
// package in that index with the job's sha256, built for this router, of the
// version the job names. The feed's signing key never leaves the build host
// (scripts/publish-pro-feed.sh); the panel cannot sign.

// Where install/install.sh leaves the signed Vectra feed (CUSTOMFEEDS,
// FEED_NAME, KEYS) and where OpenWrt names the router's architecture; tests
// stand them in.
var (
	vectraFeedConf     = "/etc/opkg/customfeeds.conf"
	opkgKeysDir        = "/etc/opkg/keys"
	openwrtReleasePath = "/etc/openwrt_release"
)

const (
	vectraFeedName = "vectra_pro"
	// notInSignedFeed starts every refusal of a package the signed Vectra
	// feed does not vouch for: what the panel shows its operator.
	notInSignedFeed = "update_controller: not in the signed Vectra feed (nothing installed)"
	// An index is a few kilobytes (a feed directory is one architecture's
	// handful of packages), a signature under 200 bytes: the caps keep a
	// misbehaving server from costing a 234 MB router more than a megabyte.
	maxFeedIndexBytes     = 1 << 20
	maxFeedSignatureBytes = 4 << 10
)

// signedFeedPackage is the vectra-controller-pro of the signed Vectra feed
// whose sha256 is sha, or why there is none: the feed's line in
// customfeeds.conf, its index (Packages.gz for a src/gz feed, as opkg
// downloads it) and Packages.sig over https, the signature by a key in
// /etc/opkg/keys, the package built for this router and, when the job names
// a version, of that version.
func signedFeedPackage(ctx context.Context, sha, version string) (feedverify.Package, error) {
	var none feedverify.Package
	arch, _, pkgs, err := trustedFeedIndex(ctx)
	if err != nil {
		return none, err
	}
	var has []string
	for _, p := range pkgs {
		if p.Name != proPackageName {
			continue
		}
		if !strings.EqualFold(p.SHA256, sha) {
			has = append(has, p.Version+" (sha256 "+p.SHA256+")")
			continue
		}
		if p.Architecture != arch {
			return none, fmt.Errorf("the feed's %s %s with that sha256 is built for %s, this router is %s", proPackageName, p.Version, p.Architecture, arch)
		}
		if version != "" && p.Version != version {
			return none, fmt.Errorf("the feed's %s with that sha256 is %s, the job names %s", proPackageName, p.Version, version)
		}
		return p, nil
	}
	if len(has) == 0 {
		return none, fmt.Errorf("the feed lists no %s with sha256 %s, and no %s at all", proPackageName, sha, proPackageName)
	}
	return none, fmt.Errorf("the feed lists no %s with sha256 %s; it has %s", proPackageName, sha, strings.Join(has, ", "))
}

// routerArch is the architecture the router's packages are built for:
// DISTRIB_ARCH of /etc/openwrt_release.
func routerArch(file string) (string, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == "DISTRIB_ARCH" {
			if a := strings.Trim(strings.TrimSpace(v), `"'`); a != "" {
				return a, nil
			}
		}
	}
	return "", fmt.Errorf("%s names no DISTRIB_ARCH", file)
}

// trustedFeedIndex verifies before any candidate is selected.
func trustedFeedIndex(ctx context.Context) (string, feedverify.Feed, []feedverify.Package, error) {
	arch, err := routerArch(openwrtReleasePath)
	if err != nil {
		return "", feedverify.Feed{}, nil, fmt.Errorf("cannot tell this router's architecture: %w", err)
	}
	conf, err := os.ReadFile(vectraFeedConf)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", feedverify.Feed{}, nil, err
	}
	feed, ok := feedverify.FindFeed(conf, vectraFeedName)
	if !ok {
		return "", feedverify.Feed{}, nil, fmt.Errorf("no %s feed in %s (the Vectra installer adds it, with its key)", vectraFeedName, vectraFeedConf)
	}
	if err := requireHTTPS(feed.URL); err != nil {
		return "", feedverify.Feed{}, nil, fmt.Errorf("the %s feed %s: %w", vectraFeedName, feed.URL, err)
	}
	raw, err := fetchHTTPS(ctx, feed.IndexURL(), maxFeedIndexBytes)
	if err != nil {
		return "", feedverify.Feed{}, nil, fmt.Errorf("%s: %w", path.Base(feed.IndexURL()), err)
	}
	index, err := feed.Unpack(raw, maxFeedIndexBytes)
	if err != nil {
		return "", feedverify.Feed{}, nil, err
	}
	sig, err := fetchHTTPS(ctx, feed.SignatureURL(), maxFeedSignatureBytes)
	if err != nil {
		return "", feedverify.Feed{}, nil, fmt.Errorf("Packages.sig: %w", err)
	}
	if _, err := feedverify.VerifyWithKeys(opkgKeysDir, index, sig); err != nil {
		return "", feedverify.Feed{}, nil, fmt.Errorf("Packages.sig of %s: %w", feed.URL, err)
	}
	pkgs, err := feedverify.ParseIndex(index)
	if err != nil {
		return "", feedverify.Feed{}, nil, fmt.Errorf("Packages of %s: %w", feed.URL, err)
	}
	return arch, feed, pkgs, nil
}

// The raw panel update lane has the same installed-version floor as automatic
// discovery. A valid old feed signature is not permission to roll back root code.
var signedFloorNewer = newerMaintenanceVersion

func signedControllerVersionFloor(ctx context.Context, candidate string) error {
	installed, e := installedMaintenanceVersion(ctx)
	if e != nil {
		return errors.New("installed controller version unavailable")
	}
	older, e := signedFloorNewer(ctx, installed, candidate)
	if e != nil {
		return errors.New("controller version comparison unavailable")
	}
	if older {
		return errors.New("refusing signed controller downgrade")
	}
	if installed == candidate {
		// opkg would print "up to date" and exit 0: nothing to install, and
		// nothing to report as installed.
		return errControllerUpToDate
	}
	return nil
}

// errControllerUpToDate: the feed's package is the version already installed.
var errControllerUpToDate = errors.New("controller already at the feed's version")
