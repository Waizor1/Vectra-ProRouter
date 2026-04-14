type BrowserAlertCandidate = {
  id: string;
  severity: "critical" | "warning" | "info";
};

export function pickFreshAlertsForBrowser<T extends BrowserAlertCandidate>(
  alerts: T[],
  previousAlertIds: Set<string>,
) {
  return alerts.filter(
    (alert) => alert.severity !== "info" && !previousAlertIds.has(alert.id),
  );
}
