import { mkdir, readdir, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import {
  type ElectronApplication,
  _electron as electron,
  expect,
  type Page,
  test,
} from "@playwright/test";
import { axeCheck } from "./a11y-helpers";
import { cleanup, type Fixture, fixture, placeWindow, root } from "./helpers";

// UI-45: when the mapping service does not start, the window says why and
// offers 打开文件夹, 重试 and 退出 instead of quitting. 打开文件夹 is not
// clicked here: it opens Explorer. A config.json with errors is no such
// failure since PRO-29: it is repaired (config-repair.spec.ts); one that
// cannot be read at all, here a folder, still is.

/** Starts the app without waiting for a connection, which never comes. */
async function open(
  f: Fixture,
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
  return { app, page };
}

const phase = (page: Page) =>
  page.evaluate(async () => (await window.padmate.getConnection()).phase);

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("a config.json that cannot be read says where, and 重试 after the fix opens the app", async ({}, info) => {
  const f = await fixture();
  let app: ElectronApplication | undefined;
  const config = join(f.env.PADMATE_UI_DATA_DIR, "config.json");
  // A folder where the file belongs: nothing the daemon can repair.
  await rm(config, { force: true });
  await mkdir(config);
  try {
    const opened = await open(f);
    app = opened.app;
    const page = opened.page;
    const view = page.getByTestId("start-failure");
    await expect(view).toBeVisible({ timeout: 20000 });
    // Main shows the window on ready-to-show once the start is decided; the
    // page can already be rendered (hidden) a moment before that.
    await expect
      .poll(() =>
        app?.evaluate(({ BrowserWindow }) =>
          BrowserWindow.getAllWindows()
            .find((w) => w.getTitle() !== "padmate overlay")
            ?.isVisible(),
        ),
      )
      .toBe(true);
    expect(await phase(page)).toBe("failed");
    await expect(
      page.getByRole("heading", { name: "映射服务未能启动" }),
    ).toBeVisible();
    await expect(page.getByTestId("start-summary")).toHaveText(
      /^无法读取设置文件 config.json/,
    );
    await expect(page.getByTestId("start-path")).toHaveText(config);
    await expect(
      page.getByRole("button", { name: "打开文件夹" }),
    ).toBeVisible();
    await expect(page.getByRole("button", { name: "退出" })).toBeVisible();
    // Nothing of the app around it, and no developer hints.
    await expect(page.getByRole("navigation")).toHaveCount(0);
    await expect(view).not.toContainText("task ");
    await expect(view).not.toContainText("docs/");
    await page.screenshot({ path: info.outputPath("start-failure.png") });
    // UI-24: the page is checked like the rest of the window.
    await axeCheck(page, "start failure");

    await rm(config, { recursive: true });
    await writeFile(
      config,
      JSON.stringify({ schemaVersion: 0, enabled: false }),
    );
    await page.getByRole("button", { name: "重试" }).click();
    await expect.poll(() => phase(page), { timeout: 15000 }).toBe("connected");
    await expect(view).toHaveCount(0);
    await expect(page.getByRole("navigation")).toBeVisible();
    // Main read the failed start's report and removed it.
    expect(
      await readdir(join(f.env.PADMATE_UI_USER_DATA, "startup-reports")),
    ).toEqual([]);
  } finally {
    await cleanup(f, app);
  }
});

test("a mapping service that cannot run says so, and 退出 quits", async () => {
  const f = await fixture();
  let app: ElectronApplication | undefined;
  f.env.PADMATE_UI_DAEMON = join(f.path, "missing", "padmated.exe");
  try {
    const opened = await open(f);
    app = opened.app;
    const page = opened.page;
    await expect(page.getByTestId("start-summary")).toHaveText(
      "无法运行映射服务。",
      { timeout: 20000 },
    );
    await expect(page.getByTestId("start-path")).toHaveCount(0);
    await page.getByRole("button", { name: "退出" }).click();
    const launched = app.process();
    await expect
      .poll(() => launched.exitCode, { timeout: 10000 })
      .not.toBeNull();
  } finally {
    await cleanup(f, app);
  }
});
