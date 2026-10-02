# Control-plane carve-out lifecycle and rollback

`controlplane-direct.sh remove` is deliberately temporary: it removes only
`inet vectra_controlplane`; installed startup and firewall hooks rebuild it.
It is not an uninstall or persistent rollback.

For a persistent rollback use the helper's `disable` command. This is a live
network change and requires the operator's approval before running on a router.
The command creates a root-only marker at
`/etc/vectra-controller/controlplane-direct.disabled`, removes its nft table,
and unregisters `firewall.vectra_controlplane` only when its type and path match
our generated script include. It deletes the generated include file only when
its ownership header matches. User firewall rules, custom include contents,
and package-owned startup/hotplug files are retained. Automatic `apply` does
nothing while the marker exists. A failed nft deletion is reported as failure;
the marker still prevents automatic restoration, and retry is safe.

A normal package reinstall/upgrade runs `93_vectra_controlplane_direct`, which
registers the include and calls `enable`, clearing the marker and restoring the
carve-out. `enable` alone restores the helper's table/file; use the uci-default
or normal reinstall when the UCI registration also needs restoring. Do not
upgrade while intending to keep the old carve-out disabled.

For a package downgrade, first stage and verify the trusted old IPK and an
independent recovery path. Run persistent disable before installing an older
package that does not implement this marker; check that its hooks cannot
restore the new rule. The old r42 package has a different lifecycle, so a
package downgrade by itself is not a complete firewall rollback. Do not flush
other nft tables or restore the entire firewall config. No reboot, full firewall
reload, PassWall restart, or firmware write is part of this helper's cleanup.

Local test (mocked nft/UCI, temporary filesystem only):

    bash router/vectra-controller-agent/openwrt/tests/controlplane_rollback_test.sh

It covers install, temporary remove/restore, persistent disable, repeat disable,
automatic apply while disabled, reinstall, nft failure/retry, and preservation
of unrelated user rules and a repurposed include. These tests do not prove live
VPN routing or a remote recovery path on a customer's device.
