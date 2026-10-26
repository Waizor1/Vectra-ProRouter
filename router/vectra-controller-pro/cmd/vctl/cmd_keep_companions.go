package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// `vctl keep-companions [--wait <seconds>]`: the companions' Auto-Installed
// marks out of opkg's status file (opkg_companions.go), for the installer and
// Rollout-VctlFleet.py after their opkg — the one way the file is written
// besides opkg, under opkg's own lock, whoever asks. A shell has no fcntl
// lock to take: busybox's flock and OpenWrt's lock are flock(2), which opkg's
// lockf neither sees nor heeds.
func init() {
	register(command{name: "keep-companions", summary: "keep vectra-geodata and vectra-reporter whatever a vctl's Depends says (their Auto-Installed mark out of opkg's status)", run: cmdKeepCompanions})
}

const keepCompanionsUsage = `usage: vctl keep-companions [--wait <seconds>]
  Drops the "Auto-Installed: yes" line of vectra-geodata and vectra-reporter
  from /usr/lib/opkg/status, under opkg's own lock: opkg removes a package so
  marked as an orphan once the vctl that brought it in is replaced by one that
  does not depend on it. Nothing is written where nothing is marked. Exits 1,
  the status as it was, while another opkg runs past --wait (default 10).`

var keepCompanionsOut io.Writer = os.Stdout

func cmdKeepCompanions(args []string) error {
	fs := newFlagSet("keep-companions")
	fs.Usage = func() { fmt.Fprintln(os.Stderr, keepCompanionsUsage) }
	wait := fs.Int("wait", 10, "seconds to wait out another opkg")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 || *wait < 0 {
		return fmt.Errorf("unexpected %q (vctl keep-companions [--wait <seconds>])", strings.Join(args, " "))
	}
	kept, err := keepCompanionsWaiting(time.Duration(*wait)*time.Second, false)
	if err != nil {
		return fmt.Errorf("the marks of %s stay: %w", strings.Join(companionPackages, " "), err)
	}
	if len(kept) == 0 {
		fmt.Fprintln(keepCompanionsOut, "companions: none marked")
		return nil
	}
	fmt.Fprintf(keepCompanionsOut, "companions: kept %s (Auto-Installed mark dropped)\n", strings.Join(kept, " "))
	return nil
}
