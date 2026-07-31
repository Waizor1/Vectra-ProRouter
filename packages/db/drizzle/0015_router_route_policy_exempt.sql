-- Per-router route-policy exemption, operator-controlled.
--
-- Nullable with no default on purpose: NULL means "no operator opinion", and
-- only then does the seed exemption list in fleet-route-policy.ts apply. An
-- explicit TRUE/FALSE overrides that list in both directions, so adding or
-- retiring an exemption stops requiring a controller rebuild plus a fleet-wide
-- controller rollout.
--
-- Idempotent so a re-run against an already-migrated database is a no-op.
ALTER TABLE "vectra_router" ADD COLUMN IF NOT EXISTS "route_policy_exempt" boolean;
--> statement-breakpoint
ALTER TABLE "vectra_router" ADD COLUMN IF NOT EXISTS "route_policy_exempt_reason" text;
