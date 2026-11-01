import { readFileSync } from "node:fs";
import { mkdtemp, readdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import {
  LANGUAGE_FILE,
  LanguageFile,
  languageFileText,
  languageHandlers,
  parseLanguageFile,
} from "./ui-language";

let dir: string;
beforeEach(async () => {
  dir = await mkdtemp(join(tmpdir(), "padmate-language-"));
});
afterEach(async () => {
  await rm(dir, { recursive: true, force: true });
});

const file = () => join(dir, "ui", LANGUAGE_FILE);
const options = readFileSync(
  new URL("../../build/padmate-options.nsh", import.meta.url),
  "utf8",
);

describe("the window's language file (ADR-0018)", () => {
  it("is one line of JSON with the language and who wrote it", () => {
    expect(LANGUAGE_FILE).toBe("language.json");
    expect(languageFileText("en", "settings")).toBe(
      '{"language":"en","setBy":"settings"}\n',
    );
    expect(languageFileText("zh-CN", "installer")).toBe(
      '{"language":"zh-CN","setBy":"installer"}\n',
    );
  });

  it("is the file the installer writes, in the line it writes and compares with", () => {
    expect(options).toContain(
      `!define PADMATE_LANGUAGE_FILE "${LANGUAGE_FILE}"`,
    );
    for (const language of ["zh-CN", "en"] as const)
      expect(options).toContain(
        `S== '${languageFileText(language, "installer").trimEnd()}'`,
      );
    expect(options).toContain(
      `FileWrite $0 '${languageFileText("en", "installer").trimEnd().replace("en", "$R9")}$\\n'`,
    );
    // And what the settings page writes is not one of the installer's lines,
    // which it would write over.
    for (const language of ["zh-CN", "en"] as const)
      expect(options).not.toContain(
        languageFileText(language, "settings").trimEnd(),
      );
  });

  it("reads a language and its writer, and nothing else", () => {
    expect(
      parseLanguageFile('{"language":"en","setBy":"installer"}\n'),
    ).toEqual({
      language: "en",
      setBy: "installer",
    });
    expect(
      parseLanguageFile('{ "setBy": "settings", "language": "zh-CN" }'),
    ).toEqual({ language: "zh-CN", setBy: "settings" });
    for (const text of [
      "",
      "garbage",
      "null",
      "[]",
      '"en"',
      '{"language":"fr","setBy":"settings"}',
      '{"language":"EN","setBy":"settings"}',
      '{"language":"en"}',
      '{"language":"en","setBy":"someone"}',
    ])
      expect(parseLanguageFile(text)).toBeNull();
  });

  it("is null when there is no file, and holds the person's pick once written", async () => {
    const language = new LanguageFile(file());
    await expect(language.read()).resolves.toBeNull();
    await language.write("en");
    await expect(readFile(file(), "utf8")).resolves.toBe(
      languageFileText("en", "settings"),
    );
    await expect(language.read()).resolves.toEqual({
      language: "en",
      setBy: "settings",
    });
  });

  it("replaces what the installer wrote, and the last of several writes wins", async () => {
    const language = new LanguageFile(file());
    await language.write("en");
    await writeFile(file(), languageFileText("en", "installer"));
    await Promise.all([language.write("zh-CN"), language.write("en")]);
    await expect(language.read()).resolves.toEqual({
      language: "en",
      setBy: "settings",
    });
    // No temporary file is left.
    await expect(readdir(join(dir, "ui"))).resolves.toEqual([LANGUAGE_FILE]);
  });

  it("refuses a language the window does not have", async () => {
    const language = new LanguageFile(file());
    await expect(language.write("fr" as "en")).rejects.toThrow(
      "Invalid language",
    );
    const handlers = languageHandlers(language);
    expect(handlers.languageGet.count).toBe(0);
    expect(handlers.languageSet.count).toBe(1);
    expect(() => handlers.languageSet.run("de")).toThrow("Invalid language");
    expect(() => handlers.languageSet.run({ language: "en" })).toThrow(
      "Invalid language",
    );
    await handlers.languageSet.run("zh-CN");
    await expect(handlers.languageGet.run()).resolves.toEqual({
      language: "zh-CN",
      setBy: "settings",
    });
  });
});
