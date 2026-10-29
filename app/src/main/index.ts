import { execFile, spawn } from "node:child_process";
import { mkdirSync } from "node:fs";
import { readdir, readFile, stat, writeFile } from "node:fs/promises";
import { extname, join, resolve, sep } from "node:path";
import {
  app,
  BrowserWindow,
  dialog,
  ipcMain,
  nativeImage,
  powerMonitor,
  protocol,
  session,
  shell,
} from "electron";
import type { ConnectionState, DaemonEvent, DeviceEntry } from "../shared/api";
import { appIconHandlers } from "./app-icon-handlers";
import {
  appPathHandlers,
  appPathResolver,
  regExe,
  registrySources,
} from "./app-paths";
import { archiveHandlers } from "./archive-handlers";
import { managerDisconnected } from "./bridge";
import { CloseGuard, EXIT_WAIT_MS } from "./close-guard";
import { configRepairHandlers } from "./config-repair-handlers";
import {
  exitAfterSaves,
  holdsWindowClose,
  routeDaemonEvents,
} from "./daemon-exit";
import type { DaemonManager } from "./daemon-manager";
import { deviceHandlers } from "./device-handlers";
import {
  editorLauncher,
  editorRecorder,
  startDetached,
} from "./editor-launcher";
import { EventBuffer } from "./event-buffer";
import { bindEventDelivery } from "./event-delivery";
import { HudSubscription } from "./hud-subscription";
import {
  installedAppHandlers,
  listFixtureApps,
  listInstalledApps,
  programScan,
  windowsDirectory,
} from "./installed-apps";
import { libraryHandlers, uiResult } from "./library-handlers";
import { macroHandlers } from "./macro-handlers";
import { NativeCapture } from "./native-capture";
import { OverlayController, overlayPageURL } from "./overlay";
import { OverlayHints } from "./overlay-hints";
import { OverlayPanels } from "./overlay-panels";
import { OverlayPreferences } from "./overlay-prefs";
import { OverlayToaster } from "./overlay-toasts";
import { electronOverlayHost } from "./overlay-window";
import { PadCapture } from "./pad-capture";
import { RawWatch } from "./raw-watch";
import { createDaemonManager, runtimeOptions } from "./runtime";
import {
  archiveExtension,
  baseWebPreferences,
  contentSecurityPolicy,
  validateConfigWrite,
  validateControl,
  validateDevice,
  validateExport,
  validateProfileID,
  validateToggle,
} from "./security";
import { claimSingleInstance } from "./single-instance";
import {
  failureDialogText,
  pageRecovery,
  startFailureHandlers,
  startFolderTarget,
  uiFailureText,
} from "./start-failure";
import { startupHandlers } from "./startup-handlers";
import { systemSettingsHandlers } from "./system-settings";
import type { UpdateController } from "./update-controller";
import { updateHandlers } from "./update-handlers";
import { createUpdates, removeStaleUpdate } from "./update-main";
import { closeHides, quitsWhileHidden } from "./window-close";

protocol.registerSchemesAsPrivileged([
  {
    scheme: "padmate",
    privileges: { standard: true, secure: true, supportFetchAPI: true },
  },
]);

// Browser state must be separated before Electron creates the default session.
const browserData = runtimeOptions().userData;
mkdirSync(browserData, { recursive: true });
app.setPath("userData", browserData);
// PKG-10: one window per set of browser data. After an update the installer
// starts padmate again; a second instance does nothing but bring the first
// one's window to the front.
const firstInstance = claimSingleInstance(app, showExistingWindow);

let window: BrowserWindow | undefined;
// PKG-10 (ADR-0073): the automatic update, made when the app is ready.
let updates: UpdateController | undefined;
// HUD-01 (ADR-0063): the overlay is a second window, made once after this one
// is ready, never shown actively and destroyed with this one.
let overlay: OverlayController | undefined;
let manager: DaemonManager | undefined;
let quitting = false;
let pageReady = false;
let shown = false;
// UI-45: the page did not load, or its renderer went away while the start
// had failed; then a dialog says why instead of the page.
let pageBroken = false;
let rendererGone = false;
// The page is loaded again after rendererGone; it shows once loaded.
let recovering = false;
let failureDialog = false;
// DMN-22: the daemon's exit is being answered (its tray's 退出, or the
// installer's, PKG-04): the window finishes its saves, tells the daemon and
// quits, and a close in between waits for that.
let trayExiting = false;
// ADR-0077: closing the window hid it, and padmate.exe stays with the page
// and the controller's model loaded until a second start shows it again.
let hiddenByClose = false;
// A quit waits for the saves (before-quit): the close it makes goes through.
let quitHeld = false;
// UI-43: closing waits for the mapping page's saves; nothing asks the user.
const closeGuard = new CloseGuard({
  ask: () => {
    if (!window || window.isDestroyed() || !pageReady) return false;
    window.webContents.send("padmate:closing");
    return true;
  },
  close: () => {
    if (window && !window.isDestroyed()) window.close();
  },
});
const capture = new NativeCapture({
  focused: () =>
    !!window &&
    !window.isDestroyed() &&
    window.isFocused() &&
    manager?.state.phase === "connected",
  spawn: () => {
    const options = runtimeOptions();
    return spawn(
      options.ctl,
      [
        "keyboard-capture",
        "--ipc-dir",
        options.ipcDir,
        "--ui-pid",
        String(process.pid),
      ],
      { windowsHide: true, shell: false, stdio: "pipe", env: options.childEnv },
    );
  },
  notify: (event) => {
    if (window && !window.isDestroyed())
      window.webContents.send("padmate:keyboardCapture", event);
  },
});
const padCapture = new PadCapture({
  call: (method) => manager?.call(method),
  focused: () =>
    !!window &&
    !window.isDestroyed() &&
    window.isFocused() &&
    manager?.state.phase === "connected",
});
const events = new EventBuffer((batch) => {
  if (window && !window.isDestroyed())
    window.webContents.send("padmate:eventBatch", batch);
});
// HUD-04 (ADR-0063): the daemon's state events, as they arrive, tell the
// overlay what changed. Not while the main window is focused: the user is
// looking at the editor then. Whether there are toasts and in which language
// is the UI's choice, pushed by the page (HUD-05, overlayPreferences).
const toasts = new OverlayToaster({
  mainFocused: () => !!window && !window.isDestroyed() && window.isFocused(),
  show: (text, ms) => overlay?.toast(text, ms),
});
// HUD-03 (ADR-0063): the key hints the daemon lists in its state events while
// the hints button is held. They show also while the main window is focused
// and do not depend on the toast switch. What kind of pad the names are for
// comes from the daemon's device list, asked once per device.
const hints = new OverlayHints({
  show: (message) => overlay?.hints(message),
  pad: async (device) => {
    const list = await manager?.call<DeviceEntry[]>("devices.list");
    const info = list?.find((entry) => entry.info.id === device)?.info;
    return info
      ? {
          type: info.type,
          buttonLabels: info.buttonLabels,
          buttonMask: info.buttonMask,
        }
      : undefined;
  },
});
// SCR-22 (ADR-0063): the panels a script keeps with hud.panel, which the daemon
// lists in its state events. They show also while the main window is focused
// and do not depend on the toast switch; the page keeps them back while the
// hints show.
const panels = new OverlayPanels({
  show: (message) => {
    if (!overlay) return false;
    overlay.panels(message);
    return true;
  },
});
// SCR-22: the daemon tells a script that an overlay is there (host.has("hud"))
// while somebody subscribes to its hud topic. The UI does while its overlay is
// up: from the moment its window is ready, until it is given up (the window
// could not be made, its page did not load, or its renderer died twice). Every
// connection subscribes as the overlay is then.
const hud = new HudSubscription((method, params) =>
  manager
    ? manager.call(method, params)
    : Promise.reject(new Error(managerDisconnected)),
);
// IN-17, IN-19 (ADR-0072): the raw joystick events of the controller the
// mapping wizard watches. The input.raw topic is on the same subscription as
// hud, wished up while a watch lasts; the watch ends when the wizard says so,
// and here when its page goes away.
const rawWatch = new RawWatch(
  (method, params) => manager?.call(method, params),
  { up: (on) => hud.rawUp(on) },
);
// HUD-05: what the UI pushed about the overlay (toasts, language, position,
// opacity), validated here; the overlay is made with the latest of it.
const overlayPreferences = new OverlayPreferences(() => [
  toasts,
  hints,
  overlay,
]);
const rendererRoot = resolve(__dirname, "../renderer");
const rendererURL = process.env.ELECTRON_RENDERER_URL;
const developmentServer = !app.isPackaged && !!rendererURL;

/**
 * A close while the daemon is connected (ADR-0077): the window hides, and a
 * field still being typed in is committed, as when it closes (UI-43). The
 * page stays, so nothing waits for the saves.
 */
function hideOnClose(): void {
  if (!window || window.isDestroyed()) return;
  hiddenByClose = true;
  window.webContents.send("padmate:closing");
  window.hide();
}

/** A second padmate was started: the window of this one comes to the front. */
function showExistingWindow(): void {
  if (!window || window.isDestroyed() || !shown) return;
  if (window.isMinimized()) window.restore();
  window.show();
  window.focus();
}

function send(channel: string, value: ConnectionState | DaemonEvent): void {
  if (window && !window.isDestroyed()) window.webContents.send(channel, value);
}

// The window shows once the page is ready and the start is decided: the
// app when connected, why not when it failed (UI-45).
function showWhenReady(): void {
  const phase = manager?.state.phase;
  if (pageReady && !shown && !quitting && phase && phase !== "connecting") {
    shown = true;
    window?.show();
  }
}

/**
 * HUD-01: makes the overlay once the main window is ready, so the main page
 * is the first window Playwright sees (firstWindow). BrowserWindow
 * .getAllWindows() has no order to rely on (Electron 44 lists the newest
 * first), so the E2E specs find the main window by title, not by [0].
 */
function startOverlay(): void {
  if (overlay || quitting) return;
  overlay = new OverlayController(
    electronOverlayHost({
      pageURL: overlayPageURL(developmentServer, rendererURL),
      preload: resolve(__dirname, "../preload/overlay.js"),
      secondaryDisplay: runtimeOptions().e2eDisplay === "secondary",
    }),
    overlayPreferences.value,
  );
  overlay.onReady(() => hud.overlayUp(true));
  overlay.onGaveUp(() => hud.overlayUp(false));
  overlay.start();
}

/** 打开文件夹 (UI-45): never a path of the report's other than its file. */
async function openStartFolder(): Promise<void> {
  const target = startFolderTarget(
    manager?.state.failure,
    runtimeOptions().dataDir,
  );
  if ("show" in target) shell.showItemInFolder(target.show);
  else if (await shell.openPath(target.open))
    throw new Error("Could not open the data folder");
}

/** PRO-29: selects the backup of a repaired config.json in the data folder. */
function showConfigBackup(backup: string): void {
  shell.showItemInFolder(join(runtimeOptions().dataDir, backup));
}

/** The programs of the Start Menu (UI-46), or of a test's own list. */
function listPrograms() {
  const options = runtimeOptions();
  const isFile = async (path: string) => (await stat(path)).isFile();
  // Programs below the Windows directory are system tools, not listed.
  const windowsDir = windowsDirectory();
  if (options.appsFile)
    return listFixtureApps(options.appsFile, {
      read: (file) => readFile(file, "utf8"),
      isFile,
      windowsDir,
    });
  const start = join("Microsoft", "Windows", "Start Menu", "Programs");
  return listInstalledApps({
    roots: [
      join(process.env.ProgramData ?? "C:\\ProgramData", start),
      join(app.getPath("appData"), start),
    ],
    list: async (dir) =>
      (await readdir(dir, { withFileTypes: true })).map((entry) => ({
        name: entry.name,
        directory: entry.isDirectory(),
        file: entry.isFile(),
      })),
    target: (link) => shell.readShortcutLink(link).target,
    isFile,
    windowsDir,
  });
}

// UI-42: one reading of the Start Menu serves the picker and the search for
// a bound app's path; a test's own apps list stands for the whole machine, so
// the registry (App Paths and the MuiCache) is not read then.
const programs = programScan(listPrograms);
const findAppPath = appPathResolver({
  installed: programs.cached,
  ...registrySources(regExe(execFile), {
    fixture: () => Boolean(runtimeOptions().appsFile),
  }),
  isFile: async (path) => (await stat(path)).isFile(),
});

/** Quits from the start-failure page: there is nothing to save. */
function quitAfterFailure(): void {
  closeGuard.abandon();
  app.quit();
}

/**
 * The start failed and the page cannot say why (UI-45): a dialog does,
 * until the user quits or a retry ends otherwise (pageRecovery).
 */
async function showFailureDialog(): Promise<void> {
  if (failureDialog) return;
  failureDialog = true;
  try {
    for (;;) {
      const state = manager?.state;
      if (quitting || state?.phase !== "failed" || !state.failure) return;
      const text = failureDialogText(state.failure);
      const { response } = await dialog.showMessageBox({
        type: "error",
        title: "padmate",
        message: text.message,
        detail: text.detail,
        buttons: text.buttons,
        defaultId: 1,
        cancelId: 2,
        noLink: true,
      });
      if (response === 0) await openStartFolder().catch(() => {});
      else if (response === 1) await manager?.connect();
      else {
        quitAfterFailure();
        return;
      }
    }
  } finally {
    failureDialog = false;
  }
}

/** The page cannot load, but the daemon answers: nothing to show. */
function uiUnavailable(): void {
  dialog.showErrorBox("padmate", uiFailureText(app.isPackaged));
  app.quit();
}

// Opens a macro in an editor: VS Code when installed, else Notepad. A
// development run with PADMATE_UI_EDITOR_LOG records the path instead.
function openEditor(): (path: string) => Promise<boolean> {
  const log = runtimeOptions().editorLog;
  if (log) return editorRecorder(log);
  return editorLauncher({
    vscode: async () =>
      (await app.getApplicationInfoForProtocol("vscode://")).path,
    systemRoot: process.env.SystemRoot,
    start: startDetached,
  });
}

function registerHandlers(update: UpdateController): void {
  ipcMain.on("padmate:eventAck", (event, id: unknown) => {
    if (
      window &&
      event.sender === window.webContents &&
      event.senderFrame === window.webContents.mainFrame
    )
      events.acknowledge(id);
  });
  const methods: Record<string, string> = {
    status: "status.get",
    devices: "devices.list",
    pause: "engine.pause",
    resume: "engine.resume",
    releaseAll: "engine.releaseAll",
    configGet: "config.get",
    scriptStatus: "script.status",
    scriptReload: "script.reload",
    scriptLogs: "script.logs",
  };
  const handlers: Record<string, (...args: unknown[]) => unknown> = {
    connection: () => manager?.state || { phase: "connecting" },
    reconnect: () => manager?.connect(),
    configPut: (text, revision) =>
      manager?.call("config.put", validateConfigWrite(text, revision)),
    setOutput: (enabled) =>
      manager?.call("ui.setOutput", { enabled: validateToggle(enabled) }),
  };
  const editorHandlers: Record<
    string,
    { count: number; run: (...args: unknown[]) => unknown }
  > = {
    profilesList: { count: 0, run: () => manager?.call("profiles.list") },
    profileGet: {
      count: 1,
      run: (id) => manager?.call("profiles.get", { id: validateProfileID(id) }),
    },
    // PRO-19: a profile with its parents merged in; read-only.
    profileResolve: {
      count: 1,
      run: (id) =>
        manager?.call("profiles.resolve", { id: validateProfileID(id) }),
    },
    profilePut: {
      count: 3,
      run: (id, text, revision) =>
        manager?.call("profiles.put", {
          id: validateProfileID(id),
          ...validateConfigWrite(text, revision),
        }),
    },
    ...libraryHandlers((method, params) => manager?.call(method, params)),
    // IN-13: the controller buzzes and flashes, to tell which it is.
    ...deviceHandlers(
      (method, params) => manager?.call(method, params),
      rawWatch,
    ),
    // PKG-10: the automatic update; the window sends no address or version.
    ...updateHandlers(update),
    // UI-19: start at sign-in, the daemon's own entry.
    ...startupHandlers(
      (method, params) => manager?.call(method, params),
      runtimeOptions().packagedTest,
    ),
    // UI-30: the Game Bar page of Windows Settings, the one address Main
    // opens for the window (system-settings.ts).
    ...systemSettingsHandlers((url) => shell.openExternal(url)),
    // UI-45: the page that says why the mapping service did not start.
    ...startFailureHandlers({
      state: () => manager?.state,
      open: openStartFolder,
      retry: () => manager?.connect(),
      quit: quitAfterFailure,
    }),
    // UI-42: bound apps' icons, read from their executables, never from
    // a window.
    ...appIconHandlers({
      thumbnail: (path, size) =>
        nativeImage.createThumbnailFromPath(path, {
          width: size,
          height: size,
        }),
      fileIcon: (path) => app.getFileIcon(path, { size: "large" }),
    }),
    // UI-46: the programs the Start Menu lists, and an .exe the user
    // browses to; the renderer supplies no path.
    ...installedAppHandlers({
      list: programs.fresh,
      chooseExe: async () => {
        if (!window) throw new Error("UI is closed");
        const result = await dialog.showOpenDialog(window, {
          title: "选择软件 / Choose an app",
          properties: ["openFile"],
          filters: [{ name: "EXE", extensions: ["exe"] }],
        });
        return result.canceled ? undefined : result.filePaths[0];
      },
    }),
    // UI-42: the path of a rule's program that is named by file name only.
    ...appPathHandlers(findAppPath),
    // PRO-29: the dialog for a repaired config.json.
    ...configRepairHandlers({
      call: (method, params) => manager?.call(method, params),
      show: showConfigBackup,
    }),
    // PRO-20: backup of one profile as a .padmate file.
    ...archiveHandlers({
      call: (method, params) => manager?.call(method, params),
      chooseExport: async (name) => {
        if (!window) throw new Error("UI is closed");
        const result = await dialog.showSaveDialog(window, {
          title: "导出配置 / Export profile",
          defaultPath: name,
          filters: [
            { name: "padmate 配置 / profile", extensions: [archiveExtension] },
          ],
        });
        return result.canceled || !result.filePath
          ? undefined
          : result.filePath;
      },
      chooseImport: async () => {
        if (!window) throw new Error("UI is closed");
        const result = await dialog.showOpenDialog(window, {
          title: "导入配置 / Import profile",
          properties: ["openFile"],
          // A .padmate backup, or an AntiMicroX profile (PRO-21).
          filters: [
            {
              name: "padmate / AntiMicroX",
              extensions: [archiveExtension, "amgp", "xml"],
            },
            { name: "padmate 配置 / profile", extensions: [archiveExtension] },
            { name: "AntiMicroX", extensions: ["amgp", "xml"] },
          ],
        });
        return result.canceled ? undefined : result.filePaths[0];
      },
      writeFile: (path, data) => writeFile(path, data),
    }),
    // SCR-50: the scripts page's macros and its editor.
    ...macroHandlers({
      call: (method, params) => manager?.call(method, params),
      chooseScript: async () => {
        if (!window) throw new Error("UI is closed");
        const result = await dialog.showOpenDialog(window, {
          title: "导入脚本 / Import script",
          properties: ["openFile"],
          filters: [
            { name: "TypeScript / JavaScript", extensions: ["ts", "js"] },
          ],
        });
        return result.canceled ? undefined : result.filePaths[0];
      },
      openEditor: openEditor(),
      dataDir: runtimeOptions().dataDir,
    }),
    profileValidate: {
      count: 1,
      run: (text) =>
        manager?.call("profiles.validate", {
          text: validateConfigWrite(text, "").text,
        }),
    },
    profileExplain: {
      count: 2,
      run: (control, id) =>
        manager?.call("profiles.explain", {
          control: validateControl(control),
          ...(id === undefined ? {} : { id: validateProfileID(id) }),
        }),
    },
    scriptDescribe: {
      count: 1,
      run: (id) =>
        manager?.call(
          "script.describe",
          id === undefined ? {} : { id: validateProfileID(id) },
        ),
    },
    scriptRegistrations: {
      count: 1,
      run: (device) =>
        manager?.call("script.registrations", {
          device: validateDevice(device),
        }),
    },
    // HUD-05: the UI's choices about the overlay; invalid fields are ignored.
    overlayPrefs: { count: 1, run: (prefs) => overlayPreferences.push(prefs) },
    captureStart: { count: 0, run: () => capture.start() },
    // UI-19: recording a new emergency combo on the controller. IN-19: the
    // mapping wizard asks for a capture that stays while the window is away.
    padCaptureStart: {
      count: 1,
      run: (stays) => padCapture.start(stays === true),
    },
    padCaptureStop: { count: 0, run: () => padCapture.stop() },
    captureStop: {
      count: 1,
      run: (session) => {
        if (typeof session !== "number")
          throw new Error("Invalid capture session");
        capture.stopSession(session);
      },
    },
    readyToClose: { count: 0, run: () => closeGuard.release() },
    export: {
      count: 2,
      run: async (format, content) => {
        const { extension, data } = validateExport(format, content);
        if (!window) throw new Error("UI is closed");
        const result = await dialog.showSaveDialog(window, {
          title: "导出快捷键对照表 / Export shortcut table",
          defaultPath: `padmate-shortcuts.${extension}`,
          filters: [
            {
              name: extension === "png" ? "PNG" : "Markdown",
              extensions: [extension],
            },
          ],
        });
        if (result.canceled || !result.filePath) return { saved: false };
        await writeFile(result.filePath, data);
        return { saved: true };
      },
    },
  };
  for (const [name, item] of Object.entries(editorHandlers))
    handlers[name] = item.run;
  for (const [name, method] of Object.entries(methods))
    handlers[name] = () => manager?.call(method);
  for (const [name, handler] of Object.entries(handlers)) {
    ipcMain.handle(`padmate:${name}`, async (event, ...args: unknown[]) => {
      const url = event.senderFrame?.url;
      const trustedURL = developmentServer
        ? new URL(rendererURL as string).href
        : "padmate://app/index.html";
      if (
        !window ||
        event.sender !== window.webContents ||
        event.senderFrame !== window.webContents.mainFrame ||
        url !== trustedURL
      )
        throw new Error("Untrusted UI caller");
      const count =
        editorHandlers[name]?.count ??
        (name === "configPut" ? 2 : name === "setOutput" ? 1 : 0);
      if (args.length !== count) throw new Error("Invalid UI arguments");
      if (editorHandlers[name]) return uiResult(() => handler(...args));
      return handler(...args);
    });
  }
}

async function registerRenderer(): Promise<void> {
  const policy = contentSecurityPolicy(developmentServer);
  if (developmentServer) {
    session.defaultSession.webRequest.onHeadersReceived((details, callback) => {
      callback({
        responseHeaders: {
          ...details.responseHeaders,
          "Content-Security-Policy": [policy],
        },
      });
    });
    return;
  }
  const mime: Record<string, string> = {
    ".html": "text/html; charset=utf-8",
    ".js": "text/javascript; charset=utf-8",
    ".css": "text/css; charset=utf-8",
    ".svg": "image/svg+xml",
    ".png": "image/png",
    ".woff2": "font/woff2",
  };
  protocol.handle("padmate", async (request) => {
    const url = new URL(request.url);
    if (url.hostname !== "app" || request.method !== "GET")
      return new Response(null, { status: 403 });
    let file: string;
    try {
      file = resolve(rendererRoot, `.${decodeURIComponent(url.pathname)}`);
    } catch {
      return new Response(null, { status: 400 });
    }
    if (!file.startsWith(`${rendererRoot}${sep}`))
      return new Response(null, { status: 403 });
    try {
      const body = await readFile(file);
      return new Response(body, {
        headers: {
          "Content-Type": mime[extname(file)] || "application/octet-stream",
          "Content-Security-Policy": policy,
          "X-Content-Type-Options": "nosniff",
          "Cache-Control": "no-store",
        },
      });
    } catch {
      return new Response(null, {
        status: 404,
        headers: { "Content-Security-Policy": policy },
      });
    }
  });
}

app
  .whenReady()
  .then(async () => {
    if (!firstInstance) return;
    await registerRenderer();
    updates = await createUpdates({
      closeGuard,
      notify: (state) => {
        if (window && !window.isDestroyed())
          window.webContents.send("padmate:updateState", state);
      },
    });
    registerHandlers(updates);
    // The installer an earlier update left is removed once the application
    // is that version.
    if (app.isPackaged) void removeStaleUpdate();
    window = new BrowserWindow({
      width: 1180,
      height: 800,
      minWidth: 850,
      minHeight: 620,
      show: false,
      title: "padmate",
      autoHideMenuBar: true,
      webPreferences: {
        preload: resolve(__dirname, "../preload/index.js"),
        ...baseWebPreferences,
      },
    });
    window.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
    window.webContents.on("will-navigate", (event) => event.preventDefault());
    window.webContents.on("will-attach-webview", (event) =>
      event.preventDefault(),
    );
    window.webContents.on("render-process-gone", () => {
      capture.stop("renderer-exit");
      padCapture.stop();
      rawWatch.reset();
      // Nobody is left to answer: quit without asking the renderer.
      pageReady = false;
      closeGuard.release();
      if (quitting) return;
      // UI-45: the start failed (or is retried); a dialog says why instead.
      if (manager?.state.failure && manager.state.phase !== "connected") {
        rendererGone = true;
        shown = false;
        window?.hide();
        void showFailureDialog();
        return;
      }
      app.quit();
    });
    window.on("blur", () => {
      capture.stop("blur");
      // The wizard's capture stays: the mapping is paused until it closes.
      padCapture.windowAway();
    });
    window.on("close", (event) => {
      capture.stop("close");
      padCapture.stop();
      // Hidden, the page stays: the wizard keeps its raw watch and resumes,
      // paused again, once the window is back in front.
      if (
        closeHides({
          quitting,
          leaving: trayExiting || quitHeld,
          phase: manager?.state.phase,
          pageReady,
        })
      ) {
        event.preventDefault();
        hideOnClose();
        return;
      }
      rawWatch.reset();
      // The daemon's exit saves and quits by itself (DMN-22); its own quit
      // closes the window through here and is let through.
      if (holdsWindowClose({ trayExiting, quitting }, () => closeGuard.hold()))
        event.preventDefault();
    });
    window.on("show", () => {
      hiddenByClose = false;
    });
    window.on("closed", () => {
      window = undefined;
      // With the overlay alive, window-all-closed would not follow this
      // close: the optional window never keeps the app running (ADR-0063).
      overlay?.stop();
      if (!quitting) app.quit();
    });
    window.once("ready-to-show", () => {
      pageReady = true;
      showWhenReady();
      startOverlay();
    });
    window.webContents.on("did-finish-load", () => {
      if (!recovering) return;
      recovering = false;
      pageReady = true;
      showWhenReady();
    });
    window.webContents.on("did-start-loading", () => {
      capture.stop("reload");
      padCapture.stop();
      rawWatch.reset();
      // A page that reloads no longer answers a held close.
      closeGuard.release();
    });
    bindEventDelivery(window.webContents, events);
    // The daemon pauses the mapping itself while the session is locked or
    // asleep; the wizard's capture is kept, to hold it after the unlock.
    powerMonitor.on("suspend", () => {
      capture.stop("suspend");
      padCapture.windowAway();
    });
    powerMonitor.on("lock-screen", () => {
      capture.stop("lock");
      padCapture.windowAway();
    });
    manager = await createDaemonManager(
      routeDaemonEvents({
        deliver: (event) => {
          // A script's toast is for the overlay alone; the page has no use for it.
          if (event.topic !== "hud") events.push(event);
          toasts.handle(event);
          hints.handle(event);
          panels.handle(event);
        },
        // DMN-22: 退出 in the daemon's tray, or the installer's exit
        // (PKG-04, `padmatectl quit`). The daemon has stopped the mapping
        // and released every key, and waits for this UI (2 s at most): the
        // window finishes its saves as when it closes, the UI answers
        // ui.exitReady, then it quits. The end of the connection shows
        // nothing.
        trayExit: () => {
          trayExiting = true;
          manager?.exiting();
          capture.stop("exit");
          padCapture.disconnected();
          rawWatch.reset();
          void exitAfterSaves({
            settle: () => closeGuard.settle(EXIT_WAIT_MS),
            ready: () => manager?.call("ui.exitReady", {}),
            quit: () => {
              closeGuard.abandon();
              if (window && !window.isDestroyed()) window.hide();
              app.quit();
            },
          });
        },
      }),
      (state) => {
        if (state.phase !== "connected") {
          capture.stop("disconnected");
          padCapture.disconnected();
          rawWatch.reset();
          events.clear();
          // No state event will say the hints or panels ended.
          hints.clear();
          panels.clear();
        }
        if (state.phase === "connected") hud.connected();
        else hud.disconnected();
        send("padmate:connectionChanged", state);
        showWhenReady();
        if (quitting) return;
        if (quitsWhileHidden(hiddenByClose, state.phase)) {
          app.quit();
          return;
        }
        switch (
          pageRecovery(state.phase, { broken: pageBroken, gone: rendererGone })
        ) {
          case "dialog":
            void showFailureDialog();
            break;
          case "unavailable":
            uiUnavailable();
            break;
          case "reload":
            if (!window) break;
            rendererGone = false;
            recovering = true;
            window.webContents.reload();
            break;
        }
      },
      hud,
    );
    // A page that does not load still lets a failed start say why.
    pageBroken = !(await window
      .loadURL(
        developmentServer
          ? (rendererURL as string)
          : "padmate://app/index.html",
      )
      .then(
        () => true,
        () => false,
      ));
    void manager.connect();
    // The daily check starts with the window (30 s later, at most once a day).
    updates.start();
  })
  .catch(() => {
    dialog.showErrorBox("padmate", uiFailureText(app.isPackaged));
    app.quit();
  });

app.on("before-quit", (event) => {
  // Quitting closes the window first, which waits for the saves.
  if (window && !window.isDestroyed() && !quitting && closeGuard.hold()) {
    quitHeld = true;
    event.preventDefault();
    return;
  }
  quitting = true;
  overlay?.stop();
  hud.stop();
  capture.stop("exit");
  padCapture.stop();
  rawWatch.reset();
  manager?.close();
  events.close();
  updates?.dispose();
});
// The overlay is destroyed with the main window, so this still fires when the
// last window is gone.
app.on("window-all-closed", () => {
  if (!quitting) app.quit();
});
