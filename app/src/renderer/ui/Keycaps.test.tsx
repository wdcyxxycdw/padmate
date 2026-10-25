// @vitest-environment jsdom
import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { chordKeys, Keycaps } from "./Keycaps";

afterEach(cleanup);

describe("chordKeys", () => {
  it.each([
    ["Ctrl+Z", ["Ctrl", "Z"]],
    ["LCtrl+Shift+Up", ["Ctrl", "Shift", "↑"]],
    ["KeyZ", ["Z"]],
  ])("%s keeps its keycaps", (chord, want) => {
    expect(chordKeys(chord)).toEqual(want);
    expect(chordKeys(chord, "en")).toEqual(want);
  });
  it.each([
    ["WheelUp", ["滚轮↑"], ["Wheel↑"]],
    ["WheelDown", ["滚轮↓"], ["Wheel↓"]],
    ["WheelLeft", ["滚轮←"], ["Wheel←"]],
    ["WheelRight", ["滚轮→"], ["Wheel→"]],
    ["Ctrl+WheelUp", ["Ctrl", "滚轮↑"], ["Ctrl", "Wheel↑"]],
    [
      "LCtrl+Shift+WheelDown",
      ["Ctrl", "Shift", "滚轮↓"],
      ["Ctrl", "Shift", "Wheel↓"],
    ],
    // Key names fold in case, as in the daemon.
    ["alt+wheelleft", ["Alt", "滚轮←"], ["Alt", "Wheel←"]],
  ])("%s reads %j or %j", (chord, zh, en) => {
    expect(chordKeys(chord)).toEqual(zh);
    expect(chordKeys(chord, "zh-CN")).toEqual(zh);
    expect(chordKeys(chord, "en")).toEqual(en);
  });
});

describe("chordKeys of the mouse buttons", () => {
  it.each([
    ["MouseLeft", ["左键"], ["LMB"]],
    ["MouseRight", ["右键"], ["RMB"]],
    ["MouseMiddle", ["中键"], ["MMB"]],
    ["MouseX1", ["侧键1"], ["X1"]],
    ["MouseX2", ["侧键2"], ["X2"]],
    ["Shift+MouseLeft", ["Shift", "左键"], ["Shift", "LMB"]],
    ["LCtrl+Alt+MouseX2", ["Ctrl", "Alt", "侧键2"], ["Ctrl", "Alt", "X2"]],
    // Key names fold in case, and the aliases read like the names.
    ["mouseleft", ["左键"], ["LMB"]],
    ["LButton", ["左键"], ["LMB"]],
    ["rbutton", ["右键"], ["RMB"]],
    ["MButton", ["中键"], ["MMB"]],
    ["XButton1", ["侧键1"], ["X1"]],
    ["ctrl+XBUTTON2", ["Ctrl", "侧键2"], ["Ctrl", "X2"]],
  ])("%s reads %j or %j", (chord, zh, en) => {
    expect(chordKeys(chord)).toEqual(zh);
    expect(chordKeys(chord, "zh-CN")).toEqual(zh);
    expect(chordKeys(chord, "en")).toEqual(en);
  });
  it("leaves other names alone", () => {
    expect(chordKeys("Mouse")).toEqual(["Mouse"]);
    expect(chordKeys("MouseX3", "en")).toEqual(["MouseX3"]);
  });
});

describe("chordKeys of the volume and media keys (OUT-20)", () => {
  it.each([
    ["VolumeUp", ["音量加"], ["Volume up"]],
    ["VolumeDown", ["音量减"], ["Volume down"]],
    ["VolumeMute", ["静音"], ["Mute"]],
    ["MediaPlayPause", ["播放/暂停"], ["Play/Pause"]],
    ["MediaNext", ["下一首"], ["Next track"]],
    ["MediaPrevious", ["上一首"], ["Previous track"]],
    ["MediaStop", ["停止"], ["Stop"]],
    ["Ctrl+VolumeUp", ["Ctrl", "音量加"], ["Ctrl", "Volume up"]],
    // Key names fold in case, and the W3C names read like the names.
    ["volumeup", ["音量加"], ["Volume up"]],
    ["AudioVolumeMute", ["静音"], ["Mute"]],
    ["MediaTrackNext", ["下一首"], ["Next track"]],
    [
      "lctrl+MediaTrackPrevious",
      ["Ctrl", "上一首"],
      ["Ctrl", "Previous track"],
    ],
  ])("%s reads %j or %j", (chord, zh, en) => {
    expect(chordKeys(chord)).toEqual(zh);
    expect(chordKeys(chord, "zh-CN")).toEqual(zh);
    expect(chordKeys(chord, "en")).toEqual(en);
  });
  it("leaves other names alone", () => {
    expect(chordKeys("Volume")).toEqual(["Volume"]);
    expect(chordKeys("MediaPlay", "en")).toEqual(["MediaPlay"]);
  });
});

describe("Keycaps", () => {
  const caps = (container: HTMLElement) =>
    [...container.querySelectorAll("kbd")].map((cap) => cap.textContent);

  it("shows media keys by language", () => {
    const { container, rerender } = render(<Keycaps keys="Ctrl+VolumeUp" />);
    expect(caps(container)).toEqual(["Ctrl", "音量加"]);
    rerender(<Keycaps keys={["MediaPlayPause", "MediaNext"]} language="en" />);
    expect(caps(container)).toEqual(["Play/Pause", "Next track"]);
  });

  it("shows wheel keys by language, in chords and in sequences", () => {
    const { container, rerender } = render(<Keycaps keys="Ctrl+WheelUp" />);
    expect(caps(container)).toEqual(["Ctrl", "滚轮↑"]);
    rerender(<Keycaps keys="Ctrl+WheelUp" language="en" />);
    expect(caps(container)).toEqual(["Ctrl", "Wheel↑"]);
    rerender(<Keycaps keys={["Ctrl+WheelDown", "WheelRight"]} language="en" />);
    expect(caps(container)).toEqual(["Ctrl", "Wheel↓", "Wheel→"]);
    rerender(
      <Keycaps keys={["Ctrl+WheelDown", "WheelRight"]} language="zh-CN" />,
    );
    expect(caps(container)).toEqual(["Ctrl", "滚轮↓", "滚轮→"]);
  });

  it("shows mouse buttons by language, in chords and in sequences", () => {
    const { container, rerender } = render(<Keycaps keys="Shift+MouseLeft" />);
    expect(caps(container)).toEqual(["Shift", "左键"]);
    rerender(<Keycaps keys="Shift+MouseLeft" language="en" />);
    expect(caps(container)).toEqual(["Shift", "LMB"]);
    rerender(<Keycaps keys={["MouseRight", "Alt+XButton1"]} language="en" />);
    expect(caps(container)).toEqual(["RMB", "Alt", "X1"]);
    rerender(
      <Keycaps keys={["MouseMiddle", "Alt+MouseX2"]} language="zh-CN" />,
    );
    expect(caps(container)).toEqual(["中键", "Alt", "侧键2"]);
  });
});
