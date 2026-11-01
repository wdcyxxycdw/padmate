// The answers of the daemon's pads.list, pads.set and pads.migrate (ADR-0017),
// checked field by field. Main checks the answer of a call with them, so a
// reply of the wrong shape is refused in one place and the settings page only
// ever sees the types of pads.ts.

import type {
  PadCombo,
  PadFamily,
  PadFeedback,
  PadFunctionKey,
  PadFunctionKeyName,
  PadInfo,
  PadLegacy,
  PadPanic,
  PadSource,
  PadSwitches,
  PadsListResult,
  PadsMigrateResult,
  PadsSaved,
  PadsSetResult,
} from "./pads";

/** More than the daemon lists (64 remembered, plus the connected) is no state of it. */
const MAX_PADS = 256;
const MAX_OPTIONS = 6;
const MAX_COMBO_BUTTONS = 8;

const SOURCES: readonly string[] = ["device", "legacy", "default"];
const FAMILIES: readonly string[] = [
  "standard",
  "joyconLeft",
  "joyconRight",
  "joyconLeftVertical",
  "joyconRightVertical",
  "joyconPair",
  "unknown",
];
/** The buttons that can be a function key (never guide). */
export const PAD_FUNCTION_KEYS: readonly PadFunctionKeyName[] = [
  "start",
  "back",
  "paddle1",
  "paddle2",
  "paddle3",
  "paddle4",
];
const SWITCHES = ["rumble", "led", "pause", "lowBattery"] as const;

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function isSource(value: unknown): value is PadSource {
  return typeof value === "string" && SOURCES.includes(value);
}

function isFamily(value: unknown): value is PadFamily {
  return typeof value === "string" && FAMILIES.includes(value);
}

export function isPadFunctionKey(value: unknown): value is PadFunctionKeyName {
  return (
    typeof value === "string" &&
    (PAD_FUNCTION_KEYS as readonly string[]).includes(value)
  );
}

function wholeNumber(value: unknown): value is number {
  return typeof value === "number" && Number.isInteger(value) && value >= 0;
}

function combo(value: unknown): PadCombo | null {
  if (!isRecord(value)) return null;
  const buttons = value.buttons;
  if (
    !Array.isArray(buttons) ||
    buttons.length > MAX_COMBO_BUTTONS ||
    buttons.some((button) => typeof button !== "string") ||
    !wholeNumber(value.holdMs)
  )
    return null;
  return { buttons: [...(buttons as string[])], holdMs: value.holdMs };
}

function functionKey(value: unknown): PadFunctionKey | null {
  if (!isRecord(value)) return null;
  const options = value.options;
  if (
    !isPadFunctionKey(value.default) ||
    !isPadFunctionKey(value.effective) ||
    !isSource(value.source) ||
    (value.stored !== undefined && !isPadFunctionKey(value.stored)) ||
    !Array.isArray(options) ||
    options.length === 0 ||
    options.length > MAX_OPTIONS
  )
    return null;
  const list: PadFunctionKey["options"] = [];
  for (const option of options) {
    if (
      !isRecord(option) ||
      !isPadFunctionKey(option.control) ||
      typeof option.label !== "string"
    )
      return null;
    list.push({ control: option.control, label: option.label });
  }
  return {
    default: value.default,
    options: list,
    ...(value.stored === undefined ? {} : { stored: value.stored }),
    effective: value.effective,
    source: value.source,
  };
}

function switches(value: unknown): PadSwitches | null {
  if (!isRecord(value)) return null;
  const out: Partial<PadSwitches> = {};
  for (const name of SWITCHES) {
    const on = value[name];
    if (typeof on !== "boolean") return null;
    out[name] = on;
  }
  return out as PadSwitches;
}

function feedback(value: unknown): PadFeedback | null {
  if (!isRecord(value) || !isSource(value.source)) return null;
  const effective = switches(value.effective);
  if (!effective) return null;
  let stored: Partial<PadSwitches> | undefined;
  if (value.stored !== undefined) {
    if (!isRecord(value.stored)) return null;
    stored = {};
    for (const name of SWITCHES) {
      const on = value.stored[name];
      if (on === undefined) continue;
      if (typeof on !== "boolean") return null;
      stored[name] = on;
    }
  }
  return {
    ...(stored ? { stored } : {}),
    effective,
    source: value.source,
  };
}

function panic(value: unknown): PadPanic | null {
  if (!isRecord(value) || !isSource(value.source)) return null;
  const fallback = combo(value.default);
  const effective = combo(value.effective);
  const stored = value.stored === undefined ? undefined : combo(value.stored);
  if (!fallback || !effective || stored === null) return null;
  return {
    default: fallback,
    ...(stored ? { stored } : {}),
    effective,
    source: value.source,
  };
}

function pad(value: unknown): PadInfo | null {
  if (!isRecord(value)) return null;
  const key = functionKey(value.functionKey);
  const switchesOf = feedback(value.feedback);
  const panicOf = panic(value.panic);
  if (
    typeof value.key !== "string" ||
    !value.key ||
    typeof value.name !== "string" ||
    typeof value.type !== "string" ||
    !wholeNumber(value.buttonMask) ||
    typeof value.known !== "boolean" ||
    typeof value.connected !== "boolean" ||
    (value.device !== undefined && !wholeNumber(value.device)) ||
    typeof value.ignored !== "boolean" ||
    (value.lastSeen !== undefined && !wholeNumber(value.lastSeen)) ||
    !isFamily(value.family) ||
    !key ||
    !switchesOf ||
    !panicOf
  )
    return null;
  return {
    key: value.key,
    name: value.name,
    type: value.type,
    buttonMask: value.buttonMask,
    known: value.known,
    connected: value.connected,
    ...(value.device === undefined ? {} : { device: value.device }),
    ignored: value.ignored,
    ...(value.lastSeen === undefined ? {} : { lastSeen: value.lastSeen }),
    family: value.family,
    functionKey: key,
    feedback: switchesOf,
    panic: panicOf,
  };
}

function pads(value: unknown): PadInfo[] | null {
  if (!Array.isArray(value) || value.length > MAX_PADS) return null;
  const out: PadInfo[] = [];
  for (const entry of value) {
    const checked = pad(entry);
    if (!checked) return null;
    out.push(checked);
  }
  return out;
}

function legacy(value: unknown): PadLegacy | null {
  if (
    !isRecord(value) ||
    typeof value.any !== "boolean" ||
    typeof value.functionKey !== "boolean" ||
    typeof value.feedback !== "boolean" ||
    typeof value.panic !== "boolean"
  )
    return null;
  return {
    any: value.any,
    functionKey: value.functionKey,
    feedback: value.feedback,
    panic: value.panic,
  };
}

function saved(value: Record<string, unknown>): PadsSaved | null {
  if (
    typeof value.revision !== "string" ||
    typeof value.applied !== "boolean" ||
    (value.problem !== undefined && typeof value.problem !== "string") ||
    (value.restartRequired !== undefined &&
      typeof value.restartRequired !== "boolean")
  )
    return null;
  return {
    revision: value.revision,
    applied: value.applied,
    ...(value.problem === undefined ? {} : { problem: value.problem }),
    ...(value.restartRequired === undefined
      ? {}
      : { restartRequired: value.restartRequired }),
  };
}

/** The pads.list answer in value, or null when it is not one. */
export function parsePadsList(value: unknown): PadsListResult | null {
  if (!isRecord(value) || typeof value.revision !== "string") return null;
  const marks = legacy(value.legacy);
  const list = pads(value.pads);
  if (!marks || !list) return null;
  return { revision: value.revision, legacy: marks, pads: list };
}

/** The pads.set answer in value, or null when it is not one. */
export function parsePadsSet(value: unknown): PadsSetResult | null {
  if (!isRecord(value)) return null;
  const result = saved(value);
  const one = pad(value.pad);
  if (!result || !one) return null;
  return { ...result, pad: one };
}

/** The pads.migrate answer in value, or null when it is not one. */
export function parsePadsMigrate(value: unknown): PadsMigrateResult | null {
  if (!isRecord(value) || typeof value.changed !== "boolean") return null;
  const result = saved(value);
  const marks = legacy(value.legacy);
  const list = pads(value.pads);
  if (!result || !marks || !list) return null;
  return { ...result, changed: value.changed, legacy: marks, pads: list };
}
