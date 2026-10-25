import { randomUUID } from "node:crypto";
import { EventEmitter } from "node:events";
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import {
  buildEnvironment,
  certificateScript,
  cleanUpKeys,
  compareSnapshots,
  decodeSnapshot,
  describeCleanup,
  folders,
  guardFile,
  isGuidInBraces,
  keyFilePaths,
  mostKeyFilesARun,
  newPassword,
  parseModulus,
  readContainerName,
  removeWithRetries,
  selectThrowawayKeys,
  snapshotScript,
  subject,
  taskArguments,
  version,
} from "./test-sign.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const repo = resolve(here, "..", "..");

const empty = () => ({
  stores: { "CurrentUser\\My": [], "CurrentUser\\Root": ["AAAA"] },
  keys: {
    "Microsoft\\Crypto\\RSA": [],
    "Microsoft\\Crypto\\Keys": ["C:\\k\\one|10|1"],
  },
});

describe("compareSnapshots", () => {
  it("finds nothing when nothing changed", () => {
    expect(compareSnapshots(empty(), empty())).toEqual({
      problems: [],
      orphans: [],
    });
  });

  it("fails on a certificate that appeared or disappeared, in any store", () => {
    const after = empty();
    after.stores["CurrentUser\\My"] = ["BBBB"];
    after.stores["CurrentUser\\Root"] = [];
    const { problems } = compareSnapshots(empty(), after);
    expect(problems).toEqual([
      "Cert:\\CurrentUser\\My gained BBBB",
      "Cert:\\CurrentUser\\Root lost AAAA",
    ]);
  });

  it("reads a store PowerShell wrote as a single value or as null", () => {
    const before = empty();
    before.stores["CurrentUser\\Root"] = "AAAA";
    const after = empty();
    after.stores["CurrentUser\\My"] = null;
    expect(compareSnapshots(before, after).problems).toEqual([]);
  });

  it("reports a new key file as an orphan, not as a problem", () => {
    const after = empty();
    after.keys["Microsoft\\Crypto\\Keys"].push("C:\\k\\two|1803|2");
    expect(compareSnapshots(empty(), after)).toEqual({
      problems: [],
      orphans: ["C:\\k\\two"],
    });
  });

  it("fails on a key file that was removed or changed", () => {
    const removed = empty();
    removed.keys["Microsoft\\Crypto\\Keys"] = [];
    expect(compareSnapshots(empty(), removed).problems).toEqual([
      "key file C:\\k\\one was removed",
    ]);
    const changed = empty();
    changed.keys["Microsoft\\Crypto\\Keys"] = ["C:\\k\\one|11|1"];
    expect(compareSnapshots(empty(), changed).problems).toEqual([
      "key file C:\\k\\one changed",
    ]);
  });
});

describe("the test build", () => {
  it("keeps everything in .cache/signtest", () => {
    const f = folders(repo);
    for (const path of Object.values(f)) {
      expect(path.startsWith(resolve(repo, ".cache", "signtest"))).toBe(true);
    }
    expect(f.pfx).toBe(resolve(repo, ".cache", "signtest", "signtest.pfx"));
    expect(f.bin).not.toBe(resolve(repo, "bin"));
    expect(f.out).not.toBe(resolve(repo, "dist", "installer"));
  });

  it("is a prerelease of 0.0.0 that no release can be mistaken for", () => {
    expect(version).toBe("v0.0.0-signtest.1");
    expect(subject).toBe("padmate signtest");
  });

  it("asks task for the signed build with forward slashes and no secret", () => {
    const args = taskArguments({
      bin: "D:\\repo\\.cache\\signtest\\bin",
      out: "D:\\repo\\.cache\\signtest\\out",
    });
    expect(args).toEqual([
      "package:installer",
      "VERSION=v0.0.0-signtest.1",
      "INSTALLER_BIN=D:/repo/.cache/signtest/bin",
      "INSTALLER_OUT=D:/repo/.cache/signtest/out",
      "VERIFY_FLAGS=-allow-untrusted-root -timestamp=false",
    ]);
  });

  it("puts the password in the environment of the build only", () => {
    const password = newPassword();
    expect(password).not.toBe(newPassword());
    expect(password.length).toBeGreaterThanOrEqual(32);
    const env = buildEnvironment(
      {
        PATH: "x",
        CSC_LINK: "https://example.com/a.pfx",
        DEBUG: "electron-builder",
        PADMATE_SIGN_SHA1: "0".repeat(40),
        SIGNTOOL_PATH: "C:\\other\\signtool.exe",
      },
      { pfx: "D:\\repo\\.cache\\signtest\\signtest.pfx", password },
    );
    expect(env).toMatchObject({
      PATH: "x",
      PADMATE_SIGN: "pfx",
      PADMATE_SIGN_TEST: "1",
      PADMATE_SIGN_SUBJECT: "padmate signtest",
      WIN_CSC_LINK: "D:\\repo\\.cache\\signtest\\signtest.pfx",
      WIN_CSC_KEY_PASSWORD: password,
    });
    for (const name of [
      "CSC_LINK",
      "DEBUG",
      "PADMATE_SIGN_SHA1",
      "SIGNTOOL_PATH",
    ]) {
      expect(env).not.toHaveProperty(name);
    }
    expect(JSON.stringify(taskArguments({ bin: "a", out: "b" }))).not.toContain(
      password,
    );
  });
});

// The test must not touch a certificate store, however it is edited.
describe("the PowerShell it runs", () => {
  const scripts = { snapshot: snapshotScript, certificate: certificateScript };
  const source = readFileSync(resolve(here, "test-sign.mjs"), "utf8");

  it.each(Object.entries(scripts))(
    "%s script changes no store",
    (_, script) => {
      for (const forbidden of [
        "New-SelfSignedCertificate",
        "Import-Certificate",
        "Import-PfxCertificate",
        "certutil",
        "Remove-Item",
        "Set-ExecutionPolicy",
        "X509Store",
        "-CertStoreLocation",
      ]) {
        expect(script).not.toContain(forbidden);
      }
    },
  );

  it("makes the certificate in memory, from the environment", () => {
    expect(certificateScript).toContain("CertificateRequest");
    expect(certificateScript).toContain("CreateSelfSigned");
    expect(certificateScript).toContain("$env:PADMATE_SIGNTEST_PASSWORD");
    expect(certificateScript).not.toMatch(/password\s*=\s*['"]/i);
  });

  it("never runs electron-builder's self-signed certificate command or a policy override", () => {
    expect(source).not.toContain('create-self-signed-cert"');
    expect(source).not.toContain("-ExecutionPolicy");
  });

  it("looks at every store the pipeline could change", () => {
    for (const store of [
      "CurrentUser\\My",
      "CurrentUser\\Root",
      "CurrentUser\\TrustedPublisher",
      "LocalMachine\\TrustedPeople",
    ]) {
      expect(snapshotScript).toContain(store);
    }
    expect(snapshotScript).toContain("Microsoft\\Crypto\\RSA");
  });

  // powershell.exe writes to a pipe in the OEM code page. A user name in
  // Chinese came out as replacement characters and the key folder of the
  // profile was not the one %APPDATA% names, so no key file was recognised.
  it("prints the snapshot as Base64 of UTF-8, which no code page can change", () => {
    expect(snapshotScript).toContain("[Convert]::ToBase64String");
    expect(snapshotScript).toContain("[System.Text.Encoding]::UTF8.GetBytes");
  });

  it("is read back whole, whatever letters the profile folder has", () => {
    const snapshot = {
      stores: { "CurrentUser\\My": ["AAAA"] },
      keys: {
        "Microsoft\\Crypto\\Keys": [
          "C:\\Users\\用户名\\AppData\\Roaming\\Microsoft\\Crypto\\Keys\\9e68df|1803|638",
        ],
      },
    };
    const printed = Buffer.from(JSON.stringify(snapshot), "utf8").toString(
      "base64",
    );
    expect(decodeSnapshot(`${printed}\r\n`)).toEqual(snapshot);
    expect(
      keyFilePaths(decodeSnapshot(printed))[0].startsWith(
        "C:\\Users\\用户名\\AppData",
      ),
    ).toBe(true);
  });
});

// The private key of the throwaway certificate is in the PFX. "Deleted at the
// end, also when something failed" has to hold when a scanner still has the
// file for a moment, and when the run is interrupted.
describe("removeWithRetries", () => {
  const busy = (code = "EBUSY") => Object.assign(new Error(code), { code });

  function removerThatFails(times, code) {
    const calls = [];
    return {
      calls,
      rm: async (path, options) => {
        calls.push([path, options]);
        if (calls.length <= times) throw busy(code);
      },
    };
  }

  it("removes the file with the first attempt when nothing holds it", async () => {
    const r = removerThatFails(0);
    const sleeps = [];
    await expect(
      removeWithRetries("a.pfx", {
        rm: r.rm,
        sleep: async (ms) => void sleeps.push(ms),
      }),
    ).resolves.toBeUndefined();
    expect(r.calls).toEqual([["a.pfx", { force: true }]]);
    expect(sleeps).toEqual([]);
  });

  it.each(["EBUSY", "EPERM", "EACCES", "ENOTEMPTY"])(
    "tries again after %s, with a longer wait each time",
    async (code) => {
      const r = removerThatFails(2, code);
      const sleeps = [];
      await expect(
        removeWithRetries("a.pfx", {
          rm: r.rm,
          sleep: async (ms) => void sleeps.push(ms),
          delayMs: 100,
        }),
      ).resolves.toBeUndefined();
      expect(r.calls).toHaveLength(3);
      expect(sleeps).toEqual([100, 200]);
    },
  );

  it("gives the last error back, not throw it, when the file stays held", async () => {
    const r = removerThatFails(99);
    const error = await removeWithRetries("a.pfx", {
      rm: r.rm,
      sleep: async () => {},
      attempts: 4,
    });
    expect(error).toBeInstanceOf(Error);
    expect(error.code).toBe("EBUSY");
    expect(r.calls).toHaveLength(4);
  });

  it("does not retry an error that waiting cannot cure", async () => {
    const r = removerThatFails(99, "EINVAL");
    const error = await removeWithRetries("a.pfx", {
      rm: r.rm,
      sleep: async () => {},
    });
    expect(error.code).toBe("EINVAL");
    expect(r.calls).toHaveLength(1);
  });

  it("passes the options of a folder through", async () => {
    const r = removerThatFails(0);
    await removeWithRetries("dir", {
      rm: r.rm,
      options: { recursive: true },
    });
    expect(r.calls[0][1]).toEqual({ force: true, recursive: true });
  });

  it("removes a real file, and is content with one that is not there", async () => {
    const dir = mkdtempSync(join(tmpdir(), "padmate-rm-"));
    try {
      const file = join(dir, "signtest.pfx");
      writeFileSync(file, "x");
      await expect(removeWithRetries(file)).resolves.toBeUndefined();
      expect(existsSync(file)).toBe(false);
      await expect(removeWithRetries(file)).resolves.toBeUndefined();
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });
});

describe("guardFile", () => {
  function fakeProcess() {
    const proc = new EventEmitter();
    proc.exitCodes = [];
    proc.exit = (code) => {
      proc.exitCodes.push(code);
    };
    return proc;
  }

  it("deletes the file when the process exits, however it exits", () => {
    const proc = fakeProcess();
    const removed = [];
    guardFile("D:\\x\\signtest.pfx", {
      proc,
      rmSync: (path, options) => removed.push([path, options]),
    });
    proc.emit("exit", 1);
    expect(removed).toEqual([["D:\\x\\signtest.pfx", { force: true }]]);
  });

  it.each([
    ["SIGINT", 130],
    ["SIGTERM", 143],
    ["SIGHUP", 129],
    ["SIGBREAK", 149],
  ])("turns %s into an exit, so that the exit handler runs", (signal, code) => {
    const proc = fakeProcess();
    guardFile("a.pfx", { proc, rmSync: () => {} });
    proc.emit(signal);
    expect(proc.exitCodes).toEqual([code]);
  });

  it("does not let a failing delete hide the reason the process ends", () => {
    const proc = fakeProcess();
    guardFile("a.pfx", {
      proc,
      rmSync: () => {
        throw busyError();
      },
    });
    expect(() => proc.emit("exit", 1)).not.toThrow();
  });

  it("lets go of the process when the run is over", () => {
    const proc = fakeProcess();
    const release = guardFile("a.pfx", { proc, rmSync: () => {} });
    expect(proc.listenerCount("exit")).toBe(1);
    release();
    for (const name of ["exit", "SIGINT", "SIGTERM", "SIGHUP", "SIGBREAK"]) {
      expect(proc.listenerCount(name)).toBe(0);
    }
  });
});

function busyError() {
  return Object.assign(new Error("EBUSY"), { code: "EBUSY" });
}

// signtool imports the PFX to sign with it and leaves the private key behind as
// a CNG key file in %APPDATA%\Microsoft\Crypto\Keys. The run deletes those, and
// only those: the files that were not there before, that are directly in that
// folder, whose container name is a GUID in braces and that hold the public key
// of the throwaway certificate. Everything else in the folder belongs to the
// person and to other programs. The layout is that of a CNG key file (checked
// on this machine against a key made and deleted through CngKey): a DWORD
// version, a DWORD 0, the length of the container name in bytes at offset 8,
// the name in UTF-16 at offset 0x2c, and later the public key blob in plain.
describe("the key files signtool leaves behind", () => {
  const modulus = Buffer.from(
    Array.from({ length: 256 }, (_, i) => (i * 7 + 3) & 0xff),
  );
  const otherModulus = Buffer.from(
    Array.from({ length: 256 }, (_, i) => (i * 11 + 5) & 0xff),
  );
  const braces = () => `{${randomUUID().toUpperCase()}}`;

  function keyFile(container, publicKey = modulus) {
    const name = Buffer.from(container, "utf16le");
    const header = Buffer.alloc(0x2c);
    header.writeUInt32LE(1, 0);
    header.writeUInt32LE(name.length, 8);
    return Buffer.concat([
      header,
      name,
      Buffer.alloc(40, 1),
      publicKey,
      Buffer.alloc(300, 2),
    ]);
  }

  // A fake profile: the Keys folder, a sibling folder of the old CSP containers
  // and a few files, laid out the way a person's folder is.
  function profile() {
    const root = mkdtempSync(join(tmpdir(), "padmate-keys-"));
    const keys = join(root, "Microsoft", "Crypto", "Keys");
    const rsa = join(root, "Microsoft", "Crypto", "RSA", "S-1-5-21-1");
    mkdirSync(keys, { recursive: true });
    mkdirSync(rsa, { recursive: true });
    const put = (path, data) => {
      mkdirSync(dirname(path), { recursive: true });
      writeFileSync(path, data);
      return path;
    };
    return {
      root,
      keys,
      rsa,
      put,
      cleanup: () => rmSync(root, { recursive: true, force: true }),
    };
  }

  const listing = (dir) => readdirSync(dir, { recursive: true }).sort();

  describe("readContainerName and isGuidInBraces", () => {
    it("reads the name at offset 0x2c as the length at offset 8 says", () => {
      const name = braces();
      expect(readContainerName(keyFile(name))).toBe(name);
      expect(readContainerName(keyFile("te-Edge"))).toBe("te-Edge");
    });

    it("does not read what is not a key file", () => {
      expect(readContainerName(Buffer.alloc(0))).toBeUndefined();
      expect(
        readContainerName(Buffer.from("not a key file at all, just text")),
      ).toBeUndefined();
      const odd = keyFile(braces());
      odd.writeUInt32LE(75, 8);
      expect(readContainerName(odd)).toBeUndefined();
      const huge = keyFile(braces());
      huge.writeUInt32LE(0x7fffffff, 8);
      expect(readContainerName(huge)).toBeUndefined();
      expect(
        readContainerName(keyFile(braces()).subarray(0, 0x2c + 20)),
      ).toBeUndefined();
    });

    it("accepts a GUID in braces and nothing else", () => {
      expect(isGuidInBraces("{0A1B2C3D-4E5F-6789-ABCD-EF0123456789}")).toBe(
        true,
      );
      expect(isGuidInBraces("{0a1b2c3d-4e5f-6789-abcd-ef0123456789}")).toBe(
        true,
      );
      for (const no of [
        "",
        "0A1B2C3D-4E5F-6789-ABCD-EF0123456789",
        "{0A1B2C3D-4E5F-6789-ABCD-EF012345678}",
        "{0A1B2C3D-4E5F-6789-ABCD-EF0123456789}x",
        "x{0A1B2C3D-4E5F-6789-ABCD-EF0123456789}",
        "{0A1B2C3D-4E5F-6789-ABCD-EF012345678G}",
        "te-edge",
        "{0A1B2C3D4E5F6789ABCDEF0123456789}",
      ]) {
        expect(isGuidInBraces(no), no).toBe(false);
      }
    });
  });

  describe("parseModulus", () => {
    it("reads the line the certificate script prints", () => {
      const hex = modulus.toString("hex");
      expect(parseModulus(`modulus:${hex}\r\n`)).toEqual(modulus);
      expect(parseModulus(`noise\nmodulus:${hex}\nmore noise\n`)).toEqual(
        modulus,
      );
    });

    it("gives nothing for a missing, odd or short value", () => {
      expect(parseModulus("")).toBeUndefined();
      expect(parseModulus("modulus:\n")).toBeUndefined();
      expect(
        parseModulus(`modulus:${modulus.toString("hex")}0\n`),
      ).toBeUndefined();
      expect(parseModulus("modulus:abcdef\n")).toBeUndefined();
      expect(parseModulus(`modulus:${"zz".repeat(256)}\n`)).toBeUndefined();
    });

    it("is printed by the certificate script, and only the public part", () => {
      expect(certificateScript).toContain("modulus:");
      expect(certificateScript).toContain("ExportParameters($false)");
      expect(certificateScript).not.toContain("ExportParameters($true)");
      expect(certificateScript).not.toMatch(/\.(D|P|Q|DP|DQ|InverseQ)\b/);
    });
  });

  describe("keyFilePaths", () => {
    it("lists the paths of every key folder, whatever PowerShell wrote", () => {
      expect(
        keyFilePaths({
          stores: {},
          keys: {
            "Microsoft\\Crypto\\RSA": null,
            "Microsoft\\Crypto\\Keys": ["C:\\k\\one|10|1", "C:\\k\\two|11|2"],
            other: "C:\\k\\only|1|3",
          },
        }),
      ).toEqual(["C:\\k\\one", "C:\\k\\two", "C:\\k\\only"]);
    });
  });

  describe("which files are deleted", () => {
    it("deletes the new files that hold the certificate's key and touches nothing else", async () => {
      const p = profile();
      try {
        const unchanged = new Map();
        const keep = (path, data) => {
          p.put(path, data);
          unchanged.set(path, data);
          return path;
        };
        // What was there before the run, even if it looks just like ours.
        const before = [
          keep(join(p.keys, "old-person-key"), keyFile(braces())),
          keep(join(p.keys, "old-empty"), Buffer.alloc(0)),
        ];
        // What appeared during the run but is not ours, one reason each.
        const others = [
          keep(
            join(p.keys, "another-app-key"),
            keyFile(braces(), otherModulus),
          ),
          keep(join(p.keys, "named-key"), keyFile("te-SomeApp-1234")),
          keep(
            join(p.keys, "guid-without-braces"),
            keyFile(randomUUID().toUpperCase()),
          ),
          keep(join(p.keys, "junk"), Buffer.from("x".repeat(500))),
          keep(join(p.keys, "empty"), Buffer.alloc(0)),
          keep(join(p.keys, "sub", "nested-key"), keyFile(braces())),
          keep(join(p.rsa, "csp-container"), keyFile(braces())),
          keep(
            join(p.keys, "too-big"),
            Buffer.concat([keyFile(braces()), Buffer.alloc(80 * 1024)]),
          ),
        ];
        const ours = [
          p.put(join(p.keys, "ours-1"), keyFile(braces())),
          p.put(join(p.keys, "ours-2"), keyFile(braces())),
        ];
        const result = await cleanUpKeys({
          orphans: [...others, ...ours],
          before,
          keysDir: p.keys,
          modulus,
        });
        expect(result.deleted.map((d) => d.path).sort()).toEqual(
          [...ours].sort(),
        );
        for (const d of result.deleted)
          expect(isGuidInBraces(d.container)).toBe(true);
        expect(result.failed).toEqual([]);
        expect(result.kept.map((k) => k.path).sort()).toEqual(
          [...others].sort(),
        );
        for (const k of result.kept) expect(k.why).toBeTruthy();
        // On disk: ours are gone, every other file is there with its bytes.
        for (const f of ours) expect(existsSync(f)).toBe(false);
        for (const [path, data] of unchanged) {
          expect(readFileSync(path).equals(data), path).toBe(true);
        }
        expect(listing(p.keys)).toEqual(
          [
            "another-app-key",
            "empty",
            "guid-without-braces",
            "junk",
            "named-key",
            "old-empty",
            "old-person-key",
            "sub",
            join("sub", "nested-key"),
            "too-big",
          ].sort(),
        );
        expect(listing(p.rsa)).toEqual(["csp-container"]);
      } finally {
        p.cleanup();
      }
    });

    it("compares the folder the way Windows does, whatever the case", async () => {
      const p = profile();
      try {
        const file = p.put(join(p.keys, "ours"), keyFile(braces()));
        const shouted = file.replace(p.keys, p.keys.toUpperCase());
        const picked = await selectThrowawayKeys({
          orphans: [shouted],
          before: [],
          keysDir: p.keys,
          modulus,
        });
        expect(picked.selected.map((s) => s.path)).toEqual([shouted]);
        const known = await selectThrowawayKeys({
          orphans: [file],
          before: [file.toUpperCase()],
          keysDir: p.keys,
          modulus,
        });
        expect(known.selected).toEqual([]);
        expect(known.kept[0].why).toMatch(/before/);
      } finally {
        p.cleanup();
      }
    });

    it("deletes nothing when the certificate's public key is not known", async () => {
      const p = profile();
      try {
        const file = p.put(join(p.keys, "ours"), keyFile(braces()));
        for (const unknown of [
          undefined,
          Buffer.alloc(0),
          Buffer.from("short"),
        ]) {
          const result = await cleanUpKeys({
            orphans: [file],
            before: [],
            keysDir: p.keys,
            modulus: unknown,
          });
          expect(result.deleted).toEqual([]);
          expect(existsSync(file)).toBe(true);
          expect(result.kept[0].why).toMatch(/public key/);
        }
      } finally {
        p.cleanup();
      }
    });

    it("deletes nothing when more files match than one run can leave", async () => {
      const p = profile();
      try {
        const files = Array.from({ length: mostKeyFilesARun + 1 }, (_, i) =>
          p.put(join(p.keys, `ours-${i}`), keyFile(braces())),
        );
        const result = await cleanUpKeys({
          orphans: files,
          before: [],
          keysDir: p.keys,
          modulus,
        });
        expect(result.deleted).toEqual([]);
        expect(listing(p.keys)).toHaveLength(mostKeyFilesARun + 1);
        expect(result.kept[0].why).toMatch(
          new RegExp(`more than ${mostKeyFilesARun}`),
        );
        // At the limit it still deletes.
        const few = await cleanUpKeys({
          orphans: files.slice(0, mostKeyFilesARun),
          before: [],
          keysDir: p.keys,
          modulus,
        });
        expect(few.deleted).toHaveLength(mostKeyFilesARun);
      } finally {
        p.cleanup();
      }
    });

    it("looks again at a file just before deleting it", async () => {
      const p = profile();
      try {
        const file = p.put(join(p.keys, "ours"), keyFile(braces()));
        const swapped = keyFile(braces(), otherModulus);
        let reads = 0;
        const removed = [];
        const result = await cleanUpKeys({
          orphans: [file],
          before: [],
          keysDir: p.keys,
          modulus,
          read: async (path) => (++reads === 1 ? readFileSync(path) : swapped),
          remove: async (path) => void removed.push(path),
        });
        expect(reads).toBe(2);
        expect(removed).toEqual([]);
        expect(result.deleted).toEqual([]);
        expect(result.kept[0].why).toMatch(/changed/);
      } finally {
        p.cleanup();
      }
    });

    it("never hands the remover a path it was not given as a new file of the keys folder", async () => {
      const p = profile();
      try {
        const old = p.put(join(p.keys, "old"), keyFile(braces()));
        const ours = p.put(join(p.keys, "ours"), keyFile(braces()));
        const outside = p.put(join(p.root, "elsewhere"), keyFile(braces()));
        const removed = [];
        await cleanUpKeys({
          orphans: [ours, outside],
          before: [old],
          keysDir: p.keys,
          modulus,
          remove: async (path) => void removed.push(path),
        });
        expect(removed).toEqual([ours]);
      } finally {
        p.cleanup();
      }
    });

    it("leaves alone what lstat says is not a regular file, a link for one", async () => {
      const p = profile();
      try {
        const link = p.put(join(p.keys, "link"), keyFile(braces()));
        const removed = [];
        // What lstat answers for a symbolic link, which this machine may not
        // let a test make.
        const status = async () => ({
          isFile: () => false,
          isSymbolicLink: () => true,
        });
        const how = { orphans: [link], before: [], keysDir: p.keys, modulus };
        // Neither the choice nor the look before the deletion lets it through.
        expect(
          (await selectThrowawayKeys({ ...how, status })).selected,
        ).toEqual([]);
        const result = await cleanUpKeys({
          ...how,
          status,
          remove: async (path) => void removed.push(path),
        });
        expect(removed).toEqual([]);
        expect(result.deleted).toEqual([]);
        expect(result.kept[0].why).toMatch(/not a regular file/);
      } finally {
        p.cleanup();
      }
    });

    it("reports a file it cannot delete and goes on with the others", async () => {
      const p = profile();
      try {
        const a = p.put(join(p.keys, "a"), keyFile(braces()));
        const b = p.put(join(p.keys, "b"), keyFile(braces()));
        const result = await cleanUpKeys({
          orphans: [a, b],
          before: [],
          keysDir: p.keys,
          modulus,
          remove: async (path) => (path === a ? busyError() : undefined),
        });
        expect(result.failed.map((f) => f.path)).toEqual([a]);
        expect(result.deleted.map((d) => d.path)).toEqual([b]);
      } finally {
        p.cleanup();
      }
    });

    it("says what it deleted and what it left", () => {
      const lines = describeCleanup({
        deleted: [
          {
            path: "C:\\k\\ours",
            container: "{0A1B2C3D-4E5F-6789-ABCD-EF0123456789}",
          },
        ],
        kept: [
          {
            path: "C:\\k\\other",
            why: "the container name is not a GUID in braces",
          },
        ],
        failed: [{ path: "C:\\k\\busy", error: busyError() }],
      }).join("\n");
      expect(lines).toContain("deleted 1 key file");
      expect(lines).toContain("C:\\k\\ours");
      expect(lines).toContain("{0A1B2C3D-4E5F-6789-ABCD-EF0123456789}");
      expect(lines).toContain("C:\\k\\other");
      expect(lines).toContain("not a GUID in braces");
      expect(lines).toContain("C:\\k\\busy");
      expect(lines).toContain("EBUSY");
      expect(describeCleanup({ deleted: [], kept: [], failed: [] })).toEqual(
        [],
      );
    });
  });
});
