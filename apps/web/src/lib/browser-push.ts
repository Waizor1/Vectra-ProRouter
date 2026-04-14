export function browserSupportsServiceWorkerPush() {
  return (
    typeof window !== "undefined" &&
    "Notification" in window &&
    "serviceWorker" in navigator &&
    "PushManager" in window
  );
}

function decodeBase64Url(base64Url: string) {
  const padding = "=".repeat((4 - (base64Url.length % 4)) % 4);
  const normalized = (base64Url + padding).replace(/-/g, "+").replace(/_/g, "/");
  const raw = window.atob(normalized);

  return Uint8Array.from(raw, (character) => character.charCodeAt(0));
}

export async function registerVectraPushWorker() {
  return navigator.serviceWorker.register("/vectra-push-sw.js");
}

export async function getVectraPushSubscription() {
  const registration = await registerVectraPushWorker();
  const subscription = await registration.pushManager.getSubscription();

  return {
    registration,
    subscription,
  };
}

export async function subscribeToVectraPush(publicKey: string) {
  const registration = await registerVectraPushWorker();
  const existingSubscription = await registration.pushManager.getSubscription();
  if (existingSubscription) {
    return existingSubscription;
  }

  return registration.pushManager.subscribe({
    userVisibleOnly: true,
    applicationServerKey: decodeBase64Url(publicKey),
  });
}
