import { describe, expect, it, vi } from "vitest";
import type { ConnectionState, StartupReport } from "../shared/api";
import { isDisconnectedError } from "./bridge";
import {
  type DaemonConnection,
  DaemonManager,
  type LaunchedDaemon,
} from "./daemon-manager";
import type { DaemonExit } from "./start-failure";

function connection(): DaemonConnection & { lose: () => void } {
  let lost = () => {};
  return {
    info: { pid: 42, protocol: 1 },
    call: async <T>() => ({}) as T,
    close: vi.fn(),
    onClose: vi.fn((listener: () => void) => {
      lost = listener;
    }),
    lose: () => lost(),
  };
}

/** A launched daemon whose exit the test decides. */
function daemon(): LaunchedDaemon & { exit: (exit: DaemonExit) => void } {
  let exit: (value: DaemonExit) => void = () => {};
  const exited = new Promise<DaemonExit>((done) => {
    exit = done;
  });
  return { exited, exit: (value) => exit(value) };
}

const running = (): LaunchedDaemon => ({ exited: new Promise(() => {}) });

const report: StartupReport = {
  kind: "config",
  cause: "invalid",
  file: "C:\\data\\config.json",
  line: 14,
  col: 20,
  pointer: "/safety/panic/standard/buttons",
  message: "minItems: got 1, want 2",
  dataDir: "C:\\data",
  errors: [
    {
      line: 14,
      col: 20,
      pointer: "/safety/panic/standard/buttons",
      message: "minItems: got 1, want 2",
    },
  ],
};

const unavailable = async (): Promise<DaemonConnection> => {
  throw new Error("daemon unavailable");
};

describe("UI and daemon lifecycle", () => {
  it("connects to an existing daemon without spawning", async () => {
    const bridge = connection();
    const launch = vi.fn(running);
    const manager = new DaemonManager({
      connect: async () => bridge,
      launch,
      wait: async () => {},
      attempts: 3,
    });
    await manager.connect();
    expect(manager.state.phase).toBe("connected");
    expect(manager.state.startedByUI).toBe(false);
    expect(launch).not.toHaveBeenCalled();
    manager.close();
    expect(bridge.close).toHaveBeenCalledOnce();
  });

  it("launches once only after discovery or authentication connection fails", async () => {
    const launch = vi.fn(running);
    const connect = vi
      .fn()
      .mockRejectedValueOnce(new Error("missing discovery"))
      .mockResolvedValue(connection());
    const manager = new DaemonManager({
      connect,
      launch,
      wait: async () => {},
      attempts: 3,
    });
    await manager.connect();
    expect(launch).toHaveBeenCalledOnce();
    expect(manager.state.startedByUI).toBe(true);
    manager.close();
  });

  it("fails without a report when the started daemon never answers", async () => {
    const launch = vi.fn(running);
    const connect = vi.fn(unavailable);
    const manager = new DaemonManager({
      connect,
      launch,
      wait: async () => {},
      attempts: 3,
    });
    await manager.connect();
    expect(manager.state).toMatchObject({
      phase: "failed",
      failure: { reason: "timeout" },
    });
    expect(launch).toHaveBeenCalledOnce();
    expect(connect).toHaveBeenCalledTimes(3);
  });

  it("stops waiting when the started daemon exits with its report (UI-45)", async () => {
    const started = daemon();
    const connect = vi.fn(unavailable);
    const changes: ConnectionState[] = [];
    const manager = new DaemonManager({
      connect,
      launch: () => started,
      // Only the exit ends the wait.
      wait: () => new Promise(() => {}),
      attempts: 6,
      changed: (state) => changes.push(state),
    });
    const attempt = manager.connect();
    await vi.waitFor(() => expect(connect).toHaveBeenCalledOnce());
    started.exit({ spawned: true, code: 3, report });
    await attempt;
    expect(manager.state).toEqual({
      phase: "failed",
      failure: { reason: "report", report },
      startedByUI: true,
    });
    expect(connect).toHaveBeenCalledOnce();
    expect(changes.map((state) => state.phase)).toEqual([
      "connecting",
      "failed",
    ]);
  });

  it.each([
    [
      "an exit code without a report",
      { spawned: true, code: 1 } as const,
      { reason: "exit", code: 1 },
    ],
    [
      "an end from outside",
      { spawned: true, code: null } as const,
      { reason: "exit", code: null },
    ],
    [
      "a program that cannot run",
      { spawned: false } as const,
      { reason: "spawn" },
    ],
  ])("fails with %s", async (_name, exit, failure) => {
    const started = daemon();
    const manager = new DaemonManager({
      connect: unavailable,
      launch: () => started,
      wait: () => new Promise(() => {}),
      attempts: 3,
    });
    started.exit(exit);
    await manager.connect();
    expect(manager.state).toMatchObject({ phase: "failed", failure });
  });

  it("fails when the daemon cannot be started at all", async () => {
    const connect = vi.fn(unavailable);
    const manager = new DaemonManager({
      connect,
      launch: () => {
        throw new Error("spawn EINVAL");
      },
      wait: async () => {},
      attempts: 3,
    });
    await manager.connect();
    expect(manager.state).toMatchObject({
      phase: "failed",
      failure: { reason: "spawn" },
    });
    expect(connect).toHaveBeenCalledOnce();
  });

  it("keeps connecting when the started daemon handed the request to the running one", async () => {
    const started = daemon();
    started.exit({ spawned: true, code: 0 });
    const bridge = connection();
    const connect = vi
      .fn()
      .mockRejectedValueOnce(new Error("missing"))
      .mockRejectedValueOnce(new Error("missing"))
      .mockResolvedValueOnce(bridge);
    const wait = vi.fn(async () => {});
    const manager = new DaemonManager({
      connect,
      launch: () => started,
      wait,
      attempts: 6,
    });
    await manager.connect();
    expect(manager.state.phase).toBe("connected");
    expect(wait).toHaveBeenCalledTimes(2);
    manager.close();
  });

  it("takes a report that comes after the attempts ran out", async () => {
    const started = daemon();
    const manager = new DaemonManager({
      connect: unavailable,
      launch: () => started,
      wait: async () => {},
      attempts: 2,
    });
    await manager.connect();
    expect(manager.state.failure).toEqual({ reason: "timeout" });
    // The running instance did not answer its request for 5 s.
    started.exit({
      spawned: true,
      code: 3,
      report: { ...report, kind: "instance", cause: "unavailable" },
    });
    await vi.waitFor(() =>
      expect(manager.state.failure).toMatchObject({
        reason: "report",
        report: { kind: "instance", cause: "unavailable" },
      }),
    );
  });

  it("ignores a late exit once another attempt started", async () => {
    const first = daemon();
    const bridge = connection();
    const connect = vi
      .fn()
      .mockRejectedValueOnce(new Error("missing"))
      .mockRejectedValueOnce(new Error("missing"))
      .mockResolvedValueOnce(bridge);
    const manager = new DaemonManager({
      connect,
      launch: () => first,
      wait: async () => {},
      attempts: 2,
    });
    await manager.connect();
    expect(manager.state.phase).toBe("failed");
    await manager.connect();
    expect(manager.state.phase).toBe("connected");
    first.exit({ spawned: true, code: 3, report });
    await new Promise((done) => setTimeout(done, 0));
    expect(manager.state.phase).toBe("connected");
    manager.close();
  });

  it("retries a failed start, showing the last failure until it connects", async () => {
    const changes: ConnectionState[] = [];
    const bridge = connection();
    const broken = daemon();
    broken.exit({ spawned: true, code: 3, report });
    const connect = vi
      .fn()
      .mockRejectedValueOnce(new Error("missing"))
      .mockRejectedValueOnce(new Error("missing"))
      .mockResolvedValueOnce(bridge);
    const launch = vi
      .fn()
      .mockReturnValueOnce(broken)
      .mockReturnValue(running());
    const manager = new DaemonManager({
      connect,
      launch,
      // The broken start ends the first wait; the retry waits normally.
      wait: vi
        .fn()
        .mockReturnValueOnce(new Promise(() => {}))
        .mockResolvedValue(undefined),
      attempts: 3,
      changed: (state) => changes.push(state),
    });
    await manager.connect();
    expect(manager.state.phase).toBe("failed");
    // The owner fixed config.json, then clicked 重试.
    await manager.connect();
    expect(manager.state.phase).toBe("connected");
    expect(manager.state.failure).toBeUndefined();
    expect(launch).toHaveBeenCalledTimes(2);
    expect(
      changes.map((state) => [state.phase, state.failure?.reason]),
    ).toEqual([
      ["connecting", undefined],
      ["failed", "report"],
      ["connecting", "report"],
      ["connected", undefined],
    ]);
    manager.close();
  });

  it("shows a lost connection as disconnected, and a restart that reports as failed", async () => {
    const bridge = connection();
    const restarted = daemon();
    restarted.exit({ spawned: true, code: 3, report });
    const connect = vi
      .fn()
      .mockResolvedValueOnce(bridge)
      .mockRejectedValue(new Error("missing"));
    const manager = new DaemonManager({
      connect,
      launch: () => restarted,
      wait: () => new Promise(() => {}),
      attempts: 3,
    });
    await manager.connect();
    bridge.lose();
    expect(manager.state).toMatchObject({
      phase: "disconnected",
      message: "Daemon connection was lost",
    });
    expect(manager.state.failure).toBeUndefined();
    await manager.connect();
    expect(manager.state).toMatchObject({
      phase: "failed",
      failure: { reason: "report" },
    });
  });

  it.each([
    [{ spawned: true, code: 1 } as const, { reason: "exit", code: 1 }],
    [{ spawned: false } as const, { reason: "spawn" }],
  ])(
    "shows a reconnect whose restart ends with %j as failed",
    async (end, failure) => {
      const bridge = connection();
      const restarted = daemon();
      restarted.exit(end);
      const connect = vi
        .fn()
        .mockResolvedValueOnce(bridge)
        .mockRejectedValue(new Error("missing"));
      const manager = new DaemonManager({
        connect,
        launch: () => {
          if (!end.spawned) throw new Error("cannot run");
          return restarted;
        },
        wait: () => new Promise(() => {}),
        attempts: 3,
      });
      await manager.connect();
      bridge.lose();
      await manager.connect();
      expect(manager.state).toMatchObject({ phase: "failed", failure });
    },
  );

  it("stays disconnected when a reconnect after a crash gets no answer", async () => {
    const bridge = connection();
    const connect = vi
      .fn()
      .mockResolvedValueOnce(bridge)
      .mockRejectedValue(new Error("missing"));
    const manager = new DaemonManager({
      connect,
      launch: running,
      wait: async () => {},
      attempts: 2,
    });
    await manager.connect();
    bridge.lose();
    await manager.connect();
    expect(manager.state).toMatchObject({
      phase: "disconnected",
      message: "missing",
    });
  });

  it("shows nothing when the daemon said it is exiting (DMN-22)", async () => {
    const bridge = connection();
    const changed = vi.fn();
    const connect = vi.fn(async () => bridge);
    const manager = new DaemonManager({
      connect,
      launch: running,
      wait: async () => {},
      attempts: 3,
      changed,
    });
    await manager.connect();
    changed.mockClear();
    manager.exiting();
    bridge.lose();
    expect(changed).not.toHaveBeenCalled();
    expect(manager.state.phase).toBe("connected");
    await manager.connect();
    expect(connect).toHaveBeenCalledOnce();
    await expect(manager.call("status.get")).rejects.toThrow("disconnected");
  });

  it("rejects a call without a connection with the error that says so (SCR-22)", async () => {
    const manager = new DaemonManager({
      connect: unavailable,
      launch: running,
      wait: async () => {},
      attempts: 1,
    });
    const rejected = await manager
      .call("status.get")
      .catch((error: unknown) => error);
    expect(isDisconnectedError(rejected)).toBe(true);
  });

  it("still carries the last saves and the answer to the daemon while it exits (DMN-22)", async () => {
    const bridge = connection();
    const call = vi.fn(async () => ({ saved: true }));
    bridge.call = call as unknown as typeof bridge.call;
    const manager = new DaemonManager({
      connect: async () => bridge,
      launch: running,
      wait: async () => {},
      attempts: 3,
    });
    await manager.connect();
    manager.exiting();
    await expect(
      manager.call("profiles.put", { id: "paint" }),
    ).resolves.toEqual({ saved: true });
    await manager.call("ui.exitReady", {});
    expect(call.mock.calls.map((args) => (args as unknown[])[0])).toEqual([
      "profiles.put",
      "ui.exitReady",
    ]);
    // Closing the UI ends it for good.
    manager.close();
    await expect(manager.call("ui.exitReady")).rejects.toThrow("disconnected");
  });

  it("shares concurrent attempts and discards connections completed after close", async () => {
    const bridge = connection();
    let resolve: ((value: DaemonConnection) => void) | undefined;
    const connect = vi.fn(
      () =>
        new Promise<DaemonConnection>((done) => {
          resolve = done;
        }),
    );
    const manager = new DaemonManager({
      connect,
      launch: running,
      wait: async () => {},
      attempts: 3,
    });
    const one = manager.connect();
    const two = manager.connect();
    manager.close();
    resolve?.(bridge);
    await Promise.all([one, two]);
    expect(connect).toHaveBeenCalledOnce();
    expect(bridge.close).toHaveBeenCalledOnce();
    expect(manager.state.phase).toBe("disconnected");
  });

  it("can launch a replacement after a previously started daemon exits", async () => {
    const first = connection();
    const connect = vi
      .fn()
      .mockRejectedValueOnce(new Error("missing"))
      .mockResolvedValueOnce(first)
      .mockRejectedValueOnce(new Error("daemon exited"))
      .mockResolvedValueOnce(connection());
    const launch = vi.fn(running);
    const manager = new DaemonManager({
      connect,
      launch,
      wait: async () => {},
      attempts: 3,
    });
    await manager.connect();
    first.lose();
    await Promise.all([manager.connect(), manager.connect()]);
    expect(launch).toHaveBeenCalledTimes(2);
    expect(manager.state.phase).toBe("connected");
    expect(manager.state.startedByUI).toBe(true);
    manager.close();
  });
});
