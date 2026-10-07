#!/bin/sh
# vectra-update-guard: the safety net under a legacy-agent self-update.
#
# The panel's self-update command (apps/web/src/lib/controller-update-jobs.ts)
# writes this script to /etc/vectra-controller/update-rollback/guard.sh — it
# is NOT part of the packages being replaced, so it protects an update from any
# installed version — and drives it:
#
#   prepare <to>     before opkg: copy every file of vectra-controller-agent and
#                    luci-app-vectra-controller (their .list paths, their
#                    /usr/lib/opkg/info/<pkg>.* files) into one tar.gz, keep
#                    their /usr/lib/opkg/status stanzas, write the session
#                    metadata and a managed cron line. The copy goes to /etc
#                    (overlay) when the overlay keeps >= 8 MB free after it,
#                    else to /tmp (lost on reboot) when /tmp keeps >= 16 MB,
#                    else exit 73: no update without a way back. Exit 74 when
#                    an earlier update is still being verified.
#   restore-files <reason>
#                    opkg or a post-install check failed: put the old files and
#                    stanzas back. The old agent is still the running process
#                    (the update installs with VECTRA_SKIP_POSTINST_RESTART), so
#                    it is not restarted unless it is gone.
#   arm              the install is verified; the restart is due.
#   launch           start `run` detached (setsid): it outlives the agent, its
#                    job runner and the restart it performs.
#   run              wait a few seconds (the agent reports the job), restart
#                    the agent, then judge it every few seconds until decided.
#   tick             one judgement; cron runs it every minute, so the guard
#                    resumes after a reboot or if `run` died.
#   rollback <reason>
#                    stop the agent, restore, start it (the watchdog uses this
#                    when the binary is missing).
#   retry            one throttled retry of an incomplete restore (watchdog)
#   boot             early boot hook (/etc/init.d/vectra-update-guard, START=19,
#                    before the agent's S95): counts boots of an unconfirmed
#                    update; at G_MAX_BOOTS it rolls back before the new agent
#                    starts again (a version that hangs the router)
#   clear            operator: end a restore-failed/manual session, only while
#                    the agent runs from an executable binary
#
# Judgement (`tick`), measured from the restart (re-armed after a reboot):
#   - binary missing/not executable                      -> rollback
#   - never ran G_STABLE s in a row within G_CRASH s     -> rollback (crash loop)
#   - ran G_STABLE s and state.json shows a control-plane contact after the
#     restart                                            -> success, copy removed
#   - no contact by G_CONTACT s and the panel's /api/health has answered from
#     this router for G_HOLD s                           -> rollback (the new
#     version cannot talk although the network can)
#   - no contact by G_NETWORK s and the panel has not answered from this
#     router for the last G_HOLD s either                -> rollback: the job
#     itself proved the router could reach the panel before the update, and a
#     new version can cut its own path (carve-out, nft, TPROXY). A panel that
#     answers again is left to the rule above.
# Windows are measured on /proc/uptime (a wall-clock jump moves nothing) and
# re-armed after a reboot; only the contact stamp, a wall-clock value, is
# compared with the wall-clock time of the restart.
# A rollback restores files, info files and the two status stanzas (no other
# package is touched, nor PassWall/xray/network configuration), checks the
# result (tar's exit code, the agent binary byte-for-byte against the copy,
# the stanzas' versions), restarts the agent unless vctl owns the router, and
# writes /etc/vectra-controller/update-rollback.marker. An incomplete restore
# keeps the copy and the session (phase restore-failed) and is retried from
# every tick and by the watchdog, with a backoff (1, 2, 4 … 30 min) and at most
# G_RESTORE_MAX_ATTEMPTS attempts; then phase manual: the agent is left alone
# and an operator ends it (`clear`, or a forced update while the agent runs).
# "Complete" means: the whole copy was extracted without error (tar_ok) and
# every file of the copy is on disk as it was (sha256 manifest made at
# prepare), plus the stanzas' versions. A retry first checks that, and never
# stops an agent process started from the copy (its /proc/<pid>/exe, or the
# pid the guard itself started from the verified binary). With no agent
# running at all, manual keeps retrying every G_RESTORE_RETRY_MAX_SECONDS. `prepare` refuses (exit 75) a
# version this router rolled back from less than 24 h ago, unless
# VECTRA_GUARD_FORCE=1.
#
# POSIX sh (BusyBox ash, dash). Tests: tests/update_guard_test.sh.

set -u

# cron's PATH may lack /sbin (uci) and /usr/sbin; the caller's PATH keeps
# priority.
PATH="${PATH:-/usr/bin:/bin}:/usr/sbin:/usr/bin:/sbin:/bin"
export PATH

G_ROOT="${VECTRA_GUARD_ROOT:-}"
G_DIR="$G_ROOT/etc/vectra-controller/update-rollback"
G_SELF="$G_DIR/guard.sh"
G_META="$G_DIR/meta"
G_STANZAS="$G_DIR/status.stanzas"
G_TMP_DIR="$G_ROOT/tmp/vectra-update-rollback"
G_RUN="$G_ROOT/tmp/vectra-update-guard"
G_MARKER="$G_ROOT/etc/vectra-controller/update-rollback.marker"
G_CRONTAB="$G_ROOT/etc/crontabs/root"
G_INIT="$G_ROOT/etc/init.d/vectra-controller"
G_BOOT_HOOK="$G_ROOT/etc/init.d/vectra-update-guard"
G_MANIFEST="$G_DIR/manifest"
G_MAX_BOOTS=3
G_AGENT_BIN="$G_ROOT/usr/sbin/vectra-controller-agent"
G_INFO="$G_ROOT/usr/lib/opkg/info"
G_STATUS="$G_ROOT/usr/lib/opkg/status"
G_PKGS="vectra-controller-agent luci-app-vectra-controller"
G_OVERLAY_FLOOR_MB=8
G_TMP_FLOOR_MB=16
G_SESSION_STALE_SECONDS=7200
G_LOCK_STALE_SECONDS=600
G_MARKER_FRESH_SECONDS=86400
G_RESTORE_RETRY_SECONDS=60
G_RESTORE_RETRY_MAX_SECONDS=1800
G_RESTORE_MAX_ATTEMPTS=10
# Owner a rollback copy (and its directory) must have before it is extracted
# over /. Tests override it.
G_OWNER_UID=0
G_LOG_TAG="vectra-update-guard"
G_CRON_BEGIN="# >>> vectra-update-guard (managed) >>>"
G_CRON_END="# <<< vectra-update-guard (managed) <<<"

g_log() {
	if command -v logger >/dev/null 2>&1; then
		logger -t "$G_LOG_TAG" "$1"
	else
		printf '[%s] %s\n' "$G_LOG_TAG" "$1" >&2
	fi
}

g_now() {
	date +%s
}

# Seconds since boot: the windows' clock, immune to wall-clock steps.
g_mono() {
	g_up=""
	read -r g_up _ < /proc/uptime 2>/dev/null || true
	g_up="${g_up%%.*}"
	case "$g_up" in
	'' | *[!0-9]*) g_now ;;
	*) printf '%s' "$g_up" ;;
	esac
}

g_epoch_file() {
	g_value="$(cat "$1" 2>/dev/null || true)"
	case "$g_value" in
	'' | *[!0-9]*) ;;
	*) printf '%s' "$g_value" ;;
	esac
}

g_df_free_mb() {
	df -kP "$1" 2>/dev/null | awk 'NR == 2 { print int($4 / 1024); found = 1; exit } END { if (!found) print 0 }'
}

g_pkg_version() {
	awk -v pkg="$1" '/^Package: / { c = ($2 == pkg); next } c && /^Version: / { print $2; exit }' "$G_STATUS" 2>/dev/null
}

g_clean_value() {
	printf '%s' "$1" | tr -cd 'A-Za-z0-9._+~:/-'
}

# Leading zeros stripped so $(( )) never reads octal; no `10#` bashism.
g_strip0() {
	g_v="$1"
	while :; do
		case "$g_v" in
		0[0-9]*) g_v="${g_v#0}" ;;
		*) break ;;
		esac
	done
	[ -n "$g_v" ] || g_v=0
	printf '%s' "$g_v"
}

# RFC3339 UTC (the agent's recovery.FormatTime) -> epoch; empty on failure.
# Same algorithm as the watchdog: `date -d` first, then days-from-civil.
g_rfc3339_to_epoch() {
	g_ts="$1"
	[ -n "$g_ts" ] || return 0
	g_e="$(date -u -d "$g_ts" +%s 2>/dev/null || true)"
	case "$g_e" in
	'' | *[!0-9]*) ;;
	*)
		printf '%s' "$g_e"
		return 0
		;;
	esac
	g_core="${g_ts%Z}"
	g_core="${g_core%+00:00}"
	g_core="${g_core%-00:00}"
	# shellcheck disable=SC2046
	set -- $(printf '%s' "$g_core" | tr 'T:-' '   ')
	[ "$#" -ge 6 ] || return 0
	case "$1$2$3$4$5$6" in
	'' | *[!0-9]*) return 0 ;;
	esac
	g_y="$(g_strip0 "$1")"
	g_mo="$(g_strip0 "$2")"
	g_d="$(g_strip0 "$3")"
	g_h="$(g_strip0 "$4")"
	g_mi="$(g_strip0 "$5")"
	g_s="$(g_strip0 "$6")"
	[ "$g_mo" -ge 1 ] && [ "$g_mo" -le 12 ] || return 0
	[ "$g_mo" -le 2 ] && g_y=$((g_y - 1))
	g_era=$((g_y / 400))
	g_yoe=$((g_y - g_era * 400))
	g_mp=$(((g_mo + 9) % 12))
	g_doy=$(((153 * g_mp + 2) / 5 + g_d - 1))
	g_doe=$((g_yoe * 365 + g_yoe / 4 - g_yoe / 100 + g_doy))
	g_days=$((g_era * 146097 + g_doe - 719468))
	printf '%s' "$((g_days * 86400 + g_h * 3600 + g_mi * 60 + g_s))"
}

g_uci_get() {
	uci -q get "vectra-controller.main.$1" 2>/dev/null || true
}

g_state_path() {
	g_p="$(g_uci_get state_path)"
	[ -n "$g_p" ] || g_p=/etc/vectra-controller/state.json
	printf '%s%s' "$G_ROOT" "$g_p"
}

g_contact_raw() {
	g_f="$(g_state_path)"
	[ -f "$g_f" ] || return 0
	jsonfilter -i "$g_f" -e '@.control_plane_recovery.last_successful_control_plane_at' 2>/dev/null || true
}

g_contact_epoch() {
	g_rfc3339_to_epoch "$(g_contact_raw)"
}

# The contact stamp as it was when the clock (re)started: success needs a new
# one, so a clock stepped back cannot make an old stamp look fresh.
g_note_window_stamp() {
	g_contact_raw > "$G_RUN/stamp0" 2>/dev/null || true
}

g_owner_uid() {
	ls -ldn "$1" 2>/dev/null | awk '{ print $3 }'
}

# A copy is extracted over / only if it and its directory are what prepare
# made: regular file / directory, no symlink, owned by G_OWNER_UID.
g_copy_trusted() {
	[ -f "$1" ] && [ ! -L "$1" ] || return 1
	g_cd="${1%/*}"
	[ -d "$g_cd" ] && [ ! -L "$g_cd" ] || return 1
	[ "$(g_owner_uid "$1")" = "$G_OWNER_UID" ] && [ "$(g_owner_uid "$g_cd")" = "$G_OWNER_UID" ]
}

g_control_url() {
	g_u="$(g_uci_get control_url)"
	[ -n "$g_u" ] || g_u="$(g_uci_get panel_url)"
	[ -n "$g_u" ] || g_u=https://api.vectra-pro.net
	printf '%s' "${g_u%/}"
}

# Does the panel answer from this router? Same endpoint as the agent's own
# reachability probe; any HTTP error counts as "no".
g_panel_reachable() {
	g_url="$(g_control_url)/api/health"
	if command -v uclient-fetch >/dev/null 2>&1; then
		uclient-fetch -q -T 10 -O /dev/null "$g_url" >/dev/null 2>&1
	elif command -v wget >/dev/null 2>&1; then
		wget -q -T 10 -O /dev/null "$g_url" >/dev/null 2>&1
	else
		return 1
	fi
}

# The agent process by name (argv[0] or script name), never by command line:
# `pgrep -f` also matches the update command's own shell.
g_agent_pid() {
	if command -v pidof >/dev/null 2>&1; then
		pidof vectra-controller-agent 2>/dev/null | awk '{ print $1 }'
		return 0
	fi
	for g_pd in /proc/[0-9]*; do
		g_a0="$(tr '\000' '\n' < "$g_pd/cmdline" 2>/dev/null | head -n 1)"
		[ "${g_a0##*/}" = vectra-controller-agent ] && { printf '%s\n' "${g_pd#/proc/}"; return 0; }
	done
	return 0
}

g_kill_agent() {
	g_i=0
	while [ "$g_i" -lt 5 ]; do
		g_p="$(g_agent_pid)"
		[ -n "$g_p" ] || return 0
		kill "$g_p" 2>/dev/null || true
		sleep 1
		g_i=$((g_i + 1))
	done
	g_p="$(g_agent_pid)"
	[ -z "$g_p" ] || kill -9 "$g_p" 2>/dev/null || true
}

# vctl (vectra-controller-pro) owns the router: the legacy agent must stay off.
g_vctl_owns() {
	# Only the marker of the legacy agent vctl disabled: vctl's other markers
	# (.passwall-retired-by-vctl, .passwall-disabled-by-vctl) outlive a
	# hand-back and say nothing about who runs the router now.
	[ -f "$G_ROOT/etc/vectra-controller-pro/.legacy-agent-disabled-by-vctl" ] && return 0
	[ -f "$G_ROOT/tmp/vectra-trial.d/.legacy-agent-disabled-by-vctl" ] && return 0
	if [ -x "$G_ROOT/etc/init.d/vectra-controller-pro" ] && "$G_ROOT/etc/init.d/vectra-controller-pro" running >/dev/null 2>&1; then
		return 0
	fi
	if command -v nft >/dev/null 2>&1 && nft list tables 2>/dev/null | grep -qx 'table inet vctl'; then
		return 0
	fi
	return 1
}

g_restart_agent() {
	if g_vctl_owns; then
		g_log "vctl owns this router; not starting the legacy agent"
		return 0
	fi
	"$G_INIT" enable >/dev/null 2>&1 || true
	if "$G_INIT" running >/dev/null 2>&1; then
		"$G_INIT" restart >"$G_ROOT/tmp/vectra-controller-self-update.log" 2>&1 || true
	else
		"$G_INIT" start >"$G_ROOT/tmp/vectra-controller-self-update.log" 2>&1 || true
	fi
}

# procd may still be finishing the stopped instance: give the start a moment.
g_wait_agent_up() {
	g_vctl_owns && return 0
	g_w=0
	while [ "$g_w" -lt 15 ]; do
		[ -n "$(g_agent_pid)" ] && return 0
		sleep 1
		g_w=$((g_w + 1))
	done
	return 1
}

g_cron_reload() {
	[ -x "$G_ROOT/etc/init.d/cron" ] || return 0
	"$G_ROOT/etc/init.d/cron" enable >/dev/null 2>&1 || true
	"$G_ROOT/etc/init.d/cron" restart >/dev/null 2>&1 || "$G_ROOT/etc/init.d/cron" start >/dev/null 2>&1 || true
}

# Crontab without our block, then optionally with it (written via mv so the
# crontab directory's mtime changes too).
g_cron_write() {
	mkdir -p "${G_CRONTAB%/*}" 2>/dev/null || true
	touch "$G_CRONTAB" 2>/dev/null || true
	g_ct="$G_CRONTAB.vectra-guard.$$"
	awk -v b="$G_CRON_BEGIN" -v e="$G_CRON_END" '$0 == b { skip = 1; next } $0 == e { skip = 0; next } !skip { print }' "$G_CRONTAB" > "$g_ct" || { rm -f "$g_ct"; return 1; }
	if [ "$1" = install ]; then
		printf '%s\n* * * * * sh %s tick >/dev/null 2>&1\n%s\n' "$G_CRON_BEGIN" "$G_SELF" "$G_CRON_END" >> "$g_ct"
	fi
	mv "$g_ct" "$G_CRONTAB"
	g_cron_reload
}

g_cron_remove() {
	grep -Fxq "$G_CRON_BEGIN" "$G_CRONTAB" 2>/dev/null || return 0
	g_cron_write remove
}

# $1 seconds to wait. The lock holds "<pid> <epoch>"; a dead owner, or one
# older than G_LOCK_STALE_SECONDS, does not count.
g_lock() {
	mkdir -p "$G_RUN" 2>/dev/null || true
	g_waited=0
	while ! mkdir "$G_RUN/lock" 2>/dev/null; do
		g_owner="$(cat "$G_RUN/lock/owner" 2>/dev/null || true)"
		if [ -z "$g_owner" ]; then
			sleep 1
			g_owner="$(cat "$G_RUN/lock/owner" 2>/dev/null || true)"
		fi
		g_opid="${g_owner%% *}"
		g_oep="$(g_strip0 "${g_owner#* }")"
		case "$g_oep" in *[!0-9]*) g_oep=0 ;; esac
		g_lage=$(($(g_mono) - g_oep))
		if [ -z "$g_owner" ] || ! kill -0 "$g_opid" 2>/dev/null || [ "$g_lage" -ge "$G_LOCK_STALE_SECONDS" ] || [ "$g_lage" -lt 0 ]; then
			rm -rf "$G_RUN/lock"
			continue
		fi
		[ "$g_waited" -lt "$1" ] || return 1
		sleep 1
		g_waited=$((g_waited + 1))
	done
	printf '%s %s\n' "$$" "$(g_mono)" > "$G_RUN/lock/owner"
}

g_unlock() {
	rm -rf "$G_RUN/lock"
}

g_load_meta() {
	[ -f "$G_META" ] || return 1
	# shellcheck disable=SC1090
	. "$G_META"
}

g_phase() {
	cat "$G_DIR/phase" 2>/dev/null || true
}

g_set() {
	printf '%s\n' "$2" > "$G_DIR/$1.new" && mv "$G_DIR/$1.new" "$G_DIR/$1"
}

g_clear_run() {
	rm -f "$G_RUN/boot" "$G_RUN/pid" "$G_RUN/since" "$G_RUN/ever_stable" "$G_RUN/last_probe" "$G_RUN/reach_since" "$G_RUN/noted" "$G_RUN/old.list" "$G_RUN/stamp0" "$G_RUN/unreach_since" "$G_RUN/restoring" "$G_RUN/last_restore_try" "$G_RUN/copy_pid" "$G_RUN/tar_ok" 2>/dev/null || true
}

# End of a session, whatever the outcome: no copy, no metadata, no cron line.
g_finish() {
	rm -rf "$G_TMP_DIR" 2>/dev/null || true
	rm -rf "$G_DIR" 2>/dev/null || true
	g_clear_run
	g_boot_hook remove
	g_cron_remove
}

# The early boot hook, START=19 (before the agent's S95). Not part of either
# package: it exists only while a session does.
g_boot_hook() {
	if [ "$1" = install ]; then
		mkdir -p "${G_BOOT_HOOK%/*}" 2>/dev/null || true
		printf '%s\n' '#!/bin/sh /etc/rc.common' 'START=19' \
			'boot() { [ -f /etc/vectra-controller/update-rollback/meta ] && sh /etc/vectra-controller/update-rollback/guard.sh boot; return 0; }' \
			'start() { return 0; }' > "$G_BOOT_HOOK.new" && chmod 0755 "$G_BOOT_HOOK.new" && mv "$G_BOOT_HOOK.new" "$G_BOOT_HOOK"
		"$G_BOOT_HOOK" enable >/dev/null 2>&1 || true
	else
		[ -e "$G_BOOT_HOOK" ] || return 0
		"$G_BOOT_HOOK" disable >/dev/null 2>&1 || true
		rm -f "$G_BOOT_HOOK" "$G_ROOT"/etc/rc.d/S19vectra-update-guard 2>/dev/null || true
	fi
}

# Count this boot once (the boot hook or, without it, the first tick).
g_count_boot() {
	mkdir -p "$G_RUN" 2>/dev/null || true
	[ -f "$G_RUN/boot_counted" ] && return 0
	: > "$G_RUN/boot_counted"
	g_boots="$(g_epoch_file "$G_DIR/boots")"
	g_set boots $((${g_boots:-0} + 1))
}

# Booted G_MAX_BOOTS times without the update confirming: roll back. 0 = did.
g_boot_limit() {
	g_boots="$(g_epoch_file "$G_DIR/boots")"
	[ "${g_boots:-0}" -ge "$G_MAX_BOOTS" ] || return 1
	g_rollback full "the router booted ${g_boots} times without ${G_TO:-the new version} confirming (boot loop)"
	return 0
}

g_note_once() {
	[ "$(cat "$G_RUN/noted" 2>/dev/null || true)" = "$1" ] && return 0
	printf '%s\n' "$1" > "$G_RUN/noted"
	g_log "$2"
}

g_prepare() {
	g_to="$(g_clean_value "${1:-}")"
	g_t="$(g_now)"
	if [ -f "$G_META" ]; then
		case "$(g_phase)" in
		restoring | restore-failed | manual)
			if [ -n "${VECTRA_GUARD_FORCE:-}" ] && g_lock 60 && { g_agent_runs_executable || { g_unlock; false; }; }; then
				g_log "operator-forced update: ending the $(g_phase) rollback session (the agent runs from an executable binary); the rollback copy is dropped"
				g_end_hopeless keep
				g_unlock
			else
				echo "controller self-update refused: an earlier rollback on this router is incomplete ($(g_phase)); a forced update clears it once the agent runs, or run: sh $G_SELF clear" >&2
				return 74
			fi
			;;
		esac
	fi
	if [ -f "$G_META" ]; then
		g_created="$( (. "$G_META" 2>/dev/null; printf '%s' "${G_CREATED:-}") )"
		case "$g_created" in '' | *[!0-9]*) g_created="" ;; esac
		if [ -n "$g_created" ] && [ $((g_t - g_created)) -ge 0 ] && [ $((g_t - g_created)) -lt "$G_SESSION_STALE_SECONDS" ] && [ -n "$(g_phase)" ]; then
			echo "controller self-update refused: the previous update (since epoch $g_created) is still being verified by $G_SELF" >&2
			return 74
		fi
		g_log "discarding a stale update-guard session (prepared at ${g_created:-unknown})"
		rm -f "$G_META" "$G_STANZAS" "$G_DIR/backup.tgz" "$G_DIR/phase" "$G_DIR/window" "$G_DIR/prepared_at" "$G_DIR/armed_at" 2>/dev/null || true
		rm -rf "$G_TMP_DIR" 2>/dev/null || true
		g_clear_run
	fi
	if [ -z "${VECTRA_GUARD_FORCE:-}" ] && [ -f "$G_MARKER" ]; then
		g_mf="$(sed -n 's/^failed_version=//p' "$G_MARKER" 2>/dev/null | head -n 1)"
		g_me="$(sed -n 's/^epoch=//p' "$G_MARKER" 2>/dev/null | head -n 1)"
		case "$g_me" in '' | *[!0-9]*) g_me="" ;; esac
		if [ -n "$g_to" ] && [ "$g_mf" = "$g_to" ] && [ -n "$g_me" ] && [ $((g_t - g_me)) -lt "$G_MARKER_FRESH_SECONDS" ]; then
			echo "controller self-update refused (75): $g_to was rolled back on this router $((g_t - g_me))s ago ($(sed -n 's/^reason=//p' "$G_MARKER" | head -n 1)); to try it again force it: router Updates tab, or VectraPanelCli.sh update controller <router> --force" >&2
			return 75
		fi
	fi
	rm -rf "$G_TMP_DIR" 2>/dev/null || true
	if ! mkdir -p "$G_DIR" 2>/dev/null || ! mkdir -m 700 "$G_TMP_DIR" 2>/dev/null; then
		echo "controller self-update refused (no room for a rollback copy): cannot create $G_DIR / $G_TMP_DIR" >&2
		return 73
	fi
	chmod 700 "$G_DIR" 2>/dev/null || true
	g_list="$G_TMP_DIR/files"
	: > "$g_list"
	for g_pkg in $G_PKGS; do
		[ -f "$G_INFO/$g_pkg.list" ] || continue
		while IFS= read -r g_path || [ -n "$g_path" ]; do
			g_path="${g_path%%	*}"
			case "$g_path" in /*) ;; *) continue ;; esac
			if [ -L "$G_ROOT$g_path" ] || { [ -e "$G_ROOT$g_path" ] && [ ! -d "$G_ROOT$g_path" ]; }; then
				printf '%s\n' "${g_path#/}" >> "$g_list"
			fi
		done < "$G_INFO/$g_pkg.list"
		for g_f in "$G_INFO/$g_pkg".*; do
			[ -e "$g_f" ] && printf '%s\n' "${g_f#"$G_ROOT"/}" >> "$g_list"
		done
	done
	if ! grep -q 'vectra-controller-agent.list$' "$g_list"; then
		rm -rf "$G_TMP_DIR"
		echo "controller self-update refused: vectra-controller-agent is not installed through opkg, nothing to roll back to" >&2
		return 73
	fi
	g_stage="$G_TMP_DIR/backup.tgz"
	# shellcheck disable=SC2046
	if ! tar -C "${G_ROOT:-/}" -czf "$g_stage" $(cat "$g_list") 2>/dev/null || ! tar -tzf "$g_stage" >/dev/null 2>&1; then
		rm -rf "$G_TMP_DIR"
		echo "controller self-update refused (no room for a rollback copy): writing it to /tmp failed" >&2
		return 73
	fi
	g_write_manifest "$g_list" > "$G_MANIFEST.new" && mv "$G_MANIFEST.new" "$G_MANIFEST"
	awk -v a=vectra-controller-agent -v b=luci-app-vectra-controller '
		/^Package: / { if (keep) print ""; keep = ($2 == a || $2 == b) }
		keep && $0 == "" { print ""; keep = 0; next }
		keep { print }
		END { if (keep) print "" }' "$G_STATUS" > "$G_STANZAS.new" && mv "$G_STANZAS.new" "$G_STANZAS"
	g_size_kb=$((($(wc -c < "$g_stage") + 1023) / 1024))
	g_size_mb=$(((g_size_kb + 1023) / 1024))
	g_persist_free="$(g_df_free_mb "$G_DIR")"
	g_backup=""
	if [ $((g_persist_free - g_size_mb)) -ge "$G_OVERLAY_FLOOR_MB" ] &&
		cp "$g_stage" "$G_DIR/backup.tgz.new" 2>/dev/null && cmp -s "$g_stage" "$G_DIR/backup.tgz.new" &&
		mv "$G_DIR/backup.tgz.new" "$G_DIR/backup.tgz"; then
		rm -f "$g_stage"
		g_backup="$G_DIR/backup.tgz"
		g_where=persistent
	else
		rm -f "$G_DIR/backup.tgz.new"
		if [ "$(g_df_free_mb "$G_TMP_DIR")" -ge "$G_TMP_FLOOR_MB" ]; then
			g_backup="$g_stage"
			g_where=tmp
		fi
	fi
	rm -f "$g_list"
	if [ -z "$g_backup" ]; then
		rm -rf "$G_TMP_DIR"
		rm -f "$G_STANZAS" "$G_MANIFEST"
		echo "controller self-update refused (no room for a rollback copy of ${g_size_kb} KiB): /overlay would keep $((g_persist_free - g_size_mb)) MB (< ${G_OVERLAY_FLOOR_MB}), /tmp $(g_df_free_mb "$G_ROOT/tmp") MB (< ${G_TMP_FLOOR_MB})" >&2
		return 73
	fi
	{
		printf "G_CREATED='%s'\n" "$g_t"
		printf "G_TO='%s'\n" "$g_to"
		printf "G_FROM_AGENT='%s'\n" "$(g_clean_value "$(g_pkg_version vectra-controller-agent)")"
		printf "G_FROM_LUCI='%s'\n" "$(g_clean_value "$(g_pkg_version luci-app-vectra-controller)")"
		printf "G_BACKUP='%s'\n" "$g_backup"
		printf "G_WHERE='%s'\n" "$g_where"
		printf "G_STABLE='%s'\n" "$(g_strip0 "${VECTRA_GUARD_STABLE_SECONDS:-120}")"
		printf "G_CRASH='%s'\n" "$(g_strip0 "${VECTRA_GUARD_CRASH_SECONDS:-300}")"
		printf "G_CONTACT='%s'\n" "$(g_strip0 "${VECTRA_GUARD_CONTACT_SECONDS:-900}")"
		printf "G_PROBE_FROM='%s'\n" "$(g_strip0 "${VECTRA_GUARD_PROBE_FROM_SECONDS:-600}")"
		printf "G_PROBE_EVERY='%s'\n" "$(g_strip0 "${VECTRA_GUARD_PROBE_EVERY_SECONDS:-60}")"
		printf "G_HOLD='%s'\n" "$(g_strip0 "${VECTRA_GUARD_HOLD_SECONDS:-180}")"
		printf "G_NETWORK='%s'\n" "$(g_strip0 "${VECTRA_GUARD_NETWORK_SECONDS:-3600}")"
		printf "G_TICK='%s'\n" "$(g_strip0 "${VECTRA_GUARD_TICK_SECONDS:-10}")"
		printf "G_RESTART_DELAY='%s'\n" "$(g_strip0 "${VECTRA_GUARD_RESTART_DELAY_SECONDS:-5}")"
		printf "G_PREPARED_TIMEOUT='%s'\n" "$(g_strip0 "${VECTRA_GUARD_PREPARED_TIMEOUT_SECONDS:-300}")"
		printf "G_ARMED_TIMEOUT='%s'\n" "$(g_strip0 "${VECTRA_GUARD_ARMED_TIMEOUT_SECONDS:-60}")"
		printf "G_RESTORE_RETRY_SECONDS='%s'\n" "$(g_strip0 "${VECTRA_GUARD_RESTORE_RETRY_SECONDS:-$G_RESTORE_RETRY_SECONDS}")"
		printf "G_RESTORE_RETRY_MAX_SECONDS='%s'\n" "$(g_strip0 "${VECTRA_GUARD_RESTORE_RETRY_MAX_SECONDS:-$G_RESTORE_RETRY_MAX_SECONDS}")"
		printf "G_MAX_BOOTS='%s'\n" "$(g_strip0 "${VECTRA_GUARD_MAX_BOOTS:-$G_MAX_BOOTS}")"
		printf "G_RESTORE_MAX_ATTEMPTS='%s'\n" "$(g_strip0 "${VECTRA_GUARD_RESTORE_MAX_ATTEMPTS:-$G_RESTORE_MAX_ATTEMPTS}")"
	} > "$G_META.new" && mv "$G_META.new" "$G_META"
	g_set prepared_at "$(g_mono)"
	g_set phase prepared
	g_cron_write install
	g_boot_hook install
	g_log "rollback copy of $(g_pkg_version vectra-controller-agent) kept in $g_backup (${g_size_kb} KiB, $g_where) before updating to $g_to"
	echo "rollback copy: $g_backup (${g_size_kb} KiB, $g_where)"
	return 0
}

g_restore_status() {
	[ -f "$G_STATUS" ] || return 0
	g_st="$G_STATUS.vectra-guard.$$"
	awk -v a=vectra-controller-agent -v b=luci-app-vectra-controller '
		/^Package: / { skip = ($2 == a || $2 == b) }
		skip { if ($0 == "") skip = 0; next }
		{ print; last = $0 }
		END { if (NR > 0 && last != "") print "" }' "$G_STATUS" > "$g_st" || { rm -f "$g_st"; return 1; }
	[ -s "$G_STANZAS" ] && cat "$G_STANZAS" >> "$g_st"
	mv "$g_st" "$G_STATUS"
}

# $1 full|files, $2 reason. Caller holds the lock.
# $1 reason, $2 attempts
g_mark_rolled_back() {
	{
		printf 'time=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
		printf 'epoch=%s\n' "$(g_now)"
		printf 'reason=%s\n' "$1"
		printf 'failed_version=%s\n' "${G_TO:-}"
		printf 'restored_version=%s\n' "${G_FROM_AGENT:-}"
		printf 'attempts=%s\n' "${2:-1}"
	} > "$G_MARKER.new" 2>/dev/null && mv "$G_MARKER.new" "$G_MARKER"
}

g_agent_runs_executable() {
	[ -s "$G_AGENT_BIN" ] && [ -x "$G_AGENT_BIN" ] && [ -n "$(g_agent_pid)" ]
}

g_sha() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" 2>/dev/null | awk '{ print $1 }'
	else
		shasum -a 256 "$1" 2>/dev/null | awk '{ print $1 }'
	fi
}

# $1 file of relative paths -> "F <sha256> <path>" / "L <target> <path>" lines.
g_write_manifest() {
	while IFS= read -r g_rel || [ -n "$g_rel" ]; do
		g_abs="$G_ROOT/$g_rel"
		if [ -L "$g_abs" ]; then
			printf 'L %s %s\n' "$(readlink "$g_abs")" "$g_rel"
		else
			printf 'F %s %s\n' "$(g_sha "$g_abs")" "$g_rel"
		fi
	done < "$1"
}

# Every file of the copy is on disk exactly as it was copied.
g_disk_matches_copy() {
	[ -s "$G_MANIFEST" ] || return 1
	while read -r g_kind g_val g_rel; do
		g_abs="$G_ROOT/$g_rel"
		case "$g_kind" in
		L) [ -L "$g_abs" ] && [ "$(readlink "$g_abs")" = "$g_val" ] || return 1 ;;
		F) [ -f "$g_abs" ] && [ ! -L "$g_abs" ] && [ "$(g_sha "$g_abs")" = "$g_val" ] || return 1 ;;
		*) return 1 ;;
		esac
	done < "$G_MANIFEST"
	return 0
}

g_proc_exe() {
	printf '/proc/%s/exe' "$1"
}

# Was the running agent process started from the copy? The pid the guard
# started from the verified binary, or a process whose executable (even
# replaced on disk since) is the copy's binary.
g_agent_runs_copy() {
	g_rp="$(g_agent_pid)"
	[ -n "$g_rp" ] || return 1
	[ "$g_rp" = "$(cat "$G_RUN/copy_pid" 2>/dev/null || true)" ] && return 0
	g_want="$(awk -v m="${G_AGENT_BIN#"$G_ROOT"/}" '$1 == "F" && $3 == m { print $2; exit }' "$G_MANIFEST" 2>/dev/null)"
	[ -n "$g_want" ] && [ "$(g_sha "$(g_proc_exe "$g_rp")")" = "$g_want" ]
}

# Start the agent and, when the binary on disk is the copy's, remember its pid
# as started from the copy.
g_start_restored_agent() {
	g_restart_agent
	g_wait_agent_up || { g_restart_agent; g_wait_agent_up; } ||
		g_log "WARN: the restored agent is not running yet; procd and the watchdog keep trying"
	if g_binary_is_copy; then
		g_agent_pid > "$G_RUN/copy_pid" 2>/dev/null || true
	fi
}

# Is the agent on disk the copy's, byte for byte?
g_binary_is_copy() {
	g_member="${G_AGENT_BIN#"$G_ROOT"/}"
	[ -s "$G_AGENT_BIN" ] && [ -x "$G_AGENT_BIN" ] &&
		tar -xzOf "$G_BACKUP" "$g_member" 2>/dev/null | cmp -s - "$G_AGENT_BIN"
}

# End a session the guard cannot finish itself (operator decision). $1 keep:
# the guard script itself stays (a forced update is about to use it).
g_end_hopeless() {
	if [ "${1:-}" = keep ] && [ -f "$G_SELF" ]; then
		mkdir -p "$G_RUN" 2>/dev/null || true
		cp "$G_SELF" "$G_RUN/guard.sh.keep" 2>/dev/null || true
		g_finish
		mkdir -p "$G_DIR" 2>/dev/null && mv "$G_RUN/guard.sh.keep" "$G_SELF" 2>/dev/null || true
	else
		g_finish
	fi
}

# The restore is complete: tar succeeded (checked by the caller), the agent
# binary is the copy's byte for byte, both stanzas carry the old versions.
g_restore_ok() {
	[ -f "$G_DIR/tar_ok" ] || [ -f "$G_RUN/tar_ok" ] || return 1
	if [ -s "$G_MANIFEST" ]; then
		g_disk_matches_copy || return 1
	else
		g_member="${G_AGENT_BIN#"$G_ROOT"/}"
		if tar -tzf "$G_BACKUP" 2>/dev/null | grep -qx "$g_member"; then
			[ -s "$G_AGENT_BIN" ] && [ -x "$G_AGENT_BIN" ] || return 1
			tar -xzOf "$G_BACKUP" "$g_member" 2>/dev/null | cmp -s - "$G_AGENT_BIN" || return 1
		fi
	fi
	[ -f "$G_INFO/vectra-controller-agent.list" ] || return 1
	[ -z "${G_FROM_AGENT:-}" ] || [ "$(g_pkg_version vectra-controller-agent)" = "$G_FROM_AGENT" ] || return 1
	[ -z "${G_FROM_LUCI:-}" ] || [ "$(g_pkg_version luci-app-vectra-controller)" = "$G_FROM_LUCI" ]
}

# Retry an incomplete restore: first see whether it is in fact complete; then
# back off (G_RESTORE_RETRY_SECONDS doubling up to G_RESTORE_RETRY_MAX_SECONDS)
# and give up after G_RESTORE_MAX_ATTEMPTS: phase manual, agent left alone.
g_retry_restore() {
	g_load_meta || return 0
	if [ -f "$G_BACKUP" ] && g_restore_ok; then
		g_log "the incomplete rollback turns out complete (binary = copy, versions in place); ending the session"
		g_mark_rolled_back "$(cat "$G_DIR/rollback_reason" 2>/dev/null || echo 'rollback')" "$(g_epoch_file "$G_DIR/attempts")"
		if [ "$(cat "$G_DIR/rollback_mode" 2>/dev/null || echo full)" = full ]; then
			g_agent_runs_copy || g_start_restored_agent
		else
			[ -n "$(g_agent_pid)" ] || g_start_restored_agent
		fi
		g_finish
		return 0
	fi
	g_attempts="$(g_epoch_file "$G_DIR/attempts")"
	g_attempts="${g_attempts:-0}"
	if [ "$g_attempts" -ge "$G_RESTORE_MAX_ATTEMPTS" ]; then
		if [ -n "$(g_agent_pid)" ]; then
			if [ "$(g_phase)" != manual ]; then
				g_set phase manual
				g_log "CRITICAL: rollback still incomplete after $g_attempts attempts; leaving the running agent alone (phase manual) — the operator ends it: sh $G_SELF clear, or a forced update"
			fi
			return 0
		fi
		# No agent at all: nothing to protect, keep trying, slowly.
		g_set phase manual
		g_delay="$G_RESTORE_RETRY_MAX_SECONDS"
	else
		g_delay="$G_RESTORE_RETRY_SECONDS"
	fi
	g_k=1
	while [ "$g_attempts" -lt "$G_RESTORE_MAX_ATTEMPTS" ] && [ "$g_k" -lt "$g_attempts" ] && [ "$g_delay" -lt "$G_RESTORE_RETRY_MAX_SECONDS" ]; do
		g_delay=$((g_delay * 2))
		g_k=$((g_k + 1))
	done
	[ "$g_delay" -le "$G_RESTORE_RETRY_MAX_SECONDS" ] || g_delay="$G_RESTORE_RETRY_MAX_SECONDS"
	g_last="$(g_epoch_file "$G_RUN/last_restore_try")"
	if [ -n "$g_last" ] && [ $(($1 - g_last)) -ge 0 ] && [ $(($1 - g_last)) -lt "$g_delay" ]; then
		return 0
	fi
	g_rollback "$(cat "$G_DIR/rollback_mode" 2>/dev/null || echo full)" "retrying an incomplete rollback"
}

# $1 full|files, $2 reason. Caller holds the lock. Returns 0 when the old
# version is back, 1 when it is not (the copy and the session are kept).
g_rollback() {
	g_load_meta || return 0
	g_mode="$1"
	g_reason="$2"
	if [ ! -f "$G_BACKUP" ] || ! g_copy_trusted "$G_BACKUP" || ! tar -tzf "$G_BACKUP" >/dev/null 2>&1; then
		g_log "CRITICAL: rollback needed ($g_reason) but the rollback copy $G_BACKUP is gone, unreadable or not owned by root; $G_TO stays"
		g_finish
		return 1
	fi
	mkdir -p "$G_RUN" 2>/dev/null || true
	# A retry keeps the first attempt's reason and mode.
	case "$(g_phase)" in
	restoring | restore-failed) ;;
	*) [ -f "$G_RUN/restoring" ] || rm -f "$G_DIR/rollback_reason" "$G_DIR/rollback_mode" "$G_DIR/attempts" ;;
	esac
	if [ -s "$G_DIR/rollback_reason" ]; then
		g_reason="$(cat "$G_DIR/rollback_reason")"
		g_mode="$(cat "$G_DIR/rollback_mode" 2>/dev/null || printf '%s' "$g_mode")"
	else
		g_set rollback_reason "$g_reason"
		g_set rollback_mode "$g_mode"
	fi
	g_set phase restoring
	: > "$G_RUN/restoring"
	g_mono > "$G_RUN/last_restore_try"
	g_attempt="$(g_epoch_file "$G_DIR/attempts")"
	g_attempt=$((${g_attempt:-0} + 1))
	g_set attempts "$g_attempt"
	g_log "ROLLBACK (attempt $g_attempt): $g_reason; restoring vectra-controller-agent ${G_FROM_AGENT:-?} (the update was to ${G_TO:-?})"
	# An agent that already runs the copy's binary is not stopped again: that
	# part of the restore is done.
	rm -f "$G_DIR/tar_ok" "$G_RUN/tar_ok" 2>/dev/null || true
	g_stopped=0
	if [ "$g_mode" = full ] && ! g_agent_runs_copy; then
		"$G_INIT" stop >/dev/null 2>&1 || true
		g_kill_agent
		g_stopped=1
	fi
	# The new version's file lists, kept from the first attempt: a failed
	# attempt has already replaced the ones in $G_INFO.
	for g_pkg in $G_PKGS; do
		[ -f "$G_DIR/new.$g_pkg.list" ] || [ ! -f "$G_INFO/$g_pkg.list" ] ||
			cp "$G_INFO/$g_pkg.list" "$G_DIR/new.$g_pkg.list" 2>/dev/null || true
	done
	: > "$G_RUN/old.list"
	for g_pkg in $G_PKGS; do
		tar -xzOf "$G_BACKUP" "usr/lib/opkg/info/$g_pkg.list" 2>/dev/null | sed 's/	.*//' >> "$G_RUN/old.list" || true
	done
	# Files only the new version installed go; the old ones come back.
	for g_pkg in $G_PKGS; do
		g_nl="$G_DIR/new.$g_pkg.list"
		[ -f "$g_nl" ] || g_nl="$G_INFO/$g_pkg.list"
		[ -f "$g_nl" ] || continue
		while IFS= read -r g_path || [ -n "$g_path" ]; do
			g_path="${g_path%%	*}"
			case "$g_path" in /*) ;; *) continue ;; esac
			grep -Fxq -- "$g_path" "$G_RUN/old.list" && continue
			[ -d "$G_ROOT$g_path" ] && [ ! -L "$G_ROOT$g_path" ] && continue
			rm -f "$G_ROOT$g_path" 2>/dev/null || true
		done < "$g_nl"
	done
	for g_pkg in $G_PKGS; do
		rm -f "$G_INFO/$g_pkg".* 2>/dev/null || true
	done
	g_ok=1
	if tar -xzf "$G_BACKUP" -C "${G_ROOT:-/}" 2>/dev/null; then
		: > "$G_RUN/tar_ok"
		: > "$G_DIR/tar_ok" 2>/dev/null || true
	else
		g_ok=0
		g_log "CRITICAL: extracting $G_BACKUP failed (full filesystem?)"
	fi
	if ! g_restore_status; then
		g_ok=0
		g_log "CRITICAL: restoring the package status stanzas failed"
	fi
	[ "$g_ok" = 1 ] && ! g_restore_ok && g_ok=0
	rm -f "$G_ROOT"/tmp/luci-indexcache* 2>/dev/null || true
	rm -rf "$G_ROOT/tmp/luci-modulecache" 2>/dev/null || true
	[ -x "$G_ROOT/etc/init.d/rpcd" ] && "$G_ROOT/etc/init.d/rpcd" reload >/dev/null 2>&1
	if [ "$g_ok" != 1 ]; then
		g_set phase restore-failed
		g_log "CRITICAL: ROLLBACK attempt $g_attempt incomplete; the rollback copy $G_BACKUP and the session are kept, retrying every ${G_RESTORE_RETRY_SECONDS}s (cron, watchdog)"
		# Bring up whatever agent is there, if it is the old one intact.
		if { [ "$g_stopped" = 1 ] || [ -z "$(g_agent_pid)" ]; } && g_binary_is_copy; then
			g_start_restored_agent
		fi
		return 1
	fi
	if [ "$g_stopped" = 1 ] || [ -z "$(g_agent_pid)" ] || { [ "$g_mode" = full ] && ! g_agent_runs_copy; }; then
		g_start_restored_agent
	fi
	g_mark_rolled_back "$g_reason" "$g_attempt"
	g_log "ROLLBACK done (attempt $g_attempt): vectra-controller-agent $(g_pkg_version vectra-controller-agent) restored; marker $G_MARKER"
	g_finish
	return 0
}

# Restart the agent on the new version and start the clock.
g_start_window() {
	g_restart_agent
	sleep 2
	mkdir -p "$G_RUN" 2>/dev/null || true
	g_clear_run
	: > "$G_RUN/boot"
	# This boot is the update's own, not one to count.
	: > "$G_RUN/boot_counted"
	g_note_window_stamp
	g_set window "$(g_mono)"
	g_set window_wall "$(g_now)"
	g_set phase watching
	g_log "agent restarted on ${G_TO:-the new version}; watching it (rollback to ${G_FROM_AGENT:-?} if it fails)"
}

# One judgement. Caller holds the lock.
g_tick() {
	g_load_meta || { g_cron_remove; return 0; }
	g_t="$(g_mono)"
	mkdir -p "$G_RUN" 2>/dev/null || true
	case "$(g_phase)" in
	prepared)
		g_ref="$(g_epoch_file "$G_DIR/prepared_at")"
		if [ -z "$g_ref" ] || [ $((g_t - g_ref)) -lt 0 ]; then
			g_set prepared_at "$g_t"
		elif [ $((g_t - g_ref)) -ge "$G_PREPARED_TIMEOUT" ]; then
			g_rollback full "the update command never finished (installation state unknown)"
		fi
		return 0
		;;
	armed)
		g_ref="$(g_epoch_file "$G_DIR/armed_at")"
		if [ -z "$g_ref" ] || [ $((g_t - g_ref)) -lt 0 ]; then
			g_set armed_at "$g_t"
		elif [ $((g_t - g_ref)) -ge "$G_ARMED_TIMEOUT" ]; then
			g_log "the detached guard did not restart the agent; restarting it from cron"
			g_start_window
		fi
		return 0
		;;
	restoring | restore-failed)
		g_retry_restore "$g_t"
		return 0
		;;
	manual)
		if [ -z "$(g_agent_pid)" ]; then
			g_retry_restore "$g_t"
			return 0
		fi
		g_note_once manual "rollback incomplete after $(g_epoch_file "$G_DIR/attempts") attempts; waiting for the operator (sh $G_SELF clear, or a forced update)"
		return 0
		;;
	watching)
		# A restore whose phase could not be written (full disk) is still one.
		if [ -f "$G_RUN/restoring" ]; then
			g_retry_restore "$g_t"
			return 0
		fi
		;;
	*)
		g_log "unknown update-guard phase '$(g_phase)'; ending the session"
		g_finish
		return 0
		;;
	esac
	g_window="$(g_epoch_file "$G_DIR/window")"
	if [ ! -f "$G_RUN/boot" ] || [ -z "$g_window" ] || [ $((g_t - g_window)) -lt 0 ]; then
		g_clear_run
		: > "$G_RUN/boot"
		g_note_window_stamp
		g_set window "$g_t"
		g_set window_wall "$(g_now)"
		g_window="$g_t"
		g_log "watching the agent on ${G_TO:-the new version} again from now (reboot or clock change)"
		if [ ! -f "$G_RUN/boot_counted" ] && [ -n "$g_window" ]; then
			g_count_boot
		fi
		: > "$G_RUN/boot_counted"
		if g_boot_limit; then
			return 0
		fi
		# After a reboot only cron is left: bring back the detached loop, or
		# the judgement would run once a minute.
		g_launch
	fi
	g_age=$((g_t - g_window))
	if [ ! -f "$G_BACKUP" ]; then
		g_log "the rollback copy $G_BACKUP is gone (it was on /tmp and the router rebooted); ${G_TO:-the new version} stays, guard stops"
		g_finish
		return 0
	fi
	if [ ! -s "$G_AGENT_BIN" ] || [ ! -x "$G_AGENT_BIN" ]; then
		g_rollback full "agent binary $G_AGENT_BIN is missing or not executable"
		return 0
	fi
	g_pid="$(g_agent_pid)"
	g_stable=0
	if [ -n "$g_pid" ]; then
		if [ "$g_pid" != "$(cat "$G_RUN/pid" 2>/dev/null || true)" ]; then
			printf '%s\n' "$g_pid" > "$G_RUN/pid"
			printf '%s\n' "$g_t" > "$G_RUN/since"
		fi
		g_since="$(g_epoch_file "$G_RUN/since")"
		[ -n "$g_since" ] || g_since="$g_t"
		if [ $((g_t - g_since)) -ge "$G_STABLE" ]; then
			g_stable=1
			: > "$G_RUN/ever_stable"
		fi
	else
		rm -f "$G_RUN/pid" "$G_RUN/since"
	fi
	if [ "$g_stable" = 1 ]; then
		g_contact="$(g_contact_epoch)"
		g_wwall="$(g_epoch_file "$G_DIR/window_wall")"
		[ -n "$g_wwall" ] || g_wwall=0
		if [ -n "$g_contact" ] && [ "$g_contact" -gt "$g_wwall" ] && [ "$(g_contact_raw)" != "$(cat "$G_RUN/stamp0" 2>/dev/null || true)" ]; then
			g_log "update to ${G_TO:-?} verified: agent stable and in contact with the control plane ${g_age}s after the restart; rollback copy removed"
			g_finish
			return 0
		fi
	fi
	if [ ! -f "$G_RUN/ever_stable" ] && [ "$g_age" -ge "$G_CRASH" ]; then
		g_rollback full "the agent never ran ${G_STABLE}s in a row within ${G_CRASH}s of the restart (crash loop)"
		return 0
	fi
	if [ "$g_age" -ge "$G_PROBE_FROM" ]; then
		g_last="$(g_epoch_file "$G_RUN/last_probe")"
		if [ -z "$g_last" ] || [ $((g_t - g_last)) -ge "$G_PROBE_EVERY" ] || [ $((g_t - g_last)) -lt 0 ]; then
			printf '%s\n' "$g_t" > "$G_RUN/last_probe"
			if g_panel_reachable; then
				[ -f "$G_RUN/reach_since" ] || printf '%s\n' "$g_t" > "$G_RUN/reach_since"
				rm -f "$G_RUN/unreach_since"
			else
				rm -f "$G_RUN/reach_since"
				[ -f "$G_RUN/unreach_since" ] || printf '%s\n' "$g_t" > "$G_RUN/unreach_since"
			fi
		fi
	fi
	if [ "$g_age" -ge "$G_CONTACT" ]; then
		g_rs="$(g_epoch_file "$G_RUN/reach_since")"
		if [ -n "$g_rs" ] && [ $((g_t - g_rs)) -ge "$G_HOLD" ]; then
			g_rollback full "no control-plane contact ${g_age}s after the restart although $(g_control_url) has answered from this router for $((g_t - g_rs))s"
			return 0
		fi
		g_us="$(g_epoch_file "$G_RUN/unreach_since")"
		if [ "$g_age" -ge "$G_NETWORK" ] && [ -n "$g_us" ] && [ $((g_t - g_us)) -ge "$G_HOLD" ]; then
			g_rollback full "no control-plane contact ${g_age}s after the restart and $(g_control_url) does not answer from this router either; the update job reached it before, so the new version may have cut its own path to the panel"
			return 0
		fi
		g_note_once waiting "no control-plane contact yet (${g_age}s) and the panel does not answer from this router; giving the network until ${G_NETWORK}s, then back to ${G_FROM_AGENT:-the previous version}"
	fi
	return 0
}

g_run() {
	mkdir -p "$G_RUN" 2>/dev/null || true
	g_lp="$(cat "$G_RUN/loop.pid" 2>/dev/null || true)"
	if [ -n "$g_lp" ] && [ "$g_lp" != "$$" ] && kill -0 "$g_lp" 2>/dev/null && grep -q 'guard' "/proc/$g_lp/cmdline" 2>/dev/null; then
		return 0
	fi
	printf '%s\n' "$$" > "$G_RUN/loop.pid"
	g_load_meta || return 0
	sleep "$G_RESTART_DELAY"
	if g_lock 60; then
		[ "$(g_phase)" = armed ] && g_start_window
		g_unlock
	fi
	g_started="$(g_mono)"
	while [ -f "$G_META" ]; do
		if g_lock 30; then
			g_tick
			g_unlock
		fi
		[ -f "$G_META" ] || break
		[ $(($(g_mono) - g_started)) -lt $((G_NETWORK + 900)) ] || break
		sleep "$G_TICK"
	done
	rm -f "$G_RUN/loop.pid"
}

g_launch() {
	if command -v setsid >/dev/null 2>&1; then
		setsid sh "$G_SELF" run </dev/null >/dev/null 2>&1 &
	else
		(trap '' HUP; exec sh "$G_SELF" run) </dev/null >/dev/null 2>&1 &
	fi
}

g_main() {
	g_cmd="${1:-tick}"
	[ "$#" -gt 0 ] && shift
	case "$g_cmd" in
	prepare)
		g_prepare "${1:-}"
		;;
	restore-files)
		g_lock 60 || return 1
		g_rollback files "${1:-the installation failed}"
		g_rc=$?
		g_unlock
		return "$g_rc"
		;;
	rollback)
		g_lock 60 || return 1
		g_rollback full "${1:-rollback requested}"
		g_rc=$?
		g_unlock
		return "$g_rc"
		;;
	boot)
		g_load_meta || return 0
		case "$(g_phase)" in
		watching | armed) ;;
		*) return 0 ;;
		esac
		g_lock 30 || return 0
		g_count_boot
		g_boot_limit || true
		g_unlock
		;;
	retry)
		[ -f "$G_META" ] || return 0
		case "$(g_phase)" in restoring | restore-failed) ;; *) return 0 ;; esac
		g_lock 60 || return 1
		g_retry_restore "$(g_mono)"
		g_unlock
		case "$(g_phase)" in restoring | restore-failed | manual) return 1 ;; esac
		return 0
		;;
	clear)
		[ -f "$G_META" ] || { echo "no update-guard session"; return 0; }
		case "$(g_phase)" in
		restoring | restore-failed | manual) ;;
		*) echo "the session is $(g_phase), not a stuck rollback; nothing cleared" >&2; return 1 ;;
		esac
		g_lock 60 || return 1
		# Checked under the lock: the guard's own retry may have just stopped it.
		if ! g_agent_runs_executable; then
			g_unlock
			echo "refusing: the agent is not running from an executable $G_AGENT_BIN; the rollback copy is kept" >&2
			return 1
		fi
		g_log "operator cleared the $(g_phase) rollback session; the agent keeps running as it is"
		g_end_hopeless
		g_unlock
		echo "cleared"
		;;
	arm)
		[ -f "$G_META" ] || return 1
		g_set armed_at "$(g_mono)"
		g_set phase armed
		;;
	launch)
		g_launch
		;;
	run)
		g_run
		;;
	tick)
		g_lock 0 || return 0
		g_tick
		g_unlock
		;;
	*)
		echo "usage: $0 prepare <version>|restore-files <reason>|rollback <reason>|retry|clear|boot|arm|launch|run|tick" >&2
		return 2
		;;
	esac
}

if [ -n "${VECTRA_GUARD_LIB_ONLY:-}" ]; then
	return 0 2>/dev/null || exit 0
fi

g_main "$@"
exit $?
