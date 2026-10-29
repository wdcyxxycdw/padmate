import { describe, expect, it } from "vitest";
import type { Profile } from "../../shared/generated/schema";
import {
  analogGroup,
  analogIssues,
  convertToFullPull,
  fullControl,
  knobControls,
  legacyTwoStage,
  modeDirections,
  ringControls,
  stickReading,
  usesDepthRate,
} from "./analog-editor";

// The cases of the daemon's TestRing* and TestKnob* (internal/engine,
// m15_test.go), read the same way: an angle in degrees (0 up, clockwise) and
// a normalized depth. The editor mirrors readStick, directionsFor and
// ringWant without their hysteresis and delays.
describe("stick rings and knob, as the daemon reads them", () => {
  // The default dead zone 0.15 and saturation 0.95, as the daemon's stickAt.
  const polar = (degrees: number, norm: number) => {
    const radius = 0.15 + 0.8 * norm;
    const radians = (degrees * Math.PI) / 180;
    return [radius * Math.sin(radians), -radius * Math.cos(radians)] as const;
  };
  const at = (degrees: number, norm: number, value = {}) =>
    stickReading(...polar(degrees, norm), value);

  it("is in the inner ring from the trigger point up to the outer threshold", () => {
    expect(at(0, 0.2).ring).toBeNull();
    expect(at(0, 0.5)).toMatchObject({ active: true, ring: "inner" });
    // 0.89 is under the default 0.9; 0.92 is over it.
    expect(at(0, 0.89).ring).toBe("inner");
    expect(at(0, 0.92).ring).toBe("outer");
    expect(at(0, 0.95).ring).toBe("outer");
    expect(stickReading(0, 0).ring).toBeNull();
  });

  it("follows outerThreshold, and is never inner when it is not above the trigger point", () => {
    expect(at(90, 0.5, { outerThreshold: 0.4 }).ring).toBe("outer");
    expect(at(90, 0.3, { outerThreshold: 0.4 }).ring).toBe("inner");
    for (const outerThreshold of [0.3, 0.2, 0]) {
      expect(at(45, 0.31, { outerThreshold }).ring).toBe("outer");
      expect(at(45, 0.6, { outerThreshold }).ring).toBe("outer");
    }
    // Under the trigger point nothing is active, whatever the threshold.
    expect(at(45, 0.2, { outerThreshold: 0 }).ring).toBeNull();
  });

  it("has the rings in every direction mode", () => {
    for (const mode of [
      "standard",
      "eightWay",
      "fourWay",
      "fourWayDiagonal",
      "knob",
    ] as const)
      expect(at(180, 0.95, { mode }).ring).toBe("outer");
  });

  it("reads the angle clockwise from up", () => {
    for (const degrees of [0, 45, 90, 180, 270, 315])
      expect(at(degrees, 0.6, { mode: "knob" }).angle).toBeCloseTo(degrees, 6);
    expect(at(0, 0.2, { mode: "knob" }).angle).toBeNull();
    expect(at(90, 0.6).angle).toBeCloseTo(90, 6);
  });

  it("presses no direction in knob mode", () => {
    for (const degrees of [0, 45, 90, 135, 180, 270]) {
      expect(at(degrees, 0.7, { mode: "knob" })).toMatchObject({
        active: true,
        directions: [],
        sector: null,
      });
    }
    // The same positions press directions in the other modes.
    expect(at(0, 0.7, { mode: "standard" }).directions).toEqual(["up"]);
    expect(at(0, 0.2, { mode: "knob" }).active).toBe(false);
    expect(modeDirections("knob")).toEqual([]);
  });

  it("names the tick, ring and second-stage controls", () => {
    expect(knobControls("ls")).toEqual(["ls.cw", "ls.ccw"]);
    expect(ringControls("rs")).toEqual(["rs.inner", "rs.outer"]);
    expect(fullControl("lt")).toBe("lt.full");
    expect(analogGroup("rs.outer")).toBe("rs");
    expect(analogGroup("ls.ccw")).toBe("ls");
    expect(analogGroup("rt.full")).toBe("rt");
  });
});

describe("the settings M15 adds", () => {
  it("checks the ring, knob and second-stage ranges", () => {
    expect(
      analogIssues({
        ls: { outerThreshold: 1.2, knobStepDeg: 9 },
        rs: { knobStepDeg: 181 },
        lt: { fullPress: 2 },
      }).map((issue) => issue.path),
    ).toEqual([
      "analog.ls.outerThreshold",
      "analog.ls.knobStepDeg",
      "analog.rs.knobStepDeg",
      "analog.lt.fullPress",
    ]);
    expect(
      analogIssues({ ls: { knobStepDeg: 10 }, rs: { knobStepDeg: 180 } }),
    ).toEqual([]);
    expect(
      analogIssues({ ls: { knobStepDeg: 30.5 } }).map((issue) => issue.path),
    ).toEqual(["analog.ls.knobStepDeg"]);
  });

  it("holds the second stage to the same order as the first", () => {
    expect(analogIssues({ lt: { fullRelease: 0.95 } })).toEqual([
      { path: "analog.lt.fullRelease", code: "fullRelease" },
    ]);
    expect(analogIssues({ rt: { fullPress: 0.7, fullRelease: 0.6 } })).toEqual(
      [],
    );
  });

  it("holds a written fullPress above press, as the daemon does", () => {
    expect(
      analogIssues({ lt: { press: 0.5, fullPress: 0.5, fullRelease: 0.4 } }),
    ).toEqual([{ path: "analog.lt.fullPress", code: "fullPress" }]);
    // The default fullPress is not held to it: the two-stage set-up of M12
    // has a press of 0.9 and must keep loading.
    expect(analogIssues({ lt: { press: 0.9, release: 0.8 } })).toEqual([]);
    expect(analogIssues({ lt: { press: 0.95, release: 0.8 } })).toEqual([]);
  });
});

describe("the depth rate (ANA-12, ANA-13, ANA-15)", () => {
  const repeat = (minRateHz?: number) => ({
    type: "keys" as const,
    keys: "Z",
    mode: "tap" as const,
    repeat: { rateHz: 20, ...(minRateHz ? { minRateHz } : {}) },
  });
  const base = (layers: Profile["layers"], common?: Profile["common"]) =>
    ({ schemaVersion: 1, name: "t", layers, common }) as Profile;

  it("finds the controls of a stick or trigger that use minRateHz in any group", () => {
    const file = base(
      {
        base: { "ls.up": { press: repeat() }, lt: { press: repeat() } },
        tools: { "rs.outer": { press: repeat(3) } },
      },
      { "lt.full": { hold: repeat(2) } },
    );
    expect(usesDepthRate(file, "ls")).toBe(false);
    expect(usesDepthRate(file, "rs")).toBe(true);
    expect(usesDepthRate(file, "lt")).toBe(true);
    expect(usesDepthRate(file, "rt")).toBe(false);
  });

  it("finds them in the function set too", () => {
    const file = {
      ...base({ base: { "ls.up": { press: repeat() } } }),
      functionSet: { "rs.outer": { press: repeat(3) } },
    } as Profile;
    expect(usesDepthRate(file, "rs")).toBe(true);
    expect(usesDepthRate(file, "ls")).toBe(false);
  });

  it("counts raw half axes and ignores controls with no depth", () => {
    const file = base({
      base: {
        "lefttrigger+": { press: repeat(2) },
        "rightx-": { press: repeat(2) },
        // A knob tick has no depth, whatever the file says.
        "ls.cw": { press: repeat(2) },
        leftstick: { press: repeat(2) },
      },
    });
    expect(usesDepthRate(file, "lt")).toBe(true);
    expect(usesDepthRate(file, "rs")).toBe(true);
    expect(usesDepthRate(file, "ls")).toBe(false);
  });
});

describe("the two stages the editor set up before lt.full", () => {
  const old = (): Profile => ({
    schemaVersion: 1,
    name: "old",
    analog: {
      lt: { press: 0.9, release: 0.8, curve: "quadratic" },
      axisThreshold: 0.5,
      ls: { deadzone: 0.2 },
    },
    layers: {
      base: {
        lt: { label: "Full", press: { type: "keys", keys: "E" } },
        "lefttrigger+": { press: { type: "keys", keys: "B" } },
        south: { press: { type: "keys", keys: "Ctrl+Z" } },
      },
      alt: { lt: { press: { type: "keys", keys: "Q" } } },
    },
    common: { back: { press: { type: "keys", keys: "F12" } } },
  });
  const shape = (change: (file: Profile) => void) => {
    const file = old();
    change(file);
    return legacyTwoStage(file, "lt");
  };

  it("recognizes only that shape", () => {
    expect(legacyTwoStage(old(), "lt")).toBe(true);
    expect(legacyTwoStage(old(), "rt")).toBe(false);
    expect(shape((f) => delete f.layers.base["lefttrigger+"])).toBe(false);
    expect(
      shape((f) => {
        f.analog = { ...f.analog, axisThreshold: 0.3 };
      }),
    ).toBe(false);
    expect(
      shape((f) => {
        f.analog = { ...f.analog, axisThreshold: undefined };
      }),
    ).toBe(false);
    expect(
      shape((f) => {
        f.analog = { ...f.analog, lt: { press: 0.5 } };
      }),
    ).toBe(false);
  });

  it("is not offered when the second stage is in use", () => {
    expect(
      shape((f) => {
        f.layers.base["lt.full"] = { press: { type: "keys", keys: "X" } };
      }),
    ).toBe(false);
    expect(
      shape((f) => {
        f.analog = { ...f.analog, lt: { press: 0.9, fullPress: 0.95 } };
      }),
    ).toBe(false);
  });

  it("moves the bindings, resets press and release and keeps everything else", () => {
    const source = old();
    const before = JSON.stringify(source);
    const next = convertToFullPull(source, "lt");
    expect(JSON.stringify(source)).toBe(before);
    expect(next.layers.base["lt.full"]).toEqual(source.layers.base.lt);
    expect(next.layers.base.lt).toEqual(source.layers.base["lefttrigger+"]);
    expect(next.layers.base["lefttrigger+"]).toBeUndefined();
    // The curve stays; press and release are the defaults again.
    expect(next.analog?.lt).toEqual({ curve: "quadratic" });
    // The threshold is shared with the sticks and the other trigger.
    expect(next.analog?.axisThreshold).toBe(0.5);
    expect(next.analog?.ls).toEqual({ deadzone: 0.2 });
    expect(next.layers.base.south).toEqual(source.layers.base.south);
    expect(next.common).toEqual(source.common);
    expect(analogIssues(next.analog)).toEqual([]);
    expect(legacyTwoStage(next, "lt")).toBe(false);
  });

  it("makes the same move in every other key set", () => {
    // Press and release are shared, so a layer or the common set written for
    // a trigger that fires at 90 percent moves with the base set.
    const source = old();
    source.layers.alt["lefttrigger+"] = { press: { type: "keys", keys: "C" } };
    source.layers.third = {
      "lefttrigger+": { press: { type: "keys", keys: "D" } },
    };
    source.common = {
      ...source.common,
      lt: { press: { type: "keys", keys: "F" } },
      "lefttrigger+": { press: { type: "keys", keys: "G" } },
    };
    const before = JSON.stringify(source);
    const next = convertToFullPull(source, "lt");
    expect(JSON.stringify(source)).toBe(before);
    expect(next.layers.alt["lt.full"]).toEqual(source.layers.alt.lt);
    expect(next.layers.alt.lt).toEqual(source.layers.alt["lefttrigger+"]);
    expect(next.layers.alt["lefttrigger+"]).toBeUndefined();
    expect(next.layers.third.lt).toEqual(source.layers.third["lefttrigger+"]);
    expect(next.layers.third["lt.full"]).toBeUndefined();
    expect(next.layers.third["lefttrigger+"]).toBeUndefined();
    expect(next.common?.["lt.full"]).toEqual(source.common.lt);
    expect(next.common?.lt).toEqual(source.common["lefttrigger+"]);
    expect(next.common?.["lefttrigger+"]).toBeUndefined();
    expect(next.common?.back).toEqual(source.common.back);
    expect(legacyTwoStage(next, "lt")).toBe(false);
    expect(analogIssues(next.analog)).toEqual([]);
  });

  it("makes the same move in the function set (ADR-0075)", () => {
    const source = old();
    source.functionSet = {
      "lefttrigger+": { press: { type: "keys", keys: "C" } },
      lt: { press: { type: "keys", keys: "F" } },
    };
    const before = JSON.stringify(source);
    const next = convertToFullPull(source, "lt");
    expect(JSON.stringify(source)).toBe(before);
    expect(next.functionSet?.lt).toEqual(source.functionSet["lefttrigger+"]);
    expect(next.functionSet?.["lefttrigger+"]).toBeUndefined();
    expect(next.functionSet?.["lt.full"]).toEqual(source.functionSet.lt);
    expect(legacyTwoStage(next, "lt")).toBe(false);
  });

  it("is not offered when the function set uses the second stage", () => {
    expect(
      shape((f) => {
        f.functionSet = {
          "lt.full": { press: { type: "keys", keys: "X" } },
        };
      }),
    ).toBe(false);
  });

  it("does not add a common set the file does not have", () => {
    const source = old();
    delete source.common;
    expect(convertToFullPull(source, "lt")).not.toHaveProperty("common");
  });

  it("is not offered when another key set already uses the second stage", () => {
    expect(
      shape((f) => {
        f.layers.alt["lt.full"] = { press: { type: "keys", keys: "X" } };
      }),
    ).toBe(false);
    expect(
      shape((f) => {
        f.common = { "lt.full": { press: { type: "keys", keys: "X" } } };
      }),
    ).toBe(false);
  });

  it("is not offered when a script is registered on the trigger or its second stage", () => {
    const scripted = (control: string) =>
      legacyTwoStage(old(), "lt", [{ control }]);
    expect(legacyTwoStage(old(), "lt", [])).toBe(true);
    expect(scripted("lt")).toBe(false);
    expect(scripted("lt.full")).toBe(false);
    // Other controls do not move with the trigger.
    expect(scripted("rt")).toBe(true);
    expect(scripted("south")).toBe(true);
    expect(scripted("lefttrigger+")).toBe(true);
  });

  it("drops an analog entry that only held press and release", () => {
    const source = old();
    source.analog = { lt: { press: 0.9, release: 0.8 }, axisThreshold: 0.5 };
    expect(convertToFullPull(source, "lt").analog).toEqual({
      axisThreshold: 0.5,
    });
  });

  it("leaves the trigger empty when it had no binding of its own", () => {
    const source = old();
    delete source.layers.base.lt;
    const next = convertToFullPull(source, "lt");
    expect(next.layers.base["lt.full"]).toBeUndefined();
    expect(next.layers.base.lt).toEqual(source.layers.base["lefttrigger+"]);
  });
});
