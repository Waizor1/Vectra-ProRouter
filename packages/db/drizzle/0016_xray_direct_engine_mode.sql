-- Additive xray-direct engine support (Vectra Controller Pro).
-- Everything here defaults to the legacy passwall path so the 18 live routers
-- are unaffected. No existing column, value or default is changed.

-- New job types for the xray-direct engine.
ALTER TYPE "public"."vectra_job_type"
ADD VALUE IF NOT EXISTS 'apply_xray_config';
--> statement-breakpoint
ALTER TYPE "public"."vectra_job_type"
ADD VALUE IF NOT EXISTS 'reload_xray_outbound';
--> statement-breakpoint
ALTER TYPE "public"."vectra_job_type"
ADD VALUE IF NOT EXISTS 'refresh_xray_subscriptions';
--> statement-breakpoint
ALTER TYPE "public"."vectra_job_type"
ADD VALUE IF NOT EXISTS 'update_xray_assets';
--> statement-breakpoint

-- New artifact type for the standalone Xray binary shipped with the pro feed.
ALTER TYPE "public"."vectra_artifact_type"
ADD VALUE IF NOT EXISTS 'xray_binary';
--> statement-breakpoint

-- engineMode discriminator enum + columns. Default 'passwall' keeps every
-- existing row on the live PassWall2 path.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_type WHERE typname = 'vectra_engine_mode'
  ) THEN
    CREATE TYPE "public"."vectra_engine_mode" AS ENUM ('passwall', 'xray-direct');
  END IF;
END
$$;
--> statement-breakpoint
ALTER TABLE "vectra_router"
  ADD COLUMN IF NOT EXISTS "engine_mode" "public"."vectra_engine_mode" DEFAULT 'passwall' NOT NULL;
--> statement-breakpoint
ALTER TABLE "vectra_passwall_desired_revision"
  ADD COLUMN IF NOT EXISTS "engine_mode" "public"."vectra_engine_mode" DEFAULT 'passwall' NOT NULL;
--> statement-breakpoint
ALTER TABLE "vectra_passwall_applied_revision"
  ADD COLUMN IF NOT EXISTS "engine_mode" "public"."vectra_engine_mode" DEFAULT 'passwall' NOT NULL;
