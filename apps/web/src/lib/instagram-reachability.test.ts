import { describe, expect, it } from "vitest";

import {
  describeInstagramReachability,
  formatInstagramReachabilityLabel,
  getInstagramReachabilityChecks,
  getInstagramReachabilityStatus,
  hasInstagramReachabilityProblem,
} from "./instagram-reachability";

describe("instagram reachability helpers", () => {
  it("returns empty-state labels when probe is missing", () => {
    expect(formatInstagramReachabilityLabel(null)).toBe("нет данных");
    expect(describeInstagramReachability(null)).toBe(
      "Агент не прислал проверку Instagram: service-probes выполняются редко и пропускаются при low-memory или неработающем PassWall.",
    );
    expect(hasInstagramReachabilityProblem(null)).toBe(false);
    expect(getInstagramReachabilityStatus(null)).toBe("unknown");
  });

  it("formats single-target reachable Instagram status", () => {
    const probe = {
      reachable: true,
      checkedAt: "2026-05-26T12:00:00.000Z",
      targetUrl: "https://www.instagram.com/",
    };

    expect(formatInstagramReachabilityLabel(probe)).toBe("доступна");
    expect(describeInstagramReachability(probe)).toBe(
      "instagram.com отвечает",
    );
    expect(hasInstagramReachabilityProblem(probe)).toBe(false);
    expect(getInstagramReachabilityStatus(probe)).toBe("reachable");
  });

  it("formats partial multi-target Instagram status", () => {
    const probe = {
      reachable: false,
      checkedAt: "2026-05-26T12:00:01.000Z",
      status: "partial" as const,
      reachableCount: 1,
      totalCount: 2,
      checks: [
        {
          label: "instagram.com",
          reachable: true,
          checkedAt: "2026-05-26T12:00:00.000Z",
          targetUrl: "https://www.instagram.com/",
        },
        {
          label: "cdninstagram.com",
          reachable: false,
          checkedAt: "2026-05-26T12:00:01.000Z",
          targetUrl: "https://www.cdninstagram.com/",
          error: "context deadline exceeded",
        },
      ],
    };

    expect(formatInstagramReachabilityLabel(probe)).toBe("частично доступна");
    expect(describeInstagramReachability(probe)).toBe(
      "Отвечают 1 из 2 целей Instagram. Не отвечают: cdninstagram.com.",
    );
    expect(hasInstagramReachabilityProblem(probe)).toBe(true);
    expect(getInstagramReachabilityStatus(probe)).toBe("partial");
    expect(getInstagramReachabilityChecks(probe)).toEqual([
      {
        label: "instagram.com",
        reachable: true,
        checkedAt: "2026-05-26T12:00:00.000Z",
        detail: "instagram.com отвечает",
      },
      {
        label: "cdninstagram.com",
        reachable: false,
        checkedAt: "2026-05-26T12:00:01.000Z",
        detail: "cdninstagram.com недоступен: context deadline exceeded",
      },
    ]);
  });

  it("formats fully blocked multi-target Instagram status", () => {
    const probe = {
      reachable: false,
      checkedAt: "2026-05-26T12:00:01.000Z",
      status: "blocked" as const,
      reachableCount: 0,
      totalCount: 2,
      checks: [
        {
          label: "instagram.com",
          reachable: false,
          checkedAt: "2026-05-26T12:00:00.000Z",
          targetUrl: "https://www.instagram.com/",
          error: "context deadline exceeded",
        },
        {
          label: "cdninstagram.com",
          reachable: false,
          checkedAt: "2026-05-26T12:00:01.000Z",
          targetUrl: "https://www.cdninstagram.com/",
          statusCode: 403,
        },
      ],
    };

    expect(formatInstagramReachabilityLabel(probe)).toBe("недоступна");
    expect(describeInstagramReachability(probe)).toBe(
      "Не отвечает ни одна из 2 целей Instagram.",
    );
    expect(hasInstagramReachabilityProblem(probe)).toBe(true);
    expect(getInstagramReachabilityStatus(probe)).toBe("blocked");
  });
});
