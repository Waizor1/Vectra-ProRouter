# Port forwards — the cross-layer contract (set_port_forwards / portForwards)

Frozen 2026-10-06. Owner of this contract: the ProRouter agent (vctl + panel).
Vectra Connect Backend / Frontend are another agent's: they consume this
document and do not change the router or the panel. A change to anything
below goes through this file first, with a new date line.

Source of truth in code (branch `feat/vctl-port-forwarding`):

- Go types: `internal/controlplane/types.go` (`ConnectPortForwards`,
  `ConnectPortForward`, `ConnectPortForwardDevice`), strict parse
  `internal/connectactions/actions.go` (`portForwardList`), codes
  `internal/portfwd/portfwd.go`, telemetry bounds
  `internal/connecttelemetry/connecttelemetry.go`.
- Router UI fixtures: `ui/contract/port_forwards.json`, README section
  "Port forwards".

## Path

FE → BE `POST /user/routers/{id}/actions` → Panel
`POST /api/partner/routers/{panelId}/actions` → `jobs` row
`connect_router_action` → vctl at check-in → result → Panel webhook
`router.action` `{actionId, state: applied|failed, detail: <code>}` → BE.

State: vctl telemetry `inventory.connect.portForwards` → Panel
`projectPartnerRouter` → BE snapshot → `snapshot_dto` → FE.

## Capability

`set_port_forwards: true` in the router's advertised capabilities. A layer
offers the feature only when the router advertises it; older routers simply
do not (the panel answers 409 `not_supported`).

## Action `set_port_forwards`

Params — the WHOLE list (replaces every Vectra rule; `[]` removes them all):

```json
{"rules": [
  {"id": "3fa1c09e", "preset": "minecraft-java", "destIp": "192.168.1.50",
   "port": "25565", "proto": "tcp", "direct": false, "enabled": true},
  {"destIp": "192.168.1.60", "port": "3478-3479", "proto": "both",
   "direct": true, "enabled": true}
]}
```

A rule is an object with EXACTLY these keys; unknown keys are refused:

| key | required | type / form |
|---|---|---|
| `id` | no (absent = new rule; the router assigns one) | 8 lowercase hex; unique in the list |
| `preset` | no; may be `null` | 1–24 of `a-z`, `0-9`, `-`; the router stores it, never checks it against a list |
| `destIp` | yes | IPv4 dotted quad (the router checks it is in its LAN) |
| `port` | yes | `"n"` or `"a-b"`, 1–65535, a ≤ b; same port outside and on the device |
| `proto` | yes | `tcp` \| `udp` \| `both` |
| `direct` | yes | bool — the device's own new connections go past the VPN |
| `enabled` | yes | bool |

`deviceName` is read-only (telemetry); it must NOT be sent in params.
Panel/BE mirror the shape and the syntax only; whether `destIp` is in the
LAN, conflicts, and the 32-rule limit are the router's to judge.

Limits a front layer may enforce early (same numbers as the router):
at most 32 rules; params body well under the router's 96 KiB.

### Result codes (`detail` of the `router.action` webhook)

| code | state | meaning for the user |
|---|---|---|
| `applied` | applied | saved and active |
| `invalid_params` | failed | a rule's shape/syntax is wrong (should not happen from a validating UI) |
| `too_many` | failed | more than 32 rules |
| `dest_not_lan` | failed | the address is not in the router's home network (or the LAN could not be read) |
| `dest_is_router` | failed | the address is the router itself |
| `port_conflict` | failed | the port is already forwarded (by another rule or outside Vectra) or used by the router |
| `busy` | failed | another port forward change is being applied, or uncommitted firewall edits wait in LuCI — try again |
| `apply_failed` | failed | the firewall did not take it; the previous forwards are back |
| `internal` | failed | the firewall did not reload and the router could not restore the previous state cleanly; contact support |
| generic Connect codes (`invalid_payload`, `unsupported`, `resource_guard`, `journal_unavailable`, …) | failed | as for any other action |

Replays of the same action id are answered from the router's journal
(`replayed`, same outcome). After `applied` the BE should refresh the
snapshot before showing the list (ids of new rules come from telemetry).

## Telemetry `portForwards`

`inventory.connect.portForwards` — absent/`null` when the router has no fw4
config (or the router predates the feature):

```json
{"rules": [{"id": "3fa1c09e", "preset": "minecraft-java",
            "destIp": "192.168.1.50", "deviceName": "gaming-pc",
            "port": "25565", "proto": "tcp", "direct": false, "enabled": true}],
 "devices": [{"name": "gaming-pc", "ip": "192.168.1.50"},
             {"name": null, "ip": "192.168.1.60"}],
 "cgnat": false,
 "directActive": true}
```

- `rules` ≤ 32 (a report with more drops the whole `portForwards`),
  `devices` ≤ 64; `preset`, `deviceName`, `name` may be `null`.
- `cgnat: true` → one warning line: nothing from the internet reaches a
  forward (the provider's shared address or a router in front).
- `directActive`: `null` = no rule asks for «past the VPN»; `false` = some
  enabled rule's `direct` is not in effect now (show one muted line: devices
  go through the VPN for now); `true` = in effect.
- No MAC address, no secret, nothing else. Device names are ≤ 63 printable
  runes. Every layer must whitelist these fields and drop anything else.

## Presets

The preset catalogue is the UI's (router UI `ui/app/src/lib/presets*`;
Connect FE keeps its own copy of the ids it shows). A preset may expand to
several rules (e.g. `rust` → 28015/udp + 28017/udp), all carrying the same
`preset` tag and `destIp`; a UI groups them by (`preset`, `destIp`). An
unknown tag is shown as a generic «Port N» rule. Where an official source
names a port but not its protocol (Minecraft Java/Bedrock, Valheim,
Enshrouded) the catalogue does NOT pre-fill `both`: the user picks the
protocol.

## Rollout order

vctl (release after r21) → panel → BE → FE. Each older layer just does not
offer the feature.
