package failover

import (
	"context"
	"net/netip"
	"slices"

	"vectra-controller-pro/internal/xrayview"
)

// Lookup resolves a node's name.
type Lookup func(ctx context.Context, host string) ([]netip.Addr, error)

// Endpoint is where xray reaches a node: over which transport, as conntrack
// names it (tcp, udp), and at which address.
type Endpoint struct {
	Proto string
	Addr  netip.AddrPort
}

// Endpoints names every outbound that dials each endpoint: outbounds on one
// endpoint share its fate.
type Endpoints map[Endpoint][]string

// transports are what an outbound's connections to its server travel over.
func transports(o xrayview.Outbound) []string {
	switch o.Transport {
	case "hysteria", "kcp", "mkcp", "quic":
		return []string{"udp"}
	case "xhttp":
		// HTTP/3 when its ALPN says so: either.
		return []string{"tcp", "udp"}
	}
	switch o.Protocol {
	case "hysteria", "hysteria2", "wireguard":
		return []string{"udp"}
	}
	return []string{"tcp"}
}

// MapEndpoints maps the endpoints of every node of a render, names resolved
// through lookup, and counts the names that did not resolve: without an
// address there is no evidence, and such a node is never judged.
func MapEndpoints(ctx context.Context, outs []xrayview.Outbound, lookup Lookup) (Endpoints, int) {
	eps := Endpoints{}
	unresolved := 0
	for _, o := range outs {
		if !o.Dials || o.Address == "" || o.Port <= 0 || o.Port > 65535 {
			continue
		}
		var addrs []netip.Addr
		if a, err := netip.ParseAddr(o.Address); err == nil {
			addrs = []netip.Addr{a}
		} else if lookup != nil {
			if got, err := lookup(ctx, o.Address); err == nil {
				addrs = got
			}
		}
		if len(addrs) == 0 {
			unresolved++
			continue
		}
		for _, a := range addrs {
			for _, proto := range transports(o) {
				k := Endpoint{Proto: proto, Addr: netip.AddrPortFrom(a.Unmap(), uint16(o.Port))}
				if !slices.Contains(eps[k], o.Tag) {
					eps[k] = append(eps[k], o.Tag)
				}
			}
		}
	}
	return eps, unresolved
}
