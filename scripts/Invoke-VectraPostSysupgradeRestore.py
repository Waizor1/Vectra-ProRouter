#!/usr/bin/env python3
from __future__ import annotations

import argparse
import sys

from _vectra_native import (
    CliError,
    argparse_common_router,
    invoke_remote_command,
    load_local_access_registry,
    normalize_openwrt_track,
    parse_simple_kv_lines,
    print_json,
    request_json,
    resolve_transport_spec,
    sanitize_command_output,
    sh_quote_single,
)


MANDATORY_PACKAGES = ["vectra-controller-agent", "luci-app-vectra-controller", "luci-app-passwall2", "xray-core", "geoview"]
OPTIONAL_PACKAGES = ["sing-box", "hysteria"]


def required(value: str | None, name: str) -> str:
    if not value or not value.strip():
        raise CliError(f"Missing required value: {name}.")
    return value


def registry_value(obj: dict, key: str):
    return obj.get(key)


def registry_package_version(versions: dict, package_name: str):
    return versions.get(package_name) or versions.get(package_name.replace("-", "_"))


def expected_baseline(registry: dict) -> dict:
    versions = registry_value(registry, "live_versions")
    if not versions:
        raise CliError("live_versions is missing in the local private registry.")
    packages = []
    for package_name in MANDATORY_PACKAGES + OPTIONAL_PACKAGES:
        version = registry_package_version(versions, package_name)
        if not version:
            if package_name in MANDATORY_PACKAGES:
                raise CliError(f"Baseline version for {package_name} is missing in the local private registry.")
            continue
        packages.append(
            {
                "name": package_name,
                "version": version.strip(),
                "pinned": True,
                "source": "vectra-feed" if package_name.startswith("vectra-") or package_name.startswith("luci-app-vectra-") else "router-opkg-feeds",
            }
        )
    router = registry_value(registry, "router")
    if not router:
        raise CliError("router profile is missing in the local private registry.")
    return {
        "board": registry_value(router, "board"),
        "target": registry_value(router, "target"),
        "architecture": registry_value(router, "arch"),
        "openwrtTrack": normalize_openwrt_track(registry_value(router, "openwrt")),
        "layoutFamily": registry_value(router, "layout_family"),
        "packages": packages,
    }


def router_access(registry: dict, args: argparse.Namespace) -> dict:
    router = registry_value(registry, "router") or {}
    return {
        "host": args.router_host or registry_value(router, "host"),
        "user": args.router_user or registry_value(router, "user"),
        "password": args.router_password or registry_value(router, "password"),
        "hostKey": args.router_host_key or registry_value(router, "host_key_sha256"),
    }


def vectra_feed_urls(registry: dict, expected: dict, channel: str) -> dict:
    domains = registry_value(registry, "domains")
    if not domains:
        raise CliError("domains is missing in the local private registry.")
    artifact_base_url = registry_value(domains, "artifacts") or registry_value(domains, "api") or registry_value(domains, "router_api")
    if not artifact_base_url:
        raise CliError("domains.artifacts/router_api is missing in the local private registry.")
    artifact_base_url = artifact_base_url.rstrip("/")
    if artifact_base_url.endswith("/artifacts"):
        feed_base_url = f"{artifact_base_url}/openwrt/{channel.strip()}/{expected['architecture']}"
    else:
        feed_base_url = f"{artifact_base_url}/artifacts/openwrt/{channel.strip()}/{expected['architecture']}"
    return {
        "feedName": "vectra",
        "feedUrl": feed_base_url,
        "feedFile": "/etc/opkg/customfeeds.conf.d/vectra.conf",
        "publicKeyUrl": f"{feed_base_url}/vectra.pub",
        "indexUrl": f"{feed_base_url}/index.json",
        "packagesUrl": f"{feed_base_url}/Packages",
        "signatureUrl": f"{feed_base_url}/Packages.sig",
    }


def remote_feed_index(feed_info: dict, expected: dict) -> dict:
    index = request_json(feed_info["indexUrl"])
    feed_packages = list(index.get("packages") or [])
    for package_name in ["vectra-controller-agent", "luci-app-vectra-controller"]:
        version = next(pkg["version"] for pkg in expected["packages"] if pkg["name"] == package_name)
        expected_file = f"{package_name}_{version}_all.ipk" if package_name == "luci-app-vectra-controller" else f"{package_name}_{version}_{expected['architecture']}.ipk"
        if expected_file not in feed_packages:
            raise CliError(f"Vectra feed does not contain expected package {expected_file}.")
    return {"feedName": index.get("feedName"), "channel": index.get("channel"), "targetArch": index.get("targetArch"), "packages": feed_packages}


def remote_preflight_command() -> str:
    return """set -eu
read_release_value() {
    local key="$1"
    grep -E "^${key}=" /etc/openwrt_release 2>/dev/null | head -n 1 | cut -d= -f2- | tr -d "'" | tr -d '"'
}
echo "BOARD_NAME=$(ubus call system board 2>/dev/null | jsonfilter -e '@.board_name' 2>/dev/null || true)"
echo "TARGET=$(read_release_value DISTRIB_TARGET)"
echo "ARCHITECTURE=$(read_release_value DISTRIB_ARCH)"
echo "OPENWRT_RELEASE=$(read_release_value DISTRIB_RELEASE)"
echo "KERNEL_FIRMWARE_SLOT=$(grep -o 'firmware=[^ ]*' /proc/cmdline 2>/dev/null | head -n 1 | cut -d= -f2 || true)"
echo "FW_BOOTMENU_1=$(fw_printenv -n bootmenu_1 2>/dev/null || true)"
echo "FW_BOOTMENU_2=$(fw_printenv -n bootmenu_2 2>/dev/null || true)"
echo "VECTRA_STATE_DIR=$(if [ -d /etc/vectra-controller ]; then echo present; else echo missing; fi)"
echo "VECTRA_FEED_LINE=$(grep -Rhs '^src/gz[[:space:]]\+vectra[[:space:]]' /etc/opkg/customfeeds.conf /etc/opkg/customfeeds.conf.d/*.conf 2>/dev/null | head -n 1 || true)"
echo "CUSTOM_FEED_DIR=$(if [ -d /etc/opkg/customfeeds.conf.d ]; then echo present; else echo missing; fi)"
"""


def assert_certified_router(expected: dict, facts: dict) -> None:
    if facts.get("BOARD_NAME") != expected["board"]:
        raise CliError(f"Router board mismatch: got '{facts.get('BOARD_NAME')}', expected '{expected['board']}'.")
    if facts.get("TARGET") != expected["target"]:
        raise CliError(f"Router target mismatch: got '{facts.get('TARGET')}', expected '{expected['target']}'.")
    if facts.get("ARCHITECTURE") != expected["architecture"]:
        raise CliError(f"Router architecture mismatch: got '{facts.get('ARCHITECTURE')}', expected '{expected['architecture']}'.")
    remote_track = normalize_openwrt_track(facts.get("OPENWRT_RELEASE"))
    if remote_track != expected["openwrtTrack"]:
        raise CliError(f"Router OpenWrt track mismatch: got '{facts.get('OPENWRT_RELEASE')}', expected '{expected['openwrtTrack']}.x'.")
    has_stock_boot_menu = str(facts.get("FW_BOOTMENU_1", "")).startswith("Startup firmware0") and str(facts.get("FW_BOOTMENU_2", "")).startswith("Startup firmware1")
    if expected["layoutFamily"] == "stock-layout" and not has_stock_boot_menu:
        raise CliError("Router did not prove the expected AX3000T stock-layout boot environment.")
    if facts.get("VECTRA_STATE_DIR") != "present":
        raise CliError("/etc/vectra-controller is missing, so controller identity persistence cannot be trusted for post-sysupgrade restore.")


def feed_ensure_command(feed_info: dict) -> str:
    feed_line = f"src/gz {feed_info['feedName']} {feed_info['feedUrl']}"
    return f"""set -eu
mkdir -p /etc/opkg/customfeeds.conf.d
printf '%s\n' {sh_quote_single(feed_line)} > {sh_quote_single(feed_info['feedFile'])}
wget -qO /tmp/vectra-feed.pub {sh_quote_single(feed_info['publicKeyUrl'])}
opkg-key add /tmp/vectra-feed.pub
rm -f /tmp/vectra-feed.pub
"""


def opkg_availability_command(packages: list[dict]) -> str:
    checks = []
    for package in packages:
        checks.append(
            f"""if ! opkg info {sh_quote_single(package['name'])} 2>/dev/null | grep -F "Version: {package['version']}" >/dev/null; then
    echo "MISSING_PACKAGE={package['name']}@{package['version']}"
    exit 42
fi"""
        )
    return "set -eu\nopkg update\n" + "\n".join(checks)


def installed_baseline_status_command(packages: list[dict]) -> str:
    checks = []
    for package in packages:
        checks.append(f"installed_line=$(opkg list-installed {sh_quote_single(package['name'])} 2>/dev/null | head -n 1 || true)\necho \"INSTALLED_{package['name']}=$installed_line\"")
    return "set -eu\n" + "\n".join(checks)


def opkg_install_command(packages: list[dict]) -> str:
    install_args = " ".join(f"{pkg['name']}={pkg['version']}" for pkg in packages)
    checks = []
    for package in packages:
        checks.append(
            f"""if ! opkg list-installed {package['name']} 2>/dev/null | grep -F "{package['name']} - {package['version']}" >/dev/null; then
    echo "VERIFY_FAILED={package['name']}@{package['version']}"
    exit 43
fi"""
        )
    return f"set -eu\nopkg install --force-reinstall {install_args}\n" + "\n".join(checks)


def main() -> int:
    parser = argparse.ArgumentParser(description="Dry-run or apply the certified post-sysupgrade package restore path for Vectra routers.")
    argparse_common_router(parser)
    parser.add_argument("--feed-channel", default="stable")
    parser.add_argument("--apply", action="store_true")
    parser.add_argument("--as-json", action="store_true")
    parser.add_argument("--local-registry")
    args = parser.parse_args()

    registry = load_local_access_registry(args.local_registry)
    access = router_access(registry, args)
    spec = resolve_transport_spec(
        transport=args.transport,
        router_password=access["password"],
        router_host_key=access["hostKey"],
        openssh_known_hosts_file=args.openssh_known_hosts_file,
        openssh_identity_file=args.openssh_identity_file,
        needs_upload=False,
    )
    if spec.mode == "PuTTY":
        required(access["password"], "RouterPassword")
        required(access["hostKey"], "RouterHostKey")
    access["host"] = required(access["host"], "RouterHost")
    access["user"] = required(access["user"], "RouterUser")

    expected = expected_baseline(registry)
    feed_info = vectra_feed_urls(registry, expected, args.feed_channel)
    feed_index = remote_feed_index(feed_info, expected)

    preflight = invoke_remote_command(spec=spec, router_host=access["host"], router_user=access["user"], router_password=access["password"], router_host_key=access["hostKey"], command_text=remote_preflight_command(), via_stdin_sh=True)
    if preflight["exitCode"] != 0:
        raise CliError(f"Router preflight collection failed with exit code {preflight['exitCode']}: {sanitize_command_output(preflight['text'])}")
    remote_facts = parse_simple_kv_lines(preflight["output"])
    assert_certified_router(expected, remote_facts)

    write_preview = {
        "ensure_feed": "Write /etc/opkg/customfeeds.conf.d/vectra.conf with pinned Vectra feed URL",
        "install_key": "Download and install Vectra opkg signing key",
        "opkg_update": "Refresh package indexes",
        "restore_packages": ", ".join(f"{pkg['name']}={pkg['version']}" for pkg in expected["packages"]),
    }

    installed_state = invoke_remote_command(spec=spec, router_host=access["host"], router_user=access["user"], router_password=access["password"], router_host_key=access["hostKey"], command_text=installed_baseline_status_command(expected["packages"]), via_stdin_sh=True)
    if installed_state["exitCode"] != 0:
        raise CliError(f"Baseline package status collection failed with exit code {installed_state['exitCode']}: {sanitize_command_output(installed_state['text'])}")

    steps = [
        {"name": "preflight", "mode": "read-only", "status": "ok", "output": sanitize_command_output(preflight["text"])},
        {"name": "installed-baseline", "mode": "read-only", "status": "ok", "output": sanitize_command_output(installed_state["text"])},
    ]

    if args.apply:
        feed_write = invoke_remote_command(spec=spec, router_host=access["host"], router_user=access["user"], router_password=access["password"], router_host_key=access["hostKey"], command_text=feed_ensure_command(feed_info), via_stdin_sh=True)
        if feed_write["exitCode"] != 0:
            raise CliError(f"Feed/key restore failed with exit code {feed_write['exitCode']}: {sanitize_command_output(feed_write['text'])}")
        steps.append({"name": "feed-restore", "mode": "write", "status": "ok", "output": sanitize_command_output(feed_write["text"])})

        availability = invoke_remote_command(spec=spec, router_host=access["host"], router_user=access["user"], router_password=access["password"], router_host_key=access["hostKey"], command_text=opkg_availability_command(expected["packages"]), via_stdin_sh=True)
        if availability["exitCode"] != 0:
            raise CliError(f"Baseline package availability check failed with exit code {availability['exitCode']}: {sanitize_command_output(availability['text'])}")
        steps.append({"name": "baseline-availability", "mode": "write", "status": "ok", "output": sanitize_command_output(availability["text"])})

        install = invoke_remote_command(spec=spec, router_host=access["host"], router_user=access["user"], router_password=access["password"], router_host_key=access["hostKey"], command_text=opkg_install_command(expected["packages"]), via_stdin_sh=True)
        if install["exitCode"] != 0:
            raise CliError(f"Package restore failed with exit code {install['exitCode']}: {sanitize_command_output(install['text'])}")
        steps.append({"name": "package-restore", "mode": "write", "status": "ok", "output": sanitize_command_output(install["text"])})

    result = {
        "mode": "apply" if args.apply else "dry-run",
        "router": {
            "host": access["host"],
            "user": access["user"],
            "transport": spec.mode,
            "board": remote_facts.get("BOARD_NAME"),
            "target": remote_facts.get("TARGET"),
            "architecture": remote_facts.get("ARCHITECTURE"),
            "openwrtRelease": remote_facts.get("OPENWRT_RELEASE"),
            "layoutFamily": expected["layoutFamily"],
            "firmwareSlot": remote_facts.get("KERNEL_FIRMWARE_SLOT"),
        },
        "feed": {
            "name": feed_info["feedName"],
            "url": feed_info["feedUrl"],
            "file": feed_info["feedFile"],
            "publicKeyUrl": feed_info["publicKeyUrl"],
            "index": feed_index,
        },
        "baseline": expected["packages"],
        "write_preview": write_preview,
        "steps": steps,
    }

    if args.as_json:
        print_json(result)
        return 0
    print("Vectra Post-Sysupgrade Restore")
    print("==============================")
    print(f"Mode: {result['mode']}")
    print(f"Router: {result['router']['host']} ({result['router']['board']})")
    print(f"OpenWrt: {result['router']['openwrtRelease']} / {result['router']['target']} / {result['router']['architecture']}")
    print(f"Layout: {result['router']['layoutFamily']}")
    print(f"Vectra feed: {result['feed']['url']}")
    print()
    print("Pinned baseline packages:")
    for package in result["baseline"]:
        print(f"- {package['name']} {package['version']} ({package['source']})")
    print()
    if not args.apply:
        print("Dry-run only. Planned write steps:")
        for key, value in result["write_preview"].items():
            print(f"- {key}: {value}")
        print()
        print("Use --apply only during a short LAN-attended maintenance window.")
    else:
        print("Applied steps:")
        for step in [step for step in result["steps"] if step["mode"] == "write"]:
            print(f"- {step['name']}: {step['status']}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except CliError as exc:
        print(str(exc), file=sys.stderr)
        raise SystemExit(1)
