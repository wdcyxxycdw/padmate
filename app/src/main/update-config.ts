import { homedir } from "node:os";
import { join } from "node:path";

// The constants of the automatic update (PKG-10, ADR-0073). Only Main uses
// them; the window never names a URL or a folder.

/**
 * The repository the releases are read from. The same two names are in
 * electron-builder.yml's publish block (TestElectronBuilderConfig pins them
 * equal), which becomes resources\app-update.yml. Changing the source of the
 * updates means changing both.
 */
export const UPDATE_OWNER = "wdcyxxycdw";
export const UPDATE_REPO = "padmate";

/** The page that lists the releases; the fallback of releasePageURL. */
export const RELEASES_URL = `https://github.com/${UPDATE_OWNER}/${UPDATE_REPO}/releases`;

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

/** Whether the version has a prerelease part, as 1.0.0-rc.1 has. */
export function hasPrerelease(version: string): boolean {
  return (parseVersion(version)?.pre.length ?? 0) > 0;
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
 * The code of a look that found no release to read: the repository is
 * public and has releases, and none of them is a production release (only
 * release candidates are published until 1.0.0). UpdateController takes it
 * for "nothing newer" in its second look, and for no other error.
 */
export const NO_RELEASE_CODE = "ERR_UPDATE_NO_RELEASE";

// electron-updater 6.8.9 gives one error code for every reason the look at
// the latest release failed, and the HTTP status only in the text of the
// error that it wraps (github-channels.test.ts drives its GitHub provider
// and pins this text).
const noReleasePattern =
  /please ensure a production release exists: HttpError: 404\b/;

/**
 * Whether the error is GitHub saying the repository has no production
 * release: the 404 of the request for the latest release, after the list of
 * releases was read. A repository that cannot be read at all fails at the
 * list, a network or server failure has another status: both are failures.
 */
export function isNoReleaseError(error: unknown): boolean {
  const message = (error as { message?: unknown } | null)?.message;
  return typeof message === "string" && noReleasePattern.test(message);
}

/**
 * The page of a release, built from the constants and a version that was
 * checked; the page of all releases for anything else. Nothing the feed says
 * is put into the address.
 */
export function releasePageURL(version?: string): string {
  const checked = validVersion(version);
  return checked ? `${RELEASES_URL}/tag/v${checked}` : RELEASES_URL;
}

/**
 * Whether the address is one of the release pages releasePageURL builds: the
 * list of releases, or the page of one tag. The only addresses Main opens.
 */
export function isReleasePageURL(url: string): boolean {
  if (url === RELEASES_URL) return true;
  const prefix = `${RELEASES_URL}/tag/v`;
  return (
    url.startsWith(prefix) && validVersion(url.slice(prefix.length)) !== null
  );
}
