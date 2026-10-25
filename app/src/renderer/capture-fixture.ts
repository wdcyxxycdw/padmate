// Tests record shortcuts the way the user does (OUT-18): through native
// capture, since a shortcut cannot be typed.
import { act, fireEvent, screen, within } from "@testing-library/react";
import { vi } from "vitest";
import type { KeyboardCaptureEvent, PadmateAPI } from "../shared/api";

type CaptureAPI = Pick<PadmateAPI, "onCapture" | "captureStart">;

/**
 * Replaces api's capture with one the test drives and puts api on
 * window.padmate, which the shortcut fields use. Sessions count from 1.
 */
export function fakeCapture<T extends CaptureAPI>(api: T) {
  const listeners = new Set<(event: KeyboardCaptureEvent) => void>();
  let session = 0;
  api.onCapture = vi.fn((listener: (event: KeyboardCaptureEvent) => void) => {
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  });
  api.captureStart = vi.fn(async () => ++session);
  window.padmate = api as unknown as PadmateAPI;
  /** Sends a capture event, of the latest session unless it names one. */
  const emit = (
    event: Omit<KeyboardCaptureEvent, "session"> & { session?: number },
  ) =>
    act(() => {
      for (const listener of [...listeners]) listener({ session, ...event });
    });
  /**
   * Records chord in the shortcut field called field (快捷键, 第 2 步, …;
   * the only one in scope when omitted): starts its capture, presses the
   * chord and stops the capture, which commits it.
   */
  const record = async (
    chord: string,
    { field, scope }: { field?: string; scope?: HTMLElement } = {},
  ) => {
    const root = scope ? within(scope) : screen;
    const group = field
      ? root.getByRole("group", { name: field })
      : root.getByRole("group", { name: /^(快捷键|Shortcut)$/ });
    const button = within(group).getByRole("button", {
      name: /^(录入|Capture)/,
    });
    fireEvent.click(button);
    // The session id arrives after captureStart resolves.
    await act(async () => {});
    await emit({ active: true, chord });
    fireEvent.click(button);
  };
  return { emit, record, session: () => session };
}

/** What a shortcut field shows: its chord, "" when it has none. */
export function shortcutValue(
  field = "快捷键",
  scope: HTMLElement = document.body,
): string {
  return (
    (within(scope).getByRole("group", { name: field }) as HTMLElement).dataset
      .keys ?? ""
  );
}
