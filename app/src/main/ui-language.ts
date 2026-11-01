import { mkdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import { dirname } from "node:path";
import type { SavedLanguage, UILanguage } from "../shared/api";
import type { EditorHandler } from "./library-handlers";

/** The window's language file, in its browser data. */
export const LANGUAGE_FILE = "language.json";

/**
 * What the file holds: one line of JSON, the language and who wrote it. The
 * installer writes the same line with setBy "installer" (ADR-0018,
 * build/padmate-options.nsh), and writes it again only over a line of its
 * own: once the settings page has written the file, an install leaves it.
 * So the key order and the spacing are part of the format.
 */
export function languageFileText(
  language: UILanguage,
  setBy: SavedLanguage["setBy"],
): string {
  return `${JSON.stringify({ language, setBy })}\n`;
}

export function validLanguage(value: unknown): value is UILanguage {
  return value === "zh-CN" || value === "en";
}

/**
 * The language a file says, or null for one that is not JSON, names no
 * language of the window, or no writer; nothing in it can make the window
 * fail.
 */
export function parseLanguageFile(text: string): SavedLanguage | null {
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch {
    return null;
  }
  if (value === null || typeof value !== "object" || Array.isArray(value))
    return null;
  const { language, setBy } = value as Record<string, unknown>;
  if (!validLanguage(language)) return null;
  if (setBy !== "settings" && setBy !== "installer") return null;
  return { language, setBy };
}

/**
 * The language file in <userData>\language.json. Writes follow one after
 * another and each replaces the file whole (a temporary file, then a rename),
 * so a crash leaves the old file or the new one.
 */
export class LanguageFile {
  private queue: Promise<void> = Promise.resolve();
  private counter = 0;

  constructor(private readonly file: string) {}

  /** What the file says now; null when there is none. Never rejects. */
  async read(): Promise<SavedLanguage | null> {
    try {
      return parseLanguageFile(await readFile(this.file, "utf8"));
    } catch {
      return null;
    }
  }

  /** Writes the person's pick on the settings page. */
  write(language: UILanguage): Promise<void> {
    if (!validLanguage(language))
      return Promise.reject(new Error("Invalid language"));
    const write = this.queue.then(() =>
      this.replace(languageFileText(language, "settings")),
    );
    // A failed write does not stop the ones after it.
    this.queue = write.catch(() => {});
    return write;
  }

  private async replace(text: string): Promise<void> {
    const temporary = `${this.file}.${process.pid}-${++this.counter}.tmp`;
    try {
      await mkdir(dirname(this.file), { recursive: true });
      await writeFile(temporary, text, "utf8");
      await rename(temporary, this.file);
    } catch (error) {
      await rm(temporary, { force: true }).catch(() => {});
      throw error;
    }
  }
}

// The window reads its language before it draws and writes the person's pick
// (ADR-0018). It sends a language and nothing else: Main owns the file.
export function languageHandlers(
  file: LanguageFile,
): Record<string, EditorHandler> {
  return {
    languageGet: { count: 0, run: () => file.read() },
    languageSet: {
      count: 1,
      run: (language) => {
        if (!validLanguage(language)) throw new Error("Invalid language");
        return file.write(language);
      },
    },
  };
}
