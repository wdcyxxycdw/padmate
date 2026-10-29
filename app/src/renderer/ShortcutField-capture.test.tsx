// @vitest-environment jsdom
// ADR-0076 section 5: recording a shortcut is one step. A click on the field
// or the button starts it (that click is not recorded); the first key that is
// not a modifier ends it at once, with the modifiers held; modifiers alone are
// recorded when they are let go; at most three keys; and the mouse is a key
// inside the window: a button, or one notch of the wheel.
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ProfileBinding } from "../shared/generated/schema";
import { BindingFields } from "./BindingEditor";
import { fakeCapture, shortcutValue } from "./capture-fixture";
import { editorAPIFixture } from "./editor-api-fixture";
import { MacroContext } from "./macro-context";
import type { Language } from "./store";

afterEach(() => {
  cleanup();
  // A press that was never released (the tests fire no mouseup) would keep the
  // click that follows it from the next test: the window losing focus ends it.
  window.dispatchEvent(new Event("blur"));
});

function Draft({
  initial = { press: { type: "keys", keys: "F13" } },
  language = "zh-CN",
  disabled = false,
  onElsewhere,
}: {
  initial?: ProfileBinding;
  language?: Language;
  disabled?: boolean;
  onElsewhere?: () => void;
}) {
  const [value, setValue] = useState<ProfileBinding>(initial);
  return (
    <MacroContext value={{ list: null, failing: new Set() }}>
      <BindingFields
        value={value}
        onChange={setValue}
        language={language}
        disabled={disabled}
        actions={[{ name: "cycleBrush" }]}
      />
      <output data-testid="draft">{JSON.stringify(value)}</output>
      <button type="button" onClick={onElsewhere}>
        elsewhere
      </button>
    </MacroContext>
  );
}
const draft = (): ProfileBinding =>
  JSON.parse(screen.getByTestId("draft").textContent ?? "{}");
const press = () => draft().press;

const idleName = (language: Language) =>
  language === "en" ? "Capture shortcut" : "录入快捷键";
const busyName = (language: Language) =>
  language === "en" ? "Recording…" : "录入中…";

/** Renders the field and starts a capture with the button. */
async function started(
  options: { initial?: ProfileBinding; language?: Language } = {},
) {
  const api = editorAPIFixture();
  const capture = fakeCapture(api);
  const onElsewhere = vi.fn();
  const view = render(<Draft {...options} onElsewhere={onElsewhere} />);
  const language = options.language ?? "zh-CN";
  fireEvent.click(screen.getByRole("button", { name: idleName(language) }));
  await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
  const busy = () => screen.queryByRole("button", { name: busyName(language) });
  const idle = () =>
    screen.queryByRole("button", { name: idleName(language) }) !== null;
  return { api, capture, view, busy, idle, onElsewhere, language };
}

const elsewhere = () => screen.getByRole("button", { name: "elsewhere" });

describe("starting", () => {
  it("is a click on the button, or on the field", async () => {
    const api = editorAPIFixture();
    fakeCapture(api);
    render(<Draft />);
    const field = screen.getByRole("group", { name: "快捷键" });
    fireEvent.click(field.querySelector(".drawer-capture") as HTMLElement);
    await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
    expect(screen.getByRole("button", { name: "录入中…" })).toBeTruthy();
    expect(field.getAttribute("data-active")).toBe("true");
    cleanup();

    const other = editorAPIFixture();
    fakeCapture(other);
    render(<Draft />);
    fireEvent.click(
      screen
        .getByRole("group", { name: "快捷键" })
        .querySelector(".drawer-capture-keys") as HTMLElement,
    );
    await waitFor(() => expect(other.captureStart).toHaveBeenCalledTimes(1));
  });

  it("is not started in a field that cannot be edited, by a click on the field or on the keys", async () => {
    // A disabled fieldset blocks its buttons but not a click on its other
    // elements: nothing could be recorded, and the keyboard helper would run.
    const api = editorAPIFixture();
    fakeCapture(api);
    render(<Draft disabled />);
    const field = screen.getByRole("group", { name: "快捷键" });
    fireEvent.click(field.querySelector(".drawer-capture") as HTMLElement);
    fireEvent.click(field.querySelector(".drawer-capture-keys") as HTMLElement);
    fireEvent.click(screen.getByRole("button", { name: "录入快捷键" }));
    await act(async () => {});
    expect(api.captureStart).not.toHaveBeenCalled();
    expect(field.getAttribute("data-active")).toBe("false");
    expect(screen.getByRole("button", { name: "录入快捷键" })).toBeTruthy();
  });

  it("is not done by the clear button, which only clears", async () => {
    const api = editorAPIFixture();
    fakeCapture(api);
    render(<Draft />);
    fireEvent.click(screen.getByRole("button", { name: "清除快捷键" }));
    expect(press()).toEqual({ type: "keys", keys: "" });
    await act(async () => {});
    expect(api.captureStart).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "录入快捷键" })).toBeTruthy();
  });

  it("does not record the click that starts it, nor the second press of its double click", async () => {
    const c = await started();
    // The press of the starting click came before the capture was on.
    expect(c.busy()).not.toBeNull();
    expect(press()).toEqual({ type: "keys", keys: "F13" });
    const button = c.busy() as HTMLElement;
    // The second press of a double click comes after it, with a count of 2.
    fireEvent.mouseDown(button, { detail: 2 });
    fireEvent.mouseUp(button, { detail: 2 });
    fireEvent.click(button, { detail: 2 });
    await act(async () => {});
    expect(c.busy()).not.toBeNull();
    expect(press()).toEqual({ type: "keys", keys: "F13" });
    expect(c.api.captureStart).toHaveBeenCalledTimes(1);
  });

  it("has no 鼠标 menu any more, and no button to stop", async () => {
    const api = editorAPIFixture();
    fakeCapture(api);
    render(<Draft />);
    const field = screen.getByRole("group", { name: "快捷键" });
    expect(
      within(field)
        .getAllByRole("button")
        .map((button) => button.textContent),
    ).toEqual(["", "录入快捷键"]);
    expect(screen.queryByRole("button", { name: "鼠标" })).toBeNull();
    expect(screen.queryByTestId("shortcut-mouse")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "录入快捷键" }));
    await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("button", { name: "停止捕获" })).toBeNull();
  });

  it("says how to record and how to cancel, in both languages", async () => {
    const zh = await started();
    expect(document.querySelector(".drawer-hint")?.textContent).toBe(
      "请按下快捷键，也可以点一下鼠标或滚一格滚轮。按住 Esc 1 秒取消。",
    );
    zh.view.unmount();
    cleanup();
    await started({ language: "en" });
    expect(document.querySelector(".drawer-hint")?.textContent).toBe(
      "Press a shortcut, click the mouse or turn the wheel. Hold Esc for 1 second to cancel.",
    );
  });
});

describe("the first key ends the capture", () => {
  it("a key with the modifiers held is one edit, and the helper is let go", async () => {
    const c = await started();
    await c.capture.emit({ active: true, chord: "LCtrl+LShift+Z" });
    expect(press()).toEqual({ type: "keys", keys: "LCtrl+LShift+Z" });
    expect(c.idle()).toBe(true);
    expect(shortcutValue()).toBe("LCtrl+LShift+Z");
    await waitFor(() =>
      expect(c.api.captureStop).toHaveBeenCalledExactlyOnceWith(1),
    );
    // What the helper still says of that session is nobody's.
    await c.capture.emit({ active: true, chord: "F5" });
    expect(press()).toEqual({ type: "keys", keys: "LCtrl+LShift+Z" });
  });

  it("modifiers go down and show, and are recorded when all are let go", async () => {
    const c = await started();
    await c.capture.emit({ active: true, chord: "LCtrl" });
    expect(shortcutValue()).toBe("LCtrl");
    expect(c.idle()).toBe(false);
    await c.capture.emit({ active: true, chord: "LCtrl+LShift" });
    expect(shortcutValue()).toBe("LCtrl+LShift");
    expect(press()).toEqual({ type: "keys", keys: "F13" });
    // One of them is up, one still down.
    fireEvent.keyUp(c.busy() as HTMLElement, {
      key: "Control",
      shiftKey: true,
    });
    expect(c.idle()).toBe(false);
    fireEvent.keyUp(c.busy() as HTMLElement, { key: "Shift" });
    expect(c.idle()).toBe(true);
    expect(press()).toEqual({ type: "keys", keys: "LCtrl+LShift" });
  });

  it("a single Alt is Alt", async () => {
    const c = await started();
    await c.capture.emit({ active: true, chord: "LAlt" });
    fireEvent.keyUp(c.busy() as HTMLElement, { key: "Alt" });
    expect(press()).toEqual({ type: "keys", keys: "LAlt" });
    expect(c.idle()).toBe(true);
  });

  it("the other keys that go up are not the modifiers' release", async () => {
    const c = await started();
    // The Enter that started a capture from the keyboard is let go inside it.
    fireEvent.keyUp(c.busy() as HTMLElement, { key: "Enter" });
    expect(c.idle()).toBe(false);
    await c.capture.emit({ active: true, chord: "LCtrl" });
    fireEvent.keyUp(c.busy() as HTMLElement, { key: "k", ctrlKey: true });
    expect(c.idle()).toBe(false);
  });

  it("four keys: the first three are recorded, not the fourth", async () => {
    const c = await started();
    await c.capture.emit({ active: true, chord: "LCtrl" });
    await c.capture.emit({ active: true, chord: "LCtrl+LShift" });
    await c.capture.emit({ active: true, chord: "LCtrl+LShift+LAlt" });
    expect(c.idle()).toBe(false);
    await c.capture.emit({ active: true, chord: "LCtrl+LShift+LAlt+Z" });
    expect(press()).toEqual({ type: "keys", keys: "LCtrl+LShift+LAlt" });
    expect(c.idle()).toBe(true);
  });

  it("records the key's position when asked, for the keyboard only", async () => {
    const api = editorAPIFixture();
    const capture = fakeCapture(api);
    render(<Draft />);
    fireEvent.click(screen.getByText("高级"));
    fireEvent.click(screen.getByRole("switch", { name: "映射扫描码" }));
    fireEvent.click(screen.getByRole("button", { name: "录入快捷键" }));
    await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
    await capture.emit({
      active: true,
      chord: "LCtrl+K",
      physicalChord: "LCtrl+sc:0x25",
    });
    expect(press()).toEqual({ type: "keys", keys: "LCtrl+sc:0x25" });
    // The mouse has no position: a button is a button.
    fireEvent.click(screen.getByRole("button", { name: "录入快捷键" }));
    await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(2));
    fireEvent.mouseDown(elsewhere(), { button: 2, ctrlKey: true });
    expect(press()).toEqual({ type: "keys", keys: "Ctrl+MouseRight" });
  });

  it("does not change the shortcut when it is recorded again the same", async () => {
    const api = editorAPIFixture();
    const capture = fakeCapture(api);
    const onChange = vi.fn();
    function Spy() {
      const [value, setValue] = useState<ProfileBinding>({
        press: { type: "keys", keys: "F13" },
      });
      return (
        <BindingFields
          value={value}
          onChange={(next) => {
            onChange(next);
            setValue(next);
          }}
          language="zh-CN"
          actions={[]}
        />
      );
    }
    render(<Spy />);
    fireEvent.click(screen.getByRole("button", { name: "录入快捷键" }));
    await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
    await capture.emit({ active: true, chord: "F13" });
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "录入快捷键" })).toBeTruthy();
  });

  it("ends by itself when the window loses focus, with the modifiers held so far", async () => {
    const c = await started();
    await c.capture.emit({ active: true, chord: "LWin" });
    await c.capture.emit({ active: false, reason: "blur" });
    expect(press()).toEqual({ type: "keys", keys: "LWin" });
    expect(c.idle()).toBe(true);
    expect(screen.queryByRole("alert")).toBeNull();
  });
});

describe("the mouse is a key inside the window", () => {
  it.each([
    [0, "MouseLeft"],
    [1, "MouseMiddle"],
    [2, "MouseRight"],
    [3, "MouseX1"],
    [4, "MouseX2"],
  ])("button %i is %s, and ends the capture", async (button, key) => {
    const c = await started();
    fireEvent.mouseDown(elsewhere(), { button });
    expect(press()).toEqual({ type: "keys", keys: key });
    expect(c.idle()).toBe(true);
    await waitFor(() =>
      expect(c.api.captureStop).toHaveBeenCalledExactlyOnceWith(1),
    );
  });

  it.each([
    [{ deltaY: -100 }, "WheelUp"],
    [{ deltaY: 100 }, "WheelDown"],
    [{ deltaX: -100 }, "WheelLeft"],
    [{ deltaX: 100 }, "WheelRight"],
    [{ deltaX: 20, deltaY: -100 }, "WheelUp"],
  ])("a notch of %o is %s, and ends the capture", async (delta, key) => {
    const c = await started();
    fireEvent.wheel(elsewhere(), delta);
    expect(press()).toEqual({ type: "keys", keys: key });
    expect(c.idle()).toBe(true);
  });

  it("takes the modifiers that are held, with the sides the helper heard", async () => {
    const c = await started();
    await c.capture.emit({ active: true, chord: "RCtrl" });
    fireEvent.mouseDown(elsewhere(), { ctrlKey: true, shiftKey: true });
    expect(press()).toEqual({ type: "keys", keys: "RCtrl+Shift+MouseLeft" });
  });

  it("takes them for a notch of the wheel too", async () => {
    const c = await started();
    fireEvent.wheel(elsewhere(), { deltaY: -100, altKey: true });
    expect(press()).toEqual({ type: "keys", keys: "Alt+WheelUp" });
    expect(c.idle()).toBe(true);
  });

  it("is the fourth key with three modifiers held, and is not recorded", async () => {
    const c = await started();
    await c.capture.emit({ active: true, chord: "LCtrl+LShift+LAlt" });
    fireEvent.mouseDown(elsewhere(), {
      ctrlKey: true,
      shiftKey: true,
      altKey: true,
    });
    expect(press()).toEqual({ type: "keys", keys: "LCtrl+LShift+LAlt" });
  });

  it("a click anywhere in the window is recorded, not a way to cancel", async () => {
    const c = await started();
    fireEvent.mouseDown(elsewhere());
    fireEvent.mouseUp(elsewhere());
    fireEvent.click(elsewhere());
    expect(press()).toEqual({ type: "keys", keys: "MouseLeft" });
    // The press was a key of the shortcut, not a click on what is there.
    expect(c.onElsewhere).not.toHaveBeenCalled();
    // The next click is an ordinary one again.
    await new Promise((done) => setTimeout(done, 150));
    fireEvent.mouseDown(elsewhere());
    fireEvent.mouseUp(elsewhere());
    fireEvent.click(elsewhere());
    expect(c.onElsewhere).toHaveBeenCalledTimes(1);
    expect(press()).toEqual({ type: "keys", keys: "MouseLeft" });
  });

  it("a click on the field's own button is recorded too", async () => {
    const c = await started();
    const button = c.busy() as HTMLElement;
    fireEvent.mouseDown(button);
    fireEvent.mouseUp(button);
    fireEvent.click(button);
    expect(press()).toEqual({ type: "keys", keys: "MouseLeft" });
    // The click did not start another capture.
    await act(async () => {});
    expect(c.api.captureStart).toHaveBeenCalledTimes(1);
    expect(c.idle()).toBe(true);
  });

  it("a press is kept from the page, which would take focus or select", async () => {
    await started();
    expect(fireEvent.mouseDown(elsewhere())).toBe(false);
  });

  it("opens no menu on a right click, and does not scroll the page on the wheel", async () => {
    const c = await started();
    const page = vi.fn();
    window.addEventListener("contextmenu", page);
    window.addEventListener("wheel", page);
    try {
      // While the capture runs, before anything is recorded.
      expect(fireEvent.contextMenu(elsewhere())).toBe(false);
      expect(page).not.toHaveBeenCalled();
      // The right button ends the capture on its press; the menu comes after
      // its release, when the capture is over, and is still not opened.
      fireEvent.mouseDown(elsewhere(), { button: 2 });
      expect(press()).toEqual({ type: "keys", keys: "MouseRight" });
      expect(c.idle()).toBe(true);
      fireEvent.mouseUp(elsewhere(), { button: 2 });
      expect(fireEvent.contextMenu(elsewhere())).toBe(false);
      expect(page).not.toHaveBeenCalled();
    } finally {
      window.removeEventListener("contextmenu", page);
      window.removeEventListener("wheel", page);
    }
    // Then the page is the user's again.
    await new Promise((done) => setTimeout(done, 150));
    expect(fireEvent.contextMenu(elsewhere())).toBe(true);
    expect(fireEvent.wheel(elsewhere(), { deltaY: 100 })).toBe(true);
  });

  it("keeps the wheel from scrolling while the capture waits", async () => {
    await started();
    expect(fireEvent.wheel(elsewhere(), { deltaY: 0, deltaX: 0 })).toBe(false);
  });

  it("is not recorded when no capture runs", async () => {
    const api = editorAPIFixture();
    fakeCapture(api);
    const onElsewhere = vi.fn();
    render(<Draft onElsewhere={onElsewhere} />);
    expect(fireEvent.mouseDown(elsewhere())).toBe(true);
    fireEvent.click(elsewhere());
    expect(fireEvent.wheel(elsewhere(), { deltaY: -100 })).toBe(true);
    expect(onElsewhere).toHaveBeenCalledTimes(1);
    expect(press()).toEqual({ type: "keys", keys: "F13" });
  });

  it("is recorded once, however many events come before the field has drawn the end", async () => {
    const api = editorAPIFixture();
    fakeCapture(api);
    const onChange = vi.fn();
    function Spy() {
      const [value, setValue] = useState<ProfileBinding>({
        press: { type: "keys", keys: "F13" },
      });
      return (
        <BindingFields
          value={value}
          onChange={(next) => {
            onChange(next);
            setValue(next);
          }}
          language="zh-CN"
          actions={[]}
        />
      );
    }
    render(<Spy />);
    fireEvent.click(screen.getByRole("button", { name: "录入快捷键" }));
    await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
    // One burst, as a smooth wheel brings: all of it in one task.
    act(() => {
      fireEvent.wheel(document.body, { deltaY: -100 });
      fireEvent.wheel(document.body, { deltaY: 100 });
      fireEvent.mouseDown(document.body);
    });
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange.mock.calls[0][0].press).toEqual({
      type: "keys",
      keys: "WheelUp",
    });
  });

  it("is a key of a step of a sequence as well, which a click on its keys starts", async () => {
    const api = editorAPIFixture();
    fakeCapture(api);
    render(
      <Draft initial={{ press: { type: "keys", keys: ["F13", "F14"] } }} />,
    );
    const step = screen.getByRole("group", { name: "第 2 步" });
    expect(within(step).queryByRole("button", { name: "鼠标" })).toBeNull();
    fireEvent.click(step.querySelector(".drawer-step-keys") as HTMLElement);
    await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
    expect(within(step).getByRole("button", { name: "录入中…" })).toBeTruthy();
    fireEvent.wheel(elsewhere(), { deltaY: 100, ctrlKey: true });
    expect(press()).toEqual({
      type: "keys",
      keys: ["F13", "Ctrl+WheelDown"],
    });
    expect(shortcutValue("第 2 步")).toBe("Ctrl+WheelDown");
  });

  it("shows the keys of the mouse as keycaps, in English too", async () => {
    const c = await started({ language: "en" });
    fireEvent.mouseDown(elsewhere(), { button: 3, shiftKey: true });
    const field = screen.getByRole("group", { name: "Shortcut" });
    expect(
      [...field.querySelectorAll("kbd")].map((cap) => cap.textContent),
    ).toEqual(["Shift", "X1"]);
    expect(c.idle()).toBe(true);
  });
});
