package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/api"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/sites"
	"vectra-controller-pro/internal/uci"
	"vectra-controller-pro/internal/uiapi"
	"vectra-controller-pro/internal/xrayview"
)

func init() {
	register(command{name: "rpcd", summary: "rpcd exec plugin for the router UI (ubus object \"vectra\")", run: cmdRPCD})
}

// cmdRPCD is the ubus object `vectra`, as an rpcd exec plugin.
//
// rpcd runs /usr/libexec/rpcd/vectra (which execs this) as
//
//	vectra list             -> print the methods and their parameter types
//	vectra call <method>    -> read the params object on stdin, print the answer
//
// LuCI reaches it through its authenticated ubus session, and the ACL in
// /usr/share/rpcd/acl.d/vectra-controller-pro.json splits the read methods
// from the ones that change the router. Every answer is a JSON OBJECT (rpcd
// rejects anything else), and the process exits 0 even when the answer is a
// refusal: a non-zero exit becomes an opaque ubus error, and the UI needs the
// code.
//
// Each call is its own short process on a 234 MB router, so a call gathers
// only what its answer needs (uiapi.Need*).
func cmdRPCD(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("rpcd: 'list' or 'call <method>' required")
	}
	// rpcd hands its plugins a bare environment; uci, nft, logread and date
	// are found through PATH.
	if os.Getenv("PATH") == "" {
		_ = os.Setenv("PATH", "/usr/sbin:/usr/bin:/sbin:/bin")
	}
	switch args[0] {
	case "list":
		return writeJSON(os.Stdout, rpcdSignatures)
	case "call":
		if len(args) < 2 {
			return fmt.Errorf("rpcd: call needs a method")
		}
		params, _ := io.ReadAll(io.LimitReader(os.Stdin, 64<<10))
		out := rpcdAnswer(context.Background(), rpcdConfig(), args[1], params)
		return writeJSON(os.Stdout, out)
	}
	return fmt.Errorf("rpcd: unknown verb %q", args[0])
}

// rpcdSignatures tell rpcd each method's parameters; the values only carry
// their types (numbers become int32, strings strings, lists arrays).
var rpcdSignatures = map[string]map[string]interface{}{
	"status":             {},
	"balancers":          {},
	"nodes":              {},
	"entries":            {},
	"diagnostics":        {},
	"logs":               {"lines": 200},
	"rules":              {},
	"select_entry":       {"index": 0},
	"reset_entry":        {},
	"pin_balancer":       {"balancer": "str", "node": "str"},
	"unpin_balancer":     {"balancer": "str"},
	"set_probe_interval": {"seconds": 0},
	"set_rules":          {"direct": []string{}, "proxy": []string{}},
	"restart_xray":       {},
	"services":           {},
	"set_service":        {"id": "str", "country": "str"},
}

// rpcdAgentConfig is the daemon config the init script renders at start.
const rpcdAgentConfig = "/var/run/vectra-controller-pro/agent.json"

// rpcdConfig loads the daemon's own paths; before the daemon ever started,
// the defaults are the right ones anyway.
func rpcdConfig() agentcfg.Config {
	if c, err := agentcfg.Load(rpcdAgentConfig); err == nil {
		return c
	}
	c, _ := agentcfg.Parse([]byte(`{"controlUrl":"unused"}`))
	return c
}

// rpcdMutationWait bounds a change that restarts xray. LuCI's rpc layer gives
// a call ~20 s by default; past this the answer is "pending" and the UI polls.
const rpcdMutationWait = 14 * time.Second

// rpcdProOnly are the methods of the UI's Pro view. When the operator locks a
// router to the simple view (UCI vectra-controller-pro.main.ui_lock), the
// router refuses them itself — before gathering anything or asking the
// daemon — so the lock does not rest on the UI hiding a tab. unpin_balancer
// stays: it only hands a balancer back to the provider's own choice, and the
// simple view needs it to undo a pin made earlier in Pro. rules / set_rules
// ("My sites") are the simple view's own.
var rpcdProOnly = map[string]bool{
	"balancers": true, "nodes": true, "logs": true, "pin_balancer": true, "set_probe_interval": true,
}

// Seams for tests, which never exec the real uci and never read the router.
var (
	rpcdReadUILock = readUCIUILock
	rpcdConfigFile = "/etc/config/vectra-controller-pro"
	rpcdGather     = uiapi.Gather
	rpcdGatherLogs = uiapi.GatherLogs
)

// rpcdUCITimeout bounds asking uci for the lock; past it the config file is
// read instead.
var rpcdUCITimeout = 2 * time.Second

// errUCIAbsent is uci answering that the lock option does not exist.
var errUCIAbsent = errors.New("uci: the option does not exist")

// readUCIUILock asks uci for the operator's lock: the raw value, errUCIAbsent
// when uci answers that there is no such option, any other error when uci did
// not answer — it timed out, was killed, is missing, or failed on the file.
// Not -q: a missing option (exit 1, "Entry not found") and a config uci cannot
// parse (exit 1, "Parse error") look the same without the message.
func readUCIUILock(ctx context.Context) (string, error) {
	c, cancel := context.WithTimeout(ctx, rpcdUCITimeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(c, "uci", "get", "vectra-controller-pro.main.ui_lock")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = 500 * time.Millisecond // a killed uci's pipes must not hold the call
	err := cmd.Run()
	if err == nil {
		return stdout.String(), nil
	}
	var exit *exec.ExitError
	if c.Err() == nil && errors.As(err, &exit) && exit.ExitCode() == 1 && strings.TrimSpace(stdout.String()) == "" &&
		strings.Contains(stderr.String(), "Entry not found") {
		return "", errUCIAbsent
	}
	return "", fmt.Errorf("uci get: %w: %s", err, strings.TrimSpace(stderr.String()))
}

// uiLocked reports the operator's lock. It is read at every call, so an
// operator's `uci set …; uci commit` applies to the very next one and nothing
// restarts. No such option is unlocked. When uci does not answer, the file it
// reads is parsed instead; when that cannot be read either, the router stays
// LOCKED — a customer router must not open the Pro view because uci hiccuped.
func uiLocked(ctx context.Context) bool {
	v, err := rpcdReadUILock(ctx)
	switch {
	case err == nil:
		return lockValue(v)
	case errors.Is(err, errUCIAbsent):
		return false
	}
	v, present, err := uiLockFromFile(rpcdConfigFile)
	if err != nil {
		return true
	}
	return present && lockValue(v)
}

func lockValue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// uiLockFromFile reads option ui_lock of section main from a UCI file: the
// value, and whether the option is there at all. The file must parse the way
// uci parses it, or it is an error.
func uiLockFromFile(path string) (value string, present bool, err error) {
	f, err := uci.Load(path)
	if err != nil {
		return "", false, err
	}
	if main := f.Named("main"); main != nil {
		value, present = main.Options["ui_lock"]
	}
	return value, present, nil
}

func rpcdCall(ctx context.Context, cfg agentcfg.Config, method string, params []byte) interface{} {
	locked := uiLocked(ctx) || panelLocked(cfg)
	if locked && rpcdProOnly[method] {
		return action(false, "locked", "")
	}
	if out, ok := rpcdSetupCall(ctx, cfg, method, params); ok {
		return out
	}
	env := uiapi.RouterEnv(cfg, controllerVersion())
	switch method {
	case "status":
		in := rpcdGather(ctx, env, uiapi.NeedStatus)
		in.UILocked = locked
		in.Power = rpcdPower(ctx, in.Runtime != nil, in.TableLoaded)
		return uiapi.BuildStatus(in)
	case "balancers":
		return uiapi.BuildBalancers(rpcdGather(ctx, env, uiapi.NeedBalancers))
	case "nodes":
		return uiapi.BuildNodes(rpcdGather(ctx, env, uiapi.NeedNodes))
	case "entries":
		return uiapi.BuildEntries(rpcdGather(ctx, env, uiapi.NeedEntries))
	case "diagnostics":
		return uiapi.BuildDiagnostics(rpcdGather(ctx, env, uiapi.NeedDiagnostics))
	case "logs":
		var p struct {
			Lines int `json:"lines"`
		}
		_ = json.Unmarshal(params, &p)
		return rpcdGatherLogs(ctx, filepath.Join("/var/log/vectra-controller-pro", "xray.log"), p.Lines)
	case "rules":
		// Unreadable overrides render no sites either (the daemon renders with
		// defaults), so "none" is what the router runs.
		ov, _ := localctl.LoadOverrides(cfg.OverridesPath)
		catalog, _ := geoServices(rpcdGeoDir(cfg))
		return uiapi.BuildRules(ov, catalog)
	case "services":
		// The running render: what the countries are is what runs, choice
		// included.
		raw, _ := os.ReadFile(cfg.XrayRenderPath)
		ov, _ := localctl.LoadOverrides(cfg.OverridesPath)
		var egress map[string]string
		if in := rpcdGather(ctx, env, uiapi.Need{Runtime: true}); in.Runtime != nil {
			egress = in.Runtime.Egress
		}
		return uiapi.BuildServices(cfg.RouteSource == "", raw, ov, egress)
	case "select_entry", "reset_entry", "pin_balancer", "unpin_balancer", "set_probe_interval", "set_rules", "set_service", "restart_xray":
		return rpcdMutate(ctx, cfg, method, params)
	case "set_power":
		return rpcdSetPower(ctx, params)
	}
	return action(false, "invalid_params", "unknown method "+method)
}

func action(ok bool, code, detail string) uiapi.Action {
	a := uiapi.Action{OK: ok, Code: code}
	if detail != "" {
		a.Detail = &detail
	}
	return a
}

// rpcdMutate performs one change. Changes that need a re-render go to the
// daemon WITH the change in the request: the daemon applies it and persists it
// only once it runs, so a change that fails — or is still queued when this
// process stops waiting — never sits on disk as a choice the router is not
// running.
func rpcdMutate(ctx context.Context, cfg agentcfg.Config, method string, params []byte) uiapi.Action {
	var p struct {
		Index    *int   `json:"index"`
		Balancer string `json:"balancer"`
		Node     string `json:"node"`
		Seconds  *int   `json:"seconds"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return action(false, "invalid_params", "params are not a JSON object")
		}
	}

	switch method {
	case "pin_balancer", "unpin_balancer":
		return rpcdPin(ctx, cfg, method == "pin_balancer", p.Balancer, p.Node)
	case "restart_xray":
		return askDaemon(ctx, cfg, localctl.SocketRequest{Op: localctl.OpRestartXray}, "xray_restarted")
	}

	var change localctl.Change
	okCode := ""
	switch method {
	case "select_entry":
		if p.Index == nil {
			return action(false, "invalid_params", "index is required")
		}
		idx, err := localctl.LoadEntriesIndex(cfg.EntriesIndexPath)
		if err != nil {
			return action(false, "no_entries_cache", "")
		}
		if *p.Index < 0 || *p.Index >= len(idx.Entries) {
			return action(false, "unknown_entry", fmt.Sprintf("index %d of %d", *p.Index, len(idx.Entries)))
		}
		e := idx.Entries[*p.Index]
		if e.Remark == "" {
			// An empty remark is how "no choice" is written down; a location
			// without a name cannot be chosen.
			return action(false, "unknown_entry", "this location has no name to be chosen by")
		}
		change.SetEntry = &localctl.EntryChoice{Remark: e.Remark, Index: e.Index}
		okCode = "entry_selected"
	case "reset_entry":
		change.ResetEntry = true
		okCode = "entry_reset"
	case "set_probe_interval":
		if p.Seconds == nil {
			return action(false, "invalid_params", "seconds is required")
		}
		s := *p.Seconds
		if s != 0 && (time.Duration(s)*time.Second < xray.MinProbeInterval || time.Duration(s)*time.Second > xray.MaxProbeInterval) {
			return action(false, "invalid_params", fmt.Sprintf("seconds must be 0 or within [%d, %d]",
				int(xray.MinProbeInterval/time.Second), int(xray.MaxProbeInterval/time.Second)))
		}
		change.ProbeIntervalSec = &s
		okCode = "probe_interval_set"
	case "set_rules":
		// Both lists, always: they replace what is there, so a list left out
		// is refused rather than read as "empty it".
		var rp struct {
			Direct *[]string `json:"direct"`
			Proxy  *[]string `json:"proxy"`
		}
		if len(params) > 0 {
			if err := json.Unmarshal(params, &rp); err != nil {
				return action(false, "invalid_params", "direct and proxy must be lists of strings")
			}
		}
		if rp.Direct == nil || rp.Proxy == nil {
			return action(false, "invalid_params", "direct and proxy are both required; [] empties a list")
		}
		direct, proxy, err := sites.NormalizeLists(*rp.Direct, *rp.Proxy)
		if err != nil {
			return action(false, "invalid_params", err.Error())
		}
		// A service the geo file xray runs with lacks would fail the render.
		if svcs := servicesOf(direct, proxy); len(svcs) > 0 {
			if known, ok := geoServices(rpcdGeoDir(cfg)); ok {
				for _, s := range svcs {
					if !contains(known, s) {
						return action(false, "unknown_service", s)
					}
				}
			}
		}
		change.SetRules = &localctl.Rules{Direct: direct, Proxy: proxy}
		okCode = "rules_set"
	case "set_service":
		var sp struct {
			ID      *string `json:"id"`
			Country *string `json:"country"`
		}
		if err := json.Unmarshal(params, &sp); err != nil {
			return action(false, "invalid_params", "id and country must be strings")
		}
		if sp.ID == nil || sp.Country == nil {
			return action(false, "invalid_params", `id and country are both required; "" is the entry's own path`)
		}
		if _, ok := xray.ServiceByID(*sp.ID); !ok {
			return action(false, "unknown_service", *sp.ID)
		}
		if cfg.RouteSource != "" {
			return action(false, "unavailable", "this router routes by the operator's policy: no subscription entry to choose from")
		}
		cc := strings.ToUpper(strings.TrimSpace(*sp.Country))
		if cc != "" {
			raw, err := os.ReadFile(cfg.XrayRenderPath)
			if err != nil {
				return action(false, "apply_failed", "no running config yet")
			}
			// A country of the entry, for a service the entry has a path of
			// its own for — the overlay falls back to that path.
			if sc := xray.ServiceCountries(raw); !sc.Offered[*sp.ID] || !slices.Contains(sc.Countries, cc) {
				return action(false, "unknown_country", cc)
			}
		}
		change.SetService = &localctl.ServiceChoice{ID: *sp.ID, Country: cc}
		okCode = "service_set"
	}
	return askDaemon(ctx, cfg, localctl.SocketRequest{Op: localctl.OpReapply, Change: &change}, okCode)
}

// askDaemon runs a request on the daemon's loop and maps its answer to an
// action. Only running out of time is "pending" — the daemon has the request
// and will finish it; any other transport failure is a failure.
func askDaemon(ctx context.Context, cfg agentcfg.Config, req localctl.SocketRequest, okCode string) uiapi.Action {
	c, cancel := context.WithTimeout(ctx, rpcdMutationWait)
	defer cancel()
	resp, err := localctl.Call(c, cfg.UISocketPath, req)
	switch {
	case errors.Is(err, localctl.ErrDaemonDown):
		return action(false, "controller_down", "")
	case err != nil && c.Err() != nil:
		return action(true, "pending", "")
	case err != nil:
		return action(false, "internal", err.Error())
	case resp.OK && resp.Code == "pending":
		return action(true, "pending", "")
	case resp.OK:
		return action(true, okCode, "")
	}
	code := resp.Code
	if code == "" {
		code = "internal"
	}
	return action(false, code, resp.Detail)
}

// rpcdPin pins or releases a balancer, live through xray's API and
// persistently in the overrides (re-applied after every xray start).
func rpcdPin(ctx context.Context, cfg agentcfg.Config, pin bool, balancer, node string) uiapi.Action {
	if balancer == "" || (pin && node == "") {
		return action(false, "invalid_params", "balancer (and node, to pin) are required")
	}
	raw, err := os.ReadFile(cfg.XrayRenderPath)
	if err != nil {
		return action(false, "xray_api_unavailable", "no installed xray config")
	}
	view, err := xrayview.Parse(raw)
	if err != nil {
		return action(false, "internal", err.Error())
	}
	if view.Balancer(balancer) == nil {
		return action(false, "unknown_balancer", "")
	}
	if pin {
		// xray accepts ANY tag as an override and routes everything there.
		if err := view.CanPin(balancer, node); err != nil {
			return action(false, "unknown_node", err.Error())
		}
	}
	target := ""
	if pin {
		target = node
	}

	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	apiErr := errors.New("the running config has no loopback xray API")
	if xrayview.Loopback(view.APIListen) {
		apiErr = api.OverrideBalancerTarget(c, view.APIListen, balancer, target)
	}
	if pin && apiErr != nil {
		// Not persisted: a pin that is not live would be a surprise at the
		// next xray start.
		return action(false, "xray_api_unavailable", apiErr.Error())
	}
	if _, err := localctl.UpdateOverrides(cfg.OverridesPath, func(o *localctl.Overrides) error {
		if pin {
			if o.Pins == nil {
				o.Pins = map[string]string{}
			}
			o.Pins[balancer] = node
		} else {
			delete(o.Pins, balancer)
		}
		return nil
	}); err != nil {
		return action(false, "internal", err.Error())
	}
	if pin {
		return action(true, "balancer_pinned", "")
	}
	if apiErr != nil {
		// Released on disk; xray (not answering now) will start without it.
		return action(true, "balancer_unpinned", "xray API unavailable: "+apiErr.Error())
	}
	return action(true, "balancer_unpinned", "")
}

func writeJSON(w io.Writer, v interface{}) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
