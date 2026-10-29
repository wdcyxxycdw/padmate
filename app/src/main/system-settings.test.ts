import { readFileSync } from "node:fs";
import { describe, expect, it, vi } from "vitest";
import {
  GAME_BAR_SETTINGS_URL,
  isAllowedSettingsURL,
  openSettingsPage,
  systemSettingsHandlers,
} from "./system-settings";

const source = (path: string) =>
  readFileSync(new URL(path, import.meta.url), "utf8");

describe("the Windows Settings page the window can have opened (UI-30, ADR-0075)", () => {
  it("is the Xbox Game Bar page, which holds the button's switch", () => {
    expect(GAME_BAR_SETTINGS_URL).toBe("ms-settings:gaming-gamebar");
    expect(isAllowedSettingsURL("ms-settings:gaming-gamebar")).toBe(true);
  });

  it("is the only page of ms-settings: that is allowed", () => {
    for (const url of [
      // Other pages of Settings, near and far.
      "ms-settings:gaming-xboxnetworking",
      "ms-settings:gaming-gamemode",
      "ms-settings:gaming",
      "ms-settings:",
      "ms-settings:privacy-microphone",
      "ms-settings:windowsupdate",
      "ms-settings:network",
      "ms-settings:about",
      "ms-settings:signinoptions",
      "ms-settings:defaultapps",
      "ms-settings:apps-volume",
      // The page itself, written another way.
      "MS-SETTINGS:gaming-gamebar",
      "ms-settings:Gaming-GameBar",
      "ms-settings:gaming-gamebar ",
      " ms-settings:gaming-gamebar",
      "ms-settings:gaming-gamebar?x=1",
      "ms-settings:gaming-gamebar#x",
      "ms-settings:gaming-gamebar/",
      "ms-settings:gaming-gamebar\n",
      "ms-settings://gaming-gamebar",
      "ms-settings:gaming-gamebarx",
      "ms-settingsx:gaming-gamebar",
      "ms-settings:gaming-gamebar&ms-settings:network",
      // Other addresses and things that are not addresses.
      "https://example.com",
      "ms-gamebar:",
      "ms-gamingoverlay:",
      "file:///C:/Windows/System32/cmd.exe",
      "C:\\Windows\\System32\\cmd.exe",
      "",
      undefined,
      null,
      42,
      {},
      ["ms-settings:gaming-gamebar"],
    ])
      expect(isAllowedSettingsURL(url), String(url)).toBe(false);
  });

  it("is handed to the shell only when it is allowed", async () => {
    const open = vi.fn(async (_url: string) => {});
    await openSettingsPage(GAME_BAR_SETTINGS_URL, open);
    expect(open.mock.calls).toEqual([["ms-settings:gaming-gamebar"]]);
    open.mockClear();
    for (const url of [
      "ms-settings:network",
      "ms-settings:gaming-gamebar?x=1",
      "https://example.com",
      "",
    ]) {
      await expect(openSettingsPage(url, open), url).rejects.toThrow(
        "Not an allowed settings page",
      );
    }
    expect(open).not.toHaveBeenCalled();
  });

  it("passes on a refusal of the shell", async () => {
    const open = vi.fn(async () => {
      throw new Error("no handler");
    });
    await expect(openSettingsPage(GAME_BAR_SETTINGS_URL, open)).rejects.toThrow(
      "no handler",
    );
  });
});

describe("the window's side of it", () => {
  it("is one handler that takes no argument and opens the one page", async () => {
    const open = vi.fn(async (_url: string) => {});
    const handlers = systemSettingsHandlers(open);
    expect(Object.keys(handlers)).toEqual(["gameBarSettingsOpen"]);
    // The window sends nothing, so it cannot say what to open: Main refuses
    // a call with an argument (index.ts compares the count).
    expect(handlers.gameBarSettingsOpen.count).toBe(0);
    await handlers.gameBarSettingsOpen.run();
    expect(open.mock.calls).toEqual([["ms-settings:gaming-gamebar"]]);
  });

  it("is wired from the preload to the shell through the checks, and nowhere else", () => {
    // Renderer -> preload -> Main's handler table (which trusts the main
    // window's page only, and counts the arguments) -> the one address.
    expect(source("../preload/index.ts")).toContain(
      'openGameBarSettings: () => invoke("padmate:gameBarSettingsOpen"),',
    );
    expect(source("../shared/api.ts")).toContain(
      "openGameBarSettings(): Promise<void>;",
    );
    expect(source("./index.ts")).toContain(
      "...systemSettingsHandlers((url) => shell.openExternal(url)),",
    );
    // The page the window ships has no way to ask for another address.
    expect(source("../preload/index.ts")).not.toContain("ms-settings");
    expect(source("../renderer/pages/SettingsFunctionKey.tsx")).not.toContain(
      "ms-settings",
    );
  });
});
