#!/usr/bin/env node

import { createTRPCProxyClient, httpBatchLink } from "@trpc/client";
import superjson from "superjson";
import { chmodSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(scriptDir, "../../..");
const runtimeDir = path.join(repoRoot, ".codex-runtime", "vectra-panel");
const defaultSessionFile = path.join(runtimeDir, "session.json");
const defaultBaseUrl = "https://router.vectra-pro.net";
const defaultRouterApiBaseUrl = "https://api.vectra-pro.net";
const defaultSshAlias = "vectra-prod";
const defaultRemoteEnvPath = "/opt/vectra-prorouter/.env";
const sessionCookieName = "vectra_operator_session";
const supportedPasswallPackages = [
  "luci-app-passwall2",
  "xray-core",
  "sing-box",
  "hysteria",
  "geoview",
];
const supportedLogSources = ["all", "controller", "passwall", "dnsmasq", "system"];
const operatorTrpcCatalog = {
  draft: {
    queries: ["list", "workspace", "editorSurface", "preview"],
    mutations: ["save", "queueApply", "discard"],
  },
  fleet: {
    queries: ["overview", "list", "monitoring", "pendingImportReviews", "byId"],
    mutations: ["approveImportedBaseline", "requestReimport", "deleteRouter"],
  },
  logs: {
    queries: ["history"],
    mutations: ["queueSnapshot"],
  },
  notifications: {
    queries: ["status"],
    mutations: ["subscribe", "unsubscribe"],
  },
  rescue: {
    queries: ["policy", "openIncidents", "directRouters"],
    mutations: ["triggerDirectMode", "triggerReconnect"],
  },
  terminal: {
    queries: ["history"],
    mutations: ["queueCommand"],
  },
  update: {
    queries: [
      "artifacts",
      "firmwareMatrix",
      "globalTemplateWorkspace",
      "profilesAndGroupsWorkspace",
      "versionDriftWorkspace",
    ],
    mutations: [
      "saveGlobalTemplate",
      "saveRolloutProfile",
      "deleteRolloutProfile",
      "saveRouterGroup",
      "deleteRouterGroup",
      "assignRoutersToGroup",
      "queueGlobalTemplateRollout",
      "queueGroupProfileRollout",
      "queueBulkPasswallPackageUpdate",
      "queueBulkXrayUpdate",
      "queueBulkControllerUpdate",
      "queueBulkRouterReboot",
      "queueControllerUpdate",
      "queueSubscriptionsRefresh",
      "queueRulesRefresh",
      "queuePasswallPackageUpdate",
      "queueFirmwareValidation",
    ],
  },
};
const routerApiCatalog = {
  public: ["POST /api/router/register"],
  authenticated: [
    "POST /api/router/check-in",
    "POST /api/router/job-result",
    "GET /api/router/firmware-manifest/:board",
  ],
};

class CliError extends Error {}

function shellQuote(value) {
  return `'${String(value).replace(/'/g, `'\"'\"'`)}'`;
}

function normalizeBaseUrl(value) {
  if (!value) {
    return value;
  }
  return value.replace(/\/+$/, "");
}

function parseKeyValuePairs(values, label) {
  const pairs = Array.isArray(values) ? values : values ? [values] : [];
  const result = {};

  for (const entry of pairs) {
    const equalIndex = entry.indexOf("=");
    if (equalIndex <= 0) {
      throw new CliError(`${label} entry '${entry}' must use key=value format.`);
    }
    const key = entry.slice(0, equalIndex).trim();
    const value = entry.slice(equalIndex + 1);
    if (!key) {
      throw new CliError(`${label} entry '${entry}' must have a non-empty key.`);
    }
    result[key] = value;
  }

  return result;
}

function parseJsonFile(filePath, label) {
  try {
    return JSON.parse(readFileSync(filePath, "utf8"));
  } catch (error) {
    throw new CliError(
      `Failed to parse JSON from ${label} ${filePath}: ${
        error instanceof Error ? error.message : String(error)
      }`
    );
  }
}

function parseOptionalJsonInput(rawInput) {
  if (typeof rawInput !== "string" || rawInput.trim() === "") {
    return undefined;
  }
  return parseJsonInput(rawInput);
}

function readJsonFile(filePath) {
  if (!existsSync(filePath)) {
    return null;
  }
  return JSON.parse(readFileSync(filePath, "utf8"));
}

function writeJsonFile(filePath, data) {
  mkdirSync(path.dirname(filePath), { recursive: true });
  writeFileSync(filePath, `${JSON.stringify(data, null, 2)}\n`, "utf8");
  chmodSync(filePath, 0o600);
}

function printJson(data) {
  process.stdout.write(`${JSON.stringify(data, null, 2)}\n`);
}

async function parseResponseBody(response) {
  const text = await response.text();

  try {
    return text ? JSON.parse(text) : null;
  } catch {
    return text;
  }
}

function readStdinText() {
  if (process.stdin.isTTY) {
    return "";
  }
  return readFileSync(0, "utf8");
}

function parseJsonInput(rawInput) {
  if (typeof rawInput !== "string" || rawInput.trim() === "") {
    const stdinText = readStdinText().trim();
    if (!stdinText) {
      return undefined;
    }
    try {
      return JSON.parse(stdinText);
    } catch (error) {
      throw new CliError(
        `Failed to parse JSON input from stdin: ${error instanceof Error ? error.message : String(error)}`
      );
    }
  }

  try {
    return JSON.parse(rawInput);
  } catch (error) {
    throw new CliError(
      `Failed to parse JSON from --input: ${error instanceof Error ? error.message : String(error)}`
    );
  }
}

function isUuid(value) {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(
    value,
  );
}

function parseGlobalArgs(argv) {
  const parsed = parseArgs({
    args: argv,
    allowPositionals: true,
    options: {
      "base-url": { type: "string" },
      "router-api-base-url": { type: "string" },
      username: { type: "string" },
      password: { type: "string" },
      "ssh-alias": { type: "string" },
      "ssh-env-path": { type: "string" },
      "session-file": { type: "string" },
      json: { type: "boolean" },
      "force-login": { type: "boolean" },
      "no-ssh-env": { type: "boolean" },
      help: { type: "boolean", short: "h" },
    },
  });

  return {
    positionals: parsed.positionals,
    json: Boolean(parsed.values.json),
    help: Boolean(parsed.values.help),
    forceLogin: Boolean(parsed.values["force-login"]),
    noSshEnv: Boolean(parsed.values["no-ssh-env"]),
    baseUrl: parsed.values["base-url"],
    routerApiBaseUrl: parsed.values["router-api-base-url"],
    username: parsed.values.username,
    password: parsed.values.password,
    sshAlias: parsed.values["ssh-alias"] ?? process.env.VECTRA_PANEL_SSH_ALIAS ?? defaultSshAlias,
    sshEnvPath: parsed.values["ssh-env-path"] ?? process.env.VECTRA_PANEL_SSH_ENV_PATH ?? defaultRemoteEnvPath,
    sessionFile:
      parsed.values["session-file"] ??
      process.env.VECTRA_PANEL_SESSION_FILE ??
      defaultSessionFile,
  };
}

function formatCandidate(router) {
  const label =
    router.displayName ??
    router.hostname ??
    router.deviceIdentifier ??
    router.panelDomain ??
    router.id;
  return `${label} [${router.id}]`;
}

function summarizeFleetRouter(router) {
  return {
    id: router.id,
    displayName: router.displayName ?? null,
    hostname: router.hostname ?? null,
    deviceIdentifier: router.deviceIdentifier,
    panelDomain: router.panelDomain ?? null,
    status: router.status,
    importState: router.importState,
    lastSeenAt: router.lastSeenAt ?? null,
    lastCheckInAt: router.lastCheckInAt ?? null,
    queuedJobCount: router.queuedJobCount ?? 0,
    supportState: router.support?.state ?? null,
    configTrust: router.configTrust?.state ?? null,
    controllerVersion: router.latestSnapshot?.controllerVersion ?? null,
    passwallAppVersion: router.latestSnapshot?.passwallAppVersion ?? null,
  };
}

function summarizeRouterDetails(details) {
  const latestSnapshotPayload = details.latestSnapshot?.payload ?? null;
  return {
    router: {
      id: details.router.id,
      displayName: details.router.displayName ?? null,
      hostname: details.router.hostname ?? null,
      deviceIdentifier: details.router.deviceIdentifier,
      panelDomain: details.router.panelDomain ?? null,
      model: details.router.model ?? null,
      boardName: details.router.boardName ?? null,
      target: details.router.target ?? null,
      architecture: details.router.architecture ?? null,
      openwrtRelease: details.router.openwrtRelease ?? null,
      status: details.router.status,
      importState: details.router.importState,
      controllerChannel: details.router.controllerChannel,
      lastSeenAt: details.router.lastSeenAt ?? null,
      lastCheckInAt: details.router.lastCheckInAt ?? null,
      lastDirectModeAt: details.router.lastDirectModeAt ?? null,
      lastRescueReason: details.router.lastRescueReason ?? null,
      activeRevisionId: details.router.activeRevisionId ?? null,
      pendingImportRevisionId: details.router.pendingImportRevisionId ?? null,
    },
    latestSnapshot: details.latestSnapshot
      ? {
          id: details.latestSnapshot.id,
          createdAt: details.latestSnapshot.createdAt,
          passwallEnabled: details.latestSnapshot.passwallEnabled,
          selectedNodeId: details.latestSnapshot.selectedNodeId,
          nodeCount: details.latestSnapshot.nodeCount,
          subscriptionCount: details.latestSnapshot.subscriptionCount,
          controllerVersion: details.latestSnapshot.controllerVersion ?? null,
          passwallAppVersion: details.latestSnapshot.passwallAppVersion ?? null,
          model: latestSnapshotPayload?.model ?? null,
          boardName: latestSnapshotPayload?.boardName ?? null,
          target: latestSnapshotPayload?.target ?? null,
          architecture: latestSnapshotPayload?.architecture ?? null,
          openwrtRelease: latestSnapshotPayload?.openwrtRelease ?? null,
          configDigest: latestSnapshotPayload?.configDigest ?? null,
          serviceHealth: latestSnapshotPayload?.serviceHealth ?? null,
          resources: latestSnapshotPayload?.resources ?? null,
          packageVersions: latestSnapshotPayload?.packageVersions ?? null,
          binaryVersions: latestSnapshotPayload?.binaryVersions ?? null,
          telegramReachability: latestSnapshotPayload?.telegramReachability ?? null,
          youtubeReachability: latestSnapshotPayload?.youtubeReachability ?? null,
        }
      : null,
    support: details.support ?? null,
    recentJobs: Array.isArray(details.recentJobs)
      ? details.recentJobs.slice(0, 8).map((job) => ({
          id: job.id,
          type: job.type,
          state: job.state,
          createdAt: job.createdAt,
          deliveredAt: job.deliveredAt ?? null,
          completedAt: job.completedAt ?? null,
        }))
      : [],
    incidents: details.incidents ?? [],
    applyReceipts: Array.isArray(details.applyReceipts)
      ? details.applyReceipts.slice(0, 5)
      : [],
  };
}

function summarizeDraftRevision(revision) {
  if (!revision) {
    return null;
  }

  return {
    id: revision.id,
    routerId: revision.routerId,
    revisionNumber: revision.revisionNumber,
    status: revision.status,
    origin: revision.origin,
    configDigest: revision.configDigest ?? null,
    note: revision.note ?? null,
    approvedAt: revision.approvedAt ?? null,
    createdAt: revision.createdAt ?? null,
    hasRawImportedSnapshot: revision.hasRawImportedSnapshot ?? false,
    impact: revision.impact
      ? {
          changedSections: revision.impact.changedSections ?? [],
          requiresRestart: Boolean(revision.impact.requiresRestart),
          refreshSubscriptions: Boolean(revision.impact.refreshSubscriptions),
          refreshRules: Boolean(revision.impact.refreshRules),
          packageInstall: Boolean(revision.impact.packageInstall),
          firmwareValidation: Boolean(revision.impact.firmwareValidation),
        }
      : null,
  };
}

class VectraPanelClient {
  constructor(options) {
    this.options = options;
    this.sessionFile = path.resolve(options.sessionFile);
    this.session = readJsonFile(this.sessionFile);
  }

  usage() {
    const lines = [
      "Vectra panel CLI",
      "",
      "Usage:",
      "  bash ./scripts/VectraPanelCli.sh <command> [options]",
      "",
      "Global options:",
      "  --base-url URL          Override operator panel URL",
      "  --router-api-base-url URL",
      "                          Override router-facing API URL",
      "  --username VALUE        Use operator username from CLI",
      "  --password VALUE        Use operator password from CLI",
      "  --ssh-alias NAME        SSH alias used to read production .env (default: vectra-prod)",
      "  --ssh-env-path PATH     Remote .env path (default: /opt/vectra-prorouter/.env)",
      "  --session-file PATH     Override cached session file",
      "  --force-login           Refresh cookie even if cached session exists",
      "  --no-ssh-env            Do not read credentials from the VPS over SSH",
      "  --json                  Keep raw JSON output where a command has a summarized default",
      "",
      "Commands:",
      "  catalog",
      "  status",
      "  login",
      "  logout",
      "  fleet overview|list|monitoring|pending-imports",
      "  fleet approve-import <selector> [--revision-id UUID]",
      "  fleet request-reimport <selector>",
      "  fleet delete <selector> --yes",
      "  router show <selector>",
      "  draft list|workspace [selector]|editor <selector>|save <selector> [--config JSON|--config-file PATH] [--note TEXT]|queue-apply <selector> [--revision-id UUID]|discard <selector> --revision-id UUID",
      "  logs history <selector>",
      "  logs snapshot <selector> [--source all|controller|passwall|dnsmasq|system] [--lines N]",
      "  notifications status|subscribe --input '{...}'|unsubscribe --endpoint URL",
      "  terminal history <selector>",
      "  terminal run <selector> --command 'ubus call system board' [--timeout N]",
      "  update controller <selector> [--channel stable|beta]",
      "  update passwall <selector> [--channel stable|beta] [--package xray-core ...]",
      "  update rules <selector>",
      "  update subscriptions <selector>",
      "  rescue direct <selector> [--reason TEXT]",
      "  rescue reconnect <selector> [--keep-rescue]",
      "  router-api health",
      "  router-api register --input '{...}'",
      "  router-api check-in --router-id UUID --router-token TOKEN --input '{...}'",
      "  router-api job-result --router-id UUID --router-token TOKEN --input '{...}'",
      "  router-api firmware-manifest <board> --router-id UUID --router-token TOKEN [--query key=value]",
      "  call <trpc.path> [--mutation] [--input '{\"routerId\":\"...\"}']",
      "",
      "Selector matches router id, displayName, hostname, panelDomain, or deviceIdentifier.",
      "Credentials are resolved in this order: CLI flags -> env vars -> SSH read of production .env.",
      "Only the operator cookie is cached locally under .codex-runtime/vectra-panel/.",
      "",
      "Examples:",
      "  bash ./scripts/VectraPanelCli.sh status",
      "  bash ./scripts/VectraPanelCli.sh fleet overview",
      "  bash ./scripts/VectraPanelCli.sh catalog",
      "  bash ./scripts/VectraPanelCli.sh fleet list",
      "  bash ./scripts/VectraPanelCli.sh router show OpenWrt",
      "  bash ./scripts/VectraPanelCli.sh draft workspace OpenWrt",
      "  bash ./scripts/VectraPanelCli.sh notifications status",
      "  bash ./scripts/VectraPanelCli.sh logs snapshot OpenWrt --source system --lines 100",
      "  bash ./scripts/VectraPanelCli.sh terminal run OpenWrt --command 'ubus call system board'",
      "  bash ./scripts/VectraPanelCli.sh router-api health",
      "  bash ./scripts/VectraPanelCli.sh update controller OpenWrt",
      "  bash ./scripts/VectraPanelCli.sh call fleet.monitoring",
    ];
    process.stdout.write(`${lines.join("\n")}\n`);
  }

  readRemoteEnv(keys) {
    const remotePython = [
      "import json, pathlib, sys",
      "env_path = pathlib.Path(sys.argv[1])",
      "requested = json.loads(sys.argv[2])",
      "values = {}",
      "if env_path.exists():",
      "    for raw in env_path.read_text(encoding='utf-8').splitlines():",
      "        line = raw.strip()",
      "        if not line or line.startswith('#') or '=' not in line:",
      "            continue",
      "        key, value = line.split('=', 1)",
      "        key = key.strip()",
      "        value = value.strip()",
      "        if len(value) >= 2 and value[0] == value[-1] and value[0] in {'\"', \"'\"}:",
      "            value = value[1:-1]",
      "        values[key] = value",
      "print(json.dumps({key: values.get(key) for key in requested}))",
    ].join("\n");
    const remoteCommand = `python3 - ${shellQuote(this.options.sshEnvPath)} ${shellQuote(
      JSON.stringify(keys),
    )}`;
    const result = spawnSync("ssh", ["-o", "BatchMode=yes", this.options.sshAlias, remoteCommand], {
      input: remotePython,
      encoding: "utf8",
    });

    if (result.status !== 0) {
      const text = (result.stderr || result.stdout || "").trim();
      throw new CliError(
        `Failed to read operator credentials from ${this.options.sshAlias}:${this.options.sshEnvPath}. ${text}`,
      );
    }

    try {
      return JSON.parse(result.stdout);
    } catch (error) {
      throw new CliError(
        `Failed to parse remote env payload from ${this.options.sshAlias}: ${
          error instanceof Error ? error.message : String(error)
        }`,
      );
    }
  }

  resolveConfig({ requireCredentials }) {
    let baseUrl =
      this.options.baseUrl ??
      process.env.VECTRA_PANEL_BASE_URL ??
      process.env.VECTRA_DEFAULT_CONTROL_DOMAIN ??
      this.session?.baseUrl ??
      null;
    let routerApiBaseUrl =
      this.options.routerApiBaseUrl ??
      process.env.VECTRA_ROUTER_API_BASE_URL ??
      this.session?.routerApiBaseUrl ??
      null;
    let username =
      this.options.username ?? process.env.VECTRA_OPERATOR_USER ?? null;
    let password =
      this.options.password ?? process.env.VECTRA_OPERATOR_PASSWORD ?? null;
    let source = "local";

    if (
      (!baseUrl || !routerApiBaseUrl || (requireCredentials && (!username || !password))) &&
      !this.options.noSshEnv
    ) {
      const remote = this.readRemoteEnv([
        "VECTRA_DEFAULT_CONTROL_DOMAIN",
        "VECTRA_ROUTER_API_BASE_URL",
        "VECTRA_OPERATOR_USER",
        "VECTRA_OPERATOR_PASSWORD",
      ]);
      baseUrl ||= remote.VECTRA_DEFAULT_CONTROL_DOMAIN ?? null;
      routerApiBaseUrl ||= remote.VECTRA_ROUTER_API_BASE_URL ?? null;
      username ||= remote.VECTRA_OPERATOR_USER ?? null;
      password ||= remote.VECTRA_OPERATOR_PASSWORD ?? null;
      source = "ssh";
    }

    baseUrl = normalizeBaseUrl(baseUrl ?? defaultBaseUrl);
    routerApiBaseUrl = normalizeBaseUrl(routerApiBaseUrl ?? defaultRouterApiBaseUrl);

    if (requireCredentials && (!username || !password)) {
      throw new CliError(
        "Operator credentials were not found. Pass --username/--password, export VECTRA_OPERATOR_USER/VECTRA_OPERATOR_PASSWORD, or allow SSH env lookup via vectra-prod.",
      );
    }

    return {
      baseUrl,
      routerApiBaseUrl,
      username,
      password,
      source,
    };
  }

  saveSession(session) {
    this.session = session;
    writeJsonFile(this.sessionFile, session);
  }

  clearSession() {
    this.session = null;
    if (existsSync(this.sessionFile)) {
      rmSync(this.sessionFile);
    }
  }

  async login({ force = false } = {}) {
    if (this.session && !force) {
      return this.session;
    }

    const config = this.resolveConfig({ requireCredentials: true });
    const body = new URLSearchParams({
      username: config.username,
      password: config.password,
    });
    const response = await fetch(`${config.baseUrl}/api/operator/login`, {
      method: "POST",
      headers: {
        "content-type": "application/x-www-form-urlencoded",
      },
      body,
      redirect: "manual",
    });
    const setCookie = response.headers.get("set-cookie");
    const cookie = setCookie
      ?.split(/,(?=\s*[A-Za-z0-9_-]+=)/)[0]
      ?.split(";")
      .map((part) => part.trim())
      .find((part) => part.startsWith(`${sessionCookieName}=`));

    if (!cookie) {
      throw new CliError(
        `Operator login failed at ${config.baseUrl}/api/operator/login (status ${response.status}).`,
      );
    }

    const session = {
      baseUrl: config.baseUrl,
      routerApiBaseUrl: config.routerApiBaseUrl,
      cookie,
      updatedAt: new Date().toISOString(),
      authSource: config.source,
      sessionFile: this.sessionFile,
    };
    this.saveSession(session);
    return session;
  }

  isUnauthorizedError(error) {
    const message = String(error?.message ?? error);
    return (
      message.includes("UNAUTHORIZED") ||
      message.includes("Operator session required") ||
      message.includes("401")
    );
  }

  async ensureSession(force = false) {
    if (!force && this.session) {
      return this.session;
    }
    return this.login({ force: true });
  }

  createClient(session) {
    return createTRPCProxyClient({
      links: [
        httpBatchLink({
          url: `${session.baseUrl}/api/trpc`,
          transformer: superjson,
          headers() {
            return {
              cookie: session.cookie,
              "x-trpc-source": "vectra-panel-cli",
            };
          },
        }),
      ],
    });
  }

  async invokeTrpc(pathValue, kind, input, allowRetry = true) {
    const session = await this.ensureSession(this.options.forceLogin);
    const client = this.createClient(session);
    let target = client;
    for (const segment of pathValue.split(".")) {
      target = target?.[segment];
    }

    if (!target || typeof target[kind] !== "function") {
      throw new CliError(`Unknown tRPC ${kind} path: ${pathValue}`);
    }

    try {
      return await target[kind](input);
    } catch (error) {
      if (allowRetry && this.isUnauthorizedError(error)) {
        await this.login({ force: true });
        return this.invokeTrpc(pathValue, kind, input, false);
      }
      throw error;
    }
  }

  async query(pathValue, input) {
    return this.invokeTrpc(pathValue, "query", input);
  }

  async mutate(pathValue, input) {
    return this.invokeTrpc(pathValue, "mutate", input);
  }

  async fetchAppHealth() {
    const config = this.resolveConfig({ requireCredentials: false });
    const response = await fetch(`${config.baseUrl}/api/health`);

    return {
      status: response.status,
      body: await parseResponseBody(response),
      baseUrl: config.baseUrl,
    };
  }

  async fetchRouterApiHealth() {
    const config = this.resolveConfig({ requireCredentials: false });
    const [healthzResponse, apiHealthResponse] = await Promise.all([
      fetch(`${config.routerApiBaseUrl}/healthz`),
      fetch(`${config.routerApiBaseUrl}/api/health`),
    ]);

    return {
      baseUrl: config.routerApiBaseUrl,
      healthz: {
        status: healthzResponse.status,
        body: await parseResponseBody(healthzResponse),
      },
      appHealth: {
        status: apiHealthResponse.status,
        body: await parseResponseBody(apiHealthResponse),
      },
    };
  }

  async routerApiFetch(pathname, options = {}) {
    const config = this.resolveConfig({ requireCredentials: false });
    const url = new URL(pathname, `${config.routerApiBaseUrl}/`);
    if (options.query && typeof options.query === "object") {
      for (const [key, value] of Object.entries(options.query)) {
        if (value !== undefined && value !== null) {
          url.searchParams.set(key, String(value));
        }
      }
    }

    const headers = new Headers(options.headers ?? {});
    if (options.routerId) {
      headers.set("x-vectra-router-id", options.routerId);
    }
    if (options.routerToken) {
      headers.set("x-vectra-router-token", options.routerToken);
    }
    if (options.body !== undefined && !headers.has("content-type")) {
      headers.set("content-type", "application/json");
    }

    const response = await fetch(url, {
      method: options.method ?? "GET",
      headers,
      body:
        options.body === undefined
          ? undefined
          : typeof options.body === "string"
            ? options.body
            : JSON.stringify(options.body),
    });
    return {
      ok: response.ok,
      status: response.status,
      url: url.toString(),
      body: await parseResponseBody(response),
    };
  }

  async status() {
    const [health, routerApiHealth] = await Promise.all([
      this.fetchAppHealth(),
      this.fetchRouterApiHealth(),
    ]);
    return {
      baseUrl: health.baseUrl,
      routerApiBaseUrl: routerApiHealth.baseUrl,
      sessionFile: this.sessionFile,
      sessionPresent: Boolean(this.session?.cookie),
      sessionUpdatedAt: this.session?.updatedAt ?? null,
      authSource: this.session?.authSource ?? null,
      sshAlias: this.options.sshAlias,
      sshEnvPath: this.options.sshEnvPath,
      appHealth: {
        status: health.status,
        body: health.body,
      },
      routerApiHealth,
    };
  }

  async resolveRouter(selector) {
    if (!selector) {
      throw new CliError("Router selector is required.");
    }

    if (isUuid(selector)) {
      const details = await this.query("fleet.byId", { routerId: selector });
      return {
        id: details.router.id,
        summary: details.router,
        details,
      };
    }

    const fleet = await this.query("fleet.list");
    const needle = selector.trim().toLowerCase();
    const matches = fleet.filter((router) => {
      const candidates = [
        router.id,
        router.displayName,
        router.hostname,
        router.panelDomain,
        router.deviceIdentifier,
      ]
        .filter((value) => typeof value === "string" && value.trim().length > 0)
        .map((value) => value.toLowerCase());

      return candidates.some((value) => value === needle || value.includes(needle));
    });

    if (matches.length === 0) {
      throw new CliError(`Router '${selector}' was not found.`);
    }
    if (matches.length > 1) {
      throw new CliError(
        `Router selector '${selector}' is ambiguous: ${matches.map(formatCandidate).join(", ")}`,
      );
    }

    const details = await this.query("fleet.byId", { routerId: matches[0].id });
    return {
      id: matches[0].id,
      summary: matches[0],
      details,
    };
  }
}

function parseSubcommandArgs(args, options) {
  const parsed = parseArgs({
    args,
    allowPositionals: true,
    options,
  });
  return {
    values: parsed.values,
    positionals: parsed.positionals,
  };
}

async function runCatalog(client) {
  const config = client.resolveConfig({ requireCredentials: false });
  return {
    operatorApi: {
      baseUrl: config.baseUrl,
      login: "POST /api/operator/login",
      trpc: "/api/trpc",
      auth: {
        mode: "operator_cookie",
        cookieName: sessionCookieName,
      },
      routers: operatorTrpcCatalog,
      note:
        "Any protected tRPC procedure can also be reached generically through 'call <router.procedure>' with --mutation when needed.",
    },
    routerApi: {
      baseUrl: config.routerApiBaseUrl,
      auth: {
        mode: "router_headers",
        headers: ["x-vectra-router-id", "x-vectra-router-token"],
      },
      endpoints: routerApiCatalog,
      limitation:
        "Operator auth does not mint router auth. Router tokens are separate credentials and are not recoverable from hashed DB storage.",
    },
  };
}

async function runFleet(client, args, rawJson) {
  const subcommand = args[0] ?? "overview";

  switch (subcommand) {
    case "overview":
      return client.query("fleet.overview");
    case "monitoring":
      return client.query("fleet.monitoring");
    case "pending-imports": {
      const result = await client.query("fleet.pendingImportReviews");
      return rawJson
        ? result
        : result.map((entry) => ({
            id: entry.router.id,
            displayName: entry.router.displayName ?? null,
            deviceIdentifier: entry.router.deviceIdentifier,
            importState: entry.router.importState,
            pendingRevisionId: entry.pendingRevision?.id ?? null,
            pendingRevisionNumber: entry.pendingRevision?.revisionNumber ?? null,
          }));
    }
    case "list": {
      const result = await client.query("fleet.list");
      return rawJson ? result : result.map(summarizeFleetRouter);
    }
    case "approve-import": {
      const selector = args[1];
      if (!selector) {
        throw new CliError("Usage: fleet approve-import <selector> [--revision-id UUID]");
      }
      const parsed = parseSubcommandArgs(args.slice(2), {
        "revision-id": { type: "string" },
      });
      const router = await client.resolveRouter(selector);
      const result = await client.mutate("fleet.approveImportedBaseline", {
        routerId: router.id,
        revisionId: parsed.values["revision-id"],
      });
      return {
        router: summarizeFleetRouter(router.summary),
        approvedRevision: result.revision,
      };
    }
    case "request-reimport": {
      const selector = args[1];
      if (!selector) {
        throw new CliError("Usage: fleet request-reimport <selector>");
      }
      const router = await client.resolveRouter(selector);
      const result = await client.mutate("fleet.requestReimport", {
        routerId: router.id,
      });
      return {
        router: summarizeFleetRouter(router.summary),
        updatedRouter: result,
      };
    }
    case "delete": {
      const selector = args[1];
      if (!selector) {
        throw new CliError("Usage: fleet delete <selector> --yes");
      }
      const parsed = parseSubcommandArgs(args.slice(2), {
        yes: { type: "boolean" },
      });
      if (!parsed.values.yes) {
        throw new CliError("fleet delete is destructive and requires --yes.");
      }
      const router = await client.resolveRouter(selector);
      const result = await client.mutate("fleet.deleteRouter", {
        routerId: router.id,
      });
      return {
        router: summarizeFleetRouter(router.summary),
        deletedRouter: result.router,
      };
    }
    default:
      throw new CliError(`Unknown fleet subcommand: ${subcommand}`);
  }
}

async function runRouter(client, args) {
  if (args[0] !== "show" || !args[1]) {
    throw new CliError("Usage: router show <selector>");
  }
  const resolved = await client.resolveRouter(args[1]);
  return summarizeRouterDetails(resolved.details);
}

async function runDraft(client, args, rawJson) {
  const subcommand = args[0] ?? "list";

  switch (subcommand) {
    case "list": {
      const result = await client.query("draft.list");
      return rawJson ? result : result.map(summarizeDraftRevision);
    }
    case "workspace": {
      const selector = args[1];
      if (!selector) {
        const result = await client.query("draft.workspace");
        return rawJson
          ? result
          : {
              routers: result.routers,
              selectedRouter: result.selectedRouter
                ? summarizeFleetRouter(result.selectedRouter)
                : null,
              importedRevision: summarizeDraftRevision(result.importedRevision),
              activeRevision: summarizeDraftRevision(result.activeRevision),
              latestDraft: summarizeDraftRevision(result.latestDraft),
              workspaceRevision: summarizeDraftRevision(result.workspaceRevision),
            };
      }
      const router = await client.resolveRouter(selector);
      const result = await client.query("draft.workspace", { routerId: router.id });
      return rawJson
        ? result
        : {
            routers: result.routers,
            selectedRouter: result.selectedRouter
              ? summarizeFleetRouter(result.selectedRouter)
              : null,
            importedRevision: summarizeDraftRevision(result.importedRevision),
            activeRevision: summarizeDraftRevision(result.activeRevision),
            latestDraft: summarizeDraftRevision(result.latestDraft),
            workspaceRevision: summarizeDraftRevision(result.workspaceRevision),
          };
    }
    case "editor": {
      const selector = args[1];
      if (!selector) {
        throw new CliError("Usage: draft editor <selector>");
      }
      const router = await client.resolveRouter(selector);
      return client.query("draft.editorSurface", { routerId: router.id });
    }
    case "save": {
      const selector = args[1];
      if (!selector) {
        throw new CliError(
          "Usage: draft save <selector> [--input '{...}' | --config JSON | --config-file PATH] [--note TEXT]"
        );
      }
      const parsed = parseSubcommandArgs(args.slice(2), {
        input: { type: "string" },
        config: { type: "string" },
        "config-file": { type: "string" },
        note: { type: "string" },
      });
      const router = await client.resolveRouter(selector);
      const explicitInput = parseOptionalJsonInput(parsed.values.input);

      let payload;
      if (explicitInput !== undefined) {
        payload = explicitInput;
      } else {
        const config =
          parsed.values["config-file"]
            ? parseJsonFile(parsed.values["config-file"], "--config-file")
            : parseJsonInput(parsed.values.config);
        payload = {
          routerId: router.id,
          note: parsed.values.note,
          config,
        };
      }

      if (!payload || typeof payload !== "object" || Array.isArray(payload)) {
        throw new CliError("draft save payload must be a JSON object.");
      }

      return client.mutate("draft.save", {
        ...payload,
        routerId: payload.routerId ?? router.id,
      });
    }
    case "queue-apply": {
      const selector = args[1];
      if (!selector) {
        throw new CliError("Usage: draft queue-apply <selector> [--revision-id UUID]");
      }
      const parsed = parseSubcommandArgs(args.slice(2), {
        "revision-id": { type: "string" },
      });
      const router = await client.resolveRouter(selector);
      let desiredRevisionId = parsed.values["revision-id"];

      if (!desiredRevisionId) {
        const workspace = await client.query("draft.workspace", { routerId: router.id });
        desiredRevisionId =
          workspace.latestDraft?.id ??
          workspace.workspaceRevision?.id ??
          workspace.activeRevision?.id ??
          null;
      }

      if (!desiredRevisionId) {
        throw new CliError(
          "No revision id was provided and no draft/workspace revision was found for this router."
        );
      }

      return client.mutate("draft.queueApply", {
        routerId: router.id,
        desiredRevisionId,
      });
    }
    case "discard": {
      const selector = args[1];
      if (!selector) {
        throw new CliError("Usage: draft discard <selector> --revision-id UUID");
      }
      const parsed = parseSubcommandArgs(args.slice(2), {
        "revision-id": { type: "string" },
      });
      const revisionId = parsed.values["revision-id"];
      if (!revisionId) {
        throw new CliError("Usage: draft discard <selector> --revision-id UUID");
      }
      const router = await client.resolveRouter(selector);
      return client.mutate("draft.discard", {
        routerId: router.id,
        revisionId,
      });
    }
    default:
      throw new CliError(`Unknown draft subcommand: ${subcommand}`);
  }
}

async function runLogs(client, args) {
  const subcommand = args[0];
  const selector = args[1];

  if (!subcommand || !selector) {
    throw new CliError("Usage: logs history <selector> | logs snapshot <selector> [--source ...] [--lines N]");
  }

  const router = await client.resolveRouter(selector);
  if (subcommand === "history") {
    return client.query("logs.history", { routerId: router.id });
  }

  if (subcommand === "snapshot") {
    const parsed = parseSubcommandArgs(args.slice(2), {
      source: { type: "string" },
      lines: { type: "string" },
    });
    const source = parsed.values.source ?? "all";
    const lines = parsed.values.lines ? Number(parsed.values.lines) : 200;
    if (!supportedLogSources.includes(source)) {
      throw new CliError(`Unsupported log source '${source}'. Expected one of: ${supportedLogSources.join(", ")}`);
    }
    if (!Number.isInteger(lines) || lines < 50 || lines > 400) {
      throw new CliError("Log snapshot lines must be an integer between 50 and 400.");
    }
    const job = await client.mutate("logs.queueSnapshot", {
      routerId: router.id,
      source,
      lines,
    });
    return {
      router: summarizeFleetRouter(router.summary),
      job,
    };
  }

  throw new CliError(`Unknown logs subcommand: ${subcommand}`);
}

async function runNotifications(client, args) {
  const subcommand = args[0] ?? "status";

  switch (subcommand) {
    case "status":
      return client.query("notifications.status");
    case "subscribe": {
      const parsed = parseSubcommandArgs(args.slice(1), {
        input: { type: "string" },
      });
      const input = parseJsonInput(parsed.values.input);
      if (!input || typeof input !== "object" || Array.isArray(input)) {
        throw new CliError("notifications subscribe requires an object payload via --input or stdin.");
      }
      return client.mutate("notifications.subscribe", input);
    }
    case "unsubscribe": {
      const parsed = parseSubcommandArgs(args.slice(1), {
        endpoint: { type: "string" },
      });
      if (!parsed.values.endpoint) {
        throw new CliError("notifications unsubscribe requires --endpoint.");
      }
      return client.mutate("notifications.unsubscribe", {
        endpoint: parsed.values.endpoint,
      });
    }
    default:
      throw new CliError(`Unknown notifications subcommand: ${subcommand}`);
  }
}

async function runTerminal(client, args) {
  const subcommand = args[0];
  const selector = args[1];

  if (!subcommand || !selector) {
    throw new CliError("Usage: terminal history <selector> | terminal run <selector> --command '...'");
  }

  const router = await client.resolveRouter(selector);
  if (subcommand === "history") {
    return client.query("terminal.history", { routerId: router.id });
  }

  if (subcommand === "run") {
    const parsed = parseSubcommandArgs(args.slice(2), {
      command: { type: "string" },
      timeout: { type: "string" },
    });
    const command = parsed.values.command;
    const timeoutSeconds = parsed.values.timeout ? Number(parsed.values.timeout) : 30;

    if (!command || command.trim().length === 0) {
      throw new CliError("terminal run requires --command.");
    }
    if (!Number.isInteger(timeoutSeconds) || timeoutSeconds < 5 || timeoutSeconds > 120) {
      throw new CliError("Terminal timeout must be an integer between 5 and 120 seconds.");
    }

    const job = await client.mutate("terminal.queueCommand", {
      routerId: router.id,
      command,
      timeoutSeconds,
    });
    return {
      router: summarizeFleetRouter(router.summary),
      job,
    };
  }

  throw new CliError(`Unknown terminal subcommand: ${subcommand}`);
}

async function runUpdate(client, args) {
  const subcommand = args[0];
  const selector = args[1];

  if (!subcommand || !selector) {
    throw new CliError(
      "Usage: update controller <selector> | update passwall <selector> [--package ...] | update rules <selector> | update subscriptions <selector>",
    );
  }

  const router = await client.resolveRouter(selector);
  switch (subcommand) {
    case "controller": {
      const parsed = parseSubcommandArgs(args.slice(2), {
        channel: { type: "string" },
      });
      const channel = parsed.values.channel ?? "stable";
      const job = await client.mutate("update.queueControllerUpdate", {
        routerId: router.id,
        channel,
      });
      return {
        router: summarizeFleetRouter(router.summary),
        job,
      };
    }
    case "passwall": {
      const parsed = parseSubcommandArgs(args.slice(2), {
        channel: { type: "string" },
        package: { type: "string", multiple: true },
      });
      const channel = parsed.values.channel ?? "stable";
      const packages = parsed.values.package;
      if (packages && packages.some((pkg) => !supportedPasswallPackages.includes(pkg))) {
        throw new CliError(
          `Unsupported package name. Expected one of: ${supportedPasswallPackages.join(", ")}`,
        );
      }
      const job = await client.mutate("update.queuePasswallPackageUpdate", {
        routerId: router.id,
        artifactChannel: channel,
        packages,
      });
      return {
        router: summarizeFleetRouter(router.summary),
        job,
      };
    }
    case "rules": {
      const job = await client.mutate("update.queueRulesRefresh", {
        routerId: router.id,
      });
      return {
        router: summarizeFleetRouter(router.summary),
        job,
      };
    }
    case "subscriptions": {
      const job = await client.mutate("update.queueSubscriptionsRefresh", {
        routerId: router.id,
      });
      return {
        router: summarizeFleetRouter(router.summary),
        job,
      };
    }
    default:
      throw new CliError(`Unknown update subcommand: ${subcommand}`);
  }
}

async function runRescue(client, args) {
  const subcommand = args[0];
  const selector = args[1];

  if (!subcommand || !selector) {
    throw new CliError("Usage: rescue direct <selector> [--reason TEXT] | rescue reconnect <selector> [--keep-rescue]");
  }

  const router = await client.resolveRouter(selector);
  switch (subcommand) {
    case "direct": {
      const parsed = parseSubcommandArgs(args.slice(2), {
        reason: { type: "string" },
      });
      const job = await client.mutate("rescue.triggerDirectMode", {
        routerId: router.id,
        reason: parsed.values.reason ?? "Оператор запросил прямой режим",
      });
      return {
        router: summarizeFleetRouter(router.summary),
        job,
      };
    }
    case "reconnect": {
      const parsed = parseSubcommandArgs(args.slice(2), {
        "keep-rescue": { type: "boolean" },
      });
      const job = await client.mutate("rescue.triggerReconnect", {
        routerId: router.id,
        clearRescue: !parsed.values["keep-rescue"],
      });
      return {
        router: summarizeFleetRouter(router.summary),
        job,
      };
    }
    default:
      throw new CliError(`Unknown rescue subcommand: ${subcommand}`);
  }
}

async function runRouterApi(client, args) {
  const subcommand = args[0] ?? "health";

  switch (subcommand) {
    case "health":
      return client.fetchRouterApiHealth();
    case "register": {
      const parsed = parseSubcommandArgs(args.slice(1), {
        input: { type: "string" },
      });
      const input = parseJsonInput(parsed.values.input);
      return client.routerApiFetch("/api/router/register", {
        method: "POST",
        body: input,
      });
    }
    case "check-in":
    case "job-result": {
      const parsed = parseSubcommandArgs(args.slice(1), {
        "router-id": { type: "string" },
        "router-token": { type: "string" },
        input: { type: "string" },
      });
      const routerId = parsed.values["router-id"];
      const routerToken = parsed.values["router-token"];
      const input = parseJsonInput(parsed.values.input);

      if (!routerId || !routerToken) {
        throw new CliError(`${subcommand} requires --router-id and --router-token.`);
      }
      if (!input || typeof input !== "object" || Array.isArray(input)) {
        throw new CliError(`${subcommand} requires an object payload via --input or stdin.`);
      }
      if (input.routerId && input.routerId !== routerId) {
        throw new CliError(
          `${subcommand} payload routerId does not match the --router-id header value.`
        );
      }

      return client.routerApiFetch(`/api/router/${subcommand}`, {
        method: "POST",
        routerId,
        routerToken,
        body: {
          ...input,
          routerId,
        },
      });
    }
    case "firmware-manifest": {
      const board = args[1];
      if (!board) {
        throw new CliError(
          "Usage: router-api firmware-manifest <board> --router-id UUID --router-token TOKEN [--query key=value]"
        );
      }
      const parsed = parseSubcommandArgs(args.slice(2), {
        "router-id": { type: "string" },
        "router-token": { type: "string" },
        query: { type: "string", multiple: true },
      });
      const routerId = parsed.values["router-id"];
      const routerToken = parsed.values["router-token"];
      if (!routerId || !routerToken) {
        throw new CliError("router-api firmware-manifest requires --router-id and --router-token.");
      }

      return client.routerApiFetch(`/api/router/firmware-manifest/${encodeURIComponent(board)}`, {
        routerId,
        routerToken,
        query: parseKeyValuePairs(parsed.values.query, "--query"),
      });
    }
    default:
      throw new CliError(`Unknown router-api subcommand: ${subcommand}`);
  }
}

async function runCall(client, args) {
  const pathValue = args[0];
  if (!pathValue) {
    throw new CliError("Usage: call <trpc.path> [--mutation] [--input '{...}']");
  }

  const parsed = parseSubcommandArgs(args.slice(1), {
    mutation: { type: "boolean" },
    input: { type: "string" },
  });
  const input = parseJsonInput(parsed.values.input);
  return parsed.values.mutation
    ? client.mutate(pathValue, input)
    : client.query(pathValue, input);
}

async function main() {
  const global = parseGlobalArgs(process.argv.slice(2));
  const client = new VectraPanelClient(global);

  if (global.help || global.positionals.length === 0) {
    client.usage();
    return 0;
  }

  const [command, ...rest] = global.positionals;

  if (command === "login") {
    const session = await client.login({ force: true });
    printJson({
      ok: true,
      baseUrl: session.baseUrl,
      sessionFile: client.sessionFile,
      updatedAt: session.updatedAt,
      authSource: session.authSource,
    });
    return 0;
  }

  if (command === "logout") {
    client.clearSession();
    printJson({
      ok: true,
      sessionFile: client.sessionFile,
      cleared: true,
    });
    return 0;
  }

  if (command === "status") {
    printJson(await client.status());
    return 0;
  }

  let result;
  switch (command) {
    case "catalog":
      result = await runCatalog(client);
      break;
    case "fleet":
      result = await runFleet(client, rest, global.json);
      break;
    case "router":
      result = await runRouter(client, rest);
      break;
    case "draft":
      result = await runDraft(client, rest, global.json);
      break;
    case "logs":
      result = await runLogs(client, rest);
      break;
    case "notifications":
      result = await runNotifications(client, rest);
      break;
    case "terminal":
      result = await runTerminal(client, rest);
      break;
    case "update":
      result = await runUpdate(client, rest);
      break;
    case "rescue":
      result = await runRescue(client, rest);
      break;
    case "router-api":
      result = await runRouterApi(client, rest);
      break;
    case "call":
      result = await runCall(client, rest);
      break;
    default:
      throw new CliError(`Unknown command: ${command}`);
  }

  printJson(result);
  return 0;
}

main().catch((error) => {
  if (error instanceof CliError) {
    process.stderr.write(`Error: ${error.message}\n`);
    process.exitCode = 2;
    return;
  }

  process.stderr.write(`${error instanceof Error ? error.stack ?? error.message : String(error)}\n`);
  process.exitCode = 1;
});
