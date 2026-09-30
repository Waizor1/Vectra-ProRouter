# vctl: a server per service (Plan B) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** YouTube, TikTok and Telegram each get «По умолчанию» or a country of the running subscription entry, chosen in the router UI and proven on packets.

**Architecture:** A choice is an overlay the splice adds on top of the provider's entry: a balancer `VCTL-SVC-<ID>` over the chosen country's outbounds (strategy leastPing), its fallback a loopback back into the service's own target in the entry, and the service's rules placed above the provider's (below the owner's own sites). The choice lives in the router's overrides; `vctl rpcd` serves `services` / `set_service`; the UI shows a «Сервисы» card in the simple view.

**Tech Stack:** Go (vctl), Preact + TypeScript (router UI, vitest), OpenWrt stand (bash, docker/colima).

**Spec:** docs/superpowers/specs/2026-09-30-vctl-service-routing-design.md (decision 3; decision 4 «+ сервис» and decision 6's card text are a later plan).

## Global Constraints

- Three services only: youtube (`geosite:youtube`), tiktok (`geosite:tiktok`), telegram (`geosite:telegram` + `geoip:telegram`).
- A country is offered only when the entry has dialling outbounds whose tag names it (`countryHint`: `bridge-de5` → DE; `whitelist-lv3` names none).
- A dead or vanished country never leaves a service with nothing: the overlay falls back to the service's own target in the entry; a choice whose country the entry no longer has is not rendered and is reported `stale`.
- The owner's own sites win over a service choice (their rules stay first).
- Every user-visible string in ru, en and zh; no Cyrillic in en/zh.
- Provider mode only (route_source `provider`); native/passwall answer `available: false`.
- Nothing reaches 1111 before the stand proves it on packets and the UI is checked at 390 px in both themes.

## Review Focus

1. A loop: service traffic re-entering routing through the loopback must reach the service's own target, never the overlay again (the loopback rule is first).
2. A choice the entry cannot serve any more (provider changed the entry, other location chosen): no overlay, `stale: true`, the service on its default.
3. The owner's «напрямую» list names a TikTok domain while TikTok has a country: the owner's rule wins.
4. The observatory must probe the overlay's members, or leastPing has no data (subjectSelector extended when needed).
5. set_service racing another change (set_rules, select_entry): each is one Change on the daemon's loop; the render key includes the services.

---

### Task 1: The service overlay in the splice

**Files:** Create `internal/coreengine/xray/services.go`, `internal/coreengine/xray/services_test.go`; modify `internal/coreengine/xray/runtime_options.go` (SpliceOptions.Services, Key), `internal/coreengine/xray/splice.go` (render the overlay, SpliceResult.Services*), `internal/xrayview` (exported CountryHint; uiapi's countryHint delegates to it).

- Tests first (package xray): no choice → document unchanged; tiktok=DE on an entry with sticky-de5/bridge-de5, BL-TK over sticky-by5 and a `geosite:tiktok → BL-TK` rule → outbound `vctl-svc-tiktok` (loopback, inboundTag `vctl-svc-tiktok`), balancer `VCTL-SVC-TIKTOK` selector [bridge-de5, sticky-de5] leastPing fallback `vctl-svc-tiktok`, rules in order: the loopback rule (inboundTag → BL-TK) first, the owner's rules, `geosite:tiktok → VCTL-SVC-TIKTOK`, then the provider's rules unchanged; telegram gets its domain and ip rules; a country the entry lacks → no overlay, reported stale; the observatory's subjectSelector gains the overlay's tags it lacks; the render key changes with the choice.

### Task 2: The choice in the overrides and the daemon's render

**Files:** `internal/localctl` (Overrides.Services, Change.SetService, ApplyTo), `cmd/vctl/localui.go` (spliceOptionsFor passes the services).

### Task 3: `services` and `set_service` for the UI

**Files:** `internal/uiapi` (BuildServices + types), `cmd/vctl/cmd_rpcd.go` (methods, lock: allowed in the simple view), `openwrt/files/usr/share/rpcd/acl.d/vectra-controller-pro.json`, `ui/contract/services.json` + README section; tests against the fixture.

### Task 4: The stand proves it on packets

**Files:** `test/dataplane/stand/services/subscription.json` (country-tagged outbounds on the three nodes, BL-TK, `geosite:tiktok → BL-TK`), `test/dataplane/stand/mode-services.sh`, run.sh/stand.sh/README rows.

- Assertions: default → a TikTok request (SNI www.tiktok.com) goes through BL-TK's node; `set_service tiktok NL` → through the NL node; the `services` answer; an unknown country refused; back to default → BL-TK's node again.

### Task 5: The «Сервисы» card

**Files:** `ui/app/src/api/types.ts`, `ui/app/src/api/store.ts` (read/action methods), `ui/app/src/views/Services.tsx`, `ui/app/src/views/Simple.tsx`, `ui/app/src/i18n/strings.ts`, `ui/app/src/mock/*`, tests; the built `openwrt/files/www/luci-static/vectra/vectra-app.js`.

- A row per service: its name, a select «По умолчанию (🇧🇾 Беларусь)» + the countries (flag and name from the locale); the change is pending until `services` shows it; a stale choice says so.
- Screenshots at 390 px, light and dark, before anything ships.

### Task 6: Release r23, then 1111

- VECTRA_RELEASE=23, CHANGELOG; Go suite, the stand (services, ui, failover*), installer untouched → ruling; publish pro-canary; upgrade 1111; on 1111: `services` answers, set tiktok to a country and back, the render holds VCTL-SVC-TIKTOK while chosen, sites answer; the owner's choice left at default.
