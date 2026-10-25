import { readdirSync, readFileSync } from "node:fs";
import type { BrowserWindowConstructorOptions } from "electron";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  hintsFadeMs,
  type KeyHint,
  type OverlayHintsMessage,
  type OverlayPageSettings,
  type OverlayPanelsMessage,
  overlayHeightForHints,
  overlayHeightForPanels,
  overlayToastAreaHeight,
  type ScriptPanel,
  scriptPanelRows,
} from "../shared/overlay";
import {
  chooseOverlayDisplay,
  type DisplayList,
  OverlayController,
  type OverlayHost,
  type OverlayWindow,
  overlayBottomGap,
  overlayBounds,
  overlayForwardMouse,
  overlayMaxHeight,
  overlayPageURL,
  overlaySize,
  overlayTitle,
  overlayTopGap,
  overlayWindowOptions,
  type Rect,
} from "./overlay";
import { baseWebPreferences } from "./security";

const source = (path: string) =>
  readFileSync(new URL(path, import.meta.url), "utf8");

const primary = { id: 1, workArea: { x: 0, y: 0, width: 1920, height: 1040 } };
const secondary = {
  id: 2,
  workArea: { x: 1920, y: 0, width: 2560, height: 1400 },
};

// The methods a window may have here are the OverlayWindow interface's. The
// fake throws on any other, which is what a call to show(), focus(),
// moveTop() or hide() would be.
const allowed = new Set([
  "setAlwaysOnTop",
  "setIgnoreMouseEvents",
  "setBounds",
  "getBounds",
  "showInactive",
  "toast",
  "prefs",
  "hints",
  "panels",
  "destroy",
  "isDestroyed",
  "load",
  "onReady",
  "onGone",
]);

class FakeWindow {
  calls: string[] = [];
  bounds: Rect;
  destroyed = false;
  ready: Array<() => void> = [];
  gone: Array<() => void> = [];
  loadResult: Promise<void> = Promise.resolve();
  // A window whose load() throws instead of returning a promise.
  loadThrows = false;
  // A window that does not take the size it is given the first time.
  stubborn = false;
  // A page that is gone: sending it a toast or its settings throws.
  toastFails = false;
  settingsFail = false;
  toasts: string[] = [];
  // How long each toast holds, when it said (SCR-22): undefined for the others.
  toastHolds: Array<number | undefined> = [];
  // The settings sent to the page, in order (HUD-05).
  settings: OverlayPageSettings[] = [];
  // The key hints messages sent to the page, in order (HUD-03).
  hintMessages: OverlayHintsMessage[] = [];
  hintsFail = false;
  // The script panels messages sent to the page, in order (SCR-22).
  panelMessages: OverlayPanelsMessage[] = [];
  panelsFail = false;
  private sets = 0;

  constructor(readonly options: BrowserWindowConstructorOptions) {
    this.bounds = {
      x: options.x ?? 0,
      y: options.y ?? 0,
      width: options.width ?? 0,
      height: options.height ?? 0,
    };
  }

  view(): OverlayWindow {
    const impl = {
      setAlwaysOnTop: (flag: boolean, level: string) =>
        this.calls.push(`setAlwaysOnTop(${flag},${level})`),
      setIgnoreMouseEvents: (ignore: boolean, options: { forward: boolean }) =>
        this.calls.push(
          `setIgnoreMouseEvents(${ignore},forward=${options.forward})`,
        ),
      setBounds: (bounds: Rect) => {
        this.calls.push("setBounds");
        this.sets++;
        this.bounds =
          this.stubborn && this.sets === 1
            ? { ...bounds, width: bounds.width + 1 }
            : { ...bounds };
      },
      getBounds: () => ({ ...this.bounds }),
      showInactive: () => this.calls.push("showInactive"),
      toast: (text: string, ms?: number) => {
        this.calls.push("toast");
        if (this.toastFails) throw new Error("the page is gone");
        this.toasts.push(text);
        this.toastHolds.push(ms);
      },
      prefs: (settings: OverlayPageSettings) => {
        this.calls.push("prefs");
        if (this.settingsFail) throw new Error("the page is gone");
        this.settings.push(settings);
      },
      hints: (message: OverlayHintsMessage) => {
        this.calls.push("hints");
        if (this.hintsFail) throw new Error("the page is gone");
        this.hintMessages.push(message);
      },
      panels: (message: OverlayPanelsMessage) => {
        this.calls.push("panels");
        if (this.panelsFail) throw new Error("the page is gone");
        this.panelMessages.push(message);
      },
      destroy: () => {
        this.calls.push("destroy");
        this.destroyed = true;
      },
      isDestroyed: () => this.destroyed,
      load: () => {
        this.calls.push("load");
        if (this.loadThrows) throw new Error("cannot load");
        return this.loadResult;
      },
      onReady: (listener: () => void) => this.ready.push(listener),
      onGone: (listener: () => void) => this.gone.push(listener),
    };
    return new Proxy(impl, {
      get(target, name) {
        if (typeof name === "string" && !allowed.has(name))
          throw new Error(`the overlay window must not use ${name}`);
        return target[name as keyof typeof target];
      },
    }) as unknown as OverlayWindow;
  }

  becomeReady(): void {
    for (const listener of this.ready) listener();
  }
  crash(): void {
    for (const listener of this.gone) listener();
  }
}

const preload = "C:\\padmate\\resources\\app\\out\\preload\\overlay.js";

class FakeHost implements OverlayHost {
  preload = preload;
  windows: FakeWindow[] = [];
  list: DisplayList = { primaryId: 1, displays: [primary] };
  listeners: Array<() => void> = [];
  failCreate = false;
  // The windows made next: their page does not load (the promise rejects), or load() throws.
  failLoad = false;
  loadThrows = false;
  constructor(public secondaryDisplay = false) {}

  create(options: BrowserWindowConstructorOptions): OverlayWindow {
    if (this.failCreate) throw new Error("cannot make a window");
    const win = new FakeWindow(options);
    win.loadThrows = this.loadThrows;
    if (this.failLoad) win.loadResult = Promise.reject(new Error("ERR_FAILED"));
    this.windows.push(win);
    return win.view();
  }
  displays(): DisplayList {
    return this.list;
  }
  onDisplaysChanged(listener: () => void): void {
    this.listeners.push(listener);
  }
  displaysChanged(): void {
    for (const listener of this.listeners) listener();
  }
  get last(): FakeWindow {
    return this.windows[this.windows.length - 1];
  }
}

describe("where the overlay is", () => {
  it("is 480x160 at the bottom centre of the work area, 48 above its bottom edge", () => {
    expect(overlaySize).toEqual({ width: 480, height: 160 });
    expect(overlayBottomGap).toBe(48);
    expect(overlayBounds(primary.workArea)).toEqual({
      x: 720,
      y: 1040 - 160 - 48,
      width: 480,
      height: 160,
    });
    // A work area that does not start at the origin: a taskbar on the left,
    // or a display to the right of the primary one.
    expect(overlayBounds({ x: 48, y: 0, width: 1872, height: 1080 })).toEqual({
      x: 48 + 696,
      y: 1080 - 208,
      width: 480,
      height: 160,
    });
    expect(overlayBounds(secondary.workArea)).toEqual({
      x: 1920 + 1040,
      y: 1400 - 208,
      width: 480,
      height: 160,
    });
  });

  it("is 48 below the top of the work area, centred, for the top position (HUD-05)", () => {
    expect(overlayTopGap).toBe(48);
    expect(overlayBounds(primary.workArea, "top")).toEqual({
      x: 720,
      y: 48,
      width: 480,
      height: 160,
    });
    // The work area need not start at the origin (a taskbar on top, or a
    // display to the right).
    expect(
      overlayBounds({ x: 0, y: 40, width: 1920, height: 1040 }, "top"),
    ).toEqual({ x: 720, y: 40 + 48, width: 480, height: 160 });
    expect(overlayBounds(secondary.workArea, "top")).toEqual({
      x: 1920 + 1040,
      y: 48,
      width: 480,
      height: 160,
    });
    // Bottom is the default and unchanged.
    expect(overlayBounds(primary.workArea, "bottom")).toEqual(
      overlayBounds(primary.workArea),
    );
    expect(overlayBounds(primary.workArea).y).toBe(1040 - 160 - 48);
  });

  it("keeps the top position inside a work area with little room", () => {
    // Room for the window and part of the gap.
    expect(
      overlayBounds({ x: 0, y: 10, width: 800, height: 180 }, "top"),
    ).toEqual({ x: 160, y: 10 + 20, width: 480, height: 160 });
    // No room for the gap: the window fills the area's height.
    expect(
      overlayBounds({ x: 0, y: 10, width: 800, height: 160 }, "top"),
    ).toEqual({ x: 160, y: 10, width: 480, height: 160 });
    // Smaller than the window: it shrinks to the area.
    expect(
      overlayBounds({ x: 100, y: 50, width: 400, height: 100 }, "top"),
    ).toEqual({ x: 100, y: 50, width: 400, height: 100 });
  });

  it("is on the E2E run's display for either position", () => {
    const both: DisplayList = {
      primaryId: 1,
      displays: [primary, secondary],
    };
    const area = chooseOverlayDisplay(both, true)?.workArea;
    expect(area).toEqual(secondary.workArea);
    for (const position of ["top", "bottom"] as const) {
      const bounds = overlayBounds(area as Rect, position);
      expect(bounds.x).toBeGreaterThanOrEqual(secondary.workArea.x);
      expect(bounds.x + bounds.width).toBeLessThanOrEqual(
        secondary.workArea.x + secondary.workArea.width,
      );
      expect(bounds.y).toBeGreaterThanOrEqual(secondary.workArea.y);
      expect(bounds.y + bounds.height).toBeLessThanOrEqual(
        secondary.workArea.y + secondary.workArea.height,
      );
    }
  });

  it("is in whole DIP and never covers the display", () => {
    const odd = overlayBounds({ x: 0, y: 0, width: 1367, height: 727 });
    expect(Number.isInteger(odd.x) && Number.isInteger(odd.y)).toBe(true);
    for (const area of [primary.workArea, secondary.workArea]) {
      const bounds = overlayBounds(area);
      expect(bounds.width).toBeLessThan(area.width / 2);
      expect(bounds.height).toBeLessThan(area.height / 4);
    }
  });

  it("stays inside a work area smaller than itself", () => {
    expect(overlayBounds({ x: 100, y: 50, width: 400, height: 100 })).toEqual({
      x: 100,
      y: 50,
      width: 400,
      height: 100,
    });
    // Room for the window but not for the gap: it sits at the top.
    expect(overlayBounds({ x: 0, y: 30, width: 800, height: 180 })).toEqual({
      x: 160,
      y: 30,
      width: 480,
      height: 160,
    });
  });

  it("goes on the primary display, or on the secondary one in an E2E run", () => {
    const both: DisplayList = {
      primaryId: 1,
      displays: [primary, secondary],
    };
    expect(chooseOverlayDisplay(both, false)?.id).toBe(1);
    expect(chooseOverlayDisplay(both, true)?.id).toBe(2);
    // The list's order does not make a display the primary one.
    expect(
      chooseOverlayDisplay(
        { primaryId: 2, displays: [primary, secondary] },
        false,
      )?.id,
    ).toBe(2);
    expect(
      chooseOverlayDisplay(
        { primaryId: 2, displays: [primary, secondary] },
        true,
      )?.id,
    ).toBe(1);
    // One display: there is no secondary one, as in helpers.placeWindow.
    expect(
      chooseOverlayDisplay({ primaryId: 1, displays: [primary] }, true)?.id,
    ).toBe(1);
    // The primary display missing from the list falls back to the first.
    expect(
      chooseOverlayDisplay({ primaryId: 9, displays: [secondary] }, false)?.id,
    ).toBe(2);
    expect(chooseOverlayDisplay({ primaryId: 1, displays: [] }, false)).toBe(
      undefined,
    );
  });

  it("loads the page next to the main page", () => {
    expect(overlayPageURL(false, undefined)).toBe("padmate://app/overlay.html");
    expect(overlayPageURL(false, "http://127.0.0.1:5173/")).toBe(
      "padmate://app/overlay.html",
    );
    expect(overlayPageURL(true, "http://127.0.0.1:5173/")).toBe(
      "http://127.0.0.1:5173/overlay.html",
    );
    expect(overlayPageURL(true, "http://127.0.0.1:5173/index.html")).toBe(
      "http://127.0.0.1:5173/overlay.html",
    );
  });
});

describe("the overlay window's options", () => {
  const bounds = overlayBounds(primary.workArea);
  const options = overlayWindowOptions(bounds, preload);

  it("is transparent, frameless, not focusable and out of the taskbar", () => {
    expect(options).toMatchObject({
      ...bounds,
      show: false,
      transparent: true,
      backgroundColor: "#00000000",
      frame: false,
      focusable: false,
      skipTaskbar: true,
      type: "toolbar",
      resizable: false,
      movable: false,
      minimizable: false,
      maximizable: false,
      fullscreenable: false,
      hasShadow: false,
      alwaysOnTop: true,
    });
  });

  it("has the main window's security settings, and its own preload as the one addition", () => {
    expect(options.webPreferences).toEqual({ preload, ...baseWebPreferences });
    // The overlay's preload, not the main window's: the page gets a
    // listener for toasts and nothing else (HUD-04).
    expect(options.webPreferences?.preload).toMatch(/overlay\.js$/);
    expect(options.webPreferences).toMatchObject({
      contextIsolation: true,
      sandbox: true,
      nodeIntegration: false,
      webSecurity: true,
      allowRunningInsecureContent: false,
    });
    // A window that is not the main window's child, so closing one does not
    // close the other.
    expect(options.parent).toBeUndefined();
    expect(options.modal).toBeUndefined();
  });

  it("copies the shared settings, so a window cannot change them for the next", () => {
    const first = overlayWindowOptions(bounds, preload);
    const preferences = first.webPreferences;
    if (preferences) preferences.sandbox = false;
    expect(overlayWindowOptions(bounds, preload).webPreferences?.sandbox).toBe(
      true,
    );
    expect(baseWebPreferences.sandbox).toBe(true);
  });
});

describe("making the overlay", () => {
  it("makes one window with the options and styles, and shows it once, inactive", () => {
    const host = new FakeHost();
    const overlay = new OverlayController(host);
    overlay.start();
    expect(host.windows).toHaveLength(1);
    const win = host.last;
    expect(win.options).toEqual(
      overlayWindowOptions(overlayBounds(primary.workArea), preload),
    );
    // Styles first, the page loads, and nothing is shown until it is drawn.
    expect(win.calls).toEqual([
      "setAlwaysOnTop(true,screen-saver)",
      `setIgnoreMouseEvents(true,forward=${overlayForwardMouse})`,
      "load",
    ]);
    win.becomeReady();
    expect(win.calls.filter((call) => call === "showInactive")).toHaveLength(1);
    expect(win.calls.at(-1)).toBe("showInactive");
    expect(overlay.active).toBe(true);
    // The styles are set again before the one show.
    expect(
      win.calls.filter((call) => call.startsWith("setAlwaysOnTop")),
    ).toHaveLength(2);
    expect(win.destroyed).toBe(false);
  });

  it("makes it only once, however often start is called", () => {
    const host = new FakeHost();
    const overlay = new OverlayController(host);
    overlay.start();
    overlay.start();
    overlay.start();
    expect(host.windows).toHaveLength(1);
    expect(host.listeners).toHaveLength(1);
  });

  it("puts it on the secondary display in an E2E run", () => {
    const host = new FakeHost(true);
    host.list = { primaryId: 1, displays: [primary, secondary] };
    new OverlayController(host).start();
    expect(host.last.options).toMatchObject(overlayBounds(secondary.workArea));
    expect(host.last.options.x).toBeGreaterThanOrEqual(1920);
  });

  it("makes no window when there is no display, and survives a window that cannot be made", () => {
    const none = new FakeHost();
    none.list = { primaryId: 1, displays: [] };
    const overlay = new OverlayController(none);
    expect(() => overlay.start()).not.toThrow();
    expect(none.windows).toHaveLength(0);
    expect(overlay.active).toBe(false);

    const failing = new FakeHost();
    failing.failCreate = true;
    const broken = new OverlayController(failing);
    expect(() => broken.start()).not.toThrow();
    expect(broken.active).toBe(false);
    expect(() => failing.displaysChanged()).not.toThrow();
  });

  it("is not troubled by a page that does not load: the window goes and no other is made", async () => {
    const host = new FakeHost();
    host.failLoad = true;
    const overlay = new OverlayController(host);
    expect(() => overlay.start()).not.toThrow();
    await new Promise((done) => setTimeout(done, 10));
    // A page that never comes is no overlay (SCR-22: a script is told so).
    expect(overlay.active).toBe(false);
    expect(host.last.destroyed).toBe(true);
    expect(host.windows).toHaveLength(1);
    expect(() => host.displaysChanged()).not.toThrow();
  });
});

describe("the overlay never takes the foreground", () => {
  it("calls only what the window interface has, and shows with showInactive alone", () => {
    const host = new FakeHost(true);
    host.list = { primaryId: 1, displays: [primary, secondary] };
    const overlay = new OverlayController(host);
    overlay.start();
    host.last.becomeReady();
    // Display changes, a crash and a stop: the Proxy throws on show(),
    // focus(), moveTop(), hide() or anything else.
    host.list = { primaryId: 1, displays: [primary] };
    host.displaysChanged();
    host.last.crash();
    host.last.becomeReady();
    host.displaysChanged();
    overlay.stop();
    const shows = host.windows.flatMap((win) =>
      win.calls.filter((call) => /^show|focus|moveTop|hide/.test(call)),
    );
    expect(shows).toEqual(["showInactive", "showInactive"]);
  });

  it("has no call that could show, focus, raise or hide it in its source", () => {
    const found =
      /\.(show|focus|moveTop|hide|blur|minimize|restore|maximize|setFocusable|setSkipTaskbar|setFullScreen)\(/;
    for (const file of ["./overlay.ts", "./overlay-window.ts"])
      expect(found.exec(source(file)), file).toBeNull();
    // The one call that shows it.
    expect(source("./overlay-window.ts")).toContain("win.showInactive()");
    expect(source("./overlay.ts").match(/\.showInactive\(\)/g)).toHaveLength(1);
  });

  it("talks to its page only by sending a toast, its settings, key hints and a script's panels, and listens to nothing", () => {
    const window = source("./overlay-window.ts");
    // The only messages that go to the page; nothing comes back.
    expect(window.match(/\.send\(/g)).toHaveLength(4);
    expect(window).toContain("win.webContents.send(overlayToastChannel,");
    expect(window).toContain("win.webContents.send(overlayPrefsChannel,");
    expect(window).toContain("win.webContents.send(overlayHintsChannel,");
    expect(window).toContain("win.webContents.send(overlayPanelsChannel,");
    for (const file of [window, source("./overlay.ts")])
      expect(file).not.toMatch(/ipcMain|ipcRenderer|contextBridge|\.handle\(/);
  });
});

describe("a toast (HUD-04)", () => {
  function shown(): { host: FakeHost; overlay: OverlayController } {
    const host = new FakeHost();
    const overlay = new OverlayController(host);
    overlay.start();
    host.last.becomeReady();
    host.last.calls.length = 0;
    return { host, overlay };
  }

  it("goes to the page of the window, and does nothing else to the window", () => {
    const { host, overlay } = shown();
    overlay.toast("配置：默认");
    overlay.toast("已暂停");
    expect(host.last.toasts).toEqual(["配置：默认", "已暂停"]);
    // Not a show, a focus, a raise or a move: the window is not touched.
    expect(host.last.calls).toEqual(["toast", "toast"]);
  });

  it("says how long it holds when it is a script's toast, and nothing when it is not (SCR-22)", () => {
    const { host, overlay } = shown();
    overlay.toast("已暂停");
    overlay.toast("脚本提示", 3000);
    overlay.toast("再来一个", 500);
    expect(host.last.toasts).toEqual(["已暂停", "脚本提示", "再来一个"]);
    expect(host.last.toastHolds).toEqual([undefined, 3000, 500]);
    // Still only the page is told.
    expect(host.last.calls).toEqual(["toast", "toast", "toast"]);
  });

  it("goes to the new window after the renderer died and the window was made again", () => {
    const { host, overlay } = shown();
    const first = host.last;
    first.crash();
    host.last.becomeReady();
    overlay.toast("层：绘画");
    expect(first.toasts).toEqual([]);
    expect(host.last.toasts).toEqual(["层：绘画"]);
  });

  it("goes nowhere without a window: none made, stopped, or destroyed for good", () => {
    const none = new FakeHost();
    none.list = { primaryId: 1, displays: [] };
    const absent = new OverlayController(none);
    absent.start();
    expect(() => absent.toast("已暂停")).not.toThrow();

    const { host, overlay } = shown();
    const win = host.last;
    overlay.stop();
    overlay.toast("已恢复");
    expect(win.toasts).toEqual([]);

    // Not made a third time after two crashes: still nothing to send to.
    const crashed = shown();
    crashed.host.last.crash();
    crashed.host.last.becomeReady();
    crashed.host.last.crash();
    expect(() => crashed.overlay.toast("已暂停")).not.toThrow();
    expect(crashed.host.windows.flatMap((w) => w.toasts)).toEqual([]);
  });

  it("is lost without a word when the page is gone", () => {
    const { host, overlay } = shown();
    host.last.toastFails = true;
    expect(() => overlay.toast("已暂停")).not.toThrow();
    expect(host.last.toasts).toEqual([]);
    expect(overlay.active).toBe(true);
  });
});

describe("the overlay follows the UI's settings (HUD-05)", () => {
  function shown(
    settings: ConstructorParameters<typeof OverlayController>[1] = {},
    secondaryDisplay = false,
  ): { host: FakeHost; overlay: OverlayController } {
    const host = new FakeHost(secondaryDisplay);
    host.list = { primaryId: 1, displays: [primary, secondary] };
    const overlay = new OverlayController(host, settings);
    overlay.start();
    host.last.becomeReady();
    host.last.calls.length = 0;
    host.last.settings.length = 0;
    return { host, overlay };
  }

  it("is made at the bottom with full opacity when the UI has said nothing", () => {
    const host = new FakeHost();
    new OverlayController(host).start();
    expect(host.last.options).toMatchObject(overlayBounds(primary.workArea));
    host.last.becomeReady();
    // The page is told the opacity once, before the window is shown.
    expect(host.last.settings).toEqual([{ opacity: 100 }]);
    expect(host.last.calls.at(-1)).toBe("showInactive");
    expect(host.last.calls.indexOf("prefs")).toBeLessThan(
      host.last.calls.indexOf("showInactive"),
    );
  });

  it("is made where the UI had already put it, and tells its page the opacity when it is ready", () => {
    const host = new FakeHost(true);
    host.list = { primaryId: 1, displays: [primary, secondary] };
    new OverlayController(host, { position: "top", opacity: 65 }).start();
    expect(host.last.options).toMatchObject(
      overlayBounds(secondary.workArea, "top"),
    );
    expect(host.last.settings).toEqual([]);
    host.last.becomeReady();
    expect(host.last.settings).toEqual([{ opacity: 65 }]);
    expect(host.last.bounds).toEqual(overlayBounds(secondary.workArea, "top"));
  });

  it("moves the window to the top and back with setBounds alone: no show, focus or raise", () => {
    const { host, overlay } = shown();
    const win = host.last;
    overlay.configure({ position: "top" });
    expect(win.calls).toEqual(["setBounds"]);
    expect(win.bounds).toEqual(overlayBounds(primary.workArea, "top"));
    overlay.configure({ position: "bottom" });
    expect(win.calls).toEqual(["setBounds", "setBounds"]);
    expect(win.bounds).toEqual(overlayBounds(primary.workArea, "bottom"));
    // The same position again, or none: nothing.
    overlay.configure({ position: "bottom" });
    overlay.configure({});
    expect(win.calls).toEqual(["setBounds", "setBounds"]);
    expect(overlay.active).toBe(true);
    expect(win.destroyed).toBe(false);
  });

  it("stays on its display when it moves: the E2E run's secondary one", () => {
    const { host, overlay } = shown({}, true);
    expect(host.last.bounds).toEqual(overlayBounds(secondary.workArea));
    overlay.configure({ position: "top" });
    expect(host.last.bounds).toEqual(overlayBounds(secondary.workArea, "top"));
    expect(host.last.bounds.y).toBe(secondary.workArea.y + 48);
    expect(host.last.bounds.x).toBeGreaterThanOrEqual(1920);
  });

  it("keeps the position over display changes and a crash, and the opacity over a crash", () => {
    const { host, overlay } = shown();
    overlay.configure({ position: "top" });
    const shorter = { x: 0, y: 0, width: 1920, height: 960 };
    host.list = { primaryId: 1, displays: [{ id: 1, workArea: shorter }] };
    host.displaysChanged();
    expect(host.last.bounds).toEqual(overlayBounds(shorter, "top"));
    // The window made again after a crash is at the top and is told the
    // opacity the UI chose.
    overlay.configure({ opacity: 70 });
    const first = host.last;
    first.crash();
    const second = host.last;
    expect(second).not.toBe(first);
    expect(second.options).toMatchObject(overlayBounds(shorter, "top"));
    second.becomeReady();
    expect(second.settings).toEqual([{ opacity: 70 }]);
  });

  it("sends the page a new opacity, and only when it changed", () => {
    const { host, overlay } = shown();
    const win = host.last;
    overlay.configure({ opacity: 60 });
    expect(win.settings).toEqual([{ opacity: 60 }]);
    // Not the window: only its page.
    expect(win.calls).toEqual(["prefs"]);
    overlay.configure({ opacity: 60 });
    overlay.configure({ position: "bottom" });
    overlay.configure({});
    expect(win.settings).toEqual([{ opacity: 60 }]);
    overlay.configure({ opacity: 100, position: "top" });
    expect(win.settings).toEqual([{ opacity: 60 }, { opacity: 100 }]);
    expect(win.calls).toEqual(["prefs", "setBounds", "prefs"]);
  });

  it("takes settings before there is a window and after it is gone, and survives a page that is gone", () => {
    const none = new FakeHost();
    none.list = { primaryId: 1, displays: [] };
    const absent = new OverlayController(none);
    expect(() =>
      absent.configure({ position: "top", opacity: 50 }),
    ).not.toThrow();
    absent.start();
    expect(none.windows).toHaveLength(0);

    const stopped = shown();
    const win = stopped.host.last;
    stopped.overlay.stop();
    win.calls.length = 0;
    expect(() =>
      stopped.overlay.configure({ position: "top", opacity: 50 }),
    ).not.toThrow();
    expect(win.calls).toEqual([]);

    const broken = shown();
    broken.host.last.settingsFail = true;
    expect(() => broken.overlay.configure({ opacity: 55 })).not.toThrow();
    expect(broken.overlay.active).toBe(true);
  });
});

describe("the window's height with key hints (HUD-03)", () => {
  const area = { x: 0, y: 0, width: 1920, height: 1040 };

  it("is the toast area's height without hints, which is the default size", () => {
    expect(overlaySize.height).toBe(overlayToastAreaHeight);
    expect(overlayBounds(area, "bottom", 160)).toEqual(overlayBounds(area));
    expect(overlayBounds(area, "top", 160)).toEqual(overlayBounds(area, "top"));
  });

  it("keeps the bottom edge fixed at the bottom: a taller window grows upward", () => {
    const flat = overlayBounds(area, "bottom");
    const tall = overlayBounds(area, "bottom", 300);
    expect(tall.height).toBe(300);
    expect(tall.y + tall.height).toBe(flat.y + flat.height);
    expect(tall.x).toBe(flat.x);
    expect(tall.width).toBe(flat.width);
    expect(area.y + area.height - (tall.y + tall.height)).toBe(
      overlayBottomGap,
    );
  });

  it("keeps the top edge fixed at the top: a taller window grows downward", () => {
    const flat = overlayBounds(area, "top");
    const tall = overlayBounds(area, "top", 300);
    expect(tall.height).toBe(300);
    expect(tall.y).toBe(flat.y);
    expect(tall.y - area.y).toBe(overlayTopGap);
  });

  it("is at most 494 tall (a script's panels of sixteen rows and the '…' line), and never taller than the work area or shorter than the toast area", () => {
    expect(overlayMaxHeight).toBe(494);
    expect(overlayBounds(area, "bottom", 5000).height).toBe(494);
    expect(overlayBounds(area, "bottom", 10).height).toBe(160);
    const small = { x: 0, y: 0, width: 1920, height: 200 };
    expect(overlayBounds(small, "bottom", 360)).toEqual({
      x: 720,
      y: 0,
      width: 480,
      height: 200,
    });
    expect(overlayBounds(small, "top", 360).y).toBe(0);
    // A work area that is shorter than the tallest window: the area's height.
    const short = { x: 0, y: 0, width: 1920, height: 400 };
    expect(overlayBounds(short, "bottom", 5000).height).toBe(400);
  });

  it("is what the panel needs: 160 plus the panel and its gap, 350 at most with 16 rows and the '…' line", () => {
    expect(overlayHeightForHints(0)).toBe(160);
    expect(overlayHeightForHints(1)).toBe(160 + 18 + 20 + 8);
    expect(overlayHeightForHints(16)).toBe(160 + 8 * 18 + 20 + 8);
    expect(overlayHeightForHints(17)).toBe(160 + 9 * 18 + 20 + 8);
    expect(overlayHeightForHints(500)).toBeLessThanOrEqual(overlayMaxHeight);
    for (let count = 0; count <= 70; count++)
      expect(overlayHeightForHints(count)).toBeLessThanOrEqual(
        overlayMaxHeight,
      );
  });
});

describe("key hints (HUD-03)", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  const list = (count: number): KeyHint[] =>
    Array.from({ length: count }, (_, index) => ({
      control: `misc${index + 1}`,
      label: `动作 ${index + 1}`,
    }));
  const message = (count: number, language: "zh-CN" | "en" = "zh-CN") => ({
    entries: list(count),
    language,
  });

  function shown(
    settings: ConstructorParameters<typeof OverlayController>[1] = {},
  ): { host: FakeHost; overlay: OverlayController } {
    vi.useFakeTimers();
    const host = new FakeHost();
    const overlay = new OverlayController(host, settings);
    overlay.start();
    host.last.becomeReady();
    host.last.calls.length = 0;
    host.last.hintMessages.length = 0;
    return { host, overlay };
  }
  const flat = () => overlayBounds(primary.workArea);

  it("grows the window upward first, then sends the page the entries with where the window sits", () => {
    const { host, overlay } = shown();
    const win = host.last;
    overlay.hints(message(6));
    expect(win.calls).toEqual(["setBounds", "hints"]);
    expect(win.bounds).toEqual(
      overlayBounds(primary.workArea, "bottom", overlayHeightForHints(6)),
    );
    expect(win.bounds.y + win.bounds.height).toBe(flat().y + flat().height);
    expect(win.hintMessages).toEqual([{ ...message(6), position: "bottom" }]);
  });

  it("grows downward at the top and tells the page it is at the top", () => {
    const { host, overlay } = shown({ position: "top" });
    const win = host.last;
    overlay.hints(message(16));
    expect(win.bounds.y).toBe(overlayBounds(primary.workArea, "top").y);
    expect(win.bounds.height).toBe(overlayHeightForHints(16));
    expect(win.hintMessages[0].position).toBe("top");
  });

  it("grows only as far as the panel needs, and no further than the tallest", () => {
    const { host, overlay } = shown();
    overlay.hints(message(2));
    expect(host.last.bounds.height).toBe(overlayHeightForHints(2));
    overlay.hints(message(16));
    expect(host.last.bounds.height).toBe(overlayHeightForHints(16));
    overlay.hints(message(60));
    expect(host.last.bounds.height).toBe(overlayHeightForHints(60));
    expect(host.last.bounds.height).toBeLessThanOrEqual(overlayMaxHeight);
    // The hints need 350 at most, as they did.
    expect(host.last.bounds.height).toBe(350);
  });

  it("sends a new list to the page without moving a window that is tall enough", () => {
    const { host, overlay } = shown();
    const win = host.last;
    overlay.hints(message(6));
    win.calls.length = 0;
    overlay.hints({ ...message(6), language: "en" });
    expect(win.calls).toEqual(["hints"]);
  });

  it("hides with an empty list at once, and shrinks back after the panel has faded out", () => {
    const { host, overlay } = shown();
    const win = host.last;
    overlay.hints(message(10));
    win.calls.length = 0;
    overlay.hints({ entries: [], language: "zh-CN" });
    expect(win.hintMessages.at(-1)).toEqual({
      entries: [],
      language: "zh-CN",
      position: "bottom",
    });
    // Still tall while the page fades the panel out.
    expect(win.calls).toEqual(["hints"]);
    expect(win.bounds.height).toBe(overlayHeightForHints(10));
    vi.advanceTimersByTime(hintsFadeMs - 1);
    expect(win.calls).toEqual(["hints"]);
    vi.advanceTimersByTime(60);
    expect(win.calls).toEqual(["hints", "setBounds"]);
    expect(win.bounds).toEqual(flat());
    expect(vi.getTimerCount()).toBe(0);
  });

  it("stays tall when hints come back before the shrink", () => {
    const { host, overlay } = shown();
    const win = host.last;
    overlay.hints(message(10));
    overlay.hints({ entries: [], language: "zh-CN" });
    vi.advanceTimersByTime(50);
    overlay.hints(message(4));
    vi.advanceTimersByTime(1000);
    expect(win.bounds.height).toBe(overlayHeightForHints(4));
    expect(vi.getTimerCount()).toBe(0);
    // And hiding again shrinks as before.
    overlay.hints({ entries: [], language: "zh-CN" });
    vi.advanceTimersByTime(hintsFadeMs + 60);
    expect(win.bounds).toEqual(flat());
  });

  it("does nothing to the window for a hide when nothing was shown", () => {
    const { host, overlay } = shown();
    overlay.hints({ entries: [], language: "zh-CN" });
    expect(host.last.calls).toEqual(["hints"]);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("changes the window only by setBounds: no show, focus, raise or hide", () => {
    const { host, overlay } = shown();
    overlay.hints(message(8));
    overlay.hints({ entries: [], language: "zh-CN" });
    vi.advanceTimersByTime(1000);
    overlay.hints(message(3));
    // The Proxy of the fake throws on any other method; the calls are these.
    expect(new Set(host.last.calls.filter((call) => call !== "hints"))).toEqual(
      new Set(["setBounds"]),
    );
    expect(overlay.active).toBe(true);
    expect(host.last.destroyed).toBe(false);
  });

  it("keeps the window's edge when the displays change while the hints are up", () => {
    const { host, overlay } = shown();
    overlay.hints(message(8));
    const shorter = { x: 0, y: 0, width: 1920, height: 960 };
    host.list = { primaryId: 1, displays: [{ id: 1, workArea: shorter }] };
    host.displaysChanged();
    expect(host.last.bounds).toEqual(
      overlayBounds(shorter, "bottom", overlayHeightForHints(8)),
    );
  });

  it("follows a new position while they are up, keeping the height", () => {
    const { host, overlay } = shown();
    overlay.hints(message(8));
    overlay.configure({ position: "top" });
    expect(host.last.bounds).toEqual(
      overlayBounds(primary.workArea, "top", overlayHeightForHints(8)),
    );
  });

  it("sends the hints still up to a window made again after a crash, tall for them", () => {
    const { host, overlay } = shown();
    overlay.hints(message(8));
    const first = host.last;
    first.crash();
    const second = host.last;
    expect(second).not.toBe(first);
    expect(second.options).toMatchObject(
      overlayBounds(primary.workArea, "bottom", overlayHeightForHints(8)),
    );
    second.becomeReady();
    expect(second.hintMessages).toEqual([
      { ...message(8), position: "bottom" },
    ]);
    // Hints that were hidden before the crash are not sent again.
    overlay.hints({ entries: [], language: "zh-CN" });
    vi.advanceTimersByTime(hintsFadeMs + 60);
    second.crash();
    expect(host.windows).toHaveLength(2);
  });

  it("is made flat when the hints were gone before the crash", () => {
    const { host, overlay } = shown();
    overlay.hints(message(8));
    overlay.hints({ entries: [], language: "zh-CN" });
    vi.advanceTimersByTime(hintsFadeMs + 60);
    host.last.crash();
    expect(host.last.options).toMatchObject(flat());
    host.last.becomeReady();
    expect(host.last.hintMessages).toEqual([]);
  });

  it("goes nowhere without a window, once stopped, or when the page is gone", () => {
    const none = new FakeHost();
    none.list = { primaryId: 1, displays: [] };
    const absent = new OverlayController(none);
    expect(() => absent.hints(message(3))).not.toThrow();
    absent.start();

    const stopped = shown();
    const win = stopped.host.last;
    stopped.overlay.hints(message(8));
    stopped.overlay.hints({ entries: [], language: "zh-CN" });
    stopped.overlay.stop();
    win.calls.length = 0;
    // The pending shrink ended with the window.
    expect(vi.getTimerCount()).toBe(0);
    expect(() => stopped.overlay.hints(message(3))).not.toThrow();
    vi.advanceTimersByTime(1000);
    expect(win.calls).toEqual([]);

    const broken = shown();
    broken.host.last.hintsFail = true;
    expect(() => broken.overlay.hints(message(3))).not.toThrow();
    expect(broken.overlay.active).toBe(true);
  });
});

describe("a script's panels (SCR-22)", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  const panel = (rows: number, title?: string): ScriptPanel => ({
    ...(title ? { title } : {}),
    rows: Array.from({ length: rows }, (_, index): [string, string] => [
      `左 ${index + 1}`,
      `右 ${index + 1}`,
    ]),
  });
  const message = (...panels: ScriptPanel[]) => ({ panels });
  const none = { panels: [] };

  function shown(
    settings: ConstructorParameters<typeof OverlayController>[1] = {},
  ): { host: FakeHost; overlay: OverlayController } {
    vi.useFakeTimers();
    const host = new FakeHost();
    const overlay = new OverlayController(host, settings);
    overlay.start();
    host.last.becomeReady();
    host.last.calls.length = 0;
    host.last.panelMessages.length = 0;
    return { host, overlay };
  }
  const flat = () => overlayBounds(primary.workArea);
  const hintList = (count: number): KeyHint[] =>
    Array.from({ length: count }, (_, index) => ({
      control: `misc${index + 1}`,
      label: `动作 ${index + 1}`,
    }));
  const hints = (count: number) => ({
    entries: hintList(count),
    language: "zh-CN" as const,
  });
  const noHints = { entries: [], language: "zh-CN" as const };

  it("grows the window upward first, then sends the page the panels with where the window sits", () => {
    const { host, overlay } = shown();
    const win = host.last;
    overlay.panels(message(panel(3, "画笔")));
    expect(win.calls).toEqual(["setBounds", "panels"]);
    // A title and three rows: four rows.
    expect(win.bounds).toEqual(
      overlayBounds(primary.workArea, "bottom", overlayHeightForPanels(4)),
    );
    expect(win.bounds.height).toBe(160 + 8 + 4 * 18 + 20);
    expect(win.bounds.y + win.bounds.height).toBe(flat().y + flat().height);
    expect(win.panelMessages).toEqual([
      { ...message(panel(3, "画笔")), position: "bottom" },
    ]);
  });

  it("grows downward at the top and tells the page it is at the top", () => {
    const { host, overlay } = shown({ position: "top" });
    const win = host.last;
    overlay.panels(message(panel(5)));
    expect(win.bounds.y).toBe(overlayBounds(primary.workArea, "top").y);
    expect(win.bounds.height).toBe(overlayHeightForPanels(5));
    expect(win.panelMessages[0].position).toBe("top");
  });

  it("is as tall as the rows the page draws need, sixteen and the '…' line at most, which the window can hold", () => {
    const { host, overlay } = shown();
    overlay.panels(message(panel(2)));
    expect(host.last.bounds.height).toBe(overlayHeightForPanels(2));
    overlay.panels(message(panel(15, "t")));
    expect(host.last.bounds.height).toBe(overlayHeightForPanels(16));
    // One row more than the page shows: the "…" line.
    overlay.panels(message(panel(16, "t")));
    expect(scriptPanelRows(message(panel(16, "t")).panels)).toBe(17);
    expect(host.last.bounds.height).toBe(overlayHeightForPanels(17));
    expect(host.last.bounds.height).toBe(overlayMaxHeight);
    // Four full panels are no taller.
    overlay.panels(message(panel(16, "a"), panel(16, "b"), panel(16, "c")));
    expect(host.last.bounds.height).toBe(overlayMaxHeight);
  });

  it("sends a new list to the page without moving a window that is tall enough", () => {
    const { host, overlay } = shown();
    const win = host.last;
    overlay.panels(message(panel(4)));
    win.calls.length = 0;
    overlay.panels(message({ rows: [["左", "右"], ...panel(3).rows] }));
    expect(win.calls).toEqual(["panels"]);
  });

  it("hides with an empty list at once, and shrinks back after the panels have faded out", () => {
    const { host, overlay } = shown();
    const win = host.last;
    overlay.panels(message(panel(10)));
    win.calls.length = 0;
    overlay.panels(none);
    expect(win.panelMessages.at(-1)).toEqual({
      panels: [],
      position: "bottom",
    });
    // Still tall while the page fades the panels out.
    expect(win.calls).toEqual(["panels"]);
    expect(win.bounds.height).toBe(overlayHeightForPanels(10));
    vi.advanceTimersByTime(hintsFadeMs - 1);
    expect(win.calls).toEqual(["panels"]);
    vi.advanceTimersByTime(60);
    expect(win.calls).toEqual(["panels", "setBounds"]);
    expect(win.bounds).toEqual(flat());
    expect(vi.getTimerCount()).toBe(0);
  });

  it("stays tall when panels come back before the shrink", () => {
    const { host, overlay } = shown();
    const win = host.last;
    overlay.panels(message(panel(10)));
    overlay.panels(none);
    vi.advanceTimersByTime(50);
    overlay.panels(message(panel(4)));
    vi.advanceTimersByTime(1000);
    expect(win.bounds.height).toBe(overlayHeightForPanels(4));
    expect(vi.getTimerCount()).toBe(0);
    overlay.panels(none);
    vi.advanceTimersByTime(hintsFadeMs + 60);
    expect(win.bounds).toEqual(flat());
  });

  it("does nothing to the window for a hide when nothing was shown", () => {
    const { host, overlay } = shown();
    overlay.panels(none);
    expect(host.last.calls).toEqual(["panels"]);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("changes the window only by setBounds: no show, focus, raise or hide", () => {
    const { host, overlay } = shown();
    overlay.panels(message(panel(8)));
    overlay.panels(none);
    vi.advanceTimersByTime(1000);
    overlay.panels(message(panel(3)));
    expect(
      new Set(host.last.calls.filter((call) => call !== "panels")),
    ).toEqual(new Set(["setBounds"]));
    expect(overlay.active).toBe(true);
    expect(host.last.destroyed).toBe(false);
  });

  it("keeps the window's edge when the displays change while they are up", () => {
    const { host, overlay } = shown();
    overlay.panels(message(panel(8)));
    const shorter = { x: 0, y: 0, width: 1920, height: 960 };
    host.list = { primaryId: 1, displays: [{ id: 1, workArea: shorter }] };
    host.displaysChanged();
    expect(host.last.bounds).toEqual(
      overlayBounds(shorter, "bottom", overlayHeightForPanels(8)),
    );
  });

  it("follows a new position while they are up, keeping the height, and tells the page", () => {
    const { host, overlay } = shown();
    overlay.panels(message(panel(8)));
    overlay.configure({ position: "top" });
    expect(host.last.bounds).toEqual(
      overlayBounds(primary.workArea, "top", overlayHeightForPanels(8)),
    );
    expect(host.last.panelMessages.at(-1)?.position).toBe("top");
    // With none up, a new position sends no panels.
    overlay.panels(none);
    host.last.panelMessages.length = 0;
    overlay.configure({ position: "bottom" });
    expect(host.last.panelMessages).toEqual([]);
  });

  it("sends the panels still up to a window made again after a crash, tall for them", () => {
    const { host, overlay } = shown();
    overlay.panels(message(panel(8)));
    const first = host.last;
    first.crash();
    const second = host.last;
    expect(second).not.toBe(first);
    expect(second.options).toMatchObject(
      overlayBounds(primary.workArea, "bottom", overlayHeightForPanels(8)),
    );
    second.becomeReady();
    expect(second.panelMessages).toEqual([
      { ...message(panel(8)), position: "bottom" },
    ]);
    // Panels that were hidden before the crash are not sent again.
    overlay.panels(none);
    vi.advanceTimersByTime(hintsFadeMs + 60);
    second.crash();
    expect(host.windows).toHaveLength(2);
  });

  it("is made flat when the panels were gone before the crash", () => {
    const { host, overlay } = shown();
    overlay.panels(message(panel(8)));
    overlay.panels(none);
    vi.advanceTimersByTime(hintsFadeMs + 60);
    host.last.crash();
    expect(host.last.options).toMatchObject(flat());
    host.last.becomeReady();
    expect(host.last.panelMessages).toEqual([]);
  });

  it("goes nowhere without a window, once stopped, or when the page is gone", () => {
    const missing = new FakeHost();
    missing.list = { primaryId: 1, displays: [] };
    const absent = new OverlayController(missing);
    expect(() => absent.panels(message(panel(3)))).not.toThrow();
    absent.start();

    const stopped = shown();
    const win = stopped.host.last;
    stopped.overlay.panels(message(panel(8)));
    stopped.overlay.panels(none);
    stopped.overlay.stop();
    win.calls.length = 0;
    // The pending shrink ended with the window.
    expect(vi.getTimerCount()).toBe(0);
    expect(() => stopped.overlay.panels(message(panel(3)))).not.toThrow();
    vi.advanceTimersByTime(1000);
    expect(win.calls).toEqual([]);

    const broken = shown();
    broken.host.last.panelsFail = true;
    expect(() => broken.overlay.panels(message(panel(3)))).not.toThrow();
    expect(broken.overlay.active).toBe(true);
  });

  describe("with the key hints", () => {
    it("keeps room for the taller of the two, so that the window does not change size when they trade places", () => {
      const { host, overlay } = shown();
      const win = host.last;
      overlay.panels(message(panel(12)));
      const tall = overlayHeightForPanels(12);
      expect(win.bounds.height).toBe(tall);
      // Hints are shorter: the window stays as tall as the panels need.
      overlay.hints(hints(4));
      expect(overlayHeightForHints(4)).toBeLessThan(tall);
      expect(win.bounds.height).toBe(tall);
      // They end: still tall for the panels, which come back in the page.
      win.calls.length = 0;
      overlay.hints(noHints);
      vi.advanceTimersByTime(1000);
      expect(win.bounds.height).toBe(tall);
      expect(win.calls.filter((call) => call === "setBounds")).toEqual([]);
      // The panels end: flat.
      overlay.panels(none);
      vi.advanceTimersByTime(1000);
      expect(win.bounds).toEqual(flat());
    });

    it("grows for the hints when they are the taller, and shrinks to the panels' height when they end", () => {
      const { host, overlay } = shown();
      const win = host.last;
      overlay.panels(message(panel(2)));
      overlay.hints(hints(16));
      expect(win.bounds.height).toBe(overlayHeightForHints(16));
      overlay.hints(noHints);
      // Tall until the hints have faded out, then as tall as the panels.
      vi.advanceTimersByTime(hintsFadeMs - 1);
      expect(win.bounds.height).toBe(overlayHeightForHints(16));
      vi.advanceTimersByTime(60);
      expect(win.bounds.height).toBe(overlayHeightForPanels(2));
      expect(vi.getTimerCount()).toBe(0);
    });

    it("shrinks to the hints' height when the panels end while the hints are up", () => {
      const { host, overlay } = shown();
      const win = host.last;
      overlay.hints(hints(4));
      overlay.panels(message(panel(12)));
      expect(win.bounds.height).toBe(overlayHeightForPanels(12));
      overlay.panels(none);
      vi.advanceTimersByTime(hintsFadeMs + 60);
      expect(win.bounds.height).toBe(overlayHeightForHints(4));
    });

    it("still shrinks for panels that ended when hints come in the fade, and the other way round", () => {
      const { host, overlay } = shown();
      const win = host.last;
      overlay.panels(message(panel(12)));
      overlay.panels(none);
      vi.advanceTimersByTime(50);
      overlay.hints(hints(4));
      vi.advanceTimersByTime(hintsFadeMs + 60);
      expect(win.bounds.height).toBe(overlayHeightForHints(4));
      expect(vi.getTimerCount()).toBe(0);
      overlay.hints(noHints);
      vi.advanceTimersByTime(50);
      overlay.panels(message(panel(2)));
      vi.advanceTimersByTime(hintsFadeMs + 60);
      expect(win.bounds.height).toBe(overlayHeightForPanels(2));
      expect(vi.getTimerCount()).toBe(0);
    });

    it("sends each its own messages, and both to a window made again", () => {
      const { host, overlay } = shown();
      overlay.panels(message(panel(3)));
      overlay.hints(hints(2));
      expect(host.last.panelMessages).toHaveLength(1);
      expect(host.last.hintMessages).toHaveLength(1);
      host.last.crash();
      host.last.becomeReady();
      expect(host.last.panelMessages).toHaveLength(1);
      expect(host.last.hintMessages).toHaveLength(1);
      expect(host.last.bounds.height).toBe(
        Math.max(overlayHeightForPanels(3), overlayHeightForHints(2)),
      );
    });
  });
});

describe("the overlay giving up (SCR-22)", () => {
  it("tells the one who asked when it cannot make a window: no display, or Electron refuses", () => {
    const none = new FakeHost();
    none.list = { primaryId: 1, displays: [] };
    const told = vi.fn();
    const overlay = new OverlayController(none);
    overlay.onGaveUp(told);
    expect(told).not.toHaveBeenCalled();
    overlay.start();
    expect(told).toHaveBeenCalledTimes(1);

    const refusing = new FakeHost();
    refusing.failCreate = true;
    const refused = vi.fn();
    const other = new OverlayController(refusing);
    other.onGaveUp(refused);
    other.start();
    expect(refused).toHaveBeenCalledTimes(1);
    expect(other.active).toBe(false);
  });

  it("tells it when the renderer dies a second time, and not after the first", () => {
    const host = new FakeHost();
    const told = vi.fn();
    const overlay = new OverlayController(host);
    overlay.onGaveUp(told);
    overlay.start();
    host.last.becomeReady();
    host.last.crash();
    // Made again: the overlay is still coming back.
    expect(overlay.active).toBe(true);
    expect(told).not.toHaveBeenCalled();
    host.last.becomeReady();
    host.last.crash();
    expect(overlay.active).toBe(false);
    expect(told).toHaveBeenCalledTimes(1);
    // Once, however often it is asked after.
    expect(() => host.displaysChanged()).not.toThrow();
    expect(told).toHaveBeenCalledTimes(1);
  });

  it("tells it when the window cannot be made again after the first crash", () => {
    const host = new FakeHost();
    const told = vi.fn();
    const overlay = new OverlayController(host);
    overlay.onGaveUp(told);
    overlay.start();
    host.failCreate = true;
    host.last.crash();
    expect(told).toHaveBeenCalledTimes(1);
    expect(overlay.active).toBe(false);
  });

  it("does not tell it when the app is stopping the overlay", () => {
    const host = new FakeHost();
    const told = vi.fn();
    const overlay = new OverlayController(host);
    overlay.onGaveUp(told);
    overlay.start();
    const win = host.last;
    overlay.stop();
    win.crash();
    expect(told).not.toHaveBeenCalled();
    // Not started either: a stopped controller makes no window, and says nothing.
    const early = new OverlayController(new FakeHost());
    const earlyTold = vi.fn();
    early.onGaveUp(earlyTold);
    early.stop();
    early.start();
    expect(earlyTold).not.toHaveBeenCalled();
  });

  it("is told at once when it asks after the overlay gave up", () => {
    const none = new FakeHost();
    none.list = { primaryId: 1, displays: [] };
    const overlay = new OverlayController(none);
    overlay.start();
    const told = vi.fn();
    overlay.onGaveUp(told);
    expect(told).toHaveBeenCalledTimes(1);
  });

  it("is not made worse off by a listener that throws", () => {
    const host = new FakeHost();
    host.failCreate = true;
    const overlay = new OverlayController(host);
    overlay.onGaveUp(() => {
      throw new Error("not connected");
    });
    expect(() => overlay.start()).not.toThrow();
  });

  it("does not tell a listener when the window is made and works", () => {
    const host = new FakeHost();
    const told = vi.fn();
    const overlay = new OverlayController(host);
    overlay.onGaveUp(told);
    overlay.start();
    host.last.becomeReady();
    expect(told).not.toHaveBeenCalled();
  });

  it("tells it when the page does not load, and destroys the window", async () => {
    const host = new FakeHost();
    host.failLoad = true;
    const told = vi.fn();
    const ready = vi.fn();
    const overlay = new OverlayController(host);
    overlay.onGaveUp(told);
    overlay.onReady(ready);
    overlay.start();
    // The window is there while the page loads.
    expect(told).not.toHaveBeenCalled();
    await new Promise((done) => setTimeout(done, 10));
    expect(told).toHaveBeenCalledTimes(1);
    expect(overlay.active).toBe(false);
    expect(host.last.destroyed).toBe(true);
    // No other window is made, and nothing was ever ready.
    expect(host.windows).toHaveLength(1);
    host.displaysChanged();
    expect(host.windows).toHaveLength(1);
    expect(ready).not.toHaveBeenCalled();
  });

  it("tells it when the page of the window made again does not load either", async () => {
    const host = new FakeHost();
    const told = vi.fn();
    const overlay = new OverlayController(host);
    overlay.onGaveUp(told);
    overlay.start();
    host.last.becomeReady();
    host.failLoad = true;
    host.last.crash();
    expect(overlay.active).toBe(true);
    expect(told).not.toHaveBeenCalled();
    await new Promise((done) => setTimeout(done, 10));
    expect(told).toHaveBeenCalledTimes(1);
    expect(overlay.active).toBe(false);
    expect(host.windows).toHaveLength(2);
    expect(host.last.destroyed).toBe(true);
  });

  it("tells it when load() throws, and destroys the window", () => {
    const host = new FakeHost();
    host.loadThrows = true;
    const told = vi.fn();
    const overlay = new OverlayController(host);
    overlay.onGaveUp(told);
    expect(() => overlay.start()).not.toThrow();
    expect(told).toHaveBeenCalledTimes(1);
    expect(overlay.active).toBe(false);
    expect(host.last.destroyed).toBe(true);
  });

  it("does not tell it of a page that fails to load once the app is stopping the overlay", async () => {
    const host = new FakeHost();
    host.failLoad = true;
    const told = vi.fn();
    const overlay = new OverlayController(host);
    overlay.onGaveUp(told);
    overlay.start();
    overlay.stop();
    await new Promise((done) => setTimeout(done, 10));
    expect(told).not.toHaveBeenCalled();
  });

  it("tells it once when a page that failed to load and a renderer that died both happen", async () => {
    const host = new FakeHost();
    host.failLoad = true;
    const told = vi.fn();
    const overlay = new OverlayController(host);
    overlay.onGaveUp(told);
    overlay.start();
    const win = host.last;
    await new Promise((done) => setTimeout(done, 10));
    // The window was destroyed; a late "gone" of it is nothing new.
    win.crash();
    expect(told).toHaveBeenCalledTimes(1);
    expect(host.windows).toHaveLength(1);
  });
});

describe("the overlay being up (SCR-22)", () => {
  it("tells the one who asked when a window is ready, not before", () => {
    const host = new FakeHost();
    const ready = vi.fn();
    const overlay = new OverlayController(host);
    overlay.onReady(ready);
    overlay.start();
    // Made, and its page is loading: not up yet.
    expect(ready).not.toHaveBeenCalled();
    host.last.becomeReady();
    expect(ready).toHaveBeenCalledTimes(1);
    // The window was shown before it was told.
    expect(host.last.calls.at(-1)).toBe("showInactive");
  });

  it("tells it again for the window made after the renderer died", () => {
    const host = new FakeHost();
    const ready = vi.fn();
    const told = vi.fn();
    const overlay = new OverlayController(host);
    overlay.onReady(ready);
    overlay.onGaveUp(told);
    overlay.start();
    host.last.becomeReady();
    host.last.crash();
    // The gap in between is no news: it is made again.
    expect(ready).toHaveBeenCalledTimes(1);
    expect(told).not.toHaveBeenCalled();
    host.last.becomeReady();
    expect(ready).toHaveBeenCalledTimes(2);
    expect(told).not.toHaveBeenCalled();
  });

  it("is told at once when it asks after a window is ready, and not when that window is gone", () => {
    const host = new FakeHost();
    const overlay = new OverlayController(host);
    overlay.start();
    host.last.becomeReady();
    const late = vi.fn();
    overlay.onReady(late);
    expect(late).toHaveBeenCalledTimes(1);

    const stopped = new FakeHost();
    const other = new OverlayController(stopped);
    other.start();
    stopped.last.becomeReady();
    other.stop();
    const after = vi.fn();
    other.onReady(after);
    expect(after).not.toHaveBeenCalled();
  });

  it("is not told of a window that is not the current one, or that is gone", () => {
    const host = new FakeHost();
    const ready = vi.fn();
    const overlay = new OverlayController(host);
    overlay.onReady(ready);
    overlay.start();
    const first = host.last;
    first.crash();
    // The first window's page draws a frame after it was replaced.
    first.becomeReady();
    expect(ready).not.toHaveBeenCalled();
    host.last.becomeReady();
    expect(ready).toHaveBeenCalledTimes(1);
    overlay.stop();
    host.last.becomeReady();
    expect(ready).toHaveBeenCalledTimes(1);
  });

  it("is not made worse off by a listener that throws", () => {
    const host = new FakeHost();
    const overlay = new OverlayController(host);
    overlay.onReady(() => {
      throw new Error("not connected");
    });
    overlay.start();
    expect(() => host.last.becomeReady()).not.toThrow();
    expect(overlay.active).toBe(true);
  });
});

describe("the overlay follows the displays", () => {
  it("moves to the new work area without showing", () => {
    const host = new FakeHost();
    const overlay = new OverlayController(host);
    overlay.start();
    const win = host.last;
    win.becomeReady();
    win.calls.length = 0;
    // The taskbar grew: the work area got shorter.
    host.list = {
      primaryId: 1,
      displays: [{ id: 1, workArea: { x: 0, y: 0, width: 1920, height: 960 } }],
    };
    host.displaysChanged();
    expect(win.calls).toEqual(["setBounds"]);
    expect(win.bounds).toEqual({
      x: 720,
      y: 960 - 208,
      width: 480,
      height: 160,
    });
    expect(overlay.active).toBe(true);
  });

  it("does nothing when the window is already where it should be", () => {
    const host = new FakeHost();
    new OverlayController(host).start();
    const win = host.last;
    win.calls.length = 0;
    host.displaysChanged();
    host.displaysChanged();
    expect(win.calls).toEqual([]);
  });

  it("sets the bounds again once when the window did not take them", () => {
    const host = new FakeHost();
    new OverlayController(host).start();
    const win = host.last;
    win.stubborn = true;
    win.calls.length = 0;
    host.list = { primaryId: 1, displays: [secondary] };
    host.displaysChanged();
    expect(win.calls).toEqual(["setBounds", "setBounds"]);
    expect(win.bounds).toEqual(overlayBounds(secondary.workArea));
  });

  it("moves back to the primary display when the secondary one is removed", () => {
    const host = new FakeHost(true);
    host.list = { primaryId: 1, displays: [primary, secondary] };
    new OverlayController(host).start();
    const win = host.last;
    expect(win.bounds.x).toBeGreaterThanOrEqual(1920);
    host.list = { primaryId: 1, displays: [primary] };
    host.displaysChanged();
    expect(win.bounds).toEqual(overlayBounds(primary.workArea));
    expect(win.calls).not.toContain("showInactive");
  });

  it("leaves a destroyed window and a stopped controller alone", () => {
    const host = new FakeHost();
    const overlay = new OverlayController(host);
    overlay.start();
    const win = host.last;
    overlay.stop();
    win.calls.length = 0;
    host.list = { primaryId: 1, displays: [secondary] };
    expect(() => host.displaysChanged()).not.toThrow();
    expect(win.calls).toEqual([]);
  });
});

describe("the overlay's life", () => {
  it("is destroyed with the main window and makes no other afterwards", () => {
    const host = new FakeHost();
    const overlay = new OverlayController(host);
    overlay.start();
    const win = host.last;
    win.becomeReady();
    overlay.stop();
    expect(win.destroyed).toBe(true);
    expect(overlay.active).toBe(false);
    // Stopping again, or starting, changes nothing.
    overlay.stop();
    overlay.start();
    expect(host.windows).toHaveLength(1);
    expect(win.calls.filter((call) => call === "destroy")).toHaveLength(1);
  });

  it("does not show a window that is stopped before its page is drawn", () => {
    const host = new FakeHost();
    const overlay = new OverlayController(host);
    overlay.start();
    const win = host.last;
    overlay.stop();
    win.becomeReady();
    expect(win.calls).not.toContain("showInactive");
  });

  it("is made again once after its renderer died, inactive like the first", () => {
    const host = new FakeHost();
    const overlay = new OverlayController(host);
    overlay.start();
    const first = host.last;
    first.becomeReady();
    first.crash();
    expect(first.destroyed).toBe(true);
    expect(host.windows).toHaveLength(2);
    const second = host.last;
    expect(second.options).toEqual(first.options);
    expect(second.calls.slice(0, 3)).toEqual([
      "setAlwaysOnTop(true,screen-saver)",
      `setIgnoreMouseEvents(true,forward=${overlayForwardMouse})`,
      "load",
    ]);
    expect(second.calls).not.toContain("showInactive");
    second.becomeReady();
    expect(second.calls.filter((call) => call === "showInactive")).toHaveLength(
      1,
    );
    expect(overlay.active).toBe(true);
  });

  it("is not made a third time after a second crash", () => {
    const host = new FakeHost();
    const overlay = new OverlayController(host);
    overlay.start();
    host.last.becomeReady();
    host.last.crash();
    host.last.becomeReady();
    host.last.crash();
    expect(host.windows).toHaveLength(2);
    expect(host.windows.every((win) => win.destroyed)).toBe(true);
    expect(overlay.active).toBe(false);
    // The displays can change without a window to move.
    expect(() => host.displaysChanged()).not.toThrow();
  });

  it("is not made again when it dies while the app is quitting", () => {
    const host = new FakeHost();
    const overlay = new OverlayController(host);
    overlay.start();
    const win = host.last;
    overlay.stop();
    win.crash();
    expect(host.windows).toHaveLength(1);
  });

  it("does not take a crash of a window it already replaced for a new one", () => {
    const host = new FakeHost();
    const overlay = new OverlayController(host);
    overlay.start();
    const first = host.last;
    first.crash();
    // The first window reports once more: nothing happens.
    first.crash();
    first.becomeReady();
    expect(host.windows).toHaveLength(2);
    expect(first.calls).not.toContain("showInactive");
    expect(overlay.active).toBe(true);
  });
});

describe("the overlay in the main process", () => {
  const main = source("./index.ts");

  it("is made after the main window is ready, so Playwright sees the main page first", () => {
    expect(main).toMatch(
      /window\.once\("ready-to-show", \(\) => \{\s*pageReady = true;\s*showWhenReady\(\);\s*startOverlay\(\);\s*\}\);/,
    );
    // Nothing else starts it.
    expect(main.match(/startOverlay\(\)/g)).toHaveLength(2);
    expect(main.match(/new OverlayController\(/g)).toHaveLength(1);
  });

  it("ends with the main window, so closing it still quits the app", () => {
    // The overlay would keep window-all-closed from firing: the main
    // window's closed handler stops it and quits itself.
    expect(main).toMatch(
      /window\.on\("closed", \(\) => \{\s*window = undefined;[^}]*overlay\?\.stop\(\);\s*if \(!quitting\) app\.quit\(\);\s*\}\);/,
    );
    expect(main).toMatch(/quitting = true;\s*overlay\?\.stop\(\);/);
    expect(main).toMatch(
      /app\.on\("window-all-closed", \(\) => \{\s*if \(!quitting\) app\.quit\(\);\s*\}\);/,
    );
  });

  it("is not a trusted caller of the UI's IPC", () => {
    expect(main).toContain("event.sender !== window.webContents");
    expect(main).toContain(
      "event.senderFrame !== window.webContents.mainFrame",
    );
    expect(main).toContain('throw new Error("Untrusted UI caller")');
  });

  it("serves its page like the main page, under the same CSP", () => {
    // Any file below the renderer root, with the CSP header.
    expect(main).toContain('protocol.handle("padmate"');
    expect(main).toContain('"Content-Security-Policy": policy');
    const config = source("../../electron.vite.config.ts");
    expect(config).toContain('index: resolve("src/renderer/index.html")');
    expect(config).toContain('overlay: resolve("src/renderer/overlay.html")');
  });

  it("makes the main window with the same shared settings", () => {
    expect(main).toMatch(
      /preload: resolve\(__dirname, "\.\.\/preload\/index\.js"\),\s*\.\.\.baseWebPreferences,/,
    );
  });

  it("gives the overlay its own preload, built as a second entry of its own", () => {
    expect(main).toContain(
      'preload: resolve(__dirname, "../preload/overlay.js"),',
    );
    // Two preloads in all: the main window's and the overlay's.
    expect(main.match(/resolve\(__dirname, "\.\.\/preload\//g)).toHaveLength(2);
    const config = source("../../electron.vite.config.ts");
    expect(config).toContain('index: resolve("src/preload/index.ts")');
    expect(config).toContain('overlay: resolve("src/preload/overlay.ts")');
  });

  it("tells the overlay what changed from the daemon's events, unless the main window is focused", () => {
    expect(main.match(/new OverlayToaster\(/g)).toHaveLength(1);
    expect(main).toContain(
      "mainFocused: () => !!window && !window.isDestroyed() && window.isFocused(),",
    );
    expect(main).toContain("show: (text, ms) => overlay?.toast(text, ms),");
    // The one place that feeds it: every event the daemon publishes.
    expect(main.match(/toasts\.handle\(/g)).toHaveLength(1);
  });

  it("shows a script's panels from the same events, and learns when the overlay is given up (SCR-22)", () => {
    expect(main.match(/new OverlayPanels\(/g)).toHaveLength(1);
    expect(main).toMatch(
      /new OverlayPanels\(\{\s*show: \(message\) => \{\s*if \(!overlay\) return false;\s*overlay\.panels\(message\);/,
    );
    expect(main.match(/panels\.handle\(/g)).toHaveLength(1);
    expect(main.match(/panels\.clear\(/g)).toHaveLength(1);
    expect(main.match(/overlay\.onGaveUp\(/g)).toHaveLength(1);
    expect(main.match(/overlay\.onReady\(/g)).toHaveLength(1);
    // Before the window is made, or a listener could be missed.
    expect(main).toMatch(
      /overlay\.onReady\(\(\) => hud\.overlayUp\(true\)\);\s*overlay\.onGaveUp\(\(\) => hud\.overlayUp\(false\)\);\s*overlay\.start\(\);/,
    );
  });

  it("takes the UI's settings from one handler and makes the overlay with the latest of them (HUD-05)", () => {
    // Pushed by the main window's page, through the handler table that only
    // trusts that page; checked by overlay-prefs.ts, then handed on.
    expect(main).toContain(
      "overlayPrefs: { count: 1, run: (prefs) => overlayPreferences.push(prefs) },",
    );
    expect(main).toMatch(
      /new OverlayPreferences\(\(\) => \[\s*toasts,\s*hints,\s*overlay,?\s*\]\)/,
    );
    expect(main).toMatch(
      /new OverlayController\(\s*electronOverlayHost\(\{[\s\S]*?\}\),\s*overlayPreferences\.value,\s*\)/,
    );
    // The language is no longer read out of the page.
    expect(main).not.toMatch(/executeJavaScript|readLanguage|localStorage/);
  });

  it("puts the overlay on the E2E display only through the runtime options", () => {
    expect(main).toContain(
      'secondaryDisplay: runtimeOptions().e2eDisplay === "secondary"',
    );
    expect(main).not.toContain("PADMATE_E2E_DISPLAY");
  });
});

describe("telling the overlay from the main window in the E2E specs", () => {
  it("has a title of its own, in the options and in its page", () => {
    expect(overlayTitle).toBe("padmate overlay");
    expect(
      overlayWindowOptions(overlayBounds(primary.workArea), preload).title,
    ).toBe(overlayTitle);
    expect(source("../renderer/overlay.html")).toContain(
      `<title>${overlayTitle}</title>`,
    );
    // The main page's titles are "padmate" and "padmate · <page>".
    expect(source("../renderer/index.html")).toContain(
      "<title>padmate</title>",
    );
    expect(source("../renderer/App.tsx")).toContain(
      "document.title = `padmate · ",
    );
  });

  it("is how every spec finds the main window: nothing takes getAllWindows()[0] for it", () => {
    // Electron 44 lists the newest window first (measured), the order is
    // not documented, and the overlay is a second window.
    const dir = new URL("../../tests/e2e/", import.meta.url);
    const files = readdirSync(dir).filter((name) => name.endsWith(".ts"));
    expect(files.length).toBeGreaterThan(10);
    for (const name of files) {
      const text = readFileSync(new URL(name, dir), "utf8");
      expect(text, name).not.toMatch(/getAllWindows\(\)\[\d+\]/);
      expect(text, name).not.toMatch(/getAllWindows\(\)\.at\(/);
    }
  });
});

describe("the overlay's page", () => {
  const page = source("../renderer/overlay.html");

  it("loads one local module and has no inline script or handler", () => {
    expect([...page.matchAll(/<script\b[^>]*>/g)].map((m) => m[0])).toEqual([
      '<script type="module" src="./overlay-main.tsx">',
    ]);
    expect(page).not.toMatch(/<script\b[^>]*>\s*[^<\s]/);
    expect(page).not.toMatch(/\son[a-z]+\s*=/i);
    expect(page).not.toMatch(/https?:\/\//);
    expect(page).not.toMatch(/<iframe|<object|<embed|<link/i);
  });

  it("has the element the tests find, transparent and click-through", () => {
    const css = source("../renderer/overlay.css");
    expect(css).toMatch(/background:\s*transparent/);
    expect(css).toMatch(/pointer-events:\s*none/);
    expect(css).toMatch(/\[data-visible="false"\]\s*\{\s*opacity:\s*0;/);
    expect(css).toMatch(/\[data-visible="true"\]\s*\{\s*opacity:\s*1;/);
    expect(source("../renderer/Overlay.tsx")).toContain(
      'data-testid="overlay-root"',
    );
  });
});
