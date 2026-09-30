# Provider document fixture

`entry-00.json` is a **real capture** from the production subscription
(`sub.provider.invalid`), taken 2026-08-05 from router `1111111111`
(Xiaomi AX3000T) with `User-Agent: Happ/1.0`.

> **Never repeat that capture with that agent.** The provider's anti-fraud
> sweep treats a "Happ…" agent that is not the real client's
> `Happ/<version>/<OS>/<build>` as a fake client: the device is deleted on the
> first request and the account disabled. vctl now refuses to send one
> (`internal/uaguard`).

It is entry 1 of 26 — `remarks: "🇷🇺🇪🇺 Авто Самый стабильный"`.
29 outbounds, 19 routing rules, 21,210 bytes (after the second scrub).

## Scrubbing

Credentials were replaced by **equal-shape** substitution, never by key
deletion, so the document keeps its exact structure:

| key | replacement |
|---|---|
| `id` (VLESS user UUID) | `00000000-0000-4000-8000-000000000000` |
| `publicKey` (REALITY) | a freshly generated, syntactically valid x25519 key |
| `shortId` | `00000000000000` |
| `password` | `REDACTED` |
| `auth` (hysteria2) | `00000000-0000-4000-8000-000000000000` |
| `path` (xhttp) | `/static/redacted` |

`auth` and `path` were missed by the first scrub (2026-08-05) and replaced on
2026-09-27; the earlier commits still carry the real hysteria2 credential, so
the branches holding them must have that history rewritten before any push
(the repository is public). `TestProviderFixtureCarriesNoCredential` now fails
on any credential-shaped value that is not a placeholder.

The `publicKey` must stay a **valid** x25519 base64url value (43 chars).
A dummy placeholder makes Xray reject the whole document with
`Failed to build REALITY config. > invalid "password"`, which looks like a
code defect but is only a bad fixture. Generate one with `xray x25519`.

## Invariants this fixture locks in

- top-level key order: `dns,log,stats,policy,routing,inbounds,outbounds,burstObservatory,remarks`
- `"tcpSettings":{}` x18 and `"stats":{}` x1 — and **zero** `:[]` anywhere.
  An empty object re-serialized as an empty array is the exact corruption that
  makes Xray die with `cannot unmarshal array into Go struct field ... tcpSettings`.
- both provider inbounds (`socks:10808`, `http:10809`) carry **no `listen` key**,
  so Xray would bind them to 0.0.0.0 — this is why the splice drops them.
- `inboundTag` in routing rules references only the internal loopback stages
  (`STAGE_MAIN`, `STAGE_MAIN_BACKUP`, `STAGE_WL`, `STAGE_WL_LV2`, `STAGE_WL_LV3`),
  never the entry inbound — which is why appending our own inbound is safe.
- no `allowInsecure` anywhere.

## Verified against a real engine

Spliced through `vctl render` and accepted by Xray 26.3.27 with
`XRAY_LOCATION_ASSET` pointing at the merged geo set: `Configuration OK.`
