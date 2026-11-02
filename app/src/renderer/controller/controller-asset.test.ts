import { Box3, Mesh, Quaternion, Vector3 } from "three";
import { describe, expect, it, vi } from "vitest";
import {
  directions,
  modelFixture as fixture,
  type FixtureModel as Model,
  push,
  stickOf,
} from "../testing/controller-assets";
import { sdlPads } from "../testing/sdl-pads";
import { modelShell } from "./controller-models";

describe("licensed controller assets", () => {
  it.each(["xboxone", "ps5", "switchpro"])(
    "keeps standard input and trigger anchors on %s",
    async (type) => {
      const model = await fixture(type);
      expect(model.controls.has("south")).toBe(true);
      expect(model.controls.has("lefttrigger")).toBe(true);
      expect(model.controls.has("righttrigger")).toBe(true);
      expect(
        model.callouts
          .filter((c) => c.control.endsWith("trigger") && c.placement === "top")
          .map((c) => c.control),
      ).toEqual(["lefttrigger", "righttrigger"]);
      // Every control of the model has a chip but the Xbox, PS or Home
      // button, which padmate never maps (ADR-0012); the model still lights
      // it when it is pressed.
      expect(model.controls.has("guide")).toBe(true);
      const mapped = [...model.controls.keys()].filter((c) => c !== "guide");
      expect(new Set(model.callouts.map((c) => c.control))).toEqual(
        new Set(mapped),
      );
      expect(model.callouts).toHaveLength(mapped.length);
      for (const control of [
        "south",
        "east",
        "west",
        "north",
        "dpad.up",
        "dpad.down",
        "dpad.left",
        "dpad.right",
      ]) {
        expect(model.callouts.find((c) => c.control === control)).toBeDefined();
      }
      expect(model.controls.has("paddle1")).toBe(false);
      model.dispose();
    },
  );
  it.each([
    "ps4",
    "ps5Edge",
    "switchPro",
    "joyConPair",
    "joyConLeftUpright",
    "joyConRightUpright",
    "xboxElite",
  ] as const)("anchors every chip of %s on the model", async (name) => {
    const { type, ...masks } = sdlPads[name];
    const model = await fixture(type, masks);
    const mapped = [...model.controls.keys()].filter((c) => c !== "guide");
    expect(new Set(model.callouts.map((c) => c.control))).toEqual(
      new Set(mapped),
    );
    // On the shell, inside its outline.
    for (const control of mapped) {
      const point = model.controls.get(control)?.point;
      expect(Math.abs(point?.x ?? 9), control).toBeLessThan(2.8);
      expect(Math.abs(point?.y ?? 9), control).toBeLessThan(2.2);
    }
    model.dispose();
  });
  it("resets all input feedback on release and disposes each resource once", async () => {
    const model = await fixture("xboxone");
    const stick = model.sticks.get("left");
    expect(stick).toBeDefined();
    model.update({
      buttons: { south: true },
      axes: { leftx: 32767, lefttrigger: 32767 },
    });
    expect(model.controls.get("south")?.strength).toBe(1);
    expect(model.controls.get("lefttrigger")?.strength).toBe(1);
    expect(stick?.pivot.rotation.y).toBeGreaterThan(0);
    model.update();
    for (const c of model.controls.values()) expect(c.strength).toBe(0);
    expect(stick?.pivot.rotation.y).toBeCloseTo(0);
    const spies = [...model.resources].map((r) => vi.spyOn(r, "dispose"));
    model.dispose();
    model.dispose();
    for (const spy of spies) expect(spy).toHaveBeenCalledTimes(1);
  });
});

const sticks = ["xboxone", "ps5"].flatMap((type) =>
  ["left", "right"].map((side) => [type, side] as const),
);

describe("DualShock 4 shell", () => {
  async function ds4() {
    const { type, ...masks } = sdlPads.ps4;
    return fixture(type, masks);
  }
  const at = (model: Model, control: string) => {
    const point = model.controls.get(control)?.point;
    if (!point) throw new Error(`Missing ${control}`);
    return point;
  };

  it("anchors every control inside the model outline", async () => {
    const model = await ds4();
    const outline = new Box3().setFromObject(model.group.children[0]);
    outline.expandByScalar(1e-6);
    for (const [name, control] of model.controls)
      expect(outline.containsPoint(control.point), name).toBe(true);
    for (const name of ["touchpad", "back", "start", "guide"])
      expect(model.controls.has(name), name).toBe(true);
    model.dispose();
  });

  it("puts the controls where the pad has them", async () => {
    const model = await ds4();
    const p = (control: string) => at(model, control);
    // Face buttons: the cross at the bottom, the triangle at the top, the
    // square left and the circle right of the cluster, which is the right half.
    expect(p("south").y).toBeLessThan(p("west").y);
    expect(p("west").y).toBeCloseTo(p("east").y, 2);
    expect(p("west").y).toBeLessThan(p("north").y);
    expect(p("west").x).toBeLessThan(p("south").x);
    expect(p("south").x).toBeCloseTo(p("north").x, 2);
    expect(p("south").x).toBeLessThan(p("east").x);
    for (const c of ["south", "east", "west", "north"])
      expect(p(c).x, c).toBeGreaterThan(1);
    // The D-pad is the left half, up above down and left of right.
    for (const c of ["up", "down", "left", "right"])
      expect(p(`dpad.${c}`).x, c).toBeLessThan(-1);
    expect(p("dpad.up").y).toBeGreaterThan(p("dpad.down").y);
    expect(p("dpad.left").x).toBeLessThan(p("dpad.right").x);
    // The sticks side by side below the face parts, the PS button between.
    expect(p("leftstick").x).toBeLessThan(p("guide").x);
    expect(p("guide").x).toBeLessThan(p("rightstick").x);
    expect(p("leftstick").y).toBeCloseTo(p("rightstick").y, 2);
    expect(p("leftstick").y).toBeLessThan(p("dpad.down").y);
    expect(p("rightstick").y).toBeLessThan(p("south").y);
    // Share, the touchpad and Options in a row above the sticks.
    expect(p("back").x).toBeLessThan(p("touchpad").x);
    expect(p("touchpad").x).toBeLessThan(p("start").x);
    expect(p("touchpad").y).toBeGreaterThan(p("leftstick").y);
    expect(p("back").y).toBeGreaterThan(p("leftstick").y);
    expect(p("start").y).toBeGreaterThan(p("rightstick").y);
    // The bumpers and triggers on the top edge, the triggers above the bumpers.
    expect(p("leftshoulder").x).toBeLessThan(0);
    expect(p("rightshoulder").x).toBeGreaterThan(0);
    expect(p("lefttrigger").y).toBeGreaterThan(p("leftshoulder").y);
    expect(p("righttrigger").y).toBeGreaterThan(p("rightshoulder").y);
    model.dispose();
  });

  it("shows a stick's push on the gauge without tilting anything", async () => {
    const model = await ds4();
    for (const side of ["left", "right"]) {
      const { control, stick } = stickOf(model, side);
      expect(stick.maxTilt).toBe(0);
      expect(stick.radius).toBeGreaterThan(0.2);
      model.update(push(side, 1, 0));
      expect(stick.x).toBeCloseTo(1, 6);
      expect(stick.pivot.quaternion.equals(new Quaternion())).toBe(true);
      expect(model.anchor(control).distanceTo(control.point)).toBeLessThan(
        1e-9,
      );
      expect(control.marker.parent).toBe(model.group);
    }
    model.dispose();
  });

  it("serves PS3 pads with the same shell, without a touchpad", async () => {
    const four = await ds4();
    const three = await fixture("ps3");
    expect(modelShell("ps3")).toBe(modelShell("ps4"));
    expect(three.controls.has("touchpad")).toBe(false);
    for (const [name, control] of three.controls)
      expect(
        control.point.distanceTo(
          four.controls.get(name)?.point ?? new Vector3(9, 9, 9),
        ),
        name,
      ).toBeLessThan(1e-9);
    four.dispose();
    three.dispose();
  });
});

describe("stick motion", () => {
  it.each(sticks)(
    "pivots the %s %s stick on its own axis below the cap",
    async (type, side) => {
      const model = await fixture(type);
      const { control, stick } = stickOf(model, side);
      const frame = stick.pivot.parent;
      if (!frame) throw new Error("Missing stick frame");
      const pivot = frame.getWorldPosition(new Vector3());
      const axis = new Vector3(0, 0, 1)
        .applyQuaternion(frame.getWorldQuaternion(new Quaternion()))
        .normalize();
      // The frame's +Z is the stick mesh's own axis, which the Xbox shell
      // leans back by 17–25°, rather than the camera's.
      const mesh = stick.pivot.children.find(
        (child) => child instanceof Mesh && child !== control.marker,
      );
      if (!mesh) throw new Error("Missing stick mesh");
      const own = new Vector3()
        .setFromMatrixColumn(mesh.matrixWorld, 2)
        .normalize();
      expect(own.angleTo(axis)).toBeLessThan(1e-6);
      if (type === "xboxone") expect(axis.y).toBeGreaterThan(0.25);
      // At rest the leader still ends where it did: on the cap top.
      const cap = model.anchor(control);
      expect(cap.distanceTo(control.point)).toBeLessThan(1e-6);
      const offset = cap.clone().sub(pivot);
      const along = offset.dot(axis);
      expect(along).toBeGreaterThan(0.3);
      expect(offset.addScaledVector(axis, -along).length()).toBeLessThan(0.1);
      // The pressed ring rides on the cap.
      expect(control.marker.parent).toBe(stick.pivot);
      model.dispose();
    },
  );

  it.each(sticks)(
    "moves the %s %s cap clearly in every direction without twisting it",
    async (type, side) => {
      const model = await fixture(type);
      const { control, stick } = stickOf(model, side);
      const rest = model.anchor(control);
      const travels: number[] = [];
      for (const [x, y] of directions) {
        model.update(push(side, x, y));
        const moved = model.anchor(control).sub(rest);
        // The view is head-on, so the cap's travel on screen is its X/Y move.
        const travel = Math.hypot(moved.x, moved.y);
        travels.push(travel);
        // The old pivot moved the cap top 0.03–0.04 at most.
        expect(travel).toBeGreaterThan(0.07);
        // +x is right; SDL's +y is down, which is -Y in the model.
        if (x) expect(Math.sign(moved.x)).toBe(Math.sign(x));
        if (y) expect(Math.sign(moved.y)).toBe(-Math.sign(y));
        // The tilt axis lies across the stick: the cap never spins about it.
        expect(stick.pivot.quaternion.z).toBeCloseTo(0, 9);
        // Diagonals are pulled back to the rim: never more than the full tilt.
        const angle = 2 * Math.acos(Math.min(1, stick.pivot.quaternion.w));
        expect(angle).toBeCloseTo(stick.maxTilt, 6);
        expect(Math.hypot(stick.x, stick.y)).toBeCloseTo(1, 6);
      }
      // A clamped diagonal travels as far as a straight push.
      expect(Math.max(...travels) / Math.min(...travels)).toBeLessThan(1.25);
      // Releasing the stick puts everything back.
      model.update();
      expect(stick.pivot.quaternion.equals(new Quaternion())).toBe(true);
      expect(model.anchor(control).distanceTo(rest)).toBeLessThan(1e-9);
      expect([stick.x, stick.y]).toEqual([0, 0]);
      expect(control.strength).toBe(0);
      model.dispose();
    },
  );

  it("lights a stick that is pushed past the rest threshold", async () => {
    const model = await fixture("ps5");
    const left = model.controls.get("leftstick");
    const right = model.controls.get("rightstick");
    model.update({ buttons: {}, axes: { leftx: 2000, righty: -20000 } });
    // Drift stays dark; a real push lights the control and its ring.
    expect(left?.strength).toBe(0);
    expect(left?.marker.material.opacity).toBe(0);
    expect(right?.strength).toBeCloseTo(20000 / 32768, 6);
    expect(right?.marker.material.opacity).toBeGreaterThan(0);
    model.update({ buttons: { leftstick: true }, axes: {} });
    expect(left?.strength).toBe(1);
    expect(right?.strength).toBe(0);
    model.dispose();
  });
});
