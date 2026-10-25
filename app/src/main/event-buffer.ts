import type { DaemonEvent } from "../shared/api";

export interface EventBatch {
  id: number;
  events: DaemonEvent[];
}

function input(
  event: DaemonEvent,
): { device?: number; kind?: string; control?: string } | undefined {
  return event.topic === "input" && event.data && typeof event.data === "object"
    ? event.data
    : undefined;
}

function supersedes(next: DaemonEvent, previous: DaemonEvent): boolean {
  if (next.topic !== previous.topic) return false;
  if (next.topic === "input") {
    const a = input(next);
    const b = input(previous);
    return (
      !!a?.control &&
      !!b &&
      a.device === b?.device &&
      a.control === b.control &&
      ["axis", "down", "up"].includes(a.kind || "") &&
      ["axis", "down", "up"].includes(b.kind || "")
    );
  }
  if (next.topic === "script.status" || next.topic === "output.preview")
    return true;
  return (
    next.topic === "engine.activity" &&
    (next.data as { kind?: string })?.kind === "state" &&
    (previous.data as { kind?: string })?.kind === "state"
  );
}

// Native webContents.send has no backpressure. This buffer grants one batch
// of credit, and preload returns it only after consuming that batch.
export class EventBuffer {
  private queued: DaemonEvent[] = [];
  private inFlight?: EventBatch;
  private nextID = 0;
  private scheduled = false;
  private dropped = 0;
  private closed = false;
  private suspended = false;

  constructor(
    private send: (batch: EventBatch) => void,
    private schedule: (callback: () => void) => void = (callback) => {
      setTimeout(callback, 16);
    },
  ) {}
  get size(): number {
    return this.queued.length;
  }

  push(event: DaemonEvent): void {
    if (this.closed) return;
    const state = input(event);
    if (state?.kind === "removed")
      this.queued = this.queued.filter(
        (previous) => input(previous)?.device !== state.device,
      );
    const replace = this.queued.findIndex((previous) =>
      supersedes(event, previous),
    );
    if (replace >= 0) this.queued.splice(replace, 1);
    if (this.queued.length >= 128) {
      // Preserve the final controller state preferentially over older log rows.
      const log = this.queued.findIndex((row) => row.topic === "log");
      this.queued.splice(log >= 0 ? log : 0, 1);
      this.dropped++;
    }
    this.queued.push(event);
    this.requestFlush();
  }

  acknowledge(id: unknown): void {
    if (id !== this.inFlight?.id) return;
    this.inFlight = undefined;
    this.requestFlush();
  }

  suspendDelivery(): void {
    this.suspended = true;
    if (this.inFlight) this.dropped += this.inFlight.events.length;
    this.inFlight = undefined;
  }

  resumeDelivery(): void {
    this.suspended = false;
    this.requestFlush();
  }

  clear(): void {
    this.queued = [];
    this.inFlight = undefined;
    this.dropped = 0;
    // Batch IDs never rewind, so an old renderer acknowledgement cannot
    // release credit belonging to a new daemon connection.
  }

  close(): void {
    this.closed = true;
    this.queued = [];
    this.inFlight = undefined;
  }

  private requestFlush(): void {
    if (
      this.closed ||
      this.suspended ||
      this.scheduled ||
      this.inFlight ||
      (this.queued.length === 0 && this.dropped === 0)
    )
      return;
    this.scheduled = true;
    this.schedule(() => {
      this.scheduled = false;
      if (this.closed || this.suspended || this.inFlight) return;
      const events: DaemonEvent[] = this.dropped
        ? [{ topic: "dropped", data: { n: this.dropped } }]
        : [];
      this.dropped = 0;
      events.push(...this.queued.splice(0, 32 - events.length));
      if (!events.length) return;
      this.inFlight = { id: ++this.nextID, events };
      this.send(this.inFlight);
    });
  }
}
