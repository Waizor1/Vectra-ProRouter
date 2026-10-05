package main

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/conntrack"
)

// `vctl forget-dns-flows` (dataplane-teardown.sh, the deadman): the flows
// redirected to xray's DNS inbound are forgotten, nothing else — not the
// router's own open-path DNS, which is where a removed table leaves it; and
// whatever the kernel does, it returns, within its bound, without an error.
func TestForgetDNSFlowsTakesTheRedirectedOnesOnly(t *testing.T) {
	const table = `ipv4     2 udp      17 178 src=192.168.0.2 dst=77.37.251.33 sport=41234 dport=53 src=127.0.0.1 dst=192.168.0.2 sport=10053 dport=41234 [ASSURED] mark=0 zone=0 use=2
ipv4     2 tcp      6 100 ESTABLISHED src=192.168.0.2 dst=77.37.255.30 sport=40000 dport=53 src=127.0.0.1 dst=192.168.0.2 sport=10053 dport=40000 [ASSURED] mark=0 zone=0 use=2
ipv4     2 udp      17 170 src=192.168.0.2 dst=77.37.255.30 sport=41300 dport=53 src=77.37.255.30 dst=192.168.0.2 sport=53 dport=41300 [ASSURED] mark=0 zone=0 use=2
ipv4     2 udp      17 170 src=192.168.0.2 dst=8.8.8.8 sport=41301 dport=53 src=127.0.0.1 dst=192.168.0.2 sport=15353 dport=41301 [ASSURED] mark=0 zone=0 use=2`
	prevRead, prevForget, prevWithin := dnsFlowsRead, dnsFlowsForget, forgetDNSFlowsWithin
	t.Cleanup(func() { dnsFlowsRead, dnsFlowsForget, forgetDNSFlowsWithin = prevRead, prevForget, prevWithin })
	dnsFlowsRead = func() ([]conntrack.Entry, error) { return conntrack.Parse(strings.NewReader(table)), nil }
	var forgotten []uint16
	dnsFlowsForget = func(es []conntrack.Entry) (int, error) {
		for _, e := range es {
			forgotten = append(forgotten, e.SPort)
		}
		return len(es), nil
	}
	if err := cmdForgetDNSFlows(nil); err != nil || !reflect.DeepEqual(forgotten, []uint16{41234, 40000}) {
		t.Fatalf("default: err %v, forgotten %v; want the two flows into 10053", err, forgotten)
	}
	forgotten = nil
	if err := cmdForgetDNSFlows([]string{"-ports", "10053,15353,53,x"}); err != nil || !reflect.DeepEqual(forgotten, []uint16{41234, 40000, 41301}) {
		t.Fatalf("-ports: err %v, forgotten %v", err, forgotten)
	}
	if got := forgetDNSFlows(nil); !strings.Contains(got, "no port") {
		t.Fatalf("no port: %q", got)
	}
	if err := cmdForgetDNSFlows([]string{"-bogus"}); err != nil {
		t.Fatalf("a bad flag failed the command: %v", err)
	}

	// A table that never answers: given up on, no error.
	forgetDNSFlowsWithin = 50 * time.Millisecond
	// cmdForgetDNSFlows abandons its worker here (the process exits in
	// production; the test does not): release it and wait for it to finish
	// before the hooks are restored, so it never reads them mid-restore.
	block := make(chan struct{})
	finished := make(chan struct{})
	t.Cleanup(func() { close(block); <-finished })
	dnsFlowsForget = func([]conntrack.Entry) (int, error) { close(finished); return 0, nil }
	dnsFlowsRead = func() ([]conntrack.Entry, error) { <-block; return nil, nil }
	start := time.Now()
	if err := cmdForgetDNSFlows(nil); err != nil || time.Since(start) > 2*time.Second {
		t.Fatalf("a hanging table: err %v after %s", err, time.Since(start))
	}
}
