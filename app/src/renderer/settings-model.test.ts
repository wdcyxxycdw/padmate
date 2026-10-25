import { describe, expect, it } from "vitest";
import type { Config } from "../shared/generated/schema";
import {
  batteryTags,
  ComboRecorder,
  comboProblem,
  deviceCombo,
  FEEDBACK_SWITCHES,
  feedbackSwitchOn,
  globalTiming,
  identifyFailureText,
  isPanicFamily,
  parseMs,
  setFeedbackSwitch,
  setGlobalTiming,
  setPanicCombo,
  startupDryRun,
  startupFailureText,
} from "./settings-model";

describe("global timing (UI-19)", () => {
  it("reads a millisecond field", () => {
    expect(parseMs("")).toBeNull();
    expect(parseMs("  ")).toBeNull();
    expect(parseMs(" 400 ")).toBe(400);
    expect(parseMs("0")).toBe(0);
    expect(parseMs("60000")).toBe(60000);
    for (const text of ["60001", "-1", "1.5", "1e3", "abc"])
      expect(parseMs(text), text).toBeUndefined();
  });

  it("sets and removes one value, keeping the others", () => {
    const config: Config = {
      schemaVersion: 0,
      timing: { multiTapMs: 300 },
      rules: [],
    };
    expect(setGlobalTiming(config, "holdMs", 400)).toEqual({
      schemaVersion: 0,
      timing: { multiTapMs: 300, holdMs: 400 },
      rules: [],
    });
    expect(setGlobalTiming(config, "multiTapMs", 300)).toBeNull();
    expect(setGlobalTiming(config, "holdMs", null)).toBeNull();
    // The last value removes timing itself.
    expect(setGlobalTiming(config, "multiTapMs", null)).toEqual({
      schemaVersion: 0,
      rules: [],
    });
    expect(config.timing).toEqual({ multiTapMs: 300 });
    expect(globalTiming(config, "multiTapMs")).toBe(300);
    expect(globalTiming(config, "holdMs")).toBeUndefined();
    expect(
      globalTiming(
        { schemaVersion: 0, timing: { holdMs: 1.5 } } as Config,
        "holdMs",
      ),
    ).toBeUndefined();
    expect(globalTiming(null, "holdMs")).toBeUndefined();
  });
});

describe("emergency combos (UI-19)", () => {
  it("sets and removes one family", () => {
    const combo = { buttons: ["west", "east"], holdMs: 3000 };
    const config: Config = {
      schemaVersion: 0,
      safety: {
        panic: { joyconLeft: { buttons: ["south", "east"], holdMs: 500 } },
      },
    };
    const next = setPanicCombo(config, "standard", combo);
    expect(next).toEqual({
      schemaVersion: 0,
      safety: {
        panic: {
          joyconLeft: { buttons: ["south", "east"], holdMs: 500 },
          standard: combo,
        },
      },
    });
    expect(setPanicCombo(next as Config, "standard", { ...combo })).toBeNull();
    expect(setPanicCombo(config, "standard", null)).toBeNull();
    expect(setPanicCombo(config, "joyconLeft", null)).toEqual({
      schemaVersion: 0,
    });
    expect(config.safety?.panic?.standard).toBeUndefined();
  });

  it("checks families, devices and the number of buttons", () => {
    expect(isPanicFamily("joyconRightVertical")).toBe(true);
    expect(isPanicFamily("joycon")).toBe(false);
    const combo = {
      device: 7,
      family: "standard",
      buttons: ["back", "start"],
      holdMs: 2000,
    };
    const status = { combos: [combo] } as Parameters<typeof deviceCombo>[0];
    expect(deviceCombo(status, 7)).toBe(combo);
    expect(deviceCombo(status, 8)).toBeUndefined();
    expect(deviceCombo(null, 7)).toBeUndefined();
    expect(comboProblem(["a"])).toBe("few");
    expect(comboProblem(["a", "b"])).toBeNull();
    expect(comboProblem(["a", "b", "c", "d"])).toBeNull();
    expect(comboProblem(["a", "b", "c", "d", "e"])).toBe("many");
  });

  it("records the most buttons held together, in press order", () => {
    const recorder = new ComboRecorder();
    expect(recorder.update([])).toBe(false);
    expect(recorder.update(["east"])).toBe(false);
    expect(recorder.update(["south", "east"])).toBe(false);
    expect(recorder.pressed).toEqual(["east", "south"]);
    expect(recorder.update(["east", "south", "west"])).toBe(false);
    // Letting go of one is not the end; the largest set stays.
    expect(recorder.update(["east"])).toBe(false);
    expect(recorder.update([])).toBe(true);
    expect(recorder.recorded).toEqual(["east", "south", "west"]);
    expect(recorder.update([])).toBe(false);
    // The next press starts another recording.
    recorder.update(["north"]);
    expect(recorder.update([])).toBe(true);
    expect(recorder.recorded).toEqual(["north"]);
  });
});

describe("start at sign-in (UI-19)", () => {
  const ready = {
    available: true,
    enabled: false,
    current: false,
    mode: "inject",
  } as const;
  it("marks a dry run only for this daemon's entry or none", () => {
    const dry = { ...ready, mode: "dry-run" } as const;
    expect(startupDryRun(dry)).toBe(true);
    expect(startupDryRun({ ...dry, enabled: true, current: true })).toBe(true);
    // Another padmate's value: its mode is not this daemon's.
    expect(startupDryRun({ ...dry, enabled: true })).toBe(false);
    expect(startupDryRun(ready)).toBe(false);
    expect(
      startupDryRun({ ...dry, available: false, reason: "development" }),
    ).toBe(false);
  });

  it("explains a failed change", () => {
    expect(startupFailureText({ code: -32017 }, "zh-CN")).toBe(
      "开发版无法设置登录时启动。",
    );
    expect(startupFailureText({ code: -32098 }, "en")).toBe(
      "Start at sign-in not changed. See Settings → Logs.",
    );
    expect(startupFailureText({}, "zh-CN")).toBe(
      "无法更改登录时启动，原因见“设置 → 日志”。",
    );
  });
});

describe("identify a controller (IN-13)", () => {
  it("explains a failure", () => {
    expect(identifyFailureText({ code: -32004 }, "zh-CN")).toBe("手柄已断开。");
    expect(identifyFailureText({ code: -32004 }, "en")).toBe(
      "The controller is no longer connected.",
    );
    expect(identifyFailureText({ code: -32098 }, "zh-CN")).toBe(
      "手柄没有响应，原因见“设置 → 日志”。",
    );
    expect(identifyFailureText({}, "en")).toBe(
      "The controller did not respond. See Settings → Logs.",
    );
  });
});

describe("the feedback switches of config.json (UI-27, ADR-0067)", () => {
  const base: Config = { schemaVersion: 0 };

  it("lists the four switches in the order the page shows them", () => {
    expect(FEEDBACK_SWITCHES).toEqual(["rumble", "led", "pause", "lowBattery"]);
  });

  it("is on unless the file says false", () => {
    for (const key of FEEDBACK_SWITCHES) {
      expect(feedbackSwitchOn(null, key)).toBe(true);
      expect(feedbackSwitchOn(base, key)).toBe(true);
      expect(feedbackSwitchOn({ ...base, feedback: {} }, key)).toBe(true);
      expect(
        feedbackSwitchOn({ ...base, feedback: { [key]: true } }, key),
      ).toBe(true);
      expect(
        feedbackSwitchOn({ ...base, feedback: { [key]: false } }, key),
      ).toBe(false);
    }
    // One switch does not touch another.
    expect(
      feedbackSwitchOn({ ...base, feedback: { led: false } }, "rumble"),
    ).toBe(true);
  });

  it("writes false for a switch that goes off and removes the member when it goes on", () => {
    const off = setFeedbackSwitch(base, "rumble", false);
    expect(off).toEqual({ schemaVersion: 0, feedback: { rumble: false } });
    const more = setFeedbackSwitch(off as Config, "lowBattery", false);
    expect(more).toEqual({
      schemaVersion: 0,
      feedback: { rumble: false, lowBattery: false },
    });
    const back = setFeedbackSwitch(more as Config, "rumble", true);
    expect(back).toEqual({
      schemaVersion: 0,
      feedback: { lowBattery: false },
    });
  });

  it("drops the object with its last member, so the file is as before", () => {
    const off: Config = { schemaVersion: 0, feedback: { led: false } };
    const on = setFeedbackSwitch(off, "led", true);
    expect(on).toEqual({ schemaVersion: 0 });
    expect(on && "feedback" in on).toBe(false);
  });

  it("changes nothing when the switch already is as asked", () => {
    expect(setFeedbackSwitch(base, "pause", true)).toBeNull();
    expect(
      setFeedbackSwitch(
        { ...base, feedback: { pause: false } },
        "pause",
        false,
      ),
    ).toBeNull();
    // A member written as true is on: turning it on removes it.
    expect(
      setFeedbackSwitch({ ...base, feedback: { pause: true } }, "pause", true),
    ).toEqual({ schemaVersion: 0 });
  });

  it("keeps the other members of the file and does not change its input", () => {
    const config: Config = {
      schemaVersion: 0,
      enabled: false,
      timing: { holdMs: 300 },
      feedback: { led: false },
    };
    const before = structuredClone(config);
    expect(setFeedbackSwitch(config, "rumble", false)).toEqual({
      schemaVersion: 0,
      enabled: false,
      timing: { holdMs: 300 },
      feedback: { led: false, rumble: false },
    });
    expect(config).toEqual(before);
  });
});

describe("the tags of a controller's battery (IN-12)", () => {
  it("is the charge, and 'charging' while it charges", () => {
    expect(batteryTags({ state: "discharging", percent: 80 }, "zh-CN")).toEqual(
      ["电量 80%"],
    );
    expect(batteryTags({ state: "charging", percent: 55 }, "zh-CN")).toEqual([
      "电量 55%",
      "充电中",
    ]);
    expect(batteryTags({ state: "charging" }, "zh-CN")).toEqual(["充电中"]);
    expect(batteryTags({ state: "discharging", percent: 0 }, "zh-CN")).toEqual([
      "电量 0%",
    ]);
  });

  it("is nothing for a controller that is wired: a receiver says full for ever (ADR-0067)", () => {
    expect(batteryTags({ state: "wired", percent: 100 }, "zh-CN")).toEqual([]);
    expect(batteryTags({ state: "wired", percent: 40 }, "zh-CN")).toEqual([]);
    expect(batteryTags({ state: "wired" }, "zh-CN")).toEqual([]);
    expect(batteryTags({ state: "wired", percent: 100 }, "en")).toEqual([]);
  });

  it("is nothing when it is not read, unknown, or reports no charge and is not charging", () => {
    expect(batteryTags(undefined, "zh-CN")).toEqual([]);
    expect(batteryTags({ state: "unknown" }, "zh-CN")).toEqual([]);
    expect(batteryTags({ state: "unknown", percent: 50 }, "zh-CN")).toEqual([]);
    expect(batteryTags({ state: "discharging" }, "zh-CN")).toEqual([]);
  });

  it("is English when the language is", () => {
    expect(batteryTags({ state: "charging", percent: 42 }, "en")).toEqual([
      "Battery 42%",
      "Charging",
    ]);
    expect(batteryTags({ state: "unknown" }, "en")).toEqual([]);
  });
});
