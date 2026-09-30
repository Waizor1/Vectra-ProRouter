package power

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Say is where a change says what it does, one line at a time.
type Say func(format string, a ...interface{})

// Describe says where the traffic goes, for `vectra` and its log.
func Describe(holder string) string {
	switch holder {
	case Vectra:
		return "through Vectra"
	case PassWall:
		return "through PassWall2"
	case Agent:
		return "through the previous Vectra agent"
	}
	return "directly, without a VPN"
}

// On turns Vectra on for good: the UCI switch and the boot links on, then the
// init script's start — its takeover. A running trial is kept instead. What
// is on already is left alone: `on` twice takes nothing twice.
//
// On is done when vctl is up and — configured — carries the traffic, its
// data plane loaded. One that does not get there is turned off again at
// once, the router given back (giveBack): a vctl that holds the router and
// carries nothing leaves the LAN without its VPN, and nothing else would
// hand it back.
func On(ctx context.Context, env Env, say Say) error {
	f := Read(ctx, env, false)
	if f.Trial != nil {
		return Keep(ctx, env, say)
	}
	if f.UCI && f.Boot && f.Running {
		// Nothing changed here, so nothing to undo: said, not given back.
		if err := env.carries(ctx); err != nil {
			return fmt.Errorf("Vectra is on, but %w", err)
		}
		say("Vectra is on already: the traffic goes %s", Describe(Vectra))
		return nil
	}
	if !f.UCI {
		if err := env.setSwitch(ctx, say, true); err != nil {
			return err
		}
	}
	if !f.Boot {
		if err := env.init(ctx, say, "enable"); err != nil {
			return env.giveBack(ctx, say, err)
		}
	}
	if f.Running {
		if err := env.carries(ctx); err != nil {
			return env.giveBack(ctx, say, err)
		}
	} else if err := env.start(ctx, say); err != nil {
		return env.giveBack(ctx, say, err)
	}
	say("Vectra is on: `vectra off` gives the traffic back (%s)", Describe(Read(ctx, env, true).HandBack()))
	return nil
}

// Off turns Vectra off and gives the router back as it was before Vectra
// took it.
//
// The UCI switch goes first, because it is the one that holds: with it off,
// no start takes the router again — not a reboot's, not a restart's, not one
// racing this. Boot links removed first instead, and this process killed
// before the stop, would leave a router whose next boot starts nothing at all.
//
// Then the init script's stop: it unloads the data plane and restores what
// its breadcrumbs say it took. It runs even when vctl does not — it is also
// how a data plane left behind goes, and how what is still owed comes back —
// and with nothing owed it touches nothing. The boot links go last, unless a
// breadcrumb is still there: then every boot tries the hand-back again.
//
// A trial ends even when the UCI switch cannot be written (a full or
// read-only overlay): the trial never needed it — it runs whatever the switch
// says, and its deadman's `off` must still give the router back.
func Off(ctx context.Context, env Env, say Say) error {
	f := Read(ctx, env, false)
	if f.UCI {
		if err := env.setSwitch(ctx, say, false); err != nil {
			if f.Trial == nil {
				return err
			}
			say("%v; the trial ends all the same", err)
		}
	}
	if f.Trial != nil {
		say("the trial ends")
		if err := os.Remove(env.Trial); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := env.init(ctx, say, "stop"); err != nil {
		return err
	}
	if !env.settle(ctx) {
		return errors.New("vctl still runs after its stop (logread -e vectra-controller-pro)")
	}
	after := Read(ctx, env, false)
	switch {
	case after.Owed != "" && after.Boot:
		say("%s did not come back enabled and its breadcrumb stays: so do the boot links, and every boot tries again", stackName(after.Owed))
	case after.Owed != "":
		say("%s did not come back enabled and its breadcrumb stays: `vectra off` tries again", stackName(after.Owed))
	case after.Boot:
		if err := env.init(ctx, say, "disable"); err != nil {
			return err
		}
	}
	say("Vectra is off: the traffic goes %s", Describe(after.Holder()))
	return nil
}

// Keep makes the running trial Vectra on for good: the UCI switch and the
// boot links on, the trial's file gone — its deadman sees that and leaves —
// and the init script's start once more, now without the file: today's
// takeover, which disables the legacy agent and PassWall as well as stopping
// them, and keeps PassWall's own switch off for good (its breadcrumb moved to
// /etc, the trial's snippet gone). procd keeps vctl's instance, unless the
// switch was off during the trial: a changed config file restarts it, a few
// seconds without internet.
//
// Once the trial's file is gone its deadman is disarmed, so from there on
// whatever fails gives the router back (giveBack) rather than leave it
// half kept.
func Keep(ctx context.Context, env Env, say Say) error {
	f := Read(ctx, env, false)
	if f.Trial == nil {
		if f.UCI && f.Boot && f.Running {
			say("Vectra is on for good already")
			return nil
		}
		return errors.New("no trial runs: `vectra on` turns Vectra on for good, `vectra on --trial` tries it")
	}
	if !f.UCI {
		if err := env.setSwitch(ctx, say, true); err != nil {
			return err
		}
	}
	say("the trial is kept")
	if err := os.Remove(env.Trial); err != nil && !os.IsNotExist(err) {
		return err
	}
	if !f.Boot {
		if err := env.init(ctx, say, "enable"); err != nil {
			return env.giveBack(ctx, say, err)
		}
	}
	if err := env.start(ctx, say); err != nil {
		return env.giveBack(ctx, say, err)
	}
	// Proven, not assumed: a service left starting at boot would come up next
	// to vctl after the next reboot.
	if left := env.startingAtBoot(); len(left) > 0 {
		return env.giveBack(ctx, say, fmt.Errorf("the takeover did not disable %s: it would start next to Vectra after a reboot", strings.Join(left, ", ")))
	}
	// And PassWall's own switch, which the trial turned off: noted on tmpfs
	// alone, nothing would turn it on again after a reboot; the trial's
	// snippet left behind turns it on at the next boot.
	if env.TrialMarkers != "" && exists(filepath.Join(env.TrialMarkers, switchMarker)) && !exists(filepath.Join(env.MarkerDir, switchMarker)) {
		return env.giveBack(ctx, say, errors.New("the takeover did not note on /etc that PassWall's own switch is off: after a reboot `vectra off` could not turn it on again"))
	}
	if exists(env.Snippet) {
		return env.giveBack(ctx, say, fmt.Errorf("the takeover left %s: the next boot turns PassWall's own switch on again", env.Snippet))
	}
	say("Vectra stays on, also after a reboot")
	return nil
}

// StartTrial takes the traffic for minutes (see the package comment). The
// deadman is started before anything is taken — the way back runs first —
// and a takeover that does not come up ends the trial at once, with Off. A
// trial already running gets its minutes again, from now.
func StartTrial(ctx context.Context, env Env, minutes int, deadman func(id string) (pid int, err error), say Say) error {
	f := Read(ctx, env, false)
	switch {
	case f.UCI && f.Boot && f.Running:
		say("Vectra is on for good already (it starts at boot): there is nothing to try. `vectra off` gives the router back")
		return nil
	case f.UCI && f.Boot:
		return errors.New("Vectra is switched on for good (it starts at boot) but does not run: `vectra on` starts it; after `vectra off`, `vectra on --trial` tries it")
	case f.Running && f.Trial == nil:
		return errors.New("Vectra runs already, without a trial: its takeover disabled what it stopped, so a reboot would not give the router back. `vectra off` first")
	}
	now := env.Now()
	t := Trial{ID: strconv.FormatInt(now.UnixNano(), 36), Started: now.UTC(), Until: now.Add(time.Duration(minutes) * time.Minute).UTC(), Minutes: minutes}
	if err := writeTrial(env, t); err != nil {
		return err
	}
	pid, err := deadman(t.ID)
	if err != nil && f.Trial != nil {
		// Starting over: the old deadman may have seen the new trial and left.
		// A trial with no deadman ends now rather than never.
		say("the deadman did not start: the trial ends now")
		if oerr := Off(ctx, env, say); oerr != nil {
			say("%v", oerr)
		}
		return fmt.Errorf("the deadman did not start: %w", err)
	}
	if err != nil {
		_ = os.Remove(env.Trial)
		return fmt.Errorf("the deadman did not start, so nothing was taken: %w", err)
	}
	t.Deadman = pid
	if err := writeTrial(env, t); err != nil {
		say("the deadman runs, but its pid was not noted: %v", err)
	}
	until := t.Until.Format("15:04 UTC")
	if f.Running {
		say("the trial starts over: %d min, until %s", minutes, until)
		return nil
	}
	say("a trial of %d min, until %s: the legacy agent and PassWall are stopped, not disabled", minutes, until)
	if err := env.start(ctx, say); err != nil {
		say("%v", err)
		say("the trial ends at once")
		if oerr := Off(ctx, env, say); oerr != nil {
			say("%v", oerr)
		}
		return err
	}
	say("Vectra carries the traffic until %s. `vectra keep` keeps it; `vectra off` goes back now; a reboot goes back too", until)
	return nil
}

// Deadman ends the trial it was started for. Every DeadmanPoll it looks for
// the trial's file — gone or replaced (keep, off, another trial) and it
// leaves — and once the minutes are up it takes the power lock, after any
// change in flight, and turns Vectra off. It keeps its own clock: the router's
// may still jump when NTP syncs.
func Deadman(ctx context.Context, env Env, id string, say Say) error {
	t := LoadTrial(env)
	if t == nil || t.ID != id {
		return nil
	}
	deadline := env.Now().Add(time.Duration(t.Minutes) * time.Minute)
	for {
		env.Sleep(DeadmanPoll)
		if cur := LoadTrial(env); cur == nil || cur.ID != id {
			say("the trial was kept or ended: the deadman leaves")
			return nil
		}
		if !env.Now().Before(deadline) {
			break
		}
	}
	lock, err := waitLock(ctx, env)
	if err != nil {
		return fmt.Errorf("the trial is over, but the power lock stayed held: %w", err)
	}
	defer lock.Close()
	if cur := LoadTrial(env); cur == nil || cur.ID != id {
		return nil
	}
	say("the trial's %d min are up", t.Minutes)
	return Off(ctx, env, say)
}

// waitLock takes the power lock, waiting up to LockWait for a change in
// flight.
func waitLock(ctx context.Context, env Env) (*os.File, error) {
	start := env.Now()
	for {
		f, err := Lock(env)
		if !errors.Is(err, ErrBusy) || env.Now().Sub(start) >= LockWait || ctx.Err() != nil {
			return f, err
		}
		env.Sleep(Poll)
	}
}

// setSwitch sets the UCI switch, vectra-controller-pro.main.enabled, and
// commits it. `set …main=controller` first: a config without its section
// would refuse the option.
func (env Env) setSwitch(ctx context.Context, say Say, on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	say("uci set %s.main.enabled=%s", pkg, v)
	for _, args := range [][]string{
		{"set", pkg + ".main=controller"},
		{"set", pkg + ".main.enabled=" + v},
		{"commit", pkg},
	} {
		c, cancel := context.WithTimeout(ctx, UCITime)
		err := env.Run(c, "uci", args...)
		cancel()
		if err != nil {
			return fmt.Errorf("uci %s: %w", strings.Join(args, " "), err)
		}
	}
	return nil
}

func (env Env) init(ctx context.Context, say Say, verb string) error {
	say("%s %s", env.Init, verb)
	c, cancel := context.WithTimeout(ctx, InitTime)
	defer cancel()
	if err := env.Run(c, env.Init, verb); err != nil {
		return fmt.Errorf("%s %s: %w", env.Init, verb, err)
	}
	return nil
}

// Why a switch-on did not get there.
var (
	errDeclined = errors.New("the takeover declined and handed the router back — a precondition failed, or PassWall would not stop (logread -e vectra-controller-pro)")
	errUnstable = errors.New("vctl did not stay up (logread -e vctl)")
	errIdle     = errors.New("vctl runs without its data plane: it would carry no traffic (logread -e vctl)")
)

// start runs the init script's start, then waits for vctl to be up and, if
// it is configured, to carry the traffic.
func (env Env) start(ctx context.Context, say Say) error {
	if err := env.init(ctx, say, "start"); err != nil {
		return err
	}
	if err := env.up(ctx); err != nil {
		return err
	}
	return env.carries(ctx)
}

// up waits, within Settle, for vctl to be up: its socket answering, or procd
// running it three looks in a row, Poll apart — one look can catch a vctl
// between two crashes of a crash loop. An instance procd does not have at
// all is the takeover declining: the init script handed the router back.
func (env Env) up(ctx context.Context) error {
	start, seen := env.Now(), 0
	for {
		if env.Daemon != nil && env.Daemon(ctx) {
			return nil
		}
		running, present, ok := procd(ctx, env, pkg)
		switch {
		case ok && running:
			if seen++; seen >= 3 {
				return nil
			}
		case ok && !present:
			return errDeclined
		default:
			seen = 0
		}
		if env.Now().Sub(start) >= Settle || ctx.Err() != nil {
			return errUnstable
		}
		env.Sleep(Poll)
	}
}

// carries waits, within DataplaneWait, for a configured vctl's data plane to
// load; an unconfigured one — before its first setup — carries nothing yet,
// by design, and passes. vctl stopping meanwhile fails at once.
func (env Env) carries(ctx context.Context) error {
	if !env.configured() {
		return nil
	}
	start := env.Now()
	for {
		if env.loaded(ctx) {
			return nil
		}
		if running, _, ok := procd(ctx, env, pkg); ok && !running {
			return errUnstable
		}
		if env.Now().Sub(start) >= DataplaneWait || ctx.Err() != nil {
			return errIdle
		}
		env.Sleep(Poll)
	}
}

// giveBack turns Vectra off again — Off, the whole hand-back — after a
// switch-on that did not get there, and says what happened.
func (env Env) giveBack(ctx context.Context, say Say, cause error) error {
	say("%v", cause)
	say("turning Vectra off again, and giving the router back")
	if err := Off(ctx, env, say); err != nil {
		return fmt.Errorf("%w; turning Vectra off again failed too: %v", cause, err)
	}
	return fmt.Errorf("%w: Vectra is off again, the traffic goes %s", cause, Describe(Read(ctx, env, false).Holder()))
}

// settle waits, within Settle, for procd to show vctl gone.
func (env Env) settle(ctx context.Context) bool {
	start := env.Now()
	for {
		if running, _, ok := procd(ctx, env, pkg); ok && !running {
			return true
		}
		if env.Now().Sub(start) >= Settle || ctx.Err() != nil {
			return false
		}
		env.Sleep(Poll)
	}
}

// startingAtBoot names the other stacks that still start at boot.
func (env Env) startingAtBoot() []string {
	var out []string
	for _, p := range append(append([]string{}, env.PassWall...), env.Agent) {
		if executable(p) && startsAtBoot(env.RCDir, filepath.Base(p)) {
			out = append(out, filepath.Base(p))
		}
	}
	return out
}

func stackName(holder string) string {
	switch holder {
	case PassWall:
		return "PassWall2"
	case Agent:
		return "the previous Vectra agent"
	}
	return holder
}
