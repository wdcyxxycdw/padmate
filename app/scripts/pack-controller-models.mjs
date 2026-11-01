import { copyFile, mkdir, readFile, stat, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

// Run from the repository root: node app/scripts/pack-controller-models.mjs
// A model whose source is missing is skipped, so its committed GLB stays.
const out = "app/src/renderer/controller/assets";
const cache = ".cache/asset-candidates";

// Longest side in pixels per texture role, and the WebP quality. PNG
// textures are converted with these; textures that are already WebP are
// embedded as they are.
const limits = {
  color: { size: 1024, quality: 85 },
  data: { size: 512, quality: 80 },
};
// Material texture slots and the limit group they belong to. Normal maps
// are dropped for the models that set dropNormals (see the report).
const slots = {
  baseColorTexture: "color",
  emissiveTexture: "color",
  metallicRoughnessTexture: "data",
  occlusionTexture: "data",
  normalTexture: "data",
};

const models = [
  { name: "xbox", dir: `${cache}/xbox`, file: "xbox.glb" },
  {
    name: "dualshock4",
    dir: `${cache}/ds4`,
    file: "dualshock4.glb",
    prune: true,
    dropNormals: true,
  },
];

async function exists(path) {
  try {
    await stat(path);
    return true;
  } catch {
    return false;
  }
}

let sharpModule;
async function loadSharp() {
  if (sharpModule) return sharpModule;
  const path = resolve(
    process.cwd(),
    ".cache/asset-tools/node_modules/sharp/lib/index.js",
  );
  try {
    sharpModule = (await import(pathToFileURL(path).href)).default;
  } catch (err) {
    throw new Error(
      `sharp is needed to convert PNG textures but could not be loaded from ${path}. Run: npm install --prefix .cache/asset-tools sharp@0.34.5`,
      { cause: err },
    );
  }
  return sharpModule;
}

// Every material texture reference, including those of extensions.
function* textureRefs(material) {
  for (const [key, value] of Object.entries(material)) {
    if (value && typeof value === "object") {
      if (typeof value.index === "number" && key in slots) {
        yield { owner: material, key, ref: value };
      } else {
        yield* textureRefs(value);
      }
    }
  }
}

// Removes images, textures and samplers no material uses any more and
// renumbers the references that are left.
function prune(j) {
  const keep = (list, used) => {
    const map = new Map();
    const kept = [];
    list.forEach((item, i) => {
      if (used.has(i)) {
        map.set(i, kept.length);
        kept.push(item);
      }
    });
    return { map, kept };
  };
  const texUsed = new Set();
  for (const m of j.materials ?? []) {
    for (const { ref } of textureRefs(m)) texUsed.add(ref.index);
  }
  const textures = keep(j.textures ?? [], texUsed);
  const imgUsed = new Set();
  const smpUsed = new Set();
  for (const t of textures.kept) {
    if (t.source !== undefined) imgUsed.add(t.source);
    if (t.sampler !== undefined) smpUsed.add(t.sampler);
  }
  const images = keep(j.images ?? [], imgUsed);
  const samplers = keep(j.samplers ?? [], smpUsed);
  for (const t of textures.kept) {
    if (t.source !== undefined) t.source = images.map.get(t.source);
    if (t.sampler !== undefined) t.sampler = samplers.map.get(t.sampler);
  }
  for (const m of j.materials ?? []) {
    for (const { ref } of textureRefs(m)) {
      ref.index = textures.map.get(ref.index);
    }
  }
  j.textures = textures.kept;
  j.images = images.kept;
  j.samplers = samplers.kept;
  for (const k of ["textures", "images", "samplers"]) {
    if (j[k].length === 0) delete j[k];
  }
}

// The strictest limit group of each image over the textures that use it.
function imageLimits(j) {
  const result = new Map();
  for (const m of j.materials ?? []) {
    for (const { key, ref } of textureRefs(m)) {
      const img = j.textures[ref.index].source;
      if (img === undefined) continue;
      const group = slots[key];
      if (group === "color" || !result.has(img)) result.set(img, group);
    }
  }
  return result;
}

async function pack(model) {
  const j = JSON.parse(await readFile(`${model.dir}/scene.gltf`, "utf8"));
  if (model.dropNormals) {
    for (const m of j.materials ?? []) delete m.normalTexture;
  }
  if (model.prune) prune(j);
  const groups = imageLimits(j);
  const chunks = [await readFile(`${model.dir}/scene.bin`)];
  let length = chunks[0].length;
  for (const [i, img] of (j.images ?? []).entries()) {
    let data = await readFile(`${model.dir}/${img.uri}`);
    if (img.uri.toLowerCase().endsWith(".png")) {
      const limit = limits[groups.get(i) ?? "data"];
      const sharp = await loadSharp();
      data = await sharp(data)
        .resize(limit.size, limit.size, {
          fit: "inside",
          withoutEnlargement: true,
        })
        .webp({ quality: limit.quality })
        .toBuffer();
    }
    const pad = (4 - (length % 4)) % 4;
    if (pad) {
      chunks.push(Buffer.alloc(pad));
      length += pad;
    }
    const idx = j.bufferViews.length;
    j.bufferViews.push({
      buffer: 0,
      byteOffset: length,
      byteLength: data.length,
    });
    chunks.push(data);
    length += data.length;
    delete img.uri;
    img.bufferView = idx;
    img.mimeType = "image/webp";
  }
  j.buffers = [{ byteLength: length }];
  const json = Buffer.from(JSON.stringify(j));
  const jsonSize = Math.ceil(json.length / 4) * 4;
  const binSize = Math.ceil(length / 4) * 4;
  const glb = Buffer.alloc(12 + 8 + jsonSize + 8 + binSize);
  glb.writeUInt32LE(0x46546c67, 0);
  glb.writeUInt32LE(2, 4);
  glb.writeUInt32LE(glb.length, 8);
  glb.writeUInt32LE(jsonSize, 12);
  glb.writeUInt32LE(0x4e4f534a, 16);
  glb.fill(32, 20, 20 + jsonSize);
  json.copy(glb, 20);
  glb.writeUInt32LE(binSize, 20 + jsonSize);
  glb.writeUInt32LE(0x004e4942, 24 + jsonSize);
  Buffer.concat(chunks).copy(glb, 28 + jsonSize);
  await writeFile(`${out}/${model.file}`, glb);
  return glb.length;
}

await mkdir(out, { recursive: true });
const sizes = {};
for (const model of models) {
  if (!(await exists(`${model.dir}/scene.gltf`))) {
    console.log(`skip ${model.name}: no source in ${model.dir}`);
    continue;
  }
  sizes[model.file] = await pack(model);
}
if (await exists(`${cache}/dualsense.glb`)) {
  await copyFile(`${cache}/dualsense.glb`, `${out}/dualsense.glb`);
  sizes["dualsense.glb"] = (await stat(`${out}/dualsense.glb`)).size;
} else {
  console.log(`skip dualsense: no source at ${cache}/dualsense.glb`);
}
console.log(sizes);
