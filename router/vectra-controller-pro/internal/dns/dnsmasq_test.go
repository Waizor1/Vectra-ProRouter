package dns

import (
	"strings"
	"testing"
)

func TestRender_FleetExample(t *testing.T) {
	c := SplitConfig{
		LocalListen:     "127.0.0.1",
		UpstreamServers: []string{"127.0.0.1#5353"},
		CacheSize:       1500,
		PerDomain: []DomainGroup{
			{Comment: "China-direct", Domains: []string{"geosite-cn"}, SetV4: "vctl_direct4", SetV6: "vctl_direct6"},
			{Comment: "WorldProxy", Domains: []string{"youtube.com", "googlevideo.com"}, SetV4: "vctl_proxy4", SetV6: "vctl_proxy6"},
		},
	}
	out := Render(c).Content
	for _, must := range []string{
		"listen-address=127.0.0.1",
		"server=127.0.0.1#5353",
		"cache-size=1500",
		"nftset=/geosite-cn/4#inet#vctl#vctl_direct4,6#inet#vctl#vctl_direct6",
		"nftset=/youtube.com/googlevideo.com/4#inet#vctl#vctl_proxy4,6#inet#vctl#vctl_proxy6",
	} {
		if !strings.Contains(out, must) {
			t.Errorf("expected %q in:\n%s", must, out)
		}
	}
}
