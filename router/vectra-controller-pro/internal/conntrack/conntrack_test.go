package conntrack

import (
	"net/netip"
	"strings"
	"testing"
)

const sample = `ipv4     2 tcp      6 52 SYN_SENT src=198.51.100.7 dst=203.0.113.5 sport=51054 dport=50055 packets=1 bytes=60 [UNREPLIED] src=203.0.113.5 dst=198.51.100.7 sport=50055 dport=51054 packets=0 bytes=0 mark=0 zone=0 use=2
ipv4     2 tcp      6 7431 ESTABLISHED src=192.168.1.141 dst=203.0.113.9 sport=58843 dport=40060 packets=1017 bytes=923632 src=203.0.113.9 dst=198.51.100.7 sport=40060 dport=58843 packets=1096 bytes=88041 [OFFLOAD] mark=268435456 zone=0 use=3
ipv4     2 udp      17 28 src=198.51.100.7 dst=203.0.113.6 sport=41000 dport=50052 packets=3 bytes=900 [UNREPLIED] src=203.0.113.6 dst=198.51.100.7 sport=50052 dport=41000 packets=0 bytes=0 mark=0 zone=0 use=2
ipv6     10 tcp      6 100 ESTABLISHED src=2001:db8::1 dst=2001:db8::2 sport=1 dport=443 packets=1 bytes=1 src=2001:db8::2 dst=2001:db8::1 sport=443 dport=1 packets=1 bytes=1 [ASSURED] mark=0 zone=0 use=2
ipv4     2 icmp     1 29 src=192.168.1.1 dst=8.8.8.8 type=8 code=0 id=1 packets=1 bytes=84 src=8.8.8.8 dst=192.168.1.1 type=0 code=0 id=1 packets=1 bytes=84 mark=0 zone=0 use=2
garbage
ipv4     2 tcp      6 10 SYN_SENT src=198.51.100.7 dst=203.0.113.5`

func TestParseReadsTheOriginalTuple(t *testing.T) {
	es := Parse(strings.NewReader(sample))
	if len(es) != 4 {
		t.Fatalf("entries %d: %+v", len(es), es)
	}
	a := es[0]
	if a.Proto != "tcp" || a.State != "SYN_SENT" || a.Replied || a.Dst != netip.MustParseAddr("203.0.113.5") || a.DPort != 50055 || a.SPort != 51054 {
		t.Fatalf("%+v", a)
	}
	if !es[1].Replied || es[1].DPort != 40060 || es[1].State != "ESTABLISHED" {
		t.Fatalf("%+v", es[1])
	}
	if es[2].Proto != "udp" || es[2].Replied || es[2].DPort != 50052 || es[2].State != "" {
		t.Fatalf("%+v", es[2])
	}
	if es[3].Dst != netip.MustParseAddr("2001:db8::2") || !es[3].Replied {
		t.Fatalf("%+v", es[3])
	}
}

// net.netfilter.nf_conntrack_acct=0 prints no packets=/bytes=; a line cut
// before its reply tuple says nothing of whether it was answered.
func TestParseReadsLinesWithoutCounters(t *testing.T) {
	const noAcct = `ipv4     2 tcp      6 117 SYN_SENT src=198.51.100.7 dst=203.0.113.5 sport=51055 dport=50055 [UNREPLIED] src=203.0.113.5 dst=198.51.100.7 sport=50055 dport=51055 mark=0 zone=0 use=2
ipv4     2 tcp      6 7431 ESTABLISHED src=198.51.100.7 dst=203.0.113.9 sport=58843 dport=40060 src=203.0.113.9 dst=198.51.100.7 sport=40060 dport=58843 [ASSURED] mark=0 zone=0 use=3
ipv4     2 tcp      6 117 SYN_SENT src=198.51.100.7 dst=203.0.113.5 sport=51056 dport=50055`
	es := Parse(strings.NewReader(noAcct))
	if len(es) != 2 || es[0].Replied || es[0].SPort != 51055 || !es[1].Replied || es[1].DPort != 40060 {
		t.Fatalf("%+v", es)
	}
}
