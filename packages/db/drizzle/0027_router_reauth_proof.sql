-- F1: single-use ledger for ed25519 reauth RECOVERY proofs. One row per
-- (router, sha256(signature)) consumed by an accepted recovery registration, so
-- a captured (routerId, proof) tuple cannot replay within the proof's ±skew
-- freshness window. A new table only, nothing existing changes. The panel
-- tolerates its absence (the single-use check fails open and the recovery
-- proceeds), so this can run before or after the deploy. Idempotent
-- (IF NOT EXISTS) like the migrations before it. The expires_at index feeds the
-- global TTL sweep (history-retention) that reaps stale rows independently of
-- per-router activity.
CREATE TABLE IF NOT EXISTS "vectra_router_reauth_proof" (
  "id" text PRIMARY KEY NOT NULL,
  "router_id" text NOT NULL,
  "proof_sha256" text NOT NULL,
  "signed_at" timestamp with time zone NOT NULL,
  "expires_at" timestamp with time zone NOT NULL,
  "consumed_at" timestamp with time zone DEFAULT now() NOT NULL,
  "created_at" timestamp with time zone DEFAULT now() NOT NULL,
  CONSTRAINT "vectra_router_reauth_proof_router_id_vectra_router_id_fk"
    FOREIGN KEY ("router_id") REFERENCES "public"."vectra_router"("id")
    ON DELETE cascade ON UPDATE no action
);
--> statement-breakpoint
CREATE UNIQUE INDEX IF NOT EXISTS "vectra_router_reauth_proof_router_proof_idx"
  ON "vectra_router_reauth_proof" ("router_id", "proof_sha256");
--> statement-breakpoint
CREATE INDEX IF NOT EXISTS "vectra_router_reauth_proof_expires_idx"
  ON "vectra_router_reauth_proof" ("expires_at");
