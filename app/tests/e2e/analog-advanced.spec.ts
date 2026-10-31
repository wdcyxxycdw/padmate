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
import { cleanup, launch, recordShortcut, setWindowSize } from "./helpers";

// The advanced analog editor (UI-28, M15): the knob and its ticks, the rings
// and the second stage of a trigger. Every edit is read back from the profile
// file the daemon has on disk, and a few are driven through the fake pad.

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

/**
 * Waits for profile.json to hold what pick reads from it. Edits that follow
 * each other leave the first save to a daemon that may still be starting up,
 * so this waits longer than the other polls; the edits still land, late. An
 * edit that was refused or lost shows as the editor's notice, which the
 * failure names: that is not a slow daemon.
 */
async function expectOnDisk(
  page: Page,
  client: FixtureClient,
  pick: (file: Awaited<ReturnType<typeof onDisk>>) => unknown,
  want: unknown,
): Promise<void> {
  try {
    await expect
      .poll(async () => pick(await onDisk(client)), { timeout: 20000 })
      .toEqual(want);
  } catch (error) {
    const said = (await page.getByTestId("editor-notice").allInnerTexts()).join(
      " / ",
    );
    if (!said) throw error;
    throw new Error(
      `${error instanceof Error ? error.message : error}\nThe editor says: ${said}`,
    );
  }
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

const previews = (client: FixtureClient) =>
  client.events
    .filter((event) => event.topic === "output.preview")
    .map((event) => event.data.chord);

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("a knob is set up with its step and ticks, and turning the stick sends a tick", async ({}, info) => {
  test.setTimeout(90000);
  const f = await editorFixture();
  let app: ElectronApplication | undefined;
  let client: FixtureClient | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    const page = opened.page;
    client = await FixtureClient.connect(f);
    const live = client;
    await openControl(page, "rightstick");
    const panel = page.getByRole("region", { name: "摇杆设置" });

    await panel
      .getByRole("radiogroup", { name: "方向模式", exact: true })
      .getByRole("radio", { name: "旋钮", exact: true })
      .click();
    await expect
      .poll(async () => (await onDisk(live)).analog?.rs?.mode)
      .toBe("knob");
    // The directions give way to the step and the two ticks.
    await expect(panel.locator(".analog-cell")).toHaveCount(0);
    await expect(
      panel.getByRole("button", { name: "WASD", exact: true }),
    ).toHaveCount(0);
    const step = panel.getByRole("spinbutton", {
      name: "每格角度",
      exact: true,
    });
    await expect(step).toHaveValue("30");
    await step.fill("45");
    await step.press("Enter");
    await expect
      .poll(async () => (await onDisk(live)).analog?.rs?.knobStepDeg)
      .toBe(45);
    // A step the daemon would refuse is not saved.
    await step.fill("5");
    await step.press("Enter");
    await expect(step).toHaveAttribute("aria-invalid", "true");
    expect((await onDisk(live)).analog.rs.knobStepDeg).toBe(45);
    await step.fill("30");
    await step.press("Enter");
    await expect
      .poll(async () => (await onDisk(live)).analog?.rs?.knobStepDeg)
      .toBe(30);

    await panel.locator('.analog-row[data-control="rs.cw"]').click();
    const cw = panel.getByRole("region", { name: "顺时针一格", exact: true });
    // A step is one shortcut: no mappings, sequence or script (ADR-0079 §8).
    for (const name of ["添加其他动作", "增加动作序列", "脚本"])
      await expect(cw.getByRole("button", { name, exact: true })).toHaveCount(
        0,
      );
    await recordShortcut(
      app,
      cw.getByRole("group", { name: "快捷键", exact: true }),
      "F21",
    );
    await panel.locator('.analog-row[data-control="rs.ccw"]').click();
    await recordShortcut(
      app,
      panel
        .getByRole("region", { name: "逆时针一格", exact: true })
        .getByRole("group", { name: "快捷键", exact: true }),
      "F22",
    );
    await expectOnDisk(
      page,
      live,
      (file) => [
        file.layers.base["rs.cw"]?.press?.keys,
        file.layers.base["rs.ccw"]?.press?.keys,
      ],
      ["F21", "F22"],
    );
    // The rest of the file was left as it was.
    expect((await onDisk(live)).layers.base["ls.right"].press.keys).toBe("F18");
    // 功能 names the movement: in analog.rs.label, and on its label.
    const name = page
      .getByTestId("mapping-editor")
      .getByRole("textbox", { name: "功能", exact: true });
    await name.fill("画笔大小");
    await name.press("Enter");
    await expectOnDisk(
      page,
      live,
      (file) => file.analog?.rs?.label,
      "画笔大小",
    );
    await expect(
      page.locator('button[data-callout="rightstick"] .stage-chip-title'),
    ).toHaveText("画笔大小");
    await expect(page.getByTestId("editor-notice")).toHaveCount(0);

    // Turning the stick a quarter turn sends one clockwise tick.
    live.events.length = 0;
    await live.call("fake.axis", { axis: "righty", value: -20000 });
    await page.waitForTimeout(100);
    // Both axes of a position go out together: the knob reads the angle 4 ms
    // after an input, and a position with one axis still old is not one the
    // stick was ever at.
    await Promise.all([
      live.call("fake.axis", { axis: "rightx", value: 14142 }),
      live.call("fake.axis", { axis: "righty", value: -14142 }),
    ]);
    await expect.poll(() => previews(live)).toContain("F21");
    expect(previews(live)).not.toContain("F22");
    await Promise.all([
      live.call("fake.axis", { axis: "rightx", value: 0 }),
      live.call("fake.axis", { axis: "righty", value: 0 }),
    ]);
    // Letting go makes no tick.
    await page.waitForTimeout(150);
    expect(previews(live).filter((chord) => chord === "F22")).toHaveLength(0);

    await setWindowSize(app, 850, 620);
    await expectPageFits(page);
    await page.screenshot({ path: info.outputPath("knob-850.png") });
  } finally {
    client?.close();
    await cleanup(f, app);
  }
});

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("the rings are bound and set, and the hard push sends its key", async ({}, info) => {
  test.setTimeout(90000);
  const f = await editorFixture();
  let app: ElectronApplication | undefined;
  let client: FixtureClient | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    const page = opened.page;
    client = await FixtureClient.connect(f);
    const live = client;
    await openControl(page, "leftstick");
    const panel = page.getByRole("region", { name: "摇杆设置", exact: true });
    // The file has the engine's standard mode: the drawer shows it as 四方向,
    // and opening it writes nothing.
    await expect(
      panel.getByRole("radio", { name: "四方向", exact: true }),
    ).toHaveAttribute("aria-checked", "true");
    expect((await onDisk(live)).analog.ls.mode).toBe("standard");
    await panel.getByTestId("stick-advanced").locator("summary").click();

    await panel.locator('.analog-row[data-control="ls.outer"]').click();
    await recordShortcut(
      app,
      panel
        .getByRole("region", { name: "重推", exact: true })
        .getByRole("group", { name: "快捷键", exact: true }),
      "F23",
    );
    await panel.locator('.analog-row[data-control="ls.inner"]').click();
    await recordShortcut(
      app,
      panel
        .getByRole("region", { name: "轻推", exact: true })
        .getByRole("group", { name: "快捷键", exact: true }),
      "F24",
    );
    const from = panel.getByRole("spinbutton", {
      name: "重推的位置",
      exact: true,
    });
    await expect(from).toHaveValue("90");
    await from.fill("80");
    await from.press("Enter");
    await expectOnDisk(
      page,
      live,
      (file) => [
        file.layers.base["ls.outer"]?.press?.keys,
        file.layers.base["ls.inner"]?.press?.keys,
        file.analog.ls.outerThreshold,
      ],
      ["F23", "F24", 0.8],
    );
    // The first edit wrote the mode the drawer shows; the dead zone is as it was.
    expect((await onDisk(live)).analog.ls).toMatchObject({
      mode: "fourWay",
      deadzone: 0.6,
    });

    // A medium push is the light push, a full push the hard push.
    await live.call("fake.axis", { axis: "lefty", value: -24000 });
    await expect.poll(() => previews(live)).toContain("F24");
    await expect(
      panel.locator('.analog-row[data-control="ls.inner"]'),
    ).toHaveAttribute("data-active", "true");
    await live.call("fake.axis", { axis: "lefty", value: -32767 });
    await expect.poll(() => previews(live)).toContain("F23");
    await expect(
      panel.locator('.analog-row[data-control="ls.outer"]'),
    ).toHaveAttribute("data-active", "true");
    await live.call("fake.axis", { axis: "lefty", value: 0 });
    await page.screenshot({ path: info.outputPath("rings.png") });
  } finally {
    client?.close();
    await cleanup(f, app);
  }
});

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("the full pull of a trigger is bound and set, and the old two-stage set-up moves over", async ({}, info) => {
  test.setTimeout(120000);
  const f = await editorFixture();
  // A file made by the editor of M12: the full press as the trigger's own
  // key at 90%, the half press on the raw axis at a shared 50%.
  await writeFile(
    join(f.path, "data", "profiles", "paint", "profile.json"),
    JSON.stringify({
      schemaVersion: 1,
      name: "Two stages",
      analog: { lt: { press: 0.9, release: 0.8 }, axisThreshold: 0.5 },
      layers: {
        base: {
          lt: { press: { type: "keys", keys: "F13", mode: "tap" } },
          "lefttrigger+": { press: { type: "keys", keys: "F14", mode: "tap" } },
          south: { press: { type: "keys", keys: "Ctrl+Z", mode: "tap" } },
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
    client = await FixtureClient.connect(f);
    const live = client;
    await openControl(page, "lefttrigger");
    await page
      .locator("details.disclosure > summary")
      .filter({ hasText: "扳机设置" })
      .click();
    const panel = page.getByRole("region", { name: "扳机设置", exact: true });
    // The old set-up keeps working; nothing is changed until it is moved.
    await expect(panel.getByRole("button", { name: /设置半按/ })).toHaveCount(
      0,
    );
    await panel
      .getByRole("button", { name: "改用按到底", exact: true })
      .click();
    const dialog = page.getByRole("dialog", { name: "改用按到底？" });
    await expect(dialog).toBeVisible();
    expect((await onDisk(live)).layers.base["lt.full"]).toBeUndefined();
    await dialog.getByRole("button", { name: "改用", exact: true }).click();
    await expect
      .poll(
        async () => (await onDisk(live)).layers.base["lt.full"]?.press?.keys,
      )
      .toBe("F13");
    const moved = await onDisk(live);
    expect(moved.layers.base.lt.press.keys).toBe("F14");
    expect(moved.layers.base["lefttrigger+"]).toBeUndefined();
    expect(moved.layers.base.south.press.keys).toBe("Ctrl+Z");
    expect(moved.analog.lt).toBeUndefined();
    expect(moved.analog.axisThreshold).toBe(0.5);
    await expect(
      panel.getByRole("button", { name: "改用按到底", exact: true }),
    ).toHaveCount(0);

    // The second stage: its points, and a different key.
    const press = panel.getByRole("spinbutton", {
      name: "按到底：按下",
      exact: true,
    });
    await expect(press).toHaveValue("90");
    await press.fill("95");
    await press.press("Enter");
    const release = panel.getByRole("spinbutton", {
      name: "按到底：松开",
      exact: true,
    });
    await release.fill("70");
    await release.press("Enter");
    await expect
      .poll(async () => (await onDisk(live)).analog?.lt)
      .toEqual({ fullPress: 0.95, fullRelease: 0.7 });
    // A release above the press is not saved, and says why.
    await release.fill("99");
    await release.press("Enter");
    await expect(panel.getByRole("alert")).toContainText(
      "“按到底：松开”不能大于“按到底：按下”",
    );
    await expect
      .poll(async () => (await onDisk(live)).analog?.lt?.fullRelease)
      .toBe(0.7);
    await release.fill("70");
    await release.press("Enter");

    await panel.locator('.analog-row[data-control="lt.full"]').click();
    const full = panel.getByRole("region", { name: "按到底", exact: true });
    await recordShortcut(
      app,
      full.getByRole("group", { name: "快捷键", exact: true }),
      "F15",
    );
    await expect
      .poll(
        async () => (await onDisk(live)).layers.base["lt.full"]?.press?.keys,
      )
      .toBe("F15");

    // Pulling past the second stage sends both keys.
    live.events.length = 0;
    await live.call("fake.axis", { axis: "lefttrigger", value: 32767 });
    await expect
      .poll(() => previews(live))
      .toEqual(expect.arrayContaining(["F14", "F15"]));
    await live.call("fake.axis", { axis: "lefttrigger", value: 0 });
    await page.screenshot({ path: info.outputPath("trigger-stages.png") });
  } finally {
    client?.close();
    await cleanup(f, app);
  }
});
