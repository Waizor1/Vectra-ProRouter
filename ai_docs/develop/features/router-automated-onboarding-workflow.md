# Automated Router Onboarding Workflow

Date: 2026-05-14  
Status: backend MVP in progress; auto-run disabled by default  
Scope: Vectra-ProRouter production panel + router controller

## Goal

New customer routers should converge from "first check-in" to "ready for normal
PassWall work" without a Codex/manual terminal session. The panel owns the
workflow, stores the operator intent, queues safe router jobs, observes job
results/check-ins, and stops automatically when a safety gate says the router
needs human attention.

This workflow is based on repeated live onboardings across AX3000T, Netis NX31,
Cudy WR3000E/WR3000H, and low-storage controller-only installs.

## Non-goals

- No unattended sysupgrade.
- No unattended reboot by default.
- No blind package installation on unsupported boards.
- No raw subscription URLs, UUID secrets, or full VLESS node details in logs,
  event messages, UI task cards, or final status summaries.
- No Codex/agent dependency in the happy path. Codex can diagnose failed runs,
  but it must not be the orchestrator.

## Design principles

1. **Panel-owned state machine.** Onboarding state lives in the web/backend
   database and advances on `register`, `check-in`, `job-result`, and a small
   recovery poller.
2. **Typed jobs first.** Use `apply_passwall_config`, `refresh_subscriptions`,
   controller update, and future typed jobs for runtime/verification. Keep
   `run_terminal_command` as an explicit fallback/debug lane, not the steady
   state.
3. **Semantic route intent, not node ids.** Subscription refresh can rotate UCI
   section ids. The desired fleet baseline is described as route intent
   (`RU-entry Germany`, `RU Russia`, `Netherlands`, `Belarus`,
   `RU-entry Poland + Discord UDP tuning`) and only then resolved to current
   live node ids.
4. **Live proof before completion.** A run is not done until the router reports
   services/resources and route smoke, then the panel accepts the final live
   import as the active baseline.
5. **Small reversible steps.** Every stage is idempotent and resumable. If the
   router goes offline, the run waits instead of stacking unrelated jobs.

## Current primitives to reuse

| Existing primitive | Use in onboarding |
| --- | --- |
| `routers.importState=awaiting_import/import_review/approved/out_of_sync` | Source-of-truth import/readiness stage. |
| `requestReimport` | Ask the controller to send a fresh live PassWall import on next check-in. |
| `approveImport` | Promote the current live import to authoritative baseline when the run owns the change. |
| `createOperatorDraftRevisionWithDb` + `queueDesiredRevisionApplyJobWithDb` | Create/apply subscription and baseline revisions. |
| `refresh_subscriptions` job | Let the router run native PassWall subscription refresh. |
| `NormalizeFleetRoutePolicyConfig` / `fleet.normalizeRoutePolicy` | Build the canonical non-`hh` baseline from current live nodes. |
| controller `ReconcileFleetRoutePolicy` | Repair route intent after native subscription refresh rotates node ids. |
| resource/job safety guard | Block heavy work under low RAM/tmp/overlay or known safety events. |

## New backend surfaces

### 1. Onboarding profile

Create a panel-managed profile that can be attached before or after first
check-in.

```ts
type RouterOnboardingProfile = {
  routerId: string;
  enabled: boolean;
  targetHostname?: string;
  displayName?: string;
  subscriptionSecretRef?: string;
  baseline: "standard-non-hh" | "hh-exempt" | "subscription-only";
  runtimePolicy: "auto-minimal-passwall-xray" | "controller-only";
  verifyPolicy: "route-smoke" | "services-only";
  notes?: string;
};
```

Subscription URLs are stored via the existing secret-blob pattern or a new
profile-scoped secret blob. UI/API responses show only presence, provider label,
and checksum/preview.

### 2. Onboarding run

Add a durable run row or equivalent event-sourced state.

```ts
type RouterOnboardingRun = {
  id: string;
  routerId: string;
  profileId: string;
  state: RouterOnboardingState;
  status: "running" | "waiting" | "blocked" | "failed" | "done";
  attempt: number;
  lastJobId?: string;
  activeRevisionId?: string;
  lastError?: string;
  nextRunAfter?: Date;
  createdAt: Date;
  updatedAt: Date;
  completedAt?: Date;
};
```

Only one active run per router is allowed. All queued jobs use dedupe keys with
the run id and state, for example:

```text
onboarding:<runId>:refresh_subscriptions
onboarding:<runId>:normalize_route_policy:<revisionId>
onboarding:<runId>:verify_route_smoke
```

## State machine

| State | Entry condition | Action | Success transition | Block/fail transition |
| --- | --- | --- | --- | --- |
| `created` | Profile enabled and router exists. | Load latest router row/snapshot/jobs/incidents. | `preflight` | `blocked_unsupported`, `blocked_conflict` |
| `preflight` | Router has recent or first check-in. | Check board support, direct/rescue state, incidents, active jobs, controller version, RAM/tmp/overlay, installed packages. | `request_initial_import` or `approve_initial_import` | `blocked_low_resources`, `blocked_incident`, `waiting_offline` |
| `request_initial_import` | `awaiting_import` or no live revision. | Set `importState=awaiting_import`; wait for controller check-in import payload. | `approve_initial_import` | `waiting_offline` |
| `approve_initial_import` | Pending import exists and no active approved baseline, or run requested the re-import. | Approve live import as starting baseline. | `rename_router` | `blocked_import_conflict` |
| `rename_router` | Profile has `targetHostname` different from live hostname. | Queue typed hostname job; current MVP can reuse the existing terminal hostname payload. | `ensure_runtime` | `waiting_job`, `failed_hostname` |
| `ensure_runtime` | Snapshot/package state known. | Ensure controller is current enough; for low-storage installs, use minimal PassWall/Xray repair instead of full stack. | `apply_subscription` | `blocked_low_overlay`, `failed_runtime` |
| `apply_subscription` | Profile has subscription secret. | Create desired revision from latest live config with subscription item only; queue apply with `refreshSubscriptions=true`, `refreshRules=false`, restart required. | `refresh_subscription` | `waiting_job`, `failed_apply_subscription` |
| `refresh_subscription` | Subscription revision applied or native refresh requested. | Queue/observe `refresh_subscriptions`; request live re-import after success. | `resolve_route_baseline` | `waiting_job`, `failed_subscription_refresh` |
| `resolve_route_baseline` | Latest live import contains subscription nodes. | Resolve baseline by semantic route intent and live node health. `hh` exits as exempt. | `apply_route_baseline` or `verify_runtime` | `blocked_no_valid_nodes` |
| `apply_route_baseline` | Baseline differs from live config. | Create/apply desired revision with route bindings, DNS contour, and Discord UDP tuning. | `verify_runtime` | `waiting_job`, `failed_apply_baseline` |
| `verify_runtime` | Apply/refresh finished and router has checked in. | Verify services, resources, packages, bindings, and route smoke. | `final_reimport` | `repair_runtime` or `blocked_smoke_failed` |
| `repair_runtime` | Failure matches known safe repair class. | Queue typed repair, for example compact geodata + `dnsmasq-full` when Xray fails on missing geodata or dnsmasq lacks nftset. | `verify_runtime` | `blocked_runtime_unknown`, `blocked_low_overlay` |
| `final_reimport` | Runtime proof is green. | Request re-import and approve the run-owned live revision. | `done` | `waiting_offline`, `blocked_import_conflict` |
| `done` | Approved live import, queue empty, smoke green. | Write event log summary. | terminal | terminal |

## Safety gates

The orchestrator must stop or wait instead of forcing progress when any gate is
red.

| Gate | Auto action |
| --- | --- |
| Router offline/stale | `waiting_offline`; do not add more jobs. |
| Unsupported board/layout | `blocked_unsupported`; no destructive actions. |
| Direct/rescue mode or open critical incident | `blocked_incident`; do not restart PassWall. |
| Existing non-onboarding active jobs | `waiting_queue`; wait for drain. |
| RAM/tmp/overlay below current job safety floors | `blocked_low_resources`; allow only read-only/import checks. |
| Overlay cannot safely fit projected runtime repair | `blocked_low_overlay`; keep controller-only state. |
| Pending import differs from the run-owned operation | `blocked_import_conflict`; require operator review. |
| No subscription secret/profile | `blocked_missing_profile`; router can stay approved but unconfigured. |
| Route baseline cannot resolve enough live nodes | `blocked_no_valid_nodes`; keep PassWall in last known safe state. |
| Smoke returns `000` on a required route after fallback attempts | `blocked_smoke_failed`; do not mark ready. |

## Baseline route intent

Default non-`hh` profile:

| Slot | Intent | Required extras |
| --- | --- | --- |
| `WorldProxy` | RU-entry Germany YouTube-capable node | none |
| `YouTube` | RU Russia YouTube-capable node | none |
| `Special` | Netherlands; prefer live-good plain NL, fallback to RU-entry NL if plain NL returns `000` | none |
| `Tiktok` | Belarus | none |
| `DiscordVoiceUdp` | RU-entry Poland YouTube-capable node | rule `network=udp`, `port=19294-19344,50000-50100`; node `mux=1`, `mux_concurrency=-1`, `xudp_concurrency=16` |

The selected shunt node is authoritative for LuCI-visible targets:

```text
passwall2.<selectedShunt>.WorldProxy
passwall2.<selectedShunt>.YouTube
passwall2.<selectedShunt>.Special
passwall2.<selectedShunt>.Tiktok
passwall2.<selectedShunt>.DiscordVoiceUdp
```

The workflow must write both `basicSettings.shuntRules[].outboundNodeId` and
the selected shunt-node extras so panel preview, controller apply, LuCI, and
router import converge.

## Runtime repair profiles

### Minimal PassWall/Xray contour

Use this on low-overlay Cudy/WR class routers where the full PassWall managed
stack is unsafe.

Allowed:

- `luci-app-passwall2`
- `xray-core`
- `tcping`
- compact geodata files when the config contains `geosite:`/`geoip:` rules
- `dnsmasq-full` when generated PassWall dnsmasq config uses `nftset=...`

Avoid by default unless explicitly needed and storage projection is green:

- `sing-box`
- `geoview`
- full `v2ray-geoip`
- full `v2ray-geosite`
- `chinadns-ng`

Known repair signatures:

| Evidence | Safe repair |
| --- | --- |
| Xray stderr contains `failed to open geosite.dat` or `failed to open geoip.dat` | Install/download compact configured geodata assets into `/usr/share/v2ray/`, then restart PassWall. |
| dnsmasq log contains `recompile with HAVE_NFTSET defined to enable nftset directives` | Replace base `dnsmasq` with `dnsmasq-full` if overlay projection remains safe. |
| PassWall running but `xray` process missing | Inspect generated config/log first; apply only a known repair signature. |

## Completion criteria

The run may mark `done` only when all are true:

- Router `status=active`.
- `importState=approved`.
- `pendingImportRevisionId=null`.
- `activeRevisionId` is the final live import/digest accepted by the run.
- Queue has no active onboarding jobs and no unrelated active jobs.
- No open critical incidents.
- Latest snapshot reports:
  - `passwall=running`
  - `passwallServer=running`
  - `dnsmasq=running`
  - `controller=running`
- `xray` runtime is present when PassWall profile requires Xray.
- `podkop` and `sing-box` are absent for the standard profile.
- Overlay/RAM/tmp are above safety thresholds.
- Route smoke returns `204` for all configured standard slots.
- Panel/editor state has no pending route-policy drift for non-`hh` routers.

## Operator UI

Fleet/router detail should show:

- profile attached/not attached;
- current run state;
- current blocking gate, if any;
- last job and last successful proof;
- safe retry button for blocked transient states;
- "take over manually" button that pauses auto-onboarding and leaves the router
  in its current safe state.

Normal operator copy should avoid raw `import/re-import/trust` language on the
happy path. Use:

- `Автонастройка роутера`
- `Ждём первое чтение`
- `Применяем подписку`
- `Подбираем серверы`
- `Проверяем маршруты`
- `Готов к работе`

## MVP implementation slice

Implemented locally in the first backend slice:

- `packages/db/src/schema.ts` plus migration
  `packages/db/drizzle/0009_panel_owned_onboarding.sql` add durable
  onboarding profiles and runs.
- `apps/web/src/server/vectra/router-auto-onboarding.ts` owns the
  feature-flagged state-machine advance path.
- Router `register`, `check-in`, and `job-result` routes call the orchestrator
  only when `VECTRA_AUTO_ONBOARDING_ENABLED=true`.
- The protected `onboarding` tRPC router can save/pause/retry profiles and
  returns subscription presence/hash metadata without raw URLs.
- Current automated stages cover initial import approval, hostname job queueing,
  subscription apply/refresh, semantic fleet route baseline apply,
  typed route-smoke verification, final re-import approval, and safety gates for
  offline routers, unsupported boards, active jobs, open incidents, and low
  resources.
- `verify_passwall_routes` is now a typed controller job. For
  `verifyPolicy="route-smoke"` and `baseline="standard-non-hh"` the panel queues
  it in `verify_runtime` and moves to `final_reimport` only after the job result
  is `success`, `ok=true`, all five managed slots have binding/rule/node extras
  green, and each bound route returns `204` from
  `/usr/share/passwall2/test.sh url_test_node <node>`.
- Router detail now has a local operator card for preparing the panel-owned
  onboarding profile, saving the subscription secret, manually advancing or
  retrying a run, pausing for manual takeover, and viewing recent run blockers.
  The card shows subscription presence/hash only, never the raw URL.
- `ensure_passwall_runtime` is now a typed controller job for the known
  low-storage/YuranRod repair class. The panel queues only compact geodata and
  `dnsmasq-full` actions after confirming the core PassWall/Xray runtime already
  exists; the controller treats the job as storage-gated, downloads the
  `dnsmasq-full` package before removing base `dnsmasq`, preserves
  `/etc/config/dhcp`, restarts PassWall, and returns post-repair inventory. The
  web state machine advances only when the job result is `success`, `ok=true`,
  all action statuses are green, services are running, and RAM/tmp/overlay are
  still above the storage repair floors.
- `advanceRouterOnboardingWithDb` now serializes same-process per-router
  advancement so simultaneous register/check-in/job-result callbacks do not
  stack duplicate revisions/jobs in the normal single-web-container deployment.

Still open after the local MVP:

- supervised pilot enablement and production deploy.

1. **Backend state and profile**
   - Add onboarding profile/run persistence.
   - Add protected API to create/update/pause/retry profile.
   - Add event log entries for every state transition.

2. **Read/write orchestrator**
   - Add `advanceRouterOnboarding(routerId)` in
     `apps/web/src/server/vectra/router-auto-onboarding.ts`.
   - Call it after router register, check-in processing, job-result processing,
     and from a small cron/poller.
   - Keep feature flag default off:
     `VECTRA_AUTO_ONBOARDING_ENABLED=false`.

3. **Subscription + route baseline**
   - Use latest live import as source.
   - Inject subscription from secret.
   - Queue apply/refresh/re-import.
   - Run semantic fleet policy normalization with live-health fallback.

4. **Verification**
   - Done locally: typed `verify_passwall_routes` controller job returns
     selected shunt bindings, required extras, and `url_test_node` results.
   - Keep terminal smoke as a debug fallback only.

5. **Runtime repair**
   - Done locally: typed `ensure_passwall_runtime` covers compact geodata and
     `dnsmasq-full` repair without blind full-stack installs.
   - Keep storage/resource gates strict and require post-repair service/resource
     proof before subscription apply.

6. **UI**
   - Done locally for router detail: add an onboarding card to save/update a
     profile, advance/retry/pause the run, and show a compact timeline/blocker.
   - Fleet-row shortcuts can come later after the supervised pilot.

## Rollout plan

1. Local tests for state machine and route intent resolution.
2. Dry-run mode on existing approved routers: no jobs, only predicted actions.
3. Enable profile creation for one new pilot router; auto-run still off.
4. Enable auto-run for one supervised pilot router.
5. Enable for pilot/certified non-`hh` routers.
6. Keep `hh` exempt until a separate profile is defined and live-proven.

## Test matrix

| Scenario | Expected result |
| --- | --- |
| Fresh router, first import already present | Auto-approve, then continue. |
| Fresh router offline after registration | Run waits, no stacked jobs. |
| Router has unrelated queued apply/update | Run waits for queue drain. |
| Subscription imports valid nodes | Baseline resolves and applies. |
| Plain NL node returns `000` | Fallback to live-good RU-entry NL. |
| Missing `geosite.dat` prevents Xray start | Known repair installs compact geodata, then verifies. |
| Base `dnsmasq` cannot parse `nftset` | Known repair swaps to `dnsmasq-full`, then verifies. |
| Overlay below repair floor | Block with controller-only/manual-required state. |
| `hh` router | Exempt or profile-specific workflow only. |
| Native PassWall refresh rotates node ids | Controller/panel reconcile route intent by semantics. |

## Open decisions before production enablement

1. Whether to add a cross-process PostgreSQL advisory lock before running more
   than one web container. The local MVP has an in-process per-router lock,
   which is enough for the current single-container pilot lane but not a
   horizontal-scale guarantee.
2. Whether final route smoke should be required for all profiles or optional for
   subscription-only profiles.
3. Exact pilot rollout criteria for enabling `VECTRA_AUTO_ONBOARDING_ENABLED`
   on production after one supervised router run.
