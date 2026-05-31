package firewall

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CommitConfirmer applies a firewall ruleset behind a deadman timer: if the
// controller does not Confirm() within Timeout (because the new rules broke its
// own connectivity), a detached watchdog process automatically reverts, so a
// bad ruleset cannot strand the router beyond Timeout. This mirrors the
// "commit-confirmed" pattern from network gear and is the single most important
// safety net for remote firewall changes.
type CommitConfirmer struct {
	ConfirmPath string        // sentinel the deadman checks before reverting
	Timeout     time.Duration // grace period before auto-revert

	// Injectable for tests; default to the real OS.
	applyNFT func(script string) error
	runCmd   func(name string, args ...string) error
	spawn    func(script string) error
	writeFin func(path string) error
}

// NewCommitConfirmer returns a confirmer wired to the real OS.
func NewCommitConfirmer(confirmPath string, timeout time.Duration) *CommitConfirmer {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	return &CommitConfirmer{
		ConfirmPath: confirmPath,
		Timeout:     timeout,
		applyNFT:    applyNFTScript,
		runCmd:      runCommand,
		spawn:       spawnDetached,
		writeFin:    touchFile,
	}
}

// Apply arms the deadman, then applies the nft script + routing commands. If
// applying the rules fails, the deadman is left armed (it will revert) and the
// error is returned. After Apply returns nil, the caller MUST call Confirm()
// within Timeout once it has re-verified connectivity.
func (c *CommitConfirmer) Apply(script string, spec Spec) error {
	// Clear any stale confirmation so the freshly-armed deadman is authoritative.
	_ = os.Remove(c.ConfirmPath)

	deadman := BuildDeadmanScript(RevertCommands(spec), c.Timeout, c.ConfirmPath)
	if err := c.spawn(deadman); err != nil {
		return fmt.Errorf("arm commit-confirm deadman: %w", err)
	}
	if err := c.applyNFT(script); err != nil {
		return fmt.Errorf("apply nft ruleset: %w", err)
	}
	for _, cmd := range RoutingCommands(spec) {
		fields := strings.Fields(cmd)
		if len(fields) == 0 {
			continue
		}
		// Best-effort: ip rule/route may already exist; don't fail the whole
		// apply on a duplicate. The deadman still guards correctness.
		if err := c.runCmd(fields[0], fields[1:]...); err != nil {
			// log-level concern handled by caller; keep going.
			continue
		}
	}
	return nil
}

// Confirm cancels the pending auto-revert by writing the sentinel file. Call
// this only after re-verifying the control plane is reachable post-apply.
func (c *CommitConfirmer) Confirm() error {
	return c.writeFin(c.ConfirmPath)
}

// BuildDeadmanScript renders the detached watchdog shell. The revert commands
// are vctl's own constants (not external input). It reverts only if the
// confirm sentinel is absent after the grace period, then cleans up.
func BuildDeadmanScript(revert []string, timeout time.Duration, confirmPath string) string {
	secs := int(timeout.Seconds())
	if secs < 1 {
		secs = 1
	}
	var b strings.Builder
	fmt.Fprintf(&b, "sleep %d\n", secs)
	fmt.Fprintf(&b, "if [ ! -f %q ]; then\n", confirmPath)
	for _, cmd := range revert {
		fmt.Fprintf(&b, "  %s 2>/dev/null || true\n", cmd)
	}
	b.WriteString("  logger -t vctl 'firewall commit-confirm: auto-reverted (no confirmation)'\n")
	b.WriteString("fi\n")
	fmt.Fprintf(&b, "rm -f %q\n", confirmPath)
	return b.String()
}

// ---- real-OS implementations ---------------------------------------------

func applyNFTScript(script string) error {
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nft -f -: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func runCommand(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// spawnDetached runs the deadman in its own session so it survives the
// controller exiting/crashing — the whole point of the safety net.
func spawnDetached(script string) error {
	cmd := exec.Command("setsid", "sh", "-c", script)
	if err := cmd.Start(); err == nil {
		_ = cmd.Process.Release()
		return nil
	}
	// Fallback where setsid is unavailable: detached sh.
	cmd = exec.Command("sh", "-c", script)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func touchFile(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}
