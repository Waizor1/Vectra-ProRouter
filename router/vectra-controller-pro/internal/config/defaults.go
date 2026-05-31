package config

// ApplyDefaults fills in defaults for fields the operator left unset.
// It NEVER overwrites a non-zero operator value. Every default applied
// is the controller's "we have to put something here for Xray" choice,
// and is loggable via DefaultsApplied(before, after).
func ApplyDefaults(c *Config) {
	if c == nil {
		return
	}
	if c.Schema == 0 {
		c.Schema = SchemaVersion
	}
	if c.Instance.LogLevel == "" {
		c.Instance.LogLevel = "warning"
	}

	// Process defaults
	p := &c.Process
	if p.XrayBinary == "" {
		p.XrayBinary = "/usr/bin/xray"
	}
	if p.WorkDir == "" {
		p.WorkDir = "/var/run/vectra-controller-pro"
	}
	if p.ConfigFile == "" {
		p.ConfigFile = p.WorkDir + "/xray.json"
	}
	if p.LogDir == "" {
		p.LogDir = "/var/log/vectra-controller-pro"
	}
	if p.OOMScoreAdj == 0 {
		// Slightly less likely to be OOM-killed than default (0).
		// -1000 would make us unkillable which is anti-social; -100 is a sane "important but not critical" hint.
		p.OOMScoreAdj = -100
	}
	if p.RestartBackoff.InitialMs == 0 {
		p.RestartBackoff.InitialMs = 500
	}
	if p.RestartBackoff.Factor == 0 {
		p.RestartBackoff.Factor = 2.0
	}
	if p.RestartBackoff.MaxMs == 0 {
		p.RestartBackoff.MaxMs = 60_000
	}
	if p.RestartBackoff.Reset == "" {
		p.RestartBackoff.Reset = "60s"
	}
	if p.ReloadGrace == "" {
		p.ReloadGrace = "5s"
	}
	if p.StartTimeout == "" {
		p.StartTimeout = "15s"
	}

	// Inbound tproxy defaults
	if c.Inbounds.Tproxy != nil {
		t := c.Inbounds.Tproxy
		if t.ListenIP == "" {
			t.ListenIP = "0.0.0.0"
		}
		if t.Port == 0 {
			t.Port = 12345
		}
		if t.FwMark == 0 {
			t.FwMark = 0x1
		}
		if t.Tag == "" {
			t.Tag = "tproxy-in"
		}
	}
	if c.Inbounds.Socks != nil && c.Inbounds.Socks.Tag == "" {
		c.Inbounds.Socks.Tag = "socks-in"
	}
	if c.Inbounds.HTTP != nil && c.Inbounds.HTTP.Tag == "" {
		c.Inbounds.HTTP.Tag = "http-in"
	}
	if c.Inbounds.DNS != nil && c.Inbounds.DNS.Tag == "" {
		c.Inbounds.DNS.Tag = "dns-in"
	}

	// DNS defaults
	if c.DNS.QueryStrategy == "" {
		c.DNS.QueryStrategy = "UseIPv4"
	}

	// Geo defaults
	if c.Geo.AssetDir == "" {
		c.Geo.AssetDir = "/usr/share/xray"
	}

	// Node defaults: backfill Tag from ID if empty.
	for i := range c.Nodes {
		if c.Nodes[i].Tag == "" && c.Nodes[i].ID != "" {
			c.Nodes[i].Tag = "node-" + c.Nodes[i].ID
		}
	}

	// Subscription fetch defaults
	for i := range c.Subscriptions {
		f := &c.Subscriptions[i].Fetch
		if f.ConnectTimeoutS == 0 {
			f.ConnectTimeoutS = 5
		}
		if f.MaxTimeoutS == 0 {
			f.MaxTimeoutS = 30
		}
		if f.Retries == 0 {
			f.Retries = 2
		}
		if f.Mode == "" {
			f.Mode = "auto"
		}
	}
}

// DefaultsDiff returns a list of human-readable strings describing every
// field that ApplyDefaults would change on c. Useful for `vctl render --verbose`.
func DefaultsDiff(c *Config) []string {
	if c == nil {
		return nil
	}
	dup, err := Clone(c)
	if err != nil {
		return []string{"clone failed: " + err.Error()}
	}
	ApplyDefaults(dup)
	var diffs []string
	if c.Process.OOMScoreAdj == 0 && dup.Process.OOMScoreAdj != 0 {
		diffs = append(diffs, "process.oomScoreAdj: 0 -> -100 (default)")
	}
	if c.DNS.QueryStrategy == "" && dup.DNS.QueryStrategy != "" {
		diffs = append(diffs, "dns.queryStrategy: \"\" -> \""+dup.DNS.QueryStrategy+"\" (default)")
	}
	if c.Geo.AssetDir == "" && dup.Geo.AssetDir != "" {
		diffs = append(diffs, "geo.assetDir: \"\" -> \""+dup.Geo.AssetDir+"\" (default)")
	}
	if c.Process.XrayBinary == "" {
		diffs = append(diffs, "process.xrayBinary: \"\" -> \""+dup.Process.XrayBinary+"\" (default)")
	}
	if c.Process.WorkDir == "" {
		diffs = append(diffs, "process.workDir: \"\" -> \""+dup.Process.WorkDir+"\" (default)")
	}
	for i := range c.Nodes {
		if c.Nodes[i].Tag == "" && dup.Nodes[i].Tag != "" {
			diffs = append(diffs, "nodes["+c.Nodes[i].ID+"].tag: \"\" -> \""+dup.Nodes[i].Tag+"\" (default = node-<id>)")
		}
	}
	return diffs
}
