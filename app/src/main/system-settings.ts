import type { EditorHandler } from "./library-handlers";

/**
 * The one page of Windows Settings the window can ask Main to open (UI-30,
 * ADR-0075): Gaming → Game Bar, where the switch "Allow your controller to
 * open Game Bar" lives ("Open Xbox Game Bar using this button on a
 * controller" on older Windows). The Xbox button of a controller
 * opens the Game Bar through Windows itself, which padmate cannot stop and
 * does not change (it changes no system setting); a user who makes that
 * button the function key turns the switch off there.
 */
export const GAME_BAR_SETTINGS_URL = "ms-settings:gaming-gamebar";

/**
 * Whether Main may open this address as Windows Settings: exactly the Game
 * Bar page, not another page of ms-settings: and not the same page with a
 * query, a fragment or other letter case, so that only one fixed address is
 * ever handed to the shell.
 */
export function isAllowedSettingsURL(url: unknown): boolean {
  return url === GAME_BAR_SETTINGS_URL;
}

/** Opens url through open (the shell) when it is an allowed settings page. */
export async function openSettingsPage(
  url: string,
  open: (url: string) => Promise<void>,
): Promise<void> {
  if (!isAllowedSettingsURL(url))
    throw new Error("Not an allowed settings page");
  await open(url);
}

/**
 * The window's side of it. The window sends no address: Main opens the fixed
 * one, through openSettingsPage.
 */
export function systemSettingsHandlers(
  open: (url: string) => Promise<void>,
): Record<string, EditorHandler> {
  return {
    gameBarSettingsOpen: {
      count: 0,
      run: () => openSettingsPage(GAME_BAR_SETTINGS_URL, open),
    },
  };
}
