-- Partner Idempotency-Key reservation: a key is bound to its request before
-- the operation runs (status_code 0 = no final answer yet), and the attempt
-- that holds it owns it until locked_until. Additive and nullable: every
-- existing row is a final 2xx answer and stays NULL. Idempotent (IF NOT EXISTS)
-- like the migrations before it.
ALTER TABLE "vectra_partner_idempotency_key" ADD COLUMN IF NOT EXISTS "locked_until" timestamp with time zone;
