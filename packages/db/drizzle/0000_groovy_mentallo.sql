DO $$ BEGIN
 CREATE TYPE "public"."vectra_router_status" AS ENUM('pending', 'active', 'offline', 'direct', 'rescue', 'disabled');
EXCEPTION
 WHEN duplicate_object THEN null;
END $$;
--> statement-breakpoint
DO $$ BEGIN
 CREATE TYPE "public"."vectra_credential_type" AS ENUM('bootstrap', 'agent_token');
EXCEPTION
 WHEN duplicate_object THEN null;
END $$;
--> statement-breakpoint
DO $$ BEGIN
 CREATE TYPE "public"."vectra_job_type" AS ENUM(
  'apply_passwall_config',
  'refresh_subscriptions',
  'refresh_rules',
  'update_controller',
  'update_passwall_packages',
  'validate_firmware',
  'enter_direct_mode',
  'reconnect'
 );
EXCEPTION
 WHEN duplicate_object THEN null;
END $$;
--> statement-breakpoint
DO $$ BEGIN
 CREATE TYPE "public"."vectra_job_state" AS ENUM('queued', 'delivered', 'running', 'succeeded', 'failed', 'cancelled');
EXCEPTION
 WHEN duplicate_object THEN null;
END $$;
--> statement-breakpoint
DO $$ BEGIN
 CREATE TYPE "public"."vectra_job_result_status" AS ENUM('accepted', 'success', 'failure');
EXCEPTION
 WHEN duplicate_object THEN null;
END $$;
--> statement-breakpoint
DO $$ BEGIN
 CREATE TYPE "public"."vectra_artifact_type" AS ENUM('controller', 'passwall_package', 'passwall_bundle', 'firmware');
EXCEPTION
 WHEN duplicate_object THEN null;
END $$;
--> statement-breakpoint
DO $$ BEGIN
 CREATE TYPE "public"."vectra_controller_channel" AS ENUM('stable', 'beta');
EXCEPTION
 WHEN duplicate_object THEN null;
END $$;
--> statement-breakpoint
DO $$ BEGIN
 CREATE TYPE "public"."vectra_incident_type" AS ENUM(
  'proxy_outage',
  'server_unreachable',
  'subscription_degraded',
  'entered_direct_mode',
  'recovered'
 );
EXCEPTION
 WHEN duplicate_object THEN null;
END $$;
--> statement-breakpoint
DO $$ BEGIN
 CREATE TYPE "public"."vectra_incident_state" AS ENUM('open', 'resolved');
EXCEPTION
 WHEN duplicate_object THEN null;
END $$;
--> statement-breakpoint
DO $$ BEGIN
 CREATE TYPE "public"."vectra_severity" AS ENUM('info', 'warning', 'critical');
EXCEPTION
 WHEN duplicate_object THEN null;
END $$;
--> statement-breakpoint
CREATE TABLE "vectra_artifact" (
	"id" text PRIMARY KEY NOT NULL,
	"type" "vectra_artifact_type" NOT NULL,
	"channel" "vectra_controller_channel" DEFAULT 'stable' NOT NULL,
	"name" text NOT NULL,
	"version" text NOT NULL,
	"architecture" text,
	"board_name" text,
	"layout_family" text,
	"download_url" text NOT NULL,
	"checksum_sha256" text NOT NULL,
	"signature_url" text,
	"metadata" jsonb DEFAULT '{}'::jsonb NOT NULL,
	"published_at" timestamp with time zone DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE TABLE "vectra_event_log" (
	"id" text PRIMARY KEY NOT NULL,
	"router_id" text,
	"type" text NOT NULL,
	"severity" "vectra_severity" DEFAULT 'info' NOT NULL,
	"message" text NOT NULL,
	"metadata" jsonb DEFAULT '{}'::jsonb NOT NULL,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE TABLE "vectra_firmware_manifest" (
	"id" text PRIMARY KEY NOT NULL,
	"board_name" text NOT NULL,
	"target" text NOT NULL,
	"architecture" text NOT NULL,
	"layout_family" text NOT NULL,
	"channel" "vectra_controller_channel" DEFAULT 'stable' NOT NULL,
	"version" text NOT NULL,
	"validation_command" text DEFAULT 'sysupgrade -T /tmp/firmware.bin' NOT NULL,
	"artifact_id" text NOT NULL,
	"rollout_policy" jsonb DEFAULT '{}'::jsonb NOT NULL,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE TABLE "vectra_health_incident" (
	"id" text PRIMARY KEY NOT NULL,
	"router_id" text NOT NULL,
	"type" "vectra_incident_type" NOT NULL,
	"state" "vectra_incident_state" DEFAULT 'open' NOT NULL,
	"reason" text NOT NULL,
	"metadata" jsonb DEFAULT '{}'::jsonb NOT NULL,
	"opened_at" timestamp with time zone DEFAULT now() NOT NULL,
	"resolved_at" timestamp with time zone
);
--> statement-breakpoint
CREATE TABLE "vectra_job_result" (
	"id" text PRIMARY KEY NOT NULL,
	"job_id" text NOT NULL,
	"router_id" text NOT NULL,
	"status" "vectra_job_result_status" NOT NULL,
	"payload" jsonb DEFAULT '{}'::jsonb NOT NULL,
	"reported_at" timestamp with time zone DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE TABLE "vectra_job" (
	"id" text PRIMARY KEY NOT NULL,
	"router_id" text NOT NULL,
	"type" "vectra_job_type" NOT NULL,
	"state" "vectra_job_state" DEFAULT 'queued' NOT NULL,
	"payload" jsonb DEFAULT '{}'::jsonb NOT NULL,
	"desired_revision_id" text,
	"dedupe_key" text,
	"deliver_after" timestamp with time zone,
	"delivered_at" timestamp with time zone,
	"completed_at" timestamp with time zone,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE TABLE "vectra_passwall_applied_revision" (
	"id" text PRIMARY KEY NOT NULL,
	"router_id" text NOT NULL,
	"desired_revision_id" text,
	"result" text DEFAULT 'applied' NOT NULL,
	"config" jsonb DEFAULT 'null'::jsonb,
	"raw_snapshot" jsonb DEFAULT 'null'::jsonb,
	"reported_at" timestamp with time zone DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE TABLE "vectra_passwall_desired_revision" (
	"id" text PRIMARY KEY NOT NULL,
	"router_id" text NOT NULL,
	"revision_number" integer NOT NULL,
	"status" text DEFAULT 'draft' NOT NULL,
	"config" jsonb NOT NULL,
	"raw_imported_snapshot" jsonb DEFAULT 'null'::jsonb,
	"created_by" text DEFAULT 'operator' NOT NULL,
	"note" text,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE TABLE "vectra_router_credential" (
	"id" text PRIMARY KEY NOT NULL,
	"router_id" text NOT NULL,
	"type" "vectra_credential_type" DEFAULT 'agent_token' NOT NULL,
	"token_hash" text NOT NULL,
	"token_preview" text NOT NULL,
	"device_public_key" text NOT NULL,
	"issued_at" timestamp with time zone DEFAULT now() NOT NULL,
	"last_used_at" timestamp with time zone,
	"revoked_at" timestamp with time zone
);
--> statement-breakpoint
CREATE TABLE "vectra_router_inventory_snapshot" (
	"id" text PRIMARY KEY NOT NULL,
	"router_id" text NOT NULL,
	"source" text DEFAULT 'check_in' NOT NULL,
	"payload" jsonb NOT NULL,
	"passwall_enabled" boolean DEFAULT false NOT NULL,
	"selected_node_id" text,
	"node_count" integer DEFAULT 0 NOT NULL,
	"subscription_count" integer DEFAULT 0 NOT NULL,
	"controller_version" text,
	"passwall_app_version" text,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE TABLE "vectra_router" (
	"id" text PRIMARY KEY NOT NULL,
	"device_identifier" text NOT NULL,
	"display_name" text,
	"hostname" text,
	"panel_domain" text,
	"model" text,
	"board_name" text,
	"target" text,
	"architecture" text,
	"openwrt_release" text,
	"status" "vectra_router_status" DEFAULT 'pending' NOT NULL,
	"controller_channel" "vectra_controller_channel" DEFAULT 'stable' NOT NULL,
	"approved_at" timestamp with time zone,
	"last_seen_at" timestamp with time zone,
	"last_check_in_at" timestamp with time zone,
	"last_direct_mode_at" timestamp with time zone,
	"last_rescue_reason" text,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL,
	"updated_at" timestamp with time zone DEFAULT now() NOT NULL
);
--> statement-breakpoint
ALTER TABLE "vectra_event_log" ADD CONSTRAINT "vectra_event_log_router_id_vectra_router_id_fk" FOREIGN KEY ("router_id") REFERENCES "public"."vectra_router"("id") ON DELETE set null ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "vectra_firmware_manifest" ADD CONSTRAINT "vectra_firmware_manifest_artifact_id_vectra_artifact_id_fk" FOREIGN KEY ("artifact_id") REFERENCES "public"."vectra_artifact"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "vectra_health_incident" ADD CONSTRAINT "vectra_health_incident_router_id_vectra_router_id_fk" FOREIGN KEY ("router_id") REFERENCES "public"."vectra_router"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "vectra_job_result" ADD CONSTRAINT "vectra_job_result_job_id_vectra_job_id_fk" FOREIGN KEY ("job_id") REFERENCES "public"."vectra_job"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "vectra_job_result" ADD CONSTRAINT "vectra_job_result_router_id_vectra_router_id_fk" FOREIGN KEY ("router_id") REFERENCES "public"."vectra_router"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "vectra_job" ADD CONSTRAINT "vectra_job_router_id_vectra_router_id_fk" FOREIGN KEY ("router_id") REFERENCES "public"."vectra_router"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "vectra_job" ADD CONSTRAINT "vectra_job_desired_revision_id_vectra_passwall_desired_revision_id_fk" FOREIGN KEY ("desired_revision_id") REFERENCES "public"."vectra_passwall_desired_revision"("id") ON DELETE set null ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "vectra_passwall_applied_revision" ADD CONSTRAINT "vectra_passwall_applied_revision_router_id_vectra_router_id_fk" FOREIGN KEY ("router_id") REFERENCES "public"."vectra_router"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "vectra_passwall_applied_revision" ADD CONSTRAINT "vectra_passwall_applied_revision_desired_revision_id_vectra_passwall_desired_revision_id_fk" FOREIGN KEY ("desired_revision_id") REFERENCES "public"."vectra_passwall_desired_revision"("id") ON DELETE set null ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "vectra_passwall_desired_revision" ADD CONSTRAINT "vectra_passwall_desired_revision_router_id_vectra_router_id_fk" FOREIGN KEY ("router_id") REFERENCES "public"."vectra_router"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "vectra_router_credential" ADD CONSTRAINT "vectra_router_credential_router_id_vectra_router_id_fk" FOREIGN KEY ("router_id") REFERENCES "public"."vectra_router"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "vectra_router_inventory_snapshot" ADD CONSTRAINT "vectra_router_inventory_snapshot_router_id_vectra_router_id_fk" FOREIGN KEY ("router_id") REFERENCES "public"."vectra_router"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
CREATE INDEX "vectra_artifact_lookup_idx" ON "vectra_artifact" USING btree ("type","channel","name");--> statement-breakpoint
CREATE INDEX "vectra_event_log_router_idx" ON "vectra_event_log" USING btree ("router_id");--> statement-breakpoint
CREATE UNIQUE INDEX "vectra_firmware_manifest_unique_idx" ON "vectra_firmware_manifest" USING btree ("board_name","target","architecture","layout_family","channel");--> statement-breakpoint
CREATE INDEX "vectra_health_incident_router_idx" ON "vectra_health_incident" USING btree ("router_id");--> statement-breakpoint
CREATE INDEX "vectra_job_result_router_idx" ON "vectra_job_result" USING btree ("router_id");--> statement-breakpoint
CREATE INDEX "vectra_job_result_job_idx" ON "vectra_job_result" USING btree ("job_id");--> statement-breakpoint
CREATE INDEX "vectra_job_router_state_idx" ON "vectra_job" USING btree ("router_id","state");--> statement-breakpoint
CREATE UNIQUE INDEX "vectra_job_dedupe_idx" ON "vectra_job" USING btree ("dedupe_key");--> statement-breakpoint
CREATE INDEX "vectra_passwall_applied_router_idx" ON "vectra_passwall_applied_revision" USING btree ("router_id");--> statement-breakpoint
CREATE UNIQUE INDEX "vectra_passwall_revision_router_revision_idx" ON "vectra_passwall_desired_revision" USING btree ("router_id","revision_number");--> statement-breakpoint
CREATE INDEX "vectra_passwall_revision_router_idx" ON "vectra_passwall_desired_revision" USING btree ("router_id");--> statement-breakpoint
CREATE INDEX "vectra_router_credential_router_idx" ON "vectra_router_credential" USING btree ("router_id");--> statement-breakpoint
CREATE UNIQUE INDEX "vectra_router_credential_token_idx" ON "vectra_router_credential" USING btree ("token_hash");--> statement-breakpoint
CREATE INDEX "vectra_router_inventory_router_idx" ON "vectra_router_inventory_snapshot" USING btree ("router_id");--> statement-breakpoint
CREATE INDEX "vectra_router_inventory_created_idx" ON "vectra_router_inventory_snapshot" USING btree ("created_at");--> statement-breakpoint
CREATE UNIQUE INDEX "vectra_router_device_identifier_idx" ON "vectra_router" USING btree ("device_identifier");--> statement-breakpoint
CREATE INDEX "vectra_router_status_idx" ON "vectra_router" USING btree ("status");--> statement-breakpoint
CREATE INDEX "vectra_router_last_seen_idx" ON "vectra_router" USING btree ("last_seen_at");
