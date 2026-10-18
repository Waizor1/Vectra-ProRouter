# vctl r26: services are data — «Игры», a stable exit, and no release for a service change

Date: 2026-10-18. Status: the owner's direction in chat. The owner opted out of
questions («не делай мозгу — сделай по красоте»); every call below not quoted
from the owner is the builder's.

Quotes (2026-10-18):

- «Вчера я нейросети с играми переключил на авто и у них постоянно менялся
  айпи у них и меня выкидывал» — in the Star Citizen launcher.
- «Сделай отдельный сервис «Игры» с закреплённым стабильным выходом».
- «Ток надо как-то иметь возможность настроить игры — например стим у меня
  отлично работает и без впн».
- «Стар ситизен не работает без впн».
- «Чтобы в будущем мы могли без релиза как-то править наши сервисы».
- «Сможешь сделать более отказоустойчивый пакет? А то постоянно обновлять
  роутер ради одной правки — это тумач».

## Why

Measured on 1111 on 2026-10-18 with `www.cloudflare.com/cdn-cgi/trace`:

- **The main path changes the client's IP between connections.** The main
  path is BL-MAIN, leastLoad, two nodes selected. It gave 4 exit IPs in 8
  requests.
- **Most nodes rotate the egress themselves.** Through the exit probe, six
  requests each:
  - bridge-nl5: 3 IPs;
  - bridge-de5: 3;
  - bridge-fin5: 2;
  - bridge-pl5: 2;
  - hy2-nl5: 3;
  - bridge-fr5: 1.

  So pinning a node is not enough. The node must be one that keeps one
  address.
- **«Нейросети» on «Авто» (the Kazakh cascade) kept one IP in 16 requests.**

A launcher that ties its login to the client's address (Star Citizen's)
logs out when the address changes. Since r24 games ride the main path.

There is also a second problem: what a service is, and how a router may route
it, is compiled into vctl, the router UI and the Connect MiniApp. Every change
is a release on three sides.

## What changes

### 1. The catalog defines the services

The catalog stays schema 1, with additive fields. r24 and r25 read schema 1
leniently, ignore what they do not know, and keep working with the fields they
know.

**`services.<id>`** keeps `onlyKnown`, `names` and `geosites`, and gains:

| Field | Meaning |
|---|---|
| `title` | `{"ru": …, "en": …, "zh": …}`: what the UIs show. Required for a service to be shown. |
| `group` | A key of `groups` (e.g. `games`); empty = top level. |
| `order` | Sort key within its group. |
| `icon` | A key from a fixed icon set both UIs carry (`ai`, `youtube`, `tiktok`, `telegram`, `games`, `steam`, `epic`, `riot`, `battlenet`, `starcitizen`, `globe`). Unknown → `globe`. |
| `marker` | The provider matcher that finds the service's own rule, e.g. `domain:steampowered.com`. Empty → the service has no provider rule of its own: «Как основной VPN» means the main path, and its names are the catalog's only. |
| `ipMarker` | The same, by address (`geoip:telegram`). |
| `ips` | CIDRs or `geoip:` names the service also covers by address (the same caps as sites). |
| `modes` | What the owner may choose, a subset of `main`, `stable`, `direct`, `country`, `location`. |
| `default` | The mode while the owner chose nothing: `main`, `stable`, `direct` or `location`. `location` = «Нейросети»' own `aiDefault` exits. |

**`groups.<id>`**: `title`, `order`, `icon`, and an optional `hint` (`title`
form) shown under the group.

**The built-in catalog** carries today's four services with the same
behaviour, plus the group `games` with:

- `steam`;
- `epic`;
- `riot`;
- `battlenet`;
- `starcitizen` (the RSI names and the Cloud Imperium ones).

Their names are the provider's from the shared 41-name rule plus a few known
ones. All of them are `onlyKnown` with `marker` = their first provider name.
The default for every game is `stable`: through the VPN, as the provider meant,
with one address.

**The code keeps** only:

- the mechanics;
- a fallback marker for the four old services, so a catalog lacking `marker`
  renders as r25.

Up to 64 services (r24 and r25 cap at 16 and refuse more: they then keep their
built-in catalog — safe).

### 2. Modes (per service, the owner's choice or the catalog's default)

| Mode | What it means |
|---|---|
| `main` «Как основной VPN» | No rule of vctl's. The provider's routing decides: today's behaviour. |
| `stable` «VPN · постоянный IP» | A rule above the provider's, with the service's names (`onlyKnown`-filtered provider names plus the catalog's) and `ips`, to the balancer `VCTL-STABLE`. Its only member is the stable exit (§3). Its fallback is a loopback into the service's own provider path, or the main path without one. A dead pin falls back, never to nothing. All stable services share the one exit, so a launcher and its CDN share an address. |
| `direct` «Напрямую» | The same names go to the plain freedom outbound (the one the owner's direct sites use). They are NOT FakeDNS names (§5), so their answers are real and Russian destinations stay in the kernel's direct path. |
| `country` | The existing country overlay (router UI). |
| `location` | The existing Connect location (`ServiceEntries`). |

**Precedence**, top to bottom:

1. the owner's sites;
2. the catalog's fleet sites;
3. direct services;
4. stable services;
5. country and location services;
6. the provider's rules.

**The owner's choices** are stored in the overrides as `serviceModes` (`id → mode`).

- Choosing `country` or `location` is stored where it is today. Choosing a
  mode clears the service's country and location.
- `:auto` clears all three, so the catalog's default applies.
- A choice for a service the catalog no longer has, or for a mode it no
  longer offers, is kept but not rendered, and is reported stale.

### 3. The stable exit

**Candidates** are the dialling members of the main path's balancer (the
catch-all's target) that the exit probe reaches. That is BL-MAIN's members on
today's provider. When the main path is a single outbound, it is the only
candidate.

**A round** asks each candidate, `samples` times (default 6), for the trace
URL (default `https://www.cloudflare.com/cdn-cgi/trace`), each time on a new
connection, and reads `ip=`.

- **Stable** means all samples answered with one address.
- **The pin:**
  1. the current pin, if it is still stable;
  2. otherwise the stable candidate with the lowest median answer time;
  3. none if no candidate is stable — then the stable services ride their
     fallback (the provider's path) and the status says so.

**When rounds run:**

- a full round 3 minutes after start, then every `everyHours` (default 6, ±10%);
- the pin alone with 3 samples every `recheckMinutes` (default 30). A pin that
  answers with a second address, or not at all twice, triggers a full round.

**Persistence:** the pin (`tag`, `ip`, `country`, `checkedAt`) is kept in
`/etc/vectra-controller-pro/stable-exit.json`, so a restart keeps the same
address. A pin whose tag the current document lacks is dropped.

**Rendering:** the pin is part of the render key, so a new pin is a new render
(xray restarts once). That happens only when the pin changes.

**The router-wide toggle `stableMain`** (overrides, default off; the
catalog's `stableExit.main` sets the fleet default):

- The provider's catch-all rule for the LAN goes to `VCTL-STABLE` too: every
  site through the VPN leaves from the one address. For a game whose servers
  are reached by address, and for anything else that logs out when the
  address changes.
- The fallback is the catch-all's own target.

### 4. Tunables without a release

`tunables` in the catalog. Each field is bounded and validated; an out-of-range
value refuses the whole catalog.

| Field | Meaning | Bounds |
|---|---|---|
| `stableExit.samples` | | 3–12 |
| `stableExit.everyHours` | | 1–48 |
| `stableExit.recheckMinutes` | | 10–240 |
| `stableExit.url` | | https; host `www.cloudflare.com` or `cloudflare.com` only (it must answer `ip=`) |
| `stableExit.main` | the fleet default of `stableMain` | bool |
| `door.rate`, `door.burst` | the device door | rate 10–100/s, burst 30–400 |
| `quic.refuse` | the fleet default of the QUIC refusal; the owner's `refuse_quic` UCI wins | bool |

### 5. FakeDNS follows the first matching rule

`proxiedNames` walks the rules in xray's order. A name whose first matching
rule (by an equal matcher) sends it direct is not a FakeDNS name, even if a
later provider rule proxies it. That is how direct games keep real answers.

### 6. Fault tolerance

- **A downloaded catalog whose render fails `xray -test`, where the built-in
  catalog's render of the same document passes, is quarantined.**
  - Its serial goes to `/etc/vectra-controller-pro/catalog.refused`.
  - The router renders with the built-in catalog (or the previous stored one)
    until a higher serial arrives.
  - `vctl catalog show` and the status say so.
- **A service whose marker finds no rule** in a location is still offered when
  it has `names`: `main` then means the main path. A service with neither
  marker nor names is not offered.
- **Every catalog field reaching the render** goes through the existing
  parsers: the site parser, geosite usability and the CIDR parser. Nothing in
  a catalog names a node or runs code.

### 7. The contract: what each layer passes on

The chain is: the router's check-in → the ProRouter panel's partner API →
the Connect backend (snapshot DTO) → the MiniApp.

**Every layer passes service descriptions through.** No layer knows the
services by heart any more: a service the catalog adds reaches the MiniApp
with no release anywhere.

**Every layer validates shape and bounds, never the set of ids.** A field
that breaks the bounds drops that field (or that service), never the
check-in.

#### Router → panel (check-in `connect` block)

```jsonc
"services": [{                    // ≤100, in display order
  "id": "steam",                  // ^[a-z0-9][a-z0-9-]{0,31}$
  "entryId": null,                // as today: the location it runs through, null = not a location
  "auto": true,                   // as today: the owner made no choice
  "entries": ["…"],               // as today (locations carrying it), optional
  "stale": false,                 // as today
  // r26, all optional (an older router sends none of them):
  "title": {"ru": "Steam", "en": "Steam", "zh": "Steam"},  // each ≤64 chars, no control chars
  "group": "games",               // a serviceGroups id, or absent
  "order": 10,                    // -1000..1000
  "icon": "steam",                // ^[a-z0-9][a-z0-9-]{0,31}$; unknown to a UI → its generic icon
  "modes": ["stable", "direct", "main"],  // ⊂ main|stable|direct|location, in the order to show
  "mode": "stable",               // what runs now: main|stable|direct|location
  "default": "stable"             // what «Авто» means: main|stable|direct|location
}],
"serviceGroups": [{               // ≤16, r26, optional
  "id": "games", "title": {"ru": "Игры", "en": "Games", "zh": "游戏"},
  "order": 10, "icon": "games",
  "hint": {"ru": "…", "en": "…", "zh": "…"}   // optional
}],
"stableExit": {                   // r26, optional
  "state": "pinned",              // pinned | none (no exit keeps one address) | checking | off (no exit probe)
  "country": "FR",                // ISO-2 or null; the IP is never sent
  "checkedAt": "2026-10-18T12:00:00Z",  // or null
  "main": false                   // the whole VPN leaves through it (set_stable_main)
},
"capabilities": [ …, "set_service_mode", "set_stable_main" ]
```

#### Actions (Connect → router), each gated by its capability

- **`set_service` `{service, entryId?, mode?}`.** `mode` is new
  (capability `set_service_mode`) and is one of `main`, `stable`, `direct`.
  When `mode` is present, `entryId` must be absent or null.
  - `mode: "main"` = today's `entryId: null` («Как основной VPN»).
  - `entryId: ":auto"` = the catalog's default («Авто»), as today.
  - `entryId: "<location>"` = a location, as today.
  - The router refuses a mode the service does not offer (`invalid_params`).
- **`set_stable_main` `{on: bool}`.** New, capability `set_stable_main`: the
  whole VPN through the stable exit.

#### The MiniApp

The MiniApp shows the services of `snapshot.services` that carry a `title`:
- grouped by `serviceGroups`, in `order`;
- each with the options its `modes` allow — «Авто» (`:auto`, labelled with
  what `default` means), «Как основной VPN» (`main`), «VPN · постоянный IP»
  (`stable`), «Напрямую» (`direct`), the locations (`location`, from
  `entries`);
- the current one from `auto`, `mode` and `entryId`.

A service without a `title` (an older router) keeps today's hard-coded
presentation of the four r25 services.

`stableExit` is shown in the group's header, or under the stable option:
«Постоянный IP · 🇫🇷», «Ищем узел с постоянным IP», «Нет узла с постоянным
IP — игры идут общим путём VPN». The toggle «Постоянный IP для всего VPN»
needs `set_stable_main`.

#### Router UI (rpcd `vectra`)

- **`services`** items gain the same `title` (in the UI's language),
  `group`, `order`, `icon`, `modes` (`country` included for the router UI),
  `mode` and `default`.
- **The answer** gains `groups` and `stableExit`.
- **Rpc `set_service_mode` `{id, mode}`.** `mode` is `""` for «Авто»,
  otherwise `main`, `stable` or `direct`.
- **Rpc `set_stable_main` `{on}`.**

## What does not change

- The provider's document is still the base.
- The owner's sites win over everything.
- Port forwards, the kill switch, failover and exit checks are untouched.
- `country` and `location` behave as in r25.

## Proof

**Unit tests:**

- catalog validation of every new field and bound;
- services built from the catalog;
- each mode's rules and their precedence;
- the stable balancer and its fallback, including no pin;
- the `stableMain` catch-all rewrite;
- FakeDNS first-match;
- the picker: stable/unstable, stickiness, recheck, persistence, a missing tag;
- quarantine;
- rpc and Connect actions;
- telemetry.

**Elsewhere:**

- The data-plane stand vs the r25 baseline.
- 1111:
  - games on `stable` → one address over 16 requests through a game name;
  - Steam on `direct` → the home address;
  - the pin survives a restart;
  - a catalog-only change (a new service published) shows in both UIs without
    a release.
