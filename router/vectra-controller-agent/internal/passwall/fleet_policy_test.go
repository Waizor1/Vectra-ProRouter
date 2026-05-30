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

// TestFindFleetRoutePolicyTargetSelectsWorkingNodePerSlot exercises the real
// selection (threshold + order) against the live fleet for every category, and
// asserts the dead UAE node is never chosen.
func TestFindFleetRoutePolicyTargetSelectsWorkingNodePerSlot(t *testing.T) {
	nodes := realFleetNodes()
	cases := []struct {
		slot      string
		wantOneOf []string
	}{
		{"WorldProxy", []string{"WM3tsJ7I"}},
		{"YouTube", []string{"WM3tsJ7I", "IoUWHdPS", "QJjZqQRF"}},
		{"Special", []string{"WuGHS4PD"}},
		{"Tiktok", []string{"8EbKwZxy"}},
		{"DiscordVoiceUdp", []string{"QJjZqQRF"}},
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
