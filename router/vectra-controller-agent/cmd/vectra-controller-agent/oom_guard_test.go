package main

import (
	"os"
	"strings"
	"testing"
)

// PassWall 26.9 renamed the global DNS forwarder from dnsmasq_default to
// dnsmasq_acl_default. The guard has to shield both until no router runs 26.8.
func TestOOMGuardShieldsPassWallDNSForwarderOnEveryLayout(t *testing.T) {
	script, err := os.ReadFile("../../openwrt/files/usr/sbin/vectra-oom-guard")
	if err != nil {
		t.Fatalf("read packaged OOM guard: %v", err)
	}
	for _, line := range []string{
		`guard "/tmp/etc/passwall2/bin/dnsmasq_default" -200`,
		`guard "/tmp/etc/passwall2/bin/dnsmasq_acl_default" -200`,
	} {
		if !strings.Contains(string(script), line) {
			t.Fatalf("vectra-oom-guard must keep %s", line)
		}
	}
}
