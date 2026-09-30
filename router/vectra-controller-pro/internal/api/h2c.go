package api

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// A minimal gRPC client over cleartext HTTP/2, for xray's API on loopback.
//
// Why not google.golang.org/grpc: vctl is stdlib-only (no go.sum, built by the
// OpenWrt SDK's Go), and the stdlib grew an h2c client only in Go 1.24. Why not
// the `xray api` CLI: every call execs the 32 MB xray binary, measured at a
// ~26 MB peak RSS per call, and `xray api bi` answers one balancer per process
// in a human-readable table. A UI refresh would spawn seven of them on a
// 234 MB router.
//
// What it implements is exactly what a unary call over a private loopback
// connection needs, and no more:
//
//   - requests: HEADERS encoded with HPACK literals that never touch the
//     dynamic table (so no table state to keep), then one DATA frame;
//   - responses: DATA frames are collected per stream; response HEADERS and
//     trailers are NOT decoded (that needs HPACK's dynamic table and Huffman
//     decoding). A unary call that fails is sent "trailers-only" — a single
//     HEADERS frame with END_STREAM and no DATA — so "the stream ended without
//     a message" IS the failure signal, and the only thing lost is xray's
//     error text;
//   - connection upkeep: SETTINGS and PING are acknowledged, WINDOW_UPDATE is
//     ignored (every message here is far below the 64 KiB initial window).
//
// Several calls share one connection as concurrent streams (1, 3, 5, ...).

const h2Preface = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"

const (
	frameData         = 0x0
	frameHeaders      = 0x1
	frameRSTStream    = 0x3
	frameSettings     = 0x4
	framePing         = 0x6
	frameGoAway       = 0x7
	frameContinuation = 0x9

	flagEndStream  = 0x1
	flagAck        = 0x1
	flagEndHeaders = 0x4
	flagPadded     = 0x8

	maxFramePayload = 1 << 14 // the protocol default the server must honour
	// maxGRPCMessage bounds one answer: a GetBalancerInfo reply is a few
	// hundred bytes, and a short-lived process on a 234 MB router must not
	// buffer whatever a misbehaving peer streams until the deadline.
	maxGRPCMessage = 1 << 20
)

// grpcCall is one unary request on the shared connection.
type grpcCall struct {
	path    string // "/package.Service/Method"
	request []byte // serialized protobuf
}

// grpcResult is a call's outcome: the response message, or an error.
type grpcResult struct {
	message []byte
	err     error
}

// errNoMessage is the trailers-only answer: the server ended the stream
// without a response message, which for a unary RPC means a non-OK status.
var errNoMessage = errors.New("xray answered with an error status and no message")

// grpcUnary runs calls concurrently on one h2c connection to addr.
func grpcUnary(ctx context.Context, addr string, calls []grpcCall) ([]grpcResult, error) {
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	}

	w := bufio.NewWriter(conn)
	if _, err := w.WriteString(h2Preface); err != nil {
		return nil, err
	}
	if err := writeFrame(w, frameSettings, 0, 0, nil); err != nil {
		return nil, err
	}
	for i, c := range calls {
		if len(c.request) > maxFramePayload-5 {
			return nil, fmt.Errorf("grpc request for %s is %d bytes; one DATA frame carries at most %d", c.path, len(c.request), maxFramePayload-5)
		}
		sid := uint32(2*i + 1)
		block := hpackRequest(c.path, addr)
		if len(block) > maxFramePayload {
			return nil, fmt.Errorf("grpc header block for %s too large", c.path)
		}
		if err := writeFrame(w, frameHeaders, flagEndHeaders, sid, block); err != nil {
			return nil, err
		}
		msg := make([]byte, 5+len(c.request))
		binary.BigEndian.PutUint32(msg[1:5], uint32(len(c.request)))
		copy(msg[5:], c.request)
		if err := writeFrame(w, frameData, flagEndStream, sid, msg); err != nil {
			return nil, err
		}
	}
	if err := w.Flush(); err != nil {
		return nil, err
	}

	results := make([]grpcResult, len(calls))
	bodies := make([][]byte, len(calls))
	done := make([]bool, len(calls))
	remaining := len(calls)
	r := bufio.NewReader(conn)
	for remaining > 0 {
		typ, flags, sid, payload, err := readFrame(r)
		if err != nil {
			return nil, fmt.Errorf("h2c read: %w", err)
		}
		switch typ {
		case frameSettings:
			if flags&flagAck == 0 {
				if err := writeFrame(w, frameSettings, flagAck, 0, nil); err != nil {
					return nil, err
				}
				if err := w.Flush(); err != nil {
					return nil, err
				}
			}
			continue
		case framePing:
			if flags&flagAck == 0 {
				if err := writeFrame(w, framePing, flagAck, 0, payload); err != nil {
					return nil, err
				}
				if err := w.Flush(); err != nil {
					return nil, err
				}
			}
			continue
		case frameGoAway:
			return nil, errors.New("xray closed the API connection (GOAWAY)")
		}
		idx := int(sid-1) / 2
		if sid == 0 || sid%2 == 0 || idx >= len(calls) || done[idx] {
			continue // connection-level or foreign frame
		}
		switch typ {
		case frameData:
			body := payload
			if flags&flagPadded != 0 {
				if len(body) == 0 || int(body[0]) >= len(body) {
					return nil, errors.New("h2c: malformed padded DATA frame")
				}
				body = body[1 : len(body)-int(body[0])]
			}
			if len(bodies[idx])+len(body) > maxGRPCMessage {
				return nil, fmt.Errorf("h2c: the answer to %s exceeds %d bytes", calls[idx].path, maxGRPCMessage)
			}
			bodies[idx] = append(bodies[idx], body...)
		case frameRSTStream:
			results[idx].err = errors.New("xray reset the stream")
			done[idx] = true
			remaining--
			continue
		}
		if (typ == frameData || typ == frameHeaders) && flags&flagEndStream != 0 {
			done[idx] = true
			remaining--
			results[idx] = decodeGRPCBody(bodies[idx])
		}
	}
	return results, nil
}

func decodeGRPCBody(b []byte) grpcResult {
	if len(b) == 0 {
		return grpcResult{err: errNoMessage}
	}
	if len(b) < 5 {
		return grpcResult{err: errors.New("truncated grpc message")}
	}
	if b[0] != 0 {
		return grpcResult{err: errors.New("xray sent a compressed grpc message; none was negotiated")}
	}
	n := binary.BigEndian.Uint32(b[1:5])
	if int(n) != len(b)-5 {
		return grpcResult{err: fmt.Errorf("grpc message length %d, got %d bytes", n, len(b)-5)}
	}
	return grpcResult{message: b[5:]}
}

func writeFrame(w *bufio.Writer, typ, flags byte, sid uint32, payload []byte) error {
	var h [9]byte
	h[0], h[1], h[2] = byte(len(payload)>>16), byte(len(payload)>>8), byte(len(payload))
	h[3], h[4] = typ, flags
	binary.BigEndian.PutUint32(h[5:], sid&0x7fffffff)
	if _, err := w.Write(h[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// maxReadFrame bounds what a frame may claim: the server must not exceed the
// 16 KiB default without our SETTINGS allowing it, and we never allow it.
const maxReadFrame = maxFramePayload

func readFrame(r *bufio.Reader) (typ, flags byte, sid uint32, payload []byte, err error) {
	var h [9]byte
	if _, err = io.ReadFull(r, h[:]); err != nil {
		return
	}
	n := int(h[0])<<16 | int(h[1])<<8 | int(h[2])
	if n > maxReadFrame {
		err = fmt.Errorf("frame of %d bytes exceeds %d", n, maxReadFrame)
		return
	}
	typ, flags = h[3], h[4]
	sid = binary.BigEndian.Uint32(h[5:]) & 0x7fffffff
	payload = make([]byte, n)
	_, err = io.ReadFull(r, payload)
	return
}

// hpackRequest encodes the request pseudo-headers and gRPC headers with
// literals and static-table indices only (RFC 7541 §6.1, §6.2.2), so the
// server's view of our dynamic table stays empty.
func hpackRequest(path, authority string) []byte {
	var b []byte
	b = append(b, 0x83)                         // :method: POST       (static 3)
	b = append(b, 0x86)                         // :scheme: http       (static 6)
	b = hpackLiteral(b, 4, path)                // :path               (static name 4)
	b = hpackLiteral(b, 1, authority)           // :authority      (static name 1)
	b = hpackLiteral(b, 31, "application/grpc") // content-type (static name 31)
	b = append(b, 0x00)                         // te: trailers        (new name, no indexing)
	b = hpackString(b, "te")
	b = hpackString(b, "trailers")
	return b
}

// hpackLiteral is "Literal Header Field without Indexing — Indexed Name".
func hpackLiteral(b []byte, nameIndex int, value string) []byte {
	b = hpackInt(b, 0x00, 4, nameIndex)
	return hpackString(b, value)
}

func hpackString(b []byte, s string) []byte {
	b = hpackInt(b, 0x00, 7, len(s)) // H=0: no Huffman
	return append(b, s...)
}

// hpackInt encodes v with an n-bit prefix (RFC 7541 §5.1); first carries the
// bits above the prefix.
func hpackInt(b []byte, first byte, n uint, v int) []byte {
	max := 1<<n - 1
	if v < max {
		return append(b, first|byte(v))
	}
	b = append(b, first|byte(max))
	v -= max
	for v >= 128 {
		b = append(b, byte(v%128)|0x80)
		v /= 128
	}
	return append(b, byte(v))
}
