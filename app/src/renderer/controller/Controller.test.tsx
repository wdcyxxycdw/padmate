// @vitest-environment jsdom
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { StrictMode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { DeviceInfo } from "../../shared/api";
import { translator } from "../i18n";
import { joyCon2Pads, sdlPads } from "../testing/sdl-pads";
import { Controller } from "./Controller";
import { type Callout, controllerCallouts } from "./controller-callouts";
import type { ChipInfo } from "./controller-chips";
import * as scenes from "./controller-scene";

function chip(info: Partial<ChipInfo> & { title: string }): ChipInfo {
  return {
    detail: [],
    summary: "",
    state: "bound",
    scriptOwned: false,
    ...info,
  };
}

const info: DeviceInfo = {
  id: 1,
  name: "Studio controller",
  type: "xboxone",
  vendor: 0,
  product: 0,
  version: 0,
  guid: "",
  driver: "virtual",
  path: "",
  serial: "",
  gamepad: true,
  virtual: true,
};
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("3D controller component", () => {
  it("offers independent keyboard-accessible callouts and selects exactly one control", () => {
    const select = vi.fn();
    const { container, rerender } = render(
      <Controller
        info={info}
        t={translator("en")}
        onSelectControl={select}
        selectedControl="south"
      />,
    );
    const buttons = container.querySelectorAll<HTMLButtonElement>(
      "button[data-callout]",
    );
    // The function key (Menu by default) is tagged, not a button, and the
    // Xbox button, which padmate never maps, has no chip (ADR-0012). Each
    // stick is two buttons, its movement and its press.
    expect(buttons).toHaveLength(17);
    expect(container.querySelectorAll("[data-callout]")).toHaveLength(18);
    expect(container.querySelector('[data-callout="guide"]')).toBeNull();
    const controls = [
      "south",
      "east",
      "west",
      "north",
      "dpad.up",
      "dpad.down",
      "dpad.left",
      "dpad.right",
      "leftstick",
      "leftstick.press",
      "rightstick",
      "rightstick.press",
    ];
    for (const control of controls) {
      const button = container.querySelector<HTMLButtonElement>(
        `button[data-callout="${control}"]`,
      );
      expect(button).not.toBeNull();
      if (button) fireEvent.click(button);
      expect(select).toHaveBeenLastCalledWith(control);
    }
    expect(
      container
        .querySelector('button[data-callout="south"]')
        ?.getAttribute("aria-pressed"),
    ).toBe("true");
    rerender(
      <Controller
        info={info}
        t={translator("en")}
        onSelectControl={select}
        selectedControl="dpad.left"
      />,
    );
    expect(
      container.querySelectorAll('button[aria-pressed="true"]'),
    ).toHaveLength(1);
    expect(
      container
        .querySelector('button[data-callout="south"]')
        ?.getAttribute("aria-pressed"),
    ).toBe("false");
  });
  it("describes the group with one instruction and names the state list", () => {
    render(
      <Controller
        info={info}
        t={translator("en")}
        language="en"
        input={{
          buttons: { south: true },
          axes: { lefttrigger: 16384 },
        }}
      />,
    );
    const group = screen.getByRole("group");
    // aria-description is dropped: aria-describedby wins over it.
    expect(group.hasAttribute("aria-description")).toBe(false);
    const description = document.getElementById(
      group.getAttribute("aria-describedby") ?? "",
    );
    expect(description?.textContent).toBe(
      "Click a label to edit that control · Press a button on the controller to light up its label",
    );
    // The list is its own element, with its own name, and no instruction row.
    const list = screen.getByRole("list", { name: "Controller state" });
    expect(group.contains(list)).toBe(true);
    expect(description?.contains(list)).toBe(false);
    expect(list.textContent).not.toContain("Click a label");
    expect(
      (list.querySelector('[data-control="south"]') as HTMLElement).textContent,
    ).toMatch(/Pressed/);
    expect(
      (list.querySelector('[data-control="lefttrigger"]') as HTMLElement)
        .textContent,
    ).toMatch(/50%$/);
  });
  it("names the state list in Chinese too", () => {
    render(<Controller info={info} t={translator("zh-CN")} language="zh-CN" />);
    expect(screen.getByRole("list", { name: "手柄按键状态" })).toBeDefined();
  });

  it("tags the function key that cannot be opened, whichever of View and Menu it is", () => {
    const select = vi.fn();
    const menu = vi.fn();
    const { container } = render(
      <Controller
        info={info}
        t={translator("en")}
        onSelectControl={select}
        onMenuControl={menu}
        functionKey="back"
        chips={{
          back: {
            title: "Function key",
            detail: [],
            summary: "While held, other keys switch to another set",
            state: "function",
            scriptOwned: false,
          },
        }}
      />,
    );
    const chip = container.querySelector<HTMLElement>(
      '[data-callout="back"]',
    ) as HTMLElement;
    expect(chip.tagName).toBe("DIV");
    expect(chip.dataset.state).toBe("function");
    expect(chip.textContent).toContain("Function key");
    expect(chip.getAttribute("aria-label")).toBe("View · Function key");
    expect(chip.title).toBe(
      "View · Function key\nWhile held, other keys switch to another set",
    );
    fireEvent.click(chip);
    expect(fireEvent.contextMenu(chip)).toBe(true);
    expect(select).not.toHaveBeenCalled();
    expect(menu).not.toHaveBeenCalled();
    // The pad's own name for it stays on the badge and in the layout.
    expect(container.querySelector('button[data-callout="back"]')).toBeNull();
    // The other is an ordinary control.
    expect(
      container.querySelector('button[data-callout="start"]'),
    ).not.toBeNull();
  });

  it("tags the function key without a chip given for it, Menu until it is told another", () => {
    const tagged = (container: HTMLElement) =>
      [
        ...container.querySelectorAll<HTMLElement>('[data-state="function"]'),
      ].map((node) => node.dataset.callout);
    const { container, rerender } = render(
      <Controller info={info} t={translator("en")} />,
    );
    expect(tagged(container)).toEqual(["start"]);
    rerender(
      <Controller info={info} t={translator("en")} functionKey="back" />,
    );
    expect(tagged(container)).toEqual(["back"]);
  });

  it("marks a chip that shows what a control does otherwise", () => {
    const { container } = render(
      <Controller
        info={info}
        t={translator("en")}
        chips={{
          south: {
            title: "Normal: Undo",
            detail: [],
            summary: "",
            state: "unbound",
            scriptOwned: false,
            inherited: true,
          },
        }}
      />,
    );
    const chip = container.querySelector<HTMLElement>(
      'button[data-callout="south"]',
    ) as HTMLElement;
    expect(chip.dataset.inherited).toBe("true");
    expect(chip.dataset.state).toBe("unbound");
    expect(chip.getAttribute("aria-label")).toBe("A · Normal: Undo");
    const other = container.querySelector<HTMLElement>(
      'button[data-callout="east"]',
    ) as HTMLElement;
    expect(other.dataset.inherited).toBeUndefined();
  });

  it("selects a right-clicked chip and asks for its menu instead of the browser's", () => {
    const calls: string[] = [];
    const { container } = render(
      <Controller
        info={info}
        t={translator("en")}
        onSelectControl={(control) => calls.push(`select ${control}`)}
        onMenuControl={(control) => calls.push(`menu ${control}`)}
      />,
    );
    const chip = (control: string) => {
      const button = container.querySelector(
        `button[data-callout="${control}"]`,
      );
      if (!button) throw new Error(`Missing chip ${control}`);
      return button;
    };
    // fireEvent returns false when the handler prevented the default menu.
    expect(fireEvent.contextMenu(chip("north"))).toBe(false);
    expect(fireEvent.contextMenu(chip("lefttrigger"))).toBe(false);
    expect(calls).toEqual([
      "select north",
      "menu north",
      "select lefttrigger",
      "menu lefttrigger",
    ]);
    // A left click only selects.
    fireEvent.click(chip("dpad.left"));
    expect(calls.at(-1)).toBe("select dpad.left");
    expect(calls).toHaveLength(5);
  });

  it("reports missing WebGL without calling jsdom getContext and preserves input semantics", () => {
    const context = vi.spyOn(HTMLCanvasElement.prototype, "getContext");
    const { container, rerender } = render(
      <Controller
        info={info}
        t={translator("en")}
        input={{ buttons: { south: true }, axes: {} }}
      />,
    );
    expect(screen.getByRole("group").getAttribute("aria-label")).toBe(
      "Studio controller · Live controller",
    );
    expect(screen.getByRole("group").getAttribute("data-renderer")).toBe(
      "unavailable",
    );
    expect(screen.getByText(/WebGL could not start/)).toBeDefined();
    expect(context).not.toHaveBeenCalled();
    expect(
      container
        .querySelector('[data-control="south"]')
        ?.getAttribute("data-pressed"),
    ).toBe("true");
    rerender(<Controller info={info} t={translator("en")} />);
    expect(
      container
        .querySelector('[data-control="south"]')
        ?.getAttribute("data-pressed"),
    ).toBe("false");
  });

  it("marks WebGL only after drawing and builds a new scene when the model changes", () => {
    vi.stubGlobal("WebGL2RenderingContext", class {});
    const update = vi.fn();
    const dispose = vi.fn();
    let ready = (_callouts: Callout[]) => {};
    const create = vi
      .spyOn(scenes, "createControllerScene")
      .mockImplementation((_host, _info, onReady) => {
        ready = onReady;
        return { update, dispose };
      });
    const { container, rerender, unmount } = render(
      <Controller
        info={info}
        t={translator("en")}
        input={{ buttons: { south: true }, axes: {} }}
        selectedControl="south"
        chips={{ back: chip({ title: "Not set", state: "unbound" }) }}
      />,
    );
    // The scene gets the stage host to put the session's canvas in, the
    // callout layer and what the first frame shows.
    const [host, modelInfo, , , labels, initial] = create.mock.calls[0];
    expect(host).toBe(container.querySelector(".stage-host"));
    expect(modelInfo).toEqual({
      type: info.type,
      vendor: info.vendor,
      product: info.product,
    });
    expect(labels).toBe(container.querySelector(".controller-callouts"));
    expect(initial).toEqual({
      input: { buttons: { south: true }, axes: {} },
      selected: "south",
      unbound: ["back"],
    });
    expect(screen.getByRole("group").getAttribute("data-renderer")).toBe(
      "loading",
    );
    act(() => ready(controllerCallouts(info)));
    expect(screen.getByRole("group").getAttribute("data-renderer")).toBe(
      "webgl",
    );
    rerender(<Controller info={{ ...info }} t={translator("zh-CN")} />);
    expect(create).toHaveBeenCalledTimes(1);
    rerender(
      <Controller info={{ ...info, type: "ps5" }} t={translator("en")} />,
    );
    expect(create).toHaveBeenCalledTimes(2);
    expect(dispose).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("group").getAttribute("data-renderer")).toBe(
      "loading",
    );
    unmount();
    expect(dispose).toHaveBeenCalledTimes(2);
  });

  it("moves a Joy-Con's chips to the places the built model arranged, keeping its own labels", () => {
    vi.stubGlobal("WebGL2RenderingContext", class {});
    let ready = (_callouts: Callout[]) => {};
    const update = vi.fn();
    vi.spyOn(scenes, "createControllerScene").mockImplementation(
      (_host, _info, onReady) => {
        ready = onReady;
        return { update, dispose: vi.fn() };
      },
    );
    const sideways = { ...info, id: 6, ...sdlPads.joyConRightSideways };
    const placement = (container: HTMLElement, control: string) =>
      container
        .querySelector(`[data-callout="${control}"]`)
        ?.getAttribute("data-placement");
    const { container, rerender } = render(
      <Controller info={sideways} t={translator("en")} language="en" />,
    );
    // Before the model, the table's first choice: R and ZR in the corner at
    // the top, the face buttons below.
    expect(placement(container, "paddle1")).toBe("top");
    expect(
      container
        .querySelector('[data-callout="paddle1"]')
        ?.getAttribute("data-side"),
    ).toBe("right");
    expect(placement(container, "south")).toBe("bottom");
    // The model arranged them otherwise (and named them in Chinese, which
    // the chips do not take).
    const arranged = controllerCallouts(sideways, "zh-CN").map((callout) =>
      callout.control === "south"
        ? { ...callout, placement: "right" as const, slot: 5 }
        : callout,
    );
    expect(screen.getByRole("group").getAttribute("data-arranged")).toBeNull();
    const before = update.mock.calls.length;
    act(() => ready(arranged));
    expect(screen.getByRole("group").getAttribute("data-arranged")).toBe(
      "true",
    );
    // The leaders start on the chips as measured, and the chips just moved:
    // the scene draws them again.
    expect(update.mock.calls.length).toBeGreaterThan(before);
    expect(placement(container, "south")).toBe("right");
    expect(placement(container, "paddle1")).toBe("top");
    expect(
      container
        .querySelector('[data-callout="paddle1"]')
        ?.getAttribute("aria-label"),
    ).toMatch(/^R · /);
    // Another model starts again from the table.
    rerender(
      <Controller
        info={{ ...info, id: 7, ...sdlPads.joyConLeftSideways }}
        t={translator("en")}
        language="en"
      />,
    );
    expect(placement(container, "south")).toBe("bottom");
  });

  it("gives a Joy-Con's rows the wide layout and the model's size, and no other pad", () => {
    vi.stubGlobal("WebGL2RenderingContext", class {});
    let ready = (
      _callouts: Callout[],
      _frame?: { width: number; halfHeight: number },
    ) => {};
    vi.spyOn(scenes, "createControllerScene").mockImplementation(
      (_host, _info, onReady) => {
        ready = onReady;
        return { update: vi.fn(), dispose: vi.fn() };
      },
    );
    const labels = (container: HTMLElement) =>
      container.querySelector<HTMLElement>(".controller-labels");
    const pair = { ...info, id: 8, ...joyCon2Pads.joyConPair };
    const { container, rerender } = render(
      <Controller info={pair} t={translator("en")} />,
    );
    // A pair's row above stays narrow; both rows of a single one are wide.
    expect(labels(container)?.dataset.wideRows).toBe("true");
    expect(labels(container)?.dataset.wideTop).toBeUndefined();
    expect(labels(container)?.dataset.corners).toBe("true");
    expect(labels(container)?.dataset.columns).toBe("true");
    expect(labels(container)?.style.getPropertyValue("--top-count")).toBe("2");
    // The model's size is the stylesheet's default until it is built.
    expect(labels(container)?.style.getPropertyValue("--model-h")).toBe("");
    act(() =>
      ready(controllerCallouts(pair), { width: 3.45, halfHeight: 2.07 }),
    );
    expect(labels(container)?.style.getPropertyValue("--model-w")).toBe("3.45");
    expect(labels(container)?.style.getPropertyValue("--model-h")).toBe("2.07");
    rerender(
      <Controller
        info={{ ...info, id: 9, ...sdlPads.joyConRightSideways }}
        t={translator("en")}
      />,
    );
    expect(labels(container)?.dataset.wideTop).toBe("true");
    expect(labels(container)?.style.getPropertyValue("--model-h")).toBe("");
    // Any other pad, and a Joy-Con the table does not know, keep the rows.
    rerender(<Controller info={info} t={translator("en")} />);
    expect(labels(container)?.dataset.wideRows).toBeUndefined();
    rerender(
      <Controller
        info={{ ...info, type: "joyconleft", buttonMask: 0, axisMask: 0 }}
        t={translator("en")}
      />,
    );
    expect(labels(container)?.dataset.wideRows).toBeUndefined();
  });

  it("makes its scene with the one it is given", () => {
    vi.stubGlobal("WebGL2RenderingContext", class {});
    const shared = vi
      .spyOn(scenes, "createControllerScene")
      .mockReturnValue({ update: vi.fn(), dispose: vi.fn() });
    const own = vi
      .fn<typeof scenes.createControllerScene>()
      .mockReturnValue({ update: vi.fn(), dispose: vi.fn() });
    const { unmount } = render(
      <Controller info={info} t={translator("en")} createScene={own} />,
    );
    expect(own).toHaveBeenCalledTimes(1);
    expect(shared).not.toHaveBeenCalled();
    unmount();
  });

  it("lets go of the scene on each effect cleanup, StrictMode's replay included", () => {
    vi.stubGlobal("WebGL2RenderingContext", class {});
    const dispose = vi.fn();
    const create = vi
      .spyOn(scenes, "createControllerScene")
      .mockReturnValue({ update: vi.fn(), dispose });
    const { unmount } = render(
      <StrictMode>
        <Controller info={info} t={translator("en")} />
      </StrictMode>,
    );
    // Disposing only lets go of the retained stage (controller-scene tests),
    // so the replay builds nothing new on the GPU.
    expect(create).toHaveBeenCalledTimes(2);
    expect(dispose).toHaveBeenCalledOnce();
    unmount();
    expect(dispose).toHaveBeenCalledTimes(2);
  });
  it("shows what each control does and its ownership", () => {
    const chips: Record<string, ChipInfo> = {
      south: chip({
        title: "Undo",
        detail: [{ keys: "Ctrl+Z" }],
        summary: "Ctrl+Z",
      }),
      west: chip({
        title: "Smart eraser",
        detail: [{ text: "Handled by script main.ts" }],
        state: "script",
        scriptOwned: true,
      }),
      north: chip({
        title: "Brush",
        detail: [{ keys: "B" }, { gesture: "Hold", keys: "Shift+B" }],
      }),
      east: chip({ title: "E", titleKeys: { keys: "E" } }),
      leftstick: chip({
        title: "Pan",
        detail: [{ chords: ["Up", "Down", "Left", "Right"] }],
      }),
      "leftstick.press": chip({
        title: "Eyedropper",
        detail: [{ keys: "Alt" }],
        scriptOwned: true,
        state: "script",
      }),
      "rightstick.press": chip({ title: "Locked", state: "locked" }),
      start: chip({ title: "Not set", state: "unbound" }),
      back: chip({ title: "Blocked", state: "locked" }),
      lefttrigger: chip({
        title: "Eyedropper",
        detail: [{ keys: "Alt", hold: true }],
      }),
    };
    const { container } = render(
      <Controller
        info={info}
        t={translator("en")}
        language="en"
        chips={chips}
        input={{ buttons: {}, axes: { lefttrigger: 16384 } }}
      />,
    );
    const button = (name: string) => screen.getByRole("button", { name });
    const south = button("A · Undo");
    expect(south.getAttribute("data-callout")).toBe("south");
    // Edits are saved at once: nothing marks a chip as unsaved.
    expect(south.hasAttribute("data-dirty")).toBe(false);
    expect(south.title.split("\n")).toEqual(["A · Undo", "Ctrl+Z"]);
    expect(
      [...south.querySelectorAll(".stage-chip-detail kbd")].map(
        (key) => key.textContent,
      ),
    ).toEqual(["Ctrl", "Z"]);
    const west = button("X · Smart eraser");
    expect(within(west).getByTestId("script-ownership").textContent).toBe("S");
    expect(west.textContent).toContain("Handled by script main.ts");
    expect(within(south).queryByTestId("script-ownership")).toBeNull();
    expect(button("Y · Brush").textContent).toContain("Hold");
    expect(
      button("B · E").querySelector(".stage-chip-title kbd")?.textContent,
    ).toBe("E");
    expect(button("LS Move · Pan").querySelectorAll("kbd")).toHaveLength(4);
    // A stick's press is its own button, with its own state and marks.
    const press = button("LS Press · Eyedropper");
    expect(press.getAttribute("data-callout")).toBe("leftstick.press");
    expect(press.getAttribute("data-state")).toBe("script");
    expect(within(press).getByTestId("script-ownership").textContent).toBe("S");
    expect(
      within(button("LS Move · Pan")).queryByTestId("script-ownership"),
    ).toBeNull();
    const locked = button("RS Press · Locked");
    expect(locked.querySelector(".stage-chip-lock")).not.toBeNull();
    expect(locked.title).toContain("Only editable in the file");
    expect(button("Menu · Not set").getAttribute("data-state")).toBe("unbound");
    const back = button("View · Blocked");
    expect(back.querySelector(".stage-chip-lock")).not.toBeNull();
    expect(back.title).toContain("Only editable in the file");
    const trigger = button("LT · Eyedropper");
    expect(trigger.getAttribute("data-active")).toBe("true");
    expect(trigger.textContent).toContain("50%");
    expect(trigger.textContent).toContain("held");
    // Controls without chip data still get a chip named after the control.
    expect(button("RB · RB").getAttribute("data-state")).toBe("bound");
    expect(container.querySelector(".controller-hint")).toBeNull();
  });

  it("puts the divider between the keycaps of a knob chip", () => {
    // A knob shows its two ticks as one pair: `] / [`, in the detail line
    // and, when it is the whole summary, in the title.
    const chips: Record<string, ChipInfo> = {
      rightstick: chip({
        title: "Knob",
        detail: [{ chords: ["]", "["], divider: "/" }],
      }),
      leftstick: chip({
        title: "] / [",
        titleKeys: { chords: ["]", "["], divider: "/" },
      }),
      // Without a divider the keycaps follow each other, as for directions.
      south: chip({
        title: "Directions",
        detail: [{ chords: ["Up", "Down"] }],
      }),
    };
    render(
      <Controller
        info={info}
        t={translator("en")}
        language="en"
        chips={chips}
      />,
    );
    const button = (name: string) => screen.getByRole("button", { name });
    const parts = (element: Element | null) =>
      [...(element?.children ?? [])].map((child) => child.textContent);
    expect(
      parts(button("RS Move · Knob").querySelector(".stage-chip-detail")),
    ).toEqual(["]", "/", "["]);
    expect(
      button("RS Move · Knob").querySelectorAll(".stage-chip-detail kbd"),
    ).toHaveLength(2);
    const title = button("LS Move · ] / [").querySelector(".stage-chip-title");
    expect(parts(title)).toEqual(["]", "/", "["]);
    expect(title?.querySelectorAll("kbd")).toHaveLength(2);
    expect(
      parts(button("A · Directions").querySelector(".stage-chip-detail")),
    ).toEqual(["↑", "↓"]);
  });

  it("lays out chips by region and only shows the hint when asked", () => {
    const { container, rerender } = render(
      <Controller info={info} t={translator("zh-CN")} hint />,
    );
    const placement = (control: string) => {
      const chip = container.querySelector(`[data-callout="${control}"]`);
      return [
        chip?.getAttribute("data-placement"),
        chip?.getAttribute("data-side"),
      ];
    };
    expect(placement("lefttrigger")).toEqual(["top", "left"]);
    expect(placement("rightshoulder")).toEqual(["top", "right"]);
    expect(placement("back")).toEqual(["top", null]);
    expect(placement("guide")).toEqual([undefined, undefined]);
    expect(placement("dpad.left")).toEqual(["left", null]);
    // A stick is one chip of two cells, which sits in its column.
    const stick = (control: string) =>
      container.querySelector<HTMLElement>(`[data-callout-group="${control}"]`);
    expect(stick("rightstick")?.getAttribute("data-placement")).toBe("right");
    expect(stick("leftstick")?.getAttribute("data-placement")).toBe("left");
    expect(stick("rightstick")?.style.getPropertyValue("--span")).toBe("2");
    expect(stick("rightstick")?.style.getPropertyValue("--slot")).toBe("6");
    for (const control of ["leftstick", "leftstick.press"])
      expect(
        stick("leftstick")?.querySelector(`[data-callout="${control}"]`),
        control,
      ).not.toBeNull();
    expect(container.querySelector(".controller-heading")).toBeNull();
    expect(container.querySelector(".controller-hint")?.textContent).toBe(
      "点击标签即可编辑对应按键 · 按下手柄按键时，对应标签会亮起",
    );
    rerender(
      <Controller info={{ ...info, type: "ps5" }} t={translator("en")} />,
    );
    expect(placement("touchpad")).toEqual(["top", null]);
    expect(placement("guide")).toEqual([undefined, undefined]);
    expect(container.querySelector(".controller-hint")).toBeNull();
    expect(
      container
        .querySelector('[data-callout="south"] .stage-glyph')
        ?.getAttribute("data-face"),
    ).toBe("ps-blue");
  });

  it("draws each chip's control with its icon (ADR-0013)", () => {
    const { container, rerender } = render(
      <Controller info={info} t={translator("zh-CN")} />,
    );
    const glyph = (control: string) =>
      container.querySelector<HTMLElement>(
        `[data-callout="${control}"] .stage-glyph`,
      );
    const art = (control: string) =>
      glyph(control)?.querySelector("svg")?.getAttribute("data-pad-icon");
    expect(art("south")).toBe("kenney/xbox_button_a");
    expect(glyph("south")?.dataset.icon).toBe("true");
    expect(glyph("south")?.dataset.face).toBe("green");
    expect(glyph("south")?.textContent).toBe("");
    expect(art("leftshoulder")).toBe("kenney/xbox_lb");
    expect(art("righttrigger")).toBe("kenney/xbox_rt");
    expect(art("dpad.up")).toBe("kenney/xbox_dpad_up");
    expect(art("back")).toBe("kenney/xbox_button_view");
    // A stick's two cells: the stick turning both ways, and its click.
    expect(art("leftstick")).toBe("promptfont/analog-l-cl");
    expect(art("leftstick.press")).toBe("kenney/xbox_stick_l_press");
    expect(art("rightstick")).toBe("promptfont/analog-r-cl");
    rerender(
      <Controller info={{ ...info, type: "ps5" }} t={translator("en")} />,
    );
    expect(art("south")).toBe("kenney/playstation_button_cross");
    expect(art("touchpad")).toBe("kenney/playstation5_touchpad");
    expect(art("rightstick.press")).toBe("kenney/playstation_stick_r_press");
    rerender(
      <Controller info={{ ...info, type: "switchpro" }} t={translator("en")} />,
    );
    expect(art("east")).toBe("kenney/switch_button_a");
    expect(art("lefttrigger")).toBe("kenney/switch_button_zl");
    expect(art("start")).toBe("kenney/switch_button_plus");
    expect(art("leftstick.press")).toBe("kenney/switch_stick_l_press");
  });

  it("keeps chip lines inline so a shortcut that does not fit ends in an ellipsis", () => {
    // jsdom does not lay out; this checks the cascade that makes Chromium hide
    // an overflowing keycap whole and draw "…" (a flex line clips mid-key).
    const style = document.createElement("style");
    style.textContent = readFileSync(
      resolve(__dirname, "controller-3d.css"),
      "utf8",
    );
    document.head.append(style);
    try {
      const { container } = render(
        <Controller
          info={info}
          t={translator("en")}
          chips={{
            south: chip({
              title: "Ctrl+Shift+Z",
              titleKeys: { keys: "Ctrl+Shift+Z" },
              detail: [{ gesture: "Hold", keys: ["Ctrl+K", "S"] }],
            }),
          }}
        />,
      );
      const south = container.querySelector('[data-callout="south"]');
      const lines = [".stage-chip-title", ".stage-chip-detail"].map(
        (selector) => south?.querySelector<HTMLElement>(selector),
      );
      for (const line of lines) {
        if (!line) throw new Error("Missing chip line");
        const css = getComputedStyle(line);
        expect([
          css.display,
          css.overflow,
          css.textOverflow,
          css.whiteSpace,
        ]).toEqual(["block", "hidden", "ellipsis", "nowrap"]);
        for (const group of line.querySelectorAll<HTMLElement>(
          ".keycaps, .keycaps-chord",
        ))
          expect(getComputedStyle(group).display).toBe("inline");
        const keys = [...line.querySelectorAll<HTMLElement>(".kc")];
        expect(keys.length).toBeGreaterThan(1);
        for (const key of keys)
          expect(getComputedStyle(key).display).toBe("inline-flex");
      }
    } finally {
      style.remove();
    }
  });

  it("lights a stick's chip while the stick is pushed past drift", () => {
    const { container, rerender } = render(
      <Controller
        info={info}
        t={translator("en")}
        input={{ buttons: {}, axes: { leftx: 2000, righty: -20000 } }}
      />,
    );
    const active = (control: string) =>
      container
        .querySelector(`button[data-callout="${control}"]`)
        ?.getAttribute("data-active");
    // Drift stays dark; a real push lights the movement cell, and only it.
    expect(active("leftstick")).toBe("false");
    expect(active("rightstick")).toBe("true");
    expect(active("rightstick.press")).toBe("false");
    // The chip shows no percentage for a stick; that is the trigger's.
    expect(
      container.querySelector('[data-callout="rightstick"] .stage-chip-pct'),
    ).toBeNull();
    // The click lights the press cell, not the movement.
    rerender(
      <Controller
        info={info}
        t={translator("en")}
        input={{ buttons: { leftstick: true }, axes: {} }}
      />,
    );
    expect(active("leftstick.press")).toBe("true");
    expect(active("leftstick")).toBe("false");
    expect(active("rightstick")).toBe("false");
    expect(active("rightstick.press")).toBe("false");
    rerender(<Controller info={info} t={translator("en")} />);
    expect(active("leftstick")).toBe("false");
    expect(active("leftstick.press")).toBe("false");
  });

  it("names the two cells of a stick, and selects them apart", () => {
    const select = vi.fn();
    const menu = vi.fn();
    const { container, rerender } = render(
      <Controller
        info={info}
        t={translator("en")}
        language="en"
        onSelectControl={select}
        onMenuControl={menu}
        selectedControl="leftstick.press"
        chips={{
          leftstick: chip({ title: "Direction keys" }),
          "leftstick.press": chip({ title: "Not set", state: "unbound" }),
        }}
      />,
    );
    const move = screen.getByRole("button", {
      name: "LS Move · Direction keys",
    });
    const press = screen.getByRole("button", { name: "LS Press · Not set" });
    expect(move.getAttribute("aria-pressed")).toBe("false");
    expect(press.getAttribute("aria-pressed")).toBe("true");
    expect(move.getAttribute("data-callout")).toBe("leftstick");
    expect(press.getAttribute("data-callout")).toBe("leftstick.press");
    expect(press.getAttribute("data-state")).toBe("unbound");
    // The top cell is the movement, the bottom the press.
    expect(move.nextElementSibling).toBe(press);
    // Without chip data they are still named after the stick.
    expect(screen.getByRole("button", { name: "RS Move · RS" })).toBeDefined();
    expect(screen.getByRole("button", { name: "RS Press · RS" })).toBeDefined();
    // A click selects the cell, a right click selects it and asks for its menu.
    fireEvent.click(move);
    expect(select).toHaveBeenLastCalledWith("leftstick");
    expect(menu).not.toHaveBeenCalled();
    expect(fireEvent.contextMenu(press)).toBe(false);
    expect(select).toHaveBeenLastCalledWith("leftstick.press");
    expect(menu).toHaveBeenCalledWith("leftstick.press");
    // Only one of the 18 buttons is pressed at a time.
    expect(
      container.querySelectorAll('button[aria-pressed="true"]'),
    ).toHaveLength(1);
    rerender(
      <Controller
        info={info}
        t={translator("zh-CN")}
        language="zh-CN"
        selectedControl="leftstick"
        chips={{ leftstick: chip({ title: "方向键" }) }}
      />,
    );
    expect(
      screen
        .getByRole("button", { name: "LS 旋转 · 方向键" })
        .getAttribute("aria-pressed"),
    ).toBe("true");
    expect(screen.getByRole("button", { name: "LS 点按 · LS" })).toBeDefined();
  });

  it("calls the click L3 on a PlayStation pad", () => {
    render(
      <Controller
        info={{ ...info, type: "ps5" }}
        t={translator("zh-CN")}
        language="zh-CN"
      />,
    );
    expect(screen.getByRole("button", { name: "LS 旋转 · L3" })).toBeDefined();
    expect(screen.getByRole("button", { name: "L3 点按 · L3" })).toBeDefined();
  });

  it("leaves the stick's leader to both cells, dashed only when both are unset", () => {
    vi.stubGlobal("WebGL2RenderingContext", class {});
    const update = vi.fn();
    vi.spyOn(scenes, "createControllerScene").mockReturnValue({
      update,
      dispose: vi.fn(),
    });
    const unset = (title: string) => chip({ title, state: "unbound" });
    const { rerender } = render(
      <Controller
        info={info}
        t={translator("en")}
        selectedControl="leftstick.press"
        chips={{
          leftstick: unset("Not set"),
          "leftstick.press": chip({ title: "Tab" }),
        }}
      />,
    );
    // One cell is set: the leader is solid. Either cell selects the leader.
    expect(update).toHaveBeenLastCalledWith(undefined, "leftstick", []);
    rerender(
      <Controller
        info={info}
        t={translator("en")}
        selectedControl="rightstick"
        chips={{
          leftstick: unset("Not set"),
          "leftstick.press": unset("Not set"),
          rightstick: unset("Not set"),
          "rightstick.press": chip({ title: "Tab" }),
        }}
      />,
    );
    expect(update).toHaveBeenLastCalledWith(undefined, "rightstick", [
      "leftstick",
    ]);
  });

  it("passes unset controls to the scene so their leaders are dashed", () => {
    vi.stubGlobal("WebGL2RenderingContext", class {});
    const update = vi.fn();
    vi.spyOn(scenes, "createControllerScene").mockReturnValue({
      update,
      dispose: vi.fn(),
    });
    const { rerender } = render(
      <Controller
        info={info}
        t={translator("en")}
        chips={{ back: chip({ title: "Not set", state: "unbound" }) }}
      />,
    );
    expect(update).toHaveBeenLastCalledWith(undefined, undefined, ["back"]);
    rerender(
      <Controller
        info={info}
        t={translator("en")}
        selectedControl="south"
        chips={{ back: chip({ title: "Paste" }) }}
      />,
    );
    expect(update).toHaveBeenLastCalledWith(undefined, "south", []);
  });
});

describe("the controls of the connected pad", () => {
  const ids = (container: HTMLElement) =>
    [...container.querySelectorAll<HTMLElement>("[data-callout]")]
      .map((node) => node.dataset.callout)
      .sort();

  it("gives each extra button SDL reports a chip below the controller", () => {
    const { container } = render(
      <Controller
        info={{ ...info, ...sdlPads.ps5Edge }}
        t={translator("en")}
      />,
    );
    for (const control of ["misc1", "paddle1", "paddle2", "paddle3", "paddle4"])
      expect(
        container
          .querySelector(`button[data-callout="${control}"]`)
          ?.getAttribute("data-placement"),
        control,
      ).toBe("bottom");
    const art = (control: string) =>
      container
        .querySelector(`[data-callout="${control}"] svg`)
        ?.getAttribute("data-pad-icon");
    expect(art("misc1")).toBe("kenney/playstation5_button_mute");
    expect(art("paddle1")).toBe("kenney/playstation5_elite_rb");
    expect(art("paddle4")).toBe("kenney/playstation5_elite_fn_l");
  });

  it("draws Capture, which neither icon set has, with its own symbol", () => {
    const { container } = render(
      <Controller
        info={{ ...info, ...sdlPads.switchPro }}
        t={translator("en")}
      />,
    );
    const glyph = container.querySelector(
      '[data-callout="misc1"] .stage-glyph',
    );
    expect(glyph?.querySelector("svg rect")).not.toBeNull();
    expect(glyph?.textContent).toBe("");
  });

  it("follows the pad as another one is chosen or plugged in", () => {
    const { container, rerender } = render(
      <Controller info={info} t={translator("en")} />,
    );
    expect(ids(container)).not.toContain("misc1");
    expect(ids(container)).toContain("dpad.up");
    rerender(
      <Controller
        info={{ ...info, id: 2, ...sdlPads.joyConLeftSideways }}
        t={translator("en")}
      />,
    );
    // A single Joy-Con held sideways: no D-pad, right stick, triggers or −.
    expect(ids(container)).toEqual(
      [
        "east",
        "leftshoulder",
        "leftstick",
        "leftstick.press",
        "north",
        "paddle2",
        "paddle4",
        "south",
        "start",
        "rightshoulder",
        "west",
      ].sort(),
    );
    rerender(
      <Controller
        info={{ ...info, id: 3, ...sdlPads.joyConPair }}
        t={translator("en")}
      />,
    );
    expect(ids(container)).toEqual(
      expect.arrayContaining(["misc1", "dpad.up", "rightstick"]),
    );
    // The pair has no SL and SR chips though the pad reports them.
    expect(ids(container)).not.toContain("paddle1");
    rerender(
      <Controller
        info={{ ...info, id: 5, ...sdlPads.joyConRightUpright }}
        t={translator("en")}
      />,
    );
    expect(
      container
        .querySelector('[data-callout="paddle1"] svg')
        ?.getAttribute("data-pad-icon"),
    ).toBe("kenney/switch_button_sr");
    // Unplugged, the pad's info goes back to what SDL did not describe.
    rerender(<Controller info={info} t={translator("en")} />);
    expect(ids(container)).not.toContain("paddle1");
  });

  it("lists the state of the pad's own controls only", () => {
    const { container } = render(
      <Controller
        info={{ ...info, ...sdlPads.joyConLeftSideways }}
        t={translator("en")}
      />,
    );
    const listed = [
      ...container.querySelectorAll<HTMLElement>(
        ".controller-accessible-state li",
      ),
    ].map((node) => node.dataset.control);
    expect(listed).toContain("paddle2");
    expect(listed).not.toContain("dpad.up");
    expect(listed).not.toContain("misc2");
  });

  it("gives a Joy-Con 2 pair its C button, named in the interface's language, and its row of two chips below", () => {
    const pair = { ...info, id: 4, ...joyCon2Pads.joyConPair };
    const { container, rerender } = render(
      <Controller info={pair} t={translator("en")} language="en" />,
    );
    const c = container.querySelector<HTMLElement>('[data-callout="misc2"]');
    expect(c?.getAttribute("aria-label")).toMatch(/^C · /);
    expect(c?.getAttribute("data-placement")).toBe("bottom");
    expect(
      container
        .querySelector<HTMLElement>(".controller-labels")
        ?.style.getPropertyValue("--row-count"),
    ).toBe("2");
    rerender(<Controller info={pair} t={translator("zh-CN")} />);
    expect(
      container
        .querySelector('[data-callout="misc2"]')
        ?.getAttribute("aria-label"),
    ).toMatch(/^C 键 · /);
    // A Switch OLED pair has no such button: the Capture button alone.
    rerender(
      <Controller
        info={{ ...info, id: 3, ...sdlPads.joyConPair }}
        t={translator("en")}
      />,
    );
    expect(container.querySelector('[data-callout="misc2"]')).toBeNull();
    expect(
      container
        .querySelector<HTMLElement>(".controller-labels")
        ?.style.getPropertyValue("--row-count"),
    ).toBe("1");
  });
});
