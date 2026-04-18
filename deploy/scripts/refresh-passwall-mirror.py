#!/usr/bin/env python3
from __future__ import annotations

import argparse
import io
import json
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import urllib.request
import zipfile
from pathlib import Path


GITHUB_REPO = "Openwrt-Passwall/openwrt-passwall2"
GITHUB_API_ROOT = f"https://api.github.com/repos/{GITHUB_REPO}/releases"
USER_AGENT = "Vectra-PassWall-Mirror-Refresh/1.0"
DEFAULT_DEPLOY_ROOT = Path("/opt/vectra-prorouter")
DEFAULT_ARCH = "aarch64_cortex-a53"
DEFAULT_SUBPATH = Path("deploy/runtime/artifacts/bootstrap/passwall2")

REQUIRED_PACKAGES = [
    "tcping",
    "xray-core",
    "geoview",
    "v2ray-geoip",
    "v2ray-geosite",
    "chinadns-ng",
    "luci-app-passwall2",
]
OPTIONAL_PACKAGES = ["sing-box", "hysteria"]
OPENWRT_FEED_DEPS = {
    "coreutils",
    "coreutils-base64",
    "coreutils-nohup",
    "curl",
    "ip-full",
    "libc",
    "libuci-lua",
    "lua",
    "luci-compat",
    "luci-lib-jsonc",
    "luci-lua-runtime",
    "resolveip",
    "unzip",
}
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


class RefreshError(RuntimeError):
    pass


def info(message: str) -> None:
    print(message, flush=True)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Refresh the live PassWall bootstrap mirror on the VPS and sync "
            "artifact metadata into PostgreSQL."
        )
    )
    parser.add_argument(
        "--deploy-root",
        default=str(DEFAULT_DEPLOY_ROOT),
        help="Live deploy root that contains deploy/runtime and docker-compose.yml.",
    )
    parser.add_argument(
        "--arch",
        default=DEFAULT_ARCH,
        help="PassWall bundle architecture suffix to mirror.",
    )
    parser.add_argument(
        "--tag",
        help="Override the upstream PassWall release tag. Defaults to releases/latest.",
    )
    parser.add_argument(
        "--skip-metadata-sync",
        action="store_true",
        help="Refresh files only and skip sync-artifact-metadata.mjs.",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Print the plan without downloading or writing anything.",
    )
    return parser.parse_args()


def request_json(url: str) -> dict:
    request = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
    with urllib.request.urlopen(request, timeout=60) as response:
        return json.load(response)


def download_file(url: str, destination: Path) -> None:
    request = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
    with urllib.request.urlopen(request, timeout=120) as response, destination.open("wb") as handle:
        shutil.copyfileobj(response, handle)


def parse_control_fields(control_text: str) -> dict[str, str]:
    fields: dict[str, str] = {}
    current_key: str | None = None
    for raw_line in control_text.splitlines():
        if raw_line.startswith((" ", "\t")) and current_key:
            fields[current_key] += "\n" + raw_line.strip()
            continue
        if ":" not in raw_line:
            continue
        key, value = raw_line.split(":", 1)
        current_key = key.strip()
        fields[current_key] = value.strip()
    return fields


def parse_control_dependencies(control_text: str) -> list[str]:
    fields = parse_control_fields(control_text)
    raw_depends = fields.get("Depends", "")
    dependencies: list[str] = []
    for chunk in raw_depends.split(","):
        candidate = chunk.strip()
        if not candidate:
            continue
        candidate = candidate.split("|", 1)[0].strip()
        candidate = candidate.lstrip("+").strip()
        candidate = re.sub(r"\s*\(.*?\)", "", candidate).strip()
        if candidate:
            dependencies.append(candidate)
    return dependencies


def extract_control_text(ipk_path: Path) -> str:
    listing = subprocess.run(
        ["ar", "t", str(ipk_path)],
        check=True,
        capture_output=True,
        text=True,
    )
    control_member = next(
        (
            line.strip()
            for line in listing.stdout.splitlines()
            if line.strip().startswith("control.tar.")
        ),
        None,
    )
    if not control_member:
        raise RefreshError(f"control.tar.* member was not found in {ipk_path.name}")

    archive_bytes = subprocess.run(
        ["ar", "p", str(ipk_path), control_member],
        check=True,
        capture_output=True,
    ).stdout

    with tarfile.open(fileobj=io.BytesIO(archive_bytes), mode="r:*") as archive:
        member = next(
            (
                item
                for item in archive.getmembers()
                if item.name in {"control", "./control"}
            ),
            None,
        )
        if not member:
            raise RefreshError(f"control file was not found inside {control_member}")
        extracted = archive.extractfile(member)
        if extracted is None:
            raise RefreshError(f"control file could not be extracted from {ipk_path.name}")
        return extracted.read().decode("utf-8")


def resolve_ipk_metadata(ipk_path: Path) -> dict[str, int | str]:
    control_text = extract_control_text(ipk_path)
    fields = parse_control_fields(control_text)
    version = fields.get("Version")
    if not version:
        raise RefreshError(f"Version field is missing in {ipk_path.name}")

    installed_size_kib = fields.get("Installed-Size")
    if not installed_size_kib:
        raise RefreshError(f"Installed-Size field is missing in {ipk_path.name}")

    return {
        "version": version,
        "installedSizeBytes": int(installed_size_kib) * 1024,
        "downloadSizeBytes": ipk_path.stat().st_size,
        "controlText": control_text,
    }


def fetch_release(tag: str | None) -> dict:
    if tag:
        return request_json(f"{GITHUB_API_ROOT}/tags/{tag}")
    return request_json(f"{GITHUB_API_ROOT}/latest")


def current_manifest_is_usable(manifest_path: Path, tag: str, arch: str) -> bool:
    if not manifest_path.is_file():
        return False
    try:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except json.JSONDecodeError:
        return False

    if manifest.get("tag") != tag or manifest.get("arch") != arch:
        return False

    entries = list(manifest.get("requiredPackages") or []) + list(
        manifest.get("optionalPackages") or []
    )
    if not entries:
        return False

    base_dir = manifest_path.parent
    for entry in entries:
        filename = entry.get("filename")
        if not isinstance(filename, str) or not (base_dir / filename).is_file():
            return False
    return True


def build_manifest(
    release: dict,
    arch: str,
    output_dir: Path,
    luci_asset: dict,
    bundle_asset: dict,
    resolved_files: dict[str, str],
    resolved_meta: dict[str, dict[str, int | str]],
    published_optional: list[str],
) -> dict:
    return {
        "tag": release["tag_name"],
        "arch": arch,
        "requiredPackages": [
            {
                "name": package_name,
                "filename": resolved_files[package_name],
                "version": resolved_meta[package_name]["version"],
                "downloadSizeBytes": int(
                    resolved_meta[package_name]["downloadSizeBytes"]
                ),
                "installedSizeBytes": int(
                    resolved_meta[package_name]["installedSizeBytes"]
                ),
            }
            for package_name in REQUIRED_PACKAGES
        ],
        "optionalPackages": [
            {
                "name": package_name,
                "filename": resolved_files[package_name],
                "version": resolved_meta[package_name]["version"],
                "downloadSizeBytes": int(
                    resolved_meta[package_name]["downloadSizeBytes"]
                ),
                "installedSizeBytes": int(
                    resolved_meta[package_name]["installedSizeBytes"]
                ),
            }
            for package_name in published_optional
        ],
        "sourceUrls": {
            "release": release.get("html_url"),
            "luciAppPackage": luci_asset.get("browser_download_url"),
            "packageBundle": bundle_asset.get("browser_download_url"),
        },
    }


def build_passwall_mirror(release: dict, arch: str, output_dir: Path) -> None:
    assets = list(release.get("assets") or [])
    luci_asset = next(
        (
            asset
            for asset in assets
            if str(asset.get("name", "")).startswith("luci-app-passwall2_")
            and str(asset.get("name", "")).endswith("_all.ipk")
        ),
        None,
    )
    if not luci_asset:
        raise RefreshError(
            f"luci-app-passwall2 .ipk asset was not found in release {release.get('tag_name')}"
        )

    bundle_asset_name = f"passwall_packages_ipk_{arch}.zip"
    bundle_asset = next(
        (asset for asset in assets if asset.get("name") == bundle_asset_name),
        None,
    )
    if not bundle_asset:
        raise RefreshError(
            f"Asset {bundle_asset_name} was not found in release {release.get('tag_name')}"
        )

    output_dir.mkdir(parents=True, exist_ok=True)

    with tempfile.TemporaryDirectory(prefix="vectra-passwall-refresh-") as temp_dir_name:
        temp_dir = Path(temp_dir_name)
        luci_path = temp_dir / str(luci_asset["name"])
        bundle_path = temp_dir / str(bundle_asset["name"])

        info(f"Downloading upstream luci-app-passwall2: {luci_asset['browser_download_url']}")
        download_file(str(luci_asset["browser_download_url"]), luci_path)
        info(f"Downloading upstream bundle: {bundle_asset['browser_download_url']}")
        download_file(str(bundle_asset["browser_download_url"]), bundle_path)

        luci_meta = resolve_ipk_metadata(luci_path)
        missing_dependencies = [
            dependency
            for dependency in parse_control_dependencies(str(luci_meta["controlText"]))
            if dependency not in REQUIRED_PACKAGES and dependency not in OPENWRT_FEED_DEPS
        ]
        if missing_dependencies:
            raise RefreshError(
                "Uncovered luci-app-passwall2 dependencies: "
                + ", ".join(missing_dependencies)
            )

        resolved_files: dict[str, str] = {"luci-app-passwall2": str(luci_asset["name"])}
        resolved_meta: dict[str, dict[str, int | str]] = {"luci-app-passwall2": luci_meta}
        shutil.copy2(luci_path, output_dir / str(luci_asset["name"]))

        published_optional: list[str] = []

        with zipfile.ZipFile(bundle_path, "r") as archive:
            for package_name in [pkg for pkg in REQUIRED_PACKAGES if pkg != "luci-app-passwall2"]:
                pattern = re.compile(PACKAGE_PATTERNS[package_name])
                entry = next(
                    (
                        name
                        for name in archive.namelist()
                        if pattern.match(Path(name).name)
                    ),
                    None,
                )
                if not entry:
                    raise RefreshError(
                        f"Package {package_name} was not found in the upstream PassWall bundle"
                    )
                published_path = output_dir / Path(entry).name
                info(f"Publishing required mirrored package: {published_path.name}")
                published_path.write_bytes(archive.read(entry))
                resolved_files[package_name] = published_path.name
                resolved_meta[package_name] = resolve_ipk_metadata(published_path)

            for package_name in OPTIONAL_PACKAGES:
                pattern = re.compile(PACKAGE_PATTERNS[package_name])
                entry = next(
                    (
                        name
                        for name in archive.namelist()
                        if pattern.match(Path(name).name)
                    ),
                    None,
                )
                if not entry:
                    info(
                        "Optional mirrored package is absent in the upstream bundle; "
                        f"skipping: {package_name}"
                    )
                    continue
                published_path = output_dir / Path(entry).name
                info(f"Publishing optional mirrored package: {published_path.name}")
                published_path.write_bytes(archive.read(entry))
                resolved_files[package_name] = published_path.name
                resolved_meta[package_name] = resolve_ipk_metadata(published_path)
                published_optional.append(package_name)

        manifest = build_manifest(
            release=release,
            arch=arch,
            output_dir=output_dir,
            luci_asset=luci_asset,
            bundle_asset=bundle_asset,
            resolved_files=resolved_files,
            resolved_meta=resolved_meta,
            published_optional=published_optional,
        )
        manifest_path = output_dir / "manifest.json"
        manifest_path.write_text(
            json.dumps(manifest, ensure_ascii=False, indent=2) + "\n",
            encoding="utf-8",
        )


def ensure_live_deploy_root(deploy_root: Path) -> None:
    required_paths = [
        deploy_root / ".env",
        deploy_root / "deploy/runtime/postgres/PG_VERSION",
        deploy_root / "deploy/runtime/artifacts",
        deploy_root / "docker-compose.yml",
        deploy_root / "deploy/scripts/sync-runtime-artifacts.sh",
        deploy_root / "apps/web/scripts/sync-artifact-metadata.mjs",
    ]
    missing = [str(path) for path in required_paths if not path.exists()]
    if missing:
        raise RefreshError(
            "Deploy root does not look complete enough for a live refresh. Missing: "
            + ", ".join(missing)
        )


def run_command(command: list[str], cwd: Path) -> None:
    info(f"Running: {' '.join(command)}")
    subprocess.run(command, cwd=str(cwd), check=True)


def sync_mirror_into_runtime(deploy_root: Path, source_dir: Path, tag: str, arch: str) -> None:
    channel = f"bootstrap/passwall2/{tag}/{arch}"
    run_command(
        [
            "bash",
            str(deploy_root / "deploy/scripts/sync-runtime-artifacts.sh"),
            "--source",
            str(source_dir),
            "--channel",
            channel,
            "--target",
            str(deploy_root),
        ],
        cwd=deploy_root,
    )


def sync_passwall_metadata(deploy_root: Path, tag: str, arch: str, dry_run: bool) -> None:
    mirror_dir = f"deploy/runtime/artifacts/bootstrap/passwall2/{tag}/{arch}"
    mode_flag = "--dry-run" if dry_run else "--apply"
    run_command(
        [
            "docker",
            "compose",
            "--env-file",
            ".env",
            "exec",
            "-T",
            "web",
            "node",
            "./apps/web/scripts/sync-artifact-metadata.mjs",
            "--passwall-mirror-dir",
            mirror_dir,
            mode_flag,
        ],
        cwd=deploy_root,
    )


def main() -> int:
    args = parse_args()
    deploy_root = Path(args.deploy_root).expanduser().resolve()
    ensure_live_deploy_root(deploy_root)

    release = fetch_release(args.tag)
    tag = str(release.get("tag_name") or "").strip()
    if not tag:
        raise RefreshError("Upstream PassWall release tag is missing.")

    info(f"Upstream PassWall release: {tag}")

    relative_mirror_dir = DEFAULT_SUBPATH / tag / args.arch
    manifest_path = deploy_root / relative_mirror_dir / "manifest.json"
    mirror_is_current = current_manifest_is_usable(manifest_path, tag, args.arch)

    if args.dry_run:
        info(f"Target mirror dir: {deploy_root / relative_mirror_dir}")
        if mirror_is_current:
            info("Mirror already matches the latest upstream tag; no file refresh is needed.")
        else:
            info("Mirror is stale or incomplete; a refresh would download and publish the latest tag.")
        if not args.skip_metadata_sync:
            info("Metadata sync would be executed after the file refresh step.")
        return 0

    if mirror_is_current:
        info("Mirror already matches the latest upstream tag; skipping file refresh.")
    else:
        with tempfile.TemporaryDirectory(prefix="vectra-passwall-mirror-stage-") as staging_dir_name:
            staging_dir = Path(staging_dir_name) / tag / args.arch
            build_passwall_mirror(release, args.arch, staging_dir)
            sync_mirror_into_runtime(deploy_root, staging_dir, tag, args.arch)

    if args.skip_metadata_sync:
        info("Skipping metadata sync by request.")
        return 0

    sync_passwall_metadata(deploy_root, tag, args.arch, dry_run=False)
    info("PassWall mirror refresh completed successfully.")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except RefreshError as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        raise SystemExit(1)
