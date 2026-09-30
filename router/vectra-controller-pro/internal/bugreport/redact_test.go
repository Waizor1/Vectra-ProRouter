package bugreport

import (
	"reflect"
	"testing"
)

func TestRedact(t *testing.T) {
	for in, want := range map[string]string{
		"fetch https://sub.example.com/api/sub/AbCdEf?x=1 failed":                "fetch <url> failed",
		"node vless://2b1c8f4a-9d3e-4f5a-8b6c-7d8e9f0a1b2c@1.2.3.4:443?sni=x#DE": "node <link>",
		"user 2b1c8f4a-9d3e-4f5a-8b6c-7d8e9f0a1b2c logged":                       "user <uuid> logged",
		"client 192.168.1.23 (aa:bb:cc:dd:ee:ff)":                                "client <lan> (<mac>)",
		"dial tcp 203.0.113.9:443: i/o timeout":                                  "dial tcp <ip>:443: i/o timeout",
		"fake 198.18.0.7, lo 127.0.0.1, any 0.0.0.0":                             "fake 198.18.0.7, lo 127.0.0.1, any 0.0.0.0",
		"lookup instagram.com: no such host":                                     "lookup <host>: no such host",
		"panel router.vectra-pro.net answered":                                   "panel router.vectra-pro.net answered",
		"geosite.dat and xray.json stay":                                         "geosite.dat and xray.json stay",
		"password=hunter2 token: abc api_key=zzz":                                "password=<redacted> token: <redacted> api_key=<redacted>",
		`{"privateKey":"kL0mNoPq","port":443}`:                                   `{"privateKey":"<redacted>","port":443}`,
		"hwid 4f8e0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7":  "hwid <secret>",
		"v6 2001:db8:85a3::8a2e:370:7334 and ::1":                                "v6 <ip6> and ::1",
		"at 21:03:11 it stopped":                                                 "at 21:03:11 it stopped",
		"mail me at someone@example.org":                                         "mail me at <email>",
		"vectra-controller-pro/internal/apply.(*Applier).Apply(0x4000123456)":    "vectra-controller-pro/internal/apply.(*Applier).Apply(0x4000123456)",
		"lookup xn--80ak6aa92e.xn--p1ai failed":                                  "lookup <host> failed",
		"dial example.kr:443":                                                    "dial <host>:443",
		"Tue Sep 29 18:19:19 2026 daemon.err vctl[1]: kern.warning user.notice":  "Tue Sep 29 18:19:19 2026 daemon.err vctl[1]: kern.warning user.notice",
		"client iphone-ivan.lan asked":                                           "client <host> asked",
		"visited site.xxx":                                                       "visited <host>",
		"deadman.sh ran, libc.so.6 loaded":                                       "deadman.sh ran, libc.so.6 loaded",
		"runtime.gopark(0x0) in downloads.openwrt.org":                           "runtime.gopark(0x0) in downloads.openwrt.org",
		"\t/usr/lib/go/src/runtime/proc.go:402 +0x1c8":                           "\t/usr/lib/go/src/runtime/proc.go:402 +0x1c8",
	} {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestRedactValueReachesNestedDetails(t *testing.T) {
	in := map[string]any{
		"https://sub.example.com/k": 1,
		"url":                       "https://sub.example.com/api/sub/secret",
		"nodes":                     []any{"vless://2b1c8f4a-9d3e-4f5a-8b6c-7d8e9f0a1b2c@h:1", map[string]any{"pbk": "x key=abc"}},
		"count":                     3,
		"ok":                        true,
	}
	want := map[string]any{
		"<url>": 1,
		"url":   "<url>",
		"nodes": []any{"<link>", map[string]any{"pbk": "x key=<redacted>"}},
		"count": 3,
		"ok":    true,
	}
	if got := RedactValue(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
}
