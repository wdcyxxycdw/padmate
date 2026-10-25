import type { Profile } from "../shared/generated/schema";

/** Undo steps kept per profile (UI-43). */
export const HISTORY_LIMIT = 100;

/**
 * Undo and redo of one profile's edits on the mapping page (UI-43): whole
 * drafts, newest last. It lives in the renderer's memory only; the daemon's
 * saved versions (.history) are a separate thing.
 */
export class EditHistory {
  private undoSteps: Profile[] = [];
  private redoSteps: Profile[] = [];

  constructor(private readonly limit = HISTORY_LIMIT) {}

  get canUndo(): boolean {
    return this.undoSteps.length > 0;
  }

  get canRedo(): boolean {
    return this.redoSteps.length > 0;
  }

  /** A new step: before is the draft as it was. Redo is gone after it. */
  record(before: Profile): void {
    this.undoSteps.push(structuredClone(before));
    if (this.undoSteps.length > this.limit)
      this.undoSteps.splice(0, this.undoSteps.length - this.limit);
    this.redoSteps = [];
  }

  /** The draft is not a step of its own (see edit); redo is gone after it. */
  dropRedo(): void {
    this.redoSteps = [];
  }

  /** The draft before the last step, or null; current becomes a redo. */
  undo(current: Profile): Profile | null {
    const previous = this.undoSteps.pop();
    if (!previous) return null;
    this.redoSteps.push(structuredClone(current));
    return previous;
  }

  /** The draft the last undo left, or null; current becomes an undo. */
  redo(current: Profile): Profile | null {
    const next = this.redoSteps.pop();
    if (!next) return null;
    this.undoSteps.push(structuredClone(current));
    return next;
  }

  clear(): void {
    this.undoSteps = [];
    this.redoSteps = [];
  }
}
