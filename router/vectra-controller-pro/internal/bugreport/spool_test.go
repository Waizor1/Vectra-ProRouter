package bugreport

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func rep(code, key, sev string, at time.Time) Report {
	return Report{Schema: 1, ReportID: NewID(), Code: code, Severity: sev, Fingerprint: Fingerprint(code, key),
		Title: code, FirstAt: at, LastAt: at, Count: 1, Router: Router{DeviceID: "d", Vctl: "v"}}
}

func TestSpoolMergesTheSameFingerprint(t *testing.T) {
	s := Spool{Dir: t.TempDir(), MaxFiles: 32, MaxBytes: 512 << 10}
	t0 := time.Unix(1790000000, 0).UTC()
	if merged, _, err := s.Add(rep("XRAY_CRASH", "exit 2", "high", t0), t0, time.Time{}); merged || err != nil {
		t.Fatal(merged, err)
	}
	if merged, _, err := s.Add(rep("XRAY_CRASH", "exit 2", "high", t0.Add(time.Minute)), t0.Add(time.Minute), time.Time{}); !merged || err != nil {
		t.Fatal(merged, err)
	}
	es := s.Entries()
	if len(es) != 1 || es[0].Report.Count != 2 || !es[0].Report.LastAt.Equal(t0.Add(time.Minute)) || !es[0].Report.FirstAt.Equal(t0) {
		t.Fatalf("%+v", es)
	}
}

func TestSpoolEvictsTheLeastSevereOldestFirst(t *testing.T) {
	s := Spool{Dir: t.TempDir(), MaxFiles: 3, MaxBytes: 512 << 10}
	t0 := time.Unix(1790000000, 0).UTC()
	crit := rep("VCTL_PANIC", "p", "critical", t0)
	s.Add(crit, t0, time.Time{})
	for i := 0; i < 4; i++ {
		at := t0.Add(time.Duration(i+1) * time.Minute)
		s.Add(rep("KERNEL_ERROR", string(rune('a'+i)), "medium", at), at, time.Time{})
	}
	es := s.Entries()
	if len(es) != 3 || es[0].Report.ReportID != crit.ReportID {
		t.Fatalf("%d kept, first %s", len(es), es[0].Report.Code)
	}
	if es[1].Report.Fingerprint != Fingerprint("KERNEL_ERROR", "c") {
		t.Fatalf("the oldest medium ones did not go first: %+v", es)
	}
}

func TestSpoolHoldsANewOneBackAndDropsWhatItCannotRead(t *testing.T) {
	s := Spool{Dir: t.TempDir(), MaxFiles: 32, MaxBytes: 512 << 10}
	t0 := time.Unix(1790000000, 0).UTC()
	s.Add(rep("OOM_KILL", "xray", "high", t0), t0, t0.Add(6*time.Hour))
	if err := os.WriteFile(filepath.Join(s.Dir, "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	es := s.Entries()
	if len(es) != 1 || !es[0].Meta.NextAt.Equal(t0.Add(6*time.Hour)) {
		t.Fatalf("%+v", es)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "broken.json")); !os.IsNotExist(err) {
		t.Fatal("a file that is no entry stayed")
	}
}

func TestSpoolAddOnAnUnwritableDirFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "file-not-dir")
	if err := os.WriteFile(dir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s := Spool{Dir: dir, MaxFiles: 32, MaxBytes: 512 << 10}
	t0 := time.Unix(1790000000, 0).UTC()
	if _, _, err := s.Add(rep("TEST", "t", "low", t0), t0, time.Time{}); err == nil {
		t.Fatal("no error")
	}
}
