import { describe, expect, it } from "vitest";
import type { MappingResult } from "../shared/api";
import type { Config } from "../shared/generated/schema";
import {
  elementName,
  hasCustomMapping,
  liveElement,
  mappingFailureText,
  mappingNotices,
  padSelector,
  stepInfo,
} from "./mapping-model";
import { DEFAULT_STEPS } from "./mapwizard";
import { padIcons } from "./pad-icon";

const config = (devices: Config["devices"]): Config =>
  ({ schemaVersion: 0, devices }) as Config;

describe("custom mappings in config.json (IN-18)", () => {
  it("knows a controller with a mapping saved under its key", () => {
    const saved = config({
      "030000007e05/p:x": { mapping: "ff,*,a:b0,platform:Windows" },
      "030000007e05/p:y": { calibration: { ls: { deadzone: 0.2 } } },
      "030000007e05/p:z": { mapping: "" },
    });
    expect(hasCustomMapping(saved, "030000007e05/p:x")).toBe(true);
    expect(hasCustomMapping(saved, "030000007e05/p:y")).toBe(false);
    expect(hasCustomMapping(saved, "030000007e05/p:z")).toBe(false);
    expect(hasCustomMapping(saved, "other")).toBe(false);
    expect(hasCustomMapping(config(undefined), "x")).toBe(false);
    expect(hasCustomMapping(null, "x")).toBe(false);
  });

  it("selects a controller by its connection ID, which a key can't (all raw joysticks of a model share one)", () => {
    expect(
      padSelector({ info: { id: 12 }, key: "030000007e05/p:x" } as never),
    ).toBe("12");
  });
});

describe("what each prompt of the wizard shows", () => {
  it("names every step in both languages, with a hint and a picture", () => {
    for (const step of DEFAULT_STEPS)
      for (const language of ["zh-CN", "en"] as const) {
        const info = stepInfo(step.id, language);
        expect(info.name, `${step.id} ${language}`).toBeTruthy();
        expect(info.prompt).toBeTruthy();
        expect(info.hint).toBeTruthy();
        // Every picture is a vendored icon (ADR-0079 §7).
        expect(info.icon.kind).toBe("art");
        if (info.icon.kind === "art")
          expect(padIcons, step.id).toHaveProperty([info.icon.name]);
      }
    expect(() => stepInfo("nope", "en")).toThrow();
  });

  it("says where the face buttons are, so that a pad with other letters can be mapped", () => {
    expect(stepInfo("a", "zh-CN")).toMatchObject({
      name: "A",
      prompt: "按下 A 键",
      hint: "右侧四个键中下面的那个，按一下再松开",
      icon: { kind: "art", name: "kenney/xbox_button_a", face: "green" },
    });
    expect(stepInfo("y", "en")).toMatchObject({
      name: "Y",
      prompt: "Press Y",
      hint: "The top one of the four buttons on the right. Press and release",
    });
  });

  it("asks a stick to be pushed out and brought back", () => {
    expect(stepInfo("ls-left", "zh-CN")).toMatchObject({
      name: "左摇杆向左",
      prompt: "把左摇杆推到最左，再放回中间",
      icon: { kind: "art", name: "kenney/xbox_stick_l_left" },
    });
    expect(stepInfo("rs-down", "en")).toMatchObject({
      prompt: "Push the right stick all the way down, then back to the middle",
      icon: { kind: "art", name: "kenney/xbox_stick_r_down" },
    });
    expect(stepInfo("ls-click", "en").icon).toEqual({
      kind: "art",
      name: "kenney/xbox_stick_l_press",
    });
  });

  it("draws the d-pad direction, the shoulders and the middle buttons", () => {
    expect(stepInfo("dpleft", "en").icon).toEqual({
      kind: "art",
      name: "kenney/xbox_dpad_left",
    });
    expect(stepInfo("lt", "en").icon).toEqual({
      kind: "art",
      name: "kenney/xbox_lt",
    });
    expect(stepInfo("rb", "en").icon).toEqual({
      kind: "art",
      name: "kenney/xbox_rb",
    });
    // The middle buttons carry the symbol printed on them (ADR-0077).
    expect(stepInfo("view", "zh-CN")).toMatchObject({
      name: "View（返回键）",
      icon: { kind: "art", name: "kenney/xbox_button_view" },
    });
    expect(stepInfo("menu", "en").icon).toEqual({
      kind: "art",
      name: "kenney/xbox_button_menu",
    });
    expect(stepInfo("home", "en").icon).toEqual({
      kind: "art",
      name: "kenney/switch_button_home",
    });
    // Home may be missing: it can be skipped.
    expect(stepInfo("home", "zh-CN").hint).toContain("跳过");
  });

  it("says that View and Menu are for the emergency pause, and does not call them the function key (ADR-0075: the function key is one of three, chosen in Settings)", () => {
    for (const id of ["view", "menu"]) {
      expect(stepInfo(id, "zh-CN").hint).toContain("用于紧急暂停");
      expect(stepInfo(id, "zh-CN").hint).not.toContain("功能键");
      expect(stepInfo(id, "en").hint).toContain("emergency pause");
      expect(stepInfo(id, "en").hint).not.toMatch(/function key/i);
    }
  });
});

describe("how an element is named", () => {
  it("names buttons, hat directions and axis directions as SDL counts them", () => {
    expect(
      elementName(
        { kind: "button", index: 3, mask: 0, rest: 0, far: 0 },
        "zh-CN",
      ),
    ).toBe("按钮 3");
    expect(
      elementName({ kind: "hat", index: 0, mask: 1, rest: 0, far: 0 }, "en"),
    ).toBe("Hat 0 ↑");
    expect(
      elementName({ kind: "hat", index: 1, mask: 2, rest: 0, far: 0 }, "zh-CN"),
    ).toBe("帽子 1 →");
    expect(
      elementName({ kind: "hat", index: 1, mask: 4, rest: 0, far: 0 }, "en"),
    ).toBe("Hat 1 ↓");
    expect(
      elementName({ kind: "hat", index: 1, mask: 8, rest: 0, far: 0 }, "en"),
    ).toBe("Hat 1 ←");
    expect(
      elementName(
        { kind: "axis", index: 2, mask: 0, rest: 0, far: 30000 },
        "zh-CN",
      ),
    ).toBe("轴 2 +");
    expect(
      elementName(
        { kind: "axis", index: 4, mask: 0, rest: -32768, far: 0 },
        "en",
      ),
    ).toBe("Axis 4 +");
    expect(
      elementName(
        { kind: "axis", index: 4, mask: 0, rest: 0, far: -30000 },
        "en",
      ),
    ).toBe("Axis 4 −");
  });
});

describe("what a raw event shows while a step waits", () => {
  const rest = [0, 0, -32768];
  it("shows a button while it is down, and nothing when it is released", () => {
    expect(
      liveElement({ kind: "button", index: 3, value: 1 }, rest, "zh-CN"),
    ).toEqual({ key: "button:3", text: "按钮 3" });
    expect(
      liveElement({ kind: "button", index: 3, value: 0 }, rest, "zh-CN"),
    ).toEqual({ key: "button:3", text: null });
  });

  it("shows a hat in a direction and an axis that is well away from its rest", () => {
    expect(
      liveElement({ kind: "hat", index: 0, value: 8 }, rest, "en"),
    ).toEqual({ key: "hat:0", text: "Hat 0 ←" });
    expect(
      liveElement({ kind: "hat", index: 0, value: 0 }, rest, "en"),
    ).toEqual({ key: "hat:0", text: null });
    expect(
      liveElement({ kind: "axis", index: 1, value: 20000 }, rest, "en"),
    ).toEqual({ key: "axis:1", text: "Axis 1 +" });
    expect(
      liveElement({ kind: "axis", index: 1, value: -20000 }, rest, "en"),
    ).toEqual({ key: "axis:1", text: "Axis 1 −" });
    // Within half of the wizard's excursion, or the jitter of a stick at rest.
    expect(
      liveElement({ kind: "axis", index: 1, value: 3000 }, rest, "en"),
    ).toEqual({ key: "axis:1", text: null });
    // A trigger that rests at -32768 is measured from there.
    expect(
      liveElement({ kind: "axis", index: 2, value: -32000 }, rest, "en"),
    ).toEqual({ key: "axis:2", text: null });
    expect(
      liveElement({ kind: "axis", index: 2, value: 0 }, rest, "en"),
    ).toEqual({ key: "axis:2", text: "Axis 2 +" });
  });
});

describe("what to tell after a mapping is saved", () => {
  const result = (over: Partial<MappingResult> = {}): MappingResult => ({
    device: "k",
    guid: "ff",
    mapping: "ff,*,a:b0,platform:Windows",
    keys: ["k"],
    changed: true,
    applied: true,
    notes: [],
    warnings: [],
    ...over,
  });

  it("has nothing to say when all is well", () => {
    expect(mappingNotices(result(), "zh-CN")).toEqual([]);
  });

  it("says in one line that View and Menu are missing, and in one each otherwise", () => {
    const both = mappingNotices(
      result({ warnings: [{ code: "noBack" }, { code: "noStart" }] }),
      "zh-CN",
    );
    expect(both).toEqual([
      "没有映射 View 和 Menu。它们是紧急暂停的按键，缺了它们这只手柄无法用紧急暂停。",
    ]);
    expect(
      mappingNotices(result({ warnings: [{ code: "noStart" }] }), "zh-CN"),
    ).toEqual([
      "没有映射 Menu。它是紧急暂停的按键，缺了它这只手柄无法用紧急暂停。",
    ]);
    expect(
      mappingNotices(result({ warnings: [{ code: "noBack" }] }), "en"),
    ).toEqual([
      "View is not mapped. It is a button of the emergency pause, so without it this controller cannot use it.",
    ]);
    expect(
      mappingNotices(
        result({ warnings: [{ code: "noBack" }, { code: "noStart" }] }),
        "en",
      ),
    ).toEqual([
      "View and Menu are not mapped. They are the buttons of the emergency pause, so without them this controller cannot use it.",
    ]);
  });

  it("says when SDL reads another mapping or still reads a joystick", () => {
    expect(
      mappingNotices(
        result({
          warnings: [
            { code: "shadowed", device: 1 },
            { code: "shadowed", device: 2 },
            { code: "notGamepad", device: 3 },
          ],
        }),
        "zh-CN",
      ),
    ).toEqual([
      "SDL 读到的是另一份映射（可能来自环境变量 SDL_GAMECONTROLLERCONFIG），刚保存的映射没有生效。",
      "SDL 仍把它当作普通摇杆，映射没有生效。请至少映射一个按钮或轴，再试一次。",
    ]);
    expect(
      mappingNotices(result({ warnings: [{ code: "shadowed" }] }), "en"),
    ).toEqual([
      "SDL reads another mapping for this controller, probably from the SDL_GAMECONTROLLERCONFIG environment variable, so the mapping just saved is not in effect.",
    ]);
    expect(
      mappingNotices(result({ warnings: [{ code: "notGamepad" }] }), "en"),
    ).toEqual([
      "SDL still reads this controller as a plain joystick, so the mapping is not in effect. Map at least one button or axis and try again.",
    ]);
  });

  it("says when the mapping is saved but was not applied", () => {
    expect(
      mappingNotices(result({ applied: false, problem: "bad" }), "zh-CN"),
    ).toEqual(["已保存，但还没有生效：bad"]);
    expect(mappingNotices(result({ applied: false }), "en")).toEqual([
      "Saved, but not in effect yet.",
    ]);
  });

  it("ignores a warning it does not know", () => {
    expect(
      mappingNotices(result({ warnings: [{ code: "later" }] }), "en"),
    ).toEqual([]);
  });
});

describe("what a failure says", () => {
  it("names the controller that has gone, and the input that cannot", () => {
    expect(mappingFailureText({ code: -32004 }, "start", "zh-CN")).toBe(
      "手柄已断开。",
    );
    expect(mappingFailureText({ code: -32004 }, "save", "en")).toBe(
      "The controller is no longer connected.",
    );
    expect(mappingFailureText({ code: -32026 }, "start", "zh-CN")).toBe(
      "无法读取这只手柄的原始输入。",
    );
    expect(mappingFailureText({ code: -32026 }, "save", "en")).toBe(
      "This input does not support gamepad mappings.",
    );
    expect(mappingFailureText({ code: -32025 }, "save", "zh-CN")).toBe(
      "这份映射无法保存。",
    );
  });

  it("points to the log for the rest", () => {
    expect(mappingFailureText({}, "start", "zh-CN")).toBe(
      "无法读取手柄的原始输入，原因见“设置 → 日志”。",
    );
    expect(mappingFailureText({}, "save", "zh-CN")).toBe(
      "映射没有保存，原因见“设置 → 日志”。",
    );
    expect(mappingFailureText({}, "reset", "en")).toBe(
      "The default mapping was not restored. See Settings → Logs.",
    );
    expect(mappingFailureText({}, "start", "en")).toBe(
      "The controller's raw input could not be read. See Settings → Logs.",
    );
  });
});
