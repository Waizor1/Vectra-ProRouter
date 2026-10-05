/**
 * Background worker: runs the panel's background loops (auto-rescue, push
 * monitor, stuck-job janitor, retention, route health, partner webhooks) in a
 * process of their own, so their sweeps get the second CPU core instead of
 * competing with check-ins on the web's event loop.
 *
 * Built by `pnpm build:worker` into dist/worker/main.mjs and started as plain
 * `node` (compose service `worker`). It does work only when
 * VECTRA_BACKGROUND_MODE=worker-separate; in the default "in-web" mode it
 * stays idle and healthy, so switching modes is one env change for both
 * services and either direction is a plain `docker compose up -d`.
 *
 * Every tick holds its loop's PostgreSQL advisory lock (background-lock.ts),
 * so even a web still in in-web mode and this worker never run one loop at
 * the same time.
 */
import { writeFileSync } from "node:fs";

const HEARTBEAT_FILE =
  process.env.VECTRA_WORKER_HEARTBEAT_FILE ?? "/tmp/vectra-worker.alive";
const HEARTBEAT_INTERVAL_MS = 15_000;
const DRAIN_TIMEOUT_MS = 25_000;

function beat() {
  try {
    writeFileSync(HEARTBEAT_FILE, new Date().toISOString());
  } catch (error) {
    console.error("[worker] heartbeat file", error);
  }
}

async function main() {
  const mode = process.env.VECTRA_BACKGROUND_MODE ?? "in-web";
  if (mode !== "worker-separate") {
    console.info(
      "[worker] VECTRA_BACKGROUND_MODE=%s: the loops run in the web process; idling",
      mode,
    );
    beat();
    // Not unref'd: this timer is what keeps an idle worker alive.
    const timer = setInterval(beat, HEARTBEAT_INTERVAL_MS);
    const stop = () => {
      clearInterval(timer);
      process.exit(0);
    };
    process.once("SIGTERM", stop);
    process.once("SIGINT", stop);
    return;
  }

  // Only now load the application: env validation, the database pool and the
  // loop modules.
  const { startBackgroundLoops } = await import(
    "~/server/vectra/background-loops"
  );
  const {
    backgroundTicksInFlight,
    beginBackgroundDrain,
    holdLoopPresence,
  } = await import("~/server/vectra/background-lock");

  const started = startBackgroundLoops();
  const running = (Object.keys(started) as Array<keyof typeof started>).filter(
    (loop) => started[loop],
  );
  console.info("[worker] started loops: %s", running.join(", ") || "(none)");

  const presence = await holdLoopPresence(running);
  beat();

  let stopping = false;
  // Not unref'd: the loop timers are, so this is what keeps the worker alive.
  const heartbeat = setInterval(() => {
    presence.ping().then(beat, (error: unknown) => {
      if (stopping) return;
      // The presence locks went with the connection; restart cleanly rather
      // than run on invisibly (restart: unless-stopped brings us back).
      console.error("[worker] presence connection lost; exiting", error);
      process.exit(1);
    });
  }, HEARTBEAT_INTERVAL_MS);

  const shutdown = (signal: string) => {
    if (stopping) return;
    stopping = true;
    clearInterval(heartbeat);
    beginBackgroundDrain();
    console.info(
      "[worker] %s: draining %d tick(s) in flight",
      signal,
      backgroundTicksInFlight(),
    );
    const deadline = Date.now() + DRAIN_TIMEOUT_MS;
    const wait = setInterval(() => {
      if (backgroundTicksInFlight() === 0 || Date.now() >= deadline) {
        clearInterval(wait);
        // An interrupted tick is safe: its lock dies with the connection and
        // every sweep is written to be re-run by the next tick.
        // Releasing the presence locks must not hold up the exit if the
        // database is what went away.
        setTimeout(() => process.exit(0), 2_000).unref();
        void presence
          .release()
          .catch(() => undefined)
          .finally(() => process.exit(0));
      }
    }, 200);
  };
  process.once("SIGTERM", () => shutdown("SIGTERM"));
  process.once("SIGINT", () => shutdown("SIGINT"));
}

main().catch((error: unknown) => {
  console.error("[worker] failed to start", error);
  process.exit(1);
});
