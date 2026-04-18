#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]]; then
  cat >&2 <<'EOF'
Usage: bash ./scripts/run-ps1.sh ./scripts/<script>.ps1 [args...]

This wrapper prefers PowerShell 7 (`pwsh`) and falls back to `powershell`
when available.
EOF
  exit 64
fi

if command -v pwsh >/dev/null 2>&1; then
  ps_runner="pwsh"
elif command -v powershell >/dev/null 2>&1; then
  ps_runner="powershell"
else
  cat >&2 <<'EOF'
PowerShell was not found.

Install PowerShell 7 on macOS/Linux, for example:
  brew install --cask powershell

Then rerun the command, or invoke the .ps1 file directly with `pwsh -File`.
EOF
  exit 127
fi

script_path="$1"
shift

exec "$ps_runner" -NoProfile -File "$script_path" "$@"
