// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FieldError, numberParser, useCommitField } from "./commit-field";

afterEach(cleanup);

function Field({
  initial = "",
  onCommit,
  onDone,
}: {
  initial?: string;
  onCommit: (value: number | undefined) => void;
  onDone?: (how: "enter" | "escape") => void;
}) {
  const [value, setValue] = useState(initial);
  const field = useCommitField({
    value,
    parse: numberParser("zh-CN", { min: 0, max: 100, optional: true }),
    onCommit: (next) => {
      setValue(next === undefined ? "" : String(next));
      onCommit(next);
    },
    onDone,
  });
  return (
    <label>
      数值
      <input {...field.props} />
      <FieldError id={field.errorId} text={field.error} />
    </label>
  );
}

const input = () => screen.getByRole("textbox", { name: /^数值/ });

describe("commit fields", () => {
  it("commits on blur and on Enter, not while typing", () => {
    const commit = vi.fn();
    const done = vi.fn();
    render(<Field initial="5" onCommit={commit} onDone={done} />);
    fireEvent.change(input(), { target: { value: "1" } });
    fireEvent.change(input(), { target: { value: "12" } });
    expect(commit).not.toHaveBeenCalled();
    fireEvent.blur(input());
    expect(commit).toHaveBeenCalledExactlyOnceWith(12);
    fireEvent.change(input(), { target: { value: "30" } });
    fireEvent.keyDown(input(), { key: "Enter" });
    expect(commit).toHaveBeenLastCalledWith(30);
    expect(done).toHaveBeenCalledWith("enter");
    // Leaving a field that was not typed in commits nothing.
    fireEvent.blur(input());
    expect(commit).toHaveBeenCalledTimes(2);
  });

  it("keeps text that does not parse and shows why", () => {
    const commit = vi.fn();
    render(<Field initial="5" onCommit={commit} />);
    fireEvent.change(input(), { target: { value: "150" } });
    fireEvent.blur(input());
    expect(commit).not.toHaveBeenCalled();
    expect((input() as HTMLInputElement).value).toBe("150");
    expect(input().getAttribute("aria-invalid")).toBe("true");
    const error = screen.getByRole("alert");
    expect(error.textContent).toBe("请输入 0–100 的整数");
    expect(input().getAttribute("aria-describedby")).toBe(error.id);
    // Typing again clears the error until the next commit.
    fireEvent.change(input(), { target: { value: "1.5" } });
    expect(screen.queryByRole("alert")).toBeNull();
    fireEvent.keyDown(input(), { key: "Enter" });
    expect(screen.getByRole("alert")).toBeTruthy();
    expect(commit).not.toHaveBeenCalled();
  });

  it("puts the committed value back on Escape", () => {
    const commit = vi.fn();
    const done = vi.fn();
    const outer = vi.fn();
    render(
      // biome-ignore lint/a11y/noStaticElementInteractions: Records whether Escape reached the parent.
      <div onKeyDown={(event) => outer(event.key)}>
        <Field initial="5" onCommit={commit} onDone={done} />
      </div>,
    );
    fireEvent.change(input(), { target: { value: "abc" } });
    fireEvent.blur(input());
    fireEvent.keyDown(input(), { key: "Escape" });
    expect((input() as HTMLInputElement).value).toBe("5");
    expect(screen.queryByRole("alert")).toBeNull();
    expect(done).toHaveBeenCalledWith("escape");
    expect(outer).not.toHaveBeenCalled();
    // With nothing to undo, Escape goes on to the parent (a drawer).
    fireEvent.keyDown(input(), { key: "Escape" });
    expect(outer).toHaveBeenCalledWith("Escape");
    fireEvent.blur(input());
    expect(commit).not.toHaveBeenCalled();
  });

  it("commits empty as undefined when a default applies", () => {
    const commit = vi.fn();
    render(<Field initial="5" onCommit={commit} />);
    fireEvent.change(input(), { target: { value: " " } });
    fireEvent.blur(input());
    expect(commit).toHaveBeenCalledExactlyOnceWith(undefined);
  });

  it("takes Enter as a new line in a multi-line field, and commits on blur", () => {
    const commit = vi.fn();
    function Lines() {
      const [value, setValue] = useState("a");
      const field = useCommitField({
        value,
        multiline: true,
        onCommit: (next) => {
          setValue(next);
          commit(next);
        },
      });
      return <textarea aria-label="行" {...field.props} />;
    }
    render(<Lines />);
    const area = screen.getByRole("textbox", { name: "行" });
    fireEvent.change(area, { target: { value: "a\nb" } });
    // Enter is the key's own: it is not taken, so the browser adds the line.
    expect(fireEvent.keyDown(area, { key: "Enter" })).toBe(true);
    expect(commit).not.toHaveBeenCalled();
    fireEvent.blur(area);
    expect(commit).toHaveBeenCalledExactlyOnceWith("a\nb");
    // Escape still puts the committed text back.
    fireEvent.change(area, { target: { value: "x" } });
    fireEvent.keyDown(area, { key: "Escape" });
    expect((area as HTMLTextAreaElement).value).toBe("a\nb");
  });

  it("parses numbers in both languages", () => {
    const whole = numberParser("en", { min: 1, max: 9 });
    expect(whole("")).toEqual({ error: "Enter a whole number from 1 to 9" });
    expect(whole("2.5")).toEqual({ error: "Enter a whole number from 1 to 9" });
    expect(whole(" 3 ")).toEqual({ value: 3 });
    const any = numberParser("zh-CN", { min: 0, max: 1, integer: false });
    expect(any("0.25")).toEqual({ value: 0.25 });
    expect(any("1e")).toEqual({ error: "请输入 0–1 的数值" });
    expect(numberParser("en", { min: 0, max: 1, integer: false })("x")).toEqual(
      {
        error: "Enter a number from 0 to 1",
      },
    );
  });
});
