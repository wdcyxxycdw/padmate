// Which chip of the controller picture a profile binding key belongs to. Its
// own module, with nothing heavy to import: the overlay's page names controls
// too (HUD-03, control-names.ts) and must not load the controller workspace.
import { canonicalControl } from "./editor-model";

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
  if (key.startsWith("ls.") || /^left[xy][+-]$/.test(key)) return "leftstick";
  if (key.startsWith("rs.") || /^right[xy][+-]$/.test(key)) return "rightstick";
  // Diagonals have no chip; each D-pad drawer sets them under 十字键设置.
  if (/^dpad\.(up|down)(left|right)$/.test(key)) return "dpad.up";
  return key;
}
