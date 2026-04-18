# Vectra OpenWrt feed publishing

Date prepared: 2026-04-05

This runbook defines the packaging and signed feed lane for the current Vectra
router packages:

- `vectra-controller-agent`
- `luci-app-vectra-controller`

The target deployment for the first pilot uses the live control-plane hosts:

- operator panel: `https://router.vectra-pro.net`
- router/API host: `https://api.vectra-pro.net`

Artifacts are served from:

- `https://api.vectra-pro.net/artifacts/openwrt/<channel>/<arch>/`
- additional metadata-backed artifacts and firmware files can live under
  `https://api.vectra-pro.net/artifacts/<lane>/...`

## Packaging baseline

- OpenWrt `24.10.x`
- matching OpenWrt SDK for `ipkg-make-index.sh`, `mkhash`, `usign`, and target-arch metadata
- system Go on the Linux build host for the controller binary cross-build
- signed `opkg` feed
- split packages: controller core + LuCI package
- version-aligned release of both packages

## What changed in the repo

- the agent package ships OpenWrt runtime files that auto-seed:
  - `control_url=https://api.vectra-pro.net`
  - `panel_url=https://router.vectra-pro.net`
  - runtime file paths
  - `model`
  - `board_name`
  - `target`
  - `architecture`
  - `openwrt_release`
- `scripts/build-vectra-openwrt-feed.sh` no longer relies on OpenWrt `golang/host`
  or `luci.mk` package compilation for the pilot feed
- instead it now:
  - resolves `CONFIG_TARGET_ARCH_PACKAGES` from the SDK
  - cross-builds the Go agent with `GOTOOLCHAIN=local CGO_ENABLED=0`
  - packages both `.ipk` files manually in the OpenWrt-compatible outer `tar.gz` format
  - uses SDK-host `mkhash`, `usign`, and `ipkg-make-index.sh` to generate and sign the feed
- the LuCI package now includes a `postinst` hook that clears LuCI caches and reloads `rpcd`
- the agent package now includes a `postinst` hook that runs `uci-defaults`, enables the service, and restarts it after install
- the JS-only LuCI package no longer depends on `luci.mk` or the legacy Lua lane;
  its published pilot package is built from `package.mk` semantics and packed
  manually alongside the agent

## Ubuntu 24.04 build prerequisites

On the build host:

```sh
sudo apt update
sudo apt install -y build-essential clang flex gawk gcc-multilib gettext \
  git golang-go libncurses5-dev libssl-dev python3 python3-setuptools rsync unzip zlib1g-dev zstd
```

`usign` is usually easiest to obtain from the matching OpenWrt SDK or from an
existing OpenWrt build environment. The Vectra build script now checks `PATH`
first and then falls back to `staging_dir/host/bin/usign` or
`staging_dir/hostpkg/bin/usign` inside the SDK.

## Verified SDK target

For the current AX3000T stock-layout pilot/stable-prep lane, the live build host
uses the official OpenWrt `24.10.6` Filogic SDK for Linux x86_64:

```text
https://downloads.openwrt.org/releases/24.10.6/targets/mediatek/filogic/openwrt-sdk-24.10.6-mediatek-filogic_gcc-13.3.0_musl.Linux-x86_64.tar.zst
```

This is the correct family for `aarch64_cortex-a53` packages under the
`mediatek/filogic` target and matches the current OpenWrt `24.10.6` pilot
router baseline.

## SDK prep

The current manual packaging lane does not require building the OpenWrt package
feeds, but the SDK still must provide:

```sh
scripts/ipkg-make-index.sh
staging_dir/host/bin/mkhash
staging_dir/host/bin/usign
.config with CONFIG_TARGET_ARCH_PACKAGES
```

Pilot note:

- on the current VPS, official OpenWrt `24.10.x` SDK `golang/host` proved unstable
  during bootstrap, so the feed script intentionally bypasses that lane and uses
  system Go for the controller binary
- the SDK is still the authoritative source for target architecture, `mkhash`,
  `usign`, and feed index generation

## Build and sign the feed

From the repository root:

```sh
scripts/build-vectra-openwrt-feed.sh \
  --sdk-root /opt/openwrt-sdk-24.10.6-mediatek-filogic_gcc-13.3.0_musl.Linux-x86_64 \
  --version 0.1.9 \
  --release 1 \
  --channel stable \
  --create-key
```

Output lands under:

```text
dist/openwrt-feed/stable/<arch>/
|- vectra-controller-agent_<version>-r<release>_<arch>.ipk
|- luci-app-vectra-controller_<version>-r<release>_all.ipk
|- Packages
|- Packages.gz
|- Packages.sig
|- vectra.pub
|- feed.conf
\- index.json
```

## Publish to `api.vectra-pro.net`

Sync the generated directory to the VPS artifact root:

```sh
rsync -av dist/openwrt-feed/stable/aarch64_cortex-a53/ \
  root@72.56.14.52:/opt/vectra-prorouter/deploy/runtime/artifacts/openwrt/stable/aarch64_cortex-a53/
```

The published router feed URL then becomes:

```text
https://api.vectra-pro.net/artifacts/openwrt/stable/aarch64_cortex-a53
```

## Sync metadata into PostgreSQL

The web update center and firmware lookup do not read directly from the static
artifact volume. They read from the `artifacts` and `firmware_manifests` tables.
That means a pilot-ready publish now has two parts:

1. copy the static files under `deploy/runtime/artifacts/...`
2. upsert metadata/manifests into PostgreSQL

Start from the committed example and move it into the mounted runtime area:

```sh
mkdir -p deploy/runtime/artifacts/seed
cp deploy/examples/pilot-artifacts.seed.json \
  deploy/runtime/artifacts/seed/pilot-artifacts.json
```

Then edit the copied JSON for the real firmware filename, version, and
`board_name + target + arch + layout_family` tuple.

Preview the DB sync:

```sh
docker compose --env-file .env exec web \
  node ./apps/web/scripts/sync-artifact-metadata.mjs \
  --feed-dir ./deploy/runtime/artifacts/openwrt/stable/aarch64_cortex-a53 \
  --spec ./deploy/runtime/artifacts/seed/pilot-artifacts.json \
  --dry-run
```

Apply it:

```sh
docker compose --env-file .env exec web \
  node ./apps/web/scripts/sync-artifact-metadata.mjs \
  --feed-dir ./deploy/runtime/artifacts/openwrt/stable/aarch64_cortex-a53 \
  --spec ./deploy/runtime/artifacts/seed/pilot-artifacts.json \
  --apply
```

What the script does:

- parses `index.json` from the signed OpenWrt feed output
- upserts controller package rows into `vectra_artifact`
- computes SHA-256 from published files when a `file` path is provided
- upserts firmware artifact rows and links them into `vectra_firmware_manifest`

This closes the pilot gap where static files existed on disk, but the panel and
router-facing firmware-manifest API still had no corresponding database rows.

## Router install flow

On the router:

```sh
scp vectra.pub root@router:/tmp/vectra.pub
ssh root@router opkg-key add /tmp/vectra.pub
ssh root@router "printf '%s\n' 'src/gz vectra https://api.vectra-pro.net/artifacts/openwrt/stable/aarch64_cortex-a53' >> /etc/opkg/customfeeds.conf"
ssh root@router opkg update
ssh root@router opkg install vectra-controller-agent luci-app-vectra-controller
```

The first-install bootstrap defaults are now split explicitly:

- `control_url=https://api.vectra-pro.net`
- `panel_url=https://router.vectra-pro.net`

The agent uses `control_url` for `/api/router/*` traffic. LuCI shows both
values, and older configs that only contain `panel_url` still work through the
agent-side fallback.

## Sysupgrade persistence and recovery findings

Live findings from the repeated AX3000T stock-layout firmware tests:

- controller identity persistence across `sysupgrade` now depends on the agent
  package shipping `/lib/upgrade/keep.d/vectra-controller`
- that keep rule preserves `/etc/vectra-controller`, which includes:
  - `state.json`
  - device identity
  - issued agent token
  - rescue snapshot
  - retry-safe job journal
- both `sysupgrade -l` and a real `sysupgrade -b /tmp/...tar.gz` now confirm
  that `etc/vectra-controller/state.json` is present in the backup archive
- after a real repeated `sysupgrade` on the live AX3000T, reinstalling the
  controller packages brought the router back under the same `router_id`
  instead of registering a new device

Post-sysupgrade restore helper:

```bash
python3 ./scripts/Invoke-VectraPostSysupgradeRestore.py
```

Default mode is dry-run/read-only. It reads live access only from
`ProRouter/98 Local/`, connects through either native OpenSSH or PuTTY depending on transport parameters,
verifies the certified AX3000T stock-layout tuple, verifies the public Vectra
feed index, and reads the currently installed baseline package versions. It does
not run `opkg update`, firmware writes, `sysupgrade`, reset, clean, or blind
reinstall in dry-run mode.

Use apply mode only during a short LAN-attended maintenance window:

```bash
python3 ./scripts/Invoke-VectraPostSysupgradeRestore.py --apply
```

OpenSSH example:

```bash
python3 ./scripts/Invoke-VectraPostSysupgradeRestore.py \
  --transport OpenSSH \
  --openssh-known-hosts-file ./router-known_hosts \
  --openssh-identity-file ~/.ssh/id_ed25519
```

In `-Apply` mode only, the helper writes the Vectra feed file/key, runs
`opkg update`, checks exact baseline package availability, installs the pinned
baseline package set, and verifies installed versions.

The full pinned baseline is sourced from the local private registry, currently:

- `vectra-controller-agent`
- `luci-app-vectra-controller`
- `luci-app-passwall2`
- `xray-core`
- `geoview`
- optional `sing-box` / `hysteria` only when the registry has exact versions

Important caveat:

- the keep.d fix preserves controller identity and state, but it does **not**
  preserve installed packages across sysupgrade by itself
- after flash, the guarded restore lane still reinstalls:
  - `luci-app-passwall2`
  - `xray-core`
  - `geoview`
  - `vectra-controller-agent`
  - `luci-app-vectra-controller`

Fallback for Vectra packages if the normal signed feed path is unavailable:

```sh
wget -O /tmp/vectra-controller-agent.ipk \
  https://api.vectra-pro.net/artifacts/openwrt/stable/aarch64_cortex-a53/vectra-controller-agent_<version>_aarch64_cortex-a53.ipk
wget -O /tmp/luci-app-vectra-controller.ipk \
  https://api.vectra-pro.net/artifacts/openwrt/stable/aarch64_cortex-a53/luci-app-vectra-controller_<version>_all.ipk
opkg install /tmp/vectra-controller-agent.ipk /tmp/luci-app-vectra-controller.ipk
```

Why this matters:

- after sysupgrade, `opkg update` may still succeed for the Vectra feed, yet
  `opkg install vectra-controller-agent luci-app-vectra-controller` can fail on
  the actual package download step through the embedded `wget`
- direct `wget -> /tmp/*.ipk -> opkg install /tmp/*.ipk` remains the guarded
  fallback when the feed install path is unhealthy

## Release rules

- code change in the Go agent or LuCI app: bump `--version`
- packaging-only change: bump `--release`
- publish both packages together when UCI schema or LuCI presentation changes

## Current limitations

- the build script expects a Linux host with system Go plus a matching OpenWrt SDK
- the repo does not yet have CI automation for nightly feed publication
- router-side self-update logic is still separate from this feed publishing lane
- official OpenWrt `24.10.x` SDK `golang/host` remains unstable on the current VPS, so the pilot lane intentionally uses manual agent packaging instead of SDK Go package compilation
- a pure-SDK LuCI compile lane is still not release-ready on the tested host: the
  current SDK hits missing `ucode/module.h` headers when `lucihttp` falls back to
  the ucode branch, so the published pilot feed remains on the manual packager
- firmware validation remains guarded/manual even though the agent now supports
  artifact staging and `sysupgrade -T`, because the post-flash package restore
  lane is still manual
