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
  const partners = parsed.data.map((e) => ({
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
  assertDistinctSecrets(partners);
  return partners;
}

/**
 * A secret that two partners (or one partner twice) would both accept leaves
 * a request's owner open to doubt, so it is refused. Names the partners and
 * never the secret. `legacy` is the partner that comes from the old variables.
 */
function assertDistinctSecrets(
  partners: readonly PartnerConfig[],
  legacy: PartnerConfig | null = null,
) {
  const nameOf = (p: PartnerConfig) =>
    p === legacy ? `${p.id} (VECTRA_PARTNER_SECRET)` : p.id;
  const owners = new Map<string, PartnerConfig>();
  for (const partner of partners) {
    for (const secret of partner.secrets) {
      const owner = owners.get(secret);
      if (owner === partner) {
        throw new Error(
          `VECTRA_PARTNERS partner ${nameOf(partner)} lists the same secret twice`,
        );
      }
      if (owner) {
        throw new Error(
          `VECTRA_PARTNERS partners ${nameOf(owner)} and ${nameOf(partner)} share a secret`,
        );
      }
      owners.set(secret, partner);
    }
  }
}

/**
 * Vectra Connect as configured before the registry. Its claim key and bot stay
 * where they always were (VECTRA_ROUTER_CLAIM_*, VECTRA_CONNECT_BOT_USERNAME;
 * router-claim-state reads them), so they are null here. The secret is passed
 * on exactly as the partner API always read it (no trim); empty means none.
 */
function legacyPartner(e: PartnerEnv): PartnerConfig {
  const secret = e.VECTRA_PARTNER_SECRET ?? "";
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

/** Throws (naming VECTRA_PARTNERS, never a value) when the listed partners are invalid. */
export function partnersFrom(e: PartnerEnv): PartnerConfig[] {
  const listed = parsePartners(e.VECTRA_PARTNERS);
  if (listed.some((p) => p.id === DEFAULT_PARTNER_ID)) return listed;
  const legacy = legacyPartner(e);
  const partners = [legacy, ...listed];
  assertDistinctSecrets(partners, legacy);
  return partners;
}

let lastReported: string | null = null;

/**
 * Every partner of the running process. A VECTRA_PARTNERS that does not
 * validate must never take Vectra Connect down with it: the error is logged
 * once (it names the variable, never a value) and the default partner, as the
 * old variables configure it, is served alone.
 */
export function listPartners(): PartnerConfig[] {
  const e = env as PartnerEnv;
  try {
    return partnersFrom(e);
  } catch (error) {
    // Only this module's own messages are logged: they hold no values.
    const message =
      error instanceof Error && error.message.startsWith("VECTRA_PARTNERS")
        ? error.message
        : "VECTRA_PARTNERS could not be read";
    if (message !== lastReported) {
      lastReported = message;
      console.error(
        `[partner-registry] ${message}; serving only the ${DEFAULT_PARTNER_ID} partner`,
      );
    }
    return [legacyPartner(e)];
  }
}

export function findPartner(id: string): PartnerConfig | null {
  return listPartners().find((p) => p.id === id) ?? null;
}

/** A key that starts the way a scoped key does: a partner id, then ":". */
const SCOPE_PREFIX = new RegExp(`${PARTNER_ID_PATTERN.source.slice(0, -1)}:`);

/**
 * Idempotency keys and request ids live in shared tables: every partner's are
 * scoped, so two partners never collide, and the default partner's keep their
 * old form so the keys Vectra Connect already stored still replay.
 *
 * Why no two (partner, key) pairs share a result:
 *  - another partner P gets `P:key`. P is a partner id (no ":"), so the text
 *    before the first ":" is P, and it matches PARTNER_ID_PATTERN.
 *  - the default partner's key is returned as it is only when it does NOT
 *    start with `<partner id>:`, so it can never equal any `P:key`.
 *  - one that does (a Vectra key "bloopcat:k-1" would otherwise equal
 *    BloopCat's scoped "k-1") becomes `vectra:key`. The text before its first
 *    ":" is "vectra", the default partner, whom no other partner shares, and
 *    stripping that prefix gives the key back.
 *  - within the default partner, a raw key never starts with a partner id and
 *    a wrapped one always does, so the two forms stay apart too.
 */
export function scopeIdempotencyKey(partnerId: string, key: string) {
  if (partnerId !== DEFAULT_PARTNER_ID) return `${partnerId}:${key}`;
  return SCOPE_PREFIX.test(key) ? `${DEFAULT_PARTNER_ID}:${key}` : key;
}
