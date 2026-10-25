import { describe, expect, it, vi } from "vitest";
import type {
  PadmateAPI,
  ProfileDiagnostic,
  ProfileDocument,
  ProfileEntry,
  ProfileResolution,
} from "../shared/api";
import type { Profile } from "../shared/generated/schema";
import { createEditorStore, type EditorStore } from "./editor-store";

// The profile a profile is based on, which the editor reads next to it
// (PRO-19, ADR-0070).

const parentProfile: Profile = {
  schemaVersion: 1,
  name: "Paint",
  layers: { base: { south: { tap: { type: "keys", keys: "Ctrl+Z" } } } },
};
const child: Profile = {
  schemaVersion: 1,
  name: "Child",
  extends: "paint",
  layers: { base: { east: { tap: { type: "keys", keys: "E" } } } },
};
const context = { id: "child", device: "d", connection: "c" };

function setup(
  files: Record<string, Profile> = { child, paint: parentProfile },
  options: { diagnostics?: ProfileDiagnostic[] } = {},
) {
  const resolution = (id: string): ProfileResolution => ({
    id,
    text: JSON.stringify(files[id]),
    revision: "r1",
    inherited: [],
    diagnostics: [],
  });
  const api = {
    getProfile: vi.fn(
      async (id: string): Promise<ProfileDocument> => ({
        id,
        text: JSON.stringify(files[id]),
        revision: "r1",
        diagnostics: options.diagnostics ?? [],
      }),
    ),
    resolveProfile: vi.fn(async (id: string) => resolution(id)),
    listProfiles: vi.fn(async (): Promise<ProfileEntry[]> => []),
    getScriptDescription: vi.fn(async () => ({
      metadata: { runtime: 0, actions: [] },
    })),
    getScriptRegistrations: vi.fn(async () => []),
    listMacros: vi.fn(async () => ({ macros: [], missing: [] })),
    validateProfile: vi.fn(async () => ({ valid: true, diagnostics: [] })),
    putProfile: vi.fn(async () => ({ revision: "r2", applied: true })),
  };
  const store = createEditorStore(api as unknown as PadmateAPI);
  return { api, store, files, resolution };
}
/** Lets the promises the store started run. */
const settled = () => new Promise((resolve) => setTimeout(resolve, 0));
const draft = (store: EditorStore) => {
  const value = store.getState().draft;
  if (!value) throw new Error("Expected loaded draft");
  return value;
};

describe("the parent of an edited profile", () => {
  it("is read when the profile is loaded, merged and with who wrote what", async () => {
    const { api, store } = setup();
    api.resolveProfile.mockImplementation(async (id: string) => ({
      id,
      text: JSON.stringify(parentProfile),
      revision: "r1",
      inherited: [{ pointer: "/layers/base/south", profile: "root" }],
      diagnostics: [],
    }));
    await store.getState().activate(context);
    await settled();
    expect(api.resolveProfile).toHaveBeenCalledExactlyOnceWith("paint");
    expect(store.getState().parent).toEqual({
      id: "paint",
      profile: parentProfile,
      origins: [{ pointer: "/layers/base/south", profile: "root" }],
    });
    // The draft is still the profile's own file.
    expect(draft(store)).toEqual(child);
  });

  it("is none for a profile that is based on nothing, and nothing is asked", async () => {
    const { api, store } = setup({ child: parentProfile });
    await store.getState().activate(context);
    await settled();
    expect(api.resolveProfile).not.toHaveBeenCalled();
    expect(store.getState().parent).toBeNull();
  });

  it("is none when it cannot be read, and the profile is still edited", async () => {
    const { api, store } = setup();
    api.resolveProfile.mockRejectedValue({ message: "profile not found" });
    await store.getState().activate(context);
    await settled();
    expect(store.getState().parent).toBeNull();
    expect(store.getState().error).toBe("");
    store.getState().edit({ ...draft(store), name: "Renamed" });
    expect(await store.getState().flush()).toBe(true);
  });

  it("is none when its text is not a profile", async () => {
    const { api, store } = setup();
    api.resolveProfile.mockResolvedValue({
      id: "paint",
      text: "{}",
      revision: "r1",
      inherited: [],
      diagnostics: [],
    });
    await store.getState().activate(context);
    await settled();
    expect(store.getState().parent).toBeNull();
  });

  it("is read again when profiles change, whichever file it was", async () => {
    const { api, store, files } = setup();
    await store.getState().activate(context);
    await settled();
    files.paint = {
      ...parentProfile,
      layers: {
        base: { south: { tap: { type: "keys", keys: "Ctrl+Shift+Z" } } },
      },
    };
    await store.getState().profileChanged();
    await settled();
    expect(api.resolveProfile).toHaveBeenCalledTimes(2);
    expect(store.getState().parent?.profile.layers.base.south?.tap).toEqual({
      type: "keys",
      keys: "Ctrl+Shift+Z",
    });
  });

  it("follows the extends of the draft: another parent, or none", async () => {
    const { api, store, files } = setup();
    files.other = { ...parentProfile, name: "Other" };
    await store.getState().activate(context);
    await settled();
    expect(store.getState().parent?.id).toBe("paint");
    store.getState().edit({ ...draft(store), extends: "other" });
    await settled();
    expect(api.resolveProfile).toHaveBeenLastCalledWith("other");
    expect(store.getState().parent?.id).toBe("other");
    // Based on none: the parent goes at once, and nothing is asked.
    const asked = api.resolveProfile.mock.calls.length;
    const { extends: _extends, ...alone } = draft(store);
    store.getState().edit(alone as Profile);
    expect(store.getState().parent).toBeNull();
    await settled();
    expect(api.resolveProfile).toHaveBeenCalledTimes(asked);
    // And back: undo reads the parent again.
    expect(store.getState().undo()).toBe(true);
    await settled();
    expect(store.getState().parent?.id).toBe("other");
  });

  it("does not read the parent again for an edit that leaves extends alone", async () => {
    const { api, store } = setup();
    await store.getState().activate(context);
    await settled();
    store.getState().edit({ ...draft(store), name: "Renamed" });
    await store.getState().flush();
    await settled();
    expect(api.resolveProfile).toHaveBeenCalledTimes(1);
  });

  it("ignores an answer that comes after a newer question", async () => {
    const { api, store, files, resolution } = setup();
    files.other = { ...parentProfile, name: "Other" };
    let release: (value: ProfileResolution) => void = () => {};
    api.resolveProfile
      .mockImplementationOnce(
        () =>
          new Promise<ProfileResolution>((resolve) => {
            release = resolve;
          }),
      )
      .mockImplementation(async (id: string) => resolution(id));
    await store.getState().activate(context);
    // The first question is open; the draft is based on another profile now.
    store.getState().edit({ ...draft(store), extends: "other" });
    await settled();
    expect(store.getState().parent?.id).toBe("other");
    release(resolution("paint"));
    await settled();
    expect(store.getState().parent?.id).toBe("other");
  });

  it("goes when another profile is opened", async () => {
    const { store, files } = setup();
    files.alone = parentProfile;
    await store.getState().activate(context);
    await settled();
    expect(store.getState().parent).not.toBeNull();
    await store
      .getState()
      .activate({ id: "alone", device: "d", connection: "c" });
    await settled();
    expect(store.getState().parent).toBeNull();
  });

  it("is read again with the profile, when the file is read from disk again", async () => {
    const { api, store } = setup();
    await store.getState().activate(context);
    await settled();
    await store.getState().reload();
    await settled();
    expect(api.resolveProfile).toHaveBeenCalledTimes(2);
  });
});

describe("the warning about a key the profile empties", () => {
  const warning: ProfileDiagnostic = {
    severity: "warning",
    pointer: "/layers/base/south",
    offset: 0,
    line: 1,
    col: 1,
    message: "binding has no action",
  };
  const emptied: Profile = {
    ...child,
    layers: { base: { south: {}, east: child.layers.base.east } },
  };
  it("is not shown while the parent binds that key", async () => {
    const { store } = setup(
      { child: emptied, paint: parentProfile },
      { diagnostics: [warning] },
    );
    await store.getState().activate(context);
    await settled();
    expect(store.getState().diagnostics).toEqual([]);
  });
  it("is shown when the parent does not bind it", async () => {
    const { store } = setup(
      {
        child: emptied,
        paint: { ...parentProfile, layers: { base: {} } },
      },
      { diagnostics: [warning] },
    );
    await store.getState().activate(context);
    await settled();
    expect(store.getState().diagnostics).toEqual([warning]);
  });
});

describe("a key of the parent that the draft empties", () => {
  const emptied = (store: EditorStore): Profile => ({
    ...draft(store),
    layers: {
      base: {
        ...draft(store).layers.base,
        south: { tap: { type: "keys", keys: "" } },
      },
    },
  });
  it("is saved as an empty binding, so the parent's binding does not come back", async () => {
    const { api, store } = setup();
    await store.getState().activate(context);
    await settled();
    // The drawer's 清除快捷键 on south, which only the parent binds.
    store.getState().edit(emptied(store));
    expect(store.getState().unsaved).toBe(true);
    expect(await store.getState().flush()).toBe(true);
    const text = (api.putProfile.mock.calls.at(-1) as unknown[])[1] as string;
    expect((JSON.parse(text) as Profile).layers.base).toEqual({
      east: { tap: { type: "keys", keys: "E" } },
      south: {},
    });
    expect(store.getState().unsaved).toBe(false);
  });
  it("goes from the file altogether for a profile that is based on nothing", async () => {
    const { api, store } = setup({ child: parentProfile });
    await store.getState().activate(context);
    await settled();
    // Nothing is inherited there, so the key goes from the file altogether.
    store.getState().edit({
      ...draft(store),
      layers: { base: { south: { tap: { type: "keys", keys: "" } } } },
    });
    expect(store.getState().unsaved).toBe(true);
    await store.getState().flush();
    const text = (api.putProfile.mock.calls.at(-1) as unknown[])[1] as string;
    expect((JSON.parse(text) as Profile).layers.base).toEqual({});
  });
});

describe("the profile list the editor holds", () => {
  it("is read again when profiles change, though the open profile did not", async () => {
    const { api, store } = setup();
    api.listProfiles.mockResolvedValue([
      { id: "child", name: "Child", extends: "paint" },
      { id: "paint", name: "Paint" },
    ]);
    await store.getState().activate(context);
    await settled();
    expect(store.getState().profiles.map((entry) => entry.id)).toEqual([
      "child",
      "paint",
    ]);
    // Another profile is copied, imported or renamed: the open file is as it was.
    api.listProfiles.mockResolvedValue([
      { id: "child", name: "Child", extends: "paint" },
      { id: "paint", name: "Paint (renamed)" },
      { id: "copy", name: "Copy" },
    ]);
    await store.getState().profileChanged();
    await settled();
    expect(store.getState().profiles.map((entry) => entry.name)).toEqual([
      "Child",
      "Paint (renamed)",
      "Copy",
    ]);
    // The open profile was not read again: its revision is the same.
    expect(store.getState().unsaved).toBe(false);
  });
  it("keeps the list it has when it cannot be read again", async () => {
    const { api, store } = setup();
    api.listProfiles.mockResolvedValue([{ id: "paint", name: "Paint" }]);
    await store.getState().activate(context);
    await settled();
    api.listProfiles.mockRejectedValue({ message: "gone" });
    await store.getState().profileChanged();
    await settled();
    expect(store.getState().profiles.map((entry) => entry.id)).toEqual([
      "paint",
    ]);
  });
});
