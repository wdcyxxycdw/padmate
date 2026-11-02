import { describe, expect, it } from "vitest";
import { padFixture, padsFixture } from "../renderer/testing/pads-fixture";
import {
  isPadFunctionKey,
  parsePadsList,
  parsePadsMigrate,
  parsePadsSet,
} from "./pads-parse";

const pad = padFixture("joyConRight", { effective: "rightshoulder" });
const list = padsFixture([pad, padFixture("xbox", { connected: false })], {
  legacy: { feedback: true },
});

describe("pads.list", () => {
  it("is read as it is sent, JSON round trip included", () => {
    expect(parsePadsList(JSON.parse(JSON.stringify(list)))).toEqual(list);
  });

  it("keeps what the controller has stored apart from what applies", () => {
    const stored = padFixture("xbox", {
      feedback: {
        stored: { led: false },
        effective: { rumble: true, led: false, pause: true, lowBattery: true },
        source: "device",
      },
      panic: {
        default: { buttons: ["back", "start"], holdMs: 2000 },
        stored: { buttons: ["north", "south"], holdMs: 1500 },
        effective: { buttons: ["north", "south"], holdMs: 1500 },
        source: "device",
      },
    });
    const read = parsePadsList(padsFixture([stored]));
    expect(read?.pads[0].feedback.stored).toEqual({ led: false });
    expect(read?.pads[0].panic.stored?.holdMs).toBe(1500);
  });

  it.each([
    ["no object", 5],
    ["a revision that is no string", { ...list, revision: 1 }],
    ["pads that are no array", { ...list, pads: {} }],
    ["no legacy marks", { ...list, legacy: undefined }],
    [
      "a family it does not know",
      { ...list, pads: [{ ...pad, family: "gamecube" }] },
    ],
    [
      "a function key that is guide",
      {
        ...list,
        pads: [
          { ...pad, functionKey: { ...pad.functionKey, effective: "guide" } },
        ],
      },
    ],
    [
      "no function key options",
      {
        ...list,
        pads: [{ ...pad, functionKey: { ...pad.functionKey, options: [] } }],
      },
    ],
    [
      "a source it does not know",
      {
        ...list,
        pads: [{ ...pad, feedback: { ...pad.feedback, source: "file" } }],
      },
    ],
    [
      "a switch that is no boolean",
      {
        ...list,
        pads: [
          {
            ...pad,
            feedback: { ...pad.feedback, effective: { rumble: 1 } },
          },
        ],
      },
    ],
    [
      "a combo with a hold that is no number",
      {
        ...list,
        pads: [
          {
            ...pad,
            panic: {
              ...pad.panic,
              effective: { buttons: ["a", "b"], holdMs: "2000" },
            },
          },
        ],
      },
    ],
    ["a pad without a key", { ...list, pads: [{ ...pad, key: "" }] }],
  ])("is refused with %s", (_name, value) => {
    expect(parsePadsList(value)).toBeNull();
  });

  it("is refused with more controllers than the daemon lists", () => {
    const many = Array.from({ length: 257 }, (_, i) => ({
      ...pad,
      key: `k${i}`,
    }));
    expect(parsePadsList({ ...list, pads: many })).toBeNull();
  });
});

describe("pads.set and pads.migrate", () => {
  const saved = { revision: "rev-2", applied: true, restartRequired: true };

  it("read the saved state and the controller", () => {
    expect(parsePadsSet({ ...saved, pad })).toEqual({ ...saved, pad });
    expect(
      parsePadsSet({ ...saved, applied: false, problem: "x", pad }),
    ).toEqual({ ...saved, applied: false, problem: "x", pad });
    expect(parsePadsSet({ ...saved, pad: { ...pad, key: 3 } })).toBeNull();
    expect(parsePadsSet({ revision: 3, applied: true, pad })).toBeNull();
  });

  it("read the folded list", () => {
    const answer = {
      revision: "rev-2",
      applied: true,
      changed: true,
      legacy: list.legacy,
      pads: list.pads,
    };
    expect(parsePadsMigrate(answer)).toEqual(answer);
    expect(parsePadsMigrate({ ...answer, changed: "yes" })).toBeNull();
    expect(parsePadsMigrate({ ...answer, pads: null })).toBeNull();
  });

  it("know the buttons that can be a function key", () => {
    for (const key of [
      "start",
      "back",
      "leftshoulder",
      "rightshoulder",
      "lt",
      "rt",
    ])
      expect(isPadFunctionKey(key)).toBe(true);
    // A retired paddle is no function key any more.
    for (const key of ["guide", "north", "paddle1", "paddle4", "", null, 3])
      expect(isPadFunctionKey(key)).toBe(false);
  });
});
