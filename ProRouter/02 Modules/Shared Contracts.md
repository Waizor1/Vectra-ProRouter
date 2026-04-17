---
type: module
path: packages/contracts
stage: active
confidence: medium
last-reviewed: 2026-04-17
tags:
  - module
  - contracts
  - zod
---

# Shared Contracts

## Confirmed

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
