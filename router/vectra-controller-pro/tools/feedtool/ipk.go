package main

// An .ipk as opkg reads it: a gzipped tar holding ./debian-binary ("2.0"),
// ./control.tar.gz and ./data.tar.gz — what OpenWrt's ipkg-build writes. The
// archives are deterministic (sorted entries, owner root, one fixed mtime, no
// gzip name or time), so the same payload always gives the same bytes and the
// same SHA256sum in the feed index.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// buildIpk packs dataDir and controlDir into out. The control file's
// Installed-Size is set from the payload, so it can never disagree with it.
func buildIpk(dataDir, controlDir, out string, mtime time.Time) error {
	ctrlPath := filepath.Join(controlDir, "control")
	ctrl, err := os.ReadFile(ctrlPath)
	if err != nil {
		return fmt.Errorf("control: %w", err)
	}
	fields, err := parseControl(ctrl)
	if err != nil {
		return fmt.Errorf("%s: %w", ctrlPath, err)
	}
	for _, f := range []string{"Package", "Version", "Architecture"} {
		if fields.get(f) == "" {
			return fmt.Errorf("%s: no %s field", ctrlPath, f)
		}
	}
	size, err := payloadSize(dataDir)
	if err != nil {
		return err
	}
	fields.set("Installed-Size", strconv.FormatInt(size, 10))

	data, err := tarGz(dataDir, mtime, nil)
	if err != nil {
		return fmt.Errorf("data.tar.gz: %w", err)
	}
	control, err := tarGz(controlDir, mtime, map[string][]byte{"control": fields.bytes()})
	if err != nil {
		return fmt.Errorf("control.tar.gz: %w", err)
	}

	var outer bytes.Buffer
	gz := newGzip(&outer)
	tw := tar.NewWriter(gz)
	for _, m := range []struct {
		name string
		body []byte
	}{{"./debian-binary", []byte("2.0\n")}, {"./data.tar.gz", data}, {"./control.tar.gz", control}} {
		if err := tw.WriteHeader(fileHeader(m.name, 0o644, int64(len(m.body)), mtime)); err != nil {
			return err
		}
		if _, err := tw.Write(m.body); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return os.WriteFile(out, outer.Bytes(), 0o644)
}

// payloadSize is what the files take once installed: regular files' bytes.
func payloadSize(dir string) (int64, error) {
	var n int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			fi, err := d.Info()
			if err != nil {
				return err
			}
			n += fi.Size()
		}
		return nil
	})
	return n, err
}

func newGzip(w io.Writer) *gzip.Writer {
	gz, _ := gzip.NewWriterLevel(w, gzip.BestCompression)
	gz.Header.OS = 3 // unix; no name, no mtime
	return gz
}

func fileHeader(name string, mode int64, size int64, mtime time.Time) *tar.Header {
	return &tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: mode, Size: size, ModTime: mtime, Uname: "root", Gname: "root", Format: tar.FormatGNU}
}

// tarGz archives dir as "./"-rooted entries in sorted order. override replaces
// the content of top-level files by name (the rewritten control file).
func tarGz(dir string, mtime time.Time, override map[string][]byte) ([]byte, error) {
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != dir {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)

	var buf bytes.Buffer
	gz := newGzip(&buf)
	tw := tar.NewWriter(gz)
	for _, p := range paths {
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil, err
		}
		name := "./" + filepath.ToSlash(rel)
		if strings.HasPrefix(filepath.Base(rel), "._") {
			return nil, fmt.Errorf("%s: an AppleDouble file would be installed on the router", name)
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return nil, err
		}
		perm := int64(fi.Mode().Perm())
		switch {
		case fi.IsDir():
			h := &tar.Header{Typeflag: tar.TypeDir, Name: name + "/", Mode: perm, ModTime: mtime, Uname: "root", Gname: "root", Format: tar.FormatGNU}
			if err := tw.WriteHeader(h); err != nil {
				return nil, err
			}
		case fi.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return nil, err
			}
			h := &tar.Header{Typeflag: tar.TypeSymlink, Name: name, Linkname: target, Mode: 0o777, ModTime: mtime, Uname: "root", Gname: "root", Format: tar.FormatGNU}
			if err := tw.WriteHeader(h); err != nil {
				return nil, err
			}
		case fi.Mode().IsRegular():
			body, ok := override[filepath.ToSlash(rel)]
			if !ok {
				if body, err = os.ReadFile(p); err != nil {
					return nil, err
				}
			}
			if err := tw.WriteHeader(fileHeader(name, perm, int64(len(body)), mtime)); err != nil {
				return nil, err
			}
			if _, err := tw.Write(body); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("%s: neither a file, a directory nor a symlink", name)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// readIpkControl returns the control file inside an .ipk (outer tar, inner
// control.tar.gz). Both the gzip-tar and the classic `ar` outer forms are
// accepted, as opkg does.
func readIpkControl(ipk []byte) ([]byte, error) {
	var ctrlTgz []byte
	if bytes.HasPrefix(ipk, []byte("!<arch>\n")) {
		var err error
		if ctrlTgz, err = arMember(ipk, "control.tar.gz"); err != nil {
			return nil, err
		}
	} else {
		var err error
		if ctrlTgz, err = tgzMember(ipk, "control.tar.gz"); err != nil {
			return nil, err
		}
	}
	return tgzMember(ctrlTgz, "control")
}

// tgzMember returns the named file ("x" or "./x") from a gzipped tar.
func tgzMember(tgz []byte, want string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s not found", want)
		}
		if err != nil {
			return nil, err
		}
		if strings.TrimPrefix(h.Name, "./") == want {
			return io.ReadAll(tr)
		}
	}
}

func arMember(ar []byte, want string) ([]byte, error) {
	r := ar[8:]
	for len(r) >= 60 {
		name := strings.TrimRight(strings.TrimSpace(string(r[:16])), "/")
		size, err := strconv.ParseInt(strings.TrimSpace(string(r[48:58])), 10, 64)
		if err != nil || size < 0 || int64(len(r)-60) < size {
			return nil, errors.New("a damaged ar archive")
		}
		body := r[60 : 60+size]
		if strings.TrimPrefix(name, "./") == want {
			return body, nil
		}
		r = r[60+size:]
		if size%2 == 1 && len(r) > 0 {
			r = r[1:]
		}
	}
	return nil, fmt.Errorf("%s not found", want)
}

func cmdIpk(args []string) error {
	fs := newFlags("ipk", "-data DIR -control DIR -out FILE [-mtime UNIX]")
	data := fs.String("data", "", "payload tree, as installed under /")
	control := fs.String("control", "", "control, conffiles and maintainer scripts")
	out := fs.String("out", "", ".ipk to write")
	mtime := fs.Int64("mtime", sourceDateEpoch(), "file times (default: $SOURCE_DATE_EPOCH, else 0)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *data == "" || *control == "" || *out == "" {
		return usageError(fs)
	}
	return buildIpk(*data, *control, *out, time.Unix(*mtime, 0).UTC())
}

func sourceDateEpoch() int64 {
	if v, err := strconv.ParseInt(os.Getenv("SOURCE_DATE_EPOCH"), 10, 64); err == nil && v > 0 {
		return v
	}
	return 0
}

func cmdControl(args []string) error {
	fs := newFlags("control", "FILE.ipk")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return usageError(fs)
	}
	raw, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	ctrl, err := readIpkControl(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", fs.Arg(0), err)
	}
	_, err = os.Stdout.Write(ctrl)
	return err
}
