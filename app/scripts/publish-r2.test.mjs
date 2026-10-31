import { createHash } from "node:crypto";
import {
  existsSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve, win32 } from "node:path";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, it } from "vitest";
import { marker, refusal, repoRoot } from "./public-mirror.mjs";
import {
  BUCKET,
  checkLatest,
  checkOrder,
  checkTag,
  compareVersions,
  describePlan,
  IMMUTABLE,
  inspectFile,
  npxInvocation,
  parseArgs,
  parseLatest,
  planUpload,
  publish,
  SITE,
  sha256Line,
  verifyPublished,
  WRANGLER,
  wranglerArgs,
} from "./publish-r2.mjs";

const folders = [];
afterEach(() => {
  for (const dir of folders.splice(0))
    rmSync(dir, { recursive: true, force: true });
});
const folder = () => {
  const dir = mkdtempSync(join(tmpdir(), "padmate-r2-"));
  folders.push(dir);
  return dir;
};

const installerOf = (label) => `padmate-${label}-windows-amd64-setup.exe`;
const sha512Of = (bytes) => createHash("sha512").update(bytes).digest("base64");
const sha256Of = (bytes) => createHash("sha256").update(bytes).digest("hex");

/** latest.yml the way electron-builder 26.17.0 writes it for the nsis target. */
function latestYml(label, bytes, { version = label.slice(1) } = {}) {
  const name = installerOf(label);
  const sum = sha512Of(bytes);
  return `version: ${version}\nfiles:\n  - url: ${name}\n    sha512: ${sum}\npath: ${name}\nsha512: ${sum}\nreleaseDate: '2026-10-31T01:02:03.456Z'\n`;
}

/** A dist/installer folder with the files of one release. */
function distOf(label, bytes = `installer of ${label}`, latest) {
  const dir = folder();
  writeFileSync(join(dir, installerOf(label)), bytes);
  writeFileSync(join(dir, "latest.yml"), latest ?? latestYml(label, bytes));
  return dir;
}

/**
 * The download site in memory: a bucket, a fetch that answers the way the
 * site does (R2 for the files, the Worker for the address itself) and a put
 * that stores a file in the bucket. Everything that happens is in `events`.
 */
function site({ failPut = () => false, failFetch = () => false } = {}) {
  const bucket = new Map();
  const events = [];
  const seed = (label, bytes = `installer of ${label}`) => {
    bucket.set(installerOf(label), Buffer.from(bytes));
    bucket.set("latest.yml", Buffer.from(latestYml(label, bytes)));
  };
  const fetch = async (url, init = {}) => {
    const method = init.method ?? "GET";
    const u = new URL(url);
    events.push(`${method} ${u.pathname}`);
    const wrong = failFetch(method, u);
    if (wrong) return wrong;
    if (u.pathname === "/") {
      const latest = bucket.get("latest.yml");
      const path = latest && /^path: (\S+)$/m.exec(latest.toString())?.[1];
      return path
        ? new Response(null, {
            status: 302,
            headers: { location: SITE + path },
          })
        : new Response("not available", { status: 503 });
    }
    const object = bucket.get(u.pathname.slice(1));
    if (!object) return new Response("not found", { status: 404 });
    const headers = { "content-length": String(object.length) };
    return method === "HEAD"
      ? new Response(null, { status: 200, headers })
      : new Response(object, { status: 200, headers });
  };
  const put = async (step) => {
    events.push(`put ${step.key}`);
    if (failPut(step)) throw new Error(`wrangler failed on ${step.key}`);
    bucket.set(step.key, readFileSync(step.file));
  };
  return { bucket, events, fetch, put, seed };
}

const noWait = { attempts: 1, delayMs: 0 };

describe("checkTag", () => {
  it("takes a stable release tag and names its installer", () => {
    expect(checkTag("v1.0.3")).toEqual({
      label: "v1.0.3",
      version: "1.0.3",
      installerName: "padmate-v1.0.3-windows-amd64-setup.exe",
    });
    expect(checkTag("v0.0.1").version).toBe("0.0.1");
  });

  it("refuses a prerelease: it never goes to the download site", () => {
    for (const label of ["v1.0.3-rc.1", "v1.0.3-rc.5", "v1.0.0-m30a.2"])
      expect(() => checkTag(label)).toThrow(/prerelease/);
  });

  it("refuses what is not a release tag", () => {
    for (const label of [
      "",
      "dev",
      "1.0.3",
      "v1.0",
      "v1.0.3+build",
      "v01.0.3",
      "v1.0.3-",
      "de58d1d-dirty",
      "v1.0.3\n",
      undefined,
    ])
      expect(() => checkTag(label)).toThrow(/not a release tag/);
  });
});

describe("the files of a release", () => {
  it("writes the .sha256 as <hex>, two spaces, the file name and a line feed", () => {
    expect(
      sha256Line("ab".repeat(32), "padmate-v1.0.3-windows-amd64-setup.exe"),
    ).toBe(`${"ab".repeat(32)}  padmate-v1.0.3-windows-amd64-setup.exe\n`);
  });

  it("reads the size and both hashes of a file in one pass", async () => {
    const dir = folder();
    const file = join(dir, "x.bin");
    writeFileSync(file, "hello");
    expect(await inspectFile(file)).toEqual({
      size: 5,
      sha256:
        "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
      sha512: sha512Of("hello"),
    });
  });

  it("plans three objects, latest.yml last", () => {
    const plan = planUpload("v1.0.3", "D:\\repo\\dist\\installer");
    const name = "padmate-v1.0.3-windows-amd64-setup.exe";
    expect(plan.map((s) => s.key)).toEqual([
      name,
      `${name}.sha256`,
      "latest.yml",
    ]);
    expect(plan.map((s) => s.role)).toEqual([
      "installer",
      "checksum",
      "latest",
    ]);
    expect(plan[0]).toMatchObject({
      contentType: "application/vnd.microsoft.portable-executable",
      cacheControl: "public, max-age=31536000, immutable",
    });
    expect(plan[1]).toMatchObject({
      contentType: "text/plain; charset=utf-8",
      cacheControl: "public, max-age=31536000, immutable",
    });
    expect(plan[2]).toMatchObject({
      contentType: "text/yaml; charset=utf-8",
      cacheControl: "no-cache",
    });
    expect(IMMUTABLE).toBe("public, max-age=31536000, immutable");
    for (const step of plan) expect(step.file.endsWith(step.key)).toBe(true);
  });
});

describe("wrangler", () => {
  it("is pinned to one exact version", () => {
    expect(WRANGLER).toBe("wrangler@4.151.1");
    expect(WRANGLER).toMatch(/^wrangler@\d+\.\d+\.\d+$/);
    expect(BUCKET).toBe("padmate");
  });

  it("puts an object into the bucket with its type and cache control", () => {
    expect(
      wranglerArgs({
        key: "latest.yml",
        file: "C:\\x\\latest.yml",
        contentType: "text/yaml; charset=utf-8",
        cacheControl: "no-cache",
      }),
    ).toEqual([
      "--yes",
      "wrangler@4.151.1",
      "r2",
      "object",
      "put",
      "padmate/latest.yml",
      "--file",
      "C:\\x\\latest.yml",
      "--remote",
      "--content-type",
      "text/yaml; charset=utf-8",
      "--cache-control",
      "no-cache",
    ]);
  });

  it("runs npx through node on Windows, where a .cmd cannot be spawned without a shell", () => {
    const exists = () => true;
    expect(
      npxInvocation({
        execPath: "C:\\Program Files\\nodejs\\node.exe",
        platform: "win32",
        exists,
      }),
    ).toEqual({
      command: "C:\\Program Files\\nodejs\\node.exe",
      prefix: [
        win32.join(
          "C:\\Program Files\\nodejs",
          "node_modules",
          "npm",
          "bin",
          "npx-cli.js",
        ),
      ],
    });
    expect(
      npxInvocation({ execPath: "/usr/bin/node", platform: "linux", exists }),
    ).toEqual({
      command: "npx",
      prefix: [],
    });
    expect(() =>
      npxInvocation({
        execPath: "C:\\node.exe",
        platform: "win32",
        exists: () => false,
      }),
    ).toThrow(/npx-cli\.js/);
  });
});

describe("latest.yml", () => {
  const bytes = "installer bytes";
  const text = latestYml("v1.0.3", bytes);
  const want = {
    version: "1.0.3",
    installerName: installerOf("v1.0.3"),
    sha512: sha512Of(bytes),
  };

  it("is read the way electron-builder writes it", () => {
    expect(parseLatest(text)).toEqual({
      version: "1.0.3",
      path: installerOf("v1.0.3"),
      sha512: sha512Of(bytes),
      files: [{ url: installerOf("v1.0.3"), sha512: sha512Of(bytes) }],
    });
    expect(parseLatest(text.replaceAll("\n", "\r\n")).version).toBe("1.0.3");
    expect(parseLatest(`version: '1.0.3'\n`).version).toBe("1.0.3");
    expect(parseLatest("").files).toEqual([]);
  });

  it("passes when it names this version, this installer and its hash", () => {
    expect(checkLatest(parseLatest(text), want)).toEqual([]);
  });

  it("names what is wrong", () => {
    const problems = (t) => checkLatest(parseLatest(t), want).join("\n");
    expect(
      problems(latestYml("v1.0.3", bytes, { version: "1.0.4" })),
    ).toContain("version");
    expect(
      problems(text.replaceAll(installerOf("v1.0.3"), "padmate-setup.exe")),
    ).toContain("padmate-setup.exe");
    expect(problems(latestYml("v1.0.3", "other bytes"))).toContain("sha512");
    expect(problems("version: 1.0.3\n")).toContain("files");
    const two = text.replace(
      "path:",
      "  - url: second.exe\n    sha512: x\npath:",
    );
    expect(problems(two)).toContain("2 entries");
  });
});

describe("versions", () => {
  it("orders them as semantic versioning does", () => {
    expect(compareVersions("1.0.3", "1.0.2")).toBeGreaterThan(0);
    expect(compareVersions("1.0.2", "1.0.3")).toBeLessThan(0);
    expect(compareVersions("1.0.3", "1.0.3")).toBe(0);
    expect(compareVersions("1.10.0", "1.9.9")).toBeGreaterThan(0);
    expect(compareVersions("2.0.0", "1.99.99")).toBeGreaterThan(0);
    expect(compareVersions("1.0.3", "1.0.3-rc.5")).toBeGreaterThan(0);
    expect(compareVersions("1.0.3-rc.2", "1.0.3-rc.10")).toBeLessThan(0);
    expect(compareVersions("1.0.3-rc.1", "1.0.3-rc")).toBeGreaterThan(0);
    expect(() => compareVersions("1.0", "1.0.0")).toThrow(/version/);
  });

  it("accepts only a newer version than the one on the site, unless forced", () => {
    expect(
      checkOrder({ version: "1.0.3", live: undefined, force: false }),
    ).toBeUndefined();
    expect(
      checkOrder({ version: "1.0.3", live: "1.0.2", force: false }),
    ).toBeUndefined();
    expect(() =>
      checkOrder({ version: "1.0.3", live: "1.0.3", force: false }),
    ).toThrow(/same version/);
    expect(() =>
      checkOrder({ version: "1.0.2", live: "1.0.3", force: false }),
    ).toThrow(/downgrade/);
    expect(
      checkOrder({ version: "1.0.2", live: "1.0.3", force: true }),
    ).toMatch(/force/);
    expect(
      checkOrder({ version: "1.0.3", live: "1.0.3", force: true }),
    ).toMatch(/force/);
  });
});

describe("parseArgs", () => {
  it("reads the version and the flags", () => {
    expect(parseArgs(["--version", "v1.0.3"])).toEqual({
      label: "v1.0.3",
      dist: undefined,
      dryRun: false,
      force: false,
    });
    expect(
      parseArgs(["--version", "v1.0.3", "--dry-run", "--force", "--dist", "X"]),
    ).toEqual({
      label: "v1.0.3",
      dist: "X",
      dryRun: true,
      force: true,
    });
  });

  it("refuses the rest", () => {
    for (const args of [
      [],
      ["--dry-run"],
      ["--version"],
      ["--version", "v1.0.3", "--upload"],
      ["--version", "v1.0.3", "extra"],
      ["--version", "v1.0.3", "--dist"],
      ["--version", "v1.0.3", "--version", "v1.0.4"],
    ])
      expect(() => parseArgs(args)).toThrow(/usage/);
  });
});

describe("verifyPublished", () => {
  const bytes = "installer bytes";
  const label = "v1.0.3";
  const latest = Buffer.from(latestYml(label, bytes));
  const checksum = sha256Line(sha256Of(bytes), installerOf(label));
  const good = () => {
    const s = site();
    s.seed(label, bytes);
    s.bucket.set(`${installerOf(label)}.sha256`, Buffer.from(checksum));
    return s;
  };
  const facts = {
    installerName: installerOf(label),
    size: Buffer.byteLength(bytes),
    latest,
    checksum,
  };
  const run = (fetch, retry = noWait) =>
    verifyPublished({
      fetch,
      ...facts,
      retry,
      sleep: async () => {},
      now: () => 1,
    });

  it("passes when the site serves what was uploaded", async () => {
    expect(await run(good().fetch)).toEqual([]);
  });

  it("asks for a fresh copy: a query that no cache holds", async () => {
    const s = good();
    const urls = [];
    await run((url, init) => {
      urls.push(url);
      return s.fetch(url, init);
    });
    expect(urls.filter((u) => u.includes("latest.yml"))[0]).toMatch(
      /latest\.yml\?verify=/,
    );
    expect(urls.filter((u) => u.includes("setup.exe?"))[0]).toMatch(
      /setup\.exe\?verify=/,
    );
    // The address itself is asked as it is: the Worker is routed on / only.
    expect(urls).toContain(SITE);
  });

  it("does not follow the redirect of the address", async () => {
    const seen = [];
    const s = good();
    await run((url, init) => {
      seen.push([url, init?.redirect, init?.method]);
      return s.fetch(url, init);
    });
    expect(seen.find(([u]) => u === SITE)?.[1]).toBe("manual");
    expect(seen.find(([u]) => u.includes("setup.exe?"))?.[2]).toBe("HEAD");
  });

  it("names a latest.yml that is not the uploaded one", async () => {
    const s = good();
    s.bucket.set(
      "latest.yml",
      Buffer.from(latestYml(label, bytes, { version: "1.0.2" })),
    );
    expect((await run(s.fetch)).join("\n")).toContain("latest.yml");
  });

  it("names an installer of another size", async () => {
    const s = good();
    s.bucket.set(installerOf(label), Buffer.from("short"));
    expect((await run(s.fetch)).join("\n")).toMatch(/Content-Length/);
  });

  it("names a checksum that is not the uploaded one", async () => {
    const s = good();
    s.bucket.set(`${installerOf(label)}.sha256`, Buffer.from("00  x\n"));
    expect((await run(s.fetch)).join("\n")).toContain(".sha256");
  });

  it("wants a 302 to the installer from the address", async () => {
    const wrong = (response) => (method, u) =>
      method === "GET" && u.pathname === "/" ? response() : undefined;
    for (const [what, response, text] of [
      [
        "a 301",
        () =>
          new Response(null, {
            status: 301,
            headers: { location: SITE + installerOf(label) },
          }),
        "302",
      ],
      ["a 200", () => new Response("<html>", { status: 200 }), "302"],
      ["a 503", () => new Response("down", { status: 503 }), "302"],
      [
        "another installer",
        () =>
          new Response(null, {
            status: 302,
            headers: {
              location: `${SITE}padmate-v1.0.2-windows-amd64-setup.exe`,
            },
          }),
        installerOf(label),
      ],
      [
        "another host",
        () =>
          new Response(null, {
            status: 302,
            headers: { location: `https://example.com/${installerOf(label)}` },
          }),
        "padmate.download",
      ],
      ["no Location", () => new Response(null, { status: 302 }), "Location"],
    ]) {
      const s = site({ failFetch: wrong(response) });
      s.seed(label, bytes);
      s.bucket.set(`${installerOf(label)}.sha256`, Buffer.from(checksum));
      const problems = (await run(s.fetch)).join("\n");
      expect(problems, what).toContain(text);
    }
  });

  it("reports a request that fails", async () => {
    const s = good();
    const problems = await run((url, init) => {
      if (init?.method === "HEAD") throw new Error("ECONNRESET");
      return s.fetch(url, init);
    });
    expect(problems.join("\n")).toContain("ECONNRESET");
  });

  it("waits for the address to follow: the Worker caches latest.yml for a minute", async () => {
    const s = good();
    let asks = 0;
    const waits = [];
    const problems = await verifyPublished({
      fetch: (url, init) => {
        if (url === SITE && ++asks < 3)
          return new Response(null, {
            status: 302,
            headers: {
              location: `${SITE}padmate-v1.0.2-windows-amd64-setup.exe`,
            },
          });
        return s.fetch(url, init);
      },
      ...facts,
      retry: { attempts: 5, delayMs: 15000 },
      sleep: async (ms) => waits.push(ms),
      now: () => 1,
    });
    expect(problems).toEqual([]);
    expect(asks).toBe(3);
    expect(waits).toEqual([15000, 15000]);
  });

  it("gives up after the attempts and reports the last problems", async () => {
    const s = good();
    s.bucket.delete("latest.yml");
    let waits = 0;
    const problems = await verifyPublished({
      fetch: s.fetch,
      ...facts,
      retry: { attempts: 3, delayMs: 1 },
      sleep: async () => {
        waits++;
      },
      now: () => 1,
    });
    expect(problems.length).toBeGreaterThan(0);
    expect(waits).toBe(2);
  });
});

describe("publish", () => {
  const label = "v1.0.3";
  const quiet = () => {
    const lines = [];
    return { lines, log: (line) => lines.push(line) };
  };
  const deps = (s, extra = {}) => ({
    fetch: s.fetch,
    put: s.put,
    sleep: async () => {},
    retry: noWait,
    now: () => 1,
    repoRoot: folder(),
    ...extra,
  });
  const names = (s) =>
    s.events.filter((e) => e.startsWith("put ")).map((e) => e.slice(4));

  it("uploads the installer, its checksum and then latest.yml, and checks the site", async () => {
    const bytes = "installer of v1.0.3";
    const dist = distOf(label, bytes);
    const s = site();
    s.seed("v1.0.2");
    const out = quiet();
    const result = await publish({ label, dist }, deps(s, { log: out.log }));

    const name = installerOf(label);
    expect(names(s)).toEqual([name, `${name}.sha256`, "latest.yml"]);
    expect(result.uploaded).toEqual([name, `${name}.sha256`, "latest.yml"]);
    // The checksum file is written next to the installer, as release.yml does.
    expect(readFileSync(join(dist, `${name}.sha256`), "utf8")).toBe(
      `${sha256Of(bytes)}  ${name}\n`,
    );
    expect(s.bucket.get(`${name}.sha256`).toString()).toBe(
      `${sha256Of(bytes)}  ${name}\n`,
    );
    expect(s.bucket.get("latest.yml").toString()).toBe(
      readFileSync(join(dist, "latest.yml"), "utf8"),
    );
    // Nothing is uploaded before both questions to the site are answered, and
    // the address is asked last, after latest.yml.
    const firstPut = s.events.findIndex((e) => e.startsWith("put "));
    expect(s.events.slice(0, firstPut)).toEqual([
      `HEAD /${name}`,
      "GET /latest.yml",
    ]);
    expect(s.events.at(-1)).toBe("GET /");
    expect(s.events.lastIndexOf("put latest.yml")).toBeGreaterThan(
      s.events.lastIndexOf(`put ${name}.sha256`),
    );
    expect(out.lines.join("\n")).toContain(name);
  });

  it("is the first release on an empty site", async () => {
    const dist = distOf(label);
    const s = site();
    await publish({ label, dist }, deps(s));
    expect(names(s)).toHaveLength(3);
  });

  it("does nothing on a dry run: no request, no upload, no file", async () => {
    const dist = distOf(label);
    const s = site();
    const out = quiet();
    const result = await publish(
      { label, dist, dryRun: true },
      deps(s, { log: out.log }),
    );
    expect(s.events).toEqual([]);
    expect(result.uploaded).toEqual([]);
    expect(existsSync(join(dist, `${installerOf(label)}.sha256`))).toBe(false);
    const text = out.lines.join("\n");
    for (const want of [
      installerOf(label),
      "latest.yml",
      "no-cache",
      IMMUTABLE,
      "dry run",
    ])
      expect(text).toContain(want);
    // The plan shows the checksum line that would be written.
    expect(text).toContain(
      `${sha256Of(`installer of ${label}`)}  ${installerOf(label)}`,
    );
  });

  it("describes the plan for a person", () => {
    const text = describePlan({
      label,
      plan: planUpload(label, "D:\\d"),
      size: 5,
      sha256: "ab".repeat(32),
    }).join("\n");
    expect(text).toContain("padmate/latest.yml");
    expect(text).toContain("application/vnd.microsoft.portable-executable");
  });

  it("refuses a prerelease before it looks at anything", async () => {
    const s = site();
    await expect(
      publish({ label: "v1.0.3-rc.1", dist: folder() }, deps(s)),
    ).rejects.toThrow(/prerelease/);
    expect(s.events).toEqual([]);
  });

  it("refuses in the public code mirror", async () => {
    const root = folder();
    writeFileSync(join(root, marker), "x\n");
    const s = site();
    await expect(
      publish({ label, dist: distOf(label) }, deps(s, { repoRoot: root })),
    ).rejects.toThrow(refusal);
    expect(s.events).toEqual([]);
  });

  it("refuses when the installer or latest.yml is not there", async () => {
    const s = site();
    await expect(publish({ label, dist: folder() }, deps(s))).rejects.toThrow(
      /task package:installer/,
    );
    const dist = distOf(label);
    rmSync(join(dist, "latest.yml"));
    await expect(publish({ label, dist }, deps(s))).rejects.toThrow(
      /latest\.yml/,
    );
    expect(s.events).toEqual([]);
  });

  it("refuses a latest.yml that is not this installer's", async () => {
    const s = site();
    const other = distOf(
      label,
      "installer of v1.0.3",
      latestYml(label, "some other bytes"),
    );
    await expect(publish({ label, dist: other }, deps(s))).rejects.toThrow(
      /sha512/,
    );
    const version = distOf(
      label,
      "installer of v1.0.3",
      latestYml(label, "installer of v1.0.3", { version: "1.0.4" }),
    );
    await expect(publish({ label, dist: version }, deps(s))).rejects.toThrow(
      /version/,
    );
    expect(s.events).toEqual([]);
  });

  it("never overwrites a published installer", async () => {
    const dist = distOf(label);
    const s = site();
    s.bucket.set(installerOf(label), Buffer.from("published"));
    await expect(publish({ label, dist }, deps(s))).rejects.toThrow(/already/);
    expect(names(s)).toEqual([]);
    expect(existsSync(join(dist, `${installerOf(label)}.sha256`))).toBe(false);
  });

  it("does not guess when the site cannot be asked", async () => {
    const dist = distOf(label);
    for (const [what, failFetch] of [
      [
        "a 500 for the installer",
        (m, _u) =>
          m === "HEAD" ? new Response(null, { status: 500 }) : undefined,
      ],
      [
        "a 403 for the installer",
        (m, _u) =>
          m === "HEAD" ? new Response(null, { status: 403 }) : undefined,
      ],
      [
        "a 500 for latest.yml",
        (_m, u) =>
          u.pathname === "/latest.yml"
            ? new Response("x", { status: 500 })
            : undefined,
      ],
      [
        "a request that fails",
        () => {
          throw new Error("ECONNRESET");
        },
      ],
    ]) {
      const s = site({ failFetch });
      await expect(publish({ label, dist }, deps(s)), what).rejects.toThrow();
      expect(names(s), what).toEqual([]);
    }
  });

  it("refuses a downgrade and the same version", async () => {
    const dist = distOf(label);
    for (const live of ["v1.0.4", "v1.0.3"]) {
      const s = site();
      s.seed(live);
      // The same version has its installer on the site too, so only latest.yml
      // is the question: move the installer out of the way.
      s.bucket.delete(installerOf(live));
      await expect(publish({ label, dist }, deps(s)), live).rejects.toThrow(
        live === "v1.0.4" ? /downgrade/ : /same version/,
      );
      expect(names(s), live).toEqual([]);
    }
  });

  it("lets --force publish over a newer latest.yml", async () => {
    const dist = distOf(label);
    const s = site();
    s.seed("v1.0.4");
    const out = quiet();
    await publish({ label, dist, force: true }, deps(s, { log: out.log }));
    expect(names(s)).toHaveLength(3);
    expect(out.lines.join("\n")).toMatch(/force/);
    // --force is about the order only: an installer that is there stays.
    const again = site();
    again.bucket.set(installerOf(label), Buffer.from("published"));
    await expect(
      publish({ label, dist, force: true }, deps(again)),
    ).rejects.toThrow(/already/);
  });

  it("does not guess at a latest.yml it cannot read", async () => {
    const dist = distOf(label);
    const s = site();
    s.bucket.set("latest.yml", Buffer.from("<html>a captive portal</html>"));
    await expect(publish({ label, dist }, deps(s))).rejects.toThrow(
      /latest\.yml/,
    );
    expect(names(s)).toEqual([]);
  });

  it("stops at the first upload that fails and never reaches latest.yml", async () => {
    const name = installerOf(label);
    for (const failing of [name, `${name}.sha256`]) {
      const s = site({ failPut: (step) => step.key === failing });
      await expect(
        publish({ label, dist: distOf(label) }, deps(s)),
      ).rejects.toThrow(/wrangler failed/);
      expect(names(s)).not.toContain("latest.yml");
      expect(s.bucket.has("latest.yml")).toBe(false);
    }
  });

  it("fails when the site does not serve what was uploaded", async () => {
    const s = site({
      failFetch: (m, u) =>
        m === "GET" && u.pathname === "/"
          ? new Response(null, {
              status: 302,
              headers: {
                location: `${SITE}padmate-v1.0.2-windows-amd64-setup.exe`,
              },
            })
          : undefined,
    });
    await expect(
      publish({ label, dist: distOf(label) }, deps(s)),
    ).rejects.toThrow(/302|padmate-v1\.0\.3/);
  });
});

describe("the task", () => {
  const taskfile = readFileSync(resolve(repoRoot, "Taskfile.yml"), "utf8");
  const block =
    /^ {2}release:r2:\n(?: {4}.*\n|\s*\n)+/m.exec(taskfile)?.[0] ?? "";

  it("runs the script for the VERSION, on Windows, and not in the public mirror", () => {
    expect(block).not.toBe("");
    expect(block).toContain("dir: app");
    expect(block).toContain("platforms: [windows]");
    expect(block).toContain(`msg: "${refusal}"`);
    expect(block).toContain('test ! -e "{{.ROOT_DIR}}/.padmate-public"');
    expect(block).toContain(
      'node scripts/publish-r2.mjs --version "{{.VERSION}}" {{.CLI_ARGS}}',
    );
  });

  it("is not a step of anything that builds", () => {
    // Nothing uploads as a side effect of a build.
    const others = taskfile.replace(block, "");
    expect(others).not.toContain("publish-r2");
    expect(others).not.toContain("release:r2");
  });

  it("is in the folder the other scripts are", () => {
    const here = dirname(fileURLToPath(import.meta.url));
    expect(existsSync(join(here, "publish-r2.mjs"))).toBe(true);
  });
});
