import { describe, expect, it } from "vitest";
import type { DeviceEntry } from "../../shared/api";
import type { PadInfo } from "../../shared/pads";
import { padFixture, padsFixture } from "../testing/pads-fixture";
import { sdlPads } from "../testing/sdl-pads";
import {
  effectiveFunctionKey,
  padKind,
  padOf,
  padSetParams,
  padTitle,
  padWithChange,
  typeName,
} from "./pads-model";

const entry = (
  id: number,
  key: string,
  type: string,
  buttonMask = 0,
): DeviceEntry => ({
  info: {
    id,
    name: type,
    type,
    vendor: 0,
    product: 0,
    version: 0,
    guid: "",
    driver: "",
    path: "",
    serial: "",
    gamepad: true,
    virtual: false,
    buttonMask,
  },
  key,
  slot: 0,
  status: "mapped",
  disabled: false,
});

describe("the function key of a change", () => {
  const pad = padFixture("joyConRight");

  it("is the key chosen, and null for the default of the family, which takes the member out", () => {
    expect(padSetParams(pad, { functionKey: "paddle1" })).toEqual({
      key: pad.key,
      functionKey: "paddle1",
    });
    const chosen = padFixture("joyConRight", { effective: "paddle1" });
    expect(padSetParams(chosen, { functionKey: "paddle3" })).toEqual({
      key: pad.key,
      functionKey: null,
    });
  });

  it("is nothing when the controller already has the key", () => {
    expect(padSetParams(pad, { functionKey: "paddle3" })).toBeNull();
    expect(padSetParams(pad, {})).toBeNull();
  });
});

describe("the feedback switches of a change", () => {
  it("writes false for a switch that goes off, and only that member", () => {
    const pad = padFixture("xbox");
    expect(
      padSetParams(pad, { feedback: { name: "rumble", on: false } }),
    ).toEqual({ key: pad.key, feedback: { rumble: false } });
  });

  it("keeps the switches the controller has off, and takes the object away with the last", () => {
    const pad = padFixture("xbox", {
      feedback: {
        stored: { rumble: false, led: false },
        effective: { rumble: false, led: false, pause: true, lowBattery: true },
        source: "device",
      },
    });
    expect(
      padSetParams(pad, { feedback: { name: "lowBattery", on: false } }),
    ).toEqual({
      key: pad.key,
      feedback: { rumble: false, led: false, lowBattery: false },
    });
    expect(
      padSetParams(pad, { feedback: { name: "rumble", on: true } }),
    ).toEqual({ key: pad.key, feedback: { led: false } });
    const last = padFixture("xbox", {
      feedback: {
        stored: { led: false },
        effective: { rumble: true, led: false, pause: true, lowBattery: true },
        source: "device",
      },
    });
    expect(padSetParams(last, { feedback: { name: "led", on: true } })).toEqual(
      {
        key: pad.key,
        feedback: null,
      },
    );
  });

  it("is nothing when the switch already is as asked", () => {
    const pad = padFixture("xbox");
    expect(
      padSetParams(pad, { feedback: { name: "pause", on: true } }),
    ).toBeNull();
  });
});

describe("the emergency combo of a change", () => {
  const combo = { buttons: ["south", "east"], holdMs: 3000 };

  it("sets the combo, once", () => {
    const pad = padFixture("xbox");
    expect(padSetParams(pad, { panic: combo })).toEqual({
      key: pad.key,
      panic: combo,
    });
    const has = padFixture("xbox", {
      panic: {
        default: pad.panic.default,
        stored: combo,
        effective: combo,
        source: "device",
      },
    });
    expect(padSetParams(has, { panic: { ...combo } })).toBeNull();
  });

  it("goes back to the default only while the controller has no default one", () => {
    const pad = padFixture("xbox");
    expect(padSetParams(pad, { panic: null })).toBeNull();
    const has = padFixture("xbox", {
      panic: {
        default: pad.panic.default,
        stored: combo,
        effective: combo,
        source: "device",
      },
    });
    expect(padSetParams(has, { panic: null })).toEqual({
      key: pad.key,
      panic: null,
    });
    // The retired global combo applies: that has something to take away too.
    const legacy = padFixture("xbox", {
      panic: {
        default: pad.panic.default,
        effective: combo,
        source: "legacy",
      },
    });
    expect(padSetParams(legacy, { panic: null })).toEqual({
      key: pad.key,
      panic: null,
    });
  });
});

describe("a change as shown while it is saved", () => {
  it("is the controller with the setting changed", () => {
    const pad = padFixture("joyConRight");
    const shown = padWithChange(pad, {
      functionKey: "paddle1",
      feedback: { name: "led", on: false },
      panic: { buttons: ["a", "b"], holdMs: 1000 },
    });
    expect(shown.functionKey.effective).toBe("paddle1");
    expect(shown.feedback.effective.led).toBe(false);
    expect(shown.feedback.effective.rumble).toBe(true);
    expect(shown.panic.effective).toEqual({
      buttons: ["a", "b"],
      holdMs: 1000,
    });
    // Back to the default shows the default.
    expect(padWithChange(shown, { panic: null }).panic.effective).toEqual(
      pad.panic.default,
    );
    expect(pad.functionKey.effective).toBe("paddle3");
  });
});

describe("the function key the mapping page edits", () => {
  const sideways = sdlPads.joyConRightSideways;

  it("is what the daemon says for the controller, by its connection and by its key", () => {
    const pad = padFixture("joyConRight", {
      effective: "paddle1",
      device: 7,
      key: "joycon-key",
    });
    const pads = padsFixture([pad]);
    expect(
      effectiveFunctionKey(
        pads,
        entry(7, "another", sideways.type, sideways.buttonMask),
      ),
    ).toBe("paddle1");
    expect(
      effectiveFunctionKey(
        pads,
        entry(9, "joycon-key", sideways.type, sideways.buttonMask),
      ),
    ).toBe("paddle1");
    expect(padOf(pads, entry(1, "x", "xboxone"))).toBeUndefined();
  });

  it("is the default of its kind before the list is read", () => {
    const at = (type: string, mask?: number) =>
      effectiveFunctionKey(null, entry(1, "k", type, mask));
    expect(at("xboxone")).toBe("start");
    expect(at(sideways.type, sideways.buttonMask)).toBe("paddle3");
    expect(
      at(
        sdlPads.joyConLeftSideways.type,
        sdlPads.joyConLeftSideways.buttonMask,
      ),
    ).toBe("paddle4");
    expect(at("joyconpair", sdlPads.joyConPair.buttonMask)).toBe("back");
    expect(
      at(sdlPads.joyConLeftUpright.type, sdlPads.joyConLeftUpright.buttonMask),
    ).toBe("back");
  });

  it("is Menu with no controller", () => {
    expect(effectiveFunctionKey(padsFixture([]), undefined)).toBe("start");
  });
});

describe("naming a controller", () => {
  const named = (overrides: Partial<PadInfo>) => padFixture("xbox", overrides);

  it("is its own name, else its kind", () => {
    expect(padTitle(named({ name: "Xbox Wireless Controller" }), "zh-CN")).toBe(
      "Xbox Wireless Controller",
    );
    expect(padTitle(named({ name: " " }), "zh-CN")).toBe("Xbox");
    expect(
      padTitle(named({ name: "", type: "unknown", known: false }), "zh-CN"),
    ).toBe("通用手柄");
    expect(
      padTitle(named({ name: "", type: "unknown", known: false }), "en"),
    ).toBe("Generic controller");
  });

  it("says the kind in words, the grip of a single Joy-Con too", () => {
    for (const [type, zh, en] of [
      ["xbox360", "Xbox 360", "Xbox 360"],
      ["xboxone", "Xbox", "Xbox"],
      ["ps5", "PlayStation 5", "PlayStation 5"],
      ["switchpro", "Switch Pro", "Switch Pro"],
      ["gamecube", "GameCube", "GameCube"],
      ["joyconpair", "Joy-Con（双）", "Joy-Con pair"],
    ] as const) {
      expect(typeName(type, "zh-CN")).toBe(zh);
      expect(typeName(type, "en")).toBe(en);
    }
    expect(padKind(padFixture("joyConRight"), "zh-CN")).toBe(
      "Joy-Con（右，横握）",
    );
    expect(padKind(padFixture("joyConLeft"), "en")).toBe(
      "Joy-Con (L, sideways)",
    );
    expect(
      padKind(padFixture("joyConLeft", { family: "joyconLeftVertical" }), "en"),
    ).toBe("Joy-Con (L, upright)");
    expect(padKind(padFixture("joyConPair"), "en")).toBe("Joy-Con pair");
  });
});
