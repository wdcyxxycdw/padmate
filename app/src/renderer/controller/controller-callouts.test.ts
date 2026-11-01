import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { sdlPads } from "../testing/sdl-pads";
import { controllerCallouts } from "./controller-callouts";

const controls = (info: Parameters<typeof controllerCallouts>[0]) =>
  controllerCallouts(info).map((callout) => callout.control);

describe("the function keys among the callouts (ADR-0012)", () => {
  it("are View and Menu on a pad that has both", () => {
    for (const type of ["xboxone", "ps5", "switchpro", "unknown"]) {
      const list = controls({ type });
      expect(list, type).toContain("back");
      expect(list, type).toContain("start");
    }
  });

  it("are Menu alone on a single Joy-Con held sideways", () => {
    for (const info of [
      sdlPads.joyConLeftSideways,
      sdlPads.joyConRightSideways,
    ]) {
      const list = controls(info);
      expect(list, info.type).not.toContain("back");
      expect(list, info.type).toContain("start");
    }
  });

  it("are the one each single Joy-Con held upright has", () => {
    // − on the left one, + on the right one.
    expect(controls(sdlPads.joyConLeftUpright)).toContain("back");
    expect(controls(sdlPads.joyConLeftUpright)).not.toContain("start");
    expect(controls(sdlPads.joyConRightUpright)).toContain("start");
    expect(controls(sdlPads.joyConRightUpright)).not.toContain("back");
  });
});

describe("the stick callouts", () => {
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

describe("the callouts follow the controls SDL reports", () => {
  const callout = (info: Parameters<typeof controllerCallouts>[0], c: string) =>
    controllerCallouts(info).find((entry) => entry.control === c);
  const standard = [
    "lefttrigger",
    "leftshoulder",
    "back",
    "start",
    "righttrigger",
    "rightshoulder",
    "leftstick",
    "dpad.up",
    "dpad.left",
    "dpad.right",
    "dpad.down",
    "north",
    "east",
    "west",
    "south",
    "rightstick",
  ];

  it("keep the standard layout when SDL did not say", () => {
    expect(controls({ type: "xboxone" }).sort()).toEqual([...standard].sort());
    expect(controls({ type: "switchpro" }).sort()).toEqual(
      [...standard].sort(),
    );
    expect(controls({ type: "ps4" }).sort()).toEqual(
      [...standard, "touchpad"].sort(),
    );
    expect(controls({ type: "ps3" })).not.toContain("touchpad");
  });

  it.each([
    ["xboxSeries", ["misc1"]],
    ["xboxElite", ["paddle1", "paddle2", "paddle3", "paddle4"]],
    ["ps4", ["touchpad"]],
    ["ps5", ["touchpad", "misc1"]],
    [
      "ps5Edge",
      ["touchpad", "misc1", "paddle1", "paddle2", "paddle3", "paddle4"],
    ],
    ["switchPro", ["misc1"]],
    ["joyConPair", ["misc1", "paddle1", "paddle2", "paddle3", "paddle4"]],
  ] as const)("give %s its extra buttons: %j", (name, extra) => {
    expect(controls(sdlPads[name]).sort()).toEqual(
      [...standard, ...extra].sort(),
    );
  });

  it("never give the Xbox, PS or Home button a chip", () => {
    for (const info of Object.values(sdlPads))
      expect(controls(info)).not.toContain("guide");
  });

  it("show only what a single Joy-Con held sideways has", () => {
    expect(controls(sdlPads.joyConLeftSideways).sort()).toEqual(
      [
        "north",
        "east",
        "west",
        "south",
        "leftstick",
        "leftshoulder",
        "rightshoulder",
        "start",
        "paddle2",
        "paddle4",
      ].sort(),
    );
    expect(controls(sdlPads.joyConRightSideways)).toEqual(
      expect.arrayContaining(["paddle1", "paddle3", "leftstick"]),
    );
    expect(controls(sdlPads.joyConRightSideways)).not.toContain("rightstick");
    // Its SL and SR are the shoulders, its L and ZL (R and ZR) paddles.
    expect(callout(sdlPads.joyConLeftSideways, "leftshoulder")?.label).toBe(
      "SL",
    );
    expect(callout(sdlPads.joyConLeftSideways, "paddle2")?.label).toBe("L");
    expect(callout(sdlPads.joyConLeftSideways, "paddle4")?.label).toBe("ZL");
    expect(callout(sdlPads.joyConRightSideways, "paddle1")?.label).toBe("R");
    expect(callout(sdlPads.joyConRightSideways, "paddle3")?.label).toBe("ZR");
  });

  it("show only what a single Joy-Con held upright has", () => {
    expect(controls(sdlPads.joyConLeftUpright).sort()).toEqual(
      [
        "lefttrigger",
        "leftshoulder",
        "back",
        "leftstick",
        "dpad.up",
        "dpad.left",
        "dpad.right",
        "dpad.down",
        "misc1",
        "paddle2",
        "paddle4",
      ].sort(),
    );
    expect(controls(sdlPads.joyConRightUpright).sort()).toEqual(
      [
        "righttrigger",
        "rightshoulder",
        "start",
        "north",
        "east",
        "west",
        "south",
        "rightstick",
        "paddle1",
        "paddle3",
      ].sort(),
    );
    // Upright, the right one's SL and SR are paddles and R, ZR its shoulder
    // and trigger, though it has face buttons.
    expect(callout(sdlPads.joyConRightUpright, "paddle1")?.label).toBe("SR");
    expect(callout(sdlPads.joyConRightUpright, "paddle3")?.label).toBe("SL");
    expect(callout(sdlPads.joyConRightUpright, "rightshoulder")?.label).toBe(
      "R",
    );
    expect(callout(sdlPads.joyConLeftUpright, "misc1")?.label).toBe("Capture");
  });

  it("put the extra buttons in one row below the controller, the left hand's on the left", () => {
    const row = (info: Parameters<typeof controllerCallouts>[0]) =>
      controllerCallouts(info)
        .filter((entry) => entry.placement === "bottom")
        .sort((a, b) => (a.offset ?? 0) - (b.offset ?? 0))
        .map((entry) => [entry.control, entry.offset]);
    expect(row(sdlPads.ps5Edge)).toEqual([
      ["paddle4", -2],
      ["paddle2", -1],
      ["misc1", 0],
      ["paddle1", 1],
      ["paddle3", 2],
    ]);
    expect(row(sdlPads.switchPro)).toEqual([["misc1", 0]]);
    expect(row(sdlPads.joyConLeftSideways)).toEqual([
      ["paddle4", -0.5],
      ["paddle2", 0.5],
    ]);
    expect(row({ type: "xboxone" })).toEqual([]);
  });

  it("center a short column on the middle of the stage", () => {
    // The sideways Joy-Con's stick alone, its four face buttons.
    const info = sdlPads.joyConLeftSideways;
    expect(callout(info, "leftstick")?.slot).toBe(4);
    expect(callout(info, "north")?.slot).toBe(3);
    expect(callout(info, "south")?.slot).toBe(6);
    // A full column is where it was.
    expect(callout(sdlPads.ps5, "dpad.up")?.slot).toBe(2);
  });
});
