import { copyFile, mkdir, readFile, writeFile } from "node:fs/promises";

const out = "app/src/renderer/assets";
await mkdir(out, { recursive: true });
const dir = ".cache/asset-candidates/xbox";
const j = JSON.parse(await readFile(`${dir}/scene.gltf`, "utf8"));
const chunks = [await readFile(`${dir}/scene.bin`)];
let length = chunks[0].length;
for (const img of j.images) {
  const data = await readFile(`${dir}/${img.uri}`);
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
await writeFile(`${out}/xbox.glb`, glb);
await copyFile(".cache/asset-candidates/dualsense.glb", `${out}/dualsense.glb`);
console.log({ xboxBytes: glb.length });
