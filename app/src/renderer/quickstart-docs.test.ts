// ENG-18, ENG-17: the user documents name the buttons of the app. This test
// keeps the two from drifting apart. Every name in the table of
// app/tests/docs/names.ts must still be a pair of strings of the app, a
// Chinese one and an English one that sit next to each other in the source
// (renderer sources, the tray menu of padmated, the texts of the installer),
// and the documents must still use it. Change a button's text, in one
// language or both, and this fails until the table and the documents say the
// same. user-docs.test.ts checks the other direction: that every quoted name
// in a document is in the table.
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { quotedSpans, read, userDocPairs } from "../../tests/docs/markdown";
import { names, type Source, trayNames } from "../../tests/docs/names";
import { inMirror } from "../../tests/mirror";

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, "..", "..", "..");

/** Joins the two texts of a pair into the key the tables use. */
const pairKey = (zh: string, en: string) => `${zh}\u0000${en}`;

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) return sources(path);
    return /\.tsx?$/.test(entry.name) && !/\.test\.tsx?$/.test(entry.name)
      ? [readFileSync(path, "utf8")]
      : [];
  });
}

/** A double-quoted string literal of a source file. */
const literal = String.raw`"((?:[^"\\\r\n]|\\.)*)"`;

/** The text a string literal stands for, or null if it is not a plain one. */
function readLiteral(body: string): string | null {
  try {
    return JSON.parse(`"${body}"`) as string;
  } catch {
    return null;
  }
}

const hasChinese = (text: string) => /[一-鿿]/.test(text);

/**
 * The pairs of strings the renderer holds: a Chinese literal and an English
 * one side by side ("zh", "en"), as the arguments of t() and tx(), in an
 * array, as `zh: "…", en: "…"` of an object, or in the order of a ternary
 * (`language === "en" ? "…" : "…"`). The literals of the source are read in
 * turn, so "a", "b", "c" is the two pairs a-b and b-c.
 */
function rendererPairs(text: string): Set<string> {
  const pairs = new Set<string>();
  const found = [...text.matchAll(new RegExp(literal, "g"))];
  const add = (zhBody: string, enBody: string) => {
    const zh = readLiteral(zhBody);
    const en = readLiteral(enBody);
    if (zh !== null && en !== null && hasChinese(zh) && !hasChinese(en) && en)
      pairs.add(pairKey(zh, en));
  };
  found.forEach((first, index) => {
    const second = found[index + 1];
    if (!second) return;
    const between = text.slice(first.index + first[0].length, second.index);
    const before = text.slice(Math.max(0, first.index - 12), first.index);
    if (/^\s*,\s*$/.test(between)) add(first[1], second[1]);
    else if (/^\s*,\s*en:\s*$/.test(between) && /\bzh:\s*$/.test(before))
      add(first[1], second[1]);
    else if (/^\s*:\s*$/.test(between) && /\?\s*$/.test(before))
      add(second[1], first[1]);
  });
  return pairs;
}

/** i18n.ts keeps one block per language; the keys line the two up. */
function i18nPairs(text: string): Set<string> {
  const pairs = new Set<string>();
  const start = text.indexOf("const zh = {");
  const middle = text.indexOf("const en");
  if (start < 0 || middle < 0) return pairs;
  const block = (body: string) => {
    const values = new Map<string, string>();
    const item = new RegExp(`^ {2}(\\w+):\\s*${literal}`, "gm");
    for (const m of body.matchAll(item)) {
      const value = readLiteral(m[2]);
      if (value !== null) values.set(m[1], value);
    }
    return values;
  };
  const zh = block(text.slice(start, middle));
  const en = block(text.slice(middle));
  for (const [key, value] of zh) {
    const other = en.get(key);
    if (other && hasChinese(value) && !hasChinese(other))
      pairs.add(pairKey(value, other));
  }
  return pairs;
}

/** The texts of the installer: a LangString has an English and a Chinese text. */
function installerPairs(text: string): Set<string> {
  const english = new Map<string, string>();
  const chinese = new Map<string, string>();
  const item =
    /LangString\s+(\w+)\s+\$\{LANG_(ENGLISH|SIMPCHINESE)\}\s+"([^"]*)"/g;
  for (const m of text.matchAll(item))
    (m[2] === "ENGLISH" ? english : chinese).set(m[1], m[3]);
  const pairs = new Set<string>();
  for (const [key, en] of english) {
    const zh = chinese.get(key);
    if (!zh) continue;
    // The text up to its first line break; "$\r$\n" is NSIS's.
    const line = (value: string) => value.split("$\\r$\\n")[0];
    pairs.add(pairKey(line(zh), line(en)));
  }
  return pairs;
}

const rendererTable = new Set([
  ...rendererPairs(sources(here).join("\n")),
  ...i18nPairs(readFileSync(join(here, "i18n.ts"), "utf8")),
]);
const installerTable = installerPairs(
  readFileSync(join(root, "app", "build", "installer.nsh"), "utf8"),
);
const tray = readFileSync(
  join(root, "daemon", "cmd", "padmated", "tray_windows.go"),
  "utf8",
);

// The public code mirror has no documents (daemon/internal/publicmirror): the
// tests that check them are skipped there, and only there.
const docPairs = inMirror ? [] : userDocPairs();
const zhSpans = new Set(docPairs.flatMap((pair) => quotedSpans(read(pair.zh))));
const enSpans = new Set(docPairs.flatMap((pair) => quotedSpans(read(pair.en))));

// The table with the source of each row filled in, for it.each.
const rows = names.map(([zh, en, source]): [string, string, Source] => [
  zh,
  en,
  source ?? "renderer",
]);

describe("the names of the app that the documents quote", () => {
  it("lists no name twice", () => {
    const seen = new Set<string>();
    for (const [zh, en] of names) {
      const key = pairKey(zh, en);
      expect(seen.has(key), `“${zh}” / “${en}” is listed twice`).toBe(false);
      seen.add(key);
    }
  });

  it.each(rows)("the app has %s / %s as a pair", (zh, en, source) => {
    const table = source === "installer" ? installerTable : rendererTable;
    expect(
      table.has(pairKey(zh, en)),
      `no adjacent pair “${zh}” / “${en}” in the ${source} sources`,
    ).toBe(true);
  });

  it.skipIf(inMirror).each(rows)("a Chinese document uses %s", (zh) => {
    expect(zhSpans.has(zh), `no Chinese document quotes “${zh}”`).toBe(true);
  });

  it.skipIf(inMirror).each(rows)("an English document uses %s", (_zh, en) => {
    expect(enSpans.has(en), `no English document quotes “${en}”`).toBe(true);
  });

  it.each(trayNames)("the tray menu has %s", (name) => {
    expect(tray).toContain(`"${name}"`);
  });

  it.skipIf(inMirror).each(trayNames)("the documents name %s", (name) => {
    expect(zhSpans.has(name)).toBe(true);
    expect(enSpans.has(name)).toBe(true);
  });
});

describe("the reader of the source", () => {
  it("finds the kinds of pairs the app writes", () => {
    const pairs = rendererPairs(`
      t("确定", "OK"),
      { zh: "按下", en: "Press" }
      language === "en" ? "Keys being edited" : "编辑的按键"
      tx(language, "关于", "About")
      ["甲", "Alpha", "Beta"]
    `);
    expect(pairs.has(pairKey("确定", "OK"))).toBe(true);
    expect(pairs.has(pairKey("按下", "Press"))).toBe(true);
    expect(pairs.has(pairKey("编辑的按键", "Keys being edited"))).toBe(true);
    expect(pairs.has(pairKey("关于", "About"))).toBe(true);
    expect(pairs.has(pairKey("甲", "Alpha"))).toBe(true);
  });

  it("does not take two Chinese strings or two English ones for a pair", () => {
    expect(rendererPairs(`["上", "下"], ["Up", "Down"]`).size).toBe(0);
  });

  it("reads the aligned keys of i18n.ts", () => {
    const pairs = i18nPairs(
      'const zh = {\n  overview: "映射",\n} as const;\nconst en: Record<K, string> = {\n  overview: "Mapping",\n};\n',
    );
    expect(pairs.has(pairKey("映射", "Mapping"))).toBe(true);
  });

  it("reads the two texts of an installer message", () => {
    const pairs = installerPairs(
      // biome-ignore lint/suspicious/noTemplateCurlyInString: NSIS writes its language names like this
      'LangString k ${LANG_ENGLISH} "Keep?$\\r$\\n$\\r$\\nYes."\nLangString k ${LANG_SIMPCHINESE} "保留吗？$\\r$\\n$\\r$\\n是。"',
    );
    expect(pairs.has(pairKey("保留吗？", "Keep?"))).toBe(true);
  });
});
