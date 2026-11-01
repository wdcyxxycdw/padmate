import { Box3, Mesh, Quaternion, Raycaster, Vector3 } from "three";
import { describe, expect, it } from "vitest";
import {
  directions,
  modelFixture as fixture,
  type FixtureModel as Model,
  parseGlb,
  push,
  stickOf,
} from "../testing/controller-assets";
import { joyCon2Pads, sdlPads } from "../testing/sdl-pads";
import { joyConMaxHeight } from "./controller-joycon";

type JoyConName =
  | "joyConLeftSideways"
  | "joyConRightSideways"
  | "joyConLeftUpright"
  | "joyConRightUpright"
  | "joyConPair";
const layouts: JoyConName[] = [
  "joyConLeftSideways",
  "joyConRightSideways",
  "joyConLeftUpright",
  "joyConRightUpright",
  "joyConPair",
];
/**
 * The two generations the shells tests run for: what each pad is told by
 * (the Joy-Con 2's USB ids), where the cap of its stick begins in the stick
 * mesh, and how deep its cap sits over its pivot (normalized units, at least).
 */
const generations = [
  {
    name: "Switch OLED Joy-Con",
    pads: sdlPads as Record<JoyConName, PadInfo>,
    ids: {},
    isCap: (z: number) => z >= 0.0109,
    minDepth: 0.15,
    minMove: 0.07,
  },
  {
    name: "Joy-Con 2",
    pads: joyCon2Pads as Record<JoyConName, PadInfo>,
    ids: { vendor: 0x057e, product: 0x2066 },
    isCap: () => true,
    minDepth: 0.1,
    minMove: 0.05,
  },
];
type PadInfo = {
  type: string;
  buttonMask: number;
  axisMask: number;
  vendor?: number;
  product?: number;
};
/** The shell's own meshes: the markers are not the model. */
function outline(model: Model) {
  const markers = new Set<unknown>(
    [...model.controls.values()].map((c) => c.marker),
  );
  const box = new Box3();
  model.group.updateMatrixWorld(true);
  model.group.traverse((node) => {
    if (node instanceof Mesh && !markers.has(node)) box.expandByObject(node);
  });
  return box;
}
const pointOf = (model: Model, control: string) => {
  const point = model.controls.get(control)?.point;
  if (!point) throw new Error(`Missing ${control}`);
  return point;
};

describe.each(generations)("$name shells", (generation) => {
  const joyCon = (name: JoyConName) => {
    const { type, ...masks } = generation.pads[name];
    return fixture(type, masks);
  };
  it("are as tall as the Xbox shell at most", async () => {
    const size = new Box3()
      .setFromObject(await parseGlb("xbox"))
      .getSize(new Vector3());
    expect(joyConMaxHeight).toBeCloseTo((size.y * 5.6) / size.x, 2);
  });

  it.each(layouts)(
    "anchors every chip of %s inside the model",
    async (name) => {
      const model = await joyCon(name);
      const mapped = [...model.controls.keys()].filter((c) => c !== "guide");
      expect(new Set(model.callouts.map((c) => c.control))).toEqual(
        new Set(mapped),
      );
      expect(model.callouts).toHaveLength(mapped.length);
      const box = outline(model).expandByScalar(1e-6);
      for (const [control, c] of model.controls)
        expect(box.containsPoint(c.point), control).toBe(true);
      // Nothing is hidden: the letters and arrows are in the texture.
      model.group.traverse((node) => expect(node.visible).toBe(true));
      model.dispose();
    },
  );

  it.each(layouts)("fits the box of %s", async (name) => {
    const model = await joyCon(name);
    const box = outline(model);
    const size = box.getSize(new Vector3());
    const center = box.getCenter(new Vector3());
    expect(size.x).toBeLessThanOrEqual(5.6 + 1e-6);
    expect(size.y).toBeLessThanOrEqual(joyConMaxHeight + 1e-6);
    // The larger side fills the box.
    expect(Math.max(size.x / 5.6, size.y / joyConMaxHeight)).toBeCloseTo(1, 6);
    expect(Math.hypot(center.x, center.y)).toBeLessThan(1e-6);
    model.dispose();
  });

  it("turns a Joy-Con held sideways and stands one upright otherwise", async () => {
    const sizes = new Map<string, Vector3>();
    for (const name of layouts) {
      const model = await joyCon(name);
      sizes.set(name, outline(model).getSize(new Vector3()));
      model.dispose();
    }
    for (const name of ["joyConLeftSideways", "joyConRightSideways"]) {
      const size = sizes.get(name) as Vector3;
      expect(size.x, name).toBeCloseTo(5.6, 6);
      expect(size.y, name).toBeLessThan(2.1);
    }
    for (const name of ["joyConLeftUpright", "joyConRightUpright"]) {
      const size = sizes.get(name) as Vector3;
      expect(size.y, name).toBeCloseTo(joyConMaxHeight, 6);
      expect(size.x, name).toBeLessThan(1.6);
    }
    // The pair is two upright Joy-Cons and a gap of about a quarter of one.
    const pair = sizes.get("joyConPair") as Vector3;
    const one = sizes.get("joyConLeftUpright") as Vector3;
    expect(pair.y).toBeCloseTo(one.y, 6);
    expect(pair.x / one.x).toBeGreaterThan(2.15);
    expect(pair.x / one.x).toBeLessThan(2.35);
  });

  it.each(["joyconleft", "joyconright"])(
    "keeps a %s whose buttons are not known yet upright",
    async (type) => {
      const model = await fixture(type, {
        ...generation.ids,
        product:
          type === "joyconleft" ? 0x2067 : (generation.ids.product ?? 0x2007),
      });
      const size = outline(model).getSize(new Vector3());
      expect(size.y).toBeGreaterThan(size.x);
      // The stick end is the top of the left one; the right one's stick is
      // below its face buttons.
      if (type === "joyconleft")
        expect(pointOf(model, "leftstick").y).toBeGreaterThan(
          pointOf(model, "dpad.up").y,
        );
      else
        expect(pointOf(model, "rightstick").y).toBeLessThan(
          pointOf(model, "south").y,
        );
      model.dispose();
    },
  );

  it("lays a left Joy-Con held sideways with the stick on the left and the rail on top", async () => {
    const model = await joyCon("joyConLeftSideways");
    const p = (control: string) => pointOf(model, control);
    const faces = ["south", "east", "west", "north"].map(p);
    for (const face of faces) expect(p("leftstick").x).toBeLessThan(face.x);
    // The D-pad's four buttons, turned: down is east, left is south.
    expect(p("west").x).toBeLessThan(p("south").x);
    expect(p("south").x).toBeCloseTo(p("north").x, 1);
    expect(p("south").x).toBeLessThan(p("east").x);
    expect(p("south").y).toBeLessThan(p("west").y);
    expect(p("west").y).toBeCloseTo(p("east").y, 1);
    expect(p("west").y).toBeLessThan(p("north").y);
    // SL and SR along the rail, on top, SL nearer the stick.
    expect(p("leftshoulder").x).toBeLessThan(p("rightshoulder").x);
    for (const rail of [p("leftshoulder"), p("rightshoulder")])
      for (const face of faces) expect(rail.y).toBeGreaterThan(face.y);
    // L and ZL at the stick end, Capture at the other.
    expect(p("paddle2").x).toBeLessThan(p("leftstick").x);
    expect(p("paddle4").x).toBeLessThan(p("leftstick").x);
    expect(p("guide").x).toBeGreaterThan(p("leftstick").x);
    model.dispose();
  });

  it("lays a right Joy-Con held sideways with the stick on the left and the rail on top", async () => {
    const model = await joyCon("joyConRightSideways");
    const p = (control: string) => pointOf(model, control);
    const faces = ["south", "east", "west", "north"].map(p);
    for (const face of faces) expect(p("leftstick").x).toBeLessThan(face.x);
    // A is south, X east, B west, Y north.
    expect(p("west").x).toBeLessThan(p("south").x);
    expect(p("south").x).toBeCloseTo(p("north").x, 1);
    expect(p("south").x).toBeLessThan(p("east").x);
    expect(p("south").y).toBeLessThan(p("west").y);
    expect(p("west").y).toBeCloseTo(p("east").y, 1);
    expect(p("west").y).toBeLessThan(p("north").y);
    expect(p("leftshoulder").x).toBeLessThan(p("rightshoulder").x);
    for (const rail of [p("leftshoulder"), p("rightshoulder")])
      for (const face of faces) expect(rail.y).toBeGreaterThan(face.y);
    // R and ZR at the face end, on the right; Home at the stick end.
    expect(p("paddle1").x).toBeGreaterThan(p("north").x);
    expect(p("paddle3").x).toBeGreaterThan(p("north").x);
    expect(p("start").x).toBeGreaterThan(p("north").x);
    expect(p("guide").x).toBeLessThan(p("south").x);
    model.dispose();
  });

  it("lays an upright left Joy-Con with the D-pad below the stick and L above it", async () => {
    const model = await joyCon("joyConLeftUpright");
    const p = (control: string) => pointOf(model, control);
    for (const d of ["up", "down", "left", "right"])
      expect(p(`dpad.${d}`).y, d).toBeLessThan(p("leftstick").y);
    expect(p("dpad.up").y).toBeGreaterThan(p("dpad.down").y);
    expect(p("dpad.left").x).toBeLessThan(p("dpad.right").x);
    expect(p("dpad.left").y).toBeCloseTo(p("dpad.right").y, 1);
    expect(p("leftshoulder").y).toBeGreaterThan(p("leftstick").y);
    expect(p("lefttrigger").y).toBeGreaterThan(p("leftstick").y);
    expect(p("back").y).toBeGreaterThan(p("leftstick").y);
    expect(p("misc1").y).toBeLessThan(p("dpad.down").y);
    // SL above SR on the rail, which is on the right.
    expect(p("paddle2").y).toBeGreaterThan(p("paddle4").y);
    expect(p("paddle2").x).toBeGreaterThan(p("dpad.right").x);
    model.dispose();
  });

  it("lays an upright right Joy-Con with X above B and the stick below the face buttons", async () => {
    const model = await joyCon("joyConRightUpright");
    const p = (control: string) => pointOf(model, control);
    expect(p("north").y).toBeGreaterThan(p("south").y);
    expect(p("west").x).toBeLessThan(p("east").x);
    expect(p("west").y).toBeCloseTo(p("east").y, 1);
    for (const c of ["south", "east", "west", "north"])
      expect(p("rightstick").y, c).toBeLessThan(p(c).y);
    expect(p("guide").y).toBeLessThan(p("rightstick").y);
    expect(p("rightshoulder").y).toBeGreaterThan(p("north").y);
    expect(p("righttrigger").y).toBeGreaterThan(p("north").y);
    expect(p("start").y).toBeGreaterThan(p("north").y);
    // SR above SL on the rail, which is on the left.
    expect(p("paddle1").y).toBeGreaterThan(p("paddle3").y);
    expect(p("paddle1").x).toBeLessThan(p("west").x);
    model.dispose();
  });

  it("puts the left Joy-Con's controls at -x and the right one's at +x in a pair", async () => {
    const model = await joyCon("joyConPair");
    const p = (control: string) => pointOf(model, control);
    for (const c of [
      "dpad.up",
      "dpad.down",
      "dpad.left",
      "dpad.right",
      "leftstick",
      "leftshoulder",
      "lefttrigger",
      "back",
      "misc1",
      "paddle2",
      "paddle4",
    ])
      expect(p(c).x, c).toBeLessThan(0);
    for (const c of [
      "south",
      "east",
      "west",
      "north",
      "rightstick",
      "rightshoulder",
      "righttrigger",
      "start",
      "guide",
      "paddle1",
      "paddle3",
    ])
      expect(p(c).x, c).toBeGreaterThan(0);
    // Each hand as on the single one.
    expect(p("leftstick").y).toBeGreaterThan(p("dpad.up").y);
    expect(p("north").y).toBeGreaterThan(p("south").y);
    expect(p("rightstick").y).toBeLessThan(p("south").y);
    expect(p("leftshoulder").y).toBeGreaterThan(p("leftstick").y);
    expect(p("rightshoulder").y).toBeGreaterThan(p("north").y);
    model.dispose();
  });

  it.each([
    ["joyConLeftUpright", ["left"]],
    ["joyConRightUpright", ["right"]],
    ["joyConLeftSideways", ["left"]],
    ["joyConRightSideways", ["left"]],
    ["joyConPair", ["left", "right"]],
  ] as const)(
    "tilts the sticks of %s on input and resets them",
    async (name, sides) => {
      const model = await joyCon(name);
      expect([...model.sticks.keys()].sort()).toEqual([...sides].sort());
      for (const side of sides) {
        const { control, stick } = stickOf(model, side);
        const rest = model.anchor(control);
        expect(stick.maxTilt).toBeGreaterThan(0.1);
        // The pivot is under the cap on the axis the stick stands on, +Z: the
        // layouts only turn a Joy-Con about Z.
        const frame = stick.pivot.parent;
        if (!frame) throw new Error("Missing stick frame");
        const axis = new Vector3(0, 0, 1).applyQuaternion(
          frame.getWorldQuaternion(new Quaternion()),
        );
        expect(axis.angleTo(new Vector3(0, 0, 1))).toBeLessThan(1e-6);
        const offset = rest.clone().sub(frame.getWorldPosition(new Vector3()));
        expect(offset.z).toBeGreaterThan(generation.minDepth);
        expect(Math.hypot(offset.x, offset.y)).toBeLessThan(0.1);
        // The pressed ring rides on the cap.
        expect(control.marker.parent).toBe(stick.pivot);
        for (const [x, y] of directions) {
          model.update(push(side, x, y));
          const moved = model.anchor(control).sub(rest);
          expect(Math.hypot(moved.x, moved.y)).toBeGreaterThan(
            generation.minMove,
          );
          // +x is right; SDL's +y is down, which is -Y in the model.
          if (x) expect(Math.sign(moved.x)).toBe(Math.sign(x));
          if (y) expect(Math.sign(moved.y)).toBe(-Math.sign(y));
          expect(stick.pivot.quaternion.z).toBeCloseTo(0, 9);
          const angle = 2 * Math.acos(Math.min(1, stick.pivot.quaternion.w));
          expect(angle).toBeCloseTo(stick.maxTilt, 6);
        }
        model.update();
        expect(stick.pivot.quaternion.equals(new Quaternion())).toBe(true);
        expect(model.anchor(control).distanceTo(rest)).toBeLessThan(1e-9);
        expect(control.strength).toBe(0);
        // The gauge's radius is the cap's: 8 mm of a Joy-Con of 109.
        expect(stick.radius).toBeGreaterThan(0.25);
        expect(stick.radius).toBeLessThan(0.45);
      }
      model.dispose();
    },
  );

  it.each(["joyConLeftUpright", "joyConRightUpright"] as const)(
    "keeps the cap of %s above the shell at the full tilt in every direction",
    async (name) => {
      const model = await joyCon(name);
      const side = name === "joyConLeftUpright" ? "left" : "right";
      const { control, stick } = stickOf(model, side);
      const shell = model.group.getObjectByName("shell");
      const cap = stick.pivot.children.find(
        (child): child is Mesh =>
          child instanceof Mesh && child !== control.marker,
      );
      if (!shell || !cap) throw new Error("Missing shell or stick");
      // The shell's front as a height map around the stick, 0.04 apart (about
      // a millimetre), read by looking down at it.
      const step = 0.04;
      const reach = 14;
      const centre = control.point;
      const ray = new Raycaster();
      const height = new Map<string, number>();
      for (let i = -reach; i <= reach; i++)
        for (let j = -reach; j <= reach; j++) {
          ray.set(
            new Vector3(centre.x + i * step, centre.y + j * step, 5),
            new Vector3(0, 0, -1),
          );
          const hit = ray.intersectObject(shell, true)[0];
          if (hit) height.set(`${i},${j}`, hit.point.z);
        }
      // The highest shell under a point: the four nodes around it.
      const shellAt = (x: number, y: number) => {
        const i = (x - centre.x) / step;
        const j = (y - centre.y) / step;
        let top = -Infinity;
        for (const a of [Math.floor(i), Math.ceil(i)])
          for (const b of [Math.floor(j), Math.ceil(j)])
            top = Math.max(top, height.get(`${a},${b}`) ?? -Infinity);
        return top;
      };
      const position = cap.geometry.attributes.position;
      // The cap is the part of the stick above its stem (z 11 mm and up on a
      // Switch OLED Joy-Con; a Joy-Con 2's stick is the cap alone).
      const capVertices: number[] = [];
      for (let i = 0; i < position.count; i++)
        if (generation.isCap(position.getZ(i))) capVertices.push(i);
      expect(capVertices.length).toBeGreaterThan(100);
      const world = new Vector3();
      for (const [x, y] of directions) {
        model.update(push(side, x, y));
        model.group.updateMatrixWorld(true);
        let lowest = Infinity;
        for (const i of capVertices) {
          world.fromBufferAttribute(position, i).applyMatrix4(cap.matrixWorld);
          lowest = Math.min(lowest, world.z - shellAt(world.x, world.y));
        }
        expect(lowest, `${x},${y}`).toBeGreaterThan(0);
      }
      model.dispose();
    },
  );
});
