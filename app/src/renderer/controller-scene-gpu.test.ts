// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ModelInfo } from "./controller-callouts";

// The GPU stage on a renderer that draws nothing: there is no WebGL in the
// test, so this covers what the stage does around the renderer, not the
// pixels.
const gl = vi.hoisted(() => ({
  renderers: [] as {
    sizes: [number, number][];
    lost: boolean;
    disposed: number;
  }[],
}));

vi.mock("three", async (importOriginal) => {
  const three = await importOriginal<typeof import("three")>();
  class FakeRenderer {
    toneMapping = 0;
    toneMappingExposure = 1;
    sizes: [number, number][] = [];
    lost = false;
    disposed = 0;
    ratio = 1;
    constructor() {
      gl.renderers.push(this);
    }
    getContext() {
      return { isContextLost: () => this.lost };
    }
    getPixelRatio() {
      return this.ratio;
    }
    setPixelRatio(ratio: number) {
      this.ratio = ratio;
    }
    setSize(width: number, height: number) {
      this.sizes.push([width, height]);
    }
    render() {}
    dispose() {
      this.disposed++;
    }
    forceContextLoss() {
      this.lost = true;
    }
  }
  class FakeEnvironments {
    fromScene() {
      return { texture: {}, dispose() {} };
    }
    dispose() {}
  }
  return {
    ...three,
    WebGLRenderer: FakeRenderer,
    PMREMGenerator: FakeEnvironments,
  };
});

vi.mock("./controller-models", () => ({
  // The model never arrives: only the stage itself is looked at.
  controllerModels: { get: () => new Promise(() => {}) },
  modelShell: () => "xbox",
}));

import { STAGE_IDLE_MS } from "./controller-stage";

describe("the GPU stage of the controller scene (H2)", () => {
  const info: ModelInfo = { type: "xboxone" };
  let createControllerScene: typeof import("./controller-scene").createControllerScene;
  beforeEach(async () => {
    // The scene keeps its stage for the whole session: each test gets its own.
    vi.resetModules();
    ({ createControllerScene } = await import("./controller-scene"));
    gl.renderers.length = 0;
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    const observer = class {
      observe = vi.fn();
      unobserve = vi.fn();
      disconnect = vi.fn();
    };
    vi.stubGlobal("ResizeObserver", observer);
    vi.stubGlobal("IntersectionObserver", observer);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
    document.body.replaceChildren();
  });

  function host() {
    const element = document.createElement("div");
    document.body.append(element);
    return element;
  }

  it("is built again when its context was lost while the page was away, with no event to say so", () => {
    createControllerScene(host(), info, vi.fn(), vi.fn()).dispose();
    expect(gl.renderers).toHaveLength(1);
    // Still usable: the next visit takes it.
    createControllerScene(host(), info, vi.fn(), vi.fn()).dispose();
    expect(gl.renderers).toHaveLength(1);
    // The context goes (sleep, a GPU reset) without `webglcontextlost` having
    // arrived; the stage must not be drawn on again.
    gl.renderers[0].lost = true;
    createControllerScene(host(), info, vi.fn(), vi.fn()).dispose();
    expect(gl.renderers).toHaveLength(2);
    expect(gl.renderers[0].disposed).toBe(1);
    expect(gl.renderers[1].lost).toBe(false);
  });

  it("shrinks its drawing buffer when the page lets go, and frees the renderer after the idle time", () => {
    const scene = createControllerScene(host(), info, vi.fn(), vi.fn());
    expect(gl.renderers[0].sizes).toEqual([]);
    scene.dispose();
    expect(gl.renderers[0].sizes).toEqual([[1, 1]]);
    expect(gl.renderers[0].disposed).toBe(0);
    vi.advanceTimersByTime(STAGE_IDLE_MS);
    expect(gl.renderers[0].disposed).toBe(1);
    // A stage that is gone is not resized again.
    scene.dispose();
    expect(gl.renderers[0].sizes).toEqual([[1, 1]]);
  });
});
