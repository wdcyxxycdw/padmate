import {
  type CSSProperties,
  type ReactNode,
  useContext,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";
import type { ProfileEntry, ScriptRegistration } from "../../shared/api";
import type {
  ProfileBinding,
  ProfileDpad,
  ProfileStick,
  ProfileTiming,
  ProfileTrigger,
} from "../../shared/generated/schema";
import { BindingFields } from "../BindingEditor";
import {
  actionText,
  isFileOnlyAction,
  registrationsFor,
  usedGestures,
} from "../editor-model";
import type { EditOptions } from "../editor-store";
import { MacroContext, missingMacros } from "../macro-context";
import type { Language } from "../store";
import { FieldError, numberParser, useCommitField } from "../ui/commit-field";
import { Dialog, MenuButton } from "../ui/controls";
import { Icon } from "../ui/Icon";
import { Keycaps } from "../ui/Keycaps";
import type { Direction } from "./analog-editor";

export const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

export const directionArrow: Record<Direction, string> = {
  up: "↑",
  right: "→",
  down: "↓",
  left: "←",
  upright: "↗",
  downright: "↘",
  downleft: "↙",
  upleft: "↖",
};
const directionNames: Record<Direction, [string, string]> = {
  up: ["上", "Up"],
  right: ["右", "Right"],
  down: ["下", "Down"],
  left: ["左", "Left"],
  upright: ["右上", "Up-right"],
  downright: ["右下", "Down-right"],
  downleft: ["左下", "Down-left"],
  upleft: ["左上", "Up-left"],
};
export function directionName(direction: Direction, language: Language) {
  const [zh, en] = directionNames[direction];
  return tx(language, zh, en);
}

/**
 * One-line text for a binding's first gesture, for labels and summaries.
 * An unbound control a script handles shows the script's label instead.
 */
export function bindingText(
  binding: ProfileBinding,
  language: Language,
  actions?: { name: string; label?: string }[],
  script?: string,
  profiles?: { id: string; name?: string }[],
): string {
  const gesture = usedGestures(binding)[0];
  const action = gesture ? binding[gesture] : undefined;
  if (!action)
    return binding.block
      ? tx(language, "已屏蔽", "Blocked")
      : (script ?? tx(language, "未设置", "Not set"));
  return actionText(action, language, actions, profiles);
}

/** Label of the first script registration that handles a control, if any. */
export function scriptLabel(
  registrations: ScriptRegistration[],
  control: string,
  language: Language,
): string | undefined {
  const owner = registrationsFor(registrations, control).owners[0];
  return owner ? owner.label || tx(language, "脚本", "Script") : undefined;
}

export function isBound(binding: ProfileBinding) {
  return usedGestures(binding).length > 0 || !!binding.block;
}

/** Keycaps (or an action name) of a binding's first gesture. */
export function BindingSummary({
  binding,
  language,
  actions,
  profiles,
  script,
}: {
  binding: ProfileBinding;
  language: Language;
  actions?: { name: string; label?: string }[];
  /** Library profiles, to name a profile switch as the drawer does. */
  profiles?: { id: string; name?: string }[];
  /** Label of a script that handles the control. */
  script?: string;
}) {
  const gestures = usedGestures(binding);
  const action = gestures[0] ? binding[gestures[0]] : undefined;
  const missing = missingMacros(useContext(MacroContext).list);
  if (!action)
    return (
      <span
        className={
          script && !binding.block ? "analog-script-label" : "analog-unset"
        }
      >
        {bindingText(binding, language, actions, script, profiles)}
      </span>
    );
  const noKeys =
    action.type === "keys" &&
    [action.keys].flat().every((chord) => !chord.trim());
  return (
    <span className="analog-summary">
      {noKeys ? (
        <span className="analog-unset">
          {tx(language, "未设置快捷键", "No shortcut yet")}
        </span>
      ) : action.type === "keys" ? (
        <Keycaps keys={action.keys} language={language} />
      ) : action.type === "macro" ? (
        // A script (SCR-50): its icon and name, a warning when it is gone.
        <span
          className="analog-summary-text analog-summary-macro"
          data-missing={missing.includes(action.file) || undefined}
        >
          <Icon
            name={missing.includes(action.file) ? "warn" : "script"}
            size={12}
            stroke={2.2}
          />
          {actionText(action, language, actions, profiles, missing)}
        </span>
      ) : (
        <span className="analog-summary-text">
          {actionText(action, language, actions, profiles)}
        </span>
      )}
      {gestures.length > 1 && (
        <span
          className="analog-more"
          title={tx(
            language,
            `另有 ${gestures.length - 1} 个手势`,
            gestures.length > 2
              ? `${gestures.length - 1} more gestures`
              : "1 more gesture",
          )}
        >
          +{gestures.length - 1}
        </span>
      )}
    </span>
  );
}

export function ScriptBadge({ language }: { language: Language }) {
  return (
    <span
      className="analog-s"
      data-testid="analog-script-ownership"
      title={tx(language, "由脚本接管", "Handled by a script")}
    >
      S
    </span>
  );
}

/**
 * A number field committed on Enter or when it loses focus (UI-43). Only
 * values within [min, max] are committed; other text stays with an error.
 */
function useNumberField({
  value,
  min,
  max,
  integer,
  language,
  onCommit,
}: {
  value: number;
  min: number;
  max: number;
  integer: boolean;
  language: Language;
  onCommit: (value: number) => void;
}) {
  return useCommitField<number | undefined>({
    value: String(value),
    parse: numberParser(language, { min, max, integer }),
    onCommit: (next) => {
      if (next !== undefined && next !== value) onCommit(next);
    },
  });
}

/**
 * A labelled number with a number field and a slider below it, by default a
 * percentage. A drag or a run of arrow keys on the slider is one edit
 * (EditOptions.merge). The field may reach beyond the slider (`sliderMax`):
 * a value past the end of the slider keeps it at its end.
 */
export function SliderField({
  label,
  hint,
  value,
  min = 0,
  max = 100,
  sliderMax = max,
  step = 1,
  unit = "%",
  integer = false,
  language,
  onCommit,
}: {
  label: string;
  hint?: string;
  /** Percent, e.g. 15 for 0.15, unless `unit` says otherwise. */
  value: number;
  min?: number;
  /** The largest value the number field takes. */
  max?: number;
  /** The largest value of the slider, when it is below `max`. */
  sliderMax?: number;
  /** The step of the slider. */
  step?: number;
  unit?: string;
  /** The field takes whole numbers only. */
  integer?: boolean;
  language: Language;
  onCommit: (value: number, options?: EditOptions) => void;
}) {
  const id = useId();
  const field = useNumberField({
    value,
    min,
    max,
    integer,
    language,
    onCommit,
  });
  // While the pointer is down on the slider, its moves are one gesture
  // however long it pauses. Releasing it ends the gesture.
  const latest = useRef({ value, onCommit });
  latest.current = { value, onCommit };
  const [drag] = useState(() => {
    const state = {
      down: false,
      release() {
        window.removeEventListener("pointerup", state.release);
        window.removeEventListener("pointercancel", state.release);
        if (!state.down) return;
        state.down = false;
        latest.current.onCommit(latest.current.value, { merge: id });
      },
    };
    return state;
  });
  useEffect(() => () => drag.release(), [drag]);
  const shown = Math.min(value, sliderMax);
  const fill = Math.max(
    0,
    Math.min(100, ((shown - min) / (sliderMax - min)) * 100),
  );
  return (
    <div className="analog-slider" data-wide={max >= 1000 || undefined}>
      <div className="analog-slider-head">
        <label htmlFor={id} title={hint}>
          {label}
        </label>
        <span className="analog-value">
          <input
            id={id}
            type="number"
            className="analog-number"
            inputMode={integer ? "numeric" : "decimal"}
            min={min}
            max={max}
            step={1}
            {...field.props}
          />
          <span className="analog-unit" aria-hidden="true">
            {unit}
          </span>
        </span>
      </div>
      <input
        type="range"
        className="analog-range"
        min={min}
        max={sliderMax}
        step={step}
        value={shown}
        aria-label={label}
        title={hint}
        style={{ "--analog-fill": `${fill}%` } as CSSProperties}
        onPointerDown={() => {
          drag.down = true;
          window.addEventListener("pointerup", drag.release);
          window.addEventListener("pointercancel", drag.release);
        }}
        onBlur={drag.release}
        onChange={(event) =>
          onCommit(event.currentTarget.valueAsNumber, {
            merge: id,
            hold: drag.down,
          })
        }
      />
      <FieldError id={field.errorId} text={field.error} />
    </div>
  );
}

/** A settings row: label and hint on the left, a number and unit on the right. */
export function SettingField({
  label,
  hint,
  value,
  min,
  max,
  step = 1,
  unit,
  integer = false,
  language,
  onCommit,
}: {
  label: string;
  hint?: string;
  value: number;
  min: number;
  max: number;
  step?: number;
  unit: string;
  integer?: boolean;
  language: Language;
  onCommit: (value: number) => void;
}) {
  const id = useId();
  const hintId = useId();
  const field = useNumberField({
    value,
    min,
    max,
    integer,
    language,
    onCommit,
  });
  const describedBy =
    [hint ? hintId : "", field.error ? field.errorId : ""]
      .filter(Boolean)
      .join(" ") || undefined;
  return (
    <div className="analog-setting">
      <div className="analog-setting-text">
        <label htmlFor={id}>{label}</label>
        {hint && (
          <span id={hintId} className="analog-caption">
            {hint}
          </span>
        )}
        <FieldError id={field.errorId} text={field.error} />
      </div>
      <span className="analog-value boxed">
        <input
          id={id}
          type="number"
          className="analog-number"
          inputMode={integer ? "numeric" : "decimal"}
          min={min}
          max={max}
          step={step}
          {...field.props}
          aria-describedby={describedBy}
        />
        <span className="analog-unit" aria-hidden="true">
          {unit}
        </span>
      </span>
    </div>
  );
}

/** A setting row whose control is not a number (a switch or segments). */
export function SettingRow({
  label,
  hint,
  stacked = false,
  children,
}: {
  label: string;
  hint?: string;
  /** Put the control below the label, for wide segments. */
  stacked?: boolean;
  children: ReactNode;
}) {
  return (
    <div className={`analog-setting${stacked ? " stacked" : ""}`}>
      <div className="analog-setting-text">
        <span className="analog-setting-label">{label}</span>
        {hint && <span className="analog-caption">{hint}</span>}
      </div>
      {children}
    </div>
  );
}

export interface BindingEditorProps {
  registrations: ScriptRegistration[];
  timing: Required<ProfileTiming>;
  profiles: ProfileEntry[];
  actions?: { name: string; label?: string }[];
  language: Language;
}

/**
 * The expanded editor of one direction, stage or stick click. Under a row
 * (`compact`) the row already names it and collapses it, so no header.
 * `disabled` (a read-only profile) keeps the gesture cards expandable for
 * reading and disables only what edits the binding.
 */
export function BindingCard({
  id,
  title,
  compact = false,
  control,
  binding,
  onChange,
  onClose,
  registrations,
  timing,
  profiles,
  actions,
  language,
  disabled = false,
}: BindingEditorProps & {
  id: string;
  title: string;
  compact?: boolean;
  control: string;
  binding: ProfileBinding;
  onChange: (binding: ProfileBinding | undefined) => void;
  onClose: () => void;
  disabled?: boolean;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const scripts = registrationsFor(registrations, control);
  const owners = scripts.owners;
  const names = [
    ...new Set(owners.map((owner) => owner.label || t("脚本", "Script"))),
  ];
  const [confirmClear, setConfirmClear] = useState(false);
  const collapse = useRef<HTMLButtonElement>(null);
  // Settings the editor cannot recreate (key-group actions, profile switches,
  // blocks) need a confirm.
  const fromFile =
    !!binding.block ||
    usedGestures(binding).some((gesture) => isFileOnlyAction(binding[gesture]));
  const clear = () => {
    // Clearing removes the keys; a display name stays.
    onChange(
      binding.label === undefined ? undefined : { label: binding.label },
    );
    requestAnimationFrame(() => collapse.current?.focus());
  };
  const close = () => {
    const opener = document.querySelector<HTMLElement>(
      `[aria-controls="${CSS.escape(id)}"]`,
    );
    onClose();
    requestAnimationFrame(() => opener?.focus());
  };
  return (
    <section
      id={id}
      className="analog-card"
      aria-label={title}
      data-control={control}
    >
      {!compact && (
        <header className="analog-card-head">
          <span className="analog-card-title">{title}</span>
          {owners.length > 0 && <ScriptBadge language={language} />}
          <span className="analog-spacer" />
          {isBound(binding) && !disabled && (
            <button
              type="button"
              className="btn-ghost analog-clear"
              onClick={() => (fromFile ? setConfirmClear(true) : clear())}
            >
              {t("清除", "Clear")}
            </button>
          )}
          <button
            type="button"
            ref={collapse}
            className="icon-btn bare"
            aria-label={t(`收起${title}`, `Collapse ${title}`)}
            onClick={close}
          >
            <Icon name="chevronUp" size={15} />
          </button>
        </header>
      )}
      {owners.length > 0 && (
        <p className="analog-script-note">
          {t(
            `此处也由脚本处理：${names.join("、")}。`,
            `A script also handles this: ${names.join(", ")}.`,
          )}
          {usedGestures(binding).length > 0 &&
            t(
              "同时设置了按键时，脚本可能先处理输入。",
              " With keys set too, the script may handle the input first.",
            )}
        </p>
      )}
      <div className="analog-card-body">
        <BindingFields
          key={control}
          control={control}
          value={binding}
          onChange={(next) => onChange(next)}
          timing={timing}
          profiles={profiles}
          actions={actions}
          language={language}
          disabled={disabled}
          registrations={scripts.visible}
          suggestPress={owners.length === 0 && !disabled}
        />
      </div>
      <ConfirmDialog
        open={confirmClear}
        title={t("清除此设置？", "Clear this setting?")}
        confirmLabel={t("清除", "Clear")}
        language={language}
        onConfirm={clear}
        onClose={() => setConfirmClear(false)}
      >
        <p className="dialog-text">
          {t(
            "此处有来自配置文件的设置（切换按键组、切换配置或屏蔽），无法在界面中重新创建。清除后只能通过修改配置文件恢复。",
            "This holds a setting from the profile file (a key-group action, a profile switch or a block) that the editor cannot recreate. After clearing, only editing the file brings it back.",
          )}
        </p>
      </ConfirmDialog>
    </section>
  );
}

/** A full-width row that opens one binding, e.g. 按下摇杆. */
export function BindingRow({
  title,
  hint,
  control,
  binding,
  expanded,
  cardId,
  active = false,
  registrations,
  language,
  actions,
  profiles,
  onToggle,
}: {
  title: string;
  hint?: string;
  control: string;
  binding: ProfileBinding;
  expanded: boolean;
  cardId: string;
  active?: boolean;
  registrations: ScriptRegistration[];
  language: Language;
  actions?: { name: string; label?: string }[];
  profiles?: { id: string; name?: string }[];
  onToggle: () => void;
}) {
  const script = scriptLabel(registrations, control, language);
  const bound = isBound(binding);
  return (
    <button
      type="button"
      className="analog-row"
      data-control={control}
      data-unset={(!bound && !script) || undefined}
      data-active={active}
      aria-expanded={expanded}
      aria-controls={expanded ? cardId : undefined}
      onClick={onToggle}
    >
      <span className="analog-row-text">
        <span className="analog-row-title">{title}</span>
        {hint && <span className="analog-row-hint">{hint}</span>}
      </span>
      <span className="analog-spacer" />
      {active && (
        <span className="analog-pressed">
          {tx(language, "按下中", "Pressed")}
        </span>
      )}
      <BindingSummary
        binding={binding}
        language={language}
        actions={actions}
        profiles={profiles}
        script={script}
      />
      {script !== undefined && <ScriptBadge language={language} />}
      <Icon
        name={bound ? (expanded ? "chevronUp" : "chevronDown") : "plus"}
        size={13}
        stroke={2.2}
        className={bound ? "analog-row-icon" : "analog-row-icon add"}
      />
    </button>
  );
}

/** A grid of direction cells; one click opens the direction's editor. */
export function DirectionGrid({
  group,
  directions,
  bindingFor,
  active,
  expanded,
  cardId,
  registrations,
  language,
  actions,
  profiles,
  label,
  onToggle,
}: {
  group: string;
  directions: readonly Direction[];
  bindingFor: (control: string) => ProfileBinding;
  active: readonly Direction[];
  expanded: string | null;
  cardId: string;
  registrations: ScriptRegistration[];
  language: Language;
  actions?: { name: string; label?: string }[];
  profiles?: { id: string; name?: string }[];
  label: string;
  onToggle: (control: string) => void;
}) {
  return (
    <fieldset
      className="analog-grid"
      aria-label={label}
      data-count={directions.length}
    >
      {directions.map((direction) => {
        const control = `${group}.${direction}`;
        const binding = bindingFor(control);
        const script = scriptLabel(registrations, control, language);
        const pressed = active.includes(direction);
        const open = expanded === control;
        const name = directionName(direction, language);
        return (
          <button
            type="button"
            key={direction}
            className="analog-cell"
            data-direction={direction}
            data-control={control}
            data-active={pressed}
            data-unset={(!isBound(binding) && !script) || undefined}
            aria-expanded={open}
            aria-controls={open ? cardId : undefined}
            aria-label={[
              `${name}：${bindingText(binding, language, actions, script, profiles)}`,
              pressed ? tx(language, "按下中", "pressed") : "",
              script ? tx(language, "由脚本接管", "handled by a script") : "",
            ]
              .filter(Boolean)
              .join(tx(language, "，", ", "))}
            onClick={() => onToggle(control)}
          >
            <span className="analog-cell-arrow" aria-hidden="true">
              {directionArrow[direction]}
            </span>
            <BindingSummary
              binding={binding}
              language={language}
              actions={actions}
              profiles={profiles}
              script={script}
            />
            <span className="analog-spacer" />
            {pressed && (
              <span className="analog-pressed">
                {tx(language, "按下中", "Pressed")}
              </span>
            )}
            {script !== undefined && <ScriptBadge language={language} />}
          </button>
        );
      })}
    </fieldset>
  );
}

/** A yes/no dialog for draft changes that replace something. */
export function ConfirmDialog({
  open,
  title,
  confirmLabel,
  language,
  onConfirm,
  onClose,
  children,
}: {
  open: boolean;
  title: string;
  confirmLabel: string;
  language: Language;
  onConfirm: () => void;
  onClose: () => void;
  children: ReactNode;
}) {
  return (
    <Dialog
      open={open}
      title={title}
      closeLabel={tx(language, "关闭", "Close")}
      onClose={onClose}
      className="analog-dialog"
      footer={
        <>
          <span className="spacer" />
          <button type="button" className="btn" onClick={onClose}>
            {tx(language, "取消", "Cancel")}
          </button>
          <button
            type="button"
            className="btn-primary"
            onClick={() => {
              onConfirm();
              onClose();
            }}
          >
            {confirmLabel}
          </button>
        </>
      }
    >
      {children}
    </Dialog>
  );
}

type ParamKind = "stick" | "trigger" | "dpad";
type ParamSettings = ProfileStick | ProfileTrigger | ProfileDpad;
type CopiedParams = {
  kind: ParamKind;
  source: string;
  settings: ParamSettings;
};
// App-local clipboard: it intentionally does not write the system clipboard.
let parameterClipboard: CopiedParams | null = null;
/** Clears the parameter clipboard (tests). */
export function clearParameterClipboard() {
  parameterClipboard = null;
}

/**
 * Copy, paste and reset of one stick's, trigger's or d-pad's parameters
 * (UI-36). `disabled` (a read-only profile) still allows copying.
 */
export function ParamsMenu({
  kind,
  name,
  settings,
  language,
  testId,
  disabled = false,
  openRequest,
  onReplace,
}: {
  kind: ParamKind;
  /** Display name of the control, e.g. 左摇杆. */
  name: string;
  settings: ParamSettings | undefined;
  language: Language;
  testId?: string;
  disabled?: boolean;
  /** Opens the menu whenever it changes to a new value above 0. */
  openRequest?: number;
  onReplace: (settings: ParamSettings) => void;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const [copied, setCopied] = useState(parameterClipboard);
  const [pending, setPending] = useState<"paste" | "reset" | null>(null);
  const [message, setMessage] = useState("");
  const canPaste = copied?.kind === kind && !disabled;
  return (
    <>
      <MenuButton
        label={t("参数操作", "Parameter actions")}
        className="icon-btn bare"
        testId={testId}
        openRequest={openRequest}
        items={[
          {
            label: t("复制参数", "Copy parameters"),
            icon: "copy",
            onSelect: () => {
              parameterClipboard = {
                kind,
                source: name,
                settings: structuredClone(settings ?? {}),
              };
              setCopied(parameterClipboard);
              setMessage(
                t(`已复制${name}的参数`, `Copied the parameters of ${name}`),
              );
            },
          },
          {
            label: t("粘贴参数…", "Paste parameters…"),
            icon: "paste",
            disabled: !canPaste,
            onSelect: () => setPending("paste"),
          },
          { kind: "separator" },
          {
            label: t("恢复默认参数…", "Reset parameters…"),
            icon: "refresh",
            disabled,
            onSelect: () => setPending("reset"),
          },
        ]}
      />
      {message && (
        <span className="analog-caption" role="status">
          {message}
        </span>
      )}
      <ConfirmDialog
        open={pending === "paste" && canPaste}
        title={t("粘贴参数？", "Paste parameters?")}
        confirmLabel={t("粘贴", "Paste")}
        language={language}
        onClose={() => setPending(null)}
        onConfirm={() => {
          if (copied) onReplace(structuredClone(copied.settings));
        }}
      >
        <p className="analog-dialog-text">
          {t(
            `用从${copied?.source ?? ""}复制的参数替换${name}的参数。按键设置不变。`,
            `Replace the parameters of ${name} with those copied from ${copied?.source ?? ""}. Key settings are kept.`,
          )}
        </p>
      </ConfirmDialog>
      <ConfirmDialog
        open={pending === "reset" && !disabled}
        title={t("恢复默认参数？", "Reset parameters?")}
        confirmLabel={t("恢复默认", "Reset")}
        language={language}
        onClose={() => setPending(null)}
        onConfirm={() => onReplace({})}
      >
        <p className="analog-dialog-text">
          {t(
            `${name}的参数将恢复为默认值。按键设置不变。`,
            `The parameters of ${name} return to their defaults. Key settings are kept.`,
          )}
        </p>
      </ConfirmDialog>
    </>
  );
}
