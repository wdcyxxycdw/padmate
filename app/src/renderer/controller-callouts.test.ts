import { describe, expect, it } from "vitest";
import { controllerCallouts } from "./controller-callouts";

const controls = (info: Parameters<typeof controllerCallouts>[0]) =>
  controllerCallouts(info).map((callout) => callout.control);

describe("the function keys among the callouts (ADR-0069)", () => {
  it("are View and Menu on a pad that has both", () => {
    for (const type of ["xboxone", "ps5", "switchpro", "unknown"]) {
      const list = controls({ type });
      expect(list, type).toContain("back");
      expect(list, type).toContain("start");
    }
  });

  it("are Menu alone on a single Joy-Con held sideways", () => {
    for (const type of ["joyconleft", "joyconright"]) {
      const list = controls({ type, buttonMask: 0xf });
      expect(list, type).not.toContain("back");
      expect(list, type).toContain("start");
    }
  });

  it("are both on a single Joy-Con held upright", () => {
    const list = controls({ type: "joyconleft", buttonMask: 2 });
    expect(list).toContain("back");
    expect(list).toContain("start");
  });
});
