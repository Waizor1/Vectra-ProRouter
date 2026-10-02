package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/sites"
	"vectra-controller-pro/internal/uiapi"
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
		c.SetService = &localctl.ServiceChoice{ID: p.Service, MainPath: p.EntryID == nil}
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
	return d.connectServiceOptionsFor(ov, nil)
}

// connectServiceOptionsFor is the services' locations for rendering running:
// the owner's choices, and «Нейросети» through Kazakhstan unless they chose
// otherwise (a location, «as the main VPN», a country).
func (d *daemon) connectServiceOptionsFor(ov localctl.Overrides, running []byte) (map[string]json.RawMessage, error) {
	ids := map[string]string{}
	for svc, id := range ov.ServiceEntries {
		ids[svc] = id
	}
	cache, err := localctl.LoadEntries(d.cfg.EntriesPath)
	if err != nil {
		if len(ov.ServiceEntries) == 0 {
			return nil, nil // no cache, no choice: nothing to overlay
		}
		return nil, err
	}
	if _, chosen := ids["ai"]; !chosen && ov.Services["ai"] == "" {
		if id, ok := defaultAIEntry(cache); ok {
			ids["ai"] = id
		}
	}
	out := map[string]json.RawMessage{}
	for svc, id := range ids {
		if id == localctl.ServiceMainPath {
			continue
		}
		var raw json.RawMessage
		for _, entry := range localctl.Summarize(cache) {
			if entry.Digest == id {
				raw = cache.Entries[entry.Index]
				break
			}
		}
		if raw == nil {
			if _, chosen := ov.ServiceEntries[svc]; !chosen {
				continue // a default that is not there any more is no choice to keep
			}
			return nil, errConnectStaleEntry
		}
		if running != nil && bytes.Equal(raw, running) {
			continue // the router runs that location: its own rule is the path
		}
		out[svc] = raw
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// defaultAIEntry is where «Нейросети» go by default: the provider's cascade
// through Russia to Kazakhstan (a remark with both flags), else its Kazakh
// location — a plain one before one named for a single service.
func defaultAIEntry(cache *localctl.EntriesCache) (string, bool) {
	return pickAIEntry(localctl.Summarize(cache))
}

// pickAIEntry is defaultAIEntry over the entries' summaries (the index the
// UI and the Connect telemetry read).
func pickAIEntry(entries []localctl.EntrySummary) (string, bool) {
	const ru, kz = "\U0001F1F7\U0001F1FA", "\U0001F1F0\U0001F1FF"
	best, rank := "", 0
	for _, e := range entries {
		remark := e.Remark
		if !strings.Contains(remark, kz) {
			continue
		}
		r := 1
		switch {
		case strings.Contains(remark, ru):
			r = 3
		case !strings.Contains(remark, "·"):
			r = 2
		}
		if r > rank {
			best, rank = e.Digest, r
		}
	}
	return best, rank > 0
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

// markAIDefault tells the router UI where «Нейросети» go while the owner has
// made no choice: Kazakhstan, when the subscription has a Kazakh location.
func markAIDefault(res *uiapi.Services, ov localctl.Overrides, entries []localctl.EntrySummary) {
	if _, chosen := ov.ServiceEntries["ai"]; chosen || ov.Services["ai"] != "" {
		return
	}
	if _, ok := pickAIEntry(entries); !ok {
		return
	}
	for i := range res.Services {
		if res.Services[i].ID == "ai" {
			kz := "KZ"
			res.Services[i].DefaultCountry = &kz
		}
	}
}
