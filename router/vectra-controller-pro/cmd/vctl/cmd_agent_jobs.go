package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/vault"

	"vectra-controller-pro/internal/apply"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/firewall"
	"vectra-controller-pro/internal/geo"
	"vectra-controller-pro/internal/jobsafety"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/memguard"
	"vectra-controller-pro/internal/redact"
	"vectra-controller-pro/internal/rescue"
	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/subscription"
	"vectra-controller-pro/internal/supervisor"
)

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// executeJob acknowledges, resource-gates, and dispatches a single job.
func (d *daemon) executeJob(ctx context.Context, job controlplane.Job, resp controlplane.CheckInResponse) error {
	if job.Type == "connect_router_action" {
		return d.jobConnectAction(ctx, job, resp.RouterID)
	}
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
		// Belt and braces. subscription.Fetch already redacts, but a job result
		// is the ONE payload that goes both to the panel database and into
		// state.json (which keep.d preserves into every sysupgrade backup), and
		// plenty of things can quote a URL on the way here — a wrapped
		// third-party error, run_terminal_command stdout, a future call site
		// that forgets. Scrub it once, at the choke point.
		Result: d.scrubJobResult(result),
	}
	// Journal the result before sending so a crash/blip is recoverable.
	d.st.PendingJobResult = &req
	d.st.CurrentJob = state.CurrentJob{}
	if err := d.persist(); err != nil {
		return errors.New("job result journal unavailable")
	}

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

// scrubJobResult removes every configured subscription URL from a job result
// before it is journalled or submitted. Providers put the bearer token in the
// path or query, so subscription.Scrub keeps the scheme+host and drops the rest.
func (d *daemon) scrubJobResult(result map[string]interface{}) map[string]interface{} {
	urls := d.subscriptionURLs()
	if len(urls) == 0 || len(result) == 0 {
		return result
	}
	out := make(map[string]interface{}, len(result))
	for k, v := range result {
		out[k] = scrubValue(v, urls)
	}
	return out
}

// subscriptionURLs lists the subscription URLs this router knows about,
// enabled or not: a URL that was just disabled is still a live secret.
func (d *daemon) subscriptionURLs() []string {
	cfg := d.desired
	if cfg == nil {
		// The operator config is normally loaded at startup; fall back to disk
		// so a result produced before the first check-in is scrubbed too.
		var err error
		if cfg, err = config.LoadSecret(d.cfg.XrayConfigPath); err != nil {
			return nil
		}
	}
	var urls []string
	for _, s := range cfg.Subscriptions {
		if s.URL != "" {
			urls = append(urls, s.URL)
		}
	}
	return urls
}

// scrubValue walks the JSON-shaped values a job result actually carries
// (strings, nested maps, slices) and scrubs every string it reaches. Typed
// values such as []apply.Operation are left alone: they are controller-authored
// constants and never quote a URL.
func scrubValue(v interface{}, urls []string) interface{} {
	switch t := v.(type) {
	case string:
		for _, u := range urls {
			t = subscription.Scrub(t, u)
		}
		return t
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, vv := range t {
			out[k] = scrubValue(vv, urls)
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(t))
		for k, vv := range t {
			for _, u := range urls {
				vv = subscription.Scrub(vv, u)
			}
			out[k] = vv
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, vv := range t {
			out[i] = scrubValue(vv, urls)
		}
		return out
	case []string:
		out := make([]string, len(t))
		for i, vv := range t {
			for _, u := range urls {
				vv = subscription.Scrub(vv, u)
			}
			out[i] = vv
		}
		return out
	default:
		return v
	}
}

// ---- core jobs ------------------------------------------------------------

// jobApplyXrayConfig installs the panel's OPERATOR config (config.Config, four
// blocks) and then re-installs the proxy config the operator config describes.
//
// The panel does NOT carry the proxy config: the provider document would be
// destroyed by the revision pipeline (jsonb storage, stableStringify key
// sorting, value masking, zod parsing). The router fetches it itself.
func (d *daemon) jobApplyXrayConfig(ctx context.Context, job controlplane.Job, resp controlplane.CheckInResponse) error {
	// The revision on THIS response wins over the stored one. They are normally
	// the same, and when they differ the fresh one is what the panel queued this
	// job for; reaching for the stored copy first meant a revision picked up in
	// an earlier loop decided what a later job applied.
	rev, err := decodeDesiredRevision(resp.DesiredRevision)
	if err != nil || rev == nil || len(rev.Config) == 0 {
		rev = d.st.LastDesiredRevision
	}
	if rev == nil || len(rev.Config) == 0 {
		return d.submitFailure(ctx, job, "apply_xray_config: no desired config available")
	}
	// Never apply another engine's config. A panel that predates the xray-direct
	// contract has no engine guard and serves PassWall revisions to any router;
	// config.Read would reject that document field by field, and the failure
	// would read as a corrupt config rather than as the wrong engine.
	if rev.EngineMode != controlplane.EngineModeXrayDirect {
		mode := rev.EngineMode
		if mode == "" {
			mode = "unset"
		}
		return d.submitFailure(ctx, job, fmt.Sprintf(
			"apply_xray_config: revision %s has engineMode %s, not %s — refusing to apply another engine's config",
			rev.ID, mode, controlplane.EngineModeXrayDirect))
	}

	cfg, err := config.Read(bytes.NewReader(rev.Config), "desired-revision")
	if err != nil {
		return d.submitFailure(ctx, job, "decode operator config: "+err.Error())
	}
	// A config carrying a User-Agent the provider punishes never becomes the
	// desired state (the fetcher would refuse to send it anyway).
	if err := config.ValidateSubscriptionAgents(cfg); err != nil {
		return d.submitFailure(ctx, job, "refusing operator config: "+err.Error())
	}
	operatorChanged := d.desired == nil || !sameOperatorConfig(d.desired, cfg)
	if err := config.SaveSecret(d.cfg.XrayConfigPath, cfg); err != nil {
		// Routing by PassWall2's configuration of its own accord, the router
		// keeps doing so on the base config it runs: an operator config
		// not on /etc is not the router's (a restart would not know it), and
		// d.desired is what autoRoute runs on.
		if !d.autoRoute {
			d.desired = cfg
		}
		return d.submitFailure(ctx, job, "persist operator config: "+err.Error())
	}
	d.desired = cfg
	// Routed by PassWall2's configuration until now, of vctl's own accord
	// (auto_route.go): the provider's from here on. Its render is made anew
	// below, and the one that runs keeps the LAN on the VPN until then; if the
	// provider's cannot be made, it keeps running, and the daemon's own
	// refresh tries the provider's again.
	if d.leaveAutoRoute() {
		operatorChanged = true
	}
	d.rebuildApplier()

	providerRaw, source, err := d.providerDocument(ctx, cfg)
	if err != nil {
		return d.submitFailure(ctx, job, "provider config: "+err.Error())
	}

	// A changed tproxy inbound must be re-spliced even when the provider bytes
	// are unchanged, so force a re-render by declaring the current one stale.
	nodesBefore := d.renderNodesKey()
	res, err := d.applyProvider(ctx, providerRaw, operatorChanged)
	if err != nil {
		return d.submitFailure(ctx, job, "apply: "+err.Error())
	}
	reloadedAt := time.Now()
	d.reloadAfterApply(ctx, res, providerRaw)
	// The firewall follows the OPERATOR config (tproxy port/mark/kill-switch),
	// never the provider document.
	if res.Changed || operatorChanged {
		if d.rescueState().Mode == rescue.ModeDirect && !d.directMayEnd(time.Now(), d.renderNodesKey() != nodesBefore) {
			// Direct mode stays: the data plane is not loaded onto a tunnel
			// known dead, nor out of the operator's direct mode. The rescue's
			// way back (or the operator's reconnect) loads the new config.
			logging.L().Info("a new config is applied in direct mode; the router goes back to the tunnel when the rescue or the operator takes it there")
		} else {
			if res.Changed && d.supStarted {
				// The new xray, not the one the reload signalled, gets the
				// DNS redirect (reloadXray).
				d.waitXrayAfter(ctx, reloadedAt)
			}
			d.programFirewall(ctx, cfg)
			d.rescueAfresh(time.Now())
		}
	}
	d.st.AppliedRevisionID = rev.ID

	return d.finishJob(ctx, job, "success", rev.ID, res.AppliedDigest, map[string]interface{}{
		"noop":            res.Noop,
		"changed":         res.Changed,
		"operations":      res.Operations,
		"xrayBytes":       res.XrayBytes,
		"droppedInbounds": res.DroppedInbounds,
		"providerSource":  source,
	})
}

// jobRefreshSubscriptions re-fetches the provider document and installs it.
func (d *daemon) jobRefreshSubscriptions(ctx context.Context, job controlplane.Job) error {
	if d.passwallMode() {
		result := map[string]interface{}{"routeSource": d.cfg.RouteSource}
		if d.nativeMode() {
			// The route policy's own subscriptions, asked now whatever their
			// schedule and whether or not the answer changed.
			refreshed, err := d.refreshNativeAll(ctx)
			if err != nil {
				return d.submitFailure(ctx, job, err.Error())
			}
			result["subscriptions"] = refreshed
		}
		if err := d.syncPassWall(ctx, false); err != nil {
			return d.submitFailure(ctx, job, err.Error())
		}
		d.passwallCfgStamp, d.passwallGeoStamp = d.passwallStamp()
		return d.finishJob(ctx, job, "success", d.st.AppliedRevisionID, d.st.ConfigDigest, result)
	}
	res, meta, err := d.refreshSubscription(ctx)
	if err != nil {
		return d.submitFailure(ctx, job, err.Error())
	}

	result := map[string]interface{}{
		"changed":         res.Changed,
		"noop":            res.Noop,
		"operations":      res.Operations,
		"droppedInbounds": res.DroppedInbounds,
	}
	for k, v := range meta {
		result[k] = v
	}
	return d.finishJob(ctx, job, "success", d.st.AppliedRevisionID, res.AppliedDigest, result)
}

// refreshSubscription fetches the enabled subscription and installs what it
// answers, through the same applier and `xray -test` gate as every apply:
// the refresh job's work, and the daemon's own (maybeRefreshSubscription). A
// failure installs nothing: what runs keeps running.
func (d *daemon) refreshSubscription(ctx context.Context) (apply.ApplyResult, map[string]interface{}, error) {
	cfg, err := d.loadDesiredConfig()
	if err != nil {
		return apply.ApplyResult{}, nil, fmt.Errorf("refresh: %w", err)
	}
	d.desired = cfg
	d.rebuildApplier()

	providerRaw, meta, err := d.fetchProviderDocument(ctx, cfg)
	if err != nil {
		return apply.ApplyResult{}, meta, fmt.Errorf("refresh: %w", err)
	}
	res, err := d.applyProvider(ctx, providerRaw, false)
	if err != nil {
		return res, meta, fmt.Errorf("apply refreshed config: %w", err)
	}
	if res.Changed {
		d.nodeCount = countProviderOutbounds(providerRaw)
		if d.supStarted {
			_ = d.sup.Reload(d.supCtx)
		}
	}
	return res, meta, nil
}

// The daemon refreshes its subscription by itself as well: the provider
// reshuffles its nodes, and a router that no panel sends refresh jobs to —
// the deployed panel has no xray jobs at all — would otherwise run on a
// document that rots until its nodes are gone. Every subscriptionRefreshEvery,
// and at the first poll after a start when the stored document is older than
// that; a failure keeps what runs and is tried again after
// subscriptionRetryAfter. Only a router running a data plane from a config
// with an enabled subscription takes part.
var (
	subscriptionRefreshEvery = 6 * time.Hour
	subscriptionRetryAfter   = 30 * time.Minute
)

// maybeRefreshSubscription is the daemon's own refresh, when it is due.
func (d *daemon) maybeRefreshSubscription(ctx context.Context, now time.Time) {
	if d.desired == nil || !fileExists(d.cfg.XrayRenderPath) || d.passwallMode() {
		// PassWall-compatible routing follows PassWall2's own configuration
		// (maybeSyncPassWall), which its own subscription run keeps current.
		return
	}
	if _, ok := enabledSubscription(d.desired); !ok {
		return
	}
	if d.nextSubscriptionRefresh.IsZero() {
		// The first poll after a start: due at once when the stored document
		// is older than a refresh period (a router that was off for a day),
		// else a period after it was written.
		d.nextSubscriptionRefresh = now
		if st, err := os.Stat(d.cfg.ProviderConfigPath); err == nil {
			d.nextSubscriptionRefresh = st.ModTime().Add(subscriptionRefreshEvery)
		}
	}
	if now.Before(d.nextSubscriptionRefresh) {
		return
	}
	res, _, err := d.refreshSubscription(ctx)
	if err != nil {
		d.nextSubscriptionRefresh = now.Add(subscriptionRetryAfter)
		logging.L().Warn("the subscription's own refresh failed; what runs keeps running", "err", err.Error(),
			"retryIn", subscriptionRetryAfter.String())
		return
	}
	d.nextSubscriptionRefresh = now.Add(subscriptionRefreshEvery)
	logging.L().Info("refreshed the subscription", "changed", res.Changed, "digest", res.AppliedDigest)
	if err := d.persist(); err != nil {
		logging.L().Warn("persist state after the subscription's own refresh", "err", err.Error())
	}
}

// providerDocument returns the last-good provider document from disk, or
// fetches a fresh one when there is none yet.
func (d *daemon) providerDocument(ctx context.Context, cfg *config.Config) ([]byte, string, error) {
	if raw, err := vault.ReadFile(d.cfg.ProviderConfigPath); err == nil && len(raw) > 0 {
		return raw, "cache", nil
	} else if err != nil && !os.IsNotExist(err) {
		return nil, "", errors.New("provider vault unavailable")
	}
	raw, _, err := d.fetchProviderDocument(ctx, cfg)
	if err != nil {
		return nil, "", err
	}
	return raw, "fetch", nil
}

// fetchProviderDocument fetches the enabled subscription and selects the entry
// to adopt. Returns the entry's RAW bytes plus operator-visible metadata.
func (d *daemon) fetchProviderDocument(ctx context.Context, cfg *config.Config) ([]byte, map[string]interface{}, error) {
	sub, ok := enabledSubscription(cfg)
	if !ok {
		return nil, nil, fmt.Errorf("no enabled subscription in the operator config")
	}
	if err := d.device.Validate(); err != nil {
		return nil, nil, fmt.Errorf("%w (the provider refuses a device without a valid x-hwid)", err)
	}
	opts := subscription.FetchOptions{
		URL:       sub.URL,
		UserAgent: sub.UserAgent,
		// Device identity — WITHOUT these the provider answers 403. The daemon
		// used to send only URL/UserAgent/ExtraHeaders, so x-hwid, x-ver-os and
		// x-device-model were never set.
		HWID:         d.device.HWID,
		MAC:          d.device.MAC,
		Model:        d.device.Model,
		OSRelease:    d.device.OSRelease,
		ExtraHeaders: sub.Headers,
		MaxBytes:     sub.MaxBytes,
		HTTPClient:   d.subClient,
	}
	own := ownAgent(sub)
	if own {
		opts.SignUserAgent = uaSigner(d.st, controllerVersion(), time.Now)
	}
	fr, err := subscription.Fetch(ctx, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("subscription %s: %w", sub.ID, err)
	}
	if fr.StatusCode < 200 || fr.StatusCode > 299 {
		return nil, nil, fmt.Errorf("subscription %s: http %d", sub.ID, fr.StatusCode)
	}

	meta := map[string]interface{}{
		"subscriptionId": sub.ID,
		"bodyBytes":      fr.BodyBytes,
		"contentType":    fr.ContentType,
		"bodyFormat":     fr.BodyFormat,
		"ownUserAgent":   own,
	}

	if sub.Mode == config.SubscriptionModeLinkList {
		// Degraded fallback: reachable ONLY when the operator asks for it.
		// There is no builder anymore, so it cannot produce a runnable config.
		pr := subscription.ParseBody(fr.Body, fr.ContentType)
		return nil, meta, fmt.Errorf(
			"subscription %s: mode=link-list parsed %d node(s) but the controller no longer builds configs from URIs; switch the user-agent to a JSON one (e.g. v2rayNG/1.9.5)",
			sub.ID, len(pr.Nodes))
	}

	if (fr.BodyFormat != subscription.FormatJSON || len(fr.Entries) == 0) && own {
		return nil, meta, fmt.Errorf(
			"subscription %s: expected the JSON config variant but got %q (%d bytes, content-type %q) for the router's own user-agent; ask for JSON by the URL (Remnawave: the subscription URL plus /json) or by a response rule for VectraRouter/",
			sub.ID, fr.BodyFormat, fr.BodyBytes, fr.ContentType)
	}
	if fr.BodyFormat != subscription.FormatJSON || len(fr.Entries) == 0 {
		return nil, meta, fmt.Errorf(
			"subscription %s: expected the JSON config variant but got %q (%d bytes, content-type %q); user-agent %q selects the degraded link list — use a JSON one (e.g. v2rayNG/1.9.5)",
			sub.ID, fr.BodyFormat, fr.BodyBytes, fr.ContentType, sub.UserAgent)
	}
	// The whole array is kept, so the router UI can switch locations without
	// asking the provider again.
	d.cacheEntries(sub.ID, fr)
	// The router's own choice (by remark) wins while the provider still
	// offers it; otherwise the panel's.
	idx, local, stale, err := d.resolveEntry(fr.Remarks, sub)
	if ov, oerr := localctl.LoadOverrides(d.cfg.OverridesPath); oerr == nil && ov.EntryDigest != "" {
		idx, err = connectDigestIndex(fr.Entries, ov.EntryDigest)
		if err != nil {
			return nil, meta, errConnectStaleEntry
		}
		local, stale = true, false
	}
	if err != nil {
		return nil, meta, fmt.Errorf("subscription %s: select entry: %w", sub.ID, err)
	}
	meta["entryCount"] = len(fr.Entries)
	meta["entryIndex"] = idx
	meta["entryRemark"] = fr.Remarks[idx]
	meta["entrySource"] = "panel"
	if local {
		meta["entrySource"] = "local"
	}
	if stale {
		meta["localEntryStale"] = true
	}
	return fr.Entries[idx], meta, nil
}

func enabledSubscription(cfg *config.Config) (config.Subscription, bool) {
	for _, s := range cfg.Subscriptions {
		if s.Enabled {
			return s, true
		}
	}
	return config.Subscription{}, false
}

// sameOperatorConfig compares two operator configs by their canonical JSON.
// Used only to decide whether the tproxy inbound must be re-spliced; the
// provider document is never involved.
func sameOperatorConfig(a, b *config.Config) bool {
	ra, err1 := config.Marshal(a)
	rb, err2 := config.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return bytes.Equal(ra, rb)
}

// jobUpdateAssets updates the geo files xray reads. The operator config's
// sources are the provider's routing's, into the directory the running xray
// reads (runningAssetDir), not the config's old default. The native route
// policy's files are its own, from its own sources and checked against every
// category it routes by (updateNativeGeo); PassWall's — in PassWall mode, or
// on the fallback to its directory where vectra-geodata is not installed —
// are PassWall's to keep.
func (d *daemon) jobUpdateAssets(ctx context.Context, job controlplane.Job) error {
	switch {
	case d.nativeMode():
		return d.jobUpdateNativeGeo(ctx, job)
	case d.passwallMode():
		return d.submitFailure(ctx, job, "update_assets: with route_source 'passwall' xray reads PassWall2's geo files; vctl does not replace them")
	}
	dir := d.runningAssetDir()
	switch {
	case dir == config.LegacyGeoAssetDir:
		return d.submitFailure(ctx, job, "update_assets: xray reads PassWall2's geo files in "+dir+" (vectra-geodata is not installed); vctl does not replace them — install vectra-geodata")
	case dir != config.DefaultGeoAssetDir:
		// The directory is the operator config's to name, and the files are
		// written as root: anywhere else — /etc/crontabs, with an asset
		// called "root" — the panel's geo update would be a shell on the
		// router.
		return d.submitFailure(ctx, job, "update_assets: vctl writes geo data only into its own directory "+config.DefaultGeoAssetDir+"; the config names "+dir)
	}
	cfg, err := d.loadDesiredConfig()
	if err != nil {
		return d.submitFailure(ctx, job, "update_assets: "+err.Error())
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
	return d.finishJob(ctx, job, "success", "", "", map[string]interface{}{"assets": results, "updatedCount": updated, "assetDir": dir})
}

// jobUpdateNativeGeo starts the native route policy's geo update now: its
// schedule's work (maybeUpdateNativeGeo), asked for — both files, whatever
// the schedule's switches say, as PassWall2's own update on request does. It
// runs beside the daemon's loop, as the schedule's does: a slow mirror must
// not hold up check-ins, jobs or the rescue. The outcome is in the log; xray
// reads the new files at its next start, which the next sync gives it
// (passwallStamp).
func (d *daemon) jobUpdateNativeGeo(ctx context.Context, job controlplane.Job) error {
	secs, err := loadNativePolicy()
	if err != nil {
		return d.submitFailure(ctx, job, "update_assets: "+err.Error())
	}
	if !d.startNativeGeo(ctx, secs, time.Now(), true) {
		return d.submitFailure(ctx, job, "update_assets: the route policy's geo update is running already")
	}
	return d.finishJob(ctx, job, "success", "", "", map[string]interface{}{"routeSource": routeSourceNative, "started": true, "assetDir": nativeGeoDir})
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
	d.flushResolverCache("direct mode")
	d.storeRescueState(rescue.State{Mode: rescue.ModeDirect, LastTransitionAt: time.Now()}, operatorDirectReason)
	// It lasts until the operator reconnects (operatorDirect).
	d.st.Rescue.Source = rescueSourceOperator
	_ = d.persist()
	return d.finishJob(ctx, job, "success", "", "", map[string]interface{}{"enteredDirectMode": true})
}

func (d *daemon) jobReconnect(ctx context.Context, job controlplane.Job) error {
	// Reload Xray and re-program the firewall (behind commit-confirm) so the
	// proxy data plane is actually restored. Xray first, and the new one
	// running (reloadXray): the resolver's cache is emptied once the DNS
	// redirect is in (programFirewall), and FakeDNS answers handed out before
	// a reload would lead nowhere after it. An xray that does not come back
	// leaves the router as it was.
	if !d.reloadXray(ctx) {
		return d.submitFailure(ctx, job, "reconnect: xray did not come back after its reload")
	}
	d.reapplyFirewall(ctx)
	d.storeRescueState(rescue.State{Mode: rescue.ModeProxy, LastTransitionAt: time.Now()}, "operator requested reconnect")
	_ = d.persist()
	return d.finishJob(ctx, job, "success", "", "", map[string]interface{}{"reconnected": true})
}

func (d *daemon) jobRunTerminal(ctx context.Context, job controlplane.Job) error {
	// The command is operator-authored shell delivered by the authenticated
	// panel over HTTPS (token-gated) — the same trust model as the legacy
	// agent's run_terminal_command. It is root on the router, so it runs only
	// where the router's owner allows the support shell (remote_shell.go).
	if !remoteShellAllowed() {
		return d.submitFailure(ctx, job, remoteShellOff)
	}
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
	// The panel's command is not the controller: it must not inherit the
	// controller's shield from the OOM killer (memguard.JobAdj). The shell
	// drops it itself, before it runs — or forks — anything.
	cmd := exec.CommandContext(runCtx, "sh", "-c",
		fmt.Sprintf("{ echo %d > /proc/self/oom_score_adj; } 2>/dev/null; %s", memguard.JobAdj, cmdStr))
	stdout, stderr := &cappedBuffer{max: terminalOutputMax}, &cappedBuffer{max: terminalOutputMax}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	// A child left in the background with the pipes open must not hold the
	// answer back past the command itself.
	cmd.WaitDelay = 2 * time.Second
	started := time.Now().UTC()
	err := cmd.Run()
	completed := time.Now().UTC()

	// The shape the panel parses (routerTerminalResultPayloadSchema in
	// packages/contracts): without its required timeoutSeconds, startedAt and
	// completedAt the whole payload was dropped, and every answer from vctl
	// arrived as "succeeded, stdout null" — the operator blind on a router
	// vctl runs.
	result := map[string]interface{}{
		"command":         cmdStr,
		"timeoutSeconds":  timeoutS,
		"startedAt":       started.Format(terminalTimeLayout),
		"completedAt":     completed.Format(terminalTimeLayout),
		"durationMs":      completed.Sub(started).Milliseconds(),
		"timedOut":        errors.Is(runCtx.Err(), context.DeadlineExceeded),
		"stdout":          d.redactSupport(stdout.String()),
		"stderr":          d.redactSupport(stderr.String()),
		"stdoutTruncated": stdout.truncated,
		"stderrTruncated": stderr.truncated,
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		result["exitCode"] = 0
	case errors.As(err, &exitErr) && !result["timedOut"].(bool):
		result["exitCode"] = exitErr.ExitCode()
	default:
		result["error"] = err.Error()
		return d.finishJob(ctx, job, "failure", "", "", result)
	}
	return d.finishJob(ctx, job, "success", "", "", result)
}

// redactSupport takes the router's credentials out of support output before
// it goes to the panel's database: the values vctl knows (its panel token,
// its device key, the subscriptions' addresses) wherever they appear, and
// whatever is a credential by its shape (share links, UUIDs, a URL's path
// and query, a secret's value in JSON, key=value or a UCI option — PassWall2's
// nodes, Wi-Fi's key). Hashes and digests stay: support compares them.
func (d *daemon) redactSupport(s string) string {
	for _, known := range []string{d.st.AgentToken, d.st.DevicePrivateKey, d.cfg.AgentToken} {
		if len(known) >= 8 {
			s = strings.ReplaceAll(s, known, "<redacted>")
		}
	}
	for _, u := range d.subscriptionURLs() {
		s = subscription.Scrub(s, u)
	}
	return redact.Credentials(s)
}

// terminalOutputMax caps each stream of a terminal command's answer.
const terminalOutputMax = 64 << 10

// terminalTimeLayout is RFC 3339 in UTC with milliseconds: what the panel's
// z.string().datetime() accepts.
const terminalTimeLayout = "2006-01-02T15:04:05.000Z"

// cappedBuffer keeps the first max bytes written to it and says whether it
// dropped any.
type cappedBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room < len(p) {
		c.truncated = true
		if room > 0 {
			c.buf.Write(p[:room])
		}
		return len(p), nil
	}
	return c.buf.Write(p)
}

func (c *cappedBuffer) String() string { return c.buf.String() }

func (d *daemon) jobCollectLogs(ctx context.Context, job controlplane.Job) error {
	sections := map[string]string{}
	for name, args := range map[string][]string{
		"logread": {"logread", "-l", "200"},
		"dmesg":   {"dmesg"},
	} {
		runCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		out, _ := exec.CommandContext(runCtx, args[0], args[1:]...).CombinedOutput()
		cancel()
		sections[name] = d.redactSupport(tail(string(out), 8000))
	}
	return d.finishJob(ctx, job, "success", "", "", map[string]interface{}{"logSections": sections})
}

// jobUpdateController self-updates the controller package — only one the
// signed Vectra feed publishes (signed_feed.go) — and schedules a restart so
// the init system brings up the new binary.
const proPackageName = "vectra-controller-pro"

func (d *daemon) jobUpdateController(ctx context.Context, job controlplane.Job) error {
	err := d.updateController(ctx, job)
	if errors.Is(err, errControllerUpToDate) {
		return d.finishJob(ctx, job, "success", "", "", map[string]interface{}{"controllerUpdated": false, "upToDate": true})
	}
	return err
}

// updateController installs the job's signed package. A package already
// installed is errControllerUpToDate, before anything is downloaded; every
// other outcome is reported here.
func (d *daemon) updateController(ctx context.Context, job controlplane.Job) error {
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
	// The panel's contract names the version artifactVersion
	// (updateControllerJobPayloadSchema); vctl read `version` first.
	version, _ := job.Payload["version"].(string)
	if version == "" {
		version, _ = job.Payload["artifactVersion"].(string)
	}
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
	// Only a package the signed Vectra feed publishes (signed_feed.go), and
	// that before anything is downloaded. The feed's entry has the job's
	// sha256, and the download must have it too: what opkg installs is the
	// package the feed's key vouched for.
	verifiedPackage, err := signedFeedPackage(ctx, sha, version)
	if err != nil {
		return d.submitFailure(ctx, job, notInSignedFeed+": "+err.Error())
	}
	if err := signedControllerVersionFloor(ctx, verifiedPackage.Version); errors.Is(err, errControllerUpToDate) {
		return err
	} else if err != nil {
		return d.submitFailure(ctx, job, "update_controller: "+err.Error()+" (nothing installed)")
	}

	dest := filepath.Join(os.TempDir(), controllerUpdateFile)
	// Gone however the update ends: the package is 4.4 MB of a 234 MB
	// router's RAM (/tmp), and it stayed there after every update. opkg is
	// done with it once runControllerInstall returns.
	defer os.Remove(dest)
	gotSha, err := downloadFile(ctx, artifactURL, dest)
	if err != nil {
		return d.submitFailure(ctx, job, "download: "+err.Error())
	}
	if !strings.EqualFold(sha, gotSha) {
		return d.submitFailure(ctx, job, fmt.Sprintf("checksum mismatch: got %s want %s", gotSha, sha))
	}

	installCtx, cancel := context.WithTimeout(ctx, 180*time.Second)
	out, err := runControllerInstall(installCtx, dest)
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
	if err := d.persist(); err != nil {
		return errors.New("update journal unavailable")
	}

	scheduleControllerRestart()
	return errControllerRestartRequested
}

// controllerUpdateFile is where update_controller downloads the package, in
// /tmp. internal/tune knows the name (its leftovers): a package an update
// left behind is removed at the daemon's next start.
const controllerUpdateFile = "vectra-controller-pro-update.ipk"

// ---- helpers --------------------------------------------------------------

func (d *daemon) loadDesiredConfig() (*config.Config, error) {
	raw, err := vault.ReadFile(d.cfg.XrayConfigPath)
	if err != nil {
		return nil, fmt.Errorf("read desired config: %w", err)
	}
	return config.Read(strings.NewReader(string(raw)), d.cfg.XrayConfigPath)
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
// when the OPERATOR config defines a tproxy inbound. It takes the decoded
// config directly: it used to re-decode the desired revision bytes through
// config.Read, which cannot work now that the proxy config is a provider
// document (config.Read uses DisallowUnknownFields and would reject it).
//
// It is confirmed by the router's own proof that it still reaches the
// internet (confirmFirewall, fw_confirm.go), tried again until shortly before
// the deadman wakes; the panel's probe and a successful check-in confirm too,
// but nothing waits for them. The detached deadman reverts a ruleset nothing
// confirmed.
func (d *daemon) programFirewall(ctx context.Context, cfg *config.Config) {
	d.programFirewallWithin(ctx, cfg, dnsRedirectWait)
}

// programFirewallWithin is programFirewall waiting at most dnsWait for xray
// to answer on its DNS inbound: none at all when the DNS watch takes a dead
// inbound's redirect out — every second of waiting is a second the LAN
// resolves nothing.
func (d *daemon) programFirewallWithin(ctx context.Context, cfg *config.Config, dnsWait time.Duration) {
	spec, ok := firewallSpecFromConfig(cfg)
	spec = withLANDevices(withLoadGuards(spec, d.cfg))
	if !ok {
		return // no tproxy inbound — nothing kernel-side to program
	}
	if len(spec.LANDevices) > 0 {
		d.lanDevs = spec.LANDevices
	}
	d.addDNSRedirect(ctx, &spec, dnsWait)
	d.carryFakeDNS(&spec)
	script, err := firewall.Render(spec)
	if err != nil {
		logging.L().Error("firewall render failed", "err", err.Error())
		return
	}
	applyRuleset := d.confirmer.Apply
	if d.applyRuleset != nil {
		applyRuleset = d.applyRuleset
	}
	// The deadman is armed inside the apply: its clock starts no later.
	armedAt := d.now()
	if err := applyRuleset(script, spec); err != nil {
		logging.L().Error("firewall apply failed (deadman armed; it reverts the ruleset)", "err", err.Error())
		return
	}
	oldPort, redirected, upstreams := 0, false, ""
	if d.fwProgrammed != nil {
		oldPort, redirected = redirectPort(*d.fwProgrammed)
		upstreams = redirectUpstreams(*d.fwProgrammed)
	}
	key := dnsRedirectKey(spec) + fakeDNSKey(spec)
	d.fwProgrammed = &key
	// A new table: its direct sets are empty until loaded again, and so is
	// the port forwards' set.
	d.directLoaded, d.directFailKey, d.directPartial, d.directCount = "", "", false, 0
	d.pfLoaded = ""
	if key != "" {
		logging.L().Info("the router's resolver asks through the tunnel", "redirect", key)
	}
	newPort, nowRedirected := redirectPort(key)
	if nowRedirected != redirected {
		d.dnsPathGen++
	}
	// The table changed; the flows the kernel already tracks did not
	// (forgetRedirectedFlows, forgetOpenPathDNS). Before the cache is
	// emptied, so the resolver asks again by the table as it is now.
	if redirected && (!nowRedirected || newPort != oldPort) {
		d.forgetRedirectedFlows(oldPort)
	}
	if nowRedirected && (!redirected || newPort != oldPort || upstreamsAdded(upstreams, redirectUpstreams(key))) {
		d.forgetOpenPathDNS()
	}
	if nowRedirected && !redirected {
		// The resolver asked over the open path until now — direct mode, a
		// released router, a first config, xray's DNS inbound down: what it
		// cached then are real addresses, and the ISP's forged ones for
		// blocked sites, which would keep those sites off the tunnel until
		// they expire.
		d.flushResolverCache("the resolver asks through the tunnel again")
		if st := d.xrayStatus(); d.supStarted && st.State == supervisor.StateRunning {
			// This start's FakeDNS answers are flushed already
			// (flushAfterXrayRestart).
			d.flushedStart = st.StartedAt
		}
	} else if nowRedirected && upstreamsAdded(upstreams, redirectUpstreams(key)) {
		// New WAN resolvers under a redirect already in force (a DHCP
		// renewal): one the redirect did not take may have answered. One
		// that is gone leaves nothing to empty.
		d.dnsPathGen++
		d.flushResolverCache("the resolver's servers changed")
	}
	programmed := spec
	d.fwSpec = &programmed
	d.confirmFirewall(ctx, armedAt)
	d.maybeLoadDirect(ctx)
	d.maybeSyncPortForwards(ctx, true)
}

// tearDownFirewall removes the vctl TPROXY table + ip rules so traffic flows
// DIRECT. Best-effort; the revert commands are vctl constants. Disarms any
// pending deadman since we are intentionally direct now.
func (d *daemon) tearDownFirewall(ctx context.Context) {
	cfg, err := d.loadDesiredConfig()
	if err != nil {
		return
	}
	_ = d.unloadDataPlane(ctx, cfg)
}

// unloadDataPlane is tearDownFirewall for the ruleset cfg describes; false
// when cfg describes none.
func (d *daemon) unloadDataPlane(ctx context.Context, cfg *config.Config) bool {
	spec, ok := firewallSpecFromConfig(cfg)
	spec = withLoadGuards(spec, d.cfg)
	if !ok {
		return false
	}
	d.directLoaded, d.pfLoaded = "", ""
	oldPort, redirected := 0, false
	if d.fwProgrammed != nil {
		if oldPort, redirected = redirectPort(*d.fwProgrammed); redirected {
			d.dnsPathGen++
		}
	}
	// Nothing is redirected any more: the DNS watch must not take this for a
	// loaded data plane to correct.
	d.fwProgrammed = nil
	for _, c := range firewall.RevertCommands(spec) {
		fields := strings.Fields(c)
		if len(fields) == 0 {
			continue
		}
		_ = d.runFirewallCmd(ctx, fields[0], fields[1:]...)
	}
	if redirected {
		d.forgetRedirectedFlows(oldPort)
	}
	// The set went with the table: «past the VPN» is recorded as it is now
	// (not in effect — or, in rescue's direct mode, everything is).
	_ = d.maybeSyncPortForwards(ctx, false)
	_ = d.confirmer.Confirm()
	d.firewallConfirmed()
	d.fwSpec = nil
	return true
}

// runFirewallCommand is the default d.runFirewallCmd: the real OS. The seam
// exists so the shutdown path can be tested without root or nftables, mirroring
// firewall.CommitConfirmer's injectable runCmd.
func runFirewallCommand(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}

// reapplyFirewall re-programs the TPROXY ruleset (reconnect / rescue recovery)
// behind commit-confirm.
// dataPlaneMissing reports whether the data plane should be loaded and is
// not: a configured router in proxy mode, xray running from its render, and
// no vctl table in the kernel — the commit-confirm deadman reverted it for
// want of a confirmation, or something flushed it. Never in direct mode: the
// rescue and the operator's direct mode take it down on purpose.
func (d *daemon) dataPlaneMissing(ctx context.Context) bool {
	if d.desired == nil || !d.supStarted || !fileExists(d.cfg.XrayRenderPath) {
		return false
	}
	if d.rescueState().Mode == rescue.ModeDirect {
		return false
	}
	spec, ok := firewallSpecFromConfig(d.desired)
	spec = withLoadGuards(spec, d.cfg)
	if !ok {
		return false
	}
	return !d.tableLoaded(ctx, spec.TableName)
}

// ensureDataPlane loads the data plane again when dataPlaneMissing. Without
// it a commit-confirm revert was for good: on the test router one revert in
// the first minute left the whole trial direct — vctl up, checking in,
// carrying nothing.
func (d *daemon) ensureDataPlane(ctx context.Context) {
	if d.dataPlaneMissing(ctx) {
		logging.L().Warn("the data plane should be loaded and is not (a commit-confirm revert, or a flush); loading it again")
		d.programFirewall(ctx, d.desired)
		return
	}
	if d.rescueState().Mode != rescue.ModeDirect && d.dnsRedirectStale(ctx) {
		logging.L().Warn("the data plane redirects DNS other than the running render and dnsmasq call for; loading it again")
		d.programFirewall(ctx, d.desired)
	}
}

// nftTableLoaded is the default d.tableLoaded: the kernel has table inet name.
// Terse (-t): it runs every loop, and listed whole the table brings its direct
// set along — nft holding 15k ranges peaks at 23 MB just to print them, 2.5
// MB without (measured on the data-plane stand).
func nftTableLoaded(ctx context.Context, name string) bool {
	if name == "" {
		name = "vctl"
	}
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return exec.CommandContext(c, "nft", "-t", "list", "table", "inet", name).Run() == nil
}

func (d *daemon) reapplyFirewall(ctx context.Context) {
	cfg, err := d.loadDesiredConfig()
	if err != nil {
		return
	}
	d.programFirewall(ctx, cfg)
}

// applyRescueTransition makes the data plane match an auto-rescue decision so
// "direct" really bypasses the proxy and "proxy" really restores it.
func (d *daemon) applyRescueTransition(ctx context.Context, dec rescue.Decision) {
	switch dec.NextMode {
	case rescue.ModeDirect:
		logging.L().Warn("rescue: entering direct mode (tearing down proxy firewall)", "reason", dec.Reason)
		d.tearDownFirewall(ctx)
		// Out of the tunnel's way, its FakeDNS answers lead nowhere.
		d.flushResolverCache("direct mode")
	case rescue.ModeProxy:
		logging.L().Info("rescue: recovering proxy mode (reapplying firewall)", "reason", dec.Reason)
		// Xray first, the new one running (reloadXray), then the data plane:
		// programming it waits for xray to answer on its DNS inbound and then
		// empties the resolver's cache (programFirewall), so no answer from
		// direct mode — a real address, or one the ISP forged for a blocked
		// site — keeps those sites around the tunnel until it expires (vctl
		// r17, 2026-10-04: www.youtube.com on a real IPv6 a minute after the
		// return). The other way round, FakeDNS answers handed out between
		// the two would lead nowhere after the reload.
		if !d.reloadXray(ctx) {
			// No xray to carry the LAN: direct stays, the cooldown before
			// the next try.
			st := d.rescueState()
			st.Mode, st.DirectSuccessCount, st.LastTransitionAt = rescue.ModeDirect, 0, time.Now()
			d.storeRescueState(st, "xray did not come back after its reload; staying direct")
			return
		}
		d.reapplyFirewall(ctx)
	}
}

// withLoadGuards applies the owner's switches for the load guards (UCI
// p2p_bypass, admit_rate, admit_total_rate, pace_rate, dns_rate) and for IPv6
// (UCI ipv6) to a firewall spec; a rate's burst is four seconds of it — the
// DNS door's twenty: DNS comes in page loads.
func withLoadGuards(spec firewall.Spec, ac agentcfg.Config) firewall.Spec {
	spec.RefuseIPv6 = !ac.IPv6
	if ac.NoP2PBypass {
		spec.P2PBypass = false
	}
	if v := ac.AdmitRate; v != nil && *v >= 0 {
		spec.AdmitRate, spec.AdmitBurst = *v, 4**v
	}
	if v := ac.PaceRate; v != nil && *v >= 0 {
		spec.PaceRate, spec.PaceBurst = *v, 4**v
	}
	if v := ac.AdmitTotalRate; v != nil && *v >= 0 {
		spec.AdmitTotalRate, spec.AdmitTotalBurst = *v, 4**v
	}
	if v := ac.DNSRate; v != nil && *v >= 0 {
		spec.DNSRate, spec.DNSBurst = *v, 20**v
	}
	return spec
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

// updateHTTPClient is the self-update's HTTP client; tests trust their own
// server with it.
var updateHTTPClient = func() *http.Client { return &http.Client{Timeout: 120 * time.Second} }

// getHTTPS is the self-update's GET: an https URL only, https all the way (a
// redirect to plain http is refused, not followed), and anything but a 200
// refused.
func getHTTPS(ctx context.Context, rawURL string) (*http.Response, error) {
	if err := requireHTTPS(rawURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := subscription.HTTPSOnlyRedirects(updateHTTPClient()).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return resp, nil
}

// fetchHTTPS reads an https URL whole with getHTTPS; an answer longer than max
// is refused, not cut.
func fetchHTTPS(ctx context.Context, rawURL string, max int64) ([]byte, error) {
	resp, err := getHTTPS(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("longer than %d bytes", max)
	}
	return b, nil
}

func downloadFile(ctx context.Context, rawURL, dest string) (string, error) {
	resp, err := getHTTPS(ctx, rawURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
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

// controllerInstallCommand is the self-update's opkg. At the controller's
// -800 it would make the kernel take hostapd or dnsmasq before it: neutral,
// like the panel's other commands. And the package's postinst would restart
// the running vctl from inside it — this opkg is vctl's own child: the
// restart's stop ends vctl, whose context then kills the opkg before it writes
// its status, and the job's result is never journaled. The postinst's restart
// is held back; the job restarts vctl itself once opkg is done.
func controllerInstallCommand(ctx context.Context, dest string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "sh", "-c",
		fmt.Sprintf(`{ echo %d > /proc/self/oom_score_adj; } 2>/dev/null; exec opkg install "$0"`, memguard.JobAdj), dest)
	cmd.Env = append(os.Environ(), "VECTRA_SKIP_POSTINST_RESTART=1")
	return cmd
}

// runControllerInstall runs the self-update's opkg (controllerInstallCommand)
// and returns what it said; tests stand it in.
var runControllerInstall = func(ctx context.Context, dest string) ([]byte, error) {
	return controllerInstallCommand(ctx, dest).CombinedOutput()
}

// scheduleControllerRestart restarts the controller service shortly after we
// exit, detached so the dying process does not take it down. Tests stand it
// in.
var scheduleControllerRestart = func() {
	if err := controllerRestartCommand().Start(); err != nil {
		// procd's respawn (5 s) still brings the new binary up.
		logging.L().Error("could not schedule the restart after the self-update", "err", err.Error())
	}
}

// controllerRestartCommand is that restart: a constant command (no
// interpolation), sh -c only to sequence the delay and the restart, in a
// session of its own — nothing that ends vctl's process group or session
// ends it before its start (a restart run as the controller's own child and
// killed with it leaves the service stopped).
func controllerRestartCommand() *exec.Cmd {
	cmd := exec.Command("sh", "-c", "sleep 2; /etc/init.d/vectra-controller-pro restart")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}
