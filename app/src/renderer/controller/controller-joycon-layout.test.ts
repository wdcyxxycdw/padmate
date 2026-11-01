import { Box3, Mesh, PerspectiveCamera, Vector3 } from "three";
import { describe, expect, it } from "vitest";
import {
  type FixtureModel,
  modelFixture as fixture,
} from "../testing/controller-assets";
import { joyCon2Pads, sdlPads } from "../testing/sdl-pads";
import {
  type Callout,
  controllerCallouts,
  withArrangement,
} from "./controller-callouts";
import {
  arrangeJoyCon,
  diagramHidden,
  joyConKind,
  staticLayout,
} from "./controller-joycon-layout";
import {
  cameraDistance,
  cameraSpec,
  convexHull,
  defaultFrame,
  layoutLeaders,
  layoutProblems,
  type Point,
  type Point3,
  projectedHull,
  rectHitsPolygon,
  type Stage,
  supportedStages,
} from "./controller-layout";

type Name = keyof typeof sdlPads & keyof typeof joyCon2Pads;
const placements = [
  "joyConLeftSideways",
  "joyConRightSideways",
  "joyConLeftUpright",
  "joyConRightUpright",
  "joyConPair",
] as const satisfies readonly Name[];
const generations = [
  { name: "Switch 1 (OLED model)", pads: sdlPads },
  { name: "Switch 2 (NAIT model)", pads: joyCon2Pads },
] as const;

/** Every size the workspace can give the stage, and some it can too. */
const stages: Stage[] = [
  ...supportedStages,
  { width: 777, height: 533 },
  { width: 1021, height: 677 },
  { width: 640, height: 480 },
  { width: 463, height: 601 },
  { width: 1366, height: 768 },
  { width: 1920, height: 1040 },
  { width: 1100, height: 640 },
  { width: 640, height: 640 },
];

/** The anchors of a built model, where the scene ends its leaders. */
function anchorsOf(model: FixtureModel) {
  const points: Record<string, Point3> = {};
  for (const [name, control] of model.controls) {
    const p = model.anchor(control);
    points[name] = { x: p.x, y: p.y, z: p.z };
  }
  const width = new Box3().setFromObject(model.group).getSize(new Vector3()).x;
  return { points, width };
}

const model = (
  pads: (typeof generations)[number]["pads"],
  name: Name,
): Promise<FixtureModel> => {
  const { type, ...masks } = pads[name];
  return fixture(type, masks);
};

const placeOf = (callouts: readonly Callout[], control: string) => {
  const callout = callouts.find((c) => c.control === control);
  if (!callout) throw new Error(`No ${control}`);
  return callout;
};

describe.each(generations)("$name: the leaders", ({ pads }) => {
  it.each(placements)(
    "of %s never cross, never pass through a chip, and its chips stay apart",
    async (name) => {
      const built = await model(pads, name);
      const { points, width } = anchorsOf(built);
      const outline = built.layoutAnchors?.outline ?? [];
      const frame = {
        halfHeight: built.layoutAnchors?.halfHeight ?? 0,
        topWide: !built.layoutAnchors?.narrowTop,
      };
      expect(outline.length).toBeGreaterThan(20);
      expect(frame.halfHeight).toBeGreaterThan(0.8);
      const callouts = built.callouts;
      expect(callouts.length).toBeGreaterThan(5);
      for (const stage of stages)
        for (const notice of [false, true]) {
          const leaders = layoutLeaders(
            callouts,
            stage,
            points,
            width,
            notice,
            frame,
          );
          // Every callout has a leader: a chip and a segment from its edge to
          // the part.
          expect(leaders).toHaveLength(callouts.length);
          const hull = projectedHull(outline, width, stage);
          const problems = layoutProblems(leaders, stage, undefined, hull).map(
            (p) => `${p.kind} ${p.controls.join(" / ")}`,
          );
          expect(
            problems,
            `${name} on ${stage.width}x${stage.height}${notice ? " with the notice" : ""}`,
          ).toEqual([]);
        }
      built.dispose();
    },
  );

  it.each(placements)("of %s end on distinct anchors", async (name) => {
    const built = await model(pads, name);
    const { points } = anchorsOf(built);
    const seen = new Set<string>();
    for (const callout of built.callouts) {
      const p = points[callout.control];
      const key = [p.x, p.y, p.z].map((v) => v.toFixed(3)).join(",");
      expect(seen.has(key), callout.control).toBe(false);
      seen.add(key);
    }
    built.dispose();
  });
});

/**
 * The silhouette of a built model on a stage, from every vertex of its shell
 * and the scene's own camera (not from the outline the layout keeps).
 */
function silhouette(built: FixtureModel, stage: Stage): Point[] {
  const markers = new Set<unknown>(
    [...built.controls.values()].map((c) => c.marker),
  );
  const width = new Box3().setFromObject(built.group).getSize(new Vector3()).x;
  const camera = new PerspectiveCamera(cameraSpec.fov, 1, 0.05, 100);
  camera.aspect = stage.width / stage.height;
  camera.position.set(0, 0, cameraDistance(width, camera.aspect));
  camera.lookAt(0, 0, 0);
  camera.updateProjectionMatrix();
  camera.updateMatrixWorld(true);
  built.group.updateMatrixWorld(true);
  const points: Point[] = [];
  const v = new Vector3();
  built.group.traverse((node) => {
    if (!(node instanceof Mesh) || markers.has(node)) return;
    const position = node.geometry.getAttribute("position");
    for (let i = 0; i < position.count; i++) {
      v.fromBufferAttribute(position, i).applyMatrix4(node.matrixWorld);
      v.project(camera);
      points.push({
        x: ((v.x + 1) * stage.width) / 2,
        y: ((1 - v.y) * stage.height) / 2,
      });
    }
  });
  return convexHull(points);
}

describe.each(generations)("$name: the model", ({ pads }) => {
  const some: Stage[] = [
    { width: 640, height: 640 },
    { width: 440, height: 440 },
    { width: 1100, height: 640 },
    { width: 1180, height: 800 },
    { width: 860, height: 600 },
    { width: 600, height: 900 },
    { width: 1800, height: 500 },
  ];

  it.each(placements)(
    "is never covered by a chip of %s, on its whole silhouette",
    async (name) => {
      const built = await model(pads, name);
      const { points, width } = anchorsOf(built);
      const frame = {
        halfHeight: built.layoutAnchors?.halfHeight ?? 0,
        topWide: !built.layoutAnchors?.narrowTop,
      };
      for (const stage of [
        ...some,
        ...supportedStages.filter((_, i) => i % 7 === 0),
      ])
        for (const notice of [false, true]) {
          const hull = silhouette(built, stage);
          const covering = layoutLeaders(
            built.callouts,
            stage,
            points,
            width,
            notice,
            frame,
          )
            .filter((leader) => rectHitsPolygon(leader.rect, hull))
            .map((leader) => leader.control);
          expect(
            covering,
            `${name} on ${stage.width}x${stage.height}${notice ? " with the notice" : ""}`,
          ).toEqual([]);
        }
      built.dispose();
    },
  );

  it("has an outline that holds the whole silhouette", async () => {
    const built = await model(pads, "joyConPair");
    const outline = built.layoutAnchors?.outline ?? [];
    const width = built.layoutAnchors?.width ?? 0;
    for (const stage of some) {
      const kept = projectedHull(outline, width, stage);
      const full = silhouette(built, stage);
      // The hull of the full silhouette lies within the kept one, give or
      // take a pixel.
      for (const p of full)
        expect(
          rectHitsPolygon(
            { left: p.x - 1, top: p.y - 1, width: 2, height: 2 },
            kept,
          ),
          `${stage.width}x${stage.height} ${p.x},${p.y}`,
        ).toBe(true);
    }
    built.dispose();
  });
});

describe.each(generations)("$name: the rows", ({ pads }) => {
  it.each(placements)(
    "of %s are wide enough for a two-key shortcut where the stage has room",
    async (name) => {
      const built = await model(pads, name);
      const { points, width } = anchorsOf(built);
      const frame = {
        halfHeight: built.layoutAnchors?.halfHeight ?? 0,
        topWide: !built.layoutAnchors?.narrowTop,
      };
      for (const stage of [
        { width: 1100, height: 640 },
        { width: 1180, height: 800 },
        { width: 1400, height: 900 },
      ]) {
        const leaders = layoutLeaders(
          built.callouts,
          stage,
          points,
          width,
          false,
          frame,
        );
        for (const leader of leaders) {
          const callout = placeOf(built.callouts, leader.control);
          const inRow =
            callout.placement === "bottom" ||
            (callout.placement === "top" && !callout.side);
          // "Ctrl C" and its badge: the narrowest chip of the full density
          // before was about 100 px at this size.
          if (inRow)
            expect(
              leader.rect.width,
              `${name} ${leader.control} on ${stage.width}x${stage.height}`,
            ).toBeGreaterThanOrEqual(120);
        }
      }
      built.dispose();
    },
  );
});

describe.each(generations)("$name: the places of the chips", ({ pads }) => {
  it("put the shoulder buttons of a Joy-Con held sideways at the top, and its four buttons below", async () => {
    // L and ZL are paddle2 and paddle4 on the left one, R and ZR paddle1 and
    // paddle3 on the right one.
    for (const [name, shoulders, side] of [
      ["joyConLeftSideways", ["paddle2", "paddle4"], "left"],
      ["joyConRightSideways", ["paddle1", "paddle3"], "right"],
    ] as const) {
      const built = await model(pads, name);
      for (const control of shoulders) {
        const callout = placeOf(built.callouts, control);
        expect(callout.placement, `${name} ${control}`).toBe("top");
        expect(callout.side, `${name} ${control}`).toBe(side);
      }
      // The face buttons (the d-pad's four on the left one) go below.
      for (const control of ["south", "east", "west", "north"])
        expect(placeOf(built.callouts, control).placement, control).toBe(
          "bottom",
        );
      // SL and SR sit on the rail, which is on top.
      for (const control of ["leftshoulder", "rightshoulder"]) {
        const callout = placeOf(built.callouts, control);
        expect(callout.placement, control).toBe("top");
        expect(callout.side, control).toBeUndefined();
      }
      // The stick is in a column.
      expect(placeOf(built.callouts, "leftstick").placement).toMatch(
        /^(left|right)$/,
      );
      built.dispose();
    }
  });

  it("leave SL and SR of both halves off the pair", async () => {
    const built = await model(pads, "joyConPair");
    const controls = built.callouts.map((c) => c.control);
    for (const control of ["paddle1", "paddle2", "paddle3", "paddle4"]) {
      expect(controls, control).not.toContain(control);
      expect(built.controls.has(control), control).toBe(false);
    }
    // Everything else of the pair is there, the two sticks among them.
    expect(controls).toEqual(
      expect.arrayContaining(["leftstick", "rightstick", "misc1", "start"]),
    );
    built.dispose();
  });

  it("keep SL and SR of a single Joy-Con held upright, on the side of its rail", async () => {
    // The rail of the left one is on the right of the picture.
    const left = await model(pads, "joyConLeftUpright");
    for (const control of ["paddle2", "paddle4"])
      expect(placeOf(left.callouts, control).placement, control).toMatch(
        /^(right|bottom)$/,
      );
    left.dispose();
    const right = await model(pads, "joyConRightUpright");
    for (const control of ["paddle1", "paddle3"])
      expect(placeOf(right.callouts, control).placement, control).toMatch(
        /^(left|bottom)$/,
      );
    right.dispose();
  });
});

describe("the chips before the model is known", () => {
  it.each(placements)(
    "of %s sit on the stage without covering each other",
    (name) => {
      const callouts = controllerCallouts(sdlPads[name]);
      const points = Object.fromEntries(
        callouts.map((c) => [c.control, { x: 0, y: 0, z: 0 }]),
      );
      for (const stage of stages) {
        const leaders = layoutLeaders(
          callouts,
          stage,
          points,
          5.6,
          false,
          defaultFrame,
        );
        const bad = layoutProblems(leaders, stage).filter(
          (p) => p.kind === "overlap" || p.kind === "outside",
        );
        expect(bad, `${name} ${stage.width}x${stage.height}`).toEqual([]);
      }
    },
  );

  it("keep the table order of the units", () => {
    const callouts = controllerCallouts(sdlPads.joyConRightSideways);
    expect(callouts.map((c) => c.control)).toEqual([
      "leftshoulder",
      "rightshoulder",
      "start",
      "paddle3",
      "paddle1",
      "leftstick",
      "west",
      "north",
      "south",
      "east",
    ]);
  });
});

describe("arranging the chips for anchors", () => {
  it("swaps two chips of a row whose parts are the other way round", () => {
    const callouts = staticLayout(
      controllerCallouts(sdlPads.joyConRightSideways),
      "right-sideways",
    );
    const row = (list: readonly Callout[]) =>
      list
        .filter((c) => c.placement === "top" && !c.side)
        .sort((a, b) => (a.offset ?? 0) - (b.offset ?? 0))
        .map((c) => c.control);
    expect(row(callouts)).toEqual(["leftshoulder", "rightshoulder", "start"]);
    // A made-up model, 5.6 wide, in which SL is on the right of SR and +.
    const at = (x: number, y: number) => ({ x, y, z: 0.4 });
    const swapped = arrangeJoyCon(callouts, "right-sideways", {
      width: 5.6,
      points: {
        leftshoulder: at(1.8, 0.9),
        rightshoulder: at(0.2, 0.9),
        start: at(-1.2, 0.9),
        paddle3: at(2.3, -0.1),
        paddle1: at(2.4, -0.2),
        leftstick: at(-0.2, 0),
        west: at(0.9, 0),
        north: at(1.3, 0.4),
        south: at(1.3, -0.4),
        east: at(1.8, 0),
      },
    });
    expect(row(swapped)).toEqual(["start", "rightshoulder", "leftshoulder"]);
  });

  it("returns the callouts of a pad the table does not cover as they are", () => {
    // A Joy-Con whose buttons are not known yet has the standard ones.
    const callouts = controllerCallouts({ type: "joyconleft" });
    expect(joyConKind({ type: "joyconleft" })).toBe("left-upright");
    const points = Object.fromEntries(
      callouts.map((c) => [c.control, { x: 0, y: 0, z: 0 }]),
    );
    expect(
      arrangeJoyCon(callouts, "left-upright", { width: 1, points }),
    ).toEqual(callouts);
    expect(staticLayout(callouts, "left-upright")).toEqual(callouts);
  });
});

describe("telling how a Joy-Con is shown", () => {
  it("is by its side and the grip SDL reports", () => {
    expect(joyConKind(sdlPads.joyConLeftSideways)).toBe("left-sideways");
    expect(joyConKind(sdlPads.joyConRightSideways)).toBe("right-sideways");
    expect(joyConKind(sdlPads.joyConLeftUpright)).toBe("left-upright");
    expect(joyConKind(sdlPads.joyConRightUpright)).toBe("right-upright");
    expect(joyConKind(sdlPads.joyConPair)).toBe("pair");
    expect(joyConKind(joyCon2Pads.joyConPair)).toBe("pair");
    expect(joyConKind(sdlPads.xboxSeries)).toBeUndefined();
    expect(joyConKind(sdlPads.switchPro)).toBeUndefined();
  });

  it("hides SL and SR of a pair only", () => {
    for (const control of ["paddle1", "paddle2", "paddle3", "paddle4"]) {
      expect(diagramHidden("joyconpair", control)).toBe(true);
      expect(diagramHidden("joyconleft", control)).toBe(false);
      expect(diagramHidden("joyconright", control)).toBe(false);
      expect(diagramHidden("ps5", control)).toBe(false);
    }
    expect(diagramHidden("joyconpair", "misc1")).toBe(false);
    expect(diagramHidden("joyconpair", "leftshoulder")).toBe(false);
  });
});

describe("the arrangement in another language", () => {
  it("takes the places of the model's callouts and the labels of its own", () => {
    const info = joyCon2Pads.joyConRightSideways;
    const english = controllerCallouts(info, "en");
    const arranged: Callout[] = controllerCallouts(info, "zh-CN").map((c) => ({
      ...c,
      placement: "bottom",
      offset: 7,
    }));
    const merged = withArrangement(english, arranged);
    expect(merged.map((c) => c.control).sort()).toEqual(
      english.map((c) => c.control).sort(),
    );
    for (const callout of merged) {
      expect(callout.placement).toBe("bottom");
      expect(callout.offset).toBe(7);
      expect(callout.label).toBe(
        english.find((c) => c.control === callout.control)?.label,
      );
    }
    // A callout the arrangement lacks keeps its own place.
    const partial = withArrangement(english, arranged.slice(1));
    expect(partial).toHaveLength(english.length);
    expect(partial.find((c) => c.control === arranged[0].control)).toEqual(
      english.find((c) => c.control === arranged[0].control),
    );
  });
});
