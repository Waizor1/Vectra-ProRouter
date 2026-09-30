package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"vectra-controller-pro/internal/localctl"
)

// The channel the tuning sets: what each radio hears decides a FIXED channel
// — never auto, never one that needs radar detection in most countries (DFS,
// 52-144: the radio may go quiet for a minute on a radar hit) — within what
// its channel width can carry.
//
// optimize_wifi scans (never wifi_scan, which only returns the last scan): a
// person asking for the tuning has agreed to a Wi-Fi restart. Only radios
// that are up are scanned, one after the other, each with its share of one
// budget: a scan sweeps the channels, and the access point stalls while it
// does. 2.4 GHz is scanned with iwinfo; 5 GHz with `iw … scan freq` limited
// to 36-48 and 149-165 when iw is there (OpenWrt 24.10's filogic images do
// not ship it), else with iwinfo's full sweep.
//
//   - Networks weaker than -85 dBm do not count when scoring; a signal of
//     -255 or below (or 0 and above) is no signal.
//   - 2.4 GHz: 1, 6 or 11 (12 and 13 are refused for client compatibility),
//     within what the width allows: HT40+ needs a channel up to 9 (1 or 6),
//     HT40- one from 5 (6 or 11). Each is scored by what it would share: every
//     network within ±4 channels, its power (mW from dBm) times the overlap
//     (1 − |Δ|/5). The lowest wins; a tie keeps the channel the radio is on.
//   - 5 GHz: the blocks 36-48 and 149-161 (149-165 at 20 MHz), scored by the
//     networks inside each; at 160 MHz only 36-48 (the span 36-64 carries no
//     DFS flag in Panama's rules). The less busy block wins, but the block
//     the radio is on is kept unless the other scores below 2/3 of it; on
//     auto a tie goes to 36. Within the block: at 20 MHz the least busy
//     channel; at 40, 80 and 160 MHz the channel the radio is on if it is in
//     the block, else the block's first.
//   - A radio that was not scanned (off), heard nothing twice (empty) or
//     could not scan (failed) keeps its fixed channel when that is valid for
//     its width, else 6 (2.4 GHz) or 36 (5 GHz). A radio carrying a mesh or
//     ad-hoc interface always keeps its channel: its peers are on it.

// ScanBudget is the one budget for every radio's scan, split among them.
const ScanBudget = 12 * time.Second

// Why a radio has no scan (RadioScan.Err).
const (
	ScanOff    = "off"    // not up, or no access point up on it: nothing to scan with
	ScanEmpty  = "empty"  // it heard nothing, twice
	ScanFailed = "failed" // the scan failed, or netifd could not say whether the radio is up
)

// Neighbour is a network a radio heard.
type Neighbour struct {
	Channel int
	Signal  int // dBm
}

// ChannelUse is what a radio heard on one channel.
type ChannelUse struct {
	Channel   int `json:"channel"`
	Networks  int `json:"networks"`
	Strongest int `json:"strongest"` // dBm
}

// RadioScan is one radio's scan and the channel it recommends.
type RadioScan struct {
	Device      string       `json:"device"`
	Band        string       `json:"band"`
	Current     int          `json:"current"`     // the channel it is on; 0 unknown
	Recommended int          `json:"recommended"` // 0: none (a mesh radio on auto)
	Networks    int          `json:"networks"`
	Channels    []ChannelUse `json:"channels"` // where something was heard, ascending
	Err         string       `json:"error"`    // "", ScanOff, ScanEmpty, ScanFailed
}

// ScanResult is one scan of every radio: what wifi_scan returns until the
// next optimize_wifi.
type ScanResult struct {
	Radios    []RadioScan `json:"radios"`
	ScannedAt time.Time   `json:"scannedAt"`
}

// LoadScan is the last scan, nil when there has been none since boot.
func LoadScan(env Env) *ScanResult {
	raw, err := os.ReadFile(env.WifiScan)
	if err != nil {
		return nil
	}
	var s ScanResult
	if json.Unmarshal(raw, &s) != nil {
		return nil
	}
	return &s
}

func saveScan(env Env, s ScanResult) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return localctl.WriteFileAtomic(env.WifiScan, raw, 0o644)
}

// scanRadios scans the radios that are up, one after the other within
// ScanBudget, and says what each should use.
func scanRadios(ctx context.Context, env Env, w Wifi, status map[string]radioStatus) ScanResult {
	res := ScanResult{ScannedAt: env.Now().UTC()}
	deadline := env.Now().Add(ScanBudget)
	left := 0
	for _, r := range w.Radios {
		if s := status[r.Device]; s.Up && s.APIfname != "" {
			left++
		}
	}
	for _, r := range w.Radios {
		s := RadioScan{Device: r.Device, Band: r.Band, Current: r.Channel, Channels: []ChannelUse{}}
		st := status[r.Device]
		if !st.Up || st.APIfname == "" {
			s.Err, s.Recommended = ScanOff, keepOrDefault(r)
			if status == nil && r.radioOn {
				s.Err = ScanFailed // netifd did not answer: it may well be up
			}
			res.Radios = append(res.Radios, s)
			continue
		}
		share := deadline.Sub(env.Now()) / time.Duration(left)
		left--
		c, cancel := context.WithTimeout(ctx, share)
		var info struct {
			Channel int `json:"channel"`
		}
		if iwinfo(c, env, "info", st.APIfname, time.Second, &info) == nil && info.Channel > 0 {
			s.Current = info.Channel
		}
		heard, err := scanOnce(c, env, r, st.APIfname)
		if err == nil && len(heard) == 0 {
			heard, err = scanOnce(c, env, r, st.APIfname)
		}
		cancel()
		switch {
		case err != nil:
			s.Err, s.Recommended = ScanFailed, keepOrDefault(r)
		case len(heard) == 0:
			s.Err, s.Recommended = ScanEmpty, keepOrDefault(r)
		default:
			s.Networks, s.Channels = len(heard), channelUse(heard)
			s.Recommended = recommendFor(r, heard)
		}
		res.Radios = append(res.Radios, s)
	}
	return res
}

// recommendFor is the channel for r having heard these networks.
func recommendFor(r Radio, heard []Neighbour) int {
	if r.Mesh {
		return r.Channel
	}
	return Recommend(r.Band, r.HTMode, r.Channel, heard)
}

// keepOrDefault is the channel for a radio with no scan to go by: its own
// fixed channel when valid for its width (a mesh radio's always), else 6 on
// 2.4 GHz, 36 on 5 GHz.
func keepOrDefault(r Radio) int {
	if r.Mesh || r.Channel > 0 && ValidChannel(r.Band, r.HTMode, r.Channel) == nil {
		return r.Channel
	}
	switch r.Band {
	case "2g":
		return 6
	case "5g":
		return 36
	}
	return 0
}

// The 5 GHz channels iw is asked to scan, in MHz: 36-48 and 149-165.
var iwFreqs5g = []string{"5180", "5200", "5220", "5240", "5745", "5765", "5785", "5805", "5825"}

// scanOnce is one scan of r through ifn.
func scanOnce(ctx context.Context, env Env, r Radio, ifn string) ([]Neighbour, error) {
	if r.Band == "5g" {
		args := append(append([]string{"dev", ifn, "scan", "freq"}, iwFreqs5g...), "ap-force")
		if out, err := env.Output(ctx, "iw", args...); err == nil {
			return parseIwScan(out), nil
		}
		// No iw, or no freq list: iwinfo's full sweep.
	}
	var res struct {
		Results []struct {
			Channel int `json:"channel"`
			Signal  int `json:"signal"`
		} `json:"results"`
	}
	budget := time.Second
	if d, ok := ctx.Deadline(); ok && time.Until(d) > budget {
		budget = time.Until(d)
	}
	if err := iwinfo(ctx, env, "scan", ifn, budget, &res); err != nil {
		return nil, err
	}
	heard := make([]Neighbour, 0, len(res.Results))
	for _, n := range res.Results {
		if n.Channel > 0 {
			heard = append(heard, Neighbour{Channel: n.Channel, Signal: n.Signal})
		}
	}
	return heard, nil
}

// parseIwScan reads `iw dev … scan`: a "BSS" line per network, its "freq:"
// (MHz) and "signal:" (dBm) indented below it.
func parseIwScan(out []byte) []Neighbour {
	var heard []Neighbour
	var cur *Neighbour
	for _, line := range strings.Split(string(out), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "BSS "):
			heard = append(heard, Neighbour{Signal: noSignal})
			cur = &heard[len(heard)-1]
		case cur == nil:
		case strings.HasPrefix(t, "freq:"):
			if f, err := strconv.ParseFloat(strings.TrimSpace(t[len("freq:"):]), 64); err == nil {
				cur.Channel = channelOfFreq(int(math.Round(f)))
			}
		case strings.HasPrefix(t, "signal:"):
			if fs := strings.Fields(t[len("signal:"):]); len(fs) > 0 {
				if s, err := strconv.ParseFloat(fs[0], 64); err == nil {
					cur.Signal = int(math.Round(s))
				}
			}
		}
	}
	out2 := heard[:0]
	for _, n := range heard {
		if n.Channel > 0 {
			out2 = append(out2, n)
		}
	}
	return out2
}

// channelOfFreq is the 2.4 or 5 GHz channel of a frequency in MHz; 0 for
// anything else.
func channelOfFreq(f int) int {
	switch {
	case f == 2484:
		return 14
	case f >= 2412 && f <= 2472:
		return (f - 2407) / 5
	case f >= 5160 && f <= 5885:
		return (f - 5000) / 5
	}
	return 0
}

// noSignal marks a network whose signal was not reported.
const noSignal = -256

// known: a signal was reported (iwinfo gives 0 or -256 and below for none).
func known(dBm int) bool { return dBm < 0 && dBm > -255 }

// scored: a network strong enough to count when choosing a channel.
func scored(n Neighbour) bool { return known(n.Signal) && n.Signal >= -85 }

func channelUse(heard []Neighbour) []ChannelUse {
	by := map[int]*ChannelUse{}
	for _, n := range heard {
		c := by[n.Channel]
		if c == nil {
			c = &ChannelUse{Channel: n.Channel, Strongest: -100}
			by[n.Channel] = c
		}
		c.Networks++
		if known(n.Signal) && n.Signal > c.Strongest {
			c.Strongest = n.Signal
		}
	}
	out := make([]ChannelUse, 0, len(by))
	for _, c := range by {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Channel < out[j].Channel })
	return out
}

// mW is a signal's power.
func mW(dBm int) float64 { return math.Pow(10, float64(dBm)/10) }

// Candidates2g are the 2.4 GHz channels the tuning chooses among for htmode.
func candidates2g(htmode string) []int {
	switch strings.ToUpper(strings.TrimSpace(htmode)) {
	case "HT40+":
		return []int{1, 6}
	case "HT40-":
		return []int{6, 11}
	}
	return []int{1, 6, 11}
}

// blocks5g are the 5 GHz blocks for a channel width: 36-48 and 149-161,
// with 165 at 20 MHz; 36-48 alone at 160 MHz and above.
func blocks5g(width int) [][]int {
	low := []int{36, 40, 44, 48}
	switch {
	case width >= 160:
		return [][]int{low}
	case width == 20:
		return [][]int{low, {149, 153, 157, 161, 165}}
	}
	return [][]int{low, {149, 153, 157, 161}}
}

// Recommend is the channel for a radio of band running htmode, set to
// current (0: auto), that heard these networks; 0 for a band with no rules.
func Recommend(band, htmode string, current int, heard []Neighbour) int {
	switch band {
	case "2g":
		best, bestScore := 0, 0.0
		for _, c := range candidates2g(htmode) {
			var s float64
			for _, n := range heard {
				if d := abs(n.Channel - c); d <= 4 && scored(n) {
					s += mW(n.Signal) * (1 - float64(d)/5)
				}
			}
			if best == 0 || s < bestScore || (s == bestScore && c == current) {
				best, bestScore = c, s
			}
		}
		return best
	case "5g":
		width := widthOf(htmode)
		blocks := blocks5g(width)
		load := func(chans []int) float64 {
			var s float64
			for _, n := range heard {
				if scored(n) && containsInt(chans, n.Channel) {
					s += mW(n.Signal)
				}
			}
			return s
		}
		pick := 0
		cur := -1
		for i, b := range blocks {
			if containsInt(b, current) {
				cur = i
			}
		}
		if cur >= 0 {
			pick = cur
			for i, b := range blocks {
				if i != cur && load(b) < load(blocks[cur])*2/3 && load(b) < load(blocks[pick]) {
					pick = i
				}
			}
		} else {
			for i, b := range blocks {
				if load(b) < load(blocks[pick]) {
					pick = i
				}
			}
		}
		block := blocks[pick]
		if width != 20 {
			if containsInt(block, current) {
				return current
			}
			return block[0]
		}
		best, bestScore := 0, 0.0
		for _, c := range block {
			s := load([]int{c})
			if best == 0 || s < bestScore || (s == bestScore && c == current) {
				best, bestScore = c, s
			}
		}
		return best
	}
	return 0
}

// ValidChannel is whether the tuning may set ch on a radio of band running
// htmode.
func ValidChannel(band, htmode string, ch int) error {
	switch band {
	case "2g":
		if ch == 12 || ch == 13 {
			return fmt.Errorf("channel %d is refused for client compatibility: some clients do not see 12 and 13", ch)
		}
		lo, hi := 1, 11
		switch strings.ToUpper(strings.TrimSpace(htmode)) {
		case "HT40+":
			hi = 9
		case "HT40-":
			lo = 5
		}
		if ch < lo || ch > hi {
			return fmt.Errorf("channel %d is not one %s can use on 2.4 GHz (%d-%d)", ch, orUnset(htmode), lo, hi)
		}
		return nil
	case "5g":
		width := widthOf(htmode)
		switch {
		case ch >= 52 && ch <= 144:
			return fmt.Errorf("channel %d needs radar detection (DFS) in most countries; the tuning uses 36-48 or 149-165", ch)
		case width >= 160 && ch >= 149 && ch <= 165:
			return fmt.Errorf("channel %d cannot carry %d MHz: at %d MHz only 36-48", ch, width, width)
		}
		for _, b := range blocks5g(width) {
			if containsInt(b, ch) {
				return nil
			}
		}
		if ch == 165 {
			return fmt.Errorf("channel 165 is 20 MHz only; this radio runs %s", orUnset(htmode))
		}
		return fmt.Errorf("channel %d is not one the tuning uses on 5 GHz (36-48, 149-165)", ch)
	}
	return errors.New("the tuning has no channel rules for this band")
}

func orUnset(htmode string) string {
	if htmode == "" {
		return "an unset htmode"
	}
	return htmode
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
