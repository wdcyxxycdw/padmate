// A profile file that has `extends`, as the editor treats it (PRO-19, ADR-0045,
// ADR-0070, ADR-0076). The UI no longer makes a profile extend another or
// marks what it inherits, but the file format still allows it and the daemon
// still merges the files, so a file that has it is edited as the profile it
// does.
//
// The editor edits one file at a time: the profile's own text, which keeps
// only what the profile changes. What the profile takes from its parent is
// shown as plain bindings, as any others: a control the profile does not bind
// but its parent does has its chip and its drawer, and the first edit of it
// writes the binding into the profile's own file. The `extends` member itself
// is left as it is. Everything here is pure; the daemon merges the files
// (profiles.resolve) and these rules only follow its merge for display:
//
//   - a control the profile writes replaces the parent's whole control, in the
//     base layer, in the function set and in common (the shared bindings,
//     which the drawer notes);
//   - controlNames and timing are merged member by member, analog one level
//     deeper (a setting of a stick), as the daemon does.
import type {
  Profile,
  ProfileBinding,
  ProfileLayer,
} from "../shared/generated/schema";
import {
  canonicalControl,
  diagnosticSet,
  stableStringify,
  swapView,
  withNewFunctionSet,
} from "./editor-model";

/** A parent profile as the editor sees it: merged with its own parents. */
export interface ParentProfile {
  /** The parent's library ID, which the profile's `extends` names. */
  id: string;
  /** The parent whole (profiles.resolve of it): its parents merged in. */
  profile: Profile;
}

type Entries = Record<string, unknown>;

const isObject = (value: unknown): value is Entries =>
  !!value && typeof value === "object" && !Array.isArray(value);

/** The parent the profile is based on, when the one held is that one. */
export function parentOf(
  draft: Profile,
  parent: ParentProfile | null | undefined,
): ParentProfile | null {
  return parent && draft.extends && parent.id === draft.extends ? parent : null;
}

/**
 * The controls of a layer or set: canonical name to the key as written and
 * the binding. `color` is not a control. back, start and guide are: which of
 * them is the function key is the settings' choice (ADR-0075), and what a set
 * binds on that one stays in the file.
 */
function controlsOf(
  layer: ProfileLayer | undefined,
): Map<string, [key: string, binding: ProfileBinding]> {
  const controls = new Map<string, [string, ProfileBinding]>();
  for (const [key, value] of Object.entries(layer ?? {}))
    if (key !== "color" && isObject(value))
      controls.set(canonicalControl(key), [key, value as ProfileBinding]);
  return controls;
}

const hasSettings = (binding: ProfileBinding | undefined): boolean =>
  !!binding && Object.keys(binding).length > 0;

/**
 * layer with the controls of up that it does not write: its own bindings
 * stay, in their order, and the inherited ones follow. The same layer when
 * there is nothing to add.
 */
function layerWith(
  own: ProfileLayer | undefined,
  up: ProfileLayer | undefined,
): ProfileLayer | undefined {
  if (!up) return own;
  const written = controlsOf(own);
  let result: Entries | undefined;
  for (const [control, [key, binding]] of controlsOf(up)) {
    if (written.has(control)) continue;
    result ??= { ...(own ?? {}) };
    result[key] = binding;
  }
  return (result as ProfileLayer | undefined) ?? own;
}

/** own and up merged member by member, to depth levels of objects. */
function membersWith<T extends object>(
  own: T | undefined,
  up: T | undefined,
  depth: number,
): T | undefined {
  if (!up) return own;
  if (!own) return up;
  const ours = own as Entries;
  const theirs = up as Entries;
  const result: Entries = { ...theirs, ...ours };
  if (depth > 1)
    for (const key of Object.keys(ours)) {
      const a = theirs[key];
      const b = ours[key];
      if (isObject(a) && isObject(b))
        result[key] = membersWith<Entries>(b, a, depth - 1);
    }
  return result as T;
}

/**
 * The profile with what its parent adds: the controls of the base layer, of
 * the function set and of common that it does not write, the key
 * names and the timing, and the settings of sticks, triggers and the D-pad it
 * does not set. This is what the profile does once the daemon merges the files, which
 * the mapping page shows. The same profile when the parent adds nothing, and
 * when no parent is held for it.
 */
export function withInherited(
  draft: Profile,
  parent: ParentProfile | null | undefined,
): Profile {
  const held = parentOf(draft, parent)?.profile;
  if (!held) return draft;
  // Of the old functionKeys, each side is the one set it stands for.
  const up = withNewFunctionSet(held);
  let result = withNewFunctionSet(draft);
  const own = result;
  const set = (members: Partial<Profile>) => {
    result = { ...result, ...members };
  };
  const base = layerWith(draft.layers.base, up.layers.base);
  if (base !== draft.layers.base)
    set({ layers: { ...draft.layers, base: base as ProfileLayer } });
  const functionSet = layerWith(own.functionSet, up.functionSet);
  if (functionSet !== own.functionSet) set({ functionSet });
  const common = layerWith(draft.common, up.common);
  if (common !== draft.common) set({ common });
  const names = membersWith(draft.controlNames, up.controlNames, 1);
  if (names !== draft.controlNames) set({ controlNames: names });
  const timing = membersWith(draft.timing, up.timing, 1);
  if (timing !== draft.timing) set({ timing });
  const analog = membersWith(draft.analog, up.analog, 2);
  if (analog !== draft.analog) set({ analog });
  return result;
}

/** layer without the control, whatever name it is written under. */
function withoutControl(layer: Entries, control: string): Entries {
  return Object.fromEntries(
    Object.entries(layer).filter(
      ([key]) => key === "color" || canonicalControl(key) !== control,
    ),
  );
}

/** layer with the control set: where it stands now, else at the end. */
function withControl(
  layer: Entries,
  control: string,
  binding: ProfileBinding,
): Entries {
  let placed = false;
  const result: Entries = {};
  for (const [key, value] of Object.entries(layer)) {
    if (key !== "color" && canonicalControl(key) === control) {
      if (!placed) result[key] = binding;
      placed = true;
    } else result[key] = value;
  }
  if (!placed) result[control] = binding;
  return result;
}

/**
 * The own layer of a profile after an edit made on the layer shown (which
 * holds the inherited controls too): a control that the edit changed is
 * written, the rest stays as the profile has it. A control emptied while the
 * parent binds it stays as an empty binding, which is how a profile leaves it
 * with nothing; one the parent does not bind goes.
 */
function reconcileLayer(
  own: ProfileLayer | undefined,
  shown: ProfileLayer | undefined,
  next: ProfileLayer | undefined,
  up: ProfileLayer | undefined,
): ProfileLayer | undefined {
  const before = controlsOf(shown);
  const after = controlsOf(next);
  const parents = controlsOf(up);
  let result: Entries = { ...(own ?? {}) };
  for (const control of new Set([...before.keys(), ...after.keys()])) {
    const was = before.get(control)?.[1];
    const now = after.get(control)?.[1];
    if (stableStringify(was) === stableStringify(now)) continue;
    if (now && hasSettings(now)) result = withControl(result, control, now);
    else if (hasSettings(parents.get(control)?.[1]))
      result = withControl(result, control, {});
    else result = withoutControl(result, control);
  }
  return Object.keys(result).length || own
    ? (result as ProfileLayer)
    : undefined;
}

/**
 * Members of an object after an edit made on the merged object: those the
 * edit changed are written to own, and the rest stays. undefined when nothing
 * is left, which the member then goes.
 */
function reconcileMembers(
  own: object | undefined,
  shown: object | undefined,
  next: object | undefined,
  depth: number,
): object | undefined {
  const result: Entries = { ...(own ?? {}) };
  const keys = new Set([
    ...Object.keys(shown ?? {}),
    ...Object.keys(next ?? {}),
  ]);
  for (const key of keys) {
    const was = (shown as Entries | undefined)?.[key];
    const now = (next as Entries | undefined)?.[key];
    if (stableStringify(was) === stableStringify(now)) continue;
    if (now === undefined) delete result[key];
    else if (
      depth > 1 &&
      isObject(now) &&
      (was === undefined || isObject(was))
    ) {
      const inner = reconcileMembers(
        (own as Entries | undefined)?.[key] as object | undefined,
        was,
        now,
        depth - 1,
      );
      if (inner) result[key] = inner;
      else delete result[key];
    } else result[key] = now;
  }
  return Object.keys(result).length ? result : undefined;
}

/**
 * The profile that results from an edit made on withInherited(draft, parent):
 * next is that profile edited, and only what the edit changed is written into
 * draft, so that what the profile inherits stays with its parent and is not
 * copied into the file by an edit of something else. A control edited is the
 * profile's own from then on. next itself when no parent is held.
 */
export function writeBack(
  draft: Profile,
  parent: ParentProfile | null | undefined,
  next: Profile,
): Profile {
  const held = parentOf(draft, parent)?.profile;
  if (!held) return next;
  const up = withNewFunctionSet(held);
  const shown = withInherited(draft, parent);
  if (shown === draft) return next;
  const merged: Record<string, unknown> = {};
  const layers = {
    ...next.layers,
    base: reconcileLayer(
      draft.layers.base,
      shown.layers.base,
      next.layers.base,
      up.layers.base,
    ) as ProfileLayer,
  };
  merged.layers = layers;
  const own = withNewFunctionSet(draft);
  const set = reconcileLayer(
    own.functionSet,
    shown.functionSet,
    next.functionSet,
    up.functionSet,
  );
  merged.functionSet = set && Object.keys(set).length ? set : undefined;
  merged.common = reconcileLayer(
    draft.common,
    shown.common,
    next.common,
    up.common,
  );
  merged.controlNames = reconcileMembers(
    draft.controlNames,
    shown.controlNames,
    next.controlNames,
    1,
  );
  merged.timing = reconcileMembers(draft.timing, shown.timing, next.timing, 1);
  merged.analog = reconcileMembers(draft.analog, shown.analog, next.analog, 2);
  // The members stay where the file has them; ones it did not have follow.
  const rest = { ...next } as Entries;
  const ordered: Entries = {};
  for (const key of Object.keys(own))
    if (key in merged) {
      if (merged[key] !== undefined) ordered[key] = merged[key];
    } else if (key in rest) ordered[key] = rest[key];
  for (const key of Object.keys(rest))
    if (!(key in ordered) && !(key in own)) {
      const value = key in merged ? merged[key] : rest[key];
      if (value !== undefined) ordered[key] = value;
    }
  for (const key of Object.keys(merged))
    if (!(key in ordered) && merged[key] !== undefined)
      ordered[key] = merged[key];
  return ordered as Profile;
}

/**
 * The daemon's warning "binding has no action" about a control the profile
 * leaves with an empty binding while its parent binds it: that is how a
 * profile says the key does nothing here, so the editor drops the warning, as
 * it does for a key that has only its label (isLabelOnlyWarning).
 */
export function isClearedInherited(
  diagnostic: { severity: string; pointer: string; message: string },
  profile: Profile | null,
  parent: ParentProfile | null | undefined,
): boolean {
  if (
    !profile ||
    diagnostic.severity !== "warning" ||
    diagnostic.message !== "binding has no action"
  )
    return false;
  const held = parentOf(profile, parent);
  const found = diagnosticSet(diagnostic.pointer);
  const key = found?.rest.length === 1 ? found.rest[0] : undefined;
  if (!held || !found || key === undefined) return false;
  const control = canonicalControl(key);
  const written = controlsOf(swapView(profile, found.view).layers.base).get(
    control,
  );
  const up = controlsOf(swapView(held.profile, found.view).layers.base).get(
    control,
  );
  return !!written && !hasSettings(written[1]) && hasSettings(up?.[1]);
}
