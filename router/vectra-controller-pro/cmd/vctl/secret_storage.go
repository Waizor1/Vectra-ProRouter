package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/vault"
)

// migrateSecrets is the only automatic plaintext import. A sealed marker at
// each managed path permanently closes that path to a plaintext downgrade.
// Conversion is restartable; all network/identity work follows this gate.
func migrateSecrets(c agentcfg.Config) error {
	if err := state.Migrate(c.StatePath); err != nil {
		return err
	}
	if c.LegacyStatePath != "" {
		if err := state.Migrate(c.LegacyStatePath); err != nil {
			return err
		}
	}
	validateJSON := func(b []byte) error {
		if !json.Valid(b) {
			return errors.New("invalid legacy JSON")
		}
		return nil
	}
	operator := func(b []byte) error { _, err := config.Read(bytes.NewReader(b), "legacy operator"); return err }
	if err := vault.MigrateFile(c.XrayConfigPath, operator); err != nil {
		return err
	}
	for _, p := range []string{c.ProviderConfigPath, passwallDocumentPath(c.ProviderConfigPath), c.XrayRenderPath} {
		if err := vault.MigrateFile(p, validateJSON); err != nil {
			return err
		}
	}
	if err := localctl.MigrateEntries(c.EntriesPath); err != nil {
		return err
	}
	// Crash-left atomic temp files can contain incomplete secret input. Seal
	// rather than deleting them: recovery custody remains explicit.
	for _, base := range []string{c.XrayConfigPath, c.ProviderConfigPath, passwallDocumentPath(c.ProviderConfigPath), c.XrayRenderPath} {
		if err := vault.MigrateArtifact(base + ".tmp"); err != nil {
			return err
		}
	}
	return nil
}

func init() {
	register(command{name: "teardown-mark", summary: "Print the public TPROXY teardown mark only", run: cmdTeardownMark})
}
func cmdTeardownMark(args []string) error {
	fs := newFlagSet("teardown-mark")
	path := fs.String("config", "/etc/vectra-controller-pro/xray-desired.json", "encrypted operator configuration")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := config.LoadSecret(*path)
	if err != nil {
		return errors.New("teardown mark unavailable")
	}
	mark := uint32(1)
	if c.Inbounds.Tproxy != nil && c.Inbounds.Tproxy.FwMark != 0 {
		mark = uint32(c.Inbounds.Tproxy.FwMark)
	}
	fmt.Println(mark)
	return nil
}
