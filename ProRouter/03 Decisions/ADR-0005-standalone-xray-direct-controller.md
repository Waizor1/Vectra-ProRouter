---
id: ADR-0005
status: accepted
date: 2026-05-31
tags:
  - adr
  - router-agent
  - control-plane
  - xray
  - passwall2
  - migration
---

# ADR-0005 Standalone xray-direct controller (`vectra-controller-pro`) replacing PassWall2

## Context

PassWall2 is a Lua + shell control surface on top of Xray. Every config push
forks `subscribe.lua` + `rule_update.lua` + `app.sh` + `nftables.sh` + dnsmasq
helpers; on low-RAM Filogic routers these forks have repeatedly caused `xray`
OOM (`1111111111` 2026-05-11, VagrandRouter, Kirill-MSK). PassWall2 also silently
normalizes operator intent (uTLS `fp=firefox` → `fingerprint=chrome`). The
approved strategy ([[project_xray_controller_strategy]], plan
`~/.claude/plans/eager-questing-harp.md`) is to drive Xray directly from our own
Go controller.

Today's deployed `router/vectra-controller-agent` (fleet at `0.1.13-r29`) is a
**superstructure over PassWall2**: it owns the autonomous control loop
(register/check-in/job-result, inventory, rescue/recovery, job-safety, watchdog,
OOM-guard, `.ipk`, self-update, version-drift) but delegates all Xray work to
PassWall2 via UCI + lua/shell.

A prior session (2026-05-28) built `router/vectra-controller-pro` — a clean,
standalone `vctl` binary that drives Xray directly (renderer, subscription
parser, supervisor, resource monitor, geo updater, nftables emitter, dnsmasq
generator, shell-out gRPC client; ~7200 LOC, 37 tests green). It is an excellent
**engine + manual CLI**, but has **no autonomy** — no control-plane loop, no
inventory reporting, no packaging.

The strategy doc had assumed the engine would be folded *into* the existing
agent as `internal/xrayengine`, carried by one binary with an `engineMode`
(`passwall` | `xray-direct`) discriminator, reusing the agent's proven autonomy.

## Decision

**Keep `vectra-controller-pro` as a separate, standalone controller binary** and
give it its own autonomy stack, rather than folding the engine into
`vectra-controller-agent`. (Decision by the product owner, 2026-05-31, overriding
the engineer's recommendation to fold-in.)

Concretely:

1. `vctl` gains ported analogues of the agent's autonomy: `internal/controlplane`
   (same wire protocol `2026-04-v1`), `internal/state` (identity, journal),
   `internal/inventory` (xray-native), `internal/apply` (XrayDesiredConfig →
   xray.json → supervised reload), `internal/jobsafety`, a pragmatic
   `internal/rescue`, and a `vctl agent` daemon loop.
2. The control plane gains an **additive** `engineMode` discriminator (default
   `passwall`), an `XrayDesiredConfig` schema, and xray-specific job/artifact
   types — the live passwall path is untouched.
3. **Canary identity reuse:** on a volunteer router `vctl` reuses the existing
   `RouterID` + `AgentToken` so the panel sees the *same* router flip to
   `xray-direct`. PassWall2 + the old agent stay installed-but-stopped as an
   instant rollback. Exactly one controller runs per router; `engineMode` is the
   single source of truth for which jobs/config the panel delivers.

## Consequences

### Positive
- Clean module boundary; the new controller carries no PassWall2 legacy.
- The engine's "no silent normalization" + single-binary, fork-free design is
  preserved end-to-end.
- The live fleet is shielded: every control-plane change defaults to `passwall`.

### Negative / Risks (and mitigations)
1. **Re-implementing battle-tested autonomy** (rescue/recovery/job-safety/
   self-update) risks regressions in logic the agent debugged over months.
   → *Mitigation:* port 1:1, reuse the same structs/thresholds, carry the tests.
2. **Protocol drift** — two independent clients against one API can diverge.
   → *Mitigation:* shared `ProtocolVersion`; contract-fixture tests on both sides.
3. **Code duplication** across two controllers for the foreseeable future.
   → *Accepted* as the cost; the old agent is retired once `xray-direct` is
   proven and rolled out.
4. **Deep self-recovery (auto-reboot phases) is deferred** in the first cut.
   → *Mitigation:* the canary keeps PassWall2 as an instant rollback, so the
   full recovery state machine is not a blocker for canary-ready.

## Scope of the first push (canary-ready)

Build + integrate + package + lab-validate to the point of being able to flip
`engineMode=xray-direct` on one healthy volunteer (non-`hh`, non-low-RAM).
**No live router is touched in this push.** Real `.ipk` build/feed publish, the
lua parity-oracle run, on-device dry-run, the live canary, fleet rollout, and
PassWall2 removal are each gated behind an explicit go-ahead.

## Alternatives considered
- **Fold the engine into `vectra-controller-agent` (engineer's recommendation,
  the strategy doc's original intent):** one binary, reuse all proven autonomy,
  one deploy lane, instant `engineMode→passwall` rollback. Lower risk but
  entangles the clean engine with PassWall2 legacy. **Not chosen** by the owner.
- **Promote `vctl` to immediately replace the agent fleet-wide:** rejected —
  violates canary discipline and the connectivity-first deploy doctrine.

## Related
[[project_xray_controller_strategy]] · [[feedback_deploy_guardrails]] ·
[[feedback_router_safety_guard]] · [[feedback_hh_router_no_touch]] ·
ADR-0004 (connectivity health checks) · ADR-0002 (panel-owned onboarding)
