// Reads the user documents (ENG-17): the pieces of a Markdown file that the
// two languages of a document must share. It is not a Markdown parser. It
// knows what these documents use: ATX headings, fenced code, inline code,
// lists, tables, quotes and inline links.
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, posix, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { inMirror } from "../mirror";

const here = dirname(fileURLToPath(import.meta.url));

/** The repository root. */
export const repoRoot = join(here, "..", "..", "..");

/** One user document in both languages, by path from the repository root. */
export interface DocPair {
  zh: string;
  en: string;
}

const guideDir = join(repoRoot, "docs", "guide");

/**
 * The README of the public code mirror, in both languages. It is written for
 * the root of the mirror, where it is README.md; the development repository
 * keeps its source in daemon/tools/mirror, and it is not a user document that
 * the installer ships, so userDocPairs does not list it.
 */
export const mirrorReadmePair: DocPair = {
  zh: "daemon/tools/mirror/README.md",
  en: "daemon/tools/mirror/README.en.md",
};

/**
 * The files of docs/guide, relative to the repository root, in name order. The
 * public code mirror has no documents, and the tests that read them skip
 * there: the list is empty.
 */
export function guideFiles(): string[] {
  if (inMirror) return [];
  return readdirSync(guideDir)
    .sort()
    .map((name) => posix.join("docs", "guide", name));
}

/** The Chinese file of each pair in docs/guide, with its English twin. */
export function guidePairs(): DocPair[] {
  return guideFiles()
    .filter((file) => file.endsWith(".md") && !file.endsWith(".en.md"))
    .map((zh) => ({ zh, en: zh.replace(/\.md$/, ".en.md") }));
}

/** Every user document that has two languages. */
export function userDocPairs(): DocPair[] {
  return [
    { zh: "README.md", en: "README.en.md" },
    { zh: "docs/quickstart.md", en: "docs/quickstart.en.md" },
    { zh: "docs/known-limitations.md", en: "docs/known-limitations.en.md" },
    {
      zh: "docs/troubleshooting-input.md",
      en: "docs/troubleshooting-input.en.md",
    },
    ...guidePairs(),
  ];
}

export function read(file: string): string {
  return readFileSync(join(repoRoot, ...file.split("/")), "utf8");
}

export function exists(file: string): boolean {
  return existsSync(join(repoRoot, ...file.split("/")));
}

/** The files of a folder and below it, relative to the repository root. */
export function filesUnder(folder: string): string[] {
  const base = join(repoRoot, ...folder.split("/"));
  return readdirSync(base, { withFileTypes: true }).flatMap((entry) => {
    const path = join(base, entry.name);
    return entry.isDirectory()
      ? filesUnder(posix.join(folder, entry.name))
      : [relative(repoRoot, path).split(sep).join("/")];
  });
}

/** Whether the path is a folder (a link to a folder is still a link). */
export function isFolder(file: string): boolean {
  return (
    existsSync(join(repoRoot, ...file.split("/"))) &&
    statSync(join(repoRoot, ...file.split("/"))).isDirectory()
  );
}

const fence = /^\s*(`{3,}|~{3,})/;

/** The text outside fenced code and inline code. */
export function prose(markdown: string): string {
  const kept: string[] = [];
  let closing: string | null = null;
  for (const line of markdown.split(/\r?\n/)) {
    const match = fence.exec(line);
    if (closing) {
      if (
        match &&
        match[1][0] === closing[0] &&
        match[1].length >= closing.length
      )
        closing = null;
      continue;
    }
    if (match) {
      closing = match[1];
      continue;
    }
    kept.push(line.replace(/`[^`\n]*`/g, ""));
  }
  return kept.join("\n");
}

/** The contents of the fenced code blocks, with their opening line. */
export function codeBlocks(markdown: string): string[] {
  const blocks: string[] = [];
  let closing: string | null = null;
  let current: string[] = [];
  for (const line of markdown.split(/\r?\n/)) {
    const match = fence.exec(line);
    if (closing) {
      if (
        match &&
        match[1][0] === closing[0] &&
        match[1].length >= closing.length
      ) {
        blocks.push(current.join("\n"));
        closing = null;
      } else current.push(line);
      continue;
    }
    if (match) {
      closing = match[1];
      current = [line.trim()];
    }
  }
  if (closing) blocks.push(current.join("\n"));
  return blocks;
}

/** The names a document quotes with “…”, in order, outside code. */
export function quotedSpans(markdown: string): string[] {
  return [...prose(markdown).matchAll(/“([^“”\n]+)”/g)].map((m) => m[1]);
}

/** Quote marks that are left open or are not the curly pair. */
export function strayQuotes(markdown: string): string[] {
  const text = prose(markdown).replace(/“[^“”\n]*”/g, "");
  return [...text.matchAll(/[“”]/g)].map((m) => m[0]);
}

/** The link targets of the document, in order, outside code. */
export function linkTargets(markdown: string): string[] {
  return [
    ...prose(markdown).matchAll(/\[[^\]\n]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)/g),
  ].map((m) => m[1]);
}

export function isExternal(target: string): boolean {
  return /^[a-z][a-z0-9+.-]*:/i.test(target);
}

/**
 * The link target as it would be in the other language: a link to a file
 * that has an English twin names the twin, so `.en` is taken out.
 */
export function withoutEn(target: string): string {
  return target.replace(/\.en\.md(?=$|#)/, ".md");
}

/** One block of a document: its kind (see blockKinds) and its text. */
export interface Block {
  kind: string;
  text: string;
}

/**
 * What a document is made of, block by block, outside code: a heading with its
 * level, a list item with its kind and depth, a table row, a quote line, a
 * paragraph, and a code block, each with its text.
 */
export function blocks(markdown: string): Block[] {
  const out: Block[] = [];
  let closing: string | null = null;
  let previous = "blank";
  const more = (text: string) => {
    const last = out[out.length - 1];
    if (last) last.text += ` ${text.trim()}`;
  };
  for (const line of markdown.split(/\r?\n/)) {
    const match = fence.exec(line);
    if (closing) {
      if (
        match &&
        match[1][0] === closing[0] &&
        match[1].length >= closing.length
      ) {
        closing = null;
        previous = "code";
      } else more(line);
      continue;
    }
    if (match) {
      closing = match[1];
      out.push({ kind: "code", text: "" });
      continue;
    }
    if (line.trim() === "") {
      previous = "blank";
      continue;
    }
    const heading = /^(#{1,6})\s+(.*)$/.exec(line);
    const item = /^(\s*)([-*+]|\d+\.)\s+(.*)$/.exec(line);
    if (heading) {
      out.push({ kind: `h${heading[1].length}`, text: heading[2] });
      previous = "heading";
    } else if (item) {
      const depth = Math.floor(item[1].length / 2);
      out.push({
        kind: `${/\d/.test(item[2]) ? "ol" : "ul"}${depth}`,
        text: item[3],
      });
      previous = "item";
    } else if (/^\s*\|/.test(line)) {
      out.push({ kind: "tr", text: line.trim() });
      previous = "row";
    } else if (/^>/.test(line)) {
      if (previous !== "quote") out.push({ kind: "bq", text: "" });
      more(line.replace(/^>\s?/, ""));
      previous = "quote";
    } else if (
      previous === "blank" ||
      previous === "heading" ||
      previous === "code"
    ) {
      out.push({ kind: "p", text: line.trim() });
      previous = "paragraph";
    } else if (previous === "row") {
      // A line under a table with no blank line between: part of the table.
    } else more(line);
    // Otherwise the line continues the item or paragraph above it.
  }
  return out;
}

/** The kinds of the blocks, so that two documents that say the same have the same list. */
export function blockKinds(markdown: string): string[] {
  return blocks(markdown).map((block) => block.kind);
}

/** The text of the inline code spans, in order, outside fenced code. */
export function inlineCode(markdown: string): string[] {
  const spans: string[] = [];
  let closing: string | null = null;
  for (const line of markdown.split(/\r?\n/)) {
    const match = fence.exec(line);
    if (closing) {
      if (
        match &&
        match[1][0] === closing[0] &&
        match[1].length >= closing.length
      )
        closing = null;
      continue;
    }
    if (match) {
      closing = match[1];
      continue;
    }
    for (const m of line.matchAll(/`([^`\n]*)`/g)) spans.push(m[1]);
  }
  return spans;
}

/** The prose without its quoted names, link targets and list numbers. */
function bare(markdown: string): string {
  return prose(markdown)
    .replace(/\]\([^)\s]*\)/g, "]")
    .replace(/^\s*\d+\.\s/gm, "")
    .replace(/“[^“”\n]*”/g, "");
}

/**
 * The numbers in the prose, in order, outside code, quoted names and link
 * targets: "250 ms" is 250 in either language. A number of a name (“8 方向”)
 * belongs to the name, which the table checks.
 */
export function numbers(markdown: string): string[] {
  return [...bare(markdown).matchAll(/\d+(?:\.\d+)?/g)].map((m) => m[0]);
}

const chinese = /[\u3000-\u303f\u3400-\u4dbf\u4e00-\u9fff\uff00-\uffef]+/g;

/**
 * The runs of Chinese text in a document that is meant to be English: not in
 * code, not in a link target, not the text of the link to the Chinese twin,
 * and not one of the `allowed` texts (names that the app never translates).
 */
export function chineseText(
  markdown: string,
  allowed: readonly string[],
): string[] {
  let text = prose(markdown)
    .replace(/\[中文\]\([^)\s]*\)/g, "")
    .replace(/\]\([^)\s]*\)/g, "]");
  for (const part of allowed) text = text.split(part).join("");
  return [...text.matchAll(chinese)].map((m) => m[0]);
}

/** The headings of a document, without the number before the title. */
export function headings(markdown: string): string[] {
  return blocks(markdown)
    .filter((block) => /^h\d$/.test(block.kind))
    .map((block) => block.text.replace(/^\d+\.\s+/, "").trim());
}

/** The number of cells of each table row, in order. */
export function tableShapes(markdown: string): number[] {
  return prose(markdown)
    .split("\n")
    .filter((line) => /^\s*\|/.test(line))
    .map(
      (line) =>
        line
          .trim()
          .replace(/^\||\|$/g, "")
          .split("|").length,
    );
}

/**
 * Where a link goes, from the repository root: relative to the file that has
 * it. Null for an address on the web or a link to the same page.
 */
export function resolveLink(from: string, target: string): string | null {
  if (isExternal(target) || target.startsWith("#")) return null;
  const path = target.replace(/#.*$/, "");
  return posix.normalize(posix.join(posix.dirname(from), path));
}
