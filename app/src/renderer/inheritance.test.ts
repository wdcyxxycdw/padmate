import { describe, expect, it } from "vitest";
import type { Profile } from "../shared/generated/schema";
import { savedForm, setBaseBinding, swapView } from "./editor-model";
import {
  keyInheritance,
  originOf,
  type ParentProfile,
  parentOf,
  restoreInherited,
  withExtends,
  withInherited,
  writeBack,
  writesControl,
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
  functionKeys: { back: { south: { tap: { type: "keys", keys: "F1" } } } },
};

const parent: ParentProfile = {
  id: "paint",
  profile: parentProfile,
  origins: [],
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
    expect(keysOf(shown.layers.base)).toEqual(["east", "north", "south"]);
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
  it("leaves the layer's color and the function keys' old bindings out", () => {
    const shown = withInherited(child, parent);
    expect(shown.layers.base).not.toHaveProperty("color");
    expect(shown.layers.base).not.toHaveProperty("back");
  });
  it("adds the sets of the function keys", () => {
    const shown = withInherited(child, parent);
    expect(shown.functionKeys).toEqual({
      back: { south: { tap: { type: "keys", keys: "F1" } } },
    });
    const own = withInherited(
      {
        ...child,
        functionKeys: { back: { north: { tap: { type: "keys", keys: "N" } } } },
      },
      parent,
    );
    expect(own.functionKeys?.back).toEqual({
      north: { tap: { type: "keys", keys: "N" } },
      south: { tap: { type: "keys", keys: "F1" } },
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
      origins: [],
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
    expect(result.functionKeys).toBeUndefined();
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
  it("writes a function key's set the same way", () => {
    const swapped = swapView(shown(), "back");
    const edited = setBaseBinding(swapped, "south", {
      tap: { type: "keys", keys: "F2" },
    });
    const result = writeBack(child, parent, swapView(edited, "back"));
    expect(result.functionKeys).toEqual({
      back: { south: { tap: { type: "keys", keys: "F2" } } },
    });
    expect(result.layers.base).toEqual(child.layers.base);
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
    const withFunctionKeys: Profile = {
      ...child,
      functionKeys: { start: { east: { tap: { type: "keys", keys: "Z" } } } },
      controlNames: { east: "E" },
    };
    const edited = setBaseBinding(
      withInherited(withFunctionKeys, parent),
      "south",
      south,
    );
    const result = writeBack(withFunctionKeys, parent, edited);
    expect(Object.keys(result)).toEqual(Object.keys(withFunctionKeys));
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
    const found = keyInheritance(saved, parent, "normal", ["south"]);
    expect(found.inherited).toEqual([]);
    expect(found.overridden).toEqual(["south"]);
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
  it("does the same in the set of a function key", () => {
    const draft = emptied(
      { base: {} },
      { functionKeys: { back: { south: empty } } },
    );
    expect(savedForm(draft, parent).functionKeys).toEqual({
      back: { south: {} },
    });
    // The parent binds nothing there: the set goes.
    const other = emptied(
      { base: {} },
      { functionKeys: { start: { south: empty } } },
    );
    expect(savedForm(other, parent)).not.toHaveProperty("functionKeys");
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

describe("keyInheritance", () => {
  it("tells a key the parent binds and the profile leaves to it", () => {
    expect(keyInheritance(child, parent, "normal", ["south"])).toEqual({
      inherited: ["south"],
      overridden: [],
      own: false,
      from: "paint",
    });
  });
  it("tells a key both bind: the profile's own settings win", () => {
    expect(keyInheritance(child, parent, "normal", ["east"])).toEqual({
      inherited: [],
      overridden: ["east"],
      own: true,
    });
  });
  it("tells a key only the profile binds", () => {
    expect(keyInheritance(child, parent, "normal", ["north"])).toEqual({
      inherited: [],
      overridden: [],
      own: true,
    });
  });
  it("knows an alias for the key it is", () => {
    expect(keyInheritance(child, parent, "normal", ["b"]).overridden).toEqual([
      "b",
    ]);
  });
  it("covers every key of a control, such as a stick", () => {
    const stick: ParentProfile = {
      ...parent,
      profile: {
        ...parentProfile,
        layers: {
          base: {
            "ls.up": { tap: { type: "keys", keys: "W" } },
            "ls.down": { tap: { type: "keys", keys: "S" } },
          },
        },
      },
    };
    const own: Profile = {
      ...child,
      layers: { base: { "ls.up": { tap: { type: "keys", keys: "Up" } } } },
    };
    expect(
      keyInheritance(own, stick, "normal", [
        "leftstick",
        "ls.up",
        "ls.down",
        "ls.left",
      ]),
    ).toEqual({
      inherited: ["ls.down"],
      overridden: ["ls.up"],
      own: true,
      from: "paint",
    });
  });
  it("counts an empty binding as the profile's own, overriding the parent", () => {
    const cleared: Profile = {
      ...child,
      layers: { base: { south: {} } },
    };
    expect(keyInheritance(cleared, parent, "normal", ["south"])).toEqual({
      inherited: [],
      overridden: ["south"],
      own: false,
    });
  });
  it("reads the set of a function key in its view", () => {
    expect(keyInheritance(child, parent, "back", ["south"])).toEqual({
      inherited: ["south"],
      overridden: [],
      own: false,
      from: "paint",
    });
    expect(keyInheritance(child, parent, "start", ["south"]).inherited).toEqual(
      [],
    );
  });
  it("says nothing without a parent for the profile", () => {
    const none = { inherited: [], overridden: [], own: false };
    expect(keyInheritance(child, null, "normal", ["south"])).toEqual(none);
    expect(
      keyInheritance(child, { ...parent, id: "other" }, "normal", ["south"]),
    ).toEqual(none);
  });
  it("names the profile that wrote the key, which may be the parent's own parent", () => {
    const deep: ParentProfile = {
      ...parent,
      origins: [{ pointer: "/layers/base/south", profile: "root" }],
    };
    expect(keyInheritance(child, deep, "normal", ["south"]).from).toBe("root");
  });
});

describe("originOf", () => {
  const origins = (...list: [string, string][]): ParentProfile => ({
    ...parent,
    origins: list.map(([pointer, profile]) => ({ pointer, profile })),
  });
  it("is the parent when its own parents wrote nothing of the control", () => {
    expect(originOf(parent, "normal", "south")).toBe("paint");
    expect(
      originOf(origins(["/layers/base/east", "root"]), "normal", "south"),
    ).toBe("paint");
  });
  it("is the parent's parent that the merge listed for the control", () => {
    expect(
      originOf(origins(["/layers/base/south", "root"]), "normal", "south"),
    ).toBe("root");
    // Under another name of the control.
    expect(
      originOf(origins(["/layers/base/a", "root"]), "normal", "south"),
    ).toBe("root");
  });
  it("is the one listed for a layer or a set the merge took whole", () => {
    expect(originOf(origins(["/layers/base", "root"]), "normal", "south")).toBe(
      "root",
    );
    expect(originOf(origins(["/layers", "root"]), "normal", "south")).toBe(
      "root",
    );
    expect(
      originOf(origins(["/functionKeys/back", "root"]), "back", "south"),
    ).toBe("root");
    expect(originOf(origins(["/functionKeys", "root"]), "back", "south")).toBe(
      "root",
    );
  });
  it("prefers the most specific pointer", () => {
    expect(
      originOf(
        origins(["/layers/base", "root"], ["/layers/base/south", "mid"]),
        "normal",
        "south",
      ),
    ).toBe("mid");
  });
  it("looks only at the set of the view", () => {
    expect(
      originOf(origins(["/layers/base/south", "root"]), "back", "south"),
    ).toBe("paint");
    expect(
      originOf(
        origins(["/functionKeys/back/south", "root"]),
        "normal",
        "south",
      ),
    ).toBe("paint");
    expect(
      originOf(origins(["/timing/holdMs", "root"]), "normal", "south"),
    ).toBe("paint");
  });
});

describe("restoreInherited", () => {
  it("takes the keys out of the profile's own set", () => {
    const result = restoreInherited(child, "normal", ["east"]);
    expect(result.layers.base).toEqual({
      north: { tap: { type: "keys", keys: "N" } },
    });
    expect(child.layers.base.east).toBeDefined();
  });
  it("takes an empty binding out too", () => {
    const cleared: Profile = { ...child, layers: { base: { south: {} } } };
    expect(restoreInherited(cleared, "normal", ["south"]).layers.base).toEqual(
      {},
    );
  });
  it("works on the set of a function key", () => {
    const own: Profile = {
      ...child,
      functionKeys: {
        back: { south: { tap: { type: "keys", keys: "F2" } } },
        start: { east: { tap: { type: "keys", keys: "F3" } } },
      },
    };
    const result = restoreInherited(own, "back", ["south"]);
    expect(result.functionKeys).toEqual({
      start: { east: { tap: { type: "keys", keys: "F3" } } },
    });
    expect(result.layers.base).toEqual(child.layers.base);
  });
  it("leaves a key the profile does not write as it is", () => {
    expect(restoreInherited(child, "normal", ["south"]).layers.base).toEqual(
      child.layers.base,
    );
  });
});

describe("writesControl", () => {
  it("is whether the profile's own set has the control", () => {
    expect(writesControl(child, "normal", "east")).toBe(true);
    expect(writesControl(child, "normal", "b")).toBe(true);
    expect(writesControl(child, "normal", "south")).toBe(false);
    expect(writesControl(child, "back", "south")).toBe(false);
  });
});

describe("withExtends", () => {
  const alone: Profile = {
    schemaVersion: 1,
    name: "P",
    timing: { holdMs: 1 },
    layers: { base: {} },
  };
  it("puts extends right after the name", () => {
    const result = withExtends(alone, "paint");
    expect(Object.keys(result)).toEqual([
      "schemaVersion",
      "name",
      "extends",
      "timing",
      "layers",
    ]);
    expect(result.extends).toBe("paint");
  });
  it("changes it where it is, and takes it out", () => {
    const based = withExtends(alone, "paint");
    const other = withExtends({ ...based, name: "Q" }, "photo");
    expect(Object.keys(other)).toEqual(Object.keys(based));
    expect(other.extends).toBe("photo");
    const none = withExtends(based, undefined);
    expect(none).toEqual(alone);
    expect("extends" in none).toBe(false);
    expect(withExtends(alone, undefined)).toEqual(alone);
  });
  it("does not change the profile it was given", () => {
    const before = structuredClone(alone);
    withExtends(alone, "paint");
    expect(alone).toEqual(before);
  });
});
