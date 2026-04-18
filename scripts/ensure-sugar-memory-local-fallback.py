#!/usr/bin/env python3
"""Reapply or verify the local Sugar Memory FTS fallback patch.

This script manages the user-local Sugar runtime outside git. It keeps the
current workaround reproducible after pipx reinstalls or upgrades overwrite the
patched site-packages file.
"""

from __future__ import annotations

import argparse
import os
import shutil
import subprocess
import sys
from pathlib import Path


PATCH_MARKERS = (
    "def _disable_semantic_search(self, reason: str):",
    "def _probe_vector_search_support(self, conn: sqlite3.Connection) -> bool:",
    "Vector search failed, using FTS5 fallback: {e}",
)


def resolve_sugar_bin(explicit: str | None) -> Path:
    candidates = []
    if explicit:
        candidates.append(Path(explicit).expanduser())
    env_bin = os.environ.get("SUGAR_BIN")
    if env_bin:
        candidates.append(Path(env_bin).expanduser())
    which_bin = shutil.which("sugar")
    if which_bin:
        candidates.append(Path(which_bin))
    candidates.append(Path.home() / ".local/bin/sugar")

    for candidate in candidates:
        if candidate.exists():
            return candidate

    raise SystemExit(
        "Could not find the Sugar executable. Pass --sugar-bin or set SUGAR_BIN."
    )


def resolve_store_path(sugar_bin: Path) -> Path:
    command = [
        str(sugar_bin),
        "--config",
        "/dev/null",
        "debug",
    ]
    # Use the launcher shebang's Python instead of `sugar debug`; it avoids noisy output.
    launcher = sugar_bin.read_text(encoding="utf-8").splitlines()[0]
    if not launcher.startswith("#!"):
        raise SystemExit(f"Unexpected Sugar launcher format: {sugar_bin}")
    python_bin = launcher[2:].strip()
    output = subprocess.check_output(
        [
            python_bin,
            "-c",
            "import sugar.memory.store as m; print(m.__file__)",
        ],
        text=True,
    ).strip()
    store_path = Path(output)
    if not store_path.exists():
        raise SystemExit(f"Resolved Sugar store.py does not exist: {store_path}")
    return store_path


def is_patched(content: str) -> bool:
    return all(marker in content for marker in PATCH_MARKERS)


def apply_patch(content: str) -> str:
    updated = content

    if "import struct\n" not in updated:
        updated = updated.replace("import sqlite3\n", "import sqlite3\nimport struct\n", 1)

    original_check = """    def _check_sqlite_vec(self) -> bool:\n        \"\"\"Check if sqlite-vec extension is available.\"\"\"\n        try:\n            import sqlite_vec  # noqa: F401\n\n            return True\n        except ImportError:\n            logger.info(\"sqlite-vec not available, using FTS5 fallback\")\n            return False\n"""

    replacement_check = """    def _check_sqlite_vec(self) -> bool:\n        \"\"\"Check if sqlite-vec extension is available.\"\"\"\n        try:\n            import sqlite_vec  # noqa: F401\n\n            return True\n        except ImportError:\n            logger.info(\"sqlite-vec not available, using FTS5 fallback\")\n            return False\n\n    def _disable_semantic_search(self, reason: str):\n        \"\"\"Disable semantic search and fall back to keyword search.\"\"\"\n        logger.warning(reason)\n        self._has_vec = False\n        self.embedder = FallbackEmbedder()\n\n    def _probe_vector_search_support(self, conn: sqlite3.Connection) -> bool:\n        \"\"\"Verify vector search syntax works in the active SQLite runtime.\"\"\"\n        probe_table = \"temp.sugar_vec_probe\"\n        probe_embedding = struct.pack(\"2f\", 0.0, 0.0)\n\n        try:\n            cursor = conn.cursor()\n            cursor.execute(\n                f\"\"\"\n                CREATE VIRTUAL TABLE {probe_table} USING vec0(\n                    embedding float[2]\n                )\n                \"\"\"\n            )\n            cursor.execute(\n                f\"INSERT INTO {probe_table}(rowid, embedding) VALUES (?, ?)\",\n                (1, probe_embedding),\n            )\n            cursor.execute(\n                f\"SELECT rowid FROM {probe_table} ORDER BY embedding <-> ? LIMIT 1\",\n                (probe_embedding,),\n            )\n            cursor.fetchone()\n            return True\n        except Exception as e:\n            self._disable_semantic_search(\n                f\"Vector search probe failed, using FTS5 fallback: {e}\"\n            )\n            return False\n        finally:\n            try:\n                conn.execute(f\"DROP TABLE IF EXISTS {probe_table}\")\n            except Exception:\n                pass\n"""

    if original_check not in updated:
        raise SystemExit("Could not find _check_sqlite_vec() block to patch.")
    updated = updated.replace(original_check, replacement_check, 1)

    load_snippet = """                    self._conn.enable_load_extension(True)\n                    sqlite_vec.load(self._conn)\n                    self._conn.enable_load_extension(False)\n                except Exception as e:\n                    logger.warning(f\"Failed to load sqlite-vec: {e}\")\n                    self._has_vec = False\n"""

    load_replacement = """                    self._conn.enable_load_extension(True)\n                    sqlite_vec.load(self._conn)\n                    self._conn.enable_load_extension(False)\n                    if not self._probe_vector_search_support(self._conn):\n                        self._conn.enable_load_extension(False)\n                except Exception as e:\n                    self._disable_semantic_search(f\"Failed to load sqlite-vec: {e}\")\n"""

    if load_snippet not in updated:
        raise SystemExit("Could not find sqlite-vec load block to patch.")
    updated = updated.replace(load_snippet, load_replacement, 1)

    vector_warning = '            logger.warning(f"Vector search failed: {e}")\n'
    vector_replacement = (
        '            self._disable_semantic_search('
        'f"Vector search failed, using FTS5 fallback: {e}")\n'
    )
    if vector_warning not in updated:
        raise SystemExit("Could not find vector-search warning block to patch.")
    updated = updated.replace(vector_warning, vector_replacement, 1)

    if not is_patched(updated):
        raise SystemExit("Patch application did not produce the expected markers.")

    return updated


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Verify or reapply the local Sugar Memory FTS fallback patch."
    )
    parser.add_argument("--sugar-bin", help="Path to the sugar launcher")
    parser.add_argument(
        "--check",
        action="store_true",
        help="Only verify whether the installed Sugar runtime is already patched",
    )
    args = parser.parse_args()

    sugar_bin = resolve_sugar_bin(args.sugar_bin)
    store_path = resolve_store_path(sugar_bin)
    original = store_path.read_text(encoding="utf-8")

    if is_patched(original):
        print(f"Sugar memory fallback already patched: {store_path}")
        return 0

    if args.check:
        print(f"Sugar memory fallback NOT patched: {store_path}")
        return 1

    updated = apply_patch(original)
    backup_path = store_path.with_suffix(store_path.suffix + ".bak")
    if not backup_path.exists():
        backup_path.write_text(original, encoding="utf-8")
    store_path.write_text(updated, encoding="utf-8")
    print(f"Applied Sugar memory fallback patch: {store_path}")
    print(f"Backup: {backup_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
