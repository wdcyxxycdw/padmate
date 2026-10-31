import { readFileSync } from "node:fs";
import { resolve } from "node:path";
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

describe("the stick callouts (UI-39)", () => {
  const find = (type: string, control: string) =>
    controllerCallouts({ type }).find((callout) => callout.control === control);

  it("have one callout per stick, two slots tall", () => {
    for (const type of ["xboxone", "ps5", "switchpro", "joyconleft"]) {
      const list = controls({ type });
      expect(list.filter((control) => control === "leftstick")).toHaveLength(1);
      expect(list.filter((control) => control === "rightstick")).toHaveLength(
        1,
      );
      expect(find(type, "leftstick")?.span, type).toBe(2);
      expect(find(type, "rightstick")?.span, type).toBe(2);
      // No callout of the press cell: it shares the stick's leader.
      expect(list).not.toContain("leftstick.press");
    }
  });

  it("leave no two chips of a column on the same slot", () => {
    for (const type of ["xboxone", "ps5", "switchpro", "joyconright"]) {
      for (const side of ["left", "right"] as const) {
        const taken = new Set<number>();
        for (const callout of controllerCallouts({ type })) {
          if (callout.placement !== side) continue;
          for (let n = 0; n < (callout.span ?? 1); n++) {
            const slot = (callout.slot ?? 0) + n;
            expect(taken.has(slot), `${type} ${side} ${callout.control}`).toBe(
              false,
            );
            taken.add(slot);
          }
        }
        // Slots 2 to 7: the four buttons and the stick's two.
        expect([...taken].sort((a, b) => a - b)).toEqual([2, 3, 4, 5, 6, 7]);
      }
    }
  });

  it("keep the chips above the stick where they were", () => {
    expect(find("xboxone", "leftstick")?.slot).toBe(2);
    expect(find("xboxone", "dpad.up")?.slot).toBe(4);
    expect(find("xboxone", "dpad.down")?.slot).toBe(7);
    expect(find("ps5", "dpad.up")?.slot).toBe(2);
    expect(find("ps5", "leftstick")?.slot).toBe(6);
    expect(find("xboxone", "north")?.slot).toBe(2);
    expect(find("xboxone", "south")?.slot).toBe(5);
    expect(find("xboxone", "rightstick")?.slot).toBe(6);
  });
});

describe("the stick callouts in the three chip densities (controller-3d.css)", () => {
  // Full, compact and mini, in the order the stylesheet sets them.
  const css = readFileSync(resolve(__dirname, "controller-3d.css"), "utf8");
  const sizes = (name: string) =>
    [...css.matchAll(new RegExp(String.raw`${name}: (\d+)px;`, "g"))].map((m) =>
      Number(m[1]),
    );
  const chipH = sizes("--chip-h");
  const pitch = sizes("--pitch");
  const gap = sizes("--cell-gap");
  // The badge in a cell: 28, 22 and 24 px, and 1 px of border on each side.
  const badge = [28, 22, 24];

  it("sets every size for the three densities", () => {
    expect(chipH).toHaveLength(3);
    expect(pitch).toHaveLength(3);
    expect(gap).toHaveLength(3);
  });

  it.each([0, 1, 2])("keeps the chips of a column apart in density %i", (d) => {
    for (const type of ["xboxone", "ps5", "switchpro", "joyconleft"]) {
      for (const side of ["left", "right"] as const) {
        // The box of a chip about the middle of the stage, as the stylesheet
        // places it: slots count from 4 at the middle, a chip as tall as its
        // span of slots minus the gap between two chips.
        const boxes = controllerCallouts({ type })
          .filter((callout) => callout.placement === side)
          .map((callout) => {
            const span = callout.span ?? 1;
            const centre =
              ((callout.slot ?? 0) + (span - 1) / 2 - 4) * pitch[d];
            const height = chipH[d] + (span - 1) * pitch[d];
            return {
              control: callout.control,
              top: centre - height / 2,
              bottom: centre + height / 2,
              height,
            };
          })
          .sort((a, b) => a.top - b.top);
        expect(boxes).toHaveLength(5);
        boxes.slice(1).forEach((box, index) => {
          expect(
            box.top - boxes[index].bottom,
            `${type} ${side} ${boxes[index].control} / ${box.control}`,
          ).toBe(pitch[d] - chipH[d]);
        });
        // A cell is about half the chip and holds the badge.
        const stick = boxes.find((box) => box.height > chipH[d]);
        expect((stick?.height ?? 0) - gap[d]).toBeGreaterThanOrEqual(
          2 * (badge[d] + 2),
        );
      }
    }
  });
});
