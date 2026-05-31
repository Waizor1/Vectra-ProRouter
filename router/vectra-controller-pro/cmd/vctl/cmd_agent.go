package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/apply"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/coreengine"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/firewall"
	"vectra-controller-pro/internal/inventory"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/rescue"
	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/supervisor"
)

// runtimeVersion is injected at build time via
// -ldflags "-X main.runtimeVersion=<version>-r<release>".
var runtimeVersion = "dev"

// errControllerRestartRequested signals the loop to exit so the init system
// brings up the freshly-installed controller binary (self-update).
var errControllerRestartRequested = errors.New("controller restart requested after self-update")

func init() {
	register(command{name: "agent", summary: "Run the autonomous control loop (register/check-in/apply)", run: cmdAgent})
}

func cmdAgent(args []string) error {
	fs := newFlagSet("agent")
	configPath := fs.String("config", "/etc/vectra-controller-pro/agent.json", "daemon config (JSON)")
	once := fs.Bool("once", false, "run a single loop iteration and exit")
	logLevel := fs.String("log", "info", "log level: debug|info|warning|error")
	if err := fs.Parse(args); err != nil {
		return err
	}
	setupLogging(*logLevel)

	cfg, err := agentcfg.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load agent config: %w", err)
	}

	d, err := newDaemon(cfg)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	return d.run(ctx, *once)
}

// daemon is the long-running autonomous controller.
type daemon struct {
	cfg       agentcfg.Config
	client    *controlplane.Client
	engine    coreengine.Engine
	sup       *supervisor.Process
	applier   *apply.Applier
	collector *inventory.Collector

	st           state.PersistedState
	rescuePolicy rescue.Policy
	confirmer    *firewall.CommitConfirmer

	supCtx                 context.Context
	supCancel              context.CancelFunc
	supStarted             bool
	pendingFirewallConfirm bool
}

func newDaemon(cfg agentcfg.Config) (*daemon, error) {
	st, err := state.Load(cfg.StatePath)
	if err != nil {
		return nil, fmt.Errorf("load state: %w", err)
	}
	if err := state.EnsureIdentity(&st); err != nil {
		return nil, fmt.Errorf("ensure identity: %w", err)
	}
	// Canary identity reuse: adopt the legacy agent's router id/token so the
	// panel sees the same router flip to xray-direct (not a duplicate).
	if imported, err := state.ImportLegacyIdentity(&st, cfg.LegacyStatePath); err != nil {
		logging.L().Warn("legacy identity import", "err", err.Error())
	} else if imported {
		logging.L().Info("adopted legacy router identity for xray-direct canary", "routerId", st.RouterID)
	}
	if err := state.Save(cfg.StatePath, st); err != nil {
		return nil, fmt.Errorf("save state: %w", err)
	}

	if cfg.RouterID == "" {
		cfg.RouterID = st.RouterID
	}
	if cfg.AgentToken == "" {
		cfg.AgentToken = st.AgentToken
	}

	client := controlplane.NewClient(controlplane.Options{
		BaseURL:    cfg.ControlURL,
		RouterID:   cfg.RouterID,
		AgentToken: cfg.AgentToken,
		Timeout:    cfg.RequestTimeout(),
	})

	engine := xray.New()
	sup := supervisor.NewProcess(config.Process{
		XrayBinary:  cfg.XrayBinary,
		ConfigFile:  cfg.XrayRenderPath,
		WorkDir:     "/var/run/vectra-controller-pro",
		LogDir:      "/var/log/vectra-controller-pro",
		OOMScoreAdj: -300,
		RestartBackoff: config.Backoff{
			InitialMs: 1000,
			Factor:    2.0,
			MaxMs:     60000,
			Reset:     "60s",
		},
		ReloadGrace: "5s",
	})

	d := &daemon{
		cfg:          cfg,
		client:       client,
		engine:       engine,
		sup:          sup,
		applier:      apply.New(engine, cfg.XrayConfigPath, sup.WriteXrayConfig),
		rescuePolicy: rescue.DefaultPolicy(),
		confirmer:    firewall.NewCommitConfirmer("/var/run/vectra-controller-pro/fw-confirm", 90*time.Second),
		st:           st,
	}
	d.collector = inventory.NewCollector(inventory.Options{
		EngineMode:               controlplane.EngineModeXrayDirect,
		ControllerVersion:        Version,
		ControllerRuntimeVersion: runtimeVersion,
		PanelDomain:              cfg.PanelURL,
		XrayBinary:               cfg.XrayBinary,
	})
	return d, nil
}

func (d *daemon) run(ctx context.Context, once bool) error {
	// If a rendered config already exists, bring Xray up immediately so a
	// controller restart does not drop the data plane.
	if apply.FileExists(d.cfg.XrayRenderPath) {
		d.ensureSupervisor(ctx)
	}

	if once {
		return d.runOnce(ctx)
	}

	ticker := time.NewTicker(d.cfg.PollInterval())
	defer ticker.Stop()
	for {
		if err := d.runOnce(ctx); err != nil {
			if errors.Is(err, errControllerRestartRequested) {
				return nil
			}
			logging.L().Error("loop iteration failed", "err", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// ensureSupervisor starts the Xray supervisor goroutine once.
func (d *daemon) ensureSupervisor(ctx context.Context) {
	if d.supStarted {
		return
	}
	d.supCtx, d.supCancel = context.WithCancel(ctx)
	go func() {
		if err := d.sup.Run(d.supCtx); err != nil {
			logging.L().Error("supervisor exited", "err", err.Error())
		}
	}()
	d.supStarted = true
	logging.L().Info("xray supervisor started")
}

func (d *daemon) runOnce(ctx context.Context) error {
	// Journal recovery first: flush any result a crash left pending.
	d.recoverJournal(ctx)

	nodeCount, subCount := d.currentCounts()
	inv := d.collector.Collect(ctx, d.sup.Status(), nodeCount, subCount)
	inv.AppliedRevisionID = d.st.AppliedRevisionID
	inv.ConfigDigest = d.st.ConfigDigest

	health := d.evaluateHealth(ctx, &inv)

	// Register if we have no identity yet; next loop will check in.
	if d.st.RouterID == "" || d.st.AgentToken == "" {
		return d.register(ctx, inv)
	}

	resp, err := d.client.CheckIn(ctx, controlplane.CheckInRequest{
		ProtocolVersion: controlplane.ProtocolVersion,
		RouterID:        d.st.RouterID,
		Inventory:       inv,
		Health:          health,
	})
	if err != nil {
		return fmt.Errorf("check-in: %w", err)
	}

	// A successful check-in proves the panel link survived the last firewall
	// change — cancel the pending auto-revert.
	if d.pendingFirewallConfirm {
		if err := d.confirmer.Confirm(); err != nil {
			logging.L().Warn("firewall commit-confirm failed", "err", err.Error())
		} else {
			d.pendingFirewallConfirm = false
			logging.L().Info("firewall change confirmed (panel reachable post-apply)")
		}
	}

	if len(resp.DesiredRevision) > 0 {
		if rev, err := decodeDesiredRevision(resp.DesiredRevision); err == nil && rev != nil {
			d.st.LastDesiredRevision = rev
		}
	}

	for _, job := range resp.Jobs {
		if err := d.executeJob(ctx, job, resp); err != nil {
			if errors.Is(err, errControllerRestartRequested) {
				_ = d.persist()
				return err
			}
			logging.L().Error("job failed", "jobId", job.ID, "type", job.Type, "err", err.Error())
		}
	}
	return d.persist()
}

func (d *daemon) register(ctx context.Context, inv controlplane.RouterInventory) error {
	resp, err := d.client.Register(ctx, controlplane.RegisterRequest{
		ProtocolVersion: controlplane.ProtocolVersion,
		Inventory:       inv,
	})
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}
	if resp.RouterID != "" {
		d.st.RouterID = resp.RouterID
	}
	if resp.IssuedToken != "" {
		d.st.AgentToken = resp.IssuedToken
	}
	d.client.SetCredentials(d.st.RouterID, d.st.AgentToken)
	if resp.PendingApproval {
		logging.L().Info("registered; awaiting operator approval", "routerId", d.st.RouterID)
	}
	return d.persist()
}

// evaluateHealth runs connectivity probes and updates rescue state, returning
// the RouterHealth summary for check-in.
func (d *daemon) evaluateHealth(ctx context.Context, inv *controlplane.RouterInventory) controlplane.RouterHealth {
	hc := &http.Client{Timeout: 4 * time.Second}
	serverReachable := rescue.ProbeAny(ctx, hc, serverHealthURLs(d.cfg.ControlURL))
	publicReachable := rescue.ProbeAny(ctx, hc, d.rescuePolicy.HealthURLs)

	cur := d.rescueState()
	decision := rescue.Evaluate(rescue.Input{
		CurrentState:    cur,
		PublicReachable: publicReachable,
		ProxyConclusive: cur.Mode == rescue.ModeProxy,
		DirectReachable: publicReachable, // best-effort: we can reach the internet path
		Now:             time.Now(),
	}, d.rescuePolicy)
	d.storeRescueState(decision.NextState, decision.Reason)

	inv.PanelReachability = &controlplane.RouterReachabilityProbe{
		ID: "panel", Label: "control plane", Reachable: serverReachable,
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
	}

	return controlplane.RouterHealth{
		CurrentMode:                string(decision.NextState.Mode),
		ProxyConnectivitySuccesses: decision.NextState.ProxySuccessCount,
		DirectConnectivitySuccesses: decision.NextState.DirectSuccessCount,
		PublicConnectivityFailures: decision.NextState.ProxyFailureCount,
		ServerReachable:            serverReachable,
	}
}

func (d *daemon) rescueState() rescue.State {
	st := rescue.State{
		Mode:               rescue.Mode(d.st.Rescue.Mode),
		ProxyFailureCount:  d.st.Rescue.ProxyFailureCount,
		DirectSuccessCount: d.st.Rescue.DirectSuccessCount,
	}
	if st.Mode == "" {
		st.Mode = rescue.ModeProxy
	}
	if d.st.Rescue.LastTransitionAt != "" {
		if t, err := time.Parse(time.RFC3339, d.st.Rescue.LastTransitionAt); err == nil {
			st.LastTransitionAt = t
		}
	}
	return st
}

func (d *daemon) storeRescueState(s rescue.State, reason string) {
	d.st.Rescue.Mode = string(s.Mode)
	d.st.Rescue.ProxyFailureCount = s.ProxyFailureCount
	d.st.Rescue.DirectSuccessCount = s.DirectSuccessCount
	if !s.LastTransitionAt.IsZero() {
		d.st.Rescue.LastTransitionAt = s.LastTransitionAt.UTC().Format(time.RFC3339)
	}
	if reason != "" {
		d.st.Rescue.LastReason = reason
		d.st.Rescue.HappenedAt = time.Now().UTC().Format(time.RFC3339)
		logging.L().Warn("rescue transition", "mode", s.Mode, "reason", reason)
	}
}

// recoverJournal flushes a pending result, or reports a job that a crash left
// mid-flight as failed, so the panel is never left waiting.
func (d *daemon) recoverJournal(ctx context.Context) {
	if d.st.PendingJobResult != nil {
		if _, err := d.client.SubmitJobResult(ctx, *d.st.PendingJobResult); err == nil {
			d.st.PendingJobResult = nil
			d.st.CurrentJob = state.CurrentJob{}
			_ = d.persist()
		}
		return
	}
	if d.st.CurrentJob.JobID != "" {
		_, _ = d.client.SubmitJobResult(ctx, controlplane.JobResultRequest{
			ProtocolVersion: controlplane.ProtocolVersion,
			RouterID:        d.st.RouterID,
			JobID:           d.st.CurrentJob.JobID,
			Status:          "failure",
			Result:          map[string]interface{}{"error": "controller restarted before job completed"},
		})
		d.st.CurrentJob = state.CurrentJob{}
		_ = d.persist()
	}
}

func (d *daemon) persist() error {
	return state.Save(d.cfg.StatePath, d.st)
}

// currentCounts reports node/subscription counts from the persisted desired
// config (best-effort; 0 if none applied yet).
func (d *daemon) currentCounts() (nodes, subs int) {
	raw, err := os.ReadFile(d.cfg.XrayConfigPath)
	if err != nil {
		return 0, 0
	}
	var c config.Config
	if json.Unmarshal(raw, &c) != nil {
		return 0, 0
	}
	return len(c.Nodes), len(c.Subscriptions)
}

func decodeDesiredRevision(raw json.RawMessage) (*controlplane.DesiredRevisionSummary, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var rev controlplane.DesiredRevisionSummary
	if err := json.Unmarshal(raw, &rev); err != nil {
		return nil, err
	}
	return &rev, nil
}

// serverHealthURLs derives control-plane health probe URLs from the base URL.
func serverHealthURLs(controlURL string) []string {
	if controlURL == "" {
		return nil
	}
	return []string{controlURL + "/healthz", controlURL + "/api/health"}
}
