// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PadmateAPI } from "../shared/api";
import { editorAPIFixture } from "./editor-api-fixture";
import {
  defaultOverlaySettings,
  overlayStorageKeys,
  savedOverlaySettings,
  saveOverlaySettings,
  validOverlayChange,
} from "./overlay-settings";
import { createPadmateStore } from "./store";

beforeEach(() => localStorage.clear());
afterEach(() => vi.restoreAllMocks());

describe("where the overlay's settings are kept (HUD-05)", () => {
  it("uses keys of its own in the UI's localStorage, next to the language", () => {
    expect(overlayStorageKeys).toEqual({
      toasts: "padmate.overlay.toasts",
      position: "padmate.overlay.position",
      opacity: "padmate.overlay.opacity",
    });
  });

  it("is toasts on, at the bottom, fully opaque when nothing is kept", () => {
    expect(savedOverlaySettings()).toEqual({
      toasts: true,
      position: "bottom",
      opacity: 100,
    });
    expect(defaultOverlaySettings).toEqual(savedOverlaySettings());
  });

  it('reads "off" as off and anything else as on', () => {
    for (const [value, toasts] of [
      ["off", false],
      ["on", true],
      ["", true],
      ["OFF", true],
      ["false", true],
      ["0", true],
    ] as const) {
      localStorage.setItem(overlayStorageKeys.toasts, value);
      expect(savedOverlaySettings().toasts, value).toBe(toasts);
    }
  });

  it('reads "top" as top and anything else as bottom', () => {
    for (const [value, position] of [
      ["top", "top"],
      ["bottom", "bottom"],
      ["Top", "bottom"],
      ["", "bottom"],
      ["left", "bottom"],
    ] as const) {
      localStorage.setItem(overlayStorageKeys.position, value);
      expect(savedOverlaySettings().position, value).toBe(position);
    }
  });

  it("reads an opacity that is a whole number from 40 to 100, else 100", () => {
    for (const value of ["40", "45", "47", "70", "99", "100"]) {
      localStorage.setItem(overlayStorageKeys.opacity, value);
      expect(savedOverlaySettings().opacity, value).toBe(Number(value));
    }
    for (const value of [
      "39",
      "101",
      "0",
      "-50",
      "7.5",
      "70.0",
      "",
      " 70",
      "70%",
      "abc",
      "1e2",
      "0x46",
      "1000",
      "NaN",
    ]) {
      localStorage.setItem(overlayStorageKeys.opacity, value);
      expect(savedOverlaySettings().opacity, value).toBe(100);
    }
  });

  it("falls back to the defaults when the storage cannot be read", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(savedOverlaySettings()).toEqual(defaultOverlaySettings);
  });

  it("writes only the fields it is given", () => {
    saveOverlaySettings({ toasts: false });
    expect(localStorage.getItem(overlayStorageKeys.toasts)).toBe("off");
    expect(localStorage.getItem(overlayStorageKeys.position)).toBeNull();
    expect(localStorage.getItem(overlayStorageKeys.opacity)).toBeNull();
    saveOverlaySettings({ toasts: true, position: "top", opacity: 75 });
    expect(localStorage.getItem(overlayStorageKeys.toasts)).toBe("on");
    expect(localStorage.getItem(overlayStorageKeys.position)).toBe("top");
    expect(localStorage.getItem(overlayStorageKeys.opacity)).toBe("75");
    expect(savedOverlaySettings()).toEqual({
      toasts: true,
      position: "top",
      opacity: 75,
    });
  });

  it("does not fail when the storage cannot be written", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota");
    });
    expect(() =>
      saveOverlaySettings({ toasts: false, position: "top", opacity: 50 }),
    ).not.toThrow();
  });

  it("keeps the valid fields of a change", () => {
    expect(
      validOverlayChange({ toasts: false, position: "top", opacity: 55 }),
    ).toEqual({ toasts: false, position: "top", opacity: 55 });
    expect(
      validOverlayChange({
        toasts: "no",
        position: "left",
        opacity: 30,
      } as never),
    ).toEqual({});
    expect(validOverlayChange({ position: "bottom", opacity: 72.5 })).toEqual({
      position: "bottom",
    });
    expect(validOverlayChange({})).toEqual({});
  });
});

describe("the store pushes the overlay's settings to Main (HUD-05)", () => {
  function make(overrides: Partial<PadmateAPI> = {}) {
    const api = {
      ...editorAPIFixture(),
      getConnection: vi.fn(async () => ({ phase: "disconnected" as const })),
      onConnection: vi.fn(() => () => {}),
      onEvent: vi.fn(() => () => {}),
      ...overrides,
    } as unknown as PadmateAPI & {
      setOverlayPrefs: ReturnType<typeof vi.fn>;
    };
    return { api, store: createPadmateStore(api) };
  }
  const defaults = {
    language: "zh-CN",
    toasts: true,
    position: "bottom",
    opacity: 100,
  };

  it("starts from what localStorage holds", () => {
    localStorage.setItem(overlayStorageKeys.toasts, "off");
    localStorage.setItem(overlayStorageKeys.position, "top");
    localStorage.setItem(overlayStorageKeys.opacity, "60");
    const { store } = make();
    expect(store.getState().overlay).toEqual({
      toasts: false,
      position: "top",
      opacity: 60,
    });
  });

  it("pushes all of it at start, the language included", () => {
    localStorage.setItem("padmate.language", "en");
    localStorage.setItem(overlayStorageKeys.opacity, "85");
    const { api, store } = make();
    expect(api.setOverlayPrefs).not.toHaveBeenCalled();
    const stop = store.getState().start();
    expect(api.setOverlayPrefs).toHaveBeenCalledTimes(1);
    expect(api.setOverlayPrefs).toHaveBeenCalledWith({
      language: "en",
      toasts: true,
      position: "bottom",
      opacity: 85,
    });
    stop();
  });

  it("pushes each change with the other settings and the language, and keeps it", () => {
    const { api, store } = make();
    store.getState().setOverlay({ toasts: false });
    expect(store.getState().overlay.toasts).toBe(false);
    expect(localStorage.getItem(overlayStorageKeys.toasts)).toBe("off");
    expect(api.setOverlayPrefs).toHaveBeenLastCalledWith({
      ...defaults,
      toasts: false,
    });
    store.getState().setOverlay({ position: "top" });
    expect(localStorage.getItem(overlayStorageKeys.position)).toBe("top");
    expect(api.setOverlayPrefs).toHaveBeenLastCalledWith({
      ...defaults,
      toasts: false,
      position: "top",
    });
    store.getState().setOverlay({ opacity: 65, toasts: true });
    expect(localStorage.getItem(overlayStorageKeys.opacity)).toBe("65");
    expect(localStorage.getItem(overlayStorageKeys.toasts)).toBe("on");
    expect(api.setOverlayPrefs).toHaveBeenLastCalledWith({
      ...defaults,
      position: "top",
      opacity: 65,
    });
    expect(api.setOverlayPrefs).toHaveBeenCalledTimes(3);
    // A new window reads the same from the storage.
    expect(savedOverlaySettings()).toEqual(store.getState().overlay);
  });

  it("pushes the language when it changes", () => {
    const { api, store } = make();
    store.getState().setLanguage("en");
    expect(localStorage.getItem("padmate.language")).toBe("en");
    expect(api.setOverlayPrefs).toHaveBeenLastCalledWith({
      ...defaults,
      language: "en",
    });
    store.getState().setLanguage("zh-CN");
    expect(api.setOverlayPrefs).toHaveBeenLastCalledWith(defaults);
  });

  it("ignores an invalid value, and a change that changes nothing pushes nothing", () => {
    const { api, store } = make();
    store.getState().setOverlay({ opacity: 12 });
    store.getState().setOverlay({ opacity: 72.5 });
    store.getState().setOverlay({ position: "left" as never });
    store.getState().setOverlay({ toasts: "off" as never });
    store.getState().setOverlay({});
    store.getState().setOverlay({ toasts: true, position: "bottom" });
    expect(store.getState().overlay).toEqual(defaultOverlaySettings);
    expect(api.setOverlayPrefs).not.toHaveBeenCalled();
    expect(localStorage.length).toBe(0);
  });

  it("takes the valid part of a change that is partly invalid", () => {
    const { api, store } = make();
    store.getState().setOverlay({ opacity: 12, position: "top" });
    expect(store.getState().overlay).toEqual({
      toasts: true,
      position: "top",
      opacity: 100,
    });
    expect(api.setOverlayPrefs).toHaveBeenLastCalledWith({
      ...defaults,
      position: "top",
    });
  });

  it("works when Main refuses the push or the call fails", async () => {
    const unhandled = vi.fn();
    process.on("unhandledRejection", unhandled);
    try {
      const rejecting = make({
        setOverlayPrefs: vi.fn(async () => {
          throw new Error("Untrusted UI caller");
        }),
      });
      expect(() =>
        rejecting.store.getState().setOverlay({ opacity: 50 }),
      ).not.toThrow();
      expect(rejecting.store.getState().overlay.opacity).toBe(50);
      const throwing = make({
        setOverlayPrefs: vi.fn(() => {
          throw new Error("no such call");
        }),
      });
      expect(() =>
        throwing.store.getState().setOverlay({ toasts: false }),
      ).not.toThrow();
      expect(() => throwing.store.getState().setLanguage("en")).not.toThrow();
      expect(throwing.store.getState().overlay.toasts).toBe(false);
      await new Promise((done) => setTimeout(done, 0));
      expect(unhandled).not.toHaveBeenCalled();
    } finally {
      process.off("unhandledRejection", unhandled);
    }
  });

  it("keeps a setting for this window when the storage cannot be written", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota");
    });
    const { api, store } = make();
    store.getState().setOverlay({ position: "top" });
    expect(store.getState().overlay.position).toBe("top");
    expect(api.setOverlayPrefs).toHaveBeenLastCalledWith({
      ...defaults,
      position: "top",
    });
  });
});
