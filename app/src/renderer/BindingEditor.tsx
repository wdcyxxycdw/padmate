import {
  type KeyboardEvent,
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
  ProfileTextAction,
  ProfileTiming,
} from "../shared/generated/schema";
import { holdCapture } from "./capture-hold";
import {
  canHoldAction,
  editorError,
  type FileOnlyAction,
  type Gesture,
  gestureName,
  gestures,
  hasDepth,
  hintsAction,
  isFileOnlyAction,
  isHintsAction,
  isKnobTick,
  isPrecisionAction,
  isReservedShortcut,
  jumpPercent,
  macroName,
  missingMacroText,
  type PrecisionAction,
  precisionAction,
  precisionPercent,
  precisionPercentRange,
  profileName,
  resolveTiming,
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
  TextBox,
  TextStep,
  WaitStep,
} from "./StepEditor";
import {
  actionSteps,
  maxSteps,
  movedStep,
  newStep,
  type Step,
  type StepKind,
  stepKind,
  stepsAction,
} from "./sequence-model";
import {
  endsInMouseButton,
  type MouseKey,
  withMouseKey,
  withoutHeldButtonRepeat,
} from "./shortcut-keys";
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
export function gestureText(gesture: Gesture, language: Language | undefined) {
  return gestureName(gesture, language);
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

type MouseChoice = readonly [key: MouseKey, zh: string, en: string];
/** The mouse menu of a shortcut field: the buttons, then the wheel (ADR-0062). */
const buttonChoices: readonly MouseChoice[] = [
  ["MouseLeft", "左键", "Left button"],
  ["MouseRight", "右键", "Right button"],
  ["MouseMiddle", "中键", "Middle button"],
  ["MouseX1", "侧键 1", "Side button 1"],
  ["MouseX2", "侧键 2", "Side button 2"],
];
const wheelChoices: readonly MouseChoice[] = [
  ["WheelUp", "滚轮向上", "Wheel up"],
  ["WheelDown", "滚轮向下", "Wheel down"],
  ["WheelLeft", "滚轮向左", "Wheel left"],
  ["WheelRight", "滚轮向右", "Wheel right"],
];

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
 * One chord, set by native capture only (ADR-0043): shortcuts are recorded,
 * never typed (OUT-18). The capture session is owned by this field: it
 * stops on stop, when focus leaves the field and on unmount. A capture shows
 * each chord as the keys go down (Ctrl, Ctrl+Shift, Ctrl+Shift+Z) and
 * commits the last one when it stops, so a capture is one edit; removing the
 * step or unmounting drops it. The 鼠标 menu is the other way in: it picks a
 * mouse button or wheel key, which a keyboard cannot record, and keeps the
 * modifiers of the chord, so Ctrl+Z becomes Ctrl+WheelUp. It is one edit too.
 *
 * The keyboard's way out is to hold Esc for ESC_HOLD_MS (UI-24). The helper
 * only watches (it never swallows a key), so this window gets the Esc press
 * and its release as ordinary key events, and times the hold itself, from
 * the first press (the repeat of a held key can start after a second). The
 * press that is held is not a shortcut: the one before it is kept, or
 * nothing changes when there is none. A short Esc is still Esc.
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
  const pending = useRef<string | null>(null);
  // The chords of the running capture, oldest first; esc: a bare Esc press.
  const heard = useRef<{ chord: string; esc: boolean }[]>([]);
  // The running hold of Esc, and whether one that ended the capture is still down.
  const hold = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const heldAfter = useRef(false);
  const clearHold = useCallback(() => {
    if (hold.current === undefined) return;
    clearTimeout(hold.current);
    hold.current = undefined;
  }, []);
  const finish = () => {
    const chord = pending.current;
    pending.current = null;
    heard.current = [];
    clearHold();
    setCaptured(null);
    if (chord !== null && chord !== latest.current.value)
      latest.current.onChange(chord);
  };
  const discard = () => {
    pending.current = null;
    heard.current = [];
    clearHold();
    setCaptured(null);
  };
  // Esc held for ESC_HOLD_MS: the capture ends, without the Esc that is held.
  const endByHold = () => {
    hold.current = undefined;
    heldAfter.current = true;
    if (heard.current.at(-1)?.esc) heard.current.pop();
    pending.current = heard.current.at(-1)?.chord ?? null;
    finish();
    setActive(false);
  };
  const latest = useRef({ value, onChange, physical, finish, language });
  latest.current = { value, onChange, physical, finish, language };
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
        pending.current = chord;
        heard.current.push({ chord, esc: event.chord === "Esc" });
        setCaptured(chord);
      }
      if (!event.active)
        setError(
          captureEndNote(
            event.reason,
            latest.current.language,
            Date.now() - startedAt,
          ),
        );
      // The capture ended by itself (the window lost focus, …).
      if (!event.active) latest.current.finish();
      setActive(event.active);
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
  }, [active]);
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
  const toggle = () => {
    setError("");
    heldAfter.current = false;
    if (active) finish();
    setActive(!active);
    // Keys pressed during the capture reach the field here (see swallow).
    button.current?.focus();
  };
  const clear = () => {
    setError("");
    onChange("");
    button.current?.focus();
  };
  // Not offered during a capture, which owns the chord until it stops.
  const mouseItem = ([mouse, zh, en]: MouseChoice): MenuEntry => ({
    label: t(zh, en),
    onSelect: () => {
      setError("");
      const next = withMouseKey(value, mouse);
      if (next !== value) onChange(next);
    },
  });
  const mouseItems: MenuEntry[] = [
    ...buttonChoices.map(mouseItem),
    { kind: "separator" },
    ...wheelChoices.map(mouseItem),
  ];
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
    } else if (event.key === "Escape") clearHold();
  };
  const captureLabel = active
    ? t("停止捕获", "Stop capture")
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
        if (active && !field.current?.contains(event.relatedTarget as Node)) {
          finish();
          setActive(false);
        }
        if (!field.current?.contains(event.relatedTarget as Node))
          heldAfter.current = false;
      }}
    >
      {variant === "full" ? (
        <div className="drawer-capture">
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
            <MenuButton
              label={t("鼠标", "Mouse")}
              className="btn drawer-mouse"
              testId="shortcut-mouse"
              disabled={active}
              items={mouseItems}
            >
              <Icon name="mouse" size={14} />
              {t("鼠标", "Mouse")}
            </MenuButton>
            <button
              ref={button}
              type="button"
              className="btn drawer-capture-btn"
              onClick={toggle}
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
          <span className="drawer-step-keys">{keys}</span>
          <MenuButton
            label={t("鼠标", "Mouse")}
            icon="mouse"
            className="icon-btn bare"
            testId="shortcut-mouse"
            disabled={active}
            items={mouseItems}
          />
          <button
            ref={button}
            type="button"
            className="icon-btn bare drawer-step-capture"
            aria-label={captureLabel}
            title={captureLabel}
            onClick={toggle}
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
                    setActive(false);
                    discard();
                    moves.onMove(delta);
                  },
                }
              }
              onRemove={() => {
                // Steps are positional: end this slot's capture before the
                // next step moves into it.
                setActive(false);
                discard();
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
              "请按下快捷键。点击“停止捕获”、按住 Esc 1 秒或切换到其他窗口即结束录入。",
              "Press a shortcut. Stop, hold Esc for 1 second or switch windows to finish.",
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

/** The rate of repeat when a repeat does not set one, as in the daemon. */
const defaultRateHz = 12;
/** What 推得越深越快 starts the slowest rate at. */
const defaultMinRateHz = 3;

/**
 * The timing of a repeat. On a stick, trigger or half-axis control the rate
 * can follow how far the control is pushed (ANA-12, ANA-15): the two rate
 * fields are then the slowest, at the control's threshold, and the fastest,
 * at full travel, and the slowest may not exceed the fastest.
 */
function RepeatFields({
  value,
  repeat,
  onChange,
  language,
  depth,
}: {
  value: ProfileKeysAction;
  repeat: NonNullable<ProfileKeysAction["repeat"]>;
  onChange: (value: ProfileKeysAction) => void;
  language?: Language;
  /** The control follows how far it is pushed: minRateHz is offered. */
  depth: boolean;
}) {
  const t = (zh: string, en: string) => editorText(language, zh, en);
  const rate = repeat.rateHz ?? defaultRateHz;
  const slowest = repeat.minRateHz;
  // A profile that already sets minRateHz keeps showing it, whatever the
  // control: the daemon would refuse it and the diagnostics say so.
  const deeper = slowest !== undefined;
  const setRepeat = (next: NonNullable<ProfileKeysAction["repeat"]>) =>
    onChange({ ...value, repeat: next });
  const delay = (
    <NumberField
      language={language}
      label={t("开始前等待（ms）", "Delay (ms)")}
      value={repeat.delayMs}
      onChange={(delayMs) => setRepeat({ ...repeat, delayMs })}
    />
  );
  return (
    <>
      {(depth || deeper) && (
        <div className="drawer-option">
          <span className="drawer-option-text">
            <span className="drawer-option-label">
              {t("推得越深越快", "Faster when pushed deeper")}
            </span>
          </span>
          <Switch
            label={t("推得越深越快", "Faster when pushed deeper")}
            checked={deeper}
            onChange={(checked) => {
              const { minRateHz: _minRateHz, ...rest } = repeat;
              setRepeat(
                checked
                  ? { ...rest, minRateHz: Math.min(defaultMinRateHz, rate) }
                  : rest,
              );
            }}
          />
        </div>
      )}
      {deeper ? (
        <>
          <div className="drawer-grid">{delay}</div>
          <div className="drawer-grid">
            <NumberField
              language={language}
              label={t("最慢（每秒）", "Slowest (per second)")}
              value={slowest}
              min={1}
              max={rate}
              // Empty is not "off": the switch above turns the range off.
              optional={false}
              onChange={(minRateHz) => setRepeat({ ...repeat, minRateHz })}
            />
            <NumberField
              language={language}
              label={t("最快（每秒）", "Fastest (per second)")}
              value={repeat.rateHz}
              placeholder={String(defaultRateHz)}
              min={slowest}
              max={1000}
              onChange={(rateHz) =>
                // Clearing it leaves the default, unless that is below the
                // slowest rate.
                setRepeat({
                  ...repeat,
                  rateHz:
                    rateHz === undefined && slowest > defaultRateHz
                      ? slowest
                      : rateHz,
                })
              }
            />
          </div>
        </>
      ) : (
        <>
          <div className="drawer-grid">
            {delay}
            <NumberField
              language={language}
              label={t("每秒次数", "Per second")}
              value={repeat.rateHz}
              min={1}
              max={1000}
              onChange={(rateHz) => setRepeat({ ...repeat, rateHz })}
            />
          </div>
          <button
            type="button"
            className="btn-ghost drawer-link"
            title={t("立即开始，每秒 12 次", "Starts at once, 12 per second")}
            // The turbo preset (MAP-23): OUT-11's {style: "tap", delayMs: 0} at
            // the default rate. It only fills the values. A mode that was
            // chosen becomes tap, which a turbo is.
            onClick={() =>
              onChange({
                ...value,
                ...(value.mode ? { mode: "tap" as const } : {}),
                repeat: {
                  ...repeat,
                  style: "tap",
                  delayMs: 0,
                  rateHz: defaultRateHz,
                },
              })
            }
          >
            {t("连点", "Turbo")}
          </button>
        </>
      )}
    </>
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
 * of them that taps, as before; a wait or a text among them makes it a
 * sequence action. A sequence is tapped and has no hold or repeat.
 */
function KeysFields({
  value,
  onChange,
  language,
  gesture,
  physical,
  depth,
  tick,
}: {
  value: ProfileKeysAction | ProfileSequenceAction;
  onChange: (value: ProfileKeysAction | ProfileSequenceAction) => void;
  language?: Language;
  gesture: Gesture;
  physical: boolean;
  /** The control follows how far a stick or trigger is pushed. */
  depth: boolean;
  /** The control is a knob tick: it has nothing to repeat. */
  tick: boolean;
}) {
  const t = (zh: string, en: string) => editorText(language, zh, en);
  const root = useRef<HTMLDivElement>(null);
  // Counts the moves and removals of steps. A row belongs to its place, not to
  // a step: what is typed in it and not committed (a text too long, a wait
  // that is no number) and a running capture are the row's own state, and
  // must not move into the step that takes its place. So the rows are made
  // anew when a step moves or goes.
  const [reshuffles, setReshuffles] = useState(0);
  // After a step is removed or moved, or a wait or a text is added, focus goes
  // to a row of the steps (the one before a removed step, or the single field).
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
      row?.querySelector<HTMLElement>(
        ".drawer-step-capture, input, textarea",
      ) ??
      root.current?.querySelector<HTMLElement>(".drawer-capture-btn");
    target?.focus();
  }, [value]);
  const steps = actionSteps(value);
  const keys = value.type === "keys" ? value : null;
  const canHold = gesture === "press" || gesture === "hold";
  // Without a mode, a tap repeat style taps, as in the daemon (isHoldAction).
  const outputMode =
    keys?.mode ?? (canHold && keys?.repeat?.style !== "tap" ? "hold" : "tap");
  // A held mouse button is never pressed again and again (ADR-0062): it
  // repeats as clicks, which are tap mode, so hold and repeat exclude each other.
  const button = !!keys && !steps && endsInMouseButton(keys.keys as string);
  const setSteps = (next: Step[], focus?: StepFocus) => {
    if (focus) refocus.current = focus;
    onChange(stepsAction(next, value));
  };
  const addStep = (kind: StepKind) => {
    const before: Step[] = steps ?? [{ keys: (keys?.keys as string) ?? "" }];
    // A new wait or text is filled in at once; a shortcut is recorded.
    setSteps(
      [...before, newStep(kind)],
      kind === "keys" ? undefined : { index: before.length },
    );
  };
  const kinds: StepKind[] = ["keys", "wait", "text"];
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
            if (kind === "text")
              return (
                <TextStep
                  // biome-ignore lint/suspicious/noArrayIndexKey: Rows are positional; a move or a removal makes them anew.
                  key={`${reshuffles}:${index}`}
                  index={index}
                  value={step.text ?? ""}
                  onChange={(text) => replace({ text })}
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
            onChange={(next) =>
              onChange(
                withoutHeldButtonRepeat({ ...keys, keys: next }, canHold),
              )
            }
          />
        )
      )}
      {keys && canHold && !steps && (
        <>
          <div className="drawer-option" data-wrap="true">
            <span className="drawer-option-text">
              <span className="drawer-option-label">
                {t("发送方式", "Sending")}
              </span>
            </span>
            <Segmented
              small
              label={t("发送方式", "Sending")}
              value={outputMode}
              options={[
                {
                  value: "hold",
                  label: t("按住保持", "Hold with button"),
                  title: t(
                    "按住手柄按键期间，快捷键保持按下",
                    "The key stays down while the button is held",
                  ),
                },
                {
                  value: "tap",
                  label: t("单次点按", "Tap once"),
                  title: t(
                    "只发送一次按下和松开",
                    "Sends one key press and release",
                  ),
                },
              ]}
              onChange={(mode) => {
                if (button && mode === "hold") {
                  // Holding the button ends the repeat, which would be clicks.
                  const { repeat: _repeat, ...rest } = keys;
                  onChange({ ...rest, mode });
                  return;
                }
                onChange({
                  ...keys,
                  mode,
                  ...(keys.repeat
                    ? {
                        repeat: {
                          ...keys.repeat,
                          style: mode === "hold" ? "typematic" : "tap",
                        },
                      }
                    : {}),
                });
              }}
            />
          </div>
          {(!tick || keys.repeat) && (
            <div className="drawer-option">
              <span className="drawer-option-text">
                <span className="drawer-option-label">
                  {t("连发", "Repeat")}
                </span>
                <span className="drawer-option-hint">
                  {t("按住时重复发送", "Repeats while held")}
                </span>
              </span>
              <Switch
                label={t("连发", "Repeat")}
                checked={!!keys.repeat}
                onChange={(checked) => {
                  const next = { ...keys };
                  if (checked) {
                    next.repeat = {
                      style:
                        outputMode === "hold" && !button ? "typematic" : "tap",
                      delayMs: 350,
                      rateHz: 12,
                    };
                    if (button) next.mode = "tap";
                  } else delete next.repeat;
                  onChange(next);
                }}
              />
            </div>
          )}
          {keys.repeat && (
            <RepeatFields
              value={keys}
              repeat={keys.repeat}
              onChange={onChange}
              language={language}
              depth={depth}
            />
          )}
        </>
      )}
      <MenuButton
        label={t("添加下一步", "Add a next step")}
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
        {t("添加下一步", "Add a next step")}
      </MenuButton>
    </div>
  );
}

/** The text a gesture types (OUT-21): any gesture can, since it holds nothing. */
function TextFields({
  value,
  onChange,
  language,
}: {
  value: ProfileTextAction;
  onChange: (value: ProfileTextAction) => void;
  language?: Language;
}) {
  const t = (zh: string, en: string) => editorText(language, zh, en);
  return (
    <div className="drawer-text-fields" data-testid="text-fields">
      <TextBox
        value={value.text}
        onCommit={(text) => onChange({ ...value, text })}
        label={t("输入的文字", "Text to type")}
        language={language}
        rows={4}
      />
    </div>
  );
}

/**
 * Picks the script (macro, SCR-50) a gesture runs from the script library
 * (SCR-51), which every profile shares. Until one is picked the draft keeps
 * its action, so choosing 脚本 without a script, or going to create one,
 * leaves no empty reference.
 */
function MacroFields({
  value,
  onChange,
  language,
}: {
  /** The gesture's macro, or null while none is picked. */
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

/**
 * The factor of a precision action (MOU-05), as a percentage: while the
 * gesture is held, the sticks that move the pointer move at this share of
 * their speed.
 */
function PrecisionFields({
  value,
  onChange,
  language,
}: {
  value: PrecisionAction;
  onChange: (value: PrecisionAction) => void;
  language?: Language;
}) {
  const t = (zh: string, en: string) => editorText(language, zh, en);
  return (
    <div className="drawer-grid" data-testid="precision-fields">
      <NumberField
        language={language}
        label={t("降为原速", "Speed while held")}
        unit="%"
        value={precisionPercent(value)}
        min={precisionPercentRange.min}
        max={precisionPercentRange.max}
        // Empty is not "default": the field always holds a percentage.
        optional={false}
        onChange={(percent) => {
          if (percent !== undefined)
            onChange({ ...value, factor: percent / 100 });
        }}
      />
    </div>
  );
}

/** Action types the editor can create (one app, one profile: no switching). */
type EditableType = "keys" | "text" | "macro" | "precision" | "hints";

/** Editor of one gesture's action: a shortcut or a script (macro). */
export function ActionFields({
  value,
  onChange,
  profiles = [],
  language,
  gesture = "press",
  physical = false,
  depth = false,
  tick = false,
}: {
  value: ProfileAction;
  onChange: (value: ProfileAction) => void;
  /** Library profiles, to name the target of a profile switch from the file. */
  profiles?: Profiles;
  language?: Language;
  gesture?: Gesture;
  /** Shortcut capture records physical key positions. */
  physical?: boolean;
  /** The control follows how far a stick or trigger is pushed (ANA-12). */
  depth?: boolean;
  /** The control is a knob tick (ANA-11): nothing to repeat. */
  tick?: boolean;
}) {
  const t = (zh: string, en: string) => editorText(language, zh, en);
  // Switching back to a type restores what was there before the switch.
  const previous = useRef<Partial<Record<EditableType, ProfileAction>>>({});
  // 脚本 is chosen but no script yet: the draft keeps its shortcut.
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
  const precision = isPrecisionAction(value);
  const hints = isHintsAction(value);
  const shown: EditableType =
    value.type === "macro" || choosing
      ? "macro"
      : value.type === "text"
        ? "text"
        : precision
          ? "precision"
          : hints
            ? "hints"
            : "keys";
  // Leaving a type keeps its action, so coming back restores it.
  const remember = (action: ProfileAction) => {
    if (action.type === "keys" || action.type === "sequence")
      previous.current.keys = action;
    else if (action.type === "text") previous.current.text = action;
    else if (action.type === "macro") previous.current.macro = action;
    else if (isPrecisionAction(action)) previous.current.precision = action;
    else if (isHintsAction(action)) previous.current.hints = action;
  };
  const switchType = (type: EditableType) => {
    if (type === shown) return;
    if (type === "keys") {
      setChoosing(false);
      if (value.type !== "keys" && value.type !== "sequence") {
        remember(value);
        onChange(previous.current.keys ?? { type: "keys", keys: "" });
      }
      return;
    }
    if (type === "text") {
      setChoosing(false);
      if (value.type !== "text") {
        remember(value);
        onChange(previous.current.text ?? { type: "text", text: "" });
      }
      return;
    }
    if (type === "precision") {
      setChoosing(false);
      // Choosing a script left the precision action in the draft.
      if (precision) return;
      remember(value);
      onChange(previous.current.precision ?? precisionAction());
      return;
    }
    if (type === "hints") {
      setChoosing(false);
      // Choosing a script left the hints action in the draft.
      if (hints) return;
      remember(value);
      onChange(previous.current.hints ?? hintsAction());
      return;
    }
    remember(value);
    if (previous.current.macro) onChange(previous.current.macro);
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
          { value: "text", label: t("输入文字", "Type text") },
          { value: "macro", label: t("脚本", "Script") },
          // Precision and key hints are held with the gesture, so only press
          // and hold offer them (a file's action on another gesture stays
          // listed, as the one that is set).
          ...(canHoldAction(gesture) || precision
            ? [
                {
                  value: "precision" as const,
                  label: t("精确模式", "Precision"),
                },
              ]
            : []),
          ...(canHoldAction(gesture) || hints
            ? [
                {
                  value: "hints" as const,
                  label: t("按键提示", "Key hints"),
                },
              ]
            : []),
        ]}
        onChange={switchType}
      />
      {shown === "precision" && isPrecisionAction(value) ? (
        <PrecisionFields
          value={value}
          onChange={onChange}
          language={language}
        />
      ) : shown === "text" && value.type === "text" ? (
        <TextFields value={value} onChange={onChange} language={language} />
      ) : shown === "keys" &&
        (value.type === "keys" || value.type === "sequence") ? (
        <KeysFields
          value={value}
          onChange={onChange}
          language={language}
          gesture={gesture}
          physical={physical}
          depth={depth}
          tick={tick}
        />
      ) : shown === "hints" ? null : (
        <MacroFields
          value={value.type === "macro" ? value : null}
          onChange={(next) => {
            setChoosing(false);
            onChange(next);
          }}
          language={language}
        />
      )}
    </div>
  );
}

const gestureHints: Record<Gesture, [string, string]> = {
  press: ["按下时立即触发", "Fires as soon as you press"],
  release: ["松开时触发", "Fires when you let go"],
  tap: ["短按后松开时触发", "Press and let go quickly"],
  hold: ["按住一段时间后触发", "Press and keep holding"],
  doubleTap: ["连续按两次", "Press twice quickly"],
  tripleTap: ["连续按三次", "Press three times quickly"],
  quadTap: ["连续按四次", "Press four times quickly"],
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
  /**
   * The control the binding is for (a profile key such as lt or ls.up). A
   * repeat on a stick, trigger or half-axis control can follow how far it is
   * pushed (ANA-12).
   */
  control?: string;
  /** Effective profile timing; the binding's own timing applies on top. */
  timing?: ProfileTiming;
  profiles?: Profiles;
  actions?: Actions;
  language?: Language;
  disabled?: boolean;
  /** Script registrations on this control: they can add waits. */
  registrations?: DelaySource[];
  /** Show a ready "press" gesture when the binding has none (default). */
  suggestPress?: boolean;
  /** Shown between the gestures and the advanced section. */
  children?: ReactNode;
  /** Extra content at the end of the advanced section. */
  advanced?: ReactNode;
}

/**
 * The gestures of one binding, one card each with only one open, plus the
 * binding's own advanced settings (ADR-0045).
 */
export function BindingFields({
  value,
  onChange,
  control,
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
  const used = usedGestures(value);
  // A blocked binding cannot carry actions (the daemon rejects it), so no
  // gesture is offered until 取消屏蔽 under 高级.
  const blocked = !!value.block;
  const suggested: Gesture | null =
    suggestPress && !blocked && used.length === 0 ? "press" : null;
  // A gesture picked from the menu stays a draft until it is first edited.
  const [pending, setPending] = useState<Gesture | null>(null);
  const draft = pending && !value[pending] ? pending : suggested;
  // undefined: open the first card; null: the user closed every card.
  const [open, setOpen] = useState<Gesture | null | undefined>(undefined);
  // Shortcut capture records physical key positions (高级, ADR-0045).
  const [physical, setPhysical] = useState(false);
  // A file-only action (layer action, profile switch) cannot be set again in
  // the editor: removing it asks first.
  const [confirmRemove, setConfirmRemove] = useState<Gesture | null>(null);
  const confirmAction = confirmRemove ? value[confirmRemove] : undefined;
  const list = useRef<HTMLElement>(null);
  const refocusAdd = useRef(false);
  // A removed gesture card took the focused button with it: move focus to
  // 添加手势 (or the first remaining card) once the change is rendered.
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
  }, [value, pending]);
  const shown = gestures.filter(
    (gesture) => value[gesture] || gesture === draft,
  );
  const current =
    open === null ? null : open && shown.includes(open) ? open : shown[0];
  const effective = resolveTiming(timing);
  const keysShown = shown.some(
    (gesture) => (value[gesture]?.type ?? "keys") === "keys",
  );
  // Latch only holds a shortcut's keys: a key whose gestures all run
  // scripts does not offer it (a value set in the file is kept).
  const macrosOnly =
    shown.length > 0 &&
    shown.every((gesture) => value[gesture]?.type === "macro");
  const configured = [
    ...(value.timing && Object.values(value.timing).some((v) => v !== undefined)
      ? [t("判定时间", "Timing")]
      : []),
    ...(value.policy ? [t("打断判定", "Interruptions")] : []),
    ...(value.latch ? [t("锁存", "Latch")] : []),
    ...(value.haptic && !blocked ? [t("触感", "Haptic")] : []),
    ...(physical && keysShown ? [t("按物理位置", "Key position")] : []),
    ...(value.block ? [t("屏蔽", "Block")] : []),
  ];
  const remove = (gesture: Gesture) => {
    const next = { ...value };
    delete next[gesture];
    refocusAdd.current = true;
    setOpen(undefined);
    onChange(next);
  };
  const updateTiming = (key: keyof ProfileTiming, next: number | undefined) => {
    const nextTiming: ProfileTiming = { ...value.timing };
    if (next === undefined) delete nextTiming[key];
    else nextTiming[key] = next;
    const result: ProfileBinding = { ...value, timing: nextTiming };
    if (!Object.keys(nextTiming).length) delete result.timing;
    onChange(result);
  };
  const menu: MenuEntry[] = gestures
    .filter((gesture) => !value[gesture] && gesture !== draft)
    .map((gesture) => ({
      label: gestureName(gesture, language),
      hint: t(...gestureHints[gesture]),
      onSelect: () => {
        setPending(gesture);
        setOpen(gesture);
      },
    }));
  return (
    <>
      <section
        ref={list}
        className="drawer-section"
        aria-label={t("手势", "Gestures")}
        data-testid="gesture-list"
      >
        <div className="drawer-section-head">
          <span className="field-label">{t("手势", "Gestures")}</span>
          <MenuButton
            label={t("添加手势", "Add gesture")}
            testId="add-gesture"
            className="btn-ghost drawer-add"
            disabled={disabled || blocked || menu.length === 0}
            items={menu}
          >
            <Icon name="plus" size={13} stroke={2.2} />
            {t("添加手势", "Add gesture")}
          </MenuButton>
        </div>
        {shown.length === 0 && (
          <p className="drawer-empty">
            {blocked
              ? t(
                  "此按键已屏蔽。如需设置手势，请先在“高级”中取消屏蔽。",
                  "This key is blocked. To set gestures, unblock it under Advanced first.",
                )
              : t("尚未设置手势。", "No gestures are set here.")}
          </p>
        )}
        {shown.map((gesture) => {
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
              key={gesture}
              gesture={gesture}
              open={current === gesture}
              onToggle={() => setOpen(current === gesture ? null : gesture)}
              summary={gestureSummary(
                action,
                language,
                actions,
                profiles,
                missing,
              )}
              delay={delay}
              language={language}
              bodyId={`${id}-${gesture}`}
            >
              <fieldset className="drawer-fieldset" disabled={disabled}>
                <ActionFields
                  gesture={gesture}
                  value={action}
                  profiles={profiles}
                  language={language}
                  physical={physical}
                  depth={control !== undefined && hasDepth(control)}
                  tick={control !== undefined && isKnobTick(control)}
                  onChange={(next) => {
                    onChange({ ...value, [gesture]: next });
                    if (isDraft) setPending(null);
                    setOpen(gesture);
                  }}
                />
                {delay.text && (
                  <p className="drawer-hint" data-testid="gesture-delay">
                    <Icon name="info" size={14} stroke={2} />
                    <span>{delay.text}</span>
                  </p>
                )}
                {(!isDraft || gesture !== suggested) && (
                  <button
                    type="button"
                    className="btn-ghost drawer-remove"
                    onClick={() => {
                      if (isDraft) {
                        refocusAdd.current = true;
                        setOpen(undefined);
                        setPending(null);
                        return;
                      }
                      if (isFileOnlyAction(action)) setConfirmRemove(gesture);
                      else remove(gesture);
                    }}
                  >
                    <Icon name="trash" size={14} />
                    {t("删除此手势", "Remove this gesture")}
                  </button>
                )}
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
                    {t("按物理位置", "By key position")}
                  </span>
                  <span className="drawer-option-hint">
                    {t(
                      "录入快捷键时记录按键在键盘上的位置，不受输入法或键盘布局影响。",
                      "Capturing a shortcut records where each key is on the keyboard, independent of the input method or layout.",
                    )}
                  </span>
                </span>
                <Switch
                  label={t("按物理位置", "By key position")}
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
                    // This button goes away; 添加手势 is the next step.
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
        title={t("删除此手势？", "Remove this gesture?")}
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
                const gesture = confirmRemove;
                setConfirmRemove(null);
                if (gesture && !disabled && value[gesture]) remove(gesture);
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
