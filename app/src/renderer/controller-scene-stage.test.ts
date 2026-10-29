// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ModelInfo } from "./controller-callouts";
import {
  bindControllerScene,
  createFrameScheduler,
  type SceneStage,
} from "./controller-scene";
import { createStageStore, stageKey } from "./controller-stage";
import type { PadInput } from "./store";

describe("frame scheduler flush", () => {
  it("draws a dirty frame at once, and only then", () => {
    const pending = new Map<number, FrameRequestCallback>();
    let next = 0;
    const draw = vi.fn();
    const cancel = vi.fn((id: number) => pending.delete(id));
    const scheduler = createFrameScheduler(
      draw,
      (callback) => {
        pending.set(++next, callback);
        return next;
      },
      cancel,
    );
    // Nothing changed: nothing to draw.
    scheduler.flush();
    expect(draw).not.toHaveBeenCalled();
    scheduler.invalidate();
    scheduler.flush();
    expect(draw).toHaveBeenCalledOnce();
    // The frame that was waiting is gone, and the next flush has nothing to do.
    expect(pending.size).toBe(0);
    expect(cancel).toHaveBeenCalledOnce();
    scheduler.flush();
    expect(draw).toHaveBeenCalledOnce();
    // Not while hidden or after dispose.
    scheduler.invalidate();
    scheduler.setVisible(false);
    scheduler.flush();
    expect(draw).toHaveBeenCalledOnce();
    scheduler.setVisible(true);
    scheduler.dispose();
    scheduler.flush();
    expect(draw).toHaveBeenCalledOnce();
  });
});

/** A stage that counts what a real one uploads to the GPU on its first frame. */
function fakeStage() {
  const canvas = document.createElement("canvas");
  const lostListeners = new Set<() => void>();
  let model: { update: ReturnType<typeof vi.fn> } | undefined;
  let finish = () => {};
  let fail = (_error: Error) => {};
  const loaded = new Promise<void>((resolve, reject) => {
    finish = resolve;
    fail = reject;
  });
  loaded.catch(() => {});
  const stage = {
    canvas,
    camera: {} as never,
    loaded,
    broken: false,
    /** Textures uploaded: only the stage's first frame pays for them. */
    uploads: 0,
    draws: 0,
    get model() {
      return model as never;
    },
    render: vi.fn(() => {
      if (stage.draws++ === 0) stage.uploads++;
    }),
    park: vi.fn(),
    onLost(listener: () => void) {
      lostListeners.add(listener);
      return () => lostListeners.delete(listener);
    },
    dispose: vi.fn(() => canvas.remove()),
    // What the test does to the stage.
    modelUpdate: () => model?.update,
    async built() {
      model = { update: vi.fn() };
      finish();
      await loaded;
    },
    async failed() {
      fail(new Error("No model"));
      await loaded.catch(() => {});
    },
    loseContext() {
      stage.broken = true;
      for (const listener of [...lostListeners]) listener();
    },
  };
  return stage;
}

describe("a retained controller scene (H2)", () => {
  const info: ModelInfo = { type: "xboxone" };
  const input: PadInput = { buttons: { south: true }, axes: {} };
  let frames = new Map<number, FrameRequestCallback>();
  let lastFrame = 0;
  const runFrames = () => {
    const due = [...frames.values()];
    frames.clear();
    for (const frame of due) frame(0);
  };

  beforeEach(() => {
    frames = new Map();
    // The idle time is a timer; the frames below are driven by hand.
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    const observer = class {
      observe = vi.fn();
      unobserve = vi.fn();
      disconnect = vi.fn();
    };
    vi.stubGlobal("ResizeObserver", observer);
    vi.stubGlobal("IntersectionObserver", observer);
    vi.stubGlobal("requestAnimationFrame", (frame: FrameRequestCallback) => {
      frames.set(++lastFrame, frame);
      return lastFrame;
    });
    vi.stubGlobal("cancelAnimationFrame", (id: number) => frames.delete(id));
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
    document.body.replaceChildren();
  });

  function setup() {
    const stages: ReturnType<typeof fakeStage>[] = [];
    const store = createStageStore<ModelInfo, SceneStage>({
      key: stageKey,
      create: () => {
        const stage = fakeStage();
        stages.push(stage);
        return stage as unknown as SceneStage;
      },
      usable: (stage) => !stage.broken,
    });
    return { stages, store, create: bindControllerScene(store) };
  }
  function host() {
    const element = document.createElement("div");
    element.getBoundingClientRect = () =>
      ({ width: 800, height: 600, left: 0, top: 0 }) as DOMRect;
    document.body.append(element);
    return element;
  }

  it("draws the next visit at once, with no new renderer and no new upload", async () => {
    const { stages, create } = setup();
    const firstHost = host();
    const ready = vi.fn();
    const first = create(firstHost, info, ready, vi.fn());
    // The first visit builds the stage; the model arrives later.
    expect(firstHost.firstElementChild).toBe(stages[0].canvas);
    expect(stages[0].draws).toBe(0);
    await stages[0].built();
    runFrames();
    expect(ready).toHaveBeenCalledOnce();
    expect(stages[0].uploads).toBe(1);
    // Leaving the page keeps the stage, and draws nothing.
    first.dispose();
    expect(firstHost.contains(stages[0].canvas)).toBe(false);
    expect(stages[0].dispose).not.toHaveBeenCalled();
    first.update(input, "south", []);
    runFrames();
    expect(stages[0].draws).toBe(1);
    expect(stages[0].modelUpdate()).not.toHaveBeenCalledWith(input);
    // Back on the page: the same canvas is drawn in the same call, and the
    // frame that follows is not needed.
    const nextHost = host();
    const nextReady = vi.fn();
    create(nextHost, info, nextReady, vi.fn(), undefined, {
      input,
      selected: "south",
      unbound: ["guide"],
    });
    expect(stages).toHaveLength(1);
    expect(nextHost.firstElementChild).toBe(stages[0].canvas);
    expect(nextReady).toHaveBeenCalledOnce();
    expect(stages[0].draws).toBe(2);
    expect(stages[0].uploads).toBe(1);
    expect(stages[0].modelUpdate()).toHaveBeenLastCalledWith(input);
    expect(frames.size).toBe(0);
  });

  it("keeps the one stage through a replayed effect", async () => {
    const { stages, create } = setup();
    const element = host();
    const firstReady = vi.fn();
    const replayReady = vi.fn();
    // StrictMode: mount, clean up, mount again, before the model is in.
    create(element, info, firstReady, vi.fn()).dispose();
    expect(element.childElementCount).toBe(0);
    create(element, info, replayReady, vi.fn());
    expect(stages).toHaveLength(1);
    expect(stages[0].dispose).not.toHaveBeenCalled();
    await stages[0].built();
    runFrames();
    expect(firstReady).not.toHaveBeenCalled();
    expect(replayReady).toHaveBeenCalledOnce();
    expect(stages[0].uploads).toBe(1);
  });

  it("disposes the stage when the model changes", async () => {
    const { stages, create } = setup();
    const first = create(host(), info, vi.fn(), vi.fn());
    await stages[0].built();
    first.dispose();
    const nextHost = host();
    create(nextHost, { type: "ps5" }, vi.fn(), vi.fn());
    expect(stages).toHaveLength(2);
    expect(stages[0].dispose).toHaveBeenCalledOnce();
    expect(stages[1].dispose).not.toHaveBeenCalled();
    expect(nextHost.querySelectorAll("canvas")).toHaveLength(1);
    expect(nextHost.firstElementChild).toBe(stages[1].canvas);
  });

  it("keeps the stage while no mount holds it, however long the page is away (ADR-0077)", async () => {
    const { stages, create } = setup();
    const first = create(host(), info, vi.fn(), vi.fn());
    await stages[0].built();
    first.dispose();
    // Ten minutes used to free it; the window stays loaded now.
    vi.advanceTimersByTime(24 * 60 * 60 * 1000);
    expect(stages[0].dispose).not.toHaveBeenCalled();
    // The page is shown again: the same stage, no new upload.
    create(host(), info, vi.fn(), vi.fn());
    expect(stages).toHaveLength(1);
    expect(stages[0].uploads).toBe(1);
  });

  it("shrinks the drawing buffer while the page is away, and only for the mount that held the canvas", async () => {
    const { stages, create } = setup();
    const first = create(host(), info, vi.fn(), vi.fn());
    await stages[0].built();
    runFrames();
    expect(stages[0].park).not.toHaveBeenCalled();
    first.dispose();
    expect(stages[0].park).toHaveBeenCalledOnce();
    // Back on the page: drawn at the page's size, nothing parked while it is shown.
    const second = create(host(), info, vi.fn(), vi.fn(), undefined, { input });
    expect(stages[0].render).toHaveBeenLastCalledWith(800, 600);
    expect(stages[0].park).toHaveBeenCalledOnce();
    // A mount that was replaced does not park what the newer one draws on.
    first.dispose();
    expect(stages[0].park).toHaveBeenCalledOnce();
    second.dispose();
    expect(stages[0].park).toHaveBeenCalledTimes(2);
  });

  it("lets go of the stage when a mount cannot be set up", async () => {
    const { stages, create } = setup();
    create(host(), info, vi.fn(), vi.fn()).dispose();
    await stages[0].built();
    stages[0].modelUpdate()?.mockImplementationOnce(() => {
      throw new Error("update failed");
    });
    const nextHost = host();
    expect(() =>
      create(nextHost, info, vi.fn(), vi.fn(), undefined, { input }),
    ).toThrow("update failed");
    // The mount that failed holds nothing: its canvas is off the page, and
    // the stage is kept for the next visit, as if the page had never been
    // shown.
    expect(nextHost.contains(stages[0].canvas)).toBe(false);
    expect(stages[0].dispose).not.toHaveBeenCalled();
    const lastHost = host();
    create(lastHost, info, vi.fn(), vi.fn(), undefined, { input });
    expect(stages).toHaveLength(1);
    expect(lastHost.firstElementChild).toBe(stages[0].canvas);
  });

  it("gives the canvas to the newest mount", async () => {
    const { stages, create } = setup();
    const first = create(host(), info, vi.fn(), vi.fn());
    await stages[0].built();
    const secondHost = host();
    const second = create(secondHost, info, vi.fn(), vi.fn());
    expect(stages).toHaveLength(1);
    expect(secondHost.firstElementChild).toBe(stages[0].canvas);
    // The earlier mount no longer draws or takes the canvas back.
    first.update(input, "south", []);
    first.dispose();
    runFrames();
    expect(secondHost.contains(stages[0].canvas)).toBe(true);
    expect(stages[0].modelUpdate()).not.toHaveBeenCalledWith(input);
    // Letting go of both keeps it for the next visit.
    second.dispose();
    expect(stages[0].dispose).not.toHaveBeenCalled();
  });

  it("reports a model that cannot be built, and builds a new stage next time", async () => {
    const { stages, create } = setup();
    const failure = vi.fn();
    const ready = vi.fn();
    create(host(), info, ready, failure);
    await stages[0].failed();
    expect(failure).toHaveBeenCalledOnce();
    expect(ready).not.toHaveBeenCalled();
    expect(stages[0].dispose).toHaveBeenCalledOnce();
    create(host(), info, vi.fn(), vi.fn());
    expect(stages).toHaveLength(2);
  });

  it("reports a lost context in use, and replaces a stage that lost it while away", async () => {
    const { stages, create } = setup();
    const failure = vi.fn();
    create(host(), info, vi.fn(), failure);
    await stages[0].built();
    runFrames();
    stages[0].loseContext();
    expect(failure).toHaveBeenCalledOnce();
    expect(stages[0].dispose).toHaveBeenCalledOnce();
    // Lost while the page was away: no one to tell, but not used again.
    const away = create(host(), info, vi.fn(), vi.fn());
    expect(stages).toHaveLength(2);
    await stages[1].built();
    away.dispose();
    const awayFailure = vi.fn();
    stages[1].loseContext();
    create(host(), info, vi.fn(), awayFailure);
    expect(awayFailure).not.toHaveBeenCalled();
    expect(stages).toHaveLength(3);
    expect(stages[1].dispose).toHaveBeenCalledOnce();
  });

  it("drops the stage when a frame cannot be drawn", async () => {
    const { stages, create } = setup();
    const failure = vi.fn();
    create(host(), info, vi.fn(), failure);
    stages[0].render.mockImplementationOnce(() => {
      throw new Error("GL error");
    });
    await stages[0].built();
    runFrames();
    expect(failure).toHaveBeenCalledOnce();
    expect(stages[0].dispose).toHaveBeenCalledOnce();
    runFrames();
    expect(failure).toHaveBeenCalledOnce();
  });

  it("draws only on demand while the stage is idle", async () => {
    const { stages, create } = setup();
    const scene = create(host(), info, vi.fn(), vi.fn());
    await stages[0].built();
    runFrames();
    expect(stages[0].draws).toBe(1);
    runFrames();
    expect(frames.size).toBe(0);
    expect(stages[0].draws).toBe(1);
    scene.update(input, undefined, []);
    runFrames();
    expect(stages[0].draws).toBe(2);
    runFrames();
    expect(stages[0].draws).toBe(2);
  });
});
