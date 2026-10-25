import { afterEach, describe, expect, it, vi } from "vitest";
import {
  bridgeDisconnected,
  daemonTopics,
  managerDisconnected,
} from "./bridge";
import { HudSubscription, hudRetryMs } from "./hud-subscription";

afterEach(() => {
  vi.useRealTimers();
});

type Call = (method: string, params: unknown) => Promise<unknown>;

/** Whether a subscribe call asked for the hud topic. */
function withHud(call: { mock: { calls: unknown[][] } }, index: number) {
  const [method, params] = call.mock.calls[index] as [
    string,
    { topics: string[] },
  ];
  expect(method).toBe("subscribe");
  return params.topics.includes("hud");
}

/** The hud flag of every subscribe call made so far, in order. */
function asked(call: { mock: { calls: unknown[][] } }): boolean[] {
  return call.mock.calls.map((_, index) => withHud(call, index));
}

const refused = () =>
  Object.assign(new Error("unknown topic"), { code: -32602 });

/** A call whose answers the test gives when it likes. */
function pending() {
  const waiting: Array<{
    resolve: () => void;
    reject: (error: Error) => void;
  }> = [];
  const call = vi.fn(
    () =>
      new Promise<unknown>((resolve, reject) => {
        waiting.push({ resolve: () => resolve({}), reject });
      }),
  );
  return { call, waiting };
}

/** Lets the calls that are done settle, with fake timers on. */
const settle = () => vi.advanceTimersByTimeAsync(0);

function fixture() {
  vi.useFakeTimers();
  // The call of the manager, and the one of a bridge the manager does not have yet.
  const manager = vi.fn<Call>(async () => ({}));
  const bridge = vi.fn<Call>(async () => ({}));
  const hud = new HudSubscription(manager);
  return { hud, manager, bridge };
}

describe("a connection made while the overlay is not up yet (SCR-22)", () => {
  it("subscribes without hud, and with it once the overlay's window is ready", async () => {
    const { hud, manager, bridge } = fixture();
    await hud.open(bridge);
    expect(asked(bridge)).toEqual([false]);
    hud.connected();
    await settle();
    // Nothing to change yet: the daemon was told what the overlay is.
    expect(manager).not.toHaveBeenCalled();

    hud.overlayUp(true);
    await settle();
    expect(asked(manager)).toEqual([true]);
    expect(manager.mock.calls[0][1]).toEqual({ topics: [...daemonTopics] });
    // The same news again is no news.
    hud.overlayUp(true);
    await settle();
    expect(manager).toHaveBeenCalledOnce();
  });

  it("goes without hud again when the overlay is given up", async () => {
    const { hud, manager, bridge } = fixture();
    await hud.open(bridge);
    hud.connected();
    hud.overlayUp(true);
    await settle();
    hud.overlayUp(false);
    await settle();
    expect(asked(manager)).toEqual([true, false]);
    expect(manager.mock.calls[1][1]).toEqual({
      topics: daemonTopics.filter((topic) => topic !== "hud"),
    });
  });

  it("sends a change made while the connection was being made, once it is", async () => {
    const { hud, manager, bridge } = fixture();
    await hud.open(bridge);
    // The manager does not have the connection yet: a call would find none.
    hud.overlayUp(true);
    await settle();
    expect(manager).not.toHaveBeenCalled();
    hud.connected();
    await settle();
    expect(asked(manager)).toEqual([true]);
  });

  it("sends a change made while its own subscription was on the way", async () => {
    const { hud, manager } = fixture();
    const slow = pending();
    const opened = hud.open(slow.call);
    hud.overlayUp(true);
    slow.waiting[0].resolve();
    await opened;
    // It asked without hud, as the overlay was when it began.
    expect(asked(slow.call)).toEqual([false]);
    hud.connected();
    await settle();
    expect(asked(manager)).toEqual([true]);
  });
});

describe("a connection made while the overlay is up", () => {
  it("subscribes with hud from the start and asks nothing more", async () => {
    const { hud, manager, bridge } = fixture();
    hud.overlayUp(true);
    await hud.open(bridge);
    expect(asked(bridge)).toEqual([true]);
    hud.connected();
    await settle();
    expect(manager).not.toHaveBeenCalled();
  });

  it("subscribes the next connection as the overlay is by then", async () => {
    const { hud, manager, bridge } = fixture();
    hud.overlayUp(true);
    await hud.open(bridge);
    hud.connected();
    // The connection is lost and the overlay is given up before the next one.
    hud.disconnected();
    hud.overlayUp(false);
    await settle();
    expect(manager).not.toHaveBeenCalled();
    const next = vi.fn<Call>(async () => ({}));
    await hud.open(next);
    hud.connected();
    await settle();
    expect(asked(next)).toEqual([false]);
    expect(manager).not.toHaveBeenCalled();
  });

  it("does not take a connection that could not subscribe as one it told", async () => {
    const { hud, manager } = fixture();
    const failing = vi.fn<Call>(async () => {
      throw new Error("Daemon request timed out");
    });
    await expect(hud.open(failing)).rejects.toThrow("timed out");
    // That connection is dropped by the caller; the next one starts afresh.
    const bridge = vi.fn<Call>(async () => ({}));
    hud.overlayUp(true);
    await hud.open(bridge);
    hud.connected();
    await settle();
    expect(asked(bridge)).toEqual([true]);
    expect(manager).not.toHaveBeenCalled();
  });
});

describe("one subscription change at a time (SCR-22)", () => {
  it("lets the latest wish win over those made while a call was on the way", async () => {
    vi.useFakeTimers();
    const slow = pending();
    const hud = new HudSubscription(slow.call);
    const bridge = vi.fn<Call>(async () => ({}));
    await hud.open(bridge);
    hud.connected();

    hud.overlayUp(true);
    await settle();
    expect(slow.call).toHaveBeenCalledOnce();
    // Up, down, up and down again before the daemon has answered.
    hud.overlayUp(false);
    hud.overlayUp(true);
    hud.overlayUp(false);
    await settle();
    expect(slow.call).toHaveBeenCalledOnce();

    slow.waiting[0].resolve();
    await settle();
    // One more call, for where the overlay ended up.
    expect(asked(slow.call)).toEqual([true, false]);
    slow.waiting[1].resolve();
    await settle();
    expect(slow.call).toHaveBeenCalledTimes(2);
  });

  it("makes no second call when the overlay is back where the daemon is", async () => {
    vi.useFakeTimers();
    const slow = pending();
    const hud = new HudSubscription(slow.call);
    await hud.open(vi.fn<Call>(async () => ({})));
    hud.connected();

    hud.overlayUp(true);
    await settle();
    hud.overlayUp(false);
    hud.overlayUp(true);
    slow.waiting[0].resolve();
    await settle();
    expect(slow.call).toHaveBeenCalledOnce();
  });

  it("does not let a call of a connection that has ended tell the next one what it did not", async () => {
    vi.useFakeTimers();
    const slow = pending();
    const hud = new HudSubscription(slow.call);
    await hud.open(vi.fn<Call>(async () => ({})));
    hud.connected();
    hud.overlayUp(true);
    await settle();
    expect(asked(slow.call)).toEqual([true]);

    // The connection ends with the call on the way; the overlay is given up
    // and the next connection subscribes without hud.
    hud.disconnected();
    hud.overlayUp(false);
    const next = vi.fn<Call>(async () => ({}));
    await hud.open(next);
    hud.connected();
    expect(asked(next)).toEqual([false]);
    // The old call comes back as if it had worked.
    slow.waiting[0].resolve();
    await settle();
    expect(slow.call).toHaveBeenCalledOnce();

    // It did not make the daemon "know" hud: the next news is still sent.
    hud.overlayUp(true);
    await settle();
    expect(asked(slow.call)).toEqual([true, true]);
  });
});

describe("a subscription that failed while the daemon is there (SCR-22)", () => {
  it("is tried again after a second, and then left alone", async () => {
    const { hud, manager, bridge } = fixture();
    manager.mockRejectedValueOnce(new Error("Daemon request timed out"));
    await hud.open(bridge);
    hud.connected();
    hud.overlayUp(true);
    await settle();
    expect(manager).toHaveBeenCalledOnce();

    await vi.advanceTimersByTimeAsync(hudRetryMs - 1);
    expect(manager).toHaveBeenCalledOnce();
    await vi.advanceTimersByTimeAsync(1);
    expect(asked(manager)).toEqual([true, true]);
    // It worked: no more tries.
    await vi.advanceTimersByTimeAsync(10 * hudRetryMs);
    expect(manager).toHaveBeenCalledTimes(2);
  });

  it("is tried every second for as long as it fails", async () => {
    const { hud, manager, bridge } = fixture();
    manager.mockRejectedValue(new Error("Too many pending daemon requests"));
    await hud.open(bridge);
    hud.connected();
    hud.overlayUp(true);
    await settle();
    await vi.advanceTimersByTimeAsync(3 * hudRetryMs);
    expect(manager).toHaveBeenCalledTimes(4);
    manager.mockResolvedValue({});
    await vi.advanceTimersByTimeAsync(hudRetryMs);
    expect(manager).toHaveBeenCalledTimes(5);
    await vi.advanceTimersByTimeAsync(5 * hudRetryMs);
    expect(manager).toHaveBeenCalledTimes(5);
  });

  it("is not tried again when the overlay no longer needs it", async () => {
    const { hud, manager, bridge } = fixture();
    manager.mockRejectedValueOnce(new Error("Daemon request timed out"));
    await hud.open(bridge);
    hud.connected();
    hud.overlayUp(true);
    await settle();
    // Given up again before the retry: the daemon is where the overlay is.
    hud.overlayUp(false);
    await vi.advanceTimersByTimeAsync(5 * hudRetryMs);
    expect(manager).toHaveBeenCalledOnce();
  });

  it("is asked for the newer news at once, and the old retry goes", async () => {
    const { hud, manager, bridge } = fixture();
    manager.mockRejectedValueOnce(new Error("Daemon request timed out"));
    await hud.open(bridge);
    hud.connected();
    hud.overlayUp(true);
    await settle();
    expect(manager).toHaveBeenCalledOnce();
    // The overlay is up and given up, then up again: still a change to make.
    hud.overlayUp(false);
    hud.overlayUp(true);
    await settle();
    expect(manager).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(5 * hudRetryMs);
    expect(manager).toHaveBeenCalledTimes(2);
  });

  it("is not tried again once the app is ending", async () => {
    const { hud, manager, bridge } = fixture();
    manager.mockRejectedValueOnce(new Error("Daemon request timed out"));
    await hud.open(bridge);
    hud.connected();
    hud.overlayUp(true);
    await settle();
    hud.stop();
    await vi.advanceTimersByTimeAsync(5 * hudRetryMs);
    hud.overlayUp(false);
    hud.overlayUp(true);
    await settle();
    expect(manager).toHaveBeenCalledOnce();
  });
});

describe("a subscription that failed for want of a connection (SCR-22)", () => {
  it.each([
    ["the manager's", managerDisconnected],
    ["the bridge's", bridgeDisconnected],
  ])(
    "is not tried again on %s disconnected error, and the next connection gets the overlay as it is",
    async (_who, message) => {
      const { hud, manager, bridge } = fixture();
      manager.mockRejectedValue(new Error(message));
      await hud.open(bridge);
      hud.connected();
      hud.overlayUp(true);
      await settle();
      await vi.advanceTimersByTimeAsync(10 * hudRetryMs);
      expect(manager).toHaveBeenCalledOnce();

      // The connection is reported lost, and the next one is made.
      hud.disconnected();
      const next = vi.fn<Call>(async () => ({}));
      await hud.open(next);
      hud.connected();
      await settle();
      expect(asked(next)).toEqual([true]);
      await vi.advanceTimersByTimeAsync(10 * hudRetryMs);
      expect(manager).toHaveBeenCalledOnce();
    },
  );

  it("is not tried again for any error that comes after the connection ended", async () => {
    vi.useFakeTimers();
    const slow = pending();
    const hud = new HudSubscription(slow.call);
    await hud.open(vi.fn<Call>(async () => ({})));
    hud.connected();
    hud.overlayUp(true);
    await settle();
    // The bridge ends its connection with a call on the way and rejects it
    // with what it ended with (an invalid frame, say), not the disconnected
    // message; the manager reports the end first.
    hud.disconnected();
    slow.waiting[0].reject(new Error("Daemon bridge sent an invalid frame"));
    await vi.advanceTimersByTimeAsync(10 * hudRetryMs);
    expect(slow.call).toHaveBeenCalledOnce();
  });

  it("is not tried when there is no connection to try on", async () => {
    const { hud, manager } = fixture();
    hud.overlayUp(true);
    hud.overlayUp(false);
    hud.overlayUp(true);
    await vi.advanceTimersByTimeAsync(10 * hudRetryMs);
    expect(manager).not.toHaveBeenCalled();
    // And a connection that went does not keep the state it had.
    const bridge = vi.fn<Call>(async () => ({}));
    await hud.open(bridge);
    hud.connected();
    hud.disconnected();
    hud.overlayUp(false);
    await vi.advanceTimersByTimeAsync(10 * hudRetryMs);
    expect(manager).not.toHaveBeenCalled();
  });
});

describe("an older daemon (SCR-22)", () => {
  it("that does not know hud gets a subscription without it, once", async () => {
    const { hud, manager, bridge } = fixture();
    hud.overlayUp(true);
    // A daemon from before SCR-22 refuses the list with hud, takes the other.
    bridge.mockRejectedValueOnce(refused());
    await hud.open(bridge);
    expect(asked(bridge)).toEqual([true, false]);
    hud.connected();
    await vi.advanceTimersByTimeAsync(10 * hudRetryMs);
    // Told as the overlay is: nothing to send, now or later.
    expect(manager).not.toHaveBeenCalled();
  });

  it("that learns of the overlay later also goes without hud, and is not tried again", async () => {
    const { hud, manager, bridge } = fixture();
    await hud.open(bridge);
    hud.connected();
    manager.mockRejectedValueOnce(refused());
    hud.overlayUp(true);
    await settle();
    expect(asked(manager)).toEqual([true, false]);
    await vi.advanceTimersByTimeAsync(10 * hudRetryMs);
    expect(manager).toHaveBeenCalledTimes(2);
  });

  it("that knows neither hud nor daemon.exiting is told without both", async () => {
    const { hud, manager, bridge } = fixture();
    await hud.open(bridge);
    hud.connected();
    manager
      .mockRejectedValueOnce(refused())
      .mockRejectedValueOnce(refused())
      .mockResolvedValue({});
    hud.overlayUp(true);
    await settle();
    expect(manager).toHaveBeenCalledTimes(3);
    expect(manager.mock.calls[2][1]).toEqual({
      topics: daemonTopics.filter(
        (topic) => topic !== "hud" && topic !== "daemon.exiting",
      ),
    });
    await vi.advanceTimersByTimeAsync(10 * hudRetryMs);
    expect(manager).toHaveBeenCalledTimes(3);
  });
});
