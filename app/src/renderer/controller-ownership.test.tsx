// @vitest-environment jsdom
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { useState } from "react";
import { afterEach, expect, it } from "vitest";
import type { DeviceInfo, PadmateAPI } from "../shared/api";
import { ControllerWorkspace } from "./ControllerWorkspace";
import { editorAPIFixture } from "./editor-api-fixture";
import { createEditorStore } from "./editor-store";
import { translator } from "./i18n";
import { createPadmateStore } from "./store";

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
it("shows script ownership from base and global registrations by canonical IDs", async () => {
  const editor = createEditorStore(editorAPIFixture() as unknown as PadmateAPI);
  await editor
    .getState()
    .activate({ id: "paint", device: "d", connection: "c" });
  editor.setState({
    registrations: [
      {
        id: 1,
        control: "lt",
        gesture: "press",
        scope: "base",
        observe: false,
        claim: true,
        priority: 0,
        timing: {},
      },
      {
        id: 2,
        control: "rt",
        gesture: "press",
        scope: "base",
        observe: true,
        claim: false,
        priority: 0,
        timing: {},
      },
      {
        id: 3,
        control: "y",
        gesture: "press",
        scope: "global",
        observe: false,
        claim: true,
        priority: 0,
        timing: {},
      },
      {
        id: 4,
        control: "south",
        gesture: "press",
        scope: "tools",
        observe: false,
        claim: true,
        priority: 0,
        timing: {},
      },
    ],
  });
  const view = render(
    <ControllerWorkspace
      info={info}
      t={translator("en")}
      editor={editor}
      language="en"
      store={createPadmateStore(editorAPIFixture() as unknown as PadmateAPI)}
      labelDisplay="actions"
      onSelect={() => {}}
    />,
  );
  const left = view.container.querySelector<HTMLButtonElement>(
    'button[data-callout="lefttrigger"]',
  );
  const right = view.container.querySelector<HTMLButtonElement>(
    'button[data-callout="righttrigger"]',
  );
  if (!left || !right) throw new Error("Missing trigger model labels");
  expect(within(left).getByTestId("script-ownership")).toBeDefined();
  expect(within(right).queryByTestId("script-ownership")).toBeNull();
  const owned = (control: string) =>
    view.container.querySelector(
      `button[data-callout="${control}"] [data-testid="script-ownership"]`,
    );
  // A global registration owns the control in every key group, another
  // group's registration does not reach the one the editor shows.
  expect(owned("north")).not.toBeNull();
  expect(owned("south")).toBeNull();
});

it("opens the drawer's key menu or parameter menu from a chip's right-click", async () => {
  const editor = createEditorStore(editorAPIFixture() as unknown as PadmateAPI);
  await editor
    .getState()
    .activate({ id: "paint", device: "d", connection: "c" });
  function Harness() {
    const [selected, setSelected] = useState<string>();
    const [store] = useState(() =>
      createPadmateStore(editorAPIFixture() as unknown as PadmateAPI),
    );
    return (
      <ControllerWorkspace
        info={info}
        t={translator("zh-CN")}
        editor={editor}
        language="zh-CN"
        store={store}
        labelDisplay="actions"
        selected={selected}
        onSelect={setSelected}
      />
    );
  }
  const view = render(<Harness />);
  const chip = (control: string) => {
    const button = view.container.querySelector(
      `button[data-callout="${control}"]`,
    );
    if (!button) throw new Error(`Missing chip ${control}`);
    return button;
  };
  fireEvent.contextMenu(chip("north"));
  expect(chip("north").getAttribute("aria-pressed")).toBe("true");
  const keyMenu = await screen.findByRole("menu", { name: "按键操作" });
  expect(
    within(keyMenu)
      .getAllByRole("menuitem")
      .map((item) => item.textContent),
  ).toEqual(
    expect.arrayContaining([
      expect.stringContaining("复制此按键的设置"),
      expect.stringContaining("清空此按键"),
    ]),
  );
  // Closing the menu and opening another chip by a left click keeps it closed.
  fireEvent.keyDown(document, { key: "Escape" });
  expect(screen.queryByRole("menu")).toBeNull();
  fireEvent.click(chip("east"));
  expect(screen.queryByRole("menu")).toBeNull();
  fireEvent.contextMenu(chip("leftstick"));
  expect(await screen.findByRole("menu", { name: "参数操作" })).toBeDefined();
  // The press of the stick is a key: the right click opens its key actions.
  fireEvent.keyDown(document, { key: "Escape" });
  fireEvent.contextMenu(chip("leftstick.press"));
  expect(chip("leftstick.press").getAttribute("aria-pressed")).toBe("true");
  expect(chip("leftstick").getAttribute("aria-pressed")).toBe("false");
  expect(await screen.findByRole("menu", { name: "按键操作" })).toBeDefined();
});
