import { afterEach, describe, expect, it, vi } from "vitest";
import {
  bridgeDisconnected,
  daemonTopics,
  managerDisconnected,
  rawInputTopic,
  subscribe,
} from "./bridge";
import { HudSubscription, hudRetryMs } from "./hud-subscription";

afterEach(() => {
  vi.useRealTimers();
});

type Call = (method: string, params: unknown) => Promise<unknown>;

const refused = () =>
  Object.assign(new Error("unknown topic"), { code: -32602 });

/** The topics the n-th subscribe call asked for. */
function topics(call: { mock: { calls: unknown[][] } }, n: number): string[] {
  const [method, params] = call.mock.calls[n] as [string, { topics: string[] }];
  expect(method).toBe("subscribe");
  return params.topics;
}

const settle = () => vi.advanceTimersByTimeAsync(0);

describe("subscribe with the raw topic (IN-17, ADR-0072)", () => {
  it("adds input.raw last when the wizard is open, and leaves it out otherwise", async () => {
    const plain = vi.fn(async () => ({}));
    await subscribe(plain);
    expect(topics(plain, 0)).not.toContain(rawInputTopic);

    const raw = vi.fn(async () => ({}));
    await subscribe(raw, { raw: true });
    expect(topics(raw, 0)).toEqual([...daemonTopics, rawInputTopic]);

    const noHud = vi.fn(async () => ({}));
    await subscribe(noHud, { hud: false, raw: true });
    expect(topics(noHud, 0)).toEqual([
      ...daemonTopics.filter((topic) => topic !== "hud"),
      rawInputTopic,
    ]);
  });

  it("keeps input.raw in the older lists a daemon without hud or daemon.exiting is asked", async () => {
    const call = vi
      .fn()
      .mockRejectedValueOnce(refused())
      .mockRejectedValueOnce(refused())
      .mockResolvedValue({});
    await subscribe(call, { raw: true });
    expect(topics(call, 2)).toEqual([
      ...daemonTopics.filter(
        (topic) => topic !== "hud" && topic !== "daemon.exiting",
      ),
      rawInputTopic,
    ]);
  });
});

function fixture() {
  vi.useFakeTimers();
  const manager = vi.fn<Call>(async () => ({}));
  const bridge = vi.fn<Call>(async () => ({}));
  const hud = new HudSubscription(manager);
  return { hud, manager, bridge };
}

describe("rawUp: the wizard asks for the raw events (IN-17)", () => {
  it("subscribes with input.raw and resolves once the daemon was told, and again without it", async () => {
    const { hud, manager, bridge } = fixture();
    await hud.open(bridge);
    hud.connected();
    await settle();
    expect(manager).not.toHaveBeenCalled();

    await hud.rawUp(true);
    expect(manager).toHaveBeenCalledOnce();
    expect(topics(manager, 0)).toContain(rawInputTopic);
    // Hud is as the overlay is: not up.
    expect(topics(manager, 0)).not.toContain("hud");

    // Asking again for what the daemon was told is no news.
    await hud.rawUp(true);
    expect(manager).toHaveBeenCalledOnce();

    await hud.rawUp(false);
    expect(manager).toHaveBeenCalledTimes(2);
    expect(topics(manager, 1)).not.toContain(rawInputTopic);
  });

  it("keeps hud as the overlay is, in both directions", async () => {
    const { hud, manager, bridge } = fixture();
    hud.overlayUp(true);
    await hud.open(bridge);
    hud.connected();
    await hud.rawUp(true);
    expect(topics(manager, 0)).toEqual([...daemonTopics, rawInputTopic]);
    hud.overlayUp(false);
    await settle();
    // Raw stays; hud goes.
    expect(topics(manager, 1)).toEqual([
      ...daemonTopics.filter((topic) => topic !== "hud"),
      rawInputTopic,
    ]);
    await hud.rawUp(false);
    expect(topics(manager, 2)).toEqual(
      daemonTopics.filter((topic) => topic !== "hud"),
    );
  });

  it("is refused while there is no connection, and lowering it then is nothing to tell", async () => {
    const { hud, manager } = fixture();
    await expect(hud.rawUp(true)).rejects.toThrow(managerDisconnected);
    await expect(hud.rawUp(false)).resolves.toBeUndefined();
    expect(manager).not.toHaveBeenCalled();
  });

  it("rejects when the daemon does not take it, and lowering it afterwards is not tried again", async () => {
    const { hud, manager, bridge } = fixture();
    await hud.open(bridge);
    hud.connected();
    manager.mockRejectedValueOnce(new Error("Daemon request timed out"));
    await expect(hud.rawUp(true)).rejects.toThrow("timed out");
    // The wizard gives up: the wish goes, and no retry for it is left.
    await hud.rawUp(false);
    await vi.advanceTimersByTimeAsync(10 * hudRetryMs);
    expect(manager).toHaveBeenCalledOnce();
  });

  it("rejects the waiting call when the connection ends first", async () => {
    vi.useFakeTimers();
    const waiting: Array<{ reject: (error: Error) => void }> = [];
    const slow = vi.fn<Call>(
      () =>
        new Promise((_resolve, reject) => {
          waiting.push({ reject });
        }),
    );
    const hud = new HudSubscription(slow);
    await hud.open(vi.fn<Call>(async () => ({})));
    hud.connected();
    const asked = hud.rawUp(true);
    const outcome = asked.catch((error: unknown) => error);
    await settle();
    hud.disconnected();
    await expect(outcome).resolves.toMatchObject({
      message: managerDisconnected,
    });
    waiting[0].reject(new Error(bridgeDisconnected));
    await settle();
  });

  it("goes without the raw topic again when the connection is made anew", async () => {
    const { hud, manager, bridge } = fixture();
    await hud.open(bridge);
    hud.connected();
    await hud.rawUp(true);
    // The connection is lost with the wizard open; its watch ended with it.
    hud.disconnected();
    const next = vi.fn<Call>(async () => ({}));
    await hud.open(next);
    hud.connected();
    await settle();
    expect(topics(next, 0)).not.toContain(rawInputTopic);
    expect(manager).toHaveBeenCalledOnce();
  });
});
