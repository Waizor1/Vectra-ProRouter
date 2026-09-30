package xray

import (
	"encoding/json"
	"fmt"

	"vectra-controller-pro/internal/config"
)

// The Xray-native inbound shapes below are the only ones the controller still
// emits. They are lifted from the deleted full builder (render_inbound.go's
// tproxy branch plus the xInbound/xSniffing/xSockopt types) and trimmed to the
// fields a dokodemo-door TPROXY inbound actually sets — carrying the other ~40
// stream/sockopt fields would be dead code.

type xInbound struct {
	Tag            string            `json:"tag,omitempty"`
	Listen         string            `json:"listen,omitempty"`
	Port           int               `json:"port,omitempty"`
	Protocol       string            `json:"protocol"`
	Settings       xDokodemoSettings `json:"settings"`
	StreamSettings *xStreamSettings  `json:"streamSettings,omitempty"`
	Sniffing       *xSniffing        `json:"sniffing,omitempty"`
}

type xDokodemoSettings struct {
	Network        string `json:"network,omitempty"`
	FollowRedirect bool   `json:"followRedirect,omitempty"`
}

type xStreamSettings struct {
	Sockopt *xSockopt `json:"sockopt,omitempty"`
}

type xSockopt struct {
	Mark   int    `json:"mark,omitempty"`
	TProxy string `json:"tproxy,omitempty"`
}

type xSniffing struct {
	Enabled         bool     `json:"enabled"`
	DestOverride    []string `json:"destOverride,omitempty"`
	DomainsExcluded []string `json:"domainsExcluded,omitempty"`
	MetadataOnly    bool     `json:"metadataOnly,omitempty"`
	RouteOnly       bool     `json:"routeOnly,omitempty"`
}

// TproxyInboundJSON renders the controller's single transparent-proxy inbound.
//
// The tag is fresh (default "tproxy-in"), which is safe by construction: the
// provider's routing rules only ever filter on their internal STAGE_* inbound
// tags, so a new entry-inbound tag matches no rule and needs no routing change.
func TproxyInboundJSON(t *config.TproxyInbound) ([]byte, error) {
	if t == nil {
		return nil, fmt.Errorf("xray: nil tproxy inbound")
	}
	if t.Port <= 0 || t.Port > 65535 {
		return nil, fmt.Errorf("xray: tproxy inbound port out of range: %d", t.Port)
	}
	network := "tcp"
	if t.UDPEnabled {
		network = "tcp,udp"
	}
	tag := inboundTagOf(t)
	listen := t.ListenIP
	if listen == "" {
		listen = "0.0.0.0"
	}
	ib := xInbound{
		Tag:      tag,
		Listen:   listen,
		Port:     t.Port,
		Protocol: "dokodemo-door",
		Settings: xDokodemoSettings{
			Network:        network,
			FollowRedirect: true,
		},
		StreamSettings: &xStreamSettings{
			// NOT t.FwMark: a socket carrying the tproxy fwmark resolves to the
			// `local 0.0.0.0/0 dev lo` policy route, so the inbound's replies to
			// the client would be looped back into this very socket instead of
			// being delivered. See config.DefaultXraySockMark.
			Sockopt: &xSockopt{TProxy: "tproxy", Mark: config.DefaultXraySockMark},
		},
		Sniffing: convertSniffing(t.Sniffing),
	}
	return json.Marshal(ib)
}

// inboundTagOf is the tag TproxyInboundJSON gives the inbound.
func inboundTagOf(t *config.TproxyInbound) string {
	if t == nil || t.Tag == "" {
		return "tproxy-in"
	}
	return t.Tag
}

func convertSniffing(s config.Sniffing) *xSniffing {
	if !s.Enabled {
		return nil
	}
	return &xSniffing{
		Enabled:         true,
		DestOverride:    s.DestOverride,
		DomainsExcluded: s.DomainsExcluded,
		MetadataOnly:    s.MetadataOnly,
		RouteOnly:       s.RouteOnly,
	}
}
