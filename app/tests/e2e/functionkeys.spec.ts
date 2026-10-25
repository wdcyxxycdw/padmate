import { writeFile } from "node:fs/promises";
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

// UI-30 and MAP-27 (ADR-0069): View (back) and Menu (start) are function keys.
// The fake pad is a generic one, so the pad prints Back and Start.

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

test("a function key's set is edited on the mapping page and used while the key is held; the next save drops an old binding on it", async () => {
  test.setTimeout(90000);
  const f = await editorFixture();
  // A file from before M20: Back pasted. The daemon ignores that binding and
  // warns; the editor drops it with its next save.
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
        },
      },
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

    // The switch names the pad's own keys; Back and Start are tags, not buttons.
    const switcher = page.getByRole("radiogroup", { name: "编辑的按键" });
    await expect(switcher.getByRole("radio")).toHaveText([
      "平时",
      "按住 Back",
      "按住 Start",
    ]);
    for (const control of ["back", "start"]) {
      const tag = page.locator(`[data-callout="${control}"]`);
      await expect(tag).toHaveText(/功能键/);
      expect(await tag.evaluate((element) => element.tagName)).toBe("DIV");
    }
    // The old binding is ignored, and the file says so until a save drops it.
    await expect(page.getByTestId("editor-notice")).toContainText("1 个警告");
    live.events.length = 0;
    await live.call("fake.press", { control: "back" });
    await live.call("fake.release", { control: "back" });
    await page.waitForTimeout(500);
    expect(chords(live)).toEqual([]);

    // Hold Back: a key the set leaves out shows what it does otherwise.
    await switcher.getByRole("radio", { name: "按住 Back" }).click();
    await expect(
      page.locator('button[data-callout="south"]'),
    ).toHaveAccessibleName(/ · 平时：Undo$/);
    await openControl(page, "east");
    await expect(page.getByTestId("drawer-view")).toHaveText("按住 Back");
    await expect(page.getByTestId("drawer-inherited")).toHaveText("平时：F20");
    // Setting it starts a binding of the set; the old one on Back goes with
    // this save, and nothing else of the file moves.
    await recordShortcut(
      app,
      page.getByRole("group", { name: "快捷键", exact: true }),
      "F21",
    );
    await expect
      .poll(async () => (await onDisk(live)).functionKeys?.back?.east?.press)
      .toMatchObject({ type: "keys", keys: "F21" });
    const saved = await onDisk(live);
    expect(saved.layers.base.back).toBeUndefined();
    expect(saved.layers.base.south).toEqual({
      label: "Undo",
      press: tap("F18"),
    });
    expect(saved.layers.base.east).toEqual({ press: tap("F20") });
    expect(Object.keys(saved)).toEqual([
      "schemaVersion",
      "name",
      "layers",
      "functionKeys",
    ]);
    await expect(page.getByTestId("editor-notice")).toHaveCount(0);
    await page.screenshot({
      path: test.info().outputPath("function-keys-drawer.png"),
    });

    // Held, Back switches East to the set; East alone is what it was.
    live.events.length = 0;
    await live.call("fake.press", { control: "back" });
    await live.call("fake.press", { control: "east" });
    await expect.poll(() => chords(live)).toContain("F21");
    await live.call("fake.release", { control: "east" });
    await live.call("fake.release", { control: "back" });
    live.events.length = 0;
    await live.call("fake.press", { control: "east" });
    await expect.poll(() => chords(live)).toContain("F20");
    await live.call("fake.release", { control: "east" });
    // Back itself types nothing, and South, which the set leaves out, is
    // what it always was while Back is held.
    live.events.length = 0;
    await live.call("fake.press", { control: "back" });
    await live.call("fake.press", { control: "south" });
    await expect.poll(() => chords(live)).toContain("F18");
    await live.call("fake.release", { control: "south" });
    await live.call("fake.release", { control: "back" });
    expect([...new Set(chords(live))]).toEqual(["F18"]);

    // The key list shows the set, and the function keys as tags.
    await page.getByTestId("more-menu").click();
    await page.getByRole("menuitem", { name: "查看所有按键" }).click();
    const list = page.getByRole("dialog", { name: "所有按键" });
    await expect(list).toContainText("Function keys · 按住 Back");
    await expect(list.getByTestId("function-key-row")).toHaveCount(2);
    await page.getByRole("button", { name: "关闭按键列表" }).click();

    // Clear the key from the set: the set and functionKeys go from the file.
    await openControl(page, "east");
    await page.getByTestId("key-actions").click();
    await page.getByRole("menuitem", { name: "清除这一套里的设置" }).click();
    await expect
      .poll(async () => "functionKeys" in (await onDisk(live)))
      .toBe(false);
    expect((await onDisk(live)).layers.base.east).toEqual({
      press: tap("F20"),
    });
    // The switch is view state: it is still on Back, and a reload of the
    // page's profile is the usual keys again.
    await expect(
      switcher.getByRole("radio", { name: "按住 Back" }),
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
