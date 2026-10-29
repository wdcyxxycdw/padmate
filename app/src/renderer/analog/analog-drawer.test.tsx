// @vitest-environment jsdom
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
  ScriptRegistration,
  ScriptStatusEntry,
} from "../../shared/api";
import type { Profile } from "../../shared/generated/schema";
import { fakeCapture } from "../capture-fixture";
import { editorAPIFixture } from "../editor-api-fixture";
import { createEditorStore, MERGE_IDLE_MS } from "../editor-store";
import { KeyDrawer } from "../KeyDrawer";

// The analog panels inside the real key drawer, editor store and gesture
// editor: what a stick or trigger drawer shows and what saving writes.

afterEach(() => {
  cleanup();
  vi.useRealTimers();
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
const layered: Profile = {
  schemaVersion: 1,
  name: "Paint",
  analog: { ls: { deadzone: 0.2 } },
  common: { back: { press: { type: "keys", keys: "F12" } } },
  layers: {
    base: {
      "ls.up": { press: { type: "keys", keys: "F13" } },
      south: { press: { type: "keys", keys: "Ctrl+Z" } },
    },
    tools: {
      "ls.right": { press: { type: "keys", keys: "B" } },
      "ls.down": { press: { type: "keys", keys: "N" } },
    },
  },
};
const registration = (
  id: number,
  control: string,
  changes: Partial<ScriptRegistration> = {},
): ScriptRegistration => ({
  id,
  control,
  gesture: "press",
  scope: "base",
  observe: false,
  claim: false,
  priority: 3,
  label: "Brush direction",
  timing: { holdMs: 450 },
  ...changes,
});
const status = (
  key: string,
  profileId: string,
  registrations: ScriptRegistration[],
): ScriptStatusEntry => ({
  slot: 0,
  device: 1,
  key,
  profileId,
  generation: 9,
  script: {
    state: "Running",
    entry: "main.ts",
    api: "0.21",
    paused: false,
    pending: false,
    wallStopped: false,
    restartCount: 0,
    permanent: false,
    metadata: { runtime: 14, registrations },
  },
});

async function setup(control: string, profile: Profile = layered) {
  return (await setupWithAPI(control, profile)).editor;
}

async function setupWithAPI(control: string, profile: Profile = layered) {
  const fixture = editorAPIFixture();
  const api = {
    ...fixture,
    getProfile: async (id: string) => ({
      id,
      revision: "r1",
      diagnostics: [],
      text: JSON.stringify(profile),
    }),
    getScriptRegistrations: async () => [
      status("device-a", "paint", [
        registration(1, "ls.up"),
        registration(2, "ls.right", {
          observe: true,
          scope: "global",
          gesture: "*",
          label: "Observe right",
        }),
        registration(3, "ls.down", {
          scope: "layer:tools",
          claim: true,
          label: "Tools down",
        }),
        registration(4, "lefttrigger+", {
          scope: "global",
          claim: true,
          label: "Trigger half press",
        }),
      ]),
      status("device-b", "paint", [
        registration(5, "ls.left", { label: "Wrong device" }),
      ]),
      status("device-a", "other", [
        registration(6, "ls.left", { label: "Wrong profile" }),
      ]),
    ],
  };
  const { record } = fakeCapture(api);
  const editor = createEditorStore(api as unknown as PadmateAPI);
  await editor
    .getState()
    .activate({ id: "paint", device: "device-a", connection: "c" });
  await editor.getState().refreshMetadata();
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
  return { editor, record, put: fixture.putProfile };
}
const cell = (direction: string) => {
  const found = document.querySelector<HTMLButtonElement>(
    `.analog-cell[data-direction="${direction}"]`,
  );
  if (!found) throw new Error(`no ${direction} cell`);
  return found;
};

it("marks only this device's base-key script owners on the stick", async () => {
  await setup("leftstick");
  const panel = screen.getByRole("region", { name: "摇杆设置" });
  const owned = (direction: string) =>
    within(cell(direction)).queryByTestId("analog-script-ownership");
  expect(owned("up")?.textContent).toBe("S");
  expect(owned("right")).toBeNull();
  expect(owned("down")).toBeNull();
  expect(owned("left")).toBeNull();
  // The other key group's bindings do not show up in the grid.
  expect(cell("right").getAttribute("aria-label")).toBe("右：未设置");
  fireEvent.click(cell("up"));
  const card = within(panel).getByRole("region", { name: "↑ 向上推" });
  expect(card.textContent).toContain("此处也由脚本处理：Brush direction");
  expect(card.textContent).toContain("同时设置了按键");
  expect(panel.textContent).not.toMatch(
    /Wrong device|Wrong profile|Tools down/,
  );
});

it("edits a direction with the real gesture editor and saves only base", async () => {
  const { editor, record } = await setupWithAPI("leftstick");
  fireEvent.click(cell("right"));
  const card = screen.getByRole("region", { name: "→ 向右推" });
  await record("F22", { scope: card });
  const draft = editor.getState().draft as Profile;
  expect(draft.layers.base["ls.right"]?.press).toMatchObject({
    type: "keys",
    keys: "F22",
  });
  const deadzone = screen.getByRole("spinbutton", { name: "死区" });
  fireEvent.change(deadzone, { target: { value: "15" } });
  fireEvent.keyDown(deadzone, { key: "Enter" });
  const edited = editor.getState().draft as Profile;
  expect(edited.analog?.ls?.deadzone).toBe(0.15);
  expect(edited.layers.tools).toEqual(layered.layers.tools);
  expect(edited.common).toEqual(layered.common);
  expect(edited.layers.base.south).toEqual(layered.layers.base.south);
  expect(editor.getState().unsaved).toBe(true);
  expect(await act(() => editor.getState().flush())).toBe(true);
  expect(editor.getState().unsaved).toBe(false);
});

it("saves a dead zone drag once and undoes it in one step", async () => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  const { editor, put } = await setupWithAPI("leftstick");
  const slider = screen.getByRole("slider", { name: "死区" });
  fireEvent.pointerDown(slider);
  for (const value of ["20", "25", "30"])
    fireEvent.change(slider, { target: { value } });
  // A pause while the pointer is still down does not end the drag.
  await act(() => vi.advanceTimersByTimeAsync(MERGE_IDLE_MS * 2));
  expect(put).not.toHaveBeenCalled();
  fireEvent.pointerUp(window);
  await act(() => vi.advanceTimersByTimeAsync(MERGE_IDLE_MS));
  await act(() => editor.getState().flush());
  expect(put).toHaveBeenCalledOnce();
  expect(JSON.parse(put.mock.calls[0]?.[1] as string).analog.ls.deadzone).toBe(
    0.3,
  );
  act(() => {
    editor.getState().undo();
  });
  expect((editor.getState().draft as Profile).analog?.ls?.deadzone).toBe(
    layered.analog?.ls?.deadzone,
  );
  expect(editor.getState().canUndo).toBe(false);
});

it("shows a script claim on the trigger's half press", async () => {
  await setup("lefttrigger");
  const half = document.querySelector(
    '.analog-row[data-control="lefttrigger+"]',
  ) as HTMLElement;
  expect(half).not.toBeNull();
  expect(within(half).getByTestId("analog-script-ownership")).toBeDefined();
});

it("lets a locked stick be read but not edited", async () => {
  // The profile starts on another key group, so the editor is read-only.
  await setup("leftstick", { ...layered, defaultLayer: "tools" });
  const isDisabled = (element: HTMLElement) => element.matches(":disabled");
  expect(isDisabled(cell("up"))).toBe(false);
  fireEvent.click(cell("up"));
  const card = screen.getByRole("region", { name: "↑ 向上推" });
  expect(
    isDisabled(within(card).getByRole("button", { name: "录入快捷键" })),
  ).toBe(true);
  expect(isDisabled(within(card).getByTestId("add-gesture"))).toBe(true);
  expect(isDisabled(screen.getByRole("spinbutton", { name: "死区" }))).toBe(
    true,
  );
  expect(isDisabled(screen.getByRole("button", { name: "WASD" }))).toBe(true);
  expect(isDisabled(screen.getByRole("textbox", { name: "功能" }))).toBe(true);
});

it("lets a locked trigger's half press be read but not edited", async () => {
  await setup("lefttrigger", {
    ...layered,
    defaultLayer: "tools",
    layers: {
      ...layered.layers,
      base: {
        ...layered.layers.base,
        "lefttrigger+": { press: { type: "keys", keys: "F14" } },
      },
    },
  });
  for (const details of document.querySelectorAll("details"))
    details.open = true;
  const settings = screen.getByRole("region", { name: "扳机设置" });
  const threshold = within(settings).getByRole("spinbutton", {
    name: "原始轴阈值",
  });
  expect(threshold.matches(":disabled")).toBe(true);
  const half = settings.querySelector(
    '.analog-row[data-control="lefttrigger+"]',
  ) as HTMLButtonElement;
  expect(half.matches(":disabled")).toBe(false);
  fireEvent.click(half);
  expect(
    within(settings)
      .getByRole("button", { name: "录入快捷键" })
      .matches(":disabled"),
  ).toBe(true);
});
