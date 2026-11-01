import { describe, expect, it } from "vitest";
import type { Config } from "../../shared/generated/schema";
import {
  batteryTags,
  ComboRecorder,
  comboProblem,
  FEEDBACK_SWITCHES,
  globalTiming,
  identifyFailureText,
  joyCon2AutoConnect,
  parseMs,
  setGlobalTiming,
  setJoyCon2AutoConnect,
  startupDryRun,
  startupFailureText,
} from "./settings-model";

describe("global timing", () => {
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

describe("emergency combos", () => {
  it("checks the number of buttons of a combo", () => {
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

  it("does not record the Xbox button, which padmate never uses (ADR-0012)", () => {
    const recorder = new ComboRecorder();
    recorder.update(["guide"]);
    expect(recorder.pressed).toEqual([]);
    recorder.update(["guide", "start"]);
    recorder.update(["guide", "start", "back"]);
    expect(recorder.pressed).toEqual(["start", "back"]);
    expect(recorder.update(["guide"])).toBe(true);
    expect(recorder.recorded).toEqual(["start", "back"]);
    // Held alone it is no recording at all.
    expect(recorder.update([])).toBe(false);
  });
});

describe("start at sign-in", () => {
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

describe("identify a controller", () => {
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

describe("the feedback switches (ADR-0015)", () => {
  it("lists the four switches in the order the page shows them", () => {
    expect(FEEDBACK_SWITCHES).toEqual(["rumble", "led", "pause", "lowBattery"]);
  });
});

describe("the tags of a controller's battery", () => {
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

  it("is nothing for a controller that is wired: a receiver says full for ever (ADR-0015)", () => {
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

describe("joycon2.autoConnect of config.json", () => {
  const base: Config = { schemaVersion: 0 };

  it("is on unless the file says false", () => {
    expect(joyCon2AutoConnect(null)).toBe(true);
    expect(joyCon2AutoConnect(base)).toBe(true);
    expect(joyCon2AutoConnect({ ...base, joycon2: {} })).toBe(true);
    expect(
      joyCon2AutoConnect({ ...base, joycon2: { autoConnect: true } }),
    ).toBe(true);
    expect(
      joyCon2AutoConnect({ ...base, joycon2: { autoConnect: false } }),
    ).toBe(false);
  });

  it("writes false when it goes off and takes the object away when it goes on", () => {
    const off = setJoyCon2AutoConnect(base, false);
    expect(off).toEqual({ schemaVersion: 0, joycon2: { autoConnect: false } });
    const on = setJoyCon2AutoConnect(off as Config, true);
    expect(on).toEqual({ schemaVersion: 0 });
    expect(on && "joycon2" in on).toBe(false);
  });

  it("keeps what else the file holds, and changes nothing when it already is as asked", () => {
    const other: Config = {
      schemaVersion: 0,
      devices: { "k/p:x": { feedback: { led: false } } },
      joycon2: { autoConnect: true },
    };
    expect(setJoyCon2AutoConnect(other, false)).toEqual({
      schemaVersion: 0,
      devices: { "k/p:x": { feedback: { led: false } } },
      joycon2: { autoConnect: false },
    });
    // A member written as true is on, and goes.
    expect(setJoyCon2AutoConnect(other, true)).toEqual({
      schemaVersion: 0,
      devices: { "k/p:x": { feedback: { led: false } } },
    });
    expect(setJoyCon2AutoConnect(base, true)).toBeNull();
    expect(
      setJoyCon2AutoConnect(
        { ...base, joycon2: { autoConnect: false } },
        false,
      ),
    ).toBeNull();
  });
});
