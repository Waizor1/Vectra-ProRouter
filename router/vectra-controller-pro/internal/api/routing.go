package api

import (
	"context"
	"errors"
	"fmt"
)

// xray's RoutingService (app/router/command/command.proto):
//
//	message GetBalancerInfoRequest  { string tag = 1; }
//	message GetBalancerInfoResponse { BalancerMsg balancer = 1; }
//	message BalancerMsg { OverrideInfo override = 5; PrincipleTargetInfo principle_target = 6; }
//	message OverrideInfo { string target = 2; }
//	message PrincipleTargetInfo { repeated string tag = 1; }
//	message OverrideBalancerTargetRequest { string balancerTag = 1; string target = 2; }
const (
	pathGetBalancerInfo        = "/xray.app.router.command.RoutingService/GetBalancerInfo"
	pathOverrideBalancerTarget = "/xray.app.router.command.RoutingService/OverrideBalancerTarget"
)

// DefaultAPIServer is where the router splices xray's API
// (internal/coreengine/xray: SpliceOptions.APIListen).
const DefaultAPIServer = "127.0.0.1:10085"

// BalancerInfo is what xray reports about one balancer right now.
type BalancerInfo struct {
	// Override is the target pinned with OverrideBalancerTarget, "" if none.
	Override string
	// Principle is what the strategy currently prefers — for leastLoad the
	// `expected` best nodes, for leastPing the fastest. Empty means no member
	// qualifies and traffic goes to the balancer's fallbackTag. random and
	// roundRobin report every member here, whatever its health.
	Principle []string
	// Err is set when xray refused this balancer (e.g. an unknown tag); the
	// other balancers of the same call are unaffected.
	Err error
}

// ErrUnknownBalancer is returned by OverrideBalancerTarget when xray refuses
// the balancer tag.
var ErrUnknownBalancer = errors.New("xray has no balancer with that tag")

// GetBalancerInfo asks xray about each balancer, all on one connection.
func GetBalancerInfo(ctx context.Context, server string, tags []string) (map[string]BalancerInfo, error) {
	calls := make([]grpcCall, len(tags))
	for i, tag := range tags {
		calls[i] = grpcCall{path: pathGetBalancerInfo, request: pbString(nil, 1, tag)}
	}
	res, err := grpcUnary(ctx, server, calls)
	if err != nil {
		return nil, err
	}
	out := make(map[string]BalancerInfo, len(tags))
	for i, tag := range tags {
		if res[i].err != nil {
			out[tag] = BalancerInfo{Err: res[i].err}
			continue
		}
		info, err := decodeBalancerInfo(res[i].message)
		if err != nil {
			info.Err = err
		}
		out[tag] = info
	}
	return out, nil
}

// OverrideBalancerTarget pins balancer to target; an empty target removes the
// pin. xray does NOT check that target is one of the balancer's members — it
// will route everything to any outbound tag it is given, or to nothing if the
// tag does not exist. Callers must validate the pair first.
func OverrideBalancerTarget(ctx context.Context, server, balancer, target string) error {
	req := pbString(nil, 1, balancer)
	req = pbString(req, 2, target)
	res, err := grpcUnary(ctx, server, []grpcCall{{path: pathOverrideBalancerTarget, request: req}})
	if err != nil {
		return err
	}
	if errors.Is(res[0].err, errNoMessage) {
		return fmt.Errorf("%w: %q", ErrUnknownBalancer, balancer)
	}
	return res[0].err
}

func decodeBalancerInfo(msg []byte) (BalancerInfo, error) {
	var info BalancerInfo
	balancer, err := pbField(msg, 1)
	if err != nil || balancer == nil {
		return info, err
	}
	if ov, err := pbField(balancer, 5); err != nil {
		return info, err
	} else if ov != nil {
		t, err := pbField(ov, 2)
		if err != nil {
			return info, err
		}
		info.Override = string(t)
	}
	if pt, err := pbField(balancer, 6); err != nil {
		return info, err
	} else if pt != nil {
		tags, err := pbRepeated(pt, 1)
		if err != nil {
			return info, err
		}
		for _, t := range tags {
			// leastPing answers its pick as a one-element list, and with no live
			// member that pick is "": xray v1.260327 reports [""]. An empty tag
			// is no target at all.
			if len(t) > 0 {
				info.Principle = append(info.Principle, string(t))
			}
		}
	}
	return info, nil
}

// --- protobuf wire format, for the handful of string/message fields above ---

func pbString(b []byte, field int, s string) []byte {
	if s == "" {
		return b // proto3 omits default values
	}
	b = pbVarint(b, uint64(field)<<3|2)
	b = pbVarint(b, uint64(len(s)))
	return append(b, s...)
}

func pbVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func pbReadVarint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		v |= uint64(b[i]&0x7f) << (7 * uint(i))
		if b[i] < 0x80 {
			return v, i + 1, nil
		}
	}
	return 0, 0, errors.New("protobuf: bad varint")
}

// pbWalk calls fn for every length-delimited field; other wire types are
// skipped. It is strict about truncation, since a short read means a
// corrupted answer rather than an empty one.
func pbWalk(b []byte, fn func(field int, val []byte)) error {
	for len(b) > 0 {
		key, n, err := pbReadVarint(b)
		if err != nil {
			return err
		}
		b = b[n:]
		field, wire := int(key>>3), key&7
		switch wire {
		case 0:
			_, n, err := pbReadVarint(b)
			if err != nil {
				return err
			}
			b = b[n:]
		case 1:
			if len(b) < 8 {
				return errors.New("protobuf: truncated fixed64")
			}
			b = b[8:]
		case 5:
			if len(b) < 4 {
				return errors.New("protobuf: truncated fixed32")
			}
			b = b[4:]
		case 2:
			l, n, err := pbReadVarint(b)
			if err != nil {
				return err
			}
			b = b[n:]
			if uint64(len(b)) < l {
				return errors.New("protobuf: truncated field")
			}
			fn(field, b[:l])
			b = b[l:]
		default:
			return fmt.Errorf("protobuf: unsupported wire type %d", wire)
		}
	}
	return nil
}

// pbField returns the LAST occurrence of a length-delimited field (proto3
// merge semantics for singular fields), or nil when absent.
func pbField(b []byte, field int) ([]byte, error) {
	var out []byte
	found := false
	err := pbWalk(b, func(f int, v []byte) {
		if f == field {
			out, found = v, true
		}
	})
	if !found {
		return nil, err
	}
	if out == nil {
		out = []byte{}
	}
	return out, err
}

func pbRepeated(b []byte, field int) ([][]byte, error) {
	var out [][]byte
	err := pbWalk(b, func(f int, v []byte) {
		if f == field {
			out = append(out, v)
		}
	})
	return out, err
}
