// @vitest-environment jsdom
// The drawer while the function set is shown (UI-30, ADR-0075): it reads and
// writes the set, a control the set leaves out starts empty, and the menu
// clears a control from the set.
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type {
  DeviceInfo,
  PadmateAPI,
  ProfileDiagnostic,
  ScriptRegistration,
} from "../shared/api";
import type { Profile } from "../shared/generated/schema";
import { fakeCapture, shortcutValue } from "./capture-fixture";
import { editorAPIFixture } from "./editor-api-fixture";
import type { FunctionKey, KeyView } from "./editor-model";
import { createEditorStore, type EditorStore } from "./editor-store";
import { KeyDrawer } from "./KeyDrawer";
import type { Language } from "./store";

afterEach(cleanup);

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

const keys = (value: string) =>
  ({ type: "keys", keys: value, mode: "tap" }) as const;

const paint: Profile = {
  schemaVersion: 1,
  name: "Paint",
  layers: {
    base: {
      north: { press: keys("F13") },
      south: { label: "Undo", press: keys("Ctrl+Z") },
      leftstick: { label: "Pan" },
    },
  },
  functionSet: {
    north: { label: "Redo", press: keys("F14") },
    "ls.up": { press: keys("W") },
  },
};

async function setup(
  control: string,
  view: KeyView,
  language: Language = "zh-CN",
  profile: Profile = paint,
  deviceInfo: DeviceInfo = info,
  functionKey: FunctionKey = "start",
) {
  const api = editorAPIFixture();
  api.getProfile = vi.fn(async (id: string) => ({
    id,
    text: JSON.stringify(profile),
    revision: "r1",
    diagnostics: [],
  }));
  const { record } = fakeCapture(api);
  const editor = createEditorStore(api as unknown as PadmateAPI);
  await editor
    .getState()
    .activate({ id: "paint", device: "d", connection: "c" });
  await act(() => editor.getState().refreshMetadata());
  act(() => editor.getState().setFunctionKey(functionKey));
  act(() => editor.getState().setView(view));
  const draw = (next: string) => (
    <KeyDrawer
      editor={editor}
      control={next}
      onControl={() => {}}
      info={deviceInfo}
      language={language}
      onClose={() => {}}
    />
  );
  const view$ = render(draw(control));
  return {
    api,
    editor,
    record,
    rerender: (next: string) => view$.rerender(draw(next)),
  };
}

const draft = (editor: EditorStore) => editor.getState().draft as Profile;

it("reads the set, not the usual binding, of a control the set binds", async () => {
  await setup("north", "function");
  expect(shortcutValue()).toBe("F14");
  expect(
    (
      screen.getByRole("textbox", {
        name: "功能",
      }) as HTMLInputElement
    ).value,
  ).toBe("Redo");
  // Nothing to say about the usual binding: the set has its own.
  expect(screen.queryByTestId("drawer-inherited")).toBeNull();
});

it("names the set in the header as the function key, in both languages", async () => {
  await setup("north", "function");
  expect(screen.getByTestId("drawer-view").textContent).toBe("按住功能键");
  cleanup();
  await setup("north", "function", "en");
  expect(screen.getByTestId("drawer-view").textContent).toBe(
    "Hold function key",
  );
  // Whichever key it is, it is called the function key (ADR-0076).
  cleanup();
  await setup("north", "function", "zh-CN", paint, info, "back");
  expect(screen.getByTestId("drawer-view").textContent).toBe("按住功能键");
  cleanup();
  await setup("north", "function", "en", paint, info, "guide");
  expect(screen.getByTestId("drawer-view").textContent).toBe(
    "Hold function key",
  );
  cleanup();
  await setup("north", "normal");
  expect(screen.queryByTestId("drawer-view")).toBeNull();
});

it("writes a change to the set and leaves the usual binding alone", async () => {
  const { editor, record } = await setup("north", "function");
  await record("F20");
  expect(draft(editor).functionSet?.north).toMatchObject({
    label: "Redo",
    press: { type: "keys", keys: "F20" },
  });
  expect(draft(editor).layers.base.north).toEqual({ press: keys("F13") });
  expect(draft(editor).functionSet?.["ls.up"]).toEqual(
    paint.functionSet?.["ls.up"],
  );
});

it("starts an empty binding for a control the set leaves out, and shows what it does otherwise", async () => {
  const { editor, record } = await setup("south", "function");
  expect(shortcutValue()).toBe("");
  const note = screen.getByTestId("drawer-inherited");
  expect(note.textContent).toBe("平时：Ctrl+Z");
  // Opening it changes nothing in the file.
  expect(editor.getState().unsaved).toBe(false);
  expect(draft(editor).functionSet?.south).toBeUndefined();
  await record("Ctrl+Y");
  expect(draft(editor).functionSet?.south).toEqual({
    press: { type: "keys", keys: "Ctrl+Y" },
  });
  expect(draft(editor).layers.base.south).toEqual({
    label: "Undo",
    press: keys("Ctrl+Z"),
  });
  // Set now: the note goes.
  expect(screen.queryByTestId("drawer-inherited")).toBeNull();
});

it("says nothing about the usual binding of a control that has none", async () => {
  await setup("east", "function");
  expect(screen.queryByTestId("drawer-inherited")).toBeNull();
});

it("words the note in English too", async () => {
  await setup("south", "function", "en");
  expect(screen.getByTestId("drawer-inherited").textContent).toBe(
    "Normal: Ctrl+Z",
  );
});

it("does not say a shared setting applies where the usual keys bind the key", async () => {
  // Holding the function key, a key the set leaves out falls to the base
  // layer, which comes before common: the shared setting does not apply
  // (ADR-0069).
  const shared: Profile = {
    ...paint,
    common: { south: { press: keys("Ctrl+Y") } },
  };
  await setup("south", "function", "zh-CN", shared);
  expect(screen.getByTestId("drawer-inherited").textContent).toBe(
    "平时：Ctrl+Z",
  );
  expect(screen.queryByTestId("drawer-common-notice")).toBeNull();
  cleanup();
  // Base leaves the key out too: common is what runs, and the notice says so.
  await setup("south", "function", "zh-CN", {
    ...shared,
    layers: { base: { north: { press: keys("F13") } } },
  });
  expect(screen.queryByTestId("drawer-inherited")).toBeNull();
  expect(screen.getByTestId("drawer-common-notice")).toBeTruthy();
});

it("starts the fields over when the view changes with the drawer open", async () => {
  const { editor } = await setup("north", "normal");
  // A timing field left with a value out of range keeps the text and the error.
  fireEvent.click(screen.getByText("高级"));
  const hold = screen.getByRole("textbox", { name: "触发长按时间（ms）" });
  fireEvent.change(hold, { target: { value: "1e" } });
  fireEvent.blur(hold);
  expect(hold.getAttribute("aria-invalid")).toBe("true");
  // A gesture picked from the menu is a draft card, not yet in the file.
  fireEvent.click(screen.getByTestId("add-gesture"));
  fireEvent.click(screen.getByRole("menuitem", { name: /^长按/ }));
  expect(screen.getByRole("button", { name: /^长按/ })).toBeTruthy();
  // Text typed in the label field and not committed.
  const label = screen.getByRole("textbox", { name: "功能" });
  fireEvent.change(label, { target: { value: "Draft" } });
  act(() => editor.getState().setView("function"));
  // The function set is another binding: none of it is carried over.
  const next = screen.getByRole("textbox", {
    name: "触发长按时间（ms）",
    hidden: true,
  }) as HTMLInputElement;
  expect(next.value).toBe("");
  expect(next.getAttribute("aria-invalid")).toBeNull();
  expect(screen.queryByRole("alert")).toBeNull();
  expect(screen.queryByRole("button", { name: /^长按/ })).toBeNull();
  expect(
    (
      screen.getByRole("textbox", {
        name: "功能",
      }) as HTMLInputElement
    ).value,
  ).toBe("Redo");
  // Nothing was written anywhere.
  expect(draft(editor).functionSet).toEqual(paint.functionSet);
  expect(draft(editor).layers.base.north).toEqual({ press: keys("F13") });
});

it("clears a control from the set, and the set when it is the last", async () => {
  const { editor } = await setup("north", "function");
  fireEvent.click(screen.getByTestId("key-actions"));
  const menu = screen.getByRole("menu", { name: "按键操作" });
  expect(
    within(menu)
      .getAllByRole("menuitem")
      .map((item) => item.textContent),
  ).toEqual(["复制此按键的设置", "粘贴…", "重命名按键…", "清除这一套里的设置"]);
  fireEvent.click(
    within(menu).getByRole("menuitem", { name: "清除这一套里的设置" }),
  );
  // The set keeps its other control.
  expect(draft(editor).functionSet).toEqual({
    "ls.up": paint.functionSet?.["ls.up"],
  });
  expect(draft(editor).layers.base.north).toEqual({ press: keys("F13") });
  // One undo step.
  act(() => {
    editor.getState().undo();
  });
  expect(draft(editor).functionSet?.north?.label).toBe("Redo");
});

it("removes functionSet when the last control goes", async () => {
  const only = await setup("north", "function", "zh-CN", {
    ...paint,
    functionSet: { north: paint.functionSet?.north },
  });
  fireEvent.click(screen.getByTestId("key-actions"));
  fireEvent.click(screen.getByRole("menuitem", { name: "清除这一套里的设置" }));
  expect("functionSet" in draft(only.editor)).toBe(false);
});

it("offers to clear only what the set has, and in English", async () => {
  await setup("south", "function", "en");
  fireEvent.click(screen.getByTestId("key-actions"));
  const item = screen.getByRole("menuitem", { name: "Clear in this set" });
  // South is not in the set: nothing to clear.
  expect((item as HTMLButtonElement).disabled).toBe(true);
});

it("keeps the usual menu in the usual view", async () => {
  await setup("north", "normal");
  fireEvent.click(screen.getByTestId("key-actions"));
  expect(
    within(screen.getByRole("menu", { name: "按键操作" }))
      .getAllByRole("menuitem")
      .map((item) => item.textContent),
  ).toEqual(["复制此按键的设置", "粘贴…", "重命名按键…", "清空此按键…"]);
});

it("pastes into the set", async () => {
  const { editor } = await setup("south", "normal");
  fireEvent.click(screen.getByTestId("key-actions"));
  fireEvent.click(screen.getByRole("menuitem", { name: "复制此按键的设置" }));
  act(() => editor.getState().setView("function"));
  fireEvent.click(screen.getByTestId("key-actions"));
  fireEvent.click(screen.getByRole("menuitem", { name: "粘贴…" }));
  fireEvent.click(screen.getByRole("button", { name: "粘贴" }));
  expect(draft(editor).functionSet?.south).toEqual({
    label: "Undo",
    press: keys("Ctrl+Z"),
  });
  expect(draft(editor).layers.base.south).toEqual({
    label: "Undo",
    press: keys("Ctrl+Z"),
  });
});

it("binds one of View, Menu and the Xbox button that is not the function key, in the set as anywhere", async () => {
  const { editor, record } = await setup(
    "back",
    "function",
    "zh-CN",
    paint,
    info,
    "guide",
  );
  expect(shortcutValue()).toBe("");
  await record("Ctrl+V");
  expect(draft(editor).functionSet?.back).toEqual({
    press: { type: "keys", keys: "Ctrl+V" },
  });
  expect(draft(editor).layers.base.back).toBeUndefined();
  cleanup();
  const usual = await setup("start", "normal", "zh-CN", paint, info, "guide");
  await usual.record("Ctrl+S");
  expect(draft(usual.editor).layers.base.start).toEqual({
    press: { type: "keys", keys: "Ctrl+S" },
  });
});

const problem = (
  pointer: string,
  severity: "error" | "warning" = "error",
): ProfileDiagnostic => ({
  severity,
  pointer,
  offset: 0,
  line: 1,
  col: 1,
  message: "problem",
});

it("lists the problems of the shown set, not of the usual keys", async () => {
  const { editor } = await setup("north", "function");
  act(() =>
    editor.setState({
      diagnostics: [
        problem("/functionSet/north/press"),
        // A file of the old format points into one of its two sets.
        problem("/functionKeys/back/north/press"),
        problem("/functionSet/south/press"),
        problem("/layers/base/north/press"),
      ],
    }),
  );
  const items = screen.getAllByTestId("control-diagnostic");
  expect(items).toHaveLength(2);
  expect(items[0].textContent).toContain("点按");
  act(() => editor.getState().setView("normal"));
  expect(screen.getAllByTestId("control-diagnostic")).toHaveLength(1);
});

it("does not let a script of the usual keys speak for a control of the set", async () => {
  const registration = (scope: string): ScriptRegistration => ({
    id: 1,
    control: "north",
    gesture: "press",
    scope,
    observe: false,
    claim: true,
    priority: 0,
    timing: {},
  });
  const { editor } = await setup("north", "function");
  act(() => editor.setState({ registrations: [registration("base")] }));
  expect(screen.queryByTestId("script-ownership")).toBeNull();
  // A global handler comes before every set, so it still owns the control.
  act(() => editor.setState({ registrations: [registration("global")] }));
  expect(screen.getAllByTestId("script-ownership").length).toBeGreaterThan(0);
  act(() => editor.getState().setView("normal"));
  act(() => editor.setState({ registrations: [registration("base")] }));
  expect(screen.getAllByTestId("script-ownership").length).toBeGreaterThan(0);
});

it("edits the directions of a stick in the set", async () => {
  const { editor } = await setup("leftstick", "function");
  const root = screen.getByRole("complementary");
  const up = root.querySelector<HTMLButtonElement>(
    'button[data-control="ls.up"]',
  );
  expect(up).not.toBeNull();
  fireEvent.click(up as HTMLElement);
  expect(shortcutValue()).toBe("W");
  expect(draft(editor).layers.base).toEqual(paint.layers.base);
});

it("starts the direction cards of a stick over when the view changes", async () => {
  const { editor } = await setup("leftstick", "function");
  const root = screen.getByRole("complementary");
  fireEvent.click(
    root.querySelector<HTMLButtonElement>(
      'button[data-control="ls.up"]',
    ) as HTMLElement,
  );
  const card = root.querySelector<HTMLElement>(
    'section.analog-card[data-control="ls.up"]',
  ) as HTMLElement;
  expect(card).not.toBeNull();
  fireEvent.click(within(card).getByText("高级"));
  const hold = within(card).getByRole("textbox", {
    name: "触发长按时间（ms）",
  });
  fireEvent.change(hold, { target: { value: "1e" } });
  fireEvent.blur(hold);
  expect(hold.getAttribute("aria-invalid")).toBe("true");
  act(() => editor.getState().setView("normal"));
  // The error belonged to the field of the other set.
  expect(screen.queryByRole("alert")).toBeNull();
  for (const field of screen.queryAllByRole("textbox", { hidden: true }))
    expect((field as HTMLInputElement).value).not.toBe("1e");
  expect(draft(editor).functionSet).toEqual(paint.functionSet);
});

it("falls back to the usual keys on a pad without the function key", async () => {
  const joyCon: DeviceInfo = { ...info, type: "joyconleft", buttonMask: 0xf };
  await setup("north", "function", "zh-CN", paint, joyCon, "back");
  expect(shortcutValue()).toBe("F13");
  expect(screen.queryByTestId("drawer-view")).toBeNull();
});

it("edits a file of the old functionKeys as the one set it stands for", async () => {
  const { editor, record } = await setup("north", "function", "zh-CN", {
    ...paint,
    functionSet: undefined,
    functionKeys: {
      back: { north: { label: "Back's", press: keys("F15") } },
      start: { north: { label: "Redo", press: keys("F14") } },
    },
  });
  // Menu's set wins over View's where both bind the control.
  expect(shortcutValue()).toBe("F14");
  await record("F20");
  expect("functionKeys" in draft(editor)).toBe(false);
  expect(draft(editor).functionSet?.north).toMatchObject({
    label: "Redo",
    press: { type: "keys", keys: "F20" },
  });
});
