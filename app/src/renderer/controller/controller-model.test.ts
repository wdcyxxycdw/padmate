import { describe, expect, it } from "vitest";
import { sdlPads } from "../testing/sdl-pads";
import {
  axisPosition,
  controllerLabels,
  controlStrength,
  faceLabels,
  familyName,
  functionKeyLabel,
  isSidewaysJoyCon,
  padFunctionKeys,
  padHasControl,
  padHasFunctionKey,
  stickActiveThreshold,
  stickDeflection,
  stickPush,
  triggerValue,
} from "./controller-model";

describe("controller presentation", () => {
  it.each([
    ["xboxone", ["A", "B", "X", "Y"]],
    ["ps5", ["×", "○", "□", "△"]],
    ["switchpro", ["B", "A", "Y", "X"]],
  ])(
    "labels physical positions for %s without renaming controls",
    (type, labels) => {
      expect(Object.values(faceLabels(type))).toEqual(labels);
      expect(Object.keys(faceLabels(type))).toEqual([
        "south",
        "east",
        "west",
        "north",
      ]);
    },
  );

  it("prefers the labels reported by the connected controller", () => {
    expect(faceLabels("xboxone", { south: "B", east: "A" })).toEqual({
      south: "B",
      east: "A",
      west: "X",
      north: "Y",
    });
  });

  it("does not assign a brand to an unknown controller", () => {
    expect(familyName("unknown")).toBe("generic");
    expect(faceLabels("unknown").south).toBe("S");
  });

  it.each([
    ["xboxone", ["LB", "RB", "LT", "RT", "View", "Menu"]],
    ["ps3", ["L1", "R1", "L2", "R2", "Select", "Start"]],
    ["ps4", ["L1", "R1", "L2", "R2", "Share", "Options"]],
    ["ps5", ["L1", "R1", "L2", "R2", "Create", "Options"]],
    ["switchpro", ["L", "R", "ZL", "ZR", "−", "+"]],
  ])(
    "uses the %s family labels for the surrounding controls",
    (type, expected) => {
      const labels = controllerLabels(type);
      expect([
        labels.leftshoulder,
        labels.rightshoulder,
        labels.lefttrigger,
        labels.righttrigger,
        labels.back,
        labels.start,
      ]).toEqual(expected);
    },
  );

  it("renders SDL PlayStation labels as the printed symbols", () => {
    expect(
      faceLabels("unknown", {
        south: "Cross",
        east: "Circle",
        west: "Square",
        north: "Triangle",
      }),
    ).toEqual({
      south: "×",
      east: "○",
      west: "□",
      north: "△",
    });
  });

  it("uses reported single Joy-Con face labels and keeps missing labels neutral", () => {
    expect(faceLabels("joyconleft")).toEqual({
      south: "S",
      east: "E",
      west: "W",
      north: "N",
    });
    expect(
      faceLabels("joyconright", {
        south: "A",
        east: "X",
        west: "B",
        north: "Y",
      }),
    ).toEqual({ south: "A", east: "X", west: "B", north: "Y" });
  });

  it("normalizes the asymmetric stick range and clamps malformed positions", () => {
    expect(axisPosition(-32768)).toBe(-1);
    expect(axisPosition(32767)).toBe(1);
    expect(axisPosition(0)).toBe(0);
    expect(axisPosition(40000)).toBe(1);
    expect(axisPosition(Number.NaN)).toBe(0);
  });

  it("keeps stick positions inside the unit circle with +y down", () => {
    const axes = (x: number, y: number) => ({
      buttons: {},
      axes: { leftx: x, lefty: y },
    });
    expect(stickDeflection(undefined, "left")).toEqual({
      x: 0,
      y: 0,
      length: 0,
    });
    const half = stickDeflection(axes(16384, -16384), "left");
    expect(half.x).toBeCloseTo(0.5, 4);
    expect(half.y).toBeCloseTo(-0.5, 4);
    expect(half.length).toBeCloseTo(Math.SQRT1_2, 4);
    // A square gate's corner is pulled back to the rim, keeping its direction.
    const corner = stickDeflection(axes(32767, 32767), "left");
    expect(corner.x).toBeCloseTo(Math.SQRT1_2, 6);
    expect(corner.y).toBeCloseTo(Math.SQRT1_2, 6);
    expect(corner.length).toBe(1);
    expect(stickDeflection(axes(32767, 32767), "right").length).toBe(0);
  });

  it("measures how strongly each kind of control is in use", () => {
    const input = {
      buttons: { south: true, rightstick: true },
      axes: {
        lefttrigger: 16384,
        leftx: -32768,
        righty: 2000,
      },
    };
    expect(controlStrength("south", input)).toBe(1);
    expect(controlStrength("east", input)).toBe(0);
    expect(controlStrength("lefttrigger", input)).toBeCloseTo(0.5, 3);
    expect(controlStrength("righttrigger", input)).toBe(0);
    // A pushed stick counts by how far it is pushed, once past drift.
    expect(controlStrength("leftstick", input)).toBe(1);
    const drift = stickActiveThreshold * 32767 * 0.9;
    expect(
      controlStrength("leftstick", { buttons: {}, axes: { lefty: drift } }),
    ).toBe(0);
    const push = stickActiveThreshold * 32767 * 1.1;
    expect(
      controlStrength("leftstick", { buttons: {}, axes: { lefty: push } }),
    ).toBeCloseTo(stickActiveThreshold * 1.1, 4);
    // A clicked stick is fully on even while it rests.
    expect(controlStrength("rightstick", input)).toBe(1);
    expect(controlStrength("rightstick", undefined)).toBe(0);
    expect(triggerValue(-5)).toBe(0);
    expect(triggerValue(Number.NaN)).toBe(0);
  });

  it("measures a stick's push apart from its click", () => {
    const clicked = { buttons: { leftstick: true }, axes: {} };
    // The click is the press cell's lamp, not the movement's.
    expect(stickPush("leftstick", clicked)).toBe(0);
    expect(controlStrength("leftstick", clicked)).toBe(1);
    const drift = stickActiveThreshold * 32767 * 0.9;
    expect(
      stickPush("leftstick", { buttons: {}, axes: { lefty: drift } }),
    ).toBe(0);
    const push = stickActiveThreshold * 32767 * 1.1;
    expect(
      stickPush("rightstick", { buttons: {}, axes: { righty: push } }),
    ).toBeCloseTo(stickActiveThreshold * 1.1, 4);
    expect(
      stickPush("leftstick", { buttons: {}, axes: { righty: 32767 } }),
    ).toBe(0);
    expect(stickPush("leftstick", undefined)).toBe(0);
  });
});

describe("the function key of a controller (ADR-0012)", () => {
  it.each([
    ["xboxone", "View", "Menu"],
    ["xboxseries", "View", "Menu"],
    ["xbox360", "Back", "Start"],
    ["ps5", "Create", "Options"],
    ["ps4", "Share", "Options"],
    ["ps3", "Select", "Start"],
    ["switchpro", "−", "+"],
    ["joyconpair", "−", "+"],
    ["unknown", "Back", "Start"],
  ])("is named as %s prints it: %s and %s", (type, back, start) => {
    expect(functionKeyLabel("back", type)).toBe(back);
    expect(functionKeyLabel("start", type)).toBe(start);
  });

  it("has both on a controller, and no View on a single Joy-Con held sideways", () => {
    // Held sideways it reports face buttons (buttonMask bit 0), as the
    // labels decide it.
    expect(padFunctionKeys("joyconleft", 1)).toEqual(["start"]);
    expect(padFunctionKeys("joyconright", 0xf)).toEqual(["start"]);
    expect(padHasFunctionKey("back", "joyconleft", 1)).toBe(false);
    expect(padHasFunctionKey("start", "joyconleft", 1)).toBe(true);
    expect(functionKeyLabel("start", "joyconleft", {}, 1)).toBe("−");
    expect(functionKeyLabel("start", "joyconright", {}, 0xf)).toBe("+");
    // Held upright it has − or + as the Back and Start buttons of a pad.
    expect(padFunctionKeys("joyconleft", 2).sort()).toEqual(["back", "start"]);
    expect(padFunctionKeys("xboxone").sort()).toEqual(["back", "start"]);
  });

  it("takes the names the controller reports", () => {
    expect(
      functionKeyLabel("back", "xboxone", { back: "Select", start: "Menu" }),
    ).toBe("Select");
    // A control with no name of its own is named by itself.
    expect(functionKeyLabel("start", "xboxone", { start: "Menu" })).toBe(
      "Menu",
    );
  });
});

describe("the controls of a pad", () => {
  const has = (control: string, info: Parameters<typeof padHasControl>[1]) =>
    padHasControl(control, info);

  it("are the standard ones while SDL has not said", () => {
    for (const control of ["south", "dpad.up", "leftstick", "lefttrigger"])
      expect(has(control, { type: "xboxone" }), control).toBe(true);
    expect(has("misc1", { type: "switchpro" })).toBe(false);
    expect(has("paddle1", { type: "xboxone" })).toBe(false);
    // The touchpad of a DualShock 4 or DualSense is standard.
    expect(has("touchpad", { type: "ps4" })).toBe(true);
    expect(has("touchpad", { type: "ps5" })).toBe(true);
    expect(has("touchpad", { type: "ps3" })).toBe(false);
  });

  it("are the ones SDL reports", () => {
    expect(has("misc1", sdlPads.switchPro)).toBe(true);
    expect(has("misc1", sdlPads.ps4)).toBe(false);
    expect(has("touchpad", sdlPads.ps4)).toBe(true);
    expect(has("paddle3", sdlPads.ps5Edge)).toBe(true);
    expect(has("paddle3", sdlPads.ps5)).toBe(false);
    expect(has("dpad.up", sdlPads.joyConLeftSideways)).toBe(false);
    expect(has("lefttrigger", sdlPads.joyConLeftSideways)).toBe(false);
    expect(has("lefttrigger", sdlPads.joyConLeftUpright)).toBe(true);
    // A stick is there with its axes or its click.
    expect(has("leftstick", sdlPads.joyConLeftSideways)).toBe(true);
    expect(has("rightstick", sdlPads.joyConLeftSideways)).toBe(false);
    expect(has("leftstick", { type: "xboxone", buttonMask: 1 << 7 })).toBe(
      true,
    );
    expect(has("leftstick", { type: "xboxone", axisMask: 1 })).toBe(true);
  });

  it("are not a name that is no control", () => {
    expect(has("nonsense", sdlPads.ps5)).toBe(false);
    expect(has("nonsense", { type: "ps5" })).toBe(false);
  });
});

describe("a single Joy-Con held sideways", () => {
  it("is told by the buttons SDL maps", () => {
    expect(
      isSidewaysJoyCon("joyconleft", sdlPads.joyConLeftSideways.buttonMask),
    ).toBe(true);
    expect(
      isSidewaysJoyCon("joyconright", sdlPads.joyConRightSideways.buttonMask),
    ).toBe(true);
    expect(
      isSidewaysJoyCon("joyconleft", sdlPads.joyConLeftUpright.buttonMask),
    ).toBe(false);
    // Upright, the right one has face buttons too, and its own stick.
    expect(
      isSidewaysJoyCon("joyconright", sdlPads.joyConRightUpright.buttonMask),
    ).toBe(false);
    expect(isSidewaysJoyCon("joyconpair", sdlPads.joyConPair.buttonMask)).toBe(
      false,
    );
  });
});
