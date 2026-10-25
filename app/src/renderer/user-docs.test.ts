// ENG-17: the user documents come in two languages, and the two must say the
// same thing in the same shape. For each pair (README, the quick start, the
// guide chapters, the known limitations, the input troubleshooting) this
// checks that the files match one for one, that they have the same blocks
// (headings, list items, table rows, paragraphs, code), the same code and the
// same links, the same inline code and the same numbers, that the English file
// has no Chinese in it (bar code, link targets and the names that the app does
// not translate) and no block much shorter than its Chinese twin, and that
// every name of the app that they quote with “…” is a name of the table in
// app/tests/docs/names.ts, in the same order in both. The sections that a
// document sends the reader to must still have their heading.
// quickstart-docs.test.ts checks that the table is still true of the app.
import { describe, expect, it } from "vitest";
import {
  blockKinds,
  blocks,
  chineseText,
  codeBlocks,
  exists,
  filesUnder,
  guideFiles,
  guidePairs,
  headings,
  inlineCode,
  isExternal,
  isFolder,
  linkTargets,
  mirrorReadmePair,
  numbers,
  quotedSpans,
  read,
  resolveLink,
  strayQuotes,
  tableShapes,
  userDocPairs,
  withoutEn,
} from "../../tests/docs/markdown";
import {
  chineseInEnglish,
  codePlaceholders,
  external,
  names,
  trayNames,
} from "../../tests/docs/names";
import { inMirror } from "../../tests/mirror";

/** The Chinese texts of the app and what each is called in English. */
const known = new Map<string, Set<string>>();
function know(zh: string, en: string) {
  const set = known.get(zh) ?? new Set<string>();
  set.add(en);
  known.set(zh, set);
}
for (const [zh, en] of names) know(zh, en);
for (const name of trayNames) know(name, name);
for (const { zh, en } of external) know(zh, en);

// The public code mirror has no documents (daemon/internal/publicmirror): every
// test that reads one is skipped there, and only there. Its factories still run
// when the tests are collected, so they must not read anything in the mirror.
const docs = describe.skipIf(inMirror);

// The README of the public code mirror says the same as the others do.
const pairs = [...userDocPairs(), ...(inMirror ? [] : [mirrorReadmePair])];

/** The English twin of a Chinese document. */
const twin = (zh: string) => zh.replace(/\.md$/, ".en.md");

/**
 * The shortest an English block may be, as a share of the characters of its
 * Chinese twin. The shortest block today is 1.18 times as long. The bound only
 * has to catch a block that was cut down to a word or left as a placeholder.
 */
const SHORTEST = 0.8;

/**
 * The documents of docs/ that the installed program has besides the guide
 * (UserDocuments in daemon/internal/installerpkg/documents.go).
 */
const installedDocuments = new Set(
  userDocPairs()
    .filter((pair) => pair.zh.startsWith("docs/"))
    .flatMap((pair) => [pair.zh, pair.en]),
);

/**
 * What the installed program has next to the documents (shipsFile in
 * daemon/internal/installerpkg/documents.go): the READMEs and the license, the
 * user guide and the user documents, the licenses, and the examples without
 * their READMEs. The rest of docs/ stays in the development repository.
 */
const packaged = (file: string) =>
  file === "README.md" ||
  file === "README.en.md" ||
  file === "LICENSE" ||
  file === "docs/guide" ||
  file.startsWith("docs/guide/") ||
  installedDocuments.has(file) ||
  /^licenses\//.test(file) ||
  (/^examples\//.test(file) && !/(^|\/)README\.md$/.test(file));

describe("the table of names", () => {
  it("gives every outside name a reason", () => {
    for (const entry of external)
      expect(
        entry.reason.trim().length,
        `“${entry.zh}” has no reason`,
      ).toBeGreaterThan(10);
  });

  it("keeps names of the app out of the outside list", () => {
    for (const entry of external)
      expect(
        names.some(([zh, en]) => zh === entry.zh && en === entry.en),
        `“${entry.zh}” is a name of the app: it belongs in names`,
      ).toBe(false);
  });

  it.skipIf(inMirror)("has no outside name that no document quotes", () => {
    const zhSpans = new Set(
      pairs.flatMap((pair) => quotedSpans(read(pair.zh))),
    );
    const enSpans = new Set(
      pairs.flatMap((pair) => quotedSpans(read(pair.en))),
    );
    for (const entry of external) {
      expect(
        zhSpans.has(entry.zh),
        `no Chinese document quotes “${entry.zh}”`,
      ).toBe(true);
      expect(
        enSpans.has(entry.en),
        `no English document quotes “${entry.en}”`,
      ).toBe(true);
    }
  });
});

docs("the lists of exceptions", () => {
  const zhDocs = inMirror ? [] : pairs.map((pair) => read(pair.zh));
  const enDocs = inMirror ? [] : pairs.map((pair) => read(pair.en));

  it("keeps a heading for every section that a document sends the reader to", () => {
    for (const entry of external) {
      if (!entry.heading) continue;
      expect(
        headings(read(entry.heading)),
        `${entry.heading} has no heading “${entry.zh}”`,
      ).toContain(entry.zh);
      expect(
        headings(read(twin(entry.heading))),
        `${twin(entry.heading)} has no heading “${entry.en}”`,
      ).toContain(entry.en);
    }
  });

  it("uses every placeholder of inline code, in both languages", () => {
    for (const { zh, en } of codePlaceholders) {
      expect(
        zhDocs.some((text) => inlineCode(text).includes(zh)),
        `no Chinese document has the code \`${zh}\``,
      ).toBe(true);
      expect(
        enDocs.some((text) => inlineCode(text).includes(en)),
        `no English document has the code \`${en}\``,
      ).toBe(true);
    }
  });

  it("uses every Chinese text that the English documents may keep", () => {
    for (const { text, reason } of chineseInEnglish) {
      expect(reason.trim().length, `${text} has no reason`).toBeGreaterThan(10);
      expect(
        enDocs.some((doc) => doc.includes(text)),
        `no English document has ${text}`,
      ).toBe(true);
    }
  });
});

docs("the guide folder", () => {
  it("has a Chinese and an English file for every chapter and nothing else", () => {
    const files = guideFiles();
    expect(files.length).toBeGreaterThan(2);
    const zh = files.filter((f) => !f.endsWith(".en.md")).map((f) => f);
    const en = files
      .filter((f) => f.endsWith(".en.md"))
      .map((f) => f.replace(/\.en\.md$/, ".md"));
    expect(en).toEqual(zh);
    for (const file of zh)
      expect(file, "chapters are NN-slug.md").toMatch(
        /^docs\/guide\/\d\d-[a-z0-9]+(-[a-z0-9]+)*\.md$/,
      );
  });

  it("starts with an index, and the index names every chapter in order", () => {
    const chapters = guidePairs().map((pair) => pair.zh);
    expect(chapters[0]).toBe("docs/guide/00-index.md");
    for (const [file, suffix] of [
      ["docs/guide/00-index.md", ".md"],
      ["docs/guide/00-index.en.md", ".en.md"],
    ] as const) {
      const linked = linkTargets(read(file))
        .filter((target) => /^\d\d-[a-z0-9-]+(\.en)?\.md$/.test(target))
        .filter((target) => !target.startsWith("00-"));
      expect(linked).toEqual(
        chapters
          .slice(1)
          .map((chapter) =>
            chapter.replace("docs/guide/", "").replace(/\.md$/, suffix),
          ),
      );
    }
  });
});

docs.each(pairs)("$zh and $en", ({ zh, en }) => {
  it("both exist", () => {
    expect(exists(zh), `${zh} is missing`).toBe(true);
    expect(exists(en), `${en} is missing`).toBe(true);
  });

  const zhText = exists(zh) ? read(zh) : "";
  const enText = exists(en) ? read(en) : "";
  const base = (file: string) => file.slice(file.lastIndexOf("/") + 1);

  it("each has a link to the other language near the top", () => {
    const top = (text: string) => text.split(/\r?\n/).slice(0, 8).join("\n");
    expect(linkTargets(top(zhText))).toContain(base(en));
    expect(linkTargets(top(enText))).toContain(base(zh));
  });

  it("have the same blocks in the same order", () => {
    expect(blockKinds(enText)).toEqual(blockKinds(zhText));
  });

  it("have the same code", () => {
    expect(codeBlocks(enText)).toEqual(codeBlocks(zhText));
  });

  it("have the same inline code, bar the placeholders", () => {
    // Words order differs between the languages, so the spans are compared as
    // a list in order of text; a translated placeholder is named in names.ts.
    const placeholder = new Map(codePlaceholders.map((p) => [p.zh, p.en]));
    const zhCode = inlineCode(zhText).map(
      (span) => placeholder.get(span) ?? span,
    );
    expect(inlineCode(enText).sort()).toEqual(zhCode.sort());
  });

  it("have the same numbers", () => {
    expect(numbers(enText).sort()).toEqual(numbers(zhText).sort());
  });

  it("have no Chinese in the English file, bar code, links and the names that stay Chinese", () => {
    const allowed = [
      ...trayNames,
      ...chineseInEnglish.map((entry) => entry.text),
      ...external.filter((entry) => /[一-鿿]/.test(entry.en)).map((e) => e.en),
    ];
    expect(chineseText(enText, allowed)).toEqual([]);
  });

  it("have no marker of unfinished text", () => {
    for (const text of [zhText, enText])
      expect(text).not.toMatch(/\b(?:TODO|TBD|FIXME|XXX)\b|待补|待定/);
  });

  it("have no English block much shorter than the Chinese one", () => {
    const flat = (text: string) =>
      text
        .replace(/\]\([^)\s]*\)/g, "]")
        .replace(/\s+/g, " ")
        .trim();
    const prosy = (kind: string) => kind !== "code" && !/^h\d$/.test(kind);
    const zhBlocks = blocks(zhText).filter((b) => prosy(b.kind));
    const enBlocks = blocks(enText).filter((b) => prosy(b.kind));
    expect(enBlocks.length).toBe(zhBlocks.length);
    zhBlocks.forEach((block, i) => {
      // The table's rule line, and the link to the other language.
      if (/^[\s|:-]*$/.test(block.text)) return;
      if (/^\[(?:English|中文)\]\([^)\s]*\)$/.test(block.text.trim())) return;
      const zhLength = flat(block.text).length;
      const enLength = flat(enBlocks[i].text).length;
      expect(
        enLength,
        `block ${i + 1} (${block.kind}) is ${enLength} characters in English and ${zhLength} in Chinese:
${block.text}
${enBlocks[i].text}`,
      ).toBeGreaterThanOrEqual(zhLength * SHORTEST);
    });
  });

  it("have the same table shapes", () => {
    expect(tableShapes(enText)).toEqual(tableShapes(zhText));
  });

  it("have the same links, bar the .en of a twin", () => {
    expect(linkTargets(enText).map(withoutEn)).toEqual(
      linkTargets(zhText).map(withoutEn),
    );
  });

  it("have no quote mark that is left open", () => {
    expect(strayQuotes(zhText)).toEqual([]);
    expect(strayQuotes(enText)).toEqual([]);
  });

  it("quote only names that are in the table, as the table has them", () => {
    for (const span of quotedSpans(zhText))
      expect(
        known.has(span),
        `${zh} quotes “${span}”, which is not in the table`,
      ).toBe(true);
    const english = new Set([...known.values()].flatMap((set) => [...set]));
    for (const span of quotedSpans(enText))
      expect(
        english.has(span),
        `${en} quotes “${span}”, which is not in the table`,
      ).toBe(true);
  });

  it("quote the same names in the same order", () => {
    const zhSpans = quotedSpans(zhText);
    const enSpans = quotedSpans(enText);
    const lines = zhSpans.map(
      (span, i) => `${span} | ${enSpans[i] ?? "(none)"}`,
    );
    expect(enSpans.length, `the pairs, in order:\n${lines.join("\n")}`).toBe(
      zhSpans.length,
    );
    zhSpans.forEach((span, i) => {
      expect(
        known.get(span)?.has(enSpans[i]),
        `quote ${i + 1}: “${span}” is “${enSpans[i]}” in English, which the table does not say`,
      ).toBe(true);
    });
  });
});

docs("links of the user documents", () => {
  // The README of the mirror is written for its root; the installer does not
  // ship it, and a Go test (daemon/tools/mirror) checks its links.
  const files = userDocPairs()
    .flatMap((pair) => [pair.zh, pair.en])
    .filter(exists);

  it.each(files)("%s links to files that exist and are installed", (file) => {
    for (const target of linkTargets(read(file))) {
      if (isExternal(target) || target.startsWith("#")) continue;
      expect(target, `${file}: a link to a part of another file`).not.toContain(
        "#",
      );
      const to = resolveLink(file, target);
      expect(to, `${file}: ${target}`).not.toBeNull();
      const path = (to ?? "").replace(/\/$/, "");
      expect(
        exists(path),
        `${file} links to ${target}, which is not there`,
      ).toBe(true);
      expect(
        packaged(path),
        `${file} links to ${path}, which the installed program does not have`,
      ).toBe(true);
      if (isFolder(path))
        expect(filesUnder(path).length, `${path} is empty`).toBeGreaterThan(0);
    }
  });

  it.each(files)(
    "%s links to the repository only for its releases and issues",
    (file) => {
      // The public repository has the code only (daemon/tools/mirror), so no
      // document is there to link to; its releases and issues are.
      for (const target of linkTargets(read(file)).filter(isExternal))
        if (/github\.com\/wdcyxxycdw\/padmate\b/.test(target))
          expect(target, `${file}: a link into the repository`).toMatch(
            /^https:\/\/github\.com\/wdcyxxycdw\/padmate\/(releases|issues)(\/|$)/,
          );
    },
  );

  it.each(files)("%s does not send the reader to a design record", (file) => {
    // The ADRs are internal records in Chinese; the documents explain.
    const text = read(file);
    expect(linkTargets(text).join("\n")).not.toMatch(/adr\//i);
    expect(text).not.toMatch(/\bADR-?\d{4}\b/i);
  });
});

docs("the quick start", () => {
  it("points to a section the limitations page has", () => {
    expect(read("docs/quickstart.md")).toContain("“绘画软件”一节");
    expect(read("docs/quickstart.en.md")).toContain("“Painting apps”");
    expect(headings(read("docs/known-limitations.md"))).toContain("绘画软件");
    expect(headings(read("docs/known-limitations.en.md"))).toContain(
      "Painting apps",
    );
  });
});

describe("the reader of the documents", () => {
  const sample = [
    "# Title",
    "",
    "[中文](a.md)",
    "",
    "Press “A” and `“not a name”`.",
    "",
    "- one",
    "  - nested",
    "1. step",
    "",
    "| a | b |",
    "|---|---|",
    "| 1 | 2 |",
    "",
    "```text",
    "“code” [x](y.md)",
    "```",
    "",
    "See [guide](b.en.md) and [site](https://example.com/x).",
  ].join("\n");

  it("takes quoted names from prose only", () => {
    expect(quotedSpans(sample)).toEqual(["A"]);
  });

  it("finds an open or a straight quote mark", () => {
    expect(strayQuotes("Press “A and go")).toEqual(["“"]);
    expect(strayQuotes("Press “A” and go")).toEqual([]);
  });

  it("takes links from prose only, and sees a twin through .en", () => {
    expect(linkTargets(sample)).toEqual([
      "a.md",
      "b.en.md",
      "https://example.com/x",
    ]);
    expect(withoutEn("b.en.md")).toBe("b.md");
  });

  it("lists the blocks, so a missing item shows", () => {
    expect(blockKinds(sample)).toEqual([
      "h1",
      "p",
      "p",
      "ul0",
      "ul1",
      "ol0",
      "tr",
      "tr",
      "tr",
      "code",
      "p",
    ]);
    expect(blockKinds(sample.replace("- one\n", ""))).not.toEqual(
      blockKinds(sample),
    );
  });

  it("reads the code blocks and the table shapes", () => {
    expect(codeBlocks(sample)).toEqual(["```text\n“code” [x](y.md)"]);
    expect(tableShapes(sample)).toEqual([2, 2, 2]);
  });

  it("resolves a link from the folder of the file", () => {
    expect(resolveLink("docs/guide/01.md", "../quickstart.md")).toBe(
      "docs/quickstart.md",
    );
    expect(resolveLink("README.md", "docs/guide/00-index.md")).toBe(
      "docs/guide/00-index.md",
    );
    expect(resolveLink("README.md", "https://example.com")).toBeNull();
    expect(resolveLink("README.md", "#top")).toBeNull();
  });
});
