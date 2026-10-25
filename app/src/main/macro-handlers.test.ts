import { mkdir, mkdtemp, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, sep } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { LibraryErrorCode } from "../shared/api";
import { type DaemonCall, uiResult } from "./library-handlers";
import {
  daemonSystemError,
  macroHandlers,
  pendingImportTTL,
} from "./macro-handlers";
import { maxScriptBytes } from "./security";

const temporary: string[] = [];
afterEach(async () => {
  for (const dir of temporary.splice(0))
    await rm(dir, { recursive: true, force: true });
});

async function tempDir(): Promise<string> {
  const dir = await mkdtemp(join(tmpdir(), "padmate-macro-"));
  temporary.push(dir);
  return dir;
}

async function scriptFile(name: string, data: string | Buffer) {
  const path = join(await tempDir(), name);
  await writeFile(path, data);
  return path;
}

const imported = { file: "画笔.ts", replaced: false };

function fixture(
  reply: (method: string, params: unknown) => unknown,
  chosen?: string,
  dataDir?: string,
) {
  let time = 1000;
  const call = vi.fn<DaemonCall>(async (method, params) =>
    reply(method, params),
  );
  const chooseScript = vi.fn(async () => chosen);
  const openEditor = vi.fn(async (_path: string) => true);
  const handlers = macroHandlers({
    call,
    chooseScript,
    openEditor,
    dataDir,
    now: () => time,
  });
  return {
    call,
    chooseScript,
    openEditor,
    handlers,
    advance: (ms: number) => {
      time += ms;
    },
  };
}

const exists = (name: string, replaceable: boolean) =>
  Object.assign(new Error(`profile library: paint/${name} already exists`), {
    code: LibraryErrorCode.scriptExists,
    data: { exists: true, name, replaceable },
  });

// What the renderer receives (uiResult copies the message) names no path,
// so neither the folder nor the Windows user name reaches it.
async function failure(run: () => unknown) {
  const result = await uiResult(run);
  if (result.ok) throw new Error("expected a failure");
  expect(result.error.message).not.toMatch(/[\\/]/);
  return result.error;
}

describe("macro handlers", () => {
  it("declares the renderer argument counts", () => {
    const { handlers } = fixture(() => ({}));
    expect(
      Object.fromEntries(
        Object.entries(handlers).map(([name, item]) => [name, item.count]),
      ),
    ).toEqual({
      macrosList: 0,
      macroCreate: 1,
      macroImport: 1,
      macroRename: 2,
      macroDelete: 1,
      scriptDetach: 1,
      macroEdit: 1,
      scriptEdit: 1,
    });
  });

  it("lists the library, keeping only known fields", async () => {
    const use = { profile: "paint", profileName: "绘画", control: "south" };
    const list = {
      macros: [
        {
          file: "复制全部.ts",
          name: "复制全部",
          created: 1790000000000,
          usedBy: [{ ...use, gesture: "tap", layer: "x" }],
          actions: 3,
          extra: true,
        },
        {
          file: "v1.2.ts",
          name: "v1.2",
          usedBy: [],
          invalidName: true,
        },
        {
          file: "坏.ts",
          name: "坏",
          usedBy: [],
          problem: "坏.ts:1:1: error",
        },
      ],
      missing: [
        {
          file: "旧.ts",
          usedBy: [{ ...use, control: "x", gesture: "hold" }],
          actions: 1,
        },
      ],
      legacy: [
        { profile: "old", profileName: "旧配置", entry: "main.ts", x: 1 },
      ],
    };
    const { call, handlers } = fixture(() => list);
    await expect(handlers.macrosList.run()).resolves.toEqual({
      macros: [
        {
          file: "复制全部.ts",
          name: "复制全部",
          created: 1790000000000,
          usedBy: [{ ...use, gesture: "tap" }],
          actions: 3,
        },
        { file: "v1.2.ts", name: "v1.2", usedBy: [], invalidName: true },
        {
          file: "坏.ts",
          name: "坏",
          usedBy: [],
          problem: "坏.ts:1:1: error",
        },
      ],
      missing: [
        {
          file: "旧.ts",
          usedBy: [{ ...use, control: "x", gesture: "hold" }],
          actions: 1,
        },
      ],
      legacy: [{ profile: "old", profileName: "旧配置", entry: "main.ts" }],
    });
    expect(call).toHaveBeenCalledExactlyOnceWith("macros.list", {});
    // A daemon without legacy scripts says nothing: an empty list.
    await expect(
      fixture(() => ({ macros: [], missing: [] })).handlers.macrosList.run(),
    ).resolves.toEqual({ macros: [], missing: [], legacy: [] });
    for (const bad of [
      null,
      { macros: [] },
      { macros: [{ file: 1, name: "a", usedBy: [] }], missing: [] },
      { macros: [{ file: "a.ts", name: "a" }], missing: [] },
      // A use names its profile.
      {
        macros: [
          {
            file: "a.ts",
            name: "a",
            usedBy: [{ control: "south", gesture: "tap" }],
          },
        ],
        missing: [],
      },
      {
        macros: [{ file: "a.ts", name: "a", usedBy: [], actions: -1 }],
        missing: [],
      },
      // The creation time is whole, non-negative Unix milliseconds.
      ...[-1, 1.5, "1", null].map((created) => ({
        macros: [{ file: "a.ts", name: "a", usedBy: [], created }],
        missing: [],
      })),
      { macros: [], missing: [{ file: "a.ts", usedBy: [], actions: "1" }] },
      { macros: [], missing: [], legacy: { entry: "main.ts" } },
      { macros: [], missing: [], legacy: [{ profile: "a", entry: "m.ts" }] },
      {
        macros: [],
        missing: [],
        legacy: [{ profile: "a", profileName: "A", entry: 1 }],
      },
    ])
      await expect(
        fixture(() => bad).handlers.macrosList.run(),
      ).rejects.toThrow("Invalid daemon response");
  });

  it("creates a macro under a checked name", async () => {
    const { call, handlers } = fixture(() => ({ file: "复制全部.ts" }));
    await expect(handlers.macroCreate.run("复制全部")).resolves.toEqual({
      file: "复制全部.ts",
    });
    expect(call).toHaveBeenCalledExactlyOnceWith("macros.create", {
      name: "复制全部",
    });
    for (const [name, kind] of [
      ["", "empty"],
      ["a.b", "char"],
      ["CON", "invalid"],
      ["conout$", "invalid"],
      [" a", "invalid"],
      ["一".repeat(41), "length"],
      [7, "empty"],
    ] as const) {
      call.mockClear();
      await expect(
        handlers.macroCreate.run(name),
        String(name),
      ).rejects.toMatchObject({ data: { kind } });
      expect(call).not.toHaveBeenCalled();
    }
    await expect(
      fixture(() => ({ file: "../x.ts" })).handlers.macroCreate.run("x"),
    ).rejects.toThrow("Invalid daemon response");
  });

  it("imports the chosen file under its sanitised name, bytes intact", async () => {
    const text = "\uFEFF// 画笔\nexport default () => {};\n";
    const path = await scriptFile("My Brush v1.2 (2).TS", text);
    const { call, chooseScript, handlers } = fixture(() => imported, path);
    await expect(handlers.macroImport.run(null)).resolves.toEqual(imported);
    expect(chooseScript).toHaveBeenCalledOnce();
    expect(call).toHaveBeenCalledExactlyOnceWith("macros.import", {
      name: "My Brush v1-2 (2).ts",
      text,
      replace: false,
    });
  });

  it("reports a canceled dialog without asking the daemon", async () => {
    const { call, handlers } = fixture(() => imported, undefined);
    await expect(handlers.macroImport.run(undefined)).resolves.toEqual({
      canceled: true,
    });
    expect(call).not.toHaveBeenCalled();
  });

  it("replaces only with the file read before, once, by token, under the existing name", async () => {
    const path = await scriptFile("画笔.ts", "// v2\n");
    let first = true;
    const { call, chooseScript, handlers, advance } = fixture(
      (_method, params) => {
        if (first) {
          first = false;
          throw exists("画笔.ts", true);
        }
        expect(params).toMatchObject({ replace: true });
        return { file: "画笔.ts", replaced: true };
      },
      path,
    );
    const result = (await handlers.macroImport.run(null)) as {
      exists: true;
      name: string;
      token: string;
    };
    expect(result).toEqual({
      exists: true,
      name: "画笔.ts",
      token: expect.stringMatching(/^[a-f0-9]{32}$/),
    });
    // The file changes after it was read: the replace still writes what
    // the user was asked about.
    await writeFile(path, "// v3\n");
    advance(pendingImportTTL - 1);
    await expect(
      handlers.macroImport.run({ replace: true, token: result.token }),
    ).resolves.toEqual({ file: "画笔.ts", replaced: true });
    expect(chooseScript).toHaveBeenCalledOnce();
    expect(call).toHaveBeenLastCalledWith("macros.import", {
      name: "画笔.ts",
      text: "// v2\n",
      replace: true,
    });
    call.mockClear();
    await expect(
      handlers.macroImport.run({ replace: true, token: result.token }),
    ).rejects.toMatchObject({ data: { reason: "expired" } });
    expect(call).not.toHaveBeenCalled();
  });

  it("offers no replace when the existing macro has another extension", async () => {
    const path = await scriptFile("画笔.js", "// js\n");
    const { handlers } = fixture(() => {
      throw exists("画笔.ts", false);
    }, path);
    await expect(handlers.macroImport.run(null)).resolves.toEqual({
      exists: true,
      name: "画笔.ts",
    });
  });

  it("refuses expired and spent tokens, and keeps at most four files", async () => {
    const path = await scriptFile("brush.ts", "// v2\n");
    const { call, handlers, advance } = fixture(() => {
      throw exists("brush.ts", true);
    }, path);
    const issue = async () =>
      ((await handlers.macroImport.run(null)) as { token: string }).token;
    const spent = await issue();
    await expect(
      handlers.macroImport.run({ replace: true, token: spent }),
    ).rejects.toThrow("already exists");
    // The token went with the first try, whatever the daemon said.
    await expect(
      handlers.macroImport.run({ replace: true, token: spent }),
    ).rejects.toMatchObject({ data: { reason: "expired" } });
    const late = await issue();
    advance(pendingImportTTL);
    call.mockClear();
    await expect(
      handlers.macroImport.run({ replace: true, token: late }),
    ).rejects.toMatchObject({ data: { reason: "expired" } });
    expect(call).not.toHaveBeenCalled();
    // At most four files wait; the oldest goes first.
    const tokens = [];
    for (let i = 0; i < 5; i++) tokens.push(await issue());
    await expect(
      handlers.macroImport.run({ replace: true, token: tokens[0] }),
    ).rejects.toMatchObject({ data: { reason: "expired" } });
    await expect(
      handlers.macroImport.run({ replace: true, token: tokens[4] }),
    ).rejects.toThrow("already exists");
  });

  it.each([
    { file: "notes.txt", data: "x", reason: "type" },
    { file: "brush.d.ts", data: "x", reason: "declaration" },
    { file: "con.ts", data: "x", reason: "name" },
    { file: "....ts", data: "x", reason: "name" },
    { file: "big.ts", data: "a".repeat(maxScriptBytes + 1), reason: "size" },
    {
      file: "binary.ts",
      data: Buffer.from([0x2f, 0x2f, 0xff, 0xfe]),
      reason: "encoding",
    },
  ])("refuses $file ($reason) before asking the daemon", async (test) => {
    const path = await scriptFile(test.file, test.data);
    const { call, handlers } = fixture(() => imported, path);
    await expect(handlers.macroImport.run(null)).rejects.toMatchObject({
      data: { reason: test.reason },
    });
    expect(call).not.toHaveBeenCalled();
  });

  it("accepts a file of exactly 1 MiB", async () => {
    const text = "a".repeat(maxScriptBytes);
    const path = await scriptFile("max.ts", text);
    const { call, handlers } = fixture(() => imported, path);
    await handlers.macroImport.run(null);
    expect(call).toHaveBeenCalledWith(
      "macros.import",
      expect.objectContaining({ name: "max.ts", text }),
    );
  });

  it.each([
    { name: "path option", args: [{ path: "C:\\x.ts" }] },
    { name: "replace without token", args: [{ replace: true }] },
    { name: "short token", args: [{ replace: true, token: "abc" }] },
    {
      name: "extra key",
      args: [{ replace: true, token: "a".repeat(32), name: "evil.ts" }],
    },
    { name: "a profile ID as before", args: ["paint", null] },
    { name: "string option", args: ["C:\\x.ts"] },
  ])("rejects $name without a dialog or daemon call", async (test) => {
    const { call, chooseScript, handlers } = fixture(() => imported, "x");
    await expect(handlers.macroImport.run(...test.args)).rejects.toThrow();
    expect(chooseScript).not.toHaveBeenCalled();
    expect(call).not.toHaveBeenCalled();
  });

  it("opens one import dialog at a time", async () => {
    let choose: (path: string | undefined) => void = () => {};
    const handlers = macroHandlers({
      call: async () => imported,
      chooseScript: () =>
        new Promise((resolve) => {
          choose = resolve;
        }),
      openEditor: async () => true,
    });
    const first = handlers.macroImport.run(null);
    await expect(handlers.macroImport.run(null)).rejects.toThrow(
      "already open",
    );
    choose(undefined);
    await expect(first).resolves.toEqual({ canceled: true });
  });

  it("renames and deletes with checked names, reporting every profile it wrote", async () => {
    const written = [
      { id: "paint", revision: "r2", bindings: 2 },
      { id: "photo", revision: "r9", bindings: 1 },
    ];
    const { call, handlers } = fixture((method) =>
      method === "macros.rename"
        ? { file: "新名.ts", bindings: 3, profiles: written }
        : method === "macros.delete"
          ? { bindings: 1, profiles: [written[1]] }
          : { revision: "r3", bindings: 1 },
    );
    // A name the daemon refuses can still be renamed or deleted.
    await expect(
      handlers.macroRename.run("v1.2.TS", "新名.ts"),
    ).resolves.toEqual({ file: "新名.ts", bindings: 3, profiles: written });
    expect(call).toHaveBeenLastCalledWith("macros.rename", {
      file: "v1.2.TS",
      to: "新名.ts",
    });
    await expect(handlers.macroDelete.run("旧.ts")).resolves.toEqual({
      bindings: 1,
      profiles: [written[1]],
    });
    expect(call).toHaveBeenLastCalledWith("macros.delete", { file: "旧.ts" });
    // Only a legacy script still belongs to one profile.
    await expect(handlers.scriptDetach.run("paint")).resolves.toEqual({
      revision: "r3",
      bindings: 1,
    });
    expect(call).toHaveBeenLastCalledWith("script.detach", { id: "paint" });
    call.mockClear();
    for (const args of [
      ["a.ts", "b.TS"],
      ["a.ts", "a.b.ts"],
      ["../a.ts", "b.ts"],
      ["a\\b.ts", "b.ts"],
      ["a.d.ts", "b.ts"],
      ["a.ts:x", "b.ts"],
      ["a\u0000.ts", "b.ts"],
    ])
      await expect(
        handlers.macroRename.run(...args),
        args.join(" "),
      ).rejects.toThrow();
    for (const file of ["", "a/b.ts", "a.json", 7])
      await expect(handlers.macroDelete.run(file)).rejects.toThrow();
    await expect(handlers.scriptDetach.run("CON")).rejects.toThrow();
    expect(call).not.toHaveBeenCalled();
    for (const bad of [
      { bindings: 0 },
      { bindings: "1", profiles: [] },
      { bindings: 0, profiles: [{ id: "paint", revision: "r" }] },
      { bindings: 0, profiles: [{ id: "paint", revision: 1, bindings: 0 }] },
      // The answer of macros.delete before the library.
      { revision: "r", bindings: 0 },
    ])
      await expect(
        fixture(() => bad).handlers.macroDelete.run("a.ts"),
        JSON.stringify(bad),
      ).rejects.toThrow("Invalid daemon response");
    // A profile ID that is no ID never reaches the renderer.
    await expect(
      fixture(() => ({
        bindings: 0,
        profiles: [{ id: "../x", revision: "r", bindings: 0 }],
      })).handlers.macroDelete.run("a.ts"),
    ).rejects.toThrow();
    for (const bad of [{ revision: "r" }, { revision: 1, bindings: 0 }])
      await expect(
        fixture(() => bad).handlers.scriptDetach.run("paint"),
      ).rejects.toThrow("Invalid daemon response");
  });

  it("passes the daemon's codes and data through", async () => {
    const missing = Object.assign(new Error("macro not found"), {
      code: LibraryErrorCode.macroNotFound,
      data: { file: "a.ts" },
    });
    const { handlers } = fixture(() => {
      throw missing;
    });
    expect(await failure(() => handlers.macroDelete.run("a.ts"))).toEqual({
      message: "macro not found",
      code: LibraryErrorCode.macroNotFound,
      data: { file: "a.ts" },
    });
  });

  it("names no path when the daemon's file system fails", async () => {
    const dir = join(tmpdir(), "padmate", "macros");
    const path = await scriptFile("brush.ts", "x");
    const { handlers } = fixture(() => {
      throw Object.assign(
        new Error(`write ${join(dir, ".padmate-x.tmp")}: disk full`),
        { code: daemonSystemError },
      );
    }, path);
    for (const run of [
      () => handlers.macrosList.run(),
      () => handlers.macroCreate.run("x"),
      () => handlers.macroImport.run(null),
      () => handlers.macroRename.run("a.ts", "b.ts"),
      () => handlers.macroDelete.run("a.ts"),
      () => handlers.scriptDetach.run("paint"),
      () => handlers.macroEdit.run("a.ts"),
      () => handlers.scriptEdit.run("paint"),
    ])
      expect(await failure(run)).toMatchObject({ code: daemonSystemError });
  });

  it("names no path when the chosen file cannot be read", async () => {
    const dir = await tempDir();
    const path = join(dir, "gone.ts");
    const { call, handlers } = fixture(() => imported, path);
    const error = await failure(() => handlers.macroImport.run(null));
    expect(error).toMatchObject({ data: { reason: "read" } });
    expect(error.message).not.toContain("gone");
    expect(call).not.toHaveBeenCalled();
  });

  it("opens only a script of the library, in the editor", async () => {
    const root = await tempDir();
    const macros = join(root, "macros");
    await mkdir(join(macros, "sub"), { recursive: true });
    await mkdir(join(macros, "dir.ts"));
    const path = join(macros, "复制全部 (1).ts");
    await writeFile(path, "export default () => {};\n");
    const { call, openEditor, handlers } = fixture(() => ({ path }));
    await expect(handlers.macroEdit.run("复制全部 (1).ts")).resolves.toEqual({
      opened: true,
    });
    expect(call).toHaveBeenCalledExactlyOnceWith("macros.path", {
      file: "复制全部 (1).ts",
    });
    expect(openEditor).toHaveBeenCalledExactlyOnceWith(path);
    openEditor.mockResolvedValueOnce(false);
    await expect(handlers.macroEdit.run("复制全部 (1).ts")).resolves.toEqual({
      opened: false,
    });
    openEditor.mockRejectedValueOnce(new Error(`spawn ${path} failed`));
    await expect(handlers.macroEdit.run("复制全部 (1).ts")).resolves.toEqual({
      opened: false,
    });

    await writeFile(join(macros, "other.ts"), "");
    await writeFile(join(macros, "sub", "复制全部 (1).ts"), "");
    const file = "复制全部 (1).ts";
    const bad = [
      `macros${sep}${file}`,
      `${path}${sep}`,
      join(macros, "other.ts"),
      join(macros, "sub", file),
      join(root, file),
      // A macros folder inside a profile's folder is the old layout.
      join(root, "profiles", "paint", "macros", file),
      [root, "macros", "..", "macros", file].join(sep),
      join(macros, "sub"),
      7,
    ];
    for (const test of bad) {
      const { openEditor, handlers } = fixture(() => ({ path: test }));
      await expect(
        handlers.macroEdit.run(file),
        String(test),
      ).rejects.toThrow();
      expect(openEditor).not.toHaveBeenCalled();
    }
    // A folder or a missing file where the daemon said: no editor, and the
    // error names no path.
    for (const name of ["dir.ts", "gone.ts"]) {
      const { openEditor, handlers } = fixture(() => ({
        path: join(macros, name),
      }));
      await failure(() => handlers.macroEdit.run(name));
      expect(openEditor).not.toHaveBeenCalled();
    }
    const { call: refused, handlers: checked } = fixture(() => ({ path }));
    for (const name of ["v1.2.ts", "a/b.ts", "../a.ts"])
      await expect(checked.macroEdit.run(name)).rejects.toThrow();
    expect(refused).not.toHaveBeenCalled();
  });

  it("opens a script of the library when the data folder sits inside a folder named profiles", async () => {
    const root = await tempDir();
    const data = join(root, "profiles", "padmate");
    await mkdir(join(data, "macros"), { recursive: true });
    await mkdir(join(data, "profiles", "paint", "macros"), { recursive: true });
    const path = join(data, "macros", "笔刷.ts");
    await writeFile(path, "export default () => {};");
    const old = join(data, "profiles", "paint", "macros", "笔刷.ts");
    await writeFile(old, "");

    // The data folder Main started the daemon with settles what the path is.
    const known = fixture(() => ({ path }), undefined, data);
    await expect(known.handlers.macroEdit.run("笔刷.ts")).resolves.toEqual({
      opened: true,
    });
    expect(known.openEditor).toHaveBeenCalledExactlyOnceWith(path);
    // Another spelling of the same folder is the same folder.
    const spelled = fixture(
      () => ({ path }),
      undefined,
      join(data, "..", "padmate"),
    );
    await expect(spelled.handlers.macroEdit.run("笔刷.ts")).resolves.toEqual({
      opened: true,
    });
    // The old layout inside that data folder is still refused.
    const legacy = fixture(() => ({ path: old }), undefined, data);
    await expect(legacy.handlers.macroEdit.run("笔刷.ts")).rejects.toThrow();
    expect(legacy.openEditor).not.toHaveBeenCalled();
    // Without it the path cannot be told from the old layout.
    const unknown = fixture(() => ({ path }));
    await expect(unknown.handlers.macroEdit.run("笔刷.ts")).rejects.toThrow();
    expect(unknown.openEditor).not.toHaveBeenCalled();
  });

  it("opens the legacy entry inside the profile folder only", async () => {
    const root = await tempDir();
    const folder = join(root, "profiles", "paint");
    await mkdir(join(folder, "src"), { recursive: true });
    await mkdir(join(folder, ".history"), { recursive: true });
    for (const name of ["main.ts", join("src", "brush.js"), "notes.txt"])
      await writeFile(join(folder, name), "");
    await writeFile(join(folder, ".history", "main.ts"), "");
    for (const path of [
      join(folder, "main.ts"),
      join(folder, "src", "brush.js"),
    ]) {
      const { call, openEditor, handlers } = fixture(() => ({ path }));
      await expect(handlers.scriptEdit.run("paint")).resolves.toEqual({
        opened: true,
      });
      expect(call).toHaveBeenCalledExactlyOnceWith("script.path", {
        id: "paint",
      });
      expect(openEditor).toHaveBeenCalledExactlyOnceWith(path);
    }
    for (const path of [
      join(folder, ".history", "main.ts"),
      join(folder, "notes.txt"),
      join(root, "profiles", "photo", "main.ts"),
      join(root, "main.ts"),
      "main.ts",
    ]) {
      const { openEditor, handlers } = fixture(() => ({ path }));
      await expect(handlers.scriptEdit.run("paint"), path).rejects.toThrow();
      expect(openEditor).not.toHaveBeenCalled();
    }
  });

  it("does not open a macro reached through a link", async () => {
    const root = await tempDir();
    const target = await scriptFile("target.ts", "");
    const macros = join(root, "macros");
    await mkdir(macros, { recursive: true });
    const link = join(macros, "link.ts");
    try {
      await symlink(target, link, "file");
    } catch {
      // Creating a file link needs developer mode on Windows.
      return;
    }
    const { openEditor, handlers } = fixture(() => ({ path: link }));
    await expect(handlers.macroEdit.run("link.ts")).rejects.toThrow();
    expect(openEditor).not.toHaveBeenCalled();
  });
});
