// The accessibility exceptions (UI-24): app/tests/a11y/exceptions.json, shared
// by the jsdom pass (src/renderer/a11y.test.tsx) and the Electron pass
// (tests/e2e/a11y.spec.ts). An exception names an axe rule, a CSS selector of
// the nodes it covers and why it is accepted. It is a decision, not a
// silencer: add one only after the cause is understood, and say it in
// `reason`.
import exceptionFile from "./exceptions.json";

export type ExceptionScope = "jsdom" | "electron" | "both";

export interface A11yException {
  /** The axe rule id, such as "color-contrast". */
  rule: string;
  /** The nodes it covers: a CSS selector that the node itself matches. */
  selector: string;
  reason: string;
  /** Which pass it applies to; "both" for a cause that shows in either. */
  scope: ExceptionScope;
}

export const exceptions: A11yException[] = exceptionFile as A11yException[];

/** What a file that is wrong looks like, for the test that guards the file. */
export function exceptionProblems(list: A11yException[]): string[] {
  const problems: string[] = [];
  const seen = new Set<string>();
  list.forEach((entry, index) => {
    const at = `#${index} (${entry.rule})`;
    if (!entry.rule?.trim()) problems.push(`${at}: no rule`);
    if (!entry.selector?.trim()) problems.push(`${at}: no selector`);
    if ((entry.reason ?? "").trim().length < 20)
      problems.push(`${at}: the reason must say why, in a sentence`);
    if (!["jsdom", "electron", "both"].includes(entry.scope))
      problems.push(`${at}: scope must be jsdom, electron or both`);
    const key = `${entry.rule}|${entry.selector}|${entry.scope}`;
    if (seen.has(key)) problems.push(`${at}: listed twice`);
    seen.add(key);
  });
  return problems;
}

/** The exceptions of one pass. */
export function exceptionsFor(scope: "jsdom" | "electron"): A11yException[] {
  return exceptions.filter(
    (entry) => entry.scope === scope || entry.scope === "both",
  );
}

/**
 * Whether a node of a result is excused: an exception of its rule whose
 * selector the node matches. `matches` is the page's own test, which differs
 * between the passes (the jsdom document here, the page of Playwright there).
 */
export function isExcused(
  list: A11yException[],
  rule: string,
  matches: (selector: string) => boolean,
): boolean {
  return list.some((entry) => entry.rule === rule && matches(entry.selector));
}
