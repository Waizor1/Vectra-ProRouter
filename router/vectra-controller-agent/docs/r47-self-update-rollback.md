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
     3 min; or no contact by **60 min** with the panel not answering either —
     the update job itself proved the router reached the panel before, and a
     new version can cut its own path (carve-out, nft, TPROXY), so the guard
     goes back to the known-good version. Success (stable + contact) removes
     the copy.
   - every restore is checked: tar's exit code, the agent binary byte for byte
     against the copy, both stanzas' versions. An incomplete restore (full
     filesystem, truncated write) keeps the copy and the session (phase
     `restore-failed`) and is retried every minute by the guard and every
     5 min by the watchdog; the copy is never deleted before a verified
     restore.
   - rollback marker: `/etc/vectra-controller/update-rollback.marker`.
2. **Watchdog**: a missing/empty binary is restored through the guard when a
   rollback copy exists, and an incomplete rollback is retried; the legacy
   agent is never enabled/started while vctl owns the router (any
   `/etc/vectra-controller-pro/.*-by-vctl` marker, `vectra-controller-pro
   running`, or `table inet vctl`). A merely disabled init script without vctl
   is enabled and started as before.
3. **Panel rollout guard**: `queueControllerUpdate` / `queueBulkControllerUpdate`
   refuse a router with an open `server_unreachable` incident, one opened in
   the last 24 h, a check-in older than 10 min, or status direct/rescue, unless
   forced: the router's Updates tab asks once ("Всё равно обновить", with the
   reasons), `VectraPanelCli.sh update controller <router> --force` from the
   CLI (tRPC `force: true`). Bulk reports such routers as `skipped`. A forced
   update also passes the guard's exit 75.

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
  is lost; the guard then judges the half-restored version like a fresh one
  (crash loop, no contact) and restores again.
- Agents older than 0.1.12-r1 still take the old `update_controller` job,
  which has no guard.

Tests: `openwrt/tests/update_guard_test.sh`,
`openwrt/tests/watchdog_update_rollback_test.sh` (sh, dash, BusyBox ash), the
docker stand `openwrt/tests/update-stand/run.sh` (11 scenarios), vitest
`controller-update-jobs.test.ts`, `controller-update-rollout-guard.test.ts`,
`destructive-gating.test.ts`.
