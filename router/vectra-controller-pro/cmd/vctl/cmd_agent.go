package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"vectra-controller-pro/internal/vault"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/apply"
	"vectra-controller-pro/internal/claim"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/connectactions"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/exitcheck"
	"vectra-controller-pro/internal/firewall"
	"vectra-controller-pro/internal/incident"
	"vectra-controller-pro/internal/inventory"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/memguard"
	"vectra-controller-pro/internal/power"
	"vectra-controller-pro/internal/rescue"
	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/subscription"
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
	logging.SetDefault(logging.New(*logLevel, os.Stdout, "text"))
	// A crash of the daemon goes to the reporter too (ADR-0007).
	captureCrashes()

	cfg, err := agentcfg.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load agent config: %w", err)
	}

	// Same conflict `vctl supervise` refuses on, reported rather than refused.
	// The difference is who started us: procd does, from an init script that has
	// already done the hand-over, so the only way to get here with the legacy
	// agent still enabled is that the hand-over did not take (its `disable` is
	// best-effort, `|| true`). Refusing there would leave the router with the
	// PassWall stack AND a respawn loop, which is a worse place to be than two
	// stacks with a line in the log naming the cause. A trial (`vectra on
	// --trial`) stops the agent and leaves it enabled on purpose — a reboot
	// must give the router back — so a stopped agent is no conflict then.
	if l := detectLegacyAgent(legacyInitScript, runInitVerb); l.owns() && !l.Running && power.LoadTrial(power.RouterEnv()) != nil {
		logging.L().Info("a trial: the legacy vectra-controller agent is stopped but left enabled, so a reboot gives the router back",
			"legacyAgent", l.describe())
	} else if l.owns() {
		logging.L().Error(
			"the legacy vectra-controller agent still owns this router; its watchdog will restart "+
				"the PassWall stack under xray. Hand the router over with "+
				"`/etc/init.d/vectra-controller-pro start`, which stops and disables it.",
			"legacyAgent", l.describe(), "initScript", legacyInitScript,
		)
	}

	d, err := newDaemon(cfg)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	return runAgent(ctx, d, *once)
}

// runAgent runs the control loop and, if a SIGNAL ended it, unloads the data
// plane on the way out.
//
// An `inet vctl` table and fwmark policy route left loaded with no xray behind
// them send the router's own egress to `local ... dev lo` and, with the kill
// switch on, black-hole the LAN. procd's stop path does the same teardown in
// shell (openwrt/files/etc/init.d/vectra-controller-pro); this covers a SIGTERM
// from anywhere else — a manual `kill`, an ad-hoc foreground run, a container.
//
// The gate is ctx.Err(), so it fires only for a signal: `-once` finishing
// normally and the self-update restart (errControllerRestartRequested, where
// the init script is about to bring the new binary straight back up) must leave
// the data plane alone.
func runAgent(ctx context.Context, d *daemon, once bool) error {
	err := d.run(ctx, once)
	if ctx.Err() != nil {
		d.shutdownDataPlane()
	}
	return err
}

// daemon is the long-running autonomous controller.
type daemon struct {
	// saver writes state.json only when it changed (persist).
	saver state.Saver
	// lastGoodRenderSum is the sha256 of the render kept on flash
	// (last_good_render.go).
	lastGoodRenderSum [32]byte
	// aiRefused are the «Нейросети» defaults xray refused, each on the
	// document it joined (aiRefusedKey): not tried again until either changes.
	aiRefused map[string]bool
	// svcSkipped are the owner's service locations the render skips (gone
	// from the cache, or no longer carrying the service), service → digest:
	// logged once each (noteSkippedServiceChoices).
	svcSkipped struct {
		sync.Mutex
		m map[string]string
	}
	cfg       agentcfg.Config
	client    *controlplane.Client
	sup       *supervisor.Process
	applier   *apply.Applier
	collector *inventory.Collector

	// incidents goes to the reporter's inbox (incidents.go, ADR-0007);
	// xrayExits are the last ten minutes' unasked xray exits.
	incidents   *incident.Recorder
	xrayExitsMu sync.Mutex
	xrayExits   []time.Time

	// desired is the OPERATOR config (config.Config, four blocks). It is what
	// the panel delivers. The proxy config itself comes from the provider.
	desired *config.Config
	// device carries the identity the provider keys on (HWID/MAC/model/release).
	device subscription.DeviceFacts
	// subClient overrides the HTTP client used for provider fetches. nil = the
	// package default. Tests inject an httptest client here.
	subClient *http.Client
	// nodeCount is the provider document's outbound count, refreshed on apply
	// so check-in inventory does not re-parse a ~485 KB file every loop.
	nodeCount int

	st           state.PersistedState
	rescuePolicy rescue.Policy
	confirmer    *firewall.CommitConfirmer
	// runFirewallCmd executes one firewall revert command. Injectable so the
	// shutdown path is testable without root or nftables.
	runFirewallCmd func(ctx context.Context, name string, args ...string) error
	// applyRuleset stands in for the commit-confirmer's Apply in tests
	// (programFirewall); nil is the real one.
	applyRuleset func(script string, spec firewall.Spec) error
	// reloadXrayFn and xrayStatusFn stand in for the supervisor's Reload and
	// Status in tests (reloadXray, waitXrayAfter, flushAfterXrayRestart).
	reloadXrayFn func(ctx context.Context) error
	xrayStatusFn func() supervisor.Status
	// tableLoaded tells whether the kernel has vctl's nft table (the seam
	// tests stand in for nft).
	tableLoaded func(ctx context.Context, name string) bool
	// fwProgrammed is what the loaded data plane redirects of DNS
	// (dnsRedirectKey; "" = nothing), nil until this process programmed it.
	fwProgrammed *string
	// procDir stands in for /proc in tests (resolverUIDs), rootDir for / in
	// hasIPv4Upstream.
	procDir string
	rootDir string
	// dnsAnswers stands in for dnsInboundAnswers in tests; dnsMisses counts
	// the loops in a row xray has not answered on its DNS inbound.
	dnsAnswers func(ctx context.Context, port int) bool
	dnsMisses  int
	// dnsFastMisses counts dnsWatchDue's checks in a row without an answer;
	// dnsFailedOpen: that watch took the redirect out, and puts it back.
	dnsFastMisses int
	dnsFailedOpen bool
	// dnsPutBackAfter holds the next put-back while the last one did not
	// get the redirect in (a failing apply must not reload every 3 s).
	dnsPutBackAfter time.Time
	// signalProcess stands in for syscall.Kill in tests (killOrphanXray).
	signalProcess func(pid int, sig syscall.Signal) error
	// hupResolver stands in for SIGHUP to dnsmasq in tests.
	hupResolver func(pid int) error
	// flushedFor/flushedAt/flushedGen: the last resolver flush's reason, time
	// and dnsPathGen — the same one again within resolverFlushDedupe, with
	// the resolver's path unchanged since, is not repeated.
	flushedFor string
	flushedAt  time.Time
	flushedGen int
	// dnsPathGen counts the changes of the resolver's path: the DNS redirect
	// going in or out.
	dnsPathGen int
	// flushedStart is the xray start the resolver's cache was last emptied
	// for (flushAfterXrayRestart).
	flushedStart time.Time
	// rescueCheckedAt is when the rescue last probed (rescueStep).
	rescueCheckedAt time.Time
	// reconfiguredAt is when an apply last put a new config in (rescueAfresh):
	// the rescue judges the tunnel only xraySettle after it.
	reconfiguredAt time.Time
	// wanIP is the router's own address as Cloudflare saw it around the
	// tunnel (refreshWANIP, at wanIPAt); traceAroundSaid: logged once.
	wanIP           string
	wanIPAt         time.Time
	traceAroundSaid bool
	// tunnelIPs are the exit addresses probes came back from lately (up to
	// 16): a balancer rotating exits must not re-learn the WAN every step.
	tunnelIPs map[string]bool
	// hijackMisses counts the loops in a row nothing served port 53 while
	// the loaded table hijacks the LAN's DNS to it; ownsAddr stands in for
	// routerOwns in tests.
	hijackMisses int
	ownsAddr     func(net.IP) bool
	// lanDevs are the LAN's devices the last ruleset was loaded with
	// (knownLANDevices; lanDevsAsked: netifd asked already); devNets stands
	// in for a device's subnets in tests. A resolver behind them is never
	// redirected (wanResolvers).
	lanDevs      []string
	lanDevsAsked bool
	devNets      func(dev string) []*net.IPNet
	// wanSeenV4/V6 are the WAN resolvers last seen, at wanSeenAt
	// (steeredWANResolvers: they outlive a flap's empty file).
	wanSeenV4, wanSeenV6 []string
	wanSeenAt            time.Time
	// PassWall-compatible routing (passwall_source.go): the sniffing of
	// PassWall's tproxy inbound, and what the last sync was made from.
	passwallSniffing *config.Sniffing
	// autoRoute: vctl routes by PassWall2's configuration of itself, on the
	// base operator config, until the panel's arrives (auto_route.go).
	autoRoute        bool
	passwallCfgStamp string
	passwallGeoStamp string
	// passwallRetryAt: a sync refused for want of memory is not tried
	// before then (passwallSyncBackoff).
	passwallRetryAt time.Time
	// Native routing (native_source.go): a failed subscription refresh is not
	// tried before nativeRetryAt; xrayVersion is the binary's own answer
	// when the inventory has none yet.
	nativeRetryAt time.Time
	// nativeGeoRetryAt (unix ns) is written by the geo update's goroutine.
	nativeGeoRetryAt atomic.Int64
	xrayVersion      string
	// nativeBooted: the first loop has run (boot_update's once per start);
	// nativeGeoBusy: a geo update is running in its own goroutine.
	nativeBooted  bool
	nativeGeoBusy atomic.Bool
	// memFloorKB is the MemAvailable a heavy transient needs to start
	// (memguard.HeavyFloorKB; 0 = not known, not checked).
	memFloorKB uint64
	// Direct destinations routed by the kernel (direct_bypass.go): the key
	// of what the direct sets hold ("" = nothing loaded since the table was
	// last programmed), and the last failed attempt, retried after a while.
	directLoaded  string
	directFailKey string
	directFailAt  time.Time
	// directStamp is the render's and geoip.dat's stamp at the last load;
	// directPartial whether memory allowed only part of the list then.
	directStamp    string
	directPartial  bool
	directLoadedAt time.Time
	// directCount is how many ranges the sets hold; directGeoFiles the geo
	// files the last load read (their stamps are part of directStamp).
	directCount    int
	directGeoFiles []string
	// directLoad stands in for loading the direct sets in tests, readMem for
	// reading the router's memory (nil = nft, memguard.Read).
	directLoad func(ctx context.Context, script string) error
	readMem    func() (memguard.Info, error)

	supCtx     context.Context
	supCancel  context.CancelFunc
	supStarted bool
	// supDone is closed when the supervisor goroutine returns.
	supDone chan struct{}

	// warnedForeignRevision is the id of the last other-engine revision already
	// reported, so a panel that has not been upgraded yet costs one log line
	// rather than one per poll. In-memory on purpose: a restart should say it
	// again.
	warnedForeignRevision string

	// lastControlPlaneOK is whether the PREVIOUS register/check-in reached the
	// panel. nil until one has been attempted. It is the ground truth behind
	// serverReachable — see controlPlaneReachable.
	lastControlPlaneOK *bool
	// cpFailures counts the register/check-in exchanges that failed in a
	// row; cpRetryAt is when the next may go (checkInBackoff).
	cpFailures int
	cpRetryAt  time.Time

	// fwConfirmBy is when the tries to confirm the last firewall
	// programming end (shortly before its deadman wakes), zero with none
	// pending; fwConfirmNext is the next try (fw_confirm.go). clock stands
	// in for time.Now there in tests.
	fwConfirmBy, fwConfirmNext time.Time
	clock                      func() time.Time

	// The router UI (localui.go). All written on the loop goroutine only;
	// the socket goroutine reads the published snapshot.
	uiReqs       chan uiRequest
	runtime      atomic.Pointer[localctl.Runtime]
	startedAt    time.Time
	lastCheckIn  time.Time
	lastApplyErr string
	// nextSubscriptionRefresh is when the daemon's own refresh is due
	// (maybeRefreshSubscription); zero until the first poll sets it.
	nextSubscriptionRefresh time.Time
	busy                    string
	probe                   *localctl.Probe
	// pinBudget bounds how long the start hook waits for xray's API to come
	// up before giving up on restoring pins and on the leak baseline. 0 = 45 s.
	pinBudget time.Duration
	// leakBaseline is the leak counter taken after the latest xray start
	// (written by the start hook, read by the socket goroutine).
	leakBaseline atomic.Pointer[localctl.LeakBaseline]
	// route is the main balancer as the failover watchdog last saw it.
	route atomic.Pointer[localctl.Route]
	// tunnel is the observatory's word on the main traffic's nodes
	// (publishTunnel): the rescue goes back to the proxy only when one lives.
	tunnel atomic.Pointer[tunnelLook]
	// exits is what the exit check found (cmd/vctl/exitcheck.go).
	exits exitcheck.State
	// connectHeld is the last verdict judged for Connect
	// (holdConnectVerdict; the daemon loop only). A release forgets it.
	connectHeld connectJudged
	// readCounters reads the vctl table's nft counters; nil = nft. Tests
	// replace it.
	readCounters func(ctx context.Context) (map[string]int64, bool)
	// claim is the router's side of being claimed by a Vectra account
	// (ADR-0006) until it is linked.
	claim *claimer

	// PassWall2's retirement (retire_passwall.go): the loop's next look, the
	// next try after a removal that failed, and the last refusal said. Seams
	// for tests: xray running, `ip` show, the router's resources (nil: the
	// supervisor, the ip command, the collector).
	retireNextAt      time.Time
	retireFailedUntil time.Time
	retireSaid        string
	xrayRunning       func() bool
	ipOutput          func(ctx context.Context, args ...string) ([]byte, error)
	retireResources   func() controlplane.RouterResources
}

// xrayGOGC is xray's GC target: collect when the heap has grown 30% since
// the last collection (Go's default is 100%).
const xrayGOGC = 30

func newDaemon(cfg agentcfg.Config) (*daemon, error) {
	if err := migrateSecrets(cfg); err != nil {
		// Not started: no secret is read and none is written in plaintext.
		// The dead-man hands the router back if it owes it, and the reporter
		// tells the operator either way.
		reportSecretStorage("secret_storage_migration_failed", "error", "vctl did not start: its secrets could not be sealed", err)
		return nil, fmt.Errorf("secret storage migration: %w", err)
	}
	st, err := state.Load(cfg.StatePath)
	if err != nil {
		return nil, fmt.Errorf("load state: %w", err)
	}
	// Canary identity reuse: adopt the legacy agent's router id/token/device
	// identity so the panel sees the same router flip to xray-direct (not a
	// duplicate). This MUST run before EnsureIdentity: EnsureIdentity mints a
	// random device identifier and keypair into any empty field, and every
	// adopt branch in ImportLegacyIdentity is guarded by `== ""`. With the
	// order reversed the device-identity adoption was silently dead on every
	// fresh install — the router kept the legacy routerId/token (adopted
	// unconditionally) but reported a freshly minted deviceIdentifier.
	if imported, err := state.ImportLegacyIdentity(&st, cfg.LegacyStatePath); err != nil {
		// The panel already knows this router by the old agent's identity;
		// minting another would split it in two records. With the old agent
		// installed, not starting hands the router back to it. Without it
		// there is nothing to hand back to: a new identity the operator
		// adopts beats a router that never checks in again.
		if legacyAgentInstalled() {
			reportSecretStorage("legacy_identity_unreadable", "error", "vctl did not start: the router's identity cannot be read", err)
			return nil, fmt.Errorf("legacy identity unreadable; not minting a new one: %w", err)
		}
		reportSecretStorage("legacy_identity_unreadable", "error", "the router's old identity cannot be read; vctl enrols anew", err)
		logging.L().Error("the old agent's identity cannot be read and no old agent is installed; enrolling anew", "err", err.Error())
	} else if imported {
		logging.L().Info("adopted legacy router identity for xray-direct canary", "routerId", st.RouterID)
	}
	if err := state.EnsureIdentity(&st); err != nil {
		return nil, fmt.Errorf("ensure identity: %w", err)
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
		// Keep vctl->panel traffic out of the provider's routing: the nft
		// output chain returns on this mark before any TPROXY rule.
		SocketMark: firewall.DefaultControlMark,
	})

	// No operator config yet on a router PassWall2 routes: vctl routes by its
	// configuration until the panel's arrives (auto_route.go).
	auto := autoRouteSource(cfg)
	if auto {
		cfg.RouteSource = routeSourcePassWall
	}
	assetDir := config.ResolveGeoAssetDir(cfg.GeoAssetDir)
	switch cfg.RouteSource {
	case routeSourcePassWall:
		assetDir = passwallAssetDir()
	case routeSourceNative:
		assetDir = nativeAssetDir()
	}
	sup := supervisor.NewProcessWithAssetDir(config.Process{
		XrayBinary: cfg.XrayBinary,
		ConfigFile: cfg.XrayRenderPath,
		WorkDir:    "/var/run/vectra-controller-pro",
		LogDir:     "/var/log/vectra-controller-pro",
		RestartBackoff: config.Backoff{
			InitialMs: 1000,
			Factor:    2.0,
			MaxMs:     60000,
			Reset:     "60s",
		},
		ReloadGrace: "5s",
	}, assetDir)
	// When memory runs out the kernel is to take xray — the one process that
	// grows with traffic, back in seconds — and never the controller that
	// brings it back; and xray's GC works to the RAM this router has
	// (internal/memguard).
	sup.SetOOMScoreAdj(memguard.ControllerAdj, memguard.XrayAdj)
	var memFloorKB uint64
	if mi, err := memguard.Read(); err == nil {
		sup.SetChildEnv([]string{
			fmt.Sprintf("GOMEMLIMIT=%dMiB", memguard.XrayGoMemLimitMiB(mi.TotalKB)),
			fmt.Sprintf("GOGC=%d", xrayGOGC),
		})
		memFloorKB = memguard.HeavyFloorKB(mi.TotalKB)
	}

	d := &daemon{
		cfg:            cfg,
		client:         client,
		sup:            sup,
		rescuePolicy:   rescue.DefaultPolicy(),
		confirmer:      firewall.NewCommitConfirmer("/var/run/vectra-controller-pro/fw-confirm", 90*time.Second),
		runFirewallCmd: runFirewallCommand,
		tableLoaded:    nftTableLoaded,
		st:             st,
		device:         subscription.ReadDeviceFacts(),
		uiReqs:         make(chan uiRequest, 1),
		startedAt:      time.Now().UTC(),
		memFloorKB:     memFloorKB,
	}
	d.exits.Restore(unfitSince(st.UnfitExits))
	d.exits.RestoreEgress(egressFrom(st.ExitEgress))
	// Every start — the first, a reload, a crash restart — must get the
	// router's pins back (xray keeps balancer overrides in memory only) and a
	// fresh leak baseline (see takeLeakBaseline).
	sup.SetConfigSource(func() ([]byte, error) { return vault.ReadFile(cfg.XrayRenderPath) })
	sup.SetOnStart(d.onXrayStart)
	d.incidents = incident.NewRecorder(incident.Dir, 10*time.Minute)
	sup.SetOnExit(func(code int, err error, ran time.Duration) { d.noteXrayExit(code, err, ran, time.Now()) })
	if err := d.device.Validate(); err != nil {
		// Not fatal: the daemon still registers/checks in. But the subscription
		// WILL 403 without a valid x-hwid, so make it loud.
		logging.L().Warn("device identity incomplete; subscription fetches will be refused by the provider", "err", err.Error())
	}
	// Best-effort: adopt any previously-applied operator config so the tproxy
	// inbound (and therefore the applier) is usable before the first check-in.
	if c, err := config.LoadSecret(cfg.XrayConfigPath); err == nil {
		d.desired = c
	} else if auto {
		d.enterAutoRoute()
	}
	d.claim = newClaimer(st, d.device.Model, cfg.ClaimRotate())
	d.claim.setLinked(d.linked())
	d.rebuildApplier()
	// The render on disk runs with the directory it passed `xray -test` with
	// (state.json keeps which): a config the gate refused since — saved all
	// the same — does not move it at a restart either. Its re-render under
	// the config's directory (reconcileRender: the directory is part of
	// renderKey) moves it, if the gate takes it. No render yet, or one from
	// before 0.6.0-r18: the operator config's.
	assetDir = d.geoAssetDir()
	if d.st.RenderAssetDir != "" && fileExists(cfg.XrayRenderPath) {
		assetDir = d.st.RenderAssetDir
	}
	sup.SetAssetDir(assetDir)
	d.refreshNodeCount()

	d.collector = inventory.NewCollector(inventory.Options{
		EngineMode:               controlplane.EngineModeXrayDirect,
		DeviceIdentifier:         st.DeviceIdentifier,
		DevicePublicKey:          st.DevicePublicKey,
		ControllerVersion:        Version,
		ControllerRuntimeVersion: runtimeVersion,
		PanelDomain:              cfg.PanelURL,
		XrayBinary:               cfg.XrayBinary,
		AssetDir:                 assetDir,
	})
	return d, nil
}

// rebuildApplier re-points the applier at the current operator config. Called
// whenever the desired config changes so a new tproxy port/mark is spliced in.
func (d *daemon) rebuildApplier() {
	var tproxy *config.TproxyInbound
	assetDir := d.geoAssetDir()
	allowInsecure := false
	if d.desired != nil {
		tproxy = d.desired.Inbounds.Tproxy
		for _, s := range d.desired.Subscriptions {
			if s.Enabled && s.AllowInsecureTLS {
				allowInsecure = true
			}
		}
	}
	if d.passwallMode() {
		// FakeDNS needs PassWall's sniffing on the tproxy inbound.
		if tproxy != nil && d.passwallSniffing != nil {
			t := *tproxy
			t.Sniffing = *d.passwallSniffing
			tproxy = &t
		}
	}
	d.sup.SetConfigSource(func() ([]byte, error) { return vault.ReadFile(d.cfg.XrayRenderPath) })
	d.applier = &apply.Applier{
		Tproxy:        tproxy,
		ProviderPath:  d.documentPath(),
		SecretStorage: true,
		// xray reads its geo files where the xray -test gate checked the
		// render it runs: the directory moves with a render written, never
		// before — a config the gate refused leaves the running render its
		// own directory for every restart after.
		WriteXray: func(b []byte) error {
			if err := vault.WriteFile(d.cfg.XrayRenderPath, b); err != nil {
				return err
			}
			// Kept on flash too: a reboot that cannot make it again runs
			// it (resumeLastGoodRender).
			d.keepLastGoodRender(b)
			d.sup.SetAssetDir(assetDir)
			d.st.RenderAssetDir = assetDir
			if d.collector != nil {
				d.collector.SetAssetDir(assetDir)
			}
			return nil
		},
		Validate: xray.Validator{Binary: d.cfg.XrayBinary, AssetDir: assetDir,
			OOMScoreAdj: memguard.TransientAdj, MemFloorKB: d.memFloorKB},
		AllowInsecureTLS: allowInsecure,
	}
}

// runningAssetDir is where the running xray reads its geo files: the
// directory its render passed `xray -test` with. What reads the same files
// beside xray — the kernel's direct routes, the geo update — reads this one.
func (d *daemon) runningAssetDir() string {
	if d.sup != nil {
		if dir := d.sup.AssetDir(); dir != "" {
			return dir
		}
	}
	return d.geoAssetDir()
}

// geoAssetDir is where the next render's geo files are: PassWall's, whose
// rules name its categories, in PassWall mode; the operator's otherwise.
func (d *daemon) geoAssetDir() string {
	if d.passwallMode() {
		return d.routeAssetDir()
	}
	dir := d.cfg.GeoAssetDir
	if d.desired != nil && d.desired.Geo.AssetDir != "" {
		dir = d.desired.Geo.AssetDir
	}
	return config.ResolveGeoAssetDir(dir)
}

// refreshNodeCount recounts the provider document's outbounds from disk.
func (d *daemon) refreshNodeCount() {
	raw, err := vault.ReadFile(d.documentPath())
	if err != nil {
		return
	}
	d.nodeCount = countProviderOutbounds(raw)
}

// shutdownDataPlane removes the vctl nft table and the fwmark policy route on
// daemon exit. It builds its own context: the caller's is already cancelled by
// the signal, and exec.CommandContext on a cancelled context runs nothing.
func (d *daemon) shutdownDataPlane() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	logging.L().Info("shutting down: unloading the data plane")
	d.tearDownFirewall(ctx)
}

func (d *daemon) run(ctx context.Context, once bool) error {
	if !once {
		// An update's package left in RAM by an earlier vctl (one that died
		// mid-update, or a version that never removed it): vctl's own, so
		// removed whatever the tune's switch says (tune.RemoveLeftovers:
		// older than ten minutes and open in no process — no update runs
		// before this daemon starts one).
		removeLeftoversAtStart()
		// Before this vctl's xray starts: an xray a killed vctl left must
		// not hold the tproxy port (procd respawns vctl without the init
		// script, whose start_service did this alone).
		d.killOrphanXray()
		go d.serveUI(ctx)
		go d.watchMemory(ctx)
		go d.watchFailover(ctx)
		go d.watchExits(ctx)
		// The router's tune (cmd_tune.go): in the background, never in the way.
		go d.tuneAtStart(ctx)
	}
	// PassWall-compatible routing renders from PassWall2's configuration, as
	// it is now; the document on /etc (resumeRender) only when that fails.
	if d.passwallMode() && d.desired != nil {
		if err := d.syncPassWall(ctx, false); err != nil {
			logging.L().Warn("could not render from PassWall2's configuration at the start; the last render's documents are used", "err", err.Error())
		} else {
			d.passwallCfgStamp, d.passwallGeoStamp = d.passwallStamp()
		}
	}
	// After a reboot there is no render (tmpfs): rebuild the one the router
	// ran before, from the documents on /etc (see resumeRender).
	d.resumeRender(ctx)
	// If a rendered config already exists, bring Xray up immediately so a
	// controller restart does not drop the data plane.
	if apply.FileExists(d.cfg.XrayRenderPath) {
		// First, while xray is still down: a render made under other
		// options (an upgrade that added the API, a probe interval changed
		// while the daemon was down) is redone now, at no extra restart.
		d.reconcileRender(ctx)
		d.ensureSupervisor(ctx)
		// ...and put the kernel side back, because stop unloads it. Before the
		// teardown existed the table simply survived a restart; now every stop
		// (including the reload procd triggers on any /etc/config change, and
		// the restart after a self-update) removes it, and only an
		// apply/reconnect job would ever have rebuilt it. Without this a
		// reload would leave xray running with nothing steering traffic into
		// it until the panel happened to send a config-changing job.
		d.restoreDataPlane(ctx)
	}

	d.publishRuntime()
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
		d.ensureDataPlane(ctx)
		d.retryFirewallConfirm(ctx, d.now())
		d.maybeRefreshNative(ctx, time.Now())
		d.maybeUpdateNativeGeo(ctx, time.Now())
		d.maybeSyncPassWall(ctx)
		d.maybeLoadDirect(ctx)
		d.maybeRefreshSubscription(ctx, time.Now())
		d.maybeRetirePassWall(ctx, time.Now())
		d.publishRuntime()
		// Between polls the loop serves the router UI's changes, so they run
		// here, serialized with the jobs, never beside them.
		if !d.waitForTick(ctx, ticker.C) {
			return nil
		}
	}
}

// ensureSupervisor starts the Xray supervisor goroutine once (again after
// stopXray).
func (d *daemon) ensureSupervisor(ctx context.Context) {
	if d.supStarted {
		return
	}
	supCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	d.supCtx, d.supCancel, d.supDone = supCtx, cancel, done
	go func() {
		defer close(done)
		if err := d.sup.Run(supCtx); err != nil {
			logging.L().Error("supervisor exited", "err", err.Error())
		}
	}()
	d.supStarted = true
	logging.L().Info("xray supervisor started")
}

// stopXray stops xray and ends its supervisor, and waits for both; the next
// apply starts them afresh. Stop first (SIGTERM, then the whole group killed
// after the grace), then the context: a supervisor between two starts has no
// process to signal and would otherwise start one when its backoff ends.
func (d *daemon) stopXray(ctx context.Context) {
	if !d.supStarted {
		return
	}
	if err := d.sup.Stop(ctx); err != nil {
		logging.L().Warn("xray did not stop within its grace; killed", "err", err.Error())
	}
	d.supCancel()
	select {
	case <-d.supDone:
	case <-time.After(10 * time.Second):
		logging.L().Warn("the xray supervisor did not end in time")
	}
	d.supStarted = false
}

func (d *daemon) runOnce(ctx context.Context) error {
	// Journal recovery first: flush any result a crash left pending.
	d.recoverJournal(ctx)

	d.publishConnectTelemetry(ctx, d.connectCapabilities())
	nodeCount, subCount := d.currentCounts()
	inv := d.collector.Collect(ctx, d.sup.Status(), nodeCount, subCount)
	inv.AppliedRevisionID = d.st.AppliedRevisionID
	inv.ConfigDigest = d.st.ConfigDigest
	inv.RemoteShell = remoteShellAllowed()
	// The panel erases field names from its 400, so name them here. Still send:
	// the panel is authoritative and a rejected report is no worse than a
	// skipped one, but now the router log says exactly which field is empty.
	if missing := inv.MissingRequiredFields(); len(missing) > 0 {
		logging.L().Error(
			"inventory is missing fields the panel requires; it will reject this report with HTTP 400",
			"fields", strings.Join(missing, ","),
		)
	}

	health, rescueDecision := d.evaluateHealth(ctx, &inv)
	if rescueDecision.ShouldTransition {
		d.applyRescueTransition(ctx, rescueDecision)
		// Kept now, not with the check-in's state: without the panel there
		// is none, and a reboot would start from the mode before.
		_ = d.persist()
	}

	// The panel failed the last exchanges: the next waits its turn
	// (checkInBackoff). Everything above — the rescue, the data plane —
	// does not.
	if d.now().Before(d.cpRetryAt) {
		return nil
	}

	// Register if we have no identity yet; next loop will check in.
	if d.st.RouterID == "" || d.st.AgentToken == "" {
		return d.register(ctx, inv)
	}

	d.enrichConnectCheckin(&inv)
	d.claim.setLinked(d.linked())
	resp, err := d.client.CheckIn(ctx, controlplane.CheckInRequest{
		ProtocolVersion: controlplane.ProtocolVersion,
		RouterID:        d.st.RouterID,
		Inventory:       inv,
		Health:          health,
		Claim:           d.claim.announcement(time.Now()),
	})
	d.noteControlPlane(err == nil)
	if err != nil {
		return fmt.Errorf("check-in: %w", err)
	}
	if resp.RouterID != "" && resp.RouterID != d.st.RouterID {
		return fmt.Errorf("check-in target mismatch")
	}
	d.lastCheckIn = time.Now().UTC()
	if d.adoptClaimInfo(ctx, resp.ClaimInfo) {
		// The rest of the answer that released the router was meant for the
		// owner who just left: its desired revision is not kept and its jobs
		// are not run (they stay the panel's to cancel or send again).
		return d.persist()
	}
	if err := d.persist(); err != nil {
		return fmt.Errorf("persist adopted claim: %w", err)
	}
	if err := d.connectMaintenanceAfterCheckin(ctx, d.connectBinding().OwnerRef); err != nil {
		if errors.Is(err, errControllerRestartRequested) {
			return err
		}
		logging.L().Warn("connect maintenance unavailable")
	}

	// A successful check-in proves the router's marked path out is healthy,
	// so (re)write the firewall commit-confirm sentinel. This disarms a
	// pending auto-revert even one armed by a PREVIOUS process before a
	// restart (the durable signal is the sentinel, not an in-memory flag). It
	// is one proof among others (fw_confirm.go): without the panel the
	// router's own confirms.
	if err := d.confirmer.Confirm(); err != nil {
		logging.L().Debug("firewall commit-confirm sentinel write failed", "err", err.Error())
	} else {
		d.firewallConfirmed()
	}

	if len(resp.DesiredRevision) > 0 {
		if rev, err := decodeDesiredRevision(resp.DesiredRevision); err == nil && rev != nil {
			d.adoptDesiredRevision(rev)
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
		Proof:           d.registerProof(time.Now()),
	})
	d.noteControlPlane(err == nil)
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
	d.adoptClaimInfo(ctx, resp.ClaimInfo)
	if resp.PendingApproval {
		logging.L().Info("registered; awaiting operator approval", "routerId", d.st.RouterID)
	}
	return d.persist()
}

// registerProof proves to the panel that this router holds its device key —
// the key behind inventory.devicePublicKey and the claim QR's pk — so that a
// record the panel created in advance (a pre-claimed router) goes only to the
// router it was made for. Sent on every register; nil when the key cannot be
// read, and then the panel decides. The signature is never logged.
func (d *daemon) registerProof(now time.Time) *controlplane.RegisterProof {
	key, err := claim.DeviceKey(d.st.DevicePrivateKey)
	if err != nil {
		logging.L().Warn("registering without a proof: the device key is unreadable", "err", err.Error())
		return nil
	}
	ts := now.Unix()
	return &controlplane.RegisterProof{Timestamp: ts, Signature: claim.RegisterProof(key, d.st.DeviceIdentifier, ts)}
}

// evaluateHealth runs connectivity probes and updates rescue state, returning
// the RouterHealth summary for check-in.
//
// The two reachability answers here are NOT measured the same way, on purpose:
//
//   - publicReachable asks "can traffic leave this router for the open
//     internet", and must therefore be measured on an ORDINARY socket — that is
//     the path a client's traffic takes, and the whole point of the probe.
//   - serverReachable asks "can this router reach its panel", and the control
//     plane does not use an ordinary socket. It carries
//     firewall.DefaultControlMark, which the output chain returns on as its
//     first rule. An unmarked probe to the panel is stamped with FwMark by the
//     last rule of that chain and routed to `local 0.0.0.0/0 dev lo`, so it
//     never leaves the box.
//
// Measuring the second with the first's socket, which is what this did, made
// serverReachable structurally FALSE on every router with the data plane
// loaded — while the check-in carrying that false was succeeding on the very
// same loop. A field that is always false carries no information, and the
// data-plane stand had already measured it from the other side
// (MODE=killswitch, ks_ctl_unmarked_blocked).
func (d *daemon) evaluateHealth(ctx context.Context, inv *controlplane.RouterInventory) (controlplane.RouterHealth, rescue.Decision) {
	serverReachable := d.controlPlaneReachable(ctx)
	decision := d.rescueStep(ctx)

	// No ID/Label here: panelReachability is parsed by the panel as
	// routerGroupedReachabilitySchema, which declares neither, so zod stripped
	// both on arrival. See the RouterReachabilityProbe doc comment.
	//
	// Status IS set, and only once the answer is ground truth. The panel reads
	// exactly one field off this object — `args.inventory.panelReachability?.status`
	// in router-control.ts, stored as the router's panelStatus — and this
	// controller had never set it, so panelStatus was null for every xray router
	// forever. Left unset on the very first loop, where the answer is still a
	// probe rather than a completed control-plane exchange: "unknown" is
	// honest there, "blocked" would be a guess written into an operator-facing
	// column.
	panel := &controlplane.RouterReachabilityProbe{
		Reachable: serverReachable,
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if d.lastControlPlaneOK != nil {
		panel.Status = "blocked"
		if serverReachable {
			panel.Status = "reachable"
		}
	}
	inv.PanelReachability = panel

	return controlplane.RouterHealth{
		CurrentMode:                 string(decision.NextState.Mode),
		ProxyConnectivitySuccesses:  decision.NextState.ProxySuccessCount,
		DirectConnectivitySuccesses: decision.NextState.DirectSuccessCount,
		PublicConnectivityFailures:  decision.NextState.ProxyFailureCount,
		ServerReachable:             serverReachable,
	}, decision
}

// rescueStep probes the way out, tunnel and around it, and moves the rescue
// state on by one observation (rescue.Evaluate) — once a poll, and between the
// polls while a probe has just failed (rescueRecheckDue).
func (d *daemon) rescueStep(ctx context.Context) rescue.Decision {
	// 8 s: the probe's name is now resolved through the tunnel too (dnsmasq's
	// upstream goes into xray), and a slow but working tunnel must not look
	// dead three times in a row.
	hc := &http.Client{Timeout: 8 * time.Second, Transport: tunnelProbeTransport}
	cur := d.rescueState()
	if d.operatorDirect() {
		// The operator's direct mode is the operator's: it lasts until a
		// reconnect, whatever the tunnel does.
		d.rescueCheckedAt = time.Now()
		return rescue.Decision{NextMode: cur.Mode, NextState: cur}
	}
	if cur.Mode == rescue.ModeProxy && d.desired == nil && (cur.ProxyFailureCount > 0 || !cur.FirstFailureAt.IsZero()) {
		// No owner's config — a router released, or not yet given one after
		// a claim: there is no tunnel, the LAN goes out directly, and a run of
		// "failures" counted against it is not an outage to carry into the
		// first config.
		cur.ProxyFailureCount, cur.FirstFailureAt = 0, time.Time{}
	}
	var publicReachable bool
	if cur.Mode == rescue.ModeProxy {
		publicReachable = d.probeThroughTunnel(ctx, hc)
	} else {
		publicReachable = rescue.ProbeAny(ctx, hc, d.rescuePolicy.HealthURLs)
	}

	// Direct reachability is asked AROUND the tunnel: the control plane's
	// client, whose sockets the output chain returns on and which resolves
	// names itself (controlplane/resolve.go). It used to be publicReachable
	// again — the tunnel's own answer — so with the tunnel dead the rescue's
	// "can we go direct" was false in exactly the case it exists for, and
	// the router never left proxy mode by itself. Asked only when the tunnel
	// failed: otherwise there is nothing to decide.
	directReachable := publicReachable
	if !publicReachable && cur.Mode == rescue.ModeProxy && !d.killSwitchArmed() {
		directReachable = rescue.ProbeAnyWithin(ctx, d.client.HTTPClient(), d.rescuePolicy.HealthURLs, directProbeBudget)
	}
	// Never direct under the kill switch: it promises the LAN's traffic never
	// leaves unproxied, and a direct mode is exactly that. It fails closed —
	// the rescue does not open it.
	if d.killSwitchArmed() {
		directReachable = false
	}
	decision := rescue.Evaluate(rescue.Input{
		CurrentState:    cur,
		PublicReachable: publicReachable,
		// xray itself down — a crash, a restart — is not the tunnel failing:
		// the kernel already lets the LAN past it (TPROXY falls through), and
		// direct mode would only keep the VPN off for a cooldown after xray
		// is back (the crash-loop drill on 1111, 2026-10-04). Nor is a
		// router with no tunnel at all, or one whose new config has not
		// settled yet (tunnelJudgeable).
		ProxyConclusive: cur.Mode == rescue.ModeProxy && d.tunnelJudgeable(time.Now()),
		DirectReachable: directReachable,
		TunnelDead:      d.tunnelDead(time.Now(), cur.LastTransitionAt),
		Now:             time.Now(),
	}, d.rescuePolicy)
	if decision.NextState.Mode == rescue.ModeDirect && d.st.Rescue.Mode != string(rescue.ModeDirect) {
		d.incident("RESCUE_DIRECT", reKeyNumber.ReplaceAllString(decision.Reason, "N"), "the rescue switched the router to direct: "+decision.Reason, nil)
	}
	if !publicReachable && decision.NextState.ProxyFailureCount != cur.ProxyFailureCount {
		// One miss is a slow answer, as often as not (1111 saw two a day, each
		// gone at the next look): a warning in the owner's journal only from
		// the second in a row.
		log := logging.L().Info
		if decision.NextState.ProxyFailureCount >= 2 {
			log = logging.L().Warn
		}
		log("rescue: no answer through the tunnel", "failures", decision.NextState.ProxyFailureCount,
			"of", d.rescuePolicy.TriggerFailureCount, "direct_reachable", directReachable)
	}
	d.storeRescueState(decision.NextState, decision.Reason)

	d.rescueCheckedAt = time.Now()
	return decision
}

// probeThroughTunnel asks the trace urls the way the LAN's traffic goes, and
// counts only an answer that did not come from the router's own WAN address:
// one that did went around the tunnel and says nothing of it.
func (d *daemon) probeThroughTunnel(ctx context.Context, hc *http.Client) bool {
	if len(d.rescuePolicy.TraceURLs) == 0 {
		return rescue.ProbeAny(ctx, hc, d.rescuePolicy.HealthURLs)
	}
	d.refreshWANIP(ctx, time.Now())
	ips := rescue.ProbeTraceAll(ctx, hc, d.rescuePolicy.TraceURLs, 8*time.Second, d.wanIP)
	for _, ip := range ips {
		if ip != d.wanIP && !d.tunnelIPs[ip] {
			// An address not seen before: learn the WAN's again now, so an
			// address change (a PPPoE redial, CGNAT) cannot pass a probe
			// that went around the tunnel until the next look.
			d.wanIPAt = time.Time{}
			d.refreshWANIP(ctx, time.Now())
			break
		}
	}
	through, around := false, false
	for _, ip := range ips {
		if d.wanIP != "" && ip == d.wanIP {
			around = true
			continue
		}
		through = true
		if d.tunnelIPs == nil || len(d.tunnelIPs) >= 16 {
			d.tunnelIPs = map[string]bool{}
		}
		d.tunnelIPs[ip] = true
	}
	if !through && around {
		if !d.traceAroundSaid {
			d.traceAroundSaid = true
			logging.L().Warn("rescue: the probe through the tunnel came back only from the router's own address; it went around the tunnel and counts as no answer")
		}
		return false
	}
	if through {
		d.traceAroundSaid = false
	}
	return through
}

// ipv4Transport dials IPv4 only: the path the router carries through the
// tunnel, and the family its WAN address is learned in (the control plane
// resolves "ip4"). Over IPv6 the probe could leave around the tunnel and
// answer from an address no IPv4 comparison catches.
// A fresh connection every probe (no keep-alive): one kept from before a
// failure would answer for a tunnel that is gone.
var tunnelProbeTransport = ipv4Transport()

func ipv4Transport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DisableKeepAlives = true
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	t.DialContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp4", addr)
	}
	return t
}

// wanIPEvery is how often the router learns its own WAN address: the trace
// asked around the tunnel, on the control plane's path.
const wanIPEvery = 10 * time.Minute

func (d *daemon) refreshWANIP(ctx context.Context, now time.Time) {
	if d.client == nil || (!d.wanIPAt.IsZero() && now.Sub(d.wanIPAt) < wanIPEvery) {
		return
	}
	d.wanIPAt = now
	if ip, ok := rescue.ProbeTrace(ctx, d.client.HTTPClient(), d.rescuePolicy.TraceURLs, directProbeBudget); ok {
		d.wanIP = ip
	}
}

// xraySettle is how long xray has to run before a failed probe through it
// says anything about the tunnel.
const xraySettle = 15 * time.Second

// xraySettled: xray runs, and has for xraySettle. With no supervisor started
// there is no xray to wait for: yes.
func (d *daemon) xraySettled(now time.Time) bool {
	if d.sup == nil || !d.supStarted {
		return true
	}
	st := d.sup.Status()
	return st.State == supervisor.StateRunning && now.Sub(st.StartedAt) >= xraySettle
}

// tunnelJudgeable: there is a tunnel for the rescue to judge — an owner's
// config, xraySettle past the last apply (rescueAfresh), and xray settled.
// Without a config (released, or claimed and not given one yet) a probe
// "through the tunnel" goes out the WAN and fails by design: counted, it sent
// a router just claimed in the Vectra app to direct mode 20 s after the claim,
// and kept its first config off the VPN for the two-minute cooldown (vctl
// r17, 2026-10-04).
func (d *daemon) tunnelJudgeable(now time.Time) bool {
	if d.desired == nil {
		return false
	}
	if !d.reconfiguredAt.IsZero() && now.Sub(d.reconfiguredAt) < xraySettle {
		return false
	}
	return d.xraySettled(now)
}

// rescueAfresh: an apply has just put a new config in and loaded the data
// plane for it. The failures counted before were another tunnel's — or none
// at all — so the rescue's window starts again, once xray settles on the new
// one. In the rescue's direct mode, an apply directMayEnd allowed has just put
// the router back on the tunnel: the rescue says so (proxy) and judges it
// like any other. The operator's direct mode is never ended here.
func (d *daemon) rescueAfresh(now time.Time) {
	d.reconfiguredAt = now
	st := d.rescueState()
	st.ProxyFailureCount, st.FirstFailureAt = 0, time.Time{}
	reason := ""
	if st.Mode == rescue.ModeDirect && d.fwProgrammed != nil && !d.operatorDirect() {
		st.Mode, st.DirectSuccessCount, st.LastTransitionAt = rescue.ModeProxy, 0, now
		reason = "a new config was applied; judging its tunnel afresh"
	}
	d.storeRescueState(st, reason)
}

// operatorDirectReason is the reason the direct-mode job records.
const operatorDirectReason = "operator requested direct mode"

// rescueSourceOperator marks the operator's direct mode (RescueSnapshot.Source).
const rescueSourceOperator = "operator"

// operatorDirect: the router is direct because the operator asked
// (jobEnterDirect). It stays so until the operator reconnects: neither the
// rescue nor an apply takes it back to the proxy. A state file from before
// r18 has no source; its reason tells.
func (d *daemon) operatorDirect() bool {
	r := d.st.Rescue
	if r.Mode != string(rescue.ModeDirect) {
		return false
	}
	return r.Source == rescueSourceOperator || r.Source == "" && r.LastReason == operatorDirectReason
}

// directMayEnd: an apply in direct mode may load the data plane and put the
// router on the new tunnel now — otherwise the config waits on disk for the
// rescue's own way back (reapplyFirewall reads it), and the LAN keeps its
// internet. Never out of the operator's direct mode. Other nodes or
// credentials than the ones the rescue left — likely the fix — at once,
// unless returns have already failed in a row: then only after the backoff's
// cooldown, so applies of dead nodes do not swing the LAN. The same nodes only
// when the rescue itself would go back: the cooldown over, the tunnel not
// known dead.
func (d *daemon) directMayEnd(now time.Time, nodesChanged bool) bool {
	if d.operatorDirect() {
		return false
	}
	st := d.rescueState()
	cooldownOK := st.LastTransitionAt.IsZero() || now.Sub(st.LastTransitionAt) >= rescue.CooldownAfter(d.rescuePolicy, st.FailedRetries)
	if nodesChanged {
		return st.FailedRetries == 0 || cooldownOK
	}
	return cooldownOK && !d.tunnelDead(now, st.LastTransitionAt)
}

// renderNodesKey fingerprints the nodes the running render dials: every
// outbound but xray's own (freedom, blackhole, dns, loopback), with its
// settings — addresses and credentials. "" when there is no render.
func (d *daemon) renderNodesKey() string {
	raw, err := vault.ReadFile(d.cfg.XrayRenderPath)
	if err != nil {
		return ""
	}
	var doc struct {
		Outbounds []struct {
			Protocol       string          `json:"protocol"`
			Settings       json.RawMessage `json:"settings"`
			StreamSettings json.RawMessage `json:"streamSettings"`
		} `json:"outbounds"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return ""
	}
	h := sha256.New()
	for _, o := range doc.Outbounds {
		switch o.Protocol {
		case "freedom", "blackhole", "dns", "loopback":
			continue
		}
		h.Write([]byte(o.Protocol))
		h.Write([]byte{0})
		h.Write(o.Settings)
		h.Write([]byte{0})
		h.Write(o.StreamSettings)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// xrayRestartWait bounds the wait for xray to come back after a reload (a
// var: tests shorten it); xrayRestartPoll is how often it looks.
var xrayRestartWait = 10 * time.Second

const xrayRestartPoll = 100 * time.Millisecond

func (d *daemon) xrayStatus() supervisor.Status {
	if d.xrayStatusFn != nil {
		return d.xrayStatusFn()
	}
	if d.sup == nil {
		return supervisor.Status{}
	}
	return d.sup.Status()
}

// reloadXray restarts xray and waits, at most xrayRestartWait, for the new
// process. Reload only signals the old one, which goes on answering — on its
// DNS inbound too — for a moment: a data plane programmed then put the DNS
// redirect on a process about to exit, and the cache emptied then filled
// again with FakeDNS answers the new one does not know. false: no new xray
// within the bound. With no xray started there is nothing to wait for: true.
func (d *daemon) reloadXray(ctx context.Context) bool {
	if !d.supStarted {
		return true
	}
	t0 := time.Now()
	reload := d.reloadXrayFn
	if reload == nil {
		reload = func(ctx context.Context) error { return d.sup.Reload(d.supCtx) }
	}
	if err := reload(ctx); err != nil {
		// Between two starts (a crash's backoff): the supervisor's next start
		// is as good a new xray.
		logging.L().Debug("xray reload", "err", err.Error())
	}
	return d.waitXrayAfter(ctx, t0)
}

// waitXrayAfter waits, at most xrayRestartWait from t0, for an xray started
// after t0 to run.
func (d *daemon) waitXrayAfter(ctx context.Context, t0 time.Time) bool {
	deadline := t0.Add(xrayRestartWait)
	for {
		if st := d.xrayStatus(); st.State == supervisor.StateRunning && st.StartedAt.After(t0) {
			return true
		}
		if !time.Now().Before(deadline) {
			logging.L().Warn("xray did not come back within its bound after a restart", "waited", xrayRestartWait.String())
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(xrayRestartPoll):
		}
	}
}

// flushAfterXrayRestart: xray has started anew since the last look — a
// reload after a subscription refresh or a location change, the failover
// watchdog's restart, a crash — and its render hands out FakeDNS addresses
// through the DNS redirect: the old process's pool went with it, and what
// dnsmasq cached from it leads nowhere. Once the new one answers on its DNS
// inbound the cache is emptied, once per start. The first look only notes
// the start: the data plane's programming at it empties the cache.
func (d *daemon) flushAfterXrayRestart(ctx context.Context) {
	if !d.supStarted {
		return
	}
	st := d.xrayStatus()
	if st.State != supervisor.StateRunning || st.StartedAt.IsZero() || st.StartedAt.Equal(d.flushedStart) {
		return
	}
	if d.flushedStart.IsZero() || d.fwProgrammed == nil || d.rescueState().Mode == rescue.ModeDirect {
		d.flushedStart = st.StartedAt
		return
	}
	port, in := redirectPort(*d.fwProgrammed)
	raw, err := vault.ReadFile(d.cfg.XrayRenderPath)
	if !in || err != nil || len(xray.RenderFakeDNSPools(raw)) == 0 {
		d.flushedStart = st.StartedAt
		return
	}
	if !d.answers(ctx, port) {
		return // not back yet: the next look
	}
	d.flushedStart = st.StartedAt
	d.flushResolverCache("xray restarted; the FakeDNS answers it gave are gone")
}

// rescueRecheckEvery is how soon, between the polls, the rescue looks again
// while it is unsure: a probe through the tunnel has just failed, or direct
// mode may go back to the proxy. At the polls' pace a dead tunnel took three
// minutes to leave — measured on 1111 on 2026-10-04: every node blocked, the
// LAN 147 s without internet before the router went direct.
const rescueRecheckEvery = 10 * time.Second

// rescueFailingEvery is the recheck's pace once a probe through the tunnel
// has failed: a failed step already takes up to 14 s (the tunnel's timeout,
// then the probe around it), so three of them at a 10 s pace took a minute.
const rescueFailingEvery = 3 * time.Second

// rescueRecheckDue: the rescue is to look again now, between the polls.
func (d *daemon) rescueRecheckDue(now time.Time) bool {
	if d.killSwitchArmed() {
		return false
	}
	st := d.rescueState()
	every := rescueRecheckEvery
	if st.Mode == rescue.ModeProxy && st.ProxyFailureCount > 0 {
		every = rescueFailingEvery
	}
	if now.Sub(d.rescueCheckedAt) < every {
		return false
	}
	switch st.Mode {
	case rescue.ModeProxy:
		// Not while xray itself is down or starting: the probe would say
		// nothing (ProxyConclusive), and the loop would sit in its timeouts
		// instead of serving the DNS watch and the router UI.
		// The watchdog seeing the main balancer down starts the looking
		// too: the polls alone found a blocked tunnel a minute late.
		// On the watchdog's word alone, at most once a poll's half: while
		// a borrowed country carries the main traffic the balancer stays
		// "down" for as long as its own nodes are, and the probe passes.
		if st.ProxyFailureCount == 0 && d.tunnelFailing(now) && now.Sub(d.rescueCheckedAt) < 30*time.Second {
			return false
		}
		return (st.ProxyFailureCount > 0 || d.tunnelFailing(now)) && d.tunnelJudgeable(now)
	case rescue.ModeDirect:
		// Once the cooldown allows the way back and a node lives: the
		// tunnel's return is then taken within seconds, not at the next poll.
		// Never out of the operator's direct mode.
		if d.operatorDirect() {
			return false
		}
		return now.Sub(st.LastTransitionAt) >= rescue.CooldownAfter(d.rescuePolicy, st.FailedRetries) && !d.tunnelDead(now, st.LastTransitionAt)
	}
	return false
}

// controlPlaneReachable answers "can this router reach its panel".
//
// The source is the control plane itself: whether the PREVIOUS loop's
// register/check-in completed. That is ground truth — it is the exact request
// whose reachability the field is describing, over the exact socket it uses —
// and it cannot be wrong about a health endpoint that is missing, renamed or
// answering a non-2xx. A probe can be; this one used to be, in both directions
// at once.
//
// Before any exchange has happened there is nothing to report, so it falls back
// to probing /healthz and /api/health — through the CONTROL-PLANE client, so
// the probe travels the marked path the real request will take.
func (d *daemon) controlPlaneReachable(ctx context.Context) bool {
	if d.lastControlPlaneOK != nil {
		return *d.lastControlPlaneOK
	}
	return rescue.ProbeAny(ctx, d.client.HTTPClient(), serverHealthURLs(d.cfg.ControlURL))
}

// noteControlPlane records the outcome of a register/check-in for the next
// loop's serverReachable, and the backoff of the next exchange.
func (d *daemon) noteControlPlane(ok bool) {
	d.lastControlPlaneOK = &ok
	if ok {
		d.cpFailures, d.cpRetryAt = 0, time.Time{}
		return
	}
	d.cpFailures++
	d.cpRetryAt = d.now().Add(checkInBackoff(d.cfg.PollInterval(), d.cpFailures, rand.Float64))
}

// checkInMaxBackoff caps the wait between exchanges with a panel that does
// not answer.
const checkInMaxBackoff = 5 * time.Minute

// checkInBackoff is the wait after failures exchanges in a row failed: the
// poll interval doubled for each one after the first, at most
// checkInMaxBackoff, drawn between its half and the whole (rnd in [0,1)) —
// so a fleet the panel lost does not come back all at once, and a router
// does not spend its loop on a panel that is gone. The data plane never
// waits for it.
func checkInBackoff(poll time.Duration, failures int, rnd func() float64) time.Duration {
	if failures <= 0 {
		return 0
	}
	wait := poll
	for i := 1; i < failures && wait < checkInMaxBackoff; i++ {
		wait *= 2
	}
	if wait > checkInMaxBackoff {
		wait = checkInMaxBackoff
	}
	return wait/2 + time.Duration(rnd()*float64(wait/2))
}

func (d *daemon) rescueState() rescue.State {
	st := rescue.State{
		Mode:               rescue.Mode(d.st.Rescue.Mode),
		ProxyFailureCount:  d.st.Rescue.ProxyFailureCount,
		DirectSuccessCount: d.st.Rescue.DirectSuccessCount,
		FailedRetries:      d.st.Rescue.FailedRetries,
	}
	if st.Mode == "" {
		st.Mode = rescue.ModeProxy
	}
	if d.st.Rescue.LastTransitionAt != "" {
		if t, err := time.Parse(time.RFC3339, d.st.Rescue.LastTransitionAt); err == nil {
			st.LastTransitionAt = t
		}
	}
	if d.st.Rescue.FirstFailureAt != "" {
		if t, err := time.Parse(time.RFC3339, d.st.Rescue.FirstFailureAt); err == nil {
			st.FirstFailureAt = t
		}
	}
	return st
}

func (d *daemon) storeRescueState(s rescue.State, reason string) {
	d.st.Rescue.Mode = string(s.Mode)
	d.st.Rescue.ProxyFailureCount = s.ProxyFailureCount
	d.st.Rescue.DirectSuccessCount = s.DirectSuccessCount
	d.st.Rescue.FailedRetries = s.FailedRetries
	d.st.Rescue.FirstFailureAt = ""
	if s.Mode != rescue.ModeDirect {
		d.st.Rescue.Source = ""
	}
	if !s.FirstFailureAt.IsZero() {
		d.st.Rescue.FirstFailureAt = s.FirstFailureAt.UTC().Format(time.RFC3339)
	}
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
		if expected := d.st.CurrentJob.ExpectedControllerVersion; expected != "" && d.st.PendingJobResult.Status == "success" {
			ok, err := maintenanceConfirmVersion(ctx, expected)
			if err != nil {
				return
			}
			if !ok {
				d.st.PendingJobResult.Status = "failure"
				d.st.PendingJobResult.Result = map[string]interface{}{"code": "update_version_unverified"}
				if d.persist() != nil {
					return
				}
			}
		}
		if d.persist() != nil {
			return
		}
		if _, err := d.client.SubmitJobResult(ctx, *d.st.PendingJobResult); err == nil {
			if d.connectRecoverDelivered(d.st.PendingJobResult.JobID, d.st.PendingJobResult.Status) != nil {
				return
			}
			d.st.PendingJobResult = nil
			d.st.CurrentJob = state.CurrentJob{}
			_ = d.persist()
		}
		return
	}
	if d.st.CurrentJob.JobID != "" {
		job := controlplane.Job{ID: d.st.CurrentJob.JobID, Type: d.st.CurrentJob.JobType}
		status, code := "failure", "controller_restarted"
		if job.Type == "connect_router_action" {
			binding := d.connectBinding()
			journal, err := connectactions.OpenJournal(d.cfg.StatePath + ".connect-actions.json")
			if err != nil {
				return
			}
			record, found, err := journal.Lookup(binding, job.ID)
			if err != nil {
				return
			}
			if found {
				switch record.Status {
				case connectactions.Succeeded:
					status, code = "success", "recovered_terminal"
				case connectactions.Failed:
					code = "recovered_terminal"
				}
				if record.Action == "reboot" && d.maintenancePendingReboot(binding.OwnerRef, job.ID) {
					status, code = "accepted", "reboot_pending"
				}
			}
		}
		if d.finishJob(ctx, job, status, "", "", map[string]interface{}{"code": code}) == nil && job.Type == "connect_router_action" {
			if d.connectRecoverDelivered(job.ID, status) != nil {
				d.st.PendingJobResult = &controlplane.JobResultRequest{ProtocolVersion: controlplane.ProtocolVersion, RouterID: d.st.RouterID, JobID: job.ID, Status: status, Result: map[string]interface{}{"code": code}}
				_ = d.persist()
			}
		}
	}
}

func (d *daemon) persist() error {
	d.st.ExitEgress = egressStamps(d.exits.EgressSnapshot())
	// Written only when it changed (state.Saver): a check-in that changed
	// nothing writes nothing to flash.
	return d.saver.Save(d.cfg.StatePath, d.st)
}

// currentCounts reports node/subscription counts for check-in inventory.
// "Nodes" are the provider document's outbounds; the count is cached by
// refreshNodeCount so a ~485 KB file is not re-parsed every poll.
func (d *daemon) currentCounts() (nodes, subs int) {
	if d.desired != nil {
		subs = len(d.desired.Subscriptions)
	}
	return d.nodeCount, subs
}

// countProviderOutbounds counts the provider document's outbounds without
// decoding them: each element stays a json.RawMessage.
func countProviderOutbounds(raw []byte) int {
	var doc struct {
		Outbounds []json.RawMessage `json:"outbounds"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return 0
	}
	return len(doc.Outbounds)
}

// adoptDesiredRevision persists a desired revision as this controller's, but
// ONLY when it belongs to this engine.
//
// The panel is not a single version. A panel that predates the xray-direct
// contract has no engineMode on the revision at all and serves only PassWall
// configs (packages/contracts desiredRevisionSummarySchema on main:
// `config: passwallDesiredConfigSchema`, no engineMode field), and it has no
// engine guard in resolveDesiredRevision — so it hands a vctl router whatever
// revision that router points at, which is a PassWall one.
//
// Storing that was not harmless. LastDesiredRevision is persisted to state.json
// and is the FIRST thing jobApplyXrayConfig reaches for, so a PassWall revision
// picked up while the panel was still old would outlive the panel upgrade and
// shadow the real xray revision that arrives afterwards — and the failure would
// read as "the new panel sent a config vctl cannot parse".
//
// A panel that DOES serve this engine always sets the field: it is
// `engineModeSchema.default("passwall")` in the schema, so it is present on
// every revision that panel emits. Absent therefore means "not ours".
func (d *daemon) adoptDesiredRevision(rev *controlplane.DesiredRevisionSummary) {
	if rev.EngineMode == controlplane.EngineModeXrayDirect {
		d.st.LastDesiredRevision = rev
		return
	}
	// Once per revision, not once per poll: this is the steady state against a
	// panel that has not been upgraded yet, and the loop runs every 45s.
	if d.warnedForeignRevision != rev.ID {
		d.warnedForeignRevision = rev.ID
		mode := rev.EngineMode
		if mode == "" {
			mode = "unset (a panel that predates the xray-direct contract)"
		}
		logging.L().Warn(
			"the panel offered a desired revision for another engine; ignoring it. "+
				"This router reports engineMode="+controlplane.EngineModeXrayDirect+
				" and will run on its local operator config until the panel serves one.",
			"revisionId", rev.ID, "revisionEngineMode", mode,
		)
	}
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

// directProbeBudget bounds the rescue's probe around the tunnel: it runs only
// when the tunnel's probe has failed, every loop, and with the internet down
// it must not hold the loop for the control plane's whole timeout.
const directProbeBudget = 6 * time.Second

// killSwitchArmed: the operator's config arms the kill switch (fail closed).
func (d *daemon) killSwitchArmed() bool {
	return d.desired != nil && d.desired.Inbounds.Tproxy != nil && d.desired.Inbounds.Tproxy.KillSwitch
}

// restoreDataPlane puts the kernel side back at a start — but not in direct
// mode, which survives a restart in state.json: the rescue (or the operator)
// took the data plane down on purpose, and nothing loads it back or watches it
// there. A restart would otherwise run the LAN into a dead tunnel while the
// router says "direct". The rescue's recovery reloads it when the tunnel is
// back.
func (d *daemon) restoreDataPlane(ctx context.Context) {
	if d.desired != nil && d.rescueState().Mode != rescue.ModeDirect {
		d.programFirewall(ctx, d.desired)
	}
}
