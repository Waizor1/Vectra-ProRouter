import { describe, expect, it } from "vitest";

import {
  describeTelegramReachability,
  formatTelegramReachabilityLabel,
  getTelegramReachabilityChecks,
  getTelegramReachabilityStatus,
  hasTelegramReachabilityProblem,
} from "./telegram-reachability";

describe("telegram reachability helpers", () => {
  it("returns empty-state labels when probe is missing", () => {
    expect(formatTelegramReachabilityLabel(null)).toBe("нет данных");
    expect(describeTelegramReachability(null)).toBe(
      "Агент ещё не присылал проверку Telegram.",
    );
    expect(hasTelegramReachabilityProblem(null)).toBe(false);
    expect(getTelegramReachabilityStatus(null)).toBe("unknown");
  });

  it("formats legacy single-target Telegram status", () => {
    const probe = {
      reachable: true,
      checkedAt: "2026-04-14T12:00:00.000Z",
      targetUrl: "https://telegram.org/",
    };

    expect(formatTelegramReachabilityLabel(probe)).toBe("доступна");
    expect(describeTelegramReachability(probe)).toBe("telegram.org отвечает");
    expect(hasTelegramReachabilityProblem(probe)).toBe(false);
    expect(getTelegramReachabilityStatus(probe)).toBe("reachable");
  });

  it("formats partial multi-target Telegram status", () => {
    const probe = {
      reachable: false,
      checkedAt: "2026-04-14T12:00:03.000Z",
      status: "partial" as const,
      reachableCount: 2,
      totalCount: 4,
      checks: [
        {
          label: "telegram.org",
          reachable: true,
          checkedAt: "2026-04-14T12:00:00.000Z",
          targetUrl: "https://telegram.org/",
        },
        {
          label: "web.telegram.org",
          reachable: false,
          checkedAt: "2026-04-14T12:00:01.000Z",
          targetUrl: "https://web.telegram.org/",
          statusCode: 403,
        },
        {
          label: "t.me",
          reachable: true,
          checkedAt: "2026-04-14T12:00:02.000Z",
          targetUrl: "https://t.me/",
        },
        {
          label: "api.telegram.org",
          reachable: false,
          checkedAt: "2026-04-14T12:00:03.000Z",
          targetUrl: "https://api.telegram.org/",
          error: "context deadline exceeded",
        },
      ],
    };

    expect(formatTelegramReachabilityLabel(probe)).toBe("частично доступна");
    expect(describeTelegramReachability(probe)).toBe(
      "Отвечают 2 из 4 целей Telegram. Не отвечают: web.telegram.org, api.telegram.org.",
    );
    expect(hasTelegramReachabilityProblem(probe)).toBe(true);
    expect(getTelegramReachabilityStatus(probe)).toBe("partial");
    expect(getTelegramReachabilityChecks(probe)).toEqual([
      {
        label: "telegram.org",
        reachable: true,
        checkedAt: "2026-04-14T12:00:00.000Z",
        detail: "telegram.org отвечает",
      },
      {
        label: "web.telegram.org",
        reachable: false,
        checkedAt: "2026-04-14T12:00:01.000Z",
        detail: "web.telegram.org вернул HTTP 403",
      },
      {
        label: "t.me",
        reachable: true,
        checkedAt: "2026-04-14T12:00:02.000Z",
        detail: "t.me отвечает",
      },
      {
        label: "api.telegram.org",
        reachable: false,
        checkedAt: "2026-04-14T12:00:03.000Z",
        detail: "api.telegram.org недоступен: context deadline exceeded",
      },
    ]);
  });

  it("formats fully blocked multi-target Telegram status", () => {
    const probe = {
      reachable: false,
      checkedAt: "2026-04-14T12:00:03.000Z",
      status: "blocked" as const,
      reachableCount: 0,
      totalCount: 2,
      checks: [
        {
          label: "telegram.org",
          reachable: false,
          checkedAt: "2026-04-14T12:00:00.000Z",
          targetUrl: "https://telegram.org/",
          error: "context deadline exceeded",
        },
        {
          label: "web.telegram.org",
          reachable: false,
          checkedAt: "2026-04-14T12:00:01.000Z",
          targetUrl: "https://web.telegram.org/",
          statusCode: 403,
        },
      ],
    };

    expect(formatTelegramReachabilityLabel(probe)).toBe("недоступна");
    expect(describeTelegramReachability(probe)).toBe(
      "Не отвечает ни одна из 2 целей Telegram.",
    );
    expect(hasTelegramReachabilityProblem(probe)).toBe(true);
    expect(getTelegramReachabilityStatus(probe)).toBe("blocked");
  });
});
