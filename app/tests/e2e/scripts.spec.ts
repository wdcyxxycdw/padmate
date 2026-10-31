import { access, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import {
  type ElectronApplication,
  expect,
  type Page,
  test,
} from "@playwright/test";
import { FixtureClient } from "./fixture-client";
import {
  cleanup,
  type Fixture,
  fixture,
  launch,
  rpc,
  setWindowSize,
} from "./helpers";

// State the stubbed open dialog keeps in the main process.
interface Stubs {
  nextScript?: string;
}

// Scripts (SCR-50): the editor is recorded (PADMATE_UI_EDITOR_LOG), the
// open dialog returns the file the test chose, and the fake controller runs
// the scripts in the daemon's dry run.
async function stubDialogs(app: ElectronApplication) {
  await app.evaluate(({ dialog }) => {
    const stubs = globalThis as unknown as Stubs;
    dialog.showOpenDialog = (async () =>
      stubs.nextScript
        ? { canceled: false, filePaths: [stubs.nextScript] }
        : { canceled: true, filePaths: [] }) as typeof dialog.showOpenDialog;
  });
  return (path?: string) =>
    app.evaluate((_electron, next) => {
      (globalThis as unknown as Stubs).nextScript = next;
    }, path);
}

const editorLog = (f: Fixture) => join(f.path, "editor.log");
const opened = async (f: Fixture) =>
  (await readFile(editorLog(f), "utf8").catch(() => ""))
    .split("\n")
    .filter(Boolean);

const card = (page: Page, name: string) =>
  page
    .locator("article")
    .filter({ has: page.getByRole("heading", { name, exact: true }) });

async function menu(page: Page, name: string, item: string) {
  await card(page, name).getByTestId("script-menu").click();
  await page.getByRole("menuitem", { name: item, exact: true }).click();
}

async function expectPageFits(page: Page) {
  await expect
    .poll(() =>
      page.evaluate(() =>
        [
          document.documentElement,
          ...document.querySelectorAll(
            ".main-content, .page-content, .page-column, .page-macro",
          ),
        ]
          .filter(
            (element) =>
              element.scrollWidth > element.clientWidth ||
              element.scrollHeight > element.clientHeight,
          )
          .map((element) => element.className || element.tagName),
      ),
    )
    .toEqual([]);
}

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("scripts are created, bound to a key, imported, renamed and deleted", async ({}, info) => {
  test.setTimeout(120000);
  const f = await fixture();
  let app: ElectronApplication | undefined;
  let client: FixtureClient | undefined;
  try {
    const launched = await launch(f);
    app = launched.app;
    const page = launched.page;
    const choose = await stubDialogs(app);
    const daemon = await FixtureClient.connect(f);
    client = daemon;
    const folder = join(f.path, "data", "profiles", "paint");
    // The script library (SCR-51): one folder for every profile.
    const macros = join(f.path, "data", "macros");
    const readMacro = (name: string) => readFile(join(macros, name), "utf8");
    const profile = async () =>
      JSON.parse(await readFile(join(folder, "profile.json"), "utf8"));
    const chords = () =>
      daemon.events
        .filter((event) => event.topic === "output.preview")
        .map((event) => String(event.data.chord));

    await page.getByRole("button", { name: "脚本", exact: true }).click();
    await expect(page.getByRole("heading", { name: "暂无脚本" })).toBeVisible();
    await page.screenshot({ path: info.outputPath("scripts-empty.png") });

    // 1. New: the daemon writes the file and the editor files; it opens in
    // the (recorded) editor and is not used yet.
    await page.getByRole("button", { name: "新建", exact: true }).click();
    const create = page.getByRole("dialog", { name: "新建脚本" });
    const name = create.getByRole("textbox", { name: "名称" });
    await expect(name).toHaveValue("脚本 1");
    await name.fill("复制全部");
    await create.getByRole("button", { name: "新建", exact: true }).click();
    await expect(create).toBeHidden();
    await expect(card(page, "复制全部")).toContainText("未使用");
    expect(await readMacro("复制全部.ts")).toContain('from "padmate"');
    for (const file of [
      "tsconfig.json",
      ".padmate/padmate.d.ts",
      ".padmate/math.d.ts",
      ".padmate/globals.d.ts",
      ".padmate/lib.padmate-goja.d.ts",
    ])
      await access(join(macros, file));
    await expect.poll(() => opened(f)).toEqual([join(macros, "复制全部.ts")]);

    // 2. The drawer binds it to A's 点按 (the key's only mapping, so a press
    // in the file: ADR-0076), replacing its shortcut, saved at once (UI-43);
    // the fake controller runs it, and the mapping page's key preview names
    // it.
    await page.getByRole("button", { name: "映射", exact: true }).click();
    await page.locator('button[data-callout="south"]').click();
    const drawer = page.getByTestId("mapping-editor");
    await drawer
      .getByTestId("action-type")
      .getByRole("radio", { name: "脚本", exact: true })
      .click();
    await drawer
      .getByRole("combobox", { name: "选择脚本" })
      .selectOption({ label: "复制全部" });
    await expect
      .poll(async () => (await profile()).layers.base.south.press)
      .toEqual({ type: "macro", file: "复制全部.ts" });
    // released runs right after the release: the key preview shows the
    // decision then and fades after about 3 s, before a slow build ends.
    const runs = async (released?: () => Promise<void>) => {
      daemon.events.length = 0;
      await daemon.call("fake.press", { control: "south" });
      await daemon.call("fake.release", { control: "south" });
      await released?.();
      // Building on a busy machine can take a while.
      await expect
        .poll(() => chords().filter((chord) => /Ctrl\+[AC]$/.test(chord)), {
          timeout: 30000,
        })
        .toEqual([
          expect.stringMatching(/Ctrl\+A$/),
          expect.stringMatching(/Ctrl\+C$/),
        ]);
    };
    // The fake pad is a standard one: south is printed S.
    const preview = page.getByTestId("key-preview");
    await runs(async () => {
      await expect(preview).toHaveText("S → 复制全部");
      // One drawing of the control (UI-44) and one of the script (SCR-50).
      // The fake pad is a standard one: its face keys are drawn by position,
      // with the pressed one lit (ADR-0079 §7).
      const control = preview.locator(".control-icon");
      await expect(control).toHaveCount(1);
      await expect(control).toHaveAttribute("data-kind", "art");
      await expect(
        control.locator('svg[data-pad-icon="kenney/switch_buttons_down"]'),
      ).toHaveCount(1);
      await expect(preview.locator("svg.key-preview-icon")).toHaveCount(1);
    });
    await page.getByRole("button", { name: "脚本", exact: true }).click();
    // Keys are named as the connected (standard) pad prints them, after
    // the profile whose key runs the script.
    await expect(card(page, "复制全部")).toContainText("E2E paint：S 点按");
    // The page is the library, whichever profile is open (SCR-51): the
    // fixed default in use does not hide the script, and the card still
    // names the profile whose key runs it.
    await rpc(f, "profiles.activate", { id: "default" });
    await expect(card(page, "复制全部")).toContainText("E2E paint：S 点按");
    await rpc(f, "profiles.activate", { id: "paint" });
    await expect(card(page, "复制全部")).toContainText("E2E paint：S 点按");
    await page.screenshot({ path: info.outputPath("scripts-used.png") });

    // 3. Import keeps a Chinese name; the same name asks first and writes
    // what Main read before asking; a file that is not UTF-8 is refused.
    const source = join(f.path, "下一个笔刷.ts");
    const first =
      'import { key } from "padmate";\nexport default () => key.tap("F16");\n';
    await writeFile(source, first);
    await choose(source);
    await page.getByRole("button", { name: "导入", exact: true }).click();
    await expect(card(page, "下一个笔刷")).toContainText("未使用");
    expect(await readMacro("下一个笔刷.ts")).toBe(first);
    const second = first.replace("F16", "F17");
    await writeFile(source, second);
    await page.getByRole("button", { name: "导入", exact: true }).click();
    const replace = page.getByRole("dialog", {
      name: "「下一个笔刷」已存在，是否替换？",
    });
    await expect(replace).toBeVisible();
    await writeFile(source, `${second}// changed after the dialog opened\n`);
    await replace.getByRole("button", { name: "替换", exact: true }).click();
    await expect(replace).toBeHidden();
    await expect.poll(() => readMacro("下一个笔刷.ts")).toBe(second);
    const binary = join(f.path, "binary.ts");
    await writeFile(binary, Buffer.from([0x2f, 0x2f, 0xff, 0xfe]));
    await choose(binary);
    await page.getByRole("button", { name: "导入", exact: true }).click();
    await expect(page.getByTestId("script-outcome")).toHaveText(
      "文件不是 UTF-8 编码",
    );
    await choose(undefined);

    // 4. Rename: the file and the reference follow; the key still runs it.
    await menu(page, "复制全部", "重命名");
    const rename = page.getByRole("dialog", { name: "重命名" });
    await rename.getByRole("textbox", { name: "名称" }).fill("全选复制");
    await rename.getByRole("button", { name: "确定", exact: true }).click();
    await expect(card(page, "全选复制")).toContainText("S 点按");
    await access(join(macros, "全选复制.ts"));
    await expect(access(join(macros, "复制全部.ts"))).rejects.toThrow();
    expect((await profile()).layers.base.south.press).toEqual({
      type: "macro",
      file: "全选复制.ts",
    });
    await runs();
    // Renaming is no undo step, and the steps before it, made with the old
    // name, are gone: Ctrl+Z on the mapping page keeps the new reference.
    await page.getByRole("button", { name: "映射", exact: true }).click();
    await page.locator('button[data-callout="south"]').focus();
    // A pad press just now would hold Ctrl+Z back.
    await page.waitForTimeout(600);
    await page.keyboard.press("Control+Z");
    await page.waitForTimeout(300);
    expect((await profile()).layers.base.south.press).toEqual({
      type: "macro",
      file: "全选复制.ts",
    });
    await page.getByRole("button", { name: "脚本", exact: true }).click();

    // 5. Delete asks with 取消 focused, then removes the file and the key's
    // script.
    await menu(page, "全选复制", "删除");
    const remove = page.getByRole("dialog", { name: "删除「全选复制」？" });
    await expect(remove).toContainText("有 1 处使用此脚本");
    await expect(
      remove.getByRole("button", { name: "取消", exact: true }),
    ).toBeFocused();
    await page.screenshot({ path: info.outputPath("scripts-delete.png") });
    await remove.getByRole("button", { name: "删除", exact: true }).click();
    await expect(card(page, "全选复制")).toHaveCount(0);
    await expect(access(join(macros, "全选复制.ts"))).rejects.toThrow();
    // The key's only action was the script: the daemon removes the binding
    // with it (profile.RemoveMacroRefs).
    expect((await profile()).layers?.base?.south).toBeUndefined();

    // 6. A syntax error shows on its card; fixed, it goes away (the page
    // reads the list again when the window gets focus back).
    await writeFile(
      join(macros, "出错.ts"),
      "export default function () {\n  key.tap(\n}\n",
    );
    const refocus = () =>
      page.evaluate(() => window.dispatchEvent(new Event("focus")));
    await refocus();
    await expect(card(page, "出错").getByTestId("script-error")).toContainText(
      "出错.ts:",
    );
    await writeFile(join(macros, "出错.ts"), "export default () => {};\n");
    await refocus();
    await expect(card(page, "出错").getByTestId("script-error")).toHaveCount(0);

    // 7 is the legacy script, below. 8. The smallest window: nothing scrolls sideways or overflows, in
    // both languages.
    await setWindowSize(app, 850, 620);
    await expectPageFits(page);
    await page.screenshot({ path: info.outputPath("scripts-850-zh.png") });
    await page.getByRole("button", { name: "设置", exact: true }).click();
    await page
      .getByRole("combobox", { name: "语言", exact: true })
      .selectOption("en");
    await page.getByRole("button", { name: "Scripts", exact: true }).click();
    await expect(card(page, "下一个笔刷")).toContainText("Not used");
    await expectPageFits(page);
    await page.screenshot({ path: info.outputPath("scripts-850-en.png") });
  } catch (error) {
    console.error("Script status", await client?.call("script.status"));
    console.error(
      "Observed output",
      client?.events.filter((event) => event.topic === "output.preview"),
    );
    throw error;
  } finally {
    client?.close();
    await cleanup(f, app);
  }
});

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("a legacy script shows as its own card and can be removed", async ({}, info) => {
  test.setTimeout(90000);
  const f = await fixture();
  const folder = join(f.path, "data", "profiles", "paint");
  await writeFile(
    join(folder, "profile.json"),
    JSON.stringify({
      schemaVersion: 0,
      name: "E2E paint",
      script: { entry: "hello.ts", api: "0.21" },
      layers: {
        base: {
          south: { press: { type: "keys", keys: "Ctrl+Z", mode: "tap" } },
          east: { tap: { type: "script", name: "hello" } },
        },
      },
    }),
  );
  await writeFile(
    join(folder, "hello.ts"),
    `import { defineScript, defineAction, key } from "padmate";
export default defineScript({
  actions: { hello: defineAction({ run() { void key.tap("F15"); } }) },
});
`,
  );
  let app: ElectronApplication | undefined;
  try {
    const launched = await launch(f);
    app = launched.app;
    const page = launched.page;
    await page.getByRole("button", { name: "脚本", exact: true }).click();
    const legacy = page
      .locator("article")
      .filter({ has: page.getByRole("heading", { name: /hello\.ts/ }) });
    // A legacy script says which profile it belongs to.
    await expect(legacy).toContainText("旧式 · E2E paint");
    await expect(legacy).toContainText("随配置持续运行");
    await legacy.getByTestId("script-menu").click();
    await expect(page.getByRole("menuitem")).toHaveText(["编辑", "移除"]);
    await page.getByRole("menuitem", { name: "编辑", exact: true }).click();
    await expect.poll(() => opened(f)).toEqual([join(folder, "hello.ts")]);
    await legacy.getByTestId("script-menu").click();
    await page.getByRole("menuitem", { name: "移除", exact: true }).click();
    const question = page.getByRole("dialog", { name: "移除旧式脚本？" });
    await expect(question).toContainText(
      "配置「E2E paint」将不再运行 hello.ts，文件保留。1 处脚本动作将被清空。",
    );
    await page.screenshot({ path: info.outputPath("legacy-remove.png") });
    await question.getByRole("button", { name: "移除", exact: true }).click();
    await expect(legacy).toHaveCount(0);
    const saved = JSON.parse(
      await readFile(join(folder, "profile.json"), "utf8"),
    );
    expect(saved.script).toBeUndefined();
    expect(saved.layers.base.east).toBeUndefined();
    expect(saved.layers.base.south).toBeDefined();
    await access(join(folder, "hello.ts"));
  } finally {
    await cleanup(f, app);
  }
});
