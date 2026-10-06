# Router UI ⇄ vctl contract

The router UI never talks to xray, nftables or the filesystem. It calls ONE ubus
object, `vectra`, through LuCI's authenticated `rpc` layer. `vectra` is an rpcd
exec plugin (`/usr/libexec/rpcd/vectra`) that execs `vctl rpcd`, so every answer
is produced by Go code that is unit-tested against the fixtures in this folder.
The one call beyond it is LuCI's own: the router's password (see Router
password) — vctl never handles a password.

Both sides test against the SAME files:

- Go: `cmd/vctl/rpcd_contract_test.go` decodes each fixture into the response
  struct with unknown fields disallowed and re-encodes it, so a renamed, retyped
  or dropped field fails the Go tests.
- UI: the dev/mock transport serves these files verbatim, so the UI is built and
  reviewed against exactly what the router returns.

Change a shape here first, then both sides.

## Methods

| method | params | answer |
|---|---|---|
| `status` | — | `status.json` |
| `balancers` | — | `balancers.json` |
| `nodes` | — | `nodes.json` |
| `entries` | — | `entries.json` |
| `diagnostics` | — | `diagnostics.json` |
| `logs` | `{"lines": 200}` | `logs.json` |
| `rules` | — | `rules.json` |
| `select_entry` | `{"index": 3}` | `action.json` |
| `reset_entry` | — | `action.json` |
| `pin_balancer` | `{"balancer": "BL-MAIN", "node": "bridge-de5"}` | `action.json` |
| `unpin_balancer` | `{"balancer": "BL-MAIN"}` | `action.json` |
| `set_probe_interval` | `{"seconds": 600}` (`0` = back to the default) | `action.json` |
| `set_rules` | `{"direct": ["sberbank.ru"], "proxy": ["example.org"]}` (both, always — see My sites) | `action.json` |
| `restart_xray` | — | `action.json` |
| `services` | — | `services.json` |
| `set_service` | `{"id": "tiktok", "country": "DE"}` (`""` = the entry's own path) | `action.json` |
| `set_power` | `{"on": true}` (`false` turns Vectra off; `"force": true` also where it would carry nothing — see Power) | `action.json` |
| `set_remote_shell` | `{"on": false}` (`true` lets support in — see Support shell) | `action.json` |
| `port_forwards` | — | `port_forwards.json` |
| `set_port_forwards` | `{"rules": [{"preset": "minecraft-java", "destIp": "192.168.1.50", "port": "25565", "proto": "tcp", "direct": false, "enabled": true}]}` (the whole list; `id` for a rule kept — see Port forwards) | `action.json` |

`select_entry`, `reset_entry`, `set_probe_interval`, `set_rules` and
`restart_xray` restart xray: client connections drop for a few seconds. The UI
must say so before it calls them. So must `set_power`: the stacks change hands. `pin_balancer` / `unpin_balancer` take effect
live, without a restart, and survive later restarts.

## Operator lock

For customer routers in a public release, the operator can lock a router to the
UI's simple view:

    uci set vectra-controller-pro.main.ui_lock=1 && uci commit vectra-controller-pro

`1`, `true`, `yes`, `on` (any case) lock; `0`, anything else or no option at
all is unlocked. vctl reads it at every call, so it takes effect on the next
call — nothing restarts.

- `status.ui.locked` is `true` while the lock is on; the UI then offers only
  the simple view.
- The router refuses the Pro view's methods itself, before doing any work:
  `balancers`, `nodes`, `logs`, `pin_balancer`, `set_probe_interval`,
  `port_forwards` and `set_port_forwards` (an owner whose router is locked
  manages port forwards from Vectra Connect) answer
  `{"ok": false, "code": "locked", "detail": null}`. A refused READ method
  answers with this action object instead of its usual shape — check for it
  before reading the answer.
- Still served: `status`, `entries`, `diagnostics`, `select_entry`,
  `reset_entry`, `restart_xray`, `rules` and `set_rules` (My sites is the
  simple view's), `services` and `set_service` (a service's country is the
  owner's choice too), `set_power` (turning Vectra on and off is the owner's),
  `set_remote_shell` (whether support may run commands here is the owner's), and
  `unpin_balancer` (it only hands a balancer back to the provider's own
  choice; the simple view needs it to undo a pin made earlier in Pro).
- It is a product policy, not a security boundary: root on the router can
  change UCI.
- The panel can lock a router too: `"ui": {"lock": true}` in the operator
  config it delivers (the file the daemon keeps; vctl reads it at every call,
  no daemon call). The router is locked when UCI or the panel says so, and an
  operator config the router cannot read locks it — it cannot tell.
- The setup wizard is the simple view: `setup`, `wan_check`, `wifi_scan`,
  `set_wifi`, `optimize_wifi` and `finish_setup` are always served.

## Power

Vectra has a switch of its own: the router's owner turns it on and off here,
and a person at the console with `vectra on`, `vectra off` and `vectra
status` (`vctl power`). On is the init script's takeover — the previous
Vectra agent and PassWall2 stopped and disabled, and noted as taken. Off gives
the router back as it was: what was taken comes back; where nothing was, the
internet goes out directly, without a VPN.

`status.power` — answered whether vctl runs or not:

- `enabled`: Vectra is switched on — UCI `vectra-controller-pro.main.enabled`
  and its boot links (`/etc/init.d/vectra-controller-pro enable`) both on, or
  a trial running (`vectra on --trial`, the console's alone: it leaves both as
  they were, so that a reboot gives the router back).
- `running`: procd runs vctl. The two can disagree for a few seconds after a
  change: `enabled: false, running: true` is Vectra being turned off.
  `enabled: true, running: false` for longer is a Vectra that did not start —
  a takeover that could not take the router hands it back — or that stopped.
- `holder`: who carries the LAN's traffic now: `vectra` (vctl runs and its
  data plane — the `inet vctl` table — is loaded); else `passwall2` (PassWall
  runs); else `agent` (the previous Vectra agent runs, PassWall does not);
  else `direct`. vctl running is not enough: `running: true` with `holder:
  "direct"` is a Vectra that runs but carries no traffic yet — before its
  first setup, or with a render that failed — and the LAN goes out without
  the VPN (`dataplane.loaded` says the same, and the views read that).
- `handBack`: who takes the traffic when Vectra is turned off — `passwall2`,
  `agent` or `direct` — from what the takeover noted it took; `null` while
  Vectra is off.

`status.legacy.passwall` — what became of PassWall2 on this router:
`installed` (the takeover keeps it as the way back), `retired` (vctl removed
it once Vectra had carried the traffic, switched on for good, for a day —
UCI `passwall_retire_after`; its configuration is kept in
`/etc/vectra-controller-pro/backup`; `passwallRetiredAt` says when — also
where a person had removed its packages and vctl, after the same day,
tidied what they left) or `absent`; `null` when the router did not look.
Once it is `retired`,
`handBack` is `direct`: turning Vectra off leaves the router on plain
internet.

- `wouldIdle`: Vectra does not run, and switched on now it would carry
  nothing — no operator config yet, and no PassWall2 configuration to route
  by (a router taken from a PassWall2 that carried its traffic is routed by
  that configuration until it is linked). The LAN would go out without a
  VPN until the router is linked: the page says so, and asks, before it
  turns Vectra on. `false` while vctl runs.

`set_power`: `{"on": true}` turns Vectra on for good (after a reboot too),
`{"on": false}` turns it off; `"force": true` turns it on also where it would
carry nothing yet (`power.wouldIdle`). Answers:

- `power_on`, `power_off`: it was so already; nothing changed.
- `pending`: the change runs in a process of its own and outlives the call.
  The internet drops for a few seconds while the stacks change hands; the
  change takes seconds, up to a minute where PassWall is slow to stop. Poll
  `status` until `power.enabled` and `power.running` both say it. An `on` the
  takeover could not complete stays `enabled: true, running: false`, with
  `holder` saying who got the router back.
- `busy`: a change is still in flight (from this page, another, or the
  console); nothing changed.
- `would_idle`: `{"on": true}` without `force` where Vectra would carry
  nothing (`power.wouldIdle`); nothing changed — ask the person, then send
  `{"on": true, "force": true}`.
- `invalid_params` for anything but `{"on": true|false}` (and `force`);
  `apply_failed` when the change could not be started — nothing changed;
  `internal`.

## Router password

Everything on this page is behind LuCI's login, and LuCI's login is root's
password. OpenWrt ships without one: rpcd then lets anyone on the LAN in.
`setup.passwordSet` says whether there is one (see Setup wizard); the UI sets
and changes it with LuCI's own call, the one System → Administration makes:

    luci.setPassword {"username": "root", "password": "…"}  →  {"result": true|false}

- It is luci-base's rpcd plugin (`/usr/share/rpcd/ucode/luci`): it pipes the
  password, shell-quoted, twice to busybox `passwd root`; `result` is
  whether `passwd` took it. The rpcd session stays valid: nobody is logged
  out, and the next login asks for the new password.
- This package's ACL grants exactly `luci.setPassword` beside `vectra`
  (`write`), so the page works without luci-mod-system too
  (`TestRPCDACLMatchesTheMethods` pins it).
- The LuCI view hands it to the app as `mount(host, {call, setPassword,
  lang})`: `setPassword(password)` resolves `true` only when LuCI answers
  `result: true`, `false` otherwise; a ubus or network failure rejects (the
  same `reject`/`nobatch` as `vectra`). A host that hands none gets no
  password step and no password actions.
- The UI asks for at least 8 characters, twice; it shows what went wrong
  next to the fields, keeps nothing and logs nothing.

## Support shell

The panel can run a command on the router as root (its job
`run_terminal_command`): support's console, and — were the panel or an
operator's login ever stolen — root on every router at once. So the router
runs it only where its owner allows it: UCI
`vectra-controller-pro.main.remote_shell` `1`. Refused, the panel is told
`support shell access is off on this router`, and nothing runs. A new router
starts with it off; one upgraded from a vctl that had no such switch keeps the
shell it had (`1`, written once by the package). Every check-in reports it
(`inventory.remoteShell`).

- `status.remoteShell`: `true` while the router runs the panel's commands.
  Anything but a yes in UCI — the option absent, a config the router cannot
  read — is `false`: what the router does.
- `set_remote_shell`: `{"on": true}` or `{"on": false}`, through uci and
  committed: the next job sees it, nothing restarts. Answers
  `remote_shell_set` (read `status` for the state), `invalid_params` for
  anything but `{"on": true|false}`, `internal` when uci failed (nothing
  changed). The simple view's: the operator's lock never refuses it.

## My sites

The router owner's own exceptions: sites that always go WITHOUT the VPN
(`direct` — a bank that refuses VPN addresses) and sites that always go
THROUGH it (`proxy` — blocked, but in none of the provider's lists).

- `rules` answers both lists as the router keeps them, and `max`: how many
  sites each list may hold (100).
- `set_rules` REPLACES both lists. Send both, always; `[]` empties one, and a
  list left out is refused. The router normalizes every entry itself — the UI
  may do the same for display, but what `rules` answers is what counts:
  - trimmed and lowercased; a scheme (`https://`), `user:password@`, a path,
    `?query` and `#fragment`, a port (`[2001:db8::1]:443` → `2001:db8::1`),
    a leading `*.`, `.` or `www.` and a trailing dot are dropped. `www.com`
    keeps its `www.`: it is a site of its own, not all of `.com`;
  - then it must be a domain, an IPv4/IPv6 address or a CIDR. A domain is
    kept in Unicode (`XN--C1AAPKOSAPC.XN--P1AI` → `госуслуги.рф`) and covers
    its subdomains; one label (`рф`) is a whole top-level domain. A CIDR is
    kept masked (`1.2.3.4/24` → `1.2.3.0/24`), a full-length one as its
    address. Anything else is refused;
  - a repeat is dropped (the first stays, in the order sent); a site in both
    lists is refused.
  - `sites-vector.json` pins it from both sides: the router's normalizer
    (`internal/sites`) and the UI's (`ui/app/src/lib/sites.ts`) are tested
    against the same inputs, so the list a person edits is the list the
    router keeps. The one difference is on purpose: the UI refuses a single
    label (`сбербанк`) as a mistyped site; the router keeps it as a whole
    top-level domain.
- Answers: `rules_set`; `pending` as for `select_entry` — the lists are kept
  only once the router runs them, so poll `rules`; `invalid_params` with a
  `detail` naming the first entry refused, as sent — `direct[2] "exa mple.com":
  contains a space or a control character` — or the list that is too long
  (`direct: 101 sites; at most 100`); and the usual failures
  (`controller_down`, `busy`, `apply_failed`, …).
- How the router applies them: as xray's FIRST routing rules, ahead of every
  rule of the provider's, for the LAN's traffic only. `direct` leaves through
  the provider's own freedom outbound (or one the router adds,
  `vctl-direct`); `proxy` goes where the provider sends everything no rule
  names — its main balancer. A more specific site wins over a broader one in
  the other list: `online.sberbank.ru` through the VPN and `sberbank.ru`
  direct do both apply. A site is recognised by the name the browser asks for
  (TLS SNI, HTTP Host, QUIC), so while any site is set the router sniffs
  `http`, `tls` and `quic` with `routeOnly` whatever the operator's config
  says, and still connects to the address the client resolved.

## Port forwards

Simple on purpose (the owner, 06.10): a rule is a device, a port or a range
that is the SAME outside and on the device, a protocol, «through the VPN or
past it», on or off.

- `port_forwards` answers `rules` (the router's own, in the order it keeps
  them), `devices` (`{name, ip}` from the DHCP leases and static hosts, LAN
  only, sorted by address, at most 64; `name` is `null` when the device gave
  none — no MAC address is ever sent), `cgnat`, `directActive` and `max`
  (32). A rule:
  `id` (8 hex), `preset` (the UI's tag for what the forward is for —
  `minecraft-java`, `playstation` — or `null`; 1-24 of `a-z`, `0-9`, `-`, else
  `invalid_params`; the router keeps the tag and never checks it against a
  list: the UI owns the catalogue), `destIp`, `deviceName` (read-only: the device's name now, or
  `null`), `port` (`"25565"` or `"3478-3480"`), `proto` (`tcp`, `udp`,
  `both`), `direct` (the device's own new connections go out past the VPN —
  blocked sites still go through it: their FakeDNS addresses are never sent
  past), `enabled`.
- `cgnat: true`: the WAN's address is the provider's shared one
  (100.64.0.0/10) or a private one — nothing from the internet reaches a
  forward (or it must be forwarded on the router in front too). One line in
  the UI, only when true.
- `directActive`: whether «past the VPN» is in effect for every enabled rule
  that asks for it. `null` when none asks; `false` when some rule's `direct`
  is not carried out right now — Vectra is off or stopped, the kill switch is
  armed, or the router could not write its data plane set (it keeps trying) —
  so a `direct: true` never reads as working when it is not; `true` otherwise
  (also in the rescue's direct mode, where every device goes past the VPN).
- «Past the VPN» and the kill switch: the kill switch wins. With it armed the
  router does not let any device past the VPN (the rule is not in its data
  plane, as for the P2P bypass), and `directActive` is `false`: the UI shows
  its one muted line, the device goes through the VPN for now. Even past the
  VPN, what stays in the tunnel: blocked sites (their FakeDNS addresses) and
  the device's DNS (ports 53 and 853, which the ISP forges even from public
  resolvers) — with the router's DNS hijack on or off.
- `set_port_forwards` REPLACES the list: send every rule, always; `[]` removes
  them all, and a list left out is refused. A rule without `id` is new (the
  router picks one); `preset` may be left out or `null`; `deviceName` is not
  sent. Inbound through the VPN is not
  possible: a forward reaches the router's WAN address only.
- Answers: `port_forwards_set`; `invalid_params` (a field missing, unknown or
  of the wrong type, an `id` twice, a `proto` or `port` out of range, a
  `destIp` that is not IPv4), `too_many` (more than 32), `dest_not_lan` (the
  address is outside the LAN, its network or broadcast address, or the LAN
  could not be read), `dest_is_router`, `port_conflict` (an enabled rule
  shares a protocol and a port with another enabled rule — this list's, a
  redirect made in LuCI, which is never shown, or a port the router itself
  takes from the WAN, such as DHCP renewals or SSH opened there; a wan rule
  with no port, or a wan zone accepting everything, opens the whole router
  and is not counted — which ports it serves is not known),
  `busy` (another change is being applied, or uncommitted firewall edits wait
  in uci), `apply_failed` (nothing changed: when fw4 failed to reload the
  new rules, the previous config was put back and reloaded; or the firewall
  zones of the wan and lan networks cannot be told — none, or two, carry
  one), `internal` (fw4
  failed to reload even the previous config — it is back on disk), `locked`
  (see Operator lock). Vectra Connect's `set_port_forwards` answers the same
  codes, `applied` on success; more than 32 rules is `too_many` there too.
- How the router applies it: each rule is an fw4 `config redirect` named
  `vectra_pf_<id>` (`name 'Vectra: <device name or address>'`, or
  `'Vectra: <preset> → <device name or address>'`, the tag kept in
  `option vectra_preset`; DNAT from the zone carrying the wan network to
  the one carrying lan, whatever LuCI named them, `src_dport` = `dest_port`, hairpin on), so it works with vctl stopped
  and shows in LuCI; vctl changes only those sections. `direct` puts the
  device into vctl's data plane set `vctl_pf_direct4`.

## Services

A few services — only those a person really picks a country for: `youtube`,
`tiktok`, `telegram` — can leave the subscription entry's own path for a
country the running entry has. Everything else goes where the entry sends it.

- `services` answers `available` (false where the router routes by the
  operator's policy — native, PassWall — and `services` is then `[]`) and one
  row per service: `choice` (ISO code, `null` = the entry's own path),
  `defaultCountry` (the country of the entry's own path, `null` when it names
  none or several), `countries` (the ISO codes the running entry's outbounds
  name — a whitelist level is no country; empty when the entry has no rule of
  the service's own to fall back to, and then no country is offered),
  `active` (the choice runs now) and `stale` (chosen, but the running render
  does not carry it — the entry lost the country or the service's own rule:
  the service runs on its default; it runs again if the entry offers it
  again, and «По умолчанию» clears it).
- `set_service` sets one service; `""` goes back to the entry's own path.
  Answers: `service_set`, `pending` as for `select_entry` (poll `services`);
  `invalid_params`, `unknown_service`, `unknown_country` (the running entry
  has no outbound of it, or no rule of the service's own for the overlay to
  fall back to), `unavailable` (the operator's policy routes this
  router), and the usual failures.
- How the router applies it: a balancer over the chosen country's outbounds
  (leastPing, observed), whose fallback is the entry's own path for the
  service — a dead country falls back to the default, never to nothing. Its
  rules follow the owner's own sites (which win) and precede the provider's,
  for the LAN's traffic only.

## Tune

The router's tune («Разгон роутера», `vctl tune`, internal/tune): at the
package's install (its postinst, once vctl runs) and at every start of the
daemon, vctl sets what gets the most out of the router — only what nobody
set otherwise, every value it changes backed up first. A trial (`vectra on
--trial`) waits until it is kept. The package's removal puts back what the
tune changed and is still as it left it (`vctl tune undo`); an upgrade does
not. UCI `vectra-controller-pro.main.tune` '0': the tune changes nothing.

`status.tune` (`null`: not read) — `enabled`: that switch; `profile`:
`lowmem` (less than 384 MiB of RAM) or `standard`; `items`, in this order:

| id | what the tune sets | `value` (now) |
|---|---|---|
| `zram` | compressed swap (zram-swap, a dependency of the package): its service enabled and started — `lowmem` only | the zram swap's size in MiB, `null` when none is up |
| `swappiness` | `vm.swappiness` 80 — `lowmem` only | the kernel's value |
| `vfs_cache_pressure` | `vm.vfs_cache_pressure` 200 — `lowmem` only | the kernel's value |
| `packet_steering` | `network.@globals[0].packet_steering` '1': every core takes the network's receive work — 2 cores or more | the option, `null` unset |
| `flow_offloading` | `firewall.@defaults[0].flow_offloading` '1': software flow offloading, never hardware | the option, `null` unset |
| `cron_loglevel` | `system.@system[0].cronloglevel` '9': busybox crond logs its warnings, not every job it starts into logread's 64 KB ring; applied with cron's reload. Unset and the stock levels 5, 7 and 8 (all log every job) are set to 9, the old level backed up for undo; 9 and above, anything unusual, or a level changed after the tune set 9 are `user_set` | the option, `null` unset |
| `tmp_leftovers` | vctl's own leftovers in RAM removed: an update's package (`/tmp/vectra-controller-pro-update.ipk`, `/tmp/vectra-controller-pro-auto-*.ipk`) and the vault's temp files in `/var/run/vectra-controller-pro` — by these exact names only, older than 10 minutes and open in no process. Never backed up (nothing to put back), never `user_set` | the MiB of such leftovers found, `null` when none |

`target`: what the tune sets (`on` for `zram`). `state`: `applied` (the tune
set it, and it is so), `already` (so before the tune), `pending` (not so yet:
the next run sets it), `user_set` (set otherwise on purpose — an option set
to anything else, a sysctl in a file of the owner's own, zram switched off
after the tune switched it on: left alone), `skipped` (`reason` says why).
`reason` (`null` when none): `off` (the switch), `enough_ram`, `one_core`,
`not_installed` (no zram-swap), `no_kernel_module` (zram-swap, but no zram
module for the running kernel — kmod-zram is for another one: the swap
cannot start), `no_cron` (no /etc/init.d/cron), `no_system` (no system
section in /etc/config/system), `no_swap`, `not_supported` (no
/etc/init.d/packet_steering), `no_fw4`, `no_defaults`, `hw_offload` (the
owner's `flow_offloading_hw` waits for software offloading: the tune would
switch hardware offloading on with it), `no_kernel_support` (no
`nft_flow_offload`: fw4 would fail to load its ruleset), `uci_pending`
(uncommitted changes wait in uci for that config), `check_failed` (fw4
refused its ruleset with it: taken back), `unreadable`, `failed` (with
`pending`: the last run's change failed; tried again at the next).

vm.min_free_kbytes is never the tune's. The firewall is reloaded live (fw4
replaces its own `inet fw4` table in one transaction; vctl's `inet vctl` and
its policy route are untouched), and netifd never is.

`vctl tune plan` (and `--json`, its `analysis` object) adds where the
router's memory and flash go, read only and never in `status.tune` (it walks
every process and /root): `memAvailableMiB`, `swapTotalMiB`, `swapUsedMiB`,
`topRss` (the five processes holding the most RAM: `name`, `pid`,
`rssMiB`), `overlayFreeMiB` (`null`: unread) and `reclaimable` — `.ipk`
files in /root and one level down, and directories there named `*staging*`
(`path`, `mib`, `kind`: `package` or `staging`): the operator's to remove,
never vctl's.

## Language

The router answers in CODES, never in prose, so the UI can speak ru, en and zh.

- `diagnostics.checks[].id` + `status` + `params` → the UI owns the sentence.
- `action.code` → the UI owns the sentence. `action.detail` is an optional
  technical string (English, from Go or xray) shown verbatim as secondary text.
- `logs` lines are raw log text — shown as-is, never translated.
- Provider data (`entries[].remark`, node and balancer tags) is shown as-is.

## Enumerations

- `status.engine.state`: `running`, `starting`, `reloading`, `backoff`,
  `exited`, `stopped`, `failed`, `idle`, `unknown`.
- `status.subscription.source`, `entries.source`: `panel` (the operator's
  choice) or `local` (chosen on the router). `overrideStale: true` = the location
  chosen on the router is no longer in the subscription; the panel's is in use.
- `status.probe.source`, `balancers.probe.source`: `default` (vctl's router
  default), `local` (set on the router), `provider` (the provider's own value).
- `balancers[].role`: `main` (carries everything no other rule matched),
  `routed` (named domains/IPs are routed to it), `reserve` (only reached as
  another balancer's fallback), `unused`.
- `balancers[].strategy`: xray's strategy type — `leastLoad`, `leastPing`,
  `random`, `roundRobin`.
- `balancers[].fallback`: `null`, or `{"tag", "balancer"}` where `balancer` is
  the balancer that fallback outbound leads to, or `null` when it is a plain node.
- `balancers[].matchers[].kind`: `domain`, `ip`, `all` (the catch-all rule),
  `other` (a rule matching on something else — `sample` then holds entries
  like `port:443` or `protocol:bittorrent`). `sample` holds up to 6 raw xray
  matchers (`geosite:telegram`, `domain:ru`, `geoip:ru`, CIDRs); `total` is how
  many the rule has.
- `balancers[].selected`: the targets xray's strategy currently prefers
  (`xray api bi`); `[]` = none qualifies, traffic goes to `fallback`; `null` =
  the API could not be asked. `pinned` overrides it.
- `diagnostics.checks[].status`: `ok`, `warn`, `fail`, `unknown`.
- `logs.lines[].level`: `debug`, `info`, `warn`, `error`, `unknown`;
  `source`: `vctl`, `xray` or `null`.
- `status.power.holder`: `vectra`, `passwall2`, `agent`, `direct`;
  `status.power.handBack`: `passwall2`, `agent`, `direct` or `null` (see
  Power).
- `status.legacy.passwall`: `installed`, `retired`, `absent` or `null`;
  `status.legacy.passwallRetiredAt`: RFC 3339 or `null` (see Power).
- `balancers.defaultOutbound`: the tag of xray's default outbound — the
  config's FIRST outbound. xray sends it a connection no rule routes, and the
  traffic of a balancer that has neither a target nor a fallback. `null` when
  the config has no outbounds, or the first one has no tag.
- `action.code` on success: `entry_selected`, `entry_reset`, `balancer_pinned`,
  `balancer_unpinned`, `probe_interval_set`, `rules_set`, `service_set`, `xray_restarted`,
  `wifi_set`, `wifi_optimized`, `setup_finished`, `power_on`, `power_off`,
  `remote_shell_set`, `port_forwards_set`,
  `pending` (the controller accepted the request and is still applying it; it
  is remembered only if it succeeds — poll `status`, or `rules`, to see it
  land).
  On failure (`ok: false`): `invalid_params`, `unknown_entry`, `unknown_service`,
  `unknown_country`, `unavailable`,
  `unknown_balancer`, `unknown_node`, `no_entries_cache`, `controller_down`,
  `xray_api_unavailable`, `apply_failed`, `busy`, `internal`, `would_idle`
  (set_power: see Power), `locked` (the
  operator's lock refuses this method on this router — see Operator lock),
  `unsupported` (the router has a radio the Wi-Fi tuning has no rules for —
  see Setup wizard), `too_many`, `dest_not_lan`, `dest_is_router`,
  `port_conflict` (see Port forwards).

## Diagnostics

| id | params |
|---|---|
| `controller_running` | `pid` |
| `xray_running` | `pid`, `uptimeSec` (or `state` when not running) |
| `xray_api` | `listen` (or `error`) |
| `dataplane_loaded` | `tproxyHits` |
| `no_leak` | `wouldLeak`, `sinceStart`, `escaped`, `killSwitch`, `other`; `drops`, `dropsSinceStart` when the kill switch is on |
| `single_stack` | `passwallRunning`, `agentEnabled` |
| `panel_link` | `lastCheckInAgoSec` |
| `subscription_ua` | `reason` when not ok (`malformed_happ`) |
| `memory` | `availableMiB`, `totalMiB`, `xrayMiB` |
| `dead_nodes` | `count`, `tags` |
| `balancer_fallback` | `balancers` (the balancers whose traffic is being passed on because they have no working member), `main`, `mainBlocked`, `mainDirect`, `blocked`, `blockedBalancers`, `warming` |
| `pinned_node_dead` | `balancer`, `node`, `main` when not ok |
| `tune` | `enabled`, `profile`, `items` (the ids in place: `applied` or `already`), `zramMiB` (`null` when no zram swap is up) |

- `no_leak` reads the vctl nft table's counters:
  - `vctl_would_leak` (kill switch off) / `vctl_killswitch_drops` (on) count
    the tcp/udp TPROXY did not take — including, by design, the fall-through
    of every xray restart (let through with the switch off, dropped with it
    on). So they are judged only from a baseline the router takes once each
    xray start has settled.
  - `vctl_tproxy_escaped`: packets TPROXY took and the router then forwarded
    unproxied (the fwmark policy route is missing). Never by design.
  - `vctl_unproxied_other`: everything that is not tcp/udp (a LAN ping) —
    never proxied, by design.

  Params: `wouldLeak` the total; `sinceStart` what got past the running xray;
  `escaped` the escapes since the start; `killSwitch` whether the LOADED
  ruleset has the switch armed; `drops` / `dropsSinceStart` its drops, when
  armed; `other` the not-tcp/udp total, for information only. `sinceStart`,
  `escaped` and `dropsSinceStart` are `null` until the router has a baseline
  (and `escaped` / `other` on a ruleset that lacks the counter). Status:
  `fail` if anything escaped, or `sinceStart` or `dropsSinceStart` ≥ 50;
  `warn` if 1–49; `ok` at 0; `unknown` when there is no baseline yet and the
  counters are not all 0.
- `balancer_fallback` follows traffic as xray (v26.3.27) routes it — from the
  main balancer, then each routed one, down the fallbacks, to whatever takes
  it; a balancer no traffic reaches is never listed. A balancer carries its
  traffic when it is pinned; when leastLoad/leastPing have a target; when
  random/roundRobin have a member the observatory saw alive. random/roundRobin
  WITH a fallback skip members it saw dead and pass the traffic on when none is
  left; WITHOUT one they keep sending it to their dead members (the chain is
  blocked there). A plain-outbound fallback — or, with no fallback at all,
  xray's default outbound (the config's first) — carries it when it is a node
  the observatory saw alive or never probed; a dead node, a blackhole or a
  missing tag blocks it; freedom sends it out WITHOUT the VPN.
  `main` = the main balancer passes its own traffic on; `mainBlocked` = the
  main chain ends with nothing to carry it; `mainDirect` = it ends in freedom;
  `blocked` / `blockedBalancers` = some chains (their first balancers, main
  first) end with nothing to carry them. Status: `fail` if `blocked` or
  `mainDirect`; `warn` if any balancer passes traffic on; `ok` otherwise.
  Right after an xray start the observatory has probed no member of the main
  balancer yet: then `unknown` with `warming: true` and the rest at their
  defaults. Without the observatory or xray's API the check is not reported.
- `pinned_node_dead`: a pin whose node the observatory saw dead. xray sends a
  pinned balancer's traffic to the pin whatever its health: `fail` with
  `main: true` when it is the main balancer (everything else), `warn` with
  `main: false` otherwise.
- `memory`: `warn` below 64 MiB available, `fail` below 48.
- `tune` (see Tune): `warn` when a `lowmem` router runs without its zram swap
  (the memory guard assumes one); `ok` otherwise.

An id the UI does not know is shown with its raw `id` and `params` — the router
may grow checks before the UI learns their sentences.

## Conventions

- Times are RFC 3339 UTC strings or `null` when unknown. Never `0` or `""`.
- A value the router could not measure is `null`, not `0`. `delayMs: null`
  means "not probed yet", `delayMs: 0` would be a lie. `alive: null` = the
  observatory has no record of that node yet.
- Fields that are unknowable on a given router (no xray running, no
  subscription yet) are `null` rather than absent.
- `nodes[].lastSeen` / `lastTry` are `null` with xray's burstObservatory (what
  the providers ship): it reports a verdict and delay statistics, no times.
  Only the classic observatory fills them.
- A missing ubus object is ubus status 4 — LuCI 24.10 words it "Resource not
  found"; match on the code, not the text.
- `nodes[].countryHint` is derived from the node TAG (`bridge-de5` → `DE`) and
  is `null` whenever the tag does not name a country unambiguously
  (`whitelist-lv3` is whitelist LEVEL 3, not Latvia).
- Node addresses and ports are shown; credentials (UUIDs, keys, passwords,
  short IDs, subscription URLs) are NEVER in any response. Every answer goes
  through the router's scrub on its way out (`cmd/vctl/rpcd_scrub.go`), so
  what the router quotes — a log line, `lastExit.error`, an action's `detail`
  — has them replaced: `<redacted>` (the router's own secrets, a value under a
  secret's name), `<uuid>`, `<link>` (a share link), `<secret>` (a long bare
  token, in quoted text only). A web address keeps its host:
  `https://sub.example.com/<redacted>`. Kept exactly, and not credentials:
  `status.controlPlane.routerId` (the router's id in the panel — support asks
  for it; the panel takes nothing from a router without its token beside it),
  `setup`'s claim `code`, `qr` and `botUrl` (shown to link the router), the
  owner's own sites (`rules`) and Wi-Fi names.

## Setup wizard

What a customer runs after unboxing, on the Vectra page in LuCI: the router's
password, when it has none (`passwordSet: false`) → a look at the internet
connection → the Wi-Fi, tuned and each band's network optionally renamed →
linking the router to their Vectra account (ADR-0006) → a server → done. The
router sets its internet connection up itself: the wizard shows it and checks
it, and never changes it. The password goes to LuCI's own change, never to
vctl (see Router password); it is the one step that cannot be skipped, and the
wizard opens by itself on a router that was never set up and has none. It is
the simple view: the operator's lock never refuses it.

| method | params | answer |
|---|---|---|
| `setup` | — | `setup.json` |
| `wan_check` | — | `wan_check.json` |
| `wifi_scan` | — | `wifi_scan.json` |
| `set_wifi` | `{"radios": {"radio0": {"ssid": "…", "key": "…"}, "radio1": {"ssid": "…"}}}` | `action.json`: `wifi_set`; `invalid_params`, `busy`, `apply_failed`, `internal` |
| `optimize_wifi` | `{"channels": {"radio0": 11, "radio1": 149}, "radios": {…as set_wifi}}` — both optional | `action.json`: `wifi_optimized`; `invalid_params`, `unsupported`, `busy`, `apply_failed`, `internal` |
| `finish_setup` | — | `action.json`: `setup_finished` |

`setup`:

- `done`: the wizard was finished (UCI `vectra-controller-pro.main.setup_done`).
  A router that already worked before this version — it has an operator
  config, or a WAN address, Wi-Fi on and secured on every radio that is on,
  and a root password — is marked done when the package is installed: the
  fleet never sees the wizard.
- `passwordSet`: root has a password, so LuCI's login asks for one — the
  second field of root's line in `/etc/shadow` is not empty (rpcd lets anyone
  in on an empty one; a locked `!` lets nobody in). `false` out of the box;
  `null` when the router cannot tell (no shadow file, no root in it). Never
  the password or its hash.
- `wan` (read-only): `proto` `dhcp`, `pppoe`, `static` or `other`; `link` a
  cable in the WAN port (`null`: the router cannot tell); `ipv4`, `gateway`,
  `dns` what the router has now (`null` / `[]` when none). No credential of the
  connection is ever in it.
- `lan.ipv4`: the LAN's IPv4 address as netifd has it up (`ubus call
  network.interface.lan status`, the first one), `null` when netifd cannot
  say: where this page opens on a device that does not ask the router's DNS
  (a VPN app, private DNS), which the names (`my.vectra-pro.net`,
  `vectra.lan`) need.
- `wifi.radios`: one entry per radio (UCI `wifi-device`), in file order:
  `device` (its UCI name, the key `set_wifi` and `optimize_wifi` take);
  `band` `2g`, `5g`, `6g`, `60g` or `null`; `channel` a number, or `null`
  when the radio picks its own (then `auto` is `true`); `htmode` (`null`:
  unset) and `width`, the channel width it runs in MHz — 20 (`NOHT`, `HT20`,
  `VHT20`, `HE20`, `EHT20`), 40 (`HT40`, `HT40+`, `HT40-`, `VHT40`, `HE40`,
  `EHT40`), 80 (`VHT80`, `HE80`, `EHT80`), 160 (`VHT160`, `HE160`,
  `EHT160`) or 320 (`EHT320`); `null` when `htmode` is unset or unknown;
  `country` (`null`: unset); `txpower` in dBm, `null` when unset — the
  driver's maximum; `maxPower`: `txpower` unset, or at least the most the
  driver offers (the highest `dbm` of `iwinfo txpowerlist`, less the
  `txpower_offset` of `iwinfo info`) — and no access point on the radio
  sets its own `vif_txpower`. Then the radio's first access point, the
  network of that band: `ssid` (`null`: none), `secured` (encryption is not
  none/open), `enabled` (it and its radio are on). `ap`: the radio carries
  an access point — only such a radio can be named. `mesh`: it carries a
  mesh or ad-hoc interface, whose channel is its peers': the tuning never
  changes it. `up`: netifd has the radio up (`ubus call network.wireless
  status`); `null` when netifd cannot say. Never a key.
- `wifi.tunable`: every radio is 2.4 or 5 GHz, the bands the tuning has rules
  for. The country is router-wide, and Panama's rules would switch a 6 GHz
  radio off: a router with one — or with a radio of unknown band — is not
  tunable, and `optimize_wifi` refuses it (`unsupported`).
- `wifi.tuned`: every enabled radio has `country` `PA`, `maxPower` and a
  fixed channel — the tuning below is in place. No radio enabled (or none at
  all): `true`. `null` when `tunable` is `false`.
- `wifi.verdict`: how the wizard judges the Wi-Fi as it is — not whether it
  is the tuning's recipe (`tuned` says that). Of the radios that are on:
  - `boost`: there is more to get, and the boost gets it — a radio that is
    not `up`, or 2.4 GHz on a fixed channel other than 1, 6 or 11 (2-5 and
    7-10 overlap two of them; 12 and 13 some clients do not see). The wizard
    suggests the boost («можно выжать больше»).
  - `manual`: none of that, but a choice of the owner's the tuning would
    undo — 5 GHz on a channel that needs radar detection or that its width
    cannot carry (the rules below), or the power more than 6 dB below the
    driver's most (`txpower`, or an access point's lower `vif_txpower`,
    against `iwinfo`'s list less the offset). Left alone, not suggested.
  - `fine`: otherwise — any country, 2.4 GHz on 1, 6, 11 or auto, 5 GHz on
    auto or a channel the rules allow, the power at most 6 dB down, and
    what the router cannot tell (`up` `null`, no `iwinfo`) counted as fine.
    No radio on: `fine`.

  A mesh radio's channel is its peers' and never judged. `null` when
  `tunable` is `false`. The boost itself (`optimize_wifi`) is the same
  whatever the verdict, and the wizard keeps it in reach either way.
- `wifi.apply`: the last Wi-Fi change's restart since the router booted
  (`null`: none): `{state, at, detail, radios}`. `state`:
  - `applying`: the change is committed; the Wi-Fi is being restarted and
    checked. `set_wifi` and `optimize_wifi` set it before they answer.
  - `ok`: every radio that is on in the new settings, with an access point
    on, came up with it.
  - `partial`: some did not, and none of those was up before the change —
    nothing to go back to, so nothing was rolled back.
  - `rolled_back`: a radio that was up before the change did not come up;
    the previous settings are back (`detail` says whether it came up with
    them).
  - `unverified`: nothing could be judged — netifd did not answer; or no
    radio came up and none was up before (a fresh box, a radio the router
    cannot drive); or the check stopped before it finished.
  - `failed`: a radio that was up before did not come up, and the previous
    settings could not be put back.

  `at`: when that state was reached (for `applying`, when the change was
  committed). `detail`: English, for secondary text, or `null`. `radios`:
  `{"<device>": up}` for every radio the check expected up; `{}` while
  `applying`.
- `wifi.suggested`: a network name of the router's own, `Vectra-XXXX` — XXXX
  the last 4 hex digits (uppercase) of the WAN's MAC address, or of br-lan's
  when the WAN has none; `null` when neither is known. Offer it for a network
  still named as OpenWrt names it.
- `vectra.linked`: the router has an operator config (every fleet router is
  linked); then `claim` is `null`. `vectra.botUsername`: where support links
  go, linked or not — the Vectra bot the panel named; until it has (a box
  that has never been online), the support bot the box was prepared with
  (UCI `vectra-controller-pro.main.support_bot`, a Telegram username without
  `@`: 5-32 of `A-Z a-z 0-9 _`; an invalid value is ignored); else `null`.
  `claim.botUrl` never falls back: a code means something only to the bot
  the panel named.
  `vectra.owner`: `{"label"}`, the account the panel says the router belongs
  to, or `null` — linked or not, so a linked router can say whose it is.
- `vectra.claim` (`null` when linked, and while the controller is not running):
  `state` `unclaimed` or `claimed` (the panel reported an owner; the
  configuration follows); `code`, 8 characters of Crockford base32 — show it
  as 4-4; `qr`, the text to put in a QR code (`VECTRA:R1:…`), `null` until the
  panel has sent Vectra's key (the code works regardless); `expiresAt`, when
  the code stops being valid, 30 minutes after it was made — a new one
  replaces it 10 minutes before, so a code just replaced still works for 10
  minutes; `botUrl`, `https://t.me/<bot>?start=rt_<code>` for the bot the
  panel named, else Vectra Connect's mini app,
  `https://t.me/VectraConnect_bot/start?startapp=rt_<code>` (never `null`
  while there is a code); `owner`, `{"label"}` or `null`.

`wan_check`: `{link, ipv4, dns, internet, panel, checkedAt}` — active checks,
within 8 s, that hold with xray down and with the kill switch armed (every
socket carries the control plane's mark; names are resolved at the WAN's own
DNS servers): `dns`, a name resolved; `internet`, an HTTP generate_204
answered 204 (a captive portal does not); `panel`, the panel answered at all.

`wifi_scan`: `{radios, scannedAt}` — what the last `optimize_wifi` heard.
It never scans itself: a scan disturbs the Wi-Fi, and only `optimize_wifi`
comes with the person's agreement to that. Before the first since the router
booted: `{"radios": [], "scannedAt": null}`. `scannedAt`: when that scan
began. Per radio: `device`, `band`, `current` (the channel it was on; `null`:
unknown), `recommended` (the channel the scan points to, which the tuning
set unless `channels` named another — a mesh radio's own; `null` when there
is none: a mesh radio on auto), `networks` (how many it heard), `channels` (`[{channel, networks, strongest}]`, the channels
something was heard on, ascending; `strongest` in dBm, -100 when none of
them reported a signal), `error`: `null`, or why nothing was heard — `off`
(the radio is not up, or no access point is up on it: not scanned), `empty`
(it heard nothing, twice), `failed` (the scan failed, or netifd could not
say whether the radio is up). Then `recommended` is its fixed channel when
that is valid for its width, else 6 on 2.4 GHz, 36 on 5 GHz.

How `optimize_wifi` scans, before it answers: only the radios that are up,
one after the other, within 12 s for all of them together; each through its
first access point's interface (from `network.wireless status`). 2.4 GHz
with `iwinfo scan`; 5 GHz with `iw dev <if> scan freq … ap-force` limited to
36-48 and 149-165 when `iw` is installed (OpenWrt 24.10's filogic images do
not ship it), else with `iwinfo scan`'s sweep of every channel. The sweep
takes the access point off its channel: its clients stall while it lasts —
a few seconds on 5 GHz.

The channel the tuning sets — FIXED, never auto, never one that needs radar
detection (DFS, 52-144: most countries require it there, and a radar hit
takes the radio off the air for a minute), within what the radio's width can
carry. Networks weaker than -85 dBm do not count; a signal of -255 dBm or
below is no signal.

- 2.4 GHz: 1, 6 or 11 at 20 MHz or `HT40`; `HT40+` needs a channel up to 9
  (1 or 6), `HT40-` one from 5 (6 or 11). 12 and 13 are refused for client
  compatibility: some clients do not see them. Each candidate is scored by
  what it would share: every network within ±4 channels, its power (mW from
  dBm) × overlap (1 − |Δ|/5). The lowest score wins; a tie keeps the channel
  the radio is on.
- 5 GHz: the blocks 36-48 and 149-161 — 149-165 at 20 MHz; at 160 MHz and
  wider, 36-48 only — each scored by the power of the networks heard inside
  it. The less busy block wins, but a radio stays in the block it is on
  unless the other scores below 2/3 of it; a radio on auto, or outside both
  blocks, takes the less busy one, 36 on a tie. Within the block: at 20 MHz
  its least busy channel; wider, the channel the radio is on when that is in
  the block, else the block's first (36 or 149).
- A radio carrying a mesh or ad-hoc interface keeps its channel.

Changes (`invalid_params` for anything outside these rules, naming the
field; a refusal changes nothing and never quotes a key):

- `set_wifi`: `{"radios": {"<device>": {"ssid", "key"}}}` — each radio named
  (one that carries an access point: `ap`) gets `ssid` (1-32 bytes of text)
  on its first access point. `key` (8-63 printable ASCII characters)
  replaces the key and keeps the network's WPA mode (`psk`, `psk2`,
  `psk-mixed`, `sae`, `sae-mixed`; `ieee80211w` is left alone); a network in
  a mode that takes no passphrase (`owe`, enterprise) is refused a key.
  `key` may be left out only for a network that is already secured; an open
  one needs one, else `invalid_params` with `radios.<device>.key: the
  network is open — give it a key`. An open network given a key gets WPA2
  (`psk2`), and it and its radio are switched on — a fresh box's network.
  Naming a secured network never changes whether it is on. The radios left
  out are not touched, and neither is anything else on a radio named —
  except when the call switches that radio on (it was off): then no open
  interface comes up with it — every other open one on it (another access
  point such as a guest network, a mesh, ad-hoc or client interface) is
  kept off, and `detail` says so (`radio0: an open mesh interface on this
  radio kept off; give it a key`). On a radio that was already on, its
  other interfaces stay as they are.
- `optimize_wifi`: every radio gets `country` `PA`, its `txpower` option
  removed (the driver's maximum), `vif_txpower` removed from its access
  points, and a fixed channel — the one in `channels`, else the one its scan
  recommends (see `wifi_scan`) — except a mesh radio, which keeps its
  channel (naming it in `channels` is refused). `htmode` and everything else
  stay. A channel outside the rules above is refused, naming the radio and
  the rule (`channels.radio1: channel 149 cannot carry 160 MHz: at 160 MHz
  only 36-48`). `radios` names networks exactly as `set_wifi` does, in the
  same transaction and the same single restart — a person on the Wi-Fi
  renaming and tuning it would not be there for a second call. It never
  leaves or makes a network on and open, and never switches one off: when a
  radio that is on runs an open first network and the request gives it no
  key (`radios` absent, or naming it without one), the whole call is refused
  before anything is scanned or staged — `invalid_params`,
  `radios.<device>.key: the network is open — give it a key`. An open
  network that is off is tuned and stays off, and `detail` says so
  (`radio0: open network left disabled; give it a key`). A router that is
  not `tunable` is refused with `unsupported` (`radio2: a 6 GHz radio —
  Panama's rules would switch it off`), before anything is scanned or
  staged; `set_wifi` still works on it. Panama: its power limits are above
  what some countries allow; the owner chose it.
- `finish_setup`: `setup.done` is `true` from then on.

How a Wi-Fi change (`set_wifi`, `optimize_wifi`) is applied:

- One at a time: an exclusive lock is held from reading the router to the
  end of the restart's check. A change asked for meanwhile is refused with
  `busy`.
- Its steps are staged in a private uci save directory (`uci -t`),
  switches-on last, and committed together. What LuCI stages is rpcd's, per
  session, and is never taken along. uci's default save directory is: every
  `uci commit` reads `/tmp/.uci`, whatever `-t` says — so while a Wi-Fi
  change someone left there uncommitted waits (`uci changes wireless`), the
  call is refused with `busy` rather than put it live. Before the answer,
  `wifi.apply` becomes `{"state": "applying", "at": <now>, "detail": null,
  "radios": {}}`: a `setup` read right after never shows the previous
  change's result.
- The answer arrives first. About 3 s later a detached helper runs `wifi
  reload` — a browser on that Wi-Fi reconnects, with the new name and key —
  and asks netifd every 2 s, for up to 45 s, whether every radio that is on
  in the new settings with an access point on is up with that access
  point's interface; `wifi.apply` then says what it found (states above). A
  radio that was up before the change and is not now brings the previous
  `/etc/config/wireless` back, with another reload and another wait:
  `rolled_back`. Radios that were not up before either prove nothing:
  `partial` or `unverified`, never a rollback.
- A restart that cannot be started is a failure, never a success with no
  restart: `apply_failed`, with nothing committed (or, when it fails after
  the commit, the previous settings put back) and `wifi.apply` as it was.

### Claim codes (ADR-0006)

While a router is not linked, its controller keeps a random 128-bit nonce `n`
and replaces it every 20 minutes (each stays valid 10 minutes past its
replacement). Everything derives from it (`internal/claim`):

    code      = Crockford base32 of SHA-256("vectra-claim-code/v1:" || n)[:5]      (8 chars)
    codeHash  = hex SHA-256("vectra-claim-codehash/v1:" || code)
    QR        = "VECTRA:R1:" + base64url-nopad(kid(1) || ephPub(32) || gcmNonce(12) || AES-256-GCM(plain))
    key       = HKDF-SHA256(ikm = X25519(ephPriv, serverPub), salt = ephPub || serverPub,
                            info = "vectra-router-claim/v1", 32 bytes)
    AAD       = "VECTRA:R1" || kid
    plain     = ver(1)=1 | exp(4, unix s, big-endian) | n(16) | pk(32) | didLen(1) | did |
                modelLen(1) | model (<= 40 bytes) | sig(64) = ed25519(device key) over all before it

- Check-in sends `"claim": {"codeHash", "expiresAt"}` — never the code — and
  `null` once the router is linked.
- Register sends `"proof": {"timestamp", "signature"}` on every register: the
  router holds its device key, for the panel to adopt a record it created in
  advance (a pre-claimed router). `signature` is the ed25519 signature by the
  device key — the one whose public half is `inventory.devicePublicKey` and the
  QR's `pk` — standard base64, over exactly the UTF-8 bytes
  `vectra-register/v1\n<deviceIdentifier>\n<timestamp>` (`timestamp`: unix
  seconds, base 10). Absent only when the router cannot read its own key. The
  signature is never logged. Vector (seed = SHA-256 of
  `vectra-claim-vector/register`, device `vectra-07bf0887f662`, timestamp
  `1790579520`): public key `0vE9iJroHQmUD9l8bN8ru0UmOPuOOf3nfCfhWdi3sJY=`,
  signature
  `MZ3D9cCsAGO9sQef6Y/4rd5GDj31RWS+pHX2J+2BJE5FpaVuFZ1MNpYCoiHrP61R5Vsls9XWR38FeyxipBfYBw==`
  (`internal/claim`, `TestTheRegisterProofSignsExactlyTheseBytes`).
- The panel's register and check-in answers may carry `"claimKey": {"kid",
  "publicKey"}` (Vectra's X25519 key, standard base64), `"botUsername"` and
  `"owner": {"label"}` (`null`: no owner); the router keeps them.
- `"released": true` (with `"owner": null`, until the router is claimed
  again): the owner unbound the router in the Vectra app. A router that had an
  owner (`claim_owner` in its state) goes back to the state it came out of the
  box in:
  - the data plane is unloaded and xray stopped — the LAN goes out directly,
    no interception, no kill switch;
  - removed: the operator config (and with it the subscription URL), the
    provider document, the locations cache, the rendered xray config, the
    choices made on the router (location, pins, probe interval, My sites),
    the applied revision and digest (the next owner's config applies from
    scratch), the rescue state and the owner;
  - a new claim code (the one the previous owner saw is not the router's any
    more): `setup` answers `linked: false` with it;
  - kept: the router's identity and panel token, Vectra's key and bot, and
    UCI — Wi-Fi, the root password, `setup_done`, `ui_lock`.

  One log line says so and names what was removed (never the subscription).
  The rest of that answer is not acted on: its `desiredRevision` is not kept
  and its jobs are not run — they are the panel's to cancel or send again. The
  flag does nothing to a router that never had an owner (the fleet), whatever
  else the answer says, nor to one already released: once wiped there is no
  owner, so the answers that keep saying `released` until the router is
  claimed again change nothing.
- `ui/contract/claim-vector.json` is a complete test vector — fixed keys,
  nonce, expiry and device — with every intermediate value and the resulting
  QR: a second implementation (Vectra's backend) must open it to exactly these
  fields.
