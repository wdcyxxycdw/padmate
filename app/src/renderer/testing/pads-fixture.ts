// The controller list of the daemon (pads.list) for tests: one controller of
// each family the way the daemon describes it, and a list around them. The
// labels are the daemon's (padlabel), the options its padfamily table.

import type { DeviceEntry } from "../../shared/api";
import type {
  PadCombo,
  PadFamily,
  PadFunctionKeyName,
  PadInfo,
  PadLegacy,
  PadSwitches,
  PadsListResult,
} from "../../shared/pads";
import {
  controllerLabels,
  padFunctionKeys,
} from "../controller/controller-model";
import { functionKeyControl } from "../editor/editor-model";
import { sdlPads } from "./sdl-pads";

const allOn: PadSwitches = {
  rumble: true,
  led: true,
  pause: true,
  lowBattery: true,
};

type Option = { control: PadFunctionKeyName; label: string };

const families: Record<
  string,
  {
    family: PadFamily;
    type: string;
    name: string;
    mask: number;
    options: Option[];
    panic: PadCombo;
  }
> = {
  xbox: {
    family: "standard",
    type: "xboxone",
    name: "Xbox Wireless Controller",
    mask: sdlPads.xboxSeries.buttonMask,
    options: [
      { control: "start", label: "Menu" },
      { control: "back", label: "View" },
    ],
    panic: { buttons: ["back", "start"], holdMs: 2000 },
  },
  joyConPair: {
    family: "joyconPair",
    type: "joyconpair",
    name: "Nintendo Switch Joy-Con (L/R)",
    mask: sdlPads.joyConPair.buttonMask,
    options: [
      { control: "back", label: "−" },
      { control: "start", label: "+" },
    ],
    panic: { buttons: ["back", "start"], holdMs: 2000 },
  },
  joyConRight: {
    family: "joyconRight",
    type: "joyconright",
    name: "Nintendo Switch Joy-Con (R)",
    mask: sdlPads.joyConRightUpright.buttonMask,
    options: [
      { control: "rt", label: "ZR" },
      { control: "rightshoulder", label: "R" },
    ],
    panic: { buttons: ["paddle3", "paddle1"], holdMs: 2000 },
  },
  joyConLeft: {
    family: "joyconLeft",
    type: "joyconleft",
    name: "Nintendo Switch Joy-Con (L)",
    mask: sdlPads.joyConLeftUpright.buttonMask,
    options: [
      { control: "lt", label: "ZL" },
      { control: "leftshoulder", label: "L" },
    ],
    panic: { buttons: ["paddle2", "paddle4"], holdMs: 2000 },
  },
};

export type PadKind = keyof typeof families;

/**
 * One controller as pads.list describes it. Everything is the family's
 * default unless the overrides say otherwise; connected controllers get a
 * connection ID.
 */
export function padFixture(
  kind: PadKind,
  overrides: Partial<PadInfo> & { effective?: PadFunctionKeyName } = {},
): PadInfo {
  const base = families[kind];
  const { effective, ...rest } = overrides;
  const connected = rest.connected ?? true;
  const fallback = base.options[0].control;
  const key = effective ?? rest.functionKey?.effective ?? fallback;
  return {
    key: `${kind}-guid/s:${kind}`,
    name: base.name,
    type: base.type,
    buttonMask: base.mask,
    known: true,
    connected,
    ...(connected ? { device: 1 } : {}),
    ignored: false,
    family: base.family,
    functionKey: {
      default: fallback,
      options: base.options,
      effective: key,
      source: key === fallback ? "default" : "device",
      ...(key === fallback ? {} : { stored: key }),
    },
    feedback: { effective: { ...allOn }, source: "default" },
    panic: {
      default: base.panic,
      effective: base.panic,
      source: "default",
    },
    ...rest,
  };
}

const noLegacy: PadLegacy = {
  any: false,
  functionKey: false,
  feedback: false,
  panic: false,
};

/** A pads.list answer around the controllers. */
export function padsFixture(
  pads: PadInfo[],
  options: { revision?: string; legacy?: Partial<PadLegacy> } = {},
): PadsListResult {
  const legacy = { ...noLegacy, ...options.legacy };
  legacy.any =
    options.legacy?.any ??
    (legacy.functionKey || legacy.feedback || legacy.panic);
  return { revision: options.revision ?? "rev-1", legacy, pads };
}

/**
 * The controller list a daemon would give for connected devices, built the way
 * the daemon builds it (padfamily, padlabel): the family from the type and the
 * buttons, the function keys of the family with the labels the controller
 * prints, the built-in emergency combo. Everything is the default.
 */
export function padsOfDevices(
  devices: DeviceEntry[],
  options: Parameters<typeof padsFixture>[1] = {},
): PadsListResult {
  return padsFixture(
    devices
      .filter((entry) => entry.info.gamepad)
      .map((entry): PadInfo => {
        const { type, buttonMask = 0, buttonLabels } = entry.info;
        const family: PadFamily =
          type === "joyconpair"
            ? "joyconPair"
            : type === "joyconleft"
              ? "joyconLeft"
              : type === "joyconright"
                ? "joyconRight"
                : "standard";
        const labels = controllerLabels(type, buttonLabels, buttonMask);
        const keys = padFunctionKeys(type);
        const panic: PadCombo = {
          buttons:
            family === "joyconLeft"
              ? ["paddle2", "paddle4"]
              : family === "joyconRight"
                ? ["paddle3", "paddle1"]
                : ["back", "start"],
          holdMs: 2000,
        };
        return {
          key: entry.key,
          name: entry.info.name,
          type,
          buttonMask,
          known: true,
          connected: true,
          device: entry.info.id,
          ignored: false,
          family,
          functionKey: {
            default: keys[0],
            options: keys.map((control) => ({
              control,
              label: labels[functionKeyControl(control)] ?? control,
            })),
            effective: keys[0],
            source: "default",
          },
          feedback: { effective: { ...allOn }, source: "default" },
          panic: { default: panic, effective: panic, source: "default" },
        };
      }),
    options,
  );
}
