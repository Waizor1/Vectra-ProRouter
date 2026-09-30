// feedtool builds and signs the Vectra "pro" opkg feed without the OpenWrt
// SDK: deterministic .ipk archives, the Packages index, and usign keys and
// signatures in the format opkg verifies. Build tooling only — it never runs
// on a router.
//
//	feedtool keygen -pub FILE -sec FILE [-comment TEXT]
//	feedtool fingerprint -pub FILE        the /etc/opkg/keys file name
//	feedtool sign -sec FILE -in FILE [-out FILE]
//	feedtool verify -pub FILE -in FILE [-sig FILE]
//	feedtool ipk -data DIR -control DIR -out FILE [-mtime UNIX]
//	feedtool control FILE.ipk             print an .ipk's control file
//	feedtool index [-arch ARCH] DIR       write DIR/Packages and DIR/Packages.gz
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

var commands = map[string]func([]string) error{
	"keygen":      cmdKeygen,
	"fingerprint": cmdFingerprint,
	"sign":        cmdSign,
	"verify":      cmdVerify,
	"ipk":         cmdIpk,
	"control":     cmdControl,
	"index":       cmdIndex,
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: feedtool keygen|fingerprint|sign|verify|ipk|control|index ...")
		os.Exit(2)
	}
	run, ok := commands[os.Args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "feedtool: unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
	if err := run(os.Args[2:]); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(os.Stderr, "feedtool %s: %v\n", os.Args[1], err)
		}
		os.Exit(1)
	}
}

func newFlags(name, usage string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "usage: feedtool %s %s\n", name, usage)
		fs.PrintDefaults()
	}
	return fs
}

func usageError(fs *flag.FlagSet) error {
	fs.Usage()
	return errors.New("missing arguments")
}
