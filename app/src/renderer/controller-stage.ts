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
// the textures and the drawing buffer. It is kept for one model at a time,
// freed when another model is needed, when the context is lost, and after
// STAGE_IDLE_MS without a visit. Nothing is drawn while no mount holds the
// stage.
import type { ModelInfo } from "./controller-callouts";

/** How long a stage that no mount holds stays alive, in milliseconds. */
export const STAGE_IDLE_MS = 10 * 60 * 1000;

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
  /** A mount lets go of the stage. The last one starts the idle time. */
  release(stage: S): void;
  /** Disposes a stage that failed in use. */
  discard(stage: S): void;
  /** The stage that is kept, if any. */
  current(): S | undefined;
}

export function createStageStore<I, S extends RetainedStage>({
  key,
  create,
  usable = () => true,
  idleMs = STAGE_IDLE_MS,
}: {
  key: (info: I) => string;
  create: (info: I) => S;
  /** False for a stage that cannot draw any more, e.g. a lost context. */
  usable?: (stage: S) => boolean;
  idleMs?: number;
}): StageStore<I, S> {
  let kept:
    | {
        key: string;
        stage: S;
        /** Mounts that hold the stage. */
        holders: number;
        idle?: ReturnType<typeof setTimeout>;
      }
    | undefined;
  const drop = () => {
    if (!kept) return;
    const { stage, idle } = kept;
    kept = undefined;
    if (idle !== undefined) clearTimeout(idle);
    stage.dispose();
  };
  return {
    acquire(info) {
      const wanted = key(info);
      if (kept?.key === wanted && usable(kept.stage)) {
        if (kept.idle !== undefined) clearTimeout(kept.idle);
        kept.idle = undefined;
        kept.holders++;
        return kept.stage;
      }
      // Another model, or a stage that cannot draw: nothing is kept if the
      // new one cannot be made.
      drop();
      const stage = create(info);
      kept = { key: wanted, stage, holders: 1 };
      return stage;
    },
    release(stage) {
      // A stage that was dropped meanwhile has nothing left to release.
      if (!kept || kept.stage !== stage) return;
      kept.holders = Math.max(0, kept.holders - 1);
      if (kept.holders > 0 || kept.idle !== undefined) return;
      kept.idle = setTimeout(drop, idleMs);
    },
    discard(stage) {
      if (kept?.stage === stage) drop();
    },
    current: () => kept?.stage,
  };
}
