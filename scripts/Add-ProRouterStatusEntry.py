#!/usr/bin/env python3
from __future__ import annotations

import argparse
import datetime as dt
from pathlib import Path

from _vectra_native import REPO_ROOT, write_text


def format_module_reference(value: str, resolved_vault_root: Path) -> str | None:
    if not value.strip():
        return None
    if value.startswith("[[") and value.endswith("]]"):
        return value
    candidate_path = resolved_vault_root / "02 Modules" / f"{value}.md"
    if candidate_path.exists():
        return f"[[02 Modules/{value}]]"
    return f"`{value}`"


def main() -> int:
    parser = argparse.ArgumentParser(description="Append a standardized status entry to today's ProRouter daily note.")
    parser.add_argument("--summary", required=True)
    parser.add_argument("--modules", nargs="*", default=[])
    parser.add_argument("--next-steps", nargs="*", default=[])
    parser.add_argument("--decisions", nargs="*", default=[])
    parser.add_argument("--vault-root", default=str(REPO_ROOT / "ProRouter"))
    parser.add_argument("--now")
    args = parser.parse_args()

    now = dt.datetime.fromisoformat(args.now) if args.now else dt.datetime.now()
    vault_root = Path(args.vault_root).expanduser()
    vault_root.mkdir(parents=True, exist_ok=True)
    daily_dir = vault_root / "04 Sessions" / "Daily"
    daily_dir.mkdir(parents=True, exist_ok=True)

    date_label = now.strftime("%Y-%m-%d")
    time_label = now.strftime("%H:%M")
    daily_path = daily_dir / f"{date_label}.md"
    if not daily_path.exists():
        initial = "\n".join([
            "---",
            "type: session",
            f"date: {date_label}",
            "tags:",
            "  - session",
            "---",
            "",
            f"# Session {date_label}",
            "",
            "## Summary",
            "",
            "-",
            "",
            "## Completion Updates",
            "",
        ])
        write_text(daily_path, initial)
    else:
        content = daily_path.read_text(encoding="utf-8")
        if "## Completion Updates" not in content:
            content = content.rstrip() + "\n\n## Completion Updates\n"
            write_text(daily_path, content + ("\n" if not content.endswith("\n") else ""))

    module_links = [link for item in args.modules if (link := format_module_reference(item, vault_root))]
    entry = ["", f"### {time_label}", "", f"- Summary: {args.summary}"]
    if module_links:
        entry.append(f"- Modules: {', '.join(module_links)}")
    if args.decisions:
        entry.append(f"- Decisions: {'; '.join(args.decisions)}")
    if args.next_steps:
        entry.append(f"- Next: {'; '.join(args.next_steps)}")
    with daily_path.open("a", encoding="utf-8") as handle:
        handle.write("\n".join(entry) + "\n")
    print(f"Updated {daily_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
