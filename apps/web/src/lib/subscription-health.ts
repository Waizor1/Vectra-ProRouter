/** Mirrors the optional `subscriptionHealth` block of a router snapshot. */
export type SubscriptionHealth = {
  hwidEnabled: boolean;
  scheduleEnabled: boolean;
  placeholderNodes: number;
};

type SubscriptionHealthLike = SubscriptionHealth | null | undefined;

/**
 * True when this router's next nightly subscription run will destroy its nodes.
 *
 * PassWall 26.7.16+ sends the hardware id only when `option hwid '1'` is set.
 * Without it the provider answers with a placeholder and the cron replaces every
 * real node with it — which is how nataliafilisiti and stewe lost their whole
 * node lists on 2026-09-16, hours after an upgrade that looked successful.
 *
 * Controllers older than the subscriptionHealth field report nothing. Silence is
 * not evidence that the gate is open, so it is treated as unknown: a false alarm
 * on every un-updated router would train the operator to ignore this alert.
 */
export function hasSubscriptionGateRisk(health: SubscriptionHealthLike) {
  if (!health) {
    return false;
  }
  return !health.hwidEnabled || !health.scheduleEnabled;
}

/**
 * True when the node list has ALREADY been replaced by the provider's stub.
 *
 * This is not a warning about tonight — the router is running on placeholder
 * nodes right now and its slots point at addresses that route nowhere.
 */
export function hasWipedNodeList(health: SubscriptionHealthLike) {
  if (!health) {
    return false;
  }
  return health.placeholderNodes > 0;
}

/** Names the specific missing option, so the fix is obvious from the alert. */
export function describeSubscriptionRisk(health: SubscriptionHealthLike) {
  if (!health) {
    return "Контроллер не сообщает состояние подписки.";
  }

  const problems: string[] = [];
  if (!health.hwidEnabled) {
    problems.push(
      "не выставлен hwid — подписка получит заглушку и сотрёт узлы",
    );
  }
  if (!health.scheduleEnabled) {
    problems.push("пустое расписание — крон обновления подписки не создаётся");
  }
  if (health.placeholderNodes > 0) {
    problems.push(
      `узлов-заглушек: ${health.placeholderNodes} — список узлов уже подменён`,
    );
  }

  return problems.length > 0
    ? problems.join("; ")
    : "Подписка настроена корректно.";
}
