package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/feedverify"
)

const standXrayVersion = "26.10.21-r1"

// xrayStatus is opkg's status with xray-core at version ("" for none).
func xrayStatus(version string) string {
	s := "Package: vectra-controller-pro\nVersion: " + standInstalledVersion + "\nStatus: install user installed\n\n"
	if version != "" {
		s += "Package: xray-core\nVersion: " + version + "\nStatus: install ok installed\nArchitecture: " + feedArch + "\n\n"
	}
	return s
}

// xrayStand is the signed feed stand with the feed's xray-core published
// beside vctl, a router whose opkg has xray-core at installed and whose
// binary says running; it notes every opkg install it is given.
type xrayStand struct {
	*signedFeedStand
	xray     []byte
	installs [][]string // each opkg install's arguments, files by their content
}

func newXrayStand(t *testing.T, installed, running string) *xrayStand {
	t.Helper()
	s := &xrayStand{signedFeedStand: newSignedFeedStand(t)}
	s.xray = []byte("stands in for xray-core " + standXrayVersion)
	s.signIndex(feedStanza("vectra-controller-pro", feedVersion, feedArch, s.sha) +
		feedStanza("xray-core", standXrayVersion, feedArch, sha256Hex(s.xray)) +
		feedStanza("xray-core", standXrayVersion, "mipsel_24kc", strings.Repeat("4", 64)))
	s.extra = map[string][]byte{feedPath + "/xray-core_" + standXrayVersion + "_" + feedArch + ".ipk": s.xray}

	companionStand(t, xrayStatus(installed))
	prevVersion, prevCommand, prevInstall := xrayBinaryVersion, maintenanceCommand, runControllerInstall
	t.Cleanup(func() {
		xrayBinaryVersion, maintenanceCommand, runControllerInstall = prevVersion, prevCommand, prevInstall
	})
	xrayBinaryVersion = func(context.Context) string { return running }
	standCommand := maintenanceCommand
	maintenanceCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "opkg" && len(args) == 4 && args[0] == "compare-versions" && args[2] == ">" {
			// The stand's versions: only 99.0-r1 is newer than the feed's;
			// no is opkg's exit 1.
			if args[1] == "99.0-r1" {
				return nil, nil
			}
			return nil, exec.Command("sh", "-c", "exit 1").Run()
		}
		return standCommand(ctx, name, args...)
	}
	runControllerInstall = func(_ context.Context, args ...string) ([]byte, error) {
		var call []string
		for _, a := range args {
			if b, err := os.ReadFile(a); err == nil {
				a = string(b)
			}
			call = append(call, a)
		}
		s.installs = append(s.installs, call)
		return []byte("stand-in opkg install\n"), nil
	}
	return s
}

func TestXrayPlanFollowsTheFeed(t *testing.T) {
	for _, tc := range []struct{ name, installed, running, want string }{
		{"the feed's package and its own binary", standXrayVersion, "26.10.21", xrayKeep},
		{"no xray-core", "", "", xrayWith},
		{"an older xray-core", "26.7.28-r1", "26.7.28", xrayWith},
		// The four vctl routers from 2026-10-21: 26.10.17 swapped in by hand
		// over the opkg record of 26.7.28.
		{"an older package under a binary swapped in", "26.7.28-r1", "26.10.17", xrayWith},
		{"the feed's version under another binary", standXrayVersion, "26.10.17", xrayReinstall},
		{"the feed's version, a binary that does not answer", standXrayVersion, "", xrayReinstall},
		{"a newer xray-core", "99.0-r1", "99.0", xrayDowngrade},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newXrayStand(t, tc.installed, tc.running)
			_, x, err := signedFeedPackage(context.Background(), s.sha, "")
			if err != nil || x == nil || x.Package.Version != standXrayVersion || x.Package.Architecture != feedArch {
				t.Fatalf("the feed's xray-core: %+v, %v", x, err)
			}
			if how, err := xrayPlan(context.Background(), x); err != nil || how != tc.want {
				t.Fatalf("plan %q, %v; want %q", how, err, tc.want)
			}
		})
	}
}

// What opkg is given, by plan: the feed's xray-core in the same opkg as vctl
// when it is missing or older (vctl's Depends needs it, and the file given is
// what opkg takes, not another feed's); by itself after vctl, with its force
// flag, when it is the same version or newer — never a --force-* with vctl.
func TestTheSelfUpdateInstallsTheFeedsXray(t *testing.T) {
	vctl := "stands in for vectra-controller-pro " + feedVersion
	xray := "stands in for xray-core " + standXrayVersion
	for _, tc := range []struct {
		name, installed, running string
		want                     [][]string
	}{
		{"keep", standXrayVersion, "26.10.21", [][]string{{vctl}}},
		{"with", "26.7.28-r1", "26.10.17", [][]string{{xray, vctl}}},
		{"reinstall", standXrayVersion, "26.10.17", [][]string{{vctl}, {"--force-reinstall", xray}}},
		{"downgrade", "99.0-r1", "99.0", [][]string{{vctl}, {"--force-downgrade", xray}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newXrayStand(t, tc.installed, tc.running)
			results := map[string][]controlplane.JobResultRequest{}
			mu := &sync.Mutex{}
			d := buildGuardTestDaemon(t, results, mu)
			err := d.executeJob(context.Background(), s.job("x1", nil), controlplane.CheckInResponse{})
			if !errors.Is(err, errControllerRestartRequested) {
				fail, _ := lastResult(results, mu, "x1")
				t.Fatalf("not installed: %v %v", err, fail.Result)
			}
			if !reflect.DeepEqual(s.installs, tc.want) {
				t.Fatalf("opkg install was given\n  %q\nwant\n  %q", s.installs, tc.want)
			}
			for _, call := range s.installs {
				if len(call) > 1 && call[len(call)-1] == vctl {
					for _, a := range call {
						if strings.HasPrefix(a, "--force") {
							t.Fatalf("a force flag with vctl: %q", call)
						}
					}
				}
			}
			r := d.st.PendingJobResult
			if tc.name == "keep" {
				if _, ok := r.Result["xray"]; ok {
					t.Fatalf("an xray reported where none was installed: %v", r.Result)
				}
			} else if r.Result["xray"] != standXrayVersion || r.Result["xrayInstall"] != tc.name {
				t.Fatalf("the result says %v", r.Result)
			}
			if _, err := os.Stat(filepath.Join(os.TempDir(), xrayUpdateFile)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("the downloaded xray-core stayed in /tmp: %v", err)
			}
		})
	}
}

// An xray-core whose download is not what the signed index says installs
// nothing at all, vctl included.
func TestAnXrayThatIsNotTheFeedsInstallsNothing(t *testing.T) {
	s := newXrayStand(t, "26.7.28-r1", "26.7.28")
	s.extra[feedPath+"/xray-core_"+standXrayVersion+"_"+feedArch+".ipk"] = []byte("something else")
	results := map[string][]controlplane.JobResultRequest{}
	mu := &sync.Mutex{}
	d := buildGuardTestDaemon(t, results, mu)
	_ = d.executeJob(context.Background(), s.job("x2", nil), controlplane.CheckInResponse{})
	r, _ := lastResult(results, mu, "x2")
	if msg, _ := r.Result["error"].(string); r.Status != "failure" || !strings.Contains(msg, "xray-core "+standXrayVersion+": checksum mismatch") {
		t.Fatalf("result %+v", r)
	}
	if len(s.installs) != 0 || s.restarts != 0 {
		t.Fatalf("opkg was given %q, restarts %d", s.installs, s.restarts)
	}
}

// A feed with no xray-core for this router: vctl alone, as before.
func TestAFeedWithoutXrayInstallsVctlAlone(t *testing.T) {
	s := newXrayStand(t, "26.7.28-r1", "26.7.28")
	s.signIndex(feedStanza("vectra-controller-pro", feedVersion, feedArch, s.sha) +
		feedStanza("xray-core", standXrayVersion, "mipsel_24kc", strings.Repeat("4", 64)))
	results := map[string][]controlplane.JobResultRequest{}
	mu := &sync.Mutex{}
	d := buildGuardTestDaemon(t, results, mu)
	if err := d.executeJob(context.Background(), s.job("x3", nil), controlplane.CheckInResponse{}); !errors.Is(err, errControllerRestartRequested) {
		t.Fatal(err)
	}
	if want := [][]string{{"stands in for vectra-controller-pro " + feedVersion}}; !reflect.DeepEqual(s.installs, want) {
		t.Fatalf("opkg install was given %q", s.installs)
	}
}

// Connect's automatic update takes the same index's xray-core with the vctl
// it picks; one with a file name the index must not name is refused as a
// vctl's would be.
func TestTheAutomaticUpdateCarriesTheFeedsXray(t *testing.T) {
	maintenanceFixture(t)
	maintenanceCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if len(args) == 2 && args[0] == "status" {
			return []byte("Version: 0.6.0-r37\n"), nil
		}
		return nil, nil
	}
	xray := feedverify.Package{Name: xrayPackageName, Version: standXrayVersion, Architecture: "aarch64_generic",
		Filename: "xray-core_" + standXrayVersion + "_aarch64_generic.ipk", SHA256: strings.Repeat("b", 64)}
	maintenanceFeed = func(context.Context) (string, feedverify.Feed, []feedverify.Package, error) {
		a, f, p, e := fakeMaintenanceFeed("vectra-controller-pro_0.6.0-r38_aarch64_generic.ipk")
		return a, f, append(p, xray), e
	}
	c, err := latestMaintenancePackage(context.Background())
	if err != nil || c == nil || c.Xray == nil || c.Xray.Package != xray ||
		c.Xray.URL != "https://feed.example/aarch64_generic/xray-core_"+standXrayVersion+"_aarch64_generic.ipk" {
		t.Fatalf("candidate %+v, %v", c, err)
	}
	xray.Filename = "../xray.ipk"
	if _, err := latestMaintenancePackage(context.Background()); err == nil {
		t.Fatal("an xray-core named outside the feed's directory was taken")
	}
}

// A new xray-core needs its download in /tmp and the whole binary on flash
// (the running one holds the old file): short of either, nothing at all is
// installed, and the job says so.
func TestNoRoomForTheFeedsXrayInstallsNothing(t *testing.T) {
	for _, short := range []string{"tmp", "flash"} {
		t.Run(short, func(t *testing.T) {
			s := newXrayStand(t, "26.7.28-r1", "26.7.28")
			prev := xrayFreeBytes
			t.Cleanup(func() { xrayFreeBytes = prev })
			xrayFreeBytes = func(path string) (int64, bool) {
				if (short == "tmp") == (path == os.TempDir()) {
					return 8 << 20, true
				}
				return 1 << 30, true
			}
			results := map[string][]controlplane.JobResultRequest{}
			mu := &sync.Mutex{}
			d := buildGuardTestDaemon(t, results, mu)
			_ = d.executeJob(context.Background(), s.job("x4", nil), controlplane.CheckInResponse{})
			r, _ := lastResult(results, mu, "x4")
			if msg, _ := r.Result["error"].(string); r.Status != "failure" || !strings.Contains(msg, "no room for xray-core "+standXrayVersion) {
				t.Fatalf("result %+v", r)
			}
			if len(s.installs) != 0 || s.restarts != 0 {
				t.Fatalf("opkg was given %q", s.installs)
			}
		})
	}
}

// opkg's --force-reinstall removes xray-core before it unpacks the new one:
// one that fails after that leaves no xray, and a plain install puts it back.
// One that fails with xray-core still there leaves vctl updated all the
// same: the update is journaled and restarted into, and the result says what
// went wrong with xray.
func TestAFailedXrayReinstallIsPutRight(t *testing.T) {
	for _, tc := range []struct {
		name      string
		removes   bool // the failed --force-reinstall took xray-core away first
		wantCalls int
		wantError bool
	}{
		{"failed after removing it: installed again", true, 3, false},
		{"failed with it still there", false, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newXrayStand(t, standXrayVersion, "26.10.17")
			var calls [][]string
			runControllerInstall = func(_ context.Context, args ...string) ([]byte, error) {
				calls = append(calls, args)
				if args[0] == "--force-reinstall" {
					if tc.removes {
						if err := os.WriteFile(opkgStatusFile, []byte(xrayStatus("")), 0o644); err != nil {
							t.Fatal(err)
						}
					}
					return []byte(" * opkg_install_pkg: Failed to unpack\n"), errors.New("exit status 255")
				}
				if len(args) == 1 && strings.HasSuffix(args[0], xrayUpdateFile) {
					if err := os.WriteFile(opkgStatusFile, []byte(xrayStatus(standXrayVersion)), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				return nil, nil
			}
			results := map[string][]controlplane.JobResultRequest{}
			mu := &sync.Mutex{}
			d := buildGuardTestDaemon(t, results, mu)
			if err := d.executeJob(context.Background(), s.job("x5", nil), controlplane.CheckInResponse{}); !errors.Is(err, errControllerRestartRequested) {
				r, _ := lastResult(results, mu, "x5")
				t.Fatalf("not journaled as installed: %v %v", err, r.Result)
			}
			if len(calls) != tc.wantCalls || (tc.removes && strings.HasPrefix(calls[2][0], "--")) {
				t.Fatalf("opkg install was given %q", calls)
			}
			r := d.st.PendingJobResult
			if _, has := r.Result["xrayError"]; has != tc.wantError || r.Status != "success" || s.restarts != 1 {
				t.Fatalf("journaled %+v, restarts %d", r, s.restarts)
			}
			if tc.wantError && !strings.Contains(r.Result["xrayError"].(string), "Failed to unpack") {
				t.Fatalf("the xray error does not say what opkg said: %v", r.Result["xrayError"])
			}
		})
	}
}
