---
type: module
path: ai_docs/, scripts/, RTK.md
stage: active
confidence: high
last-reviewed: 2026-04-10
tags:
  - module
  - docs
  - openwrt
---

# Knowledge Base and Runbooks

## Confirmed

- Корневой репозиторий исторически держит исследования, ранбуки и helper scripts.
- Основные входы для будущих агентов: `AGENTS.md`, `RTK.md`, `ai_docs/develop/features/`.
- В `scripts/` уже есть утилиты для инвентаризации роутера, безопасных tmp-сессий и планирования обновлений PassWall2.
- Для пользовательского Codex-окружения отдельно установлен MemPalace `v3.0.0` в `C:\Users\user\.codex\vendor_imports\mempalace\.venv` и подключен MCP-сервер `mempalace` через `C:\Users\user\.codex\config.toml`; сам `Vectra-ProRouter` не инициализировался через `mempalace init`, чтобы не добавлять служебные `entities.json`/`mempalace.yaml` в корень проекта без отдельного решения.
- Для перехода в новый чат создан безопасный handoff: `ProRouter/04 Sessions/Handoffs/2026-04-07-stable-v1-handoff.md` и prompt: `ProRouter/04 Sessions/Handoffs/2026-04-07-next-agent-prompt.md`.
- Локальные доступы сохраняются отдельно в gitignored private-memory папке `ProRouter/98 Local/`; tracked notes должны ссылаться на нее, но не дублировать raw secrets.
- Post-sysupgrade restore helper `scripts/Invoke-VectraPostSysupgradeRestore.ps1` теперь реально проходит dry-run/read-only preflight против AX3000T через pinned SSH: certified board/layout/OpenWrt/arch подтверждены, stable feed `0.1.10-r1` прочитан, установленный baseline controller/LuCI/PassWall2/xray/geoview совпадает с private registry.
- В helper исправлен live blocker удалённой shell-цитировки: remote scripts теперь передаются в `sh -s` через stdin, а не как одна длинная command string для `plink`.
- На живом AX3000T post-sysupgrade outage PassWall2 теперь сужен и закрыт attended recovery path'ом: кастомные `geoip_url`/`geosite_url` были обновлены до актуальных артефактов, после чего `geosite:russia-outside` снова появился в `/usr/share/v2ray/geosite.dat`, а runtime перестал падать на `RUSSIA-OUTSIDE`.
- Для этого же live recovery подтверждено, что baseline `luci-app-passwall2`/`xray-core`/`geoview` сам по себе недостаточен: для nftables transparent proxy после sysupgrade нужно вернуть `dnsmasq-full`, `chinadns-ng`, `kmod-nft-socket` и `kmod-nft-tproxy`; без них PassWall2 остаётся в no-proxy contour даже при `enabled=1`.
- Для remote operational commands на PassWall2 зафиксирован ещё один practical rule: если команда вызывает runtime, который может читать stdin (`nft` path внутри `app.sh`), безопаснее запускать `/etc/init.d/passwall2 start|restart` как прямую SSH command string, а не оставлять после неё хвост в `sh -s` script, иначе внутренний процесс может съесть остаток stdin и исказить post-check commands.
- Repo-side bootstrap tooling now also preserves storage facts instead of only filenames: `scripts/Sync-PasswallBootstrapMirror.ps1` extracts `Version`, `Installed-Size`, and the actual `.ipk` file length from each mirrored package, publishes them into `manifest.json`, and therefore lets the web-side AX3000T bootstrap generator validate staging/overlay budgets against the same per-package metadata it renders into the installer script.

## Boundaries

- Это главный слой для PassWall2/OpenWrt знаний в проекте.
- Документация должна оставаться привязанной к коду и фактам, а не к форумным пересказам.
- `ProRouter/98 Local/` запрещено включать в generated repo map, публичные handoff notes или коммиты.

## Next Review

- Обновить attended restore/runbook так, чтобы post-sysupgrade lane явно восстанавливала кастомные geodata и nftables DNS/runtime deps (`dnsmasq-full`, `chinadns-ng`, `kmod-nft-socket`, `kmod-nft-tproxy`), а не только baseline package presence.
