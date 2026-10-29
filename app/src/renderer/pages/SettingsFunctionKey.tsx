import { useEffect, useRef, useState } from "react";
import type { PadmateAPI } from "../../shared/api";
import {
  ControlIcon,
  type ControlIconSpec,
  controlIcon,
} from "../control-icon";
import {
  controllerLabels,
  familyName,
  padHasFunctionKey,
} from "../controller-model";
import { type FunctionKey, functionKeys } from "../editor-model";
import type { PadNames } from "../key-view";
import type { Language } from "../store";
import { Dialog, Notice, Segmented } from "../ui/controls";
import "./pages.css";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/** One of the keys to choose from: as the controller shows it. */
export interface FunctionKeyChoice {
  key: FunctionKey;
  /** What the controller prints on it: Menu, Options, +, … */
  name: string;
  icon: ControlIconSpec;
}

// The names when no controller is connected: an Xbox controller's.
const xbox: PadNames = { type: "xboxone" };

/**
 * The three keys to choose from, each with the name and the icon the
 * connected controller gives it (ADR-0076; the same icons as the key
 * preview), or an Xbox controller's when none is connected. The setting is
 * for every controller: a key the connected one lacks (View on a single
 * Joy-Con held sideways) is named as an Xbox controller names it.
 */
export function functionKeyChoices(pad?: PadNames): FunctionKeyChoice[] {
  return functionKeys.map((key) => {
    const from =
      pad && padHasFunctionKey(key, pad.type, pad.buttonMask) ? pad : xbox;
    const labels = controllerLabels(
      from.type,
      from.buttonLabels,
      from.buttonMask,
    );
    return {
      key,
      name: labels[key] ?? key,
      icon: controlIcon(key, familyName(from.type), labels),
    };
  });
}

/**
 * Settings → 通用 → 功能键 (UI-30, ADR-0075): the one key that switches the
 * other keys to the function set while it is held. Global, for every profile
 * and controller, and saved like the other rows of the page. A row is its
 * name and its choice, nothing else: the three keys, each an icon and the name
 * the connected controller gives it (ADR-0076).
 */
export function FunctionKeyRow({
  language,
  value,
  disabled,
  pad,
  onChange,
}: {
  language: Language;
  value: FunctionKey;
  disabled: boolean;
  /** The connected controller, whose names the keys have. */
  pad?: PadNames;
  onChange: (key: FunctionKey) => void;
}) {
  const name = tx(language, "功能键", "Function key");
  return (
    <div className="row" data-testid="function-key-setting">
      <div className="row-text">
        <span className="row-title">{name}</span>
      </div>
      <Segmented<FunctionKey>
        label={name}
        className="page-seg control-choice"
        disabled={disabled}
        options={functionKeyChoices(pad).map((choice) => ({
          value: choice.key,
          label: (
            <>
              <ControlIcon icon={choice.icon} />
              <span>{choice.name}</span>
            </>
          ),
        }))}
        value={value}
        onChange={onChange}
      />
    </div>
  );
}

/**
 * What choosing the Xbox button as the function key asks of the user
 * (ADR-0075): Windows opens the Xbox Game Bar from that button too, which
 * padmate can neither stop nor change, so the user turns it off in Windows
 * Settings. The setting is saved already; either button only closes this.
 * padmate does not read the Game Bar's setting and cannot tell whether it is
 * off. A dialog and not a banner: the user has to see it.
 */
export function GameBarDialog({
  open,
  language,
  api,
  onClose,
}: {
  open: boolean;
  language: Language;
  api: Pick<PadmateAPI, "openGameBarSettings">;
  onClose: () => void;
}) {
  const t = (zh: string, en: string) => tx(language, zh, en);
  const [failed, setFailed] = useState(false);
  const understood = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!open) return;
    setFailed(false);
    // The dialog's own effect (a child) has run showModal already.
    understood.current?.focus();
  }, [open]);
  const openSettings = async () => {
    try {
      await api.openGameBarSettings();
    } catch {
      setFailed(true);
      return;
    }
    onClose();
  };
  return (
    <Dialog
      open={open}
      title={t("关闭 Xbox Game Bar 的手柄按钮", "Turn off the Game Bar button")}
      closeLabel={t("关闭提示", "Close this notice")}
      onClose={onClose}
      className="confirm-dialog"
      footer={
        <>
          <span className="spacer" />
          <button
            type="button"
            className="btn"
            onClick={() => void openSettings()}
          >
            {t("打开 Windows 设置", "Open Windows Settings")}
          </button>
          <button
            ref={understood}
            type="button"
            className="btn-primary"
            onClick={onClose}
          >
            {t("知道了", "Got it")}
          </button>
        </>
      }
    >
      <p className="dialog-text">
        {t(
          "按下 Xbox 键时，Windows 的 Xbox Game Bar 也会打开，padmate 无法阻止。",
          "Windows also opens the Xbox Game Bar when the Xbox button is pressed, and padmate cannot stop it.",
        )}
      </p>
      <p className="dialog-text">
        {t(
          "请在 Windows 设置 → 游戏 → Game Bar 中，关闭“允许控制器打开 Game Bar”（旧版 Windows 里叫“使用控制器上的此按钮打开 Xbox Game Bar”）。Steam 运行时也可能占用这个键。",
          "In Windows Settings → Gaming → Game Bar, turn off “Allow your controller to open Game Bar” (older versions of Windows call it “Open Xbox Game Bar using this button on a controller”). Steam may also take this button while it runs.",
        )}
      </p>
      {failed && (
        <Notice kind="error" role="alert">
          {t(
            "无法打开 Windows 设置。",
            "Windows Settings could not be opened.",
          )}
        </Notice>
      )}
    </Dialog>
  );
}
