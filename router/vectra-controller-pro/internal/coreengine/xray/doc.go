// Package xray adapts a PROVIDER-AUTHORED Xray document for use on the router.
//
// It is deliberately not a config builder. The provider subscription returns
// complete, working Xray configurations (log, dns, outbounds, routing, policy,
// stats, burstObservatory, ...) and those are adopted WHOLESALE. Re-authoring
// them is not just unnecessary, it is actively harmful: parsing an entry and
// re-serializing it corrupts the document. A previous Lua attempt turned
// `"tcpSettings":{}` into `"tcpSettings":[]` and Xray refused to start with
// "cannot unmarshal array into Go struct field". The provider ships 388 empty
// `tcpSettings` objects and 26 empty `stats` objects per payload and ZERO empty
// arrays, so any round-trip through a typed struct is a live-fire regression.
//
// This package therefore offers three narrow, byte-conservative operations:
//
//	SpliceInbounds — re-emit every top-level key verbatim and in order, except
//	                 "inbounds", which is replaced by the controller's single
//	                 tproxy inbound. This is the ONLY mutation performed.
//	ScanAllowInsecure — a read-only scan that refuses a document which disables
//	                 TLS certificate verification anywhere.
//	Validator      — gates the write on a real `xray -test` of the spliced doc.
//
// Splice layers the router's own additions on the inbound swap, each touching
// only what it names: the egress mark (outbound_mark.go); xray's API, metrics
// and the probe interval (runtime_options.go); the owner's own sites, as the
// first routing rules (user_rules.go). What the provider may not decide on the
// router — log files, open ports, reverse tunnels, identifiers the log would
// take as lines — is replaced, dropped or refused (provider_guard.go).
package xray
