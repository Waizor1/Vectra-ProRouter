#!/usr/bin/env bash

set -euo pipefail

# macOS tar/cp can materialize extended attributes as AppleDouble `._*`
# entries unless this is disabled. Those files are invalid in OpenWrt .ipk
# payloads because opkg treats them as real package-owned paths.
export COPYFILE_DISABLE=1

usage() {
	cat <<'EOF'
Build the Vectra OpenWrt packages with a matching SDK and publish a signed opkg feed.

Usage:
  scripts/build-vectra-openwrt-feed.sh --sdk-root /abs/path/to/openwrt-sdk [options]

Required:
  --sdk-root PATH            OpenWrt SDK or buildroot root directory

Optional:
  --version VERSION          Package version for both packages
                             (default: derived from controller Makefiles)
  --release N                OpenWrt PKG_RELEASE for both packages
                             (default: derived from controller Makefiles)
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

read_makefile_assignment() {
	local file="$1"
	local key="$2"

	sed -n -E "s/^${key}\\?=([^[:space:]]+).*$/\\1/p" "$file" | head -n 1
}

resolve_package_defaults() {
	local agent_makefile="$REPO_ROOT/router/vectra-controller-agent/openwrt/Makefile"
	local luci_makefile="$REPO_ROOT/router/luci-app-vectra-controller/Makefile"
	local agent_version
	local agent_release
	local luci_version
	local luci_release

	[[ -f "$agent_makefile" ]] || {
		echo "Missing controller agent Makefile: $agent_makefile" >&2
		exit 1
	}
	[[ -f "$luci_makefile" ]] || {
		echo "Missing LuCI controller Makefile: $luci_makefile" >&2
		exit 1
	}

	agent_version="$(read_makefile_assignment "$agent_makefile" "VECTRA_VERSION")"
	agent_release="$(read_makefile_assignment "$agent_makefile" "VECTRA_RELEASE")"
	luci_version="$(read_makefile_assignment "$luci_makefile" "VECTRA_VERSION")"
	luci_release="$(read_makefile_assignment "$luci_makefile" "VECTRA_RELEASE")"

	[[ -n "$agent_version" && -n "$agent_release" ]] || {
		echo "Unable to resolve version defaults from $agent_makefile" >&2
		exit 1
	}
	[[ -n "$luci_version" && -n "$luci_release" ]] || {
		echo "Unable to resolve version defaults from $luci_makefile" >&2
		exit 1
	}

	if [[ "$agent_version" != "$luci_version" || "$agent_release" != "$luci_release" ]]; then
		echo "Controller package Makefiles disagree on version defaults:" >&2
		echo "  $agent_makefile -> ${agent_version}-r${agent_release}" >&2
		echo "  $luci_makefile -> ${luci_version}-r${luci_release}" >&2
		exit 1
	fi

	if [[ -z "${VERSION:-}" ]]; then
		VERSION="$agent_version"
	fi
	if [[ -z "${RELEASE:-}" ]]; then
		RELEASE="$agent_release"
	fi
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

remove_macos_metadata() {
	local root="$1"

	if [[ ! -d "$root" ]]; then
		return 0
	fi

	find "$root" \( -name '.DS_Store' -o -name '._*' -o -name '__MACOSX' \) -exec rm -rf {} +
}

assert_no_macos_metadata() {
	local root="$1"
	local label="$2"
	local matches

	matches="$(
		find "$root" \( -name '.DS_Store' -o -name '._*' -o -name '__MACOSX' \) -print | head -n 20
	)"
	if [[ -n "$matches" ]]; then
		echo "Refusing to package macOS metadata in $label:" >&2
		echo "$matches" >&2
		return 1
	fi
}

assert_tar_has_no_macos_metadata() {
	local archive="$1"
	local label="$2"
	local matches

	matches="$(
		tar -tzf "$archive" \
			| grep -E '(^|/)(\._[^/]+|\.DS_Store|__MACOSX)(/|$)' \
			| head -n 20 || true
	)"
	if [[ -n "$matches" ]]; then
		echo "Refusing to publish $label with macOS metadata entries:" >&2
		echo "$matches" >&2
		return 1
	fi
}

assert_ipk_has_no_macos_metadata() {
	local output_file="$1"
	local temp_dir

	temp_dir="$(mktemp -d "$SDK_ROOT/tmp/vectra-ipk-inspect.XXXXXX")"
	tar -xzf "$output_file" -C "$temp_dir"
	assert_tar_has_no_macos_metadata "$temp_dir/control.tar.gz" "$(basename "$output_file") control.tar.gz"
	assert_tar_has_no_macos_metadata "$temp_dir/data.tar.gz" "$(basename "$output_file") data.tar.gz"
	rm -rf "$temp_dir"
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
	remove_macos_metadata "$temp_dir/data"
	remove_macos_metadata "$temp_dir/control"
	assert_no_macos_metadata "$temp_dir/data" "$(basename "$output_file") data"
	assert_no_macos_metadata "$temp_dir/control" "$(basename "$output_file") control"
	printf '2.0\n' > "$temp_dir/debian-binary"
	tar --numeric-owner --owner=0 --group=0 -czf "$temp_dir/control.tar.gz" -C "$temp_dir/control" .
	tar --numeric-owner --owner=0 --group=0 -czf "$temp_dir/data.tar.gz" -C "$temp_dir/data" .
	assert_tar_has_no_macos_metadata "$temp_dir/control.tar.gz" "$(basename "$output_file") control.tar.gz"
	assert_tar_has_no_macos_metadata "$temp_dir/data.tar.gz" "$(basename "$output_file") data.tar.gz"
	rm -f "$output_file"
	tar --numeric-owner --owner=0 --group=0 -czf "$output_file" -C "$temp_dir" ./debian-binary ./control.tar.gz ./data.tar.gz
	assert_ipk_has_no_macos_metadata "$output_file"
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

	# Pre-build sanity: the source subdir MUST exist. Without this guard, an
	# incomplete staging tarball (e.g. a stray `rsync --exclude` that swallowed
	# the source subdir) would result in Go building nothing and we'd ship an
	# IPK with no binary — exactly the failure mode that bricked totchto-filiciy
	# during the r27 rollout. Fail loud here so the build aborts instead of
	# silently shipping an empty package.
	if [[ ! -d "$package_root/cmd/vectra-controller-agent" ]]; then
		echo "build_agent_package_manually: missing source directory $package_root/cmd/vectra-controller-agent" >&2
		echo "  (staging tarball is incomplete — see r27 incident notes)" >&2
		exit 1
	fi
	if [[ ! -f "$package_root/cmd/vectra-controller-agent/main.go" ]]; then
		echo "build_agent_package_manually: missing main.go in $package_root/cmd/vectra-controller-agent" >&2
		exit 1
	fi

	(
		cd "$package_root"
		GOTOOLCHAIN=local \
		CGO_ENABLED=0 \
		GOOS=linux \
		GOARCH="$go_arch" \
			go build -trimpath -ldflags="-s -w -X main.controllerAgentRuntimeVersion=${package_version}" \
				-o "$data_dir/usr/sbin/vectra-controller-agent" \
				./cmd/vectra-controller-agent
	)

	# Post-build assertion: Go can succeed with no .go files in the package
	# under unusual SDK toolchain configs, producing an empty output. Make sure
	# the binary actually landed and is non-trivially sized before we package it.
	if [[ ! -f "$data_dir/usr/sbin/vectra-controller-agent" ]]; then
		echo "build_agent_package_manually: go build produced no binary at $data_dir/usr/sbin/vectra-controller-agent" >&2
		exit 1
	fi
	local binary_size
	binary_size="$(stat -c %s "$data_dir/usr/sbin/vectra-controller-agent" 2>/dev/null \
		|| stat -f %z "$data_dir/usr/sbin/vectra-controller-agent")"
	if [[ "${binary_size:-0}" -lt 1048576 ]]; then
		echo "build_agent_package_manually: binary is suspiciously small (${binary_size} bytes < 1 MB)" >&2
		exit 1
	fi
	chmod 0755 "$data_dir/usr/sbin/vectra-controller-agent"

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

	# Final IPK-level verification: extract data.tar.gz and confirm the binary
	# is present, executable, and non-empty. This catches the case where the
	# pre-build / post-build assertions above passed but `package_ipk` or
	# `tar` somehow dropped the file (e.g. macOS metadata stripping, future
	# refactors). Belt and suspenders: any of the three guards firing aborts
	# the build before a binary-less IPK can reach the feed.
	local verify_dir
	verify_dir="$(mktemp -d "$SDK_ROOT/tmp/vectra-agent-verify.XXXXXX")"
	tar -xzf "$output_file" -C "$verify_dir"
	mkdir -p "$verify_dir/data-unpacked"
	tar -xzf "$verify_dir/data.tar.gz" -C "$verify_dir/data-unpacked"
	if [[ ! -f "$verify_dir/data-unpacked/usr/sbin/vectra-controller-agent" ]]; then
		echo "build_agent_package_manually: IPK $output_file does not contain usr/sbin/vectra-controller-agent" >&2
		echo "  data.tar.gz contents:" >&2
		tar -tzf "$verify_dir/data.tar.gz" | sed 's/^/    /' >&2
		exit 1
	fi
	local packed_size
	packed_size="$(stat -c %s "$verify_dir/data-unpacked/usr/sbin/vectra-controller-agent" 2>/dev/null \
		|| stat -f %z "$verify_dir/data-unpacked/usr/sbin/vectra-controller-agent")"
	if [[ "${packed_size:-0}" -lt 1048576 ]]; then
		echo "build_agent_package_manually: binary inside IPK is suspiciously small (${packed_size} bytes < 1 MB)" >&2
		exit 1
	fi
	rm -rf "$verify_dir"

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
VERSION=""
RELEASE=""
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

resolve_package_defaults

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
	printf 'Resolved package version: %s-r%s\n' "$VERSION" "$RELEASE"
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
