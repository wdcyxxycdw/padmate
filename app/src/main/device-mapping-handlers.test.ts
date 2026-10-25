import { describe, expect, it, vi } from "vitest";
import { deviceHandlers } from "./device-handlers";
import type { DaemonCall } from "./library-handlers";
import type { RawWatch } from "./raw-watch";

const guid = "ff00aabb000000000000000000007600";
const text = `${guid},*,a:b0,platform:Windows`;

const mappingAnswer = {
  device: "030000007e05/p:x#1",
  guid,
  mapping: text,
  keys: ["030000007e05/p:x"],
  changed: true,
  applied: true,
  revision: "r2",
  notes: ["the name was replaced by *"],
  devices: [{ device: 7, gamepad: true, applied: true }],
  warnings: [{ code: "noBack" }, { code: "shadowed", device: 7 }],
};

function fixture(reply: (method: string, params: unknown) => unknown) {
  const call = vi.fn<DaemonCall>(async (method, params) =>
    reply(method, params),
  );
  return { call, handlers: deviceHandlers(call) };
}

describe("mapping handlers (IN-18, IN-19)", () => {
  it("asks devices.setMapping with the controller and the mapping, and returns what it needs of the answer", async () => {
    const { call, handlers } = fixture(() => mappingAnswer);
    await expect(handlers.mappingSet.run("7", text)).resolves.toEqual({
      device: mappingAnswer.device,
      guid,
      mapping: text,
      keys: mappingAnswer.keys,
      changed: true,
      applied: true,
      revision: "r2",
      notes: mappingAnswer.notes,
      warnings: mappingAnswer.warnings,
    });
    expect(call.mock.calls).toEqual([
      ["devices.setMapping", { device: "7", mapping: text }],
    ]);
  });

  it("takes a problem of the commit, and an answer without a revision", async () => {
    const { handlers } = fixture(() => ({
      ...mappingAnswer,
      applied: false,
      problem: "restart needed",
      revision: undefined,
      changed: false,
      warnings: [],
    }));
    await expect(handlers.mappingSet.run("7", text)).resolves.toMatchObject({
      applied: false,
      problem: "restart needed",
      changed: false,
      warnings: [],
    });
  });

  it("refuses a bad selector or mapping before the daemon is asked", async () => {
    const { call, handlers } = fixture(() => mappingAnswer);
    for (const device of [undefined, "", 3, "a\nb"])
      await expect(
        Promise.resolve().then(() => handlers.mappingSet.run(device, text)),
        String(device),
      ).rejects.toThrow("Invalid device selector");
    for (const mapping of [undefined, 7, "", "a\nb", "x".repeat(2049)])
      await expect(
        Promise.resolve().then(() => handlers.mappingSet.run("7", mapping)),
        String(mapping),
      ).rejects.toThrow("Invalid mapping");
    await expect(
      Promise.resolve().then(() => handlers.mappingReset.run(undefined)),
    ).rejects.toThrow("Invalid device selector");
    expect(call).not.toHaveBeenCalled();
  });

  it("keeps the code and drops the message of a mapping the daemon refuses", async () => {
    const { handlers } = fixture(() => {
      throw Object.assign(new Error("SDL said C:\\Users\\x"), {
        code: -32025,
        data: { reason: "syntax" },
      });
    });
    const error = await Promise.resolve(
      handlers.mappingSet.run("7", text),
    ).catch((e: unknown) => e);
    expect(error).toMatchObject({
      message: "Mapping not saved",
      code: -32025,
    });
  });

  it("refuses an answer that is not a mapping result", async () => {
    for (const reply of [
      null,
      [],
      {},
      { ...mappingAnswer, keys: "none" },
      { ...mappingAnswer, keys: [1] },
      { ...mappingAnswer, changed: 1 },
      { ...mappingAnswer, applied: "yes" },
      { ...mappingAnswer, notes: [7] },
      { ...mappingAnswer, warnings: [{}] },
      { ...mappingAnswer, warnings: [{ code: 7 }] },
      { ...mappingAnswer, warnings: "none" },
      { ...mappingAnswer, problem: 7 },
    ]) {
      const { handlers } = fixture(() => reply);
      await expect(
        handlers.mappingSet.run("7", text),
        JSON.stringify(reply),
      ).rejects.toMatchObject({ message: "Mapping not saved" });
    }
  });

  it("resets the controller's mapping", async () => {
    const { call, handlers } = fixture(() => ({
      ...mappingAnswer,
      mapping: "",
      warnings: [],
    }));
    await expect(handlers.mappingReset.run("7")).resolves.toMatchObject({
      mapping: "",
      changed: true,
    });
    expect(call.mock.calls).toEqual([
      ["devices.resetMapping", { device: "7" }],
    ]);
    const bad = fixture(() => ({}));
    await expect(bad.handlers.mappingReset.run("7")).rejects.toMatchObject({
      message: "Mapping not reset",
    });
  });

  it("fails without a daemon connection", async () => {
    const handlers = deviceHandlers(() => undefined);
    await expect(handlers.mappingSet.run("7", text)).rejects.toThrow(
      "Daemon is not connected",
    );
    await expect(handlers.mappingReset.run("7")).rejects.toThrow(
      "Daemon is not connected",
    );
  });
});

describe("raw watch handlers (IN-17, IN-19)", () => {
  it("start and stop go through the one RawWatch, with a checked selector", async () => {
    const start = vi.fn(async (_device: string) => ({ id: 7 }));
    const stop = vi.fn(async (_device: string) => {});
    const handlers = deviceHandlers(() => undefined, {
      start,
      stop,
    } as unknown as RawWatch);
    await expect(handlers.rawWatchStart.run("7")).resolves.toEqual({ id: 7 });
    await handlers.rawWatchStop.run("7");
    expect(start).toHaveBeenCalledExactlyOnceWith("7");
    expect(stop).toHaveBeenCalledExactlyOnceWith("7");
    for (const device of [undefined, "", 3, "a\nb"]) {
      await expect(
        Promise.resolve().then(() => handlers.rawWatchStart.run(device)),
      ).rejects.toThrow("Invalid device selector");
      await expect(
        Promise.resolve().then(() => handlers.rawWatchStop.run(device)),
      ).rejects.toThrow("Invalid device selector");
    }
    expect(start).toHaveBeenCalledOnce();
    expect(stop).toHaveBeenCalledOnce();
  });
});
