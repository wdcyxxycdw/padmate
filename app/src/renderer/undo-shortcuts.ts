import { useEffect } from "react";
import type { EditorStore } from "./editor-store";
import {
  type PadInput,
  type PadmateState,
  type PadmateStore,
  readBasicSettings,
} from "./store";

/**
 * After a pad button or a pushed axis changes, keyboard undo waits this
 * long, but only while it is not known whether the pad can send keys to
 * padmate's own window (padHoldsUndo). A pad sends keys there only with
 * 在 padmate 窗口中也发送按键 on, and then the keys do nothing at all
 * (padSendsHere).
 */
export const PAD_QUIET_MS = 500;
/** An axis this far from rest counts as pushed: a quarter of full travel (raw SDL units). */
const PUSHED = 8192;

type KeyLike = Pick<
  KeyboardEvent,
  "key" | "code" | "ctrlKey" | "shiftKey" | "altKey" | "metaKey"
>;

/** Ctrl+Z undoes; Ctrl+Y and Ctrl+Shift+Z redo. */
export function undoCommand(event: KeyLike): "undo" | "redo" | null {
  if (!event.ctrlKey || event.altKey || event.metaKey) return null;
  // The letter as typed; an IME may report "Process", then the key position.
  const letter =
    event.key.length === 1
      ? event.key.toLowerCase()
      : event.code === "KeyZ"
        ? "z"
        : event.code === "KeyY"
          ? "y"
          : "";
  if (letter === "z") return event.shiftKey ? "redo" : "undo";
  if (letter === "y" && !event.shiftKey) return "redo";
  return null;
}

const textInputs = new Set([
  "text",
  "search",
  "url",
  "tel",
  "email",
  "password",
  "number",
]);

/** Whether keys typed at target edit text, so Ctrl+Z belongs to it. */
export function typingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target instanceof HTMLTextAreaElement) return true;
  if (target instanceof HTMLInputElement)
    return textInputs.has(target.type || "text");
  return target.isContentEditable === true;
}

const pushed = (input: PadInput | undefined) =>
  !!input &&
  (Object.values(input.buttons).some(Boolean) ||
    Object.values(input.axes).some((value) => Math.abs(value) >= PUSHED));

/** Whether a pad button changed, or an axis moved while pushed. */
export function padChanged(
  previous: Record<number, PadInput>,
  next: Record<number, PadInput>,
): boolean {
  for (const [device, input] of Object.entries(next)) {
    const before = previous[Number(device)];
    if (input === before) continue;
    if (input.buttons !== before?.buttons) return true;
    for (const [axis, value] of Object.entries(input.axes))
      if (value !== before?.axes[axis] && Math.abs(value) >= PUSHED)
        return true;
  }
  return false;
}

/**
 * Whether a pad may send keys to padmate's own window: 在 padmate 窗口中也
 * 发送按键 is on and a pad is connected. A pad's Ctrl+Z then reaches the
 * window within milliseconds, before its input event, so the keys cannot
 * be told apart and none of them undoes (UI-43).
 */
export function padSendsHere(
  state: Pick<PadmateState, "status" | "config">,
): boolean {
  const on =
    state.status?.sendToOwnWindow === true ||
    readBasicSettings(state.config).sendToOwnWindow;
  return on && (state.status?.devices?.length ?? 0) > 0;
}

/**
 * Whether the daemon or config.json has told whether pads send keys to
 * padmate's own window; until one has, the pad may still be about to.
 */
export function padSettingKnown(
  state: Pick<PadmateState, "status" | "config">,
): boolean {
  return (
    typeof state.status?.sendToOwnWindow === "boolean" || state.config !== null
  );
}

/**
 * Whether the pad holds Ctrl+Z and the like back (UI-43). With 在 padmate
 * 窗口中也发送按键 on, a pad's own Ctrl+Z reaches the window before its input
 * event does: nothing undoes. With it known to be off, no pad can send keys
 * here, so what a pad reports (a button the daemon never saw released, a
 * stick that rests off centre, a press a moment ago) has nothing to do
 * with a key at the keyboard and holds nothing back. Before the setting is
 * known, a pad used within PAD_QUIET_MS or still pushed does.
 */
export function padHoldsUndo(
  state: Pick<PadmateState, "status" | "config" | "inputs">,
  lastPad: number,
  now: number,
): boolean {
  if (padSendsHere(state)) return true;
  if (padSettingKnown(state)) return false;
  return (
    now - lastPad < PAD_QUIET_MS || Object.values(state.inputs).some(pushed)
  );
}

/** Whether a shortcut field is recording keys. */
const capturing = () =>
  !!document.querySelector(
    '.drawer-shortcut[data-active="true"], .drawer-step[data-active="true"]',
  );

/**
 * Ctrl+Z, Ctrl+Y and Ctrl+Shift+Z on the mapping page (UI-43). Text
 * fields, open dialogs and shortcut capture keep the keys; while a pad may
 * send keys to this window (padHoldsUndo), they do nothing.
 */
export function useUndoShortcuts(editor: EditorStore, store: PadmateStore) {
  useEffect(() => {
    let lastPad = Number.NEGATIVE_INFINITY;
    let inputs = store.getState().inputs;
    const unsubscribe = store.subscribe((state) => {
      if (state.inputs === inputs) return;
      if (padChanged(inputs, state.inputs)) lastPad = performance.now();
      inputs = state.inputs;
    });
    const onKeyDown = (event: KeyboardEvent) => {
      const command = undoCommand(event);
      if (!command || event.defaultPrevented) return;
      if (
        typingTarget(event.target) ||
        document.querySelector("dialog[open]") ||
        capturing()
      )
        return;
      if (padHoldsUndo(store.getState(), lastPad, performance.now())) return;
      event.preventDefault();
      if (command === "undo") editor.getState().undo();
      else editor.getState().redo();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("keydown", onKeyDown);
      unsubscribe();
    };
  }, [editor, store]);
}
