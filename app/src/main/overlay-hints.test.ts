import { describe, expect, it, vi } from "vitest";
import type { DaemonEvent } from "../shared/api";
import type {
  KeyHint,
  OverlayHintsMessage,
  OverlayPad,
} from "../shared/overlay";
import { type HintsHost, OverlayHints, observeHints } from "./overlay-hints";
import { OverlayPreferences } from "./overlay-prefs";

interface SlotInit {
  slot: number;
  device?: number;
  hints?: unknown;
}

// One entry of engine.activity's slots, as the daemon writes it; hints only
// while the hints button is held (HUD-03).
function slot({ slot, device = 7 + slot, hints }: SlotInit) {
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
    ...(hints === undefined ? {} : { hints }),
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

const hint = (control: string, label = control): KeyHint => ({
  control,
  label,
});
const south = hint("south", "撤销");
const east = hint("east", "重做");

describe("the hints in a state event", () => {
  it("are the first mapped slot's list, with its device", () => {
    expect(
      observeHints(state({ slot: 0, device: 9, hints: [south, east] })),
    ).toEqual({ device: 9, entries: [south, east] });
  });

  it("skip a free slot (device 0): it is listed but not mapped", () => {
    expect(
      observeHints(
        state(
          { slot: 0, device: 0, hints: [east] },
          { slot: 1, device: 4, hints: [south] },
        ),
      ),
    ).toEqual({ device: 4, entries: [south] });
  });

  it("follow the lowest slot, wherever it is in the list", () => {
    const seen = observeHints(
      state(
        { slot: 2, device: 3, hints: [east] },
        { slot: 1, device: 4, hints: [south] },
      ),
    );
    expect(seen).toEqual({ device: 4, entries: [south] });
  });

  it("are none when the first slot has none, even if a later slot has", () => {
    expect(
      observeHints(state({ slot: 0 }, { slot: 1, hints: [south] })),
    ).toEqual({ device: 7, entries: undefined });
  });

  it("are none for an empty list, or one that is not a list", () => {
    for (const hints of [[], null, "south", { south: "撤销" }, 3])
      expect(observeHints(state({ slot: 0, hints }))?.entries).toBeUndefined();
  });

  it("leave out entries that are not well-formed, and keep the others", () => {
    const seen = observeHints(
      state({
        slot: 0,
        hints: [
          south,
          null,
          "east",
          { control: 3, label: "x" },
          { control: "East", label: "x" },
          { control: "west" },
          { control: "west", label: 5 },
          { control: "x".repeat(40), label: "x" },
          east,
          { control: "south", label: "again" },
        ],
      }),
    );
    expect(seen?.entries).toEqual([south, east]);
  });

  it("are none in a state event without a usable slot, so that a panel does not stay up", () => {
    for (const slots of [undefined, [], "none", [null], [{ slot: "0" }]]) {
      const event: DaemonEvent = {
        topic: "engine.activity",
        data: { kind: "state", slots },
      };
      expect(observeHints(event)).toEqual({});
    }
  });

  it("are not found in other events", () => {
    expect(
      observeHints({ topic: "engine.activity", data: { kind: "decision" } }),
    ).toBeUndefined();
    expect(observeHints({ topic: "log", data: {} })).toBeUndefined();
    expect(
      observeHints({ topic: "engine.activity", data: null } as DaemonEvent),
    ).toBeUndefined();
  });
});

type Shown = Omit<OverlayHintsMessage, "position">;

function setup(pads: Record<number, OverlayPad | undefined> = {}) {
  const shown: Shown[] = [];
  const asked: number[] = [];
  const answers: Array<(pad: OverlayPad | undefined) => void> = [];
  const fails: Array<(error: Error) => void> = [];
  const host: HintsHost = {
    show: (message) => shown.push(message),
    pad: (device) => {
      asked.push(device);
      return new Promise((resolve, reject) => {
        answers.push(resolve);
        fails.push(reject);
      });
    },
  };
  const hints = new OverlayHints(host);
  const flush = () => new Promise((done) => setTimeout(done, 0));
  const answer = async (index = answers.length - 1, pad?: OverlayPad) => {
    answers[index](pad ?? pads[asked[index]]);
    await flush();
  };
  return { hints, shown, asked, answers, fails, answer, flush };
}

describe("OverlayHints", () => {
  it("shows the list when a button is held, once, and hides when it is not in the events any more", () => {
    const { hints, shown } = setup();
    hints.handle(state({ slot: 0 }));
    expect(shown).toEqual([]);
    hints.handle(state({ slot: 0, hints: [south, east] }));
    expect(shown).toEqual([{ entries: [south, east], language: "zh-CN" }]);
    // The same list again, as in each of the next state events: nothing.
    hints.handle(state({ slot: 0, hints: [south, east] }));
    hints.handle(state({ slot: 0, hints: [south, east] }));
    expect(shown).toHaveLength(1);
    hints.handle(state({ slot: 0 }));
    expect(shown).toHaveLength(2);
    expect(shown[1]).toEqual({ entries: [], language: "zh-CN" });
    // Hidden once; and again later it is shown again.
    hints.handle(state({ slot: 0 }));
    expect(shown).toHaveLength(2);
    hints.handle(state({ slot: 0, hints: [south, east] }));
    expect(shown).toHaveLength(3);
    expect(shown[2].entries).toEqual([south, east]);
  });

  it("shows a changed list: the layer changed while the button is held", () => {
    const { hints, shown } = setup();
    hints.handle(state({ slot: 0, hints: [south] }));
    hints.handle(state({ slot: 0, hints: [south, east] }));
    hints.handle(state({ slot: 0, hints: [hint("south", "画笔")] }));
    expect(shown.map((m) => m.entries.length)).toEqual([1, 2, 1]);
    expect(shown[2].entries[0].label).toBe("画笔");
  });

  it("changes nothing for events that are not state events", () => {
    const { hints, shown } = setup();
    hints.handle(state({ slot: 0, hints: [south] }));
    hints.handle({
      topic: "engine.activity",
      data: { kind: "decision", slot: 0 },
    });
    hints.handle({ topic: "log", data: {} });
    expect(shown).toHaveLength(1);
  });

  it("hides when the first slot goes away, or another slot takes its place without hints", () => {
    const { hints, shown } = setup();
    hints.handle(state({ slot: 0, hints: [south] }));
    hints.handle(state({ slot: 1 }));
    expect(shown.at(-1)?.entries).toEqual([]);
    hints.handle(state({ slot: 0, hints: [south] }));
    hints.handle(state());
    expect(shown.at(-1)?.entries).toEqual([]);
  });

  it("hides when the connection to the daemon ends, and asks again for pads after it", () => {
    const { hints, shown, asked } = setup();
    hints.handle(state({ slot: 0, device: 5, hints: [south] }));
    expect(asked).toEqual([5]);
    hints.clear();
    expect(shown.at(-1)).toEqual({ entries: [], language: "zh-CN" });
    // Nothing more to hide.
    hints.clear();
    expect(shown).toHaveLength(2);
    hints.handle(state({ slot: 0, device: 5, hints: [south] }));
    expect(asked).toEqual([5, 5]);
  });

  it("worded in the UI's language, and again when the language changes while shown", () => {
    const { hints, shown } = setup();
    hints.configure({ language: "en" });
    hints.handle(state({ slot: 0, hints: [south] }));
    expect(shown[0].language).toBe("en");
    hints.configure({ language: "zh-CN" });
    expect(shown.map((m) => m.language)).toEqual(["en", "zh-CN"]);
    // The same language, or none, or a position: nothing.
    hints.configure({ language: "zh-CN" });
    hints.configure({});
    hints.configure({ position: "top", opacity: 50 });
    expect(shown).toHaveLength(2);
  });

  it("does not need the toast switch, the focus of the main window or anything but the events", () => {
    const { hints, shown } = setup();
    hints.configure({ toasts: false });
    hints.handle(state({ slot: 0, hints: [south] }));
    expect(shown).toHaveLength(1);
  });

  it("does not throw when its host does", () => {
    const host: HintsHost = {
      show: () => {
        throw new Error("overlay gone");
      },
      pad: () => {
        throw new Error("daemon gone");
      },
    };
    const hints = new OverlayHints(host);
    expect(() =>
      hints.handle(state({ slot: 0, hints: [south] })),
    ).not.toThrow();
    expect(() => hints.handle(state({ slot: 0 }))).not.toThrow();
    expect(() => hints.clear()).not.toThrow();
  });
});

describe("OverlayHints and the kind of pad", () => {
  const xbox: OverlayPad = {
    type: "xboxone",
    buttonLabels: { south: "A" },
    buttonMask: 5,
  };

  it("asks the daemon once for the first slot's device, even before any hints", () => {
    const { hints, asked } = setup();
    hints.handle(state({ slot: 0, device: 4 }));
    hints.handle(state({ slot: 0, device: 4 }));
    hints.handle(state({ slot: 0, device: 4, hints: [south] }));
    expect(asked).toEqual([4]);
  });

  it("sends the hints at once, and again with the pad when the answer comes", async () => {
    const { hints, shown, answer } = setup({ 4: xbox });
    hints.handle(state({ slot: 0, device: 4, hints: [south] }));
    expect(shown).toEqual([{ entries: [south], language: "zh-CN" }]);
    await answer(0);
    expect(shown).toHaveLength(2);
    expect(shown[1]).toEqual({
      entries: [south],
      pad: xbox,
      language: "zh-CN",
    });
    // Later events with the same list send nothing.
    hints.handle(state({ slot: 0, device: 4, hints: [south] }));
    expect(shown).toHaveLength(2);
  });

  it("sends the pad with the hints when it was known before they came", async () => {
    const { hints, shown, answer } = setup({ 4: xbox });
    hints.handle(state({ slot: 0, device: 4 }));
    await answer(0);
    expect(shown).toEqual([]);
    hints.handle(state({ slot: 0, device: 4, hints: [south] }));
    expect(shown).toEqual([{ entries: [south], pad: xbox, language: "zh-CN" }]);
  });

  it("goes on without a pad the daemon does not know, and does not ask again", async () => {
    const { hints, shown, asked, answer } = setup();
    hints.handle(state({ slot: 0, device: 4, hints: [south] }));
    await answer(0, undefined);
    hints.handle(state({ slot: 0, device: 4, hints: [south] }));
    expect(asked).toEqual([4]);
    expect(shown).toEqual([{ entries: [south], language: "zh-CN" }]);
  });

  it("goes on when the question fails", async () => {
    const { hints, shown, asked, fails, flush } = setup();
    hints.handle(state({ slot: 0, device: 4, hints: [south] }));
    fails[0](new Error("daemon is disconnected"));
    await flush();
    hints.handle(state({ slot: 0, device: 4, hints: [east] }));
    expect(asked).toEqual([4]);
    expect(shown.map((m) => m.entries)).toEqual([[south], [east]]);
  });

  it("drops an answer that comes after the connection ended", async () => {
    const { hints, shown, answers, flush } = setup();
    hints.handle(state({ slot: 0, device: 4, hints: [south] }));
    hints.clear();
    const count = shown.length;
    answers[0](xbox);
    await flush();
    expect(shown).toHaveLength(count);
    hints.handle(state({ slot: 0 }));
    expect(shown).toHaveLength(count);
  });

  it("does not show the pad for another device's hints", async () => {
    const { hints, shown, answers, flush } = setup();
    hints.handle(state({ slot: 0, device: 4, hints: [south] }));
    hints.handle(state({ slot: 0, device: 6, hints: [south] }));
    answers[0](xbox);
    await flush();
    // Device 4 is known now, but the first slot is on device 6.
    expect(shown.at(-1)?.pad).toBeUndefined();
  });
});

describe("OverlayHints among the overlay's preferences (HUD-05)", () => {
  it("takes the language from a push, like the toaster, and ignores what is not valid", () => {
    const { hints, shown } = setup();
    const toasts = { configure: vi.fn() };
    const preferences = new OverlayPreferences(() => [toasts, hints]);
    hints.handle(state({ slot: 0, hints: [south] }));
    preferences.push({ language: "en", toasts: false });
    expect(toasts.configure).toHaveBeenCalledWith({
      language: "en",
      toasts: false,
    });
    expect(shown.at(-1)?.language).toBe("en");
    preferences.push({ language: "fr" });
    expect(shown).toHaveLength(2);
  });
});
