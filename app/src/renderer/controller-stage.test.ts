import { afterEach, describe, expect, it, vi } from "vitest";
import { createStageStore, STAGE_IDLE_MS, stageKey } from "./controller-stage";

afterEach(() => vi.useRealTimers());

interface FakeInfo {
  type: string;
  tag?: string;
}

/** A stage that counts what a real one would have to rebuild on the GPU. */
function fakeFactory() {
  const created: { info: FakeInfo; dispose: ReturnType<typeof vi.fn> }[] = [];
  const create = vi.fn((info: FakeInfo) => {
    const stage = { info, dispose: vi.fn(), broken: false };
    created.push(stage);
    return stage;
  });
  return { create, created };
}

const store = (create: ReturnType<typeof fakeFactory>["create"]) =>
  createStageStore<FakeInfo, ReturnType<typeof create>>({
    key: (info) => JSON.stringify(info),
    create,
    usable: (stage) => !stage.broken,
  });

describe("stage key", () => {
  it("names the shell, the button labels and the button mask", () => {
    const key = stageKey({ type: "xboxone" });
    expect(stageKey({ type: "xboxone" })).toBe(key);
    expect(stageKey({ type: "ps5" })).not.toBe(key);
    expect(stageKey({ type: "xboxone", buttonMask: 3 })).not.toBe(key);
    expect(
      stageKey({ type: "xboxone", buttonLabels: { south: "B" } }),
    ).not.toBe(key);
    // The key parses back into what the Controller passes in.
    expect(JSON.parse(stageKey({ type: "ps5", buttonMask: 7 }))).toEqual({
      type: "ps5",
      buttonMask: 7,
    });
  });
});

describe("stage store", () => {
  it("hands the same stage to the next mount without creating another", () => {
    const { create, created } = fakeFactory();
    const stages = store(create);
    const first = stages.acquire({ type: "xboxone" });
    stages.release(first);
    const second = stages.acquire({ type: "xboxone" });
    expect(second).toBe(first);
    expect(create).toHaveBeenCalledTimes(1);
    expect(created[0].dispose).not.toHaveBeenCalled();
  });

  it("disposes the old stage when the model changes", () => {
    const { create, created } = fakeFactory();
    const stages = store(create);
    const xbox = stages.acquire({ type: "xboxone" });
    stages.release(xbox);
    const pad = stages.acquire({ type: "ps5" });
    expect(pad).not.toBe(xbox);
    expect(created[0].dispose).toHaveBeenCalledOnce();
    expect(created[1].dispose).not.toHaveBeenCalled();
    // The old stage's release comes too late to matter.
    stages.release(xbox);
    expect(stages.current()).toBe(pad);
    // Back to the first model: it is built again.
    stages.acquire({ type: "xboxone" });
    expect(create).toHaveBeenCalledTimes(3);
    expect(created[1].dispose).toHaveBeenCalledOnce();
  });

  it("frees a stage nobody came back for, and builds it again after that", () => {
    vi.useFakeTimers();
    const { create, created } = fakeFactory();
    const stages = store(create);
    const stage = stages.acquire({ type: "xboxone" });
    stages.release(stage);
    vi.advanceTimersByTime(STAGE_IDLE_MS - 1);
    expect(created[0].dispose).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(created[0].dispose).toHaveBeenCalledOnce();
    expect(stages.current()).toBeUndefined();
    expect(stages.acquire({ type: "xboxone" })).not.toBe(stage);
    expect(create).toHaveBeenCalledTimes(2);
  });

  it("keeps a stage that is used again before the idle time is over", () => {
    vi.useFakeTimers();
    const { create, created } = fakeFactory();
    const stages = store(create);
    const stage = stages.acquire({ type: "xboxone" });
    stages.release(stage);
    vi.advanceTimersByTime(STAGE_IDLE_MS - 1000);
    expect(stages.acquire({ type: "xboxone" })).toBe(stage);
    // In use: the old timer is gone.
    vi.advanceTimersByTime(STAGE_IDLE_MS * 2);
    expect(created[0].dispose).not.toHaveBeenCalled();
    // The idle time starts again with the next release.
    stages.release(stage);
    vi.advanceTimersByTime(STAGE_IDLE_MS);
    expect(created[0].dispose).toHaveBeenCalledOnce();
  });

  it("waits for every mount that holds the stage before the idle time starts", () => {
    vi.useFakeTimers();
    const { create, created } = fakeFactory();
    const stages = store(create);
    const stage = stages.acquire({ type: "xboxone" });
    stages.acquire({ type: "xboxone" });
    stages.release(stage);
    vi.advanceTimersByTime(STAGE_IDLE_MS * 2);
    expect(created[0].dispose).not.toHaveBeenCalled();
    stages.release(stage);
    vi.advanceTimersByTime(STAGE_IDLE_MS);
    expect(created[0].dispose).toHaveBeenCalledOnce();
  });

  it("builds a new stage when the kept one cannot be used any more", () => {
    const { create, created } = fakeFactory();
    const stages = store(create);
    const stage = stages.acquire({ type: "xboxone" });
    stages.release(stage);
    // E.g. the GPU lost its context while the page was away.
    stage.broken = true;
    const next = stages.acquire({ type: "xboxone" });
    expect(next).not.toBe(stage);
    expect(created[0].dispose).toHaveBeenCalledOnce();
  });

  it("drops a stage that failed in use, once", () => {
    const { create, created } = fakeFactory();
    const stages = store(create);
    const stage = stages.acquire({ type: "xboxone" });
    stages.discard(stage);
    expect(created[0].dispose).toHaveBeenCalledOnce();
    expect(stages.current()).toBeUndefined();
    stages.discard(stage);
    stages.release(stage);
    expect(created[0].dispose).toHaveBeenCalledOnce();
    expect(stages.acquire({ type: "xboxone" })).not.toBe(stage);
  });

  it("keeps nothing when a stage cannot be created", () => {
    const { create, created } = fakeFactory();
    const stages = store(create);
    const stage = stages.acquire({ type: "xboxone" });
    stages.release(stage);
    create.mockImplementationOnce(() => {
      throw new Error("No WebGL");
    });
    expect(() => stages.acquire({ type: "ps5" })).toThrow("No WebGL");
    expect(stages.current()).toBeUndefined();
    expect(created[0].dispose).toHaveBeenCalledOnce();
    expect(stages.acquire({ type: "ps5" })).toBeDefined();
  });
});
