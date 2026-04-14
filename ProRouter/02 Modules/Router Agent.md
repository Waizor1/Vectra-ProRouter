---
type: module
path: router/vectra-controller-agent
stage: pilot
confidence: high
last-reviewed: 2026-04-14
tags:
  - module
  - go
  - openwrt
---

# Router Agent

## Confirmed

- Это отдельный Go-модуль `vectra-controller-agent` c `go 1.22.0`.
- В README зафиксированы зоны ответственности:
  - `cmd/vectra-controller-agent`
  - `internal/controlplane`
  - `internal/rescue`
  - `internal/passwall`
  - `openwrt/`
- OpenWrt runtime-файлы (`/etc/config`, `/etc/init.d`, `uci-defaults`, `render-config.sh`) лежат отдельно под `openwrt/files`.
- На pilot VPS агент реально cross-build'ится в статический `linux/arm64` бинарь через `GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=linux GOARCH=arm64`.
- Из этого бинаря и runtime-файлов теперь реально собирается `.ipk` для `aarch64_cortex-a53`, опубликованный в signed feed на `api.vectra-pro.net`.
- Bootstrap-конфиг теперь явно разделяет:
  - `control_url=https://api.vectra-pro.net` для router-facing REST
  - `panel_url=https://router.vectra-pro.net` для operator/UI ссылки
- Agent runtime сохраняет оба URL в status/config surface и остаётся совместимым с legacy-конфигом, где задан только `panel_url`.
- По коду подтверждено, что именно `vectra-controller-agent` содержит rescue/direct-mode path, который может записывать `set passwall2.@global[0].enabled='0'`, коммитить `passwall2` и делать `/etc/init.d/passwall2 restart`.
- Published agent package install path подтверждён по содержимому `.ipk`: `conffiles` включает `/etc/config/vectra-controller`, а `postinst` запускает `uci-defaults`, `enable` и `restart` сервиса.
- На живом `Xiaomi AX3000T stock-layout` пакет реально установлен и пережил controlled `opkg --force-reinstall` с published `.ipk` напрямую с `api.vectra-pro.net`.
- Live SHA-check подтвердил, что на роутере раньше действительно работал старый бинарь, а не только старый лог; после forced reinstall `/usr/sbin/vectra-controller-agent` совпал по SHA с текущим артефактом из production feed.
- Исправление multiline UCI parser path реально подтвердилось на живом роутере: после установки нового binary router перешёл из `awaiting_import` в `import_review`, а baseline import больше не падал на `invalid uci option line: domain:ozon.ru`.
- Live pilot chain `install -> register -> check-in -> import_review -> approve -> approved` уже реально пройдена против `router.vectra-pro.net` / `api.vectra-pro.net`.
- Rescue state теперь сохраняется в persisted state, а не живёт только в памяти процесса.
- Local rescue loop теперь использует реальные probes:
  - server reachability через `GET /api/health` на control plane
  - proxy-path probe через `/usr/share/passwall2/test.sh url_test_node <selected-node>`
  - temporary direct-path verification перед local fallback, если policy требует подтверждённый direct path
- Agent runtime теперь умеет различать server outage и proxy outage на уровне входных сигналов rescue state machine; недоступность сервера сама по себе больше не является основанием выключать PassWall2.
- Inventory collector расширен до human-usable live surface:
  - `selectedNodeLabel`
  - `layoutFamily`
  - `openwrtDescription`
  - `rulesAssets`
  - resource snapshot по RAM/swap/overlay/tmp
  - explicit service health по `vectra-controller`, `passwall2`, `passwall2_server`, `dnsmasq`
- Apply receipts нормализованы сильнее:
  - exact `uciCommands`
  - `operationResults`
  - `commandResults`
  - `postApplyImportDigest`
- Artifact-driven lanes теперь реально исполняются на agent side:
  - controller/passwall package jobs умеют скачать artifact/feed metadata, stage `.ipk` в temp dir, проверить SHA-256 и использовать `Packages`/`Packages.sig` при наличии usign verification path
  - firmware validation job больше не привязан только к legacy `imagePath`; поддержан `artifactUrl -> stage -> verify -> validationCommand/sysupgrade -T` path с сохранением legacy fallback
- Текущий workspace теперь кодирует attended PassWall2 post-sysupgrade recovery как штатное поведение `update_passwall_packages`:
  - дефолтный package set расширен до `luci-app-passwall2`, `xray-core`, `sing-box`, `hysteria`, `geoview`, `v2ray-geoip`, `v2ray-geosite`, `dnsmasq-full`, `chinadns-ng`, `kmod-nft-socket`, `kmod-nft-tproxy`, `kmod-nft-nat`
  - после feed-based и staged install agent теперь штатно вызывает `lua /usr/share/passwall2/rule_update.lua log geoip,geosite`, поэтому актуальные `geoip_url`/`geosite_url` из UCI и web panel остаются source of truth, а не хардкод в контроллере
  - затем agent делает discrete runtime recovery command `/etc/init.d/passwall2 running >/dev/null 2>&1 && /etc/init.d/passwall2 restart || /etc/init.d/passwall2 start`, без SSH stdin-tail assumptions
  - inventory/config surface теперь дополнительно показывает `dnsmasq`, `dnsmasq-full`, `chinadns-ng`, `v2ray-geoip`, `v2ray-geosite` и nft runtime-kmods, что упрощает post-sysupgrade диагностику
- Controller package `0.1.11-r1` with this recovery logic is now published in the production signed feed and mirrored into production PostgreSQL metadata; live AX3000T runtime itself still remains on `0.1.10-r1` until the next queued controller/package install cycle.
- Локальный `go test ./...` по модулю по состоянию на 2026-04-06 проходит успешно.
- По коду подтверждено, что agent уже умеет import/apply typed PassWall2 state, rescue-aware polling, staged package update jobs и firmware validation по artifact contract с legacy compatibility.
- Live recovery after controller loss уже подтверждён на AX3000T: при локальном доступе по LAN агент оказался установлен как `0.1.5-r1`, был поднят через init script, resumed outbound polling to `api.vectra-pro.net`, успешно получил `update_controller` и `reconnect`, очистил local rescue reason и перевёл router back into `proxy` + `approved`.
- На живом роутере локальный runtime теперь подтверждает post-recovery steady state:
  - `service_state=running`
  - `rescueMode=proxy`
  - `passwallEnabled=1`
  - `importState=approved`
  - `jobsAvailable=0`
  - `lastRescueReason=''`
- В текущем локальном workspace добавлен ещё один agent-side hardening path:
  - `internal/config/config.go` теперь корректно merge-ит partial `rescue_policy` и не затирает defaults нулевыми значениями JSON
  - `render-config.sh` реально рендерит rescue policy в runtime `config.json`
  - `internal/controlplane/client.go` теперь включает server response body в non-2xx error, что уже помогло на живом AX3000T диагностировать production `400`
- Первый live authoritative apply/rollback cycle теперь реально подтверждён на AX3000T:
  - draft `#20` применился на роутере и сменил `uci get passwall2.@global[0].loglevel` с `error` на `warning`
  - draft `#22` тем же путём вернул `loglevel` обратно в `error`
  - в обоих случаях роутер сохранил `enabled=1`, `node=myshunt`, `rescueMode=proxy`, `publicReachable=true`
- В apply receipts на обоих live jobs остаётся повторяющийся stderr-шум от `lua /usr/share/passwall2/subscribe.lua start all` (`Broken pipe` / `head: standard output: I/O error`), хотя итоговый job status и router health остаются успешными. Это уже не blocker для apply lane, но это реальный noise/risk, который стоит сузить в runtime path.
- Server-side production bug, который после apply возвращал router в `out_of_sync`, закрыт парным web fix: post-apply import теперь auto-promote-ится обратно в `approved`, если check-in пришёл с известным `appliedRevisionId`. Повторный rollback apply уже подтвердил этот steady-state на живом роутере.
- Live sysupgrade identity-loss root cause был закрыт и live-validated в controller package `0.1.9-r1`:
  - в agent package добавлен `/lib/upgrade/keep.d/vectra-controller`
  - keep rule сохраняет весь `/etc/vectra-controller`, а не только один файл
  - на живом AX3000T `sysupgrade -l` теперь показывает `/etc/vectra-controller/state.json`
  - backup-only proof через `sysupgrade -b /tmp/...tar.gz` подтвердил, что `etc/vectra-controller/state.json` реально попадает в архив, а не только в list surface
- Full live sysupgrade proof теперь пройден повторно на том же AX3000T уже после keep.d fix:
  - перед flash backup tar `C:\Users\user\Downloads\vectra-pre-sysupgrade-20260407b.tar.gz` включал `etc/vectra-controller/state.json`, `etc/opkg/customfeeds.conf` и opkg keys
  - после реального `sysupgrade` на тот же OpenWrt `24.10.6` файл `/etc/vectra-controller/state.json` сохранил тот же `router_id = bdfdb919-5e06-4344-ad8b-67a16f3b6fcf`
  - controller packages, LuCI package и PassWall2 app packages ожидаемо не пережили flash как установленные пакеты, но после reinstall агент не зарегистрировал новый router id
- Live router после post-firmware reinstall и последующего controller refresh снова приведён в steady state:
  - current live runtime `vectra-controller-agent = 0.1.10-r1`
  - current live runtime `luci-app-vectra-controller = 0.1.10-r1`
  - `luci-app-passwall2 = 26.4.5-r1`
  - `xray-core = 26.3.27-r1`
  - `geoview = 0.2.5-r1`
  - current physical router id `bdfdb919-5e06-4344-ad8b-67a16f3b6fcf` снова переведён из `import_review` в `active + approved`
  - active authoritative revision для него: `a02ee206-3ff6-40db-b23e-c036a48463be`
  - live check-in после approval уже подтвердил `import_state=approved`, пустой `last_rescue_reason` и прежний config digest
  - старый потерянный router id `a1c21287-d35f-4d7c-b8e0-a735b699898b` сохранён для истории, но переведён в `offline`
- Локальный follow-up по PassWall `Rule Manage` parity теперь также закрывает importer-side hole для shunt match extras: `internal/passwall/import.go` больше не отбрасывает `network` у `shunt_rules`, тесты дополнительно фиксируют round-trip `protocol`, `inbound`, `network`, `source`, `port`, `invert`, и `go test ./...` по модулю снова зелёный.
- 2026-04-08 local follow-up now also adds a new read-only diagnostics job type `collect_router_logs`: agent-side `executeJobs()` can request bounded `logread` snapshots for `all`, `controller`, `passwall`, `dnsmasq`, or raw `system`, normalize line limits, truncate oversized outputs, and report structured snapshot payloads back through the existing `job-result` channel. The shared contract fixtures and local `go test ./...` are green with this new job type.
- 2026-04-08 live production validation then closed a latent controller self-update bug on the AX3000T: controller and LuCI packages were already at `0.1.12-r1`, but the running process still pointed at `/usr/sbin/vectra-controller-agent (deleted)`, so the first `collect_router_logs` attempt failed with the old binary behavior. After a manual service restart restored the current executable, release `0.1.12-r2` added a restart-or-start `postinst`, was published to the stable feed, installed on the router, and proved that `opkg` upgrade now hands off directly to a live non-deleted binary without a manual restart.
- Live AX3000T proof now also exists for the diagnostics lane itself: `collect_router_logs` succeeded from the operator panel for both `all/200` and `system/50`, reported structured snapshot payloads through `job-result`, and those snapshots rendered in the production `Watch Logs` tab.
- 2026-04-09 live triage on router `AndreyVK` (`netis,nx31`, router id `a0c9c42e-1934-40ce-8e02-d1cce12f278e`) shows a new controller self-update regression outside the AX3000T proof path: latest snapshots still report `controllerVersion = unknown`, `packageVersions["vectra-controller-agent"]` is empty, and every recent `update_controller` job to `0.1.12-r2` failed. The decisive live evidence is the terminal `opkg` stdout: installation reaches `Configuring vectra-controller-agent.` and then dies with `signal: killed`. In local source this matches a restart race: `update_controller` already persists a pending success result and schedules a delayed restart itself, but package `0.1.12-r2` also restarts the service from `postinst`, so the running agent can be killed mid-`opkg install` before it finishes the self-update flow. The local fix now suppresses `postinst` restart only for agent-driven self-update installs via `VECTRA_SKIP_POSTINST_RESTART=1`, while keeping manual `opkg install` restart behavior intact; `go test ./...` is green, but this fix is not yet published to the feed or deployed to live routers.
- A same-night follow-up now also closes that live `AndreyVK` regression for real in published release `0.1.12-r4`: the agent self-update lane no longer relies only on an environment variable inside `postinst`, but also drops a temporary `/tmp/vectra-skip-postinst-restart` sentinel before `opkg install`, clears stale sentinels on startup, and teaches the controller init script to ignore package-driven `start/reload` when `VECTRA_SKIP_POSTINST_RESTART=1` is set. The same release also fixes `Watch Logs -> Controller` on the agent side by replacing the broken literal `logread -e 'vectra-controller|vectra'` filter with a real `grep -E` pipeline. Local `go test ./...` stayed green, controller/LuCI `0.1.12-r4` were built and published as direct artifacts on production, production metadata was synced to PostgreSQL, a live `update_controller` job on `AndreyVK` finished `success`, and subsequent snapshots now stably report `controllerVersion = 0.1.12-r4` with both controller packages present. A post-fix live `collect_router_logs` check on `source=controller` now also returns the previously hidden failure lines instead of an empty snapshot.
- A later same-day read-only check on the live AX3000T `1111111111` (`bdfdb919-5e06-4344-ad8b-67a16f3b6fcf`) surfaced a different post-update edge case on top of that fix: production `update_controller` to `0.1.12-r4` still ended with `signal: killed`, but the router did not stay on the old package state. Instead, the current live binary and package payload files on the router already match the new self-update generation (`VECTRA_SKIP_POSTINST_RESTART` and `/tmp/vectra-skip-postinst-restart` strings are present in the binary, and `/usr/lib/opkg/info/*vectra*` control/postinst files are `0.1.12-r4`), while `/usr/lib/opkg/status` leaves both `vectra-controller-agent` and `luci-app-vectra-controller` in `Status: install ... not-installed`. This explains the new production symptom precisely: the router keeps polling successfully with a live controller process, but inventory still resolves `controllerVersion = unknown` because both `render-config.sh` and `internal/inventory/collector.go` currently trust `opkg status <pkg>` as the only authoritative version source.
- A same-night live hotfix on that same AX3000T now proves the immediate recovery path too: replacing `/usr/libexec/vectra-controller/render-config.sh` with the local control-file fallback logic and restarting the service makes `/var/run/vectra-controller/config.json` and `/var/run/vectra-controller/status.json` report `controllerVersion = 0.1.12-r4` again even while `opkg status vectra-controller-agent` remains empty. This confirms the router-side version source problem separately from the package install journal state.
- A 2026-04-10 follow-up now also proves the permanent visibility fix in published release `0.1.12-r5`: controller/LuCI `0.1.12-r5` were built, copied into the production artifact path, synced into PostgreSQL metadata, and then manually reinstalled on the live AX3000T `1111111111` through the same direct `.ipk` URLs that the operator UI advertises. After that reinstall, both `opkg status vectra-controller-agent` and `opkg status luci-app-vectra-controller` returned normal `install user installed`, `/var/run/vectra-controller/config.json` and `/var/run/vectra-controller/status.json` both reported `controllerVersion = 0.1.12-r5`, and production `vectra_router_inventory_snapshot` already stores the latest snapshot for `bdfdb919-5e06-4344-ad8b-67a16f3b6fcf` as `0.1.12-r5`.
- A later same-day attended operator check now also proves the actual web-backed self-update path on that AX3000T end-to-end: clicking `App Update -> Controller -> Переустановить 0.1.12-r5` on `router.vectra-pro.net` created production job `e4e664d8-39f8-4076-ba0c-10dc30e26bfd`, PostgreSQL marked it `succeeded`, the router page returned to `очередь задач: 0` while still showing `Controller 0.1.12-r5`, and a follow-up read-only LAN check confirmed `opkg status vectra-controller-agent` plus runtime `controller_version` stayed at `0.1.12-r5`. The only leftover side effect from that successful UI-triggered reinstall is the standard opkg shadow conffile `/etc/config/vectra-controller-opkg`.
- A later 2026-04-10 cleanup pass on the same AX3000T removed that leftover shadow conffile live without touching package state: `/etc/config/vectra-controller-opkg` was backed up under `/root/vectra-cleanup-20260410-024806/` and then deleted, while both Vectra packages stayed at `Status: install user installed` and `/var/run/vectra-controller/status.json` still reported `controller_version = 0.1.12-r5` with the service running. In the local source tree, the next controller package now also stops persisting auto-discovered inventory fields in the package-owned UCI conffile, derives board/version facts at render time instead, and removes stale `vectra-controller-opkg` during `uci-defaults` migration so this shadow should not reappear after the next release.
- A later 2026-04-10 publish closes that durability loop for real in release `0.1.12-r7`: first `0.1.12-r6` proved the live operator self-update path still recreated `/etc/config/vectra-controller-opkg`, because package `postinst` returned on `VECTRA_SKIP_POSTINST_RESTART` before running `/etc/uci-defaults/90_vectra_controller_defaults`. The `r7` follow-up reorders that `postinst` flow so the migration/cleanup runs before the restart-skip exit, and controller/LuCI `0.1.12-r7` were then built, published as direct production artifacts, synced into PostgreSQL metadata, and installed through the real `App Update` button on the live AX3000T. Final LAN verification now shows `vectra-controller-agent = 0.1.12-r7`, `luci-app-vectra-controller = 0.1.12-r7`, `/var/run/vectra-controller/status.json` reporting `controller_version = 0.1.12-r7`, `/etc/config/vectra-controller-opkg` absent, and `/etc/config/vectra-controller` reduced to stable operator-managed settings only.
- A 2026-04-14 local investigation on live router `AndreyVK_Sochi` (`16129064-3a73-423d-818e-99ed9e73b61f`) found that the operator-visible `0 подписок` state is not just a stale UI badge. Production DB history shows `apply_passwall_config` on revision `d79a3fb1-5256-4c7a-8bdc-393280e54b80` did send a real `subscribe_list` section with the preserved secret URL, but agent-side `internal/passwall/apply.go` rendered the imported anonymous subscription id `@subscribe_list[0]` into the invalid UCI section name `vectra_sub_@subscribe_list[0]`. The post-apply live reimport then came back as revision `3624770b-9be9-4125-a7bf-127a3215ada3` with `0` `subscribe_list` sections, which explains why the router summary says `0 подписок` while the web draft still shows one stale subscription row. Local fix: `safeID()` now sanitizes all non-`[A-Za-z0-9_]` characters and regression test `TestExecutorApplySanitizesImportedSubscriptionSectionIDs` is green; this fix is not yet published as a controller package or deployed to routers.
- A same-day 2026-04-14 terminal follow-up added a new bounded router-terminal lane in source and published artifacts `0.1.12-r9`, and a later production follow-up advanced the safe-router path to `0.1.12-r10`: shared contracts include `run_terminal_command`, the agent executes one-shot shell commands with timeout, exit-code capture, stdout/stderr truncation, and timeout reporting, Go regression coverage is green locally, production direct artifacts plus PostgreSQL metadata now expose the newer controller/LuCI release, `Watch Logs -> Terminal` was already live-proven on router `1111111111`, and that same router has since been upgraded through the operator UI to `0.1.12-r10`.
- A later 2026-04-14 diagnostics follow-up is now live-proven on one production router rather than source-only: `internal/inventory/collector.go` probes Telegram reachability via the existing Go HTTP prober against `https://telegram.org/`, `https://web.telegram.org/`, `https://t.me/`, and `https://api.telegram.org/` with a `3s` timeout per request, caches the aggregated result in-process for `5m`, summarizes it as `reachable / partial / blocked`, and publishes both the aggregate and the per-target checks in `inventory.TelegramReachability`; local `go test ./...` is green with the probe coverage. After publishing controller/LuCI `0.1.12-r10` and upgrading router `1111111111`, production snapshots first reported Telegram `partial` because `t.me` timed out and then stabilized at `reachable` `4/4`, which confirms the probe works on a live router.

## Risks

- Packaging lane сейчас обходит `golang/host` внутри official OpenWrt `24.10.4` SDK, потому что host toolchain на данном VPS оказался нестабилен.
- Pure SDK compile lane для LuCI side всё ещё не закрыт: на VPS он упирается в `ucode/module.h` при сборке `lucihttp`, поэтому release feed для pilot/stable preview остаётся на manual packaging lane.
- Controlled direct/reconnect operator path уже валидирован live на AX3000T: manual direct job выключал `passwall2.@global[0].enabled`, reconnect job возвращал `enabled=1`, очищал rescue reason и оставлял router в `active|approved`.
- Автоматический local proxy-failure rescue path всё ещё требует отдельного failure-injection теста; validated пока именно operator-triggered direct/reconnect, а не полный outage сценарий.
- Даже после удаления LuCI-обвязки пользователь может воспринимать проблему как "остатки контроллера", если на роутере до перезагрузки продолжает жить уже запущенный процесс `vectra-controller-agent`; ребут в таком случае естественно убирает симптом без дополнительных изменений.
- Controller self-update lane уже прошла live validation для staged controller package update, но текущий финальный recovery после firmware flash выполнялся manual reinstall route, а не через self-update job.
- Firmware lane уже прошла реальный manual sysupgrade до OpenWrt `24.10.6`, а identity persistence root cause закрыта и повторно проверена в `0.1.9-r1`; post-flash PassWall2 recovery logic уже опубликована в controller `0.1.11-r1`, но этот automated queue-driven restore всё ещё не validated live на AX3000T.
- Для текущего live-router нет inbound-доступа с VPS по `22/80/443` на последнем seen IP `5.228.192.149`, поэтому удалённо добить пакет/рестарт напрямую нельзя; фактический recovery теперь зависит от следующего outbound check-in или локальной ручной переустановки/рестарта контроллера.
- apply lane сейчас live-proof; benign stderr noise по `subscribe.lua start all` сузили на стороне Vectra command normalization, но стоит оставить это в regression tests.
- Для release-grade firmware lane всё ещё нужен полный live proof уже опубликованного controller build: queued `update_passwall_packages` / reinstall path должен быть прогнан на AX3000T без ручного SSH между install и runtime recovery.
- Этот importer-side shunt-extras fix пока существует только в локальном workspace: без следующего controller package release historical approved/imported snapshots на production всё ещё могут не содержать `network` и часть match extras до reimport.
- Новый router-log lane остаётся snapshot-only: streaming/tailing по-прежнему нет, но first live proof на AX3000T уже закрыт, поэтому дальше риск смещается с "заработает ли вообще" на ширину source coverage, пустые noisy outputs и читаемость operator UX для больших snapshot payloads.
- The controller self-update bug itself is now closed in the direct-artifact production lane via `0.1.12-r4`, and the later conffile-cleanup durability fix is now live in published `0.1.12-r7`; however, the public signed `Packages` / `Packages.sig` feed metadata still has not been republished with the original long-lived usign key. Web-backed update jobs already target the correct `r7` artifacts from PostgreSQL; external feed-name installs remain a separate signing/ops follow-up.
- The real cleanup-safe proof currently exists on the AX3000T self-update lane only. `0.1.12-r7` is live-validated there, but a second attended reinstall on another hardware tuple would still increase confidence that the reordered `postinst` migration behaves identically outside this device.
- A 2026-04-10 production recheck from the VPS now closes the operator-visible loop completely for the current AX3000T baseline: external `GET /api/health` and router-authenticated `POST /api/router/check-in` are healthy, Caddy logs show repeated `200` responses for router `bdfdb919-5e06-4344-ad8b-67a16f3b6fcf`, and PostgreSQL now stores the latest snapshot for `1111111111` with `controllerVersion = 0.1.12-r7`.
- The new subscription-loss fix is still local-only. Until a new controller package is published and installed on affected routers, any panel-driven `apply_passwall_config` that targets imported subscription IDs like `@subscribe_list[0]` can still delete the live `subscribe_list` section and fail to recreate it, even though the draft row remains visible in the web UI.
- The new terminal lane is no longer release-only: one safe AX3000T now proves read-only command execution end-to-end, but broader runtime coverage is still missing for stderr and non-zero exits, longer outputs, and BusyBox variability on other router classes.
- The new Telegram reachability probe is no longer source-only: one production router on `0.1.12-r10` already reports real `partial` and later `reachable` snapshots, but wider rollout is still needed to learn how often other networks or providers block only `web.telegram.org` or `api.telegram.org`, whether the current `3s` timeout is still right outside this AX3000T path, and how operators should react to recurring partial states.

## Next Review

- Прогнать автоматический proxy failure -> local direct fallback -> stable recovery на AX3000T и зафиксировать real-world timing/cooldown.
- Поставить в очередь уже опубликованный `0.1.11-r1` `update_passwall_packages`/controller update на AX3000T и подтвердить live recovery `dnsmasq-full/chinadns-ng/nft` + custom geodata refresh без ручного SSH.
- Решить, нужен ли отдельный controller package release только ради importer-side shunt extras backfill, или операторского `reimport` достаточно для текущего AX3000T.
- После этого обновить note уже не как pilot, а как certified stable lane для `xiaomi,mi-router-ax3000t` stock-layout.
- Расширить live `collect_router_logs` проверку на AX3000T до источников `controller`, `passwall` и `dnsmasq`, затем решить, нужны ли дополнительные нормализации/подсказки для пустых или шумных логов.
- Decide whether to republish the public signed OpenWrt feed metadata with the original usign key so external feed consumers can also see `0.1.12-r7`, while keeping the already-proven PostgreSQL-backed direct-artifact lane as the primary operator update path.
- If a safe second device is available, rerun one more attended `App Update -> Controller` reinstall outside the AX3000T so the `0.1.12-r7` cleanup proof is no longer single-device only.
- Extend the attended production update proof beyond controller: run the remaining `App Update` package buttons (`PassWall2`, `Xray`, `sing-box`, `Hysteria`, `Geoview`) from the operator UI and record which jobs stay clean versus which still need router-side recovery work.
- Publish the new controller package with the `safeID()` subscription fix, install it on `AndreyVK_Sochi`, then re-apply the preserved draft revision so the stored subscription URL is restored on the live router without re-entering the secret manually.
- Extend live terminal proof beyond `1111111111`: run one bounded stderr or non-zero command and one longer read-only command, then verify that the resulting `run_terminal_command` payload still renders cleanly in production `Watch Logs`.
- Upgrade at least one additional safe router to the Telegram-capable controller/LuCI release and verify that its snapshot carries both the aggregate status and the per-domain Telegram checks, then decide whether any extra retry/help copy is needed for persistent `partial` cases.
