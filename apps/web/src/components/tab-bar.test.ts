import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

describe("TabBar", () => {
  it("wraps chips on mobile and only enables horizontal scrolling on large screens", () => {
    const source = readFileSync(
      new URL("./tab-bar.tsx", import.meta.url),
      "utf8",
    );

    expect(source).toContain("min-w-0 pb-1 lg:overflow-x-auto");
    expect(source).toContain(
      "flex w-full flex-wrap items-center gap-1 pr-1 lg:min-w-max lg:flex-nowrap lg:snap-x lg:snap-mandatory",
    );
    expect(source).toContain(
      "min-w-0 max-w-full items-center justify-center",
    );
    expect(source).toContain(
      "whitespace-normal transition lg:snap-start lg:whitespace-nowrap",
    );
    expect(source).not.toContain(
      "flex min-w-max snap-x snap-mandatory items-center gap-1 pr-1",
    );
  });
});
