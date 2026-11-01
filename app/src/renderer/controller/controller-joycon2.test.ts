// The Joy-Con 2 shells (joycon2-left.glb, joycon2-right.glb, cut out of
// "Nintendo Switch 2 Realistic 3D Model" by scripts/pack-controller-models.mjs):
// the files' structure, and how a pad of Nintendo's Joy-Con 2 ids is drawn.
import { readFile } from "node:fs/promises";
import { Box3, Mesh, type Object3D, Vector3 } from "three";
import { describe, expect, it } from "vitest";
import {
  modelFixture as fixture,
  parseGlb,
} from "../testing/controller-assets";
import { joyCon2Pads, sdlPads } from "../testing/sdl-pads";
import { controllerCallouts } from "./controller-callouts";
import { controllerLabels } from "./controller-model";

const file = (name: string) => new URL(`./assets/${name}.glb`, import.meta.url);

/** The JSON chunk of a GLB. */
async function glbJson(name: string) {
  const bytes = await readFile(file(name));
  const length = bytes.readUInt32LE(12);
  return {
    size: bytes.length,
    json: JSON.parse(bytes.subarray(20, 20 + length).toString("utf8")) as {
      extensionsUsed: string[];
      extensionsRequired: string[];
      asset: { extras: Record<string, string> };
      nodes: { name: string; mesh?: number; scale?: number[] }[];
      meshes: { name: string; primitives: { material: number }[] }[];
      materials: { name: string }[];
      accessors: { componentType: number; normalized?: boolean }[];
      images?: unknown[];
      textures?: unknown[];
    },
  };
}

// The names of the parts: a mesh each, named like the Switch OLED Joy-Con's
// (the stick, the D-pad or ABXY, the system buttons, the shoulders, the
// rail), and the C button.
const common = [
  "shell",
  "stick",
  "stick-base",
  "glyphs",
  "rail",
  "connector",
  "sync",
  "detail-1",
];
const meshParts = {
  "joycon2-left": [
    ...common,
    "dpad-up",
    "dpad-down",
    "dpad-left",
    "dpad-right",
    "minus",
    "capture",
    "l",
  ],
  "joycon2-right": [...common, "a", "b", "x", "y", "plus", "home", "c", "r"],
};
// The parts the source model has no geometry for: empty nodes in the file.
const pointParts = {
  "joycon2-left": ["sl", "sr", "zl"],
  "joycon2-right": ["sr", "sl", "zr"],
};

describe.each(["joycon2-left", "joycon2-right"] as const)("%s.glb", (name) => {
  it("has one mesh, node and material per part, and the points as empty nodes", async () => {
    const { json } = await glbJson(name);
    const meshes = meshParts[name];
    expect(json.meshes.map((mesh) => mesh.name).sort()).toEqual(
      [...meshes].sort(),
    );
    expect(
      json.nodes.filter((node) => node.mesh !== undefined).map((n) => n.name),
    ).toEqual(json.meshes.map((mesh) => mesh.name));
    expect(
      json.nodes.filter((node) => node.mesh === undefined).map((n) => n.name),
    ).toEqual(pointParts[name]);
    // Each part its own material, named like it: nothing shared, no textures.
    expect(json.materials.map((material) => material.name)).toEqual(
      json.meshes.map((mesh) => mesh.name),
    );
    expect(json.meshes.map((mesh) => mesh.primitives[0].material)).toEqual(
      json.meshes.map((_, index) => index),
    );
    expect(json.textures).toBeUndefined();
    expect(json.images).toBeUndefined();
  });

  it("is quantized: 16-bit positions with a node scale, 8-bit normals", async () => {
    const { json } = await glbJson(name);
    for (const extensions of [json.extensionsUsed, json.extensionsRequired])
      expect(extensions).toEqual(["KHR_mesh_quantization"]);
    const types = new Set(json.accessors.map((a) => a.componentType));
    // Short positions, signed-byte normals, unsigned-short indices.
    expect([...types].sort()).toEqual([5120, 5122, 5123]);
    for (const accessor of json.accessors)
      if (accessor.componentType === 5120)
        expect(accessor.normalized).toBe(true);
    // The same scale on every part, so a stick's size stays right (the
    // scene reads it from the node's world matrix).
    const scales = new Set(
      json.nodes.filter((n) => n.mesh !== undefined).map((n) => n.scale?.[0]),
    );
    expect(scales.size).toBe(1);
  });

  it("is small", async () => {
    // The Switch OLED Joy-Cons are 327 and 345 KB; these have no textures.
    expect((await glbJson(name)).size).toBeLessThan(330_000);
  });

  it("credits the original model in its metadata", async () => {
    const { extras } = (await glbJson(name)).json.asset;
    expect(extras.title).toBe("Nintendo Switch 2 Realistic 3D Model");
    expect(extras.license).toBe(
      "CC-BY-4.0 (http://creativecommons.org/licenses/by/4.0/)",
    );
    expect(extras.author).toContain("NAIT Studio");
  });

  it("is upright: the parts parse by their names, in metres, centred", async () => {
    const scene = await parseGlb(name);
    for (const part of [...meshParts[name], ...pointParts[name]])
      expect(scene.getObjectByName(part), part).toBeDefined();
    for (const part of meshParts[name])
      expect(scene.getObjectByName(part), part).toBeInstanceOf(Mesh);
    for (const part of pointParts[name])
      expect(scene.getObjectByName(part), part).not.toBeInstanceOf(Mesh);
    const box = new Box3().setFromObject(scene);
    const size = box.getSize(new Vector3());
    // 43 x 116 mm: the shell is 36 wide and the rail 6 more.
    expect(size.x).toBeGreaterThan(0.042);
    expect(size.x).toBeLessThan(0.045);
    expect(size.y).toBeGreaterThan(0.113);
    expect(size.y).toBeLessThan(0.118);
    expect(box.getCenter(new Vector3()).length()).toBeLessThan(1e-4);
    const part = (n: string) =>
      new Box3().setFromObject(scene.getObjectByName(n) as Object3D);
    const centre = (n: string) => part(n).getCenter(new Vector3());
    // The rail is on +X for the left Joy-Con and -X for the right one, the
    // sticks and buttons in front (+Z), the shoulder behind.
    const rail = centre("rail").x;
    expect(rail * (name === "joycon2-left" ? 1 : -1)).toBeGreaterThan(0.015);
    expect(centre("stick").z).toBeGreaterThan(centre("shell").z);
    const shoulder = name === "joycon2-left" ? "l" : "r";
    expect(centre(shoulder).y).toBeGreaterThan(0.045);
    expect(centre(shoulder).z).toBeLessThan(centre("shell").z);
    // SL and SR are on the rail, the shoulder's ZL or ZR behind it.
    const point = (n: string) =>
      (scene.getObjectByName(n) as Object3D).getWorldPosition(new Vector3());
    expect(point("sl").x).toBeCloseTo(rail, 3);
    expect(point("sr").x).toBeCloseTo(rail, 3);
    expect(Math.abs(point("sl").y - point("sr").y)).toBeGreaterThan(0.05);
    const trigger = name === "joycon2-left" ? "zl" : "zr";
    expect(point(trigger).z).toBeLessThan(part(shoulder).min.z + 0.003);
    // The stick is the cap, turning where its base begins.
    const stick = scene.getObjectByName("stick") as Mesh;
    stick.geometry.computeBoundingBox();
    expect(stick.geometry.boundingBox?.min.z).toBeGreaterThan(0);
    const pivotZ = stick.getWorldPosition(new Vector3()).z;
    expect(pivotZ).toBeCloseTo(part("stick-base").min.z, 4);
    expect(part("stick").min.z).toBeGreaterThan(pivotZ);
  });
});

const joyCon2 = (name: keyof typeof joyCon2Pads) => {
  const { type, ...masks } = joyCon2Pads[name];
  return fixture(type, masks);
};

describe("the C button of a Joy-Con 2", () => {
  it.each(["joyConRightSideways", "joyConRightUpright", "joyConPair"] as const)(
    "is on the C part of %s, with its own label",
    async (name) => {
      const model = await joyCon2(name);
      const control = model.controls.get("misc2");
      expect(control).toBeDefined();
      // Over the part: the leader's end is the surface point above its centre.
      const part = model.group.getObjectByName("c") as Object3D;
      const centre = new Box3().setFromObject(part).getCenter(new Vector3());
      expect(control?.point.x).toBeCloseTo(centre.x, 2);
      expect(control?.point.y).toBeCloseTo(centre.y, 2);
      const callout = model.callouts.find((entry) => entry.control === "misc2");
      expect(callout?.placement).toBe("bottom");
      expect(callout?.label).toBe("C 键");
      model.dispose();
    },
  );

  it("lights up with the button", async () => {
    const model = await joyCon2("joyConRightUpright");
    const control = model.controls.get("misc2");
    model.update({ buttons: { misc2: true }, axes: {} });
    expect(control?.strength).toBe(1);
    expect(control?.marker.material.opacity).toBeGreaterThan(0.9);
    model.update();
    expect(control?.strength).toBe(0);
    model.dispose();
  });

  it("is only on a Joy-Con 2 that reports it", async () => {
    // The left one has none, nor has a Switch OLED Joy-Con.
    for (const info of [
      joyCon2Pads.joyConLeftUpright,
      joyCon2Pads.joyConLeftSideways,
      sdlPads.joyConRightUpright,
      sdlPads.joyConPair,
    ]) {
      const { type, ...masks } = info;
      const model = await fixture(type, masks);
      expect(model.controls.has("misc2"), type).toBe(false);
      model.dispose();
    }
    // Nor does one whose buttons are not known yet: the extra keys only show
    // once SDL reports them.
    const model = await fixture("joyconright", {
      vendor: 0x057e,
      product: 0x2066,
    });
    expect(model.controls.has("misc2")).toBe(false);
    model.dispose();
  });

  it("is named C in English and C 键 in Chinese, on any Switch 2 pad", () => {
    const { type, buttonMask } = joyCon2Pads.joyConRightUpright;
    expect(controllerLabels(type, {}, buttonMask).misc2).toBe("C 键");
    expect(controllerLabels(type, {}, buttonMask, "zh-CN").misc2).toBe("C 键");
    expect(controllerLabels(type, {}, buttonMask, "en").misc2).toBe("C");
    // The other families keep their numbered name.
    expect(controllerLabels("xboxone").misc2).toBe("附加键 2");
    expect(controllerLabels("xboxone", {}, undefined, "en").misc2).toBe(
      "附加键 2",
    );
    // The chip carries the label of the interface's language.
    const info = joyCon2Pads.joyConRightUpright;
    const chip = (language?: "zh-CN" | "en") =>
      controllerCallouts(info, language).find((c) => c.control === "misc2")
        ?.label;
    expect(chip()).toBe("C 键");
    expect(chip("en")).toBe("C");
  });
});

describe("a Joy-Con 2 pair", () => {
  it("has the Capture and the C button in the row below, six chips in all", () => {
    const row = controllerCallouts(joyCon2Pads.joyConPair)
      .filter((entry) => entry.placement === "bottom")
      .sort((a, b) => (a.offset ?? 0) - (b.offset ?? 0))
      .map((entry) => [entry.control, entry.offset]);
    expect(row).toEqual([
      ["paddle4", -2.5],
      ["paddle2", -1.5],
      ["misc1", -0.5],
      ["misc2", 0.5],
      ["paddle1", 1.5],
      ["paddle3", 2.5],
    ]);
  });

  it("is drawn with the Joy-Con 2 of each side and the Capture of the left one", async () => {
    const model = await joyCon2("joyConPair");
    const mapped = [...model.controls.keys()].filter((c) => c !== "guide");
    expect(new Set(model.callouts.map((c) => c.control))).toEqual(
      new Set(mapped),
    );
    const p = (control: string) =>
      model.controls.get(control)?.point as Vector3;
    expect(p("misc1").x).toBeLessThan(0);
    expect(p("misc2").x).toBeGreaterThan(0);
    // The left Capture is a part of its own: right of the left D-pad, below it.
    expect(p("misc1").y).toBeLessThan(p("dpad.down").y);
    expect(p("misc1").x).toBeGreaterThan(p("dpad.down").x);
    // SL and SR sit on the rails, facing each other.
    expect(p("paddle2").x).toBeGreaterThan(p("leftstick").x);
    expect(p("paddle1").x).toBeLessThan(p("rightstick").x);
    model.dispose();
  });

  it("is made of the two Joy-Con 2 files, not the OLED ones", async () => {
    const { type, ...masks } = joyCon2Pads.joyConPair;
    const model = await fixture(type, masks);
    // Only the Joy-Con 2 has these parts.
    for (const part of ["stick-base", "connector"])
      expect(model.group.getObjectsByProperty("name", part)).toHaveLength(2);
    model.dispose();
    const oled = await fixture(type, sdlPads.joyConPair);
    for (const part of ["stick-base", "connector"])
      expect(oled.group.getObjectsByProperty("name", part)).toHaveLength(0);
    oled.dispose();
  });
});
