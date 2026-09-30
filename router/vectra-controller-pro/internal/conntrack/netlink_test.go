package conntrack

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

// The kernel's own encoding, built by hand: attributes in host order, their
// payloads (addresses, ports, status) in network order, nests flagged.

func nlAttr(typ uint16, payload []byte) []byte {
	l := 4 + len(payload)
	b := make([]byte, (l+3)&^3)
	binary.NativeEndian.PutUint16(b[0:], uint16(l))
	binary.NativeEndian.PutUint16(b[2:], typ)
	copy(b[4:], payload)
	return b
}

func nlNest(typ uint16, children ...[]byte) []byte {
	return nlAttr(typ|nlaFNested, bytes.Join(children, nil))
}

func be16(v uint16) []byte { b := make([]byte, 2); binary.BigEndian.PutUint16(b, v); return b }
func be32(v uint32) []byte { b := make([]byte, 4); binary.BigEndian.PutUint32(b, v); return b }

func nlMsg(typ, flags uint16, seq uint32, body []byte) []byte {
	l := 16 + len(body)
	b := make([]byte, (l+3)&^3)
	binary.NativeEndian.PutUint32(b[0:], uint32(l))
	binary.NativeEndian.PutUint16(b[4:], typ)
	binary.NativeEndian.PutUint16(b[6:], flags)
	binary.NativeEndian.PutUint32(b[8:], seq)
	copy(b[16:], body)
	return b
}

func nlTuple(typ uint16, src, dst string, proto byte, sport, dport uint16) []byte {
	s, d := netip.MustParseAddr(src), netip.MustParseAddr(dst)
	ip := nlNest(ctaTupleIP, nlAttr(ctaIPv4Src, s.AsSlice()), nlAttr(ctaIPv4Dst, d.AsSlice()))
	if s.Is6() {
		ip = nlNest(ctaTupleIP, nlAttr(ctaIPv6Src, s.AsSlice()), nlAttr(ctaIPv6Dst, d.AsSlice()))
	}
	return nlNest(typ, ip, nlNest(ctaTupleProto, nlAttr(ctaProtoNum, []byte{proto}),
		nlAttr(ctaProtoSrcPort, be16(sport)), nlAttr(ctaProtoDstPort, be16(dport))))
}

// nlEntry is one IPCTNL_MSG_CT_NEW of a dump: the reply tuple FIRST, so a
// reader that takes whichever tuple comes first is caught.
func nlEntry(seq uint32, family byte, orig, reply []byte, status uint32, tcpState int) []byte {
	body := []byte{family, 0, 0, 0}
	body = append(body, reply...)
	body = append(body, orig...)
	body = append(body, nlAttr(ctaStatus, be32(status))...)
	body = append(body, nlAttr(7 /* CTA_TIMEOUT */, be32(117))...)
	if tcpState >= 0 {
		body = append(body, nlNest(ctaProtoinfo, nlNest(ctaProtoinfoTCP, nlAttr(ctaProtoinfoTCPState, []byte{byte(tcpState)})))...)
	}
	return nlMsg(ctnlMsgNew, nlmFMulti, seq, body)
}

func TestDumpRequestAsksCtnetlinkForTheWholeTable(t *testing.T) {
	b := dumpRequest(42)
	if len(b) != 20 {
		t.Fatalf("len %d", len(b))
	}
	if l := binary.NativeEndian.Uint32(b[0:]); l != 20 {
		t.Fatalf("nlmsg_len %d", l)
	}
	if typ := binary.NativeEndian.Uint16(b[4:]); typ != 0x0101 { // CTNETLINK << 8 | IPCTNL_MSG_CT_GET
		t.Fatalf("type %#x", typ)
	}
	if fl := binary.NativeEndian.Uint16(b[6:]); fl != 0x0301 { // NLM_F_REQUEST | NLM_F_DUMP
		t.Fatalf("flags %#x", fl)
	}
	if seq := binary.NativeEndian.Uint32(b[8:]); seq != 42 {
		t.Fatalf("seq %d", seq)
	}
	if !bytes.Equal(b[16:], []byte{0, 0, 0, 0}) { // AF_UNSPEC: both families
		t.Fatalf("nfgenmsg % x", b[16:])
	}
}

func TestParseDumpReadsTheOriginalTupleAndWhetherItWasAnswered(t *testing.T) {
	const seq = 9
	var buf []byte
	// xray's attempt to a node that never answered.
	buf = append(buf, nlEntry(seq, 2,
		nlTuple(ctaTupleOrig, "198.51.100.7", "203.0.113.5", 6, 51054, 50055),
		nlTuple(ctaTupleReply, "203.0.113.5", "198.51.100.7", 6, 50055, 51054),
		ipsConfirmed, tcpSynSent)...)
	// A hysteria2 flow that was answered, over IPv6.
	buf = append(buf, nlEntry(seq, 10,
		nlTuple(ctaTupleOrig, "2001:db8::1", "2001:db8::2", 17, 41000, 443),
		nlTuple(ctaTupleReply, "2001:db8::2", "2001:db8::1", 17, 443, 41000),
		ipsConfirmed|ipsSeenReply, -1)...)
	// Neither tcp nor udp: skipped.
	buf = append(buf, nlEntry(seq, 2,
		nlTuple(ctaTupleOrig, "192.168.1.1", "8.8.8.8", 1, 0, 0),
		nlTuple(ctaTupleReply, "8.8.8.8", "192.168.1.1", 1, 0, 0),
		ipsSeenReply, -1)...)
	// A message of another request: not this dump's.
	buf = append(buf, nlEntry(seq+1, 2,
		nlTuple(ctaTupleOrig, "198.51.100.7", "203.0.113.99", 6, 1, 2),
		nlTuple(ctaTupleReply, "203.0.113.99", "198.51.100.7", 6, 2, 1),
		ipsSeenReply, tcpEstablished)...)

	var got []Entry
	done, err := parseDump(buf, seq, func(e Entry) { got = append(got, e) })
	if err != nil || done {
		t.Fatalf("done=%v err=%v", done, err)
	}
	want := []Entry{
		{Proto: "tcp", State: "SYN_SENT", Src: netip.MustParseAddr("198.51.100.7"), Dst: netip.MustParseAddr("203.0.113.5"), SPort: 51054, DPort: 50055, Replied: false},
		{Proto: "udp", Src: netip.MustParseAddr("2001:db8::1"), Dst: netip.MustParseAddr("2001:db8::2"), SPort: 41000, DPort: 443, Replied: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entry %d: got %+v want %+v", i, got[i], want[i])
		}
	}

	done, err = parseDump(nlMsg(nlmsgDone, nlmFMulti, seq, []byte{0, 0, 0, 0}), seq, func(Entry) {})
	if err != nil || !done {
		t.Fatalf("NLMSG_DONE: done=%v err=%v", done, err)
	}
}

func TestParseDumpTakesTheKernelsRefusalAsNoCtnetlink(t *testing.T) {
	errno := make([]byte, 4)
	binary.NativeEndian.PutUint32(errno, uint32(-22&0xffffffff)) // -EINVAL: no ctnetlink subsystem
	body := append(errno, dumpRequest(5)[:16]...)
	_, err := parseDump(nlMsg(nlmsgError, 0, 5, body), 5, func(Entry) {})
	if !errors.Is(err, errNoNetlink) {
		t.Fatalf("err %v", err)
	}
}

func TestParseDumpSkipsATruncatedMessage(t *testing.T) {
	m := nlEntry(3, 2,
		nlTuple(ctaTupleOrig, "198.51.100.7", "203.0.113.5", 6, 1, 50055),
		nlTuple(ctaTupleReply, "203.0.113.5", "198.51.100.7", 6, 50055, 1),
		0, tcpSynSent)
	binary.NativeEndian.PutUint32(m[0:], uint32(len(m)+40)) // claims more than there is
	var got []Entry
	if _, err := parseDump(m, 3, func(e Entry) { got = append(got, e) }); err == nil || len(got) != 0 {
		t.Fatalf("err=%v got=%+v", err, got)
	}
}

// swapNetlink stubs the netlink dump and resets the sticky fallback around a test.
func swapNetlink(t *testing.T, f func() ([]Entry, error)) *int {
	t.Helper()
	calls := 0
	prev, prevPath := dumpNetlink, Path
	dumpNetlink = func() ([]Entry, error) { calls++; return f() }
	procOnly.Store(false)
	t.Cleanup(func() { dumpNetlink, Path = prev, prevPath; procOnly.Store(false) })
	return &calls
}

func procFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "nf_conntrack")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const procLine = "ipv4     2 tcp      6 52 SYN_SENT src=198.51.100.7 dst=203.0.113.5 sport=51054 dport=50055 packets=1 bytes=60 [UNREPLIED] src=203.0.113.5 dst=198.51.100.7 sport=50055 dport=51054 packets=0 bytes=0 mark=0 zone=0 use=2\n"

func TestReadTakesNetlinkWhenItAnswers(t *testing.T) {
	nl := []Entry{{Proto: "udp", Src: netip.MustParseAddr("10.0.0.1"), Dst: netip.MustParseAddr("10.0.0.2"), SPort: 1, DPort: 2, Replied: true}}
	calls := swapNetlink(t, func() ([]Entry, error) { return nl, nil })
	Path = filepath.Join(t.TempDir(), "absent")
	es, err := Read()
	if err != nil || len(es) != 1 || es[0] != nl[0] || *calls != 1 {
		t.Fatalf("es=%+v err=%v calls=%d", es, err, *calls)
	}
}

func TestReadFallsBackToTheProcFileForGoodWithoutCtnetlink(t *testing.T) {
	calls := swapNetlink(t, func() ([]Entry, error) { return nil, errNoNetlink })
	Path = procFile(t, procLine)
	for i := 0; i < 3; i++ {
		es, err := Read()
		if err != nil || len(es) != 1 || es[0].DPort != 50055 || es[0].Replied {
			t.Fatalf("read %d: es=%+v err=%v", i, es, err)
		}
	}
	if *calls != 1 {
		t.Fatalf("netlink asked %d times; a kernel without ctnetlink is asked once", *calls)
	}
}

func TestReadAsksNetlinkAgainAfterATransientFailure(t *testing.T) {
	n := 0
	calls := swapNetlink(t, func() ([]Entry, error) {
		n++
		if n == 1 {
			return nil, errors.New("recvfrom: resource temporarily unavailable")
		}
		return []Entry{}, nil
	})
	Path = procFile(t, procLine)
	if es, err := Read(); err != nil || len(es) != 1 {
		t.Fatalf("first read (proc file this once): es=%+v err=%v", es, err)
	}
	if es, err := Read(); err != nil || len(es) != 0 || *calls != 2 {
		t.Fatalf("second read: es=%+v err=%v calls=%d", es, err, *calls)
	}
}
