import { createCipheriv, createDecipheriv, createHash, randomBytes } from "node:crypto";

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

export function encryptJson(payload: unknown) {
  const iv = randomBytes(12);
  const cipher = createCipheriv("aes-256-gcm", deriveKey(), iv);
  const plaintext = Buffer.from(stableStringify(payload), "utf8");
  const ciphertext = Buffer.concat([cipher.update(plaintext), cipher.final()]);
  const tag = cipher.getAuthTag();

  return JSON.stringify({
    v: 1,
    iv: iv.toString("base64url"),
    tag: tag.toString("base64url"),
    data: ciphertext.toString("base64url"),
  });
}

export function decryptJson<T>(ciphertext: string): T {
  const parsed = JSON.parse(ciphertext) as {
    v: number;
    iv: string;
    tag: string;
    data: string;
  };
  const decipher = createDecipheriv(
    "aes-256-gcm",
    deriveKey(),
    Buffer.from(parsed.iv, "base64url")
  );
  decipher.setAuthTag(Buffer.from(parsed.tag, "base64url"));
  const plaintext = Buffer.concat([
    decipher.update(Buffer.from(parsed.data, "base64url")),
    decipher.final(),
  ]);

  return JSON.parse(plaintext.toString("utf8")) as T;
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
const xraySecretKeyPatterns = [
  ...sensitiveExtraPatterns,
  /^shortid$/i,
  /^username$/i,
  /^user$/i,
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

  // Outbound nodes are opaque protocol objects; mask any secret-bearing key
  // (uuid/password/key/secret/token/private/shortId) at any depth. Non-secret
  // fields (server, serverName, port, flow, fingerprint, …) are preserved.
  if (Array.isArray(config.nodes)) {
    sanitized.nodes = config.nodes.map(
      (node) => maskJsonSecretsByKey(node) as Record<string, unknown>
    );
  }

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

  // Local inbounds can carry credentials (socks/http auth, shadowsocks
  // password, reality private keys). Mask by key over the inbounds subtree.
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
