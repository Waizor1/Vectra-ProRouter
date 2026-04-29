import { createEnv } from "@t3-oss/env-nextjs";
import { z } from "zod";

/** @param {boolean} defaultValue */
const booleanFlagSchema = (defaultValue) =>
  z
    .enum(["true", "false"])
    .default(defaultValue ? "true" : "false")
    .transform((value) => value === "true");

export const env = createEnv({
  /**
   * Specify your server-side environment variables schema here. This way you can ensure the app
   * isn't built with invalid env vars.
   */
  server: {
    DATABASE_URL: z.string().url(),
    VECTRA_OPERATOR_USER: z.string().min(1).default("operator"),
    VECTRA_OPERATOR_PASSWORD: z.string().min(1).default("change-me"),
    VECTRA_SECRETS_KEY: z
      .string()
      .min(32)
      .default("dev-only-vectra-secrets-key-000000"),
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
    VECTRA_TELEGRAM_BOT_TOKEN: z.string().min(1).optional(),
    VECTRA_TELEGRAM_ALLOWED_CHAT_IDS: z.string().min(1).optional(),
    VECTRA_TELEGRAM_WEBHOOK_SECRET: z.string().min(16).optional(),
    VECTRA_TELEGRAM_CALLBACK_SECRET: z.string().min(32).optional(),
    VECTRA_TELEGRAM_DRY_RUN: booleanFlagSchema(true),
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
    VECTRA_AUTO_RESCUE_MONITOR_INTERVAL_SECONDS:
      process.env.VECTRA_AUTO_RESCUE_MONITOR_INTERVAL_SECONDS,
    VECTRA_AUTO_RESCUE_STALE_SECONDS:
      process.env.VECTRA_AUTO_RESCUE_STALE_SECONDS,
    VECTRA_AUTO_RESCUE_ESCALATION_SECONDS:
      process.env.VECTRA_AUTO_RESCUE_ESCALATION_SECONDS,
    VECTRA_TELEGRAM_BOT_TOKEN: process.env.VECTRA_TELEGRAM_BOT_TOKEN,
    VECTRA_TELEGRAM_ALLOWED_CHAT_IDS:
      process.env.VECTRA_TELEGRAM_ALLOWED_CHAT_IDS,
    VECTRA_TELEGRAM_WEBHOOK_SECRET: process.env.VECTRA_TELEGRAM_WEBHOOK_SECRET,
    VECTRA_TELEGRAM_CALLBACK_SECRET:
      process.env.VECTRA_TELEGRAM_CALLBACK_SECRET,
    VECTRA_TELEGRAM_DRY_RUN: process.env.VECTRA_TELEGRAM_DRY_RUN,
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
