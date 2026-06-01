package subscription

import (
	"fmt"
	"time"

	"vectra-controller-pro/internal/config"
)

// ToConfigNodes converts each ParsedNode into a config.Node suitable for
// merging into Config.Nodes. The mapping is strict and value-preserving:
// it does NOT default, normalize, or rewrite any operator/provider value.
// Fields the subscription did not set remain empty on the Node.
func ToConfigNodes(parsed []ParsedNode, source SubscriptionRef) []config.Node {
	out := make([]config.Node, 0, len(parsed))
	now := time.Now().UTC().Format(time.RFC3339)
	for _, p := range parsed {
		defaults := p.ParserDefaults()
		// Security gate: a subscription-SOURCED node must NOT be able to disable
		// TLS certificate verification. A hostile/compromised upstream could set
		// allowInsecure=1 in its URIs and silently MITM every client. We drop it
		// here (see buildStream) and record the strip in the audit trail so an
		// operator can see it happened. allowInsecure remains expressible on
		// operator-authored nodes (which never go through this adapter).
		if p.Stream.AllowInsec {
			if defaults == nil {
				defaults = map[string]string{}
			}
			defaults["stream.tls.allowInsecure"] = "stripped (subscription-sourced; controller forbids upstream-disabled TLS verification)"
		}
		n := config.Node{
			ID:     p.ID,
			Remark: p.Remark,
			Group:  p.Group,
			// Tag will be filled by config.ApplyDefaults() ("node-"+ID).
			Enabled: true,
			Origin: &config.NodeOrigin{
				SubscriptionID:  source.ID,
				SubscriptionURL: source.URL,
				RawLink:         p.RawURI,
				ImportedAt:      now,
				Fingerprint:     "", // computed by dedupe step elsewhere
				ParserDefaults:  defaults,
			},
			Outbound: buildOutbound(p),
		}
		out = append(out, n)
	}
	return out
}

// SubscriptionRef is a thin reference to the subscription that produced
// a node (used for Origin tracking).
type SubscriptionRef struct {
	ID  string
	URL string
}

func buildOutbound(p ParsedNode) config.Outbound {
	out := config.Outbound{
		Protocol: p.Protocol,
		Server:   p.Server,
		Port:     p.Port,
		Stream:   buildStream(p.Stream),
	}
	switch p.Protocol {
	case "vless":
		if p.VLESS != nil {
			out.Settings.VLESS = &config.VLESSSettings{
				UUID:       p.VLESS.UUID,
				Flow:       p.VLESS.Flow,
				Encryption: p.VLESS.Encryption,
			}
		}
	case "vmess":
		if p.VMess != nil {
			out.Settings.VMess = &config.VMessSettings{
				UUID:     p.VMess.UUID,
				Security: p.VMess.Security,
				AlterID:  p.VMess.AlterID,
			}
		}
	case "trojan":
		if p.Trojan != nil {
			out.Settings.Trojan = &config.TrojanSettings{
				Password: p.Trojan.Password,
			}
		}
	case "shadowsocks":
		if p.Shadowsocks != nil {
			out.Settings.Shadowsocks = &config.ShadowsocksSettings{
				Method:   p.Shadowsocks.Method,
				Password: p.Shadowsocks.Password,
			}
		}
	case "hysteria2":
		if p.Hysteria2 != nil {
			s := &config.Hysteria2Settings{
				Password: p.Hysteria2.Password,
				HopPorts: p.Hysteria2.HopPorts,
				Up:       p.Hysteria2.Up,
				Down:     p.Hysteria2.Down,
			}
			if p.Hysteria2.Obfs != "" {
				s.Obfs = &config.Hy2Obfs{
					Type:     p.Hysteria2.Obfs,
					Password: p.Hysteria2.ObfsPass,
				}
			}
			out.Settings.Hysteria2 = s
		}
	}
	return out
}

func buildStream(s ParsedStream) *config.StreamSettings {
	if s.Transport == "" && s.Security == "" && s.SNI == "" {
		return nil // nothing was set
	}
	st := &config.StreamSettings{
		Transport: s.Transport,
		Security:  s.Security,
	}
	switch s.Transport {
	case "tcp":
		if s.TCPHeaderType != "" {
			st.TCP = &config.TCPSettings{Header: &config.TCPHeader{Type: s.TCPHeaderType}}
		}
	case "ws":
		st.WS = &config.WSSettings{Path: s.Path, Host: s.Host}
	case "grpc":
		st.GRPC = &config.GRPCSettings{
			ServiceName: s.ServiceName,
			MultiMode:   s.GRPCMode == "multi",
		}
	case "kcp":
		k := &config.KCPSettings{Seed: s.Seed}
		if s.HeaderType != "" {
			k.Header = &config.KCPHeader{Type: s.HeaderType}
		}
		st.KCP = k
	case "quic":
		q := &config.QUICSettings{Key: s.QUICKey, Security: s.QUICSecurity}
		if s.HeaderType != "" {
			q.Header = &config.QUICHeader{Type: s.HeaderType}
		}
		st.QUIC = q
	case "xhttp":
		st.XHTTP = &config.XHTTPSettings{Path: s.Path, Host: s.Host, Mode: s.XHTTPMode}
	case "httpupgrade":
		st.HTTPUpgrade = &config.HTTPUpgradeSettings{Path: s.Path, Host: s.Host}
	}
	switch s.Security {
	case "tls":
		st.TLS = &config.TLSSettings{
			ServerName: s.SNI,
			// allowInsecure is deliberately NOT propagated from the subscription
			// URI: a subscription-sourced node must not be able to disable TLS
			// verification (the strip is audited in ToConfigNodes). Operator-
			// authored nodes set this field directly, bypassing this adapter.
			AllowInsecure: false,
			ALPN:          s.ALPN,
			Fingerprint:   s.Fingerprint,
		}
	case "reality":
		st.REALITY = &config.REALITYSettings{
			ServerName:  s.SNI,
			PublicKey:   s.PublicKey,
			ShortID:     s.ShortID,
			SpiderX:     s.SpiderX,
			Fingerprint: s.Fingerprint,
		}
	}
	return st
}

// Errorf is exported so callers in the engine wrapper can return rich errors
// without importing fmt directly. Trivial passthrough.
func Errorf(format string, args ...any) error { return fmt.Errorf(format, args...) }
