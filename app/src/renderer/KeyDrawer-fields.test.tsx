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
import type { ProfileBinding } from "../shared/generated/schema";
import { BindingFields } from "./BindingEditor";
import { fakeCapture, shortcutValue } from "./capture-fixture";
import { editorAPIFixture } from "./editor-api-fixture";
import { MacroContext, type MacroContextValue } from "./macro-context";
import type { Language } from "./store";

afterEach(() => {
  cleanup();
  // A mouse press that records a key is kept from the click that follows it
  // for a moment: the window losing focus ends that.
  window.dispatchEvent(new Event("blur"));
});

function Draft({
  initial = { press: { type: "keys", keys: "F13" } },
  language = "zh-CN",
  profiles,
  macros = { list: null, failing: new Set() },
}: {
  initial?: ProfileBinding;
  language?: Language;
  profiles?: { id: string; name?: string }[];
  /** The profile's scripts (SCR-50) the drawer picks from. */
  macros?: MacroContextValue;
}) {
  const [value, setValue] = useState<ProfileBinding>(initial);
  return (
    <MacroContext value={macros}>
      <BindingFields
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
function addStep(kind: "快捷键" | "等待" = "快捷键") {
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

it("captures modifiers while they are held, a chord when its key goes down, and honours the key position switch", async () => {
  const { api, emit } = captureAPI();
  render(<Draft />);
  // Shortcuts are recorded, never typed (G2); the key position option
  // lives under 高级, not next to the shortcut.
  const field = screen.getByRole("group", { name: "快捷键" });
  expect(within(field).queryByRole("textbox")).toBeNull();
  expect(within(field).queryByRole("switch")).toBeNull();
  fireEvent.click(screen.getByText("高级"));
  const physical = screen.getByRole("switch", { name: "映射扫描码" });
  expect(physical.closest(".drawer-advanced")).not.toBeNull();
  fireEvent.click(physical);
  expect(screen.getByText("已设置：映射扫描码")).toBeDefined();
  fireEvent.click(screen.getByRole("button", { name: "录入快捷键" }));
  await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
  expect(screen.getByRole("button", { name: "录入中…" })).toBeDefined();
  expect(document.querySelector(".drawer-hint")?.textContent).toBe(
    "请按下快捷键，也可以点一下鼠标或滚一格滚轮。按住 Esc 1 秒取消。",
  );
  // Modifiers show as soon as they go down; the capture is one edit, made
  // when it ends.
  await emit({ session: 1, active: true, chord: "LCtrl" });
  expect(shortcutValue()).toBe("LCtrl");
  expect(field.textContent).toContain("Ctrl");
  expect(draft().press).toEqual({ type: "keys", keys: "F13" });
  // Events from another session are not ours.
  await emit({ session: 2, active: true, chord: "Alt+X" });
  expect(shortcutValue()).toBe("LCtrl");
  // The first key ends the capture, with the position it was pressed at.
  await emit({
    session: 1,
    active: true,
    chord: "LCtrl+K",
    physicalChord: "LCtrl+sc:0x25",
  });
  expect(shortcutValue()).toBe("LCtrl+sc:0x25");
  expect(draft().press).toEqual({ type: "keys", keys: "LCtrl+sc:0x25" });
  expect(screen.getByRole("button", { name: "录入快捷键" })).toBeDefined();
  await waitFor(() =>
    expect(api.captureStop).toHaveBeenCalledExactlyOnceWith(1),
  );
});

it("keeps what is held when the window loses focus, with no alert", async () => {
  const { api, emit } = captureAPI();
  render(<Draft />);
  fireEvent.click(screen.getByRole("button", { name: "录入快捷键" }));
  await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
  await emit({ session: 1, active: true, chord: "LAlt" });
  await emit({ session: 1, active: false, reason: "blur" });
  expect(draft().press).toEqual({ type: "keys", keys: "LAlt" });
  expect(screen.getByRole("button", { name: "录入快捷键" })).toBeDefined();
  // The window losing focus is what ends a capture on its own: it needs
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
  // The key was pressed before the helper said which session it is: it ends
  // the capture as soon as it is known.
  await act(async () => resolve(5));
  expect(shortcutValue()).toBe("F20");
  expect(draft().press).toEqual({ type: "keys", keys: "F20" });
});

it("ends the capture with a real mouse click, which neither blur nor the click restarts", async () => {
  const { api } = captureAPI();
  render(<Draft language="en" />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "Capture shortcut" }));
  await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
  expect(document.activeElement).toBe(
    screen.getByRole("button", { name: "Recording…" }),
  );
  // The click is a key: the left button.
  await user.click(screen.getByRole("button", { name: "Recording…" }));
  await waitFor(() =>
    expect(api.captureStop).toHaveBeenCalledExactlyOnceWith(1),
  );
  expect(draft().press).toEqual({ type: "keys", keys: "MouseLeft" });
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
  const stop = screen.getByRole("button", { name: "录入中…" });
  // Focus moving inside the field keeps capturing.
  fireEvent.blur(stop, {
    relatedTarget: screen.getByRole("group", { name: "快捷键" }),
  });
  expect(screen.getByRole("button", { name: "录入中…" })).toBeDefined();
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

it("sends a sequence as taps, and one chord again is a held shortcut without a repeat", async () => {
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
  // A new step waits for its keys; it has no text entry either.
  const step = screen.getByRole("group", { name: "第 2 步" });
  expect(step.textContent).toContain("未设置");
  expect(within(step).queryByRole("textbox")).toBeNull();
  await record("Ctrl+V", { field: "第 2 步" });
  expect(draft().press).toMatchObject({ keys: ["F13", "Ctrl+V"] });
  expect(shortcutValue("第 2 步")).toBe("Ctrl+V");
  fireEvent.click(screen.getByRole("button", { name: "删除第 1 步" }));
  // One shortcut again is written as the drawer writes one: held, no mode.
  expect(draft().press).toEqual({ type: "keys", keys: "Ctrl+V" });
  expect(shortcutValue()).toBe("Ctrl+V");
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

it("only keeps one mapping open and lets the user close it", () => {
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
  const press = screen.getByRole("button", { name: /^点按/ });
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
  const press = screen.getByRole("button", { name: /^点按/ });
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
  fireEvent.click(screen.getByRole("switch", { name: "映射扫描码" }));
  fireEvent.click(screen.getByRole("button", { name: "录入第 2 步" }));
  await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
  await emit({
    session: 1,
    active: true,
    chord: "Ctrl+K",
    physicalChord: "Ctrl+sc25",
  });
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
  expect(screen.queryByRole("switch", { name: "映射扫描码" })).toBeNull();
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
  expect(screen.getByRole("button", { name: "Recording…" })).toBeDefined();
  // (A mouse press would be a key of the capture, and the click on a button
  // is kept from it: the step goes away some other way, as with the undo.)
  fireEvent.click(screen.getByRole("button", { name: "Remove step 1" }));
  await waitFor(() =>
    expect(api.captureStop).toHaveBeenCalledExactlyOnceWith(1),
  );
  expect(screen.queryByRole("button", { name: "Recording…" })).toBeNull();
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

it("moves focus to 添加映射 when a mapping card is removed", () => {
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
  const remove = screen.getByRole("button", { name: "删除此映射" });
  remove.focus();
  fireEvent.click(remove);
  expect(draft()).toEqual({ press: { type: "keys", keys: "F13" } });
  expect(document.activeElement).toBe(screen.getByTestId("add-gesture"));
  // A mapping picked from the menu and dropped again does the same.
  fireEvent.click(screen.getByTestId("add-gesture"));
  fireEvent.click(screen.getByRole("menuitem", { name: /^双击/ }));
  fireEvent.click(screen.getByRole("button", { name: "删除此映射" }));
  expect(draft()).toEqual({ press: { type: "keys", keys: "F13" } });
  expect(document.activeElement).toBe(screen.getByTestId("add-gesture"));
});

it("does not offer mappings on a blocked binding until it is unblocked", async () => {
  const { record } = captureAPI();
  render(<Draft language="en" initial={{ block: true }} />);
  expect(screen.queryByRole("group", { name: "Shortcut" })).toBeNull();
  expect(
    (screen.getByTestId("add-gesture") as HTMLButtonElement).disabled,
  ).toBe(true);
  expect(
    screen.getByText(
      "This key is blocked. To set mappings, unblock it under Advanced first.",
    ),
  ).toBeDefined();
  fireEvent.click(screen.getByRole("button", { name: "Unblock" }));
  expect(draft()).toEqual({});
  // The unblock button is gone; focus goes to the next step.
  expect(document.activeElement).toBe(screen.getByTestId("add-gesture"));
  await record("F9");
  expect(draft()).toEqual({ press: { type: "keys", keys: "F9" } });
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
  ).toEqual(["快捷键", "脚本"]);
  // Changing the other mapping's type leaves the switch untouched.
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

it("has no 自动 button and no text entry next to the shortcut (F7, G2)", () => {
  captureAPI();
  render(<Draft initial={{ press: { type: "keys", keys: "" } }} />);
  const field = screen.getByRole("group", { name: "快捷键" });
  // Recording, which hears the mouse too, is all there is.
  expect(
    within(field)
      .getAllByRole("button")
      .map((b) => b.textContent),
  ).toEqual(["录入快捷键"]);
  expect(screen.queryByRole("button", { name: "自动" })).toBeNull();
  expect(screen.queryByTestId("auto-shortcut")).toBeNull();
  expect(within(field).queryByRole("textbox")).toBeNull();
  expect(field.textContent).toContain("未设置快捷键");
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
    const card = screen.getByRole("button", { name: /^点按/ });
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

  it("offers latch only while a mapping sends a shortcut", () => {
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
