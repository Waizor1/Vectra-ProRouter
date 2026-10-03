package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/vault"
)

func init() {
	register(command{name: "export-config", summary: "Export a key-free encrypted configuration snapshot (not full recovery)", run: cmdExportConfig})
}
func cmdExportConfig(args []string) error {
	fs := newFlagSet("export-config")
	cfgPath := fs.String("config", "/var/run/vectra-controller-pro/agent.json", "daemon path configuration")
	out := fs.String("out", "", "private encrypted snapshot archive")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" || fs.NArg() != 0 {
		return errors.New("provide -out for key-free snapshot")
	}
	c, err := agentcfg.Load(*cfgPath)
	if err != nil {
		return errors.New("export configuration unavailable")
	}
	return exportConfigSnapshot(c, *out)
}

// Exact allowlist: no keys, identity/state, tmp, runtime, logs, legacy configs,
// backups or filesystem recursion. A full sysupgrade recovery is separate.
func exportConfigSnapshot(c agentcfg.Config, out string) error {
	files := []struct{ name, path string }{{"xray-desired.vault", c.XrayConfigPath}, {"provider-config.vault", c.ProviderConfigPath}, {"provider-entries.vault", c.EntriesPath}}
	outAbs, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	var total int
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name string, b []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err := tw.Write(b)
		return err
	}
	for _, f := range files {
		a := outAbs
		keyPath, err := vault.KeyPath(f.path)
		if err != nil {
			return err
		}
		keyDir := filepath.Dir(keyPath)
		if a == keyDir || strings.HasPrefix(a, keyDir+string(filepath.Separator)) {
			return errors.New("export cannot replace vault key storage")
		}
		b, _ := filepath.Abs(f.path)
		if a == b {
			return errors.New("export cannot replace managed secret")
		}
		plain, err := vault.ReadFile(f.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return errors.New("export source integrity unavailable")
		}
		clear(plain)
		raw, err := os.ReadFile(f.path)
		if err != nil {
			return errors.New("export source unavailable")
		}
		total += len(raw)
		if total > 32<<20 || !bytes.HasPrefix(raw, []byte("VCTLVAULT1\n")) {
			return errors.New("export source is not encrypted")
		}
		if err := add(f.name, raw); err != nil {
			return err
		}
	}
	note, _ := json.Marshal(struct {
		Format    string `json:"format"`
		Recovery  bool   `json:"standaloneRecovery"`
		KeyPolicy string `json:"keyPolicy"`
	}{"vectra-key-free-config-v1", false, "Keys and device identity omitted. Full sysupgrade recovery remains privileged and sensitive."})
	if err := add("manifest.json", note); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := localctl.WriteFileAtomic(out, buf.Bytes(), 0600); err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	}
	return nil
}
