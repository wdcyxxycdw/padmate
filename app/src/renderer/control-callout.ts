// Which chip of the controller picture a profile binding key belongs to. Its
// own module, with nothing heavy to import: the overlay's page names controls
// too (HUD-03, control-names.ts) and must not load the controller workspace.
import { canonicalControl } from "./editor-model";

/**
 * A stick is one callout (one leader, one anchor on the model) with two cells,
 * each its own button and drawer: its movement keeps the stick's id
 * ("leftstick": directions, knob, mouse, rings, raw axes), its press has
 * "leftstick.press" (the L3/R3 binding, which is a button like any other).
 */
const stickPressSuffix = ".press";

/** Selection id of the press cell of a stick, e.g. leftstick -> leftstick.press. */
export function stickPress(stick: string): string {
  return `${stick}${stickPressSuffix}`;
}

/** Whether a selection id is the press cell of a stick. */
export function isStickPress(control: string): boolean {
  return control === "leftstick.press" || control === "rightstick.press";
}

/** The callout a selection id sits on: both cells of a stick are on its callout. */
export function calloutOf(control: string): string {
  return isStickPress(control)
    ? control.slice(0, -stickPressSuffix.length)
    : control;
}

/** Callout control for a profile binding key, e.g. lt -> lefttrigger. */
export function calloutFor(control: string): string {
  const key = canonicalControl(control);
  // Half presses and raw axes are set in the trigger and stick drawers.
  if (
    key === "lt" ||
    key === "lt.full" ||
    key === "lefttrigger+" ||
    key === "lefttrigger-"
  )
    return "lefttrigger";
  if (
    key === "rt" ||
    key === "rt.full" ||
    key === "righttrigger+" ||
    key === "righttrigger-"
  )
    return "righttrigger";
  // The stick's own binding is its click: the press cell. Everything inside
  // the stick (directions, rings, ticks, raw axes) is its movement.
  if (key === "leftstick" || key === "rightstick") return stickPress(key);
  if (key.startsWith("ls.") || /^left[xy][+-]$/.test(key)) return "leftstick";
  if (key.startsWith("rs.") || /^right[xy][+-]$/.test(key)) return "rightstick";
  // Diagonals have no chip and the D-pad drawer no longer sets them; a file
  // that has them is read through the D-pad (the all-keys list, diagnostics).
  if (/^dpad\.(up|down)(left|right)$/.test(key)) return "dpad.up";
  return key;
}
