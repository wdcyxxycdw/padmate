import { execFileSync, spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import {
  appVersion,
  childEnvironment,
  findSignTool,
  lineRedactor,
  outputFolder,
  parse,
  redact,
  runElectronBuilder,
  signingPlan,
} from "./package-installer.mjs";
import { isMirror } from "./public-mirror.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const app = resolve(here, "..");
const require = createRequire(import.meta.url);

const signtool =
  "C:\\Program Files (x86)\\Windows Kits\\10\\bin\\10.0.26100.0\\x64\\signtool.exe";
const tools = { findSignTool: () => signtool };
const thumbprint = "0123456789abcdef0123456789abcdef01234567";
const password = "correct horse battery staple";
const pfx = "C:\\signing\\padmate.pfx";

const store = {
  PADMATE_SIGN: "store",
  PADMATE_SIGN_SHA1: thumbprint,
  PADMATE_SIGN_SUBJECT: "CN=Open Source Developer, O=Example, C=JP",
};
const pfxEnv = {
  PADMATE_SIGN: "pfx",
  PADMATE_SIGN_SUBJECT: "padmate signtest",
  WIN_CSC_LINK: pfx,
  WIN_CSC_KEY_PASSWORD: password,
};
const azure = {
  PADMATE_SIGN: "azure",
  PADMATE_SIGN_SUBJECT: "CN=Example, C=JP",
  PADMATE_SIGN_AZURE_ENDPOINT: "https://eus.codesigning.azure.net",
  PADMATE_SIGN_AZURE_ACCOUNT: "account",
  PADMATE_SIGN_AZURE_PROFILE: "profile",
};
const certificateVariables = [
  "WIN_CSC_LINK",
  "CSC_LINK",
  "WIN_CSC_KEY_PASSWORD",
  "CSC_KEY_PASSWORD",
];

describe("appVersion", () => {
  it("takes a release tag and drops the v", () => {
    expect(appVersion("v1.0.0")).toBe("1.0.0");
    expect(appVersion("v0.0.0-signtest.1")).toBe("0.0.0-signtest.1");
  });
  it.each(["1.0.0", "v1.0", "dev", "", undefined])("rejects %j", (label) => {
    expect(() => appVersion(label)).toThrow(/not a release tag/);
  });
});

describe("signingPlan: none", () => {
  it.each([undefined, "", "  ", "none"])(
    "PADMATE_SIGN=%j does not sign and empties ambient certificates",
    (value) => {
      const plan = signingPlan({ PADMATE_SIGN: value }, tools);
      expect(plan.mode).toBe("none");
      expect(plan.args).toEqual([]);
      for (const name of certificateVariables) {
        expect(plan.envPatch).toHaveProperty(name, "");
      }
    },
  );

  it("strips a certificate that happens to be in the environment", () => {
    const ambient = {
      PATH: "x",
      WIN_CSC_LINK: pfx,
      CSC_LINK: "https://example.com/c.pfx",
      WIN_CSC_KEY_PASSWORD: password,
      CSC_KEY_PASSWORD: password,
    };
    const env = childEnvironment(ambient, signingPlan(ambient, tools));
    // Present and empty, not deleted: electron-builder.env cannot fill them.
    for (const name of certificateVariables) expect(env[name]).toBe("");
    expect(JSON.stringify(env)).not.toContain(password);
    expect(env.PATH).toBe("x");
    expect(env.CSC_IDENTITY_AUTO_DISCOVERY).toBe("false");
  });

  it("ignores the other PADMATE_SIGN variables", () => {
    const plan = signingPlan(
      { PADMATE_SIGN_SHA1: "nonsense", PADMATE_SIGN_SUBJECT: "x" },
      tools,
    );
    expect(plan.args).toEqual([]);
    expect(plan.envPatch).not.toHaveProperty("ELECTRON_BUILDER_OFFLINE");
  });

  it("refuses a mode it does not know", () => {
    expect(() => signingPlan({ PADMATE_SIGN: "sign" }, tools)).toThrow(
      /not one of none, store, pfx, azure/,
    );
    expect(() => signingPlan({ PADMATE_SIGN: "PFX" }, tools)).toThrow();
  });
});

describe("signingPlan: store", () => {
  it("passes the thumbprint, the publisher and forces signing", () => {
    const plan = signingPlan(store, tools);
    expect(plan.mode).toBe("store");
    expect(plan.args).toEqual([
      "--config.win.forceCodeSigning=true",
      "--config.win.signtoolOptions.publisherName=CN=Open Source Developer, O=Example, C=JP",
      `--config.win.signtoolOptions.certificateSha1=${thumbprint.toUpperCase()}`,
    ]);
    expect(plan.envPatch.SIGNTOOL_PATH).toBe(signtool);
    for (const name of certificateVariables) {
      expect(plan.envPatch).toHaveProperty(name, "");
    }
  });

  it.each([
    ["", "required"],
    ["abc", "40 hexadecimal"],
    [`${thumbprint}0`, "40 hexadecimal"],
    ["g".repeat(40), "40 hexadecimal"],
    ["0123456789abcdef 123456789abcdef01234567", "40 hexadecimal"],
  ])("rejects the thumbprint %j", (value, message) => {
    expect(() =>
      signingPlan({ ...store, PADMATE_SIGN_SHA1: value }, tools),
    ).toThrow(message);
  });

  it("needs a publisher", () => {
    expect(() =>
      signingPlan({ ...store, PADMATE_SIGN_SUBJECT: "" }, tools),
    ).toThrow(/PADMATE_SIGN_SUBJECT is required/);
    expect(() =>
      signingPlan({ ...store, PADMATE_SIGN_SUBJECT: "a\nb" }, tools),
    ).toThrow(/control characters/);
    expect(() =>
      signingPlan({ ...store, PADMATE_SIGN_SUBJECT: "x".repeat(257) }, tools),
    ).toThrow(/256/);
  });

  it("does not take a password from the environment", () => {
    const plan = signingPlan(
      { ...store, WIN_CSC_KEY_PASSWORD: password },
      tools,
    );
    expect(JSON.stringify(plan)).not.toContain(password);
    expect(
      childEnvironment({ WIN_CSC_KEY_PASSWORD: password }, plan)
        .WIN_CSC_KEY_PASSWORD,
    ).toBe("");
  });
});

describe("signingPlan: pfx", () => {
  it("passes the file and the password in the environment, never in the arguments", () => {
    const plan = signingPlan(pfxEnv, tools);
    expect(plan.mode).toBe("pfx");
    expect(plan.args).toEqual([
      "--config.win.forceCodeSigning=true",
      "--config.win.signtoolOptions.publisherName=padmate signtest",
    ]);
    expect(plan.envPatch.WIN_CSC_LINK).toBe(pfx);
    expect(plan.envPatch.WIN_CSC_KEY_PASSWORD).toBe(password);
    expect(plan.envPatch.SIGNTOOL_PATH).toBe(signtool);
    const joined = plan.args.join("\n");
    expect(joined).not.toContain(password);
    expect(joined).not.toContain(pfx);
  });

  it("moves the CSC_ spellings to the WIN_CSC_ ones", () => {
    const plan = signingPlan(
      {
        PADMATE_SIGN: "pfx",
        PADMATE_SIGN_SUBJECT: "x",
        CSC_LINK: "D:\\keys\\a.p12",
        CSC_KEY_PASSWORD: password,
      },
      tools,
    );
    expect(plan.envPatch.WIN_CSC_LINK).toBe("D:\\keys\\a.p12");
    expect(plan.envPatch.WIN_CSC_KEY_PASSWORD).toBe(password);
    expect(plan.envPatch).toHaveProperty("CSC_LINK", "");
    expect(plan.envPatch).toHaveProperty("CSC_KEY_PASSWORD", "");
  });

  it.each([
    "https://example.com/padmate.pfx",
    "http://example.com/padmate.pfx",
    "file:///C:/padmate.pfx",
    "data:application/x-pkcs12;base64,MIIK",
    "MIIKXAIBAzCCCiYGCSqGSIb3DQEHAaCCChcEggoTMIIKDzCC",
    "padmate.pfx",
    ".\\padmate.pfx",
    "..\\padmate.pfx",
    "\\\\server\\share\\padmate.pfx",
    "C:\\signing\\padmate.cer",
    "C:\\signing\\padmate",
    "C:\\signing\\",
  ])("rejects WIN_CSC_LINK=%j", (link) => {
    expect(() => signingPlan({ ...pfxEnv, WIN_CSC_LINK: link }, tools)).toThrow(
      /local \.pfx or \.p12 file/,
    );
  });

  it("accepts a .p12 in any case", () => {
    expect(
      signingPlan({ ...pfxEnv, WIN_CSC_LINK: "D:\\k\\Key.P12" }, tools).mode,
    ).toBe("pfx");
  });

  it("needs a non-empty password and a link", () => {
    for (const value of [undefined, "", "   "]) {
      expect(() =>
        signingPlan({ ...pfxEnv, WIN_CSC_KEY_PASSWORD: value }, tools),
      ).toThrow(/needs WIN_CSC_KEY_PASSWORD/);
    }
    expect(() =>
      signingPlan({ ...pfxEnv, WIN_CSC_LINK: undefined }, tools),
    ).toThrow(/needs WIN_CSC_LINK/);
  });

  it("an error never repeats the password or the link", () => {
    for (const bad of [
      { WIN_CSC_LINK: `https://token:${password}@example.com/a.pfx` },
      { WIN_CSC_LINK: "relative.pfx" },
    ]) {
      try {
        signingPlan({ ...pfxEnv, ...bad }, tools);
        expect.unreachable();
      } catch (error) {
        expect(error.message).not.toContain(password);
        expect(error.message).not.toContain(bad.WIN_CSC_LINK);
      }
    }
  });
});

describe("signingPlan: azure", () => {
  it("maps PADMATE_SIGN_AZURE_* to azureSignOptions", () => {
    const plan = signingPlan(azure, tools);
    expect(plan.args).toEqual([
      "--config.win.forceCodeSigning=true",
      "--config.win.azureSignOptions.endpoint=https://eus.codesigning.azure.net",
      "--config.win.azureSignOptions.codeSigningAccountName=account",
      "--config.win.azureSignOptions.certificateProfileName=profile",
      "--config.win.azureSignOptions.publisherName=CN=Example, C=JP",
    ]);
    // No signtool: Artifact Signing does not use it.
    expect(plan.envPatch).not.toHaveProperty("SIGNTOOL_PATH");
    for (const name of certificateVariables) {
      expect(plan.envPatch).toHaveProperty(name, "");
    }
  });

  it.each([
    "PADMATE_SIGN_AZURE_ENDPOINT",
    "PADMATE_SIGN_AZURE_ACCOUNT",
    "PADMATE_SIGN_AZURE_PROFILE",
    "PADMATE_SIGN_SUBJECT",
  ])("needs %s", (name) => {
    expect(() => signingPlan({ ...azure, [name]: "" }, tools)).toThrow(name);
  });
});

describe("signingPlan: every signing mode", () => {
  it.each([store, pfxEnv, azure])("forces code signing (%#)", (env) => {
    expect(signingPlan(env, tools).args).toContain(
      "--config.win.forceCodeSigning=true",
    );
  });

  it("never puts a secret in an argument or in what is printed", () => {
    for (const env of [store, pfxEnv, azure]) {
      const plan = signingPlan(
        { ...env, AZURE_CLIENT_SECRET: "client-secret" },
        tools,
      );
      const text = JSON.stringify(plan.args);
      expect(text).not.toContain(password);
      expect(text).not.toContain("client-secret");
    }
  });

  it("takes the timestamp server for store and pfx only, as http or https", () => {
    for (const env of [store, pfxEnv]) {
      const plan = signingPlan(
        { ...env, PADMATE_SIGN_TIMESTAMP: "http://timestamp.example.com/tsa" },
        tools,
      );
      expect(plan.args).toContain(
        "--config.win.signtoolOptions.rfc3161TimeStampServer=http://timestamp.example.com/tsa",
      );
    }
    for (const bad of [
      "timestamp.example.com",
      "ftp://x/y",
      "file:///x",
      "https://user:pass@x/y",
      "javascript:alert(1)",
    ]) {
      expect(() =>
        signingPlan({ ...store, PADMATE_SIGN_TIMESTAMP: bad }, tools),
      ).toThrow(/PADMATE_SIGN_TIMESTAMP/);
    }
    expect(
      signingPlan({ ...azure, PADMATE_SIGN_TIMESTAMP: "http://t/" }, tools)
        .args.join(" ")
        .includes("rfc3161"),
    ).toBe(false);
  });

  it("PADMATE_SIGN_TEST=1 builds offline, and only then", () => {
    for (const env of [store, pfxEnv, azure]) {
      expect(signingPlan(env, tools).envPatch).not.toHaveProperty(
        "ELECTRON_BUILDER_OFFLINE",
      );
      expect(
        signingPlan({ ...env, PADMATE_SIGN_TEST: "1" }, tools).envPatch
          .ELECTRON_BUILDER_OFFLINE,
      ).toBe("true");
      expect(
        signingPlan({ ...env, PADMATE_SIGN_TEST: "true" }, tools).envPatch,
      ).not.toHaveProperty("ELECTRON_BUILDER_OFFLINE");
    }
  });

  // electron-builder echoes the signtool command line when its debug output is
  // on, and `debug` turns the "electron-builder" namespace on for more
  // patterns than that name: *, electron-*, electron*. The only safe value is
  // none, and "" rather than a removed variable, as with the certificate
  // variables (electron-builder.env cannot bring it back).
  it.each([
    "electron-builder",
    "*",
    "electron-*",
    "electron*",
    "*builder",
    "express:*,electron-builder",
    "express:*",
  ])("switches the debug output off, whatever DEBUG is (%s)", (debug) => {
    for (const env of [store, pfxEnv, azure]) {
      const plan = signingPlan({ ...env, DEBUG: debug }, tools);
      expect(plan.envPatch).toHaveProperty("DEBUG", "");
      const child = childEnvironment({ DEBUG: debug, OTHER: "1" }, plan);
      expect(child.DEBUG).toBe("");
      expect(child.OTHER).toBe("1");
    }
  });

  it("switches it off even when DEBUG was not set, and in the Windows spelling of the name", () => {
    const plan = signingPlan(pfxEnv, tools);
    expect(plan.envPatch).toHaveProperty("DEBUG", "");
    // Environment names are not case sensitive on Windows: one spelling must not
    // survive next to the other.
    const child = childEnvironment({ debug: "*", Debug: "*" }, plan);
    expect(
      Object.keys(child).filter((k) => k.toUpperCase() === "DEBUG"),
    ).toEqual(["DEBUG"]);
    expect(child.DEBUG).toBe("");
  });

  it("leaves DEBUG alone in a build that signs nothing", () => {
    const plan = signingPlan({ DEBUG: "electron-builder" }, tools);
    expect(plan.envPatch).not.toHaveProperty("DEBUG");
    expect(childEnvironment({ DEBUG: "electron-builder" }, plan).DEBUG).toBe(
      "electron-builder",
    );
  });

  it("finds signtool.exe, or says how to", () => {
    expect(() => signingPlan(store, { findSignTool: () => undefined })).toThrow(
      /signtool\.exe was not found.*SIGNTOOL_PATH/s,
    );
    const own = "D:\\tools\\signtool.exe";
    const plan = signingPlan(
      { ...store, SIGNTOOL_PATH: own },
      {
        findSignTool: () => {
          throw new Error("must not look");
        },
      },
    );
    expect(plan.envPatch).not.toHaveProperty("SIGNTOOL_PATH");
    for (const bad of [
      "signtool.exe",
      "D:\\tools\\other.exe",
      "\\x\\signtool.exe",
    ]) {
      expect(() =>
        signingPlan({ ...store, SIGNTOOL_PATH: bad }, tools),
      ).toThrow(/SIGNTOOL_PATH/);
    }
  });
});

/// electron-builder removes a password from the command line of a failed step,
// but it replaces it with the unsalted SHA-256 of the password, in the message of
// the error and whatever DEBUG is. For a password a person chose that is an
// offline guessing oracle, and the build log is the one place it would be kept.
describe("a failed signtool step cannot put the password hash in the log", () => {
  const { removePassword } = require(
    resolve(app, "node_modules/builder-util/out/util"),
  );
  const hash =
    "5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8";

  it("removes what electron-builder's own removePassword leaves of the password", () => {
    const line = removePassword(
      'Exit code: 1. Command failed: signtool.exe sign /f C:\\a.pfx /p password /fd sha256 "C:\\x.exe"',
    );
    expect(line).toContain("(sha256 hash)"); // the thing to remove is real
    expect(line).not.toContain("password /fd");
    const clean = redact(line, ["password"]);
    expect(clean).not.toMatch(/[0-9a-f]{64}/);
    expect(clean).not.toContain("sha256 hash");
    expect(clean).toContain("signtool.exe sign");
    expect(clean).toContain("/fd sha256");
  });

  it("removes every hash of a text, in either case, and nothing else", () => {
    const thumbprint = "ab".repeat(20);
    const text = [
      `a ${hash} (sha256 hash) b`,
      `${hash.toUpperCase()} (sha256 hash)`,
      `/fd sha256 ${thumbprint}`,
    ].join("\n");
    const clean = redact(text);
    expect(clean).not.toContain(hash);
    expect(clean).not.toContain(hash.toUpperCase());
    expect(clean.match(/password hash removed/g)).toHaveLength(2);
    // A 40-digit thumbprint is not a password hash.
    expect(clean).toContain(thumbprint);
  });

  it("removes the password itself when it shows up, but not a short one", () => {
    expect(
      redact("signed with hunter2-long-secret now", ["hunter2-long-secret"]),
    ).toBe("signed with *** now");
    expect(redact("abc", ["abc"])).toBe("abc");
    expect(redact("x", ["", undefined])).toBe("x");
  });

  it("works on lines, whatever the chunks are", () => {
    const out = [];
    const r = lineRedactor((text) => out.push(text), ["longsecret"]);
    const text = `first ${hash} (sha256 hash) last\r\nsecond longsecret\nthird`;
    // Cut anywhere, also inside the hash and inside the line end.
    for (let i = 0; i < text.length; i += 7) r.push(text.slice(i, i + 7));
    r.end();
    const all = out.join("");
    expect(all).toBe("first (password hash removed) last\r\nsecond ***\nthird");
    expect(all).not.toContain(hash);
    // Complete lines are written as soon as they are complete.
    expect(out.length).toBeGreaterThan(1);
    // A second end() writes nothing.
    r.end();
    expect(out.join("")).toBe(all);
  });

  it("holds an unfinished line back until it is finished, and writes it at the end", () => {
    const out = [];
    const r = lineRedactor((text) => out.push(text));
    r.push("no newline yet ");
    expect(out).toEqual([]);
    r.push("and now\n");
    expect(out).toEqual(["no newline yet and now\n"]);
    r.push("tail");
    r.end();
    expect(out).toEqual(["no newline yet and now\n", "tail"]);
  });
  // The real thing: a child process that prints what electron-builder prints
  // when signtool fails, through the pipes runElectronBuilder reads.
  describe("runElectronBuilder", () => {
    const lines = [
      `Exit code: 1. Command failed: signtool.exe sign /p ${hash} (sha256 hash) /fd sha256`,
      "and the password itself: longsecret-1234",
    ];
    const script = `
      process.stdout.write(${JSON.stringify(`${lines[0]}\n`)});
      process.stderr.write(${JSON.stringify(`${lines[1]}\n`)});
      process.stdout.write("unfinished");
      process.exitCode = Number(process.env.CODE);
    `;
    function capture() {
      const text = { out: "", err: "" };
      return {
        text,
        stdout: {
          write: (t) => {
            text.out += t;
          },
        },
        stderr: {
          write: (t) => {
            text.err += t;
          },
        },
      };
    }

    it("shows a failing build's output without the hash or the password, and fails", async () => {
      const c = capture();
      await expect(
        runElectronBuilder(
          ["-e", script],
          {
            ...process.env,
            CODE: "3",
            WIN_CSC_KEY_PASSWORD: "longsecret-1234",
          },
          { redacting: true, cwd: here, stdout: c.stdout, stderr: c.stderr },
        ),
      ).rejects.toThrow("electron-builder failed (3)");
      expect(c.text.out).toBe(
        "Exit code: 1. Command failed: signtool.exe sign /p (password hash removed) /fd sha256\nunfinished",
      );
      expect(c.text.err).toBe("and the password itself: ***\n");
      expect(c.text.out + c.text.err).not.toContain(hash);
      expect(c.text.out + c.text.err).not.toContain("longsecret-1234");
    });

    it("resolves when the build succeeds", async () => {
      const c = capture();
      await runElectronBuilder(
        ["-e", script],
        { ...process.env, CODE: "0" },
        { redacting: true, cwd: here, stdout: c.stdout, stderr: c.stderr },
      );
      expect(c.text.out).toContain("(password hash removed)");
    });

    it("rejects when the program cannot be started", async () => {
      await expect(
        runElectronBuilder(["-e", "0"], process.env, {
          redacting: true,
          cwd: join(here, "no", "such", "folder"),
        }),
      ).rejects.toThrow();
    });
  });
});

describe("findSignTool", () => {
  const root = "C:\\sdk\\bin";
  const present =
    (...files) =>
    (file) =>
      files.includes(file);

  it("picks the newest version that has an x64 signtool, comparing numbers", () => {
    const file = (v) => `${root}\\${v}\\x64\\signtool.exe`;
    const versions = [
      "10.0.9600.0",
      "10.0.26100.0",
      "10.0.19041.0",
      "x86",
      "10.0.2.0.0",
    ];
    expect(
      findSignTool({
        root,
        readdir: () => versions,
        exists: present(
          file("10.0.9600.0"),
          file("10.0.26100.0"),
          file("10.0.19041.0"),
        ),
      }),
    ).toBe(file("10.0.26100.0"));
    // The newest has no signtool: the next one.
    expect(
      findSignTool({
        root,
        readdir: () => versions,
        exists: present(file("10.0.9600.0"), file("10.0.19041.0")),
      }),
    ).toBe(file("10.0.19041.0"));
  });

  it("is undefined without an SDK", () => {
    expect(
      findSignTool({
        root,
        readdir: () => {
          throw new Error("ENOENT");
        },
      }),
    ).toBeUndefined();
    expect(
      findSignTool({ root, readdir: () => ["10.0.1.0"], exists: () => false }),
    ).toBeUndefined();
  });
});

describe("the output folder of a test build", () => {
  it("is the default without --output", () => {
    expect(outputFolder(undefined)).toBe(resolve(app, "../dist/installer"));
  });
  it("must be inside .cache or dist, because it is emptied", () => {
    const inside = resolve(app, "../.cache/signtest/out");
    expect(outputFolder(inside)).toBe(inside);
    const other = resolve(app, "../dist/other");
    expect(outputFolder(other)).toBe(other);
    for (const bad of [
      resolve(app, "../.cache"),
      resolve(app, "../dist"),
      resolve(app, ".."),
      resolve(app),
      resolve(app, "../.cache/.."),
      "C:\\",
      "C:\\Users",
      resolve(app, "../.cachefoo"),
    ]) {
      expect(() => outputFolder(bad)).toThrow(/inside \.cache or dist/);
    }
  });
});

describe("parse", () => {
  it("takes --version and an optional --output", () => {
    expect(parse(["--version", "v1.0.0"])).toEqual({
      label: "v1.0.0",
      output: undefined,
    });
    expect(parse(["--version", "v1.0.0", "--output", "x"])).toEqual({
      label: "v1.0.0",
      output: "x",
    });
    for (const bad of [
      [],
      ["--version"],
      ["v1.0.0"],
      ["--output", "x", "--version", "v1"],
      ["--version", "v1.0.0", "--out", "x"],
      ["--version", "v1.0.0", "--output"],
    ]) {
      expect(() => parse(bad)).toThrow(/usage/);
    }
  });
});

// What electron-builder makes of the flags: the real command line parser, the
// real configuration loader and schema validation, on app/electron-builder.yml.
describe("electron-builder takes the flags", () => {
  const { createYargs, configureBuildCommand, normalizeOptions } = require(
    resolve(app, "node_modules/electron-builder/out/builder"),
  );
  const { getConfig, validateConfiguration } = require(
    resolve(app, "node_modules/app-builder-lib/out/util/config/config"),
  );
  const { DebugLogger } = require(
    resolve(app, "node_modules/builder-util/out/DebugLogger"),
  );

  async function effectiveConfig(plan, extra = []) {
    const argv = [
      "--win",
      "nsis",
      "--x64",
      "--publish",
      "never",
      "--config",
      "electron-builder.yml",
      "--config.extraMetadata.version=1.0.0",
      ...extra,
      ...plan.args,
    ];
    let parsed;
    createYargs()
      .command(["build", "*"], "Build", configureBuildCommand, (a) => {
        parsed = a;
      })
      .strict()
      .parseSync(argv);
    // As app-builder-lib's Packager does with options.config.
    const options = normalizeOptions(parsed);
    const configFromOptions = options.config;
    const configPath = configFromOptions.extends;
    delete configFromOptions.extends;
    const config = await getConfig(app, configPath, configFromOptions);
    await validateConfiguration(config, new DebugLogger(false));
    return config;
  }

  it("signs nothing by default", async () => {
    const config = await effectiveConfig(signingPlan({}, tools));
    expect(config.win.forceCodeSigning).toBe(false);
  });

  it("store mode: thumbprint and publisher join the sha256 and timestamp of the file", async () => {
    const config = await effectiveConfig(signingPlan(store, tools));
    expect(config.win.forceCodeSigning).toBe(true);
    expect(config.win.signtoolOptions).toMatchObject({
      publisherName: "CN=Open Source Developer, O=Example, C=JP",
      certificateSha1: thumbprint.toUpperCase(),
      signingHashAlgorithms: ["sha256"],
      rfc3161TimeStampServer: "http://timestamp.digicert.com",
    });
    expect(config.win.signtoolOptions).not.toHaveProperty("certificateFile");
  });

  it("pfx mode: no certificate in the configuration", async () => {
    const config = await effectiveConfig(signingPlan(pfxEnv, tools));
    expect(config.win.forceCodeSigning).toBe(true);
    expect(config.win.signtoolOptions.publisherName).toBe("padmate signtest");
    expect(JSON.stringify(config)).not.toContain(password);
    expect(JSON.stringify(config)).not.toContain("padmate.pfx");
  });

  it("a timestamp server replaces the default", async () => {
    const config = await effectiveConfig(
      signingPlan(
        { ...store, PADMATE_SIGN_TIMESTAMP: "http://t.example/" },
        tools,
      ),
    );
    expect(config.win.signtoolOptions.rfc3161TimeStampServer).toBe(
      "http://t.example/",
    );
  });

  it("azure mode is a valid configuration", async () => {
    const config = await effectiveConfig(signingPlan(azure, tools));
    expect(config.win.forceCodeSigning).toBe(true);
    expect(config.win.azureSignOptions).toMatchObject({
      endpoint: "https://eus.codesigning.azure.net",
      codeSigningAccountName: "account",
      certificateProfileName: "profile",
      publisherName: "CN=Example, C=JP",
    });
  });

  it("the output folder override reaches directories.output", async () => {
    const out = resolve(app, "../.cache/signtest/out");
    const config = await effectiveConfig(signingPlan({}, tools), [
      `--config.directories.output=${out}`,
    ]);
    expect(config.directories.output).toBe(out);
  });
});

// electron-builder's CLI reads app/electron-builder.env (the working directory
// is app/) and sets every variable that is not in the environment yet. A
// variable the plan only deleted would therefore come back from that file, and
// a build meant to be unsigned would be signed with an ambient certificate.
describe("an electron-builder.env file cannot bring a certificate back", () => {
  const loadEnvModule = resolve(
    app,
    "node_modules/app-builder-lib/out/util/config/load.js",
  );
  const script = `
    const { loadEnv } = require(process.argv[1]);
    loadEnv(process.argv[2]).then(() => {
      const out = {};
      for (const name of JSON.parse(process.argv[3]))
        out[name] = Object.hasOwn(process.env, name) ? process.env[name] : null;
      console.log(JSON.stringify(out));
    });
  `;
  const ambient = [
    "WIN_CSC_LINK=C:\\ambient\\owner.pfx",
    "CSC_LINK=C:\\ambient\\other.pfx",
    "WIN_CSC_KEY_PASSWORD=ambient-secret",
    "CSC_KEY_PASSWORD=ambient-secret",
  ].join("\n");

  /** The four variables as electron-builder's own CLI start-up leaves them. */
  function afterLoadEnv(plan) {
    const dir = mkdtempSync(join(tmpdir(), "padmate-env-"));
    try {
      const file = join(dir, "electron-builder.env");
      writeFileSync(file, `${ambient}\n`);
      const out = execFileSync(
        process.execPath,
        [
          "-e",
          script,
          loadEnvModule,
          file,
          JSON.stringify(certificateVariables),
        ],
        {
          env: childEnvironment(process.env, plan),
          encoding: "utf8",
          windowsHide: true,
          timeout: 60_000,
        },
      );
      return JSON.parse(out.trim().split("\n").pop());
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  }

  it.each([
    ["none", {}],
    ["store", store],
    ["azure", azure],
  ])(
    "%s: all four are present and empty, so the file cannot fill them",
    (_, env) => {
      const after = afterLoadEnv(signingPlan(env, tools));
      for (const name of certificateVariables) expect(after[name]).toBe("");
    },
  );

  it("pfx: the WIN_CSC_ pair is the plan's, the CSC_ pair is empty", () => {
    const after = afterLoadEnv(signingPlan(pfxEnv, tools));
    expect(after).toEqual({
      WIN_CSC_LINK: pfx,
      WIN_CSC_KEY_PASSWORD: password,
      CSC_LINK: "",
      CSC_KEY_PASSWORD: "",
    });
  });

  it("leaves nothing for getCscLink and doGetCscPassword to choose in a none build", () => {
    const after = afterLoadEnv(signingPlan({}, tools));
    // chooseNotNull(WIN_CSC_LINK, CSC_LINK): "" is a value, so it ends the search.
    expect(after.WIN_CSC_LINK ?? after.CSC_LINK).toBe("");
    expect(after.WIN_CSC_KEY_PASSWORD ?? after.CSC_KEY_PASSWORD).toBe("");
  });
});

// The public code mirror refuses `task package:installer` (and so `task --dry`
// of it): these tests run in the development repository.
const describeUnlessMirror = describe.skipIf(isMirror());

// `task package:installer` ends with packageinstaller verify. A signing build
// must be checked for its signatures without anyone remembering a flag, or the
// task ends green on a package nobody looked at. `task --dry` prints the
// commands and runs none.
describeUnlessMirror("task package:installer verifies what it signed", () => {
  const repoRoot = resolve(app, "..");
  // A cold start of task and of the machine can take far longer than vitest's 5 s.
  const taskTimeout = 70_000;
  const hasTask =
    process.platform === "win32" &&
    spawnSync("task", ["--version"], { windowsHide: true }).status === 0;

  function verifyLine(env, extra = []) {
    const base = { ...process.env };
    for (const name of ["PADMATE_SIGN", "PADMATE_SIGN_SUBJECT"])
      delete base[name];
    const out = spawnSync(
      "task",
      ["--dry", "package:installer", "VERSION=v1.0.0", ...extra],
      {
        cwd: repoRoot,
        env: { ...base, ...env },
        encoding: "utf8",
        windowsHide: true,
        timeout: 60_000,
      },
    );
    expect(out.status, out.stderr).toBe(0);
    const lines = `${out.stdout}\n${out.stderr}`
      .split(/\r?\n/)
      .filter((line) => line.includes("tools/packageinstaller verify"));
    expect(lines).toHaveLength(1);
    return lines[0];
  }

  it
    .skipIf(!hasTask)
    .each([
      [{}],
      [{ PADMATE_SIGN: "" }],
      [{ PADMATE_SIGN: "  " }],
      [{ PADMATE_SIGN: "none", PADMATE_SIGN_SUBJECT: "CN=x" }],
    ])(
    "an unsigned build asks for no signature (%j)",
    (env) => {
      expect(verifyLine(env)).not.toContain("-signed");
    },
    taskTimeout,
  );

  it.skipIf(!hasTask).each(["store", "pfx", "azure", " pfx "])(
    "PADMATE_SIGN=%j demands the signatures of PADMATE_SIGN_SUBJECT",
    (mode) => {
      const line = verifyLine({
        PADMATE_SIGN: mode,
        PADMATE_SIGN_SUBJECT: "CN=Example, O=Org, C=JP",
      });
      // The shell of the task expands the variable, so any subject is one argument.
      expect(line).toContain('-signed "$PADMATE_SIGN_SUBJECT"');
    },
    taskTimeout,
  );

  it.skipIf(!hasTask)(
    "VERIFY_FLAGS adds flags after it",
    () => {
      const line = verifyLine(
        { PADMATE_SIGN: "store", PADMATE_SIGN_SUBJECT: "CN=x" },
        ["VERIFY_FLAGS=-update-info -timestamp=false"],
      );
      expect(line).toMatch(
        /-signed "\$PADMATE_SIGN_SUBJECT" -update-info -timestamp=false\s*$/,
      );
    },
    taskTimeout,
  );

  it("the Taskfile text says so, for machines without task", () => {
    const text = readFileSync(resolve(repoRoot, "Taskfile.yml"), "utf8");
    expect(text).toContain('-signed "$PADMATE_SIGN_SUBJECT"');
    expect(text).toMatch(
      /packageinstaller verify[^\n]*\{\{\.SIGNED_FLAG\}\}[^\n]*\{\{\.VERIFY_FLAGS\}\}/,
    );
  });
});

describe("the files stay free of certificates", () => {
  it("electron-builder.yml and package.json name no certificate", () => {
    const yml = readFileSync(resolve(app, "electron-builder.yml"), "utf8");
    for (const key of [
      "certificateFile",
      "certificatePassword",
      "certificateSha1",
      "certificateSubjectName",
      "azureSignOptions",
      "signExts",
      "verifyUpdateCodeSignature",
    ]) {
      expect(yml).not.toContain(key);
    }
  });
});
