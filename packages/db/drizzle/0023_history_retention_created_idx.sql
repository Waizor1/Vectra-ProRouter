-- Indexes for the 30-day history retention sweeps (history-retention.ts), which
-- select the oldest rows by created_at in batches. Without them every batch is
-- a sequential scan of the whole table (event_log was 163 MB on 2026-10-05).
--
-- Build these CONCURRENTLY on production BEFORE deploying, so the deploy-time
-- migration below is a no-op there instead of locking the tables for writes:
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS "vectra_event_log_created_idx" ON "vectra_event_log" USING btree ("created_at");
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS "vectra_job_created_idx" ON "vectra_job" USING btree ("created_at");
CREATE INDEX IF NOT EXISTS "vectra_event_log_created_idx" ON "vectra_event_log" USING btree ("created_at");--> statement-breakpoint
CREATE INDEX IF NOT EXISTS "vectra_job_created_idx" ON "vectra_job" USING btree ("created_at");
