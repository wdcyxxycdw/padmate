// Publishes a release to the download site (ADR-0078): the installer, its
// .sha256 and latest.yml go to the root of the Cloudflare R2 bucket that
// https://padmate.download/ serves. The installed application reads latest.yml
// there (the generic provider of electron-updater), and a Worker on the path /
// alone redirects the address itself to the installer latest.yml names.
//
//   node scripts/publish-r2.mjs --version v1.0.3 [--dist DIR] [--dry-run] [--force]
//
// `task release:r2 VERSION=v1.0.3` runs it, with the files `task
// package:installer` left in dist/installer. Nothing uploads as a side effect
// of a build: this is the one place that writes to the bucket, and only when a
// person runs it.
//
// The upload is the wrangler of the release machine, pinned to one version and
// logged in already (`wrangler login`): this file reads no credential and the
// repository holds none.
//
// The rules, each of them a refusal before anything is written:
//   - a version with a prerelease part (v1.0.3-rc.1) never goes to the site;
//   - the public code mirror (.padmate-public) has no release to publish;
//   - latest.yml must name this version and this installer, and carry its
//     SHA-512, which is all an unsigned build is checked by;
//   - a published installer never changes (the CDN caches it for a year, and
//     latest.yml of every client carries its hash): one that is on the site
//     already is not uploaded again;
//   - a version that is not newer than the latest.yml on the site would send
//     everybody who opens the address back to an older installer (--force
//     overrides this one rule).
//
// The order is the installer, its .sha256, then latest.yml last: the file that
// clients and the Worker follow must not point at something that is not there
// yet. Afterwards the site is asked what it serves.
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { createReadStream, existsSync } from "node:fs";
import { readFile, writeFile } from "node:fs/promises";
import { dirname, join, resolve, win32 } from "node:path";
import { fileURLToPath } from "node:url";
import { refusePackaging, repoRoot } from "./public-mirror.mjs";

const here = dirname(fileURLToPath(import.meta.url));

/** The address of the download site; app/src/main/update-config.ts and Go's installerpkg.UpdateURL say the same. */
export const SITE = "https://padmate.download/";
/** The R2 bucket the site serves. */
export const BUCKET = "padmate";
/** The wrangler that uploads, one exact version, run by npx. */
export const WRANGLER = "wrangler@4.151.1";
/** An installer or a checksum never changes once it is published. */
export const IMMUTABLE = "public, max-age=31536000, immutable";

const installerType = "application/vnd.microsoft.portable-executable";
const checksumType = "text/plain; charset=utf-8";
const latestType = "text/yaml; charset=utf-8";

const usage =
  "usage: node scripts/publish-r2.mjs --version vX.Y.Z [--dist DIR] [--dry-run] [--force]";

// The same rule as daemon/internal/installerpkg.ParseTag: v, MAJOR.MINOR.PATCH
// and an optional -prerelease.
const tag =
  /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$/;

/**
 * A stable release tag, and what it names.
 *
 * @param {unknown} label
 * @returns {{ label: string, version: string, installerName: string }}
 */
export function checkTag(label) {
  const match = typeof label === "string" ? tag.exec(label) : null;
  if (!match || label.length > 64)
    throw new Error(
      `version ${JSON.stringify(label)} is not a release tag: use v and a semantic version, such as v1.0.3`,
    );
  if (match[4] !== undefined)
    throw new Error(
      `version ${label} has a prerelease part: a prerelease is never published to the download site (it is for GitHub Releases)`,
    );
  return {
    label,
    version: label.slice(1),
    installerName: `padmate-${label}-windows-amd64-setup.exe`,
  };
}

/**
 * The command line: --version is required, the rest are optional and no
 * flag is given twice.
 *
 * @param {string[]} argv
 * @returns {{ label: string, dist: string | undefined, dryRun: boolean, force: boolean }}
 */
export function parseArgs(argv) {
  const parsed = {
    label: undefined,
    dist: undefined,
    dryRun: false,
    force: false,
  };
  const seen = new Set();
  for (let i = 0; i < argv.length; i++) {
    const flag = argv[i];
    if (seen.has(flag)) throw new Error(usage);
    seen.add(flag);
    if (flag === "--dry-run") parsed.dryRun = true;
    else if (flag === "--force") parsed.force = true;
    else if (flag === "--version" || flag === "--dist") {
      const value = argv[++i];
      if (value === undefined || value.startsWith("--")) throw new Error(usage);
      if (flag === "--version") parsed.label = value;
      else parsed.dist = value;
    } else throw new Error(usage);
  }
  if (parsed.label === undefined) throw new Error(usage);
  return parsed;
}

/**
 * The line of a .sha256 file, as sha256sum writes it: the hash, two spaces
 * and the file name.
 *
 * @param {string} hex
 * @param {string} fileName
 */
export function sha256Line(hex, fileName) {
  return `${hex}  ${fileName}\n`;
}

/**
 * The size, the SHA-256 (hex, for the .sha256 file) and the SHA-512 (base64,
 * what latest.yml carries) of a file, read once.
 *
 * @param {string} file
 * @returns {Promise<{ size: number, sha256: string, sha512: string }>}
 */
export async function inspectFile(file) {
  const sha256 = createHash("sha256");
  const sha512 = createHash("sha512");
  let size = 0;
  for await (const chunk of createReadStream(file)) {
    sha256.update(chunk);
    sha512.update(chunk);
    size += chunk.length;
  }
  return {
    size,
    sha256: sha256.digest("hex"),
    sha512: sha512.digest("base64"),
  };
}

/**
 * The objects to upload, in the order of the upload: latest.yml is last.
 *
 * @param {string} label a stable tag
 * @param {string} dist the folder with the files of the release
 * @returns {{ role: string, key: string, file: string, contentType: string, cacheControl: string }[]}
 */
export function planUpload(label, dist) {
  const { installerName } = checkTag(label);
  const step = (role, key, contentType, cacheControl) => ({
    role,
    key,
    file: join(dist, key),
    contentType,
    cacheControl,
  });
  return [
    step("installer", installerName, installerType, IMMUTABLE),
    step("checksum", `${installerName}.sha256`, checksumType, IMMUTABLE),
    // Never cached: it is the one object that changes with every release.
    step("latest", "latest.yml", latestType, "no-cache"),
  ];
}

/** The arguments of npx for one object: wrangler's own `r2 object put`. */
export function wranglerArgs({ key, file, contentType, cacheControl }) {
  return [
    "--yes",
    WRANGLER,
    "r2",
    "object",
    "put",
    `${BUCKET}/${key}`,
    "--file",
    file,
    "--remote",
    "--content-type",
    contentType,
    "--cache-control",
    cacheControl,
  ];
}

/**
 * What to spawn for `npx`. Node refuses to spawn a .cmd without a shell, and a
 * shell would have to quote the content type and the cache control, so on
 * Windows npx-cli.js of the npm that comes with node is run by node itself.
 *
 * @param {{ execPath?: string, platform?: string, exists?: (file: string) => boolean }} [options]
 * @returns {{ command: string, prefix: string[] }}
 */
export function npxInvocation({
  execPath = process.execPath,
  platform = process.platform,
  exists = existsSync,
} = {}) {
  if (platform !== "win32") return { command: "npx", prefix: [] };
  const cli = win32.join(
    win32.dirname(execPath),
    "node_modules",
    "npm",
    "bin",
    "npx-cli.js",
  );
  if (!exists(cli))
    throw new Error(
      `${cli} (npx-cli.js of npm) is missing: wrangler is run through npx`,
    );
  return { command: execPath, prefix: [cli] };
}

/**
 * Uploads one object with wrangler, and resolves when it exits with 0. Its
 * output is the terminal's; the login is the one wrangler keeps on this
 * machine, and nothing about it passes through here.
 *
 * @param {{ key: string, file: string, contentType: string, cacheControl: string }} step
 */
export function wranglerPut(step) {
  const { command, prefix } = npxInvocation();
  return new Promise((done, reject) => {
    const child = spawn(command, [...prefix, ...wranglerArgs(step)], {
      cwd: resolve(here, ".."),
      windowsHide: true,
      shell: false,
      stdio: "inherit",
    });
    child.on("error", reject);
    child.on("close", (code) => {
      if (code === 0) done();
      else reject(new Error(`wrangler failed on ${step.key} (${code})`));
    });
  });
}

const unquote = (value) => {
  const v = value.trim();
  const quote = v[0];
  return (quote === "'" || quote === '"') && v.length >= 2 && v.at(-1) === quote
    ? v.slice(1, -1)
    : v;
};

/**
 * Reads latest.yml as electron-builder writes it: a few `key: value` lines and
 * a `files:` list of `- url:` entries with their sha512. It is not a YAML
 * parser, and daemon/internal/installerpkg checks the same file strictly at
 * build time; this only has to find what a publish compares.
 *
 * @param {string} text
 * @returns {{ version?: string, path?: string, sha512?: string, files: { url?: string, sha512?: string }[] }}
 */
export function parseLatest(text) {
  const info = { files: [] };
  let inFiles = false;
  for (const line of text.split(/\r?\n/)) {
    if (line.trim() === "" || line.trimStart().startsWith("#")) continue;
    if (!/^\s/.test(line)) {
      inFiles = false;
      const key = /^([A-Za-z][A-Za-z0-9]*):\s*(.*)$/.exec(line);
      if (!key) continue;
      if (key[1] === "files") inFiles = true;
      else if (["version", "path", "sha512"].includes(key[1]))
        info[key[1]] = unquote(key[2]);
      continue;
    }
    if (!inFiles) continue;
    const entry = /^\s*-\s+url:\s*(.*)$/.exec(line);
    if (entry) {
      info.files.push({ url: unquote(entry[1]) });
      continue;
    }
    const sum = /^\s+sha512:\s*(.*)$/.exec(line);
    if (sum && info.files.length > 0)
      info.files.at(-1).sha512 = unquote(sum[1]);
  }
  return info;
}

/**
 * What is wrong with a latest.yml for an installer: the version, the file name
 * and the SHA-512 are all that electron-updater checks an unsigned build by.
 *
 * @param {ReturnType<typeof parseLatest>} info
 * @param {{ version: string, installerName: string, sha512: string }} want
 * @returns {string[]}
 */
export function checkLatest(info, { version, installerName, sha512 }) {
  const problems = [];
  if (info.version !== version)
    problems.push(`latest.yml: version is ${info.version}, want ${version}`);
  if (info.path !== installerName)
    problems.push(`latest.yml: path is ${info.path}, want ${installerName}`);
  if (info.sha512 !== sha512)
    problems.push("latest.yml: sha512 is not the installer's");
  if (info.files.length === 0) problems.push("latest.yml: files is empty");
  else {
    if (info.files[0].url !== installerName)
      problems.push(
        `latest.yml: files[0].url is ${info.files[0].url}, want ${installerName}`,
      );
    if (info.files[0].sha512 !== sha512)
      problems.push("latest.yml: files[0].sha512 is not the installer's");
    if (info.files.length > 1)
      problems.push(
        `latest.yml: files has ${info.files.length} entries, want the installer only`,
      );
  }
  return problems;
}

const semver =
  /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$/;

/**
 * Semantic version precedence: negative when a is older than b, 0 when they
 * are the same, positive when a is newer. A release is newer than its own
 * prereleases, and rc.2 is older than rc.10.
 *
 * @param {string} a
 * @param {string} b
 */
export function compareVersions(a, b) {
  const [x, y] = [a, b].map((v) => {
    const m = semver.exec(v);
    if (!m) throw new Error(`${JSON.stringify(v)} is not a semantic version`);
    return { numbers: m.slice(1, 4).map(Number), pre: m[4]?.split(".") };
  });
  for (let i = 0; i < 3; i++)
    if (x.numbers[i] !== y.numbers[i]) return x.numbers[i] - y.numbers[i];
  if (!x.pre || !y.pre) return (x.pre ? 0 : 1) - (y.pre ? 0 : 1);
  for (let i = 0; i < Math.max(x.pre.length, y.pre.length); i++) {
    const [p, q] = [x.pre[i], y.pre[i]];
    if (p === undefined) return -1;
    if (q === undefined) return 1;
    if (p === q) continue;
    const [pn, qn] = [/^\d+$/.test(p), /^\d+$/.test(q)];
    if (pn && qn) return Number(p) - Number(q);
    if (pn !== qn) return pn ? -1 : 1;
    return p < q ? -1 : 1;
  }
  return 0;
}

/**
 * Refuses a version that is not newer than the one the site serves, unless
 * forced. Returns the warning to show when forcing past it.
 *
 * @param {{ version: string, live: string | undefined, force: boolean }} options
 * @returns {string | undefined}
 */
export function checkOrder({ version, live, force }) {
  if (live === undefined) return undefined;
  const order = compareVersions(version, live);
  if (order > 0) return undefined;
  const what =
    order === 0
      ? `the site serves ${live} already: that is the same version`
      : `the site serves ${live}, which is newer: that would be a downgrade`;
  if (force) return `--force: publishing ${version} although ${what}`;
  throw new Error(
    `${what}. Everybody who opens the address would be sent to the older installer; use --force only if that is meant.`,
  );
}

/**
 * The plan, for a person to read before anything happens.
 *
 * @param {{ label: string, plan: ReturnType<typeof planUpload>, size: number, sha256: string }} options
 * @returns {string[]}
 */
export function describePlan({ label, plan, size, sha256 }) {
  const { installerName } = checkTag(label);
  const lines = [
    `${label}: ${installerName}, ${size} bytes (${(size / 1048576).toFixed(1)} MB)`,
    `the bucket ${BUCKET}, in this order (latest.yml last):`,
  ];
  plan.forEach((step, i) => {
    lines.push(
      `  ${i + 1}. ${BUCKET}/${step.key}`,
      `     ${step.contentType}; ${step.cacheControl}`,
    );
  });
  lines.push(
    `the .sha256 file would hold: ${sha256Line(sha256, installerName).trimEnd()}`,
    `before the first upload: HEAD ${SITE}${installerName} must be a 404, and the version must be newer than the latest.yml on the site`,
    `after the last: latest.yml, the installer's size, the checksum and the redirect of ${SITE} are read back`,
  );
  return lines;
}

const unique = (now) => String(now());

/** Fetches with a time limit and no cache of ours. */
async function ask(fetch, url, init = {}) {
  return fetch(url, { ...init, signal: AbortSignal.timeout(30_000) });
}

/**
 * Reads the site back after an upload: latest.yml is the file that was sent,
 * the installer has the size of the file, the checksum is the line that was
 * written, and the address itself redirects (302) to the installer. A fresh
 * query on the files keeps an edge that has seen the path before from
 * answering; the address is asked as it is, because the Worker is routed on /
 * alone. The Worker keeps latest.yml for a minute, so the whole round is
 * repeated until it passes or the attempts are gone.
 *
 * @param {{
 *   fetch: typeof fetch,
 *   installerName: string,
 *   size: number,
 *   latest: Buffer,
 *   checksum: string,
 *   retry?: { attempts: number, delayMs: number },
 *   sleep?: (ms: number) => Promise<unknown>,
 *   now?: () => number,
 * }} options
 * @returns {Promise<string[]>} what is wrong, nothing when the site is right
 */
export async function verifyPublished({
  fetch,
  installerName,
  size,
  latest,
  checksum,
  retry = { attempts: 8, delayMs: 15_000 },
  sleep = (ms) => new Promise((done) => setTimeout(done, ms)),
  now = Date.now,
}) {
  const round = async () => {
    const problems = [];
    const check = async (what, body) => {
      try {
        const found = await body();
        if (found) problems.push(`${what}: ${found}`);
      } catch (error) {
        problems.push(`${what}: ${error.message}`);
      }
    };
    const q = `?verify=${unique(now)}`;
    await check("GET latest.yml", async () => {
      const res = await ask(fetch, `${SITE}latest.yml${q}`);
      if (res.status !== 200) return `status ${res.status}`;
      if (!Buffer.from(await res.arrayBuffer()).equals(latest))
        return "the site serves a different latest.yml than the one uploaded";
    });
    await check(`HEAD ${installerName}`, async () => {
      const res = await ask(fetch, `${SITE}${installerName}${q}`, {
        method: "HEAD",
      });
      if (res.status !== 200) return `status ${res.status}`;
      const length = res.headers.get("content-length");
      if (length !== String(size))
        return `Content-Length is ${length}, the installer has ${size} bytes`;
    });
    await check(`GET ${installerName}.sha256`, async () => {
      const res = await ask(fetch, `${SITE}${installerName}.sha256${q}`);
      if (res.status !== 200) return `status ${res.status}`;
      if ((await res.text()) !== checksum)
        return "the site serves a different .sha256 than the one uploaded";
    });
    await check(`GET ${SITE}`, async () => {
      const res = await ask(fetch, SITE, { redirect: "manual" });
      if (res.status !== 302) return `status ${res.status}, want 302`;
      const location = res.headers.get("location");
      if (!location) return "the 302 has no Location";
      const target = new URL(location, SITE);
      if (target.host !== new URL(SITE).host)
        return `Location ${location} is not on padmate.download`;
      if (!target.pathname.endsWith(`/${installerName}`))
        return `Location ${location} does not end in ${installerName}`;
    });
    return problems;
  };
  let problems = await round();
  for (
    let attempt = 1;
    problems.length > 0 && attempt < retry.attempts;
    attempt++
  ) {
    await sleep(retry.delayMs);
    problems = await round();
  }
  return problems;
}

/**
 * Publishes a release.
 *
 * @param {{ label: string, dist?: string, dryRun?: boolean, force?: boolean }} options
 * @param {{
 *   fetch?: typeof fetch,
 *   put?: typeof wranglerPut,
 *   sleep?: (ms: number) => Promise<unknown>,
 *   retry?: { attempts: number, delayMs: number },
 *   now?: () => number,
 *   log?: (line: string) => void,
 *   repoRoot?: string,
 *   exists?: (file: string) => boolean,
 * }} [deps]
 * @returns {Promise<{ plan: ReturnType<typeof planUpload>, uploaded: string[] }>}
 */
export async function publish(
  { label, dist, dryRun = false, force = false },
  {
    fetch = globalThis.fetch,
    put = wranglerPut,
    sleep,
    retry,
    now = Date.now,
    log = (line) => console.log(line),
    repoRoot: root = repoRoot,
    exists = existsSync,
  } = {},
) {
  // The public code mirror has no release to publish.
  refusePackaging(root, exists);
  const { version, installerName } = checkTag(label);
  const folder = resolve(dist ?? join(root, "dist", "installer"));
  const plan = planUpload(label, folder);
  const [installer, latestFile] = [plan[0].file, plan[2].file];

  // What is local: the files must be the ones of this version.
  let facts;
  try {
    facts = await inspectFile(installer);
    if (facts.size === 0) throw new Error("it is empty");
  } catch (error) {
    throw new Error(
      `${installer} cannot be read (${error.message}): run task package:installer VERSION=${label} first`,
    );
  }
  let latest;
  try {
    latest = await readFile(latestFile);
  } catch (error) {
    throw new Error(
      `${latestFile} cannot be read (${error.message}): run task package:installer VERSION=${label} first, which writes it next to the installer`,
    );
  }
  const problems = checkLatest(parseLatest(latest.toString("utf8")), {
    version,
    installerName,
    sha512: facts.sha512,
  });
  if (problems.length > 0)
    throw new Error(
      `${latestFile} is not this release's:\n  ${problems.join("\n  ")}`,
    );
  const checksum = sha256Line(facts.sha256, installerName);

  const lines = describePlan({
    label,
    plan,
    size: facts.size,
    sha256: facts.sha256,
  });
  if (dryRun) {
    log(
      "publish-r2: dry run, nothing is asked of the site, written or uploaded",
    );
    for (const line of lines) log(`  ${line}`);
    return { plan, uploaded: [] };
  }

  // What is on the site: a published installer never changes, and the site
  // must not go back to an older version. A fresh query on each question keeps
  // an edge that cached a 404 a moment ago from answering.
  const q = `?check=${unique(now)}`;
  let head;
  try {
    head = await ask(fetch, `${SITE}${installerName}${q}`, { method: "HEAD" });
  } catch (error) {
    throw new Error(
      `cannot ask the site whether ${installerName} is there: ${error.message}`,
    );
  }
  if (head.status === 200)
    throw new Error(
      `${installerName} is already on the site, and a published installer never changes (it is cached for a year, and latest.yml carries its hash): build a new version. If an earlier run stopped halfway, upload the missing .sha256 and latest.yml by hand with wrangler.`,
    );
  if (head.status !== 404)
    throw new Error(
      `the site answered ${head.status} to HEAD ${installerName}, not 404: not publishing blind`,
    );
  let live;
  let liveRes;
  try {
    liveRes = await ask(fetch, `${SITE}latest.yml${q}`);
  } catch (error) {
    throw new Error(`cannot read the latest.yml on the site: ${error.message}`);
  }
  if (liveRes.status === 200) {
    live = parseLatest(await liveRes.text()).version;
    if (live === undefined || !semver.test(live)) {
      if (!force)
        throw new Error(
          "the latest.yml on the site has no readable version: not publishing blind (--force skips this check)",
        );
      log(
        "publish-r2: --force: the latest.yml on the site has no readable version and is not compared",
      );
      live = undefined;
    }
  } else if (liveRes.status !== 404) {
    throw new Error(
      `the site answered ${liveRes.status} to latest.yml, not 200 or 404: not publishing blind`,
    );
  }
  const warning = checkOrder({ version, live, force });
  if (warning) log(`publish-r2: ${warning}`);

  // The checksum file sits next to the installer, as release.yml leaves it.
  await writeFile(plan[1].file, checksum);
  const uploaded = [];
  for (const step of plan) {
    log(`publish-r2: uploading ${BUCKET}/${step.key}`);
    await put(step);
    uploaded.push(step.key);
  }

  log(`publish-r2: reading ${SITE} back`);
  const wrong = await verifyPublished({
    fetch,
    installerName,
    size: facts.size,
    latest,
    checksum,
    retry,
    sleep,
    now,
  });
  if (wrong.length > 0)
    throw new Error(
      `uploaded, but the site does not serve it as it should:\n  ${wrong.join("\n  ")}`,
    );
  log(
    `publish-r2: ${installerName} is published; ${SITE} now sends everybody to it`,
  );
  return { plan, uploaded };
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  await publish(options);
}

if (resolve(process.argv[1] ?? "") === fileURLToPath(import.meta.url)) {
  main().catch((error) => {
    console.error(`publish-r2: ${error.message}`);
    process.exit(1);
  });
}
