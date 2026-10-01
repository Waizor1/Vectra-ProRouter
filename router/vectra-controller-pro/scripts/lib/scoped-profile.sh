# Optional reviewed profile. Sourced only when explicitly selected.
scoped_profile_env() {
 local root="${VECTRA_SCOPED_TOOL_ROOT:?set VECTRA_SCOPED_TOOL_ROOT to reviewed isolated tool/cache directory}"
 local pins="$PKG_MODULE/scripts/profiles/scoped-v1"
 [ "$(go env GOVERSION)" = go1.26.3 ] || { echo 'scoped-v1 requires pinned Go1.26.3' >&2; return 1; }
 [ "$(git -C "$PKG_MODULE" rev-parse HEAD)" = "${VECTRA_SCOPED_SOURCE_COMMIT:?set the approved exact source commit}" ] || { echo 'scoped-v1 source pin mismatch' >&2; return 1; }
 [ "$(shasum -a 256 "$pins/pins.json" | awk '{print $1}')" = aa5de141e832e94f049fba17b7aa9aaa929cadce06e6cf7e9ee3640fb02de918 ] || { echo 'scoped-v1 manifest drift' >&2; return 1; }
 [ -z "$(git -C "$PKG_MODULE" status --porcelain)" ] || { echo 'scoped-v1 refuses dirty source' >&2; return 1; }
 [ "$(shasum -a 256 "$pins/garble.patch" | awk '{print $1}')" = 2ddfb52d510db0ccc1e51e331f9bddcdc716d645f52ab920c260434f71c49375 ] || return 1
 [ "$(shasum -a 256 "$pins/garble_regression.go.txt" | awk '{print $1}')" = 17857e996a4610fc743ed17284854c037277102de1c3f2480b7d76e39ca332a2 ] || return 1
 SCOPED_TOOL="$root/tools/garble-partial-race-trial"
 [ "$(shasum -a 256 "$SCOPED_TOOL" | awk '{print $1}')" = 64d8bb3101f672c62b6e701e1292f5be17307a0516bc4e125d22ecb6f69accf9 ] || { echo 'scoped-v1 tool hash mismatch' >&2; return 1; }
 export GOTOOLCHAIN=local GOPROXY=off GOMODCACHE="$root/modcache" GOCACHE="$root/gocache" GARBLE_CACHE="$root/garblecache-partial-race-fix"
 export GOGARBLE='vectra-controller-pro/internal/vault,vectra-controller-pro/internal/state,vectra-controller-pro/internal/logging'
}
pkg_build_scoped_vctl() {
 local out="$1" arch="$2" arm="$3" mips="$4" full="$5" commit="$6" built="$7"
 [ "$arch" = arm64 ] && [ -z "$arm$mips" ] || { echo 'scoped-v1 packaged target only reviewed linux/arm64' >&2; return 1; }
 scoped_profile_env || return
 ( cd "$PKG_MODULE" && GOOS=linux GOARCH="$arch" GOARM="$arm" GOMIPS="$mips" GOMIPS64="$mips" CGO_ENABLED=0 \
 "$SCOPED_TOOL" -seed=dmVjdHJhLWx= -literals build -trimpath -buildvcs=false \
 -ldflags "-s -w -buildid= -X main.Version=$full -X main.runtimeVersion=$full -X main.Commit=$commit -X main.BuildDate=$built" -o "$out" ./cmd/vctl )
}
