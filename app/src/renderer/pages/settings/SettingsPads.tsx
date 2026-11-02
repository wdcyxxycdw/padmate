import { useId } from "react";
import type { PadFunctionKeyName, PadInfo } from "../../../shared/pads";
import {
  ControlIcon,
  type ControlIconSpec,
  controlIcon,
} from "../../controller/control-icon";
import { familyName } from "../../controller/controller-model";
import { functionKeyControl } from "../../editor/editor-model";
import type { PadChange } from "../../state/pads-model";
import { padKind, padTitle } from "../../state/pads-model";
import {
  FEEDBACK_SWITCHES,
  type FeedbackSwitch,
} from "../../state/settings-model";
import type { Language } from "../../state/store";
import { Segmented, Switch } from "../../ui/controls";
import { ComboKeys, seconds } from "./PanicComboDialog";
import "../pages.css";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/** The name of each switch, as the row and its switch carry it. */
export function switchLabel(key: FeedbackSwitch, language: Language): string {
  switch (key) {
    case "rumble":
      return tx(language, "震动", "Rumble");
    case "led":
      return tx(language, "灯光", "Lights");
    case "pause":
      return tx(language, "暂停提示", "Pause alert");
    case "lowBattery":
      return tx(language, "低电提醒", "Low battery alert");
  }
}

/**
 * How a function key is drawn in the choice: the icon the controller shows on
 * it (the same icons as the key preview), or none for a button that only has
 * a name, such as ZR.
 */
export function functionKeyIcon(
  pad: PadInfo,
  control: PadFunctionKeyName,
  label: string,
): ControlIconSpec | null {
  // ZL and ZR are the triggers: their icon goes by the trigger's control.
  const on = functionKeyControl(control);
  const icon = controlIcon(on, familyName(pad.type), { [on]: label });
  return icon.kind === "badge" ? null : icon;
}

/**
 * One controller of Settings → Controllers (ADR-0017): its function key, its
 * four feedback switches and its emergency pause, each stored for this
 * controller alone. The daemon says which function keys it can have and which
 * is the default (pads.list); the combo is changed in the dialog the page
 * owns, which needs the controller to be connected.
 */
export function PadSection({
  pad,
  language,
  disabled,
  onChange,
  onChangeCombo,
}: {
  pad: PadInfo;
  language: Language;
  /** config.json cannot be written now. */
  disabled: boolean;
  onChange: (change: PadChange) => void;
  /** Opens the dialog that records a new emergency combo. */
  onChangeCombo: () => void;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const heading = useId();
  const title = padTitle(pad, language);
  const kind = padKind(pad, language);
  const { functionKey, feedback, panic } = pad;
  const functionName = t("功能键", "Function key");
  const hold = seconds(panic.effective.holdMs, language);
  return (
    <section
      className="page-section"
      data-testid="pad-section"
      data-pad={pad.key}
      data-connected={pad.connected}
      aria-labelledby={heading}
    >
      <div className="pad-heading">
        <h2 id={heading} className="section-title">
          {title}
        </h2>
        {kind !== title && <span className="tag">{kind}</span>}
        <span className={pad.connected ? "tag accent" : "tag"}>
          {pad.connected
            ? t("已连接", "Connected")
            : t("未连接", "Not connected")}
        </span>
      </div>
      <div className="card">
        <div className="row" data-testid="pad-function-key">
          <div className="row-text">
            <span className="row-title">{functionName}</span>
            <span className="row-hint">
              {t(
                "按住时其他键换成另一套",
                "While held, other keys switch to another set",
              )}
            </span>
          </div>
          <Segmented<PadFunctionKeyName>
            label={`${functionName} ${title}`}
            className="page-seg control-choice"
            disabled={disabled}
            options={functionKey.options.map((option) => {
              const icon = functionKeyIcon(pad, option.control, option.label);
              return {
                value: option.control,
                label: (
                  <>
                    {icon && <ControlIcon icon={icon} />}
                    <span>
                      {option.control === functionKey.default
                        ? t(
                            `${option.label}（默认）`,
                            `${option.label} (default)`,
                          )
                        : option.label}
                    </span>
                  </>
                ),
              };
            })}
            value={functionKey.effective}
            onChange={(control) => onChange({ functionKey: control })}
          />
        </div>
        <div className="row pad-subhead">
          <span className="row-title">{t("反馈", "Feedback")}</span>
        </div>
        {FEEDBACK_SWITCHES.map((name) => {
          const label = switchLabel(name, language);
          return (
            <div className="row" key={name} data-testid={`pad-${name}`}>
              <div className="row-text">
                <span className="row-title">{label}</span>
              </div>
              <Switch
                label={label}
                checked={feedback.effective[name]}
                disabled={disabled}
                onChange={(on) => onChange({ feedback: { name, on } })}
              />
            </div>
          );
        })}
        <div className="row" data-testid="pad-panic">
          <div className="row-text">
            <span className="row-title">
              {t("紧急暂停", "Emergency pause")}
            </span>
            <span className="row-hint">
              {t(
                `按住 ${hold}：松开所有按键并暂停`,
                `Hold ${hold} to release all keys and pause`,
              )}
            </span>
          </div>
          <div className="page-row-control">
            <ComboKeys pad={pad} buttons={panic.effective.buttons} />
            {pad.connected && (
              <button
                type="button"
                className="btn"
                aria-label={`${t("修改紧急暂停", "Change emergency pause")} ${title}`}
                disabled={disabled}
                onClick={onChangeCombo}
              >
                {t("修改", "Change")}
              </button>
            )}
          </div>
        </div>
      </div>
    </section>
  );
}
