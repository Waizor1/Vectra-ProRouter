// Package xray is the Xray-core implementation of the CoreEngine interface.
// It renders an operator config.Config into Xray-native JSON, suitable for
// `xray run -c <file>`.
//
// The renderer is a straight-through translator: each operator field maps
// to its Xray equivalent without invented defaults or silent rewrites.
// Defaults that Xray itself supplies (e.g., domainStrategy "AsIs") are
// allowed to be omitted on output.
//
// Deterministic output is guaranteed for golden-file testing: we rely on
// Go's stdlib json.Marshal sorting map keys + on typed structs throughout.
package xray
