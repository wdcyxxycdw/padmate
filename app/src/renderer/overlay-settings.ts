import {
  defaultOverlayPrefs,
  type OverlayPrefs,
  overlayOpacityFrom,
} from "../shared/overlay";

// The overlay's settings (HUD-05, ADR-0063) are the UI's own, like its
// language: they are kept in this window's localStorage, not in config.json or
// a profile. The store pushes them to Main, which acts on them (store.ts).

/** The three settings; the fourth thing the UI tells Main, the language, is the store's. */
export type OverlaySettings = Omit<OverlayPrefs, "language">;

export const defaultOverlaySettings: Readonly<OverlaySettings> = Object.freeze({
  toasts: defaultOverlayPrefs.toasts,
  position: defaultOverlayPrefs.position,
  opacity: defaultOverlayPrefs.opacity,
});

/** The localStorage keys. */
export const overlayStorageKeys = {
  /** "off" is off; anything else, or nothing, is on. */
  toasts: "padmate.overlay.toasts",
  /** "top" or "bottom"; anything else is bottom. */
  position: "padmate.overlay.position",
  /** A whole number from 40 to 100 as text; anything else is 100. */
  opacity: "padmate.overlay.opacity",
} as const;

function read(key: string): string | null {
  try {
    return globalThis.localStorage?.getItem(key) ?? null;
  } catch {
    return null;
  }
}

/** What localStorage says; a missing, unreadable or invalid value is the default. */
export function savedOverlaySettings(): OverlaySettings {
  const opacity = read(overlayStorageKeys.opacity);
  return {
    toasts: read(overlayStorageKeys.toasts) !== "off",
    position: read(overlayStorageKeys.position) === "top" ? "top" : "bottom",
    opacity:
      (opacity !== null && /^\d{1,3}$/.test(opacity)
        ? overlayOpacityFrom(Number(opacity))
        : undefined) ?? defaultOverlaySettings.opacity,
  };
}

/** The fields of change that are valid values; the others are dropped. */
export function validOverlayChange(
  change: Partial<OverlaySettings>,
): Partial<OverlaySettings> {
  const valid: Partial<OverlaySettings> = {};
  if (typeof change.toasts === "boolean") valid.toasts = change.toasts;
  if (change.position === "top" || change.position === "bottom")
    valid.position = change.position;
  const opacity = overlayOpacityFrom(change.opacity);
  if (opacity !== undefined) valid.opacity = opacity;
  return valid;
}

/** Writes the given fields; a failure leaves the setting good for this window only. */
export function saveOverlaySettings(change: Partial<OverlaySettings>): void {
  try {
    const storage = globalThis.localStorage;
    if (!storage) return;
    if (change.toasts !== undefined)
      storage.setItem(overlayStorageKeys.toasts, change.toasts ? "on" : "off");
    if (change.position !== undefined)
      storage.setItem(overlayStorageKeys.position, change.position);
    if (change.opacity !== undefined)
      storage.setItem(overlayStorageKeys.opacity, String(change.opacity));
  } catch {
    /* Preference remains usable for this window. */
  }
}
