import { describe, expect, it, vi } from "vitest";
import { LibraryErrorCode } from "../shared/api";
import { type DaemonCall, libraryHandlers, uiResult } from "./library-handlers";

const revision = "a".repeat(64);

function fixture(reply: (method: string, params: unknown) => unknown) {
  const call = vi.fn<DaemonCall>(async (method, params) =>
    reply(method, params),
  );
  return { call, handlers: libraryHandlers(call) };
}

describe("profile library and window capture handlers", () => {
  it("declares the renderer argument counts", () => {
    const { handlers } = fixture(() => ({}));
    expect(
      Object.fromEntries(
        Object.entries(handlers).map(([name, item]) => [name, item.count]),
      ),
    ).toEqual({
      profileActivate: 1,
      profileDelete: 2,
      profileCopy: 2,
      appsRecent: 0,
    });
  });

  it.each([
    {
      name: "profileActivate",
      args: ["paint"],
      method: "profiles.activate",
      params: { id: "paint" },
      reply: { profileId: "paint", generation: 3 },
      want: { profileId: "paint", generation: 3 },
    },
    {
      name: "profileActivate",
      args: [null],
      method: "profiles.activate",
      params: { id: null },
      reply: { profileId: "", generation: 4 },
      want: { profileId: "", generation: 4 },
    },
    {
      name: "profileDelete",
      args: ["paint", revision],
      method: "profiles.delete",
      params: { id: "paint", revision },
      reply: { deleted: true },
      want: undefined,
    },
    {
      name: "profileCopy",
      args: ["paint", "paint-2"],
      method: "profiles.copy",
      params: { from: "paint", to: "paint-2" },
      reply: { revision, extra: "dropped" },
      want: { revision },
    },
    {
      name: "appsRecent",
      args: [],
      method: "apps.recent",
      params: {},
      reply: {
        apps: [
          {
            exe: "CLIPStudioPaint.exe",
            path: "C:\\Apps\\CLIPStudioPaint.exe",
            elevated: false,
            pid: 7,
          },
        ],
      },
      want: [
        {
          exe: "CLIPStudioPaint.exe",
          path: "C:\\Apps\\CLIPStudioPaint.exe",
          elevated: false,
        },
      ],
    },
  ])("$name sends $method with validated params", async (test) => {
    const { call, handlers } = fixture(() => test.reply);
    await expect(handlers[test.name].run(...test.args)).resolves.toEqual(
      test.want,
    );
    expect(call).toHaveBeenCalledExactlyOnceWith(test.method, test.params);
  });

  it.each([
    { name: "profileActivate", args: [undefined] },
    { name: "profileActivate", args: ["../secret"] },
    { name: "profileActivate", args: ["CON"] },
    { name: "profileActivate", args: [""] },
    { name: "profileDelete", args: [null, revision] },
    { name: "profileDelete", args: ["paint", null] },
    { name: "profileDelete", args: ["paint", "abc"] },
    { name: "profileDelete", args: ["paint", revision.toUpperCase()] },
    { name: "profileDelete", args: ["a/b", revision] },
    { name: "profileCopy", args: ["paint", null] },
    { name: "profileCopy", args: [null, "paint"] },
    { name: "profileCopy", args: ["paint", "paint"] },
    { name: "profileCopy", args: ["paint", "nul"] },
  ])("$name rejects $args before calling the daemon", async (test) => {
    const { call, handlers } = fixture(() => ({}));
    await expect(handlers[test.name].run(...test.args)).rejects.toThrow();
    expect(call).not.toHaveBeenCalled();
  });

  it.each([
    { name: "profileActivate", args: ["paint"], reply: { profileId: 1 } },
    { name: "profileCopy", args: ["a", "b"], reply: null },
    { name: "appsRecent", args: [], reply: { apps: {} } },
    { name: "appsRecent", args: [], reply: { apps: [{ exe: "x.exe" }] } },
  ])("$name rejects a malformed daemon reply", async (test) => {
    const { handlers } = fixture(() => test.reply);
    await expect(handlers[test.name].run(...test.args)).rejects.toThrow(
      "Invalid daemon response",
    );
  });

  it("does not report success without a daemon manager", async () => {
    const handlers = libraryHandlers(() => undefined);
    await expect(handlers.profileDelete.run("paint", revision)).rejects.toThrow(
      "not connected",
    );
  });

  it("keeps daemon error code and data for the renderer", async () => {
    const references = ["rules/3", "startup"];
    const { handlers } = fixture(() => {
      throw Object.assign(new Error("profile is referenced"), {
        code: LibraryErrorCode.profileReferenced,
        data: { references },
      });
    });
    await expect(
      uiResult(() => handlers.profileDelete.run("paint", revision)),
    ).resolves.toEqual({
      ok: false,
      error: {
        message: "profile is referenced",
        code: LibraryErrorCode.profileReferenced,
        data: { references },
      },
    });
    await expect(uiResult(() => "value")).resolves.toEqual({
      ok: true,
      value: "value",
    });
    await expect(
      uiResult(() => {
        throw "plain";
      }),
    ).resolves.toEqual({ ok: false, error: { message: "Request failed" } });
  });
});
