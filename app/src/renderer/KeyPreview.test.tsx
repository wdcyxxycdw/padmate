// @vitest-environment jsdom
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PadmateAPI } from "../shared/api";
import { controllerLabels } from "./controller-model";
import { editorAPIFixture } from "./editor-api-fixture";
import { FADE_MS, KEY_PREVIEW_MS, KeyPreview } from "./KeyPreview";
import { createPadmateStore, type PadmateStore } from "./store";

const labels = controllerLabels("xboxone");

function press(
  store: PadmateStore,
  action: string,
  { control = "south", device = 1 as number | null } = {},
) {
  act(() => {
    store.setState({
      keyPreview: {
        seq: (store.getState().keyPreview?.seq ?? 0) + 1,
        device,
        control,
        action,
      },
    });
  });
}

function setup(focused = () => true) {
  const store = createPadmateStore(editorAPIFixture() as unknown as PadmateAPI);
  const shown = vi.fn();
  const view = render(
    <KeyPreview
      store={store}
      device={1}
      labels={labels}
      family="xbox"
      scriptActions={[{ name: "cycleBrush", label: "切换笔刷" }]}
      language="zh-CN"
      onShown={shown}
      focused={focused}
    />,
  );
  return { store, shown, view, line: () => screen.getByTestId("key-preview") };
}

describe("key preview (UI-44)", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  it("says what a press sends, politely, then fades", () => {
    const { store, shown, line } = setup();
    expect(line().textContent).toBe("");
    expect(line().getAttribute("aria-live")).toBe("polite");
    press(store, "tap Ctrl+Shift+Z");
    expect(line().textContent).toBe("A → Ctrl+Shift+Z");
    expect(line().getAttribute("data-shown")).toBe("true");
    expect(shown).toHaveBeenLastCalledWith(true);
    // It never takes the focus.
    expect(document.activeElement).toBe(document.body);
    act(() => vi.advanceTimersByTime(KEY_PREVIEW_MS - 1));
    expect(line().getAttribute("data-shown")).toBe("true");
    act(() => vi.advanceTimersByTime(1));
    expect(line().hasAttribute("data-shown")).toBe(false);
    // The page's hint waits until the line has faded out.
    expect(shown).toHaveBeenLastCalledWith(true);
    act(() => vi.advanceTimersByTime(FADE_MS - 1));
    expect(shown).toHaveBeenLastCalledWith(true);
    act(() => vi.advanceTimersByTime(1));
    expect(shown).toHaveBeenLastCalledWith(false);
    expect(line().textContent).toBe("");
  });

  it("starts over with each press, the same one too", () => {
    const { store, line } = setup();
    press(store, "tap Ctrl+Z");
    act(() => vi.advanceTimersByTime(KEY_PREVIEW_MS - 100));
    press(store, "tap Ctrl+Z");
    act(() => vi.advanceTimersByTime(KEY_PREVIEW_MS - 100));
    expect(line().getAttribute("data-shown")).toBe("true");
    press(store, "hold Shift", { control: "leftshoulder" });
    expect(line().textContent).toBe("LB → Shift");
  });

  it("names the script a key runs, with the script icon (SCR-50)", () => {
    const { store, line } = setup();
    // engine.activity names a macro action by its file.
    press(store, "macro 复制全部.ts");
    expect(line().textContent).toBe("A → 复制全部");
    expect(line().querySelector("svg.key-preview-icon")).not.toBeNull();
    // A shortcut has no icon.
    press(store, "tap Ctrl+Z");
    expect(line().textContent).toBe("A → Ctrl+Z");
    expect(line().querySelector("svg.key-preview-icon")).toBeNull();
  });

  it("draws the control as the pad shows it and names it for screen readers (G6)", () => {
    const { store, line } = setup();
    press(store, "tap D");
    // Not "A → D": the A button is its icon, a green A (ADR-0079 §7).
    const icon = line().querySelector(".control-icon") as HTMLElement;
    const art = (element: Element | null) =>
      element?.querySelector("svg")?.getAttribute("data-pad-icon");
    expect(icon.dataset.kind).toBe("art");
    expect(art(icon)).toBe("kenney/xbox_button_a");
    expect(icon.dataset.face).toBe("green");
    expect(icon.getAttribute("aria-hidden")).toBe("true");
    const name = line().querySelector(".visually-hidden");
    expect(name?.textContent).toBe("A");
    expect(line().textContent).toBe("A → D");
    press(store, "hold Shift", { control: "dpad.left" });
    expect(art(line().querySelector(".control-icon"))).toBe(
      "kenney/xbox_dpad_left",
    );
    expect(line().textContent).toBe("← → Shift");
    press(store, "tap Ctrl+Z", { control: "rt" });
    expect(art(line().querySelector(".control-icon"))).toBe("kenney/xbox_rt");
  });

  it("names a script action and leaves out other actions", () => {
    const { store, line } = setup();
    press(store, "script cycleBrush", { control: "north" });
    expect(line().textContent).toBe("Y → 切换笔刷");
    act(() => vi.advanceTimersByTime(KEY_PREVIEW_MS + 1000));
    press(store, "layer push brush");
    expect(line().textContent).toBe("");
  });

  it("shows only presses of its pad while padmate is in front", () => {
    let front = false;
    const { store, line } = setup(() => front);
    press(store, "tap Ctrl+Z");
    expect(line().textContent).toBe("");
    front = true;
    press(store, "tap Ctrl+Z", { device: 2 });
    expect(line().textContent).toBe("");
    // A slot the shell does not know yet still counts.
    press(store, "tap Ctrl+Z", { device: null });
    expect(line().textContent).toBe("A → Ctrl+Z");
  });

  it("does not show a press from before it was shown", () => {
    const store = createPadmateStore(
      editorAPIFixture() as unknown as PadmateAPI,
    );
    store.setState({
      keyPreview: { seq: 4, device: 1, control: "south", action: "tap Z" },
    });
    render(
      <KeyPreview
        store={store}
        device={1}
        labels={labels}
        language="zh-CN"
        focused={() => true}
      />,
    );
    expect(screen.getByTestId("key-preview").textContent).toBe("");
    press(store, "tap Y");
    expect(screen.getByTestId("key-preview").textContent).toBe("A → Y");
  });
});
