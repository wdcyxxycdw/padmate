import type { DeviceInfo } from "../../shared/api";
import type { Language } from "../state/store";
import {
  type Anchors,
  arrangeJoyCon,
  diagramHidden,
  joyConKind,
  staticLayout,
} from "./controller-joycon-layout";
import {
  controllerLabels,
  familyName,
  padHasControl,
  padHasFunctionKey,
} from "./controller-model";

/**
 * What decides a pad's model and its controls: the type, SDL's masks and
 * labels, and the USB ids (a Joy-Con 2 has the model of its own, see
 * modelShell).
 */
export type ModelInfo = Pick<
  DeviceInfo,
  "type" | "buttonLabels" | "buttonMask" | "axisMask"
> &
  Partial<Pick<DeviceInfo, "vendor" | "product">>;

/**
 * Where one controller chip sits on the stage.
 *
 * - `left`/`right`: the side columns. `slot` 2–7 counts from the top. A
 *   stick is two cells in one chip and takes two slots (`span`); `slot` is
 *   the first of them.
 * - `top` with a `side`: the shoulder corner above that column, `slot` 0–1.
 * - `top` without a side: the row above the controller, `offset` chip widths
 *   from the center. A `raised` chip sits a little higher than the rest.
 * - `bottom`: the row below the controller, `offset` chip widths from the
 *   center: the extra buttons (Share, Capture, mute, paddles).
 *
 * controller-3d.css turns these into positions for each chip density.
 */
export interface Callout {
  control: string;
  label: string;
  placement: "left" | "right" | "top" | "bottom";
  side?: "left" | "right";
  slot?: number;
  /** How many slots a chip of a side column is tall; 1 when absent. */
  span?: number;
  offset?: number;
  raised?: boolean;
}

// The extra buttons in the row below the controller, left to right: the left
// hand's paddles, the misc buttons (Share, Mute, Capture; the C button of a
// Switch 2 pad), the right hand's paddles (the upper one of each hand nearer
// the middle).
const bottomRow = [
  "paddle4",
  "paddle2",
  "misc1",
  "misc2",
  "paddle1",
  "paddle3",
];

// Each endpoint identifies one physical control; the same IDs feed selection
// and input feedback. A pad gets a chip for each control it has
// (padHasControl), so a single Joy-Con held sideways shows no D-pad and a
// DualSense Edge its four back and Fn buttons. A pair of Joy-Cons has no SL
// or SR chip: they cannot be pressed with the two joined (diagramHidden).
// A Joy-Con's chips are placed by controller-joycon-layout.ts; the others by
// the columns and rows below.
export function controllerCallouts(
  info: ModelInfo,
  language?: Language,
): Callout[] {
  const labels = controllerLabels(
    info.type,
    info.buttonLabels,
    info.buttonMask,
    language,
  );
  const ps = familyName(info.type) === "playstation";
  const has = (control: string) =>
    padHasControl(control, info) && !diagramHidden(info.type, control);
  const callouts: Callout[] = [];
  const add = (
    control: string,
    placement: Callout["placement"],
    position: Omit<Callout, "control" | "label" | "placement"> = {},
  ) => {
    if (has(control))
      callouts.push({
        control,
        label: labels[control],
        placement,
        ...position,
      });
  };
  // DOM order is the keyboard order: top band left to right, then the
  // columns, then the row below.
  add("lefttrigger", "top", { side: "left", slot: 0 });
  add("leftshoulder", "top", { side: "left", slot: 1 });
  // A single Joy-Con held sideways has no back, only start (ADR-0012).
  if (padHasFunctionKey("back", info.type, info.buttonMask))
    add("back", "top", { offset: -1 });
  // The touchpad takes the top center. The Xbox, PS or Home button has no
  // chip: padmate never maps it (ADR-0012).
  add("touchpad", "top", { offset: 0, raised: true });
  add("start", "top", { offset: 1 });
  add("righttrigger", "top", { side: "right", slot: 0 });
  add("rightshoulder", "top", { side: "right", slot: 1 });
  const left = ps
    ? ["dpad.up", "dpad.left", "dpad.right", "dpad.down", "leftstick"]
    : ["leftstick", "dpad.up", "dpad.left", "dpad.right", "dpad.down"];
  const right = ["north", "east", "west", "south", "rightstick"];
  // A stick holds its movement and its press (two cells), so it is two slots
  // tall: the chips after it move down by one. A column of fewer controls
  // sits about the middle of slots 2 to 7.
  const column = (controls: string[], placement: "left" | "right") => {
    const present = controls.filter(has);
    const span = (control: string) => (control.endsWith("stick") ? 2 : 1);
    const height = present.reduce((sum, control) => sum + span(control), 0);
    let slot = 2 + Math.floor((6 - height) / 2);
    for (const control of present) {
      add(
        control,
        placement,
        span(control) > 1 ? { slot, span: span(control) } : { slot },
      );
      slot += span(control);
    }
  };
  column(left, "left");
  column(right, "right");
  const extra = bottomRow.filter(has);
  extra.forEach((control, index) => {
    add(control, "bottom", { offset: index - (extra.length - 1) / 2 });
  });
  const kind = joyConKind(info);
  return kind ? staticLayout(callouts, kind) : callouts;
}

/**
 * Whether the controller picture shows a control: the pad has it
 * (padHasControl) and the picture does not leave it out (diagramHidden).
 */
export function diagramHasControl(control: string, info: ModelInfo): boolean {
  return padHasControl(control, info) && !diagramHidden(info.type, control);
}

/**
 * A Joy-Con's callouts placed for its model: where each leader ends decides
 * which side and in what order the chips go, so that no leader crosses
 * another. Other pads, which have no table of this kind, are returned as they
 * are.
 */
export function arrangeCallouts(
  callouts: Callout[],
  info: ModelInfo,
  anchors: Anchors,
): Callout[] {
  const kind = joyConKind(info);
  return kind ? arrangeJoyCon(callouts, kind, anchors) : callouts;
}

/**
 * The callouts of this language with the places an arrangement (made for the
 * model, in any language) gave the controls; a callout the arrangement lacks
 * keeps its own place.
 */
export function withArrangement(
  callouts: readonly Callout[],
  arranged: readonly Callout[],
): Callout[] {
  const own = new Map(callouts.map((callout) => [callout.control, callout]));
  const placed = arranged.flatMap((callout) => {
    const base = own.get(callout.control);
    return base ? [{ ...callout, label: base.label }] : [];
  });
  const done = new Set(placed.map((callout) => callout.control));
  return [...placed, ...callouts.filter((c) => !done.has(c.control))];
}

/**
 * The element a leader line starts from: the chip of a control, or for a stick
 * the box around its two cells (its leader is one, whichever cell it serves).
 */
export function calloutBox(
  host: ParentNode,
  control: string,
): HTMLElement | null {
  return (
    host.querySelector<HTMLElement>(`[data-callout-group="${control}"]`) ??
    host.querySelector<HTMLElement>(`[data-callout="${control}"]`)
  );
}

export { calloutEdge, calloutSide } from "./controller-layout";
