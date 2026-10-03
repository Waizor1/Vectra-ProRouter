package connectactions

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const receiptMaxBytes = 4096

// Receipts are immutable terminal metadata, including interrupted actions which
// must never execute again automatically. Hash-sharded names keep lookups direct
// and preserve old-owner history without loading a lifetime-sized map into RAM.
func (j *Journal) receiptPath(key string) string {
	return filepath.Join(j.path+".receipts", key[:2], key+".json")
}
func (j *Journal) lookup(data *journalData, key string) (Record, bool, error) {
	receipt, archived, err := j.readReceipt(key)
	if err != nil {
		return Record{}, false, err
	}
	active, present := data.Records[key]
	if present && archived && active != receipt {
		return Record{}, false, ErrJournal
	}
	if present {
		return active, true, nil
	}
	return receipt, archived, nil
}
func privateDir(path string, create bool) error {
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && create {
		if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return ErrJournal
		}
		st, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode().Perm()&0077 != 0 {
		return ErrJournal
	}
	return nil
}
func (j *Journal) readReceipt(key string) (Record, bool, error) {
	root := j.path + ".receipts"
	if err := privateDir(root, false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Record{}, false, nil
		}
		return Record{}, false, ErrJournal
	}
	dir := filepath.Join(root, key[:2])
	if err := privateDir(dir, false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Record{}, false, nil
		}
		return Record{}, false, ErrJournal
	}
	f, err := os.OpenFile(j.receiptPath(key), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, ErrJournal
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > receiptMaxBytes {
		return Record{}, false, ErrJournal
	}
	raw, err := io.ReadAll(io.LimitReader(f, receiptMaxBytes+1))
	if err != nil || len(raw) > receiptMaxBytes || !uniqueJSON(raw) {
		return Record{}, false, ErrJournal
	}
	var rec Record
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&rec) != nil || dec.Decode(&struct{}{}) != io.EOF || !validRecord(key, rec) || rec.Status == Started {
		return Record{}, false, ErrJournal
	}
	return rec, true, nil
}
func (j *Journal) archiveTerminal(data *journalData) error {
	root := j.path + ".receipts"
	if err := privateDir(root, true); err != nil {
		return ErrJournal
	}
	// Persist the archive root before removing anything from the active map.
	if err := syncDirectory(filepath.Dir(root)); err != nil {
		return err
	}
	if err := ageOutReceipts(root, time.Now().Add(-ReceiptMaxAge)); err != nil {
		return err
	}
	used, err := archiveUsage(root)
	if err != nil {
		return err
	}
	// Oldest first, for when the budget is reached: a full budget makes room
	// by the oldest receipts, never by refusing the owner's next action.
	evictable, err := listReceipts(root)
	if err != nil {
		return err
	}
	budget := j.archiveBudget
	if budget <= 0 {
		budget = ArchiveBudgetBytes
	}
	keys := []string{}
	for key, rec := range data.Records {
		if rec.Status != Started {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	// Each batch frees substantial active capacity; receipts survive
	// ReceiptMaxAge (ageOutReceipts), or until the budget needs their room.
	if len(keys) > 128 {
		keys = keys[:128]
	}
	for _, key := range keys {
		rec := data.Records[key]
		prior, exists, err := j.readReceipt(key)
		if err != nil {
			return err
		}
		if exists {
			if prior != rec {
				return ErrJournal
			}
			delete(data.Records, key)
			continue
		}
		shard := filepath.Join(root, key[:2])
		newShard := false
		if err := privateDir(shard, false); errors.Is(err, os.ErrNotExist) {
			newShard = true
		} else if err != nil {
			return ErrJournal
		}
		// Reserve the receipt block plus a directory block (new shard or
		// growth of an existing shard). This is intentionally conservative.
		reserve := int64(2 * receiptMaxBytes)
		for used > budget-reserve {
			if len(evictable) == 0 {
				// Nothing of the receipts' own left to make room with.
				return ErrJournalFull
			}
			if err := removeReceipt(evictable[0]); err != nil {
				return err
			}
			used -= evictable[0].charge
			evictable = evictable[1:]
		}
		if newShard {
			if err := privateDir(shard, true); err != nil {
				return ErrJournal
			}
			if err := syncDirectory(root); err != nil {
				return err
			}
		}
		if err := writeReceipt(j.receiptPath(key), rec); err != nil {
			return err
		}
		used += reserve
		// writeReceipt fsyncs metadata and its directory first. A crash before the
		// active map is saved leaves two identical copies; lookup verifies equality.
		delete(data.Records, key)
	}
	return nil
}

// receiptFile is one receipt on flash, as the budget charges it.
type receiptFile struct {
	path   string
	mod    time.Time
	charge int64
}

// listReceipts are the receipts under root, oldest first: files named
// <sha256 hex>.json in a shard directory named by their first two letters,
// and nothing else — a crash-left temp file or anything unknown is never
// listed, so never removed (archiveUsage charges it).
func listReceipts(root string) ([]receiptFile, error) {
	shards, err := os.ReadDir(root)
	if err != nil {
		return nil, ErrJournal
	}
	var out []receiptFile
	for _, sh := range shards {
		if !sh.IsDir() || len(sh.Name()) != 2 {
			continue
		}
		dir := filepath.Join(root, sh.Name())
		if privateDir(dir, false) != nil {
			return nil, ErrJournal
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, ErrJournal
		}
		for _, e := range entries {
			key, ok := strings.CutSuffix(e.Name(), ".json")
			if !ok || len(key) != 64 || !strings.HasPrefix(key, sh.Name()) || !e.Type().IsRegular() {
				continue
			}
			if _, err := hex.DecodeString(key); err != nil {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, receiptFile{path: filepath.Join(dir, e.Name()), mod: info.ModTime(), charge: fileCharge(info)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].mod.Equal(out[j].mod) {
			return out[i].mod.Before(out[j].mod)
		}
		return out[i].path < out[j].path
	})
	return out, nil
}

// removeReceipt removes one listed receipt and syncs its shard.
func removeReceipt(r receiptFile) error {
	if err := os.Remove(r.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrJournal
	}
	return syncDirectory(filepath.Dir(r.path))
}

// ageOutReceipts removes receipts last written before cutoff.
func ageOutReceipts(root string, cutoff time.Time) error {
	receipts, err := listReceipts(root)
	if err != nil {
		return err
	}
	for _, r := range receipts {
		if !r.mod.Before(cutoff) {
			break
		}
		if err := removeReceipt(r); err != nil {
			return err
		}
	}
	return nil
}

// fileCharge is what a file costs the budget: its blocks, at least a
// receipt's maximum, rounded up to whole receipts by size.
func fileCharge(st fs.FileInfo) int64 {
	charge := int64(receiptMaxBytes)
	if stat, ok := st.Sys().(*syscall.Stat_t); ok && stat.Blocks*512 > charge {
		charge = stat.Blocks * 512
	}
	if st.Size() > charge {
		charge = ((st.Size() + receiptMaxBytes - 1) / receiptMaxBytes) * receiptMaxBytes
	}
	return charge
}

func archiveUsage(root string) (int64, error) {
	var used int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return ErrJournal
		}
		st, err := d.Info()
		if err != nil {
			return ErrJournal
		}
		if st.Mode().Perm()&0077 != 0 {
			return ErrJournal
		}
		if d.IsDir() {
			if path != root {
				rel, err := filepath.Rel(root, path)
				if err != nil || strings.Contains(rel, string(filepath.Separator)) {
					return ErrJournal
				}
				decoded, err := hex.DecodeString(rel)
				if err != nil || len(decoded) != 1 || hex.EncodeToString(decoded) != rel {
					return ErrJournal
				}
			}
			charge := int64(receiptMaxBytes)
			if stat, ok := st.Sys().(*syscall.Stat_t); ok && stat.Blocks*512 > charge {
				charge = stat.Blocks * 512
			}
			used += charge
			return nil
		}
		if !st.Mode().IsRegular() {
			return ErrJournal
		}
		// Include crash-left temp files in the budget. Never remove unrelated files.
		used += fileCharge(st)
		return nil
	})
	if err != nil {
		return 0, ErrJournal
	}
	return used, nil
}
func writeReceipt(path string, rec Record) error {
	raw, err := json.Marshal(rec)
	if err != nil || len(raw) > receiptMaxBytes {
		return ErrJournal
	}
	dir := filepath.Dir(path)
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
	if os.Rename(tmp.Name(), path) != nil {
		return ErrJournal
	}
	return syncDirectory(dir)
}
func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return ErrJournal
	}
	defer dir.Close()
	if dir.Sync() != nil {
		return ErrJournal
	}
	return nil
}
