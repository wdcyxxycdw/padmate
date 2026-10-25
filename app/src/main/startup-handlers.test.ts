import { describe, expect, it, vi } from "vitest";
import type { DaemonCall } from "./library-handlers";
import { startupHandlers } from "./startup-handlers";

const on = {
  available: true,
  enabled: true,
  current: true,
  mode: "inject",
} as const;

function fixture(reply: (method: string, params: unknown) => unknown) {
  const call = vi.fn<DaemonCall>(async (method, params) =>
    reply(method, params),
  );
  return { call, handlers: startupHandlers(call) };
}

describe("start at sign-in handlers", () => {
  it("declares the renderer argument counts", () => {
    const { handlers } = fixture(() => on);
    expect(
      Object.fromEntries(
        Object.entries(handlers).map(([name, item]) => [name, item.count]),
      ),
    ).toEqual({ startupGet: 0, startupSet: 1 });
  });

  it("reads and sets the daemon's entry with only a boolean", async () => {
    const { call, handlers } = fixture((method) =>
      method === "startup.get"
        ? { ...on, enabled: false, current: false, extra: "dropped" }
        : on,
    );
    await expect(handlers.startupGet.run()).resolves.toEqual({
      ...on,
      enabled: false,
      current: false,
    });
    await expect(handlers.startupSet.run(true)).resolves.toEqual(on);
    expect(call.mock.calls).toEqual([
      ["startup.get", {}],
      ["startup.set", { enabled: true }],
    ]);
  });

  it("keeps why the entry is unavailable", async () => {
    const off = {
      available: false,
      enabled: false,
      current: false,
      mode: "dry-run",
      reason: "development",
    };
    const { handlers } = fixture(() => off);
    await expect(handlers.startupGet.run()).resolves.toEqual(off);
  });

  it.each([
    "yes",
    1,
    null,
    undefined,
    { enabled: true },
    { exe: "C:\\evil.exe" },
  ])("refuses %j before asking the daemon", async (value) => {
    const { call, handlers } = fixture(() => on);
    await expect(async () => handlers.startupSet.run(value)).rejects.toThrow(
      "boolean",
    );
    expect(call).not.toHaveBeenCalled();
  });

  it.each([
    null,
    [],
    { ...on, available: "yes" },
    { ...on, current: undefined },
    { ...on, mode: "live" },
    { ...on, reason: "other" },
  ])("rejects the daemon response %j", async (reply) => {
    const { handlers } = fixture(() => reply);
    await expect(handlers.startupGet.run()).rejects.toThrow(
      "Invalid daemon response",
    );
  });

  it("keeps unavailable, and no path from a system error", async () => {
    const unavailable = {
      message: "login startup cannot be set in a development build",
      code: -32017,
      data: { available: false },
    };
    let failure: unknown = unavailable;
    const { handlers } = fixture(() => {
      throw failure;
    });
    await expect(handlers.startupSet.run(true)).rejects.toBe(unavailable);
    failure = {
      message:
        "open C:\\Users\\someone\\AppData\\Roaming\\padmate\\startup\\x.json: denied",
      code: -32098,
    };
    await expect(handlers.startupSet.run(true)).rejects.toMatchObject({
      message: "Startup entry not changed",
      code: -32098,
    });
  });

  it("fails without a daemon", async () => {
    const handlers = startupHandlers(() => undefined);
    await expect(handlers.startupGet.run()).rejects.toThrow("not connected");
  });
});
