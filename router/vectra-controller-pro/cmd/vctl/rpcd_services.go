package main

import (
	"context"
	"encoding/json"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/catalog"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/uiapi"
)

// set_service_mode and set_stable_main (r26, ui/contract/README.md): a
// service's mode — «Авто» (""), «Как основной VPN» (main), «VPN · постоянный
// IP» (stable), «Напрямую» (direct), as the catalog offers it — and the whole
// main path through the stable exit.
func init() {
	rpcdSignatures["set_service_mode"] = map[string]interface{}{"id": "str", "mode": "str"}
	rpcdSignatures["set_stable_main"] = map[string]interface{}{"on": true}
}

func rpcdSetServiceMode(ctx context.Context, cfg agentcfg.Config, params []byte) uiapi.Action {
	var p struct {
		ID   *string `json:"id"`
		Mode *string `json:"mode"`
	}
	if json.Unmarshal(params, &p) != nil || p.ID == nil || p.Mode == nil {
		return action(false, "invalid_params", `params must be {"id": "<service>", "mode": ""|"main"|"stable"|"direct"}`)
	}
	if _, ok := xray.ServiceByID(*p.ID); !ok {
		return action(false, "unknown_service", *p.ID)
	}
	if cfg.RouteSource != "" {
		return action(false, "unavailable", "this router routes by the operator's policy: no subscription entry to choose from")
	}
	switch m := *p.Mode; {
	case m == "", m == catalog.ModeMain:
	case m == catalog.ModeStable, m == catalog.ModeDirect:
		if !xray.ActiveCatalog().Service(*p.ID).Offers(*p.ID, m) {
			return action(false, "invalid_params", "the service does not offer "+m)
		}
	default:
		return action(false, "invalid_params", `mode is "", "main", "stable" or "direct"`)
	}
	choice := &localctl.ServiceModeChoice{ID: *p.ID, Mode: *p.Mode}
	if choice.Mode == catalog.ModeDirect {
		choice.Consent = xray.ActiveCatalog().Service(*p.ID).MatchersHash()
	}
	change := localctl.Change{SetServiceMode: choice}
	return askDaemon(ctx, cfg, localctl.SocketRequest{Op: localctl.OpReapply, Change: &change}, "service_set")
}

func rpcdSetStableMain(ctx context.Context, cfg agentcfg.Config, params []byte) uiapi.Action {
	var p struct {
		On *bool `json:"on"`
	}
	if json.Unmarshal(params, &p) != nil || p.On == nil {
		return action(false, "invalid_params", `params must be {"on": true} or {"on": false}`)
	}
	if cfg.RouteSource != "" {
		return action(false, "unavailable", "this router routes by the operator's policy")
	}
	change := localctl.Change{SetStableMain: p.On}
	return askDaemon(ctx, cfg, localctl.SocketRequest{Op: localctl.OpReapply, Change: &change}, "stable_main_set")
}
