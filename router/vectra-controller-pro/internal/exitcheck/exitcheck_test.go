package exitcheck

import (
	"bufio"
	"context"
	"crypto/x509"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"vectra-controller-pro/internal/coreengine/xray"
)

var t0 = time.Date(2026, 9, 30, 6, 0, 0, 0, time.UTC)

func ok(tag string) Result { return Result{Tag: tag, Control: true, Blocked: []bool{true}} }
func filtered(tag string) Result {
	return Result{Tag: tag, Control: true, Blocked: []bool{false, false, false}}
}
func dead(tag string) Result { return Result{Tag: tag} }

// 1111, 2026-09-30: bridge-us5 answered Google and the observatory, and no
// blocked site at all; its neighbours answered them all.
func TestAnExitThatFailsEveryBlockedSiteIsUnfit(t *testing.T) {
	var s State
	if !s.Round([]Result{ok("bridge-de5"), filtered("bridge-us5"), ok("bridge-nl5")}, t0) {
		t.Fatal("the round changed nothing")
	}
	if got := s.Unfit(); !reflect.DeepEqual(got, []string{"bridge-us5"}) {
		t.Fatalf("unfit %v", got)
	}
	if !s.Since("bridge-us5").Equal(t0) {
		t.Fatalf("since %v", s.Since("bridge-us5"))
	}
}

// With no exit carrying a blocked site there is nothing to compare against:
// the sites may be down, or the router's own network — nobody is judged.
func TestNobodyIsJudgedWhenNoExitCarriesABlockedSite(t *testing.T) {
	var s State
	if s.Round([]Result{filtered("bridge-de5"), filtered("bridge-us5")}, t0) || len(s.Unfit()) != 0 {
		t.Fatalf("judged without a witness: %v", s.Unfit())
	}
}

// An exit that does not answer the neutral URL is dead, not filtered: that is
// the observatory's to judge.
func TestADeadExitIsNotUnfit(t *testing.T) {
	var s State
	s.Round([]Result{ok("bridge-de5"), dead("bridge-us5")}, t0)
	if len(s.Unfit()) != 0 {
		t.Fatalf("unfit %v", s.Unfit())
	}
}

// One blocked site answering is enough: a single site can be down.
func TestAnExitThatCarriesOneBlockedSiteIsFit(t *testing.T) {
	var s State
	s.Round([]Result{ok("bridge-de5"), {Tag: "bridge-us5", Control: true, Blocked: []bool{false, true, false}}}, t0)
	if len(s.Unfit()) != 0 {
		t.Fatalf("unfit %v", s.Unfit())
	}
}

// Back only after two clean rounds in a row — each change restarts xray, and
// a leg that flaps must not flap the router with it; a filtered round starts
// the count again.
func TestAnUnfitExitComesBackAfterTwoCleanRounds(t *testing.T) {
	var s State
	s.Round([]Result{ok("bridge-de5"), filtered("bridge-us5")}, t0)
	steps := []struct {
		r       Result
		changed bool
		unfit   []string
	}{
		{ok("bridge-us5"), false, []string{"bridge-us5"}},
		{filtered("bridge-us5"), false, []string{"bridge-us5"}},
		{ok("bridge-us5"), false, []string{"bridge-us5"}},
		{dead("bridge-us5"), false, []string{"bridge-us5"}},
		{ok("bridge-us5"), true, nil},
	}
	for i, st := range steps {
		ch := s.Round([]Result{ok("bridge-de5"), st.r}, t0.Add(time.Duration(i+1)*10*time.Minute))
		if ch != st.changed || !reflect.DeepEqual(s.Unfit(), st.unfit) {
			t.Fatalf("step %d: changed %v unfit %v, want %v %v", i, ch, s.Unfit(), st.changed, st.unfit)
		}
	}
}

// testProxy stands in for xray's probe inbound: an HTTP proxy whose account
// picks the exit. The origin's certificate names example.com, so the sites
// differ by port: the "us" exit drops every port but 443 at the handshake;
// every exit reaches the origin for the rest.
type testProxy struct {
	origin string
	mu     sync.Mutex
	seen   map[string][]string // exit -> hosts asked
}

func (p *testProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user := ""
	if a := r.Header.Get("Proxy-Authorization"); strings.HasPrefix(a, "Basic ") {
		b, _ := base64.StdEncoding.DecodeString(a[len("Basic "):])
		user, _, _ = strings.Cut(string(b), ":")
	}
	exit := map[string]string{xray.ExitProbeUser("bridge-us5"): "us", xray.ExitProbeUser("bridge-de5"): "de"}[user]
	if exit == "" || r.Method != http.MethodConnect {
		http.Error(w, "no", http.StatusProxyAuthRequired)
		return
	}
	_, port, _ := net.SplitHostPort(r.Host)
	p.mu.Lock()
	p.seen[exit] = append(p.seen[exit], port)
	p.mu.Unlock()
	c, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	defer c.Close()
	_, _ = c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
	if exit == "us" && port != "443" {
		return // the filter kills the handshake
	}
	up, err := net.Dial("tcp", p.origin)
	if err != nil {
		return
	}
	defer up.Close()
	go func() { _, _ = io.Copy(up, bufio.NewReader(c)) }()
	_, _ = io.Copy(c, up)
}

func TestTheProberAsksEachExitThroughItsOwnAccount(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer origin.Close()
	tp := &testProxy{origin: origin.Listener.Addr().String(), seen: map[string][]string{}}
	proxy := httptest.NewServer(tp)
	defer proxy.Close()

	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	p := Prober{
		Listen:   proxy.Listener.Addr().String(),
		Control:  "https://example.com/generate_204",
		Blocked:  []string{"https://example.com:8443/favicon.ico", "https://example.com:9443/favicon.ico"},
		Timeout:  3 * time.Second,
		Attempts: 2,
		Parallel: 2,
		RootCAs:  roots,
	}
	got := p.Round(context.Background(), []string{"bridge-de5", "bridge-us5", "bridge-xx9"})
	want := []Result{
		{Tag: "bridge-de5", Control: true, Blocked: []bool{true}},
		{Tag: "bridge-us5", Control: true, Blocked: []bool{false, false}},
		{Tag: "bridge-xx9", Control: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round:\n got %+v\nwant %+v", got, want)
	}
	// One request for a healthy exit: a blocked site answering says it is
	// alive and unfiltered. Only when it does not is the neutral URL asked —
	// dead or filtered? — and then the other sites; a failing URL is asked
	// twice before the next.
	if !reflect.DeepEqual(tp.seen["de"], []string{"8443"}) {
		t.Fatalf("de asked %v", tp.seen["de"])
	}
	// A "no" counts only once the exit is known up: after the neutral URL
	// answers, the first site is asked again.
	if !reflect.DeepEqual(tp.seen["us"], []string{"8443", "8443", "443", "8443", "8443", "9443", "9443"}) {
		t.Fatalf("us asked %v", tp.seen["us"])
	}
}

// A certificate the system does not trust is a site that did not answer: a
// filter's own page under a forged certificate lets nothing through.
func TestAForgedCertificateIsNoAnswer(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer origin.Close()
	tp := &testProxy{origin: origin.Listener.Addr().String(), seen: map[string][]string{}}
	proxy := httptest.NewServer(tp)
	defer proxy.Close()
	p := Prober{Listen: proxy.Listener.Addr().String(), Control: "https://example.com/", Timeout: 3 * time.Second, RootCAs: x509.NewCertPool()}
	if got := p.Round(context.Background(), []string{"bridge-de5"}); got[0].Control {
		t.Fatalf("an untrusted certificate answered: %+v", got[0])
	}
}

// Across a restart the router starts from what it knew: the render that
// comes up already leaves the unfit exits out, and they still need two clean
// rounds to come back.
func TestTheUnfitExitsSurviveARestart(t *testing.T) {
	var a State
	a.Round([]Result{ok("bridge-de5"), filtered("bridge-us5"), filtered("hy2-nl5")}, t0)
	snap := a.Snapshot([]string{"bridge-us5", "bridge-xx1"})
	if !reflect.DeepEqual(snap, map[string]time.Time{"bridge-us5": t0}) {
		t.Fatalf("snapshot %v", snap)
	}
	var b State
	b.Restore(snap)
	if !reflect.DeepEqual(b.Unfit(), []string{"bridge-us5"}) || !b.Since("bridge-us5").Equal(t0) {
		t.Fatalf("restored %v since %v", b.Unfit(), b.Since("bridge-us5"))
	}
	if b.Round([]Result{ok("bridge-de5"), ok("bridge-us5")}, t0.Add(time.Minute)) || !b.Round([]Result{ok("bridge-de5"), ok("bridge-us5")}, t0.Add(2*time.Minute)) {
		t.Fatal("a restored exit did not need two clean rounds")
	}
}

// stallProxy answers the neutral URL (:443), drops the first blocked site
// (:8443) at once, and holds every other connection open without a word.
type stallProxy struct{ origin string }

func (p *stallProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, port, _ := net.SplitHostPort(r.Host)
	c, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	defer c.Close()
	_, _ = c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
	switch port {
	case "443":
		up, err := net.Dial("tcp", p.origin)
		if err != nil {
			return
		}
		defer up.Close()
		go func() { _, _ = io.Copy(up, bufio.NewReader(c)) }()
		_, _ = io.Copy(c, up)
	case "8443":
		return
	default:
		time.Sleep(5 * time.Second)
	}
}

// Review of r26: a round cut off by its deadline said "no" for every site
// it never finished asking — an exit that answered the neutral URL and had
// its last sites cut was judged filtered. What a deadline cut is unjudged.
func TestARoundCutOffJudgesNothingItDidNotFinish(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer origin.Close()
	proxy := httptest.NewServer(&stallProxy{origin: origin.Listener.Addr().String()})
	defer proxy.Close()
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	p := Prober{
		Listen:   proxy.Listener.Addr().String(),
		Control:  "https://example.com/generate_204",
		Blocked:  []string{"https://example.com:8443/favicon.ico", "https://example.com:9443/favicon.ico"},
		Timeout:  4 * time.Second,
		Attempts: 1,
		RootCAs:  roots,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	got := p.Round(ctx, []string{"bridge-de5"})
	if got[0].filtered() {
		t.Fatalf("a round cut off mid-exit judged it filtered: %+v", got[0])
	}
	var s State
	if s.Round([]Result{ok("bridge-nl5"), got[0]}, t0); len(s.Unfit()) != 0 {
		t.Fatalf("unfit %v", s.Unfit())
	}
}

// flakyProxy loses the first connection to every port but 443 — a node
// restarting under the round (the stand, 2026-09-30: the healthy exit was
// asked for the blocked site while its node restarted, then the neutral URL
// once it was back, and was judged filtered) — and carries the rest.
type flakyProxy struct {
	origin string
	mu     sync.Mutex
	lost   map[string]bool
}

func (p *flakyProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, port, _ := net.SplitHostPort(r.Host)
	c, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	defer c.Close()
	_, _ = c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
	p.mu.Lock()
	first := port != "443" && !p.lost[port]
	p.lost[port] = true
	p.mu.Unlock()
	if first {
		return
	}
	up, err := net.Dial("tcp", p.origin)
	if err != nil {
		return
	}
	defer up.Close()
	go func() { _, _ = io.Copy(up, bufio.NewReader(c)) }()
	_, _ = io.Copy(c, up)
}

func TestABlockedSiteLostBeforeTheExitWasKnownUpIsAskedAgain(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer origin.Close()
	proxy := httptest.NewServer(&flakyProxy{origin: origin.Listener.Addr().String(), lost: map[string]bool{}})
	defer proxy.Close()
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	p := Prober{Listen: proxy.Listener.Addr().String(), Control: "https://example.com/generate_204",
		Blocked: []string{"https://example.com:8443/favicon.ico"}, Timeout: 3 * time.Second, Attempts: 1, RootCAs: roots}
	got := p.Round(context.Background(), []string{"bridge-de5"})
	if !got[0].fit() {
		t.Fatalf("a site lost once, before the exit was known up, judged the exit: %+v", got[0])
	}
}

// whereProxy sends each exit's account to an origin of its own country: the
// "us" exit egresses in Poland (1111, 2026-09-30: «Турция» and «ОАЭ» did).
type whereProxy struct{ origins map[string]string }

func (p *whereProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user := ""
	if a := r.Header.Get("Proxy-Authorization"); strings.HasPrefix(a, "Basic ") {
		b, _ := base64.StdEncoding.DecodeString(a[len("Basic "):])
		user, _, _ = strings.Cut(string(b), ":")
	}
	origin := p.origins[user]
	c, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	defer c.Close()
	_, _ = c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
	if origin == "" {
		return
	}
	up, err := net.Dial("tcp", origin)
	if err != nil {
		return
	}
	defer up.Close()
	go func() { _, _ = io.Copy(up, bufio.NewReader(c)) }()
	_, _ = io.Copy(c, up)
}

// Where an exit really leaves: the country a Cloudflare trace sees.
func TestLocateSaysWhereEachExitLeaves(t *testing.T) {
	trace := func(loc string) *httptest.Server {
		return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("fl=1\nh=www.cloudflare.com\nip=203.0.113.9\nloc=" + loc + "\ntls=TLSv1.3\n"))
		}))
	}
	pl, de := trace("PL"), trace("DE")
	defer pl.Close()
	defer de.Close()
	roots := x509.NewCertPool()
	roots.AddCert(pl.Certificate())
	roots.AddCert(de.Certificate())
	proxy := httptest.NewServer(&whereProxy{origins: map[string]string{
		xray.ExitProbeUser("bridge-tr5"): pl.Listener.Addr().String(),
		xray.ExitProbeUser("bridge-de5"): de.Listener.Addr().String(),
	}})
	defer proxy.Close()
	p := Prober{Listen: proxy.Listener.Addr().String(), Where: "https://example.com/cdn-cgi/trace", Timeout: 3 * time.Second, Parallel: 2, RootCAs: roots}
	got := p.Locate(context.Background(), []string{"bridge-tr5", "bridge-de5", "bridge-xx9"})
	if !reflect.DeepEqual(got, map[string]string{"bridge-tr5": "PL", "bridge-de5": "DE"}) {
		t.Fatalf("located %v", got)
	}
}

// Kept per exit with its age: asked again once a day, and only then.
func TestAnExitsCountryIsAskedAgainOnceADay(t *testing.T) {
	var s State
	if due := s.EgressDue([]string{"bridge-tr5", "bridge-de5"}, t0, 24*time.Hour); !reflect.DeepEqual(due, []string{"bridge-tr5", "bridge-de5"}) {
		t.Fatalf("due %v", due)
	}
	s.SetEgress([]string{"bridge-tr5", "bridge-de5"}, map[string]string{"bridge-tr5": "PL"}, t0)
	// The one the trace did not answer through: again after an hour, not
	// every round.
	if due := s.EgressDue([]string{"bridge-tr5", "bridge-de5"}, t0.Add(10*time.Minute), 24*time.Hour); len(due) != 0 {
		t.Fatalf("due after 10 minutes %v", due)
	}
	if due := s.EgressDue([]string{"bridge-tr5", "bridge-de5"}, t0.Add(time.Hour), 24*time.Hour); !reflect.DeepEqual(due, []string{"bridge-de5"}) {
		t.Fatalf("due %v", due)
	}
	if due := s.EgressDue([]string{"bridge-tr5"}, t0.Add(25*time.Hour), 24*time.Hour); !reflect.DeepEqual(due, []string{"bridge-tr5"}) {
		t.Fatalf("due after a day %v", due)
	}
	if !reflect.DeepEqual(s.Egress(), map[string]string{"bridge-tr5": "PL"}) {
		t.Fatalf("egress %v", s.Egress())
	}
}

// The review of r29–r31: an exit located once and later not answering the
// trace (its node down) was due again every round — a request each 10
// minutes, for good. A failed look waits an hour, whatever was known before,
// and the country known stays shown meanwhile.
func TestAKnownExitThatStopsAnsweringIsAskedHourly(t *testing.T) {
	var s State
	s.SetEgress([]string{"bridge-tr5"}, map[string]string{"bridge-tr5": "PL"}, t0)
	day := t0.Add(25 * time.Hour)
	if due := s.EgressDue([]string{"bridge-tr5"}, day, 24*time.Hour); !reflect.DeepEqual(due, []string{"bridge-tr5"}) {
		t.Fatalf("due after a day %v", due)
	}
	s.SetEgress([]string{"bridge-tr5"}, nil, day) // the trace did not answer through it
	if due := s.EgressDue([]string{"bridge-tr5"}, day.Add(10*time.Minute), 24*time.Hour); len(due) != 0 {
		t.Fatalf("asked again 10 minutes after a failed look: %v", due)
	}
	if due := s.EgressDue([]string{"bridge-tr5"}, day.Add(time.Hour), 24*time.Hour); !reflect.DeepEqual(due, []string{"bridge-tr5"}) {
		t.Fatalf("due an hour after a failed look %v", due)
	}
	if !reflect.DeepEqual(s.Egress(), map[string]string{"bridge-tr5": "PL"}) {
		t.Fatalf("the known country was lost: %v", s.Egress())
	}
}

// The review of r29–r31: Cloudflare says "XX" where it does not know the
// country and "T1" for Tor; neither is a country to name on the card.
func TestATraceKnowsACountryOnlyByItsLetters(t *testing.T) {
	for body, want := range map[string]string{
		"fl=1\nloc=PL\ntls=TLSv1.3\n": "PL",
		"loc=pl\n":                    "PL",
		"loc=XX\n":                    "",
		"loc=T1\n":                    "",
		"loc=\n":                      "",
		"loc=POL\n":                   "",
		"ip=203.0.113.9\n":            "",
	} {
		if got := traceCountry(body); got != want {
			t.Errorf("%q: %q, want %q", body, got, want)
		}
	}
}

// The review of r29–r31: kept in memory only, where each exit leaves was lost
// at every restart (the nightly reboot) — the card's «(выход: …)» gone until
// the first round, every exit asked again. It goes into the state file and
// comes back with its age: shown at once, asked again when its day is up.
func TestAnExitsCountryOutlivesARestart(t *testing.T) {
	var s State
	s.SetEgress([]string{"bridge-tr5", "bridge-de5"}, map[string]string{"bridge-tr5": "PL"}, t0)
	snap := s.EgressSnapshot()
	if !reflect.DeepEqual(snap, map[string]Located{"bridge-tr5": {CC: "PL", At: t0}}) {
		t.Fatalf("snapshot %v", snap)
	}
	var r State
	r.RestoreEgress(snap)
	if !reflect.DeepEqual(r.Egress(), map[string]string{"bridge-tr5": "PL"}) {
		t.Fatalf("restored egress %v", r.Egress())
	}
	if due := r.EgressDue([]string{"bridge-tr5"}, t0.Add(2*time.Hour), 24*time.Hour); len(due) != 0 {
		t.Fatalf("asked again after a restart, before its day: %v", due)
	}
	if due := r.EgressDue([]string{"bridge-tr5"}, t0.Add(25*time.Hour), 24*time.Hour); !reflect.DeepEqual(due, []string{"bridge-tr5"}) {
		t.Fatalf("due after its day %v", due)
	}
}
