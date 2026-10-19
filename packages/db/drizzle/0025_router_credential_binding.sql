-- A3 duplicate-credential detection: one row per router with the device its
-- current credential is bound to and where its last accepted check-in came
-- from. A new table only, nothing existing changes. The panel tolerates its
-- absence (detection logs a warning and the check-in proceeds), so this can
-- run before or after the deploy. Idempotent (IF NOT EXISTS) like the
-- migrations before it.
CREATE TABLE IF NOT EXISTS "vectra_router_credential_binding" (
  "router_id" text PRIMARY KEY NOT NULL,
  "credential_id" text NOT NULL,
  "bound_device_binding" text,
  "bound_at" timestamp with time zone DEFAULT now() NOT NULL,
  "last_device_binding" text,
  "last_source_cluster" text,
  "last_seen_at" timestamp with time zone,
  "known_source_clusters" jsonb DEFAULT '[]'::jsonb NOT NULL,
  "updated_at" timestamp with time zone DEFAULT now() NOT NULL,
  CONSTRAINT "vectra_router_credential_binding_router_id_vectra_router_id_fk"
    FOREIGN KEY ("router_id") REFERENCES "public"."vectra_router"("id")
    ON DELETE cascade ON UPDATE no action
);
