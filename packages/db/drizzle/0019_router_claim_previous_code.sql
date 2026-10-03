-- ADR-0006: honour the claim code's grace period. A router replaces its code
-- every 10 minutes and promises the replaced one stays valid 2 minutes longer;
-- keeping only the latest code killed it at the next check-in (~45 s). The
-- previous code is kept here, sealed like the current one, with its own expiry.
-- Additive and nullable with no default: every existing row stays NULL.
-- Idempotent (IF NOT EXISTS) like the migrations before it.

ALTER TABLE "vectra_router" ADD COLUMN IF NOT EXISTS "previous_claim_code_hash" text;
--> statement-breakpoint
ALTER TABLE "vectra_router" ADD COLUMN IF NOT EXISTS "previous_claim_expires_at" timestamp with time zone;
--> statement-breakpoint
CREATE INDEX IF NOT EXISTS "vectra_router_previous_claim_code_hash_idx"
  ON "vectra_router" USING btree ("previous_claim_code_hash");
