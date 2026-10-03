package tune

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// vctl's own leftovers in RAM (/tmp is tmpfs, and so is /var/run).
//
// An update downloads its package into /tmp and used to leave it there:
// 4.4 MB of a 234 MB router's RAM, for good, on the test router. A vault
// write crash-left its temp file beside the render. They are looked for by
// their exact names only — a file of anyone else's is never vctl's to
// remove, whatever it is called.

// LeftoverAge: younger, a leftover may be a download or a write still going.
const LeftoverAge = 10 * time.Minute

// leftoverNames are the names, by directory, of vctl's own leftovers:
// update_controller's package, Connect's automatic update (os.CreateTemp
// puts digits for the *), the vault's temp file (vault.WriteFile) in vctl's
// run directory.
var leftoverNames = []struct {
	dir  func(Env) string
	glob string
}{
	{func(e Env) string { return e.TmpDir }, "vectra-controller-pro-update.ipk"},
	{func(e Env) string { return e.TmpDir }, "vectra-controller-pro-auto-*.ipk"},
	{func(e Env) string { return e.RunDir }, ".vault-tmp-*"},
}

// Leftover is one of vctl's leftovers found.
type Leftover struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// Leftovers are vctl's own leftovers as they are at now: a regular file of
// an exact known name, last written more than LeftoverAge ago and open in no
// process (/proc/*/fd). It only reads.
func Leftovers(env Env, now time.Time) []Leftover {
	var found []Leftover
	for _, n := range leftoverNames {
		dir := n.dir(env)
		if dir == "" {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(dir, n.glob))
		for _, m := range matches {
			st, err := os.Lstat(m)
			if err != nil || !st.Mode().IsRegular() || now.Sub(st.ModTime()) < LeftoverAge {
				continue
			}
			found = append(found, Leftover{Path: m, Bytes: st.Size()})
		}
	}
	if len(found) == 0 {
		return nil
	}
	open := openFiles(env.ProcDir)
	out := found[:0]
	for _, l := range found {
		if !open[l.Path] && !open[realPath(l.Path)] {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	if len(out) == 0 {
		return nil
	}
	return out
}

// RemoveLeftovers removes the leftovers Leftovers finds now, and says which
// went. It is what the tune's tmp_leftovers runs, and the daemon at its
// start whatever the tune's switch: an update's package is vctl's to clean
// up either way.
func RemoveLeftovers(env Env, now time.Time) ([]Leftover, error) {
	var removed []Leftover
	var errs []error
	for _, l := range Leftovers(env, now) {
		if err := os.Remove(l.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove %s: %w", l.Path, err))
			continue
		}
		removed = append(removed, l)
	}
	return removed, errors.Join(errs...)
}

func leftoverBytes(ls []Leftover) int64 {
	var n int64
	for _, l := range ls {
		n += l.Bytes
	}
	return n
}

// openFiles is every path some process holds open, by /proc/<pid>/fd.
func openFiles(procDir string) map[string]bool {
	out := map[string]bool{}
	fds, _ := filepath.Glob(filepath.Join(procDir, "[0-9]*", "fd", "*"))
	for _, fd := range fds {
		if target, err := os.Readlink(fd); err == nil {
			out[strings.TrimSuffix(target, " (deleted)")] = true
		}
	}
	return out
}

// realPath is path with its directory's symlinks resolved, as /proc shows
// an open file: /var/run is /tmp/run on OpenWrt.
func realPath(path string) string {
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return path
	}
	return filepath.Join(dir, filepath.Base(path))
}
