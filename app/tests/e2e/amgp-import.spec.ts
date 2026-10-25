import { copyFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import {
  type ElectronApplication,
  expect,
  type Page,
  test,
} from "@playwright/test";
import { cleanup, fixture, launch, root, rpc } from "./helpers";

// 配置 → 导入… of an AntiMicroX file (PRO-21, ADR-0072): .amgp and .xml go to
// the daemon as data, the report is listed by severity in a dialog, and the new
// profile opens with its 打开配置 button (Esc and the close button only close
// the report).

test.use({ actionTimeout: 15000 });

const testdata = join(root, "daemon", "internal", "amgp", "testdata");

// State the stubbed open dialog keeps in the main process.
interface OpenStubs {
  openOptions?: {
    title?: string;
    filters?: { name: string; extensions: string[] }[];
  };
  nextFile?: string;
}

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("导入… takes an AntiMicroX .amgp, lists what did not come over whole, and opens the new profile", async ({}, info) => {
  test.setTimeout(90000);
  const f = await fixture();
  let app: ElectronApplication | undefined;
  let page: Page | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    page = opened.page;
    const p = page;
    const rail = (name: string) =>
      p.getByRole("navigation").getByRole("button", { name, exact: true });

    // No real file dialog: the open dialog returns nextFile.
    await app.evaluate(({ dialog }) => {
      const stubs = globalThis as unknown as OpenStubs;
      dialog.showOpenDialog = (async (_window: unknown, options?: unknown) => {
        stubs.openOptions = options as OpenStubs["openOptions"];
        return stubs.nextFile
          ? { canceled: false, filePaths: [stubs.nextFile] }
          : { canceled: true, filePaths: [] };
      }) as typeof dialog.showOpenDialog;
    });
    const choose = (path?: string) =>
      app?.evaluate((_electron, next) => {
        (globalThis as unknown as OpenStubs).nextFile = next;
      }, path);
    const profiles = async () =>
      (await rpc(f, "profiles.list")) as { id: string; name?: string }[];
    const idOf = async (name: string) => {
      const found = (await profiles()).find((entry) => entry.name === name);
      expect(found, name).toBeTruthy();
      return found?.id ?? "";
    };
    /** The profile the daemon maps with. */
    const inUse = async () =>
      ((await rpc(f, "status.get")) as { profileId: string }).profileId;

    // The report: one dropped line is about a slot, one about the file; the
    // eight-way d-pad of set 2 is approximated; set 2 becomes a layer.
    await rail("配置").click();
    await choose(join(testdata, "dpad-modes.amgp"));
    await p.getByRole("button", { name: "导入…", exact: true }).click();
    const report = p.getByRole("dialog", {
      name: "已从 AntiMicroX 导入「dpad-modes」",
    });
    await expect(report).toBeVisible();
    await expect(report.getByRole("heading", { level: 3 })).toHaveText([
      "丢弃（2）",
      "近似（1）",
      "信息（2）",
    ]);
    await expect(report.locator("li").first()).toContainText("set 1 / dpad.up");
    await expect(report.locator(".report-where").first()).toHaveText(
      "set 1 / dpad.up",
    );
    await expect(report).toContainText("方向延迟");
    await p.screenshot({ path: info.outputPath("amgp-report.png") });

    // The open dialog offers the files this import takes next to the backup's.
    const opening = (await app.evaluate(
      () => (globalThis as unknown as OpenStubs).openOptions,
    )) as OpenStubs["openOptions"];
    expect(opening?.title).toBe("导入配置 / Import profile");
    expect(opening?.filters?.[0]?.extensions).toEqual([
      "padmate",
      "amgp",
      "xml",
    ]);
    // The profile is made; it opens only once the report is read.
    const first = await idOf("dpad-modes");
    await expect(rail("配置")).toHaveAttribute("aria-current", "page");
    expect(await inUse()).toBe("paint");

    // 打开配置: the new profile is in use on the mapping page, and the key
    // drawer says it has more key groups than the page edits (UI-39).
    await report.getByRole("button", { name: "打开配置", exact: true }).click();
    await expect(report).toHaveCount(0);
    await expect(rail("映射")).toHaveAttribute("aria-current", "page");
    await expect.poll(inUse).toBe(first);
    await p.locator('button[data-callout="dpad.up"]').click();
    await expect(
      p.getByRole("complementary", { name: "按键编辑" }),
    ).toBeVisible();
    await expect(p.getByTestId("drawer-file-notice")).toContainText(
      "此配置文件中还有 1 组按键",
    );
    await p.screenshot({ path: info.outputPath("amgp-opened.png") });

    // The same file again is a second profile with a numbered name.
    await rail("配置").click();
    await choose(join(testdata, "dpad-modes.amgp"));
    await p.getByRole("button", { name: "导入…", exact: true }).click();
    const again = p.getByRole("dialog", {
      name: "已从 AntiMicroX 导入「dpad-modes 2」",
    });
    await expect(again).toBeVisible();
    // Esc reads the report and stays on the page: the profile in use is still
    // the first one, and the new profile is in the library.
    await p.keyboard.press("Escape");
    await expect(again).toHaveCount(0);
    await expect(rail("配置")).toHaveAttribute("aria-current", "page");
    expect(await idOf("dpad-modes 2")).not.toBe(first);
    expect(await inUse()).toBe(first);

    // A .xml of the older versions takes the same way.
    const old = join(f.path, "old.xml");
    await copyFile(join(testdata, "nintendo.amgp"), old);
    await rail("配置").click();
    await choose(old);
    await p.getByRole("button", { name: "导入…", exact: true }).click();
    const xml = p.getByRole("dialog", {
      name: "已从 AntiMicroX 导入「old」",
    });
    await expect(xml).toBeVisible();
    await expect(xml.getByRole("heading", { level: 3 })).toHaveText([
      "信息（2）",
    ]);
    await xml.getByRole("button", { name: "打开配置", exact: true }).click();
    await expect(xml).toHaveCount(0);

    // A file the daemon refuses is one line in the shell, and nothing opens.
    const before = (await profiles()).length;
    const raw = join(f.path, "raw.amgp");
    await writeFile(
      raw,
      '<?xml version="1.0" encoding="UTF-8"?><joystick configversion="19"><sets/></joystick>',
    );
    await rail("配置").click();
    await choose(raw);
    await p.getByRole("button", { name: "导入…", exact: true }).click();
    await expect(p.locator(".shell-notice[role=alert]")).toContainText(
      "这是原始摇杆的配置，只能导入游戏手柄的配置",
    );
    expect(await profiles()).toHaveLength(before);
    await expect(p.getByRole("dialog")).toHaveCount(0);
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
