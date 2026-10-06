package portfwd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"vectra-controller-pro/internal/uci"
)

// Env is where the router keeps what port forwards read and change; tests
// point it at a temp dir and at fakes, and never run uci.
type Env struct {
	FirewallConfig string // /etc/config/firewall
	DHCPConfig     string // /etc/config/dhcp (static leases' names)
	Leases         string // dnsmasq's /tmp/dhcp.leases
	// RunDir is where a change's private uci save directory is made.
	RunDir string
	// UCISaveDir is uci's default save directory: every `uci commit` takes
	// along what waits there, whatever -t says, so a change refuses to run
	// over someone's uncommitted firewall edits (busy).
	UCISaveDir string
	// Lock is held for a whole change: the router UI and Vectra Connect
	// may ask at once, and two batches must not interleave.
	Lock string
	// DirectStatus is the daemon's record of the «past the VPN» set
	// (DirectStatus).
	DirectStatus string

	// Run runs a command (uci, an init script); stdin may be nil.
	Run func(ctx context.Context, stdin io.Reader, name string, args ...string) error
	// Output runs a command and returns its stdout (ubus).
	Output func(ctx context.Context, name string, args ...string) ([]byte, error)
	// NewID picks a new rule's id (NewID when nil).
	NewID func() string
}

// RouterEnv is the production Env.
func RouterEnv() Env {
	return Env{
		FirewallConfig: "/etc/config/firewall",
		DHCPConfig:     "/etc/config/dhcp",
		Leases:         "/tmp/dhcp.leases",
		RunDir:         "/var/run/vectra-controller-pro",
		UCISaveDir:     "/tmp/.uci",
		Lock:           "/var/lock/vectra-portfwd.lock",
		DirectStatus:   "/var/run/vectra-controller-pro/portfwd-direct.json",
		Run:            runGroup,
		Output: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.WaitDelay = time.Second
			return cmd.Output()
		},
	}
}

// runGroup runs a command in a process group of its own and, when ctx ends,
// kills the whole group. /etc/init.d/firewall reload is a shell that runs fw4,
// which runs nft: killing only the shell on a timeout would leave fw4 to load
// the new redirects after the restore had put the old ones back.
func runGroup(ctx context.Context, stdin io.Reader, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// The group's id is the leader's pid (Setpgid); a group already gone
		// is not an error worth more than the kill of the leader.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = time.Second
	return cmd.Run()
}

// State is what the router has: vctl's rules and the LAN's devices.
type State struct {
	Rules   []Rule
	Devices []Device
	// names: each device address's name, where it has one.
	names map[netip.Addr]string
}

// DeviceName is the name of the device at ip, nil when none is known.
func (s State) DeviceName(ip string) *string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return nil
	}
	if n, ok := s.names[a]; ok {
		return &n
	}
	return nil
}

// Read gathers the state. It never fails: what cannot be read stays empty
// (a router without fw4's config has no forwards to show).
func Read(ctx context.Context, env Env) State {
	var st State
	if f, err := uci.Load(env.FirewallConfig); err == nil {
		st.Rules = ParseFirewall(f).Own
	}
	leases, hosts := readDHCP(env)
	st.names = deviceNames(leases, hosts)
	st.Devices = devices(leases, hosts, st.names, ReadLAN(ctx, env))
	return st
}

func readDHCP(env Env) (leases, hosts map[netip.Addr]string) {
	leases = map[netip.Addr]string{}
	if raw, err := os.ReadFile(env.Leases); err == nil && len(raw) <= 1<<20 {
		leases = parseLeases(raw)
	}
	var f *uci.File
	if loaded, err := uci.Load(env.DHCPConfig); err == nil {
		f = loaded
	}
	return leases, parseStaticHosts(f)
}

// LoadOwn reads vctl's own rules from the firewall config: what the daemon
// fills firewall.SetPortForwardDirect4 from.
func LoadOwn(path string) ([]Rule, error) {
	f, err := uci.Load(path)
	if err != nil {
		return nil, err
	}
	return ParseFirewall(f).Own, nil
}

// ReadLAN asks netifd for the lan interface's IPv4 addresses: the subnets a
// destination must be in, and the router's own addresses there. Empty when
// netifd does not answer — and then every rule is refused (dest_not_lan),
// never accepted on a guess.
func ReadLAN(ctx context.Context, env Env) LAN {
	if env.Output == nil {
		return LAN{}
	}
	c, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := env.Output(c, "ubus", "call", "network.interface.lan", "status")
	if err != nil {
		return LAN{}
	}
	return parseLAN(out)
}

func parseLAN(raw []byte) LAN {
	var st struct {
		IPv4 []struct {
			Address string `json:"address"`
			Mask    int    `json:"mask"`
		} `json:"ipv4-address"`
	}
	if json.Unmarshal(raw, &st) != nil {
		return LAN{}
	}
	var lan LAN
	seen := map[netip.Prefix]bool{}
	for _, a := range st.IPv4 {
		addr, err := netip.ParseAddr(a.Address)
		if err != nil || !addr.Is4() || a.Mask < 1 || a.Mask > 32 {
			continue
		}
		lan.Router = append(lan.Router, addr)
		p := netip.PrefixFrom(addr, a.Mask).Masked()
		if !seen[p] {
			seen[p] = true
			lan.Subnets = append(lan.Subnets, p)
		}
	}
	return lan
}

var cgnatRange = netip.MustParsePrefix("100.64.0.0/10")

// CGNAT reports whether the WAN's IPv4 address (netifd's, as setup.ReadWan
// reads it; "a.b.c.d" or "a.b.c.d/len") cannot be reached from the internet:
// the provider's shared range (100.64.0.0/10) or a private one (behind
// another router). The UI then says, in one line, that a forward will not be
// reachable from outside — or must be made on the router in front too. No
// address, or one that cannot be read, is not CGNAT: there is nothing to say.
func CGNAT(wanIPv4 string) bool {
	s := strings.TrimSpace(wanIPv4)
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	a, err := netip.ParseAddr(s)
	return err == nil && a.Is4() && (cgnatRange.Contains(a) || a.IsPrivate())
}

// Apply makes the owner's list the router's port forwards: it validates the
// whole list against the router as it is now and writes vctl's redirects in
// one batch, then fw4 reloads. It returns the rules as kept (ids given). A
// refusal is an *Error by code.
//
// Under the lock, and through a private uci save directory: an owner's
// uncommitted firewall edits in uci's default one would be committed along
// with this change, so they refuse it instead (busy).
//
// The commit comes before the reload, so a reload that fails — or does not
// finish in time — would leave the new redirects committed but not running,
// to go live at the next reload or boot while the owner was told it failed.
// So the firewall file is kept as it was before the commit, and on a failed
// reload it is put back and fw4 reloaded again: apply_failed means the router
// runs the forwards it had. Only when that second reload fails as well is the
// answer internal (the old config is back on disk, fw4 did not reload it).
//
// The whole of it keeps within ctx's deadline — `vctl rpcd` has LuCI's call
// budget to answer in, and a child killed between the commit and the reload
// is exactly the state above: the first reload gets half of what is left,
// and the restore the rest (never less than restoreFloor, even past the
// deadline: a restore cut short is worse than an answer a little late).
func Apply(ctx context.Context, env Env, rules []Rule) ([]Rule, *Error) {
	unlock, err := lock(env)
	if err != nil {
		if errors.Is(err, errBusy) {
			return nil, refuse(CodeBusy, "another port forward change is being applied")
		}
		return nil, refuse(CodeApplyFailed, "lock: %v", err)
	}
	defer unlock()

	before, err := os.ReadFile(env.FirewallConfig)
	if err != nil {
		return nil, refuse(CodeApplyFailed, "the firewall config cannot be read: %v", err)
	}
	f, err := uci.Parse(string(before))
	if err != nil {
		return nil, refuse(CodeApplyFailed, "the firewall config cannot be read: %v", err)
	}
	fw := ParseFirewall(f)
	kept, verr := Validate(rules, Context{LAN: ReadLAN(ctx, env), foreign: fw.foreign, reserved: fw.reserved, NewID: env.NewID})
	if verr != nil {
		return nil, verr
	}
	if pendingUCI(env, "firewall") {
		return nil, refuse(CodeBusy, "uncommitted firewall changes wait in uci (uci changes firewall): commit or revert them first")
	}
	leases, hosts := readDHCP(env)
	names := State{names: deviceNames(leases, hosts)}
	if err := commitBatch(ctx, env, "firewall", FirewallBatch(fw, kept, names.DeviceName)); err != nil {
		if errors.Is(err, errPendingUCI) {
			return nil, refuse(CodeBusy, "uncommitted firewall changes wait in uci (uci changes firewall): commit or revert them first")
		}
		// uci commit replaces the file whole, or not at all — but one that
		// failed after it did (killed past its time) must not leave the new
		// redirects in, to go live at the next reload: fw4 has not reloaded,
		// so the file as it was is the router's state.
		if now, rerr := os.ReadFile(env.FirewallConfig); rerr != nil || !bytes.Equal(now, before) {
			if werr := writeAtomic(env.FirewallConfig, before); werr != nil {
				return nil, refuse(CodeInternal, "%v; the previous config could not be put back: %v", err, werr)
			}
		}
		return nil, refuse(CodeApplyFailed, "%v", err)
	}
	c, cancel := share(ctx, 2, reloadMax)
	err = env.Run(c, nil, "/etc/init.d/firewall", "reload")
	cancel()
	if err == nil {
		return kept, nil
	}
	if rerr := restore(ctx, env, before); rerr != nil {
		return nil, refuse(CodeInternal, "firewall reload: %v; the previous config is back on disk, but fw4 did not reload it: %v", err, rerr)
	}
	return nil, refuse(CodeApplyFailed, "firewall reload: %v; the previous forwards are back", err)
}

// reloadMax bounds one fw4 reload when the caller set no deadline;
// restoreFloor is the least a restore gets.
const (
	reloadMax    = 30 * time.Second
	restoreFloor = 3 * time.Second
)

// share is a context for one step: 1/parts of the time ctx has left, or max
// when ctx has no deadline.
func share(ctx context.Context, parts int, max time.Duration) (context.Context, context.CancelFunc) {
	if dl, ok := ctx.Deadline(); ok {
		return context.WithTimeout(ctx, time.Until(dl)/time.Duration(parts))
	}
	return context.WithTimeout(ctx, max)
}

// restore puts the firewall file back as it was and reloads fw4 on it. It
// runs even when ctx is done — within what is left of its deadline, but at
// least restoreFloor.
func restore(ctx context.Context, env Env, before []byte) error {
	budget := reloadMax
	if dl, ok := ctx.Deadline(); ok {
		budget = max(time.Until(dl), restoreFloor)
	}
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), budget)
	defer cancel()
	if err := writeAtomic(env.FirewallConfig, before); err != nil {
		return err
	}
	return env.Run(c, nil, "/etc/init.d/firewall", "reload")
}

// writeAtomic replaces path with data, keeping its mode: a temp file beside
// it, renamed over it.
func writeAtomic(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".vctl-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// pendingUCI: uci's default save directory holds changes to config.
func pendingUCI(env Env, config string) bool {
	if env.UCISaveDir == "" {
		return false
	}
	fi, err := os.Stat(filepath.Join(env.UCISaveDir, config))
	return err == nil && fi.Size() > 0
}

// errPendingUCI: someone's uncommitted edits to the config appeared in uci's
// default save directory while the batch ran.
var errPendingUCI = errors.New("uncommitted changes wait in uci")

// saveDirPrefix names a change's private uci save directory in RunDir.
const saveDirPrefix = "uci-portfwd-"

// commitBatch runs script through `uci batch` into a private save directory
// and commits config from it; the directory goes either way. One left behind
// by a change that was killed mid-way is swept first (under the lock, no
// other change has one).
//
// uci's default save directory is looked at again just before the commit,
// which takes along whatever waits there: edits made in LuCI while the batch
// ran refuse the change (errPendingUCI) instead of being committed with it.
// What is left is the instant between that look and uci's own; uci has no
// lock to close it.
func commitBatch(ctx context.Context, env Env, config, script string) error {
	if err := os.MkdirAll(env.RunDir, 0o755); err != nil {
		return err
	}
	if stale, _ := filepath.Glob(filepath.Join(env.RunDir, saveDirPrefix+"*")); len(stale) > 0 {
		for _, p := range stale {
			_ = os.RemoveAll(p)
		}
	}
	dir, err := os.MkdirTemp(env.RunDir, saveDirPrefix)
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := env.Run(c, strings.NewReader(script), "uci", "-t", dir, "batch"); err != nil {
		return fmt.Errorf("uci batch %s: %w", config, err)
	}
	if pendingUCI(env, config) {
		return errPendingUCI
	}
	if err := env.Run(c, nil, "uci", "-t", dir, "commit", config); err != nil {
		return fmt.Errorf("uci commit %s: %w", config, err)
	}
	return nil
}

var errBusy = errors.New("busy")

// lock takes the port forwards' lock without waiting.
func lock(env Env) (func(), error) {
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
			return nil, errBusy
		}
		return nil, err
	}
	return func() { f.Close() }, nil
}
