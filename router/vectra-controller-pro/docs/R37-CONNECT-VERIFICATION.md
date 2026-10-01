# r37 Connect local verification

The recovered r37 checkpoint commit 7d56b0c is extended by direct user-authorized local Connect work. No push, merge, deployment, publication or live router operation. The primary checkout and claude/funny-brahmagupta-0f2eab are untouched.

## Implemented

Native authenticated connect_router_action accepts strict {origin:partner_action,actionId,ownerRef,action,params}, bound to current adopted owner and target router. Existing restart_vpn/reload_xray_outbound and refresh_subscription/refresh_xray_subscriptions remain. New actions: select_entry, set_service, set_rules, set_wifi, reboot, update_now, set_auto_update. Exact provider entry digests survive ordering changes; service overlays import bounded namespaced provider graphs with validation, not country approximations. Unicode domain rules deduplicate before the 300-rule cap; legacy IP/local routing remains compatible.

Measured telemetry includes supported catalogue/capabilities, actual active DHCP clients, exit verdict/country, preferences and signed available version. Guest Wi-Fi readback requires verified successful Wi-Fi apply, current owner/AP identity and safe setup state. Password exists only in confidential cloned HTTPS check-in; normal snapshot, logs, action result and journal are secret-free. Panel encryption and access control are independently maintained.

Durable owner-scoped receipts suppress replay across retries, restart and archive rollover; 1024 active records and 64 MiB archive budget fail closed rather than forget old actions. Reboot remains accepted until changed boot ID proves completion. Manual and automatic updates verify installed package/runtime versions and signed feed; retry state is durable and bounded. Owner reassignment clears old configuration/overlays and secret marker.

## Verification

- Go full suite and vet: PASS,1113 top-level tests +324 subtests,46tested packages,4without tests. Full race PASS.
- UI470 tests/typecheck/build PASS;194.4KiB/93.5KiB gzip. Existing rendered390px ru/en/zh light/dark walk PASS; no frontend changes in this extension. Fresh confidential-password panel visual interaction is not claimed here.
- Real dispatcher fake TLS panel: all seven new actions, authentication, owner/target rejection, replay/interrupted actions, reboot proof, resource gates, crash recovery, Wi-Fi secrecy PASS. Routing graph/CLI old-digest replacement, maintenance, journal archive and Wi-Fi targeted race PASS.
- Subscription redirect leak: RED fake transport reproduced cross-host/port/userinfo forwarding; GREEN same-origin HTTPS permitted, all unsafe redirect transports blocked. No real network. Independent final security reviews APPROVE with no remaining concrete scoped findings. Signed rollback regression reproduced RED and now rejects older verified metadata before download/install/restart; unknown installed version or comparison error fails closed, equal reinstall/upgrades remain allowed. Root extraction is an architectural limit and separate upstream custody/revocation release audit, not hidden by cryptography.
- Build-cache failure propagation: RED old failed compile returned cached binary; GREEN compiler failure stops packaging. Earlier affected build is invalidated.
- Final 31 non-i386 unsigned feed PASS at dist/r37-connect-verified-feed (final source commit recorded in feed.json). Synthetic Docker full12/UI29/setup19 assertions PASS after final redirect fix. Installer165/165 PASS on OpenWrt24.10.8/aarch64_generic after redirect fix; the later raw-update floor is covered by focused fake update tests and final Go/race/build.

## Bridge and limits

Panel source contract pinned bea26f8. Final test-only worker /tmp/vctl-connect-bridge.test SHA256 recorded in the final local handoff after the version-floor rebuild. Env VCTL_CONNECT_BRIDGE_INPUT/OUTPUT; -test.run ^TestConnectBridgeWorker$. Input routerId,ownerRef,job or jobs,responseRouterId,confirmReboot,reportWifi. All OS/providers fake. Output results/capabilities/wireConnect/safePersisted/authenticated/mutations. Final local first-service catalogue, owner Wi-Fi confidential path and reboot proof PASS. The parent Connect executor owns final actual panel→worker→panel verification; this document does not claim that independent check.

Production signing/publication, hardware Wi-Fi/reboot/zram validation, live subscriptions/real egress and upstream per-device revocable custody are not run. No vm.min_free_kbytes, hardware offload or BBR policy expansion. No new permanent access or private key embedded. Live steps need exact separate authorization and appropriate router baseline/rollback confirmation.
