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
import type { ComponentProps } from "react";
import { afterEach, expect, it, vi } from "vitest";
import type {
  DeviceInfo,
  KeyboardCaptureEvent,
  PadmateAPI,
  ScriptRegistration,
} from "../shared/api";
import type { Profile } from "../shared/generated/schema";
import { fakeCapture, shortcutValue } from "./capture-fixture";
import { editorAPIFixture } from "./editor-api-fixture";
import { createEditorStore, type EditorStore } from "./editor-store";
import { KeyDrawer } from "./KeyDrawer";
import { controlDiagnostics } from "./KeyDrawerNotices";
import { MacroContext } from "./macro-context";
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

const paint: Profile = {
  schemaVersion: 1,
  name: "Paint",
  layers: {
    base: {
      north: { press: { type: "keys", keys: "F13" } },
      south: {
        label: "Undo",
        tap: { type: "keys", keys: "Ctrl+Z" },
        hold: { type: "keys", keys: "Ctrl+Shift+Z" },
      },
    },
  },
};

type DrawerProps = Partial<ComponentProps<typeof KeyDrawer>>;

function drawer(
  editor: EditorStore,
  control: string,
  language: Language = "zh-CN",
  props: DrawerProps = {},
) {
  return (
    <KeyDrawer
      editor={editor}
      control={control}
      onControl={() => {}}
      info={info}
      language={language}
      onClose={() => {}}
      {...props}
    />
  );
}

async function setup(
  profile: Profile = paint,
  control = "north",
  language: Language = "zh-CN",
  props: DrawerProps = {},
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
  const onClose = vi.fn();
  const view = render(drawer(editor, control, language, { onClose, ...props }));
  return {
    api,
    editor,
    onClose,
    record,
    rerender: (next: string, nextProps: DrawerProps = {}) =>
      view.rerender(
        drawer(editor, next, language, { onClose, ...props, ...nextProps }),
      ),
  };
}

const base = (editor: EditorStore) => editor.getState().draft?.layers.base;

/** Types into a text field and leaves it, which commits the text (UI-43). */
function commit(field: Element, init: { target: { value: string } }) {
  fireEvent.change(field, init);
  fireEvent.blur(field);
}

function registration(
  id: number,
  control: string,
  extra: Partial<ScriptRegistration> = {},
): ScriptRegistration {
  return {
    id,
    control,
    gesture: "press",
    scope: "base",
    observe: false,
    claim: true,
    priority: 0,
    timing: {},
    ...extra,
  };
}

it("shows the key, its label and the first gesture ready to edit", async () => {
  const { onClose } = await setup();
  const aside = screen.getByRole("complementary", { name: "按键编辑" });
  expect(aside.dataset.testid).toBe("mapping-editor");
  expect(within(aside).getByRole("heading", { name: "Y 键" })).toBeDefined();
  expect(within(aside).getByText("配置「Paint」")).toBeDefined();
  expect(
    (
      screen.getByRole("textbox", {
        name: "在手柄图上显示为",
      }) as HTMLInputElement
    ).value,
  ).toBe("");
  const types = screen.getByTestId("action-type");
  expect(types.getAttribute("aria-label")).toBe("动作类型");
  expect(
    within(types)
      .getByRole("radio", { name: "快捷键" })
      .getAttribute("aria-checked"),
  ).toBe("true");
  expect(within(types).getByRole("radio", { name: "脚本" })).toBeDefined();
  // One app, one profile: the editor offers no profile switch.
  expect(within(types).queryByRole("radio", { name: "切换配置" })).toBeNull();
  // Shortcut, script, precision and key hints (a press holds them); no switch.
  expect(
    within(types)
      .getAllByRole("radio")
      .map((radio) => radio.textContent),
  ).toEqual(["快捷键", "输入文字", "脚本", "精确模式", "按键提示"]);
  expect(shortcutValue()).toBe("F13");
  expect(screen.getByRole("button", { name: "录入快捷键" })).toBeDefined();
  expect(aside.textContent).not.toMatch(/层/);
  fireEvent.click(screen.getByRole("button", { name: "关闭按键编辑" }));
  expect(onClose).toHaveBeenCalledOnce();
});

it("uses English names for the drawer landmarks", async () => {
  await setup(paint, "north", "en");
  expect(
    screen.getByRole("complementary", { name: "Key editor" }),
  ).toBeDefined();
  expect(screen.getByRole("heading", { name: "Y key" })).toBeDefined();
  expect(
    screen.getByRole("button", { name: "Close key editor" }),
  ).toBeDefined();
  expect(screen.getByRole("group", { name: "Shortcut" })).toBeDefined();
});

it("renames the key from the title and removes an empty name", async () => {
  const { editor } = await setup();
  fireEvent.click(screen.getByRole("button", { name: "重命名此按键" }));
  const name = screen.getByRole("textbox", { name: "按键名称" });
  expect(name.getAttribute("maxLength")).toBe("80");
  expect(document.activeElement).toBe(name);
  fireEvent.change(name, { target: { value: "笔刷循环" } });
  // Typing is not an edit yet; Enter commits it.
  expect(editor.getState().draft?.controlNames).toBeUndefined();
  fireEvent.keyDown(name, { key: "Enter" });
  expect(editor.getState().draft?.controlNames?.north).toBe("笔刷循环");
  expect(screen.getByRole("heading", { name: "笔刷循环" })).toBeDefined();
  expect(document.activeElement).toBe(
    screen.getByRole("button", { name: "重命名此按键" }),
  );
  // Escape keeps the name as it was.
  fireEvent.click(screen.getByRole("button", { name: "重命名此按键" }));
  fireEvent.change(screen.getByRole("textbox", { name: "按键名称" }), {
    target: { value: "" },
  });
  fireEvent.keyDown(screen.getByRole("textbox", { name: "按键名称" }), {
    key: "Escape",
  });
  expect(editor.getState().draft?.controlNames?.north).toBe("笔刷循环");
  expect(screen.getByRole("heading", { name: "笔刷循环" })).toBeDefined();
  // An empty name, committed by leaving the field, removes the name.
  fireEvent.click(screen.getByRole("button", { name: "重命名此按键" }));
  expect(
    (screen.getByRole("textbox", { name: "按键名称" }) as HTMLInputElement)
      .value,
  ).toBe("笔刷循环");
  commit(screen.getByRole("textbox", { name: "按键名称" }), {
    target: { value: " " },
  });
  expect(editor.getState().draft?.controlNames).toBeUndefined();
  expect(screen.getByRole("heading", { name: "Y 键" })).toBeDefined();
});

it("edits the label shown on the controller and drops it when emptied", async () => {
  const { editor } = await setup();
  const label = screen.getByRole("textbox", { name: "在手柄图上显示为" });
  expect(label.getAttribute("placeholder")).toBe("F13");
  fireEvent.change(label, { target: { value: "画笔" } });
  expect(base(editor)?.north?.label).toBeUndefined();
  fireEvent.keyDown(label, { key: "Enter" });
  expect(base(editor)?.north?.label).toBe("画笔");
  commit(label, { target: { value: "" } });
  expect(base(editor)?.north).toEqual({
    press: { type: "keys", keys: "F13" },
  });
});

it("adds a gesture from the menu as a draft and removes it again", async () => {
  const { editor, record } = await setup();
  fireEvent.click(screen.getByTestId("add-gesture"));
  const menu = screen.getByRole("menu", { name: "添加手势" });
  expect(within(menu).queryByRole("menuitem", { name: /^按下/ })).toBeNull();
  fireEvent.click(within(menu).getByRole("menuitem", { name: /^长按/ }));
  // The new gesture opens alone and is not in the draft until it is set.
  expect(base(editor)?.north?.hold).toBeUndefined();
  expect(editor.getState().unsaved).toBe(false);
  expect(
    screen.getByRole("button", { name: /^按下/ }).getAttribute("aria-expanded"),
  ).toBe("false");
  expect(
    screen.getByRole("button", { name: /^长按/ }).getAttribute("aria-expanded"),
  ).toBe("true");
  expect(shortcutValue()).toBe("");
  await record("Ctrl+H");
  expect(base(editor)?.north?.hold).toEqual({ type: "keys", keys: "Ctrl+H" });
  fireEvent.click(screen.getByRole("button", { name: "删除此手势" }));
  expect(base(editor)?.north?.hold).toBeUndefined();
  expect(base(editor)?.north?.press).toEqual({ type: "keys", keys: "F13" });
});

it("offers a ready press gesture for an unset key without touching the draft", async () => {
  const { editor, record } = await setup(paint, "west");
  expect(screen.getByRole("heading", { name: "X 键" })).toBeDefined();
  expect(
    screen.getByRole("button", { name: /^按下/ }).getAttribute("aria-expanded"),
  ).toBe("true");
  expect(screen.queryByRole("button", { name: "删除此手势" })).toBeNull();
  expect(base(editor)?.west).toBeUndefined();
  await record("F9");
  expect(base(editor)?.west).toEqual({ press: { type: "keys", keys: "F9" } });
  expect(screen.getByRole("button", { name: "删除此手势" })).toBeDefined();
});

it("runs a script of the library: one pick, one save, one undo step", async () => {
  const api = editorAPIFixture();
  api.getProfile = vi.fn(async (id: string) => ({
    id,
    text: JSON.stringify(paint),
    revision: "r1",
    diagnostics: [],
  }));
  api.listMacros.mockResolvedValue({
    macros: [{ file: "复制全部.ts", name: "复制全部", usedBy: [] }],
    missing: [],
  });
  window.padmate = api as unknown as PadmateAPI;
  const editor = createEditorStore(api as unknown as PadmateAPI);
  await editor
    .getState()
    .activate({ id: "paint", device: "d", connection: "c" });
  await act(() => editor.getState().refreshMacros());
  expect(api.listMacros).toHaveBeenCalledWith();
  const list = editor.getState().macros;
  expect(list?.macros.map((macro) => macro.file)).toEqual(["复制全部.ts"]);
  render(
    <MacroContext value={{ list, failing: new Set() }}>
      {drawer(editor, "north")}
    </MacroContext>,
  );
  const types = screen.getByTestId("action-type");
  fireEvent.click(within(types).getByRole("radio", { name: "脚本" }));
  // Choosing 脚本 changes nothing until a script is picked.
  expect(base(editor)?.north?.press).toEqual({ type: "keys", keys: "F13" });
  expect(editor.getState().unsaved).toBe(false);
  expect(editor.getState().canUndo).toBe(false);
  fireEvent.change(screen.getByRole("combobox", { name: "选择脚本" }), {
    target: { value: "复制全部.ts" },
  });
  expect(base(editor)?.north?.press).toEqual({
    type: "macro",
    file: "复制全部.ts",
  });
  await act(() => editor.getState().flush());
  expect(api.putProfile).toHaveBeenCalledOnce();
  const text = (api.putProfile.mock.calls[0] as unknown[])[1] as string;
  expect((JSON.parse(text) as Profile).layers.base.north?.press).toEqual({
    type: "macro",
    file: "复制全部.ts",
  });
  // The label field suggests the script's name.
  expect(
    (
      screen.getByRole("textbox", {
        name: "在手柄图上显示为",
      }) as HTMLInputElement
    ).placeholder,
  ).toBe("复制全部");
  // One undo step takes the pick back, and saves that too.
  act(() => {
    editor.getState().undo();
  });
  expect(base(editor)?.north?.press).toEqual({ type: "keys", keys: "F13" });
  expect(editor.getState().canUndo).toBe(false);
  await act(() => editor.getState().flush());
  expect(api.putProfile).toHaveBeenCalledTimes(2);
  // profile.changed reads the list again, even when the revision stays.
  api.listMacros.mockResolvedValue({ macros: [], missing: [] });
  await act(() => editor.getState().profileChanged());
  expect(editor.getState().macros).toEqual({ macros: [], missing: [] });
});

it("makes the drawer read-only when the profile starts in another key group", async () => {
  const { editor } = await setup({
    ...paint,
    defaultLayer: "alt",
    layers: { ...paint.layers, alt: {} },
  });
  expect(editor.getState().readOnly).toBe("defaultLayer");
  expect(screen.getByTestId("drawer-read-only").textContent).toContain(
    "此配置在界面中只能查看",
  );
  expect(screen.queryByTestId("drawer-file-notice")).toBeNull();
  // Fields are disabled through their fieldset.
  expect(
    screen.getByRole("button", { name: "录入快捷键" }).matches(":disabled"),
  ).toBe(true);
  expect(
    screen
      .getByRole("textbox", { name: "在手柄图上显示为" })
      .matches(":disabled"),
  ).toBe(true);
  expect(
    within(screen.getByTestId("action-type"))
      .getByRole("radio", { name: "脚本" })
      .matches(":disabled"),
  ).toBe(true);
  expect(
    (screen.getByTestId("add-gesture") as HTMLButtonElement).disabled,
  ).toBe(true);
  expect(
    (screen.getByRole("button", { name: "重命名此按键" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  // Cards still open and close so the settings can be read.
  fireEvent.click(screen.getByRole("button", { name: /^按下/ }));
  expect(screen.queryByRole("group", { name: "快捷键" })).toBeNull();
  expect(screen.getByRole("complementary").textContent).not.toMatch(/层/);
});

it("shows layer actions from the file read-only and replaces them only after confirming", async () => {
  const layered: Profile = {
    schemaVersion: 1,
    name: "Layered",
    layers: {
      base: {
        leftshoulder: {
          tap: { type: "keys", keys: "F13" },
          hold: { type: "layer", op: "hold", layer: "alt" },
        },
      },
      alt: { south: { press: { type: "keys", keys: "B" } } },
    },
  };
  const { editor } = await setup(layered, "leftshoulder");
  const hold = screen.getByRole("button", { name: /^长按/ });
  expect(hold.textContent).toContain("切换按键组");
  fireEvent.click(hold);
  expect(screen.getByText("切换按键组（在配置文件中设置）")).toBeDefined();
  expect(screen.queryByTestId("action-type")).toBeNull();
  // Removing it asks first: the editor cannot set it again.
  fireEvent.click(screen.getByRole("button", { name: "删除此手势" }));
  expect(screen.getByText(/^此按键的长按当前会切换按键组。/)).toBeDefined();
  expect(base(editor)?.leftshoulder?.hold?.type).toBe("layer");
  fireEvent.click(screen.getByRole("button", { name: "取消" }));
  expect(base(editor)?.leftshoulder?.hold?.type).toBe("layer");
  expect(screen.queryByText(/^此按键的长按当前会切换按键组。/)).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "改用快捷键…" }));
  expect(base(editor)?.leftshoulder?.hold?.type).toBe("layer");
  fireEvent.click(screen.getByRole("button", { name: "改用快捷键" }));
  expect(base(editor)?.leftshoulder?.hold).toEqual({ type: "keys", keys: "" });
  expect(editor.getState().draft?.layers.alt).toEqual(layered.layers.alt);
  expect(screen.getByRole("complementary").textContent).not.toMatch(/层/);
});

it("removes a layer action from the file only after confirming", async () => {
  const { editor } = await setup(
    {
      schemaVersion: 1,
      name: "Layered",
      layers: {
        base: {
          leftshoulder: {
            tap: { type: "keys", keys: "F13" },
            hold: { type: "layer", op: "hold", layer: "alt" },
          },
        },
        alt: {},
      },
    },
    "leftshoulder",
  );
  fireEvent.click(screen.getByRole("button", { name: /^长按/ }));
  fireEvent.click(screen.getByRole("button", { name: "删除此手势" }));
  const dialog = screen.getByRole("dialog", { name: "删除此手势？" });
  expect(dialog.textContent).not.toMatch(/层/);
  expect(base(editor)?.leftshoulder?.hold?.type).toBe("layer");
  fireEvent.click(within(dialog).getByRole("button", { name: "删除" }));
  expect(base(editor)?.leftshoulder).toEqual({
    tap: { type: "keys", keys: "F13" },
  });
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.activeElement).toBe(screen.getByTestId("add-gesture"));
});

it("summarises the parts of a layered file in one notice", async () => {
  await setup({
    ...paint,
    common: {
      rightstick: {
        press: { type: "layer", op: "cycle", layers: ["base", "alt"] },
      },
    },
    layers: {
      ...paint.layers,
      alt: {
        color: "#3366ff",
        south: { press: { type: "profile", profile: "photo" } },
      },
    },
  });
  const notice = screen.getByTestId("drawer-file-notice");
  expect(notice.textContent).toContain("此配置文件中还有 1 组按键");
  expect(notice.textContent).toContain("界面将原样保留");
  expect(notice.textContent).toContain("此处的修改仅在此组按键使用中时生效");
  expect(notice.textContent).toContain(
    "另有 1 个切换配置的动作，同样只能在配置文件中修改。",
  );
  expect(screen.getByRole("complementary").textContent).not.toMatch(/层/);
});

const switching: Profile = {
  schemaVersion: 1,
  name: "Switching",
  layers: {
    base: {
      leftshoulder: {
        tap: { type: "keys", keys: "F13" },
        hold: { type: "profile", profile: "photo" },
      },
      rightshoulder: { press: { type: "profile", profile: "sketch" } },
      north: { press: { type: "keys", keys: "F14" } },
    },
  },
};

it("shows a profile switch from the file locked and replaces it only after confirming", async () => {
  const { editor } = await setup(switching, "leftshoulder");
  act(() =>
    editor.setState({
      profiles: [
        { id: "paint", name: "Paint" },
        { id: "photo", name: "Photo" },
      ],
    }),
  );
  const hold = screen.getByRole("button", { name: /^长按/ });
  const summary = hold.querySelector(".drawer-summary-locked");
  expect(summary?.textContent).toBe("切换到「Photo」");
  expect(summary?.querySelector(".ui-icon")).not.toBeNull();
  // A long name ends in an ellipsis; the tooltip keeps all of it.
  expect(summary?.querySelector(".drawer-summary-text")?.textContent).toBe(
    "切换到「Photo」",
  );
  expect(summary?.getAttribute("title")).toBe("切换到「Photo」");
  fireEvent.click(hold);
  const locked = screen.getByTestId("locked-action");
  expect(locked.textContent).toContain("切换到「Photo」（在配置文件中设置）");
  expect(locked.textContent).toContain("此动作无法在界面中修改，将原样保留。");
  expect(screen.queryByTestId("action-type")).toBeNull();
  // The label suggestion describes the first gesture, as the chip does.
  expect(
    screen
      .getByRole("textbox", { name: "在手柄图上显示为" })
      .getAttribute("placeholder"),
  ).toBe("F13");
  // Removing it asks first: the editor cannot set it again.
  fireEvent.click(screen.getByRole("button", { name: "删除此手势" }));
  const remove = screen.getByRole("dialog", { name: "删除此手势？" });
  expect(remove.textContent).toContain("此按键的长按当前会切换到「Photo」。");
  fireEvent.click(within(remove).getByRole("button", { name: "取消" }));
  expect(base(editor)?.leftshoulder?.hold).toEqual({
    type: "profile",
    profile: "photo",
  });
  fireEvent.click(screen.getByRole("button", { name: "改用快捷键…" }));
  const replace = screen.getByRole("dialog", { name: "改用快捷键？" });
  expect(replace.textContent).toContain("此按键的长按当前会切换到「Photo」。");
  expect(base(editor)?.leftshoulder?.hold?.type).toBe("profile");
  fireEvent.click(within(replace).getByRole("button", { name: "改用快捷键" }));
  expect(base(editor)?.leftshoulder?.hold).toEqual({ type: "keys", keys: "" });
  // The new action offers shortcut, script, precision and key hints.
  expect(
    within(screen.getByTestId("action-type"))
      .getAllByRole("radio")
      .map((radio) => radio.textContent),
  ).toEqual(["快捷键", "输入文字", "脚本", "精确模式", "按键提示"]);
  expect(base(editor)?.rightshoulder).toEqual(
    switching.layers.base.rightshoulder,
  );
  expect(screen.getByRole("complementary").textContent).not.toMatch(/层/);
});

it("removes a profile switch from the file only after confirming", async () => {
  const { editor } = await setup(switching, "rightshoulder");
  // A target the library does not list shows by its ID.
  expect(screen.getByRole("button", { name: /^按下/ }).textContent).toContain(
    "切换到「sketch」",
  );
  expect(screen.getByTestId("locked-action").textContent).toContain(
    "切换到「sketch」（在配置文件中设置）",
  );
  fireEvent.click(screen.getByRole("button", { name: "删除此手势" }));
  const dialog = screen.getByRole("dialog", { name: "删除此手势？" });
  expect(dialog.textContent).toContain("此按键的按下当前会切换到「sketch」。");
  expect(dialog.textContent).not.toMatch(/层/);
  expect(base(editor)?.rightshoulder?.press?.type).toBe("profile");
  fireEvent.click(within(dialog).getByRole("button", { name: "删除" }));
  expect(base(editor)?.rightshoulder).toBeUndefined();
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("saves profile switches exactly as the file has them", async () => {
  const { api, editor, record, rerender } = await setup(
    switching,
    "leftshoulder",
  );
  // Another gesture on the same key: the tap card opens first.
  await record("F15");
  // An unrelated key.
  rerender("north");
  await record("F16");
  await act(() => editor.getState().flush());
  // Each committed edit is saved at once.
  expect(api.putProfile).toHaveBeenCalledTimes(2);
  const text = (api.putProfile.mock.calls[1] as unknown[])[1] as string;
  const saved = JSON.parse(text) as Profile;
  expect(saved.layers.base.leftshoulder?.tap).toEqual({
    type: "keys",
    keys: "F15",
  });
  expect(saved.layers.base.north?.press).toEqual({ type: "keys", keys: "F16" });
  // The same fields in the same order, so the file keeps them byte for byte.
  expect(JSON.stringify(saved.layers.base.leftshoulder?.hold)).toBe(
    JSON.stringify(switching.layers.base.leftshoulder?.hold),
  );
  expect(JSON.stringify(saved.layers.base.rightshoulder)).toBe(
    JSON.stringify(switching.layers.base.rightshoulder),
  );
  expect(Object.keys(saved.layers.base)).toEqual(
    Object.keys(switching.layers.base),
  );
});

it("counts profile switches from the file in a plain notice", async () => {
  await setup(switching);
  const notice = screen.getByTestId("drawer-file-notice");
  expect(notice.textContent).toContain("此配置文件中有 2 个切换配置的动作");
  expect(notice.textContent).toContain("只能在配置文件中修改，界面将原样保留");
  expect(screen.getByRole("complementary").textContent).not.toMatch(/层/);
  cleanup();
  await setup(switching, "rightshoulder", "en");
  expect(screen.getByTestId("drawer-file-notice").textContent).toContain(
    "This profile file has 2 actions that switch profiles",
  );
  expect(screen.getByTestId("locked-action").textContent).toContain(
    "Switches to sketch (set in the profile file)",
  );
  expect(screen.getByRole("complementary").textContent).not.toMatch(/layer/i);
});

it("explains that a setting here takes the place of a shared one", async () => {
  const { editor, record } = await setup(
    {
      ...paint,
      common: { west: { press: { type: "keys", keys: "F1" } } },
    },
    "west",
  );
  expect(screen.getByTestId("drawer-file-notice").textContent).toContain(
    "界面未显示的设置",
  );
  expect(screen.getByTestId("drawer-common-notice").textContent).toContain(
    "按下 F1",
  );
  await record("F2");
  expect(screen.queryByTestId("drawer-common-notice")).toBeNull();
  expect(editor.getState().draft?.common?.west).toEqual({
    press: { type: "keys", keys: "F1" },
  });
});

it("copies a key and pastes it onto another key after confirming", async () => {
  const { editor, rerender } = await setup();
  fireEvent.click(screen.getByRole("button", { name: "按键操作" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "复制此按键的设置" }));
  expect(screen.getByRole("status").textContent).toContain("已复制");
  rerender("south");
  expect(screen.queryByRole("status")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "按键操作" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "粘贴…" }));
  expect(screen.getByText(/用复制的设置替换「A 键」/)).toBeDefined();
  expect(base(editor)?.south?.tap).toBeDefined();
  fireEvent.click(screen.getByRole("button", { name: "粘贴" }));
  expect(base(editor)?.south).toEqual({ press: { type: "keys", keys: "F13" } });
  expect(base(editor)?.north).toEqual({ press: { type: "keys", keys: "F13" } });
});

it("clears a key only while the confirmed key and profile are unchanged", async () => {
  const { editor, rerender } = await setup();
  fireEvent.click(screen.getByRole("button", { name: "按键操作" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "清空此按键…" }));
  rerender("south");
  const clear = screen.getByRole("button", {
    name: "清空",
  }) as HTMLButtonElement;
  expect(clear.disabled).toBe(true);
  expect(screen.getByRole("alert").textContent).toContain("请取消后重新操作");
  fireEvent.click(clear);
  expect(base(editor)?.north).toBeDefined();
  expect(base(editor)?.south).toBeDefined();
  fireEvent.click(screen.getByRole("button", { name: "取消" }));

  fireEvent.click(screen.getByRole("button", { name: "按键操作" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "清空此按键…" }));
  // Its own saves keep the request; a read from disk (a change made
  // elsewhere) makes it stale.
  const loads = editor.getState().loads;
  act(() => editor.setState({ revision: "r9" }));
  expect(
    (screen.getByRole("button", { name: "清空" }) as HTMLButtonElement)
      .disabled,
  ).toBe(false);
  act(() => editor.setState({ loads: loads + 1 }));
  expect(
    (screen.getByRole("button", { name: "清空" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  act(() => editor.setState({ loads }));
  act(() =>
    editor.setState({
      id: "other",
      context: { id: "other", device: "d", connection: "c" },
    }),
  );
  expect(
    (screen.getByRole("button", { name: "清空" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  act(() =>
    editor.setState({
      id: "paint",
      context: { id: "paint", device: "d", connection: "c" },
    }),
  );
  fireEvent.click(screen.getByRole("button", { name: "清空" }));
  expect(base(editor)?.south).toBeUndefined();
  expect(base(editor)?.north).toBeDefined();
});

it("shows what the script does with the key and when it competes with bindings", async () => {
  const { editor } = await setup();
  act(() =>
    editor.setState({
      description: {
        entry: "main.ts",
        api: "0.21",
        metadata: { runtime: 0, actions: [], registrations: [] },
      },
      registrations: [
        registration(1, "north", { label: "换笔刷" }),
        registration(2, "north", {
          gesture: "hold",
          claim: false,
          label: "临时橡皮",
        }),
        registration(3, "north", {
          gesture: "release",
          claim: false,
          observe: true,
          label: "统计",
        }),
        registration(4, "north", { scope: "layer:alt", label: "别的组" }),
      ],
    }),
  );
  const ownership = screen.getByTestId("script-ownership");
  expect(ownership.textContent).toBe("S");
  expect(screen.getByText("由脚本 main.ts 接管")).toBeDefined();
  const rows = screen.getByRole("list", { name: "脚本如何使用此按键" });
  expect(
    within(rows)
      .getAllByRole("listitem")
      .map((row) => row.textContent),
  ).toEqual(["按下换笔刷接管", "长按临时橡皮可消费", "松开统计只读取"]);
  expect(screen.getByText(/^此按键同时有绑定和脚本处理/)).toBeDefined();
  expect(screen.getByTestId("script-other-scopes").textContent).toContain(
    "1 处",
  );
  // A press fires at once even though the script also uses a hold.
  expect(screen.queryByTestId("gesture-delay")).toBeNull();

  act(() =>
    editor.setState({
      registrations: [
        registration(5, "north", { observe: true, claim: false }),
      ],
    }),
  );
  expect(screen.queryByTestId("script-ownership")).toBeNull();
  expect(screen.getByText("脚本 main.ts 会读取此按键")).toBeDefined();
  expect(screen.queryByText(/此按键同时有绑定和脚本处理/)).toBeNull();
});

it("does not suggest a gesture for a key the script owns", async () => {
  const { editor } = await setup(paint, "west");
  act(() => editor.setState({ registrations: [registration(1, "west")] }));
  expect(screen.getByTestId("script-ownership").textContent).toBe("S");
  expect(screen.getByText("此按键的输入先由脚本处理。")).toBeDefined();
  expect(screen.queryByRole("group", { name: "快捷键" })).toBeNull();
  expect(screen.getByText("尚未设置手势。")).toBeDefined();
});

it("lists the validation results for this key in plain words", async () => {
  const { editor } = await setup();
  act(() =>
    editor.setState({
      diagnostics: [
        {
          severity: "error",
          pointer: "/layers/base/north/press/keys",
          offset: 4,
          line: 3,
          col: 2,
          message: 'unknown key "NotAKey"',
        },
        {
          severity: "warning",
          pointer: "/controlNames/north",
          offset: 8,
          line: 9,
          col: 2,
          message: "name is long",
        },
        {
          severity: "error",
          pointer: "/layers/base/south/tap/keys",
          offset: 6,
          line: 5,
          col: 2,
          message: "elsewhere",
        },
        {
          severity: "error",
          pointer: "/layers/alt/north/press/keys",
          offset: 7,
          line: 6,
          col: 2,
          message: "hidden group",
        },
      ],
    }),
  );
  const items = screen.getAllByTestId("control-diagnostic");
  expect(items.map((item) => item.textContent)).toEqual([
    '错误 · 按下：unknown key "NotAKey"',
    "警告 · 按键名称：name is long",
  ]);
  expect(items[1].dataset.severity).toBe("warning");
});

it("explains gesture waits in one sentence and tags the header", async () => {
  const { editor } = await setup(paint, "south");
  const tap = screen.getByRole("button", { name: /^点按/ });
  expect(tap.getAttribute("aria-expanded")).toBe("true");
  expect(within(tap).getByText("延迟 ≤250 ms")).toBeDefined();
  expect(screen.getByTestId("gesture-delay").textContent).toBe(
    "此按键还设置了长按，因此点按需等到松开（最多 250 ms）后才触发。",
  );
  const hold = screen.getByRole("button", { name: /^长按/ });
  expect(within(hold).getByText("延迟 250 ms")).toBeDefined();
  fireEvent.click(hold);
  expect(screen.getByTestId("gesture-delay").textContent).toBe(
    "此按键还设置了点按，因此需按住 250 ms 才判定为长按，并在此时触发。",
  );
  act(() => editor.getState().setGlobalTiming({ holdMs: 300 }));
  expect(screen.getByTestId("gesture-delay").textContent).toContain("300 ms");
});

it("names the haptic of a key in the validation results (MAP-25)", () => {
  const bad = {
    severity: "error" as const,
    pointer: "/layers/base/north/haptic",
    offset: 4,
    line: 3,
    col: 2,
    message: "is not a valid value",
  };
  expect(
    controlDiagnostics([bad], "north", "button", "north", "zh-CN"),
  ).toEqual([{ diagnostic: bad, where: "触感" }]);
  expect(controlDiagnostics([bad], "north", "button", "north", "en")).toEqual([
    { diagnostic: bad, where: "Haptic" },
  ]);
});

it("keeps the key's own timing, interruption policy, latch and block under 高级", async () => {
  const { editor } = await setup(paint, "south");
  fireEvent.click(screen.getByText("高级"));
  const hold = screen.getByRole("textbox", { name: "触发长按时间（ms）" });
  expect(hold.getAttribute("placeholder")).toBe("250");
  commit(hold, { target: { value: "450" } });
  expect(base(editor)?.south?.timing).toEqual({ holdMs: 450 });
  expect(screen.getByTestId("gesture-delay").textContent).toContain("450 ms");
  // A value out of range is not saved and the field says why.
  commit(hold, { target: { value: "1e" } });
  expect(base(editor)?.south?.timing).toEqual({ holdMs: 450 });
  expect(hold.getAttribute("aria-invalid")).toBe("true");
  expect(screen.getByRole("alert").textContent).toBe("请输入 0–60000 的整数");
  commit(hold, { target: { value: "" } });
  expect(base(editor)?.south?.timing).toBeUndefined();
  const policy = screen.getByRole("combobox", {
    name: "判定长按前按下其他按键时",
  });
  fireEvent.change(policy, { target: { value: "holdPreferred" } });
  expect(base(editor)?.south?.policy).toBe("holdPreferred");
  expect(
    within(policy).getByRole("option", { name: "偏向长按" }),
  ).toBeDefined();
  fireEvent.change(policy, { target: { value: "" } });
  expect(base(editor)?.south?.policy).toBeUndefined();
  const latch = screen.getByRole("switch", { name: "锁存" });
  fireEvent.click(latch);
  expect(base(editor)?.south?.latch).toBe(true);
  fireEvent.click(latch);
  expect(base(editor)?.south?.latch).toBeUndefined();
  expect(screen.queryByTestId("binding-block")).toBeNull();
});

it("sets the key's haptic under 高级 and takes the key out again for 无 (MAP-25)", async () => {
  const { editor, record } = await setup(paint, "south");
  fireEvent.click(screen.getByText("高级"));
  const haptic = screen.getByRole("combobox", { name: "触感" });
  expect((haptic as HTMLSelectElement).value).toBe("");
  fireEvent.change(haptic, { target: { value: "confirm" } });
  expect(base(editor)?.south?.haptic).toBe("confirm");
  expect(screen.getByText("已设置：触感")).toBeDefined();
  // The rest of the key is untouched, and a later edit of it keeps the haptic.
  expect(base(editor)?.south?.label).toBe("Undo");
  expect(base(editor)?.south?.tap).toEqual({ type: "keys", keys: "Ctrl+Z" });
  fireEvent.click(screen.getByRole("switch", { name: "锁存" }));
  expect(base(editor)?.south).toMatchObject({ haptic: "confirm", latch: true });
  await record("F9");
  expect(base(editor)?.south).toMatchObject({
    haptic: "confirm",
    tap: { type: "keys", keys: "F9" },
  });
  fireEvent.change(haptic, { target: { value: "" } });
  expect(base(editor)?.south).not.toHaveProperty("haptic");
  expect(base(editor)?.south?.latch).toBe(true);
});

it("shows a block from the file and offers gestures only after unblocking", async () => {
  const { editor, record } = await setup({
    ...paint,
    layers: { base: { north: { block: true } } },
  });
  expect(screen.getByTestId("binding-block").textContent).toContain(
    "屏蔽（在配置文件中设置）",
  );
  // A blocked binding cannot have actions, so nothing invites one.
  expect(screen.queryByRole("group", { name: "快捷键" })).toBeNull();
  expect(
    (screen.getByTestId("add-gesture") as HTMLButtonElement).disabled,
  ).toBe(true);
  expect(screen.getByText(/^此按键已屏蔽。/)).toBeDefined();
  fireEvent.click(screen.getByRole("button", { name: "取消屏蔽" }));
  expect(base(editor)?.north).toBeUndefined();
  await record("F9");
  expect(base(editor)?.north).toEqual({ press: { type: "keys", keys: "F9" } });
});

it("counts only lt's own script gestures as waits on lt", async () => {
  const { editor } = await setup(
    {
      ...paint,
      layers: { base: { lt: { tap: { type: "keys", keys: "F5" } } } },
    },
    "lefttrigger",
  );
  // A double tap on the half press is a different control in the engine.
  act(() =>
    editor.setState({
      registrations: [
        registration(1, "lefttrigger+", { gesture: "doubleTap" }),
      ],
    }),
  );
  // The drawer still says a script handles the trigger.
  expect(screen.getByTestId("script-ownership").textContent).toBe("S");
  const tap = screen.getByRole("button", { name: /^点按/ });
  expect(tap.querySelector(".drawer-delay-tag")).toBeNull();
  expect(screen.queryByTestId("gesture-delay")).toBeNull();
  act(() =>
    editor.setState({
      registrations: [registration(2, "lt", { gesture: "doubleTap" })],
    }),
  );
  expect(tap.querySelector(".drawer-delay-tag")).not.toBeNull();
  expect(screen.getByTestId("gesture-delay").textContent).toBe(
    "脚本还在此按键上使用了双击，因此点按后需再等待 220 ms，确认没有再次按下后才触发。",
  );
});

it("suggests a press on lt when a script only owns the half press", async () => {
  const { editor } = await setup(paint, "lefttrigger");
  act(() =>
    editor.setState({ registrations: [registration(1, "lefttrigger+")] }),
  );
  expect(screen.getByTestId("script-ownership").textContent).toBe("S");
  expect(screen.getByRole("group", { name: "快捷键" })).toBeDefined();
});

it("edits trigger gestures on lt with trigger settings in their own section", async () => {
  const { editor, record } = await setup(
    {
      ...paint,
      layers: { base: { lt: { press: { type: "keys", keys: "F5" } } } },
    },
    "lefttrigger",
  );
  expect(screen.getByRole("heading", { name: "LT 键" })).toBeDefined();
  expect(shortcutValue()).toBe("F5");
  expect(screen.getByText("扳机设置")).toBeDefined();
  await record("F6");
  expect(base(editor)?.lt?.press).toEqual({ type: "keys", keys: "F6" });
});

it("names d-pad directions and sticks in the header", async () => {
  const { rerender } = await setup(paint, "dpad.left");
  expect(screen.getByRole("heading", { name: "十字键 ←" })).toBeDefined();
  expect(screen.getByText("十字键设置")).toBeDefined();
  rerender("leftstick");
  expect(screen.getByRole("heading", { name: "左摇杆" })).toBeDefined();
  expect(screen.queryByRole("button", { name: "按键操作" })).toBeNull();
});

it("shows why the profile did not load instead of the not-a-library text", async () => {
  const api = editorAPIFixture();
  api.getProfile = vi.fn(async () => {
    throw new Error("profile file is locked");
  });
  window.padmate = api as unknown as PadmateAPI;
  const editor = createEditorStore(api as unknown as PadmateAPI);
  await editor
    .getState()
    .activate({ id: "paint", device: "d", connection: "c" });
  render(drawer(editor, "north"));
  const error = screen.getByTestId("drawer-load-error");
  expect(error.getAttribute("role")).toBe("alert");
  expect(error.textContent).toContain("配置载入失败");
  expect(error.textContent).toContain("profile file is locked");
  expect(screen.getByRole("complementary").textContent).not.toContain(
    "未使用配置库中的配置",
  );
});

it("shows a profile without the edited key group as a load error", async () => {
  const api = editorAPIFixture();
  api.getProfile = vi.fn(async (id: string) => ({
    id,
    text: JSON.stringify({ schemaVersion: 0, name: "x", layers: {} }),
    revision: "r1",
    diagnostics: [],
  }));
  window.padmate = api as unknown as PadmateAPI;
  const editor = createEditorStore(api as unknown as PadmateAPI);
  await editor
    .getState()
    .activate({ id: "paint", device: "d", connection: "c" });
  render(drawer(editor, "north"));
  expect(screen.getByRole("alert").textContent).toContain(
    "The profile does not contain a base layer",
  );
  expect(screen.queryByText(/未使用配置库中的配置/)).toBeNull();
});

it("says the mapping is not a library profile only when there is none", async () => {
  const editor = createEditorStore(editorAPIFixture() as unknown as PadmateAPI);
  await editor.getState().activate({ id: "", device: "d", connection: "c" });
  render(drawer(editor, "north"));
  expect(screen.getByText(/未使用配置库中的配置/)).toBeDefined();
  expect(screen.queryByRole("alert")).toBeNull();
});

it("moves focus into the drawer when it opens, not on every switch", async () => {
  const outside = document.createElement("button");
  outside.textContent = "chip";
  document.body.append(outside);
  try {
    const { editor, rerender } = await setup();
    // Opening lands on the key's heading.
    expect(document.activeElement).toBe(
      screen.getByRole("heading", { name: "Y 键" }),
    );
    // Switching keys from a chip keeps focus on that chip.
    outside.focus();
    rerender("south");
    expect(document.activeElement).toBe(outside);
    // A re-render of the same key does not move focus either.
    act(() => editor.setState({ diagnostics: [], registrations: [] }));
    expect(document.activeElement).toBe(outside);
    // A switch that dropped focus to <body> brings it into the drawer.
    outside.blur();
    rerender("west");
    expect(document.activeElement).toBe(
      screen.getByRole("heading", { name: "X 键" }),
    );
  } finally {
    outside.remove();
  }
});

it("puts the profile name in its own ellipsis span with the full name as title", async () => {
  const { editor } = await setup();
  act(() => editor.setState({ registrations: [registration(1, "north")] }));
  const text = screen.getByText("配置「Paint」");
  expect(text.className).toBe("drawer-subtitle-text");
  expect(text.getAttribute("title")).toBe("配置「Paint」");
  expect(
    text.parentElement?.querySelector(".drawer-script-chip"),
  ).not.toBeNull();
});

it("links the script notice to the scripts page when it can navigate", async () => {
  const onShowScripts = vi.fn();
  const { editor } = await setup(paint, "north", "zh-CN", { onShowScripts });
  act(() => editor.setState({ registrations: [registration(1, "north")] }));
  fireEvent.click(screen.getByRole("button", { name: "前往脚本页" }));
  expect(onShowScripts).toHaveBeenCalledOnce();
  cleanup();
  const other = await setup(paint, "north");
  act(() =>
    other.editor.setState({ registrations: [registration(1, "north")] }),
  );
  expect(screen.getByTestId("script-ownership")).toBeDefined();
  expect(screen.queryByRole("button", { name: "前往脚本页" })).toBeNull();
});

it("opens the key actions menu on a menu request", async () => {
  const { rerender } = await setup();
  expect(screen.queryByRole("menu")).toBeNull();
  rerender("north", { menuRequest: 1 });
  const menu = await screen.findByRole("menu", { name: "按键操作" });
  expect(
    within(menu)
      .getAllByRole("menuitem")
      .map((item) => item.textContent),
  ).toEqual(["复制此按键的设置", "粘贴…", "重命名按键…", "清空此按键…"]);
  // The menu keeps focus; the drawer's own focus move does not take it.
  expect(menu.contains(document.activeElement)).toBe(true);
  expect(screen.getByTestId("key-actions").getAttribute("aria-expanded")).toBe(
    "true",
  );
});

it("opens a new drawer with its menu when it mounts with a request", async () => {
  await setup(paint, "south", "zh-CN", { menuRequest: 3 });
  const menu = await screen.findByRole("menu", { name: "按键操作" });
  expect(menu.contains(document.activeElement)).toBe(true);
});

it("lets a read-only stick drawer open its directions", async () => {
  await setup(
    {
      ...paint,
      defaultLayer: "alt",
      layers: {
        base: { "ls.up": { press: { type: "keys", keys: "W" } } },
        alt: {},
      },
    },
    "leftstick",
  );
  const drawerRoot = screen.getByRole("complementary");
  // No fieldset around the whole panel disables the direction cells.
  const up = drawerRoot.querySelector<HTMLButtonElement>(
    'button[data-control="ls.up"]',
  );
  expect(up).not.toBeNull();
  expect(up?.matches(":disabled")).toBe(false);
});

it("offers no 自动 and no example label in the key and stick drawers (F7)", async () => {
  const profile: Profile = {
    ...paint,
    layers: {
      base: {
        ...paint.layers.base,
        west: { press: { type: "keys", keys: "" } },
        "ls.left": { press: { type: "keys", keys: "", mode: "tap" } },
      },
    },
  };
  const { rerender } = await setup(profile, "west");
  expect(screen.queryByRole("button", { name: "自动" })).toBeNull();
  expect(screen.getByRole("group", { name: "快捷键" })).toBeDefined();
  rerender("east");
  // A key without gestures has no example label to suggest.
  const label = screen.getByRole("textbox", { name: "在手柄图上显示为" });
  expect(label.getAttribute("placeholder")).toBeNull();
  rerender("leftstick");
  const drawerRoot = screen.getByRole("complementary");
  fireEvent.click(
    drawerRoot.querySelector('button[data-control="ls.left"]') as HTMLElement,
  );
  expect(
    within(drawerRoot).getByRole("group", { name: "快捷键" }),
  ).toBeDefined();
  expect(within(drawerRoot).queryByRole("button", { name: "自动" })).toBeNull();
});

it("saves a capture once, as one undo step, however many keys it saw", async () => {
  const { api, editor } = await setup();
  const listeners: ((event: KeyboardCaptureEvent) => void)[] = [];
  api.onCapture = vi.fn((listener: (event: KeyboardCaptureEvent) => void) => {
    listeners.push(listener);
    return () => listeners.splice(listeners.indexOf(listener), 1);
  }) as unknown as typeof api.onCapture;
  fireEvent.click(screen.getByRole("button", { name: "录入快捷键" }));
  await waitFor(() => expect(api.captureStart).toHaveBeenCalledOnce());
  // Each key going down sends the chord so far.
  for (const chord of ["Ctrl", "Ctrl+Shift", "Ctrl+Shift+Z"])
    act(() => {
      for (const listener of [...listeners])
        listener({ session: 1, active: true, chord });
    });
  expect(api.putProfile).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "停止捕获" }));
  await act(() => editor.getState().flush());
  expect(api.putProfile).toHaveBeenCalledOnce();
  expect(base(editor)?.north).toEqual({
    press: { type: "keys", keys: "Ctrl+Shift+Z" },
  });
  act(() => {
    editor.getState().undo();
  });
  expect(base(editor)?.north).toEqual({ press: { type: "keys", keys: "F13" } });
  expect(editor.getState().canUndo).toBe(false);
});

it("saves a wheel key picked from the 鼠标 menu once, as one undo step (ADR-0062)", async () => {
  const { api, editor, record } = await setup();
  const sent = (call: number) =>
    (
      JSON.parse(
        (api.putProfile.mock.calls[call] as unknown[])[1] as string,
      ) as Profile
    ).layers.base.north?.press;
  const pick = (item: string) => {
    fireEvent.click(screen.getByRole("button", { name: "鼠标" }));
    fireEvent.click(screen.getByRole("menuitem", { name: item }));
  };
  // A key that is not a modifier goes: only the wheel key is left.
  pick("滚轮向上");
  expect(base(editor)?.north).toEqual({
    press: { type: "keys", keys: "WheelUp" },
  });
  expect(editor.getState().unsaved).toBe(true);
  await act(() => editor.getState().flush());
  expect(api.putProfile).toHaveBeenCalledOnce();
  expect(sent(0)).toEqual({ type: "keys", keys: "WheelUp" });
  // The modifiers of a recorded chord stay; each pick is one undo step.
  await record("Ctrl+Z");
  pick("滚轮向下");
  expect(base(editor)?.north?.press).toMatchObject({ keys: "Ctrl+WheelDown" });
  act(() => {
    editor.getState().undo();
  });
  expect(base(editor)?.north?.press).toMatchObject({ keys: "Ctrl+Z" });
  expect(shortcutValue()).toBe("Ctrl+Z");
  act(() => {
    editor.getState().undo();
  });
  act(() => {
    editor.getState().undo();
  });
  expect(base(editor)?.north?.press).toEqual({ type: "keys", keys: "F13" });
  expect(editor.getState().canUndo).toBe(false);
  act(() => {
    editor.getState().redo();
  });
  expect(base(editor)?.north?.press).toMatchObject({ keys: "WheelUp" });
  expect(shortcutValue()).toBe("WheelUp");
  await act(() => editor.getState().flush());
  expect(sent(api.putProfile.mock.calls.length - 1)).toEqual({
    type: "keys",
    keys: "WheelUp",
  });
});

it("saves a mouse button and its repeat as clicks, and undoes each step (ADR-0062)", async () => {
  const { api, editor } = await setup();
  const sent = (call: number) =>
    (
      JSON.parse(
        (api.putProfile.mock.calls[call] as unknown[])[1] as string,
      ) as Profile
    ).layers.base.north?.press;
  const pick = (item: string) => {
    fireEvent.click(screen.getByRole("button", { name: "鼠标" }));
    fireEvent.click(screen.getByRole("menuitem", { name: item }));
  };
  pick("右键");
  expect(base(editor)?.north?.press).toEqual({
    type: "keys",
    keys: "MouseRight",
  });
  await act(() => editor.getState().flush());
  expect(sent(0)).toEqual({ type: "keys", keys: "MouseRight" });
  // A held button is never pressed again and again: the repeat is clicks,
  // and the file says so, in one edit.
  fireEvent.click(screen.getByRole("switch", { name: "连发" }));
  expect(base(editor)?.north?.press).toEqual({
    type: "keys",
    keys: "MouseRight",
    mode: "tap",
    repeat: { style: "tap", delayMs: 350, rateHz: 12 },
  });
  await act(() => editor.getState().flush());
  expect(sent(api.putProfile.mock.calls.length - 1)).toEqual({
    type: "keys",
    keys: "MouseRight",
    mode: "tap",
    repeat: { style: "tap", delayMs: 350, rateHz: 12 },
  });
  act(() => {
    editor.getState().undo();
  });
  expect(base(editor)?.north?.press).toEqual({
    type: "keys",
    keys: "MouseRight",
  });
  act(() => {
    editor.getState().undo();
  });
  expect(base(editor)?.north?.press).toEqual({ type: "keys", keys: "F13" });
  expect(editor.getState().canUndo).toBe(false);
});

const triggers: Profile = {
  schemaVersion: 1,
  name: "Triggers",
  layers: {
    base: {
      lt: { press: { type: "keys", keys: "B", mode: "tap" } },
      "lt.full": {
        press: {
          type: "keys",
          keys: "E",
          mode: "tap",
          repeat: { rateHz: 12, minRateHz: 3 },
        },
      },
    },
  },
};

it("counts the second stage's script with the trigger's (ANA-14)", async () => {
  const { editor } = await setup(triggers, "lefttrigger");
  act(() =>
    editor.setState({
      registrations: [
        registration(1, "lt.full", { label: "Eraser" }),
        registration(2, "rt.full", { label: "Other trigger" }),
      ],
    }),
  );
  expect(screen.getByTestId("script-ownership").textContent).toBe("S");
  expect(document.querySelector(".drawer-script-chip")).not.toBeNull();
  const rows = screen.getByRole("list", { name: "脚本如何使用此按键" });
  expect(rows.textContent).toContain("Eraser");
  expect(rows.textContent).not.toContain("Other trigger");
  // The row of the second stage in the panel is marked too.
  const full = document.querySelector('.analog-row[data-control="lt.full"]');
  expect(
    full?.querySelector('[data-testid="analog-script-ownership"]'),
  ).not.toBeNull();
});

it("lists the validation results of the second stage and the rings with their drawer", async () => {
  const problem = (pointer: string, message: string) => ({
    severity: "error" as const,
    pointer,
    offset: 1,
    line: 1,
    col: 1,
    message,
  });
  const diagnostics = [
    problem("/layers/base/lt.full/press/keys", "bad full"),
    problem("/layers/base/rt.full/press/keys", "other trigger"),
    problem("/analog/lt/fullRelease", "fullRelease must not exceed fullPress"),
    problem("/layers/base/ls.outer/press/keys", "bad ring"),
    problem("/layers/base/rs.cw/press/keys", "other stick"),
    problem("/layers/base/ls.ccw/tap/keys", "bad tick"),
  ];
  const { editor } = await setup(triggers, "lefttrigger");
  act(() => editor.setState({ diagnostics }));
  expect(
    screen.getAllByTestId("control-diagnostic").map((item) => item.textContent),
  ).toEqual([
    "错误 · 按到底 按下：bad full",
    "错误 · 扳机设置：fullRelease must not exceed fullPress",
  ]);
  cleanup();
  const stick = await setup(triggers, "leftstick");
  act(() => stick.editor.setState({ diagnostics }));
  expect(
    screen.getAllByTestId("control-diagnostic").map((item) => item.textContent),
  ).toEqual(["错误 · 重推 按下：bad ring", "错误 · 逆时针一格 点按：bad tick"]);
});

it("offers a rate that follows the push depth on a trigger's keys, and keeps it off other keys", async () => {
  const { editor, rerender } = await setup(triggers, "lefttrigger");
  // The first stage's own key, in the drawer's gesture list.
  fireEvent.click(screen.getByRole("switch", { name: "连发" }));
  expect(
    screen
      .getByRole("switch", { name: "推得越深越快" })
      .getAttribute("aria-checked"),
  ).toBe("false");
  fireEvent.click(screen.getByRole("switch", { name: "推得越深越快" }));
  expect(base(editor)?.lt?.press).toMatchObject({
    repeat: { rateHz: 12, minRateHz: 3 },
  });
  // The second stage's key, in the panel, has it too.
  fireEvent.click(
    document.querySelector('.analog-row[data-control="lt.full"]') as Element,
  );
  const card = screen.getByRole("region", { name: "按到底" });
  expect(
    within(card)
      .getByRole("switch", { name: "推得越深越快" })
      .getAttribute("aria-checked"),
  ).toBe("true");
  expect(
    within(card).getByRole("textbox", { name: "最慢（每秒）" }),
  ).toBeDefined();
  // A button has no depth.
  rerender("north");
  fireEvent.click(screen.getByRole("switch", { name: "连发" }));
  expect(screen.queryByRole("switch", { name: "推得越深越快" })).toBeNull();
  expect(screen.getByRole("textbox", { name: "每秒次数" })).toBeDefined();
});

it("takes the rate that follows the push depth out of a key pasted onto a key without depth", async () => {
  const { editor, rerender } = await setup(triggers, "lefttrigger");
  // Copy the first stage's key with a rate range, paste it on a button.
  act(() =>
    editor.getState().edit({
      ...(editor.getState().draft as Profile),
      layers: {
        base: {
          ...(editor.getState().draft as Profile).layers.base,
          lt: {
            press: {
              type: "keys",
              keys: "B",
              mode: "tap",
              repeat: { rateHz: 12, minRateHz: 3 },
            },
          },
        },
      },
    }),
  );
  fireEvent.click(screen.getByRole("button", { name: "按键操作" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "复制此按键的设置" }));
  rerender("north");
  fireEvent.click(screen.getByRole("button", { name: "按键操作" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "粘贴…" }));
  fireEvent.click(screen.getByRole("button", { name: "粘贴" }));
  expect(base(editor)?.north).toEqual({
    press: {
      type: "keys",
      keys: "B",
      mode: "tap",
      repeat: { rateHz: 12 },
    },
  });
  // Onto the other trigger it keeps its range.
  rerender("righttrigger");
  fireEvent.click(screen.getByRole("button", { name: "按键操作" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "粘贴…" }));
  fireEvent.click(screen.getByRole("button", { name: "粘贴" }));
  expect(base(editor)?.rt?.press).toMatchObject({
    repeat: { rateHz: 12, minRateHz: 3 },
  });
});
