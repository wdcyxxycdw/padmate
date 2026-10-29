// ADR-0076 section 5: what a shortcut capture records. The first key that is
// not a modifier ends it at once, with the modifiers that are held; modifiers
// alone are recorded when they are all let go; at most three keys. The rules
// are pure: the field feeds in what the helper heard and what the window saw.
import { describe, expect, it } from "vitest";
import { MAX_CAPTURE_KEYS, ShortcutRecorder } from "./capture-recorder";

const none = { Ctrl: false, Shift: false, Alt: false, Win: false };
const held = (...mods: (keyof typeof none)[]) => ({
  ...none,
  ...Object.fromEntries(mods.map((mod) => [mod, true])),
});

describe("a key that is not a modifier", () => {
  it("ends the capture at once, with the modifiers held", () => {
    expect(new ShortcutRecorder().heard("Z")).toEqual({
      done: true,
      chord: "Z",
    });
    expect(new ShortcutRecorder().heard("LCtrl+LShift+Z")).toEqual({
      done: true,
      chord: "LCtrl+LShift+Z",
    });
  });

  it("is recorded as what the helper says for the key's position when asked", () => {
    expect(new ShortcutRecorder().heard("Ctrl+K", "Ctrl+sc:0x25")).toEqual({
      done: true,
      chord: "Ctrl+sc:0x25",
    });
  });

  it("is the same for the media keys, F13 and the mouse keys the helper never reports", () => {
    for (const chord of ["VolumeUp", "F13", "Ctrl+MediaNext", "Enter"])
      expect(new ShortcutRecorder().heard(chord)).toEqual({
        done: true,
        chord,
      });
  });
});

describe("modifiers alone", () => {
  it("are shown while they are held, and recorded when all are let go", () => {
    const recorder = new ShortcutRecorder();
    expect(recorder.heard("LCtrl")).toEqual({ done: false, shown: "LCtrl" });
    expect(recorder.heard("LCtrl+LShift")).toEqual({
      done: false,
      shown: "LCtrl+LShift",
    });
    expect(recorder.modifiersReleased()).toEqual({
      done: true,
      chord: "LCtrl+LShift",
    });
  });

  it("record a single Alt", () => {
    const recorder = new ShortcutRecorder();
    recorder.heard("LAlt");
    expect(recorder.modifiersReleased()).toEqual({
      done: true,
      chord: "LAlt",
    });
  });

  it("with a key pressed before they are up, are part of its chord", () => {
    const recorder = new ShortcutRecorder();
    recorder.heard("LCtrl");
    expect(recorder.heard("LCtrl+Z")).toEqual({ done: true, chord: "LCtrl+Z" });
  });

  it("are not a chord when nothing was heard", () => {
    expect(new ShortcutRecorder().modifiersReleased()).toEqual({
      done: false,
      shown: null,
    });
  });
});

describe("at most three keys", () => {
  it("is three keys with the fourth being any key: the first three are recorded", () => {
    expect(MAX_CAPTURE_KEYS).toBe(3);
    // Three modifiers, then a key: the key is the fourth.
    const recorder = new ShortcutRecorder();
    recorder.heard("LCtrl");
    recorder.heard("LCtrl+LShift");
    expect(recorder.heard("LCtrl+LShift+LAlt")).toEqual({
      done: false,
      shown: "LCtrl+LShift+LAlt",
    });
    expect(recorder.heard("LCtrl+LShift+LAlt+Z")).toEqual({
      done: true,
      chord: "LCtrl+LShift+LAlt",
    });
  });

  it("is also the rule for a fourth modifier, which is not recorded", () => {
    const recorder = new ShortcutRecorder();
    recorder.heard("LCtrl");
    recorder.heard("LCtrl+LShift");
    recorder.heard("LCtrl+LShift+LAlt");
    expect(recorder.heard("LCtrl+LShift+LAlt+LWin")).toEqual({
      done: true,
      chord: "LCtrl+LShift+LAlt",
    });
  });

  it("keeps the three that went down first, not the first three of the name", () => {
    // Win, Ctrl and Shift went down; the helper names them in its own order.
    const recorder = new ShortcutRecorder();
    recorder.heard("LWin");
    recorder.heard("LCtrl+LWin");
    recorder.heard("LCtrl+LShift+LWin");
    expect(recorder.heard("LCtrl+LShift+LAlt+LWin")).toEqual({
      done: true,
      chord: "LCtrl+LShift+LWin",
    });
  });

  it("falls back to the first three of the chord when the earlier events were missed", () => {
    expect(
      new ShortcutRecorder().heard(
        "LCtrl+LShift+LAlt+Z",
        "LCtrl+LShift+LAlt+sc:0x2C",
      ),
    ).toEqual({ done: true, chord: "LCtrl+LShift+LAlt" });
  });

  it("is two modifiers and a key without trouble", () => {
    expect(new ShortcutRecorder().heard("LCtrl+LShift+Z")).toEqual({
      done: true,
      chord: "LCtrl+LShift+Z",
    });
  });
});

describe("Esc", () => {
  it("waits for its release, which records it", () => {
    const recorder = new ShortcutRecorder();
    expect(recorder.heard("Esc")).toEqual({ done: false, shown: "Esc" });
    expect(recorder.escReleased()).toEqual({ done: true, chord: "Esc" });
  });

  it("is recorded as its position when asked, and by the name for the logic", () => {
    const recorder = new ShortcutRecorder();
    expect(recorder.heard("Esc", "sc:0x01")).toEqual({
      done: false,
      shown: "sc:0x01",
    });
    expect(recorder.escReleased()).toEqual({ done: true, chord: "sc:0x01" });
  });

  it("is cancelled by a hold: nothing is recorded", () => {
    const recorder = new ShortcutRecorder();
    recorder.heard("Esc");
    expect(recorder.cancel()).toEqual({ done: true, chord: null });
  });

  it("with a modifier is a chord like any other", () => {
    expect(new ShortcutRecorder().heard("LShift+Esc")).toEqual({
      done: true,
      chord: "LShift+Esc",
    });
  });

  it("is the first key when another goes down while it is held", () => {
    for (const next of ["Z", "LCtrl", "LCtrl+Z"]) {
      const recorder = new ShortcutRecorder();
      recorder.heard("Esc");
      expect(recorder.heard(next), next).toEqual({ done: true, chord: "Esc" });
    }
    const recorder = new ShortcutRecorder();
    recorder.heard("Esc");
    expect(recorder.mouse("MouseLeft", none)).toEqual({
      done: true,
      chord: "Esc",
    });
  });

  it("is not what lets modifiers go, and its release is not theirs", () => {
    const recorder = new ShortcutRecorder();
    recorder.heard("Esc");
    expect(recorder.modifiersReleased()).toEqual({
      done: false,
      shown: "Esc",
    });
    const other = new ShortcutRecorder();
    other.heard("LCtrl");
    expect(other.escReleased()).toEqual({ done: false, shown: "LCtrl" });
  });
});

describe("the mouse", () => {
  it("is a key of its own: a button or one notch of the wheel ends the capture", () => {
    for (const key of [
      "MouseLeft",
      "MouseRight",
      "MouseMiddle",
      "MouseX1",
      "MouseX2",
      "WheelUp",
      "WheelDown",
      "WheelLeft",
      "WheelRight",
    ] as const)
      expect(new ShortcutRecorder().mouse(key, none)).toEqual({
        done: true,
        chord: key,
      });
  });

  it("takes the modifiers that are held, in the order of a chord", () => {
    expect(new ShortcutRecorder().mouse("WheelUp", held("Win", "Alt"))).toEqual(
      { done: true, chord: "Alt+Win+WheelUp" },
    );
    expect(
      new ShortcutRecorder().mouse("WheelUp", held("Shift", "Ctrl")),
    ).toEqual({ done: true, chord: "Ctrl+Shift+WheelUp" });
  });

  it("keeps the sides the helper heard, and says a modifier without a side otherwise", () => {
    const recorder = new ShortcutRecorder();
    recorder.heard("LCtrl+RAlt");
    expect(recorder.mouse("MouseLeft", held("Ctrl", "Alt"))).toEqual({
      done: true,
      chord: "LCtrl+RAlt+MouseLeft",
    });
    // The helper never heard Shift, which may be only late.
    expect(
      new ShortcutRecorder().mouse("MouseLeft", held("Ctrl", "Shift")),
    ).toEqual({ done: true, chord: "Ctrl+Shift+MouseLeft" });
    const other = new ShortcutRecorder();
    other.heard("LCtrl");
    expect(other.mouse("MouseLeft", held("Ctrl", "Shift"))).toEqual({
      done: true,
      chord: "LCtrl+Shift+MouseLeft",
    });
  });

  it("does not take a modifier that is no longer held", () => {
    const recorder = new ShortcutRecorder();
    recorder.heard("LCtrl+LShift");
    expect(recorder.mouse("MouseRight", held("Shift"))).toEqual({
      done: true,
      chord: "LShift+MouseRight",
    });
  });

  it("is the fourth key with three modifiers held, and is not recorded", () => {
    expect(
      new ShortcutRecorder().mouse("MouseLeft", held("Ctrl", "Shift", "Alt")),
    ).toEqual({ done: true, chord: "Ctrl+Shift+Alt" });
    const recorder = new ShortcutRecorder();
    recorder.heard("LCtrl+LShift+LAlt");
    expect(recorder.mouse("WheelDown", held("Ctrl", "Shift", "Alt"))).toEqual({
      done: true,
      chord: "LCtrl+LShift+LAlt",
    });
  });

  it("is a second key after two modifiers and counts three", () => {
    expect(
      new ShortcutRecorder().mouse("MouseLeft", held("Ctrl", "Shift")),
    ).toEqual({ done: true, chord: "Ctrl+Shift+MouseLeft" });
  });
});

describe("the end of the capture", () => {
  it("records the modifiers that were held, as it does when they are let go", () => {
    const recorder = new ShortcutRecorder();
    recorder.heard("LAlt");
    expect(recorder.end()).toEqual({ done: true, chord: "LAlt" });
  });

  it("records nothing when nothing was heard", () => {
    expect(new ShortcutRecorder().end()).toEqual({ done: true, chord: null });
  });

  it("is cancelled by a hold of Esc even after modifiers were heard", () => {
    const recorder = new ShortcutRecorder();
    recorder.heard("LCtrl");
    expect(recorder.cancel()).toEqual({ done: true, chord: null });
  });
});
