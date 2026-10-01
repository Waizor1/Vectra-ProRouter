package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/sites"
)

func connectRouteChange(action string, params json.RawMessage, cache *localctl.EntriesCache) (*localctl.Change, string) {
	var p struct {
		EntryID *string  `json:"entryId"`
		Service string   `json:"service"`
		Direct  []string `json:"direct"`
		VPN     []string `json:"vpn"`
	}
	if json.Unmarshal(params, &p) != nil {
		return nil, "invalid_params"
	}
	c := &localctl.Change{}
	resolve := func(id string) (*localctl.EntryChoice, json.RawMessage, string) {
		if cache == nil {
			return nil, nil, "no_entries_cache"
		}
		for _, e := range localctl.Summarize(cache) {
			if e.Digest == id && e.Remark != "" {
				return &localctl.EntryChoice{Index: e.Index, Remark: e.Remark, Digest: e.Digest}, cache.Entries[e.Index], ""
			}
		}
		return nil, nil, "unknown_entry"
	}
	switch action {
	case "select_entry":
		if p.EntryID == nil {
			c.ResetEntry = true
		} else {
			entry, _, code := resolve(*p.EntryID)
			if code != "" {
				return nil, code
			}
			c.SetEntry = entry
		}
	case "set_rules":
		if len(p.Direct)+len(p.VPN) > 300 {
			return nil, "invalid_params"
		}
		normalize := func(in []string) ([]string, bool) {
			out := []string{}
			seen := map[string]bool{}
			for _, s := range in {
				parsed, err := sites.ParseConnectDomain(s)
				if err != nil || !parsed.Domain || parsed.Service != "" || len(s) > 253 {
					return nil, false
				}
				if !seen[parsed.Display] {
					seen[parsed.Display] = true
					out = append(out, parsed.Display)
				}
			}
			return out, true
		}
		direct, ok := normalize(p.Direct)
		if !ok {
			return nil, "invalid_params"
		}
		vpn, ok := normalize(p.VPN)
		if !ok {
			return nil, "invalid_params"
		}
		seen := map[string]bool{}
		for _, s := range direct {
			seen[s] = true
		}
		for _, s := range vpn {
			if seen[s] {
				return nil, "invalid_params"
			}
		}
		c.SetRules = &localctl.Rules{Direct: direct, Proxy: vpn, Connect: true}
	case "set_service":
		if _, ok := xray.ServiceByID(p.Service); !ok {
			return nil, "unknown_service"
		}
		c.SetService = &localctl.ServiceChoice{ID: p.Service}
		if p.EntryID != nil {
			_, raw, code := resolve(*p.EntryID)
			if code != "" {
				return nil, code
			}
			if err := xray.ValidateConnectServiceEntry(raw, p.Service); err != nil {
				return nil, "service_path_unavailable"
			}
			c.SetService.EntryID = *p.EntryID
		}
	default:
		return nil, "unsupported_action"
	}
	return c, ""
}

func (d *daemon) connectRoutingAction(ctx context.Context, action string, params json.RawMessage) localctl.SocketResponse {
	if d.cfg.RouteSource != "" {
		return localctl.SocketResponse{Code: "unavailable"}
	}
	cache, _ := localctl.LoadEntries(d.cfg.EntriesPath)
	change, code := connectRouteChange(action, params, cache)
	if code != "" {
		return localctl.SocketResponse{Code: code}
	}
	resp := d.localReapply(ctx, change)
	resp.Detail = ""
	return resp
}

var errConnectStaleEntry = errors.New("unknown_service_entry")

func (d *daemon) connectServiceOptions(ov localctl.Overrides) (map[string]json.RawMessage, error) {
	if len(ov.ServiceEntries) == 0 {
		return nil, nil
	}
	cache, err := localctl.LoadEntries(d.cfg.EntriesPath)
	if err != nil {
		return nil, err
	}
	out := map[string]json.RawMessage{}
	for svc, id := range ov.ServiceEntries {
		for _, entry := range localctl.Summarize(cache) {
			if entry.Digest == id {
				out[svc] = cache.Entries[entry.Index]
				break
			}
		}
		if out[svc] == nil {
			return nil, errConnectStaleEntry
		}
	}
	return out, nil
}

func connectDigestIndex(entries []json.RawMessage, id string) (int, error) {
	for i, raw := range entries {
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) == id {
			return i, nil
		}
	}
	return 0, errConnectStaleEntry
}
