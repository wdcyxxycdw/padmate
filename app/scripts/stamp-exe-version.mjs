// Sets the numeric file version of padmate.exe to the release's three numbers
// (PKG-06) and signs the file again (PKG-09). electron-builder runs this as its
// `afterSign` hook, after it has edited the Electron executable's resources and
// signed it, and before NSIS packs it.
//
// Why it is needed: electron-builder gives resedit the app version as the file
// version ("0.15.1-rc.1"), and resedit's setFileVersion splits the string at
// "." and reads each piece with Number(). "1-rc" is NaN, which becomes 0, so
// v0.15.1-rc.1 got the numeric file version 0.15.0.1 and v0.15.1-rc.2 got
// 0.15.0.2: only a prerelease of a version with PATCH 0 came out right, by
// luck. The string FileVersion cannot be changed to avoid this, because it is
// the same string, and it must stay the app's version (docs/build-installer.md).
// So the hook rewrites only the fixed FILEVERSION numbers and leaves every
// string as electron-builder wrote it. The ProductVersion needs no fix: it is
// written from "0.15.1.0", which parses.
//
// Signing. electron-builder signs padmate.exe before it calls afterSign, so
// this edit would invalidate the signature, and "stamp before signing" is not
// possible from a hook. pe-library refuses a signed file unless it is told to
// ignore the certificate (`ignoreCert`), and then writes the file without it:
// the version is stamped on an unsigned file. The hook then asks the packager to
// sign it again (`signIf`, which does nothing when no certificate is
// configured, so an unsigned build is unaffected, and throws when the build
// must be signed and could not be). Signing runs the same tool with the same
// certificate as the first time, so the file is signed twice in all: once by
// electron-builder, whose signature the stamp discards, and once here.
// scripts/stamp-exe-version.test.mjs fails if a new electron-builder no longer
// has `signIf`.
import { readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { NtExecutable, NtExecutableResource, Resource } from "resedit";

// An app version as electron-builder has it: a release tag without the "v",
// the rule of daemon/internal/installerpkg.ParseTag.
const release =
  /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/;

/**
 * The numeric version of an app version: MAJOR.MINOR.PATCH and a zero, the
 * same tuple as exeresources.FixedVersion gives the Go executables.
 *
 * @param {string} appVersion such as "0.15.1" or "0.15.1-rc.1"
 * @returns {[number, number, number, number]}
 */
export function numericVersion(appVersion) {
  const match = release.exec(appVersion ?? "");
  if (match === null)
    throw new Error(
      `version ${JSON.stringify(appVersion)} is not a release version: use MAJOR.MINOR.PATCH with an optional -prerelease, such as 0.15.1 or 0.15.1-rc.1`,
    );
  const numbers = match.slice(1, 4).map(Number);
  if (numbers.some((n) => n > 65535))
    throw new Error(
      `version ${appVersion} has a number above 65535, which a Windows version cannot hold`,
    );
  return [numbers[0], numbers[1], numbers[2], 0];
}

/**
 * Writes the numeric file version of a version resource. Strings, the product
 * version and every other field stay as they are.
 *
 * @param {import("resedit").Resource.VersionInfo} info
 * @param {string} appVersion
 */
export function applyFileVersion(info, appVersion) {
  const [major, minor, patch, build] = numericVersion(appVersion);
  info.fixedInfo.fileVersionMS = ((major << 16) | minor) >>> 0;
  info.fixedInfo.fileVersionLS = ((patch << 16) | build) >>> 0;
}

/**
 * Rewrites the numeric file version of an executable in place.
 *
 * @param {string} file path of the .exe
 * @param {string} appVersion
 */
export async function stampFileVersion(file, appVersion) {
  numericVersion(appVersion);
  // ignoreCert: a signed file is accepted and written back without its
  // signature, which the caller signs again.
  const executable = NtExecutable.from(await readFile(file), {
    ignoreCert: true,
  });
  const resources = NtExecutableResource.from(executable);
  const infos = Resource.VersionInfo.fromEntries(resources.entries);
  if (infos.length === 0) throw new Error(`${file} has no version resource`);
  for (const info of infos) {
    applyFileVersion(info, appVersion);
    info.outputToResourceEntries(resources.entries);
  }
  resources.outputResource(executable);
  await writeFile(file, Buffer.from(executable.generate()));
}

/**
 * electron-builder's afterSign hook. `context.packager.appInfo.version` is the
 * version electron-builder used for the executable's strings (the extraMetadata
 * of scripts/package-installer.mjs).
 *
 * @param {{ electronPlatformName: string, appOutDir: string, packager: { appInfo: { productFilename: string, version: string } } }} context
 */
export default async function stampExeVersion(context) {
  if (context.electronPlatformName !== "win32") return;
  const { packager } = context;
  const { productFilename, version } = packager.appInfo;
  // Before anything is changed: a build whose signing hook is gone must not
  // come out unsigned without saying so.
  if (typeof packager.signIf !== "function")
    throw new Error(
      "electron-builder's WinPackager has no signIf(file): this hook cannot sign padmate.exe again after it stamps the version",
    );
  const file = join(context.appOutDir, `${productFilename}.exe`);
  await stampFileVersion(file, version);
  const signed = await packager.signIf(file);
  // signIf says false when no certificate is configured, and also when signing
  // was skipped; a build that must be signed has to say which.
  if (signed === false && packager.forceCodeSigning === true)
    throw new Error(
      `${file} was not signed again after its version was stamped, but code signing is required`,
    );
}
