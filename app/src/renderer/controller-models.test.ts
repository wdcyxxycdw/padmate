import { readFile } from "node:fs/promises";
import {
  Group,
  Mesh,
  MeshStandardMaterial,
  type Object3D,
  SphereGeometry,
  Texture,
} from "three";
import { GLTFLoader } from "three/addons/loaders/GLTFLoader.js";
import { describe, expect, it, vi } from "vitest";
import { buildAssetModel } from "./controller-asset";
import {
  createModelCache,
  decodeBase64,
  modelCopy,
  modelShell,
} from "./controller-models";

function template() {
  const group = new Group();
  const image = { width: 2, height: 2 };
  const mesh = new Mesh(
    new SphereGeometry(1),
    new MeshStandardMaterial({ map: new Texture(image) }),
  );
  mesh.name = "shell";
  group.add(mesh);
  return group;
}

const shellMesh = (group: Object3D) => group.getObjectByName("shell") as Mesh;
const material = (mesh: Mesh) => mesh.material as MeshStandardMaterial;

/** The bundled Xbox shell, parsed with blank textures as Node has no images. */
async function xboxShell() {
  const bytes = await readFile(new URL("./assets/xbox.glb", import.meta.url));
  const loader = new GLTFLoader();
  loader.register(() => ({
    name: "test_textures",
    loadTexture: async () => new Texture(),
  }));
  const gltf = await loader.parseAsync(
    bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength),
    "",
  );
  return gltf.scene;
}

describe("controller models (G3)", () => {
  it("decodes base64 to the same bytes, fast path or not", async () => {
    const bytes = await readFile(new URL("./assets/xbox.glb", import.meta.url));
    const text = bytes.toString("base64");
    const decoded = decodeBase64(text);
    expect(decoded.byteLength).toBe(bytes.byteLength);
    expect(Buffer.from(decoded).equals(bytes)).toBe(true);
    // Electron has Uint8Array.fromBase64 and uses it.
    const native = Object.getOwnPropertyDescriptor(Uint8Array, "fromBase64");
    const fromBase64 = vi.fn((value: string) =>
      Uint8Array.from(Buffer.from(value, "base64")),
    );
    Object.defineProperty(Uint8Array, "fromBase64", {
      value: fromBase64,
      configurable: true,
    });
    try {
      expect(Buffer.from(decodeBase64(text)).equals(bytes)).toBe(true);
      expect(fromBase64).toHaveBeenCalledExactlyOnceWith(text);
    } finally {
      if (native) Object.defineProperty(Uint8Array, "fromBase64", native);
      else delete (Uint8Array as { fromBase64?: unknown }).fromBase64;
    }
    // Without it, the plain loop gives the same bytes.
    if (!native)
      expect(Buffer.from(decodeBase64(text)).equals(bytes)).toBe(true);
    expect(decodeBase64("")).toEqual(new Uint8Array());
  });

  it("uses the PlayStation shell for PlayStation pads, the Xbox one otherwise", () => {
    expect(modelShell("ps5")).toBe("playstation");
    expect(modelShell("ps3")).toBe("playstation");
    for (const type of ["xboxone", "switchpro", "joyconleft", "unknown"])
      expect(modelShell(type)).toBe("xbox");
  });

  it("decodes and parses each shell once, and gives every scene its own copy", async () => {
    const shell = template();
    const parse = vi.fn(async () => shell);
    const cache = createModelCache(parse);
    const [first, second] = await Promise.all([
      cache.get("xbox"),
      cache.get("xbox"),
    ]);
    const third = await cache.get("xbox");
    expect(parse).toHaveBeenCalledExactlyOnceWith("xbox");
    expect(new Set([shell, first, second, third]).size).toBe(4);
    const source = shellMesh(shell);
    const meshes = [first, third].map(shellMesh);
    for (const mesh of meshes) {
      // Its own geometry, material and texture ...
      expect(mesh).not.toBe(source);
      expect(mesh.geometry).not.toBe(source.geometry);
      expect(material(mesh)).not.toBe(material(source));
      expect(material(mesh).map).not.toBe(material(source).map);
      // ... over the same vertex data and decoded image.
      expect(mesh.geometry.attributes.position).toBe(
        source.geometry.attributes.position,
      );
      expect(mesh.geometry.index).toBe(source.geometry.index);
      expect(material(mesh).map?.source).toBe(material(source).map?.source);
    }
    expect(meshes[0].geometry).not.toBe(meshes[1].geometry);
    // Changing one scene's copy leaves the others and the shell alone.
    first.scale.set(2, 2, 2);
    meshes[0].visible = false;
    material(meshes[0]).color.set(0xff0000);
    expect(third.scale.x).toBe(1);
    expect(meshes[1].visible).toBe(true);
    expect(material(meshes[1]).color.getHex()).toBe(0xffffff);
    expect(material(source).color.getHex()).toBe(0xffffff);
    await cache.get("playstation");
    expect(parse).toHaveBeenCalledTimes(2);
  });

  it("tries a shell again after a failed load", async () => {
    const parse = vi
      .fn<(shell: string) => Promise<Group>>()
      .mockRejectedValueOnce(new Error("broken"))
      .mockResolvedValue(template());
    const cache = createModelCache(parse);
    await expect(cache.get("xbox")).rejects.toThrow("broken");
    await expect(cache.get("xbox")).resolves.toBeInstanceOf(Group);
    expect(parse).toHaveBeenCalledTimes(2);
  });

  it("lets a scene dispose its copy and never the shell", async () => {
    const shell = template();
    const cache = createModelCache(async () => shell);
    const source = shellMesh(shell);
    const kept = [
      source.geometry,
      material(source),
      material(source).map as Texture,
    ].map((resource) => vi.spyOn(resource, "dispose"));
    const copy = await cache.get("xbox");
    const mesh = shellMesh(copy);
    const own = [
      mesh.geometry,
      material(mesh),
      material(mesh).map as Texture,
    ].map((resource) => vi.spyOn(resource, "dispose"));
    const model = buildAssetModel(copy, { type: "xboxone" });
    expect(model.resources.has(mesh.geometry)).toBe(true);
    model.dispose();
    for (const spy of own) expect(spy).toHaveBeenCalledOnce();
    for (const spy of kept) expect(spy).not.toHaveBeenCalled();
    // The next scene's copy still has the vertex data.
    const next = await cache.get("xbox");
    expect(shellMesh(next).geometry.attributes.position).toBe(
      source.geometry.attributes.position,
    );
  });

  it("builds the bundled shell's controls on every copy and leaves the shell as parsed", async () => {
    const shell = await xboxShell();
    let stick: Object3D | undefined;
    shell.traverse((node) => {
      if (node instanceof Mesh && node.name.startsWith("Cube002_"))
        stick = node;
    });
    if (!stick) throw new Error("no left stick in the shell");
    const parent = stick.parent;
    const scale = shell.scale.clone();
    const models = [modelCopy(shell), modelCopy(shell)].map((copy) =>
      buildAssetModel(copy, { type: "xboxone" }),
    );
    const direct = buildAssetModel(await xboxShell(), { type: "xboxone" });
    for (const model of models) {
      expect([...model.controls.keys()]).toEqual([...direct.controls.keys()]);
      expect([...model.sticks.keys()]).toEqual(["left", "right"]);
      for (const [name, control] of model.controls)
        expect(
          control.point.distanceTo(
            direct.controls.get(name)?.point ??
              control.point.clone().addScalar(1),
          ),
        ).toBeLessThan(1e-6);
    }
    // Scaling, centring and the stick pivots happened on the copies.
    expect(shell.scale.equals(scale)).toBe(true);
    expect(stick.parent).toBe(parent);
    for (const model of models) model.dispose();
    direct.dispose();
  });
});
