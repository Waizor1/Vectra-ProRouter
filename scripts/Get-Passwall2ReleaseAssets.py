#!/usr/bin/env python3
from __future__ import annotations

import argparse
import sys

from _vectra_native import CliError, asset_to_dict, json_dumps, print_json, request_json


def main() -> int:
    parser = argparse.ArgumentParser(description="Resolve latest PassWall/PassWall2 release assets.")
    parser.add_argument("--app", choices=["passwall2", "passwall"], default="passwall2")
    parser.add_argument("--arch")
    parser.add_argument("--package-manager", choices=["opkg", "apk", "any"], default="any")
    parser.add_argument("--include-all-assets", action="store_true")
    parser.add_argument("--as-json", action="store_true")
    args = parser.parse_args()

    repo_map = {
        "passwall2": "Openwrt-Passwall/openwrt-passwall2",
        "passwall": "Openwrt-Passwall/openwrt-passwall",
    }
    package_extension = {"opkg": ".ipk", "apk": ".apk", "any": None}[args.package_manager]
    package_suffix = {"opkg": "ipk", "apk": "apk", "any": None}[args.package_manager]
    repo = repo_map[args.app]
    release = request_json(f"https://api.github.com/repos/{repo}/releases/latest")
    assets = list(release.get("assets") or [])

    if package_extension:
        matching_packages = [asset for asset in assets if str(asset.get("name", "")).endswith(package_extension)]
    else:
        matching_packages = [asset for asset in assets if str(asset.get("name", "")).endswith((".ipk", ".apk"))]

    matching_arch_assets = [asset for asset in assets if args.arch and args.arch in str(asset.get("name", ""))]

    recommended = []
    if args.app == "passwall2":
        luci_matches = [asset for asset in matching_packages if str(asset.get("name", "")).startswith("luci-app-passwall2")]
        recommended.extend(luci_matches[: 1 if package_extension else 2])
        if args.arch and package_suffix:
            expected_bundle = f"passwall_packages_{package_suffix}_{args.arch}.zip"
            recommended.extend([asset for asset in assets if asset.get("name") == expected_bundle][:1])
    else:
        recommended.extend(matching_packages[: 2 if package_extension else 4])

    result = {
        "app": args.app,
        "repo": repo,
        "tag": release.get("tag_name"),
        "published_at": release.get("published_at"),
        "release_url": release.get("html_url"),
        "package_manager": args.package_manager,
        "arch": args.arch,
        "recommended_assets": [asset_to_dict(asset) for asset in recommended if asset],
        "matching_package_assets": [asset_to_dict(asset) for asset in matching_packages],
        "matching_arch_assets": [asset_to_dict(asset) for asset in matching_arch_assets],
        "all_assets": [asset_to_dict(asset) for asset in assets] if args.include_all_assets else [],
    }

    if args.as_json:
        print_json(result)
        return 0

    print(f"App: {result['app']}")
    print(f"Repo: {result['repo']}")
    print(f"Tag: {result['tag']}")
    print(f"Published: {result['published_at']}")
    print(f"Release: {result['release_url']}")
    if args.arch:
        print(f"Arch: {args.arch}")
    print(f"Package manager: {args.package_manager}")
    print()
    print("Recommended assets:")
    if not result["recommended_assets"]:
        print("  (none matched current filters)")
    else:
        for asset in result["recommended_assets"]:
            print(f"  - {asset['name']}")
            print(f"    {asset['download_url']}")
    if result["matching_arch_assets"]:
        print()
        print("Matching arch assets:")
        for asset in result["matching_arch_assets"]:
            print(f"  - {asset['name']}")
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
