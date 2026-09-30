package routepolicy

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The golden files are PassWall2 26.8.10's own subscribe.lua run on the same
// UCI (refresh-before.uci) and the same feed, a local file in its place
// (`subscribe.lua start sub1 manual`, the data-plane stand's image), and
// `uci export passwall2` after it. Every credential in them is synthetic.

func readFeed(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// counter makes section names in order: n001, n002, ...
func counter() func() string {
	i := 0
	return func() string { i++; return fmt.Sprintf("n%03d", i) }
}

// nodeKey is what a node is, whatever its section's name.
func nodeKey(s Section) string {
	return s.Get("remarks") + "|" + s.Get("address") + "|" + s.Get("port")
}

// canon renders sections for comparison: names of subscription nodes
// replaced by what they are, references by what they point at.
func canon(secs []Section) []string {
	names := map[string]string{}
	for _, s := range secs {
		if s.Type == "nodes" && s.Get("add_mode") == "2" {
			names[s.Name] = "<" + nodeKey(s) + ">"
		}
	}
	name := func(v string) string {
		if n, ok := names[v]; ok {
			return n
		}
		return v
	}
	var out []string
	for _, s := range secs {
		var lines []string
		for k, v := range s.Options {
			lines = append(lines, fmt.Sprintf("  option %s=%s", k, name(v)))
		}
		for k, l := range s.Lists {
			for i, v := range l {
				lines = append(lines, fmt.Sprintf("  list %s[%d]=%s", k, i, name(v)))
			}
		}
		sort.Strings(lines)
		out = append(out, "config "+s.Type+" "+name(s.Name)+"\n"+strings.Join(lines, "\n"))
	}
	return out
}

func refreshAgainst(t *testing.T, feed, golden string) (ours, theirs []string, rep RefreshReport) {
	t.Helper()
	secs := readUCI(t, "refresh-before.uci")
	out, rep, err := Refresh(secs, "sub1", readFeed(t, feed), counter())
	if err != nil {
		t.Fatal(err)
	}
	// Written and read back: what lands in the file is what is compared.
	back, err := ParseUCI(Export(out))
	if err != nil {
		t.Fatalf("our export does not parse: %v", err)
	}
	return canon(back), canon(readUCI(t, golden)), rep
}

// Where no reference is in doubt, the refresh is PassWall's to the option:
// the same nodes from the same links (the keyword filter, empty values, the
// URL-decoding, IPv6 hosts, &amp;, the xhttp extra and its download
// address), in the same order, the slots on the same nodes — by name and
// address:port, by address:port alone after a rename, by name after a move —
// and the same md5.
func TestRefreshMatchesPassWall2(t *testing.T) {
	ours, theirs, rep := refreshAgainst(t, "refresh-feed-a.txt", "refresh-after-a.uci")
	if !reflect.DeepEqual(ours, theirs) {
		t.Fatalf("differs from PassWall2's own refresh:\n%s", sectionDiff(ours, theirs))
	}
	if rep.Nodes != 9 || len(rep.Skipped) != 1 || !strings.Contains(rep.Skipped[0], "Guise node") {
		t.Fatalf("report: %+v", rep)
	}
	how := map[string]string{}
	for _, r := range rep.Rebound {
		how[r.Ref] = r.How
	}
	want := map[string]string{
		"myshunt.WorldProxy": "name+address:port", "myshunt.YouTube": "name+address:port",
		"myshunt.Tiktok": "name+address:port", "myshunt.DiscordVoiceUdp": "address:port",
		"myshunt.Special": "name",
	}
	if !reflect.DeepEqual(how, want) {
		t.Fatalf("rebinding: %v, want %v", how, want)
	}
}

// Where PassWall re-points a slot badly, this does not, on purpose:
//   - the Netherlands moved to another bridge host and Germany took its old
//     one: PassWall's host-only match put the NL slots on Germany; the name
//     keeps them on the Netherlands;
//   - Finland is gone and there is no other Finnish node: PassWall pointed
//     its slot at the first node of the list as it was before the refresh —
//     a node it had just deleted, so the slot named nothing; this moves it to
//     the main slot's node (WorldProxy's, the Netherlands).
//
// Everything else is PassWall's.
func TestRefreshDiffersFromPassWall2OnlyWhereItFailsTheSlots(t *testing.T) {
	ours, theirs, rep := refreshAgainst(t, "refresh-feed-b.txt", "refresh-after-b.uci")
	fix := func(lines []string, from, to string) []string {
		out := append([]string(nil), lines...)
		for i := range out {
			out[i] = strings.ReplaceAll(out[i], from, to)
		}
		return out
	}
	nl := "<🇷🇺🇳🇱 Нидерланды|nl2.bridge.example|50055>"
	de := "<🇷🇺🇩🇪 Германия через мост|nl.bridge.example|50052>"
	for _, want := range []string{"option WorldProxy=" + nl, "option YouTube=" + nl, "option DiscordVoiceUdp=" + nl} {
		if !strings.Contains(strings.Join(ours, "\n"), want) {
			t.Errorf("ours lacks %s", want)
		}
	}
	for _, want := range []string{"option WorldProxy=" + de, "option DiscordVoiceUdp=nodeBY01"} {
		if !strings.Contains(strings.Join(theirs, "\n"), want) {
			t.Errorf("PassWall's golden lacks %s: the difference this test is about is not there", want)
		}
	}
	// Ours with PassWall's three choices put back must be PassWall's exactly.
	adj := fix(fix(ours, "option WorldProxy="+nl, "option WorldProxy="+de), "option YouTube="+nl, "option YouTube="+de)
	adj = fix(adj, "option DiscordVoiceUdp="+nl, "option DiscordVoiceUdp=nodeBY01")
	if !reflect.DeepEqual(adj, theirs) {
		t.Fatalf("beyond the three slots, differs from PassWall2:\n%s", sectionDiff(adj, theirs))
	}
	how := map[string]string{}
	for _, r := range rep.Rebound {
		how[r.Ref] = r.How
	}
	if how["myshunt.WorldProxy"] != "name" || how["myshunt.DiscordVoiceUdp"] != "the main slot's node" {
		t.Fatalf("rebinding: %v", how)
	}
}

// A slot whose node is gone from the feed — its host switched off on the
// panel — moves the way the owner chose: the same country by the same path,
// then by another, then the main slot's node, then «Авто Самый стабильный»,
// «Авто», the feed's first node. And a node on the old host:port that names
// another country is never taken for the old one.
func TestAVanishedNodesSlotMovesTheOwnersWay(t *testing.T) {
	const policy = `
config global 'g'
	option node 'shunt'

config shunt_rules 'WorldProxy'
	option remarks 'WorldProxy'
config shunt_rules 'YouTube'
	option remarks 'YouTube'
config shunt_rules 'Tiktok'
	option remarks 'Tiktok'
config shunt_rules 'Discord'
	option remarks 'Discord'
config shunt_rules 'Special'
	option remarks 'Special'

config nodes 'shunt'
	option type 'Xray'
	option protocol '_shunt'
	option WorldProxy 'oNL'
	option YouTube 'oFI'
	option Tiktok 'oBY'
	option Discord 'oKZ'
	option Special 'oFR'

config subscribe_list 's'
	option remark 'Sub'
`
	node := func(name, remarks, addr, port string) string {
		return "config nodes '" + name + "'\n\toption type 'Xray'\n\toption protocol 'vless'\n\toption add_mode '2'\n\toption group 'Sub'\n" +
			"\toption remarks '" + remarks + "'\n\toption address '" + addr + "'\n\toption port '" + port + "'\n"
	}
	before := policy + node("oNL", "🇷🇺🇳🇱 Нидерланды", "h1.example", "50055") + node("oFI", "🇷🇺🇫🇮 Финляндия", "h2.example", "50054") +
		node("oBY", "🇧🇾 Беларусь", "by.example", "443") + node("oKZ", "🇷🇺🇰🇿 Казахстан", "h3.example", "50058") +
		node("oFR", "🇷🇺🇫🇷 Франция", "h4.example", "50057")
	link := func(remarks, addr, port string) string {
		return "vless://00000000-0000-4000-8000-000000000001@" + addr + ":" + port + "?type=tcp&security=none#" + url.PathEscape(remarks)
	}
	feed := func(lines ...string) []byte {
		return []byte(base64.StdEncoding.EncodeToString([]byte(strings.Join(lines, "\n") + "\n")))
	}
	secs, err := ParseUCI(before)
	if err != nil {
		t.Fatal(err)
	}
	run := func(body []byte) (map[string]string, map[string]string) {
		t.Helper()
		out, rep, err := Refresh(secs, "s", body, counter())
		if err != nil {
			t.Fatal(err)
		}
		get := func(sec, opt string) string {
			for _, s := range out {
				if s.Name == sec {
					return s.Get(opt)
				}
			}
			return ""
		}
		to := map[string]string{}
		for _, slot := range []string{"WorldProxy", "YouTube", "Tiktok", "Discord", "Special"} {
			to[slot] = get(get("shunt", slot), "remarks") + "@" + get(get("shunt", slot), "address")
		}
		how := map[string]string{}
		for _, r := range rep.Rebound {
			how[strings.TrimPrefix(r.Ref, "shunt.")] = r.How
		}
		return to, how
	}

	to, how := run(feed(
		// NL: the old host:port now carries Germany; NL direct, then another
		// RU-NL bridge — the bridge is the same path.
		link("🇷🇺🇩🇪 Германия", "h1.example", "50055"),
		link("🇳🇱 Нидерланды", "nl.example", "443"),
		link("🇷🇺🇳🇱 Нидерланды 2", "h9.example", "50065"),
		// FI only direct; BY and KZ gone; France renamed on its host:port.
		link("🇫🇮 Финляндия", "fi.example", "443"),
		link("🇫🇷 Франция (новая)", "h4.example", "50057"),
		link("🇷🇺🇪🇺 Авто Самый быстрый", "a1.example", "443"),
		link("🇷🇺🇪🇺 Авто Самый стабильный", "a2.example", "443"),
	))
	want := map[string][2]string{
		"WorldProxy": {"🇷🇺🇳🇱 Нидерланды 2@h9.example", "same country, same path"},
		"YouTube":    {"🇫🇮 Финляндия@fi.example", "same country"},
		"Tiktok":     {"🇷🇺🇳🇱 Нидерланды 2@h9.example", "the main slot's node"},
		"Discord":    {"🇷🇺🇳🇱 Нидерланды 2@h9.example", "the main slot's node"},
		"Special":    {"🇫🇷 Франция (новая)@h4.example", "address:port"},
	}
	for slot, w := range want {
		if to[slot] != w[0] || how[slot] != w[1] {
			t.Errorf("%s -> %s by %q, want %s by %q", slot, to[slot], how[slot], w[0], w[1])
		}
	}

	// The main slot's own node gone, nothing of its country: «Авто Самый
	// стабильный»; and the other slots follow it there.
	to, how = run(feed(
		link("🇷🇺🇩🇪 Германия", "h1.example", "50055"),
		link("🇷🇺🇪🇺 Авто Самый быстрый", "a1.example", "443"),
		link("🇷🇺🇪🇺 Авто Самый стабильный", "a2.example", "443"),
	))
	if to["WorldProxy"] != "🇷🇺🇪🇺 Авто Самый стабильный@a2.example" || how["WorldProxy"] != "«Авто Самый стабильный»" {
		t.Errorf("WorldProxy -> %s by %q", to["WorldProxy"], how["WorldProxy"])
	}
	if to["Tiktok"] != to["WorldProxy"] || how["Tiktok"] != "the main slot's node" {
		t.Errorf("Tiktok -> %s by %q", to["Tiktok"], how["Tiktok"])
	}
	// No «Авто Самый стабильный»: any «Авто»; no «Авто»: the first node.
	to, how = run(feed(link("🇺🇸 США", "us.example", "443"), link("🇪🇺 Авто Самый быстрый", "a1.example", "443")))
	if to["WorldProxy"] != "🇪🇺 Авто Самый быстрый@a1.example" || how["WorldProxy"] != "«Авто»" {
		t.Errorf("WorldProxy -> %s by %q", to["WorldProxy"], how["WorldProxy"])
	}
	to, how = run(feed(link("🇺🇸 США", "us.example", "443"), link("🇸🇬 Сингапур", "sg.example", "443")))
	if to["WorldProxy"] != "🇺🇸 США@us.example" || how["WorldProxy"] != "the feed's first node" {
		t.Errorf("WorldProxy -> %s by %q", to["WorldProxy"], how["WorldProxy"])
	}
}

func TestFlagsOf(t *testing.T) {
	for name, want := range map[string]string{
		"🇷🇺🇳🇱 Нидерланды":              "RU NL",
		"🇳🇱 Нидерланды":                "NL",
		"🇷🇺🇫🇮 ⚡Финляндия YouTube 🚫Ad🚫": "RU FI",
		"🇷🇺🇪🇺 Авто Самый стабильный":   "RU EU",
		"Маршрутизатор":                "",
	} {
		if got := strings.Join(flagsOf(name), " "); got != want {
			t.Errorf("%q: %q, want %q", name, got, want)
		}
	}
}

// The provider's placeholder is refused, and nothing changes. PassWall
// applied it: its golden has the placeholder as the one node, and every
// slot naming a node it had deleted.
func TestRefreshRefusesThePlaceholder(t *testing.T) {
	secs := readUCI(t, "refresh-before.uci")
	out, rep, err := Refresh(secs, "sub1", readFeed(t, "refresh-feed-stub.txt"), counter())
	if !errors.Is(err, ErrEmptyFeed) || out != nil {
		t.Fatalf("err %v, out %d sections", err, len(out))
	}
	if len(rep.Skipped) != 1 || !strings.Contains(rep.Skipped[0], "placeholder") {
		t.Fatalf("report: %+v", rep)
	}
	golden := strings.Join(canon(readUCI(t, "refresh-after-stub.uci")), "\n")
	if !strings.Contains(golden, "App not supported") || !strings.Contains(golden, "option WorldProxy=nodeBY01") {
		t.Fatalf("PassWall's golden does not show the wipe this guards against:\n%s", golden)
	}
	if _, _, err := Refresh(secs, "sub1", []byte("\n\n"), counter()); !errors.Is(err, ErrEmptyFeed) {
		t.Fatalf("an empty feed: %v", err)
	}
	if _, _, err := Refresh(secs, "nope", readFeed(t, "refresh-feed-a.txt"), counter()); err == nil {
		t.Fatal("an unknown subscription refreshed")
	}
}

// A second refresh with the same feed changes nothing but the names.
func TestRefreshTwiceIsStable(t *testing.T) {
	secs := readUCI(t, "refresh-before.uci")
	body := readFeed(t, "refresh-feed-a.txt")
	once, _, err := Refresh(secs, "sub1", body, counter())
	if err != nil {
		t.Fatal(err)
	}
	next := counter()
	for i := 0; i < 20; i++ {
		next() // other names this time
	}
	twice, rep, err := Refresh(once, "sub1", body, next)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(canon(once), canon(twice)) {
		t.Fatalf("a second refresh changed the policy:\n%s", sectionDiff(canon(twice), canon(once)))
	}
	for _, r := range rep.Rebound {
		if r.How != "name+address:port" {
			t.Errorf("%s re-pointed by %s on an unchanged feed", r.Ref, r.How)
		}
	}
}

func TestFeedMD5DropsBlankLines(t *testing.T) {
	if FeedMD5([]byte("a\n\n  \n\t\r\nb\n")) != FeedMD5([]byte("a\nb\n")) {
		t.Fatal("blank lines count")
	}
	if FeedMD5([]byte("a\nb")) == FeedMD5([]byte("a\nb\n")) {
		t.Fatal("the last newline is part of the file")
	}
}

func sectionDiff(a, b []string) string {
	var out []string
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		var x, y string
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			out = append(out, fmt.Sprintf("--- section %d ours:\n%s\n--- PassWall:\n%s", i, x, y))
		}
	}
	return strings.Join(out, "\n")
}

// A subscription named "default" gives its nodes no group (PassWall's
// update_node): its fallback still finds them. A slot before WorldProxy in
// the file follows where WorldProxy went. The subscription's domain strategy
// reaches every node; what vctl cannot render is refused.
func TestRefreshEdgeCases(t *testing.T) {
	policy := func(remark, extra string) []Section {
		secs, err := ParseUCI(`
config global 'g'
	option node 'shunt'
config shunt_rules 'Early'
	option remarks 'Early'
config shunt_rules 'WorldProxy'
	option remarks 'WorldProxy'
config nodes 'shunt'
	option type 'Xray'
	option protocol '_shunt'
	option Early 'oBY'
	option WorldProxy 'oNL'
config subscribe_list 's'
	option remark '` + remark + `'
` + extra + `
config nodes 'oBY'
	option type 'Xray'
	option protocol 'vless'
	option add_mode '2'
	option group '` + remark + `'
	option remarks '🇧🇾 Беларусь'
	option address 'by.example'
	option port '443'
config nodes 'oNL'
	option type 'Xray'
	option protocol 'vless'
	option add_mode '2'
	option group '` + remark + `'
	option remarks '🇷🇺🇳🇱 Нидерланды'
	option address 'nl.example'
	option port '50055'
config nodes 'manual1'
	option type 'Xray'
	option protocol 'vless'
	option remarks 'my own'
	option address 'mine.example'
	option port '443'
`)
		if err != nil {
			t.Fatal(err)
		}
		return secs
	}
	body := []byte(base64.StdEncoding.EncodeToString([]byte(
		"vless://00000000-0000-4000-8000-000000000001@us.example:443?type=tcp&security=none#" + url.PathEscape("🇺🇸 США") + "\n" +
			"vless://00000000-0000-4000-8000-000000000001@a.example:443?type=tcp&security=none#" + url.PathEscape("🇷🇺🇪🇺 Авто Самый стабильный") + "\n")))
	get := func(secs []Section, sec, opt string) string {
		for _, s := range secs {
			if s.Name == sec {
				return s.Get(opt)
			}
		}
		return ""
	}
	for _, remark := range []string{"Sub", "default"} {
		out, rep, err := Refresh(policy(remark, "\toption domain_strategy 'UseIPv4'\n"), "s", body, counter())
		if err != nil {
			t.Fatalf("%s: %v", remark, err)
		}
		wp, early := get(out, "shunt", "WorldProxy"), get(out, "shunt", "Early")
		if get(out, wp, "remarks") != "🇷🇺🇪🇺 Авто Самый стабильный" || early != wp {
			t.Fatalf("%s: WorldProxy -> %q, Early -> %q (%+v)", remark, get(out, wp, "remarks"), get(out, early, "remarks"), rep.Rebound)
		}
		if get(out, wp, "domain_strategy") != "UseIPv4" {
			t.Fatalf("%s: the subscription's domain strategy did not reach its nodes", remark)
		}
	}
	for name, extra := range map[string]string{
		"chained":          "\toption chain_proxy '1'\n\toption preproxy_node 'manual1'\n",
		"landing":          "\toption chain_proxy '2'\n\toption to_node 'manual1'\n",
		"an interface":     "\toption chain_proxy '3'\n\toption outbound_iface 'wan'\n",
		"its own resolver": "\toption domain_resolver 'udp'\n\toption domain_resolver_dns '1.1.1.1'\n",
	} {
		if _, _, err := Refresh(policy("Sub", extra), "s", body, counter()); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: err %v, want ErrUnsupported", name, err)
		}
	}
	// A chain_proxy naming nothing PassWall would chain through (one of the
	// subscription's own nodes, a node that is not there) is ignored, as
	// update_node ignores it.
	for name, extra := range map[string]string{
		"its own node": "\toption chain_proxy '1'\n\toption preproxy_node 'oBY'\n",
		"no such node": "\toption chain_proxy '2'\n\toption to_node 'gone'\n",
	} {
		if _, _, err := Refresh(policy("Sub", extra), "s", body, counter()); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
