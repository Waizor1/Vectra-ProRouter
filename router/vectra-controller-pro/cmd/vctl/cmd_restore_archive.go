package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"vectra-controller-pro/internal/retire"
	"vectra-controller-pro/internal/vault"
)

func init() {
	register(command{name: "restore-passwall", summary: "Restore an encrypted PassWall retirement archive (after vectra off)", run: cmdRestorePasswall})
}

func cmdRestorePasswall(args []string) error {
	fs := newFlagSet("restore-passwall")
	backup := fs.String("backup", "", "encrypted retirement archive at its original path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *backup == "" || fs.NArg() != 0 {
		return fmt.Errorf("provide -backup encrypted archive path")
	}
	return restorePasswallArchive(*backup, "/")
}

// Validate the complete authenticated archive before invoking tar. Restoration
// is an explicit operator recovery action: it writes the original UCI files.
func restorePasswallArchive(backup, root string) error {
	if !strings.HasSuffix(backup, ".tar.gz.vault") {
		return fmt.Errorf("encrypted archive required; legacy tar recovery is explicit")
	}
	st, err := os.Lstat(backup)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() > retire.MaxArchiveBytes+(2<<20) {
		return fmt.Errorf("unsafe or oversized archive")
	}
	raw, err := vault.ReadFile(backup)
	if err != nil {
		return fmt.Errorf("open recovery archive: %w", err)
	}
	defer clear(raw)
	names, err := validatePasswallArchive(raw)
	if err != nil {
		return err
	}
	for _, name := range names {
		target := filepath.Join(root, filepath.FromSlash(name))
		if st, e := os.Lstat(target); e == nil {
			if !st.Mode().IsRegular() {
				return fmt.Errorf("unsafe recovery target")
			}
			if info, ok := st.Sys().(*syscall.Stat_t); ok && info.Nlink > 1 {
				return fmt.Errorf("hard-linked recovery target refused")
			}
		} else if !os.IsNotExist(e) {
			return e
		}
		// Existing symlink parents could redirect even a safe archive name.
		for p := filepath.Dir(filepath.Join(root, filepath.FromSlash(name))); ; p = filepath.Dir(p) {
			s, err := os.Lstat(p)
			if err != nil {
				return fmt.Errorf("recovery directory unavailable: %w", err)
			}
			if !s.IsDir() || s.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("unsafe recovery directory")
			}
			if p == filepath.Clean(root) {
				break
			}
			if p == filepath.Dir(p) {
				return fmt.Errorf("invalid recovery root")
			}
		}
	}
	cmd := exec.Command("tar", "-xzf", "-", "-C", root)
	cmd.Stdin = bytes.NewReader(raw)
	// Child errors may quote UCI content. Preserve the exit class only.
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("archive extraction failed: %w", err)
	}
	return nil
}

func validatePasswallArchive(raw []byte) ([]string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid recovery archive")
	}
	defer gz.Close()
	plain, err := io.ReadAll(io.LimitReader(gz, retire.MaxArchiveBytes+(1<<20)+1))
	if err != nil || int64(len(plain)) > retire.MaxArchiveBytes+(1<<20) {
		return nil, fmt.Errorf("invalid or oversized recovery archive")
	}
	defer clear(plain)
	tr := tar.NewReader(bytes.NewReader(plain))
	var names []string
	seen := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid recovery archive entry")
		}
		if h.Typeflag != tar.TypeReg || (h.Name != "etc/config/passwall2" && h.Name != "etc/config/passwall2_server") || seen[h.Name] || h.Size < 0 || h.Size > retire.MaxArchiveBytes {
			return nil, fmt.Errorf("unsupported recovery archive entry")
		}
		seen[h.Name] = true
		names = append(names, h.Name)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("empty recovery archive")
	}
	return names, nil
}
