import {
  ROUTER_INCIDENT_TRANSITIONS_MAX,
  ROUTER_JOB_RESULT_MAX_CHARS,
  ROUTER_PASSWALL_IMPORT_PART_MAX_CHARS,
  ROUTER_RAW_SNAPSHOT_MAX_CHARS,
  ROUTER_VERSION_MAP_MAX_CHARS,
} from "@vectra/contracts";
import { describe, expect, it } from "vitest";

import {
  boundJobResultPayload,
  boundRouterCheckInPayload,
} from "./router-payload-bounds";

describe("boundRouterCheckInPayload", () => {
  it("leaves a payload within the caps untouched", () => {
    const input = {
      routerId: "r",
      inventory: { rawSnapshot: { a: 1 }, packageVersions: { xray: "26.1" } },
      passwallImport: { config: { a: 1 }, rawSnapshot: { b: 2 } },
    };

    expect(boundRouterCheckInPayload(input)).toEqual({
      payload: input,
      truncated: [],
      passwallImportDropped: false,
    });
  });

  it("replaces an oversized inventory rawSnapshot with a marker", () => {
    const { payload, truncated } = boundRouterCheckInPayload({
      inventory: {
        rawSnapshot: { blob: "x".repeat(ROUTER_RAW_SNAPSHOT_MAX_CHARS) },
      },
    });

    expect(truncated).toEqual(["inventory.rawSnapshot"]);
    expect(payload).toMatchObject({
      inventory: { rawSnapshot: { truncated: true } },
    });
  });

  it("keeps the leading entries of an oversized version map", () => {
    const packageVersions = Object.fromEntries(
      Array.from({ length: 2000 }, (_, i) => [`pkg-${i}`, "1.0.0-r1"]),
    );
    const { payload, truncated } = boundRouterCheckInPayload({
      inventory: { packageVersions },
    });
    const kept = (payload as { inventory: { packageVersions: object } })
      .inventory.packageVersions;

    expect(truncated).toEqual(["inventory.packageVersions"]);
    expect(JSON.stringify(kept).length).toBeLessThanOrEqual(
      ROUTER_VERSION_MAP_MAX_CHARS,
    );
    expect(kept).toMatchObject({ "pkg-0": "1.0.0-r1" });
  });

  it("drops an import whose config is over the cap", () => {
    const { payload, truncated, passwallImportDropped } =
      boundRouterCheckInPayload({
        inventory: {},
        passwallImport: {
          config: { blob: "x".repeat(ROUTER_PASSWALL_IMPORT_PART_MAX_CHARS) },
        },
      });

    expect(passwallImportDropped).toBe(true);
    expect(truncated).toEqual(["passwallImport"]);
    expect(payload).not.toHaveProperty("passwallImport");
  });

  it("keeps an import whose only oversized part is the raw snapshot", () => {
    const { payload, passwallImportDropped } = boundRouterCheckInPayload({
      passwallImport: {
        config: { a: 1 },
        rawSnapshot: {
          blob: "x".repeat(ROUTER_PASSWALL_IMPORT_PART_MAX_CHARS),
        },
      },
    });

    expect(passwallImportDropped).toBe(false);
    expect(payload).toMatchObject({
      passwallImport: { config: { a: 1 }, rawSnapshot: { truncated: true } },
    });
  });
});

describe("boundJobResultPayload", () => {
  it("replaces an oversized result with a marker of its size", () => {
    const { payload, truncated } = boundJobResultPayload({
      result: { stdout: "z".repeat(ROUTER_JOB_RESULT_MAX_CHARS) },
    });

    expect(truncated).toEqual(["result"]);
    expect(payload).toMatchObject({
      result: { truncated: true, bytes: ROUTER_JOB_RESULT_MAX_CHARS + 13 },
    });
  });

  it("never cuts output inside a surrogate pair", () => {
    // 15999 ASCII characters then an emoji: the cap falls between its halves.
    const output = `${"a".repeat(15_999)}\u{1F600}tail`;
    const { payload } = boundJobResultPayload({ stdout: output });
    const kept = (payload as { stdout: string }).stdout;

    expect(kept).toHaveLength(15_999);
    expect(kept.endsWith("a")).toBe(true);
  });

  it("keeps the newest incident transitions and cuts long output", () => {
    const { payload, truncated } = boundJobResultPayload({
      incidentTransitions: Array.from(
        { length: ROUTER_INCIDENT_TRANSITIONS_MAX + 5 },
        (_, i) => ({ i }),
      ),
      stdout: "o".repeat(20_000),
    });

    expect(truncated).toEqual(["incidentTransitions", "stdout"]);
    const kept = (payload as { incidentTransitions: { i: number }[] })
      .incidentTransitions;
    expect(kept).toHaveLength(ROUTER_INCIDENT_TRANSITIONS_MAX);
    expect(kept[0]).toEqual({ i: 5 });
    expect(kept.at(-1)).toEqual({ i: ROUTER_INCIDENT_TRANSITIONS_MAX + 4 });
    expect((payload as { stdout: string }).stdout).toHaveLength(16_000);
  });
});
