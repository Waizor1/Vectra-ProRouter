package connectactions

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestJournalLookupActiveWithoutMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "actions.json")
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{RouterID: "router-1", OwnerRef: "owner-1"}
	e, _ := Parse(payload("reboot", `{}`))
	if _, _, err := j.Begin(b, e); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rec, found, err := j.Lookup(b, e.ActionID)
	if err != nil || !found || rec.ActionID != e.ActionID || rec.Status != Started {
		t.Fatalf("lookup active %v %v %+v", found, err, rec)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("lookup modified unfinished journal")
	}
}
func TestJournalLookupArchivedAndMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "actions.json")
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{RouterID: "router-1", OwnerRef: "owner-1"}
	e, _ := Parse(payload("set_wifi", `{"ssid":"lookup-ssid","password":"lookup-password"}`))
	if _, _, err := j.Begin(b, e); err != nil {
		t.Fatal(err)
	}
	if err := j.Complete(b, e.ActionID, Succeeded); err != nil {
		t.Fatal(err)
	}
	if err := j.withData(func(data *journalData) (bool, error) { return true, j.archiveTerminal(data) }); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	rec, found, err := reopened.Lookup(b, e.ActionID)
	if err != nil || !found || rec.Status != Succeeded || rec.ParamDigest != "" {
		t.Fatalf("lookup archived %v %v %+v", found, err, rec)
	}
	for _, tc := range []struct {
		b  Binding
		id string
	}{{b, "legacy-native-job"}, {Binding{RouterID: "router-2", OwnerRef: b.OwnerRef}, e.ActionID}, {Binding{RouterID: b.RouterID, OwnerRef: "owner-2"}, e.ActionID}} {
		if _, found, err := j.Lookup(tc.b, tc.id); err != nil || found {
			t.Fatal("missing/scope lookup returned another owner's record")
		}
	}
}
func TestJournalLookupRejectsInvalidScopeAndCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "actions.json")
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{RouterID: "router-1", OwnerRef: "owner-1"}
	e, _ := Parse(payload("reboot", `{}`))
	if _, _, err := j.Begin(b, e); err != nil {
		t.Fatal(err)
	}
	if _, found, err := j.Lookup(Binding{}, e.ActionID); err != ErrUnauthorized || found {
		t.Fatal("invalid binding accepted")
	}
	if _, found, err := j.Lookup(b, "../invalid"); err != ErrInvalidPayload || found {
		t.Fatal("invalid action id accepted")
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := j.Lookup(b, e.ActionID); err != ErrJournal || found {
		t.Fatal("corrupt active journal treated as missing")
	}
}
func TestJournalLookupRejectsCorruptArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "actions.json")
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{RouterID: "router-1", OwnerRef: "owner-1"}
	e, _ := Parse(payload("reboot", `{}`))
	if _, _, err := j.Begin(b, e); err != nil {
		t.Fatal(err)
	}
	if err := j.Complete(b, e.ActionID, Succeeded); err != nil {
		t.Fatal(err)
	}
	if err := j.withData(func(data *journalData) (bool, error) { return true, j.archiveTerminal(data) }); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(j.receiptPath(scopeKey(b, e.ActionID)), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := j.Lookup(b, e.ActionID); err != ErrJournal || found {
		t.Fatal("corrupt archive treated as missing")
	}
}
