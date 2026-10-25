import { describe, expect, it } from "vitest";
import type { Profile } from "../shared/generated/schema";
import {
  controllerChips,
  inheritedChips,
  relatedKeys,
} from "./controller-chips";
import { controllerLabels } from "./controller-model";
import type { LabelDisplay } from "./store";

// Chips of a profile that takes bindings from its parent (PRO-19), and of the
// sequence and text actions (MAP-22, OUT-21), which the editor sets now.

const labels = controllerLabels("xboxone");
const controls = Object.keys(labels);

const profile: Profile = {
  schemaVersion: 1,
  name: "Child",
  layers: {
    base: {
      south: { tap: { type: "keys", keys: "Ctrl+Z" } },
      east: { tap: { type: "keys", keys: "Ctrl+Y" } },
    },
  },
};

function chips(
  fromParent?: (keys: string[]) => string | undefined,
  display: LabelDisplay = "actions",
  value: Profile = profile,
) {
  return controllerChips({
    profile: value,
    controls,
    labels,
    registrations: [],
    display,
    language: "zh-CN",
    fromParent,
  });
}

describe("relatedKeys", () => {
  it("lists the binding keys a chip shows", () => {
    expect(relatedKeys("south")).toEqual(["south"]);
    expect(relatedKeys("lefttrigger")).toEqual([
      "lt",
      "lt.full",
      "lefttrigger+",
      "lefttrigger-",
    ]);
    expect(relatedKeys("leftstick")).toContain("ls.up");
    expect(relatedKeys("leftstick")[0]).toBe("leftstick");
  });
});

describe("a chip whose binding comes from the parent", () => {
  it("carries the name of the profile the parent supplied it from", () => {
    const result = chips((keys) => (keys[0] === "south" ? "Paint" : undefined));
    expect(result.south.fromParent).toBe("Paint");
    // The chip itself looks like any other bound chip.
    expect(result.south).toMatchObject({
      state: "bound",
      title: "Ctrl+Z",
      detail: [],
    });
    expect(result.east.fromParent).toBeUndefined();
    expect("fromParent" in result.east).toBe(false);
  });
  it("is asked with every binding key of the chip", () => {
    const asked: string[][] = [];
    chips((keys) => {
      asked.push(keys);
      return undefined;
    });
    expect(asked).toContainEqual(["south"]);
    expect(asked).toContainEqual(relatedKeys("leftstick"));
  });
  it("is not asked about View and Menu, which hold nothing", () => {
    const asked: string[][] = [];
    chips((keys) => {
      asked.push(keys);
      return undefined;
    });
    expect(asked.flat()).not.toContain("back");
    expect(asked.flat()).not.toContain("start");
  });
  it("shows no mark on a profile without a parent", () => {
    for (const chip of Object.values(chips()))
      expect(chip.fromParent).toBe(undefined);
  });
  it("is dropped from the greyed chip a function key's set falls back on", () => {
    const marked = chips((keys) => (keys[0] === "south" ? "Paint" : undefined));
    const empty = controllerChips({
      profile: { ...profile, layers: { base: {} } },
      controls,
      labels,
      registrations: [],
      display: "actions",
      language: "zh-CN",
    });
    const result = inheritedChips({
      own: empty,
      normal: marked,
      display: "actions",
      language: "zh-CN",
    });
    expect(result.south.inherited).toBe(true);
    expect(result.south.fromParent).toBeUndefined();
  });
});

describe("chips of the sequence and text actions", () => {
  const value: Profile = {
    ...profile,
    layers: {
      base: {
        south: {
          tap: {
            type: "sequence",
            steps: [{ keys: "Ctrl+A" }, { wait: 100 }, { text: "你好" }],
          },
        },
        east: { tap: { type: "text", text: "Best regards,\nKim" } },
      },
    },
  };
  it("are not locked: the editor sets them (UI-31)", () => {
    const result = chips(undefined, "actions", value);
    expect(result.south.state).toBe("bound");
    expect(result.east.state).toBe("bound");
  });
  it("say what they do", () => {
    const result = chips(undefined, "actions", value);
    expect(result.south.title).toBe("3 步序列");
    expect(result.east.title).toBe("输入文字「Best regards, Ki…」");
  });
});
