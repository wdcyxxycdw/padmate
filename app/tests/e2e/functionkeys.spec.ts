import { readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import {
  type ElectronApplication,
  expect,
  type Page,
  test,
} from "@playwright/test";
import type { ProfileDocument } from "../../src/shared/api";
import { editorFixture } from "./editor-fixture";
import { FixtureClient } from "./fixture-client";
import { cleanup, launch, recordShortcut } from "./helpers";

// UI-30 and MAP-27 (ADR-0075): one of View (back), Menu (start) and the Xbox
// button (guide) is the function key, Menu unless the settings choose another.
// The fake pad is a generic one, so the pad prints Back, Start and Home.

async function openControl(page: Page, control: string): Promise<void> {
  await page.locator(`button[data-callout="${control}"]`).click();
  await expect(page.getByTestId("mapping-editor")).toBeVisible();
}

/** profile.json of the paint profile, as the daemon has it on disk. */
async function onDisk(client: FixtureClient) {
  return JSON.parse(
    (await client.call<ProfileDocument>("profiles.get", { id: "paint" })).text,
  );
}

const tap = (keys: string) => ({ type: "keys", keys, mode: "tap" });

/** The chords the daemon says it would type since the events were cleared. */
function chords(client: FixtureClient): string[] {
  return client.events
    .filter((event) => event.topic === "output.preview")
    .map((event) => String(event.data.chord));
}

/** Presses the controls in turn and releases them in reverse. */
async function hold(client: FixtureClient, ...controls: string[]) {
  client.events.length = 0;
  for (const control of controls) await client.call("fake.press", { control });
}
async function letGo(client: FixtureClient, ...controls: string[]) {
  for (const control of [...controls].reverse())
    await client.call("fake.release", { control });
}

test("the function set of a file of the old format is edited on the mapping page and used while Menu is held; View and what is bound on Menu stay in the file", async () => {
  test.setTimeout(90000);
  const f = await editorFixture();
  // A file of v1.0.1: View's set of the old functionKeys, a binding on View
  // that the daemon ignored then and uses now (View is an ordinary key
  // unless the settings choose it), and one on Menu, which is the function
  // key and ignores it.
  await writeFile(
    join(f.path, "data", "profiles", "paint", "profile.json"),
    JSON.stringify({
      schemaVersion: 1,
      name: "Function keys",
      layers: {
        base: {
          south: { label: "Undo", press: tap("F18") },
          east: { press: tap("F20") },
          back: { label: "Paste", press: tap("Ctrl+V") },
          start: { label: "Save", press: tap("F22") },
        },
      },
      functionKeys: { back: { east: { press: tap("F21") } } },
    }),
  );
  let app: ElectronApplication | undefined;
  let client: FixtureClient | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    const page = opened.page;
    const live = await FixtureClient.connect(f);
    client = live;

    // The switch says 按住功能键, whichever key it is (ADR-0076). Start is a
    // tag, and View, an ordinary key, is a button of its own.
    const switcher = page.getByRole("radiogroup", { name: "编辑的按键" });
    await expect(switcher.getByRole("radio")).toHaveText([
      "平时",
      "按住功能键",
    ]);
    const tag = page.locator('[data-callout="start"]');
    await expect(tag).toHaveText(/功能键/);
    expect(await tag.evaluate((element) => element.tagName)).toBe("DIV");
    await expect(page.locator('button[data-callout="back"]')).toContainText(
      "Paste",
    );
    await expect(page.getByTestId("editor-notice")).toHaveCount(0);

    // View types its shortcut now; Menu, the function key, types nothing.
    await hold(live, "back");
    await expect
      .poll(() => chords(live).some((chord) => chord.endsWith("V")))
      .toBe(true);
    await letGo(live, "back");
    await hold(live, "start");
    await page.waitForTimeout(500);
    await letGo(live, "start");
    expect(chords(live)).toEqual([]);

    // The old set of View is the one set: held Menu, East is what it holds.
    await hold(live, "start", "east");
    await expect.poll(() => chords(live)).toContain("F21");
    await letGo(live, "east", "start");

    // Hold Menu on the page: a key the set leaves out shows what it does
    // otherwise.
    await switcher.getByRole("radio", { name: "按住功能键" }).click();
    await expect(
      page.locator('button[data-callout="south"]'),
    ).toHaveAccessibleName(/ · 平时：Undo$/);
    await openControl(page, "east");
    await expect(page.getByTestId("drawer-view")).toHaveText("按住功能键");
    // Setting it starts a binding of the set. This is the first save: the
    // old functionKeys becomes functionSet where it stood, and nothing else
    // of the file moves, View's and Menu's bindings included.
    await recordShortcut(
      app,
      page.getByRole("group", { name: "快捷键", exact: true }),
      "F23",
    );
    await expect
      .poll(async () => (await onDisk(live)).functionSet?.east?.press)
      .toMatchObject({ type: "keys", keys: "F23" });
    const saved = await onDisk(live);
    expect("functionKeys" in saved).toBe(false);
    expect(saved.layers.base.back).toEqual({
      label: "Paste",
      press: tap("Ctrl+V"),
    });
    expect(saved.layers.base.start).toEqual({
      label: "Save",
      press: tap("F22"),
    });
    expect(saved.layers.base.south).toEqual({
      label: "Undo",
      press: tap("F18"),
    });
    expect(saved.layers.base.east).toEqual({ press: tap("F20") });
    expect(Object.keys(saved)).toEqual([
      "schemaVersion",
      "name",
      "layers",
      "functionSet",
    ]);
    await expect(page.getByTestId("editor-notice")).toHaveCount(0);
    await page.screenshot({
      path: test.info().outputPath("function-keys-drawer.png"),
    });

    // Held, Menu switches East to the set; East alone is what it was.
    await hold(live, "start", "east");
    await expect.poll(() => chords(live)).toContain("F23");
    await letGo(live, "east", "start");
    await hold(live, "east");
    await expect.poll(() => chords(live)).toContain("F20");
    await letGo(live, "east");
    // South, which the set leaves out, is what it always was while Menu is
    // held, and Menu itself types nothing.
    await hold(live, "start", "south");
    await expect.poll(() => chords(live)).toContain("F18");
    await letGo(live, "south", "start");
    expect([...new Set(chords(live))]).toEqual(["F18"]);

    // The key list shows the set, the function key as a tag, and View, which
    // is an ordinary key, with its binding.
    await page.getByTestId("more-menu").click();
    await page.getByRole("menuitem", { name: "查看所有按键" }).click();
    const list = page.getByRole("dialog", { name: "所有按键" });
    await expect(list).toContainText("Function keys · 按住功能键");
    await expect(list.getByTestId("function-key-row")).toHaveCount(1);
    await page.getByRole("button", { name: "关闭按键列表" }).click();
    await switcher.getByRole("radio", { name: "平时" }).click();
    await page.getByTestId("more-menu").click();
    await page.getByRole("menuitem", { name: "查看所有按键" }).click();
    await expect(list).toContainText("Paste");
    await expect(list).not.toContainText("Save");
    await page.getByRole("button", { name: "关闭按键列表" }).click();

    // Clear the key from the set: the set goes from the file, View's binding
    // stays.
    await switcher.getByRole("radio", { name: "按住功能键" }).click();
    await openControl(page, "east");
    await page.getByTestId("key-actions").click();
    await page.getByRole("menuitem", { name: "清除这一套里的设置" }).click();
    await expect
      .poll(async () => "functionSet" in (await onDisk(live)))
      .toBe(false);
    const cleared = await onDisk(live);
    expect(cleared.layers.base.east).toEqual({ press: tap("F20") });
    expect(cleared.layers.base.back).toBeDefined();
    // The switch is view state: it is still on Menu, and a click on the
    // usual keys shows them again.
    await expect(
      switcher.getByRole("radio", { name: "按住功能键" }),
    ).toHaveAttribute("aria-checked", "true");
    await switcher.getByRole("radio", { name: "平时" }).click();
    await expect(
      page.locator('button[data-callout="south"]'),
    ).toHaveAccessibleName(/ · Undo$/);
  } finally {
    client?.close();
    await cleanup(f, app);
  }
});

test("the function key is chosen in Settings, followed by the daemon at once and named on the mapping page; the Xbox button asks for the Game Bar to be turned off", async () => {
  test.setTimeout(120000);
  const f = await editorFixture();
  await writeFile(
    join(f.path, "data", "profiles", "paint", "profile.json"),
    JSON.stringify({
      schemaVersion: 1,
      name: "Function keys",
      layers: {
        base: {
          south: { label: "Undo", press: tap("F18") },
          east: { press: tap("F20") },
          back: { label: "Paste", press: tap("Ctrl+V") },
          start: { label: "Save", press: tap("F22") },
          guide: { label: "Tool", press: tap("F24") },
        },
      },
      functionSet: { east: { press: tap("F21") } },
    }),
  );
  let app: ElectronApplication | undefined;
  let client: FixtureClient | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    const page = opened.page;
    const live = await FixtureClient.connect(f);
    client = live;
    const configFile = join(f.path, "data", "config.json");
    const chosen = async () =>
      JSON.parse(await readFile(configFile, "utf8")).functionKey;

    // The file says nothing, so Menu is the function key: View and the Xbox
    // button are ordinary keys, and Menu's own binding is ignored.
    await expect(
      page.getByRole("radiogroup", { name: "编辑的按键" }).getByRole("radio"),
    ).toHaveText(["平时", "按住功能键"]);
    await hold(live, "guide");
    await expect.poll(() => chords(live)).toContain("F24");
    await letGo(live, "guide");

    await page.getByRole("button", { name: "设置", exact: true }).click();
    const row = page.getByTestId("function-key-setting");
    const choice = row.getByRole("radiogroup", { name: "功能键" });
    const pick = (key: string) => choice.locator(`[data-value="${key}"]`);
    await expect(pick("start")).toHaveAttribute("aria-checked", "true");
    // The three keys are an icon and the name the pad prints (the fake pad is
    // a generic one), not text that says where they are.
    await expect(choice.getByRole("radio")).toHaveText([
      "Start",
      "Back",
      "Home",
    ]);
    await expect(choice.locator(".control-icon")).toHaveCount(3);
    await page.screenshot({
      path: test.info().outputPath("settings-function-key.png"),
    });

    // View: saved at once, and the daemon follows with no reload.
    await pick("back").click();
    await expect(page.locator(".page-autosave")).toHaveAttribute(
      "data-state",
      "saved",
    );
    await expect.poll(chosen).toBe("back");
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await hold(live, "back", "east");
    await expect.poll(() => chords(live)).toContain("F21");
    await letGo(live, "east", "back");
    // Menu is an ordinary key now, and types its binding.
    await hold(live, "start");
    await expect.poll(() => chords(live)).toContain("F22");
    await letGo(live, "start");
    // View itself types nothing.
    await hold(live, "back");
    await page.waitForTimeout(500);
    await letGo(live, "back");
    expect(chords(live)).toEqual([]);

    await page.getByRole("button", { name: "映射", exact: true }).click();
    const switcher = page.getByRole("radiogroup", { name: "编辑的按键" });
    await expect(switcher.getByRole("radio")).toHaveText([
      "平时",
      "按住功能键",
    ]);
    expect(
      await page
        .locator('[data-callout="back"]')
        .evaluate((element) => element.tagName),
    ).toBe("DIV");
    await expect(page.locator('button[data-callout="start"]')).toContainText(
      "Save",
    );

    // The Xbox button: saved, then the dialog asks for the Game Bar's own use
    // of the button to be turned off. 打开 Windows 设置 is not pressed: it
    // would open the real Settings of Windows.
    await page.getByRole("button", { name: "设置", exact: true }).click();
    await pick("guide").click();
    const dialog = page.getByRole("dialog", {
      name: "关闭 Xbox Game Bar 的手柄按钮",
    });
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText("Xbox Game Bar 也会打开");
    await expect(dialog).toContainText("允许控制器打开 Game Bar");
    await expect(
      dialog.getByRole("button", { name: "打开 Windows 设置" }),
    ).toBeVisible();
    await page.screenshot({
      path: test.info().outputPath("settings-game-bar-dialog.png"),
    });
    await dialog.getByRole("button", { name: "知道了" }).click();
    await expect(dialog).toHaveCount(0);
    await expect.poll(chosen).toBe("guide");
    // The function key was the Xbox button from the moment it was saved.
    await hold(live, "guide", "east");
    await expect.poll(() => chords(live)).toContain("F21");
    await letGo(live, "east", "guide");
    await hold(live, "guide");
    await page.waitForTimeout(500);
    await letGo(live, "guide");
    expect(chords(live)).toEqual([]);

    await page.getByRole("button", { name: "映射", exact: true }).click();
    await expect(switcher.getByRole("radio")).toHaveText([
      "平时",
      "按住功能键",
    ]);
    expect(
      await page
        .locator('[data-callout="guide"]')
        .evaluate((element) => element.tagName),
    ).toBe("DIV");
    await expect(page.locator('button[data-callout="back"]')).toContainText(
      "Paste",
    );

    // Menu again: the member goes from config.json.
    await page.getByRole("button", { name: "设置", exact: true }).click();
    await pick("start").click();
    await expect.poll(chosen).toBeUndefined();
    await expect(page.getByRole("dialog")).toHaveCount(0);
  } finally {
    client?.close();
    await cleanup(f, app);
  }
});
