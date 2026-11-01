// The licensed controller models: each shell is decoded and parsed once
// per app session, and every scene gets its own copy of it. Before, every
// visit to the mapping page decoded the ~8 MB inline GLB again, character by
// character, and parsed it again, textures included.
//
// Copying is the cheap part (about 1 ms). What a visit still cost after the cache
// was uploading the copy's textures to a new WebGL context, 0.6-0.85 s of
// blocked main thread in the Electron app; controller-stage.ts keeps the
// renderer and its textures between visits, so a copy is made only when a
// stage is built.
import {
  BufferGeometry,
  Group,
  type Material,
  Mesh,
  Texture,
  TextureLoader,
} from "three";
import { GLTFLoader } from "three/addons/loaders/GLTFLoader.js";
import { familyName } from "./controller-model";

/**
 * The shells: the DualSense for a PS5 pad, the DualShock 4 for a PS4 or PS3
 * pad, a Switch OLED Joy-Con (or both of a pair), a Joy-Con 2 (or both of a
 * pair), the Xbox one for every other pad.
 */
export type ModelShell =
  | "dualsense"
  | "dualshock"
  | "xbox"
  | "joycon-left"
  | "joycon-right"
  | "joycon-pair"
  | "joycon2-left"
  | "joycon2-right"
  | "joycon2-pair";

/** The shells that are one GLB file each; a pair is made of two of them. */
export type ModelFile = Exclude<ModelShell, `${string}-pair`>;

/** Nintendo's USB vendor id. */
const nintendo = 0x057e;
/** The Joy-Con 2 products: left, right, and the pair the daemon reports. */
const joyCon2Products = new Set([0x2067, 0x2066, 0x2068]);

/**
 * Whether a pad is a Joy-Con 2: Nintendo's vendor id and one of the Joy-Con
 * 2 product ids, which the daemon's Bluetooth LE driver reports (driver
 * "ble"); a Switch OLED Joy-Con has 0x2006 and 0x2007.
 */
export function isJoyCon2(ids: { vendor?: number; product?: number }) {
  return (
    ids.vendor === nintendo &&
    ids.product !== undefined &&
    joyCon2Products.has(ids.product)
  );
}

/**
 * The shell of a pad: by its type, and for a Joy-Con by its USB ids, the
 * Joy-Con 2's own model or the Switch OLED one.
 */
export function modelShell(
  type: string,
  ids: { vendor?: number; product?: number } = {},
): ModelShell {
  const generation = isJoyCon2(ids) ? "joycon2" : "joycon";
  if (type === "joyconleft") return `${generation}-left`;
  if (type === "joyconright") return `${generation}-right`;
  if (type === "joyconpair") return `${generation}-pair`;
  if (familyName(type) !== "playstation") return "xbox";
  return type === "ps5" ? "dualsense" : "dualshock";
}

/** The two files a pair of Joy-Cons is made of. */
export function pairFiles(
  shell: Extract<ModelShell, `${string}-pair`>,
): [left: ModelFile, right: ModelFile] {
  return shell === "joycon2-pair"
    ? ["joycon2-left", "joycon2-right"]
    : ["joycon-left", "joycon-right"];
}

/**
 * The two Joy-Cons of a pair in one group, each under its own named root: the
 * parts of both have the same names, so the root says whose they are.
 */
export function joyConPair(left: Group, right: Group): Group {
  left.name = "joycon-left";
  right.name = "joycon-right";
  const pair = new Group();
  pair.add(left, right);
  return pair;
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

/** Decodes and parses a bundled GLB file. */
async function parseBundledFile(file: ModelFile): Promise<Group> {
  // Vite embeds each local GLB in a lazy module as a data URL. Decoding it
  // here opens no connection (fetch of a data: URL would need connect-src).
  const { default: data } = await {
    dualsense: () => import("./assets/dualsense.glb?url&inline"),
    dualshock: () => import("./assets/dualshock4.glb?url&inline"),
    xbox: () => import("./assets/xbox.glb?url&inline"),
    "joycon-left": () => import("./assets/joycon-left.glb?url&inline"),
    "joycon-right": () => import("./assets/joycon-right.glb?url&inline"),
    "joycon2-left": () => import("./assets/joycon2-left.glb?url&inline"),
    "joycon2-right": () => import("./assets/joycon2-right.glb?url&inline"),
  }[file]();
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
 * What the cache parses a shell with: each GLB file is parsed once however
 * many shells use it. The pair is copies of the two Joy-Con files, so the
 * parsed files stay as parsed.
 */
export function createShellParser(
  parseFile: (file: ModelFile) => Promise<Group>,
) {
  const files = new Map<ModelFile, Promise<Group>>();
  const fileOf = (file: ModelFile) => {
    let parsed = files.get(file);
    if (!parsed) {
      const loading = parseFile(file);
      parsed = loading;
      files.set(file, loading);
      loading.catch(() => {
        if (files.get(file) === loading) files.delete(file);
      });
    }
    return parsed;
  };
  return async (shell: ModelShell): Promise<Group> => {
    if (shell !== "joycon-pair" && shell !== "joycon2-pair")
      return fileOf(shell);
    const [leftFile, rightFile] = pairFiles(shell);
    const [left, right] = await Promise.all([
      fileOf(leftFile),
      fileOf(rightFile),
    ]);
    return joyConPair(modelCopy(left), modelCopy(right));
  };
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
export const controllerModels = createModelCache(
  createShellParser(parseBundledFile),
);
