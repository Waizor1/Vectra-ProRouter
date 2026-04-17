#!/usr/bin/env bash

set -euo pipefail

usage() {
	cat <<'EOF'
Build the Vectra OpenWrt packages with a matching SDK and publish a signed opkg feed.

Usage:
  scripts/build-vectra-openwrt-feed.sh --sdk-root /abs/path/to/openwrt-sdk [options]

Required:
  --sdk-root PATH            OpenWrt SDK or buildroot root directory

Optional:
  --version VERSION          Package version for both packages (default: 0.1.12)
  --release N                OpenWrt PKG_RELEASE for both packages (default: 11)
  --channel NAME             Feed channel name (default: stable)
  --output-root PATH         Feed output root (default: <repo>/dist/openwrt-feed)
  --key-dir PATH             Directory with usign keys (default: <repo>/.keys/openwrt-feed)
  --feed-base-url URL        Base public URL for manifest output
                             (default: https://api.vectra-pro.net/artifacts/openwrt)
  --feed-name NAME           Feed name used in generated feed.conf (default: vectra)
  --create-key               Generate a usign keypair when missing
  --verbose                  Kept for compatibility; prints build helpers
  --help                     Show this help
EOF
}

require_command() {
	command -v "$1" >/dev/null 2>&1 || {
		echo "Missing required command: $1" >&2
		exit 1
	}
}

resolve_usign() {
	if command -v usign >/dev/null 2>&1; then
		command -v usign
		return
	fi

	local candidates=(
		"$SDK_ROOT/staging_dir/host/bin/usign"
		"$SDK_ROOT/staging_dir/hostpkg/bin/usign"
	)

	local candidate
	for candidate in "${candidates[@]}"; do
		if [[ -x "$candidate" ]]; then
			printf '%s\n' "$candidate"
			return
		fi
	done

	echo "Missing required command: usign (not in PATH and not found in SDK staging_dir)" >&2
	exit 1
}

resolve_mkhash() {
	if command -v mkhash >/dev/null 2>&1; then
		command -v mkhash
		return
	fi

	local candidates=(
		"$SDK_ROOT/staging_dir/host/bin/mkhash"
		"$SDK_ROOT/staging_dir/hostpkg/bin/mkhash"
	)

	local candidate
	for candidate in "${candidates[@]}"; do
		if [[ -x "$candidate" ]]; then
			printf '%s\n' "$candidate"
			return
		fi
	done

	echo "Missing required command: mkhash (not in PATH and not found in SDK staging_dir)" >&2
	exit 1
}

ensure_sdk_metadata() {
	if [[ ! -f "$SDK_ROOT/.config" ]]; then
		: > "$SDK_ROOT/.config"
		make -C "$SDK_ROOT" defconfig >/dev/null
	fi

	TARGET_ARCH="$(
		sed -n 's/^CONFIG_TARGET_ARCH_PACKAGES="\{0,1\}\([^"]*\)"\{0,1\}$/\1/p' "$SDK_ROOT/.config" \
			| head -n 1
	)"

	if [[ -z "$TARGET_ARCH" ]]; then
		make -C "$SDK_ROOT" defconfig >/dev/null
		TARGET_ARCH="$(
			sed -n 's/^CONFIG_TARGET_ARCH_PACKAGES="\{0,1\}\([^"]*\)"\{0,1\}$/\1/p' "$SDK_ROOT/.config" \
				| head -n 1
		)"
	fi

	[[ -n "$TARGET_ARCH" ]] || {
		echo "Unable to resolve CONFIG_TARGET_ARCH_PACKAGES from $SDK_ROOT/.config" >&2
		exit 1
	}
}

resolve_go_arch() {
	case "$TARGET_ARCH" in
		aarch64*|arm64*)
			printf 'arm64\n'
			;;
		x86_64)
			printf 'amd64\n'
			;;
		arm_*|armv7*|armv8*)
			printf 'arm\n'
			;;
		*)
			echo "Unsupported TARGET_ARCH for manual Go packaging: $TARGET_ARCH" >&2
			exit 1
			;;
	esac
}

write_postinst() {
	local path="$1"
	local body="$2"

	cat > "$path" <<EOF
#!/bin/sh
set -eu

$body
EOF
	chmod 0755 "$path"
}

mark_executable_if_present() {
	local path
	for path in "$@"; do
		if [[ -f "$path" ]]; then
			chmod 0755 "$path"
		fi
	done
}

package_ipk() {
	local data_dir="$1"
	local control_dir="$2"
	local output_file="$3"
	local temp_dir

	temp_dir="$(mktemp -d "$SDK_ROOT/tmp/vectra-ipk.XXXXXX")"
	mkdir -p "$temp_dir/data" "$temp_dir/control"
	cp -R "$data_dir/." "$temp_dir/data/"
	cp -R "$control_dir/." "$temp_dir/control/"
	printf '2.0\n' > "$temp_dir/debian-binary"
	tar --numeric-owner --owner=0 --group=0 -czf "$temp_dir/control.tar.gz" -C "$temp_dir/control" .
	tar --numeric-owner --owner=0 --group=0 -czf "$temp_dir/data.tar.gz" -C "$temp_dir/data" .
	rm -f "$output_file"
	tar --numeric-owner --owner=0 --group=0 -czf "$output_file" -C "$temp_dir" ./debian-binary ./control.tar.gz ./data.tar.gz
	rm -rf "$temp_dir"
}

build_agent_package_manually() {
	local package_root="$REPO_ROOT/router/vectra-controller-agent"
	local openwrt_root="$package_root/openwrt/files"
	local package_version="${VERSION}-r${RELEASE}"
	local go_arch
	local temp_dir
	local data_dir
	local control_dir
	local output_dir
	local output_file
	local installed_size

	go_arch="$(resolve_go_arch)"
	temp_dir="$(mktemp -d "$SDK_ROOT/tmp/vectra-agent.XXXXXX")"
	data_dir="$temp_dir/data"
	control_dir="$temp_dir/control"
	output_dir="$SDK_ROOT/bin/packages/vectra-manual"
	output_file="$output_dir/vectra-controller-agent_${package_version}_${TARGET_ARCH}.ipk"

	mkdir -p \
		"$data_dir/usr/sbin" \
		"$control_dir" \
		"$output_dir"

	(
		cd "$package_root"
		GOTOOLCHAIN=local \
		CGO_ENABLED=0 \
		GOOS=linux \
		GOARCH="$go_arch" \
			go build -trimpath -ldflags="-s -w" \
				-o "$data_dir/usr/sbin/vectra-controller-agent" \
				./cmd/vectra-controller-agent
	)

	if [[ -d "$openwrt_root" ]]; then
		cp -R "$openwrt_root/." "$data_dir/"
	fi

	mark_executable_if_present \
		"$data_dir/etc/init.d/vectra-controller" \
		"$data_dir/etc/uci-defaults/90_vectra_controller_defaults" \
		"$data_dir/usr/libexec/vectra-controller/render-config.sh"

	installed_size="$(du -sk "$data_dir" | awk '{print $1}')"

	cat > "$control_dir/control" <<EOF
Package: vectra-controller-agent
Version: ${package_version}
Depends: ca-bundle, jsonfilter, jshn, procd, procd-ujail, uci
Section: net
Priority: optional
Architecture: ${TARGET_ARCH}
Maintainer: Vectra
License: MIT
Source: https://router.vectra-pro.net
Installed-Size: ${installed_size}
Description: Outbound HTTPS polling agent for the Vectra PassWall2 control plane.
EOF

	cat > "$control_dir/conffiles" <<'EOF'
/etc/config/vectra-controller
EOF

write_postinst "$control_dir/postinst" '
if [ -n "${IPKG_INSTROOT:-}" ]; then
	exit 0
fi

if [ -x /etc/uci-defaults/90_vectra_controller_defaults ]; then
	/etc/uci-defaults/90_vectra_controller_defaults || true
fi

[ "${VECTRA_SKIP_POSTINST_RESTART:-}" = "1" ] && exit 0
[ -f /tmp/vectra-skip-postinst-restart ] && exit 0

/etc/init.d/vectra-controller enable >/dev/null 2>&1 || true
if /etc/init.d/vectra-controller running >/dev/null 2>&1; then
	/etc/init.d/vectra-controller restart >/dev/null 2>&1 || true
else
	/etc/init.d/vectra-controller start >/dev/null 2>&1 || true
fi
'

	package_ipk "$data_dir" "$control_dir" "$output_file"
	rm -rf "$temp_dir"
	printf '%s\n' "$output_file"
}

build_luci_package_manually() {
	local package_root="$REPO_ROOT/router/luci-app-vectra-controller"
	local package_version="${VERSION}-r${RELEASE}"
	local temp_dir
	local data_dir
	local control_dir
	local output_dir
	local output_file
	local installed_size

	temp_dir="$(mktemp -d "$SDK_ROOT/tmp/vectra-luci.XXXXXX")"
	data_dir="$temp_dir/data"
	control_dir="$temp_dir/control"
	output_dir="$SDK_ROOT/bin/packages/vectra-manual"
	output_file="$output_dir/luci-app-vectra-controller_${package_version}_all.ipk"

	mkdir -p "$data_dir" "$control_dir" "$output_dir"

	if [[ -d "$package_root/root" ]]; then
		cp -R "$package_root/root/." "$data_dir/"
	fi

	if [[ -d "$package_root/htdocs" ]]; then
		mkdir -p "$data_dir/www"
		cp -R "$package_root/htdocs/." "$data_dir/www/"
	fi

	mark_executable_if_present \
		"$data_dir/usr/libexec/vectra-controller/luci-bridge.sh"

	installed_size="$(du -sk "$data_dir" | awk '{print $1}')"

	cat > "$control_dir/control" <<EOF
Package: luci-app-vectra-controller
Version: ${package_version}
Depends: luci-base, rpcd, vectra-controller-agent
Section: luci
Priority: optional
Architecture: all
Maintainer: Vectra
License: MIT
Source: https://router.vectra-pro.net
Installed-Size: ${installed_size}
Description: LuCI bootstrap, diagnostics, and rescue console for the Vectra router agent.
EOF

	write_postinst "$control_dir/postinst" '
if [ -n "${IPKG_INSTROOT:-}" ]; then
	exit 0
fi

rm -f /tmp/luci-indexcache.*
rm -rf /tmp/luci-modulecache/
/etc/init.d/rpcd reload >/dev/null 2>&1 || true
'

	package_ipk "$data_dir" "$control_dir" "$output_file"
	rm -rf "$temp_dir"
	printf '%s\n' "$output_file"
}

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SDK_ROOT=""
VERSION="0.1.12"
RELEASE="11"
CHANNEL="stable"
OUTPUT_ROOT="$REPO_ROOT/dist/openwrt-feed"
KEY_DIR="$REPO_ROOT/.keys/openwrt-feed"
FEED_BASE_URL="https://api.vectra-pro.net/artifacts/openwrt"
FEED_NAME="vectra"
CREATE_KEY=0
MAKE_LOG_LEVEL="V=s"
USIGN_BIN=""
MKHASH_BIN=""
TARGET_ARCH=""

while [[ $# -gt 0 ]]; do
	case "$1" in
		--sdk-root)
			SDK_ROOT="$2"
			shift 2
			;;
		--version)
			VERSION="$2"
			shift 2
			;;
		--release)
			RELEASE="$2"
			shift 2
			;;
		--channel)
			CHANNEL="$2"
			shift 2
			;;
		--output-root)
			OUTPUT_ROOT="$2"
			shift 2
			;;
		--key-dir)
			KEY_DIR="$2"
			shift 2
			;;
		--feed-base-url)
			FEED_BASE_URL="$2"
			shift 2
			;;
		--feed-name)
			FEED_NAME="$2"
			shift 2
			;;
		--create-key)
			CREATE_KEY=1
			shift
			;;
		--verbose)
			MAKE_LOG_LEVEL="V=sc"
			shift
			;;
		--help|-h)
			usage
			exit 0
			;;
		*)
			echo "Unknown argument: $1" >&2
			usage >&2
			exit 1
			;;
	esac
done

if [[ -z "$SDK_ROOT" ]]; then
	echo "--sdk-root is required" >&2
	usage >&2
	exit 1
fi

require_command awk
require_command cp
require_command du
require_command find
require_command go
require_command gzip
require_command make
require_command mktemp
require_command sed
require_command tar

SDK_ROOT="$(cd "$SDK_ROOT" && pwd)"
OUTPUT_ROOT="$(mkdir -p "$OUTPUT_ROOT" && cd "$OUTPUT_ROOT" && pwd)"
KEY_DIR_ABS="$KEY_DIR"
mkdir -p "$KEY_DIR_ABS"
KEY_DIR_ABS="$(cd "$KEY_DIR_ABS" && pwd)"
USIGN_BIN="$(resolve_usign)"
MKHASH_BIN="$(resolve_mkhash)"

[[ -d "$SDK_ROOT/package" ]] || {
	echo "OpenWrt SDK root is missing package/: $SDK_ROOT" >&2
	exit 1
}
[[ -x "$SDK_ROOT/scripts/ipkg-make-index.sh" ]] || {
	echo "OpenWrt SDK root is missing scripts/ipkg-make-index.sh: $SDK_ROOT" >&2
	exit 1
}

SECRET_KEY="$KEY_DIR_ABS/${FEED_NAME}.sec"
PUBLIC_KEY="$KEY_DIR_ABS/${FEED_NAME}.pub"
if [[ ! -f "$SECRET_KEY" || ! -f "$PUBLIC_KEY" ]]; then
	if [[ "$CREATE_KEY" -ne 1 ]]; then
		echo "Missing usign keypair in $KEY_DIR_ABS. Re-run with --create-key or pre-provision ${FEED_NAME}.sec/${FEED_NAME}.pub" >&2
		exit 1
	fi
	"$USIGN_BIN" -G -s "$SECRET_KEY" -p "$PUBLIC_KEY" -c "Vectra OpenWrt feed"
fi

ensure_sdk_metadata

if [[ "$MAKE_LOG_LEVEL" == "V=sc" ]]; then
	printf 'Manual packaging target arch: %s\n' "$TARGET_ARCH"
fi

AGENT_PACKAGE="$(build_agent_package_manually)"
[[ -f "$AGENT_PACKAGE" ]] || {
	echo "Expected vectra-controller-agent .ipk after build" >&2
	exit 1
}

LUCI_PACKAGE="$(build_luci_package_manually)"
[[ -f "$LUCI_PACKAGE" ]] || {
	echo "Expected luci-app-vectra-controller .ipk after packaging" >&2
	exit 1
}

FEED_DIR="$OUTPUT_ROOT/$CHANNEL/$TARGET_ARCH"
rm -rf "$FEED_DIR"
mkdir -p "$FEED_DIR"

cp "$AGENT_PACKAGE" "$FEED_DIR/"
cp "$LUCI_PACKAGE" "$FEED_DIR/"

MKHASH="$MKHASH_BIN" "$SDK_ROOT/scripts/ipkg-make-index.sh" "$FEED_DIR" \
	| sed -E 's#^Filename: .*/#Filename: #' \
	> "$FEED_DIR/Packages"
gzip -9c "$FEED_DIR/Packages" > "$FEED_DIR/Packages.gz"
"$USIGN_BIN" -S -m "$FEED_DIR/Packages" -s "$SECRET_KEY" -x "$FEED_DIR/Packages.sig"
cp "$PUBLIC_KEY" "$FEED_DIR/${FEED_NAME}.pub"

cat > "$FEED_DIR/feed.conf" <<EOF
src/gz ${FEED_NAME} ${FEED_BASE_URL}/${CHANNEL}/${TARGET_ARCH}
EOF

cat > "$FEED_DIR/index.json" <<EOF
{
  "feedName": "${FEED_NAME}",
  "channel": "${CHANNEL}",
  "targetArch": "${TARGET_ARCH}",
  "version": "${VERSION}",
  "release": "${RELEASE}",
  "packages": [
    "$(basename "$AGENT_PACKAGE")",
    "$(basename "$LUCI_PACKAGE")"
  ],
  "publicKey": "${FEED_NAME}.pub",
  "feedConfig": "feed.conf",
  "packagesIndex": "Packages",
  "packagesSignature": "Packages.sig"
}
EOF

echo "Vectra OpenWrt feed ready:"
echo "  Feed dir: $FEED_DIR"
echo "  Public key: $FEED_DIR/${FEED_NAME}.pub"
echo "  Feed config: $FEED_DIR/feed.conf"
