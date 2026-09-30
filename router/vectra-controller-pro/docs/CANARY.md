# Canary: vctl 0.3 + router UI on one router

A hands-on test on ONE router (the owner's `1111111111`, Xiaomi AX3000T,
OpenWrt 24.10). Every step keeps a way back; the way back is written next to it.
Nothing here publishes to the feed or touches the panel.

## 0. Before you start

- **Subscription: the TEST account only** (`vas.kon99@mail.ru`), never a
  customer's URL. The provider's anti-fraud sweep deletes a device and disables
  the account on the first request whose `Happ…` User-Agent is not the real
  client's `Happ/<ver>/<OS>/<build>`; vctl refuses to send one
  (`internal/uaguard`) and refuses a config that carries one. Use
  `"userAgent": "v2rayNG/1.9.5"` (JSON variant, not a Happ agent).
- Free RAM ≥ 64 MB (`free -m`), overlay ≥ 12 MB (`df -h /overlay`).
- `xray version` ≥ 26.3.27 (the fleet's; older lacks `api.listen`).
- A second way into the router that does not depend on the proxy: LAN cable +
  SSH to `192.168.1.1`.

## 1. Build the package

Either locally, on the Mac — a static build of this checkout, the payload and
maintainer scripts replayed from `openwrt/Makefile`:

```sh
cd router/vectra-controller-pro
scripts/build-local-ipk.sh          # -> dist/vectra-controller-pro_0.5.0-r1_aarch64_cortex-a53.ipk
scp -O dist/vectra-controller-pro_*.ipk root@192.168.1.1:/tmp/
```

or with the real OpenWrt SDK on the build host, staged to the canary path (never
the fleet feed): `scripts/build-vctl-canary-ipk.sh --branch feat/vctl-router-ui`
from the repository root — see that script's header.

## 2. Install WITHOUT taking the router over

```sh
VECTRA_SKIP_POSTINST_RESTART=1 opkg install /tmp/vectra-controller-pro_*.ipk
```

PassWall and the old agent keep running; nothing about traffic changes. LuCI now
has **Services → Vectra** (it says the controller is not running — correct).

*Way back:* `opkg remove vectra-controller-pro`.

## 3. Operator config + first fetch (still nothing live)

On the Mac — the example with the TEST account's URL put in (the URL is a
secret: type it here, do not commit it anywhere):

```sh
sed 's#"url": "https://sub.example.invalid/subscription-token"#"url": "<TEST-ACCOUNT-URL>"#' \
  examples/operator-config.json > /tmp/operator-config.json
ssh root@192.168.1.1 mkdir -p /etc/vectra-controller-pro
scp -O /tmp/operator-config.json root@192.168.1.1:/etc/vectra-controller-pro/xray-desired.json
rm /tmp/operator-config.json
```

On the router:

```sh
vctl validate -config /etc/vectra-controller-pro/xray-desired.json
/usr/libexec/vectra-controller-pro/render-xray-config.sh /var/run/vectra-controller-pro/agent.json
vctl apply-local -config /var/run/vectra-controller-pro/agent.json -ignore-legacy-agent
```

`apply-local` fetches the subscription ONCE, caches all its locations, and writes
the xray config through the `xray -test` gate. It does not start xray and does
not touch the firewall. `-ignore-legacy-agent` is needed because the old agent
still owns the router at this point; apply-local only writes files.

*Way back:* nothing is live; `opkg remove vectra-controller-pro`.

## 4. Go live — as a trial first

```sh
vectra on --trial --minutes 15
vectra status                   # who carries the traffic, and the minutes left
```

The hand-over stops the old agent and PassWall (and remembers that it did),
turns PassWall2's own switch off (`uci get passwall2.@global[0].enabled` → 0:
its nightly subscription run restarts it through that switch), starts vctl,
which starts xray and programs the firewall behind a 90 s
commit-confirm: if the panel check-in does not confirm it, the ruleset reverts
by itself. As a trial it disables nothing and gives vctl no boot links, and
/etc/uci-defaults/99-vectra-trial-passwall-switch turns PassWall2's switch on at
the next boot: a power cycle brings back exactly what ran before, and after the
minutes Vectra gives the router back by itself (its log:
/tmp/vectra-power.log).

When it works, keep it — Vectra stays on after a reboot too (the old agent and
PassWall are then disabled, as by `vectra on`):

```sh
vectra keep
```

*Way back (at any moment, trial or not):*

```sh
vectra off      # unloads vctl, gives PassWall + agent back
```

## 5. Check (5–10 minutes)

First, after ~2 minutes — the commit-confirm must have passed (a check-in to the
panel confirms the firewall; without one it reverts at 90 s and the router is
left unproxied):

```sh
ubus call vectra status | jsonfilter -e '@.dataplane.loaded' -e '@.controlPlane.reachable'
# true / true — otherwise roll back (step 4's way back)
```

- LuCI → **Services → Vectra**: overview green; **Balancing** shows each
  balancer's selected node; **Nodes** shows probe results; **Journal** has lines.
- From a LAN client: Telegram, YouTube, Instagram, a `.ru` site, a speed test.
- On the router: `ubus call vectra status`, `ubus call vectra diagnostics`,
  `logread -e vctl | tail -50`, `free -m`.
- Try the controls: pin a balancer to a node and back; switch a location and
  back (xray restarts: ~3 s without internet); set the probe interval.
- `logread -e vectra-controller-pro-deadman` — the dead-man's own log — says
  nothing at all on a healthy router.
- DNS through the tunnel (0.6.0-r8+): `nft list chain inet vctl dns_steer`
  shows dnsmasq's uid redirected to :10053, `logread -e vctl | grep resolver`
  says "the router's resolver asks through the tunnel", and from a LAN client
  `nslookup www.instagram.com` answers a real address (the ISP forges
  NXDOMAIN for it on the open path, even for 8.8.8.8).
- Memory (0.6.0-r15+): `logread -e vctl | grep 'direct destinations'` says
  how many ranges the kernel routes straight out (on the fleet, the
  geoip:DIRECT list, ~14.8k ranges when memory allows);
  `nft list counter inet vctl vctl_direct_new` grows as the LAN opens Russian
  sites, and `grep -c 'mark=268435456' /proc/net/nf_conntrack` counts the
  connections the kernel carries. `cat /proc/$(pidof vctl)/oom_score_adj` is
  -800, xray's 0. On a small router do not list the table or the set, and do
  not `nft get element` in it: each reads the whole direct set back, ~23 MB
  of nft — `nft -t list table inet vctl` for the rules.

## 6. Finish or roll back

Kept (`vectra keep` — a trial ends with the 04:30 reboot), leave it running
overnight to see that reboot and the 00:00 subscription refresh (the router's
location choice and pins must survive both). PassWall2's own subscription run
restarts PassWall at 00:00 too; with its switch off that starts nothing:
`pgrep -f /tmp/etc/passwall2/bin` finds nothing the next morning. To end the
test: step 4's way back, then `opkg remove vectra-controller-pro`.

## 7. A router without PassWall2 (route_source 'native', 0.6.0-r17)

A router that routes by the operator's policy (`route_source 'passwall'`)
can drop PassWall2 altogether: vctl then keeps the policy itself
(`/etc/config/vectra_route`, imported once from `/etc/config/passwall2` as it
is), generates xray's configuration from it, refreshes its subscription and
its geo files on the policy's own schedule.

Read-only checks first, on the router as it is:

```sh
vctl passwall-render -compare   # "identical": vctl's generator and PassWall's agree on this policy
vctl passwall-render -native    # renders and passes xray -test (installs nothing)
```

`-compare` names every JSON path where the two differ (never a value: the
documents carry the subscription's keys). What vctl does not port — fragment
and noise, pre-proxy, balancers, cores other than xray, protocols other than
vless/vmess/trojan/shadowsocks — is refused by name, and the router keeps
what runs. From the subscription itself vctl reads VLESS links (all the
fleet's provider serves); a line of another scheme is skipped, and the log
says so.

Then (from a panel terminal job, detached: never restart vctl in its own job):

```sh
uci set vectra-controller-pro.main.route_source=native && uci commit vectra-controller-pro
(setsid /etc/init.d/vectra-controller-pro restart </dev/null >/dev/null 2>&1 &)
```

Check: `logread | grep 'route policy'` says `generator="vctl's generator"`;
`ls -l /etc/config/vectra_route` is `-rw-------`;
`/usr/share/vectra-controller-pro/route-geo/` has geoip.dat and geosite.dat;
the LAN's sites open as before. The subscription refreshes at its own time
(`uci show vectra_route | grep update_`), or at once with the panel's
refresh-subscriptions job; each slot's move is logged ("a slot moved to the
refreshed node", with what matched).

Only then PassWall2's packages go — not xray-core, not dnsmasq-full — and
only with vctl 0.6.0-r18 or later: r17 kept the geo directory PassWall's
generator writes into the config (`"env": {"XRAY_LOCATION_ASSET":
"/usr/share/v2ray/"}`), which a recent xray applies over vctl's, and the
test router's xray stopped starting when the packages took that directory
(`grep -c XRAY_LOCATION_ASSET /var/run/vectra-controller-pro/xray.json`
must say 0):

```sh
opkg remove luci-app-passwall2
opkg remove chinadns-ng geoview tcping v2ray-geoip v2ray-geosite
```

Way back: reinstall luci-app-passwall2 (its configuration: the backup, or
`/etc/config/vectra_route`, which is PassWall's format), then
`route_source 'passwall'`.

## 8. Bug reports (vectra-reporter)

The reporter (ADR-0007) comes with vctl from the pro feed. On the router:

- `vectra-reporter status` — what waits in the spool, and the last error;
- `vectra-reporter test` — a TEST report now (needs the router's key in
  Connect's registry, as its signed subscription UA does);
- `uci set vectra-reporter.main.enabled=0 && uci commit vectra-reporter` —
  nothing collected or sent.

Until Connect takes `POST /errors/router`, reports wait (404: retried hourly,
kept 7 days).

## 9. A native router to the subscription's engine (route_source 'provider', 0.6.0-r21)

Native routing pins each slot to one node, with no health checks: a dead node
is a dead slot until a person moves it. The subscription's own documents
balance over many nodes, and vctl 0.6.0-r21 moves a balancer off a node that
stops answering in seconds (the failover watchdog). To move a native router:

```sh
vctl route provider          # a dry run: the entry, what it carries (counts only), the UCI change
vctl route provider -apply   # the entry kept, My sites → through the VPN extended, route_source 'provider'
```

Then, detached (never restart vctl in its own job):

```sh
(setsid /etc/init.d/vectra-controller-pro restart </dev/null >/dev/null 2>&1 &)
```

Check: `ubus call vectra status` says the engine is running and
`ubus call vectra entries` names the chosen entry; the LAN's sites open;
the only rule left on the Russian bridge is YouTube's
(`jsonfilter -i /var/run/vectra-controller-pro/xray.json -e
"@.routing.rules[@.balancerTag='BL-RU'].domain[0]"`); `vctl api balancers
BL-MAIN` names a principle and no override. The watchdog's lines are
`logread | grep -e 'node failing' -e 'balancer back'`.

Way back: `uci set vectra-controller-pro.main.route_source=native && uci
commit vectra-controller-pro` and the detached restart — the native store
(`/etc/config/vectra_route`) was left as it was. The sites carried into My
sites stay there, harmless.

## Known limits

- **Native routing re-points a slot whose node left the subscription** —
  to the same country by the same path, then by another, then to the main
  slot's (WorldProxy's) node, then to «Авто Самый стабильный». It never keeps
  a node the subscription no longer lists, and never matches by the bare
  host (the provider moves exits between its bridge hosts).
- **The panel's PassWall edits do not reach a native router.** A job that runs
  `uci set passwall2...` changes a file vctl no longer reads: the policy is
  `vectra_route`, in the same format (`uci set vectra_route...` works).
- **Geo data: vctl reads its own.** `/usr/share/vectra-controller-pro/geo`
  (vectra-geodata) by default, `route-geo` beside it with native routing; a
  config naming the fleet's old `/usr/share/v2ray` gets vctl's own. Only
  `route_source 'passwall'` reads PassWall's directory.
- **The LAN's DNS to public resolvers is answered by the router** only from
  IPv4 LAN sources and IPv6 ULA ones: a client using a global IPv6 source
  address keeps asking its own resolver. `option dns_hijack '0'` turns it off.

- **A PassWall that ran with its boot link off** — an operator's, started by
  hand — gets only its own switch back from the hand-back: the takeover
  stopped it, but its link was not Vectra's to give back, so it is not
  started again.
- **A sysupgrade to an image without vctl**, with Vectra on for good, keeps
  PassWall2's switch at 0 (its config is kept, and nothing is left to turn it
  on): run `vectra off` before such a sysupgrade.
- **An upgrade of vctl hands the router back for a moment.** opkg runs the old
  package's prerm (`stop`) on an upgrade too, and the new postinst takes the
  router again: PassWall and the agent come up and go down within the
  upgrade, the self-update's included.
- A takeover that declines while vctl already runs (a `vectra keep` whose
  start fails) restores PassWall and the agent before procd has stopped that
  vctl: both stacks for a moment. Only the stop waits for vctl to be gone.
- The hold on the old agent's watchdog is a time 10 minutes ahead: a clock
  step is caught by the NTP hotplug at once, and by cron within a minute.
- **DNS through the tunnel answers only what the provider's DNS can.** A
  provider without a `dns` section gets one general server (1.1.1.1, through
  its routing); one whose `dns` hands out fake addresses or falls back to the
  system resolver is left on the open path (the job result says why).
  `option dns_tunnel '0'` turns it off.
- **The rescue's return to proxy does not test the tunnel first.** With the
  tunnel dead for a long time, the router alternates: at least 5 minutes
  direct, then a try through the tunnel (about 3 loops) before going direct
  again. Under the kill switch it never goes direct.
- For the first minute after xray starts, the balancers have not probed the
  nodes yet: first connections can take a few seconds.
- The dead-man's cron line runs every minute, and busybox crond logs each run
  as `cron.err`.
- **A client with its own DNS (DoH/DoT in the browser or the phone) that
  resolves a listed service to an address inside the direct list goes
  straight out**, as it did under PassWall2: xray would have re-resolved the
  name and proxied it. Clients using the router's DNS get FakeDNS addresses
  for listed services and are unaffected.
- **The kernel's direct routes follow memory.** With little free memory the
  direct set is loaded in part (the largest ranges first) or not at all, and
  retried every 5 minutes; what is not loaded goes through xray, straight out,
  as before. `option direct_bypass '0'` keeps everything in xray.
- **When memory stays critical** (MemAvailable under 5% of RAM for 15 s) and
  xray holds 9% of RAM or more (21 MiB on 234 MB), vctl restarts xray: a few seconds
  without the proxy, at most once per 10 minutes, logged as "the router is
  running out of memory".

## The packaging tests on busybox

The init script, the dead-man and the package scripts run on a router under
busybox ash, whose rules are not bash's (a failed redirection on `:` ends
the whole script). The packaging tests run under dash on a workstation, and
under the router's own shell and applets in an OpenWrt rootfs:

```sh
cd router/vectra-controller-pro
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -o ~/.cache/vectra-owrt/openwrt.test ./openwrt/
DOCKER_CONTEXT=colima docker run --rm -v "$PWD/..:$PWD/.." -v ~/.cache/vectra-owrt:$HOME/.cache/vectra-owrt \
  -w "$PWD/openwrt" openwrt/rootfs:aarch64_generic-24.10.8 $HOME/.cache/vectra-owrt/openwrt.test -test.count=1
```
