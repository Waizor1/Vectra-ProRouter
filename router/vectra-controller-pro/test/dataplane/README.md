# vctl data-plane stand

A reproducible network-namespace test stand that proves the vctl data plane
**actually carries traffic**.

It exists because `xray -test` returning `Configuration OK` was mistaken for
proof of a working data plane, twice, and both times the owner's home internet
went down. `xray -test` validates config *syntax*. It says nothing about whether
a single packet can traverse the box.

## Run it

```sh
./test/dataplane/run.sh              # every scenario + the self-check
./test/dataplane/run.sh full ipv6    # just these
```

One command. It cross-compiles `vctl`, fetches a pinned upstream `xray` release
(sha256-verified against the release `.dgst`), builds an OpenWrt container image,
then runs the scenarios and prints a PASS/FAIL matrix. Exits non-zero if the
data plane is broken **or** if the stand can no longer detect the defects it was
built to catch.

Requirements: `docker` pointed at a Linux host with a real kernel (Colima or a
Linux box), plus Go on the host. `run.sh` loads `nft_tproxy`/`nft_socket`
itself. **Not Docker Desktop:** its linuxkit kernel (6.12, measured 2026-09-27)
has TPROXY but no `nft_socket`, so the shipped ruleset fails to load and every
data-plane assertion fails for a reason that is not the code. With both
installed, point only the stand at Colima: `DOCKER_CONTEXT=colima ./test/dataplane/run.sh`.

Artifacts land in `build/` and per-scenario logs in `logs/` (both gitignored).

### Scenarios

| Scenario | What it covers | Verdict |
|---|---|---|
| `full` | the v4 data plane end to end | all PASS |
| `no-routing` | defect 1: the fwmark policy route is missing | must FAIL the traffic + delivery assertions |
| `no-mark` | defect 2: xray's egress sockets carry no fwmark | must FAIL `no_egress_loop` |
| `procd` | the `/etc/init.d` start path: real `.ipk`, real postinst/prerm, real procd | all PASS |
| `procd-no-restore` | defect 3: `stop`/`prerm` no longer hand the router back | must FAIL all four restore assertions |
| `procd-no-passwall-stop` | defect 6: the hand-over never stops the OTHER proxy stack — the state the first canary was rolled back from | must FAIL `passwall_stopped_on_handover`, `passwall_stays_down` |
| `ipv6` | IPv6 carried end to end on the SHIPPED `listenIP 0.0.0.0` | all PASS |
| `ipv6-no-v6-route` | defect 4: the v6 policy route is removed, the v4 one is not | must FAIL the v6 assertions while `v4_control_tcp` still passes |
| `ipv6-dual` | the same with `listenIP "::"` | all PASS |
| `ipv6-killswitch` | the kill switch ARMED on the v6 data path | all PASS |
| `ipv6-no-ct-return` | defect 7: the output chain's `ct state established,related return` removed, so the router's own v6 replies are marked and routed to `lo` | must FAIL `v6_router_reachable` |
| `killswitch` | the kill switch ARMED: fail-closed, and what must still work | all PASS |
| `killswitch-disarmed` | control: the terminal drops removed (tcp/udp and the rest, both hook points), everything else intact | must FAIL the leak assertions |
| `killswitch-shadow` | control: the shipped fleet default (`killSwitch:false`), same stimulus | must FAIL the leak assertions |
| `killswitch-no-routing` | control: armed with no policy route — the LAN gets eaten | must FAIL the through-proxy + healthy-LAN assertions |
| `killswitch-policydrop` | defect 5: `policy drop` on prerouting, every rule left alone | must FAIL the exemption assertions |
| `apply-local` | `vctl apply-local`: a data plane built with NO panel-delivered config | all PASS |
| `ui` | the router UI end to end: opkg install registers `vectra` with rpcd, LuCI serves it over `/ubus` behind the ACL; balancer pins, location switches and the probe interval proven on packets through three real xray nodes; the operator's UI lock refused by the router itself | all PASS |
| `setup` | the setup wizard (ADR-0006) on a fresh, unlinked router: the read-only WAN, the Wi-Fi per radio (`wifi_scan`, `optimize_wifi`, `set_wifi`), `finish_setup`; the claim code, its rotation, its hash in every check-in, the owner the panel names; then the owner releasing a configured router (kill switch armed) — xray stopped, the owner's files gone, the LAN out directly, proven on the packets | all PASS |
| `passwall-real` | the REAL PassWall2 26.8.10 — the bundle the test router runs — carrying the LAN, with vctl next to it as `--standby` leaves it: `vectra on --trial`, `off`, `keep`, the deadman, "midnight" (`passwall2 restart` and the real `subscribe.lua … cron`), a WAN ifup, the legacy agent's real cron watchdog, reboots, the package reinstalled during a trial, a PassWall restart racing the takeover — who carries the LAN proven on packets at every step | all PASS |
| `passwall-real-no-switch` | control: the takeover without its hold on PassWall's own switch | must FAIL `pw_trial_switch_off`, `pw_midnight_restart`, `pw_midnight_subscribe` |
| `reports` | the reporter end to end (ADR-0007): a vctl crash (SIGQUIT), vctl's data plane without xray, a boot after no clean shutdown, and switched off — against a stub Connect (`test/dataplane/reportsink`) that verifies every report's body hash and token | `reports_vctl_panic`, `reports_dataplane`, `reports_reboot`, `reports_off`, `reports_all_verified` |
| `failover` | the failover watchdog (spec 2026-09-30, decision 5): the main balancer's node stops answering (its port dropped in the inet ns) while the LAN keeps asking; the first request to succeed goes through the other node within 10 s, the move is logged and queued as `PROXY_FAILOVER`, and the override is let go once the node answers again | all PASS |
| `failover-no-watchdog` | control: UCI `failover_watchdog '0'` — only the provider's observatory moves the balancer | must FAIL `failover_recovers_in_10s` |
| `failover-stage` | the entry as the provider served it on 2026-09-30: BL-MAIN is one node, its `fallbackTag` a loopback stage into another balancer; the node dropped, BL-MAIN is put on the stage and a request goes through the other node within 10 s | all PASS |
| `services` | a server per service (spec 2026-09-30, decision 3): through `ubus vectra services`/`set_service`, TikTok goes through a chosen country's node while the rest stays on BL-MAIN; an unknown country or service is refused; the chosen country dropped, TikTok is back on its own path within 10 s; back to the default | all PASS |
| `real-egress` | the REAL subscription through the provider's REAL nodes | all PASS |
| `real-egress-no-routing` | the same against real nodes with no policy route | must FAIL, including `real_public_ip_differs` |
| `real-egress-own` | `real-egress` with the router's OWN agent: `$STAND_SUB_URL/json` fetched by `vctl subscribe fetch -signed`, signed with the stand vctl's own device key | all PASS |

`real-egress*` needs the live subscription URL. `run.sh` reads it from
`.omc/autopilot/secrets/sub-url.txt` (gitignored) or `$STAND_SUB_URL_FILE`, and
skips those scenarios when it is not there. See **Secrets** below.
`real-egress-own` needs no `STAND_SUB_UA`: it sends the router's own agent.
`passwall-real*` need the PassWall2 bundle in `build/passwall2-26.8.10` (or
`$STAND_PASSWALL_BUNDLE`) and are skipped without it.

## What it proves

Everything runs on **OpenWrt 24.10.8 aarch64** (`openwrt/rootfs`, same branch as
the fleet's 24.10.6) with real `nft` 1.1.1, real `uci`, real `procd`, real
`opkg`, and the package's own files installed at their real paths
(`/etc/init.d/vectra-controller-pro`, `/usr/sbin/vctl-xray-wrapper`,
`/etc/config/vectra-controller-pro`, the uci-defaults script). In the data-plane
scenarios xray is started through `vctl supervise`, which is the shipped path:
`ScanAllowInsecure` → `SpliceInbounds` → `xray run -test` gate → atomic write →
supervised exec of `vctl-xray-wrapper` with its real
`GOMEMLIMIT`/`GOGC`/`XRAY_LOCATION_ASSET` contract. The nft ruleset comes from
`vctl firewall render/apply` and the policy route from `vctl firewall routing` —
not from a hand-written copy. The `procd` scenario covers the *other* start
path: a real `.ipk`, real `opkg`, real `ubusd`+`procd`, and
`/etc/init.d/vectra-controller-pro start`.

`stand/stand.sh` holds the shared helpers and the three original v4 scenarios and
dispatches on `$MODE`; each later gap lives in its own `stand/mode-*.sh`, because
each brings its own topology and its own controls. Read the header comment of a
mode file before changing it — several of them record why an assertion is shaped
the way it is, including two hypotheses the stand refuted.

### Topology

```
  [client ns]              [container root ns = "router"]          [inet ns]
  10.99.0.2 ---- veth ---- 10.99.0.1        10.44.0.1 ---- veth --- 10.44.0.2
  default via                  ip_forward=1                      lo: 44.44.44.44
  10.99.0.1                    table inet vctl (from vctl)            44.44.44.53
                               ip rule fwmark 0x1 -> table 100
```

`44.44.44.0/24` sits outside every bypass set in `DefaultSpec`, so the origin is
"the internet" as far as the ruleset is concerned.

**The FORWARD path for the client subnet is dropped by a separate guard table.**
This is the anti-false-pass control: plain routing cannot carry a client packet
to the origin, so if the origin answers, the packet went through xray. Without
it every assertion below would pass with the proxy completely dead.

### Assertions

| Assertion | What it establishes |
|---|---|
| `splice_sockopt_mark` | the splice stamped `sockopt.mark` on the inbound and every dialling outbound |
| `splice_refuses_zero_mark` | a provider that pins its own `sockopt.mark` is refused, and nothing is written |
| `tcp_through_proxy` | HTTP from the client reaches the origin |
| `tls_through_proxy` | a full TLS handshake completes through tproxy |
| `dns_through_proxy` | UDP/53 name resolution works |
| `udp_through_proxy` | a UDP echo round-trips through tproxy |
| `nft_tproxy_hits` | packets matched the TPROXY rule — **see the warning below** |
| `nft_tproxy_delivered` | those packets were *delivered to xray* |
| `no_egress_loop` | xray's own egress is not re-captured into its own TPROXY socket |
| `no_unproxied_leak` | nothing reached the origin by plain forwarding |
| `internet_tls`, `internet_dns` | the same, against the real internet (`STAND_SKIP_INTERNET=1` to skip) |

The `procd`, `ipv6*` and `real-egress*` scenarios add their own; they are
documented in their sections below and in the header comment of each
`stand/mode-*.sh`.

### `nft_tproxy_hits` is not the check you think it is

The obvious counter assertion — "the TPROXY rule counter is non-zero" — **passes
on a completely dead data plane.** Measured in the `no-routing` scenario:
43,241 packets through the TPROXY rule while not one byte was carried. The rule
still *matches* every client packet; what breaks without the policy route is the
kernel's ability to *deliver* them.

So the stand asserts delivery separately (`nft_tproxy_delivered`, via
`vctl_egress_exempt`: xray only opens an outbound socket because it accepted a
tproxied connection). A stand that asserted only "counters are non-zero" would
have shipped the broken router.

### The loop detector

`no_egress_loop` measures three counters around **one** small HTTP request:

- `vctl_output_marked` must not move — no unmarked local egress fell through to
  the marking rule and got steered into TPROXY.
- `vctl_tproxy_hits` must stay under 300 — one GET is ~6 packets. If xray's own
  egress is being re-captured, every packet re-enters PREROUTING and the counter
  runs away.
- `vctl_egress_exempt` must move — xray's sockets carry a mark the output chain
  returns on.

An earlier version asserted only the first and third conditions and **passed
during an 85,150-packet loop**, because a loop makes `egress_exempt` rise too.
Amplification, not exemption, is the signal.

## Negative controls

A stand that cannot detect the defects is worthless, so `run.sh` reintroduces
them and requires the stand to fail:

| Scenario | Defect | Must fail |
|---|---|---|
| `no-routing` | `vctl firewall routing` skipped, so the fwmark policy route is never installed | `tcp/tls/dns/udp_through_proxy`, `nft_tproxy_delivered` |
| `no-mark` | the provider pins `sockopt.mark: 0`, which the splice honours, so xray's egress sockets carry no mark | `no_egress_loop` |
| `procd-no-restore` | the call to `restore_legacy_controller` is stripped out of `stop_service`, in the payload, before the `.ipk` is built | `stop_restores_legacy`, `prerm_restores_legacy` |
| `ipv6-no-v6-route` | `ip -6 rule del fwmark 0x1` — the v4 policy route is left exactly where `vctl firewall routing` put it | `v6_plumbing_applied`, `v6_*_through_proxy`, `v6_nft_tproxy_delivered` (and `v4_control_tcp` must still PASS) |
| `procd-no-passwall-stop` | `stop_passwall_stack` stripped from `start_service`, in the payload, before the `.ipk` is built. The legacy agent's watchdog is REAL in this stand, so PassWall keeps running next to vctl | `passwall_stopped_on_handover`, `passwall_stays_down` |
| `ipv6-no-ct-return` | the `ct state established,related return` rule deleted from chain `output` by handle after apply; every other rule left where `vctl firewall apply` put it | `v6_router_reachable` |
| `killswitch-disarmed` | the kill switch's two terminal `counter … drop` rules deleted by handle after apply; every other rule left where `vctl firewall apply` put it | `ks_drops_when_xray_down`, `ks_leak_blocked_at_forward`, `ks_leak_instrument` |
| `killswitch-shadow` | no injection at all — the SHIPPED fleet default (`killSwitch:false`) run through the identical measurement | `ks_drops_when_xray_down`, `ks_leak_blocked_at_forward` |
| `killswitch-no-routing` | armed, with `vctl firewall routing` skipped, so traffic that should have been carried is eaten instead | `tcp/tls/dns/udp_through_proxy`, `nft_tproxy_delivered`, `ks_lan_not_dropped_when_healthy` |
| `killswitch-policydrop` | `policy drop` put back on the prerouting chain, every rule untouched. In a base chain `return` falls through to the policy, so all five exemptions become uncounted drops | `tcp_through_proxy`, `ks_lan_not_dropped_when_healthy`, `ks_router_itself_reachable`, `ks_bypass_forwarded` |
| `real-egress-no-routing` | the same missing policy route, against the provider's real nodes | `real_tcp/tls/dns_through_node`, `real_public_ip_differs`, `real_tproxy_delivered` |
| `passwall-real-no-switch` | the whole `if` in `stop_passwall_stack` that notes PassWall's own switch, writes the trial's snippet and turns the switch off, stripped from the payload before the `.ipk` is built. The takeover still stops PassWall; its next restart brings the whole stack back next to vctl's | `pw_trial_switch_off`, `pw_midnight_restart`, `pw_midnight_subscribe` |

`no-mark` used to reach its wire state with a provider document pinning
`"sockopt": {"mark": 0}`, on the reasoning that the splice honoured an
operator-set mark and so the fixture drove the identical code path. **The splice
no longer honours it.** `injectOutboundMark` now refuses any provider-supplied
mark that is not exactly the router's and fails closed without writing, so a
live router keeps its previous good config. That is the better outcome: the trap
is a product guard now rather than a stand fixture, and
`splice_refuses_zero_mark` asserts it — including that nothing was written.

The loop detector still needs a control, though, and the product can no longer
produce the state it detects. So the stand produces it: render through the
shipped path (`vctl supervise -dry-run`), strip the sockopt the splice added to
the **outbounds**, and exec the real wrapper with the same argv the supervisor
uses. Real wrapper, real xray, identical wire behaviour — only the config is
doctored, which is now the only route to it. The strip is self-checking: it
targets the exact object the splice emits for an outbound and cannot match the
inbound's (whose sockopt also carries `tproxy`), so if it ever stops matching,
`splice_sockopt_mark` sees two marks instead of one and fails.

Measured after the repair: one client request produced **+36,208** tproxy hits
and **+36,222** marked output packets. The control is not subtle.

Several assertions carry their control **inside** the scenario, because a
separate scenario would not have proven the same thing:

- `postinst_starts_default` arms `postinst_skip_honored`: "the daemon is down"
  is worthless unless the same install without the flag brings it up.
- `no_respawn_after_stop` arms `procd_respawn`: a process that reappears no
  matter what would satisfy the respawn check on its own. cron is stopped for
  it: the package's dead-man starts a switched-on vctl a bare stop left down
  at the next minute, on purpose, and the assertions raced that minute.
- `marker_guard_no_resurrect` arms the two restore assertions: the hand-back is
  supposed to be *guarded*, not unconditional.
- `v4_control_tcp` arms every `v6_*` assertion: same box, same ruleset, same
  second.
- `real_direct_ip_control` arms `real_public_ip_differs`: it proves the
  measurement can still see this box's own address while the ruleset is loaded.
- `cp_decoy` arms `cp_sinkholed`: a counter that counted everything would move
  for both addresses.

## The counters are part of the shipped ruleset

`internal/firewall` declares named nft counters, so this is a field
diagnostic and not just test scaffolding:

```sh
nft list counters table inet vctl
```

- `vctl_tproxy_hits` — client packets matched by the TPROXY rule
- `vctl_egress_exempt` — xray's own packets returned on their socket mark
- `vctl_output_marked` — local egress that fell through to the marking rule.
  On a healthy router this barely moves. If it climbs while xray dials out,
  xray's egress is looping back into its own TPROXY socket.
- `vctl_killswitch_drops` — client tcp/udp the kill-switch refused to let out
  unproxied. Emitted only when `killSwitch` is on (`MODE=killswitch` and its
  controls).
- `vctl_would_leak` — the same instrument with the switch off: client tcp/udp
  that left unproxied, fall-through during xray restarts included.
- `vctl_unproxied_other` — everything that is not tcp/udp (a LAN ping), at the
  same two points and with the same verdict. TPROXY never carries it, so it is
  counted apart from the leak instrument rather than read as a leak.
- `vctl_tproxy_escaped` — forwarded packets that carry the tproxy fwmark:
  captured by TPROXY, then routed out the WAN because the fwmark policy route
  is missing. No verdict of its own; the instrument below it still counts
  (and, armed, drops) them.

The stand adds a few of its own, in `table inet standguard`, and they are test
scaffolding rather than field diagnostics: `leaked`/`leaked6` (the anti-false-pass
forward guard), `cp_sinkhole`/`cp_decoy`/`cp_probe` (the `procd` isolation
proof) and `marked_dns`/`marked_tcp`/`marked_udp`/`marked_other` (the
`real-egress` breakdown of local egress).

## `procd` — the `/etc/init.d` start path

Everything above drives xray through `vctl supervise`. Nothing on a real router
does that: procd starts the daemon from `/etc/init.d/vectra-controller-pro`, and
that script does considerably more than exec a binary — it disables the legacy
`vectra-controller` agent, writes a marker so the router can be handed back,
renders the daemon config from UCI and registers a respawn policy.

It also produced the field report this mode was built around: *"the service came
up on its own after an install that set `VECTRA_SKIP_POSTINST_RESTART=1`."* A
**fresh** flagged install honours the flag — `postinst_skip_honored` passes, and
`postinst_starts_default` proves that check is armed by showing the same install
without the flag does start the service. The other path an update takes, a
re-install over a package that is already installed, enabled and running, is
covered by `postinst_skip_on_reinstall`, and it honours the flag too. So the
report is **not reproduced here**; whatever caused it is somewhere this stand
does not yet reach.

The scenario builds a **real `.ipk`** and installs it with the **real `opkg`**:

- the payload is `openwrt/files/` plus the freshly built `vctl` at
  `/usr/sbin/vctl`, and the image's baked-in copies are deleted first, so
  `pkg_installed` is a real observation;
- `postinst`/`prerm` are **extracted from `openwrt/Makefile`** by `run.sh`, not
  copied, so they cannot drift from what ships;
- the outer container is a gzipped tar rather than `ar` — busybox has no `ar`
  applet and opkg 24.10 accepts both. `control` carries no `Depends`, because
  the image already has every dependency and no feed to resolve them from.
  Everything else (including `conffiles`) mirrors the real control file.

Two things it will never do:

- **install the real legacy agent.** It would check in against production with a
  real router identity. `stand/legacy-stub/` stands in for it — and for
  PassWall: the agent's loop carries the watchdog the real agent runs (while
  PassWall's own switch, `uci passwall2.@global[0].enabled`, is on, it restarts
  a PassWall that does not run), and the PassWall stub, like the real one,
  starts nothing with that switch off (`legacy-stub/passwall2.config` turns it
  on). `MODE=passwall-real` runs the real PassWall.
- **reach the production control plane.** The default route is deleted before
  anything is installed and `api`/`router.vectra-pro.net` are sinkholed in
  `/etc/hosts` to `127.0.0.99`, which nothing listens on. Both facts are
  asserted (`cp_never_leaves_box`, `cp_sinkholed`), not assumed — and
  `cp_sinkholed` requires a *decoy* counter on a second loopback address to stay
  at zero, so a counter that matched everything could not satisfy it.

## `ipv6` — and a hypothesis the stand refuted

The v6 rules and the v6 policy route were rendered and applied and had never
carried a packet. They do now: TCP, TLS, DNS and UDP all round-trip over
`2001:db8::/32` (the documentation prefix — a ULA would sit inside `bypass6`'s
`fc00::/7` and make the whole scenario vacuous).

The mode was written expecting a **failure**. `inbounds.tproxy.listenIP` is
`0.0.0.0` in the code default and in both panel fixtures, so the reasoning was:
the ruleset captures v6, the dokodemo inbound is bound v4-only, the kernel finds
no v6 socket, and v6 either black-holes or leaks. **It carries fine.** Go's
`net.Listen` treats the v4 wildcard as a *wildcard*: `favoriteAddrFamily`
returns `AF_INET6` with `IPV6_V6ONLY=0` for a wildcard listen, so
`Listen("tcp", "0.0.0.0:12345")` is a dual-stack socket and the v6 TPROXY socket
lookup finds it. `ipv6-dual` confirms the explicit `::` spelling behaves
identically and costs nothing on v4.

Because the configurational control disappeared with the hypothesis, the control
is now surgical: `ipv6-no-v6-route` removes `ip -6 rule add fwmark 0x1` and
**nothing else** — the v4 policy route stays exactly where `vctl firewall
routing` put it. v6 then fails and `v4_control_tcp` still passes in the same
run, which is the tightest available proof that a v6 failure is the v6 path's
and not the harness's. Measured there: v6 black-holes (`v6_no_unproxied_leak`
stays at 0), it does not leak to plain forwarding.

## `real-egress` — the provider's actual nodes

Authorised by the owner. This is the only mode that leaves the building.

- The subscription is fetched **from inside the OpenWrt container** with the
  agent in `STAND_SUB_UA`, which must yield the JSON variant (~485 KB, 26
  complete xray configs) rather than the degraded base64 one. There is **no
  default**: the stand used to send `Happ/1.0`, and the provider's anti-fraud
  sweep deletes the device and disables the account on the first request with a
  "Happ…" agent that is not the real client's `Happ/<version>/<OS>/<build>`.
  Use the test account's subscription and a verbatim client agent; vctl refuses
  a malformed Happ agent before any byte leaves (`internal/uaguard`). Without
  `STAND_SUB_UA` the real-egress modes are skipped.
- The HWID is **synthetic**: `sha256(mac + "-" + model)` over a
  locally-administered MAC and a model string no device has, derived by
  `vctl subscribe hwid` — the same code path `internal/subscription/device.go`
  uses. The live router keeps its own identity and is not touched.
  `real_hwid_synthetic` proves the value differs from the production one
  *without storing the production identifier in this repo*: what is stored is
  `sha256(production HWID)`.
- A real entry is spliced into tproxy by the shipped `SpliceInbounds` and dialled
  out through real REALITY / xhttp / hysteria2 nodes. If an entry carries no
  traffic the mode walks to the next one — a dead node is a provider fact — but
  no assertion softens.
- The **only** byte the mode adds to the provider's document is a top-level
  `"api"` object on `127.0.0.1:10085` (Xray's `api.listen`, which needs no
  inbound), prepended so every other byte stays where the provider put it.
  `real_provider_verbatim` strips the prefix back off and compares sha256 with
  the fetched entry, so "verbatim" is checked rather than claimed. It is there
  because `vctl api observatory` returns `ErrNotImplemented` (native gRPC
  ObservatoryService is a v0.2 item) and the provider document ships no `api`
  block, so there is otherwise no way to read a verdict out of the engine.

### `real_public_ip_differs` is the assertion that matters

Everything else in the mode would also pass on a router whose client traffic
bypassed xray entirely and reached the internet over the WAN. So the same
endpoint (`cp.cloudflare.com/cdn-cgi/trace`, address pinned with `--resolve` so
DNS cannot vary it) is measured from three vantage points:

| Measurement | Path |
|---|---|
| `BASE_PUBIP` | router ns, **before any ruleset exists** |
| `CLIENT_PUBIP` | client ns — can only leave via tproxy → xray |
| `DIRECT_PUBIP` | router ns with the ruleset up, endpoint temporarily added to the shipped `vctl_direct4` nftset so the output chain returns on it |

`real_public_ip_differs` requires `CLIENT != DIRECT`. `real_direct_ip_control`
requires `DIRECT == BASE`, and that is what arms the first one: it proves the
measurement can still see this box's own address while the ruleset is loaded, so
the client's differing is the proxy's doing and not an artefact of the ruleset
breaking the measurement. Addresses are printed masked (`80.90.x.x`) plus a
short digest, so a reader can see two values differ without the owner's home
address landing in a log.

### `nft_tproxy_delivered` is not the check you think it is either

The v4 stand asserts delivery as an absolute — `vctl_egress_exempt > 0` — on the
premise that *"xray only ever opens an outbound socket because it accepted a
tproxied connection."* **That premise is false for a real provider.**
`burstObservatory` dials every one of its sixteen `subjectSelector` outbounds at
start, whether or not a single client packet was ever delivered, so the absolute
counter is non-zero on a completely dead data plane. Exactly the same trap as
`nft_tproxy_hits`, one level further in.

The `real-egress-no-routing` control is what caught it: with the policy route
gone and nothing carried, `nft_tproxy_delivered` reported PASS. So this mode
asserts `real_tproxy_delivered` instead — a **delta** measured around one client
request against an idle baseline, where the idle windows are the control,
because they are what the observatory alone produces.

### The loop detector, recalibrated honestly

Same story. Against the fixture, `vctl_output_marked` must move by **exactly
zero**. Against the real provider it moves a little, and that is not a defect:
the provider's `dns` block resolves through 1.1.1.1/77.88.8.8 and
burstObservatory's `pingConfig` has a `connectivity` URL that Xray dials
directly. That is unmarked local egress, which is exactly what the output
chain's marking rule is for — on a PassWall2-style router the box's own traffic
is supposed to be proxied. (Measured: the trickle is entirely TCP; the
`marked_dns` and `marked_udp` counters stay at zero.)

Raising the threshold to "must be small" would be the weakening this stand
exists to prevent, so instead the background rate is **measured**: two idle
windows of the same length, and the request window has to stay within the larger
of them plus five packets. The signature of the real defect is not a trickle —
the measured loop was 85,150 packets for a handful of requests, four orders of
magnitude above that tolerance. The mode also breaks the marked egress down by
`dns` / `tcp` / `udp` / `other` in its own nft counters, so the trickle is named
rather than tolerated.

### `real-egress-own` — the router's own agent

`stand/mode-real-egress-own.sh`, on top of `real-egress`: the same topology,
the same real nodes and every assertion, with one change — the subscription is
fetched the way a router whose config names no agent fetches it: `vctl
subscribe fetch -signed -state <state.json>` against `$STAND_SUB_URL/json`,
which Remnawave serves as xray-JSON whatever the agent. No `STAND_SUB_UA`.

- `own_state_first_start` — the stand vctl's own `state.json`, with a device id
  and key, is made by its first start (`vctl agent -once` against a panel that
  is a dead port, api/router.vectra-pro.net sinkholed besides).
- `own_ua_on_wire` — the agent vctl sends, read off the IDENTICAL fetch (the
  same binary, flags, state and HWID) made against a listener on the stand's
  own address, because what goes to the provider is inside TLS: it starts with
  `VectraRouter/` and carries ` vr1.`; `x-device-os` is `OpenWrt` and `x-hwid`
  the synthetic one. The log shows the prefix and the token's length, never the
  token. Until it passes the mode stops there: nothing is fetched from the
  provider.

  **Not proven yet.** The router signs only an `https://` subscription
  (`uatoken.RequestTarget` refuses anything else before a byte is sent), so the
  listener has to terminate TLS, with the stand's certificate for 44.44.44.44.
  Neither `openssl s_server` (it recorded nothing) nor the stand's xray as a
  `dokodemo-door` with TLS in front of a recorder (it passed the TLS bytes
  through: "server gave HTTP response to HTTPS client") has done that yet; the
  image's socat has no OpenSSL. Every run so far stopped at `own_ua_on_wire`,
  before the provider.
- then `real_subscription_fetched` (the JSON variant), the splice and every
  `real-egress` assertion, `real_public_ip_differs` included.

Safety, as verified in the backend before this mode was written: the
provider's anti-fraud never deletes a device whose platform header says
OpenWrt — vctl sends `x-device-os: OpenWrt` — the HWID is synthetic, and the
subscription is the test account's (see **Secrets**).

## `ui` — the router UI, on packets

`stand/mode-ui.sh`. The package is installed with the real `opkg` into a
container that runs procd, ubusd, rpcd and uhttpd from their own init scripts,
with luci-base. In the inet namespace: an xray acting as three provider nodes
(vless on `10.44.0.2:2001/2002/2004`, `:2003` closed = a dead node) whose
access log names the inbound each client connection arrived on, and a
subscription server (`openssl s_server -HTTP`, the stand's cert trusted) whose
connections from the router are counted in nft. The panel is a stub that
accepts check-ins, because the firewall's commit-confirm deadman reverts the
ruleset ~90 s later without one.

What it asserts, among others:

- `ui_rpcd_registered` — no `vectra` object before the install, one after:
  the postinst's rpcd reload works; `ui_removed` — gone again after `opkg remove`.
- `ui_probe_default` — the provider's `43200s` is `600s` in the RUNNING config.
- `ui_pin_live`, `ui_pin_steers` — a pin changes xray's own override AND the
  node the next client connection arrives on, both ways.
- `ui_pin_survives_restart` — xray restarted (new pid: a fresh xray has no
  overrides), the pin is back and still steers.
- `ui_select_entry` — a location switch re-renders from the cache with ZERO new
  connections to the subscription server, and traffic moves to the new
  location's node.
- `ui_http_acl` — the browser's path: `/ubus` refuses an anonymous session and
  serves a logged-in one; `ui_luci_page` — the Vectra page renders, and `/admin` (LuCI's landing page) lands on it.
- `ui_operator_lock` — `uci set …ui_lock=1; uci commit`, no restart: `status`
  says `ui.locked`, the router itself answers `nodes`, `balancers`, `logs`,
  `pin_balancer` (a real balancer and node — xray's override does not move)
  and `set_probe_interval` with code `locked`, while `entries`,
  `diagnostics`, `rules` and `set_rules` are still served; `ui_lock=0` serves
  `nodes` again. The lock is lifted before anything is judged, so no later
  assertion runs locked.
- My sites, on the packets. Two names for the one origin address
  (`curl --resolve`, the name in Host and SNI): the stand's provider sends
  `direct.stand` DIRECT and `origin.stand` to BL-MAIN.
  `ui_rules_direct` — `origin.stand` goes through a node while another site
  is set; set direct (pasted as `HTTPS://WWW.Origin.Stand:443/…`, kept as
  `origin.stand`), HTTP and HTTPS are answered with no node in the nodes'
  access log and xray's own DIRECT counter moving; the render sniffs with
  `routeOnly` while sites are set. `ui_rules_proxy` — `direct.stand` goes the
  provider's DIRECT until set proxy, then through BL-MAIN's node, HTTP and
  HTTPS. `ui_rules_refuse` — garbage, a site in both lists, a missing list and
  a bad CIDR answer `invalid_params` naming the entry; xray's pid, the render,
  the overrides and `rules` do not move. `ui_rules_survive_restart` — a fresh
  xray runs the same sites. `ui_rules_cleared` — emptied: no owner's rule
  left in the render, the operator's sniffing back, for every later
  assertion.

`MODE=ui-hold` runs the same and then keeps the container up with LuCI
published on `127.0.0.1:${STAND_UI_PORT:-8080}` (root, empty password) — the
real UI against a real data plane, for a browser. `MODE=setup-hold` does the
same for a router straight out of the box — the setup wizard; see below.

## `setup` — the setup wizard and the claim

`stand/mode-setup.sh`. A router a customer has just unboxed: the package is
installed with the real `opkg`, procd, ubusd and rpcd run from their own init
scripts, and the daemon runs WITHOUT an operator config — not linked, so it
offers a claim. The panel is a stub (`stand/setup/panel-claim.sh`) that
accepts it and answers, as the real panel may, with Vectra's claim key (the
one of `ui/contract/claim-vector.json`) and a bot; once
`/tmp/setup-panel-owner` exists it names an owner too, and once
`/tmp/setup-panel-released` exists it says `"released": true, "owner": null`
— the owner unbound the router in the Vectra app. It logs every request WITH
its body, so what a check-in carries is read, not assumed. The claim code
rotates every 20 s here (`uci claim_rotate_sec`; the router's default is 10
minutes). The ui stand's three nodes and subscription server run in the inet
namespace, for the router once it is linked.

The router sets its internet connection up itself; the wizard only shows it
and checks it. There is no netifd and no radio in the container: the WAN is a
UCI section (`dhcp` on the stand's uplink, `vinet`) and the Wi-Fi a dummy
wireless config — two disabled radios with open access points, 2.4 GHz on
auto at 17 dBm under the US's rules, 5 GHz on 36 at HE80 — so the wizard's
changes are proven where they land: UCI.

- `setup_shape` — not done (the package's uci-defaults did not mark a router
  that does not work yet), not linked, no owner, an 8-character Crockford
  code, a `VECTRA:R1:` QR, the bot link.
- `setup_wan_readonly` — `wan` is `dhcp` with the cable up (vinet's carrier),
  `wifi.suggested` is `Vectra-` + the last 4 hex digits of vinet's MAC,
  `wan_check` reaches the panel over the router's own marked socket, and the
  router offers no `set_wan`.
- `setup_checkin_codehash` — the newest check-in carries
  `sha256("vectra-claim-codehash/v1:" + code)` of the code on screen, and the
  code itself never reaches the panel.
- `setup_claim_rotates` — the code changes on schedule and the check-ins move
  to the new hash.
- `setup_under_the_lock` — `ui_lock=1` refuses `nodes` but not `setup`.
- `setup_claimed` — the panel names an owner: `claim.state` is `claimed`,
  `claim.owner` and `vectra.owner` show it, `state.json` keeps it.
- `setup_wifi_radios` — `setup.wifi` has one entry per radio with its band,
  channel (`null` and `auto` for 2.4 GHz), htmode and its `width`, country,
  txpower and `maxPower`, the network's name, `secured`, `enabled`, `ap`,
  `mesh`, and `up` `null` (no netifd to ask) — never the key; `tunable`,
  `tuned` (nothing is on), no restart yet (`apply` `null`).
- `setup_wifi_scan_empty` — `wifi_scan` never scans: before any
  `optimize_wifi` it answers `{radios: [], scannedAt: null}` at once.
- `setup_wifi_unsupported` — with a 6 GHz radio added, `tunable` is false and
  `tuned` null, and `optimize_wifi` answers `unsupported` naming it, the
  wireless file unchanged.
- `setup_wifi_optimize_open` — `optimize_wifi` with channels 11 / 149:
  country `PA` on both radios, no txpower, the channels fixed, htmode
  untouched; both networks are open and were given no key, so neither is
  switched on, and `detail` says so. DFS 100, and a name without a key for an
  open network, are refused and change nothing. Right after the answer
  `setup.wifi.apply.state` is `applying` and another change is `busy`; then
  the detached helper's verdict — `unverified`, as there is no netifd to ask
  — with nothing rolled back, no private save directory or job left, nothing
  staged in uci.
- `setup_wifi_scan_cached` — `wifi_scan` answers the scan that call made:
  both radios `off` (off in UCI, and no netifd), 2.4 GHz on auto
  recommended 6, 5 GHz keeping its 36 (valid at 80 MHz), `scannedAt` set.
- `setup_wifi_optimize_named` — the same call with names and keys for both
  radios: one transaction, both radios on with psk2, `setup` says `tuned`;
  the restart `unverified`, nothing rolled back.
- `setup_wifi_set` — a Wi-Fi change left uncommitted in uci's own save
  directory (`uci set` without a commit) makes `set_wifi` answer `busy`;
  once it is reverted, `set_wifi` renames 5 GHz alone: its key stays
  (secured, none given), it stays on, 2.4 GHz is untouched.
- `setup_finish` — `setup_finished`; `setup.done` is true, UCI `setup_done=1`.

Then the owner's router in service. The stub cannot deliver an operator
config, so `stand/setup/link.sh` does what the panel's config delivery would:
the operator config with the kill switch ARMED, `vctl apply-local` (the
applier a panel job runs) and a daemon restart — xray up, the data plane
loaded behind commit-confirm. On it: a location chosen, `BL-MAIN` pinned to
`node-b`, My sites set. A stand table counts the LAN client's packets the
router FORWARDS to the origin: a proxied connection never reaches the forward
hook (TPROXY delivers it to xray), so a moving count is traffic that went out
directly.

- `setup_in_service` — linked, the owner shown, no claim; the client's
  request arrives on `node-b`, 0 packets forwarded; location, pin and sites
  in the overrides.
- `setup_released` — after `"released": true`: `setup` says `linked: false`
  with a new code and no owner; xray stopped (its pid gone, no process on the
  render); the `inet vctl` table and the fwmark rule gone; the operator
  config, provider config, locations cache and index, overrides and render
  gone; no owner, applied revision, digest, splice key or desired revision in
  `state.json`; Wi-Fi, root's password (set through `passwd`, as LuCI's
  banner would), `setup_done` and `ui_lock` (set to 1 for the release) as
  they were.
- `setup_released_direct` — the LAN client gets the origin's answer, the
  forward count moves, and no node carried it: no interception, and the armed
  kill switch left no black hole.
- `setup_released_once` — the stub keeps saying `released`; a choice made on
  the router afterwards survives two more answers: once wiped there is no
  owner, and the flag does nothing.

`MODE=setup-hold` stops at the unboxed router — the package installed, no
operator config, the stub answering with the claim key and the bot, NO owner
— with the dummy wireless config in place and the real 10-minute code, and
keeps the container up with LuCI published on
`127.0.0.1:${STAND_UI_PORT:-8080}` (root, empty password: a fresh box). The
wizard in a browser runs against the real vctl: the real code and QR, the
Wi-Fi step writing to UCI (nothing to scan — there is no radio — and each restart
ends `unverified`: there is no netifd to ask).
The rest of the story, by hand:

    docker exec vctl-setup-hold sh -c 'echo "Stand Owner" > /tmp/setup-panel-owner'
    docker exec vctl-setup-hold sh /stand/setup/link.sh
    docker exec vctl-setup-hold touch /tmp/setup-panel-released

The first makes the stub name an owner at the next check-in (5 s): the
wizard shows "account confirmed, getting the settings" and stays there — the
stub cannot deliver the settings. The second stands in for the panel's config
(see above): the router is linked, and the wizard says "connected". The
third is the owner unbinding it in the Vectra app: back to the claim step
with a new code (`rm` both files to start over). Stop it with
`docker rm -f vctl-setup-hold`.

## `passwall-real` — the real PassWall2 next to vctl

`stand/mode-passwall-real.sh`. Every other mode stands a sleep loop in for
PassWall. This one installs the real thing — `luci-app-passwall2` 26.8.10 with
its own `xray-core` 26.7.28, `chinadns-ng`, `geoview` and geo data, the bundle
in `build/passwall2-26.8.10` (aarch64_cortex-a53: the build the owner's test
router runs) — lets it carry the LAN, puts vctl next to it the way the
installer's `--standby` leaves it, and walks `vectra on --trial`, `off`,
`keep`, the deadman, "midnight", a WAN ifup, the legacy agent's cron watchdog
and reboots.

    DOCKER_CONTEXT=colima ./test/dataplane/run.sh passwall-real passwall-real-no-switch

It needs the bundle (or `STAND_PASSWALL_BUNDLE`; without it both modes are
skipped) and OpenWrt's feeds once, for PassWall's LuCI and lua dependencies
and `dnsmasq-full`: the container reaches downloads.openwrt.org at the start,
with api/router.vectra-pro.net already sinkholed; then the default route goes
and nothing leaves again (`pw_isolated` counts what goes out of eth0). run.sh
mounts the bundle and the legacy agent's real watchdog
(`openwrt/testdata/vectra-controller-watchdog.main`) into the container; the
image does not change. About 20 minutes for `passwall-real`, 10 for the control.

**Who carries the LAN is read off the packets.** The inet namespace runs one
xray as four nodes and logs the inbound each connection arrived on: vctl's
provider dials node-a/b/c (`:2001/:2002/:2004`), PassWall's one node is
`:2005` (`pw-in`), which vctl does not know. Every probe is a TCP request, a
UDP echo and a DNS query through the router's dnsmasq for a name no cache has
seen — REDIRECT, TPROXY and PassWall's DNS hijack are different mechanisms,
and a leftover of one would take its own protocol only — and each lands in
that log under the stack that carried it. The DNS query's upstream is the
router's own traffic: PassWall hijacks it into its own DNS path; vctl carried
it through its xray until 0.6.0-r2, which lets the router's own DNS and NTP go
out directly. The origin's resolver logs who asked, so the probe tells
`via[direct]` (the router's WAN address) from a node, and for vctl either
counts. A LAN client's own DNS, to a resolver of its choosing, must still go
through vctl's xray (`pw_trial_client_dns`).

**The world around it.** vctl's panel is the ui stand's stub, both
subscriptions are served in the inet namespace (PassWall's from 10.44.0.2, an
address in vctl's bypass, so its nightly fetch works under vctl too — see
below), and the legacy agent is the stand's stub installed as
`/usr/sbin/vectra-controller-agent`, so the agent's real watchdog, on its real
`*/5` cron line, finds it by the path it looks for. PassWall's subscription is
on the nightly cron with `hwid`, as on the fleet; its start delay is 1 s
rather than 60 so a reboot does not wait a minute.

**A reboot**, as far as a container can have one: every process in the
router's network namespace is SIGKILLed — no stop runs; the kernel forgets the
router's nft tables, policy rules and their tables; tmpfs comes back empty;
then procd and ubusd, `config_generate`, `/etc/uci-defaults/*` sourced in a
subshell and deleted on exit 0 exactly as `/etc/init.d/boot` does, and
`/etc/rc.d/S*` in order with `boot` — except the scripts that drive what the
container does not have (`PW_BOOT_SKIP`): the host's clock and kernel
parameters, flash and LEDs, and netifd with fw4, whose zones would have no
devices without it. PassWall's rules live in their own table and do not need
fw4; the part of fw4's reload that is PassWall's, its include, is run by hand.
(`config_generate` matters: the image has no `/etc/config/system`, and without
it no logd starts and `/dev/log` never exists. procd starts the router's
dnsmasq in ujail with `-l`, which mounts `/dev/log` into the jail — `jail:
stat(/dev/log) failed`, exit 1 — so nothing listens on the LAN's port 53.
PassWall hides that: it answers the LAN from its own dnsmasq copy, unjailed, on
:11400, so the LAN's DNS breaks only once something else carries the router.
An early run of this mode read that as a takeover defect; it was the stand.)

**The controls come first**, in the baseline, so every "stays down" below
means something: the agent's watchdog restarts an agent that was merely
stopped (`pw_ctl_watchdog_armed`); `passwall2 restart` and the real
`subscribe.lua start <id> cron` — fetching a changed subscription and ending,
as it does, with `/etc/init.d/passwall2 restart &` — each bring a new stack
with the switch on (`pw_ctl_restart_armed`, `pw_ctl_subscribe_armed`); the WAN
ifup hotplug restarts PassWall once its gate is armed (`pw_ctl_hotplug_armed`,
below); PassWall carries the router's own TCP (`pw_baseline_router_tcp`).

| step | what | assertions |
|---|---|---|
| 1 | baseline: PassWall2 carries the LAN, and the controls | `pw_baseline_*`, `pw_ctl_*` |
| 2 | `vectra on --trial --minutes 10 --foreground` | `pw_trial_*`: vctl carries the LAN; no `/tmp/etc/passwall2/bin` process; no PSW2 chain or rule, no `0x50535732` rule, table 999 empty; switch 0; `S99passwall2` kept; the agent stopped, its link kept; the agent's watchdog held (its stamp ahead of the clock, by 10 minutes at most: 0.6.0 sets it again every minute); breadcrumbs only in `/tmp/vectra-trial.d`; the snippet; no vctl boot link; the router's own TCP through vctl (`pw_trial_router_tcp`); a LAN client's own DNS through vctl's xray (`pw_trial_client_dns`) |
| 3 | midnight: `passwall2 restart`, then the real `subscribe.lua … cron` | `pw_midnight_restart`, `pw_midnight_subscribe`: no PassWall proxy, the LAN through vctl, LAN DNS answers |
| 4 | the WAN's ifup, as shipped and with its gate armed, and PassWall's firewall include | `pw_ifup_stays_down` |
| 5 | the agent's watchdog, by hand | `pw_watchdog_holds` |
| 6 | a reboot | `pw_reboot_*`: PassWall2 carries, switch 1, the snippet ran and is gone, the agent runs, vctl neither runs nor starts, no breadcrumb anywhere |
| 7 | a new trial, then `vectra off` twice | `pw_trial_after_reboot`, `pw_off_gives_back` (a new PassWall xray), `pw_off_idempotent` |
| 8 | `vectra on --trial --minutes 1`, in the background | `pw_deadman_trial_carries`, `pw_deadman_ends_trial` (nobody runs `off`) |
| 9 | `vectra keep`, a reboot, midnight, `vectra off` | `pw_keep`, `pw_keep_reboot_vctl`, `pw_keep_midnight`, `pw_keep_off` |
| 10 | a crash, during a trial: vctl SIGKILLed with its data plane loaded, then vctl made unstartable (`chmod -x`) and killed again; 0.6.0's cron dead-man (`deadman.sh`) run by hand, one "minute" at a time, each under a 60 s timeout | `pw_crash_unloaded` (its first minute unloads the data plane; vctl back and the LAN carried), `pw_crash_one_xray` (the killed vctl's xray gone, one xray: the new vctl's), `pw_crash_handback` (PassWall2 carries again, and who gave it back: the init script's guard for a binary that cannot run, or the dead-man after 10 minutes), `pw_crash_deadman_after` (its next minute runs to its end) |
| 11 | a revert, during a trial: `nft delete table inet vctl` under a running vctl; then again with the panel unreachable (its packets dropped) for longer than the commit-confirm's 90 s, and the panel back | `pw_revert_table_back` (the next poll loads it again, 0.6.0-r2's ensureDataPlane), `pw_revert_panel_down` (reloaded, reverted by its commit-confirm, reloaded for good once the panel answers) |
| 12 | the same ipk again (`--force-reinstall`) and one version up, during a trial | `pw_upgrade_same`, `pw_upgrade_newer` (no boot link, every step-2 fact, the deadman alive, vctl carries), `pw_upgrade_then_off` |
| 13 | a PassWall restart in flight when the takeover starts (0, 0.5, 1.5, 3 s apart) | `pw_race_restart` |
| 14 | the documents on `/etc`, nothing ever applied (no digest in state.json), a reboot, a trial | `pw_unapplied_no_dataplane`: no render, no xray, no data plane — intended; before 0.6.0 vctl stayed up so, from 0.6.0 the trial sees it carries nothing and gives the router back by itself (about a minute later, the LAN unproxied meanwhile) |
| — | every hand-back | `pw_handback_one_xray` (never vctl's xray and PassWall's at once), `pw_handback_frees_init` (nothing left holding vctl's init lock or the power lock); after the first, `pw_deadman_not_blocked` (two cron minutes on, no dead-man run waits on a held lock) |

The hand-backs are watched from the inet namespace, where nothing they kill
can stop the sampler: which xray runs, vctl's (argv[0] `vctl-xray-wrapper`) or
PassWall's, with their RSS (`MEASURE overlap …` in the log). A second sampler
keeps the peak RSS of the router's proxy processes per phase (`MEM …`).

Two product defects had stand-side ways past them, each announced as
`KNOWN DEFECT` where it bites, so that on a build that still has it the steps
after it test their own subject: H2, a data plane that did not come back after
a reboot (fixed in 9bb567c; `pw_trial_after_reboot`, `pw_keep_reboot_vctl`),
got past with `vctl apply-local` and a restart; vctl's init lock held after a
hand-back (fixed in 0.6.0-r5; `pw_handback_frees_init`), with a PassWall
restart from a shell that holds nothing of vctl's.

For a look by hand, run the image yourself (`docker run -d --privileged -e
MODE=passwall-real -e STAND_PW_HOLD=1` and run.sh's two passwall mounts): the
mode stops after its setup — the world up, PassWall carrying, vctl in standby —
prints `STAND-HOLD ready` and waits for `docker exec`.

`MODE=passwall-real-no-switch` builds the package with the takeover's hold on
PassWall's own switch stripped out of `stop_passwall_stack` (the whole `if`
that notes it, writes the trial's snippet and turns it off), in the payload,
before the `.ipk` is built. The takeover still stops PassWall — and "midnight"
brings its whole stack back next to vctl's. The control stops after step 3.

### What the stand found

In the real PassWall2 26.8.10, worth knowing before touching a router that
runs it:

- **Its WAN ifup hotplug is dead code.** `/etc/hotplug.d/iface/98-passwall2`
  restarts PassWall only when its cache holds `ENABLED_DEFAULT_ACL=1` and
  `/tmp/lock/passwall2_ready.lock` exists; 26.8.10's `app.sh` never writes that
  variable to its cache, so as shipped an ifup restarts nothing, PassWall in
  charge or not. The stand arms the gate by hand, as a build that cached it
  would have, to prove the restart it fires starts no proxy with the switch
  off.
- **Its stop SIGKILLs every process whose command line contains
  `passwall2/`** (`pgrep -af "passwall2/"` in `app.sh stop`), bar its own
  scripts — a shell or a `grep` that names its paths included. The stand's
  probes bracket the pattern (`passwall[2]/`) for that reason.
- **Its stop leaves `table inet passwall2`** with four empty base chains (the
  table's flush is commented out upstream). The PSW2 chains and rules, its
  `0x50535732` policy rule and table 999 do go.
- **Its init is rc.common, not procd's**: `start` runs `app.sh` in the calling
  shell, and every daemon it leaves (xray, `dnsmasq_default`, `monitor.sh`,
  `lease2hosts.sh`) inherits that shell's open descriptors.
- **`subscribe.lua` restarts it only when the subscription changed** (a new
  md5 and at least one node), and fetches through the router's own egress; its
  cron line is written by PassWall's start and removed by its stop.

In vctl — found here, fixed in the product by others; the release named is
the one the stand saw it go in:

- **The router's own TCP did not go through vctl** (until 0.6.0-r4;
  `pw_trial_router_tcp`). Only the SYN of a connection the router opens was
  marked and TPROXY'd into xray: the output chain returned on `ct state
  established,related` before its marking rule, so the ACK and the data left
  by the WAN for the real destination, which answered RST. Counted per packet
  here: SYN via lo 2, SYN-ACK from xray 2, ACK/data out the WAN 2, RST from the
  origin 2, `curl: (7) … after 0 ms`. The daemon's own unmarked health probes
  go that way. 0.6.0-r4 returns `ct direction reply` first (replies to what
  came in), lets the rest of a connection whose first packet it marked follow
  it by conntrack mark, and marks new ones with `ct mark set` too;
  `ipv6-no-ct-return` now removes both returns.
- **After a hand-back vctl's own init script stayed locked** (until
  0.6.0-r5; `pw_handback_frees_init`, the `status` probe after every
  hand-back). Every invocation of a procd init script takes
  `/var/lock/procd_<name>.lock` on fd 1000 (procd.sh's `_procd_wrapper` calls
  `procd_lock` when rc.common sources it). The hand-back starts PassWall and
  the agent from inside vctl's init, and their daemons inherited fd 1000.
  Unlocking it first (`flock -u 1000`, 0.6.0-r3) did not hold: a procd init
  script started under them — PassWall's start restarts dnsmasq and cron, the
  agent has its own — inherits the same descriptor, and `procd_lock` begins
  with `flock -n 1000` on it, locking that open file description again for as
  long as PassWall's daemons keep it open (`build/debug/relock.sh` shows it in
  ten lines). Every later `/etc/init.d/vectra-controller-pro` invocation then
  waited: `status`, `vectra on`, `vectra keep`, and `vectra off` on a router
  switched on for good, whose `disable` the power package killed after 3
  minutes, leaving the boot link. 0.6.0-r5 closes the descriptor.
- **A vctl killed with SIGKILL left its xray running** (until 0.6.0-r5;
  `pw_crash_one_xray`): xray is its own process group. The restarted vctl's
  xray failed `listen … address in use` (exit 255, again and again), and the
  LAN was carried by the orphan, with the old config, that nobody supervised.
- **Each hand-back waited 15 s with no proxy at all** (until 0.6.0-r3):
  `vctl_alive` matched vctl's own commit-confirm sleeper (`sh -c 'sleep 90; …
  /var/run/vectra-controller-pro/fw-confirm …'`). Now 1.5–2.5 s from vctl's
  xray gone to PassWall's up (`MEASURE overlap`), and never both at once
  (until 0.6.0 every hand-back ran both).
- **The cron dead-man hung on that lock every minute** (until 0.6.0-r3:
  `pw_deadman_not_blocked`), and a ruleset reverted by the commit-confirm
  stayed reverted (until 0.6.0-r2: `pw_revert_*`).
- Still so in 0.6.0-r5, and small: a controller binary that cannot run is
  handed back at once by the init script's own guard ("controller binary
  missing … handing the router back"), in the dead-man's first minute rather
  than after its 10 — PassWall carries within seconds (`pw_crash_handback`) —
  and the trial's file stays behind, so the trial's own deadman hands back a
  second time at its end. With the panel unreachable, every commit-confirm
  revert is followed by a reload at the next poll (`pw_revert_panel_down`):
  the LAN stays carried, and the commit-confirm no longer takes down for good
  a ruleset that is itself what cuts the panel off.

### PassWall's routing, and what the kernel carries straight out

The last step runs vctl with `route_source 'passwall'` on a seeded shunt: a
direct rule whose `ip_list` is `44.44.44.46` (first, as the fleet's `direct`
rule is), a proxied `origin.stand` with FakeDNS, the rest direct.

- `pw_routing_*`: the render is PassWall's (FakeDNS, the proxied rule,
  vctl's marks), the listed domain rides PassWall's node, the rest goes out.
- `pw_direct_by_kernel`: the direct rule's address is in `vctl_direct4` and
  a LAN connection to it is **forwarded by the kernel** — its endpoint sees
  the client's own address, `vctl_direct_new` counts it. Through xray the
  endpoint would see the router's (`pw_direct_rest_via_xray` is that control,
  for an address on no list).
- `pw_direct_sticky_kernel` / `pw_direct_sticky_xray`: a 6-second
  connection keeps its carrier through the direct set being flushed under it
  (what a reprogram does) and through its address entering the set (what a
  reload does). Tested against the set per packet, both would change hands
  mid-connection and be reset — `pw_direct_sticky_control` puts exactly that
  rule in front for a moment and requires the connection to die.
- `pw_lan_port_forward_tcp` / `_udp`: a DNAT port forward from the WAN to a
  server on the LAN (the WAN client a public 44.44.44.47, which the server
  sees: no masquerade) works while vctl carries. Its answers are a
  connection's reply direction; before 0.6.0-r17 TPROXY took them — the
  SYN-ACK reached xray's listener, which reset it — and
  `pw_lan_port_forward_control` removes the `ct direction reply return` for
  a moment and requires both forwards to break. The kill-switch mode has the
  same forward, armed, with xray up and SIGKILLed
  (`ks_port_forward_healthy`, `ks_port_forward_xray_down`).
- `pw_lan_dns_hijacked`: the client asks 44.44.44.53 — a public resolver —
  for `origin.stand` and gets the router's answer (a FakeDNS address), with
  `vctl_dns_hijacked` moving; `pw_lan_dns_hijack_control` takes the hijack
  out and requires 44.44.44.53's own answer.

### Native routing: no PassWall2 on the router

Then (`step14_native_routing`, last: nothing after it has a PassWall) vctl
takes the same policy with `route_source 'native'`, a slot bound to a node of
a subscription the stand serves (`https://44.44.44.44:8443/native/sub`).

- `pw_native_imported`: `/etc/config/vectra_route` is PassWall's
  configuration byte for byte, 0600; its geo files copied.
- `pw_native_carries`: vctl's own generator — FakeDNS for the listed domain,
  the slot's node (c-in) carrying it, PassWall's stack down.
- `pw_native_compare`: `vctl passwall-render -compare` says "identical" —
  the two generators on the stand's live policy.
- `pw_native_refresh_follows`: the feed moves the exit to b-in; at the
  policy's schedule (its last refresh set a day back) vctl asks, with the
  router's own agent, and the slot follows the node by name.
- `pw_native_without_passwall`: `opkg remove` of luci-app-passwall2 and its
  packages; vctl restarted carries by the policy.
- `pw_native_after_reboot`: after a power cycle too.
- `pw_native_refuses_placeholder`: the feed answers the provider's "App not
  supported" placeholder; the refresh is refused, the store unchanged.

## `dns-tunnel.sh` — the LAN resolves through the tunnel

Not a `run.sh` mode yet: a script in the stand image, run against the image
`run.sh` built (it needs the live subscription, passed by name):

```bash
docker run --rm --privileged -e STAND_SUB_URL --entrypoint sh <image> /stand/dns-tunnel.sh
```

The ISP is a dnsmasq on 5.5.5.5 that forges NXDOMAIN for www.instagram.com and
www.youtube.com, as the test router's ISP does for queries to 8.8.8.8 and
1.1.1.1 as much as to its own resolver. The router's dnsmasq runs as uid 453
and asks that ISP. The script fetches the real subscription with the router's
own signed agent, renders it with `vctl apply-local` and runs the real daemon
against a dead panel. Every one of the following must pass:

- without vctl, www.instagram.com does not resolve and example.com does (the
  forgery is real);
- the render has `vctl-dns-in` on 127.0.0.1:10053, and the table redirects
  uid 453's port-53 queries there;
- www.instagram.com and www.youtube.com resolve to real addresses, and not
  one query for them reaches the ISP;
- `vctl_dns_redirected` counts, vectra.lan still resolves locally, there is
  no AAAA answer (the provider's UseIPv4), and a TXT query is answered at
  once;
- https://www.instagram.com/ answers by its name, with no resolver loop
  (dnsmasq's forwarding table never fills);
- xray's DNS inbound unreachable takes the redirect out within two loops —
  the data plane stays, the LAN resolves over the open path — and it is back
  when the inbound answers; after those reprograms each rule is there once;
- the tunnel dead (xray's own egress dropped) with the line up takes the
  router direct by itself: no table, no redirect, the internet straight out;
- the daemon's stop takes the table, and the ISP's forgery is back;
- two `vctl firewall apply` in a row leave the tproxy rule there once.

## Secrets

`STAND_SUB_URL` is read from a gitignored file (`.omc/autopilot/secrets/sub-url.txt`
by default, or `$STAND_SUB_URL_FILE`) and handed to the container **by name**
(`docker run -e STAND_SUB_URL`), so the value never appears in a command line.
It is never echoed. `vctl subscribe fetch`'s stderr is never printed, because a
Go http transport error quotes the URL it failed on and that text would
otherwise land in `logs/real-egress.log`. Two assertions guard it:
`real_url_not_leaked` inside the container, and a final `grep` over `logs/`,
`build/` and `stand/` in `run.sh` that fails the whole run if the URL turns up.

The one place the value is unavoidably visible is the `vctl subscribe fetch`
argv inside the ephemeral container, because `vctl` has no `-url-file` flag.
Nothing in the stand prints a process list.

## What this deliberately does NOT test

- **PKI.** The HTTPS origin is self-signed and the client uses `curl -k`. The
  assertion is that a TLS handshake completes end-to-end through tproxy. The
  `real-egress` TLS assertion does validate a real certificate chain.
- **The OpenWrt SDK build.** The `.ipk` the `procd` mode installs is assembled
  by the stand from the package tree; the published SDK-built `.ipk` (which
  targets `aarch64_cortex-a53` and predates the fixes under test) is not
  exercised. What the mode covers is the maintainer scripts, the init script and
  procd — not the SDK toolchain.
- **The control plane.** No mode talks to `api.vectra-pro.net`. `procd` proves
  it structurally; `ui` and `setup` run `vctl agent` against a stub panel on
  the stand's own network, and the other modes never start it at all.
- **Geo matching semantics.** `geoip.dat`/`geosite.dat` are installed at
  `XRAY_LOCATION_ASSET` and the routing references `geoip:private`, so xray does
  load them — but no assertion depends on the match result.
- **The kill-switch on IPv6.** `MODE=killswitch` and its four controls close
  this gap for v4: the armed ruleset is applied by the shipped code, the leak is
  produced with real packets and `vctl_killswitch_drops` is read by name. The v6
  modes still set `killSwitch: false`, so an armed v6 ruleset is not exercised —
  the v6 leak assertions read the stand's own guard table, which proves the
  packet reached plain forwarding but says nothing about whether the shipped
  rule would have stopped it.

  Found on the armed mode's first run and fixed before it landed: `policy drop`
  on the prerouting chain. In an nftables base chain `return` falls through to
  the chain POLICY, so all five exemptions — the router's own address, the LAN,
  the split-DNS direct sets — became UNCOUNTED drops, and
  `vctl_killswitch_drops` read 0 throughout. `killswitch-policydrop` keeps that
  measured.

  Behaviour worth knowing before arming it on a live router: ICMP from a LAN
  client is dropped, because the TPROXY rule matches only tcp/udp and TPROXY
  cannot carry ICMP. `ping` from the LAN stops working. The stand reports the
  number (`ICMP note:` in the log, read from `vctl_unproxied_other`, where
  non-tcp/udp is counted apart from the leak instrument) rather than asserting
  a requirement nobody has stated.
