// SPDX-License-Identifier: LicenseRef-Vectra-Proprietary
// Copyright (c) 2026 Vectra. All rights reserved.

package inventory

import (
	"os"
	"path/filepath"
	"testing"
)

func writeDeviceBindingFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// nic writes a fake /sys/class/net/<name> interface.
func nic(t *testing.T, root, name, address, addrAssignType string, physical bool) {
	t.Helper()
	dir := filepath.Join(root, "sys", "class", "net", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeDeviceBindingFile(t, filepath.Join(dir, "address"), address+"\n")
	if addrAssignType != "" {
		writeDeviceBindingFile(t, filepath.Join(dir, "addr_assign_type"), addrAssignType+"\n")
	}
	if physical {
		// A "device" entry (a symlink on real hardware) marks a physical NIC;
		// a directory is enough for Lstat to see it.
		if err := os.MkdirAll(filepath.Join(dir, "device"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// The hash is pinned to the SAME golden vector the panel verifier and the
// legacy agent use (packages/contracts/fixtures/router-auth-vectors.json
// deviceBinding: board xiaomi,mi-router-ax3000t + mac aa:bb:cc:dd:ee:01).
const deviceBindingVectorHash = "v1:839d72590bc5688459a77f80a872bff4212aca5fc66daa94656e143bf2cb6d32"

func TestComputeDeviceBindingMatchesVector(t *testing.T) {
	root := t.TempDir()
	writeDeviceBindingFile(t, filepath.Join(root, "tmp", "sysinfo", "board_name"), "xiaomi,mi-router-ax3000t\n")
	nic(t, root, "lo", "00:00:00:00:00:00", "0", false)
	nic(t, root, "eth0", "aa:bb:cc:dd:ee:01", "0", true)

	if got := computeDeviceBinding(root); got != deviceBindingVectorHash {
		t.Fatalf("binding = %q, want %q", got, deviceBindingVectorHash)
	}
}

func TestComputeDeviceBindingSkipsLoAndPicksNameOrder(t *testing.T) {
	root := t.TempDir()
	writeDeviceBindingFile(t, filepath.Join(root, "tmp", "sysinfo", "board_name"), "xiaomi,mi-router-ax3000t\n")
	nic(t, root, "lo", "ff:ff:ff:ff:ff:ff", "0", true)
	nic(t, root, "eth1", "aa:bb:cc:dd:ee:99", "0", true)
	nic(t, root, "eth0", "aa:bb:cc:dd:ee:01", "0", true)

	if got := computeDeviceBinding(root); got != deviceBindingVectorHash {
		t.Fatalf("binding = %q, want %q (eth0 should win)", got, deviceBindingVectorHash)
	}
}

func TestComputeDeviceBindingSkipsNonPhysicalInPass1(t *testing.T) {
	root := t.TempDir()
	writeDeviceBindingFile(t, filepath.Join(root, "tmp", "sysinfo", "board_name"), "xiaomi,mi-router-ax3000t\n")
	// br-lan sorts before eth0 but is virtual (no device link): pass 1 must
	// skip it and pick the physical eth0 with the vector mac.
	nic(t, root, "br-lan", "aa:bb:cc:dd:ee:99", "0", false)
	nic(t, root, "eth0", "aa:bb:cc:dd:ee:01", "0", true)

	if got := computeDeviceBinding(root); got != deviceBindingVectorHash {
		t.Fatalf("binding = %q, want the physical eth0's %q", got, deviceBindingVectorHash)
	}
}

func TestComputeDeviceBindingRandomNicUsedOnlyInPass2(t *testing.T) {
	root := t.TempDir()
	writeDeviceBindingFile(t, filepath.Join(root, "tmp", "sysinfo", "board_name"), "xiaomi,mi-router-ax3000t\n")
	// Only a random-MAC physical NIC exists: pass 1 skips it (addr_assign_type
	// 1), pass 2 accepts it.
	nic(t, root, "wlan0", "aa:bb:cc:dd:ee:01", "1", true)

	if got := computeDeviceBinding(root); got != deviceBindingVectorHash {
		t.Fatalf("binding = %q, want the pass-2 wlan0's %q", got, deviceBindingVectorHash)
	}
}

func TestComputeDeviceBindingRejectsZeroAndBroadcast(t *testing.T) {
	root := t.TempDir()
	writeDeviceBindingFile(t, filepath.Join(root, "tmp", "sysinfo", "board_name"), "xiaomi,mi-router-ax3000t\n")
	nic(t, root, "eth0", "00:00:00:00:00:00", "0", true)
	nic(t, root, "eth1", "ff:ff:ff:ff:ff:ff", "0", true)

	if got := computeDeviceBinding(root); got != "" {
		t.Fatalf("binding = %q, want empty (all-zero and broadcast MACs are rejected)", got)
	}
}

func TestComputeDeviceBindingEmptyWhenBoardMissing(t *testing.T) {
	root := t.TempDir()
	nic(t, root, "eth0", "aa:bb:cc:dd:ee:01", "0", true)
	if got := computeDeviceBinding(root); got != "" {
		t.Fatalf("binding = %q, want empty without a board name", got)
	}
}

func TestComputeDeviceBindingEmptyWhenNoMAC(t *testing.T) {
	root := t.TempDir()
	writeDeviceBindingFile(t, filepath.Join(root, "tmp", "sysinfo", "board_name"), "xiaomi,mi-router-ax3000t\n")
	// No /sys/class/net at all.
	if got := computeDeviceBinding(root); got != "" {
		t.Fatalf("binding = %q, want empty without a usable MAC", got)
	}
}
