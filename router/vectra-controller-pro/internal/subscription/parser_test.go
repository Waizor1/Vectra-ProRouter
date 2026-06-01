package subscription

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestDecodeBody_Base64LinkList(t *testing.T) {
	links := "vless://uuid@host.example:443?security=reality&type=tcp#node-1\nvless://uuid@host2.example:443?security=reality&type=grpc&serviceName=svc#node-2"
	body := base64.StdEncoding.EncodeToString([]byte(links))
	dec, format := DecodeBody([]byte(body))
	if format != "base64-link-list" {
		t.Fatalf("format=%s want base64-link-list", format)
	}
	if dec != links {
		t.Fatalf("decoded mismatch")
	}
}

func TestDecodeBody_PlainLinkList(t *testing.T) {
	links := "vless://uuid@host:443?security=reality#a\nvless://uuid@host2:443#b"
	_, format := DecodeBody([]byte(links))
	if format != "plain-link-list" {
		t.Fatalf("format=%s want plain-link-list", format)
	}
}

func TestDecodeBody_WrappedBase64(t *testing.T) {
	links := "vless://uuid@host.example:443?security=reality&type=tcp#node-1"
	body := base64.StdEncoding.EncodeToString([]byte(links))
	// Wrap every 30 chars to simulate provider wrapping.
	var wrapped strings.Builder
	for i := 0; i < len(body); i += 30 {
		end := i + 30
		if end > len(body) {
			end = len(body)
		}
		wrapped.WriteString(body[i:end])
		wrapped.WriteByte('\n')
	}
	dec, format := DecodeBody([]byte(wrapped.String()))
	if format != "base64-link-list" {
		t.Fatalf("format=%s", format)
	}
	if dec != links {
		t.Fatalf("decoded mismatch")
	}
}

func TestParseVLESS_RealityTCPVision(t *testing.T) {
	u := "vless://aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee@de1.example:443?encryption=none&flow=xtls-rprx-vision&type=tcp&security=reality&sni=www.cloudflare.com&pbk=KEY&sid=ID&spx=%2F&fp=firefox#WorldProxy"
	n, err := parseVLESS(u)
	if err != nil {
		t.Fatal(err)
	}
	if n.Protocol != "vless" || n.Server != "de1.example" || n.Port != 443 {
		t.Fatalf("server/port: %+v", n)
	}
	if n.VLESS == nil || n.VLESS.UUID != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Fatalf("vless uuid mismatch")
	}
	if n.VLESS.Flow != "xtls-rprx-vision" || n.VLESS.Encryption != "none" {
		t.Fatalf("flow/encryption: %+v", n.VLESS)
	}
	if n.Stream.Transport != "tcp" || n.Stream.Security != "reality" {
		t.Fatalf("transport/security: %+v", n.Stream)
	}
	if n.Stream.SNI != "www.cloudflare.com" || n.Stream.PublicKey != "KEY" || n.Stream.ShortID != "ID" || n.Stream.SpiderX != "/" {
		t.Fatalf("reality fields: %+v", n.Stream)
	}
	// CRITICAL: fingerprint preserved as 'firefox' — no chrome rewrite.
	if n.Stream.Fingerprint != "firefox" {
		t.Fatalf("fingerprint expected 'firefox' (no silent normalization), got %q", n.Stream.Fingerprint)
	}
	if n.Remark != "WorldProxy" {
		t.Fatalf("remark=%q", n.Remark)
	}
}

func TestParseVLESS_RealityGRPC(t *testing.T) {
	u := "vless://uu@pl1.example:443?encryption=none&type=grpc&serviceName=voice&mode=gun&security=reality&sni=microsoft.com&pbk=KEY&sid=00&fp=chrome#PL"
	n, err := parseVLESS(u)
	if err != nil {
		t.Fatal(err)
	}
	if n.Stream.Transport != "grpc" || n.Stream.ServiceName != "voice" || n.Stream.GRPCMode != "gun" {
		t.Fatalf("stream: %+v", n.Stream)
	}
}

func TestParseVMess_StandardV2RayN(t *testing.T) {
	js := `{"v":"2","ps":"vmess-1","add":"host.example","port":"443","id":"00000000-0000-0000-0000-000000000000","aid":"0","scy":"auto","net":"ws","type":"none","host":"www.example","path":"/v","tls":"tls","sni":"www.example","alpn":"","fp":"firefox"}`
	u := "vmess://" + base64.StdEncoding.EncodeToString([]byte(js))
	n, err := parseVMess(u)
	if err != nil {
		t.Fatal(err)
	}
	if n.Server != "host.example" || n.Port != 443 || n.VMess.UUID == "" {
		t.Fatalf("got: %+v / %+v", n, n.VMess)
	}
	if n.Stream.Transport != "ws" || n.Stream.Path != "/v" || n.Stream.Host != "www.example" {
		t.Fatalf("ws fields: %+v", n.Stream)
	}
	if n.Stream.Fingerprint != "firefox" {
		t.Fatalf("fp not preserved")
	}
}

func TestParseTrojan_TLSDefault(t *testing.T) {
	u := "trojan://pwd@srv.example:443?sni=srv.example&type=tcp#trojan-1"
	n, err := parseTrojan(u)
	if err != nil {
		t.Fatal(err)
	}
	if n.Trojan.Password != "pwd" || n.Server != "srv.example" || n.Port != 443 {
		t.Fatalf("bad: %+v", n)
	}
	if n.Stream.Security != "tls" {
		t.Fatalf("trojan must default to tls (Xray requirement), got %q", n.Stream.Security)
	}
}

func TestParseShadowsocks_SIP002(t *testing.T) {
	userinfo := base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:passw0rd"))
	u := "ss://" + userinfo + "@srv.example:8388#ss-1"
	n, err := parseShadowsocks(u)
	if err != nil {
		t.Fatal(err)
	}
	if n.Shadowsocks.Method != "aes-256-gcm" || n.Shadowsocks.Password != "passw0rd" {
		t.Fatalf("ss decode: %+v", n.Shadowsocks)
	}
	if n.Server != "srv.example" || n.Port != 8388 {
		t.Fatalf("host: %+v", n)
	}
	if n.Remark != "ss-1" {
		t.Fatalf("remark: %s", n.Remark)
	}
}

func TestParseShadowsocks_Legacy(t *testing.T) {
	u := "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:passw0rd@srv.example:8388")) + "#ss-2"
	n, err := parseShadowsocks(u)
	if err != nil {
		t.Fatal(err)
	}
	if n.Shadowsocks.Method != "aes-256-gcm" || n.Server != "srv.example" || n.Port != 8388 {
		t.Fatalf("legacy ss: %+v", n)
	}
}

func TestParseHysteria2(t *testing.T) {
	u := "hysteria2://pwd@h2.example:8443?obfs=salamander&obfs-password=ob&sni=h2.example&insecure=1&fp=chrome#hy2-1"
	n, err := parseHysteria2(u)
	if err != nil {
		t.Fatal(err)
	}
	if n.Hysteria2.Password != "pwd" || n.Hysteria2.Obfs != "salamander" || n.Hysteria2.ObfsPass != "ob" {
		t.Fatalf("hy2 fields: %+v", n.Hysteria2)
	}
	if !n.Stream.AllowInsec {
		t.Fatalf("insecure=1 must set AllowInsec true")
	}
	if n.Stream.Fingerprint != "chrome" {
		t.Fatalf("fp")
	}
}

func TestParseBody_MixedFleetSample(t *testing.T) {
	links := strings.Join([]string{
		"vless://u1@h1:443?type=tcp&security=reality&sni=s&pbk=K&sid=I&spx=%2F&flow=xtls-rprx-vision&fp=firefox#one",
		"vless://u2@h2:443?type=grpc&serviceName=v&mode=gun&security=reality&sni=s&pbk=K&sid=I&fp=chrome#two",
		"trojan://pw@h3:443?sni=h3#three",
	}, "\n")
	body := base64.StdEncoding.EncodeToString([]byte(links))
	r := ParseBody([]byte(body))
	if r.BodyFormat != "base64-link-list" || len(r.Nodes) != 3 || len(r.UnparsedLines) != 0 {
		t.Fatalf("result: %+v", r)
	}
	// No-normalization sanity:
	if r.Nodes[0].Stream.Fingerprint != "firefox" {
		t.Fatalf("first node fp should remain 'firefox'")
	}
}

func TestParseBody_UnsupportedScheme_KeptInUnparsed(t *testing.T) {
	links := "vless://u@h:443?type=tcp#ok\nsocks://x@y:1080#nope"
	body := base64.StdEncoding.EncodeToString([]byte(links))
	r := ParseBody([]byte(body))
	if len(r.Nodes) != 1 {
		t.Fatalf("expect 1 parsed, got %d", len(r.Nodes))
	}
	if len(r.UnparsedLines) != 1 || !strings.Contains(r.UnparsedLines[0].Reason, "unsupported scheme") {
		t.Fatalf("unparsed mismatch: %+v", r.UnparsedLines)
	}
}

func TestComputeHWID_PassWall2Compatible(t *testing.T) {
	// Reference: sha256("cc:d8:43:b1:bd:0c-Xiaomi Mi Router AX3000T")
	got := ComputeHWID("cc:d8:43:b1:bd:0c", "Xiaomi Mi Router AX3000T")
	want := "760386f8b139baf471f566a22efed5a4cd24a2241636d84524bd30bc28c08b4a"
	if got != want {
		t.Fatalf("HWID mismatch:\n got  %s\n want %s", got, want)
	}
}

func TestParseUserInfo(t *testing.T) {
	v := "upload=0; download=132330565035; total=0; expire=2020712400"
	u := parseUserInfo(v)
	if u == nil || u.DownloadBytes != 132330565035 || u.ExpireAt.IsZero() {
		t.Fatalf("parse: %+v", u)
	}
}
