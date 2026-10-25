import { BrowserWindow, screen } from "electron";
import {
  overlayHintsChannel,
  overlayPanelsChannel,
  overlayPrefsChannel,
  overlayToastChannel,
} from "../shared/overlay";
import type { OverlayHost } from "./overlay";

/**
 * The Electron side of the overlay (HUD-01, ADR-0063). The window is only
 * ever shown by showInactive() (overlay.ts); this file adds nothing that
 * could activate it. Its page cannot navigate, open windows or attach
 * webviews, like the main window's. A toast (HUD-04) is a message to the page
 * on overlayToastChannel, the page's settings (HUD-05) one on
 * overlayPrefsChannel, the key hints (HUD-03) one on overlayHintsChannel and a
 * script's panels (SCR-22) one on overlayPanelsChannel, Main to page only.
 */
export function electronOverlayHost(options: {
  pageURL: string;
  /** Absolute path of the overlay's preload (out/preload/overlay.js). */
  preload: string;
  secondaryDisplay: boolean;
}): OverlayHost {
  return {
    preload: options.preload,
    secondaryDisplay: options.secondaryDisplay,
    create: (windowOptions) => {
      const win = new BrowserWindow(windowOptions);
      win.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
      win.webContents.on("will-navigate", (event) => event.preventDefault());
      win.webContents.on("will-attach-webview", (event) =>
        event.preventDefault(),
      );
      return {
        setAlwaysOnTop: (flag, level) => win.setAlwaysOnTop(flag, level),
        setIgnoreMouseEvents: (ignore, mouse) =>
          win.setIgnoreMouseEvents(ignore, mouse),
        setBounds: (bounds) => win.setBounds(bounds),
        getBounds: () => win.getBounds(),
        showInactive: () => win.showInactive(),
        toast: (text, ms) => {
          if (win.isDestroyed() || win.webContents.isDestroyed()) return;
          const message = ms === undefined ? { text } : { text, ms };
          win.webContents.send(overlayToastChannel, message);
        },
        prefs: (settings) => {
          if (win.isDestroyed() || win.webContents.isDestroyed()) return;
          win.webContents.send(overlayPrefsChannel, settings);
        },
        hints: (message) => {
          if (win.isDestroyed() || win.webContents.isDestroyed()) return;
          win.webContents.send(overlayHintsChannel, message);
        },
        panels: (message) => {
          if (win.isDestroyed() || win.webContents.isDestroyed()) return;
          win.webContents.send(overlayPanelsChannel, message);
        },
        destroy: () => win.destroy(),
        isDestroyed: () => win.isDestroyed(),
        load: () => win.loadURL(options.pageURL),
        onReady: (listener) => win.once("ready-to-show", listener),
        onGone: (listener) =>
          win.webContents.on("render-process-gone", () => listener()),
      };
    },
    displays: () => ({
      primaryId: screen.getPrimaryDisplay().id,
      displays: screen
        .getAllDisplays()
        .map((display) => ({ id: display.id, workArea: display.workArea })),
    }),
    onDisplaysChanged: (listener) => {
      screen.on("display-added", listener);
      screen.on("display-removed", listener);
      screen.on("display-metrics-changed", listener);
    },
  };
}
