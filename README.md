# Vectra-ProRouter

Operator-facing runbooks, helper scripts, and control-plane code for Vectra
Stable V1 on the certified Xiaomi AX3000T stock-layout baseline.

Current certified scope:

- `xiaomi,mi-router-ax3000t`
- `mediatek/filogic`
- `aarch64_cortex-a53`
- OpenWrt `24.10.x`
- stock-layout only

Everything outside that tuple must stay fail-closed for destructive actions.

This repository also keeps the supporting knowledge base for PassWall2 and
OpenWrt `24.xx`, with a practical focus on:

- PassWall2 operations and update workflows
- OpenWrt `24.xx` console administration
- OpenWrt application/package development
- Xiaomi AX3000T / Filogic safety and recovery planning

## What Is In This Repo

- `AGENTS.md` and `RTK.md` define the workspace rules and the fast runbook for future agent work
- `ai_docs/develop/features/` contains the curated human-readable knowledge base
- `scripts/` contains helper utilities for router inventory, temporary `/tmp` test sessions, and PassWall2 planning

## Start Here

Stable V1 quick links:

- controller/feed build: `scripts/build-vectra-openwrt-feed.sh`
- feed and restore runbook: `ai_docs/develop/features/vectra-openwrt-feed-publishing.md`
- VPS deploy runbook: `deploy/README.md`
- post-firmware package restore: `scripts/Invoke-VectraPostSysupgradeRestore.py`
- historical snapshot sanitation: `apps/web/scripts/sanitize-historical-passwall-snapshots.mjs`

- PassWall2 operational questions:
  - `ai_docs/develop/features/passwall2-ops-cheatsheet.md`
  - `ai_docs/develop/features/passwall2-openwrt24-knowledge-base.md`
- Generic OpenWrt console/platform work:
  - `ai_docs/develop/features/openwrt24-console-knowledge-base/06-cheatsheet.md`
  - `ai_docs/develop/features/openwrt24-console-knowledge-base/README.md`
- OpenWrt app/package work:
  - `ai_docs/develop/features/openwrt24-app-development-knowledge-base/06-cheatsheet.md`
  - `ai_docs/develop/features/openwrt24-app-development-knowledge-base/README.md`
- Real Xiaomi AX3000T work:
  - `ai_docs/develop/features/router-xiaomi-ax3000t-live-kb.md`
  - `ai_docs/develop/features/router-ax3000t-safe-test-harness.md`
  - `ai_docs/develop/features/openwrt24-console-knowledge-base/08-filogic-recovery-write-safety.md`

## Local Source Mirrors

The top-level Git repository intentionally tracks the knowledge base and helper scripts, not the full upstream mirror histories. These directories are treated as optional local mirrors and are ignored by the root `.gitignore`:

- `passwall2/`
- `openwrt-24.10-src/`
- `procd-src/`

If you want the full source-backed workflow locally, hydrate the mirrors into those exact paths:

```bash
git clone https://github.com/openwrt-passwall/openwrt-passwall2.git passwall2
git clone --branch openwrt-24.10 https://github.com/openwrt/openwrt.git openwrt-24.10-src
git clone https://github.com/openwrt/procd.git procd-src
```

Cross-platform helper invocation:

- Native macOS/Linux entrypoints now live directly under `scripts/` as tracked `*.py` or `*.sh` files. Prefer `python3 ./scripts/<name>.py ...` or `bash ./scripts/<name>.sh ...`.
- Existing `*.ps1` helpers are preserved as legacy fallback only when PowerShell is available.
- `bash ./scripts/run-ps1.sh ./scripts/<name>.ps1 ...` remains available only for legacy PowerShell-based workflows.
- The new Sugar runtime note lives at `ai_docs/develop/features/sugar-memory-local-runtime.md`.

## Panel API CLI

For fast operator-side reads and actions against the live Vectra panel, use:

```bash
bash ./scripts/VectraPanelCli.sh status
bash ./scripts/VectraPanelCli.sh catalog
bash ./scripts/VectraPanelCli.sh fleet overview
bash ./scripts/VectraPanelCli.sh fleet list
bash ./scripts/VectraPanelCli.sh router show OpenWrt
bash ./scripts/VectraPanelCli.sh draft workspace OpenWrt
bash ./scripts/VectraPanelCli.sh notifications status
bash ./scripts/VectraPanelCli.sh router-api health
bash ./scripts/VectraPanelCli.sh logs snapshot OpenWrt --source system --lines 100
bash ./scripts/VectraPanelCli.sh terminal run OpenWrt --command 'ubus call system board'
bash ./scripts/VectraPanelCli.sh call fleet.monitoring
```

Notes:

- The CLI logs in through the real operator flow at `/api/operator/login` and then talks to protected `tRPC` procedures under `/api/trpc`.
- Credentials are resolved from CLI flags or env vars first; if they are absent, the helper can read `VECTRA_OPERATOR_USER`, `VECTRA_OPERATOR_PASSWORD`, and `VECTRA_DEFAULT_CONTROL_DOMAIN` from the production VPS via `ssh vectra-prod`.
- Only the operator session cookie is cached locally in `.codex-runtime/vectra-panel/session.json`; no credentials are written into tracked files.
- `catalog` prints the current operator `tRPC` surface plus router-facing endpoints so you can see what is covered without reopening the source tree.
- `call <trpc.path>` is the generic fallback when you need a procedure that does not yet have a dedicated wrapper command.
- Operator auth and router auth are intentionally different domains: the panel cookie covers protected `tRPC`, while `/api/router/*` requires explicit `x-vectra-router-id` and `x-vectra-router-token` headers.

Notes:

- `passwall2/` is used as the local upstream mirror for PassWall2 source inspection
- `openwrt-24.10-src/` is used as the primary-source mirror for `sysupgrade`, base-files, and platform behavior checks
- `procd-src/` is used when validating `procd` and `ubus` runtime behavior

## Publishing Posture

This repo is safest to keep `private` by default because it contains router-specific operational notes and dated live snapshots. Promote to a public repository only after a deliberate review of what should remain private.
