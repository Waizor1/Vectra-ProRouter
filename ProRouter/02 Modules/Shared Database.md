---
type: module
path: packages/db
stage: active
confidence: medium
last-reviewed: 2026-04-07
tags:
  - module
  - db
  - drizzle
---

# Shared Database

## Confirmed

- Пакет `@vectra/db` использует `drizzle-orm`.
- Есть директория `drizzle/` и `src/`, то есть слой схем и миграций уже выделен.
- Пакет зависит от `@vectra/contracts`, что указывает на связь между схемами хранения и общими типами.
- Pilot deploy на реальном VPS поднял PostgreSQL и прогнал Drizzle migrations после ручной правки enum type creation в SQL.
- Миграционный дефект был не теоретическим: initial boot падал на отсутствии `vectra_artifact_type`, что подтверждено live logs и исправлено в committed SQL.
- Для `vectra_artifact` и `vectra_firmware_manifest` теперь добавлен воспроизводимый upsert path через bounded admin script, а не только ручные SQL-вставки.
- Добавлен локальный production-like upgrade harness: `apps/web/scripts/verify-db-upgrade-path.mjs` и native wrapper `scripts/Test-VectraDbUpgradePath.py` проверяют clean bootstrap, upgrade поверх seeded old-state схемы, запуск sanitation pass и отсутствие повторного raw snapshot drift.

## Risks

- Harness реализован, но на текущей workstation не выполнен до конца: Docker CLI установлен, однако Docker Desktop Linux engine/daemon не поднят.
- Проверка production-like upgrade path остаётся недоказанной до успешного запуска `scripts/Test-VectraDbUpgradePath.py` при поднятом Docker daemon.

## Next Review

- Прогнать production apply-path для artifact/manifests sync и проверить фактические rows на live VPS.
- Зафиксировать схему сущностей и enum surface в отдельной заметке.
- Поднять Docker daemon и выполнить `python3 ./scripts/Test-VectraDbUpgradePath.py`; затем решить, должен ли harness стать CI/pre-deploy gate.
