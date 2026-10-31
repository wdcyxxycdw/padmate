// @vitest-environment jsdom
import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { ControlIcon, controlIcon } from "./control-icon";
import { controllerLabels, familyName } from "./controller-model";

afterEach(cleanup);

const icon = (control: string, type: string, buttonMask?: number) =>
  controlIcon(
    control,
    familyName(type),
    controllerLabels(type, {}, buttonMask),
  );

describe("control icons of the key preview (UI-44)", () => {
  it("draws a control the icon sets have with its icon (ADR-0079 §7)", () => {
    // The table of which icon is in pad-icon.test.tsx; here, that it is used.
    expect(icon("south", "xboxone")).toEqual({
      kind: "art",
      name: "kenney/xbox_button_a",
      face: "green",
    });
    expect(icon("west", "ps5")).toEqual({
      kind: "art",
      name: "kenney/playstation_button_square",
      face: "pink",
    });
    expect(icon("south", "switchpro")).toEqual({
      kind: "art",
      name: "kenney/switch_button_b",
    });
    expect(icon("dpad.up", "xboxone")).toEqual({
      kind: "art",
      name: "kenney/xbox_dpad_up",
    });
    expect(icon("leftstick", "xboxone")).toEqual({
      kind: "art",
      name: "kenney/xbox_stick_l_press",
    });
    expect(icon("ls.active", "xboxone")).toEqual({
      kind: "art",
      name: "promptfont/analog-l-lrud",
    });
    expect(icon("rt", "ps5")).toEqual({
      kind: "art",
      name: "kenney/playstation_trigger_r2",
    });
    expect(icon("back", "xboxone")).toEqual({
      kind: "art",
      name: "kenney/xbox_button_view",
    });
  });

  it("keeps padmate's own drawing, or the name, where the icon sets have none", () => {
    // The diagonals of the D-pad: the cross with two arms lit.
    expect(icon("dpad.downleft", "ps5")).toEqual({
      kind: "dpad",
      arms: ["down", "left"],
    });
    // A stick's diagonals and push stages: the stick, with an arrow if any.
    expect(icon("rs.downright", "xboxone")).toEqual({
      kind: "stick",
      side: "R",
      direction: "downright",
    });
    expect(icon("ls.inner", "ps5")).toEqual({
      kind: "stick",
      side: "L",
      direction: "inner",
    });
    // Capture: a Nintendo pad's, or a left Joy-Con's held sideways.
    expect(icon("misc1", "switchpro")).toEqual({
      kind: "glyph",
      glyph: "capture",
    });
    expect(icon("guide", "joyconleft", 0xf)).toEqual({
      kind: "glyph",
      glyph: "capture",
    });
    // The extra buttons and a Joy-Con pair's side buttons have names of
    // their own, as the tray names them, never padmate's position names.
    expect(icon("misc2", "xboxone")).toEqual({
      kind: "badge",
      text: "附加键 2",
    });
    expect(icon("paddle1", "joyconpair")).toEqual({
      kind: "badge",
      text: "右 SR",
    });
    // A name SDL reports that neither set prints stays a word; so does a
    // trigger's, and a control nobody names keeps its own.
    expect(controlIcon("start", "generic", { start: "Plus Menu" })).toEqual({
      kind: "badge",
      text: "Plus Menu",
    });
    expect(controlIcon("lt", "generic", { lefttrigger: "TL" })).toEqual({
      kind: "badge",
      text: "TL",
    });
    expect(icon("bogus", "xboxone")).toEqual({ kind: "badge", text: "bogus" });
  });

  it("draws an icon in theme colours, hidden from screen readers and without text", () => {
    const { container } = render(
      <>
        <ControlIcon icon={icon("south", "xboxone")} />
        <ControlIcon icon={icon("dpad.left", "ps4")} />
      </>,
    );
    const [face, dpad] = [...container.querySelectorAll(".control-icon")];
    expect(face.getAttribute("data-kind")).toBe("art");
    expect(face.getAttribute("data-face")).toBe("green");
    expect(face.getAttribute("aria-hidden")).toBe("true");
    expect(face.querySelector("svg")?.getAttribute("data-pad-icon")).toBe(
      "kenney/xbox_button_a",
    );
    expect(dpad.hasAttribute("data-face")).toBe(false);
    expect(dpad.querySelector("svg")?.getAttribute("data-pad-icon")).toBe(
      "kenney/playstation_dpad_left",
    );
    expect(container.textContent).toBe("");
    // Colours come from the theme: no fixed colour in the markup.
    expect(container.innerHTML).not.toMatch(/#[0-9a-f]{3,6}\b|rgb/i);
  });

  it("draws its own shapes in theme colours, letters from CSS, without text", () => {
    const { container } = render(
      <>
        <ControlIcon icon={icon("dpad.upright", "xboxone")} />
        <ControlIcon icon={icon("ls.upleft", "xboxone")} />
        <ControlIcon icon={icon("misc1", "switchpro")} />
        <ControlIcon icon={icon("misc2", "xboxone")} />
      </>,
    );
    const icons = [...container.querySelectorAll(".control-icon")];
    expect(icons).toHaveLength(4);
    for (const element of icons)
      expect(element.getAttribute("aria-hidden")).toBe("true");
    const [dpad, stick, capture, badge] = icons;
    // The lit arms and the others.
    expect(dpad.querySelectorAll('[data-lit="true"]')).toHaveLength(2);
    expect(dpad.querySelectorAll('[data-lit="false"]')).toHaveLength(2);
    expect(stick.getAttribute("data-label")).toBe("L");
    expect(stick.querySelectorAll(".control-icon-arrow")).toHaveLength(1);
    expect(capture.getAttribute("data-glyph")).toBe("capture");
    expect(capture.querySelector("svg .control-icon-disc")).not.toBeNull();
    expect(badge.getAttribute("data-label")).toBe("附加键 2");
    expect(badge.querySelector("svg")).toBeNull();
    // Letters come from CSS, so the line's text is only the spoken name.
    expect(container.textContent).toBe("");
    expect(container.innerHTML).not.toMatch(/#[0-9a-f]{3,6}\b|rgb/i);
  });
});
