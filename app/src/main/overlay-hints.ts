import type { DaemonEvent } from "../shared/api";
import {
  defaultOverlayPrefs,
  hintEntriesFrom,
  type KeyHint,
  type OverlayHintsMessage,
  type OverlayLanguage,
  type OverlayPad,
  type OverlayPrefs,
} from "../shared/overlay";

// The key hints (HUD-03, ADR-0063). While a button bound to the hints action
// is held, the daemon's periodic engine.activity state event gives the slot a
// "hints" list: for each control of the slot's current layer stack, what it
// does. This takes that list out of the event and hands it to the overlay,
// and tells the overlay to hide it when the list is gone from the events.
//
// Only the first mapped slot is followed, like the toasts. Hints show also
// while the main window is focused (the user asked for them by holding the
// button), and the toast switch of the settings (HUD-05) does not touch them.

/** The hints one state event carries for the first mapped slot. */
export interface ObservedHints {
  /** The slot's device, to find out what kind of pad the names are for. */
  device?: number;
  /** The entries; undefined when the slot's hints are off. */
  entries?: KeyHint[];
}

function record(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;
}

function mapped(device: unknown): boolean {
  return (
    typeof device === "number" && Number.isSafeInteger(device) && device > 0
  );
}

/**
 * The first mapped slot of an engine.activity state event: the lowest index
 * among the slots it lists that have a device (a free slot is listed with
 * device 0, and is not a mapped one). null for a state event without such a
 * slot, undefined for an event that is not a state event. The key hints and
 * a script's panels (SCR-22) are the first mapped slot's, as the toasts are.
 */
export function firstMappedSlot(
  event: DaemonEvent,
): Record<string, unknown> | null | undefined {
  if (event.topic !== "engine.activity") return undefined;
  const data = record(event.data);
  if (data?.kind !== "state") return undefined;
  if (!Array.isArray(data.slots)) return null;
  let first: { index: number; slot: Record<string, unknown> } | undefined;
  for (const item of data.slots) {
    const slot = record(item);
    const index = slot?.slot;
    if (!slot || typeof index !== "number" || !Number.isInteger(index))
      continue;
    if (!mapped(slot.device)) continue;
    if (first && first.index <= index) continue;
    first = { index, slot };
  }
  return first ? first.slot : null;
}

/**
 * The first mapped slot's hints in an engine.activity state event: its
 * entries when it lists any, else none. undefined for events that are not
 * state events, which change nothing. A state event without a usable slot
 * has no hints: a panel that stays up for lack of news would be worse than
 * one that goes.
 */
export function observeHints(event: DaemonEvent): ObservedHints | undefined {
  const slot = firstMappedSlot(event);
  if (slot === undefined) return undefined;
  if (slot === null) return {};
  const device = slot.device;
  const entries = hintEntriesFrom(slot.hints);
  return {
    device:
      typeof device === "number" && Number.isSafeInteger(device)
        ? device
        : undefined,
    entries: entries && entries.length > 0 ? entries : undefined,
  };
}

export interface HintsHost {
  /** Gives the overlay the hints, or with no entries tells it to hide them. */
  show(message: Omit<OverlayHintsMessage, "position">): void;
  /** What kind of pad a device is; undefined when it is not known. May take a while. */
  pad(device: number): Promise<OverlayPad | undefined>;
}

// A cache of pads that never grows past a handful: pads come and go rarely.
const maxPads = 16;

/**
 * Follows the daemon's events and keeps the overlay's hints in step with
 * them. handle runs inside the bridge's reader, where an exception would drop
 * the daemon connection, so nothing in it throws. Hints are sent when the
 * list changes and not on every state event (four a second while a button is
 * held).
 *
 * The control names the page prints depend on the kind of pad (A or ×, RB or
 * R1), which the state event does not say; the pad of the first slot's
 * device is asked of the daemon once and remembered, and the hints are sent
 * again when the answer comes.
 */
export class OverlayHints {
  private language: OverlayLanguage = defaultOverlayPrefs.language;
  /** What the daemon last said the first slot's hints and device are. */
  private latest: ObservedHints = {};
  /** The last non-empty message sent, as text; "" when the overlay shows none. */
  private sent = "";
  /** The pads by device; null: asked, and not known. */
  private pads = new Map<number, OverlayPad | null>();
  private asking = new Set<number>();
  /** Bumped by clear: an answer for an earlier connection is dropped. */
  private epoch = 0;

  constructor(private readonly host: HintsHost) {}

  /** Takes the language of a push; the hints on show are worded again. */
  configure(prefs: Partial<OverlayPrefs>): void {
    if (prefs.language === undefined || prefs.language === this.language)
      return;
    this.language = prefs.language;
    this.apply();
  }

  handle(event: DaemonEvent): void {
    try {
      const seen = observeHints(event);
      if (!seen) return;
      this.latest = seen;
      if (seen.device !== undefined) this.ask(seen.device);
      this.apply();
    } catch {
      // Hints are a courtesy; a broken one never costs the connection.
    }
  }

  /**
   * The daemon is gone (or went away and may come back): no state event will
   * say the hints ended, so they end here. The pads known are forgotten with
   * the connection.
   */
  clear(): void {
    try {
      this.epoch++;
      this.latest = {};
      this.pads.clear();
      this.asking.clear();
      this.apply();
    } catch {
      // As above.
    }
  }

  private ask(device: number): void {
    if (this.pads.has(device) || this.asking.has(device)) return;
    this.asking.add(device);
    const epoch = this.epoch;
    const done = (pad: OverlayPad | undefined) => {
      if (epoch !== this.epoch) return;
      this.asking.delete(device);
      if (this.pads.size >= maxPads) this.pads.clear();
      this.pads.set(device, pad ?? null);
      try {
        this.apply();
      } catch {
        // As above.
      }
    };
    let pending: Promise<OverlayPad | undefined>;
    try {
      pending = this.host.pad(device);
    } catch {
      done(undefined);
      return;
    }
    pending.then(done, () => done(undefined));
  }

  private apply(): void {
    const { entries, device } = this.latest;
    if (!entries) {
      if (this.sent === "") return;
      this.sent = "";
      this.host.show({ entries: [], language: this.language });
      return;
    }
    const pad =
      (device !== undefined ? this.pads.get(device) : undefined) ?? undefined;
    const message = {
      entries,
      ...(pad ? { pad } : {}),
      language: this.language,
    };
    const text = JSON.stringify(message);
    if (text === this.sent) return;
    this.sent = text;
    this.host.show(message);
  }
}
