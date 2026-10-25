import { readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { type ElectronApplication, expect, test } from "@playwright/test";
import type { StatusResult } from "../../src/shared/api";
import { cleanup, fixture, launch, rpc } from "./helpers";

test.use({ actionTimeout: 15000 });

// UI-19: config.json's timing and emergency combo apply without a restart.
// The window is in front, so a press is only previewed (output.preview).

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("触发长按时间 saved in Settings applies to the next long press at once", async ({}, info) => {
  const f = await fixture();
  const data = join(f.path, "data");
  await writeFile(
    join(data, "profiles", "paint", "profile.json"),
    JSON.stringify({
      schemaVersion: 0,
      name: "E2E paint",
      layers: {
        base: {
          west: {
            tap: { type: "keys", keys: "F13" },
            hold: { type: "keys", keys: "F14" },
          },
        },
      },
    }),
  );
  await writeFile(
    join(data, "config.json"),
    JSON.stringify({
      schemaVersion: 0,
      enabled: false,
      timing: { holdMs: 60000 },
    }),
  );
  let app: ElectronApplication | undefined;
  try {
    const launched = await launch(f);
    app = launched.app;
    const page = launched.page;
    await page.evaluate(() => {
      const observed = window as unknown as { previews: string[] };
      observed.previews = [];
      window.padmate.onEvent((event) => {
        if (event.topic === "output.preview")
          observed.previews.push((event.data as { chord: string }).chord);
      });
    });
    const previews = () =>
      page.evaluate(
        () => (window as unknown as { previews: string[] }).previews,
      );
    // A long press is a tap while the hold takes a minute.
    await rpc(f, "fake.press", { control: "west" });
    await page.waitForTimeout(300);
    await rpc(f, "fake.release", { control: "west" });
    await expect.poll(previews).toEqual(["F13"]);

    await page.getByRole("button", { name: "设置", exact: true }).click();
    const hold = page.getByRole("textbox", { name: "触发长按时间" });
    await expect(hold).toHaveValue("60000");
    await expect(hold).toHaveAttribute("placeholder", "250");
    await expect(page.getByText("按住超过这个时间算长按")).toHaveCount(0);
    await hold.fill("50");
    await hold.press("Enter");
    await expect
      .poll(async () =>
        JSON.parse(await readFile(join(data, "config.json"), "utf8")),
      )
      .toEqual({
        schemaVersion: 0,
        enabled: false,
        timing: { holdMs: 50 },
      });
    await expect(page.getByText("部分设置需重启 padmate 后生效。")).toHaveCount(
      0,
    );
    await page.screenshot({ path: info.outputPath("settings-timing.png") });

    // The same long press is a hold now, without a restart.
    await rpc(f, "fake.press", { control: "west" });
    await expect.poll(previews).toEqual(["F13", "F14"]);
    await rpc(f, "fake.release", { control: "west" });
  } finally {
    await cleanup(f, app);
  }
});

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("紧急暂停 records a new combo pressed on the controller", async ({}, info) => {
  const f = await fixture();
  const data = join(f.path, "data");
  let app: ElectronApplication | undefined;
  try {
    const launched = await launch(f);
    app = launched.app;
    const page = launched.page;
    await page.getByRole("button", { name: "设置", exact: true }).click();
    await page.getByRole("button", { name: "修改紧急暂停" }).click();
    const dialog = page.getByRole("dialog", { name: "修改紧急暂停" });
    await expect(dialog).toContainText("按住新的组合键，然后松开");
    // The mapping pauses for the recording; input still comes.
    await expect
      .poll(
        async () => ((await rpc(f, "status.get")) as StatusResult).capturing,
      )
      .toBe(true);
    for (const control of ["west", "east"])
      await rpc(f, "fake.press", { control });
    // Held together: the dialog shows both before they are let go.
    await expect(dialog.getByRole("img", { name: "W + E" })).toBeVisible();
    for (const control of ["west", "east"])
      await rpc(f, "fake.release", { control });
    await expect(dialog.getByRole("alert")).toHaveCount(0);
    await dialog.getByRole("radio", { name: "3 秒", exact: true }).click();
    await page.screenshot({ path: info.outputPath("settings-combo.png") });
    await dialog.getByRole("button", { name: "保存", exact: true }).click();
    await expect(dialog).toHaveCount(0);
    await expect
      .poll(async () =>
        JSON.parse(await readFile(join(data, "config.json"), "utf8")),
      )
      .toEqual({
        schemaVersion: 0,
        enabled: false,
        safety: {
          panic: { standard: { buttons: ["west", "east"], holdMs: 3000 } },
        },
      });
    const status = (await rpc(f, "status.get")) as StatusResult;
    expect(status.capturing).toBe(false);
    expect(status.combos).toEqual([
      {
        device: 1,
        family: "standard",
        buttons: ["west", "east"],
        holdMs: 3000,
      },
    ]);
    await expect(page.getByText("按住 3 秒：松开所有按键并暂停")).toBeVisible();
  } finally {
    await cleanup(f, app);
  }
});

// PRO-29: a settings file with an error in one member is repaired; the page
// shows what stayed and the default for what was reset, never "无法读取".
// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("a repaired settings file keeps its valid settings and resets the combo that had the error", async ({}, info) => {
  const f = await fixture();
  const data = join(f.path, "data");
  await writeFile(
    join(data, "config.json"),
    JSON.stringify({
      schemaVersion: 0,
      enabled: false,
      timing: { holdMs: 320 },
      // One button: the emergency pause needs at least two.
      safety: { panic: { standard: { buttons: ["back"], holdMs: 2000 } } },
    }),
  );
  let app: ElectronApplication | undefined;
  try {
    const launched = await launch(f);
    app = launched.app;
    const page = launched.page;
    await page
      .getByRole("dialog", { name: "设置已恢复" })
      .getByRole("button", { name: "确定", exact: true })
      .click();
    await page.getByRole("button", { name: "设置", exact: true }).click();
    await expect(page.getByText("无法读取设置文件")).toHaveCount(0);
    await expect(
      page.getByRole("textbox", { name: "触发长按时间" }),
    ).toHaveValue("320");
    // The combo that had the error is the default again, in the daemon and
    // on the page: two buttons, not the one that was written.
    const status = (await rpc(f, "status.get")) as StatusResult;
    expect(status.combos?.[0]?.buttons.length).toBeGreaterThanOrEqual(2);
    await page.screenshot({ path: info.outputPath("settings-repaired.png") });
    // Editing still works on the repaired file.
    await page.getByRole("textbox", { name: "触发长按时间" }).fill("400");
    await page.getByRole("textbox", { name: "触发长按时间" }).press("Enter");
    await expect
      .poll(async () =>
        JSON.parse(await readFile(join(data, "config.json"), "utf8")),
      )
      .toMatchObject({ timing: { holdMs: 400 } });
  } finally {
    await cleanup(f, app);
  }
});

// IN-13: 识别 on a 手柄调校 row asks the daemon to identify the controller.
// The fake input daemon has nothing to buzz or light: it says which cues its
// capabilities allow (PADMATE_FAKE_CAPS; rumble, a color LED and player
// lights unless set) and records what it was asked, with the programs the
// real controller would show (fake.effects).
interface FakeEffects {
  effects: {
    kind: string;
    device: number;
    cues?: { rumble: boolean; led: boolean; player: boolean };
    rumble?: { low?: number; high?: number; ms: number }[];
    led?: { color?: string; ms: number }[];
    player?: { index: number; ms: number }[];
  }[];
}

const FAKE_PAD = "padmate IPC fake gamepad";

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("识别 buzzes and flashes the fake controller, for about two seconds", async ({}, info) => {
  const f = await fixture();
  let app: ElectronApplication | undefined;
  try {
    const launched = await launch(f);
    app = launched.app;
    const page = launched.page;
    const identified = async () =>
      ((await rpc(f, "fake.effects")) as FakeEffects).effects.filter(
        (call) => call.kind === "identify",
      );
    await rpc(f, "fake.effects", { clear: true });
    await page.getByRole("button", { name: "设置", exact: true }).click();
    const button = page.getByRole("button", { name: `识别 ${FAKE_PAD}` });
    await expect(button).toBeVisible();
    await expect(button).toBeEnabled();
    // Beside 调校, with no helper line.
    await expect(
      page.getByRole("button", { name: `调校 ${FAKE_PAD}` }),
    ).toBeVisible();

    await button.click();
    await expect(button).toBeDisabled();
    // The daemon was asked once, for the fake controller, and it can buzz and
    // flash (it has a color LED, so no chase of the player lights).
    await expect.poll(async () => (await identified()).length).toBe(1);
    const [call] = await identified();
    expect(call?.device).toBe(1);
    expect(call?.cues).toEqual({ rumble: true, led: true, player: false });
    // Three buzzes of 150 ms on both motors, 150 ms apart.
    expect(call?.rumble?.map((step) => step.ms)).toEqual([
      150, 150, 150, 150, 150,
    ]);
    expect(
      call?.rumble?.map(
        (step) => (step.low ?? 0) > 0 && step.high === step.low,
      ),
    ).toEqual([true, false, true, false, true]);
    // Three white flashes of 250 ms, 250 ms apart.
    expect(call?.led).toEqual([
      { color: "#ffffff", ms: 250 },
      { ms: 250 },
      { color: "#ffffff", ms: 250 },
      { ms: 250 },
      { color: "#ffffff", ms: 250 },
    ]);
    expect(call?.player ?? []).toEqual([]);
    // A controller that can show something says nothing under its name.
    await expect(page.getByText("该手柄无法震动或闪灯")).toHaveCount(0);
    await page.screenshot({ path: info.outputPath("settings-identify.png") });

    // The button comes back after two seconds, and another click asks again.
    await expect(button).toBeEnabled({ timeout: 5000 });
    await button.click();
    await expect.poll(async () => (await identified()).length).toBe(2);
  } finally {
    await cleanup(f, app);
  }
});

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("识别 of a controller that can show nothing says so under its name", async ({}, info) => {
  const f = await fixture();
  f.env.PADMATE_FAKE_CAPS = "none";
  let app: ElectronApplication | undefined;
  try {
    const launched = await launch(f);
    app = launched.app;
    const page = launched.page;
    await page.getByRole("button", { name: "设置", exact: true }).click();
    const button = page.getByRole("button", { name: `识别 ${FAKE_PAD}` });
    await button.click();
    const note = page.getByText("该手柄无法震动或闪灯");
    await expect(note).toBeVisible();
    // Under the name, in the row of the controller.
    await expect(
      page.locator(".row", { has: button }).getByText(FAKE_PAD),
    ).toBeVisible();
    await expect(note).toBeVisible();
    const call = ((await rpc(f, "fake.effects")) as FakeEffects).effects.find(
      (item) => item.kind === "identify",
    );
    expect(call?.cues).toEqual({ rumble: false, led: false, player: false });
    expect(call?.rumble ?? []).toEqual([]);
    expect(call?.led ?? []).toEqual([]);
    await page.screenshot({
      path: info.outputPath("settings-identify-none.png"),
    });
  } finally {
    await cleanup(f, app);
  }
});
