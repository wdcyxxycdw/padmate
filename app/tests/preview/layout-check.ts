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
  distanceToSegment,
  gapOf,
  type Leader,
  layoutProblems,
  leaderPath,
  type Point,
  pathSegments,
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

/**
 * Whether two paths are the same line to within `tolerance`: the same
 * corners, or, where one has a corner the other has not (a leader a pixel
 * short of the distance across that the route calls straight), every corner
 * of each is on the other.
 */
function samePath(a: readonly Point[], b: readonly Point[]): boolean {
  if (a.length === b.length)
    return a.every(
      (p, i) => Math.hypot(p.x - b[i].x, p.y - b[i].y) <= tolerance,
    );
  const on = (p: Point, path: readonly Point[]) =>
    pathSegments(path).some((s) => distanceToSegment(p, s) <= tolerance);
  return a.every((p) => on(p, b)) && b.every((p) => on(p, a));
}

/** A point to a tenth of a pixel, as the scene draws it. */
const tenth = (p: Point): Point => ({
  x: Math.round(p.x * 10) / 10,
  y: Math.round(p.y * 10) / 10,
});

/** A path in words: its corners. */
const describe = (path: readonly Point[]) =>
  path.map((p) => `${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(" > ");

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
    // The boxes of the chips as the browser has them, for all of them first:
    // a steep leader keeps off the others (leaderPath).
    const measured = new Map<string, Rect>();
    for (const callout of callouts) {
      const chip = calloutBox(host, callout.control);
      if (!chip) {
        problems.push(`${callout.control}: no chip`);
        continue;
      }
      result.chips++;
      const rect0 = chip.getBoundingClientRect();
      measured.set(callout.control, {
        left: rect0.left - box.left,
        top: rect0.top - box.top,
        width: rect0.width,
        height: rect0.height,
      });
    }
    for (const callout of callouts) {
      const control = callout.control;
      const rect = measured.get(control);
      if (!rect) continue;
      // The chip is where the layout model says (stylesheet and TypeScript
      // agree).
      const expected = chipRect(callout, stage, rows, notice, wide);
      for (const key of ["left", "top", "width", "height"] as const)
        if (!near(rect[key], expected[key], boxTolerance))
          problems.push(
            `${control}: chip ${key} is ${rect[key].toFixed(1)}, the layout model says ${expected[key].toFixed(1)}`,
          );
      // The whole path the browser drew: every corner of it.
      const numbers = lines
        .get(control)
        ?.querySelector(".stage-line")
        ?.getAttribute("d")
        ?.match(/-?[\d.]+/g)
        ?.map(Number);
      if (!numbers || numbers.length < 4 || numbers.length % 2 !== 0) {
        problems.push(`${control}: no leader`);
        continue;
      }
      result.leaders++;
      const path: Point[] = [];
      for (let i = 0; i < numbers.length; i += 2)
        path.push({ x: numbers[i], y: numbers[i + 1] });
      const from = path[0];
      const to = path[path.length - 1];
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
      // In between it bends where the route says, from the chips and parts
      // as the layout model has them.
      const wanted = leaderPath(
        callout,
        tenth(start),
        tenth(end),
        gapOf(callout, anchors.points, anchors.width, stage),
        callout.route === "steep"
          ? [...measured].filter(([name]) => name !== control).map(([, r]) => r)
          : [],
      );
      if (!samePath(path, wanted))
        problems.push(
          `${control}: leader runs ${describe(path)}, the route says ${describe(wanted)}`,
        );
      leaders.push({
        control,
        rect,
        path,
        ...(callout.twin ? { twin: callout.twin } : {}),
      });
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
