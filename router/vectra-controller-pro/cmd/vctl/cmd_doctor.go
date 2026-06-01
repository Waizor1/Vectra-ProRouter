package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// doctor: a small set of environment checks for local dev. On a real router
// this would be richer (nftables availability, dnsmasq running, etc.) but
// for v0.1 alpha we keep it to portable checks.
func cmdDoctor(args []string) error {
	fs := newFlagSet("doctor")
	_ = fs.Parse(args)

	fmt.Println("Vectra Controller Pro · doctor")
	fmt.Println("==============================")
	fmt.Printf("go runtime:    %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	fmt.Printf("cwd:           %s\n", mustCwd())

	checkBinary("xray", "xray (proxy core)")
	checkBinary("nft", "nftables")
	checkBinary("dnsmasq", "dnsmasq")
	checkBinary("ip", "iproute2")
	checkBinary("sha256sum", "coreutils")
	checkBinary("curl", "fetcher fallback")
	return nil
}

func mustCwd() string {
	d, err := os.Getwd()
	if err != nil {
		return "?"
	}
	return d
}

func checkBinary(name, label string) {
	path, err := exec.LookPath(name)
	if err != nil {
		fmt.Printf("  [ - ] %-12s not found (%s)\n", name, label)
		return
	}
	fmt.Printf("  [ok ] %-12s %s (%s)\n", name, path, label)
}
