CREATE TABLE IF NOT EXISTS "vectra_router_onboarding_profile" (
  "id" text PRIMARY KEY NOT NULL,
  "router_id" text NOT NULL,
  "enabled" boolean DEFAULT true NOT NULL,
  "target_hostname" text,
  "display_name" text,
  "subscription_secret_ciphertext" text,
  "subscription_url_hash" text,
  "subscription_remark" text,
  "baseline" text DEFAULT 'standard-non-hh' NOT NULL,
  "runtime_policy" text DEFAULT 'auto-minimal-passwall-xray' NOT NULL,
  "verify_policy" text DEFAULT 'route-smoke' NOT NULL,
  "notes" text,
  "created_at" timestamp with time zone DEFAULT now() NOT NULL,
  "updated_at" timestamp with time zone DEFAULT now() NOT NULL,
  CONSTRAINT "vectra_router_onboarding_profile_router_id_vectra_router_id_fk"
    FOREIGN KEY ("router_id") REFERENCES "public"."vectra_router"("id")
    ON DELETE cascade ON UPDATE no action,
  CONSTRAINT "vectra_router_onboarding_profile_baseline_check"
    CHECK ("baseline" IN ('standard-non-hh', 'hh-exempt', 'subscription-only')),
  CONSTRAINT "vectra_router_onboarding_profile_runtime_policy_check"
    CHECK ("runtime_policy" IN ('auto-minimal-passwall-xray', 'controller-only')),
  CONSTRAINT "vectra_router_onboarding_profile_verify_policy_check"
    CHECK ("verify_policy" IN ('route-smoke', 'services-only'))
);

CREATE UNIQUE INDEX IF NOT EXISTS "vectra_router_onboarding_profile_router_idx"
  ON "vectra_router_onboarding_profile" ("router_id");

CREATE INDEX IF NOT EXISTS "vectra_router_onboarding_profile_enabled_idx"
  ON "vectra_router_onboarding_profile" ("enabled");

CREATE TABLE IF NOT EXISTS "vectra_router_onboarding_run" (
  "id" text PRIMARY KEY NOT NULL,
  "router_id" text NOT NULL,
  "profile_id" text NOT NULL,
  "state" text DEFAULT 'created' NOT NULL,
  "status" text DEFAULT 'running' NOT NULL,
  "attempt" integer DEFAULT 0 NOT NULL,
  "last_job_id" text,
  "active_revision_id" text,
  "last_error" text,
  "next_run_after" timestamp with time zone,
  "completed_at" timestamp with time zone,
  "created_at" timestamp with time zone DEFAULT now() NOT NULL,
  "updated_at" timestamp with time zone DEFAULT now() NOT NULL,
  CONSTRAINT "vectra_router_onboarding_run_router_id_vectra_router_id_fk"
    FOREIGN KEY ("router_id") REFERENCES "public"."vectra_router"("id")
    ON DELETE cascade ON UPDATE no action,
  CONSTRAINT "vectra_router_onboarding_run_profile_id_vectra_router_onboarding_profile_id_fk"
    FOREIGN KEY ("profile_id") REFERENCES "public"."vectra_router_onboarding_profile"("id")
    ON DELETE cascade ON UPDATE no action,
  CONSTRAINT "vectra_router_onboarding_run_last_job_id_vectra_job_id_fk"
    FOREIGN KEY ("last_job_id") REFERENCES "public"."vectra_job"("id")
    ON DELETE set null ON UPDATE no action,
  CONSTRAINT "vectra_router_onboarding_run_active_revision_id_vectra_passwall_desired_revision_id_fk"
    FOREIGN KEY ("active_revision_id") REFERENCES "public"."vectra_passwall_desired_revision"("id")
    ON DELETE set null ON UPDATE no action,
  CONSTRAINT "vectra_router_onboarding_run_state_check"
    CHECK ("state" IN (
      'created',
      'preflight',
      'request_initial_import',
      'approve_initial_import',
      'rename_router',
      'ensure_runtime',
      'apply_subscription',
      'refresh_subscription',
      'resolve_route_baseline',
      'apply_route_baseline',
      'verify_runtime',
      'repair_runtime',
      'final_reimport',
      'done'
    )),
  CONSTRAINT "vectra_router_onboarding_run_status_check"
    CHECK ("status" IN ('running', 'waiting', 'blocked', 'failed', 'done', 'paused'))
);

CREATE INDEX IF NOT EXISTS "vectra_router_onboarding_run_router_status_idx"
  ON "vectra_router_onboarding_run" ("router_id", "status");

CREATE INDEX IF NOT EXISTS "vectra_router_onboarding_run_profile_idx"
  ON "vectra_router_onboarding_run" ("profile_id");

CREATE UNIQUE INDEX IF NOT EXISTS "vectra_router_onboarding_run_one_active_router_idx"
  ON "vectra_router_onboarding_run" ("router_id")
  WHERE "status" IN ('running', 'waiting', 'blocked', 'failed', 'paused');
