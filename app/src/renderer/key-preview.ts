// The key preview line of the mapping page (UI-44): which control the pad
// pressed and what it sends, from an engine decision. Nothing here talks
// to the daemon.
import { macroName } from "../shared/macro-names";
import { analogControlPart } from "./control-parts";
import { rawKeyName } from "./controller-chips";
import { actionText, shortText } from "./editor-model";
import type { KeyPreview, Language } from "./store";
import { chordKeys } from "./ui/Keycaps";

const arrows: Record<string, string> = {
  up: "↑",
  down: "↓",
  left: "←",
  right: "→",
  upleft: "↖",
  upright: "↗",
  downleft: "↙",
  downright: "↘",
};

// A raw half axis: y grows downwards, so y- is up.
const axisArrows: Record<string, string> = {
  "x-": "←",
  "x+": "→",
  "y-": "↑",
  "y+": "↓",
};

function own(record: Record<string, string>, key: string): string | undefined {
  return Object.hasOwn(record, key) ? record[key] : undefined;
}

/**
 * How the pad names the control of a decision: the label printed on it
 * (labels is controllerLabels), with a direction for a stick or the
 * D-pad's diagonals, such as "A", "LT", "LS ↑" or "↖".
 */
export function pressedControlName(
  control: string,
  labels: Record<string, string>,
  language: Language,
): string {
  const label = own(labels, control);
  if (label) return label;
  if (control === "lt") return own(labels, "lefttrigger") ?? "LT";
  if (control === "rt") return own(labels, "righttrigger") ?? "RT";
  const part = analogControlPart(control, language);
  if (part && /^[lr]t\./.test(control)) {
    const name =
      own(labels, control[0] === "l" ? "lefttrigger" : "righttrigger") ??
      `${control[0].toUpperCase()}T`;
    return `${name} ${part}`;
  }
  const stick = /^(l|r)s\.([a-z]+)$/.exec(control);
  if (stick) {
    const name =
      own(labels, stick[1] === "l" ? "leftstick" : "rightstick") ??
      `${stick[1].toUpperCase()}S`;
    const arrow = own(arrows, stick[2]);
    return arrow ? `${name} ${arrow}` : part ? `${name} ${part}` : name;
  }
  const dpad = /^dpad\.([a-z]+)$/.exec(control);
  if (dpad && own(arrows, dpad[1])) return own(arrows, dpad[1]) as string;
  const axis = /^(left|right)([xy][+-])$/.exec(control);
  if (axis) {
    const name = own(labels, `${axis[1]}stick`) ?? control;
    return `${name} ${own(axisArrows, axis[2])}`;
  }
  const trigger = /^(left|right)trigger[+-]$/.exec(control);
  if (trigger) {
    const name = own(labels, `${trigger[1]}trigger`) ?? control;
    return `${name} ${rawKeyName(control, language)}`;
  }
  return control;
}

/**
 * The chords of a sequence as the engine prints it, "[Ctrl+K,S]" without
 * the brackets. "," is also a key: it belongs to the chord when the chord
 * is empty so far or ends with "+".
 */
function sequenceChords(list: string): string[] {
  const chords: string[] = [];
  let current = "";
  for (const character of list) {
    if (character === "," && current !== "" && !current.endsWith("+")) {
      chords.push(current);
      current = "";
    } else current += character;
  }
  if (current) chords.push(current);
  return chords;
}

/** A text in a decision, quoted as Go quotes it: the text, or what is between the quotes. */
function unquote(quoted: string): string {
  try {
    const value: unknown = JSON.parse(quoted);
    if (typeof value === "string") return value;
  } catch {
    // Go writes some escapes JSON does not (\x00): the text stays as written.
  }
  return quoted.slice(1, -1);
}

const quoted = String.raw`"(?:[^"\\]|\\.)*"`;
const textDecision = new RegExp(`^text (${quoted})$`, "s");
const textStep = new RegExp(`^text (${quoted})`, "s");

/**
 * The steps of a sequence in a decision, such as "Ctrl+A, wait 100, text "hi"",
 * as the line shows them. They are apart by a comma and a space, which a text
 * may hold (it is quoted) and so may a key: "," is one, so a step ends at the
 * first ", " after its first character.
 */
function sequenceSteps(list: string, language: Language | undefined): string[] {
  const steps: string[] = [];
  let rest = list;
  while (rest) {
    const text = textStep.exec(rest);
    const wait = /^wait (\d+)(?=, |$)/.exec(rest);
    let length: number;
    if (text) {
      steps.push(`“${shortText(unquote(text[1]))}”`);
      length = text[0].length;
    } else if (wait) {
      steps.push(`${wait[1]} ms`);
      length = wait[0].length;
    } else {
      const end = rest.indexOf(", ", 1);
      length = end < 0 ? rest.length : end;
      steps.push(chordKeys(rest.slice(0, length), language).join("+"));
    }
    rest = rest.slice(length);
    if (!rest.startsWith(", ")) break;
    rest = rest.slice(2);
  }
  return steps;
}

/**
 * What the action sends, as the line shows it: the shortcut of a keys
 * action ("tap Ctrl+Z", "hold Shift", "tap [Ctrl+K,S]"), the name of the
 * script a macro action runs ("macro 复制全部.ts", without the extension,
 * SCR-50), the label or name of a legacy script action, a precision
 * action with its share of the speed ("mouse precision 0.3", MOU-05) or the
 * key hints action ("hud hints", HUD-03), a text ('text "hi"', OUT-21) or a
 * sequence ('sequence: Ctrl+A, wait 100, text "hi"', MAP-22). null for other
 * actions (layers, profiles, a pointer jump). language names the wheel and
 * media keys ("Ctrl+滚轮↑", "音量加").
 */
export function previewAction(
  action: string,
  scriptActions: readonly { name: string; label?: string }[] = [],
  language?: Language,
): string | null {
  const keys = /^(?:tap|hold) (.+)$/s.exec(action);
  if (keys) {
    const spec = keys[1];
    const chords =
      spec.startsWith("[") && spec.endsWith("]")
        ? sequenceChords(spec.slice(1, -1))
        : [spec];
    return chords
      .map((chord) => chordKeys(chord, language).join("+"))
      .join(", ");
  }
  const typed = textDecision.exec(action);
  if (typed)
    return actionText({ type: "text", text: unquote(typed[1]) }, language);
  const sequence = /^sequence: (.+)$/s.exec(action);
  if (sequence) return sequenceSteps(sequence[1], language).join(", ");
  const precision = /^mouse precision(?: (\S+))?$/.exec(action);
  if (precision) {
    const factor = Number(precision[1]);
    return actionText(
      {
        type: "mouse",
        op: "precision",
        ...(Number.isFinite(factor) && precision[1] ? { factor } : {}),
      },
      language,
    );
  }
  if (action === "hud hints")
    return actionText({ type: "hud", op: "hints" }, language);
  const macro = /^macro (.+)$/s.exec(action);
  if (macro) return macroName(macro[1]);
  const script = /^script (.+)$/s.exec(action);
  if (script) {
    const name = script[1];
    return (
      scriptActions.find((entry) => entry.name === name)?.label?.trim() || name
    );
  }
  return null;
}

/** The line's parts: the control, what it sends, and whether that is a script (macro). */
export interface KeyPreviewLine {
  control: string;
  sends: string;
  /** A macro action: the line shows the script icon before its name. */
  macro: boolean;
}

/** The parts of the line; null when it shows nothing. */
export function keyPreviewLine(
  preview: Pick<KeyPreview, "control" | "action">,
  labels: Record<string, string>,
  scriptActions: readonly { name: string; label?: string }[] | undefined,
  language: Language,
): KeyPreviewLine | null {
  const sends = previewAction(preview.action, scriptActions, language);
  if (!sends) return null;
  return {
    control: pressedControlName(preview.control, labels, language),
    sends,
    macro: preview.action.startsWith("macro "),
  };
}

/** The whole line, such as "A → Ctrl+Shift+Z" or "A → 复制全部"; null when it shows nothing. */
export function keyPreviewText(
  preview: Pick<KeyPreview, "control" | "action">,
  labels: Record<string, string>,
  scriptActions: readonly { name: string; label?: string }[] | undefined,
  language: Language,
): string | null {
  const line = keyPreviewLine(preview, labels, scriptActions, language);
  return line && `${line.control} → ${line.sends}`;
}
