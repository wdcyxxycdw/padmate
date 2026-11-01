import type { FunctionKey } from "../editor/editor-model";
import type { Language, PadInput } from "../state/store";

export type ControllerFamily = "xbox" | "playstation" | "nintendo" | "generic";
export type FaceControl = "south" | "east" | "west" | "north";

const printedSymbols: Record<string, string> = {
  Cross: "×",
  Circle: "○",
  Square: "□",
  Triangle: "△",
};

export function familyName(type: string): ControllerFamily {
  if (type.startsWith("xbox")) return "xbox";
  if (/^ps[345]$/.test(type)) return "playstation";
  if (type.startsWith("joycon") || type === "switchpro" || type === "gamecube")
    return "nintendo";
  return "generic";
}

export function faceLabels(
  type: string,
  reported: Record<string, string> = {},
): Record<FaceControl, string> {
  const family = familyName(type);
  const labels =
    family === "xbox"
      ? ["A", "B", "X", "Y"]
      : family === "playstation"
        ? ["×", "○", "□", "△"]
        : family === "nintendo" &&
            type !== "joyconleft" &&
            type !== "joyconright"
          ? ["B", "A", "Y", "X"]
          : ["S", "E", "W", "N"];
  return Object.fromEntries(
    ["south", "east", "west", "north"].map((control, index) => [
      control,
      printedSymbols[reported[control] ?? ""] ||
        reported[control] ||
        labels[index],
    ]),
  ) as Record<FaceControl, string>;
}

/**
 * The names of the four paddles (ARCHITECTURE §7.1; SDL's RIGHT_PADDLE1,
 * LEFT_PADDLE1, RIGHT_PADDLE2, LEFT_PADDLE2): a single Joy-Con's are L and
 * ZL, or R and ZR, held sideways, and SL and SR upright; a pair of Joy-Cons
 * has the SL and SR of both sides; a DualSense Edge has its back buttons and
 * Fn buttons. Other pads number them as the Xbox Elite does: 背键 1 and 2
 * under the right hand, 3 and 4 under the left. Go's padlabel package has
 * the same table (its test compares the two).
 */
function paddleLabels(type: string, sideways: boolean): Record<string, string> {
  if (type === "joyconleft")
    return sideways
      ? { paddle2: "L", paddle4: "ZL" }
      : { paddle2: "SL", paddle4: "SR" };
  if (type === "joyconright")
    return sideways
      ? { paddle1: "R", paddle3: "ZR" }
      : { paddle1: "SR", paddle3: "SL" };
  if (type === "joyconpair")
    return {
      paddle1: "右 SR",
      paddle2: "左 SL",
      paddle3: "右 SL",
      paddle4: "左 SR",
    };
  if (familyName(type) === "playstation")
    return {
      paddle1: "右背键",
      paddle2: "左背键",
      paddle3: "右 Fn",
      paddle4: "左 Fn",
    };
  return {
    paddle1: "背键 1",
    paddle2: "背键 3",
    paddle3: "背键 2",
    paddle4: "背键 4",
  };
}

/**
 * The name of misc button index: misc1 is the Share button of an Xbox
 * Series pad, the mute button of a DualSense and the Capture button of a
 * Nintendo pad (SDL_gamepad.h); misc2 is the C button of a Nintendo Switch 2
 * pad (a Joy-Con 2 or the Pro Controller 2), printed C, 'C 键' in Chinese;
 * the others are numbered.
 */
function miscLabel(
  family: ControllerFamily,
  index: number,
  language: Language,
): string {
  if (index === 1) {
    if (family === "xbox") return "Share";
    if (family === "playstation") return "Mute";
    if (family === "nintendo") return "Capture";
  }
  if (index === 2 && family === "nintendo")
    return language === "en" ? "C" : "C 键";
  return `附加键 ${index}`;
}

/**
 * Whether a pad is a single Joy-Con held sideways: it reports face buttons
 * (buttonMask bit 0) but no right stick (bit 8), which a right one held
 * upright has with its face buttons, and its SL and SR are the shoulders.
 */
export function isSidewaysJoyCon(type: string, buttonMask?: number): boolean {
  return (
    (type === "joyconleft" || type === "joyconright") &&
    buttonMask !== undefined &&
    (buttonMask & 1) !== 0 &&
    (buttonMask & (1 << 8)) === 0
  );
}

// SDL's gamepad buttons and axes by their bit in DeviceInfo.buttonMask and
// axisMask: the SDL_GamepadButton and SDL_GamepadAxis order
// (internal/input/controls.go).
const sdlButtons = [
  "south",
  "east",
  "west",
  "north",
  "back",
  "guide",
  "start",
  "leftstick",
  "rightstick",
  "leftshoulder",
  "rightshoulder",
  "dpad.up",
  "dpad.down",
  "dpad.left",
  "dpad.right",
  "misc1",
  "paddle1",
  "paddle2",
  "paddle3",
  "paddle4",
  "touchpad",
  "misc2",
  "misc3",
  "misc4",
  "misc5",
  "misc6",
];
const sdlAxes = [
  "leftx",
  "lefty",
  "rightx",
  "righty",
  "lefttrigger",
  "righttrigger",
];
/** The controls a pad has with each of them: a stick's axes or its click. */
const controlParts: Record<string, { buttons: string[]; axes: string[] }> = {
  leftstick: { buttons: ["leftstick"], axes: ["leftx", "lefty"] },
  rightstick: { buttons: ["rightstick"], axes: ["rightx", "righty"] },
  lefttrigger: { buttons: [], axes: ["lefttrigger"] },
  righttrigger: { buttons: [], axes: ["righttrigger"] },
};

/** What decides the controls of a pad: DeviceInfo's type and masks. */
export interface PadControls {
  type: string;
  buttonMask?: number;
  axisMask?: number;
}

/**
 * Whether a pad has a control: a button, a trigger, or a stick (its axes or
 * its click), as SDL maps them for it (buttonMask, axisMask). While SDL has
 * not said (both 0 or missing), a pad has the standard controls of SDL's
 * gamepad, and a DualShock 4 or DualSense its touchpad; the extra buttons
 * (misc, paddles) only show once SDL reports them.
 */
export function padHasControl(control: string, pad: PadControls): boolean {
  const parts = controlParts[control] ?? {
    buttons: sdlButtons.includes(control) ? [control] : [],
    axes: [],
  };
  if (!pad.buttonMask && !pad.axisMask) {
    if (control === "touchpad") return pad.type === "ps4" || pad.type === "ps5";
    return (
      parts.axes.length > 0 ||
      parts.buttons.some((button) => sdlButtons.indexOf(button) < 15)
    );
  }
  const buttons = pad.buttonMask ?? 0;
  const axes = pad.axisMask ?? 0;
  return (
    parts.buttons.some(
      (button) => (buttons & (1 << sdlButtons.indexOf(button))) !== 0,
    ) || parts.axes.some((axis) => (axes & (1 << sdlAxes.indexOf(axis))) !== 0)
  );
}

export function controllerLabels(
  type: string,
  reported: Record<string, string> = {},
  buttonMask?: number,
  language: Language = "zh-CN",
): Record<string, string> {
  const family = familyName(type);
  const common = {
    ...faceLabels(type, reported),
    "dpad.up": "↑",
    "dpad.down": "↓",
    "dpad.left": "←",
    "dpad.right": "→",
    leftshoulder: "L",
    rightshoulder: "R",
    lefttrigger: "LT",
    righttrigger: "RT",
    back: "Back",
    start: "Start",
    guide: "Home",
    leftstick: "LS",
    rightstick: "RS",
    touchpad: "Touch",
  };
  const familyLabels =
    family === "playstation"
      ? {
          leftshoulder: "L1",
          rightshoulder: "R1",
          lefttrigger: "L2",
          righttrigger: "R2",
          leftstick: "L3",
          rightstick: "R3",
          back: type === "ps3" ? "Select" : type === "ps4" ? "Share" : "Create",
          start: type === "ps3" ? "Start" : "Options",
          guide: "PS",
        }
      : family === "xbox"
        ? {
            leftshoulder: "LB",
            rightshoulder: "RB",
            lefttrigger: "LT",
            righttrigger: "RT",
            back: type === "xbox360" ? "Back" : "View",
            start: type === "xbox360" ? "Start" : "Menu",
            guide: "Xbox",
          }
        : family === "nintendo"
          ? {
              leftshoulder: "L",
              rightshoulder: "R",
              lefttrigger: "ZL",
              righttrigger: "ZR",
              back: "−",
              start: "+",
              guide: "Home",
            }
          : {};
  // A single Joy-Con's shoulder names depend on its actual mapped controls.
  const sideways = isSidewaysJoyCon(type, buttonMask);
  const joyConLabels = sideways
    ? {
        leftshoulder: "SL",
        rightshoulder: "SR",
        start: type === "joyconleft" ? "−" : "+",
        guide: type === "joyconleft" ? "Capture" : "Home",
      }
    : {};
  return {
    ...common,
    ...familyLabels,
    ...joyConLabels,
    ...paddleLabels(type, sideways),
    ...Object.fromEntries(
      [1, 2, 3, 4, 5, 6].map((index) => [
        `misc${index}`,
        miscLabel(family, index, language),
      ]),
    ),
    ...Object.fromEntries(
      Object.entries(reported)
        .filter(([, label]) => label)
        .map(([control, label]) => [control, printedSymbols[label] || label]),
    ),
  };
}

/**
 * Which of the two keys a function key can be (back, start) a pad has
 * (ADR-0012). A single Joy-Con held sideways has no back, only − or
 * +. A pad without the function key has no function key: its other keys work
 * as usual.
 */
export function padFunctionKeys(
  type: string,
  buttonMask?: number,
): FunctionKey[] {
  return isSidewaysJoyCon(type, buttonMask) ? ["start"] : ["start", "back"];
}

/** Whether the pad has the function key. */
export function padHasFunctionKey(
  key: FunctionKey,
  type: string,
  buttonMask?: number,
): boolean {
  return padFunctionKeys(type, buttonMask).includes(key);
}

/**
 * What the pad prints on the function key, e.g. Menu on an Xbox pad, Options
 * on a DualSense, + on a Switch pad; View, Create or − for the other of the
 * two.
 */
export function functionKeyLabel(
  key: FunctionKey,
  type: string,
  reported: Record<string, string> = {},
  buttonMask?: number,
): string {
  return controllerLabels(type, reported, buttonMask)[key] ?? key;
}

export function axisPosition(value = 0): number {
  if (!Number.isFinite(value)) return 0;
  return Math.max(-1, Math.min(1, value / (value < 0 ? 32768 : 32767)));
}

/** A trigger's pull, 0–1. */
export function triggerValue(value = 0) {
  return Number.isFinite(value) ? Math.max(0, Math.min(1, value / 32767)) : 0;
}

/** Below this push a stick reads as resting, which also hides small drift. */
export const stickActiveThreshold = 0.12;

/**
 * A stick's position inside the unit circle: a square diagonal is pulled back
 * to the rim. +y is down, as SDL reports it.
 */
export function stickDeflection(input: PadInput | undefined, side: string) {
  let x = axisPosition(input?.axes[`${side}x`]);
  let y = axisPosition(input?.axes[`${side}y`]);
  const length = Math.hypot(x, y);
  if (length > 1) {
    x /= length;
    y /= length;
  }
  return { x, y, length: Math.min(1, length) };
}

/**
 * How far a stick (leftstick or rightstick) is pushed, 0–1, and 0 while it
 * rests: below the threshold a drifting cap does not count. The click is not
 * part of it.
 */
export function stickPush(control: string, input?: PadInput): number {
  const { length } = stickDeflection(input, control.slice(0, -5));
  return length >= stickActiveThreshold ? length : 0;
}

/**
 * How strongly a control is in use, 0–1, for its chip and its place on the
 * model: a trigger's pull, a stick's push once past the rest threshold, and 1
 * for a pressed button (a clicked stick counts as pressed).
 */
export function controlStrength(control: string, input?: PadInput): number {
  if (control.endsWith("trigger")) return triggerValue(input?.axes[control]);
  if (input?.buttons[control]) return 1;
  if (control === "leftstick" || control === "rightstick")
    return stickPush(control, input);
  return 0;
}
