import type { BrowserWindowConstructorOptions } from "electron";
import {
  defaultOverlayPrefs,
  hintsFadeMs,
  type OverlayHintsMessage,
  type OverlayPageSettings,
  type OverlayPanelsMessage,
  type OverlayPosition,
  type OverlayPrefs,
  overlayHeightForHints,
  overlayHeightForPanels,
  overlayMaxHeight,
  overlayToastAreaHeight,
  scriptPanelRows,
} from "../shared/overlay";
import { baseWebPreferences } from "./security";

// The overlay window (HUD-01, SPK-09, ADR-0063): a second window of the UI
// that is made once, never takes the foreground and lets the mouse through.
// This file is plain logic over a small window interface, so that the
// options, the placement and the lifecycle are tested without Electron;
// overlay-window.ts is the Electron side.
//
// Nothing here may show, focus, raise or hide the window: the one call that
// shows it is showInactive(), once. A window that took the foreground would
// be padmate's own window in front, and the daemon then stops sending keys
// (OUT-25). What the page shows is sent to it as a message (a toast, HUD-04;
// its opacity, HUD-05; key hints, HUD-03; a script's panels, SCR-22): that
// changes the page, never the window. Where the window sits (HUD-05) and how
// tall it is while key hints or panels show (HUD-03, SCR-22) are a setBounds
// and nothing more.

export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface OverlayDisplay {
  id: number;
  /** In DIP, as Electron's screen module reports it. */
  workArea: Rect;
}

export interface DisplayList {
  displays: OverlayDisplay[];
  primaryId: number;
}

/**
 * The window's title, before and after its page loads (the page's <title> is
 * the same). It is how the E2E specs tell the overlay from the main window:
 * BrowserWindow.getAllWindows() has no order to rely on, so [0] is not the
 * main window once the overlay exists (Electron 44 lists the newest first).
 */
export const overlayTitle = "padmate overlay";
/** Size in DIP. The height is the toast area's; hints and panels make the window taller (overlayMaxHeight). */
export const overlaySize = {
  width: 480,
  height: overlayToastAreaHeight,
} as const;
/** The tallest the window gets while key hints or a script's panels show, in DIP (HUD-03, SCR-22). */
export { overlayMaxHeight };
/** Distance of the window's bottom edge from the work area's, in DIP. */
export const overlayBottomGap = 48;
/** Distance of the window's top edge from the work area's, in DIP (HUD-05). */
export const overlayTopGap = 48;
/**
 * Whether mouse moves are forwarded to the page below the click-through
 * window. They are not: the overlay has nothing to hover, and on Windows
 * Electron implements forwarding with a process-wide WH_MOUSE_LL hook on the
 * main thread, which would put every mouse and pen move of the machine
 * behind the UI process (ADR-0063). Ignoring the mouse without forwarding
 * still sets WS_EX_TRANSPARENT and WS_EX_LAYERED.
 */
export const overlayForwardMouse = false;

/**
 * 480x160 at the centre of the work area's bottom, 48 above its bottom edge,
 * or with position "top" at the centre of its top, 48 below its top edge
 * (HUD-05). Not the whole screen: a topmost window that covers a display is
 * taken for a full-screen program. A work area smaller than that shrinks the
 * window, and one without room for the gap keeps as much of the gap as
 * fits; it never leaves the area.
 *
 * Key hints (HUD-03) and a script's panels (SCR-22) make the window taller, up
 * to overlayMaxHeight: the edge
 * next to the screen's edge stays where it is (the bottom edge at the bottom,
 * the top edge at the top), so the toast area does not move and the panel
 * grows away from the edge.
 */
export function overlayBounds(
  workArea: Rect,
  position: OverlayPosition = "bottom",
  wantedHeight: number = overlaySize.height,
): Rect {
  const width = Math.min(overlaySize.width, workArea.width);
  const height = Math.min(
    Math.max(overlaySize.height, Math.min(wantedHeight, overlayMaxHeight)),
    workArea.height,
  );
  const room = Math.max(0, workArea.height - height);
  const offset =
    position === "top"
      ? Math.min(overlayTopGap, room)
      : Math.max(0, room - overlayBottomGap);
  return {
    x: Math.round(workArea.x + (workArea.width - width) / 2),
    y: Math.round(workArea.y + offset),
    width,
    height,
  };
}

/**
 * The display the overlay goes on: the primary one, or with secondary (an
 * E2E run that moved its window off the primary display, helpers.ts
 * placeWindow) the first display that is not the primary one, the same rule
 * the test uses. A computer with one display has only the primary one.
 */
export function chooseOverlayDisplay(
  list: DisplayList,
  secondary: boolean,
): OverlayDisplay | undefined {
  const primary =
    list.displays.find((display) => display.id === list.primaryId) ??
    list.displays[0];
  if (!secondary) return primary;
  return (
    list.displays.find((display) => display.id !== list.primaryId) ?? primary
  );
}

/**
 * The overlay's window options. Its renderer is sandboxed like the main
 * window's. Its preload (HUD-04, preload/overlay.ts) only listens for
 * toasts: the page cannot call Main.
 */
export function overlayWindowOptions(
  bounds: Rect,
  preload: string,
): BrowserWindowConstructorOptions {
  return {
    ...bounds,
    title: overlayTitle,
    show: false,
    transparent: true,
    backgroundColor: "#00000000",
    frame: false,
    // WS_EX_NOACTIVATE on Windows, and it implies skipTaskbar.
    focusable: false,
    skipTaskbar: true,
    // WS_EX_TOOLWINDOW on Windows: kept out of Alt+Tab and the taskbar.
    type: "toolbar",
    resizable: false,
    movable: false,
    minimizable: false,
    maximizable: false,
    fullscreenable: false,
    hasShadow: false,
    alwaysOnTop: true,
    webPreferences: { preload, ...baseWebPreferences },
  };
}

/** Where the overlay's page is: next to the main page, in both modes. */
export function overlayPageURL(
  developmentServer: boolean,
  rendererURL: string | undefined,
): string {
  if (developmentServer && rendererURL)
    return new URL("overlay.html", rendererURL).href;
  return "padmate://app/overlay.html";
}

/** The window as the controller uses it: nothing that could activate it. */
export interface OverlayWindow {
  setAlwaysOnTop(flag: boolean, level: "screen-saver"): void;
  setIgnoreMouseEvents(ignore: boolean, options: { forward: boolean }): void;
  setBounds(bounds: Rect): void;
  getBounds(): Rect;
  /** Shows the window without activating it. */
  showInactive(): void;
  /** Sends a toast to the page (HUD-04); ms is how long it holds, if not the default. Only its content changes. */
  toast(text: string, ms?: number): void;
  /** Sends the page its settings (HUD-05). Only its content changes. */
  prefs(settings: OverlayPageSettings): void;
  /** Sends the page key hints, or none to hide them (HUD-03). Only its content changes. */
  hints(message: OverlayHintsMessage): void;
  /** Sends the page a script's panels, or none to hide them (SCR-22). Only its content changes. */
  panels(message: OverlayPanelsMessage): void;
  destroy(): void;
  isDestroyed(): boolean;
  /** Loads the page; rejects when it cannot load. */
  load(): Promise<void>;
  /** The page has drawn its first frame (ready-to-show). */
  onReady(listener: () => void): void;
  /** The renderer process went away (render-process-gone). */
  onGone(listener: () => void): void;
}

export interface OverlayHost {
  /** The overlay's preload script, an absolute path. */
  preload: string;
  create(options: BrowserWindowConstructorOptions): OverlayWindow;
  displays(): DisplayList;
  /** Displays were added or removed, or their metrics or work area changed. */
  onDisplaysChanged(listener: () => void): void;
  /** An E2E run that put its test window on the secondary display. */
  secondaryDisplay: boolean;
}

function sameRect(a: Rect, b: Rect): boolean {
  return (
    a.x === b.x && a.y === b.y && a.width === b.width && a.height === b.height
  );
}

/**
 * Makes the overlay once after the main window is ready and keeps it for the
 * life of the app. Its content will show and hide inside the page (opacity);
 * the window is not shown and hidden again. It is optional: nothing here
 * throws, and a window that cannot be made or loaded just means no overlay.
 */
export class OverlayController {
  private current: OverlayWindow | undefined;
  private started = false;
  private stopped = false;
  // The window is made again once after its renderer died, not forever.
  private recreated = false;
  // Where the window sits and how opaque the pill is (HUD-05): what the UI
  // last said, which a window made later starts with too.
  private position: OverlayPosition;
  private opacity: number;
  // Key hints (HUD-03): how many entries the window is tall for, the hints on
  // show (sent again to a window made after a crash) and the timer that
  // shrinks the window once the panel has faded out.
  private hintCount = 0;
  private shownHints: Omit<OverlayHintsMessage, "position"> | undefined;
  private shrink: ReturnType<typeof setTimeout> | undefined;
  // A script's panels (SCR-22), the same way: how many rows the window is
  // tall for, the panels on show and the timer of their shrink.
  private panelRows = 0;
  private shownPanels: Omit<OverlayPanelsMessage, "position"> | undefined;
  private shrinkPanels: ReturnType<typeof setTimeout> | undefined;
  private gaveUp = false;
  private gaveUpListener: (() => void) | undefined;
  // Whether a window is up now: its page has drawn its first frame (SCR-22).
  private ready = false;
  private readyListener: (() => void) | undefined;

  constructor(
    private readonly host: OverlayHost,
    settings: Partial<Pick<OverlayPrefs, "position" | "opacity">> = {},
  ) {
    this.position = settings.position ?? defaultOverlayPrefs.position;
    this.opacity = settings.opacity ?? defaultOverlayPrefs.opacity;
  }

  /** Makes the window. Only the first call does, and none after stop. */
  start(): void {
    if (this.started || this.stopped) return;
    this.started = true;
    this.host.onDisplaysChanged(() => this.place());
    this.open();
  }

  /** Destroys the window and makes no other: the main window closed, or the app is quitting. */
  stop(): void {
    this.stopped = true;
    this.cancelShrink();
    this.cancelShrinkPanels();
    this.discard();
  }

  /**
   * Calls listener whenever a window is ready: its page has drawn its first
   * frame and the window is shown. That is the first window, and the one made
   * again after its renderer died; at once if one is ready already. The
   * window is up for the UI's purposes from then on (SCR-22: a script is told
   * there is an overlay), not from when it is only being made.
   */
  onReady(listener: () => void): void {
    this.readyListener = listener;
    if (this.ready) this.notifyReady();
  }

  /**
   * Calls listener, once, when there is no window and there will be none: it
   * could not be made (no display, or Electron refused), its page could not
   * be loaded, or its renderer died a second time. Not for stop(): that is
   * the app ending.
   */
  onGaveUp(listener: () => void): void {
    this.gaveUpListener = listener;
    if (this.gaveUp) this.notifyGaveUp();
  }

  /** Whether there is a window now. */
  get active(): boolean {
    return !!this.current && !this.current.isDestroyed();
  }

  /**
   * Tells the page to show a toast (HUD-04), holding for ms if that is given
   * (a script's toast, SCR-22). Nothing happens without a window; the window
   * itself is not touched, so a toast cannot take the foreground.
   */
  toast(text: string, ms?: number): void {
    const win = this.current;
    if (!win || win.isDestroyed()) return;
    try {
      if (ms === undefined) win.toast(text);
      else win.toast(text, ms);
    } catch {
      // The page may be gone; a toast is a courtesy.
    }
  }

  /**
   * Shows key hints in the page, or with no entries hides them (HUD-03). The
   * window grows to fit the panel first and shrinks back after the panel
   * has faded out; both are a setBounds with the edge next to the screen's
   * edge fixed, never a show, focus or raise. Nothing happens without a
   * window; the next window made is sent the hints still on show.
   */
  hints(message: Omit<OverlayHintsMessage, "position">): void {
    if (this.stopped) return;
    if (message.entries.length > 0) {
      this.cancelShrink();
      this.shownHints = message;
      this.hintCount = message.entries.length;
      this.place();
      this.sendHints(message);
      return;
    }
    this.shownHints = undefined;
    this.sendHints(message);
    if (this.hintCount === 0 || this.shrink) return;
    // The panel fades out for hintsFadeMs; the window stays tall until then,
    // or the fade would be cut off.
    this.shrink = setTimeout(() => {
      this.shrink = undefined;
      this.hintCount = 0;
      this.place();
    }, hintsFadeMs + 40);
  }

  /**
   * Shows a script's panels in the page, or with none hides them (SCR-22),
   * the way hints() does: the window grows to fit them first and shrinks back
   * after they have faded out, by setBounds only. The page shows the hints and
   * not the panels while the hints are up, so the window is as tall as the
   * taller of the two and does not change size when they trade places.
   */
  panels(message: Omit<OverlayPanelsMessage, "position">): void {
    if (this.stopped) return;
    if (message.panels.length > 0) {
      this.cancelShrinkPanels();
      this.shownPanels = message;
      this.panelRows = scriptPanelRows(message.panels);
      this.place();
      this.sendPanels(message);
      return;
    }
    this.shownPanels = undefined;
    this.sendPanels(message);
    if (this.panelRows === 0 || this.shrinkPanels) return;
    this.shrinkPanels = setTimeout(() => {
      this.shrinkPanels = undefined;
      this.panelRows = 0;
      this.place();
    }, hintsFadeMs + 40);
  }

  /**
   * Takes the position and opacity the UI chose (HUD-05); a field that is
   * missing or the same as before changes nothing. A new position is a
   * setBounds on the window and a new opacity a message to its page: the
   * window is not shown, focused or raised.
   */
  configure(
    settings: Partial<Pick<OverlayPrefs, "position" | "opacity">>,
  ): void {
    if (
      settings.position !== undefined &&
      settings.position !== this.position
    ) {
      this.position = settings.position;
      this.place();
      // The page lays the panel out by the position: tell it the new one.
      if (this.shownHints) this.sendHints(this.shownHints);
      if (this.shownPanels) this.sendPanels(this.shownPanels);
    }
    if (settings.opacity !== undefined && settings.opacity !== this.opacity) {
      this.opacity = settings.opacity;
      this.sendSettings();
    }
  }

  private open(): void {
    try {
      const area = chooseOverlayDisplay(
        this.host.displays(),
        this.host.secondaryDisplay,
      )?.workArea;
      if (!area) {
        this.giveUp();
        return;
      }
      const win = this.host.create(
        overlayWindowOptions(
          overlayBounds(area, this.position, this.height),
          this.host.preload,
        ),
      );
      this.current = win;
      this.applyStyles(win);
      win.onReady(() => {
        if (this.current !== win || win.isDestroyed()) return;
        // Styles set before the window was first mapped may not have stuck;
        // setting them again is harmless.
        this.applyStyles(win);
        // Created on a display of another scale it may have come out the
        // wrong size or place; put it right before it is shown.
        this.place();
        // The page listens from its preload on and keeps the latest, so
        // this reaches it whether or not it has subscribed yet.
        this.sendSettings();
        if (this.shownHints) this.sendHints(this.shownHints);
        if (this.shownPanels) this.sendPanels(this.shownPanels);
        win.showInactive();
        this.ready = true;
        this.notifyReady();
      });
      win.onGone(() => this.gone(win));
      win.load().catch(() => {
        // A page that does not load makes no overlay: the window goes and the
        // overlay is given up, as when no window could be made.
        if (this.current !== win) return;
        this.discard();
        this.giveUp();
      });
    } catch {
      this.discard();
      this.giveUp();
    }
  }

  /** How tall the window is now, in DIP: for the taller of the hints and the panels. */
  private get height(): number {
    return Math.max(
      overlayHeightForHints(this.hintCount),
      overlayHeightForPanels(this.panelRows),
    );
  }

  private cancelShrink(): void {
    if (this.shrink) clearTimeout(this.shrink);
    this.shrink = undefined;
  }

  private cancelShrinkPanels(): void {
    if (this.shrinkPanels) clearTimeout(this.shrinkPanels);
    this.shrinkPanels = undefined;
  }

  private giveUp(): void {
    if (this.gaveUp || this.stopped) return;
    this.gaveUp = true;
    this.notifyGaveUp();
  }

  private notifyReady(): void {
    try {
      this.readyListener?.();
    } catch {
      // Whoever asked to be told cannot make the overlay any worse off.
    }
  }

  private notifyGaveUp(): void {
    try {
      this.gaveUpListener?.();
    } catch {
      // Whoever asked to be told cannot make the overlay any worse off.
    }
  }

  /** Sends the page hints, with where the window sits; nothing without a window. */
  private sendHints(message: Omit<OverlayHintsMessage, "position">): void {
    const win = this.current;
    if (!win || win.isDestroyed()) return;
    try {
      win.hints({ ...message, position: this.position });
    } catch {
      // The page may be gone; hints are a courtesy.
    }
  }

  /** Sends the page a script's panels, with where the window sits; nothing without a window. */
  private sendPanels(message: Omit<OverlayPanelsMessage, "position">): void {
    const win = this.current;
    if (!win || win.isDestroyed()) return;
    try {
      win.panels({ ...message, position: this.position });
    } catch {
      // The page may be gone; panels are a courtesy.
    }
  }

  /** Tells the page how opaque the pill is; nothing without a window. */
  private sendSettings(): void {
    const win = this.current;
    if (!win || win.isDestroyed()) return;
    try {
      win.prefs({ opacity: this.opacity });
    } catch {
      // The page may be gone; the next window is sent them when it is ready.
    }
  }

  private applyStyles(win: OverlayWindow): void {
    win.setAlwaysOnTop(true, "screen-saver");
    win.setIgnoreMouseEvents(true, { forward: overlayForwardMouse });
  }

  private gone(win: OverlayWindow): void {
    if (win !== this.current) return;
    this.discard();
    if (this.stopped) return;
    if (this.recreated) {
      this.giveUp();
      return;
    }
    this.recreated = true;
    this.open();
  }

  private discard(): void {
    const win = this.current;
    this.current = undefined;
    this.ready = false;
    if (win && !win.isDestroyed()) win.destroy();
  }

  /** Puts the window where the displays say, without showing it. */
  private place(): void {
    const win = this.current;
    if (!win || win.isDestroyed()) return;
    try {
      const area = chooseOverlayDisplay(
        this.host.displays(),
        this.host.secondaryDisplay,
      )?.workArea;
      if (!area) return;
      const bounds = overlayBounds(area, this.position, this.height);
      if (sameRect(win.getBounds(), bounds)) return;
      win.setBounds(bounds);
      // A window moved between displays of different scale can come out
      // with the wrong size; once more settles it (not measured here).
      if (!sameRect(win.getBounds(), bounds)) win.setBounds(bounds);
    } catch {
      // Optional: a display change that cannot be followed leaves it as is.
    }
  }
}
