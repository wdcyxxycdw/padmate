import { describe, expect, it } from "vitest";
import type { DaemonEvent } from "../shared/api";
import { type EventBatch, EventBuffer } from "./event-buffer";

function fixture() {
  let tick: (() => void) | undefined;
  const sent: EventBatch[] = [];
  const buffer = new EventBuffer(
    (batch) => sent.push(batch),
    (callback) => {
      tick = callback;
    },
  );
  const flush = () => {
    const callback = tick;
    tick = undefined;
    callback?.();
  };
  return { buffer, sent, flush };
}

describe("bounded renderer delivery", () => {
  it("keeps one batch in flight and a bounded queue for an unresponsive renderer", () => {
    const { buffer, sent, flush } = fixture();
    buffer.push({ topic: "log", data: { sequence: 1 } });
    flush();
    for (let n = 0; n < 100000; n++)
      buffer.push({ topic: "log", data: { sequence: n + 2 } });
    flush();
    expect(sent).toHaveLength(1);
    expect(buffer.size).toBe(128);
    buffer.acknowledge(sent[0].id);
    flush();
    expect(sent).toHaveLength(2);
    expect(sent[1].events).toHaveLength(32);
    expect(sent[1].events[0]).toEqual({ topic: "dropped", data: { n: 99872 } });
  });

  it("coalesces axes and button state while preserving device removal order", () => {
    const { buffer, sent, flush } = fixture();
    const input = (
      kind: string,
      control?: string,
      value?: number,
    ): DaemonEvent => ({
      topic: "input",
      data: { device: 1, kind, control, value },
    });
    for (let n = 0; n < 10000; n++) buffer.push(input("axis", "leftx", n));
    buffer.push(input("down", "south"));
    buffer.push(input("up", "south"));
    expect(buffer.size).toBe(2);
    buffer.push(input("removed"));
    buffer.push({
      topic: "input",
      data: { kind: "down", device: 2, control: "south" },
    });
    flush();
    expect(sent[0].events).toEqual([
      input("removed"),
      { topic: "input", data: { kind: "down", device: 2, control: "south" } },
    ]);
  });

  it("suspends navigation delivery until the replacement receiver is ready", () => {
    const { buffer, sent, flush } = fixture();
    buffer.push({ topic: "script.status", data: [] });
    flush();
    buffer.push({ topic: "script.status", data: [{ generation: 2 }] });
    buffer.acknowledge(sent[0].id + 1);
    flush();
    expect(sent).toHaveLength(1);
    buffer.suspendDelivery();
    flush();
    // A loading document has no preload receiver to acknowledge this batch.
    // Sending now would permanently consume the buffer's only credit.
    expect(sent).toHaveLength(1);
    buffer.resumeDelivery();
    flush();
    expect(sent).toHaveLength(2);
    expect(sent[1].events.at(-1)).toEqual({
      topic: "script.status",
      data: [{ generation: 2 }],
    });
    buffer.push({ topic: "log", data: { afterNavigation: true } });
    buffer.acknowledge(sent[0].id);
    flush();
    expect(sent).toHaveLength(2);
    buffer.acknowledge(sent[1].id);
    flush();
    expect(sent[2].events).toEqual([
      { topic: "log", data: { afterNavigation: true } },
    ]);
  });

  it("cancels an already scheduled flush and keeps loading queues bounded", () => {
    const { buffer, sent, flush } = fixture();
    buffer.push({ topic: "log", data: { beforeNavigation: true } });
    buffer.suspendDelivery();
    flush();
    expect(sent).toEqual([]);
    for (let n = 0; n < 1000; n++)
      buffer.push({ topic: "log", data: { sequence: n } });
    flush();
    expect(sent).toEqual([]);
    expect(buffer.size).toBe(128);
    buffer.resumeDelivery();
    flush();
    expect(sent[0].events).toHaveLength(32);
    expect(sent[0].events[0]).toEqual({ topic: "dropped", data: { n: 873 } });
  });

  it("does not resume a loading page when the daemon connection is replaced", () => {
    const { buffer, sent, flush } = fixture();
    buffer.suspendDelivery();
    buffer.push({ topic: "log", data: { oldConnection: true } });
    buffer.clear();
    buffer.push({ topic: "log", data: { newConnection: true } });
    flush();
    expect(sent).toEqual([]);
    buffer.resumeDelivery();
    flush();
    expect(sent[0].events).toEqual([
      { topic: "log", data: { newConnection: true } },
    ]);
  });

  it("discards the old connection and ignores its late acknowledgement", () => {
    const { buffer, sent, flush } = fixture();
    buffer.push({ topic: "output.preview", data: { chord: "Old" } });
    flush();
    const oldID = sent[0].id;
    for (let n = 0; n < 10000; n++)
      buffer.push({ topic: "log", data: { sequence: n } });
    buffer.clear();
    buffer.push({ topic: "output.preview", data: { chord: "New" } });
    flush();
    expect(sent[1].events).toEqual([
      { topic: "output.preview", data: { chord: "New" } },
    ]);
    buffer.push({ topic: "log", data: { sequence: 1 } });
    buffer.acknowledge(oldID);
    flush();
    expect(sent).toHaveLength(2);
    buffer.acknowledge(sent[1].id);
    flush();
    expect(sent[2].events).toEqual([{ topic: "log", data: { sequence: 1 } }]);
  });
});
