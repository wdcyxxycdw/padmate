// Function keys (MAP-27, UI-30, ADR-0069): View (back) and Menu (start) hold
// no bindings of their own; while one is held, the other controls use its set
// of bindings (profile.functionKeys). These are the editor's rules for it.
import { describe, expect, it } from "vitest";
import type { ProfileDiagnostic } from "../shared/api";
import type { Profile } from "../shared/generated/schema";
import {
  bindingAt,
  functionSetId,
  isFunctionKey,
  isLabelOnlyWarning,
  layerUsage,
  savedForm,
  setBinding,
  swapView,
  usesOnlyBase,
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

const withSets: Profile = {
  ...profile,
  common: { guide: { block: true } },
  functionKeys: {
    back: { south: { label: "Redo", press: keys("Ctrl+Shift+Z") } },
    start: { "ls.cw": { press: keys("Ctrl+]") } },
  },
  analog: { ls: { mode: "knob" } },
};

/** The order of the members, which a save keeps. */
const order = (value: object) => Object.keys(value);

describe("function key controls", () => {
  it("are back and start, under their aliases too", () => {
    for (const control of ["back", "select", "start"])
      expect(isFunctionKey(control), control).toBe(true);
    for (const control of ["south", "guide", "leftstick", "lt", "dpad.up"])
      expect(isFunctionKey(control), control).toBe(false);
  });

  it("name their set the way the daemon names its level", () => {
    expect(functionSetId("back")).toBe("function:back");
    expect(functionSetId("start")).toBe("function:start");
  });
});

describe("the sets of a profile as key sets", () => {
  it("are read by their level name, empty when absent", () => {
    expect(bindingAt(withSets, "function:back", "south").label).toBe("Redo");
    expect(bindingAt(withSets, "function:start", "south")).toEqual({});
    expect(bindingAt(profile, "function:back", "south")).toEqual({});
  });

  it("are created by a binding, after the layers, and removed with the last one", () => {
    const made = setBinding(profile, "function:back", "south", {
      press: keys("F13"),
    });
    expect(made.functionKeys).toEqual({
      back: { south: { press: keys("F13") } },
    });
    expect(order(made)).toEqual([
      "schemaVersion",
      "name",
      "layers",
      "functionKeys",
    ]);
    // Nothing else moved, and the draft it came from is untouched.
    expect(made.layers).toEqual(profile.layers);
    expect(profile.functionKeys).toBeUndefined();

    const gone = setBinding(made, "function:back", "south", undefined);
    expect(gone).toEqual(profile);
    expect("functionKeys" in gone).toBe(false);
  });

  it("go after common when the profile has one", () => {
    const made = setBinding(
      { ...profile, common: { guide: { block: true } }, analog: {} },
      "function:start",
      "north",
      { press: keys("F14") },
    );
    expect(order(made)).toEqual([
      "schemaVersion",
      "name",
      "layers",
      "common",
      "functionKeys",
      "analog",
    ]);
  });

  it("keep the other set and the order of controls when one control goes", () => {
    const two = setBinding(withSets, "function:back", "east", {
      press: keys("F15"),
    });
    expect(order(two.functionKeys?.back ?? {})).toEqual(["south", "east"]);
    const one = setBinding(two, "function:back", "south", undefined);
    expect(order(one.functionKeys?.back ?? {})).toEqual(["east"]);
    expect(one.functionKeys?.start).toEqual(withSets.functionKeys?.start);
    // The last control of one set takes only that set away.
    const last = setBinding(withSets, "function:start", "ls.cw", undefined);
    expect(last.functionKeys).toEqual({ back: withSets.functionKeys?.back });
  });
});

describe("the view of a key set", () => {
  it("is the profile itself in the normal view", () => {
    expect(swapView(withSets, "normal")).toBe(withSets);
  });

  it("puts the set where the editor edits, and the normal bindings in the set's place", () => {
    const view = swapView(withSets, "back");
    expect(view.layers.base).toEqual(withSets.functionKeys?.back);
    expect(view.functionKeys?.back).toEqual(withSets.layers.base);
    // The other set and everything else are what they were.
    expect(view.functionKeys?.start).toEqual(withSets.functionKeys?.start);
    expect(view.common).toEqual(withSets.common);
    expect(view.analog).toEqual(withSets.analog);
    expect(withSets.layers.base.south?.label).toBe("Undo");
  });

  it("shows a set that does not exist as an empty one", () => {
    const view = swapView(profile, "start");
    expect(view.layers.base).toEqual({});
    expect(view.functionKeys?.start).toEqual(profile.layers.base);
  });

  it("is its own inverse, key order and all", () => {
    for (const source of [withSets, profile])
      for (const view of ["back", "start"] as const) {
        const back = swapView(swapView(source, view), view);
        expect(JSON.stringify(back), view).toBe(JSON.stringify(source));
      }
  });

  it("carries an edit of the set back to functionKeys", () => {
    const view = swapView(profile, "back");
    const edited = setBinding(view, "base", "east", { press: keys("F16") });
    const saved = swapView(edited, "back");
    expect(saved.functionKeys).toEqual({
      back: { east: { press: keys("F16") } },
    });
    expect(saved.layers).toEqual(profile.layers);
    expect(order(saved)).toEqual([
      "schemaVersion",
      "name",
      "layers",
      "functionKeys",
    ]);
  });

  it("leaves no empty set behind when the last binding of a set is cleared", () => {
    const view = swapView(withSets, "start");
    const cleared = setBinding(view, "base", "ls.cw", undefined);
    const saved = swapView(cleared, "start");
    expect(saved.functionKeys).toEqual({ back: withSets.functionKeys?.back });
    const both = swapView(
      setBinding(swapView(saved, "back"), "base", "south", undefined),
      "back",
    );
    expect("functionKeys" in both).toBe(false);
    expect(both.layers).toEqual(withSets.layers);
  });

  it("keeps the other key sets in view, for what looks at every set", () => {
    const view = swapView(withSets, "back");
    // The layers, common and both sets are all there, only one is renamed.
    expect(Object.keys(view.layers)).toEqual(["base"]);
    expect(view.functionKeys?.start).toBeDefined();
    expect(view.functionKeys?.back).toEqual(withSets.layers.base);
  });
});

describe("what a save writes of the function keys (ADR-0069)", () => {
  const old: Profile = {
    schemaVersion: 1,
    name: "Old",
    layers: {
      base: {
        south: { label: "Undo", press: keys("Ctrl+Z") },
        back: { label: "Paste", press: keys("Ctrl+V") },
        east: { press: keys("F13") },
        select: { press: keys("Ctrl+B") },
      },
      alt: {
        start: { press: keys("Ctrl+S") },
        west: { press: keys("F14") },
      },
    },
    common: { back: { block: true }, guide: { block: true } },
  };

  it("takes the old bindings of back and start out of every layer and common", () => {
    const saved = savedForm(old);
    expect(saved.layers.base).toEqual({
      south: old.layers.base.south,
      east: old.layers.base.east,
    });
    expect(saved.layers.alt).toEqual({ west: old.layers.alt.west });
    expect(saved.common).toEqual({ guide: { block: true } });
    // The members that stay keep their order, and nothing is added.
    expect(order(saved.layers.base)).toEqual(["south", "east"]);
    expect(order(saved)).toEqual(order(old));
    expect(saved.functionKeys).toBeUndefined();
    // The draft is not changed.
    expect(old.layers.base.back).toBeDefined();
  });

  it("leaves a common with nothing else as an empty one", () => {
    const saved = savedForm({
      ...old,
      common: { back: { block: true }, start: { block: true } },
    });
    expect(saved.common).toEqual({});
  });

  it("writes functionKeys only when a set has bindings", () => {
    expect("functionKeys" in savedForm({ ...profile, functionKeys: {} })).toBe(
      false,
    );
    expect(
      "functionKeys" in
        savedForm({ ...profile, functionKeys: { back: {}, start: {} } }),
    ).toBe(false);
    const kept = savedForm({
      ...profile,
      functionKeys: { back: {}, start: withSets.functionKeys?.start },
    });
    expect(kept.functionKeys).toEqual({ start: withSets.functionKeys?.start });
  });

  it("leaves out the parts of a set still being filled in, as for a layer", () => {
    const saved = savedForm({
      ...profile,
      functionKeys: {
        back: {
          south: { press: { type: "keys", keys: "" } },
          east: { label: "Brush", press: { type: "keys", keys: "" } },
          west: { press: keys("F13") },
        },
      },
    });
    expect(saved.functionKeys).toEqual({
      back: { east: { label: "Brush" }, west: { press: keys("F13") } },
    });
    // A set with nothing left but empty bindings goes.
    expect(
      "functionKeys" in
        savedForm({
          ...profile,
          functionKeys: {
            start: { south: { press: { type: "keys", keys: "" } } },
          },
        }),
    ).toBe(false);
  });

  it("returns a draft with nothing to remove as it is", () => {
    const filled: Profile = { ...withSets, schemaVersion: 1 };
    expect(savedForm(filled)).toBe(filled);
  });

  it("keeps functionKeys where the file had it", () => {
    const placed: Profile = {
      schemaVersion: 1,
      name: "Placed",
      functionKeys: withSets.functionKeys,
      layers: {
        base: { back: { block: true }, south: { press: keys("F13") } },
      },
    };
    expect(order(savedForm(placed))).toEqual([
      "schemaVersion",
      "name",
      "functionKeys",
      "layers",
    ]);
  });

  it("does not count the old bindings of back and start as more than the base layer", () => {
    const usage = layerUsage(old);
    expect(usage.extraLayers).toEqual(["alt"]);
    expect(usage.commonBindings).toBe(1);
    expect(usage.blocks).toBe(1);
    const onlyOld: Profile = {
      ...profile,
      common: { back: { block: true } },
    };
    expect(usesOnlyBase(layerUsage(onlyOld))).toBe(true);
  });
});

describe("the label-only warning in a set", () => {
  const warning = (pointer: string): ProfileDiagnostic => ({
    severity: "warning",
    pointer,
    message: "binding has no action",
    offset: 0,
    line: 1,
    col: 1,
  });

  it("is dropped for a control of a set that has only its label", () => {
    const named: Profile = {
      ...profile,
      functionKeys: { back: { east: { label: "Brush" } } },
    };
    expect(isLabelOnlyWarning(warning("/functionKeys/back/east"), named)).toBe(
      true,
    );
    expect(isLabelOnlyWarning(warning("/functionKeys/start/east"), named)).toBe(
      false,
    );
    expect(isLabelOnlyWarning(warning("/functionKeys/back/west"), named)).toBe(
      false,
    );
  });
});
