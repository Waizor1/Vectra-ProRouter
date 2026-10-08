import { z } from "zod";
import { env } from "~/env";

/** Vectra Connect: the partner the API had before there were several. */
export const DEFAULT_PARTNER_ID = "vectra";
export const PARTNER_ID_PATTERN = /^[a-z][a-z0-9-]{1,31}$/;

export type PartnerConfig = {
  id: string;
  /** What the router calls itself for this partner's customers (vctl brand id). */
  brand: string;
  /** The owner label a router shows when the partner sent none. */
  label: string;
  /** Accepted HMAC secrets, the current one first; two only during a rotation. */
  secrets: string[];
  webhook: { url: string; secret: string } | null;
  /** The partner's X25519 key routers seal their signed User-Agent to. */
  claimKey: { kid: number; publicKey: string } | null;
  botUsername: string | null;
};

export type PartnerEnv = {
  VECTRA_PARTNERS?: string;
  VECTRA_PARTNER_SECRET?: string;
  VECTRA_CONNECT_WEBHOOK_URL?: string;
  VECTRA_CONNECT_WEBHOOK_SECRET?: string;
};

const entrySchema = z
  .object({
    id: z.string().regex(PARTNER_ID_PATTERN),
    brand: z.string().regex(PARTNER_ID_PATTERN),
    label: z.string().trim().min(1).max(32),
    secrets: z.array(z.string().min(32)).min(1).max(2),
    webhookUrl: z.string().url().startsWith("https://").optional(),
    webhookSecret: z.string().min(32).optional(),
    claimKid: z.number().int().min(0).max(255).optional(),
    claimPublicKey: z.string().regex(/^[A-Za-z0-9+/]{43}=$/).optional(),
    botUsername: z.string().regex(/^[A-Za-z0-9_]{5,32}$/).optional(),
  })
  .strict()
  .refine(
    (e) => (e.webhookUrl === undefined) === (e.webhookSecret === undefined),
    "webhookUrl and webhookSecret come together",
  )
  .refine(
    (e) => (e.claimKid === undefined) === (e.claimPublicKey === undefined),
    "claimKid and claimPublicKey come together",
  );

/** The partners listed in VECTRA_PARTNERS. Errors name the variable, never a value. */
export function parsePartners(raw: string | undefined): PartnerConfig[] {
  if (raw === undefined || raw.trim() === "") return [];
  let json: unknown;
  try {
    json = JSON.parse(raw);
  } catch {
    throw new Error("VECTRA_PARTNERS is not valid JSON");
  }
  const parsed = z.array(entrySchema).max(16).safeParse(json);
  if (!parsed.success) {
    const where = parsed.error.issues
      .map((issue) => `${issue.path.join(".") || "(root)"}: ${issue.message}`)
      .join("; ");
    throw new Error(`VECTRA_PARTNERS is invalid — ${where}`);
  }
  const seen = new Set<string>();
  for (const entry of parsed.data) {
    if (seen.has(entry.id)) {
      throw new Error(`VECTRA_PARTNERS repeats partner ${entry.id}`);
    }
    seen.add(entry.id);
  }
  return parsed.data.map((e) => ({
    id: e.id,
    brand: e.brand,
    label: e.label,
    secrets: e.secrets,
    webhook:
      e.webhookUrl && e.webhookSecret
        ? { url: e.webhookUrl, secret: e.webhookSecret }
        : null,
    claimKey:
      e.claimKid !== undefined && e.claimPublicKey
        ? { kid: e.claimKid, publicKey: e.claimPublicKey }
        : null,
    botUsername: e.botUsername ?? null,
  }));
}

/**
 * Vectra Connect as configured before the registry. Its claim key and bot stay
 * where they always were (VECTRA_ROUTER_CLAIM_*, VECTRA_CONNECT_BOT_USERNAME;
 * router-claim-state reads them), so they are null here.
 */
function legacyPartner(e: PartnerEnv): PartnerConfig {
  const secret = e.VECTRA_PARTNER_SECRET?.trim() ?? "";
  const url = e.VECTRA_CONNECT_WEBHOOK_URL?.trim() ?? "";
  const webhookSecret = e.VECTRA_CONNECT_WEBHOOK_SECRET ?? "";
  return {
    id: DEFAULT_PARTNER_ID,
    brand: "vectra",
    label: "Vectra",
    secrets: secret ? [secret] : [],
    webhook: url && webhookSecret ? { url, secret: webhookSecret } : null,
    claimKey: null,
    botUsername: null,
  };
}

export function partnersFrom(e: PartnerEnv): PartnerConfig[] {
  const listed = parsePartners(e.VECTRA_PARTNERS);
  return listed.some((p) => p.id === DEFAULT_PARTNER_ID)
    ? listed
    : [legacyPartner(e), ...listed];
}

export function listPartners(): PartnerConfig[] {
  return partnersFrom(env as PartnerEnv);
}

export function findPartner(id: string): PartnerConfig | null {
  return listPartners().find((p) => p.id === id) ?? null;
}

/**
 * Idempotency keys and request ids live in shared tables: a partner's are
 * prefixed so two partners can never collide. The default partner's keep
 * their old form, so the keys Vectra Connect already stored still replay.
 */
export function scopeIdempotencyKey(partnerId: string, key: string) {
  return partnerId === DEFAULT_PARTNER_ID ? key : `${partnerId}:${key}`;
}
