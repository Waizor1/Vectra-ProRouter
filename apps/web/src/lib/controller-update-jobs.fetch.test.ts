import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  chmodSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";

import { afterEach, describe, expect, it } from "vitest";

import {
  buildTerminalControllerSelfUpdatePayload,
  controllerSelfUpdateFetchAttempts,
  controllerSelfUpdateFetchAttemptTimeoutSeconds,
  controllerSelfUpdateInstallGateSeconds,
} from "~/lib/controller-update-jobs";

// ekotri-avito and yarik, 2026-10-23: their ISPs drop part of the connections
// to api.vectra-pro.net, and a single wget per file failed the update four
// times running. These tests run the download helpers the panel generates in
// a real POSIX shell, with a fake downloader standing in for uclient-fetch.

const AGENT = "vectra-controller-agent ipk bytes";
const LUCI = "luci-app-vectra-controller ipk bytes";
const sha = (value: string) => createHash("sha256").update(value).digest("hex");
const AGENT_URL = "https://api.vectra-pro.net/a/vectra-controller-agent.ipk";
const LUCI_URL = "https://api.vectra-pro.net/a/luci-app-vectra-controller.ipk";

function generatedCommand() {
  const payload = buildTerminalControllerSelfUpdatePayload({
    artifactVersion: "0.1.13-r50",
    packageArtifacts: [
      { name: "vectra-controller-agent", artifactUrl: AGENT_URL, sha256: sha(AGENT) },
      { name: "luci-app-vectra-controller", artifactUrl: LUCI_URL, sha256: sha(LUCI) },
    ],
  });
  if (!payload) {
    throw new Error("expected a payload");
  }
  return payload.command;
}

// Command lines taken verbatim from the generated script, first occurrence of
// each prefix (`installing=0` is set again after the install).
function commandLines(prefixes: string[]) {
  const all = generatedCommand().split("\n");
  const lines = prefixes.map((prefix) => all.find((line) => line.startsWith(prefix)));
  expect(lines.every(Boolean), `command carries ${prefixes.join(", ")}`).toBe(true);
  return lines.join("\n");
}

const downloadHelpers = () =>
  commandLines([
    "mono() {",
    "installing=0",
    "fail() {",
    "fetch_attempts=",
    "fetch_timeout=",
    "fetch_budget_seconds=",
    "fetch_retry_delay=",
    "fetch_deadline=0",
    "fetch_once() {",
    "fetch() {",
  ]);

const workDirs: string[] = [];
afterEach(() => {
  for (const dir of workDirs.splice(0)) {
    rmSync(dir, { recursive: true, force: true });
  }
});

type Options = {
  /** Name the fake downloader is installed under. */
  downloader?: "uclient-fetch" | "wget";
  /** Call numbers (1-based, across both files) that succeed; others fail. */
  succeedOn?: number[];
  /** Calls that "succeed" with the wrong bytes. */
  badOn?: number[];
  /** Shell run after the helpers, before the fetches. */
  overrides?: string;
  /** Fetch the LuCI package too, after the agent. */
  both?: boolean;
  /** Also install a `wget` that always fails, as GNU wget with retries would stand in. */
  decoyWget?: boolean;
};

function runFetch(options: Options) {
  const work = mkdtempSync(path.join(tmpdir(), "vectra-fetch-"));
  workDirs.push(work);
  const bin = path.join(work, "bin");
  mkdirSync(bin);
  const counter = path.join(work, "calls");
  const argsLog = path.join(work, "args");
  const clock = path.join(work, "clock");
  writeFileSync(clock, "1000");

  // Every call costs the per-attempt timeout on a fake clock (a hung attempt
  // and a slow good one look the same to the budget). Calls listed in
  // SUCCEED_ON serve the bytes for the file being fetched; the rest fail like
  // a dropped connection (exit 4, partial file).
  writeFileSync(
    path.join(bin, options.downloader ?? "uclient-fetch"),
    [
      "#!/bin/sh",
      'n=$(cat "$COUNTER" 2>/dev/null || echo 0); n=$((n + 1)); echo "$n" > "$COUNTER"',
      'echo "$*" >> "$ARGS_LOG"',
      'echo $(( $(cat "$CLOCK") + $ATTEMPT_COST )) > "$CLOCK"',
      'out=""; url=""; while [ $# -gt 0 ]; do case "$1" in -O) out="$2"; shift 2 ;; -*) shift ;; *) url="$1"; shift ;; esac; done',
      'case " $BAD_ON " in *" $n "*) printf garbage > "$out"; exit 0 ;; esac',
      'case " $SUCCEED_ON " in *" $n "*) ;; *) echo partial > "$out"; exit 4 ;; esac',
      'case "$url" in *luci*) printf "%s" "$LUCI" > "$out" ;; *) printf "%s" "$AGENT" > "$out" ;; esac',
    ].join("\n"),
  );
  chmodSync(path.join(bin, options.downloader ?? "uclient-fetch"), 0o755);
  if (options.decoyWget) {
    writeFileSync(path.join(bin, "wget"), '#!/bin/sh\necho DECOY_WGET >> "$ARGS_LOG"\nexit 9\n');
    chmodSync(path.join(bin, "wget"), 0o755);
  }
  // sha256sum is busybox on the router; macOS dev machines only have shasum.
  writeFileSync(
    path.join(bin, "sha256sum"),
    '#!/bin/sh\nif command -v shasum >/dev/null 2>&1; then exec shasum -a 256 "$@"; fi\nexec /usr/bin/sha256sum "$@"\n',
  );
  chmodSync(path.join(bin, "sha256sum"), 0o755);

  const script = [
    "set -eu",
    downloadHelpers(),
    // The fake clock replaces seconds-since-boot; the retry delay advances it.
    'mono() { cat "$CLOCK"; }',
    'sleep() { echo $(( $(cat "$CLOCK") + $1 )) > "$CLOCK"; }',
    options.overrides ?? "",
    `fetch "${path.join(work, "agent.ipk")}" '${AGENT_URL}' '${sha(AGENT)}'`,
    options.both ? `fetch "${path.join(work, "luci.ipk")}" '${LUCI_URL}' '${sha(LUCI)}'` : "",
    'echo "FETCH_OK elapsed=$(( $(mono) - 1000 ))"',
  ].join("\n");

  const result = spawnSync("sh", ["-c", script], {
    encoding: "utf8",
    env: {
      ...process.env,
      // A real uclient-fetch/wget on the test host must not answer instead.
      PATH: `${bin}:/usr/bin:/bin`,
      COUNTER: counter,
      ARGS_LOG: argsLog,
      CLOCK: clock,
      ATTEMPT_COST: String(controllerSelfUpdateFetchAttemptTimeoutSeconds),
      AGENT,
      LUCI,
      SUCCEED_ON: (options.succeedOn ?? []).join(" "),
      BAD_ON: (options.badOn ?? []).join(" "),
    },
  });
  const calls = existsSync(counter) ? Number(readFileSync(counter, "utf8").trim()) : 0;
  const args = existsSync(argsLog) ? readFileSync(argsLog, "utf8") : "";
  return { ...result, calls, args };
}

describe("controller self-update downloads", () => {
  it("retries a dropped connection and continues with the attempt that works", () => {
    const run = runFetch({ succeedOn: [3] });

    expect(run.status, run.stderr).toBe(0);
    expect(run.stdout).toContain("FETCH_OK");
    expect(run.calls).toBe(3);
    expect(run.stderr).toContain(`retrying (1/${controllerSelfUpdateFetchAttempts})`);
    expect(run.stderr).toContain(`retrying (2/${controllerSelfUpdateFetchAttempts})`);
    // Every attempt is time-boxed, so one stalled connection cannot eat the job.
    expect(run.args).toContain(`-T ${controllerSelfUpdateFetchAttemptTimeoutSeconds} `);
  });

  it("gives up after the attempt limit with the cause and the count", () => {
    const run = runFetch({});

    expect(run.status).toBe(1);
    expect(run.stdout).not.toContain("FETCH_OK");
    expect(run.calls).toBe(controllerSelfUpdateFetchAttempts);
    expect(run.stderr).toContain(
      `controller self-update failed: download ${AGENT_URL}: exit 4 after ${controllerSelfUpdateFetchAttempts} attempt(s)`,
    );
  });

  it("stops retrying once the shared download budget is spent", () => {
    // A budget already used up: the first failure is the last attempt.
    const run = runFetch({ overrides: "fetch_budget_seconds=0" });

    expect(run.status).toBe(1);
    expect(run.calls).toBe(1);
    expect(run.stderr).toContain("after 1 attempt(s)");
  });

  it("shares one budget across both packages: worst case 66 s, then one try at LuCI", () => {
    // Agent fails 3 times and lands on the 4th (call 4, at 54 s); the LuCI
    // package then gets exactly one attempt because the deadline has passed.
    const succeeded = runFetch({ succeedOn: [4, 5], both: true });
    expect(succeeded.status, succeeded.stderr).toBe(0);
    expect(succeeded.stdout).toContain("FETCH_OK elapsed=66");

    const failed = runFetch({ succeedOn: [4], both: true });
    expect(failed.status).toBe(1);
    expect(failed.calls).toBe(5);
    expect(failed.stderr).toContain(`download ${LUCI_URL}: exit 4 after 1 attempt(s)`);
  });

  it("downloads again when the bytes do not match the published sha256", () => {
    const run = runFetch({ badOn: [1], succeedOn: [2] });

    expect(run.status, run.stderr).toBe(0);
    expect(run.calls).toBe(2);
    expect(run.stderr).toContain("sha256 mismatch, retrying (1/");
  });

  it("prefers uclient-fetch even when a wget is installed", () => {
    // With wget-ssl installed `wget` is GNU wget, whose own 20 retries would
    // spend the whole job on a single attempt.
    const run = runFetch({ succeedOn: [1], decoyWget: true });

    expect(run.status, run.stderr).toBe(0);
    expect(run.args).not.toContain("DECOY_WGET");
  });

  it("falls back to wget when there is no uclient-fetch", () => {
    const run = runFetch({ downloader: "wget", succeedOn: [1] });

    expect(run.status, run.stderr).toBe(0);
    expect(run.args).toContain(`-T ${controllerSelfUpdateFetchAttemptTimeoutSeconds} `);
  });

  it("verifies each package inside fetch, with no separate sha step left behind", () => {
    const command = generatedCommand();

    expect(command).toContain(`\nfetch "$agent_ipk" '${AGENT_URL}' '${sha(AGENT)}'\n`);
    expect(command).toContain(`\nfetch "$luci_ipk" '${LUCI_URL}' '${sha(LUCI)}'\n`);
    expect(command).not.toContain("check_sha");
  });
});

describe("controller self-update install gate", () => {
  // -T only catches a stalled connection; a slow live transfer is unbounded.
  // The gate is what keeps a late download from starting an install the 120 s
  // job limit would then kill halfway.
  function runGate(elapsedSeconds: number) {
    const script = [
      "set -eu",
      commandLines(["installing=0", "fail() {"]),
      "job_started=1000",
      `mono() { echo ${1000 + elapsedSeconds}; }`,
      commandLines(["elapsed="]),
      "echo INSTALL_WOULD_START",
    ].join("\n");
    return spawnSync("sh", ["-c", script], { encoding: "utf8" });
  }

  it("lets the install start inside the gate", () => {
    const run = runGate(controllerSelfUpdateInstallGateSeconds);
    expect(run.status, run.stderr).toBe(0);
    expect(run.stdout).toContain("INSTALL_WOULD_START");
  });

  it("refuses to start the install past the gate, before anything was touched", () => {
    const run = runGate(controllerSelfUpdateInstallGateSeconds + 1);
    expect(run.status).toBe(1);
    expect(run.stdout).not.toContain("INSTALL_WOULD_START");
    expect(run.stderr).toContain(
      `controller self-update failed: downloads took ${controllerSelfUpdateInstallGateSeconds + 1}s; not installing`,
    );
  });

  it("sits after the downloads and before the guard copies anything", () => {
    const command = generatedCommand();
    const gate = command.indexOf("\nelapsed=");
    expect(gate).toBeGreaterThan(command.indexOf('\nverify_ipk_has_agent "$agent_ipk"\n'));
    expect(gate).toBeLessThan(command.indexOf('\nmkdir -p "$guard_dir"'));
  });
});
