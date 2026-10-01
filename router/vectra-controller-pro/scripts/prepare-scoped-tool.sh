#!/bin/bash
# Reproduce the pinned patched tool in a private task-local root; never globally install.
set -euo pipefail
PKG_MODULE="$(cd "$(dirname "$0")/.." && pwd)"
PINS="$PKG_MODULE/scripts/profiles/scoped-v1"
ROOT="${VECTRA_SCOPED_TOOL_ROOT:?set private task-local tool root}"
[ "$(go env GOVERSION)" = go1.26.3 ]
[ "$(go env GOHOSTOS)/$(go env GOHOSTARCH)" = darwin/arm64 ] || { echo 'reviewed tool host is darwin/arm64' >&2; exit 1; }
mkdir -p "$ROOT/tools"; chmod 700 "$ROOT"
export GOTOOLCHAIN=local CGO_ENABLED=0 GOMODCACHE="$ROOT/modcache" GOCACHE="$ROOT/gocache" GOPROXY=off GOSUMDB=sum.golang.org
# Explicit opt-in permits ordinary registry downloads; default is offline.
[ "${VECTRA_SCOPED_ALLOW_DOWNLOADS:-0}" != 1 ] || export GOPROXY=https://proxy.golang.org
cd "$ROOT"
go mod download -json mvdan.cc/garble@v0.17.0 > module-source.json
python3 - "$ROOT" "$PINS" <<'PY'
import sys,json,pathlib,hashlib,shutil
root,pins=map(pathlib.Path,sys.argv[1:]);p=json.loads((pins/'pins.json').read_text());m=json.loads((root/'module-source.json').read_text())
assert m['Version']==p['toolVersion'] and m['Sum']==p['moduleSum'] and m['GoModSum']==p['goModSum']
assert m['Origin']['Hash']==p['toolSourceCommit']
for f,k in [('garble.patch','patchSha256'),('garble_regression.go.txt','regressionSha256')]:assert hashlib.sha256((pins/f).read_bytes()).hexdigest()==p[k]
source=root/'tool-source'
if source.exists():raise SystemExit('tool-source already exists; choose fresh root or review/remove it explicitly')
shutil.copytree(m['Dir'],source)
source.chmod(0o700)
for f in source.rglob('*'):
 if f.is_file():f.chmod(0o600)
 else:f.chmod(0o700)
shutil.copyfile(pins/'garble_regression.go.txt',source/'partial_import_test.go')
PY
patch -p1 -d "$ROOT/tool-source" < "$PINS/garble.patch"
cd "$ROOT/tool-source"
go build -trimpath -buildvcs=false -ldflags '-s -w -buildid=' -o "$ROOT/tools/garble-partial-race-trial" .
python3 - "$ROOT/tools/garble-partial-race-trial" "$PINS/pins.json" <<'PY'
import sys,pathlib,json,hashlib
binary,pins=map(pathlib.Path,sys.argv[1:]);assert hashlib.sha256(binary.read_bytes()).hexdigest()==json.loads(pins.read_text())['toolSha256'],'tool hash drift'
print('pinned scoped-v1 tool verified')
PY
