// KeySpec chords as apps see them, for the shortcut table (OUT-18): sides of
// modifiers dropped, aliases folded, scan codes of F13–F24 named. Pure.
import type {
  ProfileAction,
  ProfileKeysAction,
} from "../shared/generated/schema";
import type { Gesture } from "./editor-model";

type Modifier = "Ctrl" | "Shift" | "Alt" | "Win";
const modifierOrder: readonly Modifier[] = ["Ctrl", "Shift", "Alt", "Win"];

const modifierNames = new Map<string, Modifier>(
  Object.entries({
    Ctrl: ["Ctrl", "LCtrl", "RCtrl", "Control", "ControlLeft", "ControlRight"],
    Shift: ["Shift", "LShift", "RShift", "ShiftLeft", "ShiftRight"],
    Alt: ["Alt", "LAlt", "RAlt", "AltLeft", "AltRight", "AltGr"],
    Win: ["Win", "LWin", "RWin", "Meta", "MetaLeft", "MetaRight"],
  }).flatMap(([mod, names]) =>
    names.map((name) => [name.toLowerCase(), mod as Modifier] as const),
  ),
);

const mainChars = [
  ..."ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
  ..."-=[];'`\\,./",
];
const functionKeys = Array.from({ length: 24 }, (_, i) => `F${i + 1}`);
/**
 * The mouse buttons (ADR-0062, MOU-02, MOU-11). Unlike the wheel keys they
 * have a held state: a hold binding keeps the button down, as it does a key.
 */
export const mouseButtons = [
  "MouseLeft",
  "MouseRight",
  "MouseMiddle",
  "MouseX1",
  "MouseX2",
] as const;
export type MouseButton = (typeof mouseButtons)[number];
/** The aliases of the buttons, as in daemon/internal/keys. */
const buttonAliases: Record<string, MouseButton> = {
  lbutton: "MouseLeft",
  rbutton: "MouseRight",
  mbutton: "MouseMiddle",
  xbutton1: "MouseX1",
  xbutton2: "MouseX2",
};
/**
 * The mouse wheel keys (ADR-0062): they send one notch when pressed and
 * nothing when released, so they can end a chord but never start one.
 */
export const wheelKeys = [
  "WheelUp",
  "WheelDown",
  "WheelLeft",
  "WheelRight",
] as const;
export type WheelKey = (typeof wheelKeys)[number];
/** Every mouse key a chord can end in. Like all non-modifiers, never a modifier. */
export const mouseKeys = [...mouseButtons, ...wheelKeys] as const;
export type MouseKey = (typeof mouseKeys)[number];
const mouseKeySet = new Set<string>(mouseKeys);
const mouseButtonSet = new Set<string>(mouseButtons);
/**
 * The volume and media keys (ADR-0070, OUT-20): keyboard keys the daemon sends
 * by virtual-key code, whatever the layout. Like every key but a modifier they
 * end a chord and never start one.
 */
export const mediaKeys = [
  "VolumeUp",
  "VolumeDown",
  "VolumeMute",
  "MediaPlayPause",
  "MediaNext",
  "MediaPrevious",
  "MediaStop",
] as const;
export type MediaKey = (typeof mediaKeys)[number];
/** The W3C code names the daemon accepts as aliases. */
const mediaAliases: Record<string, MediaKey> = {
  audiovolumeup: "VolumeUp",
  audiovolumedown: "VolumeDown",
  audiovolumemute: "VolumeMute",
  mediatracknext: "MediaNext",
  mediatrackprevious: "MediaPrevious",
};
const mediaKeySet = new Set<string>(mediaKeys);
const digits = [..."0123456789"];
// Canonical key names by lower-cased name or alias, as in daemon/internal/keys.
const keyNames = new Map<string, string>([
  ...[
    ...functionKeys,
    ...mouseKeys,
    ...mediaKeys,
    ...[
      "Esc",
      "Tab",
      "Space",
      "Enter",
      "Backspace",
      "Insert",
      "Delete",
      "Home",
      "End",
      "PageUp",
      "PageDown",
      "Up",
      "Down",
      "Left",
      "Right",
      "CapsLock",
      "NumLock",
      "ScrollLock",
      "PrintScreen",
      "Pause",
      "Menu",
      "NumAdd",
      "NumSubtract",
      "NumMultiply",
      "NumDivide",
      "NumDecimal",
      "NumEnter",
      "IntlBackslash",
      "IntlRo",
      "IntlYen",
      "Convert",
      "NonConvert",
      "KanaMode",
      "Plus",
    ],
    ...digits.map((digit) => `Num${digit}`),
    ...mainChars,
    ...mainChars.map((char) => `Key${char}`),
  ].map((name) => [name.toLowerCase(), name] as [string, string]),
  ...Object.entries(buttonAliases),
  ...Object.entries(mediaAliases),
  ...Object.entries({
    escape: "Esc",
    return: "Enter",
    ins: "Insert",
    del: "Delete",
    pgup: "PageUp",
    pgdn: "PageDown",
    arrowup: "Up",
    arrowdown: "Down",
    arrowleft: "Left",
    arrowright: "Right",
    prtsc: "PrintScreen",
    apps: "Menu",
    contextmenu: "Menu",
    numpadadd: "NumAdd",
    numpadsubtract: "NumSubtract",
    numpadmultiply: "NumMultiply",
    numpaddivide: "NumDivide",
    numpaddecimal: "NumDecimal",
    numpadenter: "NumEnter",
    minus: "Key-",
    equal: "Key=",
    bracketleft: "Key[",
    bracketright: "Key]",
    semicolon: "Key;",
    quote: "Key'",
    backquote: "Key`",
    backslash: "Key\\",
    comma: "Key,",
    period: "Key.",
    slash: "Key/",
  }),
  ...digits.flatMap((digit): [string, string][] => [
    [`numpad${digit}`, `Num${digit}`],
    [`digit${digit}`, `Key${digit}`],
  ]),
]);

// Scan codes of the function keys, so sc:0x64 counts as F13.
const functionScans = new Map<number, string>([
  ...functionKeys
    .slice(0, 10)
    .map((name, i) => [0x3b + i, name] as [number, string]),
  [0x57, "F11"],
  [0x58, "F12"],
  ...functionKeys
    .slice(12, 21)
    .map((name, i) => [0x64 + i, name] as [number, string]),
  [0x6d, "F22"],
  [0x6e, "F23"],
  [0x76, "F24"],
]);

function keyName(token: string): string | null {
  const scan = /^sc:0x([0-9a-f]{1,4})$/i.exec(token);
  if (scan) {
    const code = Number.parseInt(scan[1] as string, 16);
    const valid =
      (code >= 0x01 && code <= 0x7f) || (code >= 0xe001 && code <= 0xe07f);
    if (!valid) return null;
    return functionScans.get(code) ?? `sc:0x${code.toString(16).toUpperCase()}`;
  }
  return keyNames.get(token.toLowerCase()) ?? null;
}

interface Chord {
  mods: Set<Modifier>;
  /** The main key; null for a chord of modifiers only, such as "LCtrl". */
  key: string | null;
}

function parseChord(spec: string): Chord | null {
  const tokens = spec.split("+").map((token) => token.trim());
  const mods = new Set<Modifier>();
  for (const [index, token] of tokens.entries()) {
    if (!token) return null;
    const mod = modifierNames.get(token.toLowerCase());
    if (mod) {
      mods.add(mod);
      continue;
    }
    if (index !== tokens.length - 1) return null;
    const key = keyName(token);
    return key ? { mods, key } : null;
  }
  return mods.size ? { mods, key: null } : null;
}

function formatChord(chord: Chord): string {
  return [
    ...modifierOrder.filter((mod) => chord.mods.has(mod)),
    ...(chord.key ? [chord.key] : []),
  ].join("+");
}

/**
 * spec with its main key replaced by a mouse key: the modifiers stay as they
 * are written, once per kind ("LCtrl+Shift+Z" gives "LCtrl+Shift+WheelUp"); a
 * spec without any gives the mouse key alone.
 */
export function withMouseKey(spec: string, mouse: MouseKey): string {
  const seen = new Set<Modifier>();
  const mods: string[] = [];
  for (const token of spec.split("+").map((part) => part.trim())) {
    const mod = modifierNames.get(token.toLowerCase());
    if (!mod || seen.has(mod)) continue;
    seen.add(mod);
    mods.push(token);
  }
  return [...mods, mouse].join("+");
}

/**
 * The mouse key a key name stands for, by its name or an alias in any case
 * ("lbutton" is "MouseLeft"), or null for any other key.
 */
export function mouseKeyName(token: string): MouseKey | null {
  const name = keyNames.get(token.trim().toLowerCase());
  return name && mouseKeySet.has(name) ? (name as MouseKey) : null;
}

/**
 * The media key a key name stands for, by its name or a W3C alias in any case
 * ("audiovolumeup" is "VolumeUp"), or null for any other key.
 */
export function mediaKeyName(token: string): MediaKey | null {
  const name = keyNames.get(token.trim().toLowerCase());
  return name && mediaKeySet.has(name) ? (name as MediaKey) : null;
}

// The names of the volume and media keys, in Chinese and English.
const mediaLabels: Record<MediaKey, readonly [zh: string, en: string]> = {
  VolumeUp: ["音量加", "Volume up"],
  VolumeDown: ["音量减", "Volume down"],
  VolumeMute: ["静音", "Mute"],
  MediaPlayPause: ["播放/暂停", "Play/Pause"],
  MediaNext: ["下一首", "Next track"],
  MediaPrevious: ["上一首", "Previous track"],
  MediaStop: ["停止", "Stop"],
};

/** What a media key is called: Chinese, or English when language is "en". */
export function mediaKeyLabel(key: MediaKey, language?: string): string {
  return mediaLabels[key][language === "en" ? 1 : 0];
}

/**
 * A shortcut, or a list of them joined by " → ", as written except that its
 * volume and media keys carry their names ("Ctrl+音量加"): for words that
 * describe an action.
 */
export function mediaKeysText(
  keys: string | readonly string[],
  language?: string,
): string {
  const named = (chord: string) =>
    chord
      .split(/\+(?!$)/)
      .map((part) => {
        const media = mediaKeyName(part);
        return media ? mediaKeyLabel(media, language) : part;
      })
      .join("+");
  return (typeof keys === "string" ? [keys] : keys).map(named).join(" → ");
}

/** Whether the main key of the chord spec is a mouse button, not a wheel key. */
export function endsInMouseButton(spec: string): boolean {
  const key = parseChord(spec)?.key;
  return !!key && mouseButtonSet.has(key);
}

/**
 * A press or hold keys action whose chord ends in a mouse button must not
 * repeat in hold mode: a held button is never pressed again and again, and
 * the daemon refuses the combination (ADR-0062). Such a repeat becomes
 * repeated clicks: tap mode and the tap style. Any other action is returned
 * as it is. canHold: the gesture is press or hold, where hold is the default
 * mode.
 */
export function withoutHeldButtonRepeat(
  action: ProfileKeysAction,
  canHold: boolean,
): ProfileKeysAction {
  const { keys, repeat } = action;
  if (!repeat || typeof keys !== "string" || !endsInMouseButton(keys))
    return action;
  const mode =
    action.mode ?? (canHold && repeat.style !== "tap" ? "hold" : "tap");
  const typematic =
    repeat.style === "typematic" ||
    (repeat.style === undefined && mode === "hold");
  if (!typematic) return action;
  return { ...action, mode: "tap", repeat: { ...repeat, style: "tap" } };
}

/**
 * A KeySpec chord as apps see it: modifiers without sides in the order
 * Ctrl, Shift, Alt, Win, aliases and case folded, sc:0x64–0x76 as F13–F24.
 * "lctrl + sc:0x64" gives "Ctrl+F13". Null when spec is not a chord.
 */
export function normaliseChord(spec: string): string | null {
  const chord = parseChord(spec);
  return chord ? formatChord(chord) : null;
}

/** Whether spec is a chord of modifiers only, such as "LCtrl". */
export function isModifierOnly(spec: string): boolean {
  return parseChord(spec)?.key === null;
}

/** Whether keys (a chord or a sequence) use any of F13–F24. */
export function usesHighFunctionKey(keys: string | string[]): boolean {
  return (Array.isArray(keys) ? keys : [keys]).some((spec) => {
    const key = parseChord(spec)?.key;
    return !!key && /^F(1[3-9]|2[0-4])$/.test(key);
  });
}

/**
 * Whether an action keeps keys down while the button is held: a press or
 * hold gesture whose single chord is in hold mode, which it is by default.
 */
export function isHoldAction(
  gesture: Gesture,
  action: ProfileAction | undefined,
): boolean {
  if (action?.type !== "keys") return false;
  if (gesture !== "press" && gesture !== "hold") return false;
  if (Array.isArray(action.keys)) return false;
  return (
    (action.mode ?? (action.repeat?.style === "tap" ? "tap" : "hold")) ===
    "hold"
  );
}
