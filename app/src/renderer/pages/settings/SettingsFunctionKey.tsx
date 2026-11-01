import {
  ControlIcon,
  type ControlIconSpec,
  controlIcon,
} from "../../controller/control-icon";
import {
  controllerLabels,
  familyName,
  padHasFunctionKey,
} from "../../controller/controller-model";
import { type FunctionKey, functionKeys } from "../../editor/editor-model";
import type { PadNames } from "../../editor/key-view";
import type { Language } from "../../state/store";
import { Segmented } from "../../ui/controls";
import "../pages.css";

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
 * connected controller gives it (ADR-0013; the same icons as the key
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
 * Settings → 通用 → 功能键 (ADR-0012): the one key that switches the
 * other keys to the function set while it is held. Global, for every profile
 * and controller, and saved like the other rows of the page. A row is its
 * name and its choice, nothing else: the three keys, each an icon and the name
 * the connected controller gives it (ADR-0013).
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
