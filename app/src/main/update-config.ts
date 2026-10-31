import { homedir } from "node:os";
import { join } from "node:path";

// The constants of the automatic update (PKG-10, ADR-0073). Only Main uses
// them; the window never names a URL or a folder.

/**
 * The download site the updates are read from (ADR-0078): latest.yml and the
 * installers sit in its root. It ends in a slash, because latest.yml names the
 * installer by a relative path that is resolved against it. The same address
 * is in electron-builder.yml's publish block (which becomes
 * resources\app-update.yml) and in Go's installerpkg.UpdateURL; the tests of
 * both pin them equal (TestElectronBuilderPublishesToTheUpdateSource).
 * Changing the source of the updates means changing all three.
 *
 * It is also the one address Main opens in the browser (isManualDownloadURL):
 * the site has no page, and a browser that opens it downloads the newest
 * installer.
 */
export const UPDATE_URL = "https://padmate.download/";

/**
 * The channel file the site holds: latest.yml. It is written down, as
 * electron-builder would otherwise name the channel after the prerelease part
 * of the version (rc.yml for 1.0.3-rc.1), which the site never has. Only
 * releases are published there.
 */
export const UPDATE_CHANNEL = "latest";

/**
 * The folder of %LOCALAPPDATA% where electron-updater keeps what it
 * downloads (pending\) and where electron-builder's installer keeps a copy of
 * itself. build/installer.nsh names it too (PADMATE_UPDATER_DIR) and deletes
 * it with a full uninstall.
 */
export const UPDATER_CACHE_DIR = "padmate-updater";

/** The first check comes this long after the window starts. */
export const FIRST_CHECK_MS = 30_000;
/** Checks are at least this far apart, and repeat this often while open. */
export const CHECK_INTERVAL_MS = 24 * 60 * 60 * 1000;

/** The folder electron-updater downloads to, below LOCALAPPDATA. */
export function updaterCacheRoot(
  env: NodeJS.ProcessEnv,
  home: string = homedir(),
): string {
  // The same rule as electron-updater's getAppCacheDir on Windows.
  return join(
    env.LOCALAPPDATA || join(home, "AppData", "Local"),
    UPDATER_CACHE_DIR,
  );
}

/**
 * The installed application's test mode (PKG-03, PKG-10) downloads below its
 * private browser data: electron-updater keeps its cache below the
 * LOCALAPPDATA of the process, so that variable is pointed there, before the
 * first download. Returns the folder.
 */
export function redirectUpdaterHome(
  env: NodeJS.ProcessEnv,
  userData: string,
): string {
  const home = updaterTestHome(userData);
  env.LOCALAPPDATA = home;
  return home;
}

/** The LOCALAPPDATA that the test mode gives the updater. */
export function updaterTestHome(userData: string): string {
  return join(userData, "updater-home", "Local");
}

// The same rule as the release tag in scripts/package-installer.mjs and
// daemon/internal/installerpkg.ParseTag, without the v: MAJOR.MINOR.PATCH and
// an optional -prerelease, no build metadata.
const versionPattern =
  /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$/;

interface ParsedVersion {
  core: [number, number, number];
  pre: string[];
}

function parseVersion(version: string): ParsedVersion | null {
  const match = versionPattern.exec(version);
  if (!match) return null;
  return {
    core: [Number(match[1]), Number(match[2]), Number(match[3])],
    pre: match[4] ? match[4].split(".") : [],
  };
}

/**
 * The text itself when it is a version padmate would release, else null. The
 * version of an update comes from a file on the network: it is checked before
 * it is shown or put into a URL.
 */
export function validVersion(version: unknown): string | null {
  if (typeof version !== "string" || version.length > 64) return null;
  return parseVersion(version) ? version : null;
}

/**
 * Semantic version order. Negative when a is older than b. A text that is not
 * a version is older than every version.
 */
export function compareVersions(a: string, b: string): number {
  const left = parseVersion(a);
  const right = parseVersion(b);
  if (!left || !right) return left ? 1 : right ? -1 : 0;
  for (let i = 0; i < 3; i++) {
    const difference = left.core[i] - right.core[i];
    if (difference !== 0) return difference;
  }
  // A release is later than its prereleases.
  if (!left.pre.length || !right.pre.length)
    return right.pre.length - left.pre.length;
  const count = Math.min(left.pre.length, right.pre.length);
  for (let i = 0; i < count; i++) {
    const x = left.pre[i];
    const y = right.pre[i];
    if (x === y) continue;
    const xNumber = /^\d+$/.test(x);
    const yNumber = /^\d+$/.test(y);
    if (xNumber && yNumber) return Number(x) - Number(y);
    // A number is earlier than a word.
    if (xNumber !== yNumber) return xNumber ? -1 : 1;
    return x < y ? -1 : 1;
  }
  return left.pre.length - right.pre.length;
}

/**
 * Whether the address is the one Main opens in the browser: the download site
 * itself, as the constant spells it. The only address Main opens for the
 * update; nothing the feed says is put into it.
 */
export function isManualDownloadURL(url: string): boolean {
  return url === UPDATE_URL;
}
