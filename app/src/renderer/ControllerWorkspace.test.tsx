// @vitest-environment jsdom
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { DeviceInfo, PadmateAPI, ProfileDiagnostic } from "../shared/api";
import type { Profile } from "../shared/generated/schema";
import {
  ControllerWorkspace,
  describeDiagnostic,
  diagnosticControl,
  diagnosticProfileSetting,
  diagnosticView,
} from "./ControllerWorkspace";
import { controllerLabels } from "./controller-model";
import { editorAPIFixture } from "./editor-api-fixture";
import { createEditorStore, type EditorStore } from "./editor-store";
import { translator } from "./i18n";
import { FADE_MS, KEY_PREVIEW_MS } from "./KeyPreview";
import {
  createPadmateStore,
  type LabelDisplay,
  type PadmateStore,
} from "./store";
import { PAD_QUIET_MS } from "./undo-shortcuts";

// Every menuRequest the drawer was rendered with, oldest first.
const menuRequests: number[] = [];

// The drawer has its own tests; here it only needs to open and close.
vi.mock("./KeyDrawer", async () => {
  const { createElement } = await import("react");
  return {
    KeyDrawer: ({
      control,
      onClose,
      menuRequest = 0,
      onShowScripts,
    }: {
      control: string;
      onClose: () => void;
      menuRequest?: number;
      onShowScripts?: () => void;
    }) => {
      menuRequests.push(menuRequest);
      return createElement(
        "aside",
        { "aria-label": "按键编辑" },
        createElement("span", null, control),
        createElement(
          "button",
          { type: "button", onClick: onClose },
          "关闭按键编辑",
        ),
        onShowScripts &&
          createElement(
            "button",
            { type: "button", onClick: onShowScripts },
            "前往脚本页",
          ),
      );
    },
  };
});

afterEach(() => {
  cleanup();
  menuRequests.length = 0;
});

const info: DeviceInfo = {
  id: 1,
  name: "Xbox",
  type: "xboxone",
  vendor: 0,
  product: 0,
  version: 0,
  guid: "",
  path: "",
  driver: "",
  serial: "",
  gamepad: true,
  virtual: true,
};

const paint: Profile = {
  schemaVersion: 1,
  name: "绘画",
  layers: {
    base: {
      south: {
        label: "撤销",
        press: { type: "keys", keys: "Ctrl+Z", mode: "tap" },
      },
      east: { press: { type: "keys", keys: "Space", mode: "hold" } },
    },
  },
};

async function editorFor(profile: Profile) {
  return (await editorWithAPI(profile)).editor;
}

async function editorWithAPI(profile: Profile) {
  const api = editorAPIFixture();
  api.getProfile = vi.fn(async (id: string) => ({
    id,
    text: JSON.stringify(profile),
    revision: "r1",
    diagnostics: [],
  }));
  const editor = createEditorStore(api as unknown as PadmateAPI);
  await editor
    .getState()
    .activate({ id: "paint", device: "d", connection: "c" });
  return { api, editor };
}

function Harness({
  editor,
  display = "actions",
  onSelect = () => {},
  onShowScripts,
  shell,
}: {
  editor: EditorStore;
  display?: LabelDisplay;
  onSelect?: (control: string | undefined) => void;
  onShowScripts?: () => void;
  /** The shell store, for its pad input. */
  shell?: PadmateStore;
}) {
  const [selected, setSelected] = useState<string>();
  const [store] = useState(
    () =>
      shell ?? createPadmateStore(editorAPIFixture() as unknown as PadmateAPI),
  );
  return (
    <ControllerWorkspace
      store={store}
      info={info}
      t={translator("zh-CN")}
      editor={editor}
      language="zh-CN"
      labelDisplay={display}
      selected={selected}
      onSelect={(control) => {
        onSelect(control);
        setSelected(control);
      }}
      onShowScripts={onShowScripts}
    />
  );
}

it("labels chips with the action and its keys in each display mode", async () => {
  const editor = await editorFor(paint);
  const view = render(<Harness editor={editor} />);
  const undo = screen.getByRole("button", { name: "A · 撤销" });
  expect(
    [...undo.querySelectorAll("kbd")].map((key) => key.textContent),
  ).toEqual(["Ctrl", "Z"]);
  const hand = screen.getByRole("button", { name: "B · Space" });
  expect(hand.textContent).toContain("按住");
  expect(
    screen
      .getByRole("button", { name: "Y · 未设置" })
      .getAttribute("data-state"),
  ).toBe("unbound");
  view.rerender(<Harness editor={editor} display="keys" />);
  expect(screen.getByRole("button", { name: "A · Ctrl+Z" })).toBeDefined();
});

it("opens the drawer from a chip and returns focus to it on close", async () => {
  const editor = await editorFor(paint);
  const select = vi.fn();
  const { container } = render(<Harness editor={editor} onSelect={select} />);
  expect(container.querySelector(".controller-hint")).not.toBeNull();
  const undo = screen.getByRole("button", { name: "A · 撤销" });
  fireEvent.click(undo);
  expect(select).toHaveBeenLastCalledWith("south");
  expect(undo.getAttribute("aria-pressed")).toBe("true");
  const drawer = screen.getByRole("complementary", { name: "按键编辑" });
  expect(within(drawer).getByText("south")).toBeDefined();
  expect(container.querySelector(".controller-hint")).toBeNull();
  // Another chip switches the drawer to that control.
  const left = screen.getByRole("button", { name: "← · 未设置" });
  fireEvent.click(left);
  expect(select).toHaveBeenLastCalledWith("dpad.left");
  expect(within(drawer).getByText("dpad.left")).toBeDefined();
  expect(
    container.querySelectorAll('button[data-callout][aria-pressed="true"]'),
  ).toHaveLength(1);
  fireEvent.click(within(drawer).getByRole("button", { name: "关闭按键编辑" }));
  expect(select).toHaveBeenLastCalledWith(undefined);
  expect(screen.queryByRole("complementary")).toBeNull();
  expect(document.activeElement).toBe(left);
  expect(container.querySelector(".controller-hint")).not.toBeNull();
});

it("shows edits on the chips at once, with no unsaved marks", async () => {
  const { api, editor } = await editorWithAPI(paint);
  const { container } = render(<Harness editor={editor} />);
  const chip = (control: string) =>
    container.querySelector(`button[data-callout="${control}"]`);
  const draft = structuredClone(paint);
  draft.layers.base.south = {
    label: "重做",
    press: { type: "keys", keys: "Ctrl+Y", mode: "tap" },
  };
  await act(async () => {
    editor.getState().edit(draft);
    await editor.getState().flush();
  });
  expect(chip("south")?.getAttribute("aria-label")).toBe("A · 重做");
  expect(chip("south")?.hasAttribute("data-dirty")).toBe(false);
  expect(api.putProfile).toHaveBeenCalledOnce();
  // Nothing to say: the hint stays.
  expect(container.querySelector(".controller-hint")).not.toBeNull();
  expect(screen.queryByTestId("editor-notice")).toBeNull();
});

it("shows a shortcut cleared in the drawer as unset at once (H1)", async () => {
  const { api, editor } = await editorWithAPI(paint);
  const { container } = render(<Harness editor={editor} />);
  const chip = (control: string) =>
    container.querySelector<HTMLElement>(`button[data-callout="${control}"]`);
  // The drawer's 清除快捷键 leaves an empty shortcut in the draft.
  const cleared = structuredClone(paint);
  cleared.layers.base.east = { press: { type: "keys", keys: "" } };
  cleared.layers.base.south = {
    label: "撤销",
    press: { type: "keys", keys: "" },
  };
  await act(async () => {
    editor.getState().edit(cleared);
    await editor.getState().flush();
  });
  // The draft keeps the empty part for the drawer's card ...
  expect(editor.getState().draft?.layers.base.east?.press).toEqual({
    type: "keys",
    keys: "",
  });
  // ... but the chips show what is saved: no shortcut, and no held keys.
  expect(chip("east")?.getAttribute("aria-label")).toBe("B · 未设置");
  expect(chip("east")?.getAttribute("data-state")).toBe("unbound");
  expect(chip("east")?.textContent).not.toContain("按住");
  expect(chip("south")?.getAttribute("aria-label")).toBe("A · 撤销");
  expect(chip("south")?.getAttribute("data-state")).toBe("unbound");
  expect(chip("south")?.textContent).toContain("未设置");
  expect(chip("south")?.textContent).not.toContain("按住");
  expect(chip("south")?.querySelectorAll("kbd")).toHaveLength(0);
  // What went to the file has neither part.
  const saved = JSON.parse(
    vi.mocked(api.putProfile).mock.calls.at(-1)?.[1] as string,
  ) as Profile;
  expect(saved.layers.base.east).toBeUndefined();
  expect(saved.layers.base.south).toEqual({ label: "撤销" });
});

describe("undo and redo keys", () => {
  // The south key's name: undo takes it back (the profile name it keeps).
  const renamed = (name: string): Profile => ({
    ...paint,
    controlNames: { south: name },
  });

  async function edited() {
    const editor = await editorFor(paint);
    const shell = createPadmateStore(
      editorAPIFixture() as unknown as PadmateAPI,
    );
    render(<Harness editor={editor} shell={shell} />);
    act(() => {
      editor.getState().edit(renamed("a"));
      editor.getState().edit(renamed("b"));
    });
    return { editor, shell };
  }
  const press = (key: string, options: Partial<KeyboardEventInit> = {}) =>
    act(() => {
      fireEvent.keyDown(document.body, { key, ctrlKey: true, ...options });
    });
  const name = (editor: EditorStore) =>
    editor.getState().draft?.controlNames?.south ??
    editor.getState().draft?.name;

  it("undoes with Ctrl+Z and redoes with Ctrl+Y or Ctrl+Shift+Z", async () => {
    const { editor } = await edited();
    press("z");
    expect(name(editor)).toBe("a");
    press("z");
    expect(name(editor)).toBe("绘画");
    press("y");
    expect(name(editor)).toBe("a");
    press("Z", { shiftKey: true });
    expect(name(editor)).toBe("b");
    // Without Ctrl, or with Alt, the keys are not shortcuts.
    press("z", { ctrlKey: false });
    press("z", { altKey: true });
    expect(name(editor)).toBe("b");
  });

  it("leaves the keys to text fields, dialogs and shortcut capture", async () => {
    const { editor } = await edited();
    const field = document.createElement("input");
    document.body.append(field);
    fireEvent.keyDown(field, { key: "z", ctrlKey: true });
    expect(name(editor)).toBe("b");
    field.remove();
    const dialog = document.createElement("dialog");
    dialog.setAttribute("open", "");
    document.body.append(dialog);
    press("z");
    expect(name(editor)).toBe("b");
    dialog.remove();
    const capture = document.createElement("div");
    capture.className = "drawer-shortcut";
    capture.dataset.active = "true";
    document.body.append(capture);
    press("z");
    expect(name(editor)).toBe("b");
    capture.remove();
    press("z");
    expect(name(editor)).toBe("a");
  });

  it("does not undo while a pad may send keys to padmate's window", async () => {
    const { editor, shell } = await edited();
    const sendHere = (on: boolean, devices: unknown[]) =>
      act(() =>
        shell.setState({
          status: { sendToOwnWindow: on, devices } as never,
        }),
      );
    // The pad's Ctrl+Z arrives before its input event does.
    sendHere(true, [{ info: { id: 1 } }]);
    press("z");
    press("y");
    expect(name(editor)).toBe("b");
    // Without a pad, or with the setting off, the keys are the keyboard's.
    sendHere(true, []);
    press("z");
    expect(name(editor)).toBe("a");
    sendHere(false, [{ info: { id: 1 } }]);
    press("z");
    expect(name(editor)).toBe("绘画");
  });

  it("does not undo right after a pad press", async () => {
    vi.useFakeTimers({ toFake: ["performance"] });
    try {
      const { editor, shell } = await edited();
      // The button sends Ctrl+Z to padmate's own window while held.
      act(() =>
        shell.setState({
          inputs: { 1: { buttons: { south: true }, axes: {} } },
        }),
      );
      press("z");
      expect(name(editor)).toBe("b");
      act(() =>
        shell.setState({
          inputs: { 1: { buttons: { south: false }, axes: {} } },
        }),
      );
      press("z");
      expect(name(editor)).toBe("b");
      vi.advanceTimersByTime(PAD_QUIET_MS);
      press("z");
      expect(name(editor)).toBe("a");
    } finally {
      vi.useRealTimers();
    }
  });
});

const at = (pointer: string): ProfileDiagnostic => ({
  severity: "error",
  pointer,
  offset: 0,
  line: 1,
  col: 1,
  message: "invalid",
});

it("locates diagnostics on the chip whose drawer edits them", () => {
  const cases: [string, string | null][] = [
    ["/layers/base/a/press", "south"],
    ["/layers/base/lt/press", "lefttrigger"],
    ["/layers/base/lt.full/press", "lefttrigger"],
    ["/layers/base/rt.full/tap", "righttrigger"],
    ["/layers/base/rs.cw/press", "rightstick"],
    ["/layers/base/ls.outer/press", "leftstick"],
    ["/layers/base/ls.up/hold", "leftstick"],
    ["/layers/base/rs.active", "rightstick"],
    // Raw axes and half presses belong to the stick and trigger drawers.
    ["/layers/base/leftx+/tap", "leftstick"],
    ["/layers/base/lefty-/press", "leftstick"],
    ["/layers/base/rightx-/press", "rightstick"],
    ["/layers/base/lefttrigger+", "lefttrigger"],
    ["/layers/base/righttrigger-/press", "righttrigger"],
    ["/layers/base/dpad.upleft/press", "dpad.up"],
    ["/controlNames/lt", "lefttrigger"],
    ["/controlNames/south", "south"],
    ["/analog/rs/deadzone", "rightstick"],
    ["/analog/dpad/mode", "dpad.up"],
    ["/analog/axisThreshold", "leftstick"],
    // Not a control, or not shown by the editor.
    ["/layers/base/color", null],
    ["/layers/base", null],
    ["/layers/tools/south/press", null],
    ["/common/south", null],
    ["/controlNames", null],
    ["/analog/constructor", null],
    ["/name", null],
    ["/timing/holdMs", null],
    ["/feedback/pause", null],
    ["/feedback/lowBattery", null],
  ];
  for (const [pointer, control] of cases)
    expect([pointer, diagnosticControl(at(pointer))]).toEqual([
      pointer,
      control,
    ]);
  expect(diagnosticProfileSetting(at("/name"))).toBe(true);
  expect(diagnosticProfileSetting(at("/timing"))).toBe(true);
  expect(diagnosticProfileSetting(at("/timing/holdMs"))).toBe(true);
  // No member of feedback is in the dialog: its switches are global now
  // (ADR-0067), and the other members are not shown.
  for (const pointer of [
    "/feedback",
    "/feedback/rumble",
    "/feedback/led",
    "/feedback/pause",
    "/feedback/lowBattery",
    "/feedback/strength",
    "/feedback/shortMs",
  ])
    expect(diagnosticProfileSetting(at(pointer))).toBe(false);
  expect(diagnosticProfileSetting(at("/names"))).toBe(false);
  expect(diagnosticProfileSetting(at("/layers/base/south/press"))).toBe(false);
});

it("describes where each diagnostic is in the words of the editor", () => {
  const labels = controllerLabels("xboxone");
  const profile: Profile = {
    ...paint,
    controlNames: { south: "撤销键", east: "  " },
  };
  const zh = (pointer: string) =>
    describeDiagnostic(at(pointer), profile, labels, "zh-CN");
  expect(zh("/name")).toBe("配置设置 · 名称");
  expect(zh("/timing/holdMs")).toBe("配置设置 · 触发长按时间");
  expect(zh("/timing/tapGapMs")).toBe("配置设置 · 点按间隔");
  expect(zh("/timing")).toBe("配置设置 · 按键判定");
  expect(zh("/feedback/rumble")).toBe("界面中未显示的部分");
  expect(zh("/feedback/lowBattery")).toBe("界面中未显示的部分");
  expect(zh("/feedback/strength")).toBe("界面中未显示的部分");
  expect(zh("/analog/axisThreshold")).toBe("原始轴阈值");
  expect(zh("/analog/dpad/mode")).toBe("十字键设置");
  expect(zh("/analog/ls/deadzone")).toBe("左摇杆 · 摇杆设置");
  expect(zh("/analog/rt/press")).toBe("RT 键 · 扳机设置");
  expect(zh("/layers/base/south/press")).toBe("撤销键 · 点按");
  // A blank key name falls back to the control.
  expect(zh("/controlNames/east")).toBe("B 键 · 按键名称");
  expect(zh("/layers/base/east/label")).toBe("B 键 · 显示文字");
  expect(zh("/layers/base/south/haptic")).toBe("撤销键 · 触感");
  expect(zh("/layers/base/ls.up/hold")).toBe("左摇杆 · ↑ 长按");
  expect(zh("/layers/base/ls.active")).toBe("左摇杆 · 任意方向");
  expect(zh("/layers/base/leftx+/press")).toBe("左摇杆 · 原始轴向右 点按");
  expect(zh("/layers/base/lefttrigger+")).toBe("LT 键 · 半按");
  expect(zh("/layers/base/lt/tap")).toBe("LT 键 · 点按");
  expect(zh("/layers/base/dpad.upleft/tap")).toBe("十字键 ↑ · ↖ 点按");
  expect(zh("/layers/base/color")).toBe("界面中未显示的部分");
  expect(zh("/layers/tools/south/press")).toBe("界面中未显示的部分");
  const en = (pointer: string) =>
    describeDiagnostic(at(pointer), null, labels, "en");
  expect(en("/name")).toBe("Profile settings · Name");
  expect(en("/timing/multiTapMs")).toBe("Profile settings · Multi-tap window");
  expect(en("/feedback/pause")).toBe(
    "In a part of the file the editor does not show",
  );
  expect(en("/layers/base/righty+/hold")).toBe("Right stick · Raw down Hold");
  expect(en("/controlNames/south")).toBe("A key · Key name");
  expect(en("/layers/base/south/haptic")).toBe("A key · Haptic");
  expect(en("/common/south")).toBe(
    "In a part of the file the editor does not show",
  );
});

it("opens the drawer that edits a diagnostic, but not for profile settings", async () => {
  const editor = await editorFor(paint);
  const select = vi.fn();
  render(<Harness editor={editor} onSelect={select} />);
  act(() =>
    editor.setState({
      diagnostics: [
        at("/name"),
        at("/layers/base/lefttrigger+/press"),
        at("/controlNames/south"),
      ],
    }),
  );
  // The problem list opens from its toggle.
  const problem = (where: string) => {
    if (!screen.queryAllByTestId("profile-diagnostic").length)
      fireEvent.click(
        screen.getByRole("button", { name: "3 个问题", expanded: false }),
      );
    const found = screen
      .getAllByTestId("profile-diagnostic")
      .find((button) =>
        button.querySelector("strong")?.textContent?.endsWith(where),
      );
    if (!found) throw new Error(`Missing diagnostic ${where}`);
    return found;
  };
  fireEvent.click(problem("配置设置 · 名称"));
  expect(select).not.toHaveBeenCalled();
  expect(screen.queryByRole("complementary")).toBeNull();
  fireEvent.click(problem("LT 键 · 半按 点按"));
  expect(select).toHaveBeenLastCalledWith("lefttrigger");
  expect(
    within(screen.getByRole("complementary")).getByText("lefttrigger"),
  ).toBeDefined();
  fireEvent.click(problem("A 键 · 按键名称"));
  expect(select).toHaveBeenLastCalledWith("south");
});

it("opens the drawer's menu once for a right-clicked chip", async () => {
  const editor = await editorFor(paint);
  const select = vi.fn();
  const { container } = render(<Harness editor={editor} onSelect={select} />);
  const chip = (control: string) => {
    const button = container.querySelector(`button[data-callout="${control}"]`);
    if (!button) throw new Error(`Missing chip ${control}`);
    return button;
  };
  expect(fireEvent.contextMenu(chip("north"))).toBe(false);
  expect(select).toHaveBeenLastCalledWith("north");
  expect(chip("north").getAttribute("aria-pressed")).toBe("true");
  // The drawer opened with a request, which then dropped back to 0.
  expect(menuRequests).toContain(1);
  expect(menuRequests.at(-1)).toBe(0);
  // A left click on another chip does not open the menu again.
  menuRequests.length = 0;
  fireEvent.click(chip("leftstick"));
  expect(select).toHaveBeenLastCalledWith("leftstick");
  expect(menuRequests.length).toBeGreaterThan(0);
  expect(menuRequests.every((request) => request === 0)).toBe(true);
  // Each right-click is a new request, also on the open drawer's own chip.
  for (const expected of [2, 3]) {
    menuRequests.length = 0;
    fireEvent.contextMenu(chip("leftstick"));
    expect(menuRequests).toContain(expected);
    expect(menuRequests.at(-1)).toBe(0);
  }
  expect(
    container.querySelectorAll('button[data-callout][aria-pressed="true"]'),
  ).toHaveLength(1);
});

it("passes the way to the scripts page through to the drawer", async () => {
  const editor = await editorFor(paint);
  const scripts = vi.fn();
  render(<Harness editor={editor} onShowScripts={scripts} />);
  fireEvent.click(screen.getByRole("button", { name: "A · 撤销" }));
  fireEvent.click(screen.getByRole("button", { name: "前往脚本页" }));
  expect(scripts).toHaveBeenCalledTimes(1);
});

it("reads the script library again when the drawer opens and on focus", async () => {
  const api = editorAPIFixture();
  api.getProfile = vi.fn(async (id: string) => ({
    id,
    text: JSON.stringify(paint),
    revision: "r1",
    diagnostics: [],
  }));
  const editor = createEditorStore(api as unknown as PadmateAPI);
  await editor
    .getState()
    .activate({ id: "paint", device: "d", connection: "c" });
  render(<Harness editor={editor} />);
  // Loading the profile read the list once.
  await act(async () => {});
  api.listMacros.mockClear();
  fireEvent.click(screen.getByRole("button", { name: "A · 撤销" }));
  await act(async () => {});
  expect(api.listMacros).toHaveBeenCalledTimes(1);
  expect(api.listMacros).toHaveBeenCalledWith();
  // Another control keeps the drawer open: no second read.
  fireEvent.click(screen.getByRole("button", { name: "← · 未设置" }));
  await act(async () => {});
  expect(api.listMacros).toHaveBeenCalledTimes(1);
  // A script made in the editor or by another tool shows after focus.
  api.listMacros.mockResolvedValue({
    macros: [{ file: "复制全部.ts", name: "复制全部", usedBy: [] }],
    missing: [],
  });
  await act(async () => {
    window.dispatchEvent(new Event("focus"));
  });
  expect(api.listMacros).toHaveBeenCalledTimes(2);
  expect(editor.getState().macros?.macros.map((macro) => macro.file)).toEqual([
    "复制全部.ts",
  ]);
});

it("hides the hint while the notice shows an editor error", async () => {
  const editor = await editorFor(paint);
  const { container } = render(<Harness editor={editor} />);
  expect(container.querySelector(".controller-hint")).not.toBeNull();
  act(() => editor.setState({ error: "write denied" }));
  expect(container.querySelector(".controller-hint")).toBeNull();
  // A failed load leaves no draft, but the notice still shows the error.
  act(() => editor.setState({ draft: null }));
  expect(container.querySelector(".controller-hint")).toBeNull();
  act(() => editor.setState({ error: "" }));
  expect(container.querySelector(".controller-hint")).not.toBeNull();
});

it("shows what a press sends where the hint was (UI-44)", async () => {
  const editor = await editorFor(paint);
  const shell = createPadmateStore(editorAPIFixture() as unknown as PadmateAPI);
  const focus = vi.spyOn(document, "hasFocus").mockReturnValue(true);
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  try {
    const { container } = render(<Harness editor={editor} shell={shell} />);
    const line = screen.getByTestId("key-preview");
    expect(line.textContent).toBe("");
    act(() =>
      shell.setState({
        keyPreview: {
          seq: 1,
          device: info.id,
          control: "south",
          action: "tap Ctrl+Z",
        },
      }),
    );
    expect(line.textContent).toBe("A → Ctrl+Z");
    expect(container.querySelector(".controller-hint")).toBeNull();
    // Above the notice, in the space the hint leaves.
    act(() => editor.setState({ error: "write denied" }));
    const bottom = container.querySelector(".workspace-bottom");
    expect(bottom?.firstElementChild).toBe(line);
    expect(bottom?.contains(screen.getByTestId("editor-notice"))).toBe(true);
    act(() => editor.setState({ error: "" }));
    act(() => vi.advanceTimersByTime(KEY_PREVIEW_MS));
    // The hint comes back once the line has faded.
    expect(container.querySelector(".controller-hint")).toBeNull();
    act(() => vi.advanceTimersByTime(FADE_MS));
    expect(container.querySelector(".controller-hint")).not.toBeNull();
  } finally {
    vi.useRealTimers();
    focus.mockRestore();
  }
});

// UI-30 (ADR-0075): one of View, Menu and the Xbox button is the function key
// (config.json functionKey, Menu unless it says otherwise); the page edits the
// usual bindings or the function set it switches to.
describe("the function key", () => {
  const withSet: Profile = {
    ...paint,
    functionSet: {
      south: {
        label: "重做",
        press: { type: "keys", keys: "Ctrl+Shift+Z", mode: "tap" },
      },
    },
  };
  const joyCon: DeviceInfo = {
    ...info,
    name: "Joy-Con (L)",
    type: "joyconleft",
    buttonMask: 0xf,
  };
  const chip = (container: HTMLElement, control: string) =>
    container.querySelector<HTMLElement>(`[data-callout="${control}"]`);
  const switchOptions = () =>
    within(screen.getByRole("radiogroup", { name: "编辑的按键" }))
      .getAllByRole("radio")
      .map((radio) => [radio.textContent, radio.getAttribute("aria-checked")]);
  const workspace = (
    editor: EditorStore,
    device: DeviceInfo,
    language: "zh-CN" | "en" = "zh-CN",
    store = createPadmateStore(editorAPIFixture() as unknown as PadmateAPI),
    onSelect: (control: string | undefined) => void = () => {},
  ) => (
    <ControllerWorkspace
      store={store}
      info={device}
      t={translator(language)}
      editor={editor}
      language={language}
      labelDisplay="actions"
      onSelect={onSelect}
    />
  );

  it("is switched to at the top of the page, as 按住功能键", async () => {
    const editor = await editorFor(withSet);
    render(<Harness editor={editor} />);
    expect(switchOptions()).toEqual([
      ["平时", "true"],
      ["按住功能键", "false"],
    ]);
  });

  it("is named the same whichever key the settings choose (ADR-0076)", async () => {
    const editor = await editorFor(withSet);
    act(() => editor.getState().setFunctionKey("back"));
    const { rerender } = render(<Harness editor={editor} />);
    expect(switchOptions()[1][0]).toBe("按住功能键");
    act(() => editor.getState().setFunctionKey("guide"));
    rerender(<Harness editor={editor} />);
    expect(switchOptions()[1][0]).toBe("按住功能键");
  });

  it("has an English name too", async () => {
    const editor = await editorFor(withSet);
    render(workspace(editor, { ...info, type: "ps5" }, "en"));
    expect(
      within(screen.getByRole("radiogroup", { name: "Keys being edited" }))
        .getAllByRole("radio")
        .map((radio) => radio.textContent),
    ).toEqual(["Normal", "Hold function key"]);
    act(() => editor.getState().setFunctionKey("guide"));
    expect(
      within(screen.getByRole("radiogroup", { name: "Keys being edited" }))
        .getAllByRole("radio")
        .map((radio) => radio.textContent),
    ).toEqual(["Normal", "Hold function key"]);
  });

  it("has no switch on a single Joy-Con held sideways when View is the function key, which it lacks", async () => {
    const editor = await editorFor(withSet);
    const { container } = render(workspace(editor, joyCon));
    expect(switchOptions()).toEqual([
      ["平时", "true"],
      ["按住功能键", "false"],
    ]);
    // Nor does it have a chip for it.
    expect(chip(container, "back")).toBeNull();
    expect(chip(container, "start")?.dataset.state).toBe("function");
    // With View as the function key the pad has none: no switch to show.
    act(() => editor.getState().setFunctionKey("back"));
    expect(screen.queryByRole("radiogroup", { name: "编辑的按键" })).toBeNull();
    // Menu is an ordinary key then, with nothing set.
    expect(chip(container, "start")?.dataset.state).toBe("unbound");
  });

  it("shows the set it switches to, and what a control does otherwise where the set leaves it out", async () => {
    const editor = await editorFor(withSet);
    const { container } = render(<Harness editor={editor} />);
    expect(chip(container, "south")?.getAttribute("aria-label")).toBe(
      "A · 撤销",
    );
    fireEvent.click(screen.getByRole("radio", { name: "按住功能键" }));
    expect(
      screen
        .getByRole("radio", { name: "按住功能键" })
        .getAttribute("aria-checked"),
    ).toBe("true");
    // Set in the set.
    expect(chip(container, "south")?.getAttribute("aria-label")).toBe(
      "A · 重做",
    );
    expect(chip(container, "south")?.dataset.state).toBe("bound");
    // Left out of the set: the usual binding, greyed, with the note.
    const east = chip(container, "east") as HTMLElement;
    expect(east.getAttribute("aria-label")).toBe("B · 平时：Space");
    expect(east.dataset.state).toBe("unbound");
    expect(east.dataset.inherited).toBe("true");
    // Left out of both: not set.
    expect(chip(container, "north")?.getAttribute("aria-label")).toBe(
      "Y · 未设置",
    );
    expect(chip(container, "north")?.dataset.inherited).toBeUndefined();
    fireEvent.click(screen.getByRole("radio", { name: "平时" }));
    expect(chip(container, "south")?.getAttribute("aria-label")).toBe(
      "A · 撤销",
    );
    // A view is not an edit.
    expect(editor.getState().unsaved).toBe(false);
    expect(editor.getState().canUndo).toBe(false);
  });

  it("shows the set of a file of the old functionKeys as the one set it stands for", async () => {
    const editor = await editorFor({
      ...paint,
      functionKeys: {
        back: {
          south: { press: { type: "keys", keys: "Ctrl+Y", mode: "tap" } },
          north: { press: { type: "keys", keys: "F13", mode: "tap" } },
        },
        start: {
          south: {
            label: "重做",
            press: { type: "keys", keys: "Ctrl+Shift+Z", mode: "tap" },
          },
        },
      },
    });
    const { container } = render(<Harness editor={editor} />);
    fireEvent.click(screen.getByRole("radio", { name: "按住功能键" }));
    // Menu's set wins; View's set fills in what Menu's leaves out.
    expect(chip(container, "south")?.getAttribute("aria-label")).toBe(
      "A · 重做",
    );
    expect(chip(container, "north")?.getAttribute("aria-label")).toBe(
      "Y · F13",
    );
    // Only opening the file is no edit.
    expect(editor.getState().unsaved).toBe(false);
  });

  it("is tagged on the pad in every view, and cannot be opened", async () => {
    const editor = await editorFor({
      ...withSet,
      layers: {
        base: {
          ...withSet.layers.base,
          // What a file binds on it is ignored by the daemon.
          start: { press: { type: "keys", keys: "Ctrl+S", mode: "tap" } },
        },
      },
    });
    const select = vi.fn();
    const { container } = render(<Harness editor={editor} onSelect={select} />);
    for (const view of ["平时", "按住功能键"]) {
      fireEvent.click(screen.getByRole("radio", { name: view }));
      const tag = chip(container, "start") as HTMLElement;
      expect(tag.tagName, view).toBe("DIV");
      expect(tag.getAttribute("aria-label"), view).toBe("Menu · 功能键");
      expect(tag.title, view).toBe("Menu · 功能键\n按住时其他键换成另一套");
      fireEvent.click(tag);
      fireEvent.contextMenu(tag);
    }
    expect(select).not.toHaveBeenCalled();
    expect(screen.queryByRole("complementary")).toBeNull();
  });

  it("leaves the other two of View, Menu and the Xbox button as keys to open and set", async () => {
    const editor = await editorFor({
      ...withSet,
      layers: {
        base: {
          ...withSet.layers.base,
          back: {
            label: "粘贴",
            press: { type: "keys", keys: "Ctrl+V", mode: "tap" },
          },
        },
      },
    });
    const select = vi.fn();
    const { container } = render(<Harness editor={editor} onSelect={select} />);
    const back = chip(container, "back") as HTMLElement;
    expect(back.tagName).toBe("BUTTON");
    expect(back.getAttribute("aria-label")).toBe("View · 粘贴");
    // The Xbox button has nothing yet, and can be given something.
    const guide = chip(container, "guide") as HTMLElement;
    expect(guide.tagName).toBe("BUTTON");
    expect(guide.dataset.state).toBe("unbound");
    fireEvent.click(back);
    expect(select).toHaveBeenLastCalledWith("back");
    fireEvent.click(guide);
    expect(select).toHaveBeenLastCalledWith("guide");
    // Whichever key is the function key is the one that is only a tag.
    act(() => editor.getState().setFunctionKey("guide"));
    expect(chip(container, "guide")?.tagName).toBe("DIV");
    expect(chip(container, "start")?.tagName).toBe("BUTTON");
    expect(chip(container, "start")?.dataset.state).toBe("unbound");
  });

  it("goes back to the usual keys when the profile changes", async () => {
    const editor = await editorFor(withSet);
    render(<Harness editor={editor} />);
    fireEvent.click(screen.getByRole("radio", { name: "按住功能键" }));
    expect(switchOptions()[1]).toEqual(["按住功能键", "true"]);
    await act(async () => {
      await editor
        .getState()
        .activate({ id: "other", device: "d", connection: "c" });
    });
    expect(switchOptions()[0]).toEqual(["平时", "true"]);
    expect(editor.getState().view).toBe("normal");
  });

  it("shows the usual keys when the pad has not the function key that was chosen", async () => {
    const editor = await editorFor(withSet);
    act(() => editor.getState().setFunctionKey("back"));
    const store = createPadmateStore(
      editorAPIFixture() as unknown as PadmateAPI,
    );
    const { container, rerender } = render(
      workspace(editor, info, "zh-CN", store),
    );
    fireEvent.click(screen.getByRole("radio", { name: "按住功能键" }));
    expect(chip(container, "south")?.getAttribute("aria-label")).toBe(
      "A · 重做",
    );
    rerender(workspace(editor, joyCon, "zh-CN", store));
    expect(screen.queryByRole("radiogroup", { name: "编辑的按键" })).toBeNull();
    expect(chip(container, "south")?.getAttribute("aria-label")).toBe(
      "S · 撤销",
    );
  });

  it("shows the set a problem is in, and the usual keys for a problem there", async () => {
    const editor = await editorFor(withSet);
    const select = vi.fn();
    const { container } = render(<Harness editor={editor} onSelect={select} />);
    const problem = (
      pointer: string,
      severity: "error" | "warning",
    ): ProfileDiagnostic => ({ ...at(pointer), severity, message: "problem" });
    act(() =>
      editor.setState({
        diagnostics: [
          problem("/functionSet/south/press", "error"),
          problem("/layers/base/east/press", "error"),
          problem("/functionKeys/start/south", "warning"),
          problem("/layers/base/start", "warning"),
        ],
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: "4 个问题" }));
    const items = screen.getAllByTestId("profile-diagnostic");
    expect(items.map((item) => item.textContent)).toEqual([
      expect.stringContaining("按住功能键 · A 键 · 点按"),
      expect.stringContaining("B 键 · 点按"),
      // A file of the old format points into functionKeys: the same set.
      expect.stringContaining("按住功能键 · A 键"),
      expect.stringContaining("Menu 键"),
    ]);
    // A problem in the set opens that set, with the key.
    fireEvent.click(items[0]);
    expect(editor.getState().view).toBe("function");
    expect(select).toHaveBeenLastCalledWith("south");
    expect(chip(container, "south")?.getAttribute("aria-pressed")).toBe("true");
    // A problem in the usual keys goes back to them.
    fireEvent.click(screen.getByRole("button", { name: "4 个问题" }));
    fireEvent.click(screen.getAllByTestId("profile-diagnostic")[1]);
    expect(editor.getState().view).toBe("normal");
    expect(select).toHaveBeenLastCalledWith("east");
    // A problem about a binding on the function key has nothing to open.
    select.mockClear();
    fireEvent.click(screen.getByRole("button", { name: "4 个问题" }));
    fireEvent.click(screen.getAllByTestId("profile-diagnostic")[3]);
    expect(select).not.toHaveBeenCalled();
  });
});

it("tells where a problem in the function set is", () => {
  expect(diagnosticControl(at("/functionSet/a/press"))).toBe("south");
  expect(diagnosticControl(at("/functionSet/lt.full/tap"))).toBe("lefttrigger");
  expect(diagnosticControl(at("/functionSet"))).toBeNull();
  // The old format points into one of its two sets, which is the set here.
  expect(diagnosticControl(at("/functionKeys/back/a/press"))).toBe("south");
  expect(diagnosticControl(at("/functionKeys/start/lt.full/tap"))).toBe(
    "lefttrigger",
  );
  expect(diagnosticControl(at("/functionKeys/start"))).toBeNull();
  expect(diagnosticControl(at("/functionKeys"))).toBeNull();
  expect(diagnosticView(at("/functionSet/a/press"))).toBe("function");
  expect(diagnosticView(at("/functionKeys/back/a/press"))).toBe("function");
  expect(diagnosticView(at("/functionKeys/start/a"))).toBe("function");
  expect(diagnosticView(at("/layers/base/a/press"))).toBe("normal");
  expect(diagnosticView(at("/layers/alt/a/press"))).toBeNull();
  expect(diagnosticView(at("/controlNames/south"))).toBeNull();
  expect(diagnosticView(at("/analog/ls/mode"))).toBeNull();
  const labels = controllerLabels("xboxone");
  expect(
    describeDiagnostic(at("/functionSet/lt.full/tap"), null, labels, "en"),
  ).toBe("Hold function key · LT key · full pull Tap");
  // The old format reads the same, and says "function key" whichever it is.
  expect(
    describeDiagnostic(
      at("/functionKeys/start/lt.full/tap"),
      null,
      labels,
      "en",
    ),
  ).toBe("Hold function key · LT key · full pull Tap");
  expect(
    describeDiagnostic(at("/functionSet/a/press"), null, labels, "zh-CN"),
  ).toBe("按住功能键 · A 键 · 点按");
});
