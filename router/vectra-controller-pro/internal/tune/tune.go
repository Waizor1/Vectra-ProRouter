// Package tune is the router's tune («Разгон роутера»): at install and at
// every start of the daemon vctl looks at what the router is — its RAM, its
// cores, its swap, what the kernel and UCI hold now, what someone set on
// purpose — and sets what gets the most out of this hardware while it stays
// a working router. Seven items:
//
//   - zram: a router with less than 384 MiB of RAM (the previous agent's
//     line, 91_vectra_low_mem_profile) runs compressed swap — zram-swap, a
//     dependency of the package, its service enabled and started. The memory
//     guard (internal/memguard) and the r35 stress results assume it; a clean
//     install used to have none.
//   - swappiness, vfs_cache_pressure: on the same router the kernel is told
//     to use that swap (vm.swappiness 80, vm.vfs_cache_pressure 200 — the
//     previous agent's values), in a file of the tune's own
//     (/etc/sysctl.d/95-vectra-tune.conf) that the boot applies again.
//     vm.min_free_kbytes is never touched: the init script keeps it
//     (take_memory_reserve), and nothing proved a lower one safe under Wi-Fi.
//   - packet_steering: a router with two cores or more has both take the
//     network's receive work (network.@globals[0].packet_steering '1',
//     applied by OpenWrt's own /etc/init.d/packet_steering: it writes the
//     queues' CPU masks, and netifd is never reloaded — a netifd reload would
//     drop the fwmark policy route the data plane needs).
//   - flow_offloading: software flow offloading
//     (firewall.@defaults[0].flow_offloading '1'): a forwarded connection,
//     once established, skips the firewall's chains. Never hardware
//     offloading (flow_offloading_hw): unprovable without a wired rig.
//     Applied with fw4's reload, which is safe under vctl's traffic (see
//     applyOffloading).
//   - cron_loglevel: busybox crond logs every job it runs, at err level, into
//     logread's 64 KB ring — vctl's dead-man and watchdog every minute push
//     out what the log is kept for. system.@system[0].cronloglevel '9' keeps
//     only its warnings, applied with cron's own reload.
//   - tmp_leftovers: vctl's own leftovers in RAM (/tmp): a downloaded update
//     package, a crash-left temp file of the vault (leftovers.go) — by their
//     exact names, older than ten minutes and open in no process. Nothing
//     else is ever removed, and nothing is backed up: they are vctl's, and
//     there is nothing to put back.
//
// What someone set explicitly stays theirs (user_set): an option set to
// anything else, a sysctl set in a file of their own, zram switched off
// after the tune switched it on. Every value the tune changes is backed up
// first (Env.Backup, 0600), and Undo puts back what is still as the tune
// left it. vctl-controller-pro.main.tune '0' makes Apply change nothing.
//
// Inspect only reads files: it is what `vctl tune plan` prints and what the
// router UI's status carries, at every call. `vctl tune plan` adds Analyze
// (analysis.go): where the router's memory and flash go, read only.
package tune

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"vectra-controller-pro/internal/memguard"
	"vectra-controller-pro/internal/uci"
)

// Items, in the order the tune takes them.
const (
	ItemZram             = "zram"
	ItemSwappiness       = "swappiness"
	ItemVFSCachePressure = "vfs_cache_pressure"
	ItemPacketSteering   = "packet_steering"
	ItemFlowOffloading   = "flow_offloading"
	ItemCronLogLevel     = "cron_loglevel"
	ItemTmpLeftovers     = "tmp_leftovers"
)

// An item's state.
const (
	// Applied: the tune set it, and it is still so.
	Applied = "applied"
	// Already: it was so before the tune.
	Already = "already"
	// Pending: not so yet; the tune sets it at its next run.
	Pending = "pending"
	// UserSet: set otherwise on purpose; the tune leaves it.
	UserSet = "user_set"
	// Skipped: not for this router, or not now (Reason says why).
	Skipped = "skipped"
)

// Why an item is skipped (or, with Pending, why the last run did not set it).
const (
	ReasonOff             = "off"               // vectra-controller-pro.main.tune '0'
	ReasonEnoughRAM       = "enough_ram"        // 384 MiB of RAM or more
	ReasonOneCore         = "one_core"          // nothing to steer to
	ReasonNotInstalled    = "not_installed"     // no zram-swap
	ReasonNoSwap          = "no_swap"           // a kernel without swap
	ReasonNotSupported    = "not_supported"     // no /etc/init.d/packet_steering
	ReasonNoFw4           = "no_fw4"            // not firewall4
	ReasonNoDefaults      = "no_defaults"       // no defaults section in /etc/config/firewall
	ReasonHWOffload       = "hw_offload"        // the owner's flow_offloading_hw waits for it
	ReasonNoKernelSupport = "no_kernel_support" // no nft_flow_offload (kmod-nft-offload)
	ReasonUCIPending      = "uci_pending"       // uncommitted changes wait in /tmp/.uci
	ReasonCheckFailed     = "check_failed"      // fw4 refused the ruleset with it
	ReasonUnreadable      = "unreadable"        // what it is set in could not be read
	ReasonFailed          = "failed"            // the last run's change failed
	ReasonNoKernelModule  = "no_kernel_module"  // zram-swap, but no zram module for the running kernel
	ReasonNoCron          = "no_cron"           // no /etc/init.d/cron
	ReasonNoSystem        = "no_system"         // no system section in /etc/config/system
)

// CronLogLevel is busybox crond's level the tune sets: 9 logs its warnings
// and errors, not every job it starts (8, its default, does).
const CronLogLevel = "9"

// Profiles.
const (
	Lowmem   = "lowmem"
	Standard = "standard"
)

// LowmemKB is where a router stops being small: 384 MiB, the previous
// agent's line for its low-memory profile.
const LowmemKB = 384 * 1024

// What the backup says of the zram service.
const (
	ZramDisabled = "disabled" // it did not start at boot: the tune enabled it
	ZramStopped  = "stopped"  // it did, but its swap was not up: the tune started it
	ZramEnabled  = "enabled"
	ZramStarted  = "started"
)

type sysctlSpec struct{ id, key, target string }

var sysctlSpecs = []sysctlSpec{
	{ItemSwappiness, "vm.swappiness", "80"},
	{ItemVFSCachePressure, "vm.vfs_cache_pressure", "200"},
}

// Env is where the router keeps what the tune reads and changes; tests point
// it at a temp dir and a fake runner.
type Env struct {
	ProcDir    string // /proc
	SysctlDir  string // /etc/sysctl.d
	SysctlConf string // /etc/sysctl.conf
	OwnSysctl  string // the tune's own sysctl file
	Backup     string // what the tune changed, and what was there before
	InitDir    string // /etc/init.d
	RCDir      string // /etc/rc.d
	ConfigDir  string // /etc/config
	UCISaveDir string // uci's default save directory: every commit reads it
	Fw4        string // /sbin/fw4
	SysModule  string // /sys/module
	ModulesDir string // /lib/modules
	Lock       string // one run at a time
	// TmpDir and RunDir are where vctl's own leftovers are looked for
	// (leftovers.go); "" looks nowhere.
	TmpDir string // /tmp
	RunDir string // /var/run/vectra-controller-pro
	// Overlay and RootDir are only read, by Analyze.
	Overlay string // /overlay
	RootDir string // /root

	// Run runs a command; nil in an Env that only reads (Inspect).
	Run func(ctx context.Context, name string, args ...string) error
}

// RouterEnv is the production Env.
func RouterEnv() Env {
	return Env{
		ProcDir:    "/proc",
		SysctlDir:  "/etc/sysctl.d",
		SysctlConf: "/etc/sysctl.conf",
		OwnSysctl:  "/etc/sysctl.d/95-vectra-tune.conf",
		Backup:     "/etc/vectra-controller-pro/tune-backup.json",
		InitDir:    "/etc/init.d",
		RCDir:      "/etc/rc.d",
		ConfigDir:  "/etc/config",
		UCISaveDir: "/tmp/.uci",
		Fw4:        "/sbin/fw4",
		SysModule:  "/sys/module",
		ModulesDir: "/lib/modules",
		Lock:       "/var/lock/vectra-tune.lock",
		TmpDir:     "/tmp",
		RunDir:     "/var/run/vectra-controller-pro",
		Overlay:    "/overlay",
		RootDir:    "/root",
		Run:        runCommand,
	}
}

func runCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, msg)
		}
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

// Item is one thing the tune sets, as the router has it now.
type Item struct {
	ID    string `json:"id"`
	State string `json:"state"`
	// Value is what the router has now: the zram swap's size in MiB (nil:
	// none up), a sysctl's value, a UCI option's (nil: unset).
	Value *string `json:"value"`
	// Target is what the tune sets ("on" for zram).
	Target string `json:"target"`
	Reason string `json:"reason,omitempty"`
	// Source is the file an owner's own sysctl comes from.
	Source string `json:"source,omitempty"`
}

// Plan is what the tune finds and would do.
type Plan struct {
	// On: vectra-controller-pro.main.tune is not '0'.
	On          bool   `json:"on"`
	Profile     string `json:"profile"`
	MemTotalMiB int    `json:"memTotalMiB"`
	Cores       int    `json:"cores"`
	Items       []Item `json:"items"`
	// Analysis is where the router's memory and flash go (Analyze); only
	// `vctl tune plan` reads it, never the router UI's status.
	Analysis *Analysis `json:"analysis,omitempty"`
}

// Item is the plan's item id.
func (p Plan) Item(id string) (Item, bool) {
	for _, it := range p.Items {
		if it.ID == id {
			return it, true
		}
	}
	return Item{}, false
}

func (p *Plan) item(id string) *Item {
	for i := range p.Items {
		if p.Items[i].ID == id {
			return &p.Items[i]
		}
	}
	return nil
}

// Backup is what the tune changed, by item: kept until Undo.
type Backup struct {
	Version int              `json:"version"`
	Items   map[string]Saved `json:"items"`
	// Owner are the items the owner took over after the tune had set them,
	// where nothing on the router says so by itself: zram switched off again
	// is only an absent boot link. Never set again.
	Owner []string `json:"owner,omitempty"`
}

func (b Backup) owner(id string) bool {
	for _, o := range b.Owner {
		if o == id {
			return true
		}
	}
	return false
}

// Saved is one change: Key is what changed (a sysctl, a UCI option, "zram"),
// Was what it was before the tune's first change (nil: unset), Set what the
// tune set.
type Saved struct {
	Key string  `json:"key"`
	Was *string `json:"was"`
	Set string  `json:"set"`
}

// Change is one change a run made.
type Change struct {
	ID   string
	What string // a sysctl, a UCI option, "zram swap"
	From string
	To   string
}

func (c Change) String() string { return fmt.Sprintf("%s: %s -> %s", c.What, c.From, c.To) }

// Failure is a change a run could not make.
type Failure struct {
	ID  string
	Err string
}

func (f Failure) String() string { return f.ID + ": " + f.Err }

// Result is a run: the router as it is after it, what changed, what failed.
type Result struct {
	Plan    Plan
	Changes []Change
	Failed  []Failure
}

// ErrBusy is another run holding the lock past the caller's deadline.
var ErrBusy = errors.New("tune: another run is still going")

// setting is a sysctl as a file sets it.
type setting struct{ Value, File string }

// uciOption is one option of the first section of a type.
type uciOption struct {
	Readable bool    // the config parsed
	Section  string  // the section's ref: "globals", "@defaults[0]"; "" none
	Value    *string // nil: unset
}

// Facts is what the tune reads off the router. Reading them runs nothing.
type Facts struct {
	On            bool
	MemTotalKB    uint64
	Cores         int
	SwapSupport   bool   // /proc/swaps
	ZramInstalled bool   // /etc/init.d/zram
	ZramEnabled   bool   // its boot link
	ZramKB        uint64 // the zram swap up now; 0: none
	// ZramModule: the running kernel has zram (loaded, built in, or a module
	// of its own release); true when that cannot be told.
	ZramModule bool
	// Sysctl is the kernel's values of the tune's keys (absent: unreadable).
	Sysctl map[string]string
	// UserSysctl is what files other than vctl's set, by key: the one the
	// boot applies last.
	UserSysctl map[string]setting
	Network    uciOption // network.@globals[0].packet_steering
	Firewall   uciOption // firewall.@defaults[0].flow_offloading
	OffloadHW  string    // firewall.@defaults[0].flow_offloading_hw
	System     uciOption // system.@system[0].cronloglevel
	CronInit   bool      // /etc/init.d/cron
	// Leftovers are vctl's own leftovers found (leftovers.go); LeftoversRead
	// is false when the Env names nowhere to look.
	Leftovers          []Leftover
	LeftoversRead      bool
	PacketSteeringInit bool
	Fw4                bool
	OffloadModule      bool
	// Pending is the configs with changes waiting in uci's save directory.
	Pending map[string]bool
	Backup  Backup
}

// Detect reads the router. It never fails: what cannot be read is left empty.
func Detect(env Env) Facts {
	f := Facts{On: switchOn(filepath.Join(env.ConfigDir, "vectra-controller-pro")), Sysctl: map[string]string{},
		UserSysctl: map[string]setting{}, Pending: map[string]bool{}}
	if in, err := memguard.ReadFrom(filepath.Join(env.ProcDir, "meminfo")); err == nil {
		f.MemTotalKB = in.TotalKB
	}
	f.Cores = cores(filepath.Join(env.ProcDir, "cpuinfo"))
	if raw, err := os.ReadFile(filepath.Join(env.ProcDir, "swaps")); err == nil {
		f.SwapSupport = true
		f.ZramKB = zramKB(string(raw))
	}
	f.ZramInstalled = executable(filepath.Join(env.InitDir, "zram"))
	f.ZramEnabled = bootLink(env.RCDir, "zram")
	f.ZramModule = zramModule(env)
	f.Sysctl = liveSysctl(env)
	f.UserSysctl = userSysctl(env)

	f.Network, _ = firstOption(filepath.Join(env.ConfigDir, "network"), "globals", "packet_steering")
	var fw *uci.Section
	f.Firewall, fw = firstOption(filepath.Join(env.ConfigDir, "firewall"), "defaults", "flow_offloading")
	if fw != nil {
		f.OffloadHW = fw.Options["flow_offloading_hw"]
	}
	f.System, _ = firstOption(filepath.Join(env.ConfigDir, "system"), "system", "cronloglevel")
	f.CronInit = executable(filepath.Join(env.InitDir, "cron"))
	if env.TmpDir != "" || env.RunDir != "" {
		f.Leftovers, f.LeftoversRead = Leftovers(env, time.Now()), true
	}
	f.PacketSteeringInit = executable(filepath.Join(env.InitDir, "packet_steering"))
	f.Fw4 = executable(env.Fw4)
	f.OffloadModule = exists(filepath.Join(env.SysModule, "nft_flow_offload"))
	if !f.OffloadModule {
		ko, _ := filepath.Glob(filepath.Join(env.ModulesDir, "*", "nft_flow_offload.ko"))
		f.OffloadModule = len(ko) > 0
	}
	for _, cfg := range []string{"network", "firewall", "system"} {
		if st, err := os.Stat(filepath.Join(env.UCISaveDir, cfg)); err == nil && st.Size() > 0 {
			f.Pending[cfg] = true
		}
	}
	f.Backup = loadBackup(env.Backup)
	return f
}

// Inspect is the plan for the router as it is now. It only reads files.
func Inspect(env Env) Plan { return plan(Detect(env)) }

func plan(f Facts) Plan {
	p := Plan{On: f.On, Profile: Standard, MemTotalMiB: int(f.MemTotalKB / 1024), Cores: f.Cores}
	low := f.MemTotalKB > 0 && f.MemTotalKB < LowmemKB
	if low {
		p.Profile = Lowmem
	}
	p.Items = []Item{zramItem(f, low)}
	for _, s := range sysctlSpecs {
		p.Items = append(p.Items, sysctlItem(f, low, s))
	}
	p.Items = append(p.Items, steeringItem(f), offloadingItem(f), cronItem(f), leftoversItem(f))
	if !f.On {
		for i := range p.Items {
			if p.Items[i].State == Pending {
				p.Items[i].State, p.Items[i].Reason = Skipped, ReasonOff
			}
		}
	}
	return p
}

func (f Facts) ours(id string) bool {
	_, ok := f.Backup.Items[id]
	return ok
}

func appliedOr(ours bool) string {
	if ours {
		return Applied
	}
	return Already
}

func zramItem(f Facts, low bool) Item {
	it := Item{ID: ItemZram, Target: "on"}
	if f.ZramKB > 0 {
		v := strconv.FormatUint(f.ZramKB/1024, 10)
		it.Value = &v
	}
	switch {
	case !low:
		it.State, it.Reason = Skipped, ReasonEnoughRAM
	case !f.SwapSupport:
		it.State, it.Reason = Skipped, ReasonNoSwap
	case !f.ZramInstalled:
		it.State, it.Reason = Skipped, ReasonNotInstalled
	case f.ZramKB == 0 && !f.ZramModule:
		// zram-swap is there, and kmod-zram is not for the running kernel
		// (a kernel upgraded past its package): a start would fail at every
		// run. Said as it is, for the operator.
		it.State, it.Reason = Skipped, ReasonNoKernelModule
	case f.Backup.owner(ItemZram) && f.ZramEnabled && f.ZramKB > 0:
		it.State = Already
	case f.Backup.owner(ItemZram) || (f.ours(ItemZram) && !f.ZramEnabled):
		// Switched off after the tune switched it on: the owner's.
		it.State = UserSet
	case f.ZramEnabled && f.ZramKB > 0:
		it.State = appliedOr(f.ours(ItemZram))
	default:
		it.State = Pending
	}
	return it
}

func sysctlItem(f Facts, low bool, s sysctlSpec) Item {
	it := Item{ID: s.id, Target: s.target}
	live, readable := f.Sysctl[s.key]
	if readable {
		it.Value = &live
	}
	user, owned := f.UserSysctl[s.key]
	switch {
	case !low:
		it.State, it.Reason = Skipped, ReasonEnoughRAM
	case !readable:
		it.State, it.Reason = Skipped, ReasonUnreadable
	case owned:
		it.State, it.Source = UserSet, user.File
	case live == s.target:
		it.State = appliedOr(f.ours(s.id))
	default:
		it.State = Pending
	}
	return it
}

func steeringItem(f Facts) Item {
	it := Item{ID: ItemPacketSteering, Target: "1", Value: f.Network.Value}
	switch {
	case f.Cores < 2:
		it.State, it.Reason = Skipped, ReasonOneCore
	case !f.PacketSteeringInit:
		it.State, it.Reason = Skipped, ReasonNotSupported
	case !f.Network.Readable:
		it.State, it.Reason = Skipped, ReasonUnreadable
	case f.Network.Value != nil && (*f.Network.Value == "1" || *f.Network.Value == "2"):
		// '2' is every CPU: more than the tune sets, and on.
		it.State = appliedOr(f.ours(ItemPacketSteering))
	case f.Network.Value != nil:
		it.State = UserSet
	case f.Pending["network"]:
		it.State, it.Reason = Skipped, ReasonUCIPending
	default:
		it.State = Pending
	}
	return it
}

func offloadingItem(f Facts) Item {
	it := Item{ID: ItemFlowOffloading, Target: "1", Value: f.Firewall.Value}
	switch {
	case !f.Fw4:
		it.State, it.Reason = Skipped, ReasonNoFw4
	case !f.Firewall.Readable:
		it.State, it.Reason = Skipped, ReasonUnreadable
	case f.Firewall.Section == "":
		it.State, it.Reason = Skipped, ReasonNoDefaults
	case f.Firewall.Value != nil && boolOn(*f.Firewall.Value):
		it.State = appliedOr(f.ours(ItemFlowOffloading))
	case f.Firewall.Value != nil:
		it.State = UserSet
	case boolOn(f.OffloadHW):
		// fw4 offloads in hardware only with software offloading on: set
		// here, it would switch the owner's hardware flag on too.
		it.State, it.Reason = Skipped, ReasonHWOffload
	case !f.OffloadModule:
		// fw4 does not check: with the option on and no module, its ruleset
		// fails to load — at the next boot, no firewall and no NAT at all.
		it.State, it.Reason = Skipped, ReasonNoKernelSupport
	case f.Pending["firewall"]:
		it.State, it.Reason = Skipped, ReasonUCIPending
	default:
		it.State = Pending
	}
	return it
}

func cronItem(f Facts) Item {
	it := Item{ID: ItemCronLogLevel, Target: CronLogLevel, Value: f.System.Value}
	switch {
	case !f.CronInit:
		it.State, it.Reason = Skipped, ReasonNoCron
	case !f.System.Readable:
		it.State, it.Reason = Skipped, ReasonUnreadable
	case f.System.Section == "":
		it.State, it.Reason = Skipped, ReasonNoSystem
	case f.System.Value != nil && strings.TrimSpace(*f.System.Value) == CronLogLevel:
		it.State = appliedOr(f.ours(ItemCronLogLevel))
	case f.System.Value != nil:
		// Any level set is someone's choice — LuCI's «Cron Log Level» among
		// them.
		it.State = UserSet
	case f.Pending["system"]:
		it.State, it.Reason = Skipped, ReasonUCIPending
	default:
		it.State = Pending
	}
	return it
}

// leftoversItem: Value is the MiB of vctl's own leftovers found, nil when
// none. It is never the owner's — they are vctl's files — and never backed
// up: a removed leftover has nothing to put back.
func leftoversItem(f Facts) Item {
	it := Item{ID: ItemTmpLeftovers, Target: "0"}
	if !f.LeftoversRead {
		it.State, it.Reason = Skipped, ReasonUnreadable
		return it
	}
	if len(f.Leftovers) == 0 {
		it.State = Already
		return it
	}
	v := mib(leftoverBytes(f.Leftovers))
	it.Value, it.State = &v, Pending
	return it
}

// Apply sets what the plan finds pending, and backs every change up first.
// One run at a time: it waits for another until ctx ends (ErrBusy).
func Apply(ctx context.Context, env Env) (Result, error) {
	unlock, err := lock(ctx, env)
	if err != nil {
		return Result{}, err
	}
	defer unlock()

	f := Detect(env)
	p := plan(f)
	if !f.On {
		return Result{Plan: p}, nil
	}
	a := &applier{ctx: ctx, env: env, f: f, backup: f.Backup.clone(), outcome: map[string]string{}}
	// What the owner took over since the tune set it is theirs: forgotten,
	// and never put back. zram is remembered as theirs: nothing else on the
	// router would say so at the next run.
	for _, it := range p.Items {
		if it.State == UserSet && f.ours(it.ID) {
			if it.ID == ItemZram && !a.backup.owner(ItemZram) {
				a.backup.Owner = append(a.backup.Owner, ItemZram)
			}
			a.forget(it.ID)
		}
	}
	a.zram(p)
	a.sysctl(p)
	a.steering(p)
	a.offloading(p)
	a.cron(p)
	a.leftovers(p)

	res := Result{Plan: plan(Detect(env)), Changes: a.changes, Failed: a.failed}
	for id, reason := range a.outcome {
		it := res.Plan.item(id)
		switch {
		case it == nil:
		case reason == ReasonCheckFailed:
			it.State, it.Reason = Skipped, reason
		case it.State == Pending:
			it.Reason = reason
		}
	}
	return res, nil
}

type applier struct {
	ctx     context.Context
	env     Env
	f       Facts
	backup  Backup
	changes []Change
	failed  []Failure
	// outcome is why an item was not set this run (ReasonFailed,
	// ReasonCheckFailed).
	outcome map[string]string
}

func (a *applier) run(name string, args ...string) error { return a.env.Run(a.ctx, name, args...) }

func (a *applier) change(id, what, from, to string) {
	a.changes = append(a.changes, Change{ID: id, What: what, From: from, To: to})
}

func (a *applier) fail(id, reason string, err error) {
	a.failed = append(a.failed, Failure{ID: id, Err: err.Error()})
	a.outcome[id] = reason
}

// note backs a change up before it is made. An earlier entry keeps its Was:
// the value from before the tune's first change is the one to put back.
func (a *applier) note(id string, s Saved) bool {
	before := a.backup.clone()
	if old, ok := a.backup.Items[id]; ok {
		s.Was = old.Was
	}
	a.backup.Items[id] = s
	if !a.save() {
		a.backup = before
		return false
	}
	return true
}

func (a *applier) forget(id string) {
	if _, ok := a.backup.Items[id]; !ok {
		return
	}
	delete(a.backup.Items, id)
	a.save()
}

func (a *applier) save() bool {
	if err := saveBackup(a.env.Backup, a.backup); err != nil {
		a.failed = append(a.failed, Failure{ID: "backup", Err: err.Error()})
		return false
	}
	return true
}

func (a *applier) zram(p Plan) {
	if it, _ := p.Item(ItemZram); it.State != Pending {
		return
	}
	init := filepath.Join(a.env.InitDir, "zram")
	from := "off"
	if a.f.ZramKB > 0 {
		from = fmt.Sprintf("%d MiB, not started at boot", a.f.ZramKB/1024)
	}
	was, set := ZramStopped, ZramStarted
	if !a.f.ZramEnabled {
		was, set = ZramDisabled, ZramEnabled
	}
	_, earlier := a.backup.Items[ItemZram]
	if !a.note(ItemZram, Saved{Key: "zram", Was: &was, Set: set}) {
		return
	}
	if !a.f.ZramEnabled {
		if err := a.run(init, "enable"); err != nil {
			if !earlier {
				a.forget(ItemZram)
			}
			a.fail(ItemZram, ReasonFailed, err)
			return
		}
	}
	if a.f.ZramKB == 0 {
		err := a.run(init, "start")
		if raw, rerr := os.ReadFile(filepath.Join(a.env.ProcDir, "swaps")); err == nil && rerr == nil && zramKB(string(raw)) == 0 {
			err = errors.New("its swap is not up after the start")
		}
		if err != nil {
			// Enabled by the tune, it stays noted: the boot link is the tune's.
			if a.f.ZramEnabled && !earlier {
				a.forget(ItemZram)
			}
			a.fail(ItemZram, ReasonFailed, fmt.Errorf("the zram swap did not come up: %w", err))
			return
		}
	}
	kb := a.f.ZramKB
	if raw, err := os.ReadFile(filepath.Join(a.env.ProcDir, "swaps")); err == nil {
		kb = zramKB(string(raw))
	}
	a.change(ItemZram, "zram swap", from, fmt.Sprintf("%d MiB", kb/1024))
}

// sysctl sets the pending sysctls through the tune's own file, and keeps
// that file holding every value the tune set and still holds — the boot
// applies it again (/etc/init.d/sysctl), before /etc/sysctl.conf.
func (a *applier) sysctl(p Plan) {
	var pending []sysctlSpec
	for _, s := range sysctlSpecs {
		if it, _ := p.Item(s.id); it.State == Pending {
			was := a.f.Sysctl[s.key]
			if !a.note(s.id, Saved{Key: s.key, Was: &was, Set: s.target}) {
				continue
			}
			pending = append(pending, s)
		}
	}
	if err := writeOwnSysctl(a.env.OwnSysctl, a.backup); err != nil {
		for _, s := range pending {
			a.fail(s.id, ReasonFailed, err)
		}
		return
	}
	if len(pending) == 0 {
		return
	}
	err := a.run("sysctl", "-q", "-p", a.env.OwnSysctl)
	live := liveSysctl(a.env)
	for _, s := range pending {
		switch {
		case live[s.key] == s.target:
			a.change(s.id, s.key, a.f.Sysctl[s.key], s.target)
		case err != nil:
			a.fail(s.id, ReasonFailed, err)
		default:
			a.fail(s.id, ReasonFailed, fmt.Errorf("%s is %s after sysctl -p", s.key, live[s.key]))
		}
	}
}

func (a *applier) steering(p Plan) {
	if it, _ := p.Item(ItemPacketSteering); it.State != Pending {
		return
	}
	sec := a.f.Network.Section
	var cmds [][]string
	if sec == "" {
		// OpenWrt's own name for it (config_generate).
		sec = "globals"
		cmds = append(cmds, []string{"-q", "set", "network.globals=globals"})
	}
	opt := "network." + sec + ".packet_steering"
	cmds = append(cmds, []string{"-q", "set", opt + "=1"}, []string{"-q", "commit", "network"})
	if !a.note(ItemPacketSteering, Saved{Key: opt, Set: "1"}) {
		return
	}
	for _, c := range cmds {
		if err := a.run("uci", c...); err != nil {
			if revertErr := a.run("uci", "-q", "revert", "network"); revertErr != nil {
				a.fail(ItemPacketSteering, ReasonFailed, fmt.Errorf("%v; revert failed (backup kept): %w", err, revertErr))
				return
			}
			a.forget(ItemPacketSteering)
			a.fail(ItemPacketSteering, ReasonFailed, err)
			return
		}
	}
	a.change(ItemPacketSteering, opt, "unset", "1")
	// Committed: it applies at the next interface event or boot anyway.
	if err := a.run(filepath.Join(a.env.InitDir, "packet_steering"), "reload"); err != nil {
		a.failed = append(a.failed, Failure{ID: ItemPacketSteering, Err: "set; not applied until the next boot: " + err.Error()})
	}
}

// offloading switches software flow offloading on, and has fw4 reload.
//
// The reload is safe under vctl's traffic, and so is applied now rather than
// at the next boot:
//
//   - `/etc/init.d/firewall reload` is `fw4 reload`, which renders fw4's
//     ruleset and loads it with one `nft -f`, whose script starts `table inet
//     fw4` / `flush table inet fw4` (firewall4's templates/ruleset.uc): it
//     replaces its own table, in one transaction, and no other. Only `stop`
//     (`fw4 flush`) deletes every table — the tune never stops the firewall.
//   - vctl's data plane is a table of its own, `inet vctl`
//     (internal/firewall/nft.go: "One table `inet vctl` owns everything"),
//     with no rule in fw4's, and its fwmark policy route is an `ip rule`
//     that fw4 never touches. A reload leaves both as they are, and
//     conntrack keeps every connection.
//   - Offloading moves only FORWARDED connections to the flowtable (fw4's
//     forward chain: `meta l4proto { tcp, udp } flow offload @ft`). What
//     xray carries is TPROXY'd to the router itself (input), and xray's own
//     connections are output: neither is offloaded. What vctl sends out
//     directly (ct mark, the P2P guard) is decided on a connection's first
//     packet, before it is established — its later packets taking the fast
//     path change nothing. The one thing it thins is the shadow count of a
//     leak (vctl_would_leak): past xray, only a leaking connection's first
//     packets are counted.
//
// fw4 does not check the kernel: with the option on and no flowtable
// support its ruleset fails to load, and at the next boot the router would
// have no firewall and no NAT. So the module is required (offloadingItem),
// and `fw4 check` must pass with the option committed — else it is taken
// back before anything reloads.
func (a *applier) offloading(p Plan) {
	if it, _ := p.Item(ItemFlowOffloading); it.State != Pending {
		return
	}
	opt := "firewall." + a.f.Firewall.Section + ".flow_offloading"
	if !a.note(ItemFlowOffloading, Saved{Key: opt, Set: "1"}) {
		return
	}
	for _, c := range [][]string{{"-q", "set", opt + "=1"}, {"-q", "commit", "firewall"}} {
		if err := a.run("uci", c...); err != nil {
			if revertErr := a.run("uci", "-q", "revert", "firewall"); revertErr != nil {
				a.fail(ItemFlowOffloading, ReasonFailed, fmt.Errorf("%v; revert failed (backup kept): %w", err, revertErr))
				return
			}
			a.forget(ItemFlowOffloading)
			a.fail(ItemFlowOffloading, ReasonFailed, err)
			return
		}
	}
	if err := a.run(a.env.Fw4, "-q", "check"); err != nil {
		rollbackErr := a.run("uci", "-q", "delete", opt)
		if rollbackErr == nil {
			rollbackErr = a.run("uci", "-q", "commit", "firewall")
		}
		if rollbackErr != nil {
			a.fail(ItemFlowOffloading, ReasonCheckFailed, fmt.Errorf("fw4 refuses its ruleset: %v; rollback failed (backup kept): %w", err, rollbackErr))
			return
		}
		a.forget(ItemFlowOffloading)
		a.fail(ItemFlowOffloading, ReasonCheckFailed, fmt.Errorf("fw4 refuses its ruleset with flow offloading on; taken back: %w", err))
		return
	}
	a.change(ItemFlowOffloading, opt, "unset", "1")
	if err := a.run(filepath.Join(a.env.InitDir, "firewall"), "reload"); err != nil {
		a.failed = append(a.failed, Failure{ID: ItemFlowOffloading, Err: "set; not applied until the next boot: " + err.Error()})
	}
}

// cron sets busybox crond's log level, and has cron reload: procd starts
// crond again with the new -l (its command line changed); nothing else on
// the router restarts.
func (a *applier) cron(p Plan) {
	if it, _ := p.Item(ItemCronLogLevel); it.State != Pending {
		return
	}
	opt := "system." + a.f.System.Section + ".cronloglevel"
	if !a.note(ItemCronLogLevel, Saved{Key: opt, Set: CronLogLevel}) {
		return
	}
	for _, c := range [][]string{{"-q", "set", opt + "=" + CronLogLevel}, {"-q", "commit", "system"}} {
		if err := a.run("uci", c...); err != nil {
			if revertErr := a.run("uci", "-q", "revert", "system"); revertErr != nil {
				a.fail(ItemCronLogLevel, ReasonFailed, fmt.Errorf("%v; revert failed (backup kept): %w", err, revertErr))
				return
			}
			a.forget(ItemCronLogLevel)
			a.fail(ItemCronLogLevel, ReasonFailed, err)
			return
		}
	}
	a.change(ItemCronLogLevel, opt, "unset", CronLogLevel)
	if err := a.run(filepath.Join(a.env.InitDir, "cron"), "reload"); err != nil {
		a.failed = append(a.failed, Failure{ID: ItemCronLogLevel, Err: "set; not applied until the next boot: " + err.Error()})
	}
}

// leftovers removes vctl's own leftovers found (leftovers.go): one change
// for all of them, with the MiB freed.
func (a *applier) leftovers(p Plan) {
	if it, _ := p.Item(ItemTmpLeftovers); it.State != Pending {
		return
	}
	removed, err := RemoveLeftovers(a.env, time.Now())
	if len(removed) > 0 {
		a.change(ItemTmpLeftovers, "vctl's leftovers in RAM", fmt.Sprintf("%d file(s), %s MiB", len(removed), mib(leftoverBytes(removed))), "removed")
	}
	if err != nil {
		a.fail(ItemTmpLeftovers, ReasonFailed, err)
	}
}

// Undo puts back what the tune changed and is still as it left it — what
// was changed since is left alone — and takes its own sysctl file and the
// backup away. The zram swap is not switched off under a running router
// (swapoff needs every swapped page back in RAM): its boot link goes, the
// swap with the next boot.
func Undo(ctx context.Context, env Env) (Result, error) {
	unlock, err := lock(ctx, env)
	if err != nil {
		return Result{}, err
	}
	defer unlock()

	f := Detect(env)
	a := &applier{ctx: ctx, env: env, f: f, backup: f.Backup.clone(), outcome: map[string]string{}}
	left := map[string]Saved{}
	done := func(id string, err error) {
		if err != nil {
			a.failed = append(a.failed, Failure{ID: id, Err: err.Error()})
			left[id] = a.backup.Items[id]
		}
	}

	if s, ok := a.backup.Items[ItemZram]; ok && s.Was != nil && *s.Was == ZramDisabled && f.ZramEnabled {
		err := a.run(filepath.Join(env.InitDir, "zram"), "disable")
		if err == nil {
			a.change(ItemZram, "zram swap at boot", "on", "off")
		}
		done(ItemZram, err)
	}

	if err := os.Remove(env.OwnSysctl); err != nil && !os.IsNotExist(err) {
		a.failed = append(a.failed, Failure{ID: "sysctl", Err: err.Error()})
	}
	for _, sp := range sysctlSpecs {
		s, ok := a.backup.Items[sp.id]
		if !ok || s.Was == nil || *s.Was == "" || f.Sysctl[sp.key] != s.Set {
			continue
		}
		err := a.run("sysctl", "-q", "-w", sp.key+"="+*s.Was)
		if err == nil {
			a.change(sp.id, sp.key, s.Set, *s.Was)
		}
		done(sp.id, err)
	}

	for _, u := range []struct {
		id, config, service string
		now                 uciOption
	}{
		{ItemPacketSteering, "network", "packet_steering", f.Network},
		{ItemFlowOffloading, "firewall", "firewall", f.Firewall},
		{ItemCronLogLevel, "system", "cron", f.System},
	} {
		s, ok := a.backup.Items[u.id]
		if !ok {
			continue
		}
		if f.Pending[u.config] {
			done(u.id, fmt.Errorf("uncommitted changes wait in uci (uci changes %s): %s left as it is", u.config, s.Key))
			continue
		}
		if u.now.Value == nil || *u.now.Value != s.Set {
			continue
		}
		restore := []string{"-q", "delete", s.Key}
		was := "unset"
		if s.Was != nil {
			restore, was = []string{"-q", "set", s.Key + "=" + *s.Was}, *s.Was
		}
		err := a.run("uci", restore...)
		if err == nil {
			err = a.run("uci", "-q", "commit", u.config)
		}
		if err != nil {
			_ = a.run("uci", "-q", "revert", u.config)
			done(u.id, err)
			continue
		}
		a.change(u.id, s.Key, s.Set, was)
		if err := a.run(filepath.Join(env.InitDir, u.service), "reload"); err != nil {
			a.failed = append(a.failed, Failure{ID: u.id, Err: "put back; applied at the next boot: " + err.Error()})
		}
	}

	if err := saveBackup(env.Backup, Backup{Items: left}); err != nil {
		a.failed = append(a.failed, Failure{ID: "backup", Err: err.Error()})
	}
	return Result{Plan: plan(Detect(env)), Changes: a.changes, Failed: a.failed}, nil
}

// lock takes the tune's lock, waiting for another run until ctx ends.
func lock(ctx context.Context, env Env) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(env.Lock), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(env.Lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ErrBusy
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (b Backup) clone() Backup {
	out := Backup{Version: 1, Items: map[string]Saved{}, Owner: append([]string(nil), b.Owner...)}
	for k, v := range b.Items {
		out.Items[k] = v
	}
	return out
}

func loadBackup(path string) Backup {
	b := Backup{Version: 1, Items: map[string]Saved{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		return b
	}
	var got Backup
	if json.Unmarshal(raw, &got) == nil {
		if got.Items != nil {
			b.Items = got.Items
		}
		b.Owner = got.Owner
	}
	return b
}

// saveBackup writes the backup whole (0600: root's alone), or removes it when
// nothing is left in it.
func saveBackup(path string, b Backup) error {
	if len(b.Items) == 0 && len(b.Owner) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	b.Version = 1
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(path, append(raw, '\n'), 0o600)
}

// writeOwnSysctl makes the tune's sysctl file hold the values it set and
// still holds (by the backup), or removes it when there are none. Unchanged,
// it is not written again.
func writeOwnSysctl(path string, b Backup) error {
	var lines []string
	for _, s := range sysctlSpecs {
		if saved, ok := b.Items[s.id]; ok {
			lines = append(lines, saved.Key+"="+saved.Set)
		}
	}
	if len(lines) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	body := "# vectra-controller-pro: the router's tune (`vctl tune`). A value set in a file of\n" +
		"# your own or in /etc/sysctl.conf wins, and the tune leaves it from then on;\n" +
		"# `vctl tune undo` puts back what was here before.\n" + strings.Join(lines, "\n") + "\n"
	if old, err := os.ReadFile(path); err == nil && string(old) == body {
		return nil
	}
	return writeFile(path, []byte(body), 0o644)
}

func writeFile(path string, body []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// switchOn reads vectra-controller-pro.main.tune: on unless set to a false word.
func switchOn(path string) bool {
	f, err := uci.Load(path)
	if err != nil {
		return true
	}
	if main := f.Named("main"); main != nil {
		if v, ok := main.Options["tune"]; ok {
			return !boolOff(v)
		}
	}
	return true
}

// boolOn and boolOff read a UCI boolean as fw4 and uci's own tools do.
func boolOn(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "yes", "on", "true", "enabled":
		return true
	}
	return false
}

func boolOff(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "no", "off", "false", "disabled":
		return true
	}
	return false
}

// cores counts the processors /proc/cpuinfo lists.
func cores(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if k, _, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "processor" {
			n++
		}
	}
	return n
}

// zramKB is the size of the zram swaps /proc/swaps lists, in KiB.
func zramKB(swaps string) uint64 {
	var kb uint64
	sc := bufio.NewScanner(strings.NewReader(swaps))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 3 && strings.HasPrefix(f[0], "/dev/zram") {
			if v, err := strconv.ParseUint(f[2], 10, 64); err == nil {
				kb += v
			}
		}
	}
	return kb
}

func liveSysctl(env Env) map[string]string {
	out := map[string]string{}
	for _, s := range sysctlSpecs {
		if raw, err := os.ReadFile(filepath.Join(env.ProcDir, "sys", strings.ReplaceAll(s.key, ".", "/"))); err == nil {
			out[s.key] = strings.TrimSpace(string(raw))
		}
	}
	return out
}

// userSysctl is what the sysctl files other than vctl's own set, as the boot
// applies them (/etc/init.d/sysctl): /etc/sysctl.d/*.conf in order, then
// /etc/sysctl.conf — the last one to set a key wins. vctl's files (and the
// previous Vectra agent's) are not the owner's.
func userSysctl(env Env) map[string]setting {
	files, _ := filepath.Glob(filepath.Join(env.SysctlDir, "*.conf"))
	sort.Strings(files)
	files = append(files, env.SysctlConf)
	out := map[string]setting{}
	for _, file := range files {
		if file == env.OwnSysctl || strings.Contains(filepath.Base(file), "vectra") {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		for k, v := range parseSysctl(string(raw)) {
			out[k] = setting{Value: v, File: file}
		}
	}
	return out
}

// parseSysctl reads a sysctl file as busybox's sysctl -p does: key = value,
// '#' and ';' comments, a key's '/' as '.'.
func parseSysctl(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.ReplaceAll(strings.TrimPrefix(strings.TrimSpace(k), "-"), "/", ".")
		if k == "" {
			continue
		}
		out[k] = strings.TrimSpace(v)
	}
	return out
}

// firstOption reads one option of the first section of a type, and returns
// that section (nil: none, or the config does not parse).
func firstOption(path, typ, opt string) (uciOption, *uci.Section) {
	f, err := uci.Load(path)
	if err != nil {
		return uciOption{}, nil
	}
	o := uciOption{Readable: true}
	secs := f.OfType(typ)
	if len(secs) == 0 {
		return o, nil
	}
	o.Section = secs[0].Ref()
	if v, ok := secs[0].Options[opt]; ok {
		o.Value = &v
	}
	return o, &secs[0]
}

// bootLink: the service starts at boot (its rc.d link, S<nn><name>).
func bootLink(rcDir, name string) bool {
	m, _ := filepath.Glob(filepath.Join(rcDir, "S??"+name))
	return len(m) > 0
}

func executable(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir() && st.Mode().Perm()&0o111 != 0
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// zramModule: the running kernel has zram — loaded (/sys/module/zram), built
// in, or a module of its own release under /lib/modules. When the running
// release cannot be read it says true: nothing is concluded from nothing.
func zramModule(env Env) bool {
	if exists(filepath.Join(env.SysModule, "zram")) {
		return true
	}
	raw, err := os.ReadFile(filepath.Join(env.ProcDir, "sys", "kernel", "osrelease"))
	release := strings.TrimSpace(string(raw))
	if err != nil || release == "" || strings.ContainsAny(release, `/\`) {
		return true
	}
	dir := filepath.Join(env.ModulesDir, release)
	if exists(filepath.Join(dir, "zram.ko")) {
		return true
	}
	if b, err := os.ReadFile(filepath.Join(dir, "modules.builtin")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if filepath.Base(strings.TrimSpace(line)) == "zram.ko" {
				return true
			}
		}
	}
	return false
}

// mib is bytes in MiB, one decimal.
func mib(b int64) string { return strconv.FormatFloat(float64(b)/(1<<20), 'f', 1, 64) }
