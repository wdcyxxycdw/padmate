import { copyFile, mkdir, readFile, stat, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

// Run from the repository root: node app/scripts/pack-controller-models.mjs
// A model whose source is missing is skipped, so its committed GLB stays.
// Names after the script pack only those sources (xbox, dualshock4,
// dualsense, switch-oled, switch2); PADMATE_ASSET_CACHE points at another
// asset-candidates directory, e.g. the one of the main checkout from a
// worktree. PADMATE_PACK_LIST=1 prints how the Joy-Con 2 pieces were named.
const out = "app/src/renderer/controller/assets";
const cache = process.env.PADMATE_ASSET_CACHE ?? ".cache/asset-candidates";
const only = process.argv.slice(2);
const wanted = (name) => only.length === 0 || only.includes(name);

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

// Collects the single binary buffer of a glTF. add() appends data as a new
// buffer view, padded to four bytes, and returns the view's index.
function binaryBuilder(j, initial = Buffer.alloc(0)) {
  const chunks = [initial];
  let length = initial.length;
  return {
    add(data, target, stride) {
      const pad = (4 - (length % 4)) % 4;
      if (pad) {
        chunks.push(Buffer.alloc(pad));
        length += pad;
      }
      const index = j.bufferViews.length;
      j.bufferViews.push({
        buffer: 0,
        byteOffset: length,
        byteLength: data.length,
        ...(target ? { target } : {}),
        ...(stride ? { byteStride: stride } : {}),
      });
      chunks.push(data);
      length += data.length;
      return index;
    },
    finish() {
      j.buffers = [{ byteLength: length }];
      return { chunks, length };
    },
  };
}

// Moves every image file into the binary buffer, converting PNG to WebP.
async function embedImages(j, dir, binary) {
  const groups = imageLimits(j);
  for (const [i, img] of (j.images ?? []).entries()) {
    let data = await readFile(`${dir}/${img.uri}`);
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
    delete img.uri;
    img.bufferView = binary.add(data);
    img.mimeType = "image/webp";
  }
}

async function writeGlb(j, { chunks, length }, file) {
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
  await writeFile(`${out}/${file}`, glb);
  return glb.length;
}

async function pack(model) {
  const j = JSON.parse(await readFile(`${model.dir}/scene.gltf`, "utf8"));
  if (model.dropNormals) {
    for (const m of j.materials ?? []) delete m.normalTexture;
  }
  if (model.prune) prune(j);
  const binary = binaryBuilder(j, await readFile(`${model.dir}/scene.bin`));
  await embedImages(j, model.dir, binary);
  return writeGlb(j, binary.finish(), model.file);
}

// ---- Joy-Cons from "Nintendo Switch OLED" ---------------------------------
//
// The source model holds the console and both Joy-Cons. Each Joy-Con is one
// merged mesh with one material. Its parts are told apart as connected
// components of the triangles and matched by position to the anchors below.
//
// Every Joy-Con ends up in the same upright frame, in metres (the source's
// own scale), centred on its bounding box: +Y is the top (the stick end of
// the left Joy-Con, the ABXY end of the right one), +Z the front, and the
// rail is on +X for the left and -X for the right Joy-Con. The frame is
// given as rows: the new x, y and z axes in the source world frame.
const normalize = (v) => v.map((c) => c / Math.hypot(...v));
const dot = (a, b) => a[0] * b[0] + a[1] * b[1] + a[2] * b[2];
const cross = (a, b) => [
  a[1] * b[2] - a[2] * b[1],
  a[2] * b[0] - a[0] * b[2],
  a[0] * b[1] - a[1] * b[0],
];
// The right Joy-Con lies flat and is turned by about 40 degrees. Its rail is
// a rigid copy of the left rail, so the frame follows the rail's principal
// axes (measured once): the long axis is up, the width axis is front.
const rightUp = normalize([0.7421, 0.069, 0.6668]);
const rightFrontRaw = [-0.0366, 0.9974, -0.0625];
const rightFront = normalize(
  rightFrontRaw.map((c, i) => c - dot(rightFrontRaw, rightUp) * rightUp[i]),
);
const rightFrame = [cross(rightUp, rightFront), rightUp, rightFront];

// Part anchors: the centre of the part's bounding box in the canonical
// frame, in millimetres. A component joins the nearest anchor within
// anchorReach; the largest component is the shell, and a component near no
// anchor becomes detail-N. Parts of several pieces (L, R) collect them all.
// The identities were checked against the letters and labels painted on the
// texture atlas (A, B, X, Y, SL, SR, L, ZL and so on); the left Capture
// button has no geometry of its own and stays in the shell.
const anchorReach = 5;
const joycons = {
  dir: `${cache}/switch-oled`,
  note: "Extracted from the original model: only this Joy-Con, split into one mesh and material per part, moved into an upright frame, textures converted to WebP.",
  sides: [
    {
      file: "joycon-left.glb",
      mesh: "RailLbuttons_L1_Joycons_Buttons_0",
      frame: [
        [-1, 0, 0],
        [0, 1, 0],
        [0, 0, -1],
      ],
      anchors: {
        stick: [-0.7, 26.5, 10.5],
        "dpad-up": [-0.6, 4.2, 8.6],
        "dpad-down": [-0.6, -11.9, 8.6],
        "dpad-left": [-8.6, -3.8, 8.6],
        "dpad-right": [7.5, -3.8, 8.6],
        minus: [10.3, 43.2, 8.2],
        l: [-4.5, 46.5, 0],
        zl: [-2.6, 44.8, -8.6],
        sl: [18.8, 25.3, 0.3],
        sr: [18.8, -19.1, 0.3],
        rail: [17.5, 2, 0.3],
        sync: [18.6, -6.8, 0.3],
      },
    },
    {
      file: "joycon-right.glb",
      mesh: "JoyconR_L1_Joycons_Buttons_0",
      frame: rightFrame,
      anchors: {
        stick: [0.7, -3.8, 10.5],
        a: [8.7, 26.6, 8.6],
        b: [0.6, 18.5, 8.6],
        x: [0.6, 34.6, 8.6],
        y: [-7.5, 26.6, 8.6],
        plus: [-10.3, 43.2, 7.9],
        home: [-5.5, -24, 7.8],
        r: [4.5, 46.5, 0],
        zr: [2.6, 44.8, -8.6],
        sl: [-18.8, -19.1, 0.4],
        sr: [-18.8, 25.3, 0.4],
        rail: [-17.5, 2, 0.4],
        sync: [-18.5, -6.8, 0.4],
      },
    },
  ],
};

const arrayTypes = {
  5121: Uint8Array,
  5123: Uint16Array,
  5125: Uint32Array,
  5126: Float32Array,
};
const componentOf = new Map(
  Object.entries(arrayTypes).map(([code, Type]) => [Type, Number(code)]),
);
const valueWidths = { SCALAR: 1, VEC2: 2, VEC3: 3, VEC4: 4 };

// Reads an accessor of the source glTF into a tightly packed typed array.
function readAccessor(j, bin, index) {
  const acc = j.accessors[index];
  const view = j.bufferViews[acc.bufferView];
  const Type = arrayTypes[acc.componentType];
  const width = valueWidths[acc.type];
  const stride = view.byteStride ?? width * Type.BYTES_PER_ELEMENT;
  const start = (view.byteOffset ?? 0) + (acc.byteOffset ?? 0);
  const data = new DataView(bin.buffer, bin.byteOffset, bin.byteLength);
  const read = `get${Type.name.replace("Array", "")}`;
  const result = new Type(acc.count * width);
  for (let i = 0; i < acc.count; i++) {
    for (let k = 0; k < width; k++) {
      const at = start + i * stride + k * Type.BYTES_PER_ELEMENT;
      result[i * width + k] = data[read](at, true);
    }
  }
  return result;
}

// The world matrix (column-major) of a node. The source only uses matrices.
function worldMatrix(j, index) {
  const node = j.nodes[index];
  if (node.translation || node.rotation || node.scale) {
    throw new Error(`node ${node.name} uses TRS, only matrices are handled`);
  }
  const m = node.matrix ?? [1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1];
  const parent = j.nodes.findIndex((n) => n.children?.includes(index));
  if (parent < 0) return m;
  const p = worldMatrix(j, parent);
  const out = new Array(16).fill(0);
  for (let c = 0; c < 4; c++) {
    for (let r = 0; r < 4; r++) {
      for (let k = 0; k < 4; k++) out[c * 4 + r] += p[k * 4 + r] * m[c * 4 + k];
    }
  }
  return out;
}

// The mesh in the canonical frame: world transform, then the frame rotation,
// then centring on the bounding box.
function canonicalGeometry(j, bin, side) {
  const node = j.nodes.findIndex(
    (n) => n.mesh !== undefined && j.meshes[n.mesh].name === side.mesh,
  );
  if (node < 0) throw new Error(`mesh ${side.mesh} not found`);
  const prim = j.meshes[j.nodes[node].mesh].primitives[0];
  const world = worldMatrix(j, node);
  const frame = side.frame;
  if (dot(frame[0], cross(frame[1], frame[2])) < 0.999) {
    throw new Error(`the frame of ${side.file} is not a rotation`);
  }
  // frame * (world linear part) as rows. The world scale is uniform, so
  // normals and tangents only need normalising afterwards.
  const map = frame.map((row) =>
    [0, 1, 2].map((c) => dot(row, world.slice(c * 4, c * 4 + 3))),
  );
  const apply = (rows, v) => rows.map((row) => dot(row, v));
  const shift = apply(frame, world.slice(12, 15));
  const attribute = (name) => readAccessor(j, bin, prim.attributes[name]);
  const srcPos = attribute("POSITION");
  const srcNormal = attribute("NORMAL");
  const srcTangent = attribute("TANGENT");
  const count = srcPos.length / 3;
  const pos = new Float32Array(count * 3);
  const normal = new Float32Array(count * 3);
  const tangent = new Float32Array(count * 4);
  for (let i = 0; i < count; i++) {
    const p = apply(map, srcPos.subarray(i * 3, i * 3 + 3));
    const n = normalize(apply(map, srcNormal.subarray(i * 3, i * 3 + 3)));
    const t = normalize(apply(map, srcTangent.subarray(i * 4, i * 4 + 3)));
    for (let k = 0; k < 3; k++) {
      pos[i * 3 + k] = p[k] + shift[k];
      normal[i * 3 + k] = n[k];
      tangent[i * 4 + k] = t[k];
    }
    tangent[i * 4 + 3] = srcTangent[i * 4 + 3];
  }
  const { low, high } = minMax(pos);
  for (let i = 0; i < count; i++) {
    for (let k = 0; k < 3; k++) pos[i * 3 + k] -= (low[k] + high[k]) / 2;
  }
  return {
    pos,
    normal,
    tangent,
    uv: attribute("TEXCOORD_0"),
    index: readAccessor(j, bin, prim.indices),
    material: prim.material,
  };
}

// Component-wise minimum and maximum of a flat list of 3D points.
function minMax(pos) {
  const low = [Infinity, Infinity, Infinity];
  const high = [-Infinity, -Infinity, -Infinity];
  for (let i = 0; i < pos.length; i += 3) {
    for (let k = 0; k < 3; k++) {
      low[k] = Math.min(low[k], pos[i + k]);
      high[k] = Math.max(high[k], pos[i + k]);
    }
  }
  return { low, high };
}

// Groups the triangles into connected components, largest first. Vertices
// closer than weld (metres) count as one, so UV seams do not cut a part in
// two. A component is the list of the index offsets of its triangles.
function connectedComponents(geo, weld = 1e-5) {
  const ids = new Map();
  const vertexId = new Int32Array(geo.pos.length / 3);
  for (let v = 0; v < vertexId.length; v++) {
    const key = [0, 1, 2]
      .map((k) => Math.round(geo.pos[v * 3 + k] / weld))
      .join(",");
    if (!ids.has(key)) ids.set(key, ids.size);
    vertexId[v] = ids.get(key);
  }
  const parent = Int32Array.from({ length: ids.size }, (_, i) => i);
  const find = (x) => {
    while (parent[x] !== x) {
      parent[x] = parent[parent[x]];
      x = parent[x];
    }
    return x;
  };
  for (let t = 0; t < geo.index.length; t += 3) {
    const root = find(vertexId[geo.index[t]]);
    for (let k = 1; k < 3; k++) {
      parent[find(vertexId[geo.index[t + k]])] = root;
    }
  }
  const groups = new Map();
  for (let t = 0; t < geo.index.length; t += 3) {
    const root = find(vertexId[geo.index[t]]);
    if (!groups.has(root)) groups.set(root, []);
    groups.get(root).push(t);
  }
  // Array.sort is stable, so equal sizes keep their source order.
  return [...groups.values()].sort((a, b) => b.length - a.length);
}

// The vertices of the given triangles, as flat positions.
function trianglePoints(geo, triangles) {
  const points = [];
  for (const t of triangles) {
    for (let k = 0; k < 3; k++) {
      points.push(
        ...geo.pos.subarray(geo.index[t + k] * 3, geo.index[t + k] * 3 + 3),
      );
    }
  }
  return points;
}

// Names every component: the largest is the shell, the others join the
// nearest anchor in reach, the rest become detail-N. Returns [name,
// triangles] pairs: shell, the anchored parts in table order, the details.
function nameParts(geo, side) {
  const components = connectedComponents(geo);
  const parts = new Map([["shell", components[0]]]);
  const details = [];
  for (const triangles of components.slice(1)) {
    const { low, high } = minMax(trianglePoints(geo, triangles));
    const centre = low.map((l, i) => ((l + high[i]) / 2) * 1000);
    let best;
    let bestDistance = anchorReach;
    for (const [name, anchor] of Object.entries(side.anchors)) {
      const d = Math.hypot(...anchor.map((a, i) => a - centre[i]));
      if (d < bestDistance) [best, bestDistance] = [name, d];
    }
    if (best === undefined) {
      details.push(triangles);
    } else {
      parts.set(best, [...(parts.get(best) ?? []), ...triangles]);
    }
  }
  const missing = Object.keys(side.anchors).filter((n) => !parts.has(n));
  if (missing.length > 0) {
    throw new Error(
      `${side.file}: no component near the anchor of ${missing.join(", ")}`,
    );
  }
  const named = ["shell", ...Object.keys(side.anchors)].map((name) => [
    name,
    parts.get(name),
  ]);
  details.forEach((triangles, i) => {
    named.push([`detail-${i + 1}`, triangles]);
  });
  return named;
}

// Copies the given triangles into their own compact vertex arrays.
function extractPart(geo, triangles) {
  const remap = new Map();
  const order = [];
  const index = [];
  for (const t of triangles) {
    for (let k = 0; k < 3; k++) {
      const v = geo.index[t + k];
      if (!remap.has(v)) {
        remap.set(v, order.length);
        order.push(v);
      }
      index.push(remap.get(v));
    }
  }
  const gather = (source, width) => {
    const out = new Float32Array(order.length * width);
    order.forEach((v, i) => {
      out.set(source.subarray(v * width, v * width + width), i * width);
    });
    return out;
  };
  const Index = order.length <= 0xffff ? Uint16Array : Uint32Array;
  return {
    pos: gather(geo.pos, 3),
    normal: gather(geo.normal, 3),
    tangent: gather(geo.tangent, 4),
    uv: gather(geo.uv, 2),
    index: Index.from(index),
  };
}

// The glTF of one Joy-Con: a node and a mesh per part, each part with its
// own material, a clone of the original that shares its textures.
function joyconDocument(source, geo, parts) {
  const j = {
    asset: {
      version: "2.0",
      generator: "padmate pack-controller-models",
      extras: { ...source.asset.extras, note: joycons.note },
    },
    scene: 0,
    scenes: [{ nodes: parts.map((_, i) => i) }],
    nodes: [],
    meshes: [],
    materials: [],
    accessors: [],
    bufferViews: [],
    textures: structuredClone(source.textures),
    images: structuredClone(source.images),
    samplers: structuredClone(source.samplers),
  };
  const binary = binaryBuilder(j);
  const accessor = (array, type, target, extra) => {
    j.accessors.push({
      bufferView: binary.add(
        Buffer.from(array.buffer, array.byteOffset, array.byteLength),
        target,
      ),
      componentType: componentOf.get(array.constructor),
      count: array.length / valueWidths[type],
      type,
      ...extra,
    });
    return j.accessors.length - 1;
  };
  for (const [name, triangles] of parts) {
    const part = extractPart(geo, triangles);
    const { low, high } = minMax(part.pos);
    const attributes = {
      POSITION: accessor(part.pos, "VEC3", 34962, { min: low, max: high }),
      NORMAL: accessor(part.normal, "VEC3", 34962),
      TANGENT: accessor(part.tangent, "VEC4", 34962),
      TEXCOORD_0: accessor(part.uv, "VEC2", 34962),
    };
    const indices = accessor(part.index, "SCALAR", 34963);
    j.materials.push({
      ...structuredClone(source.materials[geo.material]),
      name,
    });
    j.meshes.push({
      name,
      primitives: [
        { attributes, indices, material: j.materials.length - 1, mode: 4 },
      ],
    });
    j.nodes.push({ name, mesh: j.meshes.length - 1 });
  }
  prune(j);
  return { j, binary };
}

async function packJoycons(source) {
  const j = JSON.parse(await readFile(`${source.dir}/scene.gltf`, "utf8"));
  const bin = await readFile(`${source.dir}/scene.bin`);
  const sizes = {};
  for (const side of source.sides) {
    const geo = canonicalGeometry(j, bin, side);
    const doc = joyconDocument(j, geo, nameParts(geo, side));
    await embedImages(doc.j, source.dir, doc.binary);
    sizes[side.file] = await writeGlb(doc.j, doc.binary.finish(), side.file);
  }
  return sizes;
}

// ---- Joy-Cons from "Nintendo Switch 2 Realistic 3D Model" -----------------
//
// The source holds the console with both Joy-Cons attached: twenty meshes of
// the whole console, each a bag of separate pieces, none textured (a part's
// look is the base colour of its material). The pieces of a Joy-Con are told
// apart as connected components of the triangles, kept when they lie beside
// the console and matched by position to the anchors below, as for the Switch
// OLED, and each part gets one mesh, one node and one material.
//
// A Joy-Con 2 ends up in the same upright frame as the Switch OLED one: +Y
// the top (the stick end of the left Joy-Con, the ABXY end of the right one),
// +Z the front and the rail on +X for the left and -X for the right Joy-Con,
// centred on the bounding box of the kept parts, in metres. The source has no
// unit, so the scale comes from the console: the model is 1.041 wide with the
// Joy-Cons attached and the console is 272 mm wide, 0.2615 m per unit. The
// Joy-Con is then 115.8 mm long, the console's published height of 116 mm.
//
// The files are quantized (KHR_mesh_quantization): positions are 16-bit
// integers of `unit` metres (the scale of the part's node), normals 8-bit
// and indices 16-bit. A part's node sits at the part's origin; the stick's is
// where it turns: on its axis, at the bottom of its base (the flange where the
// stem leaves the shell), so the cap rocks about its base. The base is a part
// of its own and stays.
//
// Anchors are the centre of the part's bounding box in the frame above, in
// millimetres; a part of several pieces (the glyphs, the rail) has an anchor
// for each. The identities follow the shapes: the letters A, B, X and Y are
// modelled on the caps, the Home button has its house glyph and the C button
// is the plain square under it. The source models one piece for the shoulder
// (the big one that wraps the top edge and reaches behind the shell), none for
// SL and SR and none for ZL and ZR: those are `points`, empty nodes of the
// same names at the places they are on the real Joy-Con, which is where the
// controller drawing points its leaders (the shoulder piece serves L or R,
// ZL and ZR sit behind it).
const middle = (box, k) => (box.low[k] + box.high[k]) / 2;
// Where the points are, from the boxes of the parts (metres from the
// centre): SL and SR on the rail, ZL and ZR on the back of the shoulder.
const onRail = (y) => (boxes) => [
  middle(boxes.get("rail"), 0),
  y,
  middle(boxes.get("rail"), 2),
];
const behind = (name, from) => ({
  name,
  at: (boxes) => [
    middle(boxes.get(from), 0),
    middle(boxes.get(from), 1),
    boxes.get(from).low[2] + 0.002,
  ],
});
const stickAnchors = (x, y) => [
  { name: "stick", at: [x, y, 9.7] },
  { name: "stick-base", at: [x, y, 6.8] },
];
const railAnchors = (side) => [
  { name: "rail", at: [side * 17.3, -0.3, 0.6] },
  { name: "connector", at: [side * 18.3, -0.3, 0.6] },
  { name: "omit", at: [side * 19.7, -0.3, 0.6] },
  { name: "sync", at: [side * 20.0, 49.2, 0.6] },
  { name: "sync", at: [side * 20.2, 49.3, 0.6] },
];

const joycons2 = {
  dir: `${cache}/switch2`,
  note: "Extracted from the original model: only this Joy-Con, split into one mesh and material per part, moved into an upright frame, positions quantized.",
  metresPerUnit: 0.2615,
  unit: 5e-6,
  // The world frame of the source is Y up; the rows give the Joy-Con's x, y
  // and z axes in it.
  frame: [
    [0, 0, 1],
    [1, 0, 0],
    [0, 1, 0],
  ],
  // A component is part of a Joy-Con when its centre is further than this
  // (metres) to the right (right Joy-Con) or the left of the console's middle.
  sideStart: 0.092,
  // A component joins the nearest anchor within anchorReach (mm): a button
  // and the glyph on it are 1.8 apart. The others of at least keepTriangles
  // triangles become detail-N, the rest (contact pins, indicator dots) are
  // left out; an anchor named omit leaves its component out.
  anchorReach: 1.2,
  keepTriangles: 30,
  sides: [
    {
      file: "joycon2-left.glb",
      sign: -1,
      anchors: [
        ...stickAnchors(-3.0, 28.3),
        { name: "dpad-up", at: [-3.1, 7.0, 5.8] },
        { name: "dpad-down", at: [-2.8, -8.8, 5.8] },
        { name: "dpad-left", at: [-10.9, -0.8, 5.8] },
        { name: "dpad-right", at: [4.9, -1.0, 5.8] },
        { name: "glyphs", at: [-3.2, 7.5, 7.6] },
        { name: "glyphs", at: [-2.8, -9.2, 7.6] },
        { name: "glyphs", at: [-11.2, -0.7, 7.6] },
        { name: "glyphs", at: [5.4, -1.0, 7.6] },
        { name: "minus", at: [7.6, 44.7, 5.2] },
        { name: "capture", at: [4.6, -21.7, 4.4] },
        { name: "l", at: [-5.3, 50.0, -5.3] },
        ...railAnchors(1),
      ],
      points: [
        { name: "sl", at: onRail(0.03) },
        { name: "sr", at: onRail(-0.03) },
        behind("zl", "l"),
      ],
    },
    {
      file: "joycon2-right.glb",
      sign: 1,
      anchors: [
        ...stickAnchors(3.0, -0.9),
        { name: "a", at: [10.9, 28.6, 5.8] },
        { name: "b", at: [3.2, 20.8, 5.8] },
        { name: "x", at: [2.9, 36.7, 5.8] },
        { name: "y", at: [-4.9, 28.9, 5.8] },
        { name: "glyphs", at: [10.9, 28.6, 7.6] },
        { name: "glyphs", at: [3.3, 20.8, 7.6] },
        { name: "glyphs", at: [2.9, 36.7, 7.6] },
        { name: "glyphs", at: [-4.9, 28.9, 7.6] },
        { name: "glyphs", at: [-4.5, -22.1, 5.8] },
        { name: "glyphs", at: [-4.4, -21.3, 5.7] },
        { name: "plus", at: [-7.7, 44.7, 5.2] },
        { name: "home", at: [-4.5, -21.4, 3.2] },
        { name: "c", at: [-4.6, -33.6, 4.4] },
        { name: "r", at: [5.2, 50.0, -5.3] },
        ...railAnchors(-1),
      ],
      points: [
        { name: "sr", at: onRail(0.03) },
        { name: "sl", at: onRail(-0.03) },
        behind("zr", "r"),
      ],
    },
  ],
};

// The triangles of a mesh node in the Joy-Con frame, in metres: the node's
// world transform, the scale, then the frame. The world transform is a
// rotation (the source is a Sketchfab export), so normals take the same.
function sourceGeometry(j, bin, node, source) {
  const prim = j.meshes[j.nodes[node].mesh].primitives[0];
  const world = worldMatrix(j, node);
  const column = (c) => world.slice(c * 4, c * 4 + 3);
  const rows = source.frame.map((row) =>
    [0, 1, 2].map((c) => dot(row, column(c)) * source.metresPerUnit),
  );
  const shift = source.frame.map(
    (row) => dot(row, world.slice(12, 15)) * source.metresPerUnit,
  );
  const srcPos = readAccessor(j, bin, prim.attributes.POSITION);
  const srcNormal = readAccessor(j, bin, prim.attributes.NORMAL);
  const count = srcPos.length / 3;
  const pos = new Float32Array(count * 3);
  const normal = new Float32Array(count * 3);
  for (let i = 0; i < count; i++) {
    const p = srcPos.subarray(i * 3, i * 3 + 3);
    const n = srcNormal.subarray(i * 3, i * 3 + 3);
    const turned = normalize(
      source.frame.map((row) =>
        dot(
          row,
          [0, 1, 2].map((c) => dot(column(c), n)),
        ),
      ),
    );
    for (let k = 0; k < 3; k++) {
      pos[i * 3 + k] = dot(rows[k], p) + shift[k];
      normal[i * 3 + k] = turned[k];
    }
  }
  return {
    pos,
    normal,
    index: readAccessor(j, bin, prim.indices),
    material: prim.material,
  };
}

// Every component of every mesh of the source: its geometry, the offsets of
// its triangles, and its bounding box (metres).
function sourceComponents(j, bin, source) {
  const result = [];
  j.nodes.forEach((n, node) => {
    if (n.mesh === undefined) return;
    const geo = sourceGeometry(j, bin, node, source);
    for (const triangles of connectedComponents(geo)) {
      const { low, high } = minMax(trianglePoints(geo, triangles));
      result.push({ geo, triangles, low, high });
    }
  });
  return result;
}

const centreOf = ({ low, high }) => low.map((l, i) => (l + high[i]) / 2);

// Names the components of one Joy-Con 2. The ones beside the console, centred
// on their bounding box, go to the shell (the one of the biggest box), the
// anchored parts or detail-N. Returns the centre and the [name, components]
// pairs: shell, the anchored parts in table order, the details.
function nameParts2(components, source, side) {
  const own = components.filter(
    (c) => centreOf(c)[0] * side.sign > source.sideStart,
  );
  const low = [0, 1, 2].map((k) => Math.min(...own.map((c) => c.low[k])));
  const high = [0, 1, 2].map((k) => Math.max(...own.map((c) => c.high[k])));
  const centre = low.map((l, i) => (l + high[i]) / 2);
  const volume = (c) => c.high.reduce((v, h, k) => v * (h - c.low[k]), 1);
  const shell = own.reduce((a, b) => (volume(b) > volume(a) ? b : a));
  const parts = new Map([["shell", [shell]]]);
  const details = [];
  const omitted = [];
  for (const c of own) {
    if (c === shell) continue;
    const at = centreOf(c).map((v, k) => (v - centre[k]) * 1000);
    let best;
    let bestDistance = source.anchorReach;
    for (const anchor of side.anchors) {
      const d = Math.hypot(...anchor.at.map((a, k) => a - at[k]));
      if (d < bestDistance) [best, bestDistance] = [anchor.name, d];
    }
    if (best === "omit") continue;
    if (best !== undefined) {
      parts.set(best, [...(parts.get(best) ?? []), c]);
    } else if (c.triangles.length >= source.keepTriangles) {
      details.push(c);
    } else {
      omitted.push(c);
    }
  }
  if (process.env.PADMATE_PACK_LIST) {
    const row = (name, c) =>
      console.log(
        `${side.file} ${name.padEnd(12)} ${String(c.triangles.length).padStart(5)} tris at ${centreOf(
          c,
        )
          .map((v, k) => ((v - centre[k]) * 1000).toFixed(1).padStart(7))
          .join(" ")}  size ${c.high
          .map((h, k) => ((h - c.low[k]) * 1000).toFixed(1).padStart(5))
          .join(" ")}`,
      );
    for (const [name, list] of parts) for (const c of list) row(name, c);
    for (const c of details) row("detail", c);
    for (const c of omitted) row("(left out)", c);
  }
  const wanted = [
    ...new Set(side.anchors.map((a) => a.name).filter((n) => n !== "omit")),
  ];
  const missing = wanted.filter((name) => !parts.has(name));
  if (missing.length > 0) {
    throw new Error(
      `${side.file}: no component near the anchor of ${missing.join(", ")}`,
    );
  }
  const named = ["shell", ...wanted].map((name) => [name, parts.get(name)]);
  for (const [i, c] of details.entries()) {
    named.push([`detail-${i + 1}`, [c]]);
  }
  return { centre, parts: named };
}

// The bounding box (metres, the Joy-Con's centre at the origin) of a part.
function partBox(components, centre) {
  const low = [0, 1, 2].map((k) =>
    Math.min(...components.map((c) => c.low[k])),
  );
  const high = [0, 1, 2].map((k) =>
    Math.max(...components.map((c) => c.high[k])),
  );
  return {
    low: low.map((v, k) => v - centre[k]),
    high: high.map((v, k) => v - centre[k]),
  };
}

// The glTF of one Joy-Con 2: a node and a mesh per part, each with its own
// material (a clone of the source's), and an empty node for each point.
function joycon2Document(source, side, centre, parts) {
  const unit = joycons2.unit;
  const j = {
    asset: {
      version: "2.0",
      generator: "padmate pack-controller-models",
      extras: { ...source.asset.extras, note: joycons2.note },
    },
    extensionsUsed: ["KHR_mesh_quantization"],
    extensionsRequired: ["KHR_mesh_quantization"],
    scene: 0,
    scenes: [{ nodes: [] }],
    nodes: [],
    meshes: [],
    materials: [],
    accessors: [],
    bufferViews: [],
  };
  const binary = binaryBuilder(j);
  const view = (array, target, stride) =>
    binary.add(
      Buffer.from(array.buffer, array.byteOffset, array.byteLength),
      target,
      stride,
    );
  const addNode = (node) => {
    j.nodes.push(node);
    j.scenes[0].nodes.push(j.nodes.length - 1);
  };
  const boxes = new Map(
    parts.map(([name, components]) => [name, partBox(components, centre)]),
  );
  for (const [name, components] of parts) {
    // The part's origin, in metres from the Joy-Con's centre: the stick's is
    // on its axis, at the bottom of its base; every other part's is the centre.
    const { low, high } = boxes.get(name);
    const origin =
      name === "stick"
        ? [
            (low[0] + high[0]) / 2,
            (low[1] + high[1]) / 2,
            boxes.get("stick-base").low[2],
          ]
        : [0, 0, 0];
    const pos = [];
    const normal = [];
    const index = [];
    for (const { geo, triangles } of components) {
      const remap = new Map();
      for (const t of triangles) {
        for (let k = 0; k < 3; k++) {
          const v = geo.index[t + k];
          if (!remap.has(v)) {
            remap.set(v, pos.length / 4);
            for (let c = 0; c < 3; c++) {
              const at = geo.pos[v * 3 + c] - centre[c] - origin[c];
              pos.push(Math.round(at / unit));
              normal.push(Math.round(geo.normal[v * 3 + c] * 127));
            }
            // Four bytes per normal and eight per position keep every vertex
            // aligned to four bytes, as glTF requires.
            pos.push(0);
            normal.push(0);
          }
          index.push(remap.get(v));
        }
      }
    }
    const count = pos.length / 4;
    if (count > 0xffff) throw new Error(`${side.file}: ${name} is too big`);
    const coordinate = (c) => pos.filter((_, i) => i % 4 === c);
    j.accessors.push({
      bufferView: view(Int16Array.from(pos), 34962, 8),
      componentType: 5122,
      count,
      type: "VEC3",
      min: [0, 1, 2].map((c) => Math.min(...coordinate(c))),
      max: [0, 1, 2].map((c) => Math.max(...coordinate(c))),
    });
    const position = j.accessors.length - 1;
    j.accessors.push({
      bufferView: view(Int8Array.from(normal), 34962, 4),
      componentType: 5120,
      normalized: true,
      count,
      type: "VEC3",
    });
    const normals = j.accessors.length - 1;
    j.accessors.push({
      bufferView: view(Uint16Array.from(index), 34963),
      componentType: 5123,
      count: index.length,
      type: "SCALAR",
    });
    j.materials.push({
      ...structuredClone(source.materials[components[0].geo.material]),
      name,
    });
    j.meshes.push({
      name,
      primitives: [
        {
          attributes: { POSITION: position, NORMAL: normals },
          indices: j.accessors.length - 1,
          material: j.materials.length - 1,
          mode: 4,
        },
      ],
    });
    addNode({
      name,
      mesh: j.meshes.length - 1,
      scale: [unit, unit, unit],
      ...(name === "stick" ? { translation: origin } : {}),
    });
  }
  for (const point of side.points) {
    addNode({ name: point.name, translation: point.at(boxes) });
  }
  return { j, binary };
}

async function packJoycons2(source) {
  const j = JSON.parse(await readFile(`${source.dir}/scene.gltf`, "utf8"));
  const bin = await readFile(`${source.dir}/scene.bin`);
  const components = sourceComponents(j, bin, joycons2);
  const sizes = {};
  for (const side of joycons2.sides) {
    const { centre, parts } = nameParts2(components, joycons2, side);
    const doc = joycon2Document(j, side, centre, parts);
    sizes[side.file] = await writeGlb(doc.j, doc.binary.finish(), side.file);
  }
  return sizes;
}

await mkdir(out, { recursive: true });
const sizes = {};
for (const model of models) {
  if (!wanted(model.name)) continue;
  if (!(await exists(`${model.dir}/scene.gltf`))) {
    console.log(`skip ${model.name}: no source in ${model.dir}`);
    continue;
  }
  sizes[model.file] = await pack(model);
}
if (!wanted("dualsense")) {
  // Not asked for.
} else if (await exists(`${cache}/dualsense.glb`)) {
  await copyFile(`${cache}/dualsense.glb`, `${out}/dualsense.glb`);
  sizes["dualsense.glb"] = (await stat(`${out}/dualsense.glb`)).size;
} else {
  console.log(`skip dualsense: no source at ${cache}/dualsense.glb`);
}
if (!wanted("switch-oled")) {
  // Not asked for.
} else if (await exists(`${joycons.dir}/scene.gltf`)) {
  Object.assign(sizes, await packJoycons(joycons));
} else {
  console.log(`skip switch-oled: no source in ${joycons.dir}`);
}
if (!wanted("switch2")) {
  // Not asked for.
} else if (await exists(`${joycons2.dir}/scene.gltf`)) {
  Object.assign(sizes, await packJoycons2(joycons2));
} else {
  console.log(`skip switch2: no source in ${joycons2.dir}`);
}
console.log(sizes);
