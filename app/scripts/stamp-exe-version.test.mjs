import { readFileSync } from "node:fs";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { NtExecutable, NtExecutableResource, Resource } from "resedit";
import { afterEach, describe, expect, it, vi } from "vitest";
import stampExeVersion, {
  numericVersion,
  stampFileVersion,
} from "./stamp-exe-version.mjs";

const lang = { lang: 0x0409, codepage: 1200 };
const here = dirname(fileURLToPath(import.meta.url));

describe("numericVersion", () => {
  it.each([
    ["0.15.1", [0, 15, 1, 0]],
    ["0.15.1-rc.1", [0, 15, 1, 0]],
    ["0.15.1-rc.2", [0, 15, 1, 0]],
    ["0.15.0-rc.1", [0, 15, 0, 0]],
    ["0.14.0", [0, 14, 0, 0]],
    ["1.2.3-alpha", [1, 2, 3, 0]],
    // What `git describe` gives after a tag.
    ["0.13.1-5-gde58d1d", [0, 13, 1, 0]],
    ["65535.65535.65535", [65535, 65535, 65535, 0]],
  ])("%s is %j", (version, want) => {
    expect(numericVersion(version)).toEqual(want);
  });

  it.each([
    "v0.15.1",
    "0.15",
    "0.15.1.4",
    "0.15.1+build",
    "0.15.1-",
    "01.2.3",
    "65536.0.0",
    "0.0.65536",
    "dev",
    "",
    undefined,
  ])("rejects %j", (version) => {
    expect(() => numericVersion(version)).toThrow();
  });
});

describe("stampFileVersion", () => {
  const folders = [];
  afterEach(async () => {
    await Promise.all(
      folders.splice(0).map((f) => rm(f, { recursive: true, force: true })),
    );
  });

  // A tiny executable with a version resource written the way electron-builder
  // writes padmate.exe's: the app version as the file version string and
  // through setFileVersion, the four-number form as the product version.
  async function exeWith(version) {
    const folder = await mkdtemp(join(tmpdir(), "padmate-stamp-"));
    folders.push(folder);
    const executable = NtExecutable.createEmpty(false, false);
    const resources = NtExecutableResource.from(executable);
    const info = Resource.VersionInfo.createEmpty();
    info.setStringValues(lang, {
      FileDescription: "padmate",
      ProductName: "padmate",
    });
    info.setFileVersion(version);
    info.setProductVersion(`${version.split("-")[0]}.0`);
    info.setStringValues(lang, { FileVersion: version });
    info.outputToResourceEntries(resources.entries);
    resources.outputResource(executable);
    const file = join(folder, "padmate.exe");
    await writeFile(file, Buffer.from(executable.generate()));
    return file;
  }

  async function read(file) {
    const resources = NtExecutableResource.from(
      NtExecutable.from(await readFile(file)),
    );
    const [info] = Resource.VersionInfo.fromEntries(resources.entries);
    const fixed = info.fixedInfo;
    const unpack = (ms, ls) => [ms >>> 16, ms & 0xffff, ls >>> 16, ls & 0xffff];
    return {
      file: unpack(fixed.fileVersionMS, fixed.fileVersionLS),
      product: unpack(fixed.productVersionMS, fixed.productVersionLS),
      strings: info.getStringValues(lang),
    };
  }

  it.each([
    ["0.15.1-rc.1", [0, 15, 1, 0]],
    ["0.15.1-rc.2", [0, 15, 1, 0]],
    ["0.15.1", [0, 15, 1, 0]],
    ["0.15.0-rc.1", [0, 15, 0, 0]],
    ["1.20.300-beta.4", [1, 20, 300, 0]],
  ])(
    "%s gets the file version %j and keeps every string",
    async (version, want) => {
      const file = await exeWith(version);
      await stampFileVersion(file, version);
      const got = await read(file);
      expect(got.file).toEqual(want);
      // The product version was already right and is not touched.
      expect(got.product).toEqual(want);
      expect(got.strings.FileVersion).toBe(version);
      expect(got.strings.ProductName).toBe("padmate");
      expect(got.strings.FileDescription).toBe("padmate");
    },
  );

  it("does not turn the string version into numbers", async () => {
    const file = await exeWith("0.15.1-rc.1");
    await stampFileVersion(file, "0.15.1-rc.1");
    expect((await read(file)).strings.FileVersion).toBe("0.15.1-rc.1");
  });

  it("refuses an executable without a version resource", async () => {
    const folder = await mkdtemp(join(tmpdir(), "padmate-stamp-"));
    folders.push(folder);
    const file = join(folder, "padmate.exe");
    await writeFile(file, Buffer.from(NtExecutable.createEmpty().generate()));
    await expect(stampFileVersion(file, "0.15.1")).rejects.toThrow(
      /no version resource/,
    );
  });

  it("refuses a version that is not a release version before it reads the file", async () => {
    await expect(
      stampFileVersion(join(tmpdir(), "padmate-missing.exe"), "dev"),
    ).rejects.toThrow(/not a release version/);
  });

  it("is the afterSign hook: it stamps <productFilename>.exe of appOutDir", async () => {
    const file = await exeWith("0.15.1-rc.1");
    const appOutDir = join(file, "..");
    await stampExeVersion({
      electronPlatformName: "win32",
      appOutDir,
      packager: {
        appInfo: { productFilename: "padmate", version: "0.15.1-rc.1" },
        signIf: async () => false,
      },
    });
    expect((await read(file)).file).toEqual([0, 15, 1, 0]);
  });

  it("leaves other platforms alone", async () => {
    const file = await exeWith("0.15.1-rc.1");
    const before = await readFile(file);
    await stampExeVersion({
      electronPlatformName: "linux",
      appOutDir: join(file, ".."),
      packager: {
        appInfo: { productFilename: "padmate", version: "0.15.1-rc.1" },
      },
    });
    expect(await readFile(file)).toEqual(before);
  });

  // Data directory 4 of a PE file is its certificate table, addressed by file
  // offset. A fake one: bytes after the image that no tool would take for a
  // signature, but that make the file "signed" for pe-library.
  async function directory4(buffer) {
    const optional = buffer.readUInt32LE(0x3c) + 24;
    const directories =
      optional + (buffer.readUInt16LE(optional) === 0x20b ? 112 : 96);
    return directories + 4 * 8;
  }

  async function addFakeCertificate(file) {
    const buffer = await readFile(file);
    const at = await directory4(buffer);
    const certificate = Buffer.alloc(64, 0xab);
    certificate.writeUInt32LE(certificate.length, 0);
    certificate.writeUInt16LE(0x0200, 4);
    certificate.writeUInt16LE(0x0002, 6);
    buffer.writeUInt32LE(buffer.length, at);
    buffer.writeUInt32LE(certificate.length, at + 4);
    await writeFile(file, Buffer.concat([buffer, certificate]));
    return certificate.length;
  }

  async function securitySize(file) {
    const buffer = await readFile(file);
    return buffer.readUInt32LE((await directory4(buffer)) + 4);
  }

  it("stamps a file that is already signed, and the signature is gone", async () => {
    const file = await exeWith("0.15.1-rc.1");
    expect(await addFakeCertificate(file)).toBe(await securitySize(file));
    // pe-library refuses it without ignoreCert, which is why the hook passes it.
    expect(() => NtExecutable.from(readFileSync(file))).toThrow();
    await stampFileVersion(file, "0.15.1-rc.1");
    expect((await read(file)).file).toEqual([0, 15, 1, 0]);
    expect(await securitySize(file)).toBe(0);
  });

  // electron-builder's WinPackager: signIf(file) signs, or does nothing without
  // a certificate and says false.
  function context(file, packager) {
    return {
      electronPlatformName: "win32",
      appOutDir: join(file, ".."),
      packager: {
        appInfo: { productFilename: "padmate", version: "0.15.1-rc.1" },
        ...packager,
      },
    };
  }

  it("signs again once, after the version is stamped", async () => {
    const file = await exeWith("0.15.1-rc.1");
    await addFakeCertificate(file);
    const seen = [];
    await stampExeVersion(
      context(file, {
        signIf: async (path) => {
          // What signing would sign: the stamped file, without the old signature.
          seen.push({
            path,
            version: (await read(path)).file,
            security: await securitySize(path),
          });
          return true;
        },
      }),
    );
    expect(seen).toEqual([{ path: file, version: [0, 15, 1, 0], security: 0 }]);
  });

  it("does nothing more for an unsigned build, where signIf says false", async () => {
    const file = await exeWith("0.15.1-rc.1");
    const signIf = vi.fn(async () => false);
    await stampExeVersion(context(file, { signIf, forceCodeSigning: false }));
    expect(signIf).toHaveBeenCalledTimes(1);
    expect((await read(file)).file).toEqual([0, 15, 1, 0]);
  });

  it("fails when the build must be signed and signIf did not sign", async () => {
    const file = await exeWith("0.15.1-rc.1");
    await expect(
      stampExeVersion(
        context(file, { signIf: async () => false, forceCodeSigning: true }),
      ),
    ).rejects.toThrow(/code signing is required/);
  });

  it("fails when signing fails", async () => {
    const file = await exeWith("0.15.1-rc.1");
    await expect(
      stampExeVersion(
        context(file, {
          signIf: async () => {
            throw new Error("signtool exited with 1");
          },
          forceCodeSigning: true,
        }),
      ),
    ).rejects.toThrow(/signtool exited with 1/);
  });

  it("fails before it changes anything when the packager has no signIf", async () => {
    const file = await exeWith("0.15.1-rc.1");
    const before = await readFile(file);
    await expect(stampExeVersion(context(file, {}))).rejects.toThrow(
      /no signIf\(file\)/,
    );
    expect(await readFile(file)).toEqual(before);
  });

  it("does not sign on other platforms", async () => {
    const file = await exeWith("0.15.1-rc.1");
    const signIf = vi.fn(async () => true);
    await stampExeVersion({
      ...context(file, { signIf }),
      electronPlatformName: "darwin",
    });
    expect(signIf).not.toHaveBeenCalled();
  });
});

// The hook leans on a method of electron-builder that is not documented. A new
// electron-builder that renames it must fail here, not in a release build.
describe("electron-builder", () => {
  it("still has WinPackager.signIf(file) and forceCodeSigning", () => {
    const require = createRequire(import.meta.url);
    const source = readFileSync(
      require.resolve("app-builder-lib/out/winPackager.js"),
      "utf8",
    );
    expect(source).toMatch(/async signIf\(file\) \{/);
    expect(source).toMatch(/this\.forceCodeSigning/);
    // The version ADR-0073's notes were checked against (app/package.json pins it).
    const { version } = JSON.parse(
      readFileSync(
        resolve(here, "../node_modules/electron-builder/package.json"),
        "utf8",
      ),
    );
    expect(version).toBe("26.17.0");
  });
});
