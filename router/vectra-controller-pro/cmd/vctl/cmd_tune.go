package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/power"
	"vectra-controller-pro/internal/tune"
)

// `vctl tune` is the router's tune (internal/tune): plan is a dry run, apply
// sets what is pending, undo puts back what the tune changed. The package's
// postinst runs `apply` once vctl is up, the daemon at every start
// (tuneAtStart), the prerm `undo` on a removal.
func init() {
	register(command{name: "tune", summary: "the router's tune: plan (dry run, default) | apply | undo [--json]", run: cmdTune})
}

// Seams: tests never read or change the router.
var (
	tuneEnv = tune.RouterEnv
	// tuneTrial: a trial runs (`vectra on --trial`); it changes nothing a
	// reboot would keep, so the tune waits for `vectra keep`.
	tuneTrial           = func() bool { return power.LoadTrial(power.RouterEnv()) != nil }
	tuneOut   io.Writer = os.Stdout
	// tuneStartDelay keeps the daemon's run out of its own start: xray's
	// config check and start are its memory peak.
	tuneStartDelay = 20 * time.Second
	tuneTrialPoll  = time.Minute
)

// tuneTimeout bounds one run: fw4's check and reload take seconds.
const tuneTimeout = 3 * time.Minute

func cmdTune(args []string) error {
	// Run by a person or from the package's scripts: the environment may be
	// bare, and uci, sysctl and the init scripts are found through PATH.
	if os.Getenv("PATH") == "" {
		_ = os.Setenv("PATH", "/usr/sbin:/usr/bin:/sbin:/bin")
	}
	verb := "plan"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		verb, args = args[0], args[1:]
	}
	fs := newFlagSet("tune " + verb)
	asJSON := fs.Bool("json", false, "print the router's plan as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	env := tuneEnv()
	ctx, cancel := context.WithTimeout(context.Background(), tuneTimeout)
	defer cancel()

	var res tune.Result
	var err error
	switch verb {
	case "plan":
		// The plan, and where the router's memory and flash go: read only.
		p, a := tune.Inspect(env), tune.Analyze(env)
		p.Analysis = &a
		return printTunePlan(tuneOut, p, *asJSON)
	case "apply":
		if tuneTrial() {
			fmt.Fprintln(tuneOut, "a trial runs (vectra on --trial): the router is tuned once it is kept (vectra keep)")
			return nil
		}
		res, err = tune.Apply(ctx, env)
	case "undo":
		res, err = tune.Undo(ctx, env)
	default:
		return fmt.Errorf("tune: plan, apply or undo, not %q", verb)
	}
	if err != nil {
		return err
	}
	if *asJSON {
		return printTunePlan(tuneOut, res.Plan, true)
	}
	for _, c := range res.Changes {
		fmt.Fprintln(tuneOut, "changed: "+c.String())
	}
	for _, f := range res.Failed {
		fmt.Fprintln(tuneOut, "failed: "+f.String())
	}
	if len(res.Changes) == 0 && len(res.Failed) == 0 {
		fmt.Fprintln(tuneOut, "nothing to change")
	}
	if len(res.Failed) > 0 {
		return fmt.Errorf("%d of the tune's changes did not go through", len(res.Failed))
	}
	return nil
}

func printTunePlan(w io.Writer, p tune.Plan, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(p)
	}
	on := "on"
	if !p.On {
		on = "off (vectra-controller-pro.main.tune '0')"
	}
	fmt.Fprintf(w, "profile %s: %d MiB of RAM, %d cores; the tune is %s\n", p.Profile, p.MemTotalMiB, p.Cores, on)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, it := range p.Items {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", it.ID, it.State, tuneDetail(it))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if a := p.Analysis; a != nil {
		printTuneAnalysis(w, a)
	}
	return nil
}

// printTuneAnalysis is the plan's analysis in a few lines: nothing in it is
// the tune's to change.
func printTuneAnalysis(w io.Writer, a *tune.Analysis) {
	fmt.Fprintf(w, "memory: %d MiB available; swap %d of %d MiB used\n", a.MemAvailableMiB, a.SwapUsedMiB, a.SwapTotalMiB)
	if len(a.TopRSS) > 0 {
		top := make([]string, 0, len(a.TopRSS))
		for _, p := range a.TopRSS {
			top = append(top, fmt.Sprintf("%s %.1f MiB", p.Name, p.RSSMiB))
		}
		fmt.Fprintln(w, "  most RAM: "+strings.Join(top, ", "))
	}
	if a.OverlayFreeMiB != nil {
		fmt.Fprintf(w, "flash (overlay): %d MiB free\n", *a.OverlayFreeMiB)
	}
	if len(a.Reclaimable) > 0 {
		fmt.Fprintln(w, "  may be freed by the operator (not vctl's to delete):")
		for _, r := range a.Reclaimable {
			fmt.Fprintf(w, "    %s  %.1f MiB (%s)\n", r.Path, r.MiB, r.Kind)
		}
	}
}

// tuneDetail is an item's values in a few words.
func tuneDetail(it tune.Item) string {
	now := "unset"
	if it.Value != nil {
		now = *it.Value
		if it.ID == tune.ItemZram {
			now += " MiB"
		}
	} else if it.ID == tune.ItemZram || it.ID == tune.ItemTmpLeftovers {
		now = "none"
	}
	if it.ID == tune.ItemTmpLeftovers && it.Value != nil {
		now += " MiB"
	}
	switch it.State {
	case tune.Pending:
		d := now + " -> " + it.Target
		if it.Reason != "" {
			d += " (last run: " + it.Reason + ")"
		}
		return d
	case tune.Skipped:
		return "(" + it.Reason + ")"
	case tune.UserSet:
		if it.Source != "" {
			return now + " (the owner's: " + it.Source + ")"
		}
		return now + " (the owner's)"
	}
	return now
}

// removeLeftoversAtStart removes vctl's own leftovers in RAM (internal/tune,
// leftovers.go), one log line for them all.
func removeLeftoversAtStart() {
	removed, err := tune.RemoveLeftovers(tuneEnv(), time.Now())
	if len(removed) > 0 {
		var n int64
		for _, l := range removed {
			n += l.Bytes
		}
		logging.L().Info(fmt.Sprintf("removed vctl's leftovers in RAM: %d file(s), %.1f MiB", len(removed), float64(n)/(1<<20)))
	}
	if err != nil {
		logging.L().Warn("could not remove vctl's leftovers in RAM", "err", err.Error())
	}
}

// tuneAtStart runs the router's tune once the daemon is up, in the
// background: it never holds the daemon up. It reads first and changes only
// what differs, so after the first run it runs nothing. A trial waits for
// `vectra keep` — its changes would outlive the reboot that ends it.
func (d *daemon) tuneAtStart(ctx context.Context) {
	wait := func(dt time.Duration) bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(dt):
			return true
		}
	}
	if !wait(tuneStartDelay) {
		return
	}
	for tuneTrial() {
		if !wait(tuneTrialPoll) {
			return
		}
	}
	c, cancel := context.WithTimeout(ctx, tuneTimeout)
	defer cancel()
	res, err := tune.Apply(c, tuneEnv())
	switch {
	case errors.Is(err, tune.ErrBusy):
		logging.L().Info("tune: another run holds the router; it tunes it")
		return
	case err != nil:
		logging.L().Warn("tune: could not run", "err", err.Error())
		return
	}
	for _, ch := range res.Changes {
		logging.L().Info("tune: "+ch.String(), "item", ch.ID)
	}
	for _, f := range res.Failed {
		logging.L().Warn("tune: "+f.Err, "item", f.ID)
	}
}
