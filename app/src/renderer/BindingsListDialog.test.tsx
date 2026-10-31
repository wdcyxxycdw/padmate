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
      {[
        "south",
        "leftstick",
        "leftstick.press",
        "rightstick.press",
        "lefttrigger",
        "righttrigger",
        "dpad.up",
      ].map((control) => (
        <button
          type="button"
          key={control}
          data-callout={control}
          aria-pressed={selected === control}
        >
          {control}
        </button>
      ))}
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

it("names a stick's press like the stick, and never by its selection id", () => {
  // With no controller there are no labels: the stick's own name stands, not
  // "leftstick.press" (the press cell's selection id).
  expect(listControlName("l3", {}, "zh-CN")).toBe("leftstick");
  expect(listControlName("rightstick", {}, "en")).toBe("rightstick");
  expect(listControlName("ls.up", {}, "zh-CN")).toBe("leftstick ↑");
  const labels = controllerLabels("playstation");
  expect(listControlName("l3", labels, "zh-CN")).toBe(labels.leftstick);
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

it("opens the press cell for a stick's own key, in either name", async () => {
  const editor = await editorFor({
    ...profile,
    layers: {
      base: {
        ...profile.layers.base,
        l3: keys("Tab"),
        rightstick: keys("Shift"),
      },
    },
  });
  const onSelect = vi.fn();
  render(<Harness editor={editor} onSelect={onSelect} />);
  const click = (name: string) => {
    fireEvent.click(screen.getByRole("button", { name: "列表" }));
    const row = screen
      .getAllByRole("button")
      .find(
        (button) =>
          button.querySelector(".bindings-control")?.textContent === name,
      );
    if (!row) throw new Error(`No row named ${name}`);
    fireEvent.click(row);
  };
  // Named like the stick, as before; the direction rows still open the movement.
  click("LS");
  expect(onSelect).toHaveBeenLastCalledWith("leftstick.press");
  expect(document.activeElement?.getAttribute("data-callout")).toBe(
    "leftstick.press",
  );
  click("RS");
  expect(onSelect).toHaveBeenLastCalledWith("rightstick.press");
  click("LS ↑");
  expect(onSelect).toHaveBeenLastCalledWith("leftstick");
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
    "点按 切换到「photo」",
  );
  // With the library, the target has the display name the drawer shows.
  act(() => editor.setState({ profiles: [{ id: "photo", name: "Photo" }] }));
  expect(row?.querySelector(".bindings-actions")?.textContent).toBe(
    "点按 切换到「Photo」",
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

// UI-30 (ADR-0075): the function key is listed as one that cannot be opened,
// and the list follows the set the page shows.
const withSet: Profile = {
  ...profile,
  functionSet: { east: keys("Ctrl+Shift+Z"), "ls.up": keys("Up") },
};
const rowsOf = () =>
  [...document.querySelectorAll(".bindings-list li")].map((row) => [
    row.querySelector(".bindings-control")?.textContent,
    row.querySelector(".bindings-label")?.textContent,
    row.querySelector(".bindings-actions")?.textContent,
    row.querySelector("button") ? "button" : "fixed",
  ]);

it("lists the function key as one that cannot be opened", async () => {
  const editor = await editorFor(withSet);
  const onSelect = vi.fn();
  render(<Harness editor={editor} onSelect={onSelect} />);
  fireEvent.click(screen.getByRole("button", { name: "列表" }));
  const rows = rowsOf();
  expect(rows.slice(-1)).toEqual([
    ["Menu", "功能键", "按住时其他键换成另一套", "fixed"],
  ]);
  // The rows of bindings come first, and open their chips.
  expect(rows.slice(0, -1).every((row) => row[3] === "button")).toBe(true);
  expect(rows.filter((row) => row[3] === "fixed")).toHaveLength(1);
  const fixed = document.querySelector(".bindings-fixed") as HTMLElement;
  fireEvent.click(fixed);
  expect(onSelect).not.toHaveBeenCalled();
});

it("lists the one the settings choose, and the others as keys with their bindings", async () => {
  const editor = await editorFor({
    ...profile,
    layers: {
      base: {
        ...profile.layers.base,
        start: keys("Ctrl+S"),
        back: keys("Ctrl+V"),
      },
    },
  });
  render(<Harness editor={editor} onSelect={vi.fn()} />);
  fireEvent.click(screen.getByRole("button", { name: "列表" }));
  // Menu is the function key: what the file binds on it is not listed.
  let rows = rowsOf();
  expect(rows.map((row) => row[0])).toContain("View");
  expect(rows.slice(-1)).toEqual([
    ["Menu", "功能键", "按住时其他键换成另一套", "fixed"],
  ]);
  expect(rows.filter((row) => row[0] === "Menu")).toHaveLength(1);
  expect(rows).toContainEqual(["View", "", "点按 Ctrl+V", "button"]);
  // Another choice swaps the two.
  act(() => editor.getState().setFunctionKey("back"));
  rows = rowsOf();
  expect(rows).toContainEqual(["Menu", "", "点按 Ctrl+S", "button"]);
  expect(rows.slice(-1)).toEqual([
    ["View", "功能键", "按住时其他键换成另一套", "fixed"],
  ]);
  expect(rows.filter((row) => row[0] === "View")).toHaveLength(1);
  // The Xbox button.
  act(() => editor.getState().setFunctionKey("guide"));
  expect(rowsOf().slice(-1)).toEqual([
    ["Xbox", "功能键", "按住时其他键换成另一套", "fixed"],
  ]);
});

it("lists them when nothing else is set", async () => {
  const editor = await editorFor({ ...profile, layers: { base: {} } });
  render(<Harness editor={editor} onSelect={vi.fn()} />);
  fireEvent.click(screen.getByRole("button", { name: "列表" }));
  expect(screen.getByText("尚未设置任何按键。")).toBeDefined();
  expect(rowsOf().map((row) => row[0])).toEqual(["Menu"]);
});

it("lists the keys of the set the page shows, under its name", async () => {
  const editor = await editorFor(withSet);
  render(<Harness editor={editor} onSelect={vi.fn()} />);
  act(() => editor.getState().setView("function"));
  fireEvent.click(screen.getByRole("button", { name: "列表" }));
  const dialog = screen.getByRole("dialog", { name: "所有按键" });
  expect(dialog.textContent).toContain("绘画 · 按住功能键");
  expect(rowsOf().map((row) => row.slice(0, 3))).toEqual([
    ["B", "", "点按 Ctrl+Shift+Z"],
    ["LS ↑", "", "点按 Up"],
    ["Menu", "功能键", "按住时其他键换成另一套"],
  ]);
  act(() => editor.getState().setView("normal"));
  expect(
    screen.getByRole("dialog", { name: "所有按键" }).textContent,
  ).not.toContain("按住功能键");
});

it("lists the set of a file of the old functionKeys as the one set it stands for", async () => {
  const editor = await editorFor({
    ...profile,
    functionKeys: {
      back: { east: keys("Ctrl+Shift+Z") },
      start: { north: keys("F13") },
    },
  });
  render(<Harness editor={editor} onSelect={vi.fn()} />);
  act(() => editor.getState().setView("function"));
  fireEvent.click(screen.getByRole("button", { name: "列表" }));
  expect(rowsOf().map((row) => row[0])).toEqual(["Y", "B", "Menu"]);
});

it("lists a set with nothing in it as empty", async () => {
  const editor = await editorFor(profile);
  render(<Harness editor={editor} onSelect={vi.fn()} />);
  act(() => editor.getState().setView("function"));
  fireEvent.click(screen.getByRole("button", { name: "列表" }));
  expect(screen.getByText("尚未设置任何按键。")).toBeDefined();
});

it("names the function key as the pad prints it, in English too", async () => {
  const editor = await editorFor(withSet);
  const names = (type: string) => {
    cleanup();
    render(
      <BindingsListDialog
        open
        editor={editor}
        info={{ ...info, type }}
        language="en"
        onClose={() => {}}
        onSelect={() => {}}
      />,
    );
    return rowsOf().slice(-1);
  };
  expect(names("ps5")).toEqual([
    [
      "Options",
      "Function key",
      "While held, other keys switch to another set",
      "fixed",
    ],
  ]);
  act(() => editor.getState().setFunctionKey("back"));
  expect(names("ps5")[0][0]).toBe("Create");
  act(() => editor.getState().setFunctionKey("guide"));
  expect(names("ps5")[0][0]).toBe("PS");
});

it("draws the function key with the icon of the connected pad's generation (ADR-0079 §7)", async () => {
  const editor = await editorFor(withSet);
  const icon = (type: string) => {
    cleanup();
    render(
      <BindingsListDialog
        open
        editor={editor}
        info={{ ...info, type }}
        language="en"
        onClose={() => {}}
        onSelect={() => {}}
      />,
    );
    return screen
      .getByTestId("function-key-row")
      .querySelector("svg[data-pad-icon]")
      ?.getAttribute("data-pad-icon");
  };
  // Options on a DualShock 4 is not a DualSense's: the row reads the pad's
  // other names (its back is Share), as the chips do.
  expect(icon("ps4")).toBe("kenney/playstation4_button_options");
  expect(icon("ps5")).toBe("kenney/playstation5_button_options");
});

it("lists Menu for a single Joy-Con held sideways, and nothing for View, which it lacks", async () => {
  const editor = await editorFor(withSet);
  const show = () => {
    cleanup();
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
  };
  show();
  expect(rowsOf().slice(-1)).toEqual([
    ["−", "功能键", "按住时其他键换成另一套", "fixed"],
  ]);
  expect(rowsOf().filter((row) => row[3] === "fixed")).toHaveLength(1);
  act(() => editor.getState().setFunctionKey("back"));
  show();
  expect(rowsOf().filter((row) => row[3] === "fixed")).toHaveLength(0);
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
  expect(dialog.textContent).toContain("点按 音量加");
  expect(dialog.textContent).toContain("点按 Ctrl+下一首");
});

it("lists what the profile takes from the one its file extends, unmarked (PRO-19, ADR-0076)", async () => {
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
  editor.setState({ parent: { id: "paint", profile: parent } });
  render(<Harness editor={editor} onSelect={vi.fn()} />);
  fireEvent.click(screen.getByRole("button", { name: "列表" }));
  const dialog = screen.getByRole("dialog", { name: "所有按键" });
  const rows = [...dialog.querySelectorAll("li")]
    .filter((row) => row.querySelector(".bindings-control")?.textContent)
    .map((row) => row.textContent);
  // Its own bindings first, then what the parent adds, under the parent's name
  // and with no mark.
  expect(rows.slice(0, 3)).toEqual([
    "A点按 Ctrl+Shift+Z",
    "X点按 E",
    "重做点按 Ctrl+Y",
  ]);
  expect(dialog.querySelector('[data-testid="inherited-mark"]')).toBeNull();
});

it("shows the icon of each key before its name, as the key preview does (ADR-0076)", async () => {
  const editor = await editorFor(profile);
  render(<Harness editor={editor} onSelect={vi.fn()} />);
  fireEvent.click(screen.getByRole("button", { name: "列表" }));
  const kinds = Object.fromEntries(
    [...document.querySelectorAll(".bindings-list li")].map((row) => {
      const control = row.querySelector(".bindings-control");
      const icon = control?.firstElementChild;
      // Drawn, not read: a screen reader gets the name.
      expect(icon?.classList.contains("control-icon")).toBe(true);
      expect(icon?.getAttribute("aria-hidden")).toBe("true");
      // A vendored icon by its name (ADR-0079 §7), else padmate's shape.
      return [
        control?.textContent,
        icon
          ?.querySelector("svg[data-pad-icon]")
          ?.getAttribute("data-pad-icon") ?? icon?.getAttribute("data-kind"),
      ];
    }),
  );
  expect(kinds).toEqual({
    A: "kenney/xbox_button_a",
    "LS ↑": "kenney/xbox_stick_l_up",
    "LS 原始轴向右": "kenney/xbox_stick_l_right",
    "LT 半按": "kenney/xbox_lt",
    RT: "kenney/xbox_rt",
    // The icon sets have no diagonal: padmate's cross with two arms lit.
    "十字键 ↖": "dpad",
    // The function key, which cannot be opened, is listed the same way,
    // with the symbol printed on it (ADR-0077).
    Menu: "kenney/xbox_button_menu",
  });
});
