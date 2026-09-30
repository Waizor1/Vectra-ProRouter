package controlplane

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A name is dialled at the address the direct resolver gave, not where the
// system's resolver would have sent it.
func TestResolvingDialUsesTheDirectAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))

	dns := fakeDNS(t, "127.0.0.1")
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", dns)
	}}
	dial := resolvingDial(&net.Dialer{Timeout: 2 * time.Second}, r)
	c, err := dial(context.Background(), "tcp", "panel.invalid:"+port)
	if err != nil {
		t.Fatalf("dial through the direct answer: %v", err)
	}
	c.Close()
}

// When the direct resolvers cannot answer, the system's resolver still can:
// the name goes to the dialer as it came.
func TestResolvingDialFallsBackToTheSystem(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	r := &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("no direct resolver")
	}}
	dial := resolvingDial(&net.Dialer{Timeout: 2 * time.Second}, r)
	c, err := dial(context.Background(), "tcp", "localhost:"+port)
	if err != nil {
		t.Fatalf("fallback to the system resolver: %v", err)
	}
	c.Close()
}

// fakeDNS answers every A query over TCP with ip.
func fakeDNS(t *testing.T, ip string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	addr := net.ParseIP(ip).To4()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				for {
					var l [2]byte
					if _, err := ioReadFull(c, l[:]); err != nil {
						return
					}
					q := make([]byte, int(l[0])<<8|int(l[1]))
					if _, err := ioReadFull(c, q); err != nil {
						return
					}
					// Header and question only (Go adds an EDNS0 record
					// after the question: not echoed back).
					end := 12
					for end < len(q) && q[end] != 0 {
						end += int(q[end]) + 1
					}
					end += 5 // the root label, QTYPE, QCLASS
					if end > len(q) {
						return
					}
					qtype := int(q[end-4])<<8 | int(q[end-3])
					resp := append([]byte(nil), q[:end]...)
					resp[2] |= 0x80 // QR
					resp[3] = 0x80  // RA, NOERROR
					resp[6], resp[7], resp[8], resp[9], resp[10], resp[11] = 0, 0, 0, 0, 0, 0
					if qtype == 1 {
						resp[7] = 1 // ANCOUNT
						resp = append(resp, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4)
						resp = append(resp, addr...)
					}
					out := append([]byte{byte(len(resp) >> 8), byte(len(resp))}, resp...)
					if _, err := c.Write(out); err != nil {
						return
					}
				}
			}(c)
		}
	}()
	return ln.Addr().String()
}

func ioReadFull(c net.Conn, b []byte) (int, error) {
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	n := 0
	for n < len(b) {
		m, err := c.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// The watchdog's resolver is the control plane's own: public resolvers over
// marked sockets, Go's resolver (not the system's, whose upstream is the tunnel).
func TestMarkedResolverIsTheDirectOne(t *testing.T) {
	r := MarkedResolver(0x5643)
	if r == nil || r.Dial == nil || !r.PreferGo {
		t.Fatalf("%+v", r)
	}
}
