import { writeFile } from "node:fs/promises";
import { join } from "node:path";
import { type ElectronApplication, expect, test } from "@playwright/test";
import { cleanup, fixture, launch, rpc, setWindowSize } from "./helpers";

// 设置 → 诊断 (UI-20, UI-21, UI-22; ADR-0071): the list of the latest presses,
// the sentence for a press that did nothing, and the latency figures.

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("诊断 lists a press with its action, says why an unbound one did nothing, and counts latency samples", async ({}, info) => {
  test.setTimeout(90000);
  const f = await fixture();
  let app: ElectronApplication | undefined;
  try {
    // padmate's own window is in front while the test presses, so its keys
    // are only previewed, and the latency counts only keys that were sent
    // (the daemon of the test prints them): sendToOwnWindow lets them through.
    await writeFile(
      join(f.path, "data", "config.json"),
      JSON.stringify({
        schemaVersion: 0,
        enabled: false,
        output: { sendToOwnWindow: true },
      }),
    );
    const opened = await launch(f);
    app = opened.app;
    const page = opened.page;
    await expect(page.locator('button[data-callout="south"]')).toContainText(
      "撤销",
    );

    // Presses made on the mapping page are on the list when it is opened.
    await rpc(f, "fake.press", { control: "south" });
    await rpc(f, "fake.release", { control: "south" });

    const settings = page.getByRole("button", { name: "设置", exact: true });
    await settings.click();
    // 诊断 sits next to 日志 in Settings; the rail stays four pages.
    await page.getByRole("button", { name: "查看诊断", exact: true }).click();
    await expect(
      page.getByRole("heading", { name: "诊断", level: 1, exact: true }),
    ).toBeVisible();
    await expect(settings).toHaveAttribute("aria-current", "page");
    expect(
      await page
        .getByRole("navigation")
        .getByRole("button")
        .evaluateAll((buttons) =>
          buttons.map((button) => button.getAttribute("aria-label")),
        ),
    ).toEqual(["映射", "配置", "脚本", "设置"]);

    // A bound key: a row with the action it sends.
    const rows = page.locator(".page-act-list > li");
    const south = rows.filter({ hasText: "Ctrl+Z" });
    await expect(south).toHaveCount(1);
    await expect(south.locator(".page-act-key .visually-hidden")).toHaveText(
      "S",
    );
    await expect(south.locator(".page-act-gesture")).toHaveText("点按");
    await expect(south.locator(".page-act-where")).toHaveText("平时");
    await expect(south.locator(".page-act-time")).toHaveText(
      /^\d\d:\d\d:\d\d\.\d{3}$/,
    );

    // An unbound key: the sentence that says why nothing happened.
    await rpc(f, "fake.press", { control: "north" });
    await rpc(f, "fake.release", { control: "north" });
    const unbound = rows.filter({
      hasText: "当前配置里这个键没有设置动作",
    });
    await expect(unbound).toHaveCount(1);
    await expect(unbound.locator(".page-act-key .visually-hidden")).toHaveText(
      "N",
    );
    // The newest row is first.
    await expect(rows.first()).toContainText("当前配置里这个键没有设置动作");

    // 只看没有动作的 leaves the press that sent keys out.
    const only = page.getByRole("button", { name: "只看没有动作的" });
    await only.click();
    await expect(only).toHaveAttribute("aria-pressed", "true");
    await expect(south).toHaveCount(0);
    await expect(unbound).toHaveCount(1);
    await only.click();
    await expect(south).toHaveCount(1);

    // The key filter.
    const filter = page.getByRole("searchbox", { name: "筛选按键" });
    await filter.fill("n");
    await expect(rows).toHaveCount(1);
    await filter.fill("");
    await expect(rows).toHaveCount(2);

    // 暂停记录 keeps new presses off the list; 继续记录 takes them again.
    await page.getByRole("button", { name: "暂停记录" }).click();
    await rpc(f, "fake.press", { control: "east" });
    await rpc(f, "fake.release", { control: "east" });
    await page.waitForTimeout(400);
    await expect(rows).toHaveCount(2);
    await page.getByRole("button", { name: "继续记录" }).click();
    await rpc(f, "fake.press", { control: "east" });
    await rpc(f, "fake.release", { control: "east" });
    await expect(rows).toHaveCount(3);

    // Latency: the keys were sent (sendToOwnWindow), so the pad has samples.
    const count = page.locator(".page-latency-count");
    await expect(count).toHaveText(/^[1-9]\d* 个样本$/, { timeout: 10000 });
    await expect(page.locator('[data-figure="p99"] dd')).toHaveText(
      /^(\d+ µs|\d+\.\d\d ms)$/,
    );
    await expect(
      page.getByText(
        "从 padmate 收到手柄输入到发出按键的时间，不含手柄和接收器本身的延迟；padmate 窗口在前台时只预览，不计入。",
      ),
    ).toBeVisible();

    // 清空 empties the figures, and the next press fills them again.
    await page.getByRole("button", { name: "清空延迟统计" }).click();
    await expect(count).toHaveText("0 个样本", { timeout: 10000 });
    await expect(page.locator('[data-figure="p99"] dd')).toHaveText("—");
    await rpc(f, "fake.press", { control: "south" });
    await rpc(f, "fake.release", { control: "south" });
    await expect(count).toHaveText(/^[1-9]\d* 个样本$/, { timeout: 10000 });

    // Conflicts: the section is there, whatever the machine runs.
    await expect(
      page.getByRole("heading", { name: "冲突", level: 2 }),
    ).toBeVisible();

    await page.screenshot({
      path: info.outputPath("diagnostics-zh.png"),
      fullPage: true,
    });
    // In the smallest window nothing needs sideways scrolling.
    await setWindowSize(app, 850, 620);
    await expect
      .poll(() =>
        page.evaluate(() =>
          [
            ...document.querySelectorAll(
              ".page-scroll, .page-column, .page-act-scroll, .page-act-row, .page-latency",
            ),
          ]
            .filter((element) => element.scrollWidth > element.clientWidth)
            .map((element) => element.className),
        ),
      )
      .toEqual([]);
    await page.screenshot({
      path: info.outputPath("diagnostics-zh-850.png"),
      fullPage: true,
    });

    // 清空 empties the list; Esc goes back to Settings.
    await page.getByRole("button", { name: "清空", exact: true }).click();
    await expect(rows).toHaveCount(0);
    await page.keyboard.press("Escape");
    await expect(
      page.getByRole("heading", { name: "设置", level: 1, exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: "查看诊断", exact: true }),
    ).toBeFocused();
  } finally {
    await cleanup(f, app);
  }
});
