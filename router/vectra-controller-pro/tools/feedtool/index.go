package main

// The feed index: Packages (what opkg reads and usign signs) and Packages.gz
// (what opkg downloads), built the way OpenWrt's ipkg-make-index.sh does — each
// package's control file with Filename, Size and SHA256sum inserted before its
// Description, one blank line between packages.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// control keeps a control file's fields in order; a Description's continuation
// lines stay with it.
type control struct {
	names  []string
	values map[string]string
}

func parseControl(raw []byte) (*control, error) {
	c := &control{values: map[string]string{}}
	var last string
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if line == "" {
			return nil, errors.New("a blank line inside a control file")
		}
		if line[0] == ' ' || line[0] == '\t' {
			if last == "" {
				return nil, errors.New("a continuation line before any field")
			}
			c.values[last] += "\n" + line
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok || strings.ContainsAny(name, " \t") {
			return nil, fmt.Errorf("not a field: %q", line)
		}
		if _, dup := c.values[name]; dup {
			return nil, fmt.Errorf("field %s twice", name)
		}
		c.names = append(c.names, name)
		c.values[name] = strings.TrimLeft(value, " ")
		last = name
	}
	return c, nil
}

func (c *control) get(name string) string { return c.values[name] }

// set replaces a field in place, or adds it before Description (where
// opkg-build and the SDK put the fields they compute).
func (c *control) set(name, value string) {
	if _, ok := c.values[name]; !ok {
		at := len(c.names)
		for i, n := range c.names {
			if n == "Description" {
				at = i
				break
			}
		}
		c.names = append(c.names[:at], append([]string{name}, c.names[at:]...)...)
	}
	c.values[name] = value
}

func (c *control) bytes() []byte {
	var b strings.Builder
	for _, n := range c.names {
		b.WriteString(n + ": " + c.values[n] + "\n")
	}
	return []byte(b.String())
}

// buildIndex writes dir/Packages and dir/Packages.gz from the .ipk files in dir.
// With arch set, every package must be built for it (or for "all"): a feed
// directory is one architecture, and opkg would refuse a stray one anyway.
func buildIndex(dir, arch string) (int, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*.ipk"))
	if err != nil {
		return 0, err
	}
	sort.Strings(names)
	if len(names) == 0 {
		return 0, fmt.Errorf("%s: no .ipk files", dir)
	}
	seen := map[string]string{}
	var out bytes.Buffer
	for _, p := range names {
		raw, err := os.ReadFile(p)
		if err != nil {
			return 0, err
		}
		ctrlRaw, err := readIpkControl(raw)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", filepath.Base(p), err)
		}
		c, err := parseControl(ctrlRaw)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", filepath.Base(p), err)
		}
		pkg, ver, a := c.get("Package"), c.get("Version"), c.get("Architecture")
		if pkg == "" || ver == "" || a == "" {
			return 0, fmt.Errorf("%s: Package, Version and Architecture are required", filepath.Base(p))
		}
		if arch != "" && a != arch && a != "all" {
			return 0, fmt.Errorf("%s: built for %s, this feed is %s", filepath.Base(p), a, arch)
		}
		if prev, dup := seen[pkg]; dup {
			return 0, fmt.Errorf("%s: %s is already in the feed as %s — one version per package", filepath.Base(p), pkg, prev)
		}
		seen[pkg] = filepath.Base(p)
		sum := sha256.Sum256(raw)
		c.set("Filename", filepath.Base(p))
		c.set("Size", strconv.Itoa(len(raw)))
		c.set("SHA256sum", hex.EncodeToString(sum[:]))
		out.Write(c.bytes())
		out.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "Packages"), out.Bytes(), 0o644); err != nil {
		return 0, err
	}
	var gz bytes.Buffer
	w := newGzip(&gz)
	if _, err := w.Write(out.Bytes()); err != nil {
		return 0, err
	}
	if err := w.Close(); err != nil {
		return 0, err
	}
	return len(names), os.WriteFile(filepath.Join(dir, "Packages.gz"), gz.Bytes(), 0o644)
}

func cmdIndex(args []string) error {
	fs := newFlags("index", "[-arch ARCH] DIR")
	arch := fs.String("arch", "", "the feed's architecture; any other refuses the index")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return usageError(fs)
	}
	n, err := buildIndex(fs.Arg(0), *arch)
	if err != nil {
		return err
	}
	fmt.Printf("%d packages\n", n)
	return nil
}
