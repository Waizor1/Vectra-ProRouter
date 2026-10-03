# Vectra Controller Pro

**v0.1 Alpha** · single static Go binary that drives Xray-core directly (no PassWall2 in-between) on OpenWrt routers (target: Filogic / Xiaomi AX3000T).

> Locally-runnable test build. Not deployed to live routers. Not feature-complete vs. Xray's full surface — see [CHANGELOG / status](#status) for what's done and what's deferred.

r37 is a locally verified candidate: first-login password setup, reversible PassWall retirement, router tuning and Wi-Fi verdicts, owner-controlled support shell, and signed-feed update validation. See [r37 verification](docs/R37-VERIFICATION.md) for evidence and integration limits.

## Why

PassWall2 is a Lua + shell control surface on top of Xray. Every config push forks `subscribe.lua` + `rule_update.lua` + `app.sh` + nftables.sh + dnsmasq helpers. On low-RAM routers we have seen OOMs caused by exactly those forks. PassWall2 also silently normalizes operator-set values (e.g. uTLS `fp=firefox` → `fingerprint=chrome`), which is brittle.

Vectra Controller Pro replaces that entire surface with a single Go binary that:
- **Drives Xray directly** — installs the Xray JSON, supervises the process, programs the kernel side itself (nftables + ip rules + fwmark).
- **Respects operator intent** — no silent value mutation. What you configure is what Xray gets. Normalization, when needed, is explicit and visible.
- **Watches itself** — RSS / FD / CPU monitor, soft memory cap, oom_score_adj, crash-restart with backoff.
- **Talks to Xray over gRPC** — hot add/remove outbounds on subscription refresh (no full Xray restart), real per-outbound health via Observatory, per-route traffic via Stats.

## The proxy config comes from the provider, verbatim

The subscription returns **complete Xray documents**, not just node URIs — when
asked with a JSON user agent (e.g. `v2rayNG/1.9.5`) the provider answers
`application/json` with an array of full configs. `passwall2/*`, `Xray/*` and
`sing-box/*` get a degraded base64 `vless://` link list instead: same URL, same
device headers, different payload.

vctl therefore does **not** build a proxy config. It adopts the provider's
document wholesale and mutates exactly one thing: it replaces the `inbounds`
array with its own TPROXY inbound. Every other top-level key
(`log`, `dns`, `outbounds`, `routing`, `policy`, `stats`, `burstObservatory`) is
re-emitted byte-for-byte, in the provider's own order.

This is not fussiness. Parsing an entry and re-serializing it **corrupts** it: an
earlier Lua attempt turned `"tcpSettings":{}` into `"tcpSettings":[]` and Xray
died with *cannot unmarshal array into Go struct field*. The payload carries 388
empty `tcpSettings` objects and 26 empty `stats` objects and zero empty arrays.

Two consequences worth stating plainly:

- **The router fetches the subscription itself.** The provider document must
  never travel through the panel's revision pipeline — the `jsonb` column,
  `stableStringify` (which sorts every key), value masking and zod parsing each
  destroy byte-exactness. The panel carries only the *operator* config.
- **The provider's socks:10808 / http:10809 inbounds are dropped.** They have no
  `listen` key, so Xray would bind them to `0.0.0.0` — an unauthenticated open
  proxy on the LAN.

The operator config is correspondingly small: process supervision, the TPROXY
inbound, the geo asset dir, and which subscription to fetch. See
[`examples/operator-config.json`](examples/operator-config.json).

## Quickstart (local dev)

```bash
make build                                    # → bin/vctl
./bin/vctl version
./bin/vctl validate -config examples/operator-config.json

# Fetch the provider payload and pull out one complete config, verbatim.
./bin/vctl subscribe fetch -url '<url>' -ua 'v2rayNG/1.9.5' -out /tmp/sub.json
./bin/vctl subscribe parse -in /tmp/sub.json -content-type application/json \
    -entry 0 -out /tmp/provider-entry.json

# Splice in the TPROXY inbound (the only mutation) and inspect the result.
./bin/vctl render -config examples/operator-config.json \
    -provider /tmp/provider-entry.json -out /tmp/xray.json

# Same splice, then run Xray under the supervisor (gated on `xray -test`).
./bin/vctl supervise -config examples/operator-config.json -provider /tmp/provider-entry.json

# Device identity the provider keys on (reads /sys/class/net/eth0/address and
# /tmp/sysinfo/model, both TRIMMED — the model file has a trailing newline).
./bin/vctl subscribe hwid
```

Tests:
```bash
make vet test            # full unit + golden-file
make race                # race detector
```

## Architecture

See [ARCHITECTURE.md](ARCHITECTURE.md). High-level:

```
                        ┌──────────────────────────────────────────┐
                        │            vctl (single binary)          │
   operator config      │  ┌──────────┐                            │
   from the panel ──────┼─▶│  config  │── tproxy inbound ──┐       │
   (4 blocks)           │  └──────────┘                    ▼       │
                        │                       ┌────────────────┐ │
   provider sub URL ────┼─▶ subscription ──────▶│ coreengine/xray│ │
   (JSON user agent)    │   fetch + split       │ splice + scan  │ │
                        │   (entries stay raw)  └────────────────┘ │
                        │                                │         │
                        │                       xray -test gate    │
                        │                                ▼         │
                        │  ┌──────────┐  ┌────────────┐  xray.json │
                        │  │   geo    │  │  firewall  │      │     │
                        │  │ updater  │  │ (nftables) │      ▼     │
                        │  └──────────┘  └────────────┘  ┌───────┐ │
                        │                                │ xray  │ │ ← supervised
                        │  ┌────────────┐ ┌────────────┐ └───────┘ │
                        │  │ supervisor │◀│ resources  │      │gRPC│
                        │  └────────────┘ └────────────┘      ▼    │
                        │                                ┌───────┐ │
                        │                                │  API  │ │ Stats / Handler / Observatory
                        │                                └───────┘ │
                        └──────────────────────────────────────────┘

   Only `inbounds` is ever rewritten. Everything else is provider bytes.
```

## Design principles

1. **Byte-exactness over cleverness.** The provider's document is never parsed
   and re-emitted. `SpliceInbounds` copies each top-level value as a
   `json.RawMessage`; only `inbounds` is authored by us.
2. **No silent normalization.** Operator config is law. If `fingerprint=firefox`
   is set, Xray gets `firefox`.
3. **Fail closed on the write path.** `xray run -test` gates every install: a
   document Xray refuses is never written, and the previous good config stays
   live. A provider document that sets `allowInsecure:true` anywhere is refused
   outright (overridable per subscription, off by default).
4. **One binary, no forks.** All subsystems are in-process. No spawning `lua` /
   `wget` / `opkg` / shell pipelines on the hot path.
5. **Crash-safe.** `state.json` is atomic-written with `.tmp + rename`;
   corruption falls back to `state.json.last-good`; Xray restart with
   exponential backoff and stability-based reset.
6. **The control plane can never be routed by the data plane.** vctl's own
   sockets carry `SO_MARK 0x5643`; the nft output chain returns on it as its
   FIRST rule. Otherwise a dead provider chain costs the router its panel link
   and the commit-confirm deadman flaps the ruleset every 90s.
7. **Resource-aware.** Soft memory cap with graceful Xray reload (NOT kill) on
   threshold breach. `oom_score_adj` set to keep us alive.

## Status

**Implemented (testable locally):**
- Operator config schema (`internal/config`) — schema v1, four blocks:
  `process`, `inbounds.tproxy`, `geo`, `subscriptions`.
- Subscription engine — JSON variant (complete provider Xray configs, kept as
  raw byte slices) plus the degraded base64/URI fallback for diagnostics.
  V2RayN response-header parsing (`subscription-userinfo`, `profile-title`,
  `profile-update-interval`). Device-identity fetcher (`x-hwid`, `x-ver-os`,
  `x-device-model`, `x-device-os`) read from the router's own files.
- Inbound splice + read-only `allowInsecure` scan + `xray -test` write gate
  (`internal/coreengine/xray`).
- Process supervisor — start/stop/reload, restart-on-crash with exponential
  backoff + stability reset, `XRAY_LOCATION_ASSET` pinned for the child.
- Resources monitor — RSS/FD/CPU from `/proc`, alerts, soft cap with reload.
- Geo updater — geoip.dat/geosite.dat download, SHA verify, atomic swap.
- nftables emitter with the control-plane mark escape + commit-confirm deadman.
- Xray API: a native unary-gRPC client over cleartext HTTP/2 for the
  RoutingService (balancer selection, overrides), the metrics endpoint for the
  observatory and traffic, and the `xray api` CLI wrappers for the rest.
- Router UI: `vctl rpcd` (ubus object `vectra`), local overrides (location,
  pins, probe interval), the cached subscription array, the LuCI app.
- CLI binary `vctl` — render, validate, subscribe, supervise, firewall, geo,
  doctor, api, apply-local, agent, rpcd.

**Deferred:**
- Panel plumbing for "which profile + policy" (the panel does not and must not
  carry the provider document).
- Production-grade observability (Prometheus exporter, OpenTelemetry).
- Live router validation (canary) — and replacing the SYNTHETIC provider
  fixture in `internal/coreengine/xray/testdata/provider/` with a real
  scrubbed capture.
- Hot outbound add/remove via HandlerService on refresh (today: reload).

## Router UI

LuCI → **Services → Vectra**: a single-page app (Preact, ~40 KB gzipped, ru /
en / zh, dark and light) served from `/www/luci-static/vectra/` and rendered
inside a shadow root, so LuCI's theme and the app never style each other.

It reads and changes the router through ONE ubus object, `vectra` — an rpcd
exec plugin (`/usr/libexec/rpcd/vectra` → `vctl rpcd`) behind the ACL
`vectra-controller-pro`, reached through LuCI's authenticated session. The
contract is `ui/contract/`: a fixture per answer that both the Go tests and
the UI are held to. What it shows and does:

- **Overview** — xray, traffic interception, panel link, router resources,
  and diagnostics answered as codes the UI puts into words.
- **Balancing** — each of the provider's balancers: strategy, what traffic it
  carries, its members with xray's own probe results, what xray is selecting
  right now (native gRPC to xray's RoutingService — no `xray api` process per
  refresh), the fallback chain. A balancer can be **pinned** to one node: live,
  validated against the running config, and re-applied after every xray start.
  The **probe interval** replaces the provider's 12 h (default 10 min).
- **Nodes** — every node: probe verdict, delay, traffic, balancer membership.
- **Locations** — the subscription's locations from the local cache;
  switching re-renders locally and never asks the provider again.
- **Journal** — xray's log and the controller's, merged.

The daemon stays the only owner of xray and the firewall: changes reach it
over a 0600 unix socket and run on its loop, between check-ins.

## On and off

Vectra has a switch of its own, the same for the owner at the console and the
customer in the router UI:

```sh
vectra on                   # take the traffic, for good (also after a reboot)
vectra off                  # give the router back as it was: PassWall2, the
                            # previous agent, or the internet without a VPN
vectra status [--json]      # what is on, who carries the traffic, and the
                            # claim code while the router is not linked
vectra on --trial --minutes 10   # take it for 10 minutes, then give it back by itself
vectra keep                 # keep a trial: on for good
```

Before the panel's operator config arrives, a vctl that takes the router
from PassWall2 carries the traffic on PassWall2's own routes (route_source
'passwall', chosen automatically, on the panel's base operator config); the
claim's apply then moves it to the provider's, with no gap
(`cmd/vctl/auto_route.go`). With nothing to route by — no operator config,
no PassWall2 — `vectra on` says the LAN would go out without a VPN and asks
for `--force`.

`vectra` is `vctl power` (`internal/power`). On is the init script's takeover
(the legacy agent and PassWall stopped, disabled, and remembered — PassWall2's
own switch, `passwall2.@global[0].enabled`, turned off as well, or its nightly
subscription run restarts it next to Vectra); off is its hand-back. A change
runs detached — its log is /tmp/vectra-power.log — and one at a time. A trial
leaves every boot link as it was and notes what it owes on tmpfs, with a
uci-defaults snippet that turns PassWall2's switch on at the next boot, so a
power cycle brings back what ran before; its deadman turns Vectra off when the
minutes are up.
The UI's button is `set_power` (`ui/contract/README.md`, "Power").

A dead-man of Vectra's own watches vctl every minute, in shell
(`/usr/libexec/vectra-controller-pro/deadman.sh`, from root's crontab): a vctl
that should run and does not has its data plane unloaded at once — the LAN
goes out directly rather than into a black hole — and is started again with a
backoff (1, 2, 5, 10, 15 minutes, then every 15). Only where PassWall2 or the
old agent is owed back, after 10 minutes down, does it turn Vectra off and
give the router back. `logread -e vectra-controller-pro-deadman`.

The router answers **my.vectra-pro.net** and **vectra.lan** on the LAN,
Vectra on or off (dnsmasq, not vctl): this page is always at
http://my.vectra-pro.net and http://vectra.lan — typed with `http://`, since a
browser takes a bare `vectra.lan` for a search. A device that asks another
DNS (a VPN app, private DNS) gets neither name; it reaches the page at the
LAN's own address, 192.168.1.1 out of the box, which the page names too.

## Testing on a router

`docs/CANARY.md` — build an installable package from this checkout
(`scripts/build-local-ipk.sh`), install it without taking the router over,
then hand it over with a way back at every step.

## Repo layout

```
router/vectra-controller-pro/
├── cmd/vctl/                  # CLI binary
├── internal/
│   ├── config/                # Operator-facing config types (4 blocks)
│   ├── subscription/          # Fetch + classify; JSON entries stay raw
│   ├── apply/                 # digest → scan → splice → xray -test → install
│   ├── coreengine/
│   │   └── xray/              # Inbound splice, allowInsecure scan, -test gate
│   ├── supervisor/            # Process lifecycle
│   ├── resources/             # Self-monitoring
│   ├── api/                   # Xray API: native gRPC (h2c), metrics, CLI
│   ├── localctl/              # Router UI: overrides, locations cache, socket
│   ├── uiapi/                 # Router UI answers (ubus object "vectra")
│   ├── xrayview/              # Rendered config -> nodes/balancers, no secrets
│   ├── uaguard/               # Refuses the User-Agent the provider bans
│   ├── firewall/              # nftables emitter
│   ├── dns/                   # dnsmasq integration
│   ├── geo/                   # Geo updater
│   ├── state/                 # Persistent state
│   └── logging/               # Structured logging
├── ui/contract/               # Router UI <-> vctl contract (fixtures)
├── ui/app/                    # Router UI source (Preact); built into openwrt/files/www
├── docs/CANARY.md             # Hands-on test on one router
├── scripts/build-local-ipk.sh
├── examples/                  # Sample operator config
├── ARCHITECTURE.md
├── README.md
├── Makefile
└── go.mod
```
