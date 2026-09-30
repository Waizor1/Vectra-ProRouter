# vctl: servers by service, the subscription's own failover, seconds to recover

Date: 2026-09-30. Status: approved by the owner in chat (2026-09-29 23:59 MSK:
«Да, пиши спеку и делай»). Router under test: 1111111111 (AX3000T, 234 MB).

## Why

On 2026-09-29 the provider's Netherlands route (RU entry, :50055) stopped
answering. 1111 ran vctl's native routing (route_source `native`, the PassWall
port of r17): five slots, each pinned to ONE node, no health checks. Both
WorldProxy and YouTube sat on the dead node, so every proxied site and the
router's own tunnelled DNS were down from ~23:18 to 23:39 MSK, with vctl, xray,
the dead-man and the reporter all silent — nothing had crashed. The owner could
not switch servers either: the router UI offers the subscription's 28 locations,
and vctl refuses `select_entry` in native mode by design. Under PassWall this
never showed: the fleet panel's self-heal moved dead slots, and vctl native
takes no part in it.

The owner's direction (2026-09-29, verbatim in substance):

- one main VPN (what WorldProxy + Special were), where sites are added;
- a few services with their own server choice, like on the phone (TikTok:
  default or another server) — only where it is really needed; everything
  else goes to the main VPN;
- own services added with a «+», the router works out what goes where;
- by default the subscription's Auto servers work;
- check that the subscription JSON arrives right and the router's xray runs it
  optimally — it is our software for our servers;
- recovery is automatic and fast: «every millisecond counts… in a match a long
  disconnect kicks you out»; the safety mechanisms must work and be proven.

## What the subscription already is (read on 1111, 2026-09-29)

28 entries, each a complete xray config (`provider-entries.json.gz`, fetched
with the router's signed UA): «🇷🇺🇪🇺 Авто Самый стабильный» (20 nodes,
7 balancers), «🇪🇺 Авто Самый быстрый» and «🇪🇺⚡ Авто ULTRA» (44 nodes),
a country pair per location (direct and via a RU entry, 15 nodes each),
«Россия YouTube», «Обход глушилок». In an Auto entry:

- `BL-MAIN` (leastLoad over the country bridges, or leastPing over direct +
  bridges) → fallback `stage-wl` → `BL-WL-LV1/2/3` (whitelist nodes);
  `BL-MAIN-BACKUP` (hysteria2) → stages; `BL-TK` (TikTok, Telegram);
  `BL-RU` (a RU bridge: YouTube «без рекламы» and Russian sites);
- rules in order: UDP to Meta blocked; stage inbounds → stage balancers;
  `protocol bittorrent → DIRECT`; TikTok, Telegram → BL-TK; ChatGPT, Canva…
  → BL-MAIN; YouTube → BL-RU; gov/VK → DIRECT; `category-gov-ru`, `.ru/.рф/.by`,
  `geoip:ru` → BL-RU; private → DIRECT; **catch-all tcp,udp → BL-MAIN**;
- `burstObservatory`: interval 300 s (ULTRA 180 s), timeout 10 s, sampling 2,
  destination a generate_204.

xray 26.7.28 on 1111 accepts both Auto entries as they are (`xray run -test`:
Configuration OK — hysteria2, xhttp, ws, reality, loopback stages).

vctl's provider mode (route_source `provider`) already renders these entries:
it replaces the inbounds (TPROXY, DNS), marks the outbounds, keeps balancers and
observatory, adds the owner's sites on top (`UserRules`), serves `select_entry`,
`pin_balancer`, `set_probe_interval`, and reads the observatory through xray's
API/metrics. What it lacks is below.

## Decisions

1. **The engine is the subscription entry**, route_source `provider`, entry
   «🇷🇺🇪🇺 Авто Самый стабильный» by default. 1111 leaves native routing.
   The server card chooses an entry (Auto stable / fast / ULTRA / a country).
   Native routing stays in the code for PassWall routers of the fleet.

2. **Russian sites go direct**, not through the RU bridge: in the entry's
   rules, a rule whose target is `BL-RU` and whose matchers are Russian
   (`geoip:ru`, `geosite:category-gov-ru`, `domain:ru`, `domain:xn--p1ai`,
   `domain:su`, `domain:by`, `domain:рф`) is rendered to `DIRECT` in place.
   YouTube's rule keeps `BL-RU` (the provider's ad-free path). The order of
   rules is the provider's; nothing moves ahead of YouTube.

3. **Three services have their own server**: YouTube, TikTok, Telegram. Each
   is «По умолчанию» (the entry's own rule, untouched) or a country present in
   the running entry. A country choice adds, at the top of the routing, one rule
   (`geosite:youtube` / `geosite:tiktok` / `geosite:telegram` + `geoip:telegram`)
   to a balancer `VCTL-SVC-<SERVICE>` whose selector is that country's outbounds
   in the entry (tags `direct-<cc>*`, `bridge-<cc>*`, `sticky-<cc>*`,
   `hy2-<cc>*`), strategy leastPing, fallback the service's own target in the
   entry — a dead country falls back to the default, never to nothing. The
   observatory covers the new balancer's nodes. Countries are offered only when
   the entry names outbounds in that convention; otherwise only «По умолчанию».

4. **«Мои сайты» plus «+ сервис»**: the owner's sites stay two lists (через VPN
   / напрямую). «+ сервис» searches a catalog — the `geosite` categories the
   router's geo file carries, with human names for the common ones — and adds
   `geosite:<category>` to one of the two lists. A plain domain can still be
   typed. No per-country targets for custom services (kept small, as asked).

5. **Recovery in seconds, at no traffic cost.** A failed node must stop
   receiving NEW connections within **10 s**. Probing every node that often
   is not the way: each observatory probe is a fresh connection through a node,
   ~22 nodes every 10 s would be ~30 GB a month against the client's quota and
   our servers. Instead vctl watches what xray already does:
   - **the failover watchdog** runs in its own goroutine (the daemon's loop can
     be busy for minutes — a terminal job blocks it), every 2 s: it reads the
     router's conntrack for xray's connections to the node endpoints of the
     render (addresses resolved as xray resolves them). A node with ≥ 2
     connection attempts, made since it last answered a new connection, left
     unanswered for ≥ 2 s is **failing** (TCP SYN_SENT [UNREPLIED]; UDP
     unreplied for hysteria2). Tonight's outage looked exactly like this.
     *(Amended 2026-09-30 in implementation: the first wording — «and no
     answered connection opened in the last 10 s» — made a node in use until
     the moment it died wait ten seconds for its last answer to age, so the
     10 s below could not be met; the table is read through ctnetlink, or
     /proc/net/nf_conntrack on a kernel without it.)*
   - when a balancer's current targets (`GetBalancerInfo`) include a failing
     node, vctl re-points that balancer (`OverrideBalancerTarget`) at the best
     node of its selector that the observatory holds alive and the watchdog
     does not hold failing, and confirms with one request through the main
     path — to the observatory's own destination, any answer counting as the
     observatory counts it; a failed confirmation moves on to the next
     candidate;
   - the override is released when the node answers again (answered
     connections in conntrack), no sooner than 30 s after the move — a node
     that answers one moment and drops the next must not swing the balancer —
     or at once when the observatory has marked it dead, so the balancer
     returns to its own choice; a pin the owner set is never touched;
   - the observatory keeps the provider's own interval (300 s): it is the list
     of candidates, not the alarm. `ProbeInterval` stays as it is;
   - the time from «the node stops answering» to «new connections flow through
     another node» is measured on the stand and must be ≤ 10 s;
   - when every node of `BL-MAIN` is dead, the entry's own fallback stages
     carry the traffic (as the provider designed); when those are dead too,
     vctl's rescue (direct) takes over — in provider mode its probe does cross
     the main balancer (catch-all), unlike native mode tonight.
   Established flows through a dead node die with it (TCP cannot move); games
   reconnect onto a live node.

6. **Seen and reported.** The server card shows the node the main balancer
   uses and «переключились с X» when it changed away from a dead one. A new
   incident `PROXY_FAILOVER` (medium) is queued by vctl when the main path's
   node was declared dead, with the time to recover; `PROXY_DOWN` (critical)
   when no node of the main path answers for 60 s.

7. **Migration of 1111**: route_source `provider`, entry index of «Авто Самый
   стабильный» by remark, services «По умолчанию». From the native policy only
   what the new model would otherwise send direct is carried into «Мои сайты →
   через VPN»: entries of WorldProxy/Special that are Russian by the rules of
   decision 2. The native store stays on disk (turning back is one UCI switch).

8. **Peak load: held, not collapsed** (added 2026-09-30 04:30, from the
   night's evidence on 1111 — the owner: «роутер должен быть одновременно
   производительным и устойчивым к пиковым нагрузкам; потенциально любое
   скачивание файла такое вызовет»).
   - A torrent client's tracker storm went through xray to the entry's one
     German node, hundreds of handshakes a second; the German address then
     stopped answering the router at all (279 SYNs in 14 s, none answered)
     while it served the provider's other clients. Bursts to one foreign
     address are dangerous in themselves, not only slow.
   - Its peers, which the provider sends DIRECT, went through xray: two
     sockets and their buffers each; 15 MB of the router's memory left.
   - So: **P2P goes by the kernel** — a device opening connections to
     non-web ports faster than a person does is a P2P host for five
     minutes, and its non-web connections leave with the kernel-direct
     conntrack bit (web ports, push/chat, STUN and Discord's voice range
     stay with xray); **a device's storm waits at the door** — new
     connections past 40/s (burst 200) dropped before xray, TCP retries;
     **xray's dials to one node are paced** — past 20/s (burst 80) the SYN
     is held back by the kernel; **a burst is not a death** — with 16+
     dials outstanding the watchdog waits 8 s of silence, not 2; **a dead
     country is borrowed around** — when a balancer and its whole reserve
     are down, the watchdog lends it the entry's fastest live node of
     another country (never a Russian exit) and hands back once the entry's
     own path answers; closed connections leave conntrack after 30 s.

## Out of scope now

Probing individual outbounds on a timer *(amended 2026-09-30: every 10
minutes the router asks each foreign exit for blocked sites — the exit
check, r26 — after 1111's American exit was found failing every blocked site
while the observatory held it alive; one request per healthy exit. Probing
every node every few seconds stays out)*; the kernel bypass of `geoip:ru` in provider mode (it needs fake-DNS for every
proxied domain first); per-country targets for «+ сервис»; the fleet panel.

## Verification

- Go tests: the RU-direct rewrite (YouTube untouched, order kept), the service
  overlays (rule at the top, balancer selector by country, fallback to the
  service's own target, no countries without the tag convention), the
  watchdog's policy (failing by conntrack, candidate choice, confirmation,
  release, owner pins untouched), the conntrack parser, migration (which
  native entries are carried), the incidents.
- Stand: the provider fixture grows to the real entry's shape (BL-MAIN over
  three nodes, BL-TK, BL-RU, stages, bittorrent direct, catch-all); a new mode
  `failover`: traffic through the main balancer, the used node's port dropped
  in the inet namespace, the time until a request succeeds through another node
  asserted ≤ 10 s, then the node restored and the override released; a
  negative control with the watchdog off, where the assertion must fail. The
  `ui` mode covers the services and «+ сервис».
- Live on 1111, in this order: render the chosen entry with vctl and
  `xray -test` it there; switch route_source; sites through every service;
  then a live failover drill: drop the main node's address on the router with
  nft for 60 s and time the recovery; restore.
- Both stands green and a fresh review before anything reaches 1111; the owner's
  rule stands: only a proven data plane goes live.
