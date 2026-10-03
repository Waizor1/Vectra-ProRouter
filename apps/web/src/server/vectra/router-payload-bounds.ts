import {
  ROUTER_INCIDENT_TRANSITIONS_MAX,
  ROUTER_JOB_RESULT_MAX_CHARS,
  ROUTER_PASSWALL_IMPORT_PART_MAX_CHARS,
  ROUTER_RAW_SNAPSHOT_MAX_CHARS,
  ROUTER_VERSION_MAP_MAX_CHARS,
} from "@vectra/contracts";

/**
 * Brings an AUTHENTICATED router's check-in or job result inside the contract's
 * size caps instead of letting the schema refuse it.
 *
 * The PassWall agent retries an unsent job result before every check-in and
 * re-sends its import until one is accepted, clearing either only on a 2xx. A
 * permanent 400 for one oversized field would therefore cut the router off
 * the panel for good. So an oversized field is truncated or dropped (and
 * reported in `truncated`), and the request goes through. Anonymous register
 * keeps the strict caps.
 */

type JsonObject = Record<string, unknown>;

// zod's own string cap on a job result's stdout/stderr.
const JOB_OUTPUT_MAX_CHARS = 16_000;

function isObject(value: unknown): value is JsonObject {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function serializedLength(value: unknown) {
  try {
    return (JSON.stringify(value) as string | undefined)?.length ?? 0;
  } catch {
    return Number.POSITIVE_INFINITY;
  }
}

function serializedBytes(value: unknown) {
  try {
    return Buffer.byteLength(
      (JSON.stringify(value) as string | undefined) ?? "",
    );
  } catch {
    return 0;
  }
}

function placeholder(value: unknown) {
  return { truncated: true, bytes: serializedBytes(value) };
}

/** Keeps entries, in order, while the map still fits. */
function truncateVersionMap(map: JsonObject) {
  const kept: JsonObject = {};
  let length = 2;
  for (const [key, value] of Object.entries(map)) {
    const entryLength = serializedLength({ [key]: value }) - 1;
    if (length + entryLength > ROUTER_VERSION_MAP_MAX_CHARS) {
      break;
    }
    kept[key] = value;
    length += entryLength;
  }
  return kept;
}

export function boundRouterCheckInPayload(input: unknown) {
  const truncated: string[] = [];
  let passwallImportDropped = false;
  if (!isObject(input)) {
    return { payload: input, truncated, passwallImportDropped };
  }

  const payload: JsonObject = { ...input };
  if (isObject(payload.inventory)) {
    const inventory: JsonObject = { ...payload.inventory };
    if (
      isObject(inventory.rawSnapshot) &&
      serializedLength(inventory.rawSnapshot) > ROUTER_RAW_SNAPSHOT_MAX_CHARS
    ) {
      inventory.rawSnapshot = placeholder(inventory.rawSnapshot);
      truncated.push("inventory.rawSnapshot");
    }
    for (const field of ["packageVersions", "binaryVersions"] as const) {
      const map = inventory[field];
      if (
        isObject(map) &&
        serializedLength(map) > ROUTER_VERSION_MAP_MAX_CHARS
      ) {
        inventory[field] = truncateVersionMap(map);
        truncated.push(`inventory.${field}`);
      }
    }
    payload.inventory = inventory;
  }

  if (isObject(payload.passwallImport)) {
    const passwallImport: JsonObject = { ...payload.passwallImport };
    // The config is the import; without all of it there is nothing to import.
    if (
      serializedLength(passwallImport.config) >
      ROUTER_PASSWALL_IMPORT_PART_MAX_CHARS
    ) {
      delete payload.passwallImport;
      passwallImportDropped = true;
      truncated.push("passwallImport");
    } else {
      // The raw snapshot is only kept for reference.
      if (
        isObject(passwallImport.rawSnapshot) &&
        serializedLength(passwallImport.rawSnapshot) >
          ROUTER_PASSWALL_IMPORT_PART_MAX_CHARS
      ) {
        passwallImport.rawSnapshot = placeholder(passwallImport.rawSnapshot);
        truncated.push("passwallImport.rawSnapshot");
      }
      payload.passwallImport = passwallImport;
    }
  }

  return { payload, truncated, passwallImportDropped };
}

export function boundJobResultPayload(input: unknown) {
  const truncated: string[] = [];
  if (!isObject(input)) {
    return { payload: input, truncated };
  }

  const payload: JsonObject = { ...input };
  if (
    isObject(payload.result) &&
    serializedLength(payload.result) > ROUTER_JOB_RESULT_MAX_CHARS
  ) {
    payload.result = placeholder(payload.result);
    truncated.push("result");
  }
  if (
    Array.isArray(payload.incidentTransitions) &&
    payload.incidentTransitions.length > ROUTER_INCIDENT_TRANSITIONS_MAX
  ) {
    payload.incidentTransitions = payload.incidentTransitions.slice(
      0,
      ROUTER_INCIDENT_TRANSITIONS_MAX,
    );
    truncated.push("incidentTransitions");
  }
  for (const field of ["stdout", "stderr"] as const) {
    const output = payload[field];
    if (typeof output === "string" && output.length > JOB_OUTPUT_MAX_CHARS) {
      payload[field] = output.slice(0, JOB_OUTPUT_MAX_CHARS);
      truncated.push(field);
    }
  }

  return { payload, truncated };
}
