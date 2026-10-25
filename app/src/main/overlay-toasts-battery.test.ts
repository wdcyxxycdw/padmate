import { describe, expect, it } from "vitest";
import type { DaemonEvent } from "../shared/api";
import { maxToastLength } from "../shared/overlay";
import {
  initialToastState,
  OverlayToaster,
  reduceToast,
  type ToastChange,
  type ToastContext,
  type ToastState,
  toastText,
} from "./overlay-toasts";

// The low-battery toast (IN-12, ADR-0065): told when a controller's "low" in the
// state event's batteries goes from false to true, whichever window is in
// front, with the controller's name first when there is more than one.

interface Reading {
  key: string;
  name?: string;
  percent?: number;
  low: boolean;
  state?: string;
  /** The number of the alert a low controller is in (the daemon's, IN-12). */
  episode?: number;
}

// An engine.activity state event as the daemon writes it (rpcBattery): the
// slots are left out, since the battery is not about them.
function batteries(
  list: Reading[],
  extra: Record<string, unknown> = {},
): DaemonEvent {
  return {
    topic: "engine.activity",
    data: {
      kind: "state",
      paused: false,
      capturing: false,
      suspended: "",
      slots: [],
      batteries: list.map((b, index) => ({
        device: index + 1,
        key: b.key,
        name: b.name ?? `Pad ${b.key}`,
        state: b.state ?? "discharging",
        ...(b.percent === undefined ? {} : { percent: b.percent }),
        low: b.low,
        ...(b.episode === undefined ? {} : { episode: b.episode }),
      })),
      ...extra,
    },
  };
}

const away: ToastContext = { mainFocused: false };
const editing: ToastContext = { mainFocused: true };

function run(
  events: DaemonEvent[],
  context: ToastContext = away,
  from: ToastState = initialToastState,
): Array<ToastChange | undefined> {
  let current = from;
  return events.map((event) => {
    const step = reduceToast(current, event, context);
    current = step.next;
    return step.toast;
  });
}

describe("a low battery makes a toast", () => {
  it("on the edge of low into true, with the charge, once", () => {
    expect(
      run([
        batteries([{ key: "a", percent: 60, low: false }]),
        batteries([{ key: "a", percent: 20, low: true }]),
        // The episode goes on, lower, and the event repeats.
        batteries([{ key: "a", percent: 20, low: true }]),
        batteries([{ key: "a", percent: 10, low: true }]),
        batteries([{ key: "a", percent: 25, low: true }]),
      ]),
    ).toEqual([
      undefined,
      { kind: "battery", percent: 20 },
      undefined,
      undefined,
      undefined,
    ]);
  });

  it("again for the next episode, after the controller was not low", () => {
    expect(
      run([
        batteries([{ key: "a", percent: 15, low: false }]),
        batteries([{ key: "a", percent: 15, low: true }]),
        batteries([{ key: "a", percent: 50, state: "charging", low: false }]),
        batteries([{ key: "a", percent: 12, low: true }]),
      ]),
    ).toEqual([
      undefined,
      { kind: "battery", percent: 15 },
      undefined,
      { kind: "battery", percent: 12 },
    ]);
  });

  it("for a second episode that began between two snapshots: low at both, another alert", () => {
    // Charged and low again within a snapshot's period (250 ms), so that no
    // event ever showed the controller as not low; the daemon's episode number
    // is what tells (the owner's acceptance, 2026-10-21: the balloon came, the
    // toast did not).
    expect(
      run([
        batteries([{ key: "a", percent: 60, low: false }]),
        batteries([{ key: "a", percent: 15, low: true, episode: 1 }]),
        batteries([{ key: "a", percent: 15, low: true, episode: 1 }]),
        batteries([{ key: "a", percent: 12, low: true, episode: 2 }]),
        batteries([{ key: "a", percent: 11, low: true, episode: 2 }]),
        batteries([{ key: "a", percent: 10, low: true, episode: 3 }]),
      ]),
    ).toEqual([
      undefined,
      { kind: "battery", percent: 15 },
      undefined,
      { kind: "battery", percent: 12 },
      undefined,
      { kind: "battery", percent: 10 },
    ]);
  });

  it("an episode number that goes down or stays is not a new alert, and none at all changes nothing", () => {
    expect(
      run([
        batteries([{ key: "a", percent: 60, low: false }]),
        batteries([{ key: "a", percent: 15, low: true, episode: 5 }]),
        // A daemon that started again numbers from 1: the edge into low is what
        // counts then, and there is none here.
        batteries([{ key: "a", percent: 15, low: true, episode: 1 }]),
        // An event without the member (an older daemon) keeps the episode known.
        batteries([{ key: "a", percent: 15, low: true }]),
        batteries([{ key: "a", percent: 15, low: true, episode: 1 }]),
      ]),
    ).toEqual([
      undefined,
      { kind: "battery", percent: 15 },
      undefined,
      undefined,
      undefined,
    ]);
  });

  it("an episode number after a gap of not low is told once, by the edge", () => {
    expect(
      run([
        batteries([{ key: "a", percent: 60, low: false }]),
        batteries([{ key: "a", percent: 15, low: true, episode: 1 }]),
        batteries([{ key: "a", percent: 50, low: false }]),
        batteries([{ key: "a", percent: 15, low: true, episode: 2 }]),
      ]),
    ).toEqual([
      undefined,
      { kind: "battery", percent: 15 },
      undefined,
      { kind: "battery", percent: 15 },
    ]);
  });

  it("not for the first state: where it starts is not news", () => {
    expect(
      run([
        batteries([{ key: "a", percent: 15, low: true }]),
        batteries([{ key: "a", percent: 15, low: true }]),
      ]),
    ).toEqual([undefined, undefined]);
  });

  it("for a controller that appears low after the first state", () => {
    expect(
      run([
        batteries([]),
        batteries([{ key: "a", percent: 15, low: true }]),
        batteries([{ key: "a", percent: 15, low: true }]),
      ]),
    ).toEqual([undefined, { kind: "battery", percent: 15 }, undefined]);
  });

  it("not for one that is not low, nor for a low one without a charge", () => {
    expect(
      run([
        batteries([{ key: "a", percent: 80, low: false }]),
        batteries([{ key: "a", state: "unknown", low: false }]),
        batteries([{ key: "a", state: "wired", low: false }]),
        // A daemon that says low without a charge has nothing to tell.
        batteries([{ key: "a", low: true }]),
      ]),
    ).toEqual([undefined, undefined, undefined, undefined]);
  });

  it("not again when a controller wakes up in the same episode: what an event leaves out is kept", () => {
    expect(
      run([
        batteries([{ key: "a", percent: 40, low: false }]),
        batteries([{ key: "a", percent: 15, low: true }]),
        // The receiver reports it removed for a moment, and brings it back.
        batteries([]),
        batteries([{ key: "a", percent: 15, low: true }]),
      ]),
    ).toEqual([
      undefined,
      { kind: "battery", percent: 15 },
      undefined,
      undefined,
    ]);
  });

  it("keeps the lows over an event without batteries, and over one that cannot be read", () => {
    const noList: DaemonEvent = {
      topic: "engine.activity",
      data: { kind: "state", paused: false, slots: [] },
    };
    const odd: DaemonEvent = {
      topic: "engine.activity",
      data: {
        kind: "state",
        paused: false,
        batteries: [
          null,
          7,
          { key: "", low: true, percent: 5 },
          { key: "a", low: "yes", percent: 5 },
          { key: "b", low: true, percent: 101 },
          { key: "b", low: true, percent: 1.5 },
        ],
      },
    };
    expect(
      run([
        batteries([{ key: "a", percent: 40, low: false }]),
        noList,
        odd,
        // Nothing of that made "a" low, or forgot that it was not.
        batteries([{ key: "a", percent: 15, low: true }]),
        noList,
        batteries([{ key: "a", percent: 15, low: true }]),
      ]),
    ).toEqual([
      undefined,
      undefined,
      undefined,
      { kind: "battery", percent: 15 },
      undefined,
      undefined,
    ]);
  });

  it("tells the first of several controllers that went low in one event", () => {
    expect(
      run([
        batteries([
          { key: "a", percent: 40, low: false },
          { key: "b", percent: 40, low: false },
        ]),
        batteries([
          { key: "a", percent: 15, low: true },
          { key: "b", percent: 10, low: true },
        ]),
      ])[1],
    ).toEqual({ kind: "battery", percent: 15, pad: "Pad a" });
  });

  it("names the controller only when there is more than one", () => {
    const [, one] = run([
      batteries([{ key: "a", percent: 40, low: false }]),
      batteries([{ key: "a", percent: 15, low: true }]),
    ]);
    expect(one).toEqual({ kind: "battery", percent: 15 });
    expect(one).not.toHaveProperty("pad");
    const [, two] = run([
      batteries([
        { key: "a", percent: 40, low: false },
        { key: "b", percent: 80, low: false },
      ]),
      batteries([
        { key: "a", percent: 40, low: false },
        { key: "b", percent: 15, low: true, name: "Pro Controller" },
      ]),
    ]);
    expect(two).toEqual({
      kind: "battery",
      percent: 15,
      pad: "Pro Controller",
    });
  });

  it("each controller has an episode of its own", () => {
    expect(
      run([
        batteries([
          { key: "a", percent: 15, low: false },
          { key: "b", percent: 15, low: false },
        ]),
        batteries([
          { key: "a", percent: 15, low: true },
          { key: "b", percent: 15, low: false },
        ]),
        batteries([
          { key: "a", percent: 15, low: true },
          { key: "b", percent: 12, low: true, name: "Second" },
        ]),
      ]),
    ).toEqual([
      undefined,
      { kind: "battery", percent: 15, pad: "Pad a" },
      { kind: "battery", percent: 12, pad: "Second" },
    ]);
  });
});

describe("a low battery and the main window", () => {
  it("is told although the main window is in front", () => {
    expect(
      run(
        [
          batteries([{ key: "a", percent: 40, low: false }]),
          batteries([{ key: "a", percent: 15, low: true }]),
        ],
        editing,
      ),
    ).toEqual([undefined, { kind: "battery", percent: 15 }]);
  });

  it("while the other changes are still not told then", () => {
    const first = reduceToast(
      initialToastState,
      batteries([{ key: "a", percent: 40, low: false }], { paused: false }),
      editing,
    ).next;
    expect(
      reduceToast(
        first,
        batteries([{ key: "a", percent: 40, low: false }], { paused: true }),
        editing,
      ).toast,
    ).toBeUndefined();
  });

  it("comes before the others when they come in the same event", () => {
    const start = reduceToast(
      initialToastState,
      batteries([{ key: "a", percent: 40, low: false }], { paused: false }),
      away,
    ).next;
    const step = reduceToast(
      start,
      batteries([{ key: "a", percent: 15, low: true }], { paused: true }),
      away,
    );
    expect(step.toast).toEqual({ kind: "battery", percent: 15 });
    // The pause is not told later: it is in the state now.
    expect(
      reduceToast(
        step.next,
        batteries([{ key: "a", percent: 15, low: true }], { paused: true }),
        away,
      ).toast,
    ).toBeUndefined();
  });

  it("is not made by the other changes, and does not hide them when it is not there", () => {
    const start = reduceToast(
      initialToastState,
      batteries([{ key: "a", percent: 40, low: false }], { paused: false }),
      away,
    ).next;
    expect(
      reduceToast(
        start,
        batteries([{ key: "a", percent: 40, low: false }], { paused: true }),
        away,
      ).toast,
    ).toEqual({ kind: "paused" });
  });
});

describe("the state it keeps", () => {
  it("has the lows only once an event listed batteries, so older states stay as they were", () => {
    const step = reduceToast(
      initialToastState,
      { topic: "engine.activity", data: { kind: "state", paused: false } },
      away,
    );
    expect(step.next).not.toHaveProperty("lows");
    expect(
      reduceToast(step.next, batteries([{ key: "a", low: false }]), away).next
        .lows,
    ).toEqual({ a: false });
  });

  it("keeps the episode of each low controller, bounded like the lows", () => {
    const step = reduceToast(
      initialToastState,
      batteries([{ key: "a", percent: 15, low: true, episode: 4 }]),
      away,
    );
    expect(step.next.episodes).toEqual({ a: 4 });
    // Not low: no episode is kept for it.
    expect(
      reduceToast(
        step.next,
        batteries([{ key: "a", percent: 60, low: false }]),
        away,
      ).next.episodes,
    ).toEqual({});
    const many = Array.from({ length: 40 }, (_, i) => ({
      key: `k${i}`,
      percent: 15,
      low: true,
      episode: i + 1,
    }));
    const { next } = reduceToast(initialToastState, batteries(many), away);
    expect(Object.keys(next.episodes ?? {})).toHaveLength(32);
    expect(next.episodes).toHaveProperty("k39");
    expect(next.episodes).not.toHaveProperty("k0");
  });

  it("is bounded: the controllers it forgot first are the oldest", () => {
    const many = Array.from({ length: 40 }, (_, i) => ({
      key: `k${i}`,
      percent: 50,
      low: false,
    }));
    const { next } = reduceToast(initialToastState, batteries(many), away);
    expect(Object.keys(next.lows ?? {})).toHaveLength(32);
    expect(next.lows).toHaveProperty("k39");
    expect(next.lows).not.toHaveProperty("k0");
  });

  it("does not change the state it is given", () => {
    const before = reduceToast(
      initialToastState,
      batteries([{ key: "a", percent: 40, low: false }]),
      away,
    ).next;
    const copy = structuredClone(before);
    reduceToast(
      before,
      batteries([{ key: "a", percent: 15, low: true }]),
      away,
    );
    expect(before).toEqual(copy);
  });

  it("is safe with a key that is a member of every object", () => {
    expect(
      run([
        batteries([{ key: "__proto__", percent: 40, low: false }]),
        batteries([{ key: "constructor", percent: 15, low: true }]),
      ]),
    ).toEqual([undefined, { kind: "battery", percent: 15 }]);
  });
});

describe("the text of a low battery", () => {
  it("is Chinese or English as the UI is", () => {
    const change: ToastChange = { kind: "battery", percent: 15 };
    expect(toastText(change, "zh-CN")).toBe("手柄电量低：15%");
    expect(toastText(change, "en")).toBe("Controller battery low: 15%");
    const named: ToastChange = {
      kind: "battery",
      percent: 8,
      pad: "Nova 2 Lite",
    };
    expect(toastText(named, "zh-CN")).toBe("Nova 2 Lite 电量低：8%");
    expect(toastText(named, "en")).toBe("Nova 2 Lite battery low: 8%");
  });

  it("is one line whatever the name holds, and the charge always shows", () => {
    expect(
      toastText({ kind: "battery", percent: 7, pad: "a\nb\tc\u0000d" }, "en"),
    ).toBe("a b c d battery low: 7%");
    const long = toastText(
      { kind: "battery", percent: 7, pad: "长".repeat(1000) },
      "zh-CN",
    );
    expect(Array.from(long).length).toBeLessThan(maxToastLength);
    expect(long.endsWith("电量低：7%")).toBe(true);
    // A name that is nothing once cleaned is no name.
    expect(
      toastText({ kind: "battery", percent: 7, pad: " \n " }, "zh-CN"),
    ).toBe("手柄电量低：7%");
  });
});

describe("the toaster tells a low battery", () => {
  function make(focused = false) {
    const shown: string[] = [];
    const host = {
      focused,
      mainFocused: () => host.focused,
      show: (text: string) => {
        shown.push(text);
      },
    };
    return { toaster: new OverlayToaster(host), host, shown };
  }
  const events = [
    batteries([{ key: "a", percent: 40, low: false }]),
    batteries([{ key: "a", percent: 15, low: true }]),
  ];

  it("in the UI's language, also with the main window focused", () => {
    const { toaster, shown } = make(true);
    for (const event of events) toaster.handle(event);
    expect(shown).toEqual(["手柄电量低：15%"]);
    const english = make();
    english.toaster.configure({ language: "en" });
    for (const event of events) english.toaster.handle(event);
    expect(english.shown).toEqual(["Controller battery low: 15%"]);
  });

  it("not with toasts off (the 提示消息 switch), and not later either", () => {
    const { toaster, shown } = make();
    toaster.handle(events[0]);
    toaster.configure({ toasts: false });
    toaster.handle(events[1]);
    toaster.configure({ toasts: true });
    // The episode was seen while the switch was off: it is not news now.
    toaster.handle(events[1]);
    expect(shown).toEqual([]);
    // The next one is.
    toaster.handle(
      batteries([{ key: "a", percent: 60, state: "charging", low: false }]),
    );
    toaster.handle(batteries([{ key: "a", percent: 14, low: true }]));
    expect(shown).toEqual(["手柄电量低：14%"]);
  });
});
