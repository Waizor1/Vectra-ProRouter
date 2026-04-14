DO $$ BEGIN
 CREATE TYPE "public"."vectra_secret_blob_scope" AS ENUM('router_import', 'desired_revision');
EXCEPTION
 WHEN duplicate_object THEN null;
END $$;
--> statement-breakpoint
DO $$ BEGIN
 CREATE TYPE "public"."vectra_router_import_state" AS ENUM(
  'awaiting_import',
  'import_review',
  'approved',
  'out_of_sync'
 );
EXCEPTION
 WHEN duplicate_object THEN null;
END $$;
--> statement-breakpoint
CREATE TABLE "vectra_passwall_secret_blob" (
	"id" text PRIMARY KEY NOT NULL,
	"router_id" text NOT NULL,
	"desired_revision_id" text,
	"scope" "vectra_secret_blob_scope" NOT NULL,
	"ciphertext" text NOT NULL,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL
);
--> statement-breakpoint
ALTER TABLE "vectra_passwall_applied_revision" ADD COLUMN "job_id" text;--> statement-breakpoint
ALTER TABLE "vectra_passwall_applied_revision" ADD COLUMN "uci_digest" text;--> statement-breakpoint
ALTER TABLE "vectra_passwall_applied_revision" ADD COLUMN "stdout" text;--> statement-breakpoint
ALTER TABLE "vectra_passwall_applied_revision" ADD COLUMN "stderr" text;--> statement-breakpoint
ALTER TABLE "vectra_passwall_desired_revision" ADD COLUMN "origin" text DEFAULT 'operator_draft' NOT NULL;--> statement-breakpoint
ALTER TABLE "vectra_passwall_desired_revision" ADD COLUMN "config_digest" text;--> statement-breakpoint
ALTER TABLE "vectra_passwall_desired_revision" ADD COLUMN "approved_at" timestamp with time zone;--> statement-breakpoint
ALTER TABLE "vectra_router" ADD COLUMN "import_state" "vectra_router_import_state" DEFAULT 'awaiting_import' NOT NULL;--> statement-breakpoint
ALTER TABLE "vectra_router" ADD COLUMN "pending_import_revision_id" text;--> statement-breakpoint
ALTER TABLE "vectra_router" ADD COLUMN "active_revision_id" text;--> statement-breakpoint
ALTER TABLE "vectra_router" ADD COLUMN "last_applied_revision_id" text;--> statement-breakpoint
ALTER TABLE "vectra_router" ADD COLUMN "last_config_digest" text;--> statement-breakpoint
ALTER TABLE "vectra_passwall_secret_blob" ADD CONSTRAINT "vectra_passwall_secret_blob_router_id_vectra_router_id_fk" FOREIGN KEY ("router_id") REFERENCES "public"."vectra_router"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "vectra_passwall_secret_blob" ADD CONSTRAINT "vectra_passwall_secret_blob_desired_revision_id_vectra_passwall_desired_revision_id_fk" FOREIGN KEY ("desired_revision_id") REFERENCES "public"."vectra_passwall_desired_revision"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
CREATE INDEX "vectra_passwall_secret_router_idx" ON "vectra_passwall_secret_blob" USING btree ("router_id");--> statement-breakpoint
CREATE INDEX "vectra_passwall_secret_revision_idx" ON "vectra_passwall_secret_blob" USING btree ("desired_revision_id");
