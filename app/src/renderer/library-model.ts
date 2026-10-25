// Pure helpers for the profile library page (UI-17, UI-18, PRO-16, PRO-28):
// the automatic-switch rules in config.json, profile IDs and names, the
// fixed default profile, and the messages for library failures. Nothing
// here talks to the daemon.
//
// Config helpers take a parsed config.json and return a new object, or null
// when the change would not alter anything. They never mutate their input,
// keep unknown members (rule.device, future fields) and keep the position of
// every member they replace. Every write drops what ADR-0053 retired
// (withoutRetired), so the UI never writes it back.
import type { ConfigGetResult, MacroInfo, ProfileEntry } from "../shared/api";
import { LibraryErrorCode } from "../shared/api";
import type { Config, ConfigRule } from "../shared/generated/schema";
import { PROFILE_SCHEMA_VERSION } from "../shared/profile-version";
import type { Language } from "./store";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

export const MAX_PROFILE_NAME = 64;

/**
 * The library ID of the fixed default profile (PRO-28, ADR-0053): every app
 * no rule binds uses it. It cannot be deleted or renamed, and the UI names
 * it 默认 / Default whatever name it stores.
 */
export const DEFAULT_PROFILE_ID = "default";

export function isDefaultProfile(id: string | null | undefined): boolean {
  return id === DEFAULT_PROFILE_ID;
}

/** The fixed default profile's name in the UI language. */
export function defaultProfileName(language: Language): string {
  return tx(language, "默认", "Default");
}

/** An app to add: a typed exe pattern, or a seen app with its full path. */
export interface AppRef {
  exe: string;
  path?: string;
}

/** A config.json rule as the page shows it. */
export interface AppRule {
  /** Position in config.rules (file order). */
  index: number;
  exe: string;
  profile: string;
  enabled: boolean;
  /** The file name to show; exe itself unless exe is a path. */
  label: string;
  /** exe is an absolute path, so its tooltip shows it in full. */
  isPath: boolean;
}

// ---------------------------------------------------------------------------
// Executable patterns, mirroring daemon/internal/profile/config.go.

/** Go's path.Clean for slash-separated paths. */
function cleanPath(p: string): string {
  if (p === "") return ".";
  const rooted = p.startsWith("/");
  const out: string[] = [];
  for (const part of p.split("/")) {
    if (part === "" || part === ".") continue;
    if (part === "..") {
      if (out.length > 0 && out[out.length - 1] !== "..") out.pop();
      else if (!rooted) out.push("..");
      continue;
    }
    out.push(part);
  }
  const joined = out.join("/");
  if (rooted) return `/${joined}`;
  return joined === "" ? "." : joined;
}

/** Go's path.Base. */
function basePath(p: string): string {
  if (p === "") return ".";
  const trimmed = p.replace(/\/+$/, "");
  if (trimmed === "") return "/";
  return trimmed.slice(trimmed.lastIndexOf("/") + 1);
}

/** The form the daemon compares: lower case, forward slashes, cleaned. */
export function normalizeExe(exe: string): string {
  return cleanPath(exe.replaceAll("\\", "/").toLowerCase());
}

/** A bare file name, or an absolute drive or UNC path; no wildcards. */
export function validExe(exe: string): boolean {
  if (exe.trim() !== exe || exe === "" || /[\0*?<>|"]/.test(exe)) return false;
  const s = exe.replaceAll("\\", "/");
  if (!s.includes("/")) return !s.includes(":") && s !== "." && s !== "..";
  if (s.endsWith("/")) return false;
  return (
    (s.length > 3 && s[1] === ":" && s[2] === "/" && /^[A-Za-z]/.test(s)) ||
    (s.startsWith("//") && s.slice(2).split("/").length >= 3)
  );
}

/**
 * Whether the rule pattern selects the executable path exe, as the daemon's
 * Select does: a bare name matches the base name, a path the whole path.
 */
export function matchExe(pattern: string, exe: string): boolean {
  if (exe === "") return false;
  const p = normalizeExe(pattern);
  let e = normalizeExe(exe);
  if (!p.includes("/")) e = basePath(e);
  return p === e;
}

/** The same pattern after normalisation ("Paint.EXE" and "paint.exe"). */
export function sameExe(a: string, b: string): boolean {
  return normalizeExe(a) === normalizeExe(b);
}

export function isExePath(exe: string): boolean {
  return /[\\/]/.test(exe);
}

/** The file name of a pattern, keeping its spelling. */
export function exeLabel(exe: string): string {
  const parts = exe.split(/[\\/]/);
  return parts[parts.length - 1] || exe;
}

// ---------------------------------------------------------------------------
// config.json documents.

/** Parses config.json text; throws unless it is a JSON object. */
export function parseConfigText(text: string): Config {
  const value: unknown = JSON.parse(text.replace(/^\uFEFF/, ""));
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error("config.json is not a JSON object");
  return value as Config;
}

/** The parsed configuration, or null when there is none or it is broken. */
export function readConfig(config: ConfigGetResult | null): Config | null {
  if (!config || config.problem) return null;
  try {
    return parseConfigText(config.text);
  } catch {
    return null;
  }
}

/** Two-space JSON with a final newline, as the rest of the UI writes. */
export function configText(config: Config, bom = false): string {
  return `${bom ? "\uFEFF" : ""}${JSON.stringify(config, null, 2)}\n`;
}

/**
 * The members ADR-0053 retired: defaultProfile, onUnmatched and ignore, and
 * rules whose profile is null (they stopped padmate for an app). The daemon
 * still loads a file with them and ignores them; the UI drops them from
 * every document it writes. Returns config itself when it has none.
 */
export function withoutRetired(config: Config): Config {
  const legacy = config as Config & Record<string, unknown>;
  const rules = Array.isArray(config.rules) ? config.rules : null;
  const kept = rules?.filter(
    (rule) => !(isRule(rule) && rule.profile === null),
  );
  const retired =
    "defaultProfile" in legacy || "onUnmatched" in legacy || "ignore" in legacy;
  if (!retired && (!rules || kept?.length === rules.length)) return config;
  const {
    defaultProfile: _default,
    onUnmatched: _unmatched,
    ignore: _ignore,
    ...rest
  } = legacy;
  return (
    rules && kept && kept.length !== rules.length
      ? { ...rest, rules: kept }
      : rest
  ) as Config;
}

/**
 * Applies mutate to config.json text. Returns the new text, or null when
 * mutate reports no change. A leading byte order mark stays; the retired
 * members go (withoutRetired).
 */
export function changeConfigText(
  text: string,
  mutate: (config: Config) => Config | null,
): string | null {
  const next = mutate(parseConfigText(text));
  return next
    ? configText(withoutRetired(next), text.startsWith("\uFEFF"))
    : null;
}

/** config.enabled: switch profiles by the foreground app (default on). */
export function switchingEnabled(config: Config | null): boolean {
  return config?.enabled !== false;
}

function rulesOf(config: Config): ConfigRule[] {
  return Array.isArray(config.rules) ? config.rules : [];
}

/** A rule that selects a profile; a retired null rule selects nothing. */
function isRule(rule: unknown): rule is ConfigRule {
  return (
    !!rule &&
    typeof rule === "object" &&
    typeof (rule as ConfigRule).exe === "string"
  );
}

function bindsProfile(rule: unknown): rule is ConfigRule & { profile: string } {
  return isRule(rule) && typeof rule.profile === "string";
}

function view(rule: ConfigRule & { profile: string }, index: number): AppRule {
  return {
    index,
    exe: rule.exe,
    profile: rule.profile,
    enabled: rule.enabled !== false,
    label: exeLabel(rule.exe),
    isPath: isExePath(rule.exe),
  };
}

/** Rules for profile, in file order. */
export function rulesFor(config: Config | null, profile: string): AppRule[] {
  if (!config) return [];
  const result: AppRule[] = [];
  rulesOf(config).forEach((rule, index) => {
    if (bindsProfile(rule) && rule.profile === profile)
      result.push(view(rule, index));
  });
  return result;
}

/** The path a rule is evaluated against, when the app names one. */
function concretePath(app: AppRef): string {
  return app.path || (isExePath(app.exe) ? app.exe : "");
}

/** Whether rule pattern selects app, as far as the app is known. */
export function ruleMatchesApp(pattern: string, app: AppRef): boolean {
  const path = concretePath(app);
  return path ? matchExe(pattern, path) : sameExe(pattern, app.exe);
}

/**
 * The rule that decides app now: the first enabled rule that matches it and
 * names a profile, as the daemon's Select. Without one the app uses the
 * fixed default profile. config.enabled is not considered.
 */
export function effectiveRule(
  config: Config | null,
  app: AppRef,
): AppRule | null {
  if (!config) return null;
  const rules = rulesOf(config);
  for (let index = 0; index < rules.length; index++) {
    const rule = rules[index];
    if (
      bindsProfile(rule) &&
      rule.enabled !== false &&
      ruleMatchesApp(rule.exe, app)
    )
      return view(rule, index);
  }
  return null;
}

/**
 * The rule bindApp would replace after asking: app is decided by a rule for
 * another profile. null when app is unbound or already uses profile.
 */
export function bindingConflict(
  config: Config | null,
  app: AppRef,
  profile: string,
): AppRule | null {
  const rule = effectiveRule(config, app);
  return rule && rule.profile !== profile ? rule : null;
}

/** Whether any rule for profile, enabled or not, matches app. */
export function appBoundTo(
  config: Config | null,
  app: AppRef,
  profile: string,
): boolean {
  return rulesFor(config, profile).some((rule) =>
    ruleMatchesApp(rule.exe, app),
  );
}

function withRules(config: Config, rules: ConfigRule[]): Config {
  return { ...config, rules };
}

function enable(rule: ConfigRule): ConfigRule {
  if (rule.enabled !== false) return rule;
  const { enabled: _enabled, ...rest } = rule;
  return rest;
}

/**
 * Makes app use profile. One app has one rule:
 *
 * - Nothing changes (null) when the rule that decides app already uses
 *   profile.
 * - Rules with the same exe pattern are replaced whatever their profile or
 *   state, and so are enabled path rules for another profile that match the
 *   app's path. The first rule with the same pattern for profile stays
 *   instead (enabled again), keeping its other members.
 * - The rule goes before the first remaining enabled rule that matches the
 *   app, so that it wins (a path ahead of a broader name). Otherwise a kept
 *   rule stays where it was and a new one goes at the end.
 */
export function bindApp(
  config: Config,
  app: AppRef,
  profile: string,
): Config | null {
  const current = effectiveRule(config, app);
  if (current && current.profile === profile) return null;
  const path = concretePath(app);
  let kept: ConfigRule | null = null;
  let keptAt = -1;
  const next: ConfigRule[] = [];
  for (const rule of rulesOf(config)) {
    if (!isRule(rule)) {
      next.push(rule);
      continue;
    }
    const same = sameExe(rule.exe, app.exe);
    if (same && rule.profile === profile && !kept) {
      kept = enable(rule);
      keptAt = next.length;
      continue;
    }
    const replacedPath =
      !!path &&
      isExePath(rule.exe) &&
      rule.enabled !== false &&
      rule.profile !== profile &&
      matchExe(rule.exe, path);
    if (same || replacedPath) continue;
    next.push(rule);
  }
  const winning = next.findIndex(
    (rule) =>
      bindsProfile(rule) &&
      rule.enabled !== false &&
      ruleMatchesApp(rule.exe, app),
  );
  const at = kept
    ? winning < 0
      ? keptAt
      : Math.min(winning, keptAt)
    : winning < 0
      ? next.length
      : winning;
  next.splice(at, 0, kept ?? { exe: app.exe, profile });
  return withRules(config, next);
}

/** Removes the rules with exe's pattern for profile. */
export function unbindApp(
  config: Config,
  exe: string,
  profile: string,
): Config | null {
  const rules = rulesOf(config);
  const next = rules.filter(
    (rule) =>
      !(isRule(rule) && sameExe(rule.exe, exe) && rule.profile === profile),
  );
  return next.length === rules.length ? null : withRules(config, next);
}

/**
 * Turns the rules with exe's pattern for profile on or off. Turning one on
 * drops "enabled" (true is the default); off writes "enabled": false.
 */
export function setRuleEnabled(
  config: Config,
  exe: string,
  profile: string,
  enabled: boolean,
): Config | null {
  let changed = false;
  const next = rulesOf(config).map((rule) => {
    if (
      !isRule(rule) ||
      !sameExe(rule.exe, exe) ||
      rule.profile !== profile ||
      (rule.enabled !== false) === enabled
    )
      return rule;
    changed = true;
    return enabled ? enable(rule) : { ...rule, enabled: false };
  });
  return changed ? withRules(config, next) : null;
}

/** config.enabled: switch profiles by the foreground app. */
export function setSwitching(config: Config, enabled: boolean): Config | null {
  return (config.enabled !== false) === enabled ? null : { ...config, enabled };
}

/** Before deleting profile id: its rules go. */
export function removeProfileReferences(
  config: Config,
  id: string,
): Config | null {
  const rules = rulesOf(config);
  const kept = rules.filter((rule) => !(isRule(rule) && rule.profile === id));
  return kept.length === rules.length ? null : withRules(config, kept);
}

const sameDocument = (a: Config, b: Config) =>
  JSON.stringify(a) === JSON.stringify(b);

/**
 * Undoes removeProfileReferences(before, id), which wrote after, once the
 * delete it prepared was refused. Both are compared as written, without the
 * retired members. While nothing else changed the file, before comes back
 * whole. Otherwise the removed rules go back to their old positions unless
 * the app has a rule again.
 */
export function restoreProfileReferences(
  current: Config,
  before: Config,
  after: Config,
  id: string,
): Config | null {
  const original = withoutRetired(before);
  if (sameDocument(current, withoutRetired(after)))
    return sameDocument(current, original) ? null : original;
  const rules = [...rulesOf(current)];
  let restored = false;
  rulesOf(before).forEach((rule, index) => {
    if (!isRule(rule) || rule.profile !== id) return;
    if (rules.some((other) => isRule(other) && sameExe(other.exe, rule.exe)))
      return;
    rules.splice(Math.min(index, rules.length), 0, rule);
    restored = true;
  });
  return restored ? withRules(current, rules) : null;
}

// ---------------------------------------------------------------------------
// Profiles.

/** profile.ValidLibraryID: the folder name of a library profile. */
export function validLibraryID(id: string): boolean {
  if (!/^[a-z0-9][a-z0-9_-]{0,63}$/.test(id)) return false;
  if (["con", "prn", "aux", "nul"].includes(id)) return false;
  return !/^(com|lpt)[0-9]$/.test(id);
}

/**
 * A new library ID from a display name: lower-case ASCII letters and digits
 * joined by "-", "profile" when none remain, then "-2", "-3"… until it is
 * valid and not in existing. The fixed default's ID is never given out.
 */
export function slugId(name: string, existing: Iterable<string>): string {
  const taken = new Set([
    DEFAULT_PROFILE_ID,
    ...[...existing].map((id) => id.toLowerCase()),
  ]);
  const base =
    name
      .normalize("NFKD")
      .replace(/\p{M}/gu, "")
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "") || "profile";
  for (let n = 1; ; n++) {
    const suffix = n === 1 ? "" : `-${n}`;
    const stem = base.slice(0, 64 - suffix.length).replace(/-+$/, "");
    const id = `${stem || "profile"}${suffix}`;
    if (validLibraryID(id) && !taken.has(id)) return id;
  }
}

export type NameProblem = "empty" | "tooLong" | "taken";

/** How names compare (F2): trimmed, Unicode NFC, case-insensitive. */
function nameKey(name: string): string {
  return name.trim().normalize("NFC").toLowerCase();
}

/**
 * The names profiles other than self go by, as nameKey keys. The fixed
 * default holds both of its names, 默认 and Default, whatever it stores.
 */
function takenNames(
  profiles: readonly ProfileEntry[],
  self?: string,
): Set<string> {
  const taken = new Set<string>();
  for (const entry of profiles) {
    if (entry.id === self) continue;
    if (isDefaultProfile(entry.id)) {
      taken.add(nameKey(defaultProfileName("zh-CN")));
      taken.add(nameKey(defaultProfileName("en")));
    } else taken.add(nameKey(entry.name?.trim() || entry.id));
  }
  return taken;
}

/**
 * A profile name is trimmed, non-empty, at most 64 characters and not the
 * name of another profile in profiles (self, the one renamed, excepted).
 */
export function profileNameProblem(
  name: string,
  profiles: readonly ProfileEntry[] = [],
  self?: string,
): NameProblem | null {
  const trimmed = name.trim();
  if (!trimmed) return "empty";
  if ([...trimmed].length > MAX_PROFILE_NAME) return "tooLong";
  return takenNames(profiles, self).has(nameKey(trimmed)) ? "taken" : null;
}

export function nameProblemText(problem: NameProblem, language: Language) {
  switch (problem) {
    case "empty":
      return tx(language, "名称不能为空", "Enter a name");
    case "tooLong":
      return tx(
        language,
        `名称最多 ${MAX_PROFILE_NAME} 个字`,
        `Use at most ${MAX_PROFILE_NAME} characters`,
      );
    case "taken":
      return tx(language, "已有同名配置", "A profile with this name exists");
  }
}

/**
 * base, or "<base> 2", "<base> 3"… : the first that no profile in profiles
 * goes by, shortened to fit 64 characters.
 */
export function uniqueName(
  base: string,
  profiles: readonly ProfileEntry[],
): string {
  const taken = takenNames(profiles);
  for (let n = 1; ; n++) {
    const suffix = n === 1 ? "" : ` ${n}`;
    const room = MAX_PROFILE_NAME - suffix.length;
    const name = `${[...base.trim()].slice(0, room).join("").trimEnd()}${suffix}`;
    if (!taken.has(nameKey(name))) return name;
  }
}

/** A new, empty profile document. */
export function blankProfileText(name: string): string {
  return `${JSON.stringify({ schemaVersion: PROFILE_SCHEMA_VERSION, name, layers: { base: {} } }, null, 2)}\n`;
}

function skipSpace(text: string, at: number): number {
  let i = at;
  while (i < text.length && /[ \t\r\n]/.test(text[i] as string)) i++;
  return i;
}

function skipString(text: string, at: number): number {
  let i = at + 1;
  while (i < text.length) {
    const c = text[i];
    if (c === "\\") i += 2;
    else if (c === '"') return i + 1;
    else i++;
  }
  throw new Error("unterminated string");
}

function skipValue(text: string, at: number): number {
  const c = text[at];
  if (c === '"') return skipString(text, at);
  if (c === "{" || c === "[") {
    let depth = 0;
    let i = at;
    while (i < text.length) {
      const d = text[i];
      if (d === '"') {
        i = skipString(text, i);
        continue;
      }
      if (d === "{" || d === "[") depth++;
      else if (d === "}" || d === "]") {
        depth--;
        if (depth === 0) return i + 1;
      }
      i++;
    }
    throw new Error("unterminated value");
  }
  let i = at;
  while (i < text.length && !/[,}\] \t\r\n]/.test(text[i] as string)) i++;
  return i;
}

/** The source span of a top-level member's value in valid JSON text. */
function memberSpan(
  text: string,
  key: string,
): { start: number; end: number } | null {
  let i = skipSpace(text, text.startsWith("\uFEFF") ? 1 : 0);
  if (text[i] !== "{") return null;
  i = skipSpace(text, i + 1);
  let found: { start: number; end: number } | null = null;
  while (i < text.length && text[i] !== "}") {
    const keyEnd = skipString(text, i);
    const name: unknown = JSON.parse(text.slice(i, keyEnd));
    i = skipSpace(text, keyEnd);
    if (text[i] !== ":") return null;
    const start = skipSpace(text, i + 1);
    const end = skipValue(text, start);
    if (name === key) found = { start, end };
    i = skipSpace(text, end);
    if (text[i] === ",") i = skipSpace(text, i + 1);
  }
  return found;
}

/**
 * Sets a profile document's display name. The text stays as it was apart
 * from the name's value; a document without a name gains one after
 * schemaVersion. Throws when text is not a JSON object.
 */
export function renameProfileText(text: string, name: string): string {
  const parsed: unknown = JSON.parse(text.replace(/^\uFEFF/, ""));
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed))
    throw new Error("The profile is not a JSON object");
  try {
    const span = memberSpan(text, "name");
    if (span) {
      const next = `${text.slice(0, span.start)}${JSON.stringify(name)}${text.slice(span.end)}`;
      if (JSON.parse(next.replace(/^\uFEFF/, "")).name === name) return next;
    }
  } catch {
    /* Fall back to rewriting the document. */
  }
  const entries = Object.entries(parsed).filter(([key]) => key !== "name");
  const at = entries.findIndex(([key]) => key === "schemaVersion") + 1;
  entries.splice(at, 0, ["name", name]);
  return `${text.startsWith("\uFEFF") ? "\uFEFF" : ""}${JSON.stringify(Object.fromEntries(entries), null, 2)}\n`;
}

/**
 * Whether profile.json text names a script, so deleting the profile's folder
 * deletes script files too. Text that is not a JSON object names none.
 */
export function profileHasScript(text: string): boolean {
  try {
    const parsed: unknown = JSON.parse(text.replace(/^\uFEFF/, ""));
    return (
      !!parsed &&
      typeof parsed === "object" &&
      !Array.isArray(parsed) &&
      (parsed as { script?: unknown }).script != null
    );
  } catch {
    return false;
  }
}

/**
 * The name to show for a library profile: 默认 / Default for the fixed
 * default, whatever it stores, else its name or its ID.
 */
export function profileName(entry: ProfileEntry, language: Language): string {
  if (isDefaultProfile(entry.id)) return defaultProfileName(language);
  return storedName(entry);
}

function storedName(entry: ProfileEntry): string {
  return entry.name?.trim() || entry.id;
}

/**
 * The name of a copy: "<name> 副本" / "<name> copy", then " 2", " 3"… until
 * no profile in profiles has it, at most 64 characters.
 */
export function copyName(
  name: string,
  language: Language,
  profiles: readonly ProfileEntry[] = [],
): string {
  const suffix = tx(language, " 副本", " copy");
  // Room for " 99" as well, so numbering never cuts into the suffix.
  const room = MAX_PROFILE_NAME - [...suffix].length - 3;
  return uniqueName(
    `${[...name].slice(0, room).join("").trimEnd()}${suffix}`,
    profiles,
  );
}

// Dictionary order: numbers by value, case and accents ignored, so "脚本 2"
// comes before "脚本 10". The locale is fixed, not the system's, so Chinese
// names go by pinyin on every machine (the system's locale on an English
// one orders them by radical and strokes); the Main process lists programs
// the same way.
const collator = new Intl.Collator(["zh-CN", "en"], {
  numeric: true,
  sensitivity: "base",
});

const byText = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0);

/**
 * Oldest first; an unknown creation time (a platform that does not say)
 * comes after every known one.
 */
function byCreated(a: number | undefined, b: number | undefined): number {
  if (a === b) return 0;
  if (a === undefined) return 1;
  if (b === undefined) return -1;
  return a - b;
}

/**
 * The profiles in the order the profiles page lists them: the fixed default
 * first, then by creation time, oldest first, whichever is active; equal or
 * unknown times by name, then by ID. Returns a new array.
 */
export function sortProfiles(
  profiles: readonly ProfileEntry[],
): ProfileEntry[] {
  return [...profiles].sort(
    (a, b) =>
      Number(isDefaultProfile(b.id)) - Number(isDefaultProfile(a.id)) ||
      byCreated(a.created, b.created) ||
      collator.compare(storedName(a), storedName(b)) ||
      byText(a.id, b.id),
  );
}

/**
 * The profiles in the order a menu to pick one lists them: the fixed
 * default first, then in dictionary order of the name, then by ID. Returns a
 * new array.
 */
export function sortProfilesByName(
  profiles: readonly ProfileEntry[],
): ProfileEntry[] {
  return [...profiles].sort(
    (a, b) =>
      Number(isDefaultProfile(b.id)) - Number(isDefaultProfile(a.id)) ||
      collator.compare(storedName(a), storedName(b)) ||
      byText(a.id, b.id),
  );
}

/**
 * The scripts in the order the scripts page lists them: by creation time,
 * oldest first; equal or unknown times by name, then by file. Returns a new
 * array.
 */
export function sortMacros(macros: readonly MacroInfo[]): MacroInfo[] {
  return [...macros].sort(
    (a, b) =>
      byCreated(a.created, b.created) ||
      collator.compare(a.name, b.name) ||
      byText(a.file, b.file),
  );
}

/**
 * The scripts in the order a list to pick one lists them: in dictionary
 * order of the name, so "复制全部" comes before "复制全部 2" and
 * "复制全部 10" after it, then by file. Returns a new array.
 */
export function sortMacrosByName(macros: readonly MacroInfo[]): MacroInfo[] {
  return [...macros].sort(
    (a, b) => collator.compare(a.name, b.name) || byText(a.file, b.file),
  );
}

/**
 * The profile to activate before deleting id: the fixed default, else the
 * first other by name, else none (null disables mapping). Broken profiles
 * cannot be activated and are skipped.
 */
export function nextActiveAfterDelete(
  profiles: readonly ProfileEntry[],
  id: string,
): string | null {
  const usable = profiles.filter((entry) => entry.id !== id && !entry.problem);
  return sortProfilesByName(usable)[0]?.id ?? null;
}

// ---------------------------------------------------------------------------
// Profiles based on others (PRO-19, ADR-0070).

/** The most parents a profile has; the daemon refuses a fifth. */
export const MAX_EXTENDS_DEPTH = 4;

/** The profiles directly based on profile id, in the order of profiles. */
export function profilesBasedOn(
  profiles: readonly ProfileEntry[],
  id: string,
): ProfileEntry[] {
  return profiles.filter((entry) => entry.extends === id);
}

/**
 * The profiles level by level below profile id: the ones based on it, then
 * the ones based on those, and so on. A loop in what the library says (which
 * the daemon refuses) ends the walk.
 */
function levelsBelow(
  profiles: readonly ProfileEntry[],
  id: string,
): ProfileEntry[][] {
  const levels: ProfileEntry[][] = [];
  const seen = new Set<string>();
  let level = [id];
  for (;;) {
    const next = profiles.filter(
      (entry) =>
        !seen.has(entry.id) && !!entry.extends && level.includes(entry.extends),
    );
    if (!next.length) return levels;
    for (const entry of next) seen.add(entry.id);
    levels.push(next);
    level = next.map((entry) => entry.id);
  }
}

/** Every profile based on profile id, however far down. */
export function descendantsOf(
  profiles: readonly ProfileEntry[],
  id: string,
): ProfileEntry[] {
  return levelsBelow(profiles, id).flat();
}

/** How many parents profile id has, following what the library says. */
function parentsAbove(profiles: readonly ProfileEntry[], id: string): number {
  const seen = new Set<string>([id]);
  let count = 0;
  for (
    let parent = profiles.find((entry) => entry.id === id)?.extends;
    parent && !seen.has(parent);
    parent = profiles.find((entry) => entry.id === parent)?.extends
  ) {
    seen.add(parent);
    count++;
  }
  return count;
}

/**
 * The profiles that profile id can be based on, in the order a menu lists
 * them: not itself, not one that is based on it (however far: that would be a
 * loop), not one with errors, and not one that would give it, or a profile
 * based on it, more than four parents.
 */
export function parentChoices(
  profiles: readonly ProfileEntry[],
  id: string,
): ProfileEntry[] {
  const below = new Set(descendantsOf(profiles, id).map((entry) => entry.id));
  const height = levelsBelow(profiles, id).length;
  return sortProfilesByName(
    profiles.filter(
      (entry) =>
        entry.id !== id &&
        !below.has(entry.id) &&
        !entry.problem &&
        height + 1 + parentsAbove(profiles, entry.id) <= MAX_EXTENDS_DEPTH,
    ),
  );
}

/** The name of the profile another is based on: 默认 / Default, its name, or its ID when the library lacks it. */
export function parentName(
  id: string,
  profiles: readonly ProfileEntry[],
  language: Language,
): string {
  if (isDefaultProfile(id)) return defaultProfileName(language);
  const entry = profiles.find((item) => item.id === id);
  return entry ? profileName(entry, language) : id;
}

// ---------------------------------------------------------------------------
// Failures.

export type LibraryFailureKind =
  /** config.json changed elsewhere twice; the page shows the new file. */
  | "configConflict"
  /** A profile changed elsewhere; the list was read again. */
  | "profileConflict"
  /** The daemon was started with the profile; only a restart releases it. */
  | "startup"
  /** Running rules still select the profile. */
  | "referenced"
  /** The profile became active again before it could be deleted. */
  | "active"
  /** The library ID is taken. */
  | "exists"
  /** The fixed default profile cannot be deleted (PRO-28). */
  | "fixedDelete"
  /** The fixed default profile cannot be renamed (PRO-28). */
  | "fixedRename"
  /** The folder holds links or special files, or is too big to copy. */
  | "unsupported"
  | "disconnected"
  /** This daemon has no library (started without --data-dir). */
  | "noLibrary"
  /** config.json cannot be read; fix it first. */
  | "brokenConfig"
  /** The profile document has errors. */
  | "brokenProfile"
  /** A profile based on the one deleted cannot be written out whole first (PRO-19). */
  | "childBlocked"
  /** The profile folder holds what an export refuses (PRO-20). */
  | "notExportable"
  /** The daemon refused a profile file (PRO-20); reason is an ArchiveProblem. */
  | "archive"
  /** Main refused a profile file (PRO-20); reason is a ProfileFileProblem. */
  | "file"
  /** The daemon refused an AntiMicroX file (PRO-21); reason is an AmgpProblem. */
  | "amgp"
  | "error";

export interface LibraryFailure {
  kind: LibraryFailureKind;
  /** The daemon's message, for "error" and diagnostics. */
  message: string;
  /** Why "archive", "file", "amgp" or "childBlocked" refused. */
  reason?: string;
  /** The profile "childBlocked" names, by library ID. */
  profile?: string;
  /** The file "notExportable" names, relative to the profile folder. */
  path?: string;
}

export type LibraryOutcome<T = undefined> =
  | { ok: true; value: T }
  | { ok: false; failure: LibraryFailure };

interface ErrorInfo {
  message: string;
  code?: number;
  data?: unknown;
}

/** Reads UIErrorData (plain objects) as well as Error instances. */
export function errorInfo(error: unknown): ErrorInfo {
  if (error && typeof error === "object") {
    const e = error as { message?: unknown; code?: unknown; data?: unknown };
    return {
      message: typeof e.message === "string" ? e.message : String(error),
      ...(typeof e.code === "number" ? { code: e.code } : {}),
      ...(e.data !== undefined ? { data: e.data } : {}),
    };
  }
  return { message: String(error) };
}

/**
 * config.put loses the daemon's error code on its way through Electron, so
 * the daemon's message counts too.
 */
export function isRevisionConflict(error: unknown): boolean {
  const e = errorInfo(error);
  return (
    e.code === LibraryErrorCode.revisionConflict ||
    /revision conflict/i.test(e.message)
  );
}

/** Classifies a daemon rejection of a library request. */
export function libraryFailure(error: unknown): LibraryFailure {
  const e = errorInfo(error);
  const failure = (kind: LibraryFailureKind) => ({ kind, message: e.message });
  if (isRevisionConflict(error)) return failure("profileConflict");
  const reason = (e.data as { reason?: unknown } | undefined)?.reason;
  if (e.code === LibraryErrorCode.archiveInvalid)
    return {
      ...failure("archive"),
      reason: typeof reason === "string" ? reason : "",
    };
  if (e.code === LibraryErrorCode.amgpInvalid)
    return {
      ...failure("amgp"),
      reason: typeof reason === "string" ? reason : "",
    };
  // A refusal of the chosen file in Main, which has no daemon code.
  if (e.code === undefined && typeof reason === "string")
    return { ...failure("file"), reason };
  switch (e.code) {
    case LibraryErrorCode.profileActive:
      return failure("active");
    case LibraryErrorCode.profileReferenced: {
      const references = (e.data as { references?: unknown } | undefined)
        ?.references;
      return failure(
        Array.isArray(references) && references.includes("startup")
          ? "startup"
          : "referenced",
      );
    }
    case LibraryErrorCode.profileExists:
      return failure("exists");
    case LibraryErrorCode.profileFixed:
      return failure(
        (e.data as { op?: unknown } | undefined)?.op === "rename"
          ? "fixedRename"
          : "fixedDelete",
      );
    case LibraryErrorCode.profileUnsupported:
      return failure("unsupported");
    case -32008:
      return failure("noLibrary");
    case LibraryErrorCode.profileChild: {
      const data = e.data as { id?: unknown; reason?: unknown } | undefined;
      return {
        ...failure("childBlocked"),
        ...(typeof data?.reason === "string" ? { reason: data.reason } : {}),
        ...(typeof data?.id === "string" ? { profile: data.id } : {}),
      };
    }
  }
  return failure("error");
}

/**
 * One line for a failure. label names a profile by its library ID, for a
 * failure that names one (childBlocked); its ID stands in without it.
 */
export function failureText(
  failure: LibraryFailure,
  language: Language,
  label?: (id: string) => string,
): string {
  switch (failure.kind) {
    case "configConflict":
      return tx(
        language,
        "设置已在别处修改，已重新载入",
        "Settings changed elsewhere and were reloaded",
      );
    case "profileConflict":
      return tx(
        language,
        "配置已在别处修改，已重新载入",
        "The profile changed elsewhere and was reloaded",
      );
    case "startup":
      return tx(
        language,
        "此配置为启动时指定的配置，无法删除",
        "padmate was started with this profile, so it can't be deleted",
      );
    case "referenced":
      return tx(
        language,
        "仍有切换规则使用此配置",
        "Switching rules still use this profile",
      );
    case "active":
      return tx(
        language,
        "此配置正在使用，请先切换到其他配置",
        "This profile is in use. Switch to another one first",
      );
    case "exists":
      return tx(language, "此配置已存在", "This profile already exists");
    case "fixedDelete":
      return tx(
        language,
        "无法删除默认配置",
        "The default profile can't be deleted",
      );
    case "fixedRename":
      return tx(
        language,
        "无法重命名默认配置",
        "The default profile can't be renamed",
      );
    case "unsupported":
      return tx(
        language,
        "无法复制此配置文件夹",
        "This profile folder can't be copied",
      );
    case "disconnected":
      return tx(language, "padmate 未连接", "padmate is not connected");
    case "noLibrary":
      return tx(
        language,
        "当前启动方式无法管理配置",
        "Profiles can't be managed in this session",
      );
    case "brokenConfig":
      return tx(
        language,
        "无法读取设置文件，请先修复 config.json",
        "Fix config.json first",
      );
    case "brokenProfile":
      return tx(language, "配置文件有错误", "The profile file has errors");
    case "childBlocked": {
      if (!failure.profile)
        return tx(
          language,
          "基于它的配置无法先写成完整内容",
          "A profile based on it can't be written out whole first",
        );
      const name = label?.(failure.profile) ?? failure.profile;
      return failure.reason === "script"
        ? tx(
            language,
            `基于它的「${name}」继承了它的脚本，无法先写成完整内容`,
            `“${name}” is based on it and inherits its script, so it can't be written out whole first`,
          )
        : failure.reason === "invalid"
          ? tx(
              language,
              `基于它的「${name}」有错误，无法先写成完整内容`,
              `“${name}” is based on it and has errors, so it can't be written out whole first`,
            )
          : tx(
              language,
              `基于它的「${name}」无法先写成完整内容`,
              `“${name}” is based on it and can't be written out whole first`,
            );
    }
    case "notExportable":
      return failure.path
        ? tx(
            language,
            `配置文件夹中的「${failure.path}」无法导出`,
            `“${failure.path}” in the profile folder can't be exported`,
          )
        : tx(
            language,
            "配置文件夹中有无法导出的文件",
            "The profile folder holds files that cannot be exported",
          );
    case "archive":
      return archiveText(failure.reason, language);
    case "file":
      return fileText(failure.reason, language);
    case "amgp":
      return amgpText(failure.reason, language);
    case "error":
      return failure.message;
  }
}

/** Why the daemon refused a profile file (PRO-20). */
function archiveText(reason: string | undefined, language: Language): string {
  switch (reason) {
    case "version":
      return tx(
        language,
        "此文件由更新版本的 padmate 导出，请先更新 padmate",
        "This file was exported by a newer padmate. Update padmate first",
      );
    case "entry":
      return tx(
        language,
        "文件中含有不支持的内容",
        "The file holds entries that are not supported",
      );
    case "limit":
      return tx(
        language,
        "文件内容超出导入限制",
        "The file exceeds the import limits",
      );
    case "encoding":
      return tx(
        language,
        "文件中的脚本或 JSON 不是 UTF-8 编码",
        "A script or JSON file in it is not UTF-8",
      );
    case "profile":
      return tx(
        language,
        "文件中的配置有错误",
        "The profile in the file has errors",
      );
    default:
      return tx(
        language,
        "不是 padmate 配置文件，或文件已损坏",
        "This is not a padmate profile file, or it is damaged",
      );
  }
}

/** Why the daemon refused an AntiMicroX file (PRO-21). */
function amgpText(reason: string | undefined, language: Language): string {
  switch (reason) {
    case "size":
      return tx(language, "文件超过 1 MiB", "The file is larger than 1 MiB");
    case "encoding":
      return tx(
        language,
        "文件不是 UTF-8 编码，请用 AntiMicroX 另存，或转成 UTF-8",
        "The file is not UTF-8. Save it again in AntiMicroX, or convert it",
      );
    case "xml":
      return tx(language, "不是合法的 XML 文件", "The file is not valid XML");
    case "limit":
      return tx(
        language,
        "文件的元素太多或嵌套太深",
        "The file has too many elements or nests too deeply",
      );
    case "doctype":
      return tx(
        language,
        "文件含有 DOCTYPE 声明，出于安全不读取",
        "The file has a DOCTYPE, which padmate does not read",
      );
    case "root":
      return tx(
        language,
        "这不是 AntiMicroX 的配置文件",
        "This is not an AntiMicroX profile",
      );
    case "joystick":
      return tx(
        language,
        "这是原始摇杆的配置，只能导入游戏手柄的配置",
        "This profile is for a raw joystick. Only gamepad profiles can be imported",
      );
    case "version":
      return tx(
        language,
        "这个配置的版本太旧，请用较新的 AntiMicroX 打开后另存一次",
        "This profile is from an old version of AntiMicro. Open it in a recent AntiMicroX and save it again",
      );
    default:
      return tx(
        language,
        "无法导入这个 AntiMicroX 配置文件",
        "This AntiMicroX file cannot be imported",
      );
  }
}

/** Why Main refused a profile file (PRO-20). */
function fileText(reason: string | undefined, language: Language): string {
  switch (reason) {
    case "size":
      return tx(language, "文件超过 4 MiB", "The file is larger than 4 MiB");
    case "write":
      return tx(
        language,
        "无法保存文件，请选择其他文件夹",
        "The file could not be saved. Choose another folder",
      );
    case "expired":
      return tx(
        language,
        "等待时间过长，请重新选择文件",
        "This took too long. Choose the file again",
      );
    default:
      return tx(
        language,
        "无法读取所选文件",
        "The chosen file could not be read",
      );
  }
}
