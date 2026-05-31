# Vectra Controller Pro

**v0.1 Alpha** · single static Go binary that drives Xray-core directly (no PassWall2 in-between) on OpenWrt routers (target: Filogic / Xiaomi AX3000T).

> Locally-runnable test build. Not deployed to live routers. Not feature-complete vs. Xray's full surface — see [CHANGELOG / status](#status) for what's done and what's deferred.

## Why

PassWall2 is a Lua + shell control surface on top of Xray. Every config push forks `subscribe.lua` + `rule_update.lua` + `app.sh` + nftables.sh + dnsmasq helpers. On low-RAM routers we have seen OOMs caused by exactly those forks. PassWall2 also silently normalizes operator-set values (e.g. uTLS `fp=firefox` → `fingerprint=chrome`), which is brittle.

Vectra Controller Pro replaces that entire surface with a single Go binary that:
- **Drives Xray directly** — generates the Xray JSON, supervises the process, programs the kernel side itself (nftables + ip rules + fwmark).
- **Respects operator intent** — no silent value mutation. What you configure is what Xray gets. Normalization, when needed, is explicit and visible.
- **Watches itself** — RSS / FD / CPU monitor, soft memory cap, oom_score_adj, crash-restart with backoff.
- **Talks to Xray over gRPC** — hot add/remove outbounds on subscription refresh (no full Xray restart), real per-outbound health via Observatory, per-route traffic via Stats.

## Quickstart (local dev)

```bash
make build                                    # → bin/vctl
./bin/vctl version
./bin/vctl render -config examples/p0-vless-reality.json -out /tmp/xray.json
./bin/vctl validate -config examples/p0-vless-reality.json
./bin/vctl subscribe fetch -url '<url>' -hwid <hash> -ua passwall2/26.5.1 -out /tmp/sub.json
./bin/vctl supervise -config examples/p0-vless-reality.json   # runs Xray under supervisor
```

Tests:
```bash
make vet test            # full unit + golden-file
make race                # race detector
```

## Architecture

See [ARCHITECTURE.md](ARCHITECTURE.md). High-level:

```
                       ┌─────────────────────────────────────────┐
                       │              vctl (single binary)        │
                       │                                          │
   operator config     │  ┌──────────┐   ┌──────────────────┐    │
   (JSON, local file)──┼─▶│  config  │──▶│  coreengine/xray │────┼──▶  xray.json
                       │  └──────────┘   └──────────────────┘    │       │
                       │        │                ▲                │       │
                       │        ▼                │                │       ▼
   sub URL  ───────────┼──▶ subscription ────────┘                │   ┌─────────┐
                       │     (fetch+decode+URI parse)             │   │  xray   │  ← supervised process
                       │                                          │   └─────────┘
                       │  ┌──────────┐   ┌──────────────┐         │       │
                       │  │   geo    │   │   firewall   │         │       │ gRPC
                       │  │ updater  │   │  (nftables)  │         │       ▼
                       │  └──────────┘   └──────────────┘         │   ┌─────────┐
                       │                                          │   │   API   │  Stats / Handler / Observatory
                       │  ┌──────────────┐   ┌────────────────┐   │   │ client  │  (hot add/remove outbound,
                       │  │   supervisor │◀──│  resources mon │   │   └─────────┘   real-time health/traffic)
                       │  └──────────────┘   └────────────────┘   │
                       └─────────────────────────────────────────┘
```

## Design principles

1. **No silent normalization.** Operator config is law. If `fingerprint=firefox` is set, Xray gets `firefox`. We only normalize when explicitly requested (`vctl render --normalize`) and we log every change.
2. **One binary, no forks.** All subsystems are in-process. No spawning `lua` / `wget` / `opkg` / shell pipelines on the hot path.
3. **Crash-safe.** `state.json` is atomic-written with `.tmp + rename`; corruption falls back to `state.json.last-good`; Xray restart with exponential backoff and stability-based reset.
4. **Resource-aware.** Soft memory cap with graceful Xray reload (NOT kill) on threshold breach. `oom_score_adj` set to keep us alive. `/proc/self/status` + Xray process RSS monitored.
5. **Hot reload first.** Outbound add/remove via Xray HandlerService gRPC API where possible; full restart is fallback, not default.
6. **Lots of small explicit choices, no magic.** Config builder is straight-through translation; routing decisions are explicit; nothing is auto-inferred without a config flag.

## Status (v0.1 Alpha)

**Implemented (testable locally):**
- Operator config schema (`internal/config`) — schema v1.
- Subscription engine — base64 + URI parsers for vless / vmess / trojan / shadowsocks / hysteria2; V2RayN response-header parsing (`subscription-userinfo`, `profile-title`, `profile-update-interval`). PassWall2-impersonation HTTP fetcher (UA + x-device-* + x-hwid).
- Xray config builder — full P0 surface (VLESS+REALITY+Vision/gRPC, tproxy inbound, shunt routing, DoH DNS, fakedns); broad protocol/transport scaffolding.
- Process supervisor — start/stop/reload, restart-on-crash with exponential backoff + stability reset, structured logging, atomic state.
- Resources monitor — RSS/FD/CPU from `/proc`, alerts, soft cap with reload.
- Geo updater — geoip.dat/geosite.dat download, SHA verify, atomic swap.
- nftables emitter — generates the ruleset; CLI command to print/apply (apply gated by `--apply`).
- dnsmasq fragment generator.
- Xray gRPC client — Stats / Handler / Observatory wrappers.
- CLI binary `vctl` — render, validate, subscribe, supervise, firewall, geo, doctor.
- Golden-file tests for config builder.

**Deferred to v0.2+:**
- Broad-parity edge cases (mkcp/quic transports, balancer/observatory complex routing strategies, wireguard outbound).
- Production-grade observability (Prometheus exporter, OpenTelemetry).
- Multi-instance management / panel integration / OpenWrt packaging (`.ipk`).
- Live router validation (canary).
- nftables commit-confirm auto-revert (currently apply is one-shot; manual revert command exists).

## Repo layout

```
router/vectra-controller-pro/
├── cmd/vctl/                  # CLI binary
├── internal/
│   ├── config/                # Operator-facing config types
│   ├── subscription/          # Fetch + decode + URI parse
│   ├── coreengine/            # CoreEngine interface
│   │   └── xray/              # Xray-core implementation
│   ├── supervisor/            # Process lifecycle
│   ├── resources/             # Self-monitoring
│   ├── api/                   # Xray gRPC API client
│   ├── firewall/              # nftables emitter
│   ├── dns/                   # dnsmasq integration
│   ├── geo/                   # Geo updater
│   ├── state/                 # Persistent state
│   └── logging/               # Structured logging
├── examples/                  # Sample operator configs
├── testdata/                  # Golden files for config builder
├── ARCHITECTURE.md
├── README.md
├── Makefile
└── go.mod
```
