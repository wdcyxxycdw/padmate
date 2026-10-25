import { useId, useState } from "react";
import { useStore } from "zustand";
import type { ProfileTiming } from "../shared/generated/schema";
import { resolveTiming } from "./editor-model";
import type { EditorStore } from "./editor-store";
import { parentOf, withExtends } from "./inheritance";
import {
  defaultProfileName,
  isDefaultProfile,
  nameProblemText,
  parentChoices,
  parentName,
  profileName,
  profileNameProblem,
} from "./library-model";
import type { Language } from "./store";
import { FieldError, numberParser, useCommitField } from "./ui/commit-field";
import { Dialog, Disclosure } from "./ui/controls";
import "./pages/profiles.css";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/**
 * A millisecond field, saved on Enter or when it loses focus (UI-43). Empty
 * is the global setting. A text field: a number field reports text it
 * cannot parse ("1e") as empty, which would drop the value.
 */
function TimingRow({
  label,
  hint,
  value,
  placeholder,
  language,
  onChange,
}: {
  label: string;
  hint: string;
  value?: number;
  placeholder: number;
  language: Language;
  onChange: (value: number | undefined) => void;
}) {
  const field = useCommitField<number | undefined>({
    value: value === undefined ? "" : String(value),
    parse: numberParser(language, { min: 0, max: 60000, optional: true }),
    onCommit: (next) => {
      if (next !== value) onChange(next);
    },
  });
  return (
    <div className="row">
      <div className="row-text">
        <span className="row-title">{label}</span>
        <span className="row-hint">{hint}</span>
        <FieldError id={field.errorId} text={field.error} />
      </div>
      <span className="number-with-unit">
        <input
          className="input input-number"
          type="text"
          inputMode="numeric"
          autoComplete="off"
          spellCheck={false}
          aria-label={label}
          placeholder={String(placeholder)}
          {...field.props}
        />
        <span>ms</span>
      </span>
    </div>
  );
}

/**
 * Profile-level settings: the name and the default gesture timing (ADR-0045).
 * The feedback switches are not here: they are global (Settings → 反馈,
 * ADR-0067). Like the mapping page, each change is saved when it is committed
 * (UI-43).
 */
export function ProfileSettingsDialog({
  open,
  editor,
  language,
  onClose,
}: {
  open: boolean;
  editor: EditorStore;
  language: Language;
  onClose: () => void;
}) {
  const state = useStore(editor);
  // Typed text goes with the dialog: it starts empty each time it opens.
  const [session, setSession] = useState(0);
  const basedOnId = useId();
  const close = () => {
    setSession(session + 1);
    onClose();
  };
  const profile = state.draft;
  if (!profile) return null;
  // What an empty timing field falls back to: the profile it is based on
  // sets some of it, the global settings the rest.
  const up = parentOf(profile, state.parent);
  const upName = up ? parentName(up.id, state.profiles, language) : "";
  const global = resolveTiming(state.globalTiming, up?.profile.timing);
  const locked = !!state.readOnly || state.busy;
  const choices = parentChoices(state.profiles, state.id);
  const basedOn = profile.extends ?? "";
  const setTiming = (key: keyof ProfileTiming, value: number | undefined) => {
    const timing: ProfileTiming = { ...profile.timing, [key]: value };
    if (value === undefined) delete timing[key];
    const { timing: _previous, ...rest } = profile;
    state.edit(Object.keys(timing).length ? { ...rest, timing } : rest);
  };
  const hint = (key: keyof ProfileTiming) =>
    up?.profile.timing?.[key] !== undefined
      ? tx(
          language,
          `留空则沿用「${upName}」（${global[key]} ms）。`,
          `Leave empty to use ${upName}'s ${global[key]} ms.`,
        )
      : tx(
          language,
          `留空则使用全局设置（${global[key]} ms）。`,
          `Leave empty to use the global ${global[key]} ms.`,
        );
  // The fixed default keeps its name (PRO-28); the daemon refuses another.
  const fixed = isDefaultProfile(state.id);
  const name = fixed ? defaultProfileName(language) : profile.name;
  return (
    <Dialog
      open={open}
      title={tx(language, "配置设置", "Profile settings")}
      subtitle={tx(
        language,
        `仅影响配置「${name}」。`,
        `Only affects “${name}”.`,
      )}
      closeLabel={tx(language, "关闭配置设置", "Close profile settings")}
      onClose={close}
      footer={
        <>
          <span className="spacer" />
          <button type="button" className="btn-primary" onClick={close}>
            {tx(language, "完成", "Done")}
          </button>
        </>
      }
    >
      <fieldset
        key={`${state.id}:${session}`}
        className="plain-fieldset"
        disabled={locked}
      >
        <NameField
          name={name}
          fixed={fixed}
          language={language}
          problem={(text) => profileNameProblem(text, state.profiles, state.id)}
          // Renaming a profile is not undone (UI-43).
          onCommit={(next) =>
            state.edit({ ...profile, name: next }, { history: false })
          }
        />
        <div className="field">
          <label className="field-label" htmlFor={basedOnId}>
            {tx(language, "基于", "Based on")}
          </label>
          <select
            id={basedOnId}
            className="select"
            value={basedOn}
            onChange={(event) =>
              state.edit(withExtends(profile, event.target.value || undefined))
            }
          >
            <option value="">{tx(language, "无", "None")}</option>
            {basedOn && !choices.some((entry) => entry.id === basedOn) && (
              <option value={basedOn}>
                {parentName(basedOn, state.profiles, language)}
              </option>
            )}
            {choices.map((entry) => (
              <option key={entry.id} value={entry.id}>
                {profileName(entry, language)}
              </option>
            ))}
          </select>
        </div>
        <section>
          <h3 className="section-title">
            {tx(language, "按键判定", "Key timing")}
          </h3>
          <div className="card">
            <TimingRow
              label={tx(language, "触发长按时间", "Long-press delay")}
              hint={hint("holdMs")}
              value={profile.timing?.holdMs}
              placeholder={global.holdMs}
              language={language}
              onChange={(value) => setTiming("holdMs", value)}
            />
            <TimingRow
              label={tx(language, "连击间隔", "Multi-tap window")}
              hint={hint("multiTapMs")}
              value={profile.timing?.multiTapMs}
              placeholder={global.multiTapMs}
              language={language}
              onChange={(value) => setTiming("multiTapMs", value)}
            />
          </div>
        </section>
        <Disclosure
          title={tx(language, "高级", "Advanced")}
          summary={tx(
            language,
            "点按时长 · 点按间隔",
            "Tap duration · tap gap",
          )}
        >
          <div className="card">
            <TimingRow
              label={tx(language, "点按时长", "Tap duration")}
              hint={hint("tapPressMs")}
              value={profile.timing?.tapPressMs}
              placeholder={global.tapPressMs}
              language={language}
              onChange={(value) => setTiming("tapPressMs", value)}
            />
            <TimingRow
              label={tx(language, "点按间隔", "Tap gap")}
              hint={hint("tapGapMs")}
              value={profile.timing?.tapGapMs}
              placeholder={global.tapGapMs}
              language={language}
              onChange={(value) => setTiming("tapGapMs", value)}
            />
          </div>
        </Disclosure>
      </fieldset>
    </Dialog>
  );
}

/**
 * The profile's name, saved on Enter or when the field is left. A name that
 * is empty, too long or another profile's (F2) is not saved; the problem
 * shows while typing.
 */
function NameField({
  name,
  fixed,
  language,
  problem,
  onCommit,
}: {
  name: string;
  fixed: boolean;
  language: Language;
  problem: (text: string) => ReturnType<typeof profileNameProblem>;
  onCommit: (name: string) => void;
}) {
  const inputId = useId();
  const field = useCommitField<string>({
    value: name,
    parse: (text) => {
      const found = problem(text);
      return found
        ? { error: nameProblemText(found, language) }
        : { value: text.trim() };
    },
    onCommit: (next) => {
      if (next !== name) onCommit(next);
    },
  });
  // Shown while typing too, not only once the name is committed.
  const typing = field.editing ? problem(field.text) : null;
  const error =
    field.error || (typing ? nameProblemText(typing, language) : "");
  // The error is outside the label, or it would become part of the name.
  return (
    <div className="field">
      <label className="field-label" htmlFor={inputId}>
        {tx(language, "名称", "Name")}
      </label>
      <input
        id={inputId}
        className="input"
        maxLength={120}
        readOnly={fixed}
        {...(fixed ? { value: name } : field.props)}
        aria-invalid={!!error}
        aria-describedby={error ? field.errorId : undefined}
      />
      <FieldError id={field.errorId} text={error} />
    </div>
  );
}
