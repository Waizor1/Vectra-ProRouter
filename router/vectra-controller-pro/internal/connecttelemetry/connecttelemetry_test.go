package connecttelemetry

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
	"vectra-controller-pro/internal/controlplane"
)

func TestUnavailableIsOmitted(t *testing.T) {
	out := Build(func(string) ([]byte, error) { return nil, os.ErrNotExist }, time.Now(), Snapshot{})
	b, _ := json.Marshal(out)
	if string(b) != "{}" {
		t.Fatalf("invented observation: %s", b)
	}
}
func TestMeasuredReadingsAndStaleVerdict(t *testing.T) {
	now := time.Unix(1700000000, 0)
	country := "PL"
	read := func(p string) ([]byte, error) {
		if p == "/proc/uptime" {
			return []byte("125.9 300.2"), nil
		}
		return []byte("1700000050 00:11:22:33:44:55 192.168.1.2 a *\n1699999999 00:11:22:33:44:66 192.168.1.3 b *\n1700000060 00:11:22:33:44:55 192.168.1.2 a *\n"), nil
	}
	out := Build(read, now, Snapshot{Telemetry: controlplane.RouterConnectTelemetry{Verdict: "ok", ExitCountry: &country}, ObservedAt: now.Add(-3 * time.Minute)})
	if out.Verdict != "" || out.ExitCountry != nil || out.UptimeSec == nil || *out.UptimeSec != 125 || out.LanClients == nil || *out.LanClients != 1 {
		t.Fatalf("%+v", out)
	}
}
func TestNegativeInvalidFutureAndOversizedRemainUnknown(t *testing.T) {
	now := time.Now()
	bad := "node-pl5"
	out := Build(func(string) ([]byte, error) { return []byte("-1"), nil }, now, Snapshot{Telemetry: controlplane.RouterConnectTelemetry{Verdict: "running", ExitCountry: &bad}, ObservedAt: now.Add(time.Minute)})
	if out.UptimeSec != nil || out.LanClients != nil || out.Verdict != "" || out.ExitCountry != nil {
		t.Fatalf("%+v", out)
	}
	if LeaseCount([]byte(strings.Repeat("a", MaxFileBytes+1)), now) != nil {
		t.Fatal("accepted oversized leases")
	}
}
func TestMeasuredCountryAndFalseSurvive(t *testing.T) {
	now := time.Now()
	cc := "DE"
	off := false
	out := Build(func(string) ([]byte, error) { return nil, os.ErrNotExist }, now, Snapshot{Telemetry: controlplane.RouterConnectTelemetry{Verdict: "down", ExitCountry: &cc, SupportAccess: &off}, ObservedAt: now})
	b, _ := json.Marshal(out)
	if !strings.Contains(string(b), `"supportAccess":false`) || out.ExitCountry == nil || *out.ExitCountry != "DE" {
		t.Fatalf("%s", b)
	}
}
