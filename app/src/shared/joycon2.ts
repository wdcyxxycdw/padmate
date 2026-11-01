// The Joy-Con 2 state of the daemon (joycon2.status, and the data of the
// joycon2.changed event), checked field by field. Main checks the answer of a
// call with it and the Settings page checks an event's data with it, so a
// reply of the wrong shape is refused in one place.

/** Which hand a Joy-Con 2 is for. */
export type JoyCon2Side = "L" | "R";

/** A Joy-Con 2 that padmate remembers. */
export interface JoyCon2Known {
  addr: string;
  serial: string;
  side: JoyCon2Side;
  connected: boolean;
}

/** One connection: connecting, connected, dormant (silent) or closing. */
export interface JoyCon2Link {
  addr: string;
  serial?: string;
  side: JoyCon2Side;
  state: string;
  known: boolean;
}

export interface JoyCon2Status {
  /** False when the daemon has no Joy-Con 2 support: the page hides the section. */
  available: boolean;
  /** The daemon's setting joycon2.autoConnect as it has it. */
  autoConnect: boolean;
  /** The pairing window is open. */
  windowOpen: boolean;
  /** How long the window stays open, in ms, as of the moment of the reply. */
  windowLeftMs: number;
  known: JoyCon2Known[];
  links: JoyCon2Link[];
}

/** More than the daemon remembers (64) or connects (8) is not a state of it. */
const MAX_ROWS = 64;

function isSide(value: unknown): value is JoyCon2Side {
  return value === "L" || value === "R";
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function known(value: unknown): JoyCon2Known | null {
  if (!isRecord(value)) return null;
  if (
    typeof value.addr !== "string" ||
    typeof value.serial !== "string" ||
    !isSide(value.side) ||
    typeof value.connected !== "boolean"
  )
    return null;
  return {
    addr: value.addr,
    serial: value.serial,
    side: value.side,
    connected: value.connected,
  };
}

function link(value: unknown): JoyCon2Link | null {
  if (!isRecord(value)) return null;
  if (
    typeof value.addr !== "string" ||
    !isSide(value.side) ||
    typeof value.state !== "string" ||
    typeof value.known !== "boolean" ||
    (value.serial !== undefined && typeof value.serial !== "string")
  )
    return null;
  return {
    addr: value.addr,
    side: value.side,
    state: value.state,
    known: value.known,
    ...(value.serial === undefined ? {} : { serial: value.serial }),
  };
}

function rows<T>(value: unknown, read: (row: unknown) => T | null): T[] | null {
  if (!Array.isArray(value) || value.length > MAX_ROWS) return null;
  const out: T[] = [];
  for (const row of value) {
    const item = read(row);
    if (item === null) return null;
    out.push(item);
  }
  return out;
}

/** The status in value, or null when it is not one. */
export function parseJoyCon2Status(value: unknown): JoyCon2Status | null {
  if (!isRecord(value)) return null;
  if (typeof value.available !== "boolean") return null;
  if (!value.available)
    return {
      available: false,
      autoConnect: false,
      windowOpen: false,
      windowLeftMs: 0,
      known: [],
      links: [],
    };
  const list = rows(value.known, known);
  const links = rows(value.links, link);
  const left = value.windowLeftMs;
  if (
    !list ||
    !links ||
    typeof value.autoConnect !== "boolean" ||
    typeof value.windowOpen !== "boolean" ||
    (left !== undefined &&
      (typeof left !== "number" || !Number.isFinite(left) || left < 0))
  )
    return null;
  return {
    available: true,
    autoConnect: value.autoConnect,
    windowOpen: value.windowOpen,
    windowLeftMs: value.windowOpen ? ((left as number | undefined) ?? 0) : 0,
    known: list,
    links,
  };
}
