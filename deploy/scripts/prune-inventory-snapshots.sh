#!/usr/bin/env bash
# Retention for vectra_router_inventory_snapshot.
#
# Router check-ins append one row per router roughly every minute, but every
# reader only needs the latest snapshot per router. Left unbounded the table grew
# to ~700k rows / 1.7 GB and made the fleet monitoring query (and the web
# container) fall over. This drops snapshots older than RETENTION_DAYS while
# always keeping the most recent snapshot per router -- an offline router can have
# an old "latest" we must not delete.
#
# The set of "keep" ids (latest per router) is computed once via indexed per-row
# lookups, then old rows are deleted in committed, created_at-indexed batches so
# no batch does a full-table scan, the work commits incrementally, and concurrent
# check-in inserts are not blocked. The steady-state daily delta is one batch.
#
# A one-time VACUUM FULL is needed once after the first large prune to return the
# freed space to the OS (brief ACCESS EXCLUSIVE lock); afterwards autovacuum
# keeps the steady-state table compact.
set -euo pipefail

RETENTION_DAYS="${RETENTION_DAYS:-7}"
BATCH_SIZE="${BATCH_SIZE:-25000}"
DEPLOY_ROOT="${DEPLOY_ROOT:-/opt/vectra-prorouter}"
ENV_FILE="${ENV_FILE:-$DEPLOY_ROOT/.env}"
COMPOSE_FILE="${COMPOSE_FILE:-$DEPLOY_ROOT/docker-compose.yml}"

cd "$DEPLOY_ROOT"

psql_tuples() {
  docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" exec -T postgres \
    sh -lc 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 -P pager=off -tA'
}

echo "Vectra inventory snapshot retention"
echo "  retention: ${RETENTION_DAYS} days (latest snapshot per router always kept)"
echo "  batch:     ${BATCH_SIZE}"

# Compute the keep set once: latest snapshot id per router (indexed lookups).
# This set is intentionally frozen for the whole delete loop: a frozen set can
# only ever over-keep (e.g. an offline router that checks in mid-run), never
# delete a router's latest. Do NOT "optimize" this into a correlated subquery
# inside the DELETE -- that reintroduces the full-table scan this script avoids.
keep_ids=$(psql_tuples <<'SQL'
SELECT string_agg(quote_literal(id), ',')
FROM (
  SELECT (
    SELECT s.id FROM vectra_router_inventory_snapshot s
    WHERE s.router_id = r.id
    ORDER BY s.created_at DESC
    LIMIT 1
  ) AS id
  FROM vectra_router r
) latest
WHERE id IS NOT NULL;
SQL
)
keep_ids="${keep_ids:-''}"
echo "  keep ids:  $(printf '%s' "$keep_ids" | awk -F, '{print NF}') latest snapshots"

total=0
while :; do
  n=$(psql_tuples <<SQL
SET statement_timeout='3min';
WITH victims AS (
  SELECT id
  FROM vectra_router_inventory_snapshot
  WHERE created_at < now() - interval '${RETENTION_DAYS} days'
    AND id NOT IN (${keep_ids})
  ORDER BY created_at
  LIMIT ${BATCH_SIZE}
),
del AS (
  DELETE FROM vectra_router_inventory_snapshot s
  USING victims v
  WHERE s.id = v.id
  RETURNING 1
)
SELECT count(*) FROM del;
SQL
)
  n="${n//[^0-9]/}"
  n="${n:-0}"
  total=$((total + n))
  echo "  deleted batch=${n} total=${total}"
  [ "$n" -gt 0 ] || break
done

echo "Deleted ${total} old snapshot rows; refreshing planner stats."
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" exec -T postgres \
  sh -lc 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -P pager=off -c "ANALYZE vectra_router_inventory_snapshot;"'
echo "Retention pass complete."
