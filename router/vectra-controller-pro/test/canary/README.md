# Canary guard

`canary-guard.sh` supervises a live cutover on a real router and hands the
router back to PassWall the moment the new stack stops looking healthy.

## Why it exists

The first live canary (2026-08-05, router 1111111111) reverted on a blind
timer: it needed a human to confirm inside a fixed window, and it rolled back
when that window closed for a reason that had nothing to do with the router —
a transient panel error at the wrong minute. The data plane had been carrying
real household traffic correctly the whole time.

A timer answers "has someone been watching?". The router needs an answer to
"is this still working?". This guard reverts on evidence instead, and keeps the
absolute cap only as a backstop.

## What it watches

Every `CHECK_INTERVAL` (30s in production):

| Probe | Fails when |
|---|---|
| `xray_alive` | `status.json` is not `state=running`, its `pid` is gone, or that pid is no longer an xray |
| `mem_ok` | `MemAvailable` drops below `MEM_FLOOR_KB` (25 MB) |
| `dataplane_ok` | the `inet vctl` table or the `fwmark 0x1 lookup 100` rule is missing |
| `egress_ok` | the egress probe stops returning 200 |
| `tproxy_advancing` | the tproxy hit counter goes **backwards** |

Four of these are noisy by nature, so they must miss `FAIL_STREAK` (3) sweeps
in a row before the guard acts — a single blip does not cost a canary.

A regressing counter is different: it means something tore our table down and
built a new one underneath us, which cannot be a sampling artefact. It reverts
immediately. It also *has* to: the probe rebases its own baseline after a
regression, so the next sweep compares the new value against itself and passes.
On the streak this check would be dead code that looked alive — which is
exactly how it was first written, and what the test caught.

## Field names are load-bearing

`xray_alive` reads top-level `state` and `pid` from `supervisor.Status`
(`internal/supervisor/types.go`, written verbatim by `WriteStatus`). An
invented field name reads as an empty pid, reports "dead" on every sweep, and
reverts a perfectly healthy run within `FAIL_STREAK` sweeps. Verify against the
struct before changing it.

`pgrep -x xray` is deliberately not used: PassWall runs xray under
`vectra-xray-wrapper`, so a name match is both a known false negative on this
fleet and liable to find somebody else's process.

## Controls

| Path | Effect |
|---|---|
| `/tmp/canary-abort` | revert now |
| `/tmp/canary-extend` | push the cap out by another `CAP_SECONDS`, then consumed |
| `/tmp/canary/guard.log` | per-sweep log |
| `/tmp/canary/revert-reason` | why it reverted (absent while running) |

Everything lives in tmpfs on purpose: a reboot is itself a full recovery.

## Tests

```bash
docker run --rm -v "$PWD/test/canary:/work" busybox:latest sh /work/guard-test.sh
```

19 assertions over 11 scenarios, run under the same busybox ash the router
uses, against stubbed `nft`/`ip`/`curl`/`uci`.

The suite is deliberately two-sided. Scenarios 2–10 prove the guard *does*
revert — on a dead xray, a reused pid, low memory, a vanished table, a vanished
policy route, dead egress, an abort file, the cap, and a counter regression.
Scenarios 1 and 7 prove it does *not* — not on a healthy system, and not on a
single transient blip. Either half alone would pass for a guard that is broken
in the other direction.

Scenario 10 asserts the revert lands in under two sweeps, which is what
distinguishes a hard fault from one on the streak; a version of this test that
merely waited long enough would have passed against the dead-code original.
