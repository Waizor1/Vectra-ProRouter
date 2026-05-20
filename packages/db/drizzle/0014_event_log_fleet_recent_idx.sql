-- Partial index for fleet-wide event_log lookups (router_id IS NULL), e.g. the
-- global-template rollout-history query that filters router_id IS NULL + recent
-- created_at over a 1.5M-row table. Already created CONCURRENTLY on production;
-- this migration is idempotent (IF NOT EXISTS) so the deploy-time run is a no-op
-- there and builds it on any fresh database.
CREATE INDEX IF NOT EXISTS "vectra_event_log_fleet_recent_idx" ON "vectra_event_log" USING btree ("created_at" DESC NULLS LAST) WHERE "router_id" IS NULL;
