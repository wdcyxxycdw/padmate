// Pure helpers for the global settings: config.json's timing and the
// Joy-Con 2 switch, and start at sign-in. (The function key, feedback switches
// and emergency combo are settings of each controller, pads-model.ts.) Config
// helpers take a parsed config.json and return a new object, or null when the
// change alters nothing; they never mutate their input and keep members they
// do not know.

import {
  type DeviceBattery,
  LibraryErrorCode,
  type StartupState,
} from "../../shared/api";
import type {
  Config,
  ConfigJoycon2,
  ConfigTiming,
} from "../../shared/generated/schema";
import type { PadSwitches } from "../../shared/pads";

/** The largest timing value config.schema.json accepts, in ms. */
export const TIMING_MAX_MS = 60000;
/** The hold times the combo dialog offers, in ms. */
export const COMBO_HOLDS = [1000, 1500, 2000, 3000, 5000] as const;
/** How many buttons a recorded combo may have. */
export const COMBO_MIN_BUTTONS = 2;
export const COMBO_MAX_BUTTONS = 4;

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
 * The feedback switches (ADR-0015), in the order the Controllers page shows
 * them: 震动, 灯光, 暂停提示, 低电提醒. Each controller has its own
 * (ADR-0017).
 */
export const FEEDBACK_SWITCHES = [
  "rumble",
  "led",
  "pause",
  "lowBattery",
] as const satisfies readonly (keyof PadSwitches)[];
export type FeedbackSwitch = (typeof FEEDBACK_SWITCHES)[number];

/**
 * Whether a remembered Joy-Con 2 connects by itself when its SYNC button is
 * held: config.joycon2.autoConnect, on unless the file says false.
 */
export function joyCon2AutoConnect(config: Config | null): boolean {
  return config?.joycon2?.autoConnect !== false;
}

/**
 * Turns joycon2.autoConnect on or off. The file only says false, like a
 * feedback switch: turning it on removes the member, and the object goes with
 * it. Null when the file already says it.
 */
export function setJoyCon2AutoConnect(
  config: Config,
  on: boolean,
): Config | null {
  const written = config.joycon2?.autoConnect;
  if (on ? written === undefined : written === false) return null;
  const joycon2: ConfigJoycon2 = { ...config.joycon2 };
  if (on) delete joycon2.autoConnect;
  else joycon2.autoConnect = false;
  const { joycon2: _previous, ...rest } = config;
  return Object.keys(joycon2).length ? { ...config, joycon2 } : rest;
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
 * recording, and the next press starts another one. The Xbox button
 * (guide) is not seen.
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
    // padmate never uses the Xbox button (ADR-0012): it is no member.
    const now = new Set([...buttons].filter((b) => b !== "guide"));
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
 * What a failed identify of a controller says: the controller has
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
 * The tags of a controller's row for its power: the charge, "电量 80%",
 * when it reports one, and "充电中" while it charges. Only a controller that
 * discharges or charges has a battery to show: nothing for one whose power is
 * not read or unknown, or that is wired (plugged in, or behind a receiver that
 * says full for ever; ADR-0015). The tray's menu words it the same
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
