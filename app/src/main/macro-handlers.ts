import { randomBytes } from "node:crypto";
import { lstat, open } from "node:fs/promises";
import {
  basename,
  dirname,
  isAbsolute,
  relative,
  resolve,
  sep,
} from "node:path";
import {
  type LegacyScript,
  LibraryErrorCode,
  type MacroCreated,
  type MacroEdit,
  type MacroImportResult,
  type MacroInfo,
  type MacroList,
  type MacroRenamed,
  type MacroUse,
  type MissingMacro,
  type ProfileEdit,
} from "../shared/api";
import { checkMacroFile } from "../shared/macro-names";
import type { DaemonCall, EditorHandler } from "./library-handlers";
import {
  macroImportName,
  maxScriptBytes,
  scriptImportProblem,
  validateMacroCandidate,
  validateMacroFile,
  validateMacroName,
  validateProfileID,
  validateScriptImportOptions,
} from "./security";

export interface MacroHandlerOptions {
  call: DaemonCall;
  // Shows the native open dialog for one .ts or .js file and resolves to
  // its path, or undefined when the user cancels.
  chooseScript: () => Promise<string | undefined>;
  // Opens a file in an editor (editorLauncher); resolves to whether one
  // started.
  openEditor: (path: string) => Promise<boolean>;
  // The data folder the daemon was started with: a library path inside it
  // is the library even when the folder two levels up is called profiles.
  dataDir?: string;
  now?: () => number;
}

// A file read for an import that needs the user's confirmation to replace
// an existing script. It stays in Main only, keyed by a random token.
interface PendingImport {
  name: string;
  text: string;
  expires: number;
}

export const pendingImportTTL = 2 * 60 * 1000;
const maxPendingImports = 4;

const invalidResponse = () => new Error("Invalid daemon response");

function object(value: unknown): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value))
    throw invalidResponse();
  return value as Record<string, unknown>;
}

const optionalString = (value: unknown) =>
  value === undefined || typeof value === "string";

// The daemon counts the actions that run a macro; an older one does not.
function actions(value: unknown): { actions?: number } {
  if (value === undefined) return {};
  if (!Number.isSafeInteger(value) || (value as number) < 0)
    throw invalidResponse();
  return { actions: value as number };
}

// The daemon says when the file was created (Unix milliseconds) where the
// platform does; an older daemon does not.
function created(value: unknown): { created?: number } {
  if (value === undefined) return {};
  if (!Number.isSafeInteger(value) || (value as number) < 0)
    throw invalidResponse();
  return { created: value as number };
}

function uses(value: unknown): MacroUse[] {
  if (!Array.isArray(value)) throw invalidResponse();
  return value.map((item) => {
    const use = object(item);
    if (
      typeof use.profile !== "string" ||
      typeof use.profileName !== "string" ||
      typeof use.control !== "string" ||
      typeof use.gesture !== "string"
    )
      throw invalidResponse();
    return {
      profile: use.profile,
      profileName: use.profileName,
      control: use.control,
      gesture: use.gesture,
    };
  });
}

function macroInfo(value: unknown): MacroInfo {
  const item = object(value);
  if (
    typeof item.file !== "string" ||
    typeof item.name !== "string" ||
    !optionalString(item.problem) ||
    (item.invalidName !== undefined && typeof item.invalidName !== "boolean")
  )
    throw invalidResponse();
  return {
    file: item.file,
    name: item.name,
    ...created(item.created),
    usedBy: uses(item.usedBy),
    ...actions(item.actions),
    ...(typeof item.problem === "string" ? { problem: item.problem } : {}),
    ...(item.invalidName === true ? { invalidName: true } : {}),
  };
}

function missingMacro(value: unknown): MissingMacro {
  const item = object(value);
  if (typeof item.file !== "string") throw invalidResponse();
  return {
    file: item.file,
    usedBy: uses(item.usedBy),
    ...actions(item.actions),
  };
}

function legacyScript(value: unknown): LegacyScript {
  const item = object(value);
  if (
    typeof item.profile !== "string" ||
    typeof item.profileName !== "string" ||
    typeof item.entry !== "string"
  )
    throw invalidResponse();
  return {
    profile: item.profile,
    profileName: item.profileName,
    entry: item.entry,
  };
}

function macroList(value: unknown): MacroList {
  const list = object(value);
  if (!Array.isArray(list.macros) || !Array.isArray(list.missing))
    throw invalidResponse();
  if (list.legacy !== undefined && !Array.isArray(list.legacy))
    throw invalidResponse();
  return {
    macros: list.macros.map(macroInfo),
    missing: list.missing.map(missingMacro),
    legacy: (list.legacy ?? []).map(legacyScript),
  };
}

// A profile.json an edit rewrote: its revision and the actions changed.
function profileEdit(value: unknown): ProfileEdit {
  const result = object(value);
  if (
    typeof result.revision !== "string" ||
    !Number.isSafeInteger(result.bindings)
  )
    throw invalidResponse();
  return { revision: result.revision, bindings: result.bindings as number };
}

// An edit of the library (macros.rename, macros.delete): every profile it
// wrote.
function macroEdit(value: unknown): MacroEdit {
  const result = object(value);
  if (!Number.isSafeInteger(result.bindings) || !Array.isArray(result.profiles))
    throw invalidResponse();
  return {
    bindings: result.bindings as number,
    profiles: result.profiles.map((item) => {
      const profile = object(item);
      return { id: validateProfileID(profile.id), ...profileEdit(profile) };
    }),
  };
}

// jrpc2's code for a daemon error that it does not classify, such as a
// failed write. Its message can name the profile folder's absolute path.
export const daemonSystemError = -32098;

// Node's fs errors name the path, which may hold the Windows user name;
// errors that reach the renderer must not (uiResult copies the message).
// Each fs call of these handlers goes through quiet, which replaces such an
// error with problem().
async function quiet<T>(
  run: () => Promise<T>,
  problem: () => Error,
): Promise<T> {
  try {
    return await run();
  } catch {
    throw problem();
  }
}

const unreadable = () =>
  scriptImportProblem(
    "read",
    "The chosen file could not be read; it may have been moved, locked or taken offline",
  );

// Reads the chosen script, at most maxScriptBytes of UTF-8. A byte order
// mark is kept, so the copy has the source's bytes.
export async function readScriptFile(path: string): Promise<string> {
  const handle = await quiet(() => open(path, "r"), unreadable);
  try {
    const info = await quiet(() => handle.stat(), unreadable);
    if (!info.isFile())
      throw scriptImportProblem("type", "Only a file can be imported");
    const tooLarge = () =>
      scriptImportProblem("size", "Scripts are limited to 1 MiB");
    if (info.size > maxScriptBytes) throw tooLarge();
    // Read one byte more than allowed: the file may have grown since stat.
    const buffer = Buffer.alloc(maxScriptBytes + 1);
    let length = 0;
    while (length < buffer.length) {
      const { bytesRead } = await quiet(
        () => handle.read(buffer, length, buffer.length - length, length),
        unreadable,
      );
      if (bytesRead === 0) break;
      length += bytesRead;
    }
    if (length > maxScriptBytes) throw tooLarge();
    try {
      return new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(
        buffer.subarray(0, length),
      );
    } catch {
      throw scriptImportProblem("encoding", "Scripts must be UTF-8 text");
    }
  } finally {
    // The file was only read; a failed close loses nothing.
    await handle.close().catch(() => undefined);
  }
}

// The folder of profile id in a path the daemon gave: the nearest
// ancestor <...>\profiles\<id>.
function profileFolder(path: string, id: string): string | undefined {
  for (let dir = dirname(path); dir !== dirname(dir); dir = dirname(dir))
    if (basename(dir) === id && basename(dirname(dir)) === "profiles")
      return dir;
  return undefined;
}

// Whether two absolute folders are the same one (Windows paths ignore case).
function sameFolder(a: string, b: string): boolean {
  const [x, y] = [resolve(a), resolve(b)];
  return process.platform === "win32"
    ? x.toLowerCase() === y.toLowerCase()
    : x === y;
}

// Checks the path the daemon gave for an editor: absolute and normal, and
// either the file `file` of the script library (<data>/macros/<file>) or,
// for the legacy script of profile id, a script file in the profile folder
// outside .history. A macros folder inside a profile's folder, the old
// layout <...>/profiles/<id>/macros, is not the library, unless <...> is
// the data folder itself.
function checkedPath(
  path: unknown,
  id?: string,
  file?: string,
  dataDir?: string,
): string {
  if (typeof path !== "string" || !isAbsolute(path) || resolve(path) !== path)
    throw invalidResponse();
  if (file !== undefined) {
    const folder = dirname(path);
    const library =
      dataDir !== undefined && sameFolder(dirname(folder), dataDir);
    if (
      basename(path) !== file ||
      basename(folder) !== "macros" ||
      (!library && basename(dirname(dirname(folder))) === "profiles")
    )
      throw invalidResponse();
    return path;
  }
  const folder = profileFolder(path, id as string);
  if (!folder) throw invalidResponse();
  const inside = relative(folder, path).split(sep);
  if (inside[0].toLowerCase() === ".history" || !/\.[cm]?[jt]sx?$/i.test(path))
    throw invalidResponse();
  return path;
}

/**
 * The scripts page's script library (SCR-50, SCR-51, design §10). The
 * renderer supplies file names (and a profile ID for a legacy script)
 * only: Main picks and reads an imported file itself, and the paths the
 * daemon names for an editor never leave Main.
 */
export function macroHandlers(
  options: MacroHandlerOptions,
): Record<string, EditorHandler> {
  const now = options.now ?? Date.now;
  const pending = new Map<string, PendingImport>();
  let choosing = false;
  const request = async (method: string, params: unknown) => {
    const call = options.call(method, params);
    if (!call) throw new Error("Daemon is not connected");
    try {
      return await call;
    } catch (error) {
      // The daemon logs the full error.
      if ((error as { code?: unknown })?.code === daemonSystemError)
        throw Object.assign(
          new Error(
            "padmate could not use the script folder; the daemon log has the details",
          ),
          { code: daemonSystemError },
        );
      throw error;
    }
  };
  const prune = () => {
    const time = now();
    for (const [token, item] of pending)
      if (item.expires <= time) pending.delete(token);
  };
  const write = async (
    item: Omit<PendingImport, "expires">,
    replace: boolean,
  ): Promise<MacroImportResult> => {
    const result = object(
      await request("macros.import", {
        name: item.name,
        text: item.text,
        replace,
      }),
    );
    if (typeof result.file !== "string" || typeof result.replaced !== "boolean")
      throw invalidResponse();
    return { file: result.file, replaced: result.replaced };
  };
  // Keeps the file for a confirmed replace; the oldest pending file goes
  // first when too many wait.
  const keep = (item: Omit<PendingImport, "expires">): string => {
    prune();
    while (pending.size >= maxPendingImports)
      pending.delete(pending.keys().next().value as string);
    const token = randomBytes(16).toString("hex");
    pending.set(token, { ...item, expires: now() + pendingImportTTL });
    return token;
  };
  const edit = async (path: string) => {
    // The file may be deleted or replaced by a link after the daemon named
    // it; the error must not name it.
    const info = await quiet(
      () => lstat(path),
      () => new Error("The file is not available"),
    );
    if (!info.isFile()) throw new Error("The file is not a regular file");
    return { opened: await options.openEditor(path).catch(() => false) };
  };
  return {
    macrosList: {
      count: 0,
      run: async (): Promise<MacroList> =>
        macroList(await request("macros.list", {})),
    },
    macroCreate: {
      count: 1,
      run: async (name): Promise<MacroCreated> => {
        const params = { name: validateMacroName(name) };
        const { file } = object(await request("macros.create", params));
        if (typeof file !== "string" || checkMacroFile(file))
          throw invalidResponse();
        return { file };
      },
    },
    macroImport: {
      count: 1,
      run: async (value): Promise<MacroImportResult> => {
        const confirm = validateScriptImportOptions(value);
        prune();
        if (confirm) {
          const item = pending.get(confirm.token);
          // A token works once.
          pending.delete(confirm.token);
          if (!item)
            throw scriptImportProblem(
              "expired",
              "Choose the script file again",
            );
          return write(item, true);
        }
        if (choosing) throw new Error("An import dialog is already open");
        choosing = true;
        let path: string | undefined;
        try {
          path = await options.chooseScript();
        } finally {
          choosing = false;
        }
        if (path === undefined) return { canceled: true };
        const name = macroImportName(basename(path));
        const item = { name, text: await readScriptFile(path) };
        try {
          return await write(item, false);
        } catch (error) {
          const failure = error as { code?: unknown; data?: unknown };
          if (failure?.code !== LibraryErrorCode.scriptExists) throw error;
          const data =
            failure.data && typeof failure.data === "object"
              ? (failure.data as Record<string, unknown>)
              : {};
          // The existing script's exact name, which a replace keeps.
          const existing = typeof data.name === "string" ? data.name : name;
          return data.replaceable === true
            ? { exists: true, name: existing, token: keep(item) }
            : { exists: true, name: existing };
        }
      },
    },
    macroRename: {
      count: 2,
      run: async (file, to): Promise<MacroRenamed> => {
        const params = {
          file: validateMacroCandidate(file),
          to: validateMacroFile(to),
        };
        const result = object(await request("macros.rename", params));
        if (typeof result.file !== "string") throw invalidResponse();
        return { file: result.file, ...macroEdit(result) };
      },
    },
    macroDelete: {
      count: 1,
      run: async (file): Promise<MacroEdit> =>
        macroEdit(
          await request("macros.delete", {
            file: validateMacroCandidate(file),
          }),
        ),
    },
    scriptDetach: {
      count: 1,
      run: async (id): Promise<ProfileEdit> =>
        profileEdit(
          await request("script.detach", { id: validateProfileID(id) }),
        ),
    },
    macroEdit: {
      count: 1,
      run: async (file): Promise<{ opened: boolean }> => {
        const name = validateMacroFile(file);
        const { path } = object(await request("macros.path", { file: name }));
        return edit(checkedPath(path, undefined, name, options.dataDir));
      },
    },
    scriptEdit: {
      count: 1,
      run: async (id): Promise<{ opened: boolean }> => {
        const profile = validateProfileID(id);
        const { path } = object(await request("script.path", { id: profile }));
        return edit(checkedPath(path, profile));
      },
    },
  };
}
