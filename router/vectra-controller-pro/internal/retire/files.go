package retire

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/vault"
)

// Files in Env.MarkerDir: the record of the retirement (RetiredName) and the
// window's clock (ClockName). The init script knows both by these names.
const (
	RecordName = ".passwall-retired-by-vctl"
	ClockName  = ".passwall-retire-clock"
)

// What the takeover notes it owes PassWall2 (the init script's
// PASSWALL_MARKER and PASSWALL_SWITCH_MARKER): its rc.d link and its own
// switch.
var owedNames = []string{".passwall-disabled-by-vctl", ".passwall-switch-off-by-vctl"}

// BackupKeep is how many backups of PassWall2's configuration are kept.
const BackupKeep = 3

// Present: PassWall2 is on the router — its package installed, or an init
// script of its there.
func (e Env) Present() bool {
	for _, p := range e.PassWall {
		if p != "" && exists(p) {
			return true
		}
	}
	return e.InfoDir != "" && exists(filepath.Join(e.InfoDir, App+".control"))
}

// State is PassWall2 on this router, for the UI: "installed" while it is
// there, "retired" (and when) once vctl took it off, "absent" where it
// never was — or went some other way.
func (e Env) State() (string, time.Time) {
	if e.Present() {
		return "installed", time.Time{}
	}
	if r, ok := e.ReadRecord(); ok {
		return "retired", r.At
	}
	return "absent", time.Time{}
}

// The clock measures one of two windows, and says which: vctl carrying the
// traffic with PassWall2 on the router — the retirement's — or with
// PassWall2 gone, a person having removed it, and no record of a
// retirement — Tidy's; then its second line is clockAbsent. A clock counts
// only in the state it was started in, so a PassWall2 absent for the seconds
// of an opkg upgrade is never taken for one a person removed, and one put
// back never inherits the time it was away.
const clockAbsent = "absent"

// Since is when the window started, false when its clock does not run — or
// was started with PassWall2 in the other state.
func (e Env) Since() (time.Time, bool) {
	at, absent, ok := e.readClock()
	if !ok || absent == e.Present() {
		return time.Time{}, false
	}
	return at, true
}

// readClock reads the clock: when it started and whether it measures
// PassWall2's absence; false when there is none, or none to read.
func (e Env) readClock() (at time.Time, absent, ok bool) {
	if e.MarkerDir == "" {
		return time.Time{}, false, false
	}
	b, err := os.ReadFile(filepath.Join(e.MarkerDir, ClockName))
	if err != nil {
		return time.Time{}, false, false
	}
	line, rest, _ := strings.Cut(string(b), "\n")
	at, err = time.Parse(time.RFC3339, strings.TrimSpace(line))
	if err != nil {
		return time.Time{}, false, false
	}
	kind, _, _ := strings.Cut(rest, "\n")
	return at, strings.TrimSpace(kind) == clockAbsent, true
}

// StartClock starts the window at now, for PassWall2 as the router has it
// now, unless its clock runs already (Since); a clock started in the other
// state is started over. It returns when the window started.
func (e Env) StartClock(now time.Time) (time.Time, error) {
	if at, ok := e.Since(); ok {
		return at, nil
	}
	at := now.UTC().Truncate(time.Second)
	body := at.Format(time.RFC3339) + "\n"
	if !e.Present() {
		body += clockAbsent + "\n"
	}
	return at, localctl.WriteFileAtomic(filepath.Join(e.MarkerDir, ClockName), []byte(body), 0o644)
}

// DropStaleClock takes a clock started in the other state away — PassWall2
// was on the router then and is not now, or the other way round — so that
// it cannot come back to life when the state does. It says whether there was
// one.
func (e Env) DropStaleClock() (bool, error) {
	_, absent, ok := e.readClock()
	if !ok || absent != e.Present() {
		return false, nil
	}
	return true, e.stopClock()
}

func (e Env) stopClock() error {
	return removeIfThere(filepath.Join(e.MarkerDir, ClockName))
}

// Record is what the retirement did: when, what went, what of the list
// stayed, and where PassWall2's configuration was kept. ByHand: a person
// had removed PassWall2's packages, and Tidy finished the rest.
type Record struct {
	At      time.Time
	Removed []string
	Kept    []string
	Backup  string
	ByHand  bool
}

// ReadRecord reads the record, false when there is none.
func (e Env) ReadRecord() (Record, bool) {
	if e.MarkerDir == "" {
		return Record{}, false
	}
	b, err := os.ReadFile(filepath.Join(e.MarkerDir, RecordName))
	if err != nil {
		return Record{}, false
	}
	var r Record
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), "=")
		switch k {
		case "at":
			r.At, _ = time.Parse(time.RFC3339, v)
		case "removed":
			r.Removed = strings.Fields(v)
		case "kept":
			r.Kept = strings.Fields(v)
		case "backup":
			r.Backup = v
		case "by":
			r.ByHand = v == "hand"
		}
	}
	return r, true
}

// writeRecord writes it, key=value lines a person reads with cat.
func (e Env) writeRecord(r Record) error {
	body := fmt.Sprintf("at=%s\nremoved=%s\nkept=%s\nbackup=%s\n",
		r.At.UTC().Format(time.RFC3339), strings.Join(r.Removed, " "), strings.Join(r.Kept, " "), r.Backup)
	if r.ByHand {
		body += "by=hand\n"
	}
	return localctl.WriteFileAtomic(filepath.Join(e.MarkerDir, RecordName), []byte(body), 0o644)
}

func (e Env) dropRecord() error {
	return removeIfThere(filepath.Join(e.MarkerDir, RecordName))
}

// forget takes away what the takeover noted it owes PassWall2 — on /etc and
// a trial's on tmpfs — and the trial's snippet that turns its switch on at
// the next boot: with PassWall2 gone nothing is owed it. The old agent's
// breadcrumb is not PassWall2's: it stays.
func (e Env) forget() error {
	var errs []error
	for _, n := range owedNames {
		for _, dir := range []string{e.MarkerDir, e.TrialMarkers} {
			if dir != "" {
				errs = append(errs, removeIfThere(filepath.Join(dir, n)))
			}
		}
	}
	if e.Snippet != "" {
		errs = append(errs, removeIfThere(e.Snippet))
	}
	return errors.Join(errs...)
}

// Left: PassWall2 is gone with no record of a retirement — a person removed
// it — and left something behind: what the takeover noted it owes it, or its
// configuration.
func (e Env) Left() bool {
	if e.Present() {
		return false
	}
	if _, ok := e.ReadRecord(); ok {
		return false
	}
	if e.owes() {
		return true
	}
	for _, c := range e.Configs {
		if _, err := os.Lstat(c); err == nil {
			return true
		}
	}
	return false
}

// owes: a breadcrumb of what the takeover owes PassWall2 is still there.
func (e Env) owes() bool {
	for _, n := range owedNames {
		for _, dir := range []string{e.MarkerDir, e.TrialMarkers} {
			if dir != "" && exists(filepath.Join(dir, n)) {
				return true
			}
		}
	}
	return e.Snippet != "" && exists(e.Snippet)
}

// MaxArchiveBytes bounds the plaintext archive in memory on small routers.
const MaxArchiveBytes int64 = 32 << 20

// Backup preserves recovery in an authenticated encrypted archive. Older
// plaintext backups are sealed at vctl's start (SealPlaintextBackups); only
// encrypted generations are rotated.
func (e Env) Backup(now time.Time) (string, error) {
	var files []string
	var total int64
	for _, c := range e.Configs {
		st, err := os.Stat(c)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !st.Mode().IsRegular() {
			return "", fmt.Errorf("configuration is not a regular file")
		}
		total += st.Size()
		if total > MaxArchiveBytes {
			return "", fmt.Errorf("configuration archive exceeds size limit")
		}
		files = append(files, c)
	}
	if len(files) == 0 {
		return "", nil
	}
	if err := os.MkdirAll(e.BackupDir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(e.BackupDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(e.BackupDir, fmt.Sprintf("passwall2-%d.tar.gz.vault", now.Unix()))
	var body bytes.Buffer
	defer func() { clear(body.Bytes()) }()
	gz := gzip.NewWriter(&body)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		if err := addFile(tw, f); err != nil {
			return "", fmt.Errorf("backing up configuration: %w", err)
		}
	}
	if err := tw.Close(); err != nil {
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	if int64(body.Len()) > MaxArchiveBytes+(1<<20) {
		return "", fmt.Errorf("configuration archive exceeds size limit")
	}
	if err := vault.WriteFile(path, body.Bytes()); err != nil {
		return "", err
	}
	e.rotate()
	return path, nil
}

// SealPlaintextBackups seals what an older vctl or a person left in the
// backup directory as plaintext: PassWall2's nodes and subscriptions. An old
// archive passwall2-*.tar.gz becomes passwall2-*.tar.gz.vault — the form
// `vctl restore-passwall` restores — once the sealed copy reads back byte for
// byte; any other file (a hand-made copy of a UCI file) is sealed in place.
// Nothing is lost: what was readable stays recoverable, with the key outside
// this directory.
func (e Env) SealPlaintextBackups() error {
	ents, err := os.ReadDir(e.BackupDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range ents {
		name := d.Name()
		path := filepath.Join(e.BackupDir, name)
		if !d.Type().IsRegular() || strings.HasSuffix(name, ".vault") || !vault.Unsealed(path) {
			continue
		}
		if !strings.HasPrefix(name, "passwall2-") || !strings.HasSuffix(name, ".tar.gz") {
			errs = append(errs, vault.MigrateFile(path, func([]byte) error { return nil }))
			continue
		}
		errs = append(errs, sealArchive(path))
	}
	return errors.Join(errs...)
}

func sealArchive(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	defer clear(raw)
	if int64(len(raw)) > MaxArchiveBytes+(1<<20) {
		return fmt.Errorf("configuration archive exceeds size limit")
	}
	sealed := path + ".vault"
	if err := vault.WriteFile(sealed, raw); err != nil {
		return err
	}
	back, err := vault.ReadFile(sealed)
	if err != nil {
		return err
	}
	same := bytes.Equal(back, raw)
	clear(back)
	if !same {
		return fmt.Errorf("sealed archive does not read back")
	}
	return os.Remove(path)
}

func addFile(tw *tar.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("not a regular file")
	}
	h := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     strings.TrimPrefix(filepath.ToSlash(path), "/"),
		Mode:     int64(st.Mode().Perm()),
		Size:     st.Size(),
		ModTime:  st.ModTime(),
	}
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

// rotate keeps the newest BackupKeep backups; nothing else in the directory
// is touched.
func (e Env) rotate() {
	ents, err := os.ReadDir(e.BackupDir)
	if err != nil {
		return
	}
	type backup struct {
		name  string
		stamp int64
	}
	var bs []backup
	for _, d := range ents {
		n := d.Name()
		s, ok := strings.CutPrefix(n, "passwall2-")
		if !ok || !strings.HasSuffix(s, ".tar.gz.vault") || d.IsDir() {
			continue
		}
		stamp, err := strconv.ParseInt(strings.TrimSuffix(s, ".tar.gz.vault"), 10, 64)
		if err != nil {
			continue
		}
		bs = append(bs, backup{n, stamp})
	}
	sort.Slice(bs, func(i, j int) bool { return bs[i].stamp > bs[j].stamp })
	for i := BackupKeep; i < len(bs); i++ {
		_ = os.Remove(filepath.Join(e.BackupDir, bs[i].name))
	}
}

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func removeIfThere(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
