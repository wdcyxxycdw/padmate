// @vitest-environment jsdom
// The step editor of the key drawer (UI-31): the steps of a sequence, the text
// a binding types, and the choices around them.
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

/** 添加下一步: opens the menu and picks the kind of the new step. */
function addStep(kind: string, add = "添加下一步") {
  fireEvent.click(screen.getByRole("button", { name: add }));
  fireEvent.click(screen.getByRole("menuitem", { name: kind }));
}

const step = (n: number) => screen.getByRole("group", { name: `第 ${n} 步` });

const mixed: ProfileBinding = {
  press: {
    type: "sequence",
    steps: [{ keys: "Ctrl+A" }, { wait: 100 }, { text: "你好" }],
  },
};

it("offers a shortcut, a wait and a text as the next step", () => {
  captureAPI();
  render(<Draft initial={{ press: { type: "keys", keys: "F13" } }} />);
  fireEvent.click(screen.getByRole("button", { name: "添加下一步" }));
  const menu = screen.getByRole("menu", { name: "添加下一步" });
  expect(
    within(menu)
      .getAllByRole("menuitem")
      .map((item) => item.textContent),
  ).toEqual(["快捷键", "等待", "文字"]);
});

it("names the next steps in English", () => {
  captureAPI();
  render(
    <Draft language="en" initial={{ press: { type: "keys", keys: "F13" } }} />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Add a next step" }));
  expect(
    screen.getAllByRole("menuitem").map((item) => item.textContent),
  ).toEqual(["Shortcut", "Wait", "Text"]);
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
  // Neither a send mode nor a repeat: a sequence taps.
  expect(screen.queryByRole("radiogroup", { name: "发送方式" })).toBeNull();
  expect(screen.queryByRole("switch", { name: "连发" })).toBeNull();
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
    steps: [{ keys: "Ctrl+A" }, { wait: 250 }, { text: "你好" }],
  });
  fireEvent.change(wait(), { target: { value: "10000" } });
  fireEvent.keyDown(wait(), { key: "Enter" });
  expect(draft().press).toMatchObject({
    steps: [{ keys: "Ctrl+A" }, { wait: 10000 }, { text: "你好" }],
  });
  for (const bad of ["0", "10001", "abc", "1.5", ""]) {
    commit(wait(), bad);
    expect(within(step(2)).getByRole("alert").textContent).toBe(
      "请输入 1–10000 的整数",
    );
    expect(wait().getAttribute("aria-invalid")).toBe("true");
    // Nothing was committed, and Escape puts the wait back.
    expect(draft().press).toMatchObject({
      steps: [{ keys: "Ctrl+A" }, { wait: 10000 }, { text: "你好" }],
    });
    fireEvent.keyDown(wait(), { key: "Escape" });
    expect((wait() as HTMLInputElement).value).toBe("10000");
    expect(within(step(2)).queryByRole("alert")).toBeNull();
  }
});

it("adds a text: a sequence with an empty text, ready to be written", () => {
  captureAPI();
  render(<Draft initial={{ press: { type: "keys", keys: "F13" } }} />);
  addStep("文字");
  expect(draft().press).toEqual({
    type: "sequence",
    steps: [{ keys: "F13" }, { text: "" }],
  });
  const box = within(step(2)).getByRole("textbox", { name: "输入的文字" });
  expect(document.activeElement).toBe(box);
  expect(within(step(2)).getByTestId("text-left").textContent).toBe("剩余 256");
  commit(box, "你好");
  expect(draft().press).toEqual({
    type: "sequence",
    steps: [{ keys: "F13" }, { text: "你好" }],
  });
  expect(within(step(2)).getByTestId("text-left").textContent).toBe("剩余 254");
});

it("counts what is left while typing, an emoji as two", () => {
  captureAPI();
  render(<Draft initial={mixed} />);
  const box = () =>
    within(step(3)).getByRole("textbox", { name: "输入的文字" });
  const left = () => within(step(3)).getByTestId("text-left");
  expect(left().textContent).toBe("剩余 254");
  fireEvent.change(box(), { target: { value: "😀😀" } });
  expect(left().textContent).toBe("剩余 252");
  // Typing is not committing.
  expect(draft().press).toMatchObject({
    steps: [{ keys: "Ctrl+A" }, { wait: 100 }, { text: "你好" }],
  });
  fireEvent.blur(box());
  expect(draft().press).toMatchObject({
    steps: [{ keys: "Ctrl+A" }, { wait: 100 }, { text: "😀😀" }],
  });
});

it("takes Enter as a new line, which a text may have, and keeps a tab", () => {
  captureAPI();
  render(<Draft initial={mixed} />);
  const box = within(step(3)).getByRole("textbox", { name: "输入的文字" });
  fireEvent.change(box, { target: { value: "第一行\n\t第二行" } });
  // Enter is the key's own, so the browser adds the line; nothing is taken.
  expect(fireEvent.keyDown(box, { key: "Enter" })).toBe(true);
  expect(within(step(3)).queryByRole("alert")).toBeNull();
  fireEvent.blur(box);
  expect(draft().press).toMatchObject({
    steps: [{ keys: "Ctrl+A" }, { wait: 100 }, { text: "第一行\n\t第二行" }],
  });
});

it("does not commit a text that is too long, and says so", () => {
  captureAPI();
  render(<Draft initial={mixed} />);
  const box = () =>
    within(step(3)).getByRole("textbox", { name: "输入的文字" });
  fireEvent.change(box(), { target: { value: "x".repeat(257) } });
  // The message and the count are there while typing.
  expect(within(step(3)).getByRole("alert").textContent).toBe(
    "最多 256 个字符，表情符号占两个",
  );
  expect(within(step(3)).getByTestId("text-left").textContent).toBe("剩余 -1");
  expect(
    within(step(3)).getByTestId("text-left").getAttribute("data-over"),
  ).not.toBeNull();
  fireEvent.blur(box());
  expect(draft().press).toMatchObject({
    steps: [{ keys: "Ctrl+A" }, { wait: 100 }, { text: "你好" }],
  });
  expect(box().getAttribute("aria-invalid")).toBe("true");
  // Escape puts the committed text back.
  fireEvent.keyDown(box(), { key: "Escape" });
  expect((box() as HTMLTextAreaElement).value).toBe("你好");
  expect(within(step(3)).queryByRole("alert")).toBeNull();
  // Exactly the limit is fine.
  commit(box(), "x".repeat(256));
  expect(draft().press).toMatchObject({
    steps: [{ keys: "Ctrl+A" }, { wait: 100 }, { text: "x".repeat(256) }],
  });
});

it("does not commit a text with a control character, and says so", () => {
  captureAPI();
  render(<Draft initial={mixed} />);
  const box = () =>
    within(step(3)).getByRole("textbox", { name: "输入的文字" });
  commit(box(), "a\u0007b");
  expect(within(step(3)).getByRole("alert").textContent).toBe(
    "不能含有换行和制表符以外的控制字符",
  );
  expect(draft().press).toMatchObject({
    steps: [{ keys: "Ctrl+A" }, { wait: 100 }, { text: "你好" }],
  });
});

it("says the same in English", () => {
  captureAPI();
  render(<Draft language="en" initial={mixed} />);
  const group = screen.getByRole("group", { name: "Step 3" });
  const box = within(group).getByRole("textbox", { name: "Text to type" });
  expect(within(group).getByTestId("text-left").textContent).toBe("254 left");
  commit(box, "x".repeat(300));
  expect(within(group).getByRole("alert").textContent).toBe(
    "At most 256 characters; an emoji counts as two",
  );
  commit(box, "\u0000");
  expect(within(group).getByRole("alert").textContent).toBe(
    "No control characters except a newline and a tab",
  );
  const wait = within(screen.getByRole("group", { name: "Step 2" })).getByRole(
    "textbox",
    { name: "Wait (ms)" },
  );
  commit(wait, "0");
  expect(
    within(screen.getByRole("group", { name: "Step 2" })).getByRole("alert")
      .textContent,
  ).toBe("Enter a whole number from 1 to 10000");
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
  expect(step(3).textContent).toContain("文字");
  // The line above the rows reads the sequence left to right.
  const preview = document.querySelector(".drawer-sequence-preview");
  expect(preview?.textContent).toContain("Ctrl");
  expect(preview?.textContent).toContain("100 ms");
  expect(preview?.textContent).toContain("“你好”");
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
    steps: [{ wait: 100 }, { keys: "Ctrl+A" }, { text: "你好" }],
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
    steps: [{ wait: 100 }, { keys: "Ctrl+A" }, { text: "你好" }],
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
  expect(screen.queryByRole("button", { name: "停止捕获" })).toBeNull();
});

it("removes a step, and with the last wait or text it is a list of shortcuts again", () => {
  captureAPI();
  render(
    <Draft
      initial={{
        press: {
          type: "sequence",
          steps: [{ keys: "F13" }, { wait: 5 }, { keys: "F14" }, { text: "x" }],
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
  fireEvent.click(screen.getByRole("button", { name: "删除第 1 步" }));
  expect(draft().press).toEqual({ type: "keys", keys: "F14", mode: "tap" });
  expect(screen.getByRole("group", { name: "快捷键" })).toBeDefined();
});

it("focuses the previous step's field when a wait or a text goes", () => {
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
  expect(draft().press).toEqual({ type: "keys", keys: "", mode: "tap" });
});

it("adds steps of every kind to a sequence, a shortcut by recording", async () => {
  const { record } = captureAPI();
  render(<Draft initial={mixed} />);
  addStep("快捷键");
  expect(draft().press).toEqual({
    type: "sequence",
    steps: [{ keys: "Ctrl+A" }, { wait: 100 }, { text: "你好" }, { keys: "" }],
  });
  await record("Enter", { field: "第 4 步" });
  expect(draft().press).toMatchObject({
    steps: [
      { keys: "Ctrl+A" },
      { wait: 100 },
      { text: "你好" },
      { keys: "Enter" },
    ],
  });
  addStep("等待");
  addStep("文字");
  expect(draft().press).toMatchObject({
    steps: [
      { keys: "Ctrl+A" },
      { wait: 100 },
      { text: "你好" },
      { keys: "Enter" },
      { wait: 100 },
      { text: "" },
    ],
  });
});

it("stops offering a next step at 64", () => {
  captureAPI();
  const steps = Array.from({ length: 64 }, (_, i) => ({ wait: i + 1 }));
  render(<Draft initial={{ press: { type: "sequence", steps } }} />);
  expect(
    (screen.getByRole("button", { name: "添加下一步" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
});

it("types a text: the action type 输入文字 on any gesture", () => {
  captureAPI();
  render(<Draft initial={{ tap: { type: "keys", keys: "F13" } }} />);
  const types = within(screen.getByTestId("action-type"));
  fireEvent.click(types.getByRole("radio", { name: "输入文字" }));
  expect(draft().tap).toEqual({ type: "text", text: "" });
  const box = screen.getByRole("textbox", { name: "输入的文字" });
  expect(document.activeElement).not.toBe(box);
  expect(screen.getByTestId("text-left").textContent).toBe("剩余 256");
  commit(box, "Best regards,\nKim");
  expect(draft().tap).toEqual({ type: "text", text: "Best regards,\nKim" });
  expect(screen.getByTestId("text-left").textContent).toBe("剩余 239");
  // No shortcut, no send mode, no steps: a text is typed as it stands.
  expect(screen.queryByRole("button", { name: "添加下一步" })).toBeNull();
  expect(screen.queryByRole("radiogroup", { name: "发送方式" })).toBeNull();
  // Going back to a shortcut finds the shortcut that was there.
  fireEvent.click(types.getByRole("radio", { name: "快捷键" }));
  expect(draft().tap).toEqual({ type: "keys", keys: "F13" });
  // And back to the text.
  fireEvent.click(types.getByRole("radio", { name: "输入文字" }));
  expect(draft().tap).toEqual({ type: "text", text: "Best regards,\nKim" });
});

it("edits a text action from the file, not locked", () => {
  captureAPI();
  render(<Draft initial={{ hold: { type: "text", text: "签名 😀" } }} />);
  expect(screen.queryByTestId("locked-action")).toBeNull();
  const types = within(screen.getByTestId("action-type"));
  expect(
    types.getByRole("radio", { name: "输入文字" }).getAttribute("aria-checked"),
  ).toBe("true");
  const box = screen.getByRole("textbox", {
    name: "输入的文字",
  }) as HTMLTextAreaElement;
  expect(box.value).toBe("签名 😀");
  expect(screen.getByTestId("text-left").textContent).toBe("剩余 251");
});

it("shows a sequence and a text in the collapsed gesture, as what they send", () => {
  captureAPI();
  render(
    <Draft
      initial={{
        ...mixed,
        tap: { type: "text", text: "Best regards,\nKim here" },
      }}
    />,
  );
  const press = screen.getByRole("button", { name: /^按下/ });
  expect(press.textContent).toContain("Ctrl");
  expect(press.textContent).toContain("100 ms");
  expect(press.textContent).toContain("你好");
  expect(press.querySelector(".drawer-summary-locked")).toBeNull();
  const tap = screen.getByRole("button", { name: /^点按/ });
  expect(tap.textContent).toContain("Best regards, Ki");
  expect(tap.querySelector(".drawer-summary-locked")).toBeNull();
});

it("keeps the text of a step when another step is edited", () => {
  captureAPI();
  render(<Draft initial={mixed} />);
  commit(within(step(2)).getByRole("textbox", { name: "等待时间（ms）" }), "7");
  expect(draft().press).toEqual({
    type: "sequence",
    steps: [{ keys: "Ctrl+A" }, { wait: 7 }, { text: "你好" }],
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
  // The gesture's summary reads them the same way.
  expect(screen.getByRole("button", { name: /^按下/ }).textContent).toContain(
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

it("does not carry text typed in a step, and not committed, to the step that takes its place", () => {
  captureAPI();
  render(
    <Draft
      initial={{
        press: { type: "sequence", steps: [{ text: "A" }, { text: "B" }] },
      }}
    />,
  );
  const box = (n: number) =>
    within(step(n)).getByRole("textbox", {
      name: "输入的文字",
    }) as HTMLTextAreaElement;
  // Too long to be a text: the box keeps it and says why, nothing is saved.
  fireEvent.change(box(2), { target: { value: "x".repeat(257) } });
  fireEvent.blur(box(2));
  expect(within(step(2)).getByRole("alert")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "上移第 2 步" }));
  expect(draft().press).toEqual({
    type: "sequence",
    steps: [{ text: "B" }, { text: "A" }],
  });
  // Each row shows its own step; the junk went with the step it was typed in.
  expect(box(1).value).toBe("B");
  expect(box(2).value).toBe("A");
  expect(within(step(1)).queryByRole("alert")).toBeNull();
  expect(within(step(2)).queryByRole("alert")).toBeNull();
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

it("does not hand the text typed in a removed step to the step after it", () => {
  captureAPI();
  render(
    <Draft
      initial={{
        press: { type: "sequence", steps: [{ text: "A" }, { text: "B" }] },
      }}
    />,
  );
  const box = (n: number) =>
    within(step(n)).getByRole("textbox", {
      name: "输入的文字",
    }) as HTMLTextAreaElement;
  fireEvent.change(box(1), { target: { value: "x".repeat(257) } });
  fireEvent.blur(box(1));
  fireEvent.click(screen.getByRole("button", { name: "删除第 1 步" }));
  expect(draft().press).toEqual({ type: "sequence", steps: [{ text: "B" }] });
  expect(box(1).value).toBe("B");
  expect(within(step(1)).queryByRole("alert")).toBeNull();
});

it("says 未设置 for a text or a sequence still to be filled in, as for a shortcut", () => {
  captureAPI();
  render(
    <Draft
      initial={{
        tap: { type: "text", text: "" },
        press: { type: "sequence", steps: [{ keys: "" }, { text: "" }] },
        hold: { type: "keys", keys: "" },
      }}
    />,
  );
  for (const name of [/^点按/, /^按下/, /^长按/])
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
  const press = screen.getByRole("button", { name: /^按下/ });
  expect(press.textContent).toContain("150 ms");
  expect(press.textContent).not.toContain("未设置");
});
