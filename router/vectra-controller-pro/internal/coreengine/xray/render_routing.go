package xray

import "vectra-controller-pro/internal/config"

func buildRouting(c *config.Config) *xRouting {
	out := &xRouting{
		DomainStrategy: c.Routing.DomainStrategy,
		DomainMatcher:  c.Routing.DomainMatcher,
		Rules:          make([]xRoutingRule, 0, len(c.Routing.Rules)+2),
	}

	// Synthesize a "dns-out" rule so any traffic from the DNS inbound goes to the dns-out outbound.
	if c.Inbounds.DNS != nil {
		out.Rules = append(out.Rules, xRoutingRule{
			Type:        "field",
			InboundTag:  []string{c.Inbounds.DNS.Tag},
			OutboundTag: "dns-out",
		})
	}
	// Synthesize an "api" rule so the API inbound routes to the api outbound.
	if c.API != nil && c.API.Tag != "" {
		out.Rules = append(out.Rules, xRoutingRule{
			Type:        "field",
			InboundTag:  []string{c.API.Tag},
			OutboundTag: c.API.Tag,
		})
	}
	// Synthesize a metrics rule when a metrics inbound is synthesized (listen set),
	// so scrape traffic on the metrics inbound is handled by the metrics tag.
	if c.Metrics != nil && c.Metrics.Tag != "" && c.Metrics.Listen != "" {
		out.Rules = append(out.Rules, xRoutingRule{
			Type:        "field",
			InboundTag:  []string{c.Metrics.Tag},
			OutboundTag: c.Metrics.Tag,
		})
	}
	for _, r := range c.Routing.Rules {
		t := r.Type
		if t == "" {
			t = "field"
		}
		out.Rules = append(out.Rules, xRoutingRule{
			Type:        t,
			Domain:      r.Domain,
			Domains:     r.Domains,
			IP:          r.IP,
			Port:        r.Port,
			SourcePort:  r.SourcePort,
			Network:     r.Network,
			Source:      r.Source,
			User:        r.User,
			InboundTag:  r.InboundTag,
			Protocol:    r.Protocol,
			Attrs:       r.Attrs,
			OutboundTag: r.OutboundTag,
			BalancerTag: r.BalancerTag,
			// Emit ruleTag so API-driven rerouting (HandlerService/RoutingService)
			// can target this rule by name. Operator-set label only.
			RuleTag: r.Tag,
		})
	}
	for _, b := range c.Routing.Balancers {
		var strat *xBalancerStrategy
		if b.Strategy != nil {
			strat = &xBalancerStrategy{Type: b.Strategy.Type, Settings: b.Strategy.Settings}
		}
		out.Balancers = append(out.Balancers, xBalancer{
			Tag: b.Tag, Selector: b.Selector, FallbackTag: b.FallbackTag, Strategy: strat,
		})
	}
	return out
}
