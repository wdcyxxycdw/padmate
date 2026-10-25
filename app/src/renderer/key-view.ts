// Which set of bindings the mapping page shows (UI-30, ADR-0069): the usual
// one, or the set View or Menu switches to while held. The choice itself is
// the editor store's `view`; these are the rules around it that do not need
// the store.
import type { ScriptRegistration } from "../shared/api";
import { functionKeyLabels, functionKeysOf } from "./controller-model";
import type { KeyView } from "./editor-model";
import type { Language } from "./store";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/** The parts of a pad's description that decide its function keys and their names. */
export interface PadNames {
  type: string;
  buttonLabels?: Record<string, string>;
  buttonMask?: number;
}

/**
 * The view to show: the one asked for, unless this pad lacks that function
 * key (a single Joy-Con held sideways has no View), then the usual one.
 */
export function effectiveView(view: KeyView, pad: PadNames): KeyView {
  if (view === "normal") return view;
  return functionKeysOf(pad.type, pad.buttonMask).includes(view)
    ? view
    : "normal";
}

/** A view as a sentence names it: 平时, 按住 View, 按住 Menu. */
export function viewName(
  view: KeyView,
  pad: PadNames,
  language: Language,
): string {
  if (view === "normal") return tx(language, "平时", "Normal");
  const label = functionKeyLabels(pad.type, pad.buttonLabels, pad.buttonMask)[
    view
  ];
  return tx(language, `按住 ${label}`, `Hold ${label}`);
}

/** The views a pad has, in the order of the switch: usual first. */
export function viewOptions(
  pad: PadNames,
  language: Language,
): { value: KeyView; label: string }[] {
  const views: KeyView[] = [
    "normal",
    ...functionKeysOf(pad.type, pad.buttonMask),
  ];
  return views.map((value) => ({
    value,
    label: viewName(value, pad, language),
  }));
}

/**
 * The script registrations that apply in a view. A script cannot register
 * into a function key's set, so a control the set binds is the set's alone,
 * and only a global handler (which comes before every set) reaches it.
 */
export function viewRegistrations(
  registrations: ScriptRegistration[],
  view: KeyView,
): ScriptRegistration[] {
  return view === "normal"
    ? registrations
    : registrations.filter((entry) => entry.scope === "global");
}
