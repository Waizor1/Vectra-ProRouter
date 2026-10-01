package setup

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// step is one `uci` invocation. ignoreErr is for a delete of what may be
// absent.
type step struct {
	args      []string
	ignoreErr bool
}

func set(key, value string) step { return step{args: []string{"set", key + "=" + value}} }
func del(key string) step        { return step{args: []string{"-q", "delete", key}, ignoreErr: true} }

// describe names a step without its value: an error must never carry a Wi-Fi
// key.
func (s step) describe() string {
	out := make([]string, len(s.args))
	for i, a := range s.args {
		k, _, _ := strings.Cut(a, "=")
		out[i] = k
	}
	return strings.Join(out, " ")
}

func (env Env) apply(ctx context.Context, steps []step) error {
	for _, s := range steps {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := env.Run(c, nil, "uci", s.args...)
		cancel()
		if err != nil && !s.ignoreErr {
			return fmt.Errorf("uci %s: %w", s.describe(), err)
		}
	}
	return nil
}

// optionName is what uci accepts as an option name; anything else read from a
// file is not written back.
func optionName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// Finish marks the setup done.
func Finish(ctx context.Context, env Env) error {
	return env.apply(ctx, []step{
		{args: []string{"set", "vectra-controller-pro.main=controller"}},
		set("vectra-controller-pro.main.setup_done", "1"),
		{args: []string{"commit", "vectra-controller-pro"}},
	})
}

// SetRemoteShell switches the support shell (RemoteShell) on or off.
func SetRemoteShell(ctx context.Context, env Env, on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	return env.apply(ctx, []step{
		{args: []string{"set", "vectra-controller-pro.main=controller"}},
		set("vectra-controller-pro.main.remote_shell", v),
		{args: []string{"commit", "vectra-controller-pro"}},
	})
}

// Working reports whether the router was set up before the wizard existed —
// the fleet being upgraded: it is linked (it has an operator config), or it
// has a WAN address, Wi-Fi that is on and secured on every enabled radio, and
// a root password.
func Working(ctx context.Context, env Env, linked bool) bool {
	if linked {
		return true
	}
	return ReadWan(ctx, env).IPv4 != "" && ReadWifi(ctx, env).secured() && PasswordSet(env)
}

// MarkDoneIfWorking marks the setup done on a router that already works, so
// an upgraded fleet router never shows the wizard. It reports whether it did.
func MarkDoneIfWorking(ctx context.Context, env Env, linked bool) (bool, error) {
	if Done(env) || !Working(ctx, env, linked) {
		return false, nil
	}
	return true, Finish(ctx, env)
}
