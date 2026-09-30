package api

import (
	"strings"
	"testing"
)

// balancerMsg wraps raw PrincipleTargetInfo bytes the way xray sends them:
// GetBalancerInfoResponse{balancer: BalancerMsg{principle_target: pt}}.
func balancerMsg(pt []byte) []byte {
	bal := append(pbVarint(nil, 6<<3|2), pbVarint(nil, uint64(len(pt)))...)
	bal = append(bal, pt...)
	msg := append(pbVarint(nil, 1<<3|2), pbVarint(nil, uint64(len(bal)))...)
	return append(msg, bal...)
}

// leastPing answers its pick as a one-element list; with no live member the
// pick is "", and xray v1.260327 sends [""] — proto3 writes a repeated
// element even when it is empty (0a 00). That is no target, not a target
// named "".
func TestAnEmptyPrincipleTagIsNoTarget(t *testing.T) {
	empty := []byte{1<<3 | 2, 0}
	for _, tc := range []struct {
		name string
		pt   []byte
		want string
	}{
		{"only an empty tag", empty, ""},
		{"empty tags around a real one", append(append(append([]byte{}, empty...), pbString(nil, 1, "node-a")...), empty...), "node-a"},
		{"two real tags", append(pbString(nil, 1, "node-a"), pbString(nil, 1, "node-b")...), "node-a,node-b"},
	} {
		info, err := decodeBalancerInfo(balancerMsg(tc.pt))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := strings.Join(info.Principle, ","); got != tc.want || len(info.Principle) != len(strings.FieldsFunc(tc.want, func(r rune) bool { return r == ',' })) {
			t.Errorf("%s: principle = %q (%d tags), want %q", tc.name, info.Principle, len(info.Principle), tc.want)
		}
	}
}
