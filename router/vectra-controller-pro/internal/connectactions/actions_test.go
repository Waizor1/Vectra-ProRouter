package connectactions

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func payload(action, params string) []byte {
	return []byte(`{"origin":"partner_action","actionId":"act-1","ownerRef":"owner-1","action":"` + action + `","params":` + params + `}`)
}
func TestSevenStrictActions(t *testing.T) {
	for _, tc := range []struct{ action, params string }{
		{"select_entry", `{"entryId":null}`}, {"select_entry", `{"entryId":"entry:01"}`},
		{"set_rules", `{"direct":["example.com"],"vpn":[]}`},
		{"set_service", `{"service":"youtube","entryId":null}`},
		{"set_service", `{"service":"ai","entryId":":auto"}`}, // back to the default
		{"set_wifi", `{"ssid":"Роутер","password":"secret-123"}`},
		{"reboot", `{}`}, {"update_now", `{}`}, {"set_auto_update", `{"enabled":false}`},
	} {
		t.Run(tc.action, func(t *testing.T) {
			e, err := Parse(payload(tc.action, tc.params))
			if err != nil || e.Action != tc.action {
				t.Fatalf("valid action rejected: %v", err)
			}
		})
	}
}
func TestStrictActionsRejectMalformedWithoutEcho(t *testing.T) {
	for _, tc := range []struct{ action, params string }{
		{"select_entry", `{}`}, {"select_entry", `{"entryId":"bad/value"}`}, {"select_entry", `{"entryId":42}`},
		{"set_rules", `{"direct":null,"vpn":[]}`}, {"set_rules", `{"direct":[],"vpn":["https://example.com"]}`},
		{"set_rules", `{"direct":[],"vpn":["bad..example"]}`},
		{"set_service", `{"service":"","entryId":null}`}, {"set_service", `{"service":"youtube"}`},
		{"set_wifi", `{"ssid":"ok","password":"SENSITIVE" ,"unexpected":true}`},
		{"set_wifi", `{"ssid":"","password":"secret-123"}`}, {"set_wifi", `{"ssid":"ok\n","password":"secret-123"}`},
		{"set_wifi", `{"ssid":"ok","password":"парольпароль"}`},
		{"set_wifi", `{"ssid":"ok","password":"short"}`},
		{"reboot", `{"force":true}`}, {"update_now", `null`}, {"set_auto_update", `{}`},
		{"set_auto_update", `{"enabled":null}`}, {"set_auto_update", `{"enabled":"true"}`}, {"shell", `{}`},
		{"set_wifi", `{"ssid":"ok","password":"secret-123","password":"secret-456"}`},
	} {
		_, err := Parse(payload(tc.action, tc.params))
		if err != ErrInvalidPayload {
			t.Errorf("expected fixed rejection for action %s", tc.action)
		}
	}
	for _, raw := range []string{
		`{"origin":"partner_action","actionId":"act-1","ownerRef":"owner-1","action":"reboot","params":{},"unknown":"SENSITIVE"}`,
		`{"origin":"partner_action","actionId":"act-1","ownerRef":"owner-1","action":"reboot","params":{},"action":"set_wifi"}`,
		`{"origin":"wrong","actionId":"act-1","ownerRef":"owner-1","action":"reboot","params":{}}`,
		string(payload("reboot", `{}`)) + `{}`,
		strings.Repeat(" ", MaxPayloadBytes+1),
	} {
		if _, err := Parse([]byte(raw)); err != ErrInvalidPayload {
			t.Error("malformed envelope accepted")
		}
	}
}
func TestActionBounds(t *testing.T) {
	makeWiFi := func(ssid, password string) []byte {
		p, _ := json.Marshal(WiFi{SSID: ssid, Password: password})
		return payload("set_wifi", string(p))
	}
	for _, tc := range []struct {
		ssid, password string
		ok             bool
	}{
		{strings.Repeat("a", 32), strings.Repeat("p", 63), true},
		{strings.Repeat("a", 33), "password", false}, {strings.Repeat("ж", 16), "password", true},
		{strings.Repeat("ж", 17), "password", false}, {"ssid", strings.Repeat("p", 64), false},
	} {
		_, err := Parse(makeWiFi(tc.ssid, tc.password))
		if (err == nil) != tc.ok {
			t.Error("wifi bounds wrong")
		}
	}
	rules := Rules{Direct: []string{}, VPN: []string{}}
	for i := 0; i < 300; i++ {
		rules.Direct = append(rules.Direct, fmt.Sprintf("domain%d.example", i))
	}
	p, _ := json.Marshal(rules)
	if _, err := Parse(payload("set_rules", string(p))); err != nil {
		t.Fatal("300 domains rejected")
	}
	rules.VPN = append(rules.VPN, "example.net")
	p, _ = json.Marshal(rules)
	if _, err := Parse(payload("set_rules", string(p))); err != ErrInvalidPayload {
		t.Fatal("301 domains accepted")
	}
}
func TestOwnerBindingRejectsStaleAndWrongRouter(t *testing.T) {
	e, err := Parse(payload("reboot", `{}`))
	if err != nil {
		t.Fatal(err)
	}
	good := Binding{RouterID: "router-1", OwnerRef: "owner-1"}
	if err := Authorize(e, good, "router-1"); err != nil {
		t.Fatal(err)
	}
	for _, b := range []Binding{{}, {RouterID: "router-1"}, {RouterID: "router-1", OwnerRef: "owner-2"}} {
		if Authorize(e, b, "router-1") != ErrUnauthorized {
			t.Error("bad owner accepted")
		}
	}
	if Authorize(e, good, "router-2") != ErrUnauthorized {
		t.Error("wrong target accepted")
	}
}
func TestRulesNormalizePartnerDomains(t *testing.T) {
	e, err := Parse(payload("set_rules", `{"direct":[" EXAMPLE.COM ","example.com","*.пример.рф",".under_score.example."],"vpn":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	p := e.Params.(Rules)
	want := []string{"example.com", "*.пример.рф", ".under_score.example."}
	if !reflect.DeepEqual(p.Direct, want) {
		t.Fatalf("domain normalization mismatch: %v", p.Direct)
	}
	duplicate := Rules{Direct: []string{}, VPN: []string{}}
	for i := 0; i < 400; i++ {
		duplicate.Direct = append(duplicate.Direct, "EXAMPLE.COM")
	}
	raw, _ := json.Marshal(duplicate)
	if _, err := Parse(payload("set_rules", string(raw))); err != nil {
		t.Fatal("deduped domain limit rejected")
	}
	if _, err := Parse(payload("select_entry", `{"entryId":"`+strings.Repeat("a", 64)+`"}`)); err != nil {
		t.Fatal("64 byte ID rejected")
	}
	if _, err := Parse(payload("select_entry", `{"entryId":"`+strings.Repeat("a", 65)+`"}`)); err != ErrInvalidPayload {
		t.Fatal("65 byte ID accepted")
	}
	domain := strings.Repeat("x", 253)
	raw, _ = json.Marshal(Rules{Direct: []string{domain}, VPN: []string{}})
	if _, err := Parse(payload("set_rules", string(raw))); err != nil {
		t.Fatal("253 character domain rejected")
	}
	raw, _ = json.Marshal(Rules{Direct: []string{domain + "x"}, VPN: []string{}})
	if _, err := Parse(payload("set_rules", string(raw))); err != ErrInvalidPayload {
		t.Fatal("254 character domain accepted")
	}
}
