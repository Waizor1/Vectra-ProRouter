import {
  createCipheriv,
  createDecipheriv,
  createHash,
  createHmac,
  randomBytes,
} from "node:crypto";
import { gunzipSync, gzipSync } from "node:zlib";

import {
  MASKED_SECRET_PLACEHOLDER,
  passwallDesiredConfigSchema,
  type PasswallDesiredConfig,
  type XrayDesiredConfig,
  xrayDesiredConfigSchema,
} from "@vectra/contracts";

import { env } from "~/env";

type JsonValue =
  | string
  | number
  | boolean
  | null
  | JsonValue[]
  | { [key: string]: JsonValue };

type PasswallSecretPayload = {
  config: PasswallDesiredConfig;
};

type XraySecretPayload = {
  config: XrayDesiredConfig;
};

const sensitiveExtraPatterns = [
  /secret/i,
  /password/i,
  /token/i,
  /private/i,
  /uuid/i,
  /key/i,
];

const rawSnapshotSensitiveKeyPatterns = [
  ...sensitiveExtraPatterns,
  /^url$/i,
  /^uri$/i,
  /^username$/i,
  /^user$/i,
];

function deriveKey() {
  return createHash("sha256").update(env.VECTRA_SECRETS_KEY).digest();
}

export function stableStringify(value: unknown): string {
  return JSON.stringify(sortValue(value));
}

function sortValue(value: unknown): JsonValue {
  if (
    typeof value === "string" ||
    typeof value === "number" ||
    typeof value === "boolean" ||
    value === null
  ) {
    return value;
  }

  if (Array.isArray(value)) {
    return value.map((entry) => sortValue(entry));
  }

  if (typeof value === "object" && value) {
    return Object.fromEntries(
      Object.entries(value as Record<string, unknown>)
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([key, entry]) => [key, sortValue(entry)])
    ) as JsonValue;
  }

  return JSON.stringify(value);
}

export function computeConfigDigest(value: unknown) {
  return createHash("sha256").update(stableStringify(value)).digest("hex");
}

export const KEYED_DIGEST_PREFIX = "hmac1:";

/**
 * A digest of request material that may carry a secret (a Wi-Fi password),
 * keyed by a subkey of VECTRA_SECRETS_KEY: unlike a bare sha256, a database
 * dump alone cannot be brute-forced back to the password. `purpose` separates
 * the uses of the subkey.
 */
export function keyedDigest(purpose: string, ...parts: Array<string | Uint8Array>) {
  const subkey = createHmac("sha256", env.VECTRA_SECRETS_KEY)
    .update(`vectra-keyed-digest-v1\n${purpose}`)
    .digest();
  const mac = createHmac("sha256", subkey);
  for (const part of parts) {
    mac.update(part);
  }
  return `${KEYED_DIGEST_PREFIX}${mac.digest("hex")}`;
}

// Envelope versions:
//   v1 — plaintext JSON, then AES-256-GCM.
//   v2 — plaintext JSON, then gzip, then AES-256-GCM.
//
// Ciphertext is incompressible, so a v1 blob defeats Postgres TOAST entirely:
// the secret table carried 954 MB of TOAST against 1160 kB of heap while the
// jsonb columns beside it compressed 3.3x. Config payloads are repetitive node
// lists that gzip ~5x, but only while they are still plaintext — compressing
// after encryption is worthless. Hence gzip first, encrypt second.
//
// v1 blobs are still read as-is: re-encrypting the existing rows would need the
// whole table rewritten under the operator key for no benefit, since retention
// ages them out anyway.
const SECRET_ENVELOPE_VERSION = 2;

// `aad` binds an envelope to its context (purpose, row id) through the GCM
// tag: a ciphertext copied onto another row fails to decrypt instead of
// decrypting into the wrong context. Such an envelope carries `ad: 1` and can
// only be opened with the same `aad`.
export function encryptJson(payload: unknown, options: { aad?: string } = {}) {
  const iv = randomBytes(12);
  const cipher = createCipheriv("aes-256-gcm", deriveKey(), iv);
  if (options.aad !== undefined) {
    cipher.setAAD(Buffer.from(options.aad, "utf8"));
  }
  const plaintext = gzipSync(Buffer.from(stableStringify(payload), "utf8"));
  const ciphertext = Buffer.concat([cipher.update(plaintext), cipher.final()]);
  const tag = cipher.getAuthTag();

  return JSON.stringify({
    v: SECRET_ENVELOPE_VERSION,
    ...(options.aad !== undefined ? { ad: 1 } : {}),
    iv: iv.toString("base64url"),
    tag: tag.toString("base64url"),
    data: ciphertext.toString("base64url"),
  });
}

/** Whether an envelope was sealed with associated data (see encryptJson). */
export function isAadBoundEnvelope(ciphertext: string) {
  try {
    return (JSON.parse(ciphertext) as { ad?: unknown }).ad === 1;
  } catch {
    return false;
  }
}

export function decryptJson<T>(
  ciphertext: string,
  options: { aad?: string } = {},
): T {
  const parsed = JSON.parse(ciphertext) as {
    v: number;
    ad?: number;
    iv: string;
    tag: string;
    data: string;
  };
  const decipher = createDecipheriv(
    "aes-256-gcm",
    deriveKey(),
    Buffer.from(parsed.iv, "base64url")
  );
  if (parsed.ad === 1) {
    if (options.aad === undefined) {
      throw new Error("Envelope requires associated data.");
    }
    decipher.setAAD(Buffer.from(options.aad, "utf8"));
  }
  decipher.setAuthTag(Buffer.from(parsed.tag, "base64url"));
  const plaintext = Buffer.concat([
    decipher.update(Buffer.from(parsed.data, "base64url")),
    decipher.final(),
  ]);

  const json = parsed.v >= 2 ? gunzipSync(plaintext) : plaintext;

  return JSON.parse(json.toString("utf8")) as T;
}

function sanitizeExtras(extras: Record<string, string | number | boolean | string[] | null>) {
  const sanitized: Record<string, string | number | boolean | string[] | null> = {};

  for (const [key, value] of Object.entries(extras)) {
    sanitized[key] = sensitiveExtraPatterns.some((pattern) => pattern.test(key))
      ? MASKED_SECRET_PLACEHOLDER
      : value;
  }

  return sanitized;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function hasStringId(
  value: unknown
): value is {
  id: string;
} {
  return isRecord(value) && typeof value.id === "string" && value.id.length > 0;
}

function maskStringPreservingQuotes(raw: string) {
  const trimmed = raw.trim();
  if (
    (trimmed.startsWith("'") && trimmed.endsWith("'")) ||
    (trimmed.startsWith('"') && trimmed.endsWith('"'))
  ) {
    return `${trimmed[0]}${MASKED_SECRET_PLACEHOLDER}${trimmed[0]}`;
  }

  return MASKED_SECRET_PLACEHOLDER;
}

function sanitizeUCIAssignment(raw: string) {
  const separatorIndex = raw.indexOf("=");
  if (separatorIndex === -1) {
    return null;
  }

  const left = raw.slice(0, separatorIndex).trim();
  const right = raw.slice(separatorIndex + 1);
  const option = left.split(".").at(-1)?.trim() ?? "";

  if (
    option &&
    rawSnapshotSensitiveKeyPatterns.some((pattern) => pattern.test(option))
  ) {
    return `${left}=${maskStringPreservingQuotes(right)}`;
  }

  return null;
}

function sanitizeUnknownSecrets(value: unknown, keyHint?: string): unknown {
  if (typeof value === "string") {
    if (
      keyHint &&
      rawSnapshotSensitiveKeyPatterns.some((pattern) => pattern.test(keyHint))
    ) {
      return MASKED_SECRET_PLACEHOLDER;
    }

    return sanitizeUCIAssignment(value) ?? value;
  }

  if (Array.isArray(value)) {
    return value.map((entry) => sanitizeUnknownSecrets(entry, keyHint));
  }

  if (isRecord(value)) {
    return Object.fromEntries(
      Object.entries(value).map(([key, entry]) => [
        key,
        sanitizeUnknownSecrets(entry, key),
      ])
    );
  }

  return value;
}

function restoreMaskedSecrets(nextValue: unknown, sourceValue: unknown): unknown {
  if (nextValue === MASKED_SECRET_PLACEHOLDER) {
    return sourceValue ?? nextValue;
  }

  if (Array.isArray(nextValue)) {
    if (Array.isArray(sourceValue) && nextValue.every(hasStringId)) {
      const sourceById = new Map(
        sourceValue.filter(hasStringId).map((entry) => [entry.id, entry])
      );
      return nextValue.map((entry, index) =>
        restoreMaskedSecrets(
          entry,
          hasStringId(entry)
            ? sourceById.get(entry.id)
            : Array.isArray(sourceValue)
              ? sourceValue[index]
              : undefined
        )
      );
    }

    return nextValue.map((entry, index) =>
      restoreMaskedSecrets(
        entry,
        Array.isArray(sourceValue) ? sourceValue[index] : undefined
      )
    );
  }

  if (isRecord(nextValue)) {
    const sourceRecord = isRecord(sourceValue) ? sourceValue : {};
    return Object.fromEntries(
      Object.entries(nextValue).map(([key, entry]) => [
        key,
        restoreMaskedSecrets(entry, sourceRecord[key]),
      ])
    );
  }

  return nextValue;
}

export function sanitizePasswallConfig(config: PasswallDesiredConfig) {
  return {
    ...config,
    nodes: config.nodes.map((node) => ({
      ...node,
      username: node.username ? MASKED_SECRET_PLACEHOLDER : node.username,
      password: node.password ? MASKED_SECRET_PLACEHOLDER : node.password,
      extras: sanitizeExtras(node.extras),
    })),
    subscriptions: {
      ...config.subscriptions,
      items: config.subscriptions.items.map((item) => ({
        ...item,
        url: item.url ? MASKED_SECRET_PLACEHOLDER : item.url,
        extras: sanitizeExtras(item.extras),
      })),
    },
  } satisfies PasswallDesiredConfig;
}

export function sanitizePasswallRawSnapshot(snapshot: Record<string, unknown>) {
  return sanitizeUnknownSecrets(snapshot) as Record<string, unknown>;
}

export function hydratePasswallConfig(
  maskedConfig: PasswallDesiredConfig,
  ciphertext: string | null
): PasswallDesiredConfig {
  if (!ciphertext) {
    return maskedConfig;
  }

  const payload = decryptJson<PasswallSecretPayload>(ciphertext);
  return passwallDesiredConfigSchema.parse(payload.config);
}

export function createSecretPayload(config: PasswallDesiredConfig) {
  return encryptJson({
    config,
  } satisfies PasswallSecretPayload);
}

export function restoreMaskedPasswallConfig(
  maskedConfig: PasswallDesiredConfig,
  sourceConfig: PasswallDesiredConfig | null
) {
  if (!sourceConfig) {
    return maskedConfig;
  }

  return passwallDesiredConfigSchema.parse(
    restoreMaskedSecrets(maskedConfig, sourceConfig)
  );
}

// ---------------------------------------------------------------------------
// xray-direct engine secret handling (Vectra Controller Pro).
//
// The xray config carries node/subscription secrets (vless uuid, reality
// publicKey/shortId, trojan/shadowsocks passwords, wireguard secretKey,
// subscription URLs and headers, inbound credentials). Mirroring the passwall
// path, those are MASKED in the jsonb `config` column (and in anything shipped
// to operator clients) and kept in cleartext only inside the encrypted secret
// blob, which is the source of truth when hydrating for the controller.
// ---------------------------------------------------------------------------

// `shortId` is a reality secret and `username`/`user` are inbound credentials;
// neither matches the shared secret-key patterns, so they are masked
// explicitly alongside them. Subscription `url`/`headers` are handled
// structurally on the subscription itself (so public geo asset URLs under
// `geo` are never masked).
//
// `pass` is the socks/http OUTBOUND credential key (node.go
// SocksOutboundSettings.Pass / HTTPOutboundSettings.Pass) which `/password/i`
// does not catch; `seed` is the KCP stream obfuscation pre-shared secret
// (stream.go KCPSettings.Seed). Outbound auth `headers` are masked
// structurally on the node (see `maskHeaders` use in `sanitizeXrayConfig`),
// because `maskJsonSecretsByKey` only matches the header MAP key, never the
// inner header names (e.g. `headers.Authorization`).
const xraySecretKeyPatterns = [
  ...sensitiveExtraPatterns,
  /^shortid$/i,
  /^username$/i,
  /^user$/i,
  /^pass$/i,
  /pass/i,
  /^seed$/i,
];

function maskJsonSecretsByKey(value: unknown, keyHint?: string): JsonValue {
  if (typeof value === "string") {
    if (
      keyHint &&
      xraySecretKeyPatterns.some((pattern) => pattern.test(keyHint))
    ) {
      return MASKED_SECRET_PLACEHOLDER;
    }
    return value;
  }

  if (
    typeof value === "number" ||
    typeof value === "boolean" ||
    value === null
  ) {
    return value;
  }

  if (Array.isArray(value)) {
    return value.map((entry) => maskJsonSecretsByKey(entry, keyHint));
  }

  if (isRecord(value)) {
    return Object.fromEntries(
      Object.entries(value).map(([key, entry]) => [
        key,
        maskJsonSecretsByKey(entry, key),
      ])
    ) as JsonValue;
  }

  return null;
}

function maskHeaders(headers: unknown) {
  if (!isRecord(headers)) {
    return headers;
  }

  return Object.fromEntries(
    Object.entries(headers).map(([key]) => [key, MASKED_SECRET_PLACEHOLDER])
  );
}

export function sanitizeXrayConfig(config: XrayDesiredConfig): XrayDesiredConfig {
  const sanitized: Record<string, unknown> = { ...config };

  // NOTE: there is no `nodes` array to mask any more. Since the "consume
  // provider JSON" pivot the panel never carries outbounds — the router fetches
  // the provider document itself. The only secrets left in the operator config
  // are the subscription URL (which IS the credential: it embeds the per-router
  // token) and any auth headers sent with it.

  // Subscriptions: the fetch URL and any auth headers are the secrets. Mask
  // them structurally so public geo asset URLs (under `geo`) are untouched.
  if (Array.isArray(config.subscriptions)) {
    sanitized.subscriptions = config.subscriptions.map((subscription) => {
      const masked = maskJsonSecretsByKey(subscription) as Record<
        string,
        unknown
      >;
      if (typeof subscription.url === "string" && subscription.url.length > 0) {
        masked.url = MASKED_SECRET_PLACEHOLDER;
      }
      if ("headers" in subscription) {
        masked.headers = maskHeaders(subscription.headers);
      }
      return masked;
    });
  }

  // The tproxy inbound carries no credential today (listenIP/port/fwmark/
  // udpEnabled/sniffing/tag/killSwitch). Masking by key over the subtree is
  // therefore a no-op, kept as a cheap guard so a future credential-bearing
  // field cannot silently land in the jsonb column in cleartext.
  if (config.inbounds) {
    sanitized.inbounds = maskJsonSecretsByKey(config.inbounds) as Record<
      string,
      unknown
    >;
  }

  return sanitized as XrayDesiredConfig;
}

export function hydrateXrayConfig(
  storedConfig: XrayDesiredConfig,
  ciphertext: string | null
): XrayDesiredConfig {
  if (!ciphertext) {
    return storedConfig;
  }

  const payload = decryptJson<XraySecretPayload>(ciphertext);
  return xrayDesiredConfigSchema.parse(payload.config);
}

export function createXraySecretPayload(config: XrayDesiredConfig) {
  return encryptJson({
    config,
  } satisfies XraySecretPayload);
}

// Mirror of `restoreMaskedPasswallConfig` for the xray path. When an edit UI
// loads a MASKED config and re-submits it, every placeholder must be replaced
// with the real secret from the prior (hydrated) revision before digest +
// encrypt — otherwise the literal placeholder would be persisted as a "real"
// secret and the controller would hydrate placeholders into a broken router.
// `restoreMaskedSecrets` already matches array entries by `id`, which covers
// xray `nodes[]`/`subscriptions[]` (both keyed by a string `id`).
export function restoreMaskedXrayConfig(
  maskedConfig: XrayDesiredConfig,
  sourceConfig: XrayDesiredConfig | null
) {
  if (!sourceConfig) {
    return maskedConfig;
  }

  return xrayDesiredConfigSchema.parse(
    restoreMaskedSecrets(maskedConfig, sourceConfig)
  );
}
