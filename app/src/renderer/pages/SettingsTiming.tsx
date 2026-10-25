import { useId, useState } from "react";
import type {
  Config,
  ConfigTiming,
  ProfileTiming,
} from "../../shared/generated/schema";
import { resolveTiming } from "../editor-model";
import { globalTiming, parseMs } from "../settings-model";
import type { Language } from "../store";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/**
 * A millisecond field, saved when it loses focus or on Enter. Empty is the
 * built-in default, which the placeholder shows; an invalid value is not
 * saved, and the field shows the saved one again. It is a text field: a
 * number field reports text it cannot parse ("1e", "-") as empty, which
 * would save the built-in default.
 */
function MsRow({
  title,
  hint,
  value,
  placeholder,
  disabled,
  language,
  onSave,
}: {
  title: string;
  hint?: string;
  value: number | undefined;
  placeholder: number;
  disabled: boolean;
  language: Language;
  onSave: (value: number | null) => void;
}) {
  const id = useId();
  const hintId = useId();
  // The text as typed, while it differs from what is saved.
  const [text, setText] = useState<string | null>(null);
  const invalid = text !== null && parseMs(text) === undefined;
  const commit = () => {
    if (text === null) return;
    const next = parseMs(text);
    setText(null);
    if (next !== undefined && next !== (value ?? null)) onSave(next);
  };
  return (
    <div className="row">
      <div className="row-text">
        <label className="row-title" htmlFor={id}>
          {title}
        </label>
        {hint && (
          <span className="row-hint" id={hintId}>
            {hint}
          </span>
        )}
      </div>
      <span className="number-with-unit page-ms">
        <input
          id={id}
          className="input input-number"
          type="text"
          inputMode="numeric"
          autoComplete="off"
          spellCheck={false}
          value={text ?? (value === undefined ? "" : String(value))}
          placeholder={String(placeholder)}
          disabled={disabled}
          aria-invalid={invalid || undefined}
          aria-describedby={hint ? hintId : undefined}
          onChange={(event) => setText(event.currentTarget.value)}
          onBlur={commit}
          onKeyDown={(event) => {
            if (event.key === "Enter") commit();
          }}
        />
        <span>{tx(language, "毫秒", "ms")}</span>
      </span>
    </div>
  );
}

/**
 * Settings → 按键判定 (UI-19): config.json's default hold and multi-tap
 * times, under every profile's own. They apply when saved.
 */
export function TimingSection({
  language,
  config,
  disabled,
  onSave,
}: {
  language: Language;
  /** The parsed config.json; null while it cannot be read. */
  config: Config | null;
  disabled: boolean;
  onSave: (key: keyof ConfigTiming, value: number | null) => void;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const builtIn = resolveTiming() as Required<ProfileTiming>;
  return (
    <section className="page-section">
      <h2 className="section-title">{t("按键判定", "Key timing")}</h2>
      <div className="card">
        <MsRow
          title={t("触发长按时间", "Long-press delay")}
          value={globalTiming(config, "holdMs")}
          placeholder={builtIn.holdMs}
          disabled={disabled}
          language={language}
          onSave={(value) => onSave("holdMs", value)}
        />
        <MsRow
          title={t("连击间隔", "Multi-tap window")}
          hint={t(
            "两次点按的间隔在此时间内视为连击",
            "Two taps within this time are a multi-tap",
          )}
          value={globalTiming(config, "multiTapMs")}
          placeholder={builtIn.multiTapMs}
          disabled={disabled}
          language={language}
          onSave={(value) => onSave("multiTapMs", value)}
        />
      </div>
    </section>
  );
}
