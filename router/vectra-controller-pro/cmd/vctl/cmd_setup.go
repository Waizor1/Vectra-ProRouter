package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"vectra-controller-pro/internal/setup"
)

func init() {
	register(command{name: "setup", summary: "setup wizard helpers: wifi-apply | mark-done-if-working", run: cmdSetup})
}

func cmdSetup(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("setup: wifi-apply | mark-done-if-working")
	}
	// Spawned from rpcd, or from a uci-defaults script: the environment may
	// be bare, and uci, wifi and ubus are found through PATH.
	if os.Getenv("PATH") == "" {
		_ = os.Setenv("PATH", "/usr/sbin:/usr/bin:/sbin:/bin")
	}
	switch args[0] {
	case "wifi-apply":
		return setupWifiApply(setup.RouterEnv(), handedFile(3, "vectra-wifi.lock"), os.Stdin)
	case "mark-done-if-working":
		_, present, err := operatorConfig(rpcdConfig())
		marked, merr := setup.MarkDoneIfWorking(context.Background(), setup.RouterEnv(), present && err == nil)
		if marked {
			fmt.Println("setup_done=1: the router already works")
		}
		return merr
	}
	return fmt.Errorf("setup: unknown helper %q", args[0])
}

// handedFile is the descriptor fd this process was started with, or nil when
// it was given none there: a File on a descriptor that is not open would,
// once collected, close whatever file took that number since — the lock
// itself, when the helper is run by hand.
func handedFile(fd int, name string) *os.File {
	var st syscall.Stat_t
	if syscall.Fstat(fd, &st) != nil {
		return nil
	}
	return os.NewFile(uintptr(fd), name)
}

// setupWifiApply restarts the Wi-Fi after a change and checks it came up
// (setup.ApplyWifi). Spawned by rpcd, it was handed the Wi-Fi lock as fd 3
// and waits for the word on stdin, sent only once the change is committed.
// Run by hand, it takes the lock itself — refusing while a change holds it —
// and needs no word.
func setupWifiApply(env setup.Env, handed *os.File, word *os.File) error {
	lock := handed
	if setup.HoldsWifiLock(env, lock) {
		if line, _ := bufio.NewReader(word).ReadString('\n'); line != "go\n" {
			return nil // rpcd did not commit: there is nothing to restart
		}
	} else {
		if handed != nil {
			handed.Close() // not the lock: nothing this helper needs
		}
		var err error
		if lock, err = setup.LockWifi(env); err != nil {
			return err
		}
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	a := setup.ApplyWifi(ctx, env)
	fmt.Println(a.State)
	return nil
}
