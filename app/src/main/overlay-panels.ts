import type { DaemonEvent } from "../shared/api";
import {
  type OverlayPanelsMessage,
  type ScriptPanel,
  scriptPanelsFrom,
} from "../shared/overlay";
import { firstMappedSlot } from "./overlay-hints";

// A script's panels (SCR-22, ADR-0063). hud.panel(id, content) keeps a panel
// in the script's runtime; the daemon's periodic engine.activity state event
// gives the slot that runs the script a "panels" list while it has any. This
// takes that list out of the event and hands it to the overlay, and tells the
// overlay to hide it when the list is gone from the events.
//
// Only the first mapped slot is followed, like the toasts and the key hints.
// The panels show also while the main window is focused (the main window does
// not show them) and do not depend on the toast switch of the settings.
// Whether they show while key hints are up is the page's to decide: it shows
// the hints and keeps the panels back.

/**
 * The first mapped slot's panels in an engine.activity state event, as
 * scriptPanelsFrom checks them: none when it lists none or the list is not
 * one. undefined for events that are not state events, which change nothing. A
 * state event without a usable slot has no panels: a panel that stays up for
 * lack of news would be worse than one that goes.
 */
export function observePanels(event: DaemonEvent): ScriptPanel[] | undefined {
  const slot = firstMappedSlot(event);
  if (slot === undefined) return undefined;
  if (slot === null) return [];
  return scriptPanelsFrom(slot.panels) ?? [];
}

export interface PanelsHost {
  /**
   * Gives the overlay the panels, or with none tells it to hide them. false
   * says there is no overlay yet to give them to: the next state event gives
   * them again (panels stay up, so unlike a toast they must not be missed
   * because the overlay was made after the first event).
   */
  show(message: Omit<OverlayPanelsMessage, "position">): boolean;
}

/**
 * Follows the daemon's events and keeps the overlay's panels in step with
 * them. handle runs inside the bridge's reader, where an exception would drop
 * the daemon connection, so nothing in it throws. Panels are sent when they
 * change and not on every state event (four a second while a script has a
 * panel).
 */
export class OverlayPanels {
  /** The last non-empty message sent, as text; "" when the overlay shows none. */
  private sent = "";

  constructor(private readonly host: PanelsHost) {}

  handle(event: DaemonEvent): void {
    try {
      const panels = observePanels(event);
      if (panels) this.apply(panels);
    } catch {
      // Panels are a courtesy; a broken one never costs the connection.
    }
  }

  /**
   * The daemon is gone (or went away and may come back): no state event will
   * say the panels ended, so they end here.
   */
  clear(): void {
    try {
      this.apply([]);
    } catch {
      // As above.
    }
  }

  private apply(panels: ScriptPanel[]): void {
    if (panels.length === 0) {
      if (this.sent === "") return;
      if (this.host.show({ panels: [] })) this.sent = "";
      return;
    }
    const text = JSON.stringify(panels);
    if (text === this.sent) return;
    if (this.host.show({ panels })) this.sent = text;
  }
}
