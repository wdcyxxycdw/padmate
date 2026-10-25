import type { DaemonEvent } from "../shared/api";

/**
 * Why the daemon stops for good (daemon.exiting): the user's 退出 in its
 * tray (DMN-22), or the installer replacing or removing padmate (PKG-04,
 * `padmatectl quit`). The UI answers both the same way.
 */
const EXIT_REASONS = ["tray", "installer"] as const;
export type DaemonExitReason = (typeof EXIT_REASONS)[number];

function exitReason(data: unknown): DaemonExitReason | undefined {
  if (data === null || typeof data !== "object") return undefined;
  const reason = (data as { reason?: unknown }).reason;
  return EXIT_REASONS.find((known) => known === reason);
}

/**
 * Routes the daemon's events (DMN-22). daemon.exiting with reason "tray",
 * the user's 退出 in the daemon's tray, or "installer" quits the UI, once
 * (trayExit, with the reason); any other reason changes nothing, and the end
 * of the connection shows as before. The renderer never gets
 * daemon.exiting; every other event is delivered.
 */
export function routeDaemonEvents(options: {
  deliver: (event: DaemonEvent) => void;
  trayExit: (reason: DaemonExitReason) => void;
}): (event: DaemonEvent) => void {
  let exited = false;
  return (event) => {
    if (event.topic !== "daemon.exiting") {
      options.deliver(event);
      return;
    }
    const reason = exitReason(event.data);
    if (exited || reason === undefined) return;
    exited = true;
    options.trayExit(reason);
  };
}

/** How long the answer to the daemon (ui.exitReady) is waited for at most. */
export const ACK_WAIT_MS = 500;

/**
 * What the UI does once the daemon announced its exit, the tray's 退出 or the
 * installer's (DMN-22, PKG-04): the renderer finishes its saves like a
 * closing window (settle, bounded by the caller), the UI tells the daemon it
 * is ready (ready, ui.exitReady), then
 * it quits. A save that is slow, a daemon that does not answer or a call
 * that fails never keep it from quitting: the daemon gives up by itself
 * after 2 s, and a connection that ends first is no loss to show.
 */
export async function exitAfterSaves(options: {
  settle: () => Promise<void>;
  ready: () => Promise<unknown> | undefined;
  quit: () => void;
  ackWaitMs?: number;
}): Promise<void> {
  try {
    await options.settle();
  } catch {
    // Nothing more to wait for.
  }
  let timer: ReturnType<typeof setTimeout> | undefined;
  const limit = new Promise<void>((done) => {
    timer = setTimeout(done, options.ackWaitMs ?? ACK_WAIT_MS);
  });
  const answered = Promise.resolve()
    .then(() => options.ready())
    .then(
      () => {},
      () => {},
    );
  await Promise.race([answered, limit]);
  clearTimeout(timer);
  options.quit();
}

/**
 * Whether the window's close event is held (DMN-22). While the daemon's exit
 * (the tray's or the installer's) waits for the saves, every close is held,
 * because the exit quits by itself. Its own quit closes the window again, and that close must go
 * through: a prevented close cancels the quit and leaves a hidden window
 * and a live process. Otherwise the close guard decides (guardHolds, which
 * is only asked then).
 */
export function holdsWindowClose(
  state: { trayExiting: boolean; quitting: boolean },
  guardHolds: () => boolean,
): boolean {
  if (state.trayExiting) return !state.quitting;
  return guardHolds();
}
