ALTER TYPE "public"."vectra_job_type"
ADD VALUE IF NOT EXISTS 'run_rescue_repair';

CREATE TABLE IF NOT EXISTS "vectra_rescue_case" (
  "id" text PRIMARY KEY NOT NULL,
  "router_id" text NOT NULL,
  "trigger" text NOT NULL,
  "state" text DEFAULT 'open' NOT NULL,
  "trigger_details" jsonb DEFAULT '{}'::jsonb NOT NULL,
  "evidence" jsonb DEFAULT '{}'::jsonb NOT NULL,
  "repair_attempts" jsonb DEFAULT '[]'::jsonb NOT NULL,
  "diagnosis" jsonb DEFAULT '{}'::jsonb NOT NULL,
  "started_at" timestamp with time zone DEFAULT now() NOT NULL,
  "last_attempt_at" timestamp with time zone,
  "escalated_at" timestamp with time zone,
  "resolved_at" timestamp with time zone,
  "silenced_until" timestamp with time zone,
  "created_at" timestamp with time zone DEFAULT now() NOT NULL,
  "updated_at" timestamp with time zone DEFAULT now() NOT NULL,
  CONSTRAINT "vectra_rescue_case_router_id_vectra_router_id_fk"
    FOREIGN KEY ("router_id") REFERENCES "public"."vectra_router"("id")
    ON DELETE cascade ON UPDATE no action,
  CONSTRAINT "vectra_rescue_case_trigger_check"
    CHECK ("trigger" IN (
      'direct_mode',
      'proxy_outage',
      'server_unreachable',
      'stale_check_in',
      'foreign_reachability_blocked',
      'telegram_blocked'
    )),
  CONSTRAINT "vectra_rescue_case_state_check"
    CHECK ("state" IN ('open', 'repairing', 'escalated', 'silenced', 'resolved'))
);

CREATE INDEX IF NOT EXISTS "vectra_rescue_case_router_state_idx"
  ON "vectra_rescue_case" ("router_id", "state");

CREATE INDEX IF NOT EXISTS "vectra_rescue_case_started_idx"
  ON "vectra_rescue_case" ("started_at");

CREATE INDEX IF NOT EXISTS "vectra_rescue_case_resolved_idx"
  ON "vectra_rescue_case" ("resolved_at");

CREATE UNIQUE INDEX IF NOT EXISTS "vectra_rescue_case_one_active_router_idx"
  ON "vectra_rescue_case" ("router_id")
  WHERE "state" IN ('open', 'repairing', 'escalated', 'silenced');
