import { describe, expect, it } from "vitest";
import type {
  ProfileAction,
  ProfileKeysAction,
} from "../shared/generated/schema";
import type { Gesture } from "./editor-model";
import {
  endsInMouseButton,
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
  withMouseKey,
  withoutHeldButtonRepeat,
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
  it.each<[string, boolean]>([
    ["MouseLeft", true],
    ["Ctrl+MouseRight", true],
    ["LCtrl+Shift+xbutton1", true],
    ["Alt+MButton", true],
    ["WheelUp", false],
    ["Ctrl+WheelDown", false],
    ["Ctrl+Z", false],
    ["Ctrl", false],
    ["MouseLeft+Ctrl", false],
    ["", false],
  ])("%j ends in a button: %s", (spec, want) => {
    expect(endsInMouseButton(spec)).toBe(want);
  });
});

describe("withMouseKey", () => {
  it.each<[string, MouseKey, string]>([
    // The modifiers stay as written, the main key goes.
    ["Ctrl+Z", "WheelUp", "Ctrl+WheelUp"],
    ["LCtrl+Shift+Z", "WheelDown", "LCtrl+Shift+WheelDown"],
    ["Alt+Win+F13", "WheelLeft", "Alt+Win+WheelLeft"],
    ["Ctrl+sc25", "WheelRight", "Ctrl+WheelRight"],
    ["Ctrl+Z", "MouseLeft", "Ctrl+MouseLeft"],
    ["LShift+K", "MouseX2", "LShift+MouseX2"],
    // A chord of modifiers only keeps them all.
    ["Ctrl+Shift", "WheelUp", "Ctrl+Shift+WheelUp"],
    ["rshift", "WheelUp", "rshift+WheelUp"],
    ["Alt", "MouseRight", "Alt+MouseRight"],
    // Another mouse key is replaced, of either kind.
    ["Ctrl+WheelUp", "WheelDown", "Ctrl+WheelDown"],
    ["WheelLeft", "WheelRight", "WheelRight"],
    ["Ctrl+MouseLeft", "MouseRight", "Ctrl+MouseRight"],
    ["MouseLeft", "WheelUp", "WheelUp"],
    ["Shift+WheelUp", "MouseMiddle", "Shift+MouseMiddle"],
    ["Ctrl+LButton", "MouseX1", "Ctrl+MouseX1"],
    // Without a chord there is the mouse key alone.
    ["", "WheelUp", "WheelUp"],
    ["  ", "WheelDown", "WheelDown"],
    ["Z", "WheelUp", "WheelUp"],
    ["", "MouseLeft", "MouseLeft"],
    ["Z", "MouseMiddle", "MouseMiddle"],
    // A modifier twice (both sides, an alias) is kept once.
    ["LCtrl+RCtrl+Z", "WheelUp", "LCtrl+WheelUp"],
    ["Ctrl+Control+Shift", "WheelUp", "Ctrl+Shift+WheelUp"],
    ["LCtrl+RCtrl+Z", "MouseLeft", "LCtrl+MouseLeft"],
    // A dangling "+" leaves nothing behind.
    ["Ctrl+", "WheelUp", "Ctrl+WheelUp"],
    ["Ctrl+", "MouseRight", "Ctrl+MouseRight"],
  ])("%j with %s gives %j", (spec, mouse, want) => {
    expect(withMouseKey(spec, mouse)).toBe(want);
  });
  it("gives chords the daemon reads back", () => {
    for (const mouse of mouseKeys) {
      expect(normaliseChord(withMouseKey("lctrl+Z", mouse))).toBe(
        `Ctrl+${mouse}`,
      );
      expect(normaliseChord(withMouseKey("", mouse))).toBe(mouse);
    }
  });
});

describe("withoutHeldButtonRepeat", () => {
  const keys = (extra: Partial<ProfileKeysAction>): ProfileKeysAction => ({
    type: "keys",
    keys: "MouseLeft",
    ...extra,
  });
  const typematic = { style: "typematic", delayMs: 350, rateHz: 12 } as const;
  const clicks = { style: "tap", delayMs: 350, rateHz: 12 } as const;

  it("turns a hold repeat of a button into repeated clicks", () => {
    // An explicit hold mode, the default mode of press and hold, a repeat
    // style that says it: all hold.
    expect(
      withoutHeldButtonRepeat(keys({ mode: "hold", repeat: typematic }), true),
    ).toEqual(keys({ mode: "tap", repeat: clicks }));
    expect(withoutHeldButtonRepeat(keys({ repeat: typematic }), true)).toEqual(
      keys({ mode: "tap", repeat: clicks }),
    );
    expect(
      withoutHeldButtonRepeat(keys({ repeat: { rateHz: 5 } }), true),
    ).toEqual(keys({ mode: "tap", repeat: { rateHz: 5, style: "tap" } }));
    expect(
      withoutHeldButtonRepeat(
        keys({ keys: "Ctrl+xbutton1", repeat: typematic }),
        true,
      ),
    ).toEqual(keys({ keys: "Ctrl+xbutton1", mode: "tap", repeat: clicks }));
  });
  it("keeps the other settings of the repeat", () => {
    const repeat = {
      style: "typematic",
      delayMs: 0,
      rateHz: 30,
      minRateHz: 5,
    } as const;
    expect(
      withoutHeldButtonRepeat(keys({ mode: "hold", repeat }), true),
    ).toEqual(keys({ mode: "tap", repeat: { ...repeat, style: "tap" } }));
  });
  it("leaves repeated clicks, holds and every other chord as they are", () => {
    const same: [ProfileKeysAction, boolean][] = [
      [keys({ mode: "tap", repeat: clicks }), true],
      [keys({ repeat: { style: "tap" } }), true],
      [keys({ mode: "tap", repeat: { rateHz: 5 } }), true],
      [keys({ mode: "hold" }), true],
      [keys({}), true],
      [keys({ mode: "tap" }), true],
      // Not a gesture that holds: its mode is tap.
      [keys({ repeat: { rateHz: 5 } }), false],
      [keys({ keys: "Ctrl+Z", mode: "hold", repeat: typematic }), true],
      [keys({ keys: "WheelUp", mode: "hold", repeat: typematic }), true],
      [keys({ keys: "Ctrl+WheelDown", repeat: typematic }), true],
      [keys({ keys: "", mode: "hold", repeat: typematic }), true],
      // A sequence has no repeat to settle.
      [keys({ keys: ["MouseLeft", "F13"], mode: "tap" }), true],
    ];
    for (const [action, canHold] of same)
      expect(withoutHeldButtonRepeat(action, canHold)).toBe(action);
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
