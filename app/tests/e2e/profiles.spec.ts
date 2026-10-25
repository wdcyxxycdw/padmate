import { mkdir, readdir, readFile, stat, writeFile } from "node:fs/promises";
import { join } from "node:path";
import {
  type ElectronApplication,
  expect,
  type Locator,
  type Page,
  test,
} from "@playwright/test";
import type { StatusResult } from "../../src/shared/api";
import {
  cleanup,
  type Fixture,
  fixture,
  launch,
  rpc,
  setWindowSize,
} from "./helpers";

// A missing control fails its step with the locator instead of the whole
// test running out of time without cleanup.
test.use({ actionTimeout: 15000 });

const pretty = (value: unknown) => `${JSON.stringify(value, null, 2)}\n`;

// The rule the page starts with. Its device field is a member the page
// never edits; it must survive every write.
const notepadRule = { exe: "notepad.exe", profile: "other", device: "any" };

// What config.json keeps: every field the page does not change must come
// back unchanged and in place.
const kept = {
  $schema: "https://padmate.invalid/config.schema.json",
  schemaVersion: 0,
  // Off, so the profile in use stays whatever window is in front.
  enabled: false,
  releaseOnSwitch: "never",
  restoreLayerState: false,
  rules: [notepadRule],
  timing: { holdMs: 320 },
  output: { sendToOwnWindow: false },
};
// An older file also has what ADR-0053 retired: the page shows none of it
// and its first write drops it.
const seed = {
  $schema: kept.$schema,
  schemaVersion: 0,
  enabled: false,
  onUnmatched: "keep",
  releaseOnSwitch: "never",
  restoreLayerState: false,
  defaultProfile: "paint",
  ignore: ["explorer.exe"],
  rules: [
    notepadRule,
    { exe: "C:\\Tools\\mspaint.exe", profile: null, device: "any" },
  ],
  timing: kept.timing,
  output: kept.output,
};

async function profilesFixture(): Promise<Fixture> {
  const f = await fixture();
  const data = join(f.path, "data");
  await mkdir(join(data, "profiles", "other"));
  await writeFile(
    join(data, "profiles", "other", "profile.json"),
    pretty({ schemaVersion: 0, name: "Other profile", layers: { base: {} } }),
  );
  // A file the profile does not reference; deleting the profile deletes it.
  await writeFile(join(data, "profiles", "other", "notes.ts"), "export {};\n");
  await writeFile(join(data, "config.json"), pretty(seed));
  // The programs the 从已安装的软件中选择… dialog lists, instead of the real
  // Start Menu (UI-46): names and the empty files their shortcuts lead to.
  // The uninstaller is left out of the list.
  const apps = join(f.path, "apps");
  await mkdir(apps);
  const programs = [
    { name: "Krita", file: "krita.exe" },
    { name: "Paint Studio", file: "paintstudio.exe" },
    { name: "Uninstall Krita", file: "unins000.exe" },
  ];
  for (const program of programs) await writeFile(join(apps, program.file), "");
  await writeFile(
    join(f.path, "apps.json"),
    JSON.stringify(
      programs.map((program) => ({
        name: program.name,
        path: join(apps, program.file),
      })),
    ),
  );
  f.env.PADMATE_UI_APPS_FILE = join(f.path, "apps.json");
  return f;
}

/**
 * 添加软件 → 从已安装的软件中选择… in scope, then the app named name (a
 * row's name starts with it, then its program name).
 */
async function addInstalled(
  page: Page,
  scope: Locator,
  name: string,
): Promise<void> {
  await scope.getByRole("button", { name: "添加软件", exact: true }).click();
  await page
    .getByRole("menuitem", { name: "从已安装的软件中选择…", exact: true })
    .click();
  const dialog = page.getByRole("dialog", { name: "选择软件" });
  await expect(dialog).toBeVisible();
  await dialog.getByRole("button", { name: new RegExp(`^${name}`) }).click();
  await expect(dialog).toHaveCount(0);
}

/**
 * 添加软件 → 浏览… in scope, with the file dialog answering path (no real
 * dialog): the .exe is bound by its name.
 */
async function browseProgram(
  app: ElectronApplication,
  page: Page,
  scope: Locator,
  path: string,
): Promise<void> {
  await app.evaluate(({ dialog }, file) => {
    dialog.showOpenDialog = (async () => ({
      canceled: false,
      filePaths: [file],
    })) as typeof dialog.showOpenDialog;
  }, path);
  await scope.getByRole("button", { name: "添加软件", exact: true }).click();
  await page.getByRole("menuitem", { name: "浏览…", exact: true }).click();
}

async function activeProfile(f: Fixture): Promise<string> {
  return ((await rpc(f, "status.get")) as StatusResult).profileId;
}

/** Nothing on the 配置 page may need sideways scrolling. */
async function expectNoSidewaysScroll(page: Page): Promise<void> {
  await expect
    .poll(() =>
      page.evaluate(() =>
        [
          document.documentElement,
          ...document.querySelectorAll(
            ".main-content, .page-content, .page-scroll, .page-column, .profiles-grid, .profiles-grid > li, .card, .row, .profile-card-head, .profile-card-apps, .app-icons",
          ),
        ]
          .filter((element) => element.scrollWidth > element.clientWidth)
          .map((element) => element.className || element.tagName),
      ),
    )
    .toEqual([]);
  // Cards stay inside the column, even where an overflow is visible.
  const outside = await page.evaluate(() => {
    const column = document
      .querySelector(".page-column-profiles")
      ?.getBoundingClientRect();
    if (!column) return ["missing column"];
    return [...document.querySelectorAll(".page-column-profiles .card")]
      .filter((card) => {
        const box = card.getBoundingClientRect();
        return box.left < column.left - 1 || box.right > column.right + 1;
      })
      .map((card) => card.className);
  });
  expect(outside).toEqual([]);
}

/**
 * Every 添加软件 menu opens inside the page: .page-scroll hides sideways
 * overflow, so a menu cut off at a card's right edge needs no scrollbar.
 */
async function expectAddMenusInside(page: Page): Promise<void> {
  const buttons = page.locator(".profiles-grid .app-add");
  const count = await buttons.count();
  for (let index = 0; index < count; index++) {
    const button = buttons.nth(index);
    await button.scrollIntoViewIfNeeded();
    await button.click();
    const menu = page.getByRole("menu", { name: "添加软件" });
    await expect(menu).toBeVisible();
    const outside = await menu.evaluate((element) => {
      const box = element.getBoundingClientRect();
      const clip = element.closest(".page-scroll")?.getBoundingClientRect();
      if (!clip) return "no scroll container";
      return box.left < clip.left || box.right > clip.right
        ? `${box.left}-${box.right} outside ${clip.left}-${clip.right}`
        : "";
    });
    expect(outside).toBe("");
    await page.keyboard.press("Escape");
    await expect(menu).toHaveCount(0);
  }
}

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("配置 page creates, binds, moves, switches and deletes profiles through config.json, with the fixed default first", async ({}, info) => {
  test.setTimeout(120000);
  const f = await profilesFixture();
  const data = join(f.path, "data");
  const configFile = () => readFile(join(data, "config.json"), "utf8");
  const expectConfig = (value: unknown) =>
    expect.poll(configFile).toBe(pretty(value));
  let app: ElectronApplication | undefined;
  let page: Page | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    page = opened.page;
    const p = page;
    const card = (id: string) => p.locator(`li[data-profile-id="${id}"]`);
    // A bound app is an icon; its name is the button's name and tooltip.
    const icon = (id: string, name: string) =>
      card(id).getByRole("button", { name, exact: true });
    const rail = (name: string) =>
      p.getByRole("navigation").getByRole("button", { name, exact: true });
    expect(await activeProfile(f)).toBe("paint");

    // The page: one card per profile, the fixed default first, then in the
    // order the profiles were created (paint before other), not the active
    // one first.
    await rail("配置").click();
    await expect(rail("配置")).toHaveAttribute("aria-current", "page");
    await expect(p.locator(".profiles-grid > li")).toHaveCount(3);
    await expect(p.locator(".profiles-grid > li").nth(0)).toHaveAttribute(
      "data-profile-id",
      "default",
    );
    await expect(p.locator(".profiles-grid > li").nth(1)).toHaveAttribute(
      "data-profile-id",
      "paint",
    );
    await expect(
      card("default").getByRole("heading", { name: "默认" }),
    ).toBeVisible();
    await expect(card("default")).toContainText("未绑定的软件使用此配置");
    await expect(
      card("default").getByRole("button", { name: "添加软件", exact: true }),
    ).toHaveCount(0);
    await card("default")
      .getByRole("button", { name: "「默认」的更多操作", exact: true })
      .click();
    await expect(p.getByRole("menuitem")).toHaveText([
      "配置设置…",
      "复制",
      "导出…",
    ]);
    await p.keyboard.press("Escape");
    await expect(
      card("paint").getByRole("heading", { name: "E2E paint" }),
    ).toBeVisible();
    await expect(
      card("paint").getByText("使用中", { exact: true }),
    ).toBeVisible();
    await expect(card("paint").locator(".app-icon")).toHaveCount(0);
    await expect(icon("other", "notepad.exe")).toBeVisible();
    // No path is known for notepad.exe, and the test's own apps list has none
    // (the registry is not read then): the generic application icon stands
    // in, never a letter.
    await expect(
      icon("other", "notepad.exe").locator(".app-icon-generic svg"),
    ).toBeVisible();
    await expect(icon("other", "notepad.exe")).not.toContainText("N");
    // Nothing of what ADR-0053 retired: no list of other apps, no apps
    // where padmate is off, no apps that never switch, no default tag.
    for (const text of ["其他软件", "停用 padmate", "不切换", "高级"])
      await expect(p.getByText(text)).toHaveCount(0);
    await expect(p.getByText("mspaint.exe")).toHaveCount(0);
    await expect(p.locator(".tag", { hasText: "默认" })).toHaveCount(0);
    expect(await p.locator("body").innerText()).not.toMatch(/层/);

    // The switch writes config.enabled at once and keeps everything else;
    // the retired members go with the first write.
    const switching = p.getByRole("switch", { name: "按软件自动切换配置" });
    await expect(switching).toHaveAttribute("aria-checked", "false");
    await switching.click();
    await expectConfig({ ...kept, enabled: true });
    await expect(p.locator(".page-column-profiles")).toHaveAttribute(
      "data-switching",
      "on",
    );
    await switching.click();
    await expectConfig(kept);
    await expect(p.locator(".page-column-profiles")).toHaveAttribute(
      "data-switching",
      "off",
    );
    // Switching on looks at the window in front: unless Windows let
    // padmate's own window come to the front, an app no rule binds
    // switched to the default. Start again from paint.
    await rpc(f, "profiles.activate", { id: "paint" });
    await expect.poll(() => activeProfile(f)).toBe("paint");

    // 新建配置 from blank: a non-ASCII name gets the fallback library ID.
    // The name field has no placeholder, and a name in use is refused.
    await p.getByRole("button", { name: "新建配置", exact: true }).click();
    const create = p.getByRole("dialog", { name: "新建配置" });
    await expect(create).toBeVisible();
    await expect(
      create.getByRole("radio", { name: "空白", exact: true }),
    ).toHaveAttribute("aria-checked", "true");
    await expect(create.getByRole("textbox")).not.toHaveAttribute(
      "placeholder",
    );
    await create.getByRole("button", { name: "新建", exact: true }).click();
    await expect(create.getByText("名称不能为空")).toBeVisible();
    for (const taken of [" e2e PAINT ", "默认"]) {
      await create.getByRole("textbox").fill(taken);
      await expect(create.getByText("已有同名配置")).toBeVisible();
    }
    await create.getByRole("textbox").fill("测试");
    await create.getByRole("button", { name: "新建", exact: true }).click();
    await expect(create).toHaveCount(0);
    await expect(
      card("profile").getByRole("heading", { name: "测试" }),
    ).toBeVisible();
    await expect(card("profile").locator(".app-icon")).toHaveCount(0);
    await expect(
      card("profile").getByRole("button", { name: "添加软件", exact: true }),
    ).toBeFocused();
    expect(
      JSON.parse(
        await readFile(
          join(data, "profiles", "profile", "profile.json"),
          "utf8",
        ),
      ),
    ).toEqual({ schemaVersion: 1, name: "测试", layers: { base: {} } });
    expect(await configFile()).toBe(pretty(kept));

    // The 添加软件 menu has three ways, and none types a program name.
    await card("profile")
      .getByRole("button", { name: "添加软件", exact: true })
      .click();
    await expect(p.getByRole("menuitem")).toHaveText([
      "点选窗口…",
      "从已安装的软件中选择…",
      "浏览…",
    ]);
    await expect(p.getByText("手动输入")).toHaveCount(0);
    // The installed apps: a blocking dialog with a search, the Start Menu's
    // programs by name without the uninstaller.
    await p
      .getByRole("menuitem", { name: "从已安装的软件中选择…", exact: true })
      .click();
    const installed = p.getByRole("dialog", { name: "选择软件" });
    await expect(installed).toBeVisible();
    await expect(p.locator("dialog[open]")).toHaveCount(1);
    const search = installed.getByRole("searchbox", { name: "搜索" });
    await expect(search).toBeFocused();
    await expect(search).not.toHaveAttribute("placeholder");
    await expect(
      installed.getByRole("button", { name: /^Krita\s*krita\.exe/ }),
    ).toBeVisible();
    await expect(
      installed.getByRole("button", {
        name: /^Paint Studio\s*paintstudio\.exe/,
      }),
    ).toBeVisible();
    await expect(installed.getByText("Uninstall Krita")).toHaveCount(0);
    await p.screenshot({
      path: info.outputPath("profiles-installed-apps.png"),
    });
    await search.fill("paint");
    await expect(installed.getByRole("button", { name: /^Krita/ })).toHaveCount(
      0,
    );
    await search.fill("zzz");
    await expect(installed.getByText("没有匹配的软件")).toBeVisible();
    // Esc in a search field that holds text clears it first, as in any
    // browser; the next Esc ends the dialog. Nothing is bound.
    await p.keyboard.press("Escape");
    await expect(search).toHaveValue("");
    await expect(installed).toBeVisible();
    await p.keyboard.press("Escape");
    await expect(installed).toHaveCount(0);
    expect(await configFile()).toBe(pretty(kept));
    // Choosing binds the program by its file name; the path gives the icon.
    await addInstalled(p, card("profile"), "Krita");
    await expect(icon("profile", "krita.exe")).toBeVisible();
    await expect(icon("profile", "krita.exe")).toHaveAttribute(
      "title",
      "krita.exe",
    );
    await expectConfig({
      ...kept,
      rules: [notepadRule, { exe: "krita.exe", profile: "profile" }],
    });
    // Once the profile has it, the list no longer offers it.
    await card("profile")
      .getByRole("button", { name: "添加软件", exact: true })
      .click();
    await p
      .getByRole("menuitem", { name: "从已安装的软件中选择…", exact: true })
      .click();
    await expect(
      p
        .getByRole("dialog", { name: "选择软件" })
        .getByRole("button", { name: /^Paint Studio/ }),
    ).toBeVisible();
    await expect(
      p
        .getByRole("dialog", { name: "选择软件" })
        .getByRole("button", { name: /^Krita/ }),
    ).toHaveCount(0);
    await p.keyboard.press("Escape");

    // Binding it to another profile asks, then moves the one rule.
    await addInstalled(p, card("other"), "Krita");
    const move = p.getByRole("dialog", { name: "此软件已绑定" });
    await expect(move).toContainText(
      "krita.exe 当前使用「测试」，是否改为「Other profile」？",
    );
    await p.screenshot({ path: info.outputPath("profiles-move-dialog.png") });
    await move.getByRole("button", { name: "更改", exact: true }).click();
    await expect(move).toHaveCount(0);
    await expect(icon("other", "krita.exe")).toBeVisible();
    await expect(icon("profile", "krita.exe")).toHaveCount(0);
    const moved = {
      ...kept,
      rules: [notepadRule, { exe: "krita.exe", profile: "other" }],
    };
    await expectConfig(moved);

    // The same program twice on one profile changes nothing: browsing to
    // the same file says so.
    await browseProgram(
      app,
      p,
      card("other"),
      join(f.path, "apps", "krita.exe"),
    );
    await expect(p.locator(".profiles-pill")).toContainText("已在列表中");
    await expect(move).toHaveCount(0);
    expect(await configFile()).toBe(pretty(moved));

    // The icon's menu turns the binding off (greyed) and on again.
    await icon("other", "krita.exe").click();
    await expect(p.getByRole("menuitem")).toHaveText(["停用", "解除绑定"]);
    await p.getByRole("menuitem", { name: "停用", exact: true }).click();
    await expectConfig({
      ...kept,
      rules: [
        notepadRule,
        { exe: "krita.exe", profile: "other", enabled: false },
      ],
    });
    const off = icon("other", "krita.exe 已停用");
    await expect(off).toHaveAttribute("title", "krita.exe（已停用）");
    await expect(
      card("other").locator('.app-icon[data-enabled="false"]'),
    ).toHaveCount(1);
    await off.click();
    await p.getByRole("menuitem", { name: "启用", exact: true }).click();
    await expectConfig(moved);

    // 点选窗口… waits in a dialog that blocks the page; Esc ends it.
    await card("profile")
      .getByRole("button", { name: "添加软件", exact: true })
      .click();
    await p.getByRole("menuitem", { name: "点选窗口…", exact: true }).click();
    const pick = p.getByRole("dialog", { name: "点选窗口" });
    await expect(pick).toContainText("请切换到要绑定的软件，并点击其窗口。");
    await expect(p.locator("dialog[open]")).toHaveCount(1);
    await p.screenshot({ path: info.outputPath("profiles-pick-window.png") });
    await p.keyboard.press("Escape");
    await expect(pick).toHaveCount(0);
    expect(await configFile()).toBe(pretty(moved));

    // Screenshots at the default and the smallest window size.
    await expect(p.locator(".profiles-pill")).toBeEmpty({ timeout: 10000 });
    await setWindowSize(app, 1180, 800);
    await expectNoSidewaysScroll(p);
    await expectAddMenusInside(p);
    await p.screenshot({ path: info.outputPath("profiles-1180x800.png") });
    await setWindowSize(app, 850, 620);
    await expectNoSidewaysScroll(p);
    await expectAddMenusInside(p);
    await p.screenshot({ path: info.outputPath("profiles-850x620.png") });
    await p
      .locator(".page-scroll")
      .evaluate((element) => element.scrollTo(0, element.scrollHeight));
    await expectNoSidewaysScroll(p);
    await p.screenshot({
      path: info.outputPath("profiles-850x620-bottom.png"),
    });
    await setWindowSize(app, 1180, 800);

    // The topbar menu on the mapping page switches the active profile.
    await rail("映射").click();
    await p.getByTestId("profile-menu").click();
    await expect(
      p.getByRole("menu").getByText("切换配置", { exact: true }),
    ).toBeVisible();
    await expect(
      p.getByRole("menuitemradio", { name: "E2E paint", exact: true }),
    ).toHaveAttribute("aria-checked", "true");
    await expect(
      p.getByRole("menuitemradio", { name: "默认", exact: true }),
    ).toHaveAttribute("aria-checked", "false");
    await p.getByRole("menuitemradio", { name: "测试", exact: true }).click();
    await expect.poll(() => activeProfile(f)).toBe("profile");
    await expect(p.getByTestId("profile-menu")).toContainText("测试");
    await p.getByTestId("profile-menu").click();
    await expect(
      p.getByRole("menuitemradio", { name: "测试", exact: true }),
    ).toHaveAttribute("aria-checked", "true");
    await p.getByTestId("manage-profiles").click();
    await expect(rail("配置")).toHaveAttribute("aria-current", "page");
    // The active profile keeps its place: 测试 was created last.
    await expect(p.locator(".profiles-grid > li").nth(3)).toHaveAttribute(
      "data-profile-id",
      "profile",
    );
    await expect(
      card("profile").getByText("使用中", { exact: true }),
    ).toBeVisible();
    await expect(
      card("paint").getByText("使用中", { exact: true }),
    ).toHaveCount(0);
    expect(await configFile()).toBe(pretty(moved));

    // 删除 a profile that is not in use: the confirmation starts on 取消,
    // the folder is deleted for good (no trash/), and every rule that
    // selects it is removed in the same write.
    await card("other")
      .getByRole("button", { name: "「Other profile」的更多操作", exact: true })
      .click();
    await p.getByRole("menuitem", { name: "删除…", exact: true }).click();
    const remove = p.getByRole("dialog", { name: "删除「Other profile」？" });
    await expect(remove).toContainText(
      "删除后无法恢复，绑定的软件将一并解除。",
    );
    await expect(
      remove.getByRole("button", { name: "取消", exact: true }),
    ).toBeFocused();
    await remove.getByRole("button", { name: "删除", exact: true }).click();
    await expect(remove).toHaveCount(0);
    await expect(card("other")).toHaveCount(0);
    await expect(p.locator(".profiles-grid > li")).toHaveCount(3);
    const cleaned = { ...kept, rules: [] };
    await expectConfig(cleaned);
    await expect(stat(join(data, "profiles", "other"))).rejects.toThrow();
    await expect(stat(join(data, "trash"))).rejects.toThrow();
    const left = await readdir(join(data, "profiles"));
    expect(left).not.toContain("other");
    expect(left.filter((name) => name.startsWith("."))).toEqual([]);
    expect(await activeProfile(f)).toBe("profile");

    // 编辑按键 on the fixed default activates it like any profile.
    await card("default")
      .getByRole("button", { name: "编辑按键", exact: true })
      .click();
    await expect.poll(() => activeProfile(f)).toBe("default");
    await expect(rail("映射")).toHaveAttribute("aria-current", "page");
    await expect(p.getByTestId("profile-menu")).toContainText("默认");
    await rail("配置").click();

    // 编辑按键 on another profile activates it and opens the mapping page.
    await card("paint")
      .getByRole("button", { name: "编辑按键", exact: true })
      .click();
    await expect.poll(() => activeProfile(f)).toBe("paint");
    await expect(rail("映射")).toHaveAttribute("aria-current", "page");
    await expect(p.getByTestId("profile-menu")).toContainText("E2E paint");

    // 新建配置… from the topbar creates the profile and switches to it.
    await p.getByTestId("profile-menu").click();
    await p.getByTestId("new-profile").click();
    const topbarCreate = p.getByRole("dialog", { name: "新建配置" });
    await topbarCreate.getByRole("textbox").fill("Krita");
    await topbarCreate
      .getByRole("button", { name: "新建", exact: true })
      .click();
    await expect(topbarCreate).toHaveCount(0);
    await expect.poll(() => activeProfile(f)).toBe("krita");
    expect(
      JSON.parse(
        await readFile(join(data, "profiles", "krita", "profile.json"), "utf8"),
      ),
    ).toEqual({ schemaVersion: 1, name: "Krita", layers: { base: {} } });

    // With mapping off, the mapping page offers to create or choose one.
    await p.evaluate(() => window.padmate.activateProfile(null));
    await expect.poll(() => activeProfile(f)).toBe("");
    await expect(p.getByText("没有启用配置", { exact: true })).toBeVisible();
    await expect(
      p.getByRole("button", { name: "新建配置", exact: true }),
    ).toBeVisible();
    await p.getByRole("button", { name: "选择配置", exact: true }).click();
    await p.getByRole("menuitemradio", { name: "测试", exact: true }).click();
    await expect.poll(() => activeProfile(f)).toBe("profile");
    await expect(p.getByText("没有启用配置", { exact: true })).toHaveCount(0);

    // Activation never writes config.json.
    expect(await configFile()).toBe(pretty(cleaned));
  } catch (error) {
    console.error(
      "Page text:",
      await page?.locator("body").innerText({ timeout: 2000 }).catch(String),
    );
    console.error("config.json:", await configFile().catch(String));
    throw error;
  } finally {
    await cleanup(f, app);
  }
});

/** The files of a profile folder, relative and sorted, without .history. */
async function folderFiles(dir: string, prefix = ""): Promise<string[]> {
  const files: string[] = [];
  for (const entry of await readdir(join(dir, prefix), {
    withFileTypes: true,
  })) {
    const name = prefix ? `${prefix}/${entry.name}` : entry.name;
    if (entry.name === ".history") continue;
    if (entry.isDirectory()) files.push(...(await folderFiles(dir, name)));
    else files.push(name);
  }
  return files.sort();
}

// State the stubbed Electron dialogs keep in the main process.
interface ArchiveStubs {
  saveOptions?: { title?: string; defaultPath?: string; filters?: unknown };
  openOptions?: { title?: string; filters?: unknown };
  nextFile?: string;
}

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("导出 and 导入 back up one profile with its scripts; saved versions stay out of the UI", async ({}, info) => {
  test.setTimeout(90000);
  const f = await fixture();
  const data = join(f.path, "data");
  const folder = join(data, "profiles", "paint");
  const file = join(folder, "profile.json");
  const history = join(folder, ".history");
  const backup = join(f.path, "backup");
  await mkdir(join(folder, "scripts"), { recursive: true });
  await mkdir(backup);
  // A script and a note: the export carries every file of the folder.
  await writeFile(join(folder, "scripts", "brush.ts"), "export {};\n");
  await writeFile(join(folder, "notes.md"), "# 笔记\n");
  // A script of the library (SCR-51) that a key of the profile runs: the
  // export carries it, and an import puts it back into the library.
  const library = join(data, "macros");
  const brush = "export default () => {};\n// v1\n";
  await mkdir(library, { recursive: true });
  await writeFile(join(library, "画笔.ts"), brush);
  const stored = JSON.parse(await readFile(file, "utf8"));
  stored.layers.base.north = { tap: { type: "macro", file: "画笔.ts" } };
  await writeFile(file, JSON.stringify(stored));
  let app: ElectronApplication | undefined;
  let page: Page | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    page = opened.page;
    const p = page;
    const rail = (name: string) =>
      p.getByRole("navigation").getByRole("button", { name, exact: true });
    const card = (id: string) => p.locator(`li[data-profile-id="${id}"]`);
    // No real file dialog: the save dialog writes into the test's folder
    // under the name it suggests, the open dialog returns nextFile.
    await app.evaluate(({ dialog }, dir) => {
      const stubs = globalThis as unknown as ArchiveStubs;
      dialog.showSaveDialog = (async (_window: unknown, options?: unknown) => {
        const chosen = options as ArchiveStubs["saveOptions"];
        stubs.saveOptions = chosen;
        return {
          canceled: false,
          filePath: `${dir}\\${chosen?.defaultPath ?? "x.padmate"}`,
        };
      }) as typeof dialog.showSaveDialog;
      dialog.showOpenDialog = (async (_window: unknown, options?: unknown) => {
        stubs.openOptions = options as ArchiveStubs["openOptions"];
        return stubs.nextFile
          ? { canceled: false, filePaths: [stubs.nextFile] }
          : { canceled: true, filePaths: [] };
      }) as typeof dialog.showOpenDialog;
    }, backup);
    const choose = (path?: string) =>
      app?.evaluate((_electron, next) => {
        (globalThis as unknown as ArchiveStubs).nextFile = next;
      }, path);

    // An edit on the mapping page is saved at once; padmated still keeps
    // the version it replaced, but no page lists or restores it (F9).
    const label = p
      .getByTestId("mapping-editor")
      .getByRole("textbox", { name: "在手柄图上显示为" });
    await p.locator('button[data-callout="south"]').click();
    await expect(label).toBeVisible();
    await label.fill("撤销一步");
    await label.press("Enter");
    await expect.poll(() => readFile(file, "utf8")).toContain("撤销一步");
    await expect.poll(async () => (await readdir(history)).length).toBe(1);
    await rail("配置").click();
    await card("paint")
      .getByRole("button", { name: "「E2E paint」的更多操作", exact: true })
      .click();
    await expect(p.getByRole("menuitem")).toHaveText([
      "配置设置…",
      "重命名…",
      "复制",
      "导出…",
      "删除…",
    ]);

    // 导出… saves one .padmate file named after the profile.
    await p.getByRole("menuitem", { name: "导出…", exact: true }).click();
    await expect(p.locator(".profiles-pill")).toContainText(
      "已导出「E2E paint」",
    );
    const exported = join(backup, "E2E paint.padmate");
    const archive = await readFile(exported);
    expect(archive.subarray(0, 2).toString("latin1")).toBe("PK");
    const options = (await app.evaluate(
      () => (globalThis as unknown as ArchiveStubs).saveOptions,
    )) as ArchiveStubs["saveOptions"];
    expect(options?.title).toBe("导出配置 / Export profile");
    expect(options?.defaultPath).toBe("E2E paint.padmate");
    // The script the profile runs is in the file, under macros/.
    expect(archive.includes(Buffer.from("macros/画笔.ts", "utf8"))).toBe(true);
    const original = await folderFiles(folder);
    expect(original).toEqual(["notes.md", "profile.json", "scripts/brush.ts"]);

    // 导入… of a file whose name a profile has asks first: 另存为 adds a
    // new profile with every file of the backup.
    await choose(exported);
    await p.getByRole("button", { name: "导入…", exact: true }).click();
    const clash = p.getByRole("dialog", {
      name: "已有名为「E2E paint」的配置",
    });
    await expect(clash).toBeVisible();
    const newName = clash.getByRole("textbox");
    await expect(newName).toHaveValue("E2E paint 2");
    await expect(newName).toBeFocused();
    await p.screenshot({ path: info.outputPath("import-clash.png") });
    await newName.fill("E2E paint");
    await expect(clash).toContainText("已有同名配置");
    await expect(
      clash.getByRole("button", { name: "另存为", exact: true }),
    ).toBeDisabled();
    await newName.fill("备份的配置");
    await newName.press("Enter");
    await expect(clash).toHaveCount(0);
    await expect(p.locator(".profiles-pill")).toContainText(
      "已导入「备份的配置」",
    );
    const added = (
      (await rpc(f, "profiles.list")) as { id: string; name?: string }[]
    ).find((entry) => entry.name === "备份的配置");
    expect(added?.id).toBeTruthy();
    const copy = join(data, "profiles", added?.id ?? "missing");
    expect(await folderFiles(copy)).toEqual(original);
    expect(await readFile(join(copy, "scripts", "brush.ts"), "utf8")).toBe(
      "export {};\n",
    );
    // The library already has the script with these contents: it is reused,
    // nothing asks, and no copy appears.
    expect(await readdir(library)).not.toContain("画笔 2.ts");
    await expect(
      card(added?.id ?? "missing").getByRole("heading", {
        name: "备份的配置",
      }),
    ).toBeVisible();

    // 替换 overwrites the profile in use with the backup: a later edit and
    // a later file go, the replaced profile.json is kept as a version, and
    // the mapping page shows the backup at once.
    await rail("映射").click();
    await p.locator('button[data-callout="south"]').click();
    await expect(label).toBeVisible();
    await label.fill("撤销两步");
    await label.press("Enter");
    await expect.poll(() => readFile(file, "utf8")).toContain("撤销两步");
    await expect.poll(async () => (await readdir(history)).length).toBe(2);
    await writeFile(join(folder, "scripts", "extra.ts"), "export {};\n");
    // The library's script changed since the backup: the import asks what
    // to do with the two (nothing is written before the answer).
    const changed = "export default () => {};\n// v2\n";
    await writeFile(join(library, "画笔.ts"), changed);
    await rail("配置").click();
    await p.getByRole("button", { name: "导入…", exact: true }).click();
    await expect(clash).toBeVisible();
    await clash.getByRole("button", { name: "替换", exact: true }).click();
    await expect(clash).toHaveCount(0);
    const scripts = p.getByRole("dialog", {
      name: "已有脚本「画笔」，内容不同",
    });
    await expect(scripts).toBeVisible();
    await expect(
      scripts.getByRole("button", { name: "都保留", exact: true }),
    ).toBeFocused();
    await p.screenshot({ path: info.outputPath("import-scripts.png") });
    await scripts.getByRole("button", { name: "都保留", exact: true }).click();
    await expect(scripts).toHaveCount(0);
    await expect(p.locator(".profiles-pill")).toContainText(
      "已导入「E2E paint」",
    );
    expect(await folderFiles(folder)).toEqual(original);
    expect(await readFile(file, "utf8")).toContain("撤销一步");
    // Both scripts are in the library: the library's own is untouched, the
    // backup's is numbered, and the imported profile runs the numbered one.
    expect(await readFile(join(library, "画笔.ts"), "utf8")).toBe(changed);
    expect(await readFile(join(library, "画笔 2.ts"), "utf8")).toBe(brush);
    expect(
      JSON.parse(await readFile(file, "utf8")).layers.base.north.tap,
    ).toEqual({
      type: "macro",
      file: "画笔 2.ts",
    });
    await expect.poll(async () => (await readdir(history)).length).toBe(3);
    await rail("映射").click();
    await expect(p.locator('button[data-callout="south"]')).toContainText(
      "撤销一步",
    );
    await rail("配置").click();

    // A file that is not a backup is refused with one line.
    const bogus = join(backup, "bogus.padmate");
    await writeFile(bogus, "not a zip");
    await choose(bogus);
    await p.getByRole("button", { name: "导入…", exact: true }).click();
    await expect(p.locator(".shell-notice[role=alert]")).toContainText(
      "不是 padmate 配置文件，或文件已损坏",
    );
    const opening = (await app.evaluate(
      () => (globalThis as unknown as ArchiveStubs).openOptions,
    )) as ArchiveStubs["openOptions"];
    expect(opening?.title).toBe("导入配置 / Import profile");
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
