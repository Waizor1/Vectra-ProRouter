---
type: module
path: router/luci-app-vectra-controller
stage: active
confidence: high
last-reviewed: 2026-05-11
tags:
  - module
  - luci
  - openwrt
---

# LuCI Controller Package

## Confirmed

- 2026-05-12 LuCI/controller package `0.1.13-r20`: the aligned LuCI package is published and installed with the r20 controller agent across all fresh online routers. There is still no LuCI UI behavior change in this slice; the package version is kept aligned so self-update installs a validated controller/LuCI pair. Stale offline `testrouter` remains queued on r9 and was intentionally not forced.

- 2026-05-12 LuCI/controller package `0.1.13-r18`: package release is bumped alongside the router-agent runtime watchdog fix. There is no LuCI UI behavior change in this slice; the LuCI package version stays aligned so controller self-update installs the matching agent/LuCI pair and the local console reports the same release line. Public r18 artifacts are synced and `1111111111` plus the online active fleet now report `luci-app-vectra-controller=0.1.13-r18` except stale queued `testrouter`.
- 2026-05-11 LuCI/controller package `0.1.13-r17`: package release was bumped with the PassWall watchdog agent release so local console/package metadata stays aligned. There is no LuCI UI behavior change in this slice; the LuCI package is published beside `vectra-controller-agent 0.1.13-r17`, synced into production metadata, and installed on the fresh online active fleet. Vagrand live package proof reports `luci-app-vectra-controller=0.1.13-r17` with controller `0.1.13-r17` and PassWall services running.

- 2026-05-11 LuCI/controller package `0.1.13-r16`: release is published to the signed stable feed and installed across online active routers. `luci-bridge.sh` and `render-config.sh` now avoid `opkg status` in status/config rendering by reading opkg metadata files directly, and `render-config.sh` skips expensive binary-version probes under the 64 MB low-memory floor. Vagrand live package proof reports `version_luci=0.1.13-r16`, `render_uses_opkg=0`, and `luci_uses_opkg=0`.

- Пакет состоит из `htdocs/` и `root/`, без обязательного server-side Lua controller layer.
- Legacy `luasrc/` tree удалён из pilot package path, чтобы LuCI package не проваливался обратно в старый Lua runtime lane.
- `Makefile` переведён на plain `package.mk` semantics; published `.ipk` больше не тянет `luci-lua-runtime`.
- В `htdocs/luci-static/resources/view/vectra-controller/status.js` уже есть реальный JS view для bootstrap/status/rescue UX.
- В `root/usr/libexec/vectra-controller/luci-bridge.sh` bridge теперь читает agent `status.json`, пишет отдельный `luci-status.json` и не затирает runtime status агента.
- LuCI surface показывает install-critical поля pilot bootstrap:
  - `control_url`
  - `panel_url`
  - router ID / approval / import state
  - rescue mode / selected node / pending jobs / applied revision / config digest
- Пакет реально собирается в `.ipk`, опубликован в signed feed и отдается с `api.vectra-pro.net/artifacts/openwrt/...`.
- Bridge по-прежнему работает как отдельный local-console слой и не затирает runtime status агента, читая поля напрямую из `status.json` и UCI.
- По коду подтверждено, что LuCI package зависит от `vectra-controller-agent` и сам по себе не является единственным источником переключения `passwall2`; auto-disable path живёт в агенте, а LuCI bridge даёт только operator action `direct/resume`.
- Stable V1 local console теперь переведён на русский в текущем JS-view пути:
  - русское меню `Контроллер Vectra`
  - русские labels/status/actions
  - русское explanation рядом с raw degraded message `Subscription expired or upstream proxy unavailable`
- В текущем stable-minimal rescue surface локальный alert больше не должен трактовать исторический `last_rescue_reason` как активную деградацию сам по себе: view теперь показывает degraded warning только если `status.rescueMode === 'direct'`.
- В local console добавлено отдельное действие `Отключить аварийный режим`, которое локально возвращает `passwall2.@global[0].enabled=1`, очищает `vectra-controller.main.last_rescue_reason` и перезапускает `passwall2` вместе с `vectra-controller`.
- Status view теперь показывает richer local summary:
  - `control/panel URL`
  - `serviceState` для controller и `passwallServiceState`
  - selected node label с fallback на id/address/protocol
  - import/apply status
  - controller/LuCI package versions
  - last rescue reason / last operator message / local refresh timestamp
- Локальные sanity checks по состоянию на 2026-04-06 выполнены:
  - `node --check` для `status.js`
  - `bash -n` для `luci-bridge.sh`
  - JSON parse для LuCI menu descriptor
- Live package/runtime recovery уже подтверждён на AX3000T:
  - `vectra-controller-agent 0.1.5-r1` и `luci-app-vectra-controller 0.1.5-r1` стоят как user-installed пакеты
  - init service enabled
  - `luci-bridge.sh status` и `luci-status.json` показывают `rescueMode=proxy`, `importState=approved`, `jobsAvailable=0`, пустой `lastRescueReason`
- На живом AX3000T локально подтверждён дополнительный hotfix поверх bridge:
  - `resume` теперь очищает не только UCI-поле `last_rescue_reason`, но и persisted agent `state.json`, так что controller больше не возвращается в `direct` из старого rescue snapshot после рестарта
  - `direct` теперь синхронизирует persisted rescue block в `state.json`, а `status` использует fallback из state/UCI, поэтому LuCI больше не обязана ждать свежий agent check-in, чтобы показать согласованный rescue mode
  - локальный `resume` regression path уже прогнан live: после action `lastRescueReason=''`, `lastRescueAt=''`, `rescue.state.mode='proxy'`, counters reset
- Current live package version is `0.1.10-r1`; it is already published in the production signed feed and installed on the AX3000T together with the updated agent package.
- После повторного реального sysupgrade на том же `24.10.6` LuCI package снова был восстановлен вручную и теперь живёт вместе с agent under the same preserved router identity, а не после новой регистрации устройства.
- Локальный JS-view copy polish добил остаточные технические labels вокруг controller service, degraded state, health-check URL и runtime config action; `node --check` для `status.js` проходит.

## Risks

- На живом роутере ещё не проверены install-time cache invalidation и отображение menu/status после установки.
- Live menu/render path на самом LuCI экране был browser-smoke проверен на текущем deployed `0.1.10-r1`, но локальный copy polish ещё не опубликован в новом LuCI package.
- UI surface теперь существенно богаче pilot baseline, но всё ещё intentionally минимален и не дублирует веб-панель по depth/history.
- Русский текст сейчас закрыт в текущем JS-view path; полноценный LuCI i18n/po lane отдельно не валидировался и может понадобиться, если пакет будет расширяться дальше.
- Surface зависит от текущего shape `status.json` и UCI keys `vectra-controller.main.*`; при изменении agent runtime schema bridge/view надо держать синхронно.
- После выхода live contour на `0.1.10-r1` основной remaining gap для LuCI surface — публикация следующего package build с copy/shunt-aware rescue fixes и повторный local LuCI browser pass.

## Next Review

- После следующей публикации feed установить новый LuCI package на AX3000T и повторить local LuCI browser pass.
- Подтвердить live, что richer status surface корректно читает runtime status агента после apply/update/rescue событий.
- После live validation решить, нужен ли отдельный `po/` i18n lane или текущий stable-minimal JS path достаточен для V1.

## 2026-04-26 LuCI package recovery hardening

- Root cause of the `0.1.13-r4` LuCI disappearance was confirmed as macOS AppleDouble metadata inside the generated `.ipk` payloads (`._*`, `.DS_Store`, `__MACOSX`-class paths). `scripts/build-vectra-openwrt-feed.sh` now disables macOS copyfile sidecars, removes metadata from package staging, hard-fails if metadata remains in `data/` or `control/`, and verifies final `.ipk` inner `control.tar.gz`/`data.tar.gz` before publishing. Clean `0.1.13-r5` artifacts were verified with no metadata matches; canary `testrouter` also proved LuCI menu descriptor, rpcd ACL, `luci-bridge.sh`, and `status.js` present on-device.

## 2026-05-04 controller package r10

- Controller/LuCI package release moved from `0.1.13-r9` to `0.1.13-r10` only for the state self-heal hotfix. The published LuCI package was installed together with the agent on AndreyVK through the normal controller self-update command; post-install checks confirmed `luci-app-vectra-controller 0.1.13-r10`, required LuCI menu/ACL/bridge/status files present via the self-update guard, and `vectra-controller` running after restart.
