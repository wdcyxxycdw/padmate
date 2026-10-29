// Keeping the controller scene's GPU side between visits to the mapping page
// (H2, UI-04).
//
// G3 stopped decoding the GLB on every visit, but each visit still made a new
// WebGL renderer and uploaded the model's ~10 textures to the new context. That
// upload is synchronous: measured in the Electron app (ANGLE/D3D11) it blocks
// the main thread for 0.6-0.85 s (texSubImage2D), against 0.35-1.1 ms for
// copying the cached model. So the stage (canvas, renderer, scene, environment
// and the model with its GPU textures) outlives a mount: the next mount takes
// the same canvas and draws.
//
// The trade-off is GPU memory held while the page is away: one WebGL context,
// the textures and the drawing buffer (parked at 1×1). It is kept for one
// model at a time, freed when another model is needed or the context is lost,
// and otherwise kept until padmate quits: the window stays loaded after it is
// closed (ADR-0077), and ten minutes away used to free it (ADR-0058) and so
// bring the upload back. Nothing is drawn while no mount holds the stage.
import type { ModelInfo } from "./controller-callouts";

/**
 * What a stage is made from: the shell and the labels of the buttons, so the
 * same pad always gets the same stage.
 */
export function stageKey({ type, buttonLabels, buttonMask }: ModelInfo) {
  return JSON.stringify({ type, buttonLabels, buttonMask });
}

export interface RetainedStage {
  /** Releases everything the stage holds on the GPU; it is not used again. */
  dispose(): void;
}

export interface StageStore<I, S extends RetainedStage> {
  /** The stage for a model: the kept one, or a new one (the old one is disposed). */
  acquire(info: I): S;
  /** Disposes a stage that failed in use. */
  discard(stage: S): void;
  /** The stage that is kept, if any. */
  current(): S | undefined;
}

export function createStageStore<I, S extends RetainedStage>({
  key,
  create,
  usable = () => true,
}: {
  key: (info: I) => string;
  create: (info: I) => S;
  /** False for a stage that cannot draw any more, e.g. a lost context. */
  usable?: (stage: S) => boolean;
}): StageStore<I, S> {
  let kept: { key: string; stage: S } | undefined;
  const drop = () => {
    if (!kept) return;
    const { stage } = kept;
    kept = undefined;
    stage.dispose();
  };
  return {
    acquire(info) {
      const wanted = key(info);
      if (kept?.key === wanted && usable(kept.stage)) return kept.stage;
      // Another model, or a stage that cannot draw: nothing is kept if the
      // new one cannot be made.
      drop();
      const stage = create(info);
      kept = { key: wanted, stage };
      return stage;
    },
    discard(stage) {
      if (kept?.stage === stage) drop();
    },
    current: () => kept?.stage,
  };
}
