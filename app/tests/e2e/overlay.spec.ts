import { writeFile } from "node:fs/promises";
import { join } from "node:path";
import {
  type ElectronApplication,
  expect,
  type Locator,
  type Page,
  test,
} from "@playwright/test";
import {
  cleanup,
  closeApplication,
  type Fixture,
  fixture,
  launch,
  queryWindow,
  rpc,
} from "./helpers";

// SPK-09 and HUD-01 (ADR-0063): the overlay window never takes the
// foreground, and its window has the styles that keep it from doing so.
//
// Focus-dependent, like the key preview and the native capture specs: padmate's
// own window must be in front for the foreground checks, and Windows does not
// let a background process take the foreground. When another program holds
// it, the first test fails at "bring the main window to the front"; run the
// spec again when nobody is using the computer. PADMATE_E2E_DISPLAY=secondary
// keeps both windows off the primary display (the overlay follows the test
// window, see runtime-options.ts).
//
// The foreground is read with `padmate-probe window --json`, built by
// `npm run prepare:e2e`: GetForegroundWindow and GetWindowLongPtrW only. The
// spec sends no OS keys or mouse input: the HUD-04 and HUD-05 cases operate
// the main window's page through Playwright's DOM input only.

const WS_EX_TOPMOST = 0x8;
const WS_EX_TRANSPARENT = 0x20;
const WS_EX_TOOLWINDOW = 0x80;
const WS_EX_LAYERED = 0x80000;
const WS_EX_NOACTIVATE = 0x08000000;
const styles = {
  WS_EX_NOACTIVATE,
  WS_EX_TRANSPARENT,
  WS_EX_TOPMOST,
  WS_EX_LAYERED,
  // Out of Alt+Tab and the taskbar.
  WS_EX_TOOLWINDOW,
};

/** How often the overlay is toggled in one block, and how often the foreground is read in between. */
const toggles = 200;
const chunk = 20;

type Which = "main" | "overlay";

/** The native window handle of the main window or the overlay ("0x…"), if it exists. */
function handleOf(
  app: ElectronApplication,
  which: Which,
): Promise<string | undefined> {
  return app.evaluate(({ BrowserWindow }, which) => {
    // By title: BrowserWindow.getAllWindows() has no order to rely on.
    const isOverlay = (w: { getTitle(): string }) =>
      w.getTitle() === "padmate overlay";
    const windows = BrowserWindow.getAllWindows().filter(
      (w) => !w.isDestroyed(),
    );
    const win = windows.find((w) => isOverlay(w) === (which === "overlay"));
    if (!win) return undefined;
    const handle = win.getNativeWindowHandle();
    const value =
      handle.length >= 8
        ? handle.readBigUInt64LE(0)
        : BigInt(handle.readUInt32LE(0));
    return `0x${value.toString(16)}`;
  }, which);
}

function same(a: string | undefined, b: string | undefined): boolean {
  return !!a && !!b && BigInt(a) === BigInt(b);
}

/** Waits until the overlay window exists and the system says it is visible. */
async function overlayShown(app: ElectronApplication): Promise<string> {
  const seen: { handle?: string } = {};
  await expect
    .poll(
      async () => {
        seen.handle = await handleOf(app, "overlay");
        return seen.handle
          ? (await queryWindow(seen.handle)).window?.visible
          : false;
      },
      { timeout: 15000, message: "the overlay window is shown" },
    )
    .toBe(true);
  return seen.handle as string;
}

/** Brings the main window to the front, as the focus-dependent specs do, and waits until it is. */
async function mainInFront(
  app: ElectronApplication,
  page: Page,
): Promise<string> {
  await page.bringToFront();
  await app.evaluate(({ BrowserWindow }) => {
    const window = BrowserWindow.getAllWindows().find(
      (w) => w.getTitle() !== "padmate overlay",
    );
    // Not app.focus(): on Windows it focuses the app's first window, which
    // can be the overlay (getAllWindows() lists the newest first).
    window?.setAlwaysOnTop(true);
    window?.focus();
    window?.setAlwaysOnTop(false);
  });
  const main = (await handleOf(app, "main")) as string;
  await expect
    .poll(async () => same((await queryWindow()).foreground, main), {
      timeout: 10000,
      message:
        "bring the main window to the front: another program holds the foreground, run this spec when nobody is using the computer",
    })
    .toBe(true);
  return main;
}

function expectStyles(exStyle: number): void {
  for (const [name, bit] of Object.entries(styles))
    expect((exStyle & bit) >>> 0 === bit, name).toBe(true);
}

/** The foreground is still the window it was. */
async function expectForeground(main: string, when: string): Promise<void> {
  const now = (await queryWindow()).foreground;
  expect(same(now, main), `${when}: foreground ${now}, expected ${main}`).toBe(
    true,
  );
}

test("the overlay never takes the foreground, and its window has the right styles (SPK-09, HUD-01)", async () => {
  test.setTimeout(180000);
  const f: Fixture = await fixture();
  let app: ElectronApplication | undefined;
  try {
    const opened = await launch(f);
    const running = opened.app;
    app = running;
    const overlay = await overlayShown(running);
    const main = await mainInFront(running, opened.page);
    expect(same(overlay, main)).toBe(false);

    // The window: styles, owner, size and place.
    const info = (await queryWindow(overlay)).window;
    expect(info?.visible).toBe(true);
    expect(info?.pid).toBe(f.mainPIDs.get(running));
    expectStyles(info?.exStyle ?? 0);

    const place = await running.evaluate(({ app, BrowserWindow, screen }) => {
      const win = BrowserWindow.getAllWindows().find(
        (w) => w.getTitle() === "padmate overlay",
      );
      if (!win) return undefined;
      const bounds = win.getBounds();
      const display = screen.getDisplayMatching(bounds);
      const pid = win.webContents.getOSProcessId();
      return {
        bounds,
        workArea: display.workArea,
        display: display.id,
        primary: screen.getPrimaryDisplay().id,
        displays: screen.getAllDisplays().length,
        sandboxed: app.getAppMetrics().find((metric) => metric.pid === pid)
          ?.sandboxed,
        focusable: win.isFocusable(),
        alwaysOnTop: win.isAlwaysOnTop(),
      };
    });
    expect(place).toBeDefined();
    if (!place) return;
    expect(place.sandboxed).toBe(true);
    expect(place.focusable).toBe(false);
    expect(place.alwaysOnTop).toBe(true);
    // 480x160 DIP, centred, 48 above the bottom of the work area; a pixel
    // of rounding is allowed for scaled displays.
    expect(Math.abs(place.bounds.width - 480)).toBeLessThanOrEqual(1);
    expect(Math.abs(place.bounds.height - 160)).toBeLessThanOrEqual(1);
    expect(
      Math.abs(
        place.bounds.x +
          place.bounds.width / 2 -
          (place.workArea.x + place.workArea.width / 2),
      ),
    ).toBeLessThanOrEqual(1);
    expect(
      Math.abs(
        place.workArea.y +
          place.workArea.height -
          (place.bounds.y + place.bounds.height) -
          48,
      ),
    ).toBeLessThanOrEqual(1);
    if (process.env.PADMATE_E2E_DISPLAY === "secondary" && place.displays > 1)
      expect(place.display).not.toBe(place.primary);

    await expectForeground(main, "after the overlay was shown");

    // Block 1: the content shows and hides inside the overlay's page, as the
    // HUD will do it. The window is not touched.
    const content = (n: number, from: number) =>
      running.evaluate(
        async ({ BrowserWindow }, { n, from }) => {
          const win = BrowserWindow.getAllWindows().find(
            (w) => w.getTitle() === "padmate overlay",
          );
          if (!win) throw new Error("no overlay window");
          for (let i = 0; i < n; i++) {
            const visible = (from + i) % 2 === 0;
            await win.webContents.executeJavaScript(
              `document.querySelector('[data-testid="overlay-root"]').dataset.visible = '${visible}'`,
            );
            await new Promise((done) => setTimeout(done, 5));
          }
          return win.webContents.executeJavaScript(
            `getComputedStyle(document.querySelector('[data-testid="overlay-root"]')).opacity`,
          );
        },
        { n, from },
      );
    for (let from = 0; from < toggles; from += chunk) {
      const opacity = await content(chunk, from);
      // The last toggle of the chunk sets visible when its index is even.
      expect(opacity).toBe((from + chunk - 1) % 2 === 0 ? "1" : "0");
      await expectForeground(main, `after ${from + chunk} content toggles`);
    }
    const afterContent = (await queryWindow(overlay)).window;
    expect(afterContent?.visible).toBe(true);
    expectStyles(afterContent?.exStyle ?? 0);
    console.log(
      `SPK-09 content toggles: ${toggles} done, foreground unchanged, exStyle 0x${((afterContent?.exStyle ?? 0) >>> 0).toString(16)}`,
    );

    // Block 2 (spike only, the product never does this): hide() and
    // showInactive() on the window itself, from Main.
    const cycle = (n: number) =>
      running.evaluate(async ({ BrowserWindow }, n) => {
        const win = BrowserWindow.getAllWindows().find(
          (w) => w.getTitle() === "padmate overlay",
        );
        if (!win) throw new Error("no overlay window");
        for (let i = 0; i < n; i++) {
          win.hide();
          await new Promise((done) => setTimeout(done, 5));
          win.showInactive();
          await new Promise((done) => setTimeout(done, 5));
        }
      }, n);
    for (let done = 0; done < toggles; done += chunk) {
      await cycle(chunk);
      await expectForeground(main, `after ${done + chunk} hide/showInactive`);
    }
    const afterCycles = (await queryWindow(overlay)).window;
    expect(afterCycles?.visible).toBe(true);
    expectStyles(afterCycles?.exStyle ?? 0);
    console.log(
      `SPK-09 hide/showInactive cycles: ${toggles} done, foreground unchanged, exStyle 0x${((afterCycles?.exStyle ?? 0) >>> 0).toString(16)}`,
    );

    // The main window still is the first window, and still works.
    expect(
      await opened.page.evaluate(
        async () => (await window.padmate.getConnection()).phase,
      ),
    ).toBe("connected");
  } finally {
    await cleanup(f, app);
  }
});

test("closing the main window quits the app although the overlay window is alive", async () => {
  test.setTimeout(90000);
  const f = await fixture();
  let app: ElectronApplication | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    await overlayShown(app);
    // closeApplication closes the first window, the main one, and waits for
    // the process to end: without the overlay being destroyed with the main
    // window, window-all-closed would not follow and this times out.
    // The handle is taken first: once the app has gone, app.process() can no
    // longer be asked.
    const child = app.process();
    await closeApplication(app);
    expect(child.exitCode).not.toBeNull();
  } finally {
    await cleanup(f, app);
  }
});

test("the overlay window is made again once after its renderer crashed, and not a second time", async () => {
  test.setTimeout(120000);
  const f = await fixture();
  let app: ElectronApplication | undefined;
  try {
    const opened = await launch(f);
    const running = opened.app;
    app = running;
    const first = await overlayShown(running);
    const main = await mainInFront(running, opened.page);
    const crash = () =>
      running.evaluate(({ BrowserWindow }) => {
        BrowserWindow.getAllWindows()
          .find((w) => w.getTitle() === "padmate overlay")
          ?.webContents.forcefullyCrashRenderer();
      });

    await crash();
    // A new window, shown without taking the foreground, with the styles.
    const second: { handle?: string } = {};
    await expect
      .poll(
        async () => {
          second.handle = await handleOf(running, "overlay");
          if (!second.handle || same(second.handle, first)) return false;
          return (await queryWindow(second.handle)).window?.visible;
        },
        { timeout: 15000, message: "the overlay window is made again" },
      )
      .toBe(true);
    expectStyles((await queryWindow(second.handle)).window?.exStyle ?? 0);
    await expectForeground(main, "after the overlay was made again");

    await crash();
    await expect
      .poll(() => handleOf(running, "overlay"), {
        timeout: 15000,
        message: "the overlay window is gone",
      })
      .toBeUndefined();
    // Not made a third time.
    await new Promise((done) => setTimeout(done, 2000));
    expect(await handleOf(running, "overlay")).toBeUndefined();
    // The app and its main window are untouched.
    expect(
      await opened.page.evaluate(
        async () => (await window.padmate.getConnection()).phase,
      ),
    ).toBe("connected");
    await expectForeground(main, "after the overlay crashed twice");
  } finally {
    await cleanup(f, app);
  }
});

// HUD-04 (ADR-0063): the overlay tells, for a moment, what just changed. The
// daemon's state events make a toast when the active profile changes, mapping
// is paused or resumed, or the layer changes (here: profile and pause; the
// fixture's profile has one layer). Not while padmate's own window is
// focused: the user is looking at the editor then.
//
// Focus-dependent like the specs above: the main window has to be in front
// first, then it is blurred (as editors.spec.ts does), so a program holding
// the foreground fails the first step; run the spec again when nobody is using
// the computer. Nothing is pressed or clicked, and the fixture daemon only
// prints (--dry-run, fake input).

type Shown = { text: string; visible?: string };

/** The overlay's page: Playwright lists a page for every window of the app. */
async function overlayPage(app: ElectronApplication): Promise<Page> {
  const found: { page?: Page } = {};
  await expect
    .poll(
      async () => {
        for (const page of app.windows()) {
          const title = await page.title().catch(() => "");
          if (title === "padmate overlay") {
            found.page = page;
            return true;
          }
        }
        return false;
      },
      { timeout: 15000, message: "the overlay's page" },
    )
    .toBe(true);
  return found.page as Page;
}

/**
 * Records every text the toast element gets, with the root's data-visible at
 * that moment: a toast lasts under two seconds, and polling could miss one.
 */
async function recordToasts(page: Page): Promise<void> {
  await page.evaluate(() => {
    const seen: Shown[] = [];
    (window as unknown as { shownToasts: Shown[] }).shownToasts = seen;
    const pill = document.querySelector('[data-testid="overlay-toast"]');
    const root = document.querySelector<HTMLElement>(
      '[data-testid="overlay-root"]',
    );
    if (!pill || !root) throw new Error("the overlay page has no toast");
    new MutationObserver(() => {
      const text = pill.textContent ?? "";
      if (text) seen.push({ text, visible: root.dataset.visible });
    }).observe(pill, { childList: true, characterData: true, subtree: true });
  });
}

function shownToasts(page: Page): Promise<Shown[]> {
  return page.evaluate(
    () => (window as unknown as { shownToasts: Shown[] }).shownToasts,
  );
}

/** The main window's page shows Settings (a click on the page; nothing is typed). */
async function openSettings(page: Page): Promise<void> {
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "设置", exact: true }),
  ).toBeVisible();
}

function mainFocused(app: ElectronApplication): Promise<boolean | undefined> {
  return app.evaluate(({ BrowserWindow }) =>
    BrowserWindow.getAllWindows()
      .find((w) => w.getTitle() !== "padmate overlay")
      ?.isFocused(),
  );
}

test("the overlay shows a toast for a profile switch, a pause and a resume, but not while the main window is focused (HUD-04)", async () => {
  test.setTimeout(180000);
  const f: Fixture = await fixture();
  let app: ElectronApplication | undefined;
  try {
    const opened = await launch(f);
    const running = opened.app;
    app = running;
    const overlay = await overlayShown(running);
    const page = await overlayPage(running);
    await recordToasts(page);
    await mainInFront(running, opened.page);
    expect(await mainFocused(running)).toBe(true);

    const profiles = (await rpc(f, "profiles.list")) as {
      id: string;
      name?: string;
    }[];
    const named = (id: string) => profiles.find((p) => p.id === id)?.name;
    const defaultName = named("default");
    expect(defaultName, "the fixed default profile has a name").toBeTruthy();
    expect(named("paint")).toBe("E2E paint");
    const active = async () =>
      ((await rpc(f, "status.get")) as { profileId: string }).profileId;

    // 1. The main window is focused: switching the profile and back is not
    // told. Six state events (every 250 ms) go by after each switch.
    await rpc(f, "profiles.activate", { id: "default" });
    await expect.poll(active).toBe("default");
    await new Promise((done) => setTimeout(done, 1500));
    await rpc(f, "profiles.activate", { id: "paint" });
    await expect.poll(active).toBe("paint");
    await new Promise((done) => setTimeout(done, 1500));
    expect(await shownToasts(page)).toEqual([]);
    await expect(page.getByTestId("overlay-root")).toHaveAttribute(
      "data-visible",
      "false",
    );
    expect(await mainFocused(running)).toBe(true);

    // 2. The window loses focus: the same changes are told, once each, in
    // the UI's language (Chinese, until it is set to English below).
    await running.evaluate(({ BrowserWindow }) =>
      BrowserWindow.getAllWindows()
        .find((w) => w.getTitle() !== "padmate overlay")
        ?.blur(),
    );
    await expect
      .poll(() => mainFocused(running), {
        message: "the main window is blurred",
      })
      .toBe(false);
    // Whatever Windows put in front now must stay there: a toast does not
    // take the foreground (the overlay cannot be activated).
    await new Promise((done) => setTimeout(done, 500));
    const resting = (await queryWindow()).foreground;
    expect(
      same(resting, overlay),
      "the overlay did not take the foreground",
    ).toBe(false);

    const told = async (texts: string[]) =>
      expect
        .poll(async () => (await shownToasts(page)).map((s) => s.text), {
          timeout: 10000,
          message: `the toasts so far: ${texts.join(" | ")}`,
        })
        .toEqual(texts);

    await rpc(f, "profiles.activate", { id: "default" });
    const expected = [`配置：${defaultName}`];
    await told(expected);
    await rpc(f, "engine.pause");
    expected.push("已暂停");
    await told(expected);
    await rpc(f, "engine.resume");
    expected.push("已恢复");
    await told(expected);
    await rpc(f, "profiles.activate", { id: "paint" });
    expected.push("配置：E2E paint");
    await told(expected);

    // Each was on screen: the root was visible when the text came.
    expect((await shownToasts(page)).map((s) => s.visible)).toEqual(
      expected.map(() => "true"),
    );
    // The toast fades out and the content is hidden again.
    await expect(page.getByTestId("overlay-toast")).toHaveText("", {
      timeout: 10000,
    });
    await expect(page.getByTestId("overlay-root")).toHaveAttribute(
      "data-visible",
      "false",
    );

    // 3. The language is the UI's: set to English in Settings (the page
    // pushes it to Main, HUD-05), the next toasts are English.
    await openSettings(opened.page);
    await opened.page
      .getByRole("combobox", { name: "语言" })
      .selectOption("en");
    // The push is an IPC call; the next state event is up to 250 ms away.
    await new Promise((done) => setTimeout(done, 300));
    await rpc(f, "engine.pause");
    expected.push("Paused");
    await told(expected);
    await rpc(f, "engine.resume");
    expected.push("Resumed");
    await told(expected);

    // 4. No toast took the foreground, and the overlay is still what it was.
    await expectForeground(resting, "after the toasts");
    const info = (await queryWindow(overlay)).window;
    expect(info?.visible).toBe(true);
    expectStyles(info?.exStyle ?? 0);
  } finally {
    await cleanup(f, app);
  }
});

// IN-12 (ADR-0065): a controller that discharges to 15% is announced on the
// overlay, once per episode and also while the main window is in front (the
// editor shows the battery only on the settings page); charging and a new drop
// is announced again. The fixture daemon has no battery and no motors:
// fake.battery sets the power the fake controller reports, and fake.effects
// lists what the daemon asked of it, which for a low battery is an Alert-class
// play call with the pulses of three and the flash. Focus-dependent only in
// that the main window is brought to the front first, like the specs above;
// nothing is pressed.

interface FakeAlerts {
  effects: {
    kind: string;
    device: number;
    class?: string;
    preset?: string;
    blink?: boolean;
  }[];
}

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("the overlay tells a low battery once per episode, and the fake controller is asked for the alert (IN-12)", async ({}, info) => {
  test.setTimeout(180000);
  const f: Fixture = await fixture();
  let app: ElectronApplication | undefined;
  try {
    const opened = await launch(f);
    const running = opened.app;
    app = running;
    await overlayShown(running);
    const page = await overlayPage(running);
    await recordToasts(page);
    await mainInFront(running, opened.page);
    // The main window stays in front: the toast is told all the same.
    expect(await mainFocused(running)).toBe(true);

    const told = async (texts: string[]) =>
      expect
        .poll(async () => (await shownToasts(page)).map((s) => s.text), {
          timeout: 10000,
          message: `the toasts so far: ${texts.join(" | ")}`,
        })
        .toEqual(texts);
    const alerts = async () =>
      ((await rpc(f, "fake.effects")) as FakeAlerts).effects.filter(
        (call) => call.kind === "play" && call.class === "alert",
      );
    const battery = (state: string, percent?: number) =>
      rpc(
        f,
        "fake.battery",
        percent === undefined ? { state } : { state, percent },
      );
    // The state event that carries the batteries comes every 250 ms.
    const settle = () => new Promise((done) => setTimeout(done, 700));
    const faded = () =>
      expect(page.getByTestId("overlay-toast")).toHaveText("", {
        timeout: 10000,
      });

    // 1. Above the threshold (25%): nothing is told, nothing is asked of the
    // controller.
    await battery("discharging", 60);
    await settle();
    expect(await shownToasts(page)).toEqual([]);
    expect(await alerts()).toEqual([]);

    // 2. Down to 15%: told in the overlay, and the controller is asked for the
    // pulses of three and the flash (Alert class), once.
    await battery("discharging", 15);
    const expected = ["手柄电量低：15%"];
    await told(expected);
    await page.screenshot({
      path: info.outputPath("overlay-battery-toast.png"),
    });
    expect(await alerts()).toEqual([
      expect.objectContaining({ device: 1, preset: "pulse3", blink: true }),
    ]);
    await faded();

    // Lower in the same episode: not told again, not asked again.
    await battery("discharging", 10);
    await settle();
    expect(await shownToasts(page)).toHaveLength(1);
    expect(await alerts()).toHaveLength(1);

    // The settings page shows it on the controller's row.
    await openSettings(opened.page);
    await expect(opened.page.getByText("电量 10%")).toBeVisible();

    // 3. Charging (re-arms at once, whatever the charge): the next drop is
    // another episode, told and asked for again.
    await battery("charging", 40);
    await settle();
    await expect(opened.page.getByText("充电中")).toBeVisible();
    expect(await shownToasts(page)).toHaveLength(1);
    await battery("discharging", 15);
    expected.push("手柄电量低：15%");
    await told(expected);
    expect(await alerts()).toHaveLength(2);
    await expect(opened.page.getByText("电量 15%")).toBeVisible();
    // Each was on screen: the root was visible when the text came.
    expect((await shownToasts(page)).map((s) => s.visible)).toEqual(
      expected.map(() => "true"),
    );
  } finally {
    await cleanup(f, app);
  }
});

// HUD-05 (ADR-0063): Settings → 浮层 has three rows: 提示消息 (on or off),
// 位置 (底部 or 顶部) and 不透明度 (40-100 %). They are the UI's own, kept in
// its localStorage, and the page pushes them to Main, which moves the window
// (setBounds only), gates the toasts and tells the overlay's page the opacity.
//
// Focus-dependent like the toast spec above, for the same reasons: the main
// window is brought to the front first, and blurred before the toasts are
// looked at. Clicks go to the page by CDP and do not change which window is
// focused; the spec checks that, and that nothing takes the foreground.

/** The overlay window's bounds and the work area of the display it is on. */
function overlayPlace(app: ElectronApplication) {
  return app.evaluate(({ BrowserWindow, screen }) => {
    const win = BrowserWindow.getAllWindows().find(
      (w) => w.getTitle() === "padmate overlay",
    );
    if (!win) return undefined;
    const bounds = win.getBounds();
    const display = screen.getDisplayMatching(bounds);
    return { bounds, workArea: display.workArea, display: display.id };
  });
}

type Place = NonNullable<Awaited<ReturnType<typeof overlayPlace>>>;

/** Distance of the window from the top / bottom of its work area, and from being centred. */
function gaps(place: Place) {
  const { bounds, workArea } = place;
  return {
    top: bounds.y - workArea.y,
    bottom: workArea.y + workArea.height - (bounds.y + bounds.height),
    centre: Math.abs(
      bounds.x + bounds.width / 2 - (workArea.x + workArea.width / 2),
    ),
  };
}

/** Sets a range input the way a drag does: the value, then an input event React hears. */
async function setRange(
  section: Locator,
  name: string,
  value: number,
): Promise<void> {
  await section.getByRole("slider", { name }).evaluate((input, value) => {
    const range = input as HTMLInputElement;
    const set = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )?.set;
    set?.call(range, String(value));
    range.dispatchEvent(new Event("input", { bubbles: true }));
  }, value);
}

/** The overlay root's --overlay-opacity, as the page has it. */
function pageOpacity(page: Page): Promise<string> {
  return page.evaluate(() =>
    (
      document.querySelector('[data-testid="overlay-root"]') as HTMLElement
    ).style.getPropertyValue("--overlay-opacity"),
  );
}

test("the overlay follows the position, toast and opacity settings (HUD-05)", async () => {
  test.setTimeout(240000);
  const f: Fixture = await fixture();
  let app: ElectronApplication | undefined;
  try {
    const opened = await launch(f);
    const running = opened.app;
    app = running;
    const overlay = await overlayShown(running);
    const page = await overlayPage(running);
    await recordToasts(page);
    const main = await mainInFront(running, opened.page);

    const before = await overlayPlace(running);
    expect(before).toBeDefined();
    if (!before) return;
    // The default: at the bottom, 48 DIP above the work area's edge.
    expect(Math.abs(gaps(before).bottom - 48)).toBeLessThanOrEqual(1);

    await openSettings(opened.page);
    const section = opened.page.getByTestId("overlay-settings");
    const toasts = section.getByRole("switch", { name: "提示消息" });
    await expect(toasts).toHaveAttribute("aria-checked", "true");
    await expect(section.getByRole("radio", { name: "底部" })).toHaveAttribute(
      "aria-checked",
      "true",
    );
    await expect(section.getByRole("slider", { name: "不透明度" })).toHaveValue(
      "100",
    );

    // 1. 顶部: the window's top edge is 48 DIP below the top of its display's
    // work area, still centred, the same size, on the same display, and not
    // shown, raised or focused to get there.
    await section.getByRole("radio", { name: "顶部" }).click();
    let top: Place | undefined;
    await expect
      .poll(
        async () => {
          top = await overlayPlace(running);
          return top ? Math.abs(gaps(top).top - 48) : Number.POSITIVE_INFINITY;
        },
        { timeout: 10000, message: "the overlay moved to the top" },
      )
      .toBeLessThanOrEqual(1);
    if (!top) return;
    expect(gaps(top).centre).toBeLessThanOrEqual(1);
    expect(top.bounds.width).toBe(before.bounds.width);
    expect(top.bounds.height).toBe(before.bounds.height);
    expect(top.display).toBe(before.display);
    expect(top.workArea).toEqual(before.workArea);
    expect(
      await opened.page.evaluate(() =>
        localStorage.getItem("padmate.overlay.position"),
      ),
    ).toBe("top");
    await expectForeground(main, "after the overlay moved to the top");
    const moved = (await queryWindow(overlay)).window;
    expect(moved?.visible).toBe(true);
    expectStyles(moved?.exStyle ?? 0);

    // 底部 again.
    await section.getByRole("radio", { name: "底部" }).click();
    await expect
      .poll(
        async () => {
          const place = await overlayPlace(running);
          return place
            ? Math.abs(gaps(place).bottom - 48)
            : Number.POSITIVE_INFINITY;
        },
        { timeout: 10000, message: "the overlay moved back to the bottom" },
      )
      .toBeLessThanOrEqual(1);
    await expectForeground(main, "after the overlay moved back");

    // The main window loses focus: toasts are told from now on. Clicks on
    // the page do not bring it back.
    await running.evaluate(({ BrowserWindow }) =>
      BrowserWindow.getAllWindows()
        .find((w) => w.getTitle() !== "padmate overlay")
        ?.blur(),
    );
    await expect
      .poll(() => mainFocused(running), {
        message: "the main window is blurred",
      })
      .toBe(false);
    await new Promise((done) => setTimeout(done, 500));
    const resting = (await queryWindow()).foreground;
    expect(
      same(resting, overlay),
      "the overlay did not take the foreground",
    ).toBe(false);

    // 2. 提示消息 off: a pause and a resume are not told, although the
    // window is blurred and the overlay is there. The state events come
    // every 250 ms; two seconds is eight of them after each.
    await toasts.click();
    await expect(toasts).toHaveAttribute("aria-checked", "false");
    expect(
      await opened.page.evaluate(() =>
        localStorage.getItem("padmate.overlay.toasts"),
      ),
    ).toBe("off");
    expect(await mainFocused(running)).toBe(false);
    await new Promise((done) => setTimeout(done, 500));
    await rpc(f, "engine.pause");
    await new Promise((done) => setTimeout(done, 2000));
    await rpc(f, "engine.resume");
    await new Promise((done) => setTimeout(done, 2000));
    expect(await shownToasts(page)).toEqual([]);
    await expect(page.getByTestId("overlay-root")).toHaveAttribute(
      "data-visible",
      "false",
    );
    // The window stays as it is: shown, with its styles.
    const quiet = (await queryWindow(overlay)).window;
    expect(quiet?.visible).toBe(true);
    expectStyles(quiet?.exStyle ?? 0);

    // Back on: the next change is told, and the ones before are not.
    await toasts.click();
    await expect(toasts).toHaveAttribute("aria-checked", "true");
    await new Promise((done) => setTimeout(done, 500));
    const told = async (texts: string[]) =>
      expect
        .poll(async () => (await shownToasts(page)).map((s) => s.text), {
          timeout: 10000,
          message: `the toasts so far: ${texts.join(" | ")}`,
        })
        .toEqual(texts);
    await rpc(f, "engine.pause");
    await told(["已暂停"]);

    // 3. 不透明度: the pill fades in to the chosen share (the page's custom
    // property on the root; the toast's computed opacity reaches it within
    // the 120 ms fade in, long before the 1.5 s hold is over).
    await setRange(section, "不透明度", 50);
    await expect(section.getByText("50%")).toBeVisible();
    expect(
      await opened.page.evaluate(() =>
        localStorage.getItem("padmate.overlay.opacity"),
      ),
    ).toBe("50");
    await expect
      .poll(() => pageOpacity(page), {
        message: "the overlay's page got the opacity",
      })
      .toBe("0.5");
    await rpc(f, "engine.resume");
    await told(["已暂停", "已恢复"]);
    await expect
      .poll(
        () =>
          page.evaluate(
            () =>
              getComputedStyle(
                document.querySelector(
                  '[data-testid="overlay-toast"]',
                ) as Element,
              ).backgroundColor,
          ),
        {
          intervals: [50, 50, 100, 100, 100],
          timeout: 1500,
          message:
            "the toast's background is at half its opacity (the text stays opaque)",
        },
      )
      .toBe("rgba(20, 20, 24, 0.41)");
    await expect(page.getByTestId("overlay-toast")).toHaveText("", {
      timeout: 10000,
    });
    await setRange(section, "不透明度", 100);
    await expect.poll(() => pageOpacity(page)).toBe("1");

    // 4. Nothing took the foreground, and the overlay is what it was.
    expect(await mainFocused(running)).toBe(false);
    await expectForeground(resting, "after the settings were changed");
    const end = (await queryWindow(overlay)).window;
    expect(end?.visible).toBe(true);
    expectStyles(end?.exStyle ?? 0);
    const after = await overlayPlace(running);
    expect(after?.bounds).toEqual(before.bounds);
  } finally {
    await cleanup(f, app);
  }
});

// HUD-03 (ADR-0063): the 按键提示 action lists, while its button is held, what
// the other controls of the current layer do. The daemon puts the list in its
// state events (every 250 ms), Main passes it to the overlay's page, and the
// window grows for the panel (setBounds only: the bottom edge stays, the toast
// area does not move) and shrinks back after the button is let go.
//
// Focus-dependent like the specs above: the main window is brought to the
// front first and stays focused, because hints show also then (the user asked
// for them by holding the button). The fixture daemon is in fake-input mode
// (--dry-run), so pressing a button through fake.press sends no key. Written
// for the main session to run; it has not been run.

type Row = { name: string; label: string };

/** The rows of the hints panel, as the overlay's page has them. */
function hintRows(page: Page): Promise<Row[]> {
  return page.evaluate(() =>
    [
      ...document.querySelectorAll(
        '[data-testid="overlay-hints"] .overlay-hint',
      ),
    ].map((row) => ({
      name: row.querySelector(".overlay-hint-control")?.textContent ?? "",
      label: row.querySelector(".overlay-hint-label")?.textContent ?? "",
    })),
  );
}

test("holding the button bound to the key hints lists the layer's bindings in the overlay, and letting go hides them (HUD-03)", async () => {
  test.setTimeout(240000);
  const f: Fixture = await fixture();
  let app: ElectronApplication | undefined;
  try {
    // The fixture's paint profile with the hints action on the right
    // shoulder, one binding with a label and one without (its first action
    // is told instead). Written before the app starts.
    await writeFile(
      join(f.path, "data", "profiles", "paint", "profile.json"),
      JSON.stringify({
        schemaVersion: 0,
        name: "E2E paint",
        layers: {
          base: {
            south: {
              label: "撤销",
              press: { type: "keys", keys: "Ctrl+Z", mode: "tap" },
            },
            east: {
              label: "重做",
              press: { type: "keys", keys: "Ctrl+Y", mode: "tap" },
            },
            north: { press: { type: "keys", keys: "F13", mode: "tap" } },
            rightshoulder: { press: { type: "hud", op: "hints" } },
          },
        },
      }),
    );
    const opened = await launch(f);
    const running = opened.app;
    app = running;
    const overlay = await overlayShown(running);
    const page = await overlayPage(running);
    const main = await mainInFront(running, opened.page);
    expect(await mainFocused(running)).toBe(true);
    const panel = page.getByTestId("overlay-hints");
    await expect(panel).toHaveAttribute("data-visible", "false");
    const before = await overlayPlace(running);
    expect(before).toBeDefined();
    if (!before) return;
    expect(before.bounds.height).toBe(160);

    // 1. Press and hold the right shoulder: the panel lists the bindings of
    // the layer, but not the hints control itself, with the main window
    // still focused.
    await rpc(f, "fake.press", { control: "rightshoulder" });
    await expect(panel).toHaveAttribute("data-visible", "true", {
      timeout: 10000,
    });
    const rows = await hintRows(page);
    // Three bindings besides the hints control itself; the one without a
    // label is told by its first action.
    const labels = rows.map((row) => row.label);
    expect(rows).toHaveLength(3);
    expect(labels).toContain("撤销");
    expect(labels).toContain("重做");
    expect(labels.filter((label) => label.includes("F13"))).toHaveLength(1);
    // Every control has a name, and the hints control is not among them.
    for (const row of rows) expect(row.name).not.toBe("");
    expect(new Set(rows.map((row) => row.name)).size).toBe(rows.length);
    expect(await mainFocused(running)).toBe(true);

    // 2. The window grew for the panel by setBounds: taller, the same bottom
    // edge, the same place and size otherwise, not shown, raised or focused.
    let grown: Place | undefined;
    await expect
      .poll(
        async () => {
          grown = await overlayPlace(running);
          return grown?.bounds.height ?? 0;
        },
        { timeout: 10000, message: "the overlay window grew for the hints" },
      )
      .toBeGreaterThan(160);
    if (!grown) return;
    expect(grown.bounds.height).toBeLessThanOrEqual(360);
    expect(grown.bounds.x).toBe(before.bounds.x);
    expect(grown.bounds.width).toBe(before.bounds.width);
    expect(grown.bounds.y + grown.bounds.height).toBe(
      before.bounds.y + before.bounds.height,
    );
    expect(grown.display).toBe(before.display);
    await expectForeground(main, "while the hints are shown");
    const shown = (await queryWindow(overlay)).window;
    expect(shown?.visible).toBe(true);
    expectStyles(shown?.exStyle ?? 0);

    // 3. Let go: the panel fades out and is empty, the window shrinks back.
    await rpc(f, "fake.release", { control: "rightshoulder" });
    await expect(panel).toHaveAttribute("data-visible", "false", {
      timeout: 10000,
    });
    await expect.poll(() => hintRows(page), { timeout: 5000 }).toEqual([]);
    await expect
      .poll(async () => (await overlayPlace(running))?.bounds, {
        timeout: 10000,
        message: "the overlay window shrank back",
      })
      .toEqual(before.bounds);

    // 4. Again: the panel comes back, then goes (pressing and letting go is
    // all it takes), and nothing took the foreground.
    await rpc(f, "fake.press", { control: "rightshoulder" });
    await expect(panel).toHaveAttribute("data-visible", "true", {
      timeout: 10000,
    });
    await rpc(f, "fake.release", { control: "rightshoulder" });
    await expect(panel).toHaveAttribute("data-visible", "false", {
      timeout: 10000,
    });
    await expectForeground(main, "after the hints were shown twice");
    const end = (await queryWindow(overlay)).window;
    expect(end?.visible).toBe(true);
    expectStyles(end?.exStyle ?? 0);
    expect(await mainFocused(running)).toBe(true);
  } finally {
    await cleanup(f, app);
  }
});

// SCR-22 (ADR-0063): a script's hud.toast and hud.panel reach the overlay. The
// UI subscribes to the daemon's hud topic while its overlay exists (the daemon
// then tells scripts so: host.has("hud")), shows a script's toast as the pill,
// and draws the panels the daemon lists in its state events where the key
// hints go. The window grows for the panels by setBounds only, like for the
// hints, and shrinks back when the script removes them.
//
// Focus-dependent like the hints case above: the main window is brought to the
// front and stays focused, because a script's toast and panels show also then
// (the main window does not show them). The fixture daemon is in fake-input
// mode (--dry-run), so pressing a button through fake.press sends no key, but
// the script's handlers run. Written for the main session to run; it has not
// been run.

/** The lines of the script panels as the overlay's page draws them: a title, or "left | right". */
function panelLines(page: Page): Promise<string[]> {
  return page.evaluate(() =>
    [...document.querySelectorAll('[data-testid="overlay-panels"] li')].map(
      (line) =>
        line.children.length === 0
          ? (line.textContent ?? "")
          : [...line.children]
              .map((cell) => cell.textContent ?? "")
              .join(" | "),
    ),
  );
}

test("a script's hud.toast and hud.panel show in the overlay, and the panels go when the script removes them (SCR-22)", async () => {
  test.setTimeout(240000);
  const f: Fixture = await fixture();
  let app: ElectronApplication | undefined;
  try {
    // The fixture's paint profile with a script of API 0.21 (the current one;
    // hud is there since 0.16):
    // west shows a toast and two panels (the second says whether the daemon
    // knows an overlay is there), north removes both. Written before the app
    // starts.
    const folder = join(f.path, "data", "profiles", "paint");
    await writeFile(
      join(folder, "profile.json"),
      JSON.stringify({
        schemaVersion: 0,
        name: "E2E paint",
        script: { entry: "main.ts", api: "0.21" },
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
      join(folder, "main.ts"),
      `import { host, hud, on } from "padmate";
on("west", "press", () => {
  hud.toast("脚本提示");
  hud.panel("p", { title: "画笔", rows: [["大小", "12"], ["流量", "80"]] });
  hud.panel("h", { rows: [["浮层", String(host.has("hud"))]] });
});
on("north", "press", () => {
  hud.panel("p", null);
  hud.panel("h", null);
});
`,
    );
    const opened = await launch(f);
    const running = opened.app;
    app = running;
    const overlay = await overlayShown(running);
    const page = await overlayPage(running);
    const main = await mainInFront(running, opened.page);
    expect(await mainFocused(running)).toBe(true);
    const panel = page.getByTestId("overlay-panels");
    await expect(panel).toHaveAttribute("data-visible", "false");
    const before = await overlayPlace(running);
    expect(before).toBeDefined();
    if (!before) return;
    expect(before.bounds.height).toBe(160);
    await recordToasts(page);

    // padmate's own window in front is a local dry run (M10): the daemon
    // pauses the scripts while it is the foreground. So, as in the toast case,
    // the main window is brought to the front first (a program holding the
    // foreground fails that step) and then blurred; whatever Windows puts in
    // front then must stay there.
    await running.evaluate(({ BrowserWindow }) =>
      BrowserWindow.getAllWindows()
        .find((w) => w.getTitle() !== "padmate overlay")
        ?.blur(),
    );
    await expect
      .poll(() => mainFocused(running), {
        message: "the main window is blurred",
      })
      .toBe(false);
    await new Promise((done) => setTimeout(done, 500));
    const resting = (await queryWindow()).foreground;
    expect(
      same(resting, overlay),
      "the overlay did not take the foreground",
    ).toBe(false);
    expect(
      same(resting, main),
      "another window took the foreground after the blur",
    ).toBe(false);

    // padmate's own window coming to the front and leaving it each release
    // the slot (M10), which drops the script's runtime; the next press asks
    // for a new one (SCRIPTING §4.3), and a press before its setup is done
    // is dropped. So press east, which the script does not use and the
    // profile does not bind, until the script runs, before the first press
    // that counts.
    let lastStatus: unknown;
    await expect
      .poll(
        async () => {
          const views = await rpc(f, "script.status");
          lastStatus = views;
          const runs =
            Array.isArray(views) &&
            views.some(
              (view: { script?: { state?: string } }) =>
                view.script?.state === "Running",
            );
          if (!runs) {
            await rpc(f, "fake.press", { control: "east" });
            await rpc(f, "fake.release", { control: "east" });
          }
          return runs;
        },
        {
          timeout: 30000,
          message: "the script is running",
        },
      )
      .toBe(true)
      .catch(async (error: unknown) => {
        const status = (await rpc(f, "status.get")) as Record<string, unknown>;
        const { slots: _slots, devices: _devices, ...daemon } = status ?? {};
        throw new Error(
          `the script is not running; daemon ${JSON.stringify(daemon)}; last script.status: ${JSON.stringify(lastStatus)}`,
          { cause: error },
        );
      });

    // 1. West: the script's toast shows in the pill, and the panels list the
    // script's two panels in the order it made them, the second telling that
    // the daemon knows of the overlay.
    await rpc(f, "fake.press", { control: "west" });
    await rpc(f, "fake.release", { control: "west" });
    await expect
      .poll(async () => (await shownToasts(page)).map((toast) => toast.text), {
        timeout: 10000,
        message: "the script's toast",
      })
      .toContain("脚本提示");
    expect(
      (await shownToasts(page)).find((toast) => toast.text === "脚本提示")
        ?.visible,
    ).toBe("true");
    await expect(panel).toHaveAttribute("data-visible", "true", {
      timeout: 10000,
    });
    expect(await panelLines(page)).toEqual([
      "画笔",
      "大小 | 12",
      "流量 | 80",
      "浮层 | true",
    ]);
    // The key hints are not up, and nothing took the foreground.
    await expect(page.getByTestId("overlay-hints")).toHaveAttribute(
      "data-visible",
      "false",
    );
    await expectForeground(resting, "while the toast and panels show");

    // 2. The window grew for the panels by setBounds: the four rows, the
    // same bottom edge, the same place and size otherwise, not shown, raised
    // or focused. 260 DIP is a whole number of pixels at 125% and 175%, which
    // the 242 of three rows is not; Electron reports whole DIP, so heights
    // and edges are compared to a pixel, as in the first case of this file.
    const grownHeight = 160 + 8 + 4 * 18 + 20;
    let grown: Place | undefined;
    await expect
      .poll(
        async () => {
          grown = await overlayPlace(running);
          return Math.abs((grown?.bounds.height ?? 0) - grownHeight) <= 1;
        },
        {
          timeout: 10000,
          message: "the overlay window grew for the panels",
        },
      )
      .toBe(true);
    if (!grown) return;
    expect(grown.bounds.x).toBe(before.bounds.x);
    expect(grown.bounds.width).toBe(before.bounds.width);
    expect(
      Math.abs(
        grown.bounds.y +
          grown.bounds.height -
          (before.bounds.y + before.bounds.height),
      ),
    ).toBeLessThanOrEqual(1);
    expect(grown.display).toBe(before.display);
    await expectForeground(resting, "while the panels are shown");
    const shown = (await queryWindow(overlay)).window;
    expect(shown?.visible).toBe(true);
    expectStyles(shown?.exStyle ?? 0);

    // 3. North: the script removes both panels. They fade out and the page is
    // empty again, and the window shrinks back.
    await rpc(f, "fake.press", { control: "north" });
    await rpc(f, "fake.release", { control: "north" });
    await expect(panel).toHaveAttribute("data-visible", "false", {
      timeout: 10000,
    });
    await expect.poll(() => panelLines(page), { timeout: 5000 }).toEqual([]);
    await expect
      .poll(async () => (await overlayPlace(running))?.bounds, {
        timeout: 10000,
        message: "the overlay window shrank back",
      })
      .toEqual(before.bounds);

    // 4. Nothing took the foreground, and the overlay is what it was.
    await expectForeground(resting, "after the panels were removed");
    const end = (await queryWindow(overlay)).window;
    expect(end?.visible).toBe(true);
    expectStyles(end?.exStyle ?? 0);
  } finally {
    await cleanup(f, app);
  }
});
