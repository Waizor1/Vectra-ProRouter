#!/usr/bin/env bash
set -euo pipefail

export COPYFILE_DISABLE=1

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../.." && pwd)"
OUTPUT_DIR="$REPO_ROOT/apps/web/public/install-helper"
WORK_DIR="$(mktemp -d)"
HELPER_VERSION="${HELPER_VERSION:-0.1.0}"
MACOS_SIGN_IDENTITY="${MACOS_SIGN_IDENTITY:-}"
MACOS_NOTARY_PROFILE="${MACOS_NOTARY_PROFILE:-}"

cleanup() {
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

mkdir -p "$OUTPUT_DIR"
rm -f "$OUTPUT_DIR"/*.zip

write_readme() {
  local path="$1"
  local starter="$2"
  local signature_note="$3"

  cat >"$path" <<EOF
1. Extract this zip.
2. Start the helper with $starter
3. Keep the helper running.
4. Return to https://router.vectra-pro.net/install and press the main button.

$signature_note
EOF
}

build_binary() {
  local output_path="$1"
  local goos="$2"
  local goarch="$3"

  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
    go build -o "$output_path" .
}

zip_directory() {
  local source_dir="$1"
  local output_zip="$2"

  find "$source_dir" -name '._*' -delete
  (
    cd "$source_dir"
    zip -X -qr "$output_zip" .
  )
}

sign_and_notarize_macos_app() {
  local app_bundle="$1"

  if [ -z "$MACOS_SIGN_IDENTITY" ]; then
    return 0
  fi

  if ! command -v codesign >/dev/null 2>&1; then
    echo "codesign is required when MACOS_SIGN_IDENTITY is set." >&2
    exit 1
  fi

  codesign --force --deep --options runtime --sign "$MACOS_SIGN_IDENTITY" "$app_bundle"
  codesign --verify --deep --strict --verbose=2 "$app_bundle"

  if [ -z "$MACOS_NOTARY_PROFILE" ]; then
    return 0
  fi

  if ! command -v xcrun >/dev/null 2>&1; then
    echo "xcrun is required when MACOS_NOTARY_PROFILE is set." >&2
    exit 1
  fi

  local notary_zip="$WORK_DIR/$(basename "$app_bundle")-notary.zip"
  rm -f "$notary_zip"
  ditto -c -k --keepParent "$app_bundle" "$notary_zip"
  xcrun notarytool submit "$notary_zip" \
    --keychain-profile "$MACOS_NOTARY_PROFILE" \
    --wait
  xcrun stapler staple "$app_bundle"
}

build_macos_archive() {
  local package_id="$1"
  local goarch="$2"

  local package_dir="$WORK_DIR/$package_id"
  local app_name="Vectra Install Helper.app"
  local app_bundle="$package_dir/$app_name"
  local contents_dir="$app_bundle/Contents"
  local macos_dir="$contents_dir/MacOS"
  local starter_name="Vectra Install Helper"
  local binary_name="vectra-install-helper"
  local signature_note="This build is unsigned in the current workspace unless Developer ID signing and notarization are configured."

  mkdir -p "$macos_dir"
  build_binary "$macos_dir/$binary_name" "darwin" "$goarch"

  cat >"$macos_dir/$starter_name" <<'EOF'
#!/bin/sh
set -eu
DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
cd "$DIR"
exec "./vectra-install-helper"
EOF
  chmod +x "$macos_dir/$starter_name" "$macos_dir/$binary_name"

  cat >"$contents_dir/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key>
  <string>en</string>
  <key>CFBundleDisplayName</key>
  <string>Vectra Install Helper</string>
  <key>CFBundleExecutable</key>
  <string>$starter_name</string>
  <key>CFBundleIdentifier</key>
  <string>net.vectra.install-helper</string>
  <key>CFBundleInfoDictionaryVersion</key>
  <string>6.0</string>
  <key>CFBundleName</key>
  <string>Vectra Install Helper</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleShortVersionString</key>
  <string>$HELPER_VERSION</string>
  <key>CFBundleVersion</key>
  <string>$HELPER_VERSION</string>
  <key>LSMinimumSystemVersion</key>
  <string>12.0</string>
  <key>NSHighResolutionCapable</key>
  <true/>
</dict>
</plist>
EOF

  sign_and_notarize_macos_app "$app_bundle"

  if [ -n "$MACOS_SIGN_IDENTITY" ] && [ -n "$MACOS_NOTARY_PROFILE" ]; then
    signature_note="This build is signed with Developer ID and notarized for macOS Gatekeeper."
  elif [ -n "$MACOS_SIGN_IDENTITY" ]; then
    signature_note="This build is signed with Developer ID, but notarization was not requested in this workspace."
  fi

  write_readme "$package_dir/README.txt" "$app_name" "$signature_note"
  zip_directory "$package_dir" "$OUTPUT_DIR/$package_id.zip"
}

build_windows_archive() {
  local package_id="$1"

  local package_dir="$WORK_DIR/$package_id"
  mkdir -p "$package_dir"
  build_binary "$package_dir/vectra-install-helper.exe" "windows" "amd64"

  cat >"$package_dir/start-vectra-install-helper.cmd" <<'EOF'
@echo off
cd /d "%~dp0"
vectra-install-helper.exe
pause
EOF

  write_readme \
    "$package_dir/README.txt" \
    "start-vectra-install-helper.cmd" \
    "Windows signing is not part of this helper script yet."
  zip_directory "$package_dir" "$OUTPUT_DIR/$package_id.zip"
}

build_linux_archive() {
  local package_id="$1"

  local package_dir="$WORK_DIR/$package_id"
  mkdir -p "$package_dir"
  build_binary "$package_dir/vectra-install-helper" "linux" "amd64"

  cat >"$package_dir/start-vectra-install-helper.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$DIR"
chmod +x ./vectra-install-helper
./vectra-install-helper
EOF
  chmod +x "$package_dir/start-vectra-install-helper.sh" "$package_dir/vectra-install-helper"

  write_readme \
    "$package_dir/README.txt" \
    "start-vectra-install-helper.sh" \
    "Linux signing is not part of this helper script yet."
  zip_directory "$package_dir" "$OUTPUT_DIR/$package_id.zip"
}

cd "$SCRIPT_DIR"

build_macos_archive "vectra-install-helper-macos-apple-silicon" "arm64"
build_macos_archive "vectra-install-helper-macos-intel" "amd64"
build_windows_archive "vectra-install-helper-windows-x64"
build_linux_archive "vectra-install-helper-linux-x64"

if [ -n "$MACOS_SIGN_IDENTITY" ]; then
  echo "Built helper release artifacts into $OUTPUT_DIR with macOS signing."
else
  echo "Built helper release artifacts into $OUTPUT_DIR without macOS signing."
  echo "To produce Gatekeeper-friendly macOS artifacts, set MACOS_SIGN_IDENTITY and MACOS_NOTARY_PROFILE."
fi
