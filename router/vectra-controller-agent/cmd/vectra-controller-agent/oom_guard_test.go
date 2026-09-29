package main

import (
	"os"
	"regexp"
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
		// A whole active line: a commented-out guard must not satisfy this.
		active := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(line) + `\s*$`)
		if !active.Match(script) {
			t.Fatalf("vectra-oom-guard must keep an active %s", line)
		}
	}
}
