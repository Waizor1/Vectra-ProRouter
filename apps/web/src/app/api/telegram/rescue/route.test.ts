import { describe, expect, it, vi } from "vitest";

const envMock = vi.hoisted(() => ({
  env: {
    NODE_ENV: "production",
    VECTRA_TELEGRAM_WEBHOOK_SECRET: "webhook-secret-0123456789" as
      | string
      | undefined,
  },
}));

vi.mock("~/env", () => envMock);
const autoRescue = vi.hoisted(() => {
  class RescueActionRefusedError extends Error {
    readonly status = 400;
  }
  return {
    RescueActionRefusedError,
    queueRescueCaseLogCollection: vi.fn(),
  };
});
const telegram = vi.hoisted(() => ({
  answerTelegramCallback: vi.fn(async () => undefined),
  isTelegramChatAllowed: vi.fn(() => true),
  verifyTelegramRescueActionToken: vi.fn(),
}));

vi.mock("~/server/vectra/auto-rescue", () => ({
  RescueActionRefusedError: autoRescue.RescueActionRefusedError,
  queueRescueCaseLogCollection: autoRescue.queueRescueCaseLogCollection,
  queueRescueCaseReconnectProxy: vi.fn(),
  queueRescueCaseSafeRepair: vi.fn(),
  silenceRescueCase: vi.fn(),
}));
vi.mock("~/server/vectra/telegram-rescue", () => telegram);

const { POST } = await import("./route");

function update(secret?: string) {
  return POST(
    new Request("https://example.test/api/telegram/rescue", {
      method: "POST",
      headers: secret ? { "x-telegram-bot-api-secret-token": secret } : {},
      body: JSON.stringify({}),
    }),
  );
}

describe("POST /api/telegram/rescue", () => {
  it("accepts only the configured webhook secret", async () => {
    expect((await update("webhook-secret-0123456789")).status).toBe(200);
    expect((await update("webhook-secret-012345678")).status).toBe(401);
    expect((await update("x")).status).toBe(401);
    expect((await update()).status).toBe(401);
  });

  it("keeps working without a secret, and says so", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
    envMock.env.VECTRA_TELEGRAM_WEBHOOK_SECRET = undefined;

    expect((await update()).status).toBe(200);
    expect(warn).toHaveBeenCalledWith(
      expect.stringContaining("VECTRA_TELEGRAM_WEBHOOK_SECRET is not set"),
    );

    envMock.env.VECTRA_TELEGRAM_WEBHOOK_SECRET = "webhook-secret-0123456789";
    warn.mockRestore();
  });

  // vctl never runs collect_router_logs: the button answers with why, as a
  // typed 400, not the generic "action failed".
  it("answers a refused log collection with its reason", async () => {
    telegram.verifyTelegramRescueActionToken.mockReturnValueOnce({
      caseId: "case-1",
      action: "collect_logs",
    });
    autoRescue.queueRescueCaseLogCollection.mockRejectedValueOnce(
      new autoRescue.RescueActionRefusedError(
        "vctl routers: logs come from vctl",
      ),
    );

    const response = await POST(
      new Request("https://example.test/api/telegram/rescue", {
        method: "POST",
        headers: {
          "x-telegram-bot-api-secret-token": "webhook-secret-0123456789",
        },
        body: JSON.stringify({
          callback_query: {
            id: "cb-1",
            data: "rescue:token",
            message: { chat: { id: 1 } },
          },
        }),
      }),
    );

    expect(response.status).toBe(400);
    expect(await response.json()).toEqual({
      ok: false,
      error: "vctl routers: logs come from vctl",
    });
    expect(telegram.answerTelegramCallback).toHaveBeenCalledWith(
      expect.objectContaining({ text: "vctl routers: logs come from vctl" }),
    );
  });
});
