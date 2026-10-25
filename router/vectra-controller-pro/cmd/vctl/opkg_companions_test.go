package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// theDefaultControllerInstall is runControllerInstall as vctl has it, before
// any test stands it in.
var theDefaultControllerInstall = runControllerInstall

// A router whose vctl came from the pro feed: vectra-geodata and
// vectra-reporter came in as its dependencies, as did xray-core and a kmod.
const companionStatus = `Package: vectra-controller-pro
Version: 0.7.0-r28
Depends: ca-bundle, xray-core (>= 26.3.27), vectra-geodata, vectra-reporter (>= 1.0.0-r4)
Status: install user installed
Architecture: aarch64_cortex-a53
Conffiles:
 /etc/config/vectra-controller-pro 1f2e
Installed-Time: 1792900000

Package: vectra-geodata
Version: 2026.9.28-r2
Status: install ok installed
Architecture: all
Installed-Time: 1792900000
Auto-Installed: yes

Package: xray-core
Version: 26.7.28-r1
Status: install ok installed
Architecture: aarch64_cortex-a53
Installed-Time: 1792900000
Auto-Installed: yes

Package: vectra-reporter
Version: 1.0.0-r4
Status: install ok installed
Architecture: aarch64_cortex-a53
Conffiles:
 /etc/config/vectra-reporter 9a8b
Installed-Time: 1792900000
Auto-Installed: yes

Package: vectra-geodata-extra
Version: 1
Status: install ok installed
Architecture: all
Installed-Time: 1792900000
Auto-Installed: yes

Package: kmod-nft-tproxy
Version: 6.6.119-r1
Status: install ok installed
Architecture: aarch64_cortex-a53
Installed-Time: 1792900000
Auto-Installed: yes

`

func companionStand(t *testing.T, status string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "status")
	if err := os.WriteFile(p, []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}
	prevStatus, prevProc := opkgStatusFile, opkgProcDir
	opkgStatusFile, opkgProcDir = p, filepath.Join(dir, "proc")
	t.Cleanup(func() { opkgStatusFile, opkgProcDir = prevStatus, prevProc })
	return p
}

// Only the companions lose the mark: xray-core, the kmod and a package whose
// name merely starts like one keep theirs, and every other byte stays.
func TestKeepCompanionsDropsOnlyTheirMarks(t *testing.T) {
	p := companionStand(t, companionStatus)
	kept, err := keepCompanions()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(kept, []string{"vectra-geodata", "vectra-reporter"}) {
		t.Fatalf("kept %v", kept)
	}
	got, _ := os.ReadFile(p)
	want := strings.Replace(companionStatus, "Installed-Time: 1792900000\nAuto-Installed: yes\n\nPackage: xray-core",
		"Installed-Time: 1792900000\n\nPackage: xray-core", 1)
	want = strings.Replace(want, " /etc/config/vectra-reporter 9a8b\nInstalled-Time: 1792900000\nAuto-Installed: yes\n",
		" /etc/config/vectra-reporter 9a8b\nInstalled-Time: 1792900000\n", 1)
	if string(got) != want {
		t.Fatalf("status now:\n%s\nwant:\n%s", got, want)
	}
	if strings.Count(string(got), autoInstalledMark) != 3 {
		t.Fatalf("the other packages' marks are gone:\n%s", got)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if _, err := os.Stat(p + ".vctl"); !os.IsNotExist(err) {
		t.Fatalf("the temporary file stayed: %v", err)
	}
	// Once more: nothing to drop, nothing written.
	before, _ := os.Stat(p)
	if kept, err := keepCompanions(); err != nil || kept != nil {
		t.Fatalf("again: kept %v, err %v", kept, err)
	}
	if after, _ := os.Stat(p); !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("the file was written again")
	}
}

// Another opkg writes the file from what it read: the marks stay, and the
// file is not touched.
func TestKeepCompanionsLeavesTheFileToAnotherOpkg(t *testing.T) {
	p := companionStand(t, companionStatus)
	comm := filepath.Join(opkgProcDir, "4242", "comm")
	if err := os.MkdirAll(filepath.Dir(comm), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(comm, []byte("opkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := keepCompanions(); !errors.Is(err, errOpkgRunning) {
		t.Fatalf("err %v, want errOpkgRunning", err)
	}
	if got, _ := os.ReadFile(p); string(got) != companionStatus {
		t.Fatalf("status changed:\n%s", got)
	}
}

func TestKeepCompanionsWithoutAStatusFile(t *testing.T) {
	p := companionStand(t, "")
	os.Remove(p)
	if kept, err := keepCompanions(); err != nil || kept != nil {
		t.Fatalf("kept %v, err %v", kept, err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("a status file was made")
	}
}

// The self-update's opkg — update_controller's and Connect's automatic update
// alike — finds the marks gone already: opkg would remove a companion before
// anything after it runs.
func TestTheSelfUpdateDropsTheMarksBeforeOpkgRuns(t *testing.T) {
	p := companionStand(t, companionStatus)
	dir := t.TempDir()
	seen := filepath.Join(dir, "status-at-opkg")
	opkg := "#!/bin/sh\ncp \"$VCTL_TEST_STATUS\" \"$VCTL_TEST_SEEN\"\necho \"$@\" > \"$VCTL_TEST_SEEN.args\"\n"
	if err := os.WriteFile(filepath.Join(dir, "opkg"), []byte(opkg), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("VCTL_TEST_STATUS", p)
	t.Setenv("VCTL_TEST_SEEN", seen)
	if out, err := theDefaultControllerInstall(context.Background(), "/tmp/vectra-controller-pro-update.ipk"); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	args, _ := os.ReadFile(seen + ".args")
	if strings.TrimSpace(string(args)) != "install /tmp/vectra-controller-pro-update.ipk" {
		t.Fatalf("opkg %q", args)
	}
	got, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	if kept, _ := dropCompanionMarks(got); kept != nil {
		t.Fatalf("opkg ran with the marks of %v still there", kept)
	}
}
