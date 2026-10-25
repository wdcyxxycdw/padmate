import { describe, expect, it, vi } from "vitest";
import type { DaemonEvent } from "../shared/api";
import { maxToastLength } from "../shared/overlay";
import {
  initialToastState,
  OverlayToaster,
  reduceToast,
  scriptToastFrom,
  type ToastChange,
  type ToastContext,
  type ToastState,
  toastText,
} from "./overlay-toasts";

interface SlotInit {
  slot: number;
  profile?: string;
  base?: string;
  overlays?: string[];
}

// One entry of engine.activity's slots, as the daemon writes it (rpcSlot).
function slot({
  slot,
  profile = "绘画",
  base = "base",
  overlays = [],
}: SlotInit) {
  return {
    slot,
    device: 7 + slot,
    key: `pad-${slot}`,
    name: "Pad",
    profile,
    generation: 1,
    layers: { base, overlays, color: "#ffffff" },
    latches: [],
    mode: 0,
  };
}

function state(
  paused: boolean,
  ...slots: SlotInit[]
): DaemonEvent & { data: Record<string, unknown> } {
  return {
    topic: "engine.activity",
    data: {
      kind: "state",
      paused,
      capturing: false,
      suspended: "",
      slots: slots.map(slot),
    },
  };
}

const away: ToastContext = { mainFocused: false };
const editing: ToastContext = { mainFocused: true };

/** Feeds events in turn; returns what each made. */
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

describe("what makes a toast", () => {
  it("follows the first slot with a device: a free slot (device 0) is skipped", () => {
    const event = (profile: string) => ({
      topic: "engine.activity" as const,
      data: {
        kind: "state",
        paused: false,
        slots: [
          {
            slot: 0,
            device: 0,
            profile: "默认",
            layers: { base: "base", overlays: [] },
          },
          {
            slot: 1,
            device: 5,
            profile,
            layers: { base: "base", overlays: [] },
          },
        ],
      },
    });
    const first = reduceToast(initialToastState, event("绘画"), away);
    expect(first.next.slot).toBe(1);
    expect(first.next.profile).toBe("绘画");
    const second = reduceToast(first.next, event("CSP"), away);
    expect(second.toast).toEqual({ kind: "profile", name: "CSP" });
  });

  it("makes none for the first state: it is where we start, not a change", () => {
    const first = reduceToast(
      initialToastState,
      state(true, { slot: 0, profile: "绘画", overlays: ["fn"] }),
      away,
    );
    expect(first.toast).toBeUndefined();
    expect(first.next).toEqual({
      seen: true,
      paused: true,
      slot: 0,
      profile: "绘画",
      // The base: overlay layers are not told.
      layer: "base",
    });
  });

  it("makes one when the slot's profile changes, by switch or by activation", () => {
    expect(
      run([
        state(false, { slot: 0, profile: "绘画" }),
        state(false, { slot: 0, profile: "默认" }),
        state(false, { slot: 0, profile: "绘画" }),
      ]),
    ).toEqual([
      undefined,
      { kind: "profile", name: "默认" },
      { kind: "profile", name: "绘画" },
    ]);
  });

  it("makes one for a pause and one for a resume", () => {
    expect(
      run([
        state(false, { slot: 0 }),
        state(true, { slot: 0 }),
        state(true, { slot: 0 }),
        state(false, { slot: 0 }),
      ]),
    ).toEqual([undefined, { kind: "paused" }, undefined, { kind: "resumed" }]);
  });

  it("tells a pause also when no controller is mapped", () => {
    expect(run([state(false), state(true), state(false)])).toEqual([
      undefined,
      { kind: "paused" },
      { kind: "resumed" },
    ]);
  });

  it("makes one when the base layer changes, and none for overlay layers pushed or popped", () => {
    const toasts = run([
      state(false, { slot: 0, base: "base" }),
      // Overlays (a layer held while painting, or toggled) are not told.
      state(false, { slot: 0, base: "base", overlays: ["fn"] }),
      state(false, { slot: 0, base: "base", overlays: ["fn", "brush"] }),
      state(false, { slot: 0, base: "base", overlays: ["fn"] }),
      state(false, { slot: 0, base: "base" }),
      // The base is switched, and back.
      state(false, { slot: 0, base: "erase" }),
      state(false, { slot: 0, base: "base" }),
    ]);
    expect(toasts).toEqual([
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      { kind: "layer", name: "erase" },
      { kind: "layer", name: "base" },
    ]);
  });

  it("counts a base switched under an overlay: the base is what is told", () => {
    expect(
      run([
        state(false, { slot: 0, base: "base", overlays: ["fn"] }),
        state(false, { slot: 0, base: "erase", overlays: ["fn"] }),
      ]),
    ).toEqual([undefined, { kind: "layer", name: "erase" }]);
  });

  it("makes none for the same state again, as a periodic event or a reconnect repeats it", () => {
    const same = state(false, { slot: 0, overlays: ["fn"] });
    expect(run([same, same, same, same])).toEqual([
      undefined,
      undefined,
      undefined,
      undefined,
    ]);
  });

  it("keeps what it saw over a gap: a reconnect with the same state is quiet, one with another is a change", () => {
    // The state carried over a lost connection is the reducer's state: only
    // the events in between are missing.
    const before = reduceToast(
      initialToastState,
      state(false, { slot: 0, profile: "绘画" }),
      away,
    ).next;
    expect(
      run([state(false, { slot: 0, profile: "绘画" })], away, before),
    ).toEqual([undefined]);
    expect(
      run([state(false, { slot: 0, profile: "默认" })], away, before),
    ).toEqual([{ kind: "profile", name: "默认" }]);
  });

  it("shows the profile when several things changed at once, then the pause, then the layer", () => {
    const base = state(false, { slot: 0, profile: "绘画", base: "base" });
    expect(
      run([
        base,
        // A new profile has other layers: only the profile is told.
        state(true, { slot: 0, profile: "默认", base: "main" }),
      ])[1],
    ).toEqual({ kind: "profile", name: "默认" });
    expect(
      run([base, state(true, { slot: 0, profile: "绘画", base: "main" })])[1],
    ).toEqual({ kind: "paused" });
    // What was not told is not told later either.
    expect(
      run([
        base,
        state(true, { slot: 0, profile: "默认", base: "main" }),
        state(true, { slot: 0, profile: "默认", base: "main" }),
      ]),
    ).toEqual([undefined, { kind: "profile", name: "默认" }, undefined]);
  });
});

describe("the main window being in front", () => {
  it("makes none while it is focused, and does not tell the change later", () => {
    const start = reduceToast(
      initialToastState,
      state(false, { slot: 0, profile: "绘画" }),
      editing,
    ).next;
    const focusedChange = reduceToast(
      start,
      state(true, { slot: 0, profile: "默认", overlays: ["fn"] }),
      editing,
    );
    expect(focusedChange.toast).toBeUndefined();
    // The window loses focus: the same state is not news, a new change is.
    expect(
      run(
        [
          state(true, { slot: 0, profile: "默认", overlays: ["fn"] }),
          state(false, { slot: 0, profile: "默认", overlays: ["fn"] }),
        ],
        away,
        focusedChange.next,
      ),
    ).toEqual([undefined, { kind: "resumed" }]);
  });

  it("does not take the first state for a change either way", () => {
    expect(
      reduceToast(initialToastState, state(false, { slot: 0 }), editing).toast,
    ).toBeUndefined();
  });
});

describe("only the first mapped slot is followed", () => {
  it("ignores the others", () => {
    expect(
      run([
        state(false, { slot: 0, profile: "绘画" }, { slot: 1, base: "a" }),
        state(
          false,
          { slot: 0, profile: "绘画" },
          { slot: 1, base: "b", profile: "默认" },
        ),
      ]),
    ).toEqual([undefined, undefined]);
  });

  it("takes the lowest index, whatever the order of the list", () => {
    const toasts = run([
      state(false, { slot: 2, base: "x" }, { slot: 0, base: "base" }),
      state(false, { slot: 2, base: "y" }, { slot: 0, base: "fn" }),
    ]);
    expect(toasts).toEqual([undefined, { kind: "layer", name: "fn" }]);
  });

  it("takes a new first slot for a new starting point: a pad unplugged or plugged in is not a change", () => {
    expect(
      run([
        state(false, { slot: 0, profile: "绘画", base: "a" }),
        // The first pad goes; the second, on another layer, is now the first.
        state(false, { slot: 1, profile: "绘画", base: "b" }),
        // Nobody is mapped.
        state(false),
        // A pad comes back.
        state(false, { slot: 0, profile: "默认", base: "c" }),
        // A real change on it.
        state(false, { slot: 0, profile: "默认", base: "d" }),
      ]),
    ).toEqual([
      undefined,
      undefined,
      undefined,
      undefined,
      { kind: "layer", name: "d" },
    ]);
  });
});

describe("a gap in the slots", () => {
  it("leaves what is known as it is, so a change after the gap is told", () => {
    expect(
      run([
        state(false, { slot: 0, profile: "绘画", base: "a" }),
        // The list is empty for a moment: nobody is mapped.
        state(false),
        state(false, { slot: 0, profile: "默认", base: "a" }),
        state(false),
        state(false, { slot: 0, profile: "默认", base: "b" }),
      ]),
    ).toEqual([
      undefined,
      undefined,
      { kind: "profile", name: "默认" },
      undefined,
      { kind: "layer", name: "b" },
    ]);
  });

  it("keeps the last profile and layer over an event whose slot does not say them", () => {
    const unreadable = {
      topic: "engine.activity",
      data: {
        kind: "state",
        paused: false,
        slots: [{ slot: 0, profile: "", layers: {} }],
      },
    } satisfies DaemonEvent;
    expect(
      run([
        state(false, { slot: 0, profile: "绘画", base: "a" }),
        unreadable,
        state(false, { slot: 0, profile: "默认", base: "b" }),
      ]),
    ).toEqual([undefined, undefined, { kind: "profile", name: "默认" }]);
  });

  it("starts again from the first state of another slot, which says nothing of the old one", () => {
    const toasts = run([
      state(false, { slot: 0, profile: "绘画", base: "a" }),
      state(false),
      // Not the slot it knew: a new starting point, silent.
      state(false, { slot: 1, profile: "默认", base: "b" }),
      state(false, { slot: 1, profile: "默认", base: "c" }),
    ]);
    expect(toasts).toEqual([
      undefined,
      undefined,
      undefined,
      { kind: "layer", name: "c" },
    ]);
  });
});

describe("what it does with events that are not what it expects", () => {
  const known = reduceToast(
    initialToastState,
    state(false, { slot: 0, profile: "绘画" }),
    away,
  ).next;

  it("passes over other events without changing its state", () => {
    const others: DaemonEvent[] = [
      { topic: "input", data: { kind: "down", device: 1, control: "south" } },
      {
        topic: "engine.activity",
        data: { kind: "decision", slot: 0, decision: { reason: "dispatch" } },
      },
      { topic: "engine.activity", data: null },
      { topic: "engine.activity", data: undefined },
      { topic: "engine.activity", data: "state" },
      { topic: "engine.activity", data: [] },
      { topic: "engine.activity", data: { paused: true } },
      { topic: "profile.changed", data: { kind: "active", id: "default" } },
      { topic: "dropped", data: { n: 3 } },
    ];
    for (const event of others) {
      const step = reduceToast(known, event, away);
      expect(step.toast).toBeUndefined();
      expect(step.next).toBe(known);
    }
    // Before the first state they do not make it the first either.
    expect(reduceToast(initialToastState, others[1], away).next).toBe(
      initialToastState,
    );
  });

  it("does not take a field it cannot read for a change, and keeps the last known pause", () => {
    const odd: DaemonEvent[] = [
      // paused is not a boolean: the pause is unknown, not resumed.
      {
        topic: "engine.activity",
        data: { kind: "state", paused: "yes", slots: [slot({ slot: 0 })] },
      },
      // No slots list, an empty profile, a layers object without names.
      { topic: "engine.activity", data: { kind: "state", paused: false } },
      {
        topic: "engine.activity",
        data: {
          kind: "state",
          paused: false,
          slots: [{ slot: 0, profile: "", layers: { base: "", overlays: [] } }],
        },
      },
      // Slots that are not slots.
      {
        topic: "engine.activity",
        data: {
          kind: "state",
          paused: false,
          slots: [null, 3, "x", {}, { slot: "0" }, { slot: 1.5 }],
        },
      },
    ];
    for (const event of odd) {
      const step = reduceToast(known, event, away);
      expect(step.toast, JSON.stringify(event.data)).toBeUndefined();
      expect(step.next.seen).toBe(true);
      expect(step.next.paused).toBe(false);
    }
  });

  it("ignores fields it does not know", () => {
    const extra = state(false, { slot: 0, profile: "绘画" });
    Object.assign(extra.data, { future: { a: 1 }, capturing: true });
    expect(reduceToast(known, extra, away).toast).toBeUndefined();
  });

  it("does not change the state it is given", () => {
    const before = structuredClone(known);
    reduceToast(known, state(true, { slot: 0, profile: "默认" }), away);
    expect(known).toEqual(before);
  });
});

describe("the text", () => {
  const changes: Array<[ToastChange, string, string]> = [
    [{ kind: "profile", name: "绘画" }, "配置：绘画", "Profile: 绘画"],
    [{ kind: "paused" }, "已暂停", "Paused"],
    [{ kind: "resumed" }, "已恢复", "Resumed"],
    [{ kind: "layer", name: "fn" }, "层：fn", "Layer: fn"],
    [{ kind: "layer", name: "base" }, "层：base", "Layer: base"],
  ];

  it("is Chinese or English as the UI is", () => {
    for (const [change, zh, en] of changes) {
      expect(toastText(change, "zh-CN")).toBe(zh);
      expect(toastText(change, "en")).toBe(en);
    }
  });

  it("is one line of a bounded length, whatever the name holds", () => {
    expect(toastText({ kind: "profile", name: "a\nb\t c\u0000d" }, "en")).toBe(
      "Profile: a b c d",
    );
    const long = toastText({ kind: "layer", name: "长".repeat(1000) }, "zh-CN");
    expect(Array.from(long)).toHaveLength(maxToastLength);
    expect(long.startsWith("层：长")).toBe(true);
  });
});

describe("the toaster", () => {
  function make(
    options: { focused?: boolean; show?: (text: string) => void } = {},
  ) {
    const shown: string[] = [];
    const host = {
      focused: options.focused ?? false,
      mainFocused() {
        return host.focused;
      },
      show:
        options.show ??
        ((text: string) => {
          shown.push(text);
        }),
    };
    return { toaster: new OverlayToaster(host), host, shown };
  }

  it("tells the overlay a change at once, in Chinese until the UI says otherwise, and the first state nothing", () => {
    const { toaster, shown } = make();
    toaster.handle(state(false, { slot: 0, profile: "绘画" }));
    expect(shown).toEqual([]);
    toaster.handle(state(false, { slot: 0, profile: "默认" }));
    toaster.handle(state(true, { slot: 0, profile: "默认" }));
    toaster.handle(state(false, { slot: 0, profile: "默认" }));
    expect(shown).toEqual(["配置：默认", "已暂停", "已恢复"]);
  });

  it("speaks English once the UI has said it is, and Chinese again when it says so", () => {
    const { toaster, shown } = make();
    toaster.configure({ language: "en" });
    toaster.handle(state(false, { slot: 0, profile: "Paint" }));
    toaster.handle(state(false, { slot: 0, profile: "Default" }));
    expect(shown).toEqual(["Profile: Default"]);
    toaster.handle(state(true, { slot: 0, profile: "Default" }));
    // The other fields of a push do not change the language, and a push
    // without one keeps it.
    toaster.configure({ toasts: true });
    toaster.handle(state(false, { slot: 0, profile: "Default" }));
    expect(shown).toEqual(["Profile: Default", "Paused", "Resumed"]);
    toaster.configure({ language: "zh-CN" });
    toaster.handle(state(true, { slot: 0, profile: "Default" }));
    expect(shown.at(-1)).toBe("已暂停");
  });

  it("shows nothing with toasts off, and the changes meanwhile are not told when they are on again (HUD-05)", () => {
    const { toaster, shown } = make();
    toaster.handle(state(false, { slot: 0, profile: "绘画" }));
    toaster.configure({ toasts: false });
    toaster.handle(state(true, { slot: 0, profile: "默认" }));
    toaster.handle(state(false, { slot: 0, profile: "默认" }));
    expect(shown).toEqual([]);
    toaster.configure({ toasts: true });
    // The same state as before it was turned on: nothing is news.
    toaster.handle(state(false, { slot: 0, profile: "默认" }));
    expect(shown).toEqual([]);
    toaster.handle(state(true, { slot: 0, profile: "默认" }));
    expect(shown).toEqual(["已暂停"]);
    // Off again: the next change is not told, the one after it is.
    toaster.configure({ toasts: false });
    toaster.handle(state(false, { slot: 0, profile: "默认" }));
    toaster.configure({ toasts: true });
    toaster.handle(state(true, { slot: 0, profile: "默认" }));
    expect(shown).toEqual(["已暂停", "已暂停"]);
  });

  it("is on, and in Chinese, until the first push; a push without a field leaves it", () => {
    const { toaster, shown } = make();
    toaster.configure({});
    toaster.handle(state(false, { slot: 0 }));
    toaster.handle(state(true, { slot: 0 }));
    expect(shown).toEqual(["已暂停"]);
    toaster.configure({ toasts: false, language: "en" });
    toaster.configure({});
    toaster.handle(state(false, { slot: 0 }));
    expect(shown).toEqual(["已暂停"]);
    toaster.configure({ toasts: true });
    toaster.handle(state(true, { slot: 0 }));
    expect(shown).toEqual(["已暂停", "Paused"]);
  });

  it("shows nothing while the main window is focused, and not afterwards either", () => {
    const { toaster, host, shown } = make({ focused: true });
    toaster.handle(state(false, { slot: 0, profile: "绘画" }));
    toaster.handle(state(true, { slot: 0, profile: "默认" }));
    expect(shown).toEqual([]);
    host.focused = false;
    toaster.handle(state(true, { slot: 0, profile: "默认" }));
    expect(shown).toEqual([]);
    toaster.handle(state(false, { slot: 0, profile: "默认" }));
    expect(shown).toEqual(["已恢复"]);
  });

  it("never throws into the daemon's reader, and does not lose the thread on a failure", () => {
    // It runs inside the bridge's frame handler: an exception there ends the
    // connection to the daemon.
    const throwing = make({
      show: () => {
        throw new Error("no overlay");
      },
    });
    throwing.toaster.handle(state(false, { slot: 0 }));
    expect(() =>
      throwing.toaster.handle(state(true, { slot: 0 })),
    ).not.toThrow();
    const after = vi.fn();
    const focus = make({ show: after });
    focus.host.mainFocused = () => {
      throw new Error("Object has been destroyed");
    };
    focus.toaster.handle(state(false, { slot: 0 }));
    expect(() => focus.toaster.handle(state(true, { slot: 0 }))).not.toThrow();
    expect(after).not.toHaveBeenCalled();
    for (const event of [
      { topic: "engine.activity", data: undefined },
      { topic: "engine.activity", data: { kind: "state", slots: 5 } },
      { topic: "log", data: null },
    ] as DaemonEvent[])
      expect(() => throwing.toaster.handle(event)).not.toThrow();
  });
});

describe("a script's toast (SCR-22)", () => {
  // The event the daemon publishes for hud.toast.
  const hud = (data: unknown): DaemonEvent => ({ topic: "hud", data });
  const toast = (text: string, ms = 1500, slot = 0) =>
    hud({ kind: "toast", slot, text, ms });

  function make(focused = false) {
    const shown: Array<[string, number | undefined]> = [];
    const host = {
      focused,
      mainFocused: () => host.focused,
      show: (text: string, ms?: number) => {
        shown.push([text, ms]);
      },
    };
    return { toaster: new OverlayToaster(host), host, shown };
  }

  it("is the text and how long it holds, from the event", () => {
    expect(scriptToastFrom(toast("脚本提示", 3000))).toEqual({
      text: "脚本提示",
      ms: 3000,
    });
    // The slot does not matter, nor do other members.
    expect(
      scriptToastFrom(
        hud({ kind: "toast", slot: 3, text: "a", ms: 600, x: 1 }),
      ),
    ).toEqual({ text: "a", ms: 600 });
    expect(scriptToastFrom(hud({ kind: "toast", text: "a", ms: 600 }))).toEqual(
      {
        text: "a",
        ms: 600,
      },
    );
  });

  it("holds 1500 ms unless ms is a whole number from 500 to 10000", () => {
    for (const ms of [500, 10000, 1234])
      expect(scriptToastFrom(toast("a", ms))?.ms, String(ms)).toBe(ms);
    for (const ms of [
      499,
      10001,
      0,
      -5,
      1500.5,
      Number.NaN,
      "3000",
      null,
      undefined,
      {},
    ])
      expect(scriptToastFrom(toast("a", ms as number))?.ms, String(ms)).toBe(
        1500,
      );
    expect(scriptToastFrom(hud({ kind: "toast", text: "a" }))?.ms).toBe(1500);
  });

  it("is one line of a bounded length, like the others", () => {
    expect(scriptToastFrom(toast(" a\n\tb\u0000 c "))?.text).toBe("a b c");
    expect(
      Array.from(scriptToastFrom(toast("长".repeat(500)))?.text ?? ""),
    ).toHaveLength(maxToastLength);
  });

  it("is none for an empty text, a text that is not a string, another kind or another topic", () => {
    for (const data of [
      { kind: "toast", text: "", ms: 1500 },
      { kind: "toast", text: " \n\t", ms: 1500 },
      { kind: "toast", text: 7, ms: 1500 },
      { kind: "toast", ms: 1500 },
      { kind: "panel", text: "a", ms: 1500 },
      { text: "a", ms: 1500 },
      null,
      "a",
      [],
      undefined,
    ])
      expect(scriptToastFrom(hud(data)), JSON.stringify(data)).toBeUndefined();
    expect(
      scriptToastFrom({
        topic: "log",
        data: { kind: "toast", text: "a", ms: 1500 },
      }),
    ).toBeUndefined();
    expect(
      scriptToastFrom({
        topic: "engine.activity",
        data: { kind: "toast", text: "a", ms: 1500 },
      }),
    ).toBeUndefined();
  });

  it("shows at once in the overlay, with the time it holds", () => {
    const { toaster, shown } = make();
    toaster.handle(toast("脚本提示", 3000));
    toaster.handle(toast("再来一个", 1500));
    expect(shown).toEqual([
      ["脚本提示", 3000],
      ["再来一个", 1500],
    ]);
  });

  it("shows although the main window is focused, and from any slot", () => {
    const { toaster, shown } = make(true);
    toaster.handle(toast("脚本提示", 1500, 0));
    toaster.handle(toast("另一个手柄", 1500, 3));
    expect(shown.map(([text]) => text)).toEqual(["脚本提示", "另一个手柄"]);
  });

  it("follows the switch for toasts: with them off it shows nothing, and tells nothing later", () => {
    const { toaster, shown } = make();
    toaster.configure({ toasts: false });
    toaster.handle(toast("脚本提示"));
    expect(shown).toEqual([]);
    toaster.configure({ toasts: true });
    expect(shown).toEqual([]);
    toaster.handle(toast("脚本提示"));
    expect(shown).toHaveLength(1);
  });

  it("is told as it is, whatever the UI's language", () => {
    const { toaster, shown } = make();
    toaster.configure({ language: "en" });
    toaster.handle(toast("脚本提示"));
    expect(shown[0][0]).toBe("脚本提示");
  });

  it("does not disturb the state toasts: they go on from where they were", () => {
    const { toaster, shown } = make();
    toaster.handle(state(false, { slot: 0, profile: "绘画" }));
    toaster.handle(toast("脚本提示"));
    toaster.handle(state(false, { slot: 0, profile: "默认" }));
    toaster.handle(toast("再来一个", 800));
    toaster.handle(state(true, { slot: 0, profile: "默认" }));
    expect(shown).toEqual([
      ["脚本提示", 1500],
      ["配置：默认", undefined],
      ["再来一个", 800],
      ["已暂停", undefined],
    ]);
  });

  it("never throws into the daemon's reader", () => {
    const toaster = new OverlayToaster({
      mainFocused: () => false,
      show: () => {
        throw new Error("no overlay");
      },
    });
    expect(() => toaster.handle(toast("a"))).not.toThrow();
    expect(() => toaster.handle(hud(undefined))).not.toThrow();
  });
});
