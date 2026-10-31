// A control's name as the UI lists it (UI-08, SCR-50): the key list and the
// scripts page name every control the daemon can report the same way.
import { calloutFor, calloutOf } from "./control-callout";
import { analogControlPart } from "./control-parts";
import { rawKeyName } from "./controller-chips";
import { canonicalControl } from "./editor-model";
import type { Language } from "./store";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

const arrows: Record<string, string> = {
  up: "↑",
  down: "↓",
  left: "←",
  right: "→",
  upright: "↗",
  downright: "↘",
  downleft: "↙",
  upleft: "↖",
};

/**
 * A control as printed on this controller, and for a key inside a stick,
 * trigger or the d-pad also which direction, raw axis or half press it is,
 * e.g. "LS ↖", "十字键 ↘", "LS 原始轴向右", "LT 半按", "LS 重推", "LT 按到底".
 */
export function listControlName(
  control: string,
  labels: Record<string, string>,
  language: Language,
): string {
  const key = canonicalControl(control);
  const callout = calloutFor(key);
  // A stick's press is named like the stick: LS, or L3 on a PlayStation pad;
  // with no labels it is the stick's id, never the press cell's selection id.
  const name =
    labels[callout] ?? labels[calloutOf(callout)] ?? calloutOf(callout);
  const part = analogControlPart(key, language);
  if (part) return `${name} ${part}`;
  const direction = /^(ls|rs|dpad)\.(\w+)$/.exec(key);
  if (direction) {
    const where =
      direction[1] === "dpad" ? tx(language, "十字键", "D-pad") : name;
    if (direction[2] === "active")
      return `${where} ${tx(language, "任意方向", "any direction")}`;
    return `${where} ${arrows[direction[2]] ?? direction[2]}`;
  }
  if (/^(?:left|right)(?:[xy]|trigger)[+-]$/.test(key))
    return `${name} ${rawKeyName(key, language)}`;
  return labels[key] ?? name;
}
