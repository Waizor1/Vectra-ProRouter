package failover

import (
	"net/netip"
	"testing"
	"time"

	"vectra-controller-pro/internal/conntrack"
)

func failingDetector(t0 time.Time) *Detector {
	d := NewDetector()
	e1, e2 := syn(1), syn(2)
	d.Observe(t0, []conntrack.Entry{e1, e2}, eps)
	d.Observe(t0.Add(2*time.Second), []conntrack.Entry{e1, e2}, eps)
	return d
}

var main3 = Balancer{Tag: "BL-MAIN", Members: []string{"bridge-de5", "bridge-nl5", "bridge-pl5"}, Principle: []string{"bridge-nl5"}}
var alive = map[string]Health{"bridge-de5": {true, 80}, "bridge-nl5": {true, 40}, "bridge-pl5": {true, 60}}

func TestAFailingPrincipleMovesToTheFastestLiveMember(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	acts := NewPolicy().Decide(t0.Add(2*time.Second), []Balancer{main3}, alive, failingDetector(t0))
	if len(acts) != 1 || acts[0].Target != "bridge-pl5" || acts[0].From != "bridge-nl5" || acts[0].Reason != "failing" {
		t.Fatalf("%+v", acts)
	}
}

func TestTheOwnersPinIsNeverTouched(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	b := main3
	b.OwnerPin, b.Override = "bridge-nl5", "bridge-nl5"
	if acts := NewPolicy().Decide(t0.Add(2*time.Second), []Balancer{b}, alive, failingDetector(t0)); len(acts) != 0 {
		t.Fatalf("%+v", acts)
	}
}

func TestSomebodyElsesOverrideIsLeftAlone(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	b := main3
	b.Override = "bridge-nl5" // set through the UI a moment ago, not yet in Overrides
	if acts := NewPolicy().Decide(t0.Add(2*time.Second), []Balancer{b}, alive, failingDetector(t0)); len(acts) != 0 {
		t.Fatalf("%+v", acts)
	}
}

func TestNoLiveCandidateMeansNoOverrideAndDownTime(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	dead := map[string]Health{"bridge-de5": {false, 0}, "bridge-nl5": {true, 40}, "bridge-pl5": {false, 0}}
	p := NewPolicy()
	if acts := p.Decide(t0.Add(2*time.Second), []Balancer{main3}, dead, failingDetector(t0)); len(acts) != 0 {
		t.Fatalf("%+v", acts)
	}
	if p.DownFor("BL-MAIN", t0.Add(62*time.Second)) < 60*time.Second {
		t.Fatalf("down %s", p.DownFor("BL-MAIN", t0.Add(62*time.Second)))
	}
	if p.DownFor("BL-OTHER", t0.Add(62*time.Second)) != 0 {
		t.Fatal("down time for a balancer never judged")
	}
}

func TestReleasedWhenTheNodeAnswersAgainAndMovedAgainAsSoonAsItFails(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	p := NewPolicy()
	det := failingDetector(t0)
	acts := p.Decide(t0.Add(2*time.Second), []Balancer{main3}, alive, det)
	p.Applied(acts[0], t0.Add(2*time.Second))
	p.Confirmed("BL-MAIN", true, t0.Add(3*time.Second))
	b := main3
	b.Override = acts[0].Target
	ok := syn(7)
	ok.Replied, ok.State = true, "ESTABLISHED"
	det.Observe(t0.Add(10*time.Second), []conntrack.Entry{ok}, eps)
	// It answers again 8 s after the move: the override holds — a node that
	// answers one moment and drops the next must not swing the balancer back
	// and forth every few seconds.
	if rel := p.Decide(t0.Add(10*time.Second), []Balancer{b}, alive, det); len(rel) != 0 {
		t.Fatalf("released within the hold: %+v", rel)
	}
	rel := p.Decide(t0.Add(32*time.Second), []Balancer{b}, alive, det)
	if len(rel) != 1 || rel[0].Target != "" || rel[0].Reason != "recovered" {
		t.Fatalf("%+v", rel)
	}
	p.Applied(rel[0], t0.Add(32*time.Second))
	b.Override = ""
	// Dead again: two new attempts unanswered for 2 s move it at once.
	det.Observe(t0.Add(33*time.Second), []conntrack.Entry{ok, syn(20), syn(21)}, eps)
	det.Observe(t0.Add(35*time.Second), []conntrack.Entry{ok, syn(20), syn(21)}, eps)
	if again := p.Decide(t0.Add(35*time.Second), []Balancer{b}, alive, det); len(again) != 1 || again[0].Target != "bridge-pl5" {
		t.Fatalf("not moved again: %+v", again)
	}
}

func TestReleasedWhenTheBalancerNoLongerPrefersTheDeadNode(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	p := NewPolicy()
	det := failingDetector(t0)
	acts := p.Decide(t0.Add(2*time.Second), []Balancer{main3}, alive, det)
	p.Applied(acts[0], t0.Add(2*time.Second))
	b := main3
	b.Override, b.Principle = acts[0].Target, []string{"bridge-de5"} // the observatory marked nl5 dead
	rel := p.Decide(t0.Add(4*time.Second), []Balancer{b}, alive, det)
	if len(rel) != 1 || rel[0].Target != "" || rel[0].Reason != "gone" {
		t.Fatalf("%+v", rel)
	}
}

func TestAFailedConfirmationTriesTheNextCandidate(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	p := NewPolicy()
	det := failingDetector(t0)
	first := p.Decide(t0.Add(2*time.Second), []Balancer{main3}, alive, det)
	p.Applied(first[0], t0.Add(2*time.Second))
	p.Confirmed("BL-MAIN", false, t0.Add(3*time.Second))
	b := main3
	b.Override = first[0].Target
	next := p.Decide(t0.Add(3*time.Second), []Balancer{b}, alive, det)
	if len(next) != 1 || next[0].Target != "bridge-de5" {
		t.Fatalf("%+v", next)
	}
}

// A new entry or a provider refresh restarts xray with other balancers: the
// watchdog forgets what it held for the ones that are gone.
func TestForgetDropsBalancersNoLongerInTheRender(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	p := NewPolicy()
	det := failingDetector(t0)
	for _, a := range p.Decide(t0.Add(2*time.Second), []Balancer{main3}, alive, det) {
		p.Applied(a, t0.Add(2*time.Second))
	}
	p.Forget(map[string]bool{"BL-OTHER": true})
	// xray restarted: no override any more, the old memory must not claim it.
	// Kept, that memory would release "its" override as gone; forgotten, the
	// override is somebody else's and left alone.
	b := main3
	b.Override, b.Principle = "bridge-pl5", []string{"bridge-de5"}
	if acts := p.Decide(t0.Add(3*time.Second), []Balancer{b}, alive, det); len(acts) != 0 {
		t.Fatalf("acted on a forgotten override: %+v", acts)
	}
}

// Every member failing: the watchdog lets go of its override rather than
// hold the balancer on a dead node — xray ignores its own choice and the
// fallback chain while an override is set — and counts the time down.
func TestAnOverrideWhoseNodeFailsTooWithNoOtherCandidateIsLetGo(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	pl := netip.MustParseAddrPort("203.0.113.7:443")
	eps2 := Endpoints{{Proto: "tcp", Addr: nl}: {"bridge-nl5"}, {Proto: "tcp", Addr: pl}: {"bridge-pl5"}}
	plSyn := func(sport uint16) conntrack.Entry {
		return conntrack.Entry{Proto: "tcp", State: "SYN_SENT", Src: netip.MustParseAddr("198.51.100.7"), Dst: pl.Addr(), SPort: sport, DPort: pl.Port()}
	}
	two := Balancer{Tag: "BL-MAIN", Members: []string{"bridge-nl5", "bridge-pl5"}, Principle: []string{"bridge-nl5"}}
	health := map[string]Health{"bridge-nl5": {true, 40}, "bridge-pl5": {true, 60}}
	p := NewPolicy()
	det := failingDetector(t0)
	acts := p.Decide(t0.Add(2*time.Second), []Balancer{two}, health, det)
	if len(acts) != 1 || acts[0].Target != "bridge-pl5" {
		t.Fatalf("%+v", acts)
	}
	p.Applied(acts[0], t0.Add(2*time.Second))
	b := two
	b.Override = "bridge-pl5"
	det.Observe(t0.Add(4*time.Second), []conntrack.Entry{syn(1), syn(2), plSyn(31), plSyn(32)}, eps2)
	det.Observe(t0.Add(6*time.Second), []conntrack.Entry{syn(1), syn(2), plSyn(31), plSyn(32)}, eps2)
	rel := p.Decide(t0.Add(6*time.Second), []Balancer{b}, health, det)
	if len(rel) != 1 || rel[0].Target != "" {
		t.Fatalf("held on a dead node: %+v", rel)
	}
	p.Applied(rel[0], t0.Add(6*time.Second))
	if p.DownFor("BL-MAIN", t0.Add(66*time.Second)) < 60*time.Second {
		t.Fatalf("down %s", p.DownFor("BL-MAIN", t0.Add(66*time.Second)))
	}
}

// xray refused the move (its API timed out): nothing changed there, so
// nothing is taken as done here — the move is proposed again, and xray's
// balancer is not mistaken for somebody else's.
func TestAMoveXrayRefusedIsProposedAgain(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	p := NewPolicy()
	det := failingDetector(t0)
	if acts := p.Decide(t0.Add(2*time.Second), []Balancer{main3}, alive, det); len(acts) != 1 {
		t.Fatalf("%+v", acts)
	}
	again := p.Decide(t0.Add(4*time.Second), []Balancer{main3}, alive, det)
	if len(again) != 1 || again[0].Target != "bridge-pl5" {
		t.Fatalf("not proposed again: %+v", again)
	}
	// The second move of a held balancer, refused: still the watchdog's.
	p.Applied(again[0], t0.Add(4*time.Second))
	b := main3
	b.Override = "bridge-pl5"
	plDead := syn(50)
	plDead.Dst = netip.MustParseAddr("203.0.113.7")
	plDead.DPort = 443
	eps3 := Endpoints{{Proto: "tcp", Addr: nl}: {"bridge-nl5"}, {Proto: "tcp", Addr: netip.MustParseAddrPort("203.0.113.7:443")}: {"bridge-pl5"}}
	pl2 := plDead
	pl2.SPort = 51
	det.Observe(t0.Add(6*time.Second), []conntrack.Entry{syn(1), syn(2), plDead, pl2}, eps3)
	det.Observe(t0.Add(8*time.Second), []conntrack.Entry{syn(1), syn(2), plDead, pl2}, eps3)
	for i := 0; i < 2; i++ { // refused, then proposed again
		next := p.Decide(t0.Add(time.Duration(8+2*i)*time.Second), []Balancer{b}, alive, det)
		if len(next) != 1 || next[0].Target != "bridge-de5" {
			t.Fatalf("try %d: %+v", i, next)
		}
	}
}

func TestAReleaseXrayRefusedIsProposedAgain(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	p := NewPolicy()
	det := failingDetector(t0)
	acts := p.Decide(t0.Add(2*time.Second), []Balancer{main3}, alive, det)
	p.Applied(acts[0], t0.Add(2*time.Second))
	b := main3
	b.Override, b.Principle = acts[0].Target, []string{"bridge-de5"}
	for i := 0; i < 2; i++ {
		rel := p.Decide(t0.Add(time.Duration(4+2*i)*time.Second), []Balancer{b}, alive, det)
		if len(rel) != 1 || rel[0].Target != "" || rel[0].Reason != "gone" {
			t.Fatalf("try %d: %+v", i, rel)
		}
	}
}

// The provider's «Авто» entry as served on 2026-09-30: BL-MAIN is one node,
// its fallback the entry's own stage (a loopback into the bridge balancer).
// Its node failing, there is no other member to move to: the balancer goes
// onto its fallback at once — xray itself would wait for the observatory to
// mark the node dead, rounds of probes away.
func TestABalancerWithNoOtherMemberGoesOntoItsFallbackAtOnce(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	one := Balancer{Tag: "BL-MAIN", Members: []string{"bridge-nl5"}, Principle: []string{"bridge-nl5"}, Fallback: "stage-bridge"}
	p := NewPolicy()
	det := failingDetector(t0)
	acts := p.Decide(t0.Add(2*time.Second), []Balancer{one}, alive, det)
	if len(acts) != 1 || acts[0].Target != "stage-bridge" || acts[0].From != "bridge-nl5" || acts[0].Reason != "fallback" {
		t.Fatalf("%+v", acts)
	}
	p.Applied(acts[0], t0.Add(2*time.Second))
	if d := p.DownFor("BL-MAIN", t0.Add(70*time.Second)); d != 0 {
		t.Fatalf("down %s while the fallback carries it", d)
	}
	// Held there while the node stays dead; handed back once it answers
	// again and the hold is over.
	b := one
	b.Override = "stage-bridge"
	if more := p.Decide(t0.Add(10*time.Second), []Balancer{b}, alive, det); len(more) != 0 {
		t.Fatalf("%+v", more)
	}
	ok := syn(7)
	ok.Replied, ok.State = true, "ESTABLISHED"
	det.Observe(t0.Add(40*time.Second), []conntrack.Entry{ok}, eps)
	rel := p.Decide(t0.Add(40*time.Second), []Balancer{b}, alive, det)
	if len(rel) != 1 || rel[0].Target != "" || rel[0].Reason != "recovered" {
		t.Fatalf("%+v", rel)
	}
}

// Moved to a member that then fails too, with none left: onto the fallback,
// not back to a dead choice.
func TestAnExhaustedOverrideGoesOntoTheFallbackWhenThereIsOne(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	pl := netip.MustParseAddrPort("203.0.113.7:443")
	eps2 := Endpoints{{Proto: "tcp", Addr: nl}: {"bridge-nl5"}, {Proto: "tcp", Addr: pl}: {"bridge-pl5"}}
	plSyn := func(sport uint16) conntrack.Entry {
		return conntrack.Entry{Proto: "tcp", State: "SYN_SENT", Src: netip.MustParseAddr("198.51.100.7"), Dst: pl.Addr(), SPort: sport, DPort: pl.Port()}
	}
	two := Balancer{Tag: "BL-MAIN", Members: []string{"bridge-nl5", "bridge-pl5"}, Principle: []string{"bridge-nl5"}, Fallback: "stage-bridge"}
	health := map[string]Health{"bridge-nl5": {true, 40}, "bridge-pl5": {true, 60}}
	p := NewPolicy()
	det := failingDetector(t0)
	acts := p.Decide(t0.Add(2*time.Second), []Balancer{two}, health, det)
	p.Applied(acts[0], t0.Add(2*time.Second))
	b := two
	b.Override = "bridge-pl5"
	det.Observe(t0.Add(4*time.Second), []conntrack.Entry{syn(1), syn(2), plSyn(31), plSyn(32)}, eps2)
	det.Observe(t0.Add(6*time.Second), []conntrack.Entry{syn(1), syn(2), plSyn(31), plSyn(32)}, eps2)
	next := p.Decide(t0.Add(6*time.Second), []Balancer{b}, health, det)
	if len(next) != 1 || next[0].Target != "stage-bridge" || next[0].Reason != "fallback" {
		t.Fatalf("%+v", next)
	}
}

// The entry as 1111 ran it on 2026-09-30: BL-MAIN one German node, its
// stages a German bridge and the whitelist levels. After a tracker storm the
// German address stopped answering the router at all (279 SYNs, none
// answered) while the entry's Belarusian nodes carried everything: the
// balancer and its whole reserve dead, other countries of the same entry
// alive — and nothing moved, for 26 minutes, until it was moved by hand.
var mainDE = Balancer{Tag: "BL-MAIN", Members: []string{"sticky-de5"}, Fallback: "stage-bridge",
	Chain: []string{"bridge-de5", "whitelist-lv1"}, Borrow: []string{"sticky-by5", "direct-by-raw"}}
var deDead = map[string]Health{"sticky-de5": {false, 0}, "bridge-de5": {false, 0}, "whitelist-lv1": {false, 0},
	"sticky-by5": {true, 359}, "direct-by-raw": {true, 268}}

func TestABalancerWhoseNodesAndReserveAreAllDeadBorrowsTheEntrysFastestLiveCountry(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	acts := NewPolicy().Decide(t0, []Balancer{mainDE}, deDead, NewDetector())
	if len(acts) != 1 || acts[0].Target != "direct-by-raw" || acts[0].Reason != "borrowed" {
		t.Fatalf("%+v", acts)
	}
}

// No word from the observatory is no verdict: xray right after a start has
// not probed yet, and borrowing then would move a healthy balancer.
func TestNothingIsBorrowedWithoutTheObservatorysWord(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	h := map[string]Health{"sticky-by5": {true, 359}, "direct-by-raw": {true, 268}}
	if acts := NewPolicy().Decide(t0, []Balancer{mainDE}, h, NewDetector()); len(acts) != 0 {
		t.Fatalf("%+v", acts)
	}
	// One node of the reserve alive: xray's own fallback carries it.
	h = map[string]Health{"sticky-de5": {false, 0}, "bridge-de5": {true, 90}, "whitelist-lv1": {false, 0}, "sticky-by5": {true, 359}}
	if acts := NewPolicy().Decide(t0, []Balancer{mainDE}, h, NewDetector()); len(acts) != 0 {
		t.Fatalf("borrowed while the reserve lives: %+v", acts)
	}
}

func TestABorrowIsReturnedWhenTheEntrysOwnNodeAnswersAgainNotSooner(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	p := NewPolicy()
	det := NewDetector()
	acts := p.Decide(t0, []Balancer{mainDE}, deDead, det)
	p.Applied(acts[0], t0)
	b := mainDE
	b.Override = "direct-by-raw"
	back := map[string]Health{"sticky-de5": {true, 70}, "bridge-de5": {false, 0}, "whitelist-lv1": {false, 0},
		"sticky-by5": {true, 359}, "direct-by-raw": {true, 268}}
	if acts := p.Decide(t0.Add(10*time.Second), []Balancer{b}, back, det); len(acts) != 0 {
		t.Fatalf("returned inside the hold: %+v", acts)
	}
	if acts := p.Decide(t0.Add(ReleaseHold), []Balancer{b}, deDead, det); len(acts) != 0 {
		t.Fatalf("returned to a dead entry: %+v", acts)
	}
	acts = p.Decide(t0.Add(ReleaseHold), []Balancer{b}, back, det)
	if len(acts) != 1 || acts[0].Target != "" || acts[0].Reason != "recovered" {
		t.Fatalf("%+v", acts)
	}
}

func TestABorrowedNodeThatDiesIsTradedForTheNextOne(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	p := NewPolicy()
	det := NewDetector()
	p.Applied(p.Decide(t0, []Balancer{mainDE}, deDead, det)[0], t0)
	b := mainDE
	b.Override = "direct-by-raw"
	h := map[string]Health{"sticky-de5": {false, 0}, "bridge-de5": {false, 0}, "whitelist-lv1": {false, 0},
		"sticky-by5": {true, 359}, "direct-by-raw": {false, 0}}
	acts := p.Decide(t0.Add(4*time.Second), []Balancer{b}, h, det)
	if len(acts) != 1 || acts[0].Target != "sticky-by5" || acts[0].Reason != "borrowed" {
		t.Fatalf("%+v", acts)
	}
}

// The watchdog moved the balancer onto its reserve and the request through
// it failed: with the reserve dead too, the entry's other countries are next —
// not a hold on a dead stage (1111, 03:42:47 → 03:45:09 direct).
func TestAFailedConfirmationOnADeadReserveBorrows(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	p := NewPolicy()
	det := NewDetector()
	p.Applied(Action{"BL-MAIN", "stage-bridge", "sticky-de5", "fallback"}, t0)
	p.Confirmed("BL-MAIN", false, t0)
	b := mainDE
	b.Override = "stage-bridge"
	acts := p.Decide(t0.Add(2*time.Second), []Balancer{b}, deDead, det)
	if len(acts) != 1 || acts[0].Target != "direct-by-raw" || acts[0].Reason != "borrowed" {
		t.Fatalf("%+v", acts)
	}
}

// Handed back only on the word that the entry's own path answers: no word
// about a node of it (the observatory has not probed it) is no evidence, and
// releasing on it would put the balancer back on a dead path (review of r23).
func TestABorrowIsReturnedOnlyOnWordThatTheOwnPathAnswers(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	p := NewPolicy()
	det := NewDetector()
	p.Applied(p.Decide(t0, []Balancer{mainDE}, deDead, det)[0], t0)
	b := mainDE
	b.Override = "direct-by-raw"
	noWord := map[string]Health{"sticky-de5": {false, 0}, "bridge-de5": {false, 0},
		"sticky-by5": {true, 359}, "direct-by-raw": {true, 268}} // whitelist-lv1: no word
	if acts := p.Decide(t0.Add(ReleaseHold+time.Second), []Balancer{b}, noWord, det); len(acts) != 0 {
		t.Fatalf("handed back without word of the own path: %+v", acts)
	}
}

// A borrowed node that took the move but carried nothing (its confirmation
// failed) is not borrowed again for a while: otherwise exhausted → released →
// borrowed again, every few seconds (review of r23).
func TestANodeThatFailedItsConfirmationIsNotBorrowedAgainAtOnce(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	p := NewPolicy()
	det := NewDetector()
	b := mainDE
	b.Borrow = []string{"direct-by-raw"}
	acts := p.Decide(t0, []Balancer{b}, deDead, det)
	if len(acts) != 1 || acts[0].Target != "direct-by-raw" {
		t.Fatalf("%+v", acts)
	}
	p.Applied(acts[0], t0)
	p.Confirmed("BL-MAIN", false, t0)
	b.Override = "direct-by-raw"
	acts = p.Decide(t0.Add(2*time.Second), []Balancer{b}, deDead, det)
	if len(acts) != 1 || acts[0].Target != "" || acts[0].Reason != "exhausted" {
		t.Fatalf("%+v", acts)
	}
	p.Applied(acts[0], t0.Add(2*time.Second))
	b.Override = ""
	if acts := p.Decide(t0.Add(4*time.Second), []Balancer{b}, deDead, det); len(acts) != 0 {
		t.Fatalf("borrowed the node that just failed its confirmation again: %+v", acts)
	}
	if acts := p.Decide(t0.Add(4*time.Minute), []Balancer{b}, deDead, det); len(acts) != 1 || acts[0].Target != "direct-by-raw" {
		t.Fatalf("never tried again: %+v", acts)
	}
}

// What the watchdog moved, for the owner's server card (spec decision 6):
// off which node, onto which, when and why — until it hands the balancer back.
func TestTheWatchdogSaysWhatItMoved(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	p := NewPolicy()
	if _, ok := p.Move("BL-MAIN"); ok {
		t.Fatal("a move before any")
	}
	p.Applied(Action{"BL-MAIN", "sticky-by5", "sticky-de5", "borrowed"}, t0)
	if m, ok := p.Move("BL-MAIN"); !ok || m != (Move{From: "sticky-de5", To: "sticky-by5", Reason: "borrowed", At: t0}) {
		t.Fatalf("move %+v %v", m, ok)
	}
	p.Applied(Action{"BL-MAIN", "", "sticky-de5", "recovered"}, t0.Add(time.Minute))
	if _, ok := p.Move("BL-MAIN"); ok {
		t.Fatal("a handed-back balancer still reported moved")
	}
}
