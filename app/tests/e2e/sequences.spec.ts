import {
  type ElectronApplication,
  expect,
  type Page,
  test,
} from "@playwright/test";
import type { ProfileDocument } from "../../src/shared/api";
import { FixtureClient } from "./fixture-client";
import { cleanup, fixture, launch, recordShortcut } from "./helpers";

// UI-31, MAP-22, OUT-20, OUT-21 (ADR-0070): the steps of a sequence, the type
// text action and the media keys, in the key drawer. The fake pad is a
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

async function addStep(page: Page, kind: "快捷键" | "等待" | "文字") {
  await page.getByRole("button", { name: "添加下一步" }).click();
  await page.getByRole("menuitem", { name: kind, exact: true }).click();
}

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("a key sends a sequence with a wait and a text, and types a text, and sends a media key", async ({}, info) => {
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
    // ... of 150 ms, and a text after it.
    await wait.fill("150");
    await wait.press("Enter");
    await addStep(page, "文字");
    const text = page
      .getByRole("group", { name: "第 3 步" })
      .getByRole("textbox", { name: "输入的文字" });
    await expect(text).toBeFocused();
    await text.fill("你好");
    await expect(page.getByTestId("text-left")).toHaveText("剩余 254");
    // Typing saves nothing; leaving the box does.
    await text.blur();
    await expect.poll(south).toEqual({
      type: "sequence",
      steps: [{ keys: "Ctrl+Z" }, { wait: 150 }, { text: "你好" }],
    });
    await expect(page.getByTestId("editor-notice")).toHaveCount(0);
    await page.screenshot({ path: info.outputPath("sequence-steps.png") });

    // A press runs the steps: the shortcut at once, the text as the engine
    // says it (the dry run prints both).
    live.events.length = 0;
    await live.call("fake.press", { control: "south" });
    await live.call("fake.release", { control: "south" });
    await expect
      .poll(() => decisions(live))
      .toContain('sequence: Ctrl+Z, wait 150, text "你好"');
    // The shortcut step reached the output (the preview lists chords; a text
    // is not one, so it is only in the decision above).
    await expect.poll(() => chords(live)).toContain("LCtrl+Z");
    await expect(page.getByTestId("key-preview")).toHaveText(
      "S → Ctrl+Z, 150 ms, “你好”",
    );

    // A step moves up; the chip is a sequence of three steps.
    await page.getByRole("button", { name: "上移第 3 步" }).click();
    await expect.poll(south).toEqual({
      type: "sequence",
      steps: [{ keys: "Ctrl+Z" }, { text: "你好" }, { wait: 150 }],
    });
    await expect(page.locator('button[data-callout="south"]')).toContainText(
      "撤销",
    );

    // Taking away the last wait and text makes it the shortcut again.
    await page.getByRole("button", { name: "删除第 3 步" }).click();
    await page.getByRole("button", { name: "删除第 2 步" }).click();
    await expect
      .poll(south)
      .toEqual({ type: "keys", keys: "Ctrl+Z", mode: "tap" });
    await expect(page.getByRole("group", { name: "快捷键" })).toBeVisible();

    // 输入文字 is an action type of its own, on any gesture.
    await openControl(page, "east");
    await page
      .getByTestId("action-type")
      .getByRole("radio", { name: "输入文字" })
      .click();
    const box = page.getByRole("textbox", { name: "输入的文字" });
    await box.fill("签名\nKim");
    await box.blur();
    await expect
      .poll(async () => (await onDisk(live)).layers.base.east?.press)
      .toEqual({ type: "text", text: "签名\nKim" });
    live.events.length = 0;
    await live.call("fake.press", { control: "east" });
    await live.call("fake.release", { control: "east" });
    await expect.poll(() => decisions(live)).toContain('text "签名\\nKim"');
    await expect(page.getByTestId("key-preview")).toContainText(
      "输入文字「签名 Kim」",
    );
    await page.screenshot({ path: info.outputPath("type-text.png") });

    // A text that cannot be typed is not saved and says why.
    await box.fill("a\u0007b");
    await expect(
      page.getByTestId("mapping-editor").getByRole("alert"),
    ).toHaveText("不能含有换行和制表符以外的控制字符");
    await box.blur();
    expect((await onDisk(live)).layers.base.east?.press).toEqual({
      type: "text",
      text: "签名\nKim",
    });
    await box.press("Escape");
    await expect(box).toHaveValue("签名\nKim");

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
test("a profile based on another shows what it inherits, owns what it edits, gives it back with 恢复为继承, and keeps the keys when the parent is deleted", async ({}, info) => {
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
    const chip = page.locator('button[data-callout="south"]');
    const mark = chip.getByTestId("inherited-mark");
    const sketch = () => onDisk(live, "sketch");

    // A new profile from the top bar, which switches to it ...
    await page.getByTestId("profile-menu").click();
    await page.getByTestId("new-profile").click();
    const create = page.getByRole("dialog", { name: "新建配置" });
    await create.getByRole("textbox").fill("Sketch");
    await create.getByRole("button", { name: "新建", exact: true }).click();
    await expect(create).toHaveCount(0);
    await expect(page.getByTestId("profile-menu")).toContainText("Sketch");
    await expect(mark).toHaveCount(0);

    // ... based on the paint profile in its 配置设置.
    await page.getByTestId("profile-menu").click();
    await page.getByTestId("profile-settings").click();
    const dialog = page.getByRole("dialog", { name: "配置设置" });
    const based = dialog.getByRole("combobox", { name: "基于" });
    await expect(based).toHaveValue("");
    // Not itself; the default and the other profiles can be chosen.
    await expect(based.locator("option")).toHaveText([
      "无",
      "默认",
      "E2E paint",
    ]);
    await based.selectOption({ label: "E2E paint" });
    await expect.poll(async () => (await sketch()).extends).toBe("paint");
    await dialog.getByRole("button", { name: "完成" }).click();
    await expect(dialog).toBeHidden();

    // South is the parent's: its chip shows the parent's binding, marked.
    await expect(chip).toHaveAttribute("data-from-parent", "true");
    await expect(chip).toHaveAttribute("data-state", "bound");
    await expect(mark).toHaveText("继承");
    await expect(chip).toContainText("撤销");
    // Nothing of the parent's was copied into the file.
    expect((await sketch()).layers.base).toEqual({});
    await openControl(page, "south");
    await expect(page.getByTestId("drawer-parent")).toHaveText(
      "继承自 E2E paint",
    );
    await expect(
      page.getByRole("group", { name: "快捷键", exact: true }),
    ).toHaveAttribute("data-keys", "Ctrl+Z");
    await page.getByTestId("key-actions").click();
    await expect(
      page.getByRole("menuitem", { name: "恢复为继承" }),
    ).toHaveCount(0);
    await page.keyboard.press("Escape");
    await page.screenshot({ path: info.outputPath("inherited-key.png") });

    // The 配置 page says what it is based on, under the name.
    await page.getByRole("button", { name: "配置", exact: true }).click();
    await expect(page.getByRole("article", { name: "Sketch" })).toContainText(
      "基于「E2E paint」",
    );
    await expect(
      page.getByRole("article", { name: "E2E paint" }),
    ).not.toContainText("基于");
    await page.getByRole("button", { name: "映射", exact: true }).click();

    // Editing it makes it the profile's own, whole.
    await openControl(page, "south");
    await recordShortcut(
      app,
      page.getByRole("group", { name: "快捷键", exact: true }),
      "Ctrl+Shift+Z",
    );
    await expect
      .poll(async () => (await sketch()).layers.base.south)
      .toEqual({
        label: "撤销",
        press: { type: "keys", keys: "Ctrl+Shift+Z", mode: "tap" },
      });
    await expect(chip).not.toHaveAttribute("data-from-parent", "true");
    await expect(mark).toHaveCount(0);
    await expect(page.getByTestId("drawer-parent")).toHaveCount(0);
    // And a press types it.
    live.events.length = 0;
    await live.call("fake.press", { control: "south" });
    await live.call("fake.release", { control: "south" });
    await expect.poll(() => decisions(live)).toContain("tap Ctrl+Shift+Z");

    // 恢复为继承 gives the key back to the parent: the control leaves the file.
    await page.getByTestId("key-actions").click();
    await page.getByRole("menuitem", { name: "恢复为继承" }).click();
    await expect.poll(async () => (await sketch()).layers.base).toEqual({});
    await expect(chip).toHaveAttribute("data-from-parent", "true");
    await expect(page.getByTestId("drawer-parent")).toHaveText(
      "继承自 E2E paint",
    );
    live.events.length = 0;
    await live.call("fake.press", { control: "south" });
    await live.call("fake.release", { control: "south" });
    await expect.poll(() => decisions(live)).toContain("tap Ctrl+Z");

    // Deleting the parent says how many profiles are based on it, and they
    // keep what they inherited.
    await page.getByRole("button", { name: "配置", exact: true }).click();
    const parentCard = page.getByRole("article", { name: "E2E paint" });
    await parentCard
      .getByRole("button", { name: "「E2E paint」的更多操作" })
      .click();
    await page.getByRole("menuitem", { name: "删除…" }).click();
    const confirm = page.getByRole("dialog", { name: "删除「E2E paint」？" });
    await expect(confirm).toContainText(
      "有 1 个配置基于它，将保留各自现有的按键。",
    );
    await page.screenshot({ path: info.outputPath("delete-parent.png") });
    await confirm.getByRole("button", { name: "删除", exact: true }).click();
    await expect(confirm).toHaveCount(0);
    await expect(parentCard).toHaveCount(0);
    await expect.poll(async () => "extends" in (await sketch())).toBe(false);
    expect((await sketch()).layers.base.south).toEqual({
      label: "撤销",
      press: { type: "keys", keys: "Ctrl+Z", mode: "tap" },
    });
    await expect(
      page.getByRole("article", { name: "Sketch" }),
    ).not.toContainText("基于");
    // The mapping page shows the keys as the profile's own now.
    await page.getByRole("button", { name: "映射", exact: true }).click();
    await expect(chip).toHaveAttribute("data-state", "bound");
    await expect(chip).not.toHaveAttribute("data-from-parent", "true");
    await expect(mark).toHaveCount(0);
  } finally {
    client?.close();
    await cleanup(f, app);
  }
});
