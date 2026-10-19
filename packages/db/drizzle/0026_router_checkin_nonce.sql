-- A2 check-in nonce single-use ledger: one row per (router, nonce) consumed by
-- an accepted signed check-in, so a captured (nonce, signature, body) tuple
-- cannot replay within the 24h nonce window. A new table only, nothing existing
-- changes. The panel tolerates its absence (the replay check fails open and the
-- check-in proceeds), so this can run before or after the deploy. Idempotent
-- (IF NOT EXISTS) like the migrations before it.
CREATE TABLE IF NOT EXISTS "vectra_router_checkin_nonce" (
  "router_id" text NOT NULL,
  "nonce_hash" text NOT NULL,
  "expires_at" timestamp with time zone NOT NULL,
  CONSTRAINT "vectra_router_checkin_nonce_router_id_nonce_hash_pk"
    PRIMARY KEY ("router_id", "nonce_hash"),
  CONSTRAINT "vectra_router_checkin_nonce_router_id_vectra_router_id_fk"
    FOREIGN KEY ("router_id") REFERENCES "public"."vectra_router"("id")
    ON DELETE cascade ON UPDATE no action
);
--> statement-breakpoint
CREATE INDEX IF NOT EXISTS "vectra_router_checkin_nonce_expires_idx"
  ON "vectra_router_checkin_nonce" ("router_id", "expires_at");
