# Xiaomi AX3000T Live Router KB

Purpose: reusable live-router knowledge base for this exact device, collected in read-only mode on `2026-04-04`.

Scope: this document is about the real router reached at the usual LAN management address, not the generic upstream PassWall2 source mirror.

Status: the base profile below was collected read-only on `2026-04-04`. Additional live updates from `2026-04-07` are appended in section 14 after real package reinstall, approval recovery, and backup-surface verification.

## 1. Access Profile

- Access method used successfully: `plink` over SSH
- Auth model observed: password login for `root`
- Verified host key fingerprint: `ED25519 SHA256:Wqvfq+vI35+zyrO6VMhYLY6ZbHlqgdbbI0+K9Bubad8`
- Safe connection rule for future agents: pin the host key and use batch mode; do not accept host keys interactively
- Secret handling rule: do not write the router password into repo files, scripts, or Sugar memory

## 2. Verified Live Facts

- Model: `Xiaomi Mi Router AX3000T`
- Board name: `xiaomi,mi-router-ax3000t`
- OpenWrt release: `24.10.4`
- Revision: `r28959-29397011cc`
- Kernel: `6.6.110`
- Target: `mediatek/filogic`
- Package architecture: `aarch64_cortex-a53`
- Userland machine arch: `aarch64`
- Root filesystem: `squashfs` with writable `ubifs` overlay
- Package manager on this router: `opkg`

Source: live `ubus call system board`, `/etc/openwrt_release`, `/usr/lib/os-release`, `opkg --version`, `opkg print-architecture`, `uname -m`.

## 3. PassWall2 State

- `luci-app-passwall2` installed: `26.3.5-r1`
- PassWall2 global enable flag: `1`
- Current selected node id: `myshunt`
- Detected node count: `15`
- Detected subscription count: `1`
- Active PassWall-related runtime observed:
  - `chinadns-ng`
  - `xray`
  - PassWall-managed `dnsmasq`
  - `lease2hosts.sh`
  - `monitor.sh`

Safe interpretation: PassWall2 is active on this router right now. Any future write action touching networking, DNS, firewall, or proxy binaries is production-sensitive.

## 4. Component Drift Risk Already Present

Verified mismatch between package database and live binaries:

- `opkg` package `xray-core` is `25.12.1-r1`, but `xray version` reports `Xray 26.2.6`
- `opkg` package `geoview` is `0.1.11-r1`, but `geoview -version` reports `Geoview 0.2.5`

Implication:

- This router already shows evidence that component binaries may have been updated outside a normal package transaction.
- Future agents must not assume `opkg list-installed` is the source of truth for component runtime versions.
- For component verification on this router, always record both:
  - package database version
  - binary self-reported version

This directly supports the earlier repository-level conclusion that the built-in PassWall2 component updater can desynchronize package state from runtime binaries.

## 5. Resource Envelope for Future Testing

- RAM total: about `234 MB`
- RAM available at collection time: about `67 MB`
- Swap present: about `117 MB`, unused at collection time
- Persistent overlay free space: about `23 MB`
- `/tmp` free space: about `115 MB`

Operational meaning:

- Persistent space is limited. Do not use `/overlay` for first-pass experiments unless persistence is explicitly required.
- First test path for new binaries or scripts should be `/tmp`, because it is volatile and clears on reboot.
- A reboot is therefore a safe cleanup boundary for many future experiments, provided no persistent config changes were committed.

## 6. Layout and Recovery Facts

Verified on-router evidence:

- `/sbin/sysupgrade` exists
- `sysupgrade -h` on this router exposes `-s`, described as staying on the current partition for dual-firmware devices
- `fw_printenv` is present
- Boot environment includes:
  - `bootmenu_1=Startup firmware0`
  - `bootmenu_2=Startup firmware1`
  - `flag_boot_rootfs=0`
  - `flag_boot_success=1`
  - `flag_last_success=0`
- Kernel cmdline currently includes `firmware=0`

Working conclusion:

- This device is operating with a stock Xiaomi dual-firmware style boot environment.
- Current active firmware slot at collection time is `firmware=0`.
- Current board identity is stock layout, not OpenWrt U-Boot layout:
  - live board: `xiaomi,mi-router-ax3000t`
  - not `xiaomi,mi-router-ax3000t-ubootmod`

## 7. Image Selection Rule For This Exact Router

For this router in its current state, the default safe image family is the stock-layout AX3000T build, not the `ubootmod` build.

Use this naming family for future OpenWrt sysupgrade planning:

- `openwrt-24.10.x-mediatek-filogic-xiaomi_mi-router-ax3000t-squashfs-sysupgrade.bin`

Do not use on this router unless there is an explicit bootloader/layout migration plan:

- `xiaomi_mi-router-ax3000t-ubootmod-*`
- `.itb` sysupgrade artifacts intended for the OpenWrt U-Boot layout
- bootloader write artifacts such as `preloader.bin` or `bl31-uboot.fip`

Why:

- OpenWrt’s AX3000T support adds separate stock-layout and `ubootmod` target definitions, including different artifact families for each layout. The upstream support patch explicitly defines both `xiaomi_mi-router-ax3000t` and `xiaomi_mi-router-ax3000t-ubootmod` as distinct devices, and the download directory for `24.10.4` contains both stock-layout and `ubootmod` images. See sources below.

## 8. Safe Operating Model

### Allowed by default

- Read-only SSH inventory
- Checking package versions
- Checking binary self-reported versions
- Listing processes
- Inspecting `fw_printenv`, partitions, mounts, memory, and overlay usage
- Generating upgrade plans locally from collected output
- Copying future test binaries into `/tmp` only, without replacing system binaries
- Running future test binaries manually from `/tmp` on non-conflicting ports, if they do not touch routing/firewall/DNS

### Require explicit permission first

- `uci set`, `uci commit`, or any config write
- `opkg install`, `opkg upgrade`, or `opkg remove`
- PassWall2 restarts, reloads, or node switches
- `sysupgrade`, `mtd`, `ubiformat`, `ubiupdatevol`, `firstboot`, `jffs2reset`
- Writing bootloader environment with `fw_setenv` or `nvram set`
- Replacing binaries under `/usr/bin`, `/usr/share/passwall2`, or `/etc/init.d`
- Any test that changes firewall, DNS, default route, or Wi-Fi

### Hard no without recovery prep

- Bootloader migration to `ubootmod`
- Flashing `preloader.bin` or `bl31-uboot.fip`
- Flashing images for the wrong layout
- Installing network stack or kernel-module packages over remote-only access
- Any operation that would leave the router needing a reboot before verification

## 9. Safety Protocol For Future Development

Before any change that is not read-only:

1. Confirm wired access is used, not Wi-Fi management through the same router being changed.
2. Confirm current slot and boot flags again with:
   - `cat /proc/cmdline`
   - `fw_printenv | grep -E 'flag_|bootmenu|boot_fw|bootcmd'`
3. Capture a fresh inventory and save it locally.
4. Capture backups before writes are allowed:
   - `sysupgrade -l` to inspect backup scope
   - full config backup only after explicit permission
5. If touching PassWall2 components, compare:
   - `opkg list-installed`
   - binary self-reported versions
6. If touching firewall behavior, render and validate before reload:
   - `fw4 print`
   - `fw4 check`
7. If testing a future custom program, prefer this order:
   - run from `/tmp`
   - use a high unused port
   - do not autostart on boot
   - do not replace any package-owned file

## 10. Recovery Thinking

Verified locally:

- The boot environment exposes dual firmware startup entries.
- The router still has stock-style boot variables and boot menu entries.
- `sysupgrade -l` on this unit includes:
  - `/etc/config/passwall2`
  - `/etc/config/passwall2_server`
  - `/etc/config/network`
  - `/etc/config/firewall`
  - `/etc/config/wireless`
- `/etc/config/uhttpd`
- `/etc/dropbear/*`

Officially documented, but not executed on this unit:

- The OpenWrt AX3000T device page now documents:
  - stock-bootloader TFTP recovery behavior
  - UART recovery path for stock bootloader installs
  - `ubootmod` recovery volume behavior when that layout is used
- The official OpenWrt failsafe docs confirm:
  - `24.10+` uses a four-second button window
  - failsafe comes up on `192.168.1.1`
  - DHCP and Wi-Fi are disabled in failsafe
  - `mount_root` is the command to recover overlay-backed settings

Conservative rule for this router:

- Treat TFTP/stock-bootloader recovery as a possible fallback, not as a permission slip to take risky actions remotely.
- Do not assume recovery is guaranteed until we have independently staged and documented the exact recovery files and a verified procedure for this unit.
- Use the dedicated write-safety and recovery runbook before any live write:
  - [08-filogic-recovery-write-safety.md](openwrt24-console-knowledge-base/08-filogic-recovery-write-safety.md)

## 11. Fast Commands For Future Agents

### Local read-only inventory from this workstation

Use the local helper script. Prefer native OpenSSH on macOS/Linux and keep secrets out of tracked files:

```bash
python3 ./scripts/Get-OpenWrtRouterInventory.py \
  --router-host '<router-ip>' \
  --router-user '<ssh-user>' \
  --transport OpenSSH \
  --openssh-known-hosts-file ./router-known_hosts \
  --openssh-identity-file ~/.ssh/id_ed25519 \
  --include-passwall-plan
```

PuTTY password fallback remains supported with `--router-password` and `--router-host-key`.

### Planning PassWall2 updates from saved inventory

```bash
python3 ./scripts/Resolve-Passwall2RouterPlan.py \
  --input-file ./path/to/saved-router-inventory.txt
```

### Local tmp-based custom-program test lane

Read first:

- [router-ax3000t-safe-test-harness.md](router-ax3000t-safe-test-harness.md)

Then use:

```bash
python3 ./scripts/Manage-OpenWrtTmpProgramSession.py \
  --action baseline \
  --router-host <ip> \
  --router-user <user> \
  --transport OpenSSH \
  --openssh-known-hosts-file ./router-known_hosts \
  --openssh-identity-file ~/.ssh/id_ed25519
```

## 12. Current PassWall2 Upgrade Posture

As of this KB:

- installed app package: `26.3.5-r1`
- workspace-verified latest upstream PassWall2 release metadata resolves to `26.4.2-1`
- architecture family for this router remains `aarch64_cortex-a53`
- default package manager path remains `opkg` with `.ipk`

Important distinction for this router:

- app upgrade path: package-based
- component runtime verification: package version plus binary self-report
- built-in component updater: fallback-only, because this router already shows drift evidence

## 13. Sources

Router-collected evidence:

- live SSH inventory collected read-only on `2026-04-04`

External references:

- OpenWrt AX3000T support patch and image/layout definitions: [lede-commits patch for Xiaomi AX3000T](https://lists.infradead.org/pipermail/lede-commits/2024-May/021239.html)
- OpenWrt `24.10.4` filogic target directory showing both stock-layout and `ubootmod` AX3000T artifacts: [downloads.openwrt.org 24.10.4 mediatek/filogic](https://downloads.openwrt.org/releases/24.10.4/targets/mediatek/filogic/)
- Search-indexed OpenWrt forum thread pointing to firmware-selector use for AX3000T and distinguishing U-Boot layout images: [Go to release build AX3000T](https://forum.openwrt.org/t/go-to-release-build-ax3000t/197504)
- Search-indexed OpenWrt forum thread referencing stock-bootloader TFTP recovery instructions for AX3000T: [Xiaomi AX3000T Bricked (no rapid blink)](https://forum.openwrt.org/t/xiaomi-ax3000t-bricked-no-rapid-blink/212425)

## 14. Live Update: 2026-04-07

Confirmed after a real manual sysupgrade and subsequent recovery work:

- The router now runs OpenWrt `24.10.6`, revision `r29141-81be8a8869`, kernel `6.6.127`.
- Board identity remains `xiaomi,mi-router-ax3000t`; layout family remains stock-layout.
- Current management address in the LAN test contour is `192.168.1.1`.
- Current controller packages on the router are:
  - `vectra-controller-agent 0.1.10-r1`
  - `luci-app-vectra-controller 0.1.10-r1`
  - `luci-app-passwall2 26.4.5-r1`
  - `xray-core 26.3.27-r1`
  - `geoview 0.2.5-r1`
- The current physical router identity in Vectra control plane is now `bdfdb919-5e06-4344-ad8b-67a16f3b6fcf`, and after approval recovery it is back in `active + approved`.
- A new controller packaging fix was validated live:
  - `vectra-controller-agent 0.1.9-r1` installs `/lib/upgrade/keep.d/vectra-controller`
  - `sysupgrade -l` now includes `/etc/vectra-controller/state.json`
  - backup-only proof via `sysupgrade -b /tmp/...tar.gz` confirmed that `etc/vectra-controller/state.json` really lands in the backup tar
  - repeated real `sysupgrade` on the same `24.10.6` image proved that `/etc/vectra-controller/state.json` survives flash and keeps the same `router_id`
- After the repeated recovery cycle, the live contour advanced again and the router is now running controller/LuCI `0.1.10-r1` while still preserving the same `router_id`.
- Post-flash reinstall findings are now also confirmed on-device:
  - `opkg update` succeeds after sysupgrade with the preserved Vectra feed/key config
  - `opkg install vectra-controller-agent luci-app-vectra-controller` may still fail on the package download step via embedded `wget`
  - direct `wget -> /tmp/*.ipk -> opkg install /tmp/*.ipk` works and restores the controller packages cleanly
  - after reinstall, the router checked back in under the same `router_id`, remained `import_state=approved`, and did not create a new router record
- A later attended PassWall2 repair on the same `24.10.6` runtime narrowed the remaining outage cause:
  - the configured custom `geosite_url`/`geoip_url` had to be refreshed so that `geosite:russia-outside` existed again in `/usr/share/v2ray/geosite.dat`
  - the router had fallen back to plain `dnsmasq` without `nftset`, so PassWall2 stayed broken until `dnsmasq-full`, `chinadns-ng`, `kmod-nft-socket`, and `kmod-nft-tproxy` were restored
  - after those restores, live `xray`, `chinadns-ng`, PassWall-managed `dnsmasq_default`, and `monitor.sh` processes all came back

Operational conclusion from the live update:

- The previous identity-loss bug after sysupgrade was caused by missing backup coverage for `/etc/vectra-controller/state.json`.
- That specific backup-surface gap is now closed for this router.
- A separate firmware caveat still remains: controller/LuCI/PassWall2 packages still do not survive sysupgrade automatically and must be restored manually, and the attended proof now shows that PassWall2 recovery for this router also depends on restoring custom geodata plus `dnsmasq-full`/nft runtime dependencies, not just the top-level app package.
