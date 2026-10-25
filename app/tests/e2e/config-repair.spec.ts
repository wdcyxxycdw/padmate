import { readdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { type ElectronApplication, expect, test } from "@playwright/test";
import type { StatusResult } from "../../src/shared/api";
import { axeCheck } from "./a11y-helpers";
import { cleanup, fixture, launch, rpc } from "./helpers";

test.use({ actionTimeout: 15000 });

// PRO-29: a config.json with errors is repaired, not refused. The daemon
// backs the file up as config.broken-YYYYMMDD-HHMMSS.json, keeps every valid
// member and resets only what had errors; the window tells once, in a
// blocking dialog, with 确定 and 打开文件夹. What cannot be repaired, such as
// a folder in place of the file, keeps the start-failure page
// (startup-failure.spec.ts).

const backupName = /^config\.broken-\d{8}-\d{6}(-\d+)?\.json$/;

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("a config.json with errors is repaired, backed up and told once", async ({}, info) => {
  const f = await fixture();
  const data = join(f.path, "data");
  // The owner's mistake (2026-10-12, F17): one emergency button. The timing
  // beside it is valid and must stay.
  const original = [
    "{",
    '  "schemaVersion": 0,',
    '  "enabled": false,',
    '  "timing": { "holdMs": 320 },',
    '  "safety": { "panic": { "standard": { "buttons": ["back"], "holdMs": 2000 } } }',
    "}",
  ].join("\n");
  await writeFile(join(data, "config.json"), original);
  let app: ElectronApplication | undefined;
  try {
    const launched = await launch(f);
    app = launched.app;
    const page = launched.page;
    // 打开文件夹 would open Explorer: record what Main would select.
    await app.evaluate(({ shell }) => {
      const seen: string[] = [];
      (globalThis as unknown as { shown: string[] }).shown = seen;
      shell.showItemInFolder = (path: string) => {
        seen.push(path);
      };
    });
    const dialog = page.getByRole("dialog", { name: "设置已恢复" });
    await expect(dialog).toBeVisible();
    // The start went on: no start-failure page, and the window is the app.
    await expect(page.getByTestId("start-failure")).toHaveCount(0);
    await expect(page.getByRole("navigation")).toBeVisible();

    const backups = (await readdir(data)).filter((name) =>
      backupName.test(name),
    );
    expect(backups).toHaveLength(1);
    const backup = backups[0] as string;
    await expect(dialog).toContainText(
      `设置文件有误，已恢复默认设置。原文件已备份为 ${backup}。`,
    );
    // The backup is the file as it was; the file keeps what was valid and
    // loses only the combo that had the error.
    expect(await readFile(join(data, backup), "utf8")).toBe(original);
    const repaired = JSON.parse(
      await readFile(join(data, "config.json"), "utf8"),
    );
    expect(repaired.enabled).toBe(false);
    expect(repaired.timing).toEqual({ holdMs: 320 });
    // The daemon drops the member with the error, not its emptied parents.
    expect(repaired.safety?.panic?.standard).toBeUndefined();
    await expect(
      dialog.getByRole("button", { name: "确定", exact: true }),
    ).toBeFocused();
    await page.screenshot({ path: info.outputPath("config-repair.png") });
    // UI-24: the dialog is checked like the rest of the window.
    await axeCheck(page, "config repair dialog");

    // 打开文件夹 selects the backup in the data folder and leaves the dialog.
    await dialog
      .getByRole("button", { name: "打开文件夹", exact: true })
      .click();
    await expect
      .poll(() =>
        app?.evaluate(
          () => (globalThis as unknown as { shown: string[] }).shown,
        ),
      )
      .toEqual([join(data, backup)]);
    await expect(dialog).toBeVisible();

    await dialog.getByRole("button", { name: "确定", exact: true }).click();
    await expect(dialog).toHaveCount(0);
    // The daemon was told: it no longer reports the repair.
    await expect
      .poll(
        async () => ((await rpc(f, "status.get")) as StatusResult).configRepair,
      )
      .toBeUndefined();
    // A new window shows nothing, and the backups stay as they are.
    await page.reload();
    await expect(page.getByRole("navigation")).toBeVisible();
    await expect(dialog).toHaveCount(0);
    expect(
      (await readdir(data)).filter((name) => backupName.test(name)),
    ).toEqual([backup]);
  } finally {
    await cleanup(f, app);
  }
});

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("a config.json that cannot be parsed is replaced by the defaults, with the original backed up", async ({}) => {
  const f = await fixture();
  const data = join(f.path, "data");
  const original = '{ "schemaVersion": 0, "enabled": false, ';
  await writeFile(join(data, "config.json"), original);
  let app: ElectronApplication | undefined;
  try {
    const launched = await launch(f);
    app = launched.app;
    const page = launched.page;
    const dialog = page.getByRole("dialog", { name: "设置已恢复" });
    await expect(dialog).toBeVisible();
    const backups = (await readdir(data)).filter((name) =>
      backupName.test(name),
    );
    expect(backups).toHaveLength(1);
    expect(await readFile(join(data, backups[0] as string), "utf8")).toBe(
      original,
    );
    expect(
      JSON.parse(await readFile(join(data, "config.json"), "utf8")),
    ).toEqual({ schemaVersion: 0 });
    await dialog.getByRole("button", { name: "确定", exact: true }).click();
    await expect(dialog).toHaveCount(0);
    // The settings page opens on the defaults: nothing says the file cannot
    // be read.
    await page.getByRole("button", { name: "设置", exact: true }).click();
    await expect(page.getByRole("heading", { name: "设置" })).toBeVisible();
    await expect(page.getByText("无法读取设置文件")).toHaveCount(0);
  } finally {
    await cleanup(f, app);
  }
});
