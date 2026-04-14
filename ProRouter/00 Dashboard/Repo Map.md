---
type: generated
updated: '2026-04-13 20:38:41 +03:00'
generated-by: scripts/Sync-ProRouterVault.ps1
tags:
  - generated
  - structure
---

# Repo Map

Generated from the current workspace root `Vectra-ProRouter`.

## Snapshot

- Generated at: `2026-04-13 20:38:41 +03:00`
- Top-level directories: `12`
- Top-level files: `13`
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
|- .codex-runtime/
|  |- controller-r3-src/
|  |- inspect-passwall2/
|  |  |- conffiles
|  |  |- control
|  |  |- control.tar.gz
|  |  |- data.tar.gz
|  |  |- debian-binary
|  |  |- postinst
|  |  |- postinst-pkg
|  |  \- prerm
|  |- lighthouse-router/
|  |  |- report.html
|  |  \- report.json
|  |- passwall-bootstrap-26.4.5-1/
|  |  |- chinadns-ng_2025.08.09-r1_aarch64_cortex-a53.ipk
|  |  |- geoview_0.2.5-r1_aarch64_cortex-a53.ipk
|  |  |- luci-app-passwall2_26.4.5-r1_all.ipk
|  |  |- v2ray-geoip_202603260032.1_all.ipk
|  |  |- v2ray-geosite_202603292224.1_all.ipk
|  |  \- xray-core_26.3.27-r1_aarch64_cortex-a53.ipk
|  |- passwall-bootstrap-mirror/
|  |  \- 26.4.5-1/
|  |- AntiScanner.sh
|  |- ax3000t-bootstrap-syntax-check.sh
|  |- ax3000t-myshunt-rebind.sh
|  |- baseline-controller-jobs.sh
|  |- bootstrap-preview.sh
|  |- controller-r4-src.tar.gz
|  |- controller-r6-src.tar.gz
|  |- controller-r7-src.tar.gz
|  |- editorSurface-359.json
|  |- fleet-live.png
|  |- fleet-live-fixed.png
|  |- install-motd.sh
|  |- latest-controller-job.sh
|  |- passwall-bootstrap-mirror-26.4.5-1.tar.gz
|  |- poll-controller-job.sh
|  |- quote-min.sh
|  |- router-geo-live.png
|  |- router-main-live.png
|  |- router-main-mobile-live.png
|  |- ssh-debug.err.log
|  |- ssh-debug.out.log
|  |- ssh-debug-n.err.log
|  |- ssh-debug-n.out.log
|  |- sync-artifact-metadata.mjs
|  |- timeweb-zabbix-install.sh
|  |- tmp-install-presets.cjs
|  |- vectra-controller-0.1.12-r2-src.tar.gz
|  |- vectra-controller-0.1.12-r3-src.tar.gz
|  |- vectra-controller-0.1.12-src.tar.gz
|  |- vectra-web-push-20260409-021508.tar.gz
|  |- vectra-web-release.tgz
|  |- vectra-web-release-20260408-003514.tar.gz
|  |- vectra-web-release-20260408-010844-clean.tar.gz
|  |- vectra-web-release-20260408-013400-router-delete.tar.gz
|  |- vectra-web-release-20260408-022721-enrollment.tar.gz
|  |- vectra-web-release-20260408-023028-enrollment-fix.tar.gz
|  |- vectra-web-shared-20260408-150330.tar.gz
|  |- vectra-web-shunt-20260408-201035.tar.gz
|  |- vectra-web-ux-20260408-145320.tar.gz
|  |- verify-artifacts.sh
|  |- verify-router-snapshot.sh
|  |- vps-backup-r6.fixed.sh
|  |- vps-backup-r6.sh
|  |- vps-build-r6.sh
|  |- vps-check-accidental-rootdirs.sh
|  |- vps-check-r6-build-state.sh
|  |- vps-check-r6-tmpdirs.sh
|  |- vps-extract-r6-src.sh
|  |- vps-find-keydir.sh
|  |- vps-find-usign.sh
|  |- vps-inspect-deploy-root.sh
|  |- vps-inspect-r6.sh
|  |- vps-inspect-r6-partial-backup.sh
|  |- vps-key-sha.sh
|  |- vps-list-backups-top.sh
|  |- vps-list-r5-src.sh
|  |- vps-list-r6-backups.sh
|  |- vps-ping.sh
|  |- web-dev.stderr.log
|  |- web-dev.stdout.log
|  |- web-dev-live.log
|  |- web-local-3101.err.log
|  |- web-local-3101.out.log
|  |- web-start.stderr.log
|  |- web-start.stdout.log
|  |- z4r.sh
|  |- z4r-main.sh
|  \- z4r-runtime.sh
|- .playwright-cli/
|  |- console-2026-04-05T23-10-55-709Z.log
|  |- console-2026-04-07T17-42-25-526Z.log
|  |- console-2026-04-07T17-46-41-615Z.log
|  |- console-2026-04-07T17-47-03-689Z.log
|  |- console-2026-04-07T17-48-17-426Z.log
|  |- console-2026-04-07T17-49-40-392Z.log
|  |- page-2026-04-05T23-10-48-374Z.yml
|  |- page-2026-04-05T23-10-51-167Z.yml
|  |- page-2026-04-05T23-10-55-944Z.yml
|  |- page-2026-04-07T17-42-25-833Z.yml
|  |- page-2026-04-07T17-43-31-116Z.yml
|  |- page-2026-04-07T17-43-54-104Z.yml
|  |- page-2026-04-07T17-46-42-047Z.yml
|  |- page-2026-04-07T17-47-03-865Z.yml
|  |- page-2026-04-07T17-47-27-551Z.yml
|  |- page-2026-04-07T17-47-57-888Z.yml
|  |- page-2026-04-07T17-48-17-608Z.yml
|  |- page-2026-04-07T17-49-05-118Z.yml
|  |- page-2026-04-07T17-49-26-853Z.yml
|  \- page-2026-04-07T17-49-40-473Z.yml
|- ai_docs/
|  \- develop/
|     \- features/
|- apps/
|  \- web/
|     |- .codex-runtime/
|     |- public/
|     |- scripts/
|     |- src/
|     |- tests/
|     |- .env
|     |- .env.example
|     |- .gitignore
|     |- drizzle.config.ts
|     |- eslint.config.js
|     |- next.config.js
|     |- next-env.d.ts
|     |- package.json
|     |- postcss.config.js
|     |- prettier.config.js
|     |- README.md
|     |- start-database.sh
|     |- tsconfig.json
|     |- tsconfig.tsbuildinfo
|     \- vitest.config.ts
|- deploy/
|  |- examples/
|  |  \- pilot-artifacts.seed.json
|  |- scripts/
|  |  |- backup-postgres.sh
|  |  |- prepare-runtime.sh
|  |  |- restore-postgres.sh
|  |  |- smoke-check.sh
|  |  \- vps-disk-cleanup.sh
|  |- systemd/
|  |  |- vectra-vps-disk-cleanup.service
|  |  \- vectra-vps-disk-cleanup.timer
|  \- README.md
|- openwrt-24.10-src/
|  |- package/
|  |  |- base-files/
|  |  |- system/
|  |  \- Makefile
|  |- target/
|  |  |- linux/
|  |  |- Config.in
|  |  \- Makefile
|  |- BSDmakefile
|  |- Config.in
|  |- COPYING
|  |- feeds.conf.default
|  |- Makefile
|  |- README.md
|  \- rules.mk
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
|- passwall2/
|  |- luci-app-passwall2/
|  |  |- htdocs/
|  |  |- luasrc/
|  |  |- po/
|  |  |- root/
|  |  \- Makefile
|  |- AGENTS.md
|  |- LICENSE
|  \- README.md
|- procd-src/
|  |- initd/
|  |  |- early.c
|  |  |- init.c
|  |  |- init.h
|  |  |- mkdev.c
|  |  \- preinit.c
|  |- jail/
|  |  |- capabilities.c
|  |  |- capabilities.h
|  |  |- cgroups.c
|  |  |- cgroups.h
|  |  |- cgroups-bpf.c
|  |  |- cgroups-bpf.h
|  |  |- elf.c
|  |  |- elf.h
|  |  |- fs.c
|  |  |- fs.h
|  |  |- jail.c
|  |  |- jail.h
|  |  |- log.h
|  |  |- netifd.c
|  |  |- netifd.h
|  |  |- preload.c
|  |  |- seccomp.c
|  |  |- seccomp.h
|  |  |- seccomp-bpf.h
|  |  |- seccomp-oci.c
|  |  |- seccomp-oci.h
|  |  \- seccomp-syscalls-helpers.h
|  |- plug/
|  |  |- coldplug.c
|  |  |- hotplug.c
|  |  |- hotplug.h
|  |  \- udevtrigger.c
|  |- service/
|  |  |- instance.c
|  |  |- instance.h
|  |  |- service.c
|  |  |- service.h
|  |  |- setlbf.c
|  |  |- trigger.c
|  |  |- validate.c
|  |  \- watch.c
|  |- trace/
|  |  |- preload.c
|  |  \- trace.c
|  |- upgraded/
|  |  |- CMakeLists.txt
|  |  \- upgraded.c
|  |- utils/
|  |  |- askfirst.c
|  |  |- utils.c
|  |  \- utils.h
|  |- CMakeLists.txt
|  |- container.h
|  |- hotplug-dispatch.c
|  |- inittab.c
|  |- libc-compat.h
|  |- log.h
|  |- make_capabilities_h.sh
|  |- make_syscall_h.sh
|  |- preload.h
|  |- procd.c
|  |- procd.h
|  |- rcS.c
|  |- rcS.h
|  |- signal.c
|  |- state.c
|  |- system.c
|  |- sysupgrade.c
|  |- sysupgrade.h
|  |- ubus.c
|  |- uxc.c
|  |- watchdog.c
|  \- watchdog.h
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
|  |- Add-ProRouterStatusEntry.ps1
|  |- build-vectra-openwrt-feed.sh
|  |- Get-OpenWrtRouterInventory.ps1
|  |- Get-Passwall2ReleaseAssets.ps1
|  |- Invoke-VectraPostSysupgradeRestore.ps1
|  |- Manage-OpenWrtTmpProgramSession.ps1
|  |- Resolve-Passwall2RouterPlan.ps1
|  |- Sync-PasswallBootstrapMirror.ps1
|  |- Sync-ProRouterVault.ps1
|  \- Test-VectraDbUpgradePath.ps1
|- .dockerignore
|- .env.example
|- .gitignore
|- AGENTS.md
|- Caddyfile
|- docker-compose.yml
|- Dockerfile.web
|- package.json
|- pnpm-lock.yaml
|- pnpm-workspace.yaml
|- README.md
|- RTK.md
\- tsconfig.base.json
```
