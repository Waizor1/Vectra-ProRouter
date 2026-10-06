package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"time"

	"vectra-controller-pro/internal/connectactions"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/jobsafety"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/portfwd"
	"vectra-controller-pro/internal/state"
)

var connectOwnerPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

func (d *daemon) connectBinding() connectactions.Binding {
	b := connectactions.Binding{RouterID: d.st.RouterID}
	if d.st.ClaimOwner != nil && connectOwnerPattern.MatchString(d.st.ClaimOwner.OwnerRef) {
		b.OwnerRef = d.st.ClaimOwner.OwnerRef
	}
	return b
}

var connectRouteExecute = func(d *daemon, c context.Context, a string, p json.RawMessage) localctl.SocketResponse {
	return d.connectRoutingAction(c, a, p)
}

// connectPortForwardsExecute applies Vectra Connect's set_port_forwards: the
// same apply as the router UI's (portfwd.Apply), then the «past the VPN» set
// written at once — this already runs on the daemon's loop, so no socket.
// The answer is a code only: never a label, an address or uci's output.
//
// It runs on the daemon's loop, like every Connect action: jobs and the
// router's own changes are serialized there by design (set_rules and
// set_wifi hold it for an xray restart or a Wi-Fi check just the same). What
// it holds the loop for is bounded — connectPortForwardsWait for the apply,
// a failed reload's restore included, then one small nft write — so a
// hanging fw4 costs one poll, never the loop.
var connectPortForwardsExecute = func(d *daemon, ctx context.Context, rules []portfwd.Rule) (string, bool) {
	c, cancel := context.WithTimeout(ctx, connectPortForwardsWait)
	_, err := portfwd.Apply(c, portfwdEnv(), rules)
	cancel()
	if err != nil {
		return err.Code, false
	}
	// A set that could not be written is tried again by the loop; the
	// forwards themselves are in.
	_ = d.maybeSyncPortForwards(ctx, true)
	return "applied", true
}

// connectPortForwardsWait bounds a Connect port forward apply on the loop.
const connectPortForwardsWait = 20 * time.Second

var connectWifiExecute = connectApplyWifi
var connectWifiMark = connectMarkWifiOwner
var connectWifiForget = connectForgetWifiOwner
var connectWifiRead = connectReadOwnerWifi
var connectResourceBlocked = func(d *daemon, class string) bool {
	return d.collector == nil || jobsafety.Evaluate(class, d.collector.Resources(), d.cfg.JobSafety).Blocked
}

func (d *daemon) connectFinish(ctx context.Context, j controlplane.Job, journal *connectactions.Journal, b connectactions.Binding, code string, ok bool) error {
	status, terminal := "failure", connectactions.Failed
	if ok {
		status, terminal = "success", connectactions.Succeeded
	}
	if err := journal.Complete(b, j.ID, terminal); err != nil {
		return d.submitFailure(ctx, j, "journal_unavailable")
	}
	return d.finishJob(ctx, j, status, "", "", map[string]interface{}{"code": code})
}
func (d *daemon) jobConnectAction(ctx context.Context, j controlplane.Job, responseRouterID string) error {
	raw, err := json.Marshal(j.Payload)
	if err != nil {
		return d.submitFailure(ctx, j, "invalid_params")
	}
	e, err := connectactions.Parse(raw)
	if err != nil || e.ActionID != j.ID {
		return d.submitFailure(ctx, j, "invalid_params")
	}
	b := d.connectBinding()
	if connectactions.Authorize(e, b, responseRouterID) != nil {
		return d.submitFailure(ctx, j, "ownership_rejected")
	}
	journal, err := connectactions.OpenJournal(d.cfg.StatePath + ".connect-actions.json")
	if err != nil {
		return d.submitFailure(ctx, j, "journal_unavailable")
	}
	if e.Action == "reboot" && d.maintenancePendingReboot(b.OwnerRef, j.ID) {
		d.ackJob(ctx, j)
		return errMaintenanceActionPending
	}
	rec, start, err := journal.Begin(b, e)
	if err != nil && !errors.Is(err, connectactions.ErrSensitiveReplay) {
		return d.submitFailure(ctx, j, "replay_rejected")
	}
	if !start {
		status := "failure"
		if rec.Status == connectactions.Succeeded {
			status = "success"
		}
		return d.finishJob(ctx, j, status, "", "", map[string]interface{}{"code": "replayed", "interrupted": rec.Status == connectactions.Interrupted})
	}
	d.st.CurrentJob = state.CurrentJob{JobID: j.ID, JobType: j.Type, AcceptedAt: nowRFC3339()}
	if d.persist() != nil {
		return d.connectFinish(ctx, j, journal, b, "journal_unavailable", false)
	}
	d.ackJob(ctx, j)
	class := "apply_xray_config"
	if e.Action == "update_now" {
		class = "update_controller"
	}
	if e.Action != "reboot" && e.Action != "set_auto_update" {
		if connectResourceBlocked(d, class) {
			return d.connectFinish(ctx, j, journal, b, "resource_guard", false)
		}
	}
	params, _ := json.Marshal(e.Params)
	switch e.Action {
	case "select_entry", "set_rules", "set_service":
		r := connectRouteExecute(d, ctx, e.Action, params)
		code := r.Code
		if code == "" {
			code = "applied"
		}
		return d.connectFinish(ctx, j, journal, b, code, r.OK)
	case "set_wifi":
		// The change makes readable back only what it set, plus what this
		// owner had already set (kept, read before the marker is forgotten)
		// where the fingerprint shows it is still unchanged: never another
		// band set by someone else or a later local change.
		wifi, _ := e.Params.(connectactions.WiFi)
		k := connectWifiReadbackKey(d.st.DevicePrivateKey)
		kept := connectWifiOwnerAPs(d.cfg, b.RouterID, b.OwnerRef)
		if err := connectWifiForget(d.cfg); err != nil {
			return d.connectFinish(ctx, j, journal, b, "secret_binding_unavailable", false)
		}
		code, ok := connectWifiExecute(ctx, d.cfg, params)
		if ok && connectWifiMark(d.cfg, k, b.RouterID, b.OwnerRef, wifi, kept) != nil {
			code, ok = "secret_binding_unavailable", false
		}
		if !ok && len(kept) > 0 {
			// Failure: restore this owner's earlier marks that still match.
			_ = connectWifiMark(d.cfg, k, b.RouterID, b.OwnerRef, connectactions.WiFi{}, kept)
		}
		return d.connectFinish(ctx, j, journal, b, code, ok)
	case "set_port_forwards":
		pf, _ := e.Params.(connectactions.PortForwards)
		code, ok := connectPortForwardsExecute(d, ctx, pf.Rules)
		return d.connectFinish(ctx, j, journal, b, code, ok)
	case "reboot", "update_now", "set_auto_update":
		p := map[string]interface{}{}
		if json.Unmarshal(params, &p) != nil {
			return d.connectFinish(ctx, j, journal, b, "invalid_params", false)
		}
		err = d.connectMaintenance(ctx, j, b.OwnerRef, e.Action, p)
		if errors.Is(err, errControllerRestartRequested) || errors.Is(err, errMaintenanceActionPending) {
			return err
		}
		terminal := connectactions.Failed
		if !errors.Is(err, errMaintenanceActionFailed) && (err == nil || (d.st.PendingJobResult != nil && d.st.PendingJobResult.Status == "success")) {
			terminal = connectactions.Succeeded
		}
		if journal.Complete(b, j.ID, terminal) != nil {
			return errors.New("connect journal unavailable")
		}
		return err
	}
	return d.connectFinish(ctx, j, journal, b, "unsupported", false)
}

func (d *daemon) connectCapabilities() map[string]bool {
	out := map[string]bool{}
	b := d.connectBinding()
	if b.OwnerRef == "" || b.RouterID == "" {
		return out
	}
	out["restart_vpn"] = d.desired != nil
	out["refresh_subscription"] = d.desired != nil
	if d.cfg.RouteSource == "" && d.desired != nil {
		if _, err := localctl.LoadEntries(d.cfg.EntriesPath); err == nil {
			out["select_entry"], out["set_rules"], out["set_service"] = true, true, true
			out["set_service_auto"] = true // set_service takes entryId ":auto"
		}
	}
	for _, radio := range connectSetup(context.Background()).Wifi.Radios {
		if radio.AP && radio.Device != "" {
			out["set_wifi"] = true
			out["set_wifi_band"] = true // set_wifi takes band: one band's radios only
			break
		}
	}
	// Port forwards are fw4 redirects: offered where fw4 keeps its config.
	if fi, err := os.Stat(portfwdEnv().FirewallConfig); err == nil && fi.Mode().IsRegular() {
		out["set_port_forwards"] = true
	}
	if fi, err := os.Stat("/sbin/reboot"); err == nil && fi.Mode().IsRegular() && fi.Mode()&0111 != 0 {
		out["reboot"] = true
	}
	if d.maintenanceFeedAvailable(b.OwnerRef) {
		out["update_now"], out["set_auto_update"] = true, true
	}
	return out
}
func (d *daemon) enrichConnectCheckin(inv *controlplane.RouterInventory) {
	b := d.connectBinding()
	if inv.Connect == nil || b.OwnerRef == "" || inv.Connect.OwnerRef != b.OwnerRef {
		return
	}
	// Clone before confidential enrichment; shared snapshots remain secret-free.
	t := *inv.Connect
	inv.Connect = &t
	auto, available, err := d.maintenanceSnapshot(b.OwnerRef)
	if err == nil {
		inv.Connect.AutoUpdate = &auto
		if available != "" {
			inv.Connect.AvailableVersion = available
		}
	}
	wifi := connectWifiRead(d.cfg, connectWifiReadbackKey(d.st.DevicePrivateKey), b.RouterID, b.OwnerRef)
	if len(wifi) > 0 {
		safe := make([]controlplane.ConnectWifi, 0, len(wifi))
		for _, w := range wifi {
			safe = append(safe, controlplane.ConnectWifi{Band: w.Band, SSID: w.SSID, Password: w.Password})
		}
		inv.Connect.Wifi = &safe
	}
}

var connectReceiptSave = func(d *daemon, owner, jobID string) error { return d.connectMaintenanceResultDelivered(owner, jobID) }

func (d *daemon) connectRecoverDelivered(jobID string, status string) error {
	b := d.connectBinding()
	if b.OwnerRef == "" {
		return nil
	}
	if status == "accepted" {
		return connectReceiptSave(d, b.OwnerRef, jobID)
	}
	journal, err := connectactions.OpenJournal(d.cfg.StatePath + ".connect-actions.json")
	if err != nil {
		return errors.New("connect journal unavailable")
	}
	_, found, err := journal.Lookup(b, jobID)
	if err != nil {
		return errors.New("connect receipt unavailable")
	}
	if found {
		terminal := connectactions.Failed
		if status == "success" {
			terminal = connectactions.Succeeded
		}
		if journal.Complete(b, jobID, terminal) != nil {
			return errors.New("connect receipt unavailable")
		}
	}
	return connectReceiptSave(d, b.OwnerRef, jobID)
}
