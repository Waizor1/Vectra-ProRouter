import { Buffer } from "node:buffer";

import type { PasswallDesiredConfig } from "@vectra/contracts";

import {
  buildNodeSemanticFingerprint,
  buildSubscriptionSemanticKey,
  buildSubscriptionUrlHash,
} from "./subscription-runtime";

type SubscriptionItem = PasswallDesiredConfig["subscriptions"]["items"][number];

type SubscriptionAuditResult = {
  remark: string;
  subscriptionKey: string;
  urlHash: string;
  enabled: boolean;
  payloadMode:
    | "plain-lines"
    | "base64-lines"
    | "ssd-json"
    | "single-link"
    | "unknown";
  fetchState:
    | "ok"
    | "disabled"
    | "http_error"
    | "network_error"
    | "parse_error";
  httpStatus: number | null;
  checkedAt: string;
  payloadNodeCount: number | null;
  resolvedPayloadNodeCount: number | null;
  payloadFingerprints: ParsedPayloadNode[] | null;
};

type ParsedPayloadNode = {
  fingerprint: string;
};

function normalizeText(value: string | null | undefined) {
  const trimmed = value?.trim();
  return trimmed && trimmed.length > 0 ? trimmed : null;
}

function normalizeLowerText(value: string | null | undefined) {
  return normalizeText(value)?.toLowerCase() ?? null;
}

function safeBase64Decode(value: string) {
  try {
    const normalized = value.replace(/-/g, "+").replace(/_/g, "/");
    const remainder = normalized.length % 4;
    const padded =
      remainder === 0 ? normalized : `${normalized}${"=".repeat(4 - remainder)}`;
    return Buffer.from(padded, "base64").toString("utf8");
  } catch {
    return null;
  }
}

function stripFragment(value: string) {
  return value.split("#")[0] ?? value;
}

function hashParsedNode(value: {
  label?: string | null;
  protocol?: string | null;
  address?: string | null;
  port?: number | null;
  username?: string | null;
  password?: string | null;
  transport?: string | null;
  tls?: boolean | null;
  extras?: Record<string, string | number | boolean | null>;
}) {
  return buildNodeSemanticFingerprint({
    id: "payload",
    label: normalizeText(value.label) ?? "payload-node",
    protocol:
      (normalizeLowerText(value.protocol) as
        | PasswallDesiredConfig["nodes"][number]["protocol"]
        | null) ?? "custom",
    enabled: true,
    group: "payload",
    address: normalizeLowerText(value.address) ?? undefined,
    port: value.port ?? undefined,
    username: normalizeText(value.username) ?? undefined,
    password: normalizeText(value.password) ?? undefined,
    transport:
      (normalizeLowerText(value.transport) as
        | PasswallDesiredConfig["nodes"][number]["transport"]
        | null) ?? undefined,
    tls: value.tls ?? undefined,
    tags: [],
    extras:
      Object.fromEntries(
        Object.entries(value.extras ?? {}).filter(([, entry]) => entry !== null),
      ) ?? {},
  });
}

function parseSsrLine(line: string) {
  const raw = line.slice("ssr://".length);
  const decoded = safeBase64Decode(raw);
  if (!decoded) {
    return null;
  }

  const [main, query = ""] = decoded.split("/?");
  if (!main) {
    return null;
  }
  const parts = main.split(":");
  if (parts.length < 6) {
    return null;
  }

  const address = parts[0];
  const portRaw = parts[1];
  const protocol = parts[2];
  const method = parts[3];
  const obfs = parts[4];
  const passwordEncoded = parts[5];
  if (
    !address ||
    !portRaw ||
    !protocol ||
    !method ||
    !obfs ||
    !passwordEncoded
  ) {
    return null;
  }
  const params = new URLSearchParams(query);
  const label = safeBase64Decode(params.get("remarks") ?? "") ?? null;
  const password = safeBase64Decode(passwordEncoded) ?? passwordEncoded;

  return {
    fingerprint: hashParsedNode({
      label,
      protocol: "shadowsocks-rust",
      address,
      port: Number.parseInt(portRaw, 10),
      password,
      extras: {
        protocol,
        method,
        obfs,
      },
    }),
  } satisfies ParsedPayloadNode;
}

function parseSsLine(line: string) {
  const fragmentIndex = line.indexOf("#");
  const fragment =
    fragmentIndex === -1 ? null : decodeURIComponent(line.slice(fragmentIndex + 1));
  const withoutFragment = fragmentIndex === -1 ? line : line.slice(0, fragmentIndex);
  const payload = withoutFragment.slice("ss://".length);

  let decoded = payload;
  if (!decoded.includes("@")) {
    const decodedPayload = safeBase64Decode(payload);
    if (!decodedPayload) {
      return null;
    }
    decoded = decodedPayload;
  }

  const atIndex = decoded.lastIndexOf("@");
  if (atIndex === -1) {
    return null;
  }

  const credentials = decoded.slice(0, atIndex);
  const server = decoded.slice(atIndex + 1);
  const separatorIndex = credentials.indexOf(":");
  const hostSeparator = server.lastIndexOf(":");
  if (separatorIndex === -1 || hostSeparator === -1) {
    return null;
  }

  return {
    fingerprint: hashParsedNode({
      label: fragment,
      protocol: "shadowsocks-rust",
      address: server.slice(0, hostSeparator),
      port: Number.parseInt(server.slice(hostSeparator + 1), 10),
      password: credentials.slice(separatorIndex + 1),
      extras: {
        method: credentials.slice(0, separatorIndex),
      },
    }),
  } satisfies ParsedPayloadNode;
}

function parseVmessLine(line: string) {
  const decoded = safeBase64Decode(line.slice("vmess://".length));
  if (!decoded) {
    return null;
  }

  const parsed = JSON.parse(decoded) as Record<string, unknown>;
  return {
    fingerprint: hashParsedNode({
      label: typeof parsed.ps === "string" ? parsed.ps : null,
      protocol: "vmess",
      address: typeof parsed.add === "string" ? parsed.add : null,
      port:
        typeof parsed.port === "string"
          ? Number.parseInt(parsed.port, 10)
          : typeof parsed.port === "number"
            ? parsed.port
            : null,
      username: typeof parsed.id === "string" ? parsed.id : null,
      transport: typeof parsed.net === "string" ? parsed.net : null,
      tls:
        typeof parsed.tls === "string"
          ? parsed.tls.toLowerCase() === "tls"
          : Boolean(parsed.tls),
      extras: {
        host: typeof parsed.host === "string" ? parsed.host : null,
        path: typeof parsed.path === "string" ? parsed.path : null,
        sni: typeof parsed.sni === "string" ? parsed.sni : null,
      },
    }),
  } satisfies ParsedPayloadNode;
}

function parseUrlBackedLine(
  line: string,
  protocol: string,
  usernameAs: "username" | "password" = "username",
) {
  try {
    const url = new URL(line);
    const fingerprint = hashParsedNode({
      label: url.hash ? decodeURIComponent(url.hash.slice(1)) : null,
      protocol,
      address: url.hostname,
      port: url.port ? Number.parseInt(url.port, 10) : null,
      username: usernameAs === "username" ? decodeURIComponent(url.username) : null,
      password: usernameAs === "password" ? decodeURIComponent(url.username) : decodeURIComponent(url.password),
      transport: url.searchParams.get("type") ?? url.searchParams.get("network"),
      tls: ["tls", "reality"].includes(
        (url.searchParams.get("security") ?? "").toLowerCase(),
      ),
      extras: {
        host: url.searchParams.get("host"),
        path: url.searchParams.get("path"),
        serviceName: url.searchParams.get("serviceName"),
        sni: url.searchParams.get("sni"),
      },
    });

    return { fingerprint } satisfies ParsedPayloadNode;
  } catch {
    return null;
  }
}

function parseProxyLine(line: string) {
  const normalizedLine = normalizeText(line);
  if (!normalizedLine) {
    return null;
  }

  if (normalizedLine.startsWith("vless://")) {
    return parseUrlBackedLine(normalizedLine, "vless");
  }
  if (normalizedLine.startsWith("trojan://")) {
    return parseUrlBackedLine(normalizedLine, "trojan", "password");
  }
  if (normalizedLine.startsWith("hysteria2://")) {
    return parseUrlBackedLine(normalizedLine, "hysteria2", "password");
  }
  if (normalizedLine.startsWith("vmess://")) {
    return parseVmessLine(normalizedLine);
  }
  if (normalizedLine.startsWith("ss://")) {
    return parseSsLine(normalizedLine);
  }
  if (normalizedLine.startsWith("ssr://")) {
    return parseSsrLine(normalizedLine);
  }

  return null;
}

function splitPayloadLines(value: string) {
  return value
    .split(/\r?\n/)
    .map((entry) => entry.trim())
    .filter((entry) => entry.length > 0 && !entry.startsWith("#"));
}

export function analyzeSubscriptionPayload(body: string) {
  const trimmedBody = normalizeText(body) ?? "";
  const checkedAt = new Date().toISOString();

  if (trimmedBody.startsWith("ssd://")) {
    const decoded = safeBase64Decode(trimmedBody.slice("ssd://".length));
    if (!decoded) {
      return {
        payloadMode: "ssd-json",
        payloadNodeCount: null,
        resolvedPayloadNodeCount: null,
        payloadFingerprints: null,
        checkedAt,
      } as const;
    }

    const parsed = JSON.parse(decoded) as {
      airport?: string;
      port?: number;
      encryption?: string;
      password?: string;
      servers?: Array<Record<string, unknown>>;
    };
    const servers = Array.isArray(parsed.servers) ? parsed.servers : [];
    const payloadFingerprints = servers.map((server) => ({
      fingerprint: hashParsedNode({
        label: typeof server.remarks === "string" ? server.remarks : parsed.airport,
        protocol: "shadowsocks-rust",
        address: typeof server.server === "string" ? server.server : null,
        port:
          typeof server.port === "number"
            ? server.port
            : typeof parsed.port === "number"
              ? parsed.port
              : null,
        password:
          typeof server.password === "string"
            ? server.password
            : typeof parsed.password === "string"
              ? parsed.password
              : null,
        extras: {
          method:
            typeof server.encryption === "string"
              ? server.encryption
              : typeof parsed.encryption === "string"
                ? parsed.encryption
                : null,
        },
      }),
    }));

    return {
      payloadMode: "ssd-json",
      payloadNodeCount: servers.length,
      resolvedPayloadNodeCount: payloadFingerprints.length,
      payloadFingerprints,
      checkedAt,
    } as const;
  }

  const directLines = splitPayloadLines(trimmedBody);
  const directLooksLikeLinks = directLines.every((line) => line.includes("://"));
  if (directLooksLikeLinks && directLines.length > 0) {
    const parsedLines = directLines.map((line) => parseProxyLine(line));
    return {
      payloadMode: directLines.length === 1 ? "single-link" : "plain-lines",
      payloadNodeCount: directLines.length,
      resolvedPayloadNodeCount: parsedLines.filter(Boolean).length,
      payloadFingerprints: parsedLines.filter(Boolean) as ParsedPayloadNode[],
      checkedAt,
    } as const;
  }

  const decodedBody = safeBase64Decode(stripFragment(trimmedBody));
  if (decodedBody) {
    const decodedLines = splitPayloadLines(decodedBody);
    const decodedLooksLikeLinks = decodedLines.every((line) => line.includes("://"));
    if (decodedLooksLikeLinks && decodedLines.length > 0) {
      const parsedLines = decodedLines.map((line) => parseProxyLine(line));
      return {
        payloadMode: "base64-lines",
        payloadNodeCount: decodedLines.length,
        resolvedPayloadNodeCount: parsedLines.filter(Boolean).length,
        payloadFingerprints: parsedLines.filter(Boolean) as ParsedPayloadNode[],
        checkedAt,
      } as const;
    }
  }

  return {
    payloadMode: "unknown",
    payloadNodeCount: null,
    resolvedPayloadNodeCount: null,
    payloadFingerprints: null,
    checkedAt,
  } as const;
}

export async function auditSubscriptions(args: {
  subscriptions: SubscriptionItem[];
  fetchImpl?: typeof fetch;
}) : Promise<SubscriptionAuditResult[]> {
  const fetchImpl = args.fetchImpl ?? fetch;

  return Promise.all(
    args.subscriptions.map(async (subscription) => {
      const subscriptionKey = buildSubscriptionSemanticKey(subscription);
      const urlHash = buildSubscriptionUrlHash(subscription.url);
      const baseResult = {
        remark: subscription.remark,
        subscriptionKey,
        urlHash,
        enabled: subscription.enabled,
      };

      if (!subscription.enabled) {
        return {
          ...baseResult,
          payloadMode: "unknown",
          fetchState: "disabled",
          httpStatus: null,
          checkedAt: new Date().toISOString(),
          payloadNodeCount: null,
          resolvedPayloadNodeCount: null,
          payloadFingerprints: null,
        } satisfies SubscriptionAuditResult;
      }

      try {
        const response = await fetchImpl(subscription.url, {
          signal: AbortSignal.timeout(5000),
          headers: {
            Accept: "text/plain,application/json;q=0.9,*/*;q=0.8",
          },
        });

        if (!response.ok) {
          return {
            ...baseResult,
            payloadMode: "unknown",
            fetchState: "http_error",
            httpStatus: response.status,
            checkedAt: new Date().toISOString(),
            payloadNodeCount: null,
            resolvedPayloadNodeCount: null,
            payloadFingerprints: null,
          } satisfies SubscriptionAuditResult;
        }

        const body = await response.text();
        const analysis = analyzeSubscriptionPayload(body);

        return {
          ...baseResult,
          payloadMode: analysis.payloadMode,
          fetchState:
            analysis.payloadMode === "unknown" ? "parse_error" : "ok",
          httpStatus: response.status,
          checkedAt: analysis.checkedAt,
          payloadNodeCount: analysis.payloadNodeCount,
          resolvedPayloadNodeCount: analysis.resolvedPayloadNodeCount,
          payloadFingerprints: analysis.payloadFingerprints,
        } satisfies SubscriptionAuditResult;
      } catch {
        return {
          ...baseResult,
          payloadMode: "unknown",
          fetchState: "network_error",
          httpStatus: null,
          checkedAt: new Date().toISOString(),
          payloadNodeCount: null,
          resolvedPayloadNodeCount: null,
          payloadFingerprints: null,
        } satisfies SubscriptionAuditResult;
      }
    }),
  );
}
