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

## 2026-09-28 — vctl 0.5.0: a router a customer sets up and links by themselves

Branch `claude/funny-brahmagupta-0f2eab` (not pushed; the branch's history
still carries c624ad8/b74ee98 — rewrite before any push).

- **Router UI** (LuCI opens on Vectra): simple view for the owner and Pro for
  the operator, the operator's lock (`ui_lock`, enforced by vctl), Vectra
  Connect's look; ru/en/zh.
- **Setup wizard**: the internet is only checked (the router connects by
  itself — owner's rule), Wi-Fi with a name of its own (Vectra-XXXX), root
  password, linking to the Vectra account by QR / code / Telegram link
  (ADR-0006, accepted); closes whatever happens; never says "done" before the
  subscription is there.
- **My sites**: the owner's always-direct / always-VPN sites as xray's first
  rules for the LAN; one normalization vector for Go and the UI.
- **Release**: when the owner unbinds the router in the app the panel sends
  `released: true`; a router that had an owner wipes that owner's config,
  subscription and choices and shows a new code (fleet routers never wipe).
- **Panel**: partner API `/api/partner/router-claims` (HMAC + idempotency,
  subscription UA required), webhooks, release, proof of the device key at
  adoption, claims only for a router that shows the code, the previous code's
  grace; migrations 0016-0018. Caddy/compose lines for deploy in git.
- Proof: go test 515, UI vitest 318 + tsc, panel vitest 591 + tsc + lint +
  real-Postgres e2e; stand on colima: `setup` 14/14 and `ui` 29/29 "DATA
  PLANE PROVEN" on the final commit, the real wizard walked in LuCI
  (`MODE=setup-hold`). Two independent reviews (UI, security) — findings
  fixed.
- Not done: Vectra Connect side (spec `docs/CONNECT-HANDOFF.md`), panel
  deploy, canary on 1111111111 — order and gates in `docs/LAUNCH.md`.

## 2026-09-28 (later) — the wizard boosts both bands, picks a server, shows around; a design pass

Same branch, uncommitted until this entry's commit; nothing on a live router.

- **Wi-Fi at full power, both bands, one button**: `optimize_wifi` puts every
  2.4/5 GHz radio on Panama's rules (owner's choice; above some countries'
  limits), the driver's full power and the freest fixed channel for its width
  (never DFS, never 12/13; 160 MHz only 36-48; HT40± honoured). The router
  listens to the air only inside that call (the scan stalls the AP), answers
  before restarting, then a detached helper checks netifd for 45 s and puts
  the old `/etc/config/wireless` back when a radio that was up does not come
  back (`apply`: applying/ok/partial/rolled_back/unverified/failed). One
  change at a time (`busy`, also while someone's uci changes are staged);
  6 GHz routers only rename (`unsupported`); mesh radios keep their channel;
  an open network on the air is refused without a key. Package depends on
  `rpcd-mod-iwinfo`. `iw` is not in 24.10 filogic images, so 5 GHz uses
  iwinfo's full sweep.
- **Wizard**: router password step removed (LuCI's banner asks); a server
  step (Auto recommended, countries, rare variants folded); a 4-card tour of
  the main screen; Back/Skip in each step's sticky foot.
- **Pro in plain words** (routes, nodes, servers, overview) — separate agent,
  merged.
- **Design pass** after the owner's "lots of places look terrible": system
  monospace removed (the "squashed text"), CSS resets that overrode
  components fixed at the root, two-column home on a monitor, phone layouts
  fixed (help step overflow, Pro header, tour bubble, link step order), notes
  with actions, "Auto" without the Russian flag, AA contrast on the main
  button. Independent designer review (19 findings) applied.
- Second design round (owner: "кучу мест где прям ужасно выглядит", "приплюснутый
  текст"): every screen walked at 390 px and 1280 px in both themes and in
  ru/en/zh with an overflow detector; "Wi-Fi"/"QR-код"/"что-то" no longer break
  at the hyphen (i18n `glue`, U+2060); a screen with its own "Next" drops
  "Skip this step"; the main button sits at the far right on wide screens;
  the tour keeps the lit card clear of its bubble (phone: centred above the
  docked bubble; a too-tall card gets the bubble beside it); the English
  "channel 11 taken" (read as "occupied") now "picked channel 11". Dev-only
  mock flags `?wifiDown=radio1`, `?wifiEnd=unverified|failed` show each
  Wi-Fi outcome.
- Proof: go test 540 (+ `-race` on the touched packages, 22 mutation checks by
  the backend agent), UI vitest 382 + tsc, bundle 170.0 KB (budget 180 KB);
  stand on colima `setup` 19/19 and, on the committed bundle, `ui` 29/29,
  "DATA PLANE PROVEN". Reviews: Wi-Fi backend (2 HIGH + 6 MEDIUM, all
  fixed), design (19), UI logic (21), the last round's delta (3 LOW, fixed:
  the support report drops the joiner, the tour never pushes a tall card's
  top off the screen, the main button is last for the keyboard too).
- Commits: e7f7458 (Wi-Fi backend, contract, stand), 988c20d (UI), 0ca93db
  (bundle).
- **Not proven: the Wi-Fi on real radios.** The stand has no radio. Before
  any live router: a spare AX3000T (not 1111111111) with HE80 and HE160, a
  phone connected during the boost, and a forced bad channel to see the
  rollback — gate 5a in `docs/LAUNCH.md`.

## 2026-09-30 — 1111: load guards, Russian networks by the kernel, the exit check, «+ Сервис», IPv6 off, real egress, watchdog (r23–r34)

- **r23/r25 load guards** (torrent/any download): P2P peers by the kernel,
  a device's new connections 15/s (burst 60), xray's dials to one node
  40/s, TIME_WAIT/SYN_SENT 30 s; the watchdog borrows another country when
  the main path and its reserve are dead. **r24**: Russian networks by the
  kernel via FakeDNS for every proxied name. **r25**: the review fixes, the
  memory check fails only where the kernel reclaims (not at 48 MB).
- **r26 the exit check**: 1111's American exit (bridge-us5) failed every
  RKN-blocked site (Instagram, Facebook, X, LinkedIn, Discord) while the
  observatory held it alive. The router now asks each foreign exit through
  a loopback HTTP probe (one account per exit) every 10 min; an exit that
  answers the neutral URL and no blocked site while another carries them
  leaves every balancer, is never borrowed, and comes back after two clean
  rounds; kept across restarts (state.json). Stand `exitcheck` 9/9; live:
  bridge-us5 out at the first round, Discord/Instagram 6/6.
- **r27 «+ Сервис»** (spec decision 4): a category of the router's geo file
  into «Мои сайты» (geosite:<id>), found by name or Russian spelling;
  refused when the file lacks it. Stand services `svc_plus_service` PASS;
  live catalog 27, refusal checked.
- Live facts (06:00): cascades DE/PL/NL/FI/FR exit in their countries;
  «Турция» and «ОАЭ» egress in Poland (Cloudflare + ipinfo) — provider's.
- **r28** (fresh review of r26–r27: 0 critical, 5 fixed): an owner's pin to
  an exit left out is parked, never deleted; a round cut off by its deadline
  judges nothing; a busy/refused render is asked for at the next round;
  exits no balancer can lose never restart xray; the probe port 10087 is
  guarded. Stand exitcheck/killswitch/failover/services green; live on 1111
  07:45.
- **r29**: the provider's whitelist levels (for a mobile network under a
  whitelist regime) are no longer named as dead nodes on a home connection.
- **r30** (owner: «NNM club не открывается — бесконечная загрузка»; «IPv6
  заблокирован у нас на серверах — выключи его и у нас»): the provider's
  Russian rule also names sites it sends through its Russian server on
  purpose (nnmclub.to, kinozal.tv, anime sites); «Russian sites direct» had
  sent that rule direct whole. A mixed rule is split now: Russian
  destinations (markers, .ru .su .by .рф, address literals) direct, the rest
  keep the bridge with FakeDNS. IPv6 off: the LAN's IPv6 to the outside is
  refused at once (TCP reset / admin-prohibited), the resolver answers IPv4
  only; UCI `ipv6 '1'` carries it. A healthy exit is no longer judged
  filtered while its node restarts (sites re-asked after the control).
  Live: NNM-Club 200.
- **r31** (owner: «доделай все что осталось»): the router locates each
  foreign exit's real egress country once a day (Cloudflare trace `loc=`,
  hourly retry when unreachable) and says so where it differs from the name
  — route card «🇹🇷 Турция (выход: 🇵🇱 Польша)», a service's country
  option likewise. The review's deferred minors done: the exit check renders
  the running document (never the UI's way through the entries cache); a
  service the geo file lost is marked «нет у роутера»; the card names unfit
  exits the main pool still goes through when no other is left. Stand
  exitcheck/ui/services green; live on 1111 12:03 — TikTok egress {AE: PL,
  TR: PL}, NNM-Club/Discord/Instagram/YouTube 200.
- **r32** (fresh review of r29–r31: 0 critical, 3 important — all fixed
  RED→GREEN): an address the provider lists in its Russian rule keeps the
  bridge (r30 had sent every literal direct — a stand-fixture rule; the
  provider's entry names only geoip:ru); an exit that stops answering the
  location check is asked hourly, not every round; IPv6 off proven on the
  packets (stand `ipv6-refused`: TCP refused in ~1 ms, UDP admin-prohibited,
  router's own v6 and IPv4 fine; control `ipv6-refused-off` detects);
  ru-kernel now proves geoip:ru by the kernel with a real RU address. Also:
  the controller's own shutdown no longer logs «xray exited; will restart»
  (the five «kills» of 30.09 were the updates, no OOM); the route line keeps
  «(выход: …)» whole at 390 px. Live 12:59: resolver 0 AAAA / 8 A, kernel
  direct 15704 ranges, NNM-Club/Discord/Instagram/YouTube 200. Leak watch
  05:48–11:52: xray RSS flat 57–62 MB.
- **r33**: the stand's PassWall takeover race (`pw_race_restart`, failing
  since r23) was the failover watchdog, not PassWall: r22's «fallback at
  once» pointed a balancer whose lone node looked failing at its fallbackTag —
  DIRECT in the stand — and nothing brought it back while traffic went
  direct. The watchdog now falls back at once only where the fallback goes
  through a node (a stage into a balancer, or a dialling outbound); direct and
  a black hole stay xray's own last resort. 1111's fallbacks are all stages
  (and one whitelist node) — no live exposure. passwall-real all PASS twice
  (race 4/4 ×2), failover modes green; live 13:57, the r32 shutdown logged as
  an orderly stop.
- **r34** (owner: «Да, все восемь» — the review's deferred minors): IPv6
  off refuses only the LAN's IPv6 outward — IPv6 into the LAN's own devices
  (netifd: up, static, no default route) returns to fw4, none known → the
  whole refusal (stand ipv6-refused + control ipv6-refused-nolan); the exit
  country survives restarts (state.json exit_egress); no location look at
  dead exits; loc=XX/T1 unknown; no v6 direct set while IPv6 is off (1111:
  all 9753 v4 ranges now fit); the card keeps no unfit server main was moved
  off; Russian matchers case-insensitive past the prefix; one flag+country
  helper in the UI. Live 16:05: `oifname "br-lan" return` before the refusal.
- Open: Wi-Fi on real radios (gate 5a), push blocked until c624ad8/b74ee98
  are rewritten.

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
