package main

import "vectra-controller-pro/internal/setup"

// The panel's support shell, run_terminal_command, runs any command as root:
// a compromised panel, or an operator's stolen login, would be root on every
// router — every router's VPN credentials at once. So it runs only where the
// router's owner allows it: UCI vectra-controller-pro.main.remote_shell '1',
// switched in the router UI («Доступ поддержки к роутеру», set_remote_shell).
// A new router starts with it off; one upgraded from a vctl without the switch
// keeps the shell it had (the package's uci-defaults write the option once).

// remoteShellOff is the refusal the panel shows its operator.
const remoteShellOff = "run_terminal_command: support shell access is off on this router — its owner can turn it on in the router UI (Vectra → «Доступ поддержки к роутеру»)"

// remoteShellAllowed reports the owner's switch, read at every use: the next
// job sees a change, and nothing restarts. Tests stand it in.
var remoteShellAllowed = func() bool { return setup.RemoteShell(setup.RouterEnv()) }
