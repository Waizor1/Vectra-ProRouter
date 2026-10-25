package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

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
// OpenWrt 24.10.8, tried). So the line goes from the status file itself, as
// the installer does it (install.sh, keep_companions): before the
// self-update's opkg, and after it — that opkg marks a companion again when
// it installs one as vctl's dependency (one a router lacked, or one whose
// minimum the new vctl raised), and a later vctl without them, the published
// r29 put back by hand included, would take it.
var companionPackages = []string{"vectra-geodata", "vectra-reporter"}

// opkgStatusFile is opkg's record of what is installed, opkgLockFile the lock
// every opkg takes (lockf) before it reads it, and opkgProcDir is where
// another opkg is looked for. Tests point them elsewhere.
var (
	opkgStatusFile = "/usr/lib/opkg/status"
	opkgLockFile   = "/var/lock/opkg.lock"
	opkgProcDir    = "/proc"
)

const autoInstalledMark = "Auto-Installed: yes"

// errOpkgRunning: another opkg runs, and it writes the status file from what
// it read — an edit now would be lost, or lose what it writes.
var errOpkgRunning = errors.New("another opkg is running")

// keepCompanions drops the companions' Auto-Installed marks from opkg's status
// file and says whose it dropped. Nothing is written when there is no mark or
// no file. It holds opkg's own lock from the read to the rename, so no opkg
// starts in between; the new file is written beside the old one, synced and
// renamed over it: a power cut leaves the one or the other.
func keepCompanions() ([]string, error) {
	if opkgRunning() {
		return nil, errOpkgRunning
	}
	unlock, err := lockOpkg()
	if err != nil {
		return nil, err
	}
	defer unlock()
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

// lockOpkg takes the lock opkg takes (an fcntl write lock on the whole file,
// as lockf(F_TLOCK) is): an opkg that holds it is errOpkgRunning, and one that
// starts while we hold it stops at "Could not lock". Without the directory
// (no opkg could run either) the process check above is all there is.
func lockOpkg() (func(), error) {
	f, err := os.OpenFile(opkgLockFile, os.O_RDWR|os.O_CREATE, 0o644)
	if errors.Is(err, os.ErrNotExist) {
		return func() {}, nil
	}
	if err != nil {
		return nil, err
	}
	lk := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0}
	if err := syscall.FcntlFlock(f.Fd(), syscall.F_SETLK, &lk); err != nil {
		f.Close()
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EACCES) {
			return nil, errOpkgRunning
		}
		return nil, err
	}
	return func() { f.Close() }, nil
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

// companionsWait is how long keepCompanionsAroundTheInstall waits for another
// opkg to finish, a second at a time; tests shorten it.
var companionsWait = 10 * time.Second

// keepCompanionsAroundTheInstall runs keepCompanions before the self-update's
// opkg and after it (<when>), waiting out another opkg for a few seconds.
// Failing is no reason to keep the update back: a vctl that depends on the
// companions, as every one from 0.7.0-r30 does, removes nothing.
func keepCompanionsAroundTheInstall(when string) {
	kept, err := keepCompanions()
	for waited := time.Duration(0); errors.Is(err, errOpkgRunning) && waited < companionsWait; waited += time.Second {
		time.Sleep(time.Second)
		kept, err = keepCompanions()
	}
	if err != nil {
		logging.L().Warn("the companions' Auto-Installed marks stay: a vctl that does not depend on them would remove them",
			"packages", strings.Join(companionPackages, " "), "when", when, "err", err.Error())
		return
	}
	if len(kept) > 0 {
		logging.L().Info("the companions stay whatever a vctl's Depends says: Auto-Installed mark dropped", "packages", strings.Join(kept, " "), "when", when)
	}
}
