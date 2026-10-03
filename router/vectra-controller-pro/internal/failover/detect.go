// Package failover decides when a node xray balances over has stopped
// answering — from what the kernel's conntrack already shows, at no probe
// traffic — and what to do about it (policy.go).
package failover

import (
	"fmt"
	"net/netip"
	"slices"
	"time"

	"vectra-controller-pro/internal/conntrack"
)

// A node is failing when at least UnansweredMin of xray's attempts to it, made
// since it last answered a new connection, have gone unanswered for
// UnansweredAge. A node still answering new connections is losing packets, not
// dead; one in use until the moment it died is judged in seconds — not once
// its last answer has aged.
const (
	UnansweredAge = 2 * time.Second
	UnansweredMin = 2
	// StormAttempts outstanding at once are a burst of the router's own —
	// a download's segments, a torrent client's trackers — that the node may
	// be pacing: answered late, not never. Then only StormAge of silence is
	// death: moving a burst to another node only takes it there.
	StormAttempts = 48 // a page load is 16+ dials: a dead node under ordinary load is judged in seconds
	StormAge      = 8 * time.Second
)

type attempt struct {
	tags  []string // every outbound dialling the endpoint
	first time.Time
}

// Detector keeps what it saw between scans: when each unanswered attempt
// first appeared, and when a node last had an answered connection appear.
type Detector struct {
	// Local says whether an address is the router's own: only connections
	// from it are xray's dials. A LAN device's connection to a node's
	// address — a phone running the provider's own app — is answered by
	// xray's transparent proxy on the router, whatever the node's state.
	// nil takes every connection.
	Local func(netip.Addr) bool

	attempts     map[string]attempt // by 5-tuple
	lastAnswered map[string]time.Time
	seenAnswered map[string]bool // answered 5-tuples of the last scan
}

func NewDetector() *Detector {
	return &Detector{attempts: map[string]attempt{}, lastAnswered: map[string]time.Time{}, seenAnswered: map[string]bool{}}
}

func tupleKey(e conntrack.Entry) string {
	return fmt.Sprintf("%s|%s|%d|%s|%d", e.Proto, e.Src, e.SPort, e.Dst, e.DPort)
}

// Observe takes one scan of the table. endpoints names the outbounds behind
// each node endpoint; other connections are not looked at.
func (d *Detector) Observe(now time.Time, entries []conntrack.Entry, endpoints Endpoints) {
	live := map[string]bool{}
	answered := map[string]bool{}
	for _, e := range entries {
		if d.Local != nil && !d.Local(e.Src.Unmap()) {
			continue
		}
		tags := endpoints[Endpoint{Proto: e.Proto, Addr: netip.AddrPortFrom(e.Dst.Unmap(), e.DPort)}]
		if len(tags) == 0 {
			continue
		}
		k := tupleKey(e)
		if e.Replied {
			answered[k] = true
			if !d.seenAnswered[k] {
				for _, t := range tags {
					d.lastAnswered[t] = now
				}
			}
			continue
		}
		live[k] = true
		if _, ok := d.attempts[k]; !ok {
			d.attempts[k] = attempt{tags: tags, first: now}
		}
	}
	for k := range d.attempts {
		if !live[k] {
			delete(d.attempts, k)
		}
	}
	d.seenAnswered = answered
}

// Keep forgets the last answer of every node but tags — the render's: it
// grew with every node the provider ever named. A node whose name does not
// resolve for now stays in the render, and keeps its answer. An empty list
// (no render read) forgets nothing.
func (d *Detector) Keep(tags []string) {
	if len(tags) == 0 {
		return
	}
	keep := make(map[string]bool, len(tags))
	for _, t := range tags {
		keep[t] = true
	}
	for t := range d.lastAnswered {
		if !keep[t] {
			delete(d.lastAnswered, t)
		}
	}
}

// Failing reports whether the node has stopped answering.
func (d *Detector) Failing(tag string, now time.Time) bool {
	last, answered := d.lastAnswered[tag]
	var ages []time.Duration
	for _, a := range d.attempts {
		if !slices.Contains(a.tags, tag) {
			continue
		}
		// First seen in the same scan as the last answer: which came first is
		// not known, and a node that died in use shows exactly that.
		if answered && a.first.Before(last) {
			continue
		}
		ages = append(ages, now.Sub(a.first))
	}
	age := UnansweredAge
	if len(ages) >= StormAttempts {
		age = StormAge
	}
	n := 0
	for _, a := range ages {
		if a >= age {
			n++
		}
	}
	return n >= UnansweredMin
}

// AnsweredSince reports whether an answered connection to the node first
// appeared at or after t.
func (d *Detector) AnsweredSince(tag string, t time.Time) bool {
	a, ok := d.lastAnswered[tag]
	return ok && !a.Before(t)
}

// FirstUnanswered is when the oldest unanswered attempt to the node still in
// the table first appeared; zero when there is none.
func (d *Detector) FirstUnanswered(tag string) time.Time {
	var first time.Time
	for _, a := range d.attempts {
		if slices.Contains(a.tags, tag) && (first.IsZero() || a.first.Before(first)) {
			first = a.first
		}
	}
	return first
}
