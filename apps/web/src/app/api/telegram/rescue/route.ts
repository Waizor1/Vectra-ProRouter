import { createHash, timingSafeEqual } from "node:crypto";

import { env } from "~/env";
import {
  RescueActionRefusedError,
  queueRescueCaseLogCollection,
  queueRescueCaseReconnectProxy,
  queueRescueCaseSafeRepair,
  silenceRescueCase,
} from "~/server/vectra/auto-rescue";
import {
  answerTelegramCallback,
  isTelegramChatAllowed,
  verifyTelegramRescueActionToken,
} from "~/server/vectra/telegram-rescue";

export const dynamic = "force-dynamic";

type TelegramCallbackQuery = {
  id?: string;
  data?: string;
  from?: {
    id?: number;
  };
  message?: {
    chat?: {
      id?: number | string;
    };
  };
};

type TelegramUpdate = {
  callback_query?: TelegramCallbackQuery;
};

function reject(status: number, message: string) {
  return Response.json({ ok: false, error: message }, { status });
}

function sha256(value: string) {
  return createHash("sha256").update(value).digest();
}

let missingSecretWarned = false;

function validateWebhookSecret(request: Request) {
  if (!env.VECTRA_TELEGRAM_WEBHOOK_SECRET) {
    // Left open so a deployment without the secret keeps working; the action
    // tokens and the chat allowlist still gate every action.
    if (env.NODE_ENV === "production" && !missingSecretWarned) {
      missingSecretWarned = true;
      console.warn(
        "[telegram-rescue] VECTRA_TELEGRAM_WEBHOOK_SECRET is not set: the webhook accepts unsigned updates.",
      );
    }
    return true;
  }

  // Digests, so the comparison is constant-time and length-independent.
  return timingSafeEqual(
    sha256(request.headers.get("x-telegram-bot-api-secret-token") ?? ""),
    sha256(env.VECTRA_TELEGRAM_WEBHOOK_SECRET),
  );
}

export async function POST(request: Request) {
  if (!validateWebhookSecret(request)) {
    return reject(401, "invalid webhook secret");
  }

  const update = (await request.json()) as TelegramUpdate;
  const callback = update.callback_query;
  if (!callback?.id || !callback.data?.startsWith("rescue:")) {
    return Response.json({ ok: true, ignored: true });
  }

  const chatId = callback.message?.chat?.id ?? callback.from?.id ?? null;
  if (!isTelegramChatAllowed(chatId)) {
    await answerTelegramCallback({
      callbackQueryId: callback.id,
      text: "Unauthorized chat.",
      alert: true,
    }).catch(() => null);
    return reject(403, "unauthorized chat");
  }

  let tokenPayload: ReturnType<typeof verifyTelegramRescueActionToken>;
  try {
    tokenPayload = verifyTelegramRescueActionToken(
      callback.data.slice("rescue:".length),
    );
  } catch (error) {
    await answerTelegramCallback({
      callbackQueryId: callback.id,
      text: error instanceof Error ? error.message : "Invalid action token.",
      alert: true,
    }).catch(() => null);
    return reject(400, "invalid token");
  }

  try {
    switch (tokenPayload.action) {
      case "run_safe_repair":
        await queueRescueCaseSafeRepair({
          caseId: tokenPayload.caseId,
          requestedBy: "telegram",
        });
        await answerTelegramCallback({
          callbackQueryId: callback.id,
          text: "Safe repair queued.",
        });
        break;
      case "reconnect_proxy":
        await queueRescueCaseReconnectProxy(tokenPayload.caseId, "telegram");
        await answerTelegramCallback({
          callbackQueryId: callback.id,
          text: "Reconnect proxy queued.",
        });
        break;
      case "collect_logs":
        await queueRescueCaseLogCollection(tokenPayload.caseId);
        await answerTelegramCallback({
          callbackQueryId: callback.id,
          text: "Log collection queued.",
        });
        break;
      case "silence_1h":
        await silenceRescueCase(tokenPayload.caseId, 60 * 60);
        await answerTelegramCallback({
          callbackQueryId: callback.id,
          text: "Rescue case silenced for 1 hour.",
        });
        break;
    }
  } catch (error) {
    await answerTelegramCallback({
      callbackQueryId: callback.id,
      text: error instanceof Error ? error.message : "Action failed.",
      alert: true,
    }).catch(() => null);
    if (error instanceof RescueActionRefusedError) {
      return reject(error.status, error.message);
    }
    return reject(400, "action failed");
  }

  return Response.json({ ok: true });
}
