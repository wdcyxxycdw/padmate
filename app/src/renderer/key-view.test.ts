import { describe, expect, it } from "vitest";
import type { ScriptRegistration } from "../shared/api";
import {
  effectiveView,
  viewName,
  viewOptions,
  viewRegistrations,
} from "./key-view";

const pad = (type: string, buttonMask?: number) => ({ type, buttonMask });

describe("the key set the page shows", () => {
  it("is the one asked for, if the pad has that function key", () => {
    expect(effectiveView("back", pad("xboxone"))).toBe("back");
    expect(effectiveView("start", pad("ps5"))).toBe("start");
    expect(effectiveView("normal", pad("ps5"))).toBe("normal");
  });

  it("is the usual one when the pad has no such function key", () => {
    // A single Joy-Con held sideways has no View, only Menu.
    expect(effectiveView("back", pad("joyconleft", 0xf))).toBe("normal");
    expect(effectiveView("start", pad("joyconleft", 0xf))).toBe("start");
  });
});

describe("naming the key sets", () => {
  it("follows the pad's own names for View and Menu", () => {
    const names = (type: string, language: "zh-CN" | "en") =>
      viewOptions(pad(type), language).map((option) => option.label);
    expect(names("xboxone", "zh-CN")).toEqual([
      "平时",
      "按住 View",
      "按住 Menu",
    ]);
    expect(names("xboxone", "en")).toEqual([
      "Normal",
      "Hold View",
      "Hold Menu",
    ]);
    expect(names("ps5", "zh-CN")).toEqual([
      "平时",
      "按住 Create",
      "按住 Options",
    ]);
    expect(names("ps4", "en")).toEqual([
      "Normal",
      "Hold Share",
      "Hold Options",
    ]);
    expect(names("ps3", "en")).toEqual(["Normal", "Hold Select", "Hold Start"]);
    expect(names("switchpro", "en")).toEqual(["Normal", "Hold −", "Hold +"]);
    expect(names("xbox360", "zh-CN")).toEqual([
      "平时",
      "按住 Back",
      "按住 Start",
    ]);
  });

  it("leaves out View on a single Joy-Con held sideways", () => {
    expect(
      viewOptions(pad("joyconright", 1), "zh-CN").map((option) => option.value),
    ).toEqual(["normal", "start"]);
    expect(
      viewOptions(pad("joyconright", 1), "en").map((option) => option.label),
    ).toEqual(["Normal", "Hold +"]);
  });

  it("names one view for a sentence", () => {
    expect(viewName("back", pad("ps5"), "zh-CN")).toBe("按住 Create");
    expect(viewName("start", pad("ps5"), "en")).toBe("Hold Options");
    expect(viewName("normal", pad("ps5"), "en")).toBe("Normal");
  });

  it("takes the names the pad reports", () => {
    expect(
      viewName(
        "back",
        { type: "xboxone", buttonLabels: { back: "Select" } },
        "en",
      ),
    ).toBe("Hold Select");
  });
});

describe("script registrations in a function key's set", () => {
  const registration = (scope: string): ScriptRegistration => ({
    id: 1,
    control: "south",
    gesture: "press",
    scope,
    observe: false,
    claim: false,
    priority: 0,
    timing: {},
  });
  const all = ["global", "base", "layer:base", "layer:alt"].map(registration);

  it("are all there in the usual view", () => {
    expect(viewRegistrations(all, "normal")).toBe(all);
  });

  it("are only the global ones in a set, which a script cannot register into", () => {
    expect(viewRegistrations(all, "back").map((entry) => entry.scope)).toEqual([
      "global",
    ]);
  });
});
