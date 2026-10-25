import { randomBytes } from "node:crypto";
import { open } from "node:fs/promises";
import { basename, extname } from "node:path";
import {
  type AmgpImported,
  type AmgpReportLine,
  type AmgpSeverity,
  LibraryErrorCode,
  type ProfileImported,
  type ProfileImportResult,
  type ScriptClash,
} from "../shared/api";
import type { DaemonCall, EditorHandler } from "./library-handlers";
import { daemonSystemError } from "./macro-handlers";
import {
  archiveData,
  archiveFileName,
  maxArchiveBytes,
  profileFileProblem,
  validateProfileID,
  validateProfileImportOptions,
} from "./security";

export interface ArchiveHandlerOptions {
  call: DaemonCall;
  // Shows the native save dialog for a .padmate file, suggesting name;
  // resolves to the chosen path, or undefined when the user cancels.
  chooseExport: (name: string) => Promise<string | undefined>;
  // Shows the native open dialog for one .padmate file; resolves to its
  // path, or undefined when the user cancels.
  chooseImport: () => Promise<string | undefined>;
  // Writes the archive to the path the user chose.
  writeFile: (path: string, data: Buffer) => Promise<void>;
  now?: () => number;
}

// What an earlier answer decided about the profile: another profile to
// replace, a new name, or nothing yet.
type Target = { replace: string } | { name: string } | Record<string, never>;

// A file read for an import that needs the user's answer first, kept in
// Main only, keyed by a random token. data is the archive in base64;
// target is the answer already given about the profile, which a later
// answer about the scripts builds on.
interface PendingImport {
  data: string;
  target: Target;
  expires: number;
}

export const pendingArchiveTTL = 2 * 60 * 1000;
const maxPendingImports = 4;

// An AntiMicroX profile (PRO-21), as the daemon's amgp.MaxBytes: Main refuses
// a larger file before reading it. These extensions choose that import.
export const maxAmgpBytes = 1024 * 1024;
const amgpExtensions = new Set([".amgp", ".xml"]);
// The most lines of report Main passes on (the daemon makes at most 1001).
const maxReportLines = 2000;

function object(value: unknown): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value))
    throw new Error("Invalid daemon response");
  return value as Record<string, unknown>;
}

// Node's fs errors name the path, which may hold the Windows user name;
// errors that reach the renderer must not, so each fs call goes through
// quiet.
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
  profileFileProblem(
    "read",
    "The chosen file could not be read; it may have been moved, locked or taken offline",
  );
const tooLarge = () =>
  profileFileProblem("size", "Profile files are limited to 4 MiB");

// The refusal the daemon gives an AntiMicroX file over its limit
// (profiles.importAmgp, reason "size"), made here before the file is read.
const amgpTooLarge = () =>
  Object.assign(new Error("文件超过 1 MiB"), {
    code: LibraryErrorCode.amgpInvalid,
    data: { reason: "size" },
  });

// Reads the chosen file, at most limit bytes: its size is checked before
// reading, and the read stops one byte past the limit in case the file grew
// since.
async function readLimited(
  path: string,
  limit: number,
  large: () => Error,
): Promise<Buffer> {
  const handle = await quiet(() => open(path, "r"), unreadable);
  try {
    const info = await quiet(() => handle.stat(), unreadable);
    if (!info.isFile()) throw unreadable();
    if (info.size > limit) throw large();
    const buffer = Buffer.alloc(limit + 1);
    let length = 0;
    while (length < buffer.length) {
      const { bytesRead } = await quiet(
        () => handle.read(buffer, length, buffer.length - length, length),
        unreadable,
      );
      if (bytesRead === 0) break;
      length += bytesRead;
    }
    if (length > limit) throw large();
    return buffer.subarray(0, length);
  } finally {
    // The file was only read; a failed close loses nothing.
    await handle.close().catch(() => undefined);
  }
}

// Reads the chosen archive, at most maxArchiveBytes.
export function readArchiveFile(path: string): Promise<Buffer> {
  return readLimited(path, maxArchiveBytes, tooLarge);
}

const severities = new Set<string>(["dropped", "approximate", "info"]);

// One line of the daemon's import report, checked field by field.
function reportLine(value: unknown): AmgpReportLine {
  const line = object(value);
  if (
    typeof line.severity !== "string" ||
    !severities.has(line.severity) ||
    typeof line.code !== "string" ||
    typeof line.message !== "string" ||
    (line.set !== undefined && !Number.isSafeInteger(line.set)) ||
    (line.slot !== undefined && !Number.isSafeInteger(line.slot)) ||
    (line.control !== undefined && typeof line.control !== "string")
  )
    throw new Error("Invalid daemon response");
  return {
    severity: line.severity as AmgpSeverity,
    code: line.code,
    ...(line.set === undefined ? {} : { set: line.set as number }),
    ...(line.control === undefined ? {} : { control: line.control as string }),
    ...(line.slot === undefined ? {} : { slot: line.slot as number }),
    message: line.message,
  };
}

// The daemon's profiles.importAmgp answer, checked field by field.
function amgpImported(value: unknown): AmgpImported {
  const result = object(value);
  if (
    typeof result.name !== "string" ||
    typeof result.revision !== "string" ||
    typeof result.applied !== "boolean" ||
    (result.problem !== undefined && typeof result.problem !== "string") ||
    !Array.isArray(result.report) ||
    result.report.length > maxReportLines
  )
    throw new Error("Invalid daemon response");
  return {
    amgp: true,
    id: validateProfileID(result.id),
    name: result.name,
    revision: result.revision,
    applied: result.applied,
    ...(typeof result.problem === "string" ? { problem: result.problem } : {}),
    report: result.report.map(reportLine),
  };
}

function imported(value: unknown): ProfileImported {
  const result = object(value);
  if (
    typeof result.name !== "string" ||
    typeof result.revision !== "string" ||
    typeof result.applied !== "boolean" ||
    (result.problem !== undefined && typeof result.problem !== "string")
  )
    throw new Error("Invalid daemon response");
  return {
    id: validateProfileID(result.id),
    name: result.name,
    revision: result.revision,
    applied: result.applied,
    ...(typeof result.problem === "string" ? { problem: result.problem } : {}),
    ...(result.macros === undefined
      ? {}
      : { macros: importedMacros(result.macros) }),
  };
}

// What became of each script of the archive (profiles.import's macros).
function importedMacros(
  value: unknown,
): NonNullable<ProfileImported["macros"]> {
  if (!Array.isArray(value)) throw new Error("Invalid daemon response");
  return value.map((item) => {
    const macro = object(item);
    if (
      typeof macro.file !== "string" ||
      typeof macro.name !== "string" ||
      (macro.same !== undefined && typeof macro.same !== "boolean") ||
      (macro.replaced !== undefined && typeof macro.replaced !== "boolean")
    )
      throw new Error("Invalid daemon response");
    return {
      file: macro.file,
      name: macro.name,
      ...(macro.same === true ? { same: true } : {}),
      ...(macro.replaced === true ? { replaced: true } : {}),
    };
  });
}

// The scripts of an archive that clash with the library's
// (rpcCodeMacroConflict's data).
function scriptClashes(value: unknown): ScriptClash[] {
  const conflicts = object(value).conflicts;
  if (!Array.isArray(conflicts) || conflicts.length === 0)
    throw new Error("Invalid daemon response");
  return conflicts.map((item) => {
    const clash = object(item);
    if (
      typeof clash.file !== "string" ||
      typeof clash.existing !== "string" ||
      typeof clash.replaceable !== "boolean"
    )
      throw new Error("Invalid daemon response");
    return {
      file: clash.file,
      existing: clash.existing,
      replaceable: clash.replaceable,
    };
  });
}

// Backup of one profile (PRO-20): 导出… and 导入… on the profiles page.
// The daemon makes and checks the archive; Main shows the dialogs and
// moves at most maxArchiveBytes between the file and the daemon. The
// renderer supplies a profile ID, a name and a token, never a path. The same
// 导入… takes an AntiMicroX .amgp or .xml file (PRO-21), chosen by its
// extension: Main reads it (at most maxAmgpBytes) and the daemon converts it.
export function archiveHandlers(
  options: ArchiveHandlerOptions,
): Record<string, EditorHandler> {
  const now = options.now ?? Date.now;
  const pending = new Map<string, PendingImport>();
  // One file dialog at a time.
  let choosing = false;
  const request = async (method: string, params: unknown) => {
    const call = options.call(method, params);
    if (!call) throw new Error("Daemon is not connected");
    try {
      return await call;
    } catch (error) {
      // Its message can name a path in the library; the daemon logs it.
      if ((error as { code?: unknown })?.code === daemonSystemError)
        throw Object.assign(
          new Error(
            "padmate could not use the profile folder; the daemon log has the details",
          ),
          { code: daemonSystemError },
        );
      throw error;
    }
  };
  const choose = async <T>(run: () => Promise<T>): Promise<T> => {
    if (choosing) throw new Error("A file dialog is already open");
    choosing = true;
    try {
      return await run();
    } finally {
      choosing = false;
    }
  };
  const prune = () => {
    const time = now();
    for (const [token, item] of pending)
      if (item.expires <= time) pending.delete(token);
  };
  // Keeps the file for the answer; the oldest one goes when too many wait.
  const keep = (data: string, target: Target): string => {
    prune();
    while (pending.size >= maxPendingImports)
      pending.delete(pending.keys().next().value as string);
    const token = randomBytes(16).toString("hex");
    pending.set(token, { data, target, expires: now() + pendingArchiveTTL });
    return token;
  };
  const add = async (
    data: string,
    target: Target,
    macros?: "replace" | "keep",
  ): Promise<ProfileImportResult> => {
    try {
      return imported(
        await request("profiles.import", {
          data,
          ...target,
          ...(macros ? { macros } : {}),
        }),
      );
    } catch (error) {
      const e = error as { code?: unknown; data?: unknown };
      if (e?.code === LibraryErrorCode.macroConflict)
        // Nothing is written yet; the answer comes with the profile's.
        return {
          conflict: true,
          scripts: scriptClashes(e.data),
          token: keep(data, target),
        };
      if (e?.code !== LibraryErrorCode.profileNameTaken) throw error;
      const clash = object(e.data);
      if (typeof clash.name !== "string")
        throw new Error("Invalid daemon response");
      return {
        exists: true,
        id: validateProfileID(clash.id),
        name: clash.name,
        token: keep(data, {}),
      };
    }
  };
  return {
    profileExport: {
      count: 2,
      run: async (id, name): Promise<{ saved: boolean }> => {
        const profile = validateProfileID(id);
        const file = archiveFileName(name);
        if (choosing) throw new Error("A file dialog is already open");
        const result = object(
          await request("profiles.export", { id: profile }),
        );
        const data = archiveData(result.data);
        const path = await choose(() => options.chooseExport(file));
        if (path === undefined) return { saved: false };
        await quiet(
          () => options.writeFile(path, data),
          () =>
            profileFileProblem(
              "write",
              "The file could not be saved; choose another folder",
            ),
        );
        return { saved: true };
      },
    },
    profileImport: {
      count: 1,
      run: async (value): Promise<ProfileImportResult> => {
        const answer = validateProfileImportOptions(value);
        prune();
        if (answer) {
          const item = pending.get(answer.token);
          // A token works once.
          pending.delete(answer.token);
          if (!item)
            throw profileFileProblem("expired", "Choose the file again");
          if ("macros" in answer)
            return add(item.data, item.target, answer.macros);
          return add(
            item.data,
            "replace" in answer
              ? { replace: answer.replace }
              : { name: answer.name },
          );
        }
        const path = await choose(() => options.chooseImport());
        if (path === undefined) return { canceled: true };
        if (amgpExtensions.has(extname(path).toLowerCase())) {
          const data = (
            await readLimited(path, maxAmgpBytes, amgpTooLarge)
          ).toString("base64");
          return amgpImported(
            await request("profiles.importAmgp", {
              data,
              fileName: [...basename(path)].slice(0, 128).join(""),
            }),
          );
        }
        const data = (await readArchiveFile(path)).toString("base64");
        return add(data, {});
      },
    },
  };
}
