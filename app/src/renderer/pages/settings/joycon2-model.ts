import { useCallback, useEffect, useState } from "react";
import type { PadmateAPI } from "../../../shared/api";
import {
  type JoyCon2Side,
  type JoyCon2Status,
  parseJoyCon2Status,
} from "../../../shared/joycon2";

/** The part of the window's API the Joy-Con 2 section uses. */
export type JoyCon2Api = Pick<
  PadmateAPI,
  | "getJoyCon2Status"
  | "watchJoyCon2"
  | "pairJoyCon2"
  | "cancelJoyCon2Pair"
  | "forgetJoyCon2"
  | "onEvent"
>;

/**
 * What the daemon said about Joy-Con 2 controllers when a window to connect a
 * new one was asked for: the serial numbers it remembered and the connections
 * it had. What has come since the window opened is what it found.
 */
export interface PairingStart {
  serials: ReadonlySet<string>;
  addrs: ReadonlySet<string>;
}

export function pairingStart(status: JoyCon2Status | null): PairingStart {
  return {
    serials: new Set(status?.known.map((entry) => entry.serial)),
    addrs: new Set(status?.links.map((link) => link.addr)),
  };
}

/**
 * The side of the controller the window has connected since it opened, or
 * null when it found none. A controller that was remembered before comes back
 * as a connection that was not there, a new one as a remembered serial number
 * that was not there.
 */
export function pairingFound(
  start: PairingStart,
  status: JoyCon2Status,
): JoyCon2Side | null {
  const added = status.known.find((entry) => !start.serials.has(entry.serial));
  if (added) return added.side;
  const link = status.links.find(
    (entry) =>
      entry.known && entry.state !== "closing" && !start.addrs.has(entry.addr),
  );
  return link ? link.side : null;
}

/** How long the window that connects a new Joy-Con 2 stays open, in seconds. */
export const PAIR_SECONDS = 30;

/** Where the dialog that connects a new Joy-Con 2 is. */
export type Pairing =
  | {
      phase: "waiting";
      start: PairingStart;
      /** The window was seen open: only its closing ends the wait. */
      opened: boolean;
      /** When the window closes, in ms of the page's clock. */
      deadline: number;
    }
  | { phase: "found"; side: JoyCon2Side }
  | { phase: "none" }
  | { phase: "failed" };

/** The wait that begins when the window is asked for at now. */
export function pairingBegin(
  status: JoyCon2Status | null,
  now: number,
): Pairing {
  return {
    phase: "waiting",
    start: pairingStart(status),
    opened: false,
    deadline: now + PAIR_SECONDS * 1000,
  };
}

/**
 * The dialog after the daemon's state `status`, read at now. While the window
 * is open the dialog waits, to the end the daemon gives. Once it has been
 * seen open and is closed, the dialog says whether a controller was
 * connected; a state from before the window opened ends nothing. A daemon
 * that cannot do Joy-Con 2 any more ends it as a failure.
 */
export function pairingAdvance(
  pairing: Pairing,
  status: JoyCon2Status,
  now: number,
): Pairing {
  if (pairing.phase !== "waiting") return pairing;
  if (!status.available) return { phase: "failed" };
  if (status.windowOpen)
    return { ...pairing, opened: true, deadline: now + status.windowLeftMs };
  if (!pairing.opened) return pairing;
  const side = pairingFound(pairing.start, status);
  return side ? { phase: "found", side } : { phase: "none" };
}

/** The whole seconds the window has left at now; never below 0. */
export function pairingSecondsLeft(deadline: number, now: number): number {
  return Math.max(0, Math.ceil((deadline - now) / 1000));
}

/**
 * The daemon's Joy-Con 2 state while the Settings page is open. null until it
 * has been read, and for good when the daemon cannot say; the section is shown
 * only when `available` is true. The events (joycon2.changed) are asked for
 * only after the first read says that the daemon has Joy-Con 2 support, and
 * let go of when the page closes, so a daemon without it is never asked for
 * the topic. `apply` takes an answer to pair, cancel or forget.
 */
export function useJoyCon2(api: JoyCon2Api): {
  status: JoyCon2Status | null;
  apply: (status: JoyCon2Status) => void;
} {
  const [status, setStatus] = useState<JoyCon2Status | null>(null);
  const apply = useCallback((next: JoyCon2Status) => setStatus(next), []);
  useEffect(() => {
    let alive = true;
    let stopListening: (() => void) | undefined;
    let requested = false;
    (async () => {
      let first: JoyCon2Status;
      try {
        first = await api.getJoyCon2Status();
      } catch {
        return;
      }
      if (!alive) return;
      setStatus(first);
      if (!first.available) return;
      stopListening = api.onEvent((event) => {
        if (event.topic !== "joycon2.changed") return;
        const next = parseJoyCon2Status(event.data);
        if (next) setStatus(next);
      });
      requested = true;
      try {
        await api.watchJoyCon2(true);
      } catch {
        // Without the events the state stays as it was read.
        return;
      }
      if (!alive) return;
      // A change between the first read and the subscription.
      const again = await api.getJoyCon2Status().catch(() => null);
      if (alive && again) setStatus(again);
    })();
    return () => {
      alive = false;
      stopListening?.();
      if (requested) void api.watchJoyCon2(false).catch(() => {});
    };
  }, [api]);
  return { status, apply };
}
