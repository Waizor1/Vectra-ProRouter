// Package exitcheck asks each foreign exit for blocked sites through xray's
// exit probe (coreengine/xray/exit_check.go) and judges which exits cannot
// carry them.
//
// 1111, 2026-09-30: through bridge-us5 Instagram, Facebook, X, LinkedIn and
// Discord ended in a failed handshake while Google and the observatory's
// neutral URL answered — the leg from the Russian entry to the exit lets the
// filter see what is inside. The observatory held it alive and leastLoad kept
// handing it connections; nothing but asking for a blocked site through that
// one exit tells it apart.
package exitcheck

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"vectra-controller-pro/internal/coreengine/xray"
)

// Absolve is how many clean rounds in a row bring an unfit exit back. Each
// change restarts xray; a leg that flaps must not flap the router with it.
const Absolve = 2

// Result is what one exit answered in a round.
type Result struct {
	Tag string
	// Control: the neutral URL answered through the exit — it is alive.
	Control bool
	// Blocked: each blocked site asked, whether it answered. Any HTTP answer
	// counts; the filter kills the handshake. The first answer ends the
	// exit's round.
	Blocked []bool
}

func (r Result) fit() bool {
	if !r.Control {
		return false
	}
	for _, b := range r.Blocked {
		if b {
			return true
		}
	}
	return false
}

// filtered: alive, and no blocked site answered through it.
func (r Result) filtered() bool { return r.Control && len(r.Blocked) > 0 && !r.fit() }

type mark struct {
	since time.Time
	clean int
}

// State is what the check keeps between rounds. The zero value is ready.
type State struct {
	mu    sync.Mutex
	unfit map[string]*mark
	// egress: the country each exit really leaves in, and when it was seen.
	egress map[string]seen
}

// seen: the country and when it was located; asked, when the trace was
// last asked through the exit, answered or not.
type seen struct {
	cc    string
	at    time.Time
	asked time.Time
}

// EgressRetry: an exit the trace did not answer through is asked again no
// sooner — a dead exit must not cost a request every round.
const EgressRetry = time.Hour

// SetEgress keeps where the asked exits were seen leaving (tag → ISO
// country); one the trace did not answer through is remembered as asked.
func (s *State) SetEgress(asked []string, located map[string]string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.egress == nil {
		s.egress = map[string]seen{}
	}
	for _, t := range asked {
		if cc := located[t]; cc != "" {
			s.egress[t] = seen{cc: cc, at: now, asked: now}
		} else {
			// Not answered: the country known stays shown, the next look
			// waits EgressRetry.
			e := s.egress[t]
			e.asked = now
			s.egress[t] = e
		}
	}
}

// KeepEgress forgets where exits no longer in the render were seen leaving.
// Without it every exit the provider ever named stayed in memory and in
// state.json for good. An empty list (no render to read) forgets nothing.
func (s *State) KeepEgress(tags []string) {
	if len(tags) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	keep := make(map[string]bool, len(tags))
	for _, t := range tags {
		keep[t] = true
	}
	for t := range s.egress {
		if !keep[t] {
			delete(s.egress, t)
		}
	}
}

// Located is where an exit was seen leaving, and when (the state file's).
type Located struct {
	CC string
	At time.Time
}

// EgressSnapshot is every located exit, for the state file; nil when none.
func (s *State) EgressSnapshot() map[string]Located {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out map[string]Located
	for t, e := range s.egress {
		if e.cc == "" {
			continue
		}
		if out == nil {
			out = map[string]Located{}
		}
		out[t] = Located{CC: e.cc, At: e.at}
	}
	return out
}

// RestoreEgress takes a snapshot back after a restart: each country shown at
// once, asked again when its day is up — a restart costs no request.
func (s *State) RestoreEgress(snap map[string]Located) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for t, l := range snap {
		if l.CC == "" {
			continue
		}
		if s.egress == nil {
			s.egress = map[string]seen{}
		}
		s.egress[t] = seen{cc: l.CC, at: l.At, asked: l.At}
	}
}

// Egress is where each exit was last seen leaving; nil when none was.
func (s *State) Egress() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.egress) == 0 {
		return nil
	}
	out := make(map[string]string, len(s.egress))
	for t, e := range s.egress {
		if e.cc != "" {
			out[t] = e.cc
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// EgressDue are the exits, in the given order, whose country is unknown or
// older than maxAge: an exit's country rarely moves, and each look costs a
// request through it.
func (s *State) EgressDue(exits []string, now time.Time, maxAge time.Duration) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, t := range exits {
		e, ok := s.egress[t]
		switch {
		case !ok:
			out = append(out, t)
		case now.Sub(e.asked) < EgressRetry:
			// Asked within the hour: not again yet, answered or not.
		case e.cc == "" || now.Sub(e.at) >= maxAge:
			out = append(out, t)
		}
	}
	return out
}

// Round folds one round in and says whether the set of unfit exits changed.
// Only against a witness: with no exit carrying a blocked site, the sites may
// be down or the router's own network — nobody is judged. A dead exit is the
// observatory's to judge and keeps whatever it had.
func (s *State) Round(rs []Result, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	witness := false
	for _, r := range rs {
		if r.fit() {
			witness = true
		}
	}
	if !witness {
		return false
	}
	if s.unfit == nil {
		s.unfit = map[string]*mark{}
	}
	changed := false
	for _, r := range rs {
		m := s.unfit[r.Tag]
		switch {
		case r.filtered() && m == nil:
			s.unfit[r.Tag] = &mark{since: now}
			changed = true
		case r.filtered():
			m.clean = 0
		case m != nil && r.fit():
			m.clean++
			if m.clean >= Absolve {
				delete(s.unfit, r.Tag)
				changed = true
			}
		}
	}
	return changed
}

// Unfit are the exits found unfit, sorted; nil when none.
func (s *State) Unfit() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for t := range s.unfit {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Since is when an exit was found unfit; zero when it is not.
func (s *State) Since(tag string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.unfit[tag]; m != nil {
		return m.since
	}
	return time.Time{}
}

// Prober asks exits through xray's exit probe.
type Prober struct {
	// Listen is the probe inbound's loopback address.
	Listen string
	// Control is the neutral URL — the observatory's own.
	Control string
	// Blocked are sites the filter blocks, from different owners, so one
	// site's outage never looks like a filter.
	Blocked []string
	// Timeout bounds one request; Attempts is how many times a URL is asked
	// before it counts as not answering.
	Timeout  time.Duration
	Attempts int
	// Parallel is how many exits are asked at once.
	Parallel int
	// RootCAs replaces the system's roots (tests); nil = the system's.
	RootCAs *x509.CertPool
	// Where is a trace that names the country a request comes from
	// ("loc=PL", Cloudflare's /cdn-cgi/trace): where an exit really leaves.
	Where string
}

// Locate says where each exit really leaves (tag → ISO country), for those
// the trace answered through.
func (p Prober) Locate(ctx context.Context, exits []string) map[string]string {
	out := map[string]string{}
	var mu sync.Mutex
	par := p.Parallel
	if par < 1 {
		par = 1
	}
	sem := make(chan struct{}, par)
	var wg sync.WaitGroup
	for _, tag := range exits {
		wg.Add(1)
		sem <- struct{}{}
		go func(tag string) {
			defer wg.Done()
			defer func() { <-sem }()
			if cc := p.where(ctx, tag); cc != "" {
				mu.Lock()
				out[tag] = cc
				mu.Unlock()
			}
		}(tag)
	}
	wg.Wait()
	return out
}

func (p Prober) where(ctx context.Context, tag string) string {
	if p.Where == "" {
		return ""
	}
	c := p.client(tag)
	defer c.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.Where, nil)
	if err != nil {
		return ""
	}
	resp, err := c.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return traceCountry(string(body))
}

// traceCountry is the country a Cloudflare trace names ("loc=PL"): two
// letters, and not "XX" (unknown to Cloudflare) — "T1" (Tor) has a digit.
func traceCountry(body string) string {
	for _, line := range strings.Split(body, "\n") {
		v, ok := strings.CutPrefix(strings.TrimSpace(line), "loc=")
		if !ok {
			continue
		}
		v = strings.ToUpper(v)
		if len(v) != 2 || v == "XX" || v[0] < 'A' || v[0] > 'Z' || v[1] < 'A' || v[1] > 'Z' {
			return ""
		}
		return v
	}
	return ""
}

// Round asks every exit, in the given order.
func (p Prober) Round(ctx context.Context, exits []string) []Result {
	out := make([]Result, len(exits))
	par := p.Parallel
	if par < 1 {
		par = 1
	}
	sem := make(chan struct{}, par)
	var wg sync.WaitGroup
	for i, tag := range exits {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, tag string) {
			defer wg.Done()
			defer func() { <-sem }()
			out[i] = p.exit(ctx, tag)
		}(i, tag)
	}
	wg.Wait()
	return out
}

func (p Prober) exit(ctx context.Context, tag string) Result {
	r := Result{Tag: tag}
	c := p.client(tag)
	defer c.CloseIdleConnections()
	// A blocked site answering says it all — the exit is alive and lets it
	// through: one request for a healthy exit. Only when it does not is the
	// neutral URL asked, to tell a dead exit from a filtered one, and then
	// the other sites.
	if len(p.Blocked) > 0 && p.answers(ctx, c, p.Blocked[0]) {
		r.Control, r.Blocked = true, []bool{true}
		return r
	}
	if !p.answers(ctx, c, p.Control) {
		return r
	}
	r.Control = true
	// The exit is up. A "no" counts only now: the first site is asked again —
	// it may have failed on a node restarting under the round (the stand,
	// 2026-09-30: a healthy exit judged filtered so) — then the others.
	for _, u := range p.Blocked {
		a := p.answers(ctx, c, u)
		r.Blocked = append(r.Blocked, a)
		if a {
			break
		}
	}
	if ctx.Err() != nil && !r.fit() {
		// The round's deadline cut this exit short: a site never finished
		// asking is no "no". Unjudged — reported as not answering at all.
		return Result{Tag: tag}
	}
	return r
}

func (p Prober) answers(ctx context.Context, c *http.Client, u string) bool {
	n := p.Attempts
	if n < 1 {
		n = 1
	}
	for i := 0; i < n; i++ {
		if ctx.Err() != nil {
			return false
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return false
		}
		resp, err := c.Do(req)
		if err == nil {
			// The headers are the answer; the body is never read.
			resp.Body.Close()
			return true
		}
	}
	return false
}

// client reaches one exit: the probe account named for it. Certificates are
// checked: a filter that answers with a page of its own under a forged
// certificate has not let the site through.
func (p Prober) client(tag string) *http.Client {
	proxy := &url.URL{Scheme: "http", User: url.UserPassword(xray.ExitProbeUser(tag), xray.ExitProbePass), Host: p.Listen}
	return &http.Client{
		Timeout: p.Timeout,
		Transport: &http.Transport{
			Proxy:               http.ProxyURL(proxy),
			TLSClientConfig:     &tls.Config{RootCAs: p.RootCAs, MinVersion: tls.VersionTLS12},
			DisableKeepAlives:   true,
			TLSHandshakeTimeout: p.Timeout,
			ForceAttemptHTTP2:   false,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Snapshot is when each of the given exits was found unfit, for those that
// are: what a render leaving them out is made with, kept across a restart.
func (s *State) Snapshot(tags []string) map[string]time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]time.Time{}
	for _, t := range tags {
		if m := s.unfit[t]; m != nil {
			out[t] = m.since
		}
	}
	return out
}

// Restore starts from a snapshot: each exit unfit since then, with no clean
// round to its name.
func (s *State) Restore(snap map[string]time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unfit = map[string]*mark{}
	for t, since := range snap {
		s.unfit[t] = &mark{since: since}
	}
}
