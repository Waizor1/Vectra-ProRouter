# PassWall2 Ops Cheatsheet for Codex

Purpose: shortest operational path for future agents. Use this when you need commands and decisions fast, without reopening the full knowledge base.

## 1. Hard Defaults

- OpenWrt `24.xx` => use `opkg` and `.ipk`
- OpenWrt `25.12+` => use `apk` and `.apk`
- For PassWall2 app updates on `24.xx`, default to manual package install
- For component updates on `24.xx`, prefer package-based updates over the built-in binary mover
- Do not trust router marketing model names alone; trust `DISTRIB_ARCH` / `OPENWRT_ARCH`

## 2. First Commands to Run

### If the question is about repository behavior

```powershell
Get-Content passwall2\luci-app-passwall2\root\etc\init.d\passwall2
Get-Content passwall2\luci-app-passwall2\root\usr\share\passwall2\app.sh
Get-Content passwall2\luci-app-passwall2\root\usr\share\passwall2\rule_update.lua
Get-Content passwall2\luci-app-passwall2\root\usr\share\passwall2\subscribe.lua
Get-Content passwall2\luci-app-passwall2\luasrc\passwall2\api.lua
```

### If the question is about a real router

```sh
ubus call system board
grep -E 'DISTRIB_(RELEASE|ARCH)' /etc/openwrt_release
opkg print-architecture
uname -m
uci get passwall2.@global[0].enabled
uci get passwall2.@global[0].node
```

### If the user pasted router command output into the workspace

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\Resolve-Passwall2RouterPlan.ps1 -InputFile .\scripts\fixtures\xiaomi-ax3000t-openwrt24.txt
```

JSON output for automation:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\Resolve-Passwall2RouterPlan.ps1 -InputFile .\scripts\fixtures\xiaomi-ax3000t-openwrt24.txt -AsJson
```

### If the agent can reach the real router from this workstation

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\Get-OpenWrtRouterInventory.ps1 -RouterHost <ip> -RouterUser <user> -RouterPassword <password> -RouterHostKey <fingerprint> -IncludePasswallPlan
```

Use this before any live-router recommendation. It is designed for read-only collection only.

## 3. Common Tasks

### Restart service

```sh
/etc/init.d/passwall2 restart
/etc/init.d/passwall2_server restart
```

### Update all subscriptions

```sh
lua /usr/share/passwall2/subscribe.lua start all
```

### Update geo databases

```sh
lua /usr/share/passwall2/rule_update.lua log geoip,geosite
```

### Test one node

```sh
/usr/share/passwall2/test.sh url_test_node <node_id>
```

### Switch main node

```sh
uci set passwall2.@global[0].node='<node_id>'
uci commit passwall2
/etc/init.d/passwall2 restart
```

### List nodes

```sh
uci show passwall2 | grep '=nodes'
```

### List subscriptions

```sh
uci show passwall2 | grep '=subscribe_list'
```

### Check logs

```sh
logread | grep -i passwall2
tail -f /tmp/log/passwall2.log
tail -f /tmp/log/passwall2_server.log
```

## 4. Update Decision Matrix

| Need | Use | Do not confuse with |
|------|-----|---------------------|
| New PassWall2 app version | install new `luci-app-passwall2` package | component updater |
| New `xray` / `sing-box` / `hysteria` / `geoview` | package update on `24.xx` | `rule_update.lua` |
| New `geoip.dat` / `geosite.dat` | `rule_update.lua` | app update |
| New node links | `subscribe.lua` | geo update |

## 5. Router Intake Rule

Before selecting release assets, fill the intake template:

- [passwall2-router-intake-template.md](passwall2-router-intake-template.md)

Do not select `ipk`/`apk` or architecture bundles before collecting:

- OpenWrt release
- package manager
- `DISTRIB_ARCH`
- router model / SoC

## 6. Release Asset Lookup

Use the local helper script for current release assets:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\Get-Passwall2ReleaseAssets.ps1 -App passwall2 -Arch aarch64_cortex-a53 -PackageManager opkg
```

JSON output mode:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\Get-Passwall2ReleaseAssets.ps1 -App passwall2 -Arch aarch64_cortex-a53 -PackageManager opkg -AsJson
```

For a full decision from pasted router facts, prefer the planner:

```powershell
Get-Content .\router-output.txt | powershell -ExecutionPolicy Bypass -File .\scripts\Resolve-Passwall2RouterPlan.ps1
```

## 7. Xiaomi AX3000T Shortcut

If the router is confirmed as Xiaomi AX3000T on OpenWrt `24.xx`, default expectation is:

- architecture: `aarch64_cortex-a53`
- package manager: `opkg`
- app package: `luci-app-passwall2_*.ipk`
- component bundle: `passwall_packages_ipk_aarch64_cortex-a53.zip`

Still verify on-device before final recommendation.

## 8. Live-Router Safety

Before any live write on AX3000T or a similar Filogic router, read:

- [08-filogic-recovery-write-safety.md](openwrt24-console-knowledge-base/08-filogic-recovery-write-safety.md)

Hard defaults for this router class:

- stock-layout AX3000T uses `*-squashfs-sysupgrade.bin`
- do not use `ubootmod` `.itb`, `preloader.bin`, or `bl31-uboot.fip` on a stock-layout router
- do not treat `sysupgrade -s` as the main safety mechanism unless verified on the exact board/layout

## 9. Canonical Docs

- Fast agent rules: [AGENTS.md](../../../AGENTS.md)
- Runbook: [RTK.md](../../../RTK.md)
- Deep reference: [passwall2-openwrt24-knowledge-base.md](passwall2-openwrt24-knowledge-base.md)
- Recovery/write-safety: [08-filogic-recovery-write-safety.md](openwrt24-console-knowledge-base/08-filogic-recovery-write-safety.md)
- Live router KB: [router-xiaomi-ax3000t-live-kb.md](router-xiaomi-ax3000t-live-kb.md)
- Live inventory collector: [Get-OpenWrtRouterInventory.ps1](../../../scripts/Get-OpenWrtRouterInventory.ps1)
- Router planner: [Resolve-Passwall2RouterPlan.ps1](../../../scripts/Resolve-Passwall2RouterPlan.ps1)
