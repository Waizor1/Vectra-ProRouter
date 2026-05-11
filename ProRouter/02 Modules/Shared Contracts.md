---
type: module
path: packages/contracts
stage: active
confidence: medium
last-reviewed: 2026-05-08
tags:
  - module
  - contracts
  - zod
---

# Shared Contracts

## Confirmed

- 2026-05-12 router safety event contract: `RouterInventory` now has additive optional `safetyEvents[]` with `type`, `severity`, `component`, `source`, `message`, `observedAt`, and bounded `evidence`. This keeps legacy agents compatible while letting the agent, fleet monitoring, router detail, and Auto-Rescue share OpenWrt-side safety evidence without overloading reachability or generic incident fields.

- 2026-05-08 r12 deployed contract parity: production web now uses `buildVectraSubscriptionSectionName()` in operation preview, so previewed `subscribe_list` section names match the controller `0.1.13-r12` apply renderer and no longer compound `vectra_sub_` prefixes.
- 2026-05-08 subscription section-name parity: `packages/contracts/src/helpers.ts` now exposes `buildVectraSubscriptionSectionName()` and operation preview uses it for `subscribe_list` UCI commands, matching controller apply while collapsing repeated `vectra_sub_` prefixes to a single canonical section name. Regression coverage lives in `apps/web/src/server/vectra/passwall-contracts.test.ts`.
- 2026-05-08 upstream PassWall parameter parity: preview/contracts now pin the latest supported extras in tests, including ShuntRule `protocol=http quic`, node `mkcp_mtu`, node `tls_pinSHA256`, and subscription `domain_resolver` / `domain_resolver_dns_https` / `domain_strategy=UseIPv4`, so the UCI preview continues to match controller apply for newly surfaced PassWall2 fields.
- 2026-05-08 preview/apply parity follow-up: `summarizePasswallRevisionDiff()` now renders `dns_redirect` and uses the same safe UCI section-id normalization as the Go controller for nodes, SOCKS, shunt rules, and imported subscription ids such as `@subscribe_list[0]`. This closes another preview-only blind spot where the panel could show technical commands that did not exactly match apply.
- 2026-05-08 PassWall preview parity: `summarizePasswallRevisionDiff()` preview now includes shunt node bindings and preserved extras for nodes, socks, subscriptions, and global sections, while skipping stale duplicate shunt-slot extras on shunt nodes. This makes the operation preview match the apply lane for server-target changes instead of hiding the command that actually decides `WorldProxy`/`DiscordVoiceUdp`.
- 2026-05-08 ShuntRule extras preview: `packages/contracts/src/helpers.ts` now includes shunt-rule `extras` when rendering operation preview UCI commands, closing the gap where fields imported and applied by the controller (`inbound`, `network`, `port`, etc.) were missing from the panel preview. Targeted `passwall-contracts` coverage pins `DiscordVoiceUdp` extras in the preview.
- 2026-05-03 production hotfix: `passwallNodeProtocolSchema` now accepts `socks` nodes, matching the Go importer/apply path that already preserved PassWall2 Socks nodes. This closes a real router enrollment parser drift where `/api/router/register` rejected a live import with `received 'socks'` before creating the router row; targeted `passwall-contracts` coverage now asserts the contract accepts imported Socks protocol nodes.
- Пакет `@vectra/contracts` экспортирует TypeScript entrypoint из `src/index.ts`.
- Основная внешняя зависимость на текущем срезе — `zod`.
- Пакет задуман как общий контрактный слой между вебом и роутерными компонентами.
- В контрактном слое уже зафиксированы router-facing schemas для:
  - `RouterInventory`
  - `PasswallDesiredConfig`
  - `RescuePolicy`
  - `UpdatePolicy`
  - register/check-in/job-result DTOs
- На срезе 2026-04-06 добавлены typed payload schemas для update lanes:
  - `updateControllerJobPayloadSchema`
  - `updatePasswallPackagesJobPayloadSchema`
  - `validateFirmwareJobPayloadSchema`
- В shared contracts уже отражён stable read/write shape для:
  - richer inventory (`selectedNodeLabel`, `rulesAssets`, `layoutFamily`, `openwrtDescription`)
  - `requestImport` в `routerConfigSyncState`
  - package artifact descriptors для router update jobs

## Questions

- Go agent всё ещё не компилируется напрямую против TypeScript contracts, поэтому cross-language drift остаётся процессным риском и требует live verification/tests.

2026-04-17 addendum:
- `updatePasswallPackagesJobPayloadSchema` is now no longer a weak “package list + maybe artifact” contract. It requires non-empty `packageArtifacts` and additionally carries `targetVersion`, `targetReleaseTag`, `originSource`, `fallbackPolicy`, and `updateScope`, so both managed-stack and scoped component jobs can stay artifact-driven end-to-end.
- `packageArtifactPayloadSchema` now also records `source`, `required`, `downloadSizeBytes`, and `installedSizeBytes`, which lets the router agent reuse the same payload both for deterministic package targeting and for storage-aware path selection on low-overlay devices.
- Result modeling is now explicit too: `passwallPackageUpdateResultPayloadSchema` and `passwallPackageUpdateResultEntrySchema` capture per-package `status`, `pathUsed`, `packageVersionAfter`, `runtimeVersionAfter`, `driftDetected`, and `error`, so the UI can distinguish clean package convergence from runtime-only fallback success instead of flattening everything into one string.
- Contract fixtures were updated with the new PassWall payload/result shape, but cross-language safety still depends on agent-side compile/runtime proof because the Go agent cannot consume the TS schemas directly.

## Next Review

- Добавить contract-level tests именно на update payloads и router job decoding paths.
- Решить, нужен ли более жёсткий discriminated job payload union, а не только набор отдельных schemas.

2026-04-18 update-delivery addendum:
- `updatePasswallPackagesJobPayloadSchema` and `passwallPackageUpdateResult*` now also cover split package-vs-runtime semantics directly: `strategy`, `packageTargetVersion`, `runtimeTargetVersion`, package/runtime before-values, and delivery-block metadata are part of the typed contract rather than only ad hoc payload conventions.
- Shared JSON fixtures now exercise those richer fields too, so both the TS schema corpus and the Go-side fixture decoder see the same PassWall update shape on `2026-04-18`.
- This contract slice is no longer only local: the guarded `2026-04-18` production web rollout included the updated `packages/contracts/src/schemas.ts`, so the live control plane now serves the stronger health probe and richer PassWall update payload/result semantics from the same committed contract set rather than an older deployed snapshot.

2026-04-22 router hostname update addendum:
- `runTerminalCommandJobPayloadSchema` now explicitly reserves `purpose = router-hostname-update` alongside the existing controller self-update and reboot purposes, and it can carry the normalized target `hostname` in the queued payload. This is the contract-level boundary that keeps the new panel rename flow from being just another opaque shell string.
- Because `routerTerminalResultPayloadSchema` remains passthrough-capable, the same lane can now report `hostnameAfter` from the agent without introducing a separate job type or a second result schema. That keeps the web-to-agent change narrow while still giving the control plane a typed-enough signal to persist the new router hostname immediately after job success.

2026-04-22 control-plane recovery contract addendum:
- The shared router check-in schema now carries the new public recovery summary required by the control-plane outage supervisor without breaking legacy agents. `routerInventorySchema` gained optional grouped reachability blocks for `panelReachability`, `ruReachability`, and `foreignReachability`, and router health now also accepts optional `recoveryPhase`, `lastRecoveryAction`, and `awaitingOperator`.
- The new recovery surface is intentionally additive and migration-light. Older routers that do not send these fields still parse cleanly, while newer agents can report explicit phases such as `controller_restart_wait`, `post_reboot_check`, or `operator_attention` and let the web side reuse the existing incident pipeline rather than inventing another transport or enum layer.
- This keeps `Shared Contracts` at `active/medium`: the cross-language boundary is now richer and locally verified, but still depends on coordinated web/agent rollout rather than a shared generated-code toolchain. The next review point for this module is contract parity after the matching controller publish and web rollout.

2026-04-22 control-plane recovery contract safety review addendum:
- A same-day review did not find a schema-level migration blocker in the new recovery payload itself. Legacy routers that omit grouped reachability or the new health fields still parse through the current optional/additive contract surface, so the primary rollout risk is behavioral drift across agent/web logic rather than a hard parser break.
- The remaining contract risk is semantic rather than syntactic: `awaitingOperator`, `recoveryPhase`, and grouped `foreign/ru/panel` statuses now drive incident ownership and destructive-action interpretation on the web side, but the contract layer does not itself prevent stale or sticky state from being reported forever. This means contract compatibility alone is not enough to trust production behavior for the new supervisor without stronger cross-layer integration tests.

2026-04-22 control-plane recovery contract blocker-fix addendum:
- No schema change was required to close the supervisor blockers found in the safety review. The additive check-in/job-result contract introduced earlier remains unchanged, and the blocker fixes stayed entirely inside agent/web state-machine logic.
- This keeps the current contract conclusion stable: backward compatibility for legacy payloads still holds, but production trust for the supervisor still depends on behavior-level regression coverage rather than on another contract migration or enum expansion.

2026-04-23 rollout addendum:
- The current contracts/db slice is now live together with the rebuilt production web app rather than staying only in local source. The deployed panel now serves the current `@vectra/contracts` layer that backs controller `0.1.13-r4` availability, router-control synthetic recovery ownership, and the newer router-side subscription preview / terminal-purpose payloads.
- The only DB-side contract/migration requirement in this release window was the additive enum value for `inspect_subscriptions`, and that boundary is now explicitly confirmed in live PostgreSQL. This means the current panel/backend contract surface is not waiting on an unapplied migration before those newer job payloads can be queued.
- Backward-compatibility reading remains the same after rollout: the contract changes in this slice are additive, so older routers that do not yet speak the new preview/recovery fields still keep parsing cleanly, while the stronger end-to-end proof for those new fields still depends on installing controller `0.1.13-r4` on at least one safe router.

2026-04-29 PassWall semantic diff addendum:
- `packages/contracts/src/helpers.ts` now treats subscription-managed node order and volatile PassWall section ids as semantic no-ops while still preserving real field changes. This prevents subscription refresh/reimport churn from turning equivalent node sets into hundreds of array-index diffs.
- `summarizePasswallRevisionDiff()` now derives changed sections and refresh flags from actual diff paths, not from the mere presence of subscription rows or enabled rule assets in the desired config. Subscription refresh is only previewed for subscription changes, and rule refresh is only previewed for rule asset/source changes.
- Targeted contract tests cover unrelated log edits not triggering subscription/rule refresh and managed node reorder/id churn producing an empty operation preview.
