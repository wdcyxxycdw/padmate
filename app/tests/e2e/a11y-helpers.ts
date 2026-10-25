// What the accessibility pass of the real window needs (UI-24, a11y.spec.ts):
// axe-core injected into the page, the computed-style signatures that stand in
// for a contrast check under forced colors, the keyboard walk, and the check
// that nothing is out of reach at 150% and 200% zoom. Only the page is used,
// so a spec of any state can call them.
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { join, resolve } from "node:path";
import { expect, type Locator, type Page } from "@playwright/test";
import type { AxeResults, RunOptions } from "axe-core";
import { type A11yException, exceptionsFor } from "../a11y/exceptions";
import {
  computeSignature,
  placeFocusStart,
  signatureProperties,
} from "../a11y/page-scripts";

const root = resolve(__dirname, "../../..");
const requireFromApp = createRequire(join(root, "app/package.json"));
const axeSource = readFileSync(
  requireFromApp.resolve("axe-core/axe.min.js"),
  "utf8",
);

/** The rules the window is held to: WCAG 2.2 AA and axe's best practices. */
export const axeTags = [
  "wcag2a",
  "wcag2aa",
  "wcag21a",
  "wcag21aa",
  "wcag22aa",
  "best-practice",
];

/**
 * Rules axe cannot decide in the real window, and why. They are reported (the
 * spec attaches every fingerprint) but do not fail the run; any other rule
 * that comes back incomplete does, so a human looks at it.
 */
export const reviewedIncomplete = new Map<string, string>([
  [
    "color-contrast",
    "Labels over the WebGL canvas have no background axe can read; their colors are the tokens that theme-contrast.test.ts checks.",
  ],
  [
    "aria-valid-attr-value",
    "aria-controls of a menu button points at the menu while it is open; axe cannot follow it and asks for a review.",
  ],
]);

export interface AxeFinding {
  rule: string;
  impact: string;
  help: string;
  target: string;
  html: string;
}

/** What one axeCheck found: the findings are what failed, the rest is for the report. */
export interface AxeOutcome {
  label: string;
  findings: AxeFinding[];
  /** "rule|target" of everything axe left undecided. */
  incomplete: string[];
  /** Incomplete findings of a rule nobody reviewed. */
  unreviewed: string[];
}

async function inject(page: Page): Promise<void> {
  // evaluate goes through the debugger, which the page's CSP does not govern
  // (script-src 'self' keeps an inline script out, shell.spec.ts proves it).
  if (!(await page.evaluate(() => "axe" in window)))
    await page.evaluate(axeSource);
}

/**
 * Every transition and animation that ends is brought to its end, so a color
 * is the one it ends in. (Waiting for them would depend on the window drawing
 * frames; finishing them does not.) What repeats, the recording dot, stays.
 */
async function settle(page: Page): Promise<void> {
  await page.evaluate(() => {
    for (const animation of document.getAnimations())
      if (animation.effect?.getTiming().iterations !== Number.POSITIVE_INFINITY)
        animation.finish();
  });
}

type Found = {
  rule: string;
  impact: string;
  help: string;
  target: string;
  html: string;
};

/**
 * Runs axe over the page as it is and returns what it found, minus what an
 * exception of tests/a11y/exceptions.json excuses. The canvas is left out (its
 * pixels are the 3D model). While a menu is open, the size of targets is
 * checked in the menu alone: axe counts every control the popup covers a
 * little as "partially obscured" and too small, which says nothing about it.
 */
export async function axeScan(
  page: Page,
  label: string,
  extra: { exclude?: string[] } = {},
): Promise<AxeOutcome> {
  await inject(page);
  await settle(page);
  const menuOpen = (await page.locator('[role="menu"]').count()) > 0;
  const exclude = ["canvas", ...(extra.exclude ?? [])];
  const excused = exceptionsFor("electron");
  const scan = (context: unknown, options: RunOptions) =>
    page.evaluate(
      ([runContext, runOptions, list]) => {
        const axe = (
          window as unknown as {
            axe: {
              run(context: unknown, options: unknown): Promise<AxeResults>;
            };
          }
        ).axe;
        return axe.run(runContext, runOptions).then((results) => {
          const covered = (rule: string, target: string[]) => {
            const node = document.querySelector(target.join(" "));
            return (
              !!node &&
              (list as A11yException[]).some(
                (entry) => entry.rule === rule && node.matches(entry.selector),
              )
            );
          };
          const flat = (items: AxeResults["violations"]) =>
            items.flatMap((item) =>
              item.nodes
                .filter((node) => !covered(item.id, node.target as string[]))
                .map((node) => ({
                  rule: item.id,
                  impact: String(item.impact),
                  help: item.help,
                  target: (node.target as string[]).join(" "),
                  html: node.html.slice(0, 200),
                })),
            );
          return {
            violations: flat(results.violations),
            incomplete: flat(results.incomplete),
          };
        });
      },
      [context, options, excused] as const,
    );
  const everything = await scan(
    { exclude: exclude.map((selector) => [selector]) },
    {
      runOnly: { type: "tag", values: axeTags },
      rules: {
        "color-contrast": { enabled: true },
        "target-size": { enabled: !menuOpen },
      },
      resultTypes: ["violations", "incomplete"],
    },
  );
  const violations: Found[] = [...everything.violations];
  const incomplete: Found[] = [...everything.incomplete];
  if (menuOpen) {
    const sizes = await scan(
      { include: [['[role="menu"]']] },
      {
        runOnly: { type: "rule", values: ["target-size"] },
        resultTypes: ["violations", "incomplete"],
      },
    );
    violations.push(...sizes.violations);
    incomplete.push(...sizes.incomplete);
  }
  return {
    label,
    findings: violations,
    incomplete: incomplete.map((item) => `${item.rule}|${item.target}`),
    unreviewed: incomplete
      .filter((item) => !reviewedIncomplete.has(item.rule))
      .map((item) => `${item.rule}|${item.target}`),
  };
}

/**
 * axeScan, as an assertion: no finding, and no incomplete result of a rule
 * that nobody reviewed. Returns the outcome so the spec can report what was
 * left undecided.
 */
export async function axeCheck(
  page: Page,
  label: string,
  extra?: { exclude?: string[] },
): Promise<AxeOutcome> {
  const outcome = await axeScan(page, label, extra);
  expect(
    outcome.findings.map(
      (item) =>
        `${item.rule} (${item.impact}): ${item.help} — ${item.target} — ${item.html}`,
    ),
    `axe: ${label}`,
  ).toEqual([]);
  expect(outcome.unreviewed, `axe, undecided: ${label}`).toEqual([]);
  return outcome;
}

// ----- Forced colors -------------------------------------------------------

/**
 * What an element looks like as the browser computes it: the properties that
 * stay under a forced color scheme, as one string, with its fill reduced to
 * whether it paints something other than what is behind it. Two states of a
 * control that a person must tell apart have to have different signatures
 * there, since the colors of the page are replaced and axe's contrast check
 * means nothing. pseudo: the element's ::before or ::after, the thumb of a
 * switch. (The code is tests/a11y/page-scripts.ts, proved in jsdom.)
 */
export async function styleSignature(
  target: Locator,
  pseudo?: "::before" | "::after",
): Promise<string> {
  return target.evaluate(computeSignature, [
    signatureProperties as readonly string[],
    pseudo ?? "",
  ] as const);
}

// ----- Keyboard -------------------------------------------------------------

/** What the focused element is, in a few words, for a message. */
export function describeFocus(page: Page): Promise<string> {
  return page.evaluate(() => {
    const element = document.activeElement;
    if (!element || element === document.body) return "(nothing)";
    const name =
      element.getAttribute("aria-label") ||
      element.textContent?.trim().slice(0, 30) ||
      element.getAttribute("title") ||
      "";
    return `${element.tagName.toLowerCase()}${element.getAttribute("role") ? `[${element.getAttribute("role")}]` : ""} "${name}"`;
  });
}

/**
 * Whether the focus can be seen: its outline, shadow, border or background
 * differs from the same element without focus. The element loses and takes
 * the focus back to find out, so call it where that does no harm.
 */
export async function focusIsVisible(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const element = document.activeElement as HTMLElement | null;
    if (!element || element === document.body) return false;
    const host = (element.closest(".page-search, .drawer-capture") ??
      element) as HTMLElement;
    const read = () => {
      const style = getComputedStyle(host);
      const own = getComputedStyle(element);
      return [
        style.outlineStyle,
        style.outlineWidth,
        style.outlineColor,
        style.boxShadow,
        style.borderTopColor,
        style.backgroundColor,
        own.outlineStyle,
        own.outlineWidth,
        own.boxShadow,
        own.borderTopColor,
        own.backgroundColor,
      ].join("|");
    };
    const focused = read();
    element.blur();
    const plain = read();
    element.focus();
    return focused !== plain;
  });
}

/** The controls a keyboard user can land on, as the page lists them. */
export async function tabbableControls(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const visible = (element: Element) => {
      const rect = element.getBoundingClientRect();
      if (rect.width < 1 || rect.height < 1) return false;
      const style = getComputedStyle(element);
      return (
        style.visibility !== "hidden" &&
        style.display !== "none" &&
        !element.closest("[inert], [hidden], dialog:not([open])") &&
        // What a closed disclosure holds is not on the page, only its summary.
        (element.matches("summary") || !element.closest("details:not([open])"))
      );
    };
    const open = document.querySelector("dialog[open]");
    return [
      ...document.querySelectorAll<HTMLElement>(
        'a[href], button, input, select, textarea, summary, [tabindex]:not([tabindex="-1"])',
      ),
    ]
      .filter(
        (element) =>
          visible(element) &&
          !(element as HTMLButtonElement).disabled &&
          element.tabIndex >= 0 &&
          (!open || open.contains(element)) &&
          !element.closest(".visually-hidden, .controller-accessible-state"),
      )
      .map((element, index) => {
        element.setAttribute("data-a11y-id", String(index));
        return `${index}:${element.tagName.toLowerCase()}:${element.getAttribute("aria-label") || element.textContent?.trim().slice(0, 24) || element.getAttribute("title") || ""}`;
      });
  });
}

export interface TabWalk {
  /** The controls the walk landed on, in order. */
  visited: string[];
  /** Listed by tabbableControls but never reached. */
  missed: string[];
  /** Whether the walk came back to its first control (or left the page). */
  wrapped: boolean;
  /** Stops without a visible focus. */
  unseen: string[];
}

/**
 * Presses Tab from the start of the page until the focus comes back to where
 * it began, and compares what it passed with the controls the page lists.
 * `inside`: a selector the focus must stay in (a dialog); a stop outside it is
 * a trap that leaks.
 */
export async function walkTabs(
  page: Page,
  { limit = 200, inside }: { limit?: number; inside?: string } = {},
): Promise<TabWalk & { leaked: string[] }> {
  const listed = await tabbableControls(page);
  // Not only blur(): the next Tab starts after the element that had the focus
  // (or was clicked) and would skip the controls before it.
  await page.evaluate(placeFocusStart);
  const visited: string[] = [];
  const unseen: string[] = [];
  const leaked: string[] = [];
  const seen = new Set<string>();
  let wrapped = false;
  let leftThePage = false;
  for (let step = 0; step < limit; step++) {
    await page.keyboard.press("Tab");
    const stop = await page.evaluate((scope) => {
      const element = document.activeElement as HTMLElement | null;
      if (
        !element ||
        element === document.body ||
        element === document.documentElement
      )
        return { id: "", name: "(page)", inside: true };
      // A scrolling region with nothing to focus in it is a stop of its own.
      if (!element.hasAttribute("data-a11y-id"))
        element.setAttribute(
          "data-a11y-id",
          `extra-${Math.random().toString(36).slice(2)}`,
        );
      return {
        id: element.getAttribute("data-a11y-id") ?? "",
        name: `${element.tagName.toLowerCase()} ${element.getAttribute("aria-label") || element.textContent?.trim().slice(0, 24) || element.getAttribute("title") || ""}`,
        inside: scope ? !!element.closest(scope) : true,
      };
    }, inside ?? "");
    if (!stop.id) {
      // Left the page's own controls: the focus wrapped to the document. One
      // more Tab goes on from the start, so a walk that began in the middle
      // still passes the controls before where it began; the next repeat or the
      // second wrap ends it.
      if (visited.length === 0 || leftThePage) break;
      leftThePage = true;
      wrapped = true;
      continue;
    }
    if (seen.has(stop.id)) {
      wrapped = true;
      break;
    }
    seen.add(stop.id);
    visited.push(stop.name);
    if (!stop.inside) leaked.push(stop.name);
    if (!(await focusIsVisible(page))) unseen.push(stop.name);
  }
  const reached = new Set(seen);
  const missed = listed
    .filter((entry) => !reached.has(entry.split(":")[0]))
    .map((entry) => entry.split(":").slice(1).join(" "));
  return { visited, missed, wrapped, unseen, leaked };
}

// ----- Zoom -----------------------------------------------------------------

/**
 * The controls that cannot be brought into view at the current size and
 * zoom: an ancestor that does not scroll clips them, or they lie outside the
 * window. A control inside something that scrolls can be reached; one in
 * overflow: hidden, or beyond the window, cannot.
 */
export async function unreachableControls(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const selector =
      'button, a[href], input, select, textarea, summary, [role="button"], [tabindex]:not([tabindex="-1"])';
    const visible = (element: Element) => {
      const rect = element.getBoundingClientRect();
      if (rect.width < 1 || rect.height < 1) return false;
      const style = getComputedStyle(element);
      if (style.visibility === "hidden" || style.display === "none")
        return false;
      if (element.closest("dialog:not([open]), [hidden], [inert]"))
        return false;
      if (!element.matches("summary") && element.closest("details:not([open])"))
        return false;
      return !element.closest(".visually-hidden, .controller-accessible-state");
    };
    const hides = (overflow: string) =>
      overflow === "hidden" || overflow === "clip";
    const scrolls = (overflow: string) =>
      overflow === "auto" || overflow === "scroll";
    const unreachable: string[] = [];
    for (const element of document.querySelectorAll(selector)) {
      if (!visible(element)) continue;
      const rect = element.getBoundingClientRect();
      let [x0, x1, y0, y1] = [rect.left, rect.right, rect.top, rect.bottom];
      let ok = true;
      let topLayer = false;
      for (
        let parent = element.parentElement;
        parent && ok && !topLayer && parent !== document.documentElement;
        parent = parent.parentElement
      ) {
        const style = getComputedStyle(parent);
        const box = parent.getBoundingClientRect();
        // A dialog is in the top layer: what is around it does not clip it.
        if (parent.tagName === "DIALOG") topLayer = true;
        if (hides(style.overflowX) && (x0 < box.left - 1 || x1 > box.right + 1))
          ok = false;
        if (hides(style.overflowY) && (y0 < box.top - 1 || y1 > box.bottom + 1))
          ok = false;
        // Scrollable: the control can be scrolled to, so it counts as inside.
        if (
          scrolls(style.overflowX) &&
          parent.scrollWidth > parent.clientWidth
        ) {
          const width = Math.min(x1 - x0, box.width);
          x0 = box.left;
          x1 = box.left + width;
        }
        if (
          scrolls(style.overflowY) &&
          parent.scrollHeight > parent.clientHeight
        ) {
          const height = Math.min(y1 - y0, box.height);
          y0 = box.top;
          y1 = box.top + height;
        }
      }
      const page = document.scrollingElement as HTMLElement;
      const pageScrolls =
        page.scrollHeight > page.clientHeight + 1 &&
        getComputedStyle(document.documentElement).overflowY !== "hidden";
      if (
        ok &&
        !pageScrolls &&
        (x0 < -1 || x1 > innerWidth + 1 || y0 < -1 || y1 > innerHeight + 1)
      )
        ok = false;
      if (!ok)
        unreachable.push(
          `${(element.getAttribute("aria-label") || element.textContent || element.className || element.tagName).trim().slice(0, 40)} [${Math.round(rect.left)},${Math.round(rect.top)} ${Math.round(rect.width)}x${Math.round(rect.height)}]`,
        );
    }
    return unreachable;
  });
}
