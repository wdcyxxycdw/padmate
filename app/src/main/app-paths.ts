import type { execFile } from "node:child_process";
import { win32 } from "node:path";
import type { InstalledApp } from "../shared/api";
import type { EditorHandler } from "./library-handlers";
import { validateAppIconPath, validateExeName } from "./security";

/** How long a name that was not found is not looked for again. */
export const APP_PATH_MISS_MS = 60_000;
/** Names Main keeps an answer for; the oldest goes first. */
export const APP_PATH_CACHE = 256;
/** The longest a reg.exe query may take. */
export const REG_TIMEOUT_MS = 1500;
/**
 * The most of a reg.exe query's output that is kept. An App Paths value is a
 * line or two; the MuiCache lists every program the user has started, two
 * lines each (about 65 KB for 280 programs), so this leaves room for several
 * thousand. Longer output makes the query fail instead of growing.
 */
export const REG_MAX_OUTPUT = 1024 * 1024;

/**
 * Where Main looks for the path of an executable that a rule names by file
 * name only (UI-42): the Start Menu's programs, then the registry's App
 * Paths, then the MuiCache, in that order.
 */
export interface AppPathSource {
  /** The programs of the Start Menu; a reading that may be a minutes old. */
  installed(): Promise<InstalledApp[]>;
  /**
   * The default values of the executable's App Paths keys, in order of
   * preference. Whatever they hold is checked before it is used.
   */
  registered(exe: string): Promise<string[]>;
  /**
   * The paths the MuiCache remembers a program with that file name was
   * started from, in the order the registry lists them. A program moved or
   * uninstalled since stays in the list, so every path is checked before it
   * is used.
   */
  launched(exe: string): Promise<string[]>;
  /** Whether path is an existing regular file. */
  isFile(path: string): Promise<boolean>;
  /** The clock, in milliseconds; Date.now when left out. */
  now?(): number;
}

/** The first of the candidates that is a local path to an existing .exe. */
async function firstFile(
  candidates: string[],
  source: AppPathSource,
): Promise<string> {
  for (const candidate of candidates) {
    try {
      const path = validateAppIconPath(candidate);
      if (await source.isFile(path)) return path;
    } catch {
      // Not a local path to an .exe, or not readable: the next one.
    }
  }
  return "";
}

async function find(exe: string, source: AppPathSource): Promise<string> {
  const wanted = exe.toLowerCase();
  try {
    for (const app of await source.installed())
      if (win32.basename(app.path).toLowerCase() === wanted) return app.path;
  } catch {
    // The registry may still know it.
  }
  // One that fails does not keep the next one from being asked.
  const lookups = [
    (name: string) => source.registered(name),
    (name: string) => source.launched(name),
  ];
  for (const lookup of lookups) {
    let candidates: string[];
    try {
      candidates = await lookup(exe);
    } catch {
      continue;
    }
    const path = await firstFile(candidates, source);
    if (path) return path;
  }
  return "";
}

/**
 * Finds the path of the executable named exe (UI-42), "" when there is none:
 * the Start Menu program with that file name (the first by name when
 * several have it), else the file an App Paths key names, else a path the
 * MuiCache remembers; the last two must be a local path to an existing .exe.
 * A path found is kept for the session and a name not found for
 * APP_PATH_MISS_MS, so a program installed later is found; overlapping asks
 * share one search; at most APP_PATH_CACHE names are kept. It never rejects.
 */
export function appPathResolver(
  source: AppPathSource,
): (exe: string) => Promise<string> {
  const now = () => (source.now ?? Date.now)();
  const cache = new Map<
    string,
    { result: Promise<string>; found?: boolean; at?: number }
  >();
  return (exe) => {
    const key = exe.toLowerCase();
    const known = cache.get(key);
    if (
      known &&
      (known.at === undefined ||
        known.found ||
        now() - known.at < APP_PATH_MISS_MS)
    )
      return known.result;
    cache.delete(key);
    const entry: { result: Promise<string>; found?: boolean; at?: number } = {
      result: find(exe, source).catch(() => ""),
    };
    entry.result.then((path) => {
      entry.found = path !== "";
      entry.at = now();
    });
    cache.set(key, entry);
    if (cache.size > APP_PATH_CACHE)
      cache.delete(cache.keys().next().value as string);
    return entry.result;
  };
}

/**
 * The renderer's ask for a path by exe name (UI-42). Only a bare .exe file
 * name is accepted (validateExeName); the answer is a local path or "".
 */
export function appPathHandlers(
  resolve: (exe: string) => Promise<string>,
): Record<string, EditorHandler> {
  return {
    appPath: {
      count: 1,
      run: (exe): Promise<string> => resolve(validateExeName(exe)),
    },
  };
}

// ---------------------------------------------------------------------------
// The registry, read-only through reg.exe.

const appPaths = "SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\App Paths\\";

/**
 * The App Paths keys of an executable: the current user's, the machine's
 * and the machine's 32-bit view, where installers of 32-bit programs write.
 */
export function appPathKeys(exe: string): string[] {
  return [
    `HKCU\\${appPaths}${exe}`,
    `HKLM\\${appPaths}${exe}`,
    `HKLM\\SOFTWARE\\WOW6432Node\\${appPaths.slice("SOFTWARE\\".length)}${exe}`,
  ];
}

/** Replaces %NAME% by the variable of that name (any case); others stay. */
export function expandEnv(
  value: string,
  env: Record<string, string | undefined>,
): string {
  const lower = new Map<string, string>();
  for (const [name, text] of Object.entries(env))
    if (text !== undefined) lower.set(name.toLowerCase(), text);
  return value.replace(
    /%([^%]+)%/g,
    (all, name: string) => lower.get(name.toLowerCase()) ?? all,
  );
}

/**
 * The path in the output of "reg query KEY /ve": the default value, a
 * REG_SZ or REG_EXPAND_SZ, with its variables expanded and quotes (and
 * arguments after them) removed. The value's name is localised
 * ("(Default)", "(默认)"), so only the type and its spacing are matched.
 * undefined when there is no such line. The text is only a candidate: the
 * caller checks it is a local path to an existing .exe.
 */
export function registeredPath(
  output: string,
  env: Record<string, string | undefined>,
): string | undefined {
  const line = /^[ \t]+\S.*?[ ]{4}REG_(?:EXPAND_)?SZ[ ]{4}(.+?)[ \t]*$/m.exec(
    output,
  );
  if (!line?.[1]) return undefined;
  let value = expandEnv(line[1], env);
  if (value.startsWith('"')) {
    const end = value.indexOf('"', 1);
    if (end > 1) value = value.slice(1, end);
  }
  return value;
}

/**
 * reg.exe writes in the console's OEM code page, which is GBK on a Chinese
 * Windows. UTF-8 when the bytes are, else GBK (a path in another OEM code
 * page then comes out wrong, fails the existence check and is dropped).
 */
export function decodeRegOutput(output: Buffer): string {
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(output);
  } catch {
    try {
      return new TextDecoder("gbk").decode(output);
    } catch {
      return output.toString("latin1");
    }
  }
}

/** Runs reg.exe with args and gives its output; undefined when it fails. */
export type RegRunner = (args: string[]) => Promise<Buffer | undefined>;

/**
 * The system's own reg.exe (never one found on PATH), limited to "query": a
 * timeout, no console window and a bounded output (REG_MAX_OUTPUT). Anything
 * else is refused, so nothing here can change the registry.
 */
export function regExe(
  exec: typeof execFile,
  systemRoot: string = process.env.SystemRoot || "C:\\Windows",
): RegRunner {
  const file = win32.join(systemRoot, "System32", "reg.exe");
  return (args) =>
    new Promise((resolve) => {
      if (args[0] !== "query") return resolve(undefined);
      exec(
        file,
        args,
        {
          encoding: "buffer",
          timeout: REG_TIMEOUT_MS,
          windowsHide: true,
          maxBuffer: REG_MAX_OUTPUT,
        },
        (error, stdout) => resolve(error ? undefined : stdout),
      );
    });
}

/**
 * Reads the App Paths of an executable through run. The three keys of one
 * program are asked together, one program at a time, so a page of unknown
 * programs does not start a crowd of processes. It never rejects; a key
 * that is missing or cannot be read gives nothing.
 */
export function registrySource(
  run: RegRunner,
  env: Record<string, string | undefined> = process.env,
): (exe: string) => Promise<string[]> {
  let queue: Promise<unknown> = Promise.resolve();
  const lookup = async (exe: string): Promise<string[]> => {
    const outputs = await Promise.all(
      appPathKeys(exe).map(async (key) => {
        try {
          return await run(["query", key, "/ve"]);
        } catch {
          return undefined;
        }
      }),
    );
    const paths: string[] = [];
    for (const output of outputs) {
      const path = output && registeredPath(decodeRegOutput(output), env);
      if (path) paths.push(path);
    }
    return paths;
  };
  return (exe) => {
    const result = queue.then(() => lookup(exe));
    queue = result;
    return result;
  };
}

/**
 * The MuiCache: Windows records the name and company of every program the
 * user starts, as values named "<full path>.FriendlyAppName" and
 * "<full path>.ApplicationCompany". It knows programs that have neither a
 * Start Menu entry nor an App Paths key.
 */
export const muiCacheKey =
  "HKCU\\Software\\Classes\\Local Settings\\Software\\Microsoft\\Windows\\Shell\\MuiCache";

/** How many paths of one file name are handed on to be checked on disk. */
export const MUICACHE_CANDIDATES = 16;

/** A value line of "reg query": its name, then its type, then its data. */
const valueLine = /^[ \t]+(\S.*?)[ ]{4}REG_[A-Z_]+(?:[ ]{4}|[ \t]*$)/;
const muiCacheSuffix = /\.(?:FriendlyAppName|ApplicationCompany)$/i;

/**
 * The paths of the executables in the output of "reg query MUICACHE", by
 * lower-case file name, each path once, in the order of the output. The
 * value names are not localised and the output is only read for them, so
 * English and Chinese systems read alike. A name that has neither suffix is
 * taken as it is (a key of plain paths reads the same), and a name whose file
 * name is not an .exe (LangID and the like) is left out. The paths are only
 * candidates: the caller checks that each is a local path to an existing .exe.
 */
export function muiCacheIndex(output: string): Map<string, string[]> {
  const index = new Map<string, string[]>();
  for (const line of output.split(/\r?\n/)) {
    const name = valueLine.exec(line)?.[1];
    if (!name) continue;
    const path = name.replace(muiCacheSuffix, "");
    const file = win32.basename(path).toLowerCase();
    if (!file.endsWith(".exe")) continue;
    const paths = index.get(file) ?? [];
    const known = path.toLowerCase();
    if (!paths.some((other) => other.toLowerCase() === known)) paths.push(path);
    index.set(file, paths);
  }
  return index;
}

/**
 * The MuiCache paths of an executable (AppPathSource.launched). One query of
 * the whole key serves every name; the reading is kept for APP_PATH_MISS_MS,
 * the time the resolver does not ask again for a name it did not find, so
 * the next such ask reads it again and sees a program started since. Asks
 * that overlap share one query. It never rejects: a query that fails or
 * times out gives no paths, and is not repeated before that time is up.
 */
export function muiCacheSource(
  run: RegRunner,
  now: () => number = Date.now,
): (exe: string) => Promise<string[]> {
  let last: { at: number; index: Map<string, string[]> } | undefined;
  let reading: Promise<Map<string, string[]>> | undefined;
  const read = (): Promise<Map<string, string[]>> => {
    if (!reading) {
      const pending = (async () => {
        let index = new Map<string, string[]>();
        try {
          const output = await run(["query", muiCacheKey]);
          if (output) index = muiCacheIndex(decodeRegOutput(output));
        } catch {
          // Nothing is known; the next reading may do better.
        }
        last = { at: now(), index };
        return index;
      })();
      reading = pending;
      const done = () => {
        if (reading === pending) reading = undefined;
      };
      pending.then(done, done);
    }
    return reading;
  };
  return async (exe) => {
    const index =
      last && now() - last.at < APP_PATH_MISS_MS ? last.index : await read();
    return (index.get(exe.toLowerCase()) ?? []).slice(0, MUICACHE_CANDIDATES);
  };
}

/**
 * The registry's sources for appPathResolver: App Paths and the MuiCache,
 * both through run. While fixture() is true (a test's own apps list stands
 * for the whole machine) neither reads anything.
 */
export function registrySources(
  run: RegRunner,
  options: {
    fixture(): boolean;
    env?: Record<string, string | undefined>;
    now?: () => number;
  },
): Pick<AppPathSource, "registered" | "launched"> {
  const registered = registrySource(run, options.env);
  const launched = muiCacheSource(run, options.now);
  return {
    registered: (exe) =>
      options.fixture() ? Promise.resolve([]) : registered(exe),
    launched: (exe) =>
      options.fixture() ? Promise.resolve([]) : launched(exe),
  };
}
