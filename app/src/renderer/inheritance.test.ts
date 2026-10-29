import { describe, expect, it } from "vitest";
import type { Profile } from "../shared/generated/schema";
import { savedForm, setBaseBinding, swapView } from "./editor-model";
import {
  isClearedInherited,
  type ParentProfile,
  parentOf,
  withInherited,
  writeBack,
} from "./inheritance";

const parentProfile: Profile = {
  schemaVersion: 1,
  name: "Paint",
  controlNames: { south: "Undo" },
  timing: { holdMs: 300 },
  analog: { ls: { mode: "mouse", deadzone: 0.1 } },
  layers: {
    base: {
      color: "#fff",
      south: { tap: { type: "keys", keys: "Ctrl+Z" } },
      // An alias of the control east.
      b: { tap: { type: "keys", keys: "Ctrl+Y" } },
      back: { tap: { type: "keys", keys: "F20" } },
    },
  },
  // The old format, which the parent's own file may still be in: it stands
  // for the one function set of ADR-0075.
  functionKeys: { back: { south: { tap: { type: "keys", keys: "F1" } } } },
};

const parent: ParentProfile = {
  id: "paint",
  profile: parentProfile,
};

const child: Profile = {
  schemaVersion: 1,
  name: "Child",
  extends: "paint",
  timing: { multiTapMs: 200 },
  layers: {
    base: {
      east: { tap: { type: "keys", keys: "E" } },
      north: { tap: { type: "keys", keys: "N" } },
    },
  },
};

const keysOf = (layer: object | undefined) => Object.keys(layer ?? {});

describe("the parent held for a profile", () => {
  it("is the one its extends names", () => {
    expect(parentOf(child, parent)).toBe(parent);
    expect(parentOf(child, { ...parent, id: "other" })).toBeNull();
    expect(parentOf(child, null)).toBeNull();
    expect(parentOf(child, undefined)).toBeNull();
    const { extends: _extends, ...alone } = child;
    expect(parentOf(alone as Profile, parent)).toBeNull();
  });
});

describe("withInherited", () => {
  it("adds the controls of the parent that the profile does not write", () => {
    const shown = withInherited(child, parent);
    // Its own bindings first, in order; then what the parent adds.
    expect(keysOf(shown.layers.base)).toEqual([
      "east",
      "north",
      "south",
      "back",
    ]);
    expect(shown.layers.base.south).toEqual(parentProfile.layers.base.south);
  });
  it("lets a control the profile writes replace the parent's, under any alias", () => {
    const shown = withInherited(child, parent);
    // The parent writes east as b; the profile's east replaces it whole.
    expect(shown.layers.base.east).toEqual({
      tap: { type: "keys", keys: "E" },
    });
    expect(keysOf(shown.layers.base)).not.toContain("b");
  });
  it("leaves the layer's color out, and adds back, which is an ordinary control", () => {
    const shown = withInherited(child, parent);
    expect(shown.layers.base).not.toHaveProperty("color");
    expect(shown.layers.base.back).toEqual({
      tap: { type: "keys", keys: "F20" },
    });
  });
  it("adds the function set, whichever format the parent writes it in", () => {
    const shown = withInherited(child, parent);
    expect(shown.functionSet).toEqual({
      south: { tap: { type: "keys", keys: "F1" } },
    });
    expect(shown).not.toHaveProperty("functionKeys");
    const written = withInherited(child, {
      ...parent,
      profile: {
        ...parentProfile,
        functionKeys: undefined,
        functionSet: { south: { tap: { type: "keys", keys: "F1" } } },
      },
    });
    expect(written.functionSet).toEqual(shown.functionSet);
    const own = withInherited(
      {
        ...child,
        functionSet: { north: { tap: { type: "keys", keys: "N" } } },
      },
      parent,
    );
    expect(own.functionSet).toEqual({
      north: { tap: { type: "keys", keys: "N" } },
      south: { tap: { type: "keys", keys: "F1" } },
    });
  });
  it("takes a profile of the old functionKeys as the set it stands for, as the parent is", () => {
    const own = withInherited(
      {
        ...child,
        functionKeys: {
          back: { north: { tap: { type: "keys", keys: "N" } } },
          start: { west: { tap: { type: "keys", keys: "W" } } },
        },
      },
      parent,
    );
    // Menu's set first, View's controls after it, then the parent's.
    expect(Object.keys(own.functionSet ?? {})).toEqual([
      "west",
      "north",
      "south",
    ]);
    expect(own).not.toHaveProperty("functionKeys");
  });
  it("lets a control of the set that the profile writes replace the parent's", () => {
    const own = withInherited(
      {
        ...child,
        functionSet: { a: { tap: { type: "keys", keys: "S" } } },
      },
      parent,
    );
    // a is south under its alias.
    expect(own.functionSet).toEqual({
      a: { tap: { type: "keys", keys: "S" } },
    });
  });
  it("merges the key names and the timing member by member", () => {
    const shown = withInherited(child, parent);
    expect(shown.controlNames).toEqual({ south: "Undo" });
    expect(shown.timing).toEqual({ holdMs: 300, multiTapMs: 200 });
    const over = withInherited(
      { ...child, timing: { holdMs: 100 }, controlNames: { south: "Back" } },
      parent,
    );
    expect(over.timing).toEqual({ holdMs: 100 });
    expect(over.controlNames).toEqual({ south: "Back" });
  });
  it("merges the settings of a stick one by one", () => {
    const shown = withInherited(
      { ...child, analog: { ls: { deadzone: 0.2 } } },
      parent,
    );
    expect(shown.analog).toEqual({ ls: { mode: "mouse", deadzone: 0.2 } });
    expect(withInherited(child, parent).analog).toEqual({
      ls: { mode: "mouse", deadzone: 0.1 },
    });
  });
  it("adds the shared (common) controls of the parent that the profile does not write", () => {
    const up: ParentProfile = {
      ...parent,
      profile: {
        ...parentProfile,
        common: {
          south: { tap: { type: "keys", keys: "Ctrl+Z" } },
          west: { tap: { type: "keys", keys: "W" } },
        },
      },
    };
    const own: Profile = {
      ...child,
      common: { west: { tap: { type: "keys", keys: "X" } } },
    };
    // The daemon merges common control by control, like the layers.
    const shown = withInherited(own, up);
    expect(shown.common).toEqual({
      west: { tap: { type: "keys", keys: "X" } },
      south: { tap: { type: "keys", keys: "Ctrl+Z" } },
    });
    expect(withInherited(child, up).common).toEqual(up.profile.common);
    // A parent without common adds none.
    expect(withInherited(child, parent)).not.toHaveProperty("common");
  });
  it("is the profile itself when there is no parent, or it adds nothing", () => {
    expect(withInherited(child, null)).toBe(child);
    expect(withInherited(child, { ...parent, id: "other" })).toBe(child);
    const bare: ParentProfile = {
      id: "paint",
      profile: { schemaVersion: 1, name: "P", layers: { base: {} } },
    };
    expect(withInherited(child, bare)).toBe(child);
  });
  it("does not change the profile or the parent", () => {
    const before = structuredClone([child, parentProfile]);
    withInherited(child, parent);
    expect([child, parentProfile]).toEqual(before);
  });
});

describe("writeBack", () => {
  const shown = () => withInherited(child, parent);
  const south = { tap: { type: "keys", keys: "Ctrl+Shift+Z" } } as const;

  it("is the edited profile itself when no parent is held", () => {
    const next = { ...child, name: "x" };
    expect(writeBack(child, null, next)).toBe(next);
    expect(writeBack(child, { ...parent, id: "other" }, next)).toBe(next);
  });
  it("writes an inherited control, edited, into the profile as its own", () => {
    const edited = setBaseBinding(shown(), "south", south);
    const result = writeBack(child, parent, edited);
    expect(result.layers.base).toEqual({
      east: { tap: { type: "keys", keys: "E" } },
      north: { tap: { type: "keys", keys: "N" } },
      south,
    });
    // Nothing else of the parent's went in.
    expect(result.controlNames).toBeUndefined();
    expect(result.timing).toEqual({ multiTapMs: 200 });
    expect(result.analog).toBeUndefined();
    expect(result.functionSet).toBeUndefined();
  });
  it("changes only the control edited when it is the profile's own", () => {
    const edited = setBaseBinding(shown(), "north", {
      tap: { type: "keys", keys: "M" },
    });
    const result = writeBack(child, parent, edited);
    expect(result.layers.base).toEqual({
      east: { tap: { type: "keys", keys: "E" } },
      north: { tap: { type: "keys", keys: "M" } },
    });
  });
  it("keeps the file as it was for an edit of something else", () => {
    const result = writeBack(child, parent, { ...shown(), name: "Renamed" });
    expect(result).toEqual({ ...child, name: "Renamed" });
    expect(Object.keys(result)).toEqual(Object.keys(child));
  });
  it("writes the control under its canonical name, in place when it is there", () => {
    const aliased: Profile = {
      ...child,
      layers: { base: { a: { tap: { type: "keys", keys: "A" } } } },
    };
    const edited = setBaseBinding(withInherited(aliased, parent), "south", {
      tap: { type: "keys", keys: "S" },
    });
    const result = writeBack(aliased, parent, edited);
    // The profile wrote it as a (the alias of south): same place, same name.
    expect(result.layers.base).toEqual({
      a: { tap: { type: "keys", keys: "S" } },
    });
    // An inherited control comes in under the canonical name.
    const added = writeBack(
      child,
      parent,
      setBaseBinding(shown(), "b", { tap: { type: "keys", keys: "B" } }),
    );
    expect(keysOf(added.layers.base)).toEqual(["east", "north"]);
  });
  it("keeps a control emptied while the parent binds it as an empty binding", () => {
    // south is the parent's alone; east both write.
    const cleared = writeBack(
      child,
      parent,
      setBaseBinding(setBaseBinding(shown(), "south", undefined), "east", {}),
    );
    expect(cleared.layers.base).toEqual({
      east: {},
      north: { tap: { type: "keys", keys: "N" } },
      south: {},
    });
  });
  it("drops a control emptied that the parent does not bind", () => {
    const result = writeBack(
      child,
      parent,
      setBaseBinding(shown(), "north", undefined),
    );
    expect(result.layers.base).toEqual({
      east: { tap: { type: "keys", keys: "E" } },
    });
  });
  it("writes the function set the same way", () => {
    const swapped = swapView(shown(), "function");
    const edited = setBaseBinding(swapped, "south", {
      tap: { type: "keys", keys: "F2" },
    });
    const result = writeBack(child, parent, swapView(edited, "function"));
    expect(result.functionSet).toEqual({
      south: { tap: { type: "keys", keys: "F2" } },
    });
    expect(result).not.toHaveProperty("functionKeys");
    expect(result.layers.base).toEqual(child.layers.base);
  });
  it("leaves the set a profile of the old format has to be saved as functionSet", () => {
    const old: Profile = {
      ...child,
      functionKeys: { start: { east: { tap: { type: "keys", keys: "Z" } } } },
    };
    const swapped = swapView(withInherited(old, parent), "function");
    const edited = setBaseBinding(swapped, "south", {
      tap: { type: "keys", keys: "F2" },
    });
    const result = writeBack(old, parent, swapView(edited, "function"));
    expect(result.functionSet).toEqual({
      east: { tap: { type: "keys", keys: "Z" } },
      south: { tap: { type: "keys", keys: "F2" } },
    });
    expect(result).not.toHaveProperty("functionKeys");
  });
  it("writes only the key names and timing the edit changed", () => {
    const edited: Profile = {
      ...shown(),
      controlNames: { south: "Undo", east: "Redo" },
      timing: { holdMs: 300, multiTapMs: 200, tapGapMs: 5 },
    };
    const result = writeBack(child, parent, edited);
    expect(result.controlNames).toEqual({ east: "Redo" });
    expect(result.timing).toEqual({ multiTapMs: 200, tapGapMs: 5 });
  });
  it("lets a member go that the edit cleared", () => {
    const edited: Profile = { ...shown(), timing: { holdMs: 300 } };
    const result = writeBack(child, parent, edited);
    // multiTapMs was the profile's own; it went. holdMs is the parent's.
    expect(result.timing).toBeUndefined();
    expect(result).not.toHaveProperty("timing");
  });
  it("writes only the setting of a stick that the edit changed", () => {
    const edited: Profile = {
      ...shown(),
      analog: { ls: { mode: "mouse", deadzone: 0.15 } },
    };
    const result = writeBack(child, parent, edited);
    expect(result.analog).toEqual({ ls: { deadzone: 0.15 } });
  });
  it("keeps what the profile has in the order the file has it", () => {
    const withSet: Profile = {
      ...child,
      functionSet: { east: { tap: { type: "keys", keys: "Z" } } },
      controlNames: { east: "E" },
    };
    const edited = setBaseBinding(
      withInherited(withSet, parent),
      "south",
      south,
    );
    const result = writeBack(withSet, parent, edited);
    expect(Object.keys(result)).toEqual(Object.keys(withSet));
  });
  it("does not change the profile it was given", () => {
    const before = structuredClone(child);
    writeBack(child, parent, setBaseBinding(shown(), "south", south));
    expect(child).toEqual(before);
  });
});

describe("writeBack of the shared (common) controls", () => {
  const up: ParentProfile = {
    ...parent,
    profile: {
      ...parentProfile,
      common: { west: { tap: { type: "keys", keys: "W" } } },
    },
  };
  it("does not copy the parent's common into the profile for an edit of something else", () => {
    const shown = withInherited(child, up);
    const result = writeBack(
      child,
      up,
      setBaseBinding(shown, "north", { tap: { type: "keys", keys: "M" } }),
    );
    expect(result).not.toHaveProperty("common");
  });
});

describe("savedForm of a profile with a parent", () => {
  const emptied = (layers: Profile["layers"], rest: Partial<Profile> = {}) =>
    ({ ...child, layers, ...rest }) as Profile;
  const empty = { tap: { type: "keys" as const, keys: "" } };
  it("keeps a key emptied while the parent binds it as an empty binding, so the parent's does not come back", () => {
    // The drawer's 清除快捷键 left south with a shortcut not filled in.
    const draft = emptied({ base: { south: empty } });
    const saved = savedForm(draft, parent);
    expect(saved.layers.base).toEqual({ south: {} });
    // The file says south does nothing here; the parent's is not inherited.
    expect(withInherited(saved, parent).layers.base.south).toEqual({});
  });
  it("drops a key emptied that the parent does not bind, as it does without a parent", () => {
    const draft = emptied({ base: { west: empty } });
    expect(savedForm(draft, parent).layers.base).toEqual({});
    expect(savedForm(draft).layers.base).toEqual({});
  });
  it("drops it when no parent is held, or the one held is not the profile's", () => {
    const draft = emptied({ base: { south: empty } });
    expect(savedForm(draft).layers.base).toEqual({});
    expect(savedForm(draft, null).layers.base).toEqual({});
    expect(savedForm(draft, { ...parent, id: "other" }).layers.base).toEqual(
      {},
    );
  });
  it("finds the parent's binding under another name of the control", () => {
    // The parent writes east as b.
    const draft = emptied({ base: { east: empty } });
    expect(savedForm(draft, parent).layers.base).toEqual({ east: {} });
  });
  it("leaves the gestures that are filled in, and the labels, as they are", () => {
    const draft = emptied({
      base: {
        south: {
          label: "撤销",
          tap: { type: "keys", keys: "" },
          hold: { type: "keys", keys: "F2" },
        },
      },
    });
    expect(savedForm(draft, parent).layers.base).toEqual({
      south: { label: "撤销", hold: { type: "keys", keys: "F2" } },
    });
  });
  it("does the same in the function set", () => {
    const draft = emptied({ base: {} }, { functionSet: { south: empty } });
    expect(savedForm(draft, parent).functionSet).toEqual({ south: {} });
    // The parent binds nothing there: the set goes.
    const other = emptied({ base: {} }, { functionSet: { west: empty } });
    expect(savedForm(other, parent)).not.toHaveProperty("functionSet");
  });
  it("does the same for back, start and guide, ordinary controls the parent may bind", () => {
    const draft = emptied({ base: { back: empty } });
    expect(savedForm(draft, parent).layers.base).toEqual({ back: {} });
  });
  it("does the same for the shared (common) controls", () => {
    const up: ParentProfile = {
      ...parent,
      profile: {
        ...parentProfile,
        common: { west: { tap: { type: "keys", keys: "W" } } },
      },
    };
    const draft = emptied({ base: {} }, { common: { west: empty } });
    expect(savedForm(draft, up).common).toEqual({ west: {} });
    expect(savedForm(draft, parent).common).toEqual({});
  });
  it("is the draft itself when nothing is left out", () => {
    expect(savedForm(child, parent)).toBe(child);
  });
});

describe("isClearedInherited", () => {
  const warning = (pointer: string) => ({
    severity: "warning",
    pointer,
    message: "binding has no action",
  });
  const cleared: Profile = { ...child, layers: { base: { south: {} } } };
  it("is the warning about a key left empty while the parent binds it", () => {
    expect(
      isClearedInherited(warning("/layers/base/south"), cleared, parent),
    ).toBe(true);
    // Under another name of the control.
    const aliased: Profile = { ...child, layers: { base: { a: {} } } };
    expect(isClearedInherited(warning("/layers/base/a"), aliased, parent)).toBe(
      true,
    );
  });
  it("is not the warning about a key the parent does not bind", () => {
    const north: Profile = { ...child, layers: { base: { north: {} } } };
    expect(
      isClearedInherited(warning("/layers/base/north"), north, parent),
    ).toBe(false);
  });
  it("is not the warning about a key the profile fills in, or does not write", () => {
    expect(
      isClearedInherited(warning("/layers/base/east"), child, parent),
    ).toBe(false);
    expect(
      isClearedInherited(warning("/layers/base/south"), child, parent),
    ).toBe(false);
  });
  it("reads the function set in its view", () => {
    const set: Profile = { ...child, functionSet: { south: {} } };
    expect(isClearedInherited(warning("/functionSet/south"), set, parent)).toBe(
      true,
    );
  });
  it("is not any other diagnostic, nor without a parent for the profile", () => {
    expect(
      isClearedInherited(
        { ...warning("/layers/base/south"), severity: "error" },
        cleared,
        parent,
      ),
    ).toBe(false);
    expect(
      isClearedInherited(
        { ...warning("/layers/base/south"), message: "something else" },
        cleared,
        parent,
      ),
    ).toBe(false);
    expect(
      isClearedInherited(warning("/layers/base/south"), cleared, null),
    ).toBe(false);
    expect(
      isClearedInherited(warning("/layers/base/south"), cleared, {
        ...parent,
        id: "other",
      }),
    ).toBe(false);
    expect(
      isClearedInherited(warning("/layers/base/south"), null, parent),
    ).toBe(false);
  });
});
