import { contextBridge, ipcRenderer } from "electron";
import {
  hintsFrom,
  type OverlayAPI,
  overlayHintsChannel,
  overlayPanelsChannel,
  overlayPrefsChannel,
  overlayToastChannel,
  pageSettingsFrom,
  panelsFrom,
  toastFrom,
} from "../shared/overlay";

// The overlay's preload (HUD-04, HUD-05, HUD-03, SCR-22, ADR-0063). The
// overlay is a sandboxed, context-isolated page with no Node; this exposes
// four things to it: a listener for the toasts Main sends, one for its
// settings (the opacity), one for the key hints and one for a script's
// panels. It never calls Main (no send, invoke or postMessage), so the page
// cannot reach an IPC handler, and a message that is not a toast, settings,
// hints or panels is dropped here.

/**
 * Listens on a channel from the moment this script runs and keeps the latest
 * message that is valid, so that a page that subscribes after Main has sent
 * it still gets it, at once. Main sends the settings, the hints and the
 * panels when they change and not again, and the page subscribes when it has
 * started, which may be after the first message. A message that hides (no
 * entries, no panels) is a message too and replaces the one before: a page
 * that subscribes then has nothing to show.
 */
function keepLatest<T>(
  channel: string,
  parse: (payload: unknown) => T | undefined,
): (listener: (message: T) => void) => () => void {
  let latest: T | undefined;
  const listeners = new Set<(message: T) => void>();
  ipcRenderer.on(channel, (_event, payload: unknown) => {
    const next = parse(payload);
    if (!next) return;
    latest = next;
    for (const listener of listeners) listener(next);
  });
  return (listener) => {
    listeners.add(listener);
    if (latest) listener(latest);
    return () => {
      listeners.delete(listener);
    };
  };
}

const api: OverlayAPI = {
  onToast: (listener) => {
    const callback = (_event: Electron.IpcRendererEvent, payload: unknown) => {
      const toast = toastFrom(payload);
      if (toast) listener(toast.text, toast.ms);
    };
    ipcRenderer.on(overlayToastChannel, callback);
    return () => {
      ipcRenderer.removeListener(overlayToastChannel, callback);
    };
  },
  onPrefs: keepLatest(overlayPrefsChannel, pageSettingsFrom),
  onHints: keepLatest(overlayHintsChannel, hintsFrom),
  onPanels: keepLatest(overlayPanelsChannel, panelsFrom),
};

contextBridge.exposeInMainWorld("padmateOverlay", Object.freeze(api));
