import type { ScriptRegistration } from "../shared/api";
import type {
  Profile,
  ProfileAction,
  ProfileBinding,
} from "../shared/generated/schema";
import { analogControlPart } from "./control-parts";
import {
  actionText,
  baseBinding,
  type Gesture,
  gestureName,
  isFileOnlyAction,
  isFunctionKey,
  registrationsFor,
  usedGestures,
} from "./editor-model";
import type { LabelDisplay, Language } from "./store";
import { chordKeys } from "./ui/Keycaps";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/**
 * Names that actions refer to: script actions by their label, profile
 * switches by the library's display name, as the drawer shows them.
 */
interface ActionNames {
  actions: { name: string; label?: string }[];
  profiles: { id: string; name?: string }[];
  /** Macro files bindings run that the macros folder lacks. */
  missing: readonly string[];
}
const describeAction = (
  action: ProfileAction | undefined,
  language: Language,
  names: ActionNames,
) => actionText(action, language, names.actions, names.profiles, names.missing);

/** One part of a chip's second line, e.g. "长按 [Shift][B]". */
export interface ChipSegment {
  /** Gesture shown before the action, e.g. "长按". */
  gesture?: string;
  /** A KeySpec shown as keycaps: one chord, or a sequence of chords. */
  keys?: string | string[];
  /** Separate chords side by side, e.g. the four stick directions. */
  chords?: string[];
  /** Shown between the chords, e.g. "/" between the two ticks of a knob. */
  divider?: string;
  /** Marks a script action. */
  script?: boolean;
  /** Marks a script (macro) a gesture runs (SCR-50), shown with its icon. */
  macro?: boolean;
  /** The macro's file is missing: a warning colour. */
  missing?: boolean;
  text?: string;
  /** The keys stay down while the control is held. */
  hold?: boolean;
}

/** What one controller chip shows (UI-39). */
export interface ChipInfo {
  /** Main text: the binding label, the action, or the control name. */
  title: string;
  /** Set when the title is a shortcut: shown as keycaps instead of `title`. */
  titleKeys?: ChipSegment;
  /** The second line of a full-size chip. */
  detail: ChipSegment[];
  /** `detail` as plain text, for tooltips. */
  summary: string;
  /**
   * unbound: nothing set; script: owned by a script; locked: only editable
   * in the file; function: View or Menu, which switch sets and bind nothing.
   */
  state: "bound" | "unbound" | "script" | "locked" | "function";
  scriptOwned: boolean;
  /**
   * The file has nothing at all for this control (not even a label). In the
   * set of a function key such a control does what it does otherwise.
   */
  empty?: boolean;
  /**
   * Nothing is set here, in the set of a function key: the chip shows what
   * the control does otherwise, greyed, under a note (ADR-0069).
   */
  inherited?: boolean;
  /**
   * The profile takes this chip's bindings from the profile it is based on,
   * and writes none of its own (PRO-19): the name of the profile that wrote
   * them. The chip looks like any other and carries a small mark.
   */
  fromParent?: string;
  /** A gesture runs a script whose file is missing (SCR-50). */
  missing?: boolean;
}

const stickDirections = ["up", "down", "left", "right"] as const;
const stickKeys = [
  ...stickDirections,
  "upleft",
  "upright",
  "downleft",
  "downright",
  "active",
];

/** Profile binding key for a callout control. */
export function bindingKey(control: string): string {
  return control === "lefttrigger"
    ? "lt"
    : control === "righttrigger"
      ? "rt"
      : control;
}

/** The rings of a stick (ANA-10) and the second stage of a trigger (ANA-14). */
function stageKeys(control: string): string[] {
  if (control === "leftstick" || control === "rightstick") {
    const group = control === "leftstick" ? "ls" : "rs";
    return [`${group}.inner`, `${group}.outer`];
  }
  if (control === "lefttrigger" || control === "righttrigger")
    return [`${bindingKey(control)}.full`];
  return [];
}

/** Raw half-axis keys a stick or trigger drawer edits, in display order. */
function rawKeys(control: string): string[] {
  if (control === "leftstick" || control === "rightstick") {
    const axis = control === "leftstick" ? "left" : "right";
    // The y axis grows downwards: y- is up.
    return [`${axis}y-`, `${axis}y+`, `${axis}x-`, `${axis}x+`];
  }
  if (control === "lefttrigger" || control === "righttrigger")
    return [`${control}+`, `${control}-`];
  return [];
}

/** Binding keys whose bindings and script registrations belong to a chip. */
export function relatedKeys(control: string): string[] {
  const key = bindingKey(control);
  if (control === "leftstick" || control === "rightstick") {
    const group = control === "leftstick" ? "ls" : "rs";
    return [
      key,
      ...stickKeys.map((direction) => `${group}.${direction}`),
      ...stageKeys(control),
      `${group}.cw`,
      `${group}.ccw`,
      ...rawKeys(control),
    ];
  }
  return [key, ...stageKeys(control), ...rawKeys(control)];
}

const rawDirections: Record<string, [string, string]> = {
  "y-": ["向上", "up"],
  "y+": ["向下", "down"],
  "x-": ["向左", "left"],
  "x+": ["向右", "right"],
};

/** How a chip names a raw half-axis key, as the stick and trigger drawers do. */
export function rawKeyName(key: string, language: Language): string {
  if (key.endsWith("trigger+")) return tx(language, "半按", "Half press");
  const [zh, en] = rawDirections[key.slice(-2)] ?? ["", ""];
  return zh
    ? tx(language, `原始轴${zh}`, `Raw ${en}`)
    : tx(language, "原始轴", "Raw axis");
}

/** A KeySpec as plain text, e.g. "Ctrl+Shift+Z" or "Ctrl+K → S". */
export function keyText(keys: string | string[], language?: Language): string {
  return (Array.isArray(keys) ? keys : [keys])
    .map((chord) => chordKeys(chord, language).join("+"))
    .join(" → ");
}

export function segmentText(segment: ChipSegment, language: Language): string {
  return [
    segment.gesture,
    segment.script || segment.macro ? tx(language, "脚本", "Script") : "",
    segment.keys ? keyText(segment.keys, language) : "",
    segment.chords
      ?.map((chord) => keyText(chord, language))
      .join(segment.divider ? ` ${segment.divider} ` : " "),
    segment.text,
    segment.hold ? tx(language, "按住", "held") : "",
  ]
    .filter(Boolean)
    .join(" ");
}

const modifiers = new Set(["Ctrl", "Shift", "Alt", "Win"]);

/**
 * Whether a keys action holds its keys for as long as the control is held.
 * An explicit hold mode says so; press and hold also hold by default, which
 * only matters to show for modifier keys.
 */
function holds(gesture: Gesture, action: ProfileAction): boolean {
  if (action.type !== "keys" || Array.isArray(action.keys)) return false;
  const keys = chordKeys(action.keys);
  // An empty chord (a shortcut cleared in the drawer) has no keys to hold;
  // every() would call it a modifier-only chord.
  if (!keys.length) return false;
  if (action.mode === "hold") return true;
  if (action.mode || action.repeat?.style === "tap") return false;
  return (
    (gesture === "press" || gesture === "hold") &&
    keys.every((key) => modifiers.has(key))
  );
}

function actionSegment(
  gesture: Gesture,
  action: ProfileAction,
  prefix: string | undefined,
  language: Language,
  names: ActionNames,
): ChipSegment {
  const base = prefix ? { gesture: prefix } : {};
  switch (action.type) {
    case "keys":
      return {
        ...base,
        keys: action.keys,
        ...(holds(gesture, action) ? { hold: true } : {}),
      };
    case "script":
      return {
        ...base,
        script: true,
        text: describeAction(action, language, names),
      };
    case "macro":
      return {
        ...base,
        macro: true,
        ...(names.missing.includes(action.file) ? { missing: true } : {}),
        text: describeAction(action, language, names),
      };
    default:
      return { ...base, text: describeAction(action, language, names) };
  }
}

/** Removes what an action does, keeping how it is triggered. */
function triggerOnly(segment: ChipSegment): ChipSegment | undefined {
  const { gesture, script, macro, missing, hold } = segment;
  const result: ChipSegment = {
    ...(gesture ? { gesture } : {}),
    ...(script ? { script } : {}),
    ...(macro ? { macro } : {}),
    ...(missing ? { missing } : {}),
    ...(hold ? { hold } : {}),
  };
  return Object.keys(result).length ? result : undefined;
}

/** What a control does, before choosing the display mode. */
interface Description {
  /** Generated title of the main action. */
  title: string;
  /** The main action, with its keys. */
  segment: ChipSegment;
  /** The keys of the main action, for the shortcut display mode. */
  keys?: ChipSegment;
  /** Whether a shortcut is clear enough to be the title on its own. */
  keysAsTitle: boolean;
  /** The other gestures. */
  extras: ChipSegment[];
}

function describeBinding(
  binding: ProfileBinding,
  language: Language,
  names: ActionNames,
  firstPrefix?: string,
): Description | undefined {
  const gestures = usedGestures(binding);
  const segments = gestures.flatMap((gesture, index) => {
    const action = binding[gesture];
    if (!action) return [];
    const prefix =
      index === 0
        ? (firstPrefix ??
          (gesture === "press" || gesture === "tap"
            ? undefined
            : gestureName(gesture, language)))
        : gestureName(gesture, language);
    return [actionSegment(gesture, action, prefix, language, names)];
  });
  const primary = gestures[0] ? binding[gestures[0]] : undefined;
  if (!primary || !segments.length) return undefined;
  const keys = primary.type === "keys" ? primary.keys : undefined;
  return {
    title: keys
      ? keyText(keys, language)
      : describeAction(primary, language, names),
    segment: segments[0],
    keys: keys ? { keys } : undefined,
    keysAsTitle: !!keys,
    extras: segments.slice(1),
  };
}

/** The primary action of a binding, as a list that is empty without one. */
function primary(binding: ProfileBinding): ProfileAction[] {
  const gesture = usedGestures(binding)[0];
  const action = gesture ? binding[gesture] : undefined;
  return action ? [action] : [];
}

/**
 * The rings of a stick and the second stage of a trigger that have keys,
 * each named as the rest of the editor names it (control-parts.ts).
 */
function stageSegments(
  profile: Profile,
  control: string,
  language: Language,
  names: ActionNames,
): ChipSegment[] {
  return stageKeys(control).flatMap((key) => {
    const part = describeBinding(
      baseBinding(profile, key),
      language,
      names,
      analogControlPart(key, language),
    );
    return part ? [part.segment, ...part.extras] : [];
  });
}

function describeStick(
  profile: Profile,
  control: string,
  language: Language,
  names: ActionNames,
): Description | undefined {
  const group = control === "leftstick" ? "ls" : "rs";
  const click = describeBinding(
    baseBinding(profile, control),
    language,
    names,
    tx(language, "按下摇杆", "Click"),
  );
  const clickSegments = click ? [click.segment, ...click.extras] : [];
  const stages = stageSegments(profile, control, language, names);
  const extras = [...clickSegments, ...stages];
  const clickOnly = click && { ...click, extras: [...click.extras, ...stages] };
  // A stick that moves the pointer sends no key (MOU-01): the chip says so,
  // and direction keys or ticks left in the file are not its summary.
  if (profile.analog?.[group]?.mode === "mouse") {
    const mouse = tx(language, "鼠标", "Mouse");
    return {
      title: mouse,
      segment: { text: mouse },
      keysAsTitle: false,
      extras,
    };
  }
  // A knob sends its two ticks and no direction (ANA-11): its keys are the
  // summary, and direction keys left in the file are not.
  if (profile.analog?.[group]?.mode === "knob") {
    const ticks = [`${group}.cw`, `${group}.ccw`].flatMap((key) =>
      primary(baseBinding(profile, key)),
    );
    if (!ticks.length) return clickOnly;
    const chords = ticks.flatMap((action) =>
      action.type === "keys" && !Array.isArray(action.keys)
        ? [action.keys]
        : [],
    );
    const plain = chords.length === ticks.length;
    return {
      title: tx(language, "旋钮", "Knob"),
      segment: plain
        ? { chords, divider: "/" }
        : actionSegment("press", ticks[0], undefined, language, names),
      keys: plain ? { chords, divider: "/" } : undefined,
      keysAsTitle: false,
      extras,
    };
  }
  const directions = stickDirections.map((direction) =>
    baseBinding(profile, `${group}.${direction}`),
  );
  const bound = stickKeys.some(
    (direction) =>
      usedGestures(baseBinding(profile, `${group}.${direction}`)).length > 0,
  );
  if (!bound) return clickOnly;
  const primaries = directions.flatMap(primary);
  const chords = primaries.flatMap((action) =>
    action.type === "keys" && !Array.isArray(action.keys) ? [action.keys] : [],
  );
  // Only diagonals or "any direction" set: name the first of those.
  const first =
    primaries[0] ??
    stickKeys.flatMap((direction) =>
      primary(baseBinding(profile, `${group}.${direction}`)),
    )[0];
  // A script or another action is shown as on a button: a macro with the
  // script icon, and its missing file as a warning.
  const segment: ChipSegment = chords.length
    ? { chords }
    : first?.type === "keys"
      ? { keys: first.keys }
      : first
        ? actionSegment("press", first, undefined, language, names)
        : { text: describeAction(first, language, names) };
  return {
    title: tx(language, "方向键", "Direction keys"),
    segment,
    keys: chords.length ? { chords } : undefined,
    keysAsTitle: false,
    extras,
  };
}

/**
 * A stick's rings and raw axes or a trigger's second stage and half press, for
 * a chip that has nothing else set: the first bound key leads, the others
 * follow it.
 */
function describeRaw(
  profile: Profile,
  control: string,
  language: Language,
  names: ActionNames,
): Description | undefined {
  const parts = [...stageKeys(control), ...rawKeys(control)].flatMap((key) => {
    const part = describeBinding(
      baseBinding(profile, key),
      language,
      names,
      analogControlPart(key, language) ?? rawKeyName(key, language),
    );
    return part ? [part] : [];
  });
  const [first, ...rest] = parts;
  if (!first) return undefined;
  return {
    ...first,
    extras: [
      ...first.extras,
      ...rest.flatMap((part) => [part.segment, ...part.extras]),
    ],
  };
}

/** The chip of View and Menu (UI-30): tagged, and nothing to edit. */
export function functionKeyChip(language: Language): ChipInfo {
  return {
    title: tx(language, "功能键", "Function key"),
    detail: [],
    summary: tx(
      language,
      "按住时其他键换成另一套",
      "While held, other keys switch to another set",
    ),
    state: "function",
    scriptOwned: false,
  };
}

/**
 * The chips of a function key's set: a control the set leaves out (its chip
 * is unset) shows what it does otherwise, in the unset look, with the note
 * "平时：" in front. own is the chips of the set; normal the chips of the
 * usual bindings; display is the display mode both were made with.
 */
export function inheritedChips({
  own,
  normal,
  display,
  language,
}: {
  own: Record<string, ChipInfo>;
  normal: Record<string, ChipInfo>;
  display: LabelDisplay;
  language: Language;
}): Record<string, ChipInfo> {
  const note = tx(language, "平时：", "Normal: ");
  // A segment's parts are separated by a space when read as text, so the note
  // alone has none at its end; in front of a gesture name it does (English).
  const noted = (segment: ChipSegment): ChipSegment => ({
    ...segment,
    gesture: segment.gesture
      ? note + segment.gesture
      : tx(language, "平时：", "Normal:"),
  });
  const result: Record<string, ChipInfo> = {};
  for (const [control, chip] of Object.entries(own)) {
    const usual = normal[control];
    // A binding with only a label owns the control (as in a layer), so only
    // a control the set does not mention does what it does otherwise.
    if (
      chip.state !== "unbound" ||
      !chip.empty ||
      !usual ||
      usual.state === "unbound" ||
      usual.state === "function"
    ) {
      result[control] = chip;
      continue;
    }
    // With the control's name as the title, the note goes before the action.
    const inDetail = display === "names" && usual.detail.length > 0;
    const detail = inDetail
      ? [noted(usual.detail[0]), ...usual.detail.slice(1)]
      : usual.detail;
    // What the usual chip takes from the profile's parent is about the usual
    // bindings, not about this set.
    const { fromParent: _fromParent, ...rest } = usual;
    result[control] = {
      ...rest,
      title: inDetail ? usual.title : note + usual.title,
      ...(usual.titleKeys && !inDetail
        ? { titleKeys: noted(usual.titleKeys) }
        : {}),
      detail,
      summary: inDetail
        ? detail.map((segment) => segmentText(segment, language)).join(" · ")
        : usual.summary,
      state: "unbound",
      inherited: true,
    };
  }
  return result;
}

export function controllerChips({
  profile,
  controls,
  labels,
  registrations,
  display,
  language,
  actions = [],
  profiles = [],
  missing = [],
  scriptEntry,
  fromParent,
}: {
  profile: Profile | null;
  controls: string[];
  labels: Record<string, string>;
  registrations: ScriptRegistration[];
  display: LabelDisplay;
  language: Language;
  actions?: { name: string; label?: string }[];
  /** Library profiles, to name the target of a profile switch. */
  profiles?: { id: string; name?: string }[];
  /** Macro files bindings run that the macros folder lacks (SCR-50). */
  missing?: readonly string[];
  /** Entry file of the profile's script, named when a script owns a control. */
  scriptEntry?: string;
  /**
   * The name of the profile a chip's binding keys are inherited from (the
   * profile writes none of them), or undefined (PRO-19). Asked for every chip
   * that holds bindings, with its binding keys.
   */
  fromParent?: (keys: string[]) => string | undefined;
}): Record<string, ChipInfo> {
  const unset = tx(language, "未设置", "Not set");
  const entry = scriptEntry?.split(/[\\/]/).pop();
  const owned = entry
    ? tx(language, `由脚本 ${entry} 接管`, `Handled by script ${entry}`)
    : tx(language, "由脚本接管", "Handled by a script");
  const names: ActionNames = { actions, profiles, missing };
  const result: Record<string, ChipInfo> = {};
  for (const control of controls) {
    // View and Menu switch sets and hold no bindings (ADR-0069).
    if (isFunctionKey(control)) {
      result[control] = functionKeyChip(language);
      continue;
    }
    const key = bindingKey(control);
    const keys = relatedKeys(control);
    const physical = labels[control] ?? control;
    const renamed = profile?.controlNames?.[key]?.trim() || undefined;
    const name = renamed ?? physical;
    // registrationsFor keeps only the base layer and global registrations.
    const owners = keys.flatMap(
      (related) => registrationsFor(registrations, related).owners,
    );
    const scriptOwned = owners.length > 0;
    if (!profile) {
      result[control] = {
        title: name,
        detail: [],
        summary: "",
        state: "unbound",
        scriptOwned,
      };
      continue;
    }
    const bindings = keys.map((related) => baseBinding(profile, related));
    const binding = bindings[0];
    const label = binding.label?.trim() || undefined;
    // Blocks, layer actions and profile switches are only editable in the file.
    const locked = bindings.some(
      (related) =>
        !!related.block ||
        usedGestures(related).some((gesture) =>
          isFileOnlyAction(related[gesture]),
        ),
    );
    const main =
      control === "leftstick" || control === "rightstick"
        ? describeStick(profile, control, language, names)
        : describeBinding(binding, language, names);
    // A trigger's second stage follows its first (ANA-14).
    const description =
      (main && (control === "lefttrigger" || control === "righttrigger")
        ? {
            ...main,
            extras: [
              ...main.extras,
              ...stageSegments(profile, control, language, names),
            ],
          }
        : main) ?? describeRaw(profile, control, language, names);
    const blocked = binding.block ? tx(language, "已屏蔽", "Blocked") : "";
    const inheritedFrom = fromParent?.(keys);
    const ownerTitle =
      owners.find((owner) => owner.label)?.label ??
      tx(language, "脚本", "Script");
    let title: string;
    let titleKeys: ChipSegment | undefined;
    let detail: ChipSegment[];
    if (display === "names") {
      title = name;
      detail = description
        ? [
            ...(label ? [{ text: label }] : []),
            description.segment,
            ...description.extras,
          ]
        : [{ text: blocked || (scriptOwned ? owned : unset) }];
    } else if (!description) {
      title = blocked
        ? (label ?? renamed ?? blocked)
        : scriptOwned
          ? (label ?? ownerTitle)
          : (label ?? unset);
      detail = blocked
        ? []
        : scriptOwned
          ? [{ text: owned }]
          : label
            ? [{ text: unset }]
            : [];
    } else if (display === "keys") {
      title = description.keys
        ? segmentText(description.keys, language)
        : description.title;
      titleKeys = description.keys;
      const trigger = triggerOnly(description.segment);
      const named = label ?? renamed;
      detail = [
        ...(trigger ? [trigger] : []),
        ...(named ? [{ text: named }] : []),
        ...description.extras,
      ];
    } else if (label ?? renamed) {
      title = label ?? renamed ?? "";
      detail = [description.segment, ...description.extras];
    } else if (description.keysAsTitle) {
      title = description.title;
      titleKeys = description.keys;
      const trigger = triggerOnly(description.segment);
      detail = [...(trigger ? [trigger] : []), ...description.extras];
    } else {
      title = description.title;
      detail = [description.segment, ...description.extras];
    }
    // Do not repeat the title on the second line (e.g. an unlabeled script action).
    detail = detail.flatMap((segment) => {
      if (segment.text !== title) return [segment];
      const rest = triggerOnly(segment);
      return rest ? [rest] : [];
    });
    result[control] = {
      title,
      ...(titleKeys ? { titleKeys } : {}),
      detail,
      summary: detail
        .map((segment) => segmentText(segment, language))
        .join(" · "),
      state: locked
        ? "locked"
        : description
          ? "bound"
          : scriptOwned
            ? "script"
            : "unbound",
      scriptOwned,
      ...(inheritedFrom ? { fromParent: inheritedFrom } : {}),
      ...(bindings.every((related) => Object.keys(related).length === 0)
        ? { empty: true }
        : {}),
      ...(description &&
      [description.segment, ...description.extras].some(
        (segment) => segment.missing,
      )
        ? { missing: true }
        : {}),
    };
  }
  return result;
}
