import { execFile } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { promisify } from "node:util";
import { _electron as electron, expect, test } from "@playwright/test";
import type { StatusResult } from "../../src/shared/api";
import { closeApplication, placeWindow } from "./helpers";

const run = promisify(execFile);

// PKG-03: the application as the installer lays it out, started in its test
// mode. PADMATE_TEST_PACKAGED_APP names an installed or unpacked padmate.exe
// (dist\installer\win-unpacked\padmate.exe of task package:installer, or the
// installed one); without it this test is skipped. The application keeps to
// folders of its own, starts its bundled daemon with --dry-run and one fake
// controller over IPC (never --inject), and refuses the login startup entry
// (app/src/main/runtime-options.ts). Nothing of the real %APPDATA% or the
// real Run key is used.
const packagedApp = process.env.PADMATE_TEST_PACKAGED_APP;

const escapeRegExp = (text: string) =>
  text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

/** The file the process with pid runs, or "" when there is no such process. */
async function imageOf(pid: number): Promise<string> {
  try {
    const { stdout } = await run(
      "powershell.exe",
      [
        "-NoProfile",
        "-NonInteractive",
        "-Command",
        `(Get-Process -Id ${pid} -ErrorAction Stop).Path`,
      ],
      { windowsHide: true },
    );
    return stdout.trim();
  } catch {
    return "";
  }
}

/** The pid that the discovery file of dir names. */
async function discoveredPid(dir: string): Promise<number> {
  const value = JSON.parse(
    await readFile(join(dir, "daemon.json"), "utf8"),
  ) as {
    pid: number;
  };
  return value.pid;
}

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("the packaged application starts its bundled daemon, shows the controller and its version", async ({}, info) => {
  test.skip(
    !packagedApp,
    "set PADMATE_TEST_PACKAGED_APP to an installed or unpacked padmate.exe",
  );
  test.setTimeout(120000);
  const exe = resolve(packagedApp as string);
  const bundledDaemon = join(dirname(exe), "resources", "bin", "padmated.exe");
  // Private folders, one with a space and Chinese in its name.
  const root = await mkdtemp(join(tmpdir(), "padmate packaged 安装版-"));
  const data = join(root, "data");
  const ipc = join(root, "run");
  const browser = join(root, "browser");
  await mkdir(join(data, "profiles", "paint"), { recursive: true });
  await mkdir(ipc, { recursive: true });
  // A user who has a profile: no first-run page, and nothing else changes.
  await writeFile(
    join(data, "profiles", "paint", "profile.json"),
    JSON.stringify({
      schemaVersion: 0,
      name: "Packaged paint",
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
    if (value !== undefined && !key.startsWith("PADMATE_")) env[key] = value;
  delete env.ELECTRON_RUN_AS_NODE;
  Object.assign(env, {
    PADMATE_UI_TEST_PACKAGED: "1",
    PADMATE_UI_TEST_FAKE_INPUT: "1",
    PADMATE_UI_DATA_DIR: data,
    PADMATE_UI_IPC_DIR: ipc,
    PADMATE_UI_USER_DATA: browser,
  });
  // The filter above drops every PADMATE_ variable; the display choice of the
  // test run must still reach the app, so its overlay leaves the primary
  // display alone like the main window (placeWindow).
  if (process.env.PADMATE_E2E_DISPLAY)
    env.PADMATE_E2E_DISPLAY = process.env.PADMATE_E2E_DISPLAY;
  let app: Awaited<ReturnType<typeof electron.launch>> | undefined;
  try {
    app = await electron.launch({
      executablePath: exe,
      args: [],
      env,
      timeout: 30000,
    });
    const page = await app.firstWindow();
    await placeWindow(app);
    page.on("pageerror", (error) =>
      console.error("Renderer error:", error.message),
    );

    // The installed application, with the folders it was told.
    const facts = await app.evaluate(({ app }) => ({
      packaged: app.isPackaged,
      version: app.getVersion(),
      userData: app.getPath("userData"),
    }));
    expect(facts.packaged).toBe(true);
    expect(resolve(facts.userData)).toBe(resolve(browser));

    await expect
      .poll(
        () =>
          page.evaluate(async () =>
            typeof window.padmate === "undefined"
              ? "loading"
              : (await window.padmate.getConnection()).phase,
          ),
        { timeout: 45000 },
      )
      .toBe("connected");
    await app.evaluate(({ BrowserWindow }) => {
      const window = BrowserWindow.getAllWindows().find(
        (w) => w.getTitle() !== "padmate overlay",
      );
      window?.show();
      window?.focus();
    });

    // Its daemon is the bundled one, a dry run with the fake controller.
    const status = (await page.evaluate(() =>
      window.padmate.getStatus(),
    )) as StatusResult;
    expect(status.dryRun).toBe(true);
    expect(status.fakeInput).toBe(true);
    expect((await imageOf(await discoveredPid(ipc))).toLowerCase()).toBe(
      bundledDaemon.toLowerCase(),
    );

    // The mapping page draws the controller.
    await page
      .getByRole("navigation")
      .getByRole("button", { name: "映射", exact: true })
      .click();
    const controller = page.locator(
      'fieldset.controller-3d[data-renderer="webgl"]',
    );
    await expect(controller).toBeVisible({ timeout: 30000 });
    await expect(controller.locator("canvas")).toBeVisible();
    await page.screenshot({ path: info.outputPath("packaged-mapping.png") });

    // One version: the daemon says the version the package.json inside
    // app.asar has (PKG-06), and the settings page shows it.
    expect(status.version.replace(/^v/, "").split(" ")[0]).toBe(facts.version);
    await page.getByRole("button", { name: "设置", exact: true }).click();
    await expect(
      page
        .getByText(new RegExp(`^padmate v${escapeRegExp(facts.version)}`))
        .first(),
    ).toBeVisible();
    await page.screenshot({ path: info.outputPath("packaged-settings.png") });

    // The login startup entry is off in the test mode, and cannot be turned on.
    const startup = await page.evaluate(() => window.padmate.getStartup());
    expect(startup.available).toBe(false);
    await expect(
      page.evaluate(() => window.padmate.setStartup(true)),
    ).rejects.toThrow();
  } finally {
    if (app) await closeApplication(app).catch(() => {});
    try {
      const pid = await discoveredPid(ipc);
      // The daemon outlives the window; this one is the test's own. A pid
      // that another program has taken (the file is a stale one) is left
      // alone: only the bundled daemon is ended.
      if ((await imageOf(pid)).toLowerCase() === bundledDaemon.toLowerCase())
        process.kill(pid);
    } catch {
      /* Never started, or already gone. */
    }
    await rm(root, {
      recursive: true,
      force: true,
      maxRetries: 10,
      retryDelay: 200,
    });
  }
});
