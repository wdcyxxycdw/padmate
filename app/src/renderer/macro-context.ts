import { createContext } from "react";
import type { MacroList, ScriptStatusEntry } from "../shared/api";

/** What the key drawer knows about the script library (SCR-50, SCR-51). */
export interface MacroContextValue {
  /** macros.list: the whole library, or null while unknown. */
  list: MacroList | null;
  /** Files that do not build or load, failed in a run, or are disabled. */
  failing: ReadonlySet<string>;
  /** Opens the scripts page: to fix a script or create the first one. */
  onShowScripts?: () => void;
}

export const MacroContext = createContext<MacroContextValue>({
  list: null,
  failing: new Set(),
});

/** Files bindings run that the library lacks. */
export function missingMacros(list: MacroList | null): string[] {
  return list?.missing.map((macro) => macro.file) ?? [];
}

/**
 * The run problem of each script, as the controllers' runtimes report it:
 * the latest error (from the newest runtime), or that it is disabled. With
 * a profile id only that profile's runtimes count; without, every
 * runtime does, as the library is shared.
 */
export function macroRunProblems(
  scripts: ScriptStatusEntry[],
  id?: string,
): Map<string, { error?: string; disabled?: boolean }> {
  const problems = new Map<string, { error?: string; disabled?: boolean }>();
  const current = scripts
    .filter((entry) => id === undefined || entry.profileId === id)
    .sort((a, b) => b.generation - a.generation);
  for (const entry of current)
    for (const macro of entry.script.macros ?? []) {
      const known = problems.get(macro.file);
      problems.set(macro.file, {
        error: known?.error || macro.error,
        disabled: !!(known?.disabled || macro.disabled),
      });
    }
  return problems;
}

/** Files of the library with a problem the drawer points to. */
export function failingMacros(
  list: MacroList | null,
  scripts: ScriptStatusEntry[],
  id: string,
): Set<string> {
  const failing = new Set<string>();
  for (const macro of list?.macros ?? [])
    if (macro.problem) failing.add(macro.file);
  for (const file of macroRunProblems(scripts, id).keys()) failing.add(file);
  return failing;
}
