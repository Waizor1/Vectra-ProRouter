---
type: prompt
date: 2026-04-07
tags:
  - prompt
  - next-agent
  - stable-v1
---

# Prompt For Next Agent

Ты работаешь в `C:\Users\user\Documents\Vectra-ProRouter` над Vectra Stable V1 для сертифицированного `Xiaomi AX3000T stock-layout` на OpenWrt `24.10.x`.

Сначала прочитай строго в таком порядке:

1. `AGENTS.md`
2. `RTK.md`
3. `ProRouter/Home.md`
4. `ProRouter/00 Dashboard/Agent Workflow.md`
5. `ProRouter/00 Dashboard/Stage Board.md`
6. `ProRouter/00 Dashboard/Repo Map.md`
7. `ProRouter/04 Sessions/Handoffs/2026-04-07-stable-v1-handoff.md`
8. `ProRouter/98 Local/Access Registry.md`
9. `ProRouter/02 Modules/Router Agent.md`
10. `ProRouter/02 Modules/LuCI Controller Package.md`
11. `ProRouter/02 Modules/Deployment Stack.md`
12. `ai_docs/develop/features/router-xiaomi-ax3000t-live-kb.md`
13. `ai_docs/develop/features/openwrt24-console-knowledge-base/08-filogic-recovery-write-safety.md`

Контур уже живой:

- UI: `https://router.vectra-pro.net`
- Router/API/artifacts: `https://api.vectra-pro.net`
- VPS: один сервер, path/domain split уже настроен
- Active router id: `bdfdb919-5e06-4344-ad8b-67a16f3b6fcf`
- Current stable feed/runtime: controller and LuCI `0.1.10-r1`

Что уже доказано live:

- install/register/import_review/approve/apply/rollback/approved
- re-approval after firmware recovery
- sysupgrade identity persistence via keep.d for `/etc/vectra-controller`
- controller self-update lane
- operator direct/reconnect actions
- guarded firmware validation и хотя бы один реальный manual sysupgrade
- false degraded remediation and local LuCI action `Отключить аварийный режим`

Главные правила:

- Общайся с пользователем по-русски.
- Пользователь хочет прямое выполнение, а не длинные теории.
- У пользователя динамический внешний IP, поэтому не делай допущений про inbound-доступ к роутеру.
- Не печатай raw secrets в ответы и не клади их в tracked notes.
- Для доступов используй локальный private registry из `ProRouter/98 Local/`.
- Для live SSH/SCP используй pinned host keys.
- Не трогай unrelated dirty changes и ничего не сноси через reset/clean.

Текущие приоритеты:

1. Закрыть post-sysupgrade package restore runbook/helper для AX3000T stock-layout.
2. Провести именно автоматический proxy-failure rescue test с живым failure injection, доказав различение `server outage` vs `proxy outage`, cooldown и recovery без flapping.
3. Прогнать browser-level smoke для локальной LuCI страницы и operator UI на текущем live state.
4. Проверить release workflow, чтобы следующий stable feed build/deploy был предсказуемым.

Работай аккуратно: если делаешь live writes или firmware-related действия, опирайся на Filogic write-safety runbook и фиксируй всё в `ProRouter/`.
