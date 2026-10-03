#!/usr/bin/env bash
set -euo pipefail
base="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
awk '/^go_build_once\(\)/{on=1} on{print} on && /^}/{exit}' "$base/build-pro-feed.sh" > "$work/function.sh"
source "$work/function.sh"
CACHE="$work/cache"; VERSION=fixture; COMMIT=fixture-dirty
mkdir -p "$CACHE/go"
bin="$CACHE/go/vctl-$VERSION-arm64"
printf 'old-binary\n' > "$bin"; chmod 0755 "$bin"; printf 'old-commit\n' > "$bin.commit"
die(){ printf '%s\n' "$*" >&2; exit 1; }
pkg_build_vctl(){ return 1; }
if result="$(go_build_once arm64 2>/dev/null)"; then printf 'FAIL: rejected build returned cached executable\n' >&2; exit 1; fi
[[ "$(cat "$bin.commit")" == old-commit ]] || { printf 'FAIL: rejected build rewrote stamp\n' >&2; exit 1; }
pkg_build_vctl(){ printf 'new-binary\n' > "$1"; chmod 0755 "$1"; }
result="$(go_build_once arm64)"
[[ "$result" == "$bin" && "$(cat "$bin.commit")" == "$COMMIT" && "$(cat "$bin")" == new-binary ]]
printf 'PASS: failed compilation cannot package cached binary; successful build stamped\n'
