#!/usr/bin/env node

import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

import postgres from "postgres";

import { runSanitization } from "./sanitize-historical-passwall-snapshots.mjs";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const workspaceRoot = path.resolve(__dirname, "..", "..", "..");
const migrationsDir = path.join(workspaceRoot, "packages", "db", "drizzle");
const maskedSecret = "<stored-secret>";

const ids = {
  router: "11111111-1111-4111-8111-111111111111",
  desired: "22222222-2222-4222-8222-222222222222",
  applied: "33333333-3333-4333-8333-333333333333",
  credential: "44444444-4444-4444-8444-444444444444",
  inventory: "55555555-5555-4555-8555-555555555555",
  artifactController: "66666666-6666-4666-8666-666666666666",
  artifactFirmware: "77777777-7777-4777-8777-777777777777",
  manifest: "88888888-8888-4888-8888-888888888888",
  job: "99999999-9999-4999-8999-999999999999",
  jobResult: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
  incident: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
  event: "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
};

function printUsage() {
  console.log(`Usage:
  node apps/web/scripts/verify-db-upgrade-path.mjs [options]

Options:
  --reset-schema       Drop and recreate public schema before each scenario
  --help               Show this help

Environment:
  DATABASE_URL         PostgreSQL test database URL

Safety:
  --reset-schema is accepted only for localhost/127.0.0.1 URLs unless
  VECTRA_DB_UPGRADE_TEST_ALLOW_RESET=1 is set.`);
}

function parseArgs(argv) {
  const options = {
    resetSchema: false,
  };

  for (const arg of argv) {
    switch (arg) {
      case "--reset-schema":
        options.resetSchema = true;
        break;
      case "--help":
      case "-h":
        printUsage();
        process.exit(0);
      default:
        throw new Error(`Unknown argument: ${arg}`);
    }
  }

  return options;
}

function assertSafeResetUrl(databaseUrl) {
  const parsed = new URL(databaseUrl);
  const safeHost =
    parsed.hostname === "127.0.0.1" || parsed.hostname === "localhost";
  if (!safeHost && process.env.VECTRA_DB_UPGRADE_TEST_ALLOW_RESET !== "1") {
    throw new Error(
      "Refusing schema reset for a non-local DATABASE_URL. Use a disposable DB or set VECTRA_DB_UPGRADE_TEST_ALLOW_RESET=1.",
    );
  }
}

function splitMigrationStatements(raw) {
  return raw
    .split("--> statement-breakpoint")
    .map((statement) => statement.trim())
    .filter(Boolean);
}

async function applyMigrationFile(sql, fileName) {
  const filePath = path.join(migrationsDir, fileName);
  const raw = await readFile(filePath, "utf8");
  for (const statement of splitMigrationStatements(raw)) {
    await sql.unsafe(statement);
  }
}

async function resetSchema(sql) {
  await sql`drop schema if exists public cascade`;
  await sql`create schema public`;
  await sql`grant all on schema public to public`;
}

async function seedProductionLikeState(sql) {
  await sql`
    insert into vectra_router (
      id,
      device_identifier,
      display_name,
      hostname,
      panel_domain,
      model,
      board_name,
      target,
      architecture,
      openwrt_release,
      status,
      controller_channel,
      approved_at,
      last_seen_at,
      last_check_in_at
    ) values (
      ${ids.router},
      'fixture-device-ax3000t',
      'AX3000T fixture',
      'ax3000t-fixture',
      'https://router.vectra-pro.net',
      'Xiaomi AX3000T',
      'xiaomi,mi-router-ax3000t',
      'mediatek/filogic',
      'aarch64_cortex-a53',
      '24.10.6',
      'active',
      'stable',
      now(),
      now(),
      now()
    )
  `;

  await sql`
    insert into vectra_router_credential (
      id,
      router_id,
      type,
      token_hash,
      token_preview,
      device_public_key
    ) values (
      ${ids.credential},
      ${ids.router},
      'agent_token',
      'fixture-token-hash',
      'fixture-token-preview',
      'fixture-device-public-key'
    )
  `;

  const oldRawSnapshot = {
    node: {
      remarks: "Primary node",
      address: "example.test",
      port: 443,
      username: "fixture-user",
      password: "fixture-password",
      uuid: "fixture-uuid",
    },
    subscription: {
      url: "https://example.test/subscription-token",
      addMode: "2",
    },
  };
  const oldAppliedSnapshot = {
    lines: [
      "passwall2.node_1.remarks='Primary node'",
      "passwall2.node_1.password='fixture-password'",
    ],
  };
  const config = {
    schemaVersion: 1,
    basicSettings: {
      main: {
        mainSwitch: true,
        selectedNodeId: "node_1",
        localhostProxy: true,
        clientProxy: true,
        nodeSocksPort: 1070,
        nodeSocksBindLocal: true,
        socksMainSwitch: false,
        extras: {},
      },
      dns: {
        directQueryStrategy: "UseIP",
        remoteDnsProtocol: "tcp",
        remoteDns: "1.1.1.1",
        remoteDnsDoh: "https://1.1.1.1/dns-query",
        remoteDnsDetour: "remote",
        remoteFakeDns: false,
        remoteDnsQueryStrategy: "UseIPv4",
        dnsHosts: [],
        dnsRedirect: true,
        extras: {},
      },
      log: {
        enableNodeLog: true,
        level: "warning",
        extras: {},
      },
      maintenance: {
        backupPaths: ["/etc/config/passwall2"],
        extras: {},
      },
      socks: [],
      shuntRules: [],
    },
    nodes: [
      {
        id: "node_1",
        label: "Primary node",
        protocol: "xray",
        enabled: true,
        group: "default",
        address: "example.test",
        port: 443,
        tags: [],
        extras: {},
      },
    ],
    subscriptions: {
      filterKeywordMode: "0",
      discardList: [],
      keepList: [],
      typePreferences: {},
      domainStrategy: "auto",
      items: [],
    },
    appUpdate: {
      binaryPaths: {
        xray: "/usr/bin/xray",
        singBox: "/usr/bin/sing-box",
        hysteria: "/usr/bin/hysteria",
        geoview: "/usr/bin/geoview",
      },
      updateStrategy: "package-preferred",
      targetVersions: {},
      extras: {},
    },
    ruleManage: {
      geoipUrl:
        "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat",
      geositeUrl:
        "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat",
      assetDirectory: "/usr/share/v2ray/",
      autoUpdate: false,
      scheduleMode: "daily",
      enabledAssets: ["geoip", "geosite"],
      shuntRules: [],
      extras: {},
    },
  };

  await sql`
    insert into vectra_passwall_desired_revision (
      id,
      router_id,
      revision_number,
      status,
      config,
      raw_imported_snapshot,
      created_by,
      note
    ) values (
      ${ids.desired},
      ${ids.router},
      1,
      'approved',
      ${JSON.stringify(config)}::jsonb,
      ${JSON.stringify(oldRawSnapshot)}::jsonb,
      'operator',
      'production-like fixture'
    )
  `;

  await sql`
    insert into vectra_passwall_applied_revision (
      id,
      router_id,
      desired_revision_id,
      result,
      config,
      raw_snapshot
    ) values (
      ${ids.applied},
      ${ids.router},
      ${ids.desired},
      'applied',
      ${JSON.stringify(config)}::jsonb,
      ${JSON.stringify(oldAppliedSnapshot)}::jsonb
    )
  `;

  await sql`
    insert into vectra_router_inventory_snapshot (
      id,
      router_id,
      source,
      payload,
      passwall_enabled,
      selected_node_id,
      node_count,
      subscription_count,
      controller_version,
      passwall_app_version
    ) values (
      ${ids.inventory},
      ${ids.router},
      'check_in',
      ${JSON.stringify({
        protocolVersion: "2026-04-v1",
        deviceIdentifier: "fixture-device-ax3000t",
        devicePublicKey: "fixture-device-public-key",
        controllerVersion: "0.1.11-r1",
        hostname: "ax3000t-fixture",
        panelDomain: "https://router.vectra-pro.net",
        model: "Xiaomi AX3000T",
        boardName: "xiaomi,mi-router-ax3000t",
        layoutFamily: "stock-layout",
        target: "mediatek/filogic",
        architecture: "aarch64_cortex-a53",
        openwrtRelease: "24.10.6",
        passwallEnabled: true,
        selectedNodeId: "node_1",
        selectedNodeLabel: "Primary node",
        nodeCount: 1,
        subscriptionCount: 0,
        packageVersions: {
          "luci-app-passwall2": "26.4.5-r1",
          "vectra-controller-agent": "0.1.11-r1",
        },
        binaryVersions: {
          xray: "26.3.27-r1",
          geoview: "0.2.5-r1",
        },
        resources: {
          memoryTotalMb: 512,
          memoryAvailableMb: 256,
          swapTotalMb: 0,
          swapFreeMb: 0,
          overlayFreeMb: 32,
          tmpFreeMb: 128,
        },
        serviceHealth: {
          controller: "running",
          passwall: "running",
          passwallServer: "running",
          dnsmasq: "running",
        },
      })}::jsonb,
      true,
      'node_1',
      1,
      0,
      '0.1.11-r1',
      '26.4.5-r1'
    )
  `;

  await sql`
    insert into vectra_artifact (
      id,
      type,
      channel,
      name,
      version,
      architecture,
      download_url,
      checksum_sha256,
      metadata
    ) values (
      ${ids.artifactController},
      'controller',
      'stable',
      'vectra-controller-agent',
      '0.1.11-r1',
      'aarch64_cortex-a53',
      'https://api.vectra-pro.net/artifacts/openwrt/stable/aarch64_cortex-a53/vectra-controller-agent_0.1.11-r1_aarch64_cortex-a53.ipk',
      'fixture-controller-sha256',
      '{"source":"production-like-fixture"}'::jsonb
    )
  `;

  await sql`
    insert into vectra_artifact (
      id,
      type,
      channel,
      name,
      version,
      architecture,
      board_name,
      layout_family,
      download_url,
      checksum_sha256,
      metadata
    ) values (
      ${ids.artifactFirmware},
      'firmware',
      'stable',
      'openwrt-ax3000t-stock',
      '24.10.6',
      'aarch64_cortex-a53',
      'xiaomi,mi-router-ax3000t',
      'stock-layout',
      'https://api.vectra-pro.net/artifacts/firmware/stable/ax3000t-stock/openwrt-24.10.6-stock-layout.bin',
      'fixture-firmware-sha256',
      '{"source":"production-like-fixture"}'::jsonb
    )
  `;

  await sql`
    insert into vectra_firmware_manifest (
      id,
      board_name,
      target,
      architecture,
      layout_family,
      channel,
      version,
      artifact_id,
      rollout_policy
    ) values (
      ${ids.manifest},
      'xiaomi,mi-router-ax3000t',
      'mediatek/filogic',
      'aarch64_cortex-a53',
      'stock-layout',
      'stable',
      '24.10.6',
      ${ids.artifactFirmware},
      '{"guarded":true}'::jsonb
    )
  `;

  await sql`
    insert into vectra_job (
      id,
      router_id,
      type,
      state,
      payload,
      desired_revision_id,
      dedupe_key
    ) values (
      ${ids.job},
      ${ids.router},
      'update_passwall_packages',
      'succeeded',
      '{"channel":"stable","packageList":["luci-app-passwall2","xray-core","sing-box","hysteria","geoview","v2ray-geoip","v2ray-geosite","dnsmasq-full","chinadns-ng","kmod-nft-socket","kmod-nft-tproxy","kmod-nft-nat"]}'::jsonb,
      ${ids.desired},
      'fixture-update-passwall-packages'
    )
  `;

  await sql`
    insert into vectra_job_result (
      id,
      job_id,
      router_id,
      status,
      payload
    ) values (
      ${ids.jobResult},
      ${ids.job},
      ${ids.router},
      'success',
      '{"installed":["luci-app-passwall2","xray-core","geoview"]}'::jsonb
    )
  `;

  await sql`
    insert into vectra_health_incident (
      id,
      router_id,
      type,
      state,
      reason,
      metadata
    ) values (
      ${ids.incident},
      ${ids.router},
      'recovered',
      'resolved',
      'fixture recovery',
      '{"source":"production-like-fixture"}'::jsonb
    )
  `;

  await sql`
    insert into vectra_event_log (
      id,
      router_id,
      type,
      severity,
      message,
      metadata
    ) values (
      ${ids.event},
      ${ids.router},
      'fixture.seeded',
      'info',
      'Production-like fixture seeded.',
      '{"source":"production-like-fixture"}'::jsonb
    )
  `;
}

async function assertTableCounts(sql, expected) {
  for (const [table, expectedCount] of Object.entries(expected)) {
    const [{ count }] = await sql.unsafe(
      `select count(*)::int as count from ${table}`,
    );
    if (count !== expectedCount) {
      throw new Error(`${table} count = ${count}, expected ${expectedCount}`);
    }
  }
}

async function assertColumnExists(sql, tableName, columnName) {
  const rows = await sql`
    select 1
    from information_schema.columns
    where table_schema = 'public'
      and table_name = ${tableName}
      and column_name = ${columnName}
    limit 1
  `;
  if (!rows.length) {
    throw new Error(`Missing column ${tableName}.${columnName}`);
  }
}

async function runCleanBootstrapScenario(sql) {
  await resetSchema(sql);
  await applyMigrationFile(sql, "0000_groovy_mentallo.sql");
  await applyMigrationFile(sql, "0001_flimsy_echo.sql");

  await assertColumnExists(sql, "vectra_router", "import_state");
  await assertColumnExists(sql, "vectra_passwall_desired_revision", "origin");
  await assertColumnExists(sql, "vectra_passwall_applied_revision", "stderr");
  await assertTableCounts(sql, {
    vectra_router: 0,
    vectra_passwall_desired_revision: 0,
    vectra_passwall_applied_revision: 0,
    vectra_artifact: 0,
  });

  return {
    scenario: "clean-bootstrap",
    migrations: ["0000_groovy_mentallo", "0001_flimsy_echo"],
    verifiedColumns: [
      "vectra_router.import_state",
      "vectra_passwall_desired_revision.origin",
      "vectra_passwall_applied_revision.stderr",
    ],
  };
}

async function runProductionLikeUpgradeScenario(sql) {
  await resetSchema(sql);
  await applyMigrationFile(sql, "0000_groovy_mentallo.sql");
  await seedProductionLikeState(sql);
  await applyMigrationFile(sql, "0001_flimsy_echo.sql");

  await assertTableCounts(sql, {
    vectra_router: 1,
    vectra_router_credential: 1,
    vectra_router_inventory_snapshot: 1,
    vectra_passwall_desired_revision: 1,
    vectra_passwall_applied_revision: 1,
    vectra_artifact: 2,
    vectra_firmware_manifest: 1,
    vectra_job: 1,
    vectra_job_result: 1,
    vectra_health_incident: 1,
    vectra_event_log: 1,
    vectra_passwall_secret_blob: 0,
  });

  const [router] = await sql`
    select import_state as "importState", board_name as "boardName", target, architecture, openwrt_release as "openwrtRelease"
    from vectra_router
    where id = ${ids.router}
  `;
  if (router.importState !== "awaiting_import") {
    throw new Error(
      `router import_state = ${router.importState}, expected awaiting_import`,
    );
  }
  if (
    router.boardName !== "xiaomi,mi-router-ax3000t" ||
    router.target !== "mediatek/filogic" ||
    router.architecture !== "aarch64_cortex-a53" ||
    router.openwrtRelease !== "24.10.6"
  ) {
    throw new Error("Router certified tuple was not preserved across upgrade.");
  }

  const dryRunSummary = await runSanitization(sql, {
    apply: false,
    limit: 500,
    routerId: null,
  });
  if (dryRunSummary.totalChanged !== 2) {
    throw new Error(
      `dry-run sanitation changed ${dryRunSummary.totalChanged}, expected 2`,
    );
  }

  const applySummary = await runSanitization(sql, {
    apply: true,
    limit: 500,
    routerId: null,
  });
  if (applySummary.totalChanged !== 2) {
    throw new Error(
      `apply sanitation changed ${applySummary.totalChanged}, expected 2`,
    );
  }

  const secondDryRunSummary = await runSanitization(sql, {
    apply: false,
    limit: 500,
    routerId: null,
  });
  if (secondDryRunSummary.totalChanged !== 0) {
    throw new Error(
      `second dry-run sanitation changed ${secondDryRunSummary.totalChanged}, expected 0`,
    );
  }

  const [desired] = await sql`
    select raw_imported_snapshot as "rawImportedSnapshot", origin, status
    from vectra_passwall_desired_revision
    where id = ${ids.desired}
  `;
  const [applied] = await sql`
    select raw_snapshot as "rawSnapshot", job_id as "jobId", stderr
    from vectra_passwall_applied_revision
    where id = ${ids.applied}
  `;
  const [{ count: maintenanceEventCount }] = await sql`
    select count(*)::int as count
    from vectra_event_log
    where type = 'maintenance.snapshot_sanitized'
  `;

  if (desired.origin !== "operator_draft" || desired.status !== "approved") {
    throw new Error(
      "Desired revision status/default columns were not preserved.",
    );
  }
  if (applied.jobId !== null || applied.stderr !== null) {
    throw new Error(
      "Applied revision nullable post-migration columns should default to null.",
    );
  }
  if (desired.rawImportedSnapshot.node.password !== maskedSecret) {
    throw new Error("Desired revision password was not masked.");
  }
  if (desired.rawImportedSnapshot.node.username !== maskedSecret) {
    throw new Error("Desired revision username was not masked.");
  }
  if (desired.rawImportedSnapshot.subscription.url !== maskedSecret) {
    throw new Error("Desired revision subscription URL was not masked.");
  }
  if (
    applied.rawSnapshot.lines[1] !==
    `passwall2.node_1.password='${maskedSecret}'`
  ) {
    throw new Error("Applied revision UCI assignment was not masked.");
  }
  if (maintenanceEventCount !== 2) {
    throw new Error(
      `maintenance event count = ${maintenanceEventCount}, expected 2`,
    );
  }

  return {
    scenario: "production-like-upgrade",
    migrations: ["0000_groovy_mentallo", "0001_flimsy_echo"],
    preserved: [
      "routers",
      "credentials",
      "inventory_snapshots",
      "desired_revisions",
      "applied_revisions",
      "jobs",
      "job_results",
      "artifacts",
      "firmware_manifests",
      "health_incidents",
      "event_log",
    ],
    sanitation: {
      dryRunChanged: dryRunSummary.totalChanged,
      applyChanged: applySummary.totalChanged,
      secondDryRunChanged: secondDryRunSummary.totalChanged,
      auditEvents: maintenanceEventCount,
    },
  };
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  if (!process.env.DATABASE_URL) {
    throw new Error("DATABASE_URL is required.");
  }
  if (!options.resetSchema) {
    throw new Error(
      "--reset-schema is required for this destructive test harness.",
    );
  }
  assertSafeResetUrl(process.env.DATABASE_URL);

  const sql = postgres(process.env.DATABASE_URL, { max: 1 });
  try {
    const cleanBootstrap = await runCleanBootstrapScenario(sql);
    const productionLikeUpgrade = await runProductionLikeUpgradeScenario(sql);
    console.log(
      JSON.stringify(
        {
          ok: true,
          database: "local disposable PostgreSQL",
          scenarios: [cleanBootstrap, productionLikeUpgrade],
        },
        null,
        2,
      ),
    );
  } finally {
    await sql.end({ timeout: 5 });
  }
}

main().catch((error) => {
  console.error(
    `[verify-db-upgrade-path] ${
      error instanceof Error ? error.message : String(error)
    }`,
  );
  process.exit(1);
});
