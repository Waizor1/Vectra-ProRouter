package passwall

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type routeVerifyBackend struct {
	lines []string
	codes map[string]string
}

func (b routeVerifyBackend) Show(ctx context.Context, packageName string) ([]string, error) {
	return b.lines, nil
}

func (b routeVerifyBackend) Batch(ctx context.Context, commands []string) error {
	return fmt.Errorf("unexpected batch: %v", commands)
}

func (b routeVerifyBackend) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	command := strings.Join(append([]string{name}, args...), " ")
	if name != "/usr/share/passwall2/test.sh" || len(args) != 2 || args[0] != "url_test_node" {
		return CommandResult{Command: command}, fmt.Errorf("unexpected command %s", command)
	}
	code := b.codes[args[1]]
	if code == "" {
		code = "000"
	}
	return CommandResult{Command: command, Stdout: code}, nil
}

func TestVerifyFleetRoutesRequiresFiveGreenManagedSlots(t *testing.T) {
	backend := routeVerifyBackend{
		lines: standardFleetRouteUCILines(),
		codes: map[string]string{
			"node_world":   "204",
			"node_youtube": "204",
			"node_special": "204",
			"node_tiktok":  "204",
			"node_discord": "204",
		},
	}

	result, err := VerifyFleetRoutes(context.Background(), backend, FleetRoutePolicyIdentity{Name: "new-router"})
	if err != nil {
		t.Fatalf("VerifyFleetRoutes() error = %v", err)
	}
	if !result.OK {
		t.Fatalf("expected verification to pass, errors=%v result=%+v", result.Errors, result)
	}
	if got, want := len(result.Slots), 5; got != want {
		t.Fatalf("slots = %d, want %d", got, want)
	}
	for _, slot := range result.Slots {
		if !slot.BindingOK || !slot.RuleExtrasOK || !slot.NodeExtrasOK || !slot.SmokeOK || slot.StatusCode != 204 {
			t.Fatalf("slot %s not fully green: %+v", slot.SlotID, slot)
		}
	}
}

func TestVerifyFleetRoutesFailsOnNon204RouteSmoke(t *testing.T) {
	backend := routeVerifyBackend{
		lines: standardFleetRouteUCILines(),
		codes: map[string]string{
			"node_world":   "204",
			"node_youtube": "204",
			"node_special": "000",
			"node_tiktok":  "204",
			"node_discord": "204",
		},
	}

	result, err := VerifyFleetRoutes(context.Background(), backend, FleetRoutePolicyIdentity{Name: "new-router"})
	if err != nil {
		t.Fatalf("VerifyFleetRoutes() error = %v", err)
	}
	if result.OK {
		t.Fatalf("expected verification to fail")
	}
	if !strings.Contains(strings.Join(result.Errors, "\n"), "Special: url_test_node returned 000") {
		t.Fatalf("expected Special 000 error, got %v", result.Errors)
	}
}

func TestVerifyFleetRoutesAcceptsRuEntryNetherlandsSpecialFallback(t *testing.T) {
	lines := standardFleetRouteUCILines()
	for i, line := range lines {
		switch line {
		case "passwall2.node_special.remarks='🇳🇱 Netherlands'":
			lines[i] = "passwall2.node_special.remarks='🇷🇺🇳🇱 Netherlands YouTube RU entry'"
		case "passwall2.node_special.address='nl1.example.net'":
			lines[i] = "passwall2.node_special.address='ru-nl.example.net'"
		case "passwall2.node_special.port='443'":
			lines[i] = "passwall2.node_special.port='50055'"
		}
	}
	lines = append(lines, "passwall2.node_special.transport='grpc'")

	backend := routeVerifyBackend{
		lines: lines,
		codes: map[string]string{
			"node_world":   "204",
			"node_youtube": "204",
			"node_special": "204",
			"node_tiktok":  "204",
			"node_discord": "204",
		},
	}

	result, err := VerifyFleetRoutes(context.Background(), backend, FleetRoutePolicyIdentity{Name: "new-router"})
	if err != nil {
		t.Fatalf("VerifyFleetRoutes() error = %v", err)
	}
	if !result.OK {
		t.Fatalf("expected RU-entry Netherlands fallback to pass, errors=%v result=%+v", result.Errors, result)
	}
	var special *RouteSlotVerificationResult
	for i := range result.Slots {
		if result.Slots[i].SlotID == "Special" {
			special = &result.Slots[i]
			break
		}
	}
	if special == nil || !special.BindingOK || !special.SmokeOK || special.StatusCode != 204 {
		t.Fatalf("Special fallback slot not green: %+v", special)
	}
}

func standardFleetRouteUCILines() []string {
	return []string{
		"passwall2.vectra_global=global",
		"passwall2.vectra_global.enabled='1'",
		"passwall2.vectra_global.node='myshunt'",
		"passwall2.myshunt=nodes",
		"passwall2.myshunt.remarks='Маршрутизатор BloopCat'",
		"passwall2.myshunt.protocol='_shunt'",
		"passwall2.myshunt.enabled='1'",
		"passwall2.myshunt.WorldProxy='node_world'",
		"passwall2.myshunt.YouTube='node_youtube'",
		"passwall2.myshunt.Special='node_special'",
		"passwall2.myshunt.Tiktok='node_tiktok'",
		"passwall2.myshunt.DiscordVoiceUdp='node_discord'",
		"passwall2.WorldProxy=shunt_rules",
		"passwall2.WorldProxy.remarks='WorldProxy'",
		"passwall2.YouTube=shunt_rules",
		"passwall2.YouTube.remarks='YouTube'",
		"passwall2.Special=shunt_rules",
		"passwall2.Special.remarks='Special'",
		"passwall2.Tiktok=shunt_rules",
		"passwall2.Tiktok.remarks='Tiktok'",
		"passwall2.DiscordVoiceUdp=shunt_rules",
		"passwall2.DiscordVoiceUdp.remarks='DiscordVoiceUdp'",
		"passwall2.DiscordVoiceUdp.network='udp'",
		"passwall2.DiscordVoiceUdp.port='19294-19344,50000-50100'",
		"passwall2.node_world=nodes",
		"passwall2.node_world.remarks='🇷🇺🇵🇱 ⚡️Польша YouTube 🚫Ad🚫'",
		"passwall2.node_world.protocol='vless'",
		"passwall2.node_world.enabled='1'",
		"passwall2.node_world.address='ru4.nfnpx.online'",
		"passwall2.node_world.port='50053'",
		"passwall2.node_world.transport='grpc'",
		"passwall2.node_youtube=nodes",
		"passwall2.node_youtube.remarks='🇷🇺 Russia YouTube RU entry'",
		"passwall2.node_youtube.protocol='vless'",
		"passwall2.node_youtube.enabled='1'",
		"passwall2.node_youtube.address='ru1.example.net'",
		"passwall2.node_youtube.port='50051'",
		"passwall2.node_youtube.transport='grpc'",
		"passwall2.node_special=nodes",
		"passwall2.node_special.remarks='🇳🇱 Netherlands'",
		"passwall2.node_special.protocol='vless'",
		"passwall2.node_special.enabled='1'",
		"passwall2.node_special.address='nl1.example.net'",
		"passwall2.node_special.port='443'",
		"passwall2.node_tiktok=nodes",
		"passwall2.node_tiktok.remarks='🇧🇾 Belarus'",
		"passwall2.node_tiktok.protocol='vless'",
		"passwall2.node_tiktok.enabled='1'",
		"passwall2.node_tiktok.address='by1.example.net'",
		"passwall2.node_tiktok.port='443'",
		"passwall2.node_discord=nodes",
		"passwall2.node_discord.remarks='🇵🇱 Poland YouTube RU entry'",
		"passwall2.node_discord.protocol='vless'",
		"passwall2.node_discord.enabled='1'",
		"passwall2.node_discord.address='ru-pl.example.net'",
		"passwall2.node_discord.port='50053'",
		"passwall2.node_discord.transport='grpc'",
		"passwall2.node_discord.mux='1'",
		"passwall2.node_discord.mux_concurrency='-1'",
		"passwall2.node_discord.xudp_concurrency='16'",
	}
}

// The cron watchdog held the shared url_test lock for the whole wait: the
// slot is reported unjudged, not as a 000 smoke failure the panel would count
// against the node.
func TestVerifyFleetRoutesReportsABusyURLTestLockAsUnjudged(t *testing.T) {
	dir := useURLTestLockDir(t)
	originalWait := URLTestLockWait
	URLTestLockWait = 100 * time.Millisecond
	t.Cleanup(func() { URLTestLockWait = originalWait })
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "owner"), []byte(fmt.Sprintf("%d 4242", time.Now().Unix())), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := routeVerifyBackend{lines: standardFleetRouteUCILines(), codes: map[string]string{}}

	result, err := VerifyFleetRoutes(context.Background(), backend, FleetRoutePolicyIdentity{Name: "new-router"})
	if err != nil {
		t.Fatalf("VerifyFleetRoutes() error = %v", err)
	}
	if len(result.Slots) == 0 {
		t.Fatal("expected slot results")
	}
	for _, slot := range result.Slots {
		if !slot.Unjudged || slot.SmokeOK || slot.StatusCode != 0 {
			t.Fatalf("slot %s = %+v, want unjudged with no status", slot.SlotID, slot)
		}
	}
	encoded, _ := json.Marshal(result.Slots[0])
	if !strings.Contains(string(encoded), `"unjudged":true`) {
		t.Fatalf("payload lacks the unjudged flag: %s", encoded)
	}
	judged, _ := json.Marshal(RouteSlotVerificationResult{SlotID: "x", SmokeOK: true})
	if strings.Contains(string(judged), "unjudged") {
		t.Fatalf("a judged slot must not carry the flag (older panels): %s", judged)
	}
}
