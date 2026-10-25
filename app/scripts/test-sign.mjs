// task test:sign: the signing pipeline end to end on this machine with a
// throwaway certificate (PKG-09, ADR-0073). Opt-in, Windows only, not part of
// task ci. It builds into .cache/signtest and installs, runs and trusts nothing.
//
//   node scripts/test-sign.mjs
//
// What it does:
//   1. Makes a self-signed code-signing certificate in memory (PowerShell,
//      CertificateRequest.CreateSelfSigned) and exports it to
//      .cache/signtest/signtest.pfx under a random password that lives in the
//      child processes' environment only. It never uses New-SelfSignedCertificate
//      or `electron-builder create-self-signed-cert`, which import into a
//      certificate store.
//   2. Builds v0.0.0-signtest.1 with PADMATE_SIGN=pfx into .cache/signtest/out
//      (daemon and CLI in .cache/signtest/bin), offline (PADMATE_SIGN_TEST=1),
//      and checks the result: the task's own `packageinstaller verify` (which adds
//      -signed "$PADMATE_SIGN_SUBJECT" to every signing build) and then
//      `packageinstaller verify -signed "padmate signtest" -allow-untrusted-root
//      -timestamp=false` again, explicitly.
//   3. Checks that the check bites: another publisher, and an untrusted root
//      that is not allowed, must fail.
//   4. Deletes the PFX, also when something failed.
//
// Before and after, it lists Cert:\CurrentUser\{My,Root,TrustedPublisher,
// TrustedPeople,CA}, Cert:\LocalMachine\{My,Root,TrustedPublisher,TrustedPeople}
// and the key containers under %APPDATA%\Microsoft\Crypto\{RSA,Keys}. It fails
// when a certificate was added or removed or a key file was removed or changed.
// signtool imports the PFX to sign with it and leaves its private key behind as
// a CNG key file in Crypto\Keys, one per run of signtool. At the end the run
// deletes those, and only those: new files, directly in that folder, with a GUID
// in braces for the container name and the public key of the throwaway
// certificate in them (see "The key files signtool leaves behind" below); it
// prints what it deleted and what it left alone, and looks at the folder again.
// The root of the throwaway certificate is never trusted (that would be a
// change of security settings), so `signtool verify /pa` is not run: its answer
// would be "untrusted root".
import { spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import { existsSync, rmSync } from "node:fs";
import { copyFile, lstat, mkdir, readFile, rm } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { findSignTool } from "./package-installer.mjs";
import { refusePackaging } from "./public-mirror.mjs";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const repo = resolve(app, "..");

export const version = "v0.0.0-signtest.1";
export const subject = "padmate signtest";

// Errors that a moment later may not be there: a scanner (Defender) or an
// indexer still holds the file that signtool has just closed.
const transient = new Set(["EBUSY", "EPERM", "EACCES", "ENOTEMPTY"]);

/**
 * Removes a file (or, with options, a folder), trying again when the system
 * says it is in use. It returns the last error instead of throwing it: the
 * caller is in a `finally`, and the answer of the run must not be replaced by
 * the failure to clean up.
 *
 * @param {string} path
 * @param {{ rm?: typeof rm, sleep?: (ms: number) => Promise<void>, attempts?: number, delayMs?: number, options?: object }} [how]
 * @returns {Promise<Error | undefined>}
 */
export async function removeWithRetries(
  path,
  {
    rm: remove = rm,
    sleep = (ms) => new Promise((done) => setTimeout(done, ms)),
    attempts = 8,
    delayMs = 250,
    options = {},
  } = {},
) {
  let last;
  for (let attempt = 1; attempt <= attempts; attempt++) {
    try {
      await remove(path, { force: true, ...options });
      return undefined;
    } catch (error) {
      last = error;
      if (!transient.has(error?.code) || attempt === attempts) break;
      await sleep(delayMs * attempt);
    }
  }
  return last;
}

const signalExitCodes = {
  SIGINT: 130,
  SIGTERM: 143,
  SIGHUP: 129,
  SIGBREAK: 149,
};

/**
 * Makes sure the file is deleted when the process ends, however: a signal (Ctrl+C
 * in the console) is turned into an exit, and the exit event removes the file
 * synchronously. A kill that gives the process no chance (taskkill /f) cannot be
 * helped. Returns the function that lets go of the process again.
 *
 * @param {string} path
 * @param {{ proc?: NodeJS.Process, rmSync?: typeof rmSync }} [how]
 */
export function guardFile(
  path,
  { proc = process, rmSync: removeSync = rmSync } = {},
) {
  const remove = () => {
    try {
      removeSync(path, { force: true });
    } catch {
      // The run reports a file that is still there; nothing may hide why the
      // process is ending.
    }
  };
  const signals = Object.entries(signalExitCodes).map(([name, code]) => [
    name,
    () => proc.exit(code),
  ]);
  proc.on("exit", remove);
  for (const [name, handler] of signals) proc.on(name, handler);
  return () => {
    proc.off("exit", remove);
    for (const [name, handler] of signals) proc.off(name, handler);
  };
}

/** Where everything of a run goes; wiped at the start of the next run. */
export function folders(root = repo) {
  const work = join(root, ".cache", "signtest");
  return {
    work,
    pfx: join(work, "signtest.pfx"),
    bin: join(work, "bin"),
    out: join(work, "out"),
  };
}

export function newPassword() {
  return `signtest-${randomBytes(24).toString("hex")}`;
}

// Reads the stores and key folders; writes JSON, as Base64 of its UTF-8 bytes.
// No parameter, no secret. The Base64 is what keeps a path whole when the user
// name has letters outside the console's code page (a Chinese or a Japanese
// name): powershell.exe writes to a pipe in the OEM code page, and read as
// UTF-8 the folder of the profile comes out as replacement characters, so no
// path from here would be the path that %APPDATA% names. The first real run
// of the key file cleanup found that.
export const snapshotScript = String.raw`
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$result = [ordered]@{ stores = [ordered]@{}; keys = [ordered]@{} }
foreach ($name in @('CurrentUser\My', 'CurrentUser\Root', 'CurrentUser\TrustedPublisher', 'CurrentUser\TrustedPeople', 'CurrentUser\CA',
                    'LocalMachine\My', 'LocalMachine\Root', 'LocalMachine\TrustedPublisher', 'LocalMachine\TrustedPeople')) {
  $result.stores[$name] = @(Get-ChildItem -LiteralPath ('Cert:\' + $name) -ErrorAction SilentlyContinue | ForEach-Object { $_.Thumbprint } | Sort-Object)
}
foreach ($rel in @('Microsoft\Crypto\RSA', 'Microsoft\Crypto\Keys')) {
  $dir = Join-Path $env:APPDATA $rel
  $files = @()
  if (Test-Path -LiteralPath $dir) {
    $files = @(Get-ChildItem -LiteralPath $dir -Recurse -File -ErrorAction SilentlyContinue | ForEach-Object { $_.FullName + '|' + $_.Length + '|' + $_.LastWriteTimeUtc.Ticks } | Sort-Object)
  }
  $result.keys[$rel] = $files
}
[Convert]::ToBase64String([System.Text.Encoding]::UTF8.GetBytes(($result | ConvertTo-Json -Depth 4 -Compress)))
`;

// Makes the certificate in memory and exports it. The password, the subject and
// the file come from the environment.
export const certificateScript = `
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$rsa = [System.Security.Cryptography.RSA]::Create(2048)
$dn = New-Object System.Security.Cryptography.X509Certificates.X500DistinguishedName('CN=' + $env:PADMATE_SIGNTEST_SUBJECT)
$req = New-Object System.Security.Cryptography.X509Certificates.CertificateRequest($dn, $rsa, [System.Security.Cryptography.HashAlgorithmName]::SHA256, [System.Security.Cryptography.RSASignaturePadding]::Pkcs1)
$oids = New-Object System.Security.Cryptography.OidCollection
[void]$oids.Add((New-Object System.Security.Cryptography.Oid('1.3.6.1.5.5.7.3.3')))
$req.CertificateExtensions.Add((New-Object System.Security.Cryptography.X509Certificates.X509EnhancedKeyUsageExtension($oids, $false)))
$req.CertificateExtensions.Add((New-Object System.Security.Cryptography.X509Certificates.X509KeyUsageExtension([System.Security.Cryptography.X509Certificates.X509KeyUsageFlags]::DigitalSignature, $true)))
$req.CertificateExtensions.Add((New-Object System.Security.Cryptography.X509Certificates.X509BasicConstraintsExtension($false, $false, 0, $true)))
$cert = $req.CreateSelfSigned([DateTimeOffset]::UtcNow.AddDays(-1), [DateTimeOffset]::UtcNow.AddDays(7))
[IO.File]::WriteAllBytes($env:PADMATE_SIGNTEST_PFX, $cert.Export([System.Security.Cryptography.X509Certificates.X509ContentType]::Pfx, $env:PADMATE_SIGNTEST_PASSWORD))
# The public modulus is how the key files signtool leaves behind are recognised
# as this certificate's. It is public; the private key is not printed.
Write-Output ('modulus:' + [BitConverter]::ToString($rsa.ExportParameters($false).Modulus).Replace('-', '').ToLowerInvariant())
$cert.Dispose()
$rsa.Dispose()
`;

/**
 * Compares two snapshots. A certificate that appeared or disappeared, or a key
 * file that disappeared or changed, is a problem; a new key file is an orphan
 * that signtool left, reported but not a problem.
 *
 * @returns {{ problems: string[], orphans: string[] }}
 */
export function compareSnapshots(before, after) {
  const problems = [];
  const orphans = [];
  for (const store of new Set([
    ...Object.keys(before.stores),
    ...Object.keys(after.stores),
  ])) {
    const was = new Set(list(before.stores[store]));
    const is = new Set(list(after.stores[store]));
    for (const t of is)
      if (!was.has(t)) problems.push(`Cert:\\${store} gained ${t}`);
    for (const t of was)
      if (!is.has(t)) problems.push(`Cert:\\${store} lost ${t}`);
  }
  for (const folder of new Set([
    ...Object.keys(before.keys),
    ...Object.keys(after.keys),
  ])) {
    const was = new Map(list(before.keys[folder]).map(splitEntry));
    const is = new Map(list(after.keys[folder]).map(splitEntry));
    for (const [file, state] of is) {
      if (!was.has(file)) orphans.push(file);
      else if (was.get(file) !== state)
        problems.push(`key file ${file} changed`);
    }
    for (const file of was.keys())
      if (!is.has(file)) problems.push(`key file ${file} was removed`);
  }
  return { problems, orphans };
}

// PowerShell writes a list of one as the value itself and an empty one as null.
const list = (x) => (Array.isArray(x) ? x : x == null ? [] : [x]);

function splitEntry(entry) {
  const i = entry.indexOf("|");
  return [entry.slice(0, i), entry.slice(i + 1)];
}

/** The paths of the key files a snapshot lists, in every key folder. */
export function keyFilePaths(snapshot) {
  return Object.values(snapshot.keys).flatMap((entries) =>
    list(entries).map((entry) => splitEntry(entry)[0]),
  );
}

// ---- The key files signtool leaves behind ----------------------------------
//
// signtool imports the PFX to sign with it and leaves the private key of the
// throwaway certificate as a CNG key file, one per signtool run, in
// %APPDATA%\Microsoft\Crypto\Keys. They are deleted at the end of the run, and
// the selection is as narrow as it can be made, because that folder holds the
// keys of the person and of other programs. A file is deleted only when
//   - it was not in the folder before the run (the "before" snapshot),
//   - it is directly in that folder (not in a sub folder, not in Crypto\RSA),
//   - it is a regular file, not a link, and small (a key file is 1 to 3 KB),
//   - the container name inside it, in UTF-16, is a GUID in braces, which is
//     the name the key import gives, and
//   - it holds the public modulus of the throwaway certificate (the public key
//     blob is stored in plain), so that it is this certificate's key and not
//     another program's key that happens to have a GUID for a name;
// and it is read again just before it is deleted. When more files qualify than
// a run can leave, none is deleted. Layout of a CNG key file, checked on this
// machine against a key made with CngKey and deleted again: DWORD version,
// DWORD 0, the length of the container name in bytes at offset 8, the name
// in UTF-16 at offset 0x2c.

const keyFileNameOffset = 0x2c;
const guidInBracesPattern =
  /^\{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}$/;

/** Key files are 1 to 3 KB; anything bigger is not one this run made. */
export const largestKeyFile = 64 * 1024;

/**
 * signtool runs 6 times in a build (padmatectl, padmated, padmate.exe twice,
 * the uninstaller, the installer). More than 24 matching files is not what this
 * run did, so nothing is deleted.
 */
export const mostKeyFilesARun = 24;

/** Whether the name is a GUID in braces: {XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX}. */
export function isGuidInBraces(name) {
  return guidInBracesPattern.test(name);
}

/**
 * The container name a CNG key file states, or undefined when the bytes do not
 * have the layout of one.
 *
 * @param {Buffer} data
 */
export function readContainerName(data) {
  if (data.length < keyFileNameOffset) return undefined;
  const length = data.readUInt32LE(8);
  if (length < 2 || length > 512 || length % 2 !== 0) return undefined;
  if (keyFileNameOffset + length > data.length) return undefined;
  return data
    .subarray(keyFileNameOffset, keyFileNameOffset + length)
    .toString("utf16le");
}

/**
 * The public modulus the certificate script printed ("modulus:" and hex), or
 * undefined. 128 bytes (a 1024-bit key) is the least that identifies a key.
 *
 * @param {string} text
 */
export function parseModulus(text) {
  const match = /^modulus:([0-9a-fA-F]+)\s*$/m.exec(text);
  if (!match || match[1].length % 2 !== 0 || match[1].length < 256)
    return undefined;
  return Buffer.from(match[1], "hex");
}

// Windows compares paths without regard to case.
const sameName = (path) => resolve(path).toLowerCase();

// What is wrong with a file that is to be taken for a key file of this run, or
// undefined when nothing is. Everything the file's content decides is here, so
// that the look just before the deletion is the look that chose it.
function whatIsWrongWith(data, modulus) {
  if (data.length > largestKeyFile)
    return `bigger than ${largestKeyFile} bytes, not a key file of this run`;
  const container = readContainerName(data);
  if (container === undefined)
    return "does not have the layout of a CNG key file";
  if (!isGuidInBraces(container))
    return `the container name ${JSON.stringify(container)} is not a GUID in braces`;
  if (!modulus || modulus.length < 128)
    return "the public key of the throwaway certificate is not known";
  if (!data.includes(modulus))
    return "does not hold the public key of the throwaway certificate";
  return undefined;
}

/**
 * Chooses the key files to delete among the new files of a run; reads them,
 * deletes nothing.
 *
 * @param {{ orphans: string[], before: string[], keysDir: string, modulus?: Buffer, read?: typeof readFile, status?: typeof lstat }} how
 * @returns {Promise<{ selected: { path: string, container: string }[], kept: { path: string, why: string }[] }>}
 */
export async function selectThrowawayKeys({
  orphans,
  before,
  keysDir,
  modulus,
  read = readFile,
  status = lstat,
}) {
  const known = new Set(before.map(sameName));
  const folder = sameName(keysDir);
  const selected = [];
  const kept = [];
  for (const path of orphans) {
    const keep = (why) => kept.push({ path, why });
    if (known.has(sameName(path))) {
      keep("was there before the run");
      continue;
    }
    if (dirname(sameName(path)) !== folder) {
      keep(`not directly in ${keysDir}`);
      continue;
    }
    let data;
    try {
      if (!(await status(path)).isFile()) {
        keep("not a regular file");
        continue;
      }
      data = await read(path);
    } catch (error) {
      keep(`cannot be read: ${error.message}`);
      continue;
    }
    const wrong = whatIsWrongWith(data, modulus);
    if (wrong) keep(wrong);
    else selected.push({ path, container: readContainerName(data) });
  }
  if (selected.length > mostKeyFilesARun) {
    for (const { path } of selected.splice(0)) {
      kept.push({
        path,
        why: `more than ${mostKeyFilesARun} files match, which is more than a run leaves: none is deleted`,
      });
    }
  }
  return { selected, kept };
}

/**
 * Deletes the key files of this run (selectThrowawayKeys), looking at each
 * once more just before. A file that cannot be deleted is reported, not
 * thrown: the answer of the run does not depend on it.
 *
 * @param {{ orphans: string[], before: string[], keysDir: string, modulus?: Buffer, read?: typeof readFile, status?: typeof lstat, remove?: (path: string) => Promise<Error | undefined> }} how
 * @returns {Promise<{ deleted: { path: string, container: string }[], kept: { path: string, why: string }[], failed: { path: string, error: Error }[] }>}
 */
export async function cleanUpKeys({
  remove = removeWithRetries,
  read = readFile,
  status = lstat,
  ...how
}) {
  const { selected, kept } = await selectThrowawayKeys({
    ...how,
    read,
    status,
  });
  const deleted = [];
  const failed = [];
  for (const { path, container } of selected) {
    let wrong;
    try {
      wrong = (await status(path)).isFile()
        ? whatIsWrongWith(await read(path), how.modulus)
        : "not a regular file";
    } catch (error) {
      wrong = `cannot be read: ${error.message}`;
    }
    if (wrong) {
      kept.push({ path, why: `changed after it was chosen: ${wrong}` });
      continue;
    }
    const error = await remove(path);
    if (error) failed.push({ path, error });
    else deleted.push({ path, container });
  }
  return { deleted, kept, failed };
}

/** The lines the run prints about what cleanUpKeys did. */
export function describeCleanup({ deleted, kept, failed }) {
  const lines = [];
  if (deleted.length > 0) {
    lines.push(
      `test:sign: deleted ${deleted.length} key file(s) signtool left, the private key of the throwaway certificate:`,
      ...deleted.map((d) => `  ${d.path} (container ${d.container})`),
    );
  }
  if (failed.length > 0) {
    lines.push(
      `test:sign: could not delete ${failed.length} key file(s) of the throwaway certificate; they are harmless and can be deleted by hand:`,
      ...failed.map((f) => `  ${f.path}: ${f.error.message}`),
    );
  }
  if (kept.length > 0) {
    lines.push(
      `test:sign: ${kept.length} new key file(s) were left alone, they are not recognised as the key of the throwaway certificate:`,
      ...kept.map((k) => `  ${k.path}: ${k.why}`),
    );
  }
  return lines;
}

/** The arguments of `task`, with every path absolute. No secret is among them. */
export function taskArguments({ bin, out }) {
  // Forward slashes: Task runs the command through a shell that reads a
  // backslash as an escape.
  const slashes = (path) => path.split(String.fromCharCode(92)).join("/");
  return [
    "package:installer",
    `VERSION=${version}`,
    `INSTALLER_BIN=${slashes(bin)}`,
    `INSTALLER_OUT=${slashes(out)}`,
    // No -signed: the task adds `-signed "$PADMATE_SIGN_SUBJECT"` to every
    // signing build by itself, and this run is what proves it does.
    "VERIFY_FLAGS=-allow-untrusted-root -timestamp=false",
  ];
}

/** The environment of the build: signing mode, test certificate, offline. */
export function buildEnvironment(base, { pfx, password }) {
  const env = { ...base };
  for (const name of [
    "CSC_LINK",
    "CSC_KEY_PASSWORD",
    "PADMATE_SIGN_SHA1",
    "PADMATE_SIGN_TIMESTAMP",
    "SIGNTOOL_PATH",
    "DEBUG",
  ])
    delete env[name];
  return {
    ...env,
    PADMATE_SIGN: "pfx",
    PADMATE_SIGN_TEST: "1",
    PADMATE_SIGN_SUBJECT: subject,
    WIN_CSC_LINK: pfx,
    WIN_CSC_KEY_PASSWORD: password,
  };
}

function run(command, args, { cwd, env, quiet } = {}) {
  return new Promise((done, reject) => {
    const child = spawn(command, args, {
      cwd,
      env,
      windowsHide: true,
      shell: false,
      stdio: quiet ? ["ignore", "pipe", "pipe"] : "inherit",
    });
    let out = "";
    let err = "";
    child.stdout?.on("data", (d) => {
      out += d;
    });
    child.stderr?.on("data", (d) => {
      err += d;
    });
    child.on("error", reject);
    child.on("exit", (code) => done({ code, out, err }));
  });
}

async function powershell(script, env = process.env) {
  const encoded = Buffer.from(script, "utf16le").toString("base64");
  const { code, out, err } = await run(
    "powershell.exe",
    ["-NoProfile", "-NonInteractive", "-EncodedCommand", encoded],
    { env, quiet: true },
  );
  // With -EncodedCommand, PowerShell writes its progress and error streams to
  // stderr as CLIXML; only stdout holds the answer.
  if (code !== 0)
    throw new Error(`powershell failed (${code}): ${(err || out).trim()}`);
  return out;
}

/** What snapshotScript printed, as an object. */
export function decodeSnapshot(text) {
  return JSON.parse(Buffer.from(text.trim(), "base64").toString("utf8"));
}

async function snapshot() {
  return decodeSnapshot(await powershell(snapshotScript));
}

async function main() {
  // The public code mirror has no documentation to pack.
  refusePackaging();
  if (process.platform !== "win32") throw new Error("test:sign needs Windows");
  const { work, pfx, bin, out } = folders();
  if (!findSignTool())
    throw new Error(
      "signtool.exe was not found: install the Windows SDK (Signing Tools)",
    );
  const sdl = join(repo, "bin", "SDL3.dll");
  if (!existsSync(sdl))
    throw new Error("bin/SDL3.dll is missing: run task sdl first");
  const stale = await removeWithRetries(work, {
    options: { recursive: true },
  });
  if (stale) throw new Error(`cannot clear ${work}: ${stale.message}`);
  await mkdir(bin, { recursive: true });
  // The pinned SDL is already here: sdlfetch checks it and downloads nothing.
  await copyFile(sdl, join(bin, "SDL3.dll"));

  const before = await snapshot();
  const password = newPassword();
  // Before the file exists: from here on, Ctrl+C or an exit deletes it too.
  const letGo = guardFile(pfx);
  let failure;
  let notDeleted;
  // The public modulus of the certificate, to recognise its key files by.
  let modulus;
  try {
    modulus = parseModulus(
      await powershell(certificateScript, {
        ...process.env,
        PADMATE_SIGNTEST_SUBJECT: subject,
        PADMATE_SIGNTEST_PFX: pfx,
        PADMATE_SIGNTEST_PASSWORD: password,
      }),
    );
    console.log(
      `test:sign: throwaway certificate "${subject}" made in memory, exported to ${pfx}`,
    );
    const env = buildEnvironment(process.env, { pfx, password });
    const taskBin = process.env.TASK_BIN || "task";
    const build = await run(taskBin, taskArguments({ bin, out }), {
      cwd: repo,
      env,
    });
    if (build.code !== 0)
      throw new Error(`task package:installer failed (${build.code})`);
    // The check bites: the same tree is refused for another publisher, and for a
    // root that is not trusted when the test does not say that is fine.
    const verify = (...flags) =>
      run(
        "go",
        [
          "run",
          "./tools/packageinstaller",
          "verify",
          "-version",
          version,
          "-dist",
          out,
          ...flags,
        ],
        { cwd: join(repo, "daemon"), env: process.env, quiet: true },
      );
    const accepted = await verify(
      "-signed",
      subject,
      "-allow-untrusted-root",
      "-timestamp=false",
    );
    if (accepted.code !== 0)
      throw new Error(`verify refused the signed build:
${accepted.err}`);
    for (const [what, flags] of [
      [
        "another publisher",
        [
          "-signed",
          "someone else",
          "-allow-untrusted-root",
          "-timestamp=false",
        ],
      ],
      ["an untrusted root", ["-signed", subject, "-timestamp=false"]],
      ["a timestamp", ["-signed", subject, "-allow-untrusted-root"]],
    ]) {
      const result = await verify(...flags);
      if (result.code === 0)
        throw new Error(`verify accepted the build with ${what}`);
      console.log(`test:sign: verify refuses ${what}, as it should`);
    }
    console.log(
      `test:sign: ${join(out, `padmate-${version}-windows-amd64-setup.exe`)} is signed by "${subject}"`,
    );
  } catch (error) {
    failure = error;
  } finally {
    // The PFX holds the private key: it does not outlive the run. A file that
    // cannot be deleted is reported below, after the stores have been looked at.
    notDeleted = await removeWithRetries(pfx);
  }
  const after = await snapshot();
  const { problems, orphans } = compareSnapshots(before, after);
  if (problems.length === 0)
    console.log("test:sign: certificate stores and key files are as they were");
  else for (const p of problems) console.error(`test:sign: ${p}`);
  if (orphans.length > 0) {
    console.log(
      `test:sign: ${orphans.length} new key file(s) in the key folders:\n  ${orphans.join("\n  ")}`,
    );
    const keysDir = process.env.APPDATA
      ? join(process.env.APPDATA, "Microsoft", "Crypto", "Keys")
      : undefined;
    if (keysDir === undefined) {
      console.log("test:sign: %APPDATA% is not set: no key file is deleted");
    } else {
      const cleaned = await cleanUpKeys({
        orphans,
        before: keyFilePaths(before),
        keysDir,
        modulus,
      });
      for (const line of describeCleanup(cleaned)) console.log(line);
      if (cleaned.deleted.length > 0) {
        // Look again: nothing that was there before may be gone or changed.
        const again = compareSnapshots(before, await snapshot());
        for (const p of again.problems) {
          if (problems.includes(p)) continue;
          problems.push(p);
          console.error(`test:sign: ${p}`);
        }
        console.log(
          `test:sign: ${again.orphans.length} new key file(s) remain in the key folders`,
        );
      }
    }
  }
  if (existsSync(pfx)) {
    problems.push(
      `${pfx} is still there${notDeleted ? ` (${notDeleted.message})` : ""}: delete it, it holds the private key of the throwaway certificate`,
    );
  } else letGo();
  if (failure) throw failure;
  if (problems.length > 0)
    throw new Error("a certificate store or a key file changed");
  console.log("test:sign: ok");
}

if (resolve(process.argv[1] ?? "") === fileURLToPath(import.meta.url)) {
  main().catch((error) => {
    console.error(`test:sign: ${error.message}`);
    process.exit(1);
  });
}
