import { readFile } from "node:fs/promises";
import { Mesh, Quaternion, Texture, Vector3 } from "three";
import { GLTFLoader } from "three/addons/loaders/GLTFLoader.js";
import { describe, expect, it, vi } from "vitest";
import { buildAssetModel } from "./controller-asset";

async function fixture(type: string) {
  const file = type.startsWith("ps") ? "dualsense" : "xbox";
  const bytes = await readFile(
    new URL(`./assets/${file}.glb`, import.meta.url),
  );
  const loader = new GLTFLoader();
  loader.register(() => ({
    name: "test_textures",
    loadTexture: async () => new Texture(),
  }));
  const gltf = await loader.parseAsync(
    bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength),
    "",
  );
  return buildAssetModel(gltf.scene, { type });
}
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
      expect(new Set(model.callouts.map((c) => c.control))).toEqual(
        new Set(model.controls.keys()),
      );
      expect(model.callouts).toHaveLength(model.controls.size);
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

type Model = Awaited<ReturnType<typeof fixture>>;
function stickOf(model: Model, side: string) {
  const control = model.controls.get(`${side}stick`);
  const stick = control?.stick;
  if (!control || !stick) throw new Error(`Missing ${side} stick`);
  return { control, stick };
}
const push = (side: string, x: number, y: number) => ({
  buttons: {},
  axes: {
    [`${side}x`]: x < 0 ? x * 32768 : x * 32767,
    [`${side}y`]: y < 0 ? y * 32768 : y * 32767,
  },
});
// Unit pushes; the diagonals are what a square gate reports at full travel.
const directions = [
  [1, 0],
  [-1, 0],
  [0, 1],
  [0, -1],
  [1, 1],
  [1, -1],
  [-1, 1],
  [-1, -1],
] as const;
const sticks = ["xboxone", "ps5"].flatMap((type) =>
  ["left", "right"].map((side) => [type, side] as const),
);

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
