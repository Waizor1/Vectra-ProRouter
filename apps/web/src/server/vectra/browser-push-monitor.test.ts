import { describe, expect, it, vi } from "vitest";

vi.mock("./browser-push", () => ({
  isBrowserPushConfigured: () => false,
  sendBrowserPushToAll: async () => undefined,
}));

import { buildFleetPushCandidates } from "./browser-push-monitor";

describe("buildFleetPushCandidates", () => {
  it("keeps only urgent operational alerts and derives stable dedupe keys", () => {
    const candidates = buildFleetPushCandidates({
      generatedAt: "2026-04-09T10:00:00.000Z",
      notificationNote: "",
      stats: [],
      charts: [],
      routers: [],
      totalAlerts: 5,
      alerts: [
        {
          id: "offline:router-1",
          kind: "offline",
          severity: "critical",
          routerId: "router-1",
          routerName: "Remote AX3000T",
          href: "/routers/router-1",
          title: "Нет свежей связи",
          description: "Последний check-in давно устарел.",
          openedAt: "2026-04-09T09:51:00.000Z",
          filters: {
            operational: "offline",
            freshness: "offline",
            memory: "unknown",
          },
        },
        {
          id: "incident:router-2:subscription_degraded",
          kind: "incident",
          severity: "warning",
          routerId: "router-2",
          routerName: "Shop NX31",
          href: "/routers/router-2",
          title: "Подписка деградировала",
          description: "Часть нод не подтянулась.",
          openedAt: "2026-04-09T09:55:00.000Z",
          filters: {
            operational: "recovery",
            freshness: "fresh",
            memory: "unknown",
          },
        },
        {
          id: "import_review:router-3",
          kind: "import_review",
          severity: "warning",
          routerId: "router-3",
          routerName: "Import Review",
          href: "/routers/router-3",
          title: "Нужна проверка импорта",
          description: "Требуется операторская проверка.",
          openedAt: "2026-04-09T09:58:00.000Z",
          filters: {
            operational: "review",
            freshness: "fresh",
            memory: "unknown",
          },
        },
        {
          id: "awaiting_import:router-4",
          kind: "awaiting_import",
          severity: "info",
          routerId: "router-4",
          routerName: "Awaiting Import",
          href: "/routers/router-4",
          title: "Ожидается первый импорт",
          description: "Для сведения.",
          openedAt: null,
          filters: {
            operational: "review",
            freshness: "never",
            memory: "unknown",
          },
        },
        {
          id: "direct:router-5",
          kind: "direct_mode",
          severity: "critical",
          routerId: "router-5",
          routerName: "Warehouse R5",
          href: "/routers/router-5",
          title: "Роутер в прямом режиме",
          description: "Локальный rescue увёл трафик в direct.",
          openedAt: "2026-04-09T09:57:00.000Z",
          filters: {
            operational: "recovery",
            freshness: "fresh",
            memory: "unknown",
          },
        },
      ],
    });

    expect(candidates).toEqual([
      {
        dedupeKey: "offline:router-1:2026-04-09T09:51:00.000Z",
        routerId: "router-1",
        routerName: "Remote AX3000T",
        kind: "offline",
        severity: "critical",
        title: "Нет свежей связи",
        body: "Последний check-in давно устарел.",
        href: "/routers/router-1",
        createdAt: "2026-04-09T09:51:00.000Z",
      },
      {
        dedupeKey:
          "incident:router-2:subscription_degraded:2026-04-09T09:55:00.000Z",
        routerId: "router-2",
        routerName: "Shop NX31",
        kind: "incident",
        severity: "warning",
        title: "Подписка деградировала",
        body: "Часть нод не подтянулась.",
        href: "/routers/router-2",
        createdAt: "2026-04-09T09:55:00.000Z",
      },
      {
        dedupeKey: "direct:router-5:2026-04-09T09:57:00.000Z",
        routerId: "router-5",
        routerName: "Warehouse R5",
        kind: "direct_mode",
        severity: "critical",
        title: "Роутер в прямом режиме",
        body: "Локальный rescue увёл трафик в direct.",
        href: "/routers/router-5",
        createdAt: "2026-04-09T09:57:00.000Z",
      },
    ]);
  });
});
