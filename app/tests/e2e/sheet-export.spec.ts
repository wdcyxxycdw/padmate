// OUT-18: shortcuts are recorded in the drawer, and the table lists them. The
// starter templates this file once tested are gone (ADR-0076).
import { readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import {
  type ElectronApplication,
  expect,
  type Page,
  test,
} from "@playwright/test";
import { cleanup, fixture, launch, recordShortcut } from "./helpers";

// A missing control fails its step with the locator instead of the whole
// test running out of time without cleanup.
test.use({ actionTimeout: 15000 });

async function openControl(page: Page, control: string): Promise<void> {
  await page.locator(`button[data-callout="${control}"]`).click();
  await expect(
    page.getByRole("complementary", { name: "按键编辑" }),
  ).toBeVisible();
}

async function openMore(page: Page, item: string): Promise<void> {
  await page.getByRole("button", { name: "更多", exact: true }).click();
  await page.getByRole("menuitem", { name: item, exact: true }).click();
}

// Keys in the base layer, in the other key group and in common.
const tableProfile = {
  schemaVersion: 0,
  name: "录入测试",
  // Not on start: it is the function key unless the settings say otherwise,
  // and what a file binds on it is not used (ADR-0075).
  common: {
    leftstick: {
      label: "切换",
      press: { type: "layer", op: "cycle", layers: ["base", "alt"] },
    },
    rightstick: {
      label: "吸管",
      press: { type: "keys", keys: "F15", mode: "tap" },
    },
  },
  layers: {
    base: {
      south: {
        label: "撤销",
        press: { type: "keys", keys: "Ctrl+Z", mode: "tap" },
      },
      north: {
        label: "重做",
        press: { type: "keys", keys: "F13", mode: "tap" },
      },
    },
    alt: {
      south: { press: { type: "keys", keys: "F14", mode: "tap" } },
      north: { press: { type: "keys", keys: "LCtrl+F16", mode: "tap" } },
    },
  },
};

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("shortcuts are only recorded, nothing assigns them, and the Markdown table lists the recorded ones", async ({}, info) => {
  test.setTimeout(90000);
  const f = await fixture();
  const file = join(f.path, "data", "profiles", "paint", "profile.json");
  await writeFile(file, JSON.stringify(tableProfile));
  let app: ElectronApplication | undefined;
  let page: Page | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    page = opened.page;
    const p = page;
    const drawer = p.getByTestId("mapping-editor");
    const label = drawer.getByRole("textbox", { name: "功能" });
    const shortcut = drawer.getByRole("group", { name: "快捷键", exact: true });
    const saved = async () => JSON.parse(await readFile(file, "utf8"));

    // Two named keys, X and B. Neither has a shortcut, and there is nowhere
    // to type one (OUT-18).
    await openControl(p, "west");
    await label.fill("抓手");
    await expect(shortcut).toHaveAttribute("data-keys", "");
    await expect(drawer.getByRole("textbox", { name: "快捷键" })).toHaveCount(
      0,
    );
    await openControl(p, "east");
    await label.fill("画笔");
    await expect(shortcut).toHaveAttribute("data-keys", "");

    // Nothing offers to fill them in: not 更多, not the export.
    await p.getByRole("button", { name: "更多", exact: true }).click();
    const menu = p.getByRole("menu");
    await expect(
      menu.getByRole("menuitem", { name: "导出快捷键对照表…", exact: true }),
    ).toBeVisible();
    await expect(menu.getByRole("menuitem", { name: /自动分配/ })).toHaveCount(
      0,
    );
    await p.keyboard.press("Escape");
    await expect(menu).toHaveCount(0);
    await openMore(p, "导出快捷键对照表…");
    const exporting = p.getByRole("dialog", { name: "导出快捷键对照表" });
    await expect(exporting).toBeVisible();
    await expect(exporting).not.toContainText("尚未设置快捷键");
    for (const name of ["自动分配并导出", "直接导出"])
      await expect(
        exporting.getByRole("button", { name, exact: true }),
      ).toHaveCount(0);
    await exporting
      .getByRole("button", { name: "取消", exact: true })
      .last()
      .click();
    await expect(exporting).toHaveCount(0);

    // X gets its shortcut by recording; it is saved at once (UI-43). B
    // stays named only.
    await openControl(p, "west");
    await recordShortcut(app, shortcut, "F16");
    await expect
      .poll(async () => (await saved()).layers.base.west)
      .toEqual({
        label: "抓手",
        press: { type: "keys", keys: "F16" },
      });
    const written = await saved();
    expect(written.layers.base.east).toEqual({ label: "画笔" });
    expect(written.layers.base.south).toEqual(tableProfile.layers.base.south);
    expect(written.layers.base.north).toEqual(tableProfile.layers.base.north);
    expect(written.layers.alt).toEqual(tableProfile.layers.alt);
    expect(written.common).toEqual(tableProfile.common);

    // Export as Markdown through a stubbed save dialog.
    await app.evaluate(({ dialog }, path) => {
      const recorded = globalThis as { exportOptions?: unknown };
      dialog.showSaveDialog = async (_window: unknown, options?: unknown) => {
        recorded.exportOptions = options;
        return { canceled: false, filePath: `${path}/sheet.md` };
      };
    }, f.path);
    await openMore(p, "导出快捷键对照表…");
    await expect(exporting).toBeVisible();
    await exporting
      .getByRole("radio", { name: "Markdown", exact: true })
      .click();
    await exporting.getByRole("button", { name: "导出", exact: true }).click();
    await expect(exporting).toHaveCount(0);
    const sheet = join(f.path, "sheet.md");
    await expect
      .poll(() => readFile(sheet, "utf8").catch(() => ""))
      .toContain("录入测试 快捷键对照表");
    await expect(p.locator(".shell-toast")).toHaveText("对照表已导出");
    const options = (await app.evaluate(
      () => (globalThis as { exportOptions?: unknown }).exportOptions,
    )) as { title?: string; defaultPath?: string };
    expect(options.title).toBe("导出快捷键对照表 / Export shortcut table");
    expect(options.defaultPath).toBe("padmate-shortcuts.md");
    const markdown = await readFile(sheet, "utf8");
    await info.attach("shortcut-table.md", {
      body: markdown,
      contentType: "text/markdown",
    });
    const lines = markdown.split("\n");
    expect(lines[0]).toBe("# 录入测试 快捷键对照表");
    expect(lines).toContain(
      "在绘画软件的快捷键设置中选中右列的功能，再按左列的手柄按键，padmate 将自动输入快捷键。",
    );
    expect(lines).toContain("| 手柄按键 | 快捷键 | 请在软件中设置为 |");
    // Button names follow the pad; the shortcut and label columns are fixed.
    expect(markdown).toMatch(/^\| [^|\n]+ \| F16 \| 抓手 \|$/m);
    expect(markdown).toMatch(/^\| [^|\n]+ \| F13 \| 重做 \|$/m);
    expect(markdown).toMatch(
      /^\| [^|\n]+ \| Ctrl\+Z \| 撤销（通常已是默认） \|$/m,
    );
    // A key without a shortcut is not a row.
    expect(markdown).not.toContain("画笔");
    // The other key group and common are counted, never shown.
    expect(lines).toContain("配置文件中还有 4 项设置未列出。");
    expect(markdown).not.toContain("F14");
    expect(markdown).not.toContain("F15");
    expect(markdown).not.toContain("吸管");
    expect(lines).toContain(
      "- 仅在此配置使用中时，按下手柄按键才会输入快捷键。",
    );
    expect(markdown).toContain("Aseprite 不支持 F13–F24。");
    expect(markdown).not.toMatch(/层|layer/i);
  } catch (error) {
    console.error(
      "Page text:",
      await page?.locator("body").innerText({ timeout: 2000 }).catch(String),
    );
    console.error("profile.json:", await readFile(file, "utf8").catch(String));
    throw error;
  } finally {
    await cleanup(f, app);
  }
});
