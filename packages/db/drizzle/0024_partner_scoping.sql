-- Several partners on the partner API: which partner claimed a router, and
-- which partner a webhook is for. NULL = the original partner, Vectra Connect
-- ("vectra"), so every existing row keeps its meaning. Additive, nullable, no
-- default, idempotent (IF NOT EXISTS) like the migrations before it.

ALTER TABLE "vectra_router" ADD COLUMN IF NOT EXISTS "partner_id" text;
--> statement-breakpoint
ALTER TABLE "vectra_partner_webhook" ADD COLUMN IF NOT EXISTS "partner_id" text;
--> statement-breakpoint
CREATE INDEX IF NOT EXISTS "vectra_router_partner_owner_idx" ON "vectra_router" ("partner_id","owner_ref");
