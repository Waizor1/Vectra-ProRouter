import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { describe, expect, it } from "vitest";

/**
 * The container only receives what docker-compose.yml lists under
 * `environment:`. `--env-file` expands `${...}` inside the compose file; it
 * does NOT hand the .env through to the process.
 *
 * So a key that exists in production .env but is missing from compose does not
 * fail loudly — the server reads `undefined` and silently takes the schema
 * default. For VECTRA_AUTO_RESCUE_ENABLED that default is `false`, which meant
 * the entire self-repair layer (rescue cases, auto-repair, escalation, and the
 * stale-park unpark sweep) was switched off in production from 2026-08-24 while
 * production .env said `true` and /api/health reported the monitor as running.
 * Nobody noticed until a customer had been parked in direct mode for two days.
 *
 * This test is the guard: add a server key to env.js and compose must carry it.
 */
const REPO_ROOT = resolve(process.cwd(), "..", "..");

function serverEnvKeys() {
  const source = readFileSync(resolve(process.cwd(), "src", "env.js"), "utf8");
  // The `server:` block declares the schema; the `runtimeEnv:` block below it
  // repeats every name, so reading the file as a whole is enough to collect
  // them and the duplicates collapse into the set.
  return new Set(source.match(/VECTRA_[A-Z0-9_]+/g) ?? []);
}

function composeForwardedKeys() {
  const source = readFileSync(resolve(REPO_ROOT, "docker-compose.yml"), "utf8");
  const environmentBlock = /\n {4}environment:\n((?: {6}.*\n|\n)*)/g;
  const forwarded = new Set<string>();
  for (const match of source.matchAll(environmentBlock)) {
    for (const key of match[1]?.match(/^ {6}(VECTRA_[A-Z0-9_]+):/gm) ?? []) {
      forwarded.add(key.trim().replace(":", ""));
    }
  }
  return forwarded;
}

describe("deployment env contract", () => {
  it("forwards every server env key to the container", () => {
    const declared = serverEnvKeys();
    const forwarded = composeForwardedKeys();

    const missing = [...declared].filter((key) => !forwarded.has(key)).sort();

    expect(
      missing,
      `docker-compose.yml does not forward these keys, so the server will read them as undefined and fall back to schema defaults: ${missing.join(", ")}`,
    ).toEqual([]);
  });

  it("forwards the auto-rescue switch specifically", () => {
    // Called out on its own because this is the one whose default silently
    // disables a whole subsystem rather than just retuning it.
    expect(composeForwardedKeys()).toContain("VECTRA_AUTO_RESCUE_ENABLED");
  });
});
