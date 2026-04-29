import crypto from "node:crypto";

import { env } from "~/env";

export const telegramRescueCallbackActions = [
  "run_safe_repair",
  "reconnect_proxy",
  "collect_logs",
  "silence_1h",
] as const;

export type TelegramRescueCallbackAction =
  (typeof telegramRescueCallbackActions)[number];

export type TelegramSendResult = {
  attempted: boolean;
  delivered: number;
  dryRun: boolean;
  reason?: string;
};

type TelegramActionTokenPayload = {
  caseId: string;
  action: TelegramRescueCallbackAction;
  exp: number;
};

type TelegramButton =
  | {
      text: string;
      url: string;
    }
  | {
      text: string;
      callback_data: string;
    };

const telegramCallbackTokenVersion = "r1";
const telegramCallbackSignatureChars = 11;
const telegramCallbackActionCodes = {
  run_safe_repair: "s",
  reconnect_proxy: "r",
  collect_logs: "l",
  silence_1h: "q",
} as const satisfies Record<TelegramRescueCallbackAction, string>;

function signPayload(payload: string, secret: string) {
  return crypto
    .createHmac("sha256", secret)
    .update(payload)
    .digest("base64url");
}

function compactCaseId(caseId: string) {
  const compact = caseId.replaceAll("-", "").toLowerCase();
  if (!/^[0-9a-f]{32}$/.test(compact)) {
    throw new Error("Telegram callback token requires a UUID case id.");
  }
  return compact;
}

function expandCaseId(compact: string) {
  if (!/^[0-9a-f]{32}$/.test(compact)) {
    throw new Error("Telegram callback token has malformed case id.");
  }
  return [
    compact.slice(0, 8),
    compact.slice(8, 12),
    compact.slice(12, 16),
    compact.slice(16, 20),
    compact.slice(20),
  ].join("-");
}

function actionFromCode(code: string): TelegramRescueCallbackAction {
  const match = telegramRescueCallbackActions.find(
    (action) => telegramCallbackActionCodes[action] === code,
  );
  if (!match) {
    throw new Error("Telegram callback token has unsupported action.");
  }
  return match;
}

export function parseTelegramAllowedChatIds(
  value = env.VECTRA_TELEGRAM_ALLOWED_CHAT_IDS,
) {
  return new Set(
    (value ?? "")
      .split(",")
      .map((entry) => entry.trim())
      .filter(Boolean),
  );
}

export function isTelegramChatAllowed(
  chatId: string | number | null | undefined,
) {
  if (chatId === null || chatId === undefined) {
    return false;
  }

  const allowed = parseTelegramAllowedChatIds();
  return allowed.has(String(chatId));
}

export function createTelegramRescueActionToken(args: {
  caseId: string;
  action: TelegramRescueCallbackAction;
  expiresAt: Date;
  secret?: string;
}) {
  const secret = args.secret ?? env.VECTRA_TELEGRAM_CALLBACK_SECRET;
  if (!secret) {
    return null;
  }

  const payload = [
    telegramCallbackTokenVersion,
    compactCaseId(args.caseId),
    telegramCallbackActionCodes[args.action],
    Math.floor(args.expiresAt.getTime() / 1000).toString(36),
  ].join(".");
  return `${payload}.${signPayload(payload, secret).slice(
    0,
    telegramCallbackSignatureChars,
  )}`;
}

export function verifyTelegramRescueActionToken(
  token: string,
  options: { secret?: string; now?: Date } = {},
): TelegramActionTokenPayload {
  const secret = options.secret ?? env.VECTRA_TELEGRAM_CALLBACK_SECRET;
  if (!secret) {
    throw new Error("Telegram callback secret is not configured.");
  }

  const [version, compact, actionCode, expBase36, signature, ...extra] =
    token.split(".");
  if (
    extra.length > 0 ||
    version !== telegramCallbackTokenVersion ||
    !compact ||
    !actionCode ||
    !expBase36 ||
    !signature
  ) {
    throw new Error("Malformed Telegram callback token.");
  }

  const payload = [version, compact, actionCode, expBase36].join(".");
  const expected = signPayload(payload, secret).slice(
    0,
    telegramCallbackSignatureChars,
  );
  const expectedBuffer = Buffer.from(expected);
  const actualBuffer = Buffer.from(signature);
  if (
    expectedBuffer.length !== actualBuffer.length ||
    !crypto.timingSafeEqual(expectedBuffer, actualBuffer)
  ) {
    throw new Error("Invalid Telegram callback signature.");
  }

  const exp = Number.parseInt(expBase36, 36);
  if (!Number.isFinite(exp)) {
    throw new Error("Telegram callback token is missing expiry.");
  }

  const nowSeconds = Math.floor((options.now ?? new Date()).getTime() / 1000);
  if (exp < nowSeconds) {
    throw new Error("Telegram callback token has expired.");
  }

  return {
    caseId: expandCaseId(compact),
    action: actionFromCode(actionCode),
    exp,
  };
}

function buildCallbackButton(args: {
  text: string;
  caseId: string;
  action: TelegramRescueCallbackAction;
  expiresAt: Date;
}): TelegramButton | null {
  const token = createTelegramRescueActionToken({
    caseId: args.caseId,
    action: args.action,
    expiresAt: args.expiresAt,
  });
  if (!token) {
    return null;
  }

  return {
    text: args.text,
    callback_data: `rescue:${token}`,
  };
}

function rescueCaseUrl(caseId: string) {
  return new URL(
    `/rescue/cases/${caseId}`,
    env.VECTRA_DEFAULT_CONTROL_DOMAIN,
  ).toString();
}

export function buildTelegramRescueButtons(caseId: string, now = new Date()) {
  const expiresAt = new Date(now.getTime() + 60 * 60 * 1000);
  const rowOne: TelegramButton[] = [
    {
      text: "Open cockpit",
      url: rescueCaseUrl(caseId),
    },
  ];
  const rowTwo = [
    buildCallbackButton({
      text: "Run safe repair",
      caseId,
      action: "run_safe_repair",
      expiresAt,
    }),
    buildCallbackButton({
      text: "Reconnect proxy",
      caseId,
      action: "reconnect_proxy",
      expiresAt,
    }),
  ].filter((button): button is TelegramButton => button !== null);
  const rowThree = [
    buildCallbackButton({
      text: "Collect logs",
      caseId,
      action: "collect_logs",
      expiresAt,
    }),
    buildCallbackButton({
      text: "Silence 1h",
      caseId,
      action: "silence_1h",
      expiresAt,
    }),
  ].filter((button): button is TelegramButton => button !== null);

  return [rowOne, rowTwo, rowThree].filter((row) => row.length > 0);
}

function telegramConfigured() {
  return (
    Boolean(env.VECTRA_TELEGRAM_BOT_TOKEN) &&
    parseTelegramAllowedChatIds().size > 0
  );
}

async function postTelegram(method: string, body: Record<string, unknown>) {
  if (!env.VECTRA_TELEGRAM_BOT_TOKEN) {
    return;
  }

  const response = await fetch(
    `https://api.telegram.org/bot${env.VECTRA_TELEGRAM_BOT_TOKEN}/${method}`,
    {
      method: "POST",
      headers: {
        "content-type": "application/json",
      },
      body: JSON.stringify(body),
    },
  );

  if (!response.ok) {
    const preview = await response.text();
    throw new Error(
      `Telegram ${method} failed with ${response.status}: ${preview.slice(0, 300)}`,
    );
  }
}

export async function sendTelegramRescueMessage(args: {
  caseId: string;
  text: string;
  includeButtons?: boolean;
}): Promise<TelegramSendResult> {
  if (env.VECTRA_TELEGRAM_DRY_RUN) {
    return {
      attempted: false,
      delivered: 0,
      dryRun: true,
      reason: "dry-run enabled",
    };
  }

  if (!telegramConfigured()) {
    return {
      attempted: false,
      delivered: 0,
      dryRun: false,
      reason: "Telegram bot token or allowed chat ids are not configured",
    };
  }

  const replyMarkup = args.includeButtons
    ? { inline_keyboard: buildTelegramRescueButtons(args.caseId) }
    : undefined;
  let delivered = 0;
  for (const chatId of parseTelegramAllowedChatIds()) {
    await postTelegram("sendMessage", {
      chat_id: chatId,
      text: args.text,
      disable_web_page_preview: true,
      reply_markup: replyMarkup,
    });
    delivered += 1;
  }

  return {
    attempted: true,
    delivered,
    dryRun: false,
  };
}

export async function answerTelegramCallback(args: {
  callbackQueryId: string;
  text: string;
  alert?: boolean;
}) {
  if (env.VECTRA_TELEGRAM_DRY_RUN || !env.VECTRA_TELEGRAM_BOT_TOKEN) {
    return;
  }

  await postTelegram("answerCallbackQuery", {
    callback_query_id: args.callbackQueryId,
    text: args.text,
    show_alert: args.alert ?? false,
  });
}
