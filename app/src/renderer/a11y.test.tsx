// @vitest-environment jsdom
// UI-24: axe-core over what the app renders, in jsdom: every page, the
// drawer, the menus and every dialog. It is the part of the accessibility
// check that `task ci` runs; the rules that need layout (contrast, target
// size, scrolling) are covered by theme-contrast.test.ts and by the Electron
// pass of tests/e2e/a11y.spec.ts, which runs axe in the real window.
//
// A finding is fixed or, when it is understood and accepted, listed in
// tests/a11y/exceptions.json with its reason: never silenced here.
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import type { AxeResults } from "axe-core";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  exceptionProblems,
  exceptions,
  exceptionsFor,
} from "../../tests/a11y/exceptions";
import type { UpdateDialogKind, UpdateState } from "../shared/update";
import {
  axeReport,
  controller,
  gamepad,
  joystick,
  pageStructure,
  renderApp,
  rulesThatFailed,
} from "./a11y-harness";
import { clearParameterClipboard } from "./analog/analog-parts";
import { BindConflictDialog } from "./pages/ProfileDialogs";
import { UpdateDialogs } from "./UpdateDialogs";
import { supportedUpdateState, updateApi } from "./update-api-fixture";

beforeEach(() => localStorage.clear());
afterEach(cleanup);

/** The page, as a screen reader's user finds it, has no violation. */
async function clean(label: string) {
  expect(await axeReport(), label).toEqual([]);
}

describe("the exceptions file", () => {
  it("is well formed: every entry says its rule, its selector and why", () => {
    expect(exceptionProblems(exceptions)).toEqual([]);
  });
  it("has a list for this pass", () => {
    expect(Array.isArray(exceptionsFor("jsdom"))).toBe(true);
  });
});

describe("the check itself", () => {
  // A pass that cannot fail proves nothing: these are what it must catch.
  it("finds a control without a name and an image without a text", async () => {
    render(
      <main>
        <h1>x</h1>
        <button type="button" />
        {/* biome-ignore lint/a11y/useAltText: The missing text is the point of the canary. */}
        <img src="x.png" />
      </main>,
    );
    const report = await axeReport();
    expect(report.some((line) => line.startsWith("button-name"))).toBe(true);
    expect(report.some((line) => line.startsWith("image-alt"))).toBe(true);
  });

  // With a dialog open axe asks the document for hit testing, which jsdom
  // does not have: every rule failed and none of them reported a violation.
  it("finds a control without a name inside an open dialog", async () => {
    render(
      <main>
        <h1>x</h1>
        <dialog open aria-label="canary">
          <button type="button" />
        </dialog>
      </main>,
    );
    const report = await axeReport();
    expect(report.some((line) => line.startsWith("button-name"))).toBe(true);
    expect(report.filter((line) => line.includes("could not run"))).toEqual([]);
  });

  it("says so when axe cannot run a rule, instead of passing", () => {
    const failed = {
      incomplete: [
        {
          id: "button-name",
          nodes: [
            {
              any: [],
              all: [],
              none: [{ message: "Axe encountered an error; test the page" }],
            },
          ],
        },
        {
          id: "color-contrast",
          nodes: [{ any: [{ message: "Needs review" }], all: [], none: [] }],
        },
      ],
    } as unknown as AxeResults;
    expect(rulesThatFailed(failed)).toEqual(["button-name"]);
  });

  it("lets an exception excuse exactly its rule on what its selector matches", async () => {
    // A bare test document has neither: the app sets both.
    document.title = "test";
    document.documentElement.lang = "en";
    render(
      <main>
        <h1>x</h1>
        <button type="button" className="excused" />
        <button type="button" className="not-excused" />
      </main>,
    );
    const report = await axeReport(document, [
      {
        rule: "button-name",
        selector: ".excused",
        reason: "A test of the exceptions: this one is excused on purpose.",
        scope: "jsdom",
      },
    ]);
    expect(report).toHaveLength(1);
    expect(report[0]).toContain("not-excused");
  });
});

describe("pages", () => {
  it("the mapping page has one main, one h1, and no violation", async () => {
    renderApp();
    await controller();
    expect(pageStructure()).toEqual({ main: 1, h1: ["映射"] });
    await clean("overview");
  });

  it("the mapping page in English", async () => {
    renderApp({ language: "en" });
    await screen.findByRole("group", { name: /Test controller/ });
    expect(pageStructure()).toEqual({ main: 1, h1: ["Mapping"] });
    await clean("overview en");
  });

  for (const [name, heading] of [
    ["配置", "配置"],
    ["脚本", "脚本"],
    ["设置", "设置"],
  ] as const)
    it(`the ${name} page has one main, one h1, and no violation`, async () => {
      renderApp();
      await controller();
      fireEvent.click(screen.getByRole("button", { name }));
      await screen.findByRole("heading", { name: heading, level: 1 });
      expect(pageStructure()).toEqual({ main: 1, h1: [heading] });
      await clean(name);
    });
});

const chip = (control: string) =>
  document.querySelector<HTMLButtonElement>(
    `button[data-callout="${control}"]`,
  ) as HTMLButtonElement;

/** Opens the key editor of a control of the mapping page. */
async function openDrawer(control: string) {
  await controller();
  fireEvent.click(chip(control));
  return screen.findByRole("complementary", { name: "按键编辑" });
}

describe("the key drawer", () => {
  for (const control of [
    "south",
    "east",
    "west",
    "lefttrigger",
    "leftstick",
    "dpad.up",
    "leftshoulder",
    "guide",
  ])
    it(`${control} has no violation`, async () => {
      renderApp();
      await openDrawer(control);
      await clean(`drawer ${control}`);
    });
});

describe("menus", () => {
  it("the profile menu", async () => {
    renderApp();
    await controller();
    fireEvent.click(screen.getByTestId("profile-menu"));
    await screen.findByRole("menu");
    await clean("profile menu");
  });
  it("the more menu", async () => {
    renderApp();
    await controller();
    fireEvent.click(screen.getByTestId("more-menu"));
    await screen.findByRole("menu");
    await clean("more menu");
  });
  it("the mouse menu of a shortcut", async () => {
    renderApp();
    const drawer = await openDrawer("south");
    fireEvent.click(within(drawer).getAllByTestId("shortcut-mouse")[0]);
    await screen.findByRole("menu");
    await clean("mouse menu");
  });
  it("the add-step menu of a shortcut", async () => {
    renderApp();
    const drawer = await openDrawer("south");
    fireEvent.click(within(drawer).getByTestId("add-step"));
    await screen.findByRole("menu");
    await clean("add step menu");
  });
});

/** The dialog that is open, found by its name. */
const dialogNamed = (name: string | RegExp) =>
  screen.findByRole("dialog", { name });

/** Opens an item of a menu of the mapping page's top bar. */
async function chooseFrom(menu: string, item: string | RegExp) {
  fireEvent.click(screen.getByTestId(menu));
  fireEvent.click(await screen.findByRole("menuitem", { name: item }));
}

describe("dialogs of the mapping page", () => {
  it("导出快捷键对照表", async () => {
    renderApp();
    await controller();
    await chooseFrom("more-menu", /^导出快捷键对照表/);
    await dialogNamed("导出快捷键对照表");
    await clean("export");
  });
  it("查看所有按键", async () => {
    renderApp();
    await controller();
    await chooseFrom("more-menu", "查看所有按键");
    await dialogNamed(/按键/);
    await clean("bindings list");
  });
  it("配置设置", async () => {
    renderApp();
    await controller();
    await chooseFrom("profile-menu", /^配置设置/);
    await dialogNamed(/配置设置/);
    await clean("profile settings");
  });
  it("新建配置", async () => {
    renderApp();
    await controller();
    await chooseFrom("profile-menu", /^新建配置/);
    await dialogNamed("新建配置");
    await clean("new profile");
  });
  it("清空此按键？", async () => {
    renderApp();
    const drawer = await openDrawer("south");
    fireEvent.click(within(drawer).getByTestId("key-actions"));
    fireEvent.click(await screen.findByRole("menuitem", { name: /清空/ }));
    await dialogNamed("清空此按键？");
    await clean("clear key");
  });
});

const calibratable = {
  ...gamepad,
  info: { ...gamepad.info, axisMask: 0b1111 },
};
const mappingGUID = "ff00aabb000000000000000000007600";

/** Goes to Settings, where the controllers and the safety dialogs are. */
async function openSettings(options: Parameters<typeof renderApp>[0] = {}) {
  const app = renderApp(options);
  await controller();
  fireEvent.click(screen.getByRole("button", { name: "设置" }));
  await screen.findByRole("heading", { name: "手柄调校", level: 2 });
  return app;
}

describe("dialogs of Settings", () => {
  it("关于与许可", async () => {
    await openSettings();
    fireEvent.click(screen.getByRole("button", { name: "关于与许可" }));
    await dialogNamed("关于与许可");
    await clean("about");
  });
  it("修改紧急暂停", async () => {
    await openSettings();
    fireEvent.click(screen.getByRole("button", { name: "修改紧急暂停" }));
    await dialogNamed("修改紧急暂停");
    await clean("panic combo");
  });
  it("调校", async () => {
    await openSettings({ devices: [calibratable] });
    fireEvent.click(
      screen.getByRole("button", { name: "调校 Test controller" }),
    );
    await dialogNamed("调校 Test controller");
    await clean("calibration");
  });
  it("建立映射", async () => {
    const app = await openSettings({ devices: [gamepad, joystick] });
    fireEvent.click(
      screen.getByRole("button", { name: "建立映射 Generic USB Joystick" }),
    );
    await dialogNamed("建立映射");
    await vi.waitFor(() => expect(app.api.rawWatchStart).toHaveBeenCalled());
    await clean("mapping wizard");
  });
  it("重新映射", async () => {
    await openSettings();
    fireEvent.click(
      screen.getByRole("button", { name: "更多操作 Test controller" }),
    );
    fireEvent.click(await screen.findByRole("menuitem", { name: "重新映射…" }));
    await dialogNamed("重新映射");
    await clean("remap wizard");
  });
  it("恢复默认映射？", async () => {
    await openSettings({
      config: {
        text: JSON.stringify({
          schemaVersion: 0,
          devices: {
            controller: { mapping: `${mappingGUID},*,a:b0,platform:Windows` },
          },
        }),
        revision: "rev",
      },
    });
    fireEvent.click(
      screen.getByRole("button", { name: "更多操作 Test controller" }),
    );
    fireEvent.click(
      await screen.findByRole("menuitem", { name: "恢复默认映射" }),
    );
    await dialogNamed("恢复默认映射？");
    await clean("reset mapping");
  });
  it("the logs and the diagnostics views", async () => {
    await openSettings();
    fireEvent.click(screen.getByRole("button", { name: "查看日志" }));
    await screen.findByRole("heading", { name: "日志", level: 1 });
    expect(pageStructure().main).toBe(1);
    await clean("logs");
    fireEvent.click(screen.getByRole("button", { name: "返回设置" }));
    fireEvent.click(await screen.findByRole("button", { name: "查看诊断" }));
    await screen.findByRole("heading", { name: "诊断", level: 1 });
    expect(pageStructure().h1).toEqual(["诊断"]);
    await clean("diagnostics");
  });
});

/** Goes to the profiles page. */
async function openProfiles(options: Parameters<typeof renderApp>[0] = {}) {
  const app = renderApp(options);
  await controller();
  fireEvent.click(screen.getByRole("button", { name: "配置" }));
  await screen.findByRole("article", { name: "Photo" });
  return app;
}
const cardMenu = async (name: string, item: string | RegExp) => {
  fireEvent.click(
    within(screen.getByRole("article", { name })).getByRole("button", {
      name: `「${name}」的更多操作`,
    }),
  );
  fireEvent.click(await screen.findByRole("menuitem", { name: item }));
};

describe("dialogs of the profiles page", () => {
  it("新建配置", async () => {
    await openProfiles();
    fireEvent.click(screen.getByRole("button", { name: "新建配置" }));
    await dialogNamed("新建配置");
    await clean("new profile");
  });
  it("新建配置 with a name that is taken", async () => {
    await openProfiles();
    fireEvent.click(screen.getByRole("button", { name: "新建配置" }));
    const dialog = await dialogNamed("新建配置");
    fireEvent.change(within(dialog).getByRole("textbox", { name: "名称" }), {
      target: { value: "photo" },
    });
    await within(dialog).findByText("已有同名配置");
    await clean("new profile with an error");
  });
  it("重命名配置", async () => {
    await openProfiles();
    await cardMenu("Photo", "重命名…");
    await dialogNamed("重命名配置");
    await clean("rename");
  });
  it("删除", async () => {
    await openProfiles();
    await cardMenu("Photo", "删除…");
    await dialogNamed(/^删除/);
    await clean("delete");
  });
  it("配置设置 of a card", async () => {
    await openProfiles();
    await cardMenu("Photo", "配置设置…");
    await dialogNamed(/配置设置/);
    await clean("card profile settings");
  });
  it("the menu of a card", async () => {
    await openProfiles();
    fireEvent.click(
      within(screen.getByRole("article", { name: "Photo" })).getByRole(
        "button",
        { name: "「Photo」的更多操作" },
      ),
    );
    await screen.findByRole("menu");
    await clean("card menu");
  });
  it("the menu of 添加软件", async () => {
    await openProfiles();
    fireEvent.click(
      within(screen.getByRole("article", { name: "Photo" })).getByRole(
        "button",
        { name: "添加软件" },
      ),
    );
    await screen.findByRole("menu");
    await clean("add app menu");
  });
  it("从已安装的软件中选择", async () => {
    const app = await openProfiles();
    app.api.listInstalledApps.mockResolvedValue([
      {
        name: "Krita",
        exe: "krita.exe",
        path: "C:Program FilesKritakrita.exe",
      },
      { name: "Photoshop", exe: "photoshop.exe", path: "C:Adobephotoshop.exe" },
    ]);
    fireEvent.click(
      within(screen.getByRole("article", { name: "Photo" })).getByRole(
        "button",
        { name: "添加软件" },
      ),
    );
    fireEvent.click(
      await screen.findByRole("menuitem", { name: "从已安装的软件中选择…" }),
    );
    await dialogNamed("选择软件");
    await screen.findByText("Krita");
    await clean("installed apps");
  });
  it("点选窗口", async () => {
    await openProfiles();
    fireEvent.click(
      within(screen.getByRole("article", { name: "Photo" })).getByRole(
        "button",
        { name: "添加软件" },
      ),
    );
    fireEvent.click(await screen.findByRole("menuitem", { name: "点选窗口…" }));
    await dialogNamed("点选窗口");
    await clean("window capture");
  });
  it("the import report of AntiMicroX", async () => {
    const app = await openProfiles();
    app.api.importProfile.mockResolvedValue({
      amgp: true,
      id: "from-amgp",
      name: "From AntiMicroX",
      revision: "r1",
      applied: true,
      report: [
        {
          severity: "dropped",
          code: "turbo",
          set: 2,
          control: "lt",
          slot: 1,
          message: "连发的设置被丢弃",
        },
        {
          severity: "approximate",
          code: "mouse",
          message: "鼠标移动近似为摇杆设置",
        },
        { severity: "info", code: "name", message: "名称取自文件名" },
      ],
    });
    fireEvent.click(screen.getByRole("button", { name: "导入…" }));
    await dialogNamed(/已从 AntiMicroX 导入/);
    await clean("amgp report");
  });
  it("the name clash of an import", async () => {
    const app = await openProfiles();
    app.api.importProfile.mockResolvedValue({
      exists: true,
      id: "photo",
      name: "Photo",
      token: "t1",
    });
    fireEvent.click(screen.getByRole("button", { name: "导入…" }));
    await dialogNamed(/Photo/);
    await clean("import clash");
  });
  it("the script clash of an import", async () => {
    const app = await openProfiles();
    app.api.importProfile.mockResolvedValue({
      conflict: true,
      token: "t2",
      scripts: [
        { file: "复制全部.ts", existing: "复制全部.ts", replaceable: true },
        { file: "旧脚本.ts", existing: "旧脚本 2.ts", replaceable: false },
      ],
    });
    fireEvent.click(screen.getByRole("button", { name: "导入…" }));
    await screen.findByRole("dialog");
    await clean("import scripts");
  });
});

const macroList = {
  macros: [
    {
      file: "复制全部.ts",
      name: "复制全部",
      usedBy: [
        {
          profile: "paint",
          profileName: "Paint",
          control: "south",
          gesture: "press",
        },
      ],
    },
    { file: "下一个笔刷.ts", name: "下一个笔刷", usedBy: [] },
  ],
  missing: [{ file: "旧.ts", usedBy: [] }],
  legacy: [],
};

describe("the scripts page and its dialogs", () => {
  async function openScripts() {
    const app = renderApp({ macros: macroList });
    await controller();
    fireEvent.click(screen.getByRole("button", { name: "脚本" }));
    await screen.findByText("复制全部");
    return app;
  }
  it("with scripts, a missing one and its menu", async () => {
    await openScripts();
    await clean("scripts");
    fireEvent.click(screen.getByRole("button", { name: "「复制全部」的操作" }));
    await screen.findByRole("menu");
    await clean("script menu");
  });
  it("新建脚本", async () => {
    await openScripts();
    fireEvent.click(screen.getByRole("button", { name: "新建" }));
    await dialogNamed("新建脚本");
    await clean("new script");
  });
  it("重命名", async () => {
    await openScripts();
    fireEvent.click(screen.getByRole("button", { name: "「复制全部」的操作" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "重命名" }));
    await dialogNamed("重命名");
    await clean("rename script");
  });
  it("删除", async () => {
    await openScripts();
    fireEvent.click(screen.getByRole("button", { name: "「复制全部」的操作" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "删除" }));
    await dialogNamed(/^删除/);
    await clean("delete script");
  });
});

/** A library that has only the fixed default: the first run is offered. */
const fresh = {
  default: { schemaVersion: 0, name: "默认", layers: { base: {} } },
};

describe("the first run, the repair and a failed start", () => {
  it("连接手柄 without a controller", async () => {
    renderApp({ devices: [], library: fresh, profileId: "default" });
    await dialogNamed("连接手柄");
    await clean("onboarding");
  });
  it("连接手柄 with a controller, in English", async () => {
    renderApp({ library: fresh, profileId: "default", language: "en" });
    await dialogNamed("Connect your controller");
    await clean("onboarding en");
  });
  it("设置已恢复", async () => {
    const app = renderApp();
    await controller();
    await act(async () => {
      app.store.setState({
        configRepair: {
          backup: "config.broken-20261024.json",
          path: "config.json",
          reset: true,
        },
      });
    });
    await dialogNamed("设置已恢复");
    await clean("config repair");
  });
  it("the page that says the service did not start", async () => {
    renderApp({
      connection: { phase: "failed", failure: { reason: "timeout" } },
    });
    await screen.findByTestId("start-failure");
    expect(pageStructure()).toEqual({ main: 1, h1: ["映射服务未能启动"] });
    await clean("start failure");
  });
  it("the page that waits for the service", async () => {
    renderApp({ connection: { phase: "disconnected", message: "gone" } });
    await screen.findByRole("button", { name: "重新连接" });
    await clean("disconnected");
  });
});

describe("states of the key drawer", () => {
  it("while a shortcut is recorded", async () => {
    const app = renderApp();
    const drawer = await openDrawer("south");
    fireEvent.click(within(drawer).getByRole("button", { name: "录入快捷键" }));
    await vi.waitFor(() => expect(app.api.captureStart).toHaveBeenCalled());
    await screen.findByRole("button", { name: "停止捕获" });
    await clean("recording");
    fireEvent.click(screen.getByRole("button", { name: "停止捕获" }));
  });
  it("with a sequence of a wait, a text and a shortcut", async () => {
    renderApp();
    const drawer = await openDrawer("south");
    for (const kind of ["等待", "文字", "快捷键"]) {
      fireEvent.click(within(drawer).getByTestId("add-step"));
      fireEvent.click(await screen.findByRole("menuitem", { name: kind }));
    }
    await clean("sequence");
  });
  it("with 高级 open", async () => {
    renderApp();
    const drawer = await openDrawer("south");
    fireEvent.click(within(drawer).getByText("高级"));
    await clean("advanced");
  });
  it("the menu to add a gesture, and its gestures", async () => {
    renderApp();
    const drawer = await openDrawer("south");
    fireEvent.click(within(drawer).getByTestId("add-gesture"));
    await screen.findByRole("menu");
    await clean("add gesture menu");
    fireEvent.click(await screen.findByRole("menuitem", { name: /长按/ }));
    await clean("with a hold");
  });
  it("the menu of the key (复制, 粘贴, 清空…)", async () => {
    renderApp();
    const drawer = await openDrawer("south");
    fireEvent.click(within(drawer).getByTestId("key-actions"));
    await screen.findByRole("menu");
    await clean("key actions menu");
  });
  it("the stick, with its advanced settings and parameter menu", async () => {
    renderApp({ devices: [calibratable] });
    const drawer = await openDrawer("leftstick");
    fireEvent.click(within(drawer).getByText(/高级/));
    await clean("stick advanced");
    fireEvent.click(within(drawer).getAllByTestId("analog-params")[0]);
    await screen.findByRole("menu");
    await clean("stick parameter menu");
  });
  it("the trigger, with live pressure", async () => {
    const app = renderApp();
    await openDrawer("lefttrigger");
    app.event("input", {
      device: 1,
      kind: "axis",
      control: "lefttrigger",
      value: 20000,
    });
    await screen.findByRole("meter");
    await clean("trigger");
  });
  it("in English", async () => {
    renderApp({ language: "en" });
    await screen.findByRole("group", { name: /Test controller/ });
    fireEvent.click(chip("south"));
    await screen.findByRole("complementary", { name: "Key editor" });
    await clean("drawer en");
  });
});

describe("logs and diagnostics with rows", () => {
  it("the logs, one row open", async () => {
    const app = await openSettings();
    app.event("log", {
      sequence: 1,
      record: {
        time: "2026-10-06T05:35:12Z",
        level: "INFO",
        msg: "controller connected",
        slot: 1,
      },
    });
    app.event("log", {
      sequence: 2,
      record: {
        level: "ERROR",
        source: "script",
        msg: "brush failed",
        entry: "brush.ts",
      },
    });
    fireEvent.click(screen.getByRole("button", { name: "查看日志" }));
    await screen.findByRole("list", { name: "日志记录" });
    await clean("logs with rows");
    fireEvent.click(
      screen.getAllByRole("button", { name: /controller connected/ })[0],
    );
    await clean("logs with an open row");
  });
  it("the diagnostics with events", async () => {
    const app = await openSettings();
    app.event("engine.activity", {
      kind: "decision",
      slot: 0,
      at: Date.now(),
      decision: {
        control: "south",
        edge: "down",
        gesture: "press",
        level: "base",
        binding: "/layers/base/south",
        action: "tap Ctrl+Z",
        reason: "dispatch",
      },
    });
    app.event("engine.activity", {
      kind: "decision",
      slot: 0,
      at: Date.now(),
      decision: { control: "east", edge: "down", reason: "unbound" },
    });
    fireEvent.click(screen.getByRole("button", { name: "查看诊断" }));
    await screen.findByRole("heading", { name: "诊断", level: 1 });
    await clean("diagnostics with rows");
  });
});

describe("the confirm dialogs of the stick and the trigger", () => {
  it("恢复默认参数？ of a stick", async () => {
    renderApp({ devices: [calibratable] });
    const drawer = await openDrawer("leftstick");
    fireEvent.click(within(drawer).getAllByTestId("analog-params")[0]);
    fireEvent.click(
      await screen.findByRole("menuitem", { name: /恢复默认参数/ }),
    );
    await dialogNamed(/恢复默认参数/);
    await clean("reset stick");
  });
  it("恢复默认参数？ of a trigger", async () => {
    renderApp();
    const drawer = await openDrawer("lefttrigger");
    fireEvent.click(within(drawer).getAllByTestId("analog-params")[0]);
    fireEvent.click(
      await screen.findByRole("menuitem", { name: /恢复默认参数/ }),
    );
    await dialogNamed(/恢复默认参数/);
    await clean("reset trigger");
  });
  it("the d-pad", async () => {
    renderApp();
    await openDrawer("dpad.up");
    await clean("dpad");
  });
});

/** A profile with the settings that the editors confirm before they change. */
const confirmable = {
  schemaVersion: 1,
  name: "Confirm",
  defaultLayer: "base",
  // The M12 set-up of the trigger: its second stage is the half axis.
  analog: { lt: { press: 0.9, release: 0.8 }, axisThreshold: 0.5 },
  layers: {
    base: {
      "ls.right": { press: { type: "keys", keys: "F18", mode: "hold" } },
      "ls.up": { press: { type: "profile", profile: "photo" } },
      "dpad.up": { press: { type: "keys", keys: "Up", mode: "hold" } },
      lt: { label: "Eraser", press: { type: "keys", keys: "E" } },
      "lefttrigger+": { press: { type: "keys", keys: "B" } },
      leftshoulder: {
        tap: { type: "keys", keys: "F13" },
        hold: { type: "profile", profile: "photo" },
      },
    },
  },
};
const withConfirmable = {
  devices: [calibratable],
  library: {
    default: { schemaVersion: 0, name: "默认", layers: { base: {} } },
    paint: confirmable,
    photo: { schemaVersion: 0, name: "Photo", layers: { base: {} } },
  },
};

describe("the other confirm dialogs of the editors", () => {
  afterEach(() => clearParameterClipboard());

  it("改为 WASD？ of a stick", async () => {
    renderApp(withConfirmable);
    const drawer = await openDrawer("leftstick");
    fireEvent.click(within(drawer).getByRole("button", { name: "WASD" }));
    await dialogNamed("改为 WASD？");
    await clean("stick preset");
  });
  it("改为 WASD？ of the d-pad", async () => {
    renderApp(withConfirmable);
    const drawer = await openDrawer("dpad.up");
    fireEvent.click(within(drawer).getByRole("button", { name: "WASD" }));
    await dialogNamed("改为 WASD？");
    await clean("d-pad preset");
  });
  it("清除此设置？ of a direction that switches profiles", async () => {
    renderApp(withConfirmable);
    const drawer = await openDrawer("leftstick");
    fireEvent.click(
      drawer.querySelector(
        '.analog-cell[data-direction="up"]',
      ) as HTMLButtonElement,
    );
    const card = await within(drawer).findByRole("region", {
      name: "↑ 向上推",
    });
    fireEvent.click(within(card).getByRole("button", { name: "清除" }));
    await dialogNamed("清除此设置？");
    await clean("clear direction");
  });
  it("改用按到底？ of a trigger with the old two stages", async () => {
    renderApp(withConfirmable);
    const drawer = await openDrawer("lefttrigger");
    fireEvent.click(within(drawer).getByRole("button", { name: "改用按到底" }));
    await dialogNamed("改用按到底？");
    await clean("trigger full pull");
  });
  it("粘贴参数？ after copying another stick's", async () => {
    renderApp(withConfirmable);
    let drawer = await openDrawer("leftstick");
    fireEvent.click(within(drawer).getAllByTestId("analog-params")[0]);
    fireEvent.click(await screen.findByRole("menuitem", { name: "复制参数" }));
    fireEvent.click(chip("rightstick"));
    drawer = await screen.findByRole("complementary", { name: "按键编辑" });
    fireEvent.click(within(drawer).getAllByTestId("analog-params")[0]);
    fireEvent.click(await screen.findByRole("menuitem", { name: "粘贴参数…" }));
    await dialogNamed("粘贴参数？");
    await clean("paste parameters");
  });
  it("删除此手势？ and 改用快捷键？ of an action from the file", async () => {
    renderApp(withConfirmable);
    const drawer = await openDrawer("leftshoulder");
    fireEvent.click(within(drawer).getByRole("button", { name: /^长按/ }));
    fireEvent.click(within(drawer).getByRole("button", { name: "删除此手势" }));
    const remove = await dialogNamed("删除此手势？");
    await clean("remove gesture");
    fireEvent.click(within(remove).getByRole("button", { name: "取消" }));
    fireEvent.click(
      within(drawer).getByRole("button", { name: "改用快捷键…" }),
    );
    await dialogNamed("改用快捷键？");
    await clean("use a shortcut instead");
  });
});

describe("the confirm dialogs of the scripts page", () => {
  it("移除旧式脚本？", async () => {
    renderApp({
      macros: {
        macros: [],
        missing: [],
        legacy: [{ profile: "photo", profileName: "Photo", entry: "hello.ts" }],
      },
    });
    await controller();
    fireEvent.click(screen.getByRole("button", { name: "脚本" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "「Photo」的旧式脚本的操作" }),
    );
    fireEvent.click(await screen.findByRole("menuitem", { name: "移除" }));
    await dialogNamed("移除旧式脚本？");
    await clean("remove legacy script");
  });
  it("the question whether to replace an imported script", async () => {
    const app = renderApp({ macros: macroList });
    await controller();
    fireEvent.click(screen.getByRole("button", { name: "脚本" }));
    await screen.findByText("复制全部");
    app.api.importMacro.mockResolvedValueOnce({
      exists: true,
      name: "复制全部.ts",
      token: "a".repeat(32),
    });
    fireEvent.click(screen.getByRole("button", { name: "导入" }));
    await dialogNamed("「复制全部」已存在，是否替换？");
    await clean("replace script");
  });
});

describe("dialogs that are rendered on their own", () => {
  it("此软件已绑定", async () => {
    document.title = "test";
    document.documentElement.lang = "zh-CN";
    render(
      <main>
        <h1>配置</h1>
        <BindConflictDialog
          request={{ exe: "krita.exe", from: "Paint", to: "Photo" }}
          language="zh-CN"
          onCancel={() => {}}
          onConfirm={() => {}}
        />
      </main>,
    );
    await dialogNamed("此软件已绑定");
    await clean("bind conflict");
  });
});

// The automatic update (PKG-10) came in a stream of its own: its dialogs and
// the settings row of a build that can update are checked here the way every
// other dialog is. The editor's fixture is a build that cannot update, so the
// pages above only show "开发版不检查更新".
describe("the update dialogs and the update settings", () => {
  const dialogs: [
    name: string,
    fields: Partial<UpdateState>,
    title: string,
    english: string,
  ][] = [
    [
      "available",
      { phase: "available", version: "1.0.1" },
      "有新版本",
      "A new version is available",
    ],
    [
      "downloading",
      { phase: "downloading", version: "1.0.1", percent: 42 },
      "正在下载",
      "Downloading",
    ],
    [
      "downloaded",
      { phase: "downloaded", version: "1.0.1", percent: 100 },
      "下载完成",
      "Download complete",
    ],
    [
      "installing",
      { phase: "installing", version: "1.0.1", percent: 100 },
      "正在安装",
      "Installing",
    ],
    ["upToDate", {}, "已是最新版本", "Up to date"],
    [
      "checkFailed",
      { errorCode: "ERR_UPDATER_LATEST_VERSION_NOT_FOUND" },
      "无法检查更新",
      "Could not check for updates",
    ],
    [
      "downloadFailed",
      { phase: "available", version: "1.0.1", errorCode: "ERR_X" },
      "无法下载更新",
      "Could not download the update",
    ],
    [
      "installFailed",
      { phase: "downloaded", version: "1.0.1", errorCode: "ERR_X" },
      "无法安装更新",
      "Could not install the update",
    ],
  ];

  /** Main says which dialog shows; the window draws it inside a page. */
  async function showDialog(
    kind: UpdateDialogKind,
    fields: Partial<UpdateState>,
    language: "zh-CN" | "en",
    title: string,
  ) {
    document.title = "test";
    document.documentElement.lang = language === "en" ? "en" : "zh-CN";
    const fake = updateApi({
      ...supportedUpdateState,
      dialog: kind,
      ...fields,
    });
    render(
      <main>
        <h1>{language === "en" ? "Settings" : "设置"}</h1>
        <UpdateDialogs api={fake.api} language={language} />
      </main>,
    );
    await dialogNamed(title);
  }

  for (const [kind, fields, title] of dialogs)
    it(`${title} (${kind})`, async () => {
      await showDialog(kind as UpdateDialogKind, fields, "zh-CN", title);
      await clean(`update dialog ${kind}`);
    });

  for (const [kind, fields, , title] of dialogs)
    it(`${title} (${kind}), in English`, async () => {
      await showDialog(kind as UpdateDialogKind, fields, "en", title);
      await clean(`update dialog ${kind} in English`);
    });

  for (const [what, fields] of [
    ["with the daily check on", {}],
    ["with the daily check off", { autoCheck: false }],
    ["while a check runs", { phase: "checking" }],
    [
      "while a version downloads",
      { phase: "downloading", version: "1.0.1", percent: 42 },
    ],
    ["with a version downloaded", { phase: "downloaded", version: "1.0.1" }],
  ] as [string, Partial<UpdateState>][])
    it(`the Settings page of a build that can update, ${what}`, async () => {
      const app = renderApp();
      app.api.getUpdateState.mockImplementation(async () => ({
        ...supportedUpdateState,
        ...fields,
      }));
      fireEvent.click(await screen.findByRole("button", { name: "设置" }));
      await screen.findByRole("heading", { name: "更新", level: 2 });
      await screen.findByRole("switch", { name: "自动检查更新" });
      await clean(`settings, updates ${what}`);
      expect(pageStructure().h1).toHaveLength(1);
    });
});
