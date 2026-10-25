import { type CSSProperties, useId } from "react";
import {
  overlayOpacityMax,
  overlayOpacityMin,
  overlayOpacityStep,
} from "../../shared/overlay";
import type { OverlaySettings } from "../overlay-settings";
import type { Language } from "../store";
import { Segmented, Switch } from "../ui/controls";
import "./pages.css";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/**
 * Settings → 浮层 (HUD-05, ADR-0063): the overlay's toasts, position and
 * opacity. They are the UI's own, kept in localStorage like the language, so
 * they do not wait for the mapping service and do not touch config.json.
 */
export function OverlaySection({
  language,
  settings,
  onChange,
}: {
  language: Language;
  settings: OverlaySettings;
  onChange: (change: Partial<OverlaySettings>) => void;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const opacityId = useId();
  const fill =
    ((settings.opacity - overlayOpacityMin) /
      (overlayOpacityMax - overlayOpacityMin)) *
    100;
  return (
    <section className="page-section" data-testid="overlay-settings">
      <h2 className="section-title">{t("浮层", "Overlay")}</h2>
      <div className="card">
        <div className="row">
          <div className="row-text">
            <span className="row-title">{t("提示消息", "Toasts")}</span>
          </div>
          <Switch
            label={t("提示消息", "Toasts")}
            checked={settings.toasts}
            onChange={(toasts) => onChange({ toasts })}
          />
        </div>
        <div className="row">
          <div className="row-text">
            <span className="row-title">{t("位置", "Position")}</span>
          </div>
          <Segmented
            label={t("位置", "Position")}
            small
            value={settings.position}
            options={[
              { value: "bottom", label: t("底部", "Bottom") },
              { value: "top", label: t("顶部", "Top") },
            ]}
            onChange={(position) => onChange({ position })}
          />
        </div>
        <div className="row">
          <div className="row-text">
            <label className="row-title" htmlFor={opacityId}>
              {t("不透明度", "Opacity")}
            </label>
          </div>
          <span className="page-range">
            <input
              id={opacityId}
              className="page-range-input"
              type="range"
              min={overlayOpacityMin}
              max={overlayOpacityMax}
              step={overlayOpacityStep}
              value={settings.opacity}
              aria-valuetext={`${settings.opacity}%`}
              style={{ "--calibration-fill": `${fill}%` } as CSSProperties}
              onChange={(event) =>
                onChange({ opacity: Number(event.currentTarget.value) })
              }
            />
            <output className="page-range-value" htmlFor={opacityId}>
              {settings.opacity}%
            </output>
          </span>
        </div>
      </div>
    </section>
  );
}
