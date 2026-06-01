package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Load reads a config from disk, validates it, and applies defaults.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config %s: %w", path, err)
	}
	defer f.Close()
	return Read(f, path)
}

// Read parses a config from any io.Reader.
func Read(r io.Reader, source string) (*Config, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", source, err)
	}
	c, err := Unmarshal(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", source, err)
	}
	ApplyDefaults(c)
	if verr := Validate(c); verr != nil {
		return c, fmt.Errorf("validate %s: %w", source, verr)
	}
	return c, nil
}

// Unmarshal parses JSON bytes into a Config without applying defaults.
// Unknown fields are rejected to catch operator typos early.
func Unmarshal(data []byte) (*Config, error) {
	var c Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, err
	}
	if c.Schema == 0 {
		c.Schema = SchemaVersion
	}
	if c.Schema != SchemaVersion {
		return &c, fmt.Errorf("config schema %d not supported (this build understands %d)",
			c.Schema, SchemaVersion)
	}
	return &c, nil
}

// Marshal renders a Config as indented JSON.
func Marshal(c *Config) ([]byte, error) {
	return json.MarshalIndent(c, "", "  ")
}

// Save atomically writes a Config to disk: .tmp + fsync + rename + dir-fsync.
// Best-effort dir fsync — works on Linux/macOS, silently skipped where unsupported.
func Save(path string, c *Config) error {
	data, err := Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open tmp: %w", err)
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("fsync tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// Clone returns a deep copy of c via JSON round-trip.
// Slow but exact — used in tests and the normalization pipeline where we
// want to compare "before normalization" to "after" without aliasing.
func Clone(c *Config) (*Config, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	var dup Config
	if err := json.Unmarshal(data, &dup); err != nil {
		return nil, err
	}
	return &dup, nil
}

// ErrInvalid is the base error returned by Validate.
var ErrInvalid = errors.New("invalid config")
