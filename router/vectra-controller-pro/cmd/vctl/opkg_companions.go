package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"vectra-controller-pro/internal/logging"
)

// companionPackages are Vectra's packages that come with vctl from the pro
// feed, and that vctl's Depends names (openwrt/Makefile, EXTRA_DEPENDS). One
// that came in as vctl's dependency is "Auto-Installed: yes" in opkg's status
// file, and opkg removes it as an orphan the moment a vctl whose Depends does
// not name it replaces one whose did ("vectra-reporter was autoinstalled and
// is now orphaned, removing"): the reporter's postrm takes its spool and
// state, and xray loses the geo data it routes by. 0.7.0-r29 was published
// without them (test/pilot). The self-update installs that one package and
// nothing after it, so nothing would put them back.
//
// No opkg command takes the mark away: `opkg flag user` sets the package's
// state, and the orphan check reads only Auto-Installed (opkg 2024.10.16 on
// OpenWrt 24.10.8, tried). So the line goes from the status file itself
// before the self-update's opkg runs — as the installer does (install.sh,
// keep_companions) — and opkg keeps it so from then on.
var companionPackages = []string{"vectra-geodata", "vectra-reporter"}

// opkgStatusFile is opkg's record of what is installed; opkgProcDir is where
// another opkg is looked for. Tests point both elsewhere.
var (
	opkgStatusFile = "/usr/lib/opkg/status"
	opkgProcDir    = "/proc"
)

const autoInstalledMark = "Auto-Installed: yes"

// errOpkgRunning: another opkg runs, and it writes the status file from what
// it read — an edit now would be lost, or lose what it writes.
var errOpkgRunning = errors.New("another opkg is running")

// keepCompanions drops the companions' Auto-Installed marks from opkg's status
// file and says whose it dropped. Nothing is written when there is no mark or
// no file. The new file is written beside the old one, synced and renamed
// over it: a power cut leaves the one or the other.
func keepCompanions() ([]string, error) {
	old, err := os.ReadFile(opkgStatusFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	kept, out := dropCompanionMarks(old)
	if len(kept) == 0 {
		return nil, nil
	}
	if opkgRunning() {
		return nil, errOpkgRunning
	}
	fi, err := os.Stat(opkgStatusFile)
	if err != nil {
		return nil, err
	}
	tmp := opkgStatusFile + ".vctl"
	if err := writeSynced(tmp, out, fi.Mode().Perm()); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	if err := os.Rename(tmp, opkgStatusFile); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	if dir, err := os.Open(filepath.Dir(opkgStatusFile)); err == nil {
		dir.Sync()
		dir.Close()
	}
	return kept, nil
}

// dropCompanionMarks is the status file without the companions' Auto-Installed
// lines, and whose they were; every other byte as it was.
func dropCompanionMarks(status []byte) ([]string, []byte) {
	var kept []string
	var out bytes.Buffer
	pkg := ""
	for _, line := range bytes.SplitAfter(status, []byte("\n")) {
		s := strings.TrimRight(string(line), "\r\n")
		switch {
		case s == "":
			pkg = ""
		case strings.HasPrefix(s, "Package: "):
			pkg = strings.TrimPrefix(s, "Package: ")
		case s == autoInstalledMark && isCompanion(pkg):
			kept = append(kept, pkg)
			continue
		}
		out.Write(line)
	}
	return kept, out.Bytes()
}

func isCompanion(name string) bool {
	for _, c := range companionPackages {
		if name == c {
			return true
		}
	}
	return false
}

// opkgRunning: a process named opkg is alive.
func opkgRunning() bool {
	comms, _ := filepath.Glob(filepath.Join(opkgProcDir, "[0-9]*", "comm"))
	for _, c := range comms {
		if b, err := os.ReadFile(c); err == nil && strings.TrimSpace(string(b)) == "opkg" {
			return true
		}
	}
	return false
}

func writeSynced(path string, b []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// keepCompanionsForTheInstall runs keepCompanions before the self-update's
// opkg. Failing is no reason to keep the update back: a vctl that depends on
// the companions, as every one from 0.7.0-r30 does, removes nothing.
func keepCompanionsForTheInstall() {
	kept, err := keepCompanions()
	if err != nil {
		logging.L().Warn("the companions' Auto-Installed marks stay: a vctl that does not depend on them would remove them",
			"packages", strings.Join(companionPackages, " "), "err", err.Error())
		return
	}
	if len(kept) > 0 {
		logging.L().Info("the companions stay whatever a vctl's Depends says: Auto-Installed mark dropped", "packages", strings.Join(kept, " "))
	}
}
