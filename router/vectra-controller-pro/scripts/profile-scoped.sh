#!/bin/bash
# Run build/test/reverse with the pinned explicit scope; never install/publish.
set -euo pipefail
PKG_MODULE="$(cd "$(dirname "$0")/.." && pwd)"
. "$PKG_MODULE/scripts/lib/scoped-profile.sh"
scoped_profile_env
cd "$PKG_MODULE"
exec "$SCOPED_TOOL" -seed=dmVjdHJhLWx= -literals "$@"
