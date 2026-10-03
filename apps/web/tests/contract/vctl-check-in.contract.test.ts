import { readFileSync } from "node:fs";

import { describe, expect, it } from "vitest";

import {
  ROUTER_RAW_SNAPSHOT_MAX_CHARS,
  ROUTER_VERSION_MAP_MAX_CHARS,
  routerCheckInRequestSchema,
} from "@vectra/contracts";

/**
 * The panel half of the vctl check-in contract guard.
 *
 * The fixture is written by the ROUTER, from the bytes vctl actually puts on the
 * wire — see TestCheckInPayloadMatchesPanelContractFixture in
 * router/vectra-controller-pro/cmd/vctl/identity_contract_test.go, which pins
 * the payload's shape to it and regenerates it with
 *
 *     VCTL_UPDATE_CONTRACT_FIXTURE=1 go test ./cmd/vctl -run ContractFixture
 *
 * This file is what makes that pin mean anything: it feeds the same bytes to the
 * panel's REAL routerCheckInRequestSchema. Neither half is sufficient alone —
 * the Go side would happily pin a payload the panel rejects (it did: the
 * check-in shipped deviceIdentifier:"" and devicePublicKey:"" and every loop
 * iteration came back HTTP 400), and this side alone would never notice vctl
 * drifting away from the fixture.
 *
 * Value-level facts that depend on the box (board facts, free space, hostname,
 * timestamps) are normalized to router 1111111111's real values on the Go side,
 * so this test asserts the contract and not the dev host it was generated on.
 */
const checkInFixture: unknown = JSON.parse(
  readFileSync(
    new URL(
      "../../../../router/vectra-controller-pro/testdata/contract/check-in-request.json",
      import.meta.url,
    ),
    "utf8",
  ),
);

/** Every key present in `value`, as dotted paths, arrays flattened by index. */
function keyPaths(value: unknown, prefix = ""): string[] {
  if (value === null || typeof value !== "object") return [];
  if (Array.isArray(value)) {
    return value.flatMap((item, i) => keyPaths(item, `${prefix}[${i}]`));
  }
  return Object.entries(value as Record<string, unknown>).flatMap(([k, v]) => {
    const path = prefix ? `${prefix}.${k}` : k;
    return [path, ...keyPaths(v, path)];
  });
}

describe("vctl check-in payload against the panel contract", () => {
  it("is accepted by routerCheckInRequestSchema", () => {
    const result = routerCheckInRequestSchema.safeParse(checkInFixture);
    expect(
      result.success ? null : result.error.flatten().fieldErrors,
    ).toBeNull();
  });

  // zod objects are non-strict here, so a field the panel does not declare is
  // silently STRIPPED rather than rejected. That is the quiet failure this
  // catches: vctl reports something, the router log says the check-in
  // succeeded, and the value never reaches the database.
  it("has no field the panel silently drops", () => {
    const parsed = routerCheckInRequestSchema.parse(checkInFixture);
    const kept = new Set(keyPaths(parsed));
    const dropped = keyPaths(checkInFixture).filter((p) => !kept.has(p));
    expect(dropped).toEqual([]);
  });

  // The teeth. If the schema accepted these too, the assertion above would be
  // vacuous — and these five are exactly the fields that produced the live 400.
  it.each([
    "deviceIdentifier",
    "devicePublicKey",
    "controllerVersion",
    "model",
    "boardName",
  ])("rejects the payload when inventory.%s is empty", (field) => {
    const payload = structuredClone(checkInFixture) as {
      inventory: Record<string, unknown>;
    };
    payload.inventory[field] = "";
    const result = routerCheckInRequestSchema.safeParse(payload);
    expect(result.success).toBe(false);
  });
});

// Register is reachable anonymously, so every free-form field a router sends
// is size-capped. The caps must reject a flood without touching a real router.
describe("router check-in size caps", () => {
  function withInventory(patch: Record<string, unknown>) {
    const payload = structuredClone(checkInFixture) as {
      inventory: Record<string, unknown>;
    };
    Object.assign(payload.inventory, patch);
    return payload;
  }

  it("rejects an inventory rawSnapshot over the cap", () => {
    const result = routerCheckInRequestSchema.safeParse(
      withInventory({
        rawSnapshot: { blob: "x".repeat(ROUTER_RAW_SNAPSHOT_MAX_CHARS) },
      }),
    );
    expect(result.success).toBe(false);
  });

  it("rejects a package version map over the cap", () => {
    const packageVersions = Object.fromEntries(
      Array.from({ length: 2000 }, (_, i) => [`pkg-${i}`, "1.0.0-r1"]),
    );
    expect(JSON.stringify(packageVersions).length).toBeGreaterThan(
      ROUTER_VERSION_MAP_MAX_CHARS,
    );
    const result = routerCheckInRequestSchema.safeParse(
      withInventory({ packageVersions }),
    );
    expect(result.success).toBe(false);
  });

  it("still accepts a realistic version map and raw snapshot", () => {
    const result = routerCheckInRequestSchema.safeParse(
      withInventory({
        packageVersions: Object.fromEntries(
          Array.from({ length: 40 }, (_, i) => [`pkg-${i}`, "26.8.10-r1"]),
        ),
        rawSnapshot: { note: "y".repeat(8 * 1024) },
      }),
    );
    expect(result.success).toBe(true);
  });
});
