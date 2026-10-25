import type { EventBuffer } from "./event-buffer";

interface RendererLifecycle {
  on(
    event: "did-start-loading" | "did-finish-load",
    listener: () => void,
  ): unknown;
}

export function bindEventDelivery(
  contents: RendererLifecycle,
  buffer: EventBuffer,
): void {
  buffer.suspendDelivery();
  contents.on("did-start-loading", () => buffer.suspendDelivery());
  contents.on("did-finish-load", () => {
    // The main document's preload has installed its batch receiver before
    // did-finish-load. Never spend delivery credit on an absent receiver.
    buffer.resumeDelivery();
  });
}
