// Synthetic developer-only fixture. Not shipped in the IPK.
package main

import (
	"os"
	"path/filepath"
	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/vault"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "nil" {
		// Before any file read: tests reverse's documented non-call-site limitation.
		_, _ = state.ImportLegacyIdentity(nil, "synthetic-unused-state.json")
		return
	}
	dir, err := os.MkdirTemp("", "vectra-diagnostic-synthetic-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "fake.json")
	if err := os.WriteFile(path, []byte(`{"synthetic":true}`), 0600); err != nil {
		panic(err)
	}
	// Validation runs before encryption/key creation, using synthetic data only.
	_ = vault.MigrateFile(path, func([]byte) error { panic("synthetic-call-site-diagnostic") })
}
