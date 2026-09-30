package bugreport

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vectra-controller-pro/internal/incident"
)

func runEnv(t *testing.T, s signed, url string, client *http.Client) Env {
	dir := t.TempDir()
	return Env{
		SpoolDir: filepath.Join(dir, "spool"), StatePath: filepath.Join(dir, "state.json"),
		InboxDir: filepath.Join(dir, "inbox"), CrashDir: filepath.Join(dir, "crash"),
		Now: func() time.Time { return s.now }, Alive: func(int) bool { return false },
		Dmesg: func() string { return "" }, BootID: func() string { return "boot-1" },
		TableLoaded: func() bool { return false }, XrayRunning: func() bool { return true },
		Facts:  func() (Router, []string) { return Router{DeviceID: "vectra-0123456789ab", Vctl: "0.6.0-r19"}, nil },
		Sender: s.sender(url, client), Budget: 20 * time.Second,
	}
}

func TestRunTakesTheInboxAndSendsWhatIsDue(t *testing.T) {
	s := newSigned(t)
	var got []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.verify(t, r, body)
		got = append(got, r.Header.Get("x-vectra-report-id"))
		w.WriteHeader(202)
	}))
	defer srv.Close()
	e := runEnv(t, s, srv.URL+"/errors/router", srv.Client())
	rec := incident.NewRecorder(e.InboxDir, time.Minute)
	rec.Record(incident.Incident{Code: "APPLY_REFUSED", Key: "k", Title: "refused", Source: "vctl"})
	queued, sent, err := Run(context.Background(), e)
	if err != nil || queued != 1 || sent != 1 || len(got) != 1 {
		t.Fatalf("queued %d, sent %d, err %v, got %v", queued, sent, err, got)
	}
	if left := incident.Read(e.InboxDir); len(left) != 0 {
		t.Fatal("the inbox kept what was taken")
	}
	if es := (Spool{Dir: e.SpoolDir, MaxFiles: 32, MaxBytes: 512 << 10}).Entries(); len(es) != 0 {
		t.Fatal("a delivered report stayed in the spool")
	}
	// The same problem again within 6 hours waits for the window's end.
	rec2 := incident.NewRecorder(e.InboxDir, time.Minute)
	rec2.Record(incident.Incident{Code: "APPLY_REFUSED", Key: "k", Title: "refused again", Source: "vctl"})
	if _, sent, _ := Run(context.Background(), e); sent != 0 {
		t.Fatal("sent again within the window")
	}
}

func TestRunCountsWhatItCannotKeep(t *testing.T) {
	s := newSigned(t)
	e := runEnv(t, s, "https://127.0.0.1:1/errors/router", &http.Client{Timeout: time.Second})
	if err := os.WriteFile(e.SpoolDir, nil, 0o600); err != nil { // a file where the spool goes
		t.Fatal(err)
	}
	incident.NewRecorder(e.InboxDir, time.Minute).Record(incident.Incident{Code: "TEST", Key: "t", Title: "t"})
	if _, _, err := Run(context.Background(), e); err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	if st := LoadState(e.StatePath); st.Dropped != 1 || st.LastError == "" {
		t.Fatalf("%+v", st)
	}
}

// A run with nothing to do leaves the state on flash as it is: it is written
// when something changed, and at the first run.
func TestRunWritesTheStateOnlyWhenItChanges(t *testing.T) {
	s := newSigned(t)
	e := runEnv(t, s, "https://127.0.0.1:1/errors/router", &http.Client{Timeout: time.Second})
	if _, _, err := Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(e.StatePath)
	if err != nil {
		t.Fatal("the first run left no state")
	}
	old := st.ModTime().Add(-time.Hour)
	if err := os.Chtimes(e.StatePath, old, old); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(e.StatePath); !st.ModTime().Equal(old) {
		t.Fatal("a run with nothing to do wrote the state again")
	}
}

// What was queued before the clock was set is kept until it is, and sent —
// not taken for older than a week when NTP moves the clock on.
func TestRunKeepsWhatWasQueuedBeforeTheClockWasSet(t *testing.T) {
	s := newSigned(t)
	got := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got++; w.WriteHeader(202) }))
	defer srv.Close()
	e := runEnv(t, s, srv.URL+"/errors/router", srv.Client())
	boot := time.Unix(100, 0)
	e.Now = func() time.Time { return boot }
	e.Sender.Now = e.Now
	incident.NewRecorder(e.InboxDir, time.Minute).Record(incident.Incident{Code: "UNEXPECTED_REBOOT", Key: "k", Title: "t", At: boot})
	if _, sent, _ := Run(context.Background(), e); sent != 0 || got != 0 {
		t.Fatal("sent with no clock")
	}
	e.Now = func() time.Time { return s.now }
	e.Sender.Now = e.Now
	if _, sent, _ := Run(context.Background(), e); sent != 1 || got != 1 {
		t.Fatalf("sent %d, the server got %d, once the clock was set", sent, got)
	}
	if st := LoadState(e.StatePath); st.Dropped != 0 {
		t.Fatalf("dropped %d", st.Dropped)
	}
}
