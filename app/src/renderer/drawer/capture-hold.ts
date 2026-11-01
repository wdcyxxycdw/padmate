import { useSyncExternalStore } from "react";

// A shortcut capture owns the keyboard and the focus of the window while it
// runs (at most 30 seconds): a field that loses focus ends its capture and
// commits the chord it heard so far. Something that opens by itself and takes
// the focus, such as the update dialog the automatic check opens (PKG-10),
// waits until no capture runs.
let running = 0;
const listeners = new Set<() => void>();

function notify() {
  for (const listener of [...listeners]) listener();
}

/** Marks a capture as running. The function it returns ends the mark, once. */
export function holdCapture(): () => void {
  let held = true;
  running += 1;
  notify();
  return () => {
    if (!held) return;
    held = false;
    running -= 1;
    notify();
  };
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** Whether a shortcut capture is running in this window. */
export function useCaptureRunning(): boolean {
  return useSyncExternalStore(
    subscribe,
    () => running > 0,
    () => false,
  );
}
