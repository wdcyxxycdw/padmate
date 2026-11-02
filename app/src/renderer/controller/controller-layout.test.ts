import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { PerspectiveCamera, Vector3 } from "three";
import { describe, expect, it } from "vitest";
import type { Callout } from "./controller-callouts";
import {
  cameraDistance,
  cameraSpec,
  chipMetrics,
  chipRect,
  cornerLift,
  cornerTopSlot,
  defaultFrame,
  distanceToSegment,
  gapOf,
  layoutLeaders,
  layoutProblems,
  leaderEnd,
  leaderLength,
  leaderPath,
  modelTop,
  modelUnit,
  pathSegments,
  projectPoint,
  type Rect,
  type Route,
  type Stage,
  segmentHitsRect,
  segmentsIntersect,
  stageDensity,
  supportedStages,
} from "./controller-layout";

const css = readFileSync(resolve(__dirname, "controller-3d.css"), "utf8");

// A small evaluator for the values the stylesheet gives the chip sizes:
// numbers in px, cqw and cqh, var(), calc(), clamp(), min() and max().
function evaluate(
  text: string,
  stage: Stage,
  vars: Record<string, string>,
): number {
  const tokens = text.match(
    /[a-z-]+\(|--[\w-]+|-?[\d.]+(?:px|cqw|cqh)?|[()+*/,-]/g,
  );
  if (!tokens) throw new Error(`Cannot read ${text}`);
  let at = 0;
  const peek = () => tokens[at];
  const take = () => tokens[at++];
  const list = (): number[] => {
    const values = [sum()];
    while (peek() === ",") {
      take();
      values.push(sum());
    }
    if (take() !== ")") throw new Error(`Unclosed in ${text}`);
    return values;
  };
  const atom = (): number => {
    const token = take();
    if (token === "(") {
      const value = sum();
      if (take() !== ")") throw new Error(`Unclosed in ${text}`);
      return value;
    }
    if (token === "var(") {
      const name = take();
      if (take() !== ")") throw new Error(`Unclosed var in ${text}`);
      return evaluate(vars[name], stage, vars);
    }
    if (token === "calc(") return list()[0];
    if (token === "clamp(") {
      const [min, value, max] = list();
      return Math.max(min, Math.min(value, max));
    }
    if (token === "min(") return Math.min(...list());
    if (token === "max(") return Math.max(...list());
    const number = /^(-?[\d.]+)(px|cqw|cqh)?$/.exec(token ?? "");
    if (!number) throw new Error(`Unexpected ${token} in ${text}`);
    const value = Number(number[1]);
    if (number[2] === "cqw") return (value * stage.width) / 100;
    if (number[2] === "cqh") return (value * stage.height) / 100;
    return value;
  };
  const product = (): number => {
    let value = atom();
    while (peek() === "*" || peek() === "/") {
      const operator = take();
      const right = atom();
      value = operator === "*" ? value * right : value / right;
    }
    return value;
  };
  const sum = (): number => {
    let value = product();
    while (peek() === "+" || peek() === "-") {
      const operator = take();
      const right = product();
      value = operator === "+" ? value + right : value - right;
    }
    return value;
  };
  return sum();
}

/** The declarations of \`.controller-labels\` for a density: full, compact, mini. */
function variables(density: "full" | "compact" | "mini") {
  const blocks = [...css.matchAll(/\.controller-labels \{([^}]*)\}/g)].map(
    (match) => match[1],
  );
  expect(blocks).toHaveLength(3);
  const vars: Record<string, string> = {};
  const upTo = { full: 1, compact: 2, mini: 3 }[density];
  for (const block of blocks.slice(0, upTo))
    for (const match of block.matchAll(/(--[\w-]+):\s*([^;]+);/g))
      vars[match[1]] = match[2].replace(/\s+/g, " ").trim();
  return vars;
}

const densityStages = {
  full: [
    { width: 860, height: 600 },
    { width: 1180, height: 800 },
    { width: 1800, height: 900 },
    { width: 1000, height: 1400 },
  ],
  compact: [
    { width: 859, height: 800 },
    { width: 700, height: 500 },
    { width: 600, height: 599 },
    { width: 1400, height: 440 },
  ],
  mini: [
    { width: 440, height: 440 },
    { width: 599, height: 900 },
    { width: 1400, height: 439 },
    { width: 520, height: 700 },
  ],
} as const;

describe("the chip sizes", () => {
  it.each(
    Object.entries(densityStages).flatMap(([density, list]) =>
      list.map((stage) => [density, stage] as const),
    ),
  )("are the stylesheet's in the %s density on %j", (density, stage) => {
    expect(stageDensity(stage)).toBe(density);
    const vars = variables(density as keyof typeof densityStages);
    const m = chipMetrics(stage);
    const sizes: [string, number][] = [
      ["--chip-h", m.chipH],
      ["--chip-w", m.chipW],
      ["--pitch", m.pitch],
      ["--corner-gap", m.cornerGap],
      ["--corner-shift", m.cornerShift],
      ["--top-w", m.topW],
      ["--pair-top-w", m.pairTopW],
      ["--top-gap", m.topGap],
      ["--top-raise", m.topRaise],
      ["--top-y", m.topY],
      ["--edge", m.edge],
      ["--model-gap", m.modelGap],
      ["--side-x", m.sideX],
    ];
    for (const [name, value] of sizes)
      expect(evaluate(vars[name], stage, vars), name).toBeCloseTo(value, 6);
  });

  it("change density at the widths and heights the stylesheet's container queries say", () => {
    expect(css).toContain(
      "@container stage (width < 860px) or (height < 600px)",
    );
    expect(css).toContain(
      "@container stage (width < 600px) or (height < 440px)",
    );
    expect(stageDensity({ width: 860, height: 600 })).toBe("full");
    expect(stageDensity({ width: 859, height: 600 })).toBe("compact");
    expect(stageDensity({ width: 860, height: 599 })).toBe("compact");
    expect(stageDensity({ width: 600, height: 440 })).toBe("compact");
    expect(stageDensity({ width: 599, height: 440 })).toBe("mini");
    expect(stageDensity({ width: 600, height: 439 })).toBe("mini");
  });
});

describe("the places of the chips", () => {
  const normal = css.replace(/\s+/g, " ");
  it("follow the rules the stylesheet has for them", () => {
    for (const rule of [
      // Columns and corners: the slot counts from 4 at the middle.
      "top: calc(50% + (var(--s) - 4) * var(--pitch)); transform: translateY(-50%);",
      "top: calc(50% + (var(--s) - 4) * var(--pitch) - var(--corner-gap));",
      "left: var(--side-x);",
      "right: var(--side-x);",
      "left: calc(var(--side-x) + var(--corner-shift));",
      "right: calc(var(--side-x) + var(--corner-shift));",
      // The row above, and the row below.
      "left: calc(50% + var(--offset) * (var(--top-w) + var(--top-gap)));",
      "top: var(--top-y);",
      "top: max(8px, calc(var(--top-y) - var(--top-raise)));",
      "(100cqw - 2 * (var(--side-x) + var(--chip-w) + var(--top-gap))) / max(5, var(--row-count, 5))",
      // A Joy-Con's rows.
      "--top-pitch: min( calc(var(--chip-w) + var(--top-gap)), calc((100cqw - 2 * var(--top-inset)) / max(1, var(--top-count, 1))) );",
      "left: calc(50% + var(--offset) * var(--top-pitch));",
      "--row-pitch: min( calc(var(--chip-w) + var(--top-gap)), calc((100cqw - 2 * var(--bottom-inset)) / max(1, var(--row-count, 1))) );",
      "--top-inset: calc(var(--side-x) + var(--chip-w) + var(--top-gap));",
      "--bottom-inset: calc(var(--side-x) + var(--chip-w) + var(--top-gap));",
      "calc(50cqh - var(--model-h) * var(--unit) - var(--chip-h) - 10px)",
      "calc(50cqh + var(--model-h) * var(--unit) + 10px), calc(100% - 72px - var(--chip-h))",
      "calc(50cqh + var(--model-h) * var(--unit) + 10px), calc(100% - 112px - var(--chip-h))",
      "--corner-shift: 0px;",
      "width: calc(var(--row-pitch) - var(--top-gap));",
      "left: calc(50% + var(--offset) * var(--row-pitch));",
      "min( calc(50% + min(27.4cqh, 19.6cqw) + 6px), calc(100% - 72px - var(--chip-h)) );",
      "calc(100% - 112px - var(--chip-h))",
      // A stick.
      "top: calc(50% + (var(--s) + (var(--span) - 1) / 2 - 4) * var(--pitch));",
      "height: calc(var(--chip-h) + (var(--span) - 1) * var(--pitch));",
    ])
      expect(normal.includes(rule), rule).toBe(true);
  });

  it("size a pixel of the model's frame as the stylesheet's --unit does", () => {
    const block = /\.controller-labels\[data-wide-rows\] \{([^}]*)\}/.exec(css);
    expect(block).not.toBeNull();
    const vars: Record<string, string> = {};
    for (const match of (block?.[1] ?? "").matchAll(/(--[\w-]+):\s*([^;]+);/g))
      vars[match[1]] = match[2].replace(/\s+/g, " ").trim();
    for (const stage of [
      { width: 1180, height: 800 },
      { width: 440, height: 900 },
      { width: 1800, height: 500 },
      { width: 640, height: 640 },
    ])
      for (const width of [5.6, 3.45, 1.58]) {
        const own: Record<string, string> = {
          ...vars,
          "--model-w": String(width),
        };
        expect(evaluate(own["--unit"], stage, own)).toBeCloseTo(
          modelUnit(stage, width),
          1,
        );
      }
    // The default frame is the stylesheet's.
    expect(Number(vars["--model-w"])).toBe(defaultFrame.width);
    expect(Number(vars["--model-h"])).toBe(defaultFrame.halfHeight);
  });

  it("put a column's chips between the stage's edge and the model", () => {
    const stage = { width: 1180, height: 800 };
    const m = chipMetrics(stage);
    const left = chipRect({ placement: "left", slot: 4 }, stage, 0);
    const right = chipRect({ placement: "right", slot: 4 }, stage, 0);
    expect(left.left).toBeCloseTo(m.sideX, 6);
    expect(right.left + right.width).toBeCloseTo(stage.width - m.sideX, 6);
    // The middle of slot 4 is the middle of the stage.
    expect(left.top + left.height / 2).toBeCloseTo(stage.height / 2, 6);
    // A stick is two slots tall and centred between its two slots.
    const stick = chipRect({ placement: "left", slot: 4, span: 2 }, stage, 0);
    expect(stick.height).toBeCloseTo(m.chipH + m.pitch, 6);
    expect(stick.top + stick.height / 2).toBeCloseTo(
      stage.height / 2 + m.pitch / 2,
      6,
    );
  });

  it("put a corner above its column, a little inside it", () => {
    const stage = { width: 1180, height: 800 };
    const column = chipRect({ placement: "right", slot: 2 }, stage, 0);
    const corner = chipRect(
      { placement: "top", side: "right", slot: 1 },
      stage,
      0,
    );
    expect(corner.top).toBeLessThan(column.top - 10);
    expect(corner.left + corner.width).toBeLessThan(column.left + column.width);
  });

  it("lift the row below when the editor notice shows, and only then", () => {
    const stage = { width: 600, height: 440 };
    const place = { placement: "bottom", offset: 0 } as const;
    const plain = chipRect(place, stage, 2);
    const lifted = chipRect(place, stage, 2, true);
    expect(lifted.top).toBeLessThan(plain.top);
    const roomy = { width: 1180, height: 800 };
    expect(chipRect(place, roomy, 2, true).top).toBe(
      chipRect(place, roomy, 2).top,
    );
  });

  it("keep supportedStages inside the sizes the workspace gives the stage", () => {
    for (const stage of supportedStages) {
      expect(stage.width).toBeGreaterThanOrEqual(440);
      expect(stage.height).toBeGreaterThanOrEqual(440);
    }
    expect(css).toContain(".controller-view");
    const workspace = readFileSync(
      resolve(__dirname, "../workspace/workspace.css"),
      "utf8",
    );
    expect(workspace).toMatch(/\.controller-view \{[^}]*min-width: 440px;/);
    expect(workspace).toMatch(/\.controller-view \{[^}]*min-height: 440px;/);
  });
});

describe("projecting the model's points", () => {
  it.each([
    [{ width: 1180, height: 800 }, 5.6],
    [{ width: 440, height: 440 }, 5.6],
    [{ width: 600, height: 900 }, 3.4],
    [{ width: 1800, height: 500 }, 1.58],
  ])("is the camera's, on %j with a model %f wide", (stage, modelWidth) => {
    const camera = new PerspectiveCamera(cameraSpec.fov, 1, 0.05, 100);
    camera.aspect = stage.width / stage.height;
    camera.position.set(0, 0, cameraDistance(modelWidth, camera.aspect));
    camera.lookAt(0, 0, 0);
    camera.updateProjectionMatrix();
    camera.updateMatrixWorld(true);
    for (const [x, y, z] of [
      [0, 0, 0],
      [2.3, -0.4, -0.44],
      [-1.2, 1.7, 0.6],
      [0.5, 0.5, 1.2],
    ]) {
      const p = new Vector3(x, y, z).project(camera);
      const expected = {
        x: ((p.x + 1) * stage.width) / 2,
        y: ((1 - p.y) * stage.height) / 2,
      };
      const got = projectPoint({ x, y, z }, modelWidth, stage);
      expect(got.x).toBeCloseTo(expected.x, 6);
      expect(got.y).toBeCloseTo(expected.y, 6);
    }
  });

  it("zooms out for a narrow stage and not for a wide one", () => {
    expect(cameraDistance(5.6, 3)).toBe(cameraSpec.minDistance);
    expect(cameraDistance(5.6, 1)).toBeGreaterThan(cameraSpec.minDistance);
    // The width the camera fits is cameraSpec.fitWidth times the model's.
    const aspect = 1;
    const width =
      2 *
      Math.tan(((cameraSpec.fov / 2) * Math.PI) / 180) *
      cameraDistance(5.6, aspect) *
      aspect;
    expect(width).toBeCloseTo(5.6 * cameraSpec.fitWidth, 6);
  });
});

describe("telling whether leaders meet", () => {
  const segment = (x1: number, y1: number, x2: number, y2: number) => ({
    from: { x: x1, y: y1 },
    to: { x: x2, y: y2 },
  });

  it("finds segments that cross, touch, or lie on each other", () => {
    expect(
      segmentsIntersect(segment(0, 0, 10, 10), segment(0, 10, 10, 0)),
    ).toBe(true);
    // An end on the other segment counts, and so does an end on an end.
    expect(segmentsIntersect(segment(0, 0, 10, 0), segment(5, 0, 5, 8))).toBe(
      true,
    );
    expect(segmentsIntersect(segment(0, 0, 4, 4), segment(4, 4, 9, 1))).toBe(
      true,
    );
    expect(segmentsIntersect(segment(0, 0, 10, 0), segment(4, 0, 12, 0))).toBe(
      true,
    );
  });

  it("finds segments that are apart", () => {
    expect(segmentsIntersect(segment(0, 0, 10, 0), segment(0, 1, 10, 1))).toBe(
      false,
    );
    // Lines that would meet beyond their ends.
    expect(segmentsIntersect(segment(0, 0, 4, 4), segment(10, 0, 6, 4))).toBe(
      false,
    );
    expect(segmentsIntersect(segment(0, 0, 10, 0), segment(11, 0, 20, 0))).toBe(
      false,
    );
  });

  it("finds a segment through a box, ending in it, or beside it", () => {
    const box: Rect = { left: 10, top: 10, width: 20, height: 20 };
    expect(segmentHitsRect(segment(0, 20, 40, 20), box)).toBe(true);
    expect(segmentHitsRect(segment(0, 0, 20, 20), box)).toBe(true);
    expect(segmentHitsRect(segment(15, 15, 18, 18), box)).toBe(true);
    expect(segmentHitsRect(segment(0, 0, 40, 5), box)).toBe(false);
    expect(segmentHitsRect(segment(0, 0, 9, 40), box)).toBe(false);
    // A margin grows the box.
    expect(segmentHitsRect(segment(0, 9.5, 40, 9.5), box)).toBe(false);
    expect(segmentHitsRect(segment(0, 9.5, 40, 9.5), box, 1)).toBe(true);
  });

  it("measures how near a point is to a segment", () => {
    const s = segment(0, 0, 10, 0);
    expect(distanceToSegment({ x: 5, y: 3 }, s)).toBe(3);
    expect(distanceToSegment({ x: 13, y: 4 }, s)).toBe(5);
    expect(distanceToSegment({ x: 5, y: 0 }, s)).toBe(0);
    expect(distanceToSegment({ x: 1, y: 1 }, segment(2, 2, 2, 2))).toBeCloseTo(
      Math.SQRT2,
      9,
    );
  });

  it("reports the problems of a layout by their kind", () => {
    const stage = { width: 400, height: 400 };
    const leader = (
      control: string,
      rect: Rect,
      from: [number, number],
      ...rest: [number, number][]
    ) => ({
      control,
      rect,
      path: [from, ...rest].map(([x, y]) => ({ x, y })),
    });
    const box = (left: number, top: number): Rect => ({
      left,
      top,
      width: 40,
      height: 20,
    });
    const clean = [
      leader("a", box(0, 0), [40, 10], [200, 100]),
      leader("b", box(0, 100), [40, 110], [200, 200]),
    ];
    expect(layoutProblems(clean, stage)).toEqual([]);
    const crossing = [
      leader("a", box(0, 0), [40, 10], [200, 200]),
      leader("b", box(0, 100), [40, 110], [200, 100]),
    ];
    expect(layoutProblems(crossing, stage).map((p) => p.kind)).toEqual([
      "cross",
    ]);
    const through = [
      leader("a", box(0, 0), [40, 10], [200, 200]),
      leader("b", box(100, 90), [140, 100], [300, 300]),
    ];
    expect(layoutProblems(through, stage).map((p) => p.kind)).toContain(
      "through",
    );
    const overlapping = [
      leader("a", box(0, 0), [40, 10], [200, 100]),
      leader("b", box(20, 10), [60, 20], [200, 300]),
    ];
    expect(layoutProblems(overlapping, stage).map((p) => p.kind)).toContain(
      "overlap",
    );
    const outside = [leader("a", box(380, 0), [380, 10], [200, 100])];
    expect(layoutProblems(outside, stage).map((p) => p.kind)).toEqual([
      "outside",
    ]);
    // A leader that ends on another's path is close enough to count.
    const grazing = [
      leader("a", box(0, 0), [40, 10], [300, 10]),
      leader("b", box(0, 100), [40, 110], [150, 10.5]),
    ];
    expect(layoutProblems(grazing, stage).map((p) => p.kind)).toContain(
      "touch",
    );
  });

  it("judges every piece of a bent leader", () => {
    const stage = { width: 400, height: 400 };
    const box = (left: number, top: number): Rect => ({
      left,
      top,
      width: 40,
      height: 20,
    });
    const leader = (
      control: string,
      rect: Rect,
      ...path: [number, number][]
    ) => ({ control, rect, path: path.map(([x, y]) => ({ x, y })) });
    // The second piece of a crosses the first of b, which no straight line
    // from the two ends would.
    const bentCross = [
      leader("a", box(0, 0), [40, 10], [150, 10], [250, 110]),
      leader("b", box(0, 100), [40, 110], [200, 110], [200, 40]),
    ];
    expect(layoutProblems(bentCross, stage).map((p) => p.kind)).toEqual([
      "cross",
    ]);
    // The second piece of a bent leader goes through a chip that the straight
    // line from its start to its end would pass by.
    const bentThrough = [
      leader("a", box(0, 0), [40, 10], [300, 10], [300, 300]),
      leader("b", box(290, 100), [330, 110], [350, 300]),
    ];
    expect(
      layoutProblems(bentThrough, stage)
        .filter((p) => p.kind === "through")
        .map((p) => p.controls.join("/")),
    ).toEqual(["a/b"]);
    const straight = [
      leader("a", box(0, 0), [40, 10], [300, 300]),
      leader("b", box(290, 100), [330, 110], [350, 300]),
    ];
    expect(
      layoutProblems(straight, stage).filter((p) => p.kind === "through"),
    ).toEqual([]);
    // A bent leader that passes another's end touches it.
    const near = [
      leader("a", box(0, 0), [40, 10], [100, 10], [200, 110]),
      leader("b", box(0, 100), [40, 110], [150, 60]),
    ];
    expect(layoutProblems(near, stage).map((p) => p.kind)).toContain("touch");
    // Its length is the sum of its pieces.
    expect(
      leaderLength(leader("a", box(0, 0), [0, 0], [30, 0], [30, 40])),
    ).toBe(70);
  });
});

describe("the bends of a leader", () => {
  const column = { placement: "left", route: "auto" } as const;
  const row = { placement: "top", route: "auto" } as const;

  it("is straight without a route", () => {
    const from = { x: 0, y: 0 };
    const to = { x: 50, y: 80 };
    expect(leaderPath({ placement: "left" }, from, to)).toEqual([from, to]);
  });

  it("runs along a column's line and then at 45 degrees where that fits", () => {
    const path = leaderPath(column, { x: 100, y: 200 }, { x: 300, y: 260 });
    expect(path).toEqual([
      { x: 100, y: 200 },
      { x: 240, y: 200 },
      { x: 300, y: 260 },
    ]);
    // Going up, and from a chip on the right of the model.
    expect(
      leaderPath(
        { placement: "right", route: "auto" },
        { x: 900, y: 200 },
        { x: 700, y: 120 },
      ),
    ).toEqual([
      { x: 900, y: 200 },
      { x: 780, y: 200 },
      { x: 700, y: 120 },
    ]);
  });

  it("takes a stub of 12 pixels where the part is too near across for 45 degrees", () => {
    expect(leaderPath(column, { x: 100, y: 200 }, { x: 150, y: 330 })).toEqual([
      { x: 100, y: 200 },
      { x: 112, y: 200 },
      { x: 150, y: 330 },
    ]);
    // Exactly the run plus the stub still bends at 45 degrees.
    expect(leaderPath(column, { x: 100, y: 200 }, { x: 200, y: 288 })).toEqual([
      { x: 100, y: 200 },
      { x: 112, y: 200 },
      { x: 200, y: 288 },
    ]);
  });

  it("is straight where the part is level with the chip", () => {
    const from = { x: 100, y: 200 };
    const to = { x: 300, y: 201.5 };
    expect(leaderPath(column, from, to)).toEqual([from, to]);
  });

  it("goes down from a chip of a row and then across", () => {
    expect(leaderPath(row, { x: 500, y: 60 }, { x: 560, y: 300 })).toEqual([
      { x: 500, y: 60 },
      { x: 500, y: 240 },
      { x: 560, y: 300 },
    ]);
    expect(leaderPath(row, { x: 500, y: 60 }, { x: 500, y: 300 })).toEqual([
      { x: 500, y: 60 },
      { x: 500, y: 300 },
    ]);
  });

  it("bends a steep leader near the part, its last run about 70 degrees", () => {
    const path = leaderPath(
      { placement: "left", route: "steep" },
      { x: 100, y: 100 },
      { x: 400, y: 217 },
    );
    expect(path[1].y).toBe(100);
    expect(path[1].x).toBeCloseTo(400 - 0.36 * 117, 6);
    const angle =
      (Math.atan2(path[2].y - path[1].y, path[2].x - path[1].x) * 180) /
      Math.PI;
    expect(angle).toBeGreaterThan(68);
    expect(angle).toBeLessThan(72);
    // Too near to bend there: the stub.
    expect(
      leaderPath(
        { placement: "left", route: "steep" },
        { x: 100, y: 100 },
        { x: 120, y: 300 },
      )[1],
    ).toEqual({ x: 112, y: 100 });
  });

  it("goes through the gap of a route along the line from the part", () => {
    // The part is at (400, 300) and the gap at (385, 310): the line goes
    // down and to the left, and meets the chip's line (y 380) at x 280.
    const path = leaderPath(
      { placement: "left", route: { gap: ["a", "b"] } },
      { x: 100, y: 380 },
      { x: 400, y: 300 },
      { x: 385, y: 310 },
    );
    expect(path).toEqual([
      { x: 100, y: 380 },
      { x: 280, y: 380 },
      { x: 400, y: 300 },
    ]);
    // Without the gap it is the plain route.
    const plain = leaderPath(column, { x: 100, y: 380 }, { x: 400, y: 300 });
    expect(
      leaderPath(
        { placement: "left", route: { gap: ["a", "b"] } },
        { x: 100, y: 380 },
        { x: 400, y: 300 },
      ),
    ).toEqual(plain);
  });

  it("goes a stub, then at 45 degrees to the line through the gap, where the line is not met on the chip's own line", () => {
    // The line from the part (400, 300) through the gap (380, 280) runs up
    // to the left, away from the chip's line (y 380): a leader meets it by
    // going a stub along its line (to x 112), at 45 degrees up to the line
    // (at 296, 196), and along it to the part.
    expect(
      leaderPath(
        { placement: "left", route: { gap: ["a", "b"] } },
        { x: 100, y: 380 },
        { x: 400, y: 300 },
        { x: 380, y: 280 },
      ),
    ).toEqual([
      { x: 100, y: 380 },
      { x: 112, y: 380 },
      { x: 296, y: 196 },
      { x: 400, y: 300 },
    ]);
  });

  it("projects the gap of a route between two parts", () => {
    const stage = { width: 800, height: 600 };
    const points = {
      a: { x: -1, y: 0.5, z: 0 },
      b: { x: 1, y: -0.5, z: 0 },
    };
    const middle = gapOf({ route: { gap: ["a", "b"] } }, points, 5.6, stage);
    const one = projectPoint(points.a, 5.6, stage);
    const two = projectPoint(points.b, 5.6, stage);
    expect(middle?.x).toBeCloseTo((one.x + two.x) / 2, 9);
    expect(middle?.y).toBeCloseTo((one.y + two.y) / 2, 9);
    expect(gapOf({ route: "auto" }, points, 5.6, stage)).toBeUndefined();
    expect(
      gapOf({ route: { gap: ["a", "missing"] } }, points, 5.6, stage),
    ).toBeUndefined();
  });

  it("draws the leaders of a layout along their routes", () => {
    const stage = { width: 1100, height: 640 };
    const points = { x: { x: 0.5, y: -0.3, z: 0 } };
    const callout = (route?: Route): Callout => ({
      control: "x",
      label: "X",
      placement: "left",
      slot: 3,
      ...(route ? { route } : {}),
    });
    const [straight] = layoutLeaders([callout()], stage, points, 5.6);
    expect(straight.path).toHaveLength(2);
    const [bent] = layoutLeaders([callout("auto")], stage, points, 5.6);
    expect(bent.path).toHaveLength(3);
    expect(bent.path[0]).toEqual(straight.path[0]);
    expect(leaderEnd(bent)).toEqual(leaderEnd(straight));
    expect(pathSegments(bent.path)).toHaveLength(2);
  });
});

describe("the places of a Joy-Con's chips", () => {
  const normal = css.replace(/\s+/g, " ");
  const block = /\.controller-labels\[data-wide-rows\] \{([^}]*)\}/.exec(css);
  const own: Record<string, string> = {};
  for (const match of (block?.[1] ?? "").matchAll(/(--[\w-]+):\s*([^;]+);/g))
    own[match[1]] = match[2].replace(/\s+/g, " ").trim();
  const stages = [
    { width: 860, height: 600 },
    { width: 1180, height: 800 },
    { width: 1800, height: 900 },
    { width: 700, height: 500 },
    { width: 1400, height: 440 },
    { width: 440, height: 440 },
    { width: 599, height: 900 },
    { width: 1100, height: 640 },
  ];
  /** The wide rows of a Joy-Con whose model is `width` wide and reaches `halfHeight`. */
  const wideOf = (width: number, halfHeight: number) => ({
    width,
    halfHeight,
    topWide: false,
    tops: 2,
    corners: true,
    columns: true,
  });

  it("hang from the model's top, as the stylesheet says", () => {
    for (const rule of [
      '.controller-labels[data-wide-rows] .stage-chip[data-hang][data-placement="left"], .controller-labels[data-wide-rows] .stage-chip[data-hang][data-placement="right"] { top: calc(var(--model-top) + var(--s) * var(--pitch)); }',
      ".controller-labels[data-wide-rows] .stage-stick[data-hang] { top: calc( var(--model-top) + (var(--s) + (var(--span) - 1) / 2) * var(--pitch) ); }",
      ".controller-labels[data-wide-rows] .stage-chip[data-side][data-hang] { top: calc(var(--model-top) + var(--s) * var(--pitch) + var(--corner-lift)); }",
    ])
      expect(normal.includes(rule), rule).toBe(true);
    for (const stage of stages)
      for (const [width, halfHeight] of [
        [5.6, 2.1],
        [3.45, 2.07],
        [1.58, 2.05],
      ]) {
        const vars = {
          ...variables(stageDensity(stage)),
          ...own,
          "--model-w": String(width),
          "--model-h": String(halfHeight),
        };
        const wide = wideOf(width, halfHeight);
        const name = `${stage.width}x${stage.height} model ${width}`;
        expect(evaluate(own["--model-top"], stage, vars), name).toBeCloseTo(
          modelTop(stage, wide),
          2,
        );
        expect(evaluate(own["--corner-lift"], stage, vars), name).toBeCloseTo(
          cornerLift(stage, wide),
          2,
        );
      }
    // A chip that hangs is as far below the model's top as its slot says; one
    // that does not is where the middle of the stage puts it.
    const stage = { width: 1180, height: 800 };
    const wide = wideOf(3.45, 2.07);
    const m = chipMetrics(stage);
    const hung = chipRect(
      { placement: "left", slot: 2, hang: true },
      stage,
      0,
      false,
      wide,
    );
    expect(hung.top + hung.height / 2).toBeCloseTo(
      modelTop(stage, wide) + 2 * m.pitch,
      6,
    );
    const centred = chipRect(
      { placement: "left", slot: 2 },
      stage,
      0,
      false,
      wide,
    );
    expect(centred.top + centred.height / 2).toBeCloseTo(
      stage.height / 2 + (2 - 4) * m.pitch,
      6,
    );
    // A stick that hangs is centred between its two slots.
    const stick = chipRect(
      { placement: "left", slot: 2, span: 2, hang: true },
      stage,
      0,
      false,
      wide,
    );
    expect(stick.top + stick.height / 2).toBeCloseTo(
      modelTop(stage, wide) + 2.5 * m.pitch,
      6,
    );
  });

  it("are where the layout put them for the density of the stage, as the stylesheet says", () => {
    expect(normal).toContain(".stage-chip, .stage-stick { --s: var(--slot); }");
    expect(normal).toContain("--s: var(--slot-compact, var(--slot));");
    expect(normal).toContain("--s: var(--slot-mini, var(--slot));");
    const place = {
      placement: "left",
      slot: 5,
      slotCompact: 3,
      slotMini: 2,
    } as const;
    const centre = (stage: Stage) => {
      const rect = chipRect(place, stage, 0);
      return rect.top + rect.height / 2 - stage.height / 2;
    };
    for (const [stage, slot] of [
      [{ width: 1180, height: 800 }, 5],
      [{ width: 700, height: 600 }, 3],
      [{ width: 1180, height: 500 }, 3],
      [{ width: 500, height: 800 }, 2],
      [{ width: 900, height: 439 }, 2],
    ] as const)
      expect(centre(stage), JSON.stringify(stage)).toBeCloseTo(
        (slot - 4) * chipMetrics(stage).pitch,
        6,
      );
    // Without them, the slot is every density's.
    const plain = chipRect(
      { placement: "left", slot: 5 },
      { width: 500, height: 800 },
      0,
    );
    expect(plain.top + plain.height / 2).toBeCloseTo(
      400 + chipMetrics({ width: 500, height: 800 }).pitch,
      6,
    );
  });

  it("sit lower when the model is too near the stage's top for the corners above it", () => {
    expect(cornerTopSlot).toBe(-1.59);
    expect(normal).toContain(
      "calc(4px + var(--chip-h) / 2 - var(--model-top) + 1.59 * var(--pitch))",
    );
    // Nothing is lifted where the stack fits, as on the design's stage.
    const roomy = wideOf(3.45, 2.07);
    expect(cornerLift({ width: 1100, height: 638 }, roomy)).toBe(0);
    // Where it does not, the top corner keeps 4px from the stage's top.
    const tight = { width: 600, height: 440 };
    const place = {
      placement: "top",
      side: "left",
      slot: cornerTopSlot,
      hang: true,
    } as const;
    for (const stage of [tight, { width: 860, height: 500 }]) {
      const wide = wideOf(1.58, 2.7);
      expect(cornerLift(stage, wide)).toBeGreaterThan(0);
      expect(chipRect(place, stage, 0, false, wide).top).toBeCloseTo(4, 6);
    }
    // Every corner is lifted by the same, so two keep their distance; the
    // corners of another pad are not lifted or hung.
    const wide = wideOf(1.58, 2.7);
    const upper = chipRect(place, tight, 0, false, wide);
    const lower = chipRect(
      { ...place, slot: cornerTopSlot + 1 },
      tight,
      0,
      false,
      wide,
    );
    expect(lower.top - upper.top).toBeCloseTo(chipMetrics(tight).pitch, 6);
    expect(
      chipRect({ placement: "top", side: "left", slot: 0 }, tight, 0).top,
    ).toBeCloseTo(
      tight.height / 2 -
        4 * chipMetrics(tight).pitch -
        chipMetrics(tight).cornerGap -
        chipMetrics(tight).chipH / 2,
      6,
    );
  });

  it("have a row above a pair that is narrower than a column's chip, as the stylesheet says", () => {
    expect(normal).toContain(
      "--chip-top-w: min(var(--top-w), var(--pair-top-w)); width: var(--chip-top-w); left: calc(50% + var(--offset) * (var(--chip-top-w) + var(--top-gap)));",
    );
    const pair = wideOf(3.45, 2.07);
    for (const stage of stages) {
      const m = chipMetrics(stage);
      const row = chipRect(
        { placement: "top", offset: 0.5 },
        stage,
        0,
        false,
        pair,
      );
      expect(row.width, `${stage.width}x${stage.height}`).toBe(
        Math.min(m.topW, m.pairTopW),
      );
    }
    // Another pad's row is as wide as ever.
    const m = chipMetrics({ width: 1100, height: 640 });
    expect(
      chipRect(
        { placement: "top", offset: 0.5 },
        { width: 1100, height: 640 },
        0,
      ).width,
    ).toBe(m.topW);
  });
});

describe("the development preview of the layouts", () => {
  const app = resolve(__dirname, "../../..");
  const read = (path: string) => readFileSync(resolve(app, path), "utf8");

  it("is run by its own script on port 5199, and is no part of the app's build", () => {
    const scripts = JSON.parse(read("package.json")).scripts;
    expect(scripts["preview:layouts"]).toBe(
      "vite --config tests/preview/vite.config.ts",
    );
    expect(read("tests/preview/vite.config.ts")).toContain("port: 5199");
    // electron-vite and electron-builder never look at it.
    expect(read("electron.vite.config.ts")).not.toContain("preview");
    expect(read("electron-builder.yml")).not.toContain("tests/");
    expect(read("tests/preview/controller-layouts.html")).toContain(
      "./controller-layouts.tsx",
    );
  });

  it("shows every placement of both generations", () => {
    const page = read("tests/preview/controller-layouts.tsx");
    for (const name of [
      "joyConLeftUpright",
      "joyConRightUpright",
      "joyConPair",
    ])
      expect(page, name).toContain(name);
    expect(page).toContain("joyCon2Pads");
    expect(page).toContain("sdlPads");
    expect(page).toContain("createSceneWithOwnStage");
  });
});

describe("the preview's self-check", () => {
  const read = (path: string) =>
    readFileSync(resolve(__dirname, "../../..", path), "utf8");

  it("is on the page (?check=1) and run headless by a script", () => {
    const page = read("tests/preview/controller-layouts.tsx");
    expect(page).toContain('query.get("check") === "1"');
    expect(page).toContain("layout-check");
    expect(read("tests/preview/layout-check.ts")).toContain(
      "getBoundingClientRect",
    );
    const script = read("tests/preview/check-layouts.mjs");
    expect(script).toContain("headless: true");
    expect(script).toContain("--use-angle=swiftshader");
    expect(script).not.toContain("electron");
  });
});
