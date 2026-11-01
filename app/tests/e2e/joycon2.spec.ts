import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { type ElectronApplication, expect, test } from "@playwright/test";
import { cleanup, fixture, launch, rpc } from "./helpers";

test.use({ actionTimeout: 15000 });

// Settings · Joy-Con 2, against the fake input's simulated backend: no
// Bluetooth, no state.json. `fake.joycon2 {side?}` is the user holding SYNC
// on a new Joy-Con 2 while the window is open (ARCHITECTURE §11).

interface JoyCon2State {
  available: boolean;
  windowOpen: boolean;
  known: { serial: string; side: string; connected: boolean }[];
}

const status = async (f: Parameters<typeof rpc>[0]) =>
  (await rpc(f, "joycon2.status")) as JoyCon2State;

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("连接新的 Joy-Con 2: the dialog counts down, remembers the controller, and 忘记 takes it away", async ({}, info) => {
  const f = await fixture();
  let app: ElectronApplication | undefined;
  try {
    const launched = await launch(f);
    app = launched.app;
    const page = launched.page;
    await page.getByRole("button", { name: "设置", exact: true }).click();
    const section = page.getByTestId("joycon2-settings");
    await expect(section).toBeVisible();
    await expect(
      section.getByRole("switch", { name: "自动连接记住的 Joy-Con 2" }),
    ).toHaveAttribute("aria-checked", "true");
    await expect(section.getByText("FAKEJC2L0001")).toHaveCount(0);

    await section.getByRole("button", { name: "连接新的 Joy-Con 2" }).click();
    const dialog = page.getByRole("dialog", { name: "连接新的 Joy-Con 2" });
    await expect(
      dialog.getByText("请在 30 秒内按住 Joy-Con 2 侧面的 SYNC 键。"),
    ).toBeVisible();
    await expect(dialog.getByRole("timer")).toHaveText(/^还剩 (30|29|28) 秒$/);
    await expect.poll(async () => (await status(f)).windowOpen).toBe(true);
    await page.screenshot({ path: info.outputPath("joycon2-window.png") });

    // The user holds SYNC on a left Joy-Con 2.
    await rpc(f, "fake.joycon2", { side: "L" });
    await expect(
      dialog.getByText("已连接并记住 Joy-Con 2（左）。"),
    ).toBeVisible();
    await page.screenshot({ path: info.outputPath("joycon2-found.png") });
    await dialog.getByRole("button", { name: "确定" }).click();
    await expect(dialog).toHaveCount(0);
    await expect(section.getByText("FAKEJC2L0001")).toBeVisible();
    await expect(section.getByText("已连接", { exact: true })).toBeVisible();

    // 忘记 asks first.
    await section
      .getByRole("button", { name: /^忘记 Joy-Con 2（左）/ })
      .click();
    const confirm = page.getByRole("dialog", {
      name: "忘记 Joy-Con 2（左）？",
    });
    await confirm.getByRole("button", { name: "取消" }).click();
    await expect(confirm).toHaveCount(0);
    expect((await status(f)).known).toHaveLength(1);
    await section
      .getByRole("button", { name: /^忘记 Joy-Con 2（左）/ })
      .click();
    await confirm.getByRole("button", { name: "忘记", exact: true }).click();
    await expect(confirm).toHaveCount(0);
    await expect(section.getByText("FAKEJC2L0001")).toHaveCount(0);
    expect((await status(f)).known).toEqual([]);
  } finally {
    await cleanup(f, app);
  }
});

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("取消 closes the window of the daemon with the dialog", async ({}) => {
  const f = await fixture();
  let app: ElectronApplication | undefined;
  try {
    const launched = await launch(f);
    app = launched.app;
    const page = launched.page;
    await page.getByRole("button", { name: "设置", exact: true }).click();
    const section = page.getByTestId("joycon2-settings");
    await section.getByRole("button", { name: "连接新的 Joy-Con 2" }).click();
    const dialog = page.getByRole("dialog", { name: "连接新的 Joy-Con 2" });
    await expect(dialog.getByRole("timer")).toBeVisible();
    await expect.poll(async () => (await status(f)).windowOpen).toBe(true);
    await dialog.getByRole("button", { name: "取消", exact: true }).click();
    await expect(dialog).toHaveCount(0);
    await expect.poll(async () => (await status(f)).windowOpen).toBe(false);
    // SYNC held now finds no window.
    expect(await rpc(f, "fake.joycon2", { side: "R" })).toMatchObject({
      accepted: false,
    });
  } finally {
    await cleanup(f, app);
  }
});

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("自动连接记住的 Joy-Con 2 is saved in config.json as joycon2.autoConnect", async ({}) => {
  const f = await fixture();
  const data = join(f.path, "data");
  let app: ElectronApplication | undefined;
  try {
    const launched = await launch(f);
    app = launched.app;
    const page = launched.page;
    await page.getByRole("button", { name: "设置", exact: true }).click();
    const sw = page
      .getByTestId("joycon2-settings")
      .getByRole("switch", { name: "自动连接记住的 Joy-Con 2" });
    await sw.click();
    await expect(sw).toHaveAttribute("aria-checked", "false");
    await expect
      .poll(async () =>
        JSON.parse(await readFile(join(data, "config.json"), "utf8")),
      )
      .toMatchObject({ joycon2: { autoConnect: false } });
    // The daemon applies it at once.
    await expect
      .poll(
        async () =>
          ((await rpc(f, "joycon2.status")) as { autoConnect: boolean })
            .autoConnect,
      )
      .toBe(false);
    await sw.click();
    await expect
      .poll(async () =>
        JSON.parse(await readFile(join(data, "config.json"), "utf8")),
      )
      .not.toHaveProperty("joycon2");
  } finally {
    await cleanup(f, app);
  }
});
