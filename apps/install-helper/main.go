package main

import (
	"fmt"
	"os"
)

func main() {
	server, err := newHelperServer()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start install helper: %v\n", err)
		os.Exit(1)
	}

	if err := server.serve(); err != nil {
		fmt.Fprintf(os.Stderr, "install helper stopped: %v\n", err)
		os.Exit(1)
	}
}
