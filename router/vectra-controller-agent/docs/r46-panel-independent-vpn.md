# 0.1.13-r46: a panel outage never turns the VPN off

Branch `agent/watchdog-panel-independent` (from `main` 290f2b6a). Legacy
PassWall agent only (`vectra-controller-agent` + `luci-app-vectra-controller`);
vctl routers are not affected.

## Behaviour before r46 (main 290f2b6a)

Line numbers refer to 290f2b6a.

### Cron watchdog (`openwrt/files/usr/sbin/vectra-controller-watchdog`, every 5 min)

1. Agent binary missing: logs CRITICAL. Process missing: `init.d start`,
   throttled to once per 300 s.
2. Process alive: dead-man (`run_deadman_check`, :335-368). If the router is
   onboarded, `passwall2.@global[0].enabled=1`, and
   `last_successful_control_plane_at` is older than 900 s (:74), or missing
   after a 900 s boot grace, then `should_fail_safe_to_direct` (:264-296)
   returns 0. The watchdog then sets `enabled=0` and restarts passwall2 (:324-330).
   **The only input is panel contact.** Nothing checks whether the proxy works.
3. The watchdog never re-enables PassWall. It leaves that to the agent.

### Agent recovery (`cmd/vectra-controller-agent/controlplane_recovery.go`)

- Every poll (45 s) it probes `/api/health` with the control-plane fwmark and
  a 5 s budget (`collectPanelReachability`, :552). The check-in itself has a
  10 s budget.
- Idle/Monitoring with a failed probe: `SkipControlPlane=true` (:154, :175),
  so **no check-in is attempted**. A successful check-in is the only thing that
  clears the outage (`noteSuccessfulControlPlaneContact`, :518).
- After 15 min (`panel_outage_threshold`), `startControlPlaneRecovery` (:437)
  acts on unmarked probes that go through the proxy:
  - RU reachable and foreign healthy: restart the controller once per outage
    (`controller_restart_wait`). The panel shows this as a "Control plane
    unreachable for over one hour" incident (`router-control.ts:413`).
  - Foreign blocked **or partial** (1 of 3 targets failing is enough, :778-790):
    go direct (`direct_settle`).
- `direct_settle` with a dead panel: reboot if the 12 h budget allows,
  otherwise **stuck** until the panel comes back (:237-251).
- `post_reboot_check` and `passwall_retry_wait`: retry proxy and keep it if
  foreign is healthy or the node answers `url_test_node`. Otherwise go back
  to direct and `operator_attention`.
- `operator_attention`: blind retry every 12 h (`RebootCooldown`) with a dead
  panel. With a live panel, retry only on node proof, also every 12 h (:382-428).
- Every phase in `direct_settle`, `reboot_wait`, `post_reboot_check` and
  `passwall_retry_wait` skips the check-in (main.go:281).
- Local rescue (`evaluateLocalRescue`, rescue_runtime.go:99) runs outside the
  recovery-owned phases and works without the panel. After 3 failures it goes
  direct; after 2 `url_test_node` successes plus a 5-min cooldown it returns
  to proxy. This is how it undoes the watchdog's direct.

### What goes wrong

- **Fleet-wide VPN-off on a panel outage.** Fifteen minutes after the panel
  becomes unreachable, every onboarded router's watchdog disables PassWall.
- **A loop with the agent.** Local rescue sees the node answer and re-enables
  proxy about 90 s later. On the next cron tick the contact is still stale, so
  the watchdog disables it again. This repeats for the whole outage.
- **leonid-avito pattern.** If the 5 s probe fails on a slow or lossy path,
  every check-in is suppressed. A check-in only gets through when a probe
  happens to pass, which was about every 20 min. That gap is more than 15 min,
  so recovery records outages (32 incidents in 2 days) and the watchdog reads
  the stale contact as a strand and turns the router direct.
- A dead panel keeps a recovery-parked router direct either indefinitely
  (`direct_settle`) or for 12 h (`operator_attention`).

## Behaviour in r46

| Piece | Change |
|---|---|
| Watchdog dead-man | Stale contact only arms the switch. It goes direct **only if every candidate node fails `test.sh url_test_node`**. The candidates are the selected node, or each concrete shunt slot, the same set as `proxyNodeReachableForRecovery`. If a node answers, or the result is inconclusive (no test.sh, virtual selection, MemAvailable < 40 MB), the proxy stays on. Decisions are logged once per state change. |
| Watchdog return path | The direct it creates is owned through `/etc/vectra-controller/watchdog-direct`, which persists across reboot and sysupgrade. The watchdog keeps it until a node answers `url_test_node`, then restores proxy. This holds whatever the panel contact says, including fresh contact, a clock behind after reboot, a missing timestamp in boot grace, or threshold 0. An ISP drop therefore comes back within about 15 min of the switch. Ownership passes to the agent only when it writes the main switch on purpose (rescue, recovery, resume, the operator's `enter_direct_mode`; the agent deletes the marker), or when its recovery parks the router. The watchdog never touches a direct it did not create. The install hook drops a stale marker when PassWall is on. |
| Watchdog vs agent | It does nothing while the agent is in `passwall_retry_wait` with a retry younger than 15 min. It flips PassWall at most once per 15 min and reaches at most one proxy verdict per 15 min. `url_test_node` runs under `/var/run/vectra-url-test.lock`, which the agent shares (`internal/passwall/url_test_lock.go`). Without it, test.sh kills every `url_test_<node>` process when it finishes, so two probes of one node would read each other as dead; a busy lock makes the node unjudged. A probe is killed after 30 s without needing `timeout` (BusyBox has no such applet). Its leftover `url_test_<node>` xray and config are reaped. Cron runs are serialized by a lock that is released only by its owner. Cron install is idempotent (tested). |
| Agent direct fallback | Under a panel outage it is vetoed while the node answers `url_test_node`. A genuinely dead node still goes direct. |
| Agent way back | `direct_settle` with a dead panel, and `operator_attention` with any panel state, retry proxy on node proof every 15 min. The blind dead-panel retry keeps its 12 h interval. The node verdict is cached for 5 min, so pollers do not start an xray every 45 s. |
| Check-ins | `SkipControlPlane` is now advisory. A paced check-in still goes out every 2 min, backing off to 4 min and then 5 min after failures. It is never sent during `reboot_wait`, inside a settle/warmup window (`direct_settle` 45 s, `passwall_retry_wait` 75 s, `post_reboot_check` 4 min), or in the tick that just switched PassWall, because a delivered job could restart PassWall under the phase's measurement. Recovery state is saved before the call. Paced check-ins also skip the shunt/route-policy self-heals. |

Panel unreachability can now only cause the agent's own logging, a controller
restart, and `server_unreachable` incidents. It can no longer turn the VPN off.

Tests:

- Go: `panel_independence_test.go`, `checkin_pacing_test.go`,
  `watchdog_ownership_test.go` and `internal/passwall/url_test_lock_test.go`.
- Shell: `openwrt/tests/watchdog_deadman_test.sh`, 106 cases under sh, dash
  and bash. It runs from `go test` via `watchdog_script_test.go`. The recovery and watchdog tests were
checked by mutation: with the proof gates removed they fail.

## Rollout plan

The release is built only from `main`, after this PR merges with CI green.
r40 is missing from `hotfix/prod-base`, so building from that branch would
roll the fleet back to r39 behaviour.

### 0. Build and publish to **beta**, not stable

Follow `reference_controller_feed_publish_lane`:

- SDK 24.10.6, key `controller-r9-keys`
- `vectra.pub` must match the published key byte for byte
- `strings` on the binary shows `0.1.13-r46`
- publish `--channel beta`
- leave stable on r45, so `update controller --channel stable` remains the rollback

Back up the current feed dir before syncing.

### 1. Exclusions (every wave)

- `hh`: never touched. It sits in the solo group «Особая группа»; list it in
  the report as "excluded by policy".
- `1111111111` and every vctl/Connect router: the agent is disabled there, so
  they are out of scope.
- Routers with a queued or running job: keep to one terminal job per router,
  and wait for your own `jobId` plus a unique marker in the command.

### 2. Pre-flight per router (read-only, from `fleet.monitoring`)

- MemAvailable ≥ 48 MB, /overlay free ≥ 8 MB, /tmp free ≥ 16 MB. Below these
  the update job exits with code 72; that is intentional, do not force it.
  Prefer RAM ≥ 64 MB, or retry soon after the 04:30 reboot, or use zram.
- Router online, checking in, no open `proxy_runtime_unusable`.
- Record the baseline:
  - `passwall2.@global[0].enabled`
  - recovery phase
  - check-in cadence
  - `url_test_node` 204 on the bound slots, as proof of a working data plane

### 3. Waves

1. **Canary (1 router):** `leonid-avito`, if it passes pre-flight; it shows
   the starvation pattern. Otherwise pick one healthy shunt router with
   RAM ≥ 64 MB. Run `update controller <router> --channel beta`. Observe for
   24 h.
2. **3–5 routers** across RAM classes, including one 234 MB box. Observe for
   24 h.
3. **Rest of the fleet:** publish r46 to stable, then run the bulk controller
   update with `hh` excluded explicitly. Routers refused with exit 72 are
   retried after zram or after the 04:30 reboot, never forced.

Gate between waves: no new direct parks, no increase in
`server_unreachable`/`proxy_outage` incidents, check-ins at least every 2 min.

### 4. Verify on a router after the update (read-only)

```sh
opkg list-installed | grep -E '^(vectra-controller-agent|luci-app-vectra-controller) '   # 0.1.13-r46
grep -c url_test_node /usr/sbin/vectra-controller-watchdog                               # > 0 (new dead-man)
grep -c 'vectra-controller-watchdog (managed) >>>' /etc/crontabs/root                     # exactly 1
uci -q get passwall2.@global[0].enabled                                                  # 1
jsonfilter -i /etc/vectra-controller/state.json -e '@.control_plane_recovery.phase'      # idle
jsonfilter -i /etc/vectra-controller/state.json -e '@.control_plane_recovery.last_successful_control_plane_at'
ls -l /etc/vectra-controller/watchdog-direct 2>/dev/null || echo "no watchdog-owned direct"
logread -e vectra-controller-watchdog | tail -n 20
/usr/share/passwall2/test.sh url_test_node <bound-slot-node>                              # 204:...
```

Panel side:

- `controllerRuntimeVersion = 0.1.13-r46`
- `lastCheckInAt` stays within about 45 s. On leonid-avito it was about 20 min
  before; it should now be at most 2 min even when the probe fails.
- No new "Control plane unreachable for over one hour" incidents.

A fresh check-in plus a `url_test_node` 204 is the evidence of success. A
package version alone is not.

### 5. Rollback

A rollback restores r45 **behaviour**, including the faults r46 fixes. On r45:

- a panel outage of 15 min or more switches PassWall off on every onboarded
  router, and the watchdog re-cuts the agent's resume every 5 min;
- check-ins starve whenever the 5 s health probe fails;
- a recovery park waits for the panel or the 12 h RebootCooldown.

So roll back only for a defect worse than these, and do not roll back during
a panel outage.

- **One router:** run `update controller <router> --channel stable` while
  stable is still r45. The r45 watchdog and agent never read
  `/etc/vectra-controller/watchdog-direct`, so the file cannot confuse them.
  If the router is in a watchdog-owned direct at that moment, the r45 agent's
  local rescue restores proxy after two `url_test_node` successes, provided
  its recovery is idle or monitoring. In a recovery phase, the 12 h gate
  applies. Check `uci -q get passwall2.@global[0].enabled` afterwards.
- **Before re-upgrading such a router to r46:** an r45 agent does not delete
  the marker when an operator sets direct. If PassWall is still off, remove
  the leftover marker with an operator-approved terminal job:
  `rm -f /etc/vectra-controller/watchdog-direct`. Otherwise r46 would treat
  that direct as its own and restore it once a node answers. If PassWall is
  on, the r46 install hook removes the marker by itself.
- **Feed:** restore the backed-up feed dir with `sync-runtime-artifacts.sh`,
  then re-run `sync-artifact-metadata.mjs --apply`.
- **Emergency stop of the dead-man on one router** (operator approval needed;
  this is a live write): `uci set vectra-controller.main.deadman_threshold=0; uci commit vectra-controller`.
  In r45 this disables the switch entirely. In r46 it stops new switches to
  direct; an already-owned direct is still restored once a node answers.
