import { describe, expect, it, vi } from "vitest";
import { joyCon2Handlers, joyCon2PairSeconds } from "./joycon2-handlers";
import type { DaemonCall } from "./library-handlers";

const state = {
  available: true,
  autoConnect: true,
  windowOpen: false,
  scanning: false,
  known: [
    {
      addr: "C8:48:05:FA:01:01",
      serial: "FAKEJC2L0001",
      side: "L",
      connected: false,
    },
  ],
  links: [],
};

function fixture(
  reply: (method: string, params: unknown) => unknown = () => state,
) {
  const call = vi.fn<DaemonCall>(async (method, params) =>
    reply(method, params),
  );
  const watch = vi.fn(async (_on: boolean) => {});
  return { call, watch, handlers: joyCon2Handlers(call, watch) };
}

describe("Joy-Con 2 handlers", () => {
  it("declares the renderer argument counts", () => {
    const { handlers } = fixture();
    expect(
      Object.fromEntries(
        Object.entries(handlers).map(([name, item]) => [name, item.count]),
      ),
    ).toEqual({
      joyCon2Status: 0,
      joyCon2Watch: 1,
      joyCon2Pair: 0,
      joyCon2CancelPair: 0,
      joyCon2Forget: 1,
    });
  });

  it("asks the daemon for its fixed methods with only what they need", async () => {
    const { call, handlers } = fixture();
    await handlers.joyCon2Status.run();
    await handlers.joyCon2Pair.run();
    await handlers.joyCon2CancelPair.run();
    await handlers.joyCon2Forget.run("FAKEJC2L0001");
    expect(call.mock.calls).toEqual([
      ["joycon2.status", {}],
      ["joycon2.pair", { seconds: joyCon2PairSeconds }],
      ["joycon2.cancelPair", {}],
      ["joycon2.forget", { serial: "FAKEJC2L0001" }],
    ]);
    expect(joyCon2PairSeconds).toBe(30);
  });

  it("answers with the checked state, and drops what the page does not use", async () => {
    const { handlers } = fixture();
    await expect(handlers.joyCon2Status.run()).resolves.toEqual({
      available: true,
      autoConnect: true,
      windowOpen: false,
      windowLeftMs: 0,
      known: [
        {
          addr: "C8:48:05:FA:01:01",
          serial: "FAKEJC2L0001",
          side: "L",
          connected: false,
        },
      ],
      links: [],
    });
  });

  it("refuses an answer that is not a state", async () => {
    for (const reply of [null, [], "x", {}, { available: 1 }]) {
      const { handlers } = fixture(() => reply);
      await expect(
        handlers.joyCon2Status.run(),
        JSON.stringify(reply),
      ).rejects.toThrow("Joy-Con 2 state not read");
    }
  });

  it("keeps the daemon's code and nothing of its message", async () => {
    const call = vi.fn<DaemonCall>(async () => {
      throw Object.assign(new Error("input: C:\\Users\\x\\state.json"), {
        code: -32027,
      });
    });
    const handlers = joyCon2Handlers(call, async () => {});
    for (const run of [
      () => handlers.joyCon2Pair.run(),
      () => handlers.joyCon2Forget.run("S1"),
    ]) {
      const error = await Promise.resolve(run()).then(
        () => null,
        (e: unknown) => e as Error & { code?: number },
      );
      expect(error?.code).toBe(-32027);
      expect(error?.message).not.toContain("state.json");
    }
  });

  it("fails when the daemon is not connected", async () => {
    const handlers = joyCon2Handlers(
      () => undefined,
      async () => {},
    );
    await expect(handlers.joyCon2Status.run()).rejects.toThrow(
      "Daemon is not connected",
    );
  });

  it("refuses a serial number that is not printable ASCII of a sane length", async () => {
    const { call, handlers } = fixture();
    for (const serial of [
      undefined,
      null,
      7,
      "",
      "has space",
      "a\nb",
      "x".repeat(65),
      "序列号",
    ])
      await expect(
        Promise.resolve().then(() => handlers.joyCon2Forget.run(serial)),
        String(serial),
      ).rejects.toThrow("Invalid Joy-Con 2 serial number");
    expect(call).not.toHaveBeenCalled();
  });

  it("passes the topic wish on, and only a boolean", async () => {
    const { watch, handlers } = fixture();
    await handlers.joyCon2Watch.run(true);
    await handlers.joyCon2Watch.run(false);
    expect(watch.mock.calls).toEqual([[true], [false]]);
    for (const on of [undefined, 1, "true", null])
      await expect(
        Promise.resolve().then(() => handlers.joyCon2Watch.run(on)),
      ).rejects.toThrow("Expected a boolean setting");
    expect(watch).toHaveBeenCalledTimes(2);
  });
});
