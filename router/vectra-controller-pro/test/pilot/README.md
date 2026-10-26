# The pilot stand: a BloopCat router, end to end, before a real one

```sh
DOCKER_CONTEXT=colima ./test/pilot/run.sh            # pilot, then upgrade
DOCKER_CONTEXT=colima ./test/pilot/run.sh pilot      # just this one
```

A router for the BloopCat pilot is set up by hand, in this order: the reporter
switched off before anything is installed, the published installer with
`--standby`, a root password, the support shell off, `vectra on --force`.
The partner's review asked for that whole sequence on real OpenWrt, through a
WAN: the upgrade from an older release, a reboot, what the router keeps, and
whether the reporter sends anything. The stand runs exactly that, with the
bytes Vectra published, and checks each step on what crosses the WAN.

## Scenarios

| Scenario | Phases | What it shows |
|---|---|---|
| `pilot` | install → reboot → power cut → controls | a new router, the pilot's steps one by one; its switches and identity through a reboot and a power cut |
| `upgrade` | old → reboot → upgrade → power cut → controls | a router on 0.7.0-r28 (built from its commit), identity made, a crash report queued while Connect was down, the owner's switches turned off; the published installer run over it |
| `upgrade06` | the same | the same from 0.6.0-r36 (`PILOT_OLD06_COMMIT`, default `0c4e4807`): vectra-reporter 1.0.0-r2, `state.json` still plaintext, no support-shell switch yet — the upgrade seals the state and brings the reporter to the published one |

Each phase is one boot of the router. A reboot is procd's shutdown order (the
rc.d `K` scripts) and a stopped container; a power cut is a killed container.
`/tmp` is a tmpfs, so it is empty after either, as on the router.

The controls phase turns the owner's switches back on: the same support-shell
job then runs, and the reporter's cron run then delivers the reports, verified
by the sink; with fw4 stopped the WAN probe reaches dropbear and uhttpd. Each
refusal above it could therefore have failed.

## What is real

- OpenWrt 24.10.8's userland (`openwrt/rootfs:aarch64_generic`), its procd,
  ubusd, rpcd, the rc.d boot order (`S*` with `boot`) and shutdown order
  (`K*` with `shutdown`), busybox crond, fw4, dnsmasq, dropbear, uhttpd.
- The installer, the feed index, its signature and the packages exactly as
  Vectra published them at <https://api.vectra-pro.net/artifacts/openwrt/pro-canary>:
  downloaded once per run, each package checked against the index; opkg on
  the router checks the signature with the key the installer adds. The binaries
  on the router are compared with the ones inside the published packages.
  `logs/published.manifest` records what was mirrored.
- The older releases, each built with its own `build-pro-feed.sh` from its
  commit (`PILOT_OLD_COMMIT`, default `9ebd3e13`, the last 0.7.0-r28 commit;
  `PILOT_OLD06_COMMIT`, default `0c4e4807`, 0.6.0-r36), signed with a throwaway
  key and served at the same pro-canary path, so the published installer finds
  the feed line it expects.
- vctl and vectra-reporter as the packages install them, started by procd and
  cron, never by the stand.

## What is not

- The hardware. The container is an aarch64 armsr userland told to accept
  `aarch64_cortex-a53` packages (`/etc/opkg.conf`, `DISTRIB_ARCH`); every boot
  shares the docker host's kernel, so a reboot keeps the kernel's boot id.
- netifd: docker configures `eth0`, and fw4's wan zone is bound to it by
  device. No LAN, no Wi-Fi, no clients behind the router.
- The panel and Connect: stubs on the WAN side (`wan/`). The stub panel
  approves any router, hands out queued jobs and names a claim key of its own
  (`claimKey`), so the reporter seals its tokens to a key the sink holds.
- Traffic through xray: the router has no subscription, so `vectra on --force`
  starts vctl with nothing to carry. The data plane is the data-plane stand's
  (`test/dataplane`).

## What the stand found (2026-10-25, published 0.7.0-r29)

Run of 2026-10-25 (installer `4f9527b9…`, vctl package `5119e940…`):
`pilot` 69/69, `upgrade` 79/82, `upgrade06` 79/82 — 227/233. The six
failures are one defect, the second point below.

Fixed in 0.7.0-r30 (#96, e2219c8c), published to pro-canary on 2026-10-25:
the stand's mirror of the live feed now carries r30, so a plain run tests r30.

- **The pilot's steps hold.** The owner's `/etc/config/vectra-reporter`
  (`enabled '0'`) survives the install and both upgrades (opkg keeps a
  modified conffile; the package's copy goes to `vectra-reporter-opkg`); the
  support shell refuses the panel's job after the install, a reboot, a power
  cut and an upgrade; nothing reaches Connect under cron while the reporter is
  off, with something to say (the dead-man's VCTL_DOWN); identity and the
  sealed `state.json` stay, the upgrade from 0.6.0-r36 seals the plaintext
  one. With the switches back on, the job runs and cron delivers reports the
  sink verifies against the device key the OLD release registered.
- **An upgrade wipes the reporter's own state.** The published
  `vectra-controller-pro_0.7.0-r29` has the Makefile's `Depends` (no
  `vectra-geodata`, no `vectra-reporter`, no xray minimum); a feed built by
  `scripts/build-pro-feed.sh` adds them. A router whose vctl came from such a
  feed has both as auto-installed dependencies; upgrading vctl to the
  published package, opkg removes them as orphans ("vectra-reporter was
  autoinstalled and is now orphaned, removing"): the reporter's postrm deletes
  `/etc/vectra-reporter` (its spool, its state, the clean-shutdown mark) and
  vectra-geodata's files go. The published installer installs both again by
  name, so the router ends up whole but its queued reports are lost
  (`upgrade_reporter_state_kept`, `upgrade_report_kept`,
  `controls_old_report` fail on purpose). vctl's own self-update
  (`update_controller`: `opkg install` of the one package) installs nothing
  back — read from the source, not run here.
- vctl 0.7 writes no crash output (`cmd/vctl/crash_go123.go`), so a crash is
  no report any more; only 0.6 reported VCTL_PANIC.
- `fw4 flush` (`/etc/init.d/firewall stop`) deletes every nftables table,
  vctl's `inet vctl` included.

## A release not published yet (`PILOT_NEW_COMMIT`)

```sh
DOCKER_CONTEXT=colima PILOT_NEW_COMMIT=<commit> ./test/pilot/run.sh
```

runs the release at that commit in the published one's place: built with its
own `build-pro-feed.sh` and `sign-pro-feed.sh` like the old ones, signed with
a throwaway key, served at the same pro-canary path. Everything else is the
same, the installer's key is read from its `BAKED_FEED_KEY_ID`, and
`*_companions_user` checks that vectra-geodata and vectra-reporter end up
installed without opkg's `Auto-Installed` mark. `upgrade_start_drops_marks`
(from r31) marks them again, as an older vctl's self-update leaves them, and
checks that the vctl drops the marks once it starts.

Run of 2026-10-25 on 0.7.0-r30 (`a75bb5c8`, branch fix/vctl-r30-packaging):
`pilot`, `upgrade`, `upgrade06` 236/236. The three checks the published r29
failed pass: the reporter's state and its queued report survive both
upgrades, and the report is delivered later. No "orphaned" line appears in
either upgrade's install log. In `upgrade06`, opkg upgraded vectra-reporter
from 1.0.0-r2 to 1.0.0-r4 as vctl's dependency and marked it again; the
installer's second pass dropped the mark.

## What only a real router shows

A pass here is no client check. Before the pilot ships, one spare AX3000T on
a fresh OpenWrt 24.10 behind a real WAN, set up with the same five steps, has
to show what the stand cannot: the MT7981 kernel and flash (a power cut on
jffs2/ubifs, a reboot with a new boot id and its own pstore), netifd's WAN,
the production panel's answers (pending approval, its flags, whether it names
a `claimKey` — without one the reporter seals to Vectra's built-in key, which
only Connect opens), and the data plane once the router has a subscription.
That run needs the owner's go: it is a live router and the production panel.

## Nothing reaches production

vctl and the reporter resolve the control plane's names themselves, over TCP to
8.8.8.8 and 77.88.8.8 (`internal/controlplane/resolve.go`), not through
`/etc/hosts`, and know the panel's address as a fallback (`KnownAddrs`). So the
router's nftables (`pilot_guard`, `pilot_dns`, loaded before anything else at
every boot) send those two resolvers to the WAN side's DNS, and drop and count
every packet to Vectra's production addresses. Every phase asserts the count is
0 (`*_no_production`). dnsmasq's upstream is the WAN side's DNS too, as an
ISP's would be. The WAN side answers Vectra's names with its own address and
asks docker's resolver for everything else.

The WAN side sits on `44.77.0.0/24`, outside every bypass set of vctl's
ruleset, like the data-plane stand's `44.44.44.0/24`: the router's control
traffic must take its marked path to reach it.

## Layout

- `run.sh`: builds the WAN side, mirrors the published release, builds the old
  one, runs the scenarios in parallel (one router each), and prints the verdict.
- `router.sh`: one boot of a router: the guard, the boot, the phase named in
  `/out/phase`; `router.sh shutdown` is the shutdown order.
- `wan/`: DNS, the mirror, the stub panel, the report sink and a port probe,
  on one address. Every request is a line of `logs/wan/requests.jsonl`.
- `logs/<scenario>/<phase>.log`: `RESULT <name> PASS|FAIL <detail>` lines.

Needs Colima (or any docker host whose kernel has nftables fib): Docker
Desktop's kernel has none. A run takes about 20 minutes, mostly cron's minutes.
