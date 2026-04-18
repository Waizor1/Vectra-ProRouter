#!/usr/bin/env python3
from __future__ import annotations

import argparse
import sys
import tempfile
import zipfile
from pathlib import Path

from _vectra_native import CliError, download_file, print_json, request_json, resolve_ipk_metadata, parse_control_dependencies


REQUIRED_PACKAGES = ["tcping", "xray-core", "geoview", "v2ray-geoip", "v2ray-geosite", "chinadns-ng", "luci-app-passwall2"]
OPTIONAL_PACKAGES = ["sing-box", "hysteria"]
OPENWRT_FEED_DEPS = {"libc", "coreutils", "coreutils-base64", "coreutils-nohup", "curl", "ip-full", "libuci-lua", "lua", "luci-compat", "luci-lib-jsonc", "resolveip", "unzip", "luci-lua-runtime"}
PACKAGE_PATTERNS = {
    "tcping": r"^tcping_.*\.ipk$",
    "xray-core": r"^xray-core_.*\.ipk$",
    "geoview": r"^geoview_.*\.ipk$",
    "v2ray-geoip": r"^v2ray-geoip_.*\.ipk$",
    "v2ray-geosite": r"^v2ray-geosite_.*\.ipk$",
    "chinadns-ng": r"^chinadns-ng_.*\.ipk$",
    "sing-box": r"^sing-box_.*\.ipk$",
    "hysteria": r"^hysteria_.*\.ipk$",
}


def main() -> int:
    parser = argparse.ArgumentParser(description="Mirror the exact upstream PassWall2 bootstrap packages needed by AX3000T workflows.")
    parser.add_argument("--tag", default="26.4.10-1")
    parser.add_argument("--arch", default="aarch64_cortex-a53")
    parser.add_argument("--output-dir", required=True)
    parser.add_argument("--include-optional", action="store_true")
    parser.add_argument("--as-json", action="store_true")
    args = parser.parse_args()

    release = request_json(f"https://api.github.com/repos/Openwrt-Passwall/openwrt-passwall2/releases/tags/{args.tag}")
    assets = list(release.get("assets") or [])
    luci_asset = next((asset for asset in assets if asset.get("name", "").startswith("luci-app-passwall2_") and asset.get("name", "").endswith("_all.ipk")), None)
    if not luci_asset:
        raise CliError(f"luci-app-passwall2 .ipk asset was not found in release {args.tag}")
    bundle_asset_name = f"passwall_packages_ipk_{args.arch}.zip"
    bundle_asset = next((asset for asset in assets if asset.get("name") == bundle_asset_name), None)
    if not bundle_asset:
        raise CliError(f"Asset {bundle_asset_name} was not found in release {args.tag}")

    output_dir = Path(args.output_dir).expanduser().resolve()
    output_dir.mkdir(parents=True, exist_ok=True)

    with tempfile.TemporaryDirectory(prefix="passwall-bootstrap-") as temp_dir_name:
        temp_dir = Path(temp_dir_name)
        luci_path = temp_dir / luci_asset["name"]
        bundle_path = temp_dir / bundle_asset["name"]
        print(f"Fetching upstream release metadata for tag {args.tag}")
        print(f"Downloading upstream luci-app-passwall2: {luci_asset['browser_download_url']}")
        download_file(luci_asset["browser_download_url"], luci_path)
        print(f"Downloading upstream bundle: {bundle_asset['browser_download_url']}")
        download_file(bundle_asset["browser_download_url"], bundle_path)

        luci_meta = resolve_ipk_metadata(luci_path)
        missing_dependencies = [dep for dep in parse_control_dependencies(luci_meta["controlText"]) if dep not in REQUIRED_PACKAGES and dep not in OPENWRT_FEED_DEPS]
        if missing_dependencies:
            raise CliError(f"Uncovered luci-app-passwall2 dependencies: {', '.join(missing_dependencies)}")

        resolved_files: dict[str, str] = {"luci-app-passwall2": luci_asset["name"]}
        resolved_meta: dict[str, dict[str, int | str]] = {"luci-app-passwall2": luci_meta}
        target_luci = output_dir / luci_asset["name"]
        target_luci.write_bytes(luci_path.read_bytes())

        published_optional: list[str] = []
        import re

        with zipfile.ZipFile(bundle_path, "r") as archive:
            for package_name in [pkg for pkg in REQUIRED_PACKAGES if pkg != "luci-app-passwall2"]:
                pattern = re.compile(PACKAGE_PATTERNS[package_name])
                entry = next((name for name in archive.namelist() if pattern.match(Path(name).name)), None)
                if not entry:
                    raise CliError(f"Package {package_name} was not found in the upstream PassWall bundle")
                published_path = output_dir / Path(entry).name
                print(f"Publishing required mirrored package: {Path(entry).name}")
                published_path.write_bytes(archive.read(entry))
                resolved_files[package_name] = Path(entry).name
                resolved_meta[package_name] = resolve_ipk_metadata(published_path)
            if args.include_optional:
                for package_name in OPTIONAL_PACKAGES:
                    pattern = re.compile(PACKAGE_PATTERNS[package_name])
                    entry = next((name for name in archive.namelist() if pattern.match(Path(name).name)), None)
                    if not entry:
                        raise CliError(f"Package {package_name} was not found in the upstream PassWall bundle")
                    published_path = output_dir / Path(entry).name
                    print(f"Publishing optional mirrored package: {Path(entry).name}")
                    published_path.write_bytes(archive.read(entry))
                    resolved_files[package_name] = Path(entry).name
                    resolved_meta[package_name] = resolve_ipk_metadata(published_path)
                    published_optional.append(package_name)

    manifest = {
        "tag": args.tag,
        "arch": args.arch,
        "requiredPackages": [
            {
                "name": package_name,
                "filename": resolved_files[package_name],
                "version": resolved_meta[package_name]["version"],
                "downloadSizeBytes": int(resolved_meta[package_name]["downloadSizeBytes"]),
                "installedSizeBytes": int(resolved_meta[package_name]["installedSizeBytes"]),
            }
            for package_name in REQUIRED_PACKAGES
        ],
        "optionalPackages": [
            {
                "name": package_name,
                "filename": resolved_files[package_name],
                "version": resolved_meta[package_name]["version"],
                "downloadSizeBytes": int(resolved_meta[package_name]["downloadSizeBytes"]),
                "installedSizeBytes": int(resolved_meta[package_name]["installedSizeBytes"]),
            }
            for package_name in published_optional
        ],
        "sourceUrls": {
            "release": release.get("html_url"),
            "luciAppPackage": luci_asset.get("browser_download_url"),
            "packageBundle": bundle_asset.get("browser_download_url"),
        },
    }
    manifest_path = output_dir / "manifest.json"
    manifest_path.write_text(__import__("json").dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    expected_files = [resolved_files[pkg] for pkg in REQUIRED_PACKAGES] + ([resolved_files[pkg] for pkg in published_optional] if args.include_optional else [])
    missing_files = [name for name in expected_files if not (output_dir / name).exists()]
    if missing_files:
        raise CliError(f"Expected files are missing after publish: {', '.join(missing_files)}")

    if args.as_json:
        print_json({"output_dir": str(output_dir), "manifest": manifest})
    else:
        print(f"Done. Bootstrap mirror published to {output_dir}")
        print(f"Manifest: {manifest_path}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except CliError as exc:
        print(str(exc), file=sys.stderr)
        raise SystemExit(1)
