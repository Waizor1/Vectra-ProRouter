#!/bin/sh
# Link the stand's router as the panel would once its owner is confirmed: the
# operator config where the daemon keeps the panel's (kill switch ARMED), the
# subscription fetched and the render installed by `vctl apply-local` — the
# applier a panel job runs — and the daemon restarted onto it: xray up, the
# data plane programmed behind commit-confirm, which the next check-in
# confirms. The stub panel cannot deliver an operator config itself.
#
# Used by MODE=setup, and by hand in MODE=setup-hold:
#   docker exec vctl-setup-hold sh /stand/setup/link.sh
set -eu

mkdir -p /tmp/sysinfo /etc/vectra-controller-pro
# The provider keys on the device's model; a container has none.
[ -s /tmp/sysinfo/model ] || echo "Vectra Setup Stand (synthetic)" > /tmp/sysinfo/model
cp /stand/setup/operator-config.json /etc/vectra-controller-pro/xray-desired.json
vctl apply-local -config /var/run/vectra-controller-pro/agent.json
/etc/init.d/vectra-controller-pro restart
