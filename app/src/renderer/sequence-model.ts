// The steps of a sequence and the text a binding types (MAP-22, OUT-21,
// UI-31, ADR-0070): what the step editor shows, how its edits become an
// action, and the rules for a text. Pure.
import type {
  ProfileAction,
  ProfileKeysAction,
  ProfileSequenceAction,
  ProfileSequenceStep,
} from "../shared/generated/schema";

/** The most steps a sequence has; the daemon refuses more. */
export const maxSteps = 64;
/** The range of a wait step, in milliseconds. */
export const waitRange = { min: 1, max: 10000 } as const;
/** What a new wait step waits, in milliseconds. */
export const defaultWaitMs = 100;
/** The most UTF-16 code units a text has; a character outside the BMP is two. */
export const maxTextUnits = 256;

export type StepKind = "keys" | "wait" | "text";
export type Step = ProfileSequenceStep;

/** What a step does: tap a shortcut, wait, or type a text. */
export function stepKind(step: Step): StepKind {
  if (step.keys !== undefined) return "keys";
  if (step.wait !== undefined) return "wait";
  return "text";
}

/**
 * A step still being filled in: its shortcut or text is empty. Such a step is
 * not saved (UI-43) but the draft keeps it. A wait is never empty.
 */
export function isEmptyStep(step: Step): boolean {
  switch (stepKind(step)) {
    case "keys":
      return !(step.keys ?? "").trim();
    case "text":
      return !step.text;
    case "wait":
      return false;
  }
}

/** A step of a kind, ready to be filled in. */
export function newStep(kind: StepKind): Step {
  switch (kind) {
    case "keys":
      return { keys: "" };
    case "wait":
      return { wait: defaultWaitMs };
    case "text":
      return { text: "" };
  }
}

/**
 * The steps the step editor shows for an action: a list of shortcuts (the
 * sequence of a keys action) or a sequence action. null for any other action,
 * and for a keys action with a single shortcut, which has no steps.
 */
export function actionSteps(action: ProfileAction | undefined): Step[] | null {
  if (action?.type === "sequence") return action.steps;
  if (action?.type === "keys" && Array.isArray(action.keys))
    return action.keys.map((keys) => ({ keys }));
  return null;
}

/**
 * The action for steps, replacing previous (the action they were edited
 * from): shortcuts only are a keys action as before, one shortcut or a list
 * of them that taps; a wait or a text makes it a sequence action. A keys
 * action keeps what it has besides its shortcut. A list that starts from one
 * shortcut or from a sequence taps (mode "tap") and has no repeat, which a
 * list of shortcuts does not have; the edit of a list leaves its mode as it is.
 */
export function stepsAction(
  steps: Step[],
  previous?: ProfileAction,
): ProfileKeysAction | ProfileSequenceAction {
  if (steps.every((step) => stepKind(step) === "keys")) {
    const keys = steps.map((step) => step.keys ?? "");
    const edited = previous?.type === "keys" && Array.isArray(previous.keys);
    const { repeat: _repeat, ...rest } =
      previous?.type === "keys" ? previous : ({} as ProfileKeysAction);
    return {
      ...rest,
      type: "keys",
      ...(edited ? {} : { mode: "tap" as const }),
      keys: keys.length <= 1 ? (keys[0] ?? "") : keys,
    };
  }
  return { type: "sequence", steps };
}

/** steps with the step at index moved by delta places; the same list when it cannot move. */
export function movedStep(steps: Step[], index: number, delta: -1 | 1): Step[] {
  const to = index + delta;
  if (index < 0 || index >= steps.length || to < 0 || to >= steps.length)
    return steps;
  const next = [...steps];
  [next[index], next[to]] = [next[to], next[index]];
  return next;
}

/** The number of UTF-16 code units of a text, which is what the limit counts. */
export function textUnits(text: string): number {
  return text.length;
}

/** What is wrong with a text, if anything; empty is not (it is not saved yet). */
export type TextProblem = "long" | "control";

// The C0 and C1 control characters and DEL, except the newline and the tab:
// the rule of the daemon's keys.ValidateText.
function hasRefusedControl(text: string): boolean {
  for (let i = 0; i < text.length; i++) {
    const code = text.charCodeAt(i);
    if (code === 0x09 || code === 0x0a) continue;
    if (code <= 0x1f || (code >= 0x7f && code <= 0x9f)) return true;
  }
  return false;
}

/** Why a text cannot be typed, or null. */
export function textProblem(text: string): TextProblem | null {
  if (hasRefusedControl(text)) return "control";
  if (textUnits(text) > maxTextUnits) return "long";
  return null;
}

/** The message under a text field for a problem. */
export function textProblemText(
  problem: TextProblem,
  language: string | undefined,
): string {
  const en = language === "en";
  return problem === "control"
    ? en
      ? "No control characters except a newline and a tab"
      : "不能含有换行和制表符以外的控制字符"
    : en
      ? `At most ${maxTextUnits} characters; an emoji counts as two`
      : `最多 ${maxTextUnits} 个字符，表情符号占两个`;
}
