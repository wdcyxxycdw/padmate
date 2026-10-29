import type { ConnectionState } from "../shared/api";

type Phase = ConnectionState["phase"];

/**
 * Whether closing the main window only hides it (ADR-0077). While the daemon
 * is connected, padmate.exe stays with the page and the controller's model
 * loaded, so a second start (the tray's 打开, the shortcuts) shows the window
 * at once. The window closes for real, and the application quits as before,
 * while it quits anyway (quitting; leaving: a quit held for the saves, or the
 * daemon's exit, DMN-22, whose close must go through), and when there is
 * nothing to come back to: no connection, or no page (UI-45).
 */
export function closeHides(state: {
  quitting: boolean;
  leaving: boolean;
  phase: Phase | undefined;
  pageReady: boolean;
}): boolean {
  return (
    !state.quitting &&
    !state.leaving &&
    state.pageReady &&
    state.phase === "connected"
  );
}

/**
 * Whether a window hidden by its close quits the application now (ADR-0077):
 * once the daemon's connection has ended, a UI nobody sees has nothing left
 * to do, and a second start begins afresh.
 */
export function quitsWhileHidden(hidden: boolean, phase: Phase): boolean {
  return hidden && (phase === "disconnected" || phase === "failed");
}
