# OpenWrt 24.x + PassWall2/Xray Optimization KB

Status: research report v1  
Created: 2026-05-15  
Scope: Vectra-managed OpenWrt routers where PassWall2 runs an Xray-heavy transparent proxy stack.

## Executive summary

The safest optimization path is not one magic Xray flag. It is a controlled operating model:

1. **Measure first**: RAM, OOM logs, Xray RSS, file descriptors/sockets, conntrack, DNS path, route smoke, and route latency before any write.
2. **Remove waste before tuning**: verbose logs, duplicate DNS chains, unnecessary probes, stale subscription cron, excessive rule surfaces, and unused PassWall components.
3. **Use reversible platform knobs**: packet steering and software flow offload can help the router, but they must be validated per device and workload.
4. **Tune Xray per route, not globally**: Mux/XUDP can help UDP-heavy special cases such as Discord voice, but upstream Xray docs warn that Mux is not a throughput booster and can hurt video/download/speed tests.
5. **Treat hardware offload, FakeDNS, SQM, zram, and sysctl changes as gated experiments**, not defaults.
6. **Do not trust only `/etc/init.d/passwall2 status`**: our own fleet already hit a case where Xray was OOM-killed while the PassWall service still looked green. Runtime proof must include the selected-node Xray process and route smoke.

## Source tiers used

### Primary / official

- OpenWrt docs: flow offloading, SQM, network configuration and packet steering, fw4/firewall, DHCP/DNS, logging.
- Xray docs: outbound Mux/XUDP, FakeDNS, policy/buffer size, logging/sniffing concepts.
- dnsmasq upstream docs.
- PassWall2 upstream repository and local source mirror in this workspace.

### Repo-local source facts

- `passwall2/luci-app-passwall2/Makefile`: PassWall2 depends on `xray-core`, `geoview`, `v2ray-geoip`, `v2ray-geosite`; transparent proxy profiles select `dnsmasq-full` plus ipset/nftset dependencies.
- `passwall2/luci-app-passwall2/root/usr/share/passwall2/0_default_config`: upstream defaults are already conservative in several places: `loglevel=error`, `prefer_nft=1`, `ipv6_tproxy=0`, `sniffing_override_dest=0`, `remote_dns_query_strategy=UseIPv4`.
- `passwall2/luci-app-passwall2/root/usr/share/passwall2/app.sh`: PassWall2 passes `dns_cache` into generated Xray config; unset default is enabled in runtime path.
- `passwall2/luci-app-passwall2/luasrc/passwall2/util_xray.lua`: Xray Mux settings are rendered per node; if `mux=1`, default `mux_concurrency=-1` and `xudpConcurrency=8` unless overridden. Sniffing is enabled for shunt nodes or when `sniffing_override_dest=1`.
- `passwall2/luci-app-passwall2/root/etc/init.d/passwall2`: `reload` is a full restart, not a soft reload.

### Community / operator evidence

Community sources are useful for failure patterns, not as universal presets. The recurring patterns were:

- DNS conflicts are common when PassWall2 redirects port 53 while Adblock/AdGuard/Home or another resolver also expects to own DNS.
- fw4/nftables-era routers should avoid stale iptables-era hacks.
- flow offloading is a forwarding-throughput knob, not a direct Xray CPU fix.
- verbose logs and long-lived proxy sockets are common low-RAM failure amplifiers.
- some PassWall2 fw4 “Save & Apply” paths need explicit service restart verification.

## Safe action matrix

### Green: safe defaults / should become baseline policy

| Action | Why | Risk | Verification |
|---|---|---:|---|
| Keep PassWall2/Xray log level at `error` or `warn`; never run debug/info fleet-wide | Logs live in RAM by default on OpenWrt and verbose Xray logs waste CPU/RAM | Low | `uci get passwall2.@global[0].loglevel`; `logread`; check `/tmp/log/passwall2*.log` growth |
| Keep DNS cache enabled unless diagnosing DNS correctness | Reduces repeated upstream resolver work | Low | Compare DNS latency and `logread`; confirm no stale cache symptom |
| Keep one DNS owner/chaining model | Avoids PassWall2/dnsmasq/adblock resolver loops and hidden bypass | Low/Medium | `netstat -lnup | grep ':53'`; `uci show dhcp`; test domain route decisions |
| Prefer fw4/nftables-native path on OpenWrt 22.03+ | Current OpenWrt uses fw4/nftables; PassWall2 selects nftables deps when firewall4 is present | Low | `fw4 print`; `nft list ruleset`; `uci get passwall2.@global_forwarding[0].prefer_nft` |
| Keep `ipv6_tproxy=0` unless IPv6 proxying is explicitly required and tested | IPv6 TPROXY adds complexity and route surface | Low | `uci get passwall2.@global_forwarding[0].ipv6_tproxy`; IPv4/IPv6 route smoke |
| Keep native PassWall subscription auto-update disabled for Vectra-managed subscriptions | Native refresh can recreate nodes and drift shunt bindings away from panel intent | Low | `uci show passwall2 | grep auto_update`; re-import diff after refresh |
| Validate selected-node runtime, not only service status | Xray can be gone while service wrapper remains green | Low | process scan for generated global config + `url_test_node` + HTTP smoke |
| Reduce route/rule count before changing kernel sysctls | Fewer rules and less shunt/domain surface reduce lookup cost and config generation cost | Low | generated Xray config size, route count, restart time, RSS before/after |

### Yellow: controlled experiments per router/workload

| Action | Expected benefit | Risk / tradeoff | Rollback |
|---|---|---|---|
| Enable packet steering on multicore routers | Better use of multiple cores for packet processing | Little benefit on single-core/interrupt-bound routers; can shift latency profile | `uci set network.globals.packet_steering='0'; uci commit network; /etc/init.d/network reload` |
| Enable **software** flow offload | Lower CPU for plain forwarded/NAT traffic | Transparent proxy/local Xray path still costs CPU; can change debugging/metrics | Disable `firewall.@defaults[0].flow_offloading` and restart firewall |
| Test `irqbalance` or manual IRQ affinity | Better multicore IRQ spread under high pps | Can make latency worse on some SoCs; needs before/after measurements | Stop/disable package or revert affinity |
| Test SQM only if latency/bufferbloat is the problem | Better latency under saturated WAN | CPU-heavy; incompatible with hardware flow offload; may reduce throughput | Disable SQM service |
| Test zram-swap on very low-RAM routers | May survive transient bursts/OOM | CPU overhead; not a cure for leaks; can hide bad configs | Disable zram-swap package/service |
| Tune Xray Mux/XUDP per node | Can help UDP-heavy or high-handshake workloads | Mux is not for speed tests/video; may worsen throughput | Remove per-node mux/xudp extras |
| Test FakeDNS only for clear transparent-proxy DNS pain | Can improve domain recovery/latency | Cache pollution, wrong FakeIP reversion, memory pool overhead | Disable FakeDNS and restart PassWall2 |
| Lower Xray buffer size if exposed later | Less per-connection memory | Too small can hurt throughput/UDP; not currently a Vectra baseline knob | Restore default |

### Red: do not make default

- Hardware flow offload fleet-wide. It can bypass QoS/SQM and is platform-specific.
- Random `sysctl` recipes from forums (`overcommit`, tiny TCP buffers, forced drop-caches cron). These often trade visible OOM for hidden instability.
- Replacing `dnsmasq-full` with base `dnsmasq` on PassWall2 transparent-proxy routers unless the exact features used by that router no longer require nftset/ipset integration.
- Killing LuCI/controller/dnsmasq as an “optimization”. It may save RAM but destroys manageability and recovery lanes.
- Enabling FakeDNS or sniffing override globally without route-by-route validation.
- Blindly enabling Xray Mux for all nodes.
- Running heavy geo/subscription updates during peak or on low-memory routers without resource guards.

## Measurement lane before any optimization

Run this read-only baseline first on each router class. For Vectra, this should become a controller diagnostic job.

```sh
# Identity and resource baseline
ubus call system board
grep -E 'DISTRIB_(RELEASE|ARCH)' /etc/openwrt_release
uptime
free -m
cat /proc/meminfo | grep -E 'MemTotal|MemFree|MemAvailable|Buffers|Cached|SwapTotal|SwapFree|Slab|SReclaimable|SUnreclaim'
df -h /overlay /tmp

# Processes and memory
top -bn1 | head -40
ps w | grep -E '[x]ray|[s]ing-box|[p]asswall2|[d]nsmasq|[c]hinadns|[g]eoview'
for p in $(pgrep xray); do echo "PID=$p"; grep -E 'VmRSS|VmSize|Threads' /proc/$p/status; tr '\0' ' ' </proc/$p/cmdline; echo; done

# OOM and service symptoms
logread | grep -Ei 'out of memory|oom|killed process|xray|passwall2|dnsmasq' | tail -80
/etc/init.d/passwall2 status 2>/dev/null || true

# Conntrack/socket pressure
cat /proc/sys/net/netfilter/nf_conntrack_count 2>/dev/null
cat /proc/sys/net/netfilter/nf_conntrack_max 2>/dev/null
ss -s 2>/dev/null || netstat -an | wc -l
ls /proc/$(pgrep xray | head -1)/fd 2>/dev/null | wc -l

# PassWall2 config surface
uci -q show passwall2.@global[0]
uci -q show passwall2.@global_forwarding[0]
uci -q show passwall2.@global_xray[0]
uci -q show passwall2.@global_rules[0]
```

Success criteria for “optimization helped”:

- `MemAvailable` improves or stays stable under the same traffic.
- Xray RSS/fd/socket count does not creep upward over 30–60 minutes of realistic load.
- No new OOM lines.
- `url_test_node` still returns `204`/`200` for all active shunt slots.
- Telegram/YouTube/Discord or customer-specific probes remain green.
- Latency does not regress under load.

## Recommended Vectra roadmap

### Phase 1 — zero-risk observability

Add or confirm controller telemetry for:

- selected Xray PID/RSS/threads/fd count;
- generated global config marker in `/proc/*/cmdline`;
- `MemAvailable`, `/tmp`, `/overlay`, swap/zram status;
- OOM log tail;
- conntrack count/max;
- dnsmasq process and port-53 ownership;
- PassWall2 generated config size and route count;
- last subscription/rules update time;
- service status plus actual route smoke.

### Phase 2 — conservative baseline cleanup

- Enforce `loglevel=error|warn` in panel-generated configs.
- Keep DNS cache enabled.
- Keep native subscription `auto_update=0` for Vectra-owned subscriptions.
- Keep `ipv6_tproxy=0` unless the router is explicitly IPv6-managed.
- Keep `sniffing_override_dest=0` globally; allow shunt-required sniffing only where needed.
- Avoid FakeDNS as a default.
- Move any custom firewall logic to fw4/nftables-native includes or generated PassWall2 config, not manual iptables fragments.

### Phase 3 — per-model experiments

Create a small canary matrix by router class:

| Router class | Try first | Avoid first |
|---|---|---|
| AX3000T / MT7981 / 256 MB+ | packet steering, software flow offload, measured XUDP on Discord route | hardware offload if SQM/QoS is active |
| WR3000/Cudy low overlay/low RAM | log/DNS/rule cleanup, no heavy auto-update, resource-guarded package work | zram/flow-offload before baseline proof |
| Very low-memory / 128 MB | no extra probes, no debug logs, minimal route set, consider zram only after OOM proof | FakeDNS pools, broad geosite refresh during peak |

### Phase 4 — panel automation

Expose an “Optimization profile” in the panel only after the diagnostics exist:

- `safe-default`: logs low, DNS cache on, no native auto-update, no FakeDNS, no IPv6 TPROXY.
- `throughput-canary`: safe-default + packet steering + software flow offload.
- `latency-canary`: safe-default + SQM test, flow offload off/hw offload off.
- `udp-voice`: safe-default + route-specific XUDP/Mux only for DiscordVoiceUdp-like routes.
- `low-memory-survival`: safe-default + reduced probe frequency + optional zram canary.

Every profile must have automatic rollback if route smoke or memory health fails.

## Practical write commands for canary only

These are not defaults. Use one change at a time, then validate.

```sh
# Packet steering canary
uci set network.globals.packet_steering='1'
uci commit network
/etc/init.d/network reload

# Software flow offload canary; keep hardware offload disabled
uci set firewall.@defaults[0].flow_offloading='1'
uci set firewall.@defaults[0].flow_offloading_hw='0'
uci commit firewall
/etc/init.d/firewall restart

# Conservative PassWall logging
uci set passwall2.@global[0].loglevel='error'
uci commit passwall2
/etc/init.d/passwall2 restart
```

Post-change validation:

```sh
free -m
logread | grep -Ei 'oom|killed process|xray|passwall2' | tail -80
/usr/share/passwall2/test.sh url_test_node <selected_node_id>
/etc/init.d/passwall2 status 2>/dev/null || true
ps w | grep -E '[x]ray|[p]asswall2|[d]nsmasq'
```

## Source links

- OpenWrt flow offloading: https://openwrt.org/docs/guide-user/perf_and_log/flow_offloading
- OpenWrt SQM: https://openwrt.org/docs/guide-user/network/traffic-shaping/sqm
- OpenWrt network configuration: https://openwrt.org/docs/guide-user/network/network_configuration
- OpenWrt UCI/network cheatsheet: https://openwrt.org/docs/guide-user/network/ucicheatsheet
- OpenWrt firewall overview: https://openwrt.org/docs/guide-user/firewall/overview
- OpenWrt firewall configuration: https://openwrt.org/docs/guide-user/firewall/firewall_configuration
- OpenWrt DNS/DHCP: https://openwrt.org/docs/guide-user/base-system/dhcp
- OpenWrt logging essentials: https://openwrt.org/docs/guide-user/base-system/log.essentials
- OpenWrt zram-swap: https://openwrt.org/docs/guide-user/additional-software/zram-swap
- dnsmasq upstream docs: https://dnsmasq.org/doc.html
- Xray outbound / Mux / XUDP: https://xtls.github.io/en/config/outbound.html
- Xray FakeDNS: https://xtls.github.io/en/config/fakedns.html
- Xray policy / bufferSize: https://xtls.github.io/en/config/policy.html
- Xray logging: https://xtls.github.io/en/config/log.html
- PassWall2 upstream: https://github.com/Openwrt-Passwall/openwrt-passwall2
- PassWall2 DNS redirect discussion: https://github.com/Openwrt-Passwall/openwrt-passwall2/discussions/776
- PassWall2 fw4 restart issue: https://github.com/Openwrt-Passwall/openwrt-passwall2/issues/996
- PassWall2 fw4 main-switch issue: https://github.com/Openwrt-Passwall/openwrt-passwall2/issues/951
- OpenWrt PPPoE/offload issue: https://github.com/openwrt/openwrt/issues/14365
- Xray high socket/RAM discussion: https://github.com/XTLS/Xray-core/discussions/5719

## Bottom line

For Vectra, the best near-term win is a **safe optimization framework**, not a fleet-wide “performance preset”:

1. Add process/RAM/socket/conntrack/DNS telemetry.
2. Enforce conservative PassWall/Xray defaults.
3. Run packet steering + software offload as canaries by router class.
4. Keep XUDP/Mux route-specific.
5. Avoid FakeDNS/hardware offload/sysctl recipes as defaults.
6. Gate every write with route smoke and rollback.

## Live baseline captured on 2026-05-15

Representative active routers sampled before optimization work:

| Router | OpenWrt | MemAvailable | Xray RSS | Threads | Conntrack | Notable config |
|---|---:|---:|---:|---:|---:|---|
| `1111111111` | 24.10.6 | ~59 MB | ~49.7 MB | 10 | 313/15360 | `remote_dns_protocol=doh`, `loglevel=error`, `ipv6_tproxy=0`, `prefer_nft=1`, `buffer_size=0`; historical OOM log still shows prior `xray invoked oom-killer` on May 11 |
| `VagrandRouter` | 24.10.5 | ~85.8 MB | ~36.1 MB | 8 | 122/15360 | `remote_dns_protocol=doh`, `remote_dns_doh=https://dns.google/dns-query`, `loglevel=warning`, `remote_fakedns=0`, `prefer_nft=1`, `buffer_size=0` |
| `kirill-msk` | 24.10.4 | ~76.3 MB | ~39.6 MB | 8 | 26/15360 | `remote_dns_protocol=doh`, `remote_dns=8.8.8.8`, `remote_dns_doh=https://dns.google/dns-query`, `loglevel=error`, `remote_fakedns=0`, `sniffing_override_dest=0`, `prefer_nft=1`, `buffer_size=0`; swap is present and almost unused |

Implication: the fleet is already on conservative DNS and nftables defaults, so the next optimization pass should focus on **measurement + targeted canaries** rather than broad DNS or firewall rewrites.
