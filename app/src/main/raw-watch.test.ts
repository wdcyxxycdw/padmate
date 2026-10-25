import { describe, expect, it, vi } from "vitest";
import type { DaemonCall } from "./library-handlers";
import { RawWatch } from "./raw-watch";

const info = (over: Record<string, unknown> = {}) => ({
  device: "030000007e05/p:x#1",
  id: 7,
  guid: "ff00aabb000000000000000000007600",
  gamepad: false,
  axes: 6,
  buttons: 14,
  hats: 1,
  axisRest: [0, 0, 0, 0, -32768, -32768],
  watching: true,
  ...over,
});

function fixture(
  reply: (method: string, params: unknown) => unknown = () => info(),
) {
  const log: string[] = [];
  const call = vi.fn<DaemonCall>(async (method, params) => {
    log.push(`${method} ${JSON.stringify(params)}`);
    return reply(method, params);
  });
  const up = vi.fn(async (on: boolean) => {
    log.push(`topic ${on ? "up" : "down"}`);
  });
  return { call, up, log, watch: new RawWatch(call, { up }) };
}

describe("RawWatch.start (IN-17, IN-19)", () => {
  it("subscribes to input.raw first, then asks the daemon for the controller's raw events", async () => {
    const { watch, log } = fixture();
    await expect(watch.start("7")).resolves.toEqual({
      device: "030000007e05/p:x#1",
      id: 7,
      guid: "ff00aabb000000000000000000007600",
      gamepad: false,
      axes: 6,
      buttons: 14,
      hats: 1,
      axisRest: [0, 0, 0, 0, -32768, -32768],
      watching: true,
    });
    expect(log).toEqual([
      "topic up",
      'devices.rawInfo {"device":"7","watch":true}',
    ]);
  });

  it("does not ask the daemon when the topic cannot be subscribed to", async () => {
    const { watch, call, up } = fixture();
    up.mockRejectedValueOnce(new Error("Daemon is disconnected"));
    await expect(watch.start("7")).rejects.toThrow("Daemon is disconnected");
    expect(call).not.toHaveBeenCalled();
  });

  it("lets go of the topic again when the daemon refuses, and keeps only the code", async () => {
    const refused = Object.assign(
      new Error("the controller is a gamepad: call capture.start first"),
      { code: -32026 },
    );
    const { watch, log } = fixture(() => {
      throw refused;
    });
    await expect(watch.start("7")).rejects.toMatchObject({
      message: "Raw events not started",
      code: -32026,
    });
    expect(log).toEqual([
      "topic up",
      'devices.rawInfo {"device":"7","watch":true}',
      "topic down",
    ]);
  });

  it("refuses an answer that does not describe the controller", async () => {
    for (const reply of [
      null,
      [],
      {},
      info({ axisRest: "none" }),
      info({ axes: "6" }),
      info({ guid: 7 }),
      info({ gamepad: "no" }),
      info({ axisRest: [0, "x"] }),
    ]) {
      const { watch } = fixture(() => reply);
      await expect(watch.start("7"), JSON.stringify(reply)).rejects.toThrow(
        "Raw events not started",
      );
    }
  });

  it("is refused without a connection", async () => {
    const up = vi.fn(async () => {});
    const watch = new RawWatch(() => undefined, { up });
    await expect(watch.start("7")).rejects.toThrow("Daemon is not connected");
    expect(up).toHaveBeenLastCalledWith(false);
  });
});

describe("RawWatch.stop", () => {
  it("ends the watch, then the subscription", async () => {
    const { watch, log } = fixture();
    await watch.start("7");
    log.length = 0;
    await watch.stop("7");
    expect(log).toEqual([
      'devices.rawInfo {"device":"7","watch":false}',
      "topic down",
    ]);
  });

  it("keeps the subscription while another controller is watched", async () => {
    const { watch, log } = fixture();
    await watch.start("7");
    await watch.start("8");
    log.length = 0;
    await watch.stop("7");
    expect(log).toEqual(['devices.rawInfo {"device":"7","watch":false}']);
    await watch.stop("8");
    expect(log.at(-1)).toBe("topic down");
  });

  it("lets go of the subscription even when the daemon fails", async () => {
    let fail = false;
    const { watch, up } = fixture(() => {
      if (fail) throw Object.assign(new Error("x"), { code: -32004 });
      return info();
    });
    await watch.start("7");
    fail = true;
    await expect(watch.stop("7")).rejects.toMatchObject({ code: -32004 });
    expect(up).toHaveBeenLastCalledWith(false);
  });

  it("stops a controller that was not started without error", async () => {
    const { watch, log } = fixture();
    await watch.stop("9");
    expect(log).toEqual([
      'devices.rawInfo {"device":"9","watch":false}',
      "topic down",
    ]);
  });
});

describe("RawWatch keeps its calls in order", () => {
  it("does not let a stop overtake a start that is on its way", async () => {
    const log: string[] = [];
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const call = vi.fn<DaemonCall>(async (method, params) => {
      log.push(`${method} ${JSON.stringify(params)}`);
      return info();
    });
    const up = vi.fn(async (on: boolean) => {
      log.push(`topic ${on ? "up" : "down"}`);
      if (on) await gate;
    });
    const watch = new RawWatch(call, { up });
    const started = watch.start("7");
    const stopped = watch.stop("7");
    await new Promise((resolve) => setTimeout(resolve, 0));
    // The topic is on its way; nothing else has been asked.
    expect(log).toEqual(["topic up"]);
    release();
    await started;
    await stopped;
    expect(log).toEqual([
      "topic up",
      'devices.rawInfo {"device":"7","watch":true}',
      'devices.rawInfo {"device":"7","watch":false}',
      "topic down",
    ]);
  });

  it("ends what a start leaves when the page goes away while it is on its way", async () => {
    const log: string[] = [];
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const call = vi.fn<DaemonCall>(async (method, params) => {
      log.push(`${method} ${JSON.stringify(params)}`);
      return info();
    });
    const up = vi.fn(async (on: boolean) => {
      log.push(`topic ${on ? "up" : "down"}`);
      if (on) await gate;
    });
    const watch = new RawWatch(call, { up });
    const started = watch.start("7");
    watch.reset();
    release();
    await started;
    await vi.waitFor(() => expect(log.at(-1)).toBe("topic down"));
    expect(log).toEqual([
      "topic up",
      'devices.rawInfo {"device":"7","watch":true}',
      'devices.rawInfo {"device":"7","watch":false}',
      "topic down",
    ]);
  });
});

describe("RawWatch.reset (the page or the connection ended)", () => {
  it("ends every watch it knows of and the subscription, without waiting", async () => {
    const { watch, log, call } = fixture();
    await watch.start("7");
    await watch.start("8");
    log.length = 0;
    watch.reset();
    await vi.waitFor(() => expect(log).toContain("topic down"));
    expect(log.filter((line) => line.startsWith("devices.rawInfo"))).toEqual([
      'devices.rawInfo {"device":"7","watch":false}',
      'devices.rawInfo {"device":"8","watch":false}',
    ]);
    // Nothing is left to end.
    call.mockClear();
    watch.reset();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(call).not.toHaveBeenCalled();
  });

  it("does not throw when the daemon is gone", async () => {
    const { watch, call, up } = fixture();
    await watch.start("7");
    call.mockRejectedValue(new Error("Daemon bridge disconnected"));
    up.mockRejectedValue(new Error("Daemon is disconnected"));
    expect(() => watch.reset()).not.toThrow();
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
});
