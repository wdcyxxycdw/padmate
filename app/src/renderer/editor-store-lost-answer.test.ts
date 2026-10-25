import { createHash } from "node:crypto";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  PadmateAPI,
  ProfileDocument,
  ProfileSaveResult,
  ProfileValidation,
} from "../shared/api";
import type { Profile } from "../shared/generated/schema";
import { createEditorStore, type EditorStore } from "./editor-store";

// A save whose answer is lost (UI-43). The daemon answers profiles.put only
// after it has applied the profile, and the bridge gives up on a request
// after 10 s (BridgeClient.call), so a put can write profile.json and still
// fail for the editor. The revision it holds is then stale: the editor has to
// tell its own write from a change made elsewhere.

/** What becomes of a put: it is answered, or the editor never hears back. */
type Fate = "answered" | "lost after write" | "lost before write";

const TIMEOUT_MS = 10000;
const base = JSON.stringify({
  schemaVersion: 1,
  name: "p",
  layers: { base: {} },
});
const context = { id: "paint", device: "d", connection: "c" };
const hash = (text: string) => createHash("sha256").update(text).digest("hex");
const sleep = (ms: number) =>
  new Promise<void>((resolve) => setTimeout(resolve, ms));
/** A request the bridge gave up on: what the editor gets has no code and no data. */
const timedOut = () => ({ message: "Daemon request timed out" });

/** A daemon with the real revision rule (a hash of the bytes) and puts that can be lost. */
function daemon() {
  let disk = { text: base, revision: hash(base) };
  const fates: Fate[] = [];
  const writes: string[] = [];
  const puts: Array<{ text: string; revision: string }> = [];
  let slowValidations = 0;
  let failingReads = 0;
  let gate: Promise<void> | null = null;
  let openGate = () => {};
  const api = {
    getProfile: vi.fn(async (id: string): Promise<ProfileDocument> => {
      const read = { id, text: disk.text, revision: disk.revision };
      // A read held back answers with the file as it was when it was asked.
      if (gate) await gate;
      if (failingReads > 0) {
        failingReads--;
        throw timedOut();
      }
      return { ...read, diagnostics: [] };
    }),
    listProfiles: vi.fn(async () => []),
    getScriptDescription: vi.fn(async () => ({
      metadata: { runtime: 0, actions: [] },
    })),
    getScriptRegistrations: vi.fn(async () => []),
    listMacros: vi.fn(async () => ({ macros: [], missing: [] })),
    validateProfile: vi.fn(async (): Promise<ProfileValidation> => {
      if (slowValidations > 0) {
        slowValidations--;
        await sleep(TIMEOUT_MS);
        throw timedOut();
      }
      return { valid: true, diagnostics: [] };
    }),
    putProfile: vi.fn(
      async (
        _id: string,
        text: string,
        revision: string,
      ): Promise<ProfileSaveResult> => {
        puts.push({ text, revision });
        const fate = fates.shift() ?? "answered";
        if (fate === "lost before write") {
          await sleep(TIMEOUT_MS);
          throw timedOut();
        }
        if (revision !== disk.revision)
          throw {
            message:
              "profile library: revision conflict; read the document again",
            code: -32009,
            data: { revision: disk.revision },
          };
        disk = { text, revision: hash(text) };
        writes.push(text);
        if (fate === "lost after write") {
          await sleep(TIMEOUT_MS);
          throw timedOut();
        }
        return { revision: disk.revision, applied: true };
      },
    ),
  };
  return {
    api,
    disk: () => disk,
    /** The texts the daemon wrote, in order. */
    writes,
    /** Every put it received. */
    puts,
    /** The fates of the next puts; the ones after them are answered. */
    lose: (...next: Fate[]) => fates.push(...next),
    /** The next validation is not answered in time. */
    loseValidation: () => {
      slowValidations++;
    },
    /** The next n reads are not answered in time. */
    loseReads: (n: number) => {
      failingReads = n;
    },
    /** Reads wait until the returned function is called. */
    holdReads: () => {
      gate = new Promise<void>((resolve) => {
        openGate = () => {
          gate = null;
          resolve();
        };
      });
      return () => openGate();
    },
    /** Another program writes the file. */
    writeElsewhere: (text: string) => {
      disk = { text, revision: hash(text) };
    },
  };
}

const withKey = (draft: Profile, control: string, keys: string): Profile => ({
  ...draft,
  layers: {
    ...draft.layers,
    base: {
      ...draft.layers.base,
      [control]: { press: { type: "keys", keys } },
    },
  },
});
const keysOf = (profile: Profile) =>
  ["rs.cw", "rs.ccw"].map(
    (control) =>
      (
        profile.layers.base as Record<
          string,
          { press?: { keys?: string } } | undefined
        >
      )[control]?.press?.keys,
  );
const draftOf = (store: EditorStore) => store.getState().draft as Profile;
/** Records a shortcut for control, the way the key drawer commits one. */
const record = (store: EditorStore, control: string, keys: string) =>
  store.getState().edit(withKey(draftOf(store), control, keys));
async function open(d: ReturnType<typeof daemon>) {
  const store = createEditorStore(d.api as unknown as PadmateAPI);
  await store.getState().activate(context);
  return store;
}
/** The file, the draft and the notices, as the user would meet them. */
function outcome(store: EditorStore, d: ReturnType<typeof daemon>) {
  const state = store.getState();
  return {
    disk: keysOf(JSON.parse(d.disk().text)),
    draft: keysOf(draftOf(store)),
    reloaded: state.reloaded,
    saveProblem: state.saveProblem,
    unsaved: state.unsaved,
    inSync: state.revision === d.disk().revision,
  };
}
const advance = (ms: number) => vi.advanceTimersByTimeAsync(ms);

beforeEach(() => {
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
});

describe("a save whose answer is lost", () => {
  it("keeps the shortcut recorded while the answer is missing", async () => {
    const d = daemon();
    const store = await open(d);
    d.lose("lost after write");
    // rs.cw is recorded and written; rs.ccw 0.7 s later, before the answer
    // is given up on.
    record(store, "rs.cw", "F21");
    await advance(700);
    record(store, "rs.ccw", "F22");
    await advance(60000);
    expect(outcome(store, d)).toEqual({
      disk: ["F21", "F22"],
      draft: ["F21", "F22"],
      reloaded: false,
      saveProblem: "",
      unsaved: false,
      inSync: true,
    });
    // The file was not taken for a change elsewhere: the steps are still there.
    expect(store.getState().canUndo).toBe(true);
  });

  it("saves the shortcut recorded meanwhile when nothing was written", async () => {
    const d = daemon();
    const store = await open(d);
    d.lose("lost before write");
    record(store, "rs.cw", "F21");
    await advance(700);
    record(store, "rs.ccw", "F22");
    await advance(60000);
    expect(outcome(store, d)).toEqual({
      disk: ["F21", "F22"],
      draft: ["F21", "F22"],
      reloaded: false,
      saveProblem: "",
      unsaved: false,
      inSync: true,
    });
    // The lost put and one that saved both; the revision was never stale.
    expect(d.puts.map((put) => put.revision)).toEqual([hash(base), hash(base)]);
    expect(d.writes).toHaveLength(1);
  });

  it("saves nothing more on a retry when there is no newer edit", async () => {
    const d = daemon();
    const store = await open(d);
    d.lose("lost after write");
    record(store, "rs.cw", "F21");
    await advance(TIMEOUT_MS + 100);
    expect(store.getState()).toMatchObject({
      saveProblem: "failed",
      saveError: "Daemon request timed out",
      unsaved: true,
    });
    store.getState().retry();
    await advance(100);
    expect(outcome(store, d)).toEqual({
      disk: ["F21", undefined],
      draft: ["F21", undefined],
      reloaded: false,
      saveProblem: "",
      unsaved: false,
      inSync: true,
    });
    // The retry was refused as a conflict with the editor's own write.
    expect(d.puts).toHaveLength(2);
    expect(d.writes).toHaveLength(1);
  });

  it("saves a newer edit on top of the write whose answer was lost", async () => {
    const d = daemon();
    const store = await open(d);
    d.lose("lost after write");
    record(store, "rs.cw", "F21");
    await advance(TIMEOUT_MS + 100);
    expect(store.getState().saveProblem).toBe("failed");
    // The user records the next shortcut after the failure showed.
    record(store, "rs.ccw", "F22");
    await advance(100);
    expect(outcome(store, d)).toEqual({
      disk: ["F21", "F22"],
      draft: ["F21", "F22"],
      reloaded: false,
      saveProblem: "",
      unsaved: false,
      inSync: true,
    });
  });

  it.each(["lost after write", "lost before write"] as const)(
    "lets a real change elsewhere win (%s)",
    async (fate) => {
      const d = daemon();
      const store = await open(d);
      d.lose(fate);
      record(store, "rs.cw", "F21");
      await advance(700);
      // Another program writes the file while the answer is still missing; it
      // is not the editor's text, so the file wins and the notice says so.
      d.writeElsewhere(
        JSON.stringify(withKey(JSON.parse(base) as Profile, "rs.cw", "F9")),
      );
      record(store, "rs.ccw", "F22");
      await advance(60000);
      expect(outcome(store, d)).toEqual({
        disk: ["F9", undefined],
        draft: ["F9", undefined],
        reloaded: true,
        saveProblem: "",
        unsaved: false,
        inSync: true,
      });
      expect(store.getState().canUndo).toBe(false);
    },
  );

  it("forgets a lost answer once a later save was answered", async () => {
    const d = daemon();
    const store = await open(d);
    d.lose("lost before write");
    record(store, "rs.cw", "F21");
    await advance(TIMEOUT_MS + 100);
    expect(store.getState().saveProblem).toBe("failed");
    // The next edit saves, and the file is known again.
    record(store, "rs.ccw", "F22");
    await advance(100);
    expect(store.getState().unsaved).toBe(false);
    // Another program writes the very text of the put that was lost. It is
    // a change elsewhere all the same: the editor knows the file.
    const lost = d.puts[0].text;
    d.writeElsewhere(lost);
    record(store, "rs.ccw", "F23");
    await advance(100);
    expect(outcome(store, d)).toEqual({
      disk: ["F21", undefined],
      draft: ["F21", undefined],
      reloaded: true,
      saveProblem: "",
      unsaved: false,
      inSync: true,
    });
  });

  it("saves the edits kept across a reconnect on top of the write", async () => {
    const d = daemon();
    const store = await open(d);
    d.lose("lost after write", "lost before write");
    record(store, "rs.cw", "F21");
    await advance(TIMEOUT_MS + 100);
    // The connection is down: the next save is not answered either.
    record(store, "rs.ccw", "F22");
    await advance(TIMEOUT_MS + 100);
    expect(store.getState()).toMatchObject({
      saveProblem: "failed",
      unsaved: true,
    });
    await store.getState().activate(null);
    await store.getState().activate({ ...context, connection: "c2" });
    await advance(100);
    expect(outcome(store, d)).toEqual({
      disk: ["F21", "F22"],
      draft: ["F21", "F22"],
      reloaded: false,
      saveProblem: "",
      unsaved: false,
      inSync: true,
    });
  });

  describe("followed by profile.changed", () => {
    // The daemon publishes it when the profile is applied, shortly before it
    // would have answered.
    it.each([
      ["while the editor still waits", 2000],
      ["after the failure showed", TIMEOUT_MS + 2000],
    ])("is no change elsewhere (%s)", async (_when, at) => {
      const d = daemon();
      const store = await open(d);
      d.lose("lost after write");
      record(store, "rs.cw", "F21");
      await advance(at);
      void store.getState().profileChanged();
      await advance(TIMEOUT_MS + 2000);
      expect(outcome(store, d)).toEqual({
        disk: ["F21", undefined],
        draft: ["F21", undefined],
        reloaded: false,
        saveProblem: "",
        unsaved: false,
        inSync: true,
      });
      expect(d.writes).toHaveLength(1);
      expect(store.getState().canUndo).toBe(true);
    });

    it("still reloads a change made elsewhere", async () => {
      const d = daemon();
      const store = await open(d);
      d.lose("lost after write");
      record(store, "rs.cw", "F21");
      await advance(TIMEOUT_MS + 100);
      d.writeElsewhere(
        JSON.stringify(withKey(JSON.parse(base) as Profile, "rs.cw", "F9")),
      );
      await store.getState().profileChanged();
      expect(outcome(store, d)).toEqual({
        disk: ["F9", undefined],
        draft: ["F9", undefined],
        reloaded: true,
        saveProblem: "",
        unsaved: false,
        inSync: true,
      });
    });
  });
});

describe("a validation whose answer is lost", () => {
  it("leaves the next save with the revision it had", async () => {
    const d = daemon();
    const store = await open(d);
    d.loseValidation();
    record(store, "rs.cw", "F21");
    await advance(700);
    record(store, "rs.ccw", "F22");
    await advance(60000);
    // Nothing reached the daemon before the second draft: one put, on the
    // revision that was read.
    expect(d.puts.map((put) => put.revision)).toEqual([hash(base)]);
    expect(outcome(store, d)).toEqual({
      disk: ["F21", "F22"],
      draft: ["F21", "F22"],
      reloaded: false,
      saveProblem: "",
      unsaved: false,
      inSync: true,
    });
  });

  it("does not make the editor forget a put whose answer was lost", async () => {
    const d = daemon();
    const store = await open(d);
    d.lose("lost after write");
    record(store, "rs.cw", "F21");
    await advance(TIMEOUT_MS + 100);
    // Only the last few puts are remembered: validations are not puts.
    for (const keys of ["F22", "F23", "F24", "F25"]) {
      d.loseValidation();
      record(store, "rs.ccw", keys);
      await advance(TIMEOUT_MS + 100);
      expect(store.getState().saveProblem).toBe("failed");
    }
    record(store, "rs.ccw", "F26");
    await advance(100);
    expect(outcome(store, d)).toEqual({
      disk: ["F21", "F26"],
      draft: ["F21", "F26"],
      reloaded: false,
      saveProblem: "",
      unsaved: false,
      inSync: true,
    });
  });

  it("is retried like any failed save", async () => {
    const d = daemon();
    const store = await open(d);
    d.loseValidation();
    record(store, "rs.cw", "F21");
    await advance(TIMEOUT_MS + 100);
    expect(store.getState()).toMatchObject({
      saveProblem: "failed",
      unsaved: true,
    });
    expect(d.puts).toHaveLength(0);
    store.getState().retry();
    await advance(100);
    expect(d.puts.map((put) => put.revision)).toEqual([hash(base)]);
    expect(outcome(store, d)).toEqual({
      disk: ["F21", undefined],
      draft: ["F21", undefined],
      reloaded: false,
      saveProblem: "",
      unsaved: false,
      inSync: true,
    });
  });
});

/** The notices and flags of an editor that has no trouble and is in sync with the file. */
const synced = {
  reloaded: false,
  saveProblem: "",
  unsaved: false,
  inSync: true,
};

describe("a lost answer meets other events", () => {
  it("keeps the edit when reading the file after the conflict fails once", async () => {
    const d = daemon();
    const store = await open(d);
    d.lose("lost after write");
    record(store, "rs.cw", "F21");
    await advance(700);
    record(store, "rs.ccw", "F22");
    // The read after the conflict is lost too.
    d.loseReads(1);
    await advance(TIMEOUT_MS + 500);
    expect(store.getState()).toMatchObject({
      saveProblem: "failed",
      unsaved: true,
      reloaded: false,
    });
    // The retry reads the file again: it is the editor's own write still.
    store.getState().retry();
    await advance(100);
    expect(outcome(store, d)).toEqual({
      disk: ["F21", "F22"],
      draft: ["F21", "F22"],
      ...synced,
    });
  });

  it("does not let failed retries push the lost write out", async () => {
    const d = daemon();
    const store = await open(d);
    // The first put is written; then the daemon cannot be reached for the
    // put that follows and for three retries of it.
    d.lose(
      "lost after write",
      "lost before write",
      "lost before write",
      "lost before write",
      "lost before write",
    );
    record(store, "rs.cw", "F21");
    await advance(700);
    record(store, "rs.ccw", "F22");
    await advance(TIMEOUT_MS * 2 + 200);
    expect(store.getState().saveProblem).toBe("failed");
    for (let retry = 0; retry < 3; retry++) {
      store.getState().retry();
      await advance(TIMEOUT_MS + 100);
      expect(store.getState().saveProblem).toBe("failed");
    }
    // Back at the daemon: its file is the first put, the editor's own.
    store.getState().retry();
    await advance(100);
    expect(outcome(store, d)).toEqual({
      disk: ["F21", "F22"],
      draft: ["F21", "F22"],
      ...synced,
    });
  });

  it("takes a file that another program wrote with the same bytes as the editor's", async () => {
    const d = daemon();
    const store = await open(d);
    d.lose("lost before write");
    record(store, "rs.cw", "F21");
    await advance(700);
    // Nothing of the editor's reached the file, but the program wrote the
    // very text it tried to save: nothing is lost by taking it for its own.
    d.writeElsewhere(
      `${JSON.stringify(withKey(JSON.parse(base) as Profile, "rs.cw", "F21"), null, 2)}\n`,
    );
    record(store, "rs.ccw", "F22");
    await advance(60000);
    expect(outcome(store, d)).toEqual({
      disk: ["F21", "F22"],
      draft: ["F21", "F22"],
      ...synced,
    });
  });

  it("does not take a lost write of one profile for a write of another", async () => {
    const d = daemon();
    const store = await open(d);
    d.lose("lost after write");
    record(store, "rs.cw", "F21");
    await advance(TIMEOUT_MS + 100);
    expect(store.getState().saveProblem).toBe("failed");
    const lost = d.disk().text;
    // The next profile starts from a file that is something else, which a
    // program then replaces with the text that put wrote to the first.
    d.writeElsewhere(
      JSON.stringify(withKey(JSON.parse(base) as Profile, "rs.cw", "F9")),
    );
    await store.getState().activate({ ...context, id: "other" });
    expect(store.getState().discarded).toBe(true);
    d.writeElsewhere(lost);
    record(store, "rs.ccw", "F22");
    await advance(100);
    expect(outcome(store, d)).toEqual({
      disk: ["F21", undefined],
      draft: ["F21", undefined],
      reloaded: true,
      saveProblem: "",
      unsaved: false,
      inSync: true,
    });
  });

  it("finishes the adoption before it switches profile", async () => {
    const d = daemon();
    const store = await open(d);
    d.lose("lost after write");
    record(store, "rs.cw", "F21");
    await advance(700);
    record(store, "rs.ccw", "F22");
    // The read after the conflict is on its way when the profile changes.
    const release = d.holdReads();
    await advance(TIMEOUT_MS + 100);
    const switched = store.getState().activate({ ...context, id: "other" });
    await advance(100);
    release();
    await switched;
    await advance(100);
    expect(store.getState()).toMatchObject({
      id: "other",
      reloaded: false,
      discarded: false,
      unsaved: false,
    });
    expect(keysOf(JSON.parse(d.disk().text))).toEqual(["F21", "F22"]);
    expect(keysOf(draftOf(store))).toEqual(["F21", "F22"]);
  });

  it("finishes the adoption before it reads the file again", async () => {
    const d = daemon();
    const store = await open(d);
    d.lose("lost after write");
    record(store, "rs.cw", "F21");
    await advance(700);
    record(store, "rs.ccw", "F22");
    const release = d.holdReads();
    await advance(TIMEOUT_MS + 100);
    const reading = store.getState().reload();
    await advance(100);
    release();
    await reading;
    await advance(100);
    expect(outcome(store, d)).toEqual({
      disk: ["F21", "F22"],
      draft: ["F21", "F22"],
      ...synced,
    });
  });

  it("undoes and redoes the steps on top of the adopted write", async () => {
    const d = daemon();
    const store = await open(d);
    d.lose("lost after write");
    record(store, "rs.cw", "F21");
    await advance(700);
    record(store, "rs.ccw", "F22");
    await advance(60000);
    expect(store.getState().canUndo).toBe(true);
    expect(store.getState().undo()).toBe(true);
    await advance(100);
    expect(outcome(store, d)).toEqual({
      disk: ["F21", undefined],
      draft: ["F21", undefined],
      ...synced,
    });
    expect(store.getState().undo()).toBe(true);
    await advance(100);
    expect(outcome(store, d).disk).toEqual([undefined, undefined]);
    expect(store.getState().redo()).toBe(true);
    expect(store.getState().redo()).toBe(true);
    await advance(100);
    expect(outcome(store, d)).toEqual({
      disk: ["F21", "F22"],
      draft: ["F21", "F22"],
      ...synced,
    });
  });

  it("keeps the steps across a reconnect on top of the adopted write", async () => {
    const d = daemon();
    const store = await open(d);
    d.lose("lost after write", "lost before write");
    record(store, "rs.cw", "F21");
    await advance(TIMEOUT_MS + 100);
    record(store, "rs.ccw", "F22");
    await advance(TIMEOUT_MS + 100);
    await store.getState().activate(null);
    await store.getState().activate({ ...context, connection: "c2" });
    await advance(100);
    expect(outcome(store, d)).toEqual({
      disk: ["F21", "F22"],
      draft: ["F21", "F22"],
      ...synced,
    });
    expect(store.getState().canUndo).toBe(true);
    expect(store.getState().undo()).toBe(true);
    await advance(100);
    expect(outcome(store, d)).toEqual({
      disk: ["F21", undefined],
      draft: ["F21", undefined],
      ...synced,
    });
  });

  it("shows the file of a reconnect that is not the editor's own write", async () => {
    const d = daemon();
    const store = await open(d);
    d.lose("lost before write");
    record(store, "rs.cw", "F21");
    await advance(TIMEOUT_MS + 100);
    expect(store.getState().saveProblem).toBe("failed");
    d.writeElsewhere(
      JSON.stringify(withKey(JSON.parse(base) as Profile, "rs.cw", "F9")),
    );
    await store.getState().activate(null);
    await store.getState().activate({ ...context, connection: "c2" });
    await advance(100);
    expect(outcome(store, d)).toEqual({
      disk: ["F9", undefined],
      draft: ["F9", undefined],
      reloaded: true,
      saveProblem: "",
      unsaved: false,
      inSync: true,
    });
    expect(store.getState().canUndo).toBe(false);
  });
});

describe("an edit undone while the answer is missing", () => {
  it("is written back once the event for the lost write arrives", async () => {
    const d = daemon();
    const store = await open(d);
    d.lose("lost after write");
    record(store, "rs.cw", "F21");
    await advance(700);
    // Back to the file the editor knows, while the put is still out.
    expect(store.getState().undo()).toBe(true);
    await advance(TIMEOUT_MS + 100);
    void store.getState().profileChanged();
    await advance(100);
    expect(outcome(store, d)).toEqual({
      disk: [undefined, undefined],
      draft: [undefined, undefined],
      ...synced,
    });
  });

  it("is written back when the event came before the failure", async () => {
    const d = daemon();
    const store = await open(d);
    d.lose("lost after write");
    record(store, "rs.cw", "F21");
    await advance(700);
    expect(store.getState().undo()).toBe(true);
    void store.getState().profileChanged();
    await advance(TIMEOUT_MS + 100);
    expect(outcome(store, d)).toEqual({
      disk: [undefined, undefined],
      draft: [undefined, undefined],
      ...synced,
    });
  });

  it("is written back without an event", async () => {
    const d = daemon();
    const store = await open(d);
    d.lose("lost after write");
    record(store, "rs.cw", "F21");
    await advance(700);
    expect(store.getState().undo()).toBe(true);
    await advance(TIMEOUT_MS + 5000);
    expect(outcome(store, d)).toEqual({
      disk: [undefined, undefined],
      draft: [undefined, undefined],
      ...synced,
    });
  });

  it("leaves the file alone when nothing was written", async () => {
    const d = daemon();
    const store = await open(d);
    d.lose("lost before write");
    record(store, "rs.cw", "F21");
    await advance(700);
    expect(store.getState().undo()).toBe(true);
    await advance(TIMEOUT_MS + 5000);
    expect(outcome(store, d)).toEqual({
      disk: [undefined, undefined],
      draft: [undefined, undefined],
      ...synced,
    });
    expect(store.getState().error).toBe("");
    expect(d.writes).toHaveLength(0);
  });

  it("shows a change made elsewhere meanwhile, without a notice", async () => {
    const d = daemon();
    const store = await open(d);
    d.lose("lost before write");
    record(store, "rs.cw", "F21");
    await advance(700);
    expect(store.getState().undo()).toBe(true);
    // The edit was undone, so nothing of the editor's is lost to the file.
    d.writeElsewhere(
      JSON.stringify(withKey(JSON.parse(base) as Profile, "rs.cw", "F9")),
    );
    await advance(TIMEOUT_MS + 5000);
    expect(outcome(store, d)).toEqual({
      disk: ["F9", undefined],
      draft: ["F9", undefined],
      ...synced,
    });
  });
});
