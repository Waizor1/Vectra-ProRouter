#!/usr/bin/env python3
"""Check upstream PassWall2 releases for new LuCI/UCI parameters.

This is intentionally read-only: it clones upstream into a temporary directory,
compares the latest GitHub release tag with the last reviewed tag, and prints
added/removed option-like lines from PassWall2 model/runtime files. It does not
attempt to decide product support by itself; new findings must still be wired
through the panel with tests and version gates.
"""
from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import urllib.request
from dataclasses import dataclass
from pathlib import Path

DEFAULT_REPO_URL = "https://github.com/Openwrt-Passwall/openwrt-passwall2.git"
DEFAULT_LATEST_API = "https://api.github.com/repos/Openwrt-Passwall/openwrt-passwall2/releases/latest"
LAST_REVIEWED_TAG = "26.5.1-1"
WATCH_PATHS = [
    "luci-app-passwall2/luasrc/model/cbi/passwall2",
    "luci-app-passwall2/root/usr/share/passwall2/0_default_config",
    "luci-app-passwall2/root/usr/share/passwall2",
    "luci-app-passwall2/luasrc/passwall2",
]
OPTION_RE = re.compile(
    r"^[+-].*(?::(?:tab)?option\([^\n]*?[\"']([^\"']+)[\"']|(?:^|[\s.:])value\([\"']([^\"']+)[\"']|\boption\s+([A-Za-z0-9_\-]+))"
)


@dataclass(frozen=True)
class Release:
    tag: str
    url: str
    published_at: str | None


def run(cmd: list[str], cwd: Path | None = None) -> str:
    completed = subprocess.run(
        cmd,
        cwd=cwd,
        check=True,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    return completed.stdout


def fetch_latest_release(api_url: str) -> Release:
    request = urllib.request.Request(api_url, headers={"User-Agent": "Codex"})
    with urllib.request.urlopen(request, timeout=30) as response:
        payload = json.load(response)
    return Release(
        tag=payload["tag_name"],
        url=payload.get("html_url") or f"https://github.com/Openwrt-Passwall/openwrt-passwall2/releases/tag/{payload['tag_name']}",
        published_at=payload.get("published_at"),
    )


def clone_repo(repo_url: str, work_dir: Path) -> Path:
    target = work_dir / "openwrt-passwall2"
    run(["git", "clone", "--quiet", "--filter=blob:none", repo_url, str(target)])
    run(["git", "fetch", "--quiet", "--tags", "--force", "origin"], cwd=target)
    return target


def tag_exists(repo: Path, tag: str) -> bool:
    return bool(run(["git", "tag", "--list", tag], cwd=repo).strip())


def collect_diff(repo: Path, baseline: str, latest: str) -> tuple[list[str], list[str], list[str]]:
    args = ["git", "diff", "--name-status", f"{baseline}..{latest}", "--", *WATCH_PATHS]
    changed_files = [line for line in run(args, cwd=repo).splitlines() if line.strip()]
    diff = run(
        ["git", "diff", "--unified=2", f"{baseline}..{latest}", "--", *WATCH_PATHS],
        cwd=repo,
    )
    added: list[str] = []
    removed: list[str] = []
    current_file = ""
    for line in diff.splitlines():
        if line.startswith("diff --git "):
            parts = line.split(" b/", 1)
            current_file = parts[1] if len(parts) == 2 else ""
            continue
        if not (line.startswith("+") or line.startswith("-")):
            continue
        if line.startswith("+++") or line.startswith("---"):
            continue
        if OPTION_RE.search(line):
            entry = f"{current_file}: {line}"
            if line.startswith("+"):
                added.append(entry)
            else:
                removed.append(entry)
    return changed_files, added, removed


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--baseline-tag", default=LAST_REVIEWED_TAG)
    parser.add_argument("--repo-url", default=DEFAULT_REPO_URL)
    parser.add_argument("--latest-api", default=DEFAULT_LATEST_API)
    parser.add_argument("--work-dir", type=Path, default=None)
    parser.add_argument("--json", action="store_true", help="emit machine-readable JSON")
    parser.add_argument("--fail-on-new", action="store_true", help="exit 2 when latest differs from baseline")
    args = parser.parse_args()

    release = fetch_latest_release(args.latest_api)
    owns_work_dir = args.work_dir is None
    work_dir = args.work_dir or Path(tempfile.mkdtemp(prefix="passwall2-param-check-"))
    work_dir.mkdir(parents=True, exist_ok=True)

    try:
      repo = clone_repo(args.repo_url, work_dir)
      if not tag_exists(repo, args.baseline_tag):
          raise SystemExit(f"baseline tag not found upstream: {args.baseline_tag}")
      if not tag_exists(repo, release.tag):
          raise SystemExit(f"latest tag not found after fetch: {release.tag}")
      if release.tag == args.baseline_tag:
          result = {
              "status": "current",
              "baselineTag": args.baseline_tag,
              "latestTag": release.tag,
              "latestUrl": release.url,
              "publishedAt": release.published_at,
              "changedFiles": [],
              "addedOptionLines": [],
              "removedOptionLines": [],
          }
      else:
          changed, added, removed = collect_diff(repo, args.baseline_tag, release.tag)
          result = {
              "status": "new-release",
              "baselineTag": args.baseline_tag,
              "latestTag": release.tag,
              "latestUrl": release.url,
              "publishedAt": release.published_at,
              "changedFiles": changed,
              "addedOptionLines": added,
              "removedOptionLines": removed,
          }
    finally:
        if owns_work_dir:
            shutil.rmtree(work_dir, ignore_errors=True)

    if args.json:
        print(json.dumps(result, ensure_ascii=False, indent=2))
    else:
        print(f"PassWall2 upstream latest: {result['latestTag']} ({result['latestUrl']})")
        print(f"Last reviewed baseline: {result['baselineTag']}")
        print(f"Status: {result['status']}")
        if result["changedFiles"]:
            print("\nChanged watched files:")
            for line in result["changedFiles"]:
                print(f"  {line}")
        if result["addedOptionLines"]:
            print("\nAdded option/value lines to review:")
            for line in result["addedOptionLines"]:
                print(f"  {line}")
        if result["removedOptionLines"]:
            print("\nRemoved option/value lines to review:")
            for line in result["removedOptionLines"]:
                print(f"  {line}")

    return 2 if args.fail_on_new and result["status"] == "new-release" else 0


if __name__ == "__main__":
    raise SystemExit(main())
