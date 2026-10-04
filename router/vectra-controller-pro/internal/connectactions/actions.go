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

type Empty struct{}
type AutoUpdate struct {
	Enabled bool `json:"enabled"`
}

// Binding is authenticated persisted identity, not a user-supplied owner label.
type Binding struct{ RouterID, OwnerRef string }

func Capabilities() []string {
	return []string{"select_entry", "set_rules", "set_service", "set_wifi", "reboot", "update_now", "set_auto_update"}
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
