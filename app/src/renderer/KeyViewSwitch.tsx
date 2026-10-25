import { useStore } from "zustand";
import type { EditorStore } from "./editor-store";
import { effectiveView, type PadNames, viewOptions } from "./key-view";
import type { Language } from "./store";
import { Segmented } from "./ui/controls";

/**
 * The switch at the top of the mapping page (UI-30, ADR-0069): the usual
 * bindings, or the set View or Menu switches to while held. The page edits
 * the one chosen; the choice is not saved in the profile.
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
  return (
    <div className="key-view-bar">
      <Segmented
        label={language === "en" ? "Keys being edited" : "编辑的按键"}
        testId="key-view"
        small
        disabled={!loaded}
        options={viewOptions(pad, language)}
        value={effectiveView(stored, pad)}
        onChange={(view) => editor.getState().setView(view)}
      />
    </div>
  );
}
