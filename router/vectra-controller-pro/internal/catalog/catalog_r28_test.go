package catalog

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// r28: «Игровые соединения» — the games' own traffic, by class, first under
// «Игры», on the stable exit by default and never direct.
func TestTheBuiltinCatalogCarriesGameplayTraffic(t *testing.T) {
	c := Builtin()
	s, ok := c.Services["gameplay"]
	if !ok {
		t.Fatal("no gameplay service")
	}
	modes, def := s.EffectiveModes("gameplay")
	if s.Group != "games" || def != ModeStable || !reflect.DeepEqual(modes, []string{ModeStable, ModeMain}) || s.Title["ru"] == "" {
		t.Fatalf("gameplay %+v (modes %v default %s)", s, modes, def)
	}
	want := []Traffic{{Network: "udp", Except: []int{53, 123, 443}}, {Network: "tcp", Except: []int{80, 443}}}
	if !reflect.DeepEqual(s.Traffic, want) {
		t.Fatalf("traffic %+v", s.Traffic)
	}
	for id, other := range c.Services {
		if other.Group == "games" && id != "gameplay" && other.Order <= s.Order {
			t.Fatalf("%s comes before gameplay (%d <= %d)", id, other.Order, s.Order)
		}
	}
	if c.Serial < 2026102001 {
		t.Fatalf("serial %d: r28's catalog must be newer than r27's built-in 2026101901", c.Serial)
	}
}

// A traffic service is a class of traffic, nothing else: it names no site,
// it is never taken out of the tunnel, and its classes are bounded.
func TestTrafficIsValidated(t *testing.T) {
	base := func(svc string) string {
		return `{"schema":1,"serial":5,"services":{"x":{` + svc + `}},"groups":{"games":{"title":{"ru":"Игры"}}},"sites":{}}`
	}
	good := []string{
		base(`"title":{"ru":"Игровые соединения"},"group":"games","modes":["stable","main"],"default":"stable","traffic":[{"network":"udp","except":[53,123,443]},{"network":"tcp","except":[80,443]}]`),
		base(`"modes":["stable","main"],"traffic":[{"network":"udp"}]`),
	}
	for _, raw := range good {
		if _, err := Parse([]byte(raw), true); err != nil {
			t.Errorf("refused %s: %v", raw, err)
		}
	}
	bad := map[string]string{
		"network":        base(`"modes":["stable","main"],"traffic":[{"network":"icmp"}]`),
		"no network":     base(`"modes":["stable","main"],"traffic":[{"except":[80]}]`),
		"twice":          base(`"modes":["stable","main"],"traffic":[{"network":"udp"},{"network":"udp","except":[53]}]`),
		"port zero":      base(`"modes":["stable","main"],"traffic":[{"network":"tcp","except":[0]}]`),
		"port high":      base(`"modes":["stable","main"],"traffic":[{"network":"tcp","except":[65536]}]`),
		"too many ports": base(`"modes":["stable","main"],"traffic":[{"network":"tcp","except":[` + somePorts(65) + `]}]`),
		"direct mode":    base(`"modes":["stable","direct"],"traffic":[{"network":"udp"}]`),
		"country mode":   base(`"modes":["stable","country"],"traffic":[{"network":"udp"}]`),
		"no modes":       base(`"traffic":[{"network":"udp"}]`),
		"names":          base(`"modes":["stable","main"],"names":["domain:x.com"],"traffic":[{"network":"udp"}]`),
		"ips":            base(`"modes":["stable","main"],"ips":["1.2.3.4"],"traffic":[{"network":"udp"}]`),
		"marker":         base(`"modes":["stable","main"],"marker":"domain:x.com","onlyKnown":true,"traffic":[{"network":"udp"}]`),
		"geosites":       base(`"modes":["stable","main"],"geosites":["steam"],"traffic":[{"network":"udp"}]`),
		"too many":       base(`"modes":["stable","main"],"traffic":[{"network":"udp"},{"network":"tcp"},{"network":"udp"},{"network":"tcp"},{"network":"udp"}]`),
	}
	for name, raw := range bad {
		if _, err := Parse([]byte(raw), true); err == nil {
			t.Errorf("%s: accepted %s", name, raw)
		}
	}
}

// Ports of a class: every port of its network but the excepted ones, as
// xray's port list.
func TestTrafficPorts(t *testing.T) {
	cases := map[string]Traffic{
		"1-52,54-122,124-442,444-65535": {Network: "udp", Except: []int{443, 53, 123}},
		"1-79,81-442,444-65535":         {Network: "tcp", Except: []int{80, 443, 80}},
		"1-65535":                       {Network: "udp"},
		"2-65534":                       {Network: "udp", Except: []int{1, 65535}},
	}
	for want, tr := range cases {
		if got := tr.Ports(); got != want {
			t.Errorf("%+v: %s, want %s", tr, got, want)
		}
	}
}

func somePorts(n int) string {
	var b strings.Builder
	for p := 1; p <= n; p++ {
		if p > 1 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(1000 + p))
	}
	return b.String()
}
