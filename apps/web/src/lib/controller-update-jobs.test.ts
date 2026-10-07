import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import path from "node:path";

import { describe, expect, it } from "vitest";

import { buildTerminalControllerSelfUpdatePayload } from "~/lib/controller-update-jobs";
import { controllerUpdateGuardScript } from "~/lib/controller-update-guard-script";

import { renderGuardModule } from "../../scripts/sync-controller-update-guard.mjs";

const repoRoot = path.resolve(__dirname, "../../../..");
const guardSource = path.join(
  repoRoot,
  "router/vectra-controller-agent/openwrt/update-guard/vectra-update-guard.sh",
);

function build(guardTimings?: Parameters<
  typeof buildTerminalControllerSelfUpdatePayload
>[0]["guardTimings"]) {
  const payload = buildTerminalControllerSelfUpdatePayload({
    artifactVersion: "0.1.13-r47",
    packageArtifacts: [
      {
        name: "vectra-controller-agent",
        artifactUrl: "https://api.vectra-pro.net/a/vectra-controller-agent.ipk",
        sha256: "aaa",
      },
      {
        name: "luci-app-vectra-controller",
        artifactUrl: "https://api.vectra-pro.net/a/luci-app-vectra-controller.ipk",
        sha256: "bbb",
      },
    ],
    guardTimings,
  });
  if (!payload) {
    throw new Error("expected a payload");
  }
  return payload.command;
}

describe("controller self-update command with the rollback guard", () => {
  it("embeds the guard generated from the canonical .sh", () => {
    expect(
      renderGuardModule(readFileSync(guardSource, "utf8")),
    ).toBe(
      readFileSync(
        path.join(__dirname, "controller-update-guard-script.ts"),
        "utf8",
      ),
    );
    const command = build();
    expect(command).toContain(
      `cat > "$guard.new" <<'VECTRA_UPDATE_GUARD_EOF'`,
    );
    expect(command).toContain(`\n${controllerUpdateGuardScript}\nVECTRA_UPDATE_GUARD_EOF\n`);
    expect(controllerUpdateGuardScript.split("\n")).not.toContain(
      "VECTRA_UPDATE_GUARD_EOF",
    );
  });

  it("backs up before installing, refuses without a copy, and hands the restart to the guard", () => {
    const command = build();
    const order = [
      "  exit 72",
      'verify_ipk_has_agent "$agent_ipk"',
      'sh "$guard" prepare "$target_version" || guard_rc=$?',
      'if [ "$guard_rc" -ne 0 ]; then [ -f "$guard_dir/meta" ] || rm -rf "$guard_dir"; exit "$guard_rc"; fi',
      "installing=1",
      'install_pair || fail "opkg install controller/LuCI pair"',
      "verify_agent_on_disk",
      'sh "$guard" arm',
      'sh "$guard" launch',
    ].map((needle) => {
      // Whole command lines, not the helper definitions above them.
      const index = command.indexOf(`\n${needle}\n`);
      expect(index, needle).toBeGreaterThanOrEqual(0);
      return index;
    });
    expect([...order].sort((a, b) => a - b)).toEqual(order);
    expect(command).toContain("exit 73");
    expect(command).toContain('sh "$guard" restore-files "$*"');
    expect(command).toContain("(previous version restored)");
    // The old fire-and-forget restart without a way back is gone.
    expect(command).not.toContain("schedule_restart");
    expect(command.length).toBeLessThanOrEqual(32000);
  });

  it("passes an operator force to prepare (past the 75 refusal) only when forced", () => {
    const forced = buildTerminalControllerSelfUpdatePayload({
      artifactVersion: "0.1.13-r47",
      packageArtifacts: [
        { name: "vectra-controller-agent", artifactUrl: "https://x/a.ipk", sha256: "a" },
        { name: "luci-app-vectra-controller", artifactUrl: "https://x/l.ipk", sha256: "b" },
      ],
      force: true,
    });
    expect(forced?.command).toContain('\nVECTRA_GUARD_FORCE=1 sh "$guard" prepare');
    expect(build()).not.toContain("VECTRA_GUARD_FORCE=1 sh");
  });

  it("passes stand timings to prepare only when asked", () => {
    expect(build()).toContain('\nsh "$guard" prepare "$target_version"');
    expect(build({ stable: 20, contact: 90 })).toContain(
      'VECTRA_GUARD_STABLE_SECONDS=20 VECTRA_GUARD_CONTACT_SECONDS=90 sh "$guard" prepare',
    );
  });

  it.each(["sh", "dash"])("parses under %s", (shell) => {
    const probe = spawnSync(shell, ["-c", "exit 0"]);
    if (probe.error) {
      return;
    }
    const result = spawnSync(shell, ["-n", "-c", build()], {
      encoding: "utf8",
    });
    expect(result.stderr).toBe("");
    expect(result.status).toBe(0);
  });
});
