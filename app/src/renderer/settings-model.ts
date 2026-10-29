// Pure helpers for the global settings (UI-19): config.json's timing,
// emergency combos and function key, and start at sign-in. Config helpers take a parsed
// config.json and return a new object, or null when the change alters
// nothing; they never mutate their input and keep members they do not know.

import {
  type DeviceBattery,
  type DeviceCombo,
  LibraryErrorCode,
  type StartupState,
  type StatusResult,
} from "../shared/api";
import type {
  Config,
  ConfigCombo,
  ConfigFeedback,
  ConfigSafety,
  ConfigTiming,
} from "../shared/generated/schema";
import {
  asFunctionKey,
  defaultFunctionKey,
  type FunctionKey,
} from "./editor-model";

/** The largest timing value config.schema.json accepts, in ms. */
export const TIMING_MAX_MS = 60000;
/** The hold times the combo dialog offers, in ms. */
export const COMBO_HOLDS = [1000, 1500, 2000, 3000, 5000] as const;
/** How many buttons a recorded combo may have. */
export const COMBO_MIN_BUTTONS = 2;
export const COMBO_MAX_BUTTONS = 4;

export type PanicFamily = keyof NonNullable<ConfigSafety["panic"]>;
const FAMILIES: readonly string[] = [
  "standard",
  "joyconLeft",
  "joyconRight",
  "joyconLeftVertical",
  "joyconRightVertical",
] satisfies PanicFamily[];

export function isPanicFamily(value: string): value is PanicFamily {
  return FAMILIES.includes(value);
}

/**
 * A millisecond field's text: a whole number from 0 to TIMING_MAX_MS, null
 * for an empty field (the built-in default), undefined when invalid.
 */
export function parseMs(text: string): number | null | undefined {
  const trimmed = text.trim();
  if (trimmed === "") return null;
  if (!/^\d+$/.test(trimmed)) return undefined;
  const value = Number(trimmed);
  return value <= TIMING_MAX_MS ? value : undefined;
}

/** config.timing[key], or undefined when it is not a valid value. */
export function globalTiming(
  config: Config | null,
  key: keyof ConfigTiming,
): number | undefined {
  const value = config?.timing?.[key];
  return Number.isInteger(value) &&
    (value as number) >= 0 &&
    (value as number) <= TIMING_MAX_MS
    ? value
    : undefined;
}

/** Sets config.timing[key], or with null removes it. */
export function setGlobalTiming(
  config: Config,
  key: keyof ConfigTiming,
  value: number | null,
): Config | null {
  const timing: ConfigTiming = { ...config.timing };
  if (value === null) {
    if (!(key in timing)) return null;
    delete timing[key];
  } else {
    if (timing[key] === value) return null;
    timing[key] = value;
  }
  const { timing: _previous, ...rest } = config;
  return Object.keys(timing).length ? { ...config, timing } : rest;
}

/**
 * The function key config.json chooses (UI-30, ADR-0075): Menu unless it says
 * View or the Xbox button. Anything else it holds reads as Menu, as the
 * daemon reads it.
 */
export function configuredFunctionKey(config: Config | null): FunctionKey {
  return asFunctionKey(config?.functionKey);
}

/**
 * Chooses the function key. The file only says what is not the default:
 * choosing Menu removes the member. Null when the file already says it.
 */
export function setFunctionKey(
  config: Config,
  key: FunctionKey,
): Config | null {
  const written: unknown = config.functionKey;
  if (key === defaultFunctionKey) {
    if (written === undefined) return null;
    const { functionKey: _previous, ...rest } = config;
    return rest;
  }
  if (written === key) return null;
  return { ...config, functionKey: key };
}

/**
 * The feedback switches of config.json (UI-27, ADR-0067), in the order the
 * Settings page shows them: 震动, 灯光, 暂停提示, 低电提醒.
 */
export const FEEDBACK_SWITCHES = [
  "rumble",
  "led",
  "pause",
  "lowBattery",
] as const satisfies readonly (keyof ConfigFeedback)[];
export type FeedbackSwitch = (typeof FEEDBACK_SWITCHES)[number];

/** Whether a switch is on: it is, unless config.feedback says false. */
export function feedbackSwitchOn(
  config: Config | null,
  key: FeedbackSwitch,
): boolean {
  return config?.feedback?.[key] !== false;
}

/**
 * Turns a feedback switch on or off. The file only says false: turning one on
 * removes its member, and the object goes with its last one. Null when the
 * file already says it (a member written as true is on, and goes).
 */
export function setFeedbackSwitch(
  config: Config,
  key: FeedbackSwitch,
  on: boolean,
): Config | null {
  const written = config.feedback?.[key];
  if (on ? written === undefined : written === false) return null;
  const feedback: ConfigFeedback = { ...config.feedback };
  if (on) delete feedback[key];
  else feedback[key] = false;
  const { feedback: _previous, ...rest } = config;
  return Object.keys(feedback).length ? { ...config, feedback } : rest;
}

/** Sets config.safety.panic[family], or with null removes it. */
export function setPanicCombo(
  config: Config,
  family: PanicFamily,
  combo: ConfigCombo | null,
): Config | null {
  const panic = { ...config.safety?.panic };
  if (combo === null) {
    if (!(family in panic)) return null;
    delete panic[family];
  } else {
    const old = panic[family];
    if (
      old &&
      old.holdMs === combo.holdMs &&
      old.buttons.length === combo.buttons.length &&
      old.buttons.every((button, i) => button === combo.buttons[i])
    )
      return null;
    panic[family] = { buttons: [...combo.buttons], holdMs: combo.holdMs };
  }
  const { panic: _panic, ...safetyRest } = config.safety ?? {};
  const safety: ConfigSafety = Object.keys(panic).length
    ? { ...config.safety, panic }
    : safetyRest;
  const { safety: _safety, ...rest } = config;
  return Object.keys(safety).length ? { ...config, safety } : rest;
}

/** The emergency combo the daemon reports for device, if any. */
export function deviceCombo(
  status: StatusResult | null,
  device: number | undefined,
): DeviceCombo | undefined {
  return status?.combos?.find((combo) => combo.device === device);
}

/** Why a recorded combo cannot be saved, or null when it can. */
export function comboProblem(buttons: string[]): "few" | "many" | null {
  if (buttons.length < COMBO_MIN_BUTTONS) return "few";
  if (buttons.length > COMBO_MAX_BUTTONS) return "many";
  return null;
}

/**
 * Records the buttons held together: feed it the buttons held after each
 * change. While any is held it keeps the largest set held at once, in the
 * order they were pressed; when all are released, that set is the
 * recording, and the next press starts another one.
 */
export class ComboRecorder {
  private held: string[] = [];
  private peak: string[] = [];
  recorded: string[] | null = null;

  /** The buttons held now, in the order they were pressed. */
  get pressed(): string[] {
    return [...this.held];
  }

  /** Returns true when a recording finished. */
  update(buttons: Iterable<string>): boolean {
    const now = new Set(buttons);
    const pressing = this.held.length > 0;
    this.held = this.held.filter((button) => now.has(button));
    for (const button of now)
      if (!this.held.includes(button)) this.held.push(button);
    if (this.held.length > 0) {
      if (!pressing) this.peak = [];
      if (this.held.length >= this.peak.length) this.peak = [...this.held];
      return false;
    }
    if (!pressing) return false;
    this.recorded = this.peak;
    return true;
  }
}

/**
 * Whether start at sign-in is marked 演练: the entry this daemon registers
 * starts a dry run, and Run holds that entry or none. With another
 * padmate's value in Run (enabled, not current) its mode is unknown.
 */
export function startupDryRun(startup: StartupState): boolean {
  return (
    startup.available &&
    startup.mode === "dry-run" &&
    (!startup.enabled || startup.current)
  );
}

/**
 * What a failed start at sign-in change says. The daemon log has the
 * system's reason, which can name a folder under the user's profile.
 */
export function startupFailureText(
  error: { code?: number },
  language: "zh-CN" | "en",
): string {
  const en = language === "en";
  if (error.code === LibraryErrorCode.startupUnavailable)
    return en
      ? "A development build cannot start at sign-in."
      : "开发版无法设置登录时启动。";
  return en
    ? "Start at sign-in not changed. See Settings → Logs."
    : "无法更改登录时启动，原因见“设置 → 日志”。";
}

/**
 * What a failed identify of a controller says (IN-13): the controller has
 * gone (-32004), or the daemon could not make it buzz and flash, which its
 * log explains.
 */
export function identifyFailureText(
  error: { code?: number },
  language: "zh-CN" | "en",
): string {
  const en = language === "en";
  if (error.code === LibraryErrorCode.notFound)
    return en ? "The controller is no longer connected." : "手柄已断开。";
  return en
    ? "The controller did not respond. See Settings → Logs."
    : "手柄没有响应，原因见“设置 → 日志”。";
}

/**
 * The tags of a controller's row for its power (IN-12): the charge, "电量 80%",
 * when it reports one, and "充电中" while it charges. Only a controller that
 * discharges or charges has a battery to show: nothing for one whose power is
 * not read or unknown, or that is wired (plugged in, or behind a receiver that
 * says full for ever; ADR-0067). The tray's menu words it the same
 * (tray.PowerParts).
 */
export function batteryTags(
  battery: DeviceBattery | undefined,
  language: "zh-CN" | "en",
): string[] {
  if (
    !battery ||
    (battery.state !== "discharging" && battery.state !== "charging")
  )
    return [];
  const en = language === "en";
  const tags: string[] = [];
  if (battery.percent !== undefined)
    tags.push(en ? `Battery ${battery.percent}%` : `电量 ${battery.percent}%`);
  if (battery.state === "charging") tags.push(en ? "Charging" : "充电中");
  return tags;
}
