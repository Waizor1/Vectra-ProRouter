-- Durable replay protection for partner request signature v2: one row per
-- accepted request id until its expiry. Idempotent (IF NOT EXISTS) like the
-- migrations before it.
CREATE TABLE IF NOT EXISTS "vectra_partner_request_nonce" (
 "request_id" text PRIMARY KEY NOT NULL,
 "fingerprint" text NOT NULL,
 "expires_at" timestamp with time zone NOT NULL
);
--> statement-breakpoint
CREATE INDEX IF NOT EXISTS "vectra_partner_request_nonce_expiry_idx" ON "vectra_partner_request_nonce" ("expires_at");
