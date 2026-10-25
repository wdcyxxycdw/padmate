import { readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import {
  type ElectronApplication,
  expect,
  type Page,
  test,
} from "@playwright/test";
import type { StatusResult } from "../../src/shared/api";
import {
  cleanup,
  type Fixture,
  fixture,
  launch,
  recordShortcut,
  root,
  rpc,
} from "./helpers";

// A missing control fails its step with the locator instead of the whole
// test running out of time without cleanup.
test.use({ actionTimeout: 15000 });

const templates = join(root, "examples", "profiles", "templates");

/** A committed template fixture, as written for a profile named name. */
async function templateFile(file: string, name: string): Promise<string> {
  const text = await readFile(join(templates, file), "utf8");
  const named = text.replace(
    '"name": "绘画"',
    `"name": ${JSON.stringify(name)}`,
  );
  expect(named).not.toBe(text);
  return named;
}

async function activeProfile(f: Fixture): Promise<string> {
  return ((await rpc(f, "status.get")) as StatusResult).profileId;
}

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

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("新建配置 from a template writes the committed template and the daemon loads it", async ({}, info) => {
  test.setTimeout(90000);
  const f = await fixture();
  const data = join(f.path, "data");
  const profileFile = (id: string) =>
    readFile(join(data, "profiles", id, "profile.json"), "utf8");
  let app: ElectronApplication | undefined;
  let page: Page | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    page = opened.page;
    const p = page;
    const card = (id: string) => p.locator(`li[data-profile-id="${id}"]`);
    const rail = (name: string) =>
      p.getByRole("navigation").getByRole("button", { name, exact: true });
    await rail("配置").click();
    await expect(rail("配置")).toHaveAttribute("aria-current", "page");

    // The library is not empty, so the dialog starts blank; 模板 suggests
    // a name and preselects the template for the connected standard pad.
    await p.getByRole("button", { name: "新建配置", exact: true }).click();
    const create = p.getByRole("dialog", { name: "新建配置" });
    await expect(create).toBeVisible();
    const startFrom = create.getByRole("radiogroup", { name: "内容" });
    await expect(
      startFrom.getByRole("radio", { name: "空白", exact: true }),
    ).toHaveAttribute("aria-checked", "true");
    await expect(
      startFrom.getByRole("radio", { name: "复制当前", exact: true }),
    ).toBeEnabled();
    const name = create.getByRole("textbox", { name: "名称" });
    await expect(name).toHaveValue("");
    await startFrom.getByRole("radio", { name: "模板", exact: true }).click();
    await expect(name).toHaveValue("绘画");
    const pad = create.getByRole("radiogroup", { name: "手柄" });
    await expect(
      pad.getByRole("radio", { name: "标准手柄", exact: true }),
    ).toHaveAttribute("aria-checked", "true");
    await expect(
      pad.getByRole("radio", { name: "单只 Joy-Con 横握", exact: true }),
    ).toHaveAttribute("aria-checked", "false");
    expect(await create.innerText()).not.toMatch(/层/);
    await p.screenshot({ path: info.outputPath("new-profile-template.png") });
    await name.fill("My studio");
    await create.getByRole("button", { name: "新建", exact: true }).click();
    await expect(create).toHaveCount(0);
    await expect(
      card("my-studio").getByRole("heading", { name: "My studio" }),
    ).toBeVisible();
    expect(await profileFile("my-studio")).toBe(
      await templateFile("standard-zh.json", "My studio"),
    );

    // A picked template wins over the connected pad.
    await p.getByRole("button", { name: "新建配置", exact: true }).click();
    await expect(create).toBeVisible();
    await startFrom.getByRole("radio", { name: "模板", exact: true }).click();
    await pad
      .getByRole("radio", { name: "单只 Joy-Con 横握", exact: true })
      .click();
    await expect(
      pad.getByRole("radio", { name: "单只 Joy-Con 横握", exact: true }),
    ).toHaveAttribute("aria-checked", "true");
    await name.fill("Joy-Con");
    await create.getByRole("button", { name: "新建", exact: true }).click();
    await expect(create).toHaveCount(0);
    await expect(
      card("joy-con").getByRole("heading", { name: "Joy-Con" }),
    ).toBeVisible();
    expect(await profileFile("joy-con")).toBe(
      await templateFile("joycon-zh.json", "Joy-Con"),
    );

    // Creating never activates; 编辑按键 does, so the daemon must accept
    // the template, and every bound control shows its label on a chip.
    expect(await activeProfile(f)).toBe("paint");
    await card("my-studio")
      .getByRole("button", { name: "编辑按键", exact: true })
      .click();
    await expect.poll(() => activeProfile(f)).toBe("my-studio");
    await expect(rail("映射")).toHaveAttribute("aria-current", "page");
    await expect(p.getByTestId("profile-menu")).toContainText("My studio");
    const chips: [string, string][] = [
      ["lefttrigger", "抓手"],
      ["leftshoulder", "Ctrl"],
      ["rightshoulder", "Shift"],
      ["righttrigger", "Alt"],
      ["dpad.left", "撤销"],
      ["dpad.right", "重做"],
      ["dpad.up", "画笔"],
      ["dpad.down", "橡皮"],
      ["south", "自由变换"],
      ["north", "新建图层"],
      ["east", "水平翻转画布"],
    ];
    for (const [callout, label] of chips)
      await expect(
        p.locator(`button[data-callout="${callout}"]`),
      ).toContainText(label);
    await p.screenshot({ path: info.outputPath("template-mapping.png") });
  } catch (error) {
    console.error(
      "Page text:",
      await page?.locator("body").innerText({ timeout: 2000 }).catch(String),
    );
    throw error;
  } finally {
    await cleanup(f, app);
  }
});

// Keys in the base layer, in the other key group and in common.
const tableProfile = {
  schemaVersion: 0,
  name: "录入测试",
  // Not on back or start: they are function keys, whose bindings a save
  // removes (ADR-0069).
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
    const label = drawer.getByRole("textbox", { name: "在手柄图上显示为" });
    const sending = drawer.getByRole("radiogroup", { name: "发送方式" });
    const shortcut = drawer.getByRole("group", { name: "快捷键", exact: true });
    const saved = async () => JSON.parse(await readFile(file, "utf8"));

    // Two named keys: X held, B tapped. Neither has a shortcut, and there
    // is nowhere to type one (OUT-18).
    await openControl(p, "west");
    await label.fill("抓手");
    await sending.getByRole("radio", { name: "按住保持", exact: true }).click();
    await expect(shortcut).toHaveAttribute("data-keys", "");
    await expect(drawer.getByRole("textbox", { name: "快捷键" })).toHaveCount(
      0,
    );
    await openControl(p, "east");
    await label.fill("画笔");
    await sending.getByRole("radio", { name: "单次点按", exact: true }).click();
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
        press: { type: "keys", keys: "F16", mode: "hold" },
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
