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
  it("draws face buttons the way each family prints them", () => {
    // Xbox: coloured letters.
    expect(icon("south", "xboxone")).toEqual({
      kind: "face",
      text: "A",
      color: "green",
    });
    expect(icon("east", "xbox360")).toEqual({
      kind: "face",
      text: "B",
      color: "red",
    });
    expect(icon("west", "xboxone")).toMatchObject({ text: "X", color: "blue" });
    expect(icon("north", "xboxone")).toMatchObject({
      text: "Y",
      color: "yellow",
    });
    // PlayStation: the four symbols, drawn.
    expect(icon("south", "ps5")).toEqual({
      kind: "symbol",
      shape: "cross",
      color: "blue",
    });
    expect(icon("east", "ps4")).toEqual({
      kind: "symbol",
      shape: "circle",
      color: "red",
    });
    expect(icon("west", "ps5")).toEqual({
      kind: "symbol",
      shape: "square",
      color: "pink",
    });
    expect(icon("north", "ps3")).toEqual({
      kind: "symbol",
      shape: "triangle",
      color: "green",
    });
    // Nintendo: the letters where Nintendo puts them, one colour.
    expect(icon("south", "switchpro")).toEqual({ kind: "face", text: "B" });
    expect(icon("east", "switchpro")).toEqual({ kind: "face", text: "A" });
    // A single Joy-Con held sideways, or a pad without letters: where the
    // button is among the four.
    expect(icon("west", "joyconleft", 0xf)).toEqual({
      kind: "position",
      position: "west",
    });
    expect(icon("north", "unknown")).toEqual({
      kind: "position",
      position: "north",
    });
  });

  it("draws the D-pad as a cross with the pressed arm lit", () => {
    expect(icon("dpad.up", "xboxone")).toEqual({
      kind: "dpad",
      arms: ["up"],
    });
    expect(icon("dpad.downleft", "ps5")).toEqual({
      kind: "dpad",
      arms: ["down", "left"],
    });
  });

  it("draws sticks as a circle, with an arrow for a direction", () => {
    expect(icon("leftstick", "xboxone")).toEqual({ kind: "stick", side: "L" });
    expect(icon("rightstick", "ps5")).toEqual({ kind: "stick", side: "R" });
    expect(icon("ls.up", "xboxone")).toEqual({
      kind: "stick",
      side: "L",
      direction: "up",
    });
    expect(icon("rs.downright", "xboxone")).toEqual({
      kind: "stick",
      side: "R",
      direction: "downright",
    });
    expect(icon("ls.active", "xboxone")).toEqual({
      kind: "stick",
      side: "L",
      direction: "active",
    });
    // A ring or knob tick is the stick, pushed, with no arrow to draw.
    expect(icon("rs.cw", "xboxone")).toEqual({
      kind: "stick",
      side: "R",
      direction: "cw",
    });
    // Raw half axes: y grows downwards.
    expect(icon("lefty-", "xboxone")).toEqual({
      kind: "stick",
      side: "L",
      direction: "up",
    });
    expect(icon("rightx+", "xboxone")).toEqual({
      kind: "stick",
      side: "R",
      direction: "right",
    });
  });

  it("puts shoulders, triggers and system buttons on badges", () => {
    expect(icon("leftshoulder", "xboxone")).toEqual({
      kind: "badge",
      text: "LB",
    });
    expect(icon("rt", "ps5")).toEqual({ kind: "badge", text: "R2" });
    expect(icon("lt", "switchpro")).toEqual({ kind: "badge", text: "ZL" });
    expect(icon("lefttrigger+", "xboxone")).toEqual({
      kind: "badge",
      text: "LT",
    });
    expect(icon("rightshoulder", "joyconright", 0xf)).toEqual({
      kind: "badge",
      text: "SR",
    });
    expect(icon("back", "xboxone")).toEqual({ kind: "badge", text: "View" });
    expect(icon("guide", "ps5")).toEqual({ kind: "badge", text: "PS" });
    expect(icon("start", "switchpro")).toEqual({ kind: "badge", text: "+" });
    // The back buttons and the extra buttons have names of their own, as
    // the tray names them, never padmate's position names.
    expect(icon("paddle1", "xboxone")).toEqual({
      kind: "badge",
      text: "背键 1",
    });
    expect(icon("paddle4", "ps5")).toEqual({ kind: "badge", text: "左 Fn" });
    expect(icon("misc1", "xboxone")).toEqual({ kind: "badge", text: "Share" });
    expect(icon("misc2", "xboxone")).toEqual({
      kind: "badge",
      text: "附加键 2",
    });
    // A single Joy-Con held upright reports SL and SR as paddles.
    expect(icon("paddle2", "joyconleft", 2)).toEqual({
      kind: "badge",
      text: "SL",
    });
    expect(icon("paddle1", "joyconright", 2)).toEqual({
      kind: "badge",
      text: "SR",
    });
    // A control nobody names keeps its name.
    expect(icon("bogus", "xboxone")).toEqual({ kind: "badge", text: "bogus" });
  });

  it("draws inline SVG in theme colours, hidden from screen readers and without text", () => {
    const { container } = render(
      <>
        <ControlIcon icon={icon("south", "xboxone")} />
        <ControlIcon icon={icon("west", "ps5")} />
        <ControlIcon icon={icon("dpad.left", "xboxone")} />
        <ControlIcon icon={icon("ls.right", "xboxone")} />
        <ControlIcon icon={icon("north", "joyconleft", 0xf)} />
        <ControlIcon icon={icon("leftshoulder", "xboxone")} />
      </>,
    );
    const icons = [...container.querySelectorAll(".control-icon")];
    expect(icons).toHaveLength(6);
    for (const element of icons)
      expect(element.getAttribute("aria-hidden")).toBe("true");
    const [face, symbol, dpad, stick, position, badge] = icons;
    expect(face.getAttribute("data-face")).toBe("green");
    expect(face.getAttribute("data-label")).toBe("A");
    expect(face.querySelector("svg circle")).not.toBeNull();
    expect(symbol.getAttribute("data-face")).toBe("pink");
    expect(symbol.querySelector("svg rect")).not.toBeNull();
    // The lit arm and the others.
    expect(dpad.querySelectorAll('[data-lit="true"]')).toHaveLength(1);
    expect(dpad.querySelectorAll('[data-lit="false"]')).toHaveLength(3);
    expect(stick.getAttribute("data-label")).toBe("L");
    expect(stick.querySelectorAll(".control-icon-arrow")).toHaveLength(1);
    expect(position.querySelectorAll('[data-lit="true"]')).toHaveLength(1);
    expect(badge.getAttribute("data-label")).toBe("LB");
    expect(badge.querySelector("svg")).toBeNull();
    // Letters come from CSS, so the line's text is only the spoken name.
    expect(container.textContent).toBe("");
    // Colours come from the theme: no fixed colour in the markup.
    expect(container.innerHTML).not.toMatch(/#[0-9a-f]{3,6}|rgb/i);
  });
});
