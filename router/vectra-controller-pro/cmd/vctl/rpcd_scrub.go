package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/redact"
	"vectra-controller-pro/internal/subscription"
)

// The router UI is any LuCI session's, so no answer of `vectra` may carry a
// credential: the VPN's user ids, passwords and keys, the subscription's
// token, the panel token, the device key. The answers are built without them
// (ui/contract/README.md, Conventions), but what the router says in words —
// its journal, the error of an xray that exited, the detail of a refusal —
// quotes whatever went wrong, and on the test router the journal carried 7
// UUIDs and 119 subscription and panel URLs. So every answer passes through
// rpcdScrub on its way out, whatever method built it.

// rpcdAnswer is what `vectra call <method>` prints: the method's answer with
// every credential taken out.
func rpcdAnswer(ctx context.Context, cfg agentcfg.Config, method string, params []byte) interface{} {
	return rpcdScrub(method, rpcdCall(ctx, cfg, method, params), rpcdSecrets(cfg))
}

// rpcdFreeText are the fields that hold words the router quotes rather than
// values it built: a log line, an error, a refusal's detail. A key or a
// token may be printed bare in them, so they lose any long run of letters and
// digits as well (redact.Text); every other string loses what is a credential
// by its shape (redact.Credentials) — a long node tag stays.
var rpcdFreeText = map[string]bool{"message": true, "detail": true, "error": true}

// rpcdVerbatim are the strings the UI needs exactly, and that are not
// credentials however they look, by method and path:
//   - status.controlPlane.routerId: the router's own id in the panel, which
//     support asks for. It opens nothing: the panel takes a router's calls
//     only with its token beside it (x-vectra-router-token).
//   - setup's claim: its code, QR and bot link are how the owner links the
//     router to their account (ADR-0006) — shown on purpose, and valid for
//     minutes.
//   - the owner's own: My sites (rules), which the UI edits and sends back
//     whole, and the Wi-Fi's names.
var rpcdVerbatim = map[string]bool{
	"status .controlPlane.routerId": true,
	"setup .vectra.claim.code":      true,
	"setup .vectra.claim.qr":        true,
	"setup .vectra.claim.botUrl":    true,
	"setup .wifi.radios[].ssid":     true,
	"setup .wifi.suggested":         true,
	"rules .direct[]":               true,
	"rules .proxy[]":                true,
	"rules .missing[]":              true,
}

// rpcdScrub takes every credential out of an answer: the router's own
// secrets (known) wherever they appear, and what is a credential by its shape.
// The answer comes back as generic JSON — the same fields, in the order the
// encoder writes a map's.
func rpcdScrub(method string, answer interface{}, known redact.Known) interface{} {
	raw, err := json.Marshal(answer)
	if err != nil {
		return action(false, "internal", "the answer could not be encoded")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // a counter stays the integer it is
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return action(false, "internal", "the answer could not be encoded")
	}
	var walk func(path, key string, v interface{}) interface{}
	walk = func(path, key string, v interface{}) interface{} {
		switch x := v.(type) {
		case map[string]interface{}:
			for k, e := range x {
				x[k] = walk(path+"."+k, k, e)
			}
		case []interface{}:
			for i, e := range x {
				x[i] = walk(path+"[]", key, e)
			}
		case string:
			if rpcdVerbatim[method+" "+path] {
				return x
			}
			// Addresses first, so a subscription's keeps its host
			// (https://host/<redacted>); then the known secrets whole,
			// before any rule could cut one in two.
			x = known.Redact(redact.URLs(x))
			if rpcdFreeText[key] {
				return redact.Text(x)
			}
			return redact.Credentials(x)
		}
		return v
	}
	return walk("", "", v)
}

// rpcdSecretKeys name the fields of an xray config that hold a credential.
var rpcdSecretKeys = map[string]bool{
	"id": true, "password": true, "pass": true, "auth": true, "psk": true, "uuid": true, "token": true, "seed": true,
	"publicKey": true, "privateKey": true, "secretKey": true, "preSharedKey": true, "shortId": true, "pbk": true, "sid": true,
	"path": true, // a transport's path can be the provider's secret too
}

// rpcdSecrets are the secrets this router holds, as exact values: its panel
// token and device key (the state), the subscription URL's secret parts (the
// operator config) and the credentials of the config xray runs (the render,
// and the provider document it came from). What cannot be read adds nothing.
func rpcdSecrets(cfg agentcfg.Config) redact.Known {
	var vals []string
	if raw, err := os.ReadFile(cfg.StatePath); err == nil {
		var st struct {
			AgentToken       string `json:"agent_token"`
			DevicePrivateKey string `json:"device_private_key"`
		}
		if json.Unmarshal(raw, &st) == nil {
			vals = append(vals, st.AgentToken, st.DevicePrivateKey)
		}
	}
	if raw, err := os.ReadFile(cfg.XrayConfigPath); err == nil {
		if c, err := config.Unmarshal(raw); err == nil {
			for _, s := range c.Subscriptions {
				vals = append(vals, subscription.SecretParts(s.URL)...)
			}
		}
	}
	for _, p := range []string{cfg.XrayRenderPath, cfg.ProviderConfigPath} {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var doc interface{}
		if json.Unmarshal(raw, &doc) == nil {
			vals = appendSecretValues(vals, doc)
		}
	}
	return redact.NewKnown(vals...)
}

func appendSecretValues(vals []string, v interface{}) []string {
	switch x := v.(type) {
	case map[string]interface{}:
		for k, e := range x {
			if s, ok := e.(string); ok && rpcdSecretKeys[k] {
				vals = append(vals, s)
				continue
			}
			vals = appendSecretValues(vals, e)
		}
	case []interface{}:
		for _, e := range x {
			vals = appendSecretValues(vals, e)
		}
	}
	return vals
}
