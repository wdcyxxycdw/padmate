// Where the chips of a Joy-Con's controls go on the stage, and how their
// leader lines bend, so that none crosses another or passes through a chip.
//
// A Joy-Con is shown in three ways: a single left or right one held upright
// (the sideways view is gone), and the pair. Each has a plan: the chips of
// each region of the stage in the order they take from the top (or left), with
// the room between some of them, and how the leader of a chip is bent (Route,
// controller-layout.ts). The regions are the two shoulder corners above the
// columns, the row above the model (the pair's minus and plus) and the two
// columns.
//
// - The pair has L and ZL, R and ZR in the corners, the left Joy-Con's stick,
//   D-pad and Capture in the left column and the right one's four buttons,
//   stick and C in the right column. Its SL and SR cannot be pressed with the
//   two joined and are left off (diagramHidden).
// - A single Joy-Con has two columns. The outer one has its shoulder button
//   (L or R) and, for a left one, the D-pad and Capture, or, for a right one,
//   the four buttons and C. The one on the rail's side has the trigger (ZL or
//   ZR), minus or plus, SL, the stick and SR, so that the chips of the rail's
//   two buttons are by the rail.
//
// Without the model the chips take the places the plan gives with the room it
// names (`staticLayout`). With the model's anchors the solver looks for the
// room between the chips of a column and the place of the column (in slots) at
// which no two leaders meet and none passes through a chip, for each density
// of the chips (their sizes differ, the model scales with the stage): the
// design's own places for the full one where they are clean, and for the
// others the shortest leaders. It judges a layout on a few stage sizes of the
// density, and on more where that is not clean (supportedStages). The chips of
// the model's top buttons hang from the model's top (modelTop), so they keep
// to them as the stage grows; the others are centred on the stage's middle,
// where the D-pad and the four buttons are.
import type { Callout } from "./controller-callouts";
import {
  cornerTopSlot,
  type Density,
  defaultFrame,
  type Leader,
  layoutLeaders,
  layoutProblems,
  leaderLength,
  type Point,
  type Point3,
  projectedHull,
  type Route,
  type Stage,
  stageDensity,
  supportedStages,
} from "./controller-layout";

export type JoyConKind = "pair" | "left-upright" | "right-upright";

/**
 * How a pad is shown, if it is a Joy-Con: a single one is always held upright,
 * whatever grip SDL reports (the daemon asks for the vertical one).
 */
export function joyConKind(info: { type: string }): JoyConKind | undefined {
  if (info.type === "joyconpair") return "pair";
  if (info.type === "joyconleft") return "left-upright";
  if (info.type === "joyconright") return "right-upright";
  return undefined;
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

type Region = "cornerL" | "cornerR" | "top" | "leftCol" | "rightCol";
// In the order of the keyboard: the band above from left to right, then the
// columns.
const regions: readonly Region[] = [
  "cornerL",
  "top",
  "cornerR",
  "leftCol",
  "rightCol",
];
type Column = "leftCol" | "rightCol";
const columns: readonly Column[] = ["leftCol", "rightCol"];

/**
 * Room between two chips of a column, in slots, that the solver may choose
 * from (the first is where it starts and what a layout without the model has).
 */
interface Room {
  room: readonly number[];
}
const room = (...slots: number[]): Room => ({ room: slots });
type Entry = string | Room;

interface Plan {
  /** The chips of each region, in order, with the room between them. */
  regions: Record<Region, readonly Entry[]>;
  /**
   * A column that hangs from the model's top (modelTop, controller-layout.ts)
   * has the slot of its first chip here, counted from it; the others have
   * `start`, counted from the stage's middle (slot 4). The chips of the
   * model's top buttons (ZL and minus, and the corners) hang, so that the
   * stage's size does not change which of their leaders reaches the model
   * first; the D-pad and the four buttons are about the model's middle.
   */
  hang: Partial<Record<Column, number>>;
  start: Partial<Record<Column, number>>;
  /**
   * How far (in slots) a column starts below where it is centred or hung,
   * which is what a layout without the model has: the pair's left column
   * clears the corners above it on the smallest stage, where the default
   * model frame is taller than the model.
   */
  shift: Partial<Record<Column, number>>;
  /** How a leader is bent where it is not `auto`. */
  routes: Readonly<Record<string, Route>>;
  /**
   * Controls whose parts are side by side (a shoulder button and its
   * trigger), by the one whose leader is the one that passes the other.
   */
  twins: Readonly<Record<string, string>>;
}

// The room a layout may leave between two chips of a column. A chip is a slot
// (a stick two), and the picture's own buttons are not on the slots' grid, so
// the room moves a chip towards its part.
const fine = (first: number) =>
  room(first, ...[0, 0.25, 0.5, 0.75, 1, 1.5].filter((r) => r !== first));

const plans: Record<JoyConKind, Plan> = {
  pair: {
    regions: {
      cornerL: ["leftshoulder", "lefttrigger"],
      cornerR: ["rightshoulder", "righttrigger"],
      top: ["back", "start"],
      leftCol: [
        "leftstick",
        "dpad.up",
        "dpad.left",
        "dpad.right",
        "dpad.down",
        "misc1",
      ],
      rightCol: ["north", "east", "west", "south", "rightstick", "misc2"],
    },
    // L and ZL, R and ZR are 4 pixels apart on the model: the one of each
    // bends near its part, so that the two lines do not run side by side.
    // The button of each diamond nearest the middle is reached through the
    // gap between two of its neighbours.
    routes: {
      leftshoulder: "steep",
      rightshoulder: "steep",
      "dpad.right": { gap: ["dpad.left", "dpad.down"] },
      west: { gap: ["east", "south"] },
    },
    hang: { rightCol: 0.38 },
    start: { leftCol: 1 },
    shift: { leftCol: 0.75 },
    twins: { lefttrigger: "leftshoulder", righttrigger: "rightshoulder" },
  },
  "left-upright": {
    regions: {
      cornerL: [],
      cornerR: [],
      top: [],
      leftCol: [
        "leftshoulder",
        room(1, 0.5, 1.5, 0, 2),
        "dpad.up",
        "dpad.left",
        "dpad.right",
        "dpad.down",
        "misc1",
      ],
      rightCol: [
        "lefttrigger",
        "back",
        fine(0),
        "paddle2",
        fine(0),
        "leftstick",
        fine(0.5),
        "paddle4",
      ],
    },
    routes: { "dpad.right": { gap: ["dpad.left", "dpad.down"] } },
    hang: { rightCol: -0.62 },
    start: { leftCol: 0.5 },
    shift: {},
    twins: { lefttrigger: "leftshoulder" },
  },
  "right-upright": {
    regions: {
      cornerL: [],
      cornerR: [],
      top: [],
      leftCol: [
        "righttrigger",
        "start",
        fine(0),
        "paddle1",
        fine(0),
        "rightstick",
        fine(0.5),
        "paddle3",
      ],
      rightCol: [
        "rightshoulder",
        fine(0),
        "north",
        "east",
        "west",
        "south",
        "misc2",
      ],
    },
    routes: { west: { gap: ["east", "south"] } },
    hang: { leftCol: -0.62 },
    start: { rightCol: 0.5 },
    shift: {},
    twins: { righttrigger: "rightshoulder" },
  },
};

const isControl = (entry: Entry): entry is string => typeof entry === "string";
const controlsOf = (plan: Plan) =>
  regions.flatMap((r) => plan.regions[r].filter(isControl));

/**
 * The room chosen for each of a column's gaps, and how far (in slots) the
 * column sits below where it is (centred, or hung from the model's top).
 */
interface State {
  room: Record<Column, number[]>;
  shift: Record<Column, number>;
}

const isStick = (control: string) => control.endsWith("stick");
const heightOf = (control: string) => (isStick(control) ? 2 : 1);

/** The entries of a region that have a chip, and the room between them. */
function entriesOf(
  plan: Plan,
  region: Region,
  present: ReadonlySet<string>,
): Entry[] {
  const list = plan.regions[region].filter(
    (entry) => !isControl(entry) || present.has(entry),
  );
  // Room needs a chip on each side.
  return list.filter((entry, index) => {
    if (isControl(entry)) return true;
    return (
      list.slice(0, index).some(isControl) &&
      list.slice(index + 1).some(isControl)
    );
  });
}

/** The room (an index into the region's gaps, as the plan has them) of each. */
function roomIndexes(plan: Plan, region: Region): Map<Room, number> {
  const map = new Map<Room, number>();
  for (const entry of plan.regions[region])
    if (!isControl(entry)) map.set(entry, map.size);
  return map;
}

const defaultState = (plan: Plan): State => {
  const room: Record<Column, number[]> = { leftCol: [], rightCol: [] };
  for (const column of columns)
    for (const entry of plan.regions[column])
      if (!isControl(entry)) room[column].push(entry.room[0]);
  return {
    room,
    shift: {
      leftCol: plan.shift.leftCol ?? 0,
      rightCol: plan.shift.rightCol ?? 0,
    },
  };
};

/** Where each chip of a column is relative to the first, in slots, and the column's height. */
function columnSlots(
  plan: Plan,
  column: Column,
  present: ReadonlySet<string>,
  chosen: readonly number[],
) {
  const indexes = roomIndexes(plan, column);
  const slots: [control: string, slot: number][] = [];
  let cursor = 0;
  for (const entry of entriesOf(plan, column, present)) {
    if (isControl(entry)) {
      slots.push([entry, cursor]);
      cursor += heightOf(entry);
    } else {
      cursor += chosen[indexes.get(entry) ?? 0] ?? entry.room[0];
    }
  }
  return { slots, height: cursor };
}

/** The callouts with the places a state gives them in one density. */
function placeIn(
  callouts: readonly Callout[],
  kind: JoyConKind,
  state: State,
): Callout[] {
  const plan = plans[kind];
  const byControl = new Map(callouts.map((c) => [c.control, c]));
  const present = new Set(byControl.keys());
  const out: Callout[] = [];
  const put = (control: string, position: Partial<Callout>) => {
    const callout = byControl.get(control);
    if (!callout) return;
    // The position is the layout's, whatever the callout had before.
    const placed: Callout = {
      ...callout,
      ...position,
      route: plan.routes[control] ?? "auto",
      ...(plan.twins[control] ? { twin: plan.twins[control] } : {}),
    };
    for (const key of [
      "span",
      "slot",
      "slotCompact",
      "slotMini",
      "side",
      "offset",
      "raised",
      "hang",
    ] as const)
      if (!(key in position)) delete placed[key];
    out.push(placed);
  };
  for (const region of regions) {
    if (region === "leftCol" || region === "rightCol") {
      const hung = plan.hang[region];
      const base = (hung ?? plan.start[region] ?? 4) + state.shift[region];
      const { slots } = columnSlots(plan, region, present, state.room[region]);
      for (const [control, slot] of slots)
        put(control, {
          placement: region === "leftCol" ? "left" : "right",
          slot: base + slot,
          ...(hung === undefined ? {} : { hang: true }),
          ...(isStick(control) ? { span: 2 } : {}),
        });
      continue;
    }
    const controls = entriesOf(plan, region, present).filter(isControl);
    for (const [index, control] of controls.entries()) {
      if (region === "top")
        put(control, {
          placement: "top",
          offset: index - (controls.length - 1) / 2,
        });
      else
        put(control, {
          placement: "top",
          side: region === "cornerL" ? "left" : "right",
          hang: true,
          // The last of a corner is on the slot after the top one's.
          slot: cornerTopSlot + index + (2 - controls.length),
        });
    }
  }
  return out;
}

/**
 * A layout: the state of each density. The chips are the same size in all
 * but the stage's size (density), and the model scales with the stage, so
 * what clears the leaders on one density does not on another; the layout of
 * the full density is the design's, the others are what the solver found for
 * them (slotCompact, slotMini).
 */
type Layout = Record<Density, State>;

/** The callouts with the places a layout gives them, for every density. */
function place(
  callouts: readonly Callout[],
  kind: JoyConKind,
  layout: Layout,
): Callout[] {
  const full = placeIn(callouts, kind, layout.full);
  const compact = placeIn(callouts, kind, layout.compact);
  const mini = placeIn(callouts, kind, layout.mini);
  return full.map((callout, index) => ({
    ...callout,
    ...(compact[index].slot !== callout.slot
      ? { slotCompact: compact[index].slot }
      : {}),
    ...(mini[index].slot !== callout.slot
      ? { slotMini: mini[index].slot }
      : {}),
  }));
}

const defaultLayout = (plan: Plan): Layout => ({
  full: defaultState(plan),
  compact: defaultState(plan),
  mini: defaultState(plan),
});

/**
 * Whether the plan has a place for every callout. A Joy-Con whose controls
 * SDL has not reported yet has the standard ones, which the plan does not
 * know; its chips keep the places the standard layout gave them.
 */
export function covers(callouts: readonly Callout[], kind: JoyConKind) {
  const known = new Set(controlsOf(plans[kind]));
  return callouts.every((c) => known.has(c.control));
}

/** Without the model: every chip where its plan puts it, with the room it starts with. */
export function staticLayout(
  callouts: readonly Callout[],
  kind: JoyConKind,
): Callout[] {
  if (!covers(callouts, kind)) return [...callouts];
  return place(callouts, kind, defaultLayout(plans[kind]));
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
 * What a layout is worth: the problems on the stages first, then, for the
 * density the design is of, how far it is from the plan's own room and places
 * (a clean layout is the design's own), and for the others the length of its
 * leaders (the chips of a column go where their parts are); the other of the
 * two decides a tie.
 */
interface Score {
  problems: number;
  length: number;
  moved: number;
}
const better = (a: Score, b: Score, design: boolean) =>
  a.problems !== b.problems
    ? a.problems < b.problems
    : design
      ? a.moved !== b.moved
        ? a.moved < b.moved
        : a.length < b.length - 0.5
      : Math.abs(a.length - b.length) > 0.5
        ? a.length < b.length
        : a.moved < b.moved;

/** The leaders of a placed layout on a stage. */
const leadersOn = (
  placed: readonly Callout[],
  anchors: Anchors,
  stage: Stage,
): Leader[] =>
  layoutLeaders(placed, stage, anchors.points, anchors.width, false, {
    halfHeight: anchors.halfHeight ?? defaultFrame.halfHeight,
    topWide: !anchors.narrowTop,
  });

/** How many problems a placed layout has on a stage. */
const problemsOn = (
  placed: readonly Callout[],
  anchors: Anchors,
  stage: Stage,
) => problemsOf(leadersOn(placed, anchors, stage), anchors, stage).length;

/**
 * The score of a layout on the stages. It stops as soon as the problems pass
 * those of `limit`, which is all a worse layout needs to be known by.
 */
function judge(
  callouts: readonly Callout[],
  kind: JoyConKind,
  state: State,
  anchors: Anchors,
  stages: readonly Stage[],
  limit?: Score,
): Score {
  const placed = placeIn(callouts, kind, state);
  const defaults = defaultState(plans[kind]);
  let moved = 0;
  for (const column of columns) {
    moved += Math.abs(state.shift[column] - defaults.shift[column]);
    state.room[column].forEach((value, index) => {
      moved += Math.abs(value - defaults.room[column][index]);
    });
  }
  let problems = 0,
    length = 0;
  for (const stage of stages) {
    const leaders = leadersOn(placed, anchors, stage);
    problems += problemsOf(leaders, anchors, stage).length;
    if (limit && problems > limit.problems)
      return { problems, length: Number.POSITIVE_INFINITY, moved };
    length += leaders.reduce((sum, l) => sum + leaderLength(l), 0);
  }
  return { problems, length: length / stages.length, moved };
}

/** The places a column (in slots) may be moved to. */
const shifts = Array.from({ length: 29 }, (_, index) => -3.5 + index * 0.25);
/** The places the two columns are tried at together. */
const pairedShifts = Array.from(
  { length: 15 },
  (_, index) => -3.5 + index * 0.5,
);

/**
 * The best state for some stage sizes, from a state to start from: for each
 * column in turn, one choice at a time (its place, the room at each gap), then
 * the two columns together, until nothing is better.
 */
function solve(
  callouts: readonly Callout[],
  kind: JoyConKind,
  anchors: Anchors,
  stages: readonly Stage[],
  design: boolean,
  from: State,
): { state: State; score: Score } {
  const plan = plans[kind];
  let state = from;
  let score = judge(callouts, kind, state, anchors, stages);
  const consider = (candidate: State) => {
    const next = judge(callouts, kind, candidate, anchors, stages, score);
    if (!better(next, score, design)) return false;
    state = candidate;
    score = next;
    return true;
  };
  // One column at a time (its place, the room at each gap), then the two
  // columns together (the best of one is not always the best of both), until
  // nothing is better.
  for (let pass = 0; pass < 3; pass++) {
    let improved = false;
    for (let round = 0; round < 4; round++) {
      let changed = false;
      for (const column of columns) {
        for (const shift of shifts)
          if (
            consider({ ...state, shift: { ...state.shift, [column]: shift } })
          )
            changed = true;
        const gaps = plan.regions[column].filter(
          (entry): entry is Room => !isControl(entry),
        );
        for (const [index, gap] of gaps.entries())
          for (const value of gap.room) {
            const current = state;
            const chosen = [...current.room[column]];
            chosen[index] = value;
            if (
              consider({
                ...current,
                room: { ...current.room, [column]: chosen },
              })
            )
              changed = true;
          }
      }
      if (!changed) break;
      improved = true;
    }
    for (const left of pairedShifts)
      for (const right of pairedShifts)
        if (consider({ ...state, shift: { leftCol: left, rightCol: right } }))
          improved = true;
    if (!improved) break;
  }
  return { state, score };
}

/**
 * The layouts found, by what they were found for: the search is the work of a
 * few hundred milliseconds, and the same model is built again whenever its
 * stage is (another pad, a lost context).
 */
const found = new Map<string, Layout>();
const keyOfAnchors = (kind: JoyConKind, anchors: Anchors) =>
  `${kind}|${anchors.width.toFixed(4)}|${Object.entries(anchors.points)
    .sort(([a], [b]) => (a < b ? -1 : 1))
    .map(
      ([c, p]) => `${c}:${p.x.toFixed(4)},${p.y.toFixed(4)},${p.z.toFixed(4)}`,
    )
    .join(";")}`;

/**
 * Stages larger than the workspace's usual (supportedStages stops at 1800 by
 * 900): a full-screen window on a large display. They are taken into account
 * too, for they are the full density, which is the design's.
 */
const largeStages: readonly Stage[] = [
  { width: 1920, height: 1040 },
  { width: 2560, height: 1300 },
];

const sameStage = (a: Stage, b: Stage) =>
  a.width === b.width && a.height === b.height;

/**
 * The state for the stages of one density: it starts with a few of them and
 * adds those of the rest that its answer is not clean on, until there are
 * none, or none it has not already been asked about (the plan has no better
 * answer).
 */
function solveDensity(
  callouts: readonly Callout[],
  kind: JoyConKind,
  anchors: Anchors,
  density: Density,
): State {
  const own = [...supportedStages, ...largeStages].filter(
    (s) => stageDensity(s) === density,
  );
  let stages = own.filter((_, index) => index % 6 === 0);
  let best: ReturnType<typeof solve> | undefined;
  const plan = plans[kind];
  const start = defaultState(plan);
  for (let round = 0; round < 8; round++) {
    best = solve(
      callouts,
      kind,
      anchors,
      stages,
      density === "full",
      best?.state ?? start,
    );
    const placed = placeIn(callouts, kind, best.state);
    const failing = own.filter(
      (stage) =>
        !stages.some((known) => sameStage(known, stage)) &&
        problemsOn(placed, anchors, stage) > 0,
    );
    if (failing.length === 0) break;
    // The newest first: the hardest sizes end a bad candidate soonest.
    stages = [...failing.slice(0, 8), ...stages];
  }
  return (best as NonNullable<typeof best>).state;
}

/**
 * The Joy-Con's callouts placed for the model's anchors: no two leaders cross
 * or pass through a chip, as far as the plan allows, on every stage of
 * supportedStages, a layout for each density of the chips. Where no choice is
 * clean it is the one with the fewest problems.
 */
export function arrangeJoyCon(
  callouts: readonly Callout[],
  kind: JoyConKind,
  anchors: Anchors,
): Callout[] {
  if (!covers(callouts, kind)) return [...callouts];
  const present = callouts.filter((c) => anchors.points[c.control]);
  const key = `${keyOfAnchors(kind, anchors)}|${present.map((c) => c.control).join()}`;
  let layout = found.get(key);
  if (!layout) {
    layout = {
      full: solveDensity(present, kind, anchors, "full"),
      compact: solveDensity(present, kind, anchors, "compact"),
      mini: solveDensity(present, kind, anchors, "mini"),
    };
    found.set(key, layout);
  }
  return place(callouts, kind, layout);
}
