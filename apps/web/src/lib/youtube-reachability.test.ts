import { describe, expect, it } from "vitest";

import {
  describeYoutubeReachability,
  formatYoutubeReachabilityLabel,
  getYoutubeReachabilityChecks,
  getYoutubeReachabilityStatus,
  hasYoutubeReachabilityProblem,
  isYoutubeVideoPathDown,
} from "./youtube-reachability";

describe("youtube reachability helpers", () => {
  it("returns empty-state labels when probe is missing", () => {
    expect(formatYoutubeReachabilityLabel(null)).toBe("нет данных");
    expect(describeYoutubeReachability(null)).toBe(
      "Агент не прислал проверку YouTube: service-probes выполняются редко и пропускаются при low-memory или неработающем PassWall.",
    );
    expect(hasYoutubeReachabilityProblem(null)).toBe(false);
    expect(getYoutubeReachabilityStatus(null)).toBe("unknown");
  });

  it("formats legacy single-target YouTube status", () => {
    const probe = {
      reachable: true,
      checkedAt: "2026-04-14T12:00:00.000Z",
      targetUrl: "https://www.youtube.com/generate_204",
    };

    expect(formatYoutubeReachabilityLabel(probe)).toBe("доступна");
    expect(describeYoutubeReachability(probe)).toBe("youtube.com отвечает");
    expect(hasYoutubeReachabilityProblem(probe)).toBe(false);
    expect(getYoutubeReachabilityStatus(probe)).toBe("reachable");
  });

  it("formats partial multi-target YouTube status", () => {
    const probe = {
      reachable: false,
      checkedAt: "2026-04-14T12:00:03.000Z",
      status: "partial" as const,
      reachableCount: 2,
      totalCount: 4,
      checks: [
        {
          label: "youtube.com",
          reachable: true,
          checkedAt: "2026-04-14T12:00:00.000Z",
          targetUrl: "https://www.youtube.com/generate_204",
        },
        {
          label: "i.ytimg.com",
          reachable: false,
          checkedAt: "2026-04-14T12:00:01.000Z",
          targetUrl: "https://i.ytimg.com/generate_204",
          statusCode: 403,
        },
        {
          label: "youtubei.googleapis.com",
          reachable: true,
          checkedAt: "2026-04-14T12:00:02.000Z",
          targetUrl: "https://youtubei.googleapis.com/",
        },
        {
          label: "youtubei.googleapis.com",
          reachable: false,
          checkedAt: "2026-04-14T12:00:03.000Z",
          targetUrl: "https://youtubei.googleapis.com/generate_204",
          error: "context deadline exceeded",
        },
      ],
    };

    expect(formatYoutubeReachabilityLabel(probe)).toBe("частично доступна");
    expect(describeYoutubeReachability(probe)).toBe(
      "Отвечают 2 из 4 целей YouTube. Не отвечают: i.ytimg.com, youtubei.googleapis.com.",
    );
    expect(hasYoutubeReachabilityProblem(probe)).toBe(true);
    expect(getYoutubeReachabilityStatus(probe)).toBe("partial");
    expect(getYoutubeReachabilityChecks(probe)).toEqual([
      {
        label: "youtube.com",
        reachable: true,
        checkedAt: "2026-04-14T12:00:00.000Z",
        detail: "youtube.com отвечает",
      },
      {
        label: "i.ytimg.com",
        reachable: false,
        checkedAt: "2026-04-14T12:00:01.000Z",
        detail: "i.ytimg.com вернул HTTP 403",
      },
      {
        label: "youtubei.googleapis.com",
        reachable: true,
        checkedAt: "2026-04-14T12:00:02.000Z",
        detail: "youtubei.googleapis.com отвечает",
      },
      {
        label: "youtubei.googleapis.com",
        reachable: false,
        checkedAt: "2026-04-14T12:00:03.000Z",
        detail: "youtubei.googleapis.com недоступен: context deadline exceeded",
      },
    ]);
  });

  it("formats fully blocked multi-target YouTube status", () => {
    const probe = {
      reachable: false,
      checkedAt: "2026-04-14T12:00:03.000Z",
      status: "blocked" as const,
      reachableCount: 0,
      totalCount: 2,
      checks: [
        {
          label: "youtube.com",
          reachable: false,
          checkedAt: "2026-04-14T12:00:00.000Z",
          targetUrl: "https://www.youtube.com/generate_204",
          error: "context deadline exceeded",
        },
        {
          label: "i.ytimg.com",
          reachable: false,
          checkedAt: "2026-04-14T12:00:01.000Z",
          targetUrl: "https://i.ytimg.com/generate_204",
          statusCode: 403,
        },
      ],
    };

    expect(formatYoutubeReachabilityLabel(probe)).toBe("недоступна");
    expect(describeYoutubeReachability(probe)).toBe(
      "Не отвечает ни одна из 2 целей YouTube.",
    );
    expect(hasYoutubeReachabilityProblem(probe)).toBe(true);
    expect(getYoutubeReachabilityStatus(probe)).toBe("blocked");
  });
});

describe("isYoutubeVideoPathDown", () => {
  // The complaint "YouTube does not work" almost always means playback, not the
  // page: the bytes come from googlevideo. A probe where the page answers and
  // googlevideo does not is a total playback failure for that user, so it must
  // not be softened into the same "partially degraded" bucket as a slow
  // thumbnail host.
  it("flags a probe whose googlevideo check failed while the page answered", () => {
    expect(
      isYoutubeVideoPathDown({
        status: "partial",
        checks: [
          {
            label: "googlevideo.com",
            targetUrl: "https://www.googlevideo.com/generate_204",
            reachable: false,
            checkedAt: "2026-09-15T00:00:00Z",
          },
          {
            label: "youtube.com",
            targetUrl: "https://www.youtube.com/generate_204",
            reachable: true,
            checkedAt: "2026-09-15T00:00:00Z",
          },
        ],
      }),
    ).toBe(true);
  });

  it("does not flag a partial probe whose googlevideo check passed", () => {
    expect(
      isYoutubeVideoPathDown({
        status: "partial",
        checks: [
          {
            label: "googlevideo.com",
            targetUrl: "https://www.googlevideo.com/generate_204",
            reachable: true,
            checkedAt: "2026-09-15T00:00:00Z",
          },
          {
            label: "i.ytimg.com",
            targetUrl: "https://i.ytimg.com/generate_204",
            reachable: false,
            checkedAt: "2026-09-15T00:00:00Z",
          },
        ],
      }),
    ).toBe(false);
  });

  // Older controllers ship a probe set without a googlevideo target at all.
  // Absence of the check is not evidence of a healthy video path, but it is also
  // not evidence of a broken one, so it must not manufacture a critical alert.
  it("does not flag a probe that carries no googlevideo check", () => {
    expect(
      isYoutubeVideoPathDown({
        status: "partial",
        checks: [
          {
            label: "youtube.com",
            targetUrl: "https://www.youtube.com/generate_204",
            reachable: false,
            checkedAt: "2026-09-15T00:00:00Z",
          },
          {
            label: "i.ytimg.com",
            targetUrl: "https://i.ytimg.com/generate_204",
            reachable: true,
            checkedAt: "2026-09-15T00:00:00Z",
          },
        ],
      }),
    ).toBe(false);
  });

  it("returns false for an empty probe", () => {
    expect(isYoutubeVideoPathDown(null)).toBe(false);
  });
});
