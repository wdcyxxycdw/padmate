import { describe, expect, it, vi } from "vitest";
import type { DaemonEvent } from "../shared/api";
import {
  defaultOverlayPrefs,
  type OverlayPrefs,
  parseOverlayPrefs,
} from "../shared/overlay";
import { OverlayController } from "./overlay";
import { OverlayPreferences, type PrefsTarget } from "./overlay-prefs";
import { OverlayToaster } from "./overlay-toasts";

function target() {
  const received: Array<Partial<OverlayPrefs>> = [];
  const fake: PrefsTarget = { configure: (prefs) => received.push(prefs) };
  return { fake, received };
}

describe("what the UI pushes about the overlay (HUD-05)", () => {
  it("is the defaults until the first push", () => {
    const preferences = new OverlayPreferences(() => []);
    expect(preferences.value).toEqual(defaultOverlayPrefs);
    expect(preferences.value).toEqual({
      language: "zh-CN",
      toasts: true,
      position: "bottom",
      opacity: 100,
    });
    // A copy: the caller cannot change what Main keeps.
    const value = preferences.value;
    value.opacity = 50;
    expect(preferences.value.opacity).toBe(100);
  });

  it("hands the valid fields of a push to every target, and keeps them", () => {
    const first = target();
    const second = target();
    const preferences = new OverlayPreferences(() => [first.fake, second.fake]);
    preferences.push({
      language: "en",
      toasts: false,
      position: "top",
      opacity: 70,
    });
    const all = {
      language: "en",
      toasts: false,
      position: "top",
      opacity: 70,
    };
    expect(first.received).toEqual([all]);
    expect(second.received).toEqual([all]);
    expect(preferences.value).toEqual(all);
    // A later push changes the fields it has.
    preferences.push({ opacity: 45 });
    expect(preferences.value).toEqual({ ...all, opacity: 45 });
    expect(first.received.at(-1)).toEqual({ opacity: 45 });
  });

  it("ignores an invalid field and applies the valid ones", () => {
    const { fake, received } = target();
    const preferences = new OverlayPreferences(() => [fake]);
    preferences.push({
      language: "fr",
      toasts: "no",
      position: "top",
      opacity: 10,
    });
    expect(received).toEqual([{ position: "top" }]);
    expect(preferences.value).toEqual({
      ...defaultOverlayPrefs,
      position: "top",
    });
    // An invalid field does not undo a good one from before.
    preferences.push({ position: "left", opacity: 1000, toasts: false });
    expect(preferences.value).toEqual({
      ...defaultOverlayPrefs,
      position: "top",
      toasts: false,
    });
  });

  it("ignores a push that is not an object at all", () => {
    const { fake, received } = target();
    const preferences = new OverlayPreferences(() => [fake]);
    preferences.push({ opacity: 80 });
    for (const raw of [undefined, null, "top", 5, true, [], [{ opacity: 60 }]])
      expect(() => preferences.push(raw), String(raw)).not.toThrow();
    expect(preferences.value.opacity).toBe(80);
    // The targets are still called, with nothing to apply.
    expect(received.at(-1)).toEqual({});
    expect(received.filter((prefs) => Object.keys(prefs).length)).toEqual([
      { opacity: 80 },
    ]);
  });

  it("asks for its targets at each push: the overlay is made later and may be gone", () => {
    let overlay: PrefsTarget | undefined;
    const toaster = target();
    const preferences = new OverlayPreferences(() => [toaster.fake, overlay]);
    expect(() => preferences.push({ position: "top" })).not.toThrow();
    const late = target();
    overlay = late.fake;
    preferences.push({ opacity: 60 });
    expect(late.received).toEqual([{ opacity: 60 }]);
    overlay = undefined;
    expect(() => preferences.push({ opacity: 50 })).not.toThrow();
    expect(toaster.received).toHaveLength(3);
  });

  it("does not let one target that fails keep the others from the push, and does not throw", () => {
    const other = target();
    const preferences = new OverlayPreferences(() => [
      {
        configure: () => {
          throw new Error("the overlay is broken");
        },
      },
      other.fake,
    ]);
    expect(() => preferences.push({ toasts: false })).not.toThrow();
    expect(other.received).toEqual([{ toasts: false }]);
    expect(preferences.value.toasts).toBe(false);
  });

  it("is what an overlay made later starts with", () => {
    const preferences = new OverlayPreferences(() => []);
    preferences.push({ position: "top", opacity: 65 });
    const value = preferences.value;
    expect(value).toMatchObject({ position: "top", opacity: 65 });
    // The controller takes its position and opacity from it.
    const created: unknown[] = [];
    const controller = new OverlayController(
      {
        preload: "overlay.js",
        secondaryDisplay: false,
        displays: () => ({
          primaryId: 1,
          displays: [
            { id: 1, workArea: { x: 0, y: 0, width: 1920, height: 1040 } },
          ],
        }),
        onDisplaysChanged: () => {},
        create: (options) => {
          created.push(options);
          throw new Error("only the options are looked at");
        },
      },
      value,
    );
    controller.start();
    expect(created).toHaveLength(1);
    expect(created[0]).toMatchObject({
      x: 720,
      y: 48,
      width: 480,
      height: 160,
    });
  });

  it("gates and translates the toasts through the toaster, and moves the overlay", () => {
    const shown: string[] = [];
    const toaster = new OverlayToaster({
      mainFocused: () => false,
      show: (text) => shown.push(text),
    });
    const configure = vi.fn();
    const preferences = new OverlayPreferences(() => [toaster, { configure }]);
    const state = (paused: boolean): DaemonEvent => ({
      topic: "engine.activity",
      data: { kind: "state", paused, slots: [] },
    });
    toaster.handle(state(false));
    toaster.handle(state(true));
    expect(shown).toEqual(["已暂停"]);
    preferences.push({ language: "en", position: "top" });
    toaster.handle(state(false));
    expect(shown).toEqual(["已暂停", "Resumed"]);
    preferences.push({ toasts: false });
    toaster.handle(state(true));
    expect(shown).toEqual(["已暂停", "Resumed"]);
    preferences.push({ toasts: true });
    toaster.handle(state(false));
    expect(shown).toEqual(["已暂停", "Resumed", "Resumed"]);
    expect(configure).toHaveBeenCalledWith({ language: "en", position: "top" });
  });

  it("is checked by the same rule the shared parser has", () => {
    // What is kept is what parseOverlayPrefs lets through, nothing more.
    const raw = {
      language: "en",
      toasts: false,
      position: "top",
      opacity: 90,
      extra: "x",
    };
    const preferences = new OverlayPreferences(() => []);
    preferences.push(raw);
    expect(preferences.value).toEqual({
      ...defaultOverlayPrefs,
      ...parseOverlayPrefs(raw),
    });
    expect(Object.keys(preferences.value).sort()).toEqual([
      "language",
      "opacity",
      "position",
      "toasts",
    ]);
  });
});
