package failover

import (
	"net/netip"
	"testing"
	"time"

	"vectra-controller-pro/internal/conntrack"
)

var nl = netip.MustParseAddrPort("203.0.113.5:50055")
var eps = Endpoints{
	{Proto: "tcp", Addr: nl}: {"bridge-nl5"},
	{Proto: "tcp", Addr: netip.MustParseAddrPort("203.0.113.9:443")}: {"direct-de5"},
}

func syn(sport uint16) conntrack.Entry {
	return conntrack.Entry{Proto: "tcp", State: "SYN_SENT", Src: netip.MustParseAddr("198.51.100.7"), Dst: nl.Addr(), SPort: sport, DPort: nl.Port()}
}

func TestTwoUnansweredAttemptsTwoSecondsOldAreFailing(t *testing.T) {
	d := NewDetector()
	t0 := time.Unix(1790000000, 0)
	d.Observe(t0, []conntrack.Entry{syn(1), syn(2)}, eps)
	if d.Failing("bridge-nl5", t0.Add(time.Second)) {
		t.Fatal("failing before 2 s")
	}
	d.Observe(t0.Add(2*time.Second), []conntrack.Entry{syn(1), syn(2)}, eps)
	if !d.Failing("bridge-nl5", t0.Add(2*time.Second)) {
		t.Fatal("not failing after 2 s")
	}
	if d.Failing("direct-de5", t0.Add(2*time.Second)) {
		t.Fatal("another node judged")
	}
}

func TestAnAnsweredConnectionClearsIt(t *testing.T) {
	d := NewDetector()
	t0 := time.Unix(1790000000, 0)
	ok := syn(3)
	ok.Replied, ok.State = true, "ESTABLISHED"
	d.Observe(t0, []conntrack.Entry{syn(1), syn(2)}, eps)
	d.Observe(t0.Add(3*time.Second), []conntrack.Entry{syn(1), syn(2), ok}, eps)
	if d.Failing("bridge-nl5", t0.Add(3*time.Second)) {
		t.Fatal("failing with a fresh answered connection")
	}
	if !d.AnsweredSince("bridge-nl5", t0.Add(2*time.Second)) {
		t.Fatal("answer not recorded")
	}
}

func TestOneAttemptIsNotEnoughAndGoneAttemptsAreForgotten(t *testing.T) {
	d := NewDetector()
	t0 := time.Unix(1790000000, 0)
	d.Observe(t0, []conntrack.Entry{syn(1)}, eps)
	d.Observe(t0.Add(5*time.Second), []conntrack.Entry{syn(1)}, eps)
	if d.Failing("bridge-nl5", t0.Add(5*time.Second)) {
		t.Fatal("one attempt")
	}
	d.Observe(t0.Add(6*time.Second), nil, eps)
	d.Observe(t0.Add(7*time.Second), []conntrack.Entry{syn(9)}, eps)
	if d.Failing("bridge-nl5", t0.Add(7*time.Second)) {
		t.Fatal("forgotten attempts still counted")
	}
}

// An answered connection that stays open is one answer, not a fresh one on
// every scan: a node that answered once and then died is judged by its new
// attempts.
func TestALongLivedConnectionIsNotAFreshAnswer(t *testing.T) {
	d := NewDetector()
	t0 := time.Unix(1790000000, 0)
	old := syn(4)
	old.Replied, old.State = true, "ESTABLISHED"
	d.Observe(t0, []conntrack.Entry{old}, eps)
	d.Observe(t0.Add(12*time.Second), []conntrack.Entry{old, syn(1), syn(2)}, eps)
	d.Observe(t0.Add(14*time.Second), []conntrack.Entry{old, syn(1), syn(2)}, eps)
	if !d.Failing("bridge-nl5", t0.Add(14*time.Second)) {
		t.Fatal("an old open connection hid a dead node")
	}
}

func TestFirstUnansweredIsTheOldestAttempt(t *testing.T) {
	d := NewDetector()
	t0 := time.Unix(1790000000, 0)
	d.Observe(t0, []conntrack.Entry{syn(1)}, eps)
	d.Observe(t0.Add(3*time.Second), []conntrack.Entry{syn(1), syn(2)}, eps)
	if got := d.FirstUnanswered("bridge-nl5"); !got.Equal(t0) {
		t.Fatalf("%s", got)
	}
	if got := d.FirstUnanswered("direct-de5"); !got.IsZero() {
		t.Fatalf("%s", got)
	}
}

// A node in use right up to the moment it died: the last connection it
// answered first shows in the same scan as the first attempts it never
// answers. It is failing once two of those are two seconds old — a match
// cannot wait ten seconds for that last answer to age.
func TestANodeInUseUntilItDiedIsFailingInTwoSeconds(t *testing.T) {
	d := NewDetector()
	t0 := time.Unix(1790000000, 0)
	last := syn(5)
	last.Replied, last.State = true, "ESTABLISHED"
	d.Observe(t0, []conntrack.Entry{last, syn(1), syn(2)}, eps)
	d.Observe(t0.Add(2*time.Second), []conntrack.Entry{last, syn(1), syn(2)}, eps)
	if !d.Failing("bridge-nl5", t0.Add(2*time.Second)) {
		t.Fatal("not failing 2 s after its first unanswered attempts: waited for its last answer to age")
	}
}

// Attempts left from an earlier death stay in the table for two minutes. An
// answer since then says the node came back; only attempts after that answer
// can say it died again.
func TestOnlyAttemptsAfterTheLastAnswerCount(t *testing.T) {
	d := NewDetector()
	t0 := time.Unix(1790000000, 0)
	back := syn(7)
	back.Replied, back.State = true, "ESTABLISHED"
	d.Observe(t0, []conntrack.Entry{syn(1), syn(2)}, eps)
	d.Observe(t0.Add(60*time.Second), []conntrack.Entry{syn(1), syn(2), back}, eps)
	if d.Failing("bridge-nl5", t0.Add(60*time.Second)) {
		t.Fatal("attempts older than an answer outweighed it")
	}
	d.Observe(t0.Add(100*time.Second), []conntrack.Entry{syn(1), syn(2), back, syn(3), syn(4)}, eps)
	d.Observe(t0.Add(102*time.Second), []conntrack.Entry{syn(1), syn(2), back, syn(3), syn(4)}, eps)
	if !d.Failing("bridge-nl5", t0.Add(102*time.Second)) {
		t.Fatal("died again after it came back, not failing")
	}
}

// A LAN device's connection to a node's address — the owner's phone with the
// provider's own app — is answered by xray's transparent proxy on the router,
// whatever the node's state: not the node's answer, and not xray's attempt.
func TestTheLANsOwnConnectionsToANodeAreNotItsAnswers(t *testing.T) {
	d := NewDetector()
	router := netip.MustParseAddr("198.51.100.7")
	d.Local = func(a netip.Addr) bool { return a == router }
	t0 := time.Unix(1790000000, 0)
	phone := syn(40)
	phone.Src, phone.Replied, phone.State = netip.MustParseAddr("192.168.1.50"), true, "ESTABLISHED"
	lanTry := syn(41)
	lanTry.Src = netip.MustParseAddr("192.168.1.51")
	d.Observe(t0, []conntrack.Entry{syn(1), syn(2)}, eps)
	d.Observe(t0.Add(2*time.Second), []conntrack.Entry{syn(1), syn(2), phone}, eps)
	if !d.Failing("bridge-nl5", t0.Add(2*time.Second)) {
		t.Fatal("the phone's connection, answered by the router itself, hid a dead node")
	}
	if d.AnsweredSince("bridge-nl5", t0) {
		t.Fatal("the phone's connection taken for the node's answer")
	}
	d2 := NewDetector()
	d2.Local = d.Local
	d2.Observe(t0, []conntrack.Entry{syn(1), lanTry}, eps)
	d2.Observe(t0.Add(2*time.Second), []conntrack.Entry{syn(1), lanTry}, eps)
	if d2.Failing("bridge-nl5", t0.Add(2*time.Second)) {
		t.Fatal("a LAN device's attempt counted as xray's")
	}
}

// Outbounds that dial one endpoint share its fate; a UDP node (hysteria2) on
// a TCP node's port is judged apart — its unanswered QUIC is not theirs.
func TestOutboundsOnOneEndpointShareItsFateAndTransportsAreApart(t *testing.T) {
	d := NewDetector()
	t0 := time.Unix(1790000000, 0)
	shared := Endpoints{
		{Proto: "tcp", Addr: nl}: {"bridge-nl5", "sticky-nl5"},
		{Proto: "udp", Addr: nl}: {"hy2-nl5"},
	}
	quic := func(sport uint16) conntrack.Entry {
		return conntrack.Entry{Proto: "udp", Src: netip.MustParseAddr("198.51.100.7"), Dst: nl.Addr(), SPort: sport, DPort: nl.Port()}
	}
	d.Observe(t0, []conntrack.Entry{quic(7), quic(8)}, shared)
	d.Observe(t0.Add(2*time.Second), []conntrack.Entry{quic(7), quic(8)}, shared)
	if !d.Failing("hy2-nl5", t0.Add(2*time.Second)) || d.Failing("bridge-nl5", t0.Add(2*time.Second)) {
		t.Fatal("the UDP node's silence was put on the TCP nodes, or not on itself")
	}
	d.Observe(t0.Add(4*time.Second), []conntrack.Entry{syn(1), syn(2)}, shared)
	d.Observe(t0.Add(6*time.Second), []conntrack.Entry{syn(1), syn(2)}, shared)
	if !d.Failing("bridge-nl5", t0.Add(6*time.Second)) || !d.Failing("sticky-nl5", t0.Add(6*time.Second)) {
		t.Fatal("outbounds on one dead endpoint judged apart")
	}
}

// A burst of the router's own dials — a download manager's segments, a
// torrent client's trackers, all through one node — can outrun what the node
// takes from one address: it answers late, paced, not never. With many
// attempts outstanding at once only a longer silence is death; moving the
// burst to another node would only take it there (1111, 2026-09-30 03:42: a
// tracker storm moved BL-MAIN and then BL-BRIDGE off live nodes in 36 s, and
// the rescue took the router direct).
func TestABurstOfDialsIsGivenLongerBeforeTheNodeIsJudged(t *testing.T) {
	d := NewDetector()
	t0 := time.Unix(1790000000, 0)
	var burst []conntrack.Entry
	for i := 0; i < StormAttempts; i++ {
		burst = append(burst, syn(uint16(100+i)))
	}
	d.Observe(t0, burst, eps)
	d.Observe(t0.Add(3*time.Second), burst, eps)
	if d.Failing("bridge-nl5", t0.Add(3*time.Second)) {
		t.Fatal("a burst unanswered for 3 s was judged dead")
	}
	d.Observe(t0.Add(StormAge), burst, eps)
	if !d.Failing("bridge-nl5", t0.Add(StormAge)) {
		t.Fatalf("a burst unanswered for %s was not judged dead", StormAge)
	}
	// Fewer attempts than a burst: the node is judged in seconds, as ever.
	d2 := NewDetector()
	few := burst[:StormAttempts-1]
	d2.Observe(t0, few, eps)
	d2.Observe(t0.Add(UnansweredAge), few, eps)
	if !d2.Failing("bridge-nl5", t0.Add(UnansweredAge)) {
		t.Fatal("a few unanswered attempts were not judged in seconds")
	}
}

// A dead node under a household's ordinary load — a page load's worth of
// dials outstanding — is judged in seconds, as ever; only a storm waits.
func TestAPageLoadOfDialsIsNotAStorm(t *testing.T) {
	d := NewDetector()
	t0 := time.Unix(1790000000, 0)
	var page []conntrack.Entry
	for i := 0; i < 30; i++ {
		page = append(page, syn(uint16(200+i)))
	}
	d.Observe(t0, page, eps)
	d.Observe(t0.Add(UnansweredAge), page, eps)
	if !d.Failing("bridge-nl5", t0.Add(UnansweredAge)) {
		t.Fatal("30 dials outstanding to a dead node waited like a storm")
	}
}
