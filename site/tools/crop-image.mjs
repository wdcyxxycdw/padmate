// Crops a square out of a payment-code image and scales it down, for
// a code that tools/qr.mjs cannot redraw: WeChat's round 赞赏码 is a mini
// program code, not a QR code, so the page shows the picture itself
// (qr.mjs --embed-on-fail). The square should hold the code with a white
// margin around it, wide enough for a phone to scan, and nothing else of the
// picture (titles, banners, names).
//
//   node tools/crop-image.mjs --in <png|jpeg> --out <file> --box X,Y,SIDE [--size 384] [--jpeg 90]
//
// The output is a PNG, or with --jpeg a JPEG of that quality: a code with a
// photo in it (WeChat puts the avatar in the middle) is several times smaller
// as a JPEG, and the page carries it inline.
//
// The scale-down averages every source pixel that falls into a target pixel,
// so the strokes stay smooth; near-white is made pure white so that JPEG noise
// in the margin does not show as a grey square on the page.

import { readFileSync, writeFileSync } from "node:fs";
import { parseArgs } from "node:util";
import jpeg from "jpeg-js";
import { PNG } from "pngjs";

const { values } = parseArgs({
  options: {
    in: { type: "string" },
    out: { type: "string" },
    box: { type: "string" },
    size: { type: "string", default: "384" },
    jpeg: { type: "string" },
  },
});
if (!values.in || !values.out || !values.box) {
  console.error(
    "usage: node tools/crop-image.mjs --in <image> --out <file> --box X,Y,SIDE [--size N] [--jpeg QUALITY]",
  );
  process.exit(2);
}

/** RGBA pixels of a PNG or JPEG, told apart by their first bytes. */
function readImage(path) {
  const bytes = readFileSync(path);
  if (bytes[0] === 0x89 && bytes[1] === 0x50) return PNG.sync.read(bytes);
  if (bytes[0] === 0xff && bytes[1] === 0xd8)
    return jpeg.decode(bytes, { useTArray: true, formatAsRGBA: true });
  throw new Error(`${path} is not a PNG or JPEG`);
}

const src = readImage(values.in);
const [bx, by, side] = values.box.split(",").map(Number);
const size = Number(values.size);
if (
  ![bx, by, side, size].every(Number.isInteger) ||
  bx < 0 ||
  by < 0 ||
  side <= 0 ||
  size <= 0 ||
  bx + side > src.width ||
  by + side > src.height
)
  throw new Error(
    `--box ${values.box} --size ${values.size} does not fit the ${src.width}x${src.height} image`,
  );

const out = new PNG({ width: size, height: size });
const scale = side / size;
for (let ty = 0; ty < size; ty++) {
  for (let tx = 0; tx < size; tx++) {
    const sx0 = bx + Math.floor(tx * scale);
    const sy0 = by + Math.floor(ty * scale);
    const sx1 = Math.max(sx0 + 1, bx + Math.floor((tx + 1) * scale));
    const sy1 = Math.max(sy0 + 1, by + Math.floor((ty + 1) * scale));
    let r = 0;
    let g = 0;
    let b = 0;
    let n = 0;
    for (let sy = sy0; sy < sy1; sy++) {
      for (let sx = sx0; sx < sx1; sx++) {
        const i = (sy * src.width + sx) * 4;
        r += src.data[i];
        g += src.data[i + 1];
        b += src.data[i + 2];
        n++;
      }
    }
    const o = (ty * size + tx) * 4;
    const px = [r / n, g / n, b / n].map(Math.round);
    const white = px.every((v) => v >= 245);
    out.data[o] = white ? 255 : px[0];
    out.data[o + 1] = white ? 255 : px[1];
    out.data[o + 2] = white ? 255 : px[2];
    out.data[o + 3] = 255;
  }
}
const quality = values.jpeg === undefined ? null : Number(values.jpeg);
if (quality !== null && !(quality >= 1 && quality <= 100))
  throw new Error(`--jpeg ${values.jpeg} is not a quality from 1 to 100`);
const bytes =
  quality === null
    ? PNG.sync.write(out, { colorType: 2 })
    : jpeg.encode({ data: out.data, width: size, height: size }, quality).data;
writeFileSync(values.out, bytes);
console.log(`${values.out}: ${size}x${size}, ${bytes.length} bytes`);
