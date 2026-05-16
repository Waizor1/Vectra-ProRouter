---
id: ADR-0003
status: proposed
date: 2026-05-17
tags:
  - adr
  - enrollment
  - filogic
  - cudy
  - hardware-support
---

# ADR-0003 Filogic-wide enrollment support (beyond AX3000T)

## Context

Today the enrollment surface is hard-coded around the Xiaomi AX3000T:

- generator constants in `apps/web/src/app/enrollment/install-presets.ts` are
  named `AX3000T_*` and the public preset is `ax3000tEnrollmentPreset`;
- three Next.js routes hard-code the model in their path:
  `/install/ax3000t-bootstrap.sh`, `/install/ax3000t-myshunt-rebind.sh`, and
  `/api/install/ax3000t-passwall2-baseline.uci`;
- the certification rule in `apps/web/src/server/vectra/support.ts` certifies
  only `xiaomi,mi-router-ax3000t`;
- the controller-agent layout-family detector
  (`router/vectra-controller-agent/internal/inventory/collector.go`) special-cases
  AX3000T while logically the test (`/proc/cmdline` for `firmware=`) applies to
  any Filogic stock-layout device.

A real Cudy WR3000H rev2 (`aarch64_cortex-a53`, `mediatek/filogic`,
OpenWrt 24.10.5) attempting bootstrap exposed this: the architecture and
target hard-checks pass, the PassWall2 mirror has matching artefacts, but
naming, routes, and the support whitelist all imply the device is
unsupported — and the failure mode is silent (a broken-DNS error surfacing as a
"storage budget" abort, fixed separately in [#1]).

The hardware family we actually support is **MediaTek Filogic stock-layout
devices on OpenWrt 24.10.x** — AX3000T is one instance. Examples that share the
same packaging contract: Cudy WR3000H rev2, Cudy TR3000, GL.iNet MT3000,
Redmi AX6S and AX3000 Filogic-variant, GL.iNet MT6000 (subject to per-board
overlay budget). The differentiation that matters in software is not the
manufacturer but the layout family, overlay capacity, and presence of
optional modem modules.

## Decision

Treat **MediaTek Filogic stock-layout / OpenWrt 24.10.x** as the supported
hardware family for enrollment. Keep AX3000T as a certified pilot device, but
remove naming, route, and whitelist coupling so any board that satisfies the
architecture + target + layout-family contract can enrol through the same
bootstrap, mirror, baseline, and controller-agent code paths.

### Scope of this decision

In scope:

- rename constants, types, helpers, and tests in
  `apps/web/src/app/enrollment/install-presets.ts` from `Ax3000t*` / `AX3000T_*`
  to `Filogic*` / `FILOGIC_*`;
- introduce new generic routes
  (`/install/filogic-bootstrap.sh`,
  `/install/filogic-myshunt-rebind.sh`,
  `/api/install/filogic-passwall2-baseline.uci`) that serve the same generated
  artefacts; keep the existing AX3000T routes responding with a permanent
  redirect (HTTP 301) for backwards compatibility with QR codes, panel
  bookmarks, and operator scripts already in circulation;
- generalise `support.ts` so certification is computed from
  `(target == "mediatek/filogic")` plus a `layoutFamily` whitelist, not from a
  board-name string equality;
- generalise `detectLayoutFamily` in the controller-agent so the `firmware=`
  cmdline check applies to all Filogic stock-layout boards, not only to
  `xiaomi,mi-router-ax3000t`;
- keep the universal PassWall2 baseline UCI as the single template; per-board
  overrides are explicitly out of scope (see below).

Out of scope for this ADR (separate follow-ups):

- per-board UCI deltas (e.g. board-specific WAN/LAN/VLAN topology) — the
  initial universal baseline must be safe across Filogic stock-layout devices;
- optional modem-module support (4G/5G/MBIM packages) — track per board in
  follow-ups, not in the generator preset;
- adding `vectra-prorouter` artifacts mirror for the OpenWrt base feed
  (`downloads.openwrt.org` zeroconf is still a precondition) — separate
  decision.

## Consequences

- A Cudy WR3000H rev2 (and similar Filogic stock-layout devices) can pass the
  certification gate and run the bootstrap without a board-name change, as
  long as the operator has working WAN/DNS during bootstrap.
- All existing QR codes / install commands keep working through the 301
  redirect on the old `/install/ax3000t-*` paths. New panel UI emits the new
  `/install/filogic-*` paths.
- The pilot/certified router list in `support.ts` becomes a family rule, not
  a literal board-name. Daily KB and Stage Board language move from "AX3000T"
  to "Filogic certified pilot" where appropriate.
- Risk: non-AX3000T Filogic devices may have UCI/network differences the
  universal baseline does not cover. Mitigation: keep a per-board override
  hook (`filogicBoardOverrides`) in `install-presets.ts` that is empty in this
  ADR but documented as the extension point. Operators who hit a divergence
  open a board-specific override PR, not a fork of the generator.
- Out-of-scope items remain explicit follow-ups, not silent regressions.

## Migration plan

1. Code rename + new routes + 301 redirects on the old routes (single PR).
2. `support.ts` family rule + controller-agent layout-family generalisation
   (single PR; covers `support.test.ts`, `presentation.test.ts`,
   `router-control.test.ts` fixture updates).
3. Update enrollment UI copy and panel-facing labels (separate PR).
4. Docs sweep: `README.md`, `ai_docs/develop/features/*`, dashboard notes
   (separate PR).

Each step lands behind the same CI added in [#2] (`pnpm typecheck`,
targeted vitest for `install-presets`, `go test` for the controller-agent).
The full red test suite in `apps/web/src/server/vectra/*.test.ts` must be
green before step 2 lands, otherwise any rename-induced churn becomes
unverifiable.

## References

- `apps/web/src/app/enrollment/install-presets.ts` — current generator
- `apps/web/src/app/install/ax3000t-bootstrap.sh/route.ts`
- `apps/web/src/app/install/ax3000t-myshunt-rebind.sh/route.ts`
- `apps/web/src/app/api/install/ax3000t-passwall2-baseline.uci/route.ts`
- `apps/web/src/server/vectra/support.ts` — certification rule
- `router/vectra-controller-agent/internal/inventory/collector.go` —
  `detectLayoutFamily`
- ADR-0002 — onboarding state machine (already mentions low-overlay Cudy)

[#1]: https://github.com/Waizor1/Vectra-ProRouter/pull/1
[#2]: TBD (this branch)
