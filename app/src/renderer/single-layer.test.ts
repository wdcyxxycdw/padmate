import { describe, expect, it, vi } from "vitest";
import type { Profile } from "../shared/generated/schema";
import { editorAPIFixture } from "./editor-api-fixture";
import {
  baseBinding,
  layerUsage,
  registrationsFor,
  setBaseBinding,
  usesOnlyBase,
} from "./editor-model";
import { createEditorStore } from "./editor-store";

const layered: Profile = {
  schemaVersion: 1,
  name: "Layers",
  defaultLayer: "base",
  common: {
    rightstick: {
      press: { type: "layer", op: "cycle", layers: ["base", "alt"] },
    },
  },
  layers: {
    base: {
      south: { label: "A", press: { type: "keys", keys: "A" } },
      leftshoulder: {
        tap: { type: "keys", keys: "F13" },
        hold: { type: "layer", op: "hold", layer: "alt" },
      },
    },
    alt: {
      color: "#3366FF",
      south: { press: { type: "keys", keys: "B" } },
      west: { block: true },
    },
  },
};

const switching: Profile = {
  schemaVersion: 1,
  name: "Switching",
  layers: {
    base: {
      leftshoulder: {
        tap: { type: "keys", keys: "F13" },
        hold: { type: "profile", profile: "photo" },
      },
      rightshoulder: { press: { type: "profile", profile: "sketch" } },
      north: { press: { type: "keys", keys: "F14" } },
    },
  },
};

describe("layer usage", () => {
  it("reports what the GUI does not show", () => {
    const usage = layerUsage(layered, "start");
    expect(usage).toEqual({
      extraLayers: ["alt"],
      commonBindings: 1,
      defaultLayer: null,
      layerActions: 2,
      blocks: 1,
      switching: true,
      profileActions: 0,
    });
    expect(usesOnlyBase(usage)).toBe(false);
    expect(
      usesOnlyBase(
        layerUsage(
          { schemaVersion: 1, name: "x", layers: { base: {} } },
          "start",
        ),
      ),
    ).toBe(true);
  });

  it("counts profile switches without making the file multi-layer", () => {
    const usage = layerUsage(switching, "start");
    expect(usage.profileActions).toBe(2);
    expect(usage.layerActions).toBe(0);
    // A single-layer file keeps its single-layer cheat sheet.
    expect(usesOnlyBase(usage)).toBe(true);
    expect(
      layerUsage(
        {
          ...layered,
          layers: {
            ...layered.layers,
            alt: { south: { press: { type: "profile", profile: "photo" } } },
          },
        },
        "start",
      ).profileActions,
    ).toBe(1);
  });

  it("keeps profile switches exactly when other bindings change", () => {
    // An unrelated key.
    let next = setBaseBinding(switching, "north", {
      press: { type: "keys", keys: "F16" },
    });
    // Another gesture on the key that also switches profiles.
    next = setBaseBinding(next, "leftshoulder", {
      ...baseBinding(next, "leftshoulder"),
      tap: { type: "keys", keys: "F15" },
    });
    expect(JSON.stringify(next.layers.base.rightshoulder)).toBe(
      JSON.stringify(switching.layers.base.rightshoulder),
    );
    expect(JSON.stringify(next.layers.base.leftshoulder?.hold)).toBe(
      JSON.stringify(switching.layers.base.leftshoulder?.hold),
    );
    expect(Object.keys(next.layers.base)).toEqual(
      Object.keys(switching.layers.base),
    );
  });

  it("editing base keeps every other part of the file", () => {
    const next = setBaseBinding(layered, "south", {
      press: { type: "keys", keys: "Ctrl+Z" },
    });
    expect(next.layers.alt).toEqual(layered.layers.alt);
    expect(next.common).toEqual(layered.common);
    expect(next.defaultLayer).toBe("base");
    expect(next.layers.base.leftshoulder).toEqual(
      layered.layers.base.leftshoulder,
    );
    expect(layered.layers.base.south?.press).toEqual({
      type: "keys",
      keys: "A",
    });
  });

  it("base, layer:base and global script registrations own a control", () => {
    const registrations = [
      { id: 1, control: "a", scope: "base", observe: false },
      { id: 2, control: "south", scope: "layer:alt", observe: false },
      { id: 3, control: "south", scope: "global", observe: true },
      // padmate.layer("base").on(...) names the edited layer explicitly.
      { id: 4, control: "south", scope: "layer:base", observe: false },
    ];
    const result = registrationsFor(registrations, "south");
    expect(result.owners.map((r) => r.id)).toEqual([1, 4]);
    expect(result.visible.map((r) => r.id)).toEqual([1, 3, 4]);
    expect(result.otherScopes.map((r) => r.id)).toEqual([2]);
  });

  it("keeps a binding in its place when it is edited", () => {
    const next = setBaseBinding(layered, "south", {
      label: "撤",
      press: { type: "keys", keys: "A" },
    });
    expect(Object.keys(next.layers.base)).toEqual(["south", "leftshoulder"]);
    // An alias edits the same entry under its canonical name, in place.
    const aliased = setBaseBinding(layered, "a", { label: "撤" });
    expect(Object.keys(aliased.layers.base)).toEqual(["south", "leftshoulder"]);
    // Removing drops only that entry; adding goes at the end.
    expect(
      Object.keys(setBaseBinding(layered, "south", undefined).layers.base),
    ).toEqual(["leftshoulder"]);
    expect(
      Object.keys(
        setBaseBinding(layered, "north", { label: "新" }).layers.base,
      ),
    ).toEqual(["south", "leftshoulder", "north"]);
  });
});

async function storeWith(profile: Profile) {
  const api = editorAPIFixture();
  api.getProfile.mockImplementation(async (id: string) => ({
    id,
    text: JSON.stringify(profile),
    revision: "r1",
    diagnostics: [],
  }));
  const store = createEditorStore(api as never);
  await store
    .getState()
    .activate({ id: "paint", device: "d", connection: "c" });
  await vi.waitFor(() => expect(store.getState().draft).not.toBeNull());
  return { api, store };
}

describe("editor store", () => {
  it("makes a profile read-only when it starts in another layer", async () => {
    const { api, store } = await storeWith({
      ...layered,
      defaultLayer: "alt",
    });
    expect(store.getState().readOnly).toBe("defaultLayer");
    const draft = store.getState().draft as Profile;
    store.getState().edit({ ...draft, name: "changed" });
    await store.getState().flush();
    expect(store.getState().unsaved).toBe(false);
    expect(store.getState().draft?.name).toBe("Layers");
    expect(api.putProfile).not.toHaveBeenCalled();
  });

  it("saves a label edit and its undo without reordering the file", async () => {
    const { api, store } = await storeWith(layered);
    const loaded = store.getState().draft as Profile;
    // Type a character into the label of a key that is not last.
    const south = loaded.layers.base.south ?? {};
    store
      .getState()
      .edit(setBaseBinding(loaded, "south", { ...south, label: "A撤" }));
    expect(store.getState().unsaved).toBe(true);
    store.getState().undo();
    expect(store.getState().draft).toEqual(layered);
    await store.getState().flush();
    expect(store.getState().unsaved).toBe(false);
    // Each save keeps the key order, so the file is not reordered.
    for (const [, text] of api.putProfile.mock.calls)
      expect(Object.keys(JSON.parse(text).layers.base)).toEqual([
        "south",
        "leftshoulder",
      ]);
  });

  it("takes an edit whose keys only moved for no change", async () => {
    const { api, store } = await storeWith(layered);
    const moved: Profile = {
      ...layered,
      layers: {
        ...layered.layers,
        base: {
          leftshoulder: layered.layers.base.leftshoulder,
          south: { press: { type: "keys", keys: "A" }, label: "A" },
        },
      },
    };
    store.getState().edit(moved);
    await store.getState().flush();
    expect(api.putProfile).not.toHaveBeenCalled();
    expect(store.getState().canUndo).toBe(false);
  });

  it("tracks layer usage on load and edit", async () => {
    const { store } = await storeWith(layered);
    expect(store.getState().readOnly).toBe("");
    expect(store.getState().usage?.layerActions).toBe(2);
    const draft = store.getState().draft as Profile;
    store.getState().edit(
      setBaseBinding(draft, "leftshoulder", {
        press: { type: "keys", keys: "LCtrl" },
      }),
    );
    expect(store.getState().usage?.layerActions).toBe(1);
    expect(store.getState().unsaved).toBe(true);
    store.getState().undo();
    expect(store.getState().usage?.layerActions).toBe(2);
  });
});
