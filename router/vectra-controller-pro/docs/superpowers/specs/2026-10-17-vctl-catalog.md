# vctl r24: «Нейросети» are AI only; a signed catalog we change without a release

Date: 2026-10-17. Status: the owner's direction in chat (2026-10-17 ~20:50 MSK):
«надо разделить нейронки со всем остальным», «нам нужна версия vctl которую не
надо будет каждый раз новый релиз делать чтобы настройки поменять — чтобы мы
могли ее настраивать»; one release for both; the settings live in a signed
file on our server. Router under test: 1111111111 (AX3000T, 234 MB, r23).

## Why

On 2026-10-17 the owner's home PC (lan4, 1 Gbit/s) downloaded Star Citizen
at 1.6 MB/s and a Steam game at 0.7 MB/s. The router was not the limit (CPU
50–60 %, xray 11 %): over 6 s the PC received 58.06 MB and the router read
58.07 MB from one node — `kz4…:443`, tag `vctl-connect-ai-sticky-kz5`, the
Kazakh exit of the service «Нейросети» (observatory delay ~1 s, ~80 Mbit/s
for everything). The main path (BL-MAIN) carried 223 Mbit/s on one stream at
the same time. Eight new connections from a LAN client to Steam, Star
Citizen, Epic Games and ChatGPT each added eight connections to the Kazakh
node; Canva, Microsoft, Spotify, Reddit and Twitch went to BL-MAIN.

The cause is two things together:

1. The provider's entries (every location) have ONE rule of 41 names to
   BL-MAIN: the AI services (chatgpt.com … deepmind.google) **and** games
   (steampowered/steamcommunity/steamstatic/steamcontent/steamusercontent/
   steamserver, epicgames/fortnite/unrealengine/easyanticheat/easy.ac,
   riotgames/riotcdn/leagueoflegends/playvalorant, battle.net/blizzard/
   blizzardcdn, robertsspaceindustries/cloudimperiumgames/rsilauncher/
   spectrum.chat). For the provider that is harmless: both go to BL-MAIN.
2. vctl takes the rule that names `domain:chatgpt.com` as «what the entry
   calls the AI service» and routes its WHOLE list through the service's own
   location (`servicePath` → `withExtra(dom.matchers)`). The default location
   of «Нейросети» is Kazakhstan, so every game went there.

And the list of what is AI (`ai_domains.go`), the Kazakh preference
(`aiCandidates`) and every other such judgement are compiled into the binary:
each correction is a package release.

## What changes

### 1. A service keeps only its own names (the fix)

A service may say that the provider's list is not to be trusted as a whole:
`onlyKnown`. For such a service the provider's rule still decides the path
(the rule that names the service's marker), but the overlay matches only the
provider's matchers that name one of the service's known names, plus the
known names themselves:

- `domain:X`, `full:X` or a plain `X` is kept when X is a known name or a
  subdomain of one (`domain:gemini.google.com` is kept for
  `full:gemini.google.com`; `domain:google.com` is not, it is the parent);
- `geosite:N` is kept when N is one of the service's known geosites;
- `keyword:`, `regexp:`, `ext:` and anything else is dropped (nothing proves
  what they match).

«Нейросети» is `onlyKnown`; YouTube, TikTok and Telegram keep the provider's
list as today. What the AI service drops stays where the provider sends it
(BL-MAIN for the games above).

### 2. The catalog

One JSON document, `catalog.json`, the knowledge that was compiled in:

```json
{
  "schema": 1,
  "serial": 2026101701,
  "services": {
    "ai":       {"onlyKnown": true, "names": ["domain:openai.com", "…"], "geosites": ["openai", "…"]},
    "youtube":  {},
    "tiktok":   {},
    "telegram": {}
  },
  "aiDefault": {"exits": [["RU", "KZ"], ["KZ"]]},
  "sites":     {"direct": [], "proxy": []},
  "refreshHours": 6
}
```

- `services.<id>`: `names` are added to the service's overlay (what
  `AIDomains` was for «Нейросети»), `geosites` are known geosite names,
  `onlyKnown` as above. The service set itself (ids, the marker that finds
  the provider's rule) stays in the code: a new service needs UI and Connect
  support anyway.
- `aiDefault.exits`: where «Нейросети» go while the owner chose nothing — the
  provider's locations whose flags read exactly so, best first (a plain
  location before one named for a single service, as today). `[]`: no
  default, «Нейросети» stay on the running location's own path.
- `sites`: fleet-wide sites in the form «Мои сайты» takes (`domain`,
  `geosite:`, an address or a network), rendered right after the owner's own
  sites: the owner's choice wins (a site the owner lists either way is not
  repeated), the fleet's wins over services and the provider. Up to 300 each.
- `refreshHours`: how often a router asks for a new catalog, 1–48.

The binary carries the same document as its built-in default
(`catalog/default.json`, embedded): a router without a downloaded catalog, or
whose catalog fails a check, renders exactly as with the default. The
default is r24's knowledge, so the fix works before anything is published.

### 3. Delivery and trust

- Published next to the feed the router installs from: the parent of the
  `vectra_pro` feed URL in `/etc/opkg/customfeeds.conf` —
  `https://api.vectra-pro.net/artifacts/openwrt/<channel>/catalog.json` and
  `catalog.json.sig` (usign, the same key as `Packages.sig`, signed on the
  build host with `feedtool sign`; the key never leaves it).
- The router accepts a catalog only when: https; ≤ 256 KB, signature ≤ 4 KB;
  the signature verifies with a key in `/etc/opkg/keys` **and** that key also
  signs the channel's `Packages` (an OpenWrt key in the same directory is no
  authority for routing); `schema` is 1; `serial` is greater than the stored
  one (equal serial: identical bytes, else refused — an old signed file
  cannot be replayed); every list parses and is within its caps.
- Stored as plain files (nothing secret) `/etc/vectra-controller-pro/catalog.json`
  and `.sig`, written atomically, only when the serial changes.
- The daemon asks at start (once the network is up) and every
  `refreshHours` ± 10 % jitter; `vctl catalog refresh` asks now (a panel
  terminal job, ssh), and the Connect action that refreshes the subscription
  refreshes the catalog too.
- A new catalog joins the render key: the router renders again and reloads
  xray through the usual path (`xray -test`, last good). A render the new
  catalog breaks is refused like any other; the router keeps running.
- `vctl catalog show` prints the source (built-in / downloaded), serial and
  what it holds; the router UI status carries `catalog: {serial, source}`.
- `vctl catalog reset` drops the downloaded catalog (back to built-in).

### 4. Our side

- The source of truth is `router/vectra-controller-pro/catalog/catalog.json`
  in git (reviewed like code). `catalog/default.json` is what a release
  embeds; they start equal.
- `scripts/publish-vctl-catalog.sh --channel pro-canary [--dry-run]`: checks
  the document with the same parser as the router (`vctl catalog check`, run
  on the Mac), refuses a serial not above the published one, ships it to the
  build host, signs it there, verifies the signature, backs up the channel's
  previous catalog, puts both files into the channel directory atomically and
  reads them back over HTTPS.
- Rollback = publish the old content with a NEW serial.

## What does not change

The provider's document is still the base; owner's sites, service country
choices, port forwards, the kill switch and failover are untouched. Nothing
in the catalog can run code or name a node: it only selects among the
provider's own outbounds and rules, and sites go through the same parser as
«Мои сайты».

## Proof

- Unit: the provider's real 41-name rule → the AI overlay holds the AI names
  only, games stay on BL-MAIN; a country choice for «Нейросети» likewise;
  YouTube/TikTok/Telegram unchanged; catalog verification (good, wrong key,
  OpenWrt key not signing the feed, lower serial, same serial other bytes,
  too large, schema 2, bad site); `aiDefault.exits` order and `[]`; fleet
  sites under the owner's; render key follows the serial.
- Data-plane stand on r24 vs the r23 baseline (the stand's 75 known drifts).
- 1111: Steam, Star Citizen, Epic connections → BL-MAIN, ChatGPT → KZ; the
  PC's downloads at main-path speed; publish a test catalog (e.g. `aiDefault`
  exits changed) and see the router take it without a release.
