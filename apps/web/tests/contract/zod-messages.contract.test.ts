import { describe, expect, it } from "vitest";
import { z } from "zod";

import {
  passwallSubscriptionSchema,
  isoDatetimeSchema,
  routerInventorySchema,
  urlStringSchema,
  zodV3Error,
  zodV3Issues,
} from "@vectra/contracts";

/**
 * The words a rejected payload comes back with are part of the contract: a
 * router's 400 carries error.flatten() (app/api/router/_lib.ts), a partner
 * reads `path: message` (partner-api.ts), and vctl's own docs quote them
 * (router/vectra-controller-pro/internal/controlplane/types.go). The panel
 * moved from zod 3 to zod 4, which words them differently; @vectra/contracts
 * installs an error map that keeps zod 3's wording. These pin it.
 */
function messages(schema: z.ZodType, input: unknown) {
  const result = schema.safeParse(input);
  if (result.success) {
    throw new Error("expected the input to be rejected");
  }
  return result.error.issues.map((issue) => issue.message);
}

describe("zod 3 wording of rejected payloads", () => {
  it.each([
    ["missing value", z.string(), undefined, "Required"],
    ["wrong type", z.string(), 5, "Expected string, received number"],
    ["record of the wrong type", z.record(z.string(), z.string()), [], "Expected object, received array"],
    ["fraction for an int", z.number().int(), 1.5, "Expected integer, received float"],
    ["short string", z.string().min(1), "", "String must contain at least 1 character(s)"],
    ["long string", z.string().max(3), "abcd", "String must contain at most 3 character(s)"],
    ["exact length", z.string().length(3), "ab", "String must contain exactly 3 character(s)"],
    ["empty array", z.array(z.string()).min(1), [], "Array must contain at least 1 element(s)"],
    ["long array", z.array(z.string()).max(1), ["a", "b"], "Array must contain at most 1 element(s)"],
    ["small number", z.number().min(30), 5, "Number must be greater than or equal to 30"],
    ["non-positive", z.number().positive(), 0, "Number must be greater than 0"],
    ["big number", z.number().max(10), 11, "Number must be less than or equal to 10"],
    ["not a multiple", z.number().multipleOf(5), 3, "Number must be a multiple of 5"],
    ["enum value", z.enum(["a", "b"]), "c", "Invalid enum value. Expected 'a' | 'b', received 'c'"],
    ["enum type", z.enum(["a", "b"]), 1, "Expected 'a' | 'b', received number"],
    ["missing enum", z.enum(["a", "b"]), undefined, "Required"],
    ["literal", z.literal("v1"), "v2", 'Invalid literal value, expected "v1"'],
    ["regex", z.string().regex(/^a$/), "b", "Invalid"],
    ["uuid", z.guid(), "nope", "Invalid uuid"],
    ["url", z.string().url(), "nope", "Invalid url"],
    ["datetime", z.string().datetime(), "nope", "Invalid datetime"],
    ["union", z.union([z.string(), z.number()]), true, "Invalid input"],
    ["refine", z.string().refine(() => false), "x", "Invalid input"],
  ])("%s", (_label, schema, input, expected) => {
    expect(messages(schema, input)).toEqual([expected]);
  });

  it("names every unrecognized key the way zod 3 did", () => {
    expect(messages(z.object({}).strict(), { a: 1, b: 2 })).toEqual([
      "Unrecognized key(s) in object: 'a', 'b'",
    ]);
  });

  it("names the discriminator values the way zod 3 did", () => {
    const schema = z.discriminatedUnion("type", [
      z.object({ type: z.literal("a") }),
      z.object({ type: z.literal("b") }),
    ]);
    expect(messages(schema, { type: "c" })).toEqual([
      "Invalid discriminator value. Expected 'a' | 'b'",
    ]);
  });

  it("answers a mistyped field with its type alone, as zod 3 did", () => {
    const schema = z.object({ label: z.string().min(1).max(64) });
    const result = schema.safeParse({ label: [] });
    expect(result.success).toBe(false);
    if (result.success) return;
    // zod 4 alone also reports the array's length under the same path.
    expect(result.error.issues).toHaveLength(2);
    expect(zodV3Error(result.error).flatten().fieldErrors).toEqual({
      label: ["Expected string, received array"],
    });
    expect(zodV3Issues(result.error.issues).map((issue) => issue.message)).toEqual([
      "Expected string, received array",
    ]);
  });

  it("keeps a message the schema sets itself", () => {
    expect(messages(z.string().min(1, "name is required"), "")).toEqual([
      "name is required",
    ]);
  });

  it("answers an empty inventory string exactly as vctl's docs quote it", () => {
    const result = routerInventorySchema.safeParse({});
    expect(result.success).toBe(false);
    if (result.success) return;
    expect(result.error.flatten().fieldErrors.deviceIdentifier).toEqual([
      "Required",
    ]);
    const blank = routerInventorySchema.safeParse({ deviceIdentifier: "" });
    expect(blank.success).toBe(false);
    if (blank.success) return;
    expect(blank.error.flatten().fieldErrors.deviceIdentifier).toEqual([
      "String must contain at least 1 character(s)",
    ]);
  });
});

describe("zod 3 parsing semantics the contracts rely on", () => {
  it("still takes a non-RFC 8-4-4-4-12 id: zod 4's uuid() would refuse it", () => {
    expect(z.guid().safeParse("11111111-2222-3333-4444-555555555555").success).toBe(
      true,
    );
  });

  it("keeps a URL exactly as sent: zod 4's url() would trim and rewrite it", () => {
    for (const url of ["  https://example.test/a  ", "https://exa\nmple.test/a"]) {
      expect(urlStringSchema().parse(url)).toBe(url);
    }
    expect(urlStringSchema().safeParse("\u00a0https://example.test").success).toBe(
      false,
    );
    expect(messages(urlStringSchema(), "nope")).toEqual(["Invalid url"]);
  });

  it("takes zod 3's ISO timestamps: optional seconds, +hhmm offsets", () => {
    for (const value of ["2026-10-28T12:00Z", "2026-10-28T12:00:05.123Z"]) {
      expect(isoDatetimeSchema().safeParse(value).success).toBe(true);
    }
    expect(isoDatetimeSchema().safeParse("2026-10-28T12:00:05+03:00").success).toBe(
      false,
    );
    for (const value of ["2026-10-28T12:00+0300", "2026-10-28T12:00:05+03:00"]) {
      expect(isoDatetimeSchema({ offset: true }).safeParse(value).success).toBe(true);
    }
    expect(messages(isoDatetimeSchema(), "2026-02-30T00:00:00Z")).toEqual([
      "Invalid datetime",
    ]);
  });

  it("parses an object default through its schema, filling nested defaults", () => {
    const schema = z.object({
      nested: z
        .object({ enabled: z.boolean().default(true) })
        .prefault({}),
    });
    expect(schema.parse({})).toEqual({ nested: { enabled: true } });
  });

  it("drops unknown keys and fills defaults on a contract schema", () => {
    const parsed = passwallSubscriptionSchema.parse({
      id: "sub-1",
      remark: "main",
      url: "https://example.test/sub",
      unknownField: "dropped",
    });
    expect(parsed).not.toHaveProperty("unknownField");
    expect(parsed).toMatchObject({
      enabled: true,
      addMode: "2",
      metadata: {},
      extras: {},
    });
  });
});

/**
 * What zod 4 does differently and the contracts take as it is: every zod that
 * carries the GHSA-mrhx-c7xc-m3w5 fix behaves this way, and nothing routers
 * send today reaches either case.
 */
describe("zod 4 behaviour the contracts accept", () => {
  it("counts a string's length in code points, so max() admits more emoji", () => {
    // zod 3 counted UTF-16 units: two emoji were 4 and failed max(2). The
    // contracts carry no min(n > 1) or length(n) on free text, so only max()
    // moves, and only towards Postgres' and vctl's own count.
    expect(z.string().max(2).safeParse("\u{1F600}\u{1F600}").success).toBe(true);
  });

  it("refuses an integer past 2^53, which JSON could not carry exactly anyway", () => {
    expect(z.number().int().safeParse(2 ** 60).success).toBe(false);
  });
});
