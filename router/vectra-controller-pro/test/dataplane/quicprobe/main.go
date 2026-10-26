// quicprobe sends one QUIC packet with a reserved version to addr: any QUIC
// server must answer it with Version Negotiation, whatever it serves. It
// prints what came back — REPLY (a QUIC answer), REFUSED (ICMP port
// unreachable) or SILENT — and how long it took, and exits 0 either way.
//
//	quicprobe -addr 198.18.3.7:443 -wait 3s
package main

import (
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

func main() {
	addr := flag.String("addr", "", "host:port to probe")
	wait := flag.Duration("wait", 3*time.Second, "how long to wait for an answer")
	flag.Parse()
	if *addr == "" {
		fmt.Fprintln(os.Stderr, "quicprobe: -addr is required")
		os.Exit(2)
	}
	fmt.Println(probe(*addr, *wait))
}

func probe(addr string, wait time.Duration) string {
	// A long header (0xC0), version 0x0a0a0a0a (reserved: never spoken, so
	// it forces Version Negotiation), an 8-byte destination connection ID,
	// no source ID, padded to the 1200 bytes a server answers at all.
	pkt := make([]byte, 1200)
	pkt[0] = 0xC3
	copy(pkt[1:5], []byte{0x0a, 0x0a, 0x0a, 0x0a})
	pkt[5] = 8
	_, _ = rand.Read(pkt[6:14])
	pkt[14] = 0

	conn, err := net.Dial("udp", addr)
	if err != nil {
		return "ERROR " + err.Error()
	}
	defer conn.Close()
	t0 := time.Now()
	if _, err := conn.Write(pkt); err != nil {
		return "ERROR " + err.Error()
	}
	_ = conn.SetReadDeadline(t0.Add(wait))
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	d := time.Since(t0).Round(time.Millisecond)
	switch {
	case err == nil && n >= 5 && buf[0]&0x80 != 0 && buf[1]|buf[2]|buf[3]|buf[4] == 0:
		return fmt.Sprintf("REPLY %dB version-negotiation in %s", n, d)
	case err == nil:
		return fmt.Sprintf("REPLY %dB in %s", n, d)
	case errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Sprintf("REFUSED in %s", d)
	default:
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return fmt.Sprintf("SILENT %s", wait)
		}
		return "ERROR " + err.Error()
	}
}
