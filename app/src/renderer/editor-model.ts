import type { ProfileDiagnostic } from "../shared/api";
import type {
  Profile,
  ProfileAction,
  ProfileBinding,
  ProfileFeedback,
  ProfileFunctionKeys,
  ProfileLayer,
  ProfileLayerAction,
  ProfileMouseAction,
  ProfileProfileAction,
  ProfileScriptAction,
  ProfileSequenceStep,
  ProfileTiming,
} from "../shared/generated/schema";
import { macroName } from "../shared/macro-names";
import { PROFILE_SCHEMA_VERSION } from "../shared/profile-version";
import { isEmptyStep, stepsAction } from "./sequence-model";
import { mediaKeysText } from "./shortcut-keys";

export { macroName };

export const gestures = [
  "press",
  "release",
  "tap",
  "hold",
  "doubleTap",
  "tripleTap",
  "quadTap",
] as const;
export type Gesture = (typeof gestures)[number];
export function resolveTiming(
  ...overrides: (ProfileTiming | undefined)[]
): Required<ProfileTiming> {
  const result: Required<ProfileTiming> = {
    holdMs: 250,
    multiTapMs: 220,
    tapPressMs: 20,
    tapGapMs: 20,
  };
  for (const timing of overrides)
    for (const key of Object.keys(result) as (keyof ProfileTiming)[])
      if (timing?.[key] !== undefined) result[key] = timing[key];
  return result;
}
export function editorError(error: unknown): string {
  return error && typeof error === "object" && "message" in error
    ? String(error.message)
    : String(error);
}
export const aliases: Record<string, string> = {
  a: "south",
  b: "east",
  x: "west",
  y: "north",
  lb: "leftshoulder",
  rb: "rightshoulder",
  l3: "leftstick",
  r3: "rightstick",
  select: "back",
  dpup: "dpad.up",
  dpdown: "dpad.down",
  dpleft: "dpad.left",
  dpright: "dpad.right",
};
export function canonicalControl(control: string) {
  return aliases[control] ?? control;
}

/**
 * Whether a control follows how far a stick or a trigger is pushed, so a
 * repeat on it may set minRateHz (ANA-12, ANA-15): a stick direction
 * (diagonals too), active, inner or outer, a half axis, lt, rt, lt.full or
 * rt.full. The same list as the daemon's control.Control.HasDepth; knob
 * ticks, d-pad diagonals and buttons have no depth.
 */
export function hasDepth(control: string): boolean {
  const key = canonicalControl(control);
  return (
    /^[lr]s\.(up|down|left|right|upleft|upright|downleft|downright|active|inner|outer)$/.test(
      key,
    ) ||
    /^(left|right)[xy][+-]$/.test(key) ||
    /^(left|right)trigger[+-]$/.test(key) ||
    /^[lr]t(\.full)?$/.test(key)
  );
}

/**
 * Whether a control is a tick of a virtual knob (ls.cw, ls.ccw, rs.cw,
 * rs.ccw): a press and an immediate release, so it has nothing to repeat.
 */
export function isKnobTick(control: string): boolean {
  return /^[lr]s\.c?cw$/.test(canonicalControl(control));
}

/**
 * A binding without minRateHz in its repeats, for a control that has no
 * depth: the daemon refuses the combination.
 */
export function withoutMinRate(binding: ProfileBinding): ProfileBinding {
  let next = binding;
  for (const gesture of gestures) {
    const action = binding[gesture];
    if (action?.type !== "keys" || action.repeat?.minRateHz === undefined)
      continue;
    const { minRateHz: _minRateHz, ...repeat } = action.repeat;
    next = { ...next, [gesture]: { ...action, repeat } };
  }
  return next;
}
/**
 * The function keys (MAP-27, ADR-0069): View (back) and Menu (start) hold no
 * bindings of their own. While one is held, the other controls use its set of
 * bindings (profile.functionKeys), and a control the set leaves out does what
 * it does otherwise.
 */
export const functionKeys = ["back", "start"] as const;
export type FunctionKey = (typeof functionKeys)[number];
/** Which set of bindings the mapping page shows and edits: the usual one, or a function key's. */
export type KeyView = "normal" | FunctionKey;

/** Whether a control (or its alias) is back or start, which only switch sets. */
export function isFunctionKey(control: string): boolean {
  return (functionKeys as readonly string[]).includes(
    canonicalControl(control),
  );
}

/**
 * The ID of a function key's set as a key set, like a layer's name: the
 * level name the daemon gives its decisions.
 */
export function functionSetId(key: FunctionKey): string {
  return `function:${key}`;
}
function functionKeyOf(id: string): FunctionKey | undefined {
  return functionKeys.find((key) => functionSetId(key) === id);
}

export function layerAt(profile: Profile, id: string): ProfileLayer {
  if (id === "common") return profile.common ?? {};
  const key = functionKeyOf(id);
  if (key) return profile.functionKeys?.[key] ?? {};
  return profile.layers[id] ?? {};
}
export function bindingAt(
  profile: Profile,
  layer: string,
  control: string,
): ProfileBinding {
  const entries = layerAt(profile, layer);
  const key = Object.keys(entries).find(
    (key) => canonicalControl(key) === canonicalControl(control),
  );
  return (
    ((key
      ? entries[key as keyof ProfileLayer]
      : undefined) as ProfileBinding) ?? {}
  );
}

/**
 * profile with the set of function key key replaced, or removed when it has
 * nothing; functionKeys goes when no set is left. A new functionKeys goes
 * after the layers (and common), an old one stays where the file had it.
 */
function withFunctionSet(
  profile: Profile,
  key: FunctionKey,
  set: Record<string, unknown> | undefined,
): Profile {
  const old = profile.functionKeys as Record<string, unknown> | undefined;
  const keep = !!set && Object.keys(set).length > 0;
  const sets: Record<string, unknown> = {};
  for (const [name, value] of Object.entries(old ?? {})) {
    if (name !== key) sets[name] = value;
    else if (keep) sets[name] = set;
  }
  if (keep && !(old && key in old)) sets[key] = set;
  const functionKeys = Object.keys(sets).length
    ? (sets as ProfileFunctionKeys)
    : undefined;
  const entries = Object.entries(profile);
  if (profile.functionKeys) {
    return Object.fromEntries(
      entries.flatMap(([name, value]) =>
        name !== "functionKeys"
          ? [[name, value]]
          : functionKeys
            ? [[name, functionKeys]]
            : [],
      ),
    ) as Profile;
  }
  if (!functionKeys) return profile;
  const after = profile.common ? "common" : "layers";
  return Object.fromEntries(
    entries.flatMap(([name, value]) =>
      name === after
        ? [
            [name, value],
            ["functionKeys", functionKeys],
          ]
        : [[name, value]],
    ),
  ) as Profile;
}

export function setBinding(
  profile: Profile,
  layer: string,
  control: string,
  binding: ProfileBinding | undefined,
): Profile {
  const next = structuredClone(profile);
  const entries = layerAt(next, layer) as Record<string, unknown>;
  const want = canonicalControl(control);
  const keep = !!binding && Object.keys(binding).length > 0;
  // Keep the control at its position so an edit and its undo leave the file as it was.
  const rebuilt: Record<string, unknown> = {};
  let placed = false;
  for (const [key, value] of Object.entries(entries)) {
    if (canonicalControl(key) !== want) {
      rebuilt[key] = value;
      continue;
    }
    if (keep && !placed) {
      rebuilt[want] = binding;
      placed = true;
    }
  }
  if (keep && !placed) rebuilt[want] = binding;
  const functionKey = functionKeyOf(layer);
  if (functionKey) return withFunctionSet(next, functionKey, rebuilt);
  if (layer === "common") next.common = rebuilt as ProfileLayer;
  else next.layers[layer] = rebuilt as ProfileLayer;
  return next;
}

/**
 * The profile as the mapping page edits it while a function key's set is
 * shown: that set stands where the base layer does, so the drawer, the
 * controller and the key list read and write it like any other, and the
 * normal bindings stand in the set's place (a set with nothing is absent).
 * It swaps, so it is its own inverse: edits made on the view go back with the
 * same call. The normal view is the profile itself.
 */
export function swapView(profile: Profile, view: KeyView): Profile {
  if (view === "normal") return profile;
  const set = profile.functionKeys?.[view];
  const swapped = {
    ...profile,
    layers: { ...profile.layers, base: (set ?? {}) as ProfileLayer },
  };
  return withFunctionSet(
    swapped,
    view,
    profile.layers.base as Record<string, unknown>,
  );
}

/** JSON with object keys sorted, so equality checks ignore key order. */
export function stableStringify(value: unknown): string {
  const sort = (item: unknown): unknown => {
    if (Array.isArray(item)) return item.map(sort);
    if (item && typeof item === "object")
      return Object.fromEntries(
        Object.keys(item)
          .sort()
          .map((key) => [key, sort((item as Record<string, unknown>)[key])]),
      );
    return item;
  };
  return JSON.stringify(sort(value)) ?? "undefined";
}

/**
 * An action still being filled in leaves out the parts that are empty. A
 * sequence leaves out its empty steps (a shortcut or a text not filled in
 * yet): when only shortcuts are left it is saved as a list of them, as the
 * editor writes it, and when nothing is left the gesture goes. A sequence
 * with nothing to leave out is saved as it is.
 */
function filledAction(action: ProfileAction): ProfileAction | null {
  if (action.type === "script") return action.name.trim() ? action : null;
  if (action.type === "text") return action.text ? action : null;
  if (action.type === "sequence") {
    const steps = action.steps.filter((step) => !isEmptyStep(step));
    if (steps.length === action.steps.length) return action;
    return steps.length ? stepsAction(steps, action) : null;
  }
  if (action.type !== "keys") return action;
  if (!Array.isArray(action.keys)) return action.keys.trim() ? action : null;
  const keys = action.keys.filter((chord) => chord.trim());
  if (!keys.length) return null;
  return keys.length === action.keys.length ? action : { ...action, keys };
}

/**
 * The draft as the editor saves it (UI-43): a part still being filled in
 * (a shortcut, a step of a sequence or a script name left empty) is left
 * out, and so is a gesture left with nothing and then a binding left
 * empty. The rest of the edits are saved meanwhile; the draft keeps the
 * empty parts until they are filled in. A draft read from a file of an
 * older format version (0) is saved at the current one (PRO-01). The four
 * feedback switches that moved to config.json (ADR-0067) are not written: the
 * daemon no longer reads them, so a save drops them, and a feedback object
 * left with nothing goes too. back and start are function keys (ADR-0069):
 * the bindings an older file has on them, in any layer or in common, no
 * longer work, so a save drops them (the daemon never rewrites a file by
 * itself), and functionKeys is written only when a set has bindings. A
 * profile based on another (PRO-19) is given that one as parent: a control the
 * edits leave with nothing, that the parent binds, is written as an empty
 * binding, which is how a profile says the key does nothing here. Left out,
 * the parent's binding would come back in the file, while the draft shows the
 * key as not set. draft is not changed.
 */
export function savedForm(
  draft: Profile,
  parent?: { id: string; profile: Profile } | null,
): Profile {
  const up =
    parent && draft.extends && parent.id === draft.extends
      ? parent.profile
      : undefined;
  const layer = (bindings: ProfileLayer | undefined, above?: ProfileLayer) => {
    if (!bindings) return bindings;
    const bound = bindingControls(above);
    let changed = false;
    const next: Record<string, unknown> = {};
    for (const [control, value] of Object.entries(bindings)) {
      if (isFunctionKey(control)) {
        changed = true;
        continue;
      }
      if (!value || typeof value !== "object") {
        next[control] = value;
        continue;
      }
      const binding = { ...(value as ProfileBinding) };
      let edited = false;
      for (const gesture of gestures) {
        const action = binding[gesture];
        if (!action) continue;
        const filled = filledAction(action);
        if (filled === action) continue;
        edited = true;
        if (filled) binding[gesture] = filled;
        else delete binding[gesture];
      }
      if (!edited) {
        next[control] = value;
        continue;
      }
      changed = true;
      if (Object.keys(binding).length) next[control] = binding;
      else if (bound.has(canonicalControl(control))) next[control] = {};
    }
    return changed ? (next as ProfileLayer) : bindings;
  };
  // A save rewrites the whole document, so a file of an older format
  // version (0, the previews') is written at the current one. A file that is
  // only opened is not saved, so it stays as it is until it is edited.
  let result =
    draft.schemaVersion === PROFILE_SCHEMA_VERSION
      ? draft
      : { ...draft, schemaVersion: PROFILE_SCHEMA_VERSION };
  const layers = Object.fromEntries(
    Object.entries(draft.layers).map(([name, value]) => [
      name,
      layer(value, up?.layers[name]),
    ]),
  ) as Profile["layers"];
  if (
    Object.entries(layers).some(([name, value]) => value !== draft.layers[name])
  )
    result = { ...result, layers };
  const common = layer(draft.common, up?.common);
  if (common !== draft.common) result = { ...result, common };
  const sets = savedFunctionKeys(draft.functionKeys, up?.functionKeys, layer);
  if (sets !== draft.functionKeys) {
    // In place, so the members keep their order in the file.
    result = Object.fromEntries(
      Object.entries(result).flatMap(([key, value]) =>
        key !== "functionKeys" ? [[key, value]] : sets ? [[key, sets]] : [],
      ),
    ) as Profile;
  }
  const feedback = withoutRetiredSwitches(draft.feedback);
  if (feedback !== draft.feedback) {
    // In place, so the members keep their order in the file.
    result = Object.fromEntries(
      Object.entries(result).flatMap(([key, value]) =>
        key !== "feedback" ? [[key, value]] : feedback ? [[key, feedback]] : [],
      ),
    ) as Profile;
  }
  return result;
}

/**
 * The controls a layer or set binds (it writes at least one setting for
 * them), by canonical name, whatever name the layer writes them under.
 */
function bindingControls(layer: ProfileLayer | undefined): Set<string> {
  const bound = new Set<string>();
  for (const [key, value] of Object.entries(layer ?? {}))
    if (
      key !== "color" &&
      !isFunctionKey(key) &&
      value &&
      typeof value === "object" &&
      Object.keys(value).length
    )
      bound.add(canonicalControl(key));
  return bound;
}

/**
 * functionKeys as a save writes it: each set cleaned like a layer (above is
 * the same set of the profile it is based on), a set with nothing left gone,
 * and the object itself gone when no set is left. The same object when
 * nothing changes.
 */
function savedFunctionKeys(
  sets: ProfileFunctionKeys | undefined,
  above: ProfileFunctionKeys | undefined,
  layer: (
    bindings: ProfileLayer | undefined,
    above?: ProfileLayer,
  ) => ProfileLayer | undefined,
): ProfileFunctionKeys | undefined {
  if (!sets) return sets;
  let changed = false;
  const next: Record<string, unknown> = {};
  for (const [key, set] of Object.entries(sets)) {
    const cleaned = layer(
      set as ProfileLayer,
      (above as Record<string, ProfileLayer | undefined> | undefined)?.[key],
    );
    if (cleaned !== set) changed = true;
    if (cleaned && Object.keys(cleaned).length) next[key] = cleaned;
    else changed = true;
  }
  if (!Object.keys(next).length) return undefined;
  return changed ? (next as ProfileFunctionKeys) : sets;
}

/** The members of a profile's feedback that config.json holds now (ADR-0067). */
const retiredFeedbackSwitches = [
  "rumble",
  "led",
  "pause",
  "lowBattery",
] as const;

/**
 * feedback without the retired switches: the same object when it has none,
 * undefined when nothing else is left.
 */
function withoutRetiredSwitches(
  feedback: ProfileFeedback | undefined,
): ProfileFeedback | undefined {
  if (!feedback || !retiredFeedbackSwitches.some((key) => key in feedback))
    return feedback;
  const rest = { ...feedback };
  for (const key of retiredFeedbackSwitches) delete rest[key];
  return Object.keys(rest).length ? rest : undefined;
}

/**
 * The precision action (MOU-05): while its gesture is held, every stick of
 * the profile that moves the pointer moves at this fraction of its speed. It
 * holds no key, so it only means something on a press or a hold.
 */
export type PrecisionAction = ProfileMouseAction & { op: "precision" };
/**
 * The key hints action (HUD-03): while its gesture is held, the overlay lists
 * what the other controls of the current layer do. It holds no key and has
 * nothing to set, so like precision it only means something on a press or a
 * hold.
 */
export type HintsAction = Extract<ProfileAction, { type: "hud" }>;
/** The jump action (MOU-08): puts the pointer somewhere. The file sets it. */
export type JumpAction = ProfileMouseAction & { op: "jump" };
/** The fraction the daemon uses when a precision action sets no factor. */
export const precisionDefault = 0.3;
/** The range of a precision factor, as a fraction and as a percentage. */
export const precisionRange = { min: 0.05, max: 1 } as const;
export const precisionPercentRange = { min: 5, max: 100 } as const;
export function isPrecisionAction(
  action: ProfileAction | undefined,
): action is PrecisionAction {
  return action?.type === "mouse" && action.op === "precision";
}
export function isHintsAction(
  action: ProfileAction | undefined,
): action is HintsAction {
  return action?.type === "hud" && action.op === "hints";
}
export function isJumpAction(
  action: ProfileAction | undefined,
): action is JumpAction {
  return action?.type === "mouse" && action.op === "jump";
}
/** A new key hints action. */
export function hintsAction(): HintsAction {
  return { type: "hud", op: "hints" };
}
/** A new precision action, at the default factor. */
export function precisionAction(): PrecisionAction {
  return { type: "mouse", op: "precision", factor: precisionDefault };
}
/** The factor of a precision action as a whole percentage, e.g. 30. */
export function precisionPercent(action: PrecisionAction): number {
  return Math.round((action.factor ?? precisionDefault) * 10000) / 100;
}
/** A fraction of the desktop as a percentage, e.g. 0.5 as 50. */
function desktopPercent(fraction: number | undefined): number {
  return Math.round((fraction ?? 0) * 10000) / 100;
}
/** The position a jump action puts the pointer at, in percent of the desktop. */
export function jumpPercent(action: ProfileMouseAction): {
  x: number;
  y: number;
} {
  return { x: desktopPercent(action.x), y: desktopPercent(action.y) };
}
/** Whether a gesture can hold an action: only then do precision and key hints mean something. */
export function canHoldAction(gesture: Gesture): boolean {
  return gesture === "press" || gesture === "hold";
}

export function actionSummary(action?: ProfileAction): string {
  if (!action) return "—";
  switch (action.type) {
    case "keys":
      return Array.isArray(action.keys) ? action.keys.join(" → ") : action.keys;
    case "script":
      return `${action.name}(${Object.entries(action.args ?? {})
        .map(([k, v]) => `${k}=${JSON.stringify(v)}`)
        .join(", ")})`;
    case "layer":
      return `${action.op} ${action.op === "cycle" ? action.layers.join(" → ") : action.layer}`;
    case "profile":
      return `profile: ${action.profile}`;
    case "macro":
      return `macro: ${action.file}`;
    case "mouse":
      if (isPrecisionAction(action))
        return `mouse precision ${precisionPercent(action)}%`;
      return `mouse jump ${jumpPercent(action).x}%,${jumpPercent(action).y}%`;
    case "hud":
      return "hud hints";
    case "sequence":
      return action.steps.map(stepSummary).join(" → ");
    case "text":
      return `"${shortText(action.text)}"`;
  }
}
/**
 * A step of a sequence action (MAP-22) in a few characters: its shortcut,
 * its wait in milliseconds, or its text, quoted and cut.
 */
export function stepSummary(step: ProfileSequenceStep): string {
  if (step.keys !== undefined) return step.keys;
  if (step.wait !== undefined) return `${step.wait} ms`;
  return `"${shortText(step.text ?? "")}"`;
}
/** A text cut to a few characters, on one line, for a summary. */
export function shortText(text: string, limit = 16): string {
  const chars = Array.from(text.replace(/\s+/g, " "));
  return chars.length <= limit
    ? text.replace(/\s+/g, " ")
    : `${chars.slice(0, limit).join("")}…`;
}
export function bindingSummary(binding: ProfileBinding): string {
  return binding.block
    ? "block"
    : gestures
        .filter((key) => binding[key])
        .map((key) => `${key}: ${actionSummary(binding[key])}`)
        .join(" · ") || "—";
}
/** Whether a chord is one the system keeps for itself (Win+L, Alt+Tab, …). */
export function isReservedShortcut(chord: string): boolean {
  const lower = chord.toLowerCase().replaceAll(" ", "");
  return (
    /(?:win|meta|super).*\+l$/.test(lower) ||
    (/ctrl/.test(lower) && /alt/.test(lower) && /(del|delete)$/.test(lower)) ||
    /alt\+tab$|alt\+f4$|ctrl\+shift\+esc(?:ape)?$/.test(lower)
  );
}
export function bindingDelays(
  binding: ProfileBinding,
  timing: ProfileTiming = {},
): {
  gesture: Gesture;
  ms: number;
  reason: "immediate" | "release" | "hold" | "multi";
}[] {
  const t = resolveTiming(timing, binding.timing);
  const max = binding.quadTap
    ? 4
    : binding.tripleTap
      ? 3
      : binding.doubleTap
        ? 2
        : 1;
  return gestures
    .filter((key) => binding[key])
    .map((gesture) => {
      const count =
        { tap: 1, doubleTap: 2, tripleTap: 3, quadTap: 4 }[gesture as "tap"] ??
        0;
      if (
        (gesture === "hold" && max > 1) ||
        (gesture === "hold" && binding.tap)
      )
        return { gesture, ms: t.holdMs, reason: "hold" };
      if (count && count < max)
        return { gesture, ms: t.multiTapMs, reason: "multi" };
      return {
        gesture,
        ms: 0,
        reason: gesture === "release" || count > 0 ? "release" : "immediate",
      };
    });
}
/** The only layer the GUI shows and edits (ADR-0045). */
export const BASE_LAYER = "base";

export function baseBinding(profile: Profile, control: string): ProfileBinding {
  return bindingAt(profile, BASE_LAYER, control);
}
export function setBaseBinding(
  profile: Profile,
  control: string,
  binding: ProfileBinding | undefined,
): Profile {
  return setBinding(profile, BASE_LAYER, control, binding);
}

/**
 * An action the GUI shows but cannot create: layer actions (ADR-0045),
 * profile switches (one app, one profile) and a legacy script's actions,
 * which the GUI no longer adds (SCR-50: keys run macros instead), and a
 * pointer jump (MOU-08), which only the file sets. It is locked in the editor
 * and saved exactly as the file has it. A sequence and a text (MAP-22, OUT-21)
 * are the step editor's own (UI-31), so they are not here.
 */
export type FileOnlyAction =
  | ProfileLayerAction
  | ProfileProfileAction
  | ProfileScriptAction
  | JumpAction;
export function isFileOnlyAction(
  action: ProfileAction | undefined,
): action is FileOnlyAction {
  return (
    action?.type === "layer" ||
    action?.type === "profile" ||
    action?.type === "script" ||
    isJumpAction(action)
  );
}

/** A profile's display name, or its ID when the library does not name it. */
export function profileName(
  id: string,
  profiles: { id: string; name?: string }[] = [],
): string {
  return profiles.find((entry) => entry.id === id)?.name || id;
}

/** What a profile file uses beyond the single layer the GUI edits. */
export interface LayerUsage {
  extraLayers: string[];
  commonBindings: number;
  defaultLayer: string | null;
  layerActions: number;
  blocks: number;
  switching: boolean;
  /**
   * Profile switch actions in any layer. They do not make the file
   * multi-layer (usesOnlyBase ignores them), but the editor cannot set them.
   */
  profileActions: number;
}
export function layerUsage(profile: Profile): LayerUsage {
  const usage: LayerUsage = {
    extraLayers: Object.keys(profile.layers).filter((id) => id !== BASE_LAYER),
    commonBindings: 0,
    defaultLayer:
      profile.defaultLayer && profile.defaultLayer !== BASE_LAYER
        ? profile.defaultLayer
        : null,
    layerActions: 0,
    blocks: 0,
    switching: false,
    profileActions: 0,
  };
  const layers: [string, ProfileLayer][] = [
    ...Object.entries(profile.layers),
    ...(profile.common
      ? [["common", profile.common] as [string, ProfileLayer]]
      : []),
  ];
  for (const [id, layer] of layers)
    for (const [key, binding] of Object.entries(layer)) {
      // back and start only switch sets: what an old file binds on them is
      // ignored by the daemon and dropped by the next save (ADR-0069).
      if (
        key === "color" ||
        isFunctionKey(key) ||
        typeof binding !== "object" ||
        !binding
      )
        continue;
      if (id === "common" && Object.keys(binding).length)
        usage.commonBindings++;
      if ((binding as ProfileBinding).block) usage.blocks++;
      for (const gesture of gestures) {
        const action = (binding as ProfileBinding)[gesture];
        if (action?.type === "profile") usage.profileActions++;
        if (action?.type !== "layer") continue;
        usage.layerActions++;
        if (action.op === "switch" || action.op === "cycle")
          usage.switching = true;
      }
    }
  return usage;
}
export function usesOnlyBase(usage: LayerUsage): boolean {
  return (
    !usage.extraLayers.length &&
    !usage.commonBindings &&
    !usage.defaultLayer &&
    !usage.layerActions &&
    !usage.blocks
  );
}

type Text = { zh: string; en: string };
const text = (language: string | undefined, value: Text) =>
  language === "en" ? value.en : value.zh;
const gestureNames: Record<Gesture, Text> = {
  press: { zh: "按下", en: "Press" },
  release: { zh: "松开", en: "Release" },
  tap: { zh: "点按", en: "Tap" },
  hold: { zh: "长按", en: "Hold" },
  doubleTap: { zh: "双击", en: "Double tap" },
  tripleTap: { zh: "三击", en: "Triple tap" },
  quadTap: { zh: "四击", en: "Quadruple tap" },
};
export function gestureName(gesture: Gesture, language?: string): string {
  return text(language, gestureNames[gesture]);
}

/**
 * A short human description of one action, for chips, lists and summaries.
 * A profile switch names its target by display name when `profiles` has it;
 * a macro is its name, or "not found" when `missing` lists its file.
 */
export function actionText(
  action: ProfileAction | undefined,
  language?: string,
  actions: { name: string; label?: string }[] = [],
  profiles: { id: string; name?: string }[] = [],
  missing: readonly string[] = [],
): string {
  if (!action) return "";
  switch (action.type) {
    case "keys":
      return mediaKeysText(action.keys, language);
    case "script":
      return (
        actions.find((entry) => entry.name === action.name)?.label ||
        action.name
      );
    case "profile": {
      const name = profileName(action.profile, profiles);
      return text(language, {
        zh: `切换到「${name}」`,
        en: `Switch to ${name}`,
      });
    }
    case "layer":
      return text(language, { zh: "切换按键组", en: "Layer action" });
    case "macro":
      return missing.includes(action.file)
        ? missingMacroText(action.file, language)
        : macroName(action.file);
    case "mouse": {
      if (isPrecisionAction(action))
        return text(language, {
          zh: `精确模式 ${precisionPercent(action)}%`,
          en: `Precision ${precisionPercent(action)}%`,
        });
      const { x, y } = jumpPercent(action);
      return text(language, {
        zh: `指针跳到屏幕位置 ${x}%，${y}%`,
        en: `Pointer jumps to ${x}%, ${y}%`,
      });
    }
    case "hud":
      return text(language, { zh: "按键提示", en: "Key hints" });
    case "sequence":
      return text(language, {
        zh: `${action.steps.length} 步序列`,
        en: `${action.steps.length}-step sequence`,
      });
    case "text": {
      const short = shortText(action.text);
      return text(language, {
        zh: `输入文字「${short}」`,
        en: `Type “${short}”`,
      });
    }
  }
}

/** A macro that bindings run but the macros folder lacks. */
export function missingMacroText(file: string, language?: string): string {
  const name = macroName(file);
  return text(language, { zh: `找不到「${name}」`, en: `“${name}” not found` });
}

/**
 * The daemon's "binding has no action" warning about a key of the base layer
 * (or of a function key's set) that has only its label: a key named but whose shortcut is not recorded
 * yet (a template leaves its keys so, OUT-18), or a stick click that holds
 * the stick's display name. The controller shows such a key as not set, so
 * the editor drops the warning.
 */
export function isLabelOnlyWarning(
  diagnostic: ProfileDiagnostic,
  profile: Profile | null,
): boolean {
  if (
    !profile ||
    diagnostic.severity !== "warning" ||
    diagnostic.message !== "binding has no action"
  )
    return false;
  const base = /^\/layers\/base\/([^/]+)$/.exec(diagnostic.pointer)?.[1];
  const inSet = /^\/functionKeys\/(back|start)\/([^/]+)$/.exec(
    diagnostic.pointer,
  );
  const key = base ?? inSet?.[2];
  if (key === undefined) return false;
  const binding = inSet
    ? bindingAt(
        profile,
        functionSetId(inSet[1] as FunctionKey),
        canonicalControl(key),
      )
    : baseBinding(profile, canonicalControl(key));
  return (
    !!binding.label?.trim() &&
    !binding.block &&
    usedGestures(binding).length === 0
  );
}

/** Gestures a binding uses, in the canonical order. */
export function usedGestures(binding: ProfileBinding): Gesture[] {
  return gestures.filter((gesture) => binding[gesture]);
}

/** Script registrations that apply to a control in the edited (base) layer. */
export function registrationsFor<
  T extends { control: string; scope: string; observe: boolean },
>(registrations: T[], control: string) {
  const key = canonicalControl(control);
  const matching = registrations.filter(
    (registration) => canonicalControl(registration.control) === key,
  );
  const visible = matching.filter(
    (registration) =>
      registration.scope === "global" ||
      registration.scope === BASE_LAYER ||
      registration.scope === `layer:${BASE_LAYER}`,
  );
  return {
    visible,
    owners: visible.filter((registration) => !registration.observe),
    otherScopes: matching.filter(
      (registration) => !visible.includes(registration),
    ),
  };
}
