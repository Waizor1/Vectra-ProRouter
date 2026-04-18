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
- 2026-04-17: `preproxy` в `passwall2` подтверждён как реальная chained-proxy схема, а не отдельный режим обхода. Для обычной ноды `chain_proxy='1'` и `preproxy_node=<id>` заставляют Xray/Sing-box строить цепочку вида `preproxy -> основная нода`; в shunt-ноде отдельный `*_proxy_tag` на каждом правиле включает такой промежуточный хоп только для выбранного правила.
- 2026-04-17: для shunt-правил UI скрывает `Preproxy` у `Direct/Block/default/Socks` и у не-normal target node, а удаление ноды, использованной как `preproxy_node`, автоматически чистит `preproxy_node` и `chain_proxy`, чтобы не оставлять битую ссылку.
- 2026-04-17: node type `Balancing` в PassWall2 относится именно к Xray outbound-balancer, а не к общей “магии выбора лучшей ноды” во всём проекте. Он собирает `balancerTag` с `selector` из списка `balancing_node`, поддерживает `random`, `roundRobin`, `leastPing`, `leastLoad`, optional `fallback_node`, а в batch-режиме автоматически подбирает только normal-ноды без `chain_proxy`.
- 2026-04-17: у `sing-box` отдельного node type `Balancing` нет; вместо него PassWall2 даёт тип `_urltest`. Отдельная страница `HAProxy` с `Enable Load Balancing` — это другой механизм на базе внешнего `haproxy`, запускаемый из `app.sh`, и его не надо смешивать с Xray-balancer node.
