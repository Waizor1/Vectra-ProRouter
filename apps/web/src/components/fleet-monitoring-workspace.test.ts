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

  it("keeps RAM risk visible in the fleet monitoring surface", () => {
    const source = readFileSync(
      new URL("./fleet-monitoring-workspace.tsx", import.meta.url),
      "utf8",
    );

    expect(source).toContain("RAM риск:");
    expect(source).toContain("Мин. RAM:");
    expect(source).toContain("router.memory.summary");
    expect(source).toContain("router.memory.level");
  });

  it("keeps service outage filters visible without opening router details", () => {
    const source = readFileSync(
      new URL("./fleet-monitoring-workspace.tsx", import.meta.url),
      "utf8",
    );

    expect(source).toContain("Telegram сбои:");
    expect(source).toContain("YouTube сбои:");
    expect(source).toContain("Нет проб:");
    expect(source).toContain('value: "telegram_degraded"');
    expect(source).toContain('value: "youtube_degraded"');
    expect(source).toContain('value: "service_unknown"');
  });
});
