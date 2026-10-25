import { afterEach, describe, expect, it, vi } from "vitest";
import { CLOSE_WAIT_MS, CloseGuard, EXIT_WAIT_MS } from "./close-guard";

afterEach(() => {
  vi.useRealTimers();
});

function guard(canAsk = true) {
  const ask = vi.fn(() => canAsk);
  const close = vi.fn();
  return { ask, close, guard: new CloseGuard({ ask, close }) };
}

describe("close guard", () => {
  it("holds the first close until the renderer is done", () => {
    const { ask, close, guard: closing } = guard();
    expect(closing.hold()).toBe(true);
    expect(ask).toHaveBeenCalledOnce();
    // Closing again while it waits asks nothing more.
    expect(closing.hold()).toBe(true);
    expect(ask).toHaveBeenCalledOnce();
    closing.release();
    expect(close).toHaveBeenCalledOnce();
    // The close that follows goes ahead.
    expect(closing.hold()).toBe(false);
    closing.release();
    expect(close).toHaveBeenCalledOnce();
  });

  it("closes after the time limit when the renderer does not answer", () => {
    vi.useFakeTimers();
    const { close, guard: closing } = guard();
    expect(closing.hold()).toBe(true);
    vi.advanceTimersByTime(CLOSE_WAIT_MS - 1);
    expect(close).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(close).toHaveBeenCalledOnce();
  });

  it("does not hold a close when there is no renderer to ask", () => {
    const { close, guard: closing } = guard(false);
    expect(closing.hold()).toBe(false);
    expect(close).not.toHaveBeenCalled();
  });

  it("stops waiting for saves when the daemon is exiting (DMN-22)", () => {
    const { ask, close, guard: closing } = guard();
    closing.abandon();
    expect(closing.hold()).toBe(false);
    expect(ask).not.toHaveBeenCalled();
    expect(close).not.toHaveBeenCalled();
  });

  it("lets a held close go ahead when the daemon is exiting", () => {
    vi.useFakeTimers();
    const { close, guard: closing } = guard();
    expect(closing.hold()).toBe(true);
    closing.abandon();
    expect(close).toHaveBeenCalledOnce();
    vi.advanceTimersByTime(CLOSE_WAIT_MS);
    expect(close).toHaveBeenCalledOnce();
    expect(closing.hold()).toBe(false);
  });

  it("ignores an answer nobody asked for", () => {
    const { close, guard: closing } = guard();
    closing.release();
    expect(close).not.toHaveBeenCalled();
    expect(closing.hold()).toBe(true);
  });
});

describe("waiting for saves when the daemon exits (DMN-22, G9)", () => {
  /** Settles into a flag, so a test can tell when it resolved. */
  function settling(closing: CloseGuard, waitMs?: number) {
    const state = { done: false };
    void closing.settle(waitMs).then(() => {
      state.done = true;
    });
    return state;
  }
  const tick = () => vi.advanceTimersByTimeAsync(0);

  it("asks the renderer once and goes on when it answers, without closing the window", async () => {
    vi.useFakeTimers();
    const { ask, close, guard: closing } = guard();
    const waited = settling(closing);
    await tick();
    expect(ask).toHaveBeenCalledOnce();
    expect(waited.done).toBe(false);
    await vi.advanceTimersByTimeAsync(400);
    expect(waited.done).toBe(false);
    closing.release();
    await tick();
    expect(waited.done).toBe(true);
    // The exit itself quits; the window is not closed for it.
    expect(close).not.toHaveBeenCalled();
    expect(closing.hold()).toBe(false);
  });

  it("goes on after the time limit when the renderer never answers", async () => {
    vi.useFakeTimers();
    const { close, guard: closing } = guard();
    const waited = settling(closing, EXIT_WAIT_MS);
    await vi.advanceTimersByTimeAsync(EXIT_WAIT_MS - 1);
    expect(waited.done).toBe(false);
    await vi.advanceTimersByTimeAsync(1);
    expect(waited.done).toBe(true);
    expect(close).not.toHaveBeenCalled();
  });

  it("stays inside the daemon's own 2 s limit", () => {
    expect(EXIT_WAIT_MS).toBeLessThan(2000);
    expect(EXIT_WAIT_MS).toBeGreaterThanOrEqual(1500);
  });

  it("goes on at once when there is no renderer to ask", async () => {
    vi.useFakeTimers();
    const { guard: closing } = guard(false);
    const waited = settling(closing);
    await tick();
    expect(waited.done).toBe(true);
  });

  it("goes on at once when the guard has already let go", async () => {
    vi.useFakeTimers();
    const { ask, guard: closing } = guard();
    closing.abandon();
    const waited = settling(closing);
    await tick();
    expect(waited.done).toBe(true);
    expect(ask).not.toHaveBeenCalled();
  });

  it("shares a close that is already held, within its own limit", async () => {
    vi.useFakeTimers();
    const { ask, close, guard: closing } = guard();
    expect(closing.hold()).toBe(true);
    const waited = settling(closing, 500);
    await tick();
    expect(ask).toHaveBeenCalledOnce();
    await vi.advanceTimersByTimeAsync(500);
    expect(waited.done).toBe(true);
    // The held close still waits for its own answer.
    expect(close).not.toHaveBeenCalled();
    closing.release();
    expect(close).toHaveBeenCalledOnce();
  });

  it("closes a window held during the wait once the answer comes", async () => {
    vi.useFakeTimers();
    const { close, guard: closing } = guard();
    const waited = settling(closing);
    await tick();
    expect(closing.hold()).toBe(true);
    closing.release();
    await tick();
    expect(waited.done).toBe(true);
    expect(close).toHaveBeenCalledOnce();
  });
});

describe("taking the saves back after an install that did not start (PKG-10)", () => {
  it("asks the renderer again when a window closes after a settle was rearmed", async () => {
    vi.useFakeTimers();
    const { ask, close, guard: closing } = guard();
    const waited = { done: false };
    void closing.settle(500).then(() => {
      waited.done = true;
    });
    await vi.advanceTimersByTimeAsync(0);
    closing.release();
    await vi.advanceTimersByTimeAsync(0);
    expect(waited.done).toBe(true);
    // Settled: a close would go ahead without waiting.
    expect(closing.hold()).toBe(false);
    expect(ask).toHaveBeenCalledOnce();

    // The install failed and the application goes on: closing waits for the
    // saves again (UI-43).
    closing.rearm();
    expect(closing.hold()).toBe(true);
    expect(ask).toHaveBeenCalledTimes(2);
    closing.release();
    expect(close).toHaveBeenCalledOnce();
  });

  it("does nothing while the guard is open or asking", () => {
    const { ask, guard: closing } = guard();
    closing.rearm();
    expect(ask).not.toHaveBeenCalled();
    expect(closing.hold()).toBe(true);
    closing.rearm();
    expect(ask).toHaveBeenCalledOnce();
    // Still the one held close: it is not asked a second time.
    expect(closing.hold()).toBe(true);
    expect(ask).toHaveBeenCalledOnce();
  });

  it("does not undo a guard that was given up because the daemon is exiting", () => {
    const { ask, guard: closing } = guard();
    closing.abandon();
    closing.rearm();
    expect(closing.hold()).toBe(false);
    expect(ask).not.toHaveBeenCalled();
  });

  it("does not undo a guard that was given up after it was settled", async () => {
    vi.useFakeTimers();
    const { guard: closing } = guard();
    void closing.settle(100);
    await vi.advanceTimersByTimeAsync(100);
    closing.abandon();
    closing.rearm();
    expect(closing.hold()).toBe(false);
  });
});
