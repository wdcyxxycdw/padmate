import { describe, expect, it } from "vitest";
import type { Profile } from "../shared/generated/schema";
import {
  controllerChips,
  inheritedChips,
  relatedKeys,
} from "./controller-chips";
import { controllerLabels } from "./controller-model";
import type { LabelDisplay } from "./store";

// The binding keys a chip shows, and the chips of the sequence and text
// actions (MAP-22, OUT-21), which the editor sets now. A chip says nothing of
// where a binding comes from (PRO-19, ADR-0076).

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

function chips(display: LabelDisplay = "actions", value: Profile = profile) {
  return controllerChips({
    profile: value,
    controls,
    labels,
    registrations: [],
    display,
    language: "zh-CN",
    functionKey: "start",
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

describe("a chip", () => {
  it("looks like any other bound chip and carries no mark of where its binding comes from", () => {
    const result = chips();
    expect(result.south).toMatchObject({
      state: "bound",
      title: "Ctrl+Z",
      detail: [],
    });
    for (const chip of Object.values(result))
      expect("fromParent" in chip).toBe(false);
  });
  it("is not made for the function key, which holds nothing", () => {
    const result = chips();
    expect(result.start.state).toBe("function");
    // The other two of View, Menu and the Xbox button are ordinary keys.
    expect(result.back.state).toBe("unbound");
    expect(result.guide.state).toBe("unbound");
  });
  it("is the usual chip, greyed, where the function set falls back on it", () => {
    const empty = controllerChips({
      profile: { ...profile, layers: { base: {} } },
      controls,
      labels,
      registrations: [],
      display: "actions",
      language: "zh-CN",
      functionKey: "start",
    });
    const result = inheritedChips({
      own: empty,
      normal: chips(),
      language: "zh-CN",
    });
    expect(result.south.inherited).toBe(true);
    expect(result.south.state).toBe("unbound");
    expect("fromParent" in result.south).toBe(false);
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
    const result = chips("actions", value);
    expect(result.south.state).toBe("bound");
    expect(result.east.state).toBe("bound");
  });
  it("say what they do", () => {
    const result = chips("actions", value);
    expect(result.south.title).toBe("3 步序列");
    expect(result.east.title).toBe("输入文字「Best regards, Ki…」");
  });
});
