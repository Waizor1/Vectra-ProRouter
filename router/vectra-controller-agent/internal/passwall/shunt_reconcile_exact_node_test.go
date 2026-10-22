package passwall

import (
	"context"
	"strings"
	"testing"
)

// AndreyVK_Sochi, 2026-10-19..22: YouTube sat on ru4 :50051, which the provider
// had dropped from the subscription, while the panel's directive named ru13
// :50051 on every check-in. Both nodes are called "🇷🇺⚡Россия YouTube 🚫Ad🚫",
// so the writer took the dead one for the node it was asked to bind and did
// nothing, without a log line, for three days.
const sameNameYouTubeLabel = "🇷🇺⚡Россия YouTube 🚫Ad🚫"

func sochiSameNameLines() []string {
	return []string{
		"passwall2.myshunt=nodes",
		"passwall2.myshunt.remarks='Маршрутизатор BloopCat'",
		"passwall2.myshunt.type='Xray'",
		"passwall2.myshunt.protocol='_shunt'",
		"passwall2.myshunt.YouTube='t7g0ZgNU'",
		"passwall2.YouTube=shunt_rules",
		"passwall2.YouTube.remarks='YouTube'",
		"passwall2.t7g0ZgNU=nodes",
		"passwall2.t7g0ZgNU.remarks='" + sameNameYouTubeLabel + "'",
		"passwall2.t7g0ZgNU.type='Xray'",
		"passwall2.t7g0ZgNU.protocol='vless'",
		"passwall2.t7g0ZgNU.transport='grpc'",
		"passwall2.t7g0ZgNU.address='ru4.nfnpx.online'",
		"passwall2.t7g0ZgNU.port='50051'",
		"passwall2.t7g0ZgNU.enabled='1'",
		"passwall2.bg2ucPAm=nodes",
		"passwall2.bg2ucPAm.remarks='" + sameNameYouTubeLabel + "'",
		"passwall2.bg2ucPAm.type='Xray'",
		"passwall2.bg2ucPAm.protocol='vless'",
		"passwall2.bg2ucPAm.transport='grpc'",
		"passwall2.bg2ucPAm.address='ru13.nfnpx.online'",
		"passwall2.bg2ucPAm.port='50051'",
		"passwall2.bg2ucPAm.add_mode='2'",
	}
}

func sochiYouTubeDirective(nodeID string) *FleetRoutePolicyDirective {
	return &FleetRoutePolicyDirective{
		Version: "2026-08-03-v4",
		Slots:   []FleetRoutePolicyDirectiveSlot{{ID: "YouTube", NodeID: nodeID}},
	}
}

// sochiDesiredRevision is the last operator-applied revision with YouTube on
// the given node, as the desired-revision self-heal remembers it.
func sochiDesiredRevision(youTubeNodeID string) DesiredConfig {
	nodes := map[string]NodeConfig{
		"t7g0ZgNU": {ID: "t7g0ZgNU", Label: sameNameYouTubeLabel, Protocol: "vless", Enabled: true, Address: "ru4.nfnpx.online", Port: 50051, Transport: "grpc"},
		"bg2ucPAm": {ID: "bg2ucPAm", Label: sameNameYouTubeLabel, Protocol: "vless", Enabled: true, Address: "ru13.nfnpx.online", Port: 50051, Transport: "grpc"},
	}
	return DesiredConfig{
		BasicSettings: BasicSettingsConfig{
			ShuntRules: []ShuntRule{{ID: "YouTube", Label: "YouTube", OutboundNodeID: youTubeNodeID}},
		},
		Nodes: []NodeConfig{
			{ID: "myshunt", Label: "Маршрутизатор BloopCat", Protocol: "shunt", Enabled: true},
			nodes[youTubeNodeID],
		},
	}
}

func TestDirectiveMovesSlotOffANodeThatOnlySharesTheName(t *testing.T) {
	backend := &fakeBackend{lines: sochiSameNameLines()}

	result, err := ReconcileFleetRoutePolicyWithDirective(context.Background(), backend, FleetRoutePolicyIdentity{}, sochiYouTubeDirective("bg2ucPAm"))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	batch := strings.Join(backend.batchCommands, "\n")
	if !strings.Contains(batch, "set passwall2.myshunt.YouTube='bg2ucPAm'") {
		t.Fatalf("the directive names bg2ucPAm (ru13); a same-named node must not stand in for it, got result=%#v batch:\n%s", result, batch)
	}
	if !result.Changed || !result.Restarted {
		t.Fatalf("a rebind must be reported and must restart PassWall, got %#v", result)
	}
	if len(result.Changes) != 1 || result.Changes[0].PreviousNodeID != "t7g0ZgNU" || result.Changes[0].RestoredNodeID != "bg2ucPAm" {
		t.Fatalf("change must record the move t7g0ZgNU -> bg2ucPAm, got %#v", result.Changes)
	}
}

func TestDirectiveAlreadyOnTheExactNodeIsANoOp(t *testing.T) {
	backend := &fakeBackend{lines: sochiSameNameLines()}

	result, err := ReconcileFleetRoutePolicyWithDirective(context.Background(), backend, FleetRoutePolicyIdentity{}, sochiYouTubeDirective("t7g0ZgNU"))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Changed || result.Restarted || len(backend.batchCommands) != 0 || len(backend.runCommands) != 0 {
		t.Fatalf("slot already on the named node must not touch UCI or restart PassWall, got result=%#v batch=%v run=%v", result, backend.batchCommands, backend.runCommands)
	}
}

// The desired-revision self-heal keeps matching by intent first. It owns a
// slot whenever the directive does not, and so does the built-in scorer, which
// breaks ties between equally named hosts by UCI order; exact-wins on this
// side made the two trade the slot on every check-in.
func TestDesiredRevisionSelfHealStillMatchesByIntent(t *testing.T) {
	backend := &fakeBackend{lines: sochiSameNameLines()}

	result, err := ReconcileShuntBindingsYieldingTo(context.Background(), backend, sochiDesiredRevision("bg2ucPAm"), nil)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Changed || len(backend.batchCommands) != 0 || len(backend.runCommands) != 0 {
		t.Fatalf("desired-revision self-heal must not move a slot between equally named nodes, got result=%#v batch=%v", result, backend.batchCommands)
	}
}

// A disabled exact node is not usable even when it is pinned: the slot keeps
// its same-named node rather than pointing at something PassWall will not
// start. The directive normalizer already refuses a disabled pin, so this
// drives the writer directly.
func TestPinnedExactNodeThatIsDisabledDoesNotWin(t *testing.T) {
	lines := append(sochiSameNameLines(), "passwall2.bg2ucPAm.enabled='0'")
	backend := &fakeBackend{lines: lines}
	sections, err := ParseUCILines(lines)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	current := importDesiredConfig(sections)
	desired := cloneDesiredConfig(current)
	for i := range desired.BasicSettings.ShuntRules {
		if desired.BasicSettings.ShuntRules[i].ID == "YouTube" {
			desired.BasicSettings.ShuntRules[i].OutboundNodeID = "bg2ucPAm"
		}
	}

	result, err := reconcileShuntBindingsFromCurrent(context.Background(), backend, current, desired, []string{"YouTube"})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if strings.Contains(strings.Join(backend.batchCommands, "\n"), "myshunt.YouTube") || result.Restarted {
		t.Fatalf("a disabled pinned node must not take the slot, got result=%#v batch=%v", result, backend.batchCommands)
	}
}

// applyingBackend applies the UCI "set" commands it is given, so consecutive
// check-ins see what the previous one wrote.
type applyingBackend struct {
	lines    []string
	batches  int
	restarts int
}

func (b *applyingBackend) Show(_ context.Context, _ string) ([]string, error) {
	return append([]string(nil), b.lines...), nil
}

func (b *applyingBackend) Batch(_ context.Context, commands []string) error {
	b.batches++
	for _, command := range commands {
		assignment, ok := strings.CutPrefix(command, "set ")
		if !ok {
			continue
		}
		key, _, _ := strings.Cut(assignment, "=")
		replaced := false
		for i, line := range b.lines {
			if strings.HasPrefix(line, key+"=") {
				b.lines[i] = assignment
				replaced = true
			}
		}
		if !replaced {
			b.lines = append(b.lines, assignment)
		}
	}
	return nil
}

func (b *applyingBackend) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	if name == "/etc/init.d/passwall2" && len(args) > 0 && args[0] == "restart" {
		b.restarts++
	}
	return CommandResult{}, nil
}

// checkIn runs the two binding self-heals in the order main.go runs them.
func checkIn(t *testing.T, backend *applyingBackend, desired DesiredConfig, directive *FleetRoutePolicyDirective) {
	t.Helper()
	if _, err := ReconcileShuntBindingsYieldingTo(context.Background(), backend, desired, directive); err != nil {
		t.Fatalf("desired-revision self-heal: %v", err)
	}
	if _, err := ReconcileFleetRoutePolicyWithDirective(context.Background(), backend, FleetRoutePolicyIdentity{}, directive); err != nil {
		t.Fatalf("route-policy self-heal: %v", err)
	}
}

func TestCheckInSelfHealsConvergeOnEquallyNamedNodes(t *testing.T) {
	cases := []struct {
		name         string
		desired      DesiredConfig
		directive    *FleetRoutePolicyDirective
		wantYouTube  string
		wantRestarts int
	}{
		{
			// The incident: the revision remembers the dead ru4, the panel pins ru13.
			name:         "directive pins the live same-named node",
			desired:      sochiDesiredRevision("t7g0ZgNU"),
			directive:    sochiYouTubeDirective("bg2ucPAm"),
			wantYouTube:  "bg2ucPAm",
			wantRestarts: 1,
		},
		{
			// No directive: the revision names ru13, the scorer's tie-break picks
			// ru4 (first in UCI order). Neither may move the slot.
			name:         "no directive, revision names a same-named sibling",
			desired:      sochiDesiredRevision("bg2ucPAm"),
			directive:    nil,
			wantYouTube:  "t7g0ZgNU",
			wantRestarts: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend := &applyingBackend{lines: sochiSameNameLines()}
			checkIn(t, backend, tc.desired, tc.directive)
			afterFirst := backend.restarts
			checkIn(t, backend, tc.desired, tc.directive)
			checkIn(t, backend, tc.desired, tc.directive)

			if backend.restarts != tc.wantRestarts || afterFirst != tc.wantRestarts {
				t.Fatalf("restarts after 1 check-in = %d, after 3 = %d, want %d both: the self-heals must settle in one pass", afterFirst, backend.restarts, tc.wantRestarts)
			}
			binding := ""
			for _, line := range backend.lines {
				if value, ok := strings.CutPrefix(line, "passwall2.myshunt.YouTube="); ok {
					binding = strings.Trim(value, "'")
				}
			}
			if binding != tc.wantYouTube {
				t.Fatalf("YouTube bound to %q, want %q", binding, tc.wantYouTube)
			}
		})
	}
}
