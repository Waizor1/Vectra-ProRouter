# PassWall2 26.8.10 — working map (as it runs on the Vectra fleet)

Reference, 2026-09-29. For engineers and AI sessions replacing PassWall2 with
vctl (`router/vectra-controller-pro`). The point is that nothing PassWall2 did,
loudly or silently, goes missing in the replacement. §13 is the checklist.

**Sources**

- The package the fleet runs, from the stand bundle
  `test/dataplane/build/passwall2-26.8.10/luci-app-passwall2_26.8.10-r1_all.ipk`
  (Version 26.8.10-r1, SourceDateEpoch 1786367482), unpacked. `file:Lnnn`
  refers to that copy; paths are the on-router paths. Sibling packages come
  from `manifest.json` in the same directory.
- A separately fetched `nftables.sh` (1023 lines, uses `ACL_JSON`/`acl_node`)
  is a **different upstream revision** from the 1132-line file in this ipk.
  This map follows the ipk.
- The operators' fleet notes (kept outside this repository), cited by file
  name, and ICM memory entries, cited as "ICM".
- Measurements on test router 1111 on 2026-09-29 (§11).
- vctl as of 0.6.0-r16 (`5c2a657`) and the legacy agent
  (`router/vectra-controller-agent`) in this repo. Rows 31 and 33–36 of §13
  also say what native routing (`route_source 'native'`, 0.6.0-r17) does.

**Markers.** **[code]** read in the 26.8.10 source. **[obs]** observed on fleet
routers (note named). **[1111]** measured on 1111 on 2026-09-29. **[inf]**
inference, not verified on a router.

No secrets appear here. Subscription URLs, UUIDs, keys, HWIDs and provider host
names are written as placeholders (`<subscription-url>`, `ruN:5005x`, `pl2:443`).

---

## 0. The whole data path on one screen

```
LAN client ── DNS :53 (to ANY server) ──┐        LAN client ── TCP / UDP ──┐
                                        ▼                                  ▼
 nft dstnat: saddr ∈ psw2_direct → PSW2_DNS      table inet passwall2 (fleet mode)
             redirect to :11400                   dst ∈ psw2_direct ............ never enters the chains
                                        │         dst ∈ psw2_vps / psw2_wan .... return
                                        ▼         UDP reply direction ........... return
 dnsmasq_default :11400 (copy of main dnsmasq)    dst ∈ 198.18.0.0/16 (FakeDNS) → xray
   node host names → AUTO_DNS + nftset psw2_vps   dst ∈ psw2_myshunt_<rule>:
   everything else → 127.0.0.1#15353                  rule bound to _direct ..... return (kernel, never xray)
                                        │             rule bound to a node ...... xray
                                        ▼         dst ∈ psw2_myshunt_white ..... return (kernel)
 xray dns-in 127.0.0.1:15353 → dns-out            anything else ............... xray
   A/AAAA of a FakeDNS rule's domain → 198.18.x.y     TCP: REDIRECT to :1041 (conntrack original dst)
   A/AAAA otherwise (default=_direct) → chinadns-ng   UDP: mark 0x50535732 + TPROXY to :1041
   qtype 65 (HTTPS RR) → empty NOERROR                          ▼
   other qtypes → chinadns-ng                     xray tcp_redir/udp_redir :1041
                                        ▼           sniff http,tls,quic,fakedns (routeOnly)
 chinadns-ng :15354 → AUTO_DNS (plain UDP :53)      rules in UCI shunt_rules order → node / direct / blackhole
   every answer → nft set psw2_myshunt_white        xray's own sockets: SO_MARK 255 → not intercepted
```

| Thing | Value | Where [code] |
|---|---|---|
| TCP REDIRECT / UDP TPROXY port | first free port from 1041 | `app.sh:get_config` L1263 |
| xray DNS inbound | 127.0.0.1:15353 (hard-coded) | `app.sh:run_global` L581 |
| chinadns-ng (direct DNS, nft-set writer) | first free port from 15354 | `app.sh:run_xray` L110 |
| dnsmasq copy `dnsmasq_default` | first free port from 11400 | `app.sh:run_global_dnsmasq` L703 |
| global socks inbound | 127.0.0.1:1070 | `app.sh:run_global` L623 |
| TPROXY fwmark | `0x50535732` (ASCII "PSW2") | `nftables.sh` L17 |
| policy route | `ip rule fwmark 0x50535732 lookup 999 pref 999`, `local 0.0.0.0/0 dev lo table 999` | `nftables.sh:add_firewall_rule` L842 |
| xray outbound SO_MARK | 255 (exempted by `meta mark 255 return`) | `util_xray.lua:gen_outbound` L161, direct outbound L2015 |
| FakeDNS pool | 198.18.0.0/16, poolSize 65535 (fc00::/18 unless UseIPv4) | `util_xray.lua:gen_config` L1643 |
| xray config | `/tmp/etc/passwall2/acl/default/global.json` | `app.sh:run_global` L618 |
| runtime dir / cache dir | `/tmp/etc/passwall2` (wiped by stop) / `/tmp/etc/passwall2_tmp` (kept) | `utils.sh`, `api.lua` L19 |
| log | `/tmp/log/passwall2.log` (emptied at stop when >1000 lines) | `utils.sh:clean_log` |

---

## 1. Packages and binaries

| Package (stand bundle) | Role | Notes |
|---|---|---|
| `luci-app-passwall2` 26.8.10-r1 (all) | shell scripts, the Lua config generator, LuCI | Depends: libc, coreutils, coreutils-base64, coreutils-nohup, coreutils-timeout, curl, ip-full, libuci-lua, lua, luci-compat, luci-lib-jsonc, lyaml, resolveip, tcping, geoview, v2ray-geoip, v2ray-geosite, unzip, luci-lua-runtime. **`xray-core` is not in Depends** (it was up to 26.4.10). |
| `xray-core` 26.7.28-r1 | the only proxy core the fleet uses | Manifest runtime target 26.7.28. Configs generated by 26.8.10 carry `version.min 26.3.27`, and xray refuses older builds. |
| `chinadns-ng` 2025.08.09-r1 | direct DNS + nft-set writer | Not in Depends. If absent, `run_ipset_dns_server` falls back to a plain dnsmasq instance (`app.sh` L909). |
| `geoview` 0.2.6 | expands `geoip:<code>` into CIDR files for nft sets | Used only with `enable_geoview_ip=1` (§8). |
| `tcping` 0.3 | LuCI latency column | Not on the data path. |
| `v2ray-geoip` / `v2ray-geosite` | default `/usr/share/v2ray/*.dat` | The fleet overwrites the files (§8). |
| `sing-box` 1.13.18, `hysteria` 2.12.1 | optional cores | Unused on the fleet [inf: all fleet nodes are type Xray]. |

System requirements: dnsmasq built with `nftset` (dnsmasq-full), the `fw4`
command, and kmod-nft-socket/-tproxy/-nat. `check_run_environment` (`app.sh` L20)
only **warns** when something is missing; it never refuses. It also warns on
dnsmasq < 2.90. With `prefer_nft=1` the backend is nftables; `iptables.sh` is
the fallback and is not used on the fleet.

**Processes.** Every binary runs through a symlink in `/tmp/etc/passwall2/bin/`
(`utils.sh:ln_run` L376):
`xray run -c …/acl/default/global.json`,
`chinadns-ng -C …/acl/default/dns_global_direct.conf -v`,
`dnsmasq_default -C …/acl/default/dnsmasq.conf -x …/dnsmasq.pid`,
plus the shell loops `monitor.sh` and `lease2hosts.sh` (and `tasks.sh` in loop
mode). PassWall's own "is it running" test is `pgrep -f /tmp/etc/passwall2/bin`.
The init script is plain rc.common, not procd, so `/etc/init.d/passwall2 running`
prints usage and exits 0 (ICM 2026-09-28).

**Binary paths** come from UCI `global_app.*_file`. The fleet sets
`xray_file=/usr/sbin/vectra-xray-wrapper`, which exports `GOMEMLIMIT=80MiB` and
`GOGC=30` and then runs `exec -a "$0" /usr/bin/xray` (`reference_xray_lowmem_health_checks.md`).

**Install side effects** (`/etc/uci-defaults/luci-passwall2`) [code]:
- Adds firewall includes `passwall2` and `passwall2_server`
  (type script, `/var/etc/passwall2.include`).
- Sets `dhcp.@dnsmasq[0].localuse=1` and `uhttpd.main.max_requests=50`.
- Registers ucitrack (`/usr/share/ucitrack/luci-app-passwall2.json`: config
  passwall2 → init passwall2).
- Copies `0_default_config` to `/etc/config/passwall2` if missing. keep.d
  preserves that file across sysupgrade.
- Migrates old options.
- Renames every `socks` section whose name does not start with `socks_`
  (named ones too) to `socks_<random>`, and every anonymous `acl_rule` /
  `subscribe_list` section to `acl_`/`sub_<random>`.
- Conffiles: `/usr/share/passwall2/direct_ip` and `domains_excluded`, so local
  edits survive upgrades.
- [inf] OpenWrt's `default_prerm`/`default_postinst` stop and start the init
  scripts, so a package upgrade restarts the stack.

**Replacement must:** pin its own core and assets instead of relying on opkg
dependency resolution, own its whole process tree, and not need LuCI or Lua.

**Pitfalls [obs]**
- Package upgrades can remove the core:
  - With opkg autoremove on and `xray-core` marked auto-installed, installing
    26.8.10 removed xray. The predictor in the 2026-09-15 incident was an old
    `xray-core` package (25.10.15); the user-installed flag did not protect it.
  - PassWall stays "running" while every VLESS node is skipped.
  - Notes: `project_passwall_26_8_10_drops_xray_dependency.md`,
    `project_passwall_26_8_10_removed_xray_incident_2026_09_15.md`.
- Package metadata lies about the binary: the package may say 25.x while
  `/usr/bin/xray` is 26.7.28. Trust `xray version`.
- LuCI's "App Update" (`api.to_move`) writes to the `xray_file` path:
  - it clobbers the wrapper (`project_xray_builtin_updater_wrapper_clobber.md`);
  - it installs "latest": 26.9.9 broke PassWall 26.4.10's config
    (`project_andrey_avito_onboarding.md`).
- opkg sizes packages by their uncompressed size (xray 31.3 MB) on small
  overlays (`reference_passwall_lowoverlay_update.md`).

---

## 2. UCI model (`/etc/config/passwall2`)

| Section type (fleet name) | Written by legacy agent? | Options that matter (package default → fleet) |
|---|---|---|
| `global` (`vectra_global`) | yes | `enabled` 0→1. `node` `rulenode`→`myshunt` (some routers `RoscomShunt`). `localhost_proxy` 1. `client_proxy` 1. `node_socks_port` 1070, `node_socks_bind_local` 1. `socks_enabled` 0. `acl_enable` 0. `direct_dns_query_strategy` UseIP. `remote_dns_protocol` tcp→doh. `remote_dns` 1.1.1.1. `remote_dns_doh` https://1.1.1.1/dns-query→Google DoH. `remote_dns_detour` remote (fleet direct, per the onboarding notes). `remote_fakedns` 0. `remote_dns_query_strategy` UseIPv4. `dns_hosts` (fleet: `dns.google`/`dns.google.com` → 8.8.8.8). `dns_redirect` 1. `dns_cache` 1. `log_node` 1. `loglevel` error. Transient flags: `flush_set`, `dnsmasq_dns_redirect`, `dnsmasq_servers`. |
| `global_delay` | no | `start_daemon` 1 (monitor.sh). `start_delay` 60 s (1 s if the option is absent). `stop/start/restart_{week,time,interval}_mode`. |
| `global_forwarding` | no | `tcp_proxy_way` redirect. `tcp_redir_ports` 1:65535 (**if the option is absent the code default is only `22,25,53,143,465,587,853,993,995,80,443`**). `udp_redir_ports` 1:65535. `tcp/udp_no_redir_ports` disable. `accept_icmp` 0. `ipv6_tproxy` 0. `prefer_nft` 1. |
| `global_xray` | no | `sniffing_override_dest` 0. `buffer_size` ([1111]: 0 → policy bufferSize 0). `fragment*`, `noise`. |
| `global_other` | no | `url_test_url` (absent → `https://www.google.com/generate_204`). `auto_detection_time` tcping. |
| `global_rules` (`vectra_global_rules`) | yes | `geoip_url`/`geosite_url` (package default Loyalsoldier; fleet itdoginfo/roscomvpn, §8). `v2ray_location_asset` /usr/share/v2ray/. `geoip_update`/`geosite_update` 1. `update_week_mode`/`update_time_mode` (fleet cron 06:00 [obs]). `auto_update` is written by the agent but read by nothing. |
| `global_app` (`vectra_global_app`) | yes | `xray_file`, `sing_box_file`, `hysteria_file`, `geoview_file`. |
| `global_subscribe` (`vectra_global_subscribe`) | yes | `filter_keyword_mode` and discard/keep lists, `*_type` core preferences. Its `user_agent` is only LuCI's default for **new** subscriptions (`project_passwall_ua_nil_version_crash.md`). |
| `nodes` | yes | Normal node: `type` Xray, `protocol` vless…, `remarks`, `address`, `port`, `uuid`, tls/reality/utls/fingerprint, `transport` and its options, `flow`, `encryption`, `mux`/`mux_concurrency`/`xudp_concurrency`, `domain_strategy`, `domain_resolver*`, `chain_proxy`/`preproxy_node`/`to_node`, `add_mode` (2 = from a subscription), `group` (= the subscription's remark). Special nodes: `_shunt` (below), `_balancing`, `_urltest`, `_iface`. |
| `shunt_rules` | yes | `remarks`, `group`, `network` (tcp,udp), `port`, `protocol`, `inbound`, `source`, `domain_list`, `ip_list`. `sourcePort` is LuCI-only (the xray generator ignores it). `invert` is sing-box only. |
| `subscribe_list` (`vectra_sub_*`) | yes | `remark`, `url`, `user_agent`, `hwid` (must be 1, §7). `access_mode` auto/direct/proxy. `boot_update`. `update_week_mode` ('' off, 0–6 weekday, 7 daily, 8 loop). `update_time_mode` HH:MM. `update_interval_mode` (hours, loop mode). `md5`. `allowInsecure`, `domain_strategy`, filters, `*_type`, chain options. Written back: `expired_date`, `rem_traffic`. **The legacy names `auto_update`/`week_update`/`time_update` are ignored.** |
| `socks`, `acl_rule`, `haproxy_config`, `global_haproxy`, `global_singbox`, `xray_noise_packets` | socks only | unused on the fleet [inf] |

**Shunt node (`protocol _shunt`) options** [code `util_xray.lua` L1344, LuCI `include/shunt_options.lua`]
- `<ruleId>` = `_direct` | `_blackhole` | `_default` (the default node's
  outbound) | `<nodeId>`. Unset means the rule is skipped entirely.
- `<ruleId>_fakedns=1`: the rule's domains resolve to FakeDNS. Needs the main
  switch `fakedns=1`.
- `<ruleId>_proxy_tag`: a pre-proxy node for that rule.
- `default_node` (**must be `_direct` on the fleet**,
  `feedback_shunt_default_node_direct.md`), `default_fakedns`, `default_proxy_tag`.
- `domainStrategy`: LuCI default IPOnDemand; the generator falls back to AsIs
  when unset. `domainMatcher` hybrid.
- `write_ipset_direct` (default 1) and `enable_geoview_ip` (default 1), §5/§6.
- `shunt_group`: only `shunt_rules` whose `group` equals it apply. The generator
  and nftables.sh compare case-sensitively; LuCI lowercases.
- Slot keys such as `China`/`GFW`/`QUIC` on a node do nothing unless a
  `shunt_rules` section with that id exists.

**How the fleet writes it**
- [code] `router/vectra-controller-agent/internal/passwall/apply.go:buildBatchCommands`:
  1. Deletes **every** section of types global, global_rules, global_app,
     global_subscribe, socks, shunt_rules, nodes and subscribe_list.
  2. Re-creates them as named sections (`vectra_global`…, rule ids, node ids,
     `vectra_sub_*`) and commits.
  3. Optionally runs `lua subscribe.lua start all` / `lua rule_update.lua log <assets>`.
  4. Runs `/etc/init.d/passwall2 restart`, then re-reads UCI to compute the
     applied digest.
- It never writes `global_forwarding`, `global_delay`, `global_other`,
  `global_xray` or `acl_rule`; those keep the package defaults or whatever was
  left on the device.
- [obs] The agent takes a slot binding only from `rule.outboundNodeId`
  (`reference_shunt_binding_lives_on_rule.md`).
- [obs] Its route-policy self-heal rewrites `passwall2.<activeShunt>.<Slot>` on
  check-ins and restarts PassWall (`reference_route_policy_label_host_and_selfheal.md`).
- [obs] It reverts local edits to `passwall2.*` but never touches network/dhcp
  (`project_1111_ipv6_wan_firewall_reload.md`).
- [obs] Customers with LuCI access change the global node or DNS and press
  "update components" (`project_andrey_avito_onboarding.md`,
  `project_nataliafilisiti_nonshunt_global_node_deadlock.md`).
- [code] A LuCI "Save & Apply" goes through ucitrack to
  `/etc/init.d/passwall2 reload`, which is a full restart. [inf] A ubus-level
  commit (`luci.model.uci` `cursor:commit`) may do the same through procd config
  triggers; a CLI `uci commit` does not.

---

## 3. Lifecycle: boot, start, stop, restart

### 3.1 `/etc/init.d/passwall2` (rc.common, START=99, STOP=15)

- **boot():** backgrounds `boot_func`, which sleeps `global_delay.start_delay`
  (60 s from the default config), runs `restart boot`, then touches
  `/var/lock/passwall2_ready.lock`. Until then the LAN is unproxied and DNS is
  plain dnsmasq.
- **Locking:** start/stop/restart take `flock -xn` on `/var/lock/passwall2.lock`.
  A concurrent call logs "already running" and **exits 0: the request is
  dropped, not queued.**
- **restart:** waits up to 300 × 2 s for `/var/lock/passwall2_{rule_update,subscribe}.lock`,
  then **deletes them even if their owner still runs**, then runs `app.sh stop`
  and `app.sh start <arg>`. `reload` is the same as restart.
- **Daemon file descriptors:** the stack is not procd, so every daemon started
  from a caller inherits the caller's descriptors. Started from vctl's procd
  init, that meant procd's lock fd 1000, and the stack hung vctl's init (ICM
  2026-09-29; fixed in vctl by `release_locks`).

### 3.2 `app.sh start` (L1119), in order [code]

1. **Leftover check.** If anything runs from `/tmp/etc/passwall2/bin`, run
   `(stop)` in a subshell and sleep 2 s. So in 26.8.10, `start` on a running
   stack is a restart; older builds only stopped it (ICM).
2. **`get_config`** (L1254):
   - `ENABLED_DEFAULT_ACL=1` only when `enabled=1` **and** `node` is a `nodes`
     section.
   - `REDIR_PORT`; the forwarding and DNS options.
   - `get_direct_dns` (L1234): `AUTO_DNS` is the first two dnsmasq `server=`
     entries without `/` (as ip#port). Failing that, the first two IPv4 from
     `resolv.conf.auto`; failing that, 119.29.29.29. `ISP_DNS`/`ISP_DNS6` also
     come from `resolv.conf.auto`.
3. **Environment.** Exports `XRAY_LOCATION_ASSET=V2RAY_LOCATION_ASSET=<v2ray_location_asset>`,
   `ENABLE_DEPRECATED_GEOSITE/GEOIP=true` and
   `SS_SYSTEM_DNS_RESOLVER_FORCE_BUILTIN=1`, then `ulimit -n 65535`.
4. **`start_haproxy`, `start_socks`**, plus `socks_auto_switch.sh` per socks
   section.
5. **`check_run_environment`** selects `USE_TABLES=nftables`.
6. **dnsmasq's own redirect.** If proxying and `dhcp.@dnsmasq[0].dns_redirect=1`
   (24.10's dnsmasq option): back it up into
   `passwall2.@global[0].dnsmasq_dns_redirect`, set it to 0, restart dnsmasq.
7. **`run_global`** (L572) → **`run_xray`** (L73):
   - With `write_ipset_direct=1`, starts chinadns-ng on the first free port from
     15354; it writes into `psw2_<node>_white/white6`.
   - `lua util_xray.lua gen_config <json> > global.json`, then
     `xray run -test -c global.json`.
   - On success, xray is **queued**.
   - On failure, it logs "…process … error, skip this transparent proxy!",
     copies xray's error into the log and sets `ENABLED_DEFAULT_ACL=0`. That
     means **no DNS hijack and no nft rules: every client goes direct** ("no
     proxy mode", fail-open).
8. **`run_global_dnsmasq`** (L658):
   - With `dns_redirect=1`: a dnsmasq copy `dnsmasq_default` on the first free
     port from 11400, and `lease2hosts.sh` starts.
   - The `dns_redirect=0` "old logic" modifies the main dnsmasq instead
     (`/tmp/dnsmasq.d/dnsmasq-passwall2.conf`, `addnmount`, a server backup) —
     not used on the fleet [inf].
9. **`nftables.sh start`:** `add_firewall_rule` (L689), then `gen_include` (L1065).
10. **Sysctls.** `net.bridge.bridge-nf-call-iptables=0`, plus ip6tables when
    IPv6 is proxied; the old values are cached for stop.
11. **`run_process_queue`** starts xray, **after** the nft rules exist. [inf]
    Briefly, traffic is sent to :1041 before anything listens there.
12. **`start_crontab`** (L754):
    - starts `monitor.sh` when `start_daemon=1`;
    - rewrites PassWall's cron lines, unless `/tmp/lock/passwall2_cron.lock`
      exists (a cron-run subscription or rule update caused this restart);
    - starts `tasks.sh` if any `*_week_mode` is 8;
    - `/etc/init.d/cron restart`.
13. **Boot only:** subscriptions with `boot_update=1` run
    `subscribe.lua start <ids> cron` 5 s later.

### 3.3 `app.sh stop` (L1184) [code]

1. `clean_log`, then load the cached variables from `/tmp/etc/passwall2/var`.
2. **`nftables.sh stop`:** `del_firewall_rule` (L1013), plus `flush_nftset`
   when `global.flush_set=1` (destroys every `psw2_*` set and the geoview
   cache), plus `flush_include`.
3. **Kill.** Runs `delete_ip2route` and kills plugin pids. Then it SIGKILLs
   `monitor.sh`, the `sleep 6s|9s|58s` loops and **every process whose command
   line contains `passwall2/`**. Only app.sh, subscribe.lua, rule_update.lua,
   tasks.sh, server_app.lua and ujail are spared.
4. **`stop_crontab`** removes the `/etc/init.d/passwall2`, `rule_update.lua log`
   and `subscribe.lua start` lines from `/etc/crontabs/root` (unless
   `cron.lock` exists). **The nightly subscription and geo cron lines exist only
   while PassWall is started.**
5. **dnsmasq.** Restores the `dns_redirect` backup. For the old logic, or when
   there was a backup, it deletes the `addnmount` and restarts dnsmasq.
6. **Cleanup.** Restores the bridge-nf sysctls, `rm -rf /tmp/etc/passwall2`,
   `exit 0`.

### 3.4 What stop leaves behind [code, 1111]

**Left in the kernel**
- `table inet passwall2` with its four base chains (`flush_table` is commented
  out):
  - `dstnat`: nat/prerouting, priority dstnat−1;
  - `mangle_prerouting`: filter/prerouting, priority mangle−1 = −151;
  - `mangle_output`: route/output, priority mangle−1;
  - `nat_output`: nat/output, priority −1.
- In `mangle_prerouting`, `meta nfproto ipv4 meta l4proto tcp socket transparent 1 meta mark set 0x50535732 counter accept`
  (and an IPv6 twin when IPv6 was proxied):
  - `del_firewall_rule` deletes only base-chain rules whose text contains
    `PSW2_`, and this rule does not;
  - every start adds another copy. [1111] 3 copies after 3 hand-backs.
- The sets `psw2_<node>_<rule>(6)` and `psw2_<node>_white(6)`, unless
  `flush_set=1`. Only the base sets psw2_local/wan/direct/vps are destroyed.

**Left elsewhere**
- `/tmp/etc/passwall2_tmp`: the dnsmasq rule cache, the xray version cache
  keyed by binary md5, `sub_curl_headers` (the HWID headers), `geo_output`,
  `dhcp-hosts`, `cache_*.txt`.
- The install-time settings: `localuse=1`, the firewall include section (its
  file flushed to `#!/bin/sh`), `max_requests=50`.

**Effect on the next TPROXY user.** Every IPv4 TCP packet of a transparent
socket gets mark 0x50535732, and evaluation of that chain stops at priority
−151, before a −150 chain. The r16 CHANGELOG says it "marked every packet of
xray's connections". The sets held about 2 MB of kernel memory, read back by
every nft set command.

**Removed by stop:** the table-999 ip rule and route, the PSW2_* chains, the
four base sets, the cron lines, the processes.

### 3.5 What restarts PassWall

Every restart is a full stop + start: xray restarts and every proxied
connection drops.

- `init restart`/`reload`: LuCI apply; the legacy agent's apply, slot
  self-heal and rescue enter/leave-direct.
- `subscribe.lua` after any run that produced nodes, manual runs included:
  `/etc/init.d/passwall2 restart &` (`subscribe.lua:update_node` L2362). This
  includes the nightly cron. An unchanged md5 or an empty feed means no restart.
- `rule_update.lua` after a geo file changed:
  - it sets `flush_set=1` and commits with apply;
  - [obs] a restart around 06:00 (`reference_router_daily_reboot_cron.md`);
  - [inf] the mechanism is the ucitrack config trigger.
- The cron `restart_week_mode` and `tasks.sh` loop (not configured on the
  fleet [inf]), and a package upgrade [inf].
- **Not** the iface hotplug: it is dead code in 26.8.10 (§9.4).

**Replacement must**
- Order its start: resolver and proxy listening first, interception last.
- Decide fail-closed vs fail-open explicitly. PassWall fails open, silently.
- Clean both its own leftovers and PassWall's.
- Never turn node or geo refreshes into restart storms.

---

## 4. xray config generation (`util_xray.lua:gen_config` L848)

### 4.1 Inputs and top level

`run_xray` passes a JSON argument:
- `flag=global`, `node`, `redir_port`, `tcp_proxy_way`;
- the socks address/port;
- `dns_listen_port=15353`, `dns_cache`;
- `direct_nftset` when `write_ipset_direct` is set;
- `remote_dns_fake`, `remote_dns_*` (the protocol-specific fields);
- `direct_dns_udp_server/port` (127.0.0.1:15354 when chinadns-ng runs, else
  AUTO_DNS);
- `direct_dns_query_strategy`.

The top level of the generated config:
- `env.XRAY_LOCATION_ASSET`;
- `log.loglevel` (warn→warning);
- `version.min "26.3.27"`;
- `dns`, `fakedns`, `inbounds`, `outbounds`, `routing`,
  `observatory`/`burstObservatory`;
- `policy.levels.0 = {bufferSize: global_xray.buffer_size, statsUserUplink/Downlink: false}`.

### 4.2 Inbounds (global instance)

| Tag | Listen | Protocol / settings | Sniffing |
|---|---|---|---|
| `socks-in` | 127.0.0.1:1070 | socks, noauth, udp | shunt: http,tls,quic, routeOnly |
| (http) | — | never generated for the global instance: `run_global` passes `http_port` without an address, and `run_xray` needs both | — |
| `dns-in` | 127.0.0.1:15353 | dokodemo-door, tcp,udp | — |
| `tcp_redir` | :1041 | dokodemo-door, followRedirect, `sockopt.tproxy = tcp_proxy_way` (**redirect** on the fleet), network tcp | shunt: `[http,tls,quic]` + `fakedns` when FakeDNS is used, metadataOnly false, **routeOnly true** (unless `sniffing_override_dest=1`, which uses real override plus `domains_excluded`) |
| `udp_redir` | :1041 | same, `sockopt.tproxy "tproxy"`, network udp | same |

### 4.3 Outbounds

- **One per bound node** (`gen_outbound` L54). Tag `<ruleId>:<node remarks>`;
  the default node's tag is `default`, placed first. Contents:
  - `settings`: address, port, id, flow, encryption, level 0;
  - `streamSettings.sockopt`: `mark 255`, `domainStrategy` (node
    `domain_strategy` or UseIP), TFO, MPTCP, happyEyeballs;
  - the transport key `method` (xray ≥ 26.7.11) or `network`;
  - tls/reality settings (serverName, fingerprint when utls, pinnedPeerCertSha256,
    verifyPeerCertByName, ECH, alpn);
  - the transport's own settings: raw header, ws, grpc, httpupgrade, xhttp with
    base64 `extra`, mkcp, hysteria;
  - `finalmask` (fragment, noise, salamander, realm);
  - `mux {enabled, concurrency, xudpConcurrency (default 8)}`.
- **Other node types.** Non-Xray nodes run as separate local socks processes and
  are reached through a socks outbound. Chains use `proxySettings`: a pre-proxy,
  a landing node or an interface.
- **Balancers** (`_balancing`): a loopback outbound plus a balancer plus
  observatory or burstObservatory (probe interval at least 10 s, default 60 s).
- **`direct`:** freedom
  `{domainStrategy: <direct_dns_query_strategy>, finalRules: [{action: "allow"}]}`
  with sockopt `mark 255`. It is placed **first** when the default is direct,
  which makes it xray's fallback outbound.
- **`blackhole`**, and **`dns-out`** (§5.3).

### 4.4 Routing (L1344–1505, L1840–1909)

**Rule order**
1. For every xray DNS server that has an outboundTag and is not FakeDNS:
   `{inboundTag:[<server tag>], outboundTag}`.
2. `{inboundTag:[dns-in], outboundTag:dns-out}`.
3. Balancer rules.
4. One pass over `shunt_rules` **in UCI file order**, filtered by `shunt_group`:
   - `<remarks> Domains` (domain_list), then `<remarks> IP` (ip_list);
   - each with `network` (default tcp,udp), `port`, `protocol`, `source`,
     `inboundTag`;
   - a rule with neither list becomes a single network/port rule (e.g.
     DiscordVoiceUdp);
   - lines starting with `#`, `rule-set:` or `rs:` are skipped.
5. `default` → the default outbound, moved to the end. It matches `network tcp,udp`,
   or `port 1-65535` under IPIfNonMatch.

`routing.domainStrategy` is the node's (fleet: IPOnDemand); `domainMatcher` is
hybrid.

**First match wins, so UCI section order is routing semantics.**
[obs] WorldProxy's IP rule (which contains Discord's subnets) catches Discord
voice UDP before `DiscordVoiceUdp`. Its mux/xudp therefore has to sit on the
WorldProxy node (`project_discord_voice_xudp_2026_08_03.md`).

### 4.5 Version-dependent output [code]

The xray version comes from running `<xray_file> version` once per binary md5,
cached in `/tmp/etc/passwall2_tmp/<md5>` (`api.lua:get_bin_version_cache` L745).
A failed detection yields "", and `compare_versions` treats "" as 0, which
selects the oldest schema.

| Branch | Effect |
|---|---|
| < 26.7.11 | `network` instead of `method` |
| ≤ 26.4.17 | no `rules` in dns-out |
| < 26.4.25 | `nonIPQuery` skip/reject and `blockTypes [65]` instead |
| always | `version.min 26.3.27` |

[obs] App and core skew breaks xray in both directions:
- app 26.6.3 emitted `action:return`, which xray 26.4.25 rejects
  (`project_passwall_upstream_xray_skew.md`);
- xray 26.9.9 dropped `proxySettings`, which app 26.4.10 emits
  (`project_andrey_avito_onboarding.md`);
- the legacy `nonIPQuery` and the new `rules` must not be mixed
  (`project_isp_forges_dns_even_public_resolvers.md`).

### 4.6 Side effects of generation

- **Rule-change detection.** The routing rules are hashed into
  `/tmp/etc/passwall2_tmp/cache_global.txt`. When they change, all
  `psw2_<node>_*` sets are `nft flush`ed, so stale direct answers do not outlive
  a rule change.
- **Direct nodes.** Non-Xray node ids are written to `direct_node_list`, which
  becomes output-chain `return` rules for their server:port.

**Replacement must**
- Honour UCI rule order as priority, and the domain-then-IP split.
- Keep port-only rules.
- Keep sniffing `routeOnly` with FakeDNS restoration.
- Give xray's own sockets a mark distinct from the TPROXY mark (PassWall: 255
  vs 0x50535732).
- Always pass `xray run -test` before switching.
- Generate against the **actual** running binary's schema.

---

## 5. DNS pipeline, end to end

### 5.1 Components

| Piece | Listens | Config | Job |
|---|---|---|---|
| main dnsmasq | :53 | unchanged, `localuse=1` | DHCP; the router's resolver (its queries get redirected away) |
| `dnsmasq_default` | :11400 | `/tmp/etc/passwall2/acl/default/dnsmasq.conf` | Front resolver for LAN and router. A copy of the main config (`helper_dnsmasq.lua:copy_instance` L119) that drops lines containing `passwall2`, `ubus`, `dhcp`, bare `server=`, `port=` and `conf-dir=`, but keeps `address=` and `server=/dom/…`. It adds `port`, a `conf-dir` symlinked to `/tmp/etc/passwall2_tmp/dnsmasq_default/`, `server=127.0.0.1#15353`, `all-servers`, `no-poll` and `no-resolv`. |
| its conf-dir | — | `001-server.conf`, `ipset.conf` | For every node host name: `server=/.<host>/<AUTO_DNS>` and `nftset=/.<host>/4#inet#passwall2#psw2_vps,6#inet#passwall2#psw2_vps6` (`add_rule` L159) |
| xray `dns-in` → `dns-out` | 127.0.0.1:15353 | global.json | Decides per qtype and domain (5.3) |
| chinadns-ng | 127.0.0.1:15354 | `dns_global_direct.conf` | Direct resolver and set writer: `china-dns`/`trust-dns` = AUTO_DNS, `filter-qtype 65`, **`default-tag chn`** with `add-tagchn-ip inet@passwall2@psw2_<node>_white,…white6`, and `group vpslist` (node host names) → `group-ipset psw2_vps,psw2_vps6` (`app.sh:run_ipset_chinadns_ng` L917) |

### 5.2 A LAN client's query

1. **Client** → UDP/TCP 53 to the router or to any IP.
2. **mangle_prerouting:** router addresses are in `psw2_direct`, so nothing
   happens. Toward an outside resolver, UDP enters PSW2_MANGLE, where
   `udp dport 53 accept` keeps it away from TPROXY.
3. **dstnat:** `ip saddr @psw2_direct jump PSW2_DNS` (inserted first), then
   `redirect to :11400`, for TCP and UDP. With `dns_redirect=1` this catches
   **every** LAN query, whatever the destination.
4. **dnsmasq_default** answers from its cache, the copied `address=` rules or
   the node-name servers. Everything else goes to 127.0.0.1#15353.
5. **xray dns-out** (5.3). Result: a FakeDNS address for listed domains; a real
   answer via chinadns-ng for the rest, **whose IPs are then added to
   `psw2_myshunt_white` and bypass xray in the kernel from then on** (§6).

**The router's own lookups** (resolv.conf → 127.0.0.1): nat_output
`oif lo udp/tcp dport 53 redirect to :11400`, only with `localhost_proxy=1`, so
they take the same path.
- The upstream traffic of chinadns-ng and dnsmasq to AUTO_DNS:53 is exempted by
  a PSW2_OUTPUT_MANGLE rule (`nftables.sh` L865) and leaves **directly, in
  plain UDP**.
- xray resolves node names through its `localhost` server (the system
  resolver), which also lands in dnsmasq_default and refreshes `psw2_vps`.

### 5.3 xray DNS as generated for the fleet shape (1111 values; [inf] derived from code)

`dns` block: `hosts` = `dns_hosts` plus the DoH host's bootstrap;
`disableFallback`/`disableFallbackIfMatch` true; `queryStrategy` UseIP;
`disableCache` false. The servers, in the order xray tries them:

1. `dns-in-default`: a clone of the direct server (127.0.0.1:15354, UseIP), with
   no domains, routed out through `direct`. It is the direct server because
   `default_node=_direct` (L1759).
2. Per-node `domain_resolver` servers, if any.
3. `dns-in-WorldProxy`, `dns-in-YouTube`, `dns-in-Special`, `dns-in-Tiktok`:
   address `fakedns`, `domains` = the rule's list, `finalQuery`.
4. `dns-in-vpslist`: `localhost` for `full:<every node host name>`.

The configured remote DoH server (`dns-in-remote`) appears **in no list**:
nothing on the fleet is proxied without FakeDNS, and the default is direct.
Quirk: the rule named `direct` gets the tag `dns-in-direct`, which collides with
the base direct server's tag, so its domain-scoped server is dropped (L1894).
That is harmless while the default is also the direct server.

**`dns-out` rules**, as for xray > 26.4.17:
1. `hijack qType 1,28` for each rule's domains;
2. `return rCode 0` for domains of `_blackhole` rules;
3. `hijack qType 1,28` for everything else;
4. **`qType 65 → return rCode 0`**, an empty HTTPS/SVCB answer;
5. `action direct`, which forwards to chinadns-ng.

So HTTPS records (and with them ECH keys) never reach clients through the
router, which keeps TLS SNI sniffable [inf]. MX, TXT, SRV and PTR go to AUTO_DNS
in plain UDP.

**FakeDNS pool:** `[{ipPool 198.18.0.0/16, poolSize 65535}]` (UseIPv4). AAAA
for FakeDNS names gets no v6 pool [inf].

### 5.4 Clients that bring their own DNS

- **Plain :53 to any resolver:** hijacked (5.2), so PassWall's view is enforced.
- **DoT (TCP 853) and DoH (443):** not hijacked. They are captured as ordinary
  TCP and routed by sniffed SNI/IP, i.e. `default` → direct unless listed.
  - Their answers are real IPs. Listed services still go through the proxy by
    SNI sniffing (routeOnly).
  - Exception: with IPOnDemand, an answer that falls inside a kernel direct set
    (geoip:DIRECT or `white`) leaves directly, as the r16 CHANGELOG notes.
- **IPv6:** `ipv6_tproxy=0`, so none of it is intercepted (§6.7).

### 5.5 Forgery exposure [obs + inf]

The ISP forges NXDOMAIN for blocked names over plain :53 even to 8.8.8.8/1.1.1.1
(`project_isp_forges_dns_even_public_resolvers.md`). PassWall shields a name
from that only when it is **listed in a FakeDNS rule**, because the proxy
resolves it remotely. Still exposed [inf]:
- blocked names that fall to `default=_direct`;
- non-A/AAAA qtypes;
- node host names (dnsmasq → AUTO_DNS);
- any rule proxied without FakeDNS whose remote DNS goes `detour=direct`.

### 5.6 Pitfalls

- **LAN host names** (`*.lan`) may not resolve through the copy [inf]. It holds
  no DHCP state, and the lease file written by `lease2hosts.sh` is not
  referenced in this code path (`add_rule` adds `addn-hosts` only when it is
  passed `LISTEN_PORT`, which `run_copy_dnsmasq` does not do).
- **Stale fake IPs [inf].** They survive an xray restart in client and dnsmasq
  caches, while xray's pool mapping does not. Restart storms therefore surface
  as a burst of dead connections.
- **198.18.x is not proof of proxying.** It shows only that FakeDNS answered
  (`reference_fakedns_not_proxy_discriminator.md`).
- **A broken remote DNS setting kills name resolution.** Evidence: ICM (hh,
  2026-05-02): the DoT host `common.dot.dns.yandex.net` configured as the DoH
  URL cut the internet minutes after the main switch went on. The same setting
  was suspected, not proven, on dmitry-filicity
  (`project_1111_ipv6_wan_firewall_reload.md`). An empty `remote_dns` was a red
  herring in `reference_global_node_must_be_shunt.md`.

**Replacement must**
- Resolve blocked names through the tunnel (vctl r8 does this for dnsmasq's
  upstream).
- Keep node names resolvable outside the tunnel.
- Decide whether to hijack LAN DNS aimed at other resolvers, and what to do
  with qtype 65.
- Keep LAN host names working.
- Survive fake-IP staleness.

---

## 6. nft data plane (fleet mode: TCP REDIRECT, UDP TPROXY, IPv4 only, ICMP off)

### 6.1 Sets (`add_firewall_rule` L695–750, `gen_shunt_list` L213)

All sets are `flags interval, timeout; auto-merge`.

| Set | Filled from | Element timeout |
|---|---|---|
| `psw2_local(6)` | the router's global addresses except WAN ones, plus 127.0.0.1 / ::1 | none |
| `psw2_direct(6)` | `/usr/share/passwall2/direct_ip` (13 ranges: 0/8, 10/8, 100.64/10, 127/8, 169.254/16, 172.16/12, 192.168/16, 224/4, 240/4, ::/127, fc00::/7, fe80::/10, ff00::/8; `geoip:` lines are expanded), the LAN address (only if `/tmp/state` has `network.lan.ifname`), the ISP DNS from resolv.conf.auto | none |
| `psw2_vps(6)` | literal IP `address=`/`download_address=` in UCI, node host names resolved by dnsmasq_default/chinadns-ng, the global node's own address | none |
| `psw2_wan(6)` | WAN IPs from `ubus network.interface dump` (`update_wan_sets` L673) | none |
| `psw2_<shunt>_<rule>(6)` | per bound rule: the literal `ip_list` plus geoview-expanded `geoip:<code>` (not `geoip:private`). **Only when the shunt has `enable_geoview_ip=1` and geoview exists; otherwise no per-rule sets are made at all.** | 365 d |
| `psw2_<shunt>_white(6)` | every answer of the direct DNS (chinadns-ng `default-tag chn`) | 365 d |

198.18.0.0/16 is deliberately **not** in `psw2_direct`. The v6 FakeDNS pool
fc00::/18 **is** inside `psw2_direct6` (fc00::/7), so v6 FakeDNS could not work.

### 6.2 Rules, reconstructed from code for the fleet options (not a dump)

Fleet options: `redirect`, ports 1:65535, no_redir disable, `dns_redirect=1`,
`localhost_proxy=1`.

```
chain dstnat            (nat, prerouting, prio dstnat-1)
  ip6 saddr @psw2_direct6 jump PSW2_DNS         # inserted at top
  ip  saddr @psw2_direct  jump PSW2_DNS
  ip daddr != @psw2_direct ip protocol tcp counter jump PSW2_NAT
chain mangle_prerouting (filter, prerouting, prio mangle-1)
  meta nfproto ipv4 meta l4proto tcp socket transparent 1 meta mark set 0x50535732 counter accept   # never removed
  ip daddr != @psw2_direct ip protocol udp counter jump PSW2_MANGLE
chain mangle_output     (route, output, prio mangle-1)
  ip daddr != @psw2_direct ip protocol udp counter jump PSW2_OUTPUT_MANGLE
  ip daddr != @psw2_direct oif lo counter return
  ip daddr != @psw2_direct meta mark 0x50535732 counter return
chain nat_output        (nat, output, prio -1)
  (udp|tcp) oif lo dport 53 counter redirect to :11400          # router's own DNS
  ip daddr != @psw2_direct ip protocol tcp counter jump PSW2_OUTPUT_NAT

PSW2_DNS:    (udp|tcp) dport 53 counter redirect to :11400
PSW2_NAT:    daddr @psw2_vps return ; daddr @psw2_wan return
             tcp daddr 198.18.0.0/16 redirect to :1041
             tcp daddr @psw2_myshunt_direct return             # _direct rule → kernel
             tcp daddr @psw2_myshunt_WorldProxy redirect to :1041   # one line per bound rule, UCI order
             …
             tcp daddr @psw2_myshunt_white return
             tcp redirect to :1041                               # everything else
PSW2_MANGLE: daddr @psw2_vps return ; ct direction reply return ; daddr @psw2_wan return
             udp iif lo meta mark 0x50535732 tproxy ip to :1041 ; udp iif lo return   # router UDP re-entering
             tcp dport 53 accept ; udp dport 53 accept
             udp daddr 198.18.0.0/16 jump PSW2_RULE
             udp daddr @psw2_myshunt_direct return ; udp daddr @psw2_myshunt_<proxied> jump PSW2_RULE … ; udp daddr @psw2_myshunt_white return
             udp jump PSW2_RULE
             udp meta mark 0x50535732 tproxy ip to :1041          # "meta mark" here is a MATCH
PSW2_RULE:   meta mark set ct mark ; meta mark 0x50535732 return
             tcp flags & (fin|syn|rst|ack) == syn meta mark set 0x50535732
             udp ct state {new,related} meta mark set 0x50535732 ; ct mark set mark
PSW2_OUTPUT_NAT:    daddr @psw2_vps return ; meta mark 255 return ; <same 198.18 / set / catch-all lines as PSW2_NAT>
PSW2_OUTPUT_MANGLE: udp daddr <AUTO_DNS> dport 53 return (inserted first) ; @psw2_vps return ; reply return ; meta mark 255 return
                    <198.18 / set lines with jump PSW2_RULE> ; udp jump PSW2_RULE
(PSW2_MANGLE_V6 and PSW2_OUTPUT_MANGLE_V6 exist, but nothing jumps to them while ipv6_tproxy=0)
ip rule fwmark 0x50535732 lookup 999 pref 999 ; ip route add local 0.0.0.0/0 dev lo table 999
```

### 6.3 What never reaches xray (decided in the kernel)

- destinations in `psw2_direct` (the base chains never jump);
- `psw2_vps` (node servers), `psw2_wan`, UDP reply direction;
- **IPs of shunt rules bound to `_direct`.** On the fleet that is `geoip:DIRECT`:
  35,572 v4 prefixes, 14,807 merged ranges (`project_vctl_memory_r15.md`);
- **every IP the direct DNS ever answered** (`white`, kept for 365 days or until
  the rules hash changes or `flush_set`);
- xray's own sockets (mark 255) and chinadns/dnsmasq upstream DNS to AUTO_DNS:53.

Everything else, including all non-listed "direct" traffic, goes through xray
and leaves by its `direct` freedom outbound.

### 6.4 The router's own traffic (`localhost_proxy=1`)

- **TCP:** nat_output → PSW2_OUTPUT_NAT → REDIRECT :1041, so it is routed by
  the shunt rules (default direct). That includes the controller's check-in,
  opkg and curl. **This is why `default_node` must be `_direct`:** a dead proxy
  there cuts the controller from the panel and strands the router
  (`feedback_shunt_default_node_direct.md`).
- **UDP:** mangle_output marks it (PSW2_RULE), the route hook re-routes it
  through table 999 to `lo`, and it re-enters prerouting, where PSW2_MANGLE
  applies `iif lo … tproxy`.

### 6.5 Marks

- The TPROXY/fwmark is 0x50535732; conntrack remembers it through PSW2_RULE
  (`ct mark set mark`, restored with `meta mark set ct mark`).
- xray's sockets use 255, which every output chain returns on.
- The `socket transparent` rule re-marks established TPROXY'd TCP. In redirect
  mode PassWall itself has no transparent TCP sockets, so there it only matters
  as a leftover.
- `mwan3_start` adds `ct mark 0x50535732 return` to `ip mangle mwan3_hook` and
  patches `/etc/init.d/mwan3` when mwan3 exists (no mwan3 on the fleet [inf]).

### 6.6 A UDP ordering trap [inf]

For UDP, a proxied set's rule only **jumps** to PSW2_RULE, which sets the mark,
and evaluation continues. A later `return` — the `white` set, or a later
`_direct` rule's set — then leaves the chain before the tproxy rule, with the
mark set. The kernel routes the packet to `lo` through table 999 and no socket
takes it, so it is dropped. For TCP, REDIRECT is terminal and the first match
wins. So a UDP destination listed for a proxied rule **and** answered by the
direct DNS is black-holed. Do not copy this pattern.

### 6.7 IPv6, ICMP, ports

- **IPv6:** `ipv6_tproxy=0`, so all client IPv6 bypasses PassWall. The fleet
  disabled `wan6` and LAN RA/DHCPv6 in 2026-06 after 1111's IPv6 flapping
  (`project_1111_ipv6_wan_firewall_reload.md`).
  - [inf] That note blames firewall reloads rebuilding PassWall's rules.
    Neither happens in 26.8.10: the hotplug is dead (§9.4), and a fw4 reload
    only runs the include, which is a no-op while PassWall's rules are present.
  - [inf] The likelier mechanism is clients using the flapping native IPv6
    directly.
- **ICMP:** `accept_icmp=0`, never redirected; a ping proves nothing.
- **Ports:** all TCP and UDP ports are redirected except UDP/TCP 53, which is
  handled by DNS.

### 6.8 Memory

- `gen_include` runs `nft list table inet passwall2 >> PSW2_RULE.nft` at every
  start. Listing a table that holds the geoip:DIRECT set costs about 23 MB of
  RAM (`project_vctl_memory_r15.md`). [inf] Every PassWall start pays that on a
  234 MB router.
- The leftover sets also cost kernel memory (§3.4).

**Replacement must**
- Keep private/LAN/WAN/node-server/fake-pool semantics.
- Put kernel fast-path bypass for large direct lists behind a memory budget.
- Keep marks distinct.
- Exclude reply-direction packets.
- Never `nft list` big sets.
- Remove PassWall's table at takeover.

---

## 7. Subscriptions (`subscribe.lua`)

### 7.1 Triggers

| Trigger | Mechanism | Fleet |
|---|---|---|
| cron | `start_crontab` writes `MM HH * * DOW lua /usr/share/passwall2/subscribe.lua start <ids> cron > /dev/null 2>&1 &` | `00 0 * * * … start vectra_sub_… cron` [obs]; **present only while PassWall is started** |
| loop mode (week 8) | `tasks.sh` every `update_interval_mode` hours (checked every 10 min) | unused |
| boot | `boot_update=1` → 5 s after `start boot` | set on some routers [obs] |
| LuCI / manual | `subscribe_manual*` → `start <id> manual` (ignores md5) | occasional |
| legacy agent | `lua subscribe.lua start all` (apply plan, `refresh_subscriptions` job); the panel's subscription rescue resets md5 first (`project_selfrepair_chain_closed_2026_09_22.md`) | yes |

### 7.2 Flow [code]

1. **`check_instance`.** Waits 100–1000 ms at random; the lock is
   `/var/lock/passwall2_subscribe.lock`, and a second instance exits. Then
   `uci revert passwall2` (**staged, uncommitted UCI changes are discarded**),
   and it waits while the rule-update lock exists.
2. **For each `subscribe_list`, `curl()`** (L1980):
   - args: `-fskL -w %{http_code} --retry 3 --connect-timeout 3 -H 'Accept-Encoding: identity'`;
   - UA: `user_agent`, where empty or `curl` means none, and the literal
     `passwall2` becomes `passwall2/<api.get_version()>` (the nil crash, 7.4);
   - `hwid=1` adds `get_headers()`;
   - `access_mode`:
     - `auto` (default): plain curl when `localhost_proxy=1`, so it goes through
       PassWall's own router interception and is routed by the shunt rules;
       otherwise socks :1070 first, then `direct`;
     - `proxy`: socks 127.0.0.1:1070;
     - `direct`: `--resolve` with an IPv4 looked up by `nslookup` **via
       223.5.5.5**.
   - The body goes to `/tmp/<cfgid>`.
3. **md5 check.** Blank lines are stripped and md5 computed. **If md5 equals the
   stored `md5` and the run is not manual: "No changes", and nothing more
   happens.** Otherwise `parse_link`, and the new md5 is stored.
4. **`parse_link`** (L2367):
   - Formats: `ssd://`; Clash YAML (lyaml); otherwise a base64 list of links
     (vmess, vless, trojan, ss, ssr, hysteria, hysteria2/hy2, tuic, anytls,
     naive).
   - The core type comes from `*_type` and binary availability. **With xray
     absent, every VLESS node is skipped** with "Skipping the VLESS node…".
   - Filters: keyword modes 0–4; no address, 127.0.0.1, an invalid host or
     remarks "NULL" also drop a node.
   - Traffic and expiry are parsed from remarks with Chinese patterns only.
5. **`update_node(0)`** (L2229):
   - It returns without changes if no subscription produced a node.
   - It **deletes every `add_mode=2` node of each group that produced nodes**
     and creates the new ones **with fresh random section ids**.
   - Then it re-points every reference by `select_node` (L2101): the global
     node, socks, haproxy, ACL, **every shunt slot, `default_node`,
     `*_proxy_tag`**, balancer lists and fallbacks, pre-proxy and landing
     nodes.
   - `select_node` tries, in order: the same id; type+remarks+address:port+group;
     type+address:port+group; address:port+group; address+group; remarks+group.
     **Failing all of these, the first node in the list.**
   - Commit. Under `cron`, touch `/tmp/lock/passwall2_cron.lock`.
   - **`/etc/init.d/passwall2 restart &`.**

### 7.3 HWID and headers (reproduce byte for byte)

`get_headers()` (L2003), cached in `/tmp/etc/passwall2_tmp/sub_curl_headers`:
- `x-device-os: OpenWrt`
- `x-ver-os: <DISTRIB_RELEASE>`
- `x-device-model: <trim /tmp/sysinfo/model>`
- `x-hwid: sha256("<trim /sys/class/net/eth0/address>-<model>")`, the first
  word of `sha256sum`

vctl reproduces it; the HWIDs matched byte for byte on 1111
(`project_vctl_legacy_watchdog_conflict.md`).

### 7.4 What breaks

- **Without `hwid=1`** (26.7.16 and later only send headers with it):
  - The provider returns a 156-byte stub `…@0.0.0.0:1#App not supported`, or
    `#Limit of devices reached`.
  - PassWall **applies it**, wiping the node list, and stores the stub's md5.
  - Every later run then says "No changes" forever.
  - Notes: `project_subscription_hwid_gate_2026_08_07.md`,
    `reference_passwall_hwid_gate_version_skew.md`.
- **`user_agent='passwall2'` with `api.get_version()` returning nil**
  (`opkg list-installed` finds no package): "attempt to concatenate a nil
  value" **before** the request. The script still exits 0, so the job reports
  success (`project_passwall_ua_nil_version_crash.md`,
  `project_yuranrod_hwid_not_recognized_2026_08_24.md`).
- **The 26.7.16 option rename.** Old configs (`week_update`/`time_update`/`auto_update`)
  silently produce no cron line: the nightly update was dead on 23/25 routers
  (`project_passwall_26_7_16_killed_subscription_cron.md`).
- **Node churn.** Nightly re-minting of ids and host names changes every node
  id and remaps slots by heuristic:
  - out_of_sync imports, a restart blip, and slots moved to the "first node" or
    to a node with the same remarks (`reference_needs_sync_out_of_sync.md`);
  - labels are not identities (`reference_route_policy_label_host_and_selfheal.md`);
  - the provider shuffles the node set nightly (`project_route_policy_never_self_heals.md`).
- **Stale live config** [obs]. After the 00:00 run, global.json on kirill-msk
  still held yesterday's hosts until the 04:30 reboot
  (`reference_midnight_subscribe_stale_xray_config.md`). By code the run
  restarts PassWall, so that restart must have failed or been dropped there
  (the init lock drops concurrent calls, §3.1) [inf].
- **The md5 lock hides provider changes.** Forcing a refresh needs `md5`
  deleted first (`reference_force_subscription_refresh_via_md5_reset.md`).
- **Format loss.** The provider serves full xray JSON configs (balancers,
  loopback stages, hysteria2, xhttp) to other User-Agents; PassWall gets only
  the degraded base64 vless list (`project_provider_json_subscription_discovery.md`).
- **Probing needs PassWall's full header set.** A UA-only probe gets the stub
  even on healthy routers (`reference_valid_subscription_probe.md`).

**Replacement must**
- Refresh on a schedule and at boot.
- Detect stub or placeholder answers and **refuse to replace** a good node set
  with them.
- Keep slot/exit intent stable across node-id churn.
- Restart or reload so the live config actually changes.
- Report failures honestly (not exit 0).
- Keep the HWID headers identical.

---

## 8. Geo data (`rule_update.lua`) and geoview

**Flow [code]**
1. Lock `/var/lock/passwall2_rule_update.lock`; wait while a subscription runs.
2. For geoip.dat and geosite.dat, with the UCI URLs:
   - Try `<url>.sha256sum`. If it verifies against the current file, "same
     version", done.
   - Otherwise download with `--retry 3 --connect-timeout 3 --max-time 300 --speed-limit 51200 --speed-time 15`,
     a Chrome User-Agent, through `curl_auto` (so via PassWall's routing).
   - Retry once on a size mismatch.
   - Verify sha256 when one exists; otherwise `cmp` against the current file.
   - Move the old file to `/tmp/bak_v2ray/` (RAM) and the new one into place.
3. On any change: set `flush_set=1` and commit with apply. The next stop flushes
   all `psw2_*` sets and the geoview cache, then a restart. xray reads the files
   only at start.

**Fleet** [obs `reference_geosite_noproxy_fresh_unit.md`, `reference_router_daily_reboot_cron.md`]
- Cron `0 6 * * * lua /usr/share/passwall2/rule_update.lua log all cron`.
- URLs:
  - geosite: `github.com/itdoginfo/allow-domains/releases/latest/download/geosite.dat`
    (about 94.8 KB, **has RUSSIA-OUTSIDE**);
  - geoip: `github.com/hydraponique/roscomvpn-geoip/…` (about 418–430 KB, **has DIRECT**).
- A shunt that names a category missing from the file makes xray refuse to
  start: no proxy mode, and **no nft table at all**. Seen with Loyalsoldier's
  10.5 MB geosite lacking RUSSIA-OUTSIDE, and with an old itdoginfo file lacking
  GOOGLE-MEET.
- The legacy agent's import fallback once wrote Loyalsoldier URLs; fixed to the
  compact sources.

**geoview** (`utils.sh:get_geoip` L153):
`geoview -input geoip.dat -list <codes> [-ipv4=false|-ipv6=false] -lowmem=true -output <file>`,
cached in `/tmp/etc/passwall2_tmp/geo_output/geoip-<code>-ipv4|6`. It is used to
fill the per-rule nft sets and `geoip:` lines of `direct_ip`.

**Replacement must**
- Refresh geo files on a schedule, verify them, and swap atomically.
- Never install a file that lacks a category the routing names (vctl's
  `xray -test` gate covers this).
- Rebuild kernel sets from the new file within its memory budget.

---

## 9. Supervision, cron, hotplug, fw4 include, boot — and the fleet's add-ons

### 9.1 PassWall's own loops

- **`monitor.sh`.** Sleeps 58 s, then every 58 s re-runs, with `nohup`, any
  command recorded in `/tmp/etc/passwall2/script_func` whose
  `pgrep -f "<cmd>"` finds nothing (xray, chinadns-ng, dnsmasq_default).
  - There is no backoff, no health check beyond "process exists", and no
    `xray -test`.
  - It is started only when `start_daemon=1`.
- **`tasks.sh`** runs every 600 s, only for `*_week_mode=8`.
- **`lease2hosts.sh`** runs every 60 s and copies DHCP leases to
  `/tmp/etc/passwall2_tmp/dhcp-hosts`, then HUPs the copies (see 5.6).
- **`socks_auto_switch.sh`** does per-socks-section failover (unused on the
  fleet).

### 9.2 Cron on a fleet router [obs]

| Line | Owner |
|---|---|
| `00 0 * * * lua /usr/share/passwall2/subscribe.lua start <sub> cron …` | PassWall; exists only while started |
| `0 6 * * * lua /usr/share/passwall2/rule_update.lua log all cron` | PassWall; exists only while started |
| `*/5 * * * * /usr/sbin/vectra-controller-watchdog` | legacy agent |
| `30 4 * * * sleep 70 && touch /etc/banner && reboot` | fleet image; daily reboot, and `/tmp` is wiped |

### 9.3 Boot

The first ~60 s after init are direct (`start_delay`). The ready lock gates the
hotplug.

### 9.4 Iface hotplug `/etc/hotplug.d/iface/98-passwall2` — dead in 26.8.10

- Intended: on ifup of the default-route device, restart PassWall; on ifupdate,
  run `nftables.sh update_wan_sets`.
- In practice the gate is `get_cache_var ENABLED_DEFAULT_ACL/ENABLED_ACLS`, and
  **app.sh never caches those variables** (grep: no `set_cache_var` for either).
  So it never fires. Also confirmed on the data-plane stand (ICM 2026-09-29).
- WAN changes therefore reach PassWall only through the fw4 include.

### 9.5 fw4 include `/var/etc/passwall2.include`

On every firewall reload, e.g. an interface ifup:
- `nft -f /tmp/etc/passwall2/PSW2_RULE.nft` if `mangle_prerouting` has no
  `PSW2` rule. That file is the full table snapshot taken at start, sets
  included.
- Then `nftables.sh update_wan_sets`.

A normal fw4 reload does not touch `table inet passwall2`, so this is usually
only the WAN-set refresh [inf].

### 9.6 Fleet add-ons around PassWall (legacy agent package, `router/vectra-controller-agent/openwrt/files`)

- **`vectra-xray-wrapper`:** `GOMEMLIMIT=80MiB`, `GOGC=30`.
- **`vectra-oom-guard`:** every 15 s sets `oom_score_adj` — agent −800, xray
  −500, chinadns-ng −300, dnsmasq_default −200.
- **Low-RAM profile:** `91_vectra_low_mem_profile` applies the sysctls
  (`min_free_kbytes` 24576) and points `xray_file` at the wrapper when it is
  still `/usr/bin/xray`. It is sometimes under-applied
  (`reference_xray_lowmem_health_checks.md`).
- **zram swap** of about 117 MB is the fleet standard (`reference_router_lowmem_zram.md`).
- **Controller watchdog (cron).** Restarts the agent. Newer builds add a
  **dead-man**: with no panel contact for more than 900 s while passwall2 is
  enabled, it fails safe to direct
  (`project_nataliafilisiti_nonshunt_global_node_deadlock.md`; not in this
  worktree's copy of the script).
- **Rescue.** Direct mode is `passwall2.@global[0].enabled=0` plus a restart
  (`reference_operator_direct_mode_is_silent.md`, `reference_stale_rescue_direct_recovery.md`).

**Replacement must**
- Supervise with backoff and health, not just "process exists".
- React to WAN changes, or not depend on WAN addresses.
- Survive a firewall reload or restart.
- Take over the memory protections.
- Own the fail-safe to direct.

---

## 10. `test.sh` and how the fleet probed nodes

### 10.1 `url_test_node <nodeId>`

1. Runs `app.sh run_socks` for that single node on 127.0.0.1:<first free port
   from 48900>, with `NO_REC_PROCESS=1`: a **second xray process**, outbound
   mark 255.
2. Sleeps 2 s.
3. `curl --connect-timeout 3 --max-time 5 -I -skL -x socks5h://… <global_other.url_test_url or google generate_204>`.
4. Prints `http_code:time_pretransfer` and kills and removes everything.
5. It works even with PassWall off.
6. **Any extra argument (a URL) is ignored.** To test a site, set
   `url_test_url` temporarily and restore it with `uci delete`
   (`reference_url_test_node_url_arg_ignored.md`). LuCI passes a dummy second
   argument too.

`test_url`/`test_proxy` are generic curl helpers (google 204, then baidu, then
ping 223.5.5.5).

### 10.2 Fleet use [obs]

- The panel's `verify_passwall_routes` runs `url_test_node` for each slot's
  node. The verdicts feed the node-health ledger, the check-in directive and the
  subscription rescue (`project_selfrepair_chain_2026_08_24.md`,
  `project_selfrepair_chain_closed_2026_09_22.md`).
- It is also the operator's triage tool.

### 10.3 Probe traps [obs]

- **TCP reachability is not a data plane:** tcping 9.7 ms on a node that
  carries nothing (`reference_tcp_reachable_node_can_be_dataplane_dead.md`).
- **The socks path is not the client path:** curl through :1070 does not
  reproduce TPROXY plus the domain rules (`reference_socks_path_does_not_reflect_client_routing.md`).
- **`pgrep -x xray` lies under the wrapper;** check `/proc/*/comm` or run a url
  test (`reference_xray_lowmem_health_checks.md`).
- **A green YouTube probe does not mean video plays:** googlevideo is not probed
  (`reference_youtube_probe_blind_to_video_path.md`).
- **"Reachable" is not "carries the slot's services":** a node can carry
  Telegram but not Instagram (`reference_ru_entry_auto_exit_carries_telegram_not_instagram.md`).
- **Cost:** each url test is an extra xray, about 40 MB peak. Low-RAM guards
  refuse diagnostic jobs under about 40 MB available
  (`project_andrey_avito_onboarding.md`).

**Replacement must:** offer a per-node (per-slot) end-to-end probe that goes
through the real outbound, with a selectable target URL and a bounded memory
cost, because the panel's self-repair depends on those verdicts.

---

## 11. Fleet config as observed on 1111 (2026-09-29)

Measured on 1111 while it still ran PassWall2 26.8.10. The package's
`xray-core` record was 26.4.25 while the binary was 26.7.28
(`project_universal_installer_pro_canary.md`). Since 07:33 MSK that day 1111
has run vctl, with PassWall switched off and its package kept as a rollback.

**Global node** `myshunt`: `default_node _direct`, `domainStrategy IPOnDemand`.

**Shunt rules, in UCI order**

| # | Rule | Match | Bound to | FakeDNS |
|---|---|---|---|---|
| 1 | `direct` | ip_list `geoip:DIRECT`; domain_list 5 entries incl. `geosite:russia-outside` | `_direct` | — |
| 2 | `WorldProxy` | 3427 IP entries + 58 domain entries (many geosite categories) | a node | yes |
| 3 | `YouTube` | `geosite:YOUTUBE` | a node | yes |
| 4 | `Special` | 11 domains | a node | yes |
| 5 | `Tiktok` | `geosite:TIKTOK` | a node | yes |
| 6 | `DiscordVoiceUdp` | udp, ports 19294-19344,50000-50100, no lists | a node | — |

**`global_forwarding`:** `tcp_proxy_way redirect`, `tcp_redir_ports`/`udp_redir_ports 1:65535`,
`no_redir disable`, `accept_icmp 0`, `ipv6_tproxy 0`.

**DNS**
- `remote_dns_protocol doh`, `remote_dns_query_strategy UseIPv4`,
  `direct_dns_query_strategy UseIP`.
- `direct_ip` = the 13 private ranges.
- `dnsmasq_default` on :11400; chinadns-ng on :15354 with `default-tag chn`,
  upstream 8.8.8.8 in plain UDP; xray FakeDNS for the shunt domains.
- nft redirects both LAN and router-own :53 to 11400.
- The ISP forges NXDOMAIN for Instagram/YouTube even toward 8.8.8.8/1.1.1.1
  (`project_isp_forges_dns_even_public_resolvers.md`).

**Generated xray**
- The `direct` outbound is freedom with `finalRules:[{"action":"allow"}]` and
  `domainStrategy UseIP`.
- `policy.levels.0.bufferSize 0`.
- `fakedns` 198.18.0.0/16, poolSize 65535.
- Inbound sniffing http,tls,quic,fakedns with routeOnly.

**nft**
- Per-rule sets `psw2_myshunt_<rule>`.
- The `direct` set returns before REDIRECT/TPROXY, i.e. **geoip:DIRECT bypasses
  xray in the kernel**.
- FAKE_IP 198.18.0.0/16 goes to the proxy chain.

**After PassWall's stop:**
- `table inet passwall2` stayed, with its four base chains (dstnat,
  mangle_prerouting, mangle_output, nat_output) and every `psw2_myshunt_*` set;
- `mangle_prerouting` held **three** copies of
  `socket transparent 1 meta mark set 0x50535732 accept`, one per start/stop
  cycle, marking every TPROXY'd packet of whatever ran next.

Derived from the code for these values [inf]:
- REDIRECT/TPROXY port 1041, xray dns-in 15353;
- the xray DNS server list and dns-out rules of §5.3;
- the remote DoH server is configured but referenced by no rule.

---

## 12. Known failure modes and incidents (fleet notes)

| # | Failure | PassWall mechanism behind it | Note(s) |
|---|---|---|---|
| 1 | Subscription stub wipes all nodes, then stays wiped | stub applied without a check; md5 lock; headers only with `hwid=1` (26.7.16+) | `project_subscription_hwid_gate_2026_08_07.md`, `reference_passwall_hwid_gate_version_skew.md`, `feedback_passwall_upgrade_preflight_mandatory.md` |
| 2 | Subscription dies silently, job "succeeds" | UA `passwall2` + nil `get_version()`; exit code 0 | `project_passwall_ua_nil_version_crash.md`, `project_yuranrod_hwid_not_recognized_2026_08_24.md` |
| 3 | Nightly node update dead for weeks | cron built only from `update_week_mode`; renamed options | `project_passwall_26_7_16_killed_subscription_cron.md` |
| 4 | Upgrade removes xray; PassWall "running", nodes skipped | `xray-core` dropped from Depends; opkg autoremove | `project_passwall_26_8_10_drops_xray_dependency.md`, `project_passwall_26_8_10_removed_xray_incident_2026_09_15.md`, `project_passwall_26_8_10_rollout_2026_09_15.md` |
| 5 | xray refuses to start after an app upgrade or a core swap | `version.min 26.3.27`; version-dependent schema (§4.5) | `project_passwall_upstream_xray_skew.md`, `project_andrey_avito_onboarding.md` |
| 6 | Wrapper clobbered; a second 30 MB binary on overlay | LuCI/builtin updater writes to `xray_file` | `project_xray_builtin_updater_wrapper_clobber.md`, `project_xray_runtime_target_unreachable_via_package.md` |
| 7 | Fresh unit: whole VPN dead | geosite lacks a referenced category → "no proxy mode", no nft table | `reference_geosite_noproxy_fresh_unit.md` |
| 8 | "Everything but Telegram", or everything through one node | global node is a plain node, not the shunt | `reference_global_node_must_be_shunt.md`, `project_nataliafilisiti_nonshunt_global_node_deadlock.md` |
| 9 | Router strands after an apply | `default_node` = a proxy; router's own TCP goes through xray | `feedback_shunt_default_node_direct.md` |
| 10 | Slots remapped, out_of_sync, restart blip | `update_node` deletes and re-creates nodes and re-points slots by heuristic | `reference_needs_sync_out_of_sync.md`, `reference_route_policy_label_host_and_selfheal.md`, `project_route_policy_never_self_heals.md` |
| 11 | Live config hours behind UCI | restart after subscribe not effective | `reference_midnight_subscribe_stale_xray_config.md`, `reference_subscription_cron_time_lives_on_device.md` |
| 12 | Discord voice dead fleet-wide | UCI rule order = routing order; WorldProxy IPs catch Discord UDP; XUDP needed on that node | `project_discord_voice_xudp_2026_08_03.md` |
| 13 | Telegram drops on 1111 | IPv6 WAN flap (see §6.7 for the mechanism) | `project_1111_ipv6_wan_firewall_reload.md` |
| 14 | Proxied domains black-holed although the wrapper "exists" | `xray_file` wrapper present, real binary missing: interception installed instead of no-proxy mode [obs; mechanism unverified] | `project_andrey_avito_onboarding.md` |
| 15 | Two proxy stacks fighting after takeover | PassWall is an independent service; subscribe and LuCI restart it despite rc.d; the legacy watchdog revives the agent | `project_vctl_legacy_watchdog_conflict.md`, `project_vctl_handover_stops_agent_not_stack.md`, ICM 2026-09-28/29 |
| 16 | Leftover mark rules and sets after PassWall stops | `del_firewall_rule` greps `PSW2_`; table never deleted | §3.4, CHANGELOG 0.6.0-r16 |
| 17 | DNS dead after a DNS setting change | invalid DoH URL (a DoT host) as remote DNS | ICM (hh, 2026-05-02); suspected in `project_1111_ipv6_wan_firewall_reload.md` |
| 18 | Blocked names resolve to NXDOMAIN | plain-UDP direct resolver path | `project_isp_forges_dns_even_public_resolvers.md` |
| 19 | Slot on a dead or retired provider host | node set churns nightly; retired hosts still served | `project_provider_retired_ru9_ru12_2026_08_24.md`, `project_ru7_ru11_retired_still_served_2026_09_06.md`, `reference_force_subscription_refresh_via_md5_reset.md` |
| 20 | False health verdicts | url_test ignores the URL; tcping, socks-path, pgrep and panel probes mislead | §10.3 notes |
| 21 | OOM / xray killed on 234 MB routers | no memory control in PassWall; the fleet adds wrapper, oom-guard, zram | `reference_router_lowmem_zram.md`, `reference_xray_lowmem_health_checks.md`, `project_vctl_memory_r15.md` |
| 22 | Customer's LuCI edits undo the config | the global node, DNS and "update components" are all one click away | `project_andrey_avito_onboarding.md` |
| 23 | Operator "direct" invisible in the panel | direct = PassWall switch off, not reported as a mode | `reference_operator_direct_mode_is_silent.md`, `reference_stale_rescue_direct_recovery.md` |

Provider-side, not PassWall, but it shapes the slot policy any replacement must
carry:
- RU-entry Telegram blocks (`project_telegram_ru_entry_block_2026_08_02.md`);
- shared-egress Instagram overload (`project_instagram_shared_exit_overload.md`,
  `project_ru_entry_cloudflare_egress_instagram.md`);
- the current canon, "Germany via bridge" `ru*:50052` (`project_fleet_canon_ru_entry_germany_2026_09_27.md`).

---

## 13. Replacement checklist: what vctl must provide or consciously drop

State of vctl 0.6.0-r16 (`5c2a657`), from a code survey of this repo. Paths are
relative to `router/vectra-controller-pro/`.

**Update, 0.6.0-r17** (the rows below are r16's; these changed):
- #6 reply direction: HAVE — `ct direction reply return` in prerouting and
  forward; port forwards proven on the stand (TCP and UDP, the kill switch
  armed and not), with a control that breaks without it.
- #20 LAN :53 to other resolvers: HAVE — `Spec.HijackDNS`, queries to public
  resolvers redirected to the router while its DNS goes through the tunnel
  and something serves :53 (`dns_hijack '0'` off).
- #26 generator dependency, #31 node refresh, #33 stub feeds, #35 link
  parsing, #36 geo refresh: HAVE in `route_source 'native'`
  (`internal/routepolicy`, `cmd/vctl/native_source.go`, `native_geo.go`) —
  PassWall's generator and `subscribe.lua` ported and proven against their
  own output; the placeholder refused; a vanished node's slot re-pointed by
  the owner's rule (same country, then the main slot's node, then «Авто»).
  PassWall2's packages can be removed (docs/CANARY.md §7).

**Status:**
- **HAVE:** implemented.
- **PARTIAL:** implemented in part.
- **MISSING:** a gap, needs a decision or work.
- **DROP:** not needed on the fleet; dropping it is fine, but say so.

### Data plane

| # | Mechanism (PassWall) | vctl | Evidence / what is missing |
|---|---|---|---|
| 1 | Capture of LAN TCP+UDP (REDIRECT/TPROXY :1041, mark 0x50535732, table 999) | HAVE | TPROXY for TCP and UDP to :12345, fwmark 0x1, table 100, `table inet vctl` — `internal/firewall/nft.go` |
| 2 | Router's own traffic through the proxy (`localhost_proxy`) | HAVE | output chain marks router-originated flows. The `ct mark` rule before the `established` return keeps established TCP on the path (nft.go L542). Router DNS/NTP go direct by design |
| 3 | Never capture reserved or router-local destinations | HAVE | `bypass4/6` + `fib daddr type local/broadcast/multicast return` |
| 4 | Never capture node servers (`psw2_vps`) | PARTIAL | xray's own egress is exempt by SO_MARK 0x5644. A LAN client talking to a node IP is captured [inf: harmless] |
| 5 | LAN subnet / ISP DNS / non-RFC1918 LAN in the direct set | MISSING | bypass is the static reserved list only; IPv6 GUA LAN prefixes and non-RFC1918 LANs are not covered |
| 6 | `ct direction reply return` before interception | MISSING | vctl prerouting has no reply/iifname guard. [inf] Port forwards and WAN-initiated flows may be captured — verify on the stand |
| 7 | Kernel bypass of `_direct` IP lists (geoip:DIRECT) | HAVE | r15/r16 `vctl_direct4/6`, ct bit 0x10000000, memory budget — `cmd/vctl/direct_bypass.go`, `internal/geodat`. The scan stops at the first non-direct rule |
| 8 | Kernel bypass of direct-DNS answers (`write_ipset_direct`, chinadns-ng `white` set) | MISSING | `internal/dns/dnsmasq.go` renders `nftset=` but is dead code. Decide: add, or accept that domain-direct traffic runs through xray (~38 KB heap per connection, `project_vctl_memory_r15.md`) |
| 9 | FakeDNS pool captured | HAVE | PassWall mode, `cmd/vctl/dns_steer.go:carryFakeDNS` |
| 10 | `socket transparent` fast path | DROP | not needed for correctness [inf] |
| 11 | IPv6 | different | PassWall on the fleet proxied no IPv6 (and wan6 is off); vctl proxies IPv6. Keep the v6-reply fix (`project_vctl_v6_replies_blackholed.md`) |
| 12 | ICMP not proxied | HAVE | parity; the kill switch counts and drops |
| 13 | Policy-route self-repair | MISSING (both) | a lost `ip rule` is only counted (`vctl_tproxy_escaped`); PassWall re-adds it only at restart |
| 14 | Survive fw4 reload/restart (PassWall: include + snapshot) | PARTIAL | vctl's own table is untouched by a fw4 reload [inf]; the `fw4 restart`/`flush ruleset` case is unverified |
| 15 | mwan3 hook; bridge-nf sysctl | DROP | no mwan3 on the fleet [inf]. vctl's `ct mark set 0x1` overwrites the whole ct mark |
| 16 | Per-client ACL; LAN socks :1070 / http inbounds | DROP / MISSING | ACL unused. Socks :1070 was used by probes and `curl_proxy`; vctl drops provider socks/http inbounds, so anything still using :1070 must move |
| 17 | Remove PassWall leftovers at takeover | HAVE | r16 `stop_passwall_stack`: switch off with breadcrumb, stop, verify, `nft delete table inet passwall2`. The ip rule 999 and dnsmasq restore come from PassWall's own stop |
| 18 | PassWall stop SIGKILLs any cmdline containing `passwall2/` | watch | a concurrent PassWall stop would kill vctl's `lua …/luci/passwall2/util_xray.lua` generator run [inf] |

### DNS

| # | Mechanism (PassWall) | vctl | Evidence / what is missing |
|---|---|---|---|
| 19 | Blocked names resolved without ISP forgery | HAVE (better) | r8: dnsmasq's upstream → `vctl-dns-in` 127.0.0.1:10053 → xray DNS through the tunnel |
| 20 | Hijack of LAN :53 aimed at other resolvers (`dns_redirect=1`) | MISSING (by design) | such queries are TPROXY'd as ordinary traffic. Decide whether PassWall's enforced view is needed |
| 21 | FakeDNS for listed domains | HAVE | PassWall's own dns section, in PassWall mode |
| 22 | Node host names resolved outside the tunnel | HAVE | `tcp+local` 8.8.8.8 / 77.88.8.8 / WAN resolvers — `internal/coreengine/xray/dns_steer.go` |
| 23 | HTTPS RR (qtype 65) suppressed | PARTIAL | no explicit rule; the code comment expects xray to answer non-A/AAAA itself. Verify the answer is empty |
| 24 | Other qtypes (MX/TXT/SRV/PTR) | verify | PassWall forwarded them to the direct resolver |
| 25 | LAN host names (`*.lan`) | HAVE (better) | vctl leaves dnsmasq serving the LAN |

### xray configuration

| # | Mechanism (PassWall) | vctl | Evidence / what is missing |
|---|---|---|---|
| 26 | Routing from the operator's shunt policy (rule order, domain/IP split, port-only rules, FakeDNS, balancers) | HAVE, borrowed | r12 `route_source 'passwall'` runs PassWall's own `util_xray.lua gen_config` + `AdaptPassWall`. **Needs luci-app-passwall2 and its Lua/LuCI deps installed and a maintained `/etc/config/passwall2`.** There is no native generator, and the owner expects future routers to ship without PassWall2 (ICM 2026-09-29) |
| 27 | Schema matched to the running xray | verify | PassWall's generator detects the version itself (md5 cache); it must see the binary vctl runs |
| 28 | `xray run -test` before switching | HAVE | skipped below max(24 MiB, 10% RAM) |
| 29 | Outbound mark distinct from TPROXY mark | HAVE | 0x5644 vs 0x1 (`project_vctl_tproxy_mark_collision.md`) |
| 30 | `finalRules allow` treated as plain freedom | HAVE | r15 |

### Subscriptions and geo

| # | Mechanism (PassWall) | vctl | Evidence / what is missing |
|---|---|---|---|
| 31 | **Nightly node refresh + boot refresh** | **HAVE in native mode (r17)**; MISSING in PassWall mode | Native: vctl refreshes on the policy's own schedule, `boot_update` at its first loop, a failed refresh changes nothing (`cmd/vctl/native_source.go`). PassWall mode: vctl skips its own refresh; the `refresh_subscriptions` job only re-renders from existing UCI (`cmd/vctl/cmd_agent_jobs.go`); PassWall's cron line is removed by the takeover's stop. **Nobody refreshes nodes.** Provider mode: HAVE, every 6 h plus a job, JSON only |
| 32 | HWID/device headers | HAVE | `internal/subscription/fetcher.go:ComputeHWID`; signed UA (`project_vctl_signed_router_ua.md`) |
| 33 | Refuse a stub/placeholder feed | HAVE in native mode (r17) | Native: a feed with no usable node (the placeholder "App not supported" @0.0.0.0:1) is refused, nothing changes, asked again after 2 h (`routepolicy.ErrEmptyFeed`). Provider mode: PassWall had no such check; vctl keeps the running config on failure, but stub **content** detection is unverified |
| 34 | Unchanged-feed no-op + forced refresh | HAVE in native mode (r17) | Native: PassWall's own feed md5 is the no-op; the panel's refresh job forces. Provider mode: sha256 digest no-op; no explicit force path found |
| 35 | Link-list (base64 `vless://`) parsing | HAVE in native mode (r17, vless) | Native: subscribe.lua's parsing of vless links (`internal/routepolicy/links.go`); another scheme is skipped and counted, never guessed at. Provider mode: vctl refuses link-list mode. It depends on the provider keeping the JSON feed |
| 36 | **Scheduled geo refresh** (06:00, sha256, backup) | **HAVE in native mode (r17)** | Native: the policy's schedule, its `geoip_update`/`geosite_update` (r18), the published sha256, and every category the policy routes by checked before a file is taken (`cmd/vctl/native_geo.go`). Otherwise: `update_xray_assets` job only; `updateSchedule`/`updateOnStart` are never read. In PassWall mode nobody updates `/usr/share/v2ray` |
| 37 | geoip → kernel sets | HAVE | own geoip.dat parser |

### Supervision, health, control plane

| # | Mechanism (PassWall) | vctl | Evidence / what is missing |
|---|---|---|---|
| 38 | Process watchdog | HAVE (better) | supervisor backoff, procd respawn, per-minute dead-man cron |
| 39 | Memory protection (the fleet's add-ons) | HAVE | memwatch, GOMEMLIMIT 22% RAM, OOM adj −800/0/+500 (`cmd/vctl/memwatch.go`, `internal/memguard`). zram stays a platform setting |
| 40 | WAN iface events | N/A | PassWall's hotplug is dead code; vctl uses `fib daddr type local` |
| 41 | **Per-slot end-to-end node probe** (`url_test_node`) feeding the panel's self-repair | **MISSING** | vctl rescue probes gstatic/cloudflare only; no per-node probe with a target URL |
| 42 | Fail-safe to direct | HAVE | rescue policy, kill switch (default off), dead-man. PassWall failed open silently |
| 43 | **Route policy from the panel** (slot → exit bindings, self-heal) | **MISSING** | vctl ignores `routePolicy`; the legacy agent that applied it is disabled; `/etc/config/passwall2` changes only via `run_terminal_command` [inf] |
| 44 | Panel apply of the full PassWall config (legacy `apply.go`) | **MISSING in PassWall mode** | operator config covers vctl's own blocks only |
| 45 | Telemetry the panel's monitoring reads (slot verdicts, service probes) | PARTIAL | check-in contract pinned (`reference_vctl_panel_checkin_contract.md`); PassWall-specific fields have no source |

### Gaps, most important first

1. **Nightly node refresh (#31).**
   - In PassWall mode nothing runs `subscribe.lua` after the takeover.
   - The node list freezes at takeover time while the provider re-mints hosts
     nightly.
   - Whoever adds it must also bring:
     - stub detection (#33);
     - HWID headers;
     - `hwid=1`;
     - slot stability across id churn;
     - a re-render/restart that actually happens.
2. **Route policy / slot bindings from the panel (#43, #44).**
   - Without the legacy agent there is no channel for the panel's exit choices
     and self-heal.
3. **Geo refresh schedule (#36).**
   - The files freeze.
   - New categories referenced by the policy will fail `xray -test`: safe, but
     the change is silently not applied.
4. **Per-slot probes (#41).**
   - The panel's node-health ledger and subscription rescue lose their input.
5. **Generator dependency (#26).**
   - PassWall-compatible routing needs PassWall's package and UCI.
   - A PassWall-free router needs a native generator implementing §4 (order,
     split, FakeDNS DNS rules, qtype 65, marks).
6. **Direct-DNS kernel bypass (#8)** and **reply-direction guard (#6)**.
   - The first is a decision on memory versus fidelity.
   - The second is a possible correctness bug to verify on the stand.
7. **LAN DNS enforcement (#20)** and **qtype 65 (#23)**.
   - Both are conscious decisions, not accidents.
