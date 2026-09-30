// Package power is Vectra's own switch: `vectra on|off|keep|status` (vctl
// power) and the router UI's set_power. Turning Vectra on is the init
// script's takeover — the legacy agent and PassWall stopped, and remembered —
// and turning it off is the init script's hand-back. This package only flips
// the switches and runs the init script, in an order that a process killed
// half way cannot turn into a router with nothing on it.
//
// A trial (`vectra on --trial`) takes the traffic for some minutes and gives
// it back by itself: a detached deadman runs the same `off` once they are up,
// unless `vectra keep` was run meanwhile. A reboot ends it too: its takeover
// leaves every boot link as it was (TRIAL_FILE in the init script).
package power

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/uci"
)

// Who carries the LAN's traffic.
const (
	Vectra   = "vectra"    // vctl runs, and its data plane is loaded
	PassWall = "passwall2" // PassWall runs (either spelling of its package)
	Agent    = "agent"     // the legacy vectra-controller agent runs; PassWall does not
	// Direct: nothing does — the internet goes out without a VPN. Also while
	// vctl runs without its data plane (Facts.Carrying): before its first
	// setup, or with a render that failed.
	Direct = "direct"
)

// pkg is the UCI config and the procd service of vctl.
const pkg = "vectra-controller-pro"

// The init script's breadcrumbs (LEGACY_MARKER, PASSWALL_MARKER,
// PASSWALL_SWITCH_MARKER), in Env.MarkerDir — or Env.TrialMarkers, for a
// trial: what its stop owes the router.
const (
	agentMarker    = ".legacy-agent-disabled-by-vctl"
	passwallMarker = ".passwall-disabled-by-vctl"
	switchMarker   = ".passwall-switch-off-by-vctl" // PassWall's own switch, uci passwall2.@global[0].enabled
)

// Env is where the router keeps what the switch reads and changes; tests
// point it at a temp dir and at fakes.
type Env struct {
	Config    string   // /etc/config/vectra-controller-pro
	RCDir     string   // /etc/rc.d
	Init      string   // /etc/init.d/vectra-controller-pro
	MarkerDir string   // /etc/vectra-controller-pro
	PassWall  []string // PassWall's init script, both spellings, as the init script probes them
	Agent     string   // the legacy agent's init script
	ProcDir   string   // /proc
	Lock      string   // held by the one power change in flight
	Log       string   // where the detached changes write
	Trial     string   // the running trial; on tmpfs, so no boot sees it
	// TrialMarkers is where a trial's takeover leaves its breadcrumbs, on
	// tmpfs too (TRIAL_MARKER_DIR).
	TrialMarkers string
	// Snippet turns PassWall's own switch on again at the next boot, while a
	// trial holds it off (PASSWALL_SWITCH_SNIPPET, in /etc/uci-defaults): the
	// boot runs and deletes it; so do `off` and `keep`, through the init
	// script.
	Snippet string
	// OperatorConfig and ProviderDoc are what vctl renders its xray from, on
	// /etc: with both there vctl is configured, and runs to carry traffic —
	// switched on, it must load its data plane. Without them it waits for its
	// first setup, and runs without one.
	OperatorConfig, ProviderDoc string

	// Run runs a command (uci, an init script, nft); Output runs one and
	// returns its stdout (ubus).
	Run    func(ctx context.Context, name string, args ...string) error
	Output func(ctx context.Context, name string, args ...string) ([]byte, error)
	// Daemon: vctl answers on its socket — it is up and serving.
	Daemon func(ctx context.Context) bool
	// Loaded: vctl's data plane is loaded. nil asks nft for the `inet vctl`
	// table; the router UI's status passes what it read from nft already.
	Loaded func(ctx context.Context) bool
	Now    func() time.Time
	Sleep  func(time.Duration)
}

// RouterEnv is the production Env. The trial's files are the init script's
// TRIAL_FILE and TRIAL_MARKER_DIR; a test holds them together.
func RouterEnv() Env {
	var cfg agentcfg.Config
	cfg.Defaults()
	return Env{
		Config:       "/etc/config/" + pkg,
		RCDir:        "/etc/rc.d",
		Init:         "/etc/init.d/" + pkg,
		MarkerDir:    "/etc/" + pkg,
		PassWall:     []string{"/etc/init.d/passwall2", "/etc/init.d/passwall"},
		Agent:        "/etc/init.d/vectra-controller",
		ProcDir:      "/proc",
		Lock:         "/var/lock/vectra-power.lock",
		Log:          "/tmp/vectra-power.log",
		Trial:        "/tmp/vectra-trial.json",
		TrialMarkers: "/tmp/vectra-trial.d",
		Snippet:      "/etc/uci-defaults/99-vectra-trial-passwall-switch",
		// The daemon's own defaults: the files it resumes its render from.
		OperatorConfig: cfg.XrayConfigPath,
		ProviderDoc:    cfg.ProviderConfigPath,
		Run:            runCommand,
		Output:         outputCommand,
		Daemon: func(ctx context.Context) bool {
			c, cancel := context.WithTimeout(ctx, CallTime)
			defer cancel()
			resp, err := localctl.Call(c, cfg.UISocketPath, localctl.SocketRequest{Op: localctl.OpRuntime})
			return err == nil && resp.OK
		},
		Now:   time.Now,
		Sleep: time.Sleep,
	}
}

// runCommand runs a command with its output discarded. Not captured on
// purpose: an init script starts daemons, and luci-app-passwall2's leave
// processes behind that would hold a pipe open for as long as PassWall runs.
// The environment loses the two variables that make the init script do
// nothing (the package's postinst guard) or skip its hand-back (a reload):
// `vectra on` from a shell that happens to carry one must still turn it on.
func runCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = cleanEnv(os.Environ())
	cmd.WaitDelay = time.Second
	return cmd.Run()
}

func outputCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	return cmd.Output()
}

func cleanEnv(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if strings.HasPrefix(kv, "VECTRA_SKIP_POSTINST_RESTART=") || strings.HasPrefix(kv, "VCTL_RELOADING=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// Timings; tests shorten them.
var (
	CallTime = 3 * time.Second  // one question to procd
	UCITime  = 10 * time.Second // one uci command
	InitTime = 3 * time.Minute  // one init script verb: PassWall's own stop and start restart dnsmasq
	Settle   = 30 * time.Second // for procd to show vctl up, or gone
	// DataplaneWait: for a configured vctl, once up, to load its data plane
	// (xray started, the firewall programmed).
	DataplaneWait = 60 * time.Second
	Poll          = time.Second
	DeadmanPoll   = 5 * time.Second
	LockWait      = 2 * time.Minute // for the deadman: a change in flight finishes first
)

// Facts are what the switch reads.
type Facts struct {
	UCI  bool // vectra-controller-pro.main.enabled, read as the init script reads it
	Boot bool // an rc.d start link: vctl starts at boot
	// Running: procd runs vctl.
	Running bool
	// Carrying: vctl's data plane is loaded (the `inet vctl` table), so it
	// carries the LAN's traffic. Read only while vctl runs.
	Carrying bool
	// PassWall, Agent: they run. Read only while vctl carries nothing, for
	// Holder.
	PassWall, Agent bool
	// Owed is what the init script's stop gives back, from its breadcrumbs:
	// PassWall, Agent or "".
	Owed  string
	Trial *Trial // nil when no trial runs
}

// On is Vectra switched on: both switches on, or a trial running (which
// leaves them as they were on purpose).
func (f Facts) On() bool { return (f.UCI && f.Boot) || f.Trial != nil }

// Holder is who carries the traffic now. vctl running is not enough: without
// its data plane nothing takes the LAN's traffic to it.
func (f Facts) Holder() string {
	switch {
	case f.Running && f.Carrying:
		return Vectra
	case f.PassWall:
		return PassWall
	case f.Agent:
		return Agent
	}
	return Direct
}

// HandBack is who takes the traffic when Vectra is turned off: what the
// breadcrumbs say the stop gives back, else nobody — the internet then goes
// out directly. "" while Vectra is off: there is nothing to give back.
func (f Facts) HandBack() string {
	switch {
	case !f.On() && !f.Running:
		return ""
	case f.Owed != "":
		return f.Owed
	}
	return Direct
}

// Read gathers the facts. It never fails: what cannot be read is off. up
// says the daemon answered on its socket, which spares asking procd.
func Read(ctx context.Context, env Env, up bool) Facts {
	f := Facts{
		UCI:     uciOn(env.Config),
		Boot:    startsAtBoot(env.RCDir, filepath.Base(env.Init)),
		Owed:    owed(env),
		Trial:   LoadTrial(env),
		Running: up,
	}
	if !f.Running {
		f.Running, _, _ = procd(ctx, env, pkg)
	}
	if f.Running {
		f.Carrying = env.loaded(ctx)
	}
	if !f.Running || !f.Carrying {
		f.PassWall = PassWallRunning(env.ProcDir)
		if !f.PassWall && executable(env.Agent) {
			f.Agent, _, _ = procd(ctx, env, filepath.Base(env.Agent))
		}
	}
	return f
}

// uciOn reads option enabled of section main as the init script does
// (config_get_bool, default 1): 1, on, true, yes and enabled are on; 0, off,
// false, no and disabled are off — exactly these spellings — and anything
// else, or nothing at all, is the default: on.
func uciOn(path string) bool {
	f, err := uci.Load(path)
	if err != nil {
		return true
	}
	if main := f.Named("main"); main != nil {
		switch main.Get("enabled") {
		case "0", "off", "false", "no", "disabled":
			return false
		}
	}
	return true
}

// startsAtBoot: the service's rc.d start link, S<nn><name> — exactly its name,
// so "vectra-controller" does not match "vectra-controller-pro".
func startsAtBoot(rcDir, name string) bool {
	m, _ := filepath.Glob(filepath.Join(rcDir, "S[0-9][0-9]"+name))
	return len(m) > 0
}

// procd tells whether procd runs an instance of the service and whether it
// has one at all — an init script whose start declined leaves none; ok is
// false when procd did not answer.
func procd(ctx context.Context, env Env, service string) (running, present, ok bool) {
	c, cancel := context.WithTimeout(ctx, CallTime)
	defer cancel()
	arg, _ := json.Marshal(map[string]string{"name": service})
	out, err := env.Output(c, "ubus", "call", "service", "list", string(arg))
	if err != nil {
		return false, false, false
	}
	var list map[string]struct {
		Instances map[string]struct {
			Running bool `json:"running"`
		} `json:"instances"`
	}
	// No such service: ubus answers with an empty object, or with nothing.
	if len(bytes.TrimSpace(out)) > 0 && json.Unmarshal(out, &list) != nil {
		return false, false, false
	}
	for _, in := range list[service].Instances {
		present = true
		running = running || in.Running
	}
	return running, present, true
}

// PassWallRunning looks for PassWall's stack the way the init script's
// passwall_running does, and PassWall's app.sh itself: something running
// from its temp bin directory, /tmp/etc/passwall2/bin (or passwall's). Not
// any command line that names it — subscribe.lua, a `tail -f` of its log,
// lease2hosts.sh carry no traffic.
func PassWallRunning(procDir string) bool {
	ents, err := os.ReadDir(procDir)
	if err != nil {
		return false
	}
	self := strconv.Itoa(os.Getpid())
	for _, e := range ents {
		if _, err := strconv.Atoi(e.Name()); err != nil || e.Name() == self {
			continue
		}
		b, err := os.ReadFile(filepath.Join(procDir, e.Name(), "cmdline"))
		if err == nil && (bytes.Contains(b, []byte("/tmp/etc/passwall2/bin")) || bytes.Contains(b, []byte("/tmp/etc/passwall/bin"))) {
			return true
		}
	}
	return false
}

// loaded tells whether vctl's data plane is loaded: env.Loaded, else nft.
func (env Env) loaded(ctx context.Context) bool {
	if env.Loaded != nil {
		return env.Loaded(ctx)
	}
	c, cancel := context.WithTimeout(ctx, CallTime)
	defer cancel()
	// Terse: the table's direct set can be 15k ranges (see nftTableLoaded).
	return env.Run(c, "nft", "-t", "list", "table", "inet", "vctl") == nil
}

// configured: vctl has what it renders its xray from (Env.OperatorConfig,
// Env.ProviderDoc).
func (env Env) configured() bool {
	return env.OperatorConfig != "" && env.ProviderDoc != "" && exists(env.OperatorConfig) && exists(env.ProviderDoc)
}

// owed is what the init script's stop gives back: the PassWall stack — its
// rc.d link or its own switch; it is restored first, and then carries the
// traffic — else the legacy agent. A breadcrumb is on /etc, or on tmpfs for a
// trial; one whose service is not installed owes nothing: the stop skips it.
func owed(env Env) string {
	if noted(env, passwallMarker) || noted(env, switchMarker) {
		for _, p := range env.PassWall {
			if executable(p) {
				return PassWall
			}
		}
	}
	if noted(env, agentMarker) && executable(env.Agent) {
		return Agent
	}
	return ""
}

// noted: the init script's breadcrumb name, on /etc or on tmpfs.
func noted(env Env, name string) bool {
	return exists(filepath.Join(env.MarkerDir, name)) || (env.TrialMarkers != "" && exists(filepath.Join(env.TrialMarkers, name)))
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// executable is the init script's `[ -x ]`: what it runs, and what it skips.
func executable(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir() && st.Mode().Perm()&0o111 != 0
}

// Trial is a trial takeover: Env.Trial, whose mere existence is what the init
// script asks about.
type Trial struct {
	ID      string    `json:"id"`
	Started time.Time `json:"started"`
	Until   time.Time `json:"until"`
	Minutes int       `json:"minutes"`
	// Deadman is its pid, for `vectra status`; 0 until it has been started.
	Deadman int `json:"deadman"`
}

// LoadTrial is the running trial, nil when there is none. A file that does
// not read as one is still a trial — the init script's `[ -f ]` says so — only
// one without a deadman of its own.
func LoadTrial(env Env) *Trial {
	st, err := os.Stat(env.Trial)
	if err != nil || !st.Mode().IsRegular() {
		return nil
	}
	var t Trial
	if raw, err := os.ReadFile(env.Trial); err == nil {
		_ = json.Unmarshal(raw, &t)
	}
	return &t
}

func writeTrial(env Env, t Trial) error {
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return localctl.WriteFileAtomic(env.Trial, raw, 0o644)
}

// DeadmanAlive reports whether the trial's deadman still runs: its pid is a
// `vctl power deadman` for this very trial, not whatever took the number.
func DeadmanAlive(env Env, t *Trial) bool {
	if t == nil || t.Deadman <= 0 || t.ID == "" {
		return false
	}
	b, err := os.ReadFile(filepath.Join(env.ProcDir, strconv.Itoa(t.Deadman), "cmdline"))
	return err == nil && bytes.Contains(b, []byte("deadman")) && bytes.Contains(b, []byte(t.ID))
}

// ErrBusy is a power change still in flight (code busy).
var ErrBusy = errors.New("another power change is still in flight")

// Lock takes the power lock without waiting: ErrBusy while a change holds
// it. It lives as long as the returned file stays open — here, or in the
// detached change it is handed to — and a change that dies lets go of it, so
// it needs no expiry of its own.
func Lock(env Env) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(env.Lock), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(env.Lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return f, nil
}

// Holds reports whether f is the power lock file and the lock is held on it:
// taking it again on the same descriptor is a no-op, on another holder's it
// fails.
func Holds(env Env, f *os.File) bool {
	if f == nil {
		return false
	}
	have, err := f.Stat()
	if err != nil {
		return false
	}
	want, err := os.Stat(env.Lock)
	if err != nil || !os.SameFile(have, want) {
		return false
	}
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}

// Busy reports whether a power change is in flight. It looks, and does not
// take the lock even for a moment — `vectra status` or the UI's status poll
// would then refuse a change that came at that moment as busy: the kernel's
// /proc/locks names every lock held, by device and inode. Where that cannot
// be read it takes the lock and lets go of it.
func Busy(env Env) bool {
	if held, ok := lockHeld(env); ok {
		return held
	}
	f, err := Lock(env)
	if err != nil {
		return errors.Is(err, ErrBusy)
	}
	f.Close()
	return false
}

// lockHeld looks for a lock on Env.Lock in /proc/locks; ok is false when it
// cannot tell. No lock file: nobody holds it.
func lockHeld(env Env) (held, ok bool) {
	st, err := os.Stat(env.Lock)
	if err != nil {
		return false, os.IsNotExist(err)
	}
	sys, isStat := st.Sys().(*syscall.Stat_t)
	raw, err := os.ReadFile(filepath.Join(env.ProcDir, "locks"))
	if !isStat || err != nil {
		return false, false
	}
	// "1: FLOCK  ADVISORY  WRITE 1234 fd:01:5678 0 EOF" — major and minor in
	// hex, the inode in decimal.
	major, minor := devNumbers(uint64(sys.Dev))
	for _, line := range strings.Split(string(raw), "\n") {
		for _, field := range strings.Fields(line) {
			parts := strings.Split(field, ":")
			if len(parts) != 3 {
				continue
			}
			ma, e1 := strconv.ParseUint(parts[0], 16, 64)
			mi, e2 := strconv.ParseUint(parts[1], 16, 64)
			ino, e3 := strconv.ParseUint(parts[2], 10, 64)
			if e1 == nil && e2 == nil && e3 == nil && ma == major && mi == minor && ino == uint64(sys.Ino) {
				return true, true
			}
		}
	}
	return false, true
}

// devNumbers splits st_dev into the kernel's major and minor, as glibc's and
// musl's major() and minor() do.
func devNumbers(dev uint64) (major, minor uint64) {
	return (dev>>8)&0xfff | (dev>>32)&^0xfff, dev&0xff | (dev>>12)&^0xff
}
