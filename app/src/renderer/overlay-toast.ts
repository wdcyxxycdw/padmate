// The timing of a toast (HUD-04, ADR-0063). The fades are CSS transitions on
// the toast's opacity (overlay.css, which must use the same durations); the
// page times the phases. A script's toast (SCR-22) says how long it holds.

import { defaultToastHoldMs } from "../shared/overlay";

/** Fade in, in ms. */
export const toastFadeInMs = 120;
/** How long it stays fully shown, in ms, unless the toast says otherwise. */
export const toastHoldMs = defaultToastHoldMs;
/** Fade out, in ms. */
export const toastFadeOutMs = 300;

export interface ToastSchedule {
  /** When the fade out starts, in ms after the toast came. */
  fadeOutAt: number;
  /** When the toast is gone. */
  endAt: number;
}

/**
 * When a toast fades out and ends, holding for holdMs (the default hold
 * without it). Without animation (prefers-reduced-motion) it just shows for
 * the hold and goes: there is no fade to wait for.
 */
export function toastSchedule(
  reducedMotion: boolean,
  holdMs: number = toastHoldMs,
): ToastSchedule {
  if (reducedMotion) return { fadeOutAt: holdMs, endAt: holdMs };
  const fadeOutAt = toastFadeInMs + holdMs;
  return { fadeOutAt, endAt: fadeOutAt + toastFadeOutMs };
}

/** Whether the system asks for no animation. */
export function prefersReducedMotion(): boolean {
  try {
    return (
      typeof window.matchMedia === "function" &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches
    );
  } catch {
    return false;
  }
}
