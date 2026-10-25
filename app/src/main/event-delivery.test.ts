import { EventEmitter } from "node:events";
import { describe, expect, it } from "vitest";
import { type EventBatch, EventBuffer } from "./event-buffer";
import { bindEventDelivery } from "./event-delivery";

describe("renderer navigation delivery", () => {
  it("waits for the new preload after initial load and each reload", () => {
    const contents = new EventEmitter();
    const scheduled: (() => void)[] = [];
    const sent: EventBatch[] = [];
    const buffer = new EventBuffer(
      (batch) => sent.push(batch),
      (callback) => scheduled.push(callback),
    );
    const flush = () => {
      for (const callback of scheduled.splice(0)) callback();
    };
    bindEventDelivery(contents, buffer);
    buffer.push({ topic: "input", data: { control: "south", kind: "down" } });
    flush();
    expect(sent).toEqual([]);
    contents.emit("did-finish-load");
    flush();
    expect(sent).toHaveLength(1);
    const previousBatch = sent[0].id;

    contents.emit("did-start-loading");
    buffer.push({ topic: "input", data: { control: "south", kind: "up" } });
    buffer.acknowledge(previousBatch);
    flush();
    expect(sent).toHaveLength(1);
    contents.emit("did-start-loading");
    contents.emit("did-finish-load");
    flush();
    expect(sent).toHaveLength(2);
    expect(sent[1].events).toEqual([
      { topic: "dropped", data: { n: 1 } },
      { topic: "input", data: { control: "south", kind: "up" } },
    ]);
    buffer.push({ topic: "log", data: { receiver: "new" } });
    buffer.acknowledge(previousBatch);
    flush();
    expect(sent).toHaveLength(2);
    buffer.acknowledge(sent[1].id);
    flush();
    expect(sent[2].events).toEqual([
      { topic: "log", data: { receiver: "new" } },
    ]);
  });
});
