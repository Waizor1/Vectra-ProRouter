package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/opkgstatus"
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
// OpenWrt 24.10.8, tried). So the line goes from the status file itself:
// before the self-update's opkg, and after it — that opkg marks a companion
// again when it installs one as vctl's dependency (one a router lacked, or
// one whose minimum the new vctl raised), and a later vctl without them, the
// published r29 put back by hand included, would take it. And when vctl
// starts (keepCompanionsAtStart): an older vctl's self-update brought this
// one in and kept nothing. The installer and Rollout-VctlFleet.py have it
// done here too (`vctl keep-companions`): one lock, opkg's, for all three.
var companionPackages = []string{"vectra-geodata", "vectra-reporter"}

// opkgStatusFile is opkg's record of what is installed, opkgLockFile the lock
// every opkg takes (lockf) before it reads it and keeps until it has written
// it, and opkgProcDir is where another opkg is looked for. Tests point them
// elsewhere.
var (
	opkgStatusFile = opkgstatus.File
	opkgLockFile   = "/var/lock/opkg.lock"
	opkgProcDir    = "/proc"
)

const autoInstalledMark = "Auto-Installed: yes"

// errOpkgRunning: another opkg runs, or has just ended and not yet let go of
// its lock file — it writes the status file from what it read: an edit now
// would be lost, or lose what it writes. Tried again later.
var errOpkgRunning = errors.New("another opkg is running")

// opkgMu: one goroutine of this vctl at a time holds opkg's lock or runs
// opkg — the start's pass, the self-update's install, the CLI, the
// maintenance's queries (opkgQuery) and PassWall's retirement. An fcntl lock
// is the process's, not the descriptor's: a second one taken in this process
// is granted at once, and closing either descriptor lets go of both; and an
// opkg this vctl starts while it holds the lock stops at "Could not lock".
var opkgMu sync.Mutex

// afterTheLock runs once opkg's lock is granted, before it is checked to hold;
// afterTheScan between the look at the processes and the one at the path;
// beforeTheLastLook before the status file is read again and compared with
// what the edit was made from; beforeTheRename after that, just before the
// rename. Tests stand an opkg that ends, a writer that interferes, or an opkg
// that tries the lock, in.
var (
	afterTheLock      = func() {}
	afterTheScan      = func() {}
	beforeTheLastLook = func() {}
	beforeTheRename   = func() {}
)

// keepCompanions drops the companions' Auto-Installed marks from opkg's status
// file and says whose it dropped. Nothing is written — and opkg's lock not
// taken — when there is no mark or no file. Otherwise it holds opkg's own lock
// from the read to the rename, so no opkg starts in between; the new file is
// written beside the old one, synced and renamed over it: a power cut leaves
// the one or the other. Every opkg is kept out from the read to the rename by
// its own lock. A writer that takes no lock (an older installer's) is not:
// the status is read once more just before the rename and must still be what
// the edit was made from, so a write of its up to that look is kept — one
// between that look and the rename is not.
func keepCompanions() ([]string, error) {
	opkgMu.Lock()
	defer opkgMu.Unlock()
	return keepCompanionsHeld()
}

func keepCompanionsHeld() ([]string, error) {
	if b, err := os.ReadFile(opkgStatusFile); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	} else if kept, _ := dropCompanionMarks(b); len(kept) == 0 {
		return nil, nil
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
	beforeTheLastLook()
	if now, err := os.ReadFile(opkgStatusFile); err != nil || !bytes.Equal(now, old) {
		os.Remove(tmp)
		return nil, errors.New("the status file changed while its marks were taken out; left as it is now")
	}
	beforeTheRename()
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

// lockOpkg takes the lock opkg takes, and gives its release. opkg (2024.10.16,
// libopkg/opkg_conf.c) creat()s the lock file, takes lockf(F_TLOCK) on it —
// an fcntl write lock on the whole file, as here — holds it from before its
// read of the status to after its write, and at its end lets go of it,
// closes it and only then unlinks it. So a lock granted here can be on a file
// a finishing opkg is about to unlink, and the next opkg creat()s a new one
// and locks that: both would hold "the" lock. Granted, it holds only once no
// opkg is alive — one that is has not unlinked yet, or will fail to lock —
// and the path is still the file locked here. Otherwise errOpkgRunning, and
// later again. An opkg that starts while it is held stops at "Could not
// lock" and unlinks nothing. Without the lock's directory, or without a look
// at the processes, nothing is held, and nothing written. The file stays:
// opkg creat()s it as it finds it.
func lockOpkg() (func(), error) {
	f, err := os.OpenFile(opkgLockFile, os.O_RDWR|os.O_CREATE, 0o640)
	if err != nil {
		return nil, fmt.Errorf("opkg's lock: %w", err)
	}
	lk := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0}
	if err := syscall.FcntlFlock(f.Fd(), syscall.F_SETLK, &lk); err != nil {
		f.Close()
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EACCES) {
			return nil, errOpkgRunning
		}
		return nil, fmt.Errorf("opkg's lock: %w", err)
	}
	afterTheLock()
	// In this order: a finishing opkg alive now has its unlink still ahead;
	// one gone has done it, and the path says so below.
	running, err := opkgRunning()
	if err != nil || running {
		f.Close()
		if err != nil {
			return nil, err
		}
		return nil, errOpkgRunning
	}
	afterTheScan()
	held, err1 := f.Stat()
	now, err2 := os.Stat(opkgLockFile)
	if err1 != nil || err2 != nil || !os.SameFile(held, now) {
		f.Close()
		return nil, errOpkgRunning
	}
	return func() { f.Close() }, nil
}

// opkgRunning: a process named opkg is alive. A process list without a
// process in it (this vctl at least) is no answer.
func opkgRunning() (bool, error) {
	comms, err := filepath.Glob(filepath.Join(opkgProcDir, "[0-9]*", "comm"))
	if err != nil || len(comms) == 0 {
		return false, fmt.Errorf("cannot tell whether an opkg runs: no processes in %s", opkgProcDir)
	}
	for _, c := range comms {
		if b, err := os.ReadFile(c); err == nil && strings.TrimSpace(string(b)) == "opkg" {
			return true, nil
		}
	}
	return false, nil
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

// keepCompanionsWaiting is keepCompanions, another opkg waited out for up to
// wait, a second at a time. held: the caller holds opkgMu.
func keepCompanionsWaiting(wait time.Duration, held bool) ([]string, error) {
	keep := keepCompanions
	if held {
		keep = keepCompanionsHeld
	}
	kept, err := keep()
	for waited := time.Duration(0); errors.Is(err, errOpkgRunning) && waited < wait; waited += time.Second {
		time.Sleep(time.Second)
		kept, err = keep()
	}
	return kept, err
}

// keepCompanionsAroundTheInstall runs keepCompanions before the self-update's
// opkg and after it (<when>), waiting out another opkg for a few seconds; the
// caller holds opkgMu. Failing is no reason to keep the update back: a vctl
// that depends on the companions, as every one from 0.7.0-r30 does, removes
// nothing.
func keepCompanionsAroundTheInstall(when string) {
	kept, err := keepCompanionsWaiting(companionsWait, true)
	if err != nil {
		logging.L().Warn("the companions' Auto-Installed marks stay: a vctl that does not depend on them would remove them",
			"packages", strings.Join(companionPackages, " "), "when", when, "err", err.Error())
		return
	}
	if len(kept) > 0 {
		logging.L().Info("the companions stay whatever a vctl's Depends says: Auto-Installed mark dropped", "packages", strings.Join(kept, " "), "when", when)
	}
}

// The start's pass waits out an opkg this long, a try every
// companionsStartRetry: the opkg of an older vctl's self-update that brought
// this one in has its xray and the postinst still to go (controllerInstallTime
// bounds it). Tests shorten both.
var (
	companionsStartWait  = 15 * time.Minute
	companionsStartRetry = 15 * time.Second
)

// keepCompanionsAtStart drops the marks an older vctl left: 0.7.0-r29's and
// earlier self-updates kept nothing after their opkg, so the companions the
// new vctl's opkg brought in as its dependencies stay marked — and go with any
// later vctl that does not name them, r29 put back by hand included. In the
// background, once; nothing is locked or written where nothing is marked.
// Not while an opkg runs: a postinst that starts this vctl runs inside it,
// and opkg writes the status — the marks with it — only at its end.
func (d *daemon) keepCompanionsAtStart(ctx context.Context) {
	deadline := time.Now().Add(companionsStartWait)
	for {
		var kept []string
		err := errOpkgRunning
		if running, scanErr := opkgRunning(); scanErr != nil || !running {
			kept, err = keepCompanions()
		}
		switch {
		case err == nil:
			if len(kept) > 0 {
				logging.L().Info("the companions stay whatever a vctl's Depends says: Auto-Installed mark dropped", "packages", strings.Join(kept, " "), "when", "at the start")
			}
			return
		case !errors.Is(err, errOpkgRunning) || !time.Now().Before(deadline):
			logging.L().Warn("the companions' Auto-Installed marks stay: a vctl that does not depend on them would remove them",
				"packages", strings.Join(companionPackages, " "), "when", "at the start", "err", err.Error())
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(companionsStartRetry):
		}
	}
}
