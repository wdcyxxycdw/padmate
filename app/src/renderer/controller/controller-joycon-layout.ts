// Where the chips of a Joy-Con's controls go on the stage, and which way
// their leader lines run so that none crosses another or passes through a
// chip.
//
// A Joy-Con is shown in five ways (a single one held sideways or upright, left
// or right, and the pair), and a control sits somewhere else on each. The
// chips go in six regions around the model: the two shoulder corners above the
// columns, the row above, the two columns, and the row below. What belongs
// where is a table of units (controls that stay together, e.g. the four face
// buttons) with the regions each may go to, the best first:
//
// - the shoulder buttons (L and ZL, or R and ZR, even when held sideways) are
//   at the top, in the corner of their own hand;
// - the face buttons go below the model when it is held sideways, and in the
//   columns when it is upright;
// - SL and SR are where the rail is: the row above when sideways, the column
//   on the rail's side when upright; the pair shows neither (they cannot be
//   pressed with the Joy-Cons joined, see diagramHidden).
//
// Without the model the chips take the order of `nominal` (`staticLayout`).
// With the model's anchors, which are in the Joy-Con's own frame, the chips of
// each region start in the order of their anchors, and the solver then looks
// for the order (and, if that is not enough, the place of a column or a row,
// and the region of a unit that has another) in which no two leaders meet and
// none passes through a chip. The chips keep their sizes while the model
// scales with the stage, so it judges a layout on a few stage sizes, and on
// more where that is not clean (supportedStages).
import type { Callout } from "./controller-callouts";
import {
  defaultFrame,
  type Leader,
  layoutLeaders,
  layoutProblems,
  leaderLength,
  type Point,
  type Point3,
  projectedHull,
  rectHitsPolygon,
  type Stage,
  supportedStages,
} from "./controller-layout";
import { isSidewaysJoyCon } from "./controller-model";

export type JoyConKind =
  | "pair"
  | "left-sideways"
  | "right-sideways"
  | "left-upright"
  | "right-upright";

/** How a pad is shown, if it is a Joy-Con. */
export function joyConKind(info: {
  type: string;
  buttonMask?: number;
}): JoyConKind | undefined {
  if (info.type === "joyconpair") return "pair";
  if (info.type !== "joyconleft" && info.type !== "joyconright")
    return undefined;
  const side = info.type === "joyconleft" ? "left" : "right";
  return `${side}-${isSidewaysJoyCon(info.type, info.buttonMask) ? "sideways" : "upright"}`;
}

/**
 * The controls of a pair of Joy-Cons that are left off its picture: SL and SR
 * of both halves, which cannot be pressed while the two are joined (they are
 * on the rails that slide into the grip). Only the picture leaves them out:
 * the daemon still reads them and a profile's bindings on them are kept.
 * Those are paddle1 to paddle4, ARCHITECTURE section 7.1.
 */
export function diagramHidden(type: string, control: string): boolean {
  return type === "joyconpair" && /^paddle[1-4]$/.test(control);
}

type Region = "cornerL" | "cornerR" | "top" | "leftCol" | "rightCol" | "bottom";
const regions: readonly Region[] = [
  "cornerL",
  "top",
  "cornerR",
  "leftCol",
  "rightCol",
  "bottom",
];

/** How many slots or chips each region takes (a stick is two slots). */
const capacity: Record<Region, number> = {
  cornerL: 2,
  cornerR: 2,
  top: 5,
  leftCol: 7,
  rightCol: 7,
  bottom: 6,
};

/** Controls that stay together, and the regions they may be in, best first. */
interface Unit {
  controls: string[];
  homes: Region[];
}
const unit = (controls: string[], ...homes: Region[]): Unit => ({
  controls,
  homes,
});

// A sideways Joy-Con's four buttons, as the picture has them: left, up, down,
// right.
const sidewaysFace = ["west", "north", "south", "east"];

// Per kind, which regions each unit may be in.
const plans: Record<JoyConKind, Unit[]> = {
  pair: [
    unit(["lefttrigger", "leftshoulder"], "cornerL"),
    unit(["righttrigger", "rightshoulder"], "cornerR"),
    unit(["back", "start"], "top"),
    unit(["leftstick", "dpad.up", "dpad.left", "dpad.down"], "leftCol"),
    // The button nearest the middle is behind the others for a leader from
    // the column; it may be reached from above or below.
    unit(["dpad.right"], "leftCol", "top", "bottom"),
    unit(["rightstick", "north", "east", "south"], "rightCol"),
    unit(["west"], "rightCol", "top", "bottom"),
    // Below the model, where it has room; else in the column of its hand.
    unit(["misc1"], "bottom", "leftCol"),
    unit(["misc2"], "bottom", "rightCol"),
  ],
  "left-upright": [
    unit(["lefttrigger", "leftshoulder"], "cornerL"),
    unit(["back"], "cornerR", "top"),
    unit(
      ["leftstick", "dpad.up", "dpad.left", "dpad.right", "dpad.down"],
      "leftCol",
    ),
    unit(["paddle2", "paddle4"], "rightCol", "bottom"),
    unit(["misc1"], "bottom", "rightCol"),
  ],
  "right-upright": [
    unit(["righttrigger", "rightshoulder"], "cornerR"),
    unit(["start"], "cornerL", "top"),
    unit(["north", "west", "east", "south", "rightstick"], "rightCol"),
    unit(["paddle1", "paddle3"], "leftCol", "bottom"),
    unit(["misc2"], "bottom", "leftCol"),
  ],
  "left-sideways": [
    unit(["paddle4", "paddle2"], "cornerL", "top"),
    unit(["start"], "top", "cornerR"),
    unit(["leftshoulder", "rightshoulder"], "top"),
    unit(["leftstick"], "leftCol"),
    unit(sidewaysFace, "bottom", "rightCol"),
  ],
  "right-sideways": [
    unit(["paddle3", "paddle1"], "cornerR", "top"),
    unit(["leftshoulder", "rightshoulder"], "top"),
    unit(["start"], "top", "cornerL"),
    unit(["misc2"], "leftCol", "top", "bottom"),
    unit(["leftstick"], "leftCol"),
    unit(sidewaysFace, "bottom", "leftCol"),
  ],
};

/**
 * The order the chips of a region take when nothing but the table is known:
 * the order of the controls here, from the first of a region to the last
 * (down a column, left to right along a row).
 */
const nominal: Record<JoyConKind, string[]> = {
  pair: [
    "lefttrigger",
    "leftshoulder",
    "righttrigger",
    "rightshoulder",
    "back",
    "start",
    "leftstick",
    "dpad.up",
    "dpad.left",
    "dpad.right",
    "dpad.down",
    "rightstick",
    "north",
    "west",
    "east",
    "south",
    "misc1",
    "misc2",
  ],
  "left-upright": [
    "lefttrigger",
    "leftshoulder",
    "back",
    "leftstick",
    "dpad.up",
    "dpad.left",
    "dpad.right",
    "dpad.down",
    "paddle2",
    "paddle4",
    "misc1",
  ],
  "right-upright": [
    "righttrigger",
    "rightshoulder",
    "start",
    "north",
    "west",
    "east",
    "south",
    "rightstick",
    "paddle1",
    "paddle3",
    "misc2",
  ],
  "left-sideways": [
    "paddle4",
    "paddle2",
    "start",
    "leftshoulder",
    "rightshoulder",
    "leftstick",
    ...sidewaysFace,
  ],
  "right-sideways": [
    "paddle3",
    "paddle1",
    "leftshoulder",
    "rightshoulder",
    "start",
    "misc2",
    "leftstick",
    ...sidewaysFace,
  ],
};

/** The chips of each region, in order. */
type Order = Record<Region, string[]>;
const emptyOrder = (): Order => ({
  cornerL: [],
  top: [],
  cornerR: [],
  leftCol: [],
  rightCol: [],
  bottom: [],
});

/** The regions whose chips may sit off the middle. */
type Shifted = "leftCol" | "rightCol" | "top" | "bottom";
const shifted: readonly Shifted[] = ["leftCol", "rightCol", "top", "bottom"];

/**
 * A layout: the chips of each region in order, and how far a column sits
 * below the middle of slots 2 to 7 (in slots) or a row sits right of the
 * middle (in chips): they are centred, but may have to move to keep their
 * leaders off other leaders.
 */
interface State {
  order: Order;
  shift: Record<Shifted, number>;
}
const stateOf = (order: Order): State => ({
  order,
  shift: { leftCol: 0, rightCol: 0, top: 0, bottom: 0 },
});

const isStick = (control: string) => control.endsWith("stick");
const heightOf = (controls: readonly string[]) =>
  controls.reduce((sum, c) => sum + (isStick(c) ? 2 : 1), 0);
const fits = (order: Order) =>
  regions.every((r) => heightOf(order[r]) <= capacity[r]);

/**
 * The first slot of a column of this height, shifted. Slots are not whole
 * numbers once shifted: a chip may sit between two (the stylesheet puts it
 * where its number says); one that goes off the stage or onto another chip is
 * a problem the solver sees.
 */
const firstSlot = (height: number, shift: number) =>
  Math.max(2, 2 + Math.floor((6 - height) / 2)) + shift;

/** The callouts with the places a layout gives them. */
function place(callouts: readonly Callout[], state: State): Callout[] {
  const { order } = state;
  const byControl = new Map(callouts.map((c) => [c.control, c]));
  const out: Callout[] = [];
  const put = (control: string, position: Partial<Callout>) => {
    const callout = byControl.get(control);
    if (!callout) return;
    // The position is the layout's, whatever the callout had before.
    const placed: Callout = { ...callout, ...position };
    for (const key of ["span", "slot", "side", "offset", "raised"] as const)
      if (!(key in position)) delete placed[key];
    out.push(placed);
  };
  for (const region of regions) {
    const controls = order[region];
    if (region === "cornerL" || region === "cornerR") {
      for (const [index, control] of controls.entries())
        put(control, {
          placement: "top",
          side: region === "cornerL" ? "left" : "right",
          slot: index + 2 - controls.length,
        });
    } else if (region === "leftCol" || region === "rightCol") {
      // A short column sits about the middle of slots 2 to 7.
      let slot = firstSlot(heightOf(controls), state.shift[region]);
      for (const control of controls) {
        put(control, {
          placement: region === "leftCol" ? "left" : "right",
          slot,
          ...(isStick(control) ? { span: 2 } : {}),
        });
        slot += isStick(control) ? 2 : 1;
      }
    } else {
      for (const [index, control] of controls.entries())
        put(control, {
          placement: region,
          offset: index - (controls.length - 1) / 2 + state.shift[region],
        });
    }
  }
  return out;
}

/** The units of a plan that have a chip, with only the controls that have. */
function unitsFor(kind: JoyConKind, present: ReadonlySet<string>): Unit[] {
  return plans[kind]
    .map((u) => ({ ...u, controls: u.controls.filter((c) => present.has(c)) }))
    .filter((u) => u.controls.length > 0);
}

/** The chips of each region when every unit is in the home `homes` says. */
function nominalOrder(
  kind: JoyConKind,
  units: readonly Unit[],
  homes: readonly number[],
): Order {
  const order = emptyOrder();
  for (const [index, u] of units.entries())
    order[u.homes[homes[index]]].push(...u.controls);
  const rank = (control: string) => nominal[kind].indexOf(control);
  for (const region of regions) order[region].sort((a, b) => rank(a) - rank(b));
  return order;
}

/**
 * Whether the table has a place for every callout. A Joy-Con whose controls
 * SDL has not reported yet has the standard ones, which the table does not
 * know; its chips keep the places the standard layout gave them.
 */
export function covers(callouts: readonly Callout[], kind: JoyConKind) {
  const known = new Set(plans[kind].flatMap((u) => u.controls));
  return callouts.every((c) => known.has(c.control));
}

/** Without the model: every unit in its first region, in nominal order. */
export function staticLayout(
  callouts: readonly Callout[],
  kind: JoyConKind,
): Callout[] {
  if (!covers(callouts, kind)) return [...callouts];
  const units = unitsFor(kind, new Set(callouts.map((c) => c.control)));
  const order = nominalOrder(
    kind,
    units,
    units.map(() => 0),
  );
  return place(callouts, stateOf(order));
}

/** What the solver needs to know of the model. */
export interface Anchors {
  /** The width of the model in its frame, which the camera fits. */
  width: number;
  /** Where each control's leader ends, in the model's frame. */
  points: Readonly<Record<string, Point3>>;
  /**
   * Points that hold the model's silhouette (shellOutline), which no chip may
   * cover. Without them chips only keep clear of each other and of leaders.
   */
  outline?: readonly Point3[];
  /** How far the model reaches up and down from the middle (halfHeightOf). */
  halfHeight?: number;
  /** The row above is not wide (a pair's, see controller-3d.css). */
  narrowTop?: boolean;
}

/** The silhouettes of a model on the stage sizes it has been asked for. */
const hulls = new WeakMap<Anchors, Map<string, Point[]>>();
function hullOn(anchors: Anchors, stage: Stage): Point[] | undefined {
  if (!anchors.outline) return undefined;
  const bySize = hulls.get(anchors) ?? new Map<string, Point[]>();
  hulls.set(anchors, bySize);
  const key = `${stage.width}x${stage.height}`;
  const hull =
    bySize.get(key) ?? projectedHull(anchors.outline, anchors.width, stage);
  bySize.set(key, hull);
  return hull;
}

/** The problems of a layout of leaders on a stage, the model's silhouette among them. */
const problemsOf = (
  leaders: readonly Leader[],
  anchors: Anchors,
  stage: Stage,
) => layoutProblems(leaders, stage, undefined, hullOn(anchors, stage));

/**
 * The stage sizes the solver starts with. A layout that is clean on one size
 * may cross on another; the solver adds the sizes of supportedStages where
 * its layout is not clean to these, and solves again.
 */
const startingStages: readonly Stage[] = [440, 600, 860, 1180].flatMap(
  (width) => [440, 600, 800].map((height) => ({ width, height })),
);

/**
 * What a layout is worth: the problems on the stages first, then how far its
 * rows and columns are from the middle, then how many pairs of chips are in
 * the other order than their parts (a row or column that reads against the
 * model is a last resort), then the length of its leaders.
 */
interface Score {
  problems: number;
  moved: number;
  inversions: number;
  length: number;
}
const better = (a: Score, b: Score) =>
  a.problems !== b.problems
    ? a.problems < b.problems
    : a.moved !== b.moved
      ? a.moved < b.moved
      : a.inversions !== b.inversions
        ? a.inversions < b.inversions
        : a.length < b.length;

/** The pairs of chips in a region that are in the other order than their parts. */
function inversionsOf(order: Order, anchors: Anchors): number {
  let count = 0;
  for (const region of regions) {
    const list = order[region];
    for (let i = 0; i < list.length; i++)
      for (let j = i + 1; j < list.length; j++) {
        const a = anchors.points[list[i]],
          b = anchors.points[list[j]];
        if (a && b && keyOf(region, a) - keyOf(region, b) > 0.05) count++;
      }
  }
  return count;
}

/** The orders of a list (it is short: a region holds six at most). */
function* permutations<T>(items: readonly T[]): Generator<T[]> {
  if (items.length <= 1) {
    yield [...items];
    return;
  }
  for (let index = 0; index < items.length; index++) {
    const rest = [...items.slice(0, index), ...items.slice(index + 1)];
    for (const tail of permutations(rest)) yield [items[index], ...tail];
  }
}

/**
 * The orders to try for the chips of a region: every order of a short list,
 * and for a long one (a column of six has 720) the lists with two chips
 * swapped, which is all a hill-climb needs.
 */
function* reorderings<T>(items: readonly T[]): Generator<T[]> {
  if (items.length <= 5) {
    yield* permutations(items);
    return;
  }
  for (let a = 0; a < items.length; a++)
    for (let b = a + 1; b < items.length; b++) {
      const swapped = [...items];
      [swapped[a], swapped[b]] = [swapped[b], swapped[a]];
      yield swapped;
    }
}

/**
 * The leaders of a placed layout on a stage; with a row below there are two,
 * for the editor notice lifts that row when it shows.
 */
function leadersOn(
  placed: readonly Callout[],
  anchors: Anchors,
  stage: Stage,
): Leader[][] {
  const notices = placed.some((c) => c.placement === "bottom")
    ? [false, true]
    : [false];
  return notices.map((notice) =>
    layoutLeaders(placed, stage, anchors.points, anchors.width, notice, {
      halfHeight: anchors.halfHeight ?? defaultFrame.halfHeight,
      topWide: !anchors.narrowTop,
    }),
  );
}

/** How many problems a placed layout has on a stage. */
function problemsOn(
  placed: readonly Callout[],
  anchors: Anchors,
  stage: Stage,
) {
  return leadersOn(placed, anchors, stage).reduce(
    (sum, leaders) => sum + problemsOf(leaders, anchors, stage).length,
    0,
  );
}

/**
 * The score of a layout on the stages. It stops as soon as the problems pass
 * those of `limit`, which is all a worse layout needs to be known by.
 */
function judge(
  callouts: readonly Callout[],
  state: State,
  anchors: Anchors,
  stages: readonly Stage[],
  limit?: Score,
): Score {
  const placed = place(callouts, state);
  const moved =
    Math.abs(state.shift.leftCol) +
    Math.abs(state.shift.rightCol) +
    2 * (Math.abs(state.shift.top) + Math.abs(state.shift.bottom));
  let problems = 0,
    length = 0,
    count = 0;
  for (const stage of stages)
    for (const leaders of leadersOn(placed, anchors, stage)) {
      problems += problemsOf(leaders, anchors, stage).length;
      if (limit && problems > limit.problems)
        return {
          problems,
          moved,
          inversions: 0,
          length: Number.POSITIVE_INFINITY,
        };
      length += leaders.reduce((sum, l) => sum + leaderLength(l), 0);
      count++;
    }
  return {
    problems,
    moved,
    inversions: inversionsOf(state.order, anchors),
    length: length / count,
  };
}

/** The places a column (in slots) or a row (in chips) may be moved to. */
const moves = (region: Shifted) =>
  region === "leftCol" || region === "rightCol"
    ? [-0.5, 0.5, -1, 1, -1.5, 1.5, -2, 2, -3, 3]
    : [-0.5, 0.5, -1, 1, -1.5, 1.5, -2, 2, -2.5, 2.5];

/**
 * The best order of the chips of each region, one region at a time; where
 * that leaves a problem, the best place of the region too (a region is moved
 * off the middle only to clear a problem).
 */
function improve(
  callouts: readonly Callout[],
  start: State,
  anchors: Anchors,
  stages: readonly Stage[],
): { state: State; score: Score } {
  let state = start,
    score = judge(callouts, start, anchors, stages);
  const consider = (candidate: State) => {
    const next = judge(callouts, candidate, anchors, stages, score);
    if (!better(next, score)) return false;
    state = candidate;
    score = next;
    return true;
  };
  for (let round = 0; round < 4; round++) {
    let changed = false;
    for (const region of regions) {
      const base = state;
      for (const order of reorderings(base.order[region]))
        if (consider({ ...base, order: { ...base.order, [region]: order } }))
          changed = true;
      // A problem is left: the region may have to move, with another order.
      if (score.problems > 0 && shifted.includes(region as Shifted))
        for (const order of state.order[region].length <= 4
          ? permutations(state.order[region])
          : [state.order[region]])
          for (const shift of moves(region as Shifted)) {
            const current = state;
            if (
              consider({
                order: { ...current.order, [region]: order },
                shift: { ...current.shift, [region]: shift },
              })
            )
              changed = true;
          }
    }
    if (!changed) break;
  }
  return { state, score };
}

/** Every choice of a home for each unit, the closest to the first first. */
function* choices(units: readonly Unit[]): Generator<number[]> {
  const all: number[][] = [];
  const walk = (index: number, chosen: number[]) => {
    if (index === units.length) {
      all.push(chosen);
      return;
    }
    for (let home = 0; home < units[index].homes.length; home++)
      walk(index + 1, [...chosen, home]);
  };
  walk(0, []);
  const cost = (c: number[]) => c.reduce((sum, home) => sum + home, 0);
  yield* all.sort((a, b) => cost(a) - cost(b));
}

/** The sort key of a chip in a region: where its anchor is on the stage. */
function keyOf(region: Region, point: Point3): number {
  // Columns and corners go down the page, rows go across.
  return region === "top" || region === "bottom" ? point.x : -point.y;
}

/**
 * Whether a chip of the rows or corners covers the model on one of the stages
 * however the chips are ordered: their places depend on how many there are,
 * not on which is which, so the search is not worth making.
 */
function coversModel(
  callouts: readonly Callout[],
  state: State,
  anchors: Anchors,
  stages: readonly Stage[],
): boolean {
  const placed = place(callouts, state);
  const fixed = new Set(
    placed
      .filter((c) => c.placement === "top" || c.placement === "bottom")
      .map((c) => c.control),
  );
  for (const stage of stages) {
    const hull = hullOn(anchors, stage);
    if (!hull) return false;
    for (const leaders of leadersOn(placed, anchors, stage))
      for (const leader of leaders)
        if (fixed.has(leader.control) && rectHitsPolygon(leader.rect, hull, 1))
          return true;
  }
  return false;
}

/** The best layout for some stage sizes, or none if the table has no room. */
function solve(
  callouts: readonly Callout[],
  kind: JoyConKind,
  units: readonly Unit[],
  anchors: Anchors,
  stages: readonly Stage[],
) {
  let best: { state: State; score: Score } | undefined;
  let tried = 0;
  for (const homes of choices(units)) {
    if (tried++ >= 80) break;
    const order = nominalOrder(kind, units, homes);
    if (!fits(order)) continue;
    if (coversModel(callouts, stateOf(order), anchors, stages)) continue;
    // The first guess: the chips of a region in the order of their anchors.
    for (const region of regions)
      order[region].sort(
        (a, b) =>
          keyOf(region, anchors.points[a]) - keyOf(region, anchors.points[b]),
      );
    const result = improve(callouts, stateOf(order), anchors, stages);
    if (!best || better(result.score, best.score)) best = result;
    if (best.score.problems === 0) break;
  }
  return best;
}

/**
 * The layouts found, by what they were found for: the search is the work of a
 * few hundred milliseconds for the Joy-Con 2 pair, and the same model is built
 * again whenever its stage is (another pad, a lost context).
 */
const found = new Map<string, State | undefined>();
const keyOfAnchors = (kind: JoyConKind, anchors: Anchors) =>
  `${kind}|${anchors.width.toFixed(4)}|${Object.entries(anchors.points)
    .sort(([a], [b]) => (a < b ? -1 : 1))
    .map(
      ([c, p]) => `${c}:${p.x.toFixed(4)},${p.y.toFixed(4)},${p.z.toFixed(4)}`,
    )
    .join(";")}`;

/**
 * The Joy-Con's callouts placed for the model's anchors: no two leaders cross
 * or pass through a chip, as far as the regions of the table allow, on every
 * stage of supportedStages. When no choice is clean it is the one with the
 * fewest problems.
 */
export function arrangeJoyCon(
  callouts: readonly Callout[],
  kind: JoyConKind,
  anchors: Anchors,
): Callout[] {
  if (!covers(callouts, kind)) return [...callouts];
  const units = unitsFor(
    kind,
    new Set(
      callouts.filter((c) => anchors.points[c.control]).map((c) => c.control),
    ),
  );
  const key = `${keyOfAnchors(kind, anchors)}|${units.map((u) => u.controls.join()).join(";")}`;
  if (found.has(key)) {
    const state = found.get(key);
    return state ? place(callouts, state) : staticLayout(callouts, kind);
  }
  let stages = [...startingStages];
  let best: ReturnType<typeof solve>;
  // Each round either ends clean on every supported stage or learns of more
  // stages to take into account; a size is added at most once.
  for (let round = 0; round < 12; round++) {
    best = solve(callouts, kind, units, anchors, stages);
    if (!best || best.score.problems > 0) break;
    const placed = place(callouts, best.state);
    const failing = supportedStages.filter(
      (stage) =>
        !stages.includes(stage) && problemsOn(placed, anchors, stage) > 0,
    );
    if (failing.length === 0) break;
    // The newest first: the hardest sizes end a bad candidate soonest.
    stages = [...failing.slice(0, 6), ...stages];
  }
  found.set(key, best?.state);
  return best ? place(callouts, best.state) : staticLayout(callouts, kind);
}
