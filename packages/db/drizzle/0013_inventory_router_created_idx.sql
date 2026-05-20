-- Composite index for "latest snapshot per router" lookups.
-- Already created on production via CREATE INDEX CONCURRENTLY; this migration is
-- idempotent (IF NOT EXISTS) so the deploy-time run is a no-op there and builds
-- the index on any fresh database.
CREATE INDEX IF NOT EXISTS "vectra_router_inventory_router_created_idx" ON "vectra_router_inventory_snapshot" USING btree ("router_id","created_at" DESC NULLS LAST);
