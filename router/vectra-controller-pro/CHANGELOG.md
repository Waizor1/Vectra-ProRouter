# Changelog

## vctl 0.7.0-r23 — the router wears its owner's brand

One package, several VPN services: the router's name, bot, support, Wi-Fi
network and address on the LAN follow the service its owner pays for
(Vectra, BloopCat), and a router that knows of none calls itself by no
service's name. Colours and layout are the same for everyone.

### What changes, by router
- **Vectra routers**: two visible things — «Поддержка» now opens
  VectraConnect_support_bot (Vectra's support bot), not the bot the claim
  code goes to; and the LuCI menu entry says «VPN», not «Vectra» (see
  Changed, which also rewords three technical errors of the Pro view).
  Everything else looks as before. A new router installed with the plain
  command (what Vectra Connect's guide runs) is labelled `vectra` by the
  installer (see `install.sh --brand` under Added). A router upgraded from
  an earlier vctl keeps being a Vectra router: its uci-defaults labels it
  `vectra` once, when it has a vctl identity — or the old Vectra agent's
  state (`legacy_state_path`: a fleet router of the old agent getting vctl
  by opkg, or a `--standby` vctl that never started) — and no label (UCI
  `main.brand_seeded` marks that it was done, on every router, so a fresh
  box is never relabelled by a later upgrade and a label the owner cleared
  stays cleared). Vectra Connect's own subscription proxy does not forward
  the headers that name the brand, so for Vectra it is the panel's answer
  and these labels that count, not the subscription.
- **BloopCat routers** (a subscription from their bot, or `--brand
  bloopcat`): «BloopCat» in every sentence, in the header and in the report
  for support; the claim goes to BloopCat_bot and support to BloopCat_supbot;
  the done screen offers to rename the Wi-Fi network to `BloopCat-XXXX`
  (a button — the router never renames it by itself); the page is at
  bloopcat.lan. No Vectra anywhere in what the owner sees.
- **Neutral routers** (no brand known — installed with `--brand none` and
  not linked to a service): the Wi-Fi network is named after the
  model, `AX3000T-XXXX`; the page is at router.lan; the claim step shows the
  code alone (no bot to send it to); the program is not named — «VPN» in a
  sentence, «Роутер 0.7.0» for the version, «сервер управления» for what the
  diagnostics reach — and no support link is shown: there is no service to
  write to until the router is linked.

### Added
- **`internal/brand`**: whose router this is, from, in this order of rank,
  (1) the subscription: its bot in `profile-web-page-url` (t.me/VectraConnect_bot,
  t.me/BloopCat_bot), else its WHOLE `profile-title` (`Vectra Connect`,
  `BloopCat`) — never a prefix, so the panel's global title «BloopCat |
  TriadConnect» names no brand; its `support-url` names the support bot;
  (2) the panel's register / check-in answer, which may carry `brand`
  (`ClaimInfo.brand`: `vectra`, `bloopcat`; an unknown id is ignored);
  (3) the installer's label (UCI `main.brand`). A failed, stub or
  unrecognised subscription never changes the brand. Persisted in
  `state.json` (`brand`, `brand_source`, `brand_support`); only the daemon
  learns it — a one-shot `vctl apply-local` never writes it.
- **`status.brand`** (`id`, `name`, `bot`, `support`, `lanName`, `site`) and
  **`setup.wifi.rename`** in the router's UI API
  (`ui/contract/README.md`, «Brand»). The UI puts `{brand}` into every
  sentence, the Russian endings agree with it, and a page that has not yet
  heard from the router remembers the last brand it saw — and names nothing
  until it knows.
- **`install.sh --brand vectra|bloopcat|none`** labels the router (UCI
  `main.brand`) until its subscription names a brand. The plain command is
  Vectra's installer (the one router.vectra-pro.net serves and Vectra
  Connect's guide runs without a flag): it labels a router that has no label
  yet `vectra`, and leaves a label put on earlier as it is — a BloopCat
  router the installer is run on again stays BloopCat. `--brand bloopcat` or
  `--brand vectra` replaces the label; `--brand none` clears it (neutral). An
  unknown brand or a missing value is refused before any change (exit 3); a
  label that could not be written or cleared is the warning
  `BRAND_NOT_WRITTEN` (exit 2).
- **LAN names**: vectra.lan, bloopcat.lan and router.lan answer on every
  router — the brand is learned after the package is installed — and the
  page shows its own brand's.

### Changed
- **The LuCI menu entry says «VPN»** (it said «Vectra»): it names no service.
- **Technical errors name no service.** The Pro view's «the program on the
  router does not respond» / «no access to it» / «update the package» say
  so on every router (they said «Модуль vectra»), with the package id a
  technician types (`vectra-controller-pro`) named as a package; the
  `would_idle` detail of `set_power` says «vctl would carry no traffic
  yet» (it said «Vectra …»).
- **Unbinding forgets the brand** the router learned (from the claim or the
  subscription) with the rest of the previous owner's state; it falls back to
  the installer's label, else neutral.

### Not changed
- Protocol strings: `VECTRA:R1`, the `VectraRouter/` user agent, `x-vectra-*`
  headers, the ubus object `vectra`, the UCI package `vectra-controller-pro`,
  file names.
- The Vectra router's wording, wordmark, colours and addresses
  (my.vectra-pro.net, vectra.lan) — pinned by the UI tests.

## vctl 0.7.0-r22 — port forwarding, with presets, from the router and from Vectra Connect

The owner's wish (06.10): open a port to a device at home — a game server,
a console, a NAS, remote desktop — simply, even in the Pro view, and choose
whether that device's own traffic goes through the VPN or past it.

### Added
- **Port forwards.** A rule is a device (its LAN address), a port or a range
  that is the same outside and on the device, TCP / UDP / both, «through the
  VPN» or «past the VPN», on or off. Stored as native fw4 `redirect`
  sections named `vectra_pf_<id>` (they work with vctl stopped and show in
  LuCI); vctl touches only its own sections, checks every other WAN redirect
  and the router's own WAN services for conflicts, refuses an address outside
  the LAN or the router itself, and keeps at most 32 rules.
- **Safe apply.** One uci batch in a private save directory under a lock,
  refused while LuCI has uncommitted firewall edits; if the commit or the
  firewall reload fails or times out, the previous config is put back and
  reloaded (the whole reload process group is stopped on a timeout), and the
  answer says so — a «failed» change never goes live later.
- **«Past the VPN» for a device.** Its own new connections leave directly
  (open NAT for consoles and games); blocked sites still go through the VPN
  (their FakeDNS addresses are never sent past) and so does its DNS (53/853,
  whatever the DNS hijack setting). The kill switch wins: while it is armed
  the device stays on the VPN, and `directActive` says so honestly — so does
  a stopped Vectra or a data plane set that could not be written.
- **Presets.** 35 from official sources — games (Minecraft Java/Bedrock,
  CS2, Rust, Valheim, Terraria, ARK, Palworld, Factorio, Satisfactory,
  Project Zomboid, Enshrouded, Don't Starve Together), consoles (PlayStation,
  Xbox), remote access (RDP, VNC, SSH, AnyDesk, Steam Remote Play, Sunshine),
  media and files (Plex, Jellyfin, Emby, Transmission, Synology DSM, web
  server, Nextcloud), home and cameras (Home Assistant, Hikvision, Dahua),
  voice (TeamSpeak 3, Mumble), your own VPN server (WireGuard, OpenVPN). A
  short wizard with six popular tiles, «All presets» with search and
  categories, «Custom port». Where a game's docs don't name the protocol the
  owner picks it — never a silent TCP+UDP. ru / en / zh.
- **Vectra Connect.** Action `set_port_forwards` (capability
  `set_port_forwards`) and telemetry `portForwards` (rules, LAN devices
  without MAC, `cgnat`, `directActive`) — contract
  `docs/superpowers/specs/2026-10-06-port-forwards-contract.md`.
- **CGNAT.** One line when the WAN address is the provider's shared one or a
  private one: nothing from the internet reaches a forward then.

### Not in this version
- IPv6 «open a port»; inbound THROUGH the VPN (the provider's exits forward
  nothing to us).

## vctl 0.7.0-r21 — the VPN no longer needs the panel to stay up

The owner's rule: the internet and the VPN keep working while the panel is
unreachable — the VPS down, its address blocked, its certificate expired —
for hours or days. An architecture review found the one place they did not
(read in the code; not yet seen live — drill plan for 1111 alongside).

### Fixed
- **The firewall is confirmed by the router's own proof.** Every ruleset
  (re)programming arms the 90 s commit-confirm deadman, and until now only
  the panel disarmed it: its `/healthz` right after the apply, or the next
  successful check-in. Without the panel each ruleset was reverted 90 s
  later and loaded again by `ensureDataPlane` — after the nightly 04:30
  reboot, a DNS-watch correction, a WAN resolver change, the rescue's way
  back — so the VPN flapped every ~2 min for as long as the panel stayed
  away (a fake-clock daemon test counts 180 reverts in 6 h on r20). Now
  every programming, a panel job's included, is confirmed by local proof
  first: the rescue's public health URLs on the control plane's own marked
  client (the panel probe's path, without the panel), or the LAN's way
  through the tunnel; tried again every 10 s until 5 s before the deadman
  wakes. The panel still confirms too — its probe while it is not known to
  be down, every successful check-in — but nothing waits for it. A ruleset
  after which the router can prove nothing is still reverted.
- **…and only with the LAN clients' path whole.** Those proofs (and the
  check-in) leave on the marked sockets, which the output chain returns on
  at its first rule: they never crossed the LAN's path, so a ruleset that
  broke it was confirmed in seconds (the panel's confirmation had the same
  blind spot). With xray running, a proof now confirms only if the loaded
  prerouting has the TPROXY rule to xray's port, the fwmark rule and its
  local route are in the kernel, xray listens on the port, and over 2 s
  neither `vctl_tproxy_escaped` nor (kill switch on) `vctl_killswitch_drops`
  climbs. Structure and counters, not the nodes: dead nodes stay the
  rescue's business and do not bring the flapping back.
- **A confirmation never lands after the revert**: the deadline runs from
  the deadman's arming (taken before the apply), the last try starts 27 s
  before it wakes, and a proof back after timeout − 5 s writes nothing.
- **The rescue's mode survives a reboot without the panel.** A transition
  decided in the poll was written to `state.json` only with a successful
  check-in's state; without the panel a reboot started from the mode before.

### Added
- **Check-ins back off from a panel that does not answer**: none after one
  failure, then doubling from two polls, at most 5 min, drawn between half
  and the whole; meanwhile each poll asks the panel's `/healthz` (3 s), and
  the check-in goes as soon as it answers — back within a poll of the
  panel's return, not 5 min later. Only the exchange waits; the rescue, the
  data plane and the DNS watch run every loop.
- **The panel's known address.** When neither the direct resolvers nor the
  system's give the panel's name an address that answers, the control plane
  dials the last address that answered, then the owner's (UCI
  `list control_ip`), then the built-in one (api/router.vectra-pro.net ->
  72.56.14.52). Each address counts only after a completed TLS handshake
  verified for the name (an ISP stub accepting :443 does not stop the
  next), and only such an address is remembered.

### Audited, unchanged (keeps working without the panel)
- A failed check-in only logs; the router is released only on the panel's
  explicit answer, never for want of one. Claim codes rotate locally.
- The provider's subscription (router-sub, behind the panel's Caddy): a
  failed or non-2xx/non-JSON refresh keeps the last good document and
  render, tried again in 30 min; native subscriptions and geo files keep
  theirs too. After a reboot the render is rebuilt from the documents on
  /etc, never fetched.
- `deadman.sh` watches vctl's process, the trial's deadman its own minutes:
  neither asks the panel.

## vctl 0.7.0-r20 — the router's resolver no longer asks a private WAN resolver over the open path

Seen live on 2026-10-05 (artem-lutfulin, r19, right after the update): the
router's resolver answered www.facebook.com and www.instagram.com with the
ISP's forged NXDOMAIN and facebook.com, www.youtube.com, chatgpt.com with
real addresses, minute by minute mixed with FakeDNS, while xray's DNS inbound
answered every one of them with FakeDNS. 1111 (same version) was clean.

### Fixed
- **The WAN's own resolvers are redirected wherever they are.** The
  redirect into the tunnel took dnsmasq's upstream queries to public
  addresses only. artem's router sits behind the provider's box, whose DHCP
  hands out two public resolvers and its own 192.168.x.1: dnsmasq asked that
  one over the open path beside the redirected ones (conntrack: replies from
  port 53 of 192.168.x.1, the public ones from 10053) and cached whichever
  answered first, the ISP's forged answers included. 1111's resolvers are
  all public, so it never showed. Now the WAN's resolvers are redirected
  by address too, and over IPv6 a link-local or ULA one is refused with the
  public ones. Only the WAN's: `resolv.conf.auto` lists every netifd
  interface's servers under `# Interface <name>`, and only those of the
  firewall's `wan` zone are taken (without such a zone, those of the
  interface the default route leaves by; neither known, none). An address
  reached through a LAN device is never taken, so an owner's Pi-hole or
  AdGuard on the LAN, a corporate resolver, a WireGuard peer's `dns` outside
  the wan zone, a `server=` — all are asked as before. Not a warm-up after an
  xray start: xray's DNS never fell back outside the tunnel for these names.
- **New WAN resolvers under a redirect already in force empty the cache.**
  They are part of the redirect's fingerprint — sorted, each once, the IPv6
  ones only where they are refused, so a reorder is no change: a DHCP
  renewal that brings a new one reprograms the table and empties dnsmasq's
  cache once. It is seen at the next poll that compares the table, so for
  up to one poll dnsmasq may ask the new resolver over the open path; the
  flush then drops whatever it answered. A WAN or PPPoE flap that empties
  `resolv.conf.auto` changes nothing for 60 s: the last resolvers stand in,
  so a flap does not reprogram, flush and re-render twice.
- **The WAN's own resolvers are the direct names' last fallback.** With its
  private resolver in the tunnel too, a router behind a box resolved its
  nodes' and its panel's names only over TCP — 8.8.8.8, 77.88.8.8, the box
  last — where a network keeps the outside's DNS closed. xray now also asks
  the WAN zone's IPv4 resolvers over plain UDP, by a rule of the router's
  own (`vctl-dns-wan`) to a plain freedom (the provider's, else
  `vctl-direct` added), never the tunnel — LAST, after the TCP ones: a WAN
  resolver may be the ISP's, which forges answers for blocked names, and a
  forged node address would keep the tunnel down. Proxied names still get
  FakeDNS. Needs xray's per-server DNS `tag` (in 26.3.27).
- **The resolver's flows follow the redirect in and out.** Drill dr2 on 1111
  (xray killed every 3 s): the DNS watch took the redirect out within
  seconds, yet the LAN resolved nothing for ~40 s more. A NAT redirect is
  bound to a connection at its first packet and lives with it — an answered
  UDP flow three minutes past its last packet (~180 of them, answered from
  127.0.0.1:10053) — and dnsmasq reuses its source ports, so its queries kept
  going to the dead inbound with the rule gone. Taking the redirect out (or
  unloading the data plane) now forgets those flows through ctnetlink before
  the cache is emptied; putting it back forgets the router's own DNS flows
  that went out unredirected, so no query rides one past the flush. Without
  ctnetlink they end on their own, as before. The same when the table goes
  without the daemon: `dataplane-teardown.sh` (stop, the hand-back before an
  update, removal) and the commit-confirm deadman run the new
  `vctl forget-dns-flows` after it — best effort, bounded to 5 s, always
  exit 0, skipped without vctl.
- **The box's admin page by name.** `my.keenetic.net`, `tplinkwifi.net`,
  `tplinklogin.net`, `fritz.box`, `router.asus.com`, `routerlogin.net`,
  `miwifi.com` are asked of the WAN's PRIVATE resolvers alone (the box), first.
  dnsmasq's rebind protection (on by default) still drops a private answer
  unless the owner allows the name (`rebind_domain`).

## vctl 0.7.0-r19 — the Vectra app no longer says "unknown" every other minute

Seen live on 2026-10-05 (vctl r18, 1111 after its nightly reboot): about
half of the check-ins carried no `connect.verdict` — "ok" and nothing,
45 s apart — so the Vectra app showed the router as "unknown" with the VPN
working (Cloudflare FR, YouTube 204, Instagram 200).

### Fixed
- **A route published during the check-in's gather counted as from the
  future.** The verdict judged the failover watchdog's route (published
  every 2 s), the observatory's probe time and the exit check's egress
  against the moment the gather began; one published while the gather ran
  is later than that and was rejected, so the verdict (and the country) was
  left out. With a 45 s poll against the 2 s watchdog the check-ins fall in
  and out of that window by turns: every other one after a reboot set the
  phase. Now an observation is fresh when it is no older than its age limit
  at the gather's start and no later than the judgement itself; a time
  later than now is still no observation.
- **One check-in that cannot judge keeps the last verdict.** A read that
  cannot judge the tunnel right now reports the last verdict the router did
  judge, with its country, published with the time it was judged: the
  check-in's freshness gate ends it 2 min after the judgement
  (`connecttelemetry.MaxObservationAge`), however many check-ins hold it.
  The hold is one owner's and one route's: a claim, a release, a location
  switch or the watchdog's move ends it; a route not readable at the moment
  keeps the verdict without the country. With no configuration to judge,
  the verdict stays out (unknown) as before.

### Deploy
No change to the check-in or the panel contract. The panel keeps the last
measured verdict over a gap of at most 3 min for routers older than r19
only (they have the race); r19 and later are taken as they report.

## vctl 0.7.0-r18 — a router just claimed in the Vectra app is on the VPN at once

Seen live on 2026-10-04 (vctl r17): a router released and claimed again by
code went direct 20 s after the claim, the owner's first config arrived while
it was direct, and the LAN sat off the VPN for two minutes — Instagram dead
behind the ISP's forged DNS. Back on the proxy, dnsmasq still held what it
had answered meanwhile, and those sites kept going around the tunnel until
their answers expired.

### Fixed
- **The rescue judged a tunnel that did not exist.** A released router (or
  one claimed and not yet given the owner's config) has no xray and no data
  plane; its probe "through the tunnel" goes out the WAN and fails by
  design. With no supervisor started xray counted as settled, so those
  failures were conclusive, and 20 s later the rescue sent the router direct
  — then held the owner's first config off the VPN for its cooldown. Now
  with no config there is nothing to judge: no failures are counted and an
  old run is cleared. After an apply the failure window starts again and the
  rescue waits xraySettle (15 s) for the new config before it counts
  anything. A dead tunnel still goes direct as in r15: three failures
  spanning 20 s once xray has settled; direct mode still keeps the internet.
- **An apply in direct mode no longer loads a dead tunnel.** r17 loaded the
  data plane on every changed apply, direct or not, while the rescue went on
  saying "direct". Now, in the rescue's direct mode, an apply loads it — and
  the rescue judges the new tunnel from proxy mode — only with other nodes or
  credentials than the ones it left (at once, unless returns have already
  failed in a row: then after the backoff's cooldown, up to 32 min), or, with
  the same nodes, when the rescue itself would go back (cooldown over, a node
  answered since). Otherwise the config waits on disk for the rescue's own
  way back, and the LAN keeps its internet; a run of applies of dead nodes
  does not swing it.
- **The operator's direct mode is the operator's.** It is recorded as such
  (state `rescue.source: "operator"`; an r17 state file is recognised by its
  reason) and lasts until the operator reconnects: neither an apply nor the
  rescue's way back ends it any more.
- **The way back waits for the new xray.** A reload only signals the old
  xray, which answers on its DNS inbound a moment longer. The rescue's return
  and the operator's reconnect now reload xray and wait (at most 10 s) for
  the new process before loading the data plane; an apply waits the same way.
  An xray that does not come back leaves the router direct (the rescue) or
  as it was (a reconnect fails).
- **Back from direct mode, the resolver kept direct-era answers.** The cache
  was emptied on the way into direct mode, not on the way back. Now every
  time the DNS redirect goes back in — the rescue's return to the proxy, an
  operator reconnect, an apply after a claim, the DNS watch putting the
  redirect back once xray answers — dnsmasq's cache is emptied after the
  redirect is in place: no real address, and no address the ISP forged for
  a blocked site, survives. The rescue (and reconnect) reload xray first and
  load the data plane second, so FakeDNS answers handed out in between do not
  lead nowhere after the reload. A release empties the cache too (the LAN
  goes out directly, xray's FakeDNS addresses lead nowhere).
- **A restarted xray's FakeDNS answers no longer outlive it.** Any new xray
  start while the DNS redirect hands out FakeDNS addresses — the reload after
  a changed subscription, a location change, the failover watchdog, a crash —
  empties dnsmasq's cache once the new xray answers, once per start.
- A flush for the same reason is not repeated within 5 s unless the
  resolver's path changed in between: every return of the DNS redirect is
  its own flush.

### Deploy
No change to the router UI, the check-in or the panel contract: nothing to
deploy first.

## vctl 0.7.0-r17 — Wi-Fi per band from the Vectra app, and «Авто» says where it goes

### Connect: Wi-Fi per band
- **`set_wifi` takes an optional `band`** (`"2g" | "5g" | "6g"`, the ids
  `connect.wifi[].band` already reports): only that band's access point
  radios get the name and password. No band is unchanged — every access
  point radio. A band this router has no access point on answers
  `unsupported`; any other band value is `invalid_params`. Advertised as the
  capability `set_wifi_band`, exactly when `set_wifi` is.
- **The owner's Wi-Fi readback stays the owner's.** After a one-band change
  the check-in reveals that band's password plus only what the same owner's
  earlier changes covered — never another band someone else set. The secret
  and rollback path (forget → apply → verify → mark, the Wi-Fi lock, the
  private rollback job) is unchanged.

### Router UI
- **«Авто» and a country no longer read as two choices.** Where the server
  is shown (the simple view's server card, the Pro overview, «Рекомендуем»
  in Серверы и сервисы), «Авто» carries the line «выбирает лучший сервер ·
  сейчас: 🇵🇱 Польша»; a server chosen by name shows just that server. The
  separate «Сейчас через …» line is gone; the watchdog's «moved» and «does
  not open blocked sites» notes stay.
- **A stale service choice says where the service runs meanwhile**: its
  default by country, or the main VPN; for «Нейросети», Kazakhstan when the
  router runs that — as the Connect check-in already reports it.

### Deploy
The panel overlay must carry `set_wifi_band` among its known capabilities
before an r17 router's per-band Wi-Fi is relied on. An older panel just
drops the flag (unknown capabilities are dropped since r16) and keeps
setting every band at once — safe in either order.

## vctl 0.7.0-r16 — a Pro view an owner acts on, and services the Vectra app can pick

### Router UI: Pro and the journal
- **Pro is Обзор · Серверы и сервисы · Маршруты · Мои сайты · Настройки ·
  Журнал.** The read-only Nodes tab is gone; the tags, counters and
  per-node addresses it and the overview's technical details showed now go
  into the support report, not onto the screen. Routes name their nodes and
  backups by human names ("Резервный канал", not "Hysteria2"), with delays
  in one column and Pin/Unpin. Settings is new: Wi-Fi, router password,
  support access, probe interval, About.
- **Design pass.** The overview names the server as the simple view does;
  the restart / turn-off / «Включить Vectra» buttons are the status card's
  own row, across the card on a phone; Настройки and Сервисы fill the width
  without empty halves.
- **Journal** opens on warnings and errors and folds repeated lines; only
  vctl's own `time=… level=…` lines are re-levelled by their slog level —
  another daemon's line that merely says `level=` keeps its syslog level.

### Connect: services
- **The check-in says more about each service** (`connect.services[]`):
  next to `entryId` (unchanged: where it runs now, the «Нейросети» Kazakh
  default folded in, null for the main VPN), optional `auto` (no owner
  choice), `entries` (cached locations that carry the service — what
  `set_service` accepts; absent when unknown, never cut short past 200) and
  `stale` (the owner's location does not run now; `entryId` is the default
  it runs on meanwhile). A country chosen in the router's own UI reports
  `auto: false, entryId: null`. `entries` is worked out once per cache, each
  location parsed once.
- **`set_service` with `entryId: ":auto"`** takes a service back to its
  default (for «Нейросети», Kazakhstan); `null` stays «as the main VPN».
  Advertised as the capability `set_service_auto`.

### Fault tolerance
- **A service location that stopped running no longer freezes the router.**
  One gone from the cache, or no longer carrying its service, refused every
  render (`unknown_entry` / `service_path_unavailable`) and with it every
  later change. The render now skips that choice — the service takes its
  default path — logs it once at WARN and keeps it, to run again when the
  location does; a new choice that cannot run is still refused. A cache
  that is there but cannot be read now (I/O, vault) moves nothing: that
  render waits, as before.
- **A single failed tunnel probe logs at INFO**; WARN from the second in a
  row.

### Deploy: panel first, this once
The deployed panel rejects a check-in carrying a capability it does not
know, so the panel from this release goes out before any r16 router sends
`set_service_auto`. That panel drops unknown capabilities instead, so the
next flag needs no such order.

## vctl 0.7.0-r15 — a failed VPN never takes the internet with it

Fault drills on a live router (1111, 2026-10-04), a LAN client measured every
two seconds: xray killed once, xray killed in a loop, every node blocked, the
panel cut off. The owner's rule: when the VPN fails the internet stays, the
VPN comes back by itself.

### Fixed
- **xray down: the router's DNS left the dead inbound only at the polls.**
  TPROXY already fell through to the open path within a second, but dnsmasq
  kept asking xray's DNS inbound until dnsDeadAfter polls (two minutes): the
  LAN resolved nothing for 90 s in the crash-loop drill. Between the polls
  the loop now asks the inbound every 3 s (dnsWatchDue); three misses in a
  row take the redirect out at once, without waiting for an answer, and the
  first answer puts it back.
- **Every node dead: 147 s without internet before the rescue went direct.**
  After a failed probe through the tunnel the rescue looks again every 10 s
  (rescueRecheckDue), not once a poll; three failures take about half a
  minute.
- **Direct mode went back to a dead tunnel, and could not leave it again.**
  After its cooldown the rescue retried the proxy with every node still
  blocked (116 s without internet), and the same cooldown held the way back
  out. Now the way back waits for a node of the main balancer, its reserve
  or the borrow pool to have answered AFTER the router left — by the
  connection table (xray's own dials, the observatory's included, answered
  or not) or the observatory's last_seen_time where it gives one: its
  "alive" alone is the verdict of its last round, which can predate the
  outage by the whole probe interval — the second drill round went back to
  a blocked tunnel on exactly that. With no answer at all for 10 minutes it
  tries once anyway, the cooldown's doubling spacing the next tries.
- **The rescue's probe could succeed around a dead tunnel.** In direct mode
  the ISP's DNS fills dnsmasq, and www.gstatic.com resolves to a Google cache
  inside the ISP — an address the kernel sends straight out. Back on the
  proxy, the probe "worked" with every node blocked and the LAN sat offline
  for five minutes. The probe through the tunnel is now Cloudflare's
  /cdn-cgi/trace, and an answer from the router's own WAN address (learned
  around the tunnel every 10 minutes) counts as none. The way out
  never waits for the cooldown, and the watchdog seeing the main balancer
  down starts the rescue's rechecks without waiting for the next poll.
- `vctl status` (ubus `vectra status`) shows the rescue: mode, failures,
  failed returns, last transition and whether direct mode waits for the
  tunnel; the rescue logs each failed probe through the tunnel.

- **FakeDNS answers outlived the tunnel.** The sites that go through the
  tunnel (Instagram, YouTube…) resolve to FakeDNS addresses (198.18.x) that
  only xray can carry; dnsmasq kept handing them out after xray died or the
  rescue went direct, so exactly those sites stayed dead with the internet
  up. Taking the redirect out and entering direct mode (the rescue's or the
  operator's) now empty dnsmasq's cache (SIGHUP).
- **The rescue looks again every 3 s after a failed probe** (10 s otherwise),
  starts on the watchdog's word that the preferred node stopped answering,
  and needs its failures to span 20 s — so it never beats the failover
  watchdog, which heals a single dead node in seconds. The probe through
  the tunnel goes over IPv4 on a fresh connection, and the tunnel counts as
  working when any trace answer is not the WAN's: at the drill ISP
  cp.cloudflare.com goes out by the kernel while www.cloudflare.com takes
  the tunnel.
- **A vctl killed outright left its xray running, and the next vctl could
  not start its own.** procd respawns vctl without the init script, whose
  start_service was the only place orphans were ended: the new xray
  crash-looped (exit 255, port taken) beside an unsupervised orphan, and when
  the orphan went the LAN had no xray for the supervisor's backoff (~1 min in
  the vctl-kill drill). vctl now ends such an xray itself at start (parent
  init, vctl's own argv), before its supervisor starts.
- **xray down sent the rescue direct.** A failed probe while xray itself is
  down or just restarted (under 15 s) no longer counts against the tunnel:
  TPROXY already lets the LAN past a dead xray, and direct mode then held the
  VPN off for the whole cooldown after xray was back.
- **The cooldown is 2 minutes, not 5** — it holds only the way back, and
  that way now waits for a live node anyway. A return that fails within 5
  minutes doubles the next cooldown (up to 32 minutes); a proxy that holds
  for 10 minutes clears it, so a tunnel the observatory calls alive but that
  carries nothing does not swing the LAN every few minutes.
- The DNS watch never touches the data plane in direct mode (it would run
  the LAN back into the tunnel the rescue left) or under the kill switch,
  and puts the redirect back only once xray has run 15 s: a crash loop no
  longer reloads the whole table, direct sets included, at every restart.

### Changed
- **The support shell is on by default** (UCI `remote_shell` '1' on a new
  router too); the owner switches it off in the router UI, and an upgrade
  keeps that — including a '0' that r14 and earlier wrote on a new router.
- The operator's "enter direct" ends by itself as the rescue's does: after
  the cooldown (2 min, was 5), once a node lives.

## vctl 0.7.0-r14 — a router taken from PassWall2 keeps its VPN, and its owner gets its code in time

What the migration of a live router (netis NX31, 2026-10-03) from PassWall2
and the old agent to vctl ran into: 17 minutes of LAN without a VPN, a claim
code that expired before it reached the app, an installer that would have
put an older xray over PassWall's, and tools that did not work before vctl's
first start.

### Fixed
- **No VPN gap when vctl takes a router from PassWall2 before it is
  linked.** A vctl with no operator config turned on without a data plane:
  the LAN went out directly until the panel's apply came (17 min on the
  router, most of it a claim code passed on by people). Now it routes by
  PassWall2's own configuration — route_source 'passwall', chosen by vctl
  itself, automatic — where PassWall2 carried the traffic (its switch on, or
  the takeover's breadcrumbs saying its switch or rc.d link was) and its
  global node is one of its nodes; one its owner had switched off routes
  nothing — on a built-in operator config: the panel's
  `buildXrayOperatorConfig` without the subscription (`config.Base`, proven
  against the panel's golden file). Nothing is written for it, no UCI and no
  operator config: the choice is made again at each start, the router still
  shows its claim code, and when the panel's operator config arrives (the
  claim's apply) vctl runs it and routes by the provider again, the PassWall
  render running until the provider's replaces it. An owner's route_source
  is never touched; a router without PassWall2 (a fresh install) waits for
  its setup as before. `vectra on` waits for that data plane like a
  configured vctl's, and without it gives the router back to PassWall2.
  `vectra status` says while it lasts (`autoRouteSource`). `vctl
  apply-local` refuses it (it installs the provider's document from the
  operator's config, which there is none of).
- **A claim code is taken for 30 minutes**, shown for 20 (it was 10 and
  2): the router's code went through two people and expired 50 s before it
  was typed. The check-in's expiry carries the grace, as the panel keeps it.
  8 characters of base32 against the backend's ~5 guesses a minute stay
  safe (`internal/claim`).
- **`vctl passwall-render [-compare]` works before vctl's first start**: it
  renders agent.json from UCI as the init script does, and uses the base
  operator config when there is no panel's yet.

### Improved
- **`vectra status` shows the claim code** while the router is not linked —
  until when it is taken and the link that opens it in the app (`--json`:
  `claim {code, expiresAt, link}`); operators dug it out of `ubus call
  vectra setup`. The setup's `botUrl` is no longer null before the first
  check-in: Vectra Connect's mini app,
  `https://t.me/VectraConnect_bot/start?startapp=rt_<code>`.
- **`vectra on` says when it would carry nothing** — no operator config and
  no PassWall2 to route by: the LAN would go out directly, without a VPN,
  until the router is linked — before anything is switched, and asks for
  `--force`. The router UI asks the same: `status.power.wouldIdle` says it,
  set_power refuses `{"on": true}` with `would_idle` unless `"force": true`,
  and the page sends that only after its dialog has said the internet goes
  without a VPN until the router is linked.

### Installer
- **An xray put on the router by hand is kept**: with no xray-core package
  (PassWall2's binary, swapped in), or over one older than the minimum, the
  xray-core of the binary's own version, from whichever feed has it, goes in
  first, checked against its feed's SHA256 — opkg would have put the pro
  feed's older one over it. No such package, no SHA256 in its feed's list, a
  binary below the minimum, or one that does not run: refused, the binary
  untouched. Over a package at the minimum or newer, opkg leaves both alone,
  and so does the installer.
- **vectra-geodata and vectra-reporter on a first install too**, by name:
  the Depends that pulls them is the pro feed's alone, and a feed without
  it left the router without its geo data — xray then refused every route
  by country and service.
- **The version check reads the feed's own list**, not the version the
  installer was signed with («установлена 0.7.0-r13, в фиде 0.6.0-r36»).
- **Exit codes say warnings from errors — and a refusal is now 1, not 2**:
  0 done; 2 done with warnings (named by code in the summary); 1 refused
  (was 2), failed, or installed to carry the traffic and not running — the
  service down or its ubus object silent after a takeover; 3 an unknown
  option. `--check` exits 0 with advice (a DPI tool, little storage left),
  naming it. Whatever reads the installer's exit code: a refusal moved from
  2 to 1, and 2 now means installed with warnings.
- **Free storage against the update's floor** (16 MB on /overlay), from
  the estimate in the checks and from what is left at the end: below it, a
  warning — Connect's «Обновить» would be refused until room is made. An
  xray binary its package replaces counts as freed.
- **The old agent**: `--check` without `--standby` says why it is needed
  (two controllers would check in as one router and take the same
  traffic), checks the rest as with it, and exits 1; Vectra goes next only
  to 0.1.13-r45 or later (`vault-read-v1`), an older one to be updated
  first.
- **`--json`**: JSON lines, ASCII codes only, each short — through the
  panel's ASCII-only output filter (docs/INSTALL.md).
- **The installer's container stand** checks first that the docker host's
  kernel has nftables' `fib` (vctl's ruleset needs it; Docker Desktop's
  linuxkit kernel has none — passwall-retire could never carry the traffic
  there) and says to run it on Colima; its retirement check reads the
  sealed backup (`.tar.gz.vault`, since 0.7.0-r4) through `vctl
  vault-restore`.

## vctl 0.7.0-r13 — crond quiet on the routers that need it

### Fixed
- **The tune's `cron_loglevel` takes the stock levels too.** r12 set level 9
  only where nothing was set, and took any level found for the owner's: on
  1111 it found 7, set by nobody, and left crond logging every job into
  logread's 64 KB ring. Busybox crond logs every job below 9, and 5, 7 and
  8 (LuCI's presets, crond's default) are no one's choice to keep: they are
  now set to 9 like an unset level, the old level backed up, and `vctl tune
  undo` puts it back. 9 and above, anything unusual, and a level changed
  after the tune set 9 stay the owner's.

## vctl 0.7.0-r12 — before the public launch: the provider decides nothing on the router, nothing leaks, the tune goes deeper

### Security
- **The provider's document no longer decides files, ports or log lines on
  the router.** Only `log.access` of the subscription's document was
  rewritten: the provider could point xray's error log at a file, raise
  the level until the log filled RAM, switch on the DNS log, open a reverse
  tunnel into the LAN, move where xray reads its files from (`env`), or
  write a line of its choosing into the router's log with a newline in a
  tag. Now `log` is the router's whole, and the rest is sorted in two:
  - **refused** — `reverse` anywhere, a freedom outbound's `redirect`, a TLS
    certificate or key *file* and `masterKeyLog`, a geo list read from
    outside xray's asset directory, a tag, selector or key with a control
    character or longer than 256 bytes, a FakeDNS pool outside xray's own
    ranges. The router keeps the render it runs;
  - **dropped, the rest applied** (one warning line) — every top-level key
    outside `dns`, `routing`, `outbounds`, `policy`, `stats`,
    `observatory`, `burstObservatory`, `remarks`, `version`, `fakedns` (and
    `log`, `inbounds`, replaced): `api`, `metrics`, `env`, `transport`,
    `$schema`, `assets` (one real entry carries it), any key xray learns
    later — in any spelling; and, with DNS through the tunnel, `dns.hosts`
    entries over the panel or NTP.
  What is allowed and why: `internal/coreengine/xray/provider_guard.go`,
  docs/SECURITY.md.
- **A refusal never costs the data plane.** Every render xray took is kept
  sealed on flash (`xray-last-good.json`, written only when it changes);
  after a reboot that cannot make the render again from the provider's
  document — refused since, or gone — that render runs. Incidents say
  which (`PROVIDER_REFUSED`, `PROVIDER_PARTS_DROPPED`,
  `RENDER_RESUME_FALLBACK`, `RENDER_RESUME_FAILED`). The installed render is
  redone once at the start, under the guard.
- **Nothing speaks to xray's TPROXY port directly.** One connection to the
  router's own address on that port made xray proxy into itself until it
  ran out of descriptors. Such packets are dropped ahead of fw4
  (`vctl_inbound_guard`), in a connection's original direction only; what
  TPROXY delivers, and answers to the router's own sockets, are untouched.
- **xray never dials into the LAN.** A sniffed `Host: 192.168.1.1` or a
  provider's connection sent there is dropped on its way out to br-lan or a
  guest bridge (`vctl_lan_dial`); xray's answers to the LAN's own
  connections pass. Its limits are in docs/SECURITY.md.

### Fixed
- **No zombie per firewall programming.** The commit-confirm dead-man was
  released, never waited for: the test router had two zombies after 20 h.
- **A check-in that changed nothing writes nothing to flash.** state.json
  and its last-good copy were sealed and written twice a minute (~5.7k
  writes a day), nearly always the same bytes. A missing copy of either is
  written again.
- **Maps of the provider's names forget the names it dropped:** the exits'
  countries (kept in state.json for good), the watchdog's last answer per
  node and last resolution per name, and «Нейросети» refusals per document.
- **Connect action receipts fit the router's flash:** their budget was 64 MB
  against ~18 MB of free overlay; it is 2 MB, a receipt older than 90 days
  goes when the archive is next written, and a full budget makes room by
  the oldest receipts — it never refuses the owner's next action.
- **An update's package does not stay in RAM.** It is removed however the
  update ends; at the daemon's start vctl removes its own leftovers in RAM
  (update packages, the vault's temp files — by exact names, older than ten
  minutes, open in no process).

### Improved
- **The tune goes deeper, with the same safeguards** (only what nobody set
  otherwise, a backup first, `vctl tune undo`, `tune '0'`, one log line per
  change): busybox crond's log level 9 (`cron_loglevel`: every job it ran
  pushed the log out of logread's 64 KB), vctl's leftovers in RAM removed
  (`tmp_leftovers`), and zram-swap whose module is for another kernel said
  as `no_kernel_module` instead of a start failing at every run. `vctl tune
  plan` adds where memory and flash go: free memory and swap, the largest
  processes, free overlay, and packages and staging directories under /root
  the operator may remove — never vctl's.

## vctl 0.6.0-r37 — the router’s password, PassWall2 retirement, signed updates and automatic tuning

### Added
- Owner-bound Connect actions select exact provider entries, route services and
  domains, change Wi-Fi, reboot, request verified updates and control auto-update.
  Measured telemetry advertises capabilities; verified Wi-Fi readback reaches
  only the bound owner through confidential HTTPS check-in.
- The setup wizard requires the router’s password when none is set, using
  LuCI’s password change. The main page warns while none is set and offers
  a password change. The page opens at my.vectra-pro.net, http://vectra.lan,
  or the LAN address; local names survive upgrades and leave on uninstall.
- PassWall2 is retired after a day carrying traffic with Vectra switched on:
  its configuration is backed up before removal, xray and dnsmasq are kept.
  A removal by hand is tidied under the same conditions. `vectra off` returns
  traffic to PassWall2 before retirement, and to plain internet afterwards.
- The owner controls support access in the UI. Fresh routers start with it
  off; migrated routers keep their previous access. Opening asks first.
- Automatic tuning sets compressed swap, swappiness, packet steering and
  software offloading only under its hardware and ownership guards. The
  wizard follows the router’s Wi-Fi verdict and explains the tune at the end.
  `vctl tune plan|apply|undo` exposes and reverses the tune’s own changes.

### Security
- Connect action receipts survive reboot and retry without repeating mutations.
  Reboot completion needs a changed boot ID; update completion verifies the
  installed package and runtime. Raw updates refuse signed downgrades.
  Subscription redirects stay on their original
  HTTPS origin, keeping custom headers and device identity off other servers.
- UI responses redact credentials; credential-bearing files use root-only
  permissions. The panel uses HTTPS without redirects; subscription and
  update downloads reject redirects to HTTP. Geo updates stay in the geo directory.
- Controller updates require a trusted usign signature over the router’s
  feed index, matching package SHA256, architecture and requested version.
  Compressed and unpacked indexes are capped at 1 MiB. No package is
  downloaded or installed when validation fails.
- Go 1.26 is declared as the release toolchain. Operator requirements and
  trust boundaries are documented in docs/SECURITY.md.

## vctl 0.6.0-r36 — no restart for one name that comes and goes

### Fixed
- **xray is restarted for servers' names only after an outage.** On the test
  router one server's name resolves at one public resolver and not at the
  other, so the watchdog saw it come and go, and that server is dead for
  real. Right after xray started, r35 would have taken its return for the
  end of an outage and restarted xray — and again after every restart, every
  ten minutes. Now it restarts only when more than half of the servers'
  names went at once (a reboot, the WAN down).

## vctl 0.6.0-r35 — the most out of the hardware: DNS in seconds, swap counted, servers back after a reboot, a door for storms

### Improved
- **DNS no longer piles up in xray.** Every name the router looked up was a
  session in xray that lived two to four minutes after its answer: an idle
  household kept ~250 of them on the test router (750 of xray's 788
  goroutines), and the garbage collector walked them all on every cycle.
  They now close 8–16 s after their answer. On the stand, 240 names left
  xray at 1460 goroutines for minutes; now it is back to 29 within 20 s.
- **The memory guard counts swap.** It acted only once free memory fell
  below 12 MB, by which time zram was full (2 MB left under load on the test
  router). It now also acts when the swap is all but full and less than
  24 MB is free.
- **Servers are back within seconds after a reboot or a long outage.** xray
  probed every server before the router's internet came up, found them dead
  and kept them dead until its next probe — 5–8 minutes, the Russian bridge
  on its fallback meanwhile. The watchdog now watches the servers' names;
  once they resolve again and xray still holds a server dead on a probe made
  while its name was gone, it restarts xray once, so it probes them at once
  (on the stand: alive 17 s after the name resolved). A server dead for
  real is left to xray's own probes — a restart would drop every
  connection for nothing.
- **One device's DNS storm waits at the door.** A LAN device asking the
  router for more than 100 names a second (2000 at once — a heavy page or a
  restored browser session passes) has the rest dropped, and its resolver
  asks again; every other device, and the router itself, are answered. UCI
  `dns_rate` ('0' off).
- **The router's whole door** (UCI `admit_total_rate`): a limit on new
  connections into xray from every device together. Off until it is sized
  on the router itself.

## vctl 0.6.0-r34 — the review's last eight: IPv6 off only outward, the exit country kept

### Fixed
- **IPv6 off refuses only the LAN's IPv6 to the outside.** It also refused
  new IPv6 from the internet to a LAN device and IPv6 routed between the
  LAN's own networks. IPv6 into the LAN's devices is now the router's own
  firewall's to judge, as without Vectra; to the WAN or anywhere unknown it
  is still refused. When the router cannot tell its LAN devices, the refusal
  stays whole — it never guesses in the direction of a leak.
- **The real exit country survives a restart.** After the nightly reboot the
  card says «(выход: 🇵🇱 Польша)» at once, and no server is asked again
  before its day.
- A server found dead in a round is not asked for its country (8 seconds
  saved each); Cloudflare's «XX» (unknown) and «T1» (Tor) are not taken for
  countries.
- While IPv6 is off, no IPv6 ranges are loaded for Russian sites: memory
  saved, and the budget goes to IPv4 ranges.
- The card no longer says «другого сервера нет» while the main traffic is
  held on another server.
- Russian sites in the provider's rules are recognised whatever their letter
  case.
- A country's flag and name are written the same way everywhere.

## vctl 0.6.0-r33 — the watchdog never sends a balancer direct

### Fixed
- **The failover watchdog could send a balancer's traffic out unproxied.**
  When a balancer's only server looked dead, r22's watchdog pointed the
  balancer at its fallback at once — and where the provider's fallback is
  «direct», the traffic left without VPN, with nothing to bring it back while
  it did (the way back needs a new answer from the server). Found as the
  stand's PassWall takeover race, failing since r23. The watchdog now falls
  back at once only where the fallback still goes through a server — the
  provider's next stage, as on 1111 (to the reserve, to the whitelist levels),
  or a server of its own. «Direct» stays xray's own last resort once its
  observatory finds every server dead. 1111 has no balancer falling back to
  direct today; a change of the provider's document could have made one.

## vctl 0.6.0-r32 — the review of r29–r31: the bridge keeps its own addresses; IPv6 off proven

### Fixed
- **An address in the provider's Russian rule keeps the bridge.** r30 read
  every address the provider lists for its Russian bridge (BL-RU) as a
  Russian network and sent it direct. The provider's entry names only
  geoip:ru today; the day it lists a foreign address it bridges on purpose —
  as it did with nnmclub.to among the names — that site would have met the
  filter at home. Only the Russian category goes direct now; anything not
  known Russian keeps the bridge (a Russian address still works through it).
- **An exit that stops answering the location check is asked hourly**, not
  every 10 minutes, and the country known stays shown meanwhile.
- **Updates no longer log «xray exited; will restart».** It was the
  controller's own shutdown stopping xray (1111: the five updates of
  30.09), read as a crash; no memory shortage was involved. It is logged as
  an orderly stop now.
- **The route line keeps «(выход: 🇵🇱 Польша)» in one piece** and a flag with
  its country on a phone's width.

### Proven
- IPv6 off, on the packets (stand `ipv6-refused`): a LAN device's TCP over
  IPv6 is refused by the router in about a millisecond, UDP too; the
  router's own IPv6 address and IPv4 keep working. Its control, with the
  ruleset that carries IPv6, fails both checks as it must. A correction to
  r30's note: while IPv6 is off the router also refuses routed IPv6 between
  LAN segments and new connections from the internet to LAN devices; the
  LAN's link-local and ULA addresses are untouched.
- Russian networks by the kernel, end to end with a real geoip:ru address
  (stand `ru-kernel`).

## vctl 0.6.0-r31 — where each server really leaves; the review's minors done

### Added
- **Where each server really leaves.** 1111, 30.09: «Турция» and «ОАЭ» left
  in Poland. Once a day the router asks a trace through each exit for the
  country it sees; where it is not the country the name says, the server
  card says «🇦🇪 ОАЭ (выход: 🇵🇱 Польша)» and a service's country choice
  «🇹🇷 Турция (выход: 🇵🇱 Польша)».

### Fixed (the review's deferred minors)
- The exit check renders the running document again, never the router UI's
  way through the entries cache: a stale location choice no longer refuses
  it, and a newer cached entry is never installed in its place.
- A service the geo file lost (an update) is marked «нет у роутера» in «Мои
  сайты» instead of looking as if it ran.
- When every server of the main pool is unfit, the card says so — «другого
  сервера нет, поэтому заблокированные сайты могут не открываться» — and
  state.json keeps every unfit exit, so the card knows after a restart.

## vctl 0.6.0-r30 — NNM-Club and the provider's Russian-bridge sites; IPv6 off

### Fixed
- **NNM-Club, Kinozal and other sites loaded forever.** The owner, 30.09:
  «NNM club не открывается — бесконечная загрузка». The provider's Russian
  rule names Russian TLDs and, beside them, sites it sends through its own
  Russian server on purpose — nnmclub.to, kinozal.tv, anime sites: they want
  a Russian address and the filter blocks them at home. «Russian sites
  direct» sent that rule direct whole when any matcher was Russian, and those
  sites into the filter (kinozal.tv even resolved to 127.0.0.1 through
  Yandex's DNS on the open path). A mixed rule is split now: Russian
  destinations — markers, names under .ru .su .by .рф, the bridge's own
  addresses — go direct; the rest keep the bridge and get FakeDNS answers.

- **A healthy exit could be judged filtered** when its node restarted under
  the round (the blocked site asked first failed, the neutral URL after it
  answered). A blocked site's «no» counts only once the exit is known up:
  after the neutral URL answers, the sites are asked again.

### Changed
- **IPv6 is off.** The owner: «IPv6 заблокирован у нас на серверах —
  выключи его и у нас». The provider's nodes carry no IPv6: captured, a
  connection over IPv6 hung at the exit (TCP connected, so no fallback);
  let past, it would leave unproxied. The router now refuses the LAN's IPv6
  to the outside at once — a reset for TCP, admin-prohibited for the rest —
  and its resolver answers IPv4 only, so devices take IPv4 straight away.
  The LAN's own IPv6 is untouched. UCI `ipv6 '1'` carries IPv6 through nodes
  that take it.

## vctl 0.6.0-r29 — whitelist levels are not «не отвечает»

### Fixed
- **The diagnostics named the whitelist levels as dead nodes.** The owner,
  30.09: «он ругается что белые списки не доступны — так они и не работают с
  вайфай». A whitelist level (whitelist-lv1…) is for a mobile network under a
  whitelist regime; on a home connection it does not answer, and that is no
  fault. The dead-nodes check names only the nodes this connection should
  reach.

## vctl 0.6.0-r28 — the exit check made safe (review of r26–r27)

### Fixed
- **An owner's pinned server was deleted** when the exit check left it out:
  the next xray start found the pin impossible and dropped it for good. It
  now waits — kept, not applied while the exit is out — and holds again
  when the exit comes back.
- **A round cut off by its deadline judged exits it never finished** as
  filtered (a false leave-out and two restarts). Such an exit is unjudged;
  the round's budget covers every exit's worst case.
- **A render the loop refused, or a loop busy with a job, waited an hour**
  instead of the next round. The ask is remembered only once the render is
  made; a render made later without the leave-out is corrected.
- **Exits no balancer can lose restarted xray on the same config** (all of
  a balancer's exits through one filtered leg). Only exits a render would
  actually move enter the options.
- **The exit probe's loopback port (10087) was not guarded** like the API's:
  a LAN request xray is tricked into dialling on loopback could pick any
  exit. Now only root reaches it, never xray's own sockets.

## vctl 0.6.0-r27 — «+ Сервис» in «Мои сайты»

### Added
- **A whole service into «Мои сайты»** (spec decision 4): «+ Сервис» under
  the site field searches the router's catalog — the categories of the geo
  file xray runs with: Discord, Instagram/Facebook/WhatsApp, X, YouTube,
  TikTok, Telegram, Gemini, Google Meet/Play, Roblox, HDRezka, sites closed
  to Russia, news, anime… — found by name, id or what people type
  («инстаграм», «дискорд»), the popular first. The chosen service joins the
  list on screen («Без VPN» or «Через VPN») as `geosite:<id>` and is shown
  by its name with a small «сервис». A site the owner typed into the other
  list is more specific and wins over it. The router refuses a category its
  geo file lacks (`unknown_service`) and never offers `private` (the LAN);
  a render drops a service a geo update removed rather than fail.

### Changed
- The exit check asks a healthy exit one request, not two: a blocked site
  answering says it is alive and lets it through; the neutral URL is asked
  only when it does not (≈0.7 GB a month of probe traffic instead of 1.4).

## vctl 0.6.0-r26 — an exit that carries no blocked site leaves the balancers

### Added
- **The router checks each foreign exit itself.** 1111, 30.09: through the
  provider's American exit Instagram, Facebook, X, LinkedIn and Discord ended
  in a failed handshake while Google and the observatory's neutral URL
  answered — the leg from the Russian entry to it lets the filter see what is
  inside — and leastLoad kept handing it connections: «Discord то работает,
  то нет». Every 10 minutes the router asks each foreign exit, through a
  loopback HTTP proxy in xray with one account per exit, for the
  observatory's neutral URL and for blocked sites of three owners
  (Facebook, Discord, X). An exit that answers the one and none of the
  others, while another exit carries them, leaves every balancer — the
  provider's and the services' — and the watchdog never lends it; it comes
  back after two clean rounds. Nobody is judged while no exit carries a
  blocked site (the sites may be down, or the router's own network), and a
  balancer is never emptied. The unfit exits survive a restart: the render
  after a reboot already leaves them out.
- The server card names them: «Не открывает заблокированные сайты: 🇺🇸 США.
  Пока не используется».
- UCI `exit_check '0'` switches it off; `exit_check_site` (list),
  `exit_check_control`, `exit_check_first`, `exit_check_every` replace the
  product's sites, neutral URL and timing.
- Stand mode `exitcheck`: the leg, the LAN before (a third of the blocked
  requests failing) and after (all through the healthy exit), the card and
  state, a vctl restart keeping it out, no exit judged without a witness,
  the exit back after two clean rounds.

## vctl 0.6.0-r25 — the load guards made safe for calls, fair between devices, switchable

From the review of r23's load guards:

### Fixed
- **A call could be taken off the VPN.** The P2P classifier counted UDP
  packets: a UDP flow is "new" until it is answered, so one unanswered call
  (RTP to a relay on a high port) could trip it and move that very flow to
  the kernel mid-stream. Now a connection is judged on its first packet only
  (`ct original packets 1`), never the router's own (`iif != lo`), and the
  exemptions cover calls: STUN/TURN 3478-3497, Zoom 8801-8810, FaceTime
  16384-16402, Google STUN 19302-19309, Viber 4244/5242, Discord's voice
  50000-65535. With the kill switch on, P2P never leaves unproxied.
- **One device could fill a node's bucket for everyone**, the watchdog's
  confirmation and the rescue's probes included: a device is now let 15 new
  connections a second (burst 60) and a node takes 40 (burst 160). The door
  counts connections, not packets.
- **The rate sets never forgot a key** and, full, stopped pacing new nodes:
  they expire (1 min; a node's 30 s after its last dial).
- **A borrow was handed back on no word at all** and flapped: it is handed
  back only when the observatory holds a node of the entry's own path alive
  and its dials answer; a borrowed node whose confirmation failed is not
  borrowed again for 3 minutes.
- **A page load was judged a storm** (16 dials): a storm is now 48 dials
  outstanding at once; a dead node under ordinary load is judged in 2 s.

### Added
- UCI `p2p_bypass '0'`, `admit_rate`, `pace_rate` (new connections a second;
  '0' switches a guard off). SYNs nobody answers leave the connection table
  after 30 s, not 120.

## vctl 0.6.0-r24 — Russian networks by the kernel

### Added
- **Russian sites leave by the kernel, not through xray.** The owner, 30.09:
  «ру сервисы … все в директ отправляй, иначе роутер сдохнет». The
  provider's Russian rules send geoip:ru straight out, but its domain rules
  come first — a blocked site whose address is in a Russian network is
  proxied by name — so every Russian connection went through xray (two
  sockets and ~38 KB each). Now, in the subscription's engine with the
  Russian bridge sent direct and DNS through the tunnel, the router answers
  every name a rule sends anywhere but straight out — the owner's «через
  VPN», the services', the provider's, in order up to the rule that takes
  everything — with a FakeDNS address (198.18.0.0/16, carried by the data
  plane, only those names: skipFallback): such a connection reaches xray,
  which dials the name. The direct scan goes past the rules that proxy by
  name, takes out what an earlier rule proxies by address (Telegram's), and
  the rest of geoip:ru goes into the kernel's direct sets. PassWall2 kept the
  fleet's proxied names apart the same way. A client with its own DoH gets
  real addresses: a proxied site in a Russian network then leaves by the
  kernel, as under PassWall2. UCI `direct_bypass '0'` turns both off.
  On the data-plane stand (`MODE=ru-kernel`): the Russian address left by
  the kernel (no node saw it); a proxied name whose real address is that one
  got a FakeDNS answer and went through the main balancer's node; a name no
  rule proxies got its real address and the kernel.

## vctl 0.6.0-r23 — peak load held, a dead country borrowed around; a country per service

### The night this answers
1111, 2026-09-30 03:42–04:20, a torrent client started on the LAN:
- its tracker storm went through xray to the entry's one German node,
  hundreds of handshakes a second, and the German address stopped answering
  the router at all (279 SYNs in 14 s, none answered) — alive for every
  other client of the provider;
- the watchdog moved BL-MAIN onto its reserve, the reserve was German too,
  and the rescue took the router direct; for 26 minutes nothing tried the
  entry's live Belarusian node, which carried TikTok fine the whole time;
- its peers, which the provider sends DIRECT, went through xray: two
  sockets and their buffers each, and the router was down to 15 MB free.

### Added
- **P2P goes by the kernel.** A LAN device opening connections to non-web
  ports faster than a person does (more than 24 at once, 4/s after) is a
  P2P host for five minutes, and its connections to non-web ports leave
  with the conntrack bit of kernel-direct flows — not through xray. Web
  ports, push and chat (5222-5223, 5228, 8080, 8443), STUN and Discord's
  voice range (UDP 50000-65535) stay with xray. Counter `vctl_p2p_direct`.
- **A storm waits at the door.** A device's new connections past 40/s
  (burst 200) are dropped before xray; TCP asks again in a second, so a
  storm is spread over time instead of choking xray, the node and the
  connection table. Counter `vctl_admit_held`.
- **xray's dials to one node are paced**: past 20 new connections a second
  (burst 80) to one address and port, the SYN is held back by the kernel
  and sent again. DNS is not paced. Counter `vctl_paced`.
- **A dead country is borrowed around.** When a balancer and its whole
  reserve are down — the observatory's word on each, or dials unanswered —
  the watchdog lends it the entry's fastest live node of another country
  (never a Russian exit: it reaches nothing a VPN is for), confirms it
  through the tunnel, and hands it back once the entry's own path answers,
  no sooner than 30 s after.

### Changed
- **A burst is not a death.** With 16 or more of the router's dials to one
  node outstanding at once, the watchdog waits for 8 s of silence, not 2:
  a node pacing a burst answers late, and moving the burst only takes it to
  the next node.
- **Closed connections leave the connection table after 30 s**, not 120
  (`/etc/sysctl.d/90-vectra-controller-pro.conf`): the storm filled 11,383
  of its 15,360 entries.

### Added (the service choice)
- **A server per service.** YouTube, TikTok and Telegram — the few services
  a person really picks a country for — get a select each in the router UI's
  simple view («Сервисы»). By default a service goes where the subscription's
  entry sends it, named by its country («По умолчанию · 🇧🇾 Беларусь»);
  chosen, it goes through that country's nodes of the running entry, and
  everything else stays on the main VPN. The choice is an overlay on the
  provider's document: a balancer over the country's outbounds (leastPing;
  random where the entry has no observatory) whose fallback is the service's
  own path, through a loopback — a dead country falls back to the default,
  never to nothing. The owner's own sites stay above it, the provider's rules
  below; the watchdog moves the overlay like any other balancer. The choice
  is kept with the router's overrides; one the subscription no longer offers
  is not rendered, and the UI says the service runs on its default. A change
  restarts xray (the internet drops for a few seconds), so the UI asks first.
  `ubus vectra services` / `set_service` (rpcd ACL: read / write) refuse
  before the daemon is asked: an unknown service, a country the entry lacks,
  a router routed by the operator's policy (native, PassWall).
  On the data-plane stand (`MODE=services`): TikTok chosen NL went through the
  Dutch node while the rest stayed on BL-MAIN; the Dutch node dropped, TikTok
  was back on its own path after 5.2 s; back to the default, no overlay left.

## vctl 0.6.0-r22 — a one-node main balancer goes onto the entry's own next stage in seconds

### Fixed
- **The failover watchdog had nowhere to move a one-node balancer.** The
  provider now serves «🇷🇺🇪🇺 Авто Самый стабильный» with BL-MAIN as ONE
  node (leastPing over `sticky-de5`, `fallbackTag` `stage-bridge` — a
  loopback into the bridge balancer). The live drill on 1111 (r21,
  2026-09-30 02:03 MSK): the node's answers dropped, the watchdog judged it
  failing — 182 unanswered attempts — and, with no other member, moved
  nothing; xray keeps a lone member for as long as the observatory still
  holds it alive, which with two samples is rounds of probes away. Now such
  a balancer is put on its `fallbackTag` at once — the stage xray itself
  would reach only then — and an override whose member fails too goes there
  as well. Handed back as before: the node answering again after the 30 s
  hold, or the observatory dropping it. Stand: `MODE=failover-stage`, the
  entry's shape.

## vctl 0.6.0-r21 — a server that stops answering is left in seconds; the subscription's own engine

### The outage this answers
On 2026-09-29 the provider's Netherlands route (RU entry, :50055) stopped
answering SYNs. The test router ran vctl's native routing: five slots, each
pinned to ONE node, no health checks. WorldProxy and YouTube sat on the dead
node, so every proxied site and the router's tunnelled DNS were down from
~23:18 to 23:39 MSK — and nothing noticed: vctl, xray, the dead-man and the
reporter were all fine, nothing had crashed. The router UI could not switch
servers either: native routing refuses the subscription's locations.

### Added
- **The failover watchdog.** Every 2 s, in its own goroutine (the daemon's
  loop can be busy for minutes with a terminal job), vctl reads the kernel's
  connection table (ctnetlink) for xray's own connections — from the
  router's addresses — to the nodes of the render; a phone on the LAN
  reaching a node's address is answered by the router's transparent proxy
  and says nothing of the node. A node with two attempts, made since it last
  answered anything, unanswered for 2 s is failing; a balancer that prefers it is re-pointed through xray's API
  (`OverrideBalancerTarget`) at the fastest node the observatory holds alive,
  and one request through the tunnel — to the provider's own probe
  destination — confirms the move (a failed one moves on to the next node).
  The override is let go when the node answers again, no sooner than 30 s
  after the move (a node that answers one moment and drops the next must not
  swing the balancer), or at once when the observatory drops the node — or
  when the node it moved to fails too and nothing is left: the balancer's own
  choice and the provider's fallback chain take over (xray ignores both
  while an override is set). A move xray refuses is asked again, never
  taken as done. The owner's pins are never touched. No probe traffic: it reads what xray
  already does. Logged (`node failing; balancer moved`), and queued for the
  reporter: `PROXY_FAILOVER` (medium), `PROXY_DOWN` (critical: no node of the
  main balancer answers for 60 s and none is alive to move to).
  On the data-plane stand (`MODE=failover`): the main balancer's node
  dropped while the LAN keeps asking — the first request to succeed went
  through the other node after **6.1 s** (budget 10 s); with the watchdog
  off (`MODE=failover-no-watchdog`, the control) none did in 30 s.
  UCI `failover_watchdog '0'` switches it off.
- **Russian sites go direct** in the subscription's documents: a rule to the
  provider's Russian bridge (`BL-RU`) that names Russian destinations
  (`geoip:ru`, `.ru`, `.рф`, `.su`, `category-gov-ru`…) is rendered to the
  direct outbound in place; YouTube's rule keeps `BL-RU` (the provider's
  ad-free path), and the provider's order of rules is kept. UCI
  `russia_direct '0'` keeps the provider's way.
- **`vctl route provider [-entry REMARK] [-apply]`** moves a native router to
  the subscription's engine (entry «🇷🇺🇪🇺 Авто Самый стабильный» by
  default): the owner's WorldProxy/Special sites that the Russian rules would
  take off the VPN (.ru, .su, .рф, .by, addresses in geoip:ru) go to My
  sites → through the VPN, the entry is kept by remark, UCI `route_source`
  becomes `provider`. A dry run by default, by counts only. The native store
  stays on disk: turning back is one UCI switch (docs/CANARY.md, section 9).

### Changed
- **The kernel's connection table is read through ctnetlink**, and from
  `/proc/net/nf_conntrack` on a kernel without it (asked once, then the proc
  file). A proc line without counters (`nf_conntrack_acct=0`) is read too:
  before, such a router's watchdog would have seen nothing.

## vctl 0.6.0-r20 and vectra-reporter 1.0.0-r2 — an upgrade is no hand-back

### Fixed
- **An upgrade handed the router back.** opkg runs the installed package's
  prerm on every upgrade too (`prerm upgrade <version>`, `PKG_UPGRADE=1`), and
  vctl's prerm stopped it: PassWall2 and the legacy agent came back for the
  seconds of the file swap, next to vctl's outgoing xray, until the new
  postinst took them away again. The prerm now leaves an upgrade alone; the
  new postinst restarts the running vctl, and a restart is no hand-back. It
  takes effect from the NEXT upgrade: the one to r20 still runs r19's prerm.
- **The panel's self-update (`update_controller`) killed its own opkg.** That
  opkg runs as vctl's child, and the postinst's restart (the prerm's stop,
  before) ended vctl and, through its context, the opkg — before opkg wrote
  its status and before the job's result was journaled. The job now holds the
  postinst's restart back (`VECTRA_SKIP_POSTINST_RESTART=1`) and restarts vctl
  itself once opkg is done, in a session of its own (setsid). From r20 on
  only: **a router on r19 or older goes to r20 by the installer or a
  setsid-detached opkg, never by the panel's job** — r19's job code and r19's
  prerm still run for that one upgrade, and its stop kills the job's opkg
  before the swap (vctl stopped and disabled, the router handed back, the
  package not upgraded).
- **vectra-reporter's journal was cron's.** A report's lines were taken by
  a mention of "vectra-controller-pro", and cron logs every run of the
  dead-man with its path, once a minute: on 1111 (r19) they were 69 of a
  report's 80 lines, vctl's own pushed out. The lines are taken by the syslog
  tag of what logs for Vectra now (vctl, its init script, the dead-man, the
  reporter), plus what others log of vctl: procd of its instance (a crash
  loop, a kill after SIGTERM) and the kernel of an OOM kill of vctl or xray.
- **vectra-reporter took a kernel's console record for a crash.** A kernel
  that keeps its console in pstore (ramoops' console size) leaves
  `console-ramoops-0` after every boot, and `pmsg-*` is userspace's: every
  reboot would have been reported as one after a kernel failure. Only pstore's
  `dmesg-*` records count now, and only they are taken away — and of those
  only a panic's, an oops' or an emergency's: a kernel told to dump at every
  shutdown too (`ramoops.max_reason`, `printk.always_kmsg_dump`) writes one
  headed `Shutdown#`/`Restart#` on a clean reboot. (The AX3000T keeps
  neither.)

### Tests
- `TestAnUpgradeIsNotAHandBack` (the prerm as opkg runs it on an upgrade),
  `TestUpdateControllerHoldsThePostinstRestartBack`,
  `TestTheSelfUpdateRestartOutlivesVctl`,
  `TestBootTakesOnlyTheKernelsDumpsForAFailure`,
  `TestBootReadsWhyTheKernelDumped`, `TestFactsTakeTheJournalByTag`; the
  installer's `passwall-upgrade`: a router taken from PassWall2 is upgraded
  (the same package relabelled 9.9.9-r1) while a watcher looks every 100 ms —
  vctl restarted in place, and PassWall2 never ran. With r19's prerm the same
  scenario fails: the watcher sees PassWall's switch go on and PassWall2 run.

## vectra-reporter 1.0.0-r1 and vctl 0.6.0-r19 — bug reports

### Added
- **vectra-reporter**, a package of its own (ADR-0007): a static binary cron
  runs every minute, and nothing of vctl's — a crashed vctl or a bad vctl
  release does not take it down. It sends Vectra Connect short reports of
  what went other than meant (`POST https://api-app.vectra-pro.net/errors/router`,
  docs/CONNECT-BUG-REPORTS.md): signed with the router's `vr1` token over the
  path and the body's sha256, so no other software can pass for a router or
  replay one; over the control-marked socket, so a report gets out while
  xray is down. What it sees itself: vctl's crash output (a file per vctl,
  `debug.SetCrashOutput`), crash loops, OOM kills and kernel faults in dmesg,
  vctl's data plane loaded with no xray, a boot after no clean shutdown (and
  pstore). What vctl and its shell tell it (the inbox,
  /var/run/vectra-reporter/inbox): `xray -test` refusing a render vctl made,
  xray exiting unasked or in a storm, the rescue going direct, a refused
  subscription or geo update, memory that keeps growing, the dead-man
  starting vctl or handing the router back, a start that had to hand back.
- Reports are small and say nothing of whose they are: links, URLs, UUIDs,
  keys and passwords, the home's addresses, e-mails and visited hosts are
  taken out before anything reaches flash; xray's access log is never read.
  Kept in a capped spool (32 reports, 512 KB; the least severe go first),
  one report a problem per 6 hours with a count, 12 an hour and 100 a day at
  most, kept 7 days; the server's answers decide the rest (the contract's
  table). `vectra-reporter status` shows the spool, `vectra-reporter test`
  sends a test report, `uci set vectra-reporter.main.enabled=0` stops it.
- The contract's test vector for Connect's tests: ui/contract/report-vector.json.

### Tests
- The stand's `reports` mode: a vctl crash (SIGQUIT), the data plane without
  xray and an unexpected reboot reach a stub Connect that verifies every
  report; nothing goes out switched off. The installer's lifecycle installs
  and removes the package, its cron block and its spool.

## 0.6.0-r18 — the geo directory is vctl's; memory under watch

### Fixed
- **xray read PassWall2's geo directory whatever vctl pinned.** PassWall's
  generator writes it into the config — `"env": {"XRAY_LOCATION_ASSET":
  "/usr/share/v2ray/"}` — and a recent xray (26.7.28, the test router's;
  not the stand's 26.3.27) applies a config's env over the one it was
  started with. With `route_source 'native'` and PassWall's
  packages removed, that directory was gone and xray stopped starting (the
  test router, 2026-09-29, 8 minutes without the proxy until the files were
  put back by hand; the rescue kept the direct sites open). The adaptation of
  PassWall-shaped configs now takes XRAY_LOCATION_ASSET out, so the
  supervisor's and the `xray -test` gate's directory — the policy's own —
  is the one xray reads. The stand's xray (26.3.27) ignores a config's env:
  its renders are now checked for naming no geo directory.
- The native policy's scheduled geo update fetched both files whatever the
  policy's `geoip_update`/`geosite_update` said; it now fetches only those
  switched on, as PassWall2's rule_update.lua does (a missing switch is on).
- The panel's `update_xray_assets` job wrote the operator config's files
  into its `geo.assetDir` as named — a directory xray might not read — and in
  native routing over nothing the policy used. It now writes where the
  running xray reads; in native routing it starts the policy's own update
  (its sources, its checks, both files) beside the daemon's loop, which it
  held up to 8 minutes; it refuses PassWall's files — in PassWall mode, and
  where xray reads them for want of vectra-geodata.
- **Every reboot handed the router back on its way down.** rc.common's
  `shutdown` is `stop`, and the hand-back ran: the legacy agent and PassWall
  enabled and started as the router went down, and at the next boot the
  agent (S95vectra-controller) started before vctl took the router again. A
  shutdown is no hand-back now, as a restart is not; vctl is enabled and
  the boot starts it, and the dead-man hands the router back if it does not
  come up.

### Changed
- **vctl's geo data lives in vctl's own directory, and is read there.** xray
  reads `/usr/share/vectra-controller-pro/geo` (vectra-geodata's, a
  dependency of vctl's package) unless another directory is named; the
  fleet's old `/usr/share/v2ray` — PassWall2's packages' directory, the
  panel's operator configs' `geo.assetDir` — reads as vctl's own, since it
  goes with those packages — unless vctl's own directory has no files and
  the old one has: a vctl package built without the vectra-geodata
  dependency (only the pro feed's carries it) on a PassWall router reads
  PassWall's files, as before, rather than failing on files that are not
  there. The supervisor, the `xray -test` gate, `vctl supervise`, `vctl geo
  update`, the inventory, the kernel's direct routes, the xray wrapper, the
  agent config's default and the installer agree on it. The directory xray
  reads moves with a render written under it, never before: a config the
  gate refused leaves the running render its own, across vctl's restarts
  too (state.json keeps the directory the render was checked against; the
  start checks it again under the config's). The native route policy's
  files stay in `/usr/share/vectra-controller-pro/route-geo`.
- vectra-geodata 2026.9.28-r2 no longer links its files into
  `/usr/share/v2ray`. The links earlier versions left there go with vctl's
  install (its uci-defaults; not where PassWall2 is installed), not with the
  data package's. The old version's prerm takes them away on an upgrade, so
  r2's postinst puts them back while the vctl installed is older than
  0.6.0-r18; r2's own prerm takes them away on a removal only.

### Added
- **The memory ledger.** Once a minute vctl notes what the router's memory
  is and what every process holds (anonymous memory, by name), and keeps the
  lowest value of each 10 minutes for a day: load only adds spikes, a leak
  raises that floor. Load moves a floor too — xray's in the evening is the
  evening's connections, against the quiet hour after the 04:30 reboot — so
  each series keeps its load beside it (a process's open descriptors, the
  tracked connections for the router's own figures), and a floor counts as
  risen only at a load no higher than where it started. Once an hour vctl
  says, in the log, what keeps growing (a floor up by 8 MiB and a quarter
  over 6 hours or more; MemAvailable's ceiling down by 8 MiB), and writes
  what holds the memory now and what grows, with the load, to
  `/var/run/vectra-controller-pro/memory.json` (tmpfs, a few KB).
- **OpenWrt's own kernel reserve instead of the previous agent's.** The old
  agent reserves 24 MB for the kernel's free-page pool
  (`99-vectra-lowmem.conf`); OpenWrt's own for a router of 64 MB and up is
  16 MB. Where the old file is, vctl's takeover puts OpenWrt's value back in
  a file read after it (on the test router: MemAvailable 63 → 86 MB, no
  page-allocation failure since), and every hand-back — the package's
  removal too — gives the agent's back. A trial changes only the running
  value; a standby install, nothing.

### Tests
- The stand's `procd` mode held no cron while it checked that a bare `stop`
  leaves vctl down: the package's dead-man starts a switched-on vctl a stop
  left down at the next minute, on purpose (a restart cut off between its
  stop and its start), and the assertions raced that minute.
- The installer's `geodata-links` scenario: an earlier vectra-geodata's links
  go with vctl's install, another package's file stays.

## 0.6.0-r17 — a router without PassWall2

### Added
- **Native routing** (`option route_source 'native'`). The operator's route
  policy — PassWall2's configuration, imported once and as it is into
  vctl's own store, `/etc/config/vectra_route` (0600), with its geo files —
  is made into xray's configuration by vctl itself, and kept current by
  vctl: nothing of PassWall2 is needed on the router, not its generator, its
  Lua, its geo packages or its cron.
  - The generator (`internal/routepolicy`) is a port of PassWall2 26.8.10's
    `gen_config` for the fleet's shape (a shunt global node, vless, xray),
    proven against PassWall's own output: byte-equivalent JSON on the fleet's
    configuration and a variant, for xray 26.3.27 and 26.7.28. What it does
    not port (fragment, pre-proxy, balancers, other cores and protocols) it
    refuses: the router keeps what runs. `vctl passwall-render -compare`
    runs both generators on a router's live policy and names every JSON
    path where they differ (no values).
  - The subscription is asked by vctl on the policy's own schedule, in the
    router's local time (OpenWrt's TZ): PassWall 26.8.10's
    `update_week_mode`/`update_time_mode` — not `auto_update`, which 26.8.10
    does not read — or, where only the older `auto_update`/`week_update`/
    `time_update` are set (which 26.7.16 and later schedule not at all), those.
    A router that takes native routing asks at its next scheduled time, not
    at once; `boot_update` at the daemon's start; the refresh job at once.
    A failed ask is retried after 30 minutes, a refused answer after 2 hours.
    It asks as the router itself: its own signed User-Agent and the device
    headers PassWall sends. The share links become nodes as
    PassWall's `subscribe.lua` makes them (proven against it on the same
    links), and every reference follows its node — with three differences:
    the provider's placeholder ("App not supported", "Limit of devices") or
    an empty feed changes nothing (PassWall applied it and every slot lost
    its node); a node is never matched by its bare host, nor by host:port
    when the names say another country (PassWall put the Netherlands slot on
    Germany when Germany took the Netherlands' old bridge host); and a slot
    whose node is gone from the feed — its host switched off on the panel —
    moves to the same country by the same path, then by another, then to the
    main slot's (WorldProxy's) node, then to «Авто Самый стабильный» (the
    owner's choice, 2026-09-29), where PassWall pointed it at a node it had
    just deleted.
  - The geo files are fetched from the policy's own sources on its schedule
    (the fleet: every day at 06:00), beside the daemon's loop, streamed to
    flash, each taken only when it has every category the policy routes by
    and matches its published sha256, both swapped only when both pass.
  - Until the import has put the policy's geo files in place, xray reads
    PassWall's, which the last render was made with. The store and the geo
    files are written with fsync: a power cut leaves the old file or the new.
- **The LAN's DNS queries to public resolvers are answered by the router**
  (`option dns_hijack '0'` turns it off): a TV or a phone with 8.8.8.8 set
  by hand got the ISP's forged NXDOMAIN for Instagram and YouTube — its
  query was ordinary traffic, and the routing sent it out directly. While the
  router's resolver asks through the tunnel and something serves port 53 on
  the router's LAN side, such queries are redirected to it (queries to the
  router and to private addresses are left alone; a flow older than the
  hijack stays as it was). PassWall2 did the same.

### Fixed
- **Port forwards work under vctl.** A forwarded server's answers — the
  reply direction of a connection the WAN opened — were captured by TPROXY:
  its SYN-ACK reached xray's listener, which reset it, and a UDP answer left
  from xray's own address. Every port forward (and inbound IPv6) was dead;
  the kill switch, armed, dropped them too. Replies now never enter xray or
  the kill switch (measured on the stand: TCP and UDP forwards, xray up and
  dead, armed and not; without the fix both break).

## 0.6.0-r16 — r15, reviewed; PassWall's leftovers go

### Fixed
- **A reprogram no longer reads the direct set back.** nft reads every
  interval set it is not told is being emptied from the kernel before it
  applies a table: with the direct set loaded, every reprogram (a DNS-steer
  change, an apply, a restart) was a 24 MB nft — at the controller's -800.
  The script flushes the direct sets before it replaces the table: 3.5 MB
  (measured, same set).
- **A new direct list that cannot be loaded no longer leaves the old one in
  the kernel.** Out of date is not "straight out as before": the sets are
  emptied, and xray carries those addresses until the new list is in.
- Memory is checked before the list is read (a few MB of vctl's heap); a
  30-minute upgrade of a partial load happens only when more fits; the
  fast path follows every geo file the list came from (ext: files too) and
  the FakeDNS pools taken out of it; a geoip category listed twice is read
  once, as xray reads it.
- **The memory watchdog stops chasing what xray does not hold.** A restart
  that did not give the memory back (a minute later the router is still
  short) doubles the wait before the next one, up to 4 hours; one that did
  resets it.
- The panel's terminal commands and a controller update's opkg drop the
  controller's OOM shield themselves, before they run anything.
- A PassWall sync refused for want of memory waits 5 minutes instead of
  running PassWall's generator again every loop.

### Changed
- **Taking a router from PassWall2 removes what its stop leaves in the
  kernel:** its nft table. Its base chains stayed hooked on every packet — on
  the test router one rule in them, left once per PassWall start (three
  there, from three upgrades' hand-backs), marked every packet of xray's
  connections — and its sets, its own copy of geoip:DIRECT among them, held
  kernel memory (2 MB there) that every nft set command read back. PassWall's
  start makes the table anew.

### Notes
- With domainStrategy IPOnDemand (the fleet's), xray matches IP rules against
  its own resolution of the sniffed name. A client with its own DoH/DoT that
  resolves a listed service to an address inside the direct list is sent
  straight out by the kernel — as PassWall2 sent it. Clients that ask the
  router get FakeDNS addresses for listed services and are unaffected.

## 0.6.0-r15 — living within a 234 MB router

Measured on the data-plane stand, arm64, the fleet's PassWall-shaped config:
every connection xray carries costs it ~38 KB (1000 held open: its heap 8 →
47 MB; 2000: 82 MB, past its 80 MiB GOMEMLIMIT, and 55 MB still held after
they closed); `xray run -test` peaks at ~40 MB; nft holding a 15k-range set
peaks at ~24 MB to load it — and as much again just to list it. And the
router's own `oom_score_adj` picture put xray (and vctl) BELOW hostapd,
dnsmasq, netifd and rpcd: out of memory, the kernel would have taken the
Wi-Fi, DNS and the LAN one after another while xray, the one process that
had grown, stayed. That is "it works for five minutes and hangs".

### Added
- **What the routing sends straight out by address is routed by the kernel,
  not xray** (`option direct_bypass '0'` turns it off). The leading direct
  rules of the running render — on the fleet, the panel's geoip:DIRECT,
  35,572 prefixes merged into 14,807 ranges — are loaded into the data
  plane's direct sets, and a LAN connection to them never enters xray.
  PassWall2 did the same with the same list; vctl had been carrying all of it
  through xray. Russian sites also keep working through an xray restart.
  - Decided per connection, on its first packet, and followed by a
    conntrack bit after that (`Spec.DirectCtMark`): reloading or emptying
    the sets moves no live connection between the kernel and xray.
  - Loaded only as far as the memory free right now allows (~1.5 KB per
    range for nft at its peak), the largest ranges first — the top 4000
    cover 92% of DIRECT's addresses; what is not loaded goes through xray,
    straight out, as before. One nft transaction; the nft process is the
    one to lose if it tips the router over. Reloaded when the list or its
    geo file changes, and after the table is reprogrammed.
  - Only rules before the first one that could send a connection elsewhere
    count, and only through a plain freedom outbound (no fragment,
    redirect, dialer proxy or interface); FakeDNS pools never.
- **A memory watchdog.** When MemAvailable stays below 5% of RAM (12 MiB) for
  15 s and xray holds 9% (21 MiB) or more, vctl restarts xray — every byte
  it held given back in a few seconds, Wi-Fi, DNS and the direct routes
  untouched — before the kernel starts killing; at most once per 10 minutes.
  The kernel's own OOM kills are logged.

### Changed
- **The kernel's OOM pick.** vctl -800 (it brings xray back and holds the
  safety nets), xray 0 (it is what grows with traffic, and is back in
  seconds), `xray -test` and PassWall's generator +500 (next to the running
  xray, they are the ones to lose), the panel's terminal commands 0 (they
  inherited vctl's protection).
- **xray's GOMEMLIMIT follows the router's RAM**: 22% of it, 24..80 MiB —
  51 MiB on the fleet's 234 MB (was 80 MiB everywhere, where memory ran out
  before Go began to collect harder). GOGC 30 as before.
- A new configuration is not checked (`xray -test`, ~15 MB of its own) while
  MemAvailable is under 24 MiB: nothing is installed, it is tried again.
- Every periodic check of the nft table is terse (`nft -t`) — the daemon's
  every loop, the cron dead-man's every minute, `vectra status`, the
  teardown: listed whole, the direct set costs nft ~23 MB each time.

### Correction to r12's notes
- Avito's 429, Wildberries' 498 and Sberbank not opening were not the
  provider's relay: the same requests from curl answered the same way
  straight out (anti-bot answers to curl, Sberbank's Russian CA). What
  PassWall-compatible routing changed that matters is Instagram and the
  other listed services.

### Tests
- passwall-real's routing step: a direct shunt rule's address is carried by
  the kernel (the endpoint sees the client's own address, not the router's),
  the rest still by xray, and live connections keep their carrier through a
  flushed set and through their address entering it.
- Unit: the geoip.dat reader, range merging and budgeting, which rules count
  as direct, the direct-set loader (memory, retries, reloads), the
  watchdog, the child's Go tuning, the config check's memory floor; the
  armed ruleset's golden file regenerated for the direct rules.

## 0.6.0-r14 — a superseded commit-confirm deadman stands down

### Fixed
- **Two firewall applies within 90 s no longer revert the second.** Each
  apply's deadman removed the shared confirm sentinel when it woke; an older
  one, woken after a newer apply had been confirmed, removed that
  confirmation, and the newer deadman then reverted a ruleset the panel had
  confirmed. Measured on the test router: vctl restarted 18 s after an
  upgrade's start, the data plane gone at 90 s and back 9 s later — with
  FakeDNS the LAN's listed sites dead meanwhile. Each apply now writes its
  own token (`fw-confirm.armed`), and a deadman whose token has been
  replaced does nothing.

## 0.6.0-r13 — PassWall routing keeps its own document

### Fixed
- PassWall-compatible routing kept its adapted document where the provider's
  is (`provider-config.json`): turning `route_source` back found PassWall's
  document there and ran PassWall's routing under the provider's name. It is
  `passwall-config.json` now, beside it; the provider's stays as it was.

### Tests
- passwall-real's PassWall-routing step runs with vctl carrying — seeded, and
  route_source set before `vectra on` — and checks PassWall's own stack is
  down throughout: before, it ran after step 12 had left vctl off, and what
  it measured was PassWall. `STAND_PW_ONLY=routing` runs the setup and this
  step alone.

## 0.6.0-r12 — routing like PassWall: the operator's route policy, PassWall's own code

### Added
- **PassWall-compatible routing** (`option route_source 'passwall'`). The
  router routes by PassWall2's own configuration — the operator's route
  policy the panel keeps on every fleet router — instead of the provider's
  xray-JSON. PassWall2's own generator (`util_xray.lua gen_config`, with the
  arguments its `run_global` passes, read from the same UCI) makes the xray
  config; vctl adapts it (its inbounds replaced by vctl's tproxy and DNS
  inbounds, its socket marks by vctl's, its system-resolver DNS by direct
  servers for the nodes' names) and runs it with everything vctl adds: the
  data plane, DNS through its inbound, the commit-confirm, the rescue, the
  dead-man, the API. Measured on the test router, the provider's scheme was
  not PassWall's: every connection through its balancers, Russian sites
  through its shared Russian relay (Avito answered 429, Wildberries 498,
  Sberbank did not open), Meta's QUIC dropped; PassWall's sends only its
  lists (WorldProxy, YouTube, Special, TikTok...) through the node the
  operator chose for each, with FakeDNS, and everything else straight out.
  - FakeDNS addresses are carried into xray (the FakeDNS pool leaves the
    bypass set), and PassWall's inbound sniffing (fakedns, routeOnly) is
    the tproxy inbound's.
  - PassWall's geo files (`v2ray_location_asset`, /usr/share/v2ray/) are
    xray's.
  - A change to PassWall's UCI (the panel, the nightly node update) or to
    the WAN's resolver re-renders; new geo files restart xray. The provider
    subscription refresh does not run in this mode, and the router UI does
    not switch locations (the owner's sites still apply on top).
  - A generation or `xray -test` failure installs nothing: what runs keeps
    running, the last render's documents at a restart.
- `vctl passwall-render`: that render and `xray -test`, installing nothing —
  the check before `route_source 'passwall'` goes on a live router.

### Tests
- passwall-real: a shunt like the fleet's (a list with FakeDNS through
  PassWall's node, everything else direct) — the listed domain resolves to a
  FakeDNS address and the LAN client's request goes through PassWall's node
  by its domain; an unlisted destination goes straight out; back to the
  provider's routing.

## 0.6.0-r11 — a loop that stays quick with the internet down

### Fixed
- **The rescue's probes no longer hold the daemon's loop.** The health URLs
  are asked at once — the first answer ends the others — instead of in turn:
  with the internet down, two dead URLs at 8 s each, plus the probe around
  the tunnel at the control plane's 10 s per URL, held every loop for about
  half a minute, and with it the data plane's and the DNS redirect's own
  checks (the data-plane stand saw a flushed table come back later than its
  17 s window). The probe around the tunnel has 6 s in all, and does not run
  at all under the kill switch, whose answer it cannot change.

### Tests
- passwall-real: the router's own DNS counts as carried by vctl when its
  redirect counter moves during the probe — since r8 it goes into xray's DNS
  inbound, which the nodes' log does not show as the client's own query. The
  stand provider's documents carry a `dns` of their own, as the real ones do.

## 0.6.0-r10 — the review of r9

### Fixed
- **A start in direct mode leaves the data plane down.** The mode survives a
  restart in state.json; the start loaded the table regardless, and nothing
  watches it in direct mode, so after the 04:30 reboot a router the rescue had
  taken direct ran its LAN into the dead tunnel while saying "direct".
- **The rescue never goes direct under the kill switch.** Now that it does go
  direct by itself, a router whose operator armed the kill switch (fail
  closed) would have let its LAN out unproxied.
- The tunnel probe's timeout is 8 s (was 4): its name is resolved through the
  tunnel now, and a slow but working tunnel must not look dead three times
  in a row.
- The control plane's own dialing gives each resolved address its share of
  the dial timeout, so one dead address cannot use up the whole request.

## 0.6.0-r9 — what the review of r8 found, and the rescue that never went direct

### Fixed
- **A reprogrammed firewall replaces vctl's table instead of adding to it.**
  The ruleset is a `table inet vctl { … }` block, and nft, given a block for
  a table that exists, adds: every chain got its rules again and nothing was
  removed. A reprogram therefore left the old rules in force behind the new
  ones — a DNS redirect a new render no longer served among them — and the
  reapply jobs (reconnect, a rescue's recovery) doubled every rule. The
  script now declares, deletes and declares the table again: one nft
  transaction, never both tables or neither.
- **The DNS redirect exists only while xray answers on its DNS inbound.**
  r8 set it whenever the render had the inbound: with xray crash-looping, all
  of the router's lookups went to a closed port — the LAN's, and vctl's own
  for its panel — where, with the kill switch off, a dead xray used to leave
  the LAN online. Programming the firewall now waits (10 s) for the inbound
  to answer a DNS query (a TXT query, answered by xray itself, without the
  tunnel) before redirecting; the loop asks again every time and takes the
  redirect out after two misses in a row, puts it back at the first answer.
- **The rescue goes direct when the tunnel is dead and the line is not.** Its
  "can we go direct" was the tunnel's own probe result again — false exactly
  when the tunnel was dead — so the router never left proxy mode by itself.
  It is now asked around the tunnel, on the control plane's marked path.
- **The panel's names no longer depend on dnsmasq.** The control plane
  resolves them itself, over TCP to 8.8.8.8 and 77.88.8.8 on its marked
  path, and asks the system's resolver only when both fail: dnsmasq's
  upstream goes through the tunnel, which is what may be down when the panel
  is needed most.
- **dnsmasq's IPv6 upstream is refused only when it has an IPv4 one** to ask
  instead: on a WAN with IPv6 resolvers only, r8 left the router no DNS.
- **More names are resolved directly, not through the tunnel:** the provider's
  DNS servers named by host (a DoH server would otherwise be resolved through
  itself), NTP's pool, and — last among the direct resolvers — the WAN's own
  IPv4 resolvers, for networks that keep port 53 to the outside closed.
- The per-loop check no longer logs a warning every loop on a router whose
  dnsmasq runs as root.

### Tests
- `dns-tunnel.sh` adds: the DNS inbound unreachable takes the redirect out
  within two loops, the data plane staying and the LAN resolving; back when it
  answers; each rule once after the reprograms; the tunnel dead with the line
  up takes the router direct by itself; two firewall applies in a row leave
  the tproxy rule there once.

## 0.6.0-r8 — the LAN resolves through the tunnel

### Fixed
- **Instagram and YouTube resolve on a router Vectra carries.** The test
  router's ISP forges NXDOMAIN for them, and not only from its own resolver:
  a query to 8.8.8.8 or 1.1.1.1 over the open path gets a forged answer too,
  injected in flight and racing the real one (measured 2026-09-29, busybox
  nslookup to each). r7's public resolvers therefore fixed nothing, and made
  YouTube worse (the ISP's own resolver answered it). Now dnsmasq's upstream
  queries go through xray:
  - every render gets a loopback DNS inbound (`vctl-dns-in`,
    127.0.0.1:10053), a rule sending it to a `dns` outbound, and so to xray's
    built-in DNS — the provider's own `dns` section, whose servers are reached
    through the provider's routing, i.e. the tunnel. It is how the provider's
    phone apps resolve;
  - the vctl nft table redirects dnsmasq's own upstream queries (by its uid,
    to public addresses, port 53) to that inbound, and refuses them over IPv6
    so dnsmasq asks over IPv4. dnsmasq still answers the LAN, keeps its local
    names (vectra.lan) and its cache; nothing in UCI changes;
  - the nodes' own host names and the panel's are answered first, by
    `tcp+local` servers (8.8.8.8, 77.88.8.8) that xray dials itself around
    its routing: resolved through the tunnel, a node would need itself to
    come up, and vctl must reach its panel with every node dead;
  - the redirect lives in the data plane's table, so everything that takes
    the data plane down — the commit-confirm deadman, the rescue's direct
    mode, a stop, the cron dead-man — gives dnsmasq its own servers back at
    the same moment. It is set only while the running render has the inbound
    and dnsmasq runs as its own user (never root: xray's and vctl's own
    lookups are root's), and the loop reprograms the table when that changes;
  - a provider whose `dns` could not answer the LAN — `fakedns`, or a
    `localhost` xray would fall back to (the system resolver: itself) — is
    installed as it was, without the inbound, and says why in the job result;
  - `option dns_tunnel '0'` in UCI turns it off.
- r7's public resolvers are undone on the next takeover wherever its note is
  found (and at boot by its trial snippet, as before).

### Tests
- `test/dataplane/stand/dns-tunnel.sh`: the real subscription (the test
  account, fetched with the router's own signed agent), real xray 26.3.27,
  real dnsmasq as uid 453 and real nftables, against a resolver on 5.5.5.5
  that forges NXDOMAIN for www.instagram.com and www.youtube.com. Without
  vctl they do not resolve; with it they resolve to real addresses, not one
  query for them reaches the "ISP", Instagram answers HTTP 200 by its name,
  vectra.lan still resolves locally, AAAA is empty (the provider's
  UseIPv4), a TXT query is answered at once, no resolver loop; after a stop
  the forgery is back.

## 0.6.0-r2 … r7 — what the live trials on the test router found

- **r2** — the router's own DNS and NTP go out directly (never through the
  balancer, whose first minute timed the check-in out and let the
  commit-confirm deadman take the data plane down), and the loop loads the
  data plane again when a revert or a flush took it.
- **r3** — a hand-back no longer waits on vctl's own deadman sleeper, and no
  longer leaves PassWall holding procd's lock.
- **r4** — a connection the router opens goes into xray whole (ct mark), not
  only its SYN: the rest used to leave by the WAN and be reset.
- **r5** — the hand-back closes procd's lock descriptor (fd 1000), which
  PassWall's daemons inherited, hanging `status`, `off` and opkg; an xray a
  killed vctl left behind is ended.
- **r6** — the terminal job answers in the panel's schema (command,
  timeoutSeconds, startedAt/completedAt, capped stdout/stderr), which the
  panel used to drop; a changed inbound is rendered again (the render key
  carries the tproxy inbound).
- **r7** — pointed dnsmasq at public resolvers; superseded by r8 (above).

## Unreleased — a vctl that dies no longer takes the LAN with it

### Added
- **A dead-man of Vectra's own, in shell**
  (`/usr/libexec/vectra-controller-pro/deadman.sh`; cron runs it every minute
  from a managed block in root's crontab, which the uci-defaults script
  writes on every install and upgrade and the postrm takes away on a
  removal). It needs nothing of vctl's. When vctl should run — Vectra on, its
  UCI switch and its boot links, or a trial — and procd does not run it, or
  its process is gone:
  - its data plane goes at once: a dead vctl with its tproxy rules loaded
    black-holes the LAN, and the internet straight out is the safe state;
  - vctl is started again, with a backoff: at once, then after 1, 2, 5, 10
    and 15 minutes, then every 15, for as long as it takes (procd gives up
    after `respawn 15 5 20`; this does not);
  - only where PassWall2 or the old agent is installed and owed back, and
    vctl has stayed down for 10 minutes, is the router handed back — `vectra
    off` in shell. Vectra's own routers, with neither, are never given up on.
  A minute that finds vctl running starts the count again; nothing is done
  while a power change or the init script's stop is in flight, while procd
  does not answer, or while Vectra is off. vctl running, configured, but
  without its data plane for 5 minutes is logged, and left to the daemon's
  own rescue. Everything it does is in `logread -e
  vectra-controller-pro-deadman`.
- **The hold on the old agent's cron watchdog lapses by itself.** It is the
  watchdog's own throttle stamp 10 minutes ahead, set again every minute by
  the dead-man while vctl runs, and at once on an NTP clock step
  (`/etc/hotplug.d/ntp/60-vectra-controller-pro`). It was set to the year
  2100: a vctl that died left PassWall disabled with its switch off, the
  agent disabled and its watchdog held off for good.

### Fixed
- **The hand-back no longer runs both stacks at once.** It moved from
  stop_service to service_stopped, which rc.common runs after procd_kill, and
  waits there — up to 15 s — for vctl and its xray to be gone. PassWall and
  the agent came up while vctl still ran: two proxy stacks on a router with
  ~45 MB free, the agent checking in next to a live vctl. A restart or a
  reload still hands nothing back; the prerm (`stop`) still does.
- **An unwritable agent breadcrumb ended the whole init script** under
  busybox ash (`: >`): it is checked now, and declines the takeover before
  anything is taken.
- **PassWall's own switch is set and committed on every hand-back while its
  breadcrumb is there**, never judged by `uci get`, which answers with a
  change only staged: a commit that failed once lost the breadcrumb on the
  retry, the switch still off on flash.
- **A PassWall restart already in flight** (subscribe.lua's at midnight, the
  ifup hotplug, one the agent spawned) **no longer comes back after the
  takeover.** The takeover waits, up to 30 s, for PassWall's own init lock
  (looking, not holding it: PassWall's stop takes it) before stopping it,
  and wants the stack down three looks in a row.
- **`vectra on` and `vectra keep` that do not get there hand the router
  back** (`vectra off`) and say what happened: the takeover declined, vctl
  did not stay up (three looks at procd in a row, or its socket answering —
  one look can catch a crash-looping vctl between two crashes), or it runs,
  configured, without its data plane. A keep that fails after the trial's
  file is gone — its deadman disarmed — hands back too. A trial that carries
  nothing ends at once.
- **`status.power.holder` is `vectra` only with the data plane loaded**;
  vctl running without it is `direct` (with `running: true`), and `vectra
  status` says it runs but carries no traffic yet.
- `vectra off` during a trial ends it even when the UCI switch cannot be
  written: its deadman's off must still give the router back.
- PassWall2 is said to carry the traffic only when its stack runs from
  /tmp/etc/passwall*/bin — not for subscribe.lua, a `tail -f` of its log or
  lease2hosts.sh.
- The agent's breadcrumb is kept until the agent is enabled again, as
  PassWall's is.
- `set_power {"on": false}` answers `power_off` only with nothing owed back;
  with a hand-back still owed it runs the off change again.
- `vectra status` and the router UI's status no longer take the power lock
  to see whether a change is in flight (`/proc/locks`): a change that came at
  that moment was refused as busy.

### Tests
- The packaging tests run under the router's own shell: cross-compiled, in
  an OpenWrt 24.10.8 rootfs, whose /bin/sh is busybox ash 1.36.1 with
  OpenWrt's applets (docs/CANARY.md, "The packaging tests on busybox"). Go
  stand-ins (uci, flock, jsonfilter) replace the python ones.

## Unreleased — the router's own subscription User-Agent

### Added
- **A User-Agent only this router can make.** A subscription that names no
  `userAgent` (and no User-Agent header) is fetched with
  `VectraRouter/<version> vr1.<token>` (`internal/uatoken`): new for every
  request, signed with the device key over the time, the HWID and the request
  target, sealed to Vectra's key — the one the panel names in check-in
  (`claimKey`), else the built-in key 1, whose private half only Vectra
  Connect's backend holds. At most 255 bytes; the contract and a test vector
  are `ui/contract/ua-vector.json`, `uatoken.Open` is the backend's reference.
  No device key or no clock: nothing is sent at all.
- Remnawave answers xray-JSON at `<subscription URL>/json` to any agent; a
  link list answered to the router's own agent is refused with that advice.
- `vctl subscribe fetch -signed [-state …]` sends the same agent by hand.

### Changed
- **A written-down copy of the router's agent is refused**
  (`uaguard.CheckConfigured`): in a config's `userAgent` or headers, at
  adoption and on the wire — only the agent signed just now may start with
  `VectraRouter/`. The panel's partner claim may now leave
  `subscription.userAgent` out (the router's own) and refuses a
  `VectraRouter/…` literal.

## Unreleased — Vectra on and off, a trial takeover, vectra.lan

### Added
- **Vectra's own switch**, for the owner at the console and for the customer in
  the UI. `vectra on` takes the traffic for good (the init script's takeover);
  `vectra off` gives the router back as it was — PassWall2 and the previous
  Vectra agent if Vectra took them, else the internet without a VPN; `vectra
  status [--json]` says what is on and who carries the traffic.
  `/usr/sbin/vectra` is `vctl power "$@"`.
  - Each change runs in a session of its own (stdin on /dev/null, output to
    /tmp/vectra-power.log): the rpcd call, or the legacy agent's job shell that
    the takeover itself stops, can die without taking the change along. One
    change at a time — the change holds a power lock for as long as it runs,
    so one that dies lets go of it; another is refused as busy.
  - `off` sets the UCI switch first, so no start (a reboot's, a restart's)
    takes the router again; then the init script's stop, then the boot links —
    kept while a hand-back is still owed, so every boot tries it again. Both
    are idempotent: `on` twice starts nothing twice, `off` twice gives nothing
    back twice.
  - **A trial**: `vectra on --trial [--minutes N]` (10) takes the traffic but
    stops PassWall2 and the agent without disabling them, gives vctl no boot
    links and notes what it owes on tmpfs (TRIAL_FILE, TRIAL_MARKER_DIR in the
    init script): a power cycle brings back exactly what ran before, with
    nothing on /etc saying otherwise. A detached deadman runs `vectra off` when
    the minutes are up, unless `vectra keep` made it Vectra on for good (the
    takeover of today: disabled, breadcrumbs on /etc). An upgrade during a
    trial does not enable vctl either.
  - rpcd: `status.power` — `enabled`, `running`, `holder` (`vectra`,
    `passwall2`, `agent`, `direct`), `handBack` — answered with vctl stopped
    too; `set_power {"on": bool}` (write ACL, served under the operator's
    lock) answers `pending`, and the UI watches `status.power`; `power_on` /
    `power_off` when it was so already; `busy` while a change is in flight.
  - UI: switched off, the main card says so and where the internet goes, with
    one button, "Turn on Vectra", behind a dialog that says what happens; the
    server, My sites and the restart are not shown. Switched on, a quiet "Turn
    off Vectra" in the footer (and in Pro's verdict), its dialog saying
    whether the internet goes back to PassWall2 or out without a VPN. Pro:
    "Off" in the header, the facts, the checks paused.
- **vectra.lan**: the router answers `vectra.lan` on the LAN with its LAN
  address, Vectra on or off — dnsmasq's `interface_name` on the LAN's `config
  dhcp` section, added by the uci-defaults script, taken away when the package
  is removed (not on an upgrade: opkg runs the old postrm with `upgrade`).
  The main screen's footer and the wizard's last screen say so.

### Fixed
- **The takeover never took a router with luci-app-passwall2.** Its init
  script is not a procd one, and rc.common answers `running` for it with its
  usage and exit status 0: the verify loop saw a stack that never stopped and
  handed every such router back after 10 seconds (the stand's PassWall is a
  procd stand-in, so it never showed there). PassWall is now judged the way
  its own app.sh judges itself — something runs from /tmp/etc/<name>/bin; a
  procd build is still asked `running`; a router without pgrep counts as
  running, and is not taken.
- **The hand-back no longer starts a PassWall that runs already.** What
  luci-app-passwall2's `start` does to a running stack depends on its build:
  its app.sh start runs its stop first, which ends in `exit 0` — older builds
  run it bare, so the start only stopped the stack; 26.8.10 runs it in a
  subshell, so the start restarted all of it. A stack that runs is left
  running — unless it came up while its own switch was off (below): turned
  back on, it is restarted, `restart` being a stop and a start in every
  build.
- **The takeover turns PassWall2's own switch off too**
  (`passwall2.@global[0].enabled`, config named like its init script), and
  the hand-back turns it on again. PassWall's own restarts obey that switch,
  not the rc.d link: subscribe.lua ends every run but a manual one — the
  nightly cron's included — with `/etc/init.d/passwall2 restart &`, and a
  LuCI save restarts it too. So a PassWall the takeover had stopped and
  disabled came back every midnight, its whole stack next to vctl's (seen on
  the test router). Noted by `.passwall-switch-off-by-vctl`, checked before
  the switch goes off; a switch an operator turned off is never touched; it
  is taken whether or not PassWall's rc.d link was on, and every hand-back —
  stop, prerm, a start with vctl switched off, a start that cannot start —
  turns it on before PassWall is enabled and started. A trial notes it on
  tmpfs and writes `/etc/uci-defaults/99-vectra-trial-passwall-switch`, which
  the next boot runs before PassWall starts (a reboot during a trial gives
  the switch back as well); no snippet, no trial. `vectra keep` moves the
  breadcrumb to /etc and deletes the snippet, and proves both. A switch still
  owed after `vectra off` keeps vctl's boot links, like any hand-back owed.
- **An unwritable PassWall breadcrumb ended the whole init script** instead of
  refusing the takeover: the checked write was `: >`, and a failed
  redirection on the special builtin `:` exits busybox ash (and dash) on the
  spot. It is `true >` now, and the takeover declines and hands back as
  intended. The init script's tests run under dash where there is one.
- **The legacy agent's cron watchdog no longer undoes the takeover.** Its
  package runs `vectra-controller-watchdog` every 5 minutes, which enables and
  starts the agent whenever no agent process runs — and the agent brings
  PassWall back: two controllers with one identity, two proxy stacks. The
  takeover now holds it off by the watchdog's own last-restart stamp on tmpfs,
  set far ahead (re-armed by every boot's takeover); the hand-back takes it
  away. Pinned against the agent package's real watchdog script — this
  tree's, and main's as the test router runs it (`openwrt/testdata`, md5
  pinned).

## Unreleased — the wizard tunes both bands, picks a server and shows around

### Added
- **The wizard's Wi-Fi step, on screen**: one card per band (its network,
  channel, "power goes to maximum"); one button, after a confirmation that
  says the router will listen to the air and restart the Wi-Fi, tunes every
  band — the router listens only then, never when the step opens. The result
  waits for the router: "checking the Wi-Fi" until it has seen the radios come
  back, then per band the channel it took, what it heard (how many networks
  share each channel, the chosen one marked) and the power; or, said plainly,
  a band that did not come up, the old settings put back, or a rollback that
  could not happen. Renaming is optional ("one network for 2.4 and 5 GHz" or a
  name per band; an existing password stays when left empty; a renamed
  network is shown with its new name) and on a tuned Wi-Fi only renames —
  except for an open network, which gets a name and a generated password
  first. A router with a 6 GHz radio only names its networks. Skipping stays
  until the Wi-Fi is tuned.
- **A server step**: "Auto" first and recommended (it goes through bridges in
  Russia to the freest exit abroad), then the countries of the subscription,
  then the rare variants (a protocol, "direct", whitelists) folded away. The
  main screen's list is grouped the same way.
- **A short tour of the main screen** after the wizard: four cards lit up in
  turn (is the VPN working, the server, My sites, what to do if something does
  not open), once per browser, again from "How it works" in the footer; Esc or
  "Skip" gives the page back.

- **Pro in plain words**: routes named by what they carry ("Main traffic",
  "Russian sites and YouTube"), each with its chain as a sentence; nodes
  grouped into bridges, Hysteria2 and whitelists, named "Bridge → Poland"
  beside their tags; servers as "Auto" and a card per country with its
  variants; an overview of four facts with the technical details folded.

### Changed
- **A design pass over every screen**, desktop and phone, both themes:
  - the simple view uses a monitor's width (the verdict across the top; the
    server and My sites beside what to do if something does not open);
  - no system monospace anywhere (it read "squashed" next to Montserrat) —
    tags, addresses and the journal use the app's face with equal-width
    digits; the network name is a labelled row;
  - the element resets no longer override components (notes had lost their
    padding and looked like input fields, buttons their colours);
  - the wizard's Back and Skip live in each step's foot with its buttons,
    in reach at the bottom of the screen on any width;
  - a note carries its one action ("Restore auto choice"); a help step has one
    button, with "Message Vectra" as a link in its sentence;
  - "Auto" shows as a split-route sign with "most stable" under it instead of
    the provider's Russian and EU flags;
  - linking on a phone puts "Open in Telegram" first and the QR, labelled for
    another device, last;
  - flags, text buttons and numbered steps sit on their text's line; one
    radius scale (8/16); the main button's white text passes AA;
  - Pro → Nodes on a phone: filters in a grid, the dead-node mark on the
    card's edge;
  - "Wi-Fi", "QR-код", "что-то" never break at their hyphen (an invisible
    word joiner holds a one- or two-letter half to the other; only the
    table's own words — a network's name stays as typed);
  - a wizard screen with its own "Next" (a result, "cannot check the
    internet", "Vectra unavailable") no longer offers "Skip this step" beside
    it — both led to the same place; on a wide screen the main button sits at
    the far right, as in the dialogs;
  - the tour keeps the lit card clear of its bubble: on a phone the card is
    centred above the docked bubble (the page gets the room to scroll the
    last card up), and a card too tall for the bubble below or above it gets
    the bubble beside it;
  - English: "picked channel 11, the least busy" (it said "channel 11 taken",
    which reads as "occupied").
- The wizard says what it checked, in words: the internet "working", the Wi-Fi
  "at full power", Vectra "subscribed", the server "chosen".
- The simple view and its toasts say "server" where they said "location".
- The package depends on `rpcd-mod-iwinfo` (the scan goes through it; LuCI
  pulled it in, the package did not say so).
- The UI build measures the bundle it has just written (a build with
  `--outDir` used to measure the one in the package, and stamp the view with
  it).
- **The setup wizard's Wi-Fi step** works per radio: `setup.wifi` lists every
  radio (band, channel or auto, htmode and the width it means, country,
  txpower, whether it runs at the driver's full power, whether it carries an
  access point or a mesh, whether netifd has it up) with its network, whether
  the Wi-Fi can be tuned (`tunable`: 2.4 and 5 GHz radios only) and is
  `tuned`, and how the last change's restart went (`apply`). `optimize_wifi`
  sets Panama's rules, the driver's full power (no `txpower`, no
  `vif_txpower`) and a fixed channel on every radio — the one asked for, or
  what its scan recommends: never DFS, never 12 or 13, and only what the
  radio's width can carry (2.4 GHz: the least shared of 1/6/11, 1/6 at
  HT40+, 6/11 at HT40-; 5 GHz: the quieter of 36-48 and 149-161, a radio
  staying in its block unless the other is clearly quieter; at 160 MHz 36-48
  only) — and can rename and key the networks in the same transaction and
  the same single Wi-Fi restart. It scans only the radios that are up, before
  it answers; `wifi_scan` returns that scan and never scans. A mesh radio
  keeps its channel; a router with a 6 GHz radio is refused (`unsupported`).
  It never leaves or makes a network on and open. `set_wifi` renames per
  radio; a secured network keeps its key when none is given, and a new key
  keeps its WPA mode.
- **A Wi-Fi change proves itself**: one at a time (`busy`), staged in a
  private uci save directory and committed with its restart marked
  "applying" before the answer. A detached helper then restarts the Wi-Fi,
  waits up to 45 s for every radio that should come up, and puts the
  previous settings back when a radio that was up before does not
  (`rolled_back`). A restart that cannot start is `apply_failed` with the
  change taken back — never a success with no restart.
- Panama's power limits are above what some countries allow; the owner chose
  them.

### Removed
- The router-password step (`set_admin_password`, `setup.admin`): LuCI's own
  banner asks for a password.

## 0.5.0 — a router a customer sets up and links by themselves

### Added
- **The setup wizard**: what a person sees on a router straight out of the
  box (LuCI now opens on Vectra). The internet is the router's own business —
  the step only checks it (`wan_check`: link, address, DNS, a real HTTP 204,
  the panel), says when the cable is missing, and after a minute without
  internet what to check; it never asks for connection settings. Wi-Fi gets
  a name of its own (`Vectra-XXXX` from the MAC, not OpenWrt's) and a
  generated password, with a QR to join; then the router's password (to
  `passwd`'s stdin only) and the link to Vectra. A step that is fine is
  ticked, every step can be skipped, and the last screen says "almost done"
  when one was. Served under the operator's lock: it is the simple view.
- **Linking to a Vectra account** (ADR-0006): while not linked the router
  shows an 8-character code and a QR only Vectra's backend can open (X25519 +
  HKDF + AES-GCM, signed with the router's ed25519 key), rotating every 10
  minutes, and a Telegram link for a phone. Check-in carries only a hash of
  the code; the panel answers with Vectra's key, the bot and the owner, and
  the wizard goes "waiting" → "account confirmed, getting the settings" →
  "connected". A router that is not linked says so on the main screen and
  opens the wizard at that step. `ui/contract/claim-vector.json` is the test
  vector for the backend's implementation.
- **Unbinding in the Vectra app** (`"released": true` in a register or
  check-in answer): a router that had an owner goes back to how it came out
  of the box — the data plane unloaded and xray stopped (the LAN goes out
  directly), the operator config, subscription document, locations cache,
  render, the choices made on it (location, pins, probe interval, My sites),
  the applied revision and the owner removed, and a new claim code — so it
  can be linked again or passed on. Wi-Fi, the root password, `setup_done`
  and `ui_lock` stay. A router that never had an owner (the fleet) ignores
  the flag, whatever the panel sends; a released one ignores it after the
  first time. Data-plane stand: `MODE=setup` proves it on the packets;
  `MODE=setup-hold` keeps a fresh box up for a browser.
- `support_bot` (UCI, empty as shipped, set when boxes are prepared): the
  support bot a box that has never been online can point to, until the panel
  names its own. The claim's Telegram link still goes only to the panel's.
- Every register carries a proof that the router holds its device key (an
  ed25519 signature over `vectra-register/v1\n<deviceIdentifier>\n<timestamp>`),
  so the panel adopts a record it created in advance — a pre-claimed router —
  only for the router it was made for.
- **My sites** (`rules` / `set_rules`, served under the operator's lock): the
  router's owner names sites that always go WITHOUT the VPN (a bank that
  refuses VPN addresses) or always THROUGH it (blocked, but in none of the
  provider's lists) — up to 100 each. The router normalizes what is pasted
  (`https://www.Sberbank.ru/ru` → `sberbank.ru`, `XN--C1AAPKOSAPC.XN--P1AI` →
  `госуслуги.рф`, `1.2.3.4/24` → `1.2.3.0/24`) and refuses anything that is not
  a domain, an address or a CIDR, naming the entry. Punycode (RFC 3492) is
  implemented in the controller; the standard library has none.
- They are xray's first routing rules, for the LAN's traffic only: direct
  through the provider's own freedom outbound (or `vctl-direct`, added marked
  and last when there is none), proxy to where the provider sends everything
  else (its main balancer). A more specific site in the other list wins. Part
  of every render — panel jobs, nightly refreshes, location switches — and
  kept only once a render with them runs.
- While any site is set the TPROXY inbound sniffs `http`, `tls` and `quic`
  with `routeOnly` (the panel's config already does; a hand-written one may
  not): a site is recognised by its SNI / Host and still dialled at the
  address the client resolved.
- `balancers.defaultOutbound`: xray's default outbound, where traffic no rule
  routes ends up.

## 0.4.0 — two views of the router, in Vectra's own look

### Added
- **The simple view**, now what the router UI opens with: one screen for the
  router's owner — does the VPN work, in words about their internet (not about
  xray); the location, and a list to switch it; three steps for "a site does
  not open" (restart the VPN, try another location, copy a report for
  support). No xray, balancers, nodes or addresses. Only what changes the
  owner's internet moves its verdict: low memory, a slow panel link or one dead
  node the balancer already avoids stay in Pro and in the report.
- **Pro**: the tabs as before, one switch away ("Simple | Pro", remembered).
- **Report for support** (both views): state, location, router, versions,
  panel link, memory and every check that is not ok, with its id — copied, or
  shown to copy by hand where the browser will not. Never credentials: the
  answers it is built from carry none.
- **The operator's lock** (`uci set vectra-controller-pro.main.ui_lock=1`):
  the UI offers only the simple view, and vctl itself refuses the Pro methods
  (`balancers`, `nodes`, `logs`, `pin_balancer`, `set_probe_interval` →
  `locked`) — read at every call, no restart. A product policy for customer
  routers, not a boundary against root.
- **Vectra Connect's design language**: its palette, the official wordmark,
  Montserrat (SIL OFL 1.1, Latin + Cyrillic subset, three weights, ~17 KB
  each), 8 px radii, the panel gradient and the glow-and-grid background.
  Dark by default, like Vectra's apps; light and "system" stay one click away.

### Fixed
- `balancer_fallback` flagged a reserve no traffic reaches (an empty whitelist
  level deep in the chain) as "no working node, traffic is on the fallback".
  It now follows the traffic from the main and every routed balancer down the
  fallbacks, and says whether the main traffic moved (`main`) or some traffic
  has nowhere to go (`blocked`, a failure).
- `no_leak` warned forever after the first ordinary xray restart: with the
  kill switch off, traffic goes direct while xray restarts, by design. The
  counter is now baselined at every xray start; only traffic that bypassed a
  running xray is a leak.
- One memory threshold: diagnostics warned below 80 MiB free while the router
  card judged below 64. Both say 64 now (48 stays the failure).
- Copy proofread in ru, en and zh, including: a "memory 31 of 32 MB" gauge
  that was Go's soft GC target, not a ceiling; a node-table column that is
  always empty with the providers' observatory (now hidden); "the change did
  not apply" said 30 s into a change that could still land; "Kill switch",
  "Overlay" and "/tmp" left untranslated; Russian number agreement ("1 пакет").

## 0.3.0 — the router UI, and balancers that notice a dead node

### Added
- **Router UI** in LuCI (Services → Vectra): overview, balancing, nodes,
  locations, journal; ru / en / zh; dark and light. It talks to one ubus object,
  `vectra` (`vctl rpcd`, rpcd exec plugin + ACL), held to `ui/contract/` by
  tests on both sides.
- **Balancer pins**: pin a balancer to one of its nodes — validated against
  the running config (xray itself accepts any tag), applied live through xray's
  RoutingService, re-applied after every xray start.
- **Locations from the cache**: every fetch keeps the whole subscription array
  (byte-exact, gzip, rewritten only when it changed); switching location is a
  local re-render — the provider is not asked again. The router's choice is
  honoured by remark on every apply, panel jobs and nightly refreshes included.
- **Observatory interval**: the provider's 12 h becomes 10 min on a router
  (or what the UI sets, 1 min – 24 h). xray probed each node once at start and
  then twice a day at random; a node that died after the start stayed "alive"
  to the balancers for hours.
- xray's API and metrics spliced in on loopback (refused anywhere else).
- A native unary-gRPC client for xray (cleartext HTTP/2, stdlib only) instead
  of exec'ing `xray api` (~26 MB peak RSS per call, one balancer per process).
- `vctl api balancers|pin|unpin|metrics`; `vctl rpcd list|call`.
- Data-plane stand `MODE=ui`: the package installed with opkg, rpcd, uhttpd
  and LuCI from their init scripts, three real xray nodes, a subscription
  server — pins, location switches and the probe interval proven on packets.
- `scripts/build-local-ipk.sh` and `docs/CANARY.md`: a hands-on test on one
  router with a way back at every step.

### Fixed
- **The subscription User-Agent that gets a customer's device deleted** is
  refused, at config adoption and before any byte is sent: any `Happ…` agent
  that is not the real client's `Happ/<ver>/<OS>/<build>`. `Happ/1.0` was the
  documented default (panel, goldens, examples, stand).
- xray's access log is off on routers (`log.access: "none"`): the provider
  leaves it unset and xray printed a line per connection into RAM.
- `xray.log` is capped (512 KiB × 2) instead of growing in tmpfs until reboot.
- `xray version` runs once per binary, not on every check-in.
- The daemon runs with GOGC=50 / GOMEMLIMIT=32MiB; xray children never
  inherit them (the wrapper's 80 MiB applies).

### Security (from review)
- The provider fixture carried a real hysteria2 credential; scrubbed, with a
  test. It remains in unpushed history — rewrite before any push.
- A provider document repeating a key in another spelling ("Api" beside
  "api") is refused: xray's case-insensitive decoding would have run the
  provider's 0.0.0.0 API instead of the router's loopback one.
- xray's API and metrics are guarded on loopback (nft `local_guard`): xray's
  own sockets (LAN -> sniffing -> DIRECT -> 127.0.0.1) and non-root processes
  are refused. The API serves only RoutingService and StatsService.
- A change made on the router is persisted only once it runs; "pending" no
  longer leaves an unrun location choice that every later refresh re-applies.
- A stored config with the old Happ/1.0 agent loads again (it had left the
  router without a firewall after a restart); new ones are refused at
  adoption. The Happ shape check is stricter.
- The journal shows only the controller's own syslog tag (no forged lines).

## Unreleased — "consume provider JSON" pivot

**The controller no longer builds proxy configs.** The provider subscription
already returns complete Xray documents; vctl adopts them wholesale and mutates
exactly one thing.

### Changed
- **Inbound splice replaces the config builder.** `xray.SpliceInbounds` re-emits
  every top-level key of the provider document byte-for-byte, in the provider's
  own order, replacing only `inbounds` with the controller's TPROXY inbound.
  Nothing is parsed into a Go struct and re-serialized — a Lua attempt that did
  turned `"tcpSettings":{}` into `"tcpSettings":[]` and Xray refused to start.
  Regression tests: `TestSplicePreservesEveryByteExceptInbounds`,
  `TestEmptyObjectsSurvive`.
- **The provider's socks:10808 / http:10809 inbounds are DROPPED**, not
  rewritten. Neither carries a `listen` key, so Xray binds them to `0.0.0.0` —
  an unauthenticated open proxy on the LAN. Dropping removes the failure mode
  outright and nothing on the router consumes them (the data plane is TPROXY).
  Guarded by `TestNoOpenProxyInbound`.
- **The router fetches the subscription itself.** The provider document never
  travels through the panel's revision pipeline (`jsonb` storage,
  `stableStringify` key sorting, value masking, zod parsing each destroy
  byte-exactness). The panel carries only the operator config.
- **Digest is `sha256(providerRaw)` directly** — no canonicalization, since
  canonicalizing is the corruption. The provider blob is persisted verbatim to
  its own file (`providerConfigPath`), never through `config.Read`.
- `internal/config` shrunk to four load-bearing blocks: `Process`+`Backoff`,
  `Inbounds.Tproxy` (incl. `KillSwitch`), `Geo`+`GeoFile`, and the
  subscription's URL/UA/headers.
- `geo.assetDir` default corrected `/usr/share/xray` → `/usr/share/v2ray`.

### Fixed
- **Fleet-wide subscription 403.** The daemon called `subscription.Fetch` with
  only URL/UserAgent/ExtraHeaders, so `x-hwid`, `x-ver-os` and `x-device-model`
  were never sent and the provider refused the device. Device facts are now read
  from `/sys/class/net/eth0/address` and `/tmp/sysinfo/model` (**trimmed** — the
  model file has a trailing newline and only the trimmed form reproduces the
  production HWID) and are independent of User-Agent. Regression tests:
  `TestDaemonFetchSendsFullDeviceHeaders`, `TestHWIDMatchesPassWall`,
  `TestHWIDStableAcrossUserAgents`.
- **`XRAY_LOCATION_ASSET` was set nowhere in the tree.** Xray looked for
  `geoip.dat` next to its own binary and failed on any config referencing geo
  data. Now exported both by `vctl-xray-wrapper` and via `cmd.Env` in the
  supervisor, so it holds regardless of how xray is launched.
- **Control-plane self-lockout.** The nft output chain marked the router's own
  egress into TPROXY, so vctl→panel traffic was routed by the provider's rules;
  a dead provider chain cost remote management and the commit-confirm deadman
  flapped every 90s. vctl's sockets now carry `SO_MARK 0x5643` and the output
  chain returns on it as its FIRST rule.

### Added
- **`xray run -test` gates every write.** A document Xray refuses is never
  installed and the previous good config stays live (the write used to happen
  unconditionally after render).
- **`allowInsecure` guard.** The old gate lived in the URI adapter, which the
  JSON path never touches. `xray.ScanAllowInsecure` walks the raw document
  READ-ONLY and refuses `allowInsecure:true` (and the `1` / `"true"` spellings);
  overridable per subscription via `allowInsecureTls`, off by default.
- Configurable subscription read cap (`subscription.maxBytes`, default 4 MiB);
  a response that hits the cap is refused as likely truncated.
- `vctl render -provider`, `vctl supervise -provider`,
  `vctl subscribe parse -entry N` (emits one entry's bytes verbatim), and
  `vctl subscribe hwid` with no flags (reads the device's own identity).

### Removed
- `internal/coreengine/xray` config builder: `render_*.go`, `types.go`,
  `engine.go`, and the parity/golden/integration tests.
- `internal/coreengine/interface.go` — the `Engine` interface had no
  implementation left once `Render` was gone.
- `internal/subscription/adapter.go` (`ToConfigNodes`) — it mapped parsed URIs
  onto `config.Node`, which the config shrink removed.
- Zero-reader config fields: `FetchPolicy` (all of it), `FilterPolicy`,
  `Subscription.UpdateMinutes`, `Subscription.PreservedTags`, plus the node /
  routing / DNS / policy / observatory schema the builder needed.

### Known gaps
- `internal/coreengine/xray/testdata/provider/entry-00.json` is **synthetic**.
  It must be replaced with a real scrubbed capture before the canary — see the
  README in that directory.

## v0.1.0-alpha (initial drop)

**First testable build.** Local dev only — does not deploy to a router yet.

### Subsystems shipped
- Operator config schema (`internal/config`) — full Xray feature surface; strict JSON (`DisallowUnknownFields`); schema-version pinned; defaults audit.
- Subscription engine — V2RayN base64 + URI parsers (vless, vmess, trojan, shadowsocks, hysteria2); PassWall2-impersonation HTTP fetcher (UA + `x-device-*` + `x-hwid`); response-header parsing (`subscription-userinfo`, `profile-title`, `profile-update-interval`); **ParserDefaults audit trail** so every protocol-required default is visible.
- Xray JSON renderer (`internal/coreengine/xray`) — inbounds (tproxy / socks / http / dns / dokodemo / shadowsocks), outbounds (vless / vmess / trojan / shadowsocks / socks / http / hysteria2 / wireguard / freedom / blackhole / dns / loopback), transports (tcp / ws / grpc / kcp / quic / xhttp / httpupgrade / domainsocket), security (TLS / REALITY / none), mux/xudp, sniffing, routing (rules + balancers), DNS (with FakeDNS), policy, stats, API, reverse, observatory.
- Process supervisor — start/stop/reload, exponential backoff with stable-uptime reset, intentional-restart flag (Reload/soft-cap reload not counted as crashes), per-process resource monitor (RSS/FD/CPU from `/proc`), soft memory cap → reload (NOT kill), `oom_score_adj` + nice + rlimit. `Setpgid` + group-kill on grace expiry. Crash-safe state writes (atomic + fsync + dir-fsync).
- Xray gRPC API client (`internal/api`) — `Ping`, `Stats`, `StatQuery`, `SystemStats`, `AddOutbound`, `RemoveOutbound`, `AddInbound`, `RemoveInbound`, `RestartLogger`. Shell-out to `xray api ...` for v0.1; native gRPC in v0.2.
- nftables emitter — block-form (atomic `nft -f -`), dedicated `inet vctl` table, dnsmasq-friendly nftsets, IPv4+IPv6, bypass nets, sniffing-aware. Routing companion commands.
- dnsmasq fragment generator — `server=` upstreams + `nftset=` hooks per domain group.
- Geo updater — `geoip.dat` / `geosite.dat` download, SHA256 verify, atomic swap with fsync, hash-match short-circuit.
- CLI `vctl` — `version | validate | render | subscribe {fetch|parse|hwid} | supervise | firewall {render|apply|revert|routing} | geo {update|verify} | api {ping|stats|statquery|sys|add-outbound|rm-outbound|observatory|logger-restart} | doctor`.

### Design guarantees
- **No silent normalization.** PassWall2's `fp=firefox→chrome` is explicitly forbidden. Every parser-supplied default is recorded in `Node.Origin.ParserDefaults`. Tests assert end-to-end: `parser_test.go::TestParseVLESS_RealityTCPVision`, `engine_test.go::TestRender_RealityFingerprintPreserved`, `audit_test.go::TestNoSilentNormalization_*`.
- **Single binary, no forks** on the hot path. No `lua` interpreter, no shell pipelines on every check-in.
- **Crash-safe writes**: every state file uses atomic `.tmp + fsync + rename + dir-fsync`.
- **No external dependencies** — stdlib only.

### Tests
- 37 unit + integration tests across 8 packages.
- Full `go test -race ./...` passes (~12s).
- `go vet ./...` clean.
- 7200 LOC.

### Known deferrals (v0.2)
- Native gRPC client (Observatory + faster Handler hot-add).
- nftables commit-confirm auto-revert.
- Scheduled subscription / geo refresh.
- OpenWrt `.ipk` packaging + canary deploy.
- HTTP API server for panel integration.
- Broad-parity edge cases (kcp/quic complex headers, balancer/observatory routing strategies, wireguard kernel-mode integration).

### Code-review verdict
Internal code-review pass (`code-reviewer` subagent) found 3 critical + 8 high + 10 medium + 7 low findings. All critical and high findings fixed before this release; medium/low are tracked in the issue list.
