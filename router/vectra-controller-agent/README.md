# Vectra Controller Agent

Go-based outbound control-plane agent for OpenWrt routers.

Current scope in this repository:

- `cmd/vectra-controller-agent`: polling loop and control-plane client
- `internal/controlplane`: versioned router-facing REST client
- `internal/rescue`: local direct-mode rescue state machine
- `internal/passwall`: dry-run apply-plan scaffolding for typed desired config
- `openwrt/`: package skeleton for OpenWrt 24.x buildroot integration

The OpenWrt package path is intentionally split from the pure Go source so the
binary can be built through the matching OpenWrt SDK and started via `procd`.

Pilot bootstrap defaults currently assume a split deployment:

- `control_url=https://api.vectra-pro.net` for router-facing REST
- `panel_url=https://router.vectra-pro.net` for operator/UI visibility

Legacy configs that only provide `panel_url` remain supported by the agent
config loader.
