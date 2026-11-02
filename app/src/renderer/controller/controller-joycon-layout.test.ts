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
  calloutEdge,
  calloutSide,
  cameraDistance,
  cameraSpec,
  convexHull,
  defaultFrame,
  layoutLeaders,
  layoutProblems,
  leaderEnd,
  type Point,
  type Point3,
  projectedHull,
  projectPoint,
  rectHitsPolygon,
  type Stage,
  supportedStages,
} from "./controller-layout";

type Name = keyof typeof sdlPads & keyof typeof joyCon2Pads;
const placements = [
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

/**
 * The stages a Joy-Con's layout is not clean on, which no choice of the plan's
 * room and the columns' places was found to clear (the design's orders and
 * routes are fixed): tall stages that are not wide, where the model is much
 * taller than the chips' columns (the mini and compact densities, 440 to 859
 * wide and 800 to 900 tall). The test below fails if a stage listed here is
 * clean, too, so the list only shrinks.
 */
const knownProblems: Record<
  string,
  Partial<Record<(typeof placements)[number], string[]>>
> = {
  "Switch 1 (OLED model)": {
    joyConRightUpright: ["440x900", "520x900", "599x900"],
    joyConPair: ["720x800", "720x900", "859x800", "859x900"],
  },
  "Switch 2 (NAIT model)": {
    joyConRightUpright: ["440x900", "520x900", "599x900"],
    joyConPair: ["859x800", "859x900"],
  },
};

/** The layout of a built Joy-Con model: its leaders on a stage, and what it needs. */
function layoutOf(built: FixtureModel) {
  const { points, width } = anchorsOf(built);
  const outline = built.layoutAnchors?.outline ?? [];
  const frame = {
    halfHeight: built.layoutAnchors?.halfHeight ?? 0,
    topWide: !built.layoutAnchors?.narrowTop,
  };
  return {
    points,
    width,
    outline,
    frame,
    leaders: (stage: Stage, notice = false) =>
      layoutLeaders(built.callouts, stage, points, width, notice, frame),
  };
}

describe.each(generations)(
  "$name: the leaders",
  ({ name: generation, pads }) => {
    it.each(placements)(
      "of %s never cross, never pass through a chip, and its chips stay apart, but on the stages listed",
      async (name) => {
        const built = await model(pads, name);
        const { outline, frame, width, leaders } = layoutOf(built);
        expect(outline.length).toBeGreaterThan(20);
        expect(frame.halfHeight).toBeGreaterThan(0.8);
        expect(built.callouts.length).toBeGreaterThan(5);
        const failing = new Map<string, string[]>();
        for (const stage of stages) {
          const list = leaders(stage);
          // Every callout has a leader: a chip and a path from its edge to the
          // part.
          expect(list).toHaveLength(built.callouts.length);
          const hull = projectedHull(outline, width, stage);
          const problems = layoutProblems(list, stage, undefined, hull).map(
            (p) => `${p.kind} ${p.controls.join(" / ")}`,
          );
          if (problems.length)
            failing.set(`${stage.width}x${stage.height}`, problems);
        }
        const known = knownProblems[generation]?.[name] ?? [];
        // What is wrong on a stage that is not listed is a failure, said in full.
        expect(
          [...failing].filter(([stage]) => !known.includes(stage)),
          `${name} on stages not listed`,
        ).toEqual([]);
        expect([...failing.keys()].sort(), `${name}: stages listed`).toEqual(
          [...known].sort(),
        );
        built.dispose();
      },
    );

    it.each(placements)(
      "of %s are not moved by the editor notice",
      async (name) => {
        // A Joy-Con has no row below to lift.
        const built = await model(pads, name);
        const { leaders } = layoutOf(built);
        const stage = { width: 1100, height: 640 };
        expect(leaders(stage, true)).toEqual(leaders(stage, false));
        built.dispose();
      },
    );

    it.each(placements)(
      "of %s leave the chip's edge along its line, and end on the part",
      async (name) => {
        const built = await model(pads, name);
        const { points, width, leaders } = layoutOf(built);
        const stage = { width: 1100, height: 640 };
        for (const leader of leaders(stage)) {
          const callout = placeOf(built.callouts, leader.control);
          const start = calloutEdge(callout, leader.rect);
          expect(leader.path[0], leader.control).toEqual(start);
          const end = projectPoint(points[leader.control], width, stage);
          expect(leaderEnd(leader).x, leader.control).toBeCloseTo(end.x, 6);
          expect(leaderEnd(leader).y, leader.control).toBeCloseTo(end.y, 6);
          // Straight, one bend, or two where a route through a gap goes a stub
          // first; the first run is along the chip's own line.
          expect(leader.path.length, leader.control).toBeGreaterThanOrEqual(2);
          expect(leader.path.length, leader.control).toBeLessThanOrEqual(4);
          if (leader.path.length > 2) {
            const second = leader.path[1];
            if (calloutSide(callout))
              expect(second.y, leader.control).toBeCloseTo(start.y, 6);
            else expect(second.x, leader.control).toBeCloseTo(start.x, 6);
          }
          // A steep leader's last run is steeper than 45 degrees; an auto one
          // that bends is 45 degrees where it can, else a stub.
          if (leader.path.length === 3) {
            const [, bend, last] = leader.path;
            const run = Math.abs(last.x - bend.x);
            const rise = Math.abs(last.y - bend.y);
            if (callout.route === "steep")
              expect(rise, leader.control).toBeGreaterThan(run * 1.5);
            if (callout.route === "auto" && run > 13)
              expect(rise, leader.control).toBeCloseTo(run, 6);
          }
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
  },
);

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
  /** The controls of a region, top to bottom (a row: left to right). */
  const column = (callouts: readonly Callout[], placement: "left" | "right") =>
    callouts
      .filter((c) => c.placement === placement)
      .sort((a, b) => (a.slot ?? 0) - (b.slot ?? 0))
      .map((c) => c.control);
  const corner = (callouts: readonly Callout[], side: "left" | "right") =>
    callouts
      .filter((c) => c.placement === "top" && c.side === side)
      .sort((a, b) => (a.slot ?? 0) - (b.slot ?? 0))
      .map((c) => c.control);

  it("put the pair's shoulders in the corners, minus and plus above, its sticks and buttons in the columns", async () => {
    const built = await model(pads, "joyConPair");
    const { callouts } = built;
    expect(corner(callouts, "left")).toEqual(["leftshoulder", "lefttrigger"]);
    expect(corner(callouts, "right")).toEqual([
      "rightshoulder",
      "righttrigger",
    ]);
    expect(
      callouts
        .filter((c) => c.placement === "top" && !c.side)
        .sort((a, b) => (a.offset ?? 0) - (b.offset ?? 0))
        .map((c) => c.control),
    ).toEqual(["back", "start"]);
    expect(column(callouts, "left")).toEqual([
      "leftstick",
      "dpad.up",
      "dpad.left",
      "dpad.right",
      "dpad.down",
      "misc1",
    ]);
    expect(column(callouts, "right")).toEqual([
      "north",
      "east",
      "west",
      "south",
      "rightstick",
      ...(built.callouts.some((c) => c.control === "misc2") ? ["misc2"] : []),
    ]);
    expect(callouts.some((c) => c.placement === "bottom")).toBe(false);
    built.dispose();
  });

  it("put a single left Joy-Con's shoulder and D-pad on the left, its trigger, minus, SL, stick and SR on its rail's side", async () => {
    const built = await model(pads, "joyConLeftUpright");
    expect(column(built.callouts, "left")).toEqual([
      "leftshoulder",
      "dpad.up",
      "dpad.left",
      "dpad.right",
      "dpad.down",
      "misc1",
    ]);
    expect(column(built.callouts, "right")).toEqual([
      "lefttrigger",
      "back",
      "paddle2",
      "leftstick",
      "paddle4",
    ]);
    // There is nothing above or below the model.
    expect(
      built.callouts.every(
        (c) => c.placement === "left" || c.placement === "right",
      ),
    ).toBe(true);
    built.dispose();
  });

  it("put a single right Joy-Con's shoulder and four buttons on the right, its trigger, plus, SR, stick and SL on its rail's side", async () => {
    const built = await model(pads, "joyConRightUpright");
    expect(column(built.callouts, "right")).toEqual([
      "rightshoulder",
      "north",
      "east",
      "west",
      "south",
      ...(built.callouts.some((c) => c.control === "misc2") ? ["misc2"] : []),
    ]);
    expect(column(built.callouts, "left")).toEqual([
      "righttrigger",
      "start",
      "paddle1",
      "rightstick",
      "paddle3",
    ]);
    expect(
      built.callouts.every(
        (c) => c.placement === "left" || c.placement === "right",
      ),
    ).toBe(true);
    built.dispose();
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

  it("bend the leaders as the design has them", async () => {
    const route = (callouts: readonly Callout[], control: string) =>
      placeOf(callouts, control).route;
    const pair = await model(pads, "joyConPair");
    expect(route(pair.callouts, "leftshoulder")).toBe("steep");
    expect(route(pair.callouts, "rightshoulder")).toBe("steep");
    expect(route(pair.callouts, "lefttrigger")).toBe("auto");
    expect(route(pair.callouts, "back")).toBe("auto");
    expect(route(pair.callouts, "dpad.right")).toEqual({
      gap: ["dpad.left", "dpad.down"],
    });
    expect(route(pair.callouts, "west")).toEqual({ gap: ["east", "south"] });
    pair.dispose();
    const left = await model(pads, "joyConLeftUpright");
    expect(route(left.callouts, "dpad.right")).toEqual({
      gap: ["dpad.left", "dpad.down"],
    });
    expect(route(left.callouts, "leftshoulder")).toBe("auto");
    left.dispose();
    const right = await model(pads, "joyConRightUpright");
    expect(route(right.callouts, "west")).toEqual({ gap: ["east", "south"] });
    right.dispose();
  });

  it("keep SL and SR of a single Joy-Con on the side of its rail", async () => {
    // The rail of the left one is on the right of the picture.
    const left = await model(pads, "joyConLeftUpright");
    for (const control of ["paddle2", "paddle4"])
      expect(placeOf(left.callouts, control).placement, control).toBe("right");
    left.dispose();
    const right = await model(pads, "joyConRightUpright");
    for (const control of ["paddle1", "paddle3"])
      expect(placeOf(right.callouts, control).placement, control).toBe("left");
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

  it("keep the plan's order and room, and bend every leader", () => {
    const callouts = controllerCallouts(sdlPads.joyConRightUpright);
    expect(callouts).toHaveLength(10);
    const placed = staticLayout(callouts, "right-upright");
    expect(placed.map((c) => c.control)).toEqual([
      "righttrigger",
      "start",
      "paddle1",
      "rightstick",
      "paddle3",
      "rightshoulder",
      "north",
      "east",
      "west",
      "south",
    ]);
    for (const callout of placed)
      expect(callout.route, callout.control).toBeDefined();
    // The outer column starts at half a slot, as the design has it.
    const slots = (placement: string) =>
      placed
        .filter((c) => c.placement === placement)
        .map((c) => [c.control, c.slot]);
    expect(slots("right")).toEqual([
      ["rightshoulder", 0.5],
      ["north", 1.5],
      ["east", 2.5],
      ["west", 3.5],
      ["south", 4.5],
    ]);
  });
});

describe("arranging the chips for anchors", () => {
  it("puts the chips in the order of the plan whatever the anchors are", () => {
    const callouts = controllerCallouts(sdlPads.joyConLeftUpright);
    // A made-up model, 5.6 wide.
    const at = (x: number, y: number) => ({ x, y, z: 0.4 });
    const placed = arrangeJoyCon(callouts, "left-upright", {
      width: 5.6,
      points: {
        leftshoulder: at(-1, 1.6),
        lefttrigger: at(0.6, 1.9),
        back: at(0.9, 1.5),
        paddle2: at(1.2, 0.8),
        leftstick: at(-0.2, 0.9),
        paddle4: at(1.2, -1),
        "dpad.up": at(-0.4, 0),
        "dpad.left": at(-0.8, -0.4),
        "dpad.right": at(0, -0.4),
        "dpad.down": at(-0.4, -0.8),
        misc1: at(0.3, -1.4),
      },
    });
    const order = (placement: string) =>
      placed
        .filter((c) => c.placement === placement)
        .sort((a, b) => (a.slot ?? 0) - (b.slot ?? 0))
        .map((c) => c.control);
    expect(order("left")).toEqual([
      "leftshoulder",
      "dpad.up",
      "dpad.left",
      "dpad.right",
      "dpad.down",
      "misc1",
    ]);
    expect(order("right")).toEqual([
      "lefttrigger",
      "back",
      "paddle2",
      "leftstick",
      "paddle4",
    ]);
    // Chips of a column do not overlap: a slot apart (the stick takes two).
    for (const placement of ["left", "right"]) {
      const list = placed
        .filter((c) => c.placement === placement)
        .sort((a, b) => (a.slot ?? 0) - (b.slot ?? 0));
      for (const [index, chip] of list.slice(1).entries())
        expect(chip.slot ?? 0).toBeGreaterThanOrEqual(
          (list[index].slot ?? 0) + (list[index].span ?? 1),
        );
    }
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
  it("is by its side: a single Joy-Con is always upright", () => {
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
    const info = joyCon2Pads.joyConRightUpright;
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
