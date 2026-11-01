// The bundled controller models for tests: the real GLBs, parsed in Node with
// blank textures (there are no images to decode), and the helpers the model
// tests share.
import { readFile } from "node:fs/promises";
import { Texture } from "three";
import { GLTFLoader } from "three/addons/loaders/GLTFLoader.js";
import {
  buildAssetModel,
  type ModelInfo,
} from "../controller/controller-asset";
import { joyConPair, modelShell } from "../controller/controller-models";

export async function parseGlb(file: string) {
  const bytes = await readFile(
    new URL(`../controller/assets/${file}.glb`, import.meta.url),
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
  return gltf.scene;
}

/** The model of a pad as the scene builds it. */
export async function modelFixture(
  type: string,
  masks: Omit<ModelInfo, "type"> = {},
) {
  const shell = modelShell(type);
  const scene =
    shell === "joycon-pair"
      ? joyConPair(
          await parseGlb("joycon-left"),
          await parseGlb("joycon-right"),
        )
      : await parseGlb(
          {
            dualsense: "dualsense",
            dualshock: "dualshock4",
            xbox: "xbox",
            "joycon-left": "joycon-left",
            "joycon-right": "joycon-right",
          }[shell],
        );
  return buildAssetModel(scene, { type, ...masks });
}
export type FixtureModel = Awaited<ReturnType<typeof modelFixture>>;

export function stickOf(model: FixtureModel, side: string) {
  const control = model.controls.get(`${side}stick`);
  const stick = control?.stick;
  if (!control || !stick) throw new Error(`Missing ${side} stick`);
  return { control, stick };
}

/** A pad input with one stick pushed to (x, y), each in -1..1. */
export const push = (side: string, x: number, y: number) => ({
  buttons: {},
  axes: {
    [`${side}x`]: x < 0 ? x * 32768 : x * 32767,
    [`${side}y`]: y < 0 ? y * 32768 : y * 32767,
  },
});

// Unit pushes; the diagonals are what a square gate reports at full travel.
export const directions = [
  [1, 0],
  [-1, 0],
  [0, 1],
  [0, -1],
  [1, 1],
  [1, -1],
  [-1, 1],
  [-1, -1],
] as const;
