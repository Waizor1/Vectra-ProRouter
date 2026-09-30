#!/bin/sh
# MODE=reports — the reporter end to end (ADR-0007): a vctl crash, vctl's
# data plane without xray and a reboot nobody saw coming reach the stand's
# Connect (reportsink, on the origin's address with the stand's certificate)
# as reports it verifies — the body's hash, the token for this path, this
# HWID, this device; nothing goes out with the reporter switched off.
#
# The origin's servers do not run here (their openssl holds :443): only the
# topology, vctl's agent (its identity; its crash output), and the sink.

SINK_LOG=/tmp/reportsink.log
REPORTS_DIR=/tmp/reports
REPORTS_AGENT=$REPORTS_DIR/agent.json

reports_setup() {
	say "the stand's Connect on $ORIGIN_IP:443; the router's model; no cron (no dead-man, no scheduled runs)"
	/etc/init.d/cron stop >/dev/null 2>&1
	mkdir -p /tmp/sysinfo "$REPORTS_DIR" /etc/vectra-controller-pro
	echo "Xiaomi Mi Router AX3000T" > /tmp/sysinfo/model
	cat /stand/tls/cert.pem >> /etc/ssl/certs/ca-certificates.crt
	cat > "$REPORTS_AGENT" <<JSON
{
  "controlUrl": "http://127.0.0.1:1",
  "statePath": "/etc/vectra-controller-pro/state.json",
  "statusPath": "$REPORTS_DIR/status.json",
  "xrayConfigPath": "$REPORTS_DIR/operator.json",
  "providerConfigPath": "$REPORTS_DIR/provider-config.json",
  "xrayRenderPath": "/var/run/vectra-controller-pro/xray.json",
  "xrayBinary": "/usr/sbin/vctl-xray-wrapper",
  "legacyStatePath": "$REPORTS_DIR/no-legacy.json",
  "pollIntervalSeconds": 2,
  "requestTimeoutSeconds": 5
}
JSON
	/usr/sbin/vctl agent -config "$REPORTS_AGENT" > "$REPORTS_DIR/vctl.log" 2>&1 &
	REPORTS_VCTL=$!
	_i=0
	while [ $_i -lt 30 ] && ! grep -q device_public_key /etc/vectra-controller-pro/state.json 2>/dev/null; do
		_i=$((_i + 1))
		sleep 1
	done
	grep -q device_public_key /etc/vectra-controller-pro/state.json 2>/dev/null ||
		die "vctl made no identity in 30 s: $(tail -3 "$REPORTS_DIR/vctl.log")"
	VKEY=$(/stand/bin/standkey new)
	/stand/bin/standkey claim -state /etc/vectra-controller-pro/state.json -private "$VKEY" ||
		die "could not tell the router's state of Vectra's test key"
	in_inet /stand/bin/reportsink -listen "$ORIGIN_IP:443" -cert /stand/tls/cert.pem -key /stand/tls/key.pem \
		-vectra-key "$VKEY" -state /etc/vectra-controller-pro/state.json -log "$SINK_LOG" > "$REPORTS_DIR/sink.out" 2>&1 &
	uci -q set vectra-reporter.main=reporter
	uci -q set vectra-reporter.main.url="https://$ORIGIN_IP/errors/router"
	uci -q set vectra-reporter.main.enabled=1
	uci -q commit vectra-reporter
	# Its own first run makes its state: from then on a boot without a clean
	# shutdown is an incident.
	/usr/sbin/vectra-reporter boot >/dev/null 2>&1
	/usr/sbin/vectra-reporter run >/dev/null 2>&1
	info "reporter: $(/usr/sbin/vectra-reporter status 2>&1 | tail -1)"
}

reports_run_until() { # <result> <pattern>: a run every 5 s until the sink logs the pattern (2 min)
	_i=0
	while [ $_i -lt 24 ]; do
		/usr/sbin/vectra-reporter run >/dev/null 2>&1
		if grep -q "$2" "$SINK_LOG" 2>/dev/null; then
			record "$1" PASS "$(grep "$2" "$SINK_LOG" | tail -1)"
			return
		fi
		_i=$((_i + 1))
		sleep 5
	done
	record "$1" FAIL "the sink has: $(tail -3 "$SINK_LOG" 2>/dev/null | tr '\n' ' ') status: $(/usr/sbin/vectra-reporter status 2>&1 | tail -3 | tr '\n' ' ')"
}

run_reports_mode() {
	setup_kernel
	setup_topology
	reports_setup

	say "vctl crashes (SIGQUIT: Go writes its crash output to the reporter's file)"
	kill -QUIT "$REPORTS_VCTL"
	sleep 2
	reports_run_until reports_vctl_panic "OK VCTL_PANIC"

	say "vctl's table loaded, no xray: said at the second run in a row"
	printf 'table inet vctl {\n}\n' > "$REPORTS_DIR/vctl-empty.nft" && nft -f "$REPORTS_DIR/vctl-empty.nft"
	reports_run_until reports_dataplane "OK DATAPLANE_WITHOUT_XRAY"
	nft delete table inet vctl 2>/dev/null

	say "a boot after no clean shutdown"
	rm -f /etc/vectra-reporter/clean-shutdown
	/usr/sbin/vectra-reporter boot >/dev/null 2>&1
	reports_run_until reports_reboot "OK UNEXPECTED_REBOOT"

	say "switched off: nothing goes out"
	uci -q set vectra-reporter.main.enabled=0 && uci -q commit vectra-reporter
	_before=$(wc -l < "$SINK_LOG")
	/usr/sbin/vectra-reporter test >/dev/null 2>&1
	/usr/sbin/vectra-reporter run >/dev/null 2>&1
	if [ "$(wc -l < "$SINK_LOG")" = "$_before" ]; then
		record reports_off PASS "nothing sent while switched off"
	else
		record reports_off FAIL "sent while switched off: $(tail -1 "$SINK_LOG")"
	fi

	if grep -q '^BAD' "$SINK_LOG"; then
		record reports_all_verified FAIL "$(grep '^BAD' "$SINK_LOG" | head -3 | tr '\n' ' ')"
	else
		record reports_all_verified PASS "every report the sink got was verified: $(grep -c '^OK' "$SINK_LOG")"
	fi
	say "the sink's log"
	cat "$SINK_LOG" 2>/dev/null
}
