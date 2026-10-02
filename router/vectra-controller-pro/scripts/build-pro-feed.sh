#!/usr/bin/env bash
# Build the Vectra "pro" opkg feed: vectra-controller-pro, xray-core and the geo
# data for every OpenWrt architecture a Go binary runs on — without the SDK.
#
#   scripts/build-pro-feed.sh [--arch ARCH]... [--out DIR] [--xray vX.Y.Z]
#
# Then sign it and bake the installer (scripts/sign-pro-feed.sh). Nothing is
# published: the feed lands in dist/pro-feed (gitignored).
#
# One directory per architecture, each a complete opkg feed:
#   <out>/<arch>/vectra-controller-pro_<ver>_<arch>.ipk   vctl + the router UI
#   <out>/<arch>/xray-core_<xray>-r1_<arch>.ipk           the upstream XTLS build
#   <out>/<arch>/vectra-geodata_<ver>_all.ipk             geoip.dat, geosite.dat
#   <out>/<arch>/Packages, Packages.gz
#   <out>/install.sh.in, <out>/feed.json
#
# Why each piece:
#   - vctl is a static CGO_ENABLED=0 binary, so one Go build serves every
#     OpenWrt architecture of its CPU family; the package is the Makefile's
#     (scripts/lib/pkg.sh), with two additions for a router that has none of
#     the fleet's stack: `xray-core (>= $XRAY_MIN)` — older xray lacks
#     api.listen — and vectra-geodata.
#   - xray-core is the XTLS release binary, checked against the release's own
#     .dgst. OpenWrt's feeds carry older xray on many branches; opkg takes the
#     highest version from any feed, so a newer official one still wins.
#   - the geo data is the set the stand proves the data plane with, pinned by
#     hash, and checked at build time to hold every geosite:/geoip: category the
#     provider's config routes by — a missing one stops xray from starting.
set -euo pipefail
export COPYFILE_DISABLE=1

PKG_MODULE="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=lib/pkg.sh
. "$PKG_MODULE/scripts/lib/pkg.sh"

OUT="$PKG_MODULE/dist/pro-feed"
CACHE="$PKG_MODULE/dist/cache"
XRAY_VERSION="v26.3.27"
XRAY_MIN="26.3.27"
# The geo data: the files the data-plane stand runs with (test/dataplane/run.sh,
# GEO_BASE), pinned. Moving the pin is deliberate: new hashes, a new version.
GEO_BASE="${GEO_BASE:-https://api.vectra-pro.net/artifacts/canary/vctl}"
GEOIP_SHA256="f3d4ddabdd69d4a32f403b1749f32b3a58a0074d47d59b8d6c35759df60277a9"
GEOSITE_SHA256="73898f78635258cf8fa75b8fdde376afc202a47e0806ce773e04140b56b9675e"
GEODATA_VERSION="2026.9.28-r2"
# vectra-reporter: the bug reporter (ADR-0007), a package of its own so a bad
# vctl release never replaces it.
REPORTER_VERSION="1.0.0-r3"
# The categories the provider's xray config routes by, read from the scrubbed
# provider entry the Go tests use — so the check follows the fixture.
PROVIDER_FIXTURE="$PKG_MODULE/internal/coreengine/xray/testdata/provider/entry-00.json"

# OpenWrt architecture -> Go target (GOARCH[:GOARM|GOMIPS]) -> XTLS asset and
# the binary inside it. Every architecture of OpenWrt 23.05 and 24.10 that Go
# builds for. Soft float wherever the architecture name promises no FPU: a
# hard-float binary dies with SIGILL on a CPU (or kernel) without one.
# Left out: armeb_xscale (no big-endian ARM in Go), powerpc_* (no 32-bit PowerPC
# in Go; powerpc64_e5500 predates the POWER8 Go requires), i386_pentium-mmx
# (xray's 386 build needs SSE2).
ARCH_TABLE="
aarch64_cortex-a53        arm64            Xray-linux-arm64-v8a.zip  xray
aarch64_cortex-a72        arm64            Xray-linux-arm64-v8a.zip  xray
aarch64_cortex-a76        arm64            Xray-linux-arm64-v8a.zip  xray
aarch64_generic           arm64            Xray-linux-arm64-v8a.zip  xray
arm_cortex-a15_neon-vfpv4 arm:7            Xray-linux-arm32-v7a.zip  xray
arm_cortex-a5_vfpv4       arm:7            Xray-linux-arm32-v7a.zip  xray
arm_cortex-a7_neon-vfpv4  arm:7            Xray-linux-arm32-v7a.zip  xray
arm_cortex-a7_vfpv4       arm:7            Xray-linux-arm32-v7a.zip  xray
arm_cortex-a8_vfpv3       arm:7            Xray-linux-arm32-v7a.zip  xray
arm_cortex-a9_neon        arm:7            Xray-linux-arm32-v7a.zip  xray
arm_cortex-a9_vfpv3-d16   arm:7            Xray-linux-arm32-v7a.zip  xray
arm_arm1176jzf-s_vfp      arm:6            Xray-linux-arm32-v6.zip   xray
arm_cortex-a7             arm:5            Xray-linux-arm32-v5.zip   xray
arm_cortex-a9             arm:5            Xray-linux-arm32-v5.zip   xray
arm_arm926ej-s            arm:5            Xray-linux-arm32-v5.zip   xray
arm_fa526                 arm:5            Xray-linux-arm32-v5.zip   xray
arm_mpcore                arm:5            Xray-linux-arm32-v5.zip   xray
arm_xscale                arm:5            Xray-linux-arm32-v5.zip   xray
mipsel_24kc               mipsle:softfloat Xray-linux-mips32le.zip   xray_softfloat
mipsel_24kc_24kf          mipsle:softfloat Xray-linux-mips32le.zip   xray_softfloat
mipsel_74kc               mipsle:softfloat Xray-linux-mips32le.zip   xray_softfloat
mipsel_mips32             mipsle:softfloat Xray-linux-mips32le.zip   xray_softfloat
mips_24kc                 mips:softfloat   Xray-linux-mips32.zip     xray_softfloat
mips_4kec                 mips:softfloat   Xray-linux-mips32.zip     xray_softfloat
mips_mips32               mips:softfloat   Xray-linux-mips32.zip     xray_softfloat
mips64_mips64r2           mips64           Xray-linux-mips64.zip     xray
mips64_octeonplus         mips64           Xray-linux-mips64.zip     xray
mips64el_mips64r2         mips64le         Xray-linux-mips64le.zip   xray
x86_64                    amd64            Xray-linux-64.zip         xray
i386_pentium4             386              Xray-linux-32.zip         xray
riscv64_riscv64           riscv64          Xray-linux-riscv64.zip    xray
loongarch64_generic       loong64          Xray-linux-loong64.zip    xray
"

usage() { sed -n '2,8p' "$0" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }
ARCHS=()
while [[ $# -gt 0 ]]; do
	case "$1" in
	--arch) ARCHS+=("$2"); shift 2 ;;
	--out) OUT="$2"; shift 2 ;;
	--xray) XRAY_VERSION="$2"; XRAY_MIN="${2#v}"; shift 2 ;;
	-h | --help) usage 0 ;;
	*) echo "unknown argument: $1" >&2; usage 2 ;;
	esac
done

row_of() { echo "$ARCH_TABLE" | awk -v a="$1" '$1 == a { print; exit }'; }
if [[ ${#ARCHS[@]} -eq 0 ]]; then
	while read -r a _; do [[ -n "$a" ]] && ARCHS+=("$a"); done <<< "$ARCH_TABLE"
fi
for a in "${ARCHS[@]}"; do
	[[ -n "$(row_of "$a")" ]] || { echo "not an architecture this feed builds: $a" >&2; exit 2; }
done

step() { printf '\n==> %s\n' "$*"; }
die() { echo "FATAL: $*" >&2; exit 1; }
sha256() { shasum -a 256 "$1" | awk '{print $1}'; }

VERSION="$(pkg_version)"
EPOCH="$(pkg_commit_epoch)"
COMMIT="$(git -C "$PKG_MODULE" rev-parse --short HEAD 2>/dev/null || echo dev)"
git -C "$PKG_MODULE" diff --quiet HEAD -- . 2>/dev/null || COMMIT="$COMMIT-dirty"
FEEDTOOL="$CACHE/feedtool"
mkdir -p "$CACHE/xray-$XRAY_VERSION" "$CACHE/go" "$CACHE/geo"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

step "feedtool"
( cd "$PKG_MODULE" && go build -o "$FEEDTOOL" ./tools/feedtool )
ipk() { "$FEEDTOOL" ipk -data "$1" -control "$2" -out "$3" -mtime "$EPOCH"; }

# ------------------------------------------------------------------ xray ----

xray_asset() { # <asset.zip> -> the verified zip in the cache
	local zip="$CACHE/xray-$XRAY_VERSION/$1" url="https://github.com/XTLS/Xray-core/releases/download/$XRAY_VERSION/$1"
	if [[ ! -f "$zip.ok" ]]; then
		curl -fsSL --retry 3 "$url" -o "$zip.part" || die "download $url"
		curl -fsSL --retry 3 "$url.dgst" -o "$zip.dgst" || die "download $url.dgst — an unverified xray never goes into the feed"
		local want got
		want="$(awk -F'= ' '/^SHA2-256=|^SHA256=/{print $2; exit}' "$zip.dgst")"
		got="$(sha256 "$zip.part")"
		[[ -n "$want" && "$want" == "$got" ]] || die "$1: sha256 $got, the release says ${want:-nothing}"
		mv "$zip.part" "$zip"
		echo "$got" > "$zip.ok"
	fi
	echo "$zip"
}

host_xray() { # an xray that runs here, to check the geo data with
	local asset
	case "$(uname -s)/$(uname -m)" in
	Darwin/arm64) asset=Xray-macos-arm64-v8a.zip ;;
	Darwin/x86_64) asset=Xray-macos-64.zip ;;
	Linux/x86_64) asset=Xray-linux-64.zip ;;
	Linux/aarch64) asset=Xray-linux-arm64-v8a.zip ;;
	*) die "no xray build for $(uname -sm) to check the geo data with" ;;
	esac
	local zip dir="$CACHE/xray-$XRAY_VERSION/host"
	zip="$(xray_asset "$asset")"
	[[ -x "$dir/xray" ]] || { mkdir -p "$dir" && unzip -oq "$zip" xray -d "$dir" && chmod 0755 "$dir/xray"; }
	echo "$dir/xray"
}

# ------------------------------------------------------------------- geo ----

step "geo data ($GEODATA_VERSION)"
for f in geoip.dat geosite.dat; do
	want="$GEOIP_SHA256"; [[ "$f" == geosite.dat ]] && want="$GEOSITE_SHA256"
	if [[ ! -f "$CACHE/geo/$f" || "$(sha256 "$CACHE/geo/$f")" != "$want" ]]; then
		curl -fsSL --retry 3 "$GEO_BASE/$f" -o "$CACHE/geo/$f.part" || die "download $GEO_BASE/$f"
		got="$(sha256 "$CACHE/geo/$f.part")"
		[[ "$got" == "$want" ]] || die "$f: sha256 $got, pinned $want — move the pin (and GEODATA_VERSION) deliberately"
		mv "$CACHE/geo/$f.part" "$CACHE/geo/$f"
	fi
	echo "  $f $(wc -c < "$CACHE/geo/$f" | tr -d ' ') B, sha256 pinned"
done

# Every category the provider routes by must be in the files, or xray refuses
# the config and the router carries nothing. xray itself is the judge.
[[ -f "$PROVIDER_FIXTURE" ]] || die "no provider fixture at $PROVIDER_FIXTURE"
sites="$(grep -oE '"geosite:[^"]+"' "$PROVIDER_FIXTURE" | sort -u | paste -sd, -)"
ips="$(grep -oE '"geoip:[^"]+"' "$PROVIDER_FIXTURE" | sort -u | paste -sd, -)"
[[ -n "$sites" && -n "$ips" ]] || die "no geosite:/geoip: rules found in the provider fixture"
cat > "$work/geo-check.json" <<JSON
{
  "log": { "loglevel": "warning" },
  "outbounds": [ { "protocol": "freedom", "tag": "direct" } ],
  "routing": { "rules": [
    { "type": "field", "domain": [ $sites ], "outboundTag": "direct" },
    { "type": "field", "ip": [ $ips ], "outboundTag": "direct" }
  ] }
}
JSON
hx="$(host_xray)"
XRAY_LOCATION_ASSET="$CACHE/geo" "$hx" run -test -c "$work/geo-check.json" > "$work/geo-check.log" 2>&1 \
	|| { cat "$work/geo-check.log" >&2; die "the geo data lacks a category the provider routes by"; }
echo "  xray $XRAY_VERSION accepts every category: $(echo "$sites,$ips" | tr -d '"')"

geo_data="$work/geo-data"; geo_ctrl="$work/geo-ctrl"
install -d "$geo_data/usr/share/vectra-controller-pro/geo" "$geo_ctrl"
install -m 0644 "$CACHE/geo/geoip.dat" "$CACHE/geo/geosite.dat" "$geo_data/usr/share/vectra-controller-pro/geo/"
# The same check travels with the data: the installer runs it on the router
# against the directory xray will read.
install -m 0644 "$work/geo-check.json" "$geo_data/usr/share/vectra-controller-pro/geo/check.json"
cat > "$geo_ctrl/control" <<CTL
Package: vectra-geodata
Version: $GEODATA_VERSION
Source: router/vectra-controller-pro
License: CC-BY-SA-4.0
Section: net
Architecture: all
Maintainer: Vectra
Description: geoip.dat and geosite.dat that Vectra's routing needs (every
 category the provider's config uses), in Vectra's own directory.
CTL
# Vectra's data stays in Vectra's directory: vctl reads it there
# (XRAY_LOCATION_ASSET), whatever another package keeps in /usr/share/v2ray.
# Versions before 2026.9.28-r2 linked their files into an empty
# /usr/share/v2ray, where vctl before 0.6.0-r18 read them. vctl 0.6.0-r18's
# install takes those links away (its uci-defaults), not this package; and as
# the old version's prerm takes them away on an upgrade too, this postinst
# puts them back while the vctl installed is older than 0.6.0-r18 (upgraded
# alone, or before vctl) and nothing else is there. The prerm takes them
# away on a removal only.
cat > "$geo_ctrl/postinst" <<'SH'
#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] && exit 0
v="$(sed -n 's/^Version: *//p' /usr/lib/opkg/info/vectra-controller-pro.control 2>/dev/null)"
[ -n "$v" ] || exit 0
base="${v%-*}"; rel="${v##*-}"; rel="${rel#r}"
case "$base" in
0.[0-5].*) ;;
0.6.0) [ "$rel" -lt 18 ] 2>/dev/null || exit 0 ;;
*) exit 0 ;;
esac
mkdir -p /usr/share/v2ray
for f in geoip.dat geosite.dat; do
	l="/usr/share/v2ray/$f"
	[ -L "$l" ] && [ ! -e "$l" ] && rm -f "$l"
	[ -e "$l" ] || ln -s "/usr/share/vectra-controller-pro/geo/$f" "$l"
done
exit 0
SH
cat > "$geo_ctrl/prerm" <<'SH'
#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] && exit 0
[ "${PKG_UPGRADE:-0}" = 1 ] && exit 0
[ "${1:-}" = upgrade ] && exit 0
for f in geoip.dat geosite.dat; do
	l="/usr/share/v2ray/$f"
	[ "$(readlink "$l" 2>/dev/null)" = "/usr/share/vectra-controller-pro/geo/$f" ] && rm -f "$l"
done
exit 0
SH
chmod 0755 "$geo_ctrl/postinst" "$geo_ctrl/prerm"
GEO_IPK="$work/vectra-geodata_${GEODATA_VERSION}_all.ipk"
ipk "$geo_data" "$geo_ctrl" "$GEO_IPK"

# ----------------------------------------------------------------- vctl ----

step "vectra-controller-pro $VERSION ($COMMIT)"
# The payload without vctl, once; each Go target adds its own binary.
payload="$work/vctl-payload"; vctl_ctrl="$work/vctl-ctrl"
install -d "$payload" "$vctl_ctrl"
n="$(pkg_stage_payload "$payload")"
echo "  payload: $n files from openwrt/Makefile"
pkg_maintainer_scripts "$vctl_ctrl"
echo "/etc/config/vectra-controller-pro" > "$vctl_ctrl/conffiles"
deps="$(pkg_depends | awk -v min="$XRAY_MIN" 'BEGIN { FS = OFS = ", " } { for (i = 1; i <= NF; i++) if ($i == "xray-core") $i = "xray-core (>= " min ")"; print }'), vectra-geodata, vectra-reporter (>= 1.0.0-r3)"
[[ "$deps" == *"xray-core (>= $XRAY_MIN)"* ]] || die "the Makefile's DEPENDS no longer names xray-core: $deps"

go_build_once() { # <go-target> -> the cached binary
	local target="$1" goarch goarm="" gomips="" bin
	goarch="${target%%:*}"
	if [[ "$target" == *:* ]]; then
		case "$goarch" in
		arm) goarm="${target#*:}" ;;
		*) gomips="${target#*:}" ;;
		esac
	fi
	bin="$CACHE/go/vctl-$VERSION-${target//:/-}"
	# A cached binary is reused only for the same commit and a clean tree.
	if [[ ! -x "$bin" || "$COMMIT" == *-dirty || "$(cat "$bin.commit" 2>/dev/null)" != "$COMMIT" ]]; then
		pkg_build_vctl "$bin" "$goarch" "$goarm" "$gomips" >&2 || die "build vctl for $target"
		echo "$COMMIT" > "$bin.commit"
	fi
	echo "$bin"
}

# The reporter's binary, one per Go target and commit, as vctl's.
reporter_build_once() { # <go-target> -> the cached binary
	local target="$1" goarch goarm="" gomips="" bin
	goarch="${target%%:*}"
	if [[ "$target" == *:* ]]; then
		case "$goarch" in
		arm) goarm="${target#*:}" ;;
		*) gomips="${target#*:}" ;;
		esac
	fi
	bin="$CACHE/go/vectra-reporter-$REPORTER_VERSION-${target//:/-}"
	if [[ ! -x "$bin" || "$COMMIT" == *-dirty || "$(cat "$bin.commit" 2>/dev/null)" != "$COMMIT" ]]; then
		( cd "$PKG_MODULE" && GOOS=linux GOARCH="$goarch" GOARM="$goarm" GOMIPS="$gomips" GOMIPS64="$gomips" CGO_ENABLED=0 \
			go build -trimpath -buildvcs=false -ldflags "-s -w -buildid= -X main.Version=$REPORTER_VERSION" \
			-o "$bin" ./cmd/vectra-reporter ) >&2 || die "build vectra-reporter for $target"
		echo "$COMMIT" > "$bin.commit"
	fi
	echo "$bin"
}

# ---------------------------------------------------------------- feeds ----

rm -rf "$OUT"
mkdir -p "$OUT"
for arch in "${ARCHS[@]}"; do
	read -r _ target asset member <<< "$(row_of "$arch")"
	dir="$OUT/$arch"
	mkdir -p "$dir"

	# vctl
	pdata="$work/p-$arch"; pctrl="$work/c-$arch"
	rm -rf "$pdata" "$pctrl"
	cp -R "$payload" "$pdata"
	cp -R "$vctl_ctrl" "$pctrl"
	install -m 0755 "$(go_build_once "$target")" "$pdata/usr/sbin/vctl"
	cat > "$pctrl/control" <<CTL
Package: vectra-controller-pro
Version: $VERSION
Depends: $deps
Source: router/vectra-controller-pro
License: MIT
Section: net
Architecture: $arch
Maintainer: Vectra
Description: Vectra Controller Pro: owns xray end to end (config, process,
 firewall, DNS, subscription) with the router UI in LuCI. Build $COMMIT.
CTL
	ipk "$pdata" "$pctrl" "$dir/vectra-controller-pro_${VERSION}_${arch}.ipk"

	# vectra-reporter
	rdata="$work/r-$arch"; rctrl="$work/rc-$arch"
	rm -rf "$rdata" "$rctrl"
	install -d "$rdata/usr/sbin" "$rctrl"
	install -m 0755 "$(reporter_build_once "$target")" "$rdata/usr/sbin/vectra-reporter"
	cp -R "$PKG_MODULE/openwrt/reporter/etc" "$rdata/etc"
	chmod 0755 "$rdata/etc/init.d/vectra-reporter"
	chmod 0644 "$rdata/etc/config/vectra-reporter"
	for s in postinst prerm postrm; do install -m 0755 "$PKG_MODULE/openwrt/reporter/$s" "$rctrl/$s"; done
	echo "/etc/config/vectra-reporter" > "$rctrl/conffiles"
	cat > "$rctrl/control" <<CTL
Package: vectra-reporter
Version: $REPORTER_VERSION
Source: router/vectra-controller-pro
License: MIT
Section: net
Architecture: $arch
Maintainer: Vectra
Description: Vectra's bug reports: crashes and what goes other than meant,
 sent signed and redacted to Vectra (ADR-0007). Build $COMMIT.
CTL
	ipk "$rdata" "$rctrl" "$dir/vectra-reporter_${REPORTER_VERSION}_${arch}.ipk"

	# xray
	zip="$(xray_asset "$asset")"
	xdata="$work/x-$arch"; xctrl="$work/xc-$arch"
	rm -rf "$xdata" "$xctrl"
	install -d "$xdata/usr/bin" "$xctrl"
	unzip -p "$zip" "$member" > "$xdata/usr/bin/xray" || die "$asset has no $member"
	[[ -s "$xdata/usr/bin/xray" ]] || die "$asset: $member is empty"
	chmod 0755 "$xdata/usr/bin/xray"
	cat > "$xctrl/control" <<CTL
Package: xray-core
Version: $XRAY_MIN-r1
Source: https://github.com/XTLS/Xray-core/releases/tag/$XRAY_VERSION
License: MPL-2.0
Section: net
Architecture: $arch
Maintainer: Vectra
Description: Xray-core $XRAY_VERSION, the XTLS release build ($asset, $member,
 sha256 $(cat "$zip.ok")).
CTL
	ipk "$xdata" "$xctrl" "$dir/xray-core_${XRAY_MIN}-r1_${arch}.ipk"

	cp "$GEO_IPK" "$dir/"
	"$FEEDTOOL" index -arch "$arch" "$dir" > /dev/null
	printf '  %-26s vctl %s  xray %s\n' "$arch" "$(du -h "$dir/vectra-controller-pro_${VERSION}_${arch}.ipk" | cut -f1)" "$(du -h "$dir/xray-core_${XRAY_MIN}-r1_${arch}.ipk" | cut -f1)"
done

install -m 0644 "$PKG_MODULE/install/install.sh" "$OUT/install.sh.in"
{
	printf '{\n  "version": "%s",\n  "commit": "%s",\n  "xray": "%s",\n  "geodata": "%s",\n  "reporter": "%s",\n  "archs": [' "$VERSION" "$COMMIT" "$XRAY_MIN" "$GEODATA_VERSION" "$REPORTER_VERSION"
	sep=""
	for a in "${ARCHS[@]}"; do printf '%s"%s"' "$sep" "$a"; sep=", "; done
	printf ']\n}\n'
} > "$OUT/feed.json"

step "done: $OUT ($(du -sh "$OUT" | cut -f1), ${#ARCHS[@]} architectures) — unsigned; next: scripts/sign-pro-feed.sh --feed $OUT --key-dir <dir>"
