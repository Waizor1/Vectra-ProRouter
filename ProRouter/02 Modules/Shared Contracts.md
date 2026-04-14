---
type: module
path: packages/contracts
stage: active
confidence: medium
last-reviewed: 2026-04-06
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

## Next Review

- Добавить contract-level tests именно на update payloads и router job decoding paths.
- Решить, нужен ли более жёсткий discriminated job payload union, а не только набор отдельных schemas.
