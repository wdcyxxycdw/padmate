// Macro file names (SCR-50, ADR-0055): the rule of the daemon's
// profile.CheckMacroFile, which Main and the renderer share. The cases in
// daemon/testdata/macro-names.json are checked by both.

/** Why a macro file name is refused: the first rule it breaks. */
export type MacroNameKind =
  | "extension"
  | "empty"
  | "length"
  | "char"
  | "invalid";

/** The most characters (code points) a macro's name may have. */
export const maxMacroName = 40;
/** The most scripts the library may hold (SCR-51: all profiles together). */
export const maxMacros = 64;

// Windows refuses \ / : * ? " < > |; "." would allow .d.ts, hidden files
// and a trailing dot Windows drops; "~" could match an 8.3 short name.
const forbidden = new Set([...'\\/:*?"<>|.~']);
// Go's unicode.IsGraphic without the spaces other than U+0020: letters,
// marks, numbers, punctuation, symbols and the plain space.
const graphic = /^[\p{L}\p{M}\p{N}\p{P}\p{S} ]$/u;
const loneSurrogate = /\p{Cs}/u;
// Windows device names, compared like the daemon, which lowers the case
// with Go's strings.ToLower: the regular expression ignores ASCII case
// only, and Go also makes the dotted İ an i (deviceStem).
const deviceName =
  /^(?:con|prn|aux|nul|conin\$|conout\$|com[0-9¹²³]|lpt[0-9¹²³])$/i;
const deviceStem = (stem: string) => stem.replaceAll("İ", "i");

/**
 * A macro's name: its file name without the .ts or .js extension, in any
 * case: a listed file with an upper-case extension has the name of the
 * file it would replace on Windows.
 */
export function macroName(file: string): string {
  return file.replace(/\.(ts|js)$/i, "");
}

/**
 * The first rule the macro file name breaks, or "" for a usable name:
 * "<name>.ts" or "<name>.js", the name 1 to 40 characters in NFC, each
 * visible and none of \ / : * ? " < > | . ~, not starting with a space or
 * "-", not ending with a space and not a Windows device name (CON,
 * CONIN$, COM¹ and the like).
 */
export function checkMacroFile(file: string): MacroNameKind | "" {
  if (!file.endsWith(".ts") && !file.endsWith(".js")) return "extension";
  const stem = file.slice(0, -3);
  if (stem === "") return "empty";
  if (loneSurrogate.test(stem)) return "char";
  if (stem.normalize("NFC") !== stem) return "invalid";
  const characters = [...stem];
  if (characters.length > maxMacroName) return "length";
  for (const character of characters)
    if (forbidden.has(character) || !graphic.test(character)) return "char";
  if (stem.startsWith(" ") || stem.startsWith("-") || stem.endsWith(" "))
    return "invalid";
  if (deviceName.test(deviceStem(stem))) return "invalid";
  return "";
}

/**
 * Whether two macro files have the same name, ignoring case and the
 * extension: a profile cannot have both.
 */
export function sameMacroName(a: string, b: string): boolean {
  return macroName(a).toLowerCase() === macroName(b).toLowerCase();
}

/**
 * Whether a file at the top of a macros folder is listed as a macro: .ts
 * or .js in any case, not a .d.ts declaration. A name checkMacroFile
 * refuses is still listed, so it can be renamed or deleted.
 */
export function isMacroCandidate(name: string): boolean {
  const lower = name.toLowerCase();
  return (
    (lower.endsWith(".ts") || lower.endsWith(".js")) && !lower.endsWith(".d.ts")
  );
}
