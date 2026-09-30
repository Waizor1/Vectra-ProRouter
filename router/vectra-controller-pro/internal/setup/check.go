package setup

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"vectra-controller-pro/internal/controlplane"
)

// The internet check's defaults: a name to resolve and an HTTP answer that is
// exactly 204 on a working connection (a captive portal answers otherwise).
const (
	DefaultProbeHost = "connectivitycheck.gstatic.com"
	DefaultProbeURL  = "http://connectivitycheck.gstatic.com/generate_204"
	// CheckBudget bounds one check (wan_check answers within it).
	CheckBudget = 8 * time.Second
)

// Checker looks at the router's own way out. Every socket carries Mark — the
// control plane's — and names are resolved straight at the WAN's DNS servers,
// so the answer holds with xray down and with the kill switch armed.
type Checker struct {
	Mark      int
	PanelURL  string
	ProbeURL  string
	ProbeHost string
	Budget    time.Duration
}

// Result is one check.
type Result struct {
	DNS      bool // the probe name resolved
	Internet bool // the probe answered 204
	Panel    bool // the panel answered at all (any HTTP status)
}

// Run checks DNS, the internet and the panel side by side, within the budget.
// dnsServers are the WAN's; none = the system's resolvers, still marked.
func (c Checker) Run(ctx context.Context, dnsServers []string) Result {
	budget := c.Budget
	if budget <= 0 {
		budget = CheckBudget
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var r Result
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		addrs, err := c.lookup(ctx, c.probeHost(), dnsServers)
		r.DNS = err == nil && len(addrs) > 0
	}()
	go func() {
		defer wg.Done()
		code, ok := c.get(ctx, c.probeURL(), dnsServers)
		r.Internet = ok && code == http.StatusNoContent
	}()
	go func() {
		defer wg.Done()
		_, ok := c.get(ctx, c.PanelURL, dnsServers)
		r.Panel = c.PanelURL != "" && ok
	}()
	wg.Wait()
	return r
}

func (c Checker) probeHost() string {
	if c.ProbeHost != "" {
		return c.ProbeHost
	}
	return DefaultProbeHost
}

func (c Checker) probeURL() string {
	if c.ProbeURL != "" {
		return c.ProbeURL
	}
	return DefaultProbeURL
}

// lookup resolves host at each server in turn (a literal address is itself).
func (c Checker) lookup(ctx context.Context, host string, servers []string) ([]string, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		return []string{a.String()}, nil
	}
	d := controlplane.MarkedDialer(c.Mark, 3*time.Second)
	try := func(server string) ([]string, error) {
		r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			if server != "" {
				address = net.JoinHostPort(server, "53")
			}
			return d.DialContext(ctx, network, address)
		}}
		lctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		return r.LookupHost(lctx, host)
	}
	if len(servers) == 0 {
		return try("")
	}
	err := errors.New("no DNS server answered")
	for _, s := range servers {
		addrs, e := try(s)
		if e == nil && len(addrs) > 0 {
			return addrs, nil
		}
		if e != nil {
			err = e
		}
	}
	return nil, err
}

// get fetches url without following a redirect: the status, and whether any
// HTTP answer came back.
func (c Checker) get(ctx context.Context, url string, servers []string) (int, bool) {
	if url == "" {
		return 0, false
	}
	d := controlplane.MarkedDialer(c.Mark, 4*time.Second)
	tr := &http.Transport{
		Proxy:               nil,
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: 4 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := c.lookup(ctx, host, servers)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				conn, e := d.DialContext(ctx, network, net.JoinHostPort(ip, port))
				if e == nil {
					return conn, nil
				}
				err = e
			}
			return nil, err
		},
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 6 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, false
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, false
	}
	resp.Body.Close()
	return resp.StatusCode, true
}
