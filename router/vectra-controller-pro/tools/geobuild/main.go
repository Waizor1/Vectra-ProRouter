// geobuild builds the geo lists vctl routes by — geosite.dat and geoip.dat —
// from pinned upstream releases (sources.go), and the manifest routers fetch
// them by. Build tooling only: it runs on the build host
// (scripts/publish-vctl-geo.sh), never on a router.
//
//	geobuild -out DIR [-prev FILE] [-accept CODE,...]
//
// DIR gets geo/geosite-<sha12>.dat, geo/geoip-<sha12>.dat and geo.json,
// unsigned: the publish script signs it where the key lives. -prev is the
// published geo.json with its files beside it, as the channel serves them:
// every category it had must still be there with at least half its rules,
// unless -accept names it (a deliberate upstream change; the categories in
// outputs stay required whatever -accept says). When both files come out
// identical to the previous build's, nothing is written and geobuild says
// "unchanged".
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(os.Stderr, "geobuild: %v\n", err)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("geobuild", flag.ContinueOnError)
	out := fs.String("out", "", "directory for geo.json and geo/*.dat")
	prev := fs.String("prev", "", "the published geo.json, its files beside it")
	accept := fs.String("accept", "", "comma-separated categories that may shrink or go this once")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: geobuild -out DIR [-prev FILE] [-accept CODE,...]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" || fs.NArg() > 0 {
		fs.Usage()
		return errors.New("-out DIR is required")
	}
	b := &builder{
		client:  &http.Client{Timeout: 5 * time.Minute},
		sources: pinned,
		outputs: outputs,
		now:     time.Now,
		log:     stdout,
	}
	return b.build(ctx, *out, *prev, splitCodes(*accept))
}

func splitCodes(s string) map[string]bool {
	out := map[string]bool{}
	for _, c := range strings.Split(s, ",") {
		if c = strings.ToUpper(strings.TrimSpace(c)); c != "" {
			out[c] = true
		}
	}
	return out
}
