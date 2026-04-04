# AGENTS.md

Precedence: the nearest `AGENTS.md` wins. Root file defines global defaults for this workspace.

Note: the root Git history tracks the KB and helper scripts. `passwall2/`, `openwrt-24.10-src/`, and `procd-src/` are expected as optional local source mirrors and may be absent in a fresh clone until hydrated.

## First Read

1. Read `RTK.md` before doing any substantial work.
2. If you work inside `passwall2/`, then also read `passwall2/AGENTS.md`.
3. If the task is about generic OpenWrt 24.xx platform administration, start with `ai_docs/develop/features/openwrt24-console-knowledge-base/06-cheatsheet.md` and then `ai_docs/develop/features/openwrt24-console-knowledge-base/README.md`.
4. If the task is about creating or packaging OpenWrt 24.xx applications, start with `ai_docs/develop/features/openwrt24-app-development-knowledge-base/06-cheatsheet.md` and then `ai_docs/develop/features/openwrt24-app-development-knowledge-base/README.md`.
5. If the task is about a real OpenWrt router, collect facts with `ai_docs/develop/features/openwrt24-console-knowledge-base/07-router-intake-template.md`.
6. Read `ai_docs/develop/features/passwall2-ops-cheatsheet.md` for the shortest PassWall2 operational path.
7. Use `ai_docs/develop/features/passwall2-openwrt24-knowledge-base.md` as the deep PassWall2 reference, not as the first-stop file.
8. If the task involves live writes, sysupgrade, recovery, or firmware safety on Filogic, read `ai_docs/develop/features/openwrt24-console-knowledge-base/08-filogic-recovery-write-safety.md`.

## Commands

| Task | Command | Notes |
|------|---------|-------|
| Repo overview | `Get-ChildItem -Force` | Root contains docs plus local upstream mirror |
| Read runbook | `Get-Content RTK.md` | Mandatory before non-trivial work |
| Read OpenWrt cheatsheet | `Get-Content ai_docs\\develop\\features\\openwrt24-console-knowledge-base\\06-cheatsheet.md` | Fastest generic OpenWrt 24.xx path |
| Read OpenWrt KB index | `Get-Content ai_docs\\develop\\features\\openwrt24-console-knowledge-base\\README.md` | Entry point for platform-level OpenWrt work |
| Read OpenWrt appdev cheatsheet | `Get-Content ai_docs\\develop\\features\\openwrt24-app-development-knowledge-base\\06-cheatsheet.md` | Fastest path for OpenWrt app/package work |
| Read OpenWrt appdev KB index | `Get-Content ai_docs\\develop\\features\\openwrt24-app-development-knowledge-base\\README.md` | Entry point for OpenWrt application lifecycle |
| Read OpenWrt appdev machine index | `Get-Content ai_docs\\develop\\features\\openwrt24-app-development-knowledge-base\\openwrt24-appdev-agent-index.json` | Structured quick lookup for app/package tasks |
| Read OpenWrt router intake template | `Get-Content ai_docs\\develop\\features\\openwrt24-console-knowledge-base\\07-router-intake-template.md` | Use before changes on a live OpenWrt router |
| Read Filogic recovery/write-safety guide | `Get-Content ai_docs\\develop\\features\\openwrt24-console-knowledge-base\\08-filogic-recovery-write-safety.md` | Mandatory before live writes, sysupgrade, or recovery planning on Filogic |
| Read OpenWrt machine index | `Get-Content ai_docs\\develop\\features\\openwrt24-console-knowledge-base\\openwrt24-agent-index.json` | Structured quick lookup for generic OpenWrt tasks |
| Read ops cheatsheet | `Get-Content ai_docs\\develop\\features\\passwall2-ops-cheatsheet.md` | Fastest path for common tasks |
| Read deep knowledge base | `Get-Content ai_docs\\develop\\features\\passwall2-openwrt24-knowledge-base.md` | Full PassWall2/OpenWrt 24.xx reference |
| Read router intake template | `Get-Content ai_docs\\develop\\features\\passwall2-router-intake-template.md` | Use before package/compatibility decisions on a real router |
| Read machine index | `Get-Content ai_docs\\develop\\features\\passwall2-agent-index.json` | Structured quick lookup |
| Read live AX3000T router KB | `Get-Content ai_docs\\develop\\features\\router-xiaomi-ax3000t-live-kb.md` | First stop for this exact router |
| Read safe test harness KB | `Get-Content ai_docs\\develop\\features\\router-ax3000t-safe-test-harness.md` | First stop before tmp-based live app tests |
| Resolve router update plan from pasted facts | `powershell -ExecutionPolicy Bypass -File .\\scripts\\Resolve-Passwall2RouterPlan.ps1 -InputFile .\\scripts\\fixtures\\xiaomi-ax3000t-openwrt24.txt` | Primary fast path for package manager, arch, and asset decisions |
| Collect live router inventory safely | `powershell -ExecutionPolicy Bypass -File .\\scripts\\Get-OpenWrtRouterInventory.ps1 -RouterHost <ip> -RouterUser <user> -RouterPassword <password> -RouterHostKey <fingerprint> -IncludePasswallPlan` | Read-only live snapshot with pinned host key |
| Run safe tmp test harness | `powershell -ExecutionPolicy Bypass -File .\\scripts\\Manage-OpenWrtTmpProgramSession.ps1 -Action baseline -RouterHost <ip> -RouterUser <user> -RouterPassword <password> -RouterHostKey <fingerprint>` | Default live app-testing lane before packaging |
| Inspect upstream tree | `Get-ChildItem -Force passwall2` | Upstream mirror lives in `passwall2/` |
| Check service entrypoints | `Get-Content passwall2\\luci-app-passwall2\\root\\etc\\init.d\\passwall2` | Main service wrapper |
| Check runtime logic | `Get-Content passwall2\\luci-app-passwall2\\root\\usr\\share\\passwall2\\app.sh` | Main execution path |
| Check update logic | `Get-Content passwall2\\luci-app-passwall2\\root\\usr\\share\\passwall2\\rule_update.lua` | Geo rules updater |
| Check subscription logic | `Get-Content passwall2\\luci-app-passwall2\\root\\usr\\share\\passwall2\\subscribe.lua` | Node subscriptions |
| Check component updater | `Get-Content passwall2\\luci-app-passwall2\\luasrc\\passwall2\\api.lua` | Binary updater and self-check |
| Resolve current release assets | `powershell -ExecutionPolicy Bypass -File .\\scripts\\Get-Passwall2ReleaseAssets.ps1 -App passwall2 -Arch aarch64_cortex-a53 -PackageManager opkg` | Prefer this over ad hoc release scraping |
| Latest PassWall2 release metadata | `Invoke-RestMethod -Headers @{ 'User-Agent'='Codex' } https://api.github.com/repos/Openwrt-Passwall/openwrt-passwall2/releases/latest` | Use when version-sensitive |

## File Map

```text
./
|- AGENTS.md                                      -> global agent rules for this workspace
|- RTK.md                                         -> Codex runbook for PassWall2/OpenWrt work
|- ai_docs/develop/features/                      -> curated internal docs
|  |- openwrt24-app-development-knowledge-base/   -> OpenWrt 24.xx app/package development KB
|  |- openwrt24-console-knowledge-base/           -> generic OpenWrt 24.xx platform KB for CLI work
|  |  \- 08-filogic-recovery-write-safety.md      -> Filogic recovery, sysupgrade and write-safety runbook
|  |- passwall2-ops-cheatsheet.md                 -> shortest operational reference
|  |- passwall2-openwrt24-knowledge-base.md       -> deep knowledge base
|  |- router-ax3000t-safe-test-harness.md         -> tmp-based live app testing protocol
|  |- router-xiaomi-ax3000t-live-kb.md            -> live router profile and safety runbook
|  \- snapshots/                                  -> dated live-router captures and plans
|- openwrt-24.10-src/                             -> local sparse OpenWrt 24.10 primary-source mirror for sysupgrade/recovery work
|- procd-src/                                     -> local procd primary-source mirror for sysupgrade ubus flow
\- scripts/                                       -> local helper utilities
   |- Get-Passwall2ReleaseAssets.ps1              -> current release asset resolver
   |- Get-OpenWrtRouterInventory.ps1              -> read-only live OpenWrt inventory collector
   |- Manage-OpenWrtTmpProgramSession.ps1         -> guarded tmp staging/start/status/stop/cleanup harness
   |- Resolve-Passwall2RouterPlan.ps1             -> parse router facts and build a safe PassWall2 update plan
   \- fixtures/xiaomi-ax3000t-openwrt24.txt       -> realistic Filogic/Xiaomi AX3000T sample intake
\- passwall2/                                     -> local upstream mirror of Openwrt-Passwall/openwrt-passwall2
   |- AGENTS.md                                   -> scoped rules for source-tree work
   \- luci-app-passwall2/
      |- Makefile                                 -> package metadata and deps
      |- root/etc/init.d/                         -> init scripts
      |- root/usr/share/passwall2/                -> runtime shell/lua helpers
      \- luasrc/passwall2/                        -> Lua APIs, server app, component update logic
```

## Golden Samples

| For | Reference | Why |
|-----|-----------|-----|
| Service lifecycle | `passwall2/luci-app-passwall2/root/etc/init.d/passwall2` | Canonical start/stop/restart flow |
| Runtime orchestration | `passwall2/luci-app-passwall2/root/usr/share/passwall2/app.sh` | Source of truth for CLI behavior |
| Geo rules update | `passwall2/luci-app-passwall2/root/usr/share/passwall2/rule_update.lua` | Source of truth for geoip/geosite refresh |
| Subscription refresh | `passwall2/luci-app-passwall2/root/usr/share/passwall2/subscribe.lua` | Source of truth for subscription CLI |
| Binary component update | `passwall2/luci-app-passwall2/luasrc/passwall2/api.lua` and `passwall2/luci-app-passwall2/luasrc/passwall2/com.lua` | Source of truth for built-in updater |
| Default OpenWrt config | `passwall2/luci-app-passwall2/root/usr/share/passwall2/0_default_config` | Baseline UCI expectations |

## Utilities

| Need | Use | Location |
|------|-----|----------|
| UCI reads/helpers | `config_n_get`, `config_t_get`, `first_type`, `get_new_port` | `passwall2/luci-app-passwall2/root/usr/share/passwall2/utils.sh` |
| Collect live router facts safely | `Get-OpenWrtRouterInventory.ps1` | `scripts/Get-OpenWrtRouterInventory.ps1` |
| Run bounded tmp app tests on the live router | `Manage-OpenWrtTmpProgramSession.ps1` | `scripts/Manage-OpenWrtTmpProgramSession.ps1` |
| Turn pasted router output into an upgrade plan | `Resolve-Passwall2RouterPlan.ps1` | `scripts/Resolve-Passwall2RouterPlan.ps1` |
| Spawn/test local socks | `app.sh run_socks` | `passwall2/luci-app-passwall2/root/usr/share/passwall2/app.sh` |
| Test node reachability | `test.sh url_test_node <node_id>` | `passwall2/luci-app-passwall2/root/usr/share/passwall2/test.sh` |
| Refresh subscriptions | `subscribe.lua start ...` | `passwall2/luci-app-passwall2/root/usr/share/passwall2/subscribe.lua` |
| Refresh geo databases | `rule_update.lua log ...` | `passwall2/luci-app-passwall2/root/usr/share/passwall2/rule_update.lua` |
| Restart dnsmasq helper-side | `helper_dnsmasq.lua restart ...` | `passwall2/luci-app-passwall2/root/usr/share/passwall2/helper_dnsmasq.lua` |

## Heuristics

| When | Do |
|------|-----|
| User asks about generic OpenWrt console/platform behavior | Start from `openwrt24-console-knowledge-base` before narrowing into app-specific docs |
| User asks how to create, package or update an OpenWrt app | Start from `openwrt24-app-development-knowledge-base` before proposing implementation details |
| User asks "how to manage from console" | Verify in `init.d`, `app.sh`, `subscribe.lua`, `rule_update.lua`, `test.sh` |
| Docs and code disagree | Trust code, then note docs as stale |
| Question is version-sensitive | Check upstream release/API metadata before answering |
| Question is OpenWrt 24.xx packaging | Default to `ipk` + `opkg`, not `apk` |
| Question is router compatibility | Trust `DISTRIB_ARCH`/`OPENWRT_ARCH`, not marketing model names |
| Task is about the real Xiaomi AX3000T | Read `ai_docs/develop/features/router-xiaomi-ax3000t-live-kb.md` before proposing actions |
| Task is about firmware writes, sysupgrade, or recovery on Filogic | Read `ai_docs/develop/features/openwrt24-console-knowledge-base/08-filogic-recovery-write-safety.md` before proposing actions |
| Task is about testing a future custom program on the live AX3000T | Read `ai_docs/develop/features/router-ax3000t-safe-test-harness.md` and use `scripts/Manage-OpenWrtTmpProgramSession.ps1` before any package/service plan |
| User pasted router command output | Run `scripts/Resolve-Passwall2RouterPlan.ps1` before making package/update recommendations |
| Need fresh live facts from the router | Use `scripts/Get-OpenWrtRouterInventory.ps1` in read-only mode with a pinned host key |
| Unsure whether updater is package-based or file-replacement | Inspect `api.lua` and confirm whether it calls package manager or moves binaries directly |

## Boundaries

### Always Do

- Distinguish confirmed facts from inference.
- Cite source files when answering repository-specific questions.
- State clearly when something was verified in code versus not executed on a real router.
- Keep the upstream mirror in `passwall2/` readable and intact unless the task explicitly asks for source edits there.

### Ask First

- Editing upstream code under `passwall2/`
- Replacing binaries, archives, or release artifacts
- Large doc restructures outside the existing `ai_docs/` tree
- Deleting or overwriting prior research docs

### Never Do

- Claim that router-side runtime behavior was tested if it was only inferred from source
- Recommend `.apk` for OpenWrt 24.xx
- Treat the built-in PassWall2 component updater as the default upgrade path for OpenWrt 24.xx
- Rewrite or re-clone `passwall2/` unless explicitly requested

## Codebase State

- This workspace is primarily a research/runbook repository plus a local upstream mirror.
- There is no repo-native automated test harness for router runtime in this workspace.
- The authoritative operational knowledge currently lives in `RTK.md` and the deep KB under `ai_docs/`.
- Generic OpenWrt app/package development knowledge now lives in `ai_docs/develop/features/openwrt24-app-development-knowledge-base/`.
- Generic OpenWrt platform knowledge now lives in `ai_docs/develop/features/openwrt24-console-knowledge-base/`.
- The fastest operational reference now lives in `ai_docs/develop/features/passwall2-ops-cheatsheet.md`.
- Machine-readable navigation lives in `ai_docs/develop/features/passwall2-agent-index.json`.
- Future agent work should preserve the distinction between:
  - PassWall2 application updates
  - binary component updates
  - geo rules updates
  - subscription updates

## Terminology

| Term | Means |
|------|-------|
| PassWall2 | LuCI app plus runtime scripts in `luci-app-passwall2` |
| Component update | Updating binaries like `xray`, `sing-box`, `hysteria`, `geoview` |
| Rules update | Updating `geoip.dat` and `geosite.dat` |
| Subscription update | Re-importing proxy node links into UCI |
| OpenWrt 24.xx path | `opkg` + `.ipk` workflow |
| Filogic class | MediaTek MT798x routers such as Xiaomi AX3000T |

## Scoped Files

- `passwall2/AGENTS.md` -> mandatory when reading or editing upstream source tree
- `ai_docs/develop/features/openwrt24-app-development-knowledge-base/README.md` -> generic OpenWrt app/package development entry point
- `ai_docs/develop/features/openwrt24-app-development-knowledge-base/openwrt24-appdev-agent-index.json` -> structured lookup for app/package development tasks
- `ai_docs/develop/features/openwrt24-console-knowledge-base/README.md` -> generic OpenWrt 24.xx platform entry point
- `ai_docs/develop/features/openwrt24-console-knowledge-base/07-router-intake-template.md` -> mandatory before risky on-device platform changes
- `ai_docs/develop/features/openwrt24-console-knowledge-base/08-filogic-recovery-write-safety.md` -> mandatory before live writes, sysupgrade, or recovery planning on Filogic
- `ai_docs/develop/features/openwrt24-console-knowledge-base/openwrt24-agent-index.json` -> structured lookup for generic OpenWrt tasks
- `ai_docs/develop/features/passwall2-ops-cheatsheet.md` -> shortest operational path
- `ai_docs/develop/features/passwall2-router-intake-template.md` -> mandatory before on-device package recommendations
- `ai_docs/develop/features/passwall2-agent-index.json` -> structured lookup for automation
- `ai_docs/develop/features/router-ax3000t-safe-test-harness.md` -> authoritative tmp-based live test protocol for custom programs
- `ai_docs/develop/features/router-xiaomi-ax3000t-live-kb.md` -> authoritative live profile for this exact router
- `ai_docs/develop/features/snapshots/xiaomi-ax3000t-2026-04-04-inventory.txt` -> dated read-only router snapshot
- `ai_docs/develop/features/snapshots/xiaomi-ax3000t-2026-04-04-passwall-plan.json` -> machine-readable PassWall2 plan for the dated snapshot
- `scripts/Get-OpenWrtRouterInventory.ps1` -> default live inventory collector for future router sessions
- `scripts/Manage-OpenWrtTmpProgramSession.ps1` -> default tmp-based live testing harness for future programs
- `scripts/Resolve-Passwall2RouterPlan.ps1` -> default parser for real router facts into a recommended update path
