package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/firewall"
	"vectra-controller-pro/internal/setup"
	"vectra-controller-pro/internal/uiapi"
)

// The setup wizard's methods (ui/contract/README.md, "Setup wizard"): what a
// customer runs after unboxing — a look at the internet connection (the
// router sets it up itself), the Wi-Fi tuned and its networks named, linking
// the router to their Vectra account. They are the simple view, so the
// operator's lock never refuses them.
var rpcdSetupSignatures = map[string]map[string]interface{}{
	"setup":         {},
	"wan_check":     {},
	"wifi_scan":     {},
	"set_wifi":      {"radios": map[string]interface{}{}},
	"optimize_wifi": {"channels": map[string]interface{}{}, "radios": map[string]interface{}{}},
	"finish_setup":  {},
}

func init() {
	for m, sig := range rpcdSetupSignatures {
		rpcdSignatures[m] = sig
	}
}

// Seams: tests point the wizard at a fake router and never spawn anything.
var (
	rpcdSetupEnv = setup.RouterEnv
	rpcdSpawn    = spawnDetached
	rpcdChecker  = func(cfg agentcfg.Config) setup.Checker {
		return setup.Checker{Mark: firewall.DefaultControlMark, PanelURL: cfg.ControlURL}
	}
)

// rpcdSetupCall answers the wizard's methods; ok is false for any other.
func rpcdSetupCall(ctx context.Context, cfg agentcfg.Config, method string, params []byte) (out interface{}, ok bool) {
	switch method {
	case "setup":
		return rpcdSetup(ctx, cfg), true
	case "wan_check":
		return rpcdWanCheck(ctx, cfg), true
	case "wifi_scan":
		return rpcdWifiScan(), true
	case "set_wifi":
		return rpcdSetWifi(ctx, params), true
	case "optimize_wifi":
		return rpcdOptimizeWifi(ctx, params), true
	case "finish_setup":
		if err := setup.Finish(ctx, rpcdSetupEnv()); err != nil {
			return action(false, "internal", err.Error()), true
		}
		return action(true, "setup_finished", ""), true
	}
	return nil, false
}

func rpcdSetup(ctx context.Context, cfg agentcfg.Config) uiapi.Setup {
	facts := setup.Read(ctx, rpcdSetupEnv())
	in := rpcdGather(ctx, uiapi.RouterEnv(cfg, controllerVersion()), uiapi.Need{Runtime: true})
	_, present, err := operatorConfig(cfg)
	bot, owner := persistedClaim(cfg)
	return uiapi.BuildSetup(facts, in.Runtime, present && err == nil, bot, owner)
}

// rpcdWanCheck looks at the router's own way out, within setup.CheckBudget.
func rpcdWanCheck(ctx context.Context, cfg agentcfg.Config) uiapi.WanCheck {
	ctx, cancel := context.WithTimeout(ctx, setup.CheckBudget)
	defer cancel()
	wan := setup.ReadWan(ctx, rpcdSetupEnv())
	return uiapi.BuildWanCheck(wan, rpcdChecker(cfg).Run(ctx, wan.DNS), time.Now())
}

// rpcdWifiScan is the air as the last optimize_wifi heard it — wifi_scan
// never scans: a sweep stalls the access points, and only optimize_wifi has
// the person's consent to disturb the Wi-Fi.
func rpcdWifiScan() uiapi.WifiScan {
	return uiapi.BuildWifiScan(setup.LoadScan(rpcdSetupEnv()))
}

// wifiParams is what set_wifi and optimize_wifi take. Anything else — the old
// {ssid, key} form among it — is refused.
type wifiParams struct {
	Channels map[string]int              `json:"channels"`
	Radios   map[string]setup.WifiChange `json:"radios"`
}

func decodeWifiParams(params []byte) (wifiParams, error) {
	var p wifiParams
	if len(params) == 0 {
		return p, nil
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, errors.New(`params must be {"channels": {"<radio>": <channel>}, "radios": {"<radio>": {"ssid": "…", "key": "…"}}}`)
	}
	return p, nil
}

// rpcdSetWifi names the radios' networks: {"radios": {"radio0": {"ssid",
// "key"}}}; the radios left out are not touched.
func rpcdSetWifi(ctx context.Context, params []byte) uiapi.Action {
	p, err := decodeWifiParams(params)
	if err != nil || p.Channels != nil {
		return action(false, "invalid_params", `params must be {"radios": {"<radio>": {"ssid": "…", "key": "…"}}}`)
	}
	return rpcdWifiChange(ctx, setup.WifiRequest{Radios: p.Radios}, "wifi_set")
}

// rpcdOptimizeWifi tunes every radio and, in the same transaction and the
// same restart, names the networks it is given (a person on the Wi-Fi who is
// renaming and tuning it would not be there for a second call).
func rpcdOptimizeWifi(ctx context.Context, params []byte) uiapi.Action {
	p, err := decodeWifiParams(params)
	if err != nil {
		return action(false, "invalid_params", err.Error())
	}
	return rpcdWifiChange(ctx, setup.WifiRequest{Tune: true, Channels: p.Channels, Radios: p.Radios}, "wifi_optimized")
}

// rpcdWifiChange runs a Wi-Fi change under the Wi-Fi lock: plan (a refusal
// changes nothing), stage, start the helper — it waits for the word — commit
// with the restart's state set to "applying" before the answer, then give
// the word: the helper, holding the lock, restarts the Wi-Fi a few seconds
// after this answer has reached the browser, checks it came up, and rolls
// back when a radio that was up is not. A helper that cannot be started is a
// failure with the staged change dropped — never a success with no restart.
func rpcdWifiChange(ctx context.Context, req setup.WifiRequest, code string) uiapi.Action {
	env := rpcdSetupEnv()
	lock, err := setup.LockWifi(env)
	switch {
	case errors.Is(err, setup.ErrBusy):
		return action(false, "busy", err.Error())
	case err != nil:
		return action(false, "internal", err.Error())
	}
	defer lock.Close() // the helper has its own descriptor of the lock
	plan, err := setup.PlanWifi(ctx, env, req)
	switch {
	case errors.Is(err, setup.ErrPending):
		return action(false, "busy", err.Error())
	case setup.IsInvalid(err):
		return action(false, "invalid_params", err.Error())
	case setup.IsUnsupported(err):
		return action(false, "unsupported", err.Error())
	case err != nil:
		return action(false, "internal", err.Error())
	}
	staged, err := setup.Stage(ctx, env, plan)
	if err != nil {
		return action(false, "internal", err.Error())
	}
	start, err := rpcdSpawn(lock, "setup", "wifi-apply")
	if err != nil {
		staged.Abort()
		return action(false, "apply_failed", "the Wi-Fi restart could not be started, so nothing was changed: "+err.Error())
	}
	if err := staged.Commit(ctx); err != nil {
		_ = start(false)
		if errors.Is(err, setup.ErrPending) {
			return action(false, "busy", err.Error())
		}
		return action(false, "internal", err.Error())
	}
	if err := start(true); err != nil {
		staged.Undo()
		return action(false, "apply_failed", "the Wi-Fi restart did not start, so the change was taken back: "+err.Error())
	}
	return action(true, code, strings.Join(plan.Notes, ". "))
}

// operatorConfig reads the operator config the panel delivered, as the daemon
// does: present says the file is there; err says the router would not take it.
func operatorConfig(cfg agentcfg.Config) (c *config.Config, present bool, err error) {
	if _, serr := os.Stat(cfg.XrayConfigPath); errors.Is(serr, os.ErrNotExist) {
		return nil, false, nil
	}
	c, err = config.Load(cfg.XrayConfigPath)
	return c, true, err
}

// panelLocked: the panel set ui.lock in the operator config it delivered. A
// config the router cannot read locks — it cannot tell whether the panel did.
func panelLocked(cfg agentcfg.Config) bool {
	c, present, err := operatorConfig(cfg)
	if !present {
		return false
	}
	return err != nil || (c.UI != nil && c.UI.Lock)
}

// persistedClaim is what the daemon kept of the panel's word on claiming —
// the Vectra bot it named and the router's owner — linked or not. Only these
// two fields are read from the state file.
func persistedClaim(cfg agentcfg.Config) (bot string, owner *uiapi.ClaimOwner) {
	raw, err := os.ReadFile(cfg.StatePath)
	if err != nil {
		return "", nil
	}
	var st struct {
		BotUsername string `json:"bot_username"`
		ClaimOwner  *struct {
			Label string `json:"label"`
		} `json:"claim_owner"`
	}
	if json.Unmarshal(raw, &st) != nil {
		return "", nil
	}
	if botUsername(st.BotUsername) {
		bot = st.BotUsername
	}
	if st.ClaimOwner != nil {
		owner = &uiapi.ClaimOwner{Label: st.ClaimOwner.Label}
	}
	return bot, owner
}

// spawnDetached starts `vctl <args>` in a session of its own, so it outlives
// this rpcd call and LuCI's timeout, with lock (when given) as its fd 3 — the
// lock stays held until it exits. It waits for the word on its stdin: start
// true sends it, false lets it go without doing anything. Its output goes
// nowhere: what it did is in the apply file.
func spawnDetached(lock *os.File, args ...string) (start func(proceed bool) error, err error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	defer devnull.Close()
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = r, devnull, devnull
	if lock != nil {
		cmd.ExtraFiles = []*os.File{lock}
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = cmd.Start()
	r.Close()
	if err != nil {
		w.Close()
		return nil, err
	}
	_ = cmd.Process.Release()
	return func(proceed bool) error {
		defer w.Close()
		if !proceed {
			return nil
		}
		_, err := w.Write([]byte("go\n"))
		return err
	}, nil
}
