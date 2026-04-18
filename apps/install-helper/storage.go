package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	keyringServiceName = "Vectra Install Helper"
	stateFileName      = "helper-state.json"
)

type secretStore interface {
	Get(key string) (string, error)
	Set(key string, value string) error
}

type keyringSecretStore struct{}

func (keyringSecretStore) Get(key string) (string, error) {
	return keyring.Get(keyringServiceName, key)
}

func (keyringSecretStore) Set(key string, value string) error {
	return keyring.Set(keyringServiceName, key, value)
}

type memorySecretStore struct {
	mu      sync.Mutex
	entries map[string]string
}

func newMemorySecretStore() *memorySecretStore {
	return &memorySecretStore{entries: make(map[string]string)}
}

func (s *memorySecretStore) Get(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.entries[key]
	if !ok {
		return "", errors.New("secret not found")
	}
	return value, nil
}

func (s *memorySecretStore) Set(key string, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = value
	return nil
}

type helperStateStore struct {
	mu       sync.Mutex
	dataDir  string
	secrets  secretStore
	profiles []credentialProfile
	hosts    map[string]trustedHostRecord
}

func newHelperStateStore(dataDir string, secrets secretStore) (*helperStateStore, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}

	store := &helperStateStore{
		dataDir:  dataDir,
		secrets:  secrets,
		profiles: make([]credentialProfile, 0),
		hosts:    make(map[string]trustedHostRecord),
	}

	if err := store.load(); err != nil {
		return nil, err
	}

	return store, nil
}

func (s *helperStateStore) statePath() string {
	return filepath.Join(s.dataDir, stateFileName)
}

func (s *helperStateStore) load() error {
	statePath := s.statePath()
	content, err := os.ReadFile(statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	var state helperDiskState
	if err := json.Unmarshal(content, &state); err != nil {
		return err
	}

	s.profiles = state.CredentialProfiles
	if state.TrustedHosts != nil {
		s.hosts = state.TrustedHosts
	}
	return nil
}

func (s *helperStateStore) persist() error {
	state := helperDiskState{
		CredentialProfiles: s.profiles,
		TrustedHosts:       s.hosts,
	}
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.statePath(), content, 0o600)
}

func (s *helperStateStore) listProfiles() []credentialProfile {
	s.mu.Lock()
	defer s.mu.Unlock()

	copyOf := append([]credentialProfile(nil), s.profiles...)
	if copyOf == nil {
		copyOf = make([]credentialProfile, 0)
	}
	sort.Slice(copyOf, func(i, j int) bool {
		left := copyOf[i].LastUsedAt
		right := copyOf[j].LastUsedAt
		switch {
		case left == nil && right == nil:
			return copyOf[i].Label < copyOf[j].Label
		case left == nil:
			return false
		case right == nil:
			return true
		default:
			return left.After(*right)
		}
	})
	return copyOf
}

func (s *helperStateStore) findProfile(id string) *credentialProfile {
	for idx := range s.profiles {
		if s.profiles[idx].ID == id {
			return &s.profiles[idx]
		}
	}
	return nil
}

func (s *helperStateStore) getProfilePassword(id string) (credentialProfile, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	profile := s.findProfile(id)
	if profile == nil {
		return credentialProfile{}, "", fmt.Errorf("credential profile %s was not found", id)
	}
	password, err := s.secrets.Get(id)
	if err != nil {
		return credentialProfile{}, "", err
	}
	return *profile, password, nil
}

func (s *helperStateStore) saveProfile(targetIP string, username string, password string) (credentialProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	label := fmt.Sprintf("%s@%s", username, targetIP)
	existing := s.findProfile(label)
	now := time.Now().UTC()

	var profile credentialProfile
	if existing != nil {
		existing.LastUsedAt = &now
		profile = *existing
	} else {
		profile = credentialProfile{
			ID:         label,
			Label:      label,
			Username:   username,
			LastUsedAt: &now,
		}
		s.profiles = append(s.profiles, profile)
	}

	if err := s.secrets.Set(profile.ID, password); err != nil {
		return credentialProfile{}, err
	}

	if err := s.persist(); err != nil {
		return credentialProfile{}, err
	}

	return profile, nil
}

func (s *helperStateStore) markProfileUsed(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	profile := s.findProfile(id)
	if profile == nil {
		return nil
	}

	now := time.Now().UTC()
	profile.LastUsedAt = &now
	return s.persist()
}

func (s *helperStateStore) trustedFingerprint(targetIP string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.hosts[targetIP]
	if !ok {
		return ""
	}
	return record.Fingerprint
}

func (s *helperStateStore) upsertTrustedHost(targetIP string, fingerprint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	s.hosts[targetIP] = trustedHostRecord{
		Fingerprint: fingerprint,
		TrustedAt:   now,
		LastSeenAt:  &now,
	}
	return s.persist()
}
