import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { LibraryErrorCode } from "../shared/api";
import { archiveHandlers, maxAmgpBytes } from "./archive-handlers";
import { type DaemonCall, uiResult } from "./library-handlers";

// An AntiMicroX profile (PRO-21, ADR-0072) is chosen with the same 导入…
// dialog as a .padmate file; Main routes it by the extension, reads it (at
// most 1 MiB) and hands the bytes to profiles.importAmgp as data. The daemon
// never gets a path.

const temporary: string[] = [];
afterEach(async () => {
  for (const dir of temporary.splice(0))
    await rm(dir, { recursive: true, force: true });
});

async function fileOf(name: string, data: Buffer | string) {
  const dir = await mkdtemp(join(tmpdir(), "padmate-amgp-"));
  temporary.push(dir);
  const path = join(dir, name);
  await writeFile(path, data);
  return path;
}

const xml =
  '<?xml version="1.0" encoding="UTF-8"?>\n<gamecontroller configversion="19"/>';
const report = [
  {
    severity: "dropped",
    code: "execute",
    set: 1,
    control: "south",
    slot: 1,
    message: "运行程序“x.exe”：padmate 不运行程序",
  },
  { severity: "approximate", code: "turbo", message: "连点" },
  { severity: "info", code: "layers", set: 2, message: "导入为层" },
];
const answer = {
  id: "my-profile",
  name: "My profile",
  report,
  revision: "r1",
  applied: true,
};

function fixture(
  reply: (method: string, params: unknown) => unknown,
  importPath?: string,
) {
  const call = vi.fn<DaemonCall>(async (method, params) =>
    reply(method, params),
  );
  const chooseImport = vi.fn(async () => importPath);
  const handlers = archiveHandlers({
    call,
    chooseExport: async () => undefined,
    chooseImport,
    writeFile: async () => {},
  });
  const run = (...args: unknown[]) =>
    uiResult(() => handlers.profileImport.run(...args));
  return { call, chooseImport, run };
}

describe("importing an AntiMicroX profile (PRO-21)", () => {
  it.each(["Paint.amgp", "Paint.AMGP", "old.xml", "OLD.XML"])(
    "sends %s to profiles.importAmgp as data with the file's name",
    async (name) => {
      const path = await fileOf(name, xml);
      const { call, run } = fixture(() => answer, path);
      expect(await run()).toEqual({
        ok: true,
        value: { amgp: true, ...answer },
      });
      expect(call).toHaveBeenCalledExactlyOnceWith("profiles.importAmgp", {
        data: Buffer.from(xml).toString("base64"),
        fileName: name,
      });
    },
  );

  it("keeps a problem of the commit, and drops what it does not know", async () => {
    const path = await fileOf("a.amgp", xml);
    const { run } = fixture(
      () => ({
        ...answer,
        applied: false,
        problem: "restart needed",
        extra: "dropped",
        report: [{ ...report[0], extra: 1 }],
      }),
      path,
    );
    const result = await run();
    expect(result).toEqual({
      ok: true,
      value: {
        amgp: true,
        ...answer,
        applied: false,
        problem: "restart needed",
        report: [report[0]],
      },
    });
  });

  it("still takes a .padmate file the old way", async () => {
    const path = await fileOf(
      "Paint.padmate",
      Buffer.from([0x50, 0x4b, 0x03, 0x04, 1]),
    );
    const { call, run } = fixture(
      () => ({ id: "paint", name: "Paint", revision: "r1", applied: true }),
      path,
    );
    expect(await run()).toEqual({
      ok: true,
      value: { id: "paint", name: "Paint", revision: "r1", applied: true },
    });
    expect(call.mock.calls[0][0]).toBe("profiles.import");
  });

  it("reads nothing and asks nothing when the user cancels", async () => {
    const { call, run } = fixture(() => answer, undefined);
    expect(await run()).toEqual({ ok: true, value: { canceled: true } });
    expect(call).not.toHaveBeenCalled();
  });

  it("refuses a file over 1 MiB the way the daemon does, before reading it", async () => {
    const path = await fileOf("big.amgp", Buffer.alloc(maxAmgpBytes + 1, 32));
    const { call, run } = fixture(() => answer, path);
    const result = await run();
    expect(result).toMatchObject({
      ok: false,
      error: {
        code: LibraryErrorCode.amgpInvalid,
        data: { reason: "size" },
      },
    });
    expect(call).not.toHaveBeenCalled();
  });

  it("takes a file of exactly 1 MiB", async () => {
    const path = await fileOf("edge.amgp", Buffer.alloc(maxAmgpBytes, 32));
    const { call, run } = fixture(() => answer, path);
    expect(await run()).toMatchObject({ ok: true });
    expect(call).toHaveBeenCalledOnce();
  });

  it("says the file could not be read without naming it", async () => {
    const { run } = fixture(
      () => answer,
      join(tmpdir(), "no-such-folder", "x.amgp"),
    );
    const result = await run();
    expect(result).toMatchObject({
      ok: false,
      error: { data: { reason: "read" } },
    });
    expect(JSON.stringify(result)).not.toContain("no-such-folder");
  });

  it("passes the daemon's refusal on with its code, reason and words", async () => {
    const path = await fileOf("joy.amgp", xml);
    const refusal = Object.assign(new Error("这是原始摇杆的配置"), {
      code: LibraryErrorCode.amgpInvalid,
      data: { reason: "joystick" },
    });
    const { run } = fixture(() => {
      throw refusal;
    }, path);
    expect(await run()).toEqual({
      ok: false,
      error: {
        message: "这是原始摇杆的配置",
        code: LibraryErrorCode.amgpInvalid,
        data: { reason: "joystick" },
      },
    });
  });

  it("refuses an answer that is not an import result", async () => {
    const path = await fileOf("a.amgp", xml);
    for (const reply of [
      null,
      [],
      {},
      { ...answer, report: "none" },
      { ...answer, report: [null] },
      { ...answer, report: [{ ...report[0], severity: "fatal" }] },
      { ...answer, report: [{ ...report[0], set: "1" }] },
      { ...answer, report: [{ ...report[0], message: 7 }] },
      { ...answer, id: "../x" },
      { ...answer, applied: "yes" },
    ]) {
      const { run } = fixture(() => reply, path);
      expect(await run(), JSON.stringify(reply)).toMatchObject({ ok: false });
    }
  });

  it("answers an empty report as an empty list", async () => {
    const path = await fileOf("a.amgp", xml);
    const { run } = fixture(() => ({ ...answer, report: [] }), path);
    expect(await run()).toMatchObject({ ok: true, value: { report: [] } });
  });
});
