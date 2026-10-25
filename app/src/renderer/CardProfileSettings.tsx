import { useEffect, useRef, useState } from "react";
import { useStore } from "zustand";
import type { PadmateAPI } from "../shared/api";
import type { ProfileTiming } from "../shared/generated/schema";
import { createEditorStore, type EditorStore } from "./editor-store";
import { ProfileSettingsDialog } from "./ProfileSettingsDialog";
import type { Language } from "./store";

/**
 * 配置设置 for the profile of a card on the 配置 page (UI-27), without
 * activating it, switching the mapping page's profile or leaving the page.
 *
 * Each profile has one editor at a time, so that its autosave, revision and
 * conflict handling are the mapping page's and two stores never write one file:
 * the profile the mapping page edits (the one in use) is edited with that
 * editor; any other profile gets an editor of its own for as long as the
 * dialog is open.
 */
export function CardProfileSettings({
  id,
  mappingEditor,
  activeId,
  api,
  globalTiming,
  profileChanges,
  language,
  onEditor,
  onClose,
}: {
  /** The profile of the card; null while the dialog is closed. */
  id: string | null;
  /** The mapping page's editor, which holds the profile in use. */
  mappingEditor: EditorStore;
  /** The profile in use: the one mappingEditor holds. */
  activeId: string;
  api: PadmateAPI;
  /** config.json's timing, the placeholders of the timing rows. */
  globalTiming: ProfileTiming;
  /** Counts profile.changed events, which tell an editor to look at its file. */
  profileChanges: number;
  language: Language;
  /** Tells the shell which editor the dialog edits, so closing the window saves it. */
  onEditor: (editor: EditorStore | null) => void;
  /** Closes the dialog; the shell saves the editor's edits first. */
  onClose: () => void;
}) {
  if (id === null) return null;
  if (id === activeId)
    return (
      <SharedEditorDialog
        editor={mappingEditor}
        language={language}
        onEditor={onEditor}
        onClose={onClose}
      />
    );
  return (
    <OwnEditorDialog
      key={id}
      id={id}
      api={api}
      globalTiming={globalTiming}
      profileChanges={profileChanges}
      language={language}
      onEditor={onEditor}
      onClose={onClose}
    />
  );
}

function SharedEditorDialog({
  editor,
  language,
  onEditor,
  onClose,
}: {
  editor: EditorStore;
  language: Language;
  onEditor: (editor: EditorStore | null) => void;
  onClose: () => void;
}) {
  useEffect(() => {
    onEditor(editor);
    return () => onEditor(null);
  }, [editor, onEditor]);
  return (
    <ProfileSettingsDialog
      open
      editor={editor}
      language={language}
      onClose={onClose}
    />
  );
}

/**
 * The dialog with an editor of its own for profile id, made when it opens and
 * let go when it closes: the editor is activated on the profile (no device, as
 * the mapping page has none either without a controller), follows
 * profile.changed like the mapping page's, and saves what is left when the
 * dialog goes (the shell has waited for it by then; this is for the dialog
 * that goes for another reason).
 *
 * The editor is made by the effect, one per run: a store activated by one run
 * and let go of by its cleanup must not be the one the next run (StrictMode
 * replays them in development) shows, because an editor asked for the same
 * profile twice loads it only once and the cleanup would then empty it.
 */
function OwnEditorDialog({
  id,
  api,
  globalTiming,
  profileChanges,
  language,
  onEditor,
  onClose,
}: {
  id: string;
  api: PadmateAPI;
  globalTiming: ProfileTiming;
  profileChanges: number;
  language: Language;
  onEditor: (editor: EditorStore | null) => void;
  onClose: () => void;
}) {
  const [editor, setEditor] = useState<EditorStore | null>(null);
  useEffect(() => {
    const own = createEditorStore(api);
    void own
      .getState()
      .activate({ id, device: "", connection: "profile-card" });
    setEditor(own);
    onEditor(own);
    return () => {
      onEditor(null);
      // Save what is left, then let go of the profile.
      void own
        .getState()
        .flush()
        .catch(() => false)
        .then(() => own.getState().activate(null))
        .catch(() => {});
    };
  }, [api, id, onEditor]);
  if (!editor) return null;
  return (
    <OwnEditorBody
      editor={editor}
      globalTiming={globalTiming}
      profileChanges={profileChanges}
      language={language}
      onClose={onClose}
    />
  );
}

function OwnEditorBody({
  editor,
  globalTiming,
  profileChanges,
  language,
  onClose,
}: {
  editor: EditorStore;
  globalTiming: ProfileTiming;
  profileChanges: number;
  language: Language;
  onClose: () => void;
}) {
  useEffect(() => {
    editor.getState().setGlobalTiming(globalTiming);
  }, [editor, globalTiming]);
  // A profile that cannot be read has no dialog to show: the shell says so.
  const error = useStore(editor, (state) => state.error);
  useEffect(() => {
    if (error) onClose();
  }, [error, onClose]);
  // Only a change that comes while the dialog is open: the editor has just
  // read the file.
  const seen = useRef(profileChanges);
  useEffect(() => {
    if (profileChanges === seen.current) return;
    seen.current = profileChanges;
    void editor.getState().profileChanged();
  }, [editor, profileChanges]);
  return (
    <ProfileSettingsDialog
      open
      editor={editor}
      language={language}
      onClose={onClose}
    />
  );
}
