// Icons of the apps bound to a profile (UI-42). Main reads an icon from an
// executable's path; this finds the path for a rule. A rule written as a
// path names it. A rule by file name ("krita.exe") uses an app seen in the
// foreground this session (apps.recent), or the path the app had when it
// was bound, which this window remembers in its own browser storage, or the
// path Main finds from the Start Menu or the registry (resolveAppIcon).
// With none, the page shows a generic application icon, never a letter.
import type { PadmateAPI, RecentApp } from "../shared/api";
import { isExePath, matchExe } from "./library-model";

/** localStorage: exe file name (lower case) → path when it was bound. */
export const APP_PATHS_KEY = "padmate.appPaths";
/** Paths remembered at most; the oldest goes first. */
export const APP_PATHS_LIMIT = 64;

/** A path Main accepts: a local drive path to an .exe. */
function localExe(path: string): boolean {
  return /^[A-Za-z]:[\\/]/.test(path) && /\.exe$/i.test(path);
}

function remembered(): Record<string, string> {
  try {
    const value: unknown = JSON.parse(
      globalThis.localStorage?.getItem(APP_PATHS_KEY) ?? "{}",
    );
    return value && typeof value === "object" && !Array.isArray(value)
      ? (value as Record<string, string>)
      : {};
  } catch {
    return {};
  }
}

/** Remembers where a bound app was, for its icon after a restart. */
export function rememberAppPath(exe: string, path: string): void {
  if (!localExe(path) || isExePath(exe)) return;
  const key = exe.toLowerCase();
  const paths = remembered();
  if (paths[key] === path) return;
  delete paths[key];
  paths[key] = path;
  const keys = Object.keys(paths);
  for (const old of keys.slice(0, Math.max(0, keys.length - APP_PATHS_LIMIT)))
    delete paths[old];
  try {
    globalThis.localStorage?.setItem(APP_PATHS_KEY, JSON.stringify(paths));
  } catch {
    /* Main finds the path again after a restart. */
  }
}

/** The executable to read a rule's icon from, or "" when none is known. */
export function iconPath(exe: string, recent: readonly RecentApp[]): string {
  if (isExePath(exe)) return localExe(exe) ? exe : "";
  const seen = recent.find((app) => matchExe(exe, app.path))?.path;
  if (seen && localExe(seen)) return seen;
  const path = remembered()[exe.toLowerCase()];
  return typeof path === "string" && localExe(path) ? path : "";
}

const icons = new Map<string, Promise<string>>();
const iconUrls = new Map<string, string>();

/** The icon's data URL, "" without one; each path is asked for once. */
export function loadAppIcon(
  api: Pick<PadmateAPI, "getAppIcon">,
  path: string,
): Promise<string> {
  const key = path.toLowerCase();
  let icon = icons.get(key);
  if (!icon) {
    icon = Promise.resolve()
      .then(() => api.getAppIcon(path))
      .then((url) =>
        typeof url === "string" && url.startsWith("data:image/") ? url : "",
      )
      .catch(() => "")
      .then((url) => {
        if (url) iconUrls.set(key, url);
        return url;
      });
    icons.set(key, icon);
  }
  return icon;
}

/**
 * The icon of path if it has been read already, else "": what a page that
 * comes back shows in its first render, so a known icon does not flash a
 * generic one first.
 */
export function peekAppIcon(path: string): string {
  return path ? (iconUrls.get(path.toLowerCase()) ?? "") : "";
}

const paths = new Map<string, Promise<string>>();

/**
 * The path Main finds for a rule's file name (the Start Menu's program with
 * it, else the registry's App Paths, else its MuiCache), "" when there is
 * none or Main fails.
 * Only a local drive path to an .exe is taken. A path found is asked for
 * once; a name not found is asked about again, as Main may find it later
 * (Main keeps its own short memory of misses).
 */
export function loadAppPath(
  api: Pick<PadmateAPI, "resolveAppPath">,
  exe: string,
): Promise<string> {
  const key = exe.toLowerCase();
  let found = paths.get(key);
  if (!found) {
    const asked: Promise<string> = Promise.resolve()
      .then(() => api.resolveAppPath(exe))
      .then((path) => (typeof path === "string" && localExe(path) ? path : ""))
      .catch(() => "");
    found = asked;
    paths.set(key, asked);
    void asked.then((path) => {
      if (!path && paths.get(key) === asked) paths.delete(key);
    });
  }
  return found;
}

/**
 * A rule's icon and the path it was read from. known is iconPath's answer.
 * The icon of a known path comes first; when there is none (the file is
 * gone, as after the app moved), or no path is known, and the rule names a
 * file only, Main is asked for the path (loadAppPath) and the icon is read
 * from that. url is "" when nothing gives an icon: the page then shows the
 * generic application icon.
 */
export async function resolveAppIcon(
  api: Pick<PadmateAPI, "getAppIcon" | "resolveAppPath">,
  exe: string,
  known: string,
): Promise<{ path: string; url: string }> {
  if (known) {
    const url = await loadAppIcon(api, known);
    if (url) return { path: known, url };
  }
  if (isExePath(exe)) return { path: known, url: "" };
  const found = await loadAppPath(api, exe);
  if (!found || found.toLowerCase() === known.toLowerCase())
    return { path: known, url: "" };
  return { path: found, url: await loadAppIcon(api, found) };
}

/** For tests: forgets the icons and paths read so far. */
export function forgetAppIcons(): void {
  icons.clear();
  iconUrls.clear();
  paths.clear();
}
