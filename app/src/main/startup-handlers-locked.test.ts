import { describe, expect, it, vi } from "vitest";
import { LibraryErrorCode } from "../shared/api";
import type { DaemonCall } from "./library-handlers";
import { startupHandlers } from "./startup-handlers";

const on = {
  available: true,
  enabled: true,
  current: true,
  mode: "inject",
} as const;

describe("start at sign-in in a packaged application under test (PKG-03)", () => {
  it("never asks the daemon to set the entry, and still reads it", async () => {
    const call = vi.fn<DaemonCall>(async () => on);
    const handlers = startupHandlers(call, true);
    await expect(handlers.startupGet.run()).resolves.toEqual(on);
    expect(() => handlers.startupSet.run(true)).toThrow(
      expect.objectContaining({ code: LibraryErrorCode.startupUnavailable }),
    );
    expect(() => handlers.startupSet.run(false)).toThrow();
    expect(call.mock.calls).toEqual([["startup.get", {}]]);
  });

  it("still checks the argument first", () => {
    const handlers = startupHandlers(
      vi.fn<DaemonCall>(async () => on),
      true,
    );
    expect(() => handlers.startupSet.run("yes")).toThrow(
      /boolean|toggle|Invalid/i,
    );
  });

  it("asks the daemon as before when not locked", async () => {
    const call = vi.fn<DaemonCall>(async () => on);
    await startupHandlers(call).startupSet.run(true);
    await startupHandlers(call, false).startupSet.run(false);
    expect(call.mock.calls).toEqual([
      ["startup.set", { enabled: true }],
      ["startup.set", { enabled: false }],
    ]);
  });
});
