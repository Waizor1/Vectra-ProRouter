// SPDX-License-Identifier: LicenseRef-Vectra-Proprietary
// Copyright (c) 2026 Vectra. All rights reserved.

package inventory

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// deviceBindingRoot is the filesystem root the binding inputs are read from.
// "/" in production; tests point it at a fake tree.
var deviceBindingRoot = "/"

// deviceBindingLabel prefixes the hashed input so the fingerprint is domain
// separated. MUST match computeDeviceBinding on the panel.
const deviceBindingLabel = "vectra-device-binding/v1\n"

// ComputeDeviceBinding returns the A4 device binding
// "v1:" + hex(sha256(label + boardName + "\n" + mac)), or "" when either input
// is missing. boardName is the trimmed contents of /tmp/sysinfo/board_name;
// mac is selected from /sys/class/net (see selectDeviceBindingMAC). The raw MAC
// never leaves the device: only this hash is reported.
//
// This deliberately reads /tmp/sysinfo/board_name directly rather than reusing
// the collector's ubus-based board detection: the panel stored the hash a
// device reported with this exact formula and board-name source, so any other
// source could silently change the hash for the same box.
func ComputeDeviceBinding() string {
	return computeDeviceBinding(deviceBindingRoot)
}

func computeDeviceBinding(root string) string {
	boardName := strings.TrimSpace(readDeviceBindingFile(filepath.Join(root, "tmp", "sysinfo", "board_name")))
	if boardName == "" {
		return ""
	}
	mac := selectDeviceBindingMAC(root)
	if mac == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(deviceBindingLabel + boardName + "\n" + mac))
	return "v1:" + hex.EncodeToString(sum[:])
}

// selectDeviceBindingMAC walks /sys/class/net in name order, skips lo, and
// returns the first usable MAC. Pass 1 considers only physical interfaces (a
// "device" entry present) whose addr_assign_type is not "1" (random); pass 2,
// only if pass 1 found nothing, considers any interface. A usable MAC is six
// bytes, not all-zero and not broadcast, rendered lower-case colon-separated.
func selectDeviceBindingMAC(root string) string {
	netDir := filepath.Join(root, "sys", "class", "net")
	entries, err := os.ReadDir(netDir)
	if err != nil {
		return ""
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() == "lo" {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		if !hasDeviceLink(netDir, name) {
			continue
		}
		// A missing or unreadable addr_assign_type counts as not-random.
		if deviceAddrAssignType(netDir, name) == "1" {
			continue
		}
		if mac := readValidMAC(netDir, name); mac != "" {
			return mac
		}
	}

	for _, name := range names {
		if mac := readValidMAC(netDir, name); mac != "" {
			return mac
		}
	}
	return ""
}

func hasDeviceLink(netDir, name string) bool {
	_, err := os.Lstat(filepath.Join(netDir, name, "device"))
	return err == nil
}

func deviceAddrAssignType(netDir, name string) string {
	return strings.TrimSpace(readDeviceBindingFile(filepath.Join(netDir, name, "addr_assign_type")))
}

func readValidMAC(netDir, name string) string {
	raw := strings.TrimSpace(readDeviceBindingFile(filepath.Join(netDir, name, "address")))
	if raw == "" {
		return ""
	}
	hardware, err := net.ParseMAC(raw)
	if err != nil || len(hardware) != 6 {
		return ""
	}
	allZero := true
	allBroadcast := true
	for _, octet := range hardware {
		if octet != 0x00 {
			allZero = false
		}
		if octet != 0xff {
			allBroadcast = false
		}
	}
	if allZero || allBroadcast {
		return ""
	}
	return strings.ToLower(hardware.String())
}

func readDeviceBindingFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}
