import { afterEach, describe, expect, it, vi } from "vitest";
import type {
  PadmateAPI,
  ProfileDocument,
  ProfileSaveResult,
  ProfileValidation,
} from "../shared/api";
import type { Profile } from "../shared/generated/schema";
import { setBinding, swapView } from "./editor-model";
import {
  createEditorStore,
  type EditorStore,
  MERGE_IDLE_MS,
} from "./editor-store";

const text = JSON.stringify({
  schemaVersion: 0,
  name: "p",
  layers: { base: {} },
});
const context = { id: "paint", device: "d", connection: "c" };

/** A daemon whose put checks the revision like profiles.put. */
function setup() {
  const disk = new Map<string, { text: string; revision: number }>();
  const read = (id: string) => disk.get(id) ?? { text, revision: 1 };
  const api = {
    getProfile: vi.fn(async (id: string): Promise<ProfileDocument> => {
      const file = read(id);
      return {
        id,
        text: file.text,
        revision: `r${file.revision}`,
        diagnostics: [],
      };
    }),
    listProfiles: vi.fn(async () => []),
    getScriptDescription: vi.fn(async () => ({
      metadata: { runtime: 0, actions: [] },
    })),
    getScriptRegistrations: vi.fn(async () => []),
    listMacros: vi.fn(async () => ({ macros: [], missing: [] })),
    validateProfile: vi.fn(
      async (_text: string): Promise<ProfileValidation> => ({
        valid: true,
        diagnostics: [],
      }),
    ),
    putProfile: vi.fn(
      async (
        id: string,
        next: string,
        revision: string,
      ): Promise<ProfileSaveResult> => {
        const file = read(id);
        if (revision !== `r${file.revision}`)
          throw {
            message: "revision conflict",
            code: -32009,
            data: { revision: `r${file.revision}` },
          };
        disk.set(id, { text: next, revision: file.revision + 1 });
        return { revision: `r${file.revision + 1}`, applied: true };
      },
    ),
  };
  /** Another program writes the file. */
  const writeElsewhere = (id: string, profile: Profile) => {
    const file = read(id);
    disk.set(id, {
      text: JSON.stringify(profile),
      revision: file.revision + 1,
    });
  };
  const onDisk = (id = "paint"): Profile => JSON.parse(read(id).text);
  return {
    api,
    store: createEditorStore(api as unknown as PadmateAPI),
    writeElsewhere,
    onDisk,
  };
}
function draft(store: EditorStore) {
  const value = store.getState().draft;
  if (!value) throw new Error("Expected loaded draft");
  return value;
}
const named = (store: EditorStore, name: string): Profile => ({
  ...draft(store),
  name,
});
/**
 * An edit that undo takes back (the name is not part of the history): the
 * south key's name. mark reads it, or the profile's name before any.
 */
const marked = (store: EditorStore, value: string): Profile => ({
  ...draft(store),
  controlNames: { south: value },
});
const mark = (profile: Profile | null | undefined) =>
  profile?.controlNames?.south ?? profile?.name;
/** Lets the save loop's promises run. */
const settled = () => new Promise((resolve) => setTimeout(resolve, 0));

afterEach(() => {
  vi.useRealTimers();
});

describe("saving", () => {
  it("saves every edit at once with the revision it was read with", async () => {
    const { api, store, onDisk } = setup();
    await store.getState().activate(context);
    store.getState().edit(named(store, "changed"));
    expect(store.getState().unsaved).toBe(true);
    expect(await store.getState().flush()).toBe(true);
    expect(api.putProfile).toHaveBeenCalledOnce();
    expect(api.putProfile.mock.calls[0]).toEqual([
      "paint",
      expect.stringContaining('"changed"'),
      "r1",
    ]);
    expect(onDisk().name).toBe("changed");
    expect(store.getState()).toMatchObject({
      unsaved: false,
      revision: "r2",
      applied: true,
      saveProblem: "",
    });
  });

  it("saves edits made during a save afterwards, one save at a time", async () => {
    const { api, store, onDisk } = setup();
    await store.getState().activate(context);
    let running = 0;
    let most = 0;
    const put = api.putProfile.getMockImplementation();
    api.putProfile.mockImplementation(async (...args) => {
      running++;
      most = Math.max(most, running);
      await settled();
      running--;
      return put?.(...args) as Promise<ProfileSaveResult>;
    });
    store.getState().edit(named(store, "a"));
    store.getState().edit(named(store, "b"));
    store.getState().edit(named(store, "c"));
    await store.getState().flush();
    expect(most).toBe(1);
    expect(onDisk().name).toBe("c");
    // The later saves carry the revision the earlier ones returned.
    expect(api.putProfile.mock.calls.map((call) => call[2])).toEqual(
      api.putProfile.mock.calls.map((_, i) => `r${i + 1}`),
    );
    expect(store.getState().unsaved).toBe(false);
  });

  it("does not save an edit that changes nothing", async () => {
    const { api, store } = setup();
    await store.getState().activate(context);
    store.getState().edit(structuredClone(draft(store)));
    await store.getState().flush();
    expect(api.putProfile).not.toHaveBeenCalled();
    expect(store.getState().canUndo).toBe(false);
  });

  it("does not save a draft the daemon refuses and saves the fix", async () => {
    const { api, store, onDisk } = setup();
    await store.getState().activate(context);
    const diagnostic = {
      severity: "error" as const,
      pointer: "/layers/base/south/press/keys",
      offset: 0,
      line: 1,
      col: 1,
      message: "unknown key",
    };
    api.validateProfile.mockResolvedValueOnce({
      valid: false,
      diagnostics: [diagnostic],
    });
    store.getState().edit(named(store, "bad"));
    expect(await store.getState().flush()).toBe(false);
    expect(api.putProfile).not.toHaveBeenCalled();
    expect(store.getState()).toMatchObject({
      saveProblem: "invalid",
      unsaved: true,
      diagnostics: [diagnostic],
    });
    expect(onDisk().name).toBe("p");
    // Flushing again does not send the same draft again.
    await store.getState().flush();
    expect(api.validateProfile).toHaveBeenCalledOnce();
    store.getState().edit(named(store, "good"));
    expect(await store.getState().flush()).toBe(true);
    expect(onDisk().name).toBe("good");
    expect(store.getState()).toMatchObject({
      saveProblem: "",
      diagnostics: [],
    });
  });

  it("makes a refused draft and the edit that fixes it one step", async () => {
    const { api, store } = setup();
    await store.getState().activate(context);
    api.validateProfile.mockResolvedValueOnce({
      valid: false,
      diagnostics: [],
    });
    // A step added empty, then filled in.
    store.getState().edit(marked(store, ""));
    await store.getState().flush();
    expect(store.getState().saveProblem).toBe("invalid");
    store.getState().edit(marked(store, "filled"));
    expect(await store.getState().flush()).toBe(true);
    store.getState().undo();
    expect(mark(store.getState().draft)).toBe("p");
    expect(store.getState().canUndo).toBe(false);
    store.getState().redo();
    expect(mark(store.getState().draft)).toBe("filled");
  });

  it("drops a refused save's problem when an undo goes back to the file", async () => {
    const { api, store } = setup();
    const warning = {
      severity: "warning" as const,
      pointer: "/layers/base/east",
      offset: 0,
      line: 1,
      col: 1,
      message: "binding has no action",
    };
    api.getProfile.mockResolvedValueOnce({
      id: "paint",
      text,
      revision: "r1",
      diagnostics: [warning],
    });
    await store.getState().activate(context);
    api.validateProfile.mockResolvedValueOnce({
      valid: false,
      diagnostics: [{ ...warning, severity: "error", message: "empty key" }],
    });
    store.getState().edit(marked(store, "step"));
    await store.getState().flush();
    expect(store.getState().saveProblem).toBe("invalid");
    store.getState().undo();
    expect(store.getState()).toMatchObject({
      saveProblem: "",
      unsaved: false,
      diagnostics: [warning],
    });
    await store.getState().flush();
    expect(api.putProfile).not.toHaveBeenCalled();
  });

  it("keeps the daemon's diagnostics when put refuses the draft", async () => {
    const { api, store } = setup();
    await store.getState().activate(context);
    const diagnostic = {
      severity: "error" as const,
      pointer: "/name",
      offset: 0,
      line: 1,
      col: 1,
      message: "bad",
    };
    api.putProfile.mockRejectedValueOnce({
      message: "invalid library document",
      code: -32602,
      data: [diagnostic],
    });
    store.getState().edit(named(store, "bad"));
    await store.getState().flush();
    expect(store.getState().saveProblem).toBe("invalid");
    expect(store.getState().diagnostics).toEqual([diagnostic]);
  });

  it("reports a failed save and saves on retry", async () => {
    const { api, store, onDisk } = setup();
    await store.getState().activate(context);
    api.putProfile.mockRejectedValueOnce({ message: "bridge closed" });
    store.getState().edit(named(store, "next"));
    await settled();
    expect(store.getState()).toMatchObject({
      saveProblem: "failed",
      saveError: "bridge closed",
      unsaved: true,
    });
    store.getState().retry();
    await store.getState().flush();
    expect(onDisk().name).toBe("next");
    expect(store.getState().saveProblem).toBe("");
  });

  it("retries a failed save once when flushed", async () => {
    const { api, store, onDisk } = setup();
    await store.getState().activate(context);
    api.putProfile.mockRejectedValueOnce({ message: "busy" });
    store.getState().edit(named(store, "next"));
    await settled();
    expect(store.getState().saveProblem).toBe("failed");
    expect(await store.getState().flush()).toBe(true);
    expect(onDisk().name).toBe("next");
  });

  it("distinguishes saved-on-disk from applied", async () => {
    const { api, store } = setup();
    await store.getState().activate(context);
    api.putProfile.mockResolvedValueOnce({
      revision: "r2",
      applied: false,
      problem: "script failed",
    });
    store.getState().edit(named(store, "next"));
    await store.getState().flush();
    expect(store.getState()).toMatchObject({
      unsaved: false,
      applied: false,
      problem: "script failed",
    });
  });

  it("refuses edits to a profile the GUI cannot edit", async () => {
    const { api, store } = setup();
    api.getProfile.mockResolvedValueOnce({
      id: "paint",
      text: JSON.stringify({
        schemaVersion: 0,
        name: "p",
        defaultLayer: "alt",
        layers: { base: {}, alt: {} },
      }),
      revision: "r1",
      diagnostics: [],
    });
    await store.getState().activate(context);
    expect(store.getState().readOnly).toBe("defaultLayer");
    store.getState().edit(named(store, "changed"));
    expect(store.getState().draft?.name).toBe("p");
    expect(store.getState().undo()).toBe(false);
    await store.getState().flush();
    expect(api.putProfile).not.toHaveBeenCalled();
  });
});

describe("merged edits", () => {
  it("makes a slider drag one undo step and one save", async () => {
    vi.useFakeTimers();
    const { api, store, onDisk } = setup();
    await store.getState().activate(context);
    // Pointer down: the drag goes on however long it pauses.
    store.getState().edit(marked(store, "1"), { merge: "dz", hold: true });
    store.getState().edit(marked(store, "2"), { merge: "dz", hold: true });
    await vi.advanceTimersByTimeAsync(MERGE_IDLE_MS * 3);
    expect(api.putProfile).not.toHaveBeenCalled();
    // Pointer up.
    store.getState().edit(marked(store, "3"), { merge: "dz" });
    await vi.advanceTimersByTimeAsync(MERGE_IDLE_MS - 1);
    expect(api.putProfile).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    await store.getState().flush();
    expect(api.putProfile).toHaveBeenCalledOnce();
    expect(mark(onDisk())).toBe("3");
    expect(store.getState().undo()).toBe(true);
    expect(mark(store.getState().draft)).toBe("p");
    expect(store.getState().canUndo).toBe(false);
  });

  it("ends a merge group with a pause or another edit", async () => {
    vi.useFakeTimers();
    const { api, store } = setup();
    await store.getState().activate(context);
    store.getState().edit(marked(store, "a1"), { merge: "a" });
    store.getState().edit(marked(store, "a2"), { merge: "a" });
    await vi.advanceTimersByTimeAsync(MERGE_IDLE_MS);
    // A pause ends the group: the next arrow key is a new step.
    store.getState().edit(marked(store, "a3"), { merge: "a" });
    // Another kind of edit ends that group at once.
    store.getState().edit(marked(store, "label"));
    await store.getState().flush();
    expect(
      api.putProfile.mock.calls.map((call) => mark(JSON.parse(call[1]))),
    ).toEqual(["a2", "a3", "label"]);
    store.getState().undo();
    expect(mark(store.getState().draft)).toBe("a3");
    store.getState().undo();
    expect(mark(store.getState().draft)).toBe("a2");
    store.getState().undo();
    expect(mark(store.getState().draft)).toBe("p");
  });

  it("undoes an open group without saving it", async () => {
    vi.useFakeTimers();
    const { api, store } = setup();
    await store.getState().activate(context);
    store.getState().edit(marked(store, "x"), { merge: "dz", hold: true });
    expect(store.getState().undo()).toBe(true);
    await vi.advanceTimersByTimeAsync(MERGE_IDLE_MS * 2);
    await store.getState().flush();
    expect(api.putProfile).not.toHaveBeenCalled();
    expect(mark(store.getState().draft)).toBe("p");
    expect(store.getState().redo()).toBe(true);
    await store.getState().flush();
    expect(api.putProfile).toHaveBeenCalledOnce();
  });

  it("says while an edit waits for its save or a save runs (SCR-50)", async () => {
    vi.useFakeTimers();
    const { api, store } = setup();
    await store.getState().activate(context);
    expect(store.getState().saving).toBe(false);
    // A merge group waits for its pause.
    store.getState().edit(marked(store, "1"), { merge: "dz" });
    expect(store.getState().saving).toBe(true);
    await vi.advanceTimersByTimeAsync(MERGE_IDLE_MS);
    expect(await store.getState().flush()).toBe(true);
    expect(store.getState().saving).toBe(false);
    // A save that runs.
    let release = () => {};
    api.validateProfile.mockImplementationOnce(
      () =>
        new Promise<ProfileValidation>((resolve) => {
          release = () => resolve({ valid: true, diagnostics: [] });
        }),
    );
    store.getState().edit(marked(store, "2"));
    expect(store.getState().saving).toBe(true);
    release();
    expect(await store.getState().flush()).toBe(true);
    expect(store.getState().saving).toBe(false);
    // A refused draft is not saving any more, but it is not on disk.
    api.validateProfile.mockResolvedValueOnce({
      valid: false,
      diagnostics: [],
    });
    store.getState().edit(marked(store, "3"));
    expect(await store.getState().flush()).toBe(false);
    expect(store.getState()).toMatchObject({ saving: false, unsaved: true });
  });

  it("saves an open group when flushed", async () => {
    vi.useFakeTimers();
    const { store, onDisk } = setup();
    await store.getState().activate(context);
    store.getState().edit(marked(store, "x"), { merge: "dz", hold: true });
    expect(await store.getState().flush()).toBe(true);
    expect(mark(onDisk())).toBe("x");
  });
});

describe("undo and redo", () => {
  it("applies earlier drafts and saves each of them", async () => {
    const { store, onDisk } = setup();
    await store.getState().activate(context);
    expect(store.getState()).toMatchObject({ canUndo: false, canRedo: false });
    store.getState().edit(marked(store, "a"));
    store.getState().edit(marked(store, "b"));
    expect(store.getState().canUndo).toBe(true);
    expect(store.getState().undo()).toBe(true);
    expect(mark(store.getState().draft)).toBe("a");
    expect(store.getState().canRedo).toBe(true);
    await store.getState().flush();
    expect(mark(onDisk())).toBe("a");
    expect(store.getState().redo()).toBe(true);
    await store.getState().flush();
    expect(mark(onDisk())).toBe("b");
    expect(store.getState().redo()).toBe(false);
    store.getState().undo();
    store.getState().edit(marked(store, "c"));
    expect(store.getState().canRedo).toBe(false);
  });

  it("keeps the history of each profile while the file stays the same", async () => {
    const { store, writeElsewhere } = setup();
    await store.getState().activate(context);
    store.getState().edit(marked(store, "paint edit"));
    await store
      .getState()
      .activate({ id: "sketch", device: "d", connection: "c" });
    expect(store.getState().canUndo).toBe(false);
    await store.getState().activate(context);
    expect(mark(store.getState().draft)).toBe("paint edit");
    expect(store.getState().undo()).toBe(true);
    expect(mark(store.getState().draft)).toBe("p");
    await store.getState().flush();
    // Changed elsewhere while another profile was open: the steps go.
    await store
      .getState()
      .activate({ id: "sketch", device: "d", connection: "c" });
    writeElsewhere("paint", {
      schemaVersion: 1,
      name: "elsewhere",
      layers: { base: {} },
    });
    await store.getState().activate(context);
    expect(store.getState().draft?.name).toBe("elsewhere");
    expect(store.getState()).toMatchObject({ canUndo: false, canRedo: false });
  });

  it("can be cleared for a profile", async () => {
    const { store } = setup();
    await store.getState().activate(context);
    store.getState().edit(named(store, "a"));
    store.getState().clearHistory("paint");
    expect(store.getState().canUndo).toBe(false);
    expect(store.getState().undo()).toBe(false);
  });
});

describe("changes elsewhere", () => {
  it("reloads after a revision conflict and says so once", async () => {
    const { api, store, writeElsewhere, onDisk } = setup();
    await store.getState().activate(context);
    store.getState().edit(named(store, "mine"));
    await store.getState().flush();
    writeElsewhere("paint", {
      schemaVersion: 1,
      name: "theirs",
      layers: { base: {} },
    });
    store.getState().edit(named(store, "mine again"));
    await store.getState().flush();
    expect(onDisk().name).toBe("theirs");
    expect(store.getState()).toMatchObject({
      reloaded: true,
      unsaved: false,
      revision: "r3",
      canUndo: false,
      canRedo: false,
    });
    expect(store.getState().draft?.name).toBe("theirs");
    expect(api.putProfile).toHaveBeenCalledTimes(2);
    store.getState().edit(named(store, "after"));
    expect(store.getState().reloaded).toBe(false);
    await store.getState().flush();
    expect(onDisk().name).toBe("after");
  });

  it("reloads a change made elsewhere and clears the history", async () => {
    const { store, writeElsewhere } = setup();
    await store.getState().activate(context);
    store.getState().edit(named(store, "mine"));
    await store.getState().flush();
    const loads = store.getState().loads;
    writeElsewhere("paint", {
      schemaVersion: 1,
      name: "external",
      layers: { base: {} },
    });
    await store.getState().profileChanged();
    expect(store.getState().draft?.name).toBe("external");
    expect(store.getState()).toMatchObject({
      reloaded: false,
      canUndo: false,
      loads: loads + 1,
    });
  });

  it("does not take its own save for a change elsewhere", async () => {
    const { api, store } = setup();
    await store.getState().activate(context);
    const put = api.putProfile.getMockImplementation();
    api.putProfile.mockImplementationOnce(async (...args) => {
      const result = await (put?.(...args) as Promise<ProfileSaveResult>);
      // profile.changed for this save arrives before put returns.
      await store.getState().profileChanged();
      return result;
    });
    store.getState().edit(named(store, "mine"));
    await store.getState().flush();
    await store.getState().profileChanged();
    await settled();
    expect(store.getState()).toMatchObject({
      reloaded: false,
      canUndo: true,
      unsaved: false,
      applied: true,
    });
    expect(store.getState().draft?.name).toBe("mine");
  });

  it("ignores a read of the file that a save of its own has outdated", async () => {
    const { api, store } = setup();
    await store.getState().activate(context);
    store.getState().edit({
      ...draft(store),
      controlNames: { south: "first" },
    });
    await store.getState().flush();
    const read = api.getProfile.getMockImplementation();
    let release = () => {};
    // The daemon answers a profile.changed read slowly, with the file as it was.
    api.getProfile.mockImplementationOnce(async (id) => {
      const document = await (read?.(id) as Promise<ProfileDocument>);
      await new Promise<void>((resolve) => {
        release = resolve;
      });
      return document;
    });
    const changed = store.getState().profileChanged();
    // An undo saves meanwhile.
    expect(store.getState().undo()).toBe(true);
    await store.getState().flush();
    expect(draft(store).controlNames).toBeUndefined();
    release();
    await changed;
    await settled();
    // The old file did not bring the undone edit back or clear the history.
    expect(draft(store).controlNames).toBeUndefined();
    expect(store.getState()).toMatchObject({
      reloaded: false,
      unsaved: false,
      canRedo: true,
    });
    expect(store.getState().redo()).toBe(true);
    expect(draft(store).controlNames).toEqual({ south: "first" });
  });

  it("reads the file again on request", async () => {
    const { api, store, writeElsewhere } = setup();
    await store.getState().activate(context);
    store.getState().edit(named(store, "mine"));
    await store.getState().flush();
    writeElsewhere("paint", {
      schemaVersion: 1,
      name: "macro renamed",
      layers: { base: {} },
    });
    api.listMacros.mockClear();
    await store.getState().reload("paint");
    expect(store.getState().draft?.name).toBe("macro renamed");
    expect(store.getState().canUndo).toBe(false);
    // The drawer's scripts are read again with it (a renamed macro).
    expect(api.listMacros).toHaveBeenCalledWith();
    // Another profile is not open: nothing to read or wait for.
    await store.getState().reload("sketch");
    expect(await store.getState().flush("sketch")).toBe(true);
  });
});

describe("switching", () => {
  it("saves the open profile before showing another one", async () => {
    vi.useFakeTimers();
    const { api, store, onDisk } = setup();
    await store.getState().activate(context);
    store.getState().edit(named(store, "dragged"), { merge: "dz" });
    await store
      .getState()
      .activate({ id: "sketch", device: "d", connection: "c" });
    expect(onDisk("paint").name).toBe("dragged");
    expect(api.putProfile.mock.calls[0]?.[0]).toBe("paint");
    expect(store.getState().id).toBe("sketch");
    expect(store.getState().draft?.name).toBe("p");
  });

  it("ignores a late load from the previous connection", async () => {
    const { api, store } = setup();
    let resolve = (_result: ProfileDocument) => {};
    api.getProfile.mockReturnValueOnce(
      new Promise((r) => {
        resolve = r;
      }),
    );
    const first = store
      .getState()
      .activate({ id: "old", device: "d", connection: "c1" });
    await vi.waitFor(() => expect(api.getProfile).toHaveBeenCalledOnce());
    await store
      .getState()
      .activate({ id: "new", device: "d", connection: "c2" });
    resolve({
      id: "old",
      text: text.replace('"p"', '"obsolete"'),
      revision: "old",
      diagnostics: [],
    });
    await first;
    expect(store.getState().id).toBe("new");
    expect(store.getState().draft?.name).toBe("p");
  });

  it("reports a profile it cannot read", async () => {
    const { api, store } = setup();
    api.getProfile.mockRejectedValueOnce({ message: "gone" });
    await store.getState().activate(context);
    expect(store.getState()).toMatchObject({ draft: null, error: "gone" });
    await store.getState().reload();
    expect(store.getState().draft?.name).toBe("p");
    expect(store.getState().error).toBe("");
  });
});

describe("review fixes (UI-43)", () => {
  const withNorth = (store: EditorStore, press: unknown): Profile => {
    const next = structuredClone(draft(store));
    next.layers.base = {
      ...next.layers.base,
      north: { press } as NonNullable<Profile["layers"]["base"]["north"]>,
    };
    return next;
  };

  it("saves what is filled in and leaves out parts still empty", async () => {
    const { api, store, onDisk } = setup();
    await store.getState().activate(context);
    store
      .getState()
      .edit(withNorth(store, { type: "keys", keys: "F13", mode: "hold" }));
    await store.getState().flush();
    expect(onDisk().layers.base.north?.press).toMatchObject({ keys: "F13" });
    // 添加下一步: the new step is empty, and the sequence taps.
    store
      .getState()
      .edit(withNorth(store, { type: "keys", keys: ["F13", ""], mode: "tap" }));
    expect(await store.getState().flush()).toBe(true);
    expect(store.getState()).toMatchObject({ unsaved: false, saveProblem: "" });
    expect(onDisk().layers.base.north?.press).toEqual({
      type: "keys",
      keys: ["F13"],
      mode: "tap",
    });
    // Another edit is saved while the step is still empty.
    store.getState().edit(named(store, "other edit"));
    await store.getState().flush();
    expect(onDisk().name).toBe("other edit");
    // The draft keeps the empty step; filling it in saves it.
    expect(draft(store).layers.base.north?.press).toMatchObject({
      keys: ["F13", ""],
    });
    store.getState().edit(
      withNorth(store, {
        type: "keys",
        keys: ["F13", "Ctrl+V"],
        mode: "tap",
      }),
    );
    await store.getState().flush();
    expect(onDisk().layers.base.north?.press).toMatchObject({
      keys: ["F13", "Ctrl+V"],
    });
    // An empty shortcut or script name leaves out the gesture, and a
    // binding with nothing else goes with it.
    store.getState().edit(withNorth(store, { type: "script", name: "" }));
    await store.getState().flush();
    expect(onDisk().layers.base.north).toBeUndefined();
    for (const [text] of api.validateProfile.mock.calls)
      expect(text).not.toContain('"name": ""');
  });

  it("checks a newer draft that came while a refused one was checked", async () => {
    const { api, store, onDisk } = setup();
    await store.getState().activate(context);
    let refuse = (_value: ProfileValidation) => {};
    api.validateProfile.mockReturnValueOnce(
      new Promise((resolve) => {
        refuse = resolve;
      }),
    );
    store.getState().edit(named(store, "refused"));
    await vi.waitFor(() => expect(api.validateProfile).toHaveBeenCalledOnce());
    store.getState().edit(named(store, "fixed"));
    refuse({
      valid: false,
      diagnostics: [
        {
          severity: "error",
          pointer: "/name",
          offset: 0,
          line: 1,
          col: 1,
          message: "bad",
        },
      ],
    });
    expect(await store.getState().flush()).toBe(true);
    expect(onDisk().name).toBe("fixed");
    expect(store.getState()).toMatchObject({ saveProblem: "", unsaved: false });
  });

  it("saves a newer draft after a failed put of an older one", async () => {
    const { api, store, onDisk } = setup();
    await store.getState().activate(context);
    let fail = (_error: unknown) => {};
    api.putProfile.mockReturnValueOnce(
      new Promise((_resolve, reject) => {
        fail = reject;
      }),
    );
    store.getState().edit(named(store, "first"));
    await vi.waitFor(() => expect(api.putProfile).toHaveBeenCalledOnce());
    store.getState().edit(named(store, "second"));
    fail({ message: "busy" });
    expect(await store.getState().flush()).toBe(true);
    expect(onDisk().name).toBe("second");
  });

  it("keeps the draft when only the pad changes", async () => {
    const { api, store } = setup();
    await store.getState().activate(context);
    api.putProfile.mockRejectedValue({ message: "disk full" });
    store.getState().edit(named(store, "mine"));
    await settled();
    expect(store.getState().saveProblem).toBe("failed");
    await store.getState().activate({ ...context, device: "d2" });
    expect(api.getProfile).toHaveBeenCalledOnce();
    expect(store.getState()).toMatchObject({
      context: { ...context, device: "d2" },
      saveProblem: "failed",
      unsaved: true,
    });
    expect(store.getState().draft?.name).toBe("mine");
  });

  it("keeps a draft that could not be saved across a reconnect and saves it then", async () => {
    const { api, store, onDisk } = setup();
    await store.getState().activate(context);
    store.getState().edit(named(store, "before"));
    await store.getState().flush();
    // The connection drops while the next edit is being saved.
    const put = api.putProfile.getMockImplementation();
    api.putProfile.mockRejectedValue({ message: "bridge closed" });
    store.getState().edit(named(store, "during"));
    await store.getState().activate(null);
    expect(store.getState().draft).toBeNull();
    if (put) api.putProfile.mockImplementation(put);
    api.putProfile.mockClear();
    await store.getState().activate({ ...context, connection: "c2" });
    expect(store.getState().draft?.name).toBe("during");
    expect(await store.getState().flush()).toBe(true);
    expect(api.putProfile).toHaveBeenCalledOnce();
    expect(api.putProfile.mock.calls[0]?.[2]).toBe("r2");
    expect(onDisk().name).toBe("during");
    expect(store.getState()).toMatchObject({
      reloaded: false,
      discarded: false,
      canUndo: true,
    });
  });

  it("lets the file win over a kept draft when it changed meanwhile", async () => {
    const { api, store, writeElsewhere } = setup();
    await store.getState().activate(context);
    api.putProfile.mockRejectedValueOnce({ message: "bridge closed" });
    store.getState().edit(named(store, "mine"));
    await store.getState().activate(null);
    writeElsewhere("paint", {
      schemaVersion: 1,
      name: "theirs",
      layers: { base: {} },
    });
    await store.getState().activate({ ...context, connection: "c2" });
    expect(store.getState().draft?.name).toBe("theirs");
    expect(store.getState()).toMatchObject({ reloaded: true, unsaved: false });
    await store.getState().flush();
    expect(api.putProfile).toHaveBeenCalledOnce();
  });

  it("says so when a switch to another profile drops an unsaved draft", async () => {
    const { api, store } = setup();
    await store.getState().activate(context);
    api.validateProfile.mockResolvedValueOnce({
      valid: false,
      diagnostics: [],
    });
    store.getState().edit(named(store, "refused"));
    await store.getState().flush();
    // The daemon switched profiles by itself.
    await store
      .getState()
      .activate({ id: "sketch", device: "d", connection: "c" });
    expect(store.getState()).toMatchObject({ id: "sketch", discarded: true });
    store.getState().dismissDiscarded();
    expect(store.getState().discarded).toBe(false);
    // Back on paint, nothing comes back.
    await store.getState().activate(context);
    expect(store.getState().draft?.name).toBe("p");
    expect(store.getState().discarded).toBe(false);
  });

  it("follows the last request when the context flips back during a save", async () => {
    const { api, store } = setup();
    await store.getState().activate(context);
    let finish = (_value: ProfileSaveResult) => {};
    api.putProfile.mockReturnValueOnce(
      new Promise((resolve) => {
        finish = resolve;
      }),
    );
    store.getState().edit(named(store, "mine"));
    await vi.waitFor(() => expect(api.putProfile).toHaveBeenCalledOnce());
    const other = { id: "sketch", device: "d", connection: "c" };
    const toOther = store.getState().activate(other);
    const back = store.getState().activate(context);
    finish({ revision: "r2", applied: true });
    await Promise.all([toOther, back]);
    expect(store.getState().id).toBe("paint");
    expect(store.getState().context).toEqual(context);
    expect(api.getProfile.mock.calls.filter(([id]) => id === "sketch")).toEqual(
      [],
    );
  });

  it("keeps a rename out of undo and redo", async () => {
    const { store, onDisk } = setup();
    await store.getState().activate(context);
    store.getState().edit(withNorth(store, { type: "keys", keys: "F13" }));
    store.getState().edit(named(store, "renamed"), { history: false });
    expect(store.getState().undo()).toBe(true);
    expect(store.getState().draft?.layers.base.north).toBeUndefined();
    expect(store.getState().draft?.name).toBe("renamed");
    expect(store.getState().canUndo).toBe(false);
    expect(store.getState().redo()).toBe(true);
    expect(store.getState().draft?.layers.base.north).toBeDefined();
    expect(store.getState().draft?.name).toBe("renamed");
    await store.getState().flush();
    expect(onDisk().name).toBe("renamed");
  });
});

// The profile format is frozen at version 1 (PRO-01). Files of version 0 open
// as they are; the editor never rewrites one until the user edits it, and
// then it writes the whole document at the current version.
describe("the profile format version", () => {
  it("does not save a version 0 file because it was opened", async () => {
    const { api, store, onDisk } = setup();
    await store.getState().activate(context);
    await settled();
    expect(draft(store).schemaVersion).toBe(0);
    expect(store.getState().unsaved).toBe(false);
    expect(api.putProfile).not.toHaveBeenCalled();
    expect(onDisk().schemaVersion).toBe(0);
  });

  it("writes version 1 when the user edits a version 0 file", async () => {
    const { api, store, onDisk } = setup();
    await store.getState().activate(context);
    store.getState().edit(named(store, "changed"));
    expect(await store.getState().flush()).toBe(true);
    expect(api.putProfile).toHaveBeenCalledOnce();
    expect(
      JSON.parse(api.putProfile.mock.calls[0]?.[1] as string),
    ).toMatchObject({
      schemaVersion: 1,
      name: "changed",
    });
    expect(onDisk()).toMatchObject({ schemaVersion: 1, name: "changed" });
    // Nothing is left to save.
    expect(store.getState().unsaved).toBe(false);
    await settled();
    expect(api.putProfile).toHaveBeenCalledOnce();
  });

  it("keeps a version 1 file at version 1", async () => {
    const { api, store, writeElsewhere, onDisk } = setup();
    writeElsewhere("paint", {
      schemaVersion: 1,
      name: "p",
      layers: { base: {} },
    });
    await store.getState().activate(context);
    store.getState().edit(named(store, "changed"));
    expect(await store.getState().flush()).toBe(true);
    expect(api.putProfile).toHaveBeenCalledOnce();
    expect(onDisk().schemaVersion).toBe(1);
  });
});

// The mapping page shows the usual bindings or the function set (UI-30,
// ADR-0075); which one is view state, not part of the profile.
describe("the shown key set (function key)", () => {
  const key = (value: string) =>
    ({ type: "keys", keys: value, mode: "tap" }) as const;
  // Under the default function key (Menu), back is an ordinary control.
  const withBack: Profile = {
    schemaVersion: 1,
    name: "p",
    layers: {
      base: {
        south: { press: key("Ctrl+Z") },
        back: { press: key("Ctrl+V") },
        start: { press: key("Ctrl+S") },
      },
    },
  };
  // The old format: View's set and Menu's, which stand for one set.
  const old: Profile = {
    schemaVersion: 1,
    name: "p",
    layers: { base: { south: { press: key("Ctrl+Z") } } },
    functionKeys: {
      back: {
        east: { press: key("F14") },
        north: { press: key("F15") },
      },
      start: { north: { press: key("F13") } },
    },
  };

  it("starts as the usual bindings and is set from the page", async () => {
    const { store } = setup();
    await store.getState().activate(context);
    expect(store.getState().view).toBe("normal");
    store.getState().setView("function");
    expect(store.getState().view).toBe("function");
    // Not part of the profile: nothing to save, nothing to undo.
    expect(store.getState().unsaved).toBe(false);
    expect(store.getState().canUndo).toBe(false);
  });

  it("goes back to the usual bindings when another profile is shown", async () => {
    const { store } = setup();
    await store.getState().activate(context);
    store.getState().setView("function");
    await store.getState().activate({ ...context, id: "other" });
    expect(store.getState().view).toBe("normal");
  });

  it("stays when the same profile is read again or the pad changes", async () => {
    const { store } = setup();
    await store.getState().activate(context);
    store.getState().setView("function");
    await store.getState().reload();
    expect(store.getState().view).toBe("function");
    await store.getState().activate({ ...context, device: "d2" });
    expect(store.getState().view).toBe("function");
  });

  it("goes back to the usual bindings when the profile is read on a new connection", async () => {
    const { store } = setup();
    await store.getState().activate(context);
    store.getState().setView("function");
    await store.getState().activate({ ...context, connection: "c2" });
    expect(store.getState().view).toBe("normal");
  });

  it("is Menu as the function key until the settings choose another", async () => {
    const { store } = setup();
    expect(store.getState().functionKey).toBe("start");
    store.getState().setFunctionKey("guide");
    expect(store.getState().functionKey).toBe("guide");
    store.getState().setFunctionKey("back");
    expect(store.getState().functionKey).toBe("back");
    // The profile is not part of it: nothing to save.
    await store.getState().activate(context);
    store.getState().setFunctionKey("start");
    expect(store.getState().unsaved).toBe(false);
    expect(store.getState().canUndo).toBe(false);
  });

  it("counts what a layer binds on the function key as nothing, whichever key it is", async () => {
    const { store, writeElsewhere } = setup();
    writeElsewhere("paint", {
      ...withBack,
      layers: { base: withBack.layers.base },
      common: { back: { block: true } },
    });
    await store.getState().activate(context);
    // Menu is the function key: View's block in common is an ordinary one.
    expect(store.getState().usage?.blocks).toBe(1);
    expect(store.getState().usage?.commonBindings).toBe(1);
    // With View chosen, the daemon ignores it, and the page follows.
    store.getState().setFunctionKey("back");
    expect(store.getState().usage?.blocks).toBe(0);
    expect(store.getState().usage?.commonBindings).toBe(0);
    store.getState().setFunctionKey("start");
    expect(store.getState().usage?.blocks).toBe(1);
  });

  it("keeps the bindings on back and start in the file through a save", async () => {
    const { api, store, writeElsewhere, onDisk } = setup();
    writeElsewhere("paint", withBack);
    await store.getState().activate(context);
    store.getState().edit(named(store, "changed"));
    expect(await store.getState().flush()).toBe(true);
    expect(api.putProfile).toHaveBeenCalledOnce();
    expect(onDisk().layers.base).toEqual(withBack.layers.base);
    expect(onDisk().name).toBe("changed");
    expect(store.getState().unsaved).toBe(false);
  });

  it("does not save a file only because it was opened with the old functionKeys", async () => {
    const { api, store, writeElsewhere } = setup();
    writeElsewhere("paint", old);
    await store.getState().activate(context);
    await settled();
    expect(store.getState().unsaved).toBe(false);
    expect(api.putProfile).not.toHaveBeenCalled();
  });

  it("saves the old functionKeys as the one functionSet they stand for, with the next save", async () => {
    const { api, store, writeElsewhere, onDisk } = setup();
    writeElsewhere("paint", old);
    await store.getState().activate(context);
    // The page reads them as the set already.
    expect(draft(store).functionSet).toEqual({
      north: { press: key("F13") },
      east: { press: key("F14") },
    });
    store.getState().edit(named(store, "changed"));
    expect(await store.getState().flush()).toBe(true);
    expect(api.putProfile).toHaveBeenCalledOnce();
    expect(onDisk().functionSet).toEqual({
      north: { press: key("F13") },
      east: { press: key("F14") },
    });
    expect(onDisk()).not.toHaveProperty("functionKeys");
    expect(store.getState().unsaved).toBe(false);
  });

  it("saves a binding of the set under functionSet", async () => {
    const { store, onDisk } = setup();
    await store.getState().activate(context);
    const next = swapView(draft(store), "function");
    const edited = setBinding(next, "base", "south", {
      press: key("Ctrl+Shift+Z"),
    });
    store.getState().edit(swapView(edited, "function"));
    expect(await store.getState().flush()).toBe(true);
    expect(onDisk().functionSet).toEqual({
      south: { press: key("Ctrl+Shift+Z") },
    });
    expect(onDisk().layers.base).toEqual({});
  });

  it("keeps the warning the daemon gives about a file until a save writes another", async () => {
    const { api, store, writeElsewhere } = setup();
    writeElsewhere("paint", old);
    const warning = {
      severity: "warning" as const,
      pointer: "/functionKeys",
      offset: 0,
      line: 1,
      col: 1,
      message: "functionKeys is ignored because functionSet is also written",
    };
    api.getProfile.mockImplementationOnce(async (id: string) => ({
      id,
      text: JSON.stringify(old),
      revision: "r2",
      diagnostics: [warning],
    }));
    await store.getState().activate(context);
    expect(store.getState().diagnostics).toEqual([warning]);
    // The saved text has no functionKeys, so the daemon has no such warning.
    store.getState().edit(named(store, "changed"));
    expect(await store.getState().flush()).toBe(true);
    expect(store.getState().diagnostics).toEqual([]);
  });
});
