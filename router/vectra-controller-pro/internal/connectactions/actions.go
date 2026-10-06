// Package connectactions validates account-scoped Connect requests and records
// execution metadata without persisting action parameters or device secrets.
// Device mutations and authenticated check-in transport belong to the caller.
package connectactions

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"vectra-controller-pro/internal/portfwd"
)

const JobType = "connect_router_action"
const MaxPayloadBytes = 96 * 1024

var ErrInvalidPayload = errors.New("invalid connect action payload")
var ErrUnauthorized = errors.New("connect action ownership rejected")
var idPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

// Envelope contains transient parameters; never put this value in persisted
// state, job stdout/stderr, or logs. Parse supplies exactly one typed Params.
type Envelope struct {
	Origin   string `json:"origin"`
	ActionID string `json:"actionId"`
	OwnerRef string `json:"ownerRef"`
	Action   string `json:"action"`
	Params   any    `json:"params"`
}

func (Envelope) String() string     { return "connect action (parameters redacted)" }
func (e Envelope) GoString() string { return e.String() }

type SelectEntry struct {
	EntryID *string `json:"entryId"`
}
type Rules struct {
	Direct []string `json:"direct"`
	VPN    []string `json:"vpn"`
}
type Service struct {
	Service string  `json:"service"`
	EntryID *string `json:"entryId"`
}
type WiFi struct {
	SSID     string `json:"ssid"`
	Password string `json:"password"`
	Band     string `json:"band,omitempty"` // "" = every access point radio
}

func (WiFi) String() string     { return "wifi parameters (redacted)" }
func (p WiFi) GoString() string { return p.String() }

// PortForwards replaces the owner's port forwards (capability
// set_port_forwards): the whole list, as the router UI's set_port_forwards.
// Parse holds each rule to its shape and syntax (portfwd.CheckSyntax); where
// it points and what it collides with the router decides when it applies it,
// against its own configuration, and answers in codes (dest_not_lan,
// port_conflict, …). A rule is {id?, preset?, destIp, port, proto, direct,
// enabled}; preset may be null.
type PortForwards struct {
	Rules []portfwd.Rule `json:"rules"`
}

type Empty struct{}
type AutoUpdate struct {
	Enabled bool `json:"enabled"`
}

// Binding is authenticated persisted identity, not a user-supplied owner label.
type Binding struct{ RouterID, OwnerRef string }

func Capabilities() []string {
	return []string{"select_entry", "set_rules", "set_service", "set_wifi", "reboot", "update_now", "set_auto_update", "set_port_forwards"}
}

// Parse rejects unknown, duplicate, missing and incorrectly typed fields. All
// failures share a fixed error, so decoder errors cannot disclose submitted data.
func Parse(raw []byte) (Envelope, error) {
	if len(raw) == 0 || len(raw) > MaxPayloadBytes || !utf8.Valid(raw) {
		return Envelope{}, ErrInvalidPayload
	}
	fields, ok := object(raw, "origin", "actionId", "ownerRef", "action", "params")
	if !ok {
		return Envelope{}, ErrInvalidPayload
	}
	var e Envelope
	if !stringValue(fields["origin"], &e.Origin) || e.Origin != "partner_action" ||
		!stringValue(fields["actionId"], &e.ActionID) || !idPattern.MatchString(e.ActionID) ||
		!stringValue(fields["ownerRef"], &e.OwnerRef) || !validReference(e.OwnerRef) ||
		!stringValue(fields["action"], &e.Action) {
		return Envelope{}, ErrInvalidPayload
	}
	params := fields["params"]
	switch e.Action {
	case "select_entry":
		p, ok := object(params, "entryId")
		if !ok {
			return Envelope{}, ErrInvalidPayload
		}
		entry, ok := entryValue(p["entryId"])
		if !ok {
			return Envelope{}, ErrInvalidPayload
		}
		e.Params = SelectEntry{EntryID: entry}
	case "set_rules":
		p, ok := object(params, "direct", "vpn")
		if !ok {
			return Envelope{}, ErrInvalidPayload
		}
		direct, ok := domainList(p["direct"])
		if !ok {
			return Envelope{}, ErrInvalidPayload
		}
		vpn, ok := domainList(p["vpn"])
		if !ok || len(direct)+len(vpn) > 300 {
			return Envelope{}, ErrInvalidPayload
		}
		e.Params = Rules{Direct: direct, VPN: vpn}
	case "set_service":
		p, ok := object(params, "service", "entryId")
		if !ok {
			return Envelope{}, ErrInvalidPayload
		}
		var service string
		if !stringValue(p["service"], &service) || !idPattern.MatchString(service) {
			return Envelope{}, ErrInvalidPayload
		}
		entry, ok := entryValue(p["entryId"])
		if !ok {
			return Envelope{}, ErrInvalidPayload
		}
		e.Params = Service{Service: service, EntryID: entry}
	case "set_wifi":
		p, ok := object(params, "ssid", "password", "band")
		if !ok {
			p, ok = object(params, "ssid", "password")
		}
		if !ok {
			return Envelope{}, ErrInvalidPayload
		}
		var wifi WiFi
		if !stringValue(p["ssid"], &wifi.SSID) || !stringValue(p["password"], &wifi.Password) || len(wifi.SSID) < 1 || len(wifi.SSID) > 32 || len(wifi.Password) < 8 || len(wifi.Password) > 63 {
			return Envelope{}, ErrInvalidPayload
		}
		if raw, has := p["band"]; has && (!stringValue(raw, &wifi.Band) || (wifi.Band != "2g" && wifi.Band != "5g" && wifi.Band != "6g")) {
			return Envelope{}, ErrInvalidPayload
		}
		for _, r := range wifi.SSID {
			if unicode.IsControl(r) {
				return Envelope{}, ErrInvalidPayload
			}
		}
		for _, r := range wifi.Password {
			if r < 32 || r > 126 {
				return Envelope{}, ErrInvalidPayload
			}
		}
		e.Params = wifi
	case "set_port_forwards":
		p, ok := object(params, "rules")
		if !ok {
			return Envelope{}, ErrInvalidPayload
		}
		rules, ok := portForwardList(p["rules"])
		if !ok {
			return Envelope{}, ErrInvalidPayload
		}
		e.Params = PortForwards{Rules: rules}
	case "reboot", "update_now":
		if _, ok := object(params); !ok {
			return Envelope{}, ErrInvalidPayload
		}
		e.Params = Empty{}
	case "set_auto_update":
		p, ok := object(params, "enabled")
		if !ok || (string(p["enabled"]) != "true" && string(p["enabled"]) != "false") {
			return Envelope{}, ErrInvalidPayload
		}
		e.Params = AutoUpdate{Enabled: string(p["enabled"]) == "true"}
	default:
		return Envelope{}, ErrInvalidPayload
	}
	return e, nil
}

func Authorize(e Envelope, b Binding, responseRouterID string) error {
	if !validReference(b.RouterID) || !validReference(b.OwnerRef) || b.RouterID != responseRouterID || b.OwnerRef != e.OwnerRef {
		return ErrUnauthorized
	}
	return nil
}
func validReference(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if r < 33 || r > 126 {
			return false
		}
	}
	return true
}
func stringValue(raw json.RawMessage, out *string) bool {
	return len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, out) == nil
}
func entryValue(raw json.RawMessage) (*string, bool) {
	if string(raw) == "null" {
		return nil, true
	}
	var entry string
	if !stringValue(raw, &entry) || !idPattern.MatchString(entry) {
		return nil, false
	}
	return &entry, true
}

// object uses streaming keys so duplicate keys cannot be silently overwritten.
func object(raw []byte, names ...string) (map[string]json.RawMessage, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, false
	}
	allowed := map[string]bool{}
	for _, n := range names {
		allowed[n] = true
	}
	out := map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, false
		}
		key, ok := tok.(string)
		if !ok || !allowed[key] {
			return nil, false
		}
		if _, exists := out[key]; exists {
			return nil, false
		}
		var val json.RawMessage
		if dec.Decode(&val) != nil {
			return nil, false
		}
		out[key] = bytes.TrimSpace(val)
	}
	tok, err = dec.Token()
	if err != nil || tok != json.Delim('}') || len(out) != len(names) {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	return out, true
}
func domainList(raw json.RawMessage) ([]string, bool) {
	if len(raw) == 0 || raw[0] != '[' {
		return nil, false
	}
	var values []string
	if json.Unmarshal(raw, &values) != nil {
		return nil, false
	}
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || utf8.RuneCountInString(value) > 253 || strings.Contains(value, "..") {
			return nil, false
		}
		base := strings.TrimPrefix(value, "*.")
		for _, r := range base {
			if !(unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-' || r == '.') {
				return nil, false
			}
		}
		if strings.Trim(base, ".") == "" {
			return nil, false
		}
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out, true
}

// portForwardKeys are a rule's fields; "id" (a new rule has none) and
// "preset" are optional.
var portForwardKeys = []string{"destIp", "port", "proto", "direct", "enabled"}

// portForwardList reads the rules: an array of objects, each with exactly
// portForwardKeys (and maybe "id", "preset"), strings and booleans of their
// types, each rule's syntax valid. How many is the router's to judge — more
// than portfwd.MaxRules is answered too_many, not refused as malformed (the
// payload's own bound, MaxPayloadBytes, keeps the list small).
func portForwardList(raw json.RawMessage) ([]portfwd.Rule, bool) {
	var items []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &items) != nil {
		return nil, false
	}
	out := make([]portfwd.Rule, 0, len(items))
	for _, item := range items {
		var f map[string]json.RawMessage
		ok := false
		for _, optional := range [][]string{{"id", "preset"}, {"id"}, {"preset"}, nil} {
			if f, ok = object(item, append(optional, portForwardKeys...)...); ok {
				break
			}
		}
		if !ok {
			return nil, false
		}
		var r portfwd.Rule
		if _, has := f["id"]; has && !stringValue(f["id"], &r.ID) {
			return nil, false
		}
		if raw, has := f["preset"]; has && string(raw) != "null" {
			var p string
			if !stringValue(raw, &p) {
				return nil, false
			}
			r.Preset = &p
		}
		if !stringValue(f["destIp"], &r.DestIP) || !stringValue(f["port"], &r.Port) || !stringValue(f["proto"], &r.Proto) ||
			!boolValue(f["direct"], &r.Direct) || !boolValue(f["enabled"], &r.Enabled) {
			return nil, false
		}
		if portfwd.CheckSyntax(r) != nil {
			return nil, false
		}
		out = append(out, r)
	}
	return out, true
}

func boolValue(raw json.RawMessage, out *bool) bool {
	switch string(raw) {
	case "true":
		*out = true
		return true
	case "false":
		*out = false
		return true
	}
	return false
}
