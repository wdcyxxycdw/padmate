// The self-check of the layouts preview (?check=1): what the browser really
// drew, read back from the DOM, against what the layout model says. The jsdom
// tests judge the layout from controller-layout.ts, which repeats the
// stylesheet's numbers; this one reads the chips' own boxes and the leaders'
// own end points, so a disagreement between the two (or a leader that was
// drawn before the chips moved) shows up here.

import { buildAssetModel } from "../../src/renderer/controller/controller-asset";
import {
  calloutBox,
  controllerCallouts,
} from "../../src/renderer/controller/controller-callouts";
import {
  calloutEdge,
  chipRect,
  type Leader,
  layoutProblems,
  projectedHull,
  projectPoint,
  type Rect,
  wideRowsOf,
} from "../../src/renderer/controller/controller-layout";
import {
  controllerModels,
  modelShell,
} from "../../src/renderer/controller/controller-models";
import type { DeviceInfo } from "../../src/shared/api";

export interface CellCheck {
  title: string;
  size: { width: number; height: number };
  chips: number;
  leaders: number;
  /** What is wrong, in words; nothing when the cell is right. */
  problems: string[];
}

/** Pixels a leader's end may be off the chip's edge or the part's anchor. */
const tolerance = 2;
/** Pixels the chip's box may differ from the layout model's. */
const boxTolerance = 3;

/** How far a point is from the border of a box (inside or out). */
function distanceToBorder(p: { x: number; y: number }, r: Rect): number {
  const dx = Math.max(r.left - p.x, 0, p.x - (r.left + r.width));
  const dy = Math.max(r.top - p.y, 0, p.y - (r.top + r.height));
  if (dx > 0 || dy > 0) return Math.hypot(dx, dy);
  return Math.min(
    p.x - r.left,
    r.left + r.width - p.x,
    p.y - r.top,
    r.top + r.height - p.y,
  );
}

const near = (a: number, b: number, by: number) => Math.abs(a - b) <= by;

/** Checks one Controller on the page. */
export async function checkCell(
  section: Element,
  info: DeviceInfo,
  title: string,
  notice: boolean,
): Promise<CellCheck> {
  const host = section.querySelector<HTMLElement>(".stage-host");
  const problems: string[] = [];
  if (!host) throw new Error(`No stage in ${title}`);
  const box = host.getBoundingClientRect();
  const stage = { width: box.width, height: box.height };
  const result: CellCheck = {
    title,
    size: { width: Math.round(stage.width), height: Math.round(stage.height) },
    chips: 0,
    leaders: 0,
    problems,
  };
  // The same model the page's scene built, for where each part is.
  const model = buildAssetModel(
    await controllerModels.get(modelShell(info.type, info)),
    info,
  );
  try {
    const anchors = model.layoutAnchors;
    if (!anchors) throw new Error(`${title} is not a Joy-Con`);
    const callouts = model.callouts;
    const rows = callouts.filter((c) => c.placement === "bottom").length;
    const wide = wideRowsOf(callouts, {
      width: anchors.width,
      halfHeight: anchors.halfHeight ?? 0,
      topWide: !anchors.narrowTop,
    });
    const leaders: Leader[] = [];
    const lines = new Map<string, Element>();
    for (const g of section.querySelectorAll("g[data-line]"))
      lines.set(g.getAttribute("data-line") ?? "", g);
    for (const callout of callouts) {
      const control = callout.control;
      const chip = calloutBox(host, control);
      const line = lines.get(control);
      if (!chip) {
        problems.push(`${control}: no chip`);
        continue;
      }
      result.chips++;
      const rect0 = chip.getBoundingClientRect();
      const rect: Rect = {
        left: rect0.left - box.left,
        top: rect0.top - box.top,
        width: rect0.width,
        height: rect0.height,
      };
      // The chip is where the layout model says (stylesheet and TypeScript
      // agree).
      const expected = chipRect(callout, stage, rows, notice, wide);
      for (const key of ["left", "top", "width", "height"] as const)
        if (!near(rect[key], expected[key], boxTolerance))
          problems.push(
            `${control}: chip ${key} is ${rect[key].toFixed(1)}, the layout model says ${expected[key].toFixed(1)}`,
          );
      const d = line
        ?.querySelector(".stage-line")
        ?.getAttribute("d")
        ?.match(/-?[\d.]+/g)
        ?.map(Number);
      if (!d || d.length < 4) {
        problems.push(`${control}: no leader`);
        continue;
      }
      result.leaders++;
      const from = { x: d[0], y: d[1] };
      const to = { x: d[2], y: d[3] };
      // It starts on the edge of its own chip that faces the controller...
      const start = calloutEdge(callout, rect);
      if (distanceToBorder(from, rect) > tolerance)
        problems.push(
          `${control}: leader starts ${distanceToBorder(from, rect).toFixed(1)} px from its chip (${from.x},${from.y})`,
        );
      else if (Math.hypot(from.x - start.x, from.y - start.y) > tolerance)
        problems.push(
          `${control}: leader starts at ${from.x},${from.y}, not the middle of the facing edge ${start.x.toFixed(1)},${start.y.toFixed(1)}`,
        );
      // ...and ends on its own part.
      const anchor = anchors.points[control];
      const end = projectPoint(anchor, anchors.width, stage);
      if (Math.hypot(to.x - end.x, to.y - end.y) > tolerance)
        problems.push(
          `${control}: leader ends at ${to.x},${to.y}, the part is at ${end.x.toFixed(1)},${end.y.toFixed(1)}`,
        );
      leaders.push({ control, rect, segment: { from, to } });
    }
    for (const control of lines.keys())
      if (!callouts.some((c) => c.control === control))
        problems.push(`${control}: leader without a chip`);
    const hull = projectedHull(anchors.outline ?? [], anchors.width, stage);
    for (const p of layoutProblems(leaders, stage, undefined, hull))
      problems.push(`${p.kind}: ${p.controls.join(" / ")}`);
    // Every chip of the layout is in the DOM once.
    const names = controllerCallouts(info).map((c) => c.control);
    for (const name of names)
      if (!callouts.some((c) => c.control === name))
        problems.push(`${name}: not in the arranged layout`);
  } finally {
    model.dispose();
  }
  return result;
}
