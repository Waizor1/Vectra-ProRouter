-- ADR-0006: the explicit "released" signal after a partner unbind. Set when the
-- Vectra account unlinks a router, cleared when it is claimed again. Additive
-- and nullable with no default: every existing router row stays NULL (never
-- released). Idempotent (IF NOT EXISTS) like the migrations before it.

ALTER TABLE "vectra_router" ADD COLUMN IF NOT EXISTS "released_at" timestamp with time zone;
