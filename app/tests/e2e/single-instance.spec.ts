import { spawn } from "node:child_process";
import { join } from "node:path";
import { type ElectronApplication, expect, test } from "@playwright/test";
import { cleanup, fixture, launch, root } from "./helpers";

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
    const child = spawn(
      electronPath,
      [join(root, "app", "out", "main", "index.js")],
      { env: one.env, stdio: "ignore", windowsHide: true },
    );
    const exitCode = await new Promise<number | null>((done) => {
      const timer = setTimeout(() => {
        child.kill();
        done(-1);
      }, 45000);
      child.on("exit", (code) => {
        clearTimeout(timer);
        done(code);
      });
    });
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
