import { readFileSync } from "node:fs";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PadCapture } from "./pad-capture";

/**
 * A PadCapture whose capture.start waits for finish or fail, and whose
 * capture.stop takes the next of stopResults ("ok" when they run out):
 * "ok" resolves, "reject" rejects at once, "unsent" returns undefined.
 */
function setup(
  focused = true,
  stopResults: ("ok" | "reject" | "unsent")[] = [],
) {
  const state = { focused };
  const calls: string[] = [];
  // The pending capture.start requests, in the order sent.
  const starts: { finish: () => void; fail: (error: Error) => void }[] = [];
  const call = vi.fn((method: string) => {
    calls.push(method);
    if (method === "capture.start")
      return new Promise<unknown>((resolve, reject) => {
        starts.push({
          finish: () => resolve({ capturing: true }),
          fail: reject,
        });
      });
    switch (stopResults.shift() ?? "ok") {
      case "reject":
        return Promise.reject(new Error("Too many pending daemon requests"));
      case "unsent":
        return undefined;
      default:
        return Promise.resolve({ capturing: false });
    }
  });
  const capture = new PadCapture({
    call,
    focused: () => state.focused,
    retryMs: 100,
  });
  return {
    state,
    calls,
    capture,
    starts,
    finish: () => starts.at(-1)?.finish(),
    fail: (error: Error) => starts.at(-1)?.fail(error),
  };
}

/** Lets the pending promise callbacks run. */
const settle = () => new Promise<void>((resolve) => setTimeout(resolve, 0));

afterEach(() => {
  vi.useRealTimers();
});

describe("controller capture for the emergency combo", () => {
  it("starts only in the focused window and stops once", async () => {
    const t = setup(false);
    await expect(t.capture.start()).rejects.toThrow("focused");
    expect(t.calls).toEqual([]);
    t.state.focused = true;
    const started = t.capture.start();
    t.finish();
    await started;
    expect(t.capture.capturing).toBe(true);
    t.capture.stop();
    t.capture.stop();
    await settle();
    t.capture.stop();
    expect(t.calls).toEqual(["capture.start", "capture.stop"]);
    expect(t.capture.capturing).toBe(false);
  });

  it("stops a capture that does not ask to stay when the window loses the front", async () => {
    const t = setup();
    const started = t.capture.start();
    t.finish();
    await started;
    t.capture.windowAway();
    await settle();
    expect(t.capture.capturing).toBe(false);
    expect(t.calls).toEqual(["capture.start", "capture.stop"]);
  });

  it("stops a capture that loses focus while it starts", async () => {
    const t = setup();
    const started = t.capture.start();
    // Main's blur handler runs before the daemon answers.
    t.capture.stop();
    t.finish();
    await expect(started).rejects.toThrow("stopped");
    expect(t.calls).toEqual(["capture.start", "capture.stop"]);
    const again = t.capture.start();
    t.state.focused = false;
    t.finish();
    await expect(again).rejects.toThrow("focused");
    expect(t.calls).toEqual([
      "capture.start",
      "capture.stop",
      "capture.start",
      "capture.stop",
    ]);
  });

  it("stops a start that failed or timed out, which may still reach the daemon", async () => {
    const t = setup();
    const started = t.capture.start();
    t.fail(new Error("Daemon request timed out"));
    await expect(started).rejects.toThrow("timed out");
    expect(t.capture.capturing).toBe(false);
    expect(t.calls).toEqual(["capture.start", "capture.stop"]);
    // Confirmed: later stops send nothing.
    await settle();
    t.capture.stop();
    expect(t.calls).toEqual(["capture.start", "capture.stop"]);
  });

  it("leaves a failed start to a later one", async () => {
    const t = setup();
    const first = t.capture.start();
    // The window has focus again and starts once more.
    const second = t.capture.start();
    t.starts[0]?.fail(new Error("Daemon request timed out"));
    await expect(first).rejects.toThrow("timed out");
    t.finish();
    await second;
    expect(t.capture.capturing).toBe(true);
    expect(t.calls).toEqual(["capture.start", "capture.start"]);
  });

  it("sends a failed stop again until the daemon confirms it", async () => {
    vi.useFakeTimers();
    const t = setup(true, ["reject", "unsent", "ok"]);
    const started = t.capture.start();
    t.finish();
    await started;
    const stops = () =>
      t.calls.filter((call) => call === "capture.stop").length;
    t.capture.stop();
    await vi.advanceTimersByTimeAsync(0);
    expect(stops()).toBe(1);
    await vi.advanceTimersByTimeAsync(100);
    expect(stops()).toBe(2);
    await vi.advanceTimersByTimeAsync(100);
    expect(stops()).toBe(3);
    // Confirmed: no more retries, and later stops send nothing.
    await vi.advanceTimersByTimeAsync(1000);
    t.capture.stop();
    expect(stops()).toBe(3);
    expect(t.capture.capturing).toBe(false);
  });

  it("drops a retry when a start or the end of the connection comes", async () => {
    vi.useFakeTimers();
    const t = setup(true, ["reject", "reject"]);
    const started = t.capture.start();
    t.finish();
    await started;
    t.capture.stop();
    await vi.advanceTimersByTimeAsync(0);
    // The window has focus again: the new capture stays on.
    const again = t.capture.start();
    t.finish();
    await again;
    await vi.advanceTimersByTimeAsync(1000);
    expect(t.calls).toEqual(["capture.start", "capture.stop", "capture.start"]);
    t.capture.stop();
    await vi.advanceTimersByTimeAsync(0);
    t.capture.disconnected();
    await vi.advanceTimersByTimeAsync(1000);
    expect(t.calls).toEqual([
      "capture.start",
      "capture.stop",
      "capture.start",
      "capture.stop",
    ]);
  });

  it("forgets a capture the daemon ended with the connection", async () => {
    const t = setup();
    const started = t.capture.start();
    t.finish();
    await started;
    t.capture.disconnected();
    t.capture.stop();
    expect(t.calls).toEqual(["capture.start"]);
    const failed = t.capture.start();
    t.fail(new Error("device busy"));
    await expect(failed).rejects.toThrow("device busy");
    expect(t.capture.capturing).toBe(false);
    expect(t.calls).toEqual(["capture.start", "capture.start", "capture.stop"]);
  });

  it("rejects without a daemon", async () => {
    const capture = new PadCapture({
      call: () => undefined,
      focused: () => true,
    });
    await expect(capture.start()).rejects.toThrow("not connected");
    expect(capture.capturing).toBe(false);
  });

  it("is stopped by every way the window or session goes away", () => {
    // index.ts needs Electron to run, so this reads it.
    const main = readFileSync(new URL("./index.ts", import.meta.url), "utf8");
    expect(main).toMatch(
      /padCaptureStart: \{\s*count: 1,\s*run: \(stays\) => padCapture\.start\(stays === true\),\s*\}/,
    );
    // Only the wizard asks to stay; the emergency combo's dialog does not.
    const source = (path: string) =>
      readFileSync(new URL(path, import.meta.url), "utf8");
    expect(source("../preload/index.ts")).toMatch(
      /padCaptureStart: \(stays\) =>\s*invoke\("padmate:padCaptureStart", stays === true\)/,
    );
    expect(source("../shared/api.ts")).toContain(
      "padCaptureStart(stays?: boolean): Promise<void>;",
    );
    expect(source("../renderer/pages/MappingWizardDialog.tsx")).not.toMatch(
      /padCaptureStart\(\)/,
    );
    expect(source("../renderer/pages/PanicComboDialog.tsx")).toContain(
      "api.padCaptureStart()",
    );
    expect(main).toContain(
      "padCaptureStop: { count: 0, run: () => padCapture.stop() }",
    );
    // A blur, a lock and a sleep end a capture only if it did not ask to
    // stay (the wizard's: the daemon pauses the mapping itself while the
    // session is locked, and the capture still holds it after the unlock).
    for (const event of [
      'window.on("blur"',
      'powerMonitor.on("suspend"',
      'powerMonitor.on("lock-screen"',
    ]) {
      const start = main.indexOf(event);
      expect(start, event).toBeGreaterThan(0);
      const body = main.slice(start, main.indexOf("});", start));
      expect(body, event).toContain("padCapture.windowAway()");
      expect(body, event).not.toContain("padCapture.stop()");
    }
    for (const event of [
      'window.on("close"',
      '"did-start-loading"',
      '"render-process-gone"',
      'app.on("before-quit"',
    ]) {
      const start = main.indexOf(event);
      expect(start, event).toBeGreaterThan(0);
      const body = main.slice(start, main.indexOf("});", start));
      expect(body, event).toContain("padCapture.stop()");
    }
    const disconnected = main.indexOf('capture.stop("disconnected")');
    expect(main.slice(disconnected, disconnected + 120)).toContain(
      "padCapture.disconnected()",
    );
  });
});

describe("a capture that stays while the window is away (the mapping wizard, ADR-0076)", () => {
  it("is not ended by the window losing the front, only by a stop", async () => {
    const t = setup();
    const started = t.capture.start(true);
    t.finish();
    await started;
    t.capture.windowAway();
    await settle();
    expect(t.capture.capturing).toBe(true);
    expect(t.calls).toEqual(["capture.start"]);
    // The wizard closes, whether or not the window is in front.
    t.state.focused = false;
    t.capture.stop();
    await settle();
    expect(t.capture.capturing).toBe(false);
    expect(t.calls).toEqual(["capture.start", "capture.stop"]);
  });

  it("is not ended by a blur that comes while it starts", async () => {
    const t = setup();
    const started = t.capture.start(true);
    // Main's blur handler runs before the daemon answers, and the window
    // is not in front when it does.
    t.capture.windowAway();
    t.state.focused = false;
    t.finish();
    await started;
    expect(t.capture.capturing).toBe(true);
    expect(t.calls).toEqual(["capture.start"]);
  });

  it("is still asked for from the front window only, and stays on when a later ask is refused", async () => {
    const t = setup();
    const first = t.capture.start(true);
    t.finish();
    await first;
    t.state.focused = false;
    await expect(t.capture.start(true)).rejects.toThrow("focused");
    expect(t.capture.capturing).toBe(true);
    expect(t.calls).toEqual(["capture.start"]);
    // Back in front, it is asked for once more; the daemon takes that as one.
    t.state.focused = true;
    const again = t.capture.start(true);
    t.finish();
    await again;
    t.capture.windowAway();
    expect(t.capture.capturing).toBe(true);
    expect(t.calls).toEqual(["capture.start", "capture.start"]);
  });

  it("is kept by a lock or a sleep, which Main reports as the window being away", async () => {
    const t = setup();
    const started = t.capture.start(true);
    t.finish();
    await started;
    // The window is in the background when the session locks and unlocks:
    // nothing asks for the capture again, and it still holds the mapping.
    t.state.focused = false;
    t.capture.windowAway();
    t.capture.windowAway();
    await settle();
    expect(t.capture.capturing).toBe(true);
    expect(t.calls).toEqual(["capture.start"]);
  });

  it("is ended by everything else that ends a capture", async () => {
    vi.useFakeTimers();
    const t = setup(true, ["reject", "ok"]);
    const first = t.capture.start(true);
    t.finish();
    await first;
    // Close, reload and a crashed page all call stop(): it is sent again
    // until the daemon confirms it, however the window stands.
    t.capture.stop();
    await vi.advanceTimersByTimeAsync(0);
    expect(t.capture.capturing).toBe(false);
    await vi.advanceTimersByTimeAsync(100);
    expect(t.calls).toEqual(["capture.start", "capture.stop", "capture.stop"]);
    // Asked for again when the window is back, and it stays once more.
    const second = t.capture.start(true);
    t.finish();
    await second;
    t.capture.windowAway();
    expect(t.capture.capturing).toBe(true);
    // The connection ends and the daemon ends the capture with it.
    t.capture.disconnected();
    expect(t.capture.capturing).toBe(false);
    t.capture.stop();
    expect(t.calls).toEqual([
      "capture.start",
      "capture.stop",
      "capture.stop",
      "capture.start",
    ]);
  });

  it("is an ordinary capture again once a start does not ask to stay", async () => {
    const t = setup();
    const first = t.capture.start(true);
    t.finish();
    await first;
    const second = t.capture.start();
    t.finish();
    await second;
    t.capture.windowAway();
    await settle();
    expect(t.capture.capturing).toBe(false);
    expect(t.calls).toEqual(["capture.start", "capture.start", "capture.stop"]);
  });
});
