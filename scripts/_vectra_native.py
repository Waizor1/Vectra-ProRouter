#!/usr/bin/env python3
from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import io
import json
import os
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import textwrap
import urllib.request
import zipfile
from dataclasses import dataclass
from pathlib import Path
from typing import Any


USER_AGENT = "Vectra-ProRouter-native/1.0"
REPO_ROOT = Path(__file__).resolve().parent.parent
SCRIPTS_DIR = REPO_ROOT / "scripts"


class CliError(RuntimeError):
    pass


def json_dumps(data: Any) -> str:
    return json.dumps(data, ensure_ascii=False, indent=2)


def print_json(data: Any) -> None:
    sys.stdout.write(json_dumps(data))
    sys.stdout.write("\n")


def read_text(path: Path) -> str:
    return path.read_text(encoding="utf-8")


def write_text(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")


def require_path(path: str | None, name: str) -> Path:
    if not path:
        raise CliError(f"Missing required value: {name}.")
    resolved = Path(path).expanduser()
    if not resolved.exists():
        raise CliError(f"Path was not found for {name}: {resolved}")
    return resolved.resolve()


def request_json(url: str) -> Any:
    req = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
    with urllib.request.urlopen(req) as response:
        return json.loads(response.read().decode("utf-8"))


def download_file(url: str, destination: Path) -> None:
    req = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
    with urllib.request.urlopen(req) as response, destination.open("wb") as handle:
        shutil.copyfileobj(response, handle)


def which_any(*candidates: str) -> str | None:
    for candidate in candidates:
        path = shutil.which(candidate)
        if path:
            return path
    return None


def require_command(*candidates: str, error: str) -> str:
    path = which_any(*candidates)
    if not path:
        raise CliError(error)
    return path


def sh_quote_single(value: str | None) -> str:
    if value is None:
        return "''"
    return "'" + value.replace("'", "'\"'\"'") + "'"


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        while True:
            chunk = handle.read(1024 * 1024)
            if not chunk:
                break
            digest.update(chunk)
    return digest.hexdigest()


def run_command(args: list[str], *, input_text: str | None = None, cwd: Path | None = None) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        args,
        input=input_text,
        text=True,
        capture_output=True,
        cwd=str(cwd) if cwd else None,
        check=False,
    )


@dataclass
class TransportSpec:
    mode: str
    ssh_path: str | None = None
    scp_path: str | None = None
    plink_path: str | None = None
    pscp_path: str | None = None
    known_hosts_file: str | None = None
    identity_file: str | None = None


def resolve_transport_spec(
    *,
    transport: str,
    router_password: str | None,
    router_host_key: str | None,
    openssh_known_hosts_file: str | None,
    openssh_identity_file: str | None,
    needs_upload: bool,
) -> TransportSpec:
    transport = transport or "Auto"
    ssh_path = which_any("ssh")
    scp_path = which_any("scp") if needs_upload else None
    plink_path = which_any("plink", "plink.exe")
    pscp_path = which_any("pscp", "pscp.exe") if needs_upload else None

    if openssh_identity_file:
        identity_path = require_path(openssh_identity_file, "OpenSshIdentityFile")
        resolved_identity = str(identity_path)
    else:
        resolved_identity = None

    if transport == "PuTTY":
        selected = "PuTTY"
    elif transport == "OpenSSH":
        selected = "OpenSSH"
    else:
        if router_password:
            selected = "PuTTY"
        elif openssh_known_hosts_file:
            selected = "OpenSSH"
        elif router_host_key:
            selected = "PuTTY"
        else:
            raise CliError(
                "Auto transport could not choose a safe path. Provide RouterPassword or RouterHostKey for PuTTY, or OpenSshKnownHostsFile for OpenSSH."
            )

    if selected == "PuTTY":
        if not router_host_key:
            raise CliError("PuTTY transport requires RouterHostKey / OPENWRT_ROUTER_HOSTKEY.")
        if not plink_path:
            raise CliError("plink was not found. Install PuTTY or use OpenSSH with a pinned known_hosts file.")
        if needs_upload and not pscp_path:
            raise CliError("pscp was not found. Install PuTTY or use OpenSSH with scp.")
        return TransportSpec(mode="PuTTY", plink_path=plink_path, pscp_path=pscp_path)

    if router_password:
        raise CliError(
            "OpenSSH transport in this workspace is key-based only. Omit RouterPassword and use ssh-agent, ~/.ssh/config, or OpenSshIdentityFile."
        )
    known_hosts = require_path(openssh_known_hosts_file, "OpenSshKnownHostsFile")
    if not ssh_path:
        raise CliError("ssh was not found in PATH.")
    if needs_upload and not scp_path:
        raise CliError("scp was not found in PATH.")
    return TransportSpec(
        mode="OpenSSH",
        ssh_path=ssh_path,
        scp_path=scp_path,
        known_hosts_file=str(known_hosts),
        identity_file=resolved_identity,
    )


def _openssh_args(spec: TransportSpec, router_user: str) -> list[str]:
    args = [
        "-o",
        "BatchMode=yes",
        "-o",
        "StrictHostKeyChecking=yes",
        "-o",
        f"UserKnownHostsFile={spec.known_hosts_file}",
        "-o",
        f"User={router_user}",
    ]
    if spec.identity_file:
        args.extend(["-o", "IdentitiesOnly=yes", "-i", spec.identity_file])
    return args


def _openscp_args(spec: TransportSpec, router_user: str) -> list[str]:
    args = ["-B"] + _openssh_args(spec, router_user)
    return args


def invoke_remote_command(
    *,
    spec: TransportSpec,
    router_host: str,
    router_user: str,
    router_password: str | None,
    router_host_key: str | None,
    command_text: str,
    via_stdin_sh: bool,
) -> dict[str, Any]:
    if spec.mode == "PuTTY":
        args = [spec.plink_path, "-ssh", "-batch", "-no-antispoof", "-hostkey", router_host_key or "", "-l", router_user]
        if router_password:
            args.extend(["-pw", router_password])
        args.append(router_host)
        if via_stdin_sh:
            args.append("sh -s")
            completed = run_command(args, input_text=command_text)
        else:
            args.append(command_text)
            completed = run_command(args)
    else:
        args = [spec.ssh_path] + _openssh_args(spec, router_user) + [router_host]
        if via_stdin_sh:
            args.append("sh -s")
            completed = run_command(args, input_text=command_text)
        else:
            args.append(command_text)
            completed = run_command(args)
    text = (completed.stdout + completed.stderr).strip()
    output_lines = [line for line in text.splitlines()] if text else []
    return {"exitCode": completed.returncode, "output": output_lines, "text": text}


def copy_openwrt_upload(
    *,
    spec: TransportSpec,
    router_host: str,
    router_user: str,
    router_password: str | None,
    router_host_key: str | None,
    source_path: Path,
    target_path: str,
) -> None:
    if spec.mode == "PuTTY":
        args = [spec.pscp_path, "-batch", "-scp", "-q", "-hostkey", router_host_key or "", "-l", router_user]
        password_file: Path | None = None
        try:
            if router_password:
                password_file = Path(tempfile.gettempdir()) / f"putty-password-{next(tempfile._get_candidate_names())}.txt"
                password_file.write_text(router_password, encoding="utf-8")
                args.extend(["-pwfile", str(password_file)])
            if source_path.is_dir():
                args.append("-r")
            args.extend([str(source_path), f"{router_host}:{target_path}"])
            completed = run_command(args)
            if completed.returncode != 0:
                raise CliError((completed.stdout + completed.stderr).strip() or "pscp upload failed")
        finally:
            if password_file and password_file.exists():
                password_file.unlink()
        return

    args = [spec.scp_path] + _openscp_args(spec, router_user)
    if source_path.is_dir():
        args.append("-r")
    args.extend([str(source_path), f"{router_user}@{router_host}:{target_path}"])
    completed = run_command(args)
    if completed.returncode != 0:
        raise CliError((completed.stdout + completed.stderr).strip() or "scp upload failed")


def asset_to_dict(asset: dict[str, Any]) -> dict[str, Any]:
    return {
        "name": asset.get("name"),
        "size": asset.get("size"),
        "download_url": asset.get("browser_download_url"),
        "digest": asset.get("digest"),
    }


def first_regex_value(text: str, patterns: list[str]) -> str | None:
    for pattern in patterns:
        match = re.search(pattern, text, re.IGNORECASE | re.MULTILINE | re.DOTALL)
        if match:
            return match.group(1).strip()
    return None


def unique_list(items: list[str | None]) -> list[str]:
    result: list[str] = []
    for item in items:
        if not item:
            continue
        if item not in result:
            result.append(item)
    return result


def detect_package_managers(text: str) -> list[str]:
    detected: list[str] = []
    if re.search(r"(?im)^\s*opkg\s+version\b", text) or re.search(r"(?im)^\s*arch\s+[A-Za-z0-9_.-]+\s+\d+\s*$", text):
        detected.append("opkg")
    if re.search(r"(?im)^\s*apk(?:-tools)?\s+\d", text) or re.search(r"(?im)^\s*installed:\s*apk-tools\b", text):
        detected.append("apk")
    return unique_list(detected)


def primary_opkg_architecture(text: str) -> str | None:
    matches = re.finditer(r"(?im)^\s*arch\s+([A-Za-z0-9_.-]+)\s+(\d+)\s*$", text)
    rows: list[tuple[int, str]] = []
    for match in matches:
        arch_name = match.group(1)
        if arch_name in {"all", "noarch"}:
            continue
        rows.append((int(match.group(2)), arch_name))
    if not rows:
        return None
    rows.sort(reverse=True)
    return rows[0][1]


def resolve_architecture(
    *,
    distrib_arch: str | None,
    openwrt_arch: str | None,
    opkg_primary_arch: str | None,
    uname_machine: str | None,
    target: str | None,
) -> dict[str, Any]:
    if distrib_arch:
        return {"arch": distrib_arch, "source": "DISTRIB_ARCH", "confidence": "high"}
    if openwrt_arch:
        return {"arch": openwrt_arch, "source": "OPENWRT_ARCH", "confidence": "high"}
    if opkg_primary_arch:
        return {"arch": opkg_primary_arch, "source": "opkg print-architecture", "confidence": "medium"}
    if uname_machine == "aarch64" and target and "filogic" in target:
        return {"arch": "aarch64_cortex-a53", "source": "target+uname heuristic", "confidence": "low"}
    if uname_machine:
        return {"arch": uname_machine, "source": "uname -m heuristic", "confidence": "low"}
    return {"arch": None, "source": None, "confidence": "unknown"}


def resolve_openwrt_policy(release_version: str | None, detected_package_managers: list[str]) -> dict[str, Any]:
    manager = None
    package_format = None
    basis = None
    notes: list[str] = []
    match = re.search(r"(?<!\d)(\d{2})\.(\d{1,2})", release_version or "")
    if match:
        major = int(match.group(1))
        minor = int(match.group(2))
        if major < 25 or (major == 25 and minor < 12):
            manager = "opkg"
            package_format = "ipk"
            basis = f"OpenWrt {release_version} policy: prefer opkg/.ipk before 25.12"
        else:
            manager = "apk"
            package_format = "apk"
            basis = f"OpenWrt {release_version} policy: prefer apk/.apk on 25.12+"
    elif len(detected_package_managers) == 1:
        manager = detected_package_managers[0]
        package_format = "apk" if manager == "apk" else "ipk"
        basis = f"Detected package manager output only: {manager}"
    elif "opkg" in detected_package_managers:
        manager = "opkg"
        package_format = "ipk"
        basis = "opkg detected without a parseable OpenWrt release"
    elif "apk" in detected_package_managers:
        manager = "apk"
        package_format = "apk"
        basis = "apk detected without a parseable OpenWrt release"
    else:
        manager = "opkg"
        package_format = "ipk"
        basis = "workspace default fallback: OpenWrt 24.xx path"
        notes.append("Package manager was not detected from pasted output; defaulted to the workspace 24.xx policy.")
    if detected_package_managers and manager not in detected_package_managers:
        notes.append("Detected package manager output conflicts with the release-based policy. Re-check pasted router facts before installing packages.")
    return {"manager": manager, "package_format": package_format, "basis": basis, "notes": notes}


def package_version(text: str, names: list[str]) -> str | None:
    for name in names:
        escaped = re.escape(name)
        patterns = [rf"(?im)^\s*{escaped}\s*-\s*([^\s]+)\s*$", rf"(?im)^\s*{escaped}\s+([^\s]+)\s+"]
        for pattern in patterns:
            match = re.search(pattern, text)
            if match:
                return match.group(1).strip()
    return None


def binary_version(text: str, patterns: list[str]) -> str | None:
    return first_regex_value(text, patterns)


def normalized_package_base_version(version: str | None) -> str | None:
    if not version:
        return None
    return re.sub(r"-r\d+$", "", version).strip()


def read_input_text(input_file: str | None, raw_text: str | None) -> str:
    if input_file:
        return Path(input_file).expanduser().read_text(encoding="utf-8")
    if raw_text:
        return raw_text
    if not sys.stdin.isatty():
        return sys.stdin.read()
    raise CliError("Provide --input-file, --raw-text, or pipe router output into stdin.")


def convert_marker_output(lines: list[str]) -> dict[str, Any]:
    markers: dict[str, str] = {}
    plain_lines: list[str] = []
    for line in lines:
        match = re.match(r"^__([A-Z0-9_]+)__=(.*)$", line)
        if match:
            markers[match.group(1).lower()] = match.group(2)
        else:
            plain_lines.append(line)
    return {
        "markers": markers,
        "plain_text": "\n".join(plain_lines),
        "plain_lines": plain_lines,
    }


def sanitize_command_output(text: str) -> str:
    if not text.strip():
        return ""
    safe_lines = []
    for line in text.splitlines():
        lower = line.lower()
        if "password" in lower or "token" in lower or "authorization" in lower:
            continue
        safe_lines.append(line)
    return "\n".join(safe_lines)


def parse_control_text(control_text: str, field_name: str) -> str:
    pattern = re.compile(rf"^{re.escape(field_name)}:\s*(.+)$", re.MULTILINE)
    match = pattern.search(control_text)
    if not match:
        raise CliError(f"{field_name} line was not found in control metadata")
    return match.group(1).strip()


def expand_ipk_control(ipk_path: Path) -> str:
    with tarfile.open(ipk_path, "r:gz") as outer:
        control_member = next((m for m in outer.getmembers() if Path(m.name).name.startswith("control.tar")), None)
        if not control_member:
            raise CliError(f"control.tar archive was not found in {ipk_path}")
        control_data = outer.extractfile(control_member)
        if control_data is None:
            raise CliError(f"Unable to read control archive from {ipk_path}")
        payload = control_data.read()
    with tempfile.TemporaryDirectory(prefix="vectra-ipk-control-") as temp_dir:
        control_tar = Path(temp_dir) / control_member.name
        control_tar.write_bytes(payload)
        with tarfile.open(control_tar, "r:*") as inner:
            member = next((m for m in inner.getmembers() if Path(m.name).name == "control"), None)
            if not member:
                raise CliError(f"control file was not found after extracting {ipk_path}")
            handle = inner.extractfile(member)
            if handle is None:
                raise CliError(f"Unable to read control file in {ipk_path}")
            return handle.read().decode("utf-8")


def resolve_ipk_metadata(ipk_path: Path) -> dict[str, Any]:
    control_text = expand_ipk_control(ipk_path)
    return {
        "version": parse_control_text(control_text, "Version"),
        "installedSizeBytes": int(parse_control_text(control_text, "Installed-Size")),
        "downloadSizeBytes": ipk_path.stat().st_size,
        "controlText": control_text,
    }


def parse_control_dependencies(control_text: str) -> list[str]:
    depends_line = parse_control_text(control_text, "Depends")
    return [entry.strip() for entry in depends_line.split(",") if entry.strip()]


def parse_simple_kv_lines(lines: list[str]) -> dict[str, str]:
    result: dict[str, str] = {}
    for line in lines:
        if "=" not in line:
            continue
        key, value = line.split("=", 1)
        result[key] = value
    return result


def normalize_openwrt_track(release_value: str | None) -> str | None:
    if not release_value:
        return None
    match = re.match(r"^(\d{2}\.\d{2})", release_value)
    return match.group(1) if match else release_value.strip()


def discover_local_access_file() -> Path | None:
    local_root = REPO_ROOT / "ProRouter" / "98 Local"
    candidates = [
        local_root / "Access Registry.json",
        local_root / "Access Registry.yaml",
        local_root / "Access Registry.yml",
        local_root / "access-registry.json",
        local_root / "access-registry.yaml",
        local_root / "access-registry.yml",
    ]
    for candidate in candidates:
        if candidate.exists():
            return candidate
    return None


def load_local_access_registry(explicit_path: str | None = None) -> dict[str, Any]:
    path = Path(explicit_path).expanduser() if explicit_path else discover_local_access_file()
    if not path or not path.exists():
        raise CliError(
            "Local private registry was not found. Create a gitignored JSON/YAML access registry under ProRouter/98 Local/ or pass explicit router/feed arguments."
        )
    suffix = path.suffix.lower()
    text = path.read_text(encoding="utf-8")
    if suffix == ".json":
        return json.loads(text)
    try:
        import yaml  # type: ignore
    except Exception as exc:  # pragma: no cover
        raise CliError(f"Reading YAML access registry requires PyYAML: {exc}")
    return yaml.safe_load(text)


def argparse_common_router(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--router-host", default=os.environ.get("OPENWRT_ROUTER_HOST"))
    parser.add_argument("--router-user", default=os.environ.get("OPENWRT_ROUTER_USER"))
    parser.add_argument("--router-password", default=os.environ.get("OPENWRT_ROUTER_PASSWORD"))
    parser.add_argument("--router-host-key", default=os.environ.get("OPENWRT_ROUTER_HOSTKEY"))
    parser.add_argument("--transport", choices=["Auto", "PuTTY", "OpenSSH"], default=os.environ.get("OPENWRT_ROUTER_TRANSPORT", "Auto"))
    parser.add_argument("--openssh-known-hosts-file", default=os.environ.get("OPENWRT_ROUTER_KNOWN_HOSTS_FILE"))
    parser.add_argument("--openssh-identity-file", default=os.environ.get("OPENWRT_ROUTER_IDENTITY_FILE"))
