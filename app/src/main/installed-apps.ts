import { win32 } from "node:path";
import type { BrowsedApp, InstalledApp } from "../shared/api";
import type { EditorHandler } from "./library-handlers";
import { validateAppIconPath } from "./security";

/** Folders below a Start Menu Programs folder that are read. */
export const MAX_DEPTH = 4;
/** Shortcuts looked at in all; the rest of the menu is not read. */
export const MAX_SHORTCUTS = 2000;
/** Programs listed at most. */
export const MAX_APPS = 600;
/**
 * The longest the Start Menu is read: a shortcut to a mapped network drive
 * that does not answer can hold one check for the Windows network timeout.
 * What was found by then is listed.
 */
export const SCAN_TIMEOUT_MS = 3000;

/**
 * Words of a setup tool: an installer, an uninstaller, an updater, upgrader
 * or patcher, a repair or recovery tool, a crash or error reporter, a helper
 * process, a telemetry tool. The rule is small on purpose: a whole word of
 * the name (isSetupTool splits camel case and separators first), so "Unison",
 * "Instagram", "HelpNDoc" and "Telegram" stay while "AutoUpdate", "unins000",
 * "EAUpdater" and "LauncherPatcher" go. A program with such a word in its
 * name is left out too; it can still be chosen from the recent apps or
 * browsed to.
 */
const setupWords =
  /\b(?:setup|install(?:er|ers|ation)?|unins|uninstall(?:er)?|update(?:r|rs|s)?|upgrade(?:r|rs|s)?|patcher|repair|recovery|(?:crash|error|bug) ?report(?:er|ing)?|helper|telemetry)\d*\b/i;
/**
 * The same for Chinese and Japanese, which have no word boundaries: 安装
 * (install), 卸载 (uninstall), 更新 (update), 升级 (upgrade), 修复 (repair),
 * 恢复 (recover), 错误报告 and 崩溃报告 (error and crash report), 遥测 and
 * テレメトリ (telemetry) anywhere in the name.
 */
const setupChinese =
  /安装|卸载|更新|升级|修复|恢复|错误报告|崩溃报告|遥测|テレメトリ/;

/**
 * Where Windows Installer keeps the icons of the shortcuts it puts in the
 * Start Menu (%APPDATA%\Microsoft\Installer\{product}\, and the one below the
 * Windows directory): such a shortcut leads to an icon file named like
 * "_4B7BB1….exe", not to the program, so binding it would match nothing.
 */
const installerCache = /(?:^|\\)Microsoft\\Installer\\/i;

/**
 * Whether a shortcut's name or a program's file name names a setup tool
 * (UI-46): an installer, uninstaller, updater, repair or recovery tool, or a
 * crash or error reporter, none of which is a program to bind a profile to.
 * The ".exe" of a file name does not count.
 */
export function isSetupTool(name: string): boolean {
  const base = name.replace(/\.exe$/i, "");
  // Names that no word split can read: "uninst", "unins000" and "uninstall"
  // all start the way an uninstaller's name does, and an all-lowercase file
  // name without separators ("autoupdate", "googleupdate") has no camel
  // case to split, so "update" or "upgrade" anywhere in it is the word.
  if (
    /^unins/i.test(base) ||
    (/^[a-z0-9]+$/.test(base) && /update|upgrade/.test(base))
  )
    return true;
  const words = base
    // EAUpdater → EA Updater, AutoUpdate → Auto Update.
    .replace(/([A-Z]+)([A-Z][a-z])/g, "$1 $2")
    .replace(/([a-z])([A-Z])/g, "$1 $2")
    .replace(/[_\-.()[\]]+/g, " ");
  return setupWords.test(words) || setupChinese.test(words);
}

/**
 * The Windows directory from the environment (%SystemRoot%, else %WINDIR%),
 * "" when neither is set. Programs below it, such as cmd.exe, explorer.exe
 * and control.exe, are system tools, whatever shortcut leads to them.
 */
export function windowsDirectory(
  env: Record<string, string | undefined> = process.env,
): string {
  return env.SystemRoot || env.WINDIR || env.windir || "";
}

/** Whether path lies below dir, in any case and with either kind of slash. */
function isBelow(path: string, dir: string): boolean {
  if (!dir) return false;
  const relative = win32.relative(dir, path);
  return (
    relative !== "" &&
    relative !== ".." &&
    !relative.startsWith("..\\") &&
    !win32.isAbsolute(relative)
  );
}

const collator = new Intl.Collator(["zh-CN", "en"], {
  numeric: true,
  sensitivity: "base",
});

/** What Main reads of the system: the Start Menu and the file system. */
export interface ShortcutSource {
  /** The Start Menu Programs folders, the first one first. */
  roots: string[];
  /** The entries of a folder; rejects when it cannot be read. */
  list(
    dir: string,
  ): Promise<Array<{ name: string; directory: boolean; file: boolean }>>;
  /** Where a .lnk file leads; throws when it cannot be read. */
  target(link: string): string;
  /** Whether path is an existing regular file. */
  isFile(path: string): Promise<boolean>;
  /**
   * The Windows directory (windowsDirectory): shortcuts to programs below it
   * are left out. Without one nothing is.
   */
  windowsDir?: string;
}

/** What accept needs of the system. */
interface AcceptSource {
  isFile: (path: string) => Promise<boolean>;
  windowsDir?: string;
}

/**
 * The program a shortcut or a listed path stands for: a local drive path to
 * an existing .exe file, under name, that is neither below the Windows
 * directory, in Windows Installer's icon cache, nor a setup tool
 * (isSetupTool, by the name or the file name).
 * The check is the one that guards icons (validateAppIconPath), so a
 * network path never reaches the file system.
 */
async function accept(
  name: unknown,
  target: unknown,
  source: AcceptSource,
): Promise<InstalledApp | undefined> {
  if (typeof name !== "string") return undefined;
  const label = name.trim();
  if (!label || isSetupTool(label)) return undefined;
  let path: string;
  try {
    path = validateAppIconPath(target);
  } catch {
    return undefined;
  }
  if (source.windowsDir && isBelow(path, source.windowsDir)) return undefined;
  if (installerCache.test(win32.normalize(path))) return undefined;
  const exe = win32.basename(path);
  if (isSetupTool(exe)) return undefined;
  try {
    if (!(await source.isFile(path))) return undefined;
  } catch {
    return undefined;
  }
  return { name: label, exe, path };
}

/**
 * The programs of the Start Menu (UI-46): every shortcut that leads to an
 * existing local .exe, named by the shortcut, once per executable (the
 * first shortcut that names it wins), sorted by name. Reading is bounded:
 * MAX_DEPTH folders down, MAX_SHORTCUTS shortcuts, MAX_APPS programs, and
 * SCAN_TIMEOUT_MS of time, after which it lists what it has. A folder or
 * shortcut that cannot be read is skipped; links to folders are not
 * followed. System tools and setup tools are left out (accept).
 */
export async function listInstalledApps(
  source: ShortcutSource,
  timeoutMs: number = SCAN_TIMEOUT_MS,
): Promise<InstalledApp[]> {
  const found = new Map<string, InstalledApp>();
  let links = 0;
  let expired = false;
  let timedOut = false;
  const full = () =>
    expired || links >= MAX_SHORTCUTS || found.size >= MAX_APPS;
  const walk = async (dir: string, depth: number): Promise<void> => {
    let entries: Awaited<ReturnType<ShortcutSource["list"]>>;
    try {
      entries = await source.list(dir);
    } catch {
      return;
    }
    entries.sort((a, b) => collator.compare(a.name, b.name));
    for (const entry of entries) {
      if (full()) return;
      const path = win32.join(dir, entry.name);
      if (entry.directory) {
        if (depth < MAX_DEPTH) await walk(path, depth + 1);
        continue;
      }
      if (!entry.file || !/\.lnk$/i.test(entry.name)) continue;
      links++;
      let target = "";
      try {
        target = source.target(path);
      } catch {
        continue;
      }
      const app = await accept(entry.name.slice(0, -".lnk".length), target, {
        isFile: source.isFile,
        windowsDir: source.windowsDir,
      });
      const key = app?.path.toLowerCase();
      if (app && key && !expired && !found.has(key)) found.set(key, app);
    }
  };
  const scan = async () => {
    for (const root of source.roots) {
      if (full()) break;
      await walk(root, 0);
    }
  };
  // A check that never comes back must not hold the list: the scan goes on
  // in the background until it reaches the next one, and then stops.
  let timer: ReturnType<typeof setTimeout> | undefined;
  const limit = new Promise<void>((resolve) => {
    timer = setTimeout(() => {
      expired = true;
      timedOut = true;
      resolve();
    }, timeoutMs);
  });
  await Promise.race([scan(), limit]);
  clearTimeout(timer);
  expired = true;
  const apps = [...found.values()].sort((a, b) =>
    collator.compare(a.name, b.name),
  );
  if (timedOut) partialReadings.add(apps);
  return apps;
}

/**
 * The readings of listInstalledApps that the time limit cut off: what is
 * missing may be found by a later reading, so a keeper of readings
 * (programScan) should not rely on one for long.
 */
const partialReadings = new WeakSet<InstalledApp[]>();

/** Whether the time limit cut the reading short (listInstalledApps). */
export function isPartialReading(apps: InstalledApp[]): boolean {
  return partialReadings.has(apps);
}

/** How long a reading of the Start Menu is reused for finding a program's path. */
export const SCAN_CACHE_MS = 5 * 60_000;

/**
 * How long a reading that ran out of time is reused: short, so a program
 * it did not reach is looked for again soon, but not so short that every
 * lookup starts a scan that runs out of time again.
 */
export const PARTIAL_SCAN_CACHE_MS = 30_000;

/**
 * Readings of the Start Menu shared by the picker and by the search for a
 * program's path (UI-42). fresh() reads again, as every opening of the
 * picker does, and keeps that reading; cached() serves the last reading for
 * SCAN_CACHE_MS. Calls that overlap share one reading, so the scan (bounded
 * by SCAN_TIMEOUT_MS) is never run twice at once. A reading that fails is
 * not kept, and one that ran out of time is kept for
 * PARTIAL_SCAN_CACHE_MS only.
 */
export function programScan(
  list: () => Promise<InstalledApp[]>,
  now: () => number = Date.now,
): { fresh(): Promise<InstalledApp[]>; cached(): Promise<InstalledApp[]> } {
  let last: { at: number; apps: InstalledApp[] } | undefined;
  let reading: Promise<InstalledApp[]> | undefined;
  const fresh = (): Promise<InstalledApp[]> => {
    if (!reading) {
      const pending = list().then((apps) => {
        last = { at: now(), apps };
        return apps;
      });
      reading = pending;
      const done = () => {
        if (reading === pending) reading = undefined;
      };
      pending.then(done, done);
    }
    return reading;
  };
  return {
    fresh,
    cached: () =>
      last &&
      now() - last.at <
        (isPartialReading(last.apps) ? PARTIAL_SCAN_CACHE_MS : SCAN_CACHE_MS)
        ? Promise.resolve(last.apps)
        : fresh(),
  };
}

/**
 * A development run with PADMATE_UI_APPS_FILE lists the entries of that
 * JSON file ([{"name", "path"}]) instead of the Start Menu, so E2E never
 * reads the real one. The same checks apply; a file that is not a list
 * gives no programs.
 */
export async function listFixtureApps(
  file: string,
  deps: {
    read: (file: string) => Promise<string>;
    isFile: (path: string) => Promise<boolean>;
    windowsDir?: string;
  },
): Promise<InstalledApp[]> {
  let entries: unknown;
  try {
    entries = JSON.parse(await deps.read(file));
  } catch {
    return [];
  }
  if (!Array.isArray(entries)) return [];
  const found = new Map<string, InstalledApp>();
  for (const entry of entries.slice(0, MAX_APPS)) {
    if (entry === null || typeof entry !== "object") continue;
    const { name, path } = entry as { name?: unknown; path?: unknown };
    const app = await accept(name, path, deps);
    const key = app?.path.toLowerCase();
    if (app && key && !found.has(key)) found.set(key, app);
  }
  return [...found.values()].sort((a, b) => collator.compare(a.name, b.name));
}

/**
 * The programs list and the .exe file dialog of the 添加软件 menu (UI-46).
 * The renderer supplies no path to either; a chosen file is checked like a
 * bound app's icon path before it is handed back.
 */
export function installedAppHandlers(options: {
  list: () => Promise<InstalledApp[]>;
  /** The open dialog for an .exe; undefined when the user cancels. */
  chooseExe: () => Promise<string | undefined>;
}): Record<string, EditorHandler> {
  // Calls that overlap share one reading of the Start Menu.
  let reading: Promise<InstalledApp[]> | undefined;
  return {
    appsInstalled: {
      count: 0,
      run: (): Promise<InstalledApp[]> => {
        if (!reading) {
          const pending = options.list();
          reading = pending;
          const done = () => {
            if (reading === pending) reading = undefined;
          };
          pending.then(done, done);
        }
        return reading;
      },
    },
    appBrowse: {
      count: 0,
      run: async (): Promise<BrowsedApp | null> => {
        const chosen = await options.chooseExe();
        if (chosen === undefined) return null;
        let path: string;
        try {
          path = validateAppIconPath(chosen);
        } catch {
          throw Object.assign(
            new Error("Choose an .exe file on a local drive"),
            { data: { reason: "path" } },
          );
        }
        return { exe: win32.basename(path), path };
      },
    },
  };
}
