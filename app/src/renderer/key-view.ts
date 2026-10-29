// Which set of bindings the mapping page shows (UI-30, ADR-0075): the usual
// one, or the function set that applies while the function key is held. The
// choice itself is the editor store's `view`; these are the rules around it
// that do not need the store.
import type { ScriptRegistration } from "../shared/api";
import { functionKeyLabel, padHasFunctionKey } from "./controller-model";
import type { FunctionKey, KeyView } from "./editor-model";
import type { Language } from "./store";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/** The parts of a pad's description that decide its function key and its name. */
export interface PadNames {
  type: string;
  buttonLabels?: Record<string, string>;
  buttonMask?: number;
}

/**
 * The view to show: the one asked for, unless this pad lacks the function key
 * (a single Joy-Con held sideways has no View), then the usual one.
 */
export function effectiveView(
  view: KeyView,
  pad: PadNames,
  functionKey: FunctionKey,
): KeyView {
  if (view === "normal") return view;
  return padHasFunctionKey(functionKey, pad.type, pad.buttonMask)
    ? view
    : "normal";
}

/**
 * What the pad prints on the function key, which the list of all keys names
 * it by: Menu, Options, +; View, Create, −; Xbox, PS, Home.
 */
export function functionKeyName(
  functionKey: FunctionKey,
  pad: PadNames,
): string {
  return functionKeyLabel(
    functionKey,
    pad.type,
    pad.buttonLabels,
    pad.buttonMask,
  );
}

/**
 * A view as a sentence names it: 平时, 按住功能键. The function key is not
 * named by the pad's own name for it (ADR-0076): it is one key, and the
 * settings say which.
 */
export function viewName(view: KeyView, language: Language): string {
  return view === "normal"
    ? tx(language, "平时", "Normal")
    : tx(language, "按住功能键", "Hold function key");
}

/**
 * The views a pad has, in the order of the switch: usual first, then the
 * function set when the pad has the function key.
 */
export function viewOptions(
  pad: PadNames,
  language: Language,
  functionKey: FunctionKey,
): { value: KeyView; label: string }[] {
  const views: KeyView[] = padHasFunctionKey(
    functionKey,
    pad.type,
    pad.buttonMask,
  )
    ? ["normal", "function"]
    : ["normal"];
  return views.map((value) => ({
    value,
    label: viewName(value, language),
  }));
}

/**
 * The script registrations that apply in a view. A script cannot register
 * into the function set, so a control the set binds is the set's alone, and
 * only a global handler (which comes before every set) reaches it.
 */
export function viewRegistrations(
  registrations: ScriptRegistration[],
  view: KeyView,
): ScriptRegistration[] {
  return view === "normal"
    ? registrations
    : registrations.filter((entry) => entry.scope === "global");
}
