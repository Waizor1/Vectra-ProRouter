package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/power"
	"vectra-controller-pro/internal/uiapi"
)

// `vctl power` is Vectra's own switch. /usr/sbin/vectra is `vctl power "$@"`,
// so a person types `vectra on`, `vectra off`, `vectra status`; the router
// UI's set_power runs the same change (rpcd_power.go).
func init() {
	register(command{name: "power", summary: "Vectra on or off (the `vectra` command): on [--trial [--minutes N]] [--force] | off | keep | status [--json]", run: cmdPower})
}

const powerUsage = `usage: vectra on [--trial [--minutes N]] [--force] | off | keep | status [--json]
  on            Vectra takes the traffic, for good: also after a reboot
  on --trial    takes it for N minutes (10), then gives it back by itself;
                a reboot gives it back too
  on --force    also when Vectra would carry nothing yet: no operator
                config, and no PassWall2 to route by until there is one
  keep          keeps a trial: Vectra stays on, as after "vectra on"
  off           gives the router back as it was: PassWall2, the previous
                Vectra agent, or the internet without a VPN
  status        whether Vectra is on, who carries the traffic, and the
                claim code while the router is not linked
on, off and keep run in the background (--foreground: here); what they do
goes to the log they name.`

// Seams: tests never start a process and never touch the router.
var (
	powerEnv = power.RouterEnv
	// powerStart starts a detached change and gives its pid.
	powerStart = func(c *exec.Cmd) (int, error) {
		if err := c.Start(); err != nil {
			return 0, err
		}
		pid := c.Process.Pid
		_ = c.Process.Release()
		return pid, nil
	}
	powerOut io.Writer = os.Stdout
	// powerHanded is the lock a detached change was handed, as its fd 3.
	powerHanded = func() *os.File { return handedFile(3, "vectra-power.lock") }
	// powerRuntime is the daemon's live state (nil: it does not answer), and
	// powerBot the Vectra bot the panel named (""), for status.
	powerRuntime = func(ctx context.Context) *localctl.Runtime {
		cfg := powerAgentCfg()
		c, cancel := context.WithTimeout(ctx, power.CallTime)
		defer cancel()
		resp, err := localctl.Call(c, cfg.UISocketPath, localctl.SocketRequest{Op: localctl.OpRuntime})
		if err != nil || !resp.OK {
			return nil
		}
		return resp.Runtime
	}
	powerBot = func() string {
		bot, _ := persistedClaim(powerAgentCfg())
		return bot
	}
)

// powerAgentCfg is the daemon's config where status reads it: the rendered
// agent.json, else its defaults — the same files.
func powerAgentCfg() agentcfg.Config {
	if cfg, err := agentcfg.Load("/var/run/vectra-controller-pro/agent.json"); err == nil {
		return cfg
	}
	var cfg agentcfg.Config
	cfg.Defaults()
	return cfg
}

// errWouldIdle: `vectra on` would leave the LAN without its VPN.
var errWouldIdle = errors.New("Vectra would carry no traffic yet: it has no operator config, and no PassWall2 configuration to route by. " +
	"Switched on now, it would leave the LAN's internet going out directly, without a VPN, until the router is linked to a Vectra account " +
	"(its claim code: vectra status, once Vectra runs). `vectra on --force` switches it on all the same")

// powerLogMax is where the power log starts afresh: it lives on /tmp, in RAM.
const powerLogMax = 64 << 10

func cmdPower(args []string) error {
	// Run by a person, from rpcd, or by the deadman: the environment may be
	// bare, and uci, ubus and the init scripts' tools are found through PATH.
	if os.Getenv("PATH") == "" {
		_ = os.Setenv("PATH", "/usr/sbin:/usr/bin:/sbin:/bin")
	}
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprintln(os.Stderr, powerUsage)
		if len(args) == 0 {
			return errors.New("on, off, keep or status")
		}
		return nil
	}
	verb := args[0]
	fs := newFlagSet("power " + verb)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, powerUsage) }
	var (
		fg      *bool
		trial   *bool
		force   *bool
		minutes *int
		asJSON  *bool
		id      *string
	)
	switch verb {
	case "on", "off", "keep":
		fg = fs.Bool("foreground", false, "make the change here, not in the background")
		if verb == "on" {
			trial = fs.Bool("trial", false, "take the traffic for --minutes, then give it back by itself")
			minutes = fs.Int("minutes", 10, "how long a trial lasts (1-1440)")
			force = fs.Bool("force", false, "switch on also when Vectra would carry no traffic yet")
		}
	case "status":
		asJSON = fs.Bool("json", false, "answer in JSON")
	case "deadman":
		// The trial's deadman, started by `on --trial`; not for people.
		id = fs.String("id", "", "the trial it ends")
	default:
		fmt.Fprintln(os.Stderr, powerUsage)
		return fmt.Errorf("unknown verb %q", verb)
	}
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected %q", strings.Join(fs.Args(), " "))
	}
	env := powerEnv()
	switch verb {
	case "status":
		return powerStatus(env, *asJSON, powerOut)
	case "deadman":
		return power.Deadman(context.Background(), env, *id, powerSay(powerOut, env.Now))
	}
	isTrial := trial != nil && *trial
	if minutes != nil && (*minutes < 1 || *minutes > 1440) {
		return fmt.Errorf("--minutes %d: a trial lasts 1 to 1440 minutes", *minutes)
	}
	if minutes != nil && !isTrial && minutesSet(fs) {
		return errors.New("--minutes is for a trial: vectra on --trial --minutes N")
	}
	// Said BEFORE anything is switched: an `on` that takes the router from
	// whatever carries its traffic now to a vctl that carries nothing. One
	// that runs already took it; the router UI asks with --force — its page
	// says what the router carries.
	if force != nil && !*force && env.WouldIdle() && !power.Read(context.Background(), env, false).Running {
		return errWouldIdle
	}
	if *fg {
		return powerChange(env, verb, isTrial, minutesOr(minutes), powerHanded(), powerOut)
	}
	return powerDetach(env, verb, isTrial, minutesOr(minutes), force != nil && *force, powerOut)
}

func minutesSet(fs *flag.FlagSet) bool {
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == "minutes" })
	return set
}

func minutesOr(m *int) int {
	if m == nil {
		return 0
	}
	return *m
}

// powerChange makes the change here, under the power lock: the one it was
// handed (fd 3, from whoever detached it), else one it takes itself.
func powerChange(env power.Env, verb string, trial bool, minutes int, handed *os.File, out io.Writer) error {
	lock, err := powerLock(env, handed)
	if err != nil {
		return err
	}
	defer lock.Close()
	ctx := context.Background()
	say := powerSay(out, env.Now)
	switch {
	case verb == "on" && trial:
		return power.StartTrial(ctx, env, minutes, func(id string) (int, error) {
			return powerSpawn(env, nil, "power", "deadman", "--id", id)
		}, say)
	case verb == "on":
		return power.On(ctx, env, say)
	case verb == "off":
		return power.Off(ctx, env, say)
	}
	return power.Keep(ctx, env, say)
}

// powerLock is the lock a change runs under. Whichever it is, what the change
// runs must not inherit it: luci-app-passwall2's start leaves processes
// behind that would hold it for as long as PassWall runs, and every later
// change would be refused as busy.
func powerLock(env power.Env, handed *os.File) (*os.File, error) {
	lock := handed
	if !power.Holds(env, lock) {
		if handed != nil {
			handed.Close() // not the lock: nothing this change needs
		}
		var err error
		if lock, err = power.Lock(env); err != nil {
			if errors.Is(err, power.ErrBusy) {
				return nil, fmt.Errorf("%w: tail %s", err, env.Log)
			}
			return nil, err
		}
	}
	syscall.CloseOnExec(int(lock.Fd()))
	return lock, nil
}

// powerDetach takes the lock and hands the change, with it, to a process of
// its own: see powerSpawn. It returns once that process has started.
func powerDetach(env power.Env, verb string, trial bool, minutes int, force bool, out io.Writer) error {
	lock, err := power.Lock(env)
	if errors.Is(err, power.ErrBusy) {
		return fmt.Errorf("%w: tail %s", err, env.Log)
	}
	if err != nil {
		return err
	}
	defer lock.Close()
	args := []string{"power", verb, "--foreground"}
	what := map[string]string{"on": "turning on", "off": "turning off", "keep": "keeping the trial"}[verb]
	if trial {
		args = append(args, "--trial", fmt.Sprintf("--minutes=%d", minutes))
		what = fmt.Sprintf("taking the traffic for a trial of %d min", minutes)
	}
	if force {
		args = append(args, "--force")
	}
	if _, err := powerSpawn(env, lock, args...); err != nil {
		return fmt.Errorf("the change did not start, so nothing changed: %w", err)
	}
	fmt.Fprintf(out, "Vectra is %s in the background; it takes a few seconds.\n  follow it: tail -f %s\n  then:      vectra status\n", what, env.Log)
	return nil
}

// powerSpawn starts `vctl <args>` in a session of its own, so that it
// outlives whoever asked: an rpcd call lives for one answer, and on a fleet
// router the legacy agent's job shell is stopped by the very takeover it
// asked for (a controller restarted from its own job lost its shell before
// the start). stdin is /dev/null, its output goes to the power log, and lock,
// when given, is its fd 3: held until it exits.
func powerSpawn(env power.Env, lock *os.File, args ...string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		return 0, err
	}
	defer devnull.Close()
	logf, err := openPowerLog(env.Log)
	if err != nil {
		return 0, err
	}
	defer logf.Close()
	cmd := exec.Command(exe, args...)
	cmd.Dir = "/" // it outlives the caller: not the caller's directory
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, logf, logf
	if lock != nil {
		cmd.ExtraFiles = []*os.File{lock}
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return powerStart(cmd)
}

func openPowerLog(path string) (*os.File, error) {
	if st, err := os.Stat(path); err == nil && st.Size() > powerLogMax {
		_ = os.Remove(path)
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

// powerSay writes a change's lines with the time, in UTC: the router's own
// zone is not known to every process that writes here.
func powerSay(out io.Writer, now func() time.Time) power.Say {
	return func(format string, a ...interface{}) {
		fmt.Fprintf(out, "%s %s\n", now().UTC().Format(time.RFC3339), fmt.Sprintf(format, a...))
	}
}

// powerStatusJSON is `vectra status --json`: status.power of the router UI
// (ui/contract), and what only a person at the console needs.
type powerStatusJSON struct {
	Enabled bool `json:"enabled"`
	Running bool `json:"running"`
	// Carrying: its data plane is loaded. Running without it, vctl carries
	// nothing and the holder is who else runs, or direct.
	Carrying bool             `json:"carrying"`
	Holder   string           `json:"holder"`
	HandBack *string          `json:"handBack"`
	UCI      bool             `json:"uci"`
	Boot     bool             `json:"boot"`
	Trial    *powerTrialState `json:"trial"`
	Busy     bool             `json:"busy"`
	// Claim: the code that links the router to a Vectra account, while vctl
	// runs and the router is not linked — what an operator passes on.
	Claim *powerClaimState `json:"claim"`
	// AutoRouteSource: "passwall" while vctl routes by PassWall2's
	// configuration of itself, until it is linked; null otherwise.
	AutoRouteSource *string `json:"autoRouteSource"`
}

type powerClaimState struct {
	Code      string `json:"code"`
	ExpiresAt string `json:"expiresAt"`
	// Link opens the claim in Vectra's app with the code filled in.
	Link string `json:"link"`
}

type powerTrialState struct {
	Until       *string `json:"until"`
	MinutesLeft int     `json:"minutesLeft"`
	Deadman     bool    `json:"deadman"`
}

func powerStatus(env power.Env, asJSON bool, out io.Writer) error {
	f := power.Read(context.Background(), env, false)
	st := powerStatusJSON{Enabled: f.On(), Running: f.Running, Carrying: f.Carrying, Holder: f.Holder(), UCI: f.UCI, Boot: f.Boot, Busy: power.Busy(env)}
	if hb := f.HandBack(); hb != "" {
		st.HandBack = &hb
	}
	if t := f.Trial; t != nil {
		st.Trial = &powerTrialState{Deadman: power.DeadmanAlive(env, t)}
		if !t.Until.IsZero() {
			u := t.Until.UTC().Format(time.RFC3339)
			st.Trial.Until = &u
			if left := t.Until.Sub(env.Now()); left > 0 {
				st.Trial.MinutesLeft = int((left + time.Minute - 1) / time.Minute)
			}
		}
	}
	if f.Running {
		if rt := powerRuntime(context.Background()); rt != nil {
			if c := rt.Claim; c != nil && c.State == "unclaimed" {
				st.Claim = &powerClaimState{Code: c.Code, ExpiresAt: c.ExpiresAt.UTC().Format(time.RFC3339), Link: uiapi.ClaimLink(powerBot(), c.Code)}
			}
			if rt.AutoRouteSource != "" {
				a := rt.AutoRouteSource
				st.AutoRouteSource = &a
			}
		}
	}
	if asJSON {
		return writeJSON(out, st)
	}
	for _, l := range powerLines(st, env.Log) {
		fmt.Fprintln(out, l)
	}
	return nil
}

// powerLines are `vectra status` for a person.
func powerLines(st powerStatusJSON, log string) []string {
	yes := map[bool]string{true: "yes", false: "no"}
	var out []string
	// vctl running without its data plane carries nothing: said so.
	idle := ""
	if st.Running && !st.Carrying {
		idle = ", and runs, but carries no traffic yet"
	}
	switch {
	case st.Trial != nil && st.Running:
		left := "its time is up"
		if st.Trial.MinutesLeft > 0 {
			left = fmt.Sprintf("%d min left", st.Trial.MinutesLeft)
		}
		out = append(out, fmt.Sprintf("Vectra is on for a trial (%s)%s: the traffic goes %s.", left, idle, power.Describe(st.Holder)),
			"  `vectra keep` keeps it on; `vectra off` goes back now; a reboot goes back too.")
		if !st.Trial.Deadman {
			out = append(out, "  The trial's deadman does not run: it ends only with `vectra off` or a reboot.")
		}
	case st.Enabled && st.Running:
		out = append(out, "Vectra is on"+idle+": the traffic goes "+power.Describe(st.Holder)+".")
	case st.Enabled:
		out = append(out, "Vectra is switched on but does not run: the traffic goes "+power.Describe(st.Holder)+".",
			"  `vectra on` starts it; logread -e vectra-controller-pro says why it stopped.")
	case st.Running:
		out = append(out, "Vectra runs but is switched off: the traffic goes "+power.Describe(st.Holder)+".",
			"  `vectra off` stops it; `vectra on` switches it on.")
	default:
		out = append(out, "Vectra is off: the traffic goes "+power.Describe(st.Holder)+".",
			"  `vectra on` turns it on; `vectra on --trial` tries it for 10 minutes.")
	}
	if st.HandBack != nil {
		out = append(out, "  Turning it off gives the traffic back: it will go "+power.Describe(*st.HandBack)+".")
	}
	if idle != "" {
		out = append(out, "  Its data plane is not loaded: before its first setup, or its render failed (logread -e vctl).")
	}
	if st.AutoRouteSource != nil {
		out = append(out, "  It routes by PassWall2's configuration until the router is linked: the operator's config then replaces it, with no gap.")
	}
	if c := st.Claim; c != nil {
		until := c.ExpiresAt
		if t, err := time.Parse(time.RFC3339, c.ExpiresAt); err == nil {
			until = t.UTC().Format("15:04 UTC")
		}
		out = append(out, fmt.Sprintf("  Not linked to a Vectra account yet. Claim code: %s (valid until %s)", c.Code, until),
			"  Link: "+c.Link)
	}
	out = append(out, fmt.Sprintf("  switch (uci enabled): %s · starts at boot: %s · running: %s", onOff(st.UCI), yes[st.Boot], yes[st.Running]))
	if st.Busy {
		out = append(out, "  A change is in flight: tail -f "+log)
	}
	return out
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
