// @vitest-environment jsdom
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  hintsFadeMs,
  hintsGap,
  hintsPanelPadding,
  hintsRowHeight,
  type KeyHint,
  type OverlayAPI,
  type OverlayHintsMessage,
  type OverlayPageSettings,
  type OverlayPanelsMessage,
  overlayToastAreaHeight,
  type ScriptPanel,
} from "../shared/overlay";
import { Overlay } from "./Overlay";
import {
  toastFadeInMs,
  toastFadeOutMs,
  toastHoldMs,
  toastSchedule,
} from "./overlay-toast";

const css = readFileSync(resolve(__dirname, "overlay.css"), "utf8");

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  delete window.padmateOverlay;
});

/**
 * The preload's API as the page sees it, and a way to send toasts, settings,
 * key hints and a script's panels through it. Like the preload, it hands a
 * page that subscribes late the latest settings it has been sent.
 */
function fakeApi(latest?: OverlayPageSettings) {
  const listeners = new Set<(text: string, ms?: number) => void>();
  const settingsListeners = new Set<(settings: OverlayPageSettings) => void>();
  const hintsListeners = new Set<(message: OverlayHintsMessage) => void>();
  const panelsListeners = new Set<(message: OverlayPanelsMessage) => void>();
  const stopped = vi.fn();
  const settingsStopped = vi.fn();
  const hintsStopped = vi.fn();
  const panelsStopped = vi.fn();
  let settings = latest;
  const api: OverlayAPI = {
    onToast: (listener) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
        stopped();
      };
    },
    onPrefs: (listener) => {
      settingsListeners.add(listener);
      if (settings) listener(settings);
      return () => {
        settingsListeners.delete(listener);
        settingsStopped();
      };
    },
    onHints: (listener) => {
      hintsListeners.add(listener);
      return () => {
        hintsListeners.delete(listener);
        hintsStopped();
      };
    },
    onPanels: (listener) => {
      panelsListeners.add(listener);
      return () => {
        panelsListeners.delete(listener);
        panelsStopped();
      };
    },
  };
  return {
    api,
    stopped,
    settingsStopped,
    hintsStopped,
    panelsStopped,
    listeners,
    settingsListeners,
    hintsListeners,
    panelsListeners,
    sendHints: (message: OverlayHintsMessage) =>
      act(() => {
        for (const listener of hintsListeners) listener(message);
      }),
    sendPanels: (message: OverlayPanelsMessage) =>
      act(() => {
        for (const listener of panelsListeners) listener(message);
      }),
    send: (text: string, ms?: number) =>
      act(() => {
        for (const listener of listeners) listener(text, ms);
      }),
    sendSettings: (next: OverlayPageSettings) =>
      act(() => {
        settings = next;
        for (const listener of settingsListeners) listener(next);
      }),
  };
}

const advance = (ms: number) =>
  act(() => {
    vi.advanceTimersByTime(ms);
  });
const root = () => screen.getByTestId("overlay-root");
const toast = () => screen.getByTestId("overlay-toast");

function reduceMotion(reduced: boolean) {
  const query = vi.fn((media: string) => ({
    media,
    matches: reduced,
    addEventListener: () => {},
    removeEventListener: () => {},
  }));
  vi.stubGlobal("matchMedia", query);
  return query;
}

describe("the overlay page (HUD-01)", () => {
  it("has its root element, hidden, and a toast element with nothing in it", () => {
    render(<Overlay api={fakeApi().api} />);
    expect(root().getAttribute("data-visible")).toBe("false");
    expect(toast().textContent).toBe("");
    expect(toast().getAttribute("data-phase")).toBe("idle");
    expect(root().childNodes).toHaveLength(1);
  });

  it("offers nothing to click, focus or type in", () => {
    const { container } = render(<Overlay api={fakeApi().api} />);
    expect(
      container.querySelectorAll(
        "a, button, input, select, textarea, iframe, [tabindex], [contenteditable]",
      ),
    ).toHaveLength(0);
  });

  it("shows and hides by the attribute, which is what the E2E spec toggles", () => {
    render(<Overlay api={fakeApi().api} />);
    // A page that changes the attribute from outside stays that way: the
    // component does not render again, so React leaves it alone.
    root().dataset.visible = "true";
    expect(root().getAttribute("data-visible")).toBe("true");
    root().dataset.visible = "false";
    expect(root().getAttribute("data-visible")).toBe("false");
  });

  it("renders without the preload's API, and shows nothing", () => {
    expect(window.padmateOverlay).toBeUndefined();
    render(<Overlay />);
    expect(root().getAttribute("data-visible")).toBe("false");
    expect(toast().textContent).toBe("");
  });

  it("takes its API from window.padmateOverlay, which the preload exposes", () => {
    const { api, send } = fakeApi();
    window.padmateOverlay = api;
    render(<Overlay />);
    send("已暂停");
    expect(toast().textContent).toBe("已暂停");
  });
});

describe("a toast (HUD-04)", () => {
  it("shows the text at once, then fades out after the fade in and the hold, and is gone", () => {
    const { api, send } = fakeApi();
    render(<Overlay api={api} />);
    send("配置：绘画");
    expect(toast().textContent).toBe("配置：绘画");
    expect(toast().getAttribute("data-phase")).toBe("in");
    expect(root().getAttribute("data-visible")).toBe("true");
    expect(toast().getAttribute("role")).toBe("status");

    // 120 ms to fade in, 1500 ms to hold: the fade out starts at 1620 ms.
    advance(toastFadeInMs + toastHoldMs - 1);
    expect(toast().getAttribute("data-phase")).toBe("in");
    advance(1);
    expect(toast().getAttribute("data-phase")).toBe("out");
    // It is still on screen while it fades, for 300 ms.
    expect(toast().textContent).toBe("配置：绘画");
    expect(root().getAttribute("data-visible")).toBe("true");
    advance(toastFadeOutMs - 1);
    expect(toast().textContent).toBe("配置：绘画");
    advance(1);
    expect(toast().textContent).toBe("");
    expect(toast().getAttribute("data-phase")).toBe("idle");
    expect(root().getAttribute("data-visible")).toBe("false");
    // Nothing is left to run.
    expect(vi.getTimerCount()).toBe(0);
  });

  it("is replaced at once by a new one, which gets its whole time", () => {
    const { api, send } = fakeApi();
    render(<Overlay api={api} />);
    send("已暂停");
    advance(1000);
    send("已恢复");
    expect(toast().textContent).toBe("已恢复");
    expect(toast().getAttribute("data-phase")).toBe("in");
    // The first toast would have started to fade at 1620 ms, 620 ms from
    // now: its timers ended with it.
    advance(toastFadeInMs + toastHoldMs - 1);
    expect(toast().textContent).toBe("已恢复");
    expect(toast().getAttribute("data-phase")).toBe("in");
    advance(1);
    expect(toast().getAttribute("data-phase")).toBe("out");
    advance(toastFadeOutMs);
    expect(toast().textContent).toBe("");
  });

  it("is replaced while it fades out: the new one is fully shown again and is not ended by the old one", () => {
    const { api, send } = fakeApi();
    render(<Overlay api={api} />);
    send("层：fn");
    advance(toastFadeInMs + toastHoldMs + 100);
    expect(toast().getAttribute("data-phase")).toBe("out");
    send("层：base");
    expect(toast().textContent).toBe("层：base");
    expect(toast().getAttribute("data-phase")).toBe("in");
    expect(root().getAttribute("data-visible")).toBe("true");
    // The old toast's end (200 ms from now) does not clear the new one.
    advance(toastFadeOutMs + 100);
    expect(toast().textContent).toBe("层：base");
    expect(toast().getAttribute("data-phase")).toBe("in");
  });

  it("comes again after it has gone", () => {
    const { api, send } = fakeApi();
    render(<Overlay api={api} />);
    send("已暂停");
    advance(toastSchedule(false).endAt);
    expect(toast().textContent).toBe("");
    send("已恢复");
    expect(toast().textContent).toBe("已恢复");
    expect(toast().getAttribute("data-phase")).toBe("in");
  });

  it("holds without fading when the system asks for no animation, and is gone after the hold", () => {
    const query = reduceMotion(true);
    const { api, send } = fakeApi();
    render(<Overlay api={api} />);
    send("已暂停");
    expect(query).toHaveBeenCalledWith("(prefers-reduced-motion: reduce)");
    advance(toastHoldMs - 1);
    expect(toast().textContent).toBe("已暂停");
    expect(toast().getAttribute("data-phase")).toBe("in");
    advance(1);
    expect(toast().textContent).toBe("");
    expect(root().getAttribute("data-visible")).toBe("false");
    expect(vi.getTimerCount()).toBe(0);
  });

  it("fades as usual when the system does not ask for no animation", () => {
    reduceMotion(false);
    const { api, send } = fakeApi();
    render(<Overlay api={api} />);
    send("已暂停");
    advance(toastHoldMs);
    expect(toast().textContent).toBe("已暂停");
    advance(toastFadeInMs + toastFadeOutMs);
    expect(toast().textContent).toBe("");
  });

  it("follows the setting for each toast", () => {
    const { api, send } = fakeApi();
    render(<Overlay api={api} />);
    reduceMotion(true);
    send("已暂停");
    advance(toastHoldMs);
    expect(toast().textContent).toBe("");
    reduceMotion(false);
    send("已恢复");
    advance(toastHoldMs);
    expect(toast().textContent).toBe("已恢复");
  });

  it("stops listening and ends its timers when the page goes", () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => {});
    const { api, send, stopped, listeners } = fakeApi();
    const view = render(<Overlay api={api} />);
    send("已暂停");
    expect(vi.getTimerCount()).toBeGreaterThan(0);
    view.unmount();
    expect(stopped).toHaveBeenCalledTimes(1);
    expect(listeners.size).toBe(0);
    expect(vi.getTimerCount()).toBe(0);
    advance(5000);
    expect(error).not.toHaveBeenCalled();
    error.mockRestore();
  });

  it("holds for as long as a script's toast says (SCR-22), the fades as before", () => {
    const { api, send } = fakeApi();
    render(<Overlay api={api} />);
    send("脚本提示", 4000);
    expect(toast().textContent).toBe("脚本提示");
    expect(toast().getAttribute("data-phase")).toBe("in");
    // Not the 1500 ms of the others: it is still fully shown after them.
    advance(toastFadeInMs + toastHoldMs + 100);
    expect(toast().getAttribute("data-phase")).toBe("in");
    advance(4000 - toastHoldMs - 100 - 1);
    expect(toast().getAttribute("data-phase")).toBe("in");
    advance(1);
    expect(toast().getAttribute("data-phase")).toBe("out");
    expect(toast().textContent).toBe("脚本提示");
    advance(toastFadeOutMs - 1);
    expect(toast().textContent).toBe("脚本提示");
    advance(1);
    expect(toast().textContent).toBe("");
    expect(root().getAttribute("data-visible")).toBe("false");
    expect(vi.getTimerCount()).toBe(0);
  });

  it("holds for the shortest and the longest time a script can ask for", () => {
    const { api, send } = fakeApi();
    render(<Overlay api={api} />);
    send("短", 500);
    advance(toastFadeInMs + 499);
    expect(toast().getAttribute("data-phase")).toBe("in");
    advance(1);
    expect(toast().getAttribute("data-phase")).toBe("out");
    advance(toastFadeOutMs);
    expect(toast().textContent).toBe("");
    send("长", 10000);
    advance(toastFadeInMs + 9999);
    expect(toast().getAttribute("data-phase")).toBe("in");
    advance(1);
    expect(toast().getAttribute("data-phase")).toBe("out");
  });

  it("holds without fading for the time a script asked for when the system asks for no animation", () => {
    reduceMotion(true);
    const { api, send } = fakeApi();
    render(<Overlay api={api} />);
    send("脚本提示", 3000);
    advance(2999);
    expect(toast().textContent).toBe("脚本提示");
    expect(toast().getAttribute("data-phase")).toBe("in");
    advance(1);
    expect(toast().textContent).toBe("");
    expect(vi.getTimerCount()).toBe(0);
  });

  it("is the same pill as the others: the newest replaces the one on screen, with its own hold", () => {
    const { api, send } = fakeApi();
    render(<Overlay api={api} />);
    send("脚本提示", 8000);
    advance(1000);
    send("已暂停");
    expect(toast().textContent).toBe("已暂停");
    expect(toast().getAttribute("data-phase")).toBe("in");
    // The script's toast would be held for another 7 s: its timers ended.
    advance(toastSchedule(false).endAt);
    expect(toast().textContent).toBe("");
    send("已恢复");
    advance(100);
    send("再一个脚本提示", 700);
    advance(toastFadeInMs + 700);
    expect(toast().getAttribute("data-phase")).toBe("out");
    advance(toastFadeOutMs);
    expect(toast().textContent).toBe("");
    expect(vi.getTimerCount()).toBe(0);
  });

  it("is one text node of the line it was given, never markup", () => {
    const { api, send } = fakeApi();
    render(<Overlay api={api} />);
    send("配置：<img src=x onerror=alert(1)>");
    expect(toast().textContent).toBe("配置：<img src=x onerror=alert(1)>");
    expect(toast().querySelector("img")).toBeNull();
  });
});

describe("the opacity setting (HUD-05)", () => {
  const opacityOf = () => root().style.getPropertyValue("--overlay-opacity");

  it("is 100 % until Main says otherwise", () => {
    render(<Overlay api={fakeApi().api} />);
    expect(opacityOf()).toBe("1");
  });

  it("multiplies the pill's opacity as a property of the root, for the next toast and the one on screen", () => {
    const { api, send, sendSettings } = fakeApi();
    render(<Overlay api={api} />);
    sendSettings({ opacity: 60 });
    expect(opacityOf()).toBe("0.6");
    send("已暂停");
    expect(toast().getAttribute("data-phase")).toBe("in");
    expect(opacityOf()).toBe("0.6");
    // A change while a toast is shown applies to it; the toast goes on.
    sendSettings({ opacity: 85 });
    expect(opacityOf()).toBe("0.85");
    expect(toast().textContent).toBe("已暂停");
    sendSettings({ opacity: 100 });
    expect(opacityOf()).toBe("1");
    // The settings neither make a toast nor hide one.
    advance(toastSchedule(false).endAt);
    expect(toast().textContent).toBe("");
    expect(root().getAttribute("data-visible")).toBe("false");
  });

  it("takes the settings Main sent before the page subscribed", () => {
    render(<Overlay api={fakeApi({ opacity: 45 }).api} />);
    expect(opacityOf()).toBe("0.45");
  });

  it("stops listening for them when the page goes", () => {
    const { api, settingsListeners, settingsStopped } = fakeApi();
    const view = render(<Overlay api={api} />);
    expect(settingsListeners.size).toBe(1);
    view.unmount();
    expect(settingsStopped).toHaveBeenCalledTimes(1);
    expect(settingsListeners.size).toBe(0);
  });

  it("scales only the pill's background: the text stays opaque", () => {
    const pill = /\.overlay-toast \{([^}]*)\}/.exec(css)?.[1] ?? "";
    expect(pill).toContain(
      "background: rgba(20, 20, 24, calc(0.82 * var(--overlay-opacity, 1)));",
    );
    // The toast itself fades in to full opacity, so its text is not dimmed.
    expect(css).toMatch(/\.overlay-toast\[data-phase="in"\] \{\s*opacity: 1;/);
    // The root is not dimmed: its opacity is the data-visible switch alone.
    expect(css).toMatch(/\[data-visible="true"\]\s*\{\s*opacity:\s*1;/);
    expect(css).not.toMatch(/\.overlay-root[^{]*\{[^}]*--overlay-opacity/);
  });
});

describe("the toast's schedule", () => {
  it("is 120 ms in, 1500 ms held, 300 ms out", () => {
    expect([toastFadeInMs, toastHoldMs, toastFadeOutMs]).toEqual([
      120, 1500, 300,
    ]);
    expect(toastSchedule(false)).toEqual({ fadeOutAt: 1620, endAt: 1920 });
    expect(toastSchedule(true)).toEqual({ fadeOutAt: 1500, endAt: 1500 });
  });
});

describe("the overlay's style", () => {
  it("fades the toast with CSS transitions of the same lengths, and not the root", () => {
    // The fade out is the toast's own transition, the fade in the one of
    // its "in" phase.
    expect(css).toMatch(
      new RegExp(
        `\\.overlay-toast \\{[^}]*transition: opacity ${toastFadeOutMs}ms linear;`,
      ),
    );
    expect(css).toMatch(
      new RegExp(
        `\\.overlay-toast\\[data-phase="in"\\] \\{\\s*opacity: 1;\\s*transition-duration: ${toastFadeInMs}ms;`,
      ),
    );
    // The root switches at once: the SPK-09 E2E reads its opacity right
    // after toggling it.
    const rootRules = css
      .replace(/\/\*[\s\S]*?\*\//g, "")
      .split("}")
      .filter((rule) => rule.includes(".overlay-root"));
    expect(rootRules.length).toBeGreaterThan(0);
    for (const rule of rootRules) expect(rule).not.toMatch(/transition/);
  });

  it("does not animate under prefers-reduced-motion", () => {
    expect(css).toMatch(
      /@media \(prefers-reduced-motion: reduce\) \{[^@]*\.overlay-toast,\s*\.overlay-toast\[data-phase="in"\],\s*\.overlay-hints,\s*\.overlay-hints\[data-visible="true"\] \{\s*transition: none;/,
    );
  });

  it("is a single dark pill, centred, on one line", () => {
    const pill = /\.overlay-toast \{([^}]*)\}/.exec(css)?.[1] ?? "";
    for (const declaration of [
      "padding: 10px 18px;",
      "border-radius: 999px;",
      "background: rgba(20, 20, 24, calc(0.82 * var(--overlay-opacity, 1)));",
      "color: #fff;",
      "font-size: 16px;",
      "white-space: nowrap;",
      "overflow: hidden;",
      "text-overflow: ellipsis;",
      "opacity: 0;",
    ])
      expect(pill, declaration).toContain(declaration);
    // No icon, no border, no shadow.
    expect(pill).not.toMatch(/border:|box-shadow|background-image/);
    const rootRule = /\.overlay-root \{([^}]*)\}/.exec(css)?.[1] ?? "";
    expect(rootRule).toContain("display: flex;");
    expect(rootRule).toContain("align-items: center;");
    expect(rootRule).toContain("justify-content: center;");
    expect(rootRule).toContain("pointer-events: none;");
  });
});

describe("the key hints panel (HUD-03)", () => {
  const panel = () => screen.getByTestId("overlay-hints");
  const rows = () =>
    [...panel().querySelectorAll(".overlay-hint")].map((row) => [
      row.querySelector(".overlay-hint-control")?.textContent,
      row.querySelector(".overlay-hint-label")?.textContent,
    ]);
  const message = (
    entries: KeyHint[],
    extra: Partial<OverlayHintsMessage> = {},
  ): OverlayHintsMessage => ({
    entries,
    language: "zh-CN",
    position: "bottom",
    ...extra,
  });
  const hint = (control: string, label: string): KeyHint => ({
    control,
    label,
  });
  // Controls the panel knows, in the order it puts them (overlay-hints.ts).
  const known = [
    "south",
    "east",
    "west",
    "north",
    "leftshoulder",
    "rightshoulder",
    "lt",
    "rt",
    "dpad.up",
    "dpad.down",
    "dpad.left",
    "dpad.right",
    "back",
    "start",
    "guide",
    "leftstick",
    "rightstick",
    "ls.up",
    "ls.down",
    "ls.left",
  ];
  const many = (count: number): KeyHint[] =>
    known
      .slice(0, count)
      .map((control, index) => hint(control, `动作${index + 1}`));

  it("is always there, hidden and empty, outside the toast's root", () => {
    render(<Overlay api={fakeApi().api} />);
    expect(panel().getAttribute("data-visible")).toBe("false");
    expect(panel().getAttribute("aria-hidden")).toBe("true");
    expect(panel().textContent).toBe("");
    expect(root().contains(panel())).toBe(false);
    // The root keeps its one child and its meaning for the toast alone.
    expect(root().childNodes).toHaveLength(1);
    expect(root().getAttribute("data-visible")).toBe("false");
  });

  it("shows each control with what it does, the control named as the key list names it", () => {
    const { api, sendHints } = fakeApi();
    render(<Overlay api={api} />);
    sendHints(
      message([
        hint("south", "撤销"),
        hint("rightshoulder", "重做"),
        hint("ls.up", "放大"),
        hint("lt", ""),
      ]),
    );
    expect(panel().getAttribute("data-visible")).toBe("true");
    expect(panel().getAttribute("aria-hidden")).toBeNull();
    // The generic names, as Main did not know the pad.
    expect(rows()).toEqual([
      ["S", "撤销"],
      ["R", "重做"],
      ["LT", ""],
      ["LS ↑", "放大"],
    ]);
    // The toast is not shown for it.
    expect(root().getAttribute("data-visible")).toBe("false");
  });

  it("names the controls for the pad the hints are for", () => {
    const { api, sendHints } = fakeApi();
    render(<Overlay api={api} />);
    const entries = [hint("south", "a"), hint("rightshoulder", "b")];
    sendHints(message(entries, { pad: { type: "xboxone" } }));
    expect(rows().map(([name]) => name)).toEqual(["A", "RB"]);
    sendHints(message(entries, { pad: { type: "ps5" } }));
    expect(rows().map(([name]) => name)).toEqual(["×", "R1"]);
    sendHints(
      message(entries, {
        pad: { type: "switchpro", buttonLabels: { south: "A" } },
      }),
    );
    expect(rows().map(([name]) => name)).toEqual(["A", "R"]);
  });

  it("names the controls in the UI's language", () => {
    const { api, sendHints } = fakeApi();
    render(<Overlay api={api} />);
    const entries = [
      hint("dpad.up", "x"),
      hint("ls.outer", "y"),
      hint("rt", "z"),
    ];
    sendHints(message(entries, { language: "zh-CN" }));
    expect(rows().map(([name]) => name)).toEqual(["RT", "十字键 ↑", "LS 重推"]);
    sendHints(message(entries, { language: "en" }));
    expect(rows().map(([name]) => name)).toEqual([
      "RT",
      "D-pad ↑",
      "LS hard push",
    ]);
  });

  it("puts them in the order of the pad, whatever order they came in", () => {
    const { api, sendHints } = fakeApi();
    render(<Overlay api={api} />);
    sendHints(
      message([
        hint("touchpad", "touchpad"),
        hint("ls.up", "stick"),
        hint("start", "start"),
        hint("dpad.left", "dpad"),
        hint("rt", "trigger"),
        hint("north", "north"),
        hint("south", "south"),
        hint("zz.unknown", "unknown"),
      ]),
    );
    expect(rows().map(([, label]) => label)).toEqual([
      "south",
      "north",
      "trigger",
      "dpad",
      "start",
      "stick",
      "touchpad",
      "unknown",
    ]);
  });

  it("shows the labels as text, never as markup", () => {
    const { api, sendHints } = fakeApi();
    render(<Overlay api={api} />);
    sendHints(message([hint("south", "<img src=x onerror=alert(1)>")]));
    expect(rows()[0][1]).toBe("<img src=x onerror=alert(1)>");
    expect(panel().querySelector("img")).toBeNull();
  });

  it("lays out in two columns: the rows are half the entries, filled column by column", () => {
    const { api, sendHints } = fakeApi();
    render(<Overlay api={api} />);
    sendHints(message(many(5)));
    const list = panel().querySelector<HTMLElement>(".overlay-hints-list");
    expect(list?.style.getPropertyValue("--hint-rows")).toBe("3");
    expect(rows()).toHaveLength(5);
    sendHints(message(many(16)));
    expect(list?.style.getPropertyValue("--hint-rows")).toBe("8");
    expect(panel().textContent).not.toContain("…");
  });

  it("shows sixteen entries at most, the first sixteen in order, and a last line for more", () => {
    const { api, sendHints } = fakeApi();
    render(<Overlay api={api} />);
    // Given backwards: the first sixteen in the pad's order are kept, not the
    // first sixteen sent.
    sendHints(message(many(20).reverse()));
    expect(rows()).toHaveLength(16);
    expect(rows().map(([, label]) => label)).toEqual(
      many(16).map((entry) => entry.label),
    );
    const more = panel().querySelector(".overlay-hints-more");
    expect(more?.textContent).toBe("…");
    expect(panel().lastElementChild).toBe(more);
  });

  it("fades out over 120 ms keeping its entries, and is empty after", () => {
    const { api, sendHints } = fakeApi();
    render(<Overlay api={api} />);
    sendHints(message([hint("south", "撤销")]));
    sendHints(message([]));
    expect(panel().getAttribute("data-visible")).toBe("false");
    expect(rows()).toEqual([["S", "撤销"]]);
    advance(hintsFadeMs - 1);
    expect(rows()).toEqual([["S", "撤销"]]);
    advance(1);
    expect(rows()).toEqual([]);
    expect(panel().textContent).toBe("");
    expect(vi.getTimerCount()).toBe(0);
  });

  it("comes back during the fade out and is not emptied by it", () => {
    const { api, sendHints } = fakeApi();
    render(<Overlay api={api} />);
    sendHints(message([hint("south", "撤销")]));
    sendHints(message([]));
    advance(60);
    sendHints(message([hint("east", "重做")]));
    advance(1000);
    expect(panel().getAttribute("data-visible")).toBe("true");
    expect(rows()).toEqual([["E", "重做"]]);
  });

  it("goes at once when the system asks for no animation", () => {
    reduceMotion(true);
    const { api, sendHints } = fakeApi();
    render(<Overlay api={api} />);
    sendHints(message([hint("south", "撤销")]));
    sendHints(message([]));
    advance(0);
    expect(rows()).toEqual([]);
  });

  it("hiding when nothing is shown does nothing", () => {
    const { api, sendHints } = fakeApi();
    render(<Overlay api={api} />);
    sendHints(message([]));
    expect(panel().textContent).toBe("");
    advance(500);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("goes on the side of the toast area away from the screen's edge: the page knows where the window sits", () => {
    const { api, sendHints } = fakeApi();
    const { container } = render(<Overlay api={api} />);
    const page = container.querySelector(".overlay-page");
    expect(page?.getAttribute("data-position")).toBe("bottom");
    sendHints(message([hint("south", "a")], { position: "top" }));
    expect(page?.getAttribute("data-position")).toBe("top");
    sendHints(message([], { position: "bottom" }));
    expect(page?.getAttribute("data-position")).toBe("bottom");
  });

  it("shows while a toast does, and each keeps its own timers", () => {
    const { api, send, sendHints } = fakeApi();
    render(<Overlay api={api} />);
    sendHints(message([hint("south", "撤销")]));
    send("已暂停");
    expect(root().getAttribute("data-visible")).toBe("true");
    expect(panel().getAttribute("data-visible")).toBe("true");
    advance(toastSchedule(false).endAt);
    expect(root().getAttribute("data-visible")).toBe("false");
    // The toast's timers ended; the hints go on until they are told to stop.
    expect(panel().getAttribute("data-visible")).toBe("true");
    expect(rows()).toHaveLength(1);
  });

  it("follows the opacity setting with its background, as the pill does", () => {
    const { api, sendSettings, sendHints } = fakeApi();
    render(<Overlay api={api} />);
    sendHints(message([hint("south", "a")]));
    sendSettings({ opacity: 60 });
    expect(panel().style.getPropertyValue("--overlay-opacity")).toBe("0.6");
  });

  it("stops listening and ends its timer when the page goes", () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => {});
    const { api, sendHints, hintsListeners, hintsStopped } = fakeApi();
    const view = render(<Overlay api={api} />);
    sendHints(message([hint("south", "a")]));
    sendHints(message([]));
    expect(vi.getTimerCount()).toBe(1);
    view.unmount();
    expect(hintsStopped).toHaveBeenCalledTimes(1);
    expect(hintsListeners.size).toBe(0);
    expect(vi.getTimerCount()).toBe(0);
    advance(1000);
    expect(error).not.toHaveBeenCalled();
    error.mockRestore();
  });

  it("offers nothing to click, focus or type in", () => {
    const { api, sendHints } = fakeApi();
    const { container } = render(<Overlay api={api} />);
    sendHints(message(many(18)));
    expect(
      container.querySelectorAll(
        "a, button, input, select, textarea, iframe, [tabindex], [contenteditable]",
      ),
    ).toHaveLength(0);
  });

  it("renders without the preload's API and shows nothing", () => {
    render(<Overlay />);
    expect(panel().textContent).toBe("");
  });
});

describe("the key hints panel's style (HUD-03)", () => {
  // The declarations of the first rule with exactly this selector.
  const rule = (selector: string) => {
    const start = css.indexOf(`${selector} {`);
    if (start < 0) return "";
    return css.slice(start, css.indexOf("}", start));
  };

  it("is as tall as Main makes the window for it: the rows, the padding and the gap are the shared numbers", () => {
    const list = rule(".overlay-hints-list");
    expect(list).toContain(
      `grid-template-rows: repeat(var(--hint-rows, 1), ${hintsRowHeight}px);`,
    );
    expect(list).toContain("grid-template-columns: repeat(2, minmax(0, 1fr));");
    expect(list).toContain("grid-auto-flow: column;");
    expect(rule(".overlay-hint")).toContain(`height: ${hintsRowHeight}px;`);
    expect(rule(".overlay-hints-more")).toContain(
      `height: ${hintsRowHeight}px;`,
    );
    expect(rule(".overlay-hints")).toContain(
      `padding: ${hintsPanelPadding}px 14px;`,
    );
    expect(rule(".overlay-hints")).toContain(
      `line-height: ${hintsRowHeight}px;`,
    );
    // Away from the toast area by the gap, which is the toast area's height
    // from the window's edge.
    for (const side of ["bottom", "top"])
      expect(
        rule(`.overlay-page[data-position="${side}"] .overlay-hints`),
      ).toContain(
        `${side}: calc(${overlayToastAreaHeight}px + ${hintsGap}px);`,
      );
  });

  it("keeps the toast area at the window's edge: 160 px, at the bottom or the top", () => {
    expect(rule(".overlay-root")).toContain(
      `height: min(${overlayToastAreaHeight}px, 100%);`,
    );
    expect(rule(".overlay-root")).toContain("position: absolute;");
    expect(
      rule('.overlay-page[data-position="bottom"] .overlay-root'),
    ).toContain("bottom: 0;");
  });

  it("fades over 120 ms and is dark like the pill, with the background alone following the opacity", () => {
    const base = rule(".overlay-hints");
    expect(base).toMatch(
      new RegExp(
        `transition:\\s*opacity ${hintsFadeMs}ms linear,\\s*visibility 0s linear ${hintsFadeMs}ms;`,
      ),
    );
    expect(base).toContain(
      "background: rgba(20, 20, 24, calc(0.82 * var(--overlay-opacity, 1)));",
    );
    expect(base).toContain("color: #fff;");
    expect(base).toContain("opacity: 0;");
    expect(base).not.toMatch(/border:|box-shadow|animation/);
    expect(rule('.overlay-hints[data-visible="true"]')).toContain(
      "opacity: 1;",
    );
    // Nothing moves but the opacity.
    expect(css).not.toMatch(/@keyframes/);
  });

  it("is a clip of text on one line each, with the control name muted", () => {
    expect(rule(".overlay-hint")).toContain("white-space: nowrap;");
    expect(rule(".overlay-hint-label")).toContain("text-overflow: ellipsis;");
    expect(rule(".overlay-hint-control")).toContain("text-overflow: ellipsis;");
    expect(rule(".overlay-hint-control")).toContain(
      "color: rgba(255, 255, 255, 0.65);",
    );
  });
});

describe("a script's panels (SCR-22)", () => {
  const panels = () => screen.getByTestId("overlay-panels");
  const hintsPanel = () => screen.getByTestId("overlay-hints");
  const rows = () =>
    [...panels().querySelectorAll("li")].map((row) =>
      row.classList.contains("overlay-panel-title")
        ? ["title", row.textContent]
        : [
            row.querySelector(".overlay-panel-left")?.textContent,
            row.querySelector(".overlay-panel-right")?.textContent,
          ],
    );
  const message = (
    list: ScriptPanel[],
    position: "top" | "bottom" = "bottom",
  ): OverlayPanelsMessage => ({ panels: list, position });
  const panel = (
    items: Array<[string, string]>,
    title?: string,
  ): ScriptPanel => (title ? { title, rows: items } : { rows: items });
  const lines = (count: number, prefix = "行"): Array<[string, string]> =>
    Array.from({ length: count }, (_, index): [string, string] => [
      `${prefix}${index + 1}`,
      `值${index + 1}`,
    ]);
  const hintMessage = (): OverlayHintsMessage => ({
    entries: [{ control: "south", label: "撤销" }],
    language: "zh-CN",
    position: "bottom",
  });
  const noHints = (): OverlayHintsMessage => ({
    entries: [],
    language: "zh-CN",
    position: "bottom",
  });

  it("is always there, hidden and empty, outside the toast's root, in the place and style of the hints panel", () => {
    render(<Overlay api={fakeApi().api} />);
    expect(panels().getAttribute("data-visible")).toBe("false");
    expect(panels().getAttribute("aria-hidden")).toBe("true");
    expect(panels().textContent).toBe("");
    expect(root().contains(panels())).toBe(false);
    expect(panels().contains(hintsPanel())).toBe(false);
    // The hints' class gives it the place, the dark background and the fades.
    expect(panels().classList.contains("overlay-hints")).toBe(true);
    // The root keeps its one child and its meaning for the toast alone.
    expect(root().childNodes).toHaveLength(1);
    expect(root().getAttribute("data-visible")).toBe("false");
  });

  it("shows a title as a row of its own and each row as its left and right text, in order, one after the other", () => {
    const { api, sendPanels } = fakeApi();
    render(<Overlay api={api} />);
    sendPanels(
      message([
        panel(
          [
            ["大小", "12"],
            ["硬度", "80"],
          ],
          "画笔",
        ),
        panel([["图层", "3"]]),
        panel([["R", "255"]], "颜色"),
      ]),
    );
    expect(panels().getAttribute("data-visible")).toBe("true");
    expect(panels().getAttribute("aria-hidden")).toBeNull();
    expect(rows()).toEqual([
      ["title", "画笔"],
      ["大小", "12"],
      ["硬度", "80"],
      ["图层", "3"],
      ["title", "颜色"],
      ["R", "255"],
    ]);
    // The toast is not shown for it, and the hints' panel is not touched.
    expect(root().getAttribute("data-visible")).toBe("false");
    expect(hintsPanel().textContent).toBe("");
    expect(hintsPanel().getAttribute("data-visible")).toBe("false");
  });

  it("shows the texts as they are given: no control names, no mapping", () => {
    const { api, sendPanels } = fakeApi();
    render(<Overlay api={api} />);
    sendPanels(message([panel([["south", "ls.up"]])]));
    expect(rows()).toEqual([["south", "ls.up"]]);
  });

  it("shows the texts as text, never as markup", () => {
    const { api, sendPanels } = fakeApi();
    render(<Overlay api={api} />);
    sendPanels(
      message([
        panel([["<b>x</b>", "<img src=x onerror=alert(1)>"]], "<i>t</i>"),
      ]),
    );
    expect(rows()).toEqual([
      ["title", "<i>t</i>"],
      ["<b>x</b>", "<img src=x onerror=alert(1)>"],
    ]);
    expect(panels().querySelector("img, b, i")).toBeNull();
  });

  it("shows sixteen rows at most, titles counting, and a last line for more", () => {
    const { api, sendPanels } = fakeApi();
    render(<Overlay api={api} />);
    // A title and fifteen rows are sixteen: nothing more.
    sendPanels(message([panel(lines(15), "画笔")]));
    expect(rows()).toHaveLength(16);
    expect(panels().querySelector(".overlay-hints-more")).toBeNull();
    // Two panels, 10 + 10 rows with titles: the first sixteen, then "…".
    sendPanels(message([panel(lines(9), "甲"), panel(lines(9, "乙"), "乙")]));
    expect(rows()).toHaveLength(16);
    expect(rows()[0]).toEqual(["title", "甲"]);
    expect(rows().at(-1)).toEqual(["乙5", "值5"]);
    const more = panels().querySelector(".overlay-hints-more");
    expect(more?.textContent).toBe("…");
    expect(panels().lastElementChild).toBe(more);
  });

  it("fades out over 120 ms keeping its rows, and is empty after", () => {
    const { api, sendPanels } = fakeApi();
    render(<Overlay api={api} />);
    sendPanels(message([panel([["大小", "12"]], "画笔")]));
    sendPanels(message([]));
    expect(panels().getAttribute("data-visible")).toBe("false");
    expect(rows()).toHaveLength(2);
    advance(hintsFadeMs - 1);
    expect(rows()).toHaveLength(2);
    advance(1);
    expect(rows()).toEqual([]);
    expect(panels().textContent).toBe("");
    expect(vi.getTimerCount()).toBe(0);
  });

  it("changes in place when the values do, without fading", () => {
    const { api, sendPanels } = fakeApi();
    render(<Overlay api={api} />);
    sendPanels(message([panel([["大小", "12"]])]));
    sendPanels(message([panel([["大小", "14"]])]));
    expect(rows()).toEqual([["大小", "14"]]);
    expect(panels().getAttribute("data-visible")).toBe("true");
    expect(vi.getTimerCount()).toBe(0);
  });

  it("comes back during the fade out and is not emptied by it", () => {
    const { api, sendPanels } = fakeApi();
    render(<Overlay api={api} />);
    sendPanels(message([panel([["大小", "12"]])]));
    sendPanels(message([]));
    advance(60);
    sendPanels(message([panel([["硬度", "80"]])]));
    advance(1000);
    expect(panels().getAttribute("data-visible")).toBe("true");
    expect(rows()).toEqual([["硬度", "80"]]);
  });

  it("goes at once when the system asks for no animation", () => {
    reduceMotion(true);
    const { api, sendPanels } = fakeApi();
    render(<Overlay api={api} />);
    sendPanels(message([panel([["大小", "12"]])]));
    sendPanels(message([]));
    advance(0);
    expect(rows()).toEqual([]);
  });

  it("hiding when nothing is shown does nothing", () => {
    const { api, sendPanels } = fakeApi();
    render(<Overlay api={api} />);
    sendPanels(message([]));
    advance(500);
    expect(panels().textContent).toBe("");
    expect(vi.getTimerCount()).toBe(0);
  });

  it("goes on the side of the toast area away from the screen's edge: the page knows where the window sits", () => {
    const { api, sendPanels } = fakeApi();
    const { container } = render(<Overlay api={api} />);
    const page = container.querySelector(".overlay-page");
    expect(page?.getAttribute("data-position")).toBe("bottom");
    sendPanels(message([panel([["a", "b"]])], "top"));
    expect(page?.getAttribute("data-position")).toBe("top");
    sendPanels(message([], "bottom"));
    expect(page?.getAttribute("data-position")).toBe("bottom");
  });

  it("gives way to the key hints, which win, and comes back when they are gone", () => {
    const { api, sendPanels, sendHints } = fakeApi();
    render(<Overlay api={api} />);
    sendPanels(message([panel([["大小", "12"]], "画笔")]));
    expect(panels().getAttribute("data-visible")).toBe("true");
    sendHints(hintMessage());
    // The hints show at once and the panels are held back, keeping their rows.
    expect(hintsPanel().getAttribute("data-visible")).toBe("true");
    expect(panels().getAttribute("data-visible")).toBe("false");
    expect(panels().getAttribute("aria-hidden")).toBe("true");
    expect(rows()).toHaveLength(2);
    // The hints fade out; the panels come back only when they are gone.
    sendHints(noHints());
    expect(hintsPanel().getAttribute("data-visible")).toBe("false");
    expect(panels().getAttribute("data-visible")).toBe("false");
    advance(hintsFadeMs - 1);
    expect(panels().getAttribute("data-visible")).toBe("false");
    advance(1);
    expect(panels().getAttribute("data-visible")).toBe("true");
    expect(panels().getAttribute("aria-hidden")).toBeNull();
    expect(rows()).toEqual([
      ["title", "画笔"],
      ["大小", "12"],
    ]);
  });

  it("is updated while the hints are up, and shows the latest when they go", () => {
    const { api, sendPanels, sendHints } = fakeApi();
    render(<Overlay api={api} />);
    sendHints(hintMessage());
    sendPanels(message([panel([["大小", "12"]])]));
    expect(panels().getAttribute("data-visible")).toBe("false");
    sendPanels(message([panel([["大小", "16"]])]));
    sendHints(noHints());
    advance(hintsFadeMs);
    expect(panels().getAttribute("data-visible")).toBe("true");
    expect(rows()).toEqual([["大小", "16"]]);
  });

  it("stays hidden when the script ended them while the hints were up", () => {
    const { api, sendPanels, sendHints } = fakeApi();
    render(<Overlay api={api} />);
    sendPanels(message([panel([["大小", "12"]])]));
    sendHints(hintMessage());
    sendPanels(message([]));
    advance(hintsFadeMs);
    expect(rows()).toEqual([]);
    sendHints(noHints());
    advance(hintsFadeMs);
    expect(panels().getAttribute("data-visible")).toBe("false");
    expect(vi.getTimerCount()).toBe(0);
  });

  it("shows while a toast does, and each keeps its own timers", () => {
    const { api, send, sendPanels } = fakeApi();
    render(<Overlay api={api} />);
    sendPanels(message([panel([["大小", "12"]])]));
    send("脚本提示", 1000);
    expect(root().getAttribute("data-visible")).toBe("true");
    expect(panels().getAttribute("data-visible")).toBe("true");
    advance(toastSchedule(false, 1000).endAt);
    expect(root().getAttribute("data-visible")).toBe("false");
    expect(panels().getAttribute("data-visible")).toBe("true");
    expect(rows()).toHaveLength(1);
  });

  it("follows the opacity setting with its background, as the pill does", () => {
    const { api, sendSettings, sendPanels } = fakeApi();
    render(<Overlay api={api} />);
    sendPanels(message([panel([["a", "b"]])]));
    sendSettings({ opacity: 60 });
    expect(panels().style.getPropertyValue("--overlay-opacity")).toBe("0.6");
  });

  it("stops listening and ends its timer when the page goes", () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => {});
    const { api, sendPanels, panelsListeners, panelsStopped } = fakeApi();
    const view = render(<Overlay api={api} />);
    expect(panelsListeners.size).toBe(1);
    sendPanels(message([panel([["a", "b"]])]));
    sendPanels(message([]));
    expect(vi.getTimerCount()).toBe(1);
    view.unmount();
    expect(panelsStopped).toHaveBeenCalledTimes(1);
    expect(panelsListeners.size).toBe(0);
    expect(vi.getTimerCount()).toBe(0);
    advance(1000);
    expect(error).not.toHaveBeenCalled();
    error.mockRestore();
  });

  it("offers nothing to click, focus or type in", () => {
    const { api, sendPanels } = fakeApi();
    const { container } = render(<Overlay api={api} />);
    sendPanels(message([panel(lines(20), "画笔")]));
    expect(
      container.querySelectorAll(
        "a, button, input, select, textarea, iframe, [tabindex], [contenteditable]",
      ),
    ).toHaveLength(0);
  });

  it("renders without the preload's API and shows nothing", () => {
    render(<Overlay />);
    expect(panels().textContent).toBe("");
  });
});

describe("a script's panels' style (SCR-22)", () => {
  // The declarations of the first rule with exactly this selector.
  const rule = (selector: string) => {
    const start = css.indexOf(`${selector} {`);
    if (start < 0) return "";
    return css.slice(start, css.indexOf("}", start));
  };

  it("has rows as tall as the hints' rows, which is what Main makes the window tall for", () => {
    const row = css.slice(
      css.indexOf(".overlay-panel-title,\n.overlay-panel-row {"),
    );
    expect(row.slice(0, row.indexOf("}"))).toContain(
      `height: ${hintsRowHeight}px;`,
    );
    expect(rule(".overlay-hints")).toContain(
      `line-height: ${hintsRowHeight}px;`,
    );
    expect(rule(".overlay-hints")).toContain(
      `padding: ${hintsPanelPadding}px 14px;`,
    );
    // One column of rows, nothing like the hints' grid.
    expect(rule(".overlay-panels-list")).not.toContain("grid");
  });

  it("puts the left text muted and the right text at the end of the row, and the title in bold", () => {
    expect(rule(".overlay-panel-left")).toContain(
      "color: rgba(255, 255, 255, 0.65);",
    );
    expect(rule(".overlay-panel-left")).toContain("text-overflow: ellipsis;");
    expect(rule(".overlay-panel-right")).toContain("text-align: right;");
    expect(rule(".overlay-panel-right")).toContain("text-overflow: ellipsis;");
    // The row is a flex line; the rule it shares with the title is the height.
    expect(css).toMatch(/\.overlay-panel-row \{[^}]*display: flex;/);
    expect(rule(".overlay-panel-title")).toContain("font-weight: 600;");
  });

  it("uses no colour that the rest of the page does not", () => {
    const panelsCss = css.slice(css.indexOf(".overlay-panels-list"));
    const colours = new Set(panelsCss.match(/rgba?\([^)]*\)|#[0-9a-f]{3,6}/gi));
    expect([...colours]).toEqual(["rgba(255, 255, 255, 0.65)"]);
  });
});
