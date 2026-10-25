import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { LibraryErrorCode } from "../shared/api";
import {
  archiveHandlers,
  pendingArchiveTTL,
  readArchiveFile,
} from "./archive-handlers";
import { type DaemonCall, uiResult } from "./library-handlers";
import { daemonSystemError } from "./macro-handlers";
import { maxArchiveBytes } from "./security";

const temporary: string[] = [];
afterEach(async () => {
  for (const dir of temporary.splice(0))
    await rm(dir, { recursive: true, force: true });
});

async function tempDir(): Promise<string> {
  const dir = await mkdtemp(join(tmpdir(), "padmate-archive-"));
  temporary.push(dir);
  return dir;
}

async function archiveFile(data: Buffer, name = "Paint.padmate") {
  const path = join(await tempDir(), name);
  await writeFile(path, data);
  return path;
}

// Enough of a zip file for Main, which only moves the bytes.
const zip = Buffer.from([0x50, 0x4b, 0x03, 0x04, 9, 8, 7, 6]);
const saved = { id: "paint-2", name: "Paint", revision: "r1", applied: false };

const nameTaken = (id = "paint", name = "Paint") =>
  Object.assign(new Error(`profile ${id} is already named "${name}"`), {
    code: LibraryErrorCode.profileNameTaken,
    data: { id, name },
  });

function fixture(
  reply: (method: string, params: unknown) => unknown,
  { exportPath, importPath }: { exportPath?: string; importPath?: string } = {},
) {
  let time = 1000;
  const call = vi.fn<DaemonCall>(async (method, params) =>
    reply(method, params),
  );
  const chooseExport = vi.fn(async (_name: string) => exportPath);
  const chooseImport = vi.fn(async () => importPath);
  const written = new Map<string, Buffer>();
  const writeFile = vi.fn(async (path: string, data: Buffer) => {
    written.set(path, data);
  });
  const handlers = archiveHandlers({
    call,
    chooseExport,
    chooseImport,
    writeFile,
    now: () => time,
  });
  const run = (name: string, ...args: unknown[]) =>
    uiResult(() => handlers[name]?.run(...args));
  return {
    call,
    chooseExport,
    chooseImport,
    writeFile,
    written,
    handlers,
    run,
    advance: (ms: number) => {
      time += ms;
    },
  };
}

describe("profile archive handlers (PRO-20)", () => {
  it("declares the renderer argument counts", () => {
    const { handlers } = fixture(() => ({}));
    expect(
      Object.fromEntries(
        Object.entries(handlers).map(([name, item]) => [name, item.count]),
      ),
    ).toEqual({ profileExport: 2, profileImport: 1 });
  });

  it("exports the daemon's archive to the file the user chose", async () => {
    const { call, chooseExport, written, run } = fixture(
      () => ({ name: "Stored", data: zip.toString("base64") }),
      { exportPath: "D:\\Backup\\绘画.padmate" },
    );
    expect(await run("profileExport", "paint", "绘画")).toEqual({
      ok: true,
      value: { saved: true },
    });
    expect(call).toHaveBeenCalledExactlyOnceWith("profiles.export", {
      id: "paint",
    });
    // The UI's name, not the stored one: the default is 默认 or Default.
    expect(chooseExport).toHaveBeenCalledExactlyOnceWith("绘画.padmate");
    expect(written.get("D:\\Backup\\绘画.padmate")).toEqual(zip);
  });

  it("writes nothing when the user cancels", async () => {
    const { writeFile, run } = fixture(() => ({
      name: "Paint",
      data: zip.toString("base64"),
    }));
    expect(await run("profileExport", "paint", "Paint")).toEqual({
      ok: true,
      value: { saved: false },
    });
    expect(writeFile).not.toHaveBeenCalled();
  });

  it("asks the daemon first, so a refusal shows before any dialog", async () => {
    const refusal = Object.assign(new Error("profile library: paint: link"), {
      code: LibraryErrorCode.profileUnsupported,
      data: { path: "paint/scripts" },
    });
    const { chooseExport, run } = fixture(
      () => {
        throw refusal;
      },
      { exportPath: "D:\\x.padmate" },
    );
    expect(await run("profileExport", "paint", "Paint")).toEqual({
      ok: false,
      error: {
        message: refusal.message,
        code: LibraryErrorCode.profileUnsupported,
        data: { path: "paint/scripts" },
      },
    });
    expect(chooseExport).not.toHaveBeenCalled();
  });

  it("refuses what the daemon should never send", async () => {
    for (const reply of [
      null,
      { name: "Paint" },
      { name: "Paint", data: "@@@@" },
      { name: "Paint", data: Buffer.from("text").toString("base64") },
    ]) {
      const { writeFile, run } = fixture(() => reply, {
        exportPath: "D:\\x.padmate",
      });
      expect(await run("profileExport", "paint", "Paint")).toMatchObject({
        ok: false,
        error: { message: "Invalid daemon response" },
      });
      expect(writeFile).not.toHaveBeenCalled();
    }
  });

  it("reports a failed write without its path", async () => {
    const { handlers, writeFile } = fixture(
      () => ({ name: "Paint", data: zip.toString("base64") }),
      { exportPath: "C:\\Users\\someone\\x.padmate" },
    );
    writeFile.mockRejectedValue(
      new Error("EPERM: C:\\Users\\someone\\x.padmate"),
    );
    const result = await uiResult(() =>
      handlers.profileExport?.run("paint", "Paint"),
    );
    expect(result).toEqual({
      ok: false,
      error: {
        message: "The file could not be saved; choose another folder",
        data: { reason: "write" },
      },
    });
  });

  it("validates the renderer's arguments before asking the daemon", async () => {
    const { call, run } = fixture(() => ({}));
    for (const args of [
      ["../paint", "Paint"],
      ["paint", null],
      ["paint", { name: "x" }],
    ])
      expect((await run("profileExport", ...args)).ok).toBe(false);
    for (const value of [
      { token: "x", replace: "paint" },
      { path: "C:\\x.padmate" },
      "C:\\x.padmate",
    ])
      expect((await run("profileImport", value)).ok).toBe(false);
    expect(call).not.toHaveBeenCalled();
  });

  it("imports the chosen file as a new profile", async () => {
    const path = await archiveFile(zip);
    const { call, run } = fixture(() => saved, { importPath: path });
    expect(await run("profileImport", null)).toEqual({
      ok: true,
      value: saved,
    });
    expect(call).toHaveBeenCalledExactlyOnceWith("profiles.import", {
      data: zip.toString("base64"),
    });
  });

  it("does nothing when the user cancels the open dialog", async () => {
    const { call, run } = fixture(() => saved);
    expect(await run("profileImport", null)).toEqual({
      ok: true,
      value: { canceled: true },
    });
    expect(call).not.toHaveBeenCalled();
  });

  it("asks about a name clash, then replaces or renames with the same file", async () => {
    const path = await archiveFile(zip);
    const replies: unknown[] = [];
    const { call, chooseImport, run } = fixture(
      (_method, params) => {
        replies.push(params);
        const { replace, name } = params as { replace?: string; name?: string };
        if (!replace && !name) throw nameTaken();
        return { ...saved, id: replace ?? "paint-2" };
      },
      { importPath: path },
    );
    const first = await run("profileImport", null);
    expect(first).toMatchObject({
      ok: true,
      value: { exists: true, id: "paint", name: "Paint" },
    });
    const token = (first as { value: { token: string } }).value.token;
    expect(token).toMatch(/^[0-9a-f]{32}$/);
    expect(await run("profileImport", { token, replace: "paint" })).toEqual({
      ok: true,
      value: { ...saved, id: "paint" },
    });
    // The file was read once; the answer reuses it.
    expect(chooseImport).toHaveBeenCalledOnce();
    expect(call.mock.calls.map((entry) => entry[1])).toEqual([
      { data: zip.toString("base64") },
      { data: zip.toString("base64"), replace: "paint" },
    ]);
    // A token works once.
    expect(await run("profileImport", { token, replace: "paint" })).toEqual({
      ok: false,
      error: { message: "Choose the file again", data: { reason: "expired" } },
    });
    const again = await run("profileImport", null);
    const second = (again as { value: { token: string } }).value.token;
    expect(
      await run("profileImport", { token: second, name: "Paint 2" }),
    ).toEqual({ ok: true, value: saved });
    expect(call.mock.calls.at(-1)?.[1]).toEqual({
      data: zip.toString("base64"),
      name: "Paint 2",
    });
  });

  it("asks again when the new name is taken too", async () => {
    const path = await archiveFile(zip);
    const { run } = fixture(
      () => {
        throw nameTaken("other", "Other");
      },
      { importPath: path },
    );
    const first = await run("profileImport", null);
    const token = (first as { value: { token: string } }).value.token;
    const second = await run("profileImport", { token, name: "Other" });
    expect(second).toMatchObject({
      ok: true,
      value: { exists: true, id: "other", name: "Other" },
    });
    expect((second as { value: { token: string } }).value.token).not.toBe(
      token,
    );
  });

  const scriptConflict = (data: unknown = [clash]) =>
    Object.assign(
      new Error("a script of the file differs from the library's"),
      {
        code: LibraryErrorCode.macroConflict,
        data: { conflicts: data },
      },
    );
  const clash = { file: "画笔.ts", existing: "画笔.ts", replaceable: true };

  it("asks about scripts that clash with the library, keeping the earlier answer", async () => {
    const path = await archiveFile(zip);
    const { call, chooseImport, run } = fixture(
      (_method, params) => {
        const { replace, name, macros } = params as {
          replace?: string;
          name?: string;
          macros?: string;
        };
        if (!replace && !name && !macros) throw nameTaken();
        if (!macros) throw scriptConflict();
        return {
          ...saved,
          id: replace ?? "paint-2",
          macros: [
            {
              file: "画笔.ts",
              name: "画笔.ts",
              replaced: macros === "replace",
            },
          ],
        };
      },
      { importPath: path },
    );
    const first = (await run("profileImport", null)) as {
      value: { token: string };
    };
    // The profile's name comes first; nothing is written until both are answered.
    const second = await run("profileImport", {
      token: first.value.token,
      replace: "paint",
    });
    expect(second).toMatchObject({
      ok: true,
      value: {
        conflict: true,
        scripts: [clash],
        token: expect.stringMatching(/^[0-9a-f]{32}$/),
      },
    });
    const token = (second as { value: { token: string } }).value.token;
    expect(token).not.toBe(first.value.token);
    // The answer about the scripts needs no profile answer: Main kept it.
    expect(await run("profileImport", { token, macros: "replace" })).toEqual({
      ok: true,
      value: {
        ...saved,
        id: "paint",
        macros: [{ file: "画笔.ts", name: "画笔.ts", replaced: true }],
      },
    });
    expect(chooseImport).toHaveBeenCalledOnce();
    expect(call.mock.calls.map((entry) => entry[1])).toEqual([
      { data: zip.toString("base64") },
      { data: zip.toString("base64"), replace: "paint" },
      { data: zip.toString("base64"), replace: "paint", macros: "replace" },
    ]);
    // A token works once.
    expect(await run("profileImport", { token, macros: "keep" })).toEqual({
      ok: false,
      error: { message: "Choose the file again", data: { reason: "expired" } },
    });
  });

  it("asks about scripts of a first call when the name is free, and keeps both on request", async () => {
    const path = await archiveFile(zip);
    const { call, run } = fixture(
      (_method, params) => {
        if (!(params as { macros?: string }).macros) throw scriptConflict();
        return saved;
      },
      { importPath: path },
    );
    const first = (await run("profileImport", null)) as {
      value: { conflict: true; token: string };
    };
    expect(first.value.conflict).toBe(true);
    expect(
      await run("profileImport", { token: first.value.token, macros: "keep" }),
    ).toEqual({ ok: true, value: saved });
    expect(call.mock.calls.at(-1)?.[1]).toEqual({
      data: zip.toString("base64"),
      macros: "keep",
    });
  });

  it("refuses script conflicts and results the daemon should never send", async () => {
    const path = await archiveFile(zip);
    for (const data of [
      [],
      "x",
      [{ file: "a.ts", existing: "a.ts" }],
      [{ file: 1, existing: "a.ts", replaceable: true }],
    ])
      await expect(
        uiResult(() =>
          fixture(
            () => {
              throw scriptConflict(data);
            },
            { importPath: path },
          ).handlers.profileImport.run(null),
        ),
      ).resolves.toMatchObject({
        ok: false,
        error: { message: "Invalid daemon response" },
      });
    for (const macros of ["x", [{ file: "a.ts" }], [{ file: "a.ts", name: 1 }]])
      await expect(
        uiResult(() =>
          fixture(() => ({ ...saved, macros }), {
            importPath: path,
          }).handlers.profileImport.run(null),
        ),
      ).resolves.toMatchObject({
        ok: false,
        error: { message: "Invalid daemon response" },
      });
    const { run } = fixture(() => saved, { importPath: path });
    for (const options of [
      { token: "a".repeat(32), macros: "both" },
      { token: "a".repeat(32), macros: "keep", replace: "paint" },
      { token: "a".repeat(32), macros: true },
    ])
      expect((await run("profileImport", options)).ok).toBe(false);
  });

  it("forgets a file after two minutes", async () => {
    const path = await archiveFile(zip);
    const { run, advance } = fixture(
      () => {
        throw nameTaken();
      },
      { importPath: path },
    );
    const first = await run("profileImport", null);
    const token = (first as { value: { token: string } }).value.token;
    advance(pendingArchiveTTL);
    expect(await run("profileImport", { token, replace: "paint" })).toEqual({
      ok: false,
      error: { message: "Choose the file again", data: { reason: "expired" } },
    });
  });

  it("passes the daemon's refusal of the file on", async () => {
    const path = await archiveFile(zip);
    const refusal = Object.assign(new Error("profile archive: not a zip"), {
      code: LibraryErrorCode.archiveInvalid,
      data: { reason: "format" },
    });
    const { run } = fixture(
      () => {
        throw refusal;
      },
      { importPath: path },
    );
    expect(await run("profileImport", null)).toEqual({
      ok: false,
      error: {
        message: refusal.message,
        code: LibraryErrorCode.archiveInvalid,
        data: { reason: "format" },
      },
    });
  });

  it("hides a system error's message, which can name a path", async () => {
    const path = await archiveFile(zip);
    const { run } = fixture(
      () => {
        throw Object.assign(
          new Error("open C:\\Users\\someone\\AppData\\Roaming\\padmate"),
          { code: daemonSystemError },
        );
      },
      { importPath: path },
    );
    const result = await run("profileImport", null);
    expect(result).toEqual({
      ok: false,
      error: {
        message:
          "padmate could not use the profile folder; the daemon log has the details",
        code: daemonSystemError,
      },
    });
  });

  it("refuses a file over 4 MiB before reading it", async () => {
    const path = await archiveFile(Buffer.alloc(maxArchiveBytes + 1));
    const { call, run } = fixture(() => saved, { importPath: path });
    expect(await run("profileImport", null)).toEqual({
      ok: false,
      error: {
        message: "Profile files are limited to 4 MiB",
        data: { reason: "size" },
      },
    });
    expect(call).not.toHaveBeenCalled();
  });

  it("reports a file it cannot read without its path", async () => {
    const dir = await tempDir();
    for (const path of [join(dir, "missing.padmate"), dir])
      await expect(readArchiveFile(path)).rejects.toMatchObject({
        message:
          "The chosen file could not be read; it may have been moved, locked or taken offline",
        data: { reason: "read" },
      });
  });

  it("opens one file dialog at a time", async () => {
    let release = (_path: string | undefined) => {};
    const { handlers, chooseImport } = fixture(() => saved);
    chooseImport.mockImplementation(
      () =>
        new Promise((resolve) => {
          release = resolve;
        }),
    );
    const first = handlers.profileImport?.run(null) as Promise<unknown>;
    await expect(
      Promise.resolve(handlers.profileImport?.run(null)),
    ).rejects.toThrow("A file dialog is already open");
    await expect(
      Promise.resolve(handlers.profileExport?.run("paint", "Paint")),
    ).rejects.toThrow("A file dialog is already open");
    release(undefined);
    await expect(first).resolves.toEqual({ canceled: true });
  });
});
