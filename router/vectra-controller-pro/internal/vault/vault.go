// Package vault encrypts controller secrets at rest. The local key is readable
// by root: this prevents casual file copying, not extraction by a live root owner.
package vault

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

const magic = "VCTLVAULT1\n"

var ErrInvalid = errors.New("vault: invalid or unauthenticated envelope")
var ErrDowngrade = errors.New("vault: plaintext downgrade refused")
var mu sync.Mutex

func paths(path string) (target, dir, marker, stage string, err error) {
	target, err = filepath.Abs(filepath.Clean(path))
	if err != nil {
		return
	}
	dir = filepath.Join(filepath.Dir(target), ".vault")
	h := sha256.Sum256([]byte(target))
	name := hex.EncodeToString(h[:])
	marker, stage = filepath.Join(dir, name+".sealed"), filepath.Join(dir, name+".pending")
	return
}

// KeyPath returns the external local key location for a secret's storage
// directory. Default OpenWrt directories use independent sibling key roots so
// ordinary whole-configuration-directory copies do not include the key.
// Alternate storage directories use a sibling; archives of their common parent
// must explicitly exclude that sibling to preserve the same property.
func KeyPath(path string) (string, error) {
	target, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	storage := filepath.Dir(target)
	for _, base := range []string{"/etc/vectra-controller-pro", "/var/run/vectra-controller-pro"} {
		if storage == base || strings.HasPrefix(storage, base+string(filepath.Separator)) {
			hash := sha256.Sum256([]byte(storage))
			return filepath.Join(base+"-vault-keys", hex.EncodeToString(hash[:]), "key"), nil
		}
	}
	return filepath.Join(storage+".vault-keys", "key"), nil
}

func checkPrivate(path string, directory bool) error {
	s, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if s.Mode()&os.ModeSymlink != 0 || s.IsDir() != directory || (!directory && !s.Mode().IsRegular()) || s.Mode().Perm()&0o077 != 0 {
		return errors.New("vault: unsafe key storage permissions or type")
	}
	return nil
}

func key(dir string, create bool) ([]byte, error) {
	// The .vault metadata directory and the external key directory have separate
	// backup/export boundaries. Never silently fall back to an adjacent key.
	p, err := KeyPath(filepath.Join(filepath.Dir(dir), "secret"))
	if err != nil {
		return nil, err
	}
	keyDir := filepath.Dir(p)
	keyRoot := keyDir
	if parent := filepath.Dir(keyDir); parent == "/etc/vectra-controller-pro-vault-keys" || parent == "/var/run/vectra-controller-pro-vault-keys" {
		keyRoot = parent
	}
	if create {
		if err = os.MkdirAll(keyDir, 0o700); err != nil {
			return nil, err
		}
	}
	if err = checkPrivate(keyRoot, true); err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("vault: local key unavailable; explicit recovery required")
		}
		return nil, err
	}
	if err = checkPrivate(keyDir, true); err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("vault: local key unavailable; explicit recovery required")
		}
		return nil, err
	}
	if err = checkPrivate(p, false); err != nil {
		if !create || !os.IsNotExist(err) {
			if os.IsNotExist(err) {
				return nil, errors.New("vault: local key unavailable; explicit recovery required")
			}
			return nil, err
		}
		// One key serves this storage directory. A new path must not reset a lost
		// key while another sealed file or tombstone depends on it.
		entries, e := os.ReadDir(dir)
		if e != nil {
			return nil, e
		}
		for _, entry := range entries {
			if filepath.Ext(entry.Name()) == ".sealed" || filepath.Ext(entry.Name()) == ".pending" {
				return nil, errors.New("vault: local key missing; explicit recovery required")
			}
		}
		b := make([]byte, 32)
		defer clear(b)
		if _, err = rand.Read(b); err != nil {
			return nil, err
		}
		f, err := os.CreateTemp(keyDir, ".key-")
		if err != nil {
			return nil, err
		}
		tmp := f.Name()
		defer os.Remove(tmp)
		if _, err = f.Write(b); err == nil {
			err = f.Sync()
		}
		ce := f.Close()
		if err == nil {
			err = ce
		}
		if err != nil {
			return nil, err
		}
		if err = os.Link(tmp, p); err != nil && !os.IsExist(err) {
			return nil, err
		}
		if err = syncDir(keyDir); err != nil {
			return nil, err
		}
		if err = syncDir(filepath.Dir(keyDir)); err != nil {
			return nil, err
		}
		if keyRoot != keyDir {
			if err = syncDir(filepath.Dir(keyRoot)); err != nil {
				return nil, err
			}
		}
		if err = checkPrivate(p, false); err != nil {
			return nil, err
		}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	if len(b) != 32 {
		return nil, errors.New("vault: invalid local key")
	}
	return b, nil
}

func aead(dir string, create bool) (cipher.AEAD, error) {
	k, err := key(dir, create)
	if err != nil {
		return nil, err
	}
	defer clear(k)
	b, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}
func open(target, dir string, raw []byte) ([]byte, error) {
	if !bytes.HasPrefix(raw, []byte(magic)) {
		return nil, ErrInvalid
	}
	a, err := aead(dir, false)
	if err != nil {
		return nil, err
	}
	raw = raw[len(magic):]
	if len(raw) < a.NonceSize()+a.Overhead() {
		return nil, ErrInvalid
	}
	out, err := a.Open(nil, raw[:a.NonceSize()], raw[a.NonceSize():], []byte(magic+target))
	if err != nil {
		return nil, ErrInvalid
	}
	return out, nil
}

// ReadFile accepts encrypted files only. It resumes a staged durable migration
// after interruption, before exposing bytes to the caller.
func ReadFile(path string) ([]byte, error) {
	mu.Lock()
	defer mu.Unlock()
	unlock, err := fileLock(path, false)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return read(path)
}
func read(path string) ([]byte, error) {
	target, dir, marker, stage, err := paths(path)
	if err != nil {
		return nil, err
	}
	if status, e := os.ReadFile(marker); e == nil {
		if string(status) == "deleted-v1\n" {
			for _, p := range []string{target, stage} {
				if e = os.Remove(p); e != nil && !os.IsNotExist(e) {
					return nil, e
				}
			}
			return nil, &os.PathError{Op: "read", Path: target, Err: os.ErrNotExist}
		}
		if string(status) != "sealed-v1\n" {
			return nil, ErrInvalid
		}
		if raw, e := os.ReadFile(stage); e == nil {
			plain, e := open(target, dir, raw)
			if e != nil {
				return nil, e
			}
			clear(plain)
			if e = os.Rename(stage, target); e != nil {
				return nil, e
			}
			if e = syncDir(filepath.Dir(target)); e != nil {
				return nil, e
			}
		} else if !os.IsNotExist(e) {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			if _, e := os.Stat(marker); e == nil {
				return nil, errors.New("vault: sealed file missing; explicit recovery required")
			}
		}
		return nil, err
	}
	if !bytes.HasPrefix(raw, []byte(magic)) {
		if _, e := os.Stat(marker); e == nil {
			return nil, ErrDowngrade
		}
	}
	return open(target, dir, raw)
}

// WriteFile uses AES-256-GCM with a fresh nonce and authenticates the absolute
// destination path and format version. No plaintext temporary file is created.
func WriteFile(path string, data []byte) error {
	mu.Lock()
	defer mu.Unlock()
	unlock, err := fileLock(path, true)
	if err != nil {
		return err
	}
	defer unlock()
	return write(path, data)
}

// RemoveFile durably retires a secret with a tombstone before unlinking it.
// Interrupted release cannot expose the previous owner's ciphertext, and a
// later legacy plaintext write cannot reopen migration. The local key remains.
func RemoveFile(path string) error {
	mu.Lock()
	defer mu.Unlock()
	unlock, err := fileLock(path, true)
	if err != nil {
		return err
	}
	defer unlock()
	target, dir, marker, stage, err := paths(path)
	if err != nil {
		return err
	}
	localKey, err := key(dir, true)
	if err != nil {
		return err
	}
	clear(localKey)
	if err = atomic(marker, []byte("deleted-v1\n")); err != nil {
		return err
	}
	for _, p := range []string{target, stage} {
		if err = os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err = syncDir(filepath.Dir(target)); err != nil {
		return err
	}
	return syncDir(dir)
}

func write(path string, data []byte) error {
	target, dir, marker, stage, err := paths(path)
	if err != nil {
		return err
	}
	// Never regenerate a lost key for an existing encrypted file or marker.
	create := true
	if _, e := os.Stat(marker); e == nil {
		create = false
	} else if !os.IsNotExist(e) {
		return e
	}
	if raw, e := os.ReadFile(target); e == nil && bytes.HasPrefix(raw, []byte("VCTLVAULT")) {
		create = false
	}
	a, err := aead(dir, create)
	if err != nil {
		return err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	raw := append([]byte(magic), nonce...)
	raw = a.Seal(raw, nonce, data, []byte(magic+target))
	if err = atomic(stage, raw); err != nil {
		return err
	}
	if err = atomic(marker, []byte("sealed-v1\n")); err != nil {
		return err
	}
	if err = os.Rename(stage, target); err != nil {
		return err
	}
	return syncDir(filepath.Dir(target))
}

// MigrateFile explicitly validates a legacy plaintext file, then atomically
// seals it. Once sealed, plaintext at this path is rejected even if valid JSON.
// A missing file is a no-op. Call at startup before normal secret reads.
func MigrateFile(path string, validate func([]byte) error) error {
	mu.Lock()
	defer mu.Unlock()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	unlock, err := fileLock(path, true)
	if err != nil {
		return err
	}
	defer unlock()
	target, _, marker, _, err := paths(path)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer clear(raw)
	if _, err = os.Stat(marker); err == nil {
		plain, e := read(path)
		clear(plain)
		err = e
		if os.IsNotExist(err) {
			return nil
		}
		return err
	} else if !os.IsNotExist(err) {
		return err
	}
	if bytes.HasPrefix(raw, []byte("VCTLVAULT")) {
		plain, e := read(path)
		clear(plain)
		err = e
		if err != nil {
			return err
		}
		return atomic(marker, []byte("sealed-v1\n"))
	}
	if validate == nil {
		return errors.New("vault: migration requires validation")
	}
	if err = validate(raw); err != nil {
		return errors.New("vault: legacy validation failed")
	}
	return write(path, raw)
}

func atomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".vault-tmp-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}
func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = f.Sync(); err != nil {
		return fmt.Errorf("vault: sync directory: %w", err)
	}
	return nil
}

// Serialize independent controller and CLI processes, including stage recovery.
func fileLock(path string, create bool) (func(), error) {
	_, dir, _, _, err := paths(path)
	if err != nil {
		return nil, err
	}
	if create {
		if err = os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	if err = checkPrivate(dir, true); err != nil {
		if !create && os.IsNotExist(err) {
			return func() {}, nil
		}
		return nil, err
	}
	p := filepath.Join(dir, "lock")
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if err = checkPrivate(p, false); err != nil {
		f.Close()
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}

// RefusePlaintextWrite prevents ordinary export/write helpers from silently
// replacing a managed encrypted file. Root may still modify files directly.
func RefusePlaintextWrite(path string) error {
	_, _, marker, stage, err := paths(path)
	if err != nil {
		return err
	}
	for _, p := range []string{marker, stage} {
		if _, e := os.Lstat(p); e == nil {
			return ErrDowngrade
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if bytes.HasPrefix(raw, []byte("VCTLVAULT")) {
		return ErrDowngrade
	}
	return nil
}

// MigrateArtifact seals a crash artifact — a temp file, an old reader's backup
// — as MigrateFile seals legacy plaintext, without validating it, with one
// difference: a byte copy of a sealed file is removed. An old reader that
// cannot parse a sealed file (vectra-reporter 1.0.0-r2, the legacy agent)
// keeps one as <name>.corrupt-<time>: authenticated to the original path, it
// never opens under its own name and reveals nothing; left in place it
// refused every start of the daemon (1111, 2026-10-02). Only a proven copy —
// one that opens under the name it was copied from — goes; an artifact that
// opens under neither name (a replaced key, a stray) stays where it is and
// does not refuse the start. Never call it on a primary secret.
func MigrateArtifact(path string) error {
	err := MigrateFile(path, func([]byte) error { return nil })
	if !errors.Is(err, ErrInvalid) {
		return err
	}
	origin := artifactOrigin(path)
	if origin == "" {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()
	unlock, e := fileLock(path, true)
	if e != nil {
		return e
	}
	defer unlock()
	raw, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	from, fromDir, _, _, e := paths(origin)
	if e != nil {
		clear(raw)
		return e
	}
	plain, e := open(from, fromDir, raw)
	clear(raw)
	if e != nil {
		return nil
	}
	clear(plain)
	target, dir, marker, stage, e := paths(path)
	if e != nil {
		return e
	}
	for _, p := range []string{target, marker, stage} {
		if e = os.Remove(p); e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	if e = syncDir(filepath.Dir(target)); e != nil {
		return e
	}
	return syncDir(dir)
}

// artifactOrigin names the file an artifact was copied from: <name> for
// <name>.corrupt-<time> and <name>.tmp, nothing for any other artifact.
func artifactOrigin(path string) string {
	base := filepath.Base(path)
	if i := strings.LastIndex(base, ".corrupt-"); i > 0 {
		return filepath.Join(filepath.Dir(path), base[:i])
	}
	if o := strings.TrimSuffix(base, ".tmp"); o != base && o != "" && !strings.HasPrefix(o, ".") {
		return filepath.Join(filepath.Dir(path), o)
	}
	return ""
}

// ResealRewritten seals again a file its legacy owner legitimately rewrote as
// plaintext over the sealed copy: the old Vectra agent after a hand-back
// cannot read its sealed state and saves it as plaintext, which every later
// read refused as a downgrade (ErrDowngrade). The plaintext must validate.
// Use it only for a file another program owns; a missing, unmarked or still
// sealed file is left to MigrateFile.
func ResealRewritten(path string, validate func([]byte) error) error {
	mu.Lock()
	defer mu.Unlock()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	unlock, err := fileLock(path, true)
	if err != nil {
		return err
	}
	defer unlock()
	target, _, marker, _, err := paths(path)
	if err != nil {
		return err
	}
	if status, e := os.ReadFile(marker); os.IsNotExist(e) {
		return nil
	} else if e != nil {
		return e
	} else if string(status) != "sealed-v1\n" {
		return nil
	}
	raw, err := os.ReadFile(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer clear(raw)
	if bytes.HasPrefix(raw, []byte("VCTLVAULT")) {
		return nil
	}
	if validate == nil || validate(raw) != nil {
		return errors.New("vault: rewritten legacy file failed validation")
	}
	return write(path, raw)
}

// Unseal writes a sealed file back as plaintext and drops its seal, for a
// reader that cannot open sealed files: the old Vectra agent downgraded to a
// release without the vault read, which would otherwise lose the router's
// identity after a hand-back. A file that is not sealed is left alone.
func Unseal(path string) error {
	mu.Lock()
	defer mu.Unlock()
	unlock, err := fileLock(path, true)
	if err != nil {
		return err
	}
	defer unlock()
	target, _, marker, stage, err := paths(path)
	if err != nil {
		return err
	}
	if _, err = os.Stat(marker); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	plain, err := read(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer clear(plain)
	if err = atomic(target, plain); err != nil {
		return err
	}
	for _, p := range []string{marker, stage} {
		if err = os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return syncDir(filepath.Dir(marker))
}

// Unsealed reports a file that was never sealed: present, without the vault's
// header, and with no seal marker. A reader of a router still on plaintext
// (0.6.0-r36, or rolled back to it) may read such a file as it is.
func Unsealed(path string) bool {
	_, _, marker, _, err := paths(path)
	if err != nil {
		return false
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, len(magic))
	n, _ := f.Read(head)
	return !bytes.HasPrefix(head[:n], []byte("VCTLVAULT"))
}
