# 0.1.13-r47: a controller self-update can no longer lose a router

Why: leonid-avito took the r46 self-update on 2026-10-06 12:20:47 UTC and never
checked in again. The panel's update command replaced the pair with
`opkg install --force-reinstall`, scheduled a restart, kept no copy of the old
version and never looked at whether the new agent came back. The watchdog, on
a missing binary, only logged "manual recovery required".

## What changed

1. **Rollback guard in the panel's update command** (all source routers, r40+):
   `apps/web/src/lib/controller-update-jobs.ts` writes
   `/etc/vectra-controller/update-rollback/guard.sh` (canonical source:
   `openwrt/update-guard/vectra-update-guard.sh`, embedded via
   `apps/web/scripts/sync-controller-update-guard.mjs`) and runs it:
   - `prepare` before opkg: tar.gz of every file in both packages' `.list`,
     their `/usr/lib/opkg/info/<pkg>.*`, and their status stanzas. On the
     overlay if 8 MB stay free after it, else `/tmp` (16 MB floor; a reboot
     loses it; the /tmp directory is 0700 and a symlinked or non-root copy is
     never extracted), else **exit 73** and nothing changes. Exit 74 while a
     previous update is still under verification or its rollback is being
     retried. **Exit 75** when this router rolled back from the same target
     version less than 24 h ago (the marker below); an operator-forced update
     passes it. Exit 72 (resource guard) unchanged.
   - opkg or a post-install check fails: `restore-files` puts the old files
     back (the old agent is still the running process).
   - success: `arm` + `launch` — a `setsid` process restarts the agent after
     5 s and judges it; a managed cron line (every minute) resumes it after a
     reboot or if it died.
   - rollback when: the binary is missing; the agent never ran 120 s in a row
     within 5 min (crash loop); no control-plane contact (state.json
     `last_successful_control_plane_at` after the restart, and a stamp that
     changed since the restart, so a clock stepped back cannot fake one) by
     15 min while the panel's `/api/health` has answered from the router for
     3 min; or no contact by **60 min** with the panel silent from the router
     for at least the last 3 min —
     the update job itself proved the router reached the panel before, and a
     new version can cut its own path (carve-out, nft, TPROXY), so the guard
     goes back to the known-good version (a panel that answers again is left
     to the 15-min rule). Windows run on `/proc/uptime`, so a wall-clock jump
     moves nothing; a reboot re-arms them. Success (stable + contact) removes
     the copy.
   - a reboot loop: an early boot hook (`/etc/init.d/vectra-update-guard`,
     START=19, written with the session and removed with it) counts boots of
     an unconfirmed update; at the 3rd it rolls back before the new agent
     starts again. A healthy version confirms within 15 min, so the daily
     04:30 reboot or one reboot during the window changes nothing. At boot
     the hook also ends a session older than 2 h without a rollback (an
     abandoned one), and otherwise restores a lost cron line and relaunches
     the judging loop. A discarded stale session leaves nothing behind (boot
     count, attempts, flags), only the guard script.
   - every restore is checked: the whole copy extracted without error, every
     file of it matching the sha256 manifest made at prepare (the packages'
     conffiles, which the agent itself writes, excepted), both stanzas'
     versions. A running agent counts as restored only if the guard started
     it from the verified binary or its `/proc/<pid>/exe` is the copy's;
     otherwise it is restarted. An incomplete restore (full
     filesystem, truncated write) keeps the copy and the session (phase
     `restore-failed`) and is retried with a backoff (1, 2, 4 … 30 min, the
     watchdog nudging the same throttled retry). Each retry first checks
     whether the restore is in fact complete and never stops an agent that
     already runs the copy's binary. After 10 attempts: phase `manual`, the
     running agent is left alone and the copy kept (with no agent running at
   all it keeps retrying every 30 min); the operator ends it with
     `sh /etc/vectra-controller/update-rollback/guard.sh clear` (via
     `VectraPanelCli.sh terminal`) or a forced update — both only while the
     agent runs from an executable binary. The copy is never deleted before a
     verified restore except by that operator decision.
   - rollback marker: `/etc/vectra-controller/update-rollback.marker`.
2. **Watchdog**: a missing/empty binary is restored through the guard when a
   rollback copy exists; an incomplete rollback nudges the guard's throttled
   retry and every other branch (dead-man included) keeps running. The legacy
   agent is never enabled/started while vctl owns the router: vctl's
   `.legacy-agent-disabled-by-vctl` marker (in `/etc/vectra-controller-pro` or
   `/tmp/vectra-trial.d`), `vectra-controller-pro running`, or `table inet
   vctl`. vctl's other markers (`.passwall-retired-by-vctl`,
   `.passwall-disabled-by-vctl`) outlive a `vectra off` and hold nothing. A
   merely disabled init script without vctl is enabled and started as before.
3. **Panel rollout guard**: `queueControllerUpdate` / `queueBulkControllerUpdate`
   refuse a router with an open `server_unreachable` incident, one opened in
   the last 24 h, a check-in older than 10 min, or status direct/rescue, unless
   forced: the router's Updates tab asks once ("Всё равно обновить", with the
   reasons), `VectraPanelCli.sh update controller <router> --force` from the
   CLI (tRPC `force: true`). Bulk reports such routers as `skipped`. A forced
   update also passes the guard's exit 75; when the newest controller job hit
   75, the Updates tab says so and offers the same confirmation. A forced
   request upgrades a still-queued unforced job in place.
4. The terminal payload cap for the panel's own command is 48000 (the guard
   is ~27 KB compacted); operator-typed commands stay at 8000.

## Known limits of a rollback

- The new version's uci-defaults have already run (`90`–`93`: config
  defaults, low-memory profile, watchdog cron, control-plane carve-out). A
  rollback restores the packages' files, not those side effects; the old
  version's own uci-defaults are not re-run.
- `/etc/vectra-controller` config (a conffile of the package) goes back to
  its pre-update content: LuCI edits made between the update and the rollback
  are lost.
- state.json is never rolled back.
- If the phase file cannot be written (filesystem full) and the router
  reboots before the retry, the "restore incomplete" flag (also kept in /tmp)
  is lost; the guard then judges the half-restored version like a fresh one:
  a crash loop or silence restores again, but an agent that runs and reaches
  the panel ends the session (copy deleted) with whatever opkg state the
  failed attempt left.
- Agents older than 0.1.12-r1 still take the old `update_controller` job,
  which has no guard.
- A forced request can race the router fetching the queued unforced job
  (between the panel's read and its update of the row): that one run fails
  with 75 and the next forced request queues a forced job. It heals itself
  and is left as is.

Tests: `openwrt/tests/update_guard_test.sh`,
`openwrt/tests/watchdog_update_rollback_test.sh` (sh, dash, BusyBox ash), the
docker stand `openwrt/tests/update-stand/run.sh` (16 scenarios), vitest
`controller-update-jobs.test.ts`, `controller-update-rollout-guard.test.ts`,
`destructive-gating.test.ts`.
