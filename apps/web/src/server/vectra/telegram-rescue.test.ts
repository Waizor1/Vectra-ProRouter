import { describe, expect, it } from "vitest";

import {
  createTelegramRescueActionToken,
  parseTelegramAllowedChatIds,
  telegramRescueCallbackActions,
  verifyTelegramRescueActionToken,
} from "./telegram-rescue";

const secret = "telegram-callback-secret-for-tests-000000";

describe("telegram rescue callback tokens", () => {
  it("round-trips signed short-lived action tokens", () => {
    const token = createTelegramRescueActionToken({
      caseId: "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
      action: "collect_logs",
      expiresAt: new Date("2026-04-25T10:10:00.000Z"),
      secret,
    });

    expect(token).toBeTruthy();
    expect(
      verifyTelegramRescueActionToken(token!, {
        secret,
        now: new Date("2026-04-25T10:00:00.000Z"),
      }),
    ).toEqual({
      caseId: "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
      action: "collect_logs",
      exp: 1777111800,
    });
  });

  it("rejects bad signatures and expired tokens", () => {
    const token = createTelegramRescueActionToken({
      caseId: "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
      action: "silence_1h",
      expiresAt: new Date("2026-04-25T10:10:00.000Z"),
      secret,
    });

    expect(() =>
      verifyTelegramRescueActionToken(`${token!}tampered`, {
        secret,
        now: new Date("2026-04-25T10:00:00.000Z"),
      }),
    ).toThrow();
    expect(() =>
      verifyTelegramRescueActionToken(token!, {
        secret,
        now: new Date("2026-04-25T10:11:00.000Z"),
      }),
    ).toThrow(/expired/i);
  });

  it("keeps callback_data within Telegram inline button limits", () => {
    for (const action of telegramRescueCallbackActions) {
      const token = createTelegramRescueActionToken({
        caseId: "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
        action,
        expiresAt: new Date("2026-04-25T10:10:00.000Z"),
        secret,
      });
      expect(token).toBeTruthy();
      expect(
        Buffer.byteLength(`rescue:${token!}`, "utf8"),
        action,
      ).toBeLessThanOrEqual(64);
    }
  });
});

describe("parseTelegramAllowedChatIds", () => {
  it("parses csv chat ids without leaking token-like config", () => {
    expect(parseTelegramAllowedChatIds("123, 456,789")).toEqual(
      new Set(["123", "456", "789"]),
    );
  });
});
