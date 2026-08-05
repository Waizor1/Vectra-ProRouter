package passwall

import "testing"

// realFleetNodes mirrors the live BloopCat subscription as enumerated on the
// fleet (sergeyavito, 2026-05). It is the exact set of labels/ports that shipped
// the YouTube mis-binding, so the scorer is exercised against ground truth rather
// than a sanitized fixture. The "🇷🇺🇦🇪 ОАЭ" :50061 node is the regression anchor:
// it passes a google-204 healthcheck but fails real youtube.com, and used to win
// the YouTube slot purely off its leading 🇷🇺 ENTRY flag.
func realFleetNodes() []NodeConfig {
	grpc := func(id, label, addr string, port int) NodeConfig {
		return NodeConfig{ID: id, Label: label, Protocol: "vless", Enabled: true, Address: addr, Port: port, Transport: "grpc"}
	}
	raw := func(id, label, addr string, port int) NodeConfig {
		return NodeConfig{ID: id, Label: label, Protocol: "vless", Enabled: true, Address: addr, Port: port, Transport: "tcp"}
	}
	return []NodeConfig{
		raw("YjMq9afO", "🇩🇪⚡Германия YouTube", "ger5.nfnpx.online", 443),
		raw("UeUVz9He", "🇵🇱 ⚡️Польша YouTube", "pl2.nfnpx.online", 443),
		raw("LXByGgx7", "🇫🇮 ⚡Финляндия YouTube", "fin1.nfnpx.online", 443),
		grpc("UCVE3oZU", "🇷🇺🇦🇪 ОАЭ", "ru4.nfnpx.online", 50061),
		raw("8EbKwZxy", "🇧🇾 Беларусь", "by2.nfnpx.online", 443),
		grpc("8cSORSea", "🇷🇺🇧🇾 Беларусь", "ru3.nfnpx.online", 50059),
		grpc("WM3tsJ7I", "🇷🇺🇩🇪⚡Германия YouTube", "ru5.nfnpx.online", 50052),
		grpc("IoUWHdPS", "🇷🇺🇫🇮 ⚡Финляндия YouTube", "ru3.nfnpx.online", 50054),
		raw("hWwQMJ0a", "🇫🇷 Франция", "fr2.nfnpx.online", 443),
		grpc("HuFshWfM", "🇷🇺🇫🇷 Франция", "ru4.nfnpx.online", 50057),
		raw("E9ZTKsJM", "🇰🇿 Казахстан", "kz1.nfnpx.online", 443),
		grpc("xRSDXh4f", "🇷🇺🇰🇿 Казахстан", "ru3.nfnpx.online", 50056),
		raw("Xh0pdJF9", "🇳🇱 Нидерланды", "nl1.nfnpx.online", 443),
		grpc("WuGHS4PD", "🇷🇺🇳🇱 Нидерланды", "ru5.nfnpx.online", 50055),
		grpc("QJjZqQRF", "🇷🇺🇵🇱 ⚡️Польша YouTube", "ru5.nfnpx.online", 50053),
		raw("aHfsFw6R", "🇹🇷 Турция", "tr1.nfnpx.online", 443),
		grpc("NaIB9epQ", "🇷🇺🇹🇷 Турция", "ru3.nfnpx.online", 50060),
		raw("v2I8WFGr", "🇺🇸 США", "usa3.nfnpx.online", 443),
		grpc("VNDjex03", "🇷🇺🇺🇸 США", "ru3.nfnpx.online", 50058),
		raw("EKloq9dE", "🇵🇱 Польша тест 1 F", "pl1.nfnpx.online", 443),
		raw("s0AqKC8u", "🇵🇱 Польша тест 2 V", "pl1.nfnpx.online", 443),
	}
}

func nodeByID(t *testing.T, id string) NodeConfig {
	t.Helper()
	for _, n := range realFleetNodes() {
		if n.ID == id {
			return n
		}
	}
	t.Fatalf("fixture node %s not found", id)
	return NodeConfig{}
}

// TestFleetRoutePolicyScoreYouTubeRejectsEntryFlagOnlyNode is the core regression
// guard. The dead UAE node carries a leading 🇷🇺 entry flag and lands on a real
// RU-entry host, but it is neither youtube-purposed nor a Russia exit, so it must
// score 0 and never be eligible for the YouTube slot.
func TestFleetRoutePolicyScoreYouTubeRejectsEntryFlagOnlyNode(t *testing.T) {
	uae := nodeByID(t, "UCVE3oZU")
	if got := fleetRoutePolicyScore("YouTube", uae); got != 0 {
		t.Fatalf("UAE entry-flag-only node scored %d for YouTube, want 0 (must not qualify off the leading 🇷🇺 entry marker)", got)
	}
}

// TestFleetRoutePolicyScoreUAENeverQualifiesAnySlot proves the dead node is inert
// across the whole policy, not just YouTube — it matches no category label.
func TestFleetRoutePolicyScoreUAENeverQualifiesAnySlot(t *testing.T) {
	uae := nodeByID(t, "UCVE3oZU")
	for _, slot := range []string{"WorldProxy", "YouTube", "Special", "Tiktok", "DiscordVoiceUdp"} {
		if got := fleetRoutePolicyScore(slot, uae); got != 0 {
			t.Fatalf("UAE node scored %d for slot %s, want 0", got, slot)
		}
	}
}

func TestFleetRoutePolicyScoreYouTubeTiers(t *testing.T) {
	scores := map[string]int{}
	for _, n := range realFleetNodes() {
		scores[n.ID] = fleetRoutePolicyScore("YouTube", n)
	}
	// youtube-purposed RU-entry grpc nodes are the real working targets.
	for _, id := range []string{"WM3tsJ7I", "IoUWHdPS", "QJjZqQRF"} {
		if scores[id] < 100 {
			t.Fatalf("youtube-purposed RU-entry node %s scored %d, want >= 100", id, scores[id])
		}
	}
	// direct :443 youtube exits are a non-RU-entry fallback shape and must stay
	// below the selection threshold so the grpc RU-entry nodes win.
	for _, id := range []string{"YjMq9afO", "UeUVz9He", "LXByGgx7"} {
		if scores[id] >= 100 {
			t.Fatalf("direct :443 youtube node %s scored %d, want < 100 (fallback only)", id, scores[id])
		}
	}
}

// TestFleetRoutePolicyScoreWorldProxyPrefersDirectPoland pins the 2026-08-02
// canon: the direct Poland :443 exit outranks RU-entry Poland, RU-entry stays
// above the selection threshold as a fallback for subscriptions with no direct
// node, and the "Extreme" twin loses a deterministic tie-break so the winner
// does not depend on node order (the subscription re-mints it on refresh).
func TestFleetRoutePolicyScoreWorldProxyPrefersDirectPoland(t *testing.T) {
	direct := NodeConfig{ID: "d", Label: "🇵🇱 ⚡️Польша YouTube 🚫Ad🚫", Protocol: "vless", Enabled: true, Address: "pl2.nfnpx.online", Port: 443, Transport: "tcp"}
	extreme := NodeConfig{ID: "x", Label: "⚡Extreme Польша 🇵🇱", Protocol: "vless", Enabled: true, Address: "pl1.nfnpx.online", Port: 443, Transport: "tcp"}
	ruEntry := NodeConfig{ID: "r", Label: "🇷🇺🇵🇱 ⚡️Польша YouTube 🚫Ad🚫", Protocol: "vless", Enabled: true, Address: "ru3.nfnpx.online", Port: 50053, Transport: "grpc"}

	directScore := fleetRoutePolicyScore("WorldProxy", direct)
	extremeScore := fleetRoutePolicyScore("WorldProxy", extreme)
	ruScore := fleetRoutePolicyScore("WorldProxy", ruEntry)

	if directScore <= ruScore {
		t.Fatalf("direct Poland scored %d, RU-entry %d; direct must win", directScore, ruScore)
	}
	if extremeScore >= directScore {
		t.Fatalf("Extreme twin scored %d, plain direct %d; plain must win the tie-break", extremeScore, directScore)
	}
	if ruScore < 100 {
		t.Fatalf("RU-entry Poland scored %d, want >= 100 so it stays a usable fallback", ruScore)
	}

	// With both shapes present the direct exit is what actually gets bound.
	target := findFleetRoutePolicyTarget([]NodeConfig{ruEntry, extreme, direct}, "WorldProxy")
	if target == nil || target.ID != "d" {
		t.Fatalf("selected %v, want the direct Poland :443 node", target)
	}
	// With no direct node at all the RU-entry fallback still binds.
	fallback := findFleetRoutePolicyTarget([]NodeConfig{ruEntry}, "WorldProxy")
	if fallback == nil || fallback.ID != "r" {
		t.Fatalf("selected %v, want the RU-entry fallback when no direct node exists", fallback)
	}
	// DiscordVoiceUdp must resolve to the SAME node as WorldProxy (2026-08-03).
	// The WorldProxy rule sits above the Discord rule in the generated Xray
	// chain and already carries the Discord prefixes with network=tcp,udp, so
	// voice packets leave through the WorldProxy node regardless. Splitting the
	// two stranded the slot's mux/xudp tuning on a node no Discord packet
	// reached and killed voice fleet-wide.
	discord := findFleetRoutePolicyTarget([]NodeConfig{ruEntry, extreme, direct}, "DiscordVoiceUdp")
	if discord == nil || discord.ID != target.ID {
		t.Fatalf("DiscordVoiceUdp selected %v, want the same node as WorldProxy (%s)", discord, target.ID)
	}
	// The lockstep must survive a fall back onto RU-entry too.
	discordFallback := findFleetRoutePolicyTarget([]NodeConfig{ruEntry}, "DiscordVoiceUdp")
	if discordFallback == nil || discordFallback.ID != fallback.ID {
		t.Fatalf("DiscordVoiceUdp fallback selected %v, want the same node as WorldProxy (%s)", discordFallback, fallback.ID)
	}
}

// TestFindFleetRoutePolicyTargetSelectsWorkingNodePerSlot exercises the real
// selection (threshold + order) against the live fleet for every category, and
// asserts the dead UAE node is never chosen.
func TestFindFleetRoutePolicyTargetSelectsWorkingNodePerSlot(t *testing.T) {
	nodes := realFleetNodes()
	cases := []struct {
		slot      string
		wantOneOf []string
	}{
		// WorldProxy moved to the direct Poland :443 exit on 2026-08-02 after
		// the provider blackholed Telegram and the Netflix OCA CDN on part of
		// its RU-entry fleet. DiscordVoiceUdp followed it onto the same node on
		// 2026-08-03 — the WorldProxy rule outranks the Discord rule in the
		// generated chain, so the slot's mux/xudp tuning has to land there.
		{"WorldProxy", []string{"UeUVz9He"}},
		{"YouTube", []string{"WM3tsJ7I", "IoUWHdPS", "QJjZqQRF"}},
		{"Special", []string{"WuGHS4PD"}},
		{"Tiktok", []string{"8EbKwZxy"}},
		{"DiscordVoiceUdp", []string{"UeUVz9He"}},
	}
	for _, tc := range cases {
		target := findFleetRoutePolicyTarget(nodes, tc.slot)
		if target == nil {
			t.Fatalf("slot %s: no target selected", tc.slot)
		}
		if target.ID == "UCVE3oZU" {
			t.Fatalf("slot %s selected the dead UAE node", tc.slot)
		}
		found := false
		for _, want := range tc.wantOneOf {
			if target.ID == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("slot %s selected %s (%s), want one of %v", tc.slot, target.ID, target.Label, tc.wantOneOf)
		}
	}
}

// TestFindFleetRoutePolicyTargetSkipsSubThresholdYouTube confirms the gate refuses
// to bind a YouTube slot when only the direct :443 fallback shape is available
// (score 90 < 100), rather than silently locking onto a sub-threshold node.
func TestFindFleetRoutePolicyTargetSkipsSubThresholdYouTube(t *testing.T) {
	nodes := []NodeConfig{
		{ID: "YjMq9afO", Label: "🇩🇪⚡Германия YouTube", Protocol: "vless", Enabled: true, Address: "ger5.nfnpx.online", Port: 443, Transport: "tcp"},
		nodeByID(t, "UCVE3oZU"),
	}
	if target := findFleetRoutePolicyTarget(nodes, "YouTube"); target != nil {
		t.Fatalf("YouTube target = %s (%s), want nil (no node >= 100)", target.ID, target.Label)
	}
}

func TestFleetRoutePolicyScoreIgnoresDisabledAndShuntNodes(t *testing.T) {
	working := nodeByID(t, "WM3tsJ7I")

	disabled := working
	disabled.Enabled = false
	if got := fleetRoutePolicyScore("YouTube", disabled); got != 0 {
		t.Fatalf("disabled node scored %d for YouTube, want 0", got)
	}

	shunt := working
	shunt.Protocol = "shunt"
	if got := fleetRoutePolicyScore("YouTube", shunt); got != 0 {
		t.Fatalf("shunt node scored %d for YouTube, want 0", got)
	}
}

func TestFleetRoutePolicyExceptionsMatchPanelList(t *testing.T) {
	// The panel holds the same list in
	// apps/web/src/server/vectra/fleet-route-policy.ts. If the two drift, one
	// silently undoes the other on every 60s check-in, so pin the contents here
	// and update both sides together.
	want := map[string]struct{}{
		"hh":            {},
		"vagrandrouter": {},
	}

	if len(fleetRoutePolicyExceptionValues) != len(want) {
		t.Fatalf(
			"exception list has %d entries, want %d",
			len(fleetRoutePolicyExceptionValues),
			len(want),
		)
	}
	for value := range want {
		if _, ok := fleetRoutePolicyExceptionValues[value]; !ok {
			t.Fatalf("missing exception %q", value)
		}
	}
}

func TestExemptRouterKeepsUnreachableCanonicalBindingUntouched(t *testing.T) {
	// VagrandRouter's ISP filters the RU-entry port range, so the canonical
	// RU-entry Poland node still outscores everything (reachability is not part
	// of the scorer) and normalization would rebind the slot to a dead node on
	// every check-in. The exception must short-circuit before that happens.
	identity := FleetRoutePolicyIdentity{Hostname: "VagrandRouter"}

	if !IsFleetRoutePolicyExempt(identity) {
		t.Fatalf("expected VagrandRouter to be exempt")
	}

	config := DesiredConfig{
		Nodes: []NodeConfig{
			{
				ID:        "ru-entry-poland",
				Label:     "🇷🇺🇵🇱 ⚡️Польша YouTube 🚫Ad🚫",
				Address:   "ru12.nfnpx.online",
				Port:      50053,
				Transport: "grpc",
				Enabled:   true,
			},
			{
				ID:        "direct-poland-443",
				Label:     "🇵🇱 ⚡️Польша YouTube 🚫Ad🚫",
				Address:   "pl1.nfnpx.online",
				Port:      443,
				Transport: "raw",
				Enabled:   true,
			},
		},
	}
	config.BasicSettings.ShuntRules = []ShuntRule{
		{ID: "WorldProxy", Label: "WorldProxy", OutboundNodeID: "direct-poland-443"},
	}

	normalized, changed := NormalizeFleetRoutePolicyConfig(config, identity)
	if changed {
		t.Fatalf("normalization must not touch an exempt router")
	}
	if got := normalized.BasicSettings.ShuntRules[0].OutboundNodeID; got != "direct-poland-443" {
		t.Fatalf("WorldProxy rebound to %q, want direct-poland-443", got)
	}

	// Sanity: without the exception the dead RU-entry node would win.
	_, changedUnexempt := NormalizeFleetRoutePolicyConfig(
		config,
		FleetRoutePolicyIdentity{Hostname: "some-other-router"},
	)
	if !changedUnexempt {
		t.Fatalf("expected a non-exempt router to be normalized onto the RU-entry node")
	}
}

// --- Panel-authored directive ---------------------------------------------
//
// These cover the mechanism that removes the "ship a new controller for every
// policy tweak" requirement: the panel names the node, the controller obeys.

func directiveTestConfig() DesiredConfig {
	config := DesiredConfig{
		Nodes: []NodeConfig{
			{ID: "ru-entry-poland", Label: "🇷🇺🇵🇱 ⚡️Польша", Address: "ru12.nfnpx.online", Port: 50053, Transport: "grpc", Enabled: true},
			{ID: "direct-france-443", Label: "🇫🇷 Франция", Address: "fr2.nfnpx.online", Port: 443, Transport: "tcp", Enabled: true},
			{ID: "myshunt", Protocol: "shunt", Enabled: true, Extras: map[string]any{"WorldProxy": "ru-entry-poland"}},
		},
	}
	config.BasicSettings.ShuntRules = []ShuntRule{
		{ID: "WorldProxy", Label: "WorldProxy", OutboundNodeID: "ru-entry-poland"},
	}
	return config
}

func TestDirectiveOverridesBuiltinScorer(t *testing.T) {
	// The scorer would pick ru-entry-poland. The panel says France. The panel
	// wins — that is the whole point of the directive.
	directive := &FleetRoutePolicyDirective{
		Version: "test-v1",
		Slots: []FleetRoutePolicyDirectiveSlot{
			{ID: "WorldProxy", NodeID: "direct-france-443"},
		},
	}

	normalized, changed := NormalizeFleetRoutePolicyConfigWithDirective(
		directiveTestConfig(), FleetRoutePolicyIdentity{Hostname: "kirill-msk"}, directive)

	if !changed {
		t.Fatalf("expected directive to rebind the slot")
	}
	if got := normalized.BasicSettings.ShuntRules[0].OutboundNodeID; got != "direct-france-443" {
		t.Fatalf("WorldProxy bound to %q, want direct-france-443", got)
	}
	for _, node := range normalized.Nodes {
		if node.Protocol != "shunt" {
			continue
		}
		if got := stringify(node.Extras["WorldProxy"]); got != "direct-france-443" {
			t.Fatalf("shunt extras WorldProxy=%q, want direct-france-443", got)
		}
	}
}

func TestDirectiveCanExemptRouterUnknownToBuiltinList(t *testing.T) {
	// Adding an exemption must not require a controller rebuild.
	identity := FleetRoutePolicyIdentity{Hostname: "kirill-msk"}
	if IsFleetRoutePolicyExempt(identity) {
		t.Fatalf("precondition: kirill-msk must not be in the built-in list")
	}

	directive := &FleetRoutePolicyDirective{Exempt: true, Reason: "operator hold"}
	_, changed := NormalizeFleetRoutePolicyConfigWithDirective(
		directiveTestConfig(), identity, directive)

	if changed {
		t.Fatalf("panel-declared exemption must short-circuit normalization")
	}
}

func TestDirectiveCanUnExemptRouterInBuiltinList(t *testing.T) {
	// The reverse direction: the panel must be able to retire a hardcoded
	// exemption without a rebuild.
	identity := FleetRoutePolicyIdentity{Hostname: "VagrandRouter"}
	if !IsFleetRoutePolicyExempt(identity) {
		t.Fatalf("precondition: VagrandRouter must be in the built-in list")
	}

	directive := &FleetRoutePolicyDirective{
		Exempt: false,
		Slots:  []FleetRoutePolicyDirectiveSlot{{ID: "WorldProxy", NodeID: "direct-france-443"}},
	}
	normalized, changed := NormalizeFleetRoutePolicyConfigWithDirective(
		directiveTestConfig(), identity, directive)

	if !changed {
		t.Fatalf("panel must be able to un-exempt a locally-listed router")
	}
	if got := normalized.BasicSettings.ShuntRules[0].OutboundNodeID; got != "direct-france-443" {
		t.Fatalf("WorldProxy bound to %q, want direct-france-443", got)
	}
}

func TestDirectiveWithUnknownNodeLeavesBindingIntact(t *testing.T) {
	// The subscription re-mints node IDs on refresh, so a directive computed one
	// check-in ago can name an ID that no longer exists. Skip, never blank.
	directive := &FleetRoutePolicyDirective{
		Slots: []FleetRoutePolicyDirectiveSlot{{ID: "WorldProxy", NodeID: "node-that-was-rotated-away"}},
	}

	normalized, changed := NormalizeFleetRoutePolicyConfigWithDirective(
		directiveTestConfig(), FleetRoutePolicyIdentity{Hostname: "kirill-msk"}, directive)

	if changed {
		t.Fatalf("unknown target node must not change anything")
	}
	if got := normalized.BasicSettings.ShuntRules[0].OutboundNodeID; got != "ru-entry-poland" {
		t.Fatalf("binding became %q, want the untouched ru-entry-poland", got)
	}
}

func TestEmptyDirectiveFallsBackToBuiltinScorer(t *testing.T) {
	// Panel reachable but with nothing to say (no resolvable targets yet): the
	// offline safety net must still run rather than leaving the slot adrift.
	config := directiveTestConfig()
	config.BasicSettings.ShuntRules[0].OutboundNodeID = "direct-france-443"

	directive := &FleetRoutePolicyDirective{Version: "test-v1", Slots: nil}
	normalized, changed := NormalizeFleetRoutePolicyConfigWithDirective(
		config, FleetRoutePolicyIdentity{Hostname: "kirill-msk"}, directive)

	if !changed {
		t.Fatalf("expected built-in scorer to run when directive carries no slots")
	}
	if got := normalized.BasicSettings.ShuntRules[0].OutboundNodeID; got != "ru-entry-poland" {
		t.Fatalf("scorer bound %q, want ru-entry-poland", got)
	}
}

func TestDirectiveSkipsDisabledTargetNode(t *testing.T) {
	config := directiveTestConfig()
	for i := range config.Nodes {
		if config.Nodes[i].ID == "direct-france-443" {
			config.Nodes[i].Enabled = false
		}
	}

	directive := &FleetRoutePolicyDirective{
		Slots: []FleetRoutePolicyDirectiveSlot{{ID: "WorldProxy", NodeID: "direct-france-443"}},
	}
	_, changed := NormalizeFleetRoutePolicyConfigWithDirective(
		config, FleetRoutePolicyIdentity{Hostname: "kirill-msk"}, directive)

	if changed {
		t.Fatalf("a disabled node must never be bound")
	}
}

func TestDirectiveAppliesRequiredExtras(t *testing.T) {
	directive := &FleetRoutePolicyDirective{
		Slots: []FleetRoutePolicyDirectiveSlot{{
			ID:         "WorldProxy",
			NodeID:     "direct-france-443",
			RuleExtras: map[string]string{"network": "udp"},
			NodeExtras: map[string]string{"mux": "1"},
		}},
	}

	normalized, changed := NormalizeFleetRoutePolicyConfigWithDirective(
		directiveTestConfig(), FleetRoutePolicyIdentity{Hostname: "kirill-msk"}, directive)

	if !changed {
		t.Fatalf("expected extras to be applied")
	}
	if got := stringify(normalized.BasicSettings.ShuntRules[0].Extras["network"]); got != "udp" {
		t.Fatalf("rule extras network=%q, want udp", got)
	}
	for _, node := range normalized.Nodes {
		if node.ID == "direct-france-443" {
			if got := stringify(node.Extras["mux"]); got != "1" {
				t.Fatalf("node extras mux=%q, want 1", got)
			}
		}
	}
}
