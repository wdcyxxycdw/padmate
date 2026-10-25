// UI-24: Windows high contrast. A forced color scheme drops background-only and
// shadow-only states, so every state the stylesheets show that way needs a
// block in @media (forced-colors: active). The Electron pass of
// app/tests/e2e/a11y.spec.ts proves the result in a real window by comparing
// computed styles; this text test is what stays in `task ci`, and it fails
// when a selector below loses its block or a new `outline: none` takes a
// focus ring away.
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const here = dirname(fileURLToPath(import.meta.url));

function stylesheets(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) return stylesheets(path);
    return entry.name.endsWith(".css") ? [path] : [];
  });
}
const sheets = new Map(
  stylesheets(here).map((path) => [
    relative(here, path).replaceAll("\\", "/"),
    readFileSync(path, "utf8"),
  ]),
);

/** A sheet without comments, split into its forced-colors blocks and the rest. */
function split(css: string): { normal: string; forced: string[] } {
  const text = css.replace(/\/\*[\s\S]*?\*\//g, "");
  const forced: string[] = [];
  let normal = "";
  let at = 0;
  const opening = /@media\s*\(forced-colors:\s*active\)\s*{/g;
  for (const match of text.matchAll(opening)) {
    const from = match.index ?? 0;
    if (from < at) continue;
    let depth = 1;
    let to = from + match[0].length;
    while (depth > 0 && to < text.length) {
      if (text[to] === "{") depth++;
      else if (text[to] === "}") depth--;
      to++;
    }
    normal += text.slice(at, from);
    forced.push(text.slice(from + match[0].length, to - 1));
    at = to;
  }
  return { normal: normal + text.slice(at), forced };
}
/** Only the selectors of a block: the bodies of its rules are dropped. */
const selectorsOf = (css: string) =>
  css.replace(/{[^{}]*}/g, "{}").replace(/\s+/g, " ");
const forced = (name: string) =>
  selectorsOf(split(sheets.get(name) ?? "").forced.join("\n"));
/**
 * The selectors the forced-colors blocks of a sheet style, one by one (a
 * list is split at its top-level commas). A required selector has to be one of
 * them: matching it as text would let `.switch` be satisfied by
 * `.switch::after`, or a selector be found inside a longer one.
 */
function forcedSelectors(name: string): string[] {
  const found = new Set<string>();
  for (const list of forced(name).split("{}")) {
    let depth = 0;
    let current = "";
    for (const char of list) {
      if (char === "(" || char === "[") depth++;
      if (char === ")" || char === "]") depth--;
      if (char === "," && depth === 0) {
        found.add(current.trim());
        current = "";
      } else current += char;
    }
    if (current.trim()) found.add(current.trim());
  }
  return [...found];
}

// [sheet, selector that must be in its forced-colors block, why]
const required: [string, string, string][] = [
  ["ui/ui.css", ".switch", "track edge: the off track was only a fill"],
  [
    "ui/ui.css",
    '.switch[aria-checked="true"]',
    "on is Highlight; the thumb moves as well",
  ],
  ["ui/ui.css", ".switch::after", "thumb"],
  ["ui/ui.css", ".segmented", "outer edge"],
  [
    "ui/ui.css",
    '.segmented > button[aria-checked="true"]',
    "the selected one was only a fill",
  ],
  ["ui/ui.css", ".menu-item:focus-visible", "focus was a faint fill"],
  ["ui/ui.css", ".menu-item:hover:not(:disabled)", "hover"],
  ["ui/ui.css", ".menu-radio", "radio mark"],
  ["ui/ui.css", '.menu-item[aria-checked="true"] .menu-radio', "checked radio"],
  ["ui/ui.css", ".input:focus", "focus was a border color"],
  ["ui/ui.css", ".select:focus", "focus was a border color"],
  ["ui/ui.css", ".btn-primary", "a button without an edge"],
  ["ui/ui.css", ".btn-ghost", "a button without an edge"],
  ["ui/ui.css", ".icon-btn.bare", "transparent edge"],
  ["shell.css", '.nav-item[aria-current="page"]', "current page was a fill"],
  ["shell.css", ".nav-alert", "red dot"],
  ["shell.css", ".status-dot", "status dot"],
  ["shell.css", ".connection-indicator", "connection dot"],
  [
    "controller-3d.css",
    '.stage-chip[aria-pressed="true"]',
    "selected chip: border color and shadow",
  ],
  [
    "controller-3d.css",
    '.stage-chip[data-active="true"]',
    "pressed control: border color",
  ],
  ["analog/analog-editor.css", ".analog-bar", "bar edge"],
  ["analog/analog-editor.css", ".analog-bar-fill", "bar fill"],
  ["analog/analog-editor.css", ".analog-bar-mark", "press and release marks"],
  [
    "analog/analog-editor.css",
    ".analog-range::-webkit-slider-runnable-track",
    "the gradient track is dropped",
  ],
  ["analog/analog-editor.css", ".analog-range::-webkit-slider-thumb", "thumb"],
  [
    "analog/analog-editor.css",
    ".analog-range:focus-visible",
    "focus was a shadow",
  ],
  ["analog/analog-editor.css", ".analog-number:focus", "focus was a border"],
  [
    "analog/analog-editor.css",
    '.analog-cell[aria-expanded="true"]',
    "open cell: border color",
  ],
  [
    "drawer.css",
    '.drawer-shortcut[data-active="true"] .drawer-capture',
    "recording: border color and shadow",
  ],
  [
    "drawer.css",
    '.drawer-step[data-active="true"] .drawer-step-row',
    "recording a step",
  ],
  ["drawer.css", ".drawer-rec-dot", "recording dot"],
  ["pages/pages.css", ".page-search:focus-within", "focus was a border color"],
  [
    "pages/pages.css",
    ".calibration-range::-webkit-slider-runnable-track",
    "the gradient track is dropped",
  ],
  ["pages/pages.css", ".page-range-input::-webkit-slider-thumb", "thumb"],
  [
    "pages/pages.css",
    '.page-toggle[aria-pressed="true"]',
    "pressed toggle was a fill",
  ],
  [
    "pages/pages.css",
    '.wizard-step[aria-current="step"]',
    "current step was a fill",
  ],
  ["pages/profiles.css", ".app-choice:focus-visible", "focus was a faint fill"],
  ["pages/profiles.css", '.profile-card[data-active="true"]', "active card"],
  ["pages/profiles.css", '.profile-card[data-flash="true"]', "flash"],
];

describe("forced colors (Windows high contrast)", () => {
  for (const [sheet, selector, why] of required)
    it(`${sheet} has ${selector} (${why})`, () => {
      expect(sheets.has(sheet), sheet).toBe(true);
      expect(forcedSelectors(sheet)).toContain(selector);
    });

  it("matches whole selectors, not a part of one", () => {
    const ui = forcedSelectors("ui/ui.css");
    // `.switch` and `.switch::after` are two entries, not one found in the other.
    expect(ui).toContain(".switch");
    expect(ui).toContain(".switch::after");
    expect(ui).not.toContain(".switch[aria-checked");
    expect(ui).not.toContain("switch");
  });

  it("gives the status dots a color in every state that has its own", () => {
    const shell = forcedSelectors("shell.css");
    for (const state of ["paused", "suspended", "capturing"])
      expect(shell).toContain(
        `.status-pill[data-state="${state}"] .status-dot`,
      );
    for (const phase of ["connected", "connecting", "disconnected"])
      expect(shell).toContain(
        `.rail-connection[data-phase="${phase}"] .connection-indicator`,
      );
  });

  it("uses system colors only inside the forced-colors blocks", () => {
    const system =
      /\b(ButtonText|ButtonFace|Highlight|HighlightText|CanvasText|GrayText|Mark)\b/;
    // A system color outside the blocks would change the normal theme.
    for (const [name, css] of sheets)
      expect(
        split(css)
          .normal.split("\n")
          .filter((line) => system.test(line)),
        name,
      ).toEqual([]);
  });
});

// A rule that takes the ring away must say what shows focus instead. The
// rules listed show it another way in the normal theme (a border color) and
// get an outline in the forced one (checked above); the others are named here
// once, so a new `outline: none` is a decision and not an accident.
const ringless = [
  "controller-3d.css:.controller-3d",
  "drawer.css:.drawer-title:focus:not(:focus-visible)",
  "pages/pages.css:.page-search input",
  "ui/ui.css:.input:focus, .select:focus",
  "analog/analog-editor.css:.analog-number:focus",
].sort();
describe("outline: none", () => {
  it("is only used where focus is shown another way", () => {
    const found: string[] = [];
    for (const [name, css] of sheets)
      for (const rule of split(css).normal.matchAll(/([^{}]+){([^{}]*)}/g))
        if (/outline\s*:\s*none/.test(rule[2]))
          found.push(`${name}:${rule[1].replace(/\s+/g, " ").trim()}`);
    expect(found.sort()).toEqual(ringless);
  });
});
