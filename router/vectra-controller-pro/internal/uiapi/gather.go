package uiapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/api"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/power"
	"vectra-controller-pro/internal/uaguard"
	"vectra-controller-pro/internal/xrayview"
)

// Need says which inputs a method reads: each rpcd call is its own process
// on a 234 MB router, so nothing is gathered that the answer does not use.
type Need struct {
	Runtime, View, Reach, Metrics, Balancers, Local, Operator, Counters, Legacy, Router bool
}

var (
	NeedStatus      = Need{Runtime: true, View: true, Reach: true, Local: true, Counters: true, Legacy: true, Router: true}
	NeedBalancers   = Need{Runtime: true, View: true, Balancers: true, Local: true}
	NeedNodes       = Need{View: true, Metrics: true}
	NeedEntries     = Need{Runtime: true, Local: true, Operator: true}
	NeedDiagnostics = Need{Runtime: true, View: true, Reach: true, Metrics: true, Balancers: true, Local: true,
		Operator: true, Counters: true, Legacy: true, Router: true}
)

// Env is where a router keeps things; tests point it elsewhere.
type Env struct {
	Cfg      agentcfg.Config
	ProcDir  string // "/proc"
	RCDir    string // "/etc/rc.d"
	Nft      string // "nft"
	Overlay  string // "/overlay"
	Tmp      string // "/tmp"
	ModelF   string // "/tmp/sysinfo/model"
	Release  string // "/etc/openwrt_release"
	Version  string
	CallTime time.Duration // per external call
}

// RouterEnv is the production Env for cfg.
func RouterEnv(cfg agentcfg.Config, version string) Env {
	return Env{Cfg: cfg, ProcDir: "/proc", RCDir: "/etc/rc.d", Nft: "nft", Overlay: "/overlay", Tmp: "/tmp",
		ModelF: "/tmp/sysinfo/model", Release: "/etc/openwrt_release", Version: version, CallTime: 3 * time.Second}
}

// Gather collects what need asks for. It never fails: what cannot be read is
// left nil, and the answer says null.
func Gather(ctx context.Context, env Env, need Need) Inputs {
	in := Inputs{Now: time.Now().UTC(), Version: env.Version}
	call := func() (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, env.CallTime) }

	if need.Runtime {
		c, cancel := call()
		if resp, err := localctl.Call(c, env.Cfg.UISocketPath, localctl.SocketRequest{Op: localctl.OpRuntime}); err == nil && resp.OK {
			in.Runtime = resp.Runtime
		}
		cancel()
	}
	if need.View || need.Reach || need.Metrics || need.Balancers {
		if raw, err := os.ReadFile(env.Cfg.XrayRenderPath); err == nil {
			in.View, _ = xrayview.Parse(raw)
		}
	}
	if need.Local {
		in.Overrides, _ = localctl.LoadOverrides(env.Cfg.OverridesPath)
		in.Index, _ = localctl.LoadEntriesIndex(env.Cfg.EntriesIndexPath)
		if in.Runtime == nil || in.Runtime.Probe == nil {
			if raw, err := os.ReadFile(env.Cfg.ProviderConfigPath); err == nil {
				in.ProviderProbeInterval, _, _ = xray.ProviderProbeInterval(raw)
			}
		}
	}
	if need.Operator {
		if raw, err := os.ReadFile(env.Cfg.XrayConfigPath); err == nil {
			if c, err := config.Unmarshal(raw); err == nil {
				for _, s := range c.Subscriptions {
					if !s.Enabled {
						continue
					}
					in.HasOperatorConfig = true
					in.PanelRemark, in.PanelIndex = s.EntryRemark, s.EntryIndex
					ua := s.UserAgent
					if h, ok := uaguard.FromHeaders(s.Headers); ok {
						ua = h
					}
					if err := uaguard.Check(ua); err != nil {
						in.UserAgentProblem = "malformed_happ"
					}
					break
				}
			}
		}
	}
	if v := in.View; v != nil {
		// Only ever loopback (see xrayview.Loopback).
		if !xrayview.Loopback(v.APIListen) {
			v.APIListen = ""
		}
		if !xrayview.Loopback(v.MetricsListen) {
			v.MetricsListen = ""
		}
		if need.Reach {
			in.APIReachable = v.APIListen != "" && dialable(ctx, v.APIListen)
			in.MetricsReachable = v.MetricsListen != "" && dialable(ctx, v.MetricsListen)
		}
		if need.Metrics && v.MetricsListen != "" {
			c, cancel := call()
			if m, err := api.FetchMetrics(c, v.MetricsListen); err == nil {
				in.Metrics, in.MetricsReachable = m, true
			}
			cancel()
		}
		if need.Balancers && v.APIListen != "" && len(v.Balancers) > 0 {
			tags := make([]string, len(v.Balancers))
			for i, b := range v.Balancers {
				tags[i] = b.Tag
			}
			c, cancel := call()
			if info, err := api.GetBalancerInfo(c, v.APIListen, tags); err == nil {
				in.Balancer, in.APIReachable = info, true
			}
			cancel()
		}
	}
	if need.Counters {
		in.Counters, in.TableLoaded = NftCounters(ctx, env)
	}
	if need.Legacy {
		in.AgentEnabled = legacyAgentEnabled(env.RCDir)
		in.PasswallRunning = power.PassWallRunning(env.ProcDir)
	}
	if need.Router {
		in.Router = routerFacts(env)
		if rt := in.Runtime; rt != nil && rt.Engine.PID > 0 && rt.Engine.State == "running" {
			in.XrayRSSMiB, in.MemoryLimitMiB = processMemory(env.ProcDir, rt.Engine.PID)
		}
	}
	return in
}

func dialable(ctx context.Context, addr string) bool {
	d := net.Dialer{Timeout: time.Second}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// NftCounters reads the named counters of the vctl table. The table's absence
// (nft exits non-zero) is the data plane being unloaded. The daemon reads the
// leak baseline with it too (cmd/vctl takeLeakBaseline).
func NftCounters(ctx context.Context, env Env) (map[string]int64, bool) {
	c, cancel := context.WithTimeout(ctx, env.CallTime)
	defer cancel()
	out, err := exec.CommandContext(c, env.Nft, "-j", "list", "counters", "table", "inet", "vctl").Output()
	if err != nil {
		return nil, false
	}
	var doc struct {
		Nftables []struct {
			Counter *struct {
				Name    string `json:"name"`
				Packets int64  `json:"packets"`
			} `json:"counter"`
		} `json:"nftables"`
	}
	if json.Unmarshal(out, &doc) != nil {
		return nil, true
	}
	counters := map[string]int64{}
	for _, e := range doc.Nftables {
		if e.Counter != nil {
			counters[e.Counter.Name] = e.Counter.Packets
		}
	}
	return counters, true
}

// legacyAgentEnabled: the old agent's rc.d start link, exactly its name —
// "S99vectra-controller", not "...-pro".
func legacyAgentEnabled(rcDir string) bool {
	matches, _ := filepath.Glob(filepath.Join(rcDir, "S*vectra-controller"))
	return len(matches) > 0
}

func routerFacts(env Env) RouterFacts {
	var f RouterFacts
	f.Hostname, _ = os.Hostname()
	if b, err := os.ReadFile(env.ModelF); err == nil {
		f.Model = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile(env.Release); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(line, "DISTRIB_DESCRIPTION="); ok {
				f.Release = strings.Trim(strings.TrimSpace(v), `'"`)
			}
		}
	}
	if b, err := os.ReadFile(filepath.Join(env.ProcDir, "meminfo")); err == nil {
		kb := map[string]int{}
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) >= 2 {
				if n, err := strconv.Atoi(fields[1]); err == nil {
					kb[strings.TrimSuffix(fields[0], ":")] = n
				}
			}
		}
		if v, ok := kb["MemTotal"]; ok {
			f.MemTotalMiB = ptr(v / 1024)
		}
		if v, ok := kb["MemAvailable"]; ok {
			f.MemAvailableMiB = ptr(v / 1024)
		}
	}
	f.OverlayFreeMiB = freeMiB(env.Overlay)
	f.TmpFreeMiB = freeMiB(env.Tmp)
	if b, err := os.ReadFile(filepath.Join(env.ProcDir, "loadavg")); err == nil {
		fields := strings.Fields(string(b))
		for i := 0; i < 3 && i < len(fields); i++ {
			if v, err := strconv.ParseFloat(fields[i], 64); err == nil {
				f.Load = append(f.Load, v)
			}
		}
	}
	if b, err := os.ReadFile(filepath.Join(env.ProcDir, "uptime")); err == nil {
		if fields := strings.Fields(string(b)); len(fields) > 0 {
			if v, err := strconv.ParseFloat(fields[0], 64); err == nil {
				f.UptimeSec = ptr(int(v))
			}
		}
	}
	return f
}

func freeMiB(path string) *int {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return nil
	}
	v := int(uint64(st.Bavail) * uint64(st.Bsize) / (1 << 20))
	return &v
}

// processMemory reads xray's RSS and the soft limit its wrapper exports
// (GOMEMLIMIT, e.g. "80MiB").
func processMemory(procDir string, pid int) (*float64, *int) {
	var rss *float64
	var limit *int
	dir := filepath.Join(procDir, strconv.Itoa(pid))
	if b, err := os.ReadFile(filepath.Join(dir, "status")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(line, "VmRSS:"); ok {
				if kb, err := strconv.Atoi(strings.Fields(v)[0]); err == nil {
					mib := float64(kb) / 1024
					mib = float64(int(mib*10+0.5)) / 10
					rss = &mib
				}
			}
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "environ")); err == nil {
		for _, kv := range bytes.Split(b, []byte{0}) {
			if v, ok := bytes.CutPrefix(kv, []byte("GOMEMLIMIT=")); ok {
				limit = parseMemLimit(string(v))
			}
		}
	}
	return rss, limit
}

func parseMemLimit(s string) *int {
	units := []struct {
		suffix string
		mul    float64
	}{{"GiB", 1024}, {"MiB", 1}, {"KiB", 1.0 / 1024}, {"B", 1.0 / (1 << 20)}}
	for _, u := range units {
		if n, ok := strings.CutSuffix(s, u.suffix); ok {
			if v, err := strconv.ParseFloat(n, 64); err == nil {
				mib := int(v*u.mul + 0.5)
				return &mib
			}
			return nil
		}
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		mib := int(v/(1<<20) + 0.5)
		return &mib
	}
	return nil
}
