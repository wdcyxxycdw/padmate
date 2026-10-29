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

// The key drawer of a hand-written profile file that extends another (PRO-19,
// ADR-0070, ADR-0076): what it takes from the parent is shown as plain
// bindings, with no mark and no way to give a key back; the first edit makes
// the key the profile's own.

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
  functionSet: { south: { press: { type: "keys", keys: "F1" } } },
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
    view?: "function";
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
    inherited: [],
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
/** Anything in the drawer that says a key is inherited, or gives it back. */
const inheritanceTraces = () => [
  screen.queryByTestId("drawer-parent"),
  ...screen.queryAllByText(/继承|Inherited/),
];
const actions = () => screen.getByTestId("key-actions");
const openMenu = () => fireEvent.click(actions());
const menuItem = (name: string) =>
  screen.queryByRole("menuitem", { name }) as HTMLButtonElement | null;
const flush = (editor: EditorStore) => act(() => editor.getState().flush());

it("shows the parent's gestures as the key's own, without a word about it", async () => {
  await setup("south");
  expect(inheritanceTraces().filter(Boolean)).toEqual([]);
  // The parent's binding, label and gestures.
  expect(
    (
      screen.getByRole("textbox", {
        name: "功能",
      }) as HTMLInputElement
    ).value,
  ).toBe("撤销");
  expect(screen.getByRole("button", { name: /^点按/ })).toBeDefined();
  expect(screen.getByRole("button", { name: /^长按/ })).toBeDefined();
  // And the parent's name for the key.
  expect(screen.getByRole("heading", { name: "撤销键" })).toBeDefined();
  expect(shortcutValue()).toBe("Ctrl+Z");
});

it("says nothing of it in English either, and has no way to give the key back", async () => {
  await setup("south", { language: "en" });
  expect(inheritanceTraces().filter(Boolean)).toEqual([]);
  openMenu();
  expect(menuItem("Restore inherited")).toBeNull();
});

it("offers no way to give a key back, whoever wrote it", async () => {
  for (const control of ["south", "east", "north", "west"]) {
    await setup(control);
    expect(inheritanceTraces().filter(Boolean)).toEqual([]);
    openMenu();
    expect(menuItem("恢复为继承")).toBeNull();
    cleanup();
  }
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
  expect(saved).not.toHaveProperty("functionSet");
});

it("shows a key the profile overrides as its own binding", async () => {
  await setup("east");
  expect(shortcutValue()).toBe("E");
  expect(inheritanceTraces().filter(Boolean)).toEqual([]);
});

it("clears an inherited key by leaving it empty, and nothing gives it back", async () => {
  const { editor } = await setup("south");
  openMenu();
  fireEvent.click(menuItem("清空此按键…") as HTMLButtonElement);
  fireEvent.click(screen.getByRole("button", { name: "清空" }));
  // The profile says the key does nothing: an empty binding, not nothing.
  expect(base(editor)?.south).toEqual({});
  expect(screen.queryByRole("button", { name: /^点按/ })).toBeNull();
  openMenu();
  expect(menuItem("恢复为继承")).toBeNull();
  // It is one step, and undone as one.
  act(() => {
    editor.getState().undo();
  });
  expect(base(editor)).not.toHaveProperty("south");
  expect(shortcutValue()).toBe("Ctrl+Z");
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
  fireEvent.click(screen.getByRole("button", { name: "删除此映射" }));
  expect(base(editor)?.east).toEqual({});
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
});

it("shows a stick whose directions come from the parent and the profile alike as plain bindings", async () => {
  await setup("leftstick");
  expect(inheritanceTraces().filter(Boolean)).toEqual([]);
  // A stick has no key menu to give its directions back with.
  expect(screen.queryByTestId("key-actions")).toBeNull();
});

it("shows a stick all of whose bindings the parent wrote as plain bindings", async () => {
  await setup("leftstick", {
    child: { ...sketchProfile, layers: { base: {} } },
  });
  expect(inheritanceTraces().filter(Boolean)).toEqual([]);
});

it("reads the function set in its own view", async () => {
  const { editor, record } = await setup("south", { view: "function" });
  expect(shortcutValue()).toBe("F1");
  await record("F2");
  expect(editor.getState().draft?.functionSet).toEqual({
    south: { press: { type: "keys", keys: "F2" } },
  });
  // The usual bindings are not touched.
  expect(base(editor)).not.toHaveProperty("south");
});

it("has no trace of inheritance without a parent for the profile", async () => {
  const { extends: _extends, ...alone } = sketchProfile;
  await setup("south", { child: alone as Profile });
  expect(inheritanceTraces().filter(Boolean)).toEqual([]);
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
  expect(shortcutValue()).toBe("Ctrl+Y");
  fireEvent.click(screen.getByRole("button", { name: "清除快捷键" }));
  // The drawer reads the draft: the key is not set.
  expect(shortcutValue()).toBe("");
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
