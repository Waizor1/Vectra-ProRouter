#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"

if [[ -n "${AST_INDEX_BIN:-}" ]]; then
  CANDIDATES=("$AST_INDEX_BIN")
else
  CANDIDATES=(
    "$HOME/.config/opencode/bin/ast-index.exe"
    "$HOME/.config/opencode/bin/ast-index"
    "ast-index.exe"
    "ast-index"
  )
fi

AST_INDEX_CMD=""
for candidate in "${CANDIDATES[@]}"; do
  if [[ "$candidate" == */* ]]; then
    if [[ -x "$candidate" ]]; then
      AST_INDEX_CMD="$candidate"
      break
    fi
  elif command -v "$candidate" >/dev/null 2>&1; then
    AST_INDEX_CMD="$candidate"
    break
  fi
done

if [[ -z "$AST_INDEX_CMD" ]]; then
  echo "ast-index binary not found." >&2
  echo "Set AST_INDEX_BIN or install ast-index under ~/.config/opencode/bin." >&2
  exit 1
fi

cd "$REPO_ROOT"
exec "$AST_INDEX_CMD" "$@"
