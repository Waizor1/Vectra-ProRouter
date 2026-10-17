// geobuild builds the geo lists vctl routes by — geosite.dat and geoip.dat —
// from pinned upstream releases (sources.go), and the manifest routers fetch
// them by. Build tooling only: it runs on the build host
// (scripts/publish-vctl-geo.sh), never on a router.
//
//	geobuild -out DIR [-prev FILE] [-accept FILE:CODE,...] [-force]
//
// DIR gets geo/geosite-<sha12>.dat, geo/geoip-<sha12>.dat and geo.json,
// unsigned: the publish script signs it where the key lives. -prev is the
// published geo.json with its files beside it, as the channel serves them:
// every category it had must still be there with at least half its rules,
// unless -accept names it with its file, e.g. geosite.dat:RUSSIA-INSIDE (a
// deliberate upstream change; the categories in outputs stay required
// whatever -accept says). When both files come out identical to the
// previous build's, nothing is written and geobuild says "unchanged" —
// unless -force asks for a new manifest anyway (the channel lost its own).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
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
	accept := fs.String("accept", "", "comma-separated FILE:CODE categories that may shrink or go this once")
	force := fs.Bool("force", false, "write a manifest even when the files are the previous build's")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: geobuild -out DIR [-prev FILE] [-accept FILE:CODE,...] [-force]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" || fs.NArg() > 0 {
		fs.Usage()
		return errors.New("-out DIR is required")
	}
	accepted, err := parseAccept(*accept, outputs)
	if err != nil {
		return err
	}
	b := &builder{
		client:  &http.Client{Timeout: 5 * time.Minute},
		sources: pinned,
		outputs: outputs,
		now:     time.Now,
		log:     stdout,
	}
	return b.build(ctx, *out, *prev, accepted, *force)
}

// parseAccept reads -accept: "geoip.dat:TELEGRAM,geosite.dat:news" →
// {"geoip.dat:TELEGRAM", "geosite.dat:NEWS"}. A bare name is refused: the
// same code may be in both files (TELEGRAM, PRIVATE), and a waiver for one
// must not let the other shrink unseen.
func parseAccept(s string, outs []output) (map[string]bool, error) {
	out := map[string]bool{}
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item == "" {
			continue
		}
		file, code, ok := strings.Cut(item, ":")
		file, code = strings.ToLower(strings.TrimSpace(file)), strings.ToUpper(strings.TrimSpace(code))
		known := slices.ContainsFunc(outs, func(o output) bool { return o.Name == file })
		switch {
		case !ok:
			return nil, fmt.Errorf("-accept %q: name its file too, e.g. geosite.dat:%s", item, strings.ToUpper(item))
		case !known || code == "":
			return nil, fmt.Errorf("-accept %q: want FILE:CODE with FILE one of geosite.dat, geoip.dat", item)
		}
		out[file+":"+code] = true
	}
	return out, nil
}
