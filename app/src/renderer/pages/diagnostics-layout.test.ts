// The layout of the diagnostics page's event list (UI-20), as far as a test
// without a layout engine can say it: jsdom has no widths, so the rules that
// keep a wide badge or an English gesture name from running into the next
// column are read from the style sheet. The widths themselves were measured in
// Chromium (ARCHITECTURE §12, "诊断").
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const css = readFileSync(
  new URL("./pages.css", import.meta.url),
  "utf8",
).replace(/\/\*[\s\S]*?\*\//g, "");

/** The declarations of the rules whose selector list has `selector`, later ones over earlier. */
function rule(selector: string): Record<string, string> {
  const declarations: Record<string, string> = {};
  for (const match of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const selectors = match[1].split(",").map((entry) => entry.trim());
    if (!selectors.includes(selector)) continue;
    for (const declaration of match[2].split(";")) {
      const colon = declaration.indexOf(":");
      if (colon > 0)
        declarations[declaration.slice(0, colon).trim()] = declaration
          .slice(colon + 1)
          .replace(/\s+/g, " ")
          .trim();
    }
  }
  return declarations;
}

/** The tracks of a grid-template-columns value, a function with its arguments being one. */
function tracks(value: string | undefined): string[] {
  const found: string[] = [];
  let depth = 0;
  let current = "";
  for (const char of value ?? "") {
    if (char === "(") depth += 1;
    if (char === ")") depth -= 1;
    if (char === " " && depth === 0) {
      if (current) found.push(current);
      current = "";
    } else current += char;
  }
  if (current) found.push(current);
  return found;
}

describe("the columns of the event list", () => {
  it("belong to the list, and each row takes them over, so every row lines up", () => {
    const list = rule(".page-act-list");
    expect(list.display).toBe("grid");
    const row = rule(".page-act-row");
    expect(row.display).toBe("grid");
    expect(row["grid-template-columns"]).toBe("subgrid");
    expect(row["grid-column"]).toBe("1 / -1");
  });

  it("are as wide as the key and the gesture they show, not a width that only face buttons and Chinese fit", () => {
    // time, key, gesture, where, result
    const single = tracks(rule(".page-act-list")["grid-template-columns"]);
    expect(single).toHaveLength(5);
    expect(single[1]).toBe("max-content");
    expect(single[2]).toBe("max-content");
    // time, pad, key, gesture, where, result
    const multi = tracks(
      rule(".page-act-list[data-pad]")["grid-template-columns"],
    );
    expect(multi).toHaveLength(6);
    expect(multi[2]).toBe("max-content");
    expect(multi[3]).toBe("max-content");
  });

  it("keep the text of the gesture on one line, and the least width of the key and the gesture", () => {
    expect(rule(".page-act-gesture")["white-space"]).toBe("nowrap");
    // What the common rows (a face button, a Chinese gesture) always had.
    expect(rule(".page-act-key")["min-width"]).toBe("30px");
    expect(rule(".page-act-gesture")["min-width"]).toBe("52px");
  });

  it("cut a long name of the pad or of the place with an ellipsis instead of widening the list", () => {
    const place = rule(".page-act-where");
    expect(place["text-overflow"]).toBe("ellipsis");
    expect(place.overflow).toBe("hidden");
    const columns = tracks(rule(".page-act-list")["grid-template-columns"]);
    expect(columns[3]).toMatch(/^fit-content\(\d+px\)$/);
    const withPad = tracks(
      rule(".page-act-list[data-pad]")["grid-template-columns"],
    );
    expect(withPad[1]).toMatch(/^fit-content\(\d+px\)$/);
    expect(withPad[4]).toMatch(/^fit-content\(\d+px\)$/);
    // The result takes what is left, and may shrink to wrap its text.
    expect(columns[4]).toBe("minmax(0, 1fr)");
    expect(withPad[5]).toBe("minmax(0, 1fr)");
  });
});
