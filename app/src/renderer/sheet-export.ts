// Exporting the shortcut table (OUT-18) from the More menu: the format
// choice and the save through Main.
import { useState } from "react";
import { useStore } from "zustand";
import { listControlName } from "./BindingsListDialog";
import { controllerLabels } from "./controller-model";
import { editorError, savedForm } from "./editor-model";
import type { EditorStore } from "./editor-store";
import { withInherited } from "./inheritance";
import { defaultProfileName, isDefaultProfile } from "./library-model";
import {
  type SheetOptions,
  shortcutSheet,
  shortcutSheetMarkdown,
  shortcutSheetPNG,
} from "./shortcut-sheet";
import type { Language, PadmateState } from "./store";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

export type SheetFormat = "png" | "markdown";

export function useSheetExport({
  state,
  editor,
  language,
  onMessage,
}: {
  state: Pick<PadmateState, "api" | "status" | "selectedDevice">;
  editor: EditorStore;
  language: Language;
  onMessage: (message: string) => void;
}) {
  const draft = useStore(editor, (value) => value.draft);
  const parent = useStore(editor, (value) => value.parent);
  const id = useStore(editor, (value) => value.id);
  const [format, setFormat] = useState<SheetFormat>("png");
  const devices = state.status?.devices ?? [];
  const info = (
    devices.find((entry) => entry.info.id === state.selectedDevice) ??
    devices[0]
  )?.info;
  const labels = controllerLabels(
    info?.type ?? "",
    info?.buttonLabels,
    info?.buttonMask,
  );
  const options: SheetOptions = {
    language,
    controlName: (control) => listControlName(control, labels, language),
    ...(isDefaultProfile(id)
      ? { profileName: defaultProfileName(language) }
      : {}),
  };
  const exportSheet = async () => {
    if (!draft) return;
    try {
      // The table lists what is saved: a shortcut cleared in the drawer is
      // not in it, nor counted as a setting (UI-43). It lists what the profile
      // takes from the profile it is based on too (PRO-19).
      const sheet = shortcutSheet(
        withInherited(savedForm(draft, parent), parent),
        options,
      );
      const result = await state.api.exportCheatSheet(
        format,
        format === "markdown"
          ? shortcutSheetMarkdown(sheet)
          : shortcutSheetPNG(sheet),
      );
      onMessage(
        result.saved
          ? tx(language, "对照表已导出", "Shortcut table exported")
          : tx(language, "已取消导出", "Export canceled"),
      );
    } catch (error) {
      onMessage(editorError(error));
    }
  };
  return { draft, format, setFormat, exportSheet };
}
