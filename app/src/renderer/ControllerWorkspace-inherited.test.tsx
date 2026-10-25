// @vitest-environment jsdom
import { act, cleanup, render } from "@testing-library/react";
import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import type { DeviceInfo, PadmateAPI, ProfileEntry } from "../shared/api";
import type { Profile } from "../shared/generated/schema";
import { ControllerWorkspace } from "./ControllerWorkspace";
import { editorAPIFixture } from "./editor-api-fixture";
import { createEditorStore, type EditorStore } from "./editor-store";
import { translator } from "./i18n";
import { createPadmateStore, type LabelDisplay, type Language } from "./store";

// The mapping page of a profile that is based on another (PRO-19): the chips
// of what it inherits, marked, beside its own.

vi.mock("./KeyDrawer", () => ({ KeyDrawer: () => null }));

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
  controlNames: { north: "笔刷" },
  layers: {
    base: {
      south: { tap: { type: "keys", keys: "Ctrl+Z" } },
      east: { tap: { type: "keys", keys: "Ctrl+Y" } },
      north: { tap: { type: "keys", keys: "B" } },
    },
  },
  functionKeys: { back: { south: { tap: { type: "keys", keys: "F1" } } } },
};
const sketch: Profile = {
  schemaVersion: 1,
  name: "Sketch",
  extends: "paint",
  layers: { base: { east: { tap: { type: "keys", keys: "E" } } } },
};
const library: ProfileEntry[] = [
  { id: "paint", name: "Paint" },
  { id: "sketch", name: "Sketch", extends: "paint" },
];

async function open(
  draft: Profile = sketch,
  options: {
    origins?: { pointer: string; profile: string }[];
    language?: Language;
    display?: LabelDisplay;
  } = {},
) {
  const api = editorAPIFixture();
  api.listProfiles.mockResolvedValue(library);
  api.getProfile.mockImplementation(async (id: string) => ({
    id,
    text: JSON.stringify(id === "paint" ? paint : draft),
    revision: "r1",
    diagnostics: [],
  }));
  api.resolveProfile.mockImplementation(async (id: string) => ({
    id,
    text: JSON.stringify(paint),
    revision: "r1",
    inherited: options.origins ?? [],
    diagnostics: [],
  }));
  const editor = createEditorStore(api as unknown as PadmateAPI);
  await editor
    .getState()
    .activate({ id: "sketch", device: "d", connection: "c" });
  await act(async () => {
    await editor.getState().refreshMetadata();
    await editor.getState().refreshParent();
  });
  render(
    <Harness
      editor={editor}
      language={options.language}
      display={options.display}
    />,
  );
  return { api, editor };
}

function Harness({
  editor,
  language = "zh-CN",
  display = "actions",
}: {
  editor: EditorStore;
  language?: Language;
  display?: LabelDisplay;
}) {
  const [store] = useState(() =>
    createPadmateStore(editorAPIFixture() as unknown as PadmateAPI),
  );
  return (
    <ControllerWorkspace
      store={store}
      info={info}
      t={translator(language)}
      editor={editor}
      language={language}
      labelDisplay={display}
      onSelect={() => {}}
    />
  );
}

const chip = (control: string) =>
  document.querySelector<HTMLElement>(`button[data-callout="${control}"]`);
const marked = (control: string) =>
  !!chip(control)?.querySelector('[data-testid="inherited-mark"]');

it("shows a binding of the parent like any other, with a small mark", async () => {
  await open();
  // south is the parent's alone.
  expect(chip("south")?.getAttribute("aria-label")).toBe("A · Ctrl+Z");
  expect(chip("south")?.getAttribute("data-state")).toBe("bound");
  expect(
    [...(chip("south")?.querySelectorAll("kbd") ?? [])].map(
      (key) => key.textContent,
    ),
  ).toEqual(["Ctrl", "Z"]);
  expect(marked("south")).toBe(true);
  expect(chip("south")?.getAttribute("title")).toContain("继承自 Paint");
  expect(
    chip("south")?.querySelector('[data-testid="inherited-mark"]')?.textContent,
  ).toBe("继承");
});

it("has no mark on a binding of its own, nor on one that overrides the parent's", async () => {
  await open({
    ...sketch,
    layers: {
      base: {
        east: { tap: { type: "keys", keys: "E" } },
        west: { tap: { type: "keys", keys: "W" } },
      },
    },
  });
  // east: both bind it; west: only the profile does.
  expect(chip("east")?.getAttribute("aria-label")).toBe("B · E");
  expect(marked("east")).toBe(false);
  expect(chip("west")?.getAttribute("aria-label")).toBe("X · W");
  expect(marked("west")).toBe(false);
  expect(marked("north")).toBe(true);
  // A control neither binds is not set.
  expect(chip("dpad.left")?.getAttribute("data-state")).toBe("unbound");
  expect(marked("dpad.left")).toBe(false);
});

it("takes the key names of the parent too", async () => {
  await open(sketch, { display: "names" });
  // The parent calls the key 笔刷; the profile does not name it.
  expect(chip("north")?.getAttribute("aria-label")).toBe("Y · 笔刷");
  expect(chip("east")?.getAttribute("aria-label")).toBe("B · B");
});

it("names the profile that wrote the binding when it is the parent's own parent", async () => {
  await open(sketch, {
    origins: [{ pointer: "/layers/base/south", profile: "default" }],
  });
  // The library does not list default here, so it stands in by its name.
  expect(chip("south")?.getAttribute("title")).toContain("继承自 默认");
  expect(chip("east")?.getAttribute("title")).not.toContain("继承自");
});

it("marks nothing for a profile that is based on nothing", async () => {
  const { extends: _extends, ...alone } = sketch;
  await open(alone as Profile);
  expect(document.querySelector('[data-testid="inherited-mark"]')).toBeNull();
  expect(chip("south")?.getAttribute("data-state")).toBe("unbound");
});

it("is in English when the language is", async () => {
  await open(sketch, { language: "en" });
  expect(
    chip("south")?.querySelector('[data-testid="inherited-mark"]')?.textContent,
  ).toBe("Inherited");
  expect(chip("south")?.getAttribute("title")).toContain(
    "Inherited from Paint",
  );
});

it("shows the set of a function key with what it inherits, marked too", async () => {
  const { editor } = await open();
  // Without a binding of its own, south does what it does otherwise ...
  await act(async () => editor.getState().setView("back"));
  expect(chip("south")?.getAttribute("aria-label")).toBe("A · F1");
  expect(marked("south")).toBe(true);
  // ... and a control the set leaves out is the usual chip, greyed, with no
  // mark: the mark is about the set that is shown.
  expect(chip("east")?.getAttribute("data-inherited")).toBe("true");
  expect(marked("east")).toBe(false);
});

it("does not copy the parent's bindings into the profile", async () => {
  const { editor } = await open();
  expect(editor.getState().draft?.layers.base).toEqual(sketch.layers.base);
});

it("shows a key the draft empties that the parent binds as not set, as the file it saves says", async () => {
  await open({
    ...sketch,
    layers: {
      base: {
        east: { tap: { type: "keys", keys: "E" } },
        south: { tap: { type: "keys", keys: "" } },
      },
    },
  });
  // The drawer's 清除快捷键 left south without a shortcut: the saved file
  // keeps it empty, so the parent's Ctrl+Z does not show.
  expect(chip("south")?.getAttribute("data-state")).toBe("unbound");
  expect(marked("south")).toBe(false);
  expect(chip("south")?.getAttribute("aria-label")).not.toContain("Ctrl+Z");
});
