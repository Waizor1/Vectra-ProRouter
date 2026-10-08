import { createEnv } from "@t3-oss/env-nextjs";
import { z } from "zod";

/** @param {boolean} defaultValue */
const booleanFlagSchema = (defaultValue) =>
  z
    .enum(["true", "false"])
    .default(defaultValue ? "true" : "false")
    .transform((value) => value === "true");

/**
 * @param {import("zod").ZodType<string, import("zod").ZodTypeDef, string | undefined>} schema
 * @param {string[]} unsafeValues
 * @param {string} label
 */
export const productionSafeStringSchema = (schema, unsafeValues, label) =>
  schema.superRefine((value, ctx) => {
    if (process.env.NODE_ENV !== "production") {
      return;
    }

    if (unsafeValues.includes(value)) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: `${label} must be set to a production secret.`,
      });
    }
  });

// The deliberately PUBLIC test key of router/vectra-controller-pro/ui/contract/
// claim-vector.json. Anyone can open a router QR sealed to it, so production
// refuses it. (Kept in sync with the vector by src/env.test.ts; the panel also
// declines to hand it to routers at runtime, router-claim-state.ts.)
const ROUTER_CLAIM_PUBLIC_TEST_KEY =
  "dsTt0kkqBdr7emCqoixBS+iz/r9Y86jyAxEzzKARNBs=";

// Standard, padded base64 of 32 bytes: exactly what vctl decodes
// (base64.StdEncoding) — a url-safe or unpadded key would be refused by every
// router and no QR would ever be shown.
export const routerClaimPubkeySchema = z
  .string()
  .regex(
    /^[A-Za-z0-9+/]{43}=$/,
    "VECTRA_ROUTER_CLAIM_PUBKEY must be a standard base64 raw 32-byte X25519 key.",
  )
  .superRefine((value, ctx) => {
    if (
      process.env.NODE_ENV === "production" &&
      value === ROUTER_CLAIM_PUBLIC_TEST_KEY
    ) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message:
          "VECTRA_ROUTER_CLAIM_PUBKEY is the public test key from claim-vector.json: anyone could open router QRs. Set Vectra's real claim key.",
      });
    }
  });

export const env = createEnv({
  /**
   * Specify your server-side environment variables schema here. This way you can ensure the app
   * isn't built with invalid env vars.
   */
  server: {
    DATABASE_URL: z.string().url(),
    VECTRA_OPERATOR_USER: z.string().min(1).default("operator"),
    VECTRA_OPERATOR_PASSWORD: productionSafeStringSchema(
      z.string().min(1).default("change-me"),
      ["change-me"],
      "VECTRA_OPERATOR_PASSWORD",
    ),
    VECTRA_SECRETS_KEY: productionSafeStringSchema(
      z.string().min(32).default("dev-only-vectra-secrets-key-000000"),
      ["dev-only-vectra-secrets-key-000000"],
      "VECTRA_SECRETS_KEY",
    ),
    VECTRA_DEFAULT_CONTROL_DOMAIN: z
      .string()
      .url()
      .default("https://router.vectra-pro.net"),
    VECTRA_ROUTER_API_BASE_URL: z
      .string()
      .url()
      .default("https://api.vectra-pro.net"),
    VECTRA_ARTIFACT_BASE_URL: z
      .string()
      .url()
      .default("https://api.vectra-pro.net/artifacts"),
    VECTRA_POLLING_INTERVAL_SECONDS: z.coerce
      .number()
      .int()
      .min(15)
      .default(45),
    VECTRA_WEB_PUSH_PUBLIC_KEY: z.string().min(1).optional(),
    VECTRA_WEB_PUSH_PRIVATE_KEY: z.string().min(1).optional(),
    VECTRA_WEB_PUSH_SUBJECT: z
      .string()
      .min(1)
      .default("mailto:admin@vectra-pro.net"),
    VECTRA_WEB_PUSH_MONITOR_INTERVAL_SECONDS: z.coerce
      .number()
      .int()
      .min(30)
      .default(60),
    VECTRA_AUTO_RESCUE_ENABLED: booleanFlagSchema(false),
    VECTRA_AUTO_ONBOARDING_ENABLED: booleanFlagSchema(false),
    VECTRA_AUTO_RESCUE_MONITOR_INTERVAL_SECONDS: z.coerce
      .number()
      .int()
      .min(30)
      .default(60),
    VECTRA_AUTO_RESCUE_STALE_SECONDS: z.coerce
      .number()
      .int()
      .min(60)
      .default(300),
    VECTRA_AUTO_RESCUE_ESCALATION_SECONDS: z.coerce
      .number()
      .int()
      .min(300)
      .max(900)
      .default(600),
    // Stuck-job janitor (post-totchto-r27-incident guard 2026-06-04):
    // periodically cancels vectra_job rows that have been state='running'
    // for longer than the threshold. A router that died mid-job leaves an
    // open journal entry the new controller re-fetches forever, blocking
    // all check-ins. Default 10 min between sweeps; default 60 min threshold
    // (no observed production job exceeds ~10 min, so 1 h is a safe upper
    // bound — replace with per-job-type SLAs in r31).
    // Where the background loops (auto-rescue, push monitor, janitor,
    // retention, route health, partner webhooks) run. "in-web" (default):
    // started inside the web process by the first /api/health call, as
    // always. "worker-separate": the web never starts them and the `worker`
    // compose service runs them instead. Either way each tick holds a
    // PostgreSQL advisory lock, so two processes never run one loop at once.
    VECTRA_BACKGROUND_MODE: z
      .enum(["in-web", "worker-separate"])
      .default("in-web"),
    VECTRA_STUCK_JOB_JANITOR_ENABLED: booleanFlagSchema(true),
    VECTRA_STUCK_JOB_JANITOR_INTERVAL_SECONDS: z.coerce
      .number()
      .int()
      .min(60)
      .max(3600)
      .default(600),
    VECTRA_STUCK_JOB_STALE_SECONDS: z.coerce
      .number()
      .int()
      .min(600)
      .default(3600),
    // Inventory-snapshot retention. Every check-in writes a snapshot, so the
    // table grows ~29k rows/day for a 30-router fleet with nothing pruning it
    // (406k rows / 1.3 GB observed on 2026-07-31, the largest table by far).
    //
    // Retention keeps the most recent N per router REGARDLESS of age, then
    // drops anything older than the window. The per-router floor is what makes
    // this safe for long-offline routers: a router silent for weeks keeps its
    // last known state instead of being erased by an age-only rule. The floor
    // is 20 against a maximum of 5 read by any code path (editor-surface), so
    // there is 4x headroom.
    VECTRA_SNAPSHOT_RETENTION_ENABLED: booleanFlagSchema(true),
    VECTRA_SNAPSHOT_RETENTION_INTERVAL_SECONDS: z.coerce
      .number()
      .int()
      .min(300)
      .max(86400)
      .default(3600),
    VECTRA_SNAPSHOT_RETENTION_HOURS: z.coerce
      .number()
      .int()
      .min(24)
      .default(72),
    VECTRA_SNAPSHOT_RETENTION_KEEP_PER_ROUTER: z.coerce
      .number()
      .int()
      .min(5)
      .default(20),
    // A check-in that carries no material change writes nothing, so a stable
    // router would otherwise leave no trace at all between config changes.
    //
    // This interval is not just a storage knob: the fleet monitoring view reads
    // reachability, safetyEvents and the resource gauges straight out of the
    // newest snapshot payload (fleet-monitoring-data.ts), and those are exactly
    // the fields the fingerprint deliberately ignores. So the heartbeat is what
    // bounds how stale the operator's telemetry can look. 15 minutes keeps that
    // honest while still cutting writes ~19x (27 routers × 4/h ≈ 108 rows/h
    // against the ~2085/h the fleet used to produce).
    VECTRA_SNAPSHOT_HEARTBEAT_MINUTES: z.coerce
      .number()
      .int()
      .min(5)
      .max(1440)
      .default(15),
    // Revision retention. The window is far longer than the snapshot one: a
    // revision is an operator-visible audit record, not telemetry, so a week
    // of superseded auto-imports stays available for diagnosis before the
    // per-router floor takes over as the only guarantee.
    VECTRA_REVISION_RETENTION_ENABLED: booleanFlagSchema(true),
    VECTRA_REVISION_RETENTION_INTERVAL_SECONDS: z.coerce
      .number()
      .int()
      .min(300)
      .max(86400)
      .default(21600),
    VECTRA_REVISION_RETENTION_HOURS: z.coerce
      .number()
      .int()
      .min(24)
      .default(168),
    VECTRA_REVISION_RETENTION_KEEP_PER_ROUTER: z.coerce
      .number()
      .int()
      .min(5)
      .default(20),
    // 30-day history retention (owner's decision 2026-10-05): event journal,
    // finished jobs with their results, resolved incidents and push alerts,
    // and desired revisions of any origin that nothing references. Deletes
    // run in batches; with DRY_RUN on (the default) a tick only logs what it
    // would delete, so the first deploy shows the numbers before any row goes.
    VECTRA_HISTORY_RETENTION_ENABLED: booleanFlagSchema(true),
    VECTRA_HISTORY_RETENTION_INTERVAL_SECONDS: z.coerce
      .number()
      .int()
      .min(300)
      .max(86400)
      .default(3600),
    VECTRA_RETENTION_DAYS: z.coerce.number().int().min(7).default(30),
    VECTRA_RETENTION_DRY_RUN: booleanFlagSchema(true),
    // Resolved rescue cases (22k rows on 2026-10-05) have their own switch on
    // top of VECTRA_RETENTION_DRY_RUN: they are only deleted when BOTH are
    // false, so this table can be enabled after the others.
    VECTRA_RESCUE_CASE_RETENTION_DRY_RUN: booleanFlagSchema(true),
    VECTRA_RETENTION_BATCH_SIZE: z.coerce
      .number()
      .int()
      .min(100)
      .max(10000)
      .default(5000),
    // Heap budget, in MB, of EACH in-memory config cache (the check-in's
    // decrypted desired revisions; the monitors' per-router config summaries).
    // One entry per router, 10-minute TTL; past the budget a router is not
    // cached and is served from Postgres as before. 0 turns caching off.
    VECTRA_CONFIG_CACHE_MB: z.coerce.number().int().min(0).max(256).default(16),
    VECTRA_TELEGRAM_BOT_TOKEN: z.string().min(1).optional(),
    VECTRA_TELEGRAM_ALLOWED_CHAT_IDS: z.string().min(1).optional(),
    VECTRA_TELEGRAM_WEBHOOK_SECRET: z.string().min(16).optional(),
    VECTRA_TELEGRAM_CALLBACK_SECRET: z.string().min(32).optional(),
    VECTRA_TELEGRAM_DRY_RUN: booleanFlagSchema(true),
    // ADR-0006 — router onboarding by QR from the Vectra app.
    // Shared secret of the partner API (POST/DELETE /api/partner/router-claims).
    // Unset = the partner API answers 503 and accepts nothing.
    VECTRA_PARTNER_SECRET: z.string().min(32).optional(),
    // Further partners of the partner API (JSON array; see
    // server/vectra/partner-registry.ts). Unset = Vectra Connect alone, from
    // the variables above. Validated by the registry, which names the variable
    // and never a value.
    VECTRA_PARTNERS: z.string().optional(),
    // The Vectra backend's X25519 key the router seals its QR to (raw 32 bytes,
    // base64) and its key id. Both unset = no claimKey in router responses.
    VECTRA_ROUTER_CLAIM_PUBKEY: routerClaimPubkeySchema.optional(),
    VECTRA_ROUTER_CLAIM_KID: z.coerce.number().int().min(0).max(255).optional(),
    // Telegram bot username for the router's "open in Telegram" deep link.
    VECTRA_CONNECT_BOT_USERNAME: z
      .string()
      .transform((value) => value.replace(/^@/, ""))
      .pipe(z.string().regex(/^[A-Za-z0-9_]{5,32}$/))
      .optional(),
    // Where router.claimed / router.ready / router.failed are sent, and the
    // secret they are signed with. Either unset = no webhooks.
    VECTRA_CONNECT_WEBHOOK_URL: z
      .string()
      .url()
      .refine(
        (value) =>
          process.env.NODE_ENV !== "production" || value.startsWith("https://"),
        "VECTRA_CONNECT_WEBHOOK_URL must be https:// in production.",
      )
      .optional(),
    VECTRA_CONNECT_WEBHOOK_SECRET: z.string().min(32).optional(),
    NODE_ENV: z
      .enum(["development", "test", "production"])
      .default("development"),
  },

  /**
   * Specify your client-side environment variables schema here. This way you can ensure the app
   * isn't built with invalid env vars. To expose them to the client, prefix them with
   * `NEXT_PUBLIC_`.
   */
  client: {
    // NEXT_PUBLIC_CLIENTVAR: z.string(),
  },

  /**
   * You can't destruct `process.env` as a regular object in the Next.js edge runtimes (e.g.
   * middlewares) or client-side so we need to destruct manually.
   */
  runtimeEnv: {
    DATABASE_URL: process.env.DATABASE_URL,
    VECTRA_OPERATOR_USER: process.env.VECTRA_OPERATOR_USER,
    VECTRA_OPERATOR_PASSWORD: process.env.VECTRA_OPERATOR_PASSWORD,
    VECTRA_SECRETS_KEY: process.env.VECTRA_SECRETS_KEY,
    VECTRA_DEFAULT_CONTROL_DOMAIN: process.env.VECTRA_DEFAULT_CONTROL_DOMAIN,
    VECTRA_ROUTER_API_BASE_URL: process.env.VECTRA_ROUTER_API_BASE_URL,
    VECTRA_ARTIFACT_BASE_URL: process.env.VECTRA_ARTIFACT_BASE_URL,
    VECTRA_POLLING_INTERVAL_SECONDS:
      process.env.VECTRA_POLLING_INTERVAL_SECONDS,
    VECTRA_WEB_PUSH_PUBLIC_KEY: process.env.VECTRA_WEB_PUSH_PUBLIC_KEY,
    VECTRA_WEB_PUSH_PRIVATE_KEY: process.env.VECTRA_WEB_PUSH_PRIVATE_KEY,
    VECTRA_WEB_PUSH_SUBJECT: process.env.VECTRA_WEB_PUSH_SUBJECT,
    VECTRA_WEB_PUSH_MONITOR_INTERVAL_SECONDS:
      process.env.VECTRA_WEB_PUSH_MONITOR_INTERVAL_SECONDS,
    VECTRA_AUTO_RESCUE_ENABLED: process.env.VECTRA_AUTO_RESCUE_ENABLED,
    VECTRA_AUTO_ONBOARDING_ENABLED: process.env.VECTRA_AUTO_ONBOARDING_ENABLED,
    VECTRA_AUTO_RESCUE_MONITOR_INTERVAL_SECONDS:
      process.env.VECTRA_AUTO_RESCUE_MONITOR_INTERVAL_SECONDS,
    VECTRA_AUTO_RESCUE_STALE_SECONDS:
      process.env.VECTRA_AUTO_RESCUE_STALE_SECONDS,
    VECTRA_AUTO_RESCUE_ESCALATION_SECONDS:
      process.env.VECTRA_AUTO_RESCUE_ESCALATION_SECONDS,
    VECTRA_BACKGROUND_MODE: process.env.VECTRA_BACKGROUND_MODE,
    VECTRA_STUCK_JOB_JANITOR_ENABLED:
      process.env.VECTRA_STUCK_JOB_JANITOR_ENABLED,
    VECTRA_STUCK_JOB_JANITOR_INTERVAL_SECONDS:
      process.env.VECTRA_STUCK_JOB_JANITOR_INTERVAL_SECONDS,
    VECTRA_STUCK_JOB_STALE_SECONDS:
      process.env.VECTRA_STUCK_JOB_STALE_SECONDS,
    VECTRA_SNAPSHOT_RETENTION_ENABLED:
      process.env.VECTRA_SNAPSHOT_RETENTION_ENABLED,
    VECTRA_SNAPSHOT_RETENTION_INTERVAL_SECONDS:
      process.env.VECTRA_SNAPSHOT_RETENTION_INTERVAL_SECONDS,
    VECTRA_SNAPSHOT_RETENTION_HOURS:
      process.env.VECTRA_SNAPSHOT_RETENTION_HOURS,
    VECTRA_SNAPSHOT_RETENTION_KEEP_PER_ROUTER:
      process.env.VECTRA_SNAPSHOT_RETENTION_KEEP_PER_ROUTER,
    VECTRA_SNAPSHOT_HEARTBEAT_MINUTES:
      process.env.VECTRA_SNAPSHOT_HEARTBEAT_MINUTES,
    VECTRA_REVISION_RETENTION_ENABLED:
      process.env.VECTRA_REVISION_RETENTION_ENABLED,
    VECTRA_REVISION_RETENTION_INTERVAL_SECONDS:
      process.env.VECTRA_REVISION_RETENTION_INTERVAL_SECONDS,
    VECTRA_REVISION_RETENTION_HOURS:
      process.env.VECTRA_REVISION_RETENTION_HOURS,
    VECTRA_REVISION_RETENTION_KEEP_PER_ROUTER:
      process.env.VECTRA_REVISION_RETENTION_KEEP_PER_ROUTER,
    VECTRA_HISTORY_RETENTION_ENABLED:
      process.env.VECTRA_HISTORY_RETENTION_ENABLED,
    VECTRA_HISTORY_RETENTION_INTERVAL_SECONDS:
      process.env.VECTRA_HISTORY_RETENTION_INTERVAL_SECONDS,
    VECTRA_RETENTION_DAYS: process.env.VECTRA_RETENTION_DAYS,
    VECTRA_RETENTION_DRY_RUN: process.env.VECTRA_RETENTION_DRY_RUN,
    VECTRA_RESCUE_CASE_RETENTION_DRY_RUN:
      process.env.VECTRA_RESCUE_CASE_RETENTION_DRY_RUN,
    VECTRA_RETENTION_BATCH_SIZE: process.env.VECTRA_RETENTION_BATCH_SIZE,
    VECTRA_CONFIG_CACHE_MB: process.env.VECTRA_CONFIG_CACHE_MB,
    VECTRA_TELEGRAM_BOT_TOKEN: process.env.VECTRA_TELEGRAM_BOT_TOKEN,
    VECTRA_TELEGRAM_ALLOWED_CHAT_IDS:
      process.env.VECTRA_TELEGRAM_ALLOWED_CHAT_IDS,
    VECTRA_TELEGRAM_WEBHOOK_SECRET: process.env.VECTRA_TELEGRAM_WEBHOOK_SECRET,
    VECTRA_TELEGRAM_CALLBACK_SECRET:
      process.env.VECTRA_TELEGRAM_CALLBACK_SECRET,
    VECTRA_TELEGRAM_DRY_RUN: process.env.VECTRA_TELEGRAM_DRY_RUN,
    VECTRA_PARTNER_SECRET: process.env.VECTRA_PARTNER_SECRET,
    VECTRA_PARTNERS: process.env.VECTRA_PARTNERS,
    VECTRA_ROUTER_CLAIM_PUBKEY: process.env.VECTRA_ROUTER_CLAIM_PUBKEY,
    VECTRA_ROUTER_CLAIM_KID: process.env.VECTRA_ROUTER_CLAIM_KID,
    VECTRA_CONNECT_BOT_USERNAME: process.env.VECTRA_CONNECT_BOT_USERNAME,
    VECTRA_CONNECT_WEBHOOK_URL: process.env.VECTRA_CONNECT_WEBHOOK_URL,
    VECTRA_CONNECT_WEBHOOK_SECRET: process.env.VECTRA_CONNECT_WEBHOOK_SECRET,
    NODE_ENV: process.env.NODE_ENV,
    // NEXT_PUBLIC_CLIENTVAR: process.env.NEXT_PUBLIC_CLIENTVAR,
  },
  /**
   * Run `build` or `dev` with `SKIP_ENV_VALIDATION` to skip env validation. This is especially
   * useful for Docker builds.
   */
  skipValidation: !!process.env.SKIP_ENV_VALIDATION,
  /**
   * Makes it so that empty strings are treated as undefined. `SOME_VAR: z.string()` and
   * `SOME_VAR=''` will throw an error.
   */
  emptyStringAsUndefined: true,
});
