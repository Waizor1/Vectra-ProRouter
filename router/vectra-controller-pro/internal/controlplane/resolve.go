package controlplane

import (
	"context"
	"net"
	"sync"
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

// KnownAddrs are the control plane's addresses as built in: dialled when
// neither the direct resolvers nor the system's give the panel's name an
// address that answers — the name blocked or forged, every resolver down.
// The URL keeps the name, so TLS still sends it (SNI) and checks the
// certificate against it: a stale address fails the handshake, it cannot
// impersonate the panel.
var KnownAddrs = map[string][]string{
	"api.vectra-pro.net":    {"72.56.14.52"},
	"router.vectra-pro.net": {"72.56.14.52"},
}

// fallbackAddrs are the addresses a name is dialled at when resolving it
// failed: the last one that answered in this process, then the owner's (UCI
// control_ip, Options.FallbackAddrs), then KnownAddrs.
type fallbackAddrs struct {
	host       string   // the name configured applies to
	configured []string // Options.FallbackAddrs

	mu      sync.Mutex
	learned map[string]string
}

func (f *fallbackAddrs) learn(host string, c net.Conn) {
	if f == nil || c == nil {
		return
	}
	a, ok := c.RemoteAddr().(*net.TCPAddr)
	if !ok || a.IP.To4() == nil || a.IP.IsLoopback() {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.learned == nil {
		f.learned = map[string]string{}
	}
	f.learned[host] = a.IP.String()
}

func (f *fallbackAddrs) addrs(host string) []string {
	if f == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(ips ...string) {
		for _, ip := range ips {
			if net.ParseIP(ip) != nil && !seen[ip] {
				seen[ip] = true
				out = append(out, ip)
			}
		}
	}
	f.mu.Lock()
	if ip := f.learned[host]; ip != "" {
		add(ip)
	}
	f.mu.Unlock()
	if host == f.host {
		add(f.configured...)
	}
	add(KnownAddrs[host]...)
	return out
}

// resolvingDial dials addr with d, having resolved its host with r first —
// IPv4, the path the router carries — and trying each address in turn. A
// lookup that fails leaves the name to d, i.e. to the system's resolver.
// When neither gives an address that answers, the name's fallback addresses
// are tried (fallbackAddrs; fb nil: none).
func resolvingDial(d *net.Dialer, r *net.Resolver, fb *fallbackAddrs) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil || net.ParseIP(host) != nil {
			return d.DialContext(ctx, network, addr)
		}
		var ips []net.IP
		if r != nil {
			lctx, cancel := context.WithTimeout(ctx, lookupBudget)
			ips, _ = r.LookupIP(lctx, "ip4", host)
			cancel()
		}
		tried := map[string]bool{}
		var first error
		if len(ips) == 0 {
			c, err := d.DialContext(ctx, network, addr)
			if err == nil {
				fb.learn(host, c)
				return c, nil
			}
			first = err
		} else {
			c, err := dialEach(ctx, d, network, ips, port, tried)
			if err == nil {
				fb.learn(host, c)
				return c, nil
			}
			first = err
		}
		var rest []net.IP
		for _, a := range fb.addrs(host) {
			if !tried[a] {
				rest = append(rest, net.ParseIP(a))
			}
		}
		if len(rest) == 0 || ctx.Err() != nil {
			return nil, first
		}
		if c, err := dialEach(ctx, d, network, rest, port, tried); err == nil {
			return c, nil
		}
		return nil, first
	}
}

// dialEach dials ips in turn, each its share of d's timeout, as net.Dialer
// shares it between the addresses it resolved itself: one dead address must
// not use up the whole request.
func dialEach(ctx context.Context, d *net.Dialer, network string, ips []net.IP, port string, tried map[string]bool) (net.Conn, error) {
	per := d.Timeout
	if per > 0 && len(ips) > 1 {
		per /= time.Duration(len(ips))
		if per < 2*time.Second {
			per = 2 * time.Second
		}
	}
	var first error
	for _, ip := range ips {
		tried[ip.String()] = true
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
