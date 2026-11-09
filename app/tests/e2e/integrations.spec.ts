import { writeFile } from "node:fs/promises";
import { join } from "node:path";
import {
  type ElectronApplication,
  expect,
  type Locator,
  type Page,
  test,
} from "@playwright/test";
import type { ProfileDocument } from "../../src/shared/api";
import type {
  IntegrationFunction,
  IntegrationFunctions,
  IntegrationItems,
  TreeNode,
} from "../../src/shared/integrations";
import { editorFixture } from "./editor-fixture";
import { FixtureClient } from "./fixture-client";
import { cleanup, type Fixture, launch, recordShortcut } from "./helpers";

// App integrations (ARCHITECTURE §16.5): the key drawer's "从 <软件名> 选择…".
// The private fake daemon registers an in-memory integration, "fakeapp"
// (Demo Paint, DemoPaint.exe): it reads and writes no file and no process.
// `fake.integration {running}` says whether the app runs. The tests do not
// name its functions: they read the list through the daemon and pick from it,
// so they follow the fake's menu.
test.use({ actionTimeout: 15000 });

const APP = "Demo Paint";
const PICK = `从 ${APP} 选择…`;
const INTEGRATION = "fakeapp";

/** A profile bound to Demo Paint: a rule on its exe, with switching on. */
async function integrationFixture(): Promise<Fixture> {
  const f = await editorFixture();
  // The test window itself (electron.exe) is also sent to paint, so the
  // profile in use stays the one under test while switching is on (§16.1: with
  // switching off no profile is bound to anything).
  await writeFile(
    join(f.path, "data", "config.json"),
    JSON.stringify({
      schemaVersion: 0,
      enabled: true,
      rules: [
        { exe: "DemoPaint.exe", profile: "paint" },
        { exe: "electron.exe", profile: "paint" },
      ],
    }),
  );
  return f;
}

async function openControl(page: Page, control: string): Promise<void> {
  await page.locator(`button[data-callout="${control}"]`).click();
  await expect(page.getByTestId("mapping-editor")).toBeVisible();
  await expect(
    page.getByRole("complementary", { name: "按键编辑" }),
  ).toBeVisible();
}

/** profile.json of the paint profile, as the daemon has it on disk. */
async function onDisk(client: FixtureClient) {
  return JSON.parse(
    (await client.call<ProfileDocument>("profiles.get", { id: "paint" })).text,
  );
}

/** The nodes from the top of the tree down to the one that has the function. */
function pathTo(
  nodes: readonly TreeNode[],
  id: string,
): TreeNode[] | undefined {
  for (const node of nodes) {
    if (node.function === id) return [node];
    const below = pathTo(node.children ?? [], id);
    if (below) return [node, ...below];
  }
  return undefined;
}

interface Pick {
  fn: IntegrationFunction;
  path: TreeNode[];
}

/** A function of the fake app that has a key, and one that has none. */
async function pickable(client: FixtureClient): Promise<{
  withKey: Pick;
  withoutKey: Pick;
}> {
  const listing = await client.call<IntegrationFunctions>(
    "integrations.functions",
    { integration: INTEGRATION, language: "zh-CN" },
  );
  const find = (test: (fn: IntegrationFunction) => boolean): Pick => {
    for (const fn of listing.functions) {
      const path = pathTo(listing.tree, fn.id);
      if (test(fn) && path) return { fn, path };
    }
    throw new Error("The fake integration has no such function");
  };
  return {
    // F13 is what the north key sends before any test changes it.
    withKey: find((fn) => fn.keys.length > 0 && fn.keys[0] !== "F13"),
    withoutKey: find((fn) => fn.keys.length === 0),
  };
}

const row = (dialog: Locator, name: string) =>
  dialog.getByRole("treeitem").filter({ hasText: new RegExp(`^\\s*${name}`) });

/**
 * Gets to a function: the setting area (the first node of its path) in the
 * dropdown, when there is one, then the groups above it, one click on the
 * arrow each.
 */
async function reveal(dialog: Locator, pick: Pick): Promise<Locator> {
  const [area, ...below] = pick.path;
  const dropdown = dialog.getByRole("combobox", { name: "设置区域" });
  if (await dropdown.count()) await dropdown.selectOption({ label: area.name });
  for (const node of below.slice(0, -1)) {
    const group = row(dialog, node.name).first();
    if ((await group.getAttribute("aria-expanded")) !== "true")
      await group.locator(".picker-row-chevron").click();
    await expect(group).toHaveAttribute("aria-expanded", "true");
  }
  const leaf = below[below.length - 1] ?? area;
  return row(dialog, leaf.name).first();
}

async function openPicker(page: Page): Promise<Locator> {
  await page
    .getByRole("complementary", { name: "按键编辑" })
    .getByRole("button", { name: PICK, exact: true })
    .click();
  const dialog = page.getByRole("dialog", { name: `从 ${APP} 选择` });
  await expect(dialog.getByRole("tree")).toBeVisible();
  return dialog;
}

const drawerShortcut = (page: Page) =>
  page
    .getByRole("complementary", { name: "按键编辑" })
    .getByRole("group", { name: "快捷键", exact: true });
const drawerLabel = (page: Page) =>
  page.getByRole("textbox", { name: "功能", exact: true });

async function integrationItems(client: FixtureClient) {
  return client.call<IntegrationItems>("integrations.items", {
    profile: "paint",
    integration: INTEGRATION,
  });
}

/** Everything a test finishes with, whatever happened. */
async function finish(
  f: Fixture,
  app: ElectronApplication | undefined,
  client: FixtureClient | undefined,
) {
  client?.close();
  await cleanup(f, app);
}

test("pick a function that has a key: 设置 changes the mapping, and it is still there after the drawer is opened again", async () => {
  test.setTimeout(90000);
  const f = await integrationFixture();
  let app: ElectronApplication | undefined;
  let client: FixtureClient | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    const page = opened.page;
    client = await FixtureClient.connect(f);
    const live = client;
    const { withKey } = await pickable(live);
    const key = withKey.fn.keys[0];
    const name = withKey.fn.name;

    await openControl(page, "north");
    const shortcut = drawerShortcut(page);
    await expect(shortcut).toHaveAttribute("data-keys", "F13");

    const dialog = await openPicker(page);
    // The top level of the menu is closed; a group opens by its arrow.
    for (const top of await dialog
      .locator('[role="treeitem"][aria-level="1"][aria-expanded]')
      .all())
      await expect(top).toHaveAttribute("aria-expanded", "false");
    const leaf = await reveal(dialog, withKey);
    await leaf.click();
    await expect(leaf).toHaveAttribute("aria-selected", "true");
    await dialog.getByRole("button", { name: "设置", exact: true }).click();
    await expect(dialog).toHaveCount(0);

    // The binding really changed: the field, the name and the file.
    await expect(shortcut).toHaveAttribute("data-keys", key);
    await expect(drawerLabel(page)).toHaveValue(name);
    await expect
      .poll(async () => (await onDisk(live)).layers.base.north)
      .toMatchObject({ label: name, press: { keys: key } });
    // The first save made the file of items (its revision was empty).
    await expect
      .poll(async () => (await integrationItems(live)).items)
      .toContainEqual(expect.objectContaining({ keys: key }));
    await expect(page.getByTestId("integration-line")).toContainText(name);

    // Still there after another key and back.
    await openControl(page, "south");
    await expect(shortcut).toHaveAttribute("data-keys", "Ctrl+Z");
    await openControl(page, "north");
    await expect(shortcut).toHaveAttribute("data-keys", key);
    await expect(drawerLabel(page)).toHaveValue(name);
    // The dialog opens at the closed menu again.
    const again = await openPicker(page);
    await expect(
      again.locator('[role="treeitem"][aria-level="1"]').first(),
    ).toHaveAttribute("aria-expanded", "false");
    await again.getByRole("button", { name: "取消", exact: true }).click();
    await expect(again).toHaveCount(0);
    await expect(shortcut).toHaveAttribute("data-keys", key);
  } finally {
    await finish(f, app, client);
  }
});

test("pick a function that has no key: record one, write it, and the mapping follows; while the app runs the write waits for it to be closed", async () => {
  test.setTimeout(120000);
  const f = await integrationFixture();
  let app: ElectronApplication | undefined;
  let client: FixtureClient | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    const page = opened.page;
    client = await FixtureClient.connect(f);
    const live = client;
    const { withoutKey } = await pickable(live);
    const chord = "Ctrl+Shift+F11";

    await openControl(page, "north");
    const shortcut = drawerShortcut(page);
    const dialog = await openPicker(page);
    const leaf = await reveal(dialog, withoutKey);
    // No key: the column of keys is empty.
    await expect(leaf.locator(".picker-row-keys")).toHaveText("");
    await leaf.click();
    await dialog.getByRole("button", { name: "设置", exact: true }).click();

    // The second step: record a key, and the daemon says who has it.
    await expect(dialog.getByTestId("integration-record")).toBeVisible();
    await recordShortcut(
      app,
      dialog.getByRole("group", { name: "快捷键", exact: true }),
      chord,
    );
    await expect(dialog.getByTestId("integration-check")).toBeVisible();
    const write = dialog.getByRole("button", { name: `写入 ${APP}` });
    await expect(write).toBeEnabled();
    // Nothing changed yet.
    await expect(shortcut).toHaveAttribute("data-keys", "F13");

    // The app is running: the write waits, and 取消 leaves everything as it was.
    await live.call("fake.integration", { running: true });
    await write.click();
    const closeFirst = page.getByRole("dialog", { name: `请先关闭 ${APP}` });
    await expect(closeFirst).toBeVisible();
    await closeFirst.getByRole("button", { name: "重试" }).click();
    await expect(closeFirst.getByRole("alert")).toContainText("还在运行");
    await closeFirst.getByRole("button", { name: "取消" }).click();
    await expect(closeFirst).toHaveCount(0);
    await expect(shortcut).toHaveAttribute("data-keys", "F13");
    expect((await onDisk(live)).layers.base.north.press.keys).toBe("F13");

    // Closed again: 重试 goes on, writes, and only then changes the mapping.
    await write.click();
    await expect(closeFirst).toBeVisible();
    await live.call("fake.integration", { running: false });
    await closeFirst.getByRole("button", { name: "重试" }).click();
    await expect(dialog.getByTestId("integration-written")).toBeVisible();
    await dialog.getByRole("button", { name: "完成", exact: true }).click();
    await expect(dialog).toHaveCount(0);

    await expect(shortcut).toHaveAttribute("data-keys", chord);
    await expect(drawerLabel(page)).toHaveValue(withoutKey.fn.name);
    await expect
      .poll(async () => (await onDisk(live)).layers.base.north.press.keys)
      .toBe(chord);
    // The app has the key now.
    const listing = await live.call<IntegrationFunctions>(
      "integrations.functions",
      { integration: INTEGRATION, language: "zh-CN" },
    );
    expect(
      listing.functions.find((fn) => fn.id === withoutKey.fn.id)?.keys,
    ).toContain(chord);
  } finally {
    await finish(f, app, client);
  }
});

test("cancel leaves the mapping as it was, and a click in the dialog is never recorded as the mapping", async () => {
  test.setTimeout(90000);
  const f = await integrationFixture();
  let app: ElectronApplication | undefined;
  let client: FixtureClient | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    const page = opened.page;
    client = await FixtureClient.connect(f);
    const live = client;
    const { withKey } = await pickable(live);
    const before = (await onDisk(live)).layers.base.north;

    await openControl(page, "north");
    const shortcut = drawerShortcut(page);
    const field = page.getByRole("complementary", { name: "按键编辑" });
    const dialog = await openPicker(page);
    // Clicks all over the dialog: the search box, the title, the empty part of
    // the tree, a group, a function.
    await dialog.getByRole("searchbox").click();
    await dialog.getByRole("heading", { name: `从 ${APP} 选择` }).click();
    await dialog.locator(".dialog-body").click({ position: { x: 4, y: 4 } });
    const leaf = await reveal(dialog, withKey);
    await leaf.click();
    await dialog.getByRole("tree").click({ position: { x: 2, y: 2 } });
    await leaf.dblclick();
    // No recording started behind the dialog.
    await expect(field.getByRole("button", { name: "录入中…" })).toHaveCount(0);

    await dialog.getByRole("button", { name: "取消", exact: true }).click();
    await expect(dialog).toHaveCount(0);
    // The click that closed the dialog started no recording either, and a
    // later click anywhere in the drawer is no mouse key.
    await expect(field.getByRole("button", { name: "录入中…" })).toHaveCount(0);
    await page.waitForTimeout(500);
    await field.click({ position: { x: 2, y: 2 } });
    await expect(shortcut).toHaveAttribute("data-keys", "F13");
    await expect(drawerLabel(page)).toHaveValue("");
    expect((await onDisk(live)).layers.base.north).toEqual(before);
    expect((await integrationItems(live)).items).toEqual([]);

    // The same with a recording that was already running when the dialog
    // was opened: opening it ends the recording, and none of its clicks is
    // a mouse key.
    await shortcut.getByRole("button", { name: "录入快捷键" }).click();
    await expect(
      shortcut.getByRole("button", { name: "录入中…" }),
    ).toBeVisible();
    const second = await openPicker(page);
    await expect(shortcut.getByRole("button", { name: "录入中…" })).toHaveCount(
      0,
    );
    const target = await reveal(second, withKey);
    await target.click();
    await second.getByRole("button", { name: "关闭", exact: true }).click();
    await expect(second).toHaveCount(0);
    await page.waitForTimeout(500);
    await expect(shortcut).toHaveAttribute("data-keys", "F13");
    expect((await onDisk(live)).layers.base.north).toEqual(before);
  } finally {
    await finish(f, app, client);
  }
});
