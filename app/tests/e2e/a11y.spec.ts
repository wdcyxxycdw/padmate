import { rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import {
  type ElectronApplication,
  expect,
  type Locator,
  type Page,
  test,
} from "@playwright/test";
import {
  type AxeOutcome,
  axeCheck,
  styleSignature,
  unreachableControls,
  walkTabs,
} from "./a11y-helpers";
import { editorFixture } from "./editor-fixture";
import { cleanup, fixture, launch, rpc, setWindowSize } from "./helpers";

// UI-24: the accessibility of the real window, in the part that needs layout:
// axe-core with contrast and target size on, Windows high contrast (forced
// colors), the keyboard, the Esc hold that ends a shortcut capture, and
// zoom. The part that does not need layout runs in jsdom with `task ci`
// (src/renderer/a11y.test.tsx, theme-contrast.test.ts, forced-colors.test.ts).
// What axe cannot decide here, and what only a person can judge (Narrator,
// the contrast themes of Windows, display scaling), is the owner's
// acceptance in `task app:dev`.
//
// Nothing here presses a key of the computer: Playwright's keyboard sends
// DevTools key events to the page (invariant 6).

test.use({ actionTimeout: 15000 });

const nav = (page: Page, name: string) =>
  page.getByRole("navigation").getByRole("button", { name, exact: true });
const chip = (page: Page, control: string) =>
  page.locator(`button[data-callout="${control}"]`);
const closeDialog = async (page: Page) => {
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
};
/** Opens a menu and chooses an item; the dialog or page it leads to is the caller's. */
async function choose(page: Page, trigger: Locator, item: string | RegExp) {
  await trigger.click();
  await page.getByRole("menuitem", { name: item }).first().click();
}
async function openControl(page: Page, control: string) {
  await chip(page, control).click();
  await expect(page.getByTestId("mapping-editor")).toBeVisible();
}
const settings = async (page: Page) => {
  await nav(page, "设置").click();
  await expect(
    page.getByRole("heading", { name: "设置", level: 1 }),
  ).toBeVisible();
};

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("axe finds nothing in the real window, state by state", async ({}, info) => {
  test.setTimeout(420000);
  const f = await editorFixture();
  let app: ElectronApplication | undefined;
  const outcomes: AxeOutcome[] = [];
  try {
    const launched = await launch(f);
    app = launched.app;
    const page = launched.page;
    await setWindowSize(app, 1180, 800);
    // No transition is half-way when axe reads a color.
    await page.emulateMedia({ reducedMotion: "reduce" });
    const check = async (label: string) => {
      outcomes.push(await axeCheck(page, label));
    };

    // The mapping page: a chip and the drawer of several kinds of key.
    await expect(chip(page, "south")).toBeVisible();
    await check("mapping");
    for (const control of ["south", "east", "lefttrigger", "dpad.up"]) {
      await openControl(page, control);
      await check(`drawer: ${control}`);
      // The trigger's and the d-pad's own settings are in a disclosure.
      const extra = page.locator(".drawer-extra summary").first();
      if (await extra.count()) {
        await extra.click();
        await check(`drawer: ${control}, its settings open`);
      }
    }
    await chip(page, "leftstick").click();
    await expect(page.getByTestId("mapping-editor")).toHaveAttribute(
      "data-control",
      "leftstick",
    );
    await check("drawer: leftstick");
    // The parameter menu is in 高级, which is closed until it is opened.
    await page.getByTestId("stick-advanced").locator("summary").click();
    await check("drawer: leftstick, 高级 open");
    await page.locator('[data-testid="analog-params"]:visible').first().click();
    await check("drawer: parameter menu of the stick");
    await page.keyboard.press("Escape");
    // The preset replaces the fixture's ls.right, so it asks first.
    await page
      .getByRole("complementary", { name: "按键编辑" })
      .getByRole("button", { name: "WASD", exact: true })
      .click();
    await expect(
      page.getByRole("dialog", { name: "改为 WASD？" }),
    ).toBeVisible();
    await check("dialog: 改为 WASD？");
    await closeDialog(page);
    await openControl(page, "south");
    // Sequence, mappings, the menus of the key.
    await page.getByTestId("add-step").click();
    await check("drawer: menu of 添加下一步");
    await page.getByRole("menuitem", { name: "等待" }).click();
    await page.getByTestId("add-step").click();
    await page.getByRole("menuitem", { name: "快捷键" }).click();
    await check("drawer: a sequence of a shortcut, a wait and a shortcut");
    await page.getByTestId("add-gesture").click();
    await check("drawer: menu of 添加映射");
    await page.keyboard.press("Escape");
    await page.getByTestId("key-actions").click();
    await check("drawer: the menu of the key");
    await page.getByRole("menuitem", { name: /清空/ }).click();
    await expect(
      page.getByRole("dialog", { name: "清空此按键？" }),
    ).toBeVisible();
    await check("dialog: 清空此按键？");
    await closeDialog(page);
    await page.getByRole("button", { name: "关闭按键编辑" }).click();

    // The top bar's menus and the dialogs they open.
    await page.getByTestId("profile-menu").click();
    await check("menu: profile");
    await page.keyboard.press("Escape");
    await page.getByTestId("more-menu").click();
    await check("menu: more");
    await page.keyboard.press("Escape");
    await choose(page, page.getByTestId("more-menu"), /^导出快捷键对照表/);
    await check("dialog: 导出快捷键对照表");
    await closeDialog(page);
    await choose(page, page.getByTestId("more-menu"), "查看所有按键");
    await check("dialog: 查看所有按键");
    await closeDialog(page);
    await choose(page, page.getByTestId("profile-menu"), /^配置设置/);
    await check("dialog: 配置设置");
    await closeDialog(page);
    await choose(page, page.getByTestId("profile-menu"), /^新建配置/);
    await check("dialog: 新建配置");
    await closeDialog(page);

    // Profiles.
    await nav(page, "配置").click();
    await expect(
      page.getByRole("article", { name: "Other profile" }),
    ).toBeVisible();
    await check("profiles");
    const card = page.getByRole("article", { name: "Other profile" });
    await card
      .getByRole("button", { name: "「Other profile」的更多操作" })
      .click();
    await check("profiles: menu of a card");
    await page.getByRole("menuitem", { name: "重命名…" }).click();
    await check("dialog: 重命名配置");
    await closeDialog(page);
    await choose(
      page,
      card.getByRole("button", { name: "「Other profile」的更多操作" }),
      "删除…",
    );
    await check("dialog: 删除配置");
    await closeDialog(page);
    await choose(
      page,
      card.getByRole("button", { name: "「Other profile」的更多操作" }),
      "配置设置…",
    );
    await check("dialog: 配置设置 of a card");
    await closeDialog(page);
    await page.getByRole("button", { name: "新建配置", exact: true }).click();
    await page
      .getByRole("dialog", { name: "新建配置" })
      .getByRole("textbox", { name: "名称" })
      .fill("Other profile");
    await expect(page.getByText("已有同名配置")).toBeVisible();
    await check("dialog: 新建配置 with a name that is taken");
    await closeDialog(page);
    await card.getByRole("button", { name: "添加软件" }).click();
    await check("profiles: menu of 添加软件");
    await page.getByRole("menuitem", { name: "从已安装的软件中选择…" }).click();
    await expect(page.getByRole("dialog", { name: "选择软件" })).toBeVisible();
    await check("dialog: 选择软件");
    await closeDialog(page);
    await card.getByRole("button", { name: "添加软件" }).click();
    await page.getByRole("menuitem", { name: "点选窗口…" }).click();
    await expect(page.getByRole("dialog", { name: "点选窗口" })).toBeVisible();
    await check("dialog: 点选窗口");
    await closeDialog(page);

    // Scripts.
    await nav(page, "脚本").click();
    await expect(
      page.getByRole("heading", { name: "脚本", level: 1 }),
    ).toBeVisible();
    await check("scripts");
    await page.getByTestId("script-menu").first().click();
    await check("scripts: menu of a script");
    await page.keyboard.press("Escape");
    await page.getByRole("button", { name: "新建", exact: true }).click();
    await check("dialog: 新建脚本");
    await closeDialog(page);

    // Settings, its dialogs, the logs and the diagnostics.
    await settings(page);
    await check("settings");
    await page.getByRole("button", { name: "关于与许可", exact: true }).click();
    await check("dialog: 关于与许可");
    await closeDialog(page);
    await page.getByRole("button", { name: "修改紧急暂停" }).click();
    await check("dialog: 修改紧急暂停");
    await closeDialog(page);
    await page
      .getByRole("button", { name: /^调校 / })
      .first()
      .click();
    await check("dialog: 调校");
    await closeDialog(page);
    // The mapping wizard (M25) opens for a controller SDL knows, as a remap.
    // The fake input has no raw events, so it shows what it does then.
    await page
      .getByRole("button", { name: /^更多操作 / })
      .first()
      .click();
    await page.getByRole("menuitem", { name: "重新映射…" }).click();
    await expect(page.getByRole("dialog", { name: "重新映射" })).toBeVisible();
    await check("dialog: 重新映射");
    await closeDialog(page);
    await rpc(f, "fake.press", { control: "south" });
    await rpc(f, "fake.release", { control: "south" });
    await page.getByRole("button", { name: "查看诊断", exact: true }).click();
    await expect(
      page.getByRole("heading", { name: "诊断", level: 1 }),
    ).toBeVisible();
    await check("diagnostics");
    await page.getByRole("button", { name: "返回设置" }).click();
    await page.getByRole("button", { name: "查看日志", exact: true }).click();
    await expect(
      page.getByRole("heading", { name: "日志", level: 1 }),
    ).toBeVisible();
    await check("logs");
    await page.getByRole("button", { name: "返回设置" }).click();

    // The same in English: the strings are other, the layout is too.
    await page
      .getByRole("combobox", { name: "语言", exact: true })
      .selectOption("en");
    await expect(
      page.getByRole("heading", { name: "Settings", level: 1 }),
    ).toBeVisible();
    await check("settings (English)");
    await nav(page, "Mapping").click();
    await openControl(page, "south");
    await check("drawer (English)");
  } finally {
    // What axe could not decide, for the person who reads the report.
    const undecided = [
      ...new Set(outcomes.flatMap((outcome) => outcome.incomplete)),
    ];
    await info.attach("axe-incomplete.json", {
      body: JSON.stringify(undecided, null, 2),
      contentType: "application/json",
    });
    await cleanup(f, app);
  }
});

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("axe finds nothing on the first-run page", async ({}, info) => {
  test.setTimeout(90000);
  const f = await fixture();
  const data = join(f.path, "data");
  // A new user, as in onboarding.spec.ts.
  await rm(join(data, "profiles", "paint"), { recursive: true, force: true });
  await rm(join(data, "state.json"), { force: true });
  await writeFile(join(data, "config.json"), '{"schemaVersion":0}');
  let app: ElectronApplication | undefined;
  try {
    const launched = await launch(f, async (electronApp, early) => {
      await setWindowSize(electronApp, 850, 620);
      await early.emulateMedia({ reducedMotion: "reduce" });
      // The page goes on by itself after ADVANCE_MS (1500): keep it, so the
      // scan has the time it needs. Nothing else of the window waits 1.5 s.
      await early.evaluate(() => {
        const real = window.setTimeout.bind(window);
        window.setTimeout = ((
          handler: TimerHandler,
          ms?: number,
          ...args: unknown[]
        ) =>
          ms === 1500
            ? 0
            : real(handler, ms, ...args)) as typeof window.setTimeout;
      });
    });
    app = launched.app;
    const page = launched.page;
    await expect(page.getByRole("dialog", { name: "连接手柄" })).toBeVisible({
      timeout: 15000,
    });
    const outcome = await axeCheck(page, "first run");
    await info.attach("axe-incomplete.json", {
      body: JSON.stringify(outcome.incomplete, null, 2),
      contentType: "application/json",
    });
  } finally {
    await cleanup(f, app);
  }
});

// ----- Forced colors ----------------------------------------------------------

const differ = async (a: Locator, b: Locator, what: string) =>
  expect(await styleSignature(a), what).not.toBe(await styleSignature(b));

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("under forced colors every state a person must tell apart still differs", async ({}, info) => {
  test.setTimeout(180000);
  const f = await editorFixture();
  let app: ElectronApplication | undefined;
  try {
    const launched = await launch(f);
    app = launched.app;
    const page = launched.page;
    await setWindowSize(app, 1180, 800);
    // Windows high contrast replaces every color: emulate it, and compare
    // the styles that are left. (axe's contrast check means nothing here.)
    await page.emulateMedia({
      forcedColors: "active",
      reducedMotion: "reduce",
    });
    expect(
      await page.evaluate(() => matchMedia("(forced-colors: active)").matches),
    ).toBe(true);
    await expect(chip(page, "south")).toBeVisible();

    // The rail: the current page against another.
    await differ(
      page.locator('nav button[aria-current="page"]'),
      page.locator("nav button:not([aria-current])").first(),
      "the current page in the rail",
    );

    // A chip that is selected, and one a pressed control lights.
    await openControl(page, "south");
    await differ(chip(page, "south"), chip(page, "east"), "the selected chip");
    await rpc(f, "fake.press", { control: "west" });
    await expect(chip(page, "west")).toHaveAttribute("data-active", "true");
    await differ(
      chip(page, "west"),
      chip(page, "north"),
      "the chip of a pressed control",
    );
    await rpc(f, "fake.release", { control: "west" });

    // The segmented control: the selected option against the others.
    const types = page.getByTestId("action-type");
    await differ(
      types.locator('[role="radio"][aria-checked="true"]').first(),
      types.locator('[role="radio"][aria-checked="false"]').first(),
      "the selected option of a segmented control",
    );

    // Buttons keep an edge, as the style says (none of them is a bare fill).
    for (const button of [
      page.getByRole("button", { name: "录入快捷键" }).first(),
      page.getByTestId("add-gesture"),
    ]) {
      const edge = await button.evaluate((element) => {
        const style = getComputedStyle(element);
        return (
          style.borderTopStyle !== "none" && style.borderTopWidth !== "0px"
        );
      });
      expect(edge, "a button has an edge").toBe(true);
    }
    // The dot that records is a color of its own: not the background's.
    const dot = page.locator(".drawer-rec-dot").first();
    await expect(dot).toBeVisible();
    expect(await styleSignature(dot)).not.toBe(
      await styleSignature(page.locator(".drawer-capture").first()),
    );

    // A menu item: the focused one against another.
    await page.getByTestId("profile-menu").click();
    const choices = page.locator('[role="menu"] button:not([disabled])');
    await expect(choices.first()).toBeFocused();
    await differ(choices.first(), choices.nth(1), "the focused menu item");
    await page.keyboard.press("Escape");

    // A trigger: its meter keeps an edge, its fill and marks a color.
    await openControl(page, "lefttrigger");
    await page.locator(".drawer-extra summary").first().click();
    const bar = page.locator(".analog-bar").first();
    await expect(bar).toBeVisible();
    await differ(
      page.locator(".analog-bar-fill").first(),
      bar,
      "the fill of the meter",
    );
    await differ(
      page.locator('.analog-bar-mark[data-kind="release"]').first(),
      page.locator('.analog-bar-mark[data-kind="full"]').first(),
      "the marks of the meter",
    );
    await page.getByRole("button", { name: "关闭按键编辑" }).click();

    // Profiles: the card in use against another.
    await nav(page, "配置").click();
    await differ(
      page.locator('.profile-card[data-active="true"]').first(),
      page.locator('.profile-card:not([data-active="true"])').first(),
      "the profile in use",
    );

    // Settings: a switch on against off, its thumb included; an input in focus.
    await settings(page);
    const toggle = page.locator('[role="switch"]:not([disabled])').first();
    await expect(toggle).toBeVisible();
    const before = await toggle.getAttribute("aria-checked");
    const off = [
      await styleSignature(toggle),
      await styleSignature(toggle, "::after"),
    ];
    await toggle.click();
    await expect(toggle).not.toHaveAttribute("aria-checked", before ?? "");
    const on = [
      await styleSignature(toggle),
      await styleSignature(toggle, "::after"),
    ];
    expect(on[0], "the track of a switch").not.toBe(off[0]);
    expect(on[1], "the thumb of a switch").not.toBe(off[1]);
    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-checked", before ?? "");
    const field = page.locator("input.input").first();
    await field.focus();
    const focused = await styleSignature(field);
    await field.blur();
    expect(focused, "a field in focus").not.toBe(await styleSignature(field));
    // The status dot is not the page's color.
    const statusDot = page.locator(".status-dot").first();
    expect(await styleSignature(statusDot)).not.toBe(
      await styleSignature(page.locator(".status-pill").first()),
    );
    await page.screenshot({
      path: info.outputPath("forced-colors-settings.png"),
    });
  } finally {
    await cleanup(f, app);
  }
});

// ----- The keyboard -------------------------------------------------------------

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("the keyboard reaches every control, never gets stuck, and shows where it is", async ({}) => {
  test.setTimeout(240000);
  const f = await editorFixture();
  let app: ElectronApplication | undefined;
  try {
    const launched = await launch(f);
    app = launched.app;
    const page = launched.page;
    await setWindowSize(app, 1180, 800);
    await expect(chip(page, "south")).toBeVisible();
    const walkPage = async (label: string) => {
      const walk = await walkTabs(page);
      expect(walk.missed, `${label}: controls Tab never reaches`).toEqual([]);
      expect(walk.wrapped, `${label}: Tab comes back to the start`).toBe(true);
      expect(
        walk.unseen,
        `${label}: stops where the focus cannot be seen`,
      ).toEqual([]);
      expect(walk.visited.length, `${label}: some control`).toBeGreaterThan(3);
    };
    await walkPage("mapping");
    await openControl(page, "south");
    await walkPage("mapping with the drawer");
    await page.getByRole("button", { name: "关闭按键编辑" }).click();
    for (const [name, heading] of [
      ["配置", "配置"],
      ["脚本", "脚本"],
      ["设置", "设置"],
    ] as const) {
      await nav(page, name).click();
      await expect(
        page.getByRole("heading", { name: heading, level: 1 }),
      ).toBeVisible();
      await walkPage(name);
    }

    // A dialog keeps the focus inside, Esc closes it and the focus goes back
    // to what opened it.
    const dialogs: [string, () => Promise<Locator>, string | RegExp][] = [
      [
        "关于与许可",
        async () =>
          page.getByRole("button", { name: "关于与许可", exact: true }),
        "关于与许可",
      ],
      [
        "修改紧急暂停",
        async () => page.getByRole("button", { name: "修改紧急暂停" }),
        "修改紧急暂停",
      ],
    ];
    await settings(page);
    for (const [label, opener, title] of dialogs) {
      const trigger = await opener();
      await trigger.focus();
      await trigger.press("Enter");
      await expect(page.getByRole("dialog", { name: title })).toBeVisible();
      const walk = await walkTabs(page, { inside: "dialog[open]" });
      expect(walk.leaked, `${label}: Tab leaves the dialog`).toEqual([]);
      expect(walk.wrapped, `${label}: Tab goes round inside the dialog`).toBe(
        true,
      );
      expect(walk.missed, `${label}: controls Tab never reaches`).toEqual([]);
      expect(walk.unseen, `${label}: focus cannot be seen`).toEqual([]);
      await page.keyboard.press("Escape");
      await expect(page.getByRole("dialog")).toHaveCount(0);
      await expect(trigger, `${label}: the focus goes back`).toBeFocused();
    }
    // A dialog that a menu opens gives the focus back to the menu's button.
    await nav(page, "映射").click();
    const profileMenu = page.getByTestId("profile-menu");
    await profileMenu.focus();
    await profileMenu.press("Enter");
    await page.getByRole("menuitem", { name: /^新建配置/ }).press("Enter");
    await expect(page.getByRole("dialog", { name: "新建配置" })).toBeVisible();
    const walk = await walkTabs(page, { inside: "dialog[open]" });
    expect(walk.leaked, "新建配置: Tab leaves the dialog").toEqual([]);
    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(profileMenu).toBeFocused();

    // A menu: the arrows, Home, End and Esc, and the end of it wraps round.
    await profileMenu.press("Enter");
    const choices = page.locator('[role="menu"] button:not([disabled])');
    const count = await choices.count();
    expect(count).toBeGreaterThan(2);
    await expect(choices.first()).toBeFocused();
    await page.keyboard.press("End");
    await expect(choices.nth(count - 1)).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await expect(choices.first()).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await expect(choices.nth(1)).toBeFocused();
    await page.keyboard.press("Home");
    await expect(choices.first()).toBeFocused();
    await page.keyboard.press("ArrowUp");
    await expect(choices.nth(count - 1)).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("menu")).toHaveCount(0);
    await expect(profileMenu).toBeFocused();
  } finally {
    await cleanup(f, app);
  }
});

// ----- Esc ends a capture -----------------------------------------------------------

/**
 * What the capture helper says, sent as Main would: it only watches the
 * keyboard, so a test must not press real keys (invariant 6). The window's
 * own Esc comes from the DevTools keyboard of the page, not from the system.
 */
async function helperSays(
  app: ElectronApplication,
  event: { chord: string; vk: number },
): Promise<void> {
  await app.evaluate(({ BrowserWindow }, data) => {
    const contents = BrowserWindow.getAllWindows().find(
      (w) => w.getTitle() !== "padmate overlay",
    )?.webContents;
    if (!contents) return;
    for (let session = 1; session <= 64; session++)
      contents.send("padmate:keyboardCapture", {
        active: true,
        session,
        ...data,
      });
  }, event);
}

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("holding Esc for a second cancels a capture and changes nothing", async ({}) => {
  test.setTimeout(90000);
  const f = await fixture();
  let app: ElectronApplication | undefined;
  try {
    const launched = await launch(f);
    app = launched.app;
    const page = launched.page;
    await openControl(page, "south");
    const field = page.getByRole("group", { name: "快捷键" });
    const before = await field.getAttribute("data-keys");
    await field.getByRole("button", { name: "录入快捷键" }).click();
    const busy = field.getByRole("button", { name: "录入中…", exact: true });
    await expect(busy).toBeVisible();
    await expect(page.getByText(/按住 Esc 1 秒/)).toBeVisible();
    // The Esc that is held: the helper hears it once (it drops repeats), the
    // window gets it as keys. A second Esc from the helper would be a second
    // key and end the capture, so it is sent again only when the first was
    // not taken.
    let sent = 0;
    await expect
      .poll(async () => {
        const keys = await field.getAttribute("data-keys");
        if (keys !== "Esc" && Date.now() - sent > 2000) {
          sent = Date.now();
          await helperSays(app as ElectronApplication, {
            chord: "Esc",
            vk: 27,
          });
        }
        return keys;
      })
      .toBe("Esc");
    await page.keyboard.down("Escape");
    // Not before a second, and the capture is still there.
    await page.waitForTimeout(600);
    await expect(busy).toBeVisible();
    await page.waitForTimeout(700);
    await expect(busy).toHaveCount(0);
    // The held press is no shortcut: nothing was recorded.
    await expect(field).toHaveAttribute("data-keys", before ?? "");
    // The Esc that is still down does not close the editor behind it.
    await page.keyboard.down("Escape");
    await page.keyboard.up("Escape");
    await expect(page.getByTestId("mapping-editor")).toBeVisible();
    await expect(
      field.getByRole("button", { name: "录入快捷键" }),
    ).toBeFocused();
  } finally {
    await cleanup(f, app);
  }
});

// ----- Zoom -------------------------------------------------------------------------

/** Zooms the window like Ctrl+Plus does; 1 puts it back. */
async function zoom(app: ElectronApplication, factor: number): Promise<void> {
  await app.evaluate(({ BrowserWindow }, value) => {
    BrowserWindow.getAllWindows()
      .find((w) => w.getTitle() !== "padmate overlay")
      ?.webContents.setZoomFactor(value);
  }, factor);
}

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("every page can be used at 150% and 200% zoom, in the smallest window too", async ({}) => {
  test.setTimeout(300000);
  const f = await editorFixture();
  let app: ElectronApplication | undefined;
  try {
    const launched = await launch(f);
    app = launched.app;
    const page = launched.page;
    await expect(chip(page, "south")).toBeVisible();
    const states: [string, () => Promise<void>][] = [
      ["mapping", async () => void (await nav(page, "映射").click())],
      [
        "mapping with the drawer",
        async () => {
          await nav(page, "映射").click();
          await openControl(page, "south");
        },
      ],
      ["profiles", async () => void (await nav(page, "配置").click())],
      ["scripts", async () => void (await nav(page, "脚本").click())],
      ["settings", async () => void (await nav(page, "设置").click())],
      [
        "logs",
        async () => {
          await nav(page, "设置").click();
          await page
            .getByRole("button", { name: "查看日志", exact: true })
            .click();
        },
      ],
      [
        "diagnostics",
        async () => {
          await nav(page, "设置").click();
          await page
            .getByRole("button", { name: "查看诊断", exact: true })
            .click();
        },
      ],
    ];
    for (const [width, height] of [
      [850, 620],
      [1180, 800],
    ] as const) {
      await setWindowSize(app, width, height);
      for (const factor of [1.5, 2]) {
        await zoom(app, factor);
        for (const [name, go] of states) {
          await go();
          await expect
            .poll(() => unreachableControls(page), {
              message: `${name} at ${factor * 100}% in ${width}x${height}: controls that cannot be reached`,
            })
            .toEqual([]);
        }
        // Dialogs scroll their own body: the ends of the dialog stay in view.
        await nav(page, "设置").click();
        await page.getByRole("button", { name: "修改紧急暂停" }).click();
        await expect
          .poll(() => unreachableControls(page), {
            message: `修改紧急暂停 at ${factor * 100}% in ${width}x${height}`,
          })
          .toEqual([]);
        await closeDialog(page);
        await nav(page, "映射").click();
        await page
          .getByTestId("mapping-editor")
          .waitFor({ state: "detached" })
          .catch(() => {});
      }
    }
  } finally {
    if (app) await zoom(app, 1).catch(() => {});
    await cleanup(f, app);
  }
});
