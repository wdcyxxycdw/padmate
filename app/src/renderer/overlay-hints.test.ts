import { describe, expect, it } from "vitest";
import { type KeyHint, maxHintsShown } from "../shared/overlay";
import { arrangeHints, hintControlName } from "./overlay-hints";

const hint = (control: string, label = control): KeyHint => ({
  control,
  label,
});
const controls = (entries: KeyHint[]) => entries.map((e) => e.control);

describe("the order of the key hints", () => {
  it("is the order of a pad: face buttons, shoulders, triggers, D-pad, centre buttons, sticks, the rest", () => {
    const given = [
      "touchpad",
      "rs.up",
      "misc1",
      "paddle2",
      "ls.up",
      "start",
      "back",
      "dpad.up",
      "rt",
      "lt",
      "rightshoulder",
      "leftshoulder",
      "north",
      "west",
      "east",
      "south",
    ].map((control) => hint(control));
    expect(controls(arrangeHints(given).shown)).toEqual([
      "south",
      "east",
      "west",
      "north",
      "leftshoulder",
      "rightshoulder",
      "lt",
      "rt",
      "dpad.up",
      "back",
      "start",
      "ls.up",
      "rs.up",
      "paddle2",
      "misc1",
      "touchpad",
    ]);
  });

  it("does not depend on the order they came in", () => {
    const given = ["south", "lt", "ls.up", "dpad.down", "start"].map(
      (control) => hint(control),
    );
    const forward = controls(arrangeHints(given).shown);
    expect(controls(arrangeHints([...given].reverse()).shown)).toEqual(forward);
    expect(
      controls(
        arrangeHints([given[2], given[0], given[4], given[3], given[1]]).shown,
      ),
    ).toEqual(forward);
  });

  it("knows the aliases the daemon accepts, and puts controls it does not know last, by name", () => {
    expect(
      controls(
        arrangeHints([hint("zeta"), hint("alpha"), hint("rb"), hint("a")])
          .shown,
      ),
    ).toEqual(["a", "rb", "alpha", "zeta"]);
  });

  it("puts the half axes, stick rings and knob ticks with their stick", () => {
    expect(
      controls(
        arrangeHints([
          hint("rs.cw"),
          hint("righty+"),
          hint("ls.inner"),
          hint("lt.full"),
          hint("ls.active"),
          hint("lefttrigger+"),
        ]).shown,
      ),
    ).toEqual([
      "lt.full",
      "ls.active",
      "ls.inner",
      "rs.cw",
      "righty+",
      "lefttrigger+",
    ]);
  });

  it("is stable: the same input gives the same output, and the input is left as it is", () => {
    const given = [hint("rt"), hint("south"), hint("ls.up")];
    const copy = structuredClone(given);
    const first = arrangeHints(given);
    expect(arrangeHints(given)).toEqual(first);
    expect(given).toEqual(copy);
  });
});

describe("how many key hints are shown", () => {
  // Controls the panel knows, in the order it puts them.
  const known = [
    "south",
    "east",
    "west",
    "north",
    "leftshoulder",
    "rightshoulder",
    "lt",
    "rt",
    "dpad.up",
    "dpad.down",
    "dpad.left",
    "dpad.right",
    "back",
    "start",
    "guide",
    "leftstick",
  ];
  const sixteen = known.slice(0, maxHintsShown).map((control) => hint(control));

  it("is all of them up to sixteen, with no mark for more", () => {
    expect(arrangeHints([])).toEqual({ shown: [], more: false });
    expect(arrangeHints(sixteen).shown).toHaveLength(16);
    expect(arrangeHints(sixteen).more).toBe(false);
  });

  it("is the first sixteen in order from seventeen on, with the mark", () => {
    const seventeen = [...sixteen, hint("touchpad")];
    const arranged = arrangeHints(seventeen);
    expect(controls(arranged.shown)).toEqual(controls(sixteen));
    expect(arranged.more).toBe(true);
    // The ones that are cut are the last in pad order, not the last sent.
    const front = arrangeHints([hint("zz"), ...sixteen, hint("south")]);
    expect(front.shown[0].control).toBe("south");
    expect(controls(front.shown)).not.toContain("zz");
    expect(front.more).toBe(true);
  });
});

describe("the name of a control in the panel", () => {
  it("is the generic one when the pad is not known", () => {
    expect(hintControlName("south", undefined, "zh-CN")).toBe("S");
    expect(hintControlName("rightshoulder", undefined, "en")).toBe("R");
    expect(hintControlName("lt", undefined, "en")).toBe("LT");
  });

  it("is the one printed on the pad", () => {
    expect(hintControlName("south", { type: "xboxone" }, "zh-CN")).toBe("A");
    expect(hintControlName("rightshoulder", { type: "xboxone" }, "zh-CN")).toBe(
      "RB",
    );
    expect(hintControlName("south", { type: "ps5" }, "zh-CN")).toBe("×");
    expect(hintControlName("back", { type: "ps5" }, "en")).toBe("Create");
    expect(hintControlName("rightshoulder", { type: "switchpro" }, "en")).toBe(
      "R",
    );
  });

  it("follows the labels the pad reports and the language, as the key list does", () => {
    expect(
      hintControlName(
        "east",
        { type: "xboxone", buttonLabels: { east: "Z" } },
        "en",
      ),
    ).toBe("Z");
    expect(hintControlName("ls.cw", { type: "xboxone" }, "zh-CN")).toBe(
      "LS 顺时针一格",
    );
    expect(hintControlName("ls.cw", { type: "xboxone" }, "en")).toBe(
      "LS clockwise tick",
    );
    expect(hintControlName("dpad.upright", undefined, "zh-CN")).toBe(
      "十字键 ↗",
    );
    expect(hintControlName("lt.full", { type: "ps5" }, "zh-CN")).toBe(
      "L2 按到底",
    );
  });
});
