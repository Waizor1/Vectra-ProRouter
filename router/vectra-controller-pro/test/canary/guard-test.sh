#!/bin/sh
# guard-test.sh — exercises canary-guard.sh against stubbed system commands.
#
# Runs inside a busybox container so the shell is the same ash the router uses.
# Each scenario builds a fake world (status.json, meminfo, /proc, nft/ip/curl
# stubs), runs the guard with a 1s sweep, and asserts on what it did.
#
# The point is not that the guard "runs". It is that it reverts when it should
# and — the assertion that actually costs something — that it does NOT revert
# when everything is fine.

set -u
GUARD=/work/canary-guard.sh
PASS=0
FAIL=0

ok()   { PASS=$((PASS+1)); echo "  PASS: $1"; }
bad()  { FAIL=$((FAIL+1)); echo "  FAIL: $1"; }

# Build a sandbox world. $1 = scenario name.
setup() {
	W=/tmp/w-$1
	rm -rf "$W"; mkdir -p "$W/bin" "$W/canary" "$W/proc/4242"
	printf 'MemAvailable:   %s kB\n' "${MEM:-60000}" >"$W/meminfo"
	printf '{"state":"%s","pid":%s,"restartCount":0,"resources":{"rssBytes":63852544}}\n' \
		"${XSTATE:-running}" "${XPID:-4242}" >"$W/canary/status.json"
	printf 'xray\0run\0-c\0/tmp/x.json\0' >"$W/proc/4242/cmdline"

	# nft stub: prints a table with a counter unless NFT_GONE is set.
	cat >"$W/bin/nft" <<STUB
#!/bin/sh
echo "nft \$*" >>$W/calls
case "\$*" in
  "list table inet vctl")
      [ -n "${NFT_GONE:-}" ] && exit 1
      echo "  counter vctl_tproxy_hits { packets ${HITS:-1000} bytes 500000 }"
      exit 0 ;;
esac
exit 0
STUB

	# ip stub: reports the policy route unless RULE_GONE is set.
	cat >"$W/bin/ip" <<STUB
#!/bin/sh
echo "ip \$*" >>$W/calls
case "\$*" in
  "rule show") [ -n "${RULE_GONE:-}" ] || echo "100:	from all fwmark 0x1 lookup 100" ;;
esac
exit 0
STUB

	# curl stub: HTTP code from EGRESS_CODE.
	cat >"$W/bin/curl" <<STUB
#!/bin/sh
echo "curl \$*" >>$W/calls
printf '%s' "${EGRESS_CODE:-200}"
exit 0
STUB

	for c in killall uci; do
		printf '#!/bin/sh\necho "%s $*" >>%s\nexit 0\n' "$c" "$W/calls" >"$W/bin/$c"
	done
	mkdir -p "$W/etc/init.d"
	printf '#!/bin/sh\necho "passwall2 $*" >>%s\nexit 0\n' "$W/calls" >"$W/bin/passwall2-init"
	chmod +x "$W"/bin/* 2>/dev/null
	: >"$W/calls"
}

run_guard() {
	# shellcheck disable=SC2086
	env PATH="$W/bin:$PATH" \
		DIR="$W/canary" MEMINFO="$W/meminfo" PROCDIR="$W/proc" \
		TEARDOWN="$W/teardown-absent.sh" \
		ABORT_FILE="$W/abort" EXTEND_FILE="$W/extend" \
		CHECK_INTERVAL=1 FAIL_STREAK=3 CAP_SECONDS="${CAP:-3600}" \
		MEM_FLOOR_KB=25000 \
		sh "$GUARD" >"$W/out" 2>&1 &
	GPID=$!
}

reason() { cat "$W/canary/revert-reason" 2>/dev/null; }
reverted() { [ -f "$W/canary/revert-reason" ]; }

# ---------------------------------------------------------------------------
echo "1. healthy system must NOT revert (negative control)"
setup healthy; run_guard; sleep 6; kill $GPID 2>/dev/null
if reverted; then bad "healthy run reverted: $(reason)"; else ok "no revert after 6 sweeps"; fi
# The line is timestamp-prefixed, so anchoring at ^ would never match.
grep -q " ok mem=" "$W/canary/guard.log" && ok "logged healthy sweeps" || bad "no healthy sweep logged"

# ---------------------------------------------------------------------------
echo "2. xray dies -> revert"
XSTATE=stopped setup xraydead; run_guard; sleep 6; kill $GPID 2>/dev/null
case "$(reason)" in *xray_dead*) ok "reverted: $(reason)";; *) bad "expected xray_dead, got '$(reason)'";; esac

# ---------------------------------------------------------------------------
echo "3. pid points at a non-xray process -> revert (pid reuse guard)"
XPID=9999 setup pidreuse
mkdir -p "$W/proc/9999"; printf 'dropbear\0' >"$W/proc/9999/cmdline"
run_guard; sleep 6; kill $GPID 2>/dev/null
case "$(reason)" in *xray_dead*) ok "reverted on pid reuse";; *) bad "pid reuse not caught, got '$(reason)'";; esac

# ---------------------------------------------------------------------------
echo "4. low memory -> revert"
MEM=10000 setup lowmem; run_guard; sleep 6; kill $GPID 2>/dev/null
case "$(reason)" in *low_mem*) ok "reverted: $(reason)";; *) bad "expected low_mem, got '$(reason)'";; esac

# ---------------------------------------------------------------------------
echo "5. data plane vanished -> revert"
NFT_GONE=1 setup nogone; run_guard; sleep 6; kill $GPID 2>/dev/null
case "$(reason)" in *dataplane_gone*) ok "reverted: $(reason)";; *) bad "expected dataplane_gone, got '$(reason)'";; esac

echo "5b. policy route vanished -> revert"
RULE_GONE=1 setup norule; run_guard; sleep 6; kill $GPID 2>/dev/null
case "$(reason)" in *dataplane_gone*) ok "reverted on missing ip rule";; *) bad "missing rule not caught, got '$(reason)'";; esac

# ---------------------------------------------------------------------------
echo "6. egress down -> revert"
EGRESS_CODE=000 setup noegress; run_guard; sleep 6; kill $GPID 2>/dev/null
case "$(reason)" in *egress_down*) ok "reverted: $(reason)";; *) bad "expected egress_down, got '$(reason)'";; esac

# ---------------------------------------------------------------------------
echo "7. transient blip must NOT revert (streak resets)"
setup blip; run_guard
sleep 2; MEMFILE="$W/meminfo"; printf 'MemAvailable:   10000 kB\n' >"$MEMFILE"   # 1 bad sweep
sleep 2; printf 'MemAvailable:   60000 kB\n' >"$MEMFILE"                         # recover
sleep 4; kill $GPID 2>/dev/null
if reverted; then bad "single blip caused revert: $(reason)"; else ok "survived a transient blip"; fi
grep -q "recovered after" "$W/canary/guard.log" && ok "logged the recovery" || bad "recovery not logged"

# ---------------------------------------------------------------------------
echo "8. abort file -> immediate revert"
setup abort; run_guard; sleep 2; touch "$W/abort"; sleep 3; kill $GPID 2>/dev/null
case "$(reason)" in *"manual abort"*) ok "reverted on abort";; *) bad "abort ignored, got '$(reason)'";; esac

# ---------------------------------------------------------------------------
echo "9. absolute cap -> revert, and extend pushes it out"
# CAP must be exported: `CAP=3 setup cap` would scope it to setup, while the
# value is read by run_guard — the guard would silently use the 3600s default
# and the test would "pass" by never exercising the cap at all.
setup cap; CAP=3; export CAP; run_guard; sleep 6; kill $GPID 2>/dev/null
case "$(reason)" in *"absolute cap"*) ok "cap fired";; *) bad "cap did not fire, got '$(reason)'";; esac

setup extend; CAP=4; export CAP; run_guard
sleep 2; touch "$W/extend"
sleep 4; kill $GPID 2>/dev/null
if reverted; then bad "extend did not push the cap: $(reason)"; else ok "extend pushed the cap out"; fi
grep -q "cap extended" "$W/canary/guard.log" && ok "logged the extension" || bad "extension not logged"

# ---------------------------------------------------------------------------
# CAP was exported above; leaving it set would silently cap the remaining
# scenarios at 4s and make them revert for the wrong reason.
unset CAP

echo "10. counter regression -> IMMEDIATE revert (must not need a streak)"
setup regress; run_guard
sleep 3
# Rebuild the stub with a lower count: this is what a table torn down and
# recreated underneath us looks like.
cat >"$W/bin/nft" <<STUB
#!/bin/sh
case "\$*" in
  "list table inet vctl") echo "  counter vctl_tproxy_hits { packets 5 bytes 100 }"; exit 0 ;;
esac
exit 0
STUB
chmod +x "$W/bin/nft"
# Wait strictly LESS than FAIL_STREAK sweeps (3 x 1s). If the guard still
# reverts, it bypassed the streak — which is the whole point, since the probe
# rebases its baseline and a second consecutive regression can never occur.
sleep 2; kill $GPID 2>/dev/null
case "$(reason)" in
  *counter_regressed*) ok "reverted inside 2s, before any streak could form";;
  "") bad "regression not caught at all";;
  *) bad "reverted for the wrong reason: '$(reason)'";;
esac

# ---------------------------------------------------------------------------
echo "11. revert actually unwinds the data plane"
setup unwind; run_guard; sleep 2; touch "$W/abort"; sleep 3; kill $GPID 2>/dev/null
for expect in "nft delete table inet vctl" "ip rule del fwmark 0x1 lookup 100" \
              "uci set passwall2.@global[0].enabled=1"; do
	grep -qF "$expect" "$W/calls" && ok "issued: $expect" || bad "never issued: $expect"
done

# ---------------------------------------------------------------------------
# Only the init script hands the router back to the legacy agent (start_service
# disables it behind a marker; stop_service consumes that marker). A revert that
# reaches for killall instead leaves the router working but unmanageable from
# the panel — which on a customer router means a site visit.
echo "12. revert prefers the init script, and does NOT use killall when it exists"
setup initstop
printf '#!/bin/sh\necho "INITSCRIPT $*" >>%s\nexit 0\n' "$W/calls" >"$W/bin/vcp-init"
chmod +x "$W/bin/vcp-init"
INITSCRIPT="$W/bin/vcp-init"; export INITSCRIPT
run_guard; sleep 2; touch "$W/abort"; sleep 3; kill $GPID 2>/dev/null
grep -qF "INITSCRIPT stop" "$W/calls" && ok "stopped via init script" || bad "init script never called"
grep -qF "killall vctl" "$W/calls" && bad "used killall despite an init script (legacy agent stays disabled)" \
	|| ok "did not fall back to killall"
unset INITSCRIPT

echo "12b. with no init script it still stops vctl (killall fallback)"
setup nokill
INITSCRIPT="$W/does-not-exist"; export INITSCRIPT
run_guard; sleep 2; touch "$W/abort"; sleep 3; kill $GPID 2>/dev/null
grep -qF "killall vctl" "$W/calls" && ok "fell back to killall" || bad "nothing stopped vctl at all"
unset INITSCRIPT

echo
echo "==================== $PASS passed, $FAIL failed ===================="
[ "$FAIL" -eq 0 ]
