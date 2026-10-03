package bugreport

import (
	"net"
	"regexp"
	"strings"

	"vectra-controller-pro/internal/redact"
)

// What leaves the router says what went wrong, never whose it was: no links
// or URLs (a subscription's is its owner's key), no UUIDs, keys, tokens or
// passwords, no addresses of the home's devices, no e-mails, no host a person
// visited. The router's own and Vectra's names stay, and so do what makes a
// stack a stack: package paths, file:line, pointers. The credentials are
// internal/redact's, which the router UI's answers use as well; the rest is
// what a report takes out beyond them.

var (
	reURL   = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>]+`)
	reEmail = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)
	reMAC   = regexp.MustCompile(`(?i)\b(?:[0-9a-f]{2}[:-]){5}[0-9a-f]{2}\b`)
	reIPv6  = regexp.MustCompile(`(?i)[0-9a-f]{0,4}(?::[0-9a-f]{0,4}){2,7}`)
	reIPv4  = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	reHost  = regexp.MustCompile(`(?i)\b(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+(?:xn--[a-z0-9-]{1,59}|[a-z]{2,24})\b`)
	// reSyslog is a log line's facility.priority (daemon.err), not a host.
	reSyslog = regexp.MustCompile(`(?i)^(?:kern|user|mail|daemon|auth|authpriv|syslog|lpr|news|uucp|cron|ftp|local[0-7])\.(?:emerg|alert|crit|err|error|warn|warning|notice|info|debug)$`)
)

// ownDomains are names that say nothing about a person: Vectra's, OpenWrt's.
var ownDomains = []string{"vectra-pro.net", "openwrt.org"}

// fileSuffixes end the names that are files, not hosts: geosite.dat,
// xray.json, deadman.sh, libc.so, proc.go. Any other dotted name — a
// visited site under any of the world's endings, its punycode (.рф is
// xn--p1ai on the wire), a LAN name (iphone.lan) — is a host.
var fileSuffixes = map[string]bool{}

func init() {
	for _, t := range strings.Fields(`go dat json sh so lua js ts conf cfg log txt pem key crt csr pub ipk tar gz tgz
		xz zip bz2 nft uci pid lock tmp html htm css png jpg svg ico md yaml yml toml ini service socket
		py c h cc cpp rs out bin img sig list sock`) {
		fileSuffixes[t] = true
	}
}

// Redact takes out of one line whatever says whose it was.
func Redact(s string) string {
	s = redact.Links(s)
	// The whole address, host included: it may be a site a person visited.
	s = reURL.ReplaceAllString(s, "<url>")
	s = reEmail.ReplaceAllString(s, "<email>")
	s = redact.KeyValues(s)
	s = redact.UUIDs(s)
	s = reMAC.ReplaceAllString(s, "<mac>")
	s = reIPv6.ReplaceAllStringFunc(s, ipv6)
	s = reIPv4.ReplaceAllStringFunc(s, ipv4)
	s = redactHosts(s)
	return redact.Tokens(s)
}

// RedactLines is Redact on every line.
func RedactLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = Redact(l)
	}
	return out
}

// RedactValue is Redact on every string in v, however deep in maps and lists.
func RedactValue(v any) any {
	switch x := v.(type) {
	case string:
		return Redact(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[Redact(k)] = RedactValue(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = RedactValue(e)
		}
		return out
	case []string:
		return RedactLines(x)
	}
	return v
}

func ipv4(s string) string {
	ip := net.ParseIP(s).To4()
	if ip == nil {
		return s
	}
	switch {
	case ip.IsLoopback(), ip.IsUnspecified(), ip.IsMulticast(), ip.Equal(net.IPv4bcast):
		return s
	case ip[0] == 198 && (ip[1] == 18 || ip[1] == 19): // xray's fake-IP pool
		return s
	case ip.IsPrivate(), ip.IsLinkLocalUnicast(), ip[0] == 100 && ip[1]&0xc0 == 64:
		return "<lan>"
	}
	return "<ip>"
}

func ipv6(s string) string {
	ip := net.ParseIP(strings.Trim(s, "[]"))
	if ip == nil || ip.To4() != nil || ip.IsLoopback() || ip.IsUnspecified() {
		return s
	}
	return "<ip6>"
}

// redactHosts replaces every host in s but Vectra's and OpenWrt's, a file's
// name, and a Go function's (runtime.gopark( in a stack).
func redactHosts(s string) string {
	idx := reHost.FindAllStringIndex(s, -1)
	if idx == nil {
		return s
	}
	var b strings.Builder
	last := 0
	for _, m := range idx {
		b.WriteString(s[last:m[0]])
		if keepHost(s[m[0]:m[1]], s, m[1]) {
			b.WriteString(s[m[0]:m[1]])
		} else {
			b.WriteString("<host>")
		}
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

func keepHost(name, s string, end int) bool {
	l := strings.ToLower(name)
	if fileSuffixes[l[strings.LastIndexByte(l, '.')+1:]] {
		return true
	}
	if end < len(s) && s[end] == '(' {
		return true // a Go function: runtime.gopark(…)
	}
	if reSyslog.MatchString(name) {
		return true
	}
	for _, d := range ownDomains {
		if l == d || strings.HasSuffix(l, "."+d) {
			return true
		}
	}
	return false
}
