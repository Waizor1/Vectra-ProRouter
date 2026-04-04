# Vectra-ProRouter

Knowledge base and operational runbooks for PassWall2 and OpenWrt `24.xx`, with a practical focus on:

- PassWall2 operations and update workflows
- OpenWrt `24.xx` console administration
- OpenWrt application/package development
- Xiaomi AX3000T / Filogic safety and recovery planning

## What Is In This Repo

- `AGENTS.md` and `RTK.md` define the workspace rules and the fast runbook for future agent work
- `ai_docs/develop/features/` contains the curated human-readable knowledge base
- `scripts/` contains helper utilities for router inventory, temporary `/tmp` test sessions, and PassWall2 planning

## Start Here

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

```powershell
git clone https://github.com/openwrt-passwall/openwrt-passwall2.git passwall2
git clone --branch openwrt-24.10 https://github.com/openwrt/openwrt.git openwrt-24.10-src
git clone https://github.com/openwrt/procd.git procd-src
```

Notes:

- `passwall2/` is used as the local upstream mirror for PassWall2 source inspection
- `openwrt-24.10-src/` is used as the primary-source mirror for `sysupgrade`, base-files, and platform behavior checks
- `procd-src/` is used when validating `procd` and `ubus` runtime behavior

## Publishing Posture

This repo is safest to keep `private` by default because it contains router-specific operational notes and dated live snapshots. Promote to a public repository only after a deliberate review of what should remain private.
