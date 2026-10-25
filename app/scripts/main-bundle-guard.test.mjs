import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { assertMainBundle, bareRequires } from "./main-bundle-guard.mjs";

describe("the requires of the main bundle (PKG-10)", () => {
  it("allows Node's own modules and electron", () => {
    const code = [
      'const fs = require("fs");',
      'const p = require("node:path");',
      'const fsp = require("fs/promises");',
      'const e = require("electron");',
      "const c = require('child_process');",
      'require("node:fs/promises");',
    ].join("\n");
    expect(bareRequires(code)).toEqual([]);
  });

  it("names a package that the installed application cannot find", () => {
    const code = [
      'const a = require("electron-updater");',
      'const b = require("fs-extra");',
      'const again = require("fs-extra");',
      'const ok = require("os");',
    ].join("\n");
    expect(bareRequires(code)).toEqual(["electron-updater", "fs-extra"]);
  });

  it("names a relative require, which the single file has no neighbour for", () => {
    expect(bareRequires('require("./chunk.js")')).toEqual(["./chunk.js"]);
  });

  it("names a require whose argument is not a literal", () => {
    expect(bareRequires("const m = require(name);")).toEqual(["<dynamic>"]);
    expect(bareRequires('require("fs" + x)')).toEqual(["<dynamic>"]);
  });

  it("leaves alone words that only contain require", () => {
    expect(
      bareRequires('const x = prerequire("a"); const y = obj.required("b");'),
    ).toEqual([]);
  });
});

describe("assertMainBundle", () => {
  let dir;
  beforeEach(async () => {
    dir = await mkdtemp(join(tmpdir(), "padmate-bundle-"));
  });
  afterEach(async () => {
    await rm(dir, { recursive: true, force: true });
  });

  it("passes a bundle that needs nothing installed", async () => {
    const file = join(dir, "index.js");
    await writeFile(file, 'const e = require("electron");\nrequire("path");\n');
    await expect(assertMainBundle(file)).resolves.toBeUndefined();
  });

  it("fails a bundle that requires a package, naming it", async () => {
    const file = join(dir, "index.js");
    await writeFile(file, 'require("electron-updater");\n');
    await expect(assertMainBundle(file)).rejects.toThrow(
      /electron-updater.*externalizeDepsPlugin/s,
    );
  });

  it("fails when the bundle is not there", async () => {
    await mkdir(join(dir, "out"));
    await expect(
      assertMainBundle(join(dir, "out", "index.js")),
    ).rejects.toThrow();
  });
});
