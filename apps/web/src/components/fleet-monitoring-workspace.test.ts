import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

describe("FleetMonitoringWorkspace", () => {
  it("keeps fleet cards through tablet landscape and defers the dense table to xl", () => {
    const source = readFileSync(
      new URL("./fleet-monitoring-workspace.tsx", import.meta.url),
      "utf8",
    );

    expect(source).toContain('className="space-y-3 xl:hidden"');
    expect(source).toContain(
      'className="hidden rounded-[1.4rem] border border-white/10 bg-[rgba(8,11,17,0.76)] px-4 py-4 xl:block"',
    );
    expect(source).not.toContain('className="space-y-3 lg:hidden"');
    expect(source).not.toContain('className="max-lg:hidden rounded-[1.4rem]');
  });
});
