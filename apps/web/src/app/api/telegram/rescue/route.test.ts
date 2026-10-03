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
vi.mock("~/server/vectra/auto-rescue", () => ({
  queueRescueCaseLogCollection: vi.fn(),
  queueRescueCaseReconnectProxy: vi.fn(),
  queueRescueCaseSafeRepair: vi.fn(),
  silenceRescueCase: vi.fn(),
}));
vi.mock("~/server/vectra/telegram-rescue", () => ({
  answerTelegramCallback: vi.fn(async () => undefined),
  isTelegramChatAllowed: vi.fn(() => true),
  verifyTelegramRescueActionToken: vi.fn(),
}));

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
});
