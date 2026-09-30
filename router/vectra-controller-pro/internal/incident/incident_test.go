package incident

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecorderWritesOnePerCodePerInterval(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1790000000, 0)
	r := NewRecorder(dir, 10*time.Minute)
	r.now = func() time.Time { return now }
	if !r.Record(Incident{Code: "XRAY_CRASH", Key: "exit 2", Title: "xray exited", Source: "vctl"}) {
		t.Fatal("the first was not written")
	}
	now = now.Add(time.Second) // a tie in time has no order: keep them apart
	if r.Record(Incident{Code: "XRAY_CRASH", Key: "exit 2", Title: "again", Source: "vctl"}) {
		t.Fatal("the same code within the interval was written")
	}
	if !r.Record(Incident{Code: "APPLY_REFUSED", Key: "k", Title: "t", Source: "vctl"}) {
		t.Fatal("another code was held back")
	}
	now = now.Add(11 * time.Minute)
	if !r.Record(Incident{Code: "XRAY_CRASH", Key: "exit 2", Title: "later", Source: "vctl"}) {
		t.Fatal("after the interval it was held back")
	}
	got := Read(dir)
	if len(got) != 3 || got[0].Code != "XRAY_CRASH" || got[0].At.IsZero() || got[2].Title != "later" {
		t.Fatalf("%+v", got)
	}
}

func TestRecorderNeverFillsTheInboxPastItsCap(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < MaxQueued; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", i)), []byte(`{"code":"X"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if NewRecorder(dir, time.Minute).Record(Incident{Code: "Y", Key: "k", Title: "t"}) {
		t.Fatal("wrote past the cap")
	}
}

// The shell's one line of JSON (the dead-man's, the init script's) reads as
// an incident; a file that is not one comes back without a code.
func TestReadTakesTheShellsLine(t *testing.T) {
	dir := t.TempDir()
	line := `{"code":"HANDBACK","key":"renderer missing","title":"vctl handed the router back: renderer missing","at":"2026-09-29T21:03:11Z","source":"init"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "1790000000000000000-HANDBACK-42.json"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "junk.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Read(dir)
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	var hb Pending
	for _, p := range got {
		if p.Code == "HANDBACK" {
			hb = p
		}
	}
	if hb.Source != "init" || hb.At.UTC().Format(time.RFC3339) != "2026-09-29T21:03:11Z" || hb.Path == "" {
		t.Fatalf("%+v", got)
	}
}
