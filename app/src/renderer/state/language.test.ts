// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PadmateAPI, SavedLanguage } from "../../shared/api";
import { editorAPIFixture } from "../testing/editor-api-fixture";
import { createPadmateStore, initialLanguage } from "./store";

beforeEach(() => localStorage.clear());
afterEach(() => vi.restoreAllMocks());

function make(savedLanguage?: SavedLanguage | null) {
  const api = {
    ...editorAPIFixture(),
    getConnection: vi.fn(async () => ({ phase: "disconnected" as const })),
    onConnection: vi.fn(() => () => {}),
    onEvent: vi.fn(() => () => {}),
  } as unknown as PadmateAPI & { setLanguage: ReturnType<typeof vi.fn> };
  return { api, store: createPadmateStore(api, { savedLanguage }) };
}

const installer = (language: SavedLanguage["language"]): SavedLanguage => ({
  language,
  setBy: "installer",
});
const settings = (language: SavedLanguage["language"]): SavedLanguage => ({
  language,
  setBy: "settings",
});

describe("the window's first language (ADR-0018)", () => {
  it("is Chinese with no file and no pick", () => {
    expect(initialLanguage(undefined)).toBe("zh-CN");
    expect(initialLanguage(null)).toBe("zh-CN");
    expect(make().store.getState().language).toBe("zh-CN");
  });

  it("is the installer's when the person has picked none", () => {
    expect(initialLanguage(installer("en"))).toBe("en");
    expect(make(installer("en")).store.getState().language).toBe("en");
    expect(make(installer("zh-CN")).store.getState().language).toBe("zh-CN");
  });

  it("is the person's pick, whatever the installer wrote after it", () => {
    localStorage.setItem("padmate.language", "zh-CN");
    expect(make(installer("en")).store.getState().language).toBe("zh-CN");
    localStorage.setItem("padmate.language", "en");
    expect(make(installer("zh-CN")).store.getState().language).toBe("en");
    expect(make(settings("en")).store.getState().language).toBe("en");
  });

  it("ignores a stored value that is no language", () => {
    localStorage.setItem("padmate.language", "fr");
    expect(make(installer("en")).store.getState().language).toBe("en");
  });

  it("writes the person's pick into the file, where the installer sees it", () => {
    const { api, store } = make(installer("en"));
    store.getState().setLanguage("zh-CN");
    expect(store.getState().language).toBe("zh-CN");
    expect(localStorage.getItem("padmate.language")).toBe("zh-CN");
    expect(api.setLanguage).toHaveBeenCalledWith("zh-CN");
  });

  it("tells the file once at start of a pick only localStorage has, so that an install leaves it", () => {
    localStorage.setItem("padmate.language", "en");
    for (const saved of [
      null,
      installer("en"),
      installer("zh-CN"),
      settings("zh-CN"),
    ]) {
      const { api, store } = make(saved);
      expect(api.setLanguage).not.toHaveBeenCalled();
      const stop = store.getState().start();
      expect(api.setLanguage).toHaveBeenCalledTimes(1);
      expect(api.setLanguage).toHaveBeenCalledWith("en");
      stop();
    }
  });

  it("writes nothing at start when the file has the pick, or there is no pick", () => {
    localStorage.setItem("padmate.language", "en");
    let { api, store } = make(settings("en"));
    let stop = store.getState().start();
    expect(api.setLanguage).not.toHaveBeenCalled();
    stop();
    localStorage.clear();
    ({ api, store } = make(installer("en")));
    stop = store.getState().start();
    expect(api.setLanguage).not.toHaveBeenCalled();
    stop();
  });

  it("keeps the pick when the file cannot be written", async () => {
    const { api, store } = make();
    api.setLanguage.mockRejectedValue(new Error("disk full"));
    store.getState().setLanguage("en");
    await Promise.resolve();
    expect(store.getState().language).toBe("en");
    expect(localStorage.getItem("padmate.language")).toBe("en");
  });
});
