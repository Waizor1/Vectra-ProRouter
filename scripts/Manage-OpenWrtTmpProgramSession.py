#!/usr/bin/env python3
from __future__ import annotations

import argparse
import datetime as dt
import re
import sys
from pathlib import Path

from _vectra_native import (
    CliError,
    argparse_common_router,
    copy_openwrt_upload,
    convert_marker_output,
    invoke_remote_command,
    print_json,
    resolve_transport_spec,
    sha256_file,
    sh_quote_single,
)


SESSION_ROOT = "/tmp/codex-test"
RESERVED_PORTS = {22, 53, 67, 68, 80, 123, 443, 7681, 1070, 11400}
DANGEROUS_PATTERNS = [
    r"(^|[\s;|&])(opkg|apk)\b",
    r"(^|[\s;|&])uci\b",
    r"(^|[\s;|&])(fw4|iptables|ip6tables|nft)\b",
    r"(^|[\s;|&])(sysupgrade|reboot|halt|poweroff|firstboot|jffs2reset)\b",
    r"(^|[\s;|&])(mtd|ubiformat|ubiupdatevol|fw_setenv)\b",
    r"(^|[\s;|&])(service|/etc/init\.d/)\b",
    r"/etc/config/",
    r"/overlay/",
    r"/usr/bin/",
    r"/usr/sbin/",
    r"/etc/init\.d/",
    r"/etc/uci-defaults/",
]


def required(value: str | None, name: str) -> str:
    if not value or not value.strip():
        raise CliError(f"Missing required value: {name}. Pass it as a parameter or set the corresponding OPENWRT_ROUTER_* environment variable.")
    return value


def safe_session_id(session_id: str | None, require_existing: bool) -> str:
    if session_id:
        if not re.match(r"^[A-Za-z0-9._-]+$", session_id):
            raise CliError("SessionId may contain only letters, digits, dot, underscore, and hyphen.")
        return session_id
    if require_existing:
        raise CliError("SessionId is required for this action.")
    return f"codex-{dt.datetime.now().strftime('%Y%m%d-%H%M%S')}"


def is_reserved_port(value: int) -> bool:
    return value in RESERVED_PORTS or (0 < value < 1024)


def assert_start_safety(args: argparse.Namespace) -> None:
    if not args.local_path:
        raise CliError("LocalPath is required for action=start.")
    local_path = Path(args.local_path).expanduser()
    if not local_path.exists():
        raise CliError(f"LocalPath does not exist: {local_path}")
    if not args.remote_command:
        raise CliError("RemoteCommand is required for action=start.")
    if "\n" in args.remote_command or "\r" in args.remote_command:
        raise CliError("RemoteCommand must be a single line.")
    if args.duration_seconds < 30 or args.duration_seconds > 3600:
        raise CliError("DurationSeconds must stay within 30..3600 to keep tmp tests bounded.")
    if not args.allow_lan_bind and args.listen_address not in {"127.0.0.1", "::1", "localhost"}:
        raise CliError("ListenAddress is restricted to loopback by default. Use --allow-lan-bind only when the test explicitly needs LAN reachability.")
    if args.port is not None and not args.allow_reserved_port and is_reserved_port(args.port):
        raise CliError(f"Port {args.port} is reserved or too privileged for the safe tmp harness.")
    if not args.unsafe_allow_mutating_command:
        for pattern in DANGEROUS_PATTERNS:
            if re.search(pattern, args.remote_command):
                raise CliError(f"RemoteCommand matched a blocked mutating pattern: {pattern}")


def local_artifact_info(path: Path) -> dict:
    return {
        "source_path": str(path.resolve()),
        "source_name": path.name,
        "is_directory": path.is_dir(),
        "local_sha256": None if path.is_dir() else sha256_file(path),
    }


def get_session_dir(session_id: str) -> str:
    return f"{SESSION_ROOT}/{session_id}"


def baseline_command(probe_port: int | None, probe_pattern: str | None) -> str:
    port_snippet = f"""echo '--- port probe ---'\nnetstat -ltnp 2>/dev/null | grep -E ':{probe_port}[[:space:]]' || true""" if probe_port else ""
    pattern_snippet = (
        f"""echo '--- process probe ---'\nps w | grep -F -- {sh_quote_single(probe_pattern)} | grep -v 'grep -F' || true\necho '--- log probe ---'\nlogread -l 80 2>/dev/null | grep -F -- {sh_quote_single(probe_pattern)} || true"""
        if probe_pattern
        else ""
    )
    return f"""echo '--- system board ---'
ubus call system board
echo '--- resources ---'
free -m 2>/dev/null || true
df -h /tmp /overlay 2>/dev/null || true
echo '--- listeners ---'
netstat -ltnp 2>/dev/null | head -n 25 || true
{port_snippet}
{pattern_snippet}
"""


def session_start_command(session_id: str, command_line: str, bind_address: str, bind_port: int | None, lifetime_seconds: int) -> str:
    session_dir = get_session_dir(session_id)
    payload_dir = f"{session_dir}/payload"
    command_file = f"{session_dir}/command.sh"
    pid_file = f"{session_dir}/pid"
    watchdog_pid_file = f"{session_dir}/watchdog.pid"
    log_file = f"{session_dir}/stdout.log"
    session_meta = f"{session_dir}/session.env"
    port_value = str(bind_port) if bind_port else ""
    return f"""set -eu
SESSION_ID={sh_quote_single(session_id)}
SESSION_DIR={sh_quote_single(session_dir)}
PAYLOAD_DIR={sh_quote_single(payload_dir)}
COMMAND_FILE={sh_quote_single(command_file)}
PID_FILE={sh_quote_single(pid_file)}
WATCHDOG_PID_FILE={sh_quote_single(watchdog_pid_file)}
LOG_FILE={sh_quote_single(log_file)}
SESSION_META={sh_quote_single(session_meta)}
mkdir -p "$SESSION_DIR" "$PAYLOAD_DIR"
if [ -f "$PID_FILE" ]; then
  OLD_PID=$(cat "$PID_FILE" 2>/dev/null || true)
  if [ -n "$OLD_PID" ] && kill -0 "$OLD_PID" 2>/dev/null; then
    echo '__ACTION__=start'
    echo '__SESSION_ID__={session_id}'
    echo '__STATE__=already-running'
    echo "__PID__=$OLD_PID"
    exit 1
  fi
fi
printf '%s\n' '#!/bin/sh' {sh_quote_single(f'cd {payload_dir}')} {sh_quote_single(f'exec /bin/sh -c {sh_quote_single(command_line)}')} > "$COMMAND_FILE"
chmod 700 "$COMMAND_FILE"
printf '%s\n' 'session_id={session_id}' 'listen_address={bind_address}' 'port={port_value}' 'duration_seconds={lifetime_seconds}' > "$SESSION_META"
setsid nohup "$COMMAND_FILE" > "$LOG_FILE" 2>&1 < /dev/null &
APP_PID=$!
echo "$APP_PID" > "$PID_FILE"
setsid nohup /bin/sh -c 'sleep {lifetime_seconds}; PID=$(cat {sh_quote_single(pid_file)} 2>/dev/null || true); if [ -n "$PID" ]; then kill "$PID" 2>/dev/null || true; fi' >/dev/null 2>&1 < /dev/null &
WATCHDOG_PID=$!
echo "$WATCHDOG_PID" > "$WATCHDOG_PID_FILE"
sleep 1
RUNNING=0
if kill -0 "$APP_PID" 2>/dev/null; then RUNNING=1; fi
echo '__ACTION__=start'
echo '__SESSION_ID__={session_id}'
echo '__SESSION_DIR__={session_dir}'
echo '__PAYLOAD_DIR__={payload_dir}'
echo "__PID__=$APP_PID"
echo "__WATCHDOG_PID__=$WATCHDOG_PID"
echo "__RUNNING__=$RUNNING"
echo '__LISTEN_ADDRESS__={bind_address}'
echo '__PORT__={port_value}'
echo '__DURATION_SECONDS__={lifetime_seconds}'
tail -n 20 "$LOG_FILE" 2>/dev/null || true
"""


def session_status_command(session_id: str, tail_lines: int) -> str:
    session_dir = get_session_dir(session_id)
    return f"""set -eu
SESSION_DIR={sh_quote_single(session_dir)}
echo '__ACTION__=status'
echo '__SESSION_ID__={session_id}'
if [ ! -d "$SESSION_DIR" ]; then
  echo '__STATE__=missing'
  exit 0
fi
PID=$(cat "$SESSION_DIR/pid" 2>/dev/null || true)
WATCHDOG_PID=$(cat "$SESSION_DIR/watchdog.pid" 2>/dev/null || true)
PORT=$(grep '^port=' "$SESSION_DIR/session.env" 2>/dev/null | cut -d= -f2- || true)
LISTEN_ADDRESS=$(grep '^listen_address=' "$SESSION_DIR/session.env" 2>/dev/null | cut -d= -f2- || true)
DURATION_SECONDS=$(grep '^duration_seconds=' "$SESSION_DIR/session.env" 2>/dev/null | cut -d= -f2- || true)
STATE=stopped
if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then STATE=running; fi
echo "__STATE__=$STATE"
echo '__SESSION_DIR__={session_dir}'
echo "__PID__=$PID"
echo "__WATCHDOG_PID__=$WATCHDOG_PID"
echo "__PORT__=$PORT"
echo "__LISTEN_ADDRESS__=$LISTEN_ADDRESS"
echo "__DURATION_SECONDS__=$DURATION_SECONDS"
echo '--- process ---'
if [ -n "$PID" ]; then ps w | grep -E "^[[:space:]]*$PID " || true; fi
echo '--- port ---'
if [ -n "$PORT" ]; then netstat -ltnp 2>/dev/null | grep -E ":$PORT[[:space:]]" || true; fi
echo '--- log tail ---'
tail -n {tail_lines} "$SESSION_DIR/stdout.log" 2>/dev/null || true
"""


def session_stop_command(session_id: str) -> str:
    session_dir = get_session_dir(session_id)
    return f"""set -eu
SESSION_DIR={sh_quote_single(session_dir)}
echo '__ACTION__=stop'
echo '__SESSION_ID__={session_id}'
if [ ! -d "$SESSION_DIR" ]; then echo '__STATE__=missing'; exit 0; fi
PID=$(cat "$SESSION_DIR/pid" 2>/dev/null || true)
WATCHDOG_PID=$(cat "$SESSION_DIR/watchdog.pid" 2>/dev/null || true)
if [ -n "$PID" ]; then kill "$PID" 2>/dev/null || true; fi
if [ -n "$WATCHDOG_PID" ]; then kill "$WATCHDOG_PID" 2>/dev/null || true; fi
echo '__STATE__=stopped'
echo "__PID__=$PID"
echo "__WATCHDOG_PID__=$WATCHDOG_PID"
"""


def session_cleanup_command(session_id: str) -> str:
    session_dir = get_session_dir(session_id)
    return f"""set -eu
SESSION_DIR={sh_quote_single(session_dir)}
echo '__ACTION__=cleanup'
echo '__SESSION_ID__={session_id}'
case "$SESSION_DIR" in /tmp/codex-test/*) ;; *) echo '__STATE__=refused'; exit 1 ;; esac
if [ ! -d "$SESSION_DIR" ]; then echo '__STATE__=missing'; exit 0; fi
PID=$(cat "$SESSION_DIR/pid" 2>/dev/null || true)
WATCHDOG_PID=$(cat "$SESSION_DIR/watchdog.pid" 2>/dev/null || true)
if [ -n "$PID" ]; then kill "$PID" 2>/dev/null || true; fi
if [ -n "$WATCHDOG_PID" ]; then kill "$WATCHDOG_PID" 2>/dev/null || true; fi
rm -rf "$SESSION_DIR"
echo '__STATE__=cleaned'
"""


def main() -> int:
    parser = argparse.ArgumentParser(description="Manage bounded /tmp program sessions on a live OpenWrt router.")
    parser.add_argument("--action", choices=["baseline", "start", "status", "stop", "cleanup"], required=True)
    argparse_common_router(parser)
    parser.add_argument("--session-id")
    parser.add_argument("--local-path")
    parser.add_argument("--remote-command")
    parser.add_argument("--listen-address", default="127.0.0.1")
    parser.add_argument("--port", type=int)
    parser.add_argument("--duration-seconds", type=int, default=900)
    parser.add_argument("--process-pattern")
    parser.add_argument("--log-lines", type=int, default=40)
    parser.add_argument("--allow-lan-bind", action="store_true")
    parser.add_argument("--allow-reserved-port", action="store_true")
    parser.add_argument("--allow-port-conflict", action="store_true")
    parser.add_argument("--unsafe-allow-mutating-command", action="store_true")
    parser.add_argument("--as-json", action="store_true")
    args = parser.parse_args()

    router_host = required(args.router_host, "RouterHost")
    router_user = required(args.router_user, "RouterUser")
    spec = resolve_transport_spec(
        transport=args.transport,
        router_password=args.router_password,
        router_host_key=args.router_host_key,
        openssh_known_hosts_file=args.openssh_known_hosts_file,
        openssh_identity_file=args.openssh_identity_file,
        needs_upload=args.action == "start",
    )
    if spec.mode == "PuTTY":
        required(args.router_password, "RouterPassword")
        required(args.router_host_key, "RouterHostKey")

    result = {
        "action": args.action,
        "router_host": router_host,
        "transport": spec.mode,
        "inventory_profile": "tmp-test-harness",
        "session_id": None,
        "remote_session_dir": None,
        "listen_address": args.listen_address,
        "port": None,
        "duration_seconds": None,
        "artifact": None,
        "state": None,
        "safety_mode": "guarded",
        "raw_text": None,
    }

    if args.action == "baseline":
        lines = invoke_remote_command(spec=spec, router_host=router_host, router_user=router_user, router_password=args.router_password, router_host_key=args.router_host_key, command_text=baseline_command(args.port, args.process_pattern), via_stdin_sh=True)["output"]
        result["port"] = args.port
        result["raw_text"] = "\n".join(lines)
        result["state"] = "read-only"
    elif args.action == "start":
        assert_start_safety(args)
        session_id = safe_session_id(args.session_id, require_existing=False)
        local_path = Path(args.local_path).expanduser().resolve()
        artifact_info = local_artifact_info(local_path)
        session_dir = get_session_dir(session_id)
        payload_dir = f"{session_dir}/payload"
        preflight_port_snippet = ""
        if args.port and not args.allow_port_conflict:
            preflight_port_snippet = f"""if netstat -ltnp 2>/dev/null | grep -E ':{args.port}[[:space:]]' >/dev/null 2>&1; then
  echo '__ACTION__=preflight'
  echo '__SESSION_ID__={session_id}'
  echo '__STATE__=port-conflict'
  exit 1
fi"""
        preflight_command = f"""set -eu
mkdir -p {sh_quote_single(payload_dir)}
{preflight_port_snippet}
echo '__ACTION__=preflight'
echo '__SESSION_ID__={session_id}'
echo '__STATE__=ok'
"""
        preflight = invoke_remote_command(spec=spec, router_host=router_host, router_user=router_user, router_password=args.router_password, router_host_key=args.router_host_key, command_text=preflight_command, via_stdin_sh=True)
        parsed = convert_marker_output(preflight["output"])
        if parsed["markers"].get("state") != "ok":
            raise CliError(parsed["plain_text"] + "\nPreflight failed.")
        copy_openwrt_upload(spec=spec, router_host=router_host, router_user=router_user, router_password=args.router_password, router_host_key=args.router_host_key, source_path=local_path, target_path=payload_dir)
        start = invoke_remote_command(spec=spec, router_host=router_host, router_user=router_user, router_password=args.router_password, router_host_key=args.router_host_key, command_text=session_start_command(session_id, args.remote_command, args.listen_address, args.port, args.duration_seconds), via_stdin_sh=True)
        start_parsed = convert_marker_output(start["output"])
        artifact_result = {
            "local_path": artifact_info["source_path"],
            "local_name": artifact_info["source_name"],
            "is_directory": artifact_info["is_directory"],
            "local_sha256": artifact_info["local_sha256"],
            "remote_uploaded_entry": f"{payload_dir}/{artifact_info['source_name']}",
        }
        if not artifact_info["is_directory"]:
            remote_hash = invoke_remote_command(spec=spec, router_host=router_host, router_user=router_user, router_password=args.router_password, router_host_key=args.router_host_key, command_text=f"sha256sum {sh_quote_single(artifact_result['remote_uploaded_entry'])} 2>/dev/null | awk '{{print $1}}'", via_stdin_sh=True)
            if remote_hash["output"]:
                artifact_result["remote_sha256"] = remote_hash["output"][0].strip().lower()
        result.update(
            {
                "session_id": session_id,
                "remote_session_dir": session_dir,
                "listen_address": args.listen_address,
                "port": args.port,
                "duration_seconds": args.duration_seconds,
                "artifact": artifact_result,
                "state": "running" if start_parsed["markers"].get("running") == "1" else "failed-to-stay-up",
                "raw_text": start_parsed["plain_text"],
            }
        )
    elif args.action == "status":
        session_id = safe_session_id(args.session_id, require_existing=True)
        status = invoke_remote_command(spec=spec, router_host=router_host, router_user=router_user, router_password=args.router_password, router_host_key=args.router_host_key, command_text=session_status_command(session_id, args.log_lines), via_stdin_sh=True)
        parsed = convert_marker_output(status["output"])
        result.update(
            {
                "session_id": session_id,
                "remote_session_dir": get_session_dir(session_id),
                "state": parsed["markers"].get("state"),
                "listen_address": parsed["markers"].get("listen_address"),
                "port": int(parsed["markers"]["port"]) if parsed["markers"].get("port") else None,
                "duration_seconds": int(parsed["markers"]["duration_seconds"]) if parsed["markers"].get("duration_seconds") else None,
                "raw_text": parsed["plain_text"],
            }
        )
    elif args.action == "stop":
        session_id = safe_session_id(args.session_id, require_existing=True)
        stop = invoke_remote_command(spec=spec, router_host=router_host, router_user=router_user, router_password=args.router_password, router_host_key=args.router_host_key, command_text=session_stop_command(session_id), via_stdin_sh=True)
        parsed = convert_marker_output(stop["output"])
        result.update({"session_id": session_id, "remote_session_dir": get_session_dir(session_id), "state": parsed["markers"].get("state"), "raw_text": parsed["plain_text"]})
    else:
        session_id = safe_session_id(args.session_id, require_existing=True)
        cleanup = invoke_remote_command(spec=spec, router_host=router_host, router_user=router_user, router_password=args.router_password, router_host_key=args.router_host_key, command_text=session_cleanup_command(session_id), via_stdin_sh=True)
        parsed = convert_marker_output(cleanup["output"])
        result.update({"session_id": session_id, "remote_session_dir": get_session_dir(session_id), "state": parsed["markers"].get("state"), "raw_text": parsed["plain_text"]})

    if args.as_json:
        print_json(result)
        return 0
    print("OpenWrt Tmp Program Session")
    print("===========================")
    print(f"Action: {result['action']}")
    print(f"Router: {result['router_host']}")
    print(f"Transport: {result['transport']}")
    if result["session_id"]:
        print(f"Session: {result['session_id']}")
    if result["remote_session_dir"]:
        print(f"Remote session dir: {result['remote_session_dir']}")
    if result["state"]:
        print(f"State: {result['state']}")
    if result["listen_address"] is not None:
        print(f"Listen address: {result['listen_address']}")
    if result["port"] is not None:
        print(f"Port: {result['port']}")
    if result["duration_seconds"] is not None:
        print(f"Duration seconds: {result['duration_seconds']}")
    if result["artifact"]:
        print(f"Artifact: {result['artifact']['local_path']}")
        if result["artifact"].get("local_sha256"):
            print(f"Local SHA256: {result['artifact']['local_sha256']}")
        if result["artifact"].get("remote_sha256"):
            print(f"Remote SHA256: {result['artifact']['remote_sha256']}")
    if result["raw_text"]:
        print()
        print(result["raw_text"])
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except CliError as exc:
        print(str(exc), file=sys.stderr)
        raise SystemExit(1)
