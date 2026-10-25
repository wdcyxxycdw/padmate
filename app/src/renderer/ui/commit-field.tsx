import { type ChangeEvent, type KeyboardEvent, useId, useState } from "react";

import type { Language } from "../store";

/** What a field's text means: the value to commit, or why it cannot be one. */
export type Parsed<T> = { value: T } | { error: string };

/**
 * A text field that commits on Enter or when it loses focus (UI-43), so
 * one finished entry is one save and one undo step. Text that does not
 * parse is not committed: the field keeps it and shows the error. Escape
 * puts the committed value back.
 */
export function useCommitField<T = string>({
  value,
  parse,
  multiline = false,
  onCommit,
  onDone,
}: {
  /** The committed value, as the field shows it. */
  value: string;
  /** Without it the text is the value. */
  parse?: (text: string) => Parsed<T>;
  /**
   * The field is a text area: Enter adds a line and only leaving the field
   * (or nothing else) commits.
   */
  multiline?: boolean;
  onCommit: (value: T) => void;
  /** After Enter committed or Escape put the value back. */
  onDone?: (how: "enter" | "escape") => void;
}) {
  // The text as typed, while it differs from the committed value.
  const [text, setText] = useState<string | null>(null);
  const [error, setError] = useState("");
  const errorId = useId();
  const commit = (): boolean => {
    if (text === null) return true;
    const result = parse ? parse(text) : { value: text as T };
    if ("error" in result) {
      setError(result.error);
      return false;
    }
    setText(null);
    setError("");
    onCommit(result.value);
    return true;
  };
  const restore = () => {
    setText(null);
    setError("");
  };
  return {
    error,
    errorId,
    /** Typed text that is not committed yet. */
    editing: text !== null,
    /** What the field shows: the typed text or the committed value. */
    text: text ?? value,
    commit,
    /** Drops typed text that is not committed. */
    reset: restore,
    props: {
      value: text ?? value,
      "aria-invalid": error ? true : undefined,
      "aria-describedby": error ? errorId : undefined,
      onChange: (
        event: ChangeEvent<HTMLInputElement | HTMLTextAreaElement>,
      ) => {
        setText(event.currentTarget.value);
        setError("");
      },
      onBlur: () => {
        commit();
      },
      onKeyDown: (
        event: KeyboardEvent<HTMLInputElement | HTMLTextAreaElement>,
      ) => {
        if (event.key === "Enter" && !multiline) {
          event.preventDefault();
          if (commit()) onDone?.("enter");
        } else if (event.key === "Escape" && (text !== null || error)) {
          event.preventDefault();
          event.stopPropagation();
          restore();
          onDone?.("escape");
        }
      },
    },
  };
}

/** The short error under a field that did not commit. */
export function FieldError({ id, text }: { id: string; text: string }) {
  if (!text) return null;
  return (
    <span className="field-error" id={id} role="alert">
      {text}
    </span>
  );
}

/**
 * Parses a number field: within [min, max], whole unless integer is false.
 * Empty is undefined when optional (a default applies), else an error.
 */
export function numberParser(
  language: Language | undefined,
  {
    min,
    max,
    integer = true,
    optional = false,
  }: { min: number; max: number; integer?: boolean; optional?: boolean },
): (text: string) => Parsed<number | undefined> {
  const problem =
    language === "en"
      ? integer
        ? `Enter a whole number from ${min} to ${max}`
        : `Enter a number from ${min} to ${max}`
      : integer
        ? `请输入 ${min}–${max} 的整数`
        : `请输入 ${min}–${max} 的数值`;
  return (text) => {
    const raw = text.trim();
    if (raw === "") return optional ? { value: undefined } : { error: problem };
    const number = Number(raw);
    return Number.isFinite(number) &&
      number >= min &&
      number <= max &&
      (!integer || Number.isInteger(number))
      ? { value: number }
      : { error: problem };
  };
}
