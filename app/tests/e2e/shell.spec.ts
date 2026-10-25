import { type ElectronApplication, expect, test } from "@playwright/test";
import {
  cleanup,
  closeApplication,
  discoveryPID,
  expectEditorLabelsFit,
  expectPageFits,
  fixture,
  launch,
  probe,
  rpc,
  sameFrame,
  setWindowSize,
} from "./helpers";

// biome-ignore lint/correctness/noEmptyPattern: Playwright requires a destructured fixture parameter.
test("real fake-input UI fits the window without keyboard injection and enforces the shell boundary", async ({}, info) => {
  const f = await fixture();
  let app: ElectronApplication | undefined;
  try {
    const launched = await launch(f);
    app = launched.app;
    const page = launched.page;
    const sandboxed = await app.evaluate(({ app, BrowserWindow }) => {
      const pid = BrowserWindow.getAllWindows()
        .find((w) => w.getTitle() !== "padmate overlay")
        ?.webContents.getOSProcessId();
      return app.getAppMetrics().find((metric) => metric.pid === pid)
        ?.sandboxed;
    });
    expect(sandboxed).toBe(true);
    // Electron contextBridge only works when contextIsolation is enabled.
    expect(await page.evaluate(() => Object.isFrozen(window.padmate))).toBe(
      true,
    );
    const response = page.waitForResponse(
      (response) => response.url() === "padmate://app/index.html",
    );
    await page.reload();
    expect((await response).headers()["content-security-policy"]).toContain(
      "script-src 'self'",
    );
    const boundaries = await page.evaluate(() => {
      const script = document.createElement("script");
      script.textContent = "window.__padmateInlineScriptRan = true";
      document.head.appendChild(script);
      return {
        require: typeof (window as unknown as { require?: unknown }).require,
        process: typeof (window as unknown as { process?: unknown }).process,
        arbitraryRPC: typeof (window.padmate as unknown as { rpc?: unknown })
          .rpc,
        inlineRan: "__padmateInlineScriptRan" in window,
      };
    });
    expect(boundaries).toEqual({
      require: "undefined",
      process: "undefined",
      arbitraryRPC: "undefined",
      inlineRan: false,
    });
    await page.bringToFront();
    await app.evaluate(({ app, BrowserWindow }) => {
      const window = BrowserWindow.getAllWindows().find(
        (w) => w.getTitle() !== "padmate overlay",
      );
      app.focus({ steal: true });
      window?.setAlwaysOnTop(true);
      window?.focus();
      window?.setAlwaysOnTop(false);
    });
    await expect
      .poll(() =>
        page.evaluate(
          async () => (await window.padmate.getStatus()).localDryRun,
        ),
      )
      .toBe(true);
    const controllerView = page.getByRole("group", { name: /实时手柄/ });
    await expect(controllerView).toHaveAttribute("data-renderer", "webgl");
    const canvas = controllerView.locator("canvas");
    // Compare the model itself; label compositing has independent subpixel rounding.
    const modelScreenshot = (path?: string) =>
      canvas.screenshot({
        path,
        style:
          ".controller-labels, .controller-callouts, .controller-heading, .controller-hint { visibility: hidden !important; }",
      });
    await expect(canvas).toBeVisible();
    await page.screenshot({ path: info.outputPath("overview-loaded.png") });
    await page.evaluate(() => {
      const observed = window as unknown as { inputEvents: unknown[] };
      observed.inputEvents = [];
      window.padmate.onEvent((event) => {
        if (event.topic === "input") observed.inputEvents.push(event.data);
      });
    });
    for (const control of ["lefttrigger", "righttrigger"]) {
      const callout = page.locator(`[data-callout="${control}"]`);
      await expect(callout).toHaveAttribute("data-placement", "top");
      await rpc(f, "fake.axis", { axis: control, value: 32767 });
      try {
        await expect(callout).toHaveAttribute("data-active", "true");
      } catch (error) {
        console.error(
          "Input events",
          await page.evaluate(
            () => (window as unknown as { inputEvents: unknown[] }).inputEvents,
          ),
        );
        throw error;
      }
      await expect(callout).toContainText("100%");
      await rpc(f, "fake.axis", { axis: control, value: 0 });
      await expect(callout).toHaveAttribute("data-active", "false");
    }
    await expect(
      page.getByRole("button", { name: "背面", exact: true }),
    ).toHaveCount(0);
    await page.screenshot({ path: info.outputPath("overview-zh.png") });
    // Keep the accessibility focus ring identical in before/after pixel checks.
    await canvas.click();
    const restingModel = await modelScreenshot(info.outputPath("resting.png"));
    const recording = await probe();
    await page.evaluate(() => {
      const observed = window as unknown as { previews: unknown[] };
      observed.previews = [];
      window.padmate.onEvent((event) => {
        if (event.topic === "output.preview")
          observed.previews.push(event.data);
      });
    });
    await rpc(f, "fake.press", { control: "south" });
    await expect(page.locator('[data-control="south"]')).toHaveAttribute(
      "data-pressed",
      "true",
    );
    await expect
      .poll(async () => sameFrame(await modelScreenshot(), restingModel))
      .toBe(false);
    await rpc(f, "fake.release", { control: "south" });
    await expect(page.locator('[data-control="south"]')).toHaveAttribute(
      "data-pressed",
      "false",
    );
    await modelScreenshot(info.outputPath("released.png"));
    // The key preview line (UI-44) sits over the stage until it fades.
    await expect(page.getByTestId("key-preview")).toHaveText("", {
      timeout: 6000,
    });
    await expect
      .poll(async () => sameFrame(await modelScreenshot(), restingModel))
      .toBe(true);
    await expect
      .poll(() =>
        page.evaluate(() =>
          (
            window as unknown as { previews: { chord: string }[] }
          ).previews.some((value) => value.chord === "LCtrl+Z"),
        ),
      )
      .toBe(true);
    await expect(page.getByText("将发送 Ctrl+Z", { exact: false })).toHaveCount(
      0,
    );
    const expectFits = async () => {
      await expectPageFits(page);
      const stage = await page.locator(".controller-stage").boundingBox();
      // The switch between the usual keys and the function-key sets (UI-30)
      // sits above the controller: it is centred in the rest of the stage.
      const bar = await page.locator(".key-view-bar").boundingBox();
      const controller = await page
        .getByRole("group", { name: /实时手柄/ })
        .boundingBox();
      expect(stage).not.toBeNull();
      expect(bar).not.toBeNull();
      expect(controller).not.toBeNull();
      if (stage && bar && controller) {
        const top = bar.y + bar.height;
        const bottom = stage.y + stage.height;
        expect(
          Math.abs(
            controller.x + controller.width / 2 - stage.x - stage.width / 2,
          ),
        ).toBeLessThan(2);
        expect(
          Math.abs(controller.y + controller.height / 2 - (top + bottom) / 2),
        ).toBeLessThan(2);
        expect(controller.y).toBeGreaterThanOrEqual(top - 1);
        expect(controller.height).toBeLessThanOrEqual(stage.height);
        expect(controller.width).toBeLessThanOrEqual(stage.width);
      }
    };
    await expectFits();
    const recorded = (await recording.finished)
      .split(/\r?\n/)
      .filter(Boolean)
      .map((line) => JSON.parse(line));
    expect(recorded.filter((event) => event.padmate)).toEqual([]);
    const canvasBounds = await canvas.boundingBox();
    if (!canvasBounds) throw new Error("Missing rendered controller canvas");
    const cx = canvasBounds.x + canvasBounds.width / 2;
    const cy = canvasBounds.y + canvasBounds.height / 2;
    await page.mouse.move(cx, cy);
    await page.mouse.down();
    await page.mouse.move(cx + 110, cy + 35, { steps: 8 });
    await page.mouse.up();
    await expect
      .poll(async () => sameFrame(await modelScreenshot(), restingModel))
      .toBe(true);
    await page.screenshot({
      path: info.outputPath("overview-fixed-front.png"),
    });
    await canvas.dblclick();
    await expect
      .poll(async () => sameFrame(await modelScreenshot(), restingModel))
      .toBe(true);
    await page.evaluate(() => window.padmate.pause());
    await expect
      .poll(() =>
        rpc(f, "status.get").then(
          (value) => (value as { paused: boolean }).paused,
        ),
      )
      .toBe(true);
    await page.evaluate(() => window.padmate.resume());
    await expect
      .poll(() =>
        rpc(f, "status.get").then(
          (value) => (value as { paused: boolean }).paused,
        ),
      )
      .toBe(false);
    await page.getByRole("button", { name: "脚本", exact: true }).click();
    await expectPageFits(page);
    await page.screenshot({
      path: info.outputPath("scripts-zh.png"),
      fullPage: true,
    });
    // The rail: 映射 · 配置 · 脚本 · 设置.
    expect(
      await page
        .getByRole("navigation")
        .getByRole("button")
        .evaluateAll((buttons) =>
          buttons.map((button) => button.getAttribute("aria-label")),
        ),
    ).toEqual(["映射", "配置", "脚本", "设置"]);
    const profilesRail = page
      .getByRole("navigation")
      .getByRole("button", { name: "配置", exact: true });
    await profilesRail.click();
    await expect(profilesRail).toHaveAttribute("aria-current", "page");
    await expectPageFits(page);
    await page.screenshot({
      path: info.outputPath("profiles-zh.png"),
      fullPage: true,
    });
    // Nothing in Settings or its logs view may need sideways scrolling, and
    // the logs toolbar stays on one line.
    const expectNoSidewaysScroll = async () => {
      await expect
        .poll(() =>
          page.evaluate(() =>
            [
              ...document.querySelectorAll(
                ".page-scroll, .page-column, .card .row, .page-logs-toolbar",
              ),
            ]
              .filter((element) => element.scrollWidth > element.clientWidth)
              .map((element) => element.className),
          ),
        )
        .toEqual([]);
    };
    const settingsRail = page.getByRole("button", {
      name: "设置",
      exact: true,
    });
    await settingsRail.click();
    await expect(settingsRail).toHaveAttribute("aria-current", "page");
    await expect(
      page.getByRole("button", { name: "日志", exact: true }),
    ).toHaveCount(0);
    await expectPageFits(page);
    await page.screenshot({
      path: info.outputPath("settings-zh.png"),
      fullPage: true,
    });
    // Logs is a view inside Settings: 日志 · 查看, back with Esc.
    await page.getByRole("button", { name: "查看日志", exact: true }).click();
    await expect(
      page.getByRole("heading", { name: "日志", exact: true }),
    ).toBeVisible();
    await expect(settingsRail).toHaveAttribute("aria-current", "page");
    await expectPageFits(page);
    await page.screenshot({
      path: info.outputPath("logs-zh.png"),
      fullPage: true,
    });
    await page.keyboard.press("Escape");
    await expect(
      page.getByRole("heading", { name: "设置", exact: true }),
    ).toBeVisible();
    await setWindowSize(app, 850, 620);
    await expectPageFits(page);
    await expectNoSidewaysScroll();
    await page.screenshot({
      path: info.outputPath("settings-zh-850.png"),
      fullPage: true,
    });
    await page.getByRole("button", { name: "查看日志", exact: true }).click();
    await expectPageFits(page);
    await expectNoSidewaysScroll();
    const toolbarCenters = await page
      .locator(".page-logs-toolbar > *")
      .evaluateAll((elements) =>
        elements.map((element) => {
          const box = element.getBoundingClientRect();
          return box.top + box.height / 2;
        }),
      );
    expect(
      Math.max(...toolbarCenters) - Math.min(...toolbarCenters),
    ).toBeLessThanOrEqual(2);
    await page.screenshot({ path: info.outputPath("logs-zh-850.png") });
    await page.getByRole("button", { name: "返回设置", exact: true }).click();
    await page.getByRole("button", { name: "关于与许可", exact: true }).click();
    await expect(
      page.getByRole("dialog", { name: "关于与许可" }),
    ).toBeVisible();
    await page.screenshot({ path: info.outputPath("licenses-zh-850.png") });
    await page.getByRole("button", { name: "关闭关于与许可" }).click();
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await page.getByRole("button", { name: "映射", exact: true }).click();
    await expectFits();
    await page.screenshot({
      path: info.outputPath("overview-zh-850.png"),
      fullPage: true,
    });
    const calloutButtons = page.locator("button[data-callout]");
    // View and Menu are function-key tags, not buttons (UI-30).
    await expect(calloutButtons).toHaveCount(15);
    await expect(page.locator("[data-callout='back']")).toHaveCount(1);
    await expect(page.locator("button[data-callout='back']")).toHaveCount(0);
    for (const control of [
      "south",
      "east",
      "west",
      "north",
      "dpad.up",
      "dpad.down",
      "dpad.left",
      "dpad.right",
      "guide",
      "leftstick",
      "rightstick",
      "leftshoulder",
      "rightshoulder",
      "lefttrigger",
      "righttrigger",
    ]) {
      const button = page.locator(`button[data-callout="${control}"]`);
      await button.click();
      await expect(button).toHaveAttribute("aria-pressed", "true");
      await expect(page.locator(`g[data-line="${control}"]`)).toHaveAttribute(
        "data-selected",
        "true",
      );
      await expect(
        page.locator('button[data-callout][aria-pressed="true"]'),
      ).toHaveCount(1);
    }
    await expectPageFits(page);
    await expect(
      page.getByRole("complementary", { name: "按键编辑" }),
    ).toBeVisible();
    await page.screenshot({ path: info.outputPath("mapping-entry-850.png") });
    await expectEditorLabelsFit(page);
    await page.getByRole("button", { name: "关闭按键编辑" }).click();
    await expect(
      page.getByRole("complementary", { name: "按键编辑" }),
    ).toHaveCount(0);
    await page.getByRole("button", { name: "设置", exact: true }).click();
    await setWindowSize(app, 1180, 800);
    await page
      .getByRole("combobox", { name: "语言", exact: true })
      .selectOption("en");
    await expectPageFits(page);
    await page.screenshot({
      path: info.outputPath("settings-en.png"),
      fullPage: true,
    });
    await setWindowSize(app, 850, 620);
    await page.getByRole("button", { name: "Mapping", exact: true }).click();
    await page.locator('button[data-callout="south"]').click();
    await expect(
      page.getByRole("complementary", { name: "Key editor" }),
    ).toBeVisible();
    await expectPageFits(page);
    await page.screenshot({
      path: info.outputPath("mapping-entry-en-850.png"),
    });
    await expectEditorLabelsFit(page);
    const pid = await discoveryPID(f);
    await closeApplication(app);
    app = undefined;
    expect(await discoveryPID(f)).toBe(pid);
    expect(
      ((await rpc(f, "status.get")) as { fakeInput: boolean }).fakeInput,
    ).toBe(true);
  } finally {
    await cleanup(f, app);
  }
});

test("UI reopening uses the existing daemon and UI process crash leaves it running", async () => {
  const f = await fixture();
  let app: ElectronApplication | undefined;
  try {
    const first = await launch(f);
    app = first.app;
    const pid = await discoveryPID(f);
    expect(
      (await first.page.evaluate(() => window.padmate.getConnection()))
        .startedByUI,
    ).toBe(true);
    await closeApplication(app);
    app = undefined;
    const reopened = await launch(f);
    app = reopened.app;
    expect(
      (await reopened.page.evaluate(() => window.padmate.getConnection()))
        .startedByUI,
    ).toBe(false);
    expect(await discoveryPID(f)).toBe(pid);
    const uiPID = f.mainPIDs.get(app);
    if (!uiPID) throw new Error("Missing owned Electron process PID");
    process.kill(uiPID);
    await expect
      .poll(() =>
        rpc(f, "status.get").then(
          (status) => (status as { fakeInput: boolean }).fakeInput,
        ),
      )
      .toBe(true);
    expect(await discoveryPID(f)).toBe(pid);
  } finally {
    await cleanup(f, app);
  }
});

test("renderer crash exits only the UI", async () => {
  const f = await fixture();
  let app: ElectronApplication | undefined;
  try {
    const opened = await launch(f);
    app = opened.app;
    const pid = await discoveryPID(f);
    await app.evaluate(({ BrowserWindow }) =>
      BrowserWindow.getAllWindows()
        .find((w) => w.getTitle() !== "padmate overlay")
        ?.webContents.forcefullyCrashRenderer(),
    );
    await expect
      .poll(() =>
        rpc(f, "status.get").then(
          (status) => (status as { fakeInput: boolean }).fakeInput,
        ),
      )
      .toBe(true);
    expect(await discoveryPID(f)).toBe(pid);
  } finally {
    await cleanup(f, app);
  }
});
