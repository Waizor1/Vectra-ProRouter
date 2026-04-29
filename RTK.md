# RTK.md

<!-- codex-global-rtk:start -->
## Global RTK shell-output layer

@/Users/waizor/.codex/RTK.md

Load `/Users/waizor/.codex/RTK.md` first for the current global RTK policy: prefer `rtk` for shell commands, keep terminal output bounded, and fall back to raw commands only when exact output or tool behavior requires it. Local notes below add project-specific routing and do not replace the global RTK rules.
<!-- codex-global-rtk:end -->


Purpose: fast Codex runbook for this workspace. Read this before any substantial PassWall2/OpenWrt task.

## 1. Workspace Role

This repository is not a live OpenWrt firmware tree.

It currently contains:

- internal docs and research under `ai_docs/`
- optional local upstream mirrors under `passwall2/`, `openwrt-24.10-src/`, and `procd-src/`

In the root Git history, the docs and helper scripts are tracked, while those source mirrors are expected local checkouts and may be absent in a fresh clone until they are hydrated.

Treat `passwall2/` as a source-of-truth code mirror for analysis unless the user explicitly asks for source changes.

## 2. Fast Start Checklist

1. Read `AGENTS.md`.
2. For any non-trivial task, read `ProRouter/Home.md`, `ProRouter/00 Dashboard/Agent Workflow.md`, `ProRouter/00 Dashboard/Stage Board.md`, and `ProRouter/00 Dashboard/Repo Map.md`.
3. Read `passwall2/AGENTS.md` if touching upstream code.
4. For generic OpenWrt 24.xx platform work, read `ai_docs/develop/features/openwrt24-console-knowledge-base/06-cheatsheet.md` first, then `ai_docs/develop/features/openwrt24-console-knowledge-base/README.md`.
5. For OpenWrt app/package development, read `ai_docs/develop/features/openwrt24-app-development-knowledge-base/06-cheatsheet.md` first, then `ai_docs/develop/features/openwrt24-app-development-knowledge-base/README.md`.
6. For a real OpenWrt router, collect facts with `ai_docs/develop/features/openwrt24-console-knowledge-base/07-router-intake-template.md`.
7. Read `ai_docs/develop/features/passwall2-ops-cheatsheet.md` for the shortest PassWall2 task path.
8. Read `ai_docs/develop/features/passwall2-openwrt24-knowledge-base.md` if the task is PassWall2 operational, packaging, update-related, or router-specific.
9. If the task is about the real Xiaomi AX3000T already analyzed in this workspace, read `ai_docs/develop/features/router-xiaomi-ax3000t-live-kb.md`.
10. If the task is about testing a future custom program on the live AX3000T, read `ai_docs/develop/features/router-ax3000t-safe-test-harness.md`.
11. If the task involves live writes, recovery, `sysupgrade`, or firmware safety on Filogic, read `ai_docs/develop/features/openwrt24-console-knowledge-base/08-filogic-recovery-write-safety.md`.
12. Use `ai_docs/develop/features/passwall2-agent-index.json`, `ai_docs/develop/features/openwrt24-console-knowledge-base/openwrt24-agent-index.json`, or `ai_docs/develop/features/openwrt24-app-development-knowledge-base/openwrt24-appdev-agent-index.json` if you want structured lookup.
13. If the user wants fresh live router facts, run `scripts/Get-OpenWrtRouterInventory.py` in read-only mode first.
14. If the user wants bounded live tmp tests for a custom program, use `scripts/Manage-OpenWrtTmpProgramSession.py` before any package/service plan.
15. If the user pasted PassWall2 router output, run `scripts/Resolve-Passwall2RouterPlan.py` first.
16. Before final response, append a completion update with `scripts/Add-ProRouterStatusEntry.py`.
17. Run `scripts/Sync-ProRouterVault.py` after structural repo changes so the generated Obsidian repo map stays current.
18. For sysupgrade/recovery answers, prefer the local OpenWrt source mirrors in `openwrt-24.10-src/` and `procd-src/` over forum summaries.
19. For version-sensitive questions, re-check current upstream releases before answering.
20. When answering, separate:
   - confirmed from code
   - confirmed from upstream release metadata
   - operational inference

## 3. What Matters Most

### Core source files

- Main client service wrapper:
  `passwall2/luci-app-passwall2/root/etc/init.d/passwall2`
- Main client runtime:
  `passwall2/luci-app-passwall2/root/usr/share/passwall2/app.sh`
- Shared shell helpers:
  `passwall2/luci-app-passwall2/root/usr/share/passwall2/utils.sh`
- Geo rules updater:
  `passwall2/luci-app-passwall2/root/usr/share/passwall2/rule_update.lua`
- Subscription updater:
  `passwall2/luci-app-passwall2/root/usr/share/passwall2/subscribe.lua`
- Connectivity test helper:
  `passwall2/luci-app-passwall2/root/usr/share/passwall2/test.sh`
- Socks auto failover:
  `passwall2/luci-app-passwall2/root/usr/share/passwall2/socks_auto_switch.sh`
- Firewall low-level helpers:
  `passwall2/luci-app-passwall2/root/usr/share/passwall2/iptables.sh`
  `passwall2/luci-app-passwall2/root/usr/share/passwall2/nftables.sh`
- Server-side runtime:
  `passwall2/luci-app-passwall2/luasrc/passwall2/server_app.lua`
- Built-in component updater:
  `passwall2/luci-app-passwall2/luasrc/passwall2/api.lua`
  `passwall2/luci-app-passwall2/luasrc/passwall2/com.lua`
- Package dependencies and defaults:
  `passwall2/luci-app-passwall2/Makefile`
  `passwall2/luci-app-passwall2/root/usr/share/passwall2/0_default_config`

### Primary UCI configs

- `/etc/config/passwall2`
- `/etc/config/passwall2_server`

### Fast reference docs

- `ProRouter/Home.md`
- `ProRouter/00 Dashboard/Agent Workflow.md`
- `ProRouter/00 Dashboard/Stage Board.md`
- `ProRouter/00 Dashboard/Repo Map.md`
- `ai_docs/develop/features/openwrt24-app-development-knowledge-base/README.md`
- `ai_docs/develop/features/openwrt24-app-development-knowledge-base/06-cheatsheet.md`
- `ai_docs/develop/features/openwrt24-app-development-knowledge-base/openwrt24-appdev-agent-index.json`
- `ai_docs/develop/features/openwrt24-console-knowledge-base/README.md`
- `ai_docs/develop/features/openwrt24-console-knowledge-base/06-cheatsheet.md`
- `ai_docs/develop/features/openwrt24-console-knowledge-base/07-router-intake-template.md`
- `ai_docs/develop/features/openwrt24-console-knowledge-base/08-filogic-recovery-write-safety.md`
- `ai_docs/develop/features/openwrt24-console-knowledge-base/openwrt24-agent-index.json`
- `ai_docs/develop/features/passwall2-ops-cheatsheet.md`
- `ai_docs/develop/features/passwall2-router-intake-template.md`
- `ai_docs/develop/features/passwall2-agent-index.json`

### Local helper scripts

- `scripts/Add-ProRouterStatusEntry.py`
- `scripts/Sync-ProRouterVault.py`
- `scripts/Get-Passwall2ReleaseAssets.py`
- `scripts/Get-OpenWrtRouterInventory.py`
- `scripts/Manage-OpenWrtTmpProgramSession.py`
- `scripts/Resolve-Passwall2RouterPlan.py`
- `openwrt-24.10-src/`
- `procd-src/`

## 4. Operational Truths Already Verified

### Service control

Supported and normal:

```sh
/etc/init.d/passwall2 start|stop|restart|reload|enable|disable
/etc/init.d/passwall2_server start|stop|restart|reload|enable|disable
```

Important:

- `reload` is not a soft reload. In code it falls back to full `restart`.

### Major CLI helpers

Supported entrypoints:

```sh
lua /usr/share/passwall2/subscribe.lua start ...
lua /usr/share/passwall2/subscribe.lua truncate ...
lua /usr/share/passwall2/rule_update.lua log ...
/usr/share/passwall2/test.sh url_test_node <node_id>
/usr/share/passwall2/app.sh run_socks ...
/usr/share/passwall2/app.sh socks_node_switch ...
```

### OpenWrt 24.xx packaging

Default answer for OpenWrt 24.xx:

- package manager: `opkg`
- package format: `.ipk`

Do not recommend `.apk` unless the target is OpenWrt `25.12+`.

### Built-in updater caveat

The built-in component updater in `api.lua` downloads/extracts binaries and moves them into place.

It does not behave like a normal package-manager transaction.

For OpenWrt 24.xx, default recommendation is:

- update app and components with `opkg`/`.ipk`
- use the built-in binary updater only as a fallback/manual override

### Local release utility

Use the local script instead of redoing ad hoc GitHub release filtering:

```bash
python3 ./scripts/Get-Passwall2ReleaseAssets.py --app passwall2 --arch aarch64_cortex-a53 --package-manager opkg
```

For end-to-end router planning, use:

```bash
python3 ./scripts/Resolve-Passwall2RouterPlan.py --input-file ./scripts/fixtures/xiaomi-ax3000t-openwrt24.txt --as-json
```

## 5. Router Compatibility Rule

When the user names a router model, do not trust the model name alone. Verify or ask for:

```sh
ubus call system board
grep -E 'DISTRIB_(RELEASE|ARCH)' /etc/openwrt_release
opkg print-architecture
uname -m
```

If you are collecting real router facts for a future change or upgrade, use:

- `ai_docs/develop/features/passwall2-router-intake-template.md`

Use `DISTRIB_ARCH` or `OPENWRT_ARCH` as the package-selection key.

For Xiaomi AX3000T, the relevant architecture is `aarch64_cortex-a53`.

When the user gives you pasted router facts instead of a live shell, prefer:

```bash
python3 ./scripts/Resolve-Passwall2RouterPlan.py --input-file ./scripts/fixtures/xiaomi-ax3000t-openwrt24.txt
```

The planner can also read pasted raw text:

```bash
cat ./router-output.txt | python3 ./scripts/Resolve-Passwall2RouterPlan.py --raw-text "$(cat)"
```

## 6. How to Answer Common Tasks

### "How do I manage PassWall2 from console?"

Check, in order:

1. `root/etc/init.d/passwall2`
2. `root/usr/share/passwall2/app.sh`
3. `root/usr/share/passwall2/subscribe.lua`
4. `root/usr/share/passwall2/rule_update.lua`
5. `root/usr/share/passwall2/test.sh`

### "How do I update rules?"

Check `rule_update.lua`.

Expected command family:

```sh
lua /usr/share/passwall2/rule_update.lua log
lua /usr/share/passwall2/rule_update.lua log geoip
lua /usr/share/passwall2/rule_update.lua log geosite
lua /usr/share/passwall2/rule_update.lua log geoip,geosite
```

### "How do I update subscriptions?"

Check `subscribe.lua`.

Expected command family:

```sh
lua /usr/share/passwall2/subscribe.lua start <cfgid>
lua /usr/share/passwall2/subscribe.lua start all
lua /usr/share/passwall2/subscribe.lua truncate
```

### "How do I update the program itself?"

Check:

- `api.to_check_self()` in `api.lua`
- latest upstream release metadata

Expected conclusion:

- self-check exists
- automatic app update is not supported
- app update is manual via package install

### "How do I update xray/sing-box/hysteria/geoview?"

Check:

- `com.lua`
- `api.lua::to_check`, `to_download`, `to_extract`, `to_move`
- release metadata and package assets

Expected default answer for OpenWrt 24.xx:

- prefer `opkg` with `.ipk` packages

## 7. When Source and Docs Conflict

Use this order of trust:

1. source code
2. release metadata
3. README
4. prior notes/docs

If docs are stale, say so explicitly.

## 8. Safety and Communication Rules

- Do not claim router runtime validation unless commands were actually run on a router.
- Distinguish "confirmed from source" from "recommended operationally".
- When the user asks for current/latest versions, re-check upstream first.
- If a task would change upstream source behavior, update the relevant docs after editing.
- If a task materially changes repo structure or project status, update `ProRouter/` notes, append a completion entry, and refresh the generated repo map.
- If forked or parallel agents are used, the main agent is responsible for consolidating their outputs into `ProRouter/`.

## 9. Preferred Documentation Targets

Use these files as the main documentation surfaces:

- quick agent policy: `AGENTS.md`
- deep runbook for agents: `RTK.md`
- project memory hub: `ProRouter/Home.md`
- project workflow: `ProRouter/00 Dashboard/Agent Workflow.md`
- readiness view: `ProRouter/00 Dashboard/Stage Board.md`
- generated repo structure: `ProRouter/00 Dashboard/Repo Map.md`
- live Obsidian Bases views: `ProRouter/00 Dashboard/*.base`
- per-module state: `ProRouter/02 Modules/`
- decisions: `ProRouter/03 Decisions/`
- dated session notes: `ProRouter/04 Sessions/Daily/`
- generic OpenWrt appdev entry point: `ai_docs/develop/features/openwrt24-app-development-knowledge-base/README.md`
- generic OpenWrt appdev cheatsheet: `ai_docs/develop/features/openwrt24-app-development-knowledge-base/06-cheatsheet.md`
- generic OpenWrt appdev machine-readable lookup: `ai_docs/develop/features/openwrt24-app-development-knowledge-base/openwrt24-appdev-agent-index.json`
- generic OpenWrt entry point: `ai_docs/develop/features/openwrt24-console-knowledge-base/README.md`
- generic OpenWrt cheatsheet: `ai_docs/develop/features/openwrt24-console-knowledge-base/06-cheatsheet.md`
- generic OpenWrt router intake template: `ai_docs/develop/features/openwrt24-console-knowledge-base/07-router-intake-template.md`
- generic OpenWrt machine-readable lookup: `ai_docs/develop/features/openwrt24-console-knowledge-base/openwrt24-agent-index.json`
- shortest operations path: `ai_docs/develop/features/passwall2-ops-cheatsheet.md`
- deep operational KB: `ai_docs/develop/features/passwall2-openwrt24-knowledge-base.md`
- real-router intake template: `ai_docs/develop/features/passwall2-router-intake-template.md`
- machine-readable lookup: `ai_docs/develop/features/passwall2-agent-index.json`
- live tmp test harness KB: `ai_docs/develop/features/router-ax3000t-safe-test-harness.md`
- live router KB: `ai_docs/develop/features/router-xiaomi-ax3000t-live-kb.md`
- dated live snapshot: `ai_docs/develop/features/snapshots/xiaomi-ax3000t-2026-04-04-inventory.txt`
- dated live PassWall2 plan: `ai_docs/develop/features/snapshots/xiaomi-ax3000t-2026-04-04-passwall-plan.json`
- live inventory collector: `scripts/Get-OpenWrtRouterInventory.py`
- tmp test harness: `scripts/Manage-OpenWrtTmpProgramSession.py`
- router-facts planner: `scripts/Resolve-Passwall2RouterPlan.py`
- completion status logger: `scripts/Add-ProRouterStatusEntry.py`
- vault structure sync: `scripts/Sync-ProRouterVault.py`

If you create new docs, keep them under `ai_docs/` unless the task is specifically about agent policy, in which case update `AGENTS.md`/`RTK.md`.
