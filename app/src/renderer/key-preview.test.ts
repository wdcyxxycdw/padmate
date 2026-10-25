import { describe, expect, it } from "vitest";
import { controllerLabels } from "./controller-model";
import {
  keyPreviewLine,
  keyPreviewText,
  pressedControlName,
  previewAction,
} from "./key-preview";

const xbox = controllerLabels("xboxone");
const switchPro = controllerLabels("switchpro");

describe("key preview line (UI-44)", () => {
  it("names the control as the pad prints it", () => {
    for (const [control, labels, want] of [
      ["south", xbox, "A"],
      ["south", switchPro, "B"],
      ["leftshoulder", xbox, "LB"],
      ["lt", xbox, "LT"],
      ["rt", switchPro, "ZR"],
      ["dpad.up", xbox, "↑"],
      ["dpad.upleft", xbox, "↖"],
      ["ls.up", xbox, "LS ↑"],
      ["rs.downright", xbox, "RS ↘"],
      ["ls.active", xbox, "LS"],
      ["ls.outer", xbox, "LS 重推"],
      ["rs.inner", xbox, "RS 轻推"],
      ["rs.cw", xbox, "RS 顺时针一格"],
      ["rs.ccw", xbox, "RS 逆时针一格"],
      ["lt.full", xbox, "LT 按到底"],
      ["rt.full", switchPro, "ZR 按到底"],
      ["lefty-", xbox, "LS ↑"],
      ["rightx+", xbox, "RS →"],
      ["lefttrigger+", xbox, "LT 半按"],
      ["paddle1", xbox, "背键 1"],
      ["paddle3", xbox, "背键 2"],
      ["misc1", xbox, "Share"],
      ["paddle2", controllerLabels("joyconleft", {}, 2), "SL"],
      ["bogus", xbox, "bogus"],
    ] as const)
      expect(pressedControlName(control, labels, "zh-CN"), control).toBe(want);
    expect(pressedControlName("righttrigger+", xbox, "en")).toBe(
      "RT Half press",
    );
    expect(pressedControlName("ls.ccw", xbox, "en")).toBe(
      "LS counter-clockwise tick",
    );
    expect(pressedControlName("rt.full", xbox, "en")).toBe("RT full pull");
    // An inherited name is not a label.
    expect(pressedControlName("constructor", xbox, "zh-CN")).toBe(
      "constructor",
    );
  });

  it("shows the shortcut of a keys action", () => {
    expect(previewAction("tap Ctrl+Shift+Z")).toBe("Ctrl+Shift+Z");
    expect(previewAction("hold Shift")).toBe("Shift");
    expect(previewAction("tap LCtrl+Up")).toBe("Ctrl+↑");
    expect(previewAction("tap [Ctrl+K,S]")).toBe("Ctrl+K, S");
    // "," is a key too.
    expect(previewAction("tap [Ctrl+,,B]")).toBe("Ctrl+,, B");
    expect(previewAction("tap [,,B]")).toBe(",, B");
    expect(previewAction("tap ,")).toBe(",");
  });

  it("shows the wheel keys of a keys action by language (ADR-0062)", () => {
    expect(previewAction("tap WheelUp")).toBe("滚轮↑");
    expect(previewAction("tap Ctrl+WheelDown")).toBe("Ctrl+滚轮↓");
    expect(previewAction("tap Ctrl+WheelDown", [], "en")).toBe("Ctrl+Wheel↓");
    expect(previewAction("hold LShift+WheelLeft", [], "zh-CN")).toBe(
      "Shift+滚轮←",
    );
    expect(previewAction("tap [Ctrl+WheelUp,WheelRight]", [], "en")).toBe(
      "Ctrl+Wheel↑, Wheel→",
    );
    const preview = { control: "ls.cw", action: "tap WheelUp" };
    expect(keyPreviewText(preview, xbox, undefined, "zh-CN")).toBe(
      "LS 顺时针一格 → 滚轮↑",
    );
    expect(keyPreviewText(preview, xbox, undefined, "en")).toBe(
      "LS clockwise tick → Wheel↑",
    );
  });

  it("shows the mouse buttons of a keys action by language (ADR-0062)", () => {
    expect(previewAction("hold MouseLeft")).toBe("左键");
    expect(previewAction("tap Ctrl+MouseRight")).toBe("Ctrl+右键");
    expect(previewAction("tap Ctrl+MouseRight", [], "en")).toBe("Ctrl+RMB");
    expect(previewAction("hold LShift+XButton1", [], "zh-CN")).toBe(
      "Shift+侧键1",
    );
    expect(previewAction("tap [MouseMiddle,Alt+MouseX2]", [], "en")).toBe(
      "MMB, Alt+X2",
    );
    const preview = { control: "south", action: "hold MouseLeft" };
    expect(keyPreviewText(preview, xbox, undefined, "zh-CN")).toBe("A → 左键");
    expect(keyPreviewText(preview, xbox, undefined, "en")).toBe("A → LMB");
  });

  it("shows the script a macro action runs, without the extension (SCR-50)", () => {
    expect(previewAction("macro 复制全部.ts")).toBe("复制全部");
    expect(previewAction("macro 按住 Shift.js")).toBe("按住 Shift");
    // Script actions of the legacy script do not name macros.
    expect(previewAction("macro 复制全部.ts", [{ name: "macro" }])).toBe(
      "复制全部",
    );
    expect(
      keyPreviewLine(
        { control: "south", action: "macro 复制全部.ts" },
        xbox,
        [],
        "zh-CN",
      ),
    ).toEqual({ control: "A", sends: "复制全部", macro: true });
    expect(
      keyPreviewText(
        { control: "leftshoulder", action: "macro 复制全部.ts" },
        xbox,
        [],
        "en",
      ),
    ).toBe("LB → 复制全部");
    expect(
      keyPreviewLine({ control: "south", action: "tap Z" }, xbox, [], "zh-CN")
        ?.macro,
    ).toBe(false);
  });

  it("shows a script action by its label, else its name", () => {
    const actions = [
      { name: "cycleBrush", label: "切换笔刷" },
      { name: "plain" },
    ];
    expect(previewAction("script cycleBrush", actions)).toBe("切换笔刷");
    expect(previewAction("script plain", actions)).toBe("plain");
    expect(previewAction("script missing")).toBe("missing");
  });

  it("shows a precision action with its share of the speed (MOU-05)", () => {
    expect(previewAction("mouse precision 0.3")).toBe("精确模式 30%");
    expect(previewAction("mouse precision 0.05", [], "zh-CN")).toBe(
      "精确模式 5%",
    );
    expect(previewAction("mouse precision 1", [], "en")).toBe("Precision 100%");
    // No factor named: the daemon's default.
    expect(previewAction("mouse precision")).toBe("精确模式 30%");
    expect(
      keyPreviewText(
        { control: "leftshoulder", action: "mouse precision 0.5" },
        xbox,
        [],
        "zh-CN",
      ),
    ).toBe("LB → 精确模式 50%");
    expect(
      keyPreviewLine(
        { control: "leftshoulder", action: "mouse precision 0.5" },
        xbox,
        [],
        "en",
      ),
    ).toEqual({ control: "LB", sends: "Precision 50%", macro: false });
  });

  it("shows the key hints action by name (HUD-03)", () => {
    expect(previewAction("hud hints")).toBe("按键提示");
    expect(previewAction("hud hints", [], "en")).toBe("Key hints");
    expect(
      keyPreviewText(
        { control: "rightshoulder", action: "hud hints" },
        xbox,
        [],
        "zh-CN",
      ),
    ).toBe("RB → 按键提示");
    // Not an action of another kind that merely starts with hud.
    expect(previewAction("hud")).toBeNull();
    expect(previewAction("hud other")).toBeNull();
  });

  it("shows the volume and media keys by name (OUT-20)", () => {
    expect(previewAction("tap VolumeUp")).toBe("音量加");
    expect(previewAction("tap Ctrl+MediaNext")).toBe("Ctrl+下一首");
    expect(previewAction("tap MediaPlayPause", [], "en")).toBe("Play/Pause");
    expect(previewAction("hold VolumeMute", [], "en")).toBe("Mute");
  });

  it("shows the text a text action types (OUT-21)", () => {
    expect(previewAction('text "你好"')).toBe("输入文字「你好」");
    expect(previewAction('text "你好"', [], "en")).toBe("Type “你好”");
    // The decision quotes the text as Go does: a newline is \n on the line.
    expect(previewAction('text "ab\ncd"')).toBe("输入文字「ab cd」");
    expect(previewAction('text "say \\"hi\\""', [], "en")).toBe(
      'Type “say "hi"”',
    );
    // A text the decision cut ends in an ellipsis inside the quotes.
    expect(previewAction('text "Best regards,…"', [], "en")).toBe(
      "Type “Best regards,…”",
    );
    // A quote that Go writes in a way JSON does not still shows the text.
    expect(previewAction('text "a\x00b"', [], "en")).toBe("Type “a\x00b”");
    expect(previewAction("text")).toBeNull();
    expect(previewAction("text abc")).toBeNull();
  });

  it("shows the steps of a sequence action (MAP-22)", () => {
    expect(
      previewAction('sequence: Ctrl+A, wait 100, text "你好", Enter'),
    ).toBe("Ctrl+A, 100 ms, “你好”, Enter");
    expect(
      previewAction('sequence: LCtrl+Up, wait 5, text "a\tb"', [], "en"),
    ).toBe("Ctrl+↑, 5 ms, “a b”");
    // "," is a key too, and a text may hold the separator.
    expect(previewAction("sequence: A, ,, wait 5")).toBe("A, ,, 5 ms");
    expect(previewAction('sequence: text "x, wait 5, y"')).toBe(
      "“x, wait 5, y”",
    );
    expect(previewAction("sequence: VolumeMute, wait 30", [], "en")).toBe(
      "Mute, 30 ms",
    );
    expect(previewAction("sequence: Ctrl+K")).toBe("Ctrl+K");
    expect(previewAction("sequence:")).toBeNull();
    expect(previewAction("sequence: ")).toBeNull();
    expect(previewAction("sequences: A")).toBeNull();
  });

  it("joins a text or a sequence with the control it came from", () => {
    expect(
      keyPreviewText(
        { control: "south", action: 'sequence: Ctrl+A, text "x"' },
        xbox,
        [],
        "zh-CN",
      ),
    ).toBe("A → Ctrl+A, “x”");
    expect(
      keyPreviewText({ control: "east", action: 'text "ab"' }, xbox, [], "en"),
    ).toBe("B → Type “ab”");
  });

  it("shows nothing for a pointer jump", () => {
    expect(previewAction("mouse jump 0.5,0.5")).toBeNull();
    expect(previewAction("mouse")).toBeNull();
  });

  it("shows nothing for layer and profile actions", () => {
    expect(previewAction("layer push brush")).toBeNull();
    expect(previewAction("profile photo")).toBeNull();
    expect(previewAction("keys")).toBeNull();
  });

  it("joins the two with an arrow", () => {
    expect(
      keyPreviewText(
        { control: "south", action: "tap Ctrl+Shift+Z" },
        xbox,
        [],
        "zh-CN",
      ),
    ).toBe("A → Ctrl+Shift+Z");
    expect(
      keyPreviewText(
        { control: "north", action: "script cycleBrush" },
        xbox,
        [{ name: "cycleBrush", label: "切换笔刷" }],
        "zh-CN",
      ),
    ).toBe("Y → 切换笔刷");
    expect(
      keyPreviewText(
        { control: "north", action: "layer toggle x" },
        xbox,
        [],
        "zh-CN",
      ),
    ).toBeNull();
  });
});
