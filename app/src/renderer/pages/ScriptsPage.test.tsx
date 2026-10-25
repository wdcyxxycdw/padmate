// @vitest-environment jsdom
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useStore } from "zustand";
import type {
  DaemonEvent,
  LegacyScript,
  MacroInfo,
  MacroList,
  MacroUse,
  PadmateAPI,
  ScriptStatusEntry,
  StatusResult,
  UIErrorData,
} from "../../shared/api";
import type { Profile } from "../../shared/generated/schema";
import { editorAPIFixture } from "../editor-api-fixture";
import { createEditorStore, type EditorStore } from "../editor-store";
import { createPadmateStore, type Language, type PadmateStore } from "../store";
import {
  failureText,
  nameProblem,
  nextScriptName,
  ScriptsPage,
  scriptActionCount,
  UnsavedEdits,
  usedByText,
  usesText,
} from "./ScriptsPage";

const status: StatusResult = {
  version: "0.13.0",
  protocol: 1,
  dryRun: true,
  fakeInput: true,
  paused: false,
  capturing: false,
  suspended: "none",
  profileId: "paint",
  slots: [],
  devices: [],
};

/** A control and gesture of a profile that run a script. */
const use = (
  control: string,
  gesture: string,
  profile = "paint",
  profileName = "绘画",
): MacroUse => ({ profile, profileName, control, gesture });

const copyAll: MacroInfo = {
  file: "复制全部.ts",
  name: "复制全部",
  usedBy: [use("south", "tap"), use("leftshoulder", "hold")],
};
/** hello.ts, the legacy script of profile paint. */
const legacyHello: LegacyScript = {
  profile: "paint",
  profileName: "绘画",
  entry: "hello.ts",
};
const brush: MacroInfo = {
  file: "下一个笔刷.js",
  name: "下一个笔刷",
  usedBy: [],
};

function Harness({
  store,
  editor,
}: {
  store: PadmateStore;
  editor: EditorStore;
}) {
  const state = useStore(store);
  return <ScriptsPage state={state} editor={editor} />;
}

function setup({
  list = { macros: [copyAll, brush], missing: [], legacy: [] } as MacroList,
  snapshot = status,
  scripts = [] as ScriptStatusEntry[],
  language = "zh-CN" as Language,
} = {}) {
  let listener = (_event: DaemonEvent) => {};
  let current = list;
  const fixture = editorAPIFixture();
  const api = {
    ...fixture,
    getConnection: vi.fn(async () => ({ phase: "connected" as const })),
    onConnection: vi.fn(() => vi.fn()),
    onEvent: vi.fn((next: (event: DaemonEvent) => void) => {
      listener = next;
      return vi.fn();
    }),
    getStatus: vi.fn(async () => snapshot),
    getDevices: vi.fn(async () => []),
    pause: vi.fn(async () => ({ paused: true, capturing: false })),
    resume: vi.fn(async () => ({ paused: false, capturing: false })),
    releaseAll: vi.fn(async () => ({ paused: false, capturing: false })),
    getConfig: vi.fn(async () => ({
      text: '{"schemaVersion":0}',
      revision: "c1",
    })),
    putConfig: vi.fn(async () => ({
      revision: "c2",
      applied: true,
      restartRequired: false,
    })),
    getScriptStatus: vi.fn(async () => scripts),
    reloadScripts: vi.fn(async () => scripts),
    getScriptLogs: vi.fn(async () => ({ entries: [], oldest: 0, next: 0 })),
    setSendToPadmate: vi.fn(async (enabled: boolean) => ({
      sendToOwnWindow: enabled,
      localDryRun: !enabled,
    })),
    reconnect: vi.fn(async () => {}),
    listMacros: vi.fn(async () => current),
  } satisfies PadmateAPI;
  const store = createPadmateStore(api);
  store.getState().setLanguage(language);
  // The mapping page's editor; it has no profile open until a test opens one.
  const editor = createEditorStore(api);
  render(<Harness store={store} editor={editor} />);
  act(() => {
    store.getState().start();
  });
  return {
    api,
    store,
    editor,
    setList: (next: MacroList) => {
      current = next;
    },
    event: (topic: DaemonEvent["topic"], data: unknown) =>
      act(() => listener({ topic, data })),
  };
}

/** A profile.json whose south tap runs a script. */
const paintProfile = (file: string): Profile => ({
  schemaVersion: 1,
  name: "paint",
  layers: {
    base: {
      south: { tap: { type: "macro", file } },
      north: { press: { type: "keys", keys: "F13" } },
    },
  },
});

/**
 * Opens paint in the mapping page's editor. The fake daemon keeps one
 * profile.json and its revision, r1 at first; calls records the saves
 * ("put") and the test's own daemon calls in order.
 */
async function openPaint(app: ReturnType<typeof setup>) {
  const disk = { profile: paintProfile("下一个笔刷.js"), revision: 1 };
  const calls: string[] = [];
  /** Writes profile.json as the daemon does; returns the new revision. */
  const write = (profile: Profile) => {
    disk.profile = profile;
    disk.revision += 1;
    return `r${disk.revision}`;
  };
  app.api.getProfile.mockImplementation(async (id: string) => ({
    id,
    text: JSON.stringify(disk.profile),
    revision: `r${disk.revision}`,
    diagnostics: [],
  }));
  app.api.putProfile.mockImplementation(async (_id, text) => {
    calls.push("put");
    return { revision: write(JSON.parse(text)), applied: true };
  });
  await act(() =>
    app.editor
      .getState()
      .activate({ id: "paint", device: "d", connection: "c" }),
  );
  return {
    disk,
    calls,
    write,
    /** One edit on the mapping page, an undo step: north sends keys. */
    edit: (keys: string) => {
      const draft = structuredClone(app.editor.getState().draft as Profile);
      draft.layers.base.north = { press: { type: "keys", keys } };
      act(() => app.editor.getState().edit(draft));
    },
  };
}

const card = async (name: string) =>
  (await screen.findByRole("heading", { name })).closest(
    "article",
  ) as HTMLElement;
const button = (name: string) =>
  screen.getByRole("button", { name }) as HTMLButtonElement;
const openMenu = (article: HTMLElement) =>
  fireEvent.click(within(article).getByTestId("script-menu"));
const menuItem = (name: string) =>
  screen.getByRole("menuitem", { name }) as HTMLButtonElement;
const dialog = (name: string) => screen.getByRole("dialog", { name });

beforeEach(() => localStorage.clear());
afterEach(cleanup);

describe("scripts page helpers", () => {
  it("names controls and gestures as the controller prints them", () => {
    const labels = { south: "A", leftshoulder: "LB", leftstick: "LS" };
    expect(usesText(copyAll.usedBy, labels, "zh-CN")).toBe("A 点按 · LB 长按");
    expect(usesText(copyAll.usedBy, labels, "en")).toBe("A Tap · LB Hold");
    expect(
      usesText([use("south", "tap"), use("south", "tap")], labels, "zh-CN"),
    ).toBe("A 点按");
    // Every control the daemon reports: diagonals, the stick's any
    // direction, raw axes and a trigger's half press too.
    const tap = (control: string) => [use(control, "tap")];
    for (const [control, zh, en] of [
      ["ls.up", "LS ↑ 点按", "LS ↑ Tap"],
      ["ls.upleft", "LS ↖ 点按", "LS ↖ Tap"],
      ["ls.active", "LS 任意方向 点按", "LS any direction Tap"],
      ["dpad.left", "十字键 ← 点按", "D-pad ← Tap"],
      ["dpad.downright", "十字键 ↘ 点按", "D-pad ↘ Tap"],
      ["leftx+", "LS 原始轴向右 点按", "LS Raw right Tap"],
      ["lefttrigger+", "LT 半按 点按", "LT Half press Tap"],
      ["lt", "LT 点按", "LT Tap"],
    ]) {
      const names = { ...labels, lefttrigger: "LT" };
      expect(usesText(tap(control), names, "zh-CN"), control).toBe(zh);
      expect(usesText(tap(control), names, "en"), control).toBe(en);
    }
  });

  it("names the keys that run a script by profile", () => {
    const labels = { south: "A", leftshoulder: "LB", north: "Y" };
    const uses = [
      use("south", "tap"),
      use("north", "press", "photo", "照片"),
      use("leftshoulder", "hold"),
      use("north", "press", "photo", "照片"),
    ];
    // A profile's keys stay together, in the order profiles first appear.
    expect(usedByText(uses, labels, "zh-CN")).toBe(
      "绘画：A 点按 · LB 长按；照片：Y 按下",
    );
    expect(usedByText(uses, labels, "en")).toBe(
      "绘画: A Tap · LB Hold; 照片: Y Press",
    );
    expect(usedByText([], labels, "zh-CN")).toBe("");
  });

  it("checks names with the daemon's rule and the other scripts", () => {
    const macros = [copyAll, brush];
    expect(nameProblem("新名字", macros)).toBe("");
    expect(nameProblem("", macros)).toBe("empty");
    expect(nameProblem("a.b", macros)).toBe("char");
    expect(nameProblem("一".repeat(41), macros)).toBe("length");
    expect(nameProblem("CON", macros)).toBe("invalid");
    expect(nameProblem("-a", macros)).toBe("invalid");
    // Names differ in more than case or the extension.
    expect(nameProblem("下一个笔刷", macros)).toBe("exists");
    expect(nameProblem("复制全部", macros, "复制全部.ts")).toBe("");
  });

  it("numbers a new script after the names in use", () => {
    expect(nextScriptName([], "zh-CN")).toBe("脚本 1");
    expect(
      nextScriptName(
        [
          { file: "脚本 1.ts", name: "脚本 1", usedBy: [] },
          { file: "脚本 3.js", name: "脚本 3", usedBy: [] },
        ],
        "zh-CN",
      ),
    ).toBe("脚本 2");
    expect(nextScriptName([], "en")).toBe("Script 1");
  });

  it("counts a legacy script's actions in the layers and common", () => {
    const text = JSON.stringify({
      script: { entry: "main.ts", api: "0.21" },
      layers: {
        base: {
          south: { tap: { type: "script", name: "a" } },
          east: { press: { type: "keys", keys: "A" } },
        },
        photo: { west: { hold: { type: "script", name: "b", args: {} } } },
      },
      common: { north: { press: { type: "script", name: "c" } } },
    });
    expect(scriptActionCount(text)).toBe(3);
    expect(scriptActionCount(`﻿${text}`)).toBe(3);
    expect(scriptActionCount("{")).toBe(0);
  });

  it("counts a legacy script's actions in the sets of the function keys too", () => {
    const text = JSON.stringify({
      layers: { base: { south: { tap: { type: "script", name: "a" } } } },
      functionKeys: {
        back: { east: { press: { type: "script", name: "b" } } },
        start: { west: { hold: { type: "script", name: "c" } } },
      },
    });
    expect(scriptActionCount(text)).toBe(3);
  });

  it("translates refusals and keeps daemon messages", () => {
    const cases: [string, string, string][] = [
      [
        "type",
        "只能导入 .ts 或 .js 文件",
        "Only .ts or .js files can be imported",
      ],
      [
        "declaration",
        "不能导入 .d.ts 类型文件",
        "A .d.ts type file can't be imported",
      ],
      [
        "name",
        "无法使用此文件名，请重命名后再导入",
        "This file name can't be used; rename the file and import it again",
      ],
      ["size", "文件超过 1 MiB", "The file is larger than 1 MiB"],
      ["encoding", "文件不是 UTF-8 编码", "The file is not UTF-8 text"],
      ["read", "无法读取此文件", "The file could not be read"],
      [
        "expired",
        "确认超时，请重新导入",
        "The confirmation expired; import the file again",
      ],
    ];
    for (const [reason, zh, en] of cases) {
      const error: UIErrorData = { message: "English", data: { reason } };
      expect(failureText("import", error, "zh-CN")).toBe(zh);
      expect(failureText("import", error, "en")).toBe(en);
    }
    const name = (kind: string): UIErrorData => ({
      message: "x",
      code: -32602,
      data: { kind },
    });
    expect(failureText("new", name("char"), "zh-CN")).toBe(
      '名称不能包含 \\ / : * ? " < > | . ~',
    );
    expect(failureText("rename", name("length"), "zh-CN")).toBe(
      "名称最多 40 个字",
    );
    expect(failureText("new", name("invalid"), "en")).toBe(
      "This name can’t be used",
    );
    expect(
      failureText("new", { message: "x", data: { limit: 64 } }, "zh-CN"),
    ).toBe("最多 64 个脚本");
    expect(failureText("rename", { message: "x", code: -32016 }, "zh-CN")).toBe(
      "已有同名脚本",
    );
    expect(failureText("edit", { message: "x", code: -32019 }, "zh-CN")).toBe(
      "找不到文件",
    );
    const daemon: UIErrorData = { message: "disk full", code: -32098 };
    expect(failureText("import", daemon, "zh-CN")).toBe(
      "无法导入脚本：disk full",
    );
    expect(failureText("delete", daemon, "en")).toBe(
      "Could not delete the script: disk full",
    );
    // A reason only means a refused file on import.
    expect(
      failureText("new", { message: "x", data: { reason: "type" } }, "zh-CN"),
    ).toBe("无法新建脚本：x");
  });
});

describe("scripts page: list", () => {
  it("shows one card per script with the keys that run it", async () => {
    const app = setup();
    const copy = await card("复制全部");
    // No controller: the generic labels.
    expect(copy.textContent).toContain("A 点按");
    expect(copy.textContent).toContain("长按");
    expect((await card("下一个笔刷")).textContent).toContain("未使用");
    expect(app.api.listMacros).toHaveBeenCalledWith();
    // It says which profile's keys run it.
    expect(copy.textContent).toContain("绘画：A 点按");
    openMenu(copy);
    expect(
      within(screen.getByRole("menu"))
        .getAllByRole("menuitem")
        .map((item) => item.textContent),
    ).toEqual(["编辑", "重命名", "删除"]);
    // The page has only 新建 and 导入 on top; no reload all.
    expect(button("新建").disabled).toBe(false);
    expect(button("导入").disabled).toBe(false);
    expect(screen.queryByRole("button", { name: "全部重新加载" })).toBeNull();
  });

  it("lists scripts by creation time, oldest first, then missing and legacy scripts", async () => {
    const made = (name: string, created?: number): MacroInfo => ({
      file: `${name}.ts`,
      name,
      usedBy: [],
      ...(created === undefined ? {} : { created }),
    });
    setup({
      list: {
        macros: [
          made("复制全部 10", 4),
          made("新", 5),
          made("复制全部", 1),
          made("复制全部 2", 2),
        ],
        missing: [{ file: "旧.ts", usedBy: [use("east", "press")] }],
        legacy: [legacyHello],
      },
    });
    await card("复制全部");
    expect(
      screen
        .getAllByRole("heading", { level: 2 })
        .map((heading) => heading.textContent),
    ).toEqual([
      "复制全部",
      "复制全部 2",
      "复制全部 10",
      "新",
      "旧",
      "hello.ts旧式 · 绘画",
    ]);
  });

  it("falls back to dictionary order of the names without creation times", async () => {
    const made = (name: string): MacroInfo => ({
      file: `${name}.ts`,
      name,
      usedBy: [],
    });
    setup({
      list: {
        macros: [made("b 10"), made("b 2"), made("b"), made("a")],
        missing: [],
        legacy: [],
      },
    });
    await card("a");
    expect(
      screen
        .getAllByRole("heading", { level: 2 })
        .map((heading) => heading.textContent),
    ).toEqual(["a", "b", "b 2", "b 10"]);
  });

  it("shows build, load and run errors on the card, with copy", async () => {
    const writeText = vi.fn(async (_text: string) => {});
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText },
      configurable: true,
    });
    const running = (
      macros: NonNullable<ScriptStatusEntry["script"]["macros"]>,
      generation = 1,
    ): ScriptStatusEntry => ({
      slot: 0,
      device: 1,
      key: "pad",
      profileId: "paint",
      generation,
      script: {
        state: "Running",
        paused: false,
        pending: false,
        wallStopped: false,
        restartCount: 0,
        permanent: false,
        metadata: { runtime: 1 },
        macros,
      },
    });
    setup({
      list: {
        macros: [
          { ...copyAll, problem: "复制全部.ts:3:9: error: oops\nmore" },
          brush,
          { file: "死循环.ts", name: "死循环", usedBy: [] },
        ],
        missing: [],
        legacy: [],
      },
      scripts: [
        running([{ file: "下一个笔刷.js", error: "下一个笔刷.js:2:1 boom" }]),
        running(
          [{ file: "死循环.ts", error: "interrupted", disabled: true }],
          2,
        ),
      ],
    });
    const copy = await card("复制全部");
    const line = within(copy).getByTestId("script-error");
    expect(line.textContent).toContain("出错");
    expect(line.textContent).toContain("复制全部.ts:3:9: error: oops");
    expect(line.querySelector("pre")).toBeNull();
    fireEvent.click(within(line).getByRole("button", { expanded: false }));
    expect(line.querySelector("pre")?.textContent).toBe(
      "复制全部.ts:3:9: error: oops\nmore",
    );
    fireEvent.click(within(line).getByRole("button", { name: "复制" }));
    await within(line).findByRole("button", { name: "已复制" });
    expect(writeText).toHaveBeenCalledWith(
      "复制全部.ts:3:9: error: oops\nmore",
    );
    expect(
      within(await card("下一个笔刷")).getByTestId("script-error").textContent,
    ).toContain("下一个笔刷.js:2:1 boom");
    expect(
      within(await card("死循环")).getByTestId("script-error").textContent,
    ).toContain("已停用，请保存文件后重试");
  });

  it("shows missing files, unusable names and the legacy script", async () => {
    setup({
      list: {
        macros: [
          { file: "v1.2.ts", name: "v1.2", usedBy: [], invalidName: true },
        ],
        missing: [{ file: "旧.ts", usedBy: [use("east", "press")] }],
        legacy: [legacyHello],
      },
    });
    const invalid = await card("v1.2");
    expect(invalid.textContent).toContain("无法使用此名称");
    openMenu(invalid);
    expect(
      within(screen.getByRole("menu"))
        .getAllByRole("menuitem")
        .map((item) => item.textContent),
    ).toEqual(["重命名", "删除"]);
    fireEvent.keyDown(document, { key: "Escape" });
    const missing = await card("旧");
    expect(missing.textContent).toContain("找不到文件 · 绘画：B 按下");
    openMenu(missing);
    expect(
      within(screen.getByRole("menu"))
        .getAllByRole("menuitem")
        .map((item) => item.textContent),
    ).toEqual(["删除"]);
    fireEvent.keyDown(document, { key: "Escape" });
    const legacy = await card("hello.ts旧式 · 绘画");
    expect(legacy.textContent).toContain("随配置持续运行");
    openMenu(legacy);
    expect(
      within(screen.getByRole("menu"))
        .getAllByRole("menuitem")
        .map((item) => item.textContent),
    ).toEqual(["编辑", "移除"]);
    // The legacy script is the last card.
    const cards = screen.getAllByRole("article");
    expect(cards[cards.length - 1]).toBe(legacy);
  });

  it("says when a failed legacy script kept the scripts from loading", async () => {
    setup({
      list: { macros: [copyAll], missing: [], legacy: [legacyHello] },
      scripts: [
        {
          slot: 0,
          device: 1,
          key: "pad",
          profileId: "paint",
          generation: 1,
          script: {
            state: "Backoff",
            entry: "hello.ts",
            paused: false,
            pending: false,
            wallStopped: false,
            restartCount: 1,
            permanent: false,
            metadata: { runtime: 1 },
            lastError: "hello.ts:1:1 SyntaxError",
          },
        },
      ],
    });
    const legacy = await card("hello.ts旧式 · 绘画");
    expect(within(legacy).getByTestId("script-error").textContent).toContain(
      "旧式脚本出错，所有脚本均未加载",
    );
  });

  it("lists every profile's legacy script at the end, each with its profile", async () => {
    setup({
      list: {
        macros: [copyAll],
        missing: [{ file: "旧.ts", usedBy: [use("east", "press")] }],
        legacy: [
          legacyHello,
          { profile: "photo", profileName: "照片", entry: "photo.ts" },
        ],
      },
    });
    await card("复制全部");
    const names = screen
      .getAllByRole("heading", { level: 2 })
      .map((heading) => heading.textContent);
    expect(names).toEqual([
      "复制全部",
      "旧",
      "hello.ts旧式 · 绘画",
      "photo.ts旧式 · 照片",
    ]);
  });

  it("shows only the failed legacy script's own profile's error", async () => {
    const failed = (profileId: string): ScriptStatusEntry => ({
      slot: 0,
      device: 1,
      key: "pad",
      profileId,
      generation: 1,
      script: {
        state: "Backoff",
        paused: false,
        pending: false,
        wallStopped: false,
        restartCount: 1,
        permanent: false,
        metadata: { runtime: 1 },
        lastError: `${profileId}.ts:1:1 SyntaxError`,
      },
    });
    setup({
      list: {
        macros: [],
        missing: [],
        legacy: [
          legacyHello,
          { profile: "photo", profileName: "照片", entry: "photo.ts" },
        ],
      },
      scripts: [failed("photo")],
    });
    const photo = await card("photo.ts旧式 · 照片");
    expect(within(photo).getByTestId("script-error").textContent).toContain(
      "photo.ts:1:1 SyntaxError",
    );
    expect(
      within(await card("hello.ts旧式 · 绘画")).queryByTestId("script-error"),
    ).toBeNull();
  });

  it("shows a script's error whichever profile's runtime reports it", async () => {
    setup({
      list: { macros: [brush], missing: [], legacy: [] },
      scripts: [
        {
          slot: 0,
          device: 1,
          key: "pad",
          profileId: "photo",
          generation: 1,
          script: {
            state: "Running",
            paused: false,
            pending: false,
            wallStopped: false,
            restartCount: 0,
            permanent: false,
            metadata: { runtime: 1 },
            macros: [{ file: "下一个笔刷.js", error: "boom" }],
          },
        },
      ],
    });
    expect(
      within(await card("下一个笔刷")).getByTestId("script-error").textContent,
    ).toContain("boom");
  });

  it("offers a reload when the scripts stopped for good", async () => {
    const app = setup({
      scripts: [
        {
          slot: 0,
          device: 1,
          key: "pad",
          profileId: "paint",
          generation: 1,
          script: {
            state: "Stopped",
            paused: false,
            pending: false,
            wallStopped: false,
            restartCount: 3,
            permanent: true,
            metadata: { runtime: 1 },
          },
        },
      ],
    });
    fireEvent.click(await screen.findByRole("button", { name: "重新加载" }));
    await waitFor(() => expect(app.api.reloadScripts).toHaveBeenCalled());
  });

  it("explains an empty list", async () => {
    setup({ list: { macros: [], missing: [], legacy: [] } });
    expect(
      await screen.findByRole("heading", { name: "暂无脚本" }),
    ).toBeTruthy();
    expect(
      screen.getByText(
        "脚本通过代码发送按键，可以等待或按条件发送不同的按键。",
      ),
    ).toBeTruthy();
  });

  it("lists the library whichever profile is open, even none", async () => {
    const app = setup({ snapshot: { ...status, profileId: "" } });
    expect((await card("复制全部")).textContent).toContain("绘画：A 点按");
    expect(screen.queryByText("请先选择配置")).toBeNull();
    expect(button("新建").disabled).toBe(false);
    expect(button("导入").disabled).toBe(false);
    expect(app.api.listMacros).toHaveBeenCalledWith();
  });

  it("names the profiles of a script that several of them use", async () => {
    setup({
      list: {
        macros: [
          {
            file: "画笔.ts",
            name: "画笔",
            usedBy: [
              use("south", "tap"),
              use("east", "tap", "default", "默认"),
            ],
          },
        ],
        missing: [],
      },
    });
    expect((await card("画笔")).textContent).toContain(
      "绘画：A 点按；默认：B 点按",
    );
  });

  it("reads the list again on profile.changed and window focus", async () => {
    const app = setup();
    await card("复制全部");
    app.setList({ macros: [brush], missing: [] });
    // A change of the script folder names no profile.
    app.event("profile.changed", { kind: "macros" });
    await waitFor(() =>
      expect(screen.queryByRole("heading", { name: "复制全部" })).toBeNull(),
    );
    app.setList({ macros: [copyAll], missing: [] });
    act(() => {
      window.dispatchEvent(new Event("focus"));
    });
    await card("复制全部");
  });

  it("shows why the list could not be read", async () => {
    const app = setup();
    await card("复制全部");
    app.api.listMacros.mockRejectedValue({ message: "boom", code: -32098 });
    app.event("profile.changed", { kind: "macros" });
    expect(await screen.findByText("无法读取脚本列表：boom")).toBeTruthy();
  });
});

describe("scripts page: actions", () => {
  it("creates a script with the next free name and opens it", async () => {
    const app = setup({
      list: {
        macros: [{ file: "脚本 1.ts", name: "脚本 1", usedBy: [] }],
        missing: [],
      },
    });
    await card("脚本 1");
    fireEvent.click(button("新建"));
    const form = dialog("新建脚本");
    const input = within(form).getByRole("textbox", {
      name: "名称",
    }) as HTMLInputElement;
    expect(input.value).toBe("脚本 2");
    expect(document.activeElement).toBe(input);
    // A name the daemon refuses says why and cannot be confirmed.
    for (const [value, message] of [
      ["  ", "请输入名称"],
      ["a.b", '名称不能包含 \\ / : * ? " < > | . ~'],
      ["一".repeat(41), "名称最多 40 个字"],
      ["CON", "无法使用此名称"],
      ["脚本 1", "已有同名脚本"],
    ]) {
      fireEvent.change(input, { target: { value } });
      expect(within(form).getByRole("alert").textContent).toBe(message);
      expect(
        (
          within(form).getByRole("button", {
            name: "新建",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(true);
    }
    // Spaces at either end are dropped and the name is NFC.
    fireEvent.change(input, { target: { value: "  复制全部  " } });
    fireEvent.submit(input.closest("form") as HTMLFormElement);
    await waitFor(() =>
      expect(app.api.createMacro).toHaveBeenCalledWith("复制全部"),
    );
    await waitFor(() =>
      expect(app.api.editMacro).toHaveBeenCalledWith("复制全部.ts"),
    );
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "新建脚本" })).toBeNull(),
    );
    expect(screen.queryByTestId("script-outcome")).toBeNull();
  });

  it("says when no editor opens and keeps a failed name in the dialog", async () => {
    const app = setup();
    await card("复制全部");
    app.api.editMacro.mockResolvedValueOnce({ opened: false });
    fireEvent.click(button("新建"));
    fireEvent.click(
      within(dialog("新建脚本")).getByRole("button", { name: "新建" }),
    );
    expect((await screen.findByTestId("script-outcome")).textContent).toBe(
      "无法打开编辑器",
    );
    app.api.createMacro.mockRejectedValueOnce({
      message: "exists",
      code: -32016,
      data: { exists: true, name: "脚本 1.ts", replaceable: false },
    });
    fireEvent.click(button("新建"));
    const form = dialog("新建脚本");
    fireEvent.click(within(form).getByRole("button", { name: "新建" }));
    expect((await within(form).findByRole("alert")).textContent).toBe(
      "已有同名脚本",
    );
    // The refusal was about that name: another one clears it, and it
    // shows again only for the refused name.
    const input = within(form).getByRole("textbox", { name: "名称" });
    fireEvent.change(input, { target: { value: "脚本 9" } });
    expect(within(form).queryByRole("alert")).toBeNull();
    fireEvent.change(input, { target: { value: "脚本 1" } });
    expect(within(form).getByRole("alert").textContent).toBe("已有同名脚本");
  });

  it("opens a script and the legacy entry in the editor", async () => {
    const app = setup({
      list: {
        macros: [copyAll],
        missing: [],
        legacy: [
          legacyHello,
          { profile: "photo", profileName: "照片", entry: "photo.ts" },
        ],
      },
    });
    openMenu(await card("复制全部"));
    fireEvent.click(menuItem("编辑"));
    await waitFor(() =>
      expect(app.api.editMacro).toHaveBeenCalledWith("复制全部.ts"),
    );
    // A legacy script is edited with its own profile's ID.
    openMenu(await card("photo.ts旧式 · 照片"));
    fireEvent.click(menuItem("编辑"));
    await waitFor(() =>
      expect(app.api.editScript).toHaveBeenCalledWith("photo"),
    );
    app.api.editMacro.mockRejectedValueOnce({ message: "gone", code: -32019 });
    openMenu(await card("复制全部"));
    fireEvent.click(menuItem("编辑"));
    expect((await screen.findByTestId("script-outcome")).textContent).toBe(
      "找不到文件",
    );
  });

  it("renames a script and keeps its extension", async () => {
    const app = setup();
    openMenu(await card("下一个笔刷"));
    fireEvent.click(menuItem("重命名"));
    const form = dialog("重命名");
    const input = within(form).getByRole("textbox", {
      name: "名称",
    }) as HTMLInputElement;
    expect(input.value).toBe("下一个笔刷");
    // The same name changes nothing.
    expect(
      (within(form).getByRole("button", { name: "确定" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    fireEvent.change(input, { target: { value: "复制全部" } });
    expect(within(form).getByRole("alert").textContent).toBe("已有同名脚本");
    fireEvent.change(input, { target: { value: "上一个笔刷" } });
    fireEvent.click(within(form).getByRole("button", { name: "确定" }));
    await waitFor(() =>
      expect(app.api.renameMacro).toHaveBeenCalledWith(
        "下一个笔刷.js",
        "上一个笔刷.js",
      ),
    );
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "重命名" })).toBeNull(),
    );
  });

  it("always asks before deleting, with cancel focused", async () => {
    // The counts are the actions a deletion clears: a key and gesture can
    // run the script in several layers and in common.
    const app = setup({
      list: {
        macros: [
          {
            ...copyAll,
            usedBy: [...copyAll.usedBy, use("east", "tap", "photo", "照片")],
            actions: 3,
          },
          brush,
        ],
        missing: [
          {
            file: "旧.ts",
            usedBy: [use("east", "tap"), use("east", "hold", "photo", "照片")],
            actions: 2,
          },
        ],
      },
    });
    openMenu(await card("下一个笔刷"));
    fireEvent.click(menuItem("删除"));
    let question = dialog("删除「下一个笔刷」？");
    expect(question.textContent).toContain("文件删除后无法恢复。");
    expect(document.activeElement).toBe(
      within(question).getByRole("button", { name: "取消" }),
    );
    fireEvent.click(within(question).getByRole("button", { name: "取消" }));
    expect(app.api.deleteMacro).not.toHaveBeenCalled();

    openMenu(await card("复制全部"));
    fireEvent.click(menuItem("删除"));
    question = dialog("删除「复制全部」？");
    // It counts the keys and the profiles they are in.
    expect(question.textContent).toContain(
      "有 3 处使用此脚本，涉及 2 个配置，删除后这些位置将被清空。文件删除后无法恢复。",
    );
    fireEvent.click(within(question).getByRole("button", { name: "删除" }));
    await waitFor(() =>
      expect(app.api.deleteMacro).toHaveBeenCalledWith("复制全部.ts"),
    );

    openMenu(await card("旧"));
    fireEvent.click(menuItem("删除"));
    question = dialog("清除对「旧」的引用？");
    expect(question.textContent).toContain("2 处将被清空，涉及 2 个配置。");
    fireEvent.click(within(question).getByRole("button", { name: "清除" }));
    await waitFor(() =>
      expect(app.api.deleteMacro).toHaveBeenCalledWith("旧.ts"),
    );
  });

  it("removes the legacy script after saying what it clears", async () => {
    const app = setup({
      list: {
        macros: [],
        missing: [],
        legacy: [{ profile: "photo", profileName: "照片", entry: "hello.ts" }],
      },
    });
    app.api.getProfile.mockResolvedValue({
      id: "photo",
      text: JSON.stringify({
        script: { entry: "hello.ts", api: "0.21" },
        layers: { base: { south: { tap: { type: "script", name: "a" } } } },
      }),
      revision: "r1",
      diagnostics: [],
    });
    openMenu(await card("hello.ts旧式 · 照片"));
    fireEvent.click(menuItem("移除"));
    const question = dialog("移除旧式脚本？");
    await waitFor(() =>
      expect(question.textContent).toContain(
        "配置「照片」将不再运行 hello.ts，文件保留。1 处脚本动作将被清空。",
      ),
    );
    expect(app.api.getProfile).toHaveBeenCalledWith("photo");
    fireEvent.click(within(question).getByRole("button", { name: "移除" }));
    // It acts on the profile the card names, not the open one.
    await waitFor(() =>
      expect(app.api.detachScript).toHaveBeenCalledWith("photo"),
    );
  });

  it("imports a file, asking before it replaces one", async () => {
    const app = setup();
    await card("复制全部");
    fireEvent.click(button("导入"));
    await waitFor(() => expect(app.api.importMacro).toHaveBeenCalledWith());
    app.api.importMacro.mockResolvedValueOnce({
      exists: true,
      name: "复制全部.ts",
      token: "a".repeat(32),
    });
    fireEvent.click(button("导入"));
    const question = await screen.findByRole("dialog", {
      name: "「复制全部」已存在，是否替换？",
    });
    // The dialog says what replacing does, not only its title.
    expect(question.textContent).toContain(
      "替换后，所有使用此脚本的按键都运行导入的内容。",
    );
    fireEvent.click(within(question).getByRole("button", { name: "替换" }));
    await waitFor(() =>
      expect(app.api.importMacro).toHaveBeenLastCalledWith({
        replace: true,
        token: "a".repeat(32),
      }),
    );
    // Another extension cannot replace it.
    app.api.importMacro.mockResolvedValueOnce({
      exists: true,
      name: "复制全部.ts",
    });
    fireEvent.click(button("导入"));
    expect((await screen.findByTestId("script-outcome")).textContent).toBe(
      "「复制全部」已存在",
    );
    app.api.importMacro.mockRejectedValueOnce({
      message: "x",
      data: { reason: "encoding" },
    });
    fireEvent.click(button("导入"));
    await waitFor(() =>
      expect(screen.getByTestId("script-outcome").textContent).toBe(
        "文件不是 UTF-8 编码",
      ),
    );
    // A canceled dialog says nothing.
    app.api.importMacro.mockResolvedValueOnce({ canceled: true });
    fireEvent.click(button("导入"));
    await waitFor(() =>
      expect(screen.queryByTestId("script-outcome")).toBeNull(),
    );
  });

  it("keeps what writes profile.json unavailable while the mapping page saves", async () => {
    const app = setup({
      list: { macros: [copyAll], missing: [], legacy: [legacyHello] },
    });
    const paint = await openPaint(app);
    let release = () => {};
    app.api.putProfile.mockImplementationOnce(async (_id, text) => {
      await new Promise<void>((resolve) => {
        release = resolve;
      });
      return { revision: paint.write(JSON.parse(text)), applied: true };
    });
    paint.edit("F14");
    expect(app.editor.getState().saving).toBe(true);
    openMenu(await card("复制全部"));
    expect(menuItem("编辑").disabled).toBe(false);
    for (const name of ["重命名", "删除"]) {
      expect(menuItem(name).disabled).toBe(true);
      expect(menuItem(name).getAttribute("aria-describedby")).toBeTruthy();
    }
    expect(screen.getAllByText("映射页的修改正在保存").length).toBeGreaterThan(
      0,
    );
    fireEvent.keyDown(document, { key: "Escape" });
    openMenu(await card("hello.ts旧式 · 绘画"));
    expect(menuItem("移除").disabled).toBe(true);
    fireEvent.keyDown(document, { key: "Escape" });
    // New and import do not touch profile.json.
    expect(button("新建").disabled).toBe(false);
    expect(button("导入").disabled).toBe(false);
    await act(async () => release());
    await waitFor(() => expect(app.editor.getState().saving).toBe(false));
    openMenu(await card("复制全部"));
    expect(menuItem("重命名").disabled).toBe(false);
    expect(screen.queryByText("映射页的修改正在保存")).toBeNull();
  });

  it("saves the mapping page first, then renames and reads the profile again", async () => {
    const app = setup();
    const paint = await openPaint(app);
    // The save fails once; renaming retries it before the daemon renames.
    app.api.putProfile.mockImplementationOnce(async () => {
      paint.calls.push("failed put");
      throw { code: -32098, message: "disk full" };
    });
    paint.edit("F14");
    await waitFor(() =>
      expect(app.editor.getState().saveProblem).toBe("failed"),
    );
    expect(app.editor.getState().canUndo).toBe(true);
    app.api.renameMacro.mockImplementationOnce(async (file, to) => {
      paint.calls.push("rename");
      // The daemon renames the reference in the file it has.
      const tap = paint.disk.profile.layers.base.south?.tap;
      expect(tap).toEqual({ type: "macro", file });
      const revision = paint.write({
        ...paint.disk.profile,
        layers: {
          base: {
            ...paint.disk.profile.layers.base,
            south: { tap: { type: "macro", file: to } },
          },
        },
      });
      return {
        file: to,
        bindings: 1,
        profiles: [{ id: "paint", revision, bindings: 1 }],
      };
    });
    openMenu(await card("下一个笔刷"));
    fireEvent.click(menuItem("重命名"));
    const form = dialog("重命名");
    fireEvent.change(within(form).getByRole("textbox", { name: "名称" }), {
      target: { value: "上一个笔刷" },
    });
    fireEvent.click(within(form).getByRole("button", { name: "确定" }));
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "重命名" })).toBeNull(),
    );
    // The edit reached the disk before the daemon rewrote the file.
    expect(paint.calls).toEqual(["failed put", "put", "rename"]);
    // The draft is the file again, not the old draft with a new revision,
    // and the steps made with the old name are gone.
    const state = app.editor.getState();
    expect(state.revision).toBe("r3");
    expect(state.draft?.layers.base.south?.tap).toEqual({
      type: "macro",
      file: "上一个笔刷.js",
    });
    expect(state.draft?.layers.base.north?.press).toEqual({
      type: "keys",
      keys: "F14",
    });
    expect(state.unsaved).toBe(false);
    expect(state.canUndo).toBe(false);
    expect(state.undo()).toBe(false);
    expect(paint.calls).toEqual(["failed put", "put", "rename"]);
  });

  it("deletes and removes after the saves, then reads the profile again", async () => {
    const app = setup({
      list: { macros: [copyAll], missing: [], legacy: [legacyHello] },
    });
    const paint = await openPaint(app);
    paint.edit("F14");
    await waitFor(() => expect(app.editor.getState().saving).toBe(false));
    expect(app.editor.getState().canUndo).toBe(true);
    // A delete whose file stays has cleared the references already.
    app.api.deleteMacro.mockImplementationOnce(async () => {
      paint.calls.push("delete");
      paint.write({ schemaVersion: 1, name: "paint", layers: { base: {} } });
      throw { code: -32098, message: "the file is in use" };
    });
    openMenu(await card("复制全部"));
    fireEvent.click(menuItem("删除"));
    fireEvent.click(
      within(dialog("删除「复制全部」？")).getByRole("button", {
        name: "删除",
      }),
    );
    expect((await screen.findByTestId("script-outcome")).textContent).toContain(
      "无法删除脚本：the file is in use",
    );
    await waitFor(() =>
      expect(app.editor.getState().draft?.layers.base).toEqual({}),
    );
    expect(app.editor.getState().canUndo).toBe(false);
    // Removing the legacy script: the same order.
    paint.edit("F15");
    app.api.detachScript.mockImplementationOnce(async () => {
      paint.calls.push("detach");
      const revision = paint.write({
        schemaVersion: 1,
        name: "paint",
        layers: { base: {} },
      });
      return { revision, bindings: 0 };
    });
    openMenu(await card("hello.ts旧式 · 绘画"));
    fireEvent.click(menuItem("移除"));
    fireEvent.click(
      within(dialog("移除旧式脚本？")).getByRole("button", { name: "移除" }),
    );
    await waitFor(() =>
      expect(app.editor.getState().draft?.layers.base.north).toBeUndefined(),
    );
    expect(paint.calls).toEqual(["put", "delete", "put", "detach"]);
    expect(app.editor.getState().revision).toBe("r5");
    expect(app.editor.getState().canUndo).toBe(false);
  });

  it("does not rename while the mapping page's edits cannot be saved", async () => {
    const app = setup();
    const paint = await openPaint(app);
    // The daemon refuses the edit.
    app.api.validateProfile.mockResolvedValue({
      valid: false,
      diagnostics: [],
    });
    paint.edit("F16");
    await waitFor(() =>
      expect(app.editor.getState().saveProblem).toBe("invalid"),
    );
    openMenu(await card("下一个笔刷"));
    fireEvent.click(menuItem("重命名"));
    const form = dialog("重命名");
    fireEvent.change(within(form).getByRole("textbox", { name: "名称" }), {
      target: { value: "上一个笔刷" },
    });
    fireEvent.click(within(form).getByRole("button", { name: "确定" }));
    expect((await within(form).findByRole("alert")).textContent).toBe(
      "无法重命名脚本：映射页的修改尚未保存",
    );
    expect(app.api.renameMacro).not.toHaveBeenCalled();
    // The edit stays for the mapping page to fix or undo.
    expect(app.editor.getState().unsaved).toBe(true);
    expect(app.editor.getState().canUndo).toBe(true);
    expect(failureText("delete", new UnsavedEdits(), "en")).toBe(
      "Could not delete the script: the mapping page has edits that are not saved",
    );
  });

  it("rewrites every profile that uses a script, and drops the open one's undo steps only when it was written", async () => {
    const app = setup();
    const paint = await openPaint(app);
    paint.edit("F14");
    await waitFor(() => expect(app.editor.getState().saving).toBe(false));
    expect(app.editor.getState().canUndo).toBe(true);
    // Another profile uses the script; the open profile does not.
    app.api.renameMacro.mockResolvedValueOnce({
      file: "上一个笔刷.js",
      bindings: 2,
      profiles: [{ id: "photo", revision: "p2", bindings: 2 }],
    });
    openMenu(await card("下一个笔刷"));
    fireEvent.click(menuItem("重命名"));
    const form = dialog("重命名");
    fireEvent.change(within(form).getByRole("textbox", { name: "名称" }), {
      target: { value: "上一个笔刷" },
    });
    fireEvent.click(within(form).getByRole("button", { name: "确定" }));
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "重命名" })).toBeNull(),
    );
    // The open profile was saved first and read again, but nothing in it
    // changed: its steps are still there.
    expect(paint.calls).toEqual(["put"]);
    expect(app.editor.getState().canUndo).toBe(true);
    // A delete that wrote the open profile drops them.
    app.api.deleteMacro.mockImplementationOnce(async () => {
      const revision = paint.write({
        ...paint.disk.profile,
        layers: { base: { north: paint.disk.profile.layers.base.north } },
      });
      return {
        bindings: 1,
        profiles: [{ id: "paint", revision, bindings: 1 }],
      };
    });
    openMenu(await card("复制全部"));
    fireEvent.click(menuItem("删除"));
    fireEvent.click(
      within(dialog("删除「复制全部」？")).getByRole("button", {
        name: "删除",
      }),
    );
    await waitFor(() => expect(app.editor.getState().canUndo).toBe(false));
    expect(app.editor.getState().draft?.layers.base.south).toBeUndefined();
  });

  it("works with no profile open on the mapping page", async () => {
    const app = setup({ snapshot: { ...status, profileId: "" } });
    openMenu(await card("复制全部"));
    fireEvent.click(menuItem("删除"));
    fireEvent.click(
      within(dialog("删除「复制全部」？")).getByRole("button", {
        name: "删除",
      }),
    );
    await waitFor(() =>
      expect(app.api.deleteMacro).toHaveBeenCalledWith("复制全部.ts"),
    );
    expect(screen.queryByTestId("script-outcome")).toBeNull();
  });

  it("works in English", async () => {
    setup({ language: "en", list: { macros: [brush], missing: [] } });
    expect((await card("下一个笔刷")).textContent).toContain("Not used");
    expect(button("New")).toBeTruthy();
    expect(button("Import")).toBeTruthy();
  });

  it("explains what replacing an existing script does, in English", async () => {
    const app = setup({
      language: "en",
      list: { macros: [copyAll], missing: [] },
    });
    await card("复制全部");
    app.api.importMacro.mockResolvedValueOnce({
      exists: true,
      name: "复制全部.ts",
      token: "b".repeat(32),
    });
    fireEvent.click(button("Import"));
    const question = await screen.findByRole("dialog", {
      name: "“复制全部” exists. Replace it?",
    });
    expect(question.textContent).toContain(
      "Replacing makes every key that uses this script run the imported contents.",
    );
  });
});
