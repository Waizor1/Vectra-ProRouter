# Changelog

## v0.1.0-alpha (initial drop)

**First testable build.** Local dev only — does not deploy to a router yet.

### Subsystems shipped
- Operator config schema (`internal/config`) — full Xray feature surface; strict JSON (`DisallowUnknownFields`); schema-version pinned; defaults audit.
- Subscription engine — V2RayN base64 + URI parsers (vless, vmess, trojan, shadowsocks, hysteria2); PassWall2-impersonation HTTP fetcher (UA + `x-device-*` + `x-hwid`); response-header parsing (`subscription-userinfo`, `profile-title`, `profile-update-interval`); **ParserDefaults audit trail** so every protocol-required default is visible.
- Xray JSON renderer (`internal/coreengine/xray`) — inbounds (tproxy / socks / http / dns / dokodemo / shadowsocks), outbounds (vless / vmess / trojan / shadowsocks / socks / http / hysteria2 / wireguard / freedom / blackhole / dns / loopback), transports (tcp / ws / grpc / kcp / quic / xhttp / httpupgrade / domainsocket), security (TLS / REALITY / none), mux/xudp, sniffing, routing (rules + balancers), DNS (with FakeDNS), policy, stats, API, reverse, observatory.
- Process supervisor — start/stop/reload, exponential backoff with stable-uptime reset, intentional-restart flag (Reload/soft-cap reload not counted as crashes), per-process resource monitor (RSS/FD/CPU from `/proc`), soft memory cap → reload (NOT kill), `oom_score_adj` + nice + rlimit. `Setpgid` + group-kill on grace expiry. Crash-safe state writes (atomic + fsync + dir-fsync).
- Xray gRPC API client (`internal/api`) — `Ping`, `Stats`, `StatQuery`, `SystemStats`, `AddOutbound`, `RemoveOutbound`, `AddInbound`, `RemoveInbound`, `RestartLogger`. Shell-out to `xray api ...` for v0.1; native gRPC in v0.2.
- nftables emitter — block-form (atomic `nft -f -`), dedicated `inet vctl` table, dnsmasq-friendly nftsets, IPv4+IPv6, bypass nets, sniffing-aware. Routing companion commands.
- dnsmasq fragment generator — `server=` upstreams + `nftset=` hooks per domain group.
- Geo updater — `geoip.dat` / `geosite.dat` download, SHA256 verify, atomic swap with fsync, hash-match short-circuit.
- CLI `vctl` — `version | validate | render | subscribe {fetch|parse|hwid} | supervise | firewall {render|apply|revert|routing} | geo {update|verify} | api {ping|stats|statquery|sys|add-outbound|rm-outbound|observatory|logger-restart} | doctor`.

### Design guarantees
- **No silent normalization.** PassWall2's `fp=firefox→chrome` is explicitly forbidden. Every parser-supplied default is recorded in `Node.Origin.ParserDefaults`. Tests assert end-to-end: `parser_test.go::TestParseVLESS_RealityTCPVision`, `engine_test.go::TestRender_RealityFingerprintPreserved`, `audit_test.go::TestNoSilentNormalization_*`.
- **Single binary, no forks** on the hot path. No `lua` interpreter, no shell pipelines on every check-in.
- **Crash-safe writes**: every state file uses atomic `.tmp + fsync + rename + dir-fsync`.
- **No external dependencies** — stdlib only.

### Tests
- 37 unit + integration tests across 8 packages.
- Full `go test -race ./...` passes (~12s).
- `go vet ./...` clean.
- 7200 LOC.

### Known deferrals (v0.2)
- Native gRPC client (Observatory + faster Handler hot-add).
- nftables commit-confirm auto-revert.
- Scheduled subscription / geo refresh.
- OpenWrt `.ipk` packaging + canary deploy.
- HTTP API server for panel integration.
- Broad-parity edge cases (kcp/quic complex headers, balancer/observatory routing strategies, wireguard kernel-mode integration).

### Code-review verdict
Internal code-review pass (`code-reviewer` subagent) found 3 critical + 8 high + 10 medium + 7 low findings. All critical and high findings fixed before this release; medium/low are tracked in the issue list.
