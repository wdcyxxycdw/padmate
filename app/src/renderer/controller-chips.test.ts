import { describe, expect, it } from "vitest";
import type { ScriptRegistration } from "../shared/api";
import type { Profile } from "../shared/generated/schema";
import {
  type ChipInfo,
  type ChipSegment,
  controllerChips,
  inheritedChips,
  keyText,
  segmentText,
} from "./controller-chips";
import { controllerLabels } from "./controller-model";
import type { FunctionKey } from "./editor-model";
import type { LabelDisplay, Language } from "./store";

const labels = controllerLabels("xboxone");
const controls = Object.keys(labels);

const paint: Profile = {
  schemaVersion: 1,
  name: "绘画",
  controlNames: { leftshoulder: "修饰键" },
  layers: {
    base: {
      south: {
        label: "撤销",
        press: { type: "keys", keys: "Ctrl+Z", mode: "tap" },
      },
      east: {
        label: "抓手",
        press: { type: "keys", keys: "Space", mode: "hold" },
      },
      north: {
        label: "画笔",
        tap: { type: "keys", keys: "B" },
        hold: { type: "keys", keys: "Shift+B" },
      },
      west: { press: { type: "keys", keys: "E", mode: "tap" } },
      leftshoulder: { press: { type: "keys", keys: "LCtrl" } },
      paddle1: { doubleTap: { type: "keys", keys: "Ctrl+S" }, label: "保存" },
      "dpad.left": {
        label: "上一个笔刷",
        press: { type: "script", name: "cycleBrush", args: { dir: -1 } },
      },
      "dpad.right": {
        press: { type: "script", name: "cycleBrush", args: { dir: 1 } },
      },
      misc1: { press: { type: "profile", profile: "photo" } },
      "ls.up": { press: { type: "keys", keys: "Up" } },
      "ls.down": { press: { type: "keys", keys: "Down" } },
      "ls.left": { press: { type: "keys", keys: "Left" } },
      "ls.right": { press: { type: "keys", keys: "Right" } },
      leftstick: { label: "平移画布" },
    },
  },
};

function registration(
  control: string,
  scope = "base",
  extra: Partial<ScriptRegistration> = {},
): ScriptRegistration {
  return {
    id: 1,
    control,
    gesture: "press",
    scope,
    observe: false,
    claim: true,
    priority: 0,
    timing: {},
    ...extra,
  };
}

function chips({
  profile = paint,
  registrations = [],
  display = "actions",
  language = "zh-CN",
  scriptEntry,
  functionKey = "start",
}: {
  profile?: Profile | null;
  registrations?: ScriptRegistration[];
  display?: LabelDisplay;
  language?: Language;
  scriptEntry?: string;
  functionKey?: FunctionKey;
} = {}): Record<string, ChipInfo> {
  return controllerChips({
    profile,
    controls,
    labels,
    registrations,
    display,
    language,
    actions: [{ name: "cycleBrush", label: "循环笔刷" }],
    scriptEntry,
    functionKey,
  });
}

describe("controller chips", () => {
  it("shows the label with its keys, gestures and held keys in action mode", () => {
    const result = chips();
    expect(result.south).toMatchObject({
      title: "撤销",
      detail: [{ keys: "Ctrl+Z" }],
      summary: "Ctrl+Z",
      state: "bound",
      scriptOwned: false,
    });
    expect(result.east.detail).toEqual([{ keys: "Space", hold: true }]);
    expect(result.east.summary).toBe("Space 按住");
    expect(result.north.detail).toEqual([
      { keys: "B" },
      { gesture: "长按", keys: "Shift+B" },
    ]);
    expect(result.north.summary).toBe("B · 长按 Shift+B");
    // A gesture other than press or tap is named even when it comes first.
    expect(result.paddle1).toMatchObject({
      title: "保存",
      detail: [{ gesture: "双击", keys: "Ctrl+S" }],
    });
  });

  it("titles unlabeled bindings with the key name, the shortcut or the action", () => {
    const result = chips();
    // A renamed control: its name, then what it does.
    expect(result.leftshoulder).toMatchObject({
      title: "修饰键",
      detail: [{ keys: "LCtrl", hold: true }],
      summary: "Ctrl 按住",
    });
    // A plain shortcut is the title itself, shown as keycaps.
    expect(result.west).toMatchObject({
      title: "E",
      titleKeys: { keys: "E" },
      detail: [],
    });
    expect(result["dpad.left"]).toMatchObject({
      title: "上一个笔刷",
      detail: [{ script: true, text: "循环笔刷" }],
      summary: "脚本 循环笔刷",
    });
    // The action label is the title; the second line does not repeat it.
    expect(result["dpad.right"]).toMatchObject({
      title: "循环笔刷",
      detail: [{ script: true }],
    });
    // A profile switch from the file is described but only editable there.
    expect(result.misc1.title).toBe("切换到「photo」");
    expect(result.misc1.detail).toEqual([]);
    expect(result.misc1.state).toBe("locked");
  });

  it("shows stick directions as separate keycaps under the stick label", () => {
    const result = chips();
    expect(result.leftstick).toMatchObject({
      title: "平移画布",
      detail: [{ chords: ["Up", "Down", "Left", "Right"] }],
      summary: "↑ ↓ ← →",
      state: "bound",
    });
    const unlabeled = structuredClone(paint);
    delete unlabeled.layers.base.leftstick;
    unlabeled.layers.base.l3 = { press: { type: "keys", keys: "Tab" } };
    expect(chips({ profile: unlabeled }).leftstick).toMatchObject({
      title: "方向键",
      detail: [
        { chords: ["Up", "Down", "Left", "Right"] },
        { gesture: "按下摇杆", keys: "Tab" },
      ],
    });
    expect(chips({ language: "en", profile: unlabeled }).leftstick.title).toBe(
      "Direction keys",
    );
  });

  it("marks unset, blocked, layer-action and profile-switch controls", () => {
    const result = chips();
    expect(result.guide).toMatchObject({
      title: "未设置",
      detail: [],
      state: "unbound",
    });
    expect(chips({ language: "en" }).guide.title).toBe("Not set");
    const file = structuredClone(paint);
    file.layers.base.guide = { block: true };
    file.layers.base.rightshoulder = {
      label: "工具组",
      press: { type: "layer", op: "hold", layer: "tools" },
    };
    file.layers.base.rightstick = { label: "备用" };
    file.layers.base.paddle1 = {
      tap: { type: "keys", keys: "Ctrl+S" },
      hold: { type: "profile", profile: "photo" },
    };
    file.layers.base["ls.left"] = {
      press: { type: "profile", profile: "photo" },
    };
    const locked = chips({ profile: file });
    expect(locked.guide).toMatchObject({ title: "已屏蔽", state: "locked" });
    expect(locked.rightshoulder).toMatchObject({
      title: "工具组",
      state: "locked",
    });
    expect(locked.rightshoulder.summary).not.toMatch(/层/);
    // Any gesture switching profiles locks the chip, also on a stick direction.
    expect(locked.paddle1).toMatchObject({
      detail: [{ gesture: "长按", text: "切换到「photo」" }],
      state: "locked",
    });
    expect(locked.leftstick.state).toBe("locked");
    // With the library, the target has the display name the drawer shows.
    const named = controllerChips({
      functionKey: "start",
      profile: file,
      controls,
      labels,
      registrations: [],
      display: "actions",
      language: "zh-CN",
      profiles: [{ id: "photo", name: "Photo" }],
    });
    expect(named.paddle1.detail).toEqual([
      { gesture: "长按", text: "切换到「Photo」" },
    ]);
    expect(named.misc1.title).toBe("切换到「Photo」");
    // A label alone does nothing yet.
    expect(locked.rightstick).toMatchObject({
      title: "备用",
      detail: [{ text: "未设置" }],
      state: "unbound",
    });
  });

  it("does not show an empty shortcut as a held modifier (H1)", () => {
    // A drawer cleared its shortcut: an empty chord is no modifier, so the
    // chip must not claim the keys stay down.
    const file = structuredClone(paint);
    file.layers.base.west = {
      label: "擦除",
      press: { type: "keys", keys: "" },
    };
    file.layers.base.north = { hold: { type: "keys", keys: "  " } };
    file.layers.base.east = {
      press: { type: "keys", keys: "", mode: "hold" },
    };
    for (const display of ["actions", "keys"] as const) {
      const result = chips({ profile: file, display });
      for (const control of ["west", "north", "east"])
        expect(
          [result[control].detail, result[control].titleKeys]
            .flat()
            .some((segment) => segment?.hold),
        ).toBe(false);
      expect(result.west.summary).not.toContain("按住");
      expect(result.north.summary).not.toContain("按住");
      expect(result.east.summary).not.toContain("按住");
    }
    // A real modifier still holds.
    expect(chips().leftshoulder.detail).toEqual([
      { keys: "LCtrl", hold: true },
    ]);
  });

  it("shows a script a key runs, and its missing file as a warning", () => {
    const file = structuredClone(paint);
    file.layers.base.rightshoulder = {
      tap: { type: "macro", file: "复制全部.ts" },
    };
    file.layers.base.leftshoulder = {
      press: { type: "keys", keys: "LCtrl" },
      hold: { type: "macro", file: "旧.js" },
    };
    const result = controllerChips({
      functionKey: "start",
      profile: file,
      controls,
      labels,
      registrations: [],
      display: "actions",
      language: "zh-CN",
      missing: ["旧.js"],
    });
    // A tap or press needs no gesture word, as for shortcuts.
    expect(result.rightshoulder).toMatchObject({
      title: "复制全部",
      detail: [{ macro: true }],
      summary: "脚本",
      state: "bound",
    });
    expect(result.rightshoulder.missing).toBeUndefined();
    expect(result.leftshoulder.detail).toContainEqual({
      gesture: "长按",
      macro: true,
      missing: true,
      text: "找不到「旧」",
    });
    expect(result.leftshoulder.missing).toBe(true);
    // A legacy script's actions are kept but only editable in the file.
    expect(chips()["dpad.left"].state).toBe("locked");
  });

  it("shows a script on a stick direction, and its missing file as a warning", () => {
    const file: Profile = {
      schemaVersion: 1,
      name: "sticks",
      layers: {
        base: {
          "ls.up": { press: { type: "macro", file: "复制全部.ts" } },
          "rs.left": { press: { type: "macro", file: "旧.js" } },
        },
      },
    };
    const result = controllerChips({
      functionKey: "start",
      profile: file,
      controls,
      labels,
      registrations: [],
      display: "actions",
      language: "zh-CN",
      missing: ["旧.js"],
    });
    expect(result.leftstick.detail).toContainEqual({
      macro: true,
      text: "复制全部",
    });
    expect(result.leftstick.missing).toBeUndefined();
    expect(result.rightstick.detail).toContainEqual({
      macro: true,
      missing: true,
      text: "找不到「旧」",
    });
    expect(result.rightstick.missing).toBe(true);
  });

  it("describes a trigger's half press and a stick's raw axes when nothing else is set", () => {
    const raw: Profile = {
      schemaVersion: 1,
      name: "raw",
      layers: {
        base: {
          "lefttrigger+": { press: { type: "keys", keys: "B" } },
          "rightx+": { press: { type: "keys", keys: "]" } },
          "rightx-": { press: { type: "keys", keys: "[" } },
        },
      },
    };
    const result = chips({ profile: raw });
    expect(result.lefttrigger).toMatchObject({
      title: "B",
      titleKeys: { keys: "B" },
      detail: [{ gesture: "半按" }],
      state: "bound",
    });
    // The first bound raw axis leads; the others follow it.
    expect(result.rightstick).toMatchObject({
      title: "[",
      titleKeys: { keys: "[" },
      detail: [{ gesture: "原始轴向左" }, { gesture: "原始轴向右", keys: "]" }],
      state: "bound",
    });
    expect(result.rightstick.summary).toBe("原始轴向左 · 原始轴向右 ]");
    expect(chips({ profile: raw, language: "en" }).lefttrigger.summary).toBe(
      "Half press",
    );
    // The main binding still comes first when both are set.
    const both = structuredClone(raw);
    both.layers.base.lt = { press: { type: "keys", keys: "Alt" } };
    expect(chips({ profile: both }).lefttrigger.titleKeys).toEqual({
      keys: "Alt",
    });
    expect(result.leftstick.state).toBe("unbound");
  });

  it("names a stick whose only direction keys are diagonals", () => {
    const diagonal: Profile = {
      schemaVersion: 1,
      name: "diagonal",
      layers: {
        base: { "ls.upleft": { press: { type: "keys", keys: "Q" } } },
      },
    };
    expect(chips({ profile: diagonal }).leftstick).toMatchObject({
      title: "方向键",
      detail: [{ keys: "Q" }],
      state: "bound",
    });
  });

  it("switches the title between actions, shortcuts and control names", () => {
    const keys = chips({ display: "keys" });
    expect(keys.south).toMatchObject({
      title: "Ctrl+Z",
      titleKeys: { keys: "Ctrl+Z" },
      detail: [{ text: "撤销" }],
    });
    expect(keys.east.detail).toEqual([{ hold: true }, { text: "抓手" }]);
    expect(keys.north.detail).toEqual([
      { text: "画笔" },
      { gesture: "长按", keys: "Shift+B" },
    ]);
    expect(keys.leftstick).toMatchObject({
      title: "↑ ↓ ← →",
      titleKeys: { chords: ["Up", "Down", "Left", "Right"] },
      detail: [{ text: "平移画布" }],
    });
    expect(keys.guide.title).toBe("未设置");
  });

  it("counts script ownership only from base and global registrations", () => {
    const result = chips({
      registrations: [
        registration("x", "base", { label: "智能橡皮" }),
        registration("y", "global"),
        registration("b", "tools"),
        registration("dpad.up", "base", { observe: true, claim: false }),
        registration("ls.up", "global"),
        registration("righttrigger+"),
      ],
      scriptEntry: "scripts/main.ts",
    });
    expect(result.west.scriptOwned).toBe(true);
    expect(result.north.scriptOwned).toBe(true);
    expect(result.east.scriptOwned).toBe(false);
    expect(result["dpad.up"].scriptOwned).toBe(false);
    expect(result.leftstick.scriptOwned).toBe(true);
    expect(result.righttrigger.scriptOwned).toBe(true);
    // A control only a script handles names the script.
    const scripted = chips({
      registrations: [registration("guide", "base", { label: "截图" })],
      scriptEntry: "scripts/main.ts",
    });
    expect(scripted.guide).toMatchObject({
      title: "截图",
      detail: [{ text: "由脚本 main.ts 接管" }],
      state: "script",
      scriptOwned: true,
    });
    expect(
      chips({ registrations: [registration("guide")], language: "en" }).guide,
    ).toMatchObject({
      title: "Script",
      summary: "Handled by a script",
    });
  });

  it("shows control names while no profile is loaded", () => {
    const result = chips({ profile: null });
    expect(result.south).toMatchObject({
      title: "A",
      state: "unbound",
    });
  });
});

describe("chip text", () => {
  it("formats shortcuts the way keycaps show them", () => {
    expect(keyText("LCtrl+Shift+Z")).toBe("Ctrl+Shift+Z");
    expect(keyText(["Ctrl+K", "S"])).toBe("Ctrl+K → S");
    // The wheel keys read by language (ADR-0062).
    expect(keyText("Ctrl+WheelUp")).toBe("Ctrl+滚轮↑");
    expect(keyText("Ctrl+WheelUp", "zh-CN")).toBe("Ctrl+滚轮↑");
    expect(keyText(["LCtrl+WheelDown", "WheelLeft"], "en")).toBe(
      "Ctrl+Wheel↓ → Wheel←",
    );
    expect(
      segmentText({ gesture: "Tap", keys: "Shift+WheelRight" }, "en"),
    ).toBe("Tap Shift+Wheel→");
    // So do the mouse buttons, by name and by alias.
    expect(keyText("Shift+MouseLeft")).toBe("Shift+左键");
    expect(keyText("Shift+LButton", "zh-CN")).toBe("Shift+左键");
    expect(keyText("MouseX1", "en")).toBe("X1");
    expect(keyText(["MouseRight", "Alt+XButton2"], "en")).toBe("RMB → Alt+X2");
    expect(keyText(["MouseMiddle", "MouseX2"])).toBe("中键 → 侧键2");
    expect(segmentText({ gesture: "Tap", keys: "Ctrl+MouseRight" }, "en")).toBe(
      "Tap Ctrl+RMB",
    );
    expect(
      segmentText(
        { gesture: "Hold", script: true, text: "Brush", hold: false },
        "en",
      ),
    ).toBe("Hold Script Brush");
  });
});

describe("chips of wheel keys (ADR-0062)", () => {
  const wheel: Profile = {
    schemaVersion: 1,
    name: "滚轮",
    layers: {
      base: {
        south: { press: { type: "keys", keys: "Ctrl+WheelUp", mode: "tap" } },
        east: {
          label: "缩小",
          press: { type: "keys", keys: "WheelDown", mode: "tap" },
        },
      },
    },
  };

  it("names a binding by its keycaps, in the language of the app", () => {
    expect(chips({ profile: wheel }).south).toMatchObject({
      title: "Ctrl+滚轮↑",
      titleKeys: { keys: "Ctrl+WheelUp" },
    });
    expect(chips({ profile: wheel, language: "en" }).south.title).toBe(
      "Ctrl+Wheel↑",
    );
  });

  it("shows them in the shortcut display mode", () => {
    const result = chips({ profile: wheel, display: "keys" });
    expect(result.east).toMatchObject({
      title: "滚轮↓",
      detail: [{ text: "缩小" }],
    });
    expect(
      chips({ profile: wheel, display: "keys", language: "en" }).east.title,
    ).toBe("Wheel↓");
  });

  it("does not take a wheel key for a held modifier", () => {
    const held: Profile = {
      ...wheel,
      layers: {
        base: { south: { press: { type: "keys", keys: "Ctrl+WheelUp" } } },
      },
    };
    expect(chips({ profile: held }).south.detail).toEqual([]);
  });
});

describe("chips of mouse buttons (ADR-0062)", () => {
  const buttons: Profile = {
    schemaVersion: 1,
    name: "点击",
    layers: {
      base: {
        south: { press: { type: "keys", keys: "MouseLeft" } },
        east: { press: { type: "keys", keys: "Ctrl+RButton", mode: "tap" } },
        north: {
          label: "中击",
          press: { type: "keys", keys: "MouseMiddle", mode: "tap" },
        },
        west: { press: { type: "keys", keys: "MouseX1", mode: "hold" } },
      },
    },
  };

  it("names a binding by its keycaps, in the language of the app", () => {
    expect(chips({ profile: buttons }).south).toMatchObject({
      title: "左键",
      titleKeys: { keys: "MouseLeft" },
    });
    expect(chips({ profile: buttons }).east.title).toBe("Ctrl+右键");
    expect(chips({ profile: buttons, language: "en" }).south.title).toBe("LMB");
    expect(chips({ profile: buttons, language: "en" }).east.title).toBe(
      "Ctrl+RMB",
    );
  });

  it("shows them in the shortcut display mode", () => {
    const result = chips({ profile: buttons, display: "keys" });
    expect(result.east.title).toBe("Ctrl+右键");
    expect(result.north).toMatchObject({
      title: "中键",
      detail: [{ text: "中击" }],
    });
    expect(
      chips({ profile: buttons, display: "keys", language: "en" }).west.title,
    ).toBe("X1");
  });

  it("does not take a button for a held modifier", () => {
    // A press holds its keys by default, which is only worth saying about
    // modifiers; the button is not one.
    expect(chips({ profile: buttons }).south.detail).toEqual([]);
  });
});

describe("chips for the rings, the knob and the second stage (UI-28)", () => {
  const file = (
    base: Profile["layers"]["base"],
    analog?: Profile["analog"],
  ): Profile => ({
    schemaVersion: 1,
    name: "m15",
    ...(analog ? { analog } : {}),
    layers: { base },
  });
  const key = (keys: string) => ({
    press: { type: "keys" as const, keys, mode: "tap" as const },
  });

  it("summarizes a knob by its two ticks", () => {
    const knob = file(
      { "rs.cw": key("]"), "rs.ccw": key("[") },
      { rs: { mode: "knob" } },
    );
    const result = chips({ profile: knob });
    expect(result.rightstick).toMatchObject({
      title: "旋钮",
      detail: [{ chords: ["]", "["], divider: "/" }],
      summary: "] / [",
      state: "bound",
    });
    expect(chips({ profile: knob, language: "en" }).rightstick.title).toBe(
      "Knob",
    );
    expect(chips({ profile: knob, display: "keys" }).rightstick).toMatchObject({
      title: "] / [",
      titleKeys: { chords: ["]", "["], divider: "/" },
    });
    // Only one tick set: just that one.
    const half = file({ "rs.ccw": key("[") }, { rs: { mode: "knob" } });
    expect(chips({ profile: half }).rightstick.detail).toEqual([
      { chords: ["["], divider: "/" },
    ]);
    // The left stick is not a knob.
    expect(result.leftstick.state).toBe("unbound");
  });

  it("does not show direction keys a knob does not send", () => {
    const knob = file(
      {
        "rs.cw": key("]"),
        "rs.ccw": key("["),
        "rs.up": key("Up"),
        rightstick: { label: "笔刷大小", ...key("Tab") },
      },
      { rs: { mode: "knob" } },
    );
    const result = chips({ profile: knob });
    expect(result.rightstick.detail).toEqual([
      { chords: ["]", "["], divider: "/" },
      { keys: "Tab", gesture: "按下摇杆" },
    ]);
    // Without the knob mode, the same file is a stick with directions, and
    // the ticks it holds are not shown.
    const plain = structuredClone(knob);
    plain.analog = {};
    expect(chips({ profile: plain }).rightstick.detail[0]).toEqual({
      chords: ["Up"],
    });
  });

  it("names a macro tick as a macro", () => {
    const knob = file(
      { "rs.cw": { press: { type: "macro", file: "放大.ts" } } },
      { rs: { mode: "knob" } },
    );
    const result = chips({ profile: knob }).rightstick;
    expect(result.title).toBe("旋钮");
    expect(result.detail[0]).toMatchObject({ macro: true });
  });

  it("lists the rings after the directions and the click", () => {
    const rings = file({
      "ls.up": key("Up"),
      "ls.outer": { press: { type: "keys", keys: "Shift", mode: "hold" } },
      "ls.inner": key("F2"),
    });
    const result = chips({ profile: rings }).leftstick;
    expect(result.detail).toEqual([
      { chords: ["Up"] },
      { gesture: "轻推", keys: "F2" },
      { gesture: "重推", keys: "Shift", hold: true },
    ]);
    expect(result.summary).toBe("↑ · 轻推 F2 · 重推 Shift 按住");
  });

  it("shows the rings of a stick that has nothing else set", () => {
    const rings = file({ "rs.outer": key("Shift") });
    expect(chips({ profile: rings }).rightstick).toMatchObject({
      title: "Shift",
      titleKeys: { keys: "Shift" },
      detail: [{ gesture: "重推" }],
      state: "bound",
    });
  });

  it("shows the second stage after the trigger's own keys", () => {
    const stages = file({ lt: key("B"), "lt.full": key("E") });
    expect(chips({ profile: stages }).lefttrigger).toMatchObject({
      title: "B",
      detail: [{ gesture: "按到底", keys: "E" }],
      summary: "按到底 E",
      state: "bound",
    });
    // Alone, the second stage leads like any other key.
    const only = file({ "rt.full": key("E") });
    expect(chips({ profile: only }).righttrigger).toMatchObject({
      title: "E",
      detail: [{ gesture: "按到底" }],
      state: "bound",
    });
    expect(chips({ profile: only }).lefttrigger.state).toBe("unbound");
  });

  it("counts the new keys for scripts and for what only the file can edit", () => {
    const registrations = [
      registration("rs.cw"),
      registration("lt.full"),
      registration("ls.inner"),
    ];
    const result = chips({ profile: file({}), registrations });
    expect(result.rightstick.scriptOwned).toBe(true);
    expect(result.lefttrigger.scriptOwned).toBe(true);
    expect(result.leftstick.scriptOwned).toBe(true);
    expect(result.righttrigger.scriptOwned).toBe(false);
    const locked = file({
      "lt.full": { press: { type: "profile", profile: "x" } },
    });
    expect(chips({ profile: locked }).lefttrigger.state).toBe("locked");
    const blocked = file({ "ls.outer": { block: true } });
    expect(chips({ profile: blocked }).leftstick.state).toBe("locked");
  });
});

describe("chips for a stick that moves the pointer (MOU-01, UI-33)", () => {
  const file = (
    base: Profile["layers"]["base"],
    analog: Profile["analog"] = { ls: { mode: "mouse" } },
  ): Profile => ({
    schemaVersion: 1,
    name: "pointer",
    analog,
    layers: { base },
  });
  const key = (keys: string) => ({
    press: { type: "keys" as const, keys, mode: "tap" as const },
  });

  it("is named 鼠标 on the callout, and Mouse in English", () => {
    const result = chips({ profile: file({}) });
    expect(result.leftstick).toMatchObject({
      title: "鼠标",
      detail: [],
      summary: "",
      state: "bound",
      scriptOwned: false,
    });
    expect(chips({ profile: file({}), language: "en" }).leftstick.title).toBe(
      "Mouse",
    );
    // The other stick is a stick like any other.
    expect(result.rightstick.state).toBe("unbound");
    expect(result.rightstick.title).toBe("未设置");
  });

  it("follows the stick that has the mode", () => {
    const right = file({}, { rs: { mode: "mouse" } });
    const result = chips({ profile: right });
    expect(result.rightstick.title).toBe("鼠标");
    expect(result.leftstick.state).toBe("unbound");
  });

  it("is the same in the shortcut display mode, without keycaps", () => {
    const result = chips({ profile: file({}), display: "keys" });
    expect(result.leftstick).toMatchObject({
      title: "鼠标",
      detail: [],
      state: "bound",
    });
    expect(result.leftstick.titleKeys).toBeUndefined();
  });

  it("does not show direction keys or knob ticks the pointer does not send", () => {
    const result = chips({
      profile: file({
        "ls.up": key("Up"),
        "ls.right": key("Right"),
        "ls.cw": key("]"),
        "ls.ccw": key("["),
      }),
    });
    expect(result.leftstick).toMatchObject({ title: "鼠标", detail: [] });
    // Without the mode, the same keys are the directions.
    const plain = file({ "ls.up": key("Up"), "ls.right": key("Right") }, {});
    expect(chips({ profile: plain }).leftstick.title).toBe("方向键");
  });

  it("lists the stick click and the rings after it", () => {
    const result = chips({
      profile: file({
        leftstick: key("Tab"),
        "ls.inner": key("F2"),
        "ls.outer": { press: { type: "keys", keys: "Shift", mode: "hold" } },
      }),
    });
    expect(result.leftstick.title).toBe("鼠标");
    expect(result.leftstick.detail).toEqual([
      { keys: "Tab", gesture: "按下摇杆" },
      { gesture: "轻推", keys: "F2" },
      { gesture: "重推", keys: "Shift", hold: true },
    ]);
  });

  it("keeps the label of the stick when it has one", () => {
    const result = chips({
      profile: file({ leftstick: { label: "移动鼠标" } }),
    });
    expect(result.leftstick).toMatchObject({
      title: "移动鼠标",
      detail: [{ text: "鼠标" }],
      state: "bound",
    });
  });

  it("counts a script on the stick and what only the file can edit", () => {
    expect(
      chips({
        profile: file({}),
        registrations: [registration("ls.active")],
      }).leftstick.scriptOwned,
    ).toBe(true);
    expect(
      chips({ profile: file({ "ls.outer": { block: true } }) }).leftstick.state,
    ).toBe("locked");
  });
});

describe("chips for the mouse actions (MOU-05, MOU-08)", () => {
  const file = (base: Profile["layers"]["base"]): Profile => ({
    schemaVersion: 1,
    name: "pointer",
    layers: { base },
  });

  it("names a precision action with its share of the speed", () => {
    const result = chips({
      profile: file({
        leftshoulder: {
          press: { type: "mouse", op: "precision", factor: 0.5 },
        },
        rightshoulder: { hold: { type: "mouse", op: "precision" } },
      }),
    });
    expect(result.leftshoulder).toMatchObject({
      title: "精确模式 50%",
      detail: [],
      summary: "",
      state: "bound",
    });
    // A hold says it is a hold.
    expect(result.rightshoulder).toMatchObject({
      title: "精确模式 30%",
      detail: [{ gesture: "长按" }],
      summary: "长按",
      state: "bound",
    });
    expect(
      chips({
        profile: file({
          leftshoulder: { press: { type: "mouse", op: "precision" } },
        }),
        language: "en",
      }).leftshoulder.title,
    ).toBe("Precision 30%");
  });

  it("names the key hints action, as a press or a hold", () => {
    const result = chips({
      profile: file({
        leftshoulder: { press: { type: "hud", op: "hints" } },
        rightshoulder: { hold: { type: "hud", op: "hints" } },
      }),
    });
    expect(result.leftshoulder).toMatchObject({
      title: "按键提示",
      detail: [],
      summary: "",
      state: "bound",
    });
    expect(result.rightshoulder).toMatchObject({
      title: "按键提示",
      detail: [{ gesture: "长按" }],
      state: "bound",
    });
    expect(
      chips({
        profile: file({
          leftshoulder: { press: { type: "hud", op: "hints" } },
        }),
        language: "en",
      }).leftshoulder.title,
    ).toBe("Key hints");
  });

  it("keeps a label and shows the action under it", () => {
    const result = chips({
      profile: file({
        leftshoulder: {
          label: "慢速",
          press: { type: "mouse", op: "precision", factor: 0.2 },
        },
      }),
    });
    expect(result.leftshoulder).toMatchObject({
      title: "慢速",
      detail: [{ text: "精确模式 20%" }],
      state: "bound",
    });
  });

  it("locks a pointer jump, which only the file sets", () => {
    const result = chips({
      profile: file({
        leftshoulder: {
          press: { type: "mouse", op: "jump", x: 0.5, y: 0.5 },
        },
      }),
    });
    expect(result.leftshoulder).toMatchObject({
      title: "指针跳到屏幕位置 50%，50%",
      state: "locked",
    });
  });
});

describe("the chip of the function key (ADR-0075)", () => {
  const file: Profile = {
    ...paint,
    layers: {
      base: {
        ...paint.layers.base,
        back: { press: { type: "keys", keys: "Ctrl+V" } },
        start: { label: "保存", press: { type: "keys", keys: "Ctrl+S" } },
        guide: { press: { type: "keys", keys: "F13" } },
      },
    },
  };
  const tag = (language: Language): ChipInfo => ({
    title: language === "en" ? "Function key" : "功能键",
    detail: [],
    summary:
      language === "en"
        ? "While held, other keys switch to another set"
        : "按住时其他键换成另一套",
    state: "function",
    scriptOwned: false,
  });

  it("tags the function key whatever the file binds on it, and only that key", () => {
    for (const language of ["zh-CN", "en"] as const)
      for (const functionKey of ["start", "back", "guide"] as const) {
        const result = chips({ profile: file, language, functionKey });
        expect(result[functionKey], `${language} ${functionKey}`).toEqual(
          tag(language),
        );
        for (const other of ["start", "back", "guide"] as const)
          if (other !== functionKey)
            expect(result[other].state, `${functionKey} ${other}`).toBe(
              "bound",
            );
      }
  });

  it("shows the other two of View, Menu and the Xbox button as ordinary keys", () => {
    const result = chips({ profile: file, functionKey: "guide" });
    expect(result.start).toMatchObject({
      title: "保存",
      detail: [{ keys: "Ctrl+S" }],
      state: "bound",
    });
    expect(result.back).toMatchObject({
      title: "Ctrl+V",
      titleKeys: { keys: "Ctrl+V" },
      state: "bound",
    });
    // A key nobody bound is unset, like any other.
    expect(chips({ functionKey: "guide" }).start.state).toBe("unbound");
  });

  it("is the same in every display mode, and when no profile is open", () => {
    for (const display of ["actions", "keys"] as const)
      expect(chips({ display }).start.state).toBe("function");
    expect(chips({ profile: null }).start.state).toBe("function");
    expect(chips({ profile: null, functionKey: "back" }).back.state).toBe(
      "function",
    );
  });

  it("does not mark a script that handles the function key as owning the chip", () => {
    // A script's handler on it never fires (SCRIPTING §1.5).
    expect(
      chips({ registrations: [registration("start", "global")] }).start,
    ).toMatchObject({ state: "function", scriptOwned: false });
  });
});

describe("a control the function set leaves out", () => {
  const file = (base: Profile["layers"]["base"]): Profile => ({
    schemaVersion: 1,
    name: "set",
    layers: { base },
  });
  const key = (keys: string) => ({
    press: { type: "keys" as const, keys, mode: "tap" as const },
  });
  const set: Profile = {
    ...paint,
    layers: {
      base: {
        east: { press: { type: "keys", keys: "Ctrl+Shift+Z", mode: "tap" } },
      },
    },
  };

  it("shows what it does otherwise, greyed, under a note", () => {
    const own = chips({ profile: set });
    const normal = chips();
    const shown = inheritedChips({
      own,
      normal,
      language: "zh-CN",
    });
    // Set in the set: as it is.
    expect(shown.east).toBe(own.east);
    // Not set there, set otherwise: the usual chip, in the unset look with the note.
    expect(shown.south).toMatchObject({
      title: "平时：撤销",
      detail: [{ keys: "Ctrl+Z" }],
      state: "unbound",
      inherited: true,
    });
    expect(shown.north.title).toBe("平时：画笔");
    expect(shown.north.detail).toEqual(normal.north.detail);
    // Set nowhere: unset, as it was.
    expect(shown.rightshoulder).toBe(own.rightshoulder);
    expect(shown.rightshoulder.state).toBe("unbound");
    // The function key is tagged in every view.
    expect(shown.start).toBe(own.start);
  });

  it("puts the note before the keycaps of a shortcut that is the title", () => {
    const normal = chips({ profile: file({ south: key("Ctrl+Z") }) });
    expect(normal.south.titleKeys).toBeDefined();
    const shown = inheritedChips({
      own: chips({ profile: file({}) }),
      normal,
      language: "en",
    });
    expect(shown.south).toMatchObject({
      title: "Normal: Ctrl+Z",
      titleKeys: { keys: "Ctrl+Z", gesture: "Normal:" },
      inherited: true,
      state: "unbound",
    });
    expect(segmentText(shown.south.titleKeys as ChipSegment, "en")).toBe(
      "Normal: Ctrl+Z",
    );
  });

  it("keeps a control the set only names, which owns it as in a layer", () => {
    const named = file({ west: { label: "备用" } });
    const own = chips({ profile: named });
    expect(own.west.empty).toBeUndefined();
    const shown = inheritedChips({
      own,
      normal: chips(),
      language: "zh-CN",
    });
    expect(shown.west).toBe(own.west);
    expect(shown.west.inherited).toBeUndefined();
    // A control with nothing written is what inherits.
    expect(own.south.empty).toBe(true);
  });

  it("keeps a chip a script owns here, and one the file locks otherwise greyed", () => {
    const own = chips({
      profile: set,
      registrations: [registration("south", "global")],
    });
    expect(own.south.state).toBe("script");
    const shown = inheritedChips({
      own,
      normal: chips(),
      language: "zh-CN",
    });
    expect(shown.south).toBe(own.south);
    // A profile switch in the usual set: it still applies here.
    const locked = inheritedChips({
      own: chips({ profile: set }),
      normal: chips({
        language: "en",
        profile: file({
          west: { press: { type: "profile", profile: "photo" } },
        }),
      }),
      language: "en",
    });
    expect(locked.west).toMatchObject({
      title: "Normal: Switch to photo",
      state: "unbound",
      inherited: true,
    });
  });
});
