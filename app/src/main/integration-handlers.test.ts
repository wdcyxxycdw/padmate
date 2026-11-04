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
        categories: [],
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
});
