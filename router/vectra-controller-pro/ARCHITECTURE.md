# Architecture · Vectra Controller Pro v0.1 Alpha

A single Go binary that drives Xray-core directly on an OpenWrt router. No `lua` interpreter, no shell pipelines on the hot path — everything important happens in-process.

```
                          ┌──────────────────────────────────────────────────────────────┐
                          │                  vctl  (single static Go binary)             │
                          │                                                              │
   operator config        │   ┌────────────┐    ┌────────────────────┐                  │
   (JSON, on disk)  ──────┼──▶│   config   │───▶│  coreengine/xray   │── render ───────┼─▶ xray.json
                          │   │  + Normaliz│    │   (JSON builder)   │                  │
                          │   └────────────┘    └────────────────────┘                  │
                          │         │                     ▲                              │
                          │         ▼                     │ ParserDefaults audit         │
   subscription URL ──────┼─▶ ┌──────────────┐            │                              │
   (PassWall-impersonate)│   │ subscription │── adapter ──┘                              │
                          │   │  fetch+parse │                                            │
                          │   └──────────────┘                                            │
                          │                                                              │
                          │   ┌──────────┐  ┌────────────┐  ┌────────┐  ┌──────────┐    │
                          │   │   geo    │  │  firewall  │  │  dns   │  │  api     │    │
                          │   │ updater  │  │  (nftables)│  │ frags  │  │ (gRPC)   │    │
                          │   └──────────┘  └────────────┘  └────────┘  └────┬─────┘    │
                          │                                                  │           │
                          │   ┌─────────────────┐   ┌─────────────────┐      │           │
                          │   │   supervisor    │◀──│  resources mon  │      │           │
                          │   │ (process + mgmt)│   │  (RSS/FD/CPU)   │      │           │
                          │   └────────┬────────┘   └─────────────────┘      │           │
                          └────────────┼──────────────────────────────────────┼───────────┘
                                       │ exec                                 │ gRPC (via xray CLI)
                                       ▼                                      ▼
                                  ┌─────────┐                            ┌─────────┐
                                  │  xray   │ ◀──── HandlerService ──── │  xray   │ (same process)
                                  └─────────┘       (hot add/remove     └─────────┘
                                                     outbounds)
```

## Subsystems

| Package | Responsibility | Hot files |
|---|---|---|
| `internal/config` | Operator-facing schema; `Load/Save/Validate`; defaults with audit (`DefaultsDiff`). | `types.go` `node.go` `stream.go` `defaults.go` `validate.go` |
| `internal/subscription` | HTTP fetcher with PassWall2-impersonation headers + URI parsers (`vless`/`vmess`/`trojan`/`shadowsocks`/`hysteria2`) + base64 decoder + adapter into `config.Node`. **ParserDefaults audit trail** for every protocol-required value the parser fills in. | `fetcher.go` `parser.go` `uri_*.go` `adapter.go` |
| `internal/coreengine` | Stable `Engine` interface (room for sing-box in v0.2). | `interface.go` |
| `internal/coreengine/xray` | Xray-core renderer. Pure-Go translation from operator config → Xray JSON. **No silent value mutation.** | `engine.go` `render_*.go` `types.go` |
| `internal/supervisor` | Xray process lifecycle: start/stop/reload; exponential backoff with stable-uptime reset; OOM-score / rlimit / nice; resource monitor (RSS/FD/CPU) with soft-cap → reload (not kill). Crash-safe state writes (`atomicwrite.go` = `.tmp + fsync + rename + dir-fsync`). | `process.go` `backoff.go` `monitor.go` `resources.go` `limits_*.go` |
| `internal/api` | Xray gRPC API client. v0.1 ships a shell-out implementation (calls `xray api ...`); v0.2 will swap for native gRPC. Interface is stable. | `client.go` |
| `internal/firewall` | nftables emitter (block-form, atomic `nft -f -` apply). Routing-rule companion commands (`ip rule`, `ip route`). | `nft.go` |
| `internal/dns` | dnsmasq drop-in fragment generator (split DNS via `nftset=` hooks into the firewall's sets). | `dnsmasq.go` |
| `internal/geo` | `geoip.dat` / `geosite.dat` updater. SHA256 verify, atomic swap, fsync. | `geo.go` |
| `internal/logging` | thin slog wrapper. | `logging.go` |
| `cmd/vctl` | CLI: `version`, `validate`, `render`, `subscribe {fetch|parse|hwid}`, `supervise`, `firewall {render|apply|revert|routing}`, `geo {update|verify}`, `api {ping|stats|statquery|sys|add-outbound|rm-outbound|observatory|logger-restart}`, `doctor`. | `main.go` `cmd_*.go` |

## Data flow — single config-apply cycle

1. **operator** edits `/etc/vctl/config.json` (or pushes via future panel API).
2. `vctl validate` → strict JSON unmarshal (`DisallowUnknownFields`), then `Validate` runs schema + cross-field checks.
3. `vctl render` → `coreengine.Engine.Render(config)` → Xray JSON bytes (deterministic).
4. `vctl supervise`:
   - `WriteXrayConfig` (atomic + fsync).
   - `Process.Run` spawns `xray run -c <path>` under a process group, sets `oom_score_adj`, captures logs (append-mode).
   - `Monitor` ticks every N seconds: reads `/proc/<pid>/status`/`stat`/`fd`, writes status JSON atomically, enforces soft RSS cap by calling `Process.Reload` (intentional restart — does NOT bump the backoff counter).
   - On crash: backoff `Next()` (exponential), restart. A stable run ≥ `RestartBackoff.Reset` resets to `InitialMs`.
5. **subscription refresh** (manual today, scheduled in v0.2):
   - `vctl subscribe fetch -url … -hwid … -ua passwall2/X` → V2RayN body decode + per-line URI parse → adapter merges into `config.Nodes` (preserving `Origin.RawLink` + `Origin.ParserDefaults`).
   - Re-render config; restart Xray OR (future) hot-add via `api.Client.AddOutbound`.

## Invariants ("no silent normalization")

1. **Parser:** every value the parser fills in *because the protocol requires it* is recorded in `Node.Origin.ParserDefaults`. The renderer prints whatever is on the node — never overrides operator values.
2. **Defaults:** the only "defaults" applied to operator config live in `config.ApplyDefaults` and are enumerated by `config.DefaultsDiff` for `vctl validate -v` output.
3. **Normalization toggles:** opt-in only, off by default, every change logged at INFO level.
4. **Test enforcement:** `parser_test.go::TestParseVLESS_RealityTCPVision` asserts `fingerprint=firefox` round-trips; `engine_test.go::TestRender_RealityFingerprintPreserved` asserts the renderer carries it through to JSON; `audit_test.go::TestNoSilentNormalization_Trojan` asserts protocol-required defaults appear in `ParserDefaults`.

## Crash safety

- **State writes:** `supervisor.WriteStatus`, `config.Save`, `supervisor.Process.WriteXrayConfig` and `geo.UpdateOne` all use `OpenFile + Write + Sync + Close + Rename + dir-Sync` — a power loss between any two steps leaves either the old file intact or the new file fully on disk.
- **Subscription fetch:** every retry builds a fresh `*http.Request` (re-use is undefined behavior per net/http docs).
- **Supervisor restart:** intentional restarts (`Reload`, soft-cap reload, `Stop`) are tracked by `expectedExit` so they don't trip backoff or inflate `RestartCount`.
- **Process termination:** `Setpgid: true` + `kill -PGID` on grace expiry — no orphan workers.
- **Wait safety:** the cmd's `Wait()` is called exactly once per run; `Stop` and observers wait on a per-run `done` channel, never spawn a second `Wait`.
- **Firewall apply:** single `nft -f -` block-form transaction — atomic table replace, no race window.

## What's deliberately deferred to v0.2

- Native gRPC `ObservatoryService` (currently shells out to `xray api ...` for handler/stats; observatory CLI doesn't exist, so observatory returns `ErrNotImplemented`).
- nftables commit-confirm auto-revert (today: `firewall apply` is one-shot; manual `firewall revert` exists).
- Scheduled subscription / geo refresh (today: manual via CLI).
- Hot-add on subscription refresh via `HandlerService` (today: full Xray restart on config change).
- Panel API server (today: stand-alone CLI).
- OpenWrt `.ipk` packaging.

## Builds & tests

```
make build         # → bin/vctl                       (static, single binary)
make test          # 35 tests, all subsystems         (~3s)
make race          # full test suite under -race      (~12s)
make vet           # go vet ./...                     (0 warnings)
```

Module: `vectra-controller-pro`, Go `1.22.0`, **no external dependencies** (stdlib only).

## Canary cutover runbook (gated — do not run on a client router without sign-off)

`vctl` is a *second* controller package alongside the live `vectra-controller-agent`.
Exactly one controller owns a router at a time; `engineMode` is the source of truth.

1. Pick a healthy volunteer (non-`hh`, non-low-RAM). PassWall2 stays installed as
   the instant rollback.
2. Install `vectra-controller-pro`. Its `init.d` **stops + disables the legacy
   `vectra-controller`** automatically (mutual exclusion), and `vctl` **adopts
   the legacy router id + token** from `/etc/vectra-controller/state.json`
   (`state.ImportLegacyIdentity`) so the panel sees the *same* router flip to
   `xray-direct`, not a duplicate.
3. Flip the router's `engineMode` to `xray-direct` in the panel and push an
   `XrayDesiredConfig`. The first `apply_xray_config` programs the firewall behind
   **commit-confirm** (a detached deadman auto-reverts within 90s unless a
   successful check-in proves the panel link survived).
4. Rollback = re-enable `vectra-controller`, set `engineMode` back to `passwall`.

## Security posture (ratified for canary) + fast-follows before fleet rollout

Ratified, intentional decisions for the canary stage:

- **Self-update is fail-closed and engine-scoped.** `update_controller` refuses any
  artifact whose name/URL isn't `vectra-controller-pro`, requires a `sha256`
  (no checksum → refuse), caps the download, and only fetches over HTTPS.
- **All external fetches are HTTPS-pinned** (controller artifact, geo assets,
  subscriptions) — a downgraded link can't deliver cleartext config/binaries.
- **`run_terminal_command` runs operator-authored shell** from the authenticated
  panel (HTTPS + per-router token) — same trust model as the legacy agent.
- **nftables is fail-open-to-direct** (`policy accept`): a broken ruleset or a dead
  Xray lets traffic egress *direct* rather than black-holing the router. This
  matches PassWall2 and is acceptable for canary because PassWall2 remains the
  rollback. **Fast-follow (before fleet rollout):** an operator-selectable strict
  kill-switch mode that `drop`s non-bypass traffic when the proxy is unhealthy.

Tracked fast-follows (not canary blockers):

- **At-rest masking parity for xray configs.** Node secrets are stored encrypted in
  `passwallSecretBlobs` (AES-256-GCM), but the `XrayDesiredConfig` is currently also
  written to the revision `config` jsonb in cleartext (passwall masks this). Mirror
  `sanitizePasswallConfig` for xray and stop shipping the raw config to clients via
  `draft.list`.
- **`allowInsecure` from subscription nodes** is passed through (PassWall2 parity);
  gate it so only operator-set nodes may disable TLS verification.
- **Job ordering** is newest-first (the pre-existing shared contract); switch the
  delivery to creation order (asc) so queued applies run in order.
- **Native gRPC** Observatory/Handler hot-reload (today: shell-out `xray api` +
  active-probe health).
