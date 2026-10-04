# Installing Vectra on any OpenWrt router

One installer for every router that runs OpenWrt 23.05 or 24.10 (opkg), on
every CPU Go builds for. OpenWrt 25 (apk) is refused by name until it is
supported.

```sh
wget -O /tmp/vectra https://router.vectra-pro.net/install && sh /tmp/vectra
```

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
6. **The feeds**: the Vectra feed's key goes to `/etc/opkg/keys/<id>` (baked
   into the installer at signing), the feed to `customfeeds.conf`, then
   `opkg update` — a list that fails its signature is dropped by opkg and the
   install stops. If OpenWrt's own feeds do not answer (a broken ISP IPv6 is the
   usual cause), `distfeeds.conf` is pointed at a mirror for this run
   (`<feed host>/openwrt-cache` by default) and put back at the end.
7. **xray without its package** (PassWall2's, swapped in by hand): opkg
   would satisfy Vectra's `xray-core` dependency with another feed's version
   written over it — an older one on the router migrated on 2026-10-03. The
   `xray-core` of the binary's own version, from whichever feed has it, is
   planned instead (installed first, checked against its feed's SHA256);
   with none — or no SHA256 in its feed's list, a binary older than the
   minimum, or one that does not run — the install is refused and the binary
   left as it is. The same over an xray-core package older than the minimum
   (opkg would upgrade it over the binary); over one at the minimum or newer,
   opkg leaves package and binary alone, and so does the installer.
   **Caveat**: that holds for the minimum of the feed the router installs
   from now. A later pro feed that raises the minimum above the installed
   xray-core package lets opkg upgrade xray-core on Vectra's next update —
   over a binary swapped in by hand. Before raising the minimum in the feed,
   put an xray-core package of the fleet's binaries' version in it (the
   installer then pins it), or swap those binaries back to their package.
8. **Storage**: Vectra's packages by the `Installed-Size` of the feed's own
   index, OpenWrt's by download size × 3 (their index has none), plus 4 MB;
   an xray binary its package replaces counts as freed. What would be left is
   held against what Vectra's own update needs free (16 MB): less is a
   warning — Connect's «Обновить» would be refused until room is made.

Then:

9. **dnsmasq → dnsmasq-full** without a moment without DHCP/DNS: its libraries
   first, both packages downloaded, the swap, `/etc/config/dhcp` restored, and
   dnsmasq must do again what it did before (answer from `/etc/hosts`, or at
   least run when another DNS server owns port 53). Otherwise the old dnsmasq
   is put back from the download and the install stops.
10. **The packages**: the planned xray-core first, then
   `vectra-controller-pro`, which pulls `xray-core (>= minimum)`, the kmods
   and the rest; `vectra-geodata` and `vectra-reporter` are named too on a
   first install, not left to the package's Depends (that Depends is the pro
   feed's, and a feed or package without it left a router without its geo
   data). An update upgrades Vectra's own packages only: xray-core may be
   PassWall's, and its version is the Depends' business.
11. **Geo data**: Vectra reads its own set, vectra-geodata's, in
    `/usr/share/vectra-controller-pro/geo`, and never replaces a file in
    `/usr/share/v2ray` (PassWall2's packages'; only links to its own set for
    a vctl older than 0.6.0-r18, which read them there). `uci geo_asset_dir`
    naming the fleet's old `/usr/share/v2ray` reads as Vectra's own. Another
    directory named there that lacks a category the provider routes by gets
    Vectra's own instead; its files stay untouched.
12. **The end is checked**: the installed version against the feed's own
    list (not the version the installer was signed with), `xray version` ≥
    minimum, xray accepting every geo category, the service running (off in
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
DOCKER_CONTEXT=colima ./test/install/run.sh                 # 21 scenarios on aarch64_generic

The routers share the docker host's kernel, which needs nftables' `fib`
(vctl's ruleset): the run checks that first and stops on a kernel without it
(Docker Desktop's linuxkit) — Colima's has it.
INSTALL_ARCHS="x86_64 arm_cortex-a15_neon-vfpv4 mips_24kc" ./test/install/run.sh lifecycle
```

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
| `standby-upgrade` | the owner's router as it is: an old copy off, PassWall2's newer xray-core and a feed offering an even newer one — upgraded, still off, PassWall2 and the agent never interrupted, xray-core untouched, PassWall's old-style `fwmark 0x1 / table 100` route surviving the upgrade and a removal |
| `passwall` | refused without `--yes`; with it: taken over, official xray-core upgraded to the minimum, Vectra's own geo data, PassWall2 back on `vectra off` |
| `passwall-upgrade` | taken over from PassWall2, then upgraded: vctl restarted in place, and PassWall2 never ran for the file swap (an upgrade is no hand-back) |
| `passwall-retire` | taken over from PassWall2 as opkg has it; refused while vctl carries nothing and with `retire_passwall '0'`; then carrying (the data-plane stand's operator config, a freedom outbound) and `vctl retire-passwall --now`: PassWall2's packages gone, xray-core and dnsmasq-full kept, its configuration in a 0600 backup, nothing owed, status `retired`; a bare stop restarted by the dead-man; `vectra off` leaves plain internet, a minute later too |
| `dnsmasq-rollback` | a dnsmasq-full that never runs: the old dnsmasq back and answering |
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
