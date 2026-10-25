// What the overlay's key hints panel (HUD-03, ADR-0063) shows and in which
// order. The daemon lists the controls in no order that matters to a reader;
// the panel puts them in the order a pad is laid out (face buttons, shoulders,
// triggers, D-pad, centre buttons, sticks, the rest) and shows the first
// maxHintsShown of them.
import {
  type KeyHint,
  maxHintsShown,
  type OverlayPad,
} from "../shared/overlay";
import { listControlName } from "./control-names";
import { controllerLabels } from "./controller-model";
import { canonicalControl } from "./editor-model";
import type { Language } from "./store";

const order = [
  "south",
  "east",
  "west",
  "north",
  "leftshoulder",
  "rightshoulder",
  "lt",
  "lt.full",
  "rt",
  "rt.full",
  "dpad.up",
  "dpad.down",
  "dpad.left",
  "dpad.right",
  "dpad.upleft",
  "dpad.upright",
  "dpad.downleft",
  "dpad.downright",
  "back",
  "start",
  "guide",
  "leftstick",
  "rightstick",
  ...["ls", "rs"].flatMap((stick) => [
    ...["up", "down", "left", "right", "upleft", "upright"].map(
      (direction) => `${stick}.${direction}`,
    ),
    `${stick}.downleft`,
    `${stick}.downright`,
    `${stick}.active`,
    `${stick}.inner`,
    `${stick}.outer`,
    `${stick}.cw`,
    `${stick}.ccw`,
  ]),
  "leftx+",
  "leftx-",
  "lefty+",
  "lefty-",
  "rightx+",
  "rightx-",
  "righty+",
  "righty-",
  "lefttrigger+",
  "lefttrigger-",
  "righttrigger+",
  "righttrigger-",
  "paddle1",
  "paddle2",
  "paddle3",
  "paddle4",
  "misc1",
  "misc2",
  "misc3",
  "misc4",
  "misc5",
  "misc6",
  "touchpad",
];
const rank = new Map(order.map((control, index) => [control, index]));

/** Where a control comes in the panel; controls not in the list come last. */
function position(control: string): number {
  return rank.get(canonicalControl(control)) ?? order.length;
}

/**
 * The entries as the panel shows them: in pad order (controls the order does
 * not know after those, by name), the first maxHintsShown of them. `more`
 * says there were others, which the panel marks with a last line "…".
 */
export function arrangeHints(entries: readonly KeyHint[]): {
  shown: KeyHint[];
  more: boolean;
} {
  const sorted = [...entries].sort(
    (a, b) =>
      position(a.control) - position(b.control) ||
      (a.control < b.control ? -1 : a.control > b.control ? 1 : 0),
  );
  return {
    shown: sorted.slice(0, maxHintsShown),
    more: sorted.length > maxHintsShown,
  };
}

/**
 * A control's name in the panel: as the UI's key list names it, for the pad
 * the hints are for (generic names when Main did not know the pad).
 */
export function hintControlName(
  control: string,
  pad: OverlayPad | undefined,
  language: Language,
): string {
  const labels = controllerLabels(
    pad?.type ?? "",
    pad?.buttonLabels,
    pad?.buttonMask,
  );
  return listControlName(control, labels, language);
}
