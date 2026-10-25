// @vitest-environment jsdom
import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { holdCapture, useCaptureRunning } from "./capture-hold";

afterEach(cleanup);

describe("the mark of a running shortcut capture", () => {
  it("is on while any capture holds it, and off after the last release", () => {
    const { result } = renderHook(() => useCaptureRunning());
    expect(result.current).toBe(false);
    let first = () => {};
    let second = () => {};
    act(() => {
      first = holdCapture();
      second = holdCapture();
    });
    expect(result.current).toBe(true);
    act(() => first());
    expect(result.current).toBe(true);
    act(() => second());
    expect(result.current).toBe(false);
  });

  it("is released once: a second call of the same release takes nothing from another capture", () => {
    const { result } = renderHook(() => useCaptureRunning());
    let mine = () => {};
    let other = () => {};
    act(() => {
      mine = holdCapture();
      other = holdCapture();
    });
    act(() => {
      mine();
      mine();
    });
    expect(result.current).toBe(true);
    act(() => other());
    expect(result.current).toBe(false);
  });
});
