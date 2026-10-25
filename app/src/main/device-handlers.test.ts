import { describe, expect, it, vi } from "vitest";
import { deviceHandlers } from "./device-handlers";
import type { DaemonCall } from "./library-handlers";

function fixture(reply: (method: string, params: unknown) => unknown) {
  const call = vi.fn<DaemonCall>(async (method, params) =>
    reply(method, params),
  );
  return { call, handlers: deviceHandlers(call) };
}

describe("identify handler (IN-13)", () => {
  it("declares the renderer argument count", () => {
    const { handlers } = fixture(() => ({}));
    expect(
      Object.fromEntries(
        Object.entries(handlers).map(([name, item]) => [name, item.count]),
      ),
    ).toEqual({
      identifyDevice: 1,
      latencyGet: 1,
      latencyReset: 1,
      rawWatchStart: 1,
      rawWatchStop: 1,
      mappingSet: 2,
      mappingReset: 1,
    });
  });

  it("asks the daemon with only the key, and returns the three cues", async () => {
    const { call, handlers } = fixture(() => ({
      rumble: true,
      led: false,
      player: true,
      extra: "dropped",
    }));
    await expect(handlers.identifyDevice.run("0300/p:x")).resolves.toEqual({
      rumble: true,
      led: false,
      player: true,
    });
    expect(call.mock.calls).toEqual([
      ["devices.identify", { device: "0300/p:x" }],
    ]);
  });

  it("refuses a bad selector before the daemon is asked", async () => {
    const { call, handlers } = fixture(() => ({}));
    for (const key of [undefined, null, "", 3, {}, "a\nb", "x".repeat(1025)])
      await expect(
        Promise.resolve().then(() => handlers.identifyDevice.run(key)),
        String(key),
      ).rejects.toThrow("Invalid device selector");
    expect(call).not.toHaveBeenCalled();
  });

  it("refuses an answer that is not the three cues", async () => {
    for (const reply of [
      null,
      [],
      "yes",
      {},
      { rumble: true, led: true },
      { rumble: 1, led: 0, player: 0 },
    ]) {
      const { handlers } = fixture(() => reply);
      await expect(
        handlers.identifyDevice.run("7"),
        JSON.stringify(reply),
      ).rejects.toMatchObject({ message: "Controller not identified" });
    }
  });

  it("keeps the code of a failure and drops its message", async () => {
    const gone = Object.assign(new Error("device is not connected"), {
      code: -32004,
    });
    const { handlers } = fixture(() => {
      throw gone;
    });
    await expect(handlers.identifyDevice.run("7")).rejects.toMatchObject({
      message: "Controller not identified",
      code: -32004,
    });
    const failing = fixture(() => {
      throw new Error("SDL: could not write C:\\Users\\someone\\x");
    });
    const error = await Promise.resolve(
      failing.handlers.identifyDevice.run("7"),
    ).catch((e: unknown) => e);
    expect(error).toMatchObject({ message: "Controller not identified" });
    expect((error as { code?: unknown }).code).toBeUndefined();
  });

  it("fails without a daemon connection", async () => {
    const handlers = deviceHandlers(() => undefined);
    await expect(handlers.identifyDevice.run("7")).rejects.toThrow(
      "Daemon is not connected",
    );
  });
});

const latencyAnswer = {
  devices: [
    {
      device: "0300/p:x",
      id: 3,
      name: "Pad",
      samples: 120,
      p50: 210,
      p90: 480,
      p99: 1320,
      max: 1710,
      warn: false,
      extra: "dropped",
    },
  ],
  dryRun: false,
  warnP99Us: 1500,
  warnMinSamples: 50,
};

describe("latency handlers (DMN-15, UI-22)", () => {
  it("declares the renderer argument count", () => {
    const { handlers } = fixture(() => ({}));
    expect(handlers.latencyGet.count).toBe(1);
    expect(handlers.latencyReset.count).toBe(1);
  });

  it("asks latency.get with no selector for all pads, and returns the figures it knows", async () => {
    const { call, handlers } = fixture(() => latencyAnswer);
    const result = await handlers.latencyGet.run(undefined);
    expect(call.mock.calls).toEqual([["latency.get", {}]]);
    expect(result).toEqual({
      ...latencyAnswer,
      devices: [{ ...latencyAnswer.devices[0], extra: undefined }],
    });
    expect(Object.keys((result as { devices: object[] }).devices[0])).toEqual([
      "device",
      "id",
      "name",
      "samples",
      "p50",
      "p90",
      "p99",
      "max",
      "warn",
    ]);
  });

  it("passes a checked selector on", async () => {
    const { call, handlers } = fixture(() => latencyAnswer);
    await handlers.latencyGet.run("0300/p:x");
    expect(call.mock.calls).toEqual([["latency.get", { device: "0300/p:x" }]]);
  });

  it("refuses a bad selector before the daemon is asked", async () => {
    const { call, handlers } = fixture(() => latencyAnswer);
    for (const device of [3, null, {}, "a\nb", "x".repeat(1025)]) {
      await expect(
        Promise.resolve().then(() => handlers.latencyGet.run(device)),
        String(device),
      ).rejects.toThrow("Invalid device selector");
      await expect(
        Promise.resolve().then(() => handlers.latencyReset.run(device)),
        String(device),
      ).rejects.toThrow("Invalid device selector");
    }
    expect(call).not.toHaveBeenCalled();
  });

  it("refuses an answer that is not the figures", async () => {
    const pad = latencyAnswer.devices[0];
    for (const reply of [
      null,
      [],
      "fast",
      {},
      { ...latencyAnswer, devices: "none" },
      { ...latencyAnswer, devices: [null] },
      { ...latencyAnswer, devices: [{ ...pad, p99: "1320" }] },
      { ...latencyAnswer, devices: [{ ...pad, warn: 0 }] },
      { ...latencyAnswer, devices: [{ ...pad, name: 7 }] },
      { ...latencyAnswer, dryRun: "no" },
      { ...latencyAnswer, warnP99Us: undefined },
    ]) {
      const { handlers } = fixture(() => reply);
      await expect(
        handlers.latencyGet.run(undefined),
        JSON.stringify(reply),
      ).rejects.toMatchObject({ message: "Latency not read" });
    }
  });

  it("clears the samples of all pads or one, and says it did", async () => {
    const { call, handlers } = fixture(() => ({ reset: true }));
    await expect(handlers.latencyReset.run(undefined)).resolves.toEqual({
      reset: true,
    });
    await expect(handlers.latencyReset.run("3")).resolves.toEqual({
      reset: true,
    });
    expect(call.mock.calls).toEqual([
      ["latency.reset", {}],
      ["latency.reset", { device: "3" }],
    ]);
    for (const reply of [null, [], {}, { reset: "yes" }]) {
      const bad = fixture(() => reply);
      await expect(
        bad.handlers.latencyReset.run(undefined),
        JSON.stringify(reply),
      ).rejects.toMatchObject({ message: "Latency not cleared" });
    }
  });

  it("keeps the code of a failure and drops its message", async () => {
    const gone = Object.assign(new Error("device is not connected"), {
      code: -32004,
    });
    const { handlers } = fixture(() => {
      throw gone;
    });
    await expect(handlers.latencyGet.run("7")).rejects.toMatchObject({
      message: "Latency not read",
      code: -32004,
    });
    await expect(handlers.latencyReset.run("7")).rejects.toMatchObject({
      message: "Latency not cleared",
      code: -32004,
    });
  });

  it("fails without a daemon connection", async () => {
    const handlers = deviceHandlers(() => undefined);
    await expect(handlers.latencyGet.run(undefined)).rejects.toThrow(
      "Daemon is not connected",
    );
    await expect(handlers.latencyReset.run(undefined)).rejects.toThrow(
      "Daemon is not connected",
    );
  });
});
