package connectactions

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestJournalDurableReplayAndNoSecretPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "actions.json")
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	e, err := Parse(payload("set_wifi", `{"ssid":"SENSITIVE_SSID","password":"SENSITIVE_PASSWORD"}`))
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{RouterID: "router-1", OwnerRef: "owner-1"}
	rec, started, err := j.Begin(b, e)
	if err != nil || !started || rec.Status != Started {
		t.Fatalf("begin %v %v %+v", started, err, rec)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"SENSITIVE_SSID", "SENSITIVE_PASSWORD", "ssid", "password", "params"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Error("sensitive data persisted")
		}
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0600 {
		t.Error("journal not private")
	}
	reopened, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	rec, started, err = reopened.Begin(b, e)
	if err != ErrSensitiveReplay || started || rec.Status != Interrupted {
		t.Fatalf("restart duplicated action: %v %v %+v", started, err, rec)
	}
	if err := reopened.Complete(b, e.ActionID, Succeeded); err != ErrTransition {
		t.Error("interrupted execution completed without proof")
	}
}
func TestJournalCompleteAndScope(t *testing.T) {
	j, err := OpenJournal(filepath.Join(t.TempDir(), "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	e, _ := Parse(payload("reboot", `{}`))
	b := Binding{RouterID: "r1", OwnerRef: "owner-1"}
	if _, _, err := j.Begin(b, e); err != nil {
		t.Fatal(err)
	}
	if err := j.Complete(b, e.ActionID, Succeeded); err != nil {
		t.Fatal(err)
	}
	if err := j.Complete(b, e.ActionID, Succeeded); err != nil {
		t.Fatal("completion not idempotent")
	}
	rec, started, err := j.Begin(b, e)
	if err != nil || started || rec.Status != Succeeded {
		t.Error("completed action replayed")
	}
	if err := j.Complete(Binding{RouterID: "r2", OwnerRef: b.OwnerRef}, e.ActionID, Succeeded); err != ErrTransition {
		t.Error("wrong router completed action")
	}
	e.OwnerRef = "owner-2"
	if _, _, err := j.Begin(b, e); err != ErrUnauthorized {
		t.Error("new owner envelope accepted under old claim")
	}
	b.OwnerRef = "owner-2"
	if _, started, err := j.Begin(b, e); err != nil || !started {
		t.Error("owner-scoped action id rejected")
	}
}
func TestJournalRejectsCorruptionAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "actions.json")
	if err := os.WriteFile(path, []byte("SENSITIVE corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenJournal(path); err != ErrJournal {
		t.Error("corruption did not fail closed")
	}
	if err := os.Symlink(path, path+"link"); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenJournal(path + "link"); err != ErrJournal {
		t.Error("journal symlink accepted")
	}
}
func TestJournalRejectsDifferentParamsForSameAction(t *testing.T) {
	j, err := OpenJournal(filepath.Join(t.TempDir(), "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{RouterID: "r1", OwnerRef: "owner-1"}
	first, _ := Parse(payload("set_auto_update", `{"enabled":true}`))
	if _, _, err := j.Begin(b, first); err != nil {
		t.Fatal(err)
	}
	different, _ := Parse(payload("set_auto_update", `{"enabled":false}`))
	if _, started, err := j.Begin(b, different); err != ErrReplayConflict || started {
		t.Fatal("different parameters replayed")
	}
	different, _ = Parse(payload("reboot", `{}`))
	if _, started, err := j.Begin(b, different); err != ErrReplayConflict || started {
		t.Fatal("different action replayed")
	}
}
func TestJournalRecoveryIsOwnerScoped(t *testing.T) {
	j, err := OpenJournal(filepath.Join(t.TempDir(), "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{RouterID: "r1", OwnerRef: "owner-1"}
	e, _ := Parse(payload("reboot", `{}`))
	if _, _, err := j.Begin(b, e); err != nil {
		t.Fatal(err)
	}
	other := Binding{RouterID: "r1", OwnerRef: "owner-2"}
	e.OwnerRef = other.OwnerRef
	if _, _, err := j.Begin(other, e); err != nil {
		t.Fatal(err)
	}
	records, err := j.Recover(b)
	if err != nil || len(records) != 1 || records[0].Status != Interrupted {
		t.Fatal("owner recovery wrong")
	}
	if err := j.Complete(other, e.ActionID, Succeeded); err != nil {
		t.Fatal("recovery interrupted unrelated owner")
	}
}
func TestJournalParallelBeginsNeverExecuteTwice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "actions.json")
	b := Binding{RouterID: "r1", OwnerRef: "owner-1"}
	e, _ := Parse(payload("reboot", `{}`))
	var wg sync.WaitGroup
	var starts atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j, err := OpenJournal(path)
			if err != nil {
				t.Error(err)
				return
			}
			_, started, err := j.Begin(b, e)
			if err != nil {
				t.Error(err)
			}
			if started {
				starts.Add(1)
			}
		}()
	}
	wg.Wait()
	if starts.Load() != 1 {
		t.Fatalf("action executions admitted: %d", starts.Load())
	}
}
func TestJournalPreservesReplayHistoryWhenFull(t *testing.T) {
	j, err := OpenJournal(filepath.Join(t.TempDir(), "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{RouterID: "r1", OwnerRef: "owner-1"}
	// Populate through the same writer to avoid 1024 independent fsync cycles.
	err = j.withData(func(data *journalData) (bool, error) {
		for i := 0; i < MaxRecords; i++ {
			id := fmt.Sprintf("action-%d", i)
			sum := sha256.Sum256([]byte(`{}`))
			data.Records[scopeKey(b, id)] = Record{ActionID: id, Action: "reboot", Status: Succeeded, ParamDigest: hex.EncodeToString(sum[:])}
		}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	e, _ := Parse(payload("reboot", `{}`))
	if _, started, err := j.Begin(b, e); err != nil || !started {
		t.Fatalf("completed history exhausted new actions: %v", err)
	}
	entries, err := os.ReadDir(j.path + ".receipts")
	if err != nil || len(entries) == 0 {
		t.Fatal("prior actions have no durable receipts")
	}

	e.ActionID = "action-0"
	if rec, started, err := j.Begin(b, e); err != nil || started || rec.Status != Succeeded {
		t.Fatal("existing action replay lost")
	}
}
func TestJournalRejectsDuplicateFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "actions.json")
	for _, raw := range []string{`{"version":1,"version":1,"records":{}}`, `{"version":1,"records":{},"records":{}}`} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenJournal(path); err != ErrJournal {
			t.Error("ambiguous duplicate journal fields accepted")
		}
	}
}

func TestJournalArchiveSurvivesRestartAndPreservesOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "actions.json")
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{RouterID: "r1", OwnerRef: "owner-1"}
	e, _ := Parse(payload("set_wifi", `{"ssid":"ARCHIVE_SECRET_SSID","password":"ARCHIVE_SECRET_PASSWORD"}`))
	for i := 0; ; i++ {
		e.ActionID = fmt.Sprintf("wifi-%d", i)
		if strings.HasPrefix(scopeKey(b, e.ActionID), "000") {
			break
		}
	}

	if _, _, err := j.Begin(b, e); err != nil {
		t.Fatal(err)
	}
	if err := j.Complete(b, e.ActionID, Succeeded); err != nil {
		t.Fatal(err)
	}
	// Exercise archive via a full active map without 1024 fsync cycles.
	if err := j.withData(func(data *journalData) (bool, error) {
		sum := sha256.Sum256([]byte(`{}`))
		for i := len(data.Records); i < MaxRecords; i++ {
			id := fmt.Sprintf("old-%d", i)
			data.Records[scopeKey(b, id)] = Record{ActionID: id, Action: "reboot", Status: Failed, ParamDigest: hex.EncodeToString(sum[:])}
		}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	next, _ := Parse(payload("reboot", `{}`))
	next.ActionID = "new-action"
	if _, started, err := j.Begin(b, next); err != nil || !started {
		t.Fatalf("archive %v", err)
	}
	reopened, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	active, err := reopened.read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := active.Records[scopeKey(b, e.ActionID)]; ok {
		t.Fatal("test action was not archived")
	}
	rec, started, err := reopened.Begin(b, e)
	if err != ErrSensitiveReplay || started || rec.Status != Succeeded {
		t.Fatal("archived wifi replayed")
	}
	if err := reopened.Complete(b, e.ActionID, Succeeded); err != nil {
		t.Fatal("archived completion not idempotent")
	}
	changed, _ := Parse(payload("reboot", `{}`))
	changed.ActionID = e.ActionID
	if _, started, err := reopened.Begin(b, changed); err != ErrReplayConflict || started {
		t.Fatal("archived action collision accepted")
	}
	other := b
	other.OwnerRef = "owner-2"
	e.OwnerRef = other.OwnerRef
	if _, started, err := reopened.Begin(other, e); err != nil || !started {
		t.Fatal("old owner history blocked new owner scope")
	}
	err = filepath.WalkDir(path+".receipts", func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if bytes.Contains(raw, []byte("ARCHIVE_SECRET")) {
			t.Error("secret persisted in archive")
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		if st.Mode().Perm() != 0600 {
			t.Error("receipt permissions")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestJournalArchiveCrashCopiesAreSafe(t *testing.T) {
	j, err := OpenJournal(filepath.Join(t.TempDir(), "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{RouterID: "r1", OwnerRef: "owner-1"}
	e, _ := Parse(payload("reboot", `{}`))
	if _, _, err := j.Begin(b, e); err != nil {
		t.Fatal(err)
	}
	if err := j.Complete(b, e.ActionID, Succeeded); err != nil {
		t.Fatal(err)
	}
	// Persist the receipt, then simulate a crash before updating the active map.
	if err := j.withData(func(data *journalData) (bool, error) { return false, j.archiveTerminal(data) }); err != nil {
		t.Fatal(err)
	}
	if rec, started, err := j.Begin(b, e); err != nil || started || rec.Status != Succeeded {
		t.Fatal("crash duplicate receipt admitted mutation")
	}
	key := scopeKey(b, e.ActionID)
	rec, ok, err := j.readReceipt(key)
	if err != nil || !ok {
		t.Fatal("receipt missing")
	}
	rec.Status = Failed
	if err := writeReceipt(j.receiptPath(key), rec); err != nil {
		t.Fatal(err)
	}
	if _, started, err := j.Begin(b, e); err != ErrJournal || started {
		t.Fatal("mismatched crash copies accepted")
	}
}
func TestJournalArchiveBudgetRejectsWithoutLosingHistory(t *testing.T) {
	j, err := OpenJournal(filepath.Join(t.TempDir(), "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{RouterID: "r1", OwnerRef: "owner-1"}
	if err := j.withData(func(data *journalData) (bool, error) {
		sum := sha256.Sum256([]byte(`{}`))
		for i := 0; i < MaxRecords; i++ {
			id := fmt.Sprintf("budget-%d", i)
			data.Records[scopeKey(b, id)] = Record{ActionID: id, Action: "reboot", Status: Succeeded, ParamDigest: hex.EncodeToString(sum[:])}
		}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	j.archiveBudget = receiptMaxBytes // Only enough for archive root, no new receipt.
	e, _ := Parse(payload("reboot", `{}`))
	if _, started, err := j.Begin(b, e); err != ErrJournalFull || started {
		t.Fatal("disk budget did not block new execution")
	}
	e.ActionID = "budget-0"
	if rec, started, err := j.Begin(b, e); err != nil || started || rec.Status != Succeeded {
		t.Fatal("storage failure lost replay history")
	}
}
func TestJournalArchiveRejectsSymlinkAndCorruption(t *testing.T) {
	j, err := OpenJournal(filepath.Join(t.TempDir(), "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{RouterID: "r1", OwnerRef: "owner-1"}
	e, _ := Parse(payload("reboot", `{}`))
	if err := os.Symlink(t.TempDir(), j.path+".receipts"); err != nil {
		t.Fatal(err)
	}
	if _, started, err := j.Begin(b, e); err != ErrJournal || started {
		t.Fatal("archive root symlink followed")
	}
	if err := os.Remove(j.path + ".receipts"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := j.Begin(b, e); err != nil {
		t.Fatal(err)
	}
	if err := j.Complete(b, e.ActionID, Succeeded); err != nil {
		t.Fatal(err)
	}
	if err := j.withData(func(data *journalData) (bool, error) { return true, j.archiveTerminal(data) }); err != nil {
		t.Fatal(err)
	}
	key := scopeKey(b, e.ActionID)
	if err := os.WriteFile(j.receiptPath(key), []byte(`{"status":"succeeded","status":"failed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, started, err := j.Begin(b, e); err != ErrJournal || started {
		t.Fatal("corrupt receipt allowed replay")
	}
}

// The receipts fit the router's flash: the budget is 2 MB, not more than the
// free overlay, and a receipt older than ReceiptMaxAge is removed when the
// archive is next written — a newer one, and anything that is no receipt,
// stays.
func TestJournalReceiptsAgeOutAndFitTheOverlay(t *testing.T) {
	if ArchiveBudgetBytes > 2*1024*1024 {
		t.Fatalf("archive budget %d bytes is more than the router's free flash takes", ArchiveBudgetBytes)
	}
	j, err := OpenJournal(filepath.Join(t.TempDir(), "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{RouterID: "r1", OwnerRef: "owner-1"}
	sum := sha256.Sum256([]byte(`{}`))
	fill := func(prefix string) {
		t.Helper()
		if err := j.withData(func(data *journalData) (bool, error) {
			for i := 0; i < 3; i++ {
				id := fmt.Sprintf("%s-%d", prefix, i)
				data.Records[scopeKey(b, id)] = Record{ActionID: id, Action: "reboot", Status: Succeeded, ParamDigest: hex.EncodeToString(sum[:])}
			}
			return true, j.archiveTerminal(data)
		}); err != nil {
			t.Fatal(err)
		}
	}
	fill("old")
	old := scopeKey(b, "old-0")
	if _, ok, err := j.readReceipt(old); err != nil || !ok {
		t.Fatalf("receipt not archived: %v", err)
	}
	stray := filepath.Join(filepath.Dir(j.receiptPath(old)), ".connect-actions-1.tmp")
	if err := os.WriteFile(stray, nil, 0600); err != nil {
		t.Fatal(err)
	}
	long := time.Now().Add(-ReceiptMaxAge - time.Hour)
	for i := 0; i < 3; i++ {
		if err := os.Chtimes(j.receiptPath(scopeKey(b, fmt.Sprintf("old-%d", i))), long, long); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(stray, long, long); err != nil {
		t.Fatal(err)
	}
	fill("new")
	for i := 0; i < 3; i++ {
		if _, ok, err := j.readReceipt(scopeKey(b, fmt.Sprintf("old-%d", i))); err != nil || ok {
			t.Fatalf("a receipt older than %s stayed (%v)", ReceiptMaxAge, err)
		}
		if _, ok, err := j.readReceipt(scopeKey(b, fmt.Sprintf("new-%d", i))); err != nil || !ok {
			t.Fatalf("a new receipt went (%v)", err)
		}
	}
	if _, err := os.Stat(stray); err != nil {
		t.Fatal("a file that is no receipt was removed")
	}
}

// A full receipt budget makes room by the oldest receipts: the owner's next
// action is never refused for it, and the newest receipts stay.
func TestJournalFullBudgetEvictsTheOldestReceipts(t *testing.T) {
	j, err := OpenJournal(filepath.Join(t.TempDir(), "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{RouterID: "r1", OwnerRef: "owner-1"}
	sum := sha256.Sum256([]byte(`{}`))
	archive := func(ids ...string) error {
		return j.withData(func(data *journalData) (bool, error) {
			for _, id := range ids {
				data.Records[scopeKey(b, id)] = Record{ActionID: id, Action: "reboot", Status: Succeeded, ParamDigest: hex.EncodeToString(sum[:])}
			}
			return true, j.archiveTerminal(data)
		})
	}
	var old []string
	for i := 0; i < 6; i++ {
		old = append(old, fmt.Sprintf("old-%d", i))
	}
	if err := archive(old...); err != nil {
		t.Fatal(err)
	}
	// Oldest first: old-0 a week ago, old-5 a day ago.
	for i, id := range old {
		at := time.Now().Add(-time.Duration(7-i) * 24 * time.Hour)
		if err := os.Chtimes(j.receiptPath(scopeKey(b, id)), at, at); err != nil {
			t.Fatal(err)
		}
	}
	used, err := archiveUsage(j.path + ".receipts")
	if err != nil {
		t.Fatal(err)
	}
	j.archiveBudget = used + 2*receiptMaxBytes // room for one more receipt
	if err := archive("new-0", "new-1"); err != nil {
		t.Fatalf("a full budget refused: %v", err)
	}
	for _, id := range []string{"new-0", "new-1", "old-2", "old-3", "old-4", "old-5"} {
		if _, ok, err := j.readReceipt(scopeKey(b, id)); err != nil || !ok {
			t.Fatalf("receipt %s went (%v)", id, err)
		}
	}
	for _, id := range []string{"old-0", "old-1"} {
		if _, ok, _ := j.readReceipt(scopeKey(b, id)); ok {
			t.Fatalf("the oldest receipt %s stayed", id)
		}
	}
}
