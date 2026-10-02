package main

import (
	"errors"
	"fmt"
	"os"

	"vectra-controller-pro/internal/vault"
)

func init() {
	register(command{name: "vault-restore", summary: "Full recovery, explicit: decrypt one sealed file into a NEW plaintext file (0600, never overwritten) — not a support export", run: cmdVaultRestore})
}

// cmdVaultRestore is the explicit full-recovery mode: it writes a secret out
// as plaintext, so it is a root operator's deliberate act on the router, and
// nothing support or export collects runs it. The sealed file is read at its
// own path (the path is authenticated); the output must not exist yet.
func cmdVaultRestore(args []string) error {
	fs := newFlagSet("vault-restore")
	in := fs.String("in", "", "sealed file, at its original path")
	out := fs.String("out", "", "new plaintext file to write (must not exist)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" || *out == "" || fs.NArg() != 0 {
		return errors.New("vault-restore -in <sealed file> -out <new plaintext file>")
	}
	return vaultRestore(*in, *out)
}

func vaultRestore(in, out string) error {
	plain, err := vault.ReadFile(in)
	if err != nil {
		return fmt.Errorf("open sealed file: %w", err)
	}
	defer clear(plain)
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(plain); err == nil {
		err = f.Sync()
	}
	if ce := f.Close(); err == nil {
		err = ce
	}
	if err != nil {
		_ = os.Remove(out)
		return err
	}
	fmt.Fprintf(os.Stderr, "restored %s to %s (plaintext, 0600): remove it when done\n", in, out)
	return nil
}
