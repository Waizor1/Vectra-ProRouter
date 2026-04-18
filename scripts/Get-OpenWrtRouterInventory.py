#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

from _vectra_native import (
    CliError,
    argparse_common_router,
    invoke_remote_command,
    print_json,
    resolve_transport_spec,
)


def get_required_value(value: str | None, name: str) -> str:
    if not value or not value.strip():
        raise CliError(f"Missing required value: {name}. Pass it as a parameter or set the corresponding OPENWRT_ROUTER_* environment variable.")
    return value


def remote_inventory_command() -> str:
    return """echo '--- system board ---'
ubus call system board
echo '--- openwrt_release ---'
grep -E 'DISTRIB_(ID|RELEASE|ARCH|TARGET|DESCRIPTION)' /etc/openwrt_release
echo '--- os-release ---'
grep -E 'OPENWRT_ARCH|NAME|VERSION' /usr/lib/os-release 2>/dev/null
echo '--- package manager ---'
opkg --version 2>/dev/null || true
apk --version 2>/dev/null || true
echo '--- architectures ---'
opkg print-architecture 2>/dev/null || true
uname -m
echo '--- installed core packages ---'
opkg list-installed 2>/dev/null | grep -E '^(luci-app-passwall2|passwall2|xray-core|xray|sing-box|hysteria|geoview|v2ray-geoip|v2ray-geosite|dnsmasq|dnsmasq-full|firewall4|nftables|luci|dropbear|openssh)' || true
apk list -I 2>/dev/null | grep -E 'passwall|xray|sing-box|hysteria|geoview|v2ray-geo|dnsmasq|firewall4' || true
echo '--- passwall safe status ---'
uci get passwall2.@global[0].enabled 2>/dev/null || true
uci get passwall2.@global[0].node 2>/dev/null || true
echo -n 'nodes_count='; uci show passwall2 2>/dev/null | grep '=nodes' | wc -l
echo -n 'subscriptions_count='; uci show passwall2 2>/dev/null | grep '=subscribe_list' | wc -l
echo '--- binary versions ---'
xray version 2>/dev/null | head -n 2 || true
sing-box version 2>/dev/null | head -n 2 || true
hysteria version 2>/dev/null | head -n 3 || true
geoview -version 2>/dev/null | head -n 1 || true
echo '--- processes ---'
ps w | grep -E '[p]asswall|[x]ray|[s]ing-box|[h]ysteria' || true
echo '--- firewall dnsmasq ---'
fw4 -V 2>/dev/null || true
dnsmasq -v 2>/dev/null | head -n 5 || true
echo '--- resources ---'
free -m 2>/dev/null || true
df -h /overlay /tmp 2>/dev/null || true
echo '--- upgrade tools ---'
which sysupgrade 2>/dev/null || true
sysupgrade -h 2>&1 | head -n 15 || true
echo '--- backup scope ---'
sysupgrade -l 2>/dev/null | grep -E '^/etc/config/(passwall2|passwall2_server|network|firewall|wireless)$|^/etc/dropbear/|^/etc/config/uhttpd$' || true
echo '--- boot partitions ---'
cat /proc/mtd 2>/dev/null || true
cat /proc/cmdline 2>/dev/null || true
mount | grep -E 'overlay|ubifs|squashfs|tmpfs' || true
echo '--- env tools ---'
which fw_printenv 2>/dev/null || true
fw_printenv 2>/dev/null | grep -E 'boot|flag|rootfs|slot' || true
"""


def get_passwall_plan(inventory_file_path: Path) -> dict | None:
    resolver = Path(__file__).with_name("Resolve-Passwall2RouterPlan.py")
    if not resolver.exists():
        return None
    completed = subprocess.run([sys.executable, str(resolver), "--input-file", str(inventory_file_path), "--as-json"], capture_output=True, text=True, check=False)
    if completed.returncode != 0 or not completed.stdout.strip():
        return None
    return json.loads(completed.stdout)


def main() -> int:
    parser = argparse.ArgumentParser(description="Collect a read-only OpenWrt router inventory over pinned SSH.")
    argparse_common_router(parser)
    parser.add_argument("--output-file")
    parser.add_argument("--include-passwall-plan", action="store_true")
    parser.add_argument("--as-json", action="store_true")
    args = parser.parse_args()

    router_host = get_required_value(args.router_host, "RouterHost")
    router_user = get_required_value(args.router_user, "RouterUser")
    spec = resolve_transport_spec(
        transport=args.transport,
        router_password=args.router_password,
        router_host_key=args.router_host_key,
        openssh_known_hosts_file=args.openssh_known_hosts_file,
        openssh_identity_file=args.openssh_identity_file,
        needs_upload=False,
    )
    if spec.mode == "PuTTY":
        get_required_value(args.router_password, "RouterPassword")
        get_required_value(args.router_host_key, "RouterHostKey")

    response = invoke_remote_command(
        spec=spec,
        router_host=router_host,
        router_user=router_user,
        router_password=args.router_password,
        router_host_key=args.router_host_key,
        command_text=remote_inventory_command(),
        via_stdin_sh=True,
    )
    if response["exitCode"] != 0:
        raise CliError(response["text"] + f"\nRemote inventory command failed with exit code {response['exitCode']}.")

    inventory_text = "\n".join(response["output"]) if response["output"] else response["text"]
    collected_at = __import__("datetime").datetime.now().isoformat(timespec="seconds")

    saved_file = None
    if args.output_file:
        output_path = Path(args.output_file).expanduser()
        output_path.parent.mkdir(parents=True, exist_ok=True)
        output_path.write_text(inventory_text, encoding="utf-8")
        saved_file = str(output_path.resolve())

    plan = None
    temp_file = None
    if args.include_passwall_plan:
        try:
            if saved_file:
                plan = get_passwall_plan(Path(saved_file))
            else:
                handle = tempfile.NamedTemporaryFile("w", suffix=".txt", prefix="openwrt-router-inventory-", delete=False, encoding="utf-8")
                handle.write(inventory_text)
                handle.close()
                temp_file = Path(handle.name)
                plan = get_passwall_plan(temp_file)
        finally:
            if temp_file and temp_file.exists():
                temp_file.unlink()

    result = {
        "collected_at": collected_at,
        "host": router_host,
        "user": router_user,
        "host_key": args.router_host_key if spec.mode == "PuTTY" else None,
        "transport": spec.mode,
        "openssh_known_hosts_file": spec.known_hosts_file if spec.mode == "OpenSSH" else None,
        "inventory_profile": "read-only",
        "output_file": saved_file,
        "raw_text": inventory_text,
        "passwall_plan": plan,
    }

    if args.as_json:
        print_json(result)
        return 0

    print("OpenWrt Router Inventory")
    print("========================")
    print(f"Collected at: {result['collected_at']}")
    print(f"Host: {result['host']}")
    print(f"Inventory mode: {result['inventory_profile']}")
    if saved_file:
        print(f"Saved raw output: {saved_file}")
    print()
    print(inventory_text)
    if plan:
        print()
        print("PassWall2 plan summary:")
        print(f"- Package manager: {plan['detection']['recommended_package_manager']}")
        print(f"- Architecture: {plan['detection']['architecture']}")
        print(f"- App artifact: {plan['recommendation']['app_artifact_name']}")
        print(f"- Component bundle: {plan['recommendation']['component_bundle_name']}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except CliError as exc:
        print(str(exc), file=sys.stderr)
        raise SystemExit(1)
