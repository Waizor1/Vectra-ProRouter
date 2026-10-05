package conntrack

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"sync/atomic"
)

// ctnetlink (linux/netfilter/nfnetlink_conntrack.h), read without a library:
// one dump of the whole table; of each entry the original tuple, the status
// and the TCP state. Binary: nothing for the kernel to format or for us to
// parse as text — a router torrenting has thousands of entries.
const (
	nlmsgError = 2
	nlmsgDone  = 3

	nlmFRequest = 0x1
	nlmFMulti   = 0x2
	nlmFDump    = 0x300

	ctnlMsgNew    = 1 << 8 // NFNL_SUBSYS_CTNETLINK << 8 | IPCTNL_MSG_CT_NEW: an entry of a dump
	ctnlMsgGet    = ctnlMsgNew | 1
	ctnlMsgDelete = ctnlMsgNew | 2

	nlmFAck = 0x4

	nlaFNested  = 1 << 15
	nlaTypeMask = 1<<14 - 1 // without NLA_F_NESTED and NLA_F_NET_BYTEORDER

	ctaTupleOrig  = 1
	ctaTupleReply = 2
	ctaStatus     = 3
	ctaProtoinfo  = 4

	ctaTupleIP    = 1
	ctaTupleProto = 2

	ctaIPv4Src = 1
	ctaIPv4Dst = 2
	ctaIPv6Src = 3
	ctaIPv6Dst = 4

	ctaProtoNum     = 1
	ctaProtoSrcPort = 2
	ctaProtoDstPort = 3

	ctaProtoinfoTCP      = 1
	ctaProtoinfoTCPState = 1

	ipsSeenReply = 1 << 1
	ipsConfirmed = 1 << 3

	tcpSynSent     = 1
	tcpEstablished = 3
)

// The kernel's names for enum tcp_conntrack, as the proc file prints them.
var tcpStates = [...]string{"NONE", "SYN_SENT", "SYN_RECV", "ESTABLISHED", "FIN_WAIT",
	"CLOSE_WAIT", "LAST_ACK", "TIME_WAIT", "CLOSE", "SYN_SENT2"}

// errNoNetlink: this kernel has no ctnetlink (no module, or it refused the
// dump) — the proc file is read from then on.
var errNoNetlink = errors.New("conntrack: no ctnetlink on this kernel")

var (
	// dumpNetlink reads the table through ctnetlink (a variable for tests).
	dumpNetlink = readNetlink
	// procOnly is set once ctnetlink turned out missing: asking again would
	// make the kernel try to load the module on every read.
	procOnly atomic.Bool
)

// Read returns the kernel's table: through ctnetlink, or from the proc file
// on a kernel without it — or this once, when a dump failed.
func Read() ([]Entry, error) {
	if !procOnly.Load() {
		es, err := dumpNetlink()
		if err == nil {
			return es, nil
		}
		if errors.Is(err, errNoNetlink) {
			procOnly.Store(true)
		}
	}
	return readProc()
}

// dumpRequest asks for every entry of both families.
func dumpRequest(seq uint32) []byte {
	b := make([]byte, 20) // nlmsghdr, then nfgenmsg: AF_UNSPEC, NFNETLINK_V0, res_id 0
	binary.NativeEndian.PutUint32(b[0:], 20)
	binary.NativeEndian.PutUint16(b[4:], ctnlMsgGet)
	binary.NativeEndian.PutUint16(b[6:], nlmFRequest|nlmFDump)
	binary.NativeEndian.PutUint32(b[8:], seq)
	return b
}

// parseDump reads one receive of the dump for request seq: each entry goes to
// add; done at NLMSG_DONE. A refused request is errNoNetlink.
func parseDump(b []byte, seq uint32, add func(Entry)) (done bool, err error) {
	for len(b) >= 16 {
		l := int(binary.NativeEndian.Uint32(b[0:]))
		typ := binary.NativeEndian.Uint16(b[4:])
		s := binary.NativeEndian.Uint32(b[8:])
		if l < 16 || l > len(b) {
			return false, fmt.Errorf("conntrack: a netlink message of %d bytes in %d", l, len(b))
		}
		body := b[16:l]
		b = b[min((l+3)&^3, len(b)):]
		if s != seq {
			continue
		}
		switch typ {
		case nlmsgDone:
			if len(body) >= 4 {
				if e := int32(binary.NativeEndian.Uint32(body)); e < 0 {
					return false, fmt.Errorf("conntrack: the dump ended with errno %d", -e)
				}
			}
			return true, nil
		case nlmsgError:
			if len(body) >= 4 {
				if e := int32(binary.NativeEndian.Uint32(body)); e != 0 {
					return false, fmt.Errorf("%w: the kernel refused the dump (errno %d)", errNoNetlink, -e)
				}
			}
		case ctnlMsgNew:
			if e, ok := parseEntry(body); ok {
				add(e)
			}
		}
	}
	return false, nil
}

// walkAttrs calls f with each attribute of b, its flags dropped from the type.
func walkAttrs(b []byte, f func(typ uint16, v []byte)) {
	for len(b) >= 4 {
		l := int(binary.NativeEndian.Uint16(b[0:]))
		if l < 4 || l > len(b) {
			return
		}
		f(binary.NativeEndian.Uint16(b[2:])&nlaTypeMask, b[4:l])
		b = b[min((l+3)&^3, len(b)):]
	}
}

// parseEntry reads an IPCTNL_MSG_CT_NEW body: nfgenmsg, then attributes.
func parseEntry(body []byte) (Entry, bool) {
	if len(body) < 4 {
		return Entry{}, false
	}
	var e Entry
	var proto byte
	orig, status := false, false
	// The reply tuple: where the answers come from.
	reply := func(v []byte) {
		walkAttrs(v, func(typ uint16, v []byte) {
			switch typ {
			case ctaTupleIP:
				walkAttrs(v, func(typ uint16, v []byte) {
					if typ == ctaIPv4Src || typ == ctaIPv6Src {
						e.ReplySrc, _ = netip.AddrFromSlice(v)
					}
				})
			case ctaTupleProto:
				walkAttrs(v, func(typ uint16, v []byte) {
					if typ == ctaProtoSrcPort && len(v) >= 2 {
						e.ReplySPort = binary.BigEndian.Uint16(v)
					}
				})
			}
		})
	}
	state := -1
	walkAttrs(body[4:], func(typ uint16, v []byte) {
		switch typ {
		case ctaTupleOrig:
			orig = true
			walkAttrs(v, func(typ uint16, v []byte) {
				switch typ {
				case ctaTupleIP:
					walkAttrs(v, func(typ uint16, v []byte) {
						switch typ {
						case ctaIPv4Src, ctaIPv6Src:
							e.Src, _ = netip.AddrFromSlice(v)
						case ctaIPv4Dst, ctaIPv6Dst:
							e.Dst, _ = netip.AddrFromSlice(v)
						}
					})
				case ctaTupleProto:
					walkAttrs(v, func(typ uint16, v []byte) {
						switch {
						case typ == ctaProtoNum && len(v) >= 1:
							proto = v[0]
						case typ == ctaProtoSrcPort && len(v) >= 2:
							e.SPort = binary.BigEndian.Uint16(v)
						case typ == ctaProtoDstPort && len(v) >= 2:
							e.DPort = binary.BigEndian.Uint16(v)
						}
					})
				}
			})
		case ctaTupleReply:
			reply(v)
		case ctaStatus:
			if len(v) >= 4 {
				status = true
				e.Replied = binary.BigEndian.Uint32(v)&ipsSeenReply != 0
			}
		case ctaProtoinfo:
			walkAttrs(v, func(typ uint16, v []byte) {
				if typ == ctaProtoinfoTCP {
					walkAttrs(v, func(typ uint16, v []byte) {
						if typ == ctaProtoinfoTCPState && len(v) >= 1 {
							state = int(v[0])
						}
					})
				}
			})
		}
	})
	switch proto {
	case 6:
		e.Proto = "tcp"
		if state >= 0 && state < len(tcpStates) {
			e.State = tcpStates[state]
		}
	case 17:
		e.Proto = "udp"
	default:
		return Entry{}, false
	}
	if !orig || !status || !e.Src.IsValid() || !e.Dst.IsValid() || e.DPort == 0 {
		return Entry{}, false
	}
	return e, true
}

// deleteRequest asks the kernel to forget the entry whose original tuple is
// e's (IPCTNL_MSG_CT_DELETE), acknowledged.
func deleteRequest(seq uint32, e Entry) []byte {
	attr := func(typ uint16, payload []byte) []byte {
		l := 4 + len(payload)
		b := make([]byte, (l+3)&^3)
		binary.NativeEndian.PutUint16(b[0:], uint16(l))
		binary.NativeEndian.PutUint16(b[2:], typ)
		copy(b[4:], payload)
		return b
	}
	cat := func(parts ...[]byte) []byte {
		var b []byte
		for _, p := range parts {
			b = append(b, p...)
		}
		return b
	}
	port := func(p uint16) []byte { return binary.BigEndian.AppendUint16(nil, p) }
	family, srcT, dstT := byte(2), uint16(ctaIPv4Src), uint16(ctaIPv4Dst) // AF_INET
	if e.Src.Is6() {
		family, srcT, dstT = 10, ctaIPv6Src, ctaIPv6Dst // AF_INET6
	}
	proto := byte(17)
	if e.Proto == "tcp" {
		proto = 6
	}
	tuple := attr(ctaTupleOrig|nlaFNested, cat(
		attr(ctaTupleIP|nlaFNested, cat(attr(srcT, e.Src.AsSlice()), attr(dstT, e.Dst.AsSlice()))),
		attr(ctaTupleProto|nlaFNested, cat(attr(ctaProtoNum, []byte{proto}),
			attr(ctaProtoSrcPort, port(e.SPort)), attr(ctaProtoDstPort, port(e.DPort)))),
	))
	b := make([]byte, 20, 20+len(tuple))
	binary.NativeEndian.PutUint32(b[0:], uint32(20+len(tuple)))
	binary.NativeEndian.PutUint16(b[4:], ctnlMsgDelete)
	binary.NativeEndian.PutUint16(b[6:], nlmFRequest|nlmFAck)
	binary.NativeEndian.PutUint32(b[8:], seq)
	b[16] = family // nfgenmsg: family, NFNETLINK_V0, res_id 0
	return append(b, tuple...)
}

// ackErrno reads the kernel's answer to request seq: 0, or the errno it
// refused with; ok is false when b holds no answer to seq.
func ackErrno(b []byte, seq uint32) (errno int, ok bool) {
	for len(b) >= 16 {
		l := int(binary.NativeEndian.Uint32(b[0:]))
		if l < 16 || l > len(b) {
			return 0, false
		}
		typ := binary.NativeEndian.Uint16(b[4:])
		s := binary.NativeEndian.Uint32(b[8:])
		body := b[16:l]
		b = b[min((l+3)&^3, len(b)):]
		if s == seq && typ == nlmsgError && len(body) >= 4 {
			return int(-int32(binary.NativeEndian.Uint32(body))), true
		}
	}
	return 0, false
}

// deleteNetlink stands in for the kernel in tests.
var deleteNetlink = deleteEntries

// Delete makes the kernel forget each entry — the next packet of such a
// connection starts a new one, which the ruleset as it is now decides. An
// entry gone already is no failure. How many were forgotten; an error when
// ctnetlink is not there to ask.
func Delete(es []Entry) (int, error) {
	if len(es) == 0 {
		return 0, nil
	}
	return deleteNetlink(es)
}
