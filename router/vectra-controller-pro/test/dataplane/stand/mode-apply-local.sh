#!/bin/sh
# `vctl apply-local` — the degradation path, end to end on real nftables.
#
# The deployed panel cannot hand this controller its config: its jobTypeSchema
# has no apply_xray_config at all (see cmd/vctl/prod_panel_compat_test.go). So a
# canary router enrolls, checks in, takes terminal and log jobs — and has no data
# plane. apply-local builds one from the documents already on the router: the
# operator config at xrayConfigPath and the byte-exact last-good provider
# document at providerConfigPath.
#
# The owner's rule is that nothing reaches a live router without a proven data
# plane, and `xray -test` is not proof. This mode is that proof for this command:
# the same topology and the same traffic assertions as MODE=full, with the
# rendered config produced by apply-local instead of by `vctl supervise`.
#
# WHAT THIS MODE ALREADY CAUGHT. The first version of apply-local also programmed
# the firewall, through the commit-confirm path. It worked: the ruleset went in,
# the policy route went in, and every traffic assertion passed. Then the mode
# waited out the 90-second deadman and the table and the route were both gone —
# because that deadman is disarmed by a successful CHECK-IN, and a command that
# programs the firewall and exits has no check-in to give it. An operator would
# have seen the proxy work, walked away, and come back to a direct router.
#
# So apply-local installs the CONFIG and stops, the daemon stays the single owner
# of the kernel state, and assert_al_leaves_the_firewall_alone keeps it that way.

AL_AGENT=/tmp/al-agent/agent.json
AL_RENDER=/var/run/vectra-controller-pro/xray.json

# The agent config a real canary router has: the operator config the panel would
# have delivered, and the last-good provider document, both already on disk.
# controlUrl points at a dead port on purpose — apply-local must not need the
# panel for anything, and this is how we find out if it does.
al_write_agent_cfg() {
	mkdir -p /tmp/al-agent "$WORK"
	cp "$CFG" /tmp/al-agent/operator.json
	cp "${PROVIDER:-$STAND/provider-marked.json}" /tmp/al-agent/provider-config.json
	cat > "$AL_AGENT" <<EOF
{
  "controlUrl": "http://127.0.0.1:1",
  "statePath": "/tmp/al-agent/state.json",
  "statusPath": "$WORK/status.json",
  "xrayConfigPath": "/tmp/al-agent/operator.json",
  "providerConfigPath": "/tmp/al-agent/provider-config.json",
  "xrayRenderPath": "$AL_RENDER",
  "xrayBinary": "/usr/sbin/vctl-xray-wrapper",
  "geoAssetDir": "/usr/share/v2ray",
  "legacyStatePath": "/tmp/al-agent/no-legacy.json",
  "pollIntervalSeconds": 2,
  "requestTimeoutSeconds": 5
}
EOF
	info "seeded: operator config + provider document on disk, panel unreachable"
}

# Start xray on exactly what apply-local installed. Not `vctl supervise`, which
# would re-splice and therefore test its own render rather than this one.
al_start_xray() {
	[ -f "$AL_RENDER" ] || die "no rendered config at $AL_RENDER to start xray on"
	/usr/sbin/vctl-xray-wrapper run -c "$AL_RENDER" > "$WORK/xray.log" 2>&1 &
	_i=0
	while [ "$_i" -lt 40 ]; do
		netstat -lnt 2>/dev/null | grep -q ":$TPROXY_PORT " && return 0
		_i=$((_i + 1)); sleep 1
	done
	echo "--- xray.log ---"; cat "$WORK/xray.log" 2>/dev/null
	die "xray never listened on :$TPROXY_PORT after apply-local"
}

# --------------------------------------------------------------- assertions ---

assert_al_installed() {
	if [ "$AL_RC" -ne 0 ]; then
		record al_installed FAIL "vctl apply-local exited $AL_RC: $(tail -n 3 /tmp/al-agent/run.log | tr '\n' ' ' | cut -c1-140)"
		return
	fi
	_bytes=$(wc -c < "$AL_RENDER" 2>/dev/null || echo 0)
	if [ -f "$AL_RENDER" ] && [ "$_bytes" -gt 0 ]; then
		record al_installed PASS "apply-local wrote $_bytes bytes to $AL_RENDER from the local operator config + cached provider document, with no panel"
	else
		record al_installed FAIL "no rendered config at $AL_RENDER"
	fi
}

# The provider document is the byte-exact last-good one. apply-local must not
# rewrite it: a decode/encode round trip is the corruption the verbatim rule
# exists to prevent, and it would be invisible until a node stopped dialling.
assert_al_provider_verbatim() {
	if cmp -s /tmp/al-agent/provider-config.json "${PROVIDER:-$STAND/provider-marked.json}"; then
		record al_provider_verbatim PASS "the cached provider document is byte-identical after the apply"
	else
		record al_provider_verbatim FAIL "apply-local rewrote the cached provider document"
	fi
}

# THE REGRESSION GATE FOR WHAT THIS MODE FOUND. apply-local must not touch the
# kernel. The commit-confirm deadman is disarmed by a successful check-in, and
# this command exits without one — so a ruleset it programmed would be reverted
# ~90s later, after appearing to work perfectly.
assert_al_leaves_the_firewall_alone() {
	_tbl=no; nft list table inet vctl >/dev/null 2>&1 && _tbl=yes
	_rule=no; ip rule list 2>/dev/null | grep -q "fwmark 0x1 lookup 100" && _rule=yes
	if [ "$_tbl" = no ] && [ "$_rule" = no ]; then
		record al_leaves_firewall_alone PASS \
			"apply-local touched neither the nft table nor the policy route — the daemon owns the kernel state and the commit-confirm deadman"
	else
		record al_leaves_firewall_alone FAIL \
			"apply-local programmed the firewall (table=$_tbl fwmark_rule=$_rule); nothing will confirm the deadman and it reverts ~90s later"
	fi
}

# --------------------------------------------------------------------- main ---

run_apply_local_mode() {
	info "operator config: $CFG"
	setup_kernel
	setup_topology
	setup_guard
	start_origins
	al_write_agent_cfg

	say "install the config with 'vctl apply-local' — no panel-delivered config anywhere"
	vctl apply-local -config "$AL_AGENT" > /tmp/al-agent/run.log 2>&1
	AL_RC=$?
	cat /tmp/al-agent/run.log

	assert_al_installed
	assert_al_provider_verbatim
	assert_al_leaves_the_firewall_alone

	say "bring the data plane up the way the daemon does: firewall, then xray on THIS render"
	apply_firewall
	al_start_xray
	nft list ruleset | sed -n '/table inet vctl/,/^}/p'
	echo "--- ip rule ---"; ip rule list

	say "the config apply-local built must carry traffic"
	assert_tcp
	assert_tls
	assert_dns
	assert_udp
	assert_tproxy_counter
	assert_tproxy_delivered
	assert_no_leak

	say "nft counters (table inet vctl)"
	nft list counters table inet vctl
}
