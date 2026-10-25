import { EventEmitter } from "node:events";
import { PassThrough } from "node:stream";
import { describe, expect, it, vi } from "vitest";
import {
  BridgeClient,
  type BridgeProcess,
  bridgeDisconnected,
  daemonTopics,
  isDisconnectedError,
  managerDisconnected,
  subscribe,
} from "./bridge";

function fixture(exitGrace?: number) {
  const child = Object.assign(new EventEmitter(), {
    stdin: new PassThrough(),
    stdout: new PassThrough(),
    stderr: new PassThrough(),
    kill: vi.fn(),
  });
  const events: unknown[] = [];
  const client = new BridgeClient(
    child as unknown as BridgeProcess,
    (event) => events.push(event),
    100,
    exitGrace,
  );
  return { child, client, events };
}

describe("authenticated CLI bridge", () => {
  it("waits for authenticated ready and matches out-of-order requests", async () => {
    const { child, client } = fixture();
    child.stdout.write('{"ready":true,"pid":42,"protocol":1}\n');
    expect(await client.ready).toEqual({ pid: 42, protocol: 1 });
    const first = client.call("status.get");
    const second = client.call("devices.list");
    await Promise.resolve();
    child.stdout.write(
      '{"id":2,"result":[]}\n{"id":1,"result":{"dryRun":true}}\n',
    );
    expect(await first).toEqual({ dryRun: true });
    expect(await second).toEqual([]);
    client.close();
  });

  it("preserves notifications across arbitrary chunks", async () => {
    const { child, client, events } = fixture();
    child.stdout.write('{"ready":true,"pid":42,"protocol":1}\n{"topic":"inp');
    child.stdout.write('ut","data":{"kind":"down","control":"south"}}\n');
    await client.ready;
    expect(events).toEqual([
      { topic: "input", data: { kind: "down", control: "south" } },
    ]);
    client.close();
  });
  it("delivers foreground apps for the window capture helper", async () => {
    const { child, client, events } = fixture();
    child.stdout.write('{"ready":true,"pid":42,"protocol":1}\n');
    await client.ready;
    const data = {
      exe: "krita.exe",
      path: "C:\\Apps\\krita.exe",
      elevated: false,
      own: false,
    };
    child.stdout.write(
      `${JSON.stringify({ topic: "app.foreground", data })}\n`,
    );
    await new Promise((resolve) => setImmediate(resolve));
    expect(events).toEqual([{ topic: "app.foreground", data }]);
    client.close();
  });

  it("delivers a script's toast, the hud topic (SCR-22)", async () => {
    const { child, client, events } = fixture();
    child.stdout.write('{"ready":true,"pid":42,"protocol":1}\n');
    await client.ready;
    const data = { kind: "toast", slot: 0, text: "脚本提示", ms: 1500 };
    child.stdout.write(`${JSON.stringify({ topic: "hud", data })}\n`);
    await new Promise((resolve) => setImmediate(resolve));
    expect(events).toEqual([{ topic: "hud", data }]);
    client.close();
  });
  it("preserves profile validation and revision conflict data", async () => {
    const { child, client } = fixture();
    child.stdout.write('{"ready":true,"pid":42,"protocol":1}\n');
    await client.ready;
    const put = client.call("profiles.put");
    await Promise.resolve();
    child.stdout.write(
      '{"id":1,"error":{"code":-32009,"message":"revision conflict","data":{"revision":"abc"}}}\n',
    );
    await expect(put).rejects.toMatchObject({
      message: "revision conflict",
      code: -32009,
      data: { revision: "abc" },
    });
    client.close();
  });

  it("rejects requests and disconnects when a bridge dies", async () => {
    const { child, client } = fixture();
    child.stdout.write('{"ready":true,"pid":42,"protocol":1}\n');
    await client.ready;
    const pending = client.call("status.get");
    child.emit("exit", 1);
    child.emit("close", 1);
    await expect(pending).rejects.toThrow("disconnected");
    await expect(client.call("status.get")).rejects.toThrow("disconnected");
  });

  it("reads what the bridge wrote before it exited (DMN-22)", async () => {
    const { child, client, events } = fixture();
    const closed = vi.fn();
    client.onClose(closed);
    child.stdout.write('{"ready":true,"pid":42,"protocol":1}\n');
    await client.ready;
    // Node may report the exit before the last of the output.
    child.emit("exit", 1);
    child.stdout.end('{"topic":"daemon.exiting","data":{"reason":"tray"}}\n');
    await new Promise((done) => setImmediate(done));
    expect(closed).not.toHaveBeenCalled();
    child.emit("close", 1);
    expect(events).toEqual([
      { topic: "daemon.exiting", data: { reason: "tray" } },
    ]);
    expect(closed).toHaveBeenCalledOnce();
  });

  it("disconnects soon after the exit when the output stays open", async () => {
    const { child, client } = fixture(10);
    child.stdout.write('{"ready":true,"pid":42,"protocol":1}\n');
    await client.ready;
    const pending = client.call("status.get");
    child.emit("exit", 1);
    await expect(pending).rejects.toThrow("disconnected");
  });

  it("bounds ready time and refuses malformed protocol frames", async () => {
    const timeout = fixture();
    await expect(timeout.client.ready).rejects.toThrow("timed out");
    expect(timeout.child.kill).toHaveBeenCalledOnce();
    const malformed = fixture();
    malformed.child.stdout.write('{"ready":true,"pid":0,"protocol":1}\n');
    await expect(malformed.client.ready).rejects.toThrow("invalid");
  });
});

describe("subscribe", () => {
  const refused = () =>
    Object.assign(new Error("too many topics"), { code: -32602 });
  const withoutHud = daemonTopics.filter((topic) => topic !== "hud");
  const withoutBoth = withoutHud.filter((topic) => topic !== "daemon.exiting");

  it("subscribes to every topic, hud last", () => {
    expect(daemonTopics.at(-1)).toBe("hud");
    expect(daemonTopics).toContain("daemon.exiting");
  });

  it("asks for every topic in one call", async () => {
    const call = vi.fn(async () => ({}));
    await subscribe(call);
    expect(call).toHaveBeenCalledExactlyOnceWith("subscribe", {
      topics: [...daemonTopics],
    });
  });

  it("goes without hud when the daemon does not know it (SCR-22)", async () => {
    const call = vi.fn().mockRejectedValueOnce(refused()).mockResolvedValue({});
    await subscribe(call);
    expect(call).toHaveBeenCalledTimes(2);
    expect(call).toHaveBeenLastCalledWith("subscribe", { topics: withoutHud });
  });

  it("goes without hud and daemon.exiting when the daemon knows neither (DMN-22)", async () => {
    const call = vi
      .fn()
      .mockRejectedValueOnce(refused())
      .mockRejectedValueOnce(refused())
      .mockResolvedValue({});
    await subscribe(call);
    expect(call).toHaveBeenCalledTimes(3);
    expect(call).toHaveBeenLastCalledWith("subscribe", {
      topics: withoutBoth,
    });
  });

  it("leaves hud out from the start when the overlay is not up, with the same fallback", async () => {
    const call = vi.fn(async () => ({}));
    await subscribe(call, { hud: false });
    expect(call).toHaveBeenCalledExactlyOnceWith("subscribe", {
      topics: withoutHud,
    });
    const old = vi.fn().mockRejectedValueOnce(refused()).mockResolvedValue({});
    await subscribe(old, { hud: false });
    expect(old).toHaveBeenCalledTimes(2);
    expect(old).toHaveBeenLastCalledWith("subscribe", { topics: withoutBoth });
    // hud: true is the default.
    const again = vi.fn(async () => ({}));
    await subscribe(again, { hud: true });
    expect(again).toHaveBeenCalledWith("subscribe", {
      topics: [...daemonTopics],
    });
  });

  it("passes other errors on at once", async () => {
    const call = vi
      .fn()
      .mockRejectedValue(new Error("Daemon request timed out"));
    await expect(subscribe(call)).rejects.toThrow("timed out");
    expect(call).toHaveBeenCalledOnce();
    const later = vi
      .fn()
      .mockRejectedValueOnce(refused())
      .mockRejectedValue(new Error("Daemon request timed out"));
    await expect(subscribe(later)).rejects.toThrow("timed out");
    expect(later).toHaveBeenCalledTimes(2);
  });

  it("gives up with the daemon's own refusal when even the last list is refused", async () => {
    const again = vi.fn().mockRejectedValue(refused());
    await expect(subscribe(again)).rejects.toThrow("too many topics");
    expect(again).toHaveBeenCalledTimes(3);
    const quiet = vi.fn().mockRejectedValue(refused());
    await expect(subscribe(quiet, { hud: false })).rejects.toThrow(
      "too many topics",
    );
    expect(quiet).toHaveBeenCalledTimes(2);
  });
});

describe("isDisconnectedError (SCR-22)", () => {
  it("knows what a call to an ended bridge says", async () => {
    const { child, client } = fixture();
    child.stdout.write('{"ready":true,"pid":42,"protocol":1}\n');
    await client.ready;
    const pending = client.call("status.get");
    client.close();
    const rejected = await pending.catch((error: unknown) => error);
    expect(rejected).toEqual(new Error(bridgeDisconnected));
    expect(isDisconnectedError(rejected)).toBe(true);
    const later = await client
      .call("status.get")
      .catch((error: unknown) => error);
    expect(isDisconnectedError(later)).toBe(true);
  });

  it("knows both messages, and nothing a daemon that is there says", () => {
    expect(isDisconnectedError(new Error(bridgeDisconnected))).toBe(true);
    expect(isDisconnectedError(new Error(managerDisconnected))).toBe(true);
    for (const error of [
      new Error("Daemon request timed out"),
      new Error("Too many pending daemon requests"),
      new Error("Daemon bridge sent an invalid frame"),
      Object.assign(new Error("unknown topic"), { code: -32602 }),
      managerDisconnected,
      undefined,
      null,
    ])
      expect(isDisconnectedError(error)).toBe(false);
  });
});
