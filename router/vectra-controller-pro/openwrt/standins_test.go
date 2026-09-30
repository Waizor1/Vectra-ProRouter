package openwrt

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"vectra-controller-pro/internal/uci"
)

// The test binary is its own stand-in for the router's uci and flock: linked
// into a test's bin directory under that name, it answers as the tool. No
// python, no host tools, so the same binary runs these tests on a
// developer's machine and inside an OpenWrt rootfs, whose /bin/sh is the
// router's busybox ash: `GOOS=linux GOARCH=arm64 go test -c`, then the binary
// run in openwrt/rootfs with this directory mounted (docs/CANARY.md).
func TestMain(m *testing.M) {
	switch filepath.Base(os.Args[0]) {
	case "uci":
		os.Exit(uciStandIn(os.Args[1:]))
	case "flock":
		os.Exit(flockStandIn(os.Args[1:]))
	case "jsonfilter":
		os.Exit(jsonfilterStandIn(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// standIn links the test binary into bin as name.
func standIn(t *testing.T, bin, name string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(bin, name))
	if err := os.Symlink(self, filepath.Join(bin, name)); err != nil {
		t.Fatal(err)
	}
}

// linkTool puts the router's own tool into bin when this machine has it —
// busybox's flock in an OpenWrt rootfs — else the stand-in.
func linkTool(t *testing.T, bin, name string) {
	t.Helper()
	if p, err := exec.LookPath(name); err == nil {
		_ = os.Remove(filepath.Join(bin, name))
		if err := os.Symlink(p, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
		return
	}
	standIn(t, bin, name)
}

// uciStandIn is uci over one of two stores: UCI_STATE, a JSON file of
// sections per config (every call recorded in STUB_LOG), or UCI_CONFIG_DIR,
// real UCI files, with a set staged in UCI_SAVE_DIR — as uci stages it in
// /tmp/.uci — until a commit writes the file (only what changes a config is
// recorded). UCI_COMMIT_FAIL=1 makes a commit fail and keep what is staged.
func uciStandIn(argv []string) int {
	if os.Getenv("UCI_STATE") != "" {
		record("uci " + strings.Join(argv, " "))
		return uciJSON(options(argv))
	}
	args := options(argv)
	if len(args) == 0 {
		return 1
	}
	switch args[0] {
	case "set", "commit", "add_list", "del_list", "delete":
		record("uci " + strings.Join(argv, " "))
	}
	return uciFiles(args)
}

func options(argv []string) []string {
	for len(argv) > 0 && strings.HasPrefix(argv[0], "-") {
		argv = argv[1:]
	}
	return argv
}

func record(line string) {
	if p := os.Getenv("STUB_LOG"); p != "" {
		if f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(f, line)
			f.Close()
		}
	}
}

type jsonSection struct {
	Name  string              `json:"name"`
	Type  string              `json:"type"`
	Opts  map[string]string   `json:"opts"`
	Lists map[string][]string `json:"lists"`
}

func uciJSON(args []string) int {
	path := os.Getenv("UCI_STATE")
	raw, err := os.ReadFile(path)
	if err != nil {
		return 1
	}
	st := map[string][]*jsonSection{}
	if json.Unmarshal(raw, &st) != nil {
		return 1
	}
	for _, secs := range st {
		for _, s := range secs {
			if s.Opts == nil {
				s.Opts = map[string]string{}
			}
			if s.Lists == nil {
				s.Lists = map[string][]string{}
			}
		}
	}
	find := func(pkg, name string) *jsonSection {
		for _, s := range st[pkg] {
			if s.Name == name {
				return s
			}
		}
		return nil
	}
	save := func() int {
		b, _ := json.Marshal(st)
		if os.WriteFile(path, b, 0o644) != nil {
			return 1
		}
		return 0
	}
	if len(args) == 0 {
		return 1
	}
	switch args[0] {
	case "commit":
		return 0
	case "show":
		if len(args) < 2 {
			return 1
		}
		for _, s := range st[args[1]] {
			fmt.Printf("%s.%s=%s\n", args[1], s.Name, s.Type)
			for _, k := range sortedKeys(s.Opts) {
				fmt.Printf("%s.%s.%s='%s'\n", args[1], s.Name, k, s.Opts[k])
			}
			for _, k := range sortedKeys(s.Lists) {
				if vs := s.Lists[k]; len(vs) > 0 {
					q := make([]string, len(vs))
					for i, v := range vs {
						q[i] = "'" + v + "'"
					}
					fmt.Printf("%s.%s.%s=%s\n", args[1], s.Name, k, strings.Join(q, " "))
				}
			}
		}
		return 0
	}
	if len(args) < 2 {
		return 1
	}
	key, val, _ := strings.Cut(args[1], "=")
	parts := strings.Split(key, ".")
	var s *jsonSection
	if len(parts) > 1 {
		s = find(parts[0], parts[1])
	}
	switch {
	case args[0] == "get":
		switch {
		case s == nil:
			return 1
		case len(parts) == 2:
			fmt.Println(s.Type)
		case hasKey(s.Opts, parts[2]):
			fmt.Println(s.Opts[parts[2]])
		case len(s.Lists[parts[2]]) > 0:
			fmt.Println(strings.Join(s.Lists[parts[2]], " "))
		default:
			return 1
		}
		return 0
	case args[0] == "set" && len(parts) == 2:
		if s == nil {
			st[parts[0]] = append(st[parts[0]], &jsonSection{Name: parts[1], Type: val, Opts: map[string]string{}, Lists: map[string][]string{}})
		} else {
			s.Type = val
		}
		return save()
	case s == nil || len(parts) < 3:
		return 1
	case args[0] == "set":
		s.Opts[parts[2]] = val
	case args[0] == "add_list":
		s.Lists[parts[2]] = append(s.Lists[parts[2]], val)
	case args[0] == "del_list":
		vs, i := s.Lists[parts[2]], -1
		for j, v := range vs {
			if v == val {
				i = j
				break
			}
		}
		if i < 0 {
			return 1
		}
		s.Lists[parts[2]] = append(vs[:i:i], vs[i+1:]...)
	default:
		return 1
	}
	return save()
}

func hasKey(m map[string]string, k string) bool { _, ok := m[k]; return ok }

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// A staged change: section reference (a name or @type[i]), option, value.
type change struct{ Ref, Opt, Val string }

var anonRef = regexp.MustCompile(`^@([A-Za-z0-9_]+)\[(-?[0-9]+)\]$`)

// fileSection is a section of a UCI file, in order, as uci keeps it.
type fileSection struct {
	Type, Name string
	Opts       [][3]string // kind (option, list), name, value
}

func uciFiles(args []string) int {
	dir, save := os.Getenv("UCI_CONFIG_DIR"), os.Getenv("UCI_SAVE_DIR")
	staged := func(pkg string) []change {
		var ch []change
		raw, err := os.ReadFile(filepath.Join(save, pkg))
		if err == nil {
			_ = json.Unmarshal(raw, &ch)
		}
		return ch
	}
	load := func(pkg string) ([]*fileSection, error) {
		raw, err := os.ReadFile(filepath.Join(dir, pkg))
		if err != nil {
			return nil, err
		}
		stmts, err := uci.Statements(string(raw))
		if err != nil {
			return nil, err
		}
		var secs []*fileSection
		for _, w := range stmts {
			switch {
			case w[0] == "package":
			case w[0] == "config":
				s := &fileSection{Type: w[1]}
				if len(w) > 2 {
					s.Name = w[2]
				}
				secs = append(secs, s)
			case len(secs) > 0 && len(w) == 3:
				secs[len(secs)-1].Opts = append(secs[len(secs)-1].Opts, [3]string{w[0], w[1], w[2]})
			default:
				return nil, errors.New("bad statement")
			}
		}
		for _, c := range staged(pkg) {
			s := findFileSection(secs, c.Ref)
			if s == nil {
				continue
			}
			kept, done := s.Opts[:0:0], false
			for _, o := range s.Opts {
				if o[1] != c.Opt {
					kept = append(kept, o)
				} else if !done {
					kept, done = append(kept, [3]string{"option", c.Opt, c.Val}), true
				}
			}
			if !done {
				kept = append(kept, [3]string{"option", c.Opt, c.Val})
			}
			s.Opts = kept
		}
		return secs, nil
	}
	split := func(key string) (pkg, ref, opt string) {
		pkg, rest, _ := strings.Cut(key, ".")
		ref, opt, _ = strings.Cut(rest, ".")
		return
	}
	switch {
	case args[0] == "get" && len(args) == 2:
		pkg, ref, opt := split(args[1])
		secs, err := load(pkg)
		s := findFileSection(secs, ref)
		if err != nil || s == nil {
			return 1
		}
		if opt == "" {
			fmt.Println(s.Type)
			return 0
		}
		var vals []string
		for _, o := range s.Opts {
			if o[1] == opt {
				vals = append(vals, o[2])
			}
		}
		if len(vals) == 0 {
			return 1
		}
		fmt.Println(strings.Join(vals, " "))
		return 0
	case args[0] == "set" && len(args) == 2:
		key, val, _ := strings.Cut(args[1], "=")
		pkg, ref, opt := split(key)
		secs, err := load(pkg)
		if err != nil || findFileSection(secs, ref) == nil {
			return 1
		}
		if opt == "" {
			// `set <config>.<name>=<type>` of a section there already: its type.
			return 0
		}
		if os.MkdirAll(save, 0o755) != nil {
			return 1
		}
		b, _ := json.Marshal(append(staged(pkg), change{ref, opt, val}))
		if os.WriteFile(filepath.Join(save, pkg), b, 0o644) != nil {
			return 1
		}
		return 0
	case args[0] == "commit":
		if os.Getenv("UCI_COMMIT_FAIL") == "1" {
			return 1
		}
		pkgs := args[1:]
		if len(pkgs) == 0 {
			ents, _ := os.ReadDir(save)
			for _, e := range ents {
				pkgs = append(pkgs, e.Name())
			}
		}
		for _, pkg := range pkgs {
			secs, err := load(pkg)
			if err != nil {
				return 1
			}
			var b strings.Builder
			for _, s := range secs {
				b.WriteString("\nconfig " + s.Type)
				if s.Name != "" {
					b.WriteString(" '" + s.Name + "'")
				}
				b.WriteString("\n")
				for _, o := range s.Opts {
					fmt.Fprintf(&b, "\t%s %s '%s'\n", o[0], o[1], o[2])
				}
			}
			b.WriteString("\n")
			if os.WriteFile(filepath.Join(dir, pkg), []byte(b.String()), 0o644) != nil {
				return 1
			}
			_ = os.Remove(filepath.Join(save, pkg))
		}
		return 0
	}
	return 1
}

func findFileSection(secs []*fileSection, ref string) *fileSection {
	if m := anonRef.FindStringSubmatch(ref); m != nil {
		var typed []*fileSection
		for _, s := range secs {
			if s.Type == m[1] {
				typed = append(typed, s)
			}
		}
		i, _ := strconv.Atoi(m[2])
		if i < 0 {
			i += len(typed)
		}
		if i < 0 || i >= len(typed) {
			return nil
		}
		return typed[i]
	}
	for _, s := range secs {
		if s.Name == ref {
			return s
		}
	}
	return nil
}

// jsonfilterStandIn is OpenWrt's jsonfilter for what the teardown asks of
// it: `-i FILE -e @.a.b.c`, the value at that path, or exit 1 when there is
// none. It really walks the document, so a wrong path in the script fails.
func jsonfilterStandIn(argv []string) int {
	var file, expr string
	for i := 0; i+1 < len(argv); i++ {
		switch argv[i] {
		case "-i":
			file, i = argv[i+1], i+1
		case "-e":
			expr, i = argv[i+1], i+1
		}
	}
	f, err := os.Open(file)
	if err != nil {
		return 1
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.UseNumber()
	var cur interface{}
	if dec.Decode(&cur) != nil {
		return 1
	}
	for _, part := range strings.Split(strings.TrimLeft(expr, "@."), ".") {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return 1
		}
		if cur, ok = m[part]; !ok {
			return 1
		}
	}
	fmt.Println(cur)
	return 0
}

// flockStandIn is busybox's flock where this machine has none:
// `flock [-sxun] FD` locks a descriptor the shell holds open, `flock [-sxn]
// FILE [-c] PROG ARGS` runs PROG under a lock on FILE. -n: fail at once, 1,
// when it is held.
func flockStandIn(argv []string) int {
	how, nb := syscall.LOCK_EX, 0
	for len(argv) > 0 && strings.HasPrefix(argv[0], "-") && argv[0] != "-c" {
		for _, c := range argv[0][1:] {
			switch c {
			case 's':
				how = syscall.LOCK_SH
			case 'x':
				how = syscall.LOCK_EX
			case 'u':
				how = syscall.LOCK_UN
			case 'n':
				nb = syscall.LOCK_NB
			}
		}
		argv = argv[1:]
	}
	if len(argv) == 0 {
		return 1
	}
	if fd, err := strconv.Atoi(argv[0]); err == nil {
		if syscall.Flock(fd, how|nb) != nil {
			return 1
		}
		return 0
	}
	f, err := os.OpenFile(argv[0], os.O_RDONLY|os.O_CREATE, 0o666)
	if err != nil {
		return 1
	}
	defer f.Close()
	if syscall.Flock(int(f.Fd()), how|nb) != nil {
		return 1
	}
	prog := argv[1:]
	if len(prog) > 0 && prog[0] == "-c" {
		prog = append([]string{"/bin/sh", "-c"}, prog[1:]...)
	}
	if len(prog) == 0 {
		return 0
	}
	cmd := exec.Command(prog[0], prog[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		return 1
	}
	return 0
}
