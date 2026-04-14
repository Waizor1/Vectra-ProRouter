// @ts-nocheck
self.addEventListener("push", (event) => {
  if (!event.data) {
    return;
  }

  let payload = {};
  try {
    payload = event.data.json();
  } catch {
    payload = {
      title: "Vectra",
      body: event.data.text(),
      href: "/fleet",
      tag: "vectra-push",
      severity: "warning",
    };
  }

  const title =
    typeof payload.title === "string" && payload.title.length > 0
      ? payload.title
      : "Vectra";
  const body =
    typeof payload.body === "string" && payload.body.length > 0
      ? payload.body
      : "Появилось новое уведомление по парку.";
  const href =
    typeof payload.href === "string" && payload.href.length > 0
      ? payload.href
      : "/fleet";
  const tag =
    typeof payload.tag === "string" && payload.tag.length > 0
      ? payload.tag
      : "vectra-push";
  const severity = payload.severity === "critical" ? "critical" : "warning";

  event.waitUntil(
    self.registration.showNotification(title, {
      body,
      tag,
      data: {
        href,
      },
      badge: "/favicon.ico",
      icon: "/favicon.ico",
      requireInteraction: severity === "critical",
    }),
  );
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();

  const href =
    event.notification.data && typeof event.notification.data.href === "string"
      ? event.notification.data.href
      : "/fleet";

  event.waitUntil(
    self.clients.matchAll({ type: "window", includeUncontrolled: true }).then(
      (clients) => {
        for (const client of clients) {
          if ("focus" in client) {
            client.navigate(href);
            return client.focus();
          }
        }

        if (self.clients.openWindow) {
          return self.clients.openWindow(href);
        }

        return undefined;
      },
    ),
  );
});
