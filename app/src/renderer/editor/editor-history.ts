import type { Profile } from "../../shared/generated/schema";

/** Undo steps kept per profile. */
export const HISTORY_LIMIT = 100;

/**
 * What a step does beyond the draft it brings back: run after an undo or a
 * redo of that step has shown its draft. A pick from an app integration
 * uses it to move the profile's item file along with the binding.
 */
export interface StepEffects {
  undo(): void;
  redo(): void;
}

interface Step {
  draft: Profile;
  effects?: StepEffects;
}

/**
 * Undo and redo of one profile's edits on the mapping page: whole
 * drafts, newest last. It lives in the renderer's memory only; the daemon's
 * saved versions (.history) are a separate thing.
 */
export class EditHistory {
  private undoSteps: Step[] = [];
  private redoSteps: Step[] = [];
  /** The effects of the step the last undo or redo moved; the caller runs them. */
  moved: StepEffects | undefined;

  constructor(private readonly limit = HISTORY_LIMIT) {}

  get canUndo(): boolean {
    return this.undoSteps.length > 0;
  }

  get canRedo(): boolean {
    return this.redoSteps.length > 0;
  }

  /** The newest undo step, to tell later whether an edit made a new one. */
  get top(): object | undefined {
    return this.undoSteps[this.undoSteps.length - 1];
  }

  /** A new step: before is the draft as it was. Redo is gone after it. */
  record(before: Profile): void {
    this.undoSteps.push({ draft: structuredClone(before) });
    if (this.undoSteps.length > this.limit)
      this.undoSteps.splice(0, this.undoSteps.length - this.limit);
    this.redoSteps = [];
  }

  /** Gives the newest step effects; false when `step` is not the newest. */
  annotate(step: object | undefined, effects: StepEffects): boolean {
    if (!step || step !== this.top) return false;
    (step as Step).effects = effects;
    return true;
  }

  /**
   * Takes the newest step away when it is `step`, with its redo: the draft
   * before it is the caller's to show. False when it is not the newest.
   */
  remove(step: object | undefined): boolean {
    if (!step || step !== this.top) return false;
    this.undoSteps.pop();
    this.redoSteps = [];
    return true;
  }

  /** The draft is not a step of its own (see edit); redo is gone after it. */
  dropRedo(): void {
    this.redoSteps = [];
  }

  /** The draft before the last step, or null; current becomes a redo. */
  undo(current: Profile): Profile | null {
    this.moved = undefined;
    const previous = this.undoSteps.pop();
    if (!previous) return null;
    this.redoSteps.push({
      draft: structuredClone(current),
      ...(previous.effects ? { effects: previous.effects } : {}),
    });
    this.moved = previous.effects;
    return previous.draft;
  }

  /** The draft the last undo left, or null; current becomes an undo. */
  redo(current: Profile): Profile | null {
    this.moved = undefined;
    const next = this.redoSteps.pop();
    if (!next) return null;
    this.undoSteps.push({
      draft: structuredClone(current),
      ...(next.effects ? { effects: next.effects } : {}),
    });
    this.moved = next.effects;
    return next.draft;
  }

  clear(): void {
    this.undoSteps = [];
    this.redoSteps = [];
    this.moved = undefined;
  }
}
