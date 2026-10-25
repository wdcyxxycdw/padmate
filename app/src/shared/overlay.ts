// What Main, the main window's page and the overlay's page share (HUD-04,
// HUD-05, HUD-03, SCR-22, ADR-0063): the channels Main sends on, the page-side
// API the overlay's preload exposes, the preferences the UI pushes to Main and
// the rules for what crosses. Main -> overlay only; the overlay can send
// nothing back.

/** The channel of a toast, from Main to the overlay's preload. */
export const overlayToastChannel = "overlay:toast";

/** The channel of the page's settings (HUD-05), from Main to the overlay's preload. */
export const overlayPrefsChannel = "overlay:prefs";

/** The channel of the key hints (HUD-03), from Main to the overlay's preload. */
export const overlayHintsChannel = "overlay:hints";

/** The channel of a script's panels (SCR-22), from Main to the overlay's preload. */
export const overlayPanelsChannel = "overlay:panels";

/** A toast is one line: at most this many characters (code points) cross. */
export const maxToastLength = 200;

/** How long a toast holds before it fades out, in ms, when nothing says otherwise. */
export const defaultToastHoldMs = 1500;
/** A script's toast (SCR-22) holds for at least this long, in ms. */
export const minToastHoldMs = 500;
/** A script's toast (SCR-22) holds for at most this long, in ms. */
export const maxToastHoldMs = 10000;

/**
 * Makes text fit a toast line: control characters and runs of whitespace
 * become one space, the ends are trimmed and the length is capped. Names come
 * from profile files, so they can hold anything. "" when nothing is left.
 */
export function limitToastText(text: string): string {
  const line = text.replace(/[\p{Cc}\s]+/gu, " ").trim();
  const characters = Array.from(line);
  return characters.length > maxToastLength
    ? characters.slice(0, maxToastLength).join("")
    : line;
}

/**
 * The text of a toast message as the preload accepts it: `{ text }` with a
 * string that is not empty once limited. Anything else is not a toast.
 */
export function toastTextFrom(payload: unknown): string | undefined {
  if (payload === null || typeof payload !== "object") return undefined;
  const text = (payload as { text?: unknown }).text;
  if (typeof text !== "string") return undefined;
  return limitToastText(text) || undefined;
}

/**
 * How long a toast holds, as the daemon words it for a script's toast
 * (SCR-22): a whole number of ms from minToastHoldMs to maxToastHoldMs.
 * undefined for anything else, which means the default hold.
 */
export function toastHoldFrom(value: unknown): number | undefined {
  return typeof value === "number" &&
    Number.isInteger(value) &&
    value >= minToastHoldMs &&
    value <= maxToastHoldMs
    ? value
    : undefined;
}

/**
 * A toast message as the preload accepts it: the text as toastTextFrom has
 * it and, when the sender gave a valid hold (SCR-22), `ms`. A toast of the
 * state changes (HUD-04) has no `ms`; so does one whose `ms` is not valid,
 * and the page then holds for the default.
 */
export function toastFrom(
  payload: unknown,
): { text: string; ms?: number } | undefined {
  const text = toastTextFrom(payload);
  if (text === undefined) return undefined;
  const ms = toastHoldFrom((payload as { ms?: unknown }).ms);
  return ms === undefined ? { text } : { text, ms };
}

/** The UI languages; the toast texts follow the one the UI is in. */
export type OverlayLanguage = "zh-CN" | "en";

/** Where the overlay sits on its display's work area (HUD-05). */
export type OverlayPosition = "bottom" | "top";

/** The pill's opacity in percent: 40 to 100 in steps of 5 in the UI. */
export const overlayOpacityMin = 40;
export const overlayOpacityMax = 100;
export const overlayOpacityStep = 5;

/**
 * What the UI keeps about the overlay and pushes to Main (HUD-05): the toasts
 * and the language they are in, where the window sits and how opaque the pill
 * is. The UI keeps it in its localStorage (store.ts), not in config.json;
 * Main knows it only from the pushes.
 */
export interface OverlayPrefs {
  language: OverlayLanguage;
  toasts: boolean;
  position: OverlayPosition;
  opacity: number;
}

/** What applies until the first push: Chinese, toasts on, at the bottom, fully opaque. */
export const defaultOverlayPrefs: Readonly<OverlayPrefs> = Object.freeze({
  language: "zh-CN",
  toasts: true,
  position: "bottom",
  opacity: 100,
});

/** An opacity percent: a whole number from 40 to 100; anything else is undefined. */
export function overlayOpacityFrom(value: unknown): number | undefined {
  return typeof value === "number" &&
    Number.isInteger(value) &&
    value >= overlayOpacityMin &&
    value <= overlayOpacityMax
    ? value
    : undefined;
}

/**
 * The fields of a push that are valid. Main takes it from the main window's
 * page and trusts it with no more than this: a field that is missing or not
 * one of its values is left out, and nothing else crosses.
 */
export function parseOverlayPrefs(value: unknown): Partial<OverlayPrefs> {
  if (value === null || typeof value !== "object" || Array.isArray(value))
    return {};
  const given = value as Record<string, unknown>;
  // Own properties only: nothing is taken from a prototype.
  const own = (key: string): unknown =>
    Object.hasOwn(given, key) ? given[key] : undefined;
  const prefs: Partial<OverlayPrefs> = {};
  const language = own("language");
  if (language === "en" || language === "zh-CN") prefs.language = language;
  const toasts = own("toasts");
  if (typeof toasts === "boolean") prefs.toasts = toasts;
  const position = own("position");
  if (position === "top" || position === "bottom") prefs.position = position;
  const opacity = overlayOpacityFrom(own("opacity"));
  if (opacity !== undefined) prefs.opacity = opacity;
  return prefs;
}

/** What the overlay's page is told about itself: the pill's opacity (HUD-05). */
export interface OverlayPageSettings {
  opacity: number;
}

/** The settings of a message from Main as the preload accepts them, else undefined. */
export function pageSettingsFrom(
  payload: unknown,
): OverlayPageSettings | undefined {
  if (payload === null || typeof payload !== "object") return undefined;
  const opacity = overlayOpacityFrom(
    (payload as { opacity?: unknown }).opacity,
  );
  return opacity === undefined ? undefined : { opacity };
}

// The key hints (HUD-03). While the button bound to the hints action is held
// the daemon lists, in its state events, what each other control of the
// current layer does; Main passes the list on and the page draws it as a
// panel next to the toast area. The window grows for it (setBounds only) and
// shrinks back when the button is let go.

/** What the toast area is high, and so the window without hints, in DIP. */
export const overlayToastAreaHeight = 160;
/** The panel shows at most this many entries; more end in a line "…". */
export const maxHintsShown = 16;
/** A message carries at most this many entries; the page shows maxHintsShown of them. */
export const maxHintsAccepted = 64;
/** A hint's text is cut to this many characters. */
export const maxHintLabelLength = 40;
/** The panel fades in and out over this long, in ms. */
export const hintsFadeMs = 120;
/** One row of the panel, in px; overlay.css has the same. */
export const hintsRowHeight = 18;
/** The panel's padding above and below its rows, in px; overlay.css has the same. */
export const hintsPanelPadding = 10;
/** Between the panel and the toast area, in px. */
export const hintsGap = 8;
/** A script's panels (SCR-22) show at most this many panels. */
export const maxScriptPanels = 4;
/** A script's panels show at most this many rows (titles count); more end in a line "…". */
export const maxPanelRows = 16;
/**
 * The tallest the window gets, in DIP: the toast area, the gap and a script's
 * panels of maxPanelRows rows with the "…" line. They are one column, so they
 * are taller than the hints' two columns, which need 350 at most.
 */
export const overlayMaxHeight =
  overlayToastAreaHeight +
  hintsGap +
  (maxPanelRows + 1) * hintsRowHeight +
  2 * hintsPanelPadding;

/** A control and what it does on the current layer, as the daemon words it. */
export interface KeyHint {
  /** The control's canonical name, e.g. "south" or "ls.up". */
  control: string;
  /** The binding's label, else a short text of its first action. */
  label: string;
}

/**
 * The pad the hints are for, as far as Main knows it, so that the page can
 * print the controls as that pad does (A or ×, RB or R1). Without it the
 * names are the generic ones.
 */
export interface OverlayPad {
  type: string;
  buttonLabels?: Record<string, string>;
  buttonMask?: number;
}

/** What Main sends the page for hints: the list (empty: hide) and how to word it. */
export interface OverlayHintsMessage {
  entries: KeyHint[];
  pad?: OverlayPad;
  /** The UI's language, for the control names. */
  language: OverlayLanguage;
  /** Where the window sits, so the panel goes on the side away from the screen's edge. */
  position: OverlayPosition;
}

/** A hint's text on one line, cut to maxHintLabelLength characters. */
export function limitHintLabel(text: string): string {
  const line = text.replace(/[\p{Cc}\s]+/gu, " ").trim();
  const characters = Array.from(line);
  return characters.length > maxHintLabelLength
    ? characters.slice(0, maxHintLabelLength).join("")
    : line;
}

const hintControl = /^[a-z0-9][a-z0-9.+-]{0,23}$/;

/**
 * The hints in a list as the daemon sent it: an entry needs a control name of
 * the daemon's spelling and a string label (which may be empty); others are
 * left out, and so are a control named a second time and everything past
 * maxHintsAccepted. undefined when the value is not an array.
 */
export function hintEntriesFrom(value: unknown): KeyHint[] | undefined {
  if (!Array.isArray(value)) return undefined;
  const entries: KeyHint[] = [];
  const named = new Set<string>();
  for (const item of value) {
    if (entries.length >= maxHintsAccepted) break;
    if (item === null || typeof item !== "object") continue;
    const { control, label } = item as { control?: unknown; label?: unknown };
    if (typeof control !== "string" || !hintControl.test(control)) continue;
    if (typeof label !== "string" || named.has(control)) continue;
    named.add(control);
    entries.push({ control, label: limitHintLabel(label) });
  }
  return entries;
}

function padFrom(value: unknown): OverlayPad | undefined {
  if (value === null || typeof value !== "object" || Array.isArray(value))
    return undefined;
  const given = value as Record<string, unknown>;
  const type = given.type;
  if (typeof type !== "string" || type.length > 40) return undefined;
  const pad: OverlayPad = { type };
  const labels = given.buttonLabels;
  if (labels !== null && typeof labels === "object" && !Array.isArray(labels)) {
    const kept: Record<string, string> = {};
    for (const [name, text] of Object.entries(labels).slice(0, 32)) {
      if (typeof text === "string" && text.length <= 24 && name.length <= 24)
        kept[name] = text;
    }
    pad.buttonLabels = kept;
  }
  const mask = given.buttonMask;
  if (typeof mask === "number" && Number.isSafeInteger(mask) && mask >= 0)
    pad.buttonMask = mask;
  return pad;
}

/**
 * A hints message as the preload accepts it: entries as hintEntriesFrom has
 * them (an empty list hides the panel), the UI's language and the window's
 * position. A pad that is not well-formed is left out; anything else wrong
 * makes the message undefined.
 */
export function hintsFrom(payload: unknown): OverlayHintsMessage | undefined {
  if (payload === null || typeof payload !== "object") return undefined;
  const given = payload as Record<string, unknown>;
  const entries = hintEntriesFrom(given.entries);
  if (!entries) return undefined;
  const language = given.language === "en" ? "en" : "zh-CN";
  const position = given.position === "top" ? "top" : "bottom";
  const pad = padFrom(given.pad);
  return { entries, ...(pad ? { pad } : {}), language, position };
}

/** How the panel lays out `count` entries: how many it shows, whether it adds "…", and the rows. */
export function hintsLayout(count: number): {
  shown: number;
  more: boolean;
  rows: number;
} {
  const shown = Math.max(0, Math.min(count, maxHintsShown));
  return {
    shown,
    more: count > maxHintsShown,
    rows: Math.ceil(shown / 2),
  };
}

/**
 * The panel's height in px for `count` entries: its rows (two columns) and
 * the "…" line, with the padding. 0 for no entries.
 */
export function hintsPanelHeight(count: number): number {
  const { shown, more, rows } = hintsLayout(count);
  if (shown === 0) return 0;
  return (rows + (more ? 1 : 0)) * hintsRowHeight + 2 * hintsPanelPadding;
}

// The window's height for a panel `panel` px tall: the toast area plus the
// panel and its gap, at most overlayMaxHeight; without a panel just the toast
// area.
function windowHeightFor(panel: number): number {
  if (panel === 0) return overlayToastAreaHeight;
  return Math.min(overlayMaxHeight, overlayToastAreaHeight + panel + hintsGap);
}

/**
 * The window's height for `count` hint entries: the toast area plus the
 * panel and its gap, at most overlayMaxHeight; without entries just the
 * toast area.
 */
export function overlayHeightForHints(count: number): number {
  return windowHeightFor(hintsPanelHeight(count));
}

// A script's panels (SCR-22). hud.panel(id, content) in a script keeps a
// panel of rows [left, right] in the daemon, which lists the panels in the
// state events of the slot that runs the script. Main passes those of the
// first mapped slot on and the page draws them where the key hints go, one
// row per line. The hints win while they are up.

/** A panel of a script: an optional title and rows of two texts. */
export interface ScriptPanel {
  title?: string;
  rows: Array<[string, string]>;
}

/** What Main sends the page for a script's panels: the panels (none: hide) and where the window sits. */
export interface OverlayPanelsMessage {
  panels: ScriptPanel[];
  /** Where the window sits, so the panels go on the side away from the screen's edge. */
  position: OverlayPosition;
}

function panelFrom(item: unknown, withId: boolean): ScriptPanel | undefined {
  if (item === null || typeof item !== "object" || Array.isArray(item))
    return undefined;
  const given = item as Record<string, unknown>;
  if (withId && (typeof given.id !== "string" || given.id === ""))
    return undefined;
  if (!Array.isArray(given.rows)) return undefined;
  const rows: Array<[string, string]> = [];
  for (const row of given.rows) {
    if (rows.length >= maxPanelRows) break;
    if (
      !Array.isArray(row) ||
      row.length !== 2 ||
      typeof row[0] !== "string" ||
      typeof row[1] !== "string"
    )
      continue;
    rows.push([limitHintLabel(row[0]), limitHintLabel(row[1])]);
  }
  const title =
    typeof given.title === "string" ? limitHintLabel(given.title) : "";
  // Nothing to draw: not a panel.
  if (title === "" && rows.length === 0) return undefined;
  return title === "" ? { rows } : { title, rows };
}

/**
 * The panels in a list: the daemon's (each with an `id` that is a string,
 * withId) or a message's (without ids). A panel needs a `rows` list; the rows
 * kept are those of two strings, at most maxPanelRows of them, and the texts
 * are cut as the hints' are (limitHintLabel). An entry that is not
 * well-formed, or has neither a title nor a row, is left out, and so is an id
 * named a second time and everything past maxScriptPanels. undefined when the
 * value is not an array.
 */
export function scriptPanelsFrom(
  value: unknown,
  withId = true,
): ScriptPanel[] | undefined {
  if (!Array.isArray(value)) return undefined;
  const panels: ScriptPanel[] = [];
  const named = new Set<string>();
  for (const item of value) {
    if (panels.length >= maxScriptPanels) break;
    const panel = panelFrom(item, withId);
    if (!panel) continue;
    if (withId) {
      const id = (item as { id: string }).id;
      if (named.has(id)) continue;
      named.add(id);
    }
    panels.push(panel);
  }
  return panels;
}

/**
 * A panels message as the preload accepts it: the panels as scriptPanelsFrom
 * has them without ids (an empty list hides them) and the window's position.
 * Anything else wrong makes the message undefined.
 */
export function panelsFrom(payload: unknown): OverlayPanelsMessage | undefined {
  if (payload === null || typeof payload !== "object") return undefined;
  const given = payload as Record<string, unknown>;
  const panels = scriptPanelsFrom(given.panels, false);
  if (!panels) return undefined;
  return { panels, position: given.position === "top" ? "top" : "bottom" };
}

/** One row of the panels as the page draws it: a title, or a left and a right text. */
export type PanelLine =
  | { kind: "title"; text: string }
  | { kind: "row"; left: string; right: string };

/**
 * The rows the page draws for the panels, in order, a title as a row of its
 * own: the first maxPanelRows of them, and `more` when there were others,
 * which the page marks with a last line "…".
 */
export function scriptPanelLines(panels: readonly ScriptPanel[]): {
  lines: PanelLine[];
  more: boolean;
} {
  const lines: PanelLine[] = [];
  let more = false;
  const add = (line: PanelLine) => {
    if (lines.length < maxPanelRows) lines.push(line);
    else more = true;
  };
  for (const panel of panels) {
    if (panel.title) add({ kind: "title", text: panel.title });
    for (const [left, right] of panel.rows) add({ kind: "row", left, right });
  }
  return { lines, more };
}

/** How many rows the page draws for the panels: the lines and the "…" line. */
export function scriptPanelRows(panels: readonly ScriptPanel[]): number {
  const { lines, more } = scriptPanelLines(panels);
  return lines.length + (more ? 1 : 0);
}

/**
 * The window's height for a script's panels that take `rows` rows (the "…"
 * line counts): the toast area plus the panel, its padding and the gap, at
 * most overlayMaxHeight; without rows just the toast area.
 */
export function overlayHeightForPanels(rows: number): number {
  return windowHeightFor(
    rows <= 0 ? 0 : rows * hintsRowHeight + 2 * hintsPanelPadding,
  );
}

/**
 * The whole of what the overlay's page can use from the host
 * (window.padmateOverlay): it listens for toasts, for its settings, for key
 * hints and for a script's panels. No way to call Main.
 */
export interface OverlayAPI {
  /**
   * Calls listener with the text of each toast and, for a script's toast
   * (SCR-22), how long it holds in ms; the returned function stops it.
   */
  onToast(listener: (text: string, ms?: number) => void): () => void;
  /**
   * Calls listener with the page's settings: at once with the latest ones
   * Main has sent, if any, then with each change; the returned function
   * stops it.
   */
  onPrefs(listener: (settings: OverlayPageSettings) => void): () => void;
  /**
   * Calls listener with each hints message (HUD-03): the entries to show, or
   * none to hide the panel. At once with the latest one Main has sent, if
   * any (an empty one hides), then with each change; the returned function
   * stops it.
   */
  onHints(listener: (message: OverlayHintsMessage) => void): () => void;
  /**
   * Calls listener with each panels message (SCR-22): the panels of a
   * script to show, or none to hide them. At once with the latest one Main
   * has sent, if any (an empty one hides), then with each change; the
   * returned function stops it.
   */
  onPanels(listener: (message: OverlayPanelsMessage) => void): () => void;
}

declare global {
  interface Window {
    /** Only the overlay's page has it, and only when its preload ran. */
    padmateOverlay?: OverlayAPI;
  }
}
