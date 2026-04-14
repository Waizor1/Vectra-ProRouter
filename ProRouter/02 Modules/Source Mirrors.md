---
type: module
path: passwall2/, openwrt-24.10-src/, procd-src/
stage: reference
confidence: high
last-reviewed: 2026-04-05
tags:
  - module
  - mirrors
  - reference
---

# Source Mirrors

## Confirmed

- `passwall2/`, `openwrt-24.10-src/` и `procd-src/` рассматриваются как локальные source mirrors.
- Корневой Git history намеренно не хранит их историю как часть основной KB.
- По текущим правилам эти директории используются прежде всего для анализа и проверки поведения, а не для спонтанных изменений.

## Guardrails

- Считать эти зеркала read-only, если пользователь явно не попросил иначе.
- При конфликте документации с кодом доверять коду в зеркалах.

## Recent Findings

- 2026-04-06: при разборе исходников `passwall2` подтверждено, что явная запись `@global[0].enabled=0` встречается в `luasrc/controller/passwall2.lua` только в обработчике `clear_all_nodes()`.
- 2026-04-06: удаление выбранных нод и truncate подписок очищают ссылки на ноды (`@global[0].node` и связанные поля), но не выключают `Main switch` напрямую; из-за этого PassWall2 может остаться без активной ноды и выглядеть "погасшим" без записи `enabled=0`.
- 2026-04-06: если `/etc/config/passwall2` отсутствует или сброшен, он пересоздаётся из `0_default_config`, где `option enabled '0'` задан по умолчанию.
