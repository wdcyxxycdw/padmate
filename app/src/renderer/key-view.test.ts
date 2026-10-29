import { describe, expect, it } from "vitest";
import type { ScriptRegistration } from "../shared/api";
import type { FunctionKey } from "./editor-model";
import {
  effectiveView,
  functionKeyName,
  viewName,
  viewOptions,
  viewRegistrations,
} from "./key-view";

const pad = (type: string, buttonMask?: number) => ({ type, buttonMask });

describe("the key set the page shows", () => {
  it("is the one asked for, if the pad has the function key", () => {
    expect(effectiveView("function", pad("xboxone"), "back")).toBe("function");
    expect(effectiveView("function", pad("ps5"), "start")).toBe("function");
    expect(effectiveView("function", pad("ps5"), "guide")).toBe("function");
    expect(effectiveView("normal", pad("ps5"), "start")).toBe("normal");
  });

  it("is the usual one when the pad has no such function key", () => {
    // A single Joy-Con held sideways has no View, only Menu and Home.
    expect(effectiveView("function", pad("joyconleft", 0xf), "back")).toBe(
      "normal",
    );
    expect(effectiveView("function", pad("joyconleft", 0xf), "start")).toBe(
      "function",
    );
    expect(effectiveView("function", pad("joyconleft", 0xf), "guide")).toBe(
      "function",
    );
  });
});

describe("naming the key sets", () => {
  const names = (
    type: string,
    language: "zh-CN" | "en",
    functionKey: FunctionKey,
  ) =>
    viewOptions(pad(type), language, functionKey).map((option) => option.label);

  it("says 按住功能键 whichever key it is and whatever the pad calls it (ADR-0076)", () => {
    for (const functionKey of ["start", "back", "guide"] as const)
      for (const type of ["xboxone", "ps5", "ps4", "switchpro", "xbox360"]) {
        expect(names(type, "zh-CN", functionKey)).toEqual([
          "平时",
          "按住功能键",
        ]);
        expect(names(type, "en", functionKey)).toEqual([
          "Normal",
          "Hold function key",
        ]);
      }
  });

  it("has the usual view and the function set, in that order", () => {
    expect(
      viewOptions(pad("xboxone"), "zh-CN", "start").map(
        (option) => option.value,
      ),
    ).toEqual(["normal", "function"]);
  });

  it("has only the usual view on a pad without the function key, so there is nothing to switch", () => {
    expect(
      viewOptions(pad("joyconright", 1), "zh-CN", "back").map(
        (option) => option.value,
      ),
    ).toEqual(["normal"]);
    expect(
      viewOptions(pad("joyconright", 1), "en", "start").map(
        (option) => option.label,
      ),
    ).toEqual(["Normal", "Hold function key"]);
  });

  it("names one view for a sentence", () => {
    expect(viewName("function", "zh-CN")).toBe("按住功能键");
    expect(viewName("function", "en")).toBe("Hold function key");
    expect(viewName("normal", "en")).toBe("Normal");
  });

  it("still takes the name the pad reports for the function key itself", () => {
    expect(
      functionKeyName("back", {
        type: "xboxone",
        buttonLabels: { back: "Select" },
      }),
    ).toBe("Select");
  });
});

describe("script registrations in the function set", () => {
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

  it("are only the global ones in the set, which a script cannot register into", () => {
    expect(
      viewRegistrations(all, "function").map((entry) => entry.scope),
    ).toEqual(["global"]);
  });
});
