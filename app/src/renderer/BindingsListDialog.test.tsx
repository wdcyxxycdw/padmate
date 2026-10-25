// @vitest-environment jsdom
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import type { DeviceInfo, PadmateAPI } from "../shared/api";
import type { Profile } from "../shared/generated/schema";
import { BindingsListDialog, listControlName } from "./BindingsListDialog";
import { controllerLabels } from "./controller-model";
import { editorAPIFixture } from "./editor-api-fixture";
import { createEditorStore, type EditorStore } from "./editor-store";

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

const keys = (keys: string) => ({ press: { type: "keys" as const, keys } });
const profile: Profile = {
  schemaVersion: 1,
  name: "绘画",
  layers: {
    base: {
      south: keys("Ctrl+Z"),
      "ls.up": keys("W"),
      "leftx+": keys("D"),
      "lefttrigger+": keys("B"),
      rt: keys("E"),
      "dpad.upleft": keys("Home"),
    },
  },
};

async function editorFor(value: Profile, id = "paint") {
  const api = editorAPIFixture();
  api.getProfile = vi.fn(async (id: string) => ({
    id,
    text: JSON.stringify(value),
    revision: "r1",
    diagnostics: [],
  }));
  const editor = createEditorStore(api as unknown as PadmateAPI);
  await editor.getState().activate({ id, device: "d", connection: "c" });
  return editor;
}

function Harness({
  editor,
  onSelect,
  drawer = false,
}: {
  editor: EditorStore;
  onSelect: (control: string) => void;
  drawer?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState("");
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        列表
      </button>
      {["south", "leftstick", "lefttrigger", "righttrigger", "dpad.up"].map(
        (control) => (
          <button
            type="button"
            key={control}
            data-callout={control}
            aria-pressed={selected === control}
          >
            {control}
          </button>
        ),
      )}
      {drawer && selected && (
        <aside data-testid="mapping-editor">
          <h2 className="drawer-title" tabIndex={-1}>
            {selected}
          </h2>
        </aside>
      )}
      <BindingsListDialog
        open={open}
        editor={editor}
        info={info}
        language="zh-CN"
        onClose={() => setOpen(false)}
        onSelect={(control) => {
          onSelect(control);
          setOpen(false);
          setSelected(control);
        }}
      />
    </>
  );
}

it("names stick directions, raw axes and half presses", () => {
  const labels = controllerLabels("xboxone");
  expect(listControlName("ls.up", labels, "zh-CN")).toBe("LS ↑");
  expect(listControlName("rs.downleft", labels, "zh-CN")).toBe("RS ↙");
  expect(listControlName("leftx+", labels, "zh-CN")).toBe("LS 原始轴向右");
  expect(listControlName("lefttrigger+", labels, "zh-CN")).toBe("LT 半按");
  expect(listControlName("lt.full", labels, "zh-CN")).toBe("LT 按到底");
  expect(listControlName("rt.full", labels, "en")).toBe("RT full pull");
  expect(listControlName("ls.inner", labels, "zh-CN")).toBe("LS 轻推");
  expect(listControlName("rs.outer", labels, "en")).toBe("RS hard push");
  expect(listControlName("ls.cw", labels, "zh-CN")).toBe("LS 顺时针一格");
  expect(listControlName("rs.ccw", labels, "en")).toBe(
    "RS counter-clockwise tick",
  );
  expect(listControlName("lefttrigger+", labels, "en")).toBe("LT Half press");
  expect(listControlName("rt", labels, "zh-CN")).toBe("RT");
  expect(listControlName("dpad.upleft", labels, "zh-CN")).toBe("十字键 ↖");
  expect(listControlName("leftstick", labels, "zh-CN")).toBe("LS");
  expect(listControlName("a", labels, "zh-CN")).toBe("A");
});

it("names the fixed default by its ID (PRO-28)", async () => {
  const editor = await editorFor({ ...profile, name: "Stored" }, "default");
  render(<Harness editor={editor} onSelect={vi.fn()} />);
  fireEvent.click(screen.getByRole("button", { name: "列表" }));
  const dialog = screen.getByRole("dialog", { name: "所有按键" });
  expect(dialog.textContent).toContain("默认");
  expect(dialog.textContent).not.toContain("Stored");
});

it("opens the stick or trigger chip for keys inside them", async () => {
  const editor = await editorFor(profile);
  const onSelect = vi.fn();
  render(<Harness editor={editor} onSelect={onSelect} />);
  const rows: [string, string][] = [
    ["LS ↑", "leftstick"],
    ["LS 原始轴向右", "leftstick"],
    ["LT 半按", "lefttrigger"],
    ["RT", "righttrigger"],
    ["十字键 ↖", "dpad.up"],
    ["A", "south"],
  ];
  for (const [name, callout] of rows) {
    fireEvent.click(screen.getByRole("button", { name: "列表" }));
    const row = screen
      .getAllByRole("button")
      .find(
        (button) =>
          button.querySelector(".bindings-control")?.textContent === name,
      );
    if (!row) throw new Error(`No row named ${name}`);
    fireEvent.click(row);
    expect(onSelect).toHaveBeenLastCalledWith(callout);
    // Focus goes to the chip the row opened, not back to the list button.
    expect(document.activeElement?.getAttribute("data-callout")).toBe(callout);
  }
});

it("returns focus to the list button when closed without a choice", async () => {
  const editor = await editorFor(profile);
  render(<Harness editor={editor} onSelect={vi.fn()} />);
  const opener = screen.getByRole("button", { name: "列表" });
  opener.focus();
  fireEvent.click(opener);
  fireEvent.click(screen.getByRole("button", { name: "关闭按键列表" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.activeElement).toBe(opener);
});

it("focuses the drawer a row opened", async () => {
  const editor = await editorFor(profile);
  render(<Harness editor={editor} onSelect={vi.fn()} drawer />);
  fireEvent.click(screen.getByRole("button", { name: "列表" }));
  const row = screen
    .getAllByRole("button")
    .find(
      (button) =>
        button.querySelector(".bindings-control")?.textContent === "LT 半按",
    );
  if (!row) throw new Error("No half-press row");
  fireEvent.click(row);
  expect(document.activeElement).toBe(
    screen.getByRole("heading", { name: "lefttrigger" }),
  );
});

it("still describes a profile switch from the file", async () => {
  const editor = await editorFor({
    ...profile,
    layers: {
      base: {
        ...profile.layers.base,
        rightshoulder: { press: { type: "profile", profile: "photo" } },
      },
    },
  });
  render(<Harness editor={editor} onSelect={vi.fn()} />);
  fireEvent.click(screen.getByRole("button", { name: "列表" }));
  const row = screen
    .getAllByRole("button")
    .find((button) =>
      button
        .querySelector(".bindings-actions")
        ?.textContent?.includes("切换到「photo」"),
    );
  expect(row?.querySelector(".bindings-actions")?.textContent).toBe(
    "按下 切换到「photo」",
  );
  // With the library, the target has the display name the drawer shows.
  act(() => editor.setState({ profiles: [{ id: "photo", name: "Photo" }] }));
  expect(row?.querySelector(".bindings-actions")?.textContent).toBe(
    "按下 切换到「Photo」",
  );
});

it("marks scripts, and a script whose file is gone", async () => {
  const api = editorAPIFixture();
  api.getProfile = vi.fn(async (id: string) => ({
    id,
    text: JSON.stringify({
      ...profile,
      layers: {
        base: {
          south: { tap: { type: "macro", file: "复制全部.ts" } },
          east: { hold: { type: "macro", file: "旧.js" } },
        },
      },
    }),
    revision: "r1",
    diagnostics: [],
  }));
  api.listMacros.mockResolvedValue({
    macros: [{ file: "复制全部.ts", name: "复制全部", usedBy: [] }],
    missing: [{ file: "旧.js", usedBy: [] }],
  });
  const editor = createEditorStore(api as unknown as PadmateAPI);
  await editor
    .getState()
    .activate({ id: "paint", device: "d", connection: "c" });
  await act(() => editor.getState().refreshMacros());
  render(<Harness editor={editor} onSelect={vi.fn()} />);
  fireEvent.click(screen.getByRole("button", { name: "列表" }));
  const actions = [
    ...document.querySelectorAll(".bindings-list button .bindings-actions"),
  ].map((cell) => cell.textContent);
  expect(actions).toEqual(["点按 脚本 复制全部", "长按 脚本 找不到「旧」"]);
});

it("lists what is saved: a shortcut cleared in the drawer is left out (H1)", async () => {
  const editor = await editorFor(profile);
  render(<Harness editor={editor} onSelect={vi.fn()} />);
  // The drawer's 清除快捷键 leaves an empty shortcut in the draft.
  const cleared = structuredClone(profile);
  cleared.layers.base.south = {
    tap: { type: "keys", keys: "Ctrl+Z" },
    hold: { type: "keys", keys: "" },
  };
  cleared.layers.base.rt = { press: { type: "keys", keys: "" } };
  cleared.layers.base.east = {
    label: "抓手",
    press: { type: "keys", keys: "", mode: "hold" },
  };
  await act(async () => {
    editor.getState().edit(cleared);
    await editor.getState().flush();
  });
  // The draft keeps the empty parts for the drawer.
  expect(editor.getState().draft?.layers.base.rt).toBeDefined();
  fireEvent.click(screen.getByRole("button", { name: "列表" }));
  const rows = [...document.querySelectorAll(".bindings-list li button")].map(
    (row) => [
      row.querySelector(".bindings-control")?.textContent,
      row.querySelector(".bindings-label")?.textContent,
      row.querySelector(".bindings-actions")?.textContent,
    ],
  );
  // The key with a shortcut left lists only that one, and a key with nothing
  // left is not listed; a label alone still is, with no action.
  expect(rows).toContainEqual(["A", "", "点按 Ctrl+Z"]);
  expect(rows).toContainEqual(["B", "抓手", ""]);
  expect(rows.map((row) => row[0])).not.toContain("RT");
  // The rows of bindings (not the function keys) say nothing of holding.
  const listed = [...document.querySelectorAll(".bindings-list button")]
    .map((row) => row.textContent)
    .join("");
  expect(listed).not.toContain("长按");
  expect(listed).not.toContain("按住");
});

// UI-30 (ADR-0069): View and Menu are listed as function keys, and the list
// follows the set the page shows.
const withSets: Profile = {
  ...profile,
  functionKeys: {
    back: { east: keys("Ctrl+Shift+Z"), "ls.up": keys("Up") },
    start: { north: keys("F13") },
  },
};
const rowsOf = () =>
  [...document.querySelectorAll(".bindings-list li")].map((row) => [
    row.querySelector(".bindings-control")?.textContent,
    row.querySelector(".bindings-label")?.textContent,
    row.querySelector(".bindings-actions")?.textContent,
    row.querySelector("button") ? "button" : "fixed",
  ]);

it("lists View and Menu as function keys that cannot be opened", async () => {
  const editor = await editorFor(withSets);
  const onSelect = vi.fn();
  render(<Harness editor={editor} onSelect={onSelect} />);
  fireEvent.click(screen.getByRole("button", { name: "列表" }));
  const rows = rowsOf();
  expect(rows.slice(-2)).toEqual([
    ["View", "功能键", "按住时其他键换成另一套", "fixed"],
    ["Menu", "功能键", "按住时其他键换成另一套", "fixed"],
  ]);
  // The rows of bindings come first, and open their chips.
  expect(rows.slice(0, -2).every((row) => row[3] === "button")).toBe(true);
  const fixed = document.querySelector(".bindings-fixed") as HTMLElement;
  fireEvent.click(fixed);
  expect(onSelect).not.toHaveBeenCalled();
});

it("lists them when nothing else is set", async () => {
  const editor = await editorFor({ ...profile, layers: { base: {} } });
  render(<Harness editor={editor} onSelect={vi.fn()} />);
  fireEvent.click(screen.getByRole("button", { name: "列表" }));
  expect(screen.getByText("尚未设置任何按键。")).toBeDefined();
  expect(rowsOf().map((row) => row[0])).toEqual(["View", "Menu"]);
});

it("lists the keys of the set the page shows, under its name", async () => {
  const editor = await editorFor(withSets);
  render(<Harness editor={editor} onSelect={vi.fn()} />);
  act(() => editor.getState().setView("back"));
  fireEvent.click(screen.getByRole("button", { name: "列表" }));
  const dialog = screen.getByRole("dialog", { name: "所有按键" });
  expect(dialog.textContent).toContain("绘画 · 按住 View");
  expect(rowsOf().map((row) => row.slice(0, 3))).toEqual([
    ["B", "", "按下 Ctrl+Shift+Z"],
    ["LS ↑", "", "按下 Up"],
    ["View", "功能键", "按住时其他键换成另一套"],
    ["Menu", "功能键", "按住时其他键换成另一套"],
  ]);
  act(() => editor.getState().setView("start"));
  expect(rowsOf().map((row) => row[0])).toEqual(["Y", "View", "Menu"]);
  act(() => editor.getState().setView("normal"));
  expect(
    screen.getByRole("dialog", { name: "所有按键" }).textContent,
  ).not.toContain("按住 View");
});

it("lists a set with nothing in it as empty", async () => {
  const editor = await editorFor(profile);
  render(<Harness editor={editor} onSelect={vi.fn()} />);
  act(() => editor.getState().setView("start"));
  fireEvent.click(screen.getByRole("button", { name: "列表" }));
  expect(screen.getByText("尚未设置任何按键。")).toBeDefined();
});

it("names the function keys as the pad prints them, in English too", async () => {
  const editor = await editorFor(withSets);
  render(
    <BindingsListDialog
      open
      editor={editor}
      info={{ ...info, type: "ps5" }}
      language="en"
      onClose={() => {}}
      onSelect={() => {}}
    />,
  );
  expect(rowsOf().slice(-2)).toEqual([
    [
      "Create",
      "Function key",
      "While held, other keys switch to another set",
      "fixed",
    ],
    [
      "Options",
      "Function key",
      "While held, other keys switch to another set",
      "fixed",
    ],
  ]);
});

it("lists only Menu for a single Joy-Con held sideways", async () => {
  const editor = await editorFor(withSets);
  render(
    <BindingsListDialog
      open
      editor={editor}
      info={{ ...info, type: "joyconleft", buttonMask: 0xf }}
      language="zh-CN"
      onClose={() => {}}
      onSelect={() => {}}
    />,
  );
  expect(rowsOf().slice(-1)).toEqual([
    ["−", "功能键", "按住时其他键换成另一套", "fixed"],
  ]);
  expect(rowsOf().filter((row) => row[3] === "fixed")).toHaveLength(1);
});

it("lists the volume and media keys by name", async () => {
  const editor = await editorFor({
    schemaVersion: 1,
    name: "媒体",
    layers: {
      base: {
        south: keys("VolumeUp"),
        east: { tap: { type: "keys", keys: "Ctrl+MediaNext" } },
      },
    },
  });
  render(<Harness editor={editor} onSelect={vi.fn()} />);
  fireEvent.click(screen.getByRole("button", { name: "列表" }));
  const dialog = screen.getByRole("dialog", { name: "所有按键" });
  expect(dialog.textContent).toContain("按下 音量加");
  expect(dialog.textContent).toContain("点按 Ctrl+下一首");
});

it("lists what the profile takes from the one it is based on, marked 继承 (PRO-19)", async () => {
  const parent: Profile = {
    schemaVersion: 1,
    name: "绘画",
    controlNames: { east: "重做" },
    layers: { base: { south: keys("Ctrl+Z"), east: keys("Ctrl+Y") } },
  };
  const child: Profile = {
    schemaVersion: 1,
    name: "草图",
    extends: "paint",
    layers: { base: { south: keys("Ctrl+Shift+Z"), west: keys("E") } },
  };
  const editor = await editorFor(child, "sketch");
  editor.setState({ parent: { id: "paint", profile: parent, origins: [] } });
  render(<Harness editor={editor} onSelect={vi.fn()} />);
  fireEvent.click(screen.getByRole("button", { name: "列表" }));
  const dialog = screen.getByRole("dialog", { name: "所有按键" });
  const rows = [...dialog.querySelectorAll("li")]
    .filter((row) => row.querySelector(".bindings-control")?.textContent)
    .map((row) => row.textContent);
  // Its own bindings first, then what the parent adds, under the parent's name.
  expect(rows.slice(0, 3)).toEqual([
    "A按下 Ctrl+Shift+Z",
    "X按下 E",
    "重做继承按下 Ctrl+Y",
  ]);
  const inherited = [...dialog.querySelectorAll("li")].filter((row) =>
    row.querySelector('[data-testid="inherited-mark"]'),
  );
  expect(inherited).toHaveLength(1);
  expect(inherited[0].textContent).toContain("Ctrl+Y");
});
