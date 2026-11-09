// @vitest-environment jsdom
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import type { DeviceInfo, PadmateAPI } from "../../shared/api";
import type { Profile } from "../../shared/generated/schema";
import { KeyDrawer } from "../drawer/KeyDrawer";
import { createEditorStore } from "../editor/editor-store";
import { fakeCapture } from "../testing/capture-fixture";
import { editorAPIFixture } from "../testing/editor-api-fixture";

// What clearing does to a knob (a stick in mode "knob") in the real key
// drawer, editor store and undo history.

afterEach(() => {
  cleanup();
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
const knob: Profile = {
  schemaVersion: 1,
  name: "Knob",
  analog: {
    ls: { mode: "knob", knobStepDeg: 45, knobTick: true, label: "笔刷" },
  },
  layers: {
    base: {
      "ls.cw": { press: { type: "keys", keys: "]" } },
      "ls.ccw": { press: { type: "keys", keys: "[" } },
    },
  },
};

async function setup(profile: Profile = knob, control = "leftstick") {
  const fixture = editorAPIFixture();
  const api = {
    ...fixture,
    getProfile: async (id: string) => ({
      id,
      revision: "r1",
      diagnostics: [],
      text: JSON.stringify(profile),
    }),
  };
  fakeCapture(api);
  const editor = createEditorStore(api as unknown as PadmateAPI);
  await editor
    .getState()
    .activate({ id: "paint", device: "device-a", connection: "c" });
  render(
    <KeyDrawer
      editor={editor}
      control={control}
      onControl={() => {}}
      info={info}
      language="zh-CN"
      onClose={() => {}}
    />,
  );
  return editor;
}
const draft = (editor: Awaited<ReturnType<typeof setup>>) =>
  editor.getState().draft as Profile;
const row = (control: string) =>
  document.querySelector(
    `.analog-row[data-control="${control}"]`,
  ) as HTMLElement;

type Editor = Awaited<ReturnType<typeof setup>>;
const ticks = (editor: Editor) => ({
  cw: draft(editor).layers.base["ls.cw"],
  ccw: draft(editor).layers.base["ls.ccw"],
});
const undo = (editor: Editor) => act(() => editor.getState().undo());

it("clearing a tick takes its binding out of the file, in one undo step", async () => {
  const editor = await setup();
  fireEvent.click(row("ls.cw"));
  fireEvent.click(screen.getByRole("button", { name: "清除快捷键" }));
  expect(ticks(editor)).toEqual({
    cw: undefined,
    ccw: { press: { type: "keys", keys: "[" } },
  });
  // The stick still maps the other tick, so it keeps its name.
  expect(draft(editor).analog?.ls?.label).toBe("笔刷");
  undo(editor);
  expect(draft(editor)).toEqual(knob);
  expect(editor.getState().canUndo).toBe(false);
});

it("clearing the last tick takes the stick's name with it, and one undo brings both back", async () => {
  const editor = await setup({
    ...knob,
    layers: { base: { "ls.cw": { press: { type: "keys", keys: "]" } } } },
  });
  fireEvent.click(row("ls.cw"));
  fireEvent.click(screen.getByRole("button", { name: "清除快捷键" }));
  expect(draft(editor).layers.base).toEqual({});
  expect(draft(editor).analog?.ls).not.toHaveProperty("label");
  // The mode and the step are parameters of the stick, not of a tick.
  expect(draft(editor).analog?.ls).toEqual({
    mode: "knob",
    knobStepDeg: 45,
    knobTick: true,
  });
  undo(editor);
  expect(draft(editor).analog?.ls?.label).toBe("笔刷");
  expect(ticks(editor).cw).toEqual({ press: { type: "keys", keys: "]" } });
});

it("clearing a tick leaves no haptic or name of the file behind", async () => {
  const editor = await setup({
    ...knob,
    layers: {
      base: {
        "ls.cw": {
          label: "放大",
          haptic: "tick",
          press: { type: "keys", keys: "]" },
        },
        "ls.ccw": { press: { type: "keys", keys: "[" } },
      },
    },
  });
  fireEvent.click(row("ls.cw"));
  fireEvent.click(screen.getByRole("button", { name: "清除快捷键" }));
  expect(draft(editor).layers.base).not.toHaveProperty("ls.cw");
  undo(editor);
  expect(ticks(editor).cw).toMatchObject({ haptic: "tick", label: "放大" });
});

it("clears a tick that the file sets with something else, which the shortcut field cannot show", async () => {
  const editor = await setup({
    ...knob,
    layers: {
      base: {
        "ls.cw": {
          tap: { type: "keys", keys: "]" },
          hold: { type: "keys", keys: "}" },
        },
        "ls.ccw": { press: { type: "keys", keys: "[" } },
      },
    },
  });
  fireEvent.click(row("ls.cw"));
  expect(screen.queryByRole("button", { name: "清除快捷键" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "清除这一格" }));
  expect(draft(editor).layers.base).not.toHaveProperty("ls.cw");
  expect(ticks(editor).ccw).toEqual({ press: { type: "keys", keys: "[" } });
  undo(editor);
  expect(ticks(editor).cw).toMatchObject({ hold: { keys: "}" } });
  expect(editor.getState().canUndo).toBe(false);
});

const openParams = () => {
  (screen.getByTestId("stick-advanced") as HTMLDetailsElement).open = true;
  fireEvent.click(screen.getByTestId("analog-params"));
};

it("resetting the parameters clears the step and the haptic in one undo step, and keeps the ticks as it says", async () => {
  const editor = await setup();
  openParams();
  fireEvent.click(screen.getByRole("menuitem", { name: "恢复默认参数…" }));
  fireEvent.click(screen.getByRole("button", { name: "恢复默认" }));
  const analog = draft(editor).analog?.ls;
  expect(analog).not.toHaveProperty("knobStepDeg");
  expect(analog).not.toHaveProperty("knobTick");
  // The stick is no knob any more and so maps nothing: its name goes (the
  // ticks stay in the file, as the dialog says the keys do).
  expect(analog).not.toHaveProperty("label");
  expect(ticks(editor).cw).toEqual({ press: { type: "keys", keys: "]" } });
  undo(editor);
  expect(draft(editor)).toEqual(knob);
  expect(editor.getState().canUndo).toBe(false);
});

it("leaving the knob keeps the ticks, the step and the haptic of the file, and one undo goes back", async () => {
  const editor = await setup();
  for (const name of ["鼠标", "四方向"]) {
    fireEvent.click(screen.getByRole("radio", { name }));
    expect(draft(editor).analog?.ls?.mode).toBe(
      name === "鼠标" ? "mouse" : "fourWay",
    );
    expect(ticks(editor).cw).toEqual({ press: { type: "keys", keys: "]" } });
    expect(draft(editor).analog?.ls).toMatchObject({
      knobStepDeg: 45,
      knobTick: true,
    });
    // The ticks are not shown on a stick that is no knob.
    expect(row("ls.cw")).toBeNull();
    undo(editor);
    expect(draft(editor)).toEqual(knob);
    expect(row("ls.cw")).not.toBeNull();
  }
});

const clearFromMenu = () => {
  fireEvent.click(screen.getByTestId("key-actions"));
  fireEvent.click(screen.getByRole("menuitem", { name: "清空此按键…" }));
  fireEvent.click(screen.getByRole("button", { name: "清空" }));
};

it("has a 清空此按键 action in the stick's key actions, as a key has", async () => {
  const editor = await setup();
  fireEvent.click(screen.getByTestId("key-actions"));
  // Copy and paste are for keys; a stick has the one action.
  expect(
    screen.getAllByRole("menuitem").map((item) => item.textContent),
  ).toEqual(["清空此按键…"]);
  fireEvent.click(screen.getByRole("menuitem", { name: "清空此按键…" }));
  expect(screen.getByRole("dialog").textContent).toContain(
    "恢复成没设置过的样子",
  );
  // Asking changes nothing; cancelling leaves it.
  fireEvent.click(screen.getByRole("button", { name: "取消" }));
  expect(draft(editor)).toEqual(knob);
});

it("clears the knob completely, to a profile that never had it, in one undo step", async () => {
  const editor = await setup();
  clearFromMenu();
  expect(draft(editor)).toEqual({
    schemaVersion: 1,
    name: "Knob",
    layers: { base: {} },
  });
  expect(draft(editor)).not.toHaveProperty("analog");
  // The panel shows an unconfigured stick: no knob, four directions, no name.
  expect(row("ls.cw")).toBeNull();
  expect(
    screen.getByRole("radio", { name: "四方向" }).getAttribute("aria-checked"),
  ).toBe("true");
  undo(editor);
  expect(draft(editor)).toEqual(knob);
  expect(editor.getState().canUndo).toBe(false);
  // Nothing left to clear: the action is off.
  clearFromMenu();
  fireEvent.click(screen.getByTestId("key-actions"));
  expect(
    screen.getByRole("menuitem", { name: "清空此按键…" }).matches(":disabled"),
  ).toBe(true);
});

it("clears what the mode leaves behind, and only this stick", async () => {
  const profile: Profile = {
    schemaVersion: 1,
    name: "Mixed",
    analog: {
      ls: { mode: "mouse", knobStepDeg: 45, knobTick: true, label: "笔刷" },
      rs: { mode: "knob" },
      axisThreshold: 0.4,
    },
    layers: {
      base: {
        "ls.cw": { press: { type: "keys", keys: "]" } },
        "ls.up": { press: { type: "keys", keys: "W" } },
        "ls.inner": { press: { type: "keys", keys: "I" } },
        "lefty-": { press: { type: "keys", keys: "U" } },
        leftstick: { press: { type: "keys", keys: "F5" } },
        "rs.cw": { press: { type: "keys", keys: "]" } },
        south: { press: { type: "keys", keys: "Ctrl+Z" } },
      },
    },
  };
  const editor = await setup(profile);
  clearFromMenu();
  expect(draft(editor)).toEqual({
    schemaVersion: 1,
    name: "Mixed",
    analog: { rs: { mode: "knob" }, axisThreshold: 0.4 },
    layers: {
      base: {
        leftstick: { press: { type: "keys", keys: "F5" } },
        "rs.cw": { press: { type: "keys", keys: "]" } },
        south: { press: { type: "keys", keys: "Ctrl+Z" } },
      },
    },
  });
  undo(editor);
  expect(draft(editor)).toEqual(profile);
});

it("clears a trigger-free right stick the same way", async () => {
  const editor = await setup(
    {
      ...knob,
      analog: { rs: { mode: "knob", knobStepDeg: 60 } },
      layers: { base: { "rs.ccw": { press: { type: "keys", keys: "[" } } } },
    },
    "rightstick",
  );
  clearFromMenu();
  expect(draft(editor)).toEqual({
    schemaVersion: 1,
    name: "Knob",
    layers: { base: {} },
  });
});
