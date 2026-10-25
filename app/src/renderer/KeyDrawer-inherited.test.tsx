// @vitest-environment jsdom
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import type { DeviceInfo, PadmateAPI, ProfileEntry } from "../shared/api";
import type { Profile } from "../shared/generated/schema";
import { fakeCapture, shortcutValue } from "./capture-fixture";
import { editorAPIFixture } from "./editor-api-fixture";
import { createEditorStore, type EditorStore } from "./editor-store";
import { KeyDrawer } from "./KeyDrawer";
import type { Language } from "./store";

// The key drawer of a profile that is based on another (PRO-19, ADR-0070):
// what it inherits is shown as the parent's, the first edit makes it the
// profile's own, and 恢复为继承 gives the key back to the parent.

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

const parentProfile: Profile = {
  schemaVersion: 1,
  name: "Paint",
  controlNames: { south: "撤销键" },
  layers: {
    base: {
      south: {
        label: "撤销",
        tap: { type: "keys", keys: "Ctrl+Z" },
        hold: { type: "keys", keys: "Ctrl+Shift+Z" },
      },
      east: { press: { type: "keys", keys: "Ctrl+Y" } },
      "ls.up": { press: { type: "keys", keys: "W" } },
      "ls.down": { press: { type: "keys", keys: "S" } },
    },
  },
  functionKeys: { back: { south: { press: { type: "keys", keys: "F1" } } } },
};
const sketchProfile: Profile = {
  schemaVersion: 1,
  name: "Sketch",
  extends: "paint",
  layers: {
    base: {
      east: { press: { type: "keys", keys: "E" } },
      north: { press: { type: "keys", keys: "N" } },
      "ls.down": { press: { type: "keys", keys: "Down" } },
    },
  },
};
const library: ProfileEntry[] = [
  { id: "paint", name: "Paint" },
  { id: "sketch", name: "Sketch", extends: "paint" },
];

async function setup(
  control: string,
  options: {
    child?: Profile;
    parent?: Profile;
    language?: Language;
    origins?: { pointer: string; profile: string }[];
    view?: "back";
  } = {},
) {
  const api = editorAPIFixture();
  api.listProfiles.mockResolvedValue(library);
  api.getProfile.mockImplementation(async (id: string) => ({
    id,
    text: JSON.stringify(
      id === "paint"
        ? (options.parent ?? parentProfile)
        : (options.child ?? sketchProfile),
    ),
    revision: "r1",
    diagnostics: [],
  }));
  api.resolveProfile.mockImplementation(async (id: string) => ({
    id,
    text: JSON.stringify(options.parent ?? parentProfile),
    revision: "r1",
    inherited: options.origins ?? [],
    diagnostics: [],
  }));
  const { record } = fakeCapture(api);
  const editor = createEditorStore(api as unknown as PadmateAPI);
  await editor
    .getState()
    .activate({ id: "sketch", device: "d", connection: "c" });
  await act(async () => {
    await editor.getState().refreshMetadata();
    await editor.getState().refreshParent();
  });
  if (options.view) editor.getState().setView(options.view);
  const language = options.language ?? "zh-CN";
  const view = render(
    <KeyDrawer
      editor={editor}
      control={control}
      onControl={() => {}}
      info={info}
      language={language}
      onClose={() => {}}
    />,
  );
  return { api, editor, record, view };
}

const base = (editor: EditorStore) => editor.getState().draft?.layers.base;
const note = () => screen.queryByTestId("drawer-parent");
const actions = () => screen.getByTestId("key-actions");
const openMenu = () => fireEvent.click(actions());
const menuItem = (name: string) =>
  screen.queryByRole("menuitem", { name }) as HTMLButtonElement | null;
const flush = (editor: EditorStore) => act(() => editor.getState().flush());

it("says a key is inherited, and shows the parent's gestures as they are", async () => {
  await setup("south");
  expect(note()?.textContent).toBe("继承自 Paint");
  // The parent's binding, label and gestures.
  expect(
    (
      screen.getByRole("textbox", {
        name: "在手柄图上显示为",
      }) as HTMLInputElement
    ).value,
  ).toBe("撤销");
  expect(screen.getByRole("button", { name: /^点按/ })).toBeDefined();
  expect(screen.getByRole("button", { name: /^长按/ })).toBeDefined();
  // And the parent's name for the key.
  expect(screen.getByRole("heading", { name: "撤销键" })).toBeDefined();
  expect(shortcutValue()).toBe("Ctrl+Z");
});

it("says it in English", async () => {
  await setup("south", { language: "en" });
  expect(note()?.textContent).toBe("Inherited from Paint");
  openMenu();
  expect(menuItem("Restore inherited")).toBeNull();
});

it("says nothing of a key the profile binds itself", async () => {
  await setup("north");
  expect(note()).toBeNull();
  openMenu();
  expect(menuItem("恢复为继承")).toBeNull();
});

it("says nothing of a key neither binds", async () => {
  await setup("west");
  expect(note()).toBeNull();
  openMenu();
  expect(menuItem("恢复为继承")).toBeNull();
});

it("names the profile that wrote the key when it is the parent's own parent", async () => {
  await setup("south", {
    origins: [{ pointer: "/layers/base/south", profile: "default" }],
  });
  expect(note()?.textContent).toBe("继承自 默认");
});

it("makes a key the profile's own with the first edit, taking the whole binding with it", async () => {
  const { editor, record } = await setup("south");
  expect(base(editor)?.south).toBeUndefined();
  await record("Ctrl+Alt+Z");
  // The binding is written whole: the label and the hold come with the edit.
  expect(base(editor)?.south).toEqual({
    label: "撤销",
    tap: { type: "keys", keys: "Ctrl+Alt+Z" },
    hold: { type: "keys", keys: "Ctrl+Shift+Z" },
  });
  // Nothing else of the parent's went into the profile.
  expect(Object.keys(base(editor) ?? {})).toEqual([
    "east",
    "north",
    "ls.down",
    "south",
  ]);
  expect(editor.getState().draft?.controlNames).toBeUndefined();
  expect(note()).toBeNull();
  // It is a step that is taken back.
  expect(editor.getState().canUndo).toBe(true);
});

it("saves the edit of an inherited key in the profile's own file", async () => {
  const { api, editor, record } = await setup("south");
  await record("F5");
  await flush(editor);
  const text = (api.putProfile.mock.calls.at(-1) as unknown[])[1] as string;
  const saved = JSON.parse(text) as Profile;
  expect(saved.extends).toBe("paint");
  expect(saved.layers.base.south).toMatchObject({
    tap: { type: "keys", keys: "F5" },
  });
  expect(saved.layers.base.east).toEqual({
    press: { type: "keys", keys: "E" },
  });
  expect(saved).not.toHaveProperty("controlNames");
  expect(saved).not.toHaveProperty("functionKeys");
});

it("offers 恢复为继承 for a key the profile overrides, and the parent's binding is back", async () => {
  const { editor } = await setup("east");
  // Both bind east: no note, but the menu can give it back.
  expect(note()).toBeNull();
  expect(shortcutValue()).toBe("E");
  openMenu();
  const restore = menuItem("恢复为继承");
  expect(restore).not.toBeNull();
  expect(restore?.disabled).toBe(false);
  fireEvent.click(restore as HTMLButtonElement);
  // One step: the control leaves the profile's own file ...
  expect(base(editor)).not.toHaveProperty("east");
  expect(Object.keys(base(editor) ?? {})).toEqual(["north", "ls.down"]);
  // ... and the key is the parent's again.
  expect(note()?.textContent).toBe("继承自 Paint");
  expect(shortcutValue()).toBe("Ctrl+Y");
  // The menu no longer offers it.
  openMenu();
  expect(menuItem("恢复为继承")).toBeNull();
  // And it is undone as one step.
  act(() => {
    editor.getState().undo();
  });
  expect(base(editor)?.east).toEqual({ press: { type: "keys", keys: "E" } });
});

it("says in English too", async () => {
  await setup("east", { language: "en" });
  openMenu();
  expect(menuItem("Restore inherited")).not.toBeNull();
});

it("clears an inherited key by leaving it empty, which 恢复为继承 undoes", async () => {
  const { editor } = await setup("south");
  openMenu();
  fireEvent.click(menuItem("清空此按键…") as HTMLButtonElement);
  fireEvent.click(screen.getByRole("button", { name: "清空" }));
  // The profile says the key does nothing: an empty binding, not nothing.
  expect(base(editor)?.south).toEqual({});
  expect(note()).toBeNull();
  expect(screen.queryByRole("button", { name: /^点按/ })).toBeNull();
  openMenu();
  fireEvent.click(menuItem("恢复为继承") as HTMLButtonElement);
  expect(base(editor)).not.toHaveProperty("south");
  expect(note()?.textContent).toBe("继承自 Paint");
});

it("clears a key that only the profile binds by taking it out", async () => {
  const { editor } = await setup("north");
  openMenu();
  fireEvent.click(menuItem("清空此按键…") as HTMLButtonElement);
  fireEvent.click(screen.getByRole("button", { name: "清空" }));
  expect(base(editor)).not.toHaveProperty("north");
});

it("removes the last gesture of an inherited key by leaving the key empty", async () => {
  const { editor } = await setup("east");
  // east is overridden; remove its only gesture: the profile still writes
  // the key, empty, so the parent's does not come back.
  fireEvent.click(screen.getByRole("button", { name: "删除此手势" }));
  expect(base(editor)?.east).toEqual({});
  openMenu();
  expect(menuItem("恢复为继承")).not.toBeNull();
});

it("copies the settings the key has, whoever wrote them", async () => {
  await setup("south");
  openMenu();
  fireEvent.click(menuItem("复制此按键的设置") as HTMLButtonElement);
  expect(screen.getByRole("status").textContent).toContain("已复制");
});

it("names the key as the profile does, and the parent's name stands where it has none", async () => {
  const { editor } = await setup("south");
  expect(screen.getByRole("heading", { name: "撤销键" })).toBeDefined();
  fireEvent.click(screen.getByRole("button", { name: "重命名此按键" }));
  const name = screen.getByRole("textbox", { name: "按键名称" });
  fireEvent.change(name, { target: { value: "回退" } });
  fireEvent.keyDown(name, { key: "Enter" });
  // Only the name is written; the binding stays the parent's.
  expect(editor.getState().draft?.controlNames).toEqual({ south: "回退" });
  expect(base(editor)).not.toHaveProperty("south");
  expect(note()?.textContent).toBe("继承自 Paint");
});

it("shows a stick whose directions the profile takes from its parent, and the one it overrides", async () => {
  const { editor } = await setup("leftstick");
  // ls.up is the parent's, ls.down the profile's own: part of the stick is
  // the profile's, so there is no single "inherited from".
  expect(note()).toBeNull();
  openMenu();
  const restore = menuItem("恢复为继承");
  expect(restore).not.toBeNull();
  fireEvent.click(restore as HTMLButtonElement);
  expect(base(editor)).not.toHaveProperty("ls.down");
  expect(note()?.textContent).toBe("继承自 Paint");
});

it("shows a stick all of whose bindings the parent wrote as inherited", async () => {
  await setup("leftstick", {
    child: { ...sketchProfile, layers: { base: {} } },
  });
  expect(note()?.textContent).toBe("继承自 Paint");
});

it("reads the set of a function key in its own view", async () => {
  const { editor, record } = await setup("south", { view: "back" });
  expect(note()?.textContent).toBe("继承自 Paint");
  expect(shortcutValue()).toBe("F1");
  await record("F2");
  expect(editor.getState().draft?.functionKeys).toEqual({
    back: { south: { press: { type: "keys", keys: "F2" } } },
  });
  // The usual bindings are not touched.
  expect(base(editor)).not.toHaveProperty("south");
});

it("has no note without a parent for the profile", async () => {
  const { extends: _extends, ...alone } = sketchProfile;
  await setup("south", { child: alone as Profile });
  expect(note()).toBeNull();
  expect(
    within(screen.getByTestId("mapping-editor")).queryByText(/继承/),
  ).toBeNull();
});

it("clears the shortcut of an inherited key: the key is the profile's own, empty, and that is what is saved", async () => {
  const { api, editor } = await setup("east", {
    child: {
      ...sketchProfile,
      layers: { base: { north: { press: { type: "keys", keys: "N" } } } },
    },
  });
  expect(note()?.textContent).toBe("继承自 Paint");
  expect(shortcutValue()).toBe("Ctrl+Y");
  fireEvent.click(screen.getByRole("button", { name: "清除快捷键" }));
  // The drawer reads the draft: the key is not set, and can be given back.
  expect(shortcutValue()).toBe("");
  expect(note()).toBeNull();
  openMenu();
  expect(menuItem("恢复为继承")).not.toBeNull();
  // The file says the key does nothing here. Without that, the file would
  // have no east and the daemon would send the parent's Ctrl+Y.
  await flush(editor);
  const text = (api.putProfile.mock.calls.at(-1) as unknown[])[1] as string;
  expect((JSON.parse(text) as Profile).layers.base).toEqual({
    north: { press: { type: "keys", keys: "N" } },
    east: {},
  });
  expect(editor.getState().unsaved).toBe(false);
});

it("shows the shared (common) setting a profile inherits from its parent", async () => {
  await setup("west", {
    parent: {
      ...parentProfile,
      common: { west: { press: { type: "keys", keys: "W" } } },
    },
  });
  // Neither profile binds west in a layer; the parent shares one in common.
  expect(screen.getByTestId("drawer-common-notice").textContent).toContain(
    "各组共用",
  );
});
