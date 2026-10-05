package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"vectra-controller-pro/internal/conntrack"
	"vectra-controller-pro/internal/coreengine/xray"
)

// `vctl forget-dns-flows`: the daemon's forgetRedirectedFlows for the paths
// that remove the data plane without it — dataplane-teardown.sh (stop, the
// hand-back before an update, removal) and the commit-confirm deadman. The
// table goes, the resolver's flows its DNS redirect took do not: for up to
// three minutes from their last packet dnsmasq's queries keep going to the
// dead loopback inbound (a customer router's DNS was patchy right after an
// r18 → r19 update). Best effort: it always exits 0, within
// forgetDNSFlowsWithin, whatever the kernel says.
func init() {
	register(command{name: "forget-dns-flows", summary: "Forget the resolver's DNS flows a removed redirect still holds (best effort)", run: cmdForgetDNSFlows})
}

// forgetDNSFlowsWithin bounds the command: a stop or an update must never
// wait on the connection table.
var forgetDNSFlowsWithin = 5 * time.Second

func cmdForgetDNSFlows(args []string) error {
	fs := newFlagSet("forget-dns-flows")
	ports := fs.String("ports", defaultDNSInboundPort(), "comma-separated loopback ports the DNS redirect sent queries to")
	if err := fs.Parse(args); err != nil {
		return nil // best effort: a bad flag forgets nothing, and fails nothing
	}
	var want []uint16
	for _, p := range strings.Split(*ports, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && n > 0 && n < 65536 && n != 53 {
			want = append(want, uint16(n))
		}
	}
	done := make(chan string, 1)
	go func() { done <- forgetDNSFlows(want) }()
	select {
	case msg := <-done:
		fmt.Println(msg)
	case <-time.After(forgetDNSFlowsWithin):
		fmt.Println("forget-dns-flows: gave up waiting for the connection table; the flows end on their own within three minutes")
	}
	return nil
}

// forgetDNSFlows forgets the flows redirected to each of ports, in one read
// of the table.
func forgetDNSFlows(ports []uint16) string {
	if len(ports) == 0 {
		return "forget-dns-flows: no port to forget flows to"
	}
	es, err := dnsFlowsRead()
	if err != nil {
		return "forget-dns-flows: the connection table is unreadable: " + err.Error()
	}
	var stale []conntrack.Entry
	for _, p := range ports {
		stale = append(stale, conntrack.RedirectedTo(es, p)...)
	}
	n, err := dnsFlowsForget(stale)
	if err != nil {
		return fmt.Sprintf("forget-dns-flows: forgot %d of %d flows: %v", n, len(stale), err)
	}
	return fmt.Sprintf("forget-dns-flows: forgot %d flows", n)
}

// defaultDNSInboundPort is the port of xray's DNS inbound every render has.
func defaultDNSInboundPort() string {
	_, port, err := net.SplitHostPort(xray.DefaultDNSListen)
	if err != nil {
		return "10053"
	}
	return port
}
