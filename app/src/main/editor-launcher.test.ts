import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { editorLauncher, editorRecorder } from "./editor-launcher";

const code =
  "C:\\Users\\me\\AppData\\Local\\Programs\\Microsoft VS Code\\Code.exe";
const notepad = "C:\\Windows\\System32\\notepad.exe";
const file = "D:\\data\\profiles\\paint\\macros\\复制全部.ts";

function setup({
  vscode = async () => code,
  files = [code, notepad],
  fails = [] as string[],
  systemRoot = "C:\\Windows" as string | undefined,
} = {}) {
  const start = vi.fn(async (program: string, _args: string[]) => {
    if (fails.includes(program)) throw new Error(`spawn ${program} ENOENT`);
  });
  const open = editorLauncher({
    vscode,
    systemRoot,
    start,
    isFile: async (path) => files.includes(path),
  });
  return { open, start };
}

describe("editor launcher", () => {
  it("opens the file in VS Code's own executable, without a shell", async () => {
    const { open, start } = setup();
    await expect(open(file)).resolves.toBe(true);
    expect(start).toHaveBeenCalledExactlyOnceWith(code, [file]);
  });

  it("falls back to Notepad when VS Code is missing or fails", async () => {
    for (const options of [
      { vscode: async () => "" },
      {
        vscode: async () => {
          throw new Error("no handler");
        },
      },
      { vscode: async () => "Code.exe" },
      { vscode: async () => "C:\\Tools\\code.cmd", files: [notepad] },
      { vscode: async () => "C:\\Tools\\..\\Tools\\Code.exe" },
      { files: [notepad] },
      { fails: [code] },
    ]) {
      const { open, start } = setup(options);
      await expect(open(file)).resolves.toBe(true);
      expect(start).toHaveBeenLastCalledWith(notepad, [file]);
    }
  });

  it("reports when no editor starts", async () => {
    for (const options of [
      { fails: [code, notepad] },
      { files: [] },
      { vscode: async () => "", systemRoot: "" },
      { vscode: async () => "", systemRoot: "Windows" },
    ]) {
      const { open } = setup(options);
      await expect(open(file)).resolves.toBe(false);
    }
  });
});

describe("editor recorder", () => {
  let dir = "";
  afterEach(async () => {
    if (dir) await rm(dir, { recursive: true, force: true });
  });
  it("records each path instead of starting an editor", async () => {
    dir = await mkdtemp(join(tmpdir(), "padmate-editor-"));
    const log = join(dir, "editor.log");
    const open = editorRecorder(log);
    await expect(open(file)).resolves.toBe(true);
    await expect(open("D:\\b.ts")).resolves.toBe(true);
    expect(await readFile(log, "utf8")).toBe(`${file}\nD:\\b.ts\n`);
  });
});
