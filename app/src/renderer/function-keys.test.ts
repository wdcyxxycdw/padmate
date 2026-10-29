// The function key (MAP-27, UI-30, ADR-0075): one of View (back), Menu (start)
// and the Xbox button (guide), chosen in the settings, holds no bindings of
// its own; while it is held, the other controls use the profile's function
// set (profile.functionSet). These are the editor's rules for it.
import { describe, expect, it } from "vitest";
import type { ProfileDiagnostic } from "../shared/api";
import type { Profile } from "../shared/generated/schema";
import {
  asFunctionKey,
  bindingAt,
  defaultFunctionKey,
  diagnosticSet,
  type FunctionKey,
  functionKeys,
  functionSetId,
  isFunctionKey,
  isLabelOnlyWarning,
  layerUsage,
  savedForm,
  setBinding,
  swapView,
  usesOnlyBase,
  withNewFunctionSet,
} from "./editor-model";

const keys = (value: string) =>
  ({ type: "keys", keys: value, mode: "tap" }) as const;

const profile: Profile = {
  schemaVersion: 1,
  name: "Paint",
  layers: {
    base: { south: { label: "Undo", press: keys("Ctrl+Z") } },
  },
};

const withSet: Profile = {
  ...profile,
  common: { guide: { block: true } },
  functionSet: {
    south: { label: "Redo", press: keys("Ctrl+Shift+Z") },
    "ls.cw": { press: keys("Ctrl+]") },
  },
  analog: { ls: { mode: "knob" } },
};

/** The order of the members, which a save keeps. */
const order = (value: object) => Object.keys(value);

describe("the function key", () => {
  it("is Menu unless the settings choose another of the three", () => {
    expect(defaultFunctionKey).toBe("start");
    expect([...functionKeys].sort()).toEqual(["back", "guide", "start"]);
    for (const key of functionKeys) expect(asFunctionKey(key)).toBe(key);
    for (const value of [undefined, null, "", "select", "south", 3, {}])
      expect(asFunctionKey(value), String(value)).toBe("start");
  });

  it("is one control, back under its alias too", () => {
    const names: Record<FunctionKey, string[]> = {
      start: ["start"],
      back: ["back", "select"],
      guide: ["guide"],
    };
    for (const key of functionKeys)
      for (const control of [
        "back",
        "select",
        "start",
        "guide",
        "south",
        "leftstick",
        "lt",
        "dpad.up",
      ])
        expect(isFunctionKey(control, key), `${key} ${control}`).toBe(
          names[key].includes(control),
        );
  });

  it("names its set apart from any layer's name", () => {
    expect(functionSetId).toBe("function:set");
    expect(functionSetId in profile.layers).toBe(false);
  });
});

describe("the function set as a key set", () => {
  it("is read by its ID, empty when absent", () => {
    expect(bindingAt(withSet, functionSetId, "south").label).toBe("Redo");
    expect(bindingAt(withSet, functionSetId, "north")).toEqual({});
    expect(bindingAt(profile, functionSetId, "south")).toEqual({});
  });

  it("is created by a binding, after the layers, and removed with the last one", () => {
    const made = setBinding(profile, functionSetId, "south", {
      press: keys("F13"),
    });
    expect(made.functionSet).toEqual({ south: { press: keys("F13") } });
    expect(order(made)).toEqual([
      "schemaVersion",
      "name",
      "layers",
      "functionSet",
    ]);
    // Nothing else moved, and the draft it came from is untouched.
    expect(made.layers).toEqual(profile.layers);
    expect(profile.functionSet).toBeUndefined();

    const gone = setBinding(made, functionSetId, "south", undefined);
    expect(gone).toEqual(profile);
    expect("functionSet" in gone).toBe(false);
  });

  it("goes after common when the profile has one", () => {
    const made = setBinding(
      { ...profile, common: { guide: { block: true } }, analog: {} },
      functionSetId,
      "north",
      { press: keys("F14") },
    );
    expect(order(made)).toEqual([
      "schemaVersion",
      "name",
      "layers",
      "common",
      "functionSet",
      "analog",
    ]);
  });

  it("keeps the order of controls when one control goes", () => {
    const two = setBinding(withSet, functionSetId, "east", {
      press: keys("F15"),
    });
    expect(order(two.functionSet ?? {})).toEqual(["south", "ls.cw", "east"]);
    const one = setBinding(two, functionSetId, "south", undefined);
    expect(order(one.functionSet ?? {})).toEqual(["ls.cw", "east"]);
  });

  it("can bind back, start and guide, whichever of them the settings choose", () => {
    const made = setBinding(profile, functionSetId, "back", {
      press: keys("Ctrl+V"),
    });
    expect(made.functionSet).toEqual({ back: { press: keys("Ctrl+V") } });
    // The same control under its alias is the same binding.
    expect(bindingAt(made, functionSetId, "select").press).toEqual(
      keys("Ctrl+V"),
    );
  });
});

describe("the view of a key set", () => {
  it("is the profile itself in the normal view", () => {
    expect(swapView(withSet, "normal")).toBe(withSet);
  });

  it("puts the set where the editor edits, and the normal bindings in the set's place", () => {
    const view = swapView(withSet, "function");
    expect(view.layers.base).toEqual(withSet.functionSet);
    expect(view.functionSet).toEqual(withSet.layers.base);
    // Everything else is what it was.
    expect(view.common).toEqual(withSet.common);
    expect(view.analog).toEqual(withSet.analog);
    expect(withSet.layers.base.south?.label).toBe("Undo");
  });

  it("shows a set that does not exist as an empty one", () => {
    const view = swapView(profile, "function");
    expect(view.layers.base).toEqual({});
    expect(view.functionSet).toEqual(profile.layers.base);
  });

  it("is its own inverse, key order and all", () => {
    for (const source of [withSet, profile]) {
      const back = swapView(swapView(source, "function"), "function");
      expect(JSON.stringify(back)).toBe(JSON.stringify(source));
    }
  });

  it("carries an edit of the set back to functionSet", () => {
    const view = swapView(profile, "function");
    const edited = setBinding(view, "base", "east", { press: keys("F16") });
    const saved = swapView(edited, "function");
    expect(saved.functionSet).toEqual({ east: { press: keys("F16") } });
    expect(saved.layers).toEqual(profile.layers);
    expect(order(saved)).toEqual([
      "schemaVersion",
      "name",
      "layers",
      "functionSet",
    ]);
  });

  it("leaves no empty set behind when the last binding of the set is cleared", () => {
    const view = swapView(
      { ...profile, functionSet: { south: { press: keys("F13") } } },
      "function",
    );
    const cleared = setBinding(view, "base", "south", undefined);
    const saved = swapView(cleared, "function");
    expect("functionSet" in saved).toBe(false);
    expect(saved.layers).toEqual(profile.layers);
  });

  it("edits the set an old functionKeys stands for, and writes it as functionSet", () => {
    const old: Profile = {
      ...profile,
      functionKeys: { start: { east: { press: keys("F13") } } },
    };
    const view = swapView(old, "function");
    expect(view.layers.base).toEqual({ east: { press: keys("F13") } });
    expect("functionKeys" in view).toBe(false);
  });
});

describe("the old functionKeys (ADR-0069) and the one set they stand for (ADR-0075)", () => {
  const old: Profile = {
    ...profile,
    functionKeys: {
      back: {
        south: { press: keys("Ctrl+Shift+Z") },
        east: { press: keys("F14") },
      },
      start: { south: { press: keys("Ctrl+Y") }, west: { press: keys("F15") } },
    },
  };

  it("are Menu's set, then View's controls that Menu's leaves out", () => {
    const next = withNewFunctionSet(old);
    expect(next.functionSet).toEqual({
      south: { press: keys("Ctrl+Y") },
      west: { press: keys("F15") },
      east: { press: keys("F14") },
    });
    expect(order(next.functionSet ?? {})).toEqual(["south", "west", "east"]);
    expect("functionKeys" in next).toBe(false);
    // The member stands where the old one stood.
    expect(order(next)).toEqual([
      "schemaVersion",
      "name",
      "layers",
      "functionSet",
    ]);
  });

  it("count a control as Menu's when it is written under another name", () => {
    const aliased = withNewFunctionSet({
      ...profile,
      functionKeys: {
        back: { a: { press: keys("F1") }, b: { press: keys("F2") } },
        start: { south: { press: keys("F3") } },
      },
    });
    expect(aliased.functionSet).toEqual({
      south: { press: keys("F3") },
      b: { press: keys("F2") },
    });
  });

  it("give way to functionSet when the file has both, as for the daemon", () => {
    const both: Profile = {
      ...old,
      functionSet: { north: { press: keys("F16") } },
    };
    const next = withNewFunctionSet(both);
    expect(next.functionSet).toEqual({ north: { press: keys("F16") } });
    expect("functionKeys" in next).toBe(false);
  });

  it("leave no set when they have no bindings, and no change when absent", () => {
    for (const functionKeys of [{}, { back: {}, start: {} }]) {
      const next = withNewFunctionSet({ ...profile, functionKeys });
      expect("functionSet" in next).toBe(false);
      expect("functionKeys" in next).toBe(false);
    }
    expect(withNewFunctionSet(withSet)).toBe(withSet);
  });

  it("are saved as functionSet, without functionKeys", () => {
    const saved = savedForm(old);
    expect(saved.functionSet).toEqual(withNewFunctionSet(old).functionSet);
    expect("functionKeys" in saved).toBe(false);
    // A draft that is only opened saves to the same text as its file.
    expect(JSON.stringify(savedForm(withNewFunctionSet(old)))).toBe(
      JSON.stringify(saved),
    );
    // The draft is not changed.
    expect(old.functionKeys).toBeDefined();
  });
});

describe("what a save writes of the function set and of back, start and guide (ADR-0075)", () => {
  const bound: Profile = {
    schemaVersion: 1,
    name: "Bound",
    layers: {
      base: {
        south: { label: "Undo", press: keys("Ctrl+Z") },
        back: { label: "Paste", press: keys("Ctrl+V") },
        guide: { press: keys("F13") },
      },
      alt: {
        start: { press: keys("Ctrl+S") },
        west: { press: keys("F14") },
      },
    },
    common: { back: { block: true }, guide: { block: true } },
  };

  it("keeps the bindings on back, start and guide in every layer and common", () => {
    const saved = savedForm(bound);
    expect(saved).toBe(bound);
    expect(saved.layers.base.back).toBeDefined();
    expect(saved.layers.alt.start).toBeDefined();
    expect(saved.common).toEqual({
      back: { block: true },
      guide: { block: true },
    });
  });

  it("writes functionSet only when it has bindings", () => {
    expect("functionSet" in savedForm({ ...profile, functionSet: {} })).toBe(
      false,
    );
    const kept = savedForm({
      ...profile,
      functionSet: { south: { press: keys("F13") } },
    });
    expect(kept.functionSet).toEqual({ south: { press: keys("F13") } });
  });

  it("leaves out the parts of the set still being filled in, as for a layer", () => {
    const saved = savedForm({
      ...profile,
      functionSet: {
        south: { press: { type: "keys", keys: "" } },
        east: { label: "Brush", press: { type: "keys", keys: "" } },
        west: { press: keys("F13") },
      },
    });
    expect(saved.functionSet).toEqual({
      east: { label: "Brush" },
      west: { press: keys("F13") },
    });
    // A set with nothing left but empty bindings goes.
    expect(
      "functionSet" in
        savedForm({
          ...profile,
          functionSet: { south: { press: { type: "keys", keys: "" } } },
        }),
    ).toBe(false);
  });

  it("returns a draft with nothing to remove as it is", () => {
    const filled: Profile = { ...withSet, schemaVersion: 1 };
    expect(savedForm(filled)).toBe(filled);
  });

  it("keeps functionSet where the file had it", () => {
    const placed: Profile = {
      schemaVersion: 1,
      name: "Placed",
      functionSet: withSet.functionSet,
      layers: {
        base: { back: { block: true }, south: { press: keys("F13") } },
      },
    };
    expect(order(savedForm(placed))).toEqual([
      "schemaVersion",
      "name",
      "functionSet",
      "layers",
    ]);
  });

  it("writes a control the edits empty, which the parent binds, as an empty binding in the set", () => {
    const parent = {
      id: "base-profile",
      profile: {
        ...profile,
        functionKeys: { start: { south: { press: keys("F13") } } },
      } as Profile,
    };
    const child: Profile = {
      ...profile,
      extends: "base-profile",
      functionSet: { south: { press: { type: "keys", keys: "" } } },
    };
    expect(savedForm(child, parent).functionSet).toEqual({ south: {} });
  });

  it("does not count what a layer binds on the function key as more than the base layer", () => {
    const only = (key: FunctionKey): Profile => ({
      ...profile,
      common: { [key]: { block: true } },
    });
    for (const key of functionKeys) {
      expect(usesOnlyBase(layerUsage(only(key), key)), key).toBe(true);
      for (const other of functionKeys.filter((entry) => entry !== key))
        expect(
          usesOnlyBase(layerUsage(only(other), key)),
          `${key} ${other}`,
        ).toBe(false);
    }
    const usage = layerUsage(bound, "start");
    expect(usage.extraLayers).toEqual(["alt"]);
    // back and guide are ordinary controls when Menu is the function key.
    expect(usage.commonBindings).toBe(2);
    expect(usage.blocks).toBe(2);
    expect(layerUsage(bound, "back").commonBindings).toBe(1);
  });
});

describe("where a diagnostic points", () => {
  it("is the base layer or the function set, and the parts after it", () => {
    expect(diagnosticSet("/layers/base/south/tap")).toEqual({
      view: "normal",
      rest: ["south", "tap"],
    });
    expect(diagnosticSet("/functionSet/south/tap")).toEqual({
      view: "function",
      rest: ["south", "tap"],
    });
    // The old format points into one of the two sets, which is the set here.
    expect(diagnosticSet("/functionKeys/back/south/tap")).toEqual({
      view: "function",
      rest: ["south", "tap"],
    });
    expect(diagnosticSet("/functionKeys/start/lt")).toEqual({
      view: "function",
      rest: ["lt"],
    });
  });

  it("is nothing for other places", () => {
    for (const pointer of [
      "",
      "/name",
      "/layers/alt/south",
      "/common/south",
      "/functionKeys",
      "/functionKeys/guide/south",
      "/analog/ls",
    ])
      expect(diagnosticSet(pointer), pointer).toBeNull();
  });
});

describe("the label-only warning in the function set", () => {
  const warning = (pointer: string): ProfileDiagnostic => ({
    severity: "warning",
    pointer,
    message: "binding has no action",
    offset: 0,
    line: 1,
    col: 1,
  });

  it("is dropped for a control of the set that has only its label", () => {
    const named: Profile = {
      ...profile,
      functionSet: { east: { label: "Brush" } },
    };
    expect(isLabelOnlyWarning(warning("/functionSet/east"), named)).toBe(true);
    expect(isLabelOnlyWarning(warning("/functionSet/west"), named)).toBe(false);
    // A warning of a file of the old format is about the same set.
    expect(isLabelOnlyWarning(warning("/functionKeys/back/east"), named)).toBe(
      true,
    );
    expect(isLabelOnlyWarning(warning("/layers/base/east"), named)).toBe(false);
  });

  it("is dropped for the old format too", () => {
    const old: Profile = {
      ...profile,
      functionKeys: { start: { east: { label: "Brush" } } },
    };
    expect(isLabelOnlyWarning(warning("/functionKeys/start/east"), old)).toBe(
      true,
    );
  });
});
