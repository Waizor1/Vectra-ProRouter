package bugreport

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/incident"
)

func TestFromIncidentRedactsAndFingerprints(t *testing.T) {
	now := time.Unix(1790000000, 0).UTC()
	in := incident.Incident{
		Code: "APPLY_REFUSED", Key: "xray: failed to load geosite", Title: "xray -test refused https://sub.example.com/x",
		Source: "vctl", Details: map[string]any{"url": "https://sub.example.com/x"},
		Log: []string{"dial 203.0.113.9:443"},
	}
	r := FromIncident(in, Router{DeviceID: "vectra-0123456789ab", Vctl: "0.6.0-r19"}, now)
	if r.Schema != 1 || r.Count != 1 || r.Severity != "high" || !r.FirstAt.Equal(now) || !r.LastAt.Equal(now) {
		t.Fatalf("%+v", r)
	}
	if r.Fingerprint != Fingerprint("APPLY_REFUSED", "xray: failed to load geosite") || len(r.Fingerprint) != 64 {
		t.Fatalf("fingerprint %q", r.Fingerprint)
	}
	if strings.Contains(r.Title, "sub.example.com") || r.Evidence.Details["url"] != "<url>" || r.Evidence.Log[0] != "dial <ip>:443" {
		t.Fatalf("not redacted: %+v", r)
	}
	if len(r.ReportID) != 36 || r.ReportID == NewID() {
		t.Fatalf("report id %q", r.ReportID)
	}
}

func TestEncodeKeepsTheBodyUnderItsCap(t *testing.T) {
	r := Report{Schema: 1, ReportID: NewID(), Code: "VCTL_PANIC", Severity: "critical", Fingerprint: Fingerprint("VCTL_PANIC", "k"),
		Title: strings.Repeat("т", 500), Count: 1, Router: Router{DeviceID: "d", Vctl: "v"}}
	r.Evidence.Stack = strings.Repeat("goroutine 1 [running]:\n", 3000)
	for i := 0; i < 500; i++ {
		r.Evidence.Log = append(r.Evidence.Log, strings.Repeat("x", 200))
	}
	b, err := r.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > MaxBody {
		t.Fatalf("%d bytes", len(b))
	}
	var back Report
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if len([]rune(back.Title)) > MaxTitle || len(back.Evidence.Log) > MaxLog || len(back.Evidence.Stack) > MaxStack+8 {
		t.Fatalf("title %d, log %d, stack %d", len([]rune(back.Title)), len(back.Evidence.Log), len(back.Evidence.Stack))
	}
	if back.Router.Vctl != "v" || back.Code != "VCTL_PANIC" {
		t.Fatal("the facts went with the evidence")
	}
}

func TestSeverities(t *testing.T) {
	if DefaultSeverity("VCTL_PANIC") != "critical" || DefaultSeverity("NOT_A_CODE") != "medium" {
		t.Fatal(DefaultSeverity("VCTL_PANIC"), DefaultSeverity("NOT_A_CODE"))
	}
	if !(SeverityRank("critical") > SeverityRank("high") && SeverityRank("high") > SeverityRank("medium") && SeverityRank("medium") > SeverityRank("low")) {
		t.Fatal("ranks out of order")
	}
}
