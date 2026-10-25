import { EventEmitter } from "node:events";
import { PassThrough } from "node:stream";
import { describe, expect, it, vi } from "vitest";
import { NativeCapture } from "./native-capture";

function child() {
  return Object.assign(new EventEmitter(), {
    stdin: new PassThrough(),
    stdout: new PassThrough(),
    stderr: new PassThrough(),
    kill: vi.fn(),
  });
}
describe("native keyboard capture lifecycle", () => {
  it("reports a bounded native startup stage without private details", async () => {
    const process = child();
    const notify = vi.fn();
    const capture = new NativeCapture({
      spawn: () => process,
      focused: () => true,
      notify,
    });
    const start = capture.start();
    process.stdout.write(
      '{"active":false,"reason":"foreground-unavailable"}\n',
    );
    await expect(start).rejects.toThrow("foreground-unavailable");
    expect(notify).toHaveBeenLastCalledWith({
      active: false,
      session: 1,
      reason: "foreground-unavailable",
    });
  });
  it("only starts focused, resolves on installed hook, and discards stale events", async () => {
    const first = child(),
      second = child();
    let focused = false;
    const notify = vi.fn();
    const spawn = vi
      .fn()
      .mockReturnValueOnce(first)
      .mockReturnValueOnce(second);
    const capture = new NativeCapture({
      spawn,
      focused: () => focused,
      notify,
    });
    await expect(capture.start()).rejects.toThrow("focused");
    expect(spawn).not.toHaveBeenCalled();
    focused = true;
    const ready = capture.start();
    first.stdout.write('{"active":true}\n');
    await ready;
    first.stdout.write('{"active":true,"chord":"RCtrl+Z"}\n');
    expect(notify).toHaveBeenLastCalledWith({
      active: true,
      chord: "RCtrl+Z",
      session: 1,
    });
    capture.stop("blur");
    expect(first.kill).toHaveBeenCalledOnce();
    const next = capture.start();
    second.stdout.write('{"active":true}\n');
    await next;
    first.stdout.write('{"active":true,"chord":"A"}\n');
    expect(notify).toHaveBeenLastCalledWith({ active: true, session: 2 });
    capture.stopSession(1);
    expect(() => capture.stopSession(-1)).toThrow();
    first.emit("exit");
    expect(second.kill).not.toHaveBeenCalled();
    capture.stop();
  });
  it("cancels pending start, EOF, timeout and malformed frames", async () => {
    vi.useFakeTimers();
    try {
      const a = child();
      const notify = vi.fn();
      const capture = new NativeCapture({
        spawn: () => a,
        focused: () => true,
        notify,
      });
      const start = capture.start();
      const rejected = expect(start).rejects.toThrow();
      capture.stop("blur");
      await rejected;
      const b = child();
      const timed = new NativeCapture({
        spawn: () => b,
        focused: () => true,
        notify,
      });
      const wait = timed.start();
      const timeout = expect(wait).rejects.toThrow();
      await vi.advanceTimersByTimeAsync(2100);
      await timeout;
      expect(b.kill).toHaveBeenCalled();
      const c = child();
      const running = new NativeCapture({
        spawn: () => c,
        focused: () => true,
        notify,
      });
      const ready = running.start();
      c.stdout.write('{"active":true}\n');
      await ready;
      c.emit("exit");
      expect(notify).toHaveBeenLastCalledWith({
        active: false,
        reason: "ended",
        session: 1,
      });
      const d = child();
      const invalid = new NativeCapture({
        spawn: () => d,
        focused: () => true,
        notify,
      });
      const opening = invalid.start();
      const bad = expect(opening).rejects.toThrow();
      d.stdout.write("not json\n");
      await bad;
      expect(d.kill).toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });
});
