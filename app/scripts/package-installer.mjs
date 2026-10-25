// Builds the installer with electron-builder (PKG-03, PKG-06, PKG-09).
//
//   node scripts/package-installer.mjs --version v0.14.0 [--output DIR]
//
// `task package:installer` runs this after it has built the daemon and CLI and
// staged them (daemon/tools/packageinstaller stage) and after `npm run build`.
// The version is the release tag: the app and the installer get it without the
// "v" (extraMetadata.version), so no file is edited for a release.
//
// Code signing (PKG-09, ADR-0073) is chosen by PADMATE_SIGN, never by a file in
// the repository: none (the default), store, pfx or azure. signingPlan turns the
// environment into electron-builder flags and an environment patch; no
// certificate, password or thumbprint is written anywhere, and none is printed.
import { spawn } from "node:child_process";
import { existsSync, readdirSync } from "node:fs";
import { rm } from "node:fs/promises";
import { dirname, relative, resolve, win32 } from "node:path";
import { fileURLToPath } from "node:url";
import { assertMainBundle } from "./main-bundle-guard.mjs";
import { refusePackaging } from "./public-mirror.mjs";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const repo = resolve(app, "..");
const dist = resolve(repo, "dist");
const defaultOutput = resolve(dist, "installer");

// The same rule as daemon/internal/installerpkg.ParseTag, which the task has
// already applied: v, MAJOR.MINOR.PATCH and an optional -prerelease.
const tag =
  /^v(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/;

export function appVersion(label) {
  if (!tag.test(label ?? "") || label.length > 64)
    throw new Error(
      `version ${JSON.stringify(label)} is not a release tag: use v and a semantic version, such as v0.14.0 or v0.14.0-rc.1`,
    );
  return label.slice(1);
}

/** The modes of PADMATE_SIGN. */
export const signModes = ["none", "store", "pfx", "azure"];

// The variables electron-builder reads a certificate from
// (app-builder-lib platformPackager.js). CSC_IDENTITY_AUTO_DISCOVERY=false only
// concerns macOS, so a certificate that happens to be in the environment would
// sign a build that was meant to be unsigned.
//
// A plan sets the ones it does not use to the empty string and does not delete
// them: the CLI of electron-builder reads app/electron-builder.env (the working
// directory) and sets every variable of that file that is not in the
// environment, so a deleted variable comes back from a file the owner keeps for
// other builds. The empty string is a value (chooseNotNull in
// platformPackager.js), and getCscLink and getCscPassword treat it as "none".
const certificateVariables = [
  "WIN_CSC_LINK",
  "CSC_LINK",
  "WIN_CSC_KEY_PASSWORD",
  "CSC_KEY_PASSWORD",
];

/**
 * The newest signtool.exe of the Windows SDK, or undefined. Using it keeps
 * electron-builder from downloading its own signing tools (winCodeSign, a 7z
 * archive with symbolic links that does not extract on Windows without
 * developer mode).
 *
 * @param {{ root?: string, readdir?: (dir: string) => string[], exists?: (file: string) => boolean }} [fsTools]
 * @returns {string | undefined}
 */
export function findSignTool({
  root = "C:\\Program Files (x86)\\Windows Kits\\10\\bin",
  readdir = readdirSync,
  exists = existsSync,
} = {}) {
  let versions;
  try {
    versions = readdir(root);
  } catch {
    return undefined;
  }
  // 10.0.26100.0 sorts after 10.0.9600.0 only as numbers.
  const key = (v) => v.split(".").map((n) => Number.parseInt(n, 10) || 0);
  const newestFirst = versions
    .filter((v) => /^\d+(\.\d+){3}$/.test(v))
    .sort((a, b) => {
      const [x, y] = [key(a), key(b)];
      for (let i = 0; i < 4; i++) if (x[i] !== y[i]) return y[i] - x[i];
      return 0;
    });
  for (const version of newestFirst) {
    const file = win32.join(root, version, "x64", "signtool.exe");
    if (exists(file)) return file;
  }
  return undefined;
}

/** A path that starts at a drive letter: not relative, not rooted without one, not a share. */
const driveAbsolute = (path) => /^[A-Za-z]:[\\/]/.test(path);

function nonEmpty(env, ...names) {
  for (const name of names) {
    const value = env[name];
    if (typeof value === "string" && value.trim() !== "") return value;
  }
  return undefined;
}

function required(env, name, why) {
  const value = nonEmpty(env, name);
  if (value === undefined) throw new Error(`${name} is required ${why}`);
  return value;
}

function checkSubject(subject) {
  const control = (c) => c.charCodeAt(0) < 32 || c.charCodeAt(0) === 127;
  if (subject.length > 256 || [...subject].some(control))
    throw new Error(
      "PADMATE_SIGN_SUBJECT must be at most 256 characters without control characters",
    );
  return subject;
}

function checkTimestampServer(url) {
  let parsed;
  try {
    parsed = new URL(url);
  } catch {
    parsed = undefined;
  }
  if (!parsed || !/^https?:$/.test(parsed.protocol) || parsed.username)
    throw new Error(
      "PADMATE_SIGN_TIMESTAMP must be an http or https URL of an RFC 3161 timestamp server, without credentials",
    );
  return url;
}

/**
 * Where the pfx mode reads the certificate from: a local file only, never a
 * URL (electron-builder would download it) and never a network share.
 */
function pfxFile(env) {
  const link = nonEmpty(env, "WIN_CSC_LINK", "CSC_LINK");
  if (link === undefined)
    throw new Error(
      "PADMATE_SIGN=pfx needs WIN_CSC_LINK (or CSC_LINK): the absolute path of a .pfx or .p12 file",
    );
  if (
    /^[a-z][a-z0-9+.-]*:\/\//i.test(link) ||
    /^data:/i.test(link) ||
    link.startsWith("\\\\") ||
    !driveAbsolute(link) ||
    !/\.(pfx|p12)$/i.test(link)
  )
    throw new Error(
      "WIN_CSC_LINK must be the absolute path of a local .pfx or .p12 file, not a URL, a network share or a relative path",
    );
  const password = nonEmpty(env, "WIN_CSC_KEY_PASSWORD", "CSC_KEY_PASSWORD");
  if (password === undefined)
    throw new Error(
      "PADMATE_SIGN=pfx needs WIN_CSC_KEY_PASSWORD (or CSC_KEY_PASSWORD): electron-builder would use an empty password without saying so",
    );
  return { link, password };
}

/**
 * Turns the environment into what electron-builder needs to sign, or to not
 * sign (ADR-0073). Pure but for the one search for signtool.exe, which a test
 * replaces.
 *
 * - PADMATE_SIGN: none (default), store, pfx or azure.
 * - none: no certificate. The variables electron-builder reads one from are
 *   set to the empty string in the child's environment (not removed: see
 *   certificateVariables).
 * - store: PADMATE_SIGN_SHA1 is the thumbprint (40 hexadecimal digits) of a
 *   certificate in the Windows certificate store (a hardware token or an HSM
 *   shows up there); no password is needed or passed.
 * - pfx: WIN_CSC_LINK is the absolute path of a local .pfx or .p12 file and
 *   WIN_CSC_KEY_PASSWORD its password. For a test certificate and the rare
 *   exportable one; signtool gets the password on its command line.
 * - azure: PADMATE_SIGN_AZURE_ENDPOINT, _ACCOUNT and _PROFILE name an Artifact
 *   Signing account (the sign-in is Azure's own variables). electron-builder
 *   installs PowerShell modules on every build, so this is for CI.
 * - PADMATE_SIGN_SUBJECT (every signing mode) is the publisher: it becomes
 *   publisherName, which electron-updater compares with the signer of a
 *   download. Prefer the full distinguished name.
 * - PADMATE_SIGN_TIMESTAMP: an RFC 3161 server other than the default of
 *   electron-builder.yml (store and pfx).
 * - PADMATE_SIGN_TEST=1: offline, no timestamp, for the local pipeline test.
 *
 * Every signing mode makes electron-builder fail when it cannot sign
 * (forceCodeSigning), so there is no half-signed package.
 *
 * @param {Record<string, string | undefined>} env
 * @param {{ findSignTool?: () => string | undefined }} [tools]
 * @returns {{ mode: string, args: string[], envPatch: Record<string, string | undefined>, subject?: string }}
 *   envPatch maps a variable to its value for the child ("" for a variable that
 *   must stay empty), or to undefined to remove it.
 */
export function signingPlan(env, tools = {}) {
  const raw = (env.PADMATE_SIGN ?? "").trim();
  const mode = raw === "" ? "none" : raw;
  if (!signModes.includes(mode))
    throw new Error(
      `PADMATE_SIGN=${JSON.stringify(mode)} is not one of ${signModes.join(", ")}`,
    );
  /** @type {Record<string, string | undefined>} */
  const envPatch = {};
  for (const name of certificateVariables) envPatch[name] = "";
  if (mode === "none") return { mode, args: [], envPatch };

  const subject = checkSubject(
    required(
      env,
      "PADMATE_SIGN_SUBJECT",
      `to sign: it is the publisher the update check compares the signer with (publisherName), for example "CN=Name, O=Organization, C=JP"`,
    ),
  );
  const args = ["--config.win.forceCodeSigning=true"];
  // electron-builder echoes the signtool command line, password included, when
  // its debug output is on. `debug` turns the "electron-builder" namespace on
  // for DEBUG=*, electron-*, electron* and more, so no pattern is tried to be
  // told apart: a signing build has no debug output. "" and not a removed
  // variable, like the certificate variables (electron-builder.env cannot bring
  // it back).
  envPatch.DEBUG = "";
  if (env.PADMATE_SIGN_TEST === "1") envPatch.ELECTRON_BUILDER_OFFLINE = "true";

  if (mode === "azure") {
    args.push(
      `--config.win.azureSignOptions.endpoint=${required(env, "PADMATE_SIGN_AZURE_ENDPOINT", "for PADMATE_SIGN=azure")}`,
      `--config.win.azureSignOptions.codeSigningAccountName=${required(env, "PADMATE_SIGN_AZURE_ACCOUNT", "for PADMATE_SIGN=azure")}`,
      `--config.win.azureSignOptions.certificateProfileName=${required(env, "PADMATE_SIGN_AZURE_PROFILE", "for PADMATE_SIGN=azure")}`,
      `--config.win.azureSignOptions.publisherName=${subject}`,
    );
    return { mode, args, envPatch, subject };
  }

  args.push(`--config.win.signtoolOptions.publisherName=${subject}`);
  if (mode === "store") {
    const sha1 = required(
      env,
      "PADMATE_SIGN_SHA1",
      "for PADMATE_SIGN=store: the thumbprint of the certificate",
    ).trim();
    if (!/^[0-9a-f]{40}$/i.test(sha1))
      throw new Error(
        "PADMATE_SIGN_SHA1 must be the 40 hexadecimal digits of a SHA-1 thumbprint",
      );
    args.push(
      `--config.win.signtoolOptions.certificateSha1=${sha1.toUpperCase()}`,
    );
  } else {
    const { link, password } = pfxFile(env);
    envPatch.WIN_CSC_LINK = link;
    envPatch.WIN_CSC_KEY_PASSWORD = password;
  }
  const timestamp = nonEmpty(env, "PADMATE_SIGN_TIMESTAMP");
  if (timestamp !== undefined)
    args.push(
      `--config.win.signtoolOptions.rfc3161TimeStampServer=${checkTimestampServer(timestamp.trim())}`,
    );
  const own = nonEmpty(env, "SIGNTOOL_PATH");
  if (own === undefined) {
    const found = (tools.findSignTool ?? findSignTool)();
    if (found === undefined)
      throw new Error(
        "signtool.exe was not found: install the Windows SDK (Signing Tools) or set SIGNTOOL_PATH to its absolute path",
      );
    envPatch.SIGNTOOL_PATH = found;
  } else if (
    !driveAbsolute(own) ||
    win32.basename(own).toLowerCase() !== "signtool.exe"
  ) {
    throw new Error("SIGNTOOL_PATH must be the absolute path of signtool.exe");
  }
  return { mode, args, envPatch, subject };
}

/** The environment of electron-builder: the process's, with the plan's patch. */
export function childEnvironment(base, plan) {
  const env = {
    ...base,
    // No certificate to look for on macOS, and no npm update check.
    CSC_IDENTITY_AUTO_DISCOVERY: "false",
    NO_UPDATE_NOTIFIER: "1",
  };
  for (const [name, value] of Object.entries(plan.envPatch)) {
    // Windows does not tell debug from DEBUG: no other spelling may stay next
    // to the one that is set.
    for (const key of Object.keys(env))
      if (key.toUpperCase() === name.toUpperCase()) delete env[key];
    if (value !== undefined) env[name] = value;
  }
  return env;
}

// electron-builder takes a password out of the command line of a failed step
// (builder-util removePassword), but what it puts there is the SHA-256 of the
// password without a salt, in the message of the error and whatever DEBUG is.
// For a password a person chose that digest can be guessed against offline, and
// the output of this script is what ends up in a build log. A signing build
// therefore reads the output of electron-builder through redact.
const passwordHash = /[0-9a-f]{64} \(sha256 hash\)/gi;

/**
 * Takes the digests electron-builder prints in place of a password, and the
 * secrets themselves (when four characters or longer), out of a text.
 *
 * @param {string} text
 * @param {(string | undefined)[]} [secrets]
 */
export function redact(text, secrets = []) {
  let out = text.replace(passwordHash, "(password hash removed)");
  for (const secret of secrets)
    if (typeof secret === "string" && secret.length >= 4)
      out = out.split(secret).join("***");
  return out;
}

/**
 * Redacts a stream line by line: a digest never contains a line end, so no
 * line is written before it is whole. `end` writes what is left.
 *
 * @param {(text: string) => void} write
 * @param {(string | undefined)[]} [secrets]
 */
export function lineRedactor(write, secrets = []) {
  let pending = "";
  return {
    push(chunk) {
      pending += chunk;
      const cut = Math.max(
        pending.lastIndexOf("\n"),
        pending.lastIndexOf("\r"),
      );
      if (cut < 0) return;
      write(redact(pending.slice(0, cut + 1), secrets));
      pending = pending.slice(cut + 1);
    },
    end() {
      if (pending !== "") write(redact(pending, secrets));
      pending = "";
    },
  };
}

/**
 * The output folder of a test build. It is deleted before the build, so it must
 * be a folder below .cache or dist of this repository, never one of them or
 * anything else.
 */
export function outputFolder(requested) {
  if (requested === undefined) return defaultOutput;
  const out = resolve(requested);
  for (const root of [resolve(repo, ".cache"), dist]) {
    const rel = relative(root, out);
    if (rel !== "" && !rel.startsWith("..") && !win32.isAbsolute(rel))
      return out;
  }
  throw new Error(
    `--output ${JSON.stringify(requested)} must be a folder inside .cache or dist of the repository: it is emptied before the build`,
  );
}

export function parse(args) {
  const usage =
    "usage: node scripts/package-installer.mjs --version vX.Y.Z [--output DIR]";
  if (args.length !== 2 && args.length !== 4) throw new Error(usage);
  if (args[0] !== "--version") throw new Error(usage);
  const parsed = { label: args[1], output: undefined };
  if (args.length === 4) {
    if (args[2] !== "--output") throw new Error(usage);
    parsed.output = args[3];
  }
  return parsed;
}

/**
 * Runs electron-builder (or, in a test, another node script) and resolves when
 * it exits with 0.
 *
 * A build that signs ({redacting: true}) reads the output through redact, with
 * the password of env as one of the secrets; one that signs nothing has no
 * password to leak and keeps the terminal as it is.
 *
 * @param {string[]} args arguments of node
 * @param {Record<string, string | undefined>} env
 * @param {{ redacting: boolean, cwd?: string, stdout?: { write(text: string): unknown }, stderr?: { write(text: string): unknown } }} options
 */
export function runElectronBuilder(
  args,
  env,
  { redacting, cwd = app, stdout = process.stdout, stderr = process.stderr },
) {
  return new Promise((done, reject) => {
    const child = spawn(process.execPath, args, {
      cwd,
      windowsHide: true,
      shell: false,
      stdio: redacting ? ["inherit", "pipe", "pipe"] : "inherit",
      env,
    });
    const redactors = [];
    if (redacting) {
      const secrets = [env.WIN_CSC_KEY_PASSWORD, env.CSC_KEY_PASSWORD];
      for (const [stream, target] of [
        [child.stdout, stdout],
        [child.stderr, stderr],
      ]) {
        const redactor = lineRedactor((text) => target.write(text), secrets);
        stream.setEncoding("utf8");
        stream.on("data", (chunk) => redactor.push(chunk));
        redactors.push(redactor);
      }
    }
    child.on("error", reject);
    // close, not exit: all the output has been read by then.
    child.on("close", (code) => {
      for (const redactor of redactors) redactor.end();
      if (code === 0) done();
      else reject(new Error(`electron-builder failed (${code})`));
    });
  });
}

async function main() {
  // The public code mirror has no documentation to pack.
  refusePackaging();
  const { label, output: requested } = parse(process.argv.slice(2));
  const version = appVersion(label);
  const output = outputFolder(requested);
  const plan = signingPlan(process.env);
  for (const needed of [
    "installer-stage/.padmate-installer-stage",
    "installer-stage/icon.ico",
    "installer-stage/resources/bin/padmated.exe",
    "installer-stage/resources/bin/padmatectl.exe",
    "installer-stage/resources/bin/SDL3.dll",
  ])
    if (!existsSync(resolve(dist, needed)))
      throw new Error(
        `${resolve(dist, needed)} is missing: run task package:installer, which stages it first`,
      );
  if (!existsSync(resolve(app, "out/main/index.js")))
    throw new Error("out/main/index.js is missing: run npm run build first");
  // What is printed names the mode and the publisher; the thumbprint, the
  // certificate file and every password stay out of the log.
  console.log(
    plan.mode === "none"
      ? "package-installer: not signing (PADMATE_SIGN=none)"
      : `package-installer: signing with ${plan.mode}, publisher ${JSON.stringify(plan.subject)}`,
  );
  // The installed application has no node_modules: the main process must be
  // one file that requires nothing but Node and electron (PKG-10).
  await assertMainBundle(resolve(app, "out/main/index.js"));
  // electron-builder keeps what an earlier build left in its output folder.
  await rm(output, { recursive: true, force: true });
  const cli = resolve(app, "node_modules/electron-builder/out/cli/cli.js");
  const args = [
    cli,
    "--win",
    "nsis",
    "--x64",
    "--publish",
    "never",
    "--config",
    "electron-builder.yml",
    `--config.extraMetadata.version=${version}`,
    ...(requested === undefined
      ? []
      : [`--config.directories.output=${output}`]),
    ...plan.args,
  ];
  await runElectronBuilder(args, childEnvironment(process.env, plan), {
    redacting: plan.mode !== "none",
  });
}

if (resolve(process.argv[1] ?? "") === fileURLToPath(import.meta.url)) {
  main().catch((error) => {
    console.error(`package-installer: ${error.message}`);
    process.exit(1);
  });
}
