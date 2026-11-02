// Pure helpers for the settings of each controller (ADR-0017): the changes the
// Controllers page makes as the pads.set a controller needs, how a controller
// is named, and the function key of the controller the mapping page edits.

import type { DeviceEntry } from "../../shared/api";
import type {
  PadCombo,
  PadFunctionKeyName,
  PadInfo,
  PadSwitches,
  PadsListResult,
  PadsSetParams,
} from "../../shared/pads";
import { defaultPadFunctionKey } from "../controller/controller-model";
import { defaultFunctionKey, type FunctionKey } from "../editor/editor-model";
import type { Language } from "./store";

const tx = (language: Language, zh: string, en: string) =>
  language === "en" ? en : zh;

/** One change of the Controllers page to one controller. */
export interface PadChange {
  /** Chooses the function key (one of the controller's options). */
  functionKey?: PadFunctionKeyName;
  /** Turns one feedback switch on or off. */
  feedback?: { name: keyof PadSwitches; on: boolean };
  /** Sets the emergency combo, or with null goes back to the default. */
  panic?: PadCombo | null;
}

function sameCombo(a: PadCombo, b: PadCombo): boolean {
  return (
    a.holdMs === b.holdMs &&
    a.buttons.length === b.buttons.length &&
    a.buttons.every((button, i) => button === b.buttons[i])
  );
}

/**
 * The pads.set that makes the change on pad as the list has it now, or null
 * when the controller already has it. The file only says what is not the
 * default of the controller's family: the default function key goes as null
 * (the member is taken out), a switch is written only when it is off, and the
 * feedback object goes with its last member.
 */
export function padSetParams(
  pad: PadInfo,
  change: PadChange,
): PadsSetParams | null {
  const params: PadsSetParams = { key: pad.key };
  const { functionKey, feedback, panic } = change;
  if (functionKey !== undefined && functionKey !== pad.functionKey.effective)
    params.functionKey =
      functionKey === pad.functionKey.default ? null : functionKey;
  if (feedback && feedback.on !== pad.feedback.effective[feedback.name]) {
    const next: Partial<PadSwitches> = { ...pad.feedback.stored };
    if (feedback.on) delete next[feedback.name];
    else next[feedback.name] = false;
    params.feedback = Object.keys(next).length ? next : null;
  }
  if (panic === null) {
    // Going back to the default has something to do while the controller has
    // its own combo or the retired global one applies.
    if (pad.panic.source !== "default") params.panic = null;
  } else if (
    panic !== undefined &&
    (pad.panic.stored === undefined || !sameCombo(panic, pad.panic.stored))
  )
    params.panic = { buttons: [...panic.buttons], holdMs: panic.holdMs };
  return Object.keys(params).length > 1 ? params : null;
}

/**
 * The controller as it will be once the change is saved, for the page to show
 * the choice at once while the daemon writes it.
 */
export function padWithChange(pad: PadInfo, change: PadChange): PadInfo {
  let next = pad;
  if (change.functionKey)
    next = {
      ...next,
      functionKey: { ...next.functionKey, effective: change.functionKey },
    };
  if (change.feedback)
    next = {
      ...next,
      feedback: {
        ...next.feedback,
        effective: {
          ...next.feedback.effective,
          [change.feedback.name]: change.feedback.on,
        },
      },
    };
  if (change.panic !== undefined)
    next = {
      ...next,
      panic: { ...next.panic, effective: change.panic ?? next.panic.default },
    };
  return next;
}

/** The controller of the list that a connected one is. */
export function padOf(
  pads: PadsListResult | null,
  device: DeviceEntry | undefined,
): PadInfo | undefined {
  if (!pads || !device) return undefined;
  return (
    pads.pads.find((pad) => pad.device === device.info.id) ??
    pads.pads.find((pad) => pad.key === device.key)
  );
}

/**
 * The function key that applies to the controller: what the daemon says for
 * it, else (before the list is read) the default of its kind; Menu with no
 * controller at all.
 */
export function effectiveFunctionKey(
  pads: PadsListResult | null,
  device: DeviceEntry | undefined,
): FunctionKey {
  if (!device) return defaultFunctionKey;
  return (
    padOf(pads, device)?.functionKey.effective ??
    defaultPadFunctionKey(device.info.type)
  );
}

/** What the controller's kind is called in words. */
export function typeName(type: string, language: Language): string {
  switch (type) {
    case "xbox360":
      return "Xbox 360";
    case "xboxone":
      return "Xbox";
    case "ps3":
      return "PlayStation 3";
    case "ps4":
      return "PlayStation 4";
    case "ps5":
      return "PlayStation 5";
    case "switchpro":
      return "Switch Pro";
    case "gamecube":
      return "GameCube";
    case "joyconleft":
      return tx(language, "Joy-Con（左）", "Joy-Con (L)");
    case "joyconright":
      return tx(language, "Joy-Con（右）", "Joy-Con (R)");
    case "joyconpair":
      return tx(language, "Joy-Con（双）", "Joy-Con pair");
  }
  return tx(language, "通用手柄", "Generic controller");
}

/**
 * The kind of a controller in words. A single Joy-Con is always held upright,
 * so its kind names the side only.
 */
export function padKind(pad: PadInfo, language: Language): string {
  if (pad.family === "joyconPair") return typeName("joyconpair", language);
  return typeName(pad.type, language);
}

/** The name the list shows: the controller's own, else its kind. */
export function padTitle(pad: PadInfo, language: Language): string {
  return pad.name.trim() || padKind(pad, language);
}
