// @vitest-environment jsdom
// The step editor of the key drawer (UI-31): the steps of a sequence, a
// shortcut or a wait each (ADR-0076), and the choices around them.
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { useState } from "react";
import { afterEach, expect, it } from "vitest";
import type { ProfileBinding } from "../shared/generated/schema";
import { BindingFields } from "./BindingEditor";
import { fakeCapture, shortcutValue } from "./capture-fixture";
import { editorAPIFixture } from "./editor-api-fixture";
import { MacroContext } from "./macro-context";
import type { Language } from "./store";

afterEach(cleanup);

function Draft({
  initial,
  language = "zh-CN",
}: {
  initial: ProfileBinding;
  language?: Language;
}) {
  const [value, setValue] = useState<ProfileBinding>(initial);
  return (
    <MacroContext value={{ list: null, failing: new Set() }}>
      <BindingFields value={value} onChange={setValue} language={language} />
      <output data-testid="draft">{JSON.stringify(value)}</output>
    </MacroContext>
  );
}
const draft = (): ProfileBinding =>
  JSON.parse(screen.getByTestId("draft").textContent ?? "{}");

function captureAPI() {
  const api = editorAPIFixture();
  return { api, ...fakeCapture(api) };
}

/** Types into a field and leaves it, which commits the text (UI-43). */
function commit(field: Element, value: string) {
  fireEvent.change(field, { target: { value } });
  fireEvent.blur(field);
}

/** 增加动作序列: opens the menu and picks the kind of the new step. */
function addStep(kind: string, add = "增加动作序列") {
  fireEvent.click(screen.getByRole("button", { name: add }));
  fireEvent.click(screen.getByRole("menuitem", { name: kind }));
}

const step = (n: number) => screen.getByRole("group", { name: `第 ${n} 步` });

const mixed: ProfileBinding = {
  press: {
    type: "sequence",
    steps: [{ keys: "Ctrl+A" }, { wait: 100 }, { keys: "Ctrl+B" }],
  },
};

/** A sequence with a text step, which only a file can have (ADR-0076). */
const withText: ProfileBinding = {
  press: {
    type: "sequence",
    steps: [{ keys: "Ctrl+A" }, { wait: 100 }, { text: "你好" }],
  },
};

it("offers a shortcut and a wait as the next step, and no text", () => {
  captureAPI();
  render(<Draft initial={{ press: { type: "keys", keys: "F13" } }} />);
  fireEvent.click(screen.getByRole("button", { name: "增加动作序列" }));
  const menu = screen.getByRole("menu", { name: "增加动作序列" });
  expect(
    within(menu)
      .getAllByRole("menuitem")
      .map((item) => item.textContent),
  ).toEqual(["快捷键", "等待"]);
});

it("names the next steps in English", () => {
  captureAPI();
  render(
    <Draft language="en" initial={{ press: { type: "keys", keys: "F13" } }} />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Add action sequence" }));
  expect(
    screen.getAllByRole("menuitem").map((item) => item.textContent),
  ).toEqual(["Shortcut", "Wait"]);
});

it("keeps shortcuts only as a list, as before", () => {
  captureAPI();
  render(<Draft initial={{ press: { type: "keys", keys: "F13" } }} />);
  addStep("快捷键");
  expect(draft().press).toEqual({
    type: "keys",
    keys: ["F13", ""],
    mode: "tap",
  });
  // The row says what kind of step it is, on the left.
  expect(step(2).textContent).toContain("快捷键");
});

it("adds a wait: the draft becomes a sequence of the shortcut and 100 ms", () => {
  captureAPI();
  render(<Draft initial={{ press: { type: "keys", keys: "F13" } }} />);
  addStep("等待");
  expect(draft().press).toEqual({
    type: "sequence",
    steps: [{ keys: "F13" }, { wait: 100 }],
  });
  expect(step(1).textContent).toContain("快捷键");
  expect(step(2).textContent).toContain("等待");
  const wait = within(step(2)).getByRole("textbox", {
    name: "等待时间（ms）",
  }) as HTMLInputElement;
  expect(wait.value).toBe("100");
  // A new wait is filled in at once: focus is in its field.
  expect(document.activeElement).toBe(wait);
});

it("commits a wait on Enter or when it is left, from 1 to 10000 ms", () => {
  captureAPI();
  render(<Draft initial={mixed} />);
  const wait = () =>
    within(step(2)).getByRole("textbox", { name: "等待时间（ms）" });
  commit(wait(), "250");
  expect(draft().press).toMatchObject({
    steps: [{ keys: "Ctrl+A" }, { wait: 250 }, { keys: "Ctrl+B" }],
  });
  fireEvent.change(wait(), { target: { value: "10000" } });
  fireEvent.keyDown(wait(), { key: "Enter" });
  expect(draft().press).toMatchObject({
    steps: [{ keys: "Ctrl+A" }, { wait: 10000 }, { keys: "Ctrl+B" }],
  });
  for (const bad of ["0", "10001", "abc", "1.5", ""]) {
    commit(wait(), bad);
    expect(within(step(2)).getByRole("alert").textContent).toBe(
      "请输入 1–10000 的整数",
    );
    expect(wait().getAttribute("aria-invalid")).toBe("true");
    // Nothing was committed, and Escape puts the wait back.
    expect(draft().press).toMatchObject({
      steps: [{ keys: "Ctrl+A" }, { wait: 10000 }, { keys: "Ctrl+B" }],
    });
    fireEvent.keyDown(wait(), { key: "Escape" });
    expect((wait() as HTMLInputElement).value).toBe("10000");
    expect(within(step(2)).queryByRole("alert")).toBeNull();
  }
});

it("says what is wrong with a wait in English", () => {
  captureAPI();
  render(<Draft language="en" initial={mixed} />);
  const group = screen.getByRole("group", { name: "Step 2" });
  commit(within(group).getByRole("textbox", { name: "Wait (ms)" }), "0");
  expect(within(group).getByRole("alert").textContent).toBe(
    "Enter a whole number from 1 to 10000",
  );
});

it("shows a sequence from the file as rows, not locked", () => {
  captureAPI();
  render(<Draft initial={mixed} />);
  expect(screen.queryByTestId("locked-action")).toBeNull();
  expect(screen.getByRole("radio", { name: "快捷键" })).toBeDefined();
  expect(
    screen.getByRole("radio", { name: "快捷键" }).getAttribute("aria-checked"),
  ).toBe("true");
  expect(step(1).textContent).toContain("快捷键");
  expect(shortcutValue("第 1 步")).toBe("Ctrl+A");
  expect(step(2).textContent).toContain("等待");
  expect(step(3).textContent).toContain("快捷键");
  // The line above the rows reads the sequence left to right.
  const preview = document.querySelector(".drawer-sequence-preview");
  expect(preview?.textContent).toContain("Ctrl");
  expect(preview?.textContent).toContain("100 ms");
});

it("moves a step up or down, and the first cannot go up nor the last down", () => {
  captureAPI();
  render(<Draft initial={mixed} />);
  const button = (name: string) => screen.getByRole("button", { name });
  expect((button("上移第 1 步") as HTMLButtonElement).disabled).toBe(true);
  expect((button("下移第 3 步") as HTMLButtonElement).disabled).toBe(true);
  expect((button("上移第 2 步") as HTMLButtonElement).disabled).toBe(false);
  fireEvent.click(button("上移第 2 步"));
  expect(draft().press).toEqual({
    type: "sequence",
    steps: [{ wait: 100 }, { keys: "Ctrl+A" }, { keys: "Ctrl+B" }],
  });
  // The moved step is first now and cannot go up again: the focus is on the
  // button that moves it down.
  expect(document.activeElement).toBe(button("下移第 1 步"));
});

it("keeps the focus on the same button when a step moves down", () => {
  captureAPI();
  render(<Draft initial={mixed} />);
  fireEvent.click(screen.getByRole("button", { name: "下移第 1 步" }));
  expect(draft().press).toEqual({
    type: "sequence",
    steps: [{ wait: 100 }, { keys: "Ctrl+A" }, { keys: "Ctrl+B" }],
  });
  expect(document.activeElement).toBe(
    screen.getByRole("button", { name: "下移第 2 步" }),
  );
});

it("moves shortcuts too, ending a capture that is running", async () => {
  const { api } = captureAPI();
  render(
    <Draft
      initial={{ press: { type: "keys", keys: ["F13", "F14", "F15"] } }}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "录入第 1 步" }));
  await waitFor(() => expect(api.captureStart).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "下移第 1 步" }));
  await waitFor(() => expect(api.captureStop).toHaveBeenCalledTimes(1));
  expect(draft().press).toEqual({ type: "keys", keys: ["F14", "F13", "F15"] });
  expect(screen.queryByRole("button", { name: "录入中…" })).toBeNull();
});

it("removes a step, and with the last wait it is a list of shortcuts again, then one shortcut", () => {
  captureAPI();
  render(
    <Draft
      initial={{
        press: {
          type: "sequence",
          steps: [{ keys: "F13" }, { wait: 5 }, { keys: "F14" }, { wait: 9 }],
        },
      }}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "删除第 4 步" }));
  expect(draft().press).toEqual({
    type: "sequence",
    steps: [{ keys: "F13" }, { wait: 5 }, { keys: "F14" }],
  });
  // Focus goes to the step before the one that went.
  expect(document.activeElement).toBe(
    screen.getByRole("button", { name: "录入第 3 步" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "删除第 2 步" }));
  expect(draft().press).toEqual({
    type: "keys",
    keys: ["F13", "F14"],
    mode: "tap",
  });
  // A list of shortcuts is a list as before: the rows say they are shortcuts.
  expect(step(1).textContent).toContain("快捷键");
  // One shortcut is written as the drawer writes a shortcut: held, no mode.
  fireEvent.click(screen.getByRole("button", { name: "删除第 1 步" }));
  expect(draft().press).toEqual({ type: "keys", keys: "F14" });
  expect(screen.getByRole("group", { name: "快捷键" })).toBeDefined();
});

it("focuses the previous step's field when a step goes", () => {
  captureAPI();
  render(<Draft initial={mixed} />);
  fireEvent.click(screen.getByRole("button", { name: "删除第 3 步" }));
  expect(document.activeElement).toBe(
    within(step(2)).getByRole("textbox", { name: "等待时间（ms）" }),
  );
});

it("moves a wait that is the only step", () => {
  captureAPI();
  render(
    <Draft initial={{ press: { type: "sequence", steps: [{ wait: 50 }] } }} />,
  );
  expect(
    (screen.getByRole("button", { name: "上移第 1 步" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  expect(
    (screen.getByRole("button", { name: "下移第 1 步" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  // Removing it leaves nothing to send: an empty shortcut.
  fireEvent.click(screen.getByRole("button", { name: "删除第 1 步" }));
  expect(draft().press).toEqual({ type: "keys", keys: "" });
});

it("adds steps of both kinds to a sequence, a shortcut by recording", async () => {
  const { record } = captureAPI();
  render(<Draft initial={mixed} />);
  addStep("快捷键");
  expect(draft().press).toEqual({
    type: "sequence",
    steps: [
      { keys: "Ctrl+A" },
      { wait: 100 },
      { keys: "Ctrl+B" },
      { keys: "" },
    ],
  });
  await record("Enter", { field: "第 4 步" });
  expect(draft().press).toMatchObject({
    steps: [
      { keys: "Ctrl+A" },
      { wait: 100 },
      { keys: "Ctrl+B" },
      { keys: "Enter" },
    ],
  });
  addStep("等待");
  expect(draft().press).toMatchObject({
    steps: [
      { keys: "Ctrl+A" },
      { wait: 100 },
      { keys: "Ctrl+B" },
      { keys: "Enter" },
      { wait: 100 },
    ],
  });
});

it("stops offering a next step at 64", () => {
  captureAPI();
  const steps = Array.from({ length: 64 }, (_, i) => ({ wait: i + 1 }));
  render(<Draft initial={{ press: { type: "sequence", steps } }} />);
  expect(
    (screen.getByRole("button", { name: "增加动作序列" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
});

it("offers no way to type a text, and shows a text action of the file as no type", () => {
  captureAPI();
  render(<Draft initial={{ hold: { type: "text", text: "签名 😀" } }} />);
  expect(screen.queryByTestId("locked-action")).toBeNull();
  const types = within(screen.getByTestId("action-type"));
  expect(types.getAllByRole("radio").map((radio) => radio.textContent)).toEqual(
    ["快捷键", "脚本"],
  );
  // Neither is selected, and nothing below says what the text is: the card
  // does, as a file's action.
  for (const radio of types.getAllByRole("radio"))
    expect(radio.getAttribute("aria-checked")).toBe("false");
  expect(screen.queryByRole("textbox", { name: "输入的文字" })).toBeNull();
  expect(screen.queryByRole("button", { name: "增加动作序列" })).toBeNull();
  expect(screen.getByRole("button", { name: /^长按/ }).textContent).toContain(
    "签名 😀",
  );
  // Opening and not editing leaves the file's text as it is.
  expect(draft()).toEqual({ hold: { type: "text", text: "签名 😀" } });
  // Choosing a shortcut replaces it.
  fireEvent.click(types.getByRole("radio", { name: "快捷键" }));
  expect(draft().hold).toEqual({ type: "keys", keys: "" });
  expect(
    types.getByRole("radio", { name: "快捷键" }).getAttribute("aria-checked"),
  ).toBe("true");
});

it("shows a sequence with a text step of the file as no type, and a shortcut replaces it", () => {
  captureAPI();
  render(<Draft initial={withText} />);
  const types = within(screen.getByTestId("action-type"));
  for (const radio of types.getAllByRole("radio"))
    expect(radio.getAttribute("aria-checked")).toBe("false");
  // No rows to edit, but the card still reads the sequence as it is.
  expect(screen.queryByRole("group", { name: "第 1 步" })).toBeNull();
  expect(screen.queryByRole("button", { name: "增加动作序列" })).toBeNull();
  const press = screen.getByRole("button", { name: /^点按/ });
  expect(press.textContent).toContain("100 ms");
  expect(press.textContent).toContain("你好");
  expect(draft()).toEqual(withText);
  fireEvent.click(types.getByRole("radio", { name: "快捷键" }));
  expect(draft().press).toEqual({ type: "keys", keys: "" });
});

it("keeps the other steps when one is edited", () => {
  captureAPI();
  render(<Draft initial={mixed} />);
  commit(within(step(2)).getByRole("textbox", { name: "等待时间（ms）" }), "7");
  expect(draft().press).toEqual({
    type: "sequence",
    steps: [{ keys: "Ctrl+A" }, { wait: 7 }, { keys: "Ctrl+B" }],
  });
});

it("records a media key from the keyboard and shows it by name (OUT-20)", async () => {
  const { record } = captureAPI();
  render(<Draft initial={{ press: { type: "keys", keys: "" } }} />);
  await record("VolumeUp");
  expect(draft().press).toMatchObject({ type: "keys", keys: "VolumeUp" });
  const caps = () =>
    [
      ...screen
        .getByRole("group", { name: "快捷键" })
        .querySelectorAll(".drawer-capture-keys kbd"),
    ].map((cap) => cap.textContent);
  expect(caps()).toEqual(["音量加"]);
  await record("Ctrl+MediaNext");
  expect(caps()).toEqual(["Ctrl", "下一首"]);
  // The mapping's summary reads them the same way.
  expect(screen.getByRole("button", { name: /^点按/ }).textContent).toContain(
    "下一首",
  );
});

it("shows media keys in English, as steps too", async () => {
  const { record } = captureAPI();
  render(
    <Draft
      language="en"
      initial={{ press: { type: "keys", keys: ["MediaPlayPause", "F13"] } }}
    />,
  );
  const group = screen.getByRole("group", { name: "Step 1" });
  expect(group.textContent).toContain("Play/Pause");
  await record("VolumeMute", { field: "Step 2" });
  expect(draft().press).toMatchObject({
    keys: ["MediaPlayPause", "VolumeMute"],
  });
  expect(screen.getByRole("group", { name: "Step 2" }).textContent).toContain(
    "Mute",
  );
});

it("does not carry a wait typed and not committed to the step that takes its place", () => {
  captureAPI();
  render(
    <Draft
      initial={{
        press: { type: "sequence", steps: [{ wait: 100 }, { wait: 200 }] },
      }}
    />,
  );
  const field = (n: number) =>
    within(step(n)).getByRole("textbox", {
      name: "等待时间（ms）",
    }) as HTMLInputElement;
  commit(field(2), "abc");
  expect(within(step(2)).getByRole("alert")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "上移第 2 步" }));
  expect(draft().press).toEqual({
    type: "sequence",
    steps: [{ wait: 200 }, { wait: 100 }],
  });
  expect(field(1).value).toBe("200");
  expect(field(2).value).toBe("100");
  expect(within(step(2)).queryByRole("alert")).toBeNull();
});

it("does not hand a wait typed in a removed step to the step after it", () => {
  captureAPI();
  render(
    <Draft
      initial={{
        press: { type: "sequence", steps: [{ wait: 100 }, { wait: 200 }] },
      }}
    />,
  );
  const field = (n: number) =>
    within(step(n)).getByRole("textbox", {
      name: "等待时间（ms）",
    }) as HTMLInputElement;
  commit(field(1), "abc");
  fireEvent.click(screen.getByRole("button", { name: "删除第 1 步" }));
  expect(draft().press).toEqual({ type: "sequence", steps: [{ wait: 200 }] });
  expect(field(1).value).toBe("200");
  expect(within(step(1)).queryByRole("alert")).toBeNull();
});

it("says 未设置 for a text of the file or a shortcut still to be filled in", () => {
  captureAPI();
  render(
    <Draft
      initial={{
        tap: { type: "text", text: "" },
        hold: { type: "keys", keys: "" },
      }}
    />,
  );
  for (const name of [/^点按/, /^长按/])
    expect(screen.getByRole("button", { name }).textContent).toContain(
      "未设置",
    );
  expect(
    screen.getByRole("button", { name: /^点按/ }).textContent,
  ).not.toContain("「");
});

it("shows a sequence with a wait as what it sends, though its shortcut is not set", () => {
  captureAPI();
  render(
    <Draft
      initial={{
        press: { type: "sequence", steps: [{ keys: "" }, { wait: 150 }] },
      }}
    />,
  );
  const press = screen.getByRole("button", { name: /^点按/ });
  expect(press.textContent).toContain("150 ms");
  expect(press.textContent).not.toContain("未设置");
});
