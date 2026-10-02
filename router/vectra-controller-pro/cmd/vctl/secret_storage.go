package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/incident"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/logging"
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
	// The old agent's state is its own: a failure to seal it is not a reason
	// to refuse vctl's start (the dead-man would hand the router back to it),
	// but it is never silent: the log and the reporter's inbox say that the
	// old agent's credentials are still in plaintext.
	if c.LegacyStatePath != "" {
		if err := state.MigrateLegacy(c.LegacyStatePath, legacyReadsSealed()); err != nil {
			reportLegacyUnsealed(err)
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
	for _, p := range []string{c.ProviderConfigPath, passwallDocumentPath(c.ProviderConfigPath)} {
		if err := vault.MigrateFile(p, validateJSON); err != nil {
			return err
		}
	}
	// The render is derived — the next apply writes it again from the sealed
	// documents. One that cannot be sealed or opened (damaged, its tmpfs key
	// gone, plaintext put over it) is retired, never read, and never a reason
	// for the router to lose its controller.
	if err := vault.MigrateFile(c.XrayRenderPath, validateJSON); err != nil {
		if e := vault.RemoveFile(c.XrayRenderPath); e != nil {
			return err
		}
		logging.L().Warn("the installed render could not be sealed; retired, the next apply writes it again", "err", err.Error())
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

// legacyAgentVaultMarker is installed by the old agent from the release that
// reads files vctl sealed (its own key, /etc/vectra-controller.vault-keys).
var legacyAgentVaultMarker = "/usr/share/vectra-controller/vault-read-v1"

// legacyAgentControl is opkg's record of the old agent's package.
var legacyAgentControl = "/usr/lib/opkg/info/vectra-controller-agent.control"

// legacyReadsSealed: the old agent's identity mirror may be sealed — the old
// agent reads sealed files, or it is not installed at all. An old agent that
// reads only plaintext keeps its mirror: sealing it would leave a hand-back
// without credentials, and the old agent would mint a new identity.
func legacyReadsSealed() bool {
	if _, err := os.Stat(legacyAgentVaultMarker); err == nil {
		return true
	}
	_, err := os.Stat(legacyAgentControl)
	return os.IsNotExist(err)
}

// reportLegacyUnsealed says, without secrets, that the old agent's state was
// left as it was: on stdout (procd keeps it, unlike stderr) and in the
// reporter's inbox.
func reportLegacyUnsealed(err error) {
	logging.L().Warn("the old agent's state was left unsealed", "err", err.Error())
	reportSecretStorage("legacy_state_unsealed", "warning", "the old agent's credentials were left in plaintext", err)
}

// reportSecretStorage puts a secret-storage failure in the reporter's inbox:
// the reporter is a process of its own, so the operator hears of it even when
// vctl does not start. The vault's errors name paths and causes, never
// contents.
func reportSecretStorage(code, severity, title string, err error) {
	incident.NewRecorder(incident.Dir, time.Hour).Record(incident.Incident{
		Code: code, Severity: severity, Key: code, Title: title,
		At: time.Now(), Source: "vctl",
		Details: map[string]any{"error": err.Error()},
	})
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
