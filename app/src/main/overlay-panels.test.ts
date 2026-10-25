import { describe, expect, it } from "vitest";
import type { DaemonEvent } from "../shared/api";
import type { OverlayPanelsMessage, ScriptPanel } from "../shared/overlay";
import { firstMappedSlot } from "./overlay-hints";
import {
  OverlayPanels,
  observePanels,
  type PanelsHost,
} from "./overlay-panels";

interface SlotInit {
  slot: number;
  device?: number;
  panels?: unknown;
}

// One entry of engine.activity's slots, as the daemon writes it; panels only
// while the slot's script keeps any (SCR-22).
function slot({ slot, device = 7 + slot, panels }: SlotInit) {
  return {
    slot,
    device,
    key: `pad-${slot}`,
    name: "Pad",
    profile: "绘画",
    generation: 1,
    layers: { base: "base", overlays: [], color: "#ffffff" },
    latches: [],
    mode: 0,
    ...(panels === undefined ? {} : { panels }),
  };
}

function state(...slots: SlotInit[]): DaemonEvent {
  return {
    topic: "engine.activity",
    data: {
      kind: "state",
      paused: false,
      capturing: false,
      suspended: "",
      slots: slots.map(slot),
    },
  };
}

// A panel as the daemon lists it.
const brush = {
  id: "brush",
  title: "画笔",
  rows: [
    ["大小", "12"],
    ["硬度", "80"],
  ],
};
const brushPanel: ScriptPanel = {
  title: "画笔",
  rows: [
    ["大小", "12"],
    ["硬度", "80"],
  ],
};
const layers = { id: "layers", rows: [["图层", "3"]] };
const layersPanel: ScriptPanel = { rows: [["图层", "3"]] };

describe("the first mapped slot of a state event", () => {
  it("is the lowest index among the slots that have a device", () => {
    expect(
      firstMappedSlot(state({ slot: 2 }, { slot: 1 }, { slot: 0, device: 0 }))
        ?.slot,
    ).toBe(1);
  });

  it("is null when there is none, undefined when the event is not a state event", () => {
    for (const slots of [undefined, [], "none", [null], [{ slot: "0" }]])
      expect(
        firstMappedSlot({
          topic: "engine.activity",
          data: { kind: "state", slots },
        }),
      ).toBeNull();
    expect(firstMappedSlot(state({ slot: 0, device: 0 }))).toBeNull();
    expect(
      firstMappedSlot({ topic: "engine.activity", data: { kind: "decision" } }),
    ).toBeUndefined();
    expect(firstMappedSlot({ topic: "log", data: {} })).toBeUndefined();
  });
});

describe("the panels in a state event", () => {
  it("are the first mapped slot's list", () => {
    expect(observePanels(state({ slot: 0, panels: [brush, layers] }))).toEqual([
      brushPanel,
      layersPanel,
    ]);
  });

  it("follow the lowest slot that has a device, wherever it is in the list", () => {
    expect(
      observePanels(
        state(
          { slot: 0, device: 0, panels: [layers] },
          { slot: 2, panels: [layers] },
          { slot: 1, panels: [brush] },
        ),
      ),
    ).toEqual([brushPanel]);
  });

  it("are none when the first slot has none, even if a later slot has", () => {
    expect(
      observePanels(state({ slot: 0 }, { slot: 1, panels: [brush] })),
    ).toEqual([]);
  });

  it("are none for an empty list, or one that is not a list", () => {
    for (const panels of [[], null, "brush", { id: "brush" }, 3])
      expect(observePanels(state({ slot: 0, panels }))).toEqual([]);
  });

  it("are none in a state event without a usable slot, so that a panel does not stay up", () => {
    for (const slots of [undefined, [], "none", [null], [{ slot: "0" }]])
      expect(
        observePanels({
          topic: "engine.activity",
          data: { kind: "state", slots },
        }),
      ).toEqual([]);
  });

  it("leave out what is not well-formed, and keep the rest", () => {
    expect(
      observePanels(
        state({
          slot: 0,
          panels: [
            null,
            "x",
            { rows: [["no id", "x"]] },
            { id: "a" },
            { id: "b", rows: [["only one"]] },
            brush,
            { id: "brush", rows: [["again", "x"]] },
          ],
        }),
      ),
    ).toEqual([brushPanel]);
  });

  it("keep four panels at most, sixteen rows each, the texts cut", () => {
    const panels = ["a", "b", "c", "d", "e"].map((id) => ({
      id,
      title: "题".repeat(60),
      rows: Array.from({ length: 30 }, (_, i) => [`l${i}`, "x".repeat(60)]),
    }));
    const seen = observePanels(state({ slot: 0, panels })) ?? [];
    expect(seen).toHaveLength(4);
    for (const panel of seen) {
      expect(panel.rows).toHaveLength(16);
      expect(Array.from(panel.title ?? "")).toHaveLength(40);
      expect(Array.from(panel.rows[0][1])).toHaveLength(40);
    }
  });

  it("are not found in other events", () => {
    expect(
      observePanels({ topic: "engine.activity", data: { kind: "decision" } }),
    ).toBeUndefined();
    expect(observePanels({ topic: "log", data: {} })).toBeUndefined();
    expect(
      observePanels({ topic: "hud", data: { kind: "toast" } }),
    ).toBeUndefined();
    expect(
      observePanels({ topic: "engine.activity", data: null } as DaemonEvent),
    ).toBeUndefined();
  });
});

describe("OverlayPanels", () => {
  function setup() {
    const shown: Array<Omit<OverlayPanelsMessage, "position">> = [];
    const host: PanelsHost = {
      show: (message) => {
        shown.push(message);
        return true;
      },
    };
    return { panels: new OverlayPanels(host), shown };
  }

  it("shows the panels when a script has them, once, and hides them when they are not in the events any more", () => {
    const { panels, shown } = setup();
    panels.handle(state({ slot: 0 }));
    expect(shown).toEqual([]);
    panels.handle(state({ slot: 0, panels: [brush] }));
    expect(shown).toEqual([{ panels: [brushPanel] }]);
    // The same panels again, as in each of the next state events: nothing.
    panels.handle(state({ slot: 0, panels: [brush] }));
    panels.handle(state({ slot: 0, panels: [brush] }));
    expect(shown).toHaveLength(1);
    panels.handle(state({ slot: 0 }));
    expect(shown).toHaveLength(2);
    expect(shown[1]).toEqual({ panels: [] });
    // Hidden once; and again later they are shown again.
    panels.handle(state({ slot: 0 }));
    expect(shown).toHaveLength(2);
    panels.handle(state({ slot: 0, panels: [brush] }));
    expect(shown).toHaveLength(3);
    expect(shown[2].panels).toEqual([brushPanel]);
  });

  it("sends changed panels: a value, a row, another panel, the order", () => {
    const { panels, shown } = setup();
    const withSize = (size: string) => ({
      ...brush,
      rows: [["大小", size], ...brush.rows.slice(1)],
    });
    panels.handle(state({ slot: 0, panels: [withSize("12")] }));
    panels.handle(state({ slot: 0, panels: [withSize("14")] }));
    panels.handle(state({ slot: 0, panels: [withSize("14"), layers] }));
    panels.handle(state({ slot: 0, panels: [layers, withSize("14")] }));
    expect(shown.map((m) => m.panels.length)).toEqual([1, 1, 2, 2]);
    expect(shown[1].panels[0].rows[0]).toEqual(["大小", "14"]);
    expect(shown[3].panels[0]).toEqual(layersPanel);
  });

  it("does not resend panels that differ only in what is not sent (an id)", () => {
    const { panels, shown } = setup();
    panels.handle(state({ slot: 0, panels: [brush] }));
    panels.handle(state({ slot: 0, panels: [{ ...brush, id: "other" }] }));
    expect(shown).toHaveLength(1);
  });

  it("changes nothing for events that are not state events", () => {
    const { panels, shown } = setup();
    panels.handle(state({ slot: 0, panels: [brush] }));
    panels.handle({
      topic: "engine.activity",
      data: { kind: "decision", slot: 0 },
    });
    panels.handle({ topic: "log", data: {} });
    panels.handle({
      topic: "hud",
      data: { kind: "toast", slot: 0, text: "x", ms: 1500 },
    });
    expect(shown).toHaveLength(1);
  });

  it("hides when the first slot goes away, or another slot takes its place without panels", () => {
    const { panels, shown } = setup();
    panels.handle(state({ slot: 0, panels: [brush] }));
    panels.handle(state({ slot: 1 }));
    expect(shown.at(-1)).toEqual({ panels: [] });
    panels.handle(state({ slot: 0, panels: [brush] }));
    panels.handle(state());
    expect(shown.at(-1)).toEqual({ panels: [] });
  });

  it("hides when the connection to the daemon ends, and shows again when they come back", () => {
    const { panels, shown } = setup();
    panels.handle(state({ slot: 0, panels: [brush] }));
    panels.clear();
    expect(shown.at(-1)).toEqual({ panels: [] });
    // Nothing more to hide.
    panels.clear();
    expect(shown).toHaveLength(2);
    // The daemon, started again, lists them again: they are shown again.
    panels.handle(state({ slot: 0, panels: [brush] }));
    expect(shown).toHaveLength(3);
    expect(shown[2].panels).toEqual([brushPanel]);
  });

  it("does not need the toast switch, the focus of the main window or anything but the events", () => {
    const { panels, shown } = setup();
    panels.handle(state({ slot: 0, panels: [brush] }));
    expect(shown).toHaveLength(1);
  });

  it("gives them again with the next event while there is no overlay to give them to", () => {
    const shown: Array<Omit<OverlayPanelsMessage, "position">> = [];
    let overlay = false;
    const panels = new OverlayPanels({
      show: (message) => {
        if (!overlay) return false;
        shown.push(message);
        return true;
      },
    });
    panels.handle(state({ slot: 0, panels: [brush] }));
    panels.handle(state({ slot: 0, panels: [brush] }));
    expect(shown).toEqual([]);
    // The overlay is made: the next state event brings them.
    overlay = true;
    panels.handle(state({ slot: 0, panels: [brush] }));
    expect(shown).toEqual([{ panels: [brushPanel] }]);
    panels.handle(state({ slot: 0, panels: [brush] }));
    expect(shown).toHaveLength(1);
    // And a hide with no overlay is tried again too.
    overlay = false;
    panels.handle(state({ slot: 0 }));
    overlay = true;
    panels.handle(state({ slot: 0 }));
    expect(shown.at(-1)).toEqual({ panels: [] });
  });

  it("does not throw when its host does", () => {
    const panels = new OverlayPanels({
      show: () => {
        throw new Error("overlay gone");
      },
    });
    expect(() =>
      panels.handle(state({ slot: 0, panels: [brush] })),
    ).not.toThrow();
    expect(() => panels.handle(state({ slot: 0 }))).not.toThrow();
    expect(() => panels.clear()).not.toThrow();
  });
});
