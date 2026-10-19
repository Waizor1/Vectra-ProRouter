// SPDX-License-Identifier: LicenseRef-Vectra-Proprietary
// Copyright (c) 2026 Vectra. All rights reserved.

package state

import (
	"path/filepath"
	"testing"
)

// The A2 check-in nonce persists and reloads, and an absent field reloads as
// "" so signing stops when the panel stops issuing nonces.
func TestCheckinNoncePersistsAndClears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	if err := Save(path, PersistedState{RouterID: "r-1", AgentToken: "tok", CheckinNonce: "vcn1.123.rand.mac"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.CheckinNonce != "vcn1.123.rand.mac" {
		t.Fatalf("CheckinNonce = %q, want it to round-trip", got.CheckinNonce)
	}

	// Re-save with the nonce cleared (the panel stopped issuing one).
	if err := Save(path, PersistedState{RouterID: "r-1", AgentToken: "tok"}); err != nil {
		t.Fatalf("Save (cleared): %v", err)
	}
	got, err = Load(path)
	if err != nil {
		t.Fatalf("Load (cleared): %v", err)
	}
	if got.CheckinNonce != "" {
		t.Fatalf("CheckinNonce = %q, want empty after clearing", got.CheckinNonce)
	}
}
