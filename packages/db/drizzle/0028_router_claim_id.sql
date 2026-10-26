-- Partner API: the claim attempt id ("claimId"). A new random UUIDv4 on every
-- successful claim, returned by the claim response and carried in every partner
-- webhook as the value it had when the event was queued, so a partner can tell
-- a late event of an earlier claim of the same router from the current one.
-- An idempotent repeat of the claim keeps it; unbind clears it. Additive and
-- nullable with no default and no backfill: every existing row stays NULL (a
-- claim made before claim ids existed reports claimId null). Idempotent
-- (IF NOT EXISTS) like the migrations before it.
--
-- ADD COLUMN takes an ACCESS EXCLUSIVE lock on the router table, the busiest
-- one (every check-in): give up after 5 s instead of queueing every check-in
-- behind a long reader. drizzle-kit migrate runs all pending migrations in ONE
-- transaction, so SET LOCAL holds until it ends (and covers 0029 as well);
-- outside a transaction it is a no-op with a warning.
SET LOCAL lock_timeout = '5s';
--> statement-breakpoint
ALTER TABLE "vectra_router" ADD COLUMN IF NOT EXISTS "claim_id" uuid;
