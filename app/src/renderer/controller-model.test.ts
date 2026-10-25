import { describe, expect, it } from "vitest";
import {
  axisPosition,
  controllerLabels,
  controlStrength,
  faceLabels,
  familyName,
  functionKeyLabels,
  functionKeysOf,
  stickActiveThreshold,
  stickDeflection,
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
});

describe("the function keys of a controller (ADR-0069)", () => {
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
  ])("are named as %s prints them: %s and %s", (type, back, start) => {
    expect(functionKeyLabels(type)).toEqual({ back, start });
  });

  it("has Menu only on a single Joy-Con held sideways", () => {
    // Held sideways it reports face buttons (buttonMask bit 0), as the
    // templates and the labels decide it.
    expect(functionKeyLabels("joyconleft", {}, 1)).toEqual({ start: "−" });
    expect(functionKeyLabels("joyconright", {}, 0xf)).toEqual({ start: "+" });
    // Held upright it has − or + as the Start button of a pad.
    expect(functionKeyLabels("joyconleft", {}, 2)).toEqual({
      back: "−",
      start: "+",
    });
    expect(functionKeysOf("joyconleft", 1)).toEqual(["start"]);
    expect(functionKeysOf("joyconleft", 2)).toEqual(["back", "start"]);
    expect(functionKeysOf("xboxone")).toEqual(["back", "start"]);
  });

  it("takes the names the controller reports", () => {
    expect(
      functionKeyLabels("xboxone", { back: "Select", start: "Menu" }),
    ).toEqual({ back: "Select", start: "Menu" });
  });
});
