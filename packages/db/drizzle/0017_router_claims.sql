-- ADR-0006: a customer links a router to their Vectra account by QR.
-- Additive only: every new router column is nullable with no default, so the
-- live fleet rows are untouched (owner NULL = not linked, as today). Idempotent
-- (IF NOT EXISTS) like the migrations before it.

ALTER TABLE "vectra_router" ADD COLUMN IF NOT EXISTS "owner_ref" text;
--> statement-breakpoint
ALTER TABLE "vectra_router" ADD COLUMN IF NOT EXISTS "owner_label" text;
--> statement-breakpoint
ALTER TABLE "vectra_router" ADD COLUMN IF NOT EXISTS "claim_code_hash" text;
--> statement-breakpoint
ALTER TABLE "vectra_router" ADD COLUMN IF NOT EXISTS "claim_expires_at" timestamp with time zone;
--> statement-breakpoint
ALTER TABLE "vectra_router" ADD COLUMN IF NOT EXISTS "claimed_at" timestamp with time zone;
--> statement-breakpoint
CREATE INDEX IF NOT EXISTS "vectra_router_claim_code_hash_idx"
  ON "vectra_router" USING btree ("claim_code_hash");
--> statement-breakpoint

-- Stored answers of the partner API, keyed by the caller's Idempotency-Key.
CREATE TABLE IF NOT EXISTS "vectra_partner_idempotency_key" (
  "key" text PRIMARY KEY NOT NULL,
  "request_hash" text NOT NULL,
  "status_code" integer NOT NULL,
  "response" jsonb NOT NULL,
  "created_at" timestamp with time zone DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE INDEX IF NOT EXISTS "vectra_partner_idempotency_key_created_idx"
  ON "vectra_partner_idempotency_key" USING btree ("created_at");
--> statement-breakpoint

-- Outbox of webhooks to the Vectra backend (router.claimed/ready/failed).
CREATE TABLE IF NOT EXISTS "vectra_partner_webhook" (
  "id" text PRIMARY KEY NOT NULL,
  "event" text NOT NULL,
  "router_id" text,
  "payload" jsonb NOT NULL,
  "attempts" integer DEFAULT 0 NOT NULL,
  "next_attempt_at" timestamp with time zone,
  "delivered_at" timestamp with time zone,
  "last_status" integer,
  "last_error" text,
  "created_at" timestamp with time zone DEFAULT now() NOT NULL,
  CONSTRAINT "vectra_partner_webhook_router_id_vectra_router_id_fk"
    FOREIGN KEY ("router_id") REFERENCES "public"."vectra_router"("id")
    ON DELETE set null ON UPDATE no action
);
--> statement-breakpoint
CREATE INDEX IF NOT EXISTS "vectra_partner_webhook_due_idx"
  ON "vectra_partner_webhook" USING btree ("next_attempt_at")
  WHERE "delivered_at" is null;
--> statement-breakpoint
CREATE INDEX IF NOT EXISTS "vectra_partner_webhook_router_idx"
  ON "vectra_partner_webhook" USING btree ("router_id");
