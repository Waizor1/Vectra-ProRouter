package xray

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"

	"vectra-controller-pro/internal/config"
)

// PassWall-compatible routing.
//
// The fleet's routers carry the operator's route policy as PassWall2's own
// configuration: a shunt node whose rules (the panel's WorldProxy, YouTube,
// Special, TikTok... slots) send their domains and addresses through the node
// the operator chose for each, FakeDNS for those domains, and everything else
// straight out. The provider's xray-JSON is a different design — built for
// its phone app: every connection through its balancers, Russian sites
// through its Russian relay (rate-limited and challenged by the marketplaces
// and banks), Meta's QUIC dropped. On the test router the owner measured the
// difference: PassWall carried Instagram and the rest; the provider's scheme
// did not.
//
// So the router can take PassWall's own configuration instead: PassWall2's
// generator (util_xray.lua gen_config, the code PassWall runs) turns the
// panel-maintained UCI into xray JSON, and AdaptPassWall makes that the kind
// of document the splice takes — vctl's own data plane, DNS inbound, socket
// marks, API and safety mechanisms around PassWall's exact routing.

// PassWallInboundTags are the inbounds PassWall's generator makes for its
// transparent proxy; its rules name them.
var PassWallInboundTags = []string{"tcp_redir", "udp_redir"}

// PassWallResult is what the adaptation read from PassWall's document.
type PassWallResult struct {
	// Sniffing of PassWall's transparent-proxy inbound. FakeDNS needs
	// "fakedns" among the overrides: a connection to a fake address is
	// routed — and dialled — by the domain it stands for.
	Sniffing config.Sniffing
	// FakeDNSPools are the fake address ranges PassWall's DNS hands out. The
	// data plane must carry them into xray, not bypass them.
	FakeDNSPools []string
	// Rules is how many routing rules PassWall made; RemovedLocalhost how
	// many "localhost" DNS servers were taken out (see AdaptPassWall).
	Rules            int
	RemovedLocalhost int
	RewrittenMarks   int
	// DroppedAssetDir: the config named its own geo directory
	// (env.XRAY_LOCATION_ASSET), and it was taken out.
	DroppedAssetDir bool
}

// AdaptPassWall turns a config PassWall2's generator produced (flag=global)
// into a provider document for Splice, the tproxy inbound tagged tproxyTag:
//
//   - its inbounds go (the splice puts vctl's tproxy and DNS inbounds in their
//     place); rules naming tcp_redir/udp_redir name tproxyTag instead;
//   - socket marks go from its outbounds: the splice stamps vctl's own, which
//     vctl's output chain returns on (PassWall's 255 would loop back into
//     TPROXY);
//   - its env goes, XRAY_LOCATION_ASSET with it: vctl says where the geo
//     files are, and what else xray's environment holds;
//   - "localhost" DNS servers go: PassWall resolves its nodes' names with the
//     system resolver, which is dnsmasq, which vctl redirects into this very
//     DNS — a loop. The splice answers those names directly instead
//     (DNSOptions: tcp+local servers for the nodes' host names);
//   - everything else — routing, the rules' domains and addresses, FakeDNS,
//     the direct DNS — is PassWall's, byte for byte in content.
func AdaptPassWall(raw []byte, tproxyTag string) ([]byte, PassWallResult, error) {
	var res PassWallResult
	if tproxyTag == "" {
		tproxyTag = "tproxy-in"
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, res, fmt.Errorf("passwall: the generated config is not a JSON object: %w", err)
	}

	// The transparent-proxy inbound's sniffing.
	var inbounds []struct {
		Tag      string `json:"tag"`
		Sniffing *struct {
			Enabled      bool     `json:"enabled"`
			DestOverride []string `json:"destOverride"`
			MetadataOnly bool     `json:"metadataOnly"`
			RouteOnly    bool     `json:"routeOnly"`
		} `json:"sniffing"`
	}
	if v, ok := doc["inbounds"]; ok {
		if err := json.Unmarshal(v, &inbounds); err != nil {
			return nil, res, fmt.Errorf("passwall: inbounds: %w", err)
		}
	}
	found := false
	for _, ib := range inbounds {
		if ib.Tag != "tcp_redir" && ib.Tag != "udp_redir" {
			continue
		}
		found = true
		if ib.Sniffing != nil && ib.Sniffing.Enabled {
			res.Sniffing = config.Sniffing{
				Enabled:      true,
				DestOverride: append([]string(nil), ib.Sniffing.DestOverride...),
				MetadataOnly: ib.Sniffing.MetadataOnly,
				RouteOnly:    ib.Sniffing.RouteOnly,
			}
		}
		if ib.Tag == "tcp_redir" {
			break
		}
	}
	if !found {
		return nil, res, fmt.Errorf("passwall: the generated config has no transparent-proxy inbound (tcp_redir): not a global config")
	}
	doc["inbounds"] = json.RawMessage("[]")

	// Where xray reads its geo files is vctl's to say. PassWall's generator
	// writes it into the config ("env": {"XRAY_LOCATION_ASSET": its
	// v2ray_location_asset, /usr/share/v2ray/}), and a recent xray (26.7.28,
	// the test router's) applies a config's env over the one it was started with — so xray read PassWall's
	// directory whatever the supervisor and the xray -test gate pinned (they
	// pin the route policy's own). On the test router that directory went with
	// PassWall's v2ray-geoip/geosite packages, and xray stopped starting
	// (2026-09-29; the stand's xray 26.3.27 ignores a config's env and did not
	// show it). The rest of env goes with it (r12): xray's environment is
	// vctl's to set, and the splice refuses any document that carries one
	// (provider_guard.go).
	for k, raw := range doc {
		if foldKey(k) != foldKey("env") {
			continue
		}
		env := map[string]json.RawMessage{}
		if json.Unmarshal(raw, &env) == nil {
			for name := range env {
				if strings.EqualFold(name, "XRAY_LOCATION_ASSET") {
					res.DroppedAssetDir = true
				}
			}
		}
		delete(doc, k)
	}

	// Outbounds: no socket marks of PassWall's.
	var outbounds []map[string]json.RawMessage
	if err := json.Unmarshal(doc["outbounds"], &outbounds); err != nil || len(outbounds) == 0 {
		return nil, res, fmt.Errorf("passwall: the generated config has no outbounds")
	}
	for _, ob := range outbounds {
		ss := map[string]json.RawMessage{}
		if raw, ok := ob["streamSettings"]; ok && json.Unmarshal(raw, &ss) == nil {
			so := map[string]json.RawMessage{}
			if raw, ok := ss["sockopt"]; ok && json.Unmarshal(raw, &so) == nil {
				if _, has := so["mark"]; has {
					delete(so, "mark")
					res.RewrittenMarks++
					if len(so) == 0 {
						delete(ss, "sockopt")
					} else {
						ss["sockopt"] = mustMarshal(so)
					}
					if len(ss) == 0 {
						delete(ob, "streamSettings")
					} else {
						ob["streamSettings"] = mustMarshal(ss)
					}
				}
			}
		}
	}
	doc["outbounds"] = mustMarshal(outbounds)

	// Routing: PassWall's inbound tags become the tproxy inbound's.
	if raw, ok := doc["routing"]; ok {
		var routing map[string]json.RawMessage
		if err := json.Unmarshal(raw, &routing); err != nil {
			return nil, res, fmt.Errorf("passwall: routing: %w", err)
		}
		var rules []map[string]json.RawMessage
		if raw, ok := routing["rules"]; ok {
			if err := json.Unmarshal(raw, &rules); err != nil {
				return nil, res, fmt.Errorf("passwall: routing.rules: %w", err)
			}
		}
		for _, r := range rules {
			var tags []string
			if raw, ok := r["inboundTag"]; ok && json.Unmarshal(raw, &tags) == nil {
				out := make([]string, 0, len(tags))
				seen := map[string]bool{}
				for _, t := range tags {
					if t == "tcp_redir" || t == "udp_redir" {
						t = tproxyTag
					}
					if !seen[t] {
						seen[t] = true
						out = append(out, t)
					}
				}
				r["inboundTag"] = mustMarshal(out)
			}
		}
		res.Rules = len(rules)
		routing["rules"] = mustMarshal(rules)
		doc["routing"] = mustMarshal(routing)
	}

	// DNS: no system resolver.
	if raw, ok := doc["dns"]; ok && !isJSONNull(raw) {
		var dns map[string]json.RawMessage
		if err := json.Unmarshal(raw, &dns); err != nil {
			return nil, res, fmt.Errorf("passwall: dns: %w", err)
		}
		var servers []json.RawMessage
		if raw, ok := dns["servers"]; ok {
			if err := json.Unmarshal(raw, &servers); err != nil {
				return nil, res, fmt.Errorf("passwall: dns.servers: %w", err)
			}
		}
		kept := servers[:0]
		for _, s := range servers {
			addr := jsonString(s)
			if addr == "" {
				var so map[string]json.RawMessage
				if json.Unmarshal(s, &so) == nil {
					addr = jsonString(so["address"])
				}
			}
			if addr == "localhost" {
				res.RemovedLocalhost++
				continue
			}
			kept = append(kept, s)
		}
		dns["servers"] = mustMarshal(kept)
		doc["dns"] = mustMarshal(dns)
	}

	res.FakeDNSPools = fakeDNSPools(doc["fakedns"])

	// Keys in a stable order: the render key and the digest must not move
	// when nothing did.
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		writeJSONString(&b, k)
		b.WriteByte(':')
		b.Write(doc[k])
	}
	b.WriteByte('}')
	return b.Bytes(), res, nil
}

// fakeDNSPools reads the ipPool of each FakeDNS pool ("fakedns": an object or
// a list of them), IPv4 ones only.
func fakeDNSPools(raw json.RawMessage) []string {
	if len(bytes.TrimSpace(raw)) == 0 || isJSONNull(raw) {
		return nil
	}
	type pool struct {
		IPPool string `json:"ipPool"`
	}
	var list []pool
	if json.Unmarshal(raw, &list) != nil {
		var one pool
		if json.Unmarshal(raw, &one) != nil {
			return nil
		}
		list = []pool{one}
	}
	var out []string
	for _, p := range list {
		if _, n, err := net.ParseCIDR(p.IPPool); err == nil && n.IP.To4() != nil {
			out = append(out, n.String())
		}
	}
	return out
}

// RenderFakeDNSPools reads the FakeDNS pools of an installed render: address
// ranges the data plane must send into xray instead of bypassing.
func RenderFakeDNSPools(render []byte) []string {
	var doc struct {
		FakeDNS json.RawMessage `json:"fakedns"`
	}
	if json.Unmarshal(render, &doc) != nil {
		return nil
	}
	return fakeDNSPools(doc.FakeDNS)
}

func mustMarshal(v interface{}) json.RawMessage {
	return json.RawMessage(marshalNoEscape(v))
}
