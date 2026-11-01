import type { DeviceInfo } from "../../shared/api";
import {
  controllerLabels,
  familyName,
  padHasControl,
  padHasFunctionKey,
} from "./controller-model";

export type ModelInfo = Pick<
  DeviceInfo,
  "type" | "buttonLabels" | "buttonMask" | "axisMask"
>;

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
// hand's paddles, the misc button, the right hand's paddles (the upper one of
// each hand nearer the middle).
const bottomRow = ["paddle4", "paddle2", "misc1", "paddle1", "paddle3"];

// Each endpoint identifies one physical control; the same IDs feed selection
// and input feedback. A pad gets a chip for each control it has
// (padHasControl), so a single Joy-Con held sideways shows no D-pad and a
// DualSense Edge its four back and Fn buttons.
export function controllerCallouts(info: ModelInfo): Callout[] {
  const labels = controllerLabels(
    info.type,
    info.buttonLabels,
    info.buttonMask,
  );
  const ps = familyName(info.type) === "playstation";
  const has = (control: string) => padHasControl(control, info);
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
  return callouts;
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

/** The side of a chip that faces the controller. */
export function calloutSide(
  callout: Pick<Callout, "placement" | "side">,
): "left" | "right" | undefined {
  if (callout.side) return callout.side;
  return callout.placement === "left" || callout.placement === "right"
    ? callout.placement
    : undefined;
}

/**
 * Where a leader line leaves a chip: the middle of the edge that faces the
 * controller. `rect` is the chip's box relative to the stage.
 */
export function calloutEdge(
  callout: Pick<Callout, "placement" | "side">,
  rect: { left: number; top: number; width: number; height: number },
): { x: number; y: number } {
  const side = calloutSide(callout);
  const middle = rect.top + rect.height / 2;
  if (side === "left") return { x: rect.left + rect.width, y: middle };
  if (side === "right") return { x: rect.left, y: middle };
  const center = rect.left + rect.width / 2;
  return callout.placement === "bottom"
    ? { x: center, y: rect.top }
    : { x: center, y: rect.top + rect.height };
}
