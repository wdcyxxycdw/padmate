// @vitest-environment jsdom
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type {
  ProfileBinding,
  ProfileKeysAction,
} from "../shared/generated/schema";
import { BindingFields } from "./BindingEditor";
import { fakeCapture, shortcutValue } from "./capture-fixture";
import { editorAPIFixture } from "./editor-api-fixture";
import { MacroContext, type MacroContextValue } from "./macro-context";
import type { Language } from "./store";

afterEach(cleanup);

function Draft({
  initial = { press: { type: "keys", keys: "F13" } },
  language = "zh-CN",
  profiles,
  macros = { list: null, failing: new Set() },
  control,
}: {
  initial?: ProfileBinding;
  language?: Language;
  profiles?: { id: string; name?: string }[];
  /** The profile's scripts (SCR-50) the drawer picks from. */
  macros?: MacroContextValue;
  /** The control the binding is for (ANA-12 offers a depth rate on some). */
  control?: string;
}) {
  const [value, setValue] = useState<ProfileBinding>(initial);
  return (
    <MacroContext value={macros}>
      <BindingFields
        control={control}
        value={value}
        onChange={setValue}
        language={language}
        actions={[{ name: "cycleBrush" }]}
        profiles={profiles}
      />
      <output data-testid="draft">{JSON.stringify(value)}</output>
    </MacroContext>
  );
}
/** Types into a text field and leaves it, which commits the text (UI-43). */
function commit(field: Element, init: { target: { value: string } }) {
  fireEvent.change(field, init);
  fireEvent.blur(field);
}

/** 添加下一步: opens the menu and picks the kind of the new step. */
function addStep(kind: "快捷键" | "等待" | "文字" = "快捷键") {
  fireEvent.click(screen.getByRole("button", { name: "添加下一步" }));
  fireEvent.click(screen.getByRole("menuitem", { name: kind }));
}

const scripts: MacroContextValue = {
  list: {
    macros: [
      { file: "复制全部.ts", name: "复制全部", usedBy: [] },
      { file: "下一个笔刷.js", name: "下一个笔刷", usedBy: [] },
      { file: "v1.2.ts", name: "v1.2", usedBy: [], invalidName: true },
    ],
    missing: [{ file: "旧.ts", usedBy: [] }],
  },
  failing: new Set(["下一个笔刷.js"]),
};
const draft = (): ProfileBinding =>
  JSON.parse(screen.getByTestId("draft").textContent ?? "{}");

function captureAPI() {
  const api = editorAPIFixture();
  return { api, ...fakeCapture(api) };
}

it("captures a chord into the shortcut and honours the key position switch", async () => {
  const { api, emit } = captureAPI();
  render(<Draft />);
  // Shortcuts are recorded, never typed (G2); the key position option
  // lives under 高级, not next to the shortcut.
  const field = screen.getByRole("group", { name: "快捷键" });
  expect(within(field).queryByRole("textbox")).toBeNull();
  expect(within(field).queryByRole("switch")).toBeNull();
  fireEvent.click(screen.getByText("高级"));
  const physical = screen.getByRole("switch", { name: "按物理位置" });
  expect(physical.closest(".drawer-advanced")).not.toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "录入快捷键" }));
  await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
  expect(screen.getByRole("button", { name: "停止捕获" })).toBeDefined();
  expect(screen.getByText(/^请按下快捷键。/).textContent).toBe(
    "请按下快捷键。点击“停止捕获”、按住 Esc 1 秒或切换到其他窗口即结束录入。",
  );
  // A chord shows as soon as its keys go down; the capture is one edit,
  // made when it ends.
  await emit({ session: 1, active: true, chord: "Ctrl+K" });
  expect(shortcutValue()).toBe("Ctrl+K");
  expect(field.textContent).toContain("Ctrl");
  expect(draft().press).toEqual({ type: "keys", keys: "F13" });
  // Events from another session are not ours.
  await emit({ session: 2, active: true, chord: "Alt+X" });
  expect(shortcutValue()).toBe("Ctrl+K");
  fireEvent.click(physical);
  expect(screen.getByText("已设置：按物理位置")).toBeDefined();
  await emit({
    session: 1,
    active: true,
    chord: "Ctrl+K",
    physicalChord: "Ctrl+sc25",
  });
  expect(shortcutValue()).toBe("Ctrl+sc25");
  expect(draft().press).toEqual({ type: "keys", keys: "F13" });
  await emit({ session: 1, active: false, reason: "blur" });
  expect(draft().press).toEqual({ type: "keys", keys: "Ctrl+sc25" });
  expect(screen.getByRole("button", { name: "录入快捷键" })).toBeDefined();
  // The window losing focus is what the hint says ends a capture: it needs
  // no alert, and the code is never shown (UI-24).
  expect(screen.queryByRole("alert")).toBeNull();
  await waitFor(() =>
    expect(api.captureStop).toHaveBeenCalledExactlyOnceWith(1),
  );
});

it("keeps keys pressed during a capture away from the page", async () => {
  const { api, emit } = captureAPI();
  const page = vi.fn();
  window.addEventListener("keydown", page);
  try {
    render(<Draft />);
    const button = screen.getByRole("button", { name: "录入快捷键" });
    fireEvent.click(button);
    await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
    // The capture button has the focus, so Space or Enter would press it.
    expect(document.activeElement).toBe(button);
    for (const key of [" ", "Enter", "z"]) {
      const down = new KeyboardEvent("keydown", {
        key,
        ctrlKey: key === "z",
        bubbles: true,
        cancelable: true,
      });
      button.dispatchEvent(down);
      expect(down.defaultPrevented, key).toBe(true);
      const up = new KeyboardEvent("keyup", {
        key,
        bubbles: true,
        cancelable: true,
      });
      button.dispatchEvent(up);
      expect(up.defaultPrevented, key).toBe(true);
    }
    expect(page).not.toHaveBeenCalled();
    await emit({ session: 1, active: true, chord: "Ctrl+Z" });
    fireEvent.click(button);
    expect(draft().press).toEqual({ type: "keys", keys: "Ctrl+Z" });
    // Without a capture the keys work as usual.
    const down = new KeyboardEvent("keydown", {
      key: "z",
      ctrlKey: true,
      bubbles: true,
      cancelable: true,
    });
    button.dispatchEvent(down);
    expect(down.defaultPrevented).toBe(false);
    expect(page).toHaveBeenCalledOnce();
  } finally {
    window.removeEventListener("keydown", page);
  }
});

it("clears a shortcut in one edit", () => {
  captureAPI();
  render(<Draft />);
  fireEvent.click(screen.getByRole("button", { name: "清除快捷键" }));
  expect(draft().press).toEqual({ type: "keys", keys: "" });
  const field = screen.getByRole("group", { name: "快捷键" });
  expect(field.textContent).toContain("未设置快捷键");
  // Nothing left to clear; focus moves on to recording a new one.
  expect(
    within(field).queryByRole("button", { name: "清除快捷键" }),
  ).toBeNull();
  expect(document.activeElement).toBe(
    screen.getByRole("button", { name: "录入快捷键" }),
  );
});

it("replays capture events that arrive before the session id", async () => {
  const { api, emit } = captureAPI();
  let resolve!: (value: number) => void;
  api.captureStart = vi.fn(
    () =>
      new Promise<number>((r) => {
        resolve = r;
      }),
  );
  render(<Draft />);
  fireEvent.click(screen.getByRole("button", { name: "录入快捷键" }));
  await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
  await emit({ session: 5, active: true, chord: "F20" });
  expect(shortcutValue()).toBe("F13");
  await act(async () => resolve(5));
  expect(shortcutValue()).toBe("F20");
  fireEvent.click(screen.getByRole("button", { name: "停止捕获" }));
  expect(draft().press).toEqual({ type: "keys", keys: "F20" });
});

it("stops capture with a real mouse click without blur restarting it", async () => {
  const { api } = captureAPI();
  render(<Draft language="en" />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "Capture shortcut" }));
  await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
  expect(document.activeElement).toBe(
    screen.getByRole("button", { name: "Stop capture" }),
  );
  await user.click(screen.getByRole("button", { name: "Stop capture" }));
  await waitFor(() =>
    expect(api.captureStop).toHaveBeenCalledExactlyOnceWith(1),
  );
  expect(api.captureStart).toHaveBeenCalledTimes(1);
  expect(
    screen.getByRole("button", { name: "Capture shortcut" }),
  ).toBeDefined();
});

it("stops capture when focus leaves the shortcut field", async () => {
  const { api } = captureAPI();
  render(
    <>
      <Draft />
      <button type="button">elsewhere</button>
    </>,
  );
  fireEvent.click(screen.getByRole("button", { name: "录入快捷键" }));
  await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
  const stop = screen.getByRole("button", { name: "停止捕获" });
  // Focus moving inside the field keeps capturing.
  fireEvent.blur(stop, {
    relatedTarget: screen.getByRole("group", { name: "快捷键" }),
  });
  expect(screen.getByRole("button", { name: "停止捕获" })).toBeDefined();
  fireEvent.blur(stop, {
    relatedTarget: screen.getByRole("button", { name: "elsewhere" }),
  });
  expect(screen.getByRole("button", { name: "录入快捷键" })).toBeDefined();
  await waitFor(() =>
    expect(api.captureStop).toHaveBeenCalledExactlyOnceWith(1),
  );
});

it("stops only its own late capture session when the editor closes", async () => {
  const { api } = captureAPI();
  let resolve!: (value: number) => void;
  api.captureStart = vi.fn(
    () =>
      new Promise<number>((r) => {
        resolve = r;
      }),
  );
  const ui = render(<Draft />);
  fireEvent.click(screen.getByRole("button", { name: "录入快捷键" }));
  await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
  ui.unmount();
  await act(async () => resolve(17));
  expect(api.captureStop).toHaveBeenCalledExactlyOnceWith(17);
});

it("warns about reserved shortcuts but not about bare letters (F6)", async () => {
  const { record } = captureAPI();
  render(<Draft />);
  await record("a");
  expect(draft().press).toEqual({ type: "keys", keys: "a" });
  const field = screen.getByRole("group", { name: "快捷键" });
  expect(field.querySelector(".drawer-warning")).toBeNull();
  expect(screen.queryByText(/输入法吃掉/)).toBeNull();
  await record("Win+L");
  expect(screen.getByTestId("reserved-warning")).toBeDefined();
});

it("keeps the repeat style in step with how the key is sent", () => {
  captureAPI();
  render(<Draft />);
  const sending = screen.getByRole("radiogroup", { name: "发送方式" });
  expect(
    within(sending)
      .getByRole("radio", { name: "按住保持" })
      .getAttribute("aria-checked"),
  ).toBe("true");
  fireEvent.click(within(sending).getByRole("radio", { name: "单次点按" }));
  expect(draft().press).toEqual({ type: "keys", keys: "F13", mode: "tap" });
  fireEvent.click(screen.getByRole("switch", { name: "连发" }));
  expect(draft().press).toMatchObject({
    mode: "tap",
    repeat: { style: "tap", delayMs: 350, rateHz: 12 },
  });
  fireEvent.click(within(sending).getByRole("radio", { name: "按住保持" }));
  expect(draft().press).toMatchObject({
    mode: "hold",
    repeat: { style: "typematic" },
  });
  commit(screen.getByRole("textbox", { name: "每秒次数" }), {
    target: { value: "20" },
  });
  expect(draft().press).toMatchObject({ repeat: { rateHz: 20 } });
  fireEvent.click(screen.getByRole("switch", { name: "连发" }));
  expect(draft().press).toEqual({ type: "keys", keys: "F13", mode: "hold" });
});

it("sends sequences as taps and turns back into one chord", async () => {
  const { record } = captureAPI();
  render(
    <Draft
      initial={{
        press: {
          type: "keys",
          keys: "F13",
          repeat: { style: "typematic", delayMs: 300, rateHz: 10 },
        },
      }}
    />,
  );
  addStep();
  expect(draft().press).toEqual({
    type: "keys",
    keys: ["F13", ""],
    mode: "tap",
  });
  expect(screen.queryByRole("radiogroup", { name: "发送方式" })).toBeNull();
  expect(screen.queryByRole("switch", { name: "连发" })).toBeNull();
  // A new step waits for its keys; it has no text entry either.
  const step = screen.getByRole("group", { name: "第 2 步" });
  expect(step.textContent).toContain("未设置");
  expect(within(step).queryByRole("textbox")).toBeNull();
  await record("Ctrl+V", { field: "第 2 步" });
  expect(draft().press).toMatchObject({ keys: ["F13", "Ctrl+V"] });
  expect(shortcutValue("第 2 步")).toBe("Ctrl+V");
  fireEvent.click(screen.getByRole("button", { name: "删除第 1 步" }));
  expect(draft().press).toEqual({ type: "keys", keys: "Ctrl+V", mode: "tap" });
  expect(shortcutValue()).toBe("Ctrl+V");
});

it("offers sending and repeat only for gestures that can hold a key", () => {
  captureAPI();
  render(<Draft initial={{ tap: { type: "keys", keys: "F13" } }} />);
  expect(screen.getByRole("group", { name: "快捷键" })).toBeDefined();
  expect(screen.queryByRole("radiogroup", { name: "发送方式" })).toBeNull();
  expect(screen.queryByRole("switch", { name: "连发" })).toBeNull();
});

it("explains the wait before a tap when more taps may follow", () => {
  captureAPI();
  render(
    <Draft
      initial={{
        tap: { type: "keys", keys: "F13" },
        doubleTap: { type: "keys", keys: "F14" },
      }}
    />,
  );
  expect(screen.getByTestId("gesture-delay").textContent).toBe(
    "此按键还设置了双击，因此点按后需再等待 220 ms，确认没有再次按下后才触发。",
  );
  fireEvent.click(screen.getByRole("button", { name: /^双击/ }));
  expect(screen.queryByTestId("gesture-delay")).toBeNull();
});

it("only keeps one gesture open and lets the user close it", () => {
  captureAPI();
  render(
    <Draft
      initial={{
        press: { type: "keys", keys: "F13" },
        hold: { type: "macro", file: "复制全部.ts" },
      }}
      macros={scripts}
    />,
  );
  const press = screen.getByRole("button", { name: /^按下/ });
  const hold = screen.getByRole("button", { name: /^长按/ });
  expect(hold.textContent).toContain("复制全部");
  expect(press.getAttribute("aria-expanded")).toBe("true");
  fireEvent.click(hold);
  expect(press.getAttribute("aria-expanded")).toBe("false");
  expect(hold.getAttribute("aria-expanded")).toBe("true");
  expect(screen.getAllByTestId("action-type")).toHaveLength(1);
  fireEvent.click(hold);
  expect(screen.queryByTestId("action-type")).toBeNull();
});

it("keeps a legacy script action locked until it becomes a shortcut", () => {
  captureAPI();
  render(
    <Draft
      initial={{ press: { type: "script", name: "cycleBrush", args: {} } }}
    />,
  );
  const press = screen.getByRole("button", { name: /^按下/ });
  expect(press.querySelector(".drawer-summary-locked")?.textContent).toBe(
    "脚本动作 cycleBrush",
  );
  expect(screen.queryByTestId("action-type")).toBeNull();
  expect(screen.getByTestId("locked-action").textContent).toContain(
    "运行脚本动作 cycleBrush（在配置文件中设置）",
  );
  fireEvent.click(screen.getByRole("button", { name: "改用快捷键…" }));
  fireEvent.click(screen.getByRole("button", { name: "改用快捷键" }));
  expect(draft().press).toEqual({ type: "keys", keys: "" });
});

it("uses the key position option for sequence steps and only for shortcuts", async () => {
  const { api, emit } = captureAPI();
  render(
    <Draft
      initial={{ press: { type: "keys", keys: ["F13", "F14"] } }}
      macros={scripts}
    />,
  );
  fireEvent.click(screen.getByText("高级"));
  fireEvent.click(screen.getByRole("switch", { name: "按物理位置" }));
  fireEvent.click(screen.getByRole("button", { name: "录入第 2 步" }));
  await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
  await emit({
    session: 1,
    active: true,
    chord: "Ctrl+K",
    physicalChord: "Ctrl+sc25",
  });
  fireEvent.click(screen.getByRole("button", { name: "停止捕获" }));
  expect(draft().press).toEqual({ type: "keys", keys: ["F13", "Ctrl+sc25"] });
  // A script has no shortcut to capture.
  fireEvent.click(
    within(screen.getByTestId("action-type")).getByRole("radio", {
      name: "脚本",
    }),
  );
  fireEvent.change(screen.getByRole("combobox", { name: "选择脚本" }), {
    target: { value: "复制全部.ts" },
  });
  expect(screen.queryByRole("switch", { name: "按物理位置" })).toBeNull();
});

it("ends a step's capture when the step is removed", async () => {
  const { api, emit } = captureAPI();
  render(
    <Draft
      language="en"
      initial={{ press: { type: "keys", keys: ["F13", "F14", "F15"] } }}
    />,
  );
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "Capture step 1" }));
  await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
  expect(screen.getByRole("button", { name: "Stop capture" })).toBeDefined();
  await user.click(screen.getByRole("button", { name: "Remove step 1" }));
  await waitFor(() =>
    expect(api.captureStop).toHaveBeenCalledExactlyOnceWith(1),
  );
  expect(screen.queryByRole("button", { name: "Stop capture" })).toBeNull();
  expect(screen.getByRole("button", { name: "Capture step 1" })).toBeDefined();
  // A late chord from the old session cannot reach the step that moved up.
  await emit({ session: 1, active: true, chord: "Ctrl+Z" });
  expect(draft().press).toEqual({ type: "keys", keys: ["F14", "F15"] });
});

it("moves focus to the previous step when a step is removed", () => {
  captureAPI();
  render(
    <Draft
      initial={{ press: { type: "keys", keys: ["F13", "F14", "F15"] } }}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "删除第 3 步" }));
  expect(draft().press).toEqual({ type: "keys", keys: ["F13", "F14"] });
  expect(document.activeElement).toBe(
    screen.getByRole("button", { name: "录入第 2 步" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "删除第 1 步" }));
  // Two steps collapse into one shortcut; focus lands on its field.
  expect(draft().press).toEqual({ type: "keys", keys: "F14" });
  expect(document.activeElement).toBe(
    screen.getByRole("button", { name: "录入快捷键" }),
  );
});

it("moves focus to 添加手势 when a gesture card is removed", () => {
  captureAPI();
  render(
    <Draft
      initial={{
        press: { type: "keys", keys: "F13" },
        hold: { type: "keys", keys: "F14" },
      }}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: /^长按/ }));
  const remove = screen.getByRole("button", { name: "删除此手势" });
  remove.focus();
  fireEvent.click(remove);
  expect(draft()).toEqual({ press: { type: "keys", keys: "F13" } });
  expect(document.activeElement).toBe(screen.getByTestId("add-gesture"));
  // A gesture picked from the menu and dropped again does the same.
  fireEvent.click(screen.getByTestId("add-gesture"));
  fireEvent.click(screen.getByRole("menuitem", { name: /^双击/ }));
  fireEvent.click(screen.getByRole("button", { name: "删除此手势" }));
  expect(draft()).toEqual({ press: { type: "keys", keys: "F13" } });
  expect(document.activeElement).toBe(screen.getByTestId("add-gesture"));
});

it("does not offer gestures on a blocked binding until it is unblocked", async () => {
  const { record } = captureAPI();
  render(<Draft language="en" initial={{ block: true }} />);
  expect(screen.queryByRole("group", { name: "Shortcut" })).toBeNull();
  expect(
    (screen.getByTestId("add-gesture") as HTMLButtonElement).disabled,
  ).toBe(true);
  expect(
    screen.getByText(
      "This key is blocked. To set gestures, unblock it under Advanced first.",
    ),
  ).toBeDefined();
  fireEvent.click(screen.getByRole("button", { name: "Unblock" }));
  expect(draft()).toEqual({});
  // The unblock button is gone; focus goes to the next step.
  expect(document.activeElement).toBe(screen.getByTestId("add-gesture"));
  await record("F9");
  expect(draft()).toEqual({ press: { type: "keys", keys: "F9" } });
});

it("keeps the Sending row able to wrap its control below the label", () => {
  captureAPI();
  render(<Draft />);
  const sending = screen.getByRole("radiogroup", { name: "发送方式" });
  expect((sending.closest(".drawer-option") as HTMLElement).dataset.wrap).toBe(
    "true",
  );
  expect(
    (
      screen
        .getByRole("switch", { name: "连发" })
        .closest(".drawer-option") as HTMLElement
    ).dataset.wrap,
  ).toBeUndefined();
});

it("offers no type for a profile switch, which stays locked", () => {
  const initial: ProfileBinding = {
    press: { type: "keys", keys: "F13" },
    hold: { type: "profile", profile: "photo" },
  };
  render(
    <Draft initial={initial} profiles={[{ id: "photo", name: "Photo" }]} />,
  );
  const types = screen.getByTestId("action-type");
  expect(
    within(types)
      .getAllByRole("radio")
      .map((radio) => radio.textContent),
  ).toEqual(["快捷键", "输入文字", "脚本", "精确模式", "按键提示"]);
  // Changing the other gesture's type leaves the switch untouched.
  fireEvent.click(within(types).getByRole("radio", { name: "脚本" }));
  fireEvent.click(within(types).getByRole("radio", { name: "快捷键" }));
  expect(draft()).toEqual(initial);
  expect(JSON.stringify(draft().hold)).toBe(JSON.stringify(initial.hold));
  const hold = screen.getByRole("button", { name: /^长按/ });
  expect(hold.querySelector(".drawer-summary-locked")?.textContent).toBe(
    "切换到「Photo」",
  );
  fireEvent.click(hold);
  expect(screen.queryByTestId("action-type")).toBeNull();
  expect(screen.getByTestId("locked-action").textContent).toContain(
    "切换到「Photo」（在配置文件中设置）",
  );
});

it("shows a press with a tap repeat style and no mode as Tap once", () => {
  captureAPI();
  render(
    <Draft
      initial={{
        press: {
          type: "keys",
          keys: "F13",
          repeat: { style: "tap", delayMs: 300, rateHz: 10 },
        },
      }}
    />,
  );
  const sending = screen.getByRole("radiogroup", { name: "发送方式" });
  expect(
    within(sending)
      .getByRole("radio", { name: "单次点按" })
      .getAttribute("aria-checked"),
  ).toBe("true");
});

it("has no 自动 button and no text entry next to the shortcut (F7, G2)", () => {
  captureAPI();
  render(<Draft initial={{ press: { type: "keys", keys: "" } }} />);
  const field = screen.getByRole("group", { name: "快捷键" });
  // Recording, and the 鼠标 menu for the keys a keyboard cannot record.
  expect(
    within(field)
      .getAllByRole("button")
      .map((b) => b.textContent),
  ).toEqual(["鼠标", "录入快捷键"]);
  expect(screen.queryByRole("button", { name: "自动" })).toBeNull();
  expect(screen.queryByTestId("auto-shortcut")).toBeNull();
  expect(within(field).queryByRole("textbox")).toBeNull();
  expect(field.textContent).toContain("未设置快捷键");
});

describe("the 鼠标 menu (ADR-0062)", () => {
  /** Opens the menu of the shortcut field in scope and picks an item. */
  const choose = (item: string, scope?: HTMLElement) => {
    const root = scope ? within(scope) : screen;
    fireEvent.click(root.getByRole("button", { name: /^(鼠标|Mouse)$/ }));
    fireEvent.click(screen.getByRole("menuitem", { name: item }));
  };
  const items = () =>
    within(screen.getByRole("menu"))
      .getAllByRole("menuitem")
      .map((item) => item.textContent);
  const caps = (scope: HTMLElement) =>
    [...scope.querySelectorAll("kbd")].map((cap) => cap.textContent);
  const disabled = (button: HTMLElement) =>
    (button as HTMLButtonElement).disabled;

  it("sits beside the capture button and offers the buttons above the wheel keys", () => {
    captureAPI();
    render(<Draft />);
    const field = screen.getByRole("group", { name: "快捷键" });
    const menu = within(field).getByRole("button", { name: "鼠标" });
    expect(menu.getAttribute("aria-haspopup")).toBe("menu");
    expect(menu.textContent).toBe("鼠标");
    expect(within(field).queryByRole("menu")).toBeNull();
    fireEvent.click(menu);
    expect(screen.getByRole("menu", { name: "鼠标" })).toBeDefined();
    expect(items()).toEqual([
      "左键",
      "右键",
      "中键",
      "侧键 1",
      "侧键 2",
      "滚轮向上",
      "滚轮向下",
      "滚轮向左",
      "滚轮向右",
    ]);
    // The two groups are told apart by a separator, not by a label.
    const separators = screen
      .getByRole("menu")
      .querySelectorAll(".menu-separator");
    expect(separators).toHaveLength(1);
    expect(separators[0].previousElementSibling?.textContent).toBe("侧键 2");
    expect(separators[0].nextElementSibling?.textContent).toBe("滚轮向上");
  });

  it("picks a mouse button that keeps the modifiers, like a wheel key", async () => {
    const { record } = captureAPI();
    render(<Draft />);
    await record("Ctrl+Z");
    choose("左键");
    expect(draft().press).toEqual({ type: "keys", keys: "Ctrl+MouseLeft" });
    const field = screen.getByRole("group", { name: "快捷键" });
    expect(shortcutValue()).toBe("Ctrl+MouseLeft");
    expect(caps(field)).toEqual(["Ctrl", "左键"]);
    // The menu closes and the button keeps the focus.
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(
      within(field).getByRole("button", { name: "鼠标" }),
    );
    // Another button, or a wheel key, replaces it; the modifiers stay.
    choose("右键");
    expect(draft().press).toEqual({ type: "keys", keys: "Ctrl+MouseRight" });
    choose("中键");
    expect(draft().press).toEqual({ type: "keys", keys: "Ctrl+MouseMiddle" });
    choose("侧键 1");
    expect(draft().press).toEqual({ type: "keys", keys: "Ctrl+MouseX1" });
    expect(caps(field)).toEqual(["Ctrl", "侧键1"]);
    choose("侧键 2");
    expect(draft().press).toEqual({ type: "keys", keys: "Ctrl+MouseX2" });
    expect(caps(field)).toEqual(["Ctrl", "侧键2"]);
    choose("滚轮向下");
    expect(draft().press).toEqual({ type: "keys", keys: "Ctrl+WheelDown" });
    choose("左键");
    expect(draft().press).toEqual({ type: "keys", keys: "Ctrl+MouseLeft" });
    // Sides of modifiers stay as they were recorded.
    await record("LShift+RAlt+K");
    choose("右键");
    expect(draft().press).toEqual({
      type: "keys",
      keys: "LShift+RAlt+MouseRight",
    });
  });

  it("sets a button alone when there is no chord, and shows an alias as the button", () => {
    captureAPI();
    render(<Draft initial={{ press: { type: "keys", keys: "" } }} />);
    choose("中键");
    expect(draft().press).toEqual({ type: "keys", keys: "MouseMiddle" });
    expect(shortcutValue()).toBe("MouseMiddle");
    fireEvent.click(screen.getByRole("button", { name: "清除快捷键" }));
    expect(draft().press).toMatchObject({ keys: "" });
    cleanup();
    // A file written with an alias reads as the button; picking that button
    // writes its name.
    render(
      <Draft initial={{ press: { type: "keys", keys: "Shift+LButton" } }} />,
    );
    const field = screen.getByRole("group", { name: "快捷键" });
    expect(caps(field)).toEqual(["Shift", "左键"]);
    choose("左键");
    expect(draft().press).toEqual({ type: "keys", keys: "Shift+MouseLeft" });
    choose("右键");
    expect(draft().press).toEqual({ type: "keys", keys: "Shift+MouseRight" });
  });

  it("keeps the modifiers of the recorded chord and replaces its key", async () => {
    const { record } = captureAPI();
    render(<Draft />);
    await record("Ctrl+Z");
    expect(draft().press).toEqual({ type: "keys", keys: "Ctrl+Z" });
    choose("滚轮向上");
    expect(draft().press).toEqual({ type: "keys", keys: "Ctrl+WheelUp" });
    const field = screen.getByRole("group", { name: "快捷键" });
    expect(shortcutValue()).toBe("Ctrl+WheelUp");
    expect(caps(field)).toEqual(["Ctrl", "滚轮↑"]);
    // The menu closes and the button keeps the focus.
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(
      within(field).getByRole("button", { name: "鼠标" }),
    );
    // Another wheel key replaces the first, the modifiers stay.
    choose("滚轮向右");
    expect(draft().press).toEqual({ type: "keys", keys: "Ctrl+WheelRight" });
    // Sides of modifiers stay as they were recorded.
    await record("LCtrl+Shift+K");
    choose("滚轮向下");
    expect(draft().press).toEqual({
      type: "keys",
      keys: "LCtrl+Shift+WheelDown",
    });
  });

  it("sets the wheel key alone when there is no chord, and keeps the other settings", () => {
    captureAPI();
    render(
      <Draft
        initial={{
          press: {
            type: "keys",
            keys: "",
            mode: "tap",
            repeat: { style: "tap", rateHz: 8 },
          },
        }}
      />,
    );
    choose("滚轮向左");
    expect(draft().press).toEqual({
      type: "keys",
      keys: "WheelLeft",
      mode: "tap",
      repeat: { style: "tap", rateHz: 8 },
    });
    expect(shortcutValue()).toBe("WheelLeft");
    // The key can be cleared again.
    fireEvent.click(screen.getByRole("button", { name: "清除快捷键" }));
    expect(draft().press).toMatchObject({ keys: "" });
  });

  it("keeps a chord of modifiers only and drops any other key", async () => {
    const { record } = captureAPI();
    render(<Draft initial={{ press: { type: "keys", keys: "Ctrl+Shift" } }} />);
    choose("滚轮向上");
    expect(draft().press).toEqual({ type: "keys", keys: "Ctrl+Shift+WheelUp" });
    await record("F13");
    choose("滚轮向下");
    expect(draft().press).toEqual({ type: "keys", keys: "WheelDown" });
  });

  it("does not edit when the chord already has that wheel key", () => {
    captureAPI();
    const onChange = vi.fn();
    function Spy() {
      const [value, setValue] = useState<ProfileBinding>({
        press: { type: "keys", keys: "Alt+WheelUp" },
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
    choose("滚轮向上");
    expect(onChange).not.toHaveBeenCalled();
    choose("滚轮向下");
    expect(onChange).toHaveBeenCalledOnce();
  });

  it("is off while a capture runs, and does not disturb it", async () => {
    const { api, emit } = captureAPI();
    render(<Draft />);
    const menu = screen.getByRole("button", { name: "鼠标" });
    expect(disabled(menu)).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "录入快捷键" }));
    await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
    expect(disabled(menu)).toBe(true);
    await emit({ session: 1, active: true, chord: "Ctrl+K" });
    fireEvent.click(menu);
    expect(screen.queryByRole("menu")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "停止捕获" }));
    expect(draft().press).toEqual({ type: "keys", keys: "Ctrl+K" });
    expect(disabled(menu)).toBe(false);
    choose("滚轮向上");
    expect(draft().press).toEqual({ type: "keys", keys: "Ctrl+WheelUp" });
  });

  it("speaks English", async () => {
    const { record } = captureAPI();
    render(<Draft language="en" />);
    const field = screen.getByRole("group", { name: "Shortcut" });
    fireEvent.click(within(field).getByRole("button", { name: "Mouse" }));
    expect(screen.getByRole("menu", { name: "Mouse" })).toBeDefined();
    expect(items()).toEqual([
      "Left button",
      "Right button",
      "Middle button",
      "Side button 1",
      "Side button 2",
      "Wheel up",
      "Wheel down",
      "Wheel left",
      "Wheel right",
    ]);
    fireEvent.keyDown(screen.getByRole("menu"), { key: "Escape" });
    await record("Shift+B", { field: "Shortcut" });
    choose("Wheel down");
    expect(draft().press).toEqual({ type: "keys", keys: "Shift+WheelDown" });
    expect(caps(field)).toEqual(["Shift", "Wheel↓"]);
    const labels: [item: string, key: string, cap: string][] = [
      ["Left button", "Shift+MouseLeft", "LMB"],
      ["Right button", "Shift+MouseRight", "RMB"],
      ["Middle button", "Shift+MouseMiddle", "MMB"],
      ["Side button 1", "Shift+MouseX1", "X1"],
      ["Side button 2", "Shift+MouseX2", "X2"],
    ];
    for (const [item, key, cap] of labels) {
      choose(item);
      expect(draft().press).toEqual({ type: "keys", keys: key });
      expect(caps(field)).toEqual(["Shift", cap]);
    }
  });

  describe("in a sequence", () => {
    const step = (n: number, en = false) =>
      screen.getByRole("group", { name: en ? `Step ${n}` : `第 ${n} 步` });

    it("sets one step and leaves the others", async () => {
      const { record } = captureAPI();
      render(
        <Draft
          initial={{
            press: { type: "keys", keys: ["Ctrl+K", "F13", "Alt+S"] },
          }}
        />,
      );
      expect(screen.getAllByRole("button", { name: "鼠标" })).toHaveLength(3);
      choose("滚轮向上", step(1));
      expect(draft().press).toEqual({
        type: "keys",
        keys: ["Ctrl+WheelUp", "F13", "Alt+S"],
      });
      choose("滚轮向下", step(2));
      await record("Alt+Q", { field: "第 3 步" });
      choose("滚轮向左", step(3));
      expect(draft().press).toEqual({
        type: "keys",
        keys: ["Ctrl+WheelUp", "WheelDown", "Alt+WheelLeft"],
      });
      expect(shortcutValue("第 3 步")).toBe("Alt+WheelLeft");
      // The preview of the whole sequence shows the keycaps.
      const preview = document.querySelector(
        ".drawer-sequence-preview",
      ) as HTMLElement;
      expect(caps(preview)).toEqual(["Ctrl", "滚轮↑", "滚轮↓", "Alt", "滚轮←"]);
    });

    it("sets a step that is still empty, and speaks English", () => {
      captureAPI();
      render(
        <Draft
          language="en"
          initial={{ press: { type: "keys", keys: ["F13", ""] } }}
        />,
      );
      fireEvent.click(
        within(step(2, true)).getByRole("button", { name: "Mouse" }),
      );
      expect(items()).toEqual([
        "Left button",
        "Right button",
        "Middle button",
        "Side button 1",
        "Side button 2",
        "Wheel up",
        "Wheel down",
        "Wheel left",
        "Wheel right",
      ]);
      fireEvent.click(screen.getByRole("menuitem", { name: "Wheel right" }));
      expect(draft().press).toEqual({
        type: "keys",
        keys: ["F13", "WheelRight"],
      });
      expect(caps(step(2, true))).toEqual(["Wheel→"]);
      choose("Side button 1", step(2, true));
      expect(draft().press).toEqual({
        type: "keys",
        keys: ["F13", "MouseX1"],
      });
      expect(caps(step(2, true))).toEqual(["X1"]);
    });

    it("sets buttons in the steps, and the preview shows them", () => {
      captureAPI();
      render(
        <Draft
          initial={{ press: { type: "keys", keys: ["Ctrl+K", "F13"] } }}
        />,
      );
      choose("左键", step(1));
      choose("侧键 2", step(2));
      expect(draft().press).toEqual({
        type: "keys",
        keys: ["Ctrl+MouseLeft", "MouseX2"],
      });
      expect(shortcutValue("第 1 步")).toBe("Ctrl+MouseLeft");
      const preview = document.querySelector(
        ".drawer-sequence-preview",
      ) as HTMLElement;
      expect(caps(preview)).toEqual(["Ctrl", "左键", "侧键2"]);
    });

    it("is off while a step is captured", async () => {
      const { api } = captureAPI();
      render(
        <Draft initial={{ press: { type: "keys", keys: ["F13", "F14"] } }} />,
      );
      fireEvent.click(
        within(step(2)).getByRole("button", { name: "录入第 2 步" }),
      );
      await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
      expect(
        disabled(within(step(2)).getByRole("button", { name: "鼠标" })),
      ).toBe(true);
      // The other step has no capture of its own.
      expect(
        disabled(within(step(1)).getByRole("button", { name: "鼠标" })),
      ).toBe(false);
    });
  });

  it("works for the other gestures of a control alike", () => {
    captureAPI();
    render(
      <Draft
        initial={{ tap: { type: "keys", keys: "Ctrl+Y", mode: "tap" } }}
      />,
    );
    choose("滚轮向下");
    expect(draft().tap).toEqual({
      type: "keys",
      keys: "Ctrl+WheelDown",
      mode: "tap",
    });
    choose("右键");
    expect(draft().tap).toEqual({
      type: "keys",
      keys: "Ctrl+MouseRight",
      mode: "tap",
    });
  });
});

describe("mouse buttons and repeat (ADR-0062)", () => {
  const pick = (item: string) => {
    fireEvent.click(screen.getByRole("button", { name: /^(鼠标|Mouse)$/ }));
    fireEvent.click(screen.getByRole("menuitem", { name: item }));
  };
  const sending = () => screen.getByRole("radiogroup", { name: "发送方式" });
  const checked = () =>
    within(sending())
      .getAllByRole("radio")
      .filter((radio) => radio.getAttribute("aria-checked") === "true")
      .map((radio) => radio.textContent);
  const repeatSwitch = () => screen.getByRole("switch", { name: "连发" });
  const clicks = { style: "tap", delayMs: 350, rateHz: 12 };

  it("holds a button by default, like a key", () => {
    captureAPI();
    render(<Draft initial={{ press: { type: "keys", keys: "F13" } }} />);
    pick("左键");
    // Nothing else is written: holding is the default of a press.
    expect(draft().press).toEqual({ type: "keys", keys: "MouseLeft" });
    expect(checked()).toEqual(["按住保持"]);
    expect(repeatSwitch().getAttribute("aria-checked")).toBe("false");
    fireEvent.click(within(sending()).getByRole("radio", { name: "单次点按" }));
    expect(draft().press).toEqual({
      type: "keys",
      keys: "MouseLeft",
      mode: "tap",
    });
    fireEvent.click(within(sending()).getByRole("radio", { name: "按住保持" }));
    expect(draft().press).toEqual({
      type: "keys",
      keys: "MouseLeft",
      mode: "hold",
    });
  });

  it.each<[string, ProfileKeysAction]>([
    ["by default", { type: "keys", keys: "MouseLeft" }],
    ["in hold mode", { type: "keys", keys: "MouseLeft", mode: "hold" }],
    ["as the alias", { type: "keys", keys: "Shift+RButton" }],
  ])("repeats a button held %s as clicks", (_, press) => {
    captureAPI();
    render(<Draft initial={{ press }} />);
    expect(checked()).toEqual(["按住保持"]);
    fireEvent.click(repeatSwitch());
    // The drawer switches to tap mode: a held button is never pressed again
    // and again, so the daemon would refuse a typematic repeat.
    expect(draft().press).toEqual({
      ...press,
      mode: "tap",
      repeat: clicks,
    });
    expect(checked()).toEqual(["单次点按"]);
    expect(repeatSwitch().getAttribute("aria-checked")).toBe("true");
    // The timing of the clicks is edited like any repeat.
    commit(screen.getByRole("textbox", { name: "每秒次数" }), {
      target: { value: "20" },
    });
    expect(draft().press).toMatchObject({
      mode: "tap",
      repeat: { style: "tap", rateHz: 20 },
    });
    fireEvent.click(repeatSwitch());
    expect(draft().press).toEqual({ ...press, mode: "tap" });
  });

  it("keeps a tap mode that was already chosen", () => {
    captureAPI();
    render(
      <Draft
        initial={{ press: { type: "keys", keys: "MouseRight", mode: "tap" } }}
      />,
    );
    fireEvent.click(repeatSwitch());
    expect(draft().press).toEqual({
      type: "keys",
      keys: "MouseRight",
      mode: "tap",
      repeat: clicks,
    });
  });

  it("ends the repeat when the button is held again", () => {
    captureAPI();
    render(
      <Draft
        initial={{
          press: {
            type: "keys",
            keys: "Ctrl+MouseLeft",
            mode: "tap",
            repeat: { style: "tap", delayMs: 300, rateHz: 10 },
          },
        }}
      />,
    );
    expect(checked()).toEqual(["单次点按"]);
    fireEvent.click(within(sending()).getByRole("radio", { name: "按住保持" }));
    // There is no typematic style to switch to: the repeat goes.
    expect(draft().press).toEqual({
      type: "keys",
      keys: "Ctrl+MouseLeft",
      mode: "hold",
    });
    expect(checked()).toEqual(["按住保持"]);
    expect(repeatSwitch().getAttribute("aria-checked")).toBe("false");
    expect(screen.queryByRole("textbox", { name: "每秒次数" })).toBeNull();
    // And the other way: tapping needs no repeat to be changed.
    fireEvent.click(within(sending()).getByRole("radio", { name: "单次点按" }));
    expect(draft().press).toEqual({
      type: "keys",
      keys: "Ctrl+MouseLeft",
      mode: "tap",
    });
  });

  it("turns a typematic repeat into clicks when the key becomes a button", async () => {
    const { record } = captureAPI();
    render(
      <Draft
        initial={{
          press: {
            type: "keys",
            keys: "F13",
            mode: "hold",
            repeat: { style: "typematic", delayMs: 300, rateHz: 10 },
          },
        }}
      />,
    );
    expect(checked()).toEqual(["按住保持"]);
    pick("右键");
    expect(draft().press).toEqual({
      type: "keys",
      keys: "MouseRight",
      mode: "tap",
      repeat: { style: "tap", delayMs: 300, rateHz: 10 },
    });
    expect(checked()).toEqual(["单次点按"]);
    expect(repeatSwitch().getAttribute("aria-checked")).toBe("true");
    // Back to a key: the repeat stays what it is, clicks of that key.
    await record("Ctrl+Z");
    expect(draft().press).toEqual({
      type: "keys",
      keys: "Ctrl+Z",
      mode: "tap",
      repeat: { style: "tap", delayMs: 300, rateHz: 10 },
    });
    // Holding the key again repeats it the way a keyboard does.
    fireEvent.click(within(sending()).getByRole("radio", { name: "按住保持" }));
    expect(draft().press).toMatchObject({
      keys: "Ctrl+Z",
      mode: "hold",
      repeat: { style: "typematic" },
    });
  });

  it("settles a repeat without a mode or style as well", () => {
    captureAPI();
    render(
      <Draft
        initial={{
          press: { type: "keys", keys: "F13", repeat: { rateHz: 8 } },
        }}
      />,
    );
    pick("侧键 1");
    expect(draft().press).toEqual({
      type: "keys",
      keys: "MouseX1",
      mode: "tap",
      repeat: { style: "tap", rateHz: 8 },
    });
  });

  it("leaves a repeat of a key or of a wheel key as it is", () => {
    captureAPI();
    const typematic = {
      type: "keys",
      keys: "F13",
      mode: "hold",
      repeat: { style: "typematic", delayMs: 300, rateHz: 10 },
    } as const;
    render(<Draft initial={{ press: typematic }} />);
    // A wheel key is a notch per repeat, which the daemon accepts held.
    pick("滚轮向上");
    expect(draft().press).toEqual({ ...typematic, keys: "WheelUp" });
    expect(checked()).toEqual(["按住保持"]);
    // The switch repeats a wheel key the way it repeats a key.
    fireEvent.click(repeatSwitch());
    expect(draft().press).toEqual({
      type: "keys",
      keys: "WheelUp",
      mode: "hold",
    });
    fireEvent.click(repeatSwitch());
    expect(draft().press).toMatchObject({
      keys: "WheelUp",
      mode: "hold",
      repeat: { style: "typematic" },
    });
  });

  it("settles nothing for a gesture that cannot hold", () => {
    captureAPI();
    render(
      <Draft initial={{ tap: { type: "keys", keys: "F13", mode: "tap" } }} />,
    );
    pick("左键");
    expect(draft().tap).toEqual({
      type: "keys",
      keys: "MouseLeft",
      mode: "tap",
    });
    expect(screen.queryByRole("radiogroup", { name: "发送方式" })).toBeNull();
    expect(screen.queryByRole("switch", { name: "连发" })).toBeNull();
  });

  it("drops the repeat when a button gets a next step", () => {
    captureAPI();
    render(
      <Draft
        initial={{
          press: {
            type: "keys",
            keys: "MouseLeft",
            mode: "tap",
            repeat: { style: "tap", delayMs: 300, rateHz: 10 },
          },
        }}
      />,
    );
    addStep();
    expect(draft().press).toEqual({
      type: "keys",
      keys: ["MouseLeft", ""],
      mode: "tap",
    });
  });

  it("speaks English", () => {
    captureAPI();
    render(
      <Draft
        language="en"
        initial={{ press: { type: "keys", keys: "MouseMiddle" } }}
      />,
    );
    const mode = screen.getByRole("radiogroup", { name: "Sending" });
    expect(
      within(mode).getByRole("radio", { name: "Hold with button" }),
    ).toBeDefined();
    fireEvent.click(screen.getByRole("switch", { name: "Repeat" }));
    expect(draft().press).toEqual({
      type: "keys",
      keys: "MouseMiddle",
      mode: "tap",
      repeat: clicks,
    });
    expect(
      within(mode)
        .getByRole("radio", { name: "Tap once" })
        .getAttribute("aria-checked"),
    ).toBe("true");
  });
});

describe("scripts (SCR-50)", () => {
  const typeRadio = (name: string) =>
    within(screen.getByTestId("action-type")).getByRole("radio", { name });
  const picker = () =>
    screen.getByRole("combobox", { name: "选择脚本" }) as HTMLSelectElement;

  it("picks a script of the profile and leaves the draft alone until then", () => {
    captureAPI();
    render(<Draft macros={scripts} />);
    fireEvent.click(typeRadio("脚本"));
    // Nothing is picked yet: the draft keeps its shortcut.
    expect(draft().press).toEqual({ type: "keys", keys: "F13" });
    expect(picker().value).toBe("");
    // A file whose name cannot be used is not offered. (The order is the
    // collator's, which depends on the locale for names like these.)
    const options = [...picker().options].map((option) => [
      option.value,
      option.text,
    ]);
    expect(options[0]).toEqual(["", "选择脚本"]);
    expect(options.slice(1).sort()).toEqual([
      ["下一个笔刷.js", "下一个笔刷"],
      ["复制全部.ts", "复制全部"],
    ]);
    // Changing your mind keeps the shortcut too.
    fireEvent.click(typeRadio("快捷键"));
    expect(draft().press).toEqual({ type: "keys", keys: "F13" });
    fireEvent.click(typeRadio("脚本"));
    fireEvent.change(picker(), { target: { value: "复制全部.ts" } });
    expect(draft().press).toEqual({ type: "macro", file: "复制全部.ts" });
    expect(screen.queryByRole("button", { name: /录入/ })).toBeNull();
    // Switching back restores the shortcut, and then the script.
    fireEvent.click(typeRadio("快捷键"));
    expect(draft().press).toEqual({ type: "keys", keys: "F13" });
    fireEvent.click(typeRadio("脚本"));
    expect(draft().press).toEqual({ type: "macro", file: "复制全部.ts" });
    const card = screen.getByRole("button", { name: /^按下/ });
    expect(card.querySelector(".drawer-summary-macro")?.textContent).toBe(
      "复制全部",
    );
  });

  it("lists the scripts to pick from in dictionary order of the names", () => {
    captureAPI();
    const made = (name: string, file = `${name}.ts`) => ({
      file,
      name,
      usedBy: [],
    });
    const list = {
      macros: [
        made("Script 10"),
        made("Script 2"),
        made("Script"),
        made("Script 3", "Script 3.js"),
        // Never offered, wherever it sorts.
        { ...made("Script 0"), invalidName: true },
      ],
      missing: [],
    };
    const { unmount } = render(<Draft macros={{ list, failing: new Set() }} />);
    fireEvent.click(typeRadio("脚本"));
    expect([...picker().options].map((option) => option.value)).toEqual([
      "",
      "Script.ts",
      "Script 2.ts",
      "Script 3.js",
      "Script 10.ts",
    ]);
    unmount();
    // A picked script that the library lacks stays on top.
    render(
      <Draft
        initial={{ tap: { type: "macro", file: "gone.ts" } }}
        macros={{ list, failing: new Set() }}
      />,
    );
    expect([...picker().options].map((option) => option.value)).toEqual([
      "gone.ts",
      "Script.ts",
      "Script 2.ts",
      "Script 3.js",
      "Script 10.ts",
    ]);
  });

  it("points to the scripts page for a failing script and the first one", () => {
    captureAPI();
    const onShowScripts = vi.fn();
    const { unmount } = render(
      <Draft
        initial={{ tap: { type: "macro", file: "下一个笔刷.js" } }}
        macros={{ ...scripts, onShowScripts }}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "此脚本有错误" }));
    expect(onShowScripts).toHaveBeenCalledTimes(1);
    unmount();
    render(
      <Draft
        macros={{
          list: { macros: [], missing: [] },
          failing: new Set(),
          onShowScripts,
        }}
      />,
    );
    fireEvent.click(typeRadio("脚本"));
    expect(screen.getByTestId("macro-empty").textContent).toContain("暂无脚本");
    fireEvent.click(screen.getByRole("button", { name: "前往脚本页" }));
    expect(onShowScripts).toHaveBeenCalledTimes(2);
    expect(draft().press).toEqual({ type: "keys", keys: "F13" });
  });

  it("says when the file of a picked script is gone", () => {
    captureAPI();
    render(
      <Draft
        initial={{ hold: { type: "macro", file: "旧.ts" } }}
        macros={scripts}
      />,
    );
    const card = screen.getByRole("button", { name: /^长按/ });
    expect(
      card.querySelector(".drawer-summary-macro[data-missing]")?.textContent,
    ).toBe("找不到「旧」");
    expect(screen.getByTestId("macro-missing").textContent).toBe(
      "找不到「旧」",
    );
    expect(picker().value).toBe("旧.ts");
    fireEvent.change(picker(), { target: { value: "复制全部.ts" } });
    expect(draft().hold).toEqual({ type: "macro", file: "复制全部.ts" });
  });

  it("offers latch only while a gesture sends a shortcut", () => {
    captureAPI();
    const { unmount } = render(
      <Draft
        initial={{ latch: true, press: { type: "macro", file: "复制全部.ts" } }}
        macros={scripts}
      />,
    );
    fireEvent.click(screen.getByText("高级"));
    expect(screen.queryByRole("switch", { name: "锁存" })).toBeNull();
    // A value set before stays.
    expect(draft().latch).toBe(true);
    unmount();
    render(
      <Draft
        initial={{
          press: { type: "macro", file: "复制全部.ts" },
          hold: { type: "keys", keys: "F13" },
        }}
        macros={scripts}
      />,
    );
    fireEvent.click(screen.getByText("高级"));
    expect(screen.getByRole("switch", { name: "锁存" })).toBeTruthy();
  });
});

describe("the haptic of a binding (MAP-25)", () => {
  const options = (select: HTMLElement) =>
    within(select)
      .getAllByRole("option")
      .map((option) => option.textContent);

  it("is a select under 高级 with the presets, none first, and writes the preset", () => {
    captureAPI();
    render(<Draft />);
    fireEvent.click(screen.getByText("高级"));
    const select = screen.getByRole("combobox", { name: "触感" });
    expect(select.closest(".drawer-advanced")).not.toBeNull();
    expect(options(select)).toEqual([
      "无",
      "轻点",
      "确认",
      "错误",
      "1 下",
      "2 下",
      "3 下",
      "4 下",
    ]);
    expect((select as HTMLSelectElement).value).toBe("");
    for (const preset of [
      "tick",
      "confirm",
      "error",
      "pulse1",
      "pulse2",
      "pulse3",
      "pulse4",
    ]) {
      fireEvent.change(select, { target: { value: preset } });
      expect(draft().haptic).toBe(preset);
      expect((select as HTMLSelectElement).value).toBe(preset);
    }
    // No hint line under it: the label and the choices say it all.
    expect(select.closest("label")?.textContent).toBe(
      `触感${options(select).join("")}`,
    );
  });

  it("takes the key out of the binding for 无, and leaves everything else", () => {
    captureAPI();
    render(
      <Draft
        initial={{
          label: "撤销",
          latch: true,
          haptic: "pulse3",
          press: { type: "keys", keys: "F13" },
          timing: { holdMs: 300 },
        }}
      />,
    );
    fireEvent.click(screen.getByText("高级"));
    const select = screen.getByRole("combobox", { name: "触感" });
    expect((select as HTMLSelectElement).value).toBe("pulse3");
    fireEvent.change(select, { target: { value: "" } });
    expect(draft()).toEqual({
      label: "撤销",
      latch: true,
      press: { type: "keys", keys: "F13" },
      timing: { holdMs: 300 },
    });
    expect("haptic" in draft()).toBe(false);
  });

  it("stays when the binding's other fields change", () => {
    captureAPI();
    render(
      <Draft
        initial={{ haptic: "error", press: { type: "keys", keys: "F13" } }}
      />,
    );
    fireEvent.click(screen.getByText("高级"));
    fireEvent.click(screen.getByRole("switch", { name: "锁存" }));
    commit(screen.getByRole("textbox", { name: "触发长按时间（ms）" }), {
      target: { value: "400" },
    });
    fireEvent.change(screen.getByRole("combobox", { name: "触感" }), {
      target: { value: "tick" },
    });
    expect(draft()).toEqual({
      haptic: "tick",
      latch: true,
      timing: { holdMs: 400 },
      press: { type: "keys", keys: "F13" },
    });
  });

  it("is named in the summary of 高级 like the other settings", () => {
    captureAPI();
    const { unmount } = render(<Draft />);
    expect(screen.getByText("判定时间 · 锁存 · 触感")).toBeDefined();
    unmount();
    const withHaptic = render(
      <Draft
        initial={{ haptic: "tick", press: { type: "keys", keys: "F13" } }}
      />,
    );
    expect(screen.getByText("已设置：触感")).toBeDefined();
    withHaptic.unmount();
    render(
      <Draft
        initial={{
          latch: true,
          haptic: "tick",
          press: { type: "keys", keys: "F13" },
        }}
      />,
    );
    expect(screen.getByText("已设置：锁存、触感")).toBeDefined();
  });

  it("is named in English in the English interface", () => {
    captureAPI();
    render(<Draft language="en" />);
    expect(screen.getByText("Timing · Latch · Haptic")).toBeDefined();
    fireEvent.click(screen.getByText("Advanced"));
    const select = screen.getByRole("combobox", { name: "Haptic" });
    expect(options(select)).toEqual([
      "None",
      "Tick",
      "Confirm",
      "Error",
      "1 pulse",
      "2 pulses",
      "3 pulses",
      "4 pulses",
    ]);
    fireEvent.change(select, { target: { value: "pulse2" } });
    expect(draft().haptic).toBe("pulse2");
    expect(screen.getByText("Set: Haptic")).toBeDefined();
  });

  it("is offered for a key that runs a script, and not for a blocked key", () => {
    captureAPI();
    const { unmount } = render(
      <Draft
        initial={{ press: { type: "macro", file: "复制全部.ts" } }}
        macros={scripts}
      />,
    );
    expect(screen.getByText("判定时间 · 触感")).toBeDefined();
    fireEvent.click(screen.getByText("高级"));
    expect(screen.getByRole("combobox", { name: "触感" })).toBeDefined();
    unmount();
    // A blocked key plays nothing: the select is not shown, and the value
    // the file has is kept.
    render(<Draft initial={{ block: true, haptic: "tick" }} />);
    expect(screen.queryByRole("combobox", { name: "触感" })).toBeNull();
    expect(draft().haptic).toBe("tick");
  });

  it("is locked with the rest of the drawer", () => {
    captureAPI();
    render(
      <BindingFields
        value={{ haptic: "tick", press: { type: "keys", keys: "F13" } }}
        onChange={() => {}}
        language="zh-CN"
        disabled
      />,
    );
    fireEvent.click(screen.getByText("高级"));
    expect(
      screen.getByRole("combobox", { name: "触感" }).closest("fieldset")
        ?.disabled,
    ).toBe(true);
  });
});

describe("a repeat rate that follows the push depth (ANA-12, ANA-15)", () => {
  const repeating = (
    repeat: NonNullable<
      Extract<NonNullable<ProfileBinding["press"]>, { type: "keys" }>["repeat"]
    >,
  ): ProfileBinding => ({
    press: { type: "keys", keys: "Ctrl+Z", mode: "tap", repeat },
  });
  const depth = () => screen.queryByRole("switch", { name: "推得越深越快" });

  it("is offered for the controls of a stick, a trigger and a half axis", () => {
    for (const control of [
      "ls.up",
      "rs.downleft",
      "ls.active",
      "rs.inner",
      "ls.outer",
      "leftx+",
      "righty-",
      "lefttrigger+",
      "lt",
      "rt",
      "lt.full",
      "rt.full",
    ]) {
      const { unmount } = render(
        <Draft control={control} initial={repeating({ rateHz: 12 })} />,
      );
      expect(depth(), control).not.toBeNull();
      unmount();
    }
  });

  it("is not offered for buttons, d-pad diagonals, stick clicks and knob ticks", () => {
    for (const control of [
      "south",
      "leftshoulder",
      "dpad.upleft",
      "leftstick",
      "ls.cw",
      "rs.ccw",
      undefined,
    ]) {
      const { unmount } = render(
        <Draft control={control} initial={repeating({ rateHz: 12 })} />,
      );
      expect(depth(), String(control)).toBeNull();
      unmount();
    }
  });

  it("starts the slowest rate at 3 and names the two rates", () => {
    render(
      <Draft
        control="ls.up"
        initial={repeating({ delayMs: 350, rateHz: 12 })}
      />,
    );
    expect(screen.getByRole("textbox", { name: "每秒次数" })).toBeDefined();
    expect(depth()?.getAttribute("aria-checked")).toBe("false");
    fireEvent.click(depth() as HTMLElement);
    expect(draft().press).toMatchObject({
      repeat: { delayMs: 350, rateHz: 12, minRateHz: 3 },
    });
    expect(depth()?.getAttribute("aria-checked")).toBe("true");
    expect(screen.queryByRole("textbox", { name: "每秒次数" })).toBeNull();
    expect(
      (
        screen.getByRole("textbox", {
          name: "最慢（每秒）",
        }) as HTMLInputElement
      ).value,
    ).toBe("3");
    expect(
      (
        screen.getByRole("textbox", {
          name: "最快（每秒）",
        }) as HTMLInputElement
      ).value,
    ).toBe("12");
    expect(
      screen.getByRole("textbox", { name: "开始前等待（ms）" }),
    ).toBeDefined();
  });

  it("takes minRateHz out again, and only that", () => {
    render(
      <Draft
        control="lt"
        initial={repeating({ delayMs: 400, rateHz: 20, minRateHz: 5 })}
      />,
    );
    fireEvent.click(depth() as HTMLElement);
    expect(draft().press).toEqual({
      type: "keys",
      keys: "Ctrl+Z",
      mode: "tap",
      repeat: { delayMs: 400, rateHz: 20 },
    });
    expect(
      (screen.getByRole("textbox", { name: "每秒次数" }) as HTMLInputElement)
        .value,
    ).toBe("20");
  });

  it("goes with the repeat when that is turned off", () => {
    render(
      <Draft control="rt" initial={repeating({ rateHz: 12, minRateHz: 3 })} />,
    );
    fireEvent.click(screen.getByRole("switch", { name: "连发" }));
    expect(draft().press).toEqual({
      type: "keys",
      keys: "Ctrl+Z",
      mode: "tap",
    });
    expect(depth()).toBeNull();
  });

  it("keeps the slowest rate at or below the fastest", () => {
    render(
      <Draft
        control="ls.up"
        initial={repeating({ rateHz: 12, minRateHz: 3 })}
      />,
    );
    const slowest = screen.getByRole("textbox", { name: "最慢（每秒）" });
    const fastest = screen.getByRole("textbox", { name: "最快（每秒）" });
    commit(slowest, { target: { value: "20" } });
    expect(slowest.getAttribute("aria-invalid")).toBe("true");
    expect(screen.getByText("请输入 1–12 的整数")).toBeDefined();
    expect(draft().press).toMatchObject({ repeat: { minRateHz: 3 } });
    commit(slowest, { target: { value: "12" } });
    expect(draft().press).toMatchObject({ repeat: { minRateHz: 12 } });
    commit(fastest, { target: { value: "8" } });
    expect(fastest.getAttribute("aria-invalid")).toBe("true");
    expect(draft().press).toMatchObject({ repeat: { rateHz: 12 } });
    // Raising the fastest first makes room.
    commit(fastest, { target: { value: "30" } });
    expect(draft().press).toMatchObject({ repeat: { rateHz: 30 } });
    commit(slowest, { target: { value: "20" } });
    expect(draft().press).toMatchObject({
      repeat: { rateHz: 30, minRateHz: 20 },
    });
    commit(slowest, { target: { value: "0" } });
    expect(slowest.getAttribute("aria-invalid")).toBe("true");
    commit(slowest, { target: { value: "2.5" } });
    expect(slowest.getAttribute("aria-invalid")).toBe("true");
    expect(draft().press).toMatchObject({ repeat: { minRateHz: 20 } });
  });

  it("does not take the range off when the slowest rate is cleared", () => {
    render(
      <Draft
        control="ls.up"
        initial={repeating({ rateHz: 12, minRateHz: 3 })}
      />,
    );
    const slowest = screen.getByRole("textbox", { name: "最慢（每秒）" });
    commit(slowest, { target: { value: "" } });
    // The field says what it needs; the file and the switch stay as they were.
    expect(slowest.getAttribute("aria-invalid")).toBe("true");
    expect(screen.getByText("请输入 1–12 的整数")).toBeDefined();
    expect(draft().press).toMatchObject({ repeat: { minRateHz: 3 } });
    expect(depth()?.getAttribute("aria-checked")).toBe("true");
    expect(screen.getByRole("textbox", { name: "最快（每秒）" })).toBeDefined();
    expect(screen.queryByRole("textbox", { name: "每秒次数" })).toBeNull();
    // Retyping a number works from there.
    commit(slowest, { target: { value: "5" } });
    expect(draft().press).toMatchObject({ repeat: { minRateHz: 5 } });
  });

  it("does not leave the default rate below the slowest when the fastest is cleared", () => {
    render(
      <Draft
        control="ls.up"
        initial={repeating({ rateHz: 40, minRateHz: 20 })}
      />,
    );
    commit(screen.getByRole("textbox", { name: "最快（每秒）" }), {
      target: { value: "" },
    });
    // The default is 12, under the slowest rate: the slowest is the floor.
    expect(draft().press).toMatchObject({
      repeat: { rateHz: 20, minRateHz: 20 },
    });
  });

  it("starts the slowest rate at the repeat rate when that is under 3", () => {
    render(<Draft control="ls.up" initial={repeating({ rateHz: 2 })} />);
    fireEvent.click(depth() as HTMLElement);
    expect(draft().press).toMatchObject({
      repeat: { rateHz: 2, minRateHz: 2 },
    });
  });

  it("counts a repeat without a rate as 12", () => {
    render(<Draft control="ls.up" initial={repeating({ delayMs: 300 })} />);
    fireEvent.click(depth() as HTMLElement);
    expect(draft().press).toMatchObject({
      repeat: { delayMs: 300, minRateHz: 3 },
    });
    commit(screen.getByRole("textbox", { name: "最慢（每秒）" }), {
      target: { value: "13" },
    });
    expect(draft().press).toMatchObject({ repeat: { minRateHz: 3 } });
    expect(
      screen
        .getByRole("textbox", { name: "最快（每秒）" })
        .getAttribute("placeholder"),
    ).toBe("12");
  });

  it("keeps the range when the way of sending changes", () => {
    render(
      <Draft
        control="ls.up"
        initial={repeating({ style: "tap", rateHz: 12, minRateHz: 3 })}
      />,
    );
    fireEvent.click(
      within(screen.getByRole("radiogroup", { name: "发送方式" })).getByRole(
        "radio",
        { name: "按住保持" },
      ),
    );
    expect(draft().press).toMatchObject({
      mode: "hold",
      repeat: { style: "typematic", rateHz: 12, minRateHz: 3 },
    });
  });

  it("lets a range the daemon would refuse be taken out", () => {
    // minRateHz on a button is not offered, but a file can have it.
    render(
      <Draft
        control="south"
        initial={repeating({ rateHz: 12, minRateHz: 3 })}
      />,
    );
    expect(depth()?.getAttribute("aria-checked")).toBe("true");
    fireEvent.click(depth() as HTMLElement);
    expect(draft().press).toMatchObject({ repeat: { rateHz: 12 } });
    expect(draft().press).not.toHaveProperty("repeat.minRateHz");
    expect(depth()).toBeNull();
  });

  it("gives a knob tick no repeat, unless the file already has one", () => {
    render(<Draft control="ls.cw" />);
    expect(screen.getByRole("radiogroup", { name: "发送方式" })).toBeDefined();
    expect(screen.queryByRole("switch", { name: "连发" })).toBeNull();
    cleanup();
    render(<Draft control="ls.cw" initial={repeating({ rateHz: 12 })} />);
    fireEvent.click(screen.getByRole("switch", { name: "连发" }));
    expect(draft().press).not.toHaveProperty("repeat");
    expect(screen.queryByRole("switch", { name: "连发" })).toBeNull();
  });

  it("is named in English", () => {
    render(
      <Draft
        control="rt.full"
        language="en"
        initial={repeating({ rateHz: 12, minRateHz: 3 })}
      />,
    );
    expect(
      screen.getByRole("switch", { name: "Faster when pushed deeper" }),
    ).toBeDefined();
    expect(
      screen.getByRole("textbox", { name: "Slowest (per second)" }),
    ).toBeDefined();
    expect(
      screen.getByRole("textbox", { name: "Fastest (per second)" }),
    ).toBeDefined();
  });
});

describe("the precision action (MOU-05)", () => {
  const typeGroup = () => screen.getByTestId("action-type");
  const typeRadio = (name: string) =>
    within(typeGroup()).getByRole("radio", { name });
  const types = () =>
    within(typeGroup())
      .getAllByRole("radio")
      .map((radio) => radio.textContent);
  const ratio = () =>
    screen.getByRole("textbox", { name: "降为原速" }) as HTMLInputElement;
  const slowed = {
    type: "mouse",
    op: "precision",
    factor: 0.5,
  } as const;
  const open = (name: RegExp) =>
    fireEvent.click(screen.getByRole("button", { name }));

  it("is offered on a press and a hold, and on no other gesture", () => {
    captureAPI();
    render(
      <Draft
        initial={{
          press: { type: "keys", keys: "F13" },
          hold: { type: "keys", keys: "F14" },
          tap: { type: "keys", keys: "F15" },
          release: { type: "keys", keys: "F16" },
          doubleTap: { type: "keys", keys: "F17" },
        }}
      />,
    );
    expect(types()).toEqual([
      "快捷键",
      "输入文字",
      "脚本",
      "精确模式",
      "按键提示",
    ]);
    open(/^长按/);
    expect(types()).toEqual([
      "快捷键",
      "输入文字",
      "脚本",
      "精确模式",
      "按键提示",
    ]);
    for (const gesture of [/^点按/, /^松开/, /^双击/]) {
      open(gesture);
      expect(types(), String(gesture)).toEqual(["快捷键", "输入文字", "脚本"]);
    }
  });

  it("turns a press into a precision action at 30%, and back to the shortcut", () => {
    captureAPI();
    render(<Draft />);
    fireEvent.click(typeRadio("精确模式"));
    expect(draft().press).toEqual({
      type: "mouse",
      op: "precision",
      factor: 0.3,
    });
    expect(typeRadio("精确模式").getAttribute("aria-checked")).toBe("true");
    expect(ratio().value).toBe("30");
    // The field says it is a percentage.
    expect(ratio().closest(".drawer-number-box")?.textContent).toBe("%");
    // No shortcut to record or to send, and no repeat.
    expect(screen.queryByRole("button", { name: /录入/ })).toBeNull();
    expect(screen.queryByRole("switch", { name: "连发" })).toBeNull();
    expect(screen.queryByRole("radiogroup", { name: "发送方式" })).toBeNull();
    // The card says what it does.
    const card = screen.getByRole("button", { name: /^按下/ });
    expect(card.querySelector(".drawer-summary-text")?.textContent).toBe(
      "精确模式 30%",
    );
    // Changing your mind restores the shortcut, and then the ratio.
    fireEvent.click(typeRadio("快捷键"));
    expect(draft().press).toEqual({ type: "keys", keys: "F13" });
    fireEvent.click(typeRadio("精确模式"));
    expect(draft().press).toEqual({
      type: "mouse",
      op: "precision",
      factor: 0.3,
    });
  });

  it("keeps the ratio that was set when the type is changed and changed back", () => {
    captureAPI();
    render(<Draft />);
    fireEvent.click(typeRadio("精确模式"));
    commit(ratio(), { target: { value: "45" } });
    expect(draft().press).toEqual({
      type: "mouse",
      op: "precision",
      factor: 0.45,
    });
    fireEvent.click(typeRadio("快捷键"));
    fireEvent.click(typeRadio("精确模式"));
    expect(draft().press).toEqual({
      type: "mouse",
      op: "precision",
      factor: 0.45,
    });
  });

  it("sets the ratio in whole percent from 5 to 100", () => {
    captureAPI();
    render(<Draft initial={{ hold: slowed }} />);
    expect(ratio().value).toBe("50");
    for (const [text, factor] of [
      ["5", 0.05],
      ["100", 1],
      ["7", 0.07],
      ["29", 0.29],
      ["57", 0.57],
    ] as const) {
      commit(ratio(), { target: { value: text } });
      expect(draft().hold, text).toEqual({
        type: "mouse",
        op: "precision",
        factor,
      });
    }
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("refuses a ratio outside 5 to 100, that is not whole, or that is empty", () => {
    captureAPI();
    render(<Draft initial={{ press: slowed }} />);
    for (const text of ["4", "101", "30.5", "abc", ""]) {
      commit(ratio(), { target: { value: text } });
      expect(ratio().getAttribute("aria-invalid"), text).toBe("true");
      expect(screen.getByRole("alert").textContent, text).toBe(
        "请输入 5–100 的整数",
      );
      expect(draft().press, text).toEqual(slowed);
    }
  });

  it("shows a precision action of the file, with its factor", () => {
    captureAPI();
    render(<Draft initial={{ hold: { type: "mouse", op: "precision" } }} />);
    expect(typeRadio("精确模式").getAttribute("aria-checked")).toBe("true");
    expect(ratio().value).toBe("30");
    // Nothing is written until it is edited.
    expect(draft().hold).toEqual({ type: "mouse", op: "precision" });
    commit(ratio(), { target: { value: "20" } });
    expect(draft().hold).toEqual({
      type: "mouse",
      op: "precision",
      factor: 0.2,
    });
  });

  it("keeps showing one that is on a gesture that cannot hold it", () => {
    // The daemon refuses it there and says so; the editor does not hide it.
    captureAPI();
    render(<Draft initial={{ tap: slowed }} />);
    expect(types()).toEqual(["快捷键", "输入文字", "脚本", "精确模式"]);
    expect(typeRadio("精确模式").getAttribute("aria-checked")).toBe("true");
    expect(ratio().value).toBe("50");
  });

  it("leaves the precision action in the draft while a script is being chosen", () => {
    captureAPI();
    render(<Draft initial={{ press: slowed }} macros={scripts} />);
    fireEvent.click(typeRadio("脚本"));
    expect(draft().press).toEqual(slowed);
    expect(typeRadio("脚本").getAttribute("aria-checked")).toBe("true");
    expect(screen.getByRole("combobox", { name: "选择脚本" })).toBeDefined();
    // Back to precision: the draft was never changed.
    fireEvent.click(typeRadio("精确模式"));
    expect(draft().press).toEqual(slowed);
    expect(ratio().value).toBe("50");
    // Picking a script replaces it, and the switch back restores it.
    fireEvent.click(typeRadio("脚本"));
    fireEvent.change(screen.getByRole("combobox", { name: "选择脚本" }), {
      target: { value: "复制全部.ts" },
    });
    expect(draft().press).toEqual({ type: "macro", file: "复制全部.ts" });
    fireEvent.click(typeRadio("精确模式"));
    expect(draft().press).toEqual(slowed);
  });

  it("goes from a script to precision and back to the script", () => {
    captureAPI();
    render(
      <Draft
        initial={{ press: { type: "macro", file: "复制全部.ts" } }}
        macros={scripts}
      />,
    );
    fireEvent.click(typeRadio("精确模式"));
    expect(draft().press).toEqual({
      type: "mouse",
      op: "precision",
      factor: 0.3,
    });
    fireEvent.click(typeRadio("脚本"));
    expect(draft().press).toEqual({ type: "macro", file: "复制全部.ts" });
  });

  it("adds no key to the key-only settings of the binding", () => {
    captureAPI();
    render(<Draft initial={{ press: slowed }} />);
    fireEvent.click(screen.getByText("高级"));
    expect(screen.queryByRole("switch", { name: "按物理位置" })).toBeNull();
  });

  it("shows a pointer jump from the file locked, as it does a profile switch", () => {
    captureAPI();
    const jump = { type: "mouse", op: "jump", x: 0.5, y: 0.25 } as const;
    render(
      <Draft initial={{ press: { type: "keys", keys: "F13" }, tap: jump }} />,
    );
    const tap = screen.getByRole("button", { name: /^点按/ });
    expect(tap.querySelector(".drawer-summary-locked")?.textContent).toBe(
      "指针跳到屏幕位置 50%，25%",
    );
    fireEvent.click(tap);
    // No type to change: the action is kept as the file has it.
    expect(screen.queryByTestId("action-type")).toBeNull();
    expect(screen.getByTestId("locked-action").textContent).toContain(
      "把指针移到屏幕位置（50%，25%）（在配置文件中设置）",
    );
    expect(draft().tap).toEqual(jump);
    // Removing it asks first, since the editor cannot set it again.
    fireEvent.click(screen.getByRole("button", { name: "删除此手势" }));
    expect(
      screen.getByRole("dialog", { name: "删除此手势？" }).textContent,
    ).toContain("此按键的点按当前会把指针移到屏幕位置（50%，25%）");
    expect(draft().tap).toEqual(jump);
  });

  it("speaks English", () => {
    captureAPI();
    render(<Draft language="en" />);
    fireEvent.click(typeRadio("Precision"));
    expect(draft().press).toEqual({
      type: "mouse",
      op: "precision",
      factor: 0.3,
    });
    commit(screen.getByRole("textbox", { name: "Speed while held" }), {
      target: { value: "2" },
    });
    expect(screen.getByRole("alert").textContent).toBe(
      "Enter a whole number from 5 to 100",
    );
    const card = screen.getByRole("button", { name: /^Press/ });
    expect(card.querySelector(".drawer-summary-text")?.textContent).toBe(
      "Precision 30%",
    );
  });
});

describe("the key hints action (HUD-03)", () => {
  const typeGroup = () => screen.getByTestId("action-type");
  const typeRadio = (name: string) =>
    within(typeGroup()).getByRole("radio", { name });
  const types = () =>
    within(typeGroup())
      .getAllByRole("radio")
      .map((radio) => radio.textContent);
  const hints = { type: "hud", op: "hints" } as const;
  const open = (name: RegExp) =>
    fireEvent.click(screen.getByRole("button", { name }));

  it("is offered next to precision on a press and a hold, and on no other gesture", () => {
    captureAPI();
    render(
      <Draft
        initial={{
          press: { type: "keys", keys: "F13" },
          hold: { type: "keys", keys: "F14" },
          tap: { type: "keys", keys: "F15" },
          release: { type: "keys", keys: "F16" },
          doubleTap: { type: "keys", keys: "F17" },
        }}
      />,
    );
    expect(types()).toEqual([
      "快捷键",
      "输入文字",
      "脚本",
      "精确模式",
      "按键提示",
    ]);
    open(/^长按/);
    expect(types()).toEqual([
      "快捷键",
      "输入文字",
      "脚本",
      "精确模式",
      "按键提示",
    ]);
    for (const gesture of [/^点按/, /^松开/, /^双击/]) {
      open(gesture);
      expect(types(), String(gesture)).toEqual(["快捷键", "输入文字", "脚本"]);
    }
  });

  it("turns a press into the hints action, which has no fields, and back to the shortcut", () => {
    captureAPI();
    render(<Draft />);
    fireEvent.click(typeRadio("按键提示"));
    expect(draft().press).toEqual(hints);
    expect(typeRadio("按键提示").getAttribute("aria-checked")).toBe("true");
    // Nothing to set, record or send: no field, no repeat, no switch.
    expect(screen.queryByRole("button", { name: /录入/ })).toBeNull();
    expect(screen.queryByRole("switch", { name: "连发" })).toBeNull();
    expect(screen.queryByRole("radiogroup", { name: "发送方式" })).toBeNull();
    expect(screen.queryByTestId("precision-fields")).toBeNull();
    // The card says what it does.
    const card = screen.getByRole("button", { name: /^按下/ });
    expect(card.querySelector(".drawer-summary-text")?.textContent).toBe(
      "按键提示",
    );
    // Changing your mind restores the shortcut, and then the hints.
    fireEvent.click(typeRadio("快捷键"));
    expect(draft().press).toEqual({ type: "keys", keys: "F13" });
    fireEvent.click(typeRadio("按键提示"));
    expect(draft().press).toEqual(hints);
  });

  it("goes between precision, the hints and a script without losing either", () => {
    captureAPI();
    render(<Draft macros={scripts} />);
    fireEvent.click(typeRadio("精确模式"));
    expect(draft().press).toEqual({
      type: "mouse",
      op: "precision",
      factor: 0.3,
    });
    fireEvent.click(typeRadio("按键提示"));
    expect(draft().press).toEqual(hints);
    fireEvent.click(typeRadio("精确模式"));
    expect(draft().press).toEqual({
      type: "mouse",
      op: "precision",
      factor: 0.3,
    });
    fireEvent.click(typeRadio("按键提示"));
    fireEvent.click(typeRadio("脚本"));
    // Choosing a script has not replaced it yet.
    expect(draft().press).toEqual(hints);
    fireEvent.click(typeRadio("按键提示"));
    expect(draft().press).toEqual(hints);
  });

  it("shows a hints action of the file as the type that is set", () => {
    captureAPI();
    render(<Draft initial={{ hold: hints }} />);
    expect(typeRadio("按键提示").getAttribute("aria-checked")).toBe("true");
    // Nothing is written until it is edited.
    expect(draft().hold).toEqual(hints);
  });

  it("keeps showing one that is on a gesture that cannot hold it", () => {
    // The daemon refuses it there and says so; the editor does not hide it.
    captureAPI();
    render(<Draft initial={{ tap: hints }} />);
    expect(types()).toEqual(["快捷键", "输入文字", "脚本", "按键提示"]);
    expect(typeRadio("按键提示").getAttribute("aria-checked")).toBe("true");
  });

  it("adds no key to the key-only settings of the binding", () => {
    captureAPI();
    render(<Draft initial={{ press: hints }} />);
    fireEvent.click(screen.getByText("高级"));
    expect(screen.queryByRole("switch", { name: "按物理位置" })).toBeNull();
  });

  it("speaks English", () => {
    captureAPI();
    render(<Draft language="en" />);
    fireEvent.click(typeRadio("Key hints"));
    expect(draft().press).toEqual(hints);
    const card = screen.getByRole("button", { name: /^Press/ });
    expect(card.querySelector(".drawer-summary-text")?.textContent).toBe(
      "Key hints",
    );
  });
});
