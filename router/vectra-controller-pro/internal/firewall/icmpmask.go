package firewall

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The kernel answers a host with ICMP «destination unreachable» at most once
// a second (burst 6, net.ipv4.icmp_ratemask bit 3): of 20 QUIC attempts a
// LAN device made at once, the refusal (Spec.RefuseQUIC) reached 5 and the
// other 15 waited out their QUIC timeout (1111, r24, 2026-10-18). While the
// table refuses QUIC, vctl takes that bit out of the mask — which also lifts
// the kernel's global cap for the type — and the table holds the router's own
// unreachables to the old rates instead, LAN devices exempt from the
// per-address one (CounterICMPOutHeld). The mask is the table's: whatever
// takes the table down puts the bit back — the daemon (RestoreICMPMask), the
// deadman and the package's teardown (RestoreICMPMaskShell). A reboot loses
// both the bit and the marker (tmpfs).

// icmpUnreachBit is ICMP_DEST_UNREACH (type 3) in net.ipv4.icmp_ratemask.
const icmpUnreachBit = 1 << 3

var (
	// ICMPRatemaskPath is the kernel's mask (the network namespace's own).
	ICMPRatemaskPath = "/proc/sys/net/ipv4/icmp_ratemask"
	// ICMPMaskMarker records that vctl took the bit out — the mask before,
	// for the record — so that only a bit vctl took is ever put back: a mask
	// the owner set without it is theirs.
	ICMPMaskMarker = "/var/run/vectra-controller-pro/icmp-ratemask"
)

// RelaxesICMP reports whether the script for s refuses QUIC — and so needs
// the bit out of the mask and holds the router's unreachables itself.
func RelaxesICMP(s Spec) bool {
	return s.RefuseQUIC && len(carriedPools(s.CarriedV4)) > 0
}

// FollowICMPMask brings the mask to what the loaded table needs: the bit out
// while it refuses QUIC (relax), back otherwise. It reports what it did
// ("" when nothing).
func FollowICMPMask(relax bool) (string, error) {
	if !relax {
		return RestoreICMPMask()
	}
	mask, err := readICMPMask()
	if err != nil {
		return "", err
	}
	if mask&icmpUnreachBit == 0 {
		return "", nil // out already: ours (the marker says) or the owner's
	}
	// The marker first: a bit taken out without it would never come back.
	if err := os.MkdirAll(filepath.Dir(ICMPMaskMarker), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(ICMPMaskMarker, []byte(strconv.Itoa(mask)+"\n"), 0o644); err != nil {
		return "", err
	}
	if err := writeICMPMask(mask &^ icmpUnreachBit); err != nil {
		_ = os.Remove(ICMPMaskMarker)
		return "", err
	}
	return fmt.Sprintf("icmp_ratemask %d -> %d", mask, mask&^icmpUnreachBit), nil
}

// RestoreICMPMask puts back the bit vctl took out, if it did; the owner's
// other bits stay as they are now.
func RestoreICMPMask() (string, error) {
	if _, err := os.Stat(ICMPMaskMarker); errors.Is(err, os.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	mask, err := readICMPMask()
	if err != nil {
		return "", err
	}
	did := ""
	if mask&icmpUnreachBit == 0 {
		if err := writeICMPMask(mask | icmpUnreachBit); err != nil {
			return "", err
		}
		did = fmt.Sprintf("icmp_ratemask %d -> %d", mask, mask|icmpUnreachBit)
	}
	if err := os.Remove(ICMPMaskMarker); err != nil && !errors.Is(err, os.ErrNotExist) {
		return did, err
	}
	return did, nil
}

// RestoreICMPMaskShell is RestoreICMPMask for a shell — the deadman, which
// must work with the daemon gone. One line, no failure.
func RestoreICMPMaskShell() string {
	return fmt.Sprintf(`[ -f %[1]q ] && { m=$(cat %[2]q 2>/dev/null) && [ -n "$m" ] && echo $((m | %[3]d)) > %[2]q; rm -f %[1]q; } || true`,
		ICMPMaskMarker, ICMPRatemaskPath, icmpUnreachBit)
}

func readICMPMask() (int, error) {
	raw, err := os.ReadFile(ICMPRatemaskPath)
	if err != nil {
		return 0, err
	}
	mask, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, fmt.Errorf("icmp_ratemask %q: %w", strings.TrimSpace(string(raw)), err)
	}
	return mask, nil
}

func writeICMPMask(mask int) error {
	return os.WriteFile(ICMPRatemaskPath, []byte(strconv.Itoa(mask)+"\n"), 0o644)
}
