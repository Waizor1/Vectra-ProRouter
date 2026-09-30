package controlplane

import (
	"context"
	"net"
	"sync/atomic"
	"syscall"
	"time"
)

// DirectResolvers answer the control plane's own lookups, over TCP from the
// router itself on the marked path — never through dnsmasq.
//
// dnsmasq's upstream goes through the tunnel while vctl carries the router
// (xray's DNS inbound, internal/coreengine/xray/dns_steer.go), and the tunnel
// is exactly what may be down when the panel is needed most. Asked here, a
// name the panel is at does not depend on xray, on the tunnel or on dnsmasq;
// only when both resolvers fail does the system's resolver get the question.
var DirectResolvers = []string{"8.8.8.8:53", "77.88.8.8:53"}

// lookupBudget bounds the direct lookup before the system's resolver is asked.
const lookupBudget = 4 * time.Second

// directResolver asks DirectResolvers in turn over TCP, each socket carrying
// the control mark. A pure-Go resolver dials once per nameserver and attempt
// in /etc/resolv.conf; which one it thought it was dialling does not matter.
func directResolver(control func(network, address string, c syscall.RawConn) error) *net.Resolver {
	var next uint32
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			i := atomic.AddUint32(&next, 1) - 1
			d := net.Dialer{Timeout: 3 * time.Second, Control: control}
			return d.DialContext(ctx, "tcp", DirectResolvers[int(i)%len(DirectResolvers)])
		},
	}
}

// MarkedResolver resolves on the control plane's own path — public resolvers
// over sockets with SO_MARK = mark — not through the tunnel: a node's name must
// resolve while the node it names is dead.
func MarkedResolver(mark int) *net.Resolver { return directResolver(setSocketMark(mark)) }

// resolvingDial dials addr with d, having resolved its host with r first —
// IPv4, the path the router carries — and trying each address in turn. A
// lookup that fails leaves the name to d, i.e. to the system's resolver.
func resolvingDial(d *net.Dialer, r *net.Resolver) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil || net.ParseIP(host) != nil || r == nil {
			return d.DialContext(ctx, network, addr)
		}
		lctx, cancel := context.WithTimeout(ctx, lookupBudget)
		ips, lerr := r.LookupIP(lctx, "ip4", host)
		cancel()
		if lerr != nil || len(ips) == 0 {
			return d.DialContext(ctx, network, addr)
		}
		// Each address gets its share of the dial timeout, as net.Dialer
		// shares it between the addresses it resolved itself: one dead
		// address must not use up the whole request.
		per := d.Timeout
		if per > 0 && len(ips) > 1 {
			per /= time.Duration(len(ips))
			if per < 2*time.Second {
				per = 2 * time.Second
			}
		}
		var first error
		for _, ip := range ips {
			dctx, cancel := ctx, context.CancelFunc(func() {})
			if per > 0 {
				dctx, cancel = context.WithTimeout(ctx, per)
			}
			c, err := d.DialContext(dctx, network, net.JoinHostPort(ip.String(), port))
			cancel()
			if err == nil {
				return c, nil
			}
			if first == nil {
				first = err
			}
		}
		return nil, first
	}
}
