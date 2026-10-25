import { describe, expect, it } from "vitest";
import type { ProfileAction } from "../shared/generated/schema";
import {
  actionSteps,
  defaultWaitMs,
  isEmptyStep,
  maxTextUnits,
  movedStep,
  newStep,
  stepKind,
  stepsAction,
  textProblem,
  textProblemText,
  textUnits,
} from "./sequence-model";

describe("steps", () => {
  it("are of the kind their one member says", () => {
    expect(stepKind({ keys: "Ctrl+A" })).toBe("keys");
    expect(stepKind({ keys: "" })).toBe("keys");
    expect(stepKind({ wait: 100 })).toBe("wait");
    expect(stepKind({ text: "hi" })).toBe("text");
    expect(stepKind({ text: "" })).toBe("text");
  });
  it("are empty while their shortcut or text is empty; a wait never is", () => {
    expect(isEmptyStep({ keys: "" })).toBe(true);
    expect(isEmptyStep({ keys: "  " })).toBe(true);
    expect(isEmptyStep({ keys: "F13" })).toBe(false);
    expect(isEmptyStep({ text: "" })).toBe(true);
    // A space is a text.
    expect(isEmptyStep({ text: " " })).toBe(false);
    expect(isEmptyStep({ wait: 1 })).toBe(false);
  });
  it("start empty, except a wait, which starts at 100 ms", () => {
    expect(newStep("keys")).toEqual({ keys: "" });
    expect(newStep("text")).toEqual({ text: "" });
    expect(newStep("wait")).toEqual({ wait: defaultWaitMs });
    expect(defaultWaitMs).toBe(100);
  });
});

describe("actionSteps", () => {
  it("lists the shortcuts of a keys action that has several", () => {
    expect(
      actionSteps({ type: "keys", keys: ["Ctrl+K", "S"], mode: "tap" }),
    ).toEqual([{ keys: "Ctrl+K" }, { keys: "S" }]);
  });
  it("lists the steps of a sequence action", () => {
    const steps = [{ keys: "Ctrl+A" }, { wait: 5 }, { text: "x" }];
    expect(actionSteps({ type: "sequence", steps })).toBe(steps);
  });
  it("has none for a single shortcut or another action", () => {
    expect(actionSteps({ type: "keys", keys: "Ctrl+Z" })).toBeNull();
    expect(actionSteps({ type: "text", text: "hi" })).toBeNull();
    expect(actionSteps({ type: "macro", file: "a.ts" })).toBeNull();
    expect(actionSteps(undefined)).toBeNull();
  });
});

describe("stepsAction", () => {
  const single: ProfileAction = { type: "keys", keys: "Ctrl+Z" };
  it("makes shortcuts only a keys action that taps, as before", () => {
    expect(stepsAction([{ keys: "Ctrl+Z" }, { keys: "" }], single)).toEqual({
      type: "keys",
      keys: ["Ctrl+Z", ""],
      mode: "tap",
    });
  });
  it("leaves the mode of a list as it is when a step goes", () => {
    expect(
      stepsAction([{ keys: "F14" }, { keys: "F15" }], {
        type: "keys",
        keys: ["F13", "F14", "F15"],
      }),
    ).toEqual({ type: "keys", keys: ["F14", "F15"] });
    expect(
      stepsAction([{ keys: "F14" }], {
        type: "keys",
        keys: ["F13", "F14"],
      }),
    ).toEqual({ type: "keys", keys: "F14" });
  });
  it("makes one shortcut left a single shortcut", () => {
    expect(
      stepsAction([{ keys: "S" }], {
        type: "keys",
        keys: ["Ctrl+K", "S"],
        mode: "tap",
      }),
    ).toEqual({ type: "keys", keys: "S", mode: "tap" });
  });
  it("drops the repeat, which a list does not have, and keeps the rest", () => {
    expect(
      stepsAction([{ keys: "A" }, { keys: "B" }], {
        type: "keys",
        keys: "A",
        repeat: { rateHz: 5 },
        mode: "hold",
      }),
    ).toEqual({ type: "keys", keys: ["A", "B"], mode: "tap" });
  });
  it("makes a wait or a text a sequence action, keeping every step", () => {
    const steps = [{ keys: "Ctrl+A" }, { wait: 100 }, { text: "" }];
    expect(stepsAction(steps, single)).toEqual({ type: "sequence", steps });
  });
  it("turns a sequence back into shortcuts when no wait or text is left", () => {
    expect(
      stepsAction([{ keys: "A" }, { keys: "B" }], {
        type: "sequence",
        steps: [{ keys: "A" }, { wait: 5 }, { keys: "B" }],
      }),
    ).toEqual({ type: "keys", keys: ["A", "B"], mode: "tap" });
    expect(
      stepsAction([{ keys: "A" }], {
        type: "sequence",
        steps: [{ keys: "A" }, { text: "x" }],
      }),
    ).toEqual({ type: "keys", keys: "A", mode: "tap" });
  });
  it("makes no steps an empty shortcut", () => {
    expect(stepsAction([], single)).toEqual({
      type: "keys",
      keys: "",
      mode: "tap",
    });
  });
});

describe("movedStep", () => {
  const steps = [{ keys: "A" }, { wait: 5 }, { text: "x" }];
  it("moves a step up or down", () => {
    expect(movedStep(steps, 1, -1)).toEqual([
      { wait: 5 },
      { keys: "A" },
      { text: "x" },
    ]);
    expect(movedStep(steps, 1, 1)).toEqual([
      { keys: "A" },
      { text: "x" },
      { wait: 5 },
    ]);
  });
  it("does not change the list it was given", () => {
    movedStep(steps, 0, 1);
    expect(steps[0]).toEqual({ keys: "A" });
  });
  it("leaves the first step where it is going up, and the last going down", () => {
    expect(movedStep(steps, 0, -1)).toBe(steps);
    expect(movedStep(steps, 2, 1)).toBe(steps);
    expect(movedStep(steps, 5, -1)).toBe(steps);
  });
});

describe("a text", () => {
  it("counts UTF-16 code units, an emoji as two", () => {
    expect(textUnits("abc")).toBe(3);
    expect(textUnits("你好")).toBe(2);
    expect(textUnits("😀")).toBe(2);
    expect(textUnits("a\nb")).toBe(3);
  });
  it("is fine from one character up to the limit", () => {
    expect(textProblem("")).toBeNull();
    expect(textProblem("a")).toBeNull();
    expect(textProblem("x".repeat(maxTextUnits))).toBeNull();
    expect(textProblem("😀".repeat(maxTextUnits / 2))).toBeNull();
    expect(maxTextUnits).toBe(256);
  });
  it("is too long one unit over, however it is made", () => {
    expect(textProblem("x".repeat(maxTextUnits + 1))).toBe("long");
    expect(textProblem(`${"😀".repeat(maxTextUnits / 2)}x`)).toBe("long");
  });
  it("may have a newline and a tab", () => {
    expect(textProblem("line\n\tindented")).toBeNull();
  });
  it.each([
    ["\u0000", "NUL"],
    ["\u0008", "backspace"],
    ["\r", "carriage return"],
    ["a\r\nb", "CRLF"],
    ["\u001b[0m", "escape"],
    ["\u007f", "DEL"],
    ["\u0085", "C1 next line"],
    ["\u009f", "last C1"],
  ])("refuses a control character: %j (%s)", (text) => {
    expect(textProblem(text)).toBe("control");
  });
  it("accepts the characters right outside the control ranges", () => {
    expect(textProblem("  ¡")).toBeNull();
    expect(textProblem(" ~")).toBeNull();
  });
  it("is told about in plain words, in both languages", () => {
    expect(textProblemText("long", "zh-CN")).toBe(
      "最多 256 个字符，表情符号占两个",
    );
    expect(textProblemText("long", "en")).toBe(
      "At most 256 characters; an emoji counts as two",
    );
    expect(textProblemText("control", "zh-CN")).toBe(
      "不能含有换行和制表符以外的控制字符",
    );
    expect(textProblemText("control", "en")).toBe(
      "No control characters except a newline and a tab",
    );
  });
});
