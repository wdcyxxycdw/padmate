// The licensed controller models (G3): each shell is decoded and parsed once
// per app session, and every scene gets its own copy of it. Before, every
// visit to the mapping page decoded the ~8 MB inline GLB again, character by
// character, and parsed it again, textures included.
//
// Copying is the cheap part (about 1 ms). What a visit still cost after G3
// was uploading the copy's textures to a new WebGL context, 0.6-0.85 s of
// blocked main thread in the Electron app; controller-stage.ts keeps the
// renderer and its textures between visits, so a copy is made only when a
// stage is built.
import {
  BufferGeometry,
  type Group,
  type Material,
  Mesh,
  Texture,
  TextureLoader,
} from "three";
import { GLTFLoader } from "three/addons/loaders/GLTFLoader.js";
import { familyName } from "./controller-model";

/** The two shells: PlayStation pads get the DualSense, every other pad the Xbox one. */
export type ModelShell = "playstation" | "xbox";

export function modelShell(type: string): ModelShell {
  return familyName(type) === "playstation" ? "playstation" : "xbox";
}

/**
 * The bytes of base64 text: Uint8Array.fromBase64 where the runtime has it
 * (Chromium 140 and later, so the app's Electron), else atob and a plain
 * loop, still about 30 times faster than Uint8Array.from(atob(text),
 * callback) on the 10 MB of text of the Xbox shell.
 */
export function decodeBase64(text: string): Uint8Array {
  const native = Uint8Array as unknown as {
    fromBase64?: (text: string) => Uint8Array;
  };
  if (typeof native.fromBase64 === "function") return native.fromBase64(text);
  const binary = atob(text);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index++)
    bytes[index] = binary.charCodeAt(index);
  return bytes;
}

/** Decodes and parses the bundled GLB of a shell. */
async function parseBundledModel(shell: ModelShell): Promise<Group> {
  // Vite embeds each local GLB in a lazy module as a data URL. Decoding it
  // here opens no connection (fetch of a data: URL would need connect-src).
  const { default: data } =
    shell === "playstation"
      ? await import("./assets/dualsense.glb?url&inline")
      : await import("./assets/xbox.glb?url&inline");
  const bytes = decodeBase64(data.slice(data.indexOf(",") + 1));
  const loader = new GLTFLoader();
  // TextureLoader uses <img src=blob:>, keeping production connect-src 'none'.
  loader.register((parser) => ({
    name: "padmate_local_textures",
    loadTexture: (index) =>
      parser.loadTextureImage(
        index,
        parser.json.textures[index].source,
        new TextureLoader(),
      ),
  }));
  // Both decoders return an array over a whole buffer of its own.
  return (await loader.parseAsync(bytes.buffer as ArrayBuffer, "")).scene;
}

/**
 * A copy of a parsed shell for one scene. The nodes, geometries, materials
 * and textures are its own, so the scene changes and disposes them as it
 * likes; the heavy parts stay shared: vertex data (the same attributes) and
 * decoded images (the same texture sources). The shell itself is never
 * rendered, so no renderer holds on to it.
 */
export function modelCopy(shell: Group): Group {
  const copy = shell.clone(true);
  const geometries = new Map<BufferGeometry, BufferGeometry>();
  const materials = new Map<Material, Material>();
  const textures = new Map<Texture, Texture>();
  const geometryOf = (source: BufferGeometry) => {
    let own = geometries.get(source);
    if (!own) {
      own = new BufferGeometry();
      own.name = source.name;
      own.setIndex(source.index);
      for (const [name, attribute] of Object.entries(source.attributes))
        own.setAttribute(name, attribute);
      own.morphAttributes = { ...source.morphAttributes };
      own.morphTargetsRelative = source.morphTargetsRelative;
      for (const { start, count, materialIndex } of source.groups)
        own.addGroup(start, count, materialIndex);
      own.setDrawRange(source.drawRange.start, source.drawRange.count);
      own.boundingBox = source.boundingBox?.clone() ?? null;
      own.boundingSphere = source.boundingSphere?.clone() ?? null;
      own.userData = source.userData;
      geometries.set(source, own);
    }
    return own;
  };
  const textureOf = (source: Texture) => {
    let own = textures.get(source);
    if (!own) {
      own = source.clone();
      textures.set(source, own);
    }
    return own;
  };
  const materialOf = (source: Material) => {
    let own = materials.get(source);
    if (!own) {
      own = source.clone();
      const record = own as unknown as Record<string, unknown>;
      for (const [key, value] of Object.entries(record))
        if (value instanceof Texture) record[key] = textureOf(value);
      materials.set(source, own);
    }
    return own;
  };
  copy.traverse((node) => {
    if (!(node instanceof Mesh)) return;
    node.geometry = geometryOf(node.geometry);
    node.material = Array.isArray(node.material)
      ? node.material.map(materialOf)
      : materialOf(node.material);
  });
  return copy;
}

/**
 * A cache of parsed shells. get gives a new copy (modelCopy) of the shell
 * each time; parse runs once per shell, or again after it failed.
 */
export function createModelCache(parse: (shell: ModelShell) => Promise<Group>) {
  const shells = new Map<ModelShell, Promise<Group>>();
  return {
    async get(shell: ModelShell): Promise<Group> {
      let parsed = shells.get(shell);
      if (!parsed) {
        const loading = parse(shell);
        parsed = loading;
        shells.set(shell, loading);
        loading.catch(() => {
          if (shells.get(shell) === loading) shells.delete(shell);
        });
      }
      return modelCopy(await parsed);
    },
  };
}

/** The models of this app session. */
export const controllerModels = createModelCache(parseBundledModel);
