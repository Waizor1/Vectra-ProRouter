package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/firewall"
	"vectra-controller-pro/internal/geo"
	"vectra-controller-pro/internal/jobsafety"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/rescue"
	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/subscription"
)

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// executeJob acknowledges, resource-gates, and dispatches a single job.
func (d *daemon) executeJob(ctx context.Context, job controlplane.Job, resp controlplane.CheckInResponse) error {
	d.ackJob(ctx, job)
	d.st.CurrentJob = state.CurrentJob{JobID: job.ID, JobType: job.Type, AcceptedAt: nowRFC3339()}
	_ = d.persist()

	if decision := jobsafety.Evaluate(job.Type, d.collector.Resources(), d.cfg.JobSafety); decision.Blocked {
		logging.L().Warn("job blocked by resource guard", "jobId", job.ID, "type", job.Type, "reasons", strings.Join(decision.Reasons, "; "))
		return d.finishJob(ctx, job, "failure", "", "", decision.ResultPayload())
	}

	switch job.Type {
	case "apply_xray_config":
		return d.jobApplyXrayConfig(ctx, job, resp)
	case "refresh_xray_subscriptions":
		return d.jobRefreshSubscriptions(ctx, job)
	case "update_xray_assets":
		return d.jobUpdateAssets(ctx, job)
	case "reload_xray_outbound":
		return d.jobReloadOutbound(ctx, job)
	case "update_controller":
		return d.jobUpdateController(ctx, job)
	case "run_terminal_command":
		return d.jobRunTerminal(ctx, job)
	case "collect_router_logs":
		return d.jobCollectLogs(ctx, job)
	case "enter_direct_mode":
		return d.jobEnterDirect(ctx, job)
	case "reconnect":
		return d.jobReconnect(ctx, job)
	default:
		return d.finishJob(ctx, job, "failure", "", "", map[string]interface{}{"error": "unsupported job type: " + job.Type})
	}
}

// ---- result helpers (persist-then-submit so a network blip retries) -------

func (d *daemon) ackJob(ctx context.Context, job controlplane.Job) {
	_, _ = d.client.SubmitJobResult(ctx, controlplane.JobResultRequest{
		ProtocolVersion: controlplane.ProtocolVersion,
		RouterID:        d.st.RouterID,
		JobID:           job.ID,
		Status:          "accepted",
		Result:          map[string]interface{}{"message": "job accepted"},
	})
}

func (d *daemon) finishJob(ctx context.Context, job controlplane.Job, status, appliedRev, digest string, result map[string]interface{}) error {
	req := controlplane.JobResultRequest{
		ProtocolVersion:   controlplane.ProtocolVersion,
		RouterID:          d.st.RouterID,
		JobID:             job.ID,
		Status:            status,
		AppliedRevisionID: appliedRev,
		ConfigDigest:      digest,
		Result:            result,
	}
	// Journal the result before sending so a crash/blip is recoverable.
	d.st.PendingJobResult = &req
	d.st.CurrentJob = state.CurrentJob{}
	_ = d.persist()

	if _, err := d.client.SubmitJobResult(ctx, req); err != nil {
		logging.L().Warn("job result submit failed; will retry next loop", "jobId", job.ID, "err", err.Error())
		return err
	}
	d.st.PendingJobResult = nil
	_ = d.persist()
	return nil
}

func (d *daemon) submitFailure(ctx context.Context, job controlplane.Job, msg string) error {
	return d.finishJob(ctx, job, "failure", "", "", map[string]interface{}{"error": msg})
}

// ---- core jobs ------------------------------------------------------------

func (d *daemon) jobApplyXrayConfig(ctx context.Context, job controlplane.Job, resp controlplane.CheckInResponse) error {
	rev := d.st.LastDesiredRevision
	if rev == nil {
		if r, err := decodeDesiredRevision(resp.DesiredRevision); err == nil {
			rev = r
		}
	}
	if rev == nil || len(rev.Config) == 0 {
		return d.submitFailure(ctx, job, "apply_xray_config: no desired config available")
	}

	res, err := d.applier.Apply(ctx, rev.Config, d.st.ConfigDigest, fileExists(d.cfg.XrayRenderPath))
	if err != nil {
		return d.submitFailure(ctx, job, "apply: "+err.Error())
	}

	if res.Changed {
		if !d.supStarted {
			d.ensureSupervisor(ctx)
		} else if err := d.sup.Reload(d.supCtx); err != nil {
			logging.L().Warn("xray reload after apply failed", "err", err.Error())
		}
		d.programFirewall(ctx, rev.Config)
	}
	d.st.AppliedRevisionID = rev.ID
	d.st.ConfigDigest = res.AppliedDigest

	return d.finishJob(ctx, job, "success", rev.ID, res.AppliedDigest, map[string]interface{}{
		"noop":       res.Noop,
		"changed":    res.Changed,
		"operations": res.Operations,
		"xrayBytes":  res.XrayBytes,
	})
}

func (d *daemon) jobRefreshSubscriptions(ctx context.Context, job controlplane.Job) error {
	cfg, err := d.loadDesiredConfig()
	if err != nil {
		return d.submitFailure(ctx, job, "refresh: "+err.Error())
	}
	enabled := 0
	added := 0
	for _, sub := range cfg.Subscriptions {
		if !sub.Enabled {
			continue
		}
		enabled++
		fr, err := subscription.Fetch(ctx, subscription.FetchOptions{
			URL:          sub.URL,
			UserAgent:    sub.UserAgent,
			ExtraHeaders: sub.Headers,
		})
		if err != nil {
			logging.L().Warn("subscription fetch failed", "sub", sub.ID, "err", err.Error())
			continue
		}
		parsed := subscription.ParseBody(fr.Body)
		newNodes := subscription.ToConfigNodes(parsed.Nodes, subscription.SubscriptionRef{ID: sub.ID, URL: sub.URL})
		cfg.Nodes = replaceSubscriptionNodes(cfg.Nodes, sub.ID, newNodes)
		added += len(newNodes)
	}

	raw, err := config.Marshal(cfg)
	if err != nil {
		return d.submitFailure(ctx, job, "marshal refreshed config: "+err.Error())
	}
	res, err := d.applier.Apply(ctx, raw, d.st.ConfigDigest, fileExists(d.cfg.XrayRenderPath))
	if err != nil {
		return d.submitFailure(ctx, job, "apply refreshed config: "+err.Error())
	}
	if res.Changed && d.supStarted {
		_ = d.sup.Reload(d.supCtx)
	}
	d.st.ConfigDigest = res.AppliedDigest
	return d.finishJob(ctx, job, "success", d.st.AppliedRevisionID, res.AppliedDigest, map[string]interface{}{
		"subscriptionsRefreshed": enabled,
		"nodesImported":          added,
		"changed":                res.Changed,
	})
}

func (d *daemon) jobUpdateAssets(ctx context.Context, job controlplane.Job) error {
	cfg, err := d.loadDesiredConfig()
	if err != nil {
		return d.submitFailure(ctx, job, "update_assets: "+err.Error())
	}
	dir := cfg.Geo.AssetDir
	if dir == "" {
		dir = "/usr/share/xray"
	}
	hc := &http.Client{Timeout: 60 * time.Second}
	results := map[string]interface{}{}
	updated := 0
	for _, a := range geoAssets(cfg) {
		r := geo.UpdateOne(ctx, dir, a, hc)
		if r.Error != nil {
			results[a.Filename] = map[string]interface{}{"error": r.Error.Error()}
			continue
		}
		results[a.Filename] = map[string]interface{}{"updated": r.Updated, "sha256": r.SHA256, "bytes": r.Bytes}
		if r.Updated {
			updated++
		}
	}
	if updated > 0 && d.supStarted {
		_ = d.sup.Reload(d.supCtx)
	}
	return d.finishJob(ctx, job, "success", "", "", map[string]interface{}{"assets": results, "updatedCount": updated})
}

func (d *daemon) jobReloadOutbound(ctx context.Context, job controlplane.Job) error {
	if !d.supStarted {
		return d.submitFailure(ctx, job, "reload: xray not running")
	}
	if err := d.sup.Reload(d.supCtx); err != nil {
		return d.submitFailure(ctx, job, "reload: "+err.Error())
	}
	return d.finishJob(ctx, job, "success", "", "", map[string]interface{}{"reloaded": true})
}

func (d *daemon) jobEnterDirect(ctx context.Context, job controlplane.Job) error {
	// Tear down the TPROXY firewall so traffic actually flows direct — otherwise
	// packets keep being tproxy'd into a (possibly dead) Xray and black-holed.
	d.tearDownFirewall(ctx)
	d.storeRescueState(rescue.State{Mode: rescue.ModeDirect, LastTransitionAt: time.Now()}, "operator requested direct mode")
	_ = d.persist()
	return d.finishJob(ctx, job, "success", "", "", map[string]interface{}{"enteredDirectMode": true})
}

func (d *daemon) jobReconnect(ctx context.Context, job controlplane.Job) error {
	// Re-program the firewall (behind commit-confirm) and reload Xray so the
	// proxy data plane is actually restored.
	d.reapplyFirewall(ctx)
	if d.supStarted {
		_ = d.sup.Reload(d.supCtx)
	}
	d.storeRescueState(rescue.State{Mode: rescue.ModeProxy, LastTransitionAt: time.Now()}, "operator requested reconnect")
	_ = d.persist()
	return d.finishJob(ctx, job, "success", "", "", map[string]interface{}{"reconnected": true})
}

func (d *daemon) jobRunTerminal(ctx context.Context, job controlplane.Job) error {
	// The command is operator-authored shell delivered by the authenticated
	// panel over HTTPS (token-gated) — the same trust model as the legacy
	// agent's run_terminal_command. It is not untrusted external input.
	cmdStr, _ := job.Payload["command"].(string)
	if strings.TrimSpace(cmdStr) == "" {
		return d.submitFailure(ctx, job, "run_terminal_command: empty command")
	}
	// Honor the panel-provided timeout (schema: 5..120s, default 30); JSON
	// numbers decode to float64 in the payload map.
	timeoutS := 30
	if v, ok := job.Payload["timeoutSeconds"].(float64); ok {
		timeoutS = int(v)
	}
	if timeoutS < 5 {
		timeoutS = 5
	} else if timeoutS > 120 {
		timeoutS = 120
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutS)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "sh", "-c", cmdStr)
	out, err := cmd.CombinedOutput()
	result := map[string]interface{}{"command": cmdStr, "stdout": string(out)}
	if err != nil {
		result["error"] = err.Error()
		return d.finishJob(ctx, job, "failure", "", "", result)
	}
	return d.finishJob(ctx, job, "success", "", "", result)
}

func (d *daemon) jobCollectLogs(ctx context.Context, job controlplane.Job) error {
	sections := map[string]string{}
	for name, args := range map[string][]string{
		"logread": {"logread", "-l", "200"},
		"dmesg":   {"dmesg"},
	} {
		runCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		out, _ := exec.CommandContext(runCtx, args[0], args[1:]...).CombinedOutput()
		cancel()
		sections[name] = tail(string(out), 8000)
	}
	return d.finishJob(ctx, job, "success", "", "", map[string]interface{}{"logSections": sections})
}

// jobUpdateController self-updates the controller package and schedules a
// restart so the init system brings up the new binary.
const proPackageName = "vectra-controller-pro"

func (d *daemon) jobUpdateController(ctx context.Context, job controlplane.Job) error {
	artifactURL, _ := job.Payload["artifactUrl"].(string)
	if artifactURL == "" {
		return d.submitFailure(ctx, job, "update_controller: missing artifactUrl")
	}
	// Panel contract field is `sha256` (packageArtifactPayloadSchema); tolerate
	// the older `checksumSha256` key for forward-compat.
	sha, _ := job.Payload["sha256"].(string)
	if sha == "" {
		sha, _ = job.Payload["checksumSha256"].(string)
	}
	version, _ := job.Payload["version"].(string)
	pkgName, _ := job.Payload["name"].(string)

	// Identity guard: the controller-update lane is engine-agnostic and could
	// hand a pro router the LEGACY agent .ipk. We only ever install our OWN
	// package — refuse anything else rather than overwrite vctl with the agent.
	if !strings.Contains(pkgName, proPackageName) && !strings.Contains(path.Base(artifactURL), proPackageName) {
		return d.submitFailure(ctx, job, fmt.Sprintf("update_controller: refusing artifact that is not %s (name=%q url=%s)", proPackageName, pkgName, artifactURL))
	}
	// Fail closed: never install an unverified root package.
	if sha == "" {
		return d.submitFailure(ctx, job, "update_controller: missing sha256 (refusing unverified install)")
	}

	dest := filepath.Join(os.TempDir(), "vectra-controller-pro-update.ipk")
	gotSha, err := downloadFile(ctx, artifactURL, dest)
	if err != nil {
		return d.submitFailure(ctx, job, "download: "+err.Error())
	}
	if !strings.EqualFold(sha, gotSha) {
		return d.submitFailure(ctx, job, fmt.Sprintf("checksum mismatch: got %s want %s", gotSha, sha))
	}

	installCtx, cancel := context.WithTimeout(ctx, 180*time.Second)
	out, err := exec.CommandContext(installCtx, "opkg", "install", "--force-reinstall", dest).CombinedOutput()
	cancel()
	if err != nil {
		return d.submitFailure(ctx, job, "opkg install: "+err.Error()+": "+tail(string(out), 1000))
	}

	// Journal a pending success result; it is flushed after the restart, once
	// the new binary confirms its runtime version.
	d.st.CurrentJob.ExpectedControllerVersion = version
	d.st.PendingJobResult = &controlplane.JobResultRequest{
		ProtocolVersion: controlplane.ProtocolVersion,
		RouterID:        d.st.RouterID,
		JobID:           job.ID,
		Status:          "success",
		Result: map[string]interface{}{
			"controllerUpdated": true,
			"version":           version,
			"sha256":            gotSha,
		},
	}
	_ = d.persist()

	scheduleControllerRestart()
	return errControllerRestartRequested
}

// ---- helpers --------------------------------------------------------------

func (d *daemon) loadDesiredConfig() (*config.Config, error) {
	raw, err := os.ReadFile(d.cfg.XrayConfigPath)
	if err != nil {
		return nil, fmt.Errorf("read desired config: %w", err)
	}
	return config.Read(strings.NewReader(string(raw)), d.cfg.XrayConfigPath)
}

// replaceSubscriptionNodes drops nodes that came from subID and appends the
// freshly fetched ones, preserving manually-added and other-subscription nodes.
func replaceSubscriptionNodes(existing []config.Node, subID string, fresh []config.Node) []config.Node {
	out := make([]config.Node, 0, len(existing)+len(fresh))
	for _, n := range existing {
		if n.Origin != nil && n.Origin.SubscriptionID == subID {
			continue
		}
		out = append(out, n)
	}
	return append(out, fresh...)
}

func geoAssets(cfg *config.Config) []geo.Asset {
	var assets []geo.Asset
	if cfg.Geo.GeoIPURL != "" {
		assets = append(assets, geo.Asset{Filename: "geoip.dat", URL: cfg.Geo.GeoIPURL})
	}
	if cfg.Geo.GeoSiteURL != "" {
		assets = append(assets, geo.Asset{Filename: "geosite.dat", URL: cfg.Geo.GeoSiteURL})
	}
	for _, e := range cfg.Geo.ExtraAssets {
		assets = append(assets, geo.Asset{Filename: e.Filename, URL: e.URL, ExpectedSHA256: e.SHA256})
	}
	return assets
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// programFirewall renders + applies the TPROXY ruleset behind commit-confirm
// when the desired config defines a tproxy inbound. It confirms immediately if
// the panel is still reachable; otherwise the next successful check-in confirms
// and the detached deadman auto-reverts if connectivity never returns.
func (d *daemon) programFirewall(ctx context.Context, raw []byte) {
	cfg, err := config.Read(bytes.NewReader(raw), "firewall-spec")
	if err != nil {
		logging.L().Warn("firewall: cannot decode desired config", "err", err.Error())
		return
	}
	spec, ok := firewallSpecFromConfig(cfg)
	if !ok {
		return // no tproxy inbound — nothing kernel-side to program
	}
	script, err := firewall.Render(spec)
	if err != nil {
		logging.L().Error("firewall render failed", "err", err.Error())
		return
	}
	if err := d.confirmer.Apply(script, spec); err != nil {
		logging.L().Error("firewall apply failed (deadman armed; auto-reverts unless a check-in confirms)", "err", err.Error())
		return
	}
	// Fast path: confirm immediately if the panel is already reachable. If not,
	// the next successful check-in's unconditional Confirm() disarms the deadman;
	// and if connectivity never returns, the deadman reverts at its timeout.
	if rescue.ProbeAny(ctx, &http.Client{Timeout: 4 * time.Second}, serverHealthURLs(d.cfg.ControlURL)) {
		if err := d.confirmer.Confirm(); err == nil {
			logging.L().Info("firewall change confirmed immediately (panel reachable)")
		}
	}
}

// tearDownFirewall removes the vctl TPROXY table + ip rules so traffic flows
// DIRECT. Best-effort; the revert commands are vctl constants. Disarms any
// pending deadman since we are intentionally direct now.
func (d *daemon) tearDownFirewall(ctx context.Context) {
	cfg, err := d.loadDesiredConfig()
	if err != nil {
		return
	}
	spec, ok := firewallSpecFromConfig(cfg)
	if !ok {
		return
	}
	for _, c := range firewall.RevertCommands(spec) {
		fields := strings.Fields(c)
		if len(fields) == 0 {
			continue
		}
		_ = exec.CommandContext(ctx, fields[0], fields[1:]...).Run()
	}
	_ = d.confirmer.Confirm()
}

// reapplyFirewall re-programs the TPROXY ruleset (reconnect / rescue recovery)
// behind commit-confirm.
func (d *daemon) reapplyFirewall(ctx context.Context) {
	cfg, err := d.loadDesiredConfig()
	if err != nil {
		return
	}
	raw, err := config.Marshal(cfg)
	if err != nil {
		return
	}
	d.programFirewall(ctx, raw)
}

// applyRescueTransition makes the data plane match an auto-rescue decision so
// "direct" really bypasses the proxy and "proxy" really restores it.
func (d *daemon) applyRescueTransition(ctx context.Context, dec rescue.Decision) {
	switch dec.NextMode {
	case rescue.ModeDirect:
		logging.L().Warn("rescue: entering direct mode (tearing down proxy firewall)", "reason", dec.Reason)
		d.tearDownFirewall(ctx)
	case rescue.ModeProxy:
		logging.L().Info("rescue: recovering proxy mode (reapplying firewall)", "reason", dec.Reason)
		d.reapplyFirewall(ctx)
		if d.supStarted {
			_ = d.sup.Reload(d.supCtx)
		}
	}
}

func firewallSpecFromConfig(cfg *config.Config) (firewall.Spec, bool) {
	if cfg == nil || cfg.Inbounds.Tproxy == nil {
		return firewall.Spec{}, false
	}
	t := cfg.Inbounds.Tproxy
	fwmark := t.FwMark
	if fwmark == 0 {
		fwmark = 1
	}
	spec := firewall.DefaultSpec(t.Port, fwmark)
	spec.IPv6Enabled = true
	spec.KillSwitch = t.KillSwitch
	return spec, true
}

func tail(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}

// maxArtifactBytes caps a controller .ipk download so a hostile/oversized
// response cannot fill /tmp.
const maxArtifactBytes = 64 << 20

func downloadFile(ctx context.Context, rawURL, dest string) (string, error) {
	if err := requireHTTPS(rawURL); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	hc := &http.Client{Timeout: 120 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxArtifactBytes)); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// requireHTTPS rejects any non-https URL so a tampered panel response or a
// downgraded link cannot deliver an unencrypted artifact/asset/subscription.
func requireHTTPS(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("refusing non-https url (scheme %q)", u.Scheme)
	}
	return nil
}

// scheduleControllerRestart restarts the controller service shortly after we
// exit, detached so the dying process does not take it down.
func scheduleControllerRestart() {
	// Constant command (no interpolation); sh -c is used only to sequence the
	// delay + restart and detach from the dying process.
	cmd := exec.Command("sh", "-c", "sleep 2; /etc/init.d/vectra-controller-pro restart")
	cmd.Stdout = nil
	cmd.Stderr = nil
	_ = cmd.Start()
}
