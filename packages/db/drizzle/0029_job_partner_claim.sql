-- Partner API: the claim a partner job was queued under ({claimId, ownerRef,
-- partnerId}), fixed when the job is created — a claim's apply job and an
-- owner's action job. Its result is reported under THAT claim, not the
-- router's current one: after an unbind and a re-claim of the same router by
-- the same owner, a late result of the old attempt's job keeps the old claimId
-- and leaves the new claim's router state alone. Never sent to the router.
-- Additive and nullable with no default: existing jobs stay NULL (their claim
-- is unknown, and their events say so). Idempotent (IF NOT EXISTS) like the
-- migrations before it.
--
-- As in 0028: give up on the ACCESS EXCLUSIVE lock after 5 s rather than
-- queue every job read behind a long reader (SET LOCAL lasts until the
-- migrate transaction ends; outside one it is a no-op with a warning).
SET LOCAL lock_timeout = '5s';
--> statement-breakpoint
ALTER TABLE "vectra_job" ADD COLUMN IF NOT EXISTS "partner_claim" jsonb;
