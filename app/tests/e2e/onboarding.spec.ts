import { readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import {
  type ElectronApplication,
  expect,
  type Page,
  test,
} from "@playwright/test";
import type { StatusResult } from "../../src/shared/api";
import { cleanup, fixture, launch, rpc, setWindowSize } from "./helpers";

// A missing control fails its step with the locator instead of the whole
// test running out of time without cleanup.
test.use({ actionTimeout: 15000 });

/** The first-run page as it first shows with a pad at the smallest size. */
interface FirstRunFrame {
  title: string;
  status: string;
  text: string;
  /** The focused control while the page shows. */
  focused: string;
  scroll: number;
  fits: boolean;
}

/**
 * Runs in the renderer before it reads anything from the daemon: records
 * the first frame of the first-run page with a connected pad at the
 * smallest window size in window.firstRun, and the focus until the page
 * goes away by itself (ADVANCE_MS), however long the test takes to look.
 */
function recordFirstRun() {
  const record = window as unknown as { firstRun?: FirstRunFrame };
  let done = false;
  const check = () => {
    if (done) return;
    const open = document.querySelector("dialog[open]");
    const frame = record.firstRun;
    if (!open) {
      done = !!frame;
      return;
    }
    const focused = open.contains(document.activeElement)
      ? (document.activeElement?.textContent ?? "")
      : "";
    if (frame) {
      if (focused) frame.focused = focused;
      return;
    }
    if (
      !open.querySelector('.onboarding-pad[data-state="connected"]') ||
      window.innerWidth > 850 ||
      window.innerHeight > 620
    )
      return;
    const body = open.querySelector(".dialog-body") as HTMLElement;
    const box = open.querySelector(".dialog-frame") as HTMLElement;
    record.firstRun = {
      title: open.querySelector("h2")?.textContent ?? "",
      status: open.querySelector(".onboarding-status")?.textContent ?? "",
      text: (open as HTMLElement).innerText,
      focused,
      scroll: body.scrollHeight - body.clientHeight,
      fits: box.getBoundingClientRect().bottom <= window.innerHeight,
    };
  };
  const frame = () => {
    check();
    if (!done) requestAnimationFrame(frame);
  };
  requestAnimationFrame(frame);
  // Frames may wait while the window is still hidden.
  const timer = setInterval(() => {
    check();
    if (done) clearInterval(timer);
  }, 25);
}

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("首次运行 shows the pad, then opens the main page by itself with the fixed default", async ({}, info) => {
  test.setTimeout(90000);
  const f = await fixture();
  const data = join(f.path, "data");
  // A new user: no profiles, no remembered profile, nothing in config.json.
  await rm(join(data, "profiles", "paint"), { recursive: true, force: true });
  await rm(join(data, "state.json"), { force: true });
  await writeFile(join(data, "config.json"), '{"schemaVersion":0}');
  let app: ElectronApplication | undefined;
  let page: Page | undefined;
  try {
    // The page goes on by itself after a moment, so it is recorded from
    // the start, at the smallest window size, before the slower checks.
    const opened = await launch(f, async (electronApp, early) => {
      await setWindowSize(electronApp, 850, 620);
      await early.evaluate(recordFirstRun);
    });
    app = opened.app;
    page = opened.page;
    const p = page;
    const dialog = p.getByRole("dialog");

    // padmated made the fixed default, which maps nothing, and uses it.
    expect(
      JSON.parse(
        await readFile(
          join(data, "profiles", "default", "profile.json"),
          "utf8",
        ),
      ),
    ).toEqual({ schemaVersion: 1, name: "默认", layers: { base: {} } });
    expect(((await rpc(f, "status.get")) as StatusResult).profileId).toBe(
      "default",
    );

    // What the page showed, however long the checks above took.
    const shown = (await (
      await p.waitForFunction(
        () =>
          (window as unknown as { firstRun?: FirstRunFrame }).firstRun ?? null,
        undefined,
        { timeout: 15000 },
      )
    ).jsonValue()) as FirstRunFrame;
    // The focus is read once the page is gone: it moves there on showing.
    await expect(dialog).toHaveCount(0);
    const focused = await p.evaluate(
      () =>
        (window as unknown as { firstRun?: FirstRunFrame }).firstRun?.focused,
    );
    expect(shown.title).toBe("连接手柄");
    expect(shown.status).toContain("已连接：padmate IPC fake gamepad");
    expect(shown.status).toContain("即将进入主页");
    expect(focused).toBe("进入主页");
    expect(shown.text).not.toMatch(/层/);
    // It fits the smallest window without scrolling.
    expect(shown.scroll).toBeLessThanOrEqual(0);
    expect(shown.fits).toBe(true);

    // Nothing is clicked: it closed by itself, and the default stays in
    // use. Nothing is made: no profile, no change to config.json.
    await expect(p.getByTestId("profile-menu")).toContainText("默认");
    expect(((await rpc(f, "status.get")) as StatusResult).profileId).toBe(
      "default",
    );
    expect(await readFile(join(data, "config.json"), "utf8")).toBe(
      '{"schemaVersion":0}',
    );
    // The mapping page shows the pad; every key is the user's to set.
    await expect(p.locator('button[data-callout="south"]')).toBeVisible();
    expect(
      await p.evaluate(() => localStorage.getItem("padmate.onboarding")),
    ).toBe("done");
    await p.screenshot({ path: info.outputPath("first-run-main.png") });

    // It does not open again, and Settings has no entry for it.
    await p.reload();
    await expect(p.getByTestId("profile-menu")).toContainText("默认");
    await expect(dialog).toHaveCount(0);
    await p
      .getByRole("navigation")
      .getByRole("button", { name: "设置", exact: true })
      .click();
    await expect(p.getByRole("combobox", { name: "语言" })).toBeVisible();
    await expect(p.getByText("入门向导")).toHaveCount(0);
    // The library holds only the default.
    expect(
      ((await rpc(f, "profiles.list")) as { id: string }[]).map(
        (entry) => entry.id,
      ),
    ).toEqual(["default"]);
  } catch (error) {
    console.error(
      "Page text:",
      await page?.locator("body").innerText({ timeout: 2000 }).catch(String),
    );
    throw error;
  } finally {
    await cleanup(f, app);
  }
});
