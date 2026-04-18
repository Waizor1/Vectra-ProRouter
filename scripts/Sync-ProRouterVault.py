#!/usr/bin/env python3
from __future__ import annotations

import argparse
import datetime as dt
import subprocess
from pathlib import Path

from _vectra_native import CliError, REPO_ROOT, write_text


SKIP_NAMES = {".git", ".obsidian", ".next", "coverage", "dist", "node_modules", "98 Local"}
SKIP_PREFIXES = [
    (REPO_ROOT / "deploy" / "runtime").resolve(),
    (REPO_ROOT / "passwall2" / ".git").resolve(),
    (REPO_ROOT / "openwrt-24.10-src" / ".git").resolve(),
    (REPO_ROOT / "procd-src" / ".git").resolve(),
]
_ignored_cache: dict[str, bool] = {}


def is_git_ignored(path: Path) -> bool:
    try:
        relative = path.resolve().relative_to(REPO_ROOT.resolve())
    except ValueError:
        return False

    cache_key = str(relative)
    cached = _ignored_cache.get(cache_key)
    if cached is not None:
        return cached

    result = subprocess.run(
        ["git", "check-ignore", "-q", cache_key],
        cwd=REPO_ROOT,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        check=False,
    )
    ignored = result.returncode == 0
    _ignored_cache[cache_key] = ignored
    return ignored


def is_skipped(path: Path) -> bool:
    if path.name in SKIP_NAMES:
        return True
    full = path.resolve()
    if any(str(full).startswith(str(prefix)) for prefix in SKIP_PREFIXES):
        return True
    return is_git_ignored(path)


def visible_children(path: Path) -> list[Path]:
    return sorted([child for child in path.iterdir() if not is_skipped(child)], key=lambda child: (not child.is_dir(), child.name.lower()))


def tree_lines(path: Path, prefix: str, current_depth: int, max_depth: int) -> list[str]:
    if current_depth >= max_depth:
        return []
    children = visible_children(path)
    lines: list[str] = []
    for index, child in enumerate(children):
        is_last = index == len(children) - 1
        branch = "\\- " if is_last else "|- "
        name = f"{child.name}/" if child.is_dir() else child.name
        lines.append(f"{prefix}{branch}{name}")
        if child.is_dir():
            next_prefix = f"{prefix}   " if is_last else f"{prefix}|  "
            lines.extend(tree_lines(child, next_prefix, current_depth + 1, max_depth))
    return lines


def main() -> int:
    parser = argparse.ArgumentParser(description="Regenerate ProRouter/00 Dashboard/Repo Map.md")
    parser.add_argument("--vault-root", default=str(REPO_ROOT / "ProRouter"))
    parser.add_argument("--depth", type=int, default=3)
    args = parser.parse_args()

    vault_root = Path(args.vault_root).expanduser()
    vault_root.mkdir(parents=True, exist_ok=True)
    dashboard_dir = vault_root / "00 Dashboard"
    dashboard_dir.mkdir(parents=True, exist_ok=True)
    repo_map_path = dashboard_dir / "Repo Map.md"
    generated_at = dt.datetime.now().astimezone().strftime("%Y-%m-%d %H:%M:%S %z")
    generated_at = f"{generated_at[:-2]}:{generated_at[-2:]}"
    repo_name = REPO_ROOT.name

    module_table = [
        ("Knowledge base and runbooks", "ai_docs/, scripts/, RTK.md", "[[02 Modules/Knowledge Base and Runbooks]]"),
        ("Web control plane", "apps/web", "[[02 Modules/Web Control Plane]]"),
        ("Shared contracts", "packages/contracts", "[[02 Modules/Shared Contracts]]"),
        ("Shared database", "packages/db", "[[02 Modules/Shared Database]]"),
        ("Router agent", "router/vectra-controller-agent", "[[02 Modules/Router Agent]]"),
        ("LuCI controller package", "router/luci-app-vectra-controller", "[[02 Modules/LuCI Controller Package]]"),
        ("Deployment stack", "deploy/", "[[02 Modules/Deployment Stack]]"),
        ("Source mirrors", "passwall2/, openwrt-24.10-src/, procd-src/", "[[02 Modules/Source Mirrors]]"),
    ]

    top_level = visible_children(REPO_ROOT)
    top_level_dirs = sum(1 for child in top_level if child.is_dir())
    top_level_files = sum(1 for child in top_level if child.is_file())
    module_lines = [f"| {name} | `{path}` | {note} |" for name, path, note in module_table]
    content = [
        "---",
        "type: generated",
        f"updated: '{generated_at}'",
        "generated-by: scripts/Sync-ProRouterVault.py",
        "tags:",
        "  - generated",
        "  - structure",
        "---",
        "",
        "# Repo Map",
        "",
        f"Generated from the current workspace root `{repo_name}`.",
        "",
        "## Snapshot",
        "",
        f"- Generated at: `{generated_at}`",
        f"- Top-level directories: `{top_level_dirs}`",
        f"- Top-level files: `{top_level_files}`",
        f"- Tree depth: `{args.depth}`",
        "",
        "## Module Notes",
        "",
        "| Area | Path | Note |",
        "|---|---|---|",
        *module_lines,
        "",
        "## Structure",
        "",
        "```text",
        "./",
        *tree_lines(REPO_ROOT, "", 0, args.depth),
        "```",
    ]
    write_text(repo_map_path, "\n".join(content) + "\n")
    print(f"Updated {repo_map_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
