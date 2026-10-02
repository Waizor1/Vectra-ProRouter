-- Connect partner actions (ADR-0006): typed owner-bound router actions and the
-- owner-sealed Wi-Fi credentials a check-in may carry. Additive and nullable:
-- every existing snapshot row stays NULL. Idempotent (IF NOT EXISTS) like the
-- migrations before it.
ALTER TYPE "public"."vectra_job_type" ADD VALUE IF NOT EXISTS 'connect_router_action';
--> statement-breakpoint
ALTER TABLE "vectra_router_inventory_snapshot" ADD COLUMN IF NOT EXISTS "connect_secret_ciphertext" text;
