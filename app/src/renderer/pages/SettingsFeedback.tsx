import { FEEDBACK_SWITCHES, type FeedbackSwitch } from "../settings-model";
import type { Language } from "../store";
import { Switch } from "../ui/controls";
import "./pages.css";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/** The name of each switch, as the row and its switch carry it. */
function switchLabel(key: FeedbackSwitch, language: Language): string {
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
 * Settings → 反馈 (UI-27, ADR-0067): the four controller feedback switches,
 * global for every profile and controller. They are config.json's feedback
 * members, saved like the other rows of the page, and the daemon applies them
 * at once. A row is its name and its switch, nothing else.
 */
export function FeedbackSection({
  language,
  settings,
  disabled,
  onChange,
}: {
  language: Language;
  settings: Record<FeedbackSwitch, boolean>;
  disabled: boolean;
  onChange: (change: Partial<Record<FeedbackSwitch, boolean>>) => void;
}) {
  return (
    <section className="page-section" data-testid="feedback-settings">
      <h2 className="section-title">{tx(language, "反馈", "Feedback")}</h2>
      <div className="card">
        {FEEDBACK_SWITCHES.map((key) => {
          const label = switchLabel(key, language);
          return (
            <div className="row" key={key}>
              <div className="row-text">
                <span className="row-title">{label}</span>
              </div>
              <Switch
                label={label}
                checked={settings[key]}
                disabled={disabled}
                onChange={(on) => onChange({ [key]: on })}
              />
            </div>
          );
        })}
      </div>
    </section>
  );
}
