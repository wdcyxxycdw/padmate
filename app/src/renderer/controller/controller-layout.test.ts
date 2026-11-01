import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { PerspectiveCamera, Vector3 } from "three";
import { describe, expect, it } from "vitest";
import {
  cameraDistance,
  cameraSpec,
  chipMetrics,
  chipRect,
  defaultFrame,
  distanceToSegment,
  layoutProblems,
  modelUnit,
  projectPoint,
  type Rect,
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
      "top: calc(50% + (var(--slot) - 4) * var(--pitch)); transform: translateY(-50%);",
      "top: calc(50% + (var(--slot) - 4) * var(--pitch) - var(--corner-gap));",
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
      "top: calc(50% + (var(--slot) + (var(--span) - 1) / 2 - 4) * var(--pitch));",
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
      to: [number, number],
    ) => ({
      control,
      rect,
      segment: segment(from[0], from[1], to[0], to[1]),
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
      "joyConLeftSideways",
      "joyConRightSideways",
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
