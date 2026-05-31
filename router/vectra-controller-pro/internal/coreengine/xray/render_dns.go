package xray

import "vectra-controller-pro/internal/config"

func buildDNS(c *config.Config) *xDNS {
	if len(c.DNS.Servers) == 0 && len(c.DNS.Hosts) == 0 {
		return nil
	}
	out := &xDNS{
		Hosts:                  c.DNS.Hosts,
		ClientIP:               c.DNS.ClientIP,
		QueryStrategy:          c.DNS.QueryStrategy,
		DisableCache:           c.DNS.DisableCache,
		DisableFallback:        c.DNS.DisableFallback,
		DisableFallbackIfMatch: c.DNS.DisableFallbackIfMatch,
		Tag:                    c.DNS.Tag,
	}
	for _, s := range c.DNS.Servers {
		// If only Address is set, emit as a bare string for compactness
		// (Xray accepts either form). Otherwise emit a structured object.
		if isBareServer(s) {
			out.Servers = append(out.Servers, s.Address)
			continue
		}
		out.Servers = append(out.Servers, xDNSServer{
			Address:       s.Address,
			Port:          s.Port,
			ClientIP:      s.ClientIP,
			SkipFallback:  s.SkipFallback,
			Domains:       s.Domains,
			ExpectIPs:     s.ExpectIPs,
			QueryStrategy: s.QueryStrategy,
			FinalQuery:    s.FinalQuery,
			Tag:           s.Tag,
		})
	}
	return out
}

func isBareServer(s config.DNSServer) bool {
	return s.Port == 0 && s.ClientIP == "" && !s.SkipFallback &&
		len(s.Domains) == 0 && len(s.ExpectIPs) == 0 &&
		s.QueryStrategy == "" && !s.FinalQuery && s.Tag == ""
}
