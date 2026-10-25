import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  type OverlayAPI,
  overlayHintsChannel,
  overlayPanelsChannel,
  overlayPrefsChannel,
  overlayToastChannel,
} from "../shared/overlay";

// The overlay's preload run against a stand-in for electron: what Main sends
// arrives through ipcRenderer.on handlers, and what the page gets is the
// object handed to contextBridge. The source-level rules (it only listens, it
// imports nothing else) are in main/security.test.ts.
type Handler = (event: unknown, payload: unknown) => void;
const electron = vi.hoisted(() => ({
  handlers: new Map<string, Handler[]>(),
  exposed: new Map<string, unknown>(),
}));

vi.mock("electron", () => ({
  contextBridge: {
    exposeInMainWorld: (name: string, api: unknown) =>
      electron.exposed.set(name, api),
  },
  ipcRenderer: {
    on: (channel: string, handler: Handler) => {
      electron.handlers.set(channel, [
        ...(electron.handlers.get(channel) ?? []),
        handler,
      ]);
    },
    removeListener: (channel: string, handler: Handler) => {
      electron.handlers.set(
        channel,
        (electron.handlers.get(channel) ?? []).filter((one) => one !== handler),
      );
    },
  },
}));

/** What Main does: sends a message on a channel to the page's preload. */
function send(channel: string, payload: unknown): void {
  for (const handler of electron.handlers.get(channel) ?? [])
    handler({}, payload);
}

/** Runs the preload afresh, as a page load does, and returns what the page gets. */
async function load(): Promise<OverlayAPI> {
  vi.resetModules();
  electron.handlers.clear();
  electron.exposed.clear();
  await import("./overlay");
  return electron.exposed.get("padmateOverlay") as OverlayAPI;
}

beforeEach(() => {
  electron.handlers.clear();
});

// The two messages the page draws, each with one that is shown and one that
// hides, and something the preload must refuse.
const shownPanels = {
  panels: [{ title: "画笔", rows: [["大小", "12"]] }],
  position: "top",
};
const hiddenPanels = { panels: [], position: "bottom" };
const shownHints = {
  entries: [{ control: "south", label: "撤销" }],
  language: "en",
  position: "top",
};
const hiddenHints = { entries: [], language: "zh-CN", position: "bottom" };

const kept = [
  {
    name: "panels (SCR-22)",
    channel: overlayPanelsChannel,
    on: (api: OverlayAPI, listener: (message: unknown) => void) =>
      api.onPanels(listener),
    shown: shownPanels,
    hidden: hiddenPanels,
    invalid: [{ panels: "none" }, { position: "top" }, null, "panels"],
  },
  {
    name: "key hints (HUD-03)",
    channel: overlayHintsChannel,
    on: (api: OverlayAPI, listener: (message: unknown) => void) =>
      api.onHints(listener),
    shown: shownHints,
    hidden: hiddenHints,
    invalid: [{ entries: "none" }, { language: "en" }, null, "hints"],
  },
];

describe.each(kept)("the preload's $name message", (kind) => {
  it("is given at once to a page that subscribes after Main sent it", async () => {
    // Main sends when the message changes and not again; the page may
    // subscribe after the first one.
    const api = await load();
    send(kind.channel, kind.shown);
    const listener = vi.fn();
    kind.on(api, listener);
    expect(listener).toHaveBeenCalledExactlyOnceWith(kind.shown);
  });

  it("has nothing to give a page before Main has sent any", async () => {
    const api = await load();
    const listener = vi.fn();
    kind.on(api, listener);
    expect(listener).not.toHaveBeenCalled();
  });

  it("is only the latest one that is given", async () => {
    const api = await load();
    send(kind.channel, kind.shown);
    send(kind.channel, kind.hidden);
    send(kind.channel, kind.shown);
    const listener = vi.fn();
    kind.on(api, listener);
    expect(listener).toHaveBeenCalledExactlyOnceWith(kind.shown);
  });

  it("is replaced by one that hides, so that a page that subscribes then is told there is nothing to show", async () => {
    const api = await load();
    send(kind.channel, kind.shown);
    send(kind.channel, kind.hidden);
    const listener = vi.fn();
    kind.on(api, listener);
    expect(listener).toHaveBeenCalledExactlyOnceWith(kind.hidden);
  });

  it("is not replaced by a message that is not one, and does not reach the page", async () => {
    const api = await load();
    send(kind.channel, kind.shown);
    const early = vi.fn();
    kind.on(api, early);
    early.mockClear();
    for (const payload of kind.invalid) send(kind.channel, payload);
    expect(early).not.toHaveBeenCalled();
    const late = vi.fn();
    kind.on(api, late);
    expect(late).toHaveBeenCalledExactlyOnceWith(kind.shown);
  });

  it("reaches a page that has subscribed as it comes, until it stops", async () => {
    const api = await load();
    const listener = vi.fn();
    const stop = kind.on(api, listener);
    send(kind.channel, kind.shown);
    send(kind.channel, kind.hidden);
    expect(listener.mock.calls).toEqual([[kind.shown], [kind.hidden]]);
    stop();
    send(kind.channel, kind.shown);
    expect(listener).toHaveBeenCalledTimes(2);
    // And a page that comes back is given the latest.
    const again = vi.fn();
    kind.on(api, again);
    expect(again).toHaveBeenCalledExactlyOnceWith(kind.shown);
  });

  it("is given to every page that subscribes, each once", async () => {
    const api = await load();
    send(kind.channel, kind.shown);
    const first = vi.fn();
    const second = vi.fn();
    kind.on(api, first);
    kind.on(api, second);
    expect(first).toHaveBeenCalledOnce();
    expect(second).toHaveBeenCalledOnce();
  });
});

describe("the preload's other messages", () => {
  it("keeps panels and hints apart", async () => {
    const api = await load();
    send(overlayPanelsChannel, shownPanels);
    const hints = vi.fn();
    api.onHints(hints);
    expect(hints).not.toHaveBeenCalled();
    send(overlayHintsChannel, shownHints);
    const panels = vi.fn();
    api.onPanels(panels);
    expect(panels).toHaveBeenCalledExactlyOnceWith(shownPanels);
    expect(hints).toHaveBeenCalledExactlyOnceWith(shownHints);
  });

  it("still keeps the page's settings for a page that subscribes late", async () => {
    const api = await load();
    send(overlayPrefsChannel, { opacity: 70 });
    send(overlayPrefsChannel, { opacity: "high" });
    const listener = vi.fn();
    api.onPrefs(listener);
    expect(listener).toHaveBeenCalledExactlyOnceWith({ opacity: 70 });
  });

  it("does not keep toasts: a toast is for the moment it is sent", async () => {
    const api = await load();
    send(overlayToastChannel, { text: "早了", ms: 800 });
    const listener = vi.fn();
    const stop = api.onToast(listener);
    expect(listener).not.toHaveBeenCalled();
    send(overlayToastChannel, { text: "脚本提示", ms: 800 });
    send(overlayToastChannel, { text: "状态" });
    send(overlayToastChannel, { text: "" });
    expect(listener.mock.calls).toEqual([
      ["脚本提示", 800],
      ["状态", undefined],
    ]);
    stop();
    send(overlayToastChannel, { text: "晚了" });
    expect(listener).toHaveBeenCalledTimes(2);
  });

  it("gives the page one frozen object with the four listeners and nothing else", async () => {
    const api = await load();
    expect(Object.isFrozen(api)).toBe(true);
    expect(Object.keys(api).sort()).toEqual([
      "onHints",
      "onPanels",
      "onPrefs",
      "onToast",
    ]);
    expect([...electron.exposed.keys()]).toEqual(["padmateOverlay"]);
  });
});
