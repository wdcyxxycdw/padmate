// Build-time tool: turns the owner's two payment QR code images into the
// site/src/qr-codes.js module the page embeds.
//
//   node tools/qr.mjs --wechat <image> --alipay <image> [--out <file>] [--embed-on-fail]
//
// Run it from site/. Why decode and redraw instead of embedding the
// screenshots: the page's Content-Security-Policy forbids style attributes and
// external URLs, and a phone screenshot is a large, soft raster with a lot of
// margin. Reading the payload out of it and drawing the code again gives a
// tiny, razor-sharp SVG that scales to any size the page wants. The redrawn
// code is decoded again before anything is written, so a bug here can never
// ship a code that points somewhere else.
//
// The dependencies (jsqr, pngjs, jpeg-js, qrcode) are dev-only. The Worker
// itself only ever imports the generated qr-codes.js and has no node_modules.

import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";
import jpeg from "jpeg-js";
import jsQR from "jsqr";
import pngjs from "pngjs";
import QRCode from "qrcode";

const { PNG } = pngjs;

const SITE_DIR = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const DEFAULT_OUT = resolve(SITE_DIR, "src/qr-codes.js");
const PAYLOADS_FILE = resolve(SITE_DIR, "qr/payloads.json");

/** Largest payload we accept; real payment codes are well under 200 characters. */
const MAX_PAYLOAD = 1000;
/** Phone screenshots are wider than this; jsQR copes far better below it. */
const DECODE_MAX_SIDE = 1200;
/** Do not shrink below this when hunting for a decodable scale. */
const DECODE_MIN_SIDE = 300;
const QUIET_ZONE = 3;
const VERIFY_SCALE = 8;

/**
 * Per-code settings. The prefixes are only a sanity check: a payment link that
 * does not look like the vendor's is worth a warning, not a failure, because
 * the vendors have changed their link formats before.
 */
const KINDS = {
  wechat: { label: "微信收款码", prefixes: ["wxp://", "https://"] },
  alipay: {
    label: "支付宝收款码",
    prefixes: ["https://qr.alipay.com/", "https://"],
  },
};

/** Control characters and the two Unicode line separators; any of them ends a // comment. */
const LINE_BREAKS = new RegExp(
  `[\\x00-\\x1f${String.fromCharCode(0x2028, 0x2029)}]`,
  "g",
);

class UserError extends Error {}

/**
 * Reads a PNG or JPEG into an RGBA buffer, choosing the decoder by magic
 * bytes because screenshots are often saved under the wrong extension.
 *
 * @param {Buffer} buf
 * @returns {{ width: number, height: number, data: Uint8ClampedArray, mime: string }}
 */
function readImage(buf) {
  let width;
  let height;
  let rgba;
  let mime;
  if (
    buf.length > 8 &&
    buf.subarray(0, 8).equals(Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]))
  ) {
    const png = PNG.sync.read(buf);
    ({ width, height } = png);
    rgba = png.data;
    mime = "image/png";
  } else if (buf.length > 3 && buf[0] === 0xff && buf[1] === 0xd8 && buf[2] === 0xff) {
    const img = jpeg.decode(buf, { useTArray: true, formatAsRGBA: true });
    ({ width, height } = img);
    rgba = img.data;
    mime = "image/jpeg";
  } else {
    throw new UserError("not a PNG or JPEG file (checked the magic bytes)");
  }
  // Flatten transparency onto white: jsQR ignores alpha, and the RGB under a
  // transparent pixel is often black, which would turn a clear background into
  // a solid block.
  const data = new Uint8ClampedArray(width * height * 4);
  for (let i = 0; i < data.length; i += 4) {
    const a = rgba[i + 3] / 255;
    data[i] = Math.round(rgba[i] * a + 255 * (1 - a));
    data[i + 1] = Math.round(rgba[i + 1] * a + 255 * (1 - a));
    data[i + 2] = Math.round(rgba[i + 2] * a + 255 * (1 - a));
    data[i + 3] = 255;
  }
  return { width, height, data, mime };
}

/** Halves an image by averaging 2x2 blocks (an odd last row or column is dropped). */
function halve({ width, height, data }) {
  const w = width >> 1;
  const h = height >> 1;
  const out = new Uint8ClampedArray(w * h * 4);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      for (let c = 0; c < 4; c++) {
        const i = ((2 * y) * width + 2 * x) * 4 + c;
        out[(y * w + x) * 4 + c] =
          (data[i] + data[i + 4] + data[i + width * 4] + data[i + width * 4 + 4] + 2) >> 2;
      }
    }
  }
  return { width: w, height: h, data: out };
}

function decodeOnce({ width, height, data }) {
  const r = jsQR(data, width, height, { inversionAttempts: "attemptBoth" });
  return r ? r.data : null;
}

/**
 * Decodes the QR code in an image: first at full size, then on copies halved
 * until they are small enough for jsQR's finder-pattern search to cope with a
 * phone screenshot (and a little further, since a very sharp code sometimes
 * only locks on at a lower scale).
 *
 * @returns {string | null}
 */
function decodeQR(img) {
  let cur = img;
  let payload = decodeOnce(cur);
  if (payload) return payload;
  for (;;) {
    const side = Math.max(cur.width, cur.height);
    if (side <= DECODE_MIN_SIDE * 2 && side <= DECODE_MAX_SIDE) break;
    cur = halve(cur);
    if (Math.max(cur.width, cur.height) > DECODE_MAX_SIDE) continue;
    payload = decodeOnce(cur);
    if (payload) return payload;
  }
  return null;
}

/**
 * Draws the module matrix as an SVG with no width/height/style attributes (the
 * page's CSP allows none, and CSS sizes the box). Dark modules are merged into
 * one horizontal rectangle per run to keep the path short.
 */
function buildSvg(qr, label) {
  const size = qr.modules.size;
  const n = size + 2 * QUIET_ZONE;
  let d = "";
  for (let y = 0; y < size; y++) {
    let x = 0;
    while (x < size) {
      if (!qr.modules.get(y, x)) {
        x++;
        continue;
      }
      let w = 1;
      while (x + w < size && qr.modules.get(y, x + w)) w++;
      d += `M${x + QUIET_ZONE} ${y + QUIET_ZONE}h${w}v1h-${w}z`;
      x += w;
    }
  }
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${n} ${n}" role="img" aria-label="${label}" shape-rendering="crispEdges"><rect width="${n}" height="${n}" fill="#fff"/><path fill="#000" d="${d}"/></svg>`;
}

/** Rasterizes the matrix (plus quiet zone) so the redrawn code can be decoded. */
function rasterize(qr) {
  const size = qr.modules.size;
  const n = size + 2 * QUIET_ZONE;
  const px = n * VERIFY_SCALE;
  const data = new Uint8ClampedArray(px * px * 4).fill(255);
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      if (!qr.modules.get(y, x)) continue;
      for (let dy = 0; dy < VERIFY_SCALE; dy++) {
        for (let dx = 0; dx < VERIFY_SCALE; dx++) {
          const i =
            (((y + QUIET_ZONE) * VERIFY_SCALE + dy) * px + (x + QUIET_ZONE) * VERIFY_SCALE + dx) * 4;
          data[i] = 0;
          data[i + 1] = 0;
          data[i + 2] = 0;
        }
      }
    }
  }
  return { width: px, height: px, data };
}

/** Everything for one code; returns what goes into the module and the record. */
function processOne(kind, file, embedOnFail) {
  const { label, prefixes } = KINDS[kind];
  const tag = `[${kind}]`;
  let buf;
  try {
    buf = readFileSync(file);
  } catch (e) {
    throw new UserError(`${tag} cannot read ${file}: ${e.message}`);
  }
  let img;
  try {
    img = readImage(buf);
  } catch (e) {
    throw new UserError(`${tag} ${file}: ${e.message}`);
  }
  console.log(`${tag} ${file}: ${img.mime}, ${img.width}x${img.height}`);

  const payload = decodeQR(img);
  if (payload === null) {
    if (!embedOnFail) {
      throw new UserError(
        `${tag} no QR code could be decoded from ${file}.\n` +
          "  Crop the image to just the code (plus a little white margin) and try again,\n" +
          "  or pass --embed-on-fail to embed the image as it is.",
      );
    }
    console.warn(
      `\n!!! ${tag} NO QR CODE COULD BE DECODED: EMBEDDING THE IMAGE AS IT IS !!!\n` +
        "!!! Crop it to just the code first, or the page will show the whole screenshot. !!!\n",
    );
    return {
      html: `<img src="data:${img.mime};base64,${buf.toString("base64")}" alt="${label}">`,
      payload: null,
    };
  }

  console.log(`${tag} payload: ${payload}`);
  if (payload.length === 0) throw new UserError(`${tag} the decoded payload is empty`);
  if (payload.length > MAX_PAYLOAD) {
    throw new UserError(`${tag} the decoded payload is ${payload.length} characters (max ${MAX_PAYLOAD})`);
  }
  if (!prefixes.some((p) => payload.startsWith(p))) {
    console.warn(
      `${tag} WARNING: the payload does not start with ${prefixes.map((p) => `"${p}"`).join(" or ")}; check it is the right code`,
    );
  }

  const qr = QRCode.create(payload, { errorCorrectionLevel: "M" });
  const again = decodeOnce(rasterize(qr));
  if (again !== payload) {
    throw new UserError(
      `${tag} round trip failed: redrew the code but it decodes to ${JSON.stringify(again)}, not the original payload`,
    );
  }
  console.log(`${tag} round trip ok: ${qr.modules.size}x${qr.modules.size} modules, version ${qr.version}`);
  return { html: buildSvg(qr, label), payload };
}

/** A payload (or "embedded image") for a one-line comment. */
function commentText(payload) {
  // Line separators would end the comment early; the payload is only shown.
  return payload === null ? "embedded image" : payload.replace(LINE_BREAKS, " ");
}

function main() {
  const { values } = parseArgs({
    options: {
      wechat: { type: "string" },
      alipay: { type: "string" },
      out: { type: "string" },
      "embed-on-fail": { type: "boolean", default: false },
    },
    strict: true,
  });
  if (!values.wechat || !values.alipay) {
    throw new UserError(
      "both --wechat <image> and --alipay <image> are required (a half-updated file is never written)\n" +
        "usage: node tools/qr.mjs --wechat <image> --alipay <image> [--out <file>] [--embed-on-fail]",
    );
  }
  const out = values.out ? resolve(values.out) : DEFAULT_OUT;
  const isDefaultOut = out === DEFAULT_OUT;

  // Both codes are processed before anything is written, so a failure in the
  // second leaves the first run's file untouched.
  const wechat = processOne("wechat", values.wechat, values["embed-on-fail"]);
  const alipay = processOne("alipay", values.alipay, values["embed-on-fail"]);

  const source = `// Generated by site/tools/qr.mjs; do not edit. Rerun it with the owner's images:
//   task site:qr -- --wechat <image> --alipay <image>
// wechat: ${commentText(wechat.payload)}
// alipay: ${commentText(alipay.payload)}

export const QR_CODES = {
  wechat: ${JSON.stringify(wechat.html)},
  alipay: ${JSON.stringify(alipay.html)},
};
`;
  mkdirSync(dirname(out), { recursive: true });
  writeFileSync(out, source, "utf8");
  console.log(`wrote ${out}`);

  if (isDefaultOut) {
    mkdirSync(dirname(PAYLOADS_FILE), { recursive: true });
    const record = { wechat: wechat.payload, alipay: alipay.payload };
    writeFileSync(PAYLOADS_FILE, `${JSON.stringify(record, null, 2)}\n`, "utf8");
    console.log(`wrote ${PAYLOADS_FILE}`);
  }
}

try {
  main();
} catch (e) {
  if (e instanceof UserError || e?.code === "ERR_PARSE_ARGS_UNKNOWN_OPTION" || e?.code === "ERR_PARSE_ARGS_INVALID_OPTION_VALUE") {
    console.error(`error: ${e.message}`);
    process.exit(1);
  }
  throw e;
}
