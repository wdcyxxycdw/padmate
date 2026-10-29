import { useStore } from "zustand";
import type { EditorStore } from "./editor-store";
import { effectiveView, type PadNames, viewOptions } from "./key-view";
import type { Language } from "./store";
import { Segmented } from "./ui/controls";

/**
 * The switch at the top of the mapping page (UI-30, ADR-0075): the usual
 * bindings, or the function set that applies while the function key (Menu
 * unless the settings say otherwise) is held. The page edits the one chosen;
 * the choice is not saved in the profile. A pad without the function key has
 * no set to switch to, so it has no switch.
 */
export function KeyViewSwitch({
  editor,
  pad,
  language,
}: {
  editor: EditorStore;
  pad: PadNames;
  language: Language;
}) {
  const stored = useStore(editor, (state) => state.view);
  const loaded = useStore(editor, (state) => !!state.draft);
  const functionKey = useStore(editor, (state) => state.functionKey);
  const options = viewOptions(pad, language, functionKey);
  if (options.length < 2) return null;
  return (
    <div className="key-view-bar">
      <Segmented
        label={language === "en" ? "Keys being edited" : "编辑的按键"}
        testId="key-view"
        small
        disabled={!loaded}
        options={options}
        value={effectiveView(stored, pad, functionKey)}
        onChange={(view) => editor.getState().setView(view)}
      />
    </div>
  );
}
