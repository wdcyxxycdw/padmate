// The event shapes the spike host passes to handlers: a subset of
// InputEvent (SCRIPTING §2.1), and a stick sample standing in for the
// StickState and dt that onStick would pass.

export interface InputEvent {
  readonly control: string;
  readonly gesture: string;
  readonly time: number;
  readonly duration: number;
  readonly count: number;
  readonly scope: string;
  readonly cycle: number;
  readonly held: boolean;
  pass(): void;
  holdKeys(spec: string): void;
  holdLayer(name: string): void;
}

export interface StickEvent {
  readonly control: string;
  readonly gesture: string;
  /** Stick position, +y up, magnitude up to 1. */
  readonly x: number;
  readonly y: number;
  /** Milliseconds since the previous sample. */
  readonly dt: number;
}

export type Handler<E> = (e: E) => void | Promise<void>;

export interface GestureOptions {
  label?: string;
  /** Set by layer(); the spike host resolves it (SCRIPTING §1.5). */
  layer?: string;
}
