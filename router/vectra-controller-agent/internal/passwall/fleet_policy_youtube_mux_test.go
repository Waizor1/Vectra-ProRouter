package passwall

import "testing"

// YouTube stays off the WorldProxy/DiscordVoiceUdp mux node (2026-10-26).
//
// PassWall2 renders mux=1 as an Xray mux object without xudpProxyUDP443, whose
// default is "reject": QUIC sent to that outbound gets no answer. realFleetNodes
// carries no "Россия YouTube" :50051 node, so three YouTube-purposed RU-entry
// nodes tie at the top and the scorer used to take the first one — WM3tsJ7I,
// the very node WorldProxy and DiscordVoiceUdp sit on.

func youTubeMuxTestConfig(worldNodeHasMux bool) DesiredConfig {
	nodes := realFleetNodes()
	for i := range nodes {
		if nodes[i].ID == "WM3tsJ7I" && worldNodeHasMux {
			nodes[i].Extras = map[string]any{"mux": "1", "mux_concurrency": "-1", "xudp_concurrency": "16"}
		}
	}
	nodes = append(nodes, NodeConfig{
		ID:       "myshunt",
		Protocol: "shunt",
		Enabled:  true,
		Extras: map[string]any{
			"WorldProxy":      "WM3tsJ7I",
			"YouTube":         "WM3tsJ7I",
			"DiscordVoiceUdp": "WM3tsJ7I",
		},
	})
	config := DesiredConfig{Nodes: nodes}
	config.BasicSettings.ShuntRules = []ShuntRule{
		{ID: "WorldProxy", Label: "WorldProxy", OutboundNodeID: "WM3tsJ7I"},
		{ID: "YouTube", Label: "YouTube", OutboundNodeID: "WM3tsJ7I"},
		{ID: "DiscordVoiceUdp", Label: "DiscordVoiceUdp", OutboundNodeID: "WM3tsJ7I",
			Extras: map[string]any{"network": "udp", "port": "19294-19344,50000-50100"}},
	}
	return config
}

func ruleBindings(config DesiredConfig) map[string]string {
	bound := map[string]string{}
	for _, rule := range config.BasicSettings.ShuntRules {
		bound[rule.ID] = rule.OutboundNodeID
	}
	return bound
}

func nodeExtras(config DesiredConfig, id string) map[string]any {
	for _, node := range config.Nodes {
		if node.ID == id {
			return node.Extras
		}
	}
	return nil
}

func TestBuiltinScorerMovesYouTubeOffTheMuxNode(t *testing.T) {
	normalized, changed := NormalizeFleetRoutePolicyConfig(
		youTubeMuxTestConfig(true), FleetRoutePolicyIdentity{Hostname: "yt-mux"})
	if !changed {
		t.Fatal("expected YouTube to move off the mux node")
	}

	bound := ruleBindings(normalized)
	if got := bound["YouTube"]; got != "IoUWHdPS" && got != "QJjZqQRF" {
		t.Fatalf("YouTube bound to %q, want a YouTube node without mux (IoUWHdPS or QJjZqQRF)", got)
	}
	if got := bound["WorldProxy"]; got != "WM3tsJ7I" {
		t.Fatalf("WorldProxy bound to %q, want WM3tsJ7I", got)
	}
	if got := bound["DiscordVoiceUdp"]; got != "WM3tsJ7I" {
		t.Fatalf("DiscordVoiceUdp bound to %q, want WM3tsJ7I (voice keeps XUDP)", got)
	}
	extras := nodeExtras(normalized, "WM3tsJ7I")
	for key, want := range map[string]string{"mux": "1", "mux_concurrency": "-1", "xudp_concurrency": "16"} {
		if got := stringify(extras[key]); got != want {
			t.Fatalf("WM3tsJ7I %s=%q, want %q", key, got, want)
		}
	}
	if got := stringify(nodeExtras(normalized, bound["YouTube"])["mux"]); got == "1" {
		t.Fatalf("YouTube's new node %s carries mux", bound["YouTube"])
	}
}

func TestBuiltinScorerAvoidsWorldProxyNodeBeforeMuxIsStamped(t *testing.T) {
	// A subscription refresh re-mints node ids, and the fresh WorldProxy node
	// gets its mux in the same pass, from the DiscordVoiceUdp slot.
	normalized, _ := NormalizeFleetRoutePolicyConfig(
		youTubeMuxTestConfig(false), FleetRoutePolicyIdentity{Hostname: "yt-fresh"})

	bound := ruleBindings(normalized)
	if bound["YouTube"] == bound["WorldProxy"] {
		t.Fatalf("YouTube shares WorldProxy's node %q", bound["YouTube"])
	}
	if got := stringify(nodeExtras(normalized, "WM3tsJ7I")["mux"]); got != "1" {
		t.Fatalf("WM3tsJ7I mux=%q after normalization, want 1", got)
	}
}

func TestBuiltinScorerKeepsYouTubeOnMuxNodeWithoutAnAlternative(t *testing.T) {
	// QUIC-less YouTube still plays over TCP; an unbound slot does not play.
	config := youTubeMuxTestConfig(true)
	kept := config.Nodes[:0]
	for _, node := range config.Nodes {
		if node.ID == "IoUWHdPS" || node.ID == "QJjZqQRF" {
			continue
		}
		kept = append(kept, node)
	}
	config.Nodes = kept

	normalized, _ := NormalizeFleetRoutePolicyConfig(config, FleetRoutePolicyIdentity{Hostname: "yt-only-mux"})
	if got := ruleBindings(normalized)["YouTube"]; got != "WM3tsJ7I" {
		t.Fatalf("YouTube bound to %q, want WM3tsJ7I (the only YouTube node)", got)
	}
}

func TestDirectivePinOfYouTubeStillWins(t *testing.T) {
	// The panel is authoritative; the mux rule lives in its scorer too.
	directive := &FleetRoutePolicyDirective{
		Slots: []FleetRoutePolicyDirectiveSlot{{ID: "YouTube", NodeID: "WM3tsJ7I"}},
	}
	normalized, _ := NormalizeFleetRoutePolicyConfigWithDirective(
		youTubeMuxTestConfig(true), FleetRoutePolicyIdentity{Hostname: "yt-pinned"}, directive)
	if got := ruleBindings(normalized)["YouTube"]; got != "WM3tsJ7I" {
		t.Fatalf("YouTube bound to %q, want the panel's pin WM3tsJ7I", got)
	}
}
