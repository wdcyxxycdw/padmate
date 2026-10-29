import { spawn } from "node:child_process";
import { join } from "node:path";
import { type ElectronApplication, expect, test } from "@playwright/test";
import {
  cleanup,
  closeWindow,
  discoveryPID,
  fixture,
  launch,
  mainWindowVisible,
  root,
} from "./helpers";

// The path of the Electron the tests run the application with.
const electronPath = require("electron") as unknown as string;

const mainWindowMinimized = (app: ElectronApplication) =>
  app.evaluate(
    ({ BrowserWindow }) =>
      BrowserWindow.getAllWindows()
        .find((w) => w.getTitle() !== "padmate overlay")
        ?.isMinimized() ?? null,
  );

// PKG-10 (ADR-0073): one window of padmate per set of browser data. After an
// update the installer starts padmate again, and a click on the shortcut twice
// would otherwise open two. Electron keys the lock by the userData folder, and
// the tests give each run a folder of its own, so the runs of the other specs
// neither meet this lock nor each other's.

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("windows with private data of their own run side by side, and a second start with the same data hands over to the first", async ({}) => {
  test.setTimeout(120000);
  const one = await fixture();
  const two = await fixture();
  try {
    const first = await launch(one);
    const second = await launch(two);
    // Different folders, different locks: both are up and connected.
    for (const { app, page } of [first, second]) {
      expect(app.process().exitCode).toBeNull();
      await expect(
        page.evaluate(async () => (await window.padmate.getConnection()).phase),
      ).resolves.toBe("connected");
    }

    // The first one is minimised, as a person leaves it.
    await first.app.evaluate(({ BrowserWindow }) => {
      BrowserWindow.getAllWindows()
        .find((w) => w.getTitle() !== "padmate overlay")
        ?.minimize();
    });
    await expect.poll(() => mainWindowMinimized(first.app)).toBe(true);

    // A second start with the same browser data (the folder of the first):
    // it quits at once, and starts neither a window nor a daemon.
    const exitCode = await startAgain(one.env);
    expect(exitCode).toBe(0);

    // The first window is brought back.
    await expect.poll(() => mainWindowMinimized(first.app)).toBe(false);
    expect(first.app.process().exitCode).toBeNull();
    await expect(
      first.page.evaluate(
        async () => (await window.padmate.getConnection()).phase,
      ),
    ).resolves.toBe("connected");
    // The other run was not disturbed.
    expect(second.app.process().exitCode).toBeNull();
  } finally {
    await cleanup(one);
    await cleanup(two);
  }
});

/** A second start with the data of f: it hands over to the first and quits. */
async function startAgain(env: NodeJS.ProcessEnv): Promise<number | null> {
  const child = spawn(
    electronPath,
    [join(root, "app", "out", "main", "index.js")],
    {
      env,
      stdio: "ignore",
      windowsHide: true,
    },
  );
  return new Promise<number | null>((done) => {
    const timer = setTimeout(() => {
      child.kill();
      done(-1);
    }, 45000);
    child.on("exit", (code) => {
      clearTimeout(timer);
      done(code);
    });
  });
}

// ADR-0077: closing the window hides it while the daemon is connected, so the
// page and the controller's model stay loaded and the next start shows them
// at once; the hidden application quits once the daemon goes away.
// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("closing the window hides it, a second start shows the same page again, and the hidden window quits when the daemon goes away", async ({}) => {
  test.setTimeout(120000);
  const f = await fixture();
  try {
    const { app, page } = await launch(f);
    const child = app.process();
    // A mark on the page: a page loaded again would not have it.
    await page.evaluate(() => {
      (window as unknown as { kept?: boolean }).kept = true;
    });
    await closeWindow(app);
    await expect.poll(() => mainWindowVisible(app)).toBe(false);
    expect(child.exitCode).toBeNull();
    await expect(
      page.evaluate(async () => (await window.padmate.getConnection()).phase),
    ).resolves.toBe("connected");

    expect(await startAgain(f.env)).toBe(0);
    await expect.poll(() => mainWindowVisible(app)).toBe(true);
    expect(
      await page.evaluate(
        () => (window as unknown as { kept?: boolean }).kept === true,
      ),
    ).toBe(true);

    // Hidden again, then the daemon ends without saying it is exiting.
    await closeWindow(app);
    await expect.poll(() => mainWindowVisible(app)).toBe(false);
    process.kill(await discoveryPID(f));
    await expect.poll(() => child.exitCode, { timeout: 20000 }).not.toBeNull();
  } finally {
    await cleanup(f);
  }
});
