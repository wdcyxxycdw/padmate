import { describe, expect, it, vi } from "vitest";
import { padFixture, padsFixture } from "../renderer/testing/pads-fixture";
import type { DaemonCall } from "./library-handlers";
import { padHandlers } from "./pad-handlers";

const pad = padFixture("joyConRight");
const list = padsFixture([pad]);
const saved = { revision: "rev-2", applied: true, pad };

function fixture(
  reply: (method: string, params: unknown) => unknown = (method) =>
    method === "pads.list"
      ? list
      : method === "pads.set"
        ? saved
        : { ...saved, changed: true, legacy: list.legacy, pads: [pad] },
) {
  const call = vi.fn<DaemonCall>(async (method, params) =>
    reply(method, params),
  );
  return { call, handlers: padHandlers(call) };
}

describe("controller settings handlers", () => {
  it("declares the renderer argument counts", () => {
    const { handlers } = fixture();
    expect(
      Object.fromEntries(
        Object.entries(handlers).map(([name, item]) => [name, item.count]),
      ),
    ).toEqual({ padsList: 0, padsSet: 1, padsMigrate: 0 });
  });

  it("asks the daemon for its fixed methods", async () => {
    const { call, handlers } = fixture();
    await expect(handlers.padsList.run()).resolves.toEqual(list);
    await handlers.padsMigrate.run();
    await expect(
      handlers.padsSet.run({ key: pad.key, functionKey: "rightshoulder" }),
    ).resolves.toEqual(saved);
    expect(call.mock.calls).toEqual([
      ["pads.list", {}],
      ["pads.migrate", {}],
      ["pads.set", { key: pad.key, functionKey: "rightshoulder" }],
    ]);
  });

  it("sends a member only when the page names it, null included", async () => {
    const { call, handlers } = fixture();
    await handlers.padsSet.run({
      key: pad.key,
      revision: "rev-1",
      functionKey: null,
      feedback: { led: false },
      panic: { buttons: ["north", "south"], holdMs: 1500 },
    });
    await handlers.padsSet.run({ key: pad.key, feedback: null, panic: null });
    expect(call.mock.calls).toEqual([
      [
        "pads.set",
        {
          key: pad.key,
          revision: "rev-1",
          functionKey: null,
          feedback: { led: false },
          panic: { buttons: ["north", "south"], holdMs: 1500 },
        },
      ],
      ["pads.set", { key: pad.key, feedback: null, panic: null }],
    ]);
  });

  it.each([
    ["no object", "x"],
    ["no key", {}],
    ["an empty key", { key: "" }],
    ["a control that is no function key", { key: "k", functionKey: "guide" }],
    ["a function key that is no string", { key: "k", functionKey: 3 }],
    ["an unknown switch", { key: "k", feedback: { sound: true } }],
    ["a switch that is no boolean", { key: "k", feedback: { led: "no" } }],
    ["a combo without buttons", { key: "k", panic: { holdMs: 2000 } }],
    [
      "a combo with a button that is no name",
      { key: "k", panic: { buttons: ["a b"], holdMs: 2000 } },
    ],
    [
      "a combo with a fractional hold",
      { key: "k", panic: { buttons: ["a", "b"], holdMs: 1.5 } },
    ],
    ["a revision that is no string", { key: "k", revision: 5 }],
  ])("refuses %s without asking the daemon", (_name, params) => {
    const { call, handlers } = fixture();
    expect(() => handlers.padsSet.run(params)).toThrow();
    expect(call).not.toHaveBeenCalled();
  });

  it("refuses an answer of the wrong shape", async () => {
    const { handlers } = fixture(() => ({ pads: "no" }));
    await expect(handlers.padsList.run()).rejects.toThrow(
      "Controllers not read",
    );
    await expect(
      handlers.padsSet.run({ key: pad.key, functionKey: null }),
    ).rejects.toThrow("Controller settings not saved");
    await expect(handlers.padsMigrate.run()).rejects.toThrow(
      "Controller settings not moved",
    );
  });

  it("keeps the code of the daemon and nothing of its message", async () => {
    const { handlers } = fixture(() => {
      throw Object.assign(new Error("conflict in a private folder"), {
        code: -32009,
      });
    });
    const error = await Promise.resolve(
      handlers.padsSet.run({ key: pad.key, functionKey: null }),
    ).catch((e: Error & { code?: number }) => e);
    expect(error).toMatchObject({
      message: "Controller settings not saved",
      code: -32009,
    });
  });

  it("is not connected without a daemon", async () => {
    const handlers = padHandlers(() => undefined);
    await expect(handlers.padsList.run()).rejects.toThrow(
      "Daemon is not connected",
    );
  });
});
