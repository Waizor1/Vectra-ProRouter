import { readFileSync } from "node:fs";

import { describe, expect, it } from "vitest";

import { routerCheckInRequestSchema } from "@vectra/contracts";

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
