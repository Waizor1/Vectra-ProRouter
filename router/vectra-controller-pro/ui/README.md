# Router UI

The customer's and the operator's window into vctl on the router, in two views:

- **Setup wizard** (on a router that was never set up): the router's password
  first, when it has none (anyone on the LAN can open its settings until then;
  LuCI's own change, the one step that cannot be skipped), the internet (the
  router connects by itself; the step only checks it and says when the cable
  is missing), Wi-Fi (a unique name, a generated password, a QR to join),
  linking to the owner's Vectra account (QR for the Vectra app, a code, or a
  Telegram deep link), and a server. Every step checks itself first, so a
  router the operator prepared goes straight through.
- **Simple** (what it opens with): does the VPN work, in plain words; the
  location and a list to switch it; "My sites" (always without / always
  through the VPN); restart, another location, a report for support; the
  router's password (a calm note while there is none, "Change the password"
  in the footer); where this page opens. No xray, balancers, nodes or server
  addresses.
- **Pro**: an honest verdict, xray balancers and the fallback chain, nodes,
  locations and the journal.

The operator can lock a router to the simple view (`ui_lock`, see
[`contract/`](contract/README.md#operator-lock)); vctl then refuses the Pro
methods itself. It runs inside LuCI, talks to one ubus object (`vectra`) —
and, for the router's password, to LuCI's own `luci.setPassword`
([contract](contract/README.md#router-password)) — speaks ru, en and zh, and
wears Vectra Connect's design language (palette, wordmark, Montserrat — see
`src/styles/app.css`).

```
ui/
  contract/   the data contract: README + one fixture per method (shared with the Go tests)
  app/        the app: Vite + Preact + TypeScript, vitest
```

It ships as two files in the router package:

| file | what |
|---|---|
| `openwrt/files/www/luci-static/vectra/vectra-app.js` | the whole app: one self-contained IIFE, built from `app/` |
| `openwrt/files/www/luci-static/resources/view/vectra/app.js` | the LuCI view that loads and mounts it |

## Develop

```sh
cd ui/app            # Node >= 22.18 (the build imports strings.ts with Node's type stripping)
npm install
npm run dev          # http://127.0.0.1:5178/
```

The dev page wraps the app in a rough copy of LuCI's chrome and answers every
call from a stateful mock built on `contract/*.json` (~300 ms per call):

| query | effect |
|---|---|
| `?scenario=healthy` | the fixtures as they are (default) |
| `?scenario=reserve` | every node of the main balancer down: its traffic runs on the backup |
| `?scenario=degraded` | xray in backoff, API unreachable, `selected: null`, nodes unprobed, failing checks, an `other` matcher, a `malformed_happ` subscription |
| `?scenario=down` | every call rejects like LuCI does when the rpcd plugin is missing (`ubus code 4`) |
| `?scenario=empty` | a fresh router: no subscription, no balancers, no nodes |
| `?scenario=unboxed` | straight out of the box: the setup wizard, the router's password first (OpenWrt ships without one; the router comes online by itself a few seconds after the page opens, and gets linked a few seconds after the Vectra step shows its code) |
| `?scenario=boxed` | a box as it ships: Wi-Fi and the router's password from the card, so no password step |
| `?scenario=off` | Vectra switched off (`vectra off`): PassWall2 carries the traffic — `&holder=agent\|direct` for the others |
| `&wifi=manual\|overlap\|down` | the Wi-Fi as an owner may have it, for the wizard's verdicts: a country of their own with 5 GHz on a radar channel at a power they set (left alone); 2.4 GHz on channel 3; 5 GHz on and not up (both suggest the boost) |
| `&lang=ru\|en\|zh` | the host page's language (what LuCI puts in `<html lang>`) |
| `&frame=0` | no LuCI chrome |
| `&locked=1` | the operator's lock: simple view only, Pro methods refused |
| `&pwfail=refused\|denied\|offline` | LuCI's password change does not take it / the session expired / the connection drops |
| `&latency=0` | answer instantly |

Mutations change the mock's state: `select_entry` answers `pending` and lands
1.5 s later (the UI keeps polling until `status` shows it), `reset_entry`,
`pin_balancer` / `unpin_balancer`, `set_probe_interval` and `restart_xray`
behave like the router. So does `set_power`: `pending`, the switch at once and
the stacks 1.5 s later; `busy` while a change is in flight; `power_on` /
`power_off` when it was so already.

## Test

```sh
npm test             # vitest, happy-dom
npm run typecheck    # tsc --noEmit
```

The suite covers the i18n table (every key in all three languages, identical
placeholders, plural forms, no Cyrillic in en/zh), the formatters, flag
parsing, a sentence for every diagnostic id and action code listed in
`contract/README.md` (read from the README itself), raw fallbacks for unknown
codes, normalization (every fixture survives unchanged; `{}` never produces
`undefined`), the fallback chain, the store (no overlapping requests, timeouts),
the inflater, the LuCI view itself (evaluated the way LuCI's loader does), and
the whole app mounted in every scenario × tab × language, with dialogs, actions,
`pending` answers that land or silently do not, polling, visibility, unmount
and blocked storage, and provider data that names things `__proto__`,
`constructor` or `toString`. `test/real/` holds answers
captured from a real vctl behind rpcd + LuCI (the UI stand in
`test/dataplane`); the app must render them — also in a tab that is hidden
when it mounts — without ever sitting on its loading skeleton.

## Build

```sh
npm run build                # writes ../../openwrt/files/www/luci-static/vectra/vectra-app.js
ANALYZE=1 npm run build      # plus approximate bytes per module
```

- One IIFE, `es2019`, no source map, no hashes or timestamps: the same sources
  give the same bytes. It defines `window.VectraApp` and nothing else.
- The CSS lives in the bundle and is injected into the app's shadow root.
- Budget: **110 KB minified** (KiB), enforced by the build. Today: **101.3 KB
  min, 51.2 KB gzip** — two views and a ru/en/zh sentence for every state.
- Next to it the package installs the brand files the stylesheet points at:
  `vectra-wordmark.png` (the official wordmark, used as a mask) and
  `fonts/montserrat-{500,600,700}.woff2` + `OFL.txt` (~17 KB each). The dev
  server serves the same package web root, so the paths match the router.
- uhttpd serves LuCI's files as they are on flash, uncompressed. The build
  therefore stores the two text payloads — the CSS (20 KB) and the ru/en/zh
  string table (27 KB) — raw-deflated and base64-encoded; `src/lib/inflate.ts`
  (a ~1.8 KB DEFLATE decoder) restores them once at startup, in a few
  milliseconds. Without this the bundle would be ~99 KB. `npm run dev` and the
  tests use the plain sources.
- The build also stamps the view's `APP_VERSION` (package version + digest of
  the bundle), so a browser never keeps a stale app after an upgrade.

Commit the two files under `openwrt/files/www/` together with the sources.

## How LuCI mounts it

`view/vectra/app.js` is an ordinary LuCI JS view (`'require view'`,
`'require rpc'`):

1. `load()` injects `<script src="/luci-static/vectra/vectra-app.js?v=<APP_VERSION>">`
   and resolves once `window.VectraApp` exists (a failed load renders a plain
   LuCI alert instead of throwing).
2. `render()` creates a host `<div>` and calls
   `window.VectraApp.mount(host, { call, setPassword, lang })`, where `lang`
   is `document.documentElement.lang` and `call(method, params)` is
   `rpc.declare({ object: 'vectra', method, params: Object.keys(params),
   expect: { '': {} }, reject: true, nobatch: true })` applied to the values.
   - `reject: true`: without it LuCI resolves a failed ubus call ("Object not
     found", "Permission denied") to `{}`, and the app would render an empty
     router instead of the error.
   - `nobatch: true`: LuCI holds batched calls until the next
     `requestAnimationFrame`, which browsers pause in background tabs.
   - `setPassword(password)` is LuCI's own `luci.setPassword` for root (the
     call System → Administration makes), declared on first use the same way,
     `expect: { result: false }`: `true` only when LuCI took the password. A
     host that hands none gets no password step and no password actions.
3. `handleSave`, `handleSaveApply` and `handleReset` are `null`: no footer.

`mount(host, { call, setPassword, lang })` returns `unmount()`. The app renders into
`host.attachShadow({ mode: 'open' })`; all styles, dialogs and toasts live in
that shadow root, `:host` resets what LuCI's theme would otherwise inherit. Full
screen is a fixed overlay at z-index 850 — above LuCI's header (800), below
LuCI's own modal layer (900), so a session-expiry login still shows.

## Inside the app

```
src/
  main.tsx, mount.tsx       the IIFE entry and mount()
  api/                      contract types, normalization, the polling store, error classes
  app/                      shell: header, tabs, polling, actions, dialogs, toasts
  views/                    Overview, Balancing, Nodes, Locations, Journal
  lib/                      formatting, flags, verdict, labels, matchers, fallback chain, storage, inflate
  i18n/strings.ts           every sentence as [ru, en, zh]
  mock/                     fixtures, scenarios, the stateful mock transport (dev and tests only)
  styles/app.css            Vectra tokens (from apps/web/src/styles/globals.css) + a derived light theme
```

- **Codes, not prose.** The router sends diagnostic ids with params and action
  codes; `i18n/strings.ts` owns every sentence. A new diagnostic id needs
  `d.<id>.t` (title), `.ok` and `.bad`; a new action code needs `a.<code>`.
  Unknown ids and codes are shown raw, never dropped.
- **null is unknown.** Normalization turns missing fields into `null`, and every
  formatter renders `null` as "нет данных" / "no data" / "无数据", never as 0.
- **Polling.** `status` plus the visible tab every 10 s; diagnostics every 60 s
  outside the Overview (the header's verdict needs them). The first load always
  happens; later refreshes pause while the page is hidden and resume at once
  when it becomes visible. A method never has two requests in flight; a call
  that has not answered in 30 s is abandoned so the next poll can retry. When a
  refresh fails, the tab keeps its last data under a "last data received"
  banner with the raw error.
- **Actions** disable every mutating control while one is running, confirm
  first when xray restarts, and toast the localized `action.code` with
  `detail` as secondary text. `pending` means accepted and still applying, kept
  only if it succeeds: the UI asks `status` for up to 30 s (location, probe
  interval, pins, a new xray pid) and then says either that it landed or,
  neutrally, that it did not take effect. A running action's button stays
  focusable (`aria-disabled`, `aria-busy`), so keyboard focus survives the
  confirm dialog; if the control disappears afterwards, focus moves to the panel.
- **Provider data is data.** Tags, levels, counters and check ids are never used
  as keys of plain objects: lookups go through `Map`/`Set`, null-prototype
  dictionaries or `lib/own.ts`, so a balancer called `__proto__` is just a name.
- **Layout** follows the app's own width (LuCI's column, not the viewport):
  a ResizeObserver sets `data-w` = `s` / `m` / `l` / `x`.
- **Preferences** (tab, theme, language, full screen, filters, journal
  auto-refresh) are stored under `vectra.ui.*`; every storage access is wrapped.
