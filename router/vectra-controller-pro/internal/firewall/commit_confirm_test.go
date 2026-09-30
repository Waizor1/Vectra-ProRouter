package firewall

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildDeadmanScript(t *testing.T) {
	script := BuildDeadmanScript([]string{"nft delete table inet vctl", "ip rule del fwmark 0x1 lookup 100"}, 90*time.Second, "/tmp/confirm")
	for _, want := range []string{"sleep 90", `if [ ! -f "/tmp/confirm" ]`, "nft delete table inet vctl", "ip rule del fwmark 0x1 lookup 100", `rm -f "/tmp/confirm"`} {
		if !strings.Contains(script, want) {
			t.Errorf("deadman script missing %q:\n%s", want, script)
		}
	}
}

func TestBuildDeadmanScriptMinimumSleep(t *testing.T) {
	if s := BuildDeadmanScript(nil, 0, "/tmp/c"); !strings.Contains(s, "sleep 1") {
		t.Errorf("expected minimum sleep 1, got:\n%s", s)
	}
}

func TestApplyArmsThenAppliesThenRoutes(t *testing.T) {
	dir := t.TempDir()
	confirm := filepath.Join(dir, "confirm")
	// Pre-create a stale confirm file; Apply must clear it before arming.
	if err := os.WriteFile(confirm, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}

	var order []string
	var spawnedScript, appliedScript string
	cc := &CommitConfirmer{
		ConfirmPath: confirm,
		Timeout:     30 * time.Second,
		applyNFT: func(s string) error {
			order = append(order, "apply")
			appliedScript = s
			return nil
		},
		runCmd: func(name string, args ...string) error {
			order = append(order, "route:"+name)
			return nil
		},
		spawn: func(s string) error {
			order = append(order, "spawn")
			spawnedScript = s
			return nil
		},
		writeFin: touchFile,
	}

	spec := DefaultSpec(12345, 1)
	if err := cc.Apply("table inet vctl {}", spec); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(confirm); !os.IsNotExist(err) {
		t.Errorf("stale confirm file should be removed before arming")
	}
	if len(order) < 2 || order[0] != "spawn" || order[1] != "apply" {
		t.Errorf("expected deadman armed BEFORE applying rules, order=%v", order)
	}
	if appliedScript != "table inet vctl {}" {
		t.Errorf("applied wrong script: %q", appliedScript)
	}
	if !strings.Contains(spawnedScript, "nft delete table inet vctl") {
		t.Errorf("deadman missing revert: %q", spawnedScript)
	}
}

func TestConfirmWritesSentinel(t *testing.T) {
	dir := t.TempDir()
	confirm := filepath.Join(dir, "confirm")
	cc := NewCommitConfirmer(confirm, 30*time.Second)
	if err := cc.Confirm(); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if _, err := os.Stat(confirm); err != nil {
		t.Errorf("confirm sentinel not written: %v", err)
	}
}

func TestApplyLeavesDeadmanArmedOnNFTFailure(t *testing.T) {
	dir := t.TempDir()
	cc := &CommitConfirmer{
		ConfirmPath: filepath.Join(dir, "confirm"),
		Timeout:     30 * time.Second,
		applyNFT:    func(string) error { return os.ErrPermission },
		runCmd:      func(string, ...string) error { return nil },
		spawn:       func(string) error { return nil },
		writeFin:    touchFile,
	}
	if err := cc.Apply("bad", DefaultSpec(1, 1)); err == nil {
		t.Fatal("expected error when nft apply fails")
	}
}

// A deadman whose apply was superseded stands down: it neither reverts nor
// removes the sentinel, which is the newer apply's confirmation by then. The
// latest one still reverts an unconfirmed ruleset.
func TestSupersededDeadmanStandsDown(t *testing.T) {
	dir := t.TempDir()
	confirm := filepath.Join(dir, "confirm")
	var scripts []string
	cc := &CommitConfirmer{
		ConfirmPath: confirm,
		Timeout:     time.Second,
		applyNFT:    func(string) error { return nil },
		runCmd:      func(string, ...string) error { return nil },
		spawn:       func(s string) error { scripts = append(scripts, s); return nil },
		writeFin:    touchFile,
	}
	spec := DefaultSpec(12345, 1)
	if err := cc.Apply("a", spec); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := cc.Apply("b", spec); err != nil {
		t.Fatal(err)
	}
	if err := cc.Confirm(); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "reverted")
	run := func(script string) {
		// The revert commands are replaced by a marker the test can see.
		s := strings.ReplaceAll(script, "nft flush table inet vctl", "touch "+marker)
		s = strings.ReplaceAll(s, "sleep 1\n", "")
		if out, err := exec.Command("sh", "-c", s).CombinedOutput(); err != nil {
			t.Fatalf("deadman: %v %s", err, out)
		}
	}
	run(scripts[0]) // the first apply's deadman wakes: superseded
	if _, err := os.Stat(confirm); err != nil {
		t.Fatal("the superseded deadman removed the newer apply's confirmation")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the superseded deadman reverted")
	}
	run(scripts[1]) // the latest: confirmed, no revert, cleans up
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("reverted a confirmed ruleset")
	}
	if _, err := os.Stat(confirm); err == nil {
		t.Fatal("the latest deadman did not clean up the sentinel")
	}
	// Unconfirmed: the latest deadman reverts.
	if err := cc.Apply("c", spec); err != nil {
		t.Fatal(err)
	}
	run(scripts[2])
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("an unconfirmed latest apply was not reverted")
	}
}
