import { describe, expect, it } from "vitest";
import type { Profile } from "../../shared/generated/schema";
import {
  analogGroup,
  analogIssues,
  combinedDirection,
  directionControls,
  fromPercent,
  rawStickControls,
  setAnalogSettings,
  setAxisThreshold,
  setControlBinding,
  settleStickMode,
  shownStickMode,
  stickMagnitude,
  stickReading,
  toPercent,
} from "./analog-editor";
import { applyAnalogPreset, presetChanges, presetMode } from "./analog-presets";

const profile = (): Profile => ({
  schemaVersion: 1,
  name: "test",
  layers: {
    base: { south: { press: { type: "keys", keys: "F13" } } },
    alt: { "ls.up": { press: { type: "keys", keys: "F14" } } },
  },
  common: { back: { press: { type: "keys", keys: "F15" } } },
  script: { api: "0.21", entry: "index.ts", params: { speed: 3 } },
});

describe("analog drafts", () => {
  it("maps model labels and virtual controls to their parameter group", () => {
    for (const [control, group] of [
      ["leftstick", "ls"],
      ["l3", "ls"],
      ["leftx+", "ls"],
      ["rs.downleft", "rs"],
      ["lefttrigger", "lt"],
      ["righttrigger+", "rt"],
      ["dpad.upright", "dpad"],
      ["dpup", "dpad"],
    ])
      expect(analogGroup(control)).toBe(group);
    expect(analogGroup("north")).toBeNull();
  });

  it("edits only the settings and the base binding without mutating the source", () => {
    const source = profile();
    const before = JSON.stringify(source);
    const updated = setControlBinding(
      setAnalogSettings(source, "ls", { deadzone: 0.4 }),
      "ls.up",
      { press: { type: "keys", keys: "Up" } },
    );
    expect(JSON.stringify(source)).toBe(before);
    expect(updated.script).toEqual(source.script);
    expect(updated.layers.alt).toEqual(source.layers.alt);
    expect(updated.common).toEqual(source.common);
    expect(updated.layers.base.south).toEqual(source.layers.base.south);
    expect(updated.layers.base["ls.up"]?.press).toEqual({
      type: "keys",
      keys: "Up",
    });
    expect(updated.analog?.ls?.deadzone).toBe(0.4);
    expect(setAxisThreshold(updated, 0.5).analog).toEqual({
      ls: { deadzone: 0.4 },
      axisThreshold: 0.5,
    });
  });

  it("retains explicit zero and checks cross-field bounds against resolved defaults", () => {
    expect(
      analogIssues({
        ls: { deadzone: 0, directionThreshold: 0 },
        lt: { press: 0, release: 0 },
      }),
    ).toEqual([]);
    expect(
      analogIssues({ ls: { deadzone: 0.96 } }).map((issue) => issue.path),
    ).toContain("analog.ls.saturation");
    expect(
      analogIssues({ rt: { press: 0.1 } }).map((issue) => issue.path),
    ).toContain("analog.rt.release");
    expect(
      analogIssues({
        rs: { diagonalWidth: 0, directionDelayMs: 1.5 },
        axisThreshold: Number.NaN,
      }),
    ).toHaveLength(3);
  });

  it("updates an aliased stick button without creating duplicate controls", () => {
    const source = profile();
    source.layers.base.l3 = { press: { type: "keys", keys: "Space" } };
    const next = setControlBinding(source, "leftstick", {
      press: { type: "keys", keys: "F15" },
    });
    expect(next.layers.base.l3).toBeUndefined();
    expect(next.layers.base.leftstick?.press).toEqual({
      type: "keys",
      keys: "F15",
    });
    expect(source.layers.base.l3).toBeDefined();
  });

  it("offers exactly the virtual directions emitted by each mode", () => {
    expect(directionControls("ls", "standard")).toEqual([
      "ls.up",
      "ls.right",
      "ls.down",
      "ls.left",
    ]);
    expect(directionControls("rs", "fourWayDiagonal")).toEqual([
      "rs.upright",
      "rs.downright",
      "rs.downleft",
      "rs.upleft",
    ]);
    expect(directionControls("dpad", "eightWay")).toHaveLength(8);
    expect(rawStickControls("ls")).toEqual([
      "lefty-",
      "leftx+",
      "lefty+",
      "leftx-",
    ]);
  });

  it("normalizes radial travel according to the daemon deadzone and saturation", () => {
    expect(stickMagnitude(0.15, 0, {})).toBe(0);
    expect(stickMagnitude(0.55, 0, {})).toBeCloseTo(0.5);
    expect(stickMagnitude(1, 1, {})).toBe(1);
    expect(stickMagnitude(0.2, 0, { deadzone: 0.4 })).toBe(0);
  });

  it("converts between stored fractions and shown percentages", () => {
    expect(toPercent(0.15)).toBe(15);
    expect(toPercent(0.175)).toBe(17.5);
    expect(fromPercent(15)).toBe(0.15);
    expect(fromPercent(17.5)).toBe(0.175);
    expect(fromPercent(toPercent(0.95))).toBe(0.95);
    expect(fromPercent(0)).toBe(0);
  });
});

describe("live stick reading", () => {
  // SDL axes: y grows downwards, so up is y = -1.
  const diagonal = Math.SQRT1_2;

  it("presses nothing inside the dead zone or below the trigger point", () => {
    expect(stickReading(0.1, 0)).toMatchObject({
      inDeadzone: true,
      active: false,
      directions: [],
    });
    const below = stickReading(0.3, 0);
    expect(below.inDeadzone).toBe(false);
    expect(below.active).toBe(false);
    expect(below.magnitude).toBeCloseTo(0.1875);
  });

  it("follows each mode on a cardinal and on a diagonal", () => {
    expect(stickReading(0, -1).directions).toEqual(["up"]);
    expect(stickReading(1, 0).directions).toEqual(["right"]);
    expect(stickReading(0, 1).directions).toEqual(["down"]);
    expect(stickReading(-1, 0).directions).toEqual(["left"]);
    expect(stickReading(diagonal, -diagonal).directions).toEqual([
      "up",
      "right",
    ]);
    expect(
      stickReading(diagonal, -diagonal, { mode: "eightWay" }).directions,
    ).toEqual(["upright"]);
    expect(
      stickReading(-diagonal, diagonal, { mode: "fourWayDiagonal" }).directions,
    ).toEqual(["downleft"]);
    expect(
      stickReading(0.2, -1, { mode: "fourWayDiagonal" }).directions,
    ).toEqual(["upright"]);
    expect(
      stickReading(diagonal, diagonal + 0.01, { mode: "fourWay" }).directions,
    ).toHaveLength(1);
  });

  it("uses the diagonal width and the eight-way fallback", () => {
    // 20 degrees from up: outside a 45 degree diagonal, inside a 60 degree one.
    const x = Math.sin(Math.PI / 9);
    const y = -Math.cos(Math.PI / 9);
    expect(stickReading(x, y).directions).toEqual(["up"]);
    expect(stickReading(x, y, { diagonalWidth: 60 }).directions).toEqual([
      "up",
      "right",
    ]);
    const unbound = () => false;
    expect(
      stickReading(
        diagonal,
        -diagonal,
        { mode: "eightWay", diagonalFallback: "cardinals" },
        unbound,
      ).directions,
    ).toEqual(["up", "right"]);
    expect(
      stickReading(diagonal, -diagonal, { mode: "eightWay" }, unbound)
        .directions,
    ).toEqual(["upright"]);
  });

  it("reports the sector that is pressed", () => {
    expect(stickReading(0, -1).sector).toEqual({ from: -22.5, to: 22.5 });
    expect(stickReading(diagonal, -diagonal).sector).toEqual({
      from: 22.5,
      to: 67.5,
    });
    expect(stickReading(0, -1, { mode: "fourWay" }).sector).toEqual({
      from: -45,
      to: 45,
    });
    expect(stickReading(0, 0).sector).toBeNull();
  });

  it("names combined directions", () => {
    expect(combinedDirection(["up", "right"])).toBe("upright");
    expect(combinedDirection(["down", "left"])).toBe("downleft");
    expect(combinedDirection(["up", "down"])).toBeNull();
  });
});

describe("the modes the stick drawer offers", () => {
  it("shows an omitted or standard mode as four directions, the others as they are", () => {
    expect(shownStickMode(undefined)).toBe("fourWay");
    expect(shownStickMode("standard")).toBe("fourWay");
    expect(shownStickMode("fourWay")).toBe("fourWay");
    expect(shownStickMode("knob")).toBe("knob");
    expect(shownStickMode("mouse")).toBe("mouse");
    // The removed modes stay what they are: the drawer selects none of them.
    expect(shownStickMode("eightWay")).toBe("eightWay");
    expect(shownStickMode("fourWayDiagonal")).toBe("fourWayDiagonal");
  });

  it("writes fourWay for a stick without a mode or in standard, and touches nothing else", () => {
    const source = profile();
    const settled = settleStickMode(
      setAnalogSettings(source, "ls", { deadzone: 0.3 }),
      "ls",
    );
    expect(settled.analog?.ls).toEqual({ deadzone: 0.3, mode: "fourWay" });
    // No analog block at all: the stick gets one.
    expect(settleStickMode(source, "rs").analog?.rs).toEqual({
      mode: "fourWay",
    });
    expect(
      settleStickMode(
        setAnalogSettings(source, "ls", { mode: "standard", mouseSpeed: 90 }),
        "ls",
      ).analog?.ls,
    ).toEqual({ mode: "fourWay", mouseSpeed: 90 });
    expect(settled.layers).toEqual(source.layers);
    expect(settled.script).toEqual(source.script);
    // The other stick is not this stick's.
    expect(
      settleStickMode(setAnalogSettings(source, "rs", { deadzone: 0.2 }), "ls")
        .analog?.rs,
    ).toEqual({ deadzone: 0.2 });
  });

  it.each(["fourWay", "knob", "mouse", "eightWay", "fourWayDiagonal"] as const)(
    "keeps %s as it is",
    (mode) => {
      const source = setAnalogSettings(profile(), "ls", {
        mode,
        deadzone: 0.3,
      });
      expect(settleStickMode(source, "ls")).toBe(source);
    },
  );

  it("makes the arrow and WASD presets four directions", () => {
    expect(presetMode("ls")).toBe("fourWay");
    expect(presetMode("rs")).toBe("fourWay");
    expect(presetMode("dpad")).toBe("buttons");
  });
});

describe("analog presets", () => {
  it.each(["ls", "rs", "dpad"] as const)(
    "makes valid arrows and WASD drafts for %s",
    (group) => {
      for (const preset of ["arrows", "wasd"] as const) {
        const source = profile();
        const next = applyAnalogPreset(source, group, preset);
        expect(analogIssues(next.analog)).toEqual([]);
        expect(next.analog?.[group]?.mode).toBe(presetMode(group));
        expect(next.layers.base[`${group}.up`]?.press).toEqual({
          type: "keys",
          keys: preset === "arrows" ? "Up" : "KeyW",
          mode: "hold",
        });
        expect(next.layers.base.south).toEqual(source.layers.base.south);
        expect(next.layers.alt).toEqual(source.layers.alt);
        expect(next.common).toEqual(source.common);
        expect(next.script).toEqual(source.script);
        expect(source.analog).toBeUndefined();
      }
    },
  );

  it("reports every base binding replaced and never erases another control or script", () => {
    const source = profile();
    source.layers.base["ls.up"] = {
      tap: { type: "script", name: "cycle" },
      label: "Custom",
    };
    source.layers.base["ls.left"] = { label: "Only a name" };
    source.layers.base["ls.upright"] = { press: { type: "keys", keys: "F15" } };
    source.layers.base["leftx+"] = { press: { type: "keys", keys: "F16" } };
    expect(presetChanges(source, "ls").map((change) => change.control)).toEqual(
      ["ls.up"],
    );
    // The other key group's ls.up is not part of the edited key set.
    expect(presetChanges(profile(), "ls")).toEqual([]);
    const next = applyAnalogPreset(source, "ls", "arrows");
    expect(next.layers.base["ls.upright"]).toEqual(
      source.layers.base["ls.upright"],
    );
    expect(next.layers.base["leftx+"]).toEqual(source.layers.base["leftx+"]);
    expect(next.layers.base["ls.up"]).toEqual({
      label: "Custom",
      press: { type: "keys", keys: "Up", mode: "hold" },
    });
    expect(next.layers.base["ls.left"]?.label).toBe("Only a name");
    expect(next.layers.alt).toEqual(source.layers.alt);
    expect(next.script).toEqual(source.script);
  });

  it("counts a block-only direction as a binding the preset replaces", () => {
    const source = profile();
    source.layers.base["ls.down"] = { block: true };
    source.layers.base["dpad.left"] = { block: true, label: "Off" };
    expect(presetChanges(source, "ls")).toEqual([
      { control: "ls.down", binding: { block: true } },
    ]);
    expect(
      presetChanges(source, "dpad").map((change) => change.control),
    ).toEqual(["dpad.left"]);
    const next = applyAnalogPreset(source, "dpad", "wasd");
    expect(next.layers.base["dpad.left"]).toEqual({
      label: "Off",
      press: { type: "keys", keys: "KeyA", mode: "hold" },
    });
  });
});
