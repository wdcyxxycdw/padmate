// Starter templates for a new profile (PRO-18). Only the base layer, only
// press gestures, no layer or profile actions. Shortcuts the painting apps
// already have and that need no setup are written here (Ctrl+Z, Ctrl+T,
// Space held for the hand tool, held Ctrl/Shift/Alt). Every other action is
// only named: the user records its shortcut in the drawer (OUT-18).
import type {
  Profile,
  ProfileBinding,
  ProfileKeysAction,
  ProfileLayer,
} from "../shared/generated/schema";
import { PROFILE_SCHEMA_VERSION } from "../shared/profile-version";
import type { Language } from "./store";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

export type TemplateId = "standard" | "joycon";

export interface TemplateInfo {
  id: TemplateId;
  zh: string;
  en: string;
  /** The pad it is made for; a single Joy-Con is held sideways. */
  family: "standard" | "joyconSideways";
}

export const TEMPLATES: readonly TemplateInfo[] = [
  {
    id: "standard",
    zh: "标准手柄",
    en: "Standard controller",
    family: "standard",
  },
  {
    id: "joycon",
    zh: "单只 Joy-Con 横握",
    en: "Single Joy-Con, sideways",
    family: "joyconSideways",
  },
];

export function templateName(id: TemplateId, language: Language): string {
  const info = TEMPLATES.find((entry) => entry.id === id) as TemplateInfo;
  return tx(language, info.zh, info.en);
}

/** The name a template profile gets when the user has not typed one. */
export function templateProfileName(language: Language): string {
  return tx(language, "绘画", "Painting");
}

/**
 * The template that suits a pad: a single Joy-Con held sideways (it reports
 * face buttons, as in controllerLabels) gets the Joy-Con one, any other pad
 * or none the standard one.
 */
export function templateForDevice(device?: {
  type: string;
  buttonMask?: number;
}): TemplateId {
  const joycon =
    device?.type === "joyconleft" || device?.type === "joyconright";
  return joycon &&
    device?.buttonMask !== undefined &&
    (device.buttonMask & 1) !== 0
    ? "joycon"
    : "standard";
}

interface ActionDef {
  zh: string;
  en: string;
  /** Keys that need no setup; without them the key only has its label. */
  keys?: string;
  hold?: boolean;
}

const actions = {
  undo: { zh: "撤销", en: "Undo", keys: "Ctrl+Z" },
  redo: { zh: "重做", en: "Redo" },
  brush: { zh: "画笔", en: "Brush" },
  eraser: { zh: "橡皮", en: "Eraser" },
  brushSmaller: { zh: "笔刷缩小", en: "Smaller brush" },
  brushLarger: { zh: "笔刷放大", en: "Larger brush" },
  zoomIn: { zh: "画布放大", en: "Zoom in" },
  zoomOut: { zh: "画布缩小", en: "Zoom out" },
  // Space is the hand tool in Krita, CSP, Photoshop and SAI without setup.
  pan: { zh: "抓手", en: "Pan", keys: "Space", hold: true },
  transform: { zh: "自由变换", en: "Free transform", keys: "Ctrl+T" },
  newLayer: { zh: "新建图层", en: "New layer" },
  flip: { zh: "水平翻转画布", en: "Flip canvas horizontally" },
  // Labelled by the key: what a held modifier does differs between apps.
  ctrl: { zh: "Ctrl", en: "Ctrl", keys: "Ctrl", hold: true },
  shift: { zh: "Shift", en: "Shift", keys: "Shift", hold: true },
  alt: { zh: "Alt", en: "Alt", keys: "Alt", hold: true },
} satisfies Record<string, ActionDef>;
type ActionId = keyof typeof actions;

const stick: [string, ActionId][] = [
  ["ls.left", "brushSmaller"],
  ["ls.right", "brushLarger"],
  ["ls.up", "zoomIn"],
  ["ls.down", "zoomOut"],
];

// Left hand first. Neither emergency pause pair (back+start; SL+SR on a
// sideways Joy-Con) is held, so holding modifiers never pauses padmate.
const layouts: Record<TemplateId, [string, ActionId][]> = {
  standard: [
    ["lt", "pan"],
    ["leftshoulder", "ctrl"],
    ["dpad.left", "undo"],
    ["dpad.right", "redo"],
    ["dpad.up", "brush"],
    ["dpad.down", "eraser"],
    ...stick,
    ["rightshoulder", "shift"],
    ["rt", "alt"],
    ["south", "transform"],
    ["north", "newLayer"],
    ["east", "flip"],
  ],
  // The same for a left and a right Joy-Con. SL and SR are leftshoulder and
  // rightshoulder; leftstick is the stick press (start, − or +, is a function key).
  joycon: [
    ...stick,
    ["south", "brush"],
    ["east", "eraser"],
    ["west", "pan"],
    ["north", "ctrl"],
    ["leftshoulder", "undo"],
    ["rightshoulder", "redo"],
    ["leftstick", "flip"],
  ],
};

function binding(def: ActionDef, language: Language): ProfileBinding {
  const label = tx(language, def.zh, def.en);
  if (!def.keys) return { label };
  const press: ProfileKeysAction = {
    type: "keys",
    keys: def.keys,
    mode: def.hold ? "hold" : "tap",
  };
  return { label, press };
}

/**
 * A new profile from a template. Keys whose shortcut the user sets have only
 * their label, which the controller shows until a shortcut is recorded.
 */
export function templateProfile(
  id: TemplateId,
  name: string,
  language: Language,
): Profile {
  const base: Record<string, ProfileBinding> = {};
  for (const [control, action] of layouts[id])
    base[control] = binding(actions[action], language);
  return {
    schemaVersion: PROFILE_SCHEMA_VERSION,
    name,
    // Diagonals would send two steps at once.
    analog: { ls: { mode: "fourWay" } },
    layers: { base: base as ProfileLayer },
  };
}

/** The profile document for a new profile from a template. */
export function templateText(
  id: TemplateId,
  name: string,
  language: Language,
): string {
  return `${JSON.stringify(templateProfile(id, name, language), null, 2)}\n`;
}
