package xray

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode"
)

// checkKeyFolding refuses a document in which any object has two keys that
// encoding/json would treat as the same field.
//
// xray decodes its config with Go's encoding/json, which matches keys
// case-insensitively (with Unicode simple folding) and lets the LAST
// occurrence win. The splice rewrites keys by their exact spelling. So a
// provider document carrying "api" AND "Api" had its "api" replaced by the
// router's loopback API while "Api" — listen 0.0.0.0 — passed through
// verbatim and won: the unauthenticated gRPC API, able to add outbounds and
// re-point every balancer, on every interface. The same trick un-marked the
// egress ("StreamSettings" beside "streamSettings") and restored the open
// socks/http inbounds ("Inbounds"). A real provider never repeats a key, in
// any spelling, so the whole document is refused rather than guessed at.
func checkKeyFolding(doc []byte) error {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	type frame struct {
		object bool
		seen   map[string]string // fold key -> first spelling
		key    bool              // next string token in this object is a key
	}
	var stack []*frame
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("xray splice: read provider document: %w", err)
		}
		var top *frame
		if n := len(stack); n > 0 {
			top = stack[n-1]
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{':
				if top != nil && top.object {
					top.key = true // this object is a value; the next token is a key
				}
				stack = append(stack, &frame{object: true, seen: map[string]string{}, key: true})
				continue
			case '[':
				if top != nil && top.object {
					top.key = true
				}
				stack = append(stack, &frame{})
				continue
			case '}', ']':
				stack = stack[:len(stack)-1]
				continue
			}
		case string:
			if top != nil && top.object && top.key {
				f := foldKey(t)
				if first, dup := top.seen[f]; dup {
					return fmt.Errorf("xray splice: the provider document repeats the key %q as %q in one object; "+
						"xray would honour the later one, whatever the router spliced — refusing the document", first, t)
				}
				top.seen[f] = t
				top.key = false
				continue
			}
		}
		// A scalar value.
		if top != nil && top.object {
			top.key = true
		}
	}
}

// foldKey maps a key to one canonical spelling per Unicode simple-fold orbit
// (the smallest rune of each orbit), a superset of what encoding/json treats
// as equal: "Api", "API" and "api" collide, and so do "K" and the Kelvin sign.
func foldKey(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		min := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if f < min {
				min = f
			}
		}
		out = append(out, min)
	}
	return string(out)
}
