package failover

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"testing"

	"vectra-controller-pro/internal/xrayview"
)

func TestEndpointsResolveNamesAndSkipWhatDoesNotResolve(t *testing.T) {
	outs := []xrayview.Outbound{
		{Tag: "bridge-nl5", Address: "ru14.example.net", Port: 50055, Dials: true},
		{Tag: "direct-de5", Address: "203.0.113.9", Port: 443, Dials: true},
		{Tag: "hy2-nl5", Address: "gone.example.net", Port: 50055, Dials: true},
		{Tag: "DIRECT", Dials: false},
		{Tag: "odd", Address: "203.0.113.10", Port: 0, Dials: true},
	}
	lookup := func(_ context.Context, h string) ([]netip.Addr, error) {
		if h == "ru14.example.net" {
			return []netip.Addr{netip.MustParseAddr("203.0.113.5"), netip.MustParseAddr("::ffff:203.0.113.6")}, nil
		}
		return nil, errors.New("nxdomain")
	}
	got, unresolved := MapEndpoints(context.Background(), outs, lookup)
	want := Endpoints{
		{Proto: "tcp", Addr: netip.MustParseAddrPort("203.0.113.5:50055")}: {"bridge-nl5"},
		{Proto: "tcp", Addr: netip.MustParseAddrPort("203.0.113.6:50055")}: {"bridge-nl5"},
		{Proto: "tcp", Addr: netip.MustParseAddrPort("203.0.113.9:443")}:   {"direct-de5"},
	}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(unresolved, []string{"hy2-nl5"}) {
		t.Fatalf("%v (unresolved %v)", got, unresolved)
	}
}

// An endpoint names its transport and every outbound that dials it: a
// hysteria2 node on a TCP node's address and port is another endpoint, and
// two TCP outbounds on one are both judged by it.
func TestEndpointsKeyByTransportAndKeepEveryTag(t *testing.T) {
	outs := []xrayview.Outbound{
		{Tag: "bridge-nl5", Protocol: "vless", Transport: "tcp", Address: "203.0.113.5", Port: 443, Dials: true},
		{Tag: "sticky-nl5", Protocol: "vless", Transport: "ws", Address: "203.0.113.5", Port: 443, Dials: true},
		{Tag: "hy2-nl5", Protocol: "hysteria", Transport: "hysteria", Address: "203.0.113.5", Port: 443, Dials: true},
		{Tag: "direct-de6", Protocol: "vless", Transport: "tcp", Address: "2001:db8::9", Port: 443, Dials: true},
	}
	got, _ := MapEndpoints(context.Background(), outs, nil)
	want := Endpoints{
		{Proto: "tcp", Addr: netip.MustParseAddrPort("203.0.113.5:443")}:   {"bridge-nl5", "sticky-nl5"},
		{Proto: "udp", Addr: netip.MustParseAddrPort("203.0.113.5:443")}:   {"hy2-nl5"},
		{Proto: "tcp", Addr: netip.MustParseAddrPort("[2001:db8::9]:443")}: {"direct-de6"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}
