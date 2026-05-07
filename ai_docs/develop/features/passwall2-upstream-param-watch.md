# PassWall2 upstream parameter watch

Last reviewed upstream release: `26.5.1-1` (published 2026-05-01).

Use this when checking whether Openwrt-Passwall/openwrt-passwall2 added LuCI/UCI parameters that the Vectra panel should expose explicitly.

## Manual check

```bash
./scripts/Check-PasswallUpstreamParams.py --baseline-tag 26.5.1-1
```

If the latest upstream tag differs from the baseline, inspect every added/removed option line. New panel support must follow this rule:

1. Preserve unknown options through `extras` so old imports are not lost.
2. Add explicit UI controls only after mapping the exact upstream option name and runtime file that consumes it.
3. Gate new controls by router PassWall2 version. On older or unknown versions the control stays inactive and shows `Работает только с PassWall2 <version>+`.
4. Add regression tests for both UCI rendering/preservation and version-gate behavior.
5. Run the focused web tests, `@vectra/web typecheck`, `@vectra/web lint`, `@vectra/web build`, and controller-agent Go tests before shipping.

## 26.5.1-1 review result

Compared with `26.4.20-1`, upstream added/renamed these relevant parameters:

- `shunt_rules.protocol` gained value `quic` in `luci-app-passwall2/luasrc/model/cbi/passwall2/client/shunt_rules.lua`.
- Xray/Hysteria2 TLS chain pinning renamed from `tls_CertSha` to `tls_pinSHA256` in `luci-app-passwall2/luasrc/model/cbi/passwall2/client/type/ray.lua`; runtime consumption is in `luci-app-passwall2/luasrc/passwall2/util_xray.lua`.
- Xray mKCP now exposes `mkcp_mtu` in client/server ray LuCI forms and uses it in `util_xray.lua` instead of hard-coding `1350`.

Compared with the older `26.4.10-1` line still referenced by parts of the bootstrap/update lane, the `26.4.20-1` surface also introduced per-subscription domain resolver extras (`domain_resolver`, `domain_resolver_dns`, `domain_resolver_dns_https`) plus newer per-subscription `domain_strategy` values. Those are supported in the panel with a `26.4.20+` gate.
