CREATE TABLE "vectra_operator_rollout_profile" (
	"id" text PRIMARY KEY NOT NULL,
	"profile_key" text NOT NULL,
	"name" text NOT NULL,
	"description" text,
	"rollout_config" jsonb NOT NULL,
	"note" text,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL,
	"updated_at" timestamp with time zone DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX "vectra_operator_rollout_profile_key_idx" ON "vectra_operator_rollout_profile" USING btree ("profile_key");
--> statement-breakpoint
CREATE TABLE "vectra_operator_router_group" (
	"id" text PRIMARY KEY NOT NULL,
	"group_key" text NOT NULL,
	"name" text NOT NULL,
	"description" text,
	"rollout_profile_id" text,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL,
	"updated_at" timestamp with time zone DEFAULT now() NOT NULL,
	CONSTRAINT "vectra_operator_router_group_rollout_profile_id_vectra_operator_rollout_profile_id_fk" FOREIGN KEY ("rollout_profile_id") REFERENCES "public"."vectra_operator_rollout_profile"("id") ON DELETE set null ON UPDATE no action
);
--> statement-breakpoint
CREATE UNIQUE INDEX "vectra_operator_router_group_key_idx" ON "vectra_operator_router_group" USING btree ("group_key");
--> statement-breakpoint
CREATE INDEX "vectra_operator_router_group_profile_idx" ON "vectra_operator_router_group" USING btree ("rollout_profile_id");
--> statement-breakpoint
ALTER TABLE "vectra_router" ADD COLUMN "rollout_group_id" text;
--> statement-breakpoint
CREATE INDEX "vectra_router_rollout_group_idx" ON "vectra_router" USING btree ("rollout_group_id");
