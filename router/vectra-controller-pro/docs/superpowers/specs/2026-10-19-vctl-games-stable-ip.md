# vctl r28: a game never changes its address

Status: approved by the owner on 2026-10-19 ("сделай всё как надо; для игр смена айпи критична — её вообще нельзя допускать"). The design calls below are the implementer's.

## What went wrong on 1111 (2026-10-19)

Star Citizen kept dropping the owner, and two defects of r26 were behind it.

1. **The game itself never rode the stable exit.** «Игры · постоянный IP» covers only the catalog's names (launchers, sites). The game connects by address and has no name to sniff:
   - a hub on TCP 34.120.68.86:8000;
   - a game server on UDP 34.76.206.119:64xxx.

   Both fell to the provider's catch-all, BL-MAIN. Its leastLoad over two nodes, and the nodes themselves, change the client's address between connections.
2. **Moving the pin restarted xray.** At 23:16:48 the pin bridge-ae5 stopped answering. At 23:19:17 a round picked bridge-tr5 (1 steady node of 7). At 23:19:19 `xray exited (intentional restart)` dropped every connection in the house, the game included. The pin was part of the render (`VCTL-STABLE` selector = [pin]), so every move was a render, and every render is a restart.

## Goals

- Everything a game sends that the provider sends through its main path leaves from one address, and keeps it while the pin lives:
  - launchers and sites, by name, as in r26;
  - **gameplay, voice and calls, by traffic class.**
- A pin move never restarts xray and never touches traffic off the stable exit.
- A dead pin is replaced in seconds by another node that keeps one address. This happens without a restart, and never by guessing a node that rotates.

## Non-goals

- Changing what the provider routes elsewhere: Russian destinations (the kernel and RussiaDirect), BL-RU, AI, a service's country, block or direct rules.
- Per-game port lists, which are always incomplete. A new game must be covered without a catalog edit.
- Keeping a game alive across the death of its node. Its session is lost with the node, and the router's job is only to land it on a steady address again.

## Anti-goals

- No restart for a pin move.
- No direct exposure: the stable exit falls back only into the main path, as in r26.
- BitTorrent never moves onto the single stable node.

## Design

### A. The pin is runtime state, not part of the render

- **What renders.** The stable machinery (the `VCTL-STABLE` balancer, the `vctl-stable-back` loopback and its back rule) renders whenever a service needs it and the r26 safety conditions hold: an observatory, a main path, and a main path that never goes direct. A pin is not needed.
  - The selector holds **every dialling member of the main path**. A member that is a prefix of a non-member tag is dropped. Exits the exit check left out are removed by `leaveOut` as from any balancer.
  - Strategy leastPing, fallback `vctl-stable-back`.
- **How the pin is applied.** `RoutingService.OverrideBalancerTarget(VCTL-STABLE, pin)` sets it:
  - after every xray start, through the start hook, as owner pins are;
  - live, when a round moves the pin.
- **Who owns the override.** The failover policy already leaves alone a balancer overridden by someone else (`policy.go`: `Override != "" && o == nil → continue`), so the pin is the stable tracker's alone. An owner pin on `VCTL-STABLE` wins: the tracker does not touch it.
- **The render key** carries the modes and Main, not the pin. On upgrade, `reconcileRender` re-renders before xray starts, so there is no extra restart.
- **No pin** (none found, or no steady node and the old pin is gone): the override is cleared. The balancer chooses among the main path's nodes and falls back into it, the r26 «services ride the main path» behaviour.
- **Fast move.** The failover watchdog's connection-table detector also watches the pin. When the pin is failing, the move happens live, with no probe:
  - it goes to the next node of the last round's steady ranking (fastest first) that is not failing, not unfit and a member;
  - the new pin's address is rechecked at the next tick (`CheckedAt` zeroed);
  - if there is no such node, the override is cleared and a full round is due at once.

### B. Gameplay rides the pin, by traffic class

- **Catalog (schema 1, additive).** `Service.traffic`: up to 4 entries `{network: tcp|udp, except: [ports]}`. The entry means every port of that network except the listed ones. A service with traffic:
  - names nothing (no names, ips, markers, geosites, onlyKnown);
  - offers only `stable` and `main` (never direct, country or location: it would take most non-web traffic out of the tunnel);
  - may default to `stable`.
- **New service «Игровые соединения» (`gameplay`, group games, first).** UDP except 53/123/443, TCP except 80/443, default stable. Web (TCP 80/443, QUIC on UDP 443), DNS and NTP keep the main path.
- **Render (`insertMainCopies`).** For every provider rule whose target is the main path (the catch-all's target) and that applies to the tproxy inbound, a copy goes in front of it:
  - first, a BitTorrent pass-through to the rule's own target (`protocol: ["bittorrent"]`), when the rule names no protocol;
  - then, per traffic entry, a copy carrying:
    - the rule's matchers;
    - `inboundTag: [tproxy]`;
    - the intersection of networks;
    - the intersection of ports (an empty intersection skips the copy; an unreadable port field skips the rule);
    - the target `balancerTag: VCTL-STABLE`.

  Copies never carry `ruleTag`, because xray ≥ 26.9.8 refuses a duplicate. Rules to other targets are untouched. The owner's rules and the services' rules stay above.
- **Stable main** («Постоянный IP для всего VPN») still rewrites the catch-all. The copies of the explicit main rules stay, so they are on the pin too.

### After the code review (same day)

While pinned, xray takes **no** fallback of its own: an override skips the strategy and the observatory (`app/router/balancing.go`). The tracker therefore owns the pin fully.

- **One owner.**
  - The failover policy is handed the tracker's pin as an owner pin, so it never moves or releases it.
  - With no pin, the tracker clears only an override it set itself, never one the policy set.
- **Kept in line.**
  - Every look of the watchdog compares xray's override with the record and corrects it (every 2 s).
  - Every stable tick (1 min) does the same from xray's `GetBalancerInfo`, also on routers whose watchdog is off.
- **No lost update.** The record has a generation. A recheck or a round that read an older record never writes over a newer one. A round whose pin was moved off while it ran is discarded, and another round runs at the next tick.
- **Two words on a dead pin.**
  - The connection table: xray's own connections to it go unanswered (seconds).
  - The observatory: dead although it takes connections (next tick).
  - Either way the pin moves along the ranking, skipping nodes either word holds dead, and the dead pin leaves the ranking.
- **A WAN blip moves nothing.** Moving requires another node to have answered since the pin went silent, or, for the observatory, to be alive. Otherwise the router's own way out is down, and moving would change the game's address for good.
- **No stable exit where nothing would pin it.** `NoStableExit` is set when the exit check is off. Nothing is rendered there, because a never-pinned balancer would only move traffic.
- **BitTorrent.** A main rule naming BitTorrent gets no copy. Encrypted BitTorrent (MSE/PE) and DHT are not sniffable, so they do reach the pin. This is a known limit.
- **Edge cases in copies.**
  - `inboundTag: []`, `port: ""` and `network: ""` read as unset, as xray reads them.
  - Without a catch-all rule, no copies go at the end. They would run before IP rules that xray matches on its second pass.

### Rollout

- r28 carries the catalog built in (serial 2026102001).
- The published catalog moves only after the fleet runs r28. An r26/r27 router would show «Игровые соединения» without being able to render it.
- Fleet order:
  1. 1111, checked live: a pin move without `xray exited`; gameplay UDP from one address;
  2. then the other vctl routers through `scripts/Rollout-VctlFleet.py`.

## Left to the builder

- Log wording.
- The exact place of the detector hook in `failoverTick`.
- Test fixtures.
