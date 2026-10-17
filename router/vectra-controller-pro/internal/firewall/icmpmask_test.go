package firewall

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// icmpHost points the mask and its marker at a temporary directory holding
// the given mask.
func icmpHost(t *testing.T, mask string) (maskPath, marker string) {
	t.Helper()
	dir := t.TempDir()
	oldMask, oldMarker := ICMPRatemaskPath, ICMPMaskMarker
	t.Cleanup(func() { ICMPRatemaskPath, ICMPMaskMarker = oldMask, oldMarker })
	ICMPRatemaskPath = filepath.Join(dir, "icmp_ratemask")
	ICMPMaskMarker = filepath.Join(dir, "run", "icmp-ratemask")
	if err := os.WriteFile(ICMPRatemaskPath, []byte(mask+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return ICMPRatemaskPath, ICMPMaskMarker
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func TestFollowICMPMaskTakesTheBitAndPutsItBack(t *testing.T) {
	mask, marker := icmpHost(t, "6168")
	did, err := FollowICMPMask(true)
	if err != nil || did != "icmp_ratemask 6168 -> 6160" {
		t.Fatalf("relax = %q, %v", did, err)
	}
	if got := readFile(t, mask); got != "6160" {
		t.Fatalf("mask %s, want 6160", got)
	}
	if got := readFile(t, marker); got != "6168" {
		t.Fatalf("marker %q, want the mask before", got)
	}
	// Again: nothing to do.
	if did, err := FollowICMPMask(true); err != nil || did != "" {
		t.Fatalf("second relax = %q, %v", did, err)
	}
	// The table no longer refuses QUIC: the bit goes back.
	did, err = FollowICMPMask(false)
	if err != nil || did != "icmp_ratemask 6160 -> 6168" {
		t.Fatalf("restore = %q, %v", did, err)
	}
	if got := readFile(t, mask); got != "6168" {
		t.Fatalf("mask %s, want 6168", got)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("marker left: %v", err)
	}
}

// A mask the owner set without the bit is theirs: never put back.
func TestTheOwnersMaskStays(t *testing.T) {
	mask, marker := icmpHost(t, "6160")
	if did, err := FollowICMPMask(true); err != nil || did != "" {
		t.Fatalf("relax = %q, %v", did, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("a marker for a bit vctl did not take")
	}
	if did, err := RestoreICMPMask(); err != nil || did != "" {
		t.Fatalf("restore = %q, %v", did, err)
	}
	if got := readFile(t, mask); got != "6160" {
		t.Fatalf("mask %s, want the owner's 6160", got)
	}
}

// Only the bit goes back: what the owner changed meanwhile stays.
func TestRestorePutsBackOnlyTheBit(t *testing.T) {
	mask, _ := icmpHost(t, "6168")
	if _, err := FollowICMPMask(true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mask, []byte("6144\n"), 0o644); err != nil { // the owner took bit 4 too
		t.Fatal(err)
	}
	if did, err := RestoreICMPMask(); err != nil || did != "icmp_ratemask 6144 -> 6152" {
		t.Fatalf("restore = %q, %v", did, err)
	}
}

// No marker, no change of the mask: the bit would never come back.
func TestNoMarkerNoRelax(t *testing.T) {
	mask, marker := icmpHost(t, "6168")
	if err := os.WriteFile(filepath.Dir(marker), []byte("a file where the directory goes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := FollowICMPMask(true); err == nil {
		t.Fatal("relaxed without a marker")
	}
	if got := readFile(t, mask); got != "6168" {
		t.Fatalf("mask %s, want it untouched", got)
	}
}

// The shell's restore — the deadman's and the teardown's — does what
// RestoreICMPMask does.
func TestRestoreICMPMaskShell(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	mask, marker := icmpHost(t, "6168")
	if _, err := FollowICMPMask(true); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sh", "-c", RestoreICMPMaskShell()).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if got := readFile(t, mask); got != "6168" {
		t.Fatalf("mask %s, want 6168", got)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("marker left")
	}
	// A bit that cannot be put back keeps its marker.
	if _, err := FollowICMPMask(true); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(mask, 0o444); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sh", "-c", RestoreICMPMaskShell()).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if os.Geteuid() != 0 {
		if _, err := os.Stat(marker); err != nil {
			t.Fatal("the marker went though the mask was not written")
		}
	}
	if err := os.Chmod(mask, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	// Without the marker it changes nothing.
	if err := os.WriteFile(mask, []byte("6160\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sh", "-c", RestoreICMPMaskShell()).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if got := readFile(t, mask); got != "6160" {
		t.Fatalf("mask %s, want the owner's 6160", got)
	}
}

// The package's teardown, shell for when vctl is gone, puts the bit back
// from the same files as the daemon.
func TestTeardownPutsTheICMPMaskBack(t *testing.T) {
	raw, err := os.ReadFile("../../openwrt/files/usr/libexec/vectra-controller-pro/dataplane-teardown.sh")
	if err != nil {
		t.Fatal(err)
	}
	sh := string(raw)
	for _, want := range []string{"ICMP_MASK=" + ICMPRatemaskPath, "ICMP_MARKER=" + ICMPMaskMarker, `echo $((m | 8)) > "$ICMP_MASK" 2>/dev/null &&`} {
		if !strings.Contains(sh, want) {
			t.Errorf("teardown lacks %q", want)
		}
	}
	if strings.Index(sh, "ICMP_MARKER=") > strings.Index(sh, "no data plane of ours to unload") {
		t.Error("the mask goes back only when the table is still there; the deadman or a crash may have taken it")
	}
}

// The deadman puts the bit back before it takes the table down.
func TestDeadmanPutsTheICMPMaskBack(t *testing.T) {
	script := BuildDeadmanScriptFor([]string{"nft delete table inet vctl"}, 90*time.Second, "/tmp/confirm", "", "")
	r, d := strings.Index(script, RestoreICMPMaskShell()), strings.Index(script, "nft delete table inet vctl")
	if r < 0 || r > d {
		t.Fatalf("restore at %d, table delete at %d:\n%s", r, d, script)
	}
	if strings.Contains(BuildDeadmanScriptFor(nil, time.Second, "/tmp/c", "", ""), "icmp_ratemask") {
		t.Error("nothing to revert, yet the mask is touched")
	}
}
