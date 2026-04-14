DO $$ BEGIN
 CREATE TYPE "public"."vectra_push_alert_kind" AS ENUM('offline', 'direct_mode', 'incident');
EXCEPTION
 WHEN duplicate_object THEN null;
END $$;
--> statement-breakpoint
CREATE TABLE "vectra_operator_push_alert" (
	"id" text PRIMARY KEY NOT NULL,
	"router_id" text NOT NULL,
	"kind" "vectra_push_alert_kind" NOT NULL,
	"severity" "vectra_severity" DEFAULT 'warning' NOT NULL,
	"dedupe_key" text NOT NULL,
	"title" text NOT NULL,
	"body" text NOT NULL,
	"href" text NOT NULL,
	"payload" jsonb DEFAULT '{}'::jsonb NOT NULL,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL,
	"resolved_at" timestamp with time zone
);
--> statement-breakpoint
CREATE TABLE "vectra_operator_push_subscription" (
	"id" text PRIMARY KEY NOT NULL,
	"operator_user" text NOT NULL,
	"endpoint" text NOT NULL,
	"p256dh" text NOT NULL,
	"auth" text NOT NULL,
	"user_agent" text,
	"last_successful_push_at" timestamp with time zone,
	"last_failure_at" timestamp with time zone,
	"last_failure_reason" text,
	"disabled_at" timestamp with time zone,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL,
	"updated_at" timestamp with time zone DEFAULT now() NOT NULL
);
--> statement-breakpoint
ALTER TABLE "vectra_operator_push_alert" ADD CONSTRAINT "vectra_operator_push_alert_router_id_vectra_router_id_fk" FOREIGN KEY ("router_id") REFERENCES "public"."vectra_router"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
CREATE UNIQUE INDEX "vectra_operator_push_alert_dedupe_idx" ON "vectra_operator_push_alert" USING btree ("dedupe_key");--> statement-breakpoint
CREATE INDEX "vectra_operator_push_alert_router_idx" ON "vectra_operator_push_alert" USING btree ("router_id");--> statement-breakpoint
CREATE INDEX "vectra_operator_push_alert_resolved_idx" ON "vectra_operator_push_alert" USING btree ("resolved_at");--> statement-breakpoint
CREATE UNIQUE INDEX "vectra_operator_push_subscription_endpoint_idx" ON "vectra_operator_push_subscription" USING btree ("endpoint");--> statement-breakpoint
CREATE INDEX "vectra_operator_push_subscription_user_idx" ON "vectra_operator_push_subscription" USING btree ("operator_user");--> statement-breakpoint
CREATE INDEX "vectra_operator_push_subscription_disabled_idx" ON "vectra_operator_push_subscription" USING btree ("disabled_at");
