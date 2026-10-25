import { afterEach, describe, expect, it, vi } from "vitest";
import type { DaemonEvent } from "../shared/api";
import { CloseGuard, EXIT_WAIT_MS } from "./close-guard";
import {
  ACK_WAIT_MS,
  type DaemonExitReason,
  exitAfterSaves,
  holdsWindowClose,
  routeDaemonEvents,
} from "./daemon-exit";

function fixture() {
  const deliver = vi.fn<(event: DaemonEvent) => void>();
  const trayExit = vi.fn<(reason: DaemonExitReason) => void>();
  return { deliver, trayExit, route: routeDaemonEvents({ deliver, trayExit }) };
}

describe("the daemon's exit event (DMN-22, PKG-04)", () => {
  it("quits the UI once for the tray's 退出, never telling the renderer", () => {
    const { deliver, trayExit, route } = fixture();
    route({ topic: "daemon.exiting", data: { reason: "tray" } });
    route({ topic: "daemon.exiting", data: { reason: "tray" } });
    expect(trayExit).toHaveBeenCalledOnce();
    expect(trayExit).toHaveBeenCalledWith("tray");
    expect(deliver).not.toHaveBeenCalled();
  });

  it("quits the UI once for the installer's exit (PKG-04), like the tray's", () => {
    const { deliver, trayExit, route } = fixture();
    route({ topic: "daemon.exiting", data: { reason: "installer" } });
    expect(trayExit).toHaveBeenCalledOnce();
    expect(trayExit).toHaveBeenCalledWith("installer");
    expect(deliver).not.toHaveBeenCalled();
  });

  it("answers the first exit only, whatever the reason of the next", () => {
    const { trayExit, route } = fixture();
    route({ topic: "daemon.exiting", data: { reason: "installer" } });
    route({ topic: "daemon.exiting", data: { reason: "tray" } });
    route({ topic: "daemon.exiting", data: { reason: "installer" } });
    expect(trayExit).toHaveBeenCalledOnce();
    expect(trayExit).toHaveBeenCalledWith("installer");
  });

  it.each([
    { reason: "session" },
    { reason: "TRAY" },
    { reason: "Installer" },
    { reason: "" },
    { reason: ["installer"] },
    {},
    null,
    "tray",
    "installer",
  ])(
    "leaves the UI open for %j: the connection's end shows as before",
    (data) => {
      const { deliver, trayExit, route } = fixture();
      route({ topic: "daemon.exiting", data });
      expect(trayExit).not.toHaveBeenCalled();
      expect(deliver).not.toHaveBeenCalled();
    },
  );

  it("delivers every other event", () => {
    const { deliver, trayExit, route } = fixture();
    const event: DaemonEvent = {
      topic: "input",
      data: { device: 1, kind: "down" },
    };
    route(event);
    expect(deliver).toHaveBeenCalledWith(event);
    expect(trayExit).not.toHaveBeenCalled();
  });
});

describe("quitting after the saves (DMN-22, G9)", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  /** A guard whose renderer answers only when the test says so. */
  function exit(
    options: {
      canAsk?: boolean;
      ready?: () => Promise<unknown> | undefined;
    } = {},
  ) {
    const order: string[] = [];
    const ask = vi.fn(() => {
      order.push("ask");
      return options.canAsk ?? true;
    });
    const close = vi.fn();
    const guard = new CloseGuard({ ask, close });
    const ready = vi.fn(() => {
      order.push("ready");
      return options.ready
        ? options.ready()
        : Promise.resolve({ exiting: true });
    });
    const quit = vi.fn(() => order.push("quit"));
    const done = exitAfterSaves({
      settle: () => guard.settle(),
      ready,
      quit,
    });
    return { guard, ask, close, ready, quit, order, done };
  }

  it("has the renderer finish, tells the daemon, then quits", async () => {
    vi.useFakeTimers();
    const { guard, order, quit, done } = exit();
    await vi.advanceTimersByTimeAsync(300);
    // A slow save: nothing is told or quit while it runs.
    expect(order).toEqual(["ask"]);
    expect(quit).not.toHaveBeenCalled();
    guard.release();
    await done;
    expect(order).toEqual(["ask", "ready", "quit"]);
  });

  it("does not wait longer than the daemon does, even when the renderer is stuck", async () => {
    vi.useFakeTimers();
    const { order, done } = exit();
    let finished = false;
    void done.then(() => {
      finished = true;
    });
    await vi.advanceTimersByTimeAsync(EXIT_WAIT_MS - 1);
    expect(order).toEqual(["ask"]);
    await vi.advanceTimersByTimeAsync(1);
    await done;
    expect(finished).toBe(true);
    expect(order).toEqual(["ask", "ready", "quit"]);
  });

  it("quits at once, still telling the daemon, when no renderer can be asked", async () => {
    const { order, done } = exit({ canAsk: false });
    await done;
    expect(order).toEqual(["ask", "ready", "quit"]);
  });

  it("quits even when the daemon never answers or refuses", async () => {
    vi.useFakeTimers();
    const stuck = exit({ canAsk: false, ready: () => new Promise(() => {}) });
    await vi.advanceTimersByTimeAsync(ACK_WAIT_MS - 1);
    expect(stuck.quit).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    await stuck.done;
    expect(stuck.quit).toHaveBeenCalledOnce();

    for (const ready of [
      () => Promise.reject(new Error("Daemon is disconnected")),
      () => undefined,
      () => {
        throw new Error("no manager");
      },
    ]) {
      const refused = exit({ canAsk: false, ready });
      await refused.done;
      expect(refused.quit).toHaveBeenCalledOnce();
    }
  });

  it("leaves the guard open for the quit that follows", async () => {
    vi.useFakeTimers();
    const { guard, close, done } = exit();
    guard.release();
    await done;
    expect(guard.hold()).toBe(false);
    expect(close).not.toHaveBeenCalled();
  });
});

describe("the window's close during the tray exit (DMN-22)", () => {
  it("holds a close while the exit waits for the saves", () => {
    const guard = vi.fn(() => false);
    expect(
      holdsWindowClose({ trayExiting: true, quitting: false }, guard),
    ).toBe(true);
    expect(guard).not.toHaveBeenCalled();
  });

  it("lets the close of the UI's own quit through, or Electron cancels the quit", () => {
    const guard = vi.fn(() => true);
    expect(holdsWindowClose({ trayExiting: true, quitting: true }, guard)).toBe(
      false,
    );
    expect(guard).not.toHaveBeenCalled();
  });

  it("leaves every other close to the guard", () => {
    expect(
      holdsWindowClose({ trayExiting: false, quitting: false }, () => true),
    ).toBe(true);
    expect(
      holdsWindowClose({ trayExiting: false, quitting: false }, () => false),
    ).toBe(false);
    expect(
      holdsWindowClose({ trayExiting: false, quitting: true }, () => false),
    ).toBe(false);
  });
});
