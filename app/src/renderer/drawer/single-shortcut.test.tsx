// @vitest-environment jsdom
// The editor of a knob step (ADR-0013): one shortcut and nothing else.
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { ProfileBinding } from "../../shared/generated/schema";
import { fakeCapture, shortcutValue } from "../testing/capture-fixture";
import { editorAPIFixture } from "../testing/editor-api-fixture";
import { SingleShortcutField } from "./BindingEditor";

afterEach(cleanup);

function setup(value: ProfileBinding) {
  const { record } = fakeCapture(editorAPIFixture());
  const onChange = vi.fn();
  render(
    <SingleShortcutField value={value} onChange={onChange} language="zh-CN" />,
  );
  return { record, onChange };
}

it("records one shortcut as a press, and offers no mappings, sequence or script", async () => {
  const { record, onChange } = setup({});
  expect(shortcutValue()).toBe("");
  for (const name of ["添加其他动作", "增加动作序列", "脚本", "点按"])
    expect(screen.queryByRole("button", { name }), name).toBeNull();
  expect(screen.queryByRole("radio")).toBeNull();
  await record("Ctrl+]");
  expect(onChange).toHaveBeenLastCalledWith({
    press: { type: "keys", keys: "Ctrl+]" },
  });
});

it("shows the shortcut of a press, keeps the label and haptic while it changes, and clears the whole step", async () => {
  const { record, onChange } = setup({
    label: "放大",
    haptic: "tick",
    press: { type: "keys", keys: "F20" },
  });
  expect(shortcutValue()).toBe("F20");
  expect(screen.queryByTestId("single-shortcut-other")).toBeNull();
  await record("F21");
  expect(onChange).toHaveBeenLastCalledWith({
    label: "放大",
    haptic: "tick",
    press: { type: "keys", keys: "F21" },
  });
  fireEvent.click(screen.getByRole("button", { name: "清除快捷键" }));
  // Nothing of the step stays: a haptic left behind could not be taken away.
  expect(onChange).toHaveBeenLastCalledWith(undefined);
  cleanup();
  const bare = setup({ press: { type: "keys", keys: "F20" } });
  fireEvent.click(screen.getByRole("button", { name: "清除快捷键" }));
  expect(bare.onChange).toHaveBeenLastCalledWith(undefined);
});

it("names a binding the file set otherwise, and replaces it with the shortcut", async () => {
  for (const value of [
    { tap: { type: "keys", keys: "A" }, hold: { type: "keys", keys: "B" } },
    { press: { type: "macro", file: "放大.ts" } },
    { press: { type: "keys", keys: ["Ctrl+K", "S"] } },
  ] as ProfileBinding[]) {
    const { record, onChange } = setup(value);
    expect(screen.getByTestId("single-shortcut-other").textContent).toContain(
      "录入快捷键会替换它",
    );
    expect(shortcutValue()).toBe("");
    await record("F22");
    expect(onChange).toHaveBeenLastCalledWith({
      press: { type: "keys", keys: "F22" },
    });
    cleanup();
  }
});

it("clears a binding the file set otherwise with one click, whole", () => {
  for (const value of [
    { tap: { type: "keys", keys: "A" }, hold: { type: "keys", keys: "B" } },
    { press: { type: "macro", file: "放大.ts" }, haptic: "tick" },
    { block: true },
  ] as ProfileBinding[]) {
    const { onChange } = setup(value);
    fireEvent.click(screen.getByRole("button", { name: "清除这一格" }));
    expect(onChange).toHaveBeenCalledExactlyOnceWith(undefined);
    cleanup();
  }
  // A plain shortcut has no such button: its field clears it.
  setup({ press: { type: "keys", keys: "F20" } });
  expect(screen.queryByTestId("single-shortcut-clear")).toBeNull();
});
