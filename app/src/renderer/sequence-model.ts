// The steps of a sequence (MAP-22, UI-31, ADR-0070): what the step editor
// shows and how its edits become an action. Pure. The drawer offers a
// shortcut and a wait (ADR-0076); a text step of a file is still told apart.
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

/** The kinds of a step; a text step only a file has (ADR-0076). */
export type StepKind = "keys" | "wait" | "text";
/** The steps a sequence can be given in the drawer. */
export type NewStepKind = Exclude<StepKind, "text">;
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
export function newStep(kind: NewStepKind): Step {
  return kind === "keys" ? { keys: "" } : { wait: defaultWaitMs };
}

/**
 * Whether an action is a sequence with a text step. The drawer cannot edit
 * one (ADR-0076): it keeps working from the file, and the drawer shows it as
 * an action of no type until a shortcut or a script replaces it.
 */
export function hasTextStep(action: ProfileAction | undefined): boolean {
  return (
    action?.type === "sequence" &&
    action.steps.some((step) => stepKind(step) === "text")
  );
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
