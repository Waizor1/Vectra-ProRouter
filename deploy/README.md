# Vectra VPS Deploy Runbook

Target baseline:

- Operator host: `router.vectra-pro.net`
- Router API host: `api.vectra-pro.net`
- OS: Ubuntu 24.04 LTS
- Topology: one VPS, two HTTPS hosts, path-aware routing
- Stack: Docker Compose + Caddy + Next.js web app + PostgreSQL
- Artifacts: local VPS disk under `deploy/runtime/artifacts`

## 1. DNS and host preparation

Point both `A` records below to the VPS public IPv4 address before the first deploy:

- `router.vectra-pro.net`
- `api.vectra-pro.net`

On the VPS:

```bash
sudo apt update
sudo apt install -y ca-certificates curl git ufw
curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker "$USER"
newgrp docker
sudo ufw allow OpenSSH
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
```

Prepare an explicit deploy root on the VPS. This runtime is not assumed to be a
Git checkout:

```bash
sudo mkdir -p /opt/vectra-prorouter
sudo chown "$USER":"$USER" /opt/vectra-prorouter
cd /opt/vectra-prorouter
```

From the workstation, sync the release bundle into that deploy root. Example:

```bash
rsync -av --delete ./ root@<vps-host>:/opt/vectra-prorouter/
```

## 2. Environment

Create the root deployment env file from the committed example:

```bash
cp .env.example .env
```

Set at minimum:

- `OPERATOR_DOMAIN=router.vectra-pro.net`
- `ROUTER_API_DOMAIN=api.vectra-pro.net`
- `ACME_EMAIL=<real-admin-email>`
- `POSTGRES_PASSWORD=<strong-random-password>`
- `DATABASE_URL=postgresql://vectra:<same-password>@postgres:5432/vectra`
- `VECTRA_OPERATOR_PASSWORD=<strong-random-password>`
- `VECTRA_SECRETS_KEY=<64 hex chars>`
- `VECTRA_DEFAULT_CONTROL_DOMAIN=https://router.vectra-pro.net`
- `VECTRA_ROUTER_API_BASE_URL=https://api.vectra-pro.net`
- `VECTRA_ARTIFACT_BASE_URL=https://api.vectra-pro.net/artifacts`

Generate a secrets key:

```bash
openssl rand -hex 32
```

## 3. Prepare runtime directories

Create local persistent directories and fix permissions:

```bash
bash deploy/scripts/prepare-runtime.sh
```

This creates:

- `deploy/runtime/postgres`
- `deploy/runtime/backups`
- `deploy/runtime/artifacts`
- `deploy/runtime/caddy/data`
- `deploy/runtime/caddy/config`

## 4. First deploy

Build and start the stack:

```bash
docker compose --env-file .env build web
docker compose --env-file .env up -d
```

What happens:

- PostgreSQL starts on the same VPS.
- The web container applies Drizzle migrations on boot, then starts Next.js.
- Caddy obtains and renews TLS automatically for both pilot hosts.
- `router.vectra-pro.net` serves the operator UI, `tRPC`, and mirrored router API routes.
- `api.vectra-pro.net` serves router-facing REST traffic and `/artifacts/*`.
- A backup sidecar writes compressed PostgreSQL dumps to `deploy/runtime/backups`.
- Base images are pulled from the public ECR mirror to avoid Docker Hub anonymous rate limits on a fresh VPS.

## 5. Update workflow

The stable lane assumes an explicit artifact sync from the workstation or CI
into the existing deploy root, not `git pull` on the VPS.

Recommended sequence:

1. Build the signed OpenWrt feed and any firmware/controller artifacts on the build host.
2. Refresh the AX3000T PassWall bootstrap mirror from the upstream PassWall2 release into a local staging directory:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\Sync-PasswallBootstrapMirror.ps1 `
  -Tag 26.4.5-1 `
  -Arch aarch64_cortex-a53 `
  -OutputDir .\dist\bootstrap\passwall2\26.4.5-1\aarch64_cortex-a53 `
  -IncludeOptional
```

This script downloads the upstream `luci-app-passwall2` package plus the
matching `passwall_packages_ipk_<arch>.zip`, validates that
`luci-app-passwall2` dependencies are fully covered by the mirrored set or the
expected OpenWrt feeds, publishes the exact `.ipk` files needed by the AX3000T
bootstrap lane, and writes `manifest.json` next to them.

3. Sync the updated repository runtime files into the VPS deploy root:

```bash
rsync -av --delete ./ root@<vps-host>:/opt/vectra-prorouter/
```

4. Sync the published artifact directories into the mounted runtime path:

```bash
rsync -av dist/openwrt-feed/stable/aarch64_cortex-a53/ \
  /opt/vectra-prorouter/deploy/runtime/artifacts/openwrt/stable/aarch64_cortex-a53/
```

```bash
mkdir -p /opt/vectra-prorouter/deploy/runtime/artifacts/bootstrap/passwall2/26.4.5-1/aarch64_cortex-a53
rsync -av dist/bootstrap/passwall2/26.4.5-1/aarch64_cortex-a53/ \
  /opt/vectra-prorouter/deploy/runtime/artifacts/bootstrap/passwall2/26.4.5-1/aarch64_cortex-a53/
```

5. Sync artifact metadata in PostgreSQL before redeploy if package versions changed:

```bash
docker compose --env-file .env exec web \
  node ./apps/web/scripts/sync-artifact-metadata.mjs \
  --feed-dir ./deploy/runtime/artifacts/openwrt/stable/aarch64_cortex-a53 \
  --dry-run
```

```bash
docker compose --env-file .env exec web \
  node ./apps/web/scripts/sync-artifact-metadata.mjs \
  --feed-dir ./deploy/runtime/artifacts/openwrt/stable/aarch64_cortex-a53 \
  --apply
```

6. Rebuild and restart the web stack:

```bash
docker compose --env-file .env build web
docker compose --env-file .env up -d
```

Watch logs:

```bash
docker compose --env-file .env logs -f web
docker compose --env-file .env logs -f caddy
docker compose --env-file .env logs -f postgres
```

## 5.1. Optional VPS disk cleanup timer

The repository now includes a conservative maintenance script at
`deploy/scripts/vps-disk-cleanup.sh`. It is intentionally limited to reclaiming
space that is safe to drop automatically:

- Docker builder cache older than 7 days
- unused Docker images older than 7 days
- `apt` package cache
- stale `/tmp/vectra*` and `/tmp/passwall-bootstrap-mirror*` artifacts older than 2 days

It does not touch Docker volumes, PostgreSQL data, active containers, or
deployment backups.

Preview what it would delete:

```bash
bash deploy/scripts/vps-disk-cleanup.sh --dry-run
```

Install the bundled `systemd` timer on the VPS:

```bash
sudo install -d -m 0755 /opt/vectra-prorouter/deploy/systemd
sudo install -m 0755 deploy/scripts/vps-disk-cleanup.sh /opt/vectra-prorouter/deploy/scripts/vps-disk-cleanup.sh
sudo install -m 0644 deploy/systemd/vectra-vps-disk-cleanup.service /etc/systemd/system/vectra-vps-disk-cleanup.service
sudo install -m 0644 deploy/systemd/vectra-vps-disk-cleanup.timer /etc/systemd/system/vectra-vps-disk-cleanup.timer
sudo systemctl daemon-reload
sudo systemctl enable --now vectra-vps-disk-cleanup.timer
```

The bundled schedule is weekly on Sunday at `04:30` VPS local time. On the
current production host this means `04:30 UTC` or `07:30 MSK`.

Inspect the timer and the last run:

```bash
systemctl list-timers vectra-vps-disk-cleanup.timer
journalctl -u vectra-vps-disk-cleanup.service -n 100 --no-pager
```

## 6. Health checks

Local container health:

```bash
docker compose --env-file .env ps
```

External smoke check:

```bash
bash deploy/scripts/smoke-check.sh \
  https://router.vectra-pro.net \
  https://api.vectra-pro.net \
  "$VECTRA_OPERATOR_USER" \
  "$VECTRA_OPERATOR_PASSWORD"
```

Expected results:

- operator `/` returns `200` when an existing session is present, redirects to
  `/login` when app-level auth is required, or returns `401` only for older
  Basic Auth deployments
- operator `/api/health` returns `200`
- router API `/api/router/register` returns a non-`5xx` response on empty test payload
- both `/healthz` endpoints return `200`

Release close-out checklist:

- `index.json`, `Packages`, `Packages.gz`, `Packages.sig`, and `vectra.pub`
  are present under the public OpenWrt feed URL
- `sync-artifact-metadata.mjs --apply` completed without drift
- `https://api.vectra-pro.net/api/health` and `https://router.vectra-pro.net/api/health`
  return `200`
- `https://api.vectra-pro.net/healthz` and `https://router.vectra-pro.net/healthz`
  return `200`
- rollback path is recorded as:
  - restore previous `deploy/runtime/artifacts/...` content
  - re-run metadata sync for the previous version set
  - rebuild/restart the web container

## 7. Artifacts publishing

Anything copied into `deploy/runtime/artifacts` becomes available under:

- `https://api.vectra-pro.net/artifacts/...`
- `https://router.vectra-pro.net/artifacts/...`

The static files alone are not enough for the Vectra update center. The web app
reads `artifacts` and `firmware_manifests` from PostgreSQL, so after publishing
files you must sync metadata into the database.

Examples:

```bash
mkdir -p deploy/runtime/artifacts/controller/stable
cp ./dist/vectra-controller-agent.ipk deploy/runtime/artifacts/controller/stable/
cp ./dist/Packages deploy/runtime/artifacts/controller/stable/
```

### 7.1 Publish OpenWrt feed output

If you built the signed feed with `scripts/build-vectra-openwrt-feed.sh`, copy
the resulting directory into the mounted artifact volume:

```bash
mkdir -p deploy/runtime/artifacts/openwrt/stable/aarch64_cortex-a53
rsync -av dist/openwrt-feed/stable/aarch64_cortex-a53/ \
  deploy/runtime/artifacts/openwrt/stable/aarch64_cortex-a53/
```

Optional firmware files can be placed under a separate static path, for example:

```bash
mkdir -p deploy/runtime/artifacts/firmware/stable/ax3000t-stock
cp ./firmware/openwrt-24.10.3.bin \
  deploy/runtime/artifacts/firmware/stable/ax3000t-stock/
```

### 7.2 Sync artifact metadata and firmware manifests

Start from the committed example file:

```bash
mkdir -p deploy/runtime/artifacts/seed
cp deploy/examples/pilot-artifacts.seed.json \
  deploy/runtime/artifacts/seed/pilot-artifacts.json
```

Edit the copied JSON so that every firmware file path, `downloadPath`, version,
and board/layout tuple matches what you actually published.

Preview the sync plan without DB writes:

```bash
docker compose --env-file .env exec web \
  node ./apps/web/scripts/sync-artifact-metadata.mjs \
  --feed-dir ./deploy/runtime/artifacts/openwrt/stable/aarch64_cortex-a53 \
  --spec ./deploy/runtime/artifacts/seed/pilot-artifacts.json \
  --dry-run
```

Apply the metadata to PostgreSQL:

```bash
docker compose --env-file .env exec web \
  node ./apps/web/scripts/sync-artifact-metadata.mjs \
  --feed-dir ./deploy/runtime/artifacts/openwrt/stable/aarch64_cortex-a53 \
  --spec ./deploy/runtime/artifacts/seed/pilot-artifacts.json \
  --apply
```

What this sync does:

- upserts controller package records from feed `index.json`
- computes SHA-256 from local published files when `file` is provided
- upserts extra artifacts such as guarded firmware images
- upserts `firmware_manifests` and links them to the correct firmware artifact
- does not publish the AX3000T bootstrap mirror automatically; refresh it
  separately with `scripts/Sync-PasswallBootstrapMirror.ps1` and copy the
  result under `deploy/runtime/artifacts/bootstrap/passwall2/<tag>/<arch>/`

Pilot note:

- `validate_firmware` remains a guarded/manual lane. The current agent still
  validates a locally staged image path, so metadata sync is necessary for panel
  visibility and manifest lookup, but it does not replace the router-side image
  staging step yet.

## 8. Backup and restore

Backups are written once per `BACKUP_INTERVAL_SECONDS` and retained for `BACKUP_KEEP_DAYS`.

Run an on-demand backup:

```bash
docker compose --env-file .env run --rm db-backup sh /usr/local/bin/backup-postgres.sh
```

Restore from a dump:

```bash
docker compose --env-file .env run --rm db-backup sh /usr/local/bin/restore-postgres.sh /backups/<dump-file.sql.gz>
```

Restore is destructive for the target database contents. Run it only during a maintenance window.

## 9. Notes and limits

- This deploy layer is production-like for the first pilot, but it is still single-VPS and single-tenant.
- Caddy access logs are emitted in JSON to container stdout.
- The app container uses Docker JSON log rotation, but app log payload structure still depends on the app itself.
- Bootstrap defaults and deployment assets now target the live pilot subdomains.
