// Where the chips of the controller stage and their leader lines fall, in
// pixels, without a browser: the same numbers controller-3d.css gives the
// chips and controller-scene.ts gives the camera, kept here so that a layout
// can be judged (does a leader cross another, does it pass through a chip) by
// code and by tests. controller-layout.test.ts holds the stylesheet's sizes
// and these in step.
import type { Callout } from "./controller-callouts";

export interface Point {
  x: number;
  y: number;
}
export interface Point3 extends Point {
  z: number;
}
export interface Rect {
  left: number;
  top: number;
  width: number;
  height: number;
}
export interface Segment {
  from: Point;
  to: Point;
}
/** The size of the stage host in CSS pixels. */
export interface Stage {
  width: number;
  height: number;
}

/** The part of a callout that decides where its chip is. */
export type Place = Pick<
  Callout,
  "placement" | "side" | "slot" | "span" | "offset" | "raised"
>;

/**
 * The camera of the scene: its vertical field of view in degrees, the
 * distance it keeps at least, and how many times the model's width it fits
 * across the view when the stage is narrow.
 */
export const cameraSpec = { fov: 32, minDistance: 11.9, fitWidth: 1.7 };

/** How far from the model the camera stands for a stage of this aspect. */
export function cameraDistance(modelWidth: number, aspect: number) {
  return Math.max(
    cameraSpec.minDistance,
    (modelWidth * cameraSpec.fitWidth) /
      (2 * Math.tan(((cameraSpec.fov / 2) * Math.PI) / 180) * aspect),
  );
}

/**
 * Where a point of the model (its frame, centred, +Z towards the camera)
 * falls on the stage, as the scene's camera projects it.
 */
export function projectPoint(
  point: Point3,
  modelWidth: number,
  stage: Stage,
): Point {
  const aspect = stage.width / stage.height;
  const depth = cameraDistance(modelWidth, aspect) - point.z;
  const scale = Math.tan(((cameraSpec.fov / 2) * Math.PI) / 180) * depth;
  return {
    x: ((point.x / (scale * aspect) + 1) * stage.width) / 2,
    y: ((1 - point.y / scale) * stage.height) / 2,
  };
}

/**
 * The stage sizes a layout has to be clean on: every width and height the
 * workspace can give the stage (440 by 440 at least, controller-workspace's
 * `.controller-view`), with each side of the two density changes (600 by
 * 440 and 860 by 600) among them.
 */
export const supportedStages: readonly Stage[] = [
  440, 520, 599, 600, 640, 720, 859, 860, 1000, 1180, 1400, 1800,
].flatMap((width) =>
  [440, 500, 599, 600, 640, 700, 800, 900].map((height) => ({
    width,
    height,
  })),
);

export type Density = "full" | "compact" | "mini";

/** The chip density the stylesheet picks for a stage (container queries). */
export function stageDensity({ width, height }: Stage): Density {
  if (width < 600 || height < 440) return "mini";
  if (width < 860 || height < 600) return "compact";
  return "full";
}

const clamp = (min: number, value: number, max: number) =>
  Math.max(min, Math.min(value, max));

/** The sizes of controller-3d.css (--chip-h and the others) for a stage. */
export function chipMetrics(stage: Stage) {
  const cqw = stage.width / 100;
  const cqh = stage.height / 100;
  const density = stageDensity(stage);
  const sizes =
    density === "full"
      ? {
          chipH: 48,
          chipW: clamp(150, 17 * cqw, 196),
          pitch: 58,
          cornerGap: 14,
          cornerShift: 24,
          topW: clamp(120, 13 * cqw, 140),
          topGap: 10,
          topRaise: 22,
          topY: clamp(12, 22.6 * cqh - 64, 50 * cqh - 300),
          edge: 16,
          modelGap: 24,
        }
      : density === "compact"
        ? {
            chipH: 32,
            chipW: clamp(96, 16 * cqw, 124),
            pitch: 40,
            cornerGap: 8,
            cornerShift: 16,
            topW: clamp(88, 14 * cqw, 112),
            topGap: 8,
            topRaise: 16,
            topY: clamp(8, 22.6 * cqh - 44, 50 * cqh - 196),
            edge: 12,
            modelGap: 16,
          }
        : {
            chipH: 30,
            chipW: 30,
            pitch: 36,
            cornerGap: 4,
            cornerShift: 10,
            topW: 30,
            topGap: 8,
            topRaise: 12,
            topY: clamp(6, 22.6 * cqh - 36, 50 * cqh - 170),
            edge: 8,
            modelGap: 12,
          };
  return {
    density,
    ...sizes,
    sideX: Math.max(
      sizes.edge,
      50 * cqw - Math.min(41 * cqh, 29.4 * cqw) - sizes.chipW - sizes.modelGap,
    ),
  };
}

/**
 * How the rows above and below the controller are laid out when they are the
 * wide rows of a Joy-Con (`data-wide-rows` in controller-3d.css): a chip as
 * wide as one of a column when the stage has room, and narrower to share the
 * room there is between the corners above, or the columns.
 */
export interface WideRows {
  /** Whether the row above is wide too (a pair's is not). */
  topWide: boolean;
  /** The model's width in its frame, as the camera fits it. */
  width: number;
  /** How far the model reaches up and down from the middle, in the same units. */
  halfHeight: number;
  /** How many chips the row above has. */
  tops: number;
  /** Whether a shoulder corner has a chip, which the row above keeps clear of. */
  corners: boolean;
  /** Whether a column has a chip, which the row below keeps clear of. */
  columns: boolean;
}

/** What the model's size is, for rows that keep off it: its width and half its height. */
export interface ModelFrame {
  width: number;
  halfHeight: number;
  /** Whether the row above is wide as well as the row below; true when absent. */
  topWide?: boolean;
}

/** The size of a model before it is known: the stylesheet's default (--model-w, --model-h). */
export const defaultFrame: ModelFrame = { width: 5.6, halfHeight: 2.1 };

/**
 * How far the model reaches up and down, as the nearest the camera comes
 * shows it (a point in front of the middle looks larger).
 */
export function halfHeightOf(outline: readonly Point3[]): number {
  let reach = 0;
  for (const p of outline)
    reach = Math.max(
      reach,
      (Math.abs(p.y) * cameraSpec.minDistance) / (cameraSpec.minDistance - p.z),
    );
  return reach;
}

/** The pixels one unit of the model's frame is on a stage (the scene's camera). */
export function modelUnit(stage: Stage, modelWidth: number): number {
  return Math.min(
    stage.height /
      (2 *
        cameraSpec.minDistance *
        Math.tan(((cameraSpec.fov / 2) * Math.PI) / 180)),
    stage.width / (cameraSpec.fitWidth * modelWidth),
  );
}

/** The wide rows a list of callouts has. */
export function wideRowsOf(
  callouts: readonly Pick<Callout, "placement" | "side">[],
  frame: ModelFrame = defaultFrame,
): WideRows {
  return {
    width: frame.width,
    halfHeight: frame.halfHeight,
    topWide: frame.topWide ?? true,
    tops: callouts.filter((c) => c.placement === "top" && !c.side).length,
    corners: callouts.some((c) => c.placement === "top" && c.side),
    columns: callouts.some(
      (c) => c.placement === "left" || c.placement === "right",
    ),
  };
}

/**
 * The box of a chip on the stage, as controller-3d.css places it. `rows` is
 * the number of chips in the row below the controller, which shares the
 * width between them, `notice` that the editor notice is showing, which
 * lifts that row, and `wide` the wide rows of a Joy-Con, if it has them. A
 * stick's box holds both of its cells.
 */
export function chipRect(
  place: Place,
  stage: Stage,
  rows: number,
  notice = false,
  wide?: WideRows,
): Rect {
  const m = chipMetrics(stage);
  const middle = stage.height / 2;
  const slot = place.slot ?? 0;
  const span = place.span ?? 1;
  const offset = place.offset ?? 0;
  if (place.placement === "left" || place.placement === "right") {
    const height = m.chipH + (span - 1) * m.pitch;
    const centre = middle + (slot + (span - 1) / 2 - 4) * m.pitch;
    return {
      left:
        place.placement === "left" ? m.sideX : stage.width - m.sideX - m.chipW,
      top: centre - height / 2,
      width: m.chipW,
      height,
    };
  }
  if (place.placement === "top" && place.side) {
    const centre = middle + (slot - 4) * m.pitch - m.cornerGap;
    // The corners of a Joy-Con are not drawn in towards the model, whose
    // shoulders reach as far as the columns' edge.
    const shift = wide ? 0 : m.cornerShift;
    return {
      left:
        place.side === "left"
          ? m.sideX + shift
          : stage.width - m.sideX - shift - m.chipW,
      top: centre - m.chipH / 2,
      width: m.chipW,
      height: m.chipH,
    };
  }
  if (place.placement === "top") {
    // A wide row is as wide as a column chip, and shares what the corners
    // leave between the chips of the row.
    const inset = wide?.corners ? m.sideX + m.chipW + m.topGap : m.edge;
    const pitch = wide?.topWide
      ? Math.min(
          m.chipW + m.topGap,
          (stage.width - 2 * inset) / Math.max(1, wide.tops),
        )
      : m.topW + m.topGap;
    return {
      left: stage.width / 2 + offset * pitch - (pitch - m.topGap) / 2,
      // The row of a Joy-Con sits just above the model.
      top: wide
        ? Math.max(
            8,
            Math.min(
              wide.topWide ? Number.POSITIVE_INFINITY : m.topY,
              middle -
                wide.halfHeight * modelUnit(stage, wide.width) -
                m.chipH -
                10,
            ),
          )
        : place.raised
          ? Math.max(8, m.topY - m.topRaise)
          : m.topY,
      width: pitch - m.topGap,
      height: m.chipH,
    };
  }
  const pitch = wide
    ? Math.min(
        m.chipW + m.topGap,
        (stage.width -
          2 * (wide.columns ? m.sideX + m.chipW + m.topGap : m.edge)) /
          Math.max(1, rows),
      )
    : Math.min(
        m.topW + m.topGap,
        (stage.width - 2 * (m.sideX + m.chipW + m.topGap)) / Math.max(5, rows),
      );
  const width = pitch - m.topGap;
  return {
    left: stage.width / 2 + offset * pitch - width / 2,
    top: Math.min(
      wide
        ? middle + wide.halfHeight * modelUnit(stage, wide.width) + 10
        : middle +
            Math.min(27.4 * (stage.height / 100), 19.6 * (stage.width / 100)) +
            6,
      stage.height - (notice ? 112 : 72) - m.chipH,
    ),
    width,
    height: m.chipH,
  };
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

/** A control's chip, its leader line, and the callout it belongs to. */
export interface Leader {
  control: string;
  rect: Rect;
  segment: Segment;
}

/**
 * The chips and leader lines of a layout on a stage: each leader runs from
 * the middle of the edge of its chip that faces the controller to the
 * projected control (what controller-scene.ts draws).
 */
export function layoutLeaders(
  callouts: readonly Callout[],
  stage: Stage,
  points: Readonly<Record<string, Point3>>,
  modelWidth: number,
  notice = false,
  /** The model's frame, for the wide rows of a Joy-Con. */
  frame?: Pick<ModelFrame, "halfHeight" | "topWide">,
): Leader[] {
  const rows = callouts.filter((c) => c.placement === "bottom").length;
  const wideRows = frame
    ? wideRowsOf(callouts, { width: modelWidth, ...frame })
    : undefined;
  const leaders: Leader[] = [];
  for (const callout of callouts) {
    const point = points[callout.control];
    if (!point) continue;
    const rect = chipRect(callout, stage, rows, notice, wideRows);
    leaders.push({
      control: callout.control,
      rect,
      segment: {
        from: calloutEdge(callout, rect),
        to: projectPoint(point, modelWidth, stage),
      },
    });
  }
  return leaders;
}

const cross = (o: Point, a: Point, b: Point) =>
  (a.x - o.x) * (b.y - o.y) - (a.y - o.y) * (b.x - o.x);
const within = (a: number, b: number, v: number) =>
  v >= Math.min(a, b) - 1e-9 && v <= Math.max(a, b) + 1e-9;

/** Whether two segments share a point; touching counts. */
export function segmentsIntersect(a: Segment, b: Segment): boolean {
  const d1 = cross(b.from, b.to, a.from);
  const d2 = cross(b.from, b.to, a.to);
  const d3 = cross(a.from, a.to, b.from);
  const d4 = cross(a.from, a.to, b.to);
  if (
    ((d1 > 0 && d2 < 0) || (d1 < 0 && d2 > 0)) &&
    ((d3 > 0 && d4 < 0) || (d3 < 0 && d4 > 0))
  )
    return true;
  const on = (p: Point, s: Segment, d: number) =>
    d === 0 && within(s.from.x, s.to.x, p.x) && within(s.from.y, s.to.y, p.y);
  return (
    on(a.from, b, d1) || on(a.to, b, d2) || on(b.from, a, d3) || on(b.to, a, d4)
  );
}

/** Whether a segment touches the inside of a box grown by `margin`. */
export function segmentHitsRect(s: Segment, r: Rect, margin = 0): boolean {
  const left = r.left - margin,
    right = r.left + r.width + margin,
    top = r.top - margin,
    bottom = r.top + r.height + margin;
  // Liang-Barsky: the part of the segment inside the box, if any.
  const dx = s.to.x - s.from.x,
    dy = s.to.y - s.from.y;
  let t0 = 0,
    t1 = 1;
  for (const [p, q] of [
    [-dx, s.from.x - left],
    [dx, right - s.from.x],
    [-dy, s.from.y - top],
    [dy, bottom - s.from.y],
  ]) {
    if (p === 0) {
      if (q < 0) return false;
    } else {
      const t = q / p;
      if (p < 0) {
        if (t > t1) return false;
        t0 = Math.max(t0, t);
      } else {
        if (t < t0) return false;
        t1 = Math.min(t1, t);
      }
    }
  }
  return t0 <= t1;
}

const overlap = (a: Rect, b: Rect) =>
  a.left < b.left + b.width &&
  b.left < a.left + a.width &&
  a.top < b.top + b.height &&
  b.top < a.top + a.height;

/** How near to another control's end a leader may pass, in pixels. */
export const anchorClearance = 1.5;

/** How far a point is from a segment. */
export function distanceToSegment(p: Point, s: Segment): number {
  const dx = s.to.x - s.from.x,
    dy = s.to.y - s.from.y;
  const length2 = dx * dx + dy * dy;
  const t =
    length2 === 0
      ? 0
      : Math.max(
          0,
          Math.min(
            1,
            ((p.x - s.from.x) * dx + (p.y - s.from.y) * dy) / length2,
          ),
        );
  return Math.hypot(p.x - (s.from.x + t * dx), p.y - (s.from.y + t * dy));
}

/** The convex hull of some points, counter-clockwise (Andrew's monotone chain). */
export function convexHull<T extends Point>(points: readonly T[]): T[] {
  const sorted = [...points].sort((a, b) => a.x - b.x || a.y - b.y);
  if (sorted.length < 3) return sorted;
  const turn = (o: Point, a: Point, b: Point) => cross(o, a, b);
  const half = (list: readonly T[]) => {
    const out: T[] = [];
    for (const p of list) {
      while (
        out.length >= 2 &&
        turn(out[out.length - 2], out[out.length - 1], p) <= 0
      )
        out.pop();
      out.push(p);
    }
    out.pop();
    return out;
  };
  return [...half(sorted), ...half([...sorted].reverse())];
}

/**
 * The silhouette of the model on a stage: the convex hull of its outline
 * points (points of its shell, see outlineOf in controller-asset.ts), as the
 * scene's camera projects them.
 */
export function projectedHull(
  outline: readonly Point3[],
  modelWidth: number,
  stage: Stage,
): Point[] {
  return convexHull(outline.map((p) => projectPoint(p, modelWidth, stage)));
}

/** Whether a box touches the inside of a convex polygon (counter-clockwise), grown by `margin`. */
export function rectHitsPolygon(
  r: Rect,
  polygon: readonly Point[],
  margin = 0,
): boolean {
  if (polygon.length < 3) return false;
  const left = r.left - margin,
    right = r.left + r.width + margin,
    top = r.top - margin,
    bottom = r.top + r.height + margin;
  // Separating axes: the box's two, and the normal of each edge.
  let minX = Number.POSITIVE_INFINITY,
    maxX = Number.NEGATIVE_INFINITY,
    minY = Number.POSITIVE_INFINITY,
    maxY = Number.NEGATIVE_INFINITY;
  for (const p of polygon) {
    minX = Math.min(minX, p.x);
    maxX = Math.max(maxX, p.x);
    minY = Math.min(minY, p.y);
    maxY = Math.max(maxY, p.y);
  }
  if (maxX <= left || minX >= right || maxY <= top || minY >= bottom)
    return false;
  const corners = [
    { x: left, y: top },
    { x: right, y: top },
    { x: right, y: bottom },
    { x: left, y: bottom },
  ];
  for (const [i, a] of polygon.entries()) {
    const b = polygon[(i + 1) % polygon.length];
    // Outside this edge (to its right, for a counter-clockwise polygon) are
    // all four corners: the edge separates.
    if (corners.every((c) => cross(a, b, c) <= 0)) return false;
  }
  return true;
}

export interface LayoutProblem {
  /**
   * `cross`: two leaders meet; `through`: a leader passes through another
   * control's chip; `touch`: a leader passes within anchorClearance of
   * another control's end, which would look like it goes there too;
   * `overlap`: two chips cover each other; `outside`: a chip is not wholly
   * on the stage; `model`: a chip covers the model's silhouette.
   */
  kind: "cross" | "through" | "touch" | "overlap" | "outside" | "model";
  controls: string[];
}

/** What is wrong with a layout on a stage; nothing when it is clean. */
export function layoutProblems(
  leaders: readonly Leader[],
  stage: Stage,
  clearance = anchorClearance,
  /** The model's silhouette on the stage (projectedHull), if chips must keep off it. */
  hull?: readonly Point[],
): LayoutProblem[] {
  const problems: LayoutProblem[] = [];
  leaders.forEach((a, index) => {
    const r = a.rect;
    if (hull && rectHitsPolygon(r, hull, 1))
      problems.push({ kind: "model", controls: [a.control] });
    if (
      r.left < 0 ||
      r.top < 0 ||
      r.left + r.width > stage.width ||
      r.top + r.height > stage.height
    )
      problems.push({ kind: "outside", controls: [a.control] });
    for (const b of leaders.slice(index + 1)) {
      if (segmentsIntersect(a.segment, b.segment))
        problems.push({ kind: "cross", controls: [a.control, b.control] });
      if (overlap(a.rect, b.rect))
        problems.push({ kind: "overlap", controls: [a.control, b.control] });
    }
    for (const b of leaders) {
      if (a === b) continue;
      if (segmentHitsRect(a.segment, b.rect, 1))
        problems.push({ kind: "through", controls: [a.control, b.control] });
      if (
        clearance > 0 &&
        distanceToSegment(b.segment.to, a.segment) < clearance
      )
        problems.push({ kind: "touch", controls: [a.control, b.control] });
    }
  });
  return problems;
}

/** The length of a leader line, in pixels. */
export const leaderLength = ({ segment: { from, to } }: Leader) =>
  Math.hypot(to.x - from.x, to.y - from.y);
