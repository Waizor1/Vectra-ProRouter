package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/vault"
)

// What support access (run_terminal_command) and log collection return goes
// to the panel's database: the router's credentials must not, its hashes
// must (support compares them).
func TestSupportOutputCarriesNoCredentials(t *testing.T) {
	d := &daemon{
		st:      state.PersistedState{AgentToken: "synthetic-panel-token-0123456789", DevicePrivateKey: "c3ludGhldGljL2RldmljZS9rZXkrMDEyMzQ1Njc4OQ=="},
		desired: &config.Config{Subscriptions: []config.Subscription{{URL: "https://sub.example.net/api/sub/synthetic-sub-token"}}},
	}
	hash := "a116354256d4f0ef9ab17f16368e95ac4b8f8b3fb88d0952fd89d516ed972fd7"
	in := strings.Join([]string{
		`{"router_id":"r","agent_token":"synthetic-panel-token-0123456789","device_private_key":"c3ludGhldGljL2RldmljZS9rZXkrMDEyMzQ1Njc4OQ=="}`,
		"token in a log line synthetic-panel-token-0123456789 and key c3ludGhldGljL2RldmljZS9rZXkrMDEyMzQ1Njc4OQ==",
		"fetch https://sub.example.net/api/sub/synthetic-sub-token failed",
		"vless://11111111-2222-3333-4444-555555555555@pl5.example.net:443?pbk=synthetic-pbk#node",
		"\toption password 'synthetic-node-pass'",
		"wireless.default_radio0.key='synthetic-wifi-key'",
		hash + "  /usr/sbin/vctl",
	}, "\n")
	out := d.redactSupport(in)
	for _, secret := range []string{"synthetic-panel-token", "c3ludGhldGljL2Rld", "synthetic-sub-token", "11111111-2222", "synthetic-pbk", "synthetic-node-pass", "synthetic-wifi-key"} {
		if strings.Contains(out, secret) {
			t.Fatalf("%q reached the support output:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, hash) || !strings.Contains(out, "/usr/sbin/vctl") {
		t.Fatalf("support lost what it compares:\n%s", out)
	}
}

// The old agent's identity mirror is sealed only when the installed old agent
// reads sealed files (its vault-read marker) or is not installed at all.
func TestTheLegacyMirrorIsSealedOnlyForAnAgentThatCanReadIt(t *testing.T) {
	dir := t.TempDir()
	oldMarker, oldControl := legacyAgentVaultMarker, legacyAgentControl
	t.Cleanup(func() { legacyAgentVaultMarker, legacyAgentControl = oldMarker, oldControl })
	legacyAgentVaultMarker = filepath.Join(dir, "vault-read-v1")
	legacyAgentControl = filepath.Join(dir, "vectra-controller-agent.control")
	if !legacyReadsSealed() {
		t.Fatal("no old agent installed: nothing reads the mirror")
	}
	_ = os.WriteFile(legacyAgentControl, []byte("Version: 0.1.13-r43\n"), 0o644)
	if legacyReadsSealed() {
		t.Fatal("sealed the mirror of an old agent that reads only plaintext")
	}
	_ = os.WriteFile(legacyAgentVaultMarker, []byte("v1\n"), 0o644)
	if !legacyReadsSealed() {
		t.Fatal("kept the mirror plaintext for an old agent that reads sealed files")
	}
}

// The old agent's files are never migrated under its running process, told
// by its executable: the init scripts and the watchdog share its name.
func TestTheOldAgentIsSeenRunning(t *testing.T) {
	dir := t.TempDir()
	old := legacyAgentProc
	t.Cleanup(func() { legacyAgentProc = old })
	legacyAgentProc = dir
	_ = os.MkdirAll(filepath.Join(dir, "12"), 0o755)
	_ = os.Symlink("/bin/busybox", filepath.Join(dir, "12", "exe")) // an init script named vectra-controll…
	if legacyAgentRunning() {
		t.Fatal("a script taken for the old agent")
	}
	_ = os.MkdirAll(filepath.Join(dir, "34"), 0o755)
	_ = os.Symlink(legacyAgentBinary, filepath.Join(dir, "34", "exe"))
	if !legacyAgentRunning() {
		t.Fatal("the old agent's process was not seen")
	}
}

// vault-restore is the explicit full-recovery mode: any sealed file back as a
// NEW plaintext file, 0600, never over an existing one.
func TestVaultRestoreWritesANewPlaintextFileOnly(t *testing.T) {
	dir := t.TempDir()
	sealed := filepath.Join(dir, "backup", "passwall2.before-native")
	_ = os.MkdirAll(filepath.Dir(sealed), 0o700)
	text := []byte("config nodes 'n'\n\toption password 'synthetic'\n")
	if err := vault.WriteFile(sealed, text); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "restored.uci")
	if err := vaultRestore(sealed, out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != string(text) {
		t.Fatal("restored content differs")
	}
	if st, _ := os.Stat(out); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
	if err := vaultRestore(sealed, out); err == nil {
		t.Fatal("overwrote an existing file")
	}
}

// At a hand-back an old agent that reads only plaintext gets its mirror back;
// one that reads sealed files leaves it sealed.
func TestHandbackGivesAPlaintextOnlyAgentItsMirror(t *testing.T) {
	dir := t.TempDir()
	oldMarker, oldControl := legacyAgentVaultMarker, legacyAgentControl
	t.Cleanup(func() { legacyAgentVaultMarker, legacyAgentControl = oldMarker, oldControl })
	legacyAgentVaultMarker = filepath.Join(dir, "vault-read-v1")
	legacyAgentControl = filepath.Join(dir, "agent.control")
	p := filepath.Join(dir, "legacy", "state.json")
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	if err := vault.WriteFile(p+".identity", []byte(`{"router_id":"r","agent_token":"synthetic-handback"}`)); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(legacyAgentControl, []byte("Version: x\n"), 0o644)
	_ = os.WriteFile(legacyAgentVaultMarker, []byte("v1\n"), 0o644)
	if err := legacyHandbackPrepare(p); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(p + ".identity"); strings.Contains(string(raw), "synthetic-handback") {
		t.Fatal("unsealed for an agent that reads sealed files")
	}
	_ = os.Remove(legacyAgentVaultMarker) // downgraded below the vault-read release
	if err := legacyHandbackPrepare(p); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(p + ".identity"); !strings.Contains(string(raw), "synthetic-handback") {
		t.Fatal("the plaintext-only agent cannot read its mirror")
	}
}
