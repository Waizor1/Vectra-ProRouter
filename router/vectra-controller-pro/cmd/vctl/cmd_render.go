package main

import (
	"encoding/json"
	"fmt"
	"os"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/coreengine/xray"
)

// cmdRender splices a PROVIDER document with the operator's tproxy inbound and
// prints the result. It is not a builder — every top-level key except
// "inbounds" is copied from the provider byte-for-byte.
func cmdRender(args []string) error {
	fs := newFlagSet("render")
	cfgPath := fs.String("config", "", "operator config JSON (required)")
	providerPath := fs.String("provider", "", "provider Xray document, one entry (required); '-' for stdin")
	out := fs.String("out", "-", "output file; '-' for stdout")
	scan := fs.Bool("scan", true, "refuse a provider config that sets allowInsecure=true")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" || *providerPath == "" {
		fs.Usage()
		return fmt.Errorf("-config and -provider are required")
	}
	c, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	providerRaw, err := readFileOrStdin(*providerPath)
	if err != nil {
		return err
	}
	if *scan {
		if err := xray.ScanAllowInsecure(providerRaw); err != nil {
			return err
		}
	}
	data, res, err := xray.SpliceInbounds(providerRaw, c.Inbounds.Tproxy)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "spliced %d bytes; kept %d top-level keys verbatim; dropped provider inbounds: %v\n",
		res.Bytes, len(res.TopLevelKeys)-1, res.DroppedInbounds)
	if !json.Valid(data) {
		return fmt.Errorf("render: produced invalid JSON")
	}
	if *out == "-" {
		_, err = os.Stdout.Write(append(data, '\n'))
		return err
	}
	return writePrivate(*out, data)
}
