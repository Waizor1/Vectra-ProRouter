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
     loses it), else **exit 73** and nothing changes. Exit 74 while a previous
     update is still under verification. Exit 72 (resource guard) unchanged.
   - opkg or a post-install check fails: `restore-files` puts the old files
     back (the old agent is still the running process).
   - success: `arm` + `launch` — a `setsid` process restarts the agent after
     5 s and judges it; a managed cron line (every minute) resumes it after a
     reboot or if it died.
   - rollback when: the binary is missing; the agent never ran 120 s in a row
     within 5 min (crash loop); no control-plane contact (state.json
     `last_successful_control_plane_at` after the restart) by 15 min while the
     panel's `/api/health` has answered from the router for 3 min.
     Panel not answering = the network: the new version stays, guard ends at
     60 min. Success (stable + contact) removes the copy.
   - rollback marker: `/etc/vectra-controller/update-rollback.marker`.
2. **Watchdog**: a missing/empty binary is restored through the guard when a
   rollback copy exists; the legacy agent is never enabled/started while vctl
   owns the router (handover marker, `vectra-controller-pro running`, or
   `table inet vctl`) or its init script is disabled.
3. **Panel rollout guard**: `queueControllerUpdate` / `queueBulkControllerUpdate`
   refuse a router with an open `server_unreachable` incident, one opened in
   the last 24 h, a check-in older than 10 min, or status direct/rescue, unless
   `force: true`. Bulk reports such routers as `skipped`.

Tests: `openwrt/tests/update_guard_test.sh`,
`openwrt/tests/watchdog_update_rollback_test.sh` (sh, dash, BusyBox ash), the
docker stand `openwrt/tests/update-stand/run.sh`, vitest
`controller-update-jobs.test.ts`, `controller-update-rollout-guard.test.ts`,
`destructive-gating.test.ts`.
