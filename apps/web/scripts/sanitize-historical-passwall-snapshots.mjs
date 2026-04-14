#!/usr/bin/env node
// @ts-nocheck

import { randomUUID } from "node:crypto";
import path from "node:path";
import { fileURLToPath } from "node:url";

import postgres from "postgres";

const MASKED_SECRET_PLACEHOLDER = "<stored-secret>";

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

function printUsage() {
  console.log(`Usage:
  node apps/web/scripts/sanitize-historical-passwall-snapshots.mjs [options]

Options:
  --apply                 Persist updates and audit events
  --dry-run               Preview only (default)
  --limit N               Max rows per table (default: 500)
  --router-id UUID        Restrict to one router
  --help                  Show this help

Notes:
  - Reads DATABASE_URL from the environment
  - Never prints raw snapshot payloads
  - Sanitizes desired revisions and applied revisions independently`);
}

export function parseArgs(argv) {
  const options = {
    apply: false,
    limit: 500,
    routerId: null,
  };

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    switch (arg) {
      case "--apply":
        options.apply = true;
        break;
      case "--dry-run":
        options.apply = false;
        break;
      case "--limit":
        options.limit = Number(argv[++index] ?? "0");
        break;
      case "--router-id":
        options.routerId = argv[++index] ?? null;
        break;
      case "--help":
      case "-h":
        printUsage();
        process.exit(0);
      default:
        throw new Error(`Unknown argument: ${arg}`);
    }
  }

  if (!Number.isInteger(options.limit) || options.limit <= 0) {
    throw new Error("--limit must be a positive integer.");
  }

  return options;
}

function sortValue(value) {
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

  if (value && typeof value === "object") {
    return Object.fromEntries(
      Object.entries(value)
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([key, entry]) => [key, sortValue(entry)])
    );
  }

  return value;
}

function stableStringify(value) {
  return JSON.stringify(sortValue(value));
}

function maskStringPreservingQuotes(raw) {
  const trimmed = raw.trim();
  if (
    (trimmed.startsWith("'") && trimmed.endsWith("'")) ||
    (trimmed.startsWith('"') && trimmed.endsWith('"'))
  ) {
    return `${trimmed[0]}${MASKED_SECRET_PLACEHOLDER}${trimmed[0]}`;
  }

  return MASKED_SECRET_PLACEHOLDER;
}

export function sanitizeUCIAssignment(raw) {
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

function sanitizeUnknownSecrets(value, keyHint) {
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

  if (value && typeof value === "object") {
    return Object.fromEntries(
      Object.entries(value).map(([key, entry]) => [
        key,
        sanitizeUnknownSecrets(entry, key),
      ])
    );
  }

  return value;
}

export function sanitizePasswallRawSnapshot(snapshot) {
  return sanitizeUnknownSecrets(snapshot);
}

async function selectRows(sql, tableName, columnName, options) {
  const routerFilter = options.routerId
    ? sql`and router_id = ${options.routerId}`
    : sql``;

  return sql`
    select id, router_id as "routerId", ${sql(columnName)} as payload
    from ${sql(tableName)}
    where ${sql(columnName)} is not null
    ${routerFilter}
    order by id
    limit ${options.limit}
  `;
}

async function updateRow(sql, tableName, columnName, rowId, sanitizedPayload) {
  await sql`
    update ${sql(tableName)}
    set ${sql(columnName)} = ${JSON.stringify(sanitizedPayload)}::jsonb
    where id = ${rowId}
  `;
}

async function insertAuditEvent(sql, row, tableName) {
  await sql`
    insert into vectra_event_log (
      id,
      router_id,
      type,
      severity,
      message,
      metadata
    ) values (
      ${randomUUID()},
      ${row.routerId},
      'maintenance.snapshot_sanitized',
      'info',
      ${`Historical ${tableName} snapshot sanitized without exposing raw payloads.`},
      ${JSON.stringify({
        table: tableName,
        recordId: row.id,
        mode: "historical_snapshot_sanitization",
      })}::jsonb
    )
  `;
}

async function processTable(sql, options, tableName, columnName) {
  const rows = await selectRows(sql, tableName, columnName, options);
  const changedRows = [];

  for (const row of rows) {
    const sanitizedPayload = sanitizePasswallRawSnapshot(row.payload);
    if (stableStringify(row.payload) !== stableStringify(sanitizedPayload)) {
      changedRows.push({
        id: row.id,
        routerId: row.routerId,
        table: tableName,
        column: columnName,
        sanitizedPayload,
      });
    }
  }

  if (!options.apply || changedRows.length === 0) {
    return {
      table: tableName,
      scanned: rows.length,
      changed: changedRows.length,
      updatedIds: changedRows.map((row) => row.id),
    };
  }

  for (const row of changedRows) {
    await updateRow(sql, tableName, columnName, row.id, row.sanitizedPayload);
    await insertAuditEvent(sql, row, tableName);
  }

  return {
    table: tableName,
    scanned: rows.length,
    changed: changedRows.length,
    updatedIds: changedRows.map((row) => row.id),
  };
}

export async function runSanitization(sql, options) {
  const executor = options.apply
    ? (work) => sql.begin(work)
    : async (work) => work(sql);

  return executor(async (tx) => {
    const desired = await processTable(
      tx,
      options,
      "vectra_passwall_desired_revision",
      "raw_imported_snapshot"
    );
    const applied = await processTable(
      tx,
      options,
      "vectra_passwall_applied_revision",
      "raw_snapshot"
    );

    return {
      mode: options.apply ? "apply" : "dry-run",
      routerId: options.routerId,
      limit: options.limit,
      tables: [desired, applied],
      totalChanged: desired.changed + applied.changed,
    };
  });
}

export async function main() {
  const options = parseArgs(process.argv.slice(2));
  if (!process.env.DATABASE_URL) {
    throw new Error("DATABASE_URL is required.");
  }

  const sql = postgres(process.env.DATABASE_URL, { max: 1 });
  try {
    const summary = await runSanitization(sql, options);

    console.log(JSON.stringify(summary, null, 2));
  } finally {
    await sql.end({ timeout: 5 });
  }
}

const invokedDirectly =
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url);

if (invokedDirectly) {
  main().catch((error) => {
    console.error(
      `[sanitize-historical-passwall-snapshots] ${error instanceof Error ? error.message : String(error)}`
    );
    process.exit(1);
  });
}
