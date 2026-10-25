import type { DaemonEvent } from "../shared/api";
import {
  defaultOverlayPrefs,
  defaultToastHoldMs,
  limitToastText,
  type OverlayLanguage,
  type OverlayPrefs,
  toastHoldFrom,
} from "../shared/overlay";

// What the overlay says when something changes (HUD-04, ADR-0063). The
// daemon already tells the UI everything in its periodic engine.activity
// state events; this takes the changes out of them. reduceToast is a pure
// function of the last state, one event and what Main knows (is the editor
// in front); OverlayToaster is the thin shell that keeps the state, the
// language and the on/off switch the UI pushed (HUD-05) and hands the text to
// the overlay.
//
// Only the first mapped slot is followed for now. A profile switch, a pause or
// a resume, and a change of the active layer each make one toast; several
// changes in one event show the first of them in that order, because a toast
// replaces the one before it at once.
//
// A script's hud.toast (SCR-22) reaches the UI as an event of the topic hud
// and is shown as the same pill, from any slot, whether or not the main
// window is focused (the main window does not show it). It follows the toast
// switch like the others.
//
// A low battery (IN-12) is told when a controller's "low" in the state event's
// batteries goes from false to true: the daemon says low only while the alert
// of the episode is raised, so a controller with the alert switched off, one
// that is plugged in or one that has not read a charge makes none, and one that
// wakes up again in the same episode (a new connection ID, the same key) is
// not told twice. The events are snapshots every 250 ms: a controller charged
// and low again between two of them is low at both, so the "episode" the daemon
// numbers its alerts with tells it: a larger one than the controller was last
// seen in is a new alert, whatever "low" did in between. It is told whichever
// controller it is, and also while the
// main window is focused: the editor shows the battery only on the settings
// page. When several changes come in one event it comes first, since it is not
// repeated. It follows the toast switch like the others.

export type ToastLanguage = OverlayLanguage;

export type ToastChange =
  | { kind: "profile"; name: string }
  | { kind: "paused" }
  | { kind: "resumed" }
  | { kind: "layer"; name: string }
  | { kind: "battery"; percent: number; pad?: string };

/**
 * What the last state event said. A field is undefined while it is unknown:
 * before the first event, with no controller mapped, or when the event left
 * it out. A change is only a change from a known value to another known one.
 */
export interface ToastState {
  /** A state event has been seen: the first one is a starting point, not a change. */
  seen: boolean;
  paused?: boolean;
  /** Index of the slot the profile and layer belong to. */
  slot?: number;
  profile?: string;
  /** The active layer: the top overlay, else the base. */
  layer?: string;
  /**
   * The "low" of each controller (by its key) in the state events that listed
   * batteries. A controller that a later event leaves out keeps its entry: one
   * that wakes up again is not a new episode. Undefined until an event has
   * listed some.
   */
  lows?: Record<string, boolean>;
  /**
   * The number of the alert (the daemon's "episode") each low controller was
   * last seen in, by its key; a controller that is not low has none. Undefined
   * until an event has given one.
   */
  episodes?: Record<string, number>;
}

export const initialToastState: ToastState = { seen: false };

/** How many controllers' "low" and episode the state keeps; the oldest go first. */
const maxLows = 32;

export interface ToastContext {
  /** The main window is focused: the user is looking at the editor. */
  mainFocused: boolean;
}

export interface ToastStep {
  next: ToastState;
  toast?: ToastChange;
}

interface Observed {
  paused?: boolean;
  slot?: { index: number; profile?: string; layer?: string };
  /** The valid entries of the event's batteries; undefined when it has none. */
  batteries?: BatteryObserved[];
}

interface BatteryObserved {
  key: string;
  name: string;
  percent?: number;
  low: boolean;
  /** The daemon's number of the alert a low controller is in. */
  episode?: number;
}

function record(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;
}

function name(value: unknown): string | undefined {
  return typeof value === "string" && value !== "" ? value : undefined;
}

// The base layer. Overlay layers are not told: a layer held while painting
// is pushed and popped with every press, and a toast for each would flash
// all the time. A base switch (switch, cycle) is a mode the user stays in.
function activeLayer(layers: unknown): string | undefined {
  const entry = record(layers);
  if (!entry) return undefined;
  return name(entry.base);
}

// The first mapped slot: the lowest index among the slots the event lists
// that have a device (a free slot is listed with device 0).
function firstSlot(slots: unknown): Observed["slot"] {
  if (!Array.isArray(slots)) return undefined;
  let first: Observed["slot"];
  for (const item of slots) {
    const slot = record(item);
    const index = slot?.slot;
    if (!slot || typeof index !== "number" || !Number.isInteger(index))
      continue;
    const device = slot.device;
    if (
      typeof device !== "number" ||
      !Number.isSafeInteger(device) ||
      device <= 0
    )
      continue;
    if (first && first.index <= index) continue;
    first = {
      index,
      profile: name(slot.profile),
      layer: activeLayer(slot.layers),
    };
  }
  return first;
}

// The controllers of the event's batteries (IN-12): the ones with a key and a
// boolean "low". Undefined when the event has no list.
function batteriesOf(value: unknown): BatteryObserved[] | undefined {
  if (!Array.isArray(value)) return undefined;
  const list: BatteryObserved[] = [];
  for (const item of value) {
    const entry = record(item);
    const key = name(entry?.key);
    if (!entry || key === undefined || typeof entry.low !== "boolean") continue;
    const percent = entry.percent;
    list.push({
      key,
      name: typeof entry.name === "string" ? entry.name : "",
      percent:
        typeof percent === "number" &&
        Number.isInteger(percent) &&
        percent >= 0 &&
        percent <= 100
          ? percent
          : undefined,
      low: entry.low,
      episode:
        typeof entry.episode === "number" &&
        Number.isSafeInteger(entry.episode) &&
        entry.episode > 0
          ? entry.episode
          : undefined,
    });
  }
  return list;
}

// engine.activity {kind:"state"}; anything else is not about this.
function observe(event: DaemonEvent): Observed | undefined {
  if (event.topic !== "engine.activity") return undefined;
  const data = record(event.data);
  if (data?.kind !== "state") return undefined;
  return {
    paused: typeof data.paused === "boolean" ? data.paused : undefined,
    slot: firstSlot(data.slots),
    batteries: batteriesOf(data.batteries),
  };
}

// The lows to take into the next state: the last known of each controller with
// what the event says, the oldest dropped when there are more than maxLows.
function nextLows(
  previous: ToastState["lows"],
  batteries: BatteryObserved[] | undefined,
): ToastState["lows"] {
  if (!batteries) return previous;
  const merged = new Map(Object.entries(previous ?? {}));
  for (const { key, low } of batteries) {
    merged.delete(key);
    merged.set(key, low);
  }
  return Object.fromEntries([...merged].slice(-maxLows));
}

// The episodes to take into the next state: of each controller that is low, the
// number the event gives, or the one known when the event gives none; a
// controller that is not low has none. Bounded like the lows.
function nextEpisodes(
  previous: ToastState["episodes"],
  batteries: BatteryObserved[] | undefined,
): ToastState["episodes"] {
  if (!batteries) return previous;
  const merged = new Map(Object.entries(previous ?? {}));
  for (const { key, low, episode } of batteries) {
    const known = merged.get(key);
    merged.delete(key);
    if (!low) continue;
    const kept = episode ?? known;
    if (kept !== undefined) merged.set(key, kept);
  }
  return Object.fromEntries([...merged].slice(-maxLows));
}

// The first controller that has a new alert: its "low" went from false to true,
// or it is low in a larger episode than it was last seen in. One that the state
// did not know counts as false before: the first event is a starting point, but
// a controller that appears low later has just got its alert. The controller
// is named when the event lists several.
function lowBattery(
  previous: ToastState,
  batteries: BatteryObserved[] | undefined,
): ToastChange | undefined {
  for (const battery of batteries ?? []) {
    const before = previous.episodes?.[battery.key];
    const newEpisode =
      battery.episode !== undefined &&
      before !== undefined &&
      battery.episode > before;
    if (
      !battery.low ||
      battery.percent === undefined ||
      (previous.lows?.[battery.key] === true && !newEpisode)
    )
      continue;
    return batteries && batteries.length > 1
      ? { kind: "battery", percent: battery.percent, pad: battery.name }
      : { kind: "battery", percent: battery.percent };
  }
  return undefined;
}

/**
 * The toast an event makes, and the state to take into the next call.
 * Events that are not state events change nothing. The state always moves on,
 * also when no toast is shown because the main window is focused: a change
 * seen then is not told later.
 *
 * Not a change: the first state event; a field that was unknown before; a
 * different slot being the first mapped (its profile and layer are a new
 * starting point, as when the first pad is unplugged); the same state again,
 * as after a reconnect. A field an event leaves out, or a list without a
 * slot, leaves what is known as it is, so a gap does not hide a change.
 */
export function reduceToast(
  previous: ToastState,
  event: DaemonEvent,
  context: ToastContext,
): ToastStep {
  const seen = observe(event);
  if (!seen) return { next: previous };
  const slot = seen.slot;
  const sameSlot = !!slot && previous.slot === slot.index;
  const next: ToastState = {
    seen: true,
    paused: seen.paused ?? previous.paused,
    slot: slot ? slot.index : previous.slot,
    profile: slot
      ? (slot.profile ?? (sameSlot ? previous.profile : undefined))
      : previous.profile,
    layer: slot
      ? (slot.layer ?? (sameSlot ? previous.layer : undefined))
      : previous.layer,
  };
  const lows = nextLows(previous.lows, seen.batteries);
  if (lows) next.lows = lows;
  const episodes = nextEpisodes(previous.episodes, seen.batteries);
  if (episodes) next.episodes = episodes;
  if (!previous.seen) return { next };
  // Told also while the main window is focused: the editor shows the battery
  // only on the settings page.
  const battery = lowBattery(previous, seen.batteries);
  if (context.mainFocused) return { next, toast: battery };
  let toast: ToastChange | undefined;
  if (
    sameSlot &&
    previous.profile !== undefined &&
    next.profile !== undefined &&
    previous.profile !== next.profile
  )
    toast = { kind: "profile", name: next.profile };
  else if (
    previous.paused !== undefined &&
    next.paused !== undefined &&
    previous.paused !== next.paused
  )
    toast = { kind: next.paused ? "paused" : "resumed" };
  else if (
    sameSlot &&
    previous.layer !== undefined &&
    next.layer !== undefined &&
    previous.layer !== next.layer
  )
    toast = { kind: "layer", name: next.layer };
  return { next, toast: battery ?? toast };
}

const texts = {
  "zh-CN": {
    profile: (name: string) => `配置：${name}`,
    paused: "已暂停",
    resumed: "已恢复",
    layer: (name: string) => `层：${name}`,
    battery: (percent: number, pad: string | undefined) =>
      pad ? `${pad} 电量低：${percent}%` : `手柄电量低：${percent}%`,
  },
  en: {
    profile: (name: string) => `Profile: ${name}`,
    paused: "Paused",
    resumed: "Resumed",
    layer: (name: string) => `Layer: ${name}`,
    battery: (percent: number, pad: string | undefined) =>
      pad
        ? `${pad} battery low: ${percent}%`
        : `Controller battery low: ${percent}%`,
  },
} as const;

/** The line a change is told in; one line, at most maxToastLength characters. */
export function toastText(
  change: ToastChange,
  language: ToastLanguage,
): string {
  const words = texts[language];
  switch (change.kind) {
    case "profile":
      return limitToastText(words.profile(change.name));
    case "layer":
      return limitToastText(words.layer(change.name));
    case "paused":
      return words.paused;
    case "resumed":
      return words.resumed;
    case "battery":
      return limitToastText(
        words.battery(change.percent, shortPadName(change.pad)),
      );
  }
}

// The controller's name in a battery toast: one line of at most 40 characters,
// so that the charge, which comes last, always shows. undefined when there is
// no name to tell.
function shortPadName(pad: string | undefined): string | undefined {
  if (pad === undefined) return undefined;
  const line = Array.from(limitToastText(pad)).slice(0, 40).join("");
  return line === "" ? undefined : line;
}

/** What a script's hud.toast asks for: the line, and how long it holds in ms. */
export interface ScriptToast {
  text: string;
  ms: number;
}

/**
 * The toast in a hud event, `{ kind: "toast", slot, text, ms }` as the daemon
 * sends it (SCR-22), checked here: the text must be a string that is not empty
 * as a toast line, and ms a whole number from 500 to 10000 ms, else the
 * default hold. undefined for any other event. The slot is not looked at: a
 * toast from any slot shows.
 */
export function scriptToastFrom(event: DaemonEvent): ScriptToast | undefined {
  if (event.topic !== "hud") return undefined;
  const data = record(event.data);
  if (data?.kind !== "toast" || typeof data.text !== "string") return undefined;
  const text = limitToastText(data.text);
  if (text === "") return undefined;
  return { text, ms: toastHoldFrom(data.ms) ?? defaultToastHoldMs };
}

export interface ToasterHost {
  /** The main window is focused. */
  mainFocused(): boolean;
  /** Shows the text in the overlay, if there is one; ms is how long it holds, if not the default. */
  show(text: string, ms?: number): void;
}

/**
 * Follows the daemon's events and tells the overlay what changed. handle
 * runs inside the bridge's reader, where an exception would drop the daemon
 * connection, so nothing in it throws.
 *
 * The language the texts are in and whether there are toasts at all come from
 * the UI, which pushes them (HUD-05, configure). Until it has, they are
 * Chinese and on. With toasts off the state still moves on: what changed
 * meanwhile is not told when they are turned on again; a script's toast is
 * not shown, and not told later either.
 */
export class OverlayToaster {
  private state = initialToastState;
  private language: ToastLanguage = defaultOverlayPrefs.language;
  private enabled = defaultOverlayPrefs.toasts;

  constructor(private readonly host: ToasterHost) {}

  /** Takes the language and the on/off switch of a push; a missing field keeps what was. */
  configure(prefs: Partial<Pick<OverlayPrefs, "language" | "toasts">>): void {
    if (prefs.language !== undefined) this.language = prefs.language;
    if (prefs.toasts !== undefined) this.enabled = prefs.toasts;
  }

  handle(event: DaemonEvent): void {
    try {
      const fromScript = scriptToastFrom(event);
      if (fromScript) {
        if (this.enabled) this.host.show(fromScript.text, fromScript.ms);
        return;
      }
      const step = reduceToast(this.state, event, {
        mainFocused: this.host.mainFocused(),
      });
      this.state = step.next;
      if (step.toast && this.enabled)
        this.host.show(toastText(step.toast, this.language));
    } catch {
      // A toast is a courtesy; a broken one never costs the connection.
    }
  }
}
