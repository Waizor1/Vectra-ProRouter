package failover

import (
	"slices"
	"sort"
	"time"
)

// Balancer is one xray balancer as the watchdog sees it at one moment.
type Balancer struct {
	Tag       string
	Members   []string // the outbound tags its selector matches
	Principle []string // what its strategy prefers now (api.BalancerInfo)
	Override  string   // what xray is pinned to now, "" if nothing
	OwnerPin  string   // the owner's own pin (localctl.Overrides.Pins)
	// Fallback is the balancer's fallbackTag: the entry's own next stage,
	// which xray takes only once its strategy has no live member left.
	Fallback string
	// Chain are the members of the balancers the fallback leads to, stage
	// by stage: the entry's own reserve for this traffic.
	Chain []string
	// Borrow are outbounds of the entry outside the balancer and its chain
	// that can carry its traffic when all of those are down: the entry's
	// other countries, never a Russian exit (it reaches nothing a VPN is
	// for). The watchdog's last resort; empty where it must not borrow.
	Borrow []string
}

// Health is the observatory's word on a node (api.Observation).
type Health struct {
	Alive   bool
	DelayMs int64
	// LastSeen is when the node last answered the observatory's probe; zero
	// when the observatory does not say.
	LastSeen time.Time
}

// Action re-points a balancer: Target "" releases the override.
type Action struct {
	Balancer string
	Target   string
	From     string // the node that failed
	Reason   string // failing | fallback | borrowed | recovered | gone | exhausted
}

// ReleaseHold is how long the watchdog keeps an override at least before the
// node it moved off, answering again, gets the balancer back: a node that
// answers one moment and drops the next must not swing it every few seconds.
// Moving off a failing node never waits.
const ReleaseHold = 30 * time.Second

// ConfirmHold is how long a node whose confirmation failed — it took the move
// and carried nothing — is not borrowed again: otherwise exhausted, released
// and borrowed again every few seconds.
const ConfirmHold = 3 * time.Minute

type override struct {
	target, from string // target: what xray holds now, as the watchdog set it
	at           time.Time
	tried        map[string]bool
	retry        bool // the confirmation through target failed
	borrowed     bool // target is another country of the entry (Balancer.Borrow)
	reason       string
}

// Move is the watchdog's own override of a balancer: what it moved off,
// onto what, when and why (Action.Reason) — until it hands the balancer back.
type Move struct {
	From, To, Reason string
	At               time.Time
}

// Move reports the watchdog's override of a balancer, if it holds one.
func (p *Policy) Move(balancer string) (Move, bool) {
	o := p.ours[balancer]
	if o == nil {
		return Move{}, false
	}
	return Move{From: o.from, To: o.target, Reason: o.reason, At: o.at}, true
}

// Policy remembers the overrides the watchdog set, so it only ever moves or
// releases its own, and how long a balancer has had no candidate at all.
type Policy struct {
	ours        map[string]*override
	downSince   map[string]time.Time
	unconfirmed map[string]time.Time // node → when a move to it carried nothing
}

func NewPolicy() *Policy {
	return &Policy{ours: map[string]*override{}, downSince: map[string]time.Time{}, unconfirmed: map[string]time.Time{}}
}

// avoid is skip plus the nodes whose confirmation failed within ConfirmHold.
func (p *Policy) avoid(skip map[string]bool, now time.Time) map[string]bool {
	out := map[string]bool{}
	for k, v := range skip {
		out[k] = v
	}
	for n, at := range p.unconfirmed {
		if now.Sub(at) < ConfirmHold {
			out[n] = true
		} else {
			delete(p.unconfirmed, n)
		}
	}
	return out
}

// down: the observatory holds the node dead, or its dials go unanswered.
func down(tag string, health map[string]Health, det *Detector, now time.Time) bool {
	h, ok := health[tag]
	return (ok && !h.Alive) || det.Failing(tag, now)
}

// ownPathDown: every node of the balancer and of its reserve is down, and the
// observatory has had its word on each — right after xray starts it has not,
// and borrowing then would move a healthy balancer.
func ownPathDown(b Balancer, health map[string]Health, det *Detector, now time.Time) bool {
	nodes := append(append([]string{}, b.Members...), b.Chain...)
	if len(nodes) == 0 {
		return false
	}
	for _, t := range nodes {
		if _, ok := health[t]; !ok || !down(t, health, det, now) {
			return false
		}
	}
	return true
}

// ownPathAlive: a node of the balancer or of its reserve is held alive by the
// observatory and answers its dials — the word a borrow is handed back on. No
// word about a node is no evidence.
func ownPathAlive(b Balancer, health map[string]Health, det *Detector, now time.Time) bool {
	for _, t := range append(append([]string{}, b.Members...), b.Chain...) {
		if h, ok := health[t]; ok && h.Alive && !det.Failing(t, now) {
			return true
		}
	}
	return false
}

// borrowable is the fastest live node of the entry's other countries, when
// the balancer's own path is down; "" otherwise.
func borrowable(b Balancer, health map[string]Health, det *Detector, now time.Time, skip map[string]bool) string {
	if len(b.Borrow) == 0 || !ownPathDown(b, health, det, now) {
		return ""
	}
	return candidate(Balancer{Members: b.Borrow}, health, det, now, skip)
}

// candidate is the fastest member the observatory holds alive that is not
// failing and not in skip; ties by the order of the members.
func candidate(b Balancer, health map[string]Health, det *Detector, now time.Time, skip map[string]bool) string {
	type c struct {
		tag   string
		delay int64
		i     int
	}
	var cs []c
	for i, m := range b.Members {
		h := health[m]
		if !h.Alive || skip[m] || det.Failing(m, now) {
			continue
		}
		cs = append(cs, c{m, h.DelayMs, i})
	}
	if len(cs) == 0 {
		return ""
	}
	sort.Slice(cs, func(a, b int) bool {
		if cs[a].delay != cs[b].delay {
			return cs[a].delay < cs[b].delay
		}
		return cs[a].i < cs[b].i
	})
	return cs[0].tag
}

// Decide looks at every balancer once and says what to change; nothing is
// taken as done until xray has done it (Applied). The owner's pins and
// overrides the watchdog did not set are never touched.
func (p *Policy) Decide(now time.Time, bals []Balancer, health map[string]Health, det *Detector) []Action {
	var acts []Action
	for _, b := range bals {
		if b.OwnerPin != "" {
			continue
		}
		o := p.ours[b.Tag]
		if o != nil && b.Override != o.target {
			// Changed behind the watchdog's back (the owner, a restart of
			// xray): no longer the watchdog's to keep.
			delete(p.ours, b.Tag)
			o = nil
		}
		if b.Override != "" && o == nil {
			continue
		}
		if o != nil && o.borrowed {
			switch {
			case o.retry || down(o.target, health, det, now):
				o.tried[o.target] = true
				o.retry = false
				if next := borrowable(b, health, det, now, p.avoid(o.tried, now)); next != "" {
					acts = append(acts, Action{b.Tag, next, o.from, "borrowed"})
				} else {
					acts = append(acts, Action{b.Tag, "", o.from, "exhausted"})
				}
			case now.Sub(o.at) >= ReleaseHold && ownPathAlive(b, health, det, now):
				// A node of the entry's own path answers again: its own
				// choice and reserve take the traffic back.
				acts = append(acts, Action{b.Tag, "", o.from, "recovered"})
			}
			continue
		}
		if o != nil {
			failing := det.Failing(o.target, now)
			switch {
			case o.retry || failing:
				o.tried[o.target] = true
				o.retry = false
				if next := candidate(b, health, det, now, o.tried); next != "" {
					acts = append(acts, Action{b.Tag, next, o.from, "failing"})
				} else if failing && b.Fallback != "" && o.target != b.Fallback {
					acts = append(acts, Action{b.Tag, b.Fallback, o.from, "fallback"})
				} else if bn := borrowable(b, health, det, now, p.avoid(o.tried, now)); bn != "" {
					// The reserve is down too: another country of the entry,
					// not a hold on a dead stage.
					acts = append(acts, Action{b.Tag, bn, o.from, "borrowed"})
				} else if failing {
					// Nothing left to move to. Held, the override would keep
					// xray on a dead node: it ignores its own choice and the
					// fallback chain while one is set.
					acts = append(acts, Action{b.Tag, "", o.from, "exhausted"})
				}
				// A failed confirmation with nothing else to try: the node
				// answers connections, so it stays.
			case !slices.Contains(b.Principle, o.from):
				// The observatory has marked it dead: the balancer avoids it
				// on its own now.
				acts = append(acts, Action{b.Tag, "", o.from, "gone"})
			case now.Sub(o.at) >= ReleaseHold && det.AnsweredSince(o.from, o.at) && !det.Failing(o.from, now):
				acts = append(acts, Action{b.Tag, "", o.from, "recovered"})
			}
			continue
		}
		if ownPathDown(b, health, det, now) {
			// The balancer and its whole reserve are down: xray has nowhere
			// to send this traffic. The entry's other countries carry it.
			from := ""
			if len(b.Members) > 0 {
				from = b.Members[0]
			}
			if next := borrowable(b, health, det, now, p.avoid(nil, now)); next != "" {
				acts = append(acts, Action{b.Tag, next, from, "borrowed"})
				continue
			}
			if len(b.Principle) == 0 {
				// Nothing to borrow either, and xray prefers nothing: down.
				if _, ok := p.downSince[b.Tag]; !ok {
					p.downSince[b.Tag] = now
				}
				continue
			}
		}
		failing := ""
		for _, m := range b.Principle {
			if det.Failing(m, now) {
				failing = m
				break
			}
		}
		if failing == "" {
			delete(p.downSince, b.Tag)
			continue
		}
		next := candidate(b, health, det, now, map[string]bool{failing: true})
		if next == "" && b.Fallback != "" {
			// No member to move to: the entry's own next stage, now — xray
			// would wait for the observatory to mark the node dead.
			acts = append(acts, Action{b.Tag, b.Fallback, failing, "fallback"})
			continue
		}
		if next == "" {
			if _, ok := p.downSince[b.Tag]; !ok {
				p.downSince[b.Tag] = now
			}
			continue
		}
		acts = append(acts, Action{b.Tag, next, failing, "failing"})
	}
	return acts
}

// Applied records an action xray has carried out. One it refused is not
// recorded, and Decide proposes it again.
func (p *Policy) Applied(a Action, now time.Time) {
	o := p.ours[a.Balancer]
	switch {
	case a.Target == "":
		delete(p.ours, a.Balancer)
		if a.Reason == "exhausted" {
			if _, ok := p.downSince[a.Balancer]; !ok {
				p.downSince[a.Balancer] = now
			}
		}
	case o != nil:
		o.target, o.at, o.reason = a.Target, now, a.Reason
		if a.Reason == "borrowed" {
			o.borrowed = true
		}
	default:
		delete(p.downSince, a.Balancer)
		p.ours[a.Balancer] = &override{target: a.Target, from: a.From, at: now, tried: map[string]bool{a.From: true},
			borrowed: a.Reason == "borrowed", reason: a.Reason}
	}
}

// Confirmed takes the result of the one request the watchdog sent through
// the balancer after moving it: a failure moves it on at the next Decide.
func (p *Policy) Confirmed(balancer string, ok bool, now time.Time) {
	if o := p.ours[balancer]; o != nil && !ok {
		o.retry = true
		p.unconfirmed[o.target] = now
	}
}

// DownFor is how long the balancer has had a failing preference and no
// candidate to move to; 0 when it has one.
func (p *Policy) DownFor(balancer string, now time.Time) time.Duration {
	if t, ok := p.downSince[balancer]; ok {
		return now.Sub(t)
	}
	return 0
}

// Forget drops what the watchdog remembers of balancers no longer in the
// render (a new entry, a provider refresh — xray restarted without overrides).
func (p *Policy) Forget(keep map[string]bool) {
	for tag := range p.ours {
		if !keep[tag] {
			delete(p.ours, tag)
		}
	}
	for tag := range p.downSince {
		if !keep[tag] {
			delete(p.downSince, tag)
		}
	}
}
