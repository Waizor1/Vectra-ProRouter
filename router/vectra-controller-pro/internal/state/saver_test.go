package state

import (
	"os"
	"path/filepath"
	"testing"
)

// A check-in that changed nothing writes nothing: the last-good copy, gone
// behind the Saver's back, is not written again by an unchanged state — and
// is by a changed one, or once state.json itself is gone or rewritten.
func TestSaverWritesOnlyWhatChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	lastGood := path + ".last-good"
	var s Saver
	st := PersistedState{RouterID: "r-1", AgentToken: "tok", ConfigDigest: "abc"}
	written := func() bool {
		t.Helper()
		_, err := os.Stat(lastGood)
		if err == nil {
			if err := os.Remove(lastGood); err != nil {
				t.Fatal(err)
			}
		}
		return err == nil
	}
	save := func() {
		t.Helper()
		if err := s.Save(path, st); err != nil {
			t.Fatal(err)
		}
	}

	save()
	if !written() {
		t.Fatal("the first save wrote nothing")
	}
	save()
	if written() {
		t.Fatal("an unchanged state was written again")
	}
	st.ConfigDigest = "def"
	save()
	if !written() {
		t.Fatal("a changed state was not written")
	}
	got, err := Load(path)
	if err != nil || got.ConfigDigest != "def" {
		t.Fatalf("Load = %+v, %v", got, err)
	}

	// state.json gone: written again, unchanged or not.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	save()
	if !written() {
		t.Fatal("a missing state.json was not written again")
	}
	// Another vctl process saved its own: the daemon's is written over it,
	// as before the Saver.
	if err := Save(path, PersistedState{RouterID: "r-1", AgentToken: "tok", ConfigDigest: "other-process"}); err != nil {
		t.Fatal(err)
	}
	_ = written()
	save()
	if !written() {
		t.Fatal("a state.json another process rewrote was left as it was")
	}
	if got, _ := Load(path); got.ConfigDigest != "def" {
		t.Fatalf("Load = %+v", got)
	}
}
