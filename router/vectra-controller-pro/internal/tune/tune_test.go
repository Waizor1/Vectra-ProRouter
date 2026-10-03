package tune

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/uci"
)

// router is a router's filesystem in a temp dir, and a runner that does to
// it what the router's own commands would: uci stages and commits into the
// config files, sysctl writes /proc/sys, the zram init links and starts.
// Every call is noted.
type router struct {
	t      *testing.T
	dir    string
	env    Env
	calls  []string
	staged map[string][]string // config -> "set a.b.c=v" / "delete a.b.c", uncommitted
	fail   map[string]error    // a call (as noted) that fails
	// zramKB is the size the zram init gives its swap when it starts.
	zramKB int
	// startsZram: its start brings the swap up (a router whose kernel has no
	// zram device fails it).
	startsZram bool
}

// newRouter is an AX3000T as a clean vctl install finds it: 234 MiB, two
// cores, zram-swap installed and off, the kernel's defaults, packet steering
// and flow offloading never set, fw4 with the offload module, crond at its
// default level, nothing of vctl's left in /tmp.
func newRouter(t *testing.T) *router {
	t.Helper()
	dir := t.TempDir()
	r := &router{t: t, dir: dir, staged: map[string][]string{}, fail: map[string]error{}, zramKB: 119896, startsZram: true}
	r.env = Env{
		ProcDir:    filepath.Join(dir, "proc"),
		SysctlDir:  filepath.Join(dir, "etc/sysctl.d"),
		SysctlConf: filepath.Join(dir, "etc/sysctl.conf"),
		OwnSysctl:  filepath.Join(dir, "etc/sysctl.d/95-vectra-tune.conf"),
		Backup:     filepath.Join(dir, "etc/vectra-controller-pro/tune-backup.json"),
		InitDir:    filepath.Join(dir, "etc/init.d"),
		RCDir:      filepath.Join(dir, "etc/rc.d"),
		ConfigDir:  filepath.Join(dir, "etc/config"),
		UCISaveDir: filepath.Join(dir, "tmp/.uci"),
		Fw4:        filepath.Join(dir, "sbin/fw4"),
		SysModule:  filepath.Join(dir, "sys/module"),
		ModulesDir: filepath.Join(dir, "lib/modules"),
		Lock:       filepath.Join(dir, "var/lock/vectra-tune.lock"),
		TmpDir:     filepath.Join(dir, "tmp"),
		RunDir:     filepath.Join(dir, "var/run/vectra-controller-pro"),
		Overlay:    filepath.Join(dir, "overlay"),
		RootDir:    filepath.Join(dir, "root"),
	}
	r.env.Run = r.run
	r.write("proc/meminfo", "MemTotal:         239792 kB\nMemFree:           60000 kB\nMemAvailable:      90000 kB\nSwapTotal:             0 kB\nSwapFree:              0 kB\n")
	r.write("proc/cpuinfo", "processor\t: 0\nBogoMIPS\t: 26.00\n\nprocessor\t: 1\nBogoMIPS\t: 26.00\n")
	r.write("proc/swaps", "Filename\t\t\t\tType\t\tSize\t\tUsed\t\tPriority\n")
	r.write("proc/sys/vm/swappiness", "60\n")
	r.write("proc/sys/vm/vfs_cache_pressure", "100\n")
	r.write("etc/sysctl.d/10-default.conf", "kernel.panic=3\nnet.ipv4.conf.default.arp_ignore=1\n")
	r.write("etc/sysctl.d/90-vectra-controller-pro.conf", "net.netfilter.nf_conntrack_tcp_timeout_time_wait=30\n")
	r.exec("etc/init.d/zram")
	r.exec("etc/init.d/packet_steering")
	r.exec("etc/init.d/firewall")
	r.exec("sbin/fw4")
	r.write("sys/module/nft_flow_offload/refcnt", "1\n")
	r.write("etc/config/network", "config interface 'loopback'\n\toption device 'lo'\n\nconfig globals 'globals'\n\toption ula_prefix 'auto'\n")
	r.write("etc/config/firewall", "config defaults\n\toption syn_flood '1'\n\toption input 'REJECT'\n\toption forward 'REJECT'\n\nconfig zone\n\toption name 'lan'\n")
	r.write("etc/config/vectra-controller-pro", "config controller 'main'\n\toption enabled '1'\n")
	r.write("etc/config/system", "config system\n\toption hostname 'OpenWrt'\n\toption log_size '64'\n")
	r.exec("etc/init.d/cron")
	for _, d := range []string{"tmp", "var/run/vectra-controller-pro", "overlay", "root"} {
		if err := os.MkdirAll(r.path(d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func (r *router) path(rel string) string { return filepath.Join(r.dir, rel) }

func (r *router) write(rel, body string) {
	r.t.Helper()
	p := r.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *router) exec(rel string) {
	r.t.Helper()
	r.write(rel, "#!/bin/sh\n")
	if err := os.Chmod(r.path(rel), 0o755); err != nil {
		r.t.Fatal(err)
	}
}

func (r *router) read(rel string) string {
	b, err := os.ReadFile(r.path(rel))
	if err != nil {
		return ""
	}
	return string(b)
}

func (r *router) exists(rel string) bool {
	_, err := os.Lstat(r.path(rel))
	return err == nil
}

// zramOn is zram-swap enabled at boot and its swap up, as its postinst leaves it.
func (r *router) zramOn() {
	r.t.Helper()
	r.link("etc/rc.d/S15zram", "../init.d/zram")
	r.write("proc/swaps", "Filename\t\t\t\tType\t\tSize\t\tUsed\t\tPriority\n/dev/zram0                              partition\t119896\t\t0\t\t100\n")
}

func (r *router) link(rel, target string) {
	r.t.Helper()
	p := r.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		r.t.Fatal(err)
	}
}

// run is the router's commands, as far as the tune uses them.
func (r *router) run(_ context.Context, name string, args ...string) error {
	name = strings.TrimPrefix(name, r.dir)
	call := strings.TrimSpace(name + " " + strings.Join(args, " "))
	r.calls = append(r.calls, call)
	if err := r.fail[call]; err != nil {
		return err
	}
	switch name {
	case "/etc/init.d/zram":
		switch args[0] {
		case "enable":
			r.link("etc/rc.d/S15zram", "../init.d/zram")
		case "disable":
			_ = os.Remove(r.path("etc/rc.d/S15zram"))
		case "start":
			if !r.startsZram {
				return errors.New("exit status 1")
			}
			r.write("proc/swaps", fmt.Sprintf("Filename\tType\tSize\tUsed\tPriority\n/dev/zram0 partition\t%d\t0\t100\n", r.zramKB))
		}
	case "sysctl":
		switch args[1] {
		case "-p":
			raw, err := os.ReadFile(args[2])
			if err != nil {
				return err
			}
			for _, line := range strings.Split(string(raw), "\n") {
				if k, v, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(strings.TrimSpace(line), "#") {
					r.write("proc/sys/"+strings.ReplaceAll(strings.TrimSpace(k), ".", "/"), strings.TrimSpace(v)+"\n")
				}
			}
		case "-w":
			k, v, _ := strings.Cut(args[2], "=")
			r.write("proc/sys/"+strings.ReplaceAll(k, ".", "/"), v+"\n")
		}
	case "uci":
		// uci -q set|delete|commit …
		verb, arg := args[1], args[2]
		cfg, _, _ := strings.Cut(arg, ".")
		switch verb {
		case "set", "delete":
			r.staged[cfg] = append(r.staged[cfg], verb+" "+arg)
		case "commit":
			r.commit(arg)
		}
	}
	return nil
}

// commit writes a config's staged changes into its file.
func (r *router) commit(cfg string) {
	r.t.Helper()
	path := "etc/config/" + cfg
	f, err := uci.Parse(r.read(path))
	if err != nil {
		r.t.Fatal(err)
	}
	for _, ch := range r.staged[cfg] {
		verb, arg, _ := strings.Cut(ch, " ")
		lhs, value, _ := strings.Cut(arg, "=")
		parts := strings.Split(lhs, ".")
		sec := section(f, parts[1])
		switch {
		case verb == "set" && len(parts) == 2:
			if sec == nil {
				f.Sections = append(f.Sections, uci.Section{Type: value, Name: parts[1], Options: map[string]string{}, Lists: map[string][]string{}})
			}
		case verb == "set":
			sec.Options[parts[2]] = value
		case verb == "delete":
			delete(sec.Options, parts[2])
		}
	}
	delete(r.staged, cfg)
	r.write(path, render(f))
}

// section is what uci would address: @type[i] or a name.
func section(f *uci.File, ref string) *uci.Section {
	for i := range f.Sections {
		if f.Sections[i].Ref() == ref {
			return &f.Sections[i]
		}
	}
	return nil
}

func render(f *uci.File) string {
	var b strings.Builder
	for _, s := range f.Sections {
		if s.Name != "" {
			fmt.Fprintf(&b, "config %s '%s'\n", s.Type, s.Name)
		} else {
			fmt.Fprintf(&b, "config %s\n", s.Type)
		}
		keys := make([]string, 0, len(s.Options))
		for k := range s.Options {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "\toption %s '%s'\n", k, s.Options[k])
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (r *router) option(cfg, typ, opt string) (string, bool) {
	r.t.Helper()
	f, err := uci.Parse(r.read("etc/config/" + cfg))
	if err != nil {
		r.t.Fatal(err)
	}
	for _, s := range f.OfType(typ) {
		v, ok := s.Options[opt]
		return v, ok
	}
	return "", false
}

func (r *router) apply() Result {
	r.t.Helper()
	res, err := Apply(context.Background(), r.env)
	if err != nil {
		r.t.Fatal(err)
	}
	return res
}

func (r *router) undo() Result {
	r.t.Helper()
	res, err := Undo(context.Background(), r.env)
	if err != nil {
		r.t.Fatal(err)
	}
	return res
}

func (r *router) backup() Backup {
	r.t.Helper()
	var b Backup
	raw, err := os.ReadFile(r.env.Backup)
	if err != nil {
		r.t.Fatalf("no backup: %v", err)
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		r.t.Fatal(err)
	}
	return b
}

// states is id -> state (and reason, when there is one).
func states(p Plan) map[string]string {
	out := map[string]string{}
	for _, it := range p.Items {
		s := it.State
		if it.Reason != "" {
			s += "/" + it.Reason
		}
		out[it.ID] = s
	}
	return out
}

func value(p Plan, id string) string {
	for _, it := range p.Items {
		if it.ID == id {
			if it.Value == nil {
				return "<nil>"
			}
			return *it.Value
		}
	}
	return "<missing>"
}

func containsCall(calls []string, want string) bool {
	for _, c := range calls {
		if c == want {
			return true
		}
	}
	return false
}

func str(s string) *string { return &s }

// A clean install on a 234 MB router: everything is set, in this order, each
// change backed up first, and a second run changes nothing.
func TestALowmemRouterGetsTheWholeTune(t *testing.T) {
	r := newRouter(t)
	p := Inspect(r.env)
	if p.Profile != Lowmem || !p.On || p.MemTotalMiB != 234 || p.Cores != 2 {
		t.Fatalf("plan: %+v", p)
	}
	want := map[string]string{ItemZram: Pending, ItemSwappiness: Pending, ItemVFSCachePressure: Pending, ItemPacketSteering: Pending, ItemFlowOffloading: Pending,
		ItemCronLogLevel: Pending, ItemTmpLeftovers: Already}
	if got := states(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("before: %v", got)
	}
	if len(r.calls) != 0 {
		t.Fatalf("Inspect ran %v", r.calls)
	}

	res := r.apply()
	wantCalls := []string{
		"/etc/init.d/zram enable",
		"/etc/init.d/zram start",
		"sysctl -q -p " + r.env.OwnSysctl,
		"uci -q set network.globals.packet_steering=1",
		"uci -q commit network",
		"/etc/init.d/packet_steering reload",
		"uci -q set firewall.@defaults[0].flow_offloading=1",
		"uci -q commit firewall",
		"/sbin/fw4 -q check",
		"/etc/init.d/firewall reload",
		"uci -q set system.@system[0].cronloglevel=9",
		"uci -q commit system",
		"/etc/init.d/cron reload",
	}
	if !reflect.DeepEqual(r.calls, wantCalls) {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(r.calls, "\n"), strings.Join(wantCalls, "\n"))
	}
	all := map[string]string{ItemZram: Applied, ItemSwappiness: Applied, ItemVFSCachePressure: Applied, ItemPacketSteering: Applied, ItemFlowOffloading: Applied,
		ItemCronLogLevel: Applied, ItemTmpLeftovers: Already}
	if got := states(res.Plan); !reflect.DeepEqual(got, all) {
		t.Fatalf("after: %v", got)
	}
	if value(res.Plan, ItemZram) != "117" || value(res.Plan, ItemSwappiness) != "80" || value(res.Plan, ItemVFSCachePressure) != "200" {
		t.Fatalf("values: %+v", res.Plan.Items)
	}
	var lines []string
	for _, c := range res.Changes {
		lines = append(lines, c.String())
	}
	wantLines := []string{
		"zram swap: off -> 117 MiB",
		"vm.swappiness: 60 -> 80",
		"vm.vfs_cache_pressure: 100 -> 200",
		"network.globals.packet_steering: unset -> 1",
		"firewall.@defaults[0].flow_offloading: unset -> 1",
		"system.@system[0].cronloglevel: unset -> 9",
	}
	if !reflect.DeepEqual(lines, wantLines) || len(res.Failed) != 0 {
		t.Fatalf("changes:\n%s\nfailed: %v", strings.Join(lines, "\n"), res.Failed)
	}

	// Every value it changed is backed up, readable by root alone.
	st, err := os.Stat(r.env.Backup)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("backup: %v %v", st, err)
	}
	b := r.backup()
	wantBackup := map[string]Saved{
		ItemZram:             {Key: "zram", Was: str(ZramDisabled), Set: ZramEnabled},
		ItemSwappiness:       {Key: "vm.swappiness", Was: str("60"), Set: "80"},
		ItemVFSCachePressure: {Key: "vm.vfs_cache_pressure", Was: str("100"), Set: "200"},
		ItemPacketSteering:   {Key: "network.globals.packet_steering", Set: "1"},
		ItemFlowOffloading:   {Key: "firewall.@defaults[0].flow_offloading", Set: "1"},
		ItemCronLogLevel:     {Key: "system.@system[0].cronloglevel", Set: "9"},
	}
	if !reflect.DeepEqual(b.Items, wantBackup) {
		t.Fatalf("backup: %+v", b.Items)
	}
	if got := r.read("etc/sysctl.d/95-vectra-tune.conf"); !strings.Contains(got, "vm.swappiness=80\n") || !strings.Contains(got, "vm.vfs_cache_pressure=200\n") ||
		strings.Contains(got, "min_free_kbytes") {
		t.Fatalf("own sysctl file:\n%s", got)
	}
	if v, _ := r.option("network", "globals", "packet_steering"); v != "1" {
		t.Fatalf("packet_steering = %q", v)
	}
	if v, _ := r.option("firewall", "defaults", "flow_offloading"); v != "1" {
		t.Fatalf("flow_offloading = %q", v)
	}
	if _, hw := r.option("firewall", "defaults", "flow_offloading_hw"); hw {
		t.Fatal("hardware offloading was set")
	}

	// Idempotent: read first, change only what differs.
	r.calls = nil
	res = r.apply()
	if len(r.calls) != 0 || len(res.Changes) != 0 {
		t.Fatalf("a second run: calls %v, changes %v", r.calls, res.Changes)
	}
	if got := states(res.Plan); !reflect.DeepEqual(got, all) {
		t.Fatalf("a second run: %v", got)
	}
}

// What was so before vctl is "already", and nothing of it is backed up: the
// previous agent's zram and sysctl file, a router someone tuned by hand.
func TestWhatWasAlreadySoIsLeftAndNotBackedUp(t *testing.T) {
	r := newRouter(t)
	r.zramOn()
	r.write("etc/sysctl.d/99-vectra-lowmem.conf", "vm.min_free_kbytes = 24576\nvm.swappiness = 80\nvm.vfs_cache_pressure = 200\n")
	r.write("proc/sys/vm/swappiness", "80\n")
	r.write("proc/sys/vm/vfs_cache_pressure", "200\n")
	r.write("etc/config/network", "config globals 'globals'\n\toption packet_steering '2'\n")
	r.write("etc/config/firewall", "config defaults\n\toption flow_offloading 'yes'\n")
	r.write("etc/config/system", "config system\n\toption cronloglevel '9'\n")
	res := r.apply()
	want := map[string]string{ItemZram: Already, ItemSwappiness: Already, ItemVFSCachePressure: Already, ItemPacketSteering: Already, ItemFlowOffloading: Already,
		ItemCronLogLevel: Already, ItemTmpLeftovers: Already}
	if got := states(res.Plan); !reflect.DeepEqual(got, want) {
		t.Fatalf("states: %v", got)
	}
	if len(r.calls) != 0 || r.exists("etc/vectra-controller-pro/tune-backup.json") || r.exists("etc/sysctl.d/95-vectra-tune.conf") {
		t.Fatalf("calls %v; backup %v; own file %v", r.calls, r.exists("etc/vectra-controller-pro/tune-backup.json"), r.exists("etc/sysctl.d/95-vectra-tune.conf"))
	}
}

// An explicit choice of the owner's stays: packet steering or offloading
// switched off, a sysctl set in a file of their own.
func TestTheOwnersExplicitChoicesStay(t *testing.T) {
	r := newRouter(t)
	r.write("etc/config/network", "config globals 'globals'\n\toption packet_steering '0'\n")
	r.write("etc/config/firewall", "config defaults\n\toption flow_offloading '0'\n")
	r.write("etc/sysctl.conf", "# mine\nvm.swappiness = 10\n")
	r.write("proc/sys/vm/swappiness", "10\n")
	r.write("etc/sysctl.d/50-mine.conf", "vm/vfs_cache_pressure=50\n")
	// LuCI's «Cron Log Level» left at «Normal» is a level set, too.
	r.write("etc/config/system", "config system\n\toption cronloglevel '8'\n")
	p := Inspect(r.env)
	want := map[string]string{ItemZram: Pending, ItemSwappiness: UserSet, ItemVFSCachePressure: UserSet, ItemPacketSteering: UserSet, ItemFlowOffloading: UserSet,
		ItemCronLogLevel: UserSet, ItemTmpLeftovers: Already}
	if got := states(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("states: %v", got)
	}
	for _, it := range p.Items {
		if it.ID == ItemSwappiness && (it.Source != r.env.SysctlConf || it.Value == nil || *it.Value != "10") {
			t.Fatalf("swappiness: %+v", it)
		}
	}
	r.apply()
	for _, c := range r.calls {
		if strings.Contains(c, "uci") || strings.Contains(c, "sysctl") || strings.Contains(c, "firewall") || strings.Contains(c, "packet_steering") || strings.Contains(c, "cron") {
			t.Fatalf("touched an owner's choice: %v", r.calls)
		}
	}
	if got := r.read("etc/sysctl.d/95-vectra-tune.conf"); got != "" {
		t.Fatalf("own sysctl file:\n%s", got)
	}
}

// A choice the owner made after the tune set a value is theirs from then on:
// the tune forgets it, and never puts its old value back.
func TestAValueTheOwnerSetSinceIsTheirs(t *testing.T) {
	r := newRouter(t)
	r.apply()
	r.write("etc/sysctl.conf", "vm.swappiness = 10\n")
	r.write("etc/config/firewall", "config defaults\n\toption flow_offloading '0'\n")
	r.calls = nil
	res := r.apply()
	if got := states(res.Plan); got[ItemSwappiness] != UserSet || got[ItemFlowOffloading] != UserSet {
		t.Fatalf("states: %v", got)
	}
	b := r.backup()
	if _, ok := b.Items[ItemSwappiness]; ok {
		t.Fatal("swappiness still noted as the tune's")
	}
	if _, ok := b.Items[ItemFlowOffloading]; ok {
		t.Fatal("flow_offloading still noted as the tune's")
	}
	if got := r.read("etc/sysctl.d/95-vectra-tune.conf"); strings.Contains(got, "swappiness") || !strings.Contains(got, "vm.vfs_cache_pressure=200") {
		t.Fatalf("own sysctl file:\n%s", got)
	}
	if len(r.calls) != 0 {
		t.Fatalf("calls: %v", r.calls)
	}
}

// Hardware offloading is never set — and software offloading is not either
// while the owner's hardware flag waits for it: it would switch that on.
func TestHardwareOffloadingIsNeverSwitchedOn(t *testing.T) {
	r := newRouter(t)
	r.write("etc/config/firewall", "config defaults\n\toption flow_offloading_hw '1'\n")
	res := r.apply()
	if got := states(res.Plan)[ItemFlowOffloading]; got != Skipped+"/"+ReasonHWOffload {
		t.Fatalf("flow_offloading: %s", got)
	}
	for _, c := range r.calls {
		if strings.Contains(c, "firewall") {
			t.Fatalf("touched the firewall: %v", r.calls)
		}
	}
}

// A router with RAM to spare keeps its swap and memory settings as they are.
func TestARouterWithRAMToSpareKeepsItsMemorySettings(t *testing.T) {
	r := newRouter(t)
	r.write("proc/meminfo", "MemTotal:         497000 kB\nMemAvailable:     300000 kB\n")
	res := r.apply()
	want := map[string]string{ItemZram: Skipped + "/" + ReasonEnoughRAM, ItemSwappiness: Skipped + "/" + ReasonEnoughRAM,
		ItemVFSCachePressure: Skipped + "/" + ReasonEnoughRAM, ItemPacketSteering: Applied, ItemFlowOffloading: Applied,
		ItemCronLogLevel: Applied, ItemTmpLeftovers: Already}
	if got := states(res.Plan); !reflect.DeepEqual(got, want) || res.Plan.Profile != Standard {
		t.Fatalf("%s: %v", res.Plan.Profile, got)
	}
	for _, c := range r.calls {
		if strings.Contains(c, "zram") || strings.Contains(c, "sysctl") {
			t.Fatalf("changed memory settings: %v", r.calls)
		}
	}
}

func TestOneCoreHasNothingToSteer(t *testing.T) {
	r := newRouter(t)
	r.write("proc/cpuinfo", "processor\t: 0\nmodel name\t: MIPS 24Kc V7.4\n")
	if got := states(Inspect(r.env))[ItemPacketSteering]; got != Skipped+"/"+ReasonOneCore {
		t.Fatalf("packet_steering: %s", got)
	}
}

// A router without a globals section gets one, named as OpenWrt names it.
func TestPacketSteeringWithoutAGlobalsSection(t *testing.T) {
	r := newRouter(t)
	r.write("etc/config/network", "config interface 'loopback'\n\toption device 'lo'\n")
	r.apply()
	if !containsCall(r.calls, "uci -q set network.globals=globals") || !containsCall(r.calls, "uci -q set network.globals.packet_steering=1") {
		t.Fatalf("calls: %v", r.calls)
	}
	if v, _ := r.option("network", "globals", "packet_steering"); v != "1" {
		t.Fatalf("packet_steering = %q", v)
	}
}

// tune '0': nothing is changed; what is in place still reads as it is.
func TestTheSwitchOffChangesNothing(t *testing.T) {
	r := newRouter(t)
	r.write("etc/config/vectra-controller-pro", "config controller 'main'\n\toption tune '0'\n")
	r.write("etc/config/network", "config globals 'globals'\n\toption packet_steering '1'\n")
	res := r.apply()
	if len(r.calls) != 0 || res.Plan.On {
		t.Fatalf("calls: %v, on %v", r.calls, res.Plan.On)
	}
	want := map[string]string{ItemZram: Skipped + "/" + ReasonOff, ItemSwappiness: Skipped + "/" + ReasonOff,
		ItemVFSCachePressure: Skipped + "/" + ReasonOff, ItemPacketSteering: Already, ItemFlowOffloading: Skipped + "/" + ReasonOff,
		ItemCronLogLevel: Skipped + "/" + ReasonOff, ItemTmpLeftovers: Already}
	if got := states(res.Plan); !reflect.DeepEqual(got, want) {
		t.Fatalf("states: %v", got)
	}
}

// Undo puts back what the tune changed — and only what is still as the tune
// left it: what the owner changed since is theirs. The zram swap is not
// switched off under a running router: its boot link goes, the swap with the
// next boot.
func TestUndoPutsBackWhatTheTuneChanged(t *testing.T) {
	r := newRouter(t)
	r.apply()
	// Since: the owner put packet steering on every CPU.
	r.write("etc/config/network", "config globals 'globals'\n\toption packet_steering '2'\n")
	r.calls = nil
	res := r.undo()
	want := []string{
		"/etc/init.d/zram disable",
		"sysctl -q -w vm.swappiness=60",
		"sysctl -q -w vm.vfs_cache_pressure=100",
		"uci -q delete firewall.@defaults[0].flow_offloading",
		"uci -q commit firewall",
		"/etc/init.d/firewall reload",
		"uci -q delete system.@system[0].cronloglevel",
		"uci -q commit system",
		"/etc/init.d/cron reload",
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(r.calls, "\n"), strings.Join(want, "\n"))
	}
	if r.exists("etc/sysctl.d/95-vectra-tune.conf") || r.exists("etc/vectra-controller-pro/tune-backup.json") {
		t.Fatal("the own sysctl file or the backup stayed")
	}
	if v, _ := r.option("network", "globals", "packet_steering"); v != "2" {
		t.Fatalf("the owner's packet_steering = %q", v)
	}
	if _, set := r.option("firewall", "defaults", "flow_offloading"); set {
		t.Fatal("flow_offloading stayed")
	}
	if len(res.Failed) != 0 {
		t.Fatalf("failed: %v", res.Failed)
	}
	// Nothing left to undo.
	r.calls = nil
	r.undo()
	if len(r.calls) != 0 {
		t.Fatalf("a second undo: %v", r.calls)
	}
}

// A value set at runtime since is not the tune's to put back.
func TestUndoLeavesASysctlChangedSince(t *testing.T) {
	r := newRouter(t)
	r.apply()
	r.write("proc/sys/vm/swappiness", "33\n")
	r.calls = nil
	r.undo()
	if containsCall(r.calls, "sysctl -q -w vm.swappiness=60") {
		t.Fatalf("calls: %v", r.calls)
	}
}

// Changes someone left uncommitted in uci's default save directory would go
// live with the tune's commit: that config waits for the next run.
func TestUncommittedChangesWait(t *testing.T) {
	r := newRouter(t)
	r.write("tmp/.uci/firewall", "firewall.@zone[0].input='ACCEPT'\n")
	res := r.apply()
	if got := states(res.Plan)[ItemFlowOffloading]; got != Skipped+"/"+ReasonUCIPending {
		t.Fatalf("flow_offloading: %s", got)
	}
	for _, c := range r.calls {
		if strings.Contains(c, "firewall") {
			t.Fatalf("touched the firewall: %v", r.calls)
		}
	}
	if got := states(res.Plan)[ItemPacketSteering]; got != Applied {
		t.Fatalf("packet_steering: %s", got)
	}
}

// fw4 refuses the ruleset with offloading on: the option is taken back
// before anything reloads, and nothing is noted as the tune's.
func TestAFirewallThatFailsItsCheckIsPutBack(t *testing.T) {
	r := newRouter(t)
	r.fail["/sbin/fw4 -q check"] = errors.New("exit status 1")
	res := r.apply()
	if got := states(res.Plan)[ItemFlowOffloading]; got != Skipped+"/"+ReasonCheckFailed {
		t.Fatalf("flow_offloading: %s", got)
	}
	if containsCall(r.calls, "/etc/init.d/firewall reload") || !containsCall(r.calls, "uci -q delete firewall.@defaults[0].flow_offloading") {
		t.Fatalf("calls: %v", r.calls)
	}
	if _, set := r.option("firewall", "defaults", "flow_offloading"); set {
		t.Fatal("flow_offloading stayed committed")
	}
	if _, noted := r.backup().Items[ItemFlowOffloading]; noted {
		t.Fatal("backed up an option it took back")
	}
	if len(res.Failed) != 1 || res.Failed[0].ID != ItemFlowOffloading {
		t.Fatalf("failed: %v", res.Failed)
	}
}

func TestNoOffloadingWithoutTheKernelModule(t *testing.T) {
	r := newRouter(t)
	if err := os.RemoveAll(r.path("sys/module/nft_flow_offload")); err != nil {
		t.Fatal(err)
	}
	if got := states(Inspect(r.env))[ItemFlowOffloading]; got != Skipped+"/"+ReasonNoKernelSupport {
		t.Fatalf("flow_offloading: %s", got)
	}
	// A module file on disk is support too (loaded on first use).
	r.write("lib/modules/6.6.119/nft_flow_offload.ko", "\x7fELF")
	if got := states(Inspect(r.env))[ItemFlowOffloading]; got != Pending {
		t.Fatalf("flow_offloading with the module on disk: %s", got)
	}
	if err := os.Remove(r.path("sbin/fw4")); err != nil {
		t.Fatal(err)
	}
	if got := states(Inspect(r.env))[ItemFlowOffloading]; got != Skipped+"/"+ReasonNoFw4 {
		t.Fatalf("flow_offloading without fw4: %s", got)
	}
}

func TestZramNeedsItsPackageAndSwapSupport(t *testing.T) {
	r := newRouter(t)
	if err := os.Remove(r.path("etc/init.d/zram")); err != nil {
		t.Fatal(err)
	}
	if got := states(Inspect(r.env))[ItemZram]; got != Skipped+"/"+ReasonNotInstalled {
		t.Fatalf("zram: %s", got)
	}
	r.exec("etc/init.d/zram")
	if err := os.Remove(r.path("proc/swaps")); err != nil {
		t.Fatal(err)
	}
	if got := states(Inspect(r.env))[ItemZram]; got != Skipped+"/"+ReasonNoSwap {
		t.Fatalf("zram without swap support: %s", got)
	}
}

// zram switched off by the owner after the tune switched it on is theirs —
// at this run and every later one (nothing else on the router says so), and
// undo leaves it alone. Switched on again by them, it is theirs too.
func TestZramSwitchedOffSinceIsTheOwnersChoice(t *testing.T) {
	r := newRouter(t)
	r.apply()
	_ = os.Remove(r.path("etc/rc.d/S15zram"))
	for run := 1; run <= 2; run++ {
		r.calls = nil
		res := r.apply()
		if got := states(res.Plan)[ItemZram]; got != UserSet {
			t.Fatalf("run %d: zram: %s", run, got)
		}
		if containsCall(r.calls, "/etc/init.d/zram enable") || containsCall(r.calls, "/etc/init.d/zram start") {
			t.Fatalf("run %d: switched zram on again: %v", run, r.calls)
		}
	}
	b := r.backup()
	if _, noted := b.Items[ItemZram]; noted || !reflect.DeepEqual(b.Owner, []string{ItemZram}) {
		t.Fatalf("backup: %+v", b)
	}
	r.link("etc/rc.d/S15zram", "../init.d/zram")
	if got := states(Inspect(r.env))[ItemZram]; got != Already {
		t.Fatalf("switched on again by the owner: %s", got)
	}
	r.calls = nil
	r.undo()
	if containsCall(r.calls, "/etc/init.d/zram disable") {
		t.Fatalf("undo switched the owner's zram off: %v", r.calls)
	}
}

// A zram that is enabled and does not come up (a kernel without the device):
// said, and tried again at the next run — never noted as the tune's.
func TestAZramThatDoesNotStartIsSaid(t *testing.T) {
	r := newRouter(t)
	r.link("etc/rc.d/S15zram", "../init.d/zram")
	r.startsZram = false
	res := r.apply()
	if got := states(res.Plan)[ItemZram]; got != Pending+"/"+ReasonFailed {
		t.Fatalf("zram: %s", got)
	}
	if len(res.Failed) != 1 || res.Failed[0].ID != ItemZram {
		t.Fatalf("failed: %v", res.Failed)
	}
	if _, err := os.Stat(r.env.Backup); err == nil {
		if _, noted := r.backup().Items[ItemZram]; noted {
			t.Fatal("a zram that did not start is noted as the tune's")
		}
	}
}

// The backup keeps the value from before the first change: a later run that
// sets a value again (changed at runtime since) does not overwrite it.
func TestTheBackupKeepsTheFirstValue(t *testing.T) {
	r := newRouter(t)
	r.apply()
	r.write("proc/sys/vm/swappiness", "30\n")
	res := r.apply()
	if got := states(res.Plan)[ItemSwappiness]; got != Applied {
		t.Fatalf("swappiness: %s", got)
	}
	if got := r.backup().Items[ItemSwappiness].Was; got == nil || *got != "60" {
		t.Fatalf("backup of swappiness: %v", got)
	}
}

// Its own file gone (by hand), the tune writes it again: the values it set
// must survive the next boot.
func TestTheOwnSysctlFileIsKept(t *testing.T) {
	r := newRouter(t)
	r.apply()
	if err := os.Remove(r.env.OwnSysctl); err != nil {
		t.Fatal(err)
	}
	r.apply()
	if got := r.read("etc/sysctl.d/95-vectra-tune.conf"); !strings.Contains(got, "vm.swappiness=80") {
		t.Fatalf("own sysctl file:\n%s", got)
	}
}

// One run at a time: the daemon at its start and the postinst's run meet.
func TestOneRunAtATime(t *testing.T) {
	r := newRouter(t)
	unlock, err := lock(context.Background(), r.env)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := Apply(ctx, r.env); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v", err)
	}
	if len(r.calls) != 0 {
		t.Fatalf("calls: %v", r.calls)
	}
}

func TestSysctlFilesAreReadAsSysctlReadsThem(t *testing.T) {
	got := parseSysctl("# comment\n; another\nvm.swappiness = 10\n  vm/vfs_cache_pressure=50  \n-net.core.somaxconn = 1024\nbroken line\n")
	want := map[string]string{"vm.swappiness": "10", "vm.vfs_cache_pressure": "50", "net.core.somaxconn": "1024"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parsed %v", got)
	}
}

func TestBackupFailurePreventsAllMutations(t *testing.T) {
	r := newRouter(t)
	if err := os.MkdirAll(r.env.Backup, 0700); err != nil {
		t.Fatal(err)
	}
	res := r.apply()
	if len(res.Failed) == 0 {
		t.Fatal("missing backup failure")
	}
	if len(r.calls) != 0 {
		t.Fatalf("mutated without durable backup: %v", r.calls)
	}
	if r.exists("etc/sysctl.d/95-vectra-tune.conf") {
		t.Fatal("persisted sysctls without backup")
	}
}

func TestFirewallRollbackFailureKeepsBackup(t *testing.T) {
	for _, verb := range []string{"delete", "commit"} {
		t.Run(verb, func(t *testing.T) {
			r := newRouter(t)
			original := r.env.Run
			checked := false
			r.env.Run = func(ctx context.Context, name string, args ...string) error {
				if name == r.env.Fw4 {
					checked = true
					return errors.New("invalid ruleset")
				}
				if checked && name == "uci" && args[1] == verb {
					return errors.New("rollback refused")
				}
				return original(ctx, name, args...)
			}
			res := r.apply()
			if _, noted := r.backup().Items[ItemFlowOffloading]; !noted {
				t.Fatal("forgot failed rollback")
			}
			found := false
			for _, failure := range res.Failed {
				if strings.Contains(failure.Err, "rollback") {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing rollback failure: %v", res.Failed)
			}
			if containsCall(r.calls, "/etc/init.d/firewall reload") {
				t.Fatal("reloaded invalid firewall")
			}
		})
	}
}

func TestFailedUCIRevertKeepsBackup(t *testing.T) {
	for _, config := range []string{"network", "firewall"} {
		t.Run(config, func(t *testing.T) {
			r := newRouter(t)
			r.fail["uci -q commit "+config] = errors.New("commit failed")
			r.fail["uci -q revert "+config] = errors.New("revert failed")
			res := r.apply()
			id := ItemPacketSteering
			if config == "firewall" {
				id = ItemFlowOffloading
			}
			if _, ok := r.backup().Items[id]; !ok {
				t.Fatal("forgot failed revert")
			}
			found := false
			for _, failure := range res.Failed {
				if strings.Contains(failure.Err, "revert failed") {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing revert failure: %v", res.Failed)
			}
		})
	}
}

func TestUndoKeepsFailedCommitBackupBehindPendingChange(t *testing.T) {
	r := newRouter(t)
	r.fail["uci -q commit firewall"] = errors.New("commit failed")
	r.fail["uci -q revert firewall"] = errors.New("revert failed")
	r.apply()
	// Failed commit leaves the committed config unset, while UCI still
	// holds the tune's staged set (the default save directory's delta).
	r.write("tmp/.uci/firewall", "firewall.@defaults[0].flow_offloading='1'\n")
	b := r.backup()
	if err := saveBackup(r.env.Backup, Backup{Items: map[string]Saved{ItemFlowOffloading: b.Items[ItemFlowOffloading]}}); err != nil {
		t.Fatal(err)
	}
	r.calls = nil
	res := r.undo()
	if _, ok := r.backup().Items[ItemFlowOffloading]; !ok {
		t.Fatal("lost backup of pending failed commit")
	}
	for _, call := range r.calls {
		if strings.HasPrefix(call, "uci ") {
			t.Fatalf("changed pending UCI: %v", r.calls)
		}
	}
	found := false
	for _, failure := range res.Failed {
		if failure.ID == ItemFlowOffloading {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing pending-change failure: %v", res.Failed)
	}
}
