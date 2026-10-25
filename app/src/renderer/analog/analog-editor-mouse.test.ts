import { describe, expect, it } from "vitest";
import {
  analogIssues,
  modeDirections,
  mouseSmoothingRange,
  mouseSpeedRange,
  stickDefaults,
  stickReading,
} from "./analog-editor";

// The stick that moves the pointer (MOU-01, MOU-04, MOU-06): the editor reads
// it as the daemon does, a stick that is pushed and presses no direction.
describe("a stick that moves the pointer", () => {
  // The default dead zone 0.15 and saturation 0.95.
  const polar = (degrees: number, norm: number) => {
    const radius = 0.15 + 0.8 * norm;
    const radians = (degrees * Math.PI) / 180;
    return [radius * Math.sin(radians), -radius * Math.cos(radians)] as const;
  };
  const at = (degrees: number, norm: number, value = {}) =>
    stickReading(...polar(degrees, norm), { mode: "mouse", ...value });

  it("presses no direction and has no sector", () => {
    expect(modeDirections("mouse")).toEqual([]);
    for (const degrees of [0, 45, 90, 135, 180, 270]) {
      expect(at(degrees, 0.7)).toMatchObject({
        active: true,
        directions: [],
        sector: null,
      });
    }
    // The same positions press directions in the other modes.
    expect(
      stickReading(...polar(0, 0.7), { mode: "standard" }).directions,
    ).toEqual(["up"]);
  });

  it("is active (ls.active, the rings) from the trigger point", () => {
    expect(at(0, 0.2).active).toBe(false);
    expect(at(0, 0.5).active).toBe(true);
    expect(at(0, 0.2, { directionThreshold: 0.1 }).active).toBe(true);
    expect(at(0, 0.2).magnitude).toBeCloseTo(0.2, 6);
    expect(stickReading(0, 0, { mode: "mouse" }).active).toBe(false);
  });

  it("has the rings like every other mode", () => {
    expect(at(90, 0.5).ring).toBe("inner");
    expect(at(90, 0.95).ring).toBe("outer");
    expect(at(90, 0.2).ring).toBeNull();
  });
});

describe("the pointer settings", () => {
  it("default to 1200 px/s and no smoothing", () => {
    expect(stickDefaults.mouseSpeed).toBe(1200);
    expect(stickDefaults.mouseSmoothingMs).toBe(0);
    expect(stickDefaults.curve).toBe("linear");
  });

  it("know the daemon's ranges", () => {
    expect(mouseSpeedRange).toEqual({ min: 50, max: 10000, slider: 4000 });
    expect(mouseSmoothingRange).toEqual({ min: 0, max: 500 });
  });

  it("are checked like the daemon checks them, for both sticks", () => {
    expect(
      analogIssues({
        ls: { mouseSpeed: 49 },
        rs: { mouseSpeed: 10001, mouseSmoothingMs: 501 },
      }).map((issue) => issue.path),
    ).toEqual([
      "analog.ls.mouseSpeed",
      "analog.rs.mouseSpeed",
      "analog.rs.mouseSmoothingMs",
    ]);
    expect(
      analogIssues({
        ls: { mouseSpeed: 50, mouseSmoothingMs: 0 },
        rs: { mouseSpeed: 10000, mouseSmoothingMs: 500 },
      }),
    ).toEqual([]);
    expect(
      analogIssues({ ls: { mouseSpeed: 1200.5, mouseSmoothingMs: -1 } }).map(
        (issue) => issue.path,
      ),
    ).toEqual(["analog.ls.mouseSpeed", "analog.ls.mouseSmoothingMs"]);
    // The defaults are in range, so a stick with none of them is fine.
    expect(analogIssues({ ls: { mode: "mouse" } })).toEqual([]);
  });

  it("are checked in every mode, as the knob step is", () => {
    expect(
      analogIssues({ ls: { mode: "standard", mouseSpeed: 20 } }).map(
        (issue) => issue.path,
      ),
    ).toEqual(["analog.ls.mouseSpeed"]);
  });
});
