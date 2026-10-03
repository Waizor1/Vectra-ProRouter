package state

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A check-in that changed nothing writes nothing — and a changed state is
// written, as is an unchanged one once state.json or its last-good copy is
// gone, or state.json was rewritten behind the Saver's back.
func TestSaverWritesOnlyWhatChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	lastGood := path + ".last-good"
	var s Saver
	st := PersistedState{RouterID: "r-1", AgentToken: "tok", ConfigDigest: "abc"}
	// stamp is state.json's identity: every write renames a new file in.
	stamp := func() string {
		t.Helper()
		fi, err := os.Stat(path)
		if err != nil {
			return "gone"
		}
		return fmt.Sprintf("%s/%d", fi.ModTime().Format(time.RFC3339Nano), fi.Size())
	}
	save := func() bool {
		t.Helper()
		before := stamp()
		time.Sleep(2 * time.Millisecond) // a rewrite gets a later ModTime
		if err := s.Save(path, st); err != nil {
			t.Fatal(err)
		}
		return stamp() != before
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }

	if !save() || !exists(lastGood) {
		t.Fatal("the first save wrote nothing")
	}
	if save() {
		t.Fatal("an unchanged state was written again")
	}
	st.ConfigDigest = "def"
	if !save() {
		t.Fatal("a changed state was not written")
	}
	if got, err := Load(path); err != nil || got.ConfigDigest != "def" {
		t.Fatalf("Load = %+v, %v", got, err)
	}
	// The last-good copy gone: written again, unchanged as the state is.
	if err := os.Remove(lastGood); err != nil {
		t.Fatal(err)
	}
	if !save() || !exists(lastGood) {
		t.Fatal("a missing last-good copy was not written again")
	}
	if save() {
		t.Fatal("written again once the last-good copy was back")
	}
	// state.json gone: written again.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if !save() {
		t.Fatal("a missing state.json was not written again")
	}
	// Another vctl process saved its own: the daemon's is written over it,
	// as before the Saver.
	if err := Save(path, PersistedState{RouterID: "r-1", AgentToken: "tok", ConfigDigest: "other-process"}); err != nil {
		t.Fatal(err)
	}
	if !save() {
		t.Fatal("a state.json another process rewrote was left as it was")
	}
	if got, _ := Load(path); got.ConfigDigest != "def" {
		t.Fatalf("Load = %+v", got)
	}
}
