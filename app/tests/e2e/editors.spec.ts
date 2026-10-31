import { readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import {
  type ElectronApplication,
  expect,
  type Page,
  test,
} from "@playwright/test";
import type { ProfileDocument, StatusResult } from "../../src/shared/api";
import { editorFixture } from "./editor-fixture";
import { FixtureClient } from "./fixture-client";
import {
  cleanup,
  closeApplication,
  closeWindow,
  launch,
  mainWindowVisible,
  recordShortcut,
  setWindowSize,
} from "./helpers";

async function openControl(page: Page, control: string): Promise<void> {
  await page.locator(`button[data-callout="${control}"]`).click();
  await expect(page.getByTestId("mapping-editor")).toBeVisible();
  await expect(
    page.getByRole("complementary", { name: "按键编辑" }),
  ).toBeVisible();
}

/** profile.json of the paint profile, as the daemon has it on disk. */
async function onDisk(client: FixtureClient) {
  return JSON.parse(
    (await client.call<ProfileDocument>("profiles.get", { id: "paint" })).text,
  );
}

async function expectPageFits(page: Page): Promise<void> {
  expect(
    await page.evaluate(() =>
      [
        document.documentElement,
        ...document.querySelectorAll(".main-content, .page-content"),
      ]
        .filter(
          (el) =>
            el.scrollWidth > el.clientWidth ||
            el.scrollHeight > el.clientHeight,
        )
        .map((el) => el.className || el.tagName),
    ),
  ).toEqual([]);
}

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("a key runs a script, ownership and IME warnings, real Markdown and PNG shortcut tables", async ({}, info) => {
  test.setTimeout(90000);
  const f = await editorFixture();
  let app: ElectronApplication | undefined;
  let client: FixtureClient | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    const page = opened.page;
    client = await FixtureClient.connect(f);
    await openControl(page, "north");
    await page
      .getByTestId("action-type")
      .getByRole("radio", { name: "脚本", exact: true })
      .click();
    // Picking a script is one edit, saved at once (UI-43).
    await page
      .getByRole("combobox", { name: "选择脚本", exact: true })
      .selectOption({ label: "下一个笔刷" });
    // The pick is one undo step (UI-43): Ctrl+Z puts the shortcut back and
    // Ctrl+Y the script, with the focus still on the list. A pad that
    // reports a button must not hold it back while its keys cannot reach
    // this window.
    const northPress = async () =>
      (await onDisk(client as FixtureClient)).layers.base.north?.press;
    const script = { type: "macro", file: "下一个笔刷.ts" };
    await expect.poll(northPress).toEqual(script);
    // A button of the pad is down and never released while undo is used.
    await client.call("fake.press", { control: "south" });
    await page.waitForTimeout(700);
    await page.keyboard.press("Control+Z");
    await expect
      .poll(northPress)
      .toEqual({ type: "keys", keys: "F13", mode: "tap" });
    await page.keyboard.press("Control+Y");
    await expect.poll(northPress).toEqual(script);
    await client.call("fake.release", { control: "south" });
    await page.getByRole("button", { name: "重命名此按键" }).click();
    const keyName = page.getByRole("textbox", {
      name: "按键名称",
      exact: true,
    });
    await keyName.fill("笔刷循环");
    await keyName.press("Enter");
    await expect
      .poll(async () => (await onDisk(client as FixtureClient)).controlNames)
      .toEqual({ north: "笔刷循环" });
    expect((await onDisk(client)).layers.base.north?.press).toEqual({
      type: "macro",
      file: "下一个笔刷.ts",
    });
    await expect(page.getByTestId("editor-notice")).toHaveCount(0);
    await expect(page.getByTestId("save-profile")).toHaveCount(0);
    client.events.length = 0;
    await client.call("fake.press", { control: "north" });
    await expect
      .poll(() =>
        client?.events.some(
          (e) => e.topic === "output.preview" && e.data.chord === "F16",
        ),
      )
      .toBe(true);
    await client.call("fake.release", { control: "north" });
    await page.screenshot({ path: info.outputPath("script-editor-zh.png") });
    await expectPageFits(page);

    await openControl(page, "east");
    await expect(
      page.getByTestId("mapping-editor").getByTestId("script-ownership"),
    ).toHaveText("S");
    await expect(page.getByText(/此按键同时有绑定和脚本处理/)).toBeVisible();
    await client.call("engine.releaseAll");
    client.events.length = 0;
    // Keep the first fresh press held across cold startup. It must remain
    // owned by the script, rather than falling through to declarative F20.
    await client.call("fake.press", { control: "east" });
    await expect
      .poll(() =>
        client?.events.some(
          (event) =>
            event.topic === "output.preview" && event.data.chord === "F21",
        ),
      )
      .toBe(true);
    expect(
      client.events.some(
        (event) =>
          event.topic === "output.preview" && event.data.chord === "F20",
      ),
    ).toBe(false);
    await client.call("fake.release", { control: "east" });
    await openControl(page, "south");
    const shortcut = page.getByRole("group", { name: "快捷键", exact: true });
    // A shortcut is recorded, never typed (OUT-18).
    await expect(
      page.getByTestId("mapping-editor").getByRole("textbox", {
        name: "快捷键",
      }),
    ).toHaveCount(0);
    await recordShortcut(app, shortcut, "A");
    // A bare letter is not warned about (F6); the field has no 自动 (F7).
    await expect(page.getByText(/输入法吃掉/)).toHaveCount(0);
    await expect(page.getByTestId("auto-shortcut")).toHaveCount(0);
    // The capture only reports it: the system's shortcut is never pressed.
    await recordShortcut(app, shortcut, "Win+L");
    await expect(page.getByTestId("reserved-warning")).toBeVisible();
    await recordShortcut(app, shortcut, "Ctrl+Z");
    await expect(page.getByTestId("reserved-warning")).toHaveCount(0);
    await openControl(page, "north");
    await app.evaluate(({ dialog }, dir) => {
      dialog.showSaveDialog = async (
        _window: unknown,
        options?: { defaultPath?: string },
      ) => ({
        canceled: false,
        filePath: `${dir}/${String(options?.defaultPath).endsWith(".png") ? "sheet.png" : "sheet.md"}`,
      });
    }, f.path);
    const exportSheet = async (format: string) => {
      await page.getByRole("button", { name: "更多", exact: true }).click();
      await page
        .getByRole("menuitem", { name: "导出快捷键对照表…", exact: true })
        .click();
      const dialog = page.getByRole("dialog", { name: "导出快捷键对照表" });
      await dialog.getByRole("radio", { name: format, exact: true }).click();
      await dialog.getByRole("button", { name: "导出", exact: true }).click();
    };
    await exportSheet("Markdown");
    await expect
      .poll(async () =>
        readFile(join(f.path, "sheet.md"), "utf8").catch(() => ""),
      )
      .toContain("Editor acceptance 快捷键对照表");
    const markdown = await readFile(join(f.path, "sheet.md"), "utf8");
    expect(markdown).toContain("| 手柄按键 | 快捷键 | 请在软件中设置为 |");
    // The latest edit is included; scripts are not shortcuts.
    expect(markdown).toContain("| Ctrl+Z | Undo（通常已是默认） |");
    expect(markdown).toContain("| F20 | （未命名） |");
    expect(markdown).not.toContain("下一个笔刷");
    await exportSheet("图片（PNG）");
    await expect
      .poll(
        async () =>
          (
            await readFile(join(f.path, "sheet.png")).catch(() =>
              Buffer.alloc(0),
            )
          ).length,
      )
      .toBeGreaterThan(1000);
    const png = await readFile(join(f.path, "sheet.png"));
    expect(png.subarray(0, 8).toString("hex")).toBe("89504e470d0a1a0a");
    await writeFile(info.outputPath("cheat-sheet.png"), png);
    await setWindowSize(app, 850, 620);
    await expectPageFits(page);
    await page.screenshot({ path: info.outputPath("editor-850.png") });
  } catch (error) {
    console.error("Engine status", await client?.call("status.get"));
    console.error(
      "Explain",
      JSON.stringify(
        await client?.call("profiles.explain", { control: "north" }),
      ),
    );
    console.error("Script status", await client?.call("script.status"));
    console.error(
      "Script log",
      await client?.call("script.logs", { limit: 10 }),
    );
    console.error(
      "Observed output",
      client?.events.filter((e) => e.topic === "output.preview"),
    );
    console.error(
      "Decisions",
      JSON.stringify(
        client?.events.filter(
          (e) =>
            e.topic === "input" ||
            (e.topic === "engine.activity" && e.data.kind === "decision") ||
            e.topic === "log",
        ),
      ),
    );
    throw error;
  } finally {
    client?.close();
    await cleanup(f, app);
  }
});

test("edits save at once: a capture without a key saves nothing, undo and redo save, a change elsewhere reloads, closing saves", async () => {
  const f = await editorFixture();
  let app: ElectronApplication | undefined;
  let client: FixtureClient | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    const page = opened.page;
    client = await FixtureClient.connect(f);
    const live = client;
    await openControl(page, "north");
    const original = await live.call<ProfileDocument>("profiles.get", {
      id: "paint",
    });
    const north = async () => (await onDisk(live)).layers.base.north?.press;
    const shortcut = page.getByRole("group", { name: "快捷键", exact: true });
    const chip = page.locator('button[data-callout="north"]');

    // A capture cancelled before any key (Esc held for a second) changes
    // nothing and saves nothing.
    await shortcut.getByRole("button", { name: "录入快捷键" }).click();
    await expect(
      shortcut.getByRole("button", { name: "录入中…" }),
    ).toBeVisible();
    await page.keyboard.down("Escape");
    await page.waitForTimeout(1200);
    await page.keyboard.up("Escape");
    await expect(shortcut.getByRole("button", { name: "录入中…" })).toHaveCount(
      0,
    );
    const notice = page.getByTestId("editor-notice");
    await expect(shortcut).toHaveAttribute("data-keys", "F13");
    await expect(notice).toHaveCount(0);
    expect(
      (await live.call<ProfileDocument>("profiles.get", { id: "paint" }))
        .revision,
    ).toBe(original.revision);

    // A recorded shortcut is saved at once; Ctrl+Z and Ctrl+Y save too.
    // The keys are pressed with focus on a chip, as a user would.
    await recordShortcut(app, shortcut, "F23");
    await expect.poll(north).toMatchObject({ keys: "F23" });
    await expect(notice).toHaveCount(0);
    await chip.focus();
    await page.keyboard.press("Control+Z");
    await expect.poll(north).toMatchObject({ keys: "F13" });
    await expect(shortcut).toHaveAttribute("data-keys", "F13");
    await page.keyboard.press("Control+Y");
    await expect.poll(north).toMatchObject({ keys: "F23" });
    // Each step waits for its save: F23 is already on disk before Ctrl+Z,
    // so polling only for it could pass while both saves are in flight,
    // and the external edit below would race them.
    await page.keyboard.press("Control+Z");
    await expect.poll(north).toMatchObject({ keys: "F13" });
    await page.keyboard.press("Control+Shift+Z");
    await expect.poll(north).toMatchObject({ keys: "F23" });
    await expect(shortcut).toHaveAttribute("data-keys", "F23");

    // A change made elsewhere is read at once, and its steps are not
    // undone: Ctrl+Z has nothing to take back.
    const current = await live.call<ProfileDocument>("profiles.get", {
      id: "paint",
    });
    const external = JSON.parse(current.text);
    external.name = "Externally edited";
    external.layers.base.north.press.keys = "F24";
    await live.call("profiles.put", {
      id: "paint",
      text: JSON.stringify(external),
      revision: current.revision,
    });
    await expect(shortcut).toHaveAttribute("data-keys", "F24");
    await expect(notice).toHaveCount(0);
    await chip.focus();
    await page.keyboard.press("Control+Z");
    await page.waitForTimeout(300);
    expect(await north()).toMatchObject({ keys: "F24" });
    expect((await onDisk(live)).name).toBe("Externally edited");
    // An old revision is still refused.
    const error = await page.evaluate(async (document) => {
      try {
        await window.padmate.putProfile(
          "paint",
          document.text,
          document.revision,
        );
        return { rejected: false, code: 0 };
      } catch (e) {
        return { rejected: true, code: (e as { code?: number }).code };
      }
    }, original);
    expect(error).toEqual({ rejected: true, code: -32009 });

    // Closing the window saves the field being typed in, without asking;
    // the window only hides (ADR-0077).
    const label = page.getByRole("textbox", {
      name: "功能",
      exact: true,
    });
    await label.fill("关窗前");
    await closeWindow(app);
    await expect
      .poll(async () => (await onDisk(live)).layers.base.north?.label)
      .toBe("关窗前");
    expect(await mainWindowVisible(app)).toBe(false);
    // Quitting does the same; closeApplication quits and waits for the
    // process; it also detaches Playwright's inspector, without which the
    // "close" event never fires.
    await page.bringToFront();
    await app.evaluate(({ BrowserWindow }) => {
      BrowserWindow.getAllWindows()
        .find((w) => w.getTitle() !== "padmate overlay")
        ?.show();
    });
    await label.fill("退出前");
    await closeApplication(app);
    expect((await onDisk(live)).layers.base.north?.label).toBe("退出前");
  } finally {
    client?.close();
    await cleanup(f, app);
  }
});

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("UI deadzone save is applied to fake input within 100 ms", async ({}, info) => {
  const f = await editorFixture();
  let app: ElectronApplication | undefined;
  let client: FixtureClient | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    const page = opened.page;
    client = await FixtureClient.connect(f);
    await openControl(page, "leftstick");
    await client.call("fake.axis", { axis: "leftx", value: 16384 });
    await expect
      .poll(() => client?.events.some((e) => e.topic === "input"))
      .toBe(true);
    // A barrier through the mapping controller proves the baseline was consumed.
    await client.call("fake.press", { control: "west" });
    await expect
      .poll(() =>
        client?.events.some(
          (e) => e.topic === "output.preview" && e.data.chord === "F19",
        ),
      )
      .toBe(true);
    expect(
      client.events.some(
        (e) => e.topic === "output.preview" && e.data.chord === "F18",
      ),
    ).toBe(false);
    await client.call("fake.release", { control: "west" });
    await client.call("fake.axis", { axis: "leftx", value: 0 });
    const deadzone = page.getByRole("spinbutton", {
      name: "死区",
      exact: true,
    });
    await deadzone.fill("15");
    let savedAt = 0;
    let sent: Promise<unknown> | undefined;
    const live = client;
    live.events.length = 0;
    live.onEvent = (event) => {
      if (
        event.topic === "profile.changed" &&
        event.data.kind === "saved" &&
        event.data.applied === true &&
        !savedAt
      ) {
        savedAt = event.at;
        sent = live.call("fake.axis", { axis: "leftx", value: 16384 });
      }
    };
    // Enter commits the value, which is saved and applied at once.
    await deadzone.press("Enter");
    await expect
      .poll(() =>
        live.events.some(
          (e) => e.topic === "output.preview" && e.data.chord === "F18",
        ),
      )
      .toBe(true);
    await sent;
    const output = live.events.find(
      (e) => e.topic === "output.preview" && e.data.chord === "F18",
    );
    expect(savedAt).toBeGreaterThan(0);
    const elapsed = (output?.at ?? Infinity) - savedAt;
    expect(elapsed).toBeGreaterThanOrEqual(0);
    expect(elapsed).toBeLessThan(100);
    await info.attach("deadzone-apply-latency", {
      body: JSON.stringify({ elapsedMs: elapsed, deadzone: 0.15 }),
      contentType: "application/json",
    });
    expect(
      JSON.parse(
        (await live.call<ProfileDocument>("profiles.get", { id: "paint" }))
          .text,
      ).analog.ls.deadzone,
    ).toBe(0.15);
    await live.call("fake.axis", { axis: "leftx", value: 0 });
    await setWindowSize(app, 850, 620);
    await expectPageFits(page);
    await page.screenshot({ path: info.outputPath("analog-editor-850.png") });
  } finally {
    client?.close();
    await cleanup(f, app);
  }
});

test("the mouse is recorded as a key inside the window, and a click or a notch of the wheel ends the capture (ADR-0076)", async () => {
  const f = await editorFixture();
  let app: ElectronApplication | undefined;
  let client: FixtureClient | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    const page = opened.page;
    client = await FixtureClient.connect(f);
    const live = client;
    await openControl(page, "north");
    const north = async () => (await onDisk(live)).layers.base.north?.press;
    const shortcut = page.getByRole("group", { name: "快捷键", exact: true });
    const busy = shortcut.getByRole("button", { name: "录入中…" });
    const start = shortcut.getByRole("button", { name: "录入快捷键" });
    // Nothing in the page that a click or the wheel could use.
    const title = page.getByTestId("mapping-editor").locator(".drawer-title");

    // A click on the field starts a capture, and is not recorded; there is no
    // 鼠标 menu and no button to stop.
    await expect(page.getByTestId("shortcut-mouse")).toHaveCount(0);
    await shortcut.locator(".drawer-capture-keys").click();
    await expect(busy).toBeVisible();
    await expect(shortcut).toHaveAttribute("data-keys", "F13");
    await expect(page.getByRole("button", { name: "停止捕获" })).toHaveCount(0);

    // A click elsewhere in the window does not cancel: it is the left button.
    await title.click();
    await expect(busy).toHaveCount(0);
    await expect(shortcut).toHaveAttribute("data-keys", "MouseLeft");
    await expect.poll(north).toMatchObject({ keys: "MouseLeft" });

    // The right button too, and no menu comes up for it.
    await start.click();
    await expect(busy).toBeVisible();
    await title.click({ button: "right" });
    await expect(busy).toHaveCount(0);
    await expect(shortcut).toHaveAttribute("data-keys", "MouseRight");
    await expect.poll(north).toMatchObject({ keys: "MouseRight" });
    await expect(page.getByRole("menu")).toHaveCount(0);

    // One notch of the wheel is the key, with the modifiers held.
    await start.click();
    await expect(busy).toBeVisible();
    await title.hover();
    await page.keyboard.down("Control");
    await page.mouse.wheel(0, -120);
    await page.keyboard.up("Control");
    await expect(busy).toHaveCount(0);
    await expect(shortcut).toHaveAttribute("data-keys", "Ctrl+WheelUp");
    await expect.poll(north).toMatchObject({ keys: "Ctrl+WheelUp" });
  } finally {
    client?.close();
    await cleanup(f, app);
  }
});

test("native capture blocks mapping and blur releases only its owner without replaying held input", async () => {
  const f = await editorFixture();
  let app: ElectronApplication | undefined;
  let client: FixtureClient | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    const page = opened.page;
    client = await FixtureClient.connect(f);
    await openControl(page, "north");
    // Windows may refuse programmatic foreground reactivation after blur;
    // Playwright clicks are DOM input and do not grant a native focus lease.
    // Verify the explicit stop/manual pause path while the initial foreground
    // activation is still valid, then exercise blur as the final focus change.
    await client.call("engine.pause");
    await page.getByRole("button", { name: "录入快捷键", exact: true }).click();
    await expect
      .poll(
        async () => (await client?.call<StatusResult>("status.get"))?.capturing,
      )
      .toBe(true);
    // Esc held for a second cancels the capture, and lets go of its lease.
    await page.keyboard.down("Escape");
    await page.waitForTimeout(1200);
    await page.keyboard.up("Escape");
    await expect
      .poll(
        async () => (await client?.call<StatusResult>("status.get"))?.capturing,
      )
      .toBe(false);
    expect((await client.call<StatusResult>("status.get")).paused).toBe(true);
    await client.call("engine.resume");
    await page.getByRole("button", { name: "录入快捷键", exact: true }).click();
    await expect
      .poll(
        async () => (await client?.call<StatusResult>("status.get"))?.capturing,
      )
      .toBe(true);
    client.events.length = 0;
    await client.call("fake.press", { control: "west" });
    await expect
      .poll(() => client?.events.some((e) => e.topic === "input"))
      .toBe(true);
    await app.evaluate(({ BrowserWindow }) =>
      BrowserWindow.getAllWindows()
        .find((w) => w.getTitle() !== "padmate overlay")
        ?.blur(),
    );
    await expect
      .poll(
        async () => (await client?.call<StatusResult>("status.get"))?.capturing,
      )
      .toBe(false);
    expect(client.events.filter((e) => e.topic === "output.preview")).toEqual(
      [],
    );
    await client.call("fake.release", { control: "west" });
    await client.call("fake.press", { control: "west" });
    await client.call("fake.release", { control: "west" });
    await expect
      .poll(
        () =>
          client?.events.filter(
            (e) => e.topic === "output.preview" && e.data.chord === "F19",
          ).length,
      )
      .toBe(1);
  } catch (error) {
    console.error(
      "Capture UI",
      await app?.firstWindow().then((p) => p.locator("body").innerText()),
    );
    throw error;
  } finally {
    client?.close();
    await cleanup(f, app);
  }
});

test("single-layer editing keeps the rest of a layered file and locks profiles that start elsewhere", async () => {
  const f = await editorFixture();
  const layered = {
    schemaVersion: 0,
    name: "Layered file",
    defaultLayer: "base",
    // The four switches that moved to config.json (ADR-0067) and the members
    // that stayed in the profile: an edit of the mapping drops the first and
    // rewrites none of the others (UI-27).
    feedback: {
      rumble: true,
      led: false,
      pause: false,
      lowBattery: false,
      shortMs: 90,
      strength: 12000,
      pausedColor: "#3366FF",
    },
    common: {
      rightstick: {
        label: "cycle",
        press: { type: "layer", op: "cycle", layers: ["base", "alt"] },
      },
    },
    layers: {
      base: {
        south: { label: "Undo", press: { type: "keys", keys: "Ctrl+Z" } },
        leftshoulder: {
          tap: { type: "keys", keys: "F13" },
          hold: { type: "layer", op: "hold", layer: "alt" },
        },
      },
      alt: {
        color: "#3366FF",
        south: { press: { type: "keys", keys: "B" } },
        west: { block: true },
      },
    },
  };
  await writeFile(
    join(f.path, "data", "profiles", "paint", "profile.json"),
    JSON.stringify(layered),
  );
  let app: ElectronApplication | undefined;
  let client: FixtureClient | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    const page = opened.page;
    client = await FixtureClient.connect(f);
    await openControl(page, "south");
    const drawer = page.getByTestId("mapping-editor");
    await expect(drawer).toContainText("组按键");
    expect(await page.locator("body").innerText()).not.toMatch(/层/);
    await recordShortcut(
      app,
      page.getByRole("group", { name: "快捷键", exact: true }),
      "F22",
    );
    const live = client;
    await expect
      .poll(async () => (await onDisk(live)).layers.base.south?.press?.keys)
      .toBe("F22");
    const saved = await onDisk(live);
    expect(saved.layers.base.south.press.keys).toBe("F22");
    // The file was version 0; the edit saves it at the current version.
    expect(saved.schemaVersion).toBe(1);
    expect(saved.layers.alt).toEqual(layered.layers.alt);
    expect(saved.common).toEqual(layered.common);
    expect(saved.layers.base.leftshoulder).toEqual(
      layered.layers.base.leftshoulder,
    );
    expect(saved.defaultLayer).toBe("base");
    // The save leaves out the retired switches, and nothing else of feedback.
    expect(saved.feedback).toEqual({
      shortMs: 90,
      strength: 12000,
      pausedColor: "#3366FF",
    });

    const current = await client.call<ProfileDocument>("profiles.get", {
      id: "paint",
    });
    await client.call("profiles.put", {
      id: "paint",
      text: JSON.stringify({ ...saved, defaultLayer: "alt" }),
      revision: current.revision,
    });
    await expect(
      page
        .getByRole("group", { name: "快捷键", exact: true })
        .getByRole("button", { name: "录入快捷键", exact: true }),
    ).toBeDisabled();
  } finally {
    client?.close();
    await cleanup(f, app);
  }
});

test("menus near the bottom of the drawer open inside the visible drawer", async () => {
  const f = await editorFixture();
  let app: ElectronApplication | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    const page = opened.page;
    await openControl(page, "leftstick");
    await page.locator('.analog-cell[data-direction="up"]').click();
    const card = page.locator(".analog-card").first();
    await card.getByTestId("add-gesture").click();
    const menu = page.getByRole("menu", { name: "添加其他动作" });
    await expect(menu).toBeVisible();
    const body = await page.locator(".drawer-body").boundingBox();
    const items = await menu
      .getByRole("menuitem")
      .evaluateAll((elements) =>
        elements.map((element) => element.getBoundingClientRect().toJSON()),
      );
    expect(body).not.toBeNull();
    expect(items.length).toBeGreaterThan(0);
    for (const item of items) {
      expect(item.top).toBeGreaterThanOrEqual((body?.y ?? 0) - 1);
      expect(item.bottom).toBeLessThanOrEqual(
        (body?.y ?? 0) + (body?.height ?? 0) + 1,
      );
    }
  } finally {
    await cleanup(f, app);
  }
});

// MAP-25: 触感 under 高级 in the key drawer is saved as the binding's haptic,
// and the daemon asks the controller for it once for each press of the key.
// The fake input daemon has no motors: fake.effects lists what it was asked
// for, an Event-class program of the preset (a "play" call).
interface FakePlay {
  effects: {
    kind: string;
    device: number;
    class?: string;
    preset?: string;
    strength?: number;
  }[];
}

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("触感 in the key drawer is saved, and a press of the key plays it once on the fake controller", async ({}, info) => {
  test.setTimeout(90000);
  const f = await editorFixture();
  let app: ElectronApplication | undefined;
  let client: FixtureClient | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    const page = opened.page;
    client = await FixtureClient.connect(f);
    const connected = client;
    // One press and release of north, and the haptics the controller was
    // asked for meanwhile.
    const plays = async () => {
      await connected.call("fake.effects", { clear: true });
      await connected.call("fake.press", { control: "north" });
      await connected.call("fake.release", { control: "north" });
      await page.waitForTimeout(400);
      return (
        (await connected.call("fake.effects")) as FakePlay
      ).effects.filter((call) => call.kind === "play");
    };
    const haptic = async () =>
      (await onDisk(connected)).layers.base.north?.haptic;

    // The key has none: a press asks for nothing.
    expect(await plays()).toEqual([]);
    await openControl(page, "north");
    const drawer = page.getByRole("complementary", { name: "按键编辑" });
    // The summary is "高级" and the list of what it holds, in one element.
    await drawer.locator("summary").filter({ hasText: /^高级/ }).click();
    const select = drawer.getByRole("combobox", { name: "触感", exact: true });
    await expect(select).toHaveValue("");
    await select.selectOption({ label: "轻点" });
    // The pick is one edit, saved at once (UI-43), and the daemon reloads it.
    await expect.poll(haptic).toBe("tick");
    await expect(drawer.getByText("已设置：触感")).toBeVisible();
    await page.screenshot({ path: info.outputPath("key-haptic.png") });
    // Until the reload is in, a press may ask for nothing: wait for the first
    // haptic, then a press asks for exactly one.
    await expect.poll(async () => (await plays()).length).toBe(1);
    const [call] = await plays();
    expect(await plays()).toHaveLength(1);
    expect(call).toMatchObject({ device: 1, class: "event", preset: "tick" });
    // The rest of the key is as it was.
    expect((await onDisk(connected)).layers.base.north).toEqual({
      haptic: "tick",
      press: { type: "keys", keys: "F13", mode: "tap" },
    });

    // 无 takes the member out of the file, and the key goes quiet again.
    await select.selectOption({ label: "无" });
    await expect.poll(haptic).toBeUndefined();
    expect((await onDisk(connected)).layers.base.north).toEqual({
      press: { type: "keys", keys: "F13", mode: "tap" },
    });
    await expect.poll(async () => (await plays()).length).toBe(0);
  } finally {
    client?.close();
    await cleanup(f, app);
  }
});

// UI-27 (ADR-0067): the four switches of Settings · 反馈 are saved as
// config.json's feedback, and the daemon follows them as soon as the file is
// saved, with no reload of the profile. The fake input daemon has no motors or
// lights: fake.effects lists the ambient state it was asked for, with the pulse
// and the color of the pause, and the mode the player lights show (playerMode);
// a low battery (fake.battery) is a play call of the Alert class.
interface FakeFeedback {
  effects: {
    kind: string;
    device: number;
    paused?: boolean;
    pulse?: boolean;
    color?: string;
    playerMode?: number;
    class?: string;
    preset?: string;
    blink?: boolean;
  }[];
}

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("the 反馈 switches in Settings are saved, and the fake controller follows them", async ({}, info) => {
  test.setTimeout(120000);
  const f = await editorFixture();
  let app: ElectronApplication | undefined;
  let client: FixtureClient | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    const page = opened.page;
    client = await FixtureClient.connect(f);
    const connected = client;
    // config.json of the fixture's data directory: the switches live there.
    const configFile = join(f.path, "data", "config.json");
    const feedback = async () =>
      JSON.parse(await readFile(configFile, "utf8")).feedback;
    const asked = async () =>
      ((await connected.call("fake.effects")) as FakeFeedback).effects.filter(
        (call) => call.kind === "feedback",
      );
    // What a pause asks of the controller: the call that says it is paused.
    const pause = async () => {
      await connected.call("fake.effects", { clear: true });
      await connected.call("engine.pause");
      let call: FakeFeedback["effects"][number] | undefined;
      await expect
        .poll(async () => {
          call = (await asked()).find((c) => c.paused);
          return !!call;
        })
        .toBe(true);
      await connected.call("engine.resume");
      return { pulse: !!call?.pulse, color: call?.color ?? "" };
    };

    await page.getByRole("button", { name: "设置", exact: true }).click();
    const section = page.getByTestId("feedback-settings");
    await expect(section).toBeVisible();
    await expect(section.getByRole("heading", { name: "反馈" })).toBeVisible();
    const toggle = (name: string) =>
      section.getByRole("switch", { name, exact: true });
    for (const name of ["震动", "灯光", "暂停提示", "低电提醒"])
      await expect(toggle(name)).toHaveAttribute("aria-checked", "true");
    await page.screenshot({
      path: info.outputPath("settings-feedback.png"),
    });
    // The page says "saved" once config.put has answered, and the daemon has
    // applied the switch by then: no reload of the profile is waited for.
    const flip = async (name: string) => {
      await toggle(name).click();
      await expect(page.locator(".page-autosave")).toHaveAttribute(
        "data-state",
        "saved",
      );
    };
    // The profile in use is not rewritten by any of this.
    const profileBefore = JSON.stringify(await onDisk(connected));

    // The default: the lights show the mode (base is 1), and a pause pulses
    // and shows the pause color.
    expect((await asked()).at(-1)?.playerMode).toBe(1);
    expect(await pause()).toEqual({ pulse: true, color: "#FFAA00" });

    // 灯光: the colors and the player lights go, and come back with the switch.
    await flip("灯光");
    await expect(toggle("灯光")).toHaveAttribute("aria-checked", "false");
    await expect.poll(feedback).toEqual({ led: false });
    await expect
      .poll(async () => (await asked()).at(-1)?.playerMode ?? 0)
      .toBe(0);
    expect((await pause()).color).toBe("");
    await flip("灯光");
    await expect.poll(feedback).toBeUndefined();
    await expect.poll(async () => (await asked()).at(-1)?.playerMode).toBe(1);

    // 暂停提示: a pause shows nothing, and the pulse of a layer change is not
    // this switch's.
    await flip("暂停提示");
    await expect.poll(feedback).toEqual({ pause: false });
    await expect.poll(async () => (await pause()).pulse).toBe(false);
    expect((await pause()).color).toBe("");
    await flip("暂停提示");
    await expect.poll(feedback).toBeUndefined();
    await expect.poll(pause).toEqual({ pulse: true, color: "#FFAA00" });

    // 震动: no pulse, and the pause color stays.
    await flip("震动");
    await expect.poll(feedback).toEqual({ rumble: false });
    await expect.poll(pause).toEqual({ pulse: false, color: "#FFAA00" });
    await flip("震动");
    await expect.poll(feedback).toBeUndefined();
    await expect.poll(pause).toEqual({ pulse: true, color: "#FFAA00" });

    // 低电提醒: with it off, a drop to 15% asks nothing of the controller, and
    // the episode is spent; charged, and with it on again, the next drop asks
    // for the Alert-class pulses and the flash, once.
    const alerts = async () =>
      ((await connected.call("fake.effects")) as FakeFeedback).effects.filter(
        (call) => call.kind === "play" && call.class === "alert",
      );
    await connected.call("fake.effects", { clear: true });
    await flip("低电提醒");
    await expect.poll(feedback).toEqual({ lowBattery: false });
    await connected.call("fake.battery", { state: "discharging", percent: 15 });
    await connected.call("fake.battery", { state: "charging", percent: 50 });
    await flip("低电提醒");
    await expect.poll(feedback).toBeUndefined();
    await connected.call("fake.battery", { state: "discharging", percent: 15 });
    await expect.poll(async () => (await alerts()).length).toBe(1);
    expect((await alerts())[0]).toMatchObject({
      device: 1,
      preset: "pulse3",
      blink: true,
    });

    // All four on again: config.json has no feedback, and the profile was not
    // touched at all (it was never saved by these switches).
    expect(JSON.parse(await readFile(configFile, "utf8"))).not.toHaveProperty(
      "feedback",
    );
    expect(JSON.stringify(await onDisk(connected))).toBe(profileBefore);
    // The mapping's 配置设置 has no switch any more.
    await page.getByRole("button", { name: "映射", exact: true }).click();
    await page.getByTestId("profile-menu").click();
    await page.getByTestId("profile-settings").click();
    const dialog = page.getByRole("dialog", { name: "配置设置" });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole("switch")).toHaveCount(0);
    await expect(dialog.getByRole("heading", { name: "反馈" })).toHaveCount(0);
    await dialog.getByRole("button", { name: "完成" }).click();
    await expect(dialog).toBeHidden();
  } finally {
    client?.close();
    await cleanup(f, app);
  }
});

// UI-27: every card of the 配置 page has 配置设置… as the first item of its
// menu. It opens the dialog for that card's profile without activating it or
// leaving the page, and the changes are saved to that profile only.
// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("配置设置… on a profile card edits that profile and nothing else", async ({}, info) => {
  test.setTimeout(120000);
  const f = await editorFixture();
  let app: ElectronApplication | undefined;
  let client: FixtureClient | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    const page = opened.page;
    client = await FixtureClient.connect(f);
    const connected = client;
    const inUse = async () =>
      ((await connected.call("status.get")) as StatusResult).profileId;
    expect(await inUse()).toBe("paint");
    const defaultText = async () =>
      JSON.parse(
        (
          await connected.call<ProfileDocument>("profiles.get", {
            id: "default",
          })
        ).text,
      );

    await page.getByRole("button", { name: "配置", exact: true }).click();
    const card = page.getByRole("article", { name: "默认" });
    await expect(card).toBeVisible();
    await card.getByRole("button", { name: "「默认」的更多操作" }).click();
    const items = page.getByRole("menuitem");
    await expect(items.first()).toHaveText("配置设置…");
    await page.screenshot({ path: info.outputPath("card-menu.png") });
    await items.first().click();
    const dialog = page.getByRole("dialog", { name: "配置设置" });
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText("仅影响配置「默认」。");
    // Only the name and the timing.
    await expect(dialog.getByRole("switch")).toHaveCount(0);
    const hold = dialog.getByRole("textbox", { name: "触发长按时间" });
    await hold.fill("321");
    await hold.press("Enter");
    await expect
      .poll(async () => (await defaultText()).timing)
      .toEqual({
        holdMs: 321,
      });
    await page.screenshot({
      path: info.outputPath("card-profile-settings.png"),
    });
    await dialog.getByRole("button", { name: "完成" }).click();
    await expect(dialog).toBeHidden();
    // Still on the 配置 page, and the profile in use is the one it was.
    await expect(
      page.getByRole("heading", { name: "配置", level: 1 }),
    ).toBeVisible();
    expect(await inUse()).toBe("paint");
    expect(await onDisk(connected)).not.toHaveProperty("timing");

    // The profile in use is edited with the mapping page's own editor.
    const paint = page.getByRole("article", { name: "Editor acceptance" });
    await paint
      .getByRole("button", { name: "「Editor acceptance」的更多操作" })
      .click();
    await page.getByRole("menuitem", { name: "配置设置…" }).click();
    await expect(dialog).toContainText("仅影响配置「Editor acceptance」。");
    const name = dialog.getByRole("textbox", { name: "名称" });
    await name.fill("Renamed on the card");
    await name.press("Enter");
    await expect
      .poll(async () => (await onDisk(connected)).name)
      .toBe("Renamed on the card");
    await dialog.getByRole("button", { name: "完成" }).click();
    await expect(dialog).toBeHidden();
    await expect(
      page.getByRole("article", { name: "Renamed on the card" }),
    ).toBeVisible();
    expect((await defaultText()).timing).toEqual({ holdMs: 321 });
    expect(await inUse()).toBe("paint");
  } finally {
    client?.close();
    await cleanup(f, app);
  }
});
