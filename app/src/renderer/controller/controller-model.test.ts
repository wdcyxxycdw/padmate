import { describe, expect, it } from "vitest";
import { functionKeyControl } from "../editor/editor-model";
import { sdlPads } from "../testing/sdl-pads";
import {
  axisPosition,
  controllerLabels,
  controlStrength,
  defaultPadFunctionKey,
  faceLabels,
  familyName,
  functionKeyLabel,
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

  it("labels a single right Joy-Con like other Nintendo pads, a left one neutrally", () => {
    expect(faceLabels("joyconleft")).toEqual({
      south: "S",
      east: "E",
      west: "W",
      north: "N",
    });
    expect(faceLabels("joyconright")).toEqual({
      south: "B",
      east: "A",
      west: "Y",
      north: "X",
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

  it("names the controls of a single Joy-Con held upright", () => {
    const left = controllerLabels("joyconleft");
    expect(left).toMatchObject({
      leftshoulder: "L",
      lefttrigger: "ZL",
      back: "−",
      misc1: "Capture",
      paddle2: "SL",
      paddle4: "SR",
    });
    const right = controllerLabels("joyconright");
    expect(right).toMatchObject({
      south: "B",
      east: "A",
      west: "Y",
      north: "X",
      rightshoulder: "R",
      righttrigger: "ZR",
      start: "+",
      guide: "Home",
      paddle1: "SR",
      paddle3: "SL",
    });
    // The buttons SDL maps no longer decide the names.
    expect(
      controllerLabels(
        "joyconright",
        {},
        sdlPads.joyConRightUpright.buttonMask,
      ),
    ).toEqual(right);
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

  it("is Menu or View on a controller with the standard layout", () => {
    const { xboxSeries, ps5 } = sdlPads;
    expect(padFunctionKeys("xboxone")).toEqual(["start", "back"]);
    expect(padFunctionKeys(ps5.type)).toEqual(["start", "back"]);
    expect(defaultPadFunctionKey(xboxSeries.type)).toBe("start");
    expect(padHasFunctionKey("back", "xboxone")).toBe(true);
    expect(padHasFunctionKey("lt", "xboxone")).toBe(false);
  });

  it("is − or + on a pair of Joy-Cons, − the default", () => {
    const { type } = sdlPads.joyConPair;
    expect(padFunctionKeys(type)).toEqual(["back", "start"]);
    expect(defaultPadFunctionKey(type)).toBe("back");
    expect(functionKeyLabel("back", type)).toBe("−");
    expect(functionKeyLabel("start", type)).toBe("+");
  });

  it("is ZL or L on a single left Joy-Con, ZR or R on a right one, the trigger the default", () => {
    expect(padFunctionKeys("joyconleft")).toEqual(["lt", "leftshoulder"]);
    expect(padFunctionKeys("joyconright")).toEqual(["rt", "rightshoulder"]);
    expect(defaultPadFunctionKey("joyconleft")).toBe("lt");
    expect(defaultPadFunctionKey("joyconright")).toBe("rt");
    expect(padHasFunctionKey("lt", "joyconleft")).toBe(true);
    expect(padHasFunctionKey("rt", "joyconleft")).toBe(false);
    // No − or + on a single Joy-Con, and no paddles.
    expect(padHasFunctionKey("back", "joyconleft")).toBe(false);
    expect(padHasFunctionKey("start", "joyconright")).toBe(false);
    // And named as a Joy-Con prints them: lt and rt are the triggers.
    expect(functionKeyLabel("lt", "joyconleft")).toBe("ZL");
    expect(functionKeyLabel("leftshoulder", "joyconleft")).toBe("L");
    expect(functionKeyLabel("rt", "joyconright")).toBe("ZR");
    expect(functionKeyLabel("rightshoulder", "joyconright")).toBe("R");
  });

  it("makes lt and rt the triggers' controls", () => {
    expect(functionKeyControl("lt")).toBe("lefttrigger");
    expect(functionKeyControl("rt")).toBe("righttrigger");
    expect(functionKeyControl("start")).toBe("start");
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
    expect(has("dpad.up", sdlPads.joyConLeftUpright)).toBe(true);
    expect(has("lefttrigger", sdlPads.joyConLeftUpright)).toBe(true);
    expect(has("lefttrigger", sdlPads.joyConRightUpright)).toBe(false);
    // A stick is there with its axes or its click.
    expect(has("leftstick", sdlPads.joyConLeftUpright)).toBe(true);
    expect(has("rightstick", sdlPads.joyConLeftUpright)).toBe(false);
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
