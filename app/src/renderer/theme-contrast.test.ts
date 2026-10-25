// UI-24: contrast of the colors the stylesheets set, computed from the CSS
// text. axe cannot do this in jsdom (no layout) and has no check for the
// contrast of a control's border (WCAG 1.4.11), so this test is what gates
// `task ci`; the Electron pass of app/tests/e2e/a11y.spec.ts looks at the
// rendered page. Change a token and this fails until the pair still passes.
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
const files = stylesheets(here);
const read = (name: string) => readFileSync(join(here, name), "utf8");
const theme = read("theme.css");

/** The custom properties of :root. */
const tokens = new Map<string, string>();
for (const match of (theme.match(/:root\s*{[^}]*}/) ?? [""])[0].matchAll(
  /(--[\w-]+)\s*:\s*([^;]+);/g,
))
  tokens.set(match[1], match[2].trim());
const token = (name: string): string => {
  const value = tokens.get(name);
  if (!value) throw new Error(`theme.css has no ${name}`);
  return value;
};

/** The value of a declaration in the first rule that is exactly `selector`. */
function declared(css: string, selector: string, property: string): string {
  const escaped = selector
    .replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
    // Line breaks and indentation in a selector are the formatter's.
    .replace(/\s+/g, "\\s+");
  const rule = new RegExp(`(?:^|})\\s*${escaped}\\s*{([^}]*)}`, "m").exec(css);
  const value = new RegExp(`(?:^|[;\\s])${property}\\s*:\\s*([^;]+)`).exec(
    rule?.[1] ?? "",
  );
  if (!value) throw new Error(`${selector} sets no ${property}`);
  return value[1].trim();
}

type RGB = [number, number, number];
function rgb(color: string): RGB {
  const hex = /^#([0-9a-f]{6})$/i.exec(color.trim());
  if (!hex) throw new Error(`not a #rrggbb color: ${color}`);
  const n = Number.parseInt(hex[1], 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}
function luminance(color: string): number {
  const [r, g, b] = rgb(color).map((channel) => {
    const value = channel / 255;
    return value <= 0.03928 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}
/** WCAG 2.x contrast ratio. */
function contrast(a: string, b: string): number {
  const [light, dark] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (light + 0.05) / (dark + 0.05);
}

describe("contrast helper", () => {
  it("matches the WCAG reference values", () => {
    expect(contrast("#000000", "#ffffff")).toBeCloseTo(21, 5);
    expect(contrast("#777777", "#ffffff")).toBeCloseTo(4.48, 2);
    expect(contrast("#ffffff", "#ffffff")).toBeCloseTo(1, 5);
  });
});

// What text sits on: the page, the cards, menus and hovered rows, and the
// components that bring their own background.
const ui = read("ui/ui.css");
const surfaces: [name: string, color: string][] = [
  ["bg", token("--bg")],
  ["surface", token("--surface")],
  ["surface-2", token("--surface-2")],
  ["surface-3", token("--surface-3")],
  ["field", token("--field")],
  [".dialog", declared(ui, ".dialog", "background")],
  [".kc", declared(ui, ".kc", "background")],
  [".tag", declared(ui, ".tag", "background")],
];

describe("text tokens (WCAG 1.4.3, 4.5:1)", () => {
  for (const name of ["--text", "--text-2", "--text-3", "--text-4"])
    for (const [surface, color] of surfaces)
      it(`${name} on ${surface}`, () => {
        expect(contrast(token(name), color)).toBeGreaterThanOrEqual(4.5);
      });
  for (const name of [
    "--accent",
    "--amber",
    "--red",
    "--green",
    "--violet",
    "--pink",
  ])
    for (const [surface, color] of surfaces.slice(0, 2))
      it(`${name} on ${surface}`, () => {
        expect(contrast(token(name), color)).toBeGreaterThanOrEqual(4.5);
      });
  it("keeps the hierarchy: each step is dimmer than the one before", () => {
    const order = ["--text", "--text-2", "--text-3", "--text-4"].map((name) =>
      luminance(token(name)),
    );
    for (let i = 1; i < order.length; i++)
      expect(order[i]).toBeLessThan(order[i - 1]);
  });
});

describe("control boundaries (WCAG 1.4.11, 3:1)", () => {
  for (const [surface, color] of surfaces.slice(0, 5))
    it(`--control-border on ${surface}`, () => {
      expect(contrast(token("--control-border"), color)).toBeGreaterThanOrEqual(
        3,
      );
    });
  it("is what inputs, selects, the segmented control and the capture box use", () => {
    const uses = (css: string, selector: string, property = "border") =>
      declared(css, selector, property);
    expect(uses(ui, ".input,\n.select")).toContain("var(--control-border)");
    expect(uses(ui, ".segmented")).toContain("var(--control-border)");
    expect(uses(read("drawer.css"), ".drawer-capture")).toContain(
      "var(--control-border)",
    );
  });
  it("is the switch track while off, and the thumb stays visible on it", () => {
    expect(declared(ui, ".switch", "background")).toBe("var(--control-border)");
    // Position carries the state; the thumb only has to be seen (3:1).
    expect(
      contrast(
        declared(ui, ".switch::after", "background"),
        token("--control-border"),
      ),
    ).toBeGreaterThanOrEqual(2.5);
  });
});

describe("placeholders", () => {
  it("use --text-4, which passes on the field", () => {
    expect(declared(ui, ".input::placeholder", "color")).toBe("var(--text-4)");
    expect(
      declared(
        read("pages/pages.css"),
        ".page-search input::placeholder",
        "color",
      ),
    ).toBe("var(--text-4)");
  });
});

// A text color written as a literal in a stylesheet is checked against the
// lightest surface text sits on, so a dim one cannot slip back in. A color
// that sits on a bright background of its own (dark ink on a face color) is
// listed with the reason.
const lightest = token("--surface-3");
const inkOnBright: Record<string, string> = {
  "controller-3d.css": "dark ink on the face color of a pressed chip glyph",
};
describe("literal text colors in the stylesheets", () => {
  for (const file of files) {
    const name = relative(here, file).replaceAll("\\", "/");
    const css = readFileSync(file, "utf8");
    const literals = [
      ...new Set(
        [...css.matchAll(/(?<![-\w])color\s*:\s*(#[0-9a-f]{6})\b/gi)].map(
          (match) => match[1].toLowerCase(),
        ),
      ),
    ];
    if (!literals.length) continue;
    it(name, () => {
      const dim = literals.filter((color) => contrast(color, lightest) < 4.5);
      const allowed = Object.entries(inkOnBright).some(([file]) =>
        name.endsWith(file),
      );
      // A listed file may hold dark ink, but nothing in the middle.
      if (allowed)
        expect(dim.filter((color) => contrast(color, "#ffffff") < 7)).toEqual(
          [],
        );
      else expect(dim).toEqual([]);
    });
  }
});

// Text dimmed with opacity falls below 4.5:1 without any token changing, so
// none of the checks above sees it (a --text-3 label at 70 % over --surface is
// 3.9:1). Dim text with a color from the tokens instead. What may keep a
// partial opacity is listed with the reason: graphics, a state that is being
// animated, and the controls and icons that are switched off (WCAG 1.4.3
// leaves inactive components out).
const dimmed: Record<string, string> = {
  "analog/analog-editor.css:.analog-v-sector": "an arc of the stick diagram",
  "analog/analog-editor.css:.analog-number:disabled, .analog-range:disabled":
    "a control that is switched off",
  "controller-3d.css:.stage-line-halo": "a line of the controller diagram",
  "controller-3d.css:.stage-gauge-hub": "a mark of the controller diagram",
  "controller-3d.css:.stage-gauge-halo": "a line of the controller diagram",
  "drawer.css:50%": "the pulse of the recording dot (a keyframe)",
  "pages/pages.css:.calibration-range:disabled, .page-range-input:disabled":
    "a control that is switched off",
  'pages/profiles.css:.app-icon[data-enabled="false"] .app-icon-image, .app-icon[data-enabled="false"] .app-icon-generic':
    "the icon of an app that is switched off",
  'pages/profiles.css:.page-column-profiles[data-switching="off"] .app-icon':
    "icons only, while switching by app is off",
  "theme.css:button:disabled": "a control that is switched off",
};
describe("opacity below 1", () => {
  it("is only used where nothing readable is dimmed by it", () => {
    const found: string[] = [];
    for (const file of files) {
      const name = relative(here, file).replaceAll("\\", "/");
      const css = readFileSync(file, "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
      for (const rule of css.matchAll(/([^{}]+){([^{}]*)}/g)) {
        const value = /(?<![-\w])opacity\s*:\s*([\d.]+)/.exec(rule[2])?.[1];
        if (value && Number(value) > 0 && Number(value) < 1)
          found.push(`${name}:${rule[1].replace(/\s+/g, " ").trim()}`);
      }
    }
    expect(found.sort()).toEqual(Object.keys(dimmed).sort());
  });

  it("dims the old setting of a preset and a greyed chip with a token color", () => {
    const analog = read("analog/analog-editor.css");
    expect(declared(analog, ".analog-change-old", "color")).toBe(
      "var(--text-3)",
    );
    const chips = read("controller-3d.css");
    expect(
      declared(
        chips,
        '.stage-chip[data-inherited]:not([data-state="unbound"]):not([data-missing]) .stage-chip-title,\n.stage-chip[data-inherited] .kc',
        "color",
      ),
    ).toBe("var(--text-3)");
  });
});
