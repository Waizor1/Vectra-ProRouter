package connectactions

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"
)

var ErrJournal = errors.New("connect action journal unavailable")
var ErrTransition = errors.New("connect action journal transition rejected")
var ErrReplayConflict = errors.New("connect action replay conflict")
var ErrSensitiveReplay = errors.New("sensitive connect action replay suppressed")
var ErrJournalFull = errors.New("connect action journal storage budget exhausted")

const MaxRecords = 1024
const maxJournalBytes = 1024 * 1024

// ArchiveBudgetBytes limits replay receipt storage, not lifetime action count.
// It was 64 MB — more than the whole free overlay of an AX3000T (~18 MB):
// receipts could have filled the router's flash. 2 MB is ~250 receipts at
// their charged size, past what an owner sends in ReceiptMaxAge.
const ArchiveBudgetBytes int64 = 2 * 1024 * 1024

// ReceiptMaxAge: a receipt older than this is removed when the archive is
// written. A receipt stops Connect's retry of an action from running it
// twice; Connect retries for minutes, not months, and an action ID it sent
// 90 days ago is not sent again. Kept for good, receipts would fill the
// budget and stop every new action (ErrJournalFull).
const ReceiptMaxAge = 90 * 24 * time.Hour

type Status string

const (
	Started     Status = "started"
	Interrupted Status = "interrupted"
	Succeeded   Status = "succeeded"
	Failed      Status = "failed"
)

// Record contains only metadata. ParamDigest is empty for actions with secrets.
// A digest of low-entropy WiFi secrets would itself disclose sensitive data.
type Record struct {
	ActionID    string `json:"actionId"`
	Action      string `json:"action"`
	Status      Status `json:"status"`
	ParamDigest string `json:"paramDigest,omitempty"`
}
type journalData struct {
	Version int               `json:"version"`
	Records map[string]Record `json:"records"`
}
type Journal struct {
	path          string
	archiveBudget int64
}

// OpenJournal validates existing durable state and fails closed on corruption.
// A regular sidecar flock serializes processes; OS release after a crash makes
// the next daemon able to read an interrupted action without stale lock removal.
func OpenJournal(path string) (*Journal, error) {
	if path == "" {
		return nil, ErrJournal
	}
	j := &Journal{path: path}
	if err := j.withData(func(*journalData) (bool, error) { return false, nil }); err != nil {
		return nil, err
	}
	return j, nil
}

// Begin persists started before a device mutation may run. started=false always
// forbids execution. An uncompleted replay becomes interrupted, never retried
// automatically: a reboot or WiFi change may already have taken effect. A WiFi
// replay has no parameter fingerprint and returns ErrSensitiveReplay regardless
// of supplied parameters. Its Record can still be used for a safe status reply.
func (j *Journal) Begin(b Binding, e Envelope) (Record, bool, error) {
	if err := Authorize(e, b, b.RouterID); err != nil {
		return Record{}, false, err
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return Record{}, false, ErrInvalidPayload
	}
	canonical, err := Parse(raw)
	if err != nil {
		return Record{}, false, err
	}
	var digest string
	if canonical.Action != "set_wifi" {
		params, err := json.Marshal(canonical.Params)
		if err != nil {
			return Record{}, false, ErrInvalidPayload
		}
		sum := sha256.Sum256(params)
		digest = hex.EncodeToString(sum[:])
	}
	key := scopeKey(b, canonical.ActionID)
	var rec Record
	var started bool
	var replayErr error
	err = j.withData(func(data *journalData) (bool, error) {
		existing, ok, lookupErr := j.lookup(data, key)
		if lookupErr != nil {
			return false, lookupErr
		}
		if ok {
			rec = existing
			if rec.Action != canonical.Action || (rec.Action != "set_wifi" && rec.ParamDigest != digest) {
				return false, ErrReplayConflict
			}
			changed := false
			if rec.Status == Started {
				rec.Status = Interrupted
				data.Records[key] = rec
				changed = true
			}
			if rec.Action == "set_wifi" {
				replayErr = ErrSensitiveReplay
			}
			return changed, nil
		}
		if len(data.Records) >= MaxRecords {
			if err := j.archiveTerminal(data); err != nil {
				return false, err
			}
			if len(data.Records) >= MaxRecords {
				return false, ErrJournalFull
			}
		}
		rec = Record{ActionID: canonical.ActionID, Action: canonical.Action, Status: Started, ParamDigest: digest}
		data.Records[key] = rec
		started = true
		return true, nil
	})
	if err != nil {
		return Record{}, false, err
	}
	return rec, started, replayErr
}

// Complete accepts only fixed terminal status values. It never accepts caller
// stdout, exception text or result payloads, keeping secrets out of the journal.
func (j *Journal) Complete(b Binding, actionID string, status Status) error {
	if !validReference(b.RouterID) || !validReference(b.OwnerRef) || !idPattern.MatchString(actionID) || (status != Succeeded && status != Failed) {
		return ErrTransition
	}
	return j.withData(func(data *journalData) (bool, error) {
		key := scopeKey(b, actionID)
		rec, ok, lookupErr := j.lookup(data, key)
		if lookupErr != nil {
			return false, lookupErr
		}
		if !ok {
			return false, ErrTransition
		}
		if rec.Status == status {
			return false, nil
		}
		if rec.Status != Started {
			return false, ErrTransition
		}
		rec.Status = status
		data.Records[key] = rec
		return true, nil
	})
}

// Recover is called once after daemon startup, before processing jobs. It marks
// unfinished entries as interrupted. Do not call while an execution is running.
// Only records within the current authenticated owner/router scope are returned.
func (j *Journal) Recover(b Binding) ([]Record, error) {
	if !validReference(b.RouterID) || !validReference(b.OwnerRef) {
		return nil, ErrUnauthorized
	}
	var records []Record
	err := j.withData(func(data *journalData) (bool, error) {
		changed := false
		for key, rec := range data.Records {
			if key != scopeKey(b, rec.ActionID) {
				continue
			}
			if rec.Status == Started {
				rec.Status = Interrupted
				data.Records[key] = rec
				changed = true
			}
			if rec.Status == Interrupted {
				records = append(records, rec)
			}
		}
		return changed, nil
	})
	sort.Slice(records, func(i, k int) bool { return records[i].ActionID < records[k].ActionID })
	return records, err
}
func scopeKey(b Binding, actionID string) string {
	sum := sha256.Sum256([]byte(b.RouterID + "\x00" + b.OwnerRef + "\x00" + actionID))
	return hex.EncodeToString(sum[:])
}
func (j *Journal) withData(fn func(*journalData) (bool, error)) error {
	if j == nil || j.path == "" {
		return ErrJournal
	}
	dir := filepath.Dir(j.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return ErrJournal
	}
	lock, err := os.OpenFile(j.path+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return ErrJournal
	}
	defer lock.Close()
	st, err := lock.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return ErrJournal
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return ErrJournal
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	data, err := j.read()
	if err != nil {
		return err
	}
	changed, err := fn(&data)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return j.write(data)
}
func (j *Journal) read() (journalData, error) {
	data := journalData{Version: 1, Records: map[string]Record{}}
	f, err := os.OpenFile(j.path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return data, nil
	}
	if err != nil {
		return journalData{}, ErrJournal
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > maxJournalBytes {
		return journalData{}, ErrJournal
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxJournalBytes+1))
	if err != nil || len(raw) > maxJournalBytes {
		return journalData{}, ErrJournal
	}
	if !uniqueJSON(raw) {
		return journalData{}, ErrJournal
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&data) != nil || dec.Decode(&struct{}{}) != io.EOF || data.Version != 1 || data.Records == nil || len(data.Records) > MaxRecords {
		return journalData{}, ErrJournal
	}
	for key, rec := range data.Records {
		if !validRecord(key, rec) {
			return journalData{}, ErrJournal
		}
	}
	return data, nil
}
func (j *Journal) write(data journalData) error {
	raw, err := json.Marshal(data)
	if err != nil || len(raw) > maxJournalBytes {
		return ErrJournal
	}
	dir := filepath.Dir(j.path)
	tmp, err := os.CreateTemp(dir, ".connect-actions-*.tmp")
	if err != nil {
		return ErrJournal
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if tmp.Chmod(0600) != nil {
		return ErrJournal
	}
	if _, err := tmp.Write(raw); err != nil {
		return ErrJournal
	}
	if tmp.Sync() != nil || tmp.Close() != nil {
		return ErrJournal
	}
	if os.Rename(tmp.Name(), j.path) != nil {
		return ErrJournal
	}
	d, err := os.Open(dir)
	if err != nil {
		return ErrJournal
	}
	defer d.Close()
	if d.Sync() != nil {
		return ErrJournal
	}
	return nil
}

// uniqueJSON rejects ambiguous object keys before decoding persisted state.
func uniqueJSON(raw []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 16 {
			return false
		}
		token, err := dec.Token()
		if err != nil {
			return false
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return true
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				key, err := dec.Token()
				if err != nil {
					return false
				}
				name, ok := key.(string)
				if !ok || seen[name] || len(seen) > MaxRecords {
					return false
				}
				seen[name] = true
				if !value(depth + 1) {
					return false
				}
			}
			end, err := dec.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			count := 0
			for dec.More() {
				count++
				if count > MaxRecords || !value(depth+1) {
					return false
				}
			}
			end, err := dec.Token()
			return err == nil && end == json.Delim(']')
		default:
			return false
		}
	}
	if !value(0) {
		return false
	}
	_, err := dec.Token()
	return err == io.EOF
}

func validRecord(key string, rec Record) bool {
	keyBytes, err := hex.DecodeString(key)
	if err != nil || len(keyBytes) != 32 || hex.EncodeToString(keyBytes) != key || !idPattern.MatchString(rec.ActionID) {
		return false
	}
	known := false
	for _, action := range Capabilities() {
		if rec.Action == action {
			known = true
		}
	}
	if !known || (rec.Status != Started && rec.Status != Interrupted && rec.Status != Succeeded && rec.Status != Failed) {
		return false
	}
	if rec.Action == "set_wifi" {
		return rec.ParamDigest == ""
	}
	digest, err := hex.DecodeString(rec.ParamDigest)
	return err == nil && len(digest) == 32 && hex.EncodeToString(digest) == rec.ParamDigest
}
