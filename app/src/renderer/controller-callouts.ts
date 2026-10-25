import type { DeviceInfo } from "../shared/api";
import {
  controllerLabels,
  familyName,
  functionKeysOf,
} from "./controller-model";

export type ModelInfo = Pick<
  DeviceInfo,
  "type" | "buttonLabels" | "buttonMask"
>;

/**
 * Where one controller chip sits on the stage (UI-39).
 *
 * - `left`/`right`: the side columns. `slot` 2–6 counts from the top.
 * - `top` with a `side`: the shoulder corner above that column, `slot` 0–1.
 * - `top` without a side: the row above the controller, `offset` chip widths
 *   from the center. A `raised` chip sits a little higher than the rest.
 * - `bottom`: centered below the controller.
 *
 * controller-3d.css turns these into positions for each chip density.
 */
export interface Callout {
  control: string;
  label: string;
  placement: "left" | "right" | "top" | "bottom";
  side?: "left" | "right";
  slot?: number;
  offset?: number;
  raised?: boolean;
}

// Each endpoint identifies one physical control; the same IDs feed selection and input feedback.
export function controllerCallouts(info: ModelInfo): Callout[] {
  const labels = controllerLabels(
    info.type,
    info.buttonLabels,
    info.buttonMask,
  );
  const ps = familyName(info.type) === "playstation";
  const callouts: Callout[] = [];
  const add = (
    control: string,
    placement: Callout["placement"],
    position: Omit<Callout, "control" | "label" | "placement"> = {},
  ) => {
    callouts.push({ control, label: labels[control], placement, ...position });
  };
  // DOM order is the keyboard order: top band left to right, then the columns.
  add("lefttrigger", "top", { side: "left", slot: 0 });
  add("leftshoulder", "top", { side: "left", slot: 1 });
  // A single Joy-Con held sideways has no back (ADR-0069), only start.
  if (functionKeysOf(info.type, info.buttonMask).includes("back"))
    add("back", "top", { offset: -1 });
  if (ps) {
    // The PS button sits low between the sticks; the touchpad takes the top center.
    if (info.type !== "ps3")
      add("touchpad", "top", { offset: 0, raised: true });
  } else add("guide", "top", { offset: 0, raised: true });
  add("start", "top", { offset: 1 });
  add("righttrigger", "top", { side: "right", slot: 0 });
  add("rightshoulder", "top", { side: "right", slot: 1 });
  const left = ps
    ? ["dpad.up", "dpad.left", "dpad.right", "dpad.down", "leftstick"]
    : ["leftstick", "dpad.up", "dpad.left", "dpad.right", "dpad.down"];
  const right = ["north", "east", "west", "south", "rightstick"];
  left.forEach((control, index) => {
    add(control, "left", { slot: index + 2 });
  });
  right.forEach((control, index) => {
    add(control, "right", { slot: index + 2 });
  });
  if (ps) add("guide", "bottom", { offset: 0 });
  return callouts;
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
