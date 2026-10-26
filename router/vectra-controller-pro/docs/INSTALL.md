# Installing Vectra on any OpenWrt router

One installer for every router that runs OpenWrt 23.05 or 24.10 (opkg), on
every CPU Go builds for. OpenWrt 25 (apk) is refused by name until it is
supported.

```sh
wget -O /tmp/vectra https://router.vectra-pro.net/install && sh /tmp/vectra
```

The plain command installs a **Vectra** router: this is Vectra's installer
(Vectra Connect's guide runs it as it is), so a router with no brand yet is
labelled `vectra`. `--brand` says whose router this is until its subscription
names the service: a BloopCat install passes `--brand bloopcat`; `--brand none`
leaves the router neutral — it names no service, and its Wi-Fi network takes
the model's name — until its owner links it. A label put on earlier stays when
the flag is not given — a BloopCat router the installer is run on again stays
BloopCat — and an explicit `--brand` replaces it. A neutral router has no
label to keep: run the installer on it again with `--brand none`, or it
becomes a Vectra router.

Keep the download-to-file form: the installer copies itself for `--uninstall`
and reads answers from the terminal, so `| sh` and `sh -c "$(…)"` do not work.
`https://router.vectra-pro.net/install` serves the signed `pro-canary` installer;
an operator can fetch another channel's from
`https://api.vectra-pro.net/artifacts/openwrt/<channel>/install.sh`.

| flag | what it does |
|---|---|
| *(none)* | install, or update what is installed; Vectra takes the traffic |
| `--standby` | install next to what carries the traffic now (PassWall2, the old Vectra agent) and leave Vectra **off**; upgrades an older copy the same way; marks the router set up (the wizard does not open by itself) |
| `--check` | every check, nothing changed |
| `--yes` | on a router with PassWall2: Vectra takes the traffic (PassWall2 stops; `vectra off` gives it back — until Vectra removes PassWall2, a day after it carries the traffic: see [PassWall2 after the install](#passwall2-after-the-install)) |
| `--uninstall [--purge]` | remove Vectra (dnsmasq-full stays); `--purge` also forgets the router's Vectra identity |
| `--brand vectra\|bloopcat\|none` | label the router with its owner's brand (UCI `main.brand`) until its subscription names one — `none` clears the label (neutral); the value follows the flag after a space (`--brand=vectra` is not understood); an unknown brand is refused before any change (exit `3`); without the flag a router with no label is labelled `vectra` and a label put on earlier stays |
| `--force` | go on below the memory floor (tests only) |
| `--json` | the same run said as JSON lines, ASCII codes only, each line short (the words go to the log): with `--check`, what an operator reads through the panel's ASCII-only output filter |

**Exit code**: `0` done, nothing to warn of (`--check`: also with advice —
a DPI tool, little storage left — named in the summary and in `--json`);
`2` done, with warnings — named by code in the summary (`Итог: …`) and in
`--json`'s last line; `1` not done: refused before any change, failed (the
summary says which, and in what state the router is), or installed to
carry the traffic and not running — the service down or its ubus object
silent; `3` an option it does not know. **Changed in 0.7.0-r14**: a refusal
exited 2 before; 2 now means installed with warnings.

`--json` prints one object a line: `{"installer":…,"mode":…}` first, then
`{"check":CODE,"level":"ok|warn|refuse|fail"[,"value":…]}` for what it found,
and last `{"result":"ok|warnings|refused|failed","exit":N,"mode":…,"code":CODE,"warnings":[…]}`.

The installer keeps a copy of itself in `/etc/vectra-controller-pro/vectra-install.sh`
for `--uninstall` after a reboot, and logs everything opkg says to
`/tmp/vectra-install.log`.

## What it does, in order

Nothing changes until every check has passed:

1. **The router**: root, `/etc/openwrt_release`, opkg (apk named and refused),
   23.05 or 24.10 (older: no nftables-era dnsmasq), firewall4.
2. **The architecture**: the first one opkg prefers that the feed carries.
3. **Memory**: `MemTotal` of a 256 MB router (192 MiB floor) — below it xray
   and vctl run the router out of memory and the owner loses the internet.
   `/tmp` ≥ 24 MB for the downloads.
4. **The clock**: later than 2026-01-01, NTP tried once; TLS needs it.
5. **Other proxies**: podkop, OpenClash, homeproxy, v2rayA, nikki/mihomo,
   ssclash, PassWall v1, a *running* xray or sing-box service — refused (two
   transparent proxies fight). OpenWrt's xray-core and sing-box packages alone
   are fine: their init script starts nothing until configured. PassWall2 is
   taken over only with `--yes` (reversibly for a day, then Vectra removes it:
   below); until the router is linked, Vectra carries its traffic on
   PassWall2's own routes. The old Vectra agent only next to `--standby` —
   two controllers would check in as one router and take the same traffic —
   and only 0.1.13-r45 or later (`/usr/share/vectra-controller/vault-read-v1`:
   it reads the identity Vectra seals and never starts beside it); an older
   one is to be updated first. `--check` without `--standby` next to the
   agent says why, checks the rest as with it, and exits 1.
6. **dnsmasq** (unless dnsmasq-full is there already): which package runs
   it, `dnsmasq` or `dnsmasq-dhcpv6`. Another package, a binary no package
   owns, or no dnsmasq at all is refused (DNSMASQ_UNKNOWN_PACKAGE,
   DNSMASQ_ABSENT). The owner must also run it, enabled and running. One
   switched off (AdGuard Home or the like does DNS and DHCP) is refused
   (DNSMASQ_NOT_ACTIVE): the installer never turns on what the owner turned
   off.
7. **The feeds**: the Vectra feed's key goes to `/etc/opkg/keys/<id>` (baked
   into the installer at signing), the feed to `customfeeds.conf`, then
   `opkg update` — a list that fails its signature is dropped by opkg and the
   install stops. If OpenWrt's own feeds do not answer (a broken ISP IPv6 is the
   usual cause), `distfeeds.conf` is pointed at a mirror for this run
   (`<feed host>/openwrt-cache` by default) and put back at the end. If they
   all answered and a package is still missing (a kmod for a firmware built by
   hand), that is said as it is: OPENWRT_PACKAGE_MISSING, not
   OPENWRT_FEEDS_UNREACHABLE.
8. **xray: one per vctl.** The xray this vctl depends on (`xray-core (>=
   26.10.21)` for 0.7.0-r30) and is proven with is Vectra's feed's own; after
   the install the router's xray is that package and that package's binary.
   Its file from Vectra's list is planned (fetched first, checked against the
   list's SHA256; installed by itself just before vctl, with `--force-space`
   — opkg counts its whole Installed-Size, which UBIFS stores in less than
   half — and, where a vctl from 0.7.0-r29 runs, that vctl restarted onto it
   so the old binary's room is back before vctl's own opkg): as an upgrade
   over an older package or none, a reinstall over the same version with a
   binary swapped in by hand
   (the four vctl routers ran 26.10.17 over xray-core 26.7.28-r1 from
   2026-10-21), a downgrade under a newer package. Another feed's xray-core,
   however new, is never taken; a feed without xray-core, without its
   SHA256, or with another xray than the installer's release, is refused.
   PassWall2 before 26.9.9-2 writes outbound proxySettings, which xray refuses
   since 26.9.8, and /usr/bin/xray is one for both: next to it `--standby` is
   refused (PASSWALL_TOO_OLD_FOR_XRAY, nothing changed — update PassWall2 to
   26.9.9-2 or newer, or let Vectra take over); a takeover warns
   (PASSWALL_HANDBACK_WITHOUT_VPN) that `vectra off` then leaves PassWall2
   off and the internet without VPN — the init script keeps it off rather
   than give it the router on an xray it breaks on, says why (`vectra
   status`, `vectra off`), and gives it back at a later hand-back once
   PassWall2 is updated.
9. **Storage**: Vectra's packages by the `Installed-Size` of the feed's own
   index, OpenWrt's by download size × 3 (their index has none), plus 4 MB;
   Vectra's xray-core whenever it goes in, a reinstall too (the xray that
   runs holds the old binary until it restarts): 1.4 × its package's Size on
   UBIFS/JFFS2 (an AX3000T: 14.1 MB on flash for the 13.3 MB package, 18.5 MB
   at the low point), its Installed-Size elsewhere; the larger of it and the
   rest when the old xray is let go between them, their sum otherwise. What
   would be left is
   held against what Vectra's own update needs free (16 MB): less is a
   warning — Connect's «Обновить» would be refused until room is made.

Then:

10. **dnsmasq → dnsmasq-full**, with the network as short a time as possible
   without DNS: its libraries first, both packages downloaded, the swap,
   `/etc/config/dhcp` restored, and dnsmasq must do again what it did before
   (answer from `/etc/hosts`, or at least run when another DNS server owns
   port 53). Before anything is removed, the old dnsmasq (or dnsmasq-dhcpv6)
   is kept twice: its package, with what it depends on already installed, and
   its files with opkg's record of them. They go to
   `/root/vectra-dnsmasq-saved/<run>` when there is room on flash, so a reboot
   does not lose them, and to `/tmp` otherwise. Each run has its own directory,
   so a copy an earlier failed run left for the owner is never taken away. Only the system dnsmasq is stopped,
   started and, if a leftover of it still holds port 53, killed: the instances
   procd runs for `/etc/config/dhcp` (`-C /var/etc/dnsmasq.conf.<section>`),
   found by name and command line. It is never `pgrep -x dnsmasq`, which
   matches `argv[0]`, and never PassWall2's copies. From the old dnsmasq's
   stop to a dnsmasq that answers, all stages share one deadline
   (`VECTRA_DNS_DEADLINE`, 120 s): a third for dnsmasq-full, then the way back
   with no network, from the package or, when opkg cannot, from the files
   (DNSMASQ_FULL_ROLLED_BACK, the install stops). Each start still gets at
   least 20 s counted from that start (the files 15 s, by hand 10 s), so a slow
   router's opkg is not charged to its dnsmasq. If even that does not answer, dnsmasq is run by hand until a reboot
   and the run ends DNSMASQ_FULL_BROKEN, saying what to run; the saved copy
   stays. What dnsmasq looked like at each failed try (processes, port 53,
   procd's view, logread) is in the install log.
11. **The packages**: Vectra's xray-core first (point 8), then
   `vectra-controller-pro`, whose Depends it meets, the kmods and the rest; `vectra-geodata` and `vectra-reporter` are named too on a
   first install, not left to the package's Depends (a vctl before 0.7.0-r30
   built otherwise than by build-pro-feed.sh names neither, and a feed that
   served one left a router without its geo data). An update upgrades
   Vectra's own packages only: xray-core is in already; a vctl not upgraded
   (the same version) is restarted onto a new xray. After the install the
   two lose opkg's `Auto-Installed` mark (`keep_companions`, which asks the
   vctl just installed: `vctl keep-companions`): opkg removes an
   auto-installed package as an orphan as soon as a vctl that does not depend
   on it replaces one that did (0.7.0-r29 as published), and no opkg command
   takes the mark away — `opkg flag user` does not. vctl takes opkg's own
   lock (`/var/lock/opkg.lock`), holds it only once no opkg is alive and the
   lock file is still the one it locked, and replaces the status only if it
   is still what it read; another opkg is waited for up to 10 s, and
   otherwise COMPANIONS_MARK, a warning — vctl from 0.7.0-r31 drops the marks
   itself when it next starts. vctl's own self-update (update_controller,
   Connect's automatic update) does it around its `opkg install`, and
   `scripts/Rollout-VctlFleet.py` after it, by the same command.
12. **Geo data**: Vectra reads its own set, vectra-geodata's, in
    `/usr/share/vectra-controller-pro/geo`, and never replaces a file in
    `/usr/share/v2ray` (PassWall2's packages'; only links to its own set for
    a vctl older than 0.6.0-r18, which read them there). `uci geo_asset_dir`
    naming the fleet's old `/usr/share/v2ray` reads as Vectra's own. Another
    directory named there that lacks a category the provider routes by gets
    Vectra's own instead; its files stay untouched.
13. **The end is checked**: the installed version against the feed's own
    list (not the version the installer was signed with), xray Vectra's own
    (the package of its feed, that package's binary: XRAY_NOT_VECTRAS
    otherwise), xray accepting every geo category, the service running (off in
    `--standby`), `ubus call vectra status`, LuCI serving the page, and free
    storage against the update's 16 MB. What fails is a warning: the run
    exits 2. The claim code and its link are printed when the router already
    reached Vectra (and stay in `vectra status` while it is not linked).

## PassWall2 after the install

Vectra's routers end without PassWall2 (the owner's decision): a new install
never brings it, and one over PassWall2 removes it once Vectra has proven
itself on that router.

- **The takeover** (`--yes`): PassWall2 stopped and disabled, its own switch
  (`passwall2.@global[0].enabled`) off, and breadcrumbs in
  `/etc/vectra-controller-pro` saying so. It stays installed, as the way back.
- **The first day**: `vectra off` gives the traffic back to PassWall2. Not
  `/etc/init.d/vectra-controller-pro stop`: with Vectra switched on, the
  dead-man (cron, every minute) starts a bare stop again.
- **The retirement**: once Vectra has carried the traffic, switched on for
  good, for a day (UCI `passwall_retire_after`, seconds, default 86400; the
  clock starts at the first look that finds vctl carrying it, and every
  `vectra off` starts it over), vctl removes PassWall2 with opkg:
  `luci-app-passwall2`, its translations, and of its helpers (`chinadns-ng`,
  `geoview`, `tcping`, `v2ray-geoip`, `v2ray-geosite`) what nothing else on
  the router needs — never xray-core, dnsmasq-full or anything the router
  runs on. First its configuration (`/etc/config/passwall2`,
  `passwall2_server`) goes into
  `/etc/vectra-controller-pro/backup/passwall2-<unix time>.tar.gz` (0600, in
  a 0700 directory, the newest three kept), and the record
  `/etc/vectra-controller-pro/.passwall-retired-by-vctl` says when and what.
  It waits while a trial runs, while vctl carries nothing (no account yet,
  the rescue's direct mode), while PassWall2 runs, with too little room for
  opkg; it never goes with `route_source 'passwall'`, nor with `'native'`
  before vctl's own store and geo files are in place. A removal opkg refuses
  leaves PassWall2 whole, and is tried again 6 hours later.
- **After it**: `vectra off` leaves the router on plain internet — there is
  nothing to give back — and the dead-man never gives anything back either;
  `ubus call vectra status` says `legacy.passwall: "retired"` and when.
- **A router whose PassWall2 packages were removed by hand** (as this
  project's docs once said to) is tidied the same way: after the same day,
  counted while PassWall2 is gone, its configuration is backed up and taken,
  and nothing says it is owed back.

For an operator:

```sh
vctl retire-passwall --now          # at once: only the day is skipped; it says why not, if not
uci set vectra-controller-pro.main.retire_passwall=0   # PassWall2 stays (then a restart)
uci set vectra-controller-pro.main.passwall_retire_after=3600
uci commit vectra-controller-pro && /etc/init.d/vectra-controller-pro restart   # a restart is no hand-back
```

PassWall2 back by hand, if someone really wants it — Vectra off first, so
that two proxies never hold the router at once:

```sh
vectra off
opkg install luci-app-passwall2   # from PassWall2's own feed or its release's .ipk files, with its helpers
vctl restore-passwall -backup /etc/vectra-controller-pro/backup/passwall2-<unix time>.tar.gz.vault
uci set passwall2.@global[0].enabled=1 && uci commit passwall2   # the takeover turned it off
/etc/init.d/passwall2 enable && /etc/init.d/passwall2 start
```

New retirement archives are encrypted and tied to their original path and local
vault key. Restore with `vctl restore-passwall`: it authenticates and validates
the archive, then feeds tar through stdin, without writing a plaintext archive.
Preserve the original archive path and its `backup/.vault` transaction metadata and separate
`/etc/vectra-controller-pro-vault-keys/` key store for
recovery. Losing that key makes the archive unreadable. This is an explicit
operator recovery action and restores plaintext UCI files for PassWall itself.
Older `.tar.gz` backups stay intact and require the original explicit
`tar -xzf <archive> -C /` recovery path; they still contain credentials.

Before `vectra on` again, `retire_passwall '0'` keeps it for good; otherwise
the takeover and the day begin anew.

## The feed

`scripts/build-pro-feed.sh` builds it without the SDK, for the 32
architectures of OpenWrt 23.05/24.10 that Go builds for (one Go build per CPU
family; soft float wherever the architecture promises no FPU):

```
<channel>/install.sh              the installer, baked
<channel>/vectra-pro.pub          the feed's usign public key
<channel>/<arch>/Packages{,.gz,.sig}
<channel>/<arch>/vectra-controller-pro_<ver>_<arch>.ipk
<channel>/<arch>/xray-core_<xray>-r1_<arch>.ipk     XTLS's release build, checked against its .dgst
<channel>/<arch>/vectra-geodata_<ver>_all.ipk       geoip.dat/geosite.dat, pinned by sha256
```

The build refuses geo data that lacks a `geosite:`/`geoip:` category the
provider's config routes by (xray itself is the judge), an xray without a
matching `.dgst`, and a feed directory with a foreign architecture or two
versions of one package. `tools/feedtool` writes the archives deterministically
and signs the index the way OpenWrt's usign does (byte for byte, pinned by a
test vector from its 24.10 userland).

Left out: `armeb_xscale`, the 32-bit `powerpc_*` and `powerpc64_e5500` (no Go
port that runs there), `i386_pentium-mmx` (xray's 386 build needs SSE2).

## Publishing (the owner's go-ahead first)

```sh
scripts/publish-pro-feed.sh --channel pro-canary --arch aarch64_cortex-a53 [--create-key]
```

Builds here, ships to the build host, signs there with the production key that
never leaves it (`/opt/vectra-prorouter/.codex-runtime/pro-feed-keys`, made once
with `--create-key`), bakes the installer, syncs only
`deploy/runtime/artifacts/openwrt/<channel>` (a channel must start with `pro`:
never the fleet's `openwrt/stable`), and reads everything back over HTTPS.
Build from a clean checkout: the script stamps the commit into vctl and warns
nothing about uncommitted work.

## The stand

```sh
DOCKER_CONTEXT=colima ./test/install/run.sh                 # 27 scenarios on aarch64_generic

The routers share the docker host's kernel, which needs nftables' `fib`
(vctl's ruleset): the run checks that first and stops on a kernel without it
(Docker Desktop's linuxkit) — Colima's has it.
INSTALL_ARCHS="x86_64 arm_cortex-a15_neon-vfpv4 mips_24kc" ./test/install/run.sh lifecycle
INSTALL_REPEAT=10 INSTALL_STRESS=4 ./test/install/run.sh feed-outage   # 10 runs, 4 busy loops in each router
```

A run that fails (the installer's, or the end's checks) leaves `DIAG|` lines in
its scenario log: dnsmasq's packages and processes, port 53, procd's view of
the service, logread and the install log.

Each scenario boots a fresh `openwrt/rootfs:<arch>-24.10.8` (procd, ubusd,
logd, rpcd, uhttpd, dnsmasq), serves a feed signed with a throwaway key from a
private docker network, runs the installer as a person would, and checks the
router it leaves. The containers cannot reach Vectra's production hosts (a
fresh vctl registers with the panel at its first start). The refusals must
leave the router byte for byte as it was (packages, feeds, keys, `/etc/config`,
`/etc/rc.d`).

| scenario | what it proves |
|---|---|
| `lifecycle` | install, run again (the daemon untouched), `--uninstall`, `--purge` |
| `check` | `--check` changes nothing |
| `standby` | next to PassWall2 and the old agent: installed, off, both still running |
| `standby-upgrade` | the owner's router as it is: an old copy off, PassWall2's xray-core and another feed offering a newer one — upgraded, still off, PassWall2 and the agent never interrupted, xray-core Vectra's own (not the newer one), PassWall's old-style `fwmark 0x1 / table 100` route surviving the upgrade and a removal |
| `passwall` | PassWall2 26.9.9-2: refused without `--yes`; with it: taken over, official xray-core replaced by Vectra's, Vectra's own geo data, PassWall2 back on `vectra off` |
| `passwall-old` | PassWall2 26.8.10 (the fleet's; proxySettings): `--standby` refused, nothing changed; `--yes` takes over with PASSWALL_HANDBACK_WITHOUT_VPN; `vectra status` and `vectra off` say why PassWall2 stays off; `vectra off` leaves plain internet, PassWall2 off and still owed |
| `passwall-upgrade` | taken over from PassWall2, then upgraded: vctl restarted in place, and PassWall2 never ran for the file swap (an upgrade is no hand-back) |
| `passwall-retire` | taken over from PassWall2 as opkg has it; refused while vctl carries nothing and with `retire_passwall '0'`; then carrying (the data-plane stand's operator config, a freedom outbound) and `vctl retire-passwall --now`: PassWall2's packages gone, xray-core and dnsmasq-full kept, its configuration in a 0600 backup, nothing owed, status `retired`; a bare stop restarted by the dead-man; `vectra off` leaves plain internet, a minute later too |
| `dnsmasq-rollback` | a dnsmasq-full that never runs: the old dnsmasq back and answering, the same version, enabled, within 120 s, kept on flash meanwhile; a second run leaves an earlier run's copy — with every download failing from the moment the swap begins |
| `dnsmasq-rollback-files` | the same, and opkg refuses to put the old package back: its saved files and opkg record go back by hand, and it answers |
| `dnsmasq-stale` | a frozen dnsmasq holds port 53 while dnsmasq-full starts (the 0.7.0-r18 installer: DNSMASQ_FULL_BROKEN): killed, installed |
| `nojail` | a router without procd's jail: its unjailed dnsmasq found, swapped, installed |
| `dnsmasq-dhcpv6` | the router's dnsmasq is `dnsmasq-dhcpv6`: put back offline when dnsmasq-full does not run, then swapped and installed |
| `dnsmasq-disabled` | dnsmasq stopped and disabled (another DNS/DHCP server): refused, unchanged, still off |
| `feed-outage` | downloads.openwrt.org away for a moment at each step that needs it: tried again, installed |
| `mirror` | downloads.openwrt.org unreachable: through a mirror, `distfeeds.conf` restored |
| `refuse-*` | arch, apk, release, memory, storage, conflict, fleet, agent-old, signature, feed-down: exit 1, the router byte for byte as it was |
| `check-json` | `--check --json` next to the old agent without `--standby`: JSON lines in ASCII, why `--standby`, the rest checked, exit 1 |

The installer's decisions are also proven without Docker, on a fake opkg,
xray and wget (`go test ./install`): the xray-core of a hand-swapped binary's
version, refusing another; geo data and reporter on a first install; the
version read from the feed; the update's storage floor; the exit codes; the
old agent; `--json`.

Foreign architectures run under qemu-user, which does not emulate everything a
router has: procd's jail is off there, dnsmasq's replies do not come back under
qemu-mips (the checks see it run instead), and Go's 386 runtime segfaults under
qemu-i386 — `i386_pentium4` is built but not run.
