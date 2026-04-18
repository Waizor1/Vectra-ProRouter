#!/usr/bin/env python3
from __future__ import annotations

import argparse
import subprocess
import sys
from pathlib import Path

from _vectra_native import (
    CliError,
    binary_version,
    detect_package_managers,
    first_regex_value,
    json_dumps,
    normalized_package_base_version,
    package_version,
    primary_opkg_architecture,
    print_json,
    read_input_text,
    resolve_architecture,
    resolve_openwrt_policy,
    unique_list,
)


def get_command_examples(package_manager: str, app_artifact_name: str | None, bundle_artifact_name: str | None) -> list[str]:
    commands: list[str] = []
    if package_manager == "opkg":
        commands.append(f"opkg install ./{app_artifact_name}" if app_artifact_name else "opkg install ./luci-app-passwall2_<version>_all.ipk")
        if bundle_artifact_name:
            commands.append(f"# unzip {bundle_artifact_name} and install only the component packages you need with opkg")
    elif package_manager == "apk":
        commands.append(f"apk add ./{app_artifact_name}" if app_artifact_name else "apk add ./luci-app-passwall2_<version>.apk")
        if bundle_artifact_name:
            commands.append(f"# unpack {bundle_artifact_name} and add the selected component packages with apk")
    commands.extend(
        [
            "/etc/init.d/passwall2 restart",
            "lua /usr/share/passwall2/rule_update.lua log geoip,geosite",
            "lua /usr/share/passwall2/subscribe.lua start all",
        ]
    )
    return commands


def main() -> int:
    parser = argparse.ArgumentParser(description="Resolve a safe PassWall2 update plan from pasted router facts.")
    source = parser.add_mutually_exclusive_group()
    source.add_argument("--input-file")
    source.add_argument("--raw-text")
    parser.add_argument("--app", choices=["passwall2"], default="passwall2")
    parser.add_argument("--skip-release-lookup", action="store_true")
    parser.add_argument("--as-json", action="store_true")
    args = parser.parse_args()

    text = read_input_text(args.input_file, args.raw_text)

    model = first_regex_value(text, [r"(?im)^\s*Router model:\s*(.+?)\s*$", r'"model"\s*:\s*"([^"]+)"'])
    board_name = first_regex_value(text, [r"(?im)^\s*board_name\s*:\s*(.+?)\s*$", r'"board_name"\s*:\s*"([^"]+)"'])
    system = first_regex_value(text, [r"(?im)^\s*SoC:\s*(.+?)\s*$", r"(?im)^\s*system\s*:\s*(.+?)\s*$", r'"system"\s*:\s*"([^"]+)"'])
    release_version = first_regex_value(text, [r"(?im)^\s*DISTRIB_RELEASE=['\"]?([^'\"]+)['\"]?\s*$", r'"version"\s*:\s*"((?:\d{2}\.\d{1,2}(?:\.\d+)?)|SNAPSHOT[^"]*)"'])
    release_description = first_regex_value(text, [r"(?im)^\s*DISTRIB_DESCRIPTION=['\"]?([^'\"]+)['\"]?\s*$", r'"description"\s*:\s*"([^"]+)"'])
    target = first_regex_value(text, [r"(?im)^\s*DISTRIB_TARGET=['\"]?([^'\"]+)['\"]?\s*$", r'"target"\s*:\s*"([^"]+)"'])
    distrib_arch = first_regex_value(text, [r"(?im)^\s*DISTRIB_ARCH=['\"]?([^'\"]+)['\"]?\s*$"])
    openwrt_arch = first_regex_value(text, [r"(?im)^\s*OPENWRT_ARCH=['\"]?([^'\"]+)['\"]?\s*$"])
    uname_machine = first_regex_value(text, [r"(?im)^\s*(aarch64)\s*$", r"(?im)^\s*(armv7l)\s*$", r"(?im)^\s*(x86_64)\s*$", r"(?im)^\s*(mipsel_24kc)\s*$"])

    opkg_primary_arch = primary_opkg_architecture(text)
    detected_package_managers = detect_package_managers(text)
    arch_resolution = resolve_architecture(
        distrib_arch=distrib_arch,
        openwrt_arch=openwrt_arch,
        opkg_primary_arch=opkg_primary_arch,
        uname_machine=uname_machine,
        target=target,
    )
    package_policy = resolve_openwrt_policy(release_version, detected_package_managers)

    installed_passwall2 = package_version(text, ["luci-app-passwall2"])
    installed_xray = package_version(text, ["xray-core", "xray"])
    installed_sing_box = package_version(text, ["sing-box"])
    installed_hysteria = package_version(text, ["hysteria", "hysteria2"])
    installed_geoview = package_version(text, ["geoview", "v2ray-geoip", "v2ray-geosite"])

    runtime_xray = binary_version(text, [r"(?im)^Xray\s+([^\s]+)\b"])
    runtime_sing_box = binary_version(text, [r"(?im)^sing-box version\s+([^\s]+)\b", r"(?im)^sing-box\s+([^\s]+)\b"])
    runtime_hysteria = binary_version(text, [r"(?im)^Version:\s*([^\s]+)\s*$", r"(?im)^hysteria(?:\s+version)?\s+([^\s]+)\b"])
    runtime_geoview = binary_version(text, [r"(?im)^Geoview\s+([^\s]+)\b"])

    notes = list(package_policy["notes"])
    if arch_resolution["confidence"] == "low":
        notes.append(f"Architecture was inferred from heuristics ({arch_resolution['source']}); confirm DISTRIB_ARCH before final package installation.")
    if not release_version:
        notes.append("OpenWrt release version was not parsed from the pasted output; package-manager recommendation is less reliable.")

    for name, package_value, runtime_value in [
        ("xray", installed_xray, runtime_xray),
        ("sing-box", installed_sing_box, runtime_sing_box),
        ("hysteria", installed_hysteria, runtime_hysteria),
        ("geoview", installed_geoview, runtime_geoview),
    ]:
        normalized_package = normalized_package_base_version(package_value)
        if normalized_package and runtime_value and normalized_package != runtime_value:
            notes.append(f"Runtime drift detected for {name}: package database says {package_value}, but the binary reports {runtime_value}.")

    if not model and board_name:
        model = board_name

    release_lookup = None
    app_asset = None
    bundle_asset = None
    if not args.skip_release_lookup:
        helper = Path(__file__).with_name("Get-Passwall2ReleaseAssets.py")
        if helper.exists():
            try:
                cmd = [sys.executable, str(helper), "--app", args.app, "--package-manager", package_policy["manager"], "--as-json"]
                if arch_resolution["arch"]:
                    cmd.extend(["--arch", arch_resolution["arch"]])
                completed = subprocess.run(cmd, capture_output=True, text=True, check=False)
                if completed.returncode == 0 and completed.stdout.strip():
                    release_lookup = __import__("json").loads(completed.stdout)
                    app_asset = next((asset for asset in release_lookup["recommended_assets"] if asset["name"].startswith("luci-app-passwall2") and asset["name"].endswith((".ipk", ".apk"))), None)
                    bundle_asset = next((asset for asset in release_lookup["recommended_assets"] if asset["name"].startswith("passwall_packages_")), None)
                else:
                    notes.append(f"Live release lookup failed: {(completed.stderr or completed.stdout).strip()}")
            except Exception as exc:
                notes.append(f"Live release lookup failed: {exc}")
        else:
            notes.append("Release lookup helper script was not found; recommended assets were not resolved.")
    else:
        notes.append("Live release lookup was skipped on request.")

    app_asset_name = app_asset["name"] if app_asset else None
    app_asset_url = app_asset["download_url"] if app_asset else None
    bundle_asset_name = bundle_asset["name"] if bundle_asset else None
    bundle_asset_url = bundle_asset["download_url"] if bundle_asset else None

    if package_policy["manager"] == "opkg":
        component_strategy = "Prefer package-based component updates; use the built-in binary updater only as a fallback/manual override."
    elif package_policy["manager"] == "apk":
        component_strategy = "Prefer package-manager transactions for components; confirm router-side apk workflow before applying."
    else:
        component_strategy = "Do not use the built-in updater as the default path until the router package manager is confirmed."

    checklist = [
        "Back up /etc/config/passwall2 and /etc/config/passwall2_server before touching packages or binaries.",
        "Capture current installed package versions and binary versions before the change window.",
        f"Use {package_policy['manager']} and .{package_policy['package_format']} artifacts for the main PassWall2 application update.",
        f"Install the app package that matches the current release: {app_asset_name}." if app_asset_name else "Resolve the exact luci-app-passwall2 package for the router before installation.",
        f"Extract {bundle_asset_name} and install only the component packages required on this router." if bundle_asset_name else "Resolve the matching component bundle for this architecture before updating xray/sing-box/hysteria/geodata packages.",
        "Restart PassWall2 after package changes and verify service health before any subscription or rule refresh.",
        "Keep application update, component update, geo rules refresh, and subscription refresh as separate maintenance actions.",
    ]

    result = {
        "app": args.app,
        "router": {
            "model": model,
            "board_name": board_name,
            "system": system,
            "target": target,
            "openwrt_release": release_version,
            "openwrt_description": release_description,
        },
        "detection": {
            "package_managers_detected": detected_package_managers,
            "recommended_package_manager": package_policy["manager"],
            "recommended_package_format": package_policy["package_format"],
            "package_manager_basis": package_policy["basis"],
            "architecture": arch_resolution["arch"],
            "architecture_source": arch_resolution["source"],
            "architecture_confidence": arch_resolution["confidence"],
            "candidates": {
                "distrib_arch": distrib_arch,
                "openwrt_arch": openwrt_arch,
                "opkg_primary_arch": opkg_primary_arch,
                "uname_machine": uname_machine,
            },
        },
        "installed": {
            "package_versions": {
                "passwall2": installed_passwall2,
                "xray": installed_xray,
                "sing_box": installed_sing_box,
                "hysteria": installed_hysteria,
                "geoview_or_geodata": installed_geoview,
            },
            "runtime_versions": {
                "xray": runtime_xray,
                "sing_box": runtime_sing_box,
                "hysteria": runtime_hysteria,
                "geoview": runtime_geoview,
            },
        },
        "recommendation": {
            "app_artifact_name": app_asset_name,
            "app_artifact_url": app_asset_url,
            "component_bundle_name": bundle_asset_name,
            "component_bundle_url": bundle_asset_url,
            "component_update_strategy": component_strategy,
            "built_in_component_updater": "fallback-only",
        },
        "commands": get_command_examples(package_policy["manager"], app_asset_name, bundle_asset_name),
        "checklist": checklist,
        "notes": unique_list(notes),
        "release_lookup": {
            "tag": release_lookup["tag"],
            "published_at": release_lookup["published_at"],
            "release_url": release_lookup["release_url"],
        }
        if release_lookup
        else None,
    }

    if args.as_json:
        print_json(result)
        return 0

    print("PassWall2 Router Plan")
    print("====================")
    print(f"Model: {result['router']['model'] or '(not parsed)'}")
    print(f"Board: {result['router']['board_name'] or '(not parsed)'}")
    print(f"Target: {result['router']['target'] or '(not parsed)'}")
    print(f"OpenWrt: {result['router']['openwrt_release'] or '(not parsed)'}")
    print(f"Package manager: {result['detection']['recommended_package_manager']}")
    print(f"Package format: .{result['detection']['recommended_package_format']}")
    print(f"Package manager basis: {result['detection']['package_manager_basis']}")
    print(f"Architecture: {result['detection']['architecture'] or '(not parsed)'}")
    print(f"Architecture source: {result['detection']['architecture_source'] or '(not parsed)'}")
    print()
    print("Recommended assets:")
    print(f"- App package: {app_asset_name or '(not resolved)'}")
    if app_asset_url:
        print(f"  {app_asset_url}")
    print(f"- Component bundle: {bundle_asset_name or '(not resolved)'}")
    if bundle_asset_url:
        print(f"  {bundle_asset_url}")
    print()
    print("Installed package versions detected:")
    print(f"- PassWall2: {installed_passwall2 or '(not parsed)'}")
    print(f"- xray: {installed_xray or '(not parsed)'}")
    print(f"- sing-box: {installed_sing_box or '(not parsed)'}")
    print(f"- hysteria: {installed_hysteria or '(not parsed)'}")
    print(f"- geoview/geodata: {installed_geoview or '(not parsed)'}")
    print()
    print("Runtime binary versions detected:")
    print(f"- xray: {runtime_xray or '(not parsed)'}")
    print(f"- sing-box: {runtime_sing_box or '(not parsed)'}")
    print(f"- hysteria: {runtime_hysteria or '(not parsed)'}")
    print(f"- geoview: {runtime_geoview or '(not parsed)'}")
    print()
    print("Safe path checklist:")
    for item in checklist:
        print(f"- {item}")
    print()
    print("Useful commands:")
    for command in result["commands"]:
        print(f"- {command}")
    if result["notes"]:
        print()
        print("Notes:")
        for note in result["notes"]:
            print(f"- {note}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except CliError as exc:
        print(str(exc), file=sys.stderr)
        raise SystemExit(1)
    except Exception as exc:
        print(str(exc), file=sys.stderr)
        raise SystemExit(1)
