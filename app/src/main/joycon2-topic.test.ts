import { afterEach, describe, expect, it, vi } from "vitest";
import {
  daemonTopics,
  joyCon2Topic,
  managerDisconnected,
  rawInputTopic,
  subscribe,
} from "./bridge";
import { HudSubscription } from "./hud-subscription";

afterEach(() => {
  vi.useRealTimers();
});

type Call = (method: string, params: unknown) => Promise<unknown>;

/** The topics the n-th subscribe call asked for. */
function topics(call: { mock: { calls: unknown[][] } }, n: number): string[] {
  const [method, params] = call.mock.calls[n] as [string, { topics: string[] }];
  expect(method).toBe("subscribe");
  return params.topics;
}

const settle = () => vi.advanceTimersByTimeAsync(0);

function fixture() {
  vi.useFakeTimers();
  const manager = vi.fn<Call>(async () => ({}));
  const bridge = vi.fn<Call>(async () => ({}));
  return { hud: new HudSubscription(manager), manager, bridge };
}

describe("subscribe with the Joy-Con 2 topic", () => {
  it("adds joycon2.changed after input.raw when the Joy-Con 2 section is up, and leaves it out otherwise", async () => {
    const plain = vi.fn(async () => ({}));
    await subscribe(plain);
    expect(topics(plain, 0)).not.toContain(joyCon2Topic);

    const joy = vi.fn(async () => ({}));
    await subscribe(joy, { joycon2: true });
    expect(topics(joy, 0)).toEqual([...daemonTopics, joyCon2Topic]);

    const both = vi.fn(async () => ({}));
    await subscribe(both, { hud: false, raw: true, joycon2: true });
    expect(topics(both, 0)).toEqual([
      ...daemonTopics.filter((topic) => topic !== "hud"),
      rawInputTopic,
      joyCon2Topic,
    ]);
  });

  it("keeps it in the older lists a daemon without hud or daemon.exiting is asked", async () => {
    const refused = () =>
      Object.assign(new Error("unknown topic"), { code: -32602 });
    const call = vi
      .fn()
      .mockRejectedValueOnce(refused())
      .mockRejectedValueOnce(refused())
      .mockResolvedValue({});
    await subscribe(call, { joycon2: true });
    expect(topics(call, 2)).toEqual([
      ...daemonTopics.filter(
        (topic) => topic !== "hud" && topic !== "daemon.exiting",
      ),
      joyCon2Topic,
    ]);
  });
});

describe("joyCon2Up: the Joy-Con 2 section asks for its events", () => {
  it("subscribes with joycon2.changed and resolves once the daemon was told, and again without it", async () => {
    const { hud, manager, bridge } = fixture();
    await hud.open(bridge);
    hud.connected();
    await settle();
    expect(manager).not.toHaveBeenCalled();

    await hud.joyCon2Up(true);
    expect(manager).toHaveBeenCalledOnce();
    expect(topics(manager, 0)).toContain(joyCon2Topic);
    expect(topics(manager, 0)).not.toContain(rawInputTopic);

    // Asking again for what the daemon was told is no news.
    await hud.joyCon2Up(true);
    expect(manager).toHaveBeenCalledOnce();

    await hud.joyCon2Up(false);
    expect(manager).toHaveBeenCalledTimes(2);
    expect(topics(manager, 1)).not.toContain(joyCon2Topic);
  });

  it("is independent of the wizard's topic and of the overlay's", async () => {
    const { hud, manager, bridge } = fixture();
    hud.overlayUp(true);
    await hud.open(bridge);
    hud.connected();
    await hud.joyCon2Up(true);
    await hud.rawUp(true);
    expect(topics(manager, 1)).toEqual([
      ...daemonTopics,
      rawInputTopic,
      joyCon2Topic,
    ]);
    await hud.rawUp(false);
    expect(topics(manager, 2)).toEqual([...daemonTopics, joyCon2Topic]);
    hud.overlayUp(false);
    await settle();
    expect(topics(manager, 3)).toEqual([
      ...daemonTopics.filter((topic) => topic !== "hud"),
      joyCon2Topic,
    ]);
    await hud.joyCon2Up(false);
    expect(topics(manager, 4)).toEqual(
      daemonTopics.filter((topic) => topic !== "hud"),
    );
  });

  it("is refused while there is no connection, and lowering it then is nothing to tell", async () => {
    const { hud, manager } = fixture();
    await expect(hud.joyCon2Up(true)).rejects.toThrow(managerDisconnected);
    await expect(hud.joyCon2Up(false)).resolves.toBeUndefined();
    expect(manager).not.toHaveBeenCalled();
  });

  it("is not on the list of the next connection", async () => {
    const { hud, bridge } = fixture();
    await hud.open(bridge);
    hud.connected();
    await hud.joyCon2Up(true);
    hud.disconnected();
    const next = vi.fn<Call>(async () => ({}));
    await hud.open(next);
    expect(topics(next, 0)).not.toContain(joyCon2Topic);
  });
});
