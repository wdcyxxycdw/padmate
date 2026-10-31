import {
  type ElectronApplication,
  expect,
  type Page,
  test,
} from "@playwright/test";
import type { ProfileDocument } from "../../src/shared/api";
import { FixtureClient } from "./fixture-client";
import { cleanup, fixture, launch, recordShortcut } from "./helpers";

// UI-31, MAP-22, OUT-20 (ADR-0070, ADR-0076): the steps of a sequence (a
// shortcut or a wait) and the media keys, in the key drawer. The fake pad is a
// standard one: south is printed S. The fixture's south taps Ctrl+Z.

async function openControl(page: Page, control: string): Promise<void> {
  await page.locator(`button[data-callout="${control}"]`).click();
  await expect(page.getByTestId("mapping-editor")).toBeVisible();
}

/** profile.json of a profile of the library, as the daemon has it on disk. */
async function onDisk(client: FixtureClient, id = "paint") {
  return JSON.parse(
    (await client.call<ProfileDocument>("profiles.get", { id })).text,
  );
}

/** What the engine decided to do since the events were cleared, press by press. */
function decisions(client: FixtureClient): string[] {
  return client.events
    .filter(
      (event) =>
        event.topic === "engine.activity" &&
        event.data.kind === "decision" &&
        (event.data.decision as { reason?: string } | undefined)?.reason ===
          "dispatch",
    )
    .map((event) => String((event.data.decision as { action: string }).action));
}

/** The chords the daemon says it would type since the events were cleared. */
function chords(client: FixtureClient): string[] {
  return client.events
    .filter((event) => event.topic === "output.preview")
    .map((event) => String(event.data.chord));
}

async function addStep(page: Page, kind: "快捷键" | "等待") {
  await page.getByRole("button", { name: "增加动作序列" }).click();
  await page.getByRole("menuitem", { name: kind, exact: true }).click();
}

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("a key sends a sequence with a wait, and sends a media key", async ({}, info) => {
  test.setTimeout(120000);
  const f = await fixture();
  let app: ElectronApplication | undefined;
  let client: FixtureClient | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    const page = opened.page;
    client = await FixtureClient.connect(f);
    const live = client;
    const south = async () => (await onDisk(live)).layers.base.south?.press;

    // South taps Ctrl+Z. A wait after it makes it a sequence of two steps ...
    await openControl(page, "south");
    await addStep(page, "等待");
    const second = page.getByRole("group", { name: "第 2 步" });
    const wait = second.getByRole("textbox", { name: "等待时间（ms）" });
    await expect(second).toContainText("等待");
    await expect(wait).toHaveValue("100");
    await expect(wait).toBeFocused();
    await expect.poll(south).toEqual({
      type: "sequence",
      steps: [{ keys: "Ctrl+Z" }, { wait: 100 }],
    });
    // ... of 150 ms.
    await wait.fill("150");
    await wait.press("Enter");
    await expect.poll(south).toEqual({
      type: "sequence",
      steps: [{ keys: "Ctrl+Z" }, { wait: 150 }],
    });
    await expect(page.getByTestId("editor-notice")).toHaveCount(0);
    await page.screenshot({ path: info.outputPath("sequence-steps.png") });

    // A press runs the steps: the shortcut at once, the wait as the engine
    // says it (the dry run prints both).
    live.events.length = 0;
    await live.call("fake.press", { control: "south" });
    await live.call("fake.release", { control: "south" });
    await expect
      .poll(() => decisions(live))
      .toContain("sequence: Ctrl+Z, wait 150");
    await expect.poll(() => chords(live)).toContain("LCtrl+Z");
    await expect(page.getByTestId("key-preview")).toHaveText(
      "S → Ctrl+Z, 150 ms",
    );

    // A step moves up; the chip is still the sequence.
    await page.getByRole("button", { name: "上移第 2 步" }).click();
    await expect.poll(south).toEqual({
      type: "sequence",
      steps: [{ wait: 150 }, { keys: "Ctrl+Z" }],
    });
    await expect(page.locator('button[data-callout="south"]')).toContainText(
      "撤销",
    );

    // Taking away the wait makes it the shortcut again, as the drawer writes
    // a shortcut: held, with no mode.
    await page.getByRole("button", { name: "删除第 1 步" }).click();
    await expect.poll(south).toEqual({ type: "keys", keys: "Ctrl+Z" });
    await expect(page.getByRole("group", { name: "快捷键" })).toBeVisible();

    // A media key is recorded like any shortcut, and shown by name.
    await openControl(page, "west");
    const shortcut = page.getByRole("group", { name: "快捷键", exact: true });
    await recordShortcut(app, shortcut, "VolumeUp");
    await expect(shortcut).toContainText("音量加");
    await expect
      .poll(async () => (await onDisk(live)).layers.base.west?.press)
      .toMatchObject({ type: "keys", keys: "VolumeUp" });
    await expect(
      page.locator('button[data-callout="west"]'),
    ).toHaveAccessibleName(/· 音量加$/);
    live.events.length = 0;
    await live.call("fake.press", { control: "west" });
    await live.call("fake.release", { control: "west" });
    await expect.poll(() => chords(live)).toContain("VolumeUp");
  } finally {
    client?.close();
    await cleanup(f, app);
  }
});

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("a profile file that extends another shows what it takes as plain keys, writes what it edits into its own file, and keeps the keys when the parent is deleted", async ({}, info) => {
  test.setTimeout(120000);
  const f = await fixture();
  let app: ElectronApplication | undefined;
  let client: FixtureClient | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    const page = opened.page;
    client = await FixtureClient.connect(f);
    const live = client;
    const chip = (control: string) =>
      page.locator(`button[data-callout="${control}"]`);
    const sketch = () => onDisk(live, "sketch");
    // The UI does not write extends any more (ADR-0076), so the profile is
    // made in the UI and the extends is written into its file, as a person
    // editing the file by hand would. The parent binds south and east.
    const paint = await live.call<ProfileDocument>("profiles.get", {
      id: "paint",
    });
    const parentFile = JSON.parse(paint.text);
    parentFile.layers.base.east = {
      press: { type: "keys", keys: "Ctrl+Y", mode: "tap" },
    };
    await live.call("profiles.put", {
      id: "paint",
      text: JSON.stringify(parentFile),
      revision: paint.revision,
    });

    // A new profile from the top bar, which switches to it ...
    await page.getByTestId("profile-menu").click();
    await page.getByTestId("new-profile").click();
    const create = page.getByRole("dialog", { name: "新建配置" });
    await create.getByRole("textbox").fill("Sketch");
    await create.getByRole("button", { name: "新建", exact: true }).click();
    await expect(create).toHaveCount(0);
    await expect(page.getByTestId("profile-menu")).toContainText("Sketch");
    await expect(chip("south")).toHaveAttribute("data-state", "unbound");

    // ... whose file then says it extends the paint profile.
    const created = await live.call<ProfileDocument>("profiles.get", {
      id: "sketch",
    });
    await live.call("profiles.put", {
      id: "sketch",
      text: JSON.stringify({ ...JSON.parse(created.text), extends: "paint" }),
      revision: created.revision,
    });

    // South and east are the parent's, and look like any other key: no mark,
    // and the settings have no "基于".
    await expect(chip("south")).toHaveAttribute("data-state", "bound");
    await expect(chip("south")).toContainText("撤销");
    await expect(chip("east")).toHaveAttribute("data-state", "bound");
    await expect(page.getByTestId("inherited-mark")).toHaveCount(0);
    await expect(chip("south")).not.toHaveAttribute("data-from-parent", "true");
    // Nothing of the parent's was copied into the file.
    expect((await sketch()).layers.base).toEqual({});
    await page.getByTestId("profile-menu").click();
    await page.getByTestId("profile-settings").click();
    const settings = page.getByRole("dialog", { name: "配置设置" });
    await expect(settings.getByRole("textbox", { name: "名称" })).toBeVisible();
    await expect(settings.getByRole("combobox")).toHaveCount(0);
    await expect(settings).not.toContainText("基于");
    await settings.getByRole("button", { name: "完成" }).click();
    await expect(settings).toBeHidden();

    // The drawer shows the parent's shortcut as the key's own, with nothing
    // to give it back.
    await openControl(page, "south");
    await expect(page.getByTestId("drawer-parent")).toHaveCount(0);
    await expect(
      page.getByRole("group", { name: "快捷键", exact: true }),
    ).toHaveAttribute("data-keys", "Ctrl+Z");
    await page.getByTestId("key-actions").click();
    await expect(
      page.getByRole("menuitem", { name: "恢复为继承" }),
    ).toHaveCount(0);
    await page.keyboard.press("Escape");
    await page.screenshot({ path: info.outputPath("extends-key.png") });

    // Editing south makes it the profile's own, and the file still extends.
    await recordShortcut(
      app,
      page.getByRole("group", { name: "快捷键", exact: true }),
      "Ctrl+Shift+Z",
    );
    await expect
      .poll(async () => (await sketch()).layers.base.south)
      .toMatchObject({
        label: "撤销",
        press: { type: "keys", keys: "Ctrl+Shift+Z" },
      });
    expect((await sketch()).extends).toBe("paint");
    // East, which was not touched, is still the parent's: not in the file.
    expect((await sketch()).layers.base).not.toHaveProperty("east");
    live.events.length = 0;
    await live.call("fake.press", { control: "south" });
    await live.call("fake.release", { control: "south" });
    // The preview names the sided keys that are pressed.
    await expect.poll(() => chords(live)).toContain("LCtrl+LShift+Z");
    live.events.length = 0;
    await live.call("fake.press", { control: "east" });
    await live.call("fake.release", { control: "east" });
    await expect.poll(() => chords(live)).toContain("LCtrl+Y");

    // The 配置 page says nothing of what a profile extends. Deleting the
    // parent does not warn about the profiles that extend it; they keep the
    // keys they had.
    await page.getByRole("button", { name: "配置", exact: true }).click();
    await expect(
      page.getByRole("article", { name: "Sketch" }),
    ).not.toContainText("基于");
    const parentCard = page.getByRole("article", { name: "E2E paint" });
    await parentCard
      .getByRole("button", { name: "「E2E paint」的更多操作" })
      .click();
    await page.getByRole("menuitem", { name: "删除…" }).click();
    const confirm = page.getByRole("dialog", { name: "删除「E2E paint」？" });
    await expect(confirm).not.toContainText("基于");
    await page.screenshot({ path: info.outputPath("delete-parent.png") });
    await confirm.getByRole("button", { name: "删除", exact: true }).click();
    await expect(confirm).toHaveCount(0);
    await expect(parentCard).toHaveCount(0);
    await expect.poll(async () => "extends" in (await sketch())).toBe(false);
    expect((await sketch()).layers.base.south).toMatchObject({
      press: { type: "keys", keys: "Ctrl+Shift+Z" },
    });
    expect((await sketch()).layers.base.east).toMatchObject({
      press: { type: "keys", keys: "Ctrl+Y" },
    });
    // The mapping page shows the keys as before.
    await page.getByRole("button", { name: "映射", exact: true }).click();
    await expect(chip("south")).toHaveAttribute("data-state", "bound");
    await expect(chip("east")).toHaveAttribute("data-state", "bound");
  } finally {
    client?.close();
    await cleanup(f, app);
  }
});
