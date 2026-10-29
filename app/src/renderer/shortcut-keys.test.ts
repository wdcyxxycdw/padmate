import { describe, expect, it } from "vitest";
import type { ProfileAction } from "../shared/generated/schema";
import type { Gesture } from "./editor-model";
import {
  chordModifiers,
  isHoldAction,
  isModifierOnly,
  type MediaKey,
  type MouseKey,
  mediaKeyLabel,
  mediaKeyName,
  mediaKeys,
  mediaKeysText,
  mouseButtons,
  mouseKeyName,
  mouseKeys,
  normaliseChord,
  usesHighFunctionKey,
  wheelKeys,
} from "./shortcut-keys";

describe("normaliseChord", () => {
  it.each([
    ["lctrl + sc:0x64", "Ctrl+F13"],
    ["ControlRight+ShiftLeft+f24", "Ctrl+Shift+F24"],
    ["Shift+Ctrl+F14", "Ctrl+Shift+F14"],
    ["sc:0x76", "F24"],
    ["sc:0x006E", "F23"],
    ["sc:0x2c", "sc:0x2C"],
    ["LShift", "Shift"],
    ["RCtrl+LCtrl", "Ctrl"],
    ["shift+ctrl", "Ctrl+Shift"],
    ["AltGr+e", "Alt+E"],
    ["Meta+L", "Win+L"],
    ["ctrl+z", "Ctrl+Z"],
    ["escape", "Esc"],
    ["Digit1", "Key1"],
    ["numpad0", "Num0"],
    ["space", "Space"],
    ["Plus", "Plus"],
    // The wheel keys have no aliases and fold in case (ADR-0062).
    ["wheelup", "WheelUp"],
    ["WHEELDOWN", "WheelDown"],
    ["lctrl+WheelLeft", "Ctrl+WheelLeft"],
    ["shift+ctrl+wheelright", "Ctrl+Shift+WheelRight"],
    // The buttons fold in case, and their aliases fold into the names.
    ["mouseleft", "MouseLeft"],
    ["MOUSEX2", "MouseX2"],
    ["LButton", "MouseLeft"],
    ["rbutton", "MouseRight"],
    ["MButton", "MouseMiddle"],
    ["xbutton1", "MouseX1"],
    ["XBUTTON2", "MouseX2"],
    ["lctrl+LButton", "Ctrl+MouseLeft"],
    ["shift+ctrl+xbutton2", "Ctrl+Shift+MouseX2"],
    // The volume and media keys (OUT-20) fold in case, and the W3C names
    // fold into the names of the daemon.
    ["volumeup", "VolumeUp"],
    ["VOLUMEMUTE", "VolumeMute"],
    ["AudioVolumeDown", "VolumeDown"],
    ["audiovolumeup", "VolumeUp"],
    ["AudioVolumeMute", "VolumeMute"],
    ["mediaplaypause", "MediaPlayPause"],
    ["MediaTrackNext", "MediaNext"],
    ["MediaTrackPrevious", "MediaPrevious"],
    ["mediastop", "MediaStop"],
    ["lctrl+MediaNext", "Ctrl+MediaNext"],
    ["shift+ctrl+VolumeUp", "Ctrl+Shift+VolumeUp"],
  ])("%s → %s", (spec, want) => {
    expect(normaliseChord(spec)).toBe(want);
  });
  it.each([
    "",
    "Ctrl+",
    "Ctrl++",
    "Z+Ctrl",
    "brush",
    "F25",
    "sc:0x80",
    "sc:0x",
    "constructor",
    // A wheel key is never a modifier: it can only end a chord.
    "WheelUp+Ctrl",
    "WheelUp+WheelDown",
    "Wheel",
    "WheelUpward",
    // Nor is a button.
    "MouseLeft+Ctrl",
    "LButton+Shift",
    "MouseLeft+MouseRight",
    "MouseLeft+WheelUp",
    "Mouse",
    "MouseLeftt",
    "MouseX3",
    "XButton3",
    "Button1",
    // A media key is never a modifier either, and the VK names are not names.
    "VolumeUp+Ctrl",
    "MediaNext+MediaStop",
    "VolumeUp+WheelUp",
    "VK_VOLUME_UP",
    "Volume",
    "Media",
    "MediaPlay",
  ])("rejects %j", (spec) => {
    expect(normaliseChord(spec)).toBeNull();
  });
});

describe("the volume and media keys (OUT-20)", () => {
  it("list the names of the daemon", () => {
    expect(mediaKeys).toEqual([
      "VolumeUp",
      "VolumeDown",
      "VolumeMute",
      "MediaPlayPause",
      "MediaNext",
      "MediaPrevious",
      "MediaStop",
    ]);
  });
  it.each<[string, MediaKey | null]>([
    ["VolumeUp", "VolumeUp"],
    ["volumedown", "VolumeDown"],
    ["AudioVolumeMute", "VolumeMute"],
    ["mediaplaypause", "MediaPlayPause"],
    ["MediaTrackNext", "MediaNext"],
    ["mediatrackprevious", "MediaPrevious"],
    [" MediaStop ", "MediaStop"],
    // Only media keys: other keys, mouse keys and nonsense are none.
    ["Z", null],
    ["Ctrl", null],
    ["WheelUp", null],
    ["MouseLeft", null],
    ["VK_MEDIA_STOP", null],
    ["constructor", null],
    ["", null],
  ])("%j names %s", (token, want) => {
    expect(mediaKeyName(token)).toBe(want);
  });
});

describe("the names of the volume and media keys (OUT-20)", () => {
  it.each<[MediaKey, string, string]>([
    ["VolumeUp", "音量加", "Volume up"],
    ["VolumeDown", "音量减", "Volume down"],
    ["VolumeMute", "静音", "Mute"],
    ["MediaPlayPause", "播放/暂停", "Play/Pause"],
    ["MediaNext", "下一首", "Next track"],
    ["MediaPrevious", "上一首", "Previous track"],
    ["MediaStop", "停止", "Stop"],
  ])("%s reads %s or %s", (key, zh, en) => {
    expect(mediaKeyLabel(key)).toBe(zh);
    expect(mediaKeyLabel(key, "zh-CN")).toBe(zh);
    expect(mediaKeyLabel(key, "en")).toBe(en);
  });
  it("puts the names into a shortcut or a list of them, and leaves the rest as written", () => {
    expect(mediaKeysText("VolumeUp")).toBe("音量加");
    expect(mediaKeysText("Ctrl+MediaNext", "en")).toBe("Ctrl+Next track");
    expect(mediaKeysText("lctrl+AudioVolumeMute", "zh-CN")).toBe("lctrl+静音");
    expect(mediaKeysText(["Ctrl+K", "VolumeDown"], "en")).toBe(
      "Ctrl+K → Volume down",
    );
    expect(mediaKeysText("Ctrl+Z")).toBe("Ctrl+Z");
    expect(mediaKeysText("Ctrl++")).toBe("Ctrl++");
    expect(mediaKeysText("")).toBe("");
  });
});

describe("the mouse keys", () => {
  it("list the names of the daemon", () => {
    expect(mouseButtons).toEqual([
      "MouseLeft",
      "MouseRight",
      "MouseMiddle",
      "MouseX1",
      "MouseX2",
    ]);
    expect(wheelKeys).toEqual([
      "WheelUp",
      "WheelDown",
      "WheelLeft",
      "WheelRight",
    ]);
    expect(mouseKeys).toEqual([...mouseButtons, ...wheelKeys]);
  });
  it.each<[string, MouseKey | null]>([
    ["MouseLeft", "MouseLeft"],
    ["mouseleft", "MouseLeft"],
    ["LButton", "MouseLeft"],
    ["RButton", "MouseRight"],
    ["mbutton", "MouseMiddle"],
    ["XButton1", "MouseX1"],
    ["xbutton2", "MouseX2"],
    ["WheelDown", "WheelDown"],
    ["wheelleft", "WheelLeft"],
    [" LButton ", "MouseLeft"],
    // Only mouse keys: other keys, modifiers and nonsense are none.
    ["Z", null],
    ["Ctrl", null],
    ["F13", null],
    ["sc:0x2c", null],
    ["Mouse", null],
    ["XButton3", null],
    ["constructor", null],
    ["", null],
  ])("%j names %s", (token, want) => {
    expect(mouseKeyName(token)).toBe(want);
  });
});

describe("chordModifiers", () => {
  it.each<[string, Record<string, string>]>([
    ["LCtrl+Shift+Z", { Ctrl: "LCtrl", Shift: "Shift" }],
    ["RAlt+LWin+WheelUp", { Alt: "RAlt", Win: "LWin" }],
    ["Ctrl+Shift", { Ctrl: "Ctrl", Shift: "Shift" }],
    ["rshift+MouseLeft", { Shift: "rshift" }],
    // A modifier twice is named once, by the first.
    ["LCtrl+RCtrl+Z", { Ctrl: "LCtrl" }],
    ["F13", {}],
    ["MouseLeft", {}],
    ["", {}],
  ])("%j has %j", (spec, want) => {
    expect(chordModifiers(spec)).toEqual(want);
  });
});

describe("isHoldAction", () => {
  it.each<[Gesture, ProfileAction, boolean]>([
    ["press", { type: "keys", keys: "F13" }, true],
    ["press", { type: "keys", keys: "" }, true],
    ["hold", { type: "keys", keys: "F13", mode: "hold" }, true],
    ["press", { type: "keys", keys: "F13", mode: "tap" }, false],
    ["press", { type: "keys", keys: ["F13", "F14"], mode: "tap" }, false],
    ["press", { type: "keys", keys: "F13", repeat: { style: "tap" } }, false],
    [
      "press",
      { type: "keys", keys: "F13", repeat: { style: "typematic" } },
      true,
    ],
    ["tap", { type: "keys", keys: "F13", mode: "tap" }, false],
    ["press", { type: "script", name: "x" }, false],
  ])("%s %j → %s", (gesture, action, want) => {
    expect(isHoldAction(gesture, action)).toBe(want);
  });
});

describe("isModifierOnly", () => {
  it.each<[string, boolean]>([
    ["LCtrl", true],
    ["Ctrl+Shift", true],
    ["alt", true],
    ["Ctrl+F13", false],
    ["Ctrl+WheelUp", false],
    ["WheelUp", false],
    ["Ctrl+MouseLeft", false],
    ["LButton", false],
    ["Space", false],
    ["", false],
    ["Ctrl+", false],
  ])("%j → %s", (spec, want) => {
    expect(isModifierOnly(spec)).toBe(want);
  });
});

describe("usesHighFunctionKey", () => {
  it.each<[string | string[], boolean]>([
    ["F13", true],
    ["Ctrl+Shift+F24", true],
    ["sc:0x64", true],
    [["Ctrl+K", "F20"], true],
    ["Ctrl+WheelUp", false],
    ["Ctrl+MouseLeft", false],
    ["F12", false],
    ["Ctrl+Z", false],
    ["Space", false],
    ["", false],
  ])("%j → %s", (keys, want) => {
    expect(usesHighFunctionKey(keys)).toBe(want);
  });
});
