---
id: ADR-0004
status: proposed
date: 2026-05-24
tags:
  - adr
  - router-agent
  - control-plane
  - monitoring
  - connectivity
---

# ADR-0004 Foreign-Service Connectivity Health Checks Without Router Overload

## Context

Operators want a per-router signal in the panel that answers one question
directly: **does this router currently give working access to foreign services
(YouTube, Instagram) through the proxy, or not?** The check should refresh at
most every ~30 minutes.

The hard constraint is memory safety. A previous generation of "checkers" for
Telegram/YouTube caused routers to run out of RAM, OOM-kill `xray`, and drop the
proxy (VagrandRouter `2026-05-11`, Kirill-MSK low-RAM class). Any new behavior
must not reintroduce that failure mode.

### Root cause of the historical OOM (confirmed in code)

There are two unrelated connectivity-probe mechanisms in the tree, with very
different cost profiles:

- **Heavy — the OOM culprit.**
  `passwall2/.../test.sh:44-59` `url_test_node` **spawns a second, full
  `xray`/`sing-box` process** (`app.sh run_socks` on a temp port), sleeps `1s`,
  curls through it, then `kill -9`s it. On a low-RAM Filogic router already
  running one proxy process, this doubles the proxy memory footprint for the
  duration of the test. Run on a schedule, this is exactly what exhausts RAM and
  triggers the kernel OOM-killer. This path is currently used only inside the
  rescue evaluator (`rescue_runtime.go:329-361`) and only *conditionally* (when a
  public probe already failed), so it is rare — not a steady-state cost.

- **Light — what the panel already uses.**
  `internal/inventory/collector.go` probes YouTube and Telegram with a plain Go
  `http.Client` GET from the agent process (~1-2 MB, no new process). This was
  deliberately hardened on `2026-05-12`: results are cached `30m`
  (`youtubeProbeCacheTTL`/`telegramProbeCacheTTL`), only run when PassWall is
  enabled and `running`, and only when `MemAvailable >= 128 MB`
  (`serviceReachabilityProbeFloorMB`); below `64 MB` they are skipped entirely
  (`2026-05-11 r16` hardening). Sequential, `3s` timeout per URL.

So YouTube and Telegram health is **already implemented, already on a 30-minute
budget, and already memory-gated**. The light path did not cause the OOM; the
heavy `url_test_node` path is the one to keep off any schedule. The data already
flows to the panel (`fleet-monitoring-data.ts:488-489`,
`router-card.tsx`, `router-detail-workspace.tsx`).

### What is actually missing

1. **Instagram** — no probe exists.
2. **A single consolidated per-router verdict** ("работает корректно / не
   работает") rather than two separate raw reachability blocks.
3. **Optional but more correct:** a *guaranteed* proxy-path probe. The light
   probe relies on the assumption that the router's own outbound traffic is
   proxied (the rescue evaluator at `rescue_runtime.go:158` treats a plain agent
   GET as a proxy-path indicator). If a router has "proxy the router itself"
   disabled, the probe would report a blocked foreign service as down even when
   client traffic is fine — a false negative.

## Decision

1. **Never put `url_test_node` (or any second-proxy-instance probe) on a
   schedule.** Routine connectivity health uses only the light in-process HTTP
   probe path, preserving the existing `30m` cache, the PassWall-enabled+running
   gate, and the `128 MB` memory floor.

2. **Add Instagram to the existing light probe path**, generalizing the
   near-duplicate `collectTelegramReachability` / `collectYouTubeReachability`
   into one helper. Keep targets minimal (1-2 per service) to reduce total GETs
   from the current 7 (3 YT + 4 TG) toward a leaner set.

3. **Compute the "works correctly" verdict on the server**
   (`fleet-monitoring.ts`), not on the router. The agent stays "dumb" and ships
   only raw per-service reachability. This lets us tune the verdict thresholds
   without re-flashing the fleet.

4. **Verdict logic that does not cry wolf:**
   - all probed services unreachable → 🔴 `не работает` (likely proxy/router
     fault — a common cause);
   - some unreachable → 🟡 `частично` (more likely that one service has its own
     outage than that the router is broken — do not raise a router-down alarm);
   - all reachable → 🟢 `работает корректно`;
   - PassWall off / low RAM / offline / no snapshot → ⚪ `неизвестно` (an honest
     blind spot, not a false green).

5. **Probe transport is phased** (see Implementation Plan). Phase 1 keeps the
   current implicit transport (light GET, relying on router-self-proxy). Phase 2
   may switch to an explicit proxy-path probe through the already-running main
   node's SOCKS inbound (`127.0.0.1:1070`, `global.node_socks_port`, see
   `app.sh:684-689`) — which reuses the running `xray` (no second process) and
   guarantees the probe traverses the proxy exactly like client traffic. Phase 2
   requires one live-router verification that `1070` is up and that router-self
   traffic routing matches the assumption; per `passwall2/CLAUDE.md` we do not
   overstate runtime certainty before that check.

## Implementation Plan

### Phase 1 — minimal-safe (selected)

Agent (`router/vectra-controller-agent`):
- `internal/inventory/collector.go`: add `instagramProbeTargets`
  (`https://www.instagram.com/`, `https://www.cdninstagram.com/`) and an
  `instagramProbeCache` mirroring the YouTube/Telegram structures; generalize the
  probe/summary/clone helpers into one service-parameterized helper to remove
  duplication. Wire it into `Collect()` next to lines 207-210 under the same
  `shouldCollectServiceReachability(inventory)` gate (line 546) — i.e. only when
  PassWall enabled+running and `MemAvailable >= 128 MB`. Keep `3s` timeout, `30m`
  cache, sequential.
- Go contract (`internal/controlplane`, `RouterInventory`): add
  `InstagramReachability *RouterReachabilityProbe` (additive, mirrors the two
  existing fields). No new probe type needed.

Contracts + DB:
- `packages/contracts/src/schemas.ts:494-495`: add `instagramReachability`
  alongside `telegramReachability`/`youtubeReachability` (optional/nullable).
- DB: none. The snapshot stores the whole `RouterInventory` in the JSONB
  `payload`, so the new field flows automatically — no migration.

Control plane (server):
- `fleet-monitoring-data.ts:488-489`: map `payload.instagramReachability`.
- `fleet-monitoring.ts`: extend the reachability/alert handling (the
  telegram/youtube touch points around `86-87, 171-172, 453, 467-468, 477,
  491-492, 822-823, 855, 858`) to include Instagram, and add a derived
  `connectivityVerdict` (`ok | partial | down | unknown`) using the rule in
  Decision §4.

Panel UI:
- `router-card.tsx` (`53-54, 154-163, 288, 301`): add Instagram next to
  YT/TG and render the consolidated verdict badge.
- `router-detail-workspace.tsx` (`1082, 1085, 1393, 1442`): same, with the
  per-target detail.
- `fleet-monitoring-workspace.tsx`: surface the verdict in the table/cards and as
  a filter.

Release: bump controller/LuCI to the next revision (current floor `0.1.13-r24`
→ `r25`), build with the canonical feed-publish lane, deploy to a safe pilot
router first, confirm the snapshot carries `instagramReachability` and the panel
shows the verdict, then roll the fleet (excluding `hh`).

### Phase 2 — guaranteed proxy-path probe (deferred)

- Add a SOCKS5 dialer to the agent and route the probe through
  `socks5h://127.0.0.1:<global.node_socks_port>` (default `1070`). Reuses the
  running proxy, no second process. Fall back to the Phase 1 direct GET if the
  port is not resolvable.
- Gate behind one live-router verification (port up; router-self routing
  assumption). Only then make it the default transport.

## Consequences

- Instagram health appears next to YouTube/Telegram with no new memory risk: it
  reuses the already-hardened light path (cache + memory floor + service gate),
  and explicitly avoids the `url_test_node` second-process pattern that caused the
  historical OOM.
- Operators get one at-a-glance verdict per router instead of interpreting two
  raw blocks; a single-service outage no longer looks like a broken router.
- Verdict logic lives server-side and is tunable without a fleet re-flash.
- Low-RAM routers (Kirill-MSK class) keep showing an honest ⚪ blind spot when
  below the memory floor, rather than spending RAM to produce a status.
- Phase 1 inherits the existing router-self-proxy assumption; Phase 2 removes
  that dependency once verified live.

## Verification

- Agent: `cd router/vectra-controller-agent && go test ./... -count=1` and
  `go vet ./...`; add coverage for the Instagram probe and the generalized
  helper (reachable / partial / blocked, cache TTL, memory-floor skip).
- Web: unit tests for the verdict rule in `fleet-monitoring.test.ts` and the
  Instagram mapping in `fleet-monitoring-data.test.ts`.
- Live: on a safe pilot router confirm the snapshot carries
  `instagramReachability`, the verdict renders, and `MemAvailable`/`xray` PID stay
  stable across a probe window (no OOM, no second proxy process observed).

## Open Questions

- Final Instagram target set and whether `cdninstagram.com` adds signal over
  `instagram.com` alone.
- Whether to trim YouTube (3→1) and Telegram (4→2) targets in the same change to
  cut total GETs.
- Phase 2 go/no-go after the `1070` live check.
