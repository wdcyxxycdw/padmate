import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import {
  checkMacroFile,
  isMacroCandidate,
  macroName,
  sameMacroName,
} from "./macro-names";

// The cases profile.CheckMacroFile and profile.SameMacroName are tested
// with in Go.
const cases = JSON.parse(
  readFileSync(
    new URL("../../../daemon/testdata/macro-names.json", import.meta.url),
    "utf8",
  ),
) as {
  files: { file: string; kind: string }[];
  same: { a: string; b: string; same: boolean }[];
};

describe("macro file names", () => {
  it("follows the daemon's rule and its order", () => {
    expect(cases.files.length).toBeGreaterThan(40);
    for (const test of cases.files)
      expect(checkMacroFile(test.file), JSON.stringify(test.file)).toBe(
        test.kind,
      );
  });

  it("compares names like the daemon", () => {
    for (const test of cases.same)
      expect(sameMacroName(test.a, test.b), `${test.a} ${test.b}`).toBe(
        test.same,
      );
  });

  it("refuses a lone surrogate as a character", () => {
    expect(checkMacroFile("a\ud800.ts")).toBe("char");
  });

  it("names a macro without its extension", () => {
    expect(macroName("复制全部.ts")).toBe("复制全部");
    expect(macroName("下一个笔刷.js")).toBe("下一个笔刷");
    expect(macroName("v1.2.TS")).toBe("v1.2");
  });

  it("lists top-level .ts and .js files but no declarations", () => {
    for (const name of ["a.ts", "A.TS", "v1.2.js", "x.Js"])
      expect(isMacroCandidate(name), name).toBe(true);
    for (const name of ["a.d.ts", "A.D.TS", "a.json", "a.tsx", "ts"])
      expect(isMacroCandidate(name), name).toBe(false);
  });
});
