# PassWall2 Router Intake Template

Purpose: collect the minimum facts from a real router before recommending packages, update paths, or compatibility conclusions.

Status: fill this before selecting PassWall2 release artifacts.

After collecting output, feed the pasted text to:

```bash
python3 ./scripts/Resolve-Passwall2RouterPlan.py --input-file ./router-output.txt
```

## 1. Target Router

- Router model:
- SoC:
- CPU family:
- OpenWrt release:
- Package manager:
- `DISTRIB_ARCH`:
- `OPENWRT_ARCH`:
- Current PassWall2 version:
- Current `xray` version:
- Current `sing-box` version:
- Current `hysteria` version:

## 2. Copy-Paste Collection Commands

Run on the router:

```sh
ubus call system board
echo '--- openwrt_release ---'
grep -E 'DISTRIB_(ID|RELEASE|ARCH|TARGET|DESCRIPTION)' /etc/openwrt_release
echo '--- os-release ---'
grep -E 'OPENWRT_ARCH|NAME|VERSION' /usr/lib/os-release 2>/dev/null
echo '--- package manager ---'
opkg --version 2>/dev/null || true
apk --version 2>/dev/null || true
echo '--- architectures ---'
opkg print-architecture 2>/dev/null || true
uname -m
echo '--- passwall status ---'
uci get passwall2.@global[0].enabled 2>/dev/null || true
uci get passwall2.@global[0].node 2>/dev/null || true
echo '--- installed packages ---'
opkg list-installed 2>/dev/null | grep -E 'passwall|xray|sing-box|hysteria|geoview|v2ray-geo' || true
apk list -I 2>/dev/null | grep -E 'passwall|xray|sing-box|hysteria|geoview|v2ray-geo' || true
echo '--- binary versions ---'
xray version 2>/dev/null | head -n 1 || true
sing-box version 2>/dev/null | head -n 1 || true
hysteria version 2>/dev/null | head -n 3 || true
geoview -version 2>/dev/null | head -n 1 || true
echo '--- firewall/dnsmasq ---'
fw4 -V 2>/dev/null || true
dnsmasq -v 2>/dev/null || true
```

## 3. Environment Facts

- `fw4` present:
- `dnsmasq` has `nftset`:
- `dnsmasq` has `ipset`:
- Current transparent proxy mode expected:
- IPv6 proxy needed:
- Storage free space:
- RAM class:

## 4. Change Goal

- Need app update only:
- Need component update:
- Need geo rules refresh:
- Need subscription refresh:
- Need bug triage only:

## 5. Decision Result

- Recommended package manager path:
- Recommended app artifact:
- Recommended architecture bundle:
- Built-in component updater allowed:
- Need full backup before work:
- Need maintenance window:

## 6. Notes

- Do not finalize package recommendations until `DISTRIB_ARCH` is captured.
- For OpenWrt `24.xx`, default recommendation remains `opkg` plus `.ipk`.
- For OpenWrt `25.12+`, switch recommendation to `apk` plus `.apk`.
- Prefer the router planner script over manual asset guessing once the pasted output is available.
