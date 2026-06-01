# Parity corpus (vctl render vs PassWall2 gen_config)

This directory holds the **live parity oracle** corpus consumed by
`TestParityCorpus` (`../parity_test.go`). It is empty by default — the test
`t.Skip`s until pairs are supplied — because captured configs can contain real
SNIs/endpoints and require a router to produce.

## What a pair is

For each node/profile `<name>` you need two files:

- `<name>.passwall-xray.json` — the Xray JSON **PassWall2's `util_xray.lua`
  `gen_config` produced** on a real router for the node. Capture it read-only:

  ```bash
  scripts/Capture-XrayParityCorpus.sh \
    --router-host <ip> --router-user root \
    --known-hosts ./router-known_hosts --name <name>
  ```

  (The source of truth is `passwall2/luci-app-passwall2/.../util_xray.lua`
  `gen_config` / `gen_outbound`. Capturing the *generated* config from a router
  is equivalent and needs no offline Lua runtime.)

- `<name>.operator.json` — the **equivalent operator config** in our
  `internal/config` schema (schema 1), describing the same node. Author it to
  mirror the captured node (protocol, REALITY/TLS params, transport, flow, mux).
  `TestParityCorpus` renders this with our engine and diffs the parity-critical
  structure (per-tag outbound protocol/security/transport + routing tags)
  against the PassWall2 side.

## Why structural, not byte-exact

PassWall2 emits extra tags, different key ordering, and helper inbounds we don't
replicate 1:1. The oracle therefore compares the **parity-critical projection**
(see `project()` in `parity_test.go`), surfacing meaningful divergences for
review rather than failing on cosmetic noise. Tighten the projection as parity
matures.

## Files here are gitignored

Only this README and `.gitignore` are tracked. Captured/authored pairs stay
local (they may carry real endpoints). Keep them in your working tree while
iterating on parity.
