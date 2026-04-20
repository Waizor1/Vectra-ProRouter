---
type: generated
updated: '2026-04-20 13:08:02 +03:00'
generated-by: scripts/Sync-ProRouterVault.py
tags:
  - generated
  - structure
---

# Repo Map

Generated from the current workspace root `Vectra-ProRouter`.

## Snapshot

- Generated at: `2026-04-20 13:08:02 +03:00`
- Top-level directories: `8`
- Top-level files: `18`
- Tree depth: `3`

## Module Notes

| Area | Path | Note |
|---|---|---|
| Knowledge base and runbooks | `ai_docs/, scripts/, RTK.md` | [[02 Modules/Knowledge Base and Runbooks]] |
| Web control plane | `apps/web` | [[02 Modules/Web Control Plane]] |
| Shared contracts | `packages/contracts` | [[02 Modules/Shared Contracts]] |
| Shared database | `packages/db` | [[02 Modules/Shared Database]] |
| Router agent | `router/vectra-controller-agent` | [[02 Modules/Router Agent]] |
| LuCI controller package | `router/luci-app-vectra-controller` | [[02 Modules/LuCI Controller Package]] |
| Deployment stack | `deploy/` | [[02 Modules/Deployment Stack]] |
| Source mirrors | `passwall2/, openwrt-24.10-src/, procd-src/` | [[02 Modules/Source Mirrors]] |

## Structure

```text
./
|- ai_docs/
|  \- develop/
|     \- features/
|- apps/
|  |- install-helper/
|  |  |- build-release-artifacts.sh
|  |  |- discovery.go
|  |  |- go.mod
|  |  |- go.sum
|  |  |- install.go
|  |  |- main.go
|  |  |- models.go
|  |  |- server.go
|  |  |- server_test.go
|  |  |- sessions.go
|  |  \- storage.go
|  \- web/
|     |- public/
|     |- scripts/
|     |- src/
|     |- tests/
|     |- .env.example
|     |- .gitignore
|     |- drizzle.config.ts
|     |- eslint.config.js
|     |- next.config.js
|     |- package.json
|     |- postcss.config.js
|     |- prettier.config.js
|     |- README.md
|     |- start-database.sh
|     |- tsconfig.json
|     \- vitest.config.ts
|- deploy/
|  |- examples/
|  |  \- pilot-artifacts.seed.json
|  |- scripts/
|  |  |- backup-postgres.sh
|  |  |- deploy-web-release.sh
|  |  |- prepare-runtime.sh
|  |  |- refresh-passwall-mirror.py
|  |  |- restore-postgres.sh
|  |  |- smoke-check.sh
|  |  |- sync-runtime-artifacts.sh
|  |  |- vps-disk-cleanup.sh
|  |  \- vps-disk-guard.sh
|  |- systemd/
|  |  |- vectra-passwall-mirror-refresh.service
|  |  |- vectra-passwall-mirror-refresh.timer
|  |  |- vectra-vps-disk-cleanup.service
|  |  |- vectra-vps-disk-cleanup.timer
|  |  |- vectra-vps-disk-guard.service
|  |  \- vectra-vps-disk-guard.timer
|  \- README.md
|- logs/
|  \- errors/
|     \- .gitkeep
|- packages/
|  |- contracts/
|  |  |- fixtures/
|  |  |- src/
|  |  |- package.json
|  |  \- tsconfig.json
|  \- db/
|     |- drizzle/
|     |- src/
|     |- package.json
|     \- tsconfig.json
|- ProRouter/
|  |- 00 Dashboard/
|  |  |- Agent Workflow.md
|  |  |- Decision Register.base
|  |  |- Module Status.base
|  |  |- Repo Map.md
|  |  |- Session Feed.base
|  |  \- Stage Board.md
|  |- 02 Modules/
|  |  |- Deployment Stack.md
|  |  |- Knowledge Base and Runbooks.md
|  |  |- LuCI Controller Package.md
|  |  |- Router Agent.md
|  |  |- Shared Contracts.md
|  |  |- Shared Database.md
|  |  |- Source Mirrors.md
|  |  \- Web Control Plane.md
|  |- 03 Decisions/
|  |  |- ADR Index.md
|  |  \- ADR-0001-obsidian-project-vault.md
|  |- 04 Sessions/
|  |  |- Daily/
|  |  \- Handoffs/
|  |- 05 Templates/
|  |  |- Daily Note.md
|  |  |- Decision Template.md
|  |  \- Module Template.md
|  \- Home.md
|- router/
|  |- luci-app-vectra-controller/
|  |  |- htdocs/
|  |  |- root/
|  |  \- Makefile
|  \- vectra-controller-agent/
|     |- cmd/
|     |- internal/
|     |- openwrt/
|     |- go.mod
|     \- README.md
|- scripts/
|  |- fixtures/
|  |  \- xiaomi-ax3000t-openwrt24.txt
|  |- _vectra_native.py
|  |- Add-ProRouterStatusEntry.ps1
|  |- Add-ProRouterStatusEntry.py
|  |- ast-index.sh
|  |- build-vectra-openwrt-feed.sh
|  |- build-web-release-slice.sh
|  |- ensure-sugar-memory-local-fallback.py
|  |- Get-OpenWrtRouterInventory.ps1
|  |- Get-OpenWrtRouterInventory.py
|  |- Get-Passwall2ReleaseAssets.ps1
|  |- Get-Passwall2ReleaseAssets.py
|  |- Invoke-VectraPostSysupgradeRestore.ps1
|  |- Invoke-VectraPostSysupgradeRestore.py
|  |- Manage-OpenWrtTmpProgramSession.ps1
|  |- Manage-OpenWrtTmpProgramSession.py
|  |- OpenWrtSshTransport.ps1
|  |- Resolve-Passwall2RouterPlan.ps1
|  |- Resolve-Passwall2RouterPlan.py
|  |- run-ps1.sh
|  |- Sync-PasswallBootstrapMirror.ps1
|  |- Sync-PasswallBootstrapMirror.py
|  |- Sync-ProRouterVault.ps1
|  |- Sync-ProRouterVault.py
|  |- Test-VectraDbUpgradePath.ps1
|  |- Test-VectraDbUpgradePath.py
|  \- VectraPanelCli.sh
|- .dockerignore
|- .env.example
|- .gitignore
|- AGENTS.md
|- Caddyfile
|- check-in
|- docker-compose.yml
|- Dockerfile.web
|- operator
|- package.json
|- pnpm-lock.yaml
|- pnpm-workspace.yaml
|- README.md
|- release-web-unconfirmed-changes.tar.gz
|- router-api
|- router-app-update-3400.png
|- RTK.md
\- tsconfig.base.json
```
