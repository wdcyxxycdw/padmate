import type { ProfileFileProblem, ScriptImportProblem } from "../shared/api";
import {
  checkMacroFile,
  isMacroCandidate,
  type MacroNameKind,
  maxMacroName,
} from "../shared/macro-names";

export function contentSecurityPolicy(development: boolean): string {
  return [
    "default-src 'self'",
    "script-src 'self'",
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data: blob:",
    "font-src 'self'",
    "worker-src 'self' blob:",
    development ? "connect-src 'self' ws://127.0.0.1:*" : "connect-src 'none'",
    "object-src 'none'",
    "frame-src 'none'",
    "base-uri 'none'",
    "form-action 'none'",
  ].join("; ");
}

/**
 * The web preferences every padmate window has (ADR-0039, ADR-0063): a
 * sandboxed renderer without Node, isolated from its preload. The main
 * window adds its preload; the overlay adds its own, toast-only one
 * (preload/overlay.ts).
 */
export const baseWebPreferences = {
  contextIsolation: true,
  sandbox: true,
  nodeIntegration: false,
  webSecurity: true,
  allowRunningInsecureContent: false,
} as const;

export function validateToggle(value: unknown): boolean {
  if (typeof value !== "boolean") throw new Error("Expected a boolean setting");
  return value;
}

export function validateConfigWrite(
  text: unknown,
  revision: unknown,
): { text: string; revision: string } {
  if (
    typeof text !== "string" ||
    Buffer.byteLength(text, "utf8") > 4 * 1024 * 1024
  )
    throw new Error("Config text must be at most 4 MiB");
  return { text, revision: validateRevision(revision) };
}

// A revision is the SHA-256 of the stored bytes, or "" for a file that does
// not exist yet.
export function validateRevision(value: unknown): string {
  if (
    typeof value !== "string" ||
    (value !== "" && !/^[a-f0-9]{64}$/.test(value))
  )
    throw new Error("Invalid revision");
  return value;
}

export function validateProfileID(value: unknown): string {
  if (
    typeof value !== "string" ||
    !/^[a-z0-9][a-z0-9_-]{0,63}$/.test(value) ||
    /^(con|prn|aux|nul|com[1-9]|lpt[1-9])$/.test(value)
  )
    throw new Error("Invalid profile ID");
  return value;
}
// The executable whose icon the profiles page shows (UI-42): an absolute
// path on a local drive ("C:\Apps\paint.exe", slashes either way) to an
// .exe. A UNC or device path is refused, so reading an icon never reaches
// the network, and so are relative parts, wildcards and stream names.
export function validateAppIconPath(value: unknown): string {
  if (typeof value !== "string" || value.length > 1024)
    throw new Error("Invalid app path");
  const path = value.replaceAll("/", "\\");
  const parts = path.split("\\");
  if (
    !/^[A-Za-z]:\\[^\\]/.test(path) ||
    !/\.exe$/i.test(path) ||
    /["*?<>|]/.test(path) ||
    [...path].some((character) => character.charCodeAt(0) < 32) ||
    // A colon past the drive names a stream.
    path.indexOf(":", 2) !== -1 ||
    parts.some((part) => part === "" || part === "." || part === "..")
  )
    throw new Error("Invalid app path");
  return path;
}
// The file name of an executable as a rule names it ("krita.exe"), which the
// renderer asks Main to find a path for (UI-42): no folder, drive, wildcard
// or stream, so the name can only ever select an App Paths key, a MuiCache
// entry or a Start Menu program.
export function validateExeName(value: unknown): string {
  if (
    typeof value !== "string" ||
    value.length > 255 ||
    value !== value.trim() ||
    !/^[^\\/:*?"<>|]+\.exe$/i.test(value) ||
    [...value].some((character) => character.charCodeAt(0) < 32)
  )
    throw new Error("Invalid exe name");
  return value;
}
// Only profile activation accepts null, which disables mapping.
export function validateProfileSelection(value: unknown): string | null {
  return value === null ? null : validateProfileID(value);
}
export function validateControl(value: unknown): string {
  if (typeof value !== "string" || !/^[a-z][a-z0-9.+_-]{0,63}$/.test(value))
    throw new Error("Invalid control");
  return value;
}
export function validateDevice(value: unknown): string {
  if (value === undefined) return "";
  if (
    typeof value !== "string" ||
    value.length > 1024 ||
    [...value].some((character) => character.charCodeAt(0) < 32)
  )
    throw new Error("Invalid device selector");
  return value;
}
// A selector that names one controller, as devices.identify needs: a
// connection ID or a DeviceKey, never empty.
export function validateDeviceKey(value: unknown): string {
  const key = validateDevice(value);
  if (key === "") throw new Error("Invalid device selector");
  return key;
}
// An SDL gamepad mapping string for devices.setMapping (IN-18, ADR-0072): at
// most the 2048 characters the daemon accepts (sdlmapping.MaxLen), printable
// ASCII. The daemon parses it, normalises it and checks its GUID against the
// controller's; this only keeps what is not a mapping out of the request.
export function validateMapping(value: unknown): string {
  if (
    typeof value !== "string" ||
    value.length === 0 ||
    value.length > 2048 ||
    [...value].some(
      (character) =>
        character.charCodeAt(0) < 32 || character.charCodeAt(0) > 126,
    )
  )
    throw new Error("Invalid mapping");
  return value;
}
export function validateExport(
  format: unknown,
  content: unknown,
): { extension: "md" | "png"; data: Buffer } {
  if (typeof content !== "string" || content.length > 16 * 1024 * 1024)
    throw new Error("Export exceeds limits");
  if (format === "markdown")
    return { extension: "md", data: Buffer.from(content, "utf8") };
  if (
    format !== "png" ||
    !/^data:image\/png;base64,[A-Za-z0-9+/]*={0,2}$/.test(content)
  )
    throw new Error("Invalid PNG export");
  const data = Buffer.from(
    content.slice("data:image/png;base64,".length),
    "base64",
  );
  if (
    !data.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]))
  )
    throw new Error("Invalid PNG signature");
  return { extension: "png", data };
}

// Scripts that Main imports into the script library (macros.import).
export const maxScriptBytes = 1024 * 1024;

// A refusal of the chosen script file that the renderer can translate:
// data.reason survives the UIResult envelope.
export function scriptImportProblem(
  reason: ScriptImportProblem,
  message: string,
): Error {
  return Object.assign(new Error(message), { data: { reason } });
}

// A refused macro name the renderer can translate: data.kind is the rule
// it breaks, as the daemon reports it (InvalidParams with data { kind }).
function macroNameProblem(kind: MacroNameKind): Error {
  return Object.assign(new Error("Invalid macro name"), { data: { kind } });
}

// A macro file name the daemon accepts (profile.CheckMacroFile), with its
// lowercase .ts or .js extension.
export function validateMacroFile(value: unknown): string {
  if (typeof value !== "string") throw macroNameProblem("extension");
  const kind = checkMacroFile(value);
  if (kind) throw macroNameProblem(kind);
  return value;
}

// A new macro's name, without the extension: "<name>.ts" is created.
export function validateMacroName(value: unknown): string {
  if (typeof value !== "string") throw macroNameProblem("empty");
  const kind = checkMacroFile(`${value}.ts`);
  if (kind) throw macroNameProblem(kind);
  return value;
}

// An existing macro file as macros.list names it, which may be a name the
// daemon refuses (to rename or delete it): a bare .ts or .js name in any
// case, not a .d.ts, without separators, colons or control characters.
// The daemon still requires it to be listed exactly.
export function validateMacroCandidate(value: unknown): string {
  if (
    typeof value !== "string" ||
    value.length > 255 ||
    !isMacroCandidate(value) ||
    /[\\/:]/.test(value) ||
    [...value].some((c) => c.charCodeAt(0) < 32 || c.charCodeAt(0) === 127)
  )
    throw new Error("Invalid macro file");
  return value;
}

// Characters a macro name cannot have (checkMacroFile's "char"): the ones
// Windows refuses, ".", "~" and anything not visible but the plain space.
const notInName = /[\\/:*?"<>|.~]|[^\p{L}\p{M}\p{N}\p{P}\p{S} ]/u;

// Names an imported macro after the chosen file's base name: NFC, the
// extension lowercased, every run of whitespace one space, every run of
// characters a name cannot have one "-", spaces trimmed from both ends and
// "-" from the start, at most 40 characters ("v1.2 (copy).TS" becomes
// "v1-2 (copy).ts"). A name that still breaks the daemon's rule, such as a
// Windows device name, is refused, never invented.
export function macroImportName(base: string): string {
  const dot = base.lastIndexOf(".");
  const extension = dot > 0 ? base.slice(dot).toLowerCase() : "";
  if (extension !== ".ts" && extension !== ".js")
    throw scriptImportProblem("type", "Only .ts and .js files can be imported");
  if (/\.d\.ts$/i.test(base))
    throw scriptImportProblem(
      "declaration",
      "A .d.ts declaration file cannot be imported as a script",
    );
  let stem = [...base.slice(0, dot).normalize("NFC").replace(/\s+/gu, " ")]
    .map((character) => (notInName.test(character) ? "-" : character))
    .join("")
    .replace(/-{2,}/g, "-")
    .replace(/^[ -]+/, "")
    .trimEnd();
  stem = [...stem].slice(0, maxMacroName).join("").trimEnd();
  if (checkMacroFile(`${stem}${extension}`))
    throw scriptImportProblem(
      "name",
      "Rename the file (not a Windows device name) and import it again",
    );
  return `${stem}${extension}`;
}

// A profile archive (PRO-20), as the daemon's profilelib.MaxArchiveBytes:
// Main refuses a larger file before reading it, and a larger export.
export const maxArchiveBytes = 4 * 1024 * 1024;
export const archiveExtension = "padmate";

// A refusal of a profile file that the renderer can translate:
// data.reason (ProfileFileProblem) survives the UIResult envelope.
export function profileFileProblem(
  reason: ProfileFileProblem,
  message: string,
): Error {
  return Object.assign(new Error(message), { data: { reason } });
}

// The file name an export suggests: the profile's name with what Windows
// does not allow in a file name replaced by "-", trimmed, at most 64
// characters, never a device name; "profile" when nothing is left. The
// user still chooses the folder and may change it.
export function archiveFileName(name: unknown): string {
  if (typeof name !== "string" || name.length > 1024)
    throw new Error("Invalid file name");
  const stem = [
    ...name
      .normalize("NFC")
      // biome-ignore lint/suspicious/noControlCharactersInRegex: Control characters are what it removes.
      .replace(/[<>:"/\\|?*\u0000-\u001f\u007f]+/g, "-"),
  ]
    .slice(0, 64)
    .join("")
    .trim()
    .replace(/[. ]+$/, "")
    .trim();
  const device =
    /^(con|prn|aux|nul|conin\$|conout\$|com[0-9¹²³]|lpt[0-9¹²³])(\.|$)/i;
  return `${stem && !device.test(stem) ? stem : "profile"}.${archiveExtension}`;
}

// The archive profiles.export returns: standard padded base64 of at most
// maxArchiveBytes that starts as a zip file does.
export function archiveData(value: unknown): Buffer {
  if (
    typeof value !== "string" ||
    value.length % 4 !== 0 ||
    value.length > Math.ceil(maxArchiveBytes / 3) * 4 ||
    !/^[A-Za-z0-9+/]*={0,2}$/.test(value)
  )
    throw new Error("Invalid daemon response");
  const data = Buffer.from(value, "base64");
  if (
    data.length > maxArchiveBytes ||
    !data.subarray(0, 4).equals(Buffer.from([0x50, 0x4b, 0x03, 0x04]))
  )
    throw new Error("Invalid daemon response");
  return data;
}

// importProfile's second call: the token of the file Main read in the
// first, and either the profile it replaces, a new name (1-64 characters
// once trimmed, as the daemon checks again) or, after scripts clashed with
// the library's, what to do with them. Anything else is the first call.
export function validateProfileImportOptions(
  value: unknown,
):
  | { token: string; replace: string }
  | { token: string; name: string }
  | { token: string; macros: "replace" | "keep" }
  | undefined {
  if (value === undefined || value === null) return undefined;
  if (typeof value !== "object" || Array.isArray(value))
    throw new Error("Invalid profile import options");
  const options = value as Record<string, unknown>;
  const keys = Object.keys(options).sort().join();
  if (
    typeof options.token !== "string" ||
    !/^[a-f0-9]{32}$/.test(options.token)
  )
    throw new Error("Invalid profile import options");
  if (keys === "replace,token")
    return {
      token: options.token,
      replace: validateProfileID(options.replace),
    };
  if (
    keys === "name,token" &&
    typeof options.name === "string" &&
    options.name.trim() !== "" &&
    [...options.name.trim()].length <= 64
  )
    return { token: options.token, name: options.name };
  if (
    keys === "macros,token" &&
    (options.macros === "replace" || options.macros === "keep")
  )
    return { token: options.token, macros: options.macros };
  throw new Error("Invalid profile import options");
}

// importMacro's second call carries the token of the file Main read in
// the first; anything else is the first call.
export function validateScriptImportOptions(
  value: unknown,
): { token: string } | undefined {
  if (value === undefined || value === null) return undefined;
  if (typeof value !== "object" || Array.isArray(value))
    throw new Error("Invalid script import options");
  const options = value as Record<string, unknown>;
  if (
    Object.keys(options).sort().join() !== "replace,token" ||
    options.replace !== true ||
    typeof options.token !== "string" ||
    !/^[a-f0-9]{32}$/.test(options.token)
  )
    throw new Error("Invalid script import options");
  return { token: options.token };
}

// The backup of a repaired config.json (PRO-29), as the daemon names it in
// the data folder: config.broken-YYYYMMDD-HHMMSS.json, with -2, -3 when
// that name is taken. Only this form crosses from the renderer; Main joins
// it to its own data folder, so no path does.
export function validateConfigBackup(value: unknown): string {
  if (
    typeof value !== "string" ||
    !/^config\.broken-\d{8}-\d{6}(-\d{1,4})?\.json$/.test(value)
  )
    throw new Error("Invalid backup name");
  return value;
}
