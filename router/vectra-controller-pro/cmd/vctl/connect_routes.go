package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"sort"
	"strings"
	"sync"
	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/sites"
	"vectra-controller-pro/internal/uiapi"
	"vectra-controller-pro/internal/vault"
)

// connectServiceAuto as a set_service entryId takes the service back to its
// default (capability set_service_auto); null stays «as the main VPN».
const connectServiceAuto = ":auto"

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
		if p.EntryID != nil && *p.EntryID == connectServiceAuto {
			// Back to the default: the owner's choice is deleted.
			c.SetService = &localctl.ServiceChoice{ID: p.Service}
			break
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

// connectServiceOptionsFor is the services' locations for rendering running:
// the owner's choices, and «Нейросети» through Kazakhstan unless they chose
// otherwise (a location, «as the main VPN», a country).
//
// A chosen location that is gone from the cache, or no longer carries its
// service, is skipped — never a reason to refuse the render, which would
// freeze the router on its last one: the service takes its default path
// («Нейросети» their Kazakh default, the others the main VPN) and the choice
// stays in the overrides, to run again when the location does. effective is
// ov without the skipped choices, what the render is made under.
func (d *daemon) connectServiceOptionsFor(ov localctl.Overrides, running []byte) (entries map[string]json.RawMessage, effective localctl.Overrides) {
	chosen := map[string]string{}
	for svc, id := range ov.ServiceEntries {
		if id != localctl.ServiceMainPath { // «as the main VPN» needs no location
			chosen[svc] = id
		}
	}
	effective = ov
	cache, err := localctl.LoadEntries(d.cfg.EntriesPath)
	if err != nil && len(chosen) == 0 {
		d.noteSkippedServiceChoices(nil)
		return nil, effective // no cache, no location chosen: nothing to overlay
	}
	out := map[string]json.RawMessage{}
	skipped := map[string]string{}
	for svc, id := range chosen {
		var raw json.RawMessage
		if cache != nil {
			raw = connectEntryRaw(cache, id)
		}
		if raw == nil || connectValidateServiceEntry(raw, svc) != nil {
			skipped[svc] = id
			continue
		}
		if running != nil && bytes.Equal(raw, running) {
			continue // the router runs that location: its own rule is the path
		}
		out[svc] = raw
	}
	if len(skipped) > 0 {
		effective.ServiceEntries = maps.Clone(ov.ServiceEntries)
		for svc := range skipped {
			delete(effective.ServiceEntries, svc)
		}
	}
	d.noteSkippedServiceChoices(skipped)
	if cache != nil {
		if _, raw, ok := aiDefault(effective, d.cfg.RouteSource, cache, running, d.aiRefused); ok && !bytes.Equal(raw, running) {
			out["ai"] = raw
		}
	}
	if len(out) == 0 {
		return nil, effective
	}
	return out, effective
}

// noteSkippedServiceChoices logs a choice the render skips once, not on
// every render; one that runs again is forgotten, so a later skip is logged.
func (d *daemon) noteSkippedServiceChoices(skipped map[string]string) {
	d.svcSkipped.Lock()
	defer d.svcSkipped.Unlock()
	for svc, id := range skipped {
		if d.svcSkipped.m[svc] == id {
			continue
		}
		if d.svcSkipped.m == nil {
			d.svcSkipped.m = map[string]string{}
		}
		d.svcSkipped.m[svc] = id
		logging.L().Warn("the owner's location for a service is not in the cache or no longer carries it; the service takes its default path until it does",
			"service", svc, "entry", shortDigest(id))
	}
	for svc := range d.svcSkipped.m {
		if _, still := skipped[svc]; !still {
			delete(d.svcSkipped.m, svc)
		}
	}
}

func connectEntryRaw(cache *localctl.EntriesCache, id string) json.RawMessage {
	for _, entry := range localctl.Summarize(cache) {
		if entry.Digest == id {
			return cache.Entries[entry.Index]
		}
	}
	return nil
}

// aiDefault is where «Нейросети» go while the owner has made no choice: the
// best Kazakh location (aiCandidates) that imports into running exactly as a
// render would and never goes direct. One that does not is skipped — an
// unchosen default never refuses a render, never unproxies a service. A
// router that runs the location itself takes its own rule.
func aiDefault(ov localctl.Overrides, routeSource string, cache *localctl.EntriesCache, running []byte, refused map[string]bool) (string, json.RawMessage, bool) {
	if routeSource != "" || cache == nil || running == nil {
		return "", nil, false
	}
	if _, chosen := ov.ServiceEntries["ai"]; chosen || ov.Services["ai"] != "" {
		return "", nil, false
	}
	for _, id := range aiCandidates(localctl.Summarize(cache)) {
		raw := connectEntryRaw(cache, id)
		if raw == nil {
			continue
		}
		if bytes.Equal(raw, running) {
			return id, raw, true
		}
		if refused[aiRefusedKey(raw, running)] {
			continue // xray refused it on this document
		}
		if xray.TrialConnectService(running, raw, "ai") == nil {
			return id, raw, true
		}
	}
	return "", nil, false
}

// aiCandidates are the Kazakh locations, best first: the provider's cascade
// entering in Russia and leaving in Kazakhstan (🇷🇺🇰🇿), then a Kazakh one;
// within each, a plain one before one named for a single service
// («🇰🇿 Gemini · Google»). The flags are read in order — the last is the
// exit — so «🇰🇿🇷🇺», leaving in Russia, is none.
func aiCandidates(entries []localctl.EntrySummary) []string {
	type candidate struct {
		id    string
		score int
	}
	var cs []candidate
	for _, e := range entries {
		flags := remarkFlags(e.Remark)
		score := 0
		switch {
		case len(flags) == 2 && flags[0] == "RU" && flags[1] == "KZ":
			score = 4
		case len(flags) == 1 && flags[0] == "KZ":
			score = 2
		default:
			continue
		}
		if !strings.Contains(e.Remark, "·") {
			score++
		}
		cs = append(cs, candidate{e.Digest, score})
	}
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].score > cs[j].score })
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.id
	}
	return out
}

// remarkFlags are the country flags in a remark, in order, as ISO codes.
func remarkFlags(remark string) []string {
	const a, z = 0x1F1E6, 0x1F1FF
	rs := []rune(remark)
	var out []string
	for i := 0; i+1 < len(rs); i++ {
		if rs[i] >= a && rs[i] <= z && rs[i+1] >= a && rs[i+1] <= z {
			out = append(out, string([]rune{'A' + rs[i] - a, 'A' + rs[i+1] - a}))
			i++
		}
	}
	return out
}

// aiDefaultApplied is the location «Нейросети» run through while the owner
// has made no choice, as the router runs it: the one the installed render
// carries (spliceKey, the render's options, names it by digest), or the
// location the router runs itself when it is the default. The Connect
// inventory and the router UI report this, never a default a render refused.
func aiDefaultApplied(cfg agentcfg.Config, ov localctl.Overrides, spliceKey string) (string, bool) {
	if cfg.RouteSource != "" {
		return "", false
	}
	if _, chosen := ov.ServiceEntries["ai"]; chosen || ov.Services["ai"] != "" {
		return "", false
	}
	if id := spliceKeyEntry(spliceKey, "ai"); id != "" {
		return id, true
	}
	// No overlay: the router may run the default itself. The check-in asks
	// every minute; the answer only changes with these files, so it is kept
	// until one of them does.
	key := aiAppliedKey(cfg)
	aiApplied.Lock()
	defer aiApplied.Unlock()
	if key != "" && key == aiApplied.key {
		return aiApplied.id, aiApplied.ok
	}
	id, ok := aiRunsDefault(cfg, ov)
	aiApplied.key, aiApplied.id, aiApplied.ok = key, id, ok
	return id, ok
}

// spliceKeyEntry is the digest of the location the render took for service.
func spliceKeyEntry(spliceKey, service string) string {
	_, rest, ok := strings.Cut(spliceKey, ";svcEntries=")
	if !ok {
		return ""
	}
	rest, _, _ = strings.Cut(rest, ";")
	for _, kv := range strings.Split(rest, ",") {
		if id, found := strings.CutPrefix(kv, service+"="); found && validConnectEntryID(id) {
			return id
		}
	}
	return ""
}

var aiApplied struct {
	sync.Mutex
	key, id string
	ok      bool
}

func aiAppliedKey(cfg agentcfg.Config) string {
	k := ""
	for _, p := range []string{cfg.EntriesPath, cfg.ProviderConfigPath} {
		st, err := os.Stat(p)
		if err != nil {
			return ""
		}
		k += fmt.Sprintf("%s:%d:%d|", p, st.Size(), st.ModTime().UnixNano())
	}
	return k
}

func aiRunsDefault(cfg agentcfg.Config, ov localctl.Overrides) (string, bool) {
	cache, err := localctl.LoadEntries(cfg.EntriesPath)
	if err != nil {
		return "", false
	}
	running, err := vault.ReadFile(cfg.ProviderConfigPath)
	if err != nil {
		return "", false
	}
	id, raw, ok := aiDefault(ov, cfg.RouteSource, cache, running, nil)
	return id, ok && bytes.Equal(raw, running)
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
// made no choice: Kazakhstan, when the router runs them there (aiDefaultApplied).
func markAIDefault(res *uiapi.Services, applied bool) {
	if !applied {
		return
	}
	for i := range res.Services {
		if res.Services[i].ID == "ai" {
			kz := "KZ"
			res.Services[i].DefaultCountry = &kz
		}
	}
}

// aiRefusedKey names a default location on the document it joined.
func aiRefusedKey(entry, document []byte) string {
	a, b := sha256.Sum256(entry), sha256.Sum256(document)
	return hex.EncodeToString(a[:]) + ":" + hex.EncodeToString(b[:])
}
