// Pure helpers for the stick calibration of a controller (IN-14): the
// deadzone and outer edge of each stick, saved per controller in config.json
// under devices.<DeviceKey>.calibration. They take a parsed config.json and
// return a new object, or null when the change alters nothing; they never
// mutate their input and keep the members they do not know.
import type {
  Config,
  ConfigCalibration,
  ConfigDevice,
  ConfigStickCalibration,
} from "../shared/generated/schema";

export type StickName = "ls" | "rs";

/** A stick's dead zone and outer edge ("saturation"), as fractions of travel. */
export interface StickValues {
  deadzone: number;
  saturation: number;
}

/** The values a profile that sets none uses (ARCHITECTURE §7.6). */
export const CALIBRATION_DEFAULTS: StickValues = {
  deadzone: 0.15,
  saturation: 0.95,
};
/** The dead zone slider goes from 0 to this. */
export const DEADZONE_MAX = 0.5;
/** The outer edge slider goes from this to 1. */
export const OUTER_MIN = 0.5;
/**
 * The least the outer edge is above the dead zone. The daemon needs only
 * "greater"; the sliders keep this far apart so the stick still has a range.
 */
export const MIN_GAP = 0.05;

const round = (value: number) => Math.round(value * 100) / 100;

/** A fraction as the whole percent the sliders show. */
export function stickPercent(fraction: number): number {
  return Math.round(fraction * 100);
}

function isFraction(value: unknown): value is number {
  return (
    typeof value === "number" &&
    Number.isFinite(value) &&
    value >= 0 &&
    value <= 1
  );
}

function deviceOf(config: Config | null, key: string): ConfigDevice | null {
  const entry = config?.devices?.[key];
  return entry && typeof entry === "object" ? entry : null;
}

/** What config.json saves for one stick of a controller; no member is a fraction. */
export function calibrationStick(
  config: Config | null,
  key: string,
  stick: StickName,
): Partial<StickValues> {
  const saved = deviceOf(config, key)?.calibration?.[stick];
  if (!saved || typeof saved !== "object") return {};
  const out: Partial<StickValues> = {};
  if (isFraction(saved.deadzone)) out.deadzone = saved.deadzone;
  if (isFraction(saved.saturation)) out.saturation = saved.saturation;
  return out;
}

/** The controller has a calibration saved: Restore default has something to do. */
export function hasCalibration(config: Config | null, key: string): boolean {
  return (["ls", "rs"] as const).some(
    (stick) => Object.keys(calibrationStick(config, key, stick)).length > 0,
  );
}

/**
 * What a stick of the profile in use is set to: its analog.ls or analog.rs
 * deadzone and saturation, the defaults for what it does not set. A document
 * that cannot be read, or whose values the daemon would not accept, gives
 * the defaults.
 */
export function profileStickValues(
  text: string | undefined,
  stick: StickName,
): StickValues {
  if (!text) return { ...CALIBRATION_DEFAULTS };
  let saved: unknown;
  try {
    const analog = (
      JSON.parse(text.replace(/^﻿/, "")) as {
        analog?: Record<string, unknown>;
      } | null
    )?.analog;
    saved = analog && typeof analog === "object" ? analog[stick] : undefined;
  } catch {
    return { ...CALIBRATION_DEFAULTS };
  }
  const values = saved && typeof saved === "object" ? saved : {};
  const { deadzone, saturation } = values as Record<string, unknown>;
  const shown: StickValues = {
    deadzone: isFraction(deadzone) ? deadzone : CALIBRATION_DEFAULTS.deadzone,
    saturation: isFraction(saturation)
      ? saturation
      : CALIBRATION_DEFAULTS.saturation,
  };
  return shown.saturation > shown.deadzone
    ? shown
    : { ...CALIBRATION_DEFAULTS };
}

/** What a stick of the controller is set to: its calibration over the profile's. */
export function shownStick(
  config: Config | null,
  key: string,
  stick: StickName,
  profile: StickValues,
): StickValues {
  return { ...profile, ...calibrationStick(config, key, stick) };
}

/**
 * current with one slider moved to value (a fraction): kept in the slider's
 * range, in whole percents, and at least MIN_GAP between the dead zone and
 * the outer edge (the one that is moved stops short of the other). A value
 * that is no number changes nothing.
 */
export function clampStick(
  change: keyof StickValues,
  value: number,
  current: StickValues,
): StickValues {
  if (!Number.isFinite(value)) return current;
  if (change === "deadzone") {
    const top = Math.min(DEADZONE_MAX, round(current.saturation - MIN_GAP));
    return { ...current, deadzone: Math.max(0, Math.min(round(value), top)) };
  }
  const bottom = Math.max(OUTER_MIN, round(current.deadzone + MIN_GAP));
  return {
    ...current,
    saturation: Math.min(1, Math.max(round(value), Math.min(bottom, 1))),
  };
}

/**
 * The sticks a controller has, by its axis mask (leftx, lefty, rightx, righty
 * from bit 0). A controller whose axes are not known, no mask, has both.
 */
export function sticksOf(axisMask: number | undefined): StickName[] {
  if (!axisMask) return ["ls", "rs"];
  const sticks: StickName[] = [];
  if ((axisMask & 0b0011) === 0b0011) sticks.push("ls");
  if ((axisMask & 0b1100) === 0b1100) sticks.push("rs");
  return sticks;
}

/** config with the device entry for key replaced; an entry with nothing in it goes. */
function withDevice(
  config: Config,
  key: string,
  entry: ConfigDevice,
): Config | null {
  const devices = { ...config.devices };
  if (Object.keys(entry).length === 0) delete devices[key];
  else devices[key] = entry;
  const { devices: _devices, ...rest } = config;
  return Object.keys(devices).length ? { ...config, devices } : rest;
}

/**
 * Sets, or with null removes, the dead zone and outer edge of one stick of
 * the controller key. A stick, calibration or device entry that is left
 * empty is removed, but an entry that has ignored stays.
 */
export function setStickCalibration(
  config: Config,
  key: string,
  stick: StickName,
  values: { deadzone?: number | null; saturation?: number | null },
): Config | null {
  const entry: ConfigDevice = { ...deviceOf(config, key) };
  const saved: ConfigStickCalibration = { ...entry.calibration?.[stick] };
  let changed = false;
  for (const member of ["deadzone", "saturation"] as const) {
    const value = values[member];
    if (value === undefined) continue;
    if (value === null) {
      if (!(member in saved)) continue;
      delete saved[member];
    } else {
      if (saved[member] === value) continue;
      saved[member] = value;
    }
    changed = true;
  }
  if (!changed) return null;
  const calibration: ConfigCalibration = { ...entry.calibration };
  if (Object.keys(saved).length) calibration[stick] = saved;
  else delete calibration[stick];
  if (Object.keys(calibration).length) entry.calibration = calibration;
  else delete entry.calibration;
  return withDevice(config, key, entry);
}

/** Removes the whole calibration of the controller key, or null when it has none. */
export function clearCalibration(config: Config, key: string): Config | null {
  const found = deviceOf(config, key);
  if (!found || !("calibration" in found)) return null;
  const { calibration: _calibration, ...entry } = found;
  return withDevice(config, key, entry);
}
