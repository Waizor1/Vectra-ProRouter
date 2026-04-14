---
type: handoff
date: 2026-04-07
tags:
  - handoff
  - stable-v1
  - live-pilot
---

# Stable V1 Handoff 2026-04-07

## Где живет проект

- Workspace: `C:\Users\user\Documents\Vectra-ProRouter`
- Operator UI: `https://router.vectra-pro.net`
- Router REST/API/artifacts: `https://api.vectra-pro.net`
- VPS deploy root: `/opt/vectra-prorouter`
- Первый certified target: `xiaomi,mi-router-ax3000t` stock-layout, OpenWrt `24.10.6`, `aarch64_cortex-a53`

## Где лежат локальные доступы

- Приватный локальный registry хранится в `ProRouter/98 Local/`.
- В обычных заметках пароли не дублируются.
- Для чтения локальных доступов использовать `ProRouter/98 Local/Access Registry.md`.

## Что уже подтверждено live

- Production-like contour работает на одном VPS с двумя хостами: `router.vectra-pro.net` и `api.vectra-pro.net`.
- AX3000T реально проходил flow `install -> register -> import_review -> approve -> apply -> rollback -> approved steady state`.
- Действующий живой router id: `bdfdb919-5e06-4344-ad8b-67a16f3b6fcf`.
- Старый pre-sysupgrade router id: `a1c21287-d35f-4d7c-b8e0-a735b699898b`; он сохранен для истории и должен оставаться `offline`.
- Активная authoritative revision: `a02ee206-3ff6-40db-b23e-c036a48463be`.
- Текущий config digest: `3b17b43b58ed9b11209584487dbd2cb277666fa902fe979680eee9650342d803`.
- После реального `sysupgrade` доказано, что identity выживает за счет сохранения `/etc/vectra-controller` через keep.d.
- Реально подтверждено, что `sysupgrade -l` и backup tar включают `/etc/vectra-controller/state.json`.
- Текущий production feed выдает `vectra-controller-agent 0.1.10-r1` и `luci-app-vectra-controller 0.1.10-r1`.
- На роутере live подтверждены `luci-app-passwall2 26.4.5-r1`, `xray-core 26.3.27-r1`, `geoview 0.2.5-r1`.
- Исправлен ложный degraded alert: LuCI/view больше не должен трактовать исторический `last_rescue_reason` как активный degraded state сам по себе.
- В LuCI добавлено локальное действие `Отключить аварийный режим`.

## Важные operational truths

- У пользователя динамический внешний IP. Это не проблема, потому что роутер работает только через outbound HTTPS polling на `api.vectra-pro.net`.
- Не надо проектировать inbound VPS -> router доступ как обязательную часть stable V1.
- Router-facing API остается REST/JSON, не `tRPC`.
- Stable V1 остается `single-tenant`, `operator-only`.
- Firmware lane остается guarded/manual, не unattended.
- После `sysupgrade` пакеты не восстанавливаются автоматически. Сохраняется identity, но package set надо вернуть отдельно.

## Что осталось закрыть до более уверенного stable

1. Формализовать post-sysupgrade package restore для AX3000T stock-layout, включая надежный fallback `wget -> /tmp/*.ipk -> opkg install /tmp/*.ipk`.
2. Прогнать именно автоматический proxy-failure rescue test, а не только операторские direct/reconnect actions.
3. Доделать browser-level smoke для локальной LuCI страницы на живом AX3000T.
4. Доделать browser-level smoke для operator UI: убедиться, что текущий роутер показывает `approved/proxy`, выбранную ноду и версии без ложного degraded warning.
5. Упростить и зафиксировать release/deploy workflow, потому что VPS deploy root сейчас не является чистым git checkout.
6. Перед следующим package build проверить, что локальные version markers совпадают с реально опубликованным stable feed.

## Ключевые файлы и заметки

- `RTK.md`
- `ProRouter/Home.md`
- `ProRouter/00 Dashboard/Agent Workflow.md`
- `ProRouter/00 Dashboard/Stage Board.md`
- `ProRouter/02 Modules/Router Agent.md`
- `ProRouter/02 Modules/LuCI Controller Package.md`
- `ProRouter/02 Modules/Deployment Stack.md`
- `ai_docs/develop/features/router-xiaomi-ax3000t-live-kb.md`
- `ai_docs/develop/features/vectra-openwrt-feed-publishing.md`

## Guardrails для следующего агента

- Не удалять старый offline router record без явной причины.
- Не писать raw secrets в tracked notes, repo docs или финальные ответы пользователю.
- Для live SSH/SCP использовать pinned host keys.
- Не объявлять rescue logic validated, пока не будет именно live failure-injection сценарий с cooldown/hysteresis/recovery.
- Не считать package-by-name install после sysupgrade надежным, пока не зафиксирован восстановительный workflow; на практике уже подтвержден fallback `wget -> /tmp/*.ipk`.
