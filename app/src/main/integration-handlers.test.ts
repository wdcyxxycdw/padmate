import { describe, expect, it, vi } from "vitest";
import { integrationHandlers } from "./integration-handlers";
import type { DaemonCall } from "./library-handlers";

const info = {
  id: "demoapp",
  name: "Demo Paint",
  exe: ["DemoPaint.exe"],
  found: true,
  running: false,
  profiles: [],
};
const backup = {
  id: "b1",
  created: "2026-11-04T10:00:00Z",
  reason: "write",
  changes: 2,
};
const item = { keys: "P", function: "tool:a", name: "G", path: ["x"] };

function fixture(
  reply: (method: string) => unknown = (method) =>
    ({
      "integrations.list": [info],
      "integrations.functions": {
        revision: "r",
        tree: [],
        functions: [],
      },
      "integrations.check": { status: "free", owners: [], notes: [] },
      "integrations.items": {
        revision: "r",
        items: [],
        statuses: [],
        consistent: true,
        dismissed: false,
      },
      "integrations.plan": { entries: [] },
      "integrations.write": { backup, revision: "r2" },
      "integrations.backups": [backup],
      "integrations.restore": backup,
    })[method] ?? {},
) {
  const call = vi.fn<DaemonCall>(async (method) => reply(method));
  return { call, handlers: integrationHandlers(call) };
}

describe("integration handlers", () => {
  it("declares the renderer argument counts", () => {
    const { handlers } = fixture();
    expect(
      Object.fromEntries(
        Object.entries(handlers).map(([name, handler]) => [
          name,
          handler.count,
        ]),
      ),
    ).toEqual({
      integrationsList: 0,
      integrationsFunctions: 2,
      integrationsCheck: 1,
      integrationsItems: 2,
      integrationsPutItems: 1,
      integrationsPlan: 2,
      integrationsWrite: 2,
      integrationsBackups: 1,
      integrationsRestore: 2,
      integrationsChooseDataDir: 1,
      integrationsResetDataDir: 1,
      integrationsDismiss: 1,
    });
  });

  it("asks the daemon for its fixed methods with the parameters cut down", async () => {
    const { call, handlers } = fixture();
    await handlers.integrationsList.run();
    await handlers.integrationsFunctions.run("demoapp", "en");
    await handlers.integrationsCheck.run({
      integration: "demoapp",
      function: "tool:a",
      keys: "P",
      extra: "dropped",
    });
    await handlers.integrationsItems.run("paint", "demoapp");
    await handlers.integrationsPutItems.run({
      profile: "paint",
      integration: "demoapp",
      revision: "r",
      items: [{ ...item, extra: 1 }],
    });
    await handlers.integrationsPlan.run("paint", "demoapp");
    await handlers.integrationsWrite.run("demoapp", [
      { function: "tool:a", add: "P", extra: 1 },
      { function: "cmd:x", remove: "P" },
    ]);
    await handlers.integrationsBackups.run("demoapp");
    await handlers.integrationsRestore.run("demoapp", "b1");
    await handlers.integrationsDismiss.run({
      profile: "paint",
      integration: "demoapp",
      differenceId: "abc",
    });
    expect(call.mock.calls).toEqual([
      ["integrations.list", {}],
      ["integrations.functions", { integration: "demoapp", language: "en" }],
      [
        "integrations.check",
        { integration: "demoapp", function: "tool:a", keys: "P" },
      ],
      ["integrations.items", { profile: "paint", integration: "demoapp" }],
      [
        "integrations.putItems",
        {
          profile: "paint",
          integration: "demoapp",
          revision: "r",
          items: [item],
        },
      ],
      ["integrations.plan", { profile: "paint", integration: "demoapp" }],
      [
        "integrations.write",
        {
          integration: "demoapp",
          changes: [
            { function: "tool:a", add: "P" },
            { function: "cmd:x", remove: "P" },
          ],
        },
      ],
      ["integrations.backups", { integration: "demoapp" }],
      ["integrations.restore", { integration: "demoapp", backup: "b1" }],
      [
        "integrations.dismiss",
        { profile: "paint", integration: "demoapp", differenceId: "abc" },
      ],
    ]);
  });

  it("takes the empty revision of a profile that has no integrations file yet", async () => {
    const { call, handlers } = fixture();
    await handlers.integrationsPutItems.run({
      profile: "paint",
      integration: "demoapp",
      revision: "",
      items: [item],
    });
    expect(call).toHaveBeenCalledWith("integrations.putItems", {
      profile: "paint",
      integration: "demoapp",
      revision: "",
      items: [item],
    });
    // A missing or too long revision is still a mistake of the caller.
    for (const revision of [undefined, 3, "r".repeat(257)])
      expect(() =>
        handlers.integrationsPutItems.run({
          profile: "paint",
          integration: "demoapp",
          revision,
          items: [],
        }),
      ).toThrow();
  });

  it("refuses parameters of the wrong shape before the daemon is asked", async () => {
    const { call, handlers } = fixture();
    const bad: Array<[string, unknown[]]> = [
      ["integrationsFunctions", ["Demo", "en"]],
      ["integrationsFunctions", ["demoapp", "fr"]],
      ["integrationsFunctions", ["1app", "en"]],
      ["integrationsFunctions", ["a".repeat(33), "en"]],
      ["integrationsItems", ["../x", "demoapp"]],
      ["integrationsItems", ["paint", "demo app"]],
      ["integrationsCheck", [{ integration: "demoapp", keys: "" }]],
      ["integrationsCheck", [{ integration: "demoapp", keys: 3 }]],
      ["integrationsCheck", [null]],
      [
        "integrationsCheck",
        [{ integration: "demoapp", function: 3, keys: "P" }],
      ],
      [
        "integrationsPutItems",
        [
          {
            profile: "paint",
            integration: "demoapp",
            revision: "r",
            items: "x",
          },
        ],
      ],
      [
        "integrationsPutItems",
        [
          {
            profile: "paint",
            integration: "demoapp",
            revision: "r",
            items: Array.from({ length: 513 }, () => item),
          },
        ],
      ],
      [
        "integrationsPutItems",
        [
          {
            profile: "paint",
            integration: "demoapp",
            revision: "r",
            items: [{ ...item, path: "x" }],
          },
        ],
      ],
      [
        "integrationsPutItems",
        [{ profile: "paint", integration: "demoapp", items: [] }],
      ],
      ["integrationsPlan", ["", "demoapp"]],
      ["integrationsWrite", ["demoapp", "x"]],
      ["integrationsWrite", ["demoapp", [{ function: "a" }]]],
      ["integrationsWrite", ["demoapp", [{ add: "P" }]]],
      [
        "integrationsWrite",
        [
          "demoapp",
          Array.from({ length: 1025 }, () => ({ function: "a", add: "P" })),
        ],
      ],
      ["integrationsRestore", ["demoapp", "../b"]],
      ["integrationsRestore", ["demoapp", ""]],
      [
        "integrationsDismiss",
        [{ profile: "paint", integration: "demoapp", differenceId: "" }],
      ],
    ];
    for (const [name, args] of bad)
      expect(
        () => handlers[name].run(...args),
        `${name} ${JSON.stringify(args)}`,
      ).toThrow();
    expect(call).not.toHaveBeenCalled();
  });

  it("refuses an answer of the wrong shape", async () => {
    const { handlers } = fixture(() => ({ nope: true }));
    await expect(handlers.integrationsList.run()).rejects.toThrow(
      "Integrations not read",
    );
    await expect(handlers.integrationsWrite.run("demoapp", [])).rejects.toThrow(
      "Settings not written",
    );
  });

  it("keeps only the daemon's code of a failure", async () => {
    const { handlers } = fixture(() => {
      throw Object.assign(new Error("C:\\Users\\me\\secret"), { code: -32028 });
    });
    const error = await (
      handlers.integrationsWrite.run("demoapp", [
        { function: "a", add: "P" },
      ]) as Promise<unknown>
    ).catch((e: Error) => e);
    expect(error).toMatchObject({
      message: "Settings not written",
      code: -32028,
    });
    expect(String((error as Error).message)).not.toContain("secret");
  });

  it("sends a change that adds and removes at once", async () => {
    const { call, handlers } = fixture();
    await handlers.integrationsWrite.run("demoapp", [
      { function: "a", add: "Q", remove: "P" },
    ]);
    expect(call).toHaveBeenCalledWith("integrations.write", {
      integration: "demoapp",
      changes: [{ function: "a", add: "Q", remove: "P" }],
    });
  });

  describe("the data folder", () => {
    const folderFixture = (
      chosen: string | undefined,
      files: string[] = [],
    ) => {
      const call = vi.fn<DaemonCall>(async () => ({}));
      const chooseFolder = vi.fn(async () => chosen);
      const fileExists = vi.fn(async (path: string) => files.includes(path));
      return {
        call,
        chooseFolder,
        fileExists,
        handlers: integrationHandlers(call, { chooseFolder, fileExists }),
      };
    };

    it("stores a folder that holds one of the app's files", async () => {
      const dir = "D:\\Data\\CLIPStudioPaintVer1_5_0";
      const { call, handlers, fileExists } = folderFixture(`${dir}\\`, [
        `${dir}\\Tool\\EditImageTool.todb`,
      ]);
      await expect(handlers.integrationsChooseDataDir.run("csp")).resolves.toBe(
        "changed",
      );
      expect(fileExists).toHaveBeenCalledWith(`${dir}\\Shortcut\\default.khc`);
      expect(call.mock.calls).toEqual([
        ["integrations.setDataDir", { integration: "csp", dataDir: dir }],
      ]);
    });

    it("answers a folder that is not the app's without telling the daemon", async () => {
      const { call, handlers } = folderFixture("D:\\Documents");
      await expect(handlers.integrationsChooseDataDir.run("csp")).resolves.toBe(
        "notAppFolder",
      );
      expect(call).not.toHaveBeenCalled();
    });

    it("answers a closed dialog", async () => {
      const { call, handlers } = folderFixture(undefined);
      await expect(handlers.integrationsChooseDataDir.run("csp")).resolves.toBe(
        "cancelled",
      );
      expect(call).not.toHaveBeenCalled();
    });

    it.each([
      "\\\\server\\share\\CSP",
      "D:\\Data\\..\\x",
      "relative\\CSP",
      "D:\\",
      "D:\\a|b",
    ])("never checks or sends %s", async (chosen) => {
      const { call, handlers, fileExists } = folderFixture(chosen);
      await expect(handlers.integrationsChooseDataDir.run("csp")).resolves.toBe(
        "notAppFolder",
      );
      expect(fileExists).not.toHaveBeenCalled();
      expect(call).not.toHaveBeenCalled();
    });

    it("refuses an integration whose folder it does not know, before the dialog", async () => {
      const { chooseFolder, handlers } = folderFixture("D:\\Data");
      await expect(
        handlers.integrationsChooseDataDir.run("demoapp"),
      ).rejects.toThrow("Invalid integration ID");
      await expect(
        handlers.integrationsChooseDataDir.run("../x"),
      ).rejects.toThrow("Invalid integration ID");
      expect(chooseFolder).not.toHaveBeenCalled();
    });

    it("takes the folder setting out with null", async () => {
      const { call, handlers } = folderFixture(undefined);
      await handlers.integrationsResetDataDir.run("csp");
      expect(call.mock.calls).toEqual([
        ["integrations.setDataDir", { integration: "csp", dataDir: null }],
      ]);
      expect(() => handlers.integrationsResetDataDir.run("Bad Id")).toThrow();
    });
  });
});
