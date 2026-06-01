package subscription

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// parseVMess handles the V2RayN-flavor VMess URI:
//
//	vmess://<base64-of-json>
//
// where JSON is the V2RayN node descriptor. We tolerate both std and url-safe
// base64, with or without padding.
func parseVMess(s string) (ParsedNode, error) {
	const prefix = "vmess://"
	if !strings.HasPrefix(s, prefix) {
		return ParsedNode{}, fmt.Errorf("vmess: missing scheme")
	}
	body := s[len(prefix):]
	if i := strings.Index(body, "#"); i >= 0 {
		// V2RayN never puts # in the body; some custom providers do.
		body = body[:i]
	}
	dec, err := decodeBase64Tolerant([]byte(body))
	if err != nil {
		return ParsedNode{}, fmt.Errorf("vmess: base64: %w", err)
	}
	// V2RayN JSON keys (all strings in source, despite being numeric-looking):
	type raw struct {
		V    string      `json:"v"`
		PS   string      `json:"ps"`
		Add  string      `json:"add"`
		Port json.Number `json:"port"`
		ID   string      `json:"id"`
		Aid  json.Number `json:"aid"`
		Scy  string      `json:"scy"`
		Net  string      `json:"net"`
		Type string      `json:"type"`
		Host string      `json:"host"`
		Path string      `json:"path"`
		TLS  string      `json:"tls"`
		SNI  string      `json:"sni"`
		ALPN string      `json:"alpn"`
		FP   string      `json:"fp"`
		// REALITY-on-vmess (rare, but observed)
		PBK string `json:"pbk"`
		SID string `json:"sid"`
		SPX string `json:"spx"`
		// gRPC mode (gun|multi). For grpc, ServiceName per V2RayN convention
		// is sent via the Path field — see "if r.Net == grpc" below.
		Mode string `json:"mode"`
	}
	var r raw
	dec2 := json.NewDecoder(strings.NewReader(string(dec)))
	dec2.UseNumber()
	if err := dec2.Decode(&r); err != nil {
		return ParsedNode{}, fmt.Errorf("vmess: json: %w", err)
	}
	port, _ := strconv.Atoi(r.Port.String())
	aid, _ := strconv.Atoi(r.Aid.String())
	node := ParsedNode{
		Protocol:      "vmess",
		Server:        r.Add,
		Port:          port,
		Remark:        r.PS,
		RawURI:        s,
		UnknownParams: map[string]string{},
		VMess: &ParsedVMess{
			UUID:     r.ID,
			Security: orDefault(r.Scy, ""),
			AlterID:  aid,
		},
		Stream: ParsedStream{
			Transport:   orDefault(r.Net, "tcp"),
			Security:    r.TLS,
			SNI:         firstNonEmpty(r.SNI, r.Host),
			Fingerprint: r.FP,
			Host:        r.Host,
			Path:        r.Path,
			TCPHeaderType: r.Type,
			HeaderType:    r.Type,
			PublicKey:     r.PBK,
			ShortID:       r.SID,
			SpiderX:       r.SPX,
			GRPCMode:      r.Mode,
		},
	}
	if r.ALPN != "" {
		for _, p := range strings.Split(r.ALPN, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				node.Stream.ALPN = append(node.Stream.ALPN, p)
			}
		}
	}
	// gRPC service name lives in r.Path when net=grpc per V2RayN convention.
	if r.Net == "grpc" {
		node.Stream.ServiceName = r.Path
	}
	return node, nil
}
