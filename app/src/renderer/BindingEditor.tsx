import {
  type KeyboardEvent,
  type MouseEvent as ReactMouseEvent,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";
import type { KeyboardCaptureEvent } from "../shared/api";
import type {
  ProfileAction,
  ProfileBinding,
  ProfileKeysAction,
  ProfileMacroAction,
  ProfileSequenceAction,
  ProfileTiming,
} from "../shared/generated/schema";
import { holdCapture } from "./capture-hold";
import {
  type CaptureStep,
  type HeldModifiers,
  ShortcutRecorder,
} from "./capture-recorder";
import {
  editorError,
  type FileOnlyAction,
  type Gesture,
  gestureName,
  holdShortcut,
  isFileOnlyAction,
  isReservedShortcut,
  jumpPercent,
  type Mapping,
  macroName,
  mappingGesture,
  mappings,
  missingMacroText,
  profileName,
  resolveTiming,
  settleTap,
  tapGestureFor,
  usedGestures,
} from "./editor-model";
import {
  type DelaySource,
  delayNote,
  GestureCard,
  gestureSummary,
} from "./GestureCard";
import { sortMacrosByName } from "./library-model";
import { MacroContext, missingMacros } from "./macro-context";
import {
  StepButtons,
  type StepMoves,
  StepsPreview,
  stepKindName,
  WaitStep,
} from "./StepEditor";
import {
  actionSteps,
  hasTextStep,
  maxSteps,
  movedStep,
  type NewStepKind,
  newStep,
  type Step,
  stepKind,
  stepsAction,
} from "./sequence-model";
import type { MouseKey } from "./shortcut-keys";
import type { Language } from "./store";
import { FieldError, numberParser, useCommitField } from "./ui/commit-field";
import {
  Dialog,
  Disclosure,
  MenuButton,
  type MenuEntry,
  Segmented,
  Switch,
} from "./ui/controls";
import { Icon } from "./ui/Icon";
import { chordKeys, Keycaps } from "./ui/Keycaps";

type Profiles = { id: string; name?: string }[];
type Actions = { name: string; label?: string }[];

export function editorText(
  language: Language | undefined,
  zh: string,
  en: string,
) {
  return language === "en" ? en : zh;
}

/**
 * A whole number of a binding, committed on Enter or when the field loses
 * focus (UI-43). Empty is the default. A text field, since a number field
 * reports text it cannot parse ("1e") as empty, which would clear the value.
 */
export function NumberField({
  label,
  value,
  onChange,
  min = 0,
  max = 60000,
  placeholder,
  unit,
  language,
  optional = true,
}: {
  label: string;
  value?: number;
  onChange: (value: number | undefined) => void;
  min?: number;
  max?: number;
  placeholder?: string;
  /** Shown at the end of the field, such as %. */
  unit?: string;
  language?: Language;
  /** False: an empty field is an error and nothing is committed. */
  optional?: boolean;
}) {
  const id = useId();
  const field = useCommitField<number | undefined>({
    value: value === undefined ? "" : String(value),
    parse: numberParser(language, { min, max, optional }),
    onCommit: (next) => {
      if (next !== value) onChange(next);
    },
  });
  const input = (
    <input
      id={id}
      type="text"
      inputMode="numeric"
      className="input input-number"
      autoComplete="off"
      spellCheck={false}
      placeholder={placeholder}
      {...field.props}
    />
  );
  return (
    <div className="drawer-number">
      <label className="drawer-number-label" htmlFor={id}>
        {label}
      </label>
      {unit ? (
        <span className="drawer-number-box">
          {input}
          <span className="drawer-number-unit" aria-hidden="true">
            {unit}
          </span>
        </span>
      ) : (
        input
      )}
      <FieldError id={field.errorId} text={field.error} />
    </div>
  );
}

/** The key of a mouse press, by MouseEvent.button. */
const mouseButtonKeys: readonly (MouseKey | undefined)[] = [
  "MouseLeft",
  "MouseMiddle",
  "MouseRight",
  "MouseX1",
  "MouseX2",
];

/** The key of one notch of the wheel: the way it turned most. */
function wheelKey(event: WheelEvent): MouseKey | null {
  const { deltaX, deltaY } = event;
  if (deltaX === 0 && deltaY === 0) return null;
  if (Math.abs(deltaY) >= Math.abs(deltaX))
    return deltaY < 0 ? "WheelUp" : "WheelDown";
  return deltaX < 0 ? "WheelLeft" : "WheelRight";
}

const heldModifiers = (event: MouseEvent): HeldModifiers => ({
  Ctrl: event.ctrlKey,
  Shift: event.shiftKey,
  Alt: event.altKey,
  Win: event.metaKey,
});

/**
 * Keeps the events that the release of a button still brings (its click, and
 * on Windows the menu of the right button) from the page, once: the press was
 * a key of the shortcut, not a click on what is under it. They come right
 * after the release; a new press, or the window losing focus, ends this too,
 * so that it can never keep a click that is meant.
 */
function swallowRelease(): void {
  const types = ["click", "auxclick", "contextmenu"] as const;
  const stop = (event: Event) => {
    event.preventDefault();
    event.stopPropagation();
  };
  let timer: ReturnType<typeof setTimeout> | undefined;
  const off = () => {
    clearTimeout(timer);
    for (const type of types) window.removeEventListener(type, stop, true);
    window.removeEventListener("mouseup", release, true);
    window.removeEventListener("mousedown", off, true);
    window.removeEventListener("blur", off);
  };
  const release = () => {
    timer = setTimeout(off, 100);
  };
  for (const type of types) window.addEventListener(type, stop, true);
  window.addEventListener("mouseup", release, true);
  window.addEventListener("mousedown", off, true);
  window.addEventListener("blur", off);
}

/** How long Esc is held to end a capture (UI-24). */
export const ESC_HOLD_MS = 1000;

/** How long one capture lasts: the helper stops itself then (keyboard_capture). */
const CAPTURE_LIMIT_MS = 30_000;

/**
 * What to say when a capture could not run, from the code of the main process
 * or the helper that says why (foreground-unavailable, hook-unavailable, …).
 * Only the window being behind is cured by a click on it; the others name what
 * did not start.
 */
function unavailableNote(code: string, language: Language | undefined): string {
  const t = (zh: string, en: string) => editorText(language, zh, en);
  switch (code) {
    case "foreground-unavailable":
    case "blur":
      return t(
        "无法录入快捷键。请点一下 padmate 窗口，确认它在最前面，再重试。",
        "Capturing is not available. Click the padmate window to bring it to the front, then try again.",
      );
    case "connection-unavailable":
      return t(
        "无法录入快捷键：连不上映射服务。请稍后再试。",
        "Capturing is not available: the mapping service can't be reached. Try again in a moment.",
      );
    case "authentication-failed":
    case "identity-verification-failed":
    case "mapping-capture-failed":
      return t(
        "无法录入快捷键：映射服务没有接受这次录入。请稍后再试。",
        "Capturing is not available: the mapping service did not accept it. Try again in a moment.",
      );
    case "hook-unavailable":
      return t(
        "无法录入快捷键：键盘监听没有启动。请重试。",
        "Capturing is not available: keyboard listening did not start. Try again.",
      );
    case "timeout":
    case "unavailable":
    case "invalid":
    case "ended":
      return t(
        "无法录入快捷键：录入组件没有启动。请重试。",
        "Capturing is not available: the capture helper did not start. Try again.",
      );
    default:
      return t(
        "无法录入快捷键。请重试。",
        "Capturing is not available. Try again.",
      );
  }
}

/**
 * What to say when a capture ended by itself, or "" when there is nothing to
 * say. The reason is a code of the main process or the helper (timeout,
 * blur, stopped, replaced, ended, …): never shown as it is. A capture the
 * user ended, or that ended because the window lost focus (the hint says it
 * will), says nothing.
 *
 * The 30 seconds are the whole capture, not a wait for a key: the helper
 * stops itself that long after it started, with no final event, so the main
 * process usually reports the exit as "ended" before its own "timeout".
 * elapsedMs, the time since the capture was asked for, tells that exit from a
 * helper that broke off early.
 */
export function captureEndNote(
  reason: string | undefined,
  language: Language | undefined,
  elapsedMs: number = Number.POSITIVE_INFINITY,
): string {
  const t = (zh: string, en: string) => editorText(language, zh, en);
  const limit = t(
    "录入最长 30 秒，已自动结束。需要的话请重新录入。",
    "Capturing stops by itself after 30 seconds. Start again if you need to.",
  );
  switch (reason) {
    case undefined:
    case "":
    case "blur":
    case "stopped":
    case "replaced":
      return "";
    case "timeout":
      return limit;
    case "ended":
      return elapsedMs >= CAPTURE_LIMIT_MS - 1000
        ? limit
        : t(
            "录入意外中断，请重试。",
            "Capturing stopped unexpectedly. Try again.",
          );
    default:
      return unavailableNote(reason, language);
  }
}

/**
 * What to say when the capture could not start, from the message of the
 * rejected request, or undefined when the error is not about capturing and is
 * shown as it is. The main process says why in "Keyboard capture <code>", or
 * that padmate is not the window in front in a sentence of its own.
 */
export function captureStartNote(
  message: string,
  language: Language | undefined,
): string | undefined {
  if (message === "Keyboard capture requires the focused padmate window")
    return unavailableNote("foreground-unavailable", language);
  const code = /^Keyboard capture ([a-z-]+)$/.exec(message)?.[1];
  if (code === undefined) return undefined;
  // The person ended it, or it was replaced by another: nothing went wrong.
  if (code === "stopped" || code === "replaced") return "";
  return unavailableNote(code, language);
}

/**
 * One chord, set by recording only (ADR-0043): shortcuts are recorded, never
 * typed (OUT-18). Recording is one step (ADR-0076): a click on the field or
 * its button starts it (that click is not recorded), the first key that is not
 * a modifier ends it at once with the modifiers held, and modifiers alone are
 * recorded when they are let go; at most three keys. The rules are the
 * ShortcutRecorder's. The keyboard is heard by the native helper (with the
 * sides of the modifiers and the key's position); the mouse is heard here:
 * inside this window a button, or one notch of the wheel, is a key too, and
 * does nothing else while the capture runs. The capture session is owned by
 * this field: it stops when the capture ends, when focus leaves the field and
 * on unmount. The chord is committed when the capture ends, so a recording is
 * one edit; removing the step or unmounting drops it.
 *
 * The way out is to hold Esc for ESC_HOLD_MS (UI-24); a short Esc is recorded
 * as Esc, and a click elsewhere is recorded as a click. The helper only
 * watches (it never swallows a key), so this window gets the Esc press and its
 * release as ordinary key events, and times the hold itself, from the first
 * press (the repeat of a held key can start after a second). The window also
 * tells when the modifiers are let go.
 */
function ShortcutField({
  value,
  onChange,
  language,
  label,
  variant,
  physical,
  onRemove,
  moves,
  stepIndex,
  kindLabel,
}: {
  value: string;
  onChange: (value: string) => void;
  language?: Language;
  label: string;
  variant: "full" | "step";
  /** Capture records physical key positions (set under 高级). */
  physical: boolean;
  onRemove?: () => void;
  /** A step of a sequence moves up and down (UI-31). */
  moves?: StepMoves;
  /** The step's place in its sequence, for the editor to find its row. */
  stepIndex?: number;
  /** What the row's left cell says; the step's name when absent. */
  kindLabel?: string;
}) {
  const t = (zh: string, en: string) => editorText(language, zh, en);
  const [active, setActive] = useState(false);
  const [error, setError] = useState("");
  // The chord the running capture would commit, shown in the field.
  const [captured, setCaptured] = useState<string | null>(null);
  // The rules of the running capture, and whether it has ended: what comes
  // before the field has drawn the end (a burst of the wheel) is no more keys.
  const recorder = useRef(new ShortcutRecorder());
  const ended = useRef(true);
  // The running hold of Esc, and whether one that ended the capture is still down.
  const hold = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const heldAfter = useRef(false);
  const clearHold = useCallback(() => {
    if (hold.current === undefined) return;
    clearTimeout(hold.current);
    hold.current = undefined;
  }, []);
  const latest = useRef({ value, onChange, physical, language });
  latest.current = { value, onChange, physical, language };
  // What the recorder says to do: show the chord so far, or end the capture
  // with what it recorded (null: nothing, as after a hold of Esc).
  const apply = useCallback(
    (step: CaptureStep) => {
      if (ended.current) return;
      if (!step.done) {
        setCaptured(step.shown);
        return;
      }
      ended.current = true;
      clearHold();
      setCaptured(null);
      setActive(false);
      const { value: current, onChange: commit } = latest.current;
      if (step.chord !== null && step.chord !== current) commit(step.chord);
    },
    [clearHold],
  );
  // Ends the capture and drops what it heard (the step is gone or moves).
  const abandon = () => {
    ended.current = true;
    clearHold();
    setCaptured(null);
    setActive(false);
  };
  // Esc held for ESC_HOLD_MS: the capture is cancelled; the Esc that is held
  // is not a shortcut.
  const endByHold = () => {
    hold.current = undefined;
    heldAfter.current = true;
    apply(recorder.current.cancel());
  };
  const field = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!active) return;
    const api = window.padmate;
    let cancelled = false;
    let session: number | undefined;
    const startedAt = Date.now();
    const early: KeyboardCaptureEvent[] = [];
    const receive = (event: KeyboardCaptureEvent) => {
      if (cancelled) return;
      if (session === undefined) {
        early.push(event);
        return;
      }
      if (event.session !== session) return;
      if (event.chord) {
        const chord = latest.current.physical
          ? (event.physicalChord ?? event.chord)
          : event.chord;
        apply(recorder.current.heard(event.chord, chord));
      }
      if (!event.active) {
        setError(
          captureEndNote(
            event.reason,
            latest.current.language,
            Date.now() - startedAt,
          ),
        );
        // The capture ended by itself (the window lost focus, …): what is
        // held is kept.
        apply(recorder.current.end());
        setActive(false);
      }
    };
    const stop = api.onCapture(receive);
    void api
      .captureStart()
      .then((id) => {
        session = id;
        if (cancelled) {
          void api.captureStop(id).catch(() => {});
          return;
        }
        for (const event of early) receive(event);
      })
      .catch((e) => {
        if (cancelled) return;
        // Its "timeout" is the 2 s the helper has to start, not the 30 s of a
        // running capture: it could not start.
        const message = editorError(e);
        setError(captureStartNote(message, latest.current.language) ?? message);
        setActive(false);
      });
    return () => {
      cancelled = true;
      stop();
      if (session !== undefined) void api.captureStop(session).catch(() => {});
    };
  }, [active, apply]);
  // Inside this window the mouse is a key too (ADR-0076): a button, or one
  // notch of the wheel. The page does not get it, and no menu comes up.
  useEffect(() => {
    if (!active) return;
    const press = (event: MouseEvent) => {
      // The second press of the double click that started the capture.
      if (ended.current || event.detail > 1) return;
      const key = mouseButtonKeys[event.button];
      if (!key) return;
      event.preventDefault();
      event.stopPropagation();
      swallowRelease();
      apply(recorder.current.mouse(key, heldModifiers(event)));
    };
    const turn = (event: WheelEvent) => {
      event.preventDefault();
      event.stopPropagation();
      const key = wheelKey(event);
      if (key) apply(recorder.current.mouse(key, heldModifiers(event)));
    };
    const menu = (event: Event) => {
      event.preventDefault();
      event.stopPropagation();
    };
    window.addEventListener("mousedown", press, true);
    window.addEventListener("wheel", turn, { capture: true, passive: false });
    window.addEventListener("contextmenu", menu, true);
    return () => {
      window.removeEventListener("mousedown", press, true);
      window.removeEventListener("wheel", turn, true);
      window.removeEventListener("contextmenu", menu, true);
    };
  }, [active, apply]);
  // A hold ends with the capture, however that ends, and with the field.
  useEffect(() => {
    if (!active) clearHold();
  }, [active, clearHold]);
  // Nothing opens by itself and takes the focus while a capture runs (the
  // update dialog: capture-hold.ts).
  useEffect(() => (active ? holdCapture() : undefined), [active]);
  useEffect(() => clearHold, [clearHold]);
  const shown = captured ?? value;
  const reserved = isReservedShortcut(shown);
  const hasKeys = chordKeys(shown).length > 0;
  const start = () => {
    // Inside a disabled fieldset the button is off, but a click on the field
    // around it still arrives: nothing could be recorded.
    if (active || button.current?.matches(":disabled")) return;
    setError("");
    heldAfter.current = false;
    recorder.current = new ShortcutRecorder();
    ended.current = false;
    setActive(true);
    // Keys pressed during the capture reach the field here (see swallow).
    button.current?.focus();
  };
  // The clear button beside the keys only clears.
  const startFromField = (event: ReactMouseEvent<HTMLElement>) => {
    if ((event.target as HTMLElement).closest(".drawer-capture-clear")) return;
    start();
  };
  const clear = () => {
    setError("");
    onChange("");
    button.current?.focus();
  };
  // While capturing, the keys belong to the capture: they neither press the
  // focused button (Space, Enter) nor reach the page's own shortcuts.
  const swallow = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!active) {
      // The Esc that ended a capture is still down: its repeats and its
      // release are not the page's (a drawer that closes on Esc must not see
      // them). A new press, which is not a repeat, is the page's again.
      if (heldAfter.current && event.key === "Escape") {
        if (event.type === "keydown" && !event.repeat)
          heldAfter.current = false;
        else {
          event.preventDefault();
          event.stopPropagation();
          if (event.type === "keyup") heldAfter.current = false;
        }
      }
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    if (event.type === "keydown") {
      if (event.key !== "Escape") {
        // Another key: not a hold of Esc alone.
        if (!event.repeat) clearHold();
      } else if (
        !event.repeat &&
        !event.nativeEvent.isComposing &&
        !(event.ctrlKey || event.altKey || event.shiftKey || event.metaKey) &&
        hold.current === undefined
      )
        hold.current = setTimeout(endByHold, ESC_HOLD_MS);
    } else if (event.key === "Escape") {
      clearHold();
      apply(recorder.current.escReleased());
    } else if (
      !(event.ctrlKey || event.altKey || event.shiftKey || event.metaKey)
    )
      apply(recorder.current.modifiersReleased());
  };
  const captureLabel = active
    ? t("录入中…", "Recording…")
    : variant === "full"
      ? t("录入快捷键", "Capture shortcut")
      : t(`录入${label}`, `Capture ${label.toLowerCase()}`);
  const keys =
    active && !hasKeys ? (
      <span className="drawer-capture-wait">
        {t("请按下快捷键…", "Press a shortcut…")}
      </span>
    ) : hasKeys ? (
      <Keycaps keys={shown} large={variant === "full"} language={language} />
    ) : (
      <span className="drawer-capture-empty">
        {variant === "full"
          ? t("未设置快捷键", "No shortcut yet")
          : t("未设置", "Not set")}
      </span>
    );
  return (
    // biome-ignore lint/a11y/useSemanticElements: buttons around one value, not a form section
    <div
      className={variant === "full" ? "drawer-shortcut" : "drawer-step"}
      ref={field}
      role="group"
      aria-label={label}
      data-active={active}
      data-step={stepIndex}
      data-kind={variant === "step" ? "keys" : undefined}
      data-keys={shown}
      onKeyDown={swallow}
      onKeyUp={swallow}
      onBlur={(event) => {
        const leaving = !field.current?.contains(event.relatedTarget as Node);
        if (active && leaving) apply(recorder.current.end());
        if (leaving) heldAfter.current = false;
      }}
    >
      {variant === "full" ? (
        // biome-ignore lint/a11y/noStaticElementInteractions: the click starts the capture like the button inside, which is the keyboard's way in
        // biome-ignore lint/a11y/useKeyWithClickEvents: same
        <div className="drawer-capture" onClick={startFromField}>
          <span className="drawer-capture-keys">{keys}</span>
          {hasKeys && !active && (
            <button
              type="button"
              className="icon-btn bare drawer-capture-clear"
              aria-label={t("清除快捷键", "Clear shortcut")}
              title={t("清除快捷键", "Clear shortcut")}
              onClick={clear}
            >
              <Icon name="close" size={14} />
            </button>
          )}
          <span className="drawer-capture-actions">
            <button
              ref={button}
              type="button"
              className="btn drawer-capture-btn"
            >
              <span className="drawer-rec-dot" aria-hidden="true" />
              {captureLabel}
            </button>
          </span>
        </div>
      ) : (
        <div className="drawer-step-row">
          <span className="drawer-step-label" aria-hidden="true">
            {kindLabel ?? label}
          </span>
          {/* biome-ignore lint/a11y/noStaticElementInteractions: the click starts the capture like the button beside it, which is the keyboard's way in */}
          {/* biome-ignore lint/a11y/useKeyWithClickEvents: same */}
          <span className="drawer-step-keys" onClick={start}>
            {keys}
          </span>
          <button
            ref={button}
            type="button"
            className="icon-btn bare drawer-step-capture"
            aria-label={captureLabel}
            title={captureLabel}
            onClick={start}
          >
            <span className="drawer-rec-dot" aria-hidden="true" />
          </button>
          {onRemove && (
            <StepButtons
              label={label}
              language={language}
              moves={
                moves && {
                  ...moves,
                  onMove: (delta) => {
                    // Steps are positional: end this slot's capture before
                    // another step moves into it.
                    abandon();
                    moves.onMove(delta);
                  },
                }
              }
              onRemove={() => {
                // Steps are positional: end this slot's capture before the
                // next step moves into it.
                abandon();
                onRemove();
              }}
            />
          )}
        </div>
      )}
      {active && (
        <p className="drawer-hint" role="status">
          <Icon name="info" size={14} stroke={2} />
          <span>
            {t(
              "请按下快捷键，也可以点一下鼠标或滚一格滚轮。按住 Esc 1 秒取消。",
              "Press a shortcut, click the mouse or turn the wheel. Hold Esc for 1 second to cancel.",
            )}
          </span>
        </p>
      )}
      {reserved && (
        <p className="drawer-warning" data-testid="reserved-warning">
          <Icon name="warn" size={14} stroke={2} />
          <span>
            {t(
              "这是系统保留的组合键，可能直接执行系统操作，无法保证录入或发送。",
              "This is a reserved system shortcut. It may run a system action and cannot be reliably captured or sent.",
            )}
          </span>
        </p>
      )}
      {error && (
        <p className="drawer-warning" role="alert">
          <Icon name="warn" size={14} stroke={2} />
          <span>{error}</span>
        </p>
      )}
    </div>
  );
}

/** The row of a step that focus goes to once the steps are shown again. */
interface StepFocus {
  index: number;
  /** A button of the row (a step that moved keeps the focus on its mover). */
  button?: "up" | "down";
}

/**
 * One shortcut, or the steps of a sequence (UI-31): shortcuts only are a list
 * of them that taps, as before; a wait among them makes it a sequence action.
 * A sequence is tapped and has no hold or repeat. A single shortcut has
 * neither a send mode nor a repeat to set (ADR-0076): it is held while the
 * button is held, which the action's editor writes (holdShortcut).
 */
function KeysFields({
  value,
  onChange,
  language,
  physical,
}: {
  value: ProfileKeysAction | ProfileSequenceAction;
  onChange: (value: ProfileKeysAction | ProfileSequenceAction) => void;
  language?: Language;
  physical: boolean;
}) {
  const t = (zh: string, en: string) => editorText(language, zh, en);
  const root = useRef<HTMLDivElement>(null);
  // Counts the moves and removals of steps. A row belongs to its place, not to
  // a step: what is typed in it and not committed (a wait that is no number)
  // and a running capture are the row's own state, and must not move into the
  // step that takes its place. So the rows are made anew when a step moves or
  // goes.
  const [reshuffles, setReshuffles] = useState(0);
  // After a step is removed or moved, or a wait is added, focus goes to a row
  // of the steps (the one before a removed step, or the single field).
  const refocus = useRef<StepFocus | null>(null);
  // biome-ignore lint/correctness/useExhaustiveDependencies: runs when the steps change
  useEffect(() => {
    const focus = refocus.current;
    if (focus === null) return;
    refocus.current = null;
    const row = root.current?.querySelector<HTMLElement>(
      `[data-step="${focus.index}"]`,
    );
    // The button that moved the step if it can still move it that way, else
    // the one for the other way.
    const order =
      focus.button === "down"
        ? ["down", "up"]
        : focus.button === "up"
          ? ["up", "down"]
          : [];
    const buttons = order.map((name) =>
      row?.querySelector<HTMLButtonElement>(`[data-action="${name}"]`),
    );
    const target =
      buttons.find((button) => button && !button.disabled) ??
      row?.querySelector<HTMLElement>(".drawer-step-capture, input") ??
      root.current?.querySelector<HTMLElement>(".drawer-capture-btn");
    target?.focus();
  }, [value]);
  const steps = actionSteps(value);
  const keys = value.type === "keys" ? value : null;
  const setSteps = (next: Step[], focus?: StepFocus) => {
    if (focus) refocus.current = focus;
    onChange(stepsAction(next, value));
  };
  const addStep = (kind: NewStepKind) => {
    const before: Step[] = steps ?? [{ keys: (keys?.keys as string) ?? "" }];
    // A new wait is filled in at once; a shortcut is recorded.
    setSteps(
      [...before, newStep(kind)],
      kind === "keys" ? undefined : { index: before.length },
    );
  };
  const kinds: NewStepKind[] = ["keys", "wait"];
  const preview = steps?.every((step) => stepKind(step) === "keys") ? (
    <Keycaps keys={steps.map((step) => step.keys ?? "")} language={language} />
  ) : (
    steps && <StepsPreview steps={steps} language={language} />
  );
  return (
    <div className="drawer-keys" ref={root}>
      {steps ? (
        <div className="drawer-sequence">
          <div className="drawer-manual-head">
            <span className="field-label">
              {t("按顺序发送", "Sent in order")}
            </span>
          </div>
          <div className="drawer-sequence-preview">{preview}</div>
          {steps.map((step, index) => {
            const label = t(`第 ${index + 1} 步`, `Step ${index + 1}`);
            const kind = stepKind(step);
            const moves: StepMoves = {
              up: index > 0,
              down: index < steps.length - 1,
              onMove: (delta) => {
                setReshuffles((count) => count + 1);
                setSteps(movedStep(steps, index, delta), {
                  index: index + delta,
                  button: delta < 0 ? "up" : "down",
                });
              },
            };
            const remove = () => {
              setReshuffles((count) => count + 1);
              setSteps(
                steps.filter((_, i) => i !== index),
                { index: Math.max(0, index - 1) },
              );
            };
            const replace = (next: Step) =>
              setSteps(steps.map((entry, i) => (i === index ? next : entry)));
            if (kind === "wait")
              return (
                <WaitStep
                  // biome-ignore lint/suspicious/noArrayIndexKey: Rows are positional; a move or a removal makes them anew.
                  key={`${reshuffles}:${index}`}
                  index={index}
                  value={step.wait ?? 0}
                  onChange={(wait) => replace({ wait })}
                  moves={moves}
                  onRemove={remove}
                  language={language}
                />
              );
            return (
              <ShortcutField
                // biome-ignore lint/suspicious/noArrayIndexKey: Rows are positional; a move or a removal makes them anew, which stops a capture.
                key={`${reshuffles}:${index}`}
                variant="step"
                label={label}
                kindLabel={stepKindName("keys", language)}
                stepIndex={index}
                language={language}
                value={step.keys ?? ""}
                physical={physical}
                onChange={(next) => replace({ keys: next })}
                moves={moves}
                onRemove={remove}
              />
            );
          })}
        </div>
      ) : (
        keys && (
          <ShortcutField
            variant="full"
            label={t("快捷键", "Shortcut")}
            language={language}
            value={keys.keys as string}
            physical={physical}
            onChange={(next) => onChange({ ...keys, keys: next })}
          />
        )
      )}
      <MenuButton
        label={t("增加动作序列", "Add action sequence")}
        className="btn-ghost drawer-link"
        testId="add-step"
        align="start"
        disabled={(steps?.length ?? 1) >= maxSteps}
        items={kinds.map((kind) => ({
          label: stepKindName(kind, language),
          onSelect: () => addStep(kind),
        }))}
      >
        <Icon name="plus" size={13} stroke={2.2} />
        {t("增加动作序列", "Add action sequence")}
      </MenuButton>
    </div>
  );
}

/**
 * One shortcut and nothing else (ADR-0079 §8): the binding of a knob step,
 * which sends its key once per step, so it has no tap, hold or double tap,
 * no sequence and no script. It writes `{press: {type: "keys", keys}}`,
 * keeping the binding's label and haptic. A binding the file set otherwise
 * is named, and capturing a shortcut replaces it.
 */
export function SingleShortcutField({
  value,
  onChange,
  language,
  disabled = false,
}: {
  value: ProfileBinding;
  onChange: (binding: ProfileBinding | undefined) => void;
  language: Language;
  disabled?: boolean;
}) {
  const t = (zh: string, en: string) => editorText(language, zh, en);
  const gestures = usedGestures(value);
  const press = value.press;
  const single =
    gestures.length === 1 &&
    press?.type === "keys" &&
    typeof press.keys === "string" &&
    !value.block;
  const other = !single && (gestures.length > 0 || !!value.block);
  // What stays when the shortcut changes: the label and the haptic.
  const kept: ProfileBinding = {
    ...(value.label === undefined ? {} : { label: value.label }),
    ...(value.haptic === undefined ? {} : { haptic: value.haptic }),
  };
  const commit = (keys: string) =>
    onChange(
      keys
        ? { ...kept, press: { type: "keys", keys } }
        : Object.keys(kept).length
          ? kept
          : undefined,
    );
  return (
    <fieldset className="drawer-fieldset" disabled={disabled}>
      {other && (
        <p className="drawer-muted" data-testid="single-shortcut-other">
          {t(
            "配置文件里这一格是别的设置，录入快捷键会替换它。",
            "The profile file sets something else here. Capturing a shortcut replaces it.",
          )}
        </p>
      )}
      <ShortcutField
        variant="full"
        label={t("快捷键", "Shortcut")}
        language={language}
        value={single ? (press.keys as string) : ""}
        physical={false}
        onChange={commit}
      />
    </fieldset>
  );
}

/**
 * Picks the script (macro, SCR-50) a mapping runs from the script library
 * (SCR-51), which every profile shares. Until one is picked the draft keeps
 * its action, so choosing 脚本 without a script, or going to create one,
 * leaves no empty reference.
 */
function MacroFields({
  value,
  onChange,
  language,
}: {
  /** The mapping's macro, or null while none is picked. */
  value: ProfileMacroAction | null;
  onChange: (value: ProfileMacroAction) => void;
  language?: Language;
}) {
  const t = (zh: string, en: string) => editorText(language, zh, en);
  const { list, failing, onShowScripts } = useContext(MacroContext);
  // A file whose name the daemon refuses never builds: not offered. The
  // rest are in dictionary order of the name, whichever was created first.
  const choices = sortMacrosByName(
    (list?.macros ?? []).filter((macro) => !macro.invalidName),
  );
  const file = value?.file ?? "";
  const listed = choices.some((macro) => macro.file === file);
  const missing =
    !!file && !!list && !list.macros.some((macro) => macro.file === file);
  if (list && !choices.length && !file)
    return (
      <div className="drawer-macro-empty" data-testid="macro-empty">
        <span className="drawer-muted">{t("暂无脚本", "No scripts yet")}</span>
        {onShowScripts && (
          <button type="button" className="btn" onClick={onShowScripts}>
            {t("前往脚本页", "Go to Scripts")}
          </button>
        )}
      </div>
    );
  return (
    <div className="drawer-macro-fields">
      <select
        className="select"
        data-testid="macro-select"
        aria-label={t("选择脚本", "Choose a script")}
        value={file}
        onChange={(event) => {
          if (event.target.value)
            onChange({ type: "macro", file: event.target.value });
        }}
      >
        {!file && (
          <option value="" disabled>
            {t("选择脚本", "Choose a script")}
          </option>
        )}
        {file && !listed && <option value={file}>{macroName(file)}</option>}
        {choices.map((macro) => (
          <option key={macro.file} value={macro.file}>
            {macro.name}
          </option>
        ))}
      </select>
      {missing ? (
        <p className="drawer-warning" data-testid="macro-missing">
          <Icon name="warn" size={14} stroke={2} />
          <span>{missingMacroText(file, language)}</span>
        </p>
      ) : file && failing.has(file) ? (
        <button
          type="button"
          className="drawer-warning drawer-macro-error"
          disabled={!onShowScripts}
          onClick={onShowScripts}
        >
          <Icon name="warn" size={14} stroke={2} />
          <span>{t("此脚本有错误", "This script has an error")}</span>
        </button>
      ) : null}
    </div>
  );
}

/**
 * What a file-only action does, as a phrase that follows "当前会" / "now".
 * The English phrase is lower case; capitalise it to start a sentence.
 */
function fileOnlyActionPhrase(
  action: FileOnlyAction,
  language: Language | undefined,
  profiles: Profiles = [],
): string {
  if (action.type === "layer")
    return editorText(language, "切换按键组", "switches key groups");
  if (action.type === "script")
    return editorText(
      language,
      `运行脚本动作 ${action.name}`,
      `runs script action ${action.name}`,
    );
  if (action.type === "mouse") {
    const { x, y } = jumpPercent(action);
    return editorText(
      language,
      `把指针移到屏幕位置（${x}%，${y}%）`,
      `moves the pointer to screen position (${x}%, ${y}%)`,
    );
  }
  const name = profileName(action.profile, profiles);
  return editorText(language, `切换到「${name}」`, `switches to ${name}`);
}

/**
 * A layer action, profile switch or legacy script action from the file: the
 * editor cannot set it, so it is shown locked, kept as is when other things
 * change, and only replaced by a shortcut after a confirm.
 */
function LockedAction({
  action,
  gesture,
  onChange,
  profiles,
  language,
}: {
  action: FileOnlyAction;
  gesture: Gesture;
  onChange: (value: ProfileAction) => void;
  profiles: Profiles;
  language?: Language;
}) {
  const t = (zh: string, en: string) => editorText(language, zh, en);
  const [confirm, setConfirm] = useState(false);
  const phrase = fileOnlyActionPhrase(action, language, profiles);
  return (
    <div className="drawer-locked-action" data-testid="locked-action">
      <div className="drawer-locked">
        <Icon name="lock" size={15} stroke={2} />
        <span className="drawer-locked-text">
          <span className="drawer-locked-title">
            {t(
              `${phrase}（在配置文件中设置）`,
              `${phrase.charAt(0).toUpperCase()}${phrase.slice(1)} (set in the profile file)`,
            )}
          </span>
          <span>
            {t(
              "此动作无法在界面中修改，将原样保留。",
              "The editor cannot change this action and keeps it as is.",
            )}
          </span>
        </span>
      </div>
      <button
        type="button"
        className="btn drawer-locked-replace"
        onClick={() => setConfirm(true)}
      >
        {t("改用快捷键…", "Use a shortcut instead…")}
      </button>
      <Dialog
        open={confirm}
        title={t("改用快捷键？", "Use a shortcut instead?")}
        closeLabel={t("关闭", "Close")}
        onClose={() => setConfirm(false)}
        footer={
          <>
            <span className="spacer" />
            <button
              type="button"
              className="btn"
              onClick={() => setConfirm(false)}
            >
              {t("取消", "Cancel")}
            </button>
            <button
              type="button"
              className="btn-primary"
              onClick={() => {
                setConfirm(false);
                onChange({ type: "keys", keys: "" });
              }}
            >
              {t("改用快捷键", "Use a shortcut")}
            </button>
          </>
        }
      >
        <p className="drawer-dialog-text">
          {t(
            `此按键的${gestureName(gesture, language)}当前会${phrase}。改用快捷键后，原有设置将被替换。`,
            `This key's ${gestureName(gesture, language).toLowerCase()} now ${phrase}. A shortcut replaces it.`,
          )}
        </p>
      </Dialog>
    </div>
  );
}

/** Action types the editor can create (one app, one profile: no switching). */
type EditableType = "keys" | "macro";

/** Whether the shortcut editor shows an action: a shortcut, or a sequence it can edit. */
function isKeysLike(
  action: ProfileAction,
): action is ProfileKeysAction | ProfileSequenceAction {
  return (
    action.type === "keys" ||
    (action.type === "sequence" && !hasTextStep(action))
  );
}

/**
 * Editor of one mapping's action: a shortcut or a script (macro). Another
 * action of the file (a text, precision, key hints, a sequence with a text
 * step) works as it is but is not offered (ADR-0076): neither type is
 * selected, and choosing one replaces it. A shortcut is written held while
 * the button is held and without repeat (holdShortcut).
 */
export function ActionFields({
  value,
  onChange,
  profiles = [],
  language,
  gesture = "tap",
  physical = false,
}: {
  value: ProfileAction;
  onChange: (value: ProfileAction) => void;
  /** Library profiles, to name the target of a profile switch from the file. */
  profiles?: Profiles;
  language?: Language;
  gesture?: Gesture;
  /** Shortcut capture records physical key positions. */
  physical?: boolean;
}) {
  const t = (zh: string, en: string) => editorText(language, zh, en);
  // Switching back to a type restores what was there before the switch.
  const previous = useRef<Partial<Record<EditableType, ProfileAction>>>({});
  // 脚本 is chosen but no script yet: the draft keeps its action.
  const [choosing, setChoosing] = useState(false);
  if (isFileOnlyAction(value))
    return (
      <LockedAction
        action={value}
        gesture={gesture}
        onChange={onChange}
        profiles={profiles}
        language={language}
      />
    );
  const change = (next: ProfileAction) => onChange(holdShortcut(next));
  // "other": an action of the file that is neither, which no type selects.
  const shown: EditableType | "other" =
    value.type === "macro" || choosing
      ? "macro"
      : isKeysLike(value)
        ? "keys"
        : "other";
  // Leaving a type keeps its action, so coming back restores it.
  const remember = (action: ProfileAction) => {
    if (isKeysLike(action)) previous.current.keys = action;
    else if (action.type === "macro") previous.current.macro = action;
  };
  const switchType = (type: EditableType | "other") => {
    if (type === shown || type === "other") return;
    if (type === "keys") {
      setChoosing(false);
      if (!isKeysLike(value)) {
        remember(value);
        change(previous.current.keys ?? { type: "keys", keys: "" });
      }
      return;
    }
    remember(value);
    if (previous.current.macro) change(previous.current.macro);
    else setChoosing(true);
  };
  return (
    <div className="drawer-action">
      <Segmented
        label={t("动作类型", "Action type")}
        testId="action-type"
        value={shown}
        options={[
          { value: "keys", label: t("快捷键", "Shortcut") },
          { value: "macro", label: t("脚本", "Script") },
        ]}
        onChange={switchType}
      />
      {shown === "keys" && isKeysLike(value) ? (
        <KeysFields
          value={value}
          onChange={change}
          language={language}
          physical={physical}
        />
      ) : shown === "macro" ? (
        <MacroFields
          value={value.type === "macro" ? value : null}
          onChange={(next) => {
            setChoosing(false);
            change(next);
          }}
          language={language}
        />
      ) : null}
    </div>
  );
}

/** What each mapping does, as the menu of 添加其他动作 says. */
const mappingHints: Record<Mapping, [string, string]> = {
  tap: ["按一下", "Press once"],
  hold: ["按住一段时间后触发", "Press and keep holding"],
  doubleTap: ["连续按两次", "Press twice quickly"],
};

const timingFields = [
  ["holdMs", "触发长按时间（ms）", "Long-press delay (ms)"],
  ["multiTapMs", "连击间隔（ms）", "Multi-tap window (ms)"],
  ["tapPressMs", "点按时长（ms）", "Tap duration (ms)"],
  ["tapGapMs", "点按间隔（ms）", "Tap gap (ms)"],
] as const;

/**
 * The haptics a binding can give (MAP-25), in the order of the schema's enum:
 * the preset, then its name in Chinese and English. No preset is "".
 */
const hapticPresets = [
  ["tick", "轻点", "Tick"],
  ["confirm", "确认", "Confirm"],
  ["error", "错误", "Error"],
  ["pulse1", "1 下", "1 pulse"],
  ["pulse2", "2 下", "2 pulses"],
  ["pulse3", "3 下", "3 pulses"],
  ["pulse4", "4 下", "4 pulses"],
] as const satisfies readonly (readonly [
  NonNullable<ProfileBinding["haptic"]>,
  string,
  string,
])[];

export interface BindingFieldsProps {
  value: ProfileBinding;
  onChange: (value: ProfileBinding) => void;
  /** Effective profile timing; the binding's own timing applies on top. */
  timing?: ProfileTiming;
  profiles?: Profiles;
  actions?: Actions;
  language?: Language;
  disabled?: boolean;
  /** Script registrations on this control: they can add waits. */
  registrations?: DelaySource[];
  /** Show a ready, empty 点按 when the binding has no mapping at all (default). */
  suggestPress?: boolean;
  /** Shown between the mappings and the advanced section. */
  children?: ReactNode;
  /** Extra content at the end of the advanced section. */
  advanced?: ReactNode;
}

/**
 * The mappings of one binding (ADR-0076): 点按, 长按 and 双击, one card each
 * with only one open, plus the binding's own advanced settings (ADR-0045).
 * The file's gesture of 点按 is a press when it is the only mapping and a tap
 * when the key also has 长按 or 双击: adding or removing one of those moves it
 * (settleTap). A mapping is deleted for good, the last one too; the key then
 * has none, and only 添加其他动作 is left. A key with nothing at all opens with an
 * empty 点按, ready to record, until that is deleted.
 */
export function BindingFields({
  value,
  onChange,
  profiles = [],
  actions = [],
  language,
  timing = {},
  disabled = false,
  registrations = [],
  suggestPress = true,
  children,
  advanced,
}: BindingFieldsProps) {
  const t = (zh: string, en: string) => editorText(language, zh, en);
  const id = useId();
  const missing = missingMacros(useContext(MacroContext).list);
  // A blocked binding cannot carry actions (the daemon rejects it), so no
  // mapping is offered until 取消屏蔽 under 高级.
  const blocked = !!value.block;
  // The last mapping was deleted: the empty 点按 does not come back.
  const [dismissed, setDismissed] = useState(false);
  // What the key opens with when nothing is set: only a key with no gesture
  // at all, not one whose file has a mapping the drawer does not offer.
  const suggested: Mapping | null =
    suggestPress && !blocked && !dismissed && usedGestures(value).length === 0
      ? "tap"
      : null;
  // A mapping picked from the menu stays a draft until it is first edited.
  const [pending, setPending] = useState<Mapping | null>(null);
  // The gesture of the file that holds a mapping, or would hold it once set.
  const gestureOf = (mapping: Mapping): Gesture =>
    mappingGesture(value, mapping) ??
    (mapping === "tap" ? tapGestureFor(value) : mapping);
  const has = (mapping: Mapping) => !!mappingGesture(value, mapping);
  const draft = pending && !has(pending) ? pending : suggested;
  // undefined: open the first card; null: the user closed every card.
  const [open, setOpen] = useState<Mapping | null | undefined>(undefined);
  // Shortcut capture records physical key positions (高级, ADR-0045).
  const [physical, setPhysical] = useState(false);
  // A file-only action (layer action, profile switch) cannot be set again in
  // the editor: removing it asks first.
  const [confirmRemove, setConfirmRemove] = useState<Mapping | null>(null);
  const confirmAction = confirmRemove
    ? value[gestureOf(confirmRemove)]
    : undefined;
  const list = useRef<HTMLElement>(null);
  const refocusAdd = useRef(false);
  // A removed mapping card took the focused button with it: move focus to
  // 添加其他动作 (or the first remaining card) once the change is rendered.
  // biome-ignore lint/correctness/useExhaustiveDependencies: runs after a removal renders
  useEffect(() => {
    if (!refocusAdd.current) return;
    refocusAdd.current = false;
    const root = list.current;
    (
      root?.querySelector<HTMLElement>(
        '[data-testid="add-gesture"]:not(:disabled)',
      ) ?? root?.querySelector<HTMLElement>(".drawer-gesture-head")
    )?.focus();
  }, [value, pending, dismissed]);
  const shown = mappings.filter((mapping) => has(mapping) || mapping === draft);
  const current =
    open === null ? null : open && shown.includes(open) ? open : shown[0];
  const effective = resolveTiming(timing);
  const typeOf = (mapping: Mapping) =>
    value[gestureOf(mapping)]?.type ?? "keys";
  const keysShown = shown.some((mapping) => typeOf(mapping) === "keys");
  // Latch only holds a shortcut's keys: a key whose mappings all run
  // scripts does not offer it (a value set in the file is kept).
  const macrosOnly =
    shown.length > 0 && shown.every((mapping) => typeOf(mapping) === "macro");
  const configured = [
    ...(value.timing && Object.values(value.timing).some((v) => v !== undefined)
      ? [t("判定时间", "Timing")]
      : []),
    ...(value.policy ? [t("打断判定", "Interruptions")] : []),
    ...(value.latch ? [t("锁存", "Latch")] : []),
    ...(value.haptic && !blocked ? [t("触感", "Haptic")] : []),
    ...(physical && keysShown ? [t("映射扫描码", "Map scan code")] : []),
    ...(value.block ? [t("屏蔽", "Block")] : []),
  ];
  const remove = (mapping: Mapping) => {
    const next = { ...value };
    delete next[gestureOf(mapping)];
    // Without a 长按 or 双击 the 点按 is a press again.
    const settled = mapping === "tap" ? next : settleTap(next);
    if (usedGestures(settled).length === 0) setDismissed(true);
    refocusAdd.current = true;
    setOpen(undefined);
    onChange(settled);
  };
  const updateTiming = (key: keyof ProfileTiming, next: number | undefined) => {
    const nextTiming: ProfileTiming = { ...value.timing };
    if (next === undefined) delete nextTiming[key];
    else nextTiming[key] = next;
    const result: ProfileBinding = { ...value, timing: nextTiming };
    if (!Object.keys(nextTiming).length) delete result.timing;
    onChange(result);
  };
  const menu: MenuEntry[] = mappings
    .filter((mapping) => !has(mapping) && mapping !== draft)
    .map((mapping) => ({
      label: gestureName(mapping, language),
      hint: t(...mappingHints[mapping]),
      onSelect: () => {
        setPending(mapping);
        setOpen(mapping);
      },
    }));
  return (
    <>
      <section
        ref={list}
        className="drawer-section"
        aria-label={t("映射", "Mappings")}
        data-testid="gesture-list"
      >
        <div className="drawer-section-head">
          <span className="field-label">{t("映射", "Mappings")}</span>
          <MenuButton
            label={t("添加其他动作", "Add another action")}
            testId="add-gesture"
            className="btn-ghost drawer-add"
            disabled={disabled || blocked || menu.length === 0}
            items={menu}
          >
            <Icon name="plus" size={13} stroke={2.2} />
            {t("添加其他动作", "Add another action")}
          </MenuButton>
        </div>
        {blocked && shown.length === 0 && (
          <p className="drawer-empty">
            {t(
              "此按键已屏蔽。如需设置映射，请先在“高级”中取消屏蔽。",
              "This key is blocked. To set mappings, unblock it under Advanced first.",
            )}
          </p>
        )}
        {shown.map((mapping) => {
          const gesture = gestureOf(mapping);
          const action: ProfileAction = value[gesture] ?? {
            type: "keys",
            keys: "",
          };
          const isDraft = !value[gesture];
          const delay = isDraft
            ? {}
            : delayNote(gesture, value, timing, language, registrations);
          return (
            <GestureCard
              key={mapping}
              gesture={gesture}
              open={current === mapping}
              onToggle={() => setOpen(current === mapping ? null : mapping)}
              summary={gestureSummary(
                action,
                language,
                actions,
                profiles,
                missing,
              )}
              delay={delay}
              language={language}
              bodyId={`${id}-${mapping}`}
            >
              <fieldset className="drawer-fieldset" disabled={disabled}>
                <ActionFields
                  gesture={gesture}
                  value={action}
                  profiles={profiles}
                  language={language}
                  physical={physical}
                  onChange={(next) => {
                    const written = { ...value, [gesture]: next };
                    // A new 长按 or 双击 turns a press into a tap.
                    onChange(
                      isDraft && mapping !== "tap"
                        ? settleTap(written)
                        : written,
                    );
                    if (isDraft) setPending(null);
                    setOpen(mapping);
                  }}
                />
                {delay.text && (
                  <p className="drawer-hint" data-testid="gesture-delay">
                    <Icon name="info" size={14} stroke={2} />
                    <span>{delay.text}</span>
                  </p>
                )}
                <button
                  type="button"
                  className="btn-ghost drawer-remove"
                  onClick={() => {
                    if (isDraft) {
                      // Nothing is in the file: only the draft goes. The
                      // empty 点按 of a key with nothing set stays gone.
                      if (mapping === suggested) setDismissed(true);
                      refocusAdd.current = true;
                      setOpen(undefined);
                      setPending(null);
                      return;
                    }
                    if (isFileOnlyAction(action)) setConfirmRemove(mapping);
                    else remove(mapping);
                  }}
                >
                  <Icon name="trash" size={14} />
                  {t("删除此映射", "Remove this mapping")}
                </button>
              </fieldset>
            </GestureCard>
          );
        })}
      </section>
      {children}
      <div className="drawer-advanced">
        <Disclosure
          title={t("高级", "Advanced")}
          summary={
            configured.length
              ? t(
                  `已设置：${configured.join("、")}`,
                  `Set: ${configured.join(", ")}`,
                )
              : macrosOnly
                ? t("判定时间 · 触感", "Timing · Haptic")
                : t("判定时间 · 锁存 · 触感", "Timing · Latch · Haptic")
          }
          defaultOpen={!!value.block}
        >
          <fieldset className="drawer-fieldset" disabled={disabled}>
            <div className="drawer-grid">
              {timingFields.map(([key, zh, en]) => (
                <NumberField
                  key={key}
                  language={language}
                  label={t(zh, en)}
                  value={value.timing?.[key]}
                  placeholder={String(effective[key])}
                  onChange={(next) => updateTiming(key, next)}
                />
              ))}
            </div>
            <p className="drawer-muted">
              {t(
                "仅对此按键生效；留空时使用配置的默认值。",
                "Only for this key. Leave empty to use the profile default.",
              )}
            </p>
            {(value.hold || value.policy) && (
              <label className="field">
                <span className="field-label">
                  {t(
                    "判定长按前按下其他按键时",
                    "When another key is pressed before a hold",
                  )}
                </span>
                <select
                  className="select"
                  value={value.policy ?? ""}
                  onChange={(event) => {
                    const next: ProfileBinding = { ...value };
                    if (event.target.value)
                      next.policy = event.target
                        .value as ProfileBinding["policy"];
                    else delete next.policy;
                    onChange(next);
                  }}
                >
                  <option value="">{t("自动", "Automatic")}</option>
                  <option value="holdPreferred">
                    {t("偏向长按", "Prefer hold")}
                  </option>
                  <option value="balanced">{t("平衡", "Balanced")}</option>
                  <option value="timeOnly">{t("仅按时间", "Time only")}</option>
                </select>
              </label>
            )}
            {keysShown && (
              <div className="drawer-option">
                <span className="drawer-option-text">
                  <span className="drawer-option-label">
                    {t("映射扫描码", "Map scan code")}
                  </span>
                  <span className="drawer-option-hint">
                    {t(
                      "录入快捷键时记录按键在键盘上的位置，不受输入法或键盘布局影响。",
                      "Capturing a shortcut records where each key is on the keyboard, independent of the input method or layout.",
                    )}
                  </span>
                </span>
                <Switch
                  label={t("映射扫描码", "Map scan code")}
                  checked={physical}
                  onChange={setPhysical}
                />
              </div>
            )}
            {!macrosOnly && (
              <div className="drawer-option">
                <span className="drawer-option-text">
                  <span className="drawer-option-label">
                    {t("锁存", "Latch")}
                  </span>
                  <span className="drawer-option-hint">
                    {t(
                      "按一次保持按下，再按一次松开",
                      "Press once to keep it down, again to let go",
                    )}
                  </span>
                </span>
                <Switch
                  label={t("锁存", "Latch")}
                  checked={value.latch ?? false}
                  onChange={(checked) => {
                    const next: ProfileBinding = { ...value };
                    if (checked) next.latch = true;
                    else delete next.latch;
                    onChange(next);
                  }}
                />
              </div>
            )}
            {!blocked && (
              <label className="field">
                <span className="field-label">{t("触感", "Haptic")}</span>
                <select
                  className="select"
                  value={value.haptic ?? ""}
                  onChange={(event) => {
                    const next: ProfileBinding = { ...value };
                    if (event.target.value)
                      next.haptic = event.target.value as NonNullable<
                        ProfileBinding["haptic"]
                      >;
                    else delete next.haptic;
                    onChange(next);
                  }}
                >
                  <option value="">{t("无", "None")}</option>
                  {hapticPresets.map(([preset, zh, en]) => (
                    <option key={preset} value={preset}>
                      {t(zh, en)}
                    </option>
                  ))}
                </select>
              </label>
            )}
            {value.block && (
              <div className="drawer-option" data-testid="binding-block">
                <span className="drawer-option-text">
                  <span className="drawer-option-label">
                    {t("屏蔽（在配置文件中设置）", "Blocked (set in the file)")}
                  </span>
                  <span className="drawer-option-hint">
                    {t(
                      "仅使用一组按键时，屏蔽不起作用。",
                      "With a single key group, blocking has no effect.",
                    )}
                  </span>
                </span>
                <button
                  type="button"
                  className="btn"
                  onClick={() => {
                    const next: ProfileBinding = { ...value };
                    delete next.block;
                    // This button goes away; 添加其他动作 is the next step.
                    refocusAdd.current = true;
                    onChange(next);
                  }}
                >
                  {t("取消屏蔽", "Unblock")}
                </button>
              </div>
            )}
            {advanced}
          </fieldset>
        </Disclosure>
      </div>
      <Dialog
        open={confirmRemove !== null}
        title={t("删除此映射？", "Remove this mapping?")}
        closeLabel={t("关闭", "Close")}
        onClose={() => setConfirmRemove(null)}
        footer={
          <>
            <span className="spacer" />
            <button
              type="button"
              className="btn"
              onClick={() => setConfirmRemove(null)}
            >
              {t("取消", "Cancel")}
            </button>
            <button
              type="button"
              className="btn-primary"
              disabled={disabled}
              onClick={() => {
                const mapping = confirmRemove;
                setConfirmRemove(null);
                if (mapping && !disabled && has(mapping)) remove(mapping);
              }}
            >
              {t("删除", "Remove")}
            </button>
          </>
        }
      >
        <p className="drawer-dialog-text">
          {confirmRemove &&
            isFileOnlyAction(confirmAction) &&
            t(
              `此按键的${gestureName(confirmRemove, language)}当前会${fileOnlyActionPhrase(confirmAction, language, profiles)}。删除后无法在界面中重新设置。`,
              `This key's ${gestureName(confirmRemove, language).toLowerCase()} now ${fileOnlyActionPhrase(confirmAction, language, profiles)}. The editor cannot set it again once removed.`,
            )}
        </p>
      </Dialog>
    </>
  );
}
