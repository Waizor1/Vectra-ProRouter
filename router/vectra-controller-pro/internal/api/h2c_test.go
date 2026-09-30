package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A scripted HTTP/2 peer: it reads the client preface and frames, and answers
// each request stream through reply(). It speaks just enough of the protocol
// to exercise the client's frame handling; the client's interoperability with
// the real thing is proven against xray 26.3.27 on the data-plane stand.
type fakePeer struct {
	t     *testing.T
	ln    net.Listener
	reply func(w *bufio.Writer, sid uint32, path string, req []byte)
	// before runs once after the preface, e.g. to send SETTINGS or a PING
	// the client must acknowledge.
	before func(w *bufio.Writer)
	acks   chan byte // frame types the client acknowledged
}

func newFakePeer(t *testing.T) *fakePeer {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &fakePeer{t: t, ln: ln, acks: make(chan byte, 16)}
	t.Cleanup(func() { ln.Close() })
	go p.serve()
	return p
}

func (p *fakePeer) addr() string { return p.ln.Addr().String() }

func (p *fakePeer) serve() {
	conn, err := p.ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	r, w := bufio.NewReader(conn), bufio.NewWriter(conn)
	pre := make([]byte, len(h2Preface))
	if _, err := io.ReadFull(r, pre); err != nil || string(pre) != h2Preface {
		p.t.Errorf("bad client preface %q", pre)
		return
	}
	if p.before != nil {
		p.before(w)
		_ = w.Flush()
	}
	paths := map[uint32]string{}
	for {
		typ, flags, sid, payload, err := readFrame(r)
		if err != nil {
			return
		}
		switch typ {
		case frameSettings, framePing:
			if flags&flagAck != 0 {
				p.acks <- typ
			}
		case frameHeaders:
			paths[sid] = decodeOurHPACK(p.t, payload)[":path"]
		case frameData:
			if len(payload) < 5 {
				p.t.Errorf("short grpc frame")
				return
			}
			p.reply(w, sid, paths[sid], payload[5:])
			_ = w.Flush()
		}
	}
}

// decodeOurHPACK decodes exactly the representations hpackRequest emits.
func decodeOurHPACK(t *testing.T, b []byte) map[string]string {
	static := map[int]string{1: ":authority", 4: ":path", 31: "content-type"}
	out := map[string]string{}
	readInt := func(prefix uint) int {
		max := 1<<prefix - 1
		v := int(b[0]) & max
		b = b[1:]
		if v < max {
			return v
		}
		m := 0
		for {
			c := b[0]
			b = b[1:]
			v += int(c&0x7f) << m
			m += 7
			if c < 0x80 {
				return v
			}
		}
	}
	readStr := func() string {
		if b[0]&0x80 != 0 {
			t.Fatal("client used Huffman coding")
		}
		n := readInt(7)
		s := string(b[:n])
		b = b[n:]
		return s
	}
	for len(b) > 0 {
		switch {
		case b[0] == 0x83:
			out[":method"] = "POST"
			b = b[1:]
		case b[0] == 0x86:
			out[":scheme"] = "http"
			b = b[1:]
		case b[0] == 0x00:
			b = b[1:]
			name := readStr()
			out[name] = readStr()
		case b[0]&0xf0 == 0x00:
			idx := readInt(4)
			out[static[idx]] = readStr()
		default:
			t.Fatalf("client used an HPACK representation it should not: %#x", b[0])
		}
	}
	return out
}

func grpcMessage(msg []byte) []byte {
	out := make([]byte, 5+len(msg))
	binary.BigEndian.PutUint32(out[1:5], uint32(len(msg)))
	copy(out[5:], msg)
	return out
}

func balancerReply(override string, principle ...string) []byte {
	var pt []byte
	for _, p := range principle {
		pt = pbString(pt, 1, p)
	}
	var bal []byte
	if override != "" {
		ov := pbString(nil, 2, override)
		bal = append(bal, pbVarint(nil, 5<<3|2)...)
		bal = append(pbVarint(bal, uint64(len(ov))), ov...)
	}
	bal = append(bal, pbVarint(nil, 6<<3|2)...)
	bal = append(pbVarint(bal, uint64(len(pt))), pt...)
	resp := append(pbVarint(nil, 1<<3|2), pbVarint(nil, uint64(len(bal)))...)
	return append(resp, bal...)
}

func TestGetBalancerInfoMultiplexesAndSurvivesUpkeepFrames(t *testing.T) {
	p := newFakePeer(t)
	p.before = func(w *bufio.Writer) {
		_ = writeFrame(w, frameSettings, 0, 0, []byte{0, 4, 0, 1, 0, 0}) // a real setting
		_ = writeFrame(w, framePing, 0, 0, []byte("12345678"))
	}
	p.reply = func(w *bufio.Writer, sid uint32, path string, req []byte) {
		if path != pathGetBalancerInfo {
			t.Errorf("path = %q", path)
		}
		tag, _ := pbField(req, 1)
		_ = writeFrame(w, frameHeaders, flagEndHeaders, sid, []byte{0x88}) // :status 200
		switch string(tag) {
		case "BL-MAIN":
			// Padded DATA, split across two frames, then trailers.
			m := grpcMessage(balancerReply("bridge-nl5", "bridge-de5", "bridge-nl5"))
			_ = writeFrame(w, frameData, flagPadded, sid, append(append([]byte{3}, m[:7]...), 0, 0, 0))
			_ = writeFrame(w, frameData, 0, sid, m[7:])
			_ = writeFrame(w, frameHeaders, flagEndHeaders|flagEndStream, sid, []byte{0x40})
		case "BL-EMPTY":
			_ = writeFrame(w, frameData, flagEndStream, sid, grpcMessage(balancerReply("")))
		default:
			// trailers-only: how xray answers an unknown balancer.
			_ = writeFrame(w, frameHeaders, flagEndHeaders|flagEndStream, sid, []byte{0x88})
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got, err := GetBalancerInfo(ctx, p.addr(), []string{"BL-MAIN", "NOPE", "BL-EMPTY"})
	if err != nil {
		t.Fatal(err)
	}
	if m := got["BL-MAIN"]; m.Err != nil || m.Override != "bridge-nl5" || strings.Join(m.Principle, ",") != "bridge-de5,bridge-nl5" {
		t.Errorf("BL-MAIN = %+v", m)
	}
	if e := got["BL-EMPTY"]; e.Err != nil || e.Override != "" || len(e.Principle) != 0 {
		t.Errorf("BL-EMPTY = %+v; an empty selection is not an error", e)
	}
	if n := got["NOPE"]; !errors.Is(n.Err, errNoMessage) {
		t.Errorf("NOPE = %+v; want the trailers-only error", n)
	}
	acked := map[byte]bool{}
	for len(acked) < 2 {
		select {
		case typ := <-p.acks:
			acked[typ] = true
		case <-time.After(time.Second):
			t.Fatalf("client acknowledged only %v; want SETTINGS and PING", acked)
		}
	}
	if !acked[frameSettings] || !acked[framePing] {
		t.Fatalf("acknowledged %v; want SETTINGS and PING", acked)
	}
}

func TestOverrideBalancerTargetReportsAnUnknownBalancer(t *testing.T) {
	p := newFakePeer(t)
	var gotBal, gotTarget string
	p.reply = func(w *bufio.Writer, sid uint32, path string, req []byte) {
		if path != pathOverrideBalancerTarget {
			t.Errorf("path = %q", path)
		}
		b, _ := pbField(req, 1)
		tg, _ := pbField(req, 2)
		gotBal, gotTarget = string(b), string(tg)
		_ = writeFrame(w, frameHeaders, flagEndHeaders|flagEndStream, sid, []byte{0x88})
	}
	err := OverrideBalancerTarget(context.Background(), p.addr(), "NOPE", "bridge-de5")
	if !errors.Is(err, ErrUnknownBalancer) {
		t.Fatalf("err = %v, want ErrUnknownBalancer", err)
	}
	if gotBal != "NOPE" || gotTarget != "bridge-de5" {
		t.Errorf("request = %q/%q", gotBal, gotTarget)
	}
}

func TestOverrideBalancerTargetSucceedsOnAnEmptyReply(t *testing.T) {
	p := newFakePeer(t)
	p.reply = func(w *bufio.Writer, sid uint32, _ string, req []byte) {
		if tg, _ := pbField(req, 2); tg != nil {
			t.Errorf("unpin sent a target %q; proto3 must omit the empty string", tg)
		}
		_ = writeFrame(w, frameData, flagEndStream, sid, grpcMessage(nil))
	}
	if err := OverrideBalancerTarget(context.Background(), p.addr(), "BL-MAIN", ""); err != nil {
		t.Fatal(err)
	}
}

func TestGRPCUnaryFailsOnResetAndGoAway(t *testing.T) {
	p := newFakePeer(t)
	p.reply = func(w *bufio.Writer, sid uint32, _ string, _ []byte) {
		_ = writeFrame(w, frameRSTStream, 0, sid, []byte{0, 0, 0, 2})
	}
	got, err := GetBalancerInfo(context.Background(), p.addr(), []string{"BL-MAIN"})
	if err != nil || got["BL-MAIN"].Err == nil {
		t.Fatalf("RST_STREAM: %v / %+v", err, got)
	}

	q := newFakePeer(t)
	q.reply = func(w *bufio.Writer, _ uint32, _ string, _ []byte) {
		_ = writeFrame(w, frameGoAway, 0, 0, make([]byte, 8))
	}
	if _, err := GetBalancerInfo(context.Background(), q.addr(), []string{"BL-MAIN"}); err == nil {
		t.Fatal("GOAWAY did not fail the call")
	}
}

func TestGRPCUnaryHonoursTheDeadline(t *testing.T) {
	p := newFakePeer(t)
	p.reply = func(*bufio.Writer, uint32, string, []byte) {} // never answers
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := GetBalancerInfo(ctx, p.addr(), []string{"BL-MAIN"}); err == nil {
		t.Fatal("a silent server did not time out")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("the deadline was not applied to the connection")
	}
}

func TestHPACKIntegerEncodingMatchesRFC7541(t *testing.T) {
	// RFC 7541 C.1.1 and C.1.2.
	if got := hpackInt(nil, 0, 5, 10); !bytes.Equal(got, []byte{0x0a}) {
		t.Errorf("10/5 = %x", got)
	}
	if got := hpackInt(nil, 0, 5, 1337); !bytes.Equal(got, []byte{0x1f, 0x9a, 0x0a}) {
		t.Errorf("1337/5 = %x", got)
	}
	long := strings.Repeat("x", 300)
	h := decodeOurHPACK(t, hpackRequest("/"+long, "127.0.0.1:10085"))
	if h[":path"] != "/"+long || h["te"] != "trailers" || h["content-type"] != "application/grpc" || h[":authority"] != "127.0.0.1:10085" {
		t.Errorf("round trip = %v", h)
	}
}

func TestProtobufDecodingIsStrictAboutTruncation(t *testing.T) {
	full := balancerReply("a", "b", "c")
	if _, err := decodeBalancerInfo(full); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeBalancerInfo(full[:len(full)-1]); err == nil {
		t.Fatal("a truncated message decoded without error")
	}
}

func TestFetchMetricsDecodesTheObservatoryAndTraffic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/debug/vars" {
			http.NotFound(w, r)
			return
		}
		// Shape captured from xray 26.3.27 (burstObservatory): alive and delay
		// are omitted when false/zero.
		_, _ = io.WriteString(w, `{"cmdline":["xray"],"memstats":{"Alloc":1},
"observatory":{"a":{"alive":true,"delay":96,"outbound_tag":"a","health_ping":{"all":2,"deviation":1,"average":96000000,"max":2,"min":1}},
"dead":{"outbound_tag":"dead","health_ping":{"all":2,"fail":2}}},
"stats":{"inbound":{},"outbound":{"a":{"downlink":138,"uplink":2043}},"user":{}}}`)
	}))
	defer srv.Close()
	m, err := FetchMetrics(context.Background(), strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	if a := m.Observatory["a"]; !a.Alive || a.DelayMs != 96 || a.HealthPing == nil || a.HealthPing.All != 2 {
		t.Errorf("a = %+v", a)
	}
	if d := m.Observatory["dead"]; d.Alive || d.HealthPing.Fail != 2 {
		t.Errorf("dead = %+v", d)
	}
	if tr := m.Stats.Outbound["a"]; tr.Uplink != 2043 || tr.Downlink != 138 {
		t.Errorf("traffic = %+v", tr)
	}
}
