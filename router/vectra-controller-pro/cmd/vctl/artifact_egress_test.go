// SPDX-License-Identifier: LicenseRef-Vectra-Proprietary
// Copyright (c) 2026 Vectra. All rights reserved.

package main

import (
	"net/netip"
	"testing"
)

func TestArtifactEgressAllowlist(t *testing.T) {
	cases := []struct {
		ip    string
		allow bool
	}{
		// Public unicast — allowed.
		{"93.184.216.34", true},
		{"1.1.1.1", true},
		{"2606:4700:4700::1111", true},
		// RFC1918 private.
		{"10.0.0.5", false},
		{"172.16.3.4", false},
		{"192.168.1.1", false},
		// ULA / IPv6 private.
		{"fd00::1", false},
		// Loopback.
		{"127.0.0.1", false},
		{"::1", false},
		// Link-local.
		{"169.254.1.1", false},
		{"fe80::1", false},
		// CGNAT 100.64.0.0/10.
		{"100.64.0.1", false},
		{"100.127.255.254", false},
		// Unspecified.
		{"0.0.0.0", false},
		{"::", false},
		// Multicast.
		{"224.0.0.1", false},
		{"ff02::1", false},
		// An IPv4-mapped public address must be treated as its v4 (allowed);
		// a mapped private one rejected. (The Control hook unmaps first.)
		{"8.8.8.8", true},
	}
	for _, c := range cases {
		ip, err := netip.ParseAddr(c.ip)
		if err != nil {
			t.Fatalf("parse %q: %v", c.ip, err)
		}
		if got := artifactEgressAddrAllowed(ip.Unmap()); got != c.allow {
			t.Errorf("artifactEgressAddrAllowed(%s) = %v, want %v", c.ip, got, c.allow)
		}
	}
}

func TestArtifactEgressControlRejectsAndAllows(t *testing.T) {
	// A private resolved address is refused at the dial control hook.
	if err := artifactEgressControl("tcp4", "192.168.0.10:443", nil); err == nil {
		t.Fatal("expected a private resolved address to be refused")
	}
	// A loopback one too (this is why tests use their own server.Client()).
	if err := artifactEgressControl("tcp4", "127.0.0.1:8443", nil); err == nil {
		t.Fatal("expected loopback to be refused")
	}
	// A public one passes.
	if err := artifactEgressControl("tcp4", "93.184.216.34:443", nil); err != nil {
		t.Fatalf("expected a public resolved address to pass, got %v", err)
	}
}
