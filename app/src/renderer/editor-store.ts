import { createStore } from "zustand/vanilla";
import type {
  MacroList,
  PadmateAPI,
  ProfileDiagnostic,
  ProfileDocument,
  ProfileEntry,
  ScriptDescription,
  ScriptRegistration,
} from "../shared/api";
import type { Profile, ProfileTiming } from "../shared/generated/schema";
import { EditHistory } from "./editor-history";
import {
  defaultFunctionKey,
  editorError,
  type FunctionKey,
  isLabelOnlyWarning,
  type KeyView,
  type LayerUsage,
  layerUsage,
  savedForm,
  stableStringify,
  withNewFunctionSet,
} from "./editor-model";
import { isClearedInherited, type ParentProfile } from "./inheritance";

/** A gesture's edits that come closer together than this are one step. */
export const MERGE_IDLE_MS = 400;

export interface EditorContext {
  id: string;
  device: string;
  connection: string;
}

/** How an edit joins the undo history (UI-43). */
export interface EditOptions {
  /**
   * Edits with the same key in a row (a slider drag, arrow keys held on
   * it) are one undo step and one save, made once they pause for
   * MERGE_IDLE_MS or another edit comes.
   */
  merge?: string;
  /** With merge: the gesture goes on (a pointer is down), so no pause ends it. */
  hold?: boolean;
  /**
   * false: not an undo step, and undo and redo keep it. Renaming the
   * profile is not undone (UI-43), so a step taken back keeps the name.
   */
  history?: false;
}

/** Why the draft is not on disk: "failed" can be retried, "invalid" needs a fix or an undo. */
export type SaveProblem = "" | "failed" | "invalid";

interface EditorState {
  id: string;
  context: EditorContext | null;
  draft: Profile | null;
  revision: string;
  /** The draft differs from the file: a save is waiting, running or refused. */
  unsaved: boolean;
  /**
   * An edit waits in a merge group or a save is running. The scripts page
   * keeps renaming, deleting and removing, which rewrite profile.json,
   * unavailable meanwhile (SCR-50).
   */
  saving: boolean;
  /** Reading the profile; nothing can be edited meanwhile. */
  busy: boolean;
  /** Why the profile could not be read. */
  error: string;
  saveProblem: SaveProblem;
  /** The message of a failed save ("failed"). */
  saveError: string;
  /**
   * The file changed elsewhere and was read again, which dropped an edit
   * that was not saved yet (a revision conflict). Cleared by the next edit.
   */
  reloaded: boolean;
  /**
   * The open profile changed to another one (the daemon switched, or
   * padmate reconnected to another) while an edit of the previous one
   * could not be saved; that edit is gone. Cleared by the next edit.
   */
  discarded: boolean;
  /** Whether the last save is in effect; null before the first save. */
  applied: boolean | null;
  /** Why the file is not in effect: its errors, or the daemon's reason after a save. */
  problem: string;
  diagnostics: ProfileDiagnostic[];
  canUndo: boolean;
  canRedo: boolean;
  /** Counts the times the draft was read from disk; edits and saves keep it. */
  loads: number;
  profiles: ProfileEntry[];
  description: ScriptDescription | null;
  registrations: ScriptRegistration[];
  scriptError: string;
  /**
   * The script library (macros.list, SCR-50, SCR-51): the drawer's script
   * list and the files bindings run that are missing. null until read.
   */
  macros: MacroList | null;
  /**
   * The profile this one's file says it extends (PRO-19, ADR-0070), merged
   * with its own parents, to show what this one takes from it; null when it
   * extends none (the usual case: the UI no longer writes `extends`,
   * ADR-0076), before it is read, or when it cannot be (the profile's problem
   * says why). It is what the parent's file holds, not this profile's draft:
   * the draft is always this profile's own file.
   */
  parent: ParentProfile | null;
  /**
   * Which set of bindings the mapping page shows and edits: the usual one, or
   * the function set that applies while the function key is held (UI-30,
   * ADR-0075). View state, not part of the profile; it is the usual one again
   * whenever a profile is loaded.
   */
  view: KeyView;
  setView(view: KeyView): void;
  /**
   * The function key (config.json functionKey): Menu until the app tells
   * otherwise, and the same for every profile and controller. It is held
   * here, as globalTiming is, so the editor's parts read it from one place.
   */
  functionKey: FunctionKey;
  setFunctionKey(key: FunctionKey): void;
  /** What the file uses beyond the base layer; null until a draft loads. */
  usage: LayerUsage | null;
  /** Non-empty when the GUI must not edit this profile (ADR-0045). */
  readOnly: "" | "defaultLayer";
  globalTiming: ProfileTiming;
  setGlobalTiming(timing: ProfileTiming): void;
  /**
   * Shows another profile, device or connection; saves the open one first.
   * An edit that cannot be saved stays when only the pad changes, and is
   * saved again when the same profile comes back on a new connection,
   * unless the file changed meanwhile (then reloaded says so). Another
   * profile drops it (discarded says so).
   */
  activate(context: EditorContext | null): Promise<void>;
  /** Commits one edit: it is saved (and applied) at once, or with its merge group. */
  edit(profile: Profile, options?: EditOptions): void;
  /** Applies the draft before the last step and saves it; false when there is none. */
  undo(): boolean;
  redo(): boolean;
  /** Saves again after a failed save. */
  retry(): void;
  dismissReloaded(): void;
  dismissDiscarded(): void;
  /**
   * Waits until the edits of profile id (the open one by default) are on
   * disk, retrying a failed save once. Resolves to false when they are not:
   * the save failed again or the daemon refused the draft. Other profiles
   * have nothing pending, so they resolve to true at once.
   */
  flush(id?: string): Promise<boolean>;
  /**
   * Reads profile id (the open one by default) from disk again after a
   * running save, dropping edits that are not saved. The undo history is
   * cleared when the file changed; clearHistory clears it in any case.
   */
  reload(id?: string): Promise<void>;
  /** Forgets the undo and redo steps of profile id (the open one by default). */
  clearHistory(id?: string): void;
  refreshMetadata(): Promise<void>;
  /** Reads macros.list again, e.g. after a profile.changed event. */
  refreshMacros(): Promise<void>;
  /** Reads the parent again (profiles.resolve), e.g. after the parent's file changed. */
  refreshParent(): Promise<void>;
  /** profile.changed: reads the file again unless the change is this editor's own save. */
  profileChanged(): Promise<void>;
}

interface Tracked {
  history: EditHistory;
  /** The file's revision the steps were made on. */
  revision: string;
}

function revisionConflict(error: {
  code?: number;
  message?: string;
  data?: unknown;
}): boolean {
  const data = error.data as { revision?: unknown } | undefined;
  return (
    error.code === -32009 ||
    (!!data && !Array.isArray(data) && typeof data.revision === "string") ||
    /revision conflict/i.test(String(error.message ?? ""))
  );
}

export function createEditorStore(api: PadmateAPI) {
  let sequence = 0;
  let metadataSequence = 0;
  let macroSequence = 0;
  let parentSequence = 0;
  let profileListSequence = 0;
  let revisionSequence = 0;
  let activation = 0;
  // The context activate was last asked for (JSON); the loaded one may lag.
  let requested = JSON.stringify(null);
  // The most recent puts whose answer never came (a timeout, a connection
  // that went away): the daemon may have written any of them, and the
  // revision held here is stale if it did. A file that holds one of the
  // texts is this editor's own write (adoptOwnWrite), not a change elsewhere.
  let unconfirmed: Array<{ text: string; sent: string }> = [];
  // An edit of profile id that was not saved when its connection went away,
  // read when the file had revision; saved again once the profile is back.
  // unconfirmed is what the editor had not heard back about by then.
  let carry: {
    id: string;
    draft: Profile;
    revision: string;
    unconfirmed: typeof unconfirmed;
  } | null = null;
  let pendingRevisionCheck = false;
  // The parent last read, which cannot be asked from inside shown() below.
  let heldParent: ParentProfile | null = null;
  // What a draft saves as (savedForm), to compare with the file. A key it
  // empties that the parent binds is saved as an empty binding (PRO-19).
  const form = (draft: Profile) =>
    stableStringify(savedForm(draft, heldParent));
  // form of the file as last read or saved, and its problems.
  let saved = "";
  let savedDiagnostics: ProfileDiagnostic[] = [];
  // An open merge group and the timer that ends it.
  let group: { key: string } | null = null;
  let groupTimer: ReturnType<typeof setTimeout> | undefined;
  // The running save loop; saves never overlap.
  let loop: Promise<void> | null = null;
  // A save failed or was refused: wait for an edit, an undo or a retry.
  let halted = false;
  const histories = new Map<string, Tracked>();
  const tracked = (id: string): Tracked => {
    let entry = histories.get(id);
    if (!entry) {
      entry = { history: new EditHistory(), revision: "" };
      histories.set(id, entry);
    }
    return entry;
  };
  // A key with only its label is not set yet, which the controller shows;
  // the daemon's "binding has no action" about it is expected here. So is the
  // one about a key the profile empties that its parent binds (PRO-19).
  const shown = (
    diagnostics: ProfileDiagnostic[] | undefined,
    draft: Profile | null,
  ) =>
    (diagnostics ?? []).filter(
      (diagnostic) =>
        !isLabelOnlyWarning(diagnostic, draft) &&
        !isClearedInherited(diagnostic, draft, heldParent),
    );
  // The function key the settings choose (setFunctionKey); the daemon ignores
  // a layer's bindings on it, so the usage does not count them.
  let functionKey: FunctionKey = defaultFunctionKey;
  const analyse = (draft: Profile) => ({
    usage: layerUsage(draft, functionKey),
    readOnly: (draft.defaultLayer && draft.defaultLayer !== "base"
      ? "defaultLayer"
      : "") as EditorState["readOnly"],
  });
  const store = createStore<EditorState>((set, get) => {
    const steps = (id: string) => {
      const history = histories.get(id)?.history;
      return { canUndo: !!history?.canUndo, canRedo: !!history?.canRedo };
    };
    const editable = () => {
      const state = get();
      return !!state.id && !!state.draft && !state.busy && !state.readOnly;
    };
    const canSave = () => {
      const state = get();
      return (
        !!state.id &&
        !!state.draft &&
        !state.busy &&
        !halted &&
        !group &&
        form(state.draft) !== saved
      );
    };
    /** Updates saving after a merge group or the save loop changed. */
    const syncSaving = () => {
      const saving = !!loop || !!group;
      if (get().saving !== saving) set({ saving });
    };
    /** Drops an open merge group without saving it. */
    const stopGroup = () => {
      clearTimeout(groupTimer);
      group = null;
      syncSaving();
    };
    /** Ends an open merge group and saves what it did. */
    const closeGroup = () => {
      if (!group) return;
      stopGroup();
      schedule();
    };
    async function metadata() {
      const state = get();
      if (!state.context) return;
      const token = sequence;
      const metadataToken = ++metadataSequence;
      // This read of the list is the newer one.
      profileListSequence++;
      const results = await Promise.allSettled([
        api.listProfiles(),
        api.getScriptDescription(state.id),
        api.getScriptRegistrations(state.context.device),
      ]);
      if (token !== sequence || metadataToken !== metadataSequence) return;
      const [profiles, description, registrations] = results;
      set({
        profiles: profiles.status === "fulfilled" ? profiles.value : [],
        description:
          description.status === "fulfilled" ? description.value : null,
        registrations:
          registrations.status === "fulfilled"
            ? registrations.value
                .filter(
                  (entry) =>
                    entry.profileId === state.id &&
                    entry.key === state.context?.device,
                )
                .flatMap((entry) => entry.script.metadata.registrations ?? [])
            : [],
        scriptError:
          description.status === "rejected"
            ? editorError(description.reason)
            : "",
      });
    }
    /**
     * Reads the list of profiles again, and nothing else: another profile
     * made, copied, imported, renamed or deleted changes the names the editor
     * checks a name against while this profile's own file stays as it is. A
     * list that cannot be read leaves the one held.
     */
    async function profileList() {
      const state = get();
      if (!state.context) return;
      const token = sequence;
      const listToken = ++profileListSequence;
      try {
        const profiles = await api.listProfiles();
        if (token === sequence && listToken === profileListSequence)
          set({ profiles });
      } catch {
        // The list held stays; the next change reads it again.
      }
    }
    async function macros() {
      const state = get();
      if (!state.context?.id) return;
      const token = sequence;
      const macroToken = ++macroSequence;
      try {
        const list = await api.listMacros();
        if (token === sequence && macroToken === macroSequence)
          set({ macros: list });
      } catch {
        // The drawer then lists no scripts; the scripts page shows why.
        if (token === sequence && macroToken === macroSequence)
          set({ macros: null });
      }
    }
    async function parentView() {
      const state = get();
      const id = state.draft?.extends;
      const token = sequence;
      const parentToken = ++parentSequence;
      if (!state.context || !id) {
        heldParent = null;
        if (state.parent) set({ parent: null });
        return;
      }
      try {
        const result = await api.resolveProfile(id);
        if (token !== sequence || parentToken !== parentSequence) return;
        const profile: Profile = withNewFunctionSet(JSON.parse(result.text));
        if (!profile.layers?.base)
          throw new Error("The parent does not contain a base layer");
        heldParent = { id, profile };
        // The warnings about keys the profile empties are not shown now.
        savedDiagnostics = shown(savedDiagnostics, get().draft);
        set({
          parent: heldParent,
          diagnostics: shown(get().diagnostics, get().draft),
        });
      } catch {
        // The profile's own problem says why its parent cannot be used.
        if (token === sequence && parentToken === parentSequence) {
          heldParent = null;
          set({ parent: null });
        }
      }
    }
    /**
     * Makes the file the draft. The undo history stays only when the
     * file is the one its steps were made on.
     */
    function fromDisk(id: string, result: ProfileDocument) {
      // A file of the old functionKeys is edited as the functionSet it stands
      // for (ADR-0075); form() is the same for both, so it is not unsaved.
      const draft: Profile = withNewFunctionSet(JSON.parse(result.text));
      if (!draft.layers?.base)
        throw new Error("The profile does not contain a base layer");
      stopGroup();
      halted = false;
      unconfirmed = [];
      saved = form(draft);
      savedDiagnostics = shown(result.diagnostics, draft);
      const entry = tracked(id);
      if (entry.revision !== result.revision) entry.history.clear();
      entry.revision = result.revision;
      set({
        draft,
        revision: result.revision,
        unsaved: false,
        error: "",
        saveProblem: "",
        saveError: "",
        applied: null,
        problem: result.problem ?? "",
        diagnostics: savedDiagnostics,
        loads: get().loads + 1,
        ...analyse(draft),
        ...steps(id),
      });
      void parentView();
    }
    async function load(context: EditorContext | null) {
      const token = ++sequence;
      stopGroup();
      halted = false;
      unconfirmed = [];
      saved = "";
      savedDiagnostics = [];
      heldParent = null;
      const empty = {
        draft: null,
        revision: "",
        unsaved: false,
        error: "",
        saveProblem: "" as const,
        saveError: "",
        reloaded: false,
        discarded: false,
        applied: null,
        problem: "",
        diagnostics: [],
        registrations: [],
        description: null,
        macros: null,
        parent: null,
        canUndo: false,
        canRedo: false,
        view: "normal" as const,
      };
      if (!context?.id) {
        set({
          ...empty,
          id: "",
          context,
          busy: false,
          usage: null,
          readOnly: "",
        });
        return;
      }
      set({ ...empty, id: context.id, context, busy: true });
      try {
        const result = await api.getProfile(context.id);
        if (token !== sequence) return;
        // The steps were made on the draft this write came from, so they
        // stay when the file is the editor's own write.
        if (
          carry?.id === context.id &&
          carry.unconfirmed.some((entry) => entry.text === result.text)
        )
          tracked(context.id).revision = result.revision;
        fromDisk(context.id, result);
        restore(context.id, result);
        void metadata();
        void macros();
      } catch (error) {
        if (token === sequence) set({ error: editorError(error) });
      } finally {
        if (token === sequence) {
          set({ busy: false });
          // A draft kept across a reconnect (restore) was shown while busy,
          // which held its save back.
          schedule();
          if (pendingRevisionCheck) void profileChanged();
        }
      }
    }
    /**
     * After profile id was read with revision: an edit of it that could
     * not be saved before its connection went away is shown and saved
     * again, unless the file changed since (the file wins, and reloaded
     * says so). A file that is the editor's own write, whose answer was
     * lost, is no change. An edit of another profile is gone, and discarded
     * says so.
     */
    function restore(id: string, file: ProfileDocument) {
      const kept = carry;
      carry = null;
      if (!kept) return;
      const own = kept.unconfirmed.some((entry) => entry.text === file.text);
      if (kept.id !== id) set({ discarded: true });
      else if (form(kept.draft) === saved) return;
      else if (kept.revision !== file.revision && !own) set({ reloaded: true });
      else {
        show(id, kept.draft);
        schedule();
      }
    }
    /**
     * The file holds the text of a put whose answer never came: it is this
     * editor's own write. Takes the file's revision and the saved state of
     * that text, so the draft, its undo history and what it has beyond the
     * text stay and are saved on top. False when the file is something else.
     */
    function adoptOwnWrite(id: string, file: ProfileDocument): boolean {
      const own = unconfirmed.find((entry) => entry.text === file.text);
      if (!own) return false;
      unconfirmed = [];
      saved = own.sent;
      savedDiagnostics = shown(file.diagnostics, JSON.parse(file.text));
      tracked(id).revision = file.revision;
      const unsaved = form(get().draft as Profile) !== saved;
      set({
        revision: file.revision,
        // Whether the daemon applied it is not known.
        applied: null,
        problem: file.problem ?? "",
        diagnostics: savedDiagnostics,
        unsaved,
        ...(unsaved ? {} : { saveProblem: "", saveError: "" }),
      });
      return true;
    }
    /**
     * After a revision conflict, with one read of the file: the editor's
     * own write (adoptOwnWrite) lets the save go on, which is the result.
     * Anything else wins, and the notice says so.
     */
    async function afterConflict(id: string): Promise<boolean> {
      const token = sequence;
      try {
        const result = await api.getProfile(id);
        if (token !== sequence) return false;
        if (adoptOwnWrite(id, result)) return true;
        fromDisk(id, result);
        set({ reloaded: true });
        void metadata();
      } catch (error) {
        if (token !== sequence) return false;
        halted = true;
        set({ saveProblem: "failed", saveError: editorError(error) });
      }
      return false;
    }
    /** Saves the draft until it matches the file; one save at a time. */
    async function run() {
      while (canSave()) {
        const state = get();
        const id = state.id;
        const draft = savedForm(state.draft as Profile, heldParent);
        const sent = stableStringify(draft);
        const text = `${JSON.stringify(draft, null, 2)}\n`;
        const token = sequence;
        // An edit made while the daemon looked at this draft is checked
        // next, whatever it said about this one.
        const outdated = () => {
          const latest = get().draft;
          return !!latest && form(latest) !== sent;
        };
        let putting = false;
        try {
          const validation = await api.validateProfile(text);
          if (token !== sequence) return;
          if (!validation.valid) {
            if (outdated()) continue;
            halted = true;
            set({
              saveProblem: "invalid",
              saveError: "",
              diagnostics: shown(validation.diagnostics, draft),
            });
            return;
          }
          putting = true;
          const result = await api.putProfile(id, text, state.revision);
          if (token !== sequence) return;
          unconfirmed = [];
          saved = sent;
          savedDiagnostics = shown(validation.diagnostics, draft);
          tracked(id).revision = result.revision;
          const latest = get().draft;
          set({
            revision: result.revision,
            applied: result.applied,
            problem: result.problem ?? "",
            diagnostics: savedDiagnostics,
            saveProblem: "",
            saveError: "",
            unsaved: !!latest && form(latest) !== saved,
          });
          void metadata();
        } catch (error) {
          if (token !== sequence) return;
          const e = error as {
            code?: number;
            message?: string;
            data?:
              | { revision?: string; diagnostics?: ProfileDiagnostic[] }
              | ProfileDiagnostic[];
          };
          if (revisionConflict(e)) {
            if (await afterConflict(id)) continue;
            return;
          }
          // No code and no data: the answer did not come, and the daemon
          // may have written this draft all the same. A retry of the same
          // text does not push an older one out.
          const lost = putting && e.code === undefined && !e.data;
          if (lost)
            unconfirmed = [
              { text, sent },
              ...unconfirmed.filter((entry) => entry.text !== text),
            ].slice(0, 4);
          if (outdated()) {
            // A draft that went back to what the file was has nothing to
            // save, so no put would find the lost write: look at the file
            // when the loop ends, as for a profile.changed that is due.
            if (lost && !canSave()) pendingRevisionCheck = true;
            continue;
          }
          halted = true;
          const diagnostics = Array.isArray(e.data)
            ? e.data
            : e.data?.diagnostics;
          if (diagnostics?.length)
            set({
              saveProblem: "invalid",
              saveError: "",
              diagnostics: shown(diagnostics, draft),
            });
          else set({ saveProblem: "failed", saveError: editorError(error) });
          return;
        }
      }
    }
    function schedule(): Promise<void> {
      if (!loop) {
        if (!canSave()) return Promise.resolve();
        loop = run().finally(() => {
          loop = null;
          syncSaving();
          if (pendingRevisionCheck) void profileChanged();
        });
        syncSaving();
      }
      return loop;
    }
    /** Ends a merge group and waits for every save; true when all are on disk. */
    async function settle(): Promise<boolean> {
      closeGroup();
      while (loop || canSave()) await schedule();
      return !get().unsaved;
    }
    async function profileChanged() {
      const state = get();
      if (state.busy || loop) {
        pendingRevisionCheck = true;
        return;
      }
      pendingRevisionCheck = false;
      if (!state.context || !state.draft) return;
      // A changed macros folder (profile.changed {kind:"macros"}) keeps the
      // revision; the list is read either way. So is the parent, whose file
      // may be what changed (PRO-19).
      void macros();
      void parentView();
      void profileList();
      const token = sequence;
      const revisionToken = ++revisionSequence;
      const known = get().revision;
      try {
        const result = await api.getProfile(state.id);
        if (
          token !== sequence ||
          revisionToken !== revisionSequence ||
          result.revision === get().revision
        )
          return;
        // A save started meanwhile; check again once it is done.
        if (loop) {
          pendingRevisionCheck = true;
          return;
        }
        // A save finished meanwhile: what was read is the file from before
        // it, which would bring an undone edit back. Read it again.
        if (get().revision !== known) {
          void profileChanged();
          return;
        }
        // A put whose answer was lost wrote this file: no change elsewhere.
        // A failed save is moot then, and the draft's newer edits are saved.
        if (adoptOwnWrite(state.id, result)) {
          halted = false;
          schedule();
          return;
        }
        const dropped = get().unsaved;
        fromDisk(state.id, result);
        if (dropped) set({ reloaded: true });
        void metadata();
      } catch (error) {
        if (token === sequence && revisionToken === revisionSequence)
          set({ error: editorError(error) });
      }
    }
    /**
     * Shows draft. Back to what the file has, a failed or refused save is
     * moot: its problem goes, and the file's own diagnostics come back.
     */
    function show(id: string, draft: Profile) {
      halted = false;
      const unsaved = form(draft) !== saved;
      set({
        draft,
        usage: layerUsage(draft, functionKey),
        unsaved,
        reloaded: false,
        discarded: false,
        ...(unsaved
          ? {}
          : { saveProblem: "", saveError: "", diagnostics: savedDiagnostics }),
        ...steps(id),
      });
    }
    /**
     * Shows a draft from the history and saves it. The profile's name is
     * not part of the history (EditOptions.history): it stays as it is.
     */
    function apply(id: string, draft: Profile, current: Profile) {
      show(id, { ...draft, name: current.name });
      schedule();
    }
    return {
      id: "",
      context: null,
      draft: null,
      revision: "",
      unsaved: false,
      saving: false,
      busy: false,
      error: "",
      saveProblem: "",
      saveError: "",
      reloaded: false,
      discarded: false,
      applied: null,
      problem: "",
      diagnostics: [],
      canUndo: false,
      canRedo: false,
      loads: 0,
      profiles: [],
      description: null,
      registrations: [],
      scriptError: "",
      macros: null,
      parent: null,
      view: "normal",
      setView: (view) => set({ view }),
      functionKey: defaultFunctionKey,
      setFunctionKey: (key) => {
        if (key === functionKey) return;
        functionKey = key;
        const draft = get().draft;
        set({
          functionKey: key,
          ...(draft ? { usage: layerUsage(draft, key) } : {}),
        });
      },
      usage: null,
      readOnly: "",
      globalTiming: {},
      setGlobalTiming: (globalTiming) => set({ globalTiming }),
      async activate(context) {
        // Compared with the last request: the loaded context lags behind
        // while the open profile's saves finish.
        const key = JSON.stringify(context);
        if (key === requested) return;
        requested = key;
        const ticket = ++activation;
        await settle();
        if (ticket !== activation) return;
        const state = get();
        // Back to the open context before another one was loaded.
        if (JSON.stringify(state.context) === key) return;
        const current = state.context;
        // Another pad on the same profile and connection: the draft stays.
        if (
          context &&
          current &&
          state.draft &&
          !state.busy &&
          context.id === current.id &&
          context.connection === current.connection
        ) {
          set({ context });
          void metadata();
          return;
        }
        if (state.draft && state.unsaved)
          carry = {
            id: state.id,
            draft: state.draft,
            revision: state.revision,
            unconfirmed,
          };
        await load(context);
      },
      edit(next, options = {}) {
        if (!editable()) return;
        const state = get();
        const current = state.draft as Profile;
        const key = options.merge;
        if (!key || group?.key !== key) {
          closeGroup();
          if (stableStringify(next) === stableStringify(current)) return;
          // A draft the daemon refused is not a step of its own: the edit
          // that fixes it joins the step that made it, and undo goes back
          // to the last draft that could be saved.
          const history = tracked(state.id).history;
          if (options.history === false) {
            // Not a step (a rename): undo and redo keep it.
          } else if (state.saveProblem === "invalid") history.dropRedo();
          else history.record(current);
          if (key) group = { key };
        }
        show(state.id, next);
        if (group) {
          clearTimeout(groupTimer);
          if (!options.hold) groupTimer = setTimeout(closeGroup, MERGE_IDLE_MS);
          syncSaving();
        } else schedule();
      },
      undo() {
        if (!editable()) return false;
        const state = get();
        // An open group is the last step: undoing it needs no save of it.
        stopGroup();
        const current = state.draft as Profile;
        const previous = tracked(state.id).history.undo(current);
        if (!previous) {
          schedule();
          return false;
        }
        apply(state.id, previous, current);
        return true;
      },
      redo() {
        if (!editable()) return false;
        const state = get();
        closeGroup();
        const current = state.draft as Profile;
        const next = tracked(state.id).history.redo(current);
        if (!next) return false;
        apply(state.id, next, current);
        return true;
      },
      retry() {
        halted = false;
        set({ saveProblem: "", saveError: "" });
        schedule();
      },
      dismissReloaded: () => set({ reloaded: false }),
      dismissDiscarded: () => set({ discarded: false }),
      async flush(id) {
        if (id !== undefined && id !== get().id) return true;
        if (get().saveProblem === "failed") halted = false;
        return settle();
      },
      async reload(id) {
        const state = get();
        if (id !== undefined && id !== state.id) return;
        if (!state.context) return;
        if (!state.draft) {
          await load(state.context);
          return;
        }
        stopGroup();
        while (loop) await loop;
        const token = sequence;
        try {
          const result = await api.getProfile(state.id);
          if (token !== sequence) return;
          fromDisk(state.id, result);
          set({ reloaded: false });
          void metadata();
          void macros();
        } catch (error) {
          if (token === sequence) set({ error: editorError(error) });
        }
      },
      clearHistory(id) {
        const target = id ?? get().id;
        histories.get(target)?.history.clear();
        if (target === get().id) set(steps(target));
      },
      refreshMetadata: metadata,
      refreshMacros: macros,
      refreshParent: parentView,
      profileChanged,
    };
  });
  return store;
}
export type EditorStore = ReturnType<typeof createEditorStore>;
