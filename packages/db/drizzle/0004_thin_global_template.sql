CREATE TABLE "vectra_operator_global_template" (
	"id" text PRIMARY KEY NOT NULL,
	"template_key" text NOT NULL,
	"title" text NOT NULL,
	"install_baseline_uci" text NOT NULL,
	"rollout_config" jsonb NOT NULL,
	"rollout_mode" text DEFAULT 'settings_only' NOT NULL,
	"note" text,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL,
	"updated_at" timestamp with time zone DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX "vectra_operator_global_template_key_idx" ON "vectra_operator_global_template" USING btree ("template_key");
