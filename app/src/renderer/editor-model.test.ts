import { describe, expect, it } from "vitest";
import type { ProfileDiagnostic } from "../shared/api";
import type { Profile, ProfileAction } from "../shared/generated/schema";
import {
  actionSummary,
  actionText,
  bindingAt,
  bindingDelays,
  gestureName,
  hasDepth,
  holdShortcut,
  isFileOnlyAction,
  isHintsAction,
  isJumpAction,
  isLabelOnlyWarning,
  isPrecisionAction,
  isReservedShortcut,
  jumpPercent,
  mappingGesture,
  mappings,
  precisionPercent,
  resolveTiming,
  savedForm,
  setBinding,
  settleTap,
  shortText,
  stableStringify,
  stepSummary,
  tapGestureFor,
  withoutMinRate,
} from "./editor-model";

const source: Profile = {
  schemaVersion: 1,
  name: "Paint | pen",
  layers: {
    base: { a: { press: { type: "keys", keys: "F13" } } },
    tools: { south: { block: true } },
  },
  script: {
    api: "0.21",
    entry: "main.ts",
    params: { nested: { enabled: true } },
  },
};
describe("mapping drafts", () => {
  it("inherits configured timing when an override is cleared and preserves explicit zero", () => {
    expect(
      resolveTiming(
        { holdMs: 700, multiTapMs: 330 },
        { holdMs: undefined },
        { multiTapMs: 0 },
      ),
    ).toMatchObject({ holdMs: 700, multiTapMs: 0 });
    expect(
      bindingDelays(
        {
          tap: { type: "keys", keys: "A" },
          hold: { type: "keys", keys: "B" },
          timing: { holdMs: undefined },
        },
        { holdMs: 700 },
      ).find((x) => x.gesture === "hold")?.ms,
    ).toBe(700);
  });
  it("edits canonical bindings without duplicate aliases or losing unrelated fields", () => {
    const next = setBinding(source, "base", "south", {
      tap: {
        type: "script",
        name: "brush",
        args: { size: 2, flag: false, list: [1, 2] },
      },
    });
    expect(next.layers.base.a).toBeUndefined();
    expect(bindingAt(next, "base", "south").tap).toEqual({
      type: "script",
      name: "brush",
      args: { size: 2, flag: false, list: [1, 2] },
    });
    expect(next.script).toEqual(source.script);
    expect(source.layers.base.a).toBeDefined();
  });
  it("keeps the haptic of a binding that another edit does not touch (MAP-25)", () => {
    const withHaptic: Profile = {
      ...source,
      layers: {
        base: {
          south: { haptic: "error", press: { type: "keys", keys: "F13" } },
          east: { haptic: "tick", press: { type: "keys", keys: "F14" } },
        },
      },
    };
    const next = setBinding(withHaptic, "base", "east", {
      haptic: "tick",
      press: { type: "keys", keys: "F15" },
    });
    expect(bindingAt(next, "base", "south").haptic).toBe("error");
    expect(bindingAt(next, "base", "east")).toEqual({
      haptic: "tick",
      press: { type: "keys", keys: "F15" },
    });
    // A binding with the haptic taken out is written without the key.
    const none = setBinding(next, "base", "east", {
      press: { type: "keys", keys: "F15" },
    });
    expect("haptic" in bindingAt(none, "base", "east")).toBe(false);
  });
  it("explains hold and multi-tap waits while pure hold has no own wait", () => {
    expect(bindingDelays({ hold: { type: "keys", keys: "Ctrl" } })[0].ms).toBe(
      0,
    );
    expect(
      bindingDelays(
        {
          tap: { type: "keys", keys: "F13" },
          hold: { type: "keys", keys: "F14" },
          doubleTap: { type: "keys", keys: "F15" },
        },
        { holdMs: 300, multiTapMs: 220 },
      ).find((x) => x.gesture === "tap")?.ms,
    ).toBe(220);
  });
  it("tells reserved system shortcuts, and nothing else", () => {
    for (const chord of ["Win+L", "Ctrl+Alt+Del", "Alt+Tab", "Ctrl+Shift+Esc"])
      expect(isReservedShortcut(chord), chord).toBe(true);
    // A bare letter is not warned about (F6).
    for (const chord of ["a", ";", "Ctrl+Z", "F13"])
      expect(isReservedShortcut(chord), chord).toBe(false);
  });
  it("compares values without regard to object key order", () => {
    expect(stableStringify({ b: 1, a: { d: [2, { y: 1, x: 0 }], c: 3 } })).toBe(
      stableStringify({ a: { c: 3, d: [2, { x: 0, y: 1 }] }, b: 1 }),
    );
    // Arrays keep their order; undefined values drop out as in JSON.
    expect(stableStringify([1, 2])).not.toBe(stableStringify([2, 1]));
    expect(stableStringify({ a: 1, b: undefined })).toBe(
      stableStringify({ a: 1 }),
    );
    expect(stableStringify(undefined)).toBe("undefined");
  });
});

describe("scripts (macros) keys run", () => {
  const action = { type: "macro", file: "复制全部.ts" } as const;
  it("are named without the extension, or as missing", () => {
    expect(actionText(action, "zh-CN")).toBe("复制全部");
    expect(actionText({ type: "macro", file: "下一个.js" }, "en")).toBe(
      "下一个",
    );
    expect(actionText(action, "zh-CN", [], [], ["复制全部.ts"])).toBe(
      "找不到「复制全部」",
    );
    expect(actionText(action, "en", [], [], ["复制全部.ts"])).toBe(
      "“复制全部” not found",
    );
    expect(actionText(action, "zh-CN", [], [], ["其他.ts"])).toBe("复制全部");
  });
});

describe("sequence and text actions (MAP-22, OUT-21)", () => {
  const sequence: ProfileAction = {
    type: "sequence",
    steps: [{ keys: "Ctrl+A" }, { wait: 100 }, { text: "你好\n世界" }],
  };
  const text: ProfileAction = {
    type: "text",
    text: "Best regards,\tKim 😀 and the rest",
  };
  it("are the step editor's own (UI-31), so they are not file-only", () => {
    expect(isFileOnlyAction(sequence)).toBe(false);
    expect(isFileOnlyAction(text)).toBe(false);
  });
  it("are saved as they are when nothing in them is empty", () => {
    const draft: Profile = {
      schemaVersion: 1,
      name: "p",
      layers: {
        base: {
          south: { press: sequence },
          east: { tap: text },
          // A hand-written sequence of shortcuts stays a sequence.
          west: {
            press: { type: "sequence", steps: [{ keys: "A" }, { keys: "B" }] },
          },
        },
      },
    };
    expect(savedForm(draft)).toEqual(draft);
  });
  it("leave out the steps still being filled in", () => {
    const draft: Profile = {
      schemaVersion: 1,
      name: "p",
      layers: {
        base: {
          south: {
            press: {
              type: "sequence",
              steps: [
                { keys: "Ctrl+A" },
                { keys: "" },
                { wait: 100 },
                { text: "" },
                { text: " " },
              ],
            },
          },
        },
      },
    };
    const before = structuredClone(draft);
    expect(savedForm(draft).layers.base.south).toEqual({
      press: {
        type: "sequence",
        steps: [{ keys: "Ctrl+A" }, { wait: 100 }, { text: " " }],
      },
    });
    expect(draft).toEqual(before);
  });
  it("are saved as the shortcuts they are left with, when no wait or text is", () => {
    const draft: Profile = {
      schemaVersion: 1,
      name: "p",
      layers: {
        base: {
          // Keys, then a text not typed yet: one shortcut.
          south: {
            press: {
              type: "sequence",
              steps: [{ keys: "Ctrl+A" }, { text: "" }],
            },
          },
          east: {
            press: {
              type: "sequence",
              steps: [{ keys: "A" }, { text: "" }, { keys: "B" }],
            },
          },
        },
      },
    };
    const base = savedForm(draft).layers.base;
    expect(base.south).toEqual({
      press: { type: "keys", keys: "Ctrl+A", mode: "tap" },
    });
    expect(base.east).toEqual({
      press: { type: "keys", keys: ["A", "B"], mode: "tap" },
    });
  });
  it("leave the gesture out when nothing in them is filled in", () => {
    const draft: Profile = {
      schemaVersion: 1,
      name: "p",
      layers: {
        base: {
          south: {
            label: "x",
            press: { type: "sequence", steps: [{ keys: "" }, { text: "" }] },
            tap: { type: "text", text: "" },
          },
          east: { hold: { type: "text", text: "" } },
        },
      },
    };
    expect(savedForm(draft).layers.base).toEqual({ south: { label: "x" } });
  });
  it("are summarized step by step, or by their text", () => {
    expect(actionSummary(sequence)).toBe('Ctrl+A → 100 ms → "你好 世界"');
    expect(actionSummary(text)).toBe('"Best regards, Ki…"');
    expect(stepSummary({ keys: "F13" })).toBe("F13");
    expect(stepSummary({ wait: 5 })).toBe("5 ms");
    expect(stepSummary({ text: "a" })).toBe('"a"');
  });
  it("are named in both languages", () => {
    expect(actionText(sequence, "zh-CN")).toBe("3 步序列");
    expect(actionText(sequence, "en")).toBe("3-step sequence");
    expect(actionText(text, "zh-CN")).toBe("输入文字「Best regards, Ki…」");
    expect(actionText(text, "en")).toBe("Type “Best regards, Ki…”");
  });
  it("cut a text between characters, on one line", () => {
    expect(shortText("short")).toBe("short");
    expect(shortText("a\n\tb")).toBe("a b");
    expect(shortText("x".repeat(16))).toBe("x".repeat(16));
    expect(shortText("😀".repeat(20))).toBe(`${"😀".repeat(16)}…`);
  });
});

describe("the shortcut of a keys action in words", () => {
  it("names the volume and media keys, in both languages", () => {
    const action: ProfileAction = { type: "keys", keys: "Ctrl+VolumeUp" };
    expect(actionText(action, "zh-CN")).toBe("Ctrl+音量加");
    expect(actionText(action, "en")).toBe("Ctrl+Volume up");
    expect(
      actionText({ type: "keys", keys: ["MediaPlayPause", "MediaNext"] }, "en"),
    ).toBe("Play/Pause → Next track");
  });
  it("leaves other shortcuts as written", () => {
    expect(actionText({ type: "keys", keys: "Ctrl+Z" }, "en")).toBe("Ctrl+Z");
    expect(
      actionText({ type: "keys", keys: ["F13", "Ctrl+WheelUp"] }, "zh-CN"),
    ).toBe("F13 → Ctrl+WheelUp");
  });
});

describe("profile switches from the file", () => {
  const action = { type: "profile", profile: "photo" } as const;
  it("are file-only, like layer actions", () => {
    expect(isFileOnlyAction(action)).toBe(true);
    expect(isFileOnlyAction({ type: "layer", op: "hold", layer: "x" })).toBe(
      true,
    );
    expect(isFileOnlyAction({ type: "keys", keys: "F13" })).toBe(false);
    // A legacy script's actions are kept but no longer set (SCR-50); keys
    // run scripts (macros) instead.
    expect(isFileOnlyAction({ type: "script", name: "brush" })).toBe(true);
    expect(isFileOnlyAction({ type: "macro", file: "复制全部.ts" })).toBe(
      false,
    );
    expect(isFileOnlyAction(undefined)).toBe(false);
  });
  it("name the target by display name when the library has one", () => {
    expect(actionText(action, "zh-CN")).toBe("切换到「photo」");
    expect(actionText(action, "en")).toBe("Switch to photo");
    const profiles = [{ id: "photo", name: "Photo" }, { id: "other" }];
    expect(actionText(action, "zh-CN", [], profiles)).toBe("切换到「Photo」");
    expect(
      actionText({ type: "profile", profile: "other" }, "en", [], profiles),
    ).toBe("Switch to other");
  });
});

describe("savedForm", () => {
  it("leaves out the parts still being filled in", () => {
    const draft: Profile = {
      schemaVersion: 1,
      name: "p",
      layers: {
        base: {
          color: "#fff",
          south: {
            label: "撤销",
            press: { type: "keys", keys: "" },
            hold: { type: "keys", keys: ["F13", " ", "F14"], mode: "tap" },
          },
          east: { press: { type: "script", name: " " } },
          west: { press: { type: "keys", keys: ["", ""] } },
          north: { press: { type: "keys", keys: "F15" } },
        },
        tools: { south: { press: { type: "keys", keys: "" } } },
      },
      common: { back: { tap: { type: "keys", keys: "" } } },
    };
    const before = structuredClone(draft);
    expect(savedForm(draft)).toEqual({
      schemaVersion: 1,
      name: "p",
      layers: {
        base: {
          color: "#fff",
          south: {
            label: "撤销",
            hold: { type: "keys", keys: ["F13", "F14"], mode: "tap" },
          },
          north: { press: { type: "keys", keys: "F15" } },
        },
        tools: {},
      },
      common: {},
    });
    expect(draft).toEqual(before);
  });

  it("keeps the haptic of a binding while its shortcut is being filled in (MAP-25)", () => {
    // The haptic is the binding's own, not the action's: the empty shortcut
    // leaves, the haptic and the label stay, and a binding left with only a
    // haptic is kept (it is a setting the user made).
    const draft: Profile = {
      schemaVersion: 1,
      name: "p",
      layers: {
        base: {
          south: {
            label: "撤销",
            haptic: "tick",
            press: { type: "keys", keys: "" },
          },
          east: { haptic: "pulse2", press: { type: "keys", keys: "F13" } },
        },
      },
    };
    expect(savedForm(draft)).toEqual({
      schemaVersion: 1,
      name: "p",
      layers: {
        base: {
          south: { label: "撤销", haptic: "tick" },
          east: { haptic: "pulse2", press: { type: "keys", keys: "F13" } },
        },
      },
    });
  });

  it("writes the current profile format version", () => {
    // A file of version 0 is opened as it is; a save rewrites the whole
    // document, so it is written at version 1 (PRO-01).
    const draft = {
      schemaVersion: 0,
      name: "p",
      layers: { base: { south: { press: { type: "keys", keys: "F13" } } } },
    } as unknown as Profile;
    expect(savedForm(draft).schemaVersion).toBe(1);
    expect(draft.schemaVersion).toBe(0);
    expect(Object.keys(savedForm(draft))).toEqual([
      "schemaVersion",
      "name",
      "layers",
    ]);
  });

  it("returns a filled-in draft as it is", () => {
    const draft: Profile = {
      schemaVersion: 1,
      name: "p",
      layers: { base: { south: { press: { type: "keys", keys: "F13" } } } },
    };
    expect(savedForm(draft)).toBe(draft);
  });

  it("leaves out the feedback switches that moved to the settings (ADR-0067)", () => {
    // rumble, led, pause and lowBattery are global now: a save drops them from
    // the file and keeps the members that stay.
    const draft: Profile = {
      schemaVersion: 1,
      name: "p",
      feedback: {
        rumble: false,
        led: true,
        pause: false,
        lowBattery: false,
        strength: 12000,
        pausedColor: "#112233",
      },
      layers: { base: {} },
    };
    const before = structuredClone(draft);
    expect(savedForm(draft)).toEqual({
      schemaVersion: 1,
      name: "p",
      feedback: { strength: 12000, pausedColor: "#112233" },
      layers: { base: {} },
    });
    expect(draft).toEqual(before);
    expect(Object.keys(savedForm(draft))).toEqual([
      "schemaVersion",
      "name",
      "feedback",
      "layers",
    ]);
  });

  it("drops a feedback object left with nothing", () => {
    const draft: Profile = {
      schemaVersion: 1,
      name: "p",
      feedback: { rumble: false, lowBattery: false },
      layers: { base: {} },
    };
    const saved = savedForm(draft);
    expect(saved).toEqual({
      schemaVersion: 1,
      name: "p",
      layers: { base: {} },
    });
    expect("feedback" in saved).toBe(false);
  });

  it("keeps a feedback object that has only the members that stay as it is", () => {
    const draft: Profile = {
      schemaVersion: 1,
      name: "p",
      feedback: { shortMs: 90 },
      layers: { base: {} },
    };
    expect(savedForm(draft)).toBe(draft);
  });
});

describe("label-only warning", () => {
  const warning = (
    pointer: string,
    changes: Partial<ProfileDiagnostic> = {},
  ): ProfileDiagnostic => ({
    severity: "warning",
    pointer,
    offset: 0,
    line: 1,
    col: 1,
    message: "binding has no action",
    ...changes,
  });

  it("recognizes a key of the base layer that has only its label", () => {
    const profile: Profile = {
      schemaVersion: 1,
      name: "t",
      layers: {
        base: {
          // A key named but not recorded yet.
          "dpad.up": { label: "画笔" },
          leftstick: { label: "移动" },
          r3: { label: "缩放" },
          north: { label: " " },
          south: { label: "x", block: true },
          west: {},
        },
        alt: { east: { label: "y" } },
      },
    };
    for (const pointer of [
      "/layers/base/dpad.up",
      "/layers/base/leftstick",
      // An alias in the file names the same control.
      "/layers/base/r3",
    ])
      expect(isLabelOnlyWarning(warning(pointer), profile), pointer).toBe(true);
    for (const pointer of [
      // No label, an empty one, or a block: the warning is about something else.
      "/layers/base/north",
      "/layers/base/south",
      "/layers/base/west",
      "/layers/alt/east",
      "/layers/base/dpad.up/label",
    ])
      expect(isLabelOnlyWarning(warning(pointer), profile), pointer).toBe(
        false,
      );
    for (const changes of [
      { severity: "error" as const },
      { message: "unknown control" },
    ])
      expect(
        isLabelOnlyWarning(warning("/layers/base/dpad.up", changes), profile),
      ).toBe(false);
    expect(isLabelOnlyWarning(warning("/layers/base/dpad.up"), null)).toBe(
      false,
    );
  });
});

describe("controls with a depth (ANA-12, ANA-15)", () => {
  it("lists the same controls as the daemon's HasDepth", () => {
    const withDepth = [
      "ls.up",
      "ls.downleft",
      "ls.active",
      "ls.inner",
      "rs.outer",
      "rs.upright",
      "leftx+",
      "righty-",
      "lefttrigger+",
      "righttrigger-",
      "lt",
      "rt",
      "lt.full",
      "rt.full",
    ];
    const without = [
      "ls.cw",
      "rs.ccw",
      "dpad.upleft",
      "dpad.up",
      "south",
      "a",
      "leftstick",
      "l3",
      "lb",
      "back",
      "ls",
      "ls.full",
      "lt.inner",
    ];
    for (const control of withDepth)
      expect(hasDepth(control), control).toBe(true);
    for (const control of without)
      expect(hasDepth(control), control).toBe(false);
  });

  it("takes minRateHz out of every repeat of a binding and nothing else", () => {
    const binding = {
      label: "Undo",
      press: {
        type: "keys" as const,
        keys: "Ctrl+Z",
        mode: "tap" as const,
        repeat: { delayMs: 300, rateHz: 12, minRateHz: 3 },
      },
      hold: {
        type: "keys" as const,
        keys: "Q",
        repeat: { rateHz: 5, minRateHz: 2 },
      },
      tap: { type: "keys" as const, keys: "X" },
    };
    const before = JSON.stringify(binding);
    expect(withoutMinRate(binding)).toEqual({
      label: "Undo",
      press: {
        type: "keys",
        keys: "Ctrl+Z",
        mode: "tap",
        repeat: { delayMs: 300, rateHz: 12 },
      },
      hold: { type: "keys", keys: "Q", repeat: { rateHz: 5 } },
      tap: { type: "keys", keys: "X" },
    });
    expect(JSON.stringify(binding)).toBe(before);
    // Nothing to take out: the same object comes back.
    const plain = { press: { type: "keys" as const, keys: "A" } };
    expect(withoutMinRate(plain)).toBe(plain);
  });
});

describe("the key hints action (HUD-03)", () => {
  const action = { type: "hud", op: "hints" } as const;

  it("is named for the editor in both languages", () => {
    expect(actionText(action, "zh-CN")).toBe("按键提示");
    expect(actionText(action, "en")).toBe("Key hints");
    expect(actionText(action)).toBe("按键提示");
  });

  it("is summarized for the debug lists", () => {
    expect(actionSummary(action)).toBe("hud hints");
  });

  it("is told apart from every other action", () => {
    expect(isHintsAction(action)).toBe(true);
    expect(isHintsAction(undefined)).toBe(false);
    expect(isHintsAction({ type: "keys", keys: "F13" })).toBe(false);
    expect(isHintsAction({ type: "mouse", op: "precision" })).toBe(false);
    expect(isPrecisionAction(action)).toBe(false);
    expect(isJumpAction(action)).toBe(false);
  });

  it("is set by the editor, so it is not file-only", () => {
    expect(isFileOnlyAction(action)).toBe(false);
  });

  it("is kept as it is when a binding is saved", () => {
    const binding = { press: action, label: "提示" };
    const saved = savedForm({
      ...source,
      layers: { base: { rightshoulder: binding } },
    });
    expect(saved.layers.base.rightshoulder).toEqual(binding);
  });
});

describe("the precision action (MOU-05)", () => {
  const action = { type: "mouse", op: "precision", factor: 0.3 } as const;

  it("is named with its factor as a percentage", () => {
    expect(actionText(action, "zh-CN")).toBe("精确模式 30%");
    expect(actionText(action, "en")).toBe("Precision 30%");
    expect(actionText({ ...action, factor: 0.05 }, "zh-CN")).toBe(
      "精确模式 5%",
    );
    expect(actionText({ ...action, factor: 1 }, "zh-CN")).toBe("精确模式 100%");
    // Without a factor, the daemon uses 0.3.
    expect(actionText({ type: "mouse", op: "precision" }, "zh-CN")).toBe(
      "精确模式 30%",
    );
    // A factor from the file that is not whole is shown as it is.
    expect(actionText({ ...action, factor: 0.125 }, "zh-CN")).toBe(
      "精确模式 12.5%",
    );
  });

  it("is summarized for the debug lists", () => {
    expect(actionSummary(action)).toBe("mouse precision 30%");
    expect(actionSummary({ type: "mouse", op: "precision" })).toBe(
      "mouse precision 30%",
    );
  });

  it("is told apart from every other action", () => {
    expect(isPrecisionAction(action)).toBe(true);
    expect(isPrecisionAction({ type: "mouse", op: "precision" })).toBe(true);
    expect(isPrecisionAction({ type: "keys", keys: "F13" })).toBe(false);
    expect(isPrecisionAction({ type: "macro", file: "复制全部.ts" })).toBe(
      false,
    );
    expect(isPrecisionAction(undefined)).toBe(false);
    // Another mouse action, or a keys action that merely has an op.
    expect(
      isPrecisionAction({ type: "mouse", op: "jump", x: 0.5, y: 0.5 }),
    ).toBe(false);
  });

  it("is set by the editor, so it is not file-only", () => {
    expect(isFileOnlyAction(action)).toBe(false);
  });

  it("reads its percentage", () => {
    expect(precisionPercent(action)).toBe(30);
    expect(precisionPercent({ type: "mouse", op: "precision" })).toBe(30);
    for (const percent of [5, 7, 29, 57, 100])
      expect(
        precisionPercent({ ...action, factor: percent / 100 }),
        String(percent),
      ).toBe(percent);
  });

  it("is saved as it is", () => {
    const profile: Profile = {
      schemaVersion: 1,
      name: "pointer",
      layers: { base: { lb: { press: action } } },
    };
    expect(savedForm(profile)).toBe(profile);
  });
});

describe("the pointer jump from the file (MOU-08)", () => {
  const jump = { type: "mouse", op: "jump", x: 0.5, y: 0.25 } as const;

  it("is file-only, since the editor cannot set it", () => {
    expect(isFileOnlyAction(jump)).toBe(true);
    expect(isJumpAction(jump)).toBe(true);
    expect(isJumpAction({ type: "mouse", op: "precision" })).toBe(false);
    expect(isJumpAction({ type: "keys", keys: "F13" })).toBe(false);
    expect(isJumpAction(undefined)).toBe(false);
  });

  it("names the place as a percentage of the desktop", () => {
    expect(actionText(jump, "zh-CN")).toBe("指针跳到屏幕位置 50%，25%");
    expect(actionText(jump, "en")).toBe("Pointer jumps to 50%, 25%");
    expect(jumpPercent(jump)).toEqual({ x: 50, y: 25 });
    expect(actionSummary(jump)).toBe("mouse jump 50%,25%");
  });
});

describe("the three mappings of the key drawer (ADR-0076)", () => {
  const shortcut = (keys: string): ProfileAction => ({ type: "keys", keys });

  it("are Tap, Hold and Double tap, in that order", () => {
    expect([...mappings]).toEqual(["tap", "hold", "doubleTap"]);
    expect(mappings.map((mapping) => gestureName(mapping, "zh-CN"))).toEqual([
      "点按",
      "长按",
      "双击",
    ]);
    expect(mappings.map((mapping) => gestureName(mapping, "en"))).toEqual([
      "Tap",
      "Hold",
      "Double tap",
    ]);
  });

  it("call a press a tap too, whichever of the two the file has", () => {
    expect(gestureName("press", "zh-CN")).toBe("点按");
    expect(gestureName("press", "en")).toBe("Tap");
    expect(gestureName("tap", "zh-CN")).toBe("点按");
    // The gestures the drawer no longer offers keep their names for lists.
    expect(gestureName("release", "zh-CN")).toBe("松开");
    expect(gestureName("tripleTap", "zh-CN")).toBe("三击");
    expect(gestureName("quadTap", "en")).toBe("Quadruple tap");
  });

  it("finds the gesture of the file that holds each mapping", () => {
    const binding = {
      press: shortcut("A"),
      hold: shortcut("B"),
      doubleTap: shortcut("C"),
    };
    expect(mappingGesture(binding, "tap")).toBe("press");
    expect(mappingGesture(binding, "hold")).toBe("hold");
    expect(mappingGesture(binding, "doubleTap")).toBe("doubleTap");
    expect(mappingGesture({ tap: shortcut("A") }, "tap")).toBe("tap");
    expect(mappingGesture({ release: shortcut("A") }, "tap")).toBeUndefined();
    expect(mappingGesture({}, "hold")).toBeUndefined();
  });

  it("write a tap as a press alone, and as a tap beside a hold or a double tap", () => {
    expect(tapGestureFor({})).toBe("press");
    expect(tapGestureFor({ press: shortcut("A") })).toBe("press");
    expect(tapGestureFor({ hold: shortcut("B") })).toBe("tap");
    expect(tapGestureFor({ doubleTap: shortcut("B") })).toBe("tap");
    // What the drawer does not offer does not count.
    expect(tapGestureFor({ release: shortcut("B") })).toBe("press");
    expect(tapGestureFor({ tripleTap: shortcut("B") })).toBe("press");
  });

  it("move a press to a tap when a hold comes, keeping its action and its place", () => {
    const before = {
      label: "Undo",
      press: shortcut("Ctrl+Z"),
      haptic: "tick" as const,
    };
    const added = settleTap({ ...before, hold: shortcut("Ctrl+Y") });
    expect(added).toEqual({
      label: "Undo",
      tap: shortcut("Ctrl+Z"),
      haptic: "tick",
      hold: shortcut("Ctrl+Y"),
    });
    expect(Object.keys(added)).toEqual(["label", "tap", "haptic", "hold"]);
    // And back, when the hold goes.
    const { hold: _hold, ...rest } = added;
    expect(settleTap(rest)).toEqual(before);
    expect(Object.keys(settleTap(rest))).toEqual(Object.keys(before));
    // A double tap does the same.
    expect(
      settleTap({ press: shortcut("A"), doubleTap: shortcut("B") }),
    ).toEqual({ tap: shortcut("A"), doubleTap: shortcut("B") });
  });

  it("leave a binding that is settled, or has no tap, as it is", () => {
    for (const binding of [
      {},
      { press: shortcut("A") },
      { tap: shortcut("A"), hold: shortcut("B") },
      { hold: shortcut("B"), doubleTap: shortcut("C") },
      // Both gestures of the file: the drawer cannot tell which is the tap.
      { press: shortcut("A"), tap: shortcut("B"), hold: shortcut("C") },
    ])
      expect(settleTap(binding)).toBe(binding);
  });

  it("write the shortcut that moves as a plain one, which a tap can hold", () => {
    // mode "hold" on a tap is an error of the daemon, and a repeat too.
    const moved = settleTap({
      press: {
        type: "keys",
        keys: "Ctrl+Z",
        mode: "hold",
        repeat: { style: "typematic", delayMs: 300 },
      },
      hold: shortcut("B"),
    });
    expect(moved.tap).toEqual(shortcut("Ctrl+Z"));
    expect(moved.press).toBeUndefined();
    // A list of shortcuts keeps the mode "tap" it needs.
    const list: ProfileAction = { type: "keys", keys: ["A", "B"], mode: "tap" };
    expect(settleTap({ press: list, hold: shortcut("C") }).tap).toEqual(list);
    // A script moves as it is.
    const script: ProfileAction = { type: "macro", file: "复制全部.ts" };
    expect(settleTap({ press: script, doubleTap: shortcut("C") }).tap).toEqual(
      script,
    );
  });

  it("keep a press that a tap cannot hold: key hints and precision are held", () => {
    for (const action of [
      { type: "hud", op: "hints" },
      { type: "mouse", op: "precision", factor: 0.3 },
      { type: "layer", layer: "alt", op: "hold" },
    ] as ProfileAction[]) {
      const binding = { press: action, hold: shortcut("B") };
      expect(settleTap(binding)).toBe(binding);
    }
  });

  it("write a shortcut held with the button and without repeat", () => {
    expect(
      holdShortcut({
        type: "keys",
        keys: "Ctrl+Z",
        mode: "tap",
        repeat: { style: "tap", delayMs: 0, rateHz: 12 },
      }),
    ).toEqual(shortcut("Ctrl+Z"));
    expect(
      holdShortcut({
        type: "keys",
        keys: "Space",
        repeat: { style: "typematic", rateHz: 20, minRateHz: 3 },
      }),
    ).toEqual(shortcut("Space"));
    expect(holdShortcut({ type: "keys", keys: "Shift", mode: "hold" })).toEqual(
      shortcut("Shift"),
    );
    // Nothing to take out: the same action comes back.
    const plain = shortcut("F13");
    expect(holdShortcut(plain)).toBe(plain);
  });

  it("leave what is not a single shortcut as it is", () => {
    for (const action of [
      { type: "keys", keys: ["A", "B"], mode: "tap" },
      { type: "sequence", steps: [{ keys: "A" }, { wait: 100 }] },
      { type: "macro", file: "复制全部.ts" },
      { type: "text", text: "hello" },
      { type: "hud", op: "hints" },
    ] as ProfileAction[])
      expect(holdShortcut(action)).toBe(action);
  });
});
