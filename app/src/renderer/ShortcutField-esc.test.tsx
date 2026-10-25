// @vitest-environment jsdom
// UI-24: recording a shortcut swallows every key, so a keyboard user needs a
// way out that is not a click: hold Esc for 1 second. The helper only watches
// the keyboard (it never swallows), so the window sees the Esc press and its
// release, and times the hold itself. A short Esc is still a shortcut (Esc).
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
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
  vi.useRealTimers();
  cleanup();
});

function Draft({
  initial = { press: { type: "keys", keys: "F13" } },
  language = "zh-CN",
}: {
  initial?: ProfileBinding;
  language?: Language;
}) {
  const [value, setValue] = useState<ProfileBinding>(initial);
  return (
    <MacroContext value={{ list: null, failing: new Set() }}>
      <BindingFields
        value={value}
        onChange={setValue}
        language={language}
        actions={[{ name: "cycleBrush" }]}
      />
      <output data-testid="draft">{JSON.stringify(value)}</output>
    </MacroContext>
  );
}
const draft = (): ProfileBinding =>
  JSON.parse(screen.getByTestId("draft").textContent ?? "{}");

/** Starts a capture, with the clock (setTimeout, clearTimeout, Date) faked. */
async function capturing(language: Language = "zh-CN") {
  const api = editorAPIFixture();
  const { emit } = fakeCapture(api);
  const view = render(<Draft language={language} />);
  const name = language === "en" ? "Capture shortcut" : "录入快捷键";
  fireEvent.click(screen.getByRole("button", { name }));
  await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
  // waitFor and the session's promises keep their real timers.
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"] });
  const button = () =>
    screen.getByRole("button", {
      name: language === "en" ? "Stop capture" : "停止捕获",
    });
  const idle = () =>
    screen.queryByRole("button", { name }) !== null &&
    screen.queryByRole("button", {
      name: language === "en" ? "Stop capture" : "停止捕获",
    }) === null;
  const key = (type: "keyDown" | "keyUp", init: KeyboardEventInit = {}) =>
    fireEvent[type](button(), { key: "Escape", ...init });
  /** What the helper reports for one press of Esc. */
  const helperEsc = () => emit({ active: true, chord: "Esc", vk: 27 });
  const pass = (ms: number) => act(() => vi.advanceTimersByTime(ms));
  return { api, emit, view, button, idle, key, helperEsc, pass };
}

describe("hold Esc to end a capture (UI-24)", () => {
  it("ends the capture after 1 second and keeps the chord before the Esc", async () => {
    const c = await capturing();
    await c.emit({ active: true, chord: "Ctrl+K", vk: 75 });
    await c.helperEsc();
    // While it is held the field shows what the helper heard.
    expect(shortcutValue()).toBe("Esc");
    c.key("keyDown");
    c.pass(999);
    expect(c.idle()).toBe(false);
    expect(draft().press).toEqual({ type: "keys", keys: "F13" });
    c.pass(1);
    expect(c.idle()).toBe(true);
    // The held Esc is not a shortcut: the one before it is committed.
    expect(draft().press).toEqual({ type: "keys", keys: "Ctrl+K" });
    expect(shortcutValue()).toBe("Ctrl+K");
    // The session is let go as the field leaves capturing (no waitFor: it
    // needs the timer that is faked here).
    expect(c.api.captureStop).toHaveBeenCalledExactlyOnceWith(1);
  });

  it("does not care which comes first, the key event or the helper's", async () => {
    const c = await capturing();
    await c.emit({ active: true, chord: "Ctrl+K", vk: 75 });
    c.key("keyDown");
    await c.helperEsc();
    c.pass(1000);
    expect(c.idle()).toBe(true);
    expect(draft().press).toEqual({ type: "keys", keys: "Ctrl+K" });
  });

  it("changes nothing when nothing was recorded before the Esc", async () => {
    const c = await capturing();
    await c.helperEsc();
    c.key("keyDown");
    c.pass(1000);
    expect(c.idle()).toBe(true);
    expect(draft().press).toEqual({ type: "keys", keys: "F13" });
    expect(shortcutValue()).toBe("F13");
  });

  it("changes nothing when the helper never heard the Esc either", async () => {
    const c = await capturing();
    c.key("keyDown");
    c.pass(1000);
    expect(c.idle()).toBe(true);
    expect(draft().press).toEqual({ type: "keys", keys: "F13" });
  });

  it("still records a short Esc as Esc", async () => {
    const c = await capturing();
    await c.helperEsc();
    c.key("keyDown");
    c.pass(400);
    c.key("keyUp");
    c.pass(5000);
    expect(c.idle()).toBe(false);
    fireEvent.click(c.button());
    expect(draft().press).toEqual({ type: "keys", keys: "Esc" });
  });

  it("keeps an earlier short Esc when a later one is held", async () => {
    const c = await capturing();
    await c.helperEsc();
    c.key("keyDown");
    c.pass(100);
    c.key("keyUp");
    await c.helperEsc();
    c.key("keyDown");
    c.pass(1000);
    expect(c.idle()).toBe(true);
    expect(draft().press).toEqual({ type: "keys", keys: "Esc" });
  });

  it("times from the first press: repeats of a held key do not restart it", async () => {
    const c = await capturing();
    await c.emit({ active: true, chord: "Ctrl+K", vk: 75 });
    await c.helperEsc();
    c.key("keyDown");
    c.pass(600);
    // The system repeats a held key; the first press is what counts.
    c.key("keyDown", { repeat: true });
    c.pass(300);
    c.key("keyDown", { repeat: true });
    c.pass(100);
    expect(c.idle()).toBe(true);
    expect(draft().press).toEqual({ type: "keys", keys: "Ctrl+K" });
  });

  it("a repeat alone never starts a hold", async () => {
    const c = await capturing();
    c.key("keyDown", { repeat: true });
    c.pass(5000);
    expect(c.idle()).toBe(false);
  });

  it("is cancelled by releasing Esc before the second is up", async () => {
    const c = await capturing();
    await c.helperEsc();
    c.key("keyDown");
    c.pass(999);
    c.key("keyUp");
    c.pass(10);
    expect(c.idle()).toBe(false);
  });

  it("is not a hold of Esc alone when another key goes down, or Esc has a modifier", async () => {
    const c = await capturing();
    c.key("keyDown", { ctrlKey: true });
    c.pass(2000);
    expect(c.idle()).toBe(false);
    c.key("keyDown");
    c.pass(500);
    fireEvent.keyDown(c.button(), { key: "k" });
    c.pass(2000);
    expect(c.idle()).toBe(false);
  });

  it("is cancelled when focus leaves the field, which ends the capture anyway", async () => {
    const c = await capturing();
    await c.emit({ active: true, chord: "Ctrl+K", vk: 75 });
    const idle = vi.getTimerCount();
    c.key("keyDown");
    expect(vi.getTimerCount()).toBe(idle + 1);
    fireEvent.blur(c.button(), { relatedTarget: document.body });
    expect(c.idle()).toBe(true);
    expect(draft().press).toEqual({ type: "keys", keys: "Ctrl+K" });
    // The hold is gone with the capture: nothing is left to run later.
    expect(vi.getTimerCount()).toBe(idle);
  });

  it("is cancelled when the capture is stopped by hand, and when the field goes away", async () => {
    const c = await capturing();
    const idle = vi.getTimerCount();
    c.key("keyDown");
    expect(vi.getTimerCount()).toBe(idle + 1);
    fireEvent.click(c.button());
    expect(c.idle()).toBe(true);
    expect(vi.getTimerCount()).toBe(idle);

    // A hold that is running while the field unmounts leaves no timer behind.
    c.view.unmount();
    vi.useRealTimers();
    const d = await capturing();
    const before = vi.getTimerCount();
    d.key("keyDown");
    expect(vi.getTimerCount()).toBe(before + 1);
    d.view.unmount();
    expect(vi.getTimerCount()).toBe(before);
  });

  it("keeps the Esc that ended the capture, its repeats and its release, from the page", async () => {
    const page = vi.fn();
    window.addEventListener("keydown", page);
    window.addEventListener("keyup", page);
    try {
      const c = await capturing();
      await c.helperEsc();
      c.key("keyDown");
      c.pass(1000);
      expect(c.idle()).toBe(true);
      // The user is still holding Esc: the system keeps repeating it, and a
      // drawer or a view that closes on Esc must not see those, or the hold
      // would close it.
      const target = screen.getByRole("button", { name: "录入快捷键" });
      for (const init of [{ repeat: true }, { repeat: true }]) {
        const down = new KeyboardEvent("keydown", {
          key: "Escape",
          bubbles: true,
          cancelable: true,
          ...init,
        });
        target.dispatchEvent(down);
        expect(down.defaultPrevented).toBe(true);
      }
      const up = new KeyboardEvent("keyup", {
        key: "Escape",
        bubbles: true,
        cancelable: true,
      });
      target.dispatchEvent(up);
      expect(up.defaultPrevented).toBe(true);
      expect(page).not.toHaveBeenCalled();
      // The next press of Esc is the page's again.
      const next = new KeyboardEvent("keydown", {
        key: "Escape",
        bubbles: true,
        cancelable: true,
      });
      target.dispatchEvent(next);
      expect(next.defaultPrevented).toBe(false);
      expect(page).toHaveBeenCalledTimes(1);
    } finally {
      window.removeEventListener("keydown", page);
      window.removeEventListener("keyup", page);
    }
  });

  it("does not hold on after an ordinary stop: Esc is the page's at once", async () => {
    const c = await capturing();
    fireEvent.click(c.button());
    const target = screen.getByRole("button", { name: "录入快捷键" });
    const down = new KeyboardEvent("keydown", {
      key: "Escape",
      bubbles: true,
      cancelable: true,
    });
    target.dispatchEvent(down);
    expect(down.defaultPrevented).toBe(false);
  });

  it("says how to end the capture, in both languages", async () => {
    const zh = await capturing();
    expect(screen.getByText(/^请按下快捷键。/).textContent).toBe(
      "请按下快捷键。点击“停止捕获”、按住 Esc 1 秒或切换到其他窗口即结束录入。",
    );
    zh.view.unmount();
    cleanup();
    vi.useRealTimers();
    await capturing("en");
    expect(screen.getByText(/^Press a shortcut\./).textContent).toBe(
      "Press a shortcut. Stop, hold Esc for 1 second or switch windows to finish.",
    );
  });
});

const WINDOW =
  "无法录入快捷键。请点一下 padmate 窗口，确认它在最前面，再重试。";
const CAP = "录入最长 30 秒，已自动结束。需要的话请重新录入。";

describe("why a capture ended by itself (UI-24)", () => {
  it("says nothing for the ends the user chose or can see", async () => {
    for (const reason of ["blur", "stopped", "replaced"]) {
      cleanup();
      vi.useRealTimers();
      const c = await capturing();
      await c.emit({ active: true, chord: "Ctrl+K", vk: 75 });
      await c.emit({ active: false, reason });
      expect(c.idle(), reason).toBe(true);
      expect(screen.queryByRole("alert"), reason).toBeNull();
      // What was recorded is kept, as before.
      expect(draft().press, reason).toEqual({ type: "keys", keys: "Ctrl+K" });
    }
  });

  it("says a sentence, not the code, for a reason the capture ended with", async () => {
    const texts: Record<string, string> = {
      timeout: CAP,
      "foreground-unavailable": WINDOW,
      "hook-unavailable": "无法录入快捷键：键盘监听没有启动。请重试。",
      "something-new": "无法录入快捷键。请重试。",
    };
    for (const [reason, text] of Object.entries(texts)) {
      cleanup();
      vi.useRealTimers();
      const c = await capturing();
      await c.emit({ active: false, reason });
      const alert = screen.getByRole("alert");
      expect(alert.textContent, reason).toBe(text);
      expect(alert.textContent, reason).not.toContain(reason);
    }
  });

  it("does not say that no key was pressed: the 30 seconds are the whole capture", async () => {
    // The helper stops itself 30 seconds after it started, key or no key; a
    // person who recorded Ctrl+K at 5 s and waited is told how long a capture
    // lasts, not that nothing was pressed.
    const c = await capturing();
    await c.emit({ active: true, chord: "Ctrl+K", vk: 75 });
    c.pass(30000);
    await c.emit({ active: false, reason: "timeout" });
    const alert = screen.getByRole("alert");
    expect(alert.textContent).toBe(CAP);
    expect(alert.textContent).not.toContain("没有按键");
    expect(draft().press).toEqual({ type: "keys", keys: "Ctrl+K" });
  });

  it("says the same when the helper ends after 30 seconds, which is how it does", async () => {
    // The helper's own 30 s context ends first and it exits without a final
    // event, so the main process reports the exit as "ended".
    const c = await capturing();
    await c.emit({ active: true, chord: "Ctrl+K", vk: 75 });
    c.pass(30000);
    await c.emit({ active: false, reason: "ended" });
    expect(screen.getByRole("alert").textContent).toBe(CAP);
  });

  it("says it broke off when the helper ends early", async () => {
    const c = await capturing();
    await c.emit({ active: true, chord: "Ctrl+K", vk: 75 });
    c.pass(4000);
    await c.emit({ active: false, reason: "ended" });
    expect(screen.getByRole("alert").textContent).toBe(
      "录入意外中断，请重试。",
    );
  });

  it("says it in English", async () => {
    const c = await capturing("en");
    await c.emit({ active: false, reason: "timeout" });
    expect(screen.getByRole("alert").textContent).toBe(
      "Capturing stops by itself after 30 seconds. Start again if you need to.",
    );
  });

  /** Renders the field with a capture that cannot start, and returns its alert. */
  async function failing(message: string, language: Language = "zh-CN") {
    const api = editorAPIFixture();
    fakeCapture(api);
    api.captureStart = vi.fn(async () => {
      throw new Error(message);
    });
    render(<Draft language={language} />);
    fireEvent.click(
      screen.getByRole("button", {
        name: language === "en" ? "Capture shortcut" : "录入快捷键",
      }),
    );
    return (await screen.findByRole("alert")).textContent ?? "";
  }

  it("says a sentence when the capture cannot start", async () => {
    expect(await failing("Keyboard capture foreground-unavailable")).toBe(
      WINDOW,
    );
    expect(screen.getByRole("button", { name: "录入快捷键" })).toBeDefined();
  });

  it("asks for the window to be in front only when that is the cause", async () => {
    // The helper is refused when padmate is not the foreground window; the
    // main process says so in a sentence of its own, which is not shown as it is.
    expect(
      await failing("Keyboard capture requires the focused padmate window"),
    ).toBe(WINDOW);
  });

  it("names what failed, not the window, when the service or the hook is the cause", async () => {
    const cases: [string, string][] = [
      [
        "connection-unavailable",
        "无法录入快捷键：连不上映射服务。请稍后再试。",
      ],
      [
        "authentication-failed",
        "无法录入快捷键：映射服务没有接受这次录入。请稍后再试。",
      ],
      [
        "identity-verification-failed",
        "无法录入快捷键：映射服务没有接受这次录入。请稍后再试。",
      ],
      [
        "mapping-capture-failed",
        "无法录入快捷键：映射服务没有接受这次录入。请稍后再试。",
      ],
      ["hook-unavailable", "无法录入快捷键：键盘监听没有启动。请重试。"],
      ["unavailable", "无法录入快捷键：录入组件没有启动。请重试。"],
      ["invalid", "无法录入快捷键：录入组件没有启动。请重试。"],
      ["something-new", "无法录入快捷键。请重试。"],
    ];
    for (const [code, text] of cases) {
      cleanup();
      const said = await failing(`Keyboard capture ${code}`);
      expect(said, code).toBe(text);
      expect(said, code).not.toContain("窗口");
      expect(said, code).not.toContain(code);
    }
  });

  it("says them in English", async () => {
    expect(await failing("Keyboard capture connection-unavailable", "en")).toBe(
      "Capturing is not available: the mapping service can't be reached. Try again in a moment.",
    );
    cleanup();
    expect(
      await failing(
        "Keyboard capture requires the focused padmate window",
        "en",
      ),
    ).toBe(
      "Capturing is not available. Click the padmate window to bring it to the front, then try again.",
    );
  });

  it("does not say 30 seconds when the helper did not start in time", async () => {
    // Main gives the helper 2 s to say it is ready: that "timeout" is not
    // the 30 s of a running capture.
    const said = await failing("Keyboard capture timeout");
    expect(said).not.toContain("30");
    expect(said).toBe("无法录入快捷键：录入组件没有启动。请重试。");
  });

  it("says nothing when the start was replaced or stopped by the person", async () => {
    // (No alert appears: the field is back to idle without a message.)
    const api = editorAPIFixture();
    fakeCapture(api);
    api.captureStart = vi.fn(async () => {
      throw new Error("Keyboard capture replaced");
    });
    render(<Draft />);
    fireEvent.click(screen.getByRole("button", { name: "录入快捷键" }));
    await waitFor(() => expect(api.captureStart).toHaveBeenCalled());
    await act(async () => {});
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByRole("button", { name: "录入快捷键" })).toBeDefined();
  });

  it("still shows an error that is not about capturing as it is", async () => {
    expect(await failing("daemon went away")).toBe("daemon went away");
  });
});
