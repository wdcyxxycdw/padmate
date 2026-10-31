import { type ChildProcess, execFile, spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { join, resolve, sep } from "node:path";
import { promisify } from "node:util";
import {
  type ElectronApplication,
  _electron as electron,
  expect,
  type Locator,
  type Page,
} from "@playwright/test";

const execute = promisify(execFile);
export const root = resolve(__dirname, "../../..");
const requireTest = createRequire(join(root, "app/package.json"));
const { PNG } = requireTest(
  join(root, "app/node_modules/playwright-core/lib/utilsBundle.js"),
) as {
  PNG: {
    sync: {
      read(data: Buffer): { width: number; height: number; data: Buffer };
    };
  };
};
const binaryRoot = join(root, ".cache", "m10-e2e", "bin");
const temporaryRoot = join(root, ".cache", "m10-e2e", "runs");
const ctl = join(binaryRoot, "padmatectl.exe");
const daemon = join(binaryRoot, "m10-daemon.test.exe");

export interface Fixture {
  path: string;
  ipc: string;
  env: Record<string, string>;
  applications: ElectronApplication[];
  mainPIDs: Map<ElectronApplication, number>;
}

export async function fixture(): Promise<Fixture> {
  await mkdir(temporaryRoot, { recursive: true });
  const path = await mkdtemp(join(temporaryRoot, "run-"));
  const ipc = join(path, "run");
  const data = join(path, "data");
  await mkdir(join(data, "profiles", "paint"), { recursive: true });
  await writeFile(
    join(data, "profiles", "paint", "profile.json"),
    JSON.stringify({
      schemaVersion: 0,
      name: "E2E paint",
      layers: {
        base: {
          south: {
            label: "撤销",
            press: { type: "keys", keys: "Ctrl+Z", mode: "tap" },
          },
        },
      },
    }),
  );
  // padmated resumes state.json's last profile (ADR-0053). Switching by app
  // is off, so paint stays in use whatever window is in front during a
  // test: an app no rule binds would switch to the fixed default.
  await writeFile(
    join(data, "state.json"),
    JSON.stringify({ schemaVersion: 0, lastProfile: "paint" }),
  );
  await writeFile(
    join(data, "config.json"),
    JSON.stringify({ schemaVersion: 0, enabled: false }),
  );
  const env: Record<string, string> = {};
  for (const [key, value] of Object.entries(process.env))
    if (value !== undefined) env[key] = value;
  // Electron is always launched as an app, even when the caller is a node-like host.
  delete env.ELECTRON_RUN_AS_NODE;
  Object.assign(env, {
    PADMATE_UI_TEST_HARNESS: "1",
    PADMATE_UI_DAEMON: daemon,
    PADMATE_UI_CTL: ctl,
    PADMATE_UI_IPC_DIR: ipc,
    PADMATE_UI_DATA_DIR: data,
    PADMATE_UI_USER_DATA: join(path, "browser"),
    // The scripts page records the files it opens instead of starting an
    // editor (SCR-50).
    PADMATE_UI_EDITOR_LOG: join(path, "editor.log"),
    PADMATE_M10_HARNESS: "1",
    PADMATE_M10_IPC_DIR: ipc,
    PADMATE_M10_PIPE: `\\\\.\\pipe\\padmate-m10-e2e-${randomUUID()}`,
    PADMATE_M10_DATA_DIR: data,
  });
  return { path, ipc, env, applications: [], mainPIDs: new Map() };
}

/**
 * PADMATE_E2E_DISPLAY=secondary moves the test window to the first display
 * that is not the primary one before it shows, so the person at the
 * computer keeps the primary display. Tests that need the real foreground
 * (keyboard capture, the key preview) still need the window focused.
 *
 * The main window is the one that is not the overlay (ADR-0063, title
 * "padmate overlay"). BrowserWindow.getAllWindows() has no order to rely on:
 * Electron 44 lists the newest window first, so [0] is the overlay.
 */
export async function placeWindow(app: ElectronApplication): Promise<void> {
  if (process.env.PADMATE_E2E_DISPLAY !== "secondary") return;
  await app.evaluate(({ BrowserWindow, screen }) => {
    const primary = screen.getPrimaryDisplay().id;
    const display = screen.getAllDisplays().find((d) => d.id !== primary);
    const window = BrowserWindow.getAllWindows().find(
      (w) => w.getTitle() !== "padmate overlay",
    );
    if (!display || !window) return;
    const area = display.workArea;
    const [width, height] = window.getSize();
    window.setPosition(
      Math.round(area.x + Math.max(0, (area.width - width) / 2)),
      Math.round(area.y + Math.max(0, (area.height - height) / 2)),
    );
  });
}

/**
 * Starts the app and waits until Main is connected. early, when given, runs
 * as soon as the renderer's document has the preload API: before the
 * renderer has read anything from the daemon, so it can watch what shows
 * first (the first-run page goes away by itself).
 */
export async function launch(
  f: Fixture,
  early?: (app: ElectronApplication, page: Page) => Promise<void>,
): Promise<{ app: ElectronApplication; page: Page }> {
  const app = await electron.launch({
    args: [join(root, "app", "out", "main", "index.js")],
    env: f.env,
    timeout: 20000,
  });
  f.applications.push(app);
  f.mainPIDs.set(app, await app.evaluate(() => process.pid));
  const page = await app.firstWindow();
  await placeWindow(app);
  page.on("pageerror", (error) =>
    console.error("Renderer error:", error.message),
  );
  if (early) {
    // Polled by time: the window stays hidden until Main is connected.
    await page.waitForFunction(() => typeof window.padmate !== "undefined", {
      polling: 50,
      timeout: 15000,
    });
    await early(app, page);
  }
  await expect
    .poll(
      () =>
        page.evaluate(async () =>
          typeof window.padmate === "undefined"
            ? "loading"
            : (await window.padmate.getConnection()).phase,
        ),
      { timeout: 15000 },
    )
    .toBe("connected")
    .catch(async (error) => {
      console.error(
        "Connection state:",
        await page.evaluate(() => window.padmate?.getConnection()),
      );
      await closeApplication(app);
      throw error;
    });
  await app.evaluate(({ BrowserWindow }) => {
    const window = BrowserWindow.getAllWindows().find(
      (w) => w.getTitle() !== "padmate overlay",
    );
    window?.show();
    window?.focus();
  });
  return { app, page };
}

/**
 * Sets the size of the main window (not the overlay, see placeWindow).
 */
export async function setWindowSize(
  app: ElectronApplication,
  width: number,
  height: number,
): Promise<void> {
  await app.evaluate(
    ({ BrowserWindow }, size) => {
      BrowserWindow.getAllWindows()
        .find((w) => w.getTitle() !== "padmate overlay")
        ?.setSize(size.width, size.height);
    },
    { width, height },
  );
}

export async function rpc(
  f: Fixture,
  method: string,
  params = {},
): Promise<unknown> {
  const result = await execute(
    ctl,
    ["rpc", "--json", "--ipc-dir", f.ipc, method, JSON.stringify(params)],
    { windowsHide: true, env: f.env, timeout: 10000 },
  );
  return JSON.parse(result.stdout);
}

/**
 * Records chord in a shortcut field (its group, named 快捷键 or 第 N 步) the
 * way the user does, since shortcuts cannot be typed (OUT-18): 录入 starts
 * the real native capture, and the chord arrives as the capture helper
 * reports it. The first key that is not a modifier ends the capture and
 * commits it, so there is nothing to stop (ADR-0076): chord must have one.
 * Main hands the renderer the chord, because a test must not press real keys
 * (invariant 6). The renderer keeps only the event of its own capture
 * session, so it goes out for each session this app may have started, and
 * again until the field shows it.
 */
export async function recordShortcut(
  app: ElectronApplication,
  field: Locator,
  chord: string,
): Promise<void> {
  await field.getByRole("button", { name: /^录入/ }).click();
  const busy = field.getByRole("button", { name: "录入中…", exact: true });
  await expect(busy).toBeVisible();
  await expect
    .poll(async () => {
      await app.evaluate(({ BrowserWindow }, keys) => {
        const contents = BrowserWindow.getAllWindows().find(
          (w) => w.getTitle() !== "padmate overlay",
        )?.webContents;
        if (!contents) return;
        for (let session = 1; session <= 64; session++)
          contents.send("padmate:keyboardCapture", {
            active: true,
            chord: keys,
            session,
          });
      }, chord);
      return field.getAttribute("data-keys");
    })
    .toBe(chord);
  await expect(busy).toHaveCount(0);
}

export async function discoveryPID(f: Fixture): Promise<number> {
  const value = JSON.parse(await readFile(join(f.ipc, "daemon.json"), "utf8"));
  expect(value.pipe).toBe(f.env.PADMATE_M10_PIPE);
  return value.pid as number;
}

/** Whether the main window (not the overlay) is visible; null without one. */
export function mainWindowVisible(
  app: ElectronApplication,
): Promise<boolean | null> {
  return app.evaluate(
    ({ BrowserWindow }) =>
      BrowserWindow.getAllWindows()
        .find((w) => w.getTitle() !== "padmate overlay")
        ?.isVisible() ?? null,
  );
}

/**
 * Closes the main window as its close button does. While the daemon is
 * connected that only hides it (ADR-0077): the application stays.
 */
export async function closeWindow(app: ElectronApplication): Promise<void> {
  await app.evaluate(({ BrowserWindow }) => {
    BrowserWindow.getAllWindows()
      .find((w) => w.getTitle() !== "padmate overlay")
      ?.close();
  });
}

export async function closeApplication(
  app: ElectronApplication,
): Promise<void> {
  if (app.process().exitCode !== null) return;
  // Closing the window only hides it (ADR-0077), so the application quits,
  // as the tray's 退出 makes it; the quit closes the window first, which
  // waits for the saves. Playwright 1.63's Electron close path waits for an
  // obsolete Node inspector shutdown message. Schedule the quit, then detach
  // the test-owned inspector through the same transport Playwright normally
  // uses.
  const closed = app.waitForEvent("close", { timeout: 5000 });
  await app
    .evaluate(({ app: application }) => {
      setTimeout(() => application.quit(), 20);
    })
    .catch(() => {});
  const implementation = (
    app as unknown as {
      _connection: {
        toImpl(value: ElectronApplication): {
          _nodeConnection: { close(): void };
        };
      };
    }
  )._connection.toImpl(app);
  implementation._nodeConnection.close();
  // A detached daemon may retain Playwright's extra Windows stdio handles.
  for (const stream of app.process().stdio) {
    if (stream && "destroy" in stream) stream.destroy();
  }
  await closed;
}

export async function cleanup(
  f: Fixture,
  app?: ElectronApplication,
): Promise<void> {
  for (const application of new Set([
    ...f.applications,
    ...(app ? [app] : []),
  ])) {
    let timeout: ReturnType<typeof setTimeout> | undefined;
    try {
      const launchedProcess = application.process();
      await Promise.race([
        closeApplication(application),
        new Promise<void>(
          (done) =>
            (timeout = setTimeout(() => {
              const pid = f.mainPIDs.get(application);
              if (pid) {
                try {
                  process.kill(pid);
                } catch {
                  /* Already exited. */
                }
              }
              launchedProcess.kill();
              done();
            }, 2000)),
        ),
      ]);
    } catch {
      /* Already crashed or closed. */
    } finally {
      if (timeout) clearTimeout(timeout);
    }
  }
  try {
    const pid = await discoveryPID(f);
    // The authenticated discovery belongs to this fixture's random pipe.
    process.kill(pid);
    await expect
      .poll(async () => {
        try {
          process.kill(pid, 0);
          return true;
        } catch {
          return false;
        }
      })
      .toBe(false);
  } catch {
    /* Harness never reached ready, or already exited. */
  }
  const path = resolve(f.path);
  if (!path.startsWith(`${resolve(temporaryRoot)}${sep}`))
    throw new Error("Refusing to remove a non-fixture path");
  await rm(path, {
    recursive: true,
    force: true,
    maxRetries: 5,
    retryDelay: 100,
  });
}

/** What `padmate-probe window --json` prints (SPK-09, ADR-0063). */
export interface WindowInfo {
  /** GetForegroundWindow, "0x" and hexadecimal. */
  foreground: string;
  foregroundPid: number;
  /** Only when a window was asked for. */
  window?: {
    hwnd: string;
    /** GWL_EXSTYLE, unsigned. */
    exStyle: number;
    visible: boolean;
    pid: number;
  };
}

/**
 * Asks padmate-probe (built by `prepare:e2e`) for the foreground window and,
 * with hwnd ("0x…"), for that window's extended style, visibility and
 * process. The command only reads: it installs no hook and sends no input.
 * windowsHide, because a console program started from a process without a
 * console would get a console window of its own.
 */
export async function queryWindow(hwnd?: string): Promise<WindowInfo> {
  const result = await execute(
    join(binaryRoot, "padmate-probe.exe"),
    ["window", "--json", ...(hwnd ? ["--hwnd", hwnd] : [])],
    { windowsHide: true, timeout: 10000 },
  );
  return JSON.parse(result.stdout) as WindowInfo;
}

export async function probe(): Promise<{
  child: ChildProcess;
  finished: Promise<string>;
}> {
  const child = spawn(
    join(binaryRoot, "padmate-probe.exe"),
    ["record", "--json", "--swallow", "injected", "--duration", "2s"],
    { windowsHide: true, shell: false, stdio: "pipe" },
  );
  let records = "";
  child.stdout?.on("data", (chunk: Buffer) => {
    records += chunk.toString("utf8");
  });
  const finished = new Promise<string>((done, reject) => {
    child.on("error", reject);
    child.on("exit", (code) =>
      code === 0 ? done(records) : reject(new Error(`Probe exited ${code}`)),
    );
  });
  await new Promise<void>((done, reject) => {
    let stderr = "";
    const timeout = setTimeout(() => {
      child.kill();
      reject(new Error("Probe did not become ready"));
    }, 5000);
    child.stderr?.on("data", (chunk: Buffer) => {
      stderr += chunk.toString("utf8");
      if (stderr.includes("ready")) {
        clearTimeout(timeout);
        done();
      }
    });
    child.on("error", (error) => {
      clearTimeout(timeout);
      reject(error);
    });
  });
  return { child, finished };
}

/** Whether two PNG screenshots show the same picture, give or take a pixel's rounding. */
export function sameFrame(a: Buffer, b: Buffer): boolean {
  if (a.equals(b)) return true;
  const x = PNG.sync.read(a),
    y = PNG.sync.read(b);
  if (x.width !== y.width || x.height !== y.height) return false;
  let changed = 0;
  for (let i = 0; i < x.data.length; i += 4) {
    let delta = 0;
    for (let channel = 0; channel < 4; channel++)
      delta = Math.max(
        delta,
        Math.abs(x.data[i + channel] - y.data[i + channel]),
      );
    // PBR redraws can differ by one level at a few anti-aliased pixels.
    // A real marker, tilt or rotation changes far more pixels and intensity.
    if (delta > 1) return false;
    if (delta && ++changed > 12) return false;
  }
  return true;
}

/**
 * The window and the page frames do not scroll or overflow (the rail, the
 * main area and the page content): what "fits" means at the usual zoom.
 */
export async function expectPageFits(page: Page): Promise<void> {
  await expect
    .poll(() =>
      page.evaluate(() => {
        return [
          document.documentElement,
          ...document.querySelectorAll(
            ".sidebar, .main-content, .page-content",
          ),
        ]
          .filter(
            (element) =>
              element.scrollWidth > element.clientWidth ||
              element.scrollHeight > element.clientHeight,
          )
          .map((element) => element.className || element.tagName);
      }),
    )
    .toEqual([]);
}

/**
 * The chips and the heading of the mapping page do not overlap, and no name is
 * cut off. The two cells of a stick (leftstick and leftstick.press) are one
 * button split in two and share their 1 px edge; nothing else may touch.
 */
export async function expectEditorLabelsFit(page: Page): Promise<void> {
  const overlaps = await page
    .locator("button[data-callout], .controller-heading, .controller-tools")
    .evaluateAll((elements) => {
      const bounds = elements.map((element) => ({
        id: element.getAttribute("data-callout") || element.className,
        rect: element.getBoundingClientRect().toJSON(),
      }));
      const cellsOfOneStick = (a: { id: string }, b: { id: string }) =>
        b.id === `${a.id}.press` || a.id === `${b.id}.press`;
      return bounds.flatMap((a, i) =>
        bounds
          .slice(i + 1)
          .filter((b) => {
            const across =
              Math.min(a.rect.right, b.rect.right) -
              Math.max(a.rect.left, b.rect.left);
            const down =
              Math.min(a.rect.bottom, b.rect.bottom) -
              Math.max(a.rect.top, b.rect.top);
            return across > 0 && down > (cellsOfOneStick(a, b) ? 1.01 : 0);
          })
          .map((b) => ({ first: a, second: b })),
      );
    });
  expect(overlaps).toEqual([]);
  const clippedNames = await page
    .locator('button[data-placement="bottom"] > span')
    .evaluateAll((labels) =>
      labels
        .filter((label) => label.scrollWidth > label.clientWidth)
        .map((label) => label.textContent),
    );
  expect(clippedNames).toEqual([]);
}
