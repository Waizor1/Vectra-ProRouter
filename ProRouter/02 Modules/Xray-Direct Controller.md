---
type: module
path: router/vectra-controller-pro
stage: beta
confidence: medium
last-reviewed: 2026-06-01
tags:
  - module
  - go
  - openwrt
  - xray
  - passwall2-replacement
  - happ-crypt
---

# Xray-Direct Controller (`vctl`)

Next-generation standalone controller that drives Xray **directly**, replacing
the PassWall2 lua/shell prosthetic. See [[03 Decisions/ADR-0005-standalone-xray-direct-controller|ADR-0005]]
for the standalone-vs-fold-in decision and risks. Sibling of (eventually
replaces) [[Router Agent]].

## 2026-06-01 — full Xray + HAPP CRYPT v5 + hardening (verified; live flip still gated)

- **Full Xray parity** closed: Observatory/BurstObservatory (balancer health feed for leastPing/leastLoad), http/2 transport, REALITY inbound + inbound streamSettings, applied `ForceFingerprint` normalization, `ruleTag`, metrics inbound.
- **HAPP CRYPT v5 key protection** (new `internal/happcrypt` + `vctl happ-crypt` CLI + panel `happCrypt.encrypt`): encrypts subscription links into `happ://crypt{2,3,4,5}/` so only the Happ app can decrypt — the app's VLESS keys can't be viewed/extracted. crypt2/3/4 offline (embedded official RSA-4096 keys, fingerprints pinned in a test); crypt5 via the official `crypto.happ.su` API (closed algorithm → license-clean, no key redistribution; HTTPS-pinned incl. redirects, no-retry on permanent 4xx, response validated, never logged). See research memory [[project... happ-crypt]].
- **Hardening:** optional firewall kill-switch (PREROUTING `policy drop` fail-closed; OUTPUT always `accept` so control-plane/DNS never dropped — can't strand the router; off by default); xray at-rest secret masking parity + `restoreMaskedXrayConfig` (closes a latent save-path trap before any xray editor UI); subscription `allowInsecure` gate (a hostile upstream can't disable outbound TLS verification); creation-order (asc) job delivery.
- **Verified:** code-review + security-review + verifier — **ALL CLEAN, 0 Critical/High**; `go test -race`, web tsc + vitest (348 pass / 4 known env), aarch64 cross-compile, `bash -n` green. PR #27. Live `.ipk` build / parity capture / on-device `192.168.99.1` test / live canary remain GATED. Plain-language report: `ai_docs/develop/features/vctl-controller-report.md`.

## Confirmed (2026-05-31 — canary-ready, NOT deployed to any live router)

- **Autonomous control loop** ported from the proven agent into `vctl`:
  `internal/controlplane` (register/check-in/job-result, protocol `2026-04-v1`,
  xray-native inventory with `engineMode`), `internal/state` (atomic + last-good
  + salvage; **adopts the legacy agent's router id/token** for canary identity
  reuse), `internal/inventory` (xray-native, bounded-subprocess timeout),
  `internal/apply` (XrayDesiredConfig → render → atomic write → reload),
  `internal/jobsafety` (RAM/overlay/tmp floors), `internal/rescue` (connectivity
  probe + direct fallback). Daemon entrypoint `vctl agent -config <json>` with
  handlers for `apply_xray_config`, `refresh_xray_subscriptions`,
  `update_xray_assets`, `reload_xray_outbound`, `update_controller`,
  `run_terminal_command`, `collect_router_logs`, `enter_direct_mode`, `reconnect`.
- **Firewall commit-confirm auto-revert** (`internal/firewall/commit_confirm.go`):
  a detached deadman reverts the TPROXY ruleset within 90s unless a successful
  check-in (re)writes the confirm sentinel — restart-safe (the sentinel, not an
  in-memory flag, is the durable signal). Direct-mode / reconnect / auto-rescue
  now actually tear down / re-apply the firewall.
- **Xray renderer** produces correct P0 fleet output (VLESS+REALITY Vision/gRPC,
  Discord mux, tproxy shunt, DoH) with operator fingerprints preserved (no silent
  normalization). Locked by golden-snapshot tests; a live parity oracle
  (`parity_test.go` + `scripts/Capture-XrayParityCorpus.sh`) is ready and skips
  until a captured PassWall2 corpus is supplied.
- **Off-router control plane** is additive (`engineMode` defaults `passwall`):
  contracts (xray job/artifact types, `xrayDesiredConfigSchema`), DB column +
  migration `0015`, engine-aware `router-control.ts`, separate `queueApplyXray`/
  `saveXray`. The 18 live passwall routers are provably unaffected (confirmed by
  code+security review).
- **Packaging**: `openwrt/Makefile` (binary `/usr/sbin/vctl`, `DEPENDS +xray-core`
  + tproxy kmods), runtime files (procd init.d that stops the legacy agent for
  mutual exclusion, render-xray-config.sh, vctl-xray-wrapper), feed integration in
  `build-vectra-openwrt-feed.sh`.
- **Security-hardened self-update**: requires `sha256` (fail-closed), refuses any
  artifact that isn't `vectra-controller-pro`, HTTPS-pins all external fetches.
- Proof: `go test -race ./...` green; e2e + self-update-guard tests pass; web tsc
  clean + vitest 327 pass (4 pre-existing env failures); aarch64 cross-compile;
  `bash -n` on all shell. Independent code-review + security-review run and
  blockers fixed.

## Risks / gated (do not run without explicit sign-off)

- **No live router has run this.** Real SDK `.ipk` build + signed-feed publish,
  the lua/captured parity-oracle corpus, on-device tmp dry-run, and the live
  canary flip are all gated. Reuses (not re-verified end-to-end on hardware) the
  agent's wire contract — first live check-in is the integration proof.
- **Fail-open-to-direct firewall** (PassWall2 parity): traffic egresses direct if
  Xray is down. Acceptable for canary (PassWall2 is the rollback); a strict
  kill-switch is a fast-follow before fleet rollout.
- **Fast-follows before fleet rollout** (tracked in ARCHITECTURE.md): at-rest
  masking parity for xray configs (currently plaintext in the revision jsonb,
  secret blob is encrypted), gate subscription `allowInsecure`, switch job
  delivery to creation-order (asc), native gRPC Observatory/Handler hot-reload.

## Next Review

- On explicit go-ahead: build/publish the `.ipk` on the VPS, capture the parity
  corpus, run the tmp dry-run on a TEST router, then flip `engineMode=xray-direct`
  on ONE healthy volunteer (non-`hh`, non-low-RAM) with PassWall2 as rollback.
- Capture live canary proof: 5-slot `url_test=204`, Discord-UDP, DNS, RAM/overlay
  delta vs the PassWall2 baseline.
