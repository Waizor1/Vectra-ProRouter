---
type: module
path: ai_docs/, scripts/, RTK.md
stage: active
confidence: high
last-reviewed: 2026-05-15
tags:
  - module
  - docs
  - openwrt
---

# Knowledge Base and Runbooks

## Confirmed

- 2026-05-16: создан безопасный handoff для нового агента `ProRouter/04 Sessions/Handoffs/2026-05-16-agent-access-memory-handoff.md`: он собирает карту доступов, local-only secret locations, проверочные команды, актуальную проектную память и guardrails без raw-паролей, cookies, токенов, DSN или ключей.
- Added `ai_docs/develop/features/openwrt24-passwall2-xray-optimization-kb.md` as the first dedicated optimization knowledge base for memory/CPU-heavy OpenWrt + PassWall2/Xray routers: source tiers, safe/yellow/red action matrix, measurement lane, canary commands, and Vectra rollout roadmap.
- Корневой репозиторий исторически держит исследования, ранбуки и helper scripts.
- Основные входы для будущих агентов: `AGENTS.md`, `RTK.md`, `ai_docs/develop/features/`.
- В `scripts/` уже есть утилиты для инвентаризации роутера, безопасных tmp-сессий и планирования обновлений PassWall2.
- Repo-side AST navigation теперь тоже инициализирована: canonical wrapper `scripts/ast-index.sh` резолвит user-local `ast-index`, root `package.json` даёт `pnpm ast:rebuild|ast:update|ast:stats`, а локальный rebuild уже подтверждён на этом workspace; текущий индекс покрывает прежде всего JS/TS contour (`apps/web`, `packages/*`), поэтому для Lua/shell/OpenWrt mirrors по-прежнему нужен `rg`.
- Для локального agent/runtime слоя теперь есть tracked note `ai_docs/develop/features/sugar-memory-local-runtime.md`, которая описывает user-local Sugar Memory workaround вне git и способ воспроизводимо переустановить fallback helper после pipx reinstall/upgrade.
- Repo hygiene для локальной работы теперь tightened: root `.gitignore` отсекает `.playwright-mcp/`, `.sugar/`, корневые QA screenshots и локальный `apps/install-helper/vectra-install-helper`, а `scripts/Sync-ProRouterVault.py` дополнительно уважает gitignore-правила, чтобы `Repo Map` не засорялся локальными runtime/directories.
- Для пользовательского Codex-окружения отдельно установлен MemPalace `v3.0.0` в `C:\Users\user\.codex\vendor_imports\mempalace\.venv` и подключен MCP-сервер `mempalace` через `C:\Users\user\.codex\config.toml`; сам `Vectra-ProRouter` не инициализировался через `mempalace init`, чтобы не добавлять служебные `entities.json`/`mempalace.yaml` в корень проекта без отдельного решения.
- Для перехода в новый чат создан безопасный handoff: `ProRouter/04 Sessions/Handoffs/2026-04-07-stable-v1-handoff.md` и prompt: `ProRouter/04 Sessions/Handoffs/2026-04-07-next-agent-prompt.md`.
- Локальные доступы сохраняются отдельно в gitignored private-memory папке `ProRouter/98 Local/`; tracked notes должны ссылаться на нее, но не дублировать raw secrets.
- Локальный Sugar Memory MCP для OpenCode/Codex на этой машине теперь снова отвечает без таймаутов после runtime-fix в pipx-установке `sugarai`: для `sugar-memory` отключён аварийный semantic/vector path при несовместимом sqlite-vec запросе и оставлен рабочий FTS5 fallback, что возвращает `search_memory`, `store_learning` и `get_project_context` в рабочее состояние.
- На операторской машине теперь установлен `gh 2.89.0`; CLI авторизован на `github.com` под `Waizor1`, базовый API доступ к приватному `Waizor1/Vectra-ProRouter` подтверждён, а non-interactive HTTPS `git ls-remote origin main` проходит после `gh auth setup-git`.
- Приватный локальный inventory доступов тоже актуализирован: на этой машине повторно подтверждён один рабочий SSH server alias и один отдельный GitHub SSH key; детализация хранится только в gitignored `ProRouter/98 Local/Server Access.md` без дублирования в tracked notes.
- Native post-sysupgrade restore helper `scripts/Invoke-VectraPostSysupgradeRestore.py` теперь реализует тот же dry-run/read-only preflight contour для AX3000T через pinned SSH: certified board/layout/OpenWrt/arch подтверждаются до любых write steps, stable feed проверяется по публичному индексу, а baseline package set сверяется с private registry.
- В helper исправлен live blocker удалённой shell-цитировки: remote scripts теперь передаются в `sh -s` через stdin, а не как одна длинная command string для `plink`.
- На живом AX3000T post-sysupgrade outage PassWall2 теперь сужен и закрыт attended recovery path'ом: кастомные `geoip_url`/`geosite_url` были обновлены до актуальных артефактов, после чего `geosite:russia-outside` снова появился в `/usr/share/v2ray/geosite.dat`, а runtime перестал падать на `RUSSIA-OUTSIDE`.
- Для этого же live recovery подтверждено, что baseline `luci-app-passwall2`/`xray-core`/`geoview` сам по себе недостаточен: для nftables transparent proxy после sysupgrade нужно вернуть `dnsmasq-full`, `chinadns-ng`, `kmod-nft-socket` и `kmod-nft-tproxy`; без них PassWall2 остаётся в no-proxy contour даже при `enabled=1`.
- Для remote operational commands на PassWall2 зафиксирован ещё один practical rule: если команда вызывает runtime, который может читать stdin (`nft` path внутри `app.sh`), безопаснее запускать `/etc/init.d/passwall2 start|restart` как прямую SSH command string, а не оставлять после неё хвост в `sh -s` script, иначе внутренний процесс может съесть остаток stdin и исказить post-check commands.
- Repo-side bootstrap tooling now also preserves storage facts instead of only filenames: native `scripts/Sync-PasswallBootstrapMirror.py` extracts `Version`, `Installed-Size`, and the actual `.ipk` file length from each mirrored package, publishes them into `manifest.json`, and therefore lets the web-side AX3000T bootstrap generator validate staging/overlay budgets against the same per-package metadata it renders into the installer script.
- Project-local Kilo configuration now exists under `.kilo/`: default `vectra-prorouter-steward` agent, focused agents for web/OpenWrt/router-agent/deploy/read-only work, reusable `/vectra-*` commands, and project skills for Vectra workflow, OpenWrt/PassWall2, web control plane, router-agent release, and VPS deploy hygiene. Global Kilo config also now exposes Sugar Memory, ICM, Context7, and Playwright MCPs for future sessions.

## Boundaries

- Это главный слой для PassWall2/OpenWrt знаний в проекте.
- Документация должна оставаться привязанной к коду и фактам, а не к форумным пересказам.
- `ProRouter/98 Local/` запрещено включать в generated repo map, публичные handoff notes или коммиты.
- Локальные agent/runtime hotfixes вне репозитория нужно фиксировать здесь и в daily note отдельно, потому что они не видны из git history проекта.
- Native Python/shell helper scripts теперь являются canonical path в этом workspace; legacy `.ps1` сохранены как fallback, но macOS-first docs, indexes и runbooks теперь указывают сначала на `python3 ./scripts/*.py`. Live-router helpers по-прежнему используют fail-closed transport layer: canonical OpenSSH через pinned `--openssh-known-hosts-file` и optional `--openssh-identity-file`, а прежний PuTTY password lane (`--router-password` + `--router-host-key`) сохранён только как fallback.

## Next Review

- Обновить attended restore/runbook так, чтобы post-sysupgrade lane явно восстанавливала кастомные geodata и nftables DNS/runtime deps (`dnsmasq-full`, `chinadns-ng`, `kmod-nft-socket`, `kmod-nft-tproxy`), а не только baseline package presence.

2026-04-17 addendum:
- The PassWall mirror/update documentation layer now shares the same current release target across bootstrap and controller-update planning: `scripts/Sync-PasswallBootstrapMirror.{py,ps1}` default to `26.4.10-1`, and the checked-in bootstrap fixtures were refreshed to the same release family (`xray-core 26.3.27-r1`, `geoview 0.2.5-r1`, `luci-app-passwall2 26.4.10-r1`, optional `sing-box 1.13.6-r1`, `hysteria 2.8.1-r1`).
- `apps/web/scripts/sync-artifact-metadata.mjs` now has a real PassWall mirror-manifest ingestion path for `passwall_package` plus stack-level `passwall_bundle` artifacts, and a local dry-run on a synthetic mirror directory confirmed the emitted metadata shape plus corrected `aarch64_cortex-a53` architecture parsing.
- The practical next runbook step is no longer “guess current PassWall versions from feeds on the router”, but “publish/sync the explicit mirror metadata first, then drive the controller update lane with that artifact set”.
- A same-day 2026-04-18 ops follow-up now also removes the stale manual examples that still suggested `26.4.5-1` for deploy-time PassWall syncs. `deploy/README.md` now uses a `PASSWALL_TAG` variable instead of hardcoding the old tag, `deploy/scripts/sync-runtime-artifacts.sh` examples switched to `<tag>` placeholders, and the deploy runbook now documents the VPS-native `deploy/scripts/refresh-passwall-mirror.py` plus its `systemd` timer as the canonical “keep production artifacts fresh” path.
- The deploy runbook now also documents a separate VPS-native disk guard path instead of only the older weekly cleanup timer: `deploy/scripts/vps-disk-guard.sh` is the analysis-first helper that logs root usage, hot paths, recent rollback backups, and Docker summary on every run, then conditionally chains into `vps-disk-cleanup.sh` and deploy-backup retention when the root filesystem crosses the configured threshold. The paired `vectra-vps-disk-guard.{service,timer}` units are now the canonical “watch the disk continuously” path, while `vectra-vps-disk-cleanup.timer` remains the lower-frequency fallback broom. The default policy is intentionally split for safety: cleanup starts at `75%`, but aged `web-release-*` / `web-deploy-ready-*` rollback backups are still preserved until a separate `82%` backup-prune threshold is crossed.

2026-04-19 addendum:
- The repo now has a tracked local operator helper for fast live panel access: `scripts/VectraPanelCli.sh` wraps `apps/web/scripts/vectra-panel-cli.mjs`, logs in through the real `/api/operator/login` flow, talks to protected `tRPC` procedures under `/api/trpc`, and caches only the operator session cookie under gitignored `.codex-runtime/vectra-panel/session.json`.
- On this workstation, the helper can also bootstrap its own credentials safely from the already-confirmed `ssh vectra-prod` path by reading `VECTRA_OPERATOR_USER`, `VECTRA_OPERATOR_PASSWORD`, and `VECTRA_DEFAULT_CONTROL_DOMAIN` from the production `.env` instead of duplicating secrets into tracked notes or repo files.
- The helper now also exposes an explicit coverage map and the missing first-class lanes that were still only implicit in source: `catalog` prints the current operator `tRPC` surface, `draft` covers list/workspace/editor/save/queue-apply, `notifications` covers status plus subscribe/unsubscribe, `fleet` now has approve-import/reimport/delete actions, and `router-api` can hit the separate router-facing endpoints once a real router id/token pair is provided.

2026-04-23 live Netis/Keenetic capture addendum:
- A same-day live DmitryGubenko follow-up now documents a safe remote workaround for third-party extender capture behind the Netis `NX31` without touching WAN. Using `scripts/VectraPanelCli.sh terminal ...`, only the downstream `lan3` bridge membership was moved from `br-lan` into the already-isolated `wifi_clean` segment, while WAN stayed on `eth1 192.168.0.182/24`.
- The write path itself is now proven fail-closed enough for future repeats: a temporary rollback script plus background timer were staged first, `network reload` and `dnsmasq restart` were then executed, and the rollback was cancelled only after read-back confirmed `network.cfg030f15.ports='lan1' 'lan2'`, `network.wifi_clean_dev.ports='lan3'`, and `lan3 master br-wifi_clean`.
- The target Keenetic capture is also explicitly verified rather than inferred. Reset Keenetic Orbiter Pro `KN-2810` in `Ext` mode obtained DHCP lease `192.168.99.128` on `br-wifi_clean` with MAC `50:ff:20:7d:1e:46` and hostname `Keenetic-1151`; read-only checks confirmed successful ICMP and an initial live HTTP response `302 -> /netfriend` with header `Ndm-Sysmode: extender`.
- Operationally this closes the earlier "can we grab the repeater onto Netis remotely?" question for the wired uplink path. A later read-back still showed the same Keenetic MAC reachable on `192.168.99.128` at ARP/L2 while HTTP timed out, so the confirmed durable state is the clean network capture; the Keenetic HTTP wizard should be treated as local/transient onboarding rather than a Netis-side networking blocker.
