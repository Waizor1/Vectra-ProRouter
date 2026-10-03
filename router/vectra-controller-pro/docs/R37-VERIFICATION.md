# r37 local verification

Integrated r37-retire (fd550c2), r37-harden (7a7ee84), and r37-tune (c53e66b) into vctl/r37, preserving the earlier login/support-shell integration. No publication, signing, live-router mutation, or primary-checkout changes.

Security regressions use red-to-green tests: persist tuning backup before mutations; retain backup on failed firewall rollback/UCI revert; block undo behind pending UCI edits before comparing committed values; resume backed-up retirement cleanup after interruption. Independent review found no remaining concrete defects within these fixes. The earlier HTTP429 review never resumed; this local review closes its outstanding work with the stated scope.

## Validation

- Go full suite: 1,025 top-level tests + 261 subtests, 44 tested packages (four packages have no tests); vet; full race suite, plus targeted tune race after final fixes.
- UI: 470 tests, TypeScript check, production build (194.4 KiB / 93.5 KiB gzip). Combined r37 features exceed the earlier 180 KiB budget; explicit ceiling is now 200 KiB.
- Fresh rendered UI at 390px: main/support, Wi-Fi, final tuning summary, ru/en/zh, light/dark, no visible overflow or console errors.
- Synthetic Docker dataplane: full 12, UI 29, setup 19 assertions passed. Initial UI/setup fixtures failed the new HTTPS guard; fixtures now terminate TLS with throwaway stand certificates. Production HTTPS enforcement stays enabled.
- OpenWrt 24.10.8 aarch64_generic installer full suite: 165/165 assertions; earlier focused lifecycle/standby/PassWall upgrade/retire suite: 76/76.
- Unsigned local r37 feed built for 31 architectures excluding the `i386` feed target; dist/r37-verified-feed. No i386 publication.

## Historical panel checkpoint (superseded)

The paragraphs below describe the original recovered r37 checkpoint. The locally authorized Connect extension now supersedes these gaps; see [R37-CONNECT-VERIFICATION.md](R37-CONNECT-VERIFICATION.md).

### Original contract and limits

Remote dispatch supports apply_xray_config, refresh_xray_subscriptions, update_xray_assets, reload_xray_outbound, update_controller, run_terminal_command, collect_router_logs, enter_direct_mode, reconnect. Unknown job types fail closed. Support shell is separately owner-controlled; it is not a substitute for Connect action APIs.

Connect restart_vpn maps to reload_xray_outbound; refresh_subscription maps to refresh_xray_subscriptions. Rules assets can use existing update_xray_assets with its validated payload. There are no typed remote jobs for Wi-Fi mutation, generic services, reboot, or an autoUpdate preference. Local rpcd methods do not create authenticated remote equivalents. Adding these requires capability negotiation, per-action schemas/permissions, persistence semantics and negative tests: a separate contract change beyond the recovered r37 A/B/C/D requirements.

Inventory already sends identity/public key, selected node, node/subscription counts, versions, resource/service health, reachability, config revision/digest, and remoteShell. It does not send Connect verdict/exitCountry/lanClients or a supported-actions capability list; panel should return null/unsupported until a negotiated telemetry extension exists. Panel-owned claims/snapshot/webhooks remain separate work.

update_controller requires HTTPS, vectra_pro feed, a trusted installed usign key, signed Packages (via Packages.gz), architecture/version/hash match and size caps. A legacy controller feed is refused. End-to-end panel integration, production signing/publication, other architecture runtime stands, hardware zram and real subscription egress were not run. vm.min_free_kbytes, hardware offloading and BBR retain existing guard constraints.
